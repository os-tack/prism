// Package plugins contains the projection plugins that translate a canonical
// .agents/ model into per-tool config files.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/version"
)

// GeminiPlugin projects a model.Project into Gemini CLI's on-disk layout:
//
//   - GEMINI.md (root + per-scope, hierarchical cascade)
//   - .gemini/agents/<name>.md (sub-agents, with YAML frontmatter)
//   - .gemini/commands/<name>.toml (slash commands — TOML, not markdown)
//   - .gemini/settings.json (mcpServers + hooks; user keys preserved)
//   - .gemini/hooks/__scope-guard__/<scope>-<event>-<basename>.sh (scoped hooks)
//   - .gemini/hooks/__perms-guard__/... (prism perms-guard sidecar + wrappers)
//
// Skills do not have a dedicated Gemini primitive; they are projected as
// sub-agents under .gemini/agents/ with the skill's trigger expressed in
// the agent's description. This is a degraded mapping (no native glob
// auto-trigger) — surfaced via Capabilities().Skills = SupportDegraded.
type GeminiPlugin struct {
	// DisableHookWrappers, when true, suppresses both the perms-guard
	// wrapper script + sidecar policy emission AND the scope-guard
	// wrappers for scoped hooks. Default false (wrappers ON). Mirrors
	// ClaudePlugin.DisableHookWrappers.
	DisableHookWrappers bool
}

// NewGemini returns a fresh GeminiPlugin.
func NewGemini() *GeminiPlugin {
	return &GeminiPlugin{}
}

// Name is the stable plugin identifier.
func (p *GeminiPlugin) Name() string {
	return "gemini"
}

