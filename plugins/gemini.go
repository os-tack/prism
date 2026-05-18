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

// geminiLegacyEventMap translates prism's legacy v0.8 Hook.Event string
// values into Gemini CLI's native event identifiers. v0.9 emission goes
// through MapHookEventFor (plugins/hook_envelope.go); this map only feeds
// the v0.8 fallback path when a hook carries Hook.Event but no typed
// Hook.EventCanonical. Claude's PreToolUse / PostToolUse map to BeforeTool
// / AfterTool; the rest pass through when they already match a Gemini
// event name. Unknown events pass through untouched.
//
// Reference Gemini events: SessionStart, SessionEnd, BeforeAgent,
// AfterAgent, BeforeModel, AfterModel, BeforeToolSelection, BeforeTool,
// AfterTool, PreCompress, Notification.
var geminiLegacyEventMap = map[string]string{
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

// mapGeminiLegacyEvent returns the Gemini event for a prism v0.8 string
// event, passing through unknown names unchanged. Only used by the v0.8
// fallback path inside resolveGeminiHookEvent.
func mapGeminiLegacyEvent(event string) string {
	if mapped, ok := geminiLegacyEventMap[event]; ok {
		return mapped
	}
	return event
}

// resolveGeminiHookEvent computes the Gemini wire-form event name + matcher
// for a single Hook, using the shared HookEvent translation table
// (plugins/hook_envelope.go) as the source of truth (SPEC §4.4.4).
//
// Resolution order:
//
//  1. h.EventCanonical with the "native:" prefix → strip the prefix and
//     use the remainder verbatim as the Gemini event name. Matcher comes
//     from MatcherV2 / Matcher unchanged. Used for opt-in passthrough of
//     Gemini-specific event names (BeforeModel, BeforeToolSelection, etc.)
//     authored directly in the canonical model.
//
//  2. h.EventCanonical set (typed) → look up via MapHookEventFor("gemini",
//     ev). When found, the table-supplied matcher (e.g. run_shell_command
//     for pre_shell) combines with any user-supplied MatcherV2 / Matcher
//     via `|`. When NOT found, ok=false and the caller emits a warning
//     ("unsupported event on gemini").
//
//  3. v0.8 fallback: h.Event populated, h.EventCanonical empty → use
//     geminiLegacyEventMap (PreToolUse → BeforeTool, …) with the
//     user-supplied Matcher unchanged.
//
// Returns ok=false ONLY for case 2's unknown-canonical branch. Empty input
// (no Event and no EventCanonical) also returns ok=false.
func resolveGeminiHookEvent(h *model.Hook) (eventName, matcher string, ok bool) {
	user := geminiUserMatcher(h)
	if h.EventCanonical != "" {
		if strings.HasPrefix(string(h.EventCanonical), "native:") {
			return strings.TrimPrefix(string(h.EventCanonical), "native:"), user, true
		}
		m, found := MapHookEventFor("gemini", h.EventCanonical)
		if !found {
			return "", "", false
		}
		return m.Event, combineMatchers(m.Matcher, user), true
	}
	if h.Event != "" {
		return mapGeminiLegacyEvent(h.Event), user, true
	}
	return "", "", false
}

// geminiUserMatcher returns the user-supplied matcher string from a Hook,
// preferring the v2 MatcherV2.Patterns slice (joined with `|`) and falling
// back to the v0.8 Matcher string.
func geminiUserMatcher(h *model.Hook) string {
	if len(h.MatcherV2.Patterns) > 0 {
		return strings.Join(h.MatcherV2.Patterns, "|")
	}
	return h.Matcher
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
		// v0.9 translation (SPEC §4.4.4): the shared MapHookEventFor table
		// (plugins/hook_envelope.go) is the source of truth. Per-action
		// canonical events (PreShell, PostFileEdit, …) translate to
		// BeforeTool/AfterTool + matcher; generic events (PreToolUse) just
		// translate the name. Unknown canonical events surface as a warning
		// in the settings emitter; here in the wrapper loop we skip them so
		// no scope-guard wrapper is emitted for a hook we won't reference.
		mappedEvent, _, ok := resolveGeminiHookEvent(h)
		if !ok || mappedEvent == "" {
			continue
		}
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
		settingsOp, settingsWarnings, err := buildGeminiSettingsOp(proj, wrapperPaths)
		if err != nil {
			return nil, err
		}
		ops = append(ops, settingsOp)
		warnings = append(warnings, settingsWarnings...)
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
func buildGeminiSettingsOp(proj *model.Project, wrapperPaths map[*model.Hook]string) (plugin.Operation, []plugin.Warning, error) {
	relPath := filepath.Join(".gemini", "settings.json")

	type pendingHook struct {
		matcher string
		entry   geminiHookEntry
	}
	buckets := map[string][]pendingHook{}
	eventOrder := []string{}
	var warnings []plugin.Warning
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		// v0.9 translation via the shared HookEvent table (SPEC §4.4.4).
		// resolveGeminiHookEvent handles three paths: native:<verbatim>
		// passthrough, typed EventCanonical → MapHookEventFor("gemini", ev)
		// with matcher combine, and the v0.8 Event-string fallback.
		mappedEvent, matcherStr, ok := resolveGeminiHookEvent(h)
		if !ok {
			// Unknown canonical event on Gemini — drop with a warn-level
			// warning so the user knows the hook isn't projected. v0.8
			// strings always resolve (the fallback passes them through
			// verbatim), so this branch only fires for typed
			// EventCanonical values absent from the gemini column.
			warn := plugin.Warning{
				Source:   proj.SourceTag(h.ScriptPath),
				Message:  fmt.Sprintf("gemini: canonical hook event %q has no Gemini equivalent; hook not projected.", h.EventCanonical),
				Severity: "warn",
			}
			if h.ScopePath != "" {
				warn.Message = fmt.Sprintf("gemini: canonical hook event %q (scope: %s) has no Gemini equivalent; hook not projected.", h.EventCanonical, h.ScopePath)
			}
			warnings = append(warnings, warn)
			continue
		}
		if mappedEvent == "" {
			continue
		}
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
			matcher: matcherStr,
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
	}, warnings, nil
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *GeminiPlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that GeminiPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*GeminiPlugin)(nil)