// Detect returns true if a .gemini/ directory or a GEMINI.md file is present
// at the given project root.
func (p *GeminiPlugin) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".gemini")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "GEMINI.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Capabilities returns the capability matrix entry for Gemini CLI.
//
// Per-field declarations match SPEC §12 (Gemini column, "gem"). v0.8 coarse
// cells are preserved for back-compat; v2 FieldCapabilities maps reflect
// the per-field tier for every non-native cell in the spec. Fields not
// listed in a map default to FieldNative.
func (p *GeminiPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,   // hierarchical cascade
		ScopeSemantic: plugin.SupportDegraded, // no native trigger description
		// Gemini has no skill primitive; skills are projected as agents
		// with the trigger embedded in the description (no auto-glob).
		Skills:      plugin.SupportDegraded,
		Commands:    plugin.SupportNative, // .gemini/commands/<name>.toml
		Agents:      plugin.SupportNative, // .gemini/agents/<name>.md
		Hooks:       plugin.SupportNative, // settings.json hooks block
		Permissions: plugin.SupportNative, // via prism perms-guard wrapper + sidecar policy
		MCP:         plugin.SupportNative,

		// SPEC §12 Agent / gem column.
		AgentFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"gemini"},
			Fields: map[string]plugin.FieldSupport{
				"DisallowedTools":  plugin.FieldDegraded,
				"ReadOnly":         plugin.FieldDegraded,
				"Background":       plugin.FieldSilent,
				"ModelFallbacks":   plugin.FieldSilent,
				"AllowedSubagents": plugin.FieldUnsupported,
				"UserInvocable":    plugin.FieldSilent,
				"ModelInvocable":   plugin.FieldSilent,
				"InitialPrompt":    plugin.FieldUnsupported,
				"ScopePath":        plugin.FieldDegraded,
			},
		},

		// SPEC §12 Skill / gem column. Globs / ContentRegex and the
		// "Always" / "Glob" activation modes are unsupported on Gemini
		// (skills project as agents without auto-trigger).
		SkillFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"gemini"},
			Fields: map[string]plugin.FieldSupport{
				"WhenToUse":                 plugin.FieldDegraded,
				"Activation.Modes.Always":   plugin.FieldUnsupported,
				"Activation.Modes.Glob":     plugin.FieldUnsupported,
				"Activation.Globs":          plugin.FieldUnsupported,
				"Activation.ContentRegex":   plugin.FieldUnsupported,
				"Activation.UserInvocable":  plugin.FieldSilent,
				"Activation.ModelInvocable": plugin.FieldSilent,
				"AllowedTools":              plugin.FieldUnsupported,
				"Arguments":                 plugin.FieldDegraded,
				"Model":                     plugin.FieldUnsupported,
				"Subagent":                  plugin.FieldUnsupported,
				"ScopePath":                 plugin.FieldDegraded,
			},
		},

		// SPEC §12 Command / gem column.
		CommandFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"gemini"},
			Fields: map[string]plugin.FieldSupport{
				"ArgumentHint": plugin.FieldSilent,
				"Arguments":    plugin.FieldDegraded,
				"Model":        plugin.FieldUnsupported,
				"Tools":        plugin.FieldUnsupported,
				"Agent":        plugin.FieldUnsupported,
				"AutoInvoke":   plugin.FieldSilent,
				"ScopePath":    plugin.FieldDegraded,
			},
		},

		// SPEC §12 Hook / gem column. Claude-only events (PreCompact,
		// SessionResume, …) are unsupported; the per-action canonical
		// events (PreShell, PostFileEdit, …) translate to BeforeTool +
		// matcher via hook_envelope.MapHookEventFor — see Plan() TODO.
		HookFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"gemini"},
			Fields: map[string]plugin.FieldSupport{
				"Event.ClaudeOnly":  plugin.FieldUnsupported,
				"Handlers.http":     plugin.FieldUnsupported,
				"Handlers.mcp_tool": plugin.FieldUnsupported,
				"Handlers.prompt":   plugin.FieldUnsupported,
				"Handlers.agent":    plugin.FieldUnsupported,
				"StatusMessage":     plugin.FieldSilent,
				"Async":             plugin.FieldUnsupported,
				"FailClosed":        plugin.FieldUnsupported,
				"Once":              plugin.FieldUnsupported,
				"If":                plugin.FieldUnsupported,
				"Cwd":               plugin.FieldSilent,
				"Env":               plugin.FieldUnsupported,
				"Bash+Powershell":   plugin.FieldUnsupported,
				// ScopePath is native via the scope-guard wrapper family
				// (SPEC §12 footnote ¹).
			},
		},

		// SPEC §12 MCPServer / gem column. Most fields are native;
		// AutoApprove and oauth auth degrade; ScopePath degrades.
		MCPServerFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"gemini"},
			Fields: map[string]plugin.FieldSupport{
				"Auth.Scheme.oauth": plugin.FieldDegraded,
				"AutoApprove":       plugin.FieldDegraded,
				"ScopePath":         plugin.FieldDegraded,
			},
		},

		// SPEC §12 Permissions / gem column — all native via the
		// perms-guard sidecar (footnote ¹).
		PermissionsFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"gemini"},
			Fields:     map[string]plugin.FieldSupport{},
		},

		// SPEC §12 Scope / gem column. Globs / glob-based activation
		// have no Gemini analog; Name degrades, Description / Tags
		// drop silently, Priority synthesizes as cascade depth.
		ScopeFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"gemini"},
			Fields: map[string]plugin.FieldSupport{
				"Name":                     plugin.FieldDegraded,
				"Description":              plugin.FieldSilent,
				"Globs":                    plugin.FieldUnsupported,
				"Activation.Always":        plugin.FieldDegraded,
				"Activation.Glob":          plugin.FieldUnsupported,
				"Activation.Manual":        plugin.FieldUnsupported,
				"Activation.ModelDecision": plugin.FieldUnsupported,
				"Priority":                 plugin.FieldDegraded,
				"Tags":                     plugin.FieldSilent,
				"IsOverride":               plugin.FieldUnsupported,
			},
		},
	}
}

// geminiHookEventMap translates prism's canonical Hook.Event values into
// Gemini CLI's 11 supported event identifiers. Claude's PreToolUse /
// PostToolUse map to BeforeTool / AfterTool; the rest pass through when
// they already match a Gemini event name. Unknown events pass through
// untouched (Gemini will warn at load time rather than silently dropping
// them).
//
// Reference Gemini events: SessionStart, SessionEnd, BeforeAgent,
// AfterAgent, BeforeModel, AfterModel, BeforeToolSelection, BeforeTool,
// AfterTool, PreCompress, Notification.
var geminiHookEventMap = map[string]string{
	"PreToolUse":          "BeforeTool",
	"PostToolUse":         "AfterTool",
	"SessionStart":        "SessionStart",
	"SessionEnd":          "SessionEnd",
	"BeforeAgent":         "BeforeAgent",
	"AfterAgent":          "AfterAgent",
	"BeforeModel":         "BeforeModel",
	"AfterModel":          "AfterModel",
	"BeforeToolSelection": "BeforeToolSelection",
	"BeforeTool":          "BeforeTool",
	"AfterTool":           "AfterTool",
	"PreCompress":         "PreCompress",
	"Notification":        "Notification",
}

// mapGeminiHookEvent returns the Gemini event for a prism canonical event,
// passing through unknown names unchanged.
func mapGeminiHookEvent(event string) string {
	if mapped, ok := geminiHookEventMap[event]; ok {
		return mapped
	}
	return event
}

// Plan produces the Operations needed to project proj into Gemini CLI's layout.
func (p *GeminiPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	mode := plugin.ModeSymlink
	switch opts.Mode {
	case "write":
		mode = plugin.ModeWrite
	case "symlink", "":
		mode = plugin.ModeSymlink
	default:
		return nil, fmt.Errorf("gemini: unknown mode %q (want \"write\" or \"symlink\")", opts.Mode)
	}

	if proj == nil {
		return nil, nil
	}

	var ops []plugin.Operation
	var warnings []plugin.Warning

	// Root GEMINI.md.
	if proj.Context != nil {
		op, err := buildGeminiContextOp(proj, proj.Context, "GEMINI.md", mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Per-scope GEMINI.md files (hierarchical cascade — Gemini walks up from
	// cwd merging GEMINI.md at each level).
	for _, sc := range proj.Scopes {
		if sc == nil || sc.Document == nil {
			continue
		}
		path := filepath.Join(sc.Path, "GEMINI.md")
		op, err := buildGeminiContextOp(proj, sc.Document, path, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Agents → .gemini/agents/<name>.md with YAML frontmatter. Scoped
	// agents are degraded — Gemini CLI has no per-path agent scoping —
	// so we prefix the name with the scope slug and emit an info warning.
	for _, ag := range proj.Agents {
		if ag == nil {
			continue
		}
		fileName := scopedName(ag.ScopePath, ag.Name) + ".md"
		path := filepath.Join(".gemini", "agents", fileName)
		body := ""
		var srcTag string
		if ag.Document != nil {
			body = ag.Document.Body
			srcTag = proj.SourceTag(ag.Document.SourcePath)
		}
		content := renderGeminiAgent(ag.Name, ag.Description, body, geminiExtensions(ag.Extensions))
		sources := []string{}
		if srcTag != "" {
			sources = append(sources, srcTag)
		} else {
			sources = append(sources, filepath.Join("agents", ag.Name+".md"))
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: content,
			Mode:    plugin.ModeWrite,
			Sources: sources,
			Plugin:  "gemini",
		}
		if ag.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   srcTag,
				Message:  fmt.Sprintf("scoped agent %q projected without path enforcement (Gemini agents are global)", ag.Name),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Skills → projected as agents with the trigger embedded in the
	// description (Gemini has no skill primitive). Degraded.
	for _, sk := range proj.Skills {
		if sk == nil {
			continue
		}
		fileName := scopedName(sk.ScopePath, sk.Name) + ".md"
		path := filepath.Join(".gemini", "agents", fileName)
		body := ""
		var srcTag string
		if sk.Document != nil {
			body = sk.Document.Body
			srcTag = proj.SourceTag(sk.Document.SourcePath)
		}
		desc := sk.Description
		if sk.Trigger != "" {
			if desc != "" {
				desc = desc + " — trigger: " + sk.Trigger
			} else {
				desc = "trigger: " + sk.Trigger
			}
		}
		// v2 additive read: prefer the v0.8 slice if populated, fall back
		// to Activation.Globs (SPEC §4.2.2). Either source projects into
		// the description string — the emission shape is unchanged.
		globs := sk.Globs
		if len(globs) == 0 && len(sk.Activation.Globs) > 0 {
			globs = sk.Activation.Globs
		}
		if len(globs) > 0 {
			if desc != "" {
				desc = desc + " (globs: " + strings.Join(globs, ", ") + ")"
			} else {
				desc = "globs: " + strings.Join(globs, ", ")
			}
		}
		content := renderGeminiAgent(sk.Name, desc, body, geminiExtensions(sk.Extensions))
		sources := []string{}
		if srcTag != "" {
			sources = append(sources, srcTag)
		} else {
			sources = append(sources, filepath.Join("skills", sk.Name, "SKILL.md"))
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: content,
			Mode:    plugin.ModeWrite,
			Sources: sources,
			Plugin:  "gemini",
		}
		// Always warn — even a global skill loses auto-trigger semantics
		// when projected as an agent.
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   srcTag,
			Message:  fmt.Sprintf("skill %q projected as Gemini agent (no native skill primitive; trigger embedded in description)", sk.Name),
			Severity: "info",
		})
		if sk.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   srcTag,
				Message:  fmt.Sprintf("scoped skill %q (scope: %s) projected without path enforcement", sk.Name, sk.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Commands → .gemini/commands/<name>.toml. Scoped commands degraded
	// (prefixed name + info warning).
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		fileName := scopedName(cmd.ScopePath, cmd.Name) + ".toml"
		path := filepath.Join(".gemini", "commands", fileName)
		body := ""
		var srcTag string
		if cmd.Document != nil {
			body = cmd.Document.Body
			srcTag = proj.SourceTag(cmd.Document.SourcePath)
		}
		content := renderGeminiCommand(cmd.Description, body, geminiExtensions(cmd.Extensions))
		sources := []string{}
		if srcTag != "" {
			sources = append(sources, srcTag)
		} else {
			sources = append(sources, filepath.Join("commands", cmd.Name+".md"))
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: content,
			Mode:    plugin.ModeWrite,
			Sources: sources,
			Plugin:  "gemini",
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   srcTag,
				Message:  fmt.Sprintf("scoped command %q projected without path enforcement (Gemini commands are global)", cmd.Name),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (must be emitted before the settings op
	// so settings can reference the wrapper's path). Each scoped hook
	// gets its own wrapper under .gemini/hooks/__scope-guard__/.
	wrapperPaths := map[*model.Hook]string{} // hook → project-relative wrapper path
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		hookBase := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		// v2 additive read: prefer canonical event when v0.8 is empty
		// (SPEC §4.4.2). Emission shape unchanged — both feed the same
		// mapGeminiHookEvent lookup.
		// TODO(prism v0.9): replace mapGeminiHookEvent with
		// MapHookEventFor("gemini", h.EventCanonical) so per-action
		// canonical events (PreShell, PostFileEdit, …) translate to
		// BeforeTool + matcher form. Translation table lives in
		// plugins/hook_envelope.go. See SPEC §4.4.4.
		rawEvent := h.Event
		if rawEvent == "" && h.EventCanonical != "" {
			rawEvent = string(h.EventCanonical)
		}
		mappedEvent := mapGeminiHookEvent(rawEvent)
		wrapperFile := scopeSlug(h.ScopePath) + "-" + mappedEvent + "-" + hookBase + ".sh"
		wrapperRel := filepath.Join(".gemini", "hooks", "__scope-guard__", wrapperFile)

		body := buildScopeGuardScript(wrapperRel, h.ScopePath, h.ScriptPath, formatScriptArg(h.ScriptPath, proj.Root))
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     wrapperRel,
			Content:  body,
			Mode:     plugin.ModeWrite,
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   "gemini",
		})
		wrapperPaths[h] = wrapperRel
	}

	// .gemini/settings.json — merges existing user keys with our
	// mcpServers + hooks blocks. Emitted whenever there is any MCP
	// server or any hook.
	if len(proj.MCP) > 0 || len(proj.Hooks) > 0 {
		settingsOp, err := buildGeminiSettingsOp(proj, wrapperPaths)
		if err != nil {
			return nil, err
		}
		ops = append(ops, settingsOp)
	}

	// Permissions (global + scoped) project via prism perms-guard wrappers
	// (same machinery as continue.go).
	wrapperOps, wrapperWarnings, err := emitPermsGuardWrappers(p.Name(), proj, p.DisableHookWrappers)
	if err != nil {
		return nil, err
	}
	ops = append(ops, wrapperOps...)
	warnings = append(warnings, wrapperWarnings...)

	// Scoped MCP servers degrade to project-global merges (Gemini has no
	// per-scope MCP).
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" || srv.ScopePath == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join(srv.ScopePath, "mcp.yaml"),
			Message:  fmt.Sprintf("Gemini has no per-scope MCP; scoped MCP server %q (scope: %s) not projected.", srv.Name, srv.ScopePath),
			Severity: "info",
		})
	}

	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// buildGeminiContextOp constructs a single Operation for a Document being
// projected to the given target path (relative to project root) in the given
// Mode.
func buildGeminiContextOp(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode) (plugin.Operation, error) {
	downgraded := false
	if doc.NeedsWrite() && mode == plugin.ModeSymlink {
		mode = plugin.ModeWrite
		downgraded = true
	}

	srcRel := proj.SourceTag(doc.SourcePath)
	sources := []string{srcRel}
	for _, inc := range doc.Includes {
		sources = append(sources, proj.SourceTag(inc))
	}

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: sources,
		Plugin:  "gemini",
	}

	if mode == plugin.ModeSymlink {
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, doc.SourcePath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.Kind = plugin.OpSymlink
		op.LinkTarget = filepath.ToSlash(linkTarget)
	} else {
		op.Kind = plugin.OpWrite
		op.Content = doc.Body
	}

	if downgraded {
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   srcRel,
			Message:  "downgraded to write mode: contains @include directives",
			Severity: "info",
		})
	}

	return op, nil
}

// renderGeminiAgent renders a sub-agent file: YAML frontmatter + body.
// Only always-present fields (name, description) are emitted; optional
// fields (tools, model, temperature, max_turns, max_timeout) are omitted
// when the canonical model doesn't carry them so the projected file
// inherits Gemini's defaults.
//
// extensions is the verbatim `extensions.gemini.*` map from the canonical
// primitive (Agent.Extensions["gemini"] or Skill.Extensions["gemini"]).
// When non-nil and non-empty, each top-level key is emitted as a YAML
// frontmatter entry below name/description. Reserved keys (name,
// description) are dropped to prevent the passthrough from clobbering
// canonical fields. Values are serialized via json.Marshal which is also
// a valid YAML flow form.
func renderGeminiAgent(name, description, body string, extensions map[string]any) string {
	var b strings.Builder
	b.WriteString("---\n")
	if name != "" {
		b.WriteString("name: ")
		b.WriteString(renderYAMLScalar(name))
		b.WriteString("\n")
	}
	if description != "" {
		b.WriteString("description: ")
		b.WriteString(renderYAMLScalar(description))
		b.WriteString("\n")
	}
	if len(extensions) > 0 {
		keys := make([]string, 0, len(extensions))
		for k := range extensions {
			if k == "name" || k == "description" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			raw, err := json.Marshal(extensions[k])
			if err != nil {
				continue
			}
			b.WriteString(k)
			b.WriteString(": ")
			b.Write(raw)
			b.WriteString("\n")
		}
	}
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// geminiExtensions returns the `gemini` extension sub-map from a
// canonical Extensions map, or nil. Supports both `map[string]any` (the
// typical YAML parse shape) and the rare interface{} variant.
func geminiExtensions(ext map[string]any) map[string]any {
	if ext == nil {
		return nil
	}
	raw, ok := ext["gemini"]
	if !ok || raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return nil
}

// renderGeminiCommand renders a slash-command TOML file. Minimal schema:
// description + prompt as triple-quoted multi-line string. Triple-quote
// sequences inside the body are escaped to prevent premature termination.
//
// extensions is the verbatim `extensions.gemini.*` map from the canonical
// Command primitive. When non-nil and non-empty, each top-level key is
// emitted as a TOML key/value below description (and above prompt) using
// JSON encoding for the value — JSON scalars/arrays/objects are valid
// TOML inline syntax. Reserved keys (description, prompt) are dropped.
func renderGeminiCommand(description, body string, extensions map[string]any) string {
	var b strings.Builder
	if description != "" {
		b.WriteString("description = ")
		b.WriteString(renderYAMLScalar(description))
		b.WriteString("\n")
	}
	if len(extensions) > 0 {
		keys := make([]string, 0, len(extensions))
		for k := range extensions {
			if k == "description" || k == "prompt" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			raw, err := json.Marshal(extensions[k])
			if err != nil {
				continue
			}
			b.WriteString(k)
			b.WriteString(" = ")
			b.Write(raw)
			b.WriteString("\n")
		}
	}
	b.WriteString("prompt = \"\"\"\n")
	b.WriteString(sanitizeTOMLTripleQuoted(body))
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\"\"\"\n")
	return b.String()
}

// sanitizeTOMLTripleQuoted escapes a literal """ inside a body so it
// can't prematurely terminate the surrounding triple-quoted string. Every
// quote inside any """ run gets escaped individually (\" → escaped quote),
// which keeps a following bare `"` from re-closing the string. The earlier
// `"""` → `""\"` form was lossy: a body containing `""""` would emit
// `""\""`, which TOML reads as `""` (empty string) plus a stray `\""`.
func sanitizeTOMLTripleQuoted(s string) string {
	if !strings.Contains(s, `"""`) {
		return s
	}
	// Walk the string. Whenever a run of >=3 consecutive quotes is found,
	// escape every quote in that run. This is conservative (it over-escapes
	// `""""` which TOML doesn't strictly require), but it never produces a
	// malformed string.
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '"' {
			j := i
			for j < len(s) && s[j] == '"' {
				j++
			}
			if j-i >= 3 {
				for k := i; k < j; k++ {
					b.WriteString(`\"`)
				}
			} else {
				b.WriteString(s[i:j])
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// geminiMCPServerJSON is the schema Gemini CLI expects for entries under
// `.gemini/settings.json`'s `mcpServers` map.
type geminiMCPServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// geminiHookEntry mirrors Gemini's settings.json inner hook schema.
type geminiHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Name    string `json:"name,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// geminiHookGroup mirrors Gemini's settings.json hook group schema.
type geminiHookGroup struct {
	Matcher    string            `json:"matcher,omitempty"`
	Sequential bool              `json:"sequential,omitempty"`
	Hooks      []geminiHookEntry `json:"hooks"`
}

// buildGeminiSettingsOp emits the OpMerge for .gemini/settings.json. The
// engine reads the existing file and passes its bytes to the Merger; Plan()
// does not touch disk. The merger preserves any user-authored top-level
// keys; only mcpServers and hooks are touched.
//
// Portability note: Gemini's settings.json hook commands use
// ${PROJECT_DIR}/<rel> so the projection survives `mv` of the project
// tree. This is the only host of the v0.8 group whose hook config supports
// env-var interpolation — cursor / cline / windsurf all bake absolute
// paths and require a re-`prism compile` after a move.
func buildGeminiSettingsOp(proj *model.Project, wrapperPaths map[*model.Hook]string) (plugin.Operation, error) {
	relPath := filepath.Join(".gemini", "settings.json")

	type pendingHook struct {
		matcher string
		entry   geminiHookEntry
	}
	buckets := map[string][]pendingHook{}
	eventOrder := []string{}
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		// v2 additive read: fall back to EventCanonical when the v0.8
		// Event slot is empty. Emission shape unchanged.
		rawEvent := h.Event
		if rawEvent == "" && h.EventCanonical != "" {
			rawEvent = string(h.EventCanonical)
		}
		if rawEvent == "" {
			continue
		}
		mappedEvent := mapGeminiHookEvent(rawEvent)
		if _, ok := buckets[mappedEvent]; !ok {
			eventOrder = append(eventOrder, mappedEvent)
		}
		var cmdPath string
		if w, ok := wrapperPaths[h]; ok {
			cmdPath = "${PROJECT_DIR}/" + filepath.ToSlash(w)
		} else if filepath.IsAbs(h.ScriptPath) {
			if rel, err := filepath.Rel(proj.Root, h.ScriptPath); err == nil && !strings.HasPrefix(rel, "..") {
				cmdPath = "${PROJECT_DIR}/" + filepath.ToSlash(rel)
			} else {
				cmdPath = h.ScriptPath
			}
		} else {
			cmdPath = "${PROJECT_DIR}/" + filepath.ToSlash(h.ScriptPath)
		}
		name := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		buckets[mappedEvent] = append(buckets[mappedEvent], pendingHook{
			matcher: h.Matcher,
			entry: geminiHookEntry{
				Type:    "command",
				Command: cmdPath,
				Name:    name,
			},
		})
	}

	merger := func(existing []byte) (string, error) {
		settings := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &settings); err != nil {
				return "", fmt.Errorf("gemini: parsing existing %s: %w", relPath, err)
			}
		}

		if len(proj.MCP) > 0 {
			servers, _ := settings["mcpServers"].(map[string]any)
			if servers == nil {
				servers = map[string]any{}
			}
			for _, srv := range proj.MCP {
				if srv == nil || srv.Name == "" {
					continue
				}
				entry := geminiMCPServerJSON{
					Command: srv.Command,
					Args:    srv.Args,
					Env:     srv.Env,
				}
				if srv.Command == "" && srv.URL != "" {
					entry.URL = srv.URL
				}
				servers[srv.Name] = entry
			}
			settings["mcpServers"] = servers
		}

		if len(buckets) > 0 {
			hooksRoot, _ := settings["hooks"].(map[string]any)
			if hooksRoot == nil {
				hooksRoot = map[string]any{}
			}
			for _, event := range eventOrder {
				pending := buckets[event]
				byMatcher := map[string][]geminiHookEntry{}
				matchers := []string{}
				for _, ph := range pending {
					if _, ok := byMatcher[ph.matcher]; !ok {
						matchers = append(matchers, ph.matcher)
					}
					byMatcher[ph.matcher] = append(byMatcher[ph.matcher], ph.entry)
				}
				sort.Strings(matchers)
				groups := make([]geminiHookGroup, 0, len(matchers))
				for _, m := range matchers {
					groups = append(groups, geminiHookGroup{
						Matcher: m,
						Hooks:   byMatcher[m],
					})
				}
				hooksRoot[event] = groups
			}
			settings["hooks"] = hooksRoot
		}

		content, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return "", err
		}
		return string(content) + "\n", nil
	}

	var sources []string
	if len(proj.MCP) > 0 {
		sources = append(sources, "mcp.yaml")
	}
	if len(proj.Hooks) > 0 {
		sources = append(sources, "hooks.yaml")
	}
	if len(sources) == 0 {
		sources = []string{"settings.json"}
	}

	return plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    relPath,
		Mode:    plugin.ModeWrite,
		Sources: sources,
		Plugin:  "gemini",
		Merger:  merger,
	}, nil
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *GeminiPlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that GeminiPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*GeminiPlugin)(nil)
