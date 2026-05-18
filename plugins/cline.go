// Package plugins contains the projection plugins shipped with agents.
//
// ClinePlugin projects a canonical .agents/ directory into Cline (and
// Roo Code, which uses the same convention) `.clinerules/` rule files,
// plus the modern Cline primitives added in late 2025.
//
// Hook.Event → Cline event mapping (canonical prism events on the left,
// Cline-native event names on the right):
//
//	PreToolUse        → PreToolUse
//	PostToolUse       → PostToolUse
//	UserPromptSubmit  → UserPromptSubmit
//	SessionStart      → TaskStart
//	SessionEnd        → TaskComplete
//	Stop              → TaskComplete
//	Notification      → (no Cline analog; pass-through)
//	SubagentStop      → (no Cline analog; pass-through)
//	PreCompact        → (no Cline analog; pass-through)
//
// Cline-native event names (TaskStart, TaskResume, UserPromptSubmit,
// PreToolUse, PostToolUse, TaskComplete, TaskCancel) pass through verbatim
// so users targeting Cline can use the native names directly.
//
// Note on hook portability: the project-relative wrapper paths baked into
// the per-event dispatcher scripts at .clinerules/hooks/<EventName> are
// absolute paths from proj.Root. Cline's hook engine has no
// ${PROJECT_DIR}-style substitution at the dispatcher level (only inside
// the wrappers we generate), so moving the project tree (`mv`, `rsync`,
// container mount) requires re-running `prism compile` to refresh the
// dispatcher paths. Same constraint applies to .cursor/hooks.json and
// .windsurf/hooks.json.
//
//   - YAML frontmatter on rule files (`paths:`, `description:`) — used
//     to express ScopePaths natively.
//   - Workflows at `.clinerules/workflows/<name>.md` — used for slash
//     commands (CMDS native, replacing the old `30-command-*` rules).
//   - Hooks at `.clinerules/hooks/<EventName>` (no extension, mode 0755)
//     — per SPEC §4.4.5 Cline uses filename-dispatch, not JSON config.
//     One executable script per event with matcher guard clauses inlined
//     at the top.
//   - MCP at `.cline/cline_mcp_settings.json` — project-local mirror of
//     the CLI settings file, written in the standard `{mcpServers:{…}}`
//     schema and merged into any existing file at that path.
//
// Capability summary (v0.8.0):
//   - Context:        native (plain-markdown rule file at 00-context.md)
//   - ScopePaths:     native (YAML frontmatter `paths:` glob array)
//   - ScopeSemantic:  native (same frontmatter; description hint)
//   - Skills:         degraded — no dedicated primitive; emitted as
//     scoped rules with `paths:` + description.
//   - Commands:       native (workflows/<name>.md slash commands)
//   - Hooks:          native (filename-dispatch scripts at
//     .clinerules/hooks/<EventName> per SPEC §4.4.5)
//   - MCP:            native (.cline/cline_mcp_settings.json)
//   - Agents:         unsupported (Cline subagents are SDK-based)
//   - Permissions:    unsupported (no native primitive in v0.8.0; could
//     be wrapped via PreToolUse in a later release)
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

// ClinePlugin projects Project state into `.clinerules/` rule files,
// workflows, hooks, and the sibling `.cline/cline_mcp_settings.json`.
type ClinePlugin struct {
	// DisableHookWrappers, when true, projects scoped hooks as if they
	// were global (no `__scope-guard__` wrapper). Default false
	// (wrappers ON). Mirrors ClaudePlugin.DisableHookWrappers for
	// programmatic configuration in tests.
	DisableHookWrappers bool
}

// NewCline constructs a ClinePlugin.
func NewCline() *ClinePlugin { return &ClinePlugin{} }

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *ClinePlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that ClinePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ClinePlugin)(nil)

// Name returns the stable plugin identifier.
func (p *ClinePlugin) Name() string { return "cline" }

// Detect returns true if the project at root looks like it uses Cline.
// Either the `.clinerules/` directory (modern, multi-file form) or a
// single `.clinerules` file (legacy form) activates the plugin.
func (p *ClinePlugin) Detect(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ".clinerules")); err == nil {
		return true
	}
	return false
}

// Capabilities returns Cline's capability matrix.
//
// The v0.8 coarse cells (Context..MCP) are preserved unchanged for
// backward-compatibility with callers that read the flat shape. The v2
// per-primitive FieldCapabilities cells (AgentFields..ScopeFields) are
// populated per SPEC §12 (Cline column, `cli`).
func (p *ClinePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		// v0.8 coarse cells (unchanged).
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportNative, // wrapper-implemented via prism perms-guard (v0.8.2)
		MCP:           plugin.SupportNative,

		// v2 per-primitive declarations (SPEC §12, `cli` column).
		AgentFields:       clineAgentFields(),
		SkillFields:       clineSkillFields(),
		CommandFields:     clineCommandFields(),
		HookFields:        clineHookFields(),
		MCPServerFields:   clineMCPServerFields(),
		PermissionsFields: clinePermissionsFields(),
		ScopeFields:       clineScopeFields(),
	}
}

// clineAgentFields encodes the SPEC §12 Agent table, Cline column. Cline
// has no subagent primitive; the entire Agent primitive is dropped
// (Supported=false). Per-field cells are declared explicitly so callers
// that consult FieldCapabilities can distinguish "unsupported" fields
// (warn-level) from "silent" ones (ModelFallbacks) per SPEC §6.2.
func clineAgentFields() plugin.FieldCapabilities {
	return plugin.FieldCapabilities{
		Supported: false,
		Fields: map[string]plugin.FieldSupport{
			"Name":             plugin.FieldUnsupported,
			"Description":      plugin.FieldUnsupported,
			"SystemPrompt":     plugin.FieldUnsupported,
			"Model":            plugin.FieldUnsupported,
			"ModelFallbacks":   plugin.FieldSilent,
			"Tools":            plugin.FieldUnsupported,
			"DisallowedTools":  plugin.FieldUnsupported,
			"ReadOnly":         plugin.FieldUnsupported,
			"Background":       plugin.FieldUnsupported,
			"MaxTurns":         plugin.FieldUnsupported,
			"Temperature":      plugin.FieldUnsupported,
			"MCPServers":       plugin.FieldUnsupported,
			"AllowedSubagents": plugin.FieldUnsupported,
			"UserInvocable":    plugin.FieldUnsupported,
			"ModelInvocable":   plugin.FieldUnsupported,
			"InitialPrompt":    plugin.FieldUnsupported,
			"ScopePath":        plugin.FieldUnsupported,
			"Extensions":       plugin.FieldNative,
		},
		Extensions: []string{"cline"},
	}
}

// clineSkillFields encodes the SPEC §12 Skill table, Cline column.
// Skills project to rule files (no dedicated primitive), so Description
// and WhenToUse degrade, and Scripts/References/Model/Subagent/ScopePath
// drop. Activation.Globs is native via the frontmatter `paths:` array.
func clineSkillFields() plugin.FieldCapabilities {
	return plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Name":                      plugin.FieldNative,
			"Description":               plugin.FieldDegraded,
			"WhenToUse":                 plugin.FieldDegraded,
			"Activation.Modes.Always":   plugin.FieldNative,
			"Activation.Modes.ModelDec": plugin.FieldUnsupported,
			"Activation.Modes.Glob":     plugin.FieldNative,
			"Activation.Modes.Manual":   plugin.FieldNative,
			"Activation.Globs":          plugin.FieldNative,
			"Activation.ContentRegex":   plugin.FieldUnsupported,
			"Activation.UserInvocable":  plugin.FieldNative,
			"Activation.ModelInvocable": plugin.FieldSilent,
			"AllowedTools":              plugin.FieldUnsupported,
			"Arguments":                 plugin.FieldUnsupported,
			"Scripts":                   plugin.FieldUnsupported,
			"References":                plugin.FieldUnsupported,
			"Model":                     plugin.FieldUnsupported,
			"Subagent":                  plugin.FieldUnsupported,
			"ScopePath":                 plugin.FieldUnsupported,
			"Extensions":                plugin.FieldNative,
		},
		Extensions: []string{"cline"},
	}
}

// clineCommandFields encodes the SPEC §12 Command table, Cline column.
// Workflows are native, but Description degrades (no native description
// surface), Arguments degrade, and several Claude-only fields drop or
// silence.
func clineCommandFields() plugin.FieldCapabilities {
	return plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Name":         plugin.FieldNative,
			"Description":  plugin.FieldDegraded,
			"ArgumentHint": plugin.FieldSilent,
			"Arguments":    plugin.FieldDegraded,
			"Model":        plugin.FieldUnsupported,
			"Tools":        plugin.FieldUnsupported,
			"Agent":        plugin.FieldDegraded,
			"AutoInvoke":   plugin.FieldSilent,
			"ScopePath":    plugin.FieldDegraded,
			"Extensions":   plugin.FieldNative,
		},
		Extensions: []string{"cline"},
	}
}

// clineHookFields encodes the SPEC §12 Hook table, Cline column. The
// core ≥4-tool event subset is native; Matcher exact/regex degrade
// (filename-dispatch model — see SPEC §4.4.5 and the Phase 2.5 TODO in
// buildClineHookOps). ScopePath is native via the perms-guard /
// scope-guard wrapper family (SPEC §12 footnote ¹).
func clineHookFields() plugin.FieldCapabilities {
	return plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Name":              plugin.FieldSilent,
			"Description":       plugin.FieldSilent,
			"Event.Core":        plugin.FieldNative,
			"Event.ClaudeOnly":  plugin.FieldUnsupported,
			"Matcher.Exact":     plugin.FieldDegraded,
			"Matcher.Regex":     plugin.FieldDegraded,
			"Handlers.Command":  plugin.FieldNative,
			"Handlers.HTTP":     plugin.FieldUnsupported,
			"Handlers.MCPTool":  plugin.FieldUnsupported,
			"Handlers.Prompt":   plugin.FieldUnsupported,
			"Handlers.Agent":    plugin.FieldUnsupported,
			"Sequential":        plugin.FieldSilent,
			"Disabled":          plugin.FieldNative,
			"TimeoutMs":         plugin.FieldDegraded,
			"StatusMessage":     plugin.FieldSilent,
			"Async":             plugin.FieldUnsupported,
			"FailClosed":        plugin.FieldUnsupported,
			"Once":              plugin.FieldUnsupported,
			"If":                plugin.FieldUnsupported,
			"Cwd":               plugin.FieldSilent,
			"Env":               plugin.FieldUnsupported,
			"BashAndPowershell": plugin.FieldUnsupported,
			"ScopePath":         plugin.FieldNative, // perms-guard / scope-guard wrapper family (SPEC §12 footnote ¹)
			"Extensions":        plugin.FieldNative,
		},
		Extensions: []string{"cline"},
	}
}

// clineMCPServerFields encodes the SPEC §12 MCPServer table, Cline
// column. All transports + bearer/header auth are native; AutoApprove
// and TimeoutMs are native (Cline-specific richness); Cwd is silent;
// Trust and the include/exclude tool filters drop.
func clineMCPServerFields() plugin.FieldCapabilities {
	return plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Name":            plugin.FieldNative,
			"Transport.Stdio": plugin.FieldNative,
			"Transport.HTTP":  plugin.FieldNative,
			"Transport.SSE":   plugin.FieldNative,
			"Command":         plugin.FieldNative,
			"Args":            plugin.FieldNative,
			"Env":             plugin.FieldNative,
			"Cwd":             plugin.FieldSilent,
			"URL":             plugin.FieldNative,
			"Headers":         plugin.FieldNative,
			"Auth.Bearer":     plugin.FieldNative,
			"Auth.Header":     plugin.FieldNative,
			"Auth.OAuth":      plugin.FieldDegraded,
			"TimeoutMs":       plugin.FieldNative,
			"Disabled":        plugin.FieldNative,
			"AutoApprove":     plugin.FieldNative,
			"Trust":           plugin.FieldUnsupported,
			"IncludeTools":    plugin.FieldUnsupported,
			"ExcludeTools":    plugin.FieldUnsupported,
			"ScopePath":       plugin.FieldDegraded,
			"Extensions":      plugin.FieldNative,
		},
		Extensions: []string{"cline"},
	}
}

// clinePermissionsFields encodes the SPEC §12 Permissions table, Cline
// column. Native via the perms-guard wrapper (SPEC §12 footnote ¹).
func clinePermissionsFields() plugin.FieldCapabilities {
	return plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Allow.Global":         plugin.FieldNative,
			"Ask.Global":           plugin.FieldNative,
			"Deny.Global":          plugin.FieldNative,
			"Allow.Scoped":         plugin.FieldNative,
			"Ask.Scoped":           plugin.FieldNative,
			"Deny.Scoped":          plugin.FieldNative,
			"Target.Bash":          plugin.FieldNative,
			"Target.EditReadWrite": plugin.FieldNative,
			"Target.FS":            plugin.FieldNative,
			"Target.Network":       plugin.FieldNative,
			"Target.MCP":           plugin.FieldNative,
			"Glob.Recursive":       plugin.FieldNative,
			"Negation":             plugin.FieldNative,
			"Extensions":           plugin.FieldNative,
		},
		Extensions: []string{"cline"},
	}
}

// clineScopeFields encodes the SPEC §12 Scope table, Cline column.
// Path cascades degrade; Globs / glob-activation are native (via the
// frontmatter `paths:` array on rule files).
func clineScopeFields() plugin.FieldCapabilities {
	return plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Path.Cascade":        plugin.FieldDegraded,
			"Path.Empty":          plugin.FieldNative,
			"Name":                plugin.FieldNative,
			"Description":         plugin.FieldNative,
			"Globs":               plugin.FieldNative,
			"Activation.Always":   plugin.FieldNative,
			"Activation.Cascade":  plugin.FieldDegraded,
			"Activation.Glob":     plugin.FieldNative,
			"Activation.Manual":   plugin.FieldUnsupported,
			"Activation.ModelDec": plugin.FieldUnsupported,
			"Priority":            plugin.FieldSilent,
			"Tags":                plugin.FieldSilent,
			"IsOverride":          plugin.FieldUnsupported,
			"Extensions":          plugin.FieldNative,
		},
		Extensions: []string{"cline"},
	}
}

// clineHookEvents enumerates Cline's native hook event names. Used by
// mapClineEvent to pass-through Cline-shaped event names verbatim.
var clineHookEvents = map[string]struct{}{
	"TaskStart":        {},
	"TaskResume":       {},
	"UserPromptSubmit": {},
	"PreToolUse":       {},
	"PostToolUse":      {},
	"TaskComplete":     {},
	"TaskCancel":       {},
}

// hookEventFor returns the Cline-native event string for h, preferring
// the v2 typed EventCanonical when set and falling back to the v0.8
// Event string. ADDITIVE read (SPEC §6.2 transition): plugins are
// expected to read both shapes for the duration of the v0.8 → v2
// migration; the parser populates EventCanonical from Event for every
// hook source, so this helper sees the same data through either path.
func hookEventFor(h *model.Hook) string {
	if h == nil {
		return ""
	}
	if h.EventCanonical != "" {
		return string(h.EventCanonical)
	}
	return h.Event
}

// skillGlobsFor returns the glob list for sk, preferring the v2
// Activation.Globs when set and falling back to the v0.8 top-level
// Globs slice. ADDITIVE read — the parser mirrors sk.Globs into
// sk.Activation.Globs at parse time, so both paths observe the same
// data; the helper exists so the renderer can survive sources that
// populate only one shape (e.g. future direct-v2 inputs).
func skillGlobsFor(sk *model.Skill) []string {
	if sk == nil {
		return nil
	}
	if len(sk.Activation.Globs) > 0 {
		return sk.Activation.Globs
	}
	return sk.Globs
}

// mapClineEvent translates a prism Hook.Event into a Cline-native event
// name. Cline-shaped events pass through unchanged; Claude-style aliases
// that map cleanly (SessionStart → TaskStart, Stop → TaskComplete) are
// rewritten. Returns the input unchanged when no mapping is known — the
// JSON file lands at .clinerules/hooks/<event>.json either way, but
// users targeting Cline should prefer Cline-native names.
func mapClineEvent(event string) string {
	if event == "" {
		return event
	}
	if _, ok := clineHookEvents[event]; ok {
		return event
	}
	switch event {
	case "SessionStart":
		return "TaskStart"
	case "SessionEnd", "Stop":
		return "TaskComplete"
	case "UserPrompt", "UserPromptSubmit":
		return "UserPromptSubmit"
	}
	return event
}

// Plan produces the Operations needed to project proj into Cline's layout.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Cline never symlinks — rule files carry a documented preamble and
// frontmatter, and we want each file self-contained so users can hand
// tune individual files. Unknown modes return an error.
func (p *ClinePlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	switch opts.Mode {
	case "", "write":
		// ok
	default:
		return nil, fmt.Errorf("cline: unsupported mode %q", opts.Mode)
	}

	var ops []plugin.Operation

	// Root context → .clinerules/00-context.md (no frontmatter; loads always).
	if proj.Context != nil {
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".clinerules/00-context.md",
			Content: ensureTrailingNewline(proj.Context.Body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		})
	}

	// Per-scope rule files at .clinerules/10-scope-<slug>.md with native
	// YAML frontmatter (`paths:` glob array, optional `description:`).
	for _, scope := range proj.Scopes {
		if scope == nil {
			continue
		}
		body := ""
		var sources []string
		if scope.Document != nil {
			body = scope.Document.Body
			sources = []string{proj.SourceTag(scope.Document.SourcePath)}
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "10-scope-"+slugify(scope.Path)+".md")),
			Content: renderClineScope(scope, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
	}

	// Skills → .clinerules/20-skill-<slug>.md (global) or
	// .clinerules/20-skill-<scopeSlug>-<name>.md (scoped). Skills get
	// a `paths:` frontmatter when a glob list is available; scripts are
	// noted as ignored (Cline has no script-execution mechanism).
	for _, skill := range proj.Skills {
		if skill == nil {
			continue
		}
		body := ""
		var sources []string
		if skill.Document != nil {
			body = skill.Document.Body
			sources = []string{proj.SourceTag(skill.Document.SourcePath)}
		}
		fileName := "20-skill-" + skillSlug(skill.Name) + ".md"
		if skill.ScopePath != "" {
			fileName = "20-skill-" + scopeSlug(skill.ScopePath) + "-" + skillSlug(skill.Name) + ".md"
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", fileName)),
			Content: renderClineSkill(skill, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if len(skill.Scripts) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline cannot execute scripts; ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Commands → .clinerules/workflows/<name>.md (native slash commands).
	// Scoped commands get a <scopeSlug>-<name>.md filename and an info
	// warning — workflows are global in Cline, so the scope path is
	// preserved only as a "When working in <path>" preamble.
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		body := ""
		var sources []string
		if cmd.Document != nil {
			body = cmd.Document.Body
			sources = []string{proj.SourceTag(cmd.Document.SourcePath)}
		}
		fileName := skillSlug(cmd.Name) + ".md"
		if cmd.ScopePath != "" {
			fileName = scopeSlug(cmd.ScopePath) + "-" + skillSlug(cmd.Name) + ".md"
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "workflows", fileName)),
			Content: renderClineWorkflow(cmd, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline workflows are global; scoped command %q (scope: %s) projected without path enforcement.", cmd.Name, cmd.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (must be emitted before the per-event
	// hook JSON so the JSON can reference the wrapper's absolute path).
	// Each scoped hook gets its own wrapper script at
	// .clinerules/hooks/__scope-guard__/<scopeSlug>-<event>-<basename>.sh.
	// The wrapper reads JSON from stdin and exec's `prism scope-guard`,
	// matching the Claude wrapper contract in plugins/claude.go.
	wrapperPaths := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		// v2 additive read: prefer EventCanonical via hookEventFor.
		wrapperFile := scopeSlug(h.ScopePath) + "-" + mapClineEvent(hookEventFor(h)) + "-" + hookName + ".sh"
		wrapperRel := filepath.ToSlash(filepath.Join(".clinerules", "hooks", "__scope-guard__", wrapperFile))
		wrapperAbs := filepath.Join(proj.Root, wrapperRel)

		body := buildScopeGuardScript(wrapperRel, h.ScopePath, h.ScriptPath, formatScriptArg(h.ScriptPath, proj.Root))
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     wrapperRel,
			Content:  body,
			Mode:     plugin.ModeWrite,
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   p.Name(),
		})
		wrapperPaths[h] = wrapperAbs
	}

	// Perms-guard wrappers + sidecar policy. v0.8.2 flipped Cline's
	// Permissions cell to native by re-using the gemini wrapper pattern:
	// .cline/hooks/__perms-guard__/{policy.json, *-gate.sh,
	// <event>-<basename>.sh}. The wrappers are wired into PreToolUse.json
	// below — per-hook wrappers replace the raw script command; bare
	// gate wrappers append as their own PreToolUse entries.
	permsOps, permsWarnings, perr := emitPermsGuardWrappers(p.Name(), proj, p.DisableHookWrappers)
	if perr != nil {
		return nil, perr
	}
	ops = append(ops, permsOps...)

	// Build (hook → perms-guard wrapper path) so the hook JSON command
	// points at the wrapper that enforces the policy, then executes the
	// user script on allow.
	permsHookWrappers := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.Event == "" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		var wrapperName string
		if h.ScopePath == "" {
			wrapperName = h.Event + "-" + base + ".sh"
		} else {
			wrapperName = permsScopeSlug(h.ScopePath) + "-" + h.Event + "-" + base + ".sh"
		}
		wrapperRel := filepath.Join("."+p.Name(), "hooks", "__perms-guard__", wrapperName)
		for _, op := range permsOps {
			if op.Path == wrapperRel {
				permsHookWrappers[h] = filepath.Join(proj.Root, wrapperRel)
				break
			}
		}
	}

	var permsGateRefs []string
	for _, op := range permsOps {
		if !strings.Contains(op.Path, "__perms-guard__") {
			continue
		}
		if strings.HasSuffix(op.Path, "global-gate.sh") || strings.HasSuffix(op.Path, "-gate.sh") {
			permsGateRefs = append(permsGateRefs, op.Path)
		}
	}
	sort.Strings(permsGateRefs)

	// Hooks → .clinerules/hooks/<EventName> (no extension, mode 0755)
	// per SPEC §4.4.5: one executable script per event with matcher
	// guard clauses inlined at the top. Scoped hooks point at their
	// scope-guard wrapper. The perms-guard gate wrappers (when
	// permissions exist but no user hooks) are appended as no-matcher
	// sections on PreToolUse so every tool call flows through the
	// policy. When user hooks DO exist, each hook's command is
	// rewritten to its per-hook perms-guard wrapper (which wraps the
	// user script).
	if hookOps := buildClineHookOps(proj, wrapperPaths, permsHookWrappers, proj.Root, permsGateRefs); len(hookOps) > 0 {
		ops = append(ops, hookOps...)
	}

	// MCP → .cline/cline_mcp_settings.json (project-local).
	//
	// Rationale: the CLI install reads the file from
	// ~/.cline/data/settings/cline_mcp_settings.json, which is per-user
	// and would surprise users by mutating their home directory. The
	// VS Code extension reads from extension globalStorage which is not
	// addressable from a project at all. We pick a project-local path
	// (.cline/cline_mcp_settings.json) — users can `ln -s` or copy it
	// into the CLI location explicitly, which keeps prism's projection
	// safe to run anywhere. The schema is identical to Claude's
	// .mcp.json (standard MCP `{mcpServers: {...}}`), so users with
	// existing tooling can swap files trivially.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildClineMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	// Orphan warnings for capabilities Cline still lacks: agents,
	// permissions. Attach to the first emitted op (if any).
	var orphanWarnings []plugin.Warning
	for _, agent := range proj.Agents {
		if agent == nil {
			continue
		}
		src := ""
		if agent.Document != nil {
			src = proj.SourceTag(agent.Document.SourcePath)
		}
		msg := fmt.Sprintf("Cline has no subagent primitive; %s not projected.", agent.Name)
		if agent.ScopePath != "" {
			msg = fmt.Sprintf("Cline has no subagent primitive; scoped agent %s (scope: %s) not projected.", agent.Name, agent.ScopePath)
		}
		orphanWarnings = append(orphanWarnings, plugin.Warning{
			Source:   src,
			Message:  msg,
			Severity: "info",
		})
		// Per-field unsupported warnings (SPEC §12 cli column). One warn-level
		// Warning per (agent, unsupported field) so downstream tooling can
		// surface exactly what was dropped.
		orphanWarnings = append(orphanWarnings, clineAgentFieldWarnings(agent, src)...)
	}
	orphanWarnings = append(orphanWarnings, permsWarnings...)

	// Per-field degradation / drop warnings for Skill / Command / Hook /
	// MCPServer / Scope per SPEC §12 cli column. Each function returns one
	// Warning per (primitive, non-zero field) pair so the contract test can
	// observe the projection's lossiness.
	for _, sk := range proj.Skills {
		if sk == nil {
			continue
		}
		src := ""
		if sk.Document != nil {
			src = proj.SourceTag(sk.Document.SourcePath)
		}
		orphanWarnings = append(orphanWarnings, clineSkillFieldWarnings(sk, src)...)
	}
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		src := ""
		if cmd.Document != nil {
			src = proj.SourceTag(cmd.Document.SourcePath)
		}
		orphanWarnings = append(orphanWarnings, clineCommandFieldWarnings(cmd, src)...)
	}
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		src := proj.SourceTag(h.ScriptPath)
		if src == "" {
			src = "hooks.yaml"
		}
		orphanWarnings = append(orphanWarnings, clineHookFieldWarnings(h, src)...)
	}
	for _, srv := range proj.MCP {
		if srv == nil {
			continue
		}
		src := "mcp.yaml"
		if srv.ScopePath != "" {
			src = srv.ScopePath + "/mcp.yaml"
		}
		orphanWarnings = append(orphanWarnings, clineMCPFieldWarnings(srv, src)...)
	}
	for _, sc := range proj.Scopes {
		if sc == nil {
			continue
		}
		src := ""
		if sc.Document != nil {
			src = proj.SourceTag(sc.Document.SourcePath)
		}
		orphanWarnings = append(orphanWarnings, clineScopeFieldWarnings(sc, src)...)
	}

	// Synthesize a no-content carrier op when orphan warnings exist but no
	// other op was produced (e.g. project only has Agents). The carrier op
	// is OpWrite with Mode=ModeHook so the engine treats it as advisory and
	// does not touch the filesystem — its sole purpose is to surface the
	// warnings so callers (and the capability-contract test) can see them.
	if len(orphanWarnings) > 0 && len(ops) == 0 {
		ops = append(ops, plugin.Operation{
			Kind:   plugin.OpWrite,
			Path:   "",
			Mode:   plugin.ModeHook,
			Plugin: p.Name(),
		})
	}
	if len(orphanWarnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, orphanWarnings...)
	}

	return ops, nil
}

// clineAgentFieldWarnings emits one warn-level Warning per non-zero
// Unsupported field on agent (SPEC §12 Agent table, cli column). Cline
// has no subagent primitive at all, so every Agent field that carries
// data is dropped — each gets its own warning naming the field so
// downstream tooling can attribute the loss.
func clineAgentFieldWarnings(agent *model.Agent, source string) []plugin.Warning {
	if agent == nil {
		return nil
	}
	var warns []plugin.Warning
	add := func(field string) {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "warn",
			Message:  fmt.Sprintf("Cline has no subagent primitive; agent %q field %s dropped.", agent.Name, field),
		})
	}
	if agent.SystemPrompt != "" {
		add("SystemPrompt")
	}
	if agent.Model != "" {
		add("Model")
	}
	if len(agent.Tools) > 0 {
		add("Tools")
	}
	if len(agent.DisallowedTools) > 0 {
		add("DisallowedTools")
	}
	if agent.ReadOnly != nil {
		add("ReadOnly")
	}
	if agent.Background != nil {
		add("Background")
	}
	if agent.MaxTurns != nil {
		add("MaxTurns")
	}
	if agent.Temperature != nil {
		add("Temperature")
	}
	if len(agent.MCPServers) > 0 {
		add("MCPServers")
	}
	if len(agent.AllowedSubagents) > 0 {
		add("AllowedSubagents")
	}
	if agent.UserInvocable != nil {
		add("UserInvocable")
	}
	if agent.ModelInvocable != nil {
		add("ModelInvocable")
	}
	if agent.InitialPrompt != "" {
		add("InitialPrompt")
	}
	if agent.ScopePath != "" {
		add("ScopePath")
	}
	return warns
}

// clineSkillFieldWarnings emits per-field degradation / drop warnings for
// a Skill per SPEC §12 Skill table, cli column. Skills project to rule
// files (no dedicated primitive), so Description / WhenToUse degrade
// (info) and AllowedTools / Arguments / References / Model / Subagent /
// ScopePath / Activation.ContentRegex drop (warn).
func clineSkillFieldWarnings(sk *model.Skill, source string) []plugin.Warning {
	if sk == nil {
		return nil
	}
	var warns []plugin.Warning
	addInfo := func(field, hint string) {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "info",
			Message:  fmt.Sprintf("Cline skill %q: %s %s.", sk.Name, field, hint),
		})
	}
	addWarn := func(field string) {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "warn",
			Message:  fmt.Sprintf("Cline skill %q: %s unsupported and dropped.", sk.Name, field),
		})
	}
	if sk.Description != "" {
		addInfo("Description", "approximated via rule-file frontmatter description")
	}
	if sk.WhenToUse != "" {
		addInfo("WhenToUse", "merged into the rule body as a hint (no dedicated trigger)")
	}
	if len(sk.AllowedTools) > 0 {
		addWarn("AllowedTools")
	}
	if len(sk.Arguments) > 0 {
		addWarn("Arguments")
	}
	if len(sk.References) > 0 {
		addWarn("References")
	}
	if sk.Model != "" {
		addWarn("Model")
	}
	if sk.Subagent != "" {
		addWarn("Subagent")
	}
	if sk.ScopePath != "" {
		addWarn("ScopePath")
	}
	if sk.Activation.ContentRegex != "" {
		addWarn("Activation.ContentRegex")
	}
	return warns
}

// clineCommandFieldWarnings emits per-field warnings for a Command per
// SPEC §12 Command table, cli column. Workflows are native, but
// Description / Arguments / Agent / ScopePath degrade and Model / Tools
// drop entirely.
func clineCommandFieldWarnings(cmd *model.Command, source string) []plugin.Warning {
	if cmd == nil {
		return nil
	}
	var warns []plugin.Warning
	addInfo := func(field, hint string) {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "info",
			Message:  fmt.Sprintf("Cline command %q: %s %s.", cmd.Name, field, hint),
		})
	}
	addWarn := func(field string) {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "warn",
			Message:  fmt.Sprintf("Cline command %q: %s unsupported and dropped.", cmd.Name, field),
		})
	}
	if cmd.Description != "" {
		addInfo("Description", "rendered as a markdown blockquote (no native description surface)")
	}
	if len(cmd.Arguments) > 0 {
		addInfo("Arguments", "Cline workflows have no native argument schema; preserved in body only")
	}
	if cmd.Agent != "" {
		addInfo("Agent", "Cline has no subagent primitive; agent name mentioned in body only")
	}
	// ScopePath already emits its own info warning in the workflow loop
	// above; we add a second mention with the field name verbatim so the
	// capability contract can match on "ScopePath".
	if cmd.ScopePath != "" {
		addInfo("ScopePath", "Cline workflows are global; path enforcement degraded")
	}
	if cmd.Model != "" {
		addWarn("Model")
	}
	if len(cmd.Tools) > 0 {
		addWarn("Tools")
	}
	return warns
}

// clineHookFieldWarnings emits per-field warnings for a Hook per SPEC
// §12 Hook table, cli column. Cline's filename-dispatch hook engine
// cannot pass per-handler env vars or honor a FailClosed semantic; both
// are dropped with warnings.
func clineHookFieldWarnings(h *model.Hook, source string) []plugin.Warning {
	if h == nil {
		return nil
	}
	var warns []plugin.Warning
	hookName := h.Name
	if hookName == "" {
		hookName = strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
	}
	for _, hd := range h.Handlers {
		if len(hd.Env) > 0 {
			warns = append(warns, plugin.Warning{
				Source:   source,
				Severity: "warn",
				Message:  fmt.Sprintf("Cline hook %q: handler Env unsupported (Cline dispatcher cannot inject per-handler env); drop or wrap in a shell script.", hookName),
			})
		}
		if hd.FailClosed {
			warns = append(warns, plugin.Warning{
				Source:   source,
				Severity: "warn",
				Message:  fmt.Sprintf("Cline hook %q: handler FailClosed unsupported (Cline treats non-block exits as fail-open).", hookName),
			})
		}
	}
	return warns
}

// clineMCPFieldWarnings emits per-field warnings for an MCPServer per
// SPEC §12 MCPServer table, cli column. Trust / IncludeTools /
// ExcludeTools drop (warn); ScopePath degrades to project-wide (info).
func clineMCPFieldWarnings(srv *model.MCPServer, source string) []plugin.Warning {
	if srv == nil {
		return nil
	}
	var warns []plugin.Warning
	addWarn := func(field string) {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "warn",
			Message:  fmt.Sprintf("Cline MCP server %q: %s unsupported and dropped.", srv.Name, field),
		})
	}
	if srv.Trust {
		addWarn("Trust")
	}
	if len(srv.IncludeTools) > 0 {
		addWarn("IncludeTools")
	}
	if len(srv.ExcludeTools) > 0 {
		addWarn("ExcludeTools")
	}
	if srv.ScopePath != "" {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "info",
			Message:  fmt.Sprintf("Cline MCP server %q: ScopePath degraded — applied project-wide (no per-scope MCP).", srv.Name),
		})
	}
	return warns
}

// clineScopeFieldWarnings emits per-field warnings for a Scope per SPEC
// §12 Scope table, cli column. IsOverride is unsupported (Cline has no
// override semantic at the rule-file layer); cascade-path semantics are
// approximated and surfaced as info elsewhere.
func clineScopeFieldWarnings(sc *model.Scope, source string) []plugin.Warning {
	if sc == nil {
		return nil
	}
	var warns []plugin.Warning
	if sc.IsOverride {
		warns = append(warns, plugin.Warning{
			Source:   source,
			Severity: "warn",
			Message:  fmt.Sprintf("Cline scope %q: IsOverride unsupported (no native override semantic at the rule-file layer).", sc.Path),
		})
	}
	return warns
}

// clineHookSection holds one (matcher, handler-list) bundle inside an event
// dispatcher script. Multiple sections for the same event are emitted in
// stable order and each runs sequentially when the matcher hits.
type clineHookSection struct {
	// matcher is the resolved tool-name pattern to guard on; empty means
	// "always run". A pipe-joined string ("Edit|Write") expresses an OR.
	matcher string
	// kind is "exact" or "regex" — controls the guard-clause shape. When
	// empty, "exact" is assumed (the v0.8 Matcher string is treated as a
	// pipe-joined exact-pattern list).
	kind string
	// handlers is the ordered list of shell snippets to execute once the
	// matcher hits. Each snippet is a complete shell statement (no
	// trailing newline); rendering joins with "\n".
	handlers []string
	// source is a Warning-source tag for any unsupported-handler
	// warnings emitted while building this section.
	source string
}

// clineEventScript is the in-memory form of a single dispatcher script
// written to .clinerules/hooks/<EventName>. One script per event; multiple
// (matcher, handler) sections fan out sequentially inside.
type clineEventScript struct {
	event    string
	sections []clineHookSection
	// warnings accumulate during build (unsupported handler kinds).
	warnings []plugin.Warning
}

// buildClineHookOps emits one OpWrite per hook EVENT at
// `.clinerules/hooks/<EventName>` (no extension, mode 0755) per SPEC
// §4.4.5. Each script reads the hook payload from stdin once, then runs
// matched handlers sequentially. Multi-handler events collapse into a
// single dispatcher script.
//
// Per-action canonical events (pre_shell, post_file_edit, pre_mcp_call,
// etc.) translate to (PreToolUse|PostToolUse + matcher) via
// MapHookEventFor; the matcher is inlined as a guard clause. The v0.8
// Hook.Event string falls back through mapClineEvent for source files that
// have not been re-parsed through the v2 canonicalization path.
//
// Handler dispatch:
//   - HookHandlerCommand (or legacy ScriptPath): exec the command, piping
//     stdin through.
//   - HookHandlerHTTP: POST the payload via `curl` (SPEC §12 keeps HTTP
//     native on Cline via this curl shim).
//   - HookHandlerMCPTool / Prompt / Agent: unsupported on Cline; emit a
//     warn-level Warning and skip the handler.
//
// permsHookWrappers maps user hooks to their perms-guard wrapper path
// (absolute); when present, the wrapper replaces the raw script command.
// permsGateRefs are project-relative paths to bare perms-guard gates
// (from emitPermsGuardWrappers, when permissions exist but no user hooks
// did); each gate is appended as a no-matcher section on PreToolUse so the
// policy fires on every tool call.
//
// Precedence: scope-guard wrappers > perms-guard wrappers > raw script.
func buildClineHookOps(proj *model.Project, wrapperPaths map[*model.Hook]string, permsHookWrappers map[*model.Hook]string, projRoot string, permsGateRefs []string) []plugin.Operation {
	if len(proj.Hooks) == 0 && len(permsGateRefs) == 0 {
		return nil
	}

	scripts := map[string]*clineEventScript{}
	eventOrder := []string{}
	ensureScript := func(event string) *clineEventScript {
		s, ok := scripts[event]
		if !ok {
			s = &clineEventScript{event: event}
			scripts[event] = s
			eventOrder = append(eventOrder, event)
		}
		return s
	}

	for _, h := range proj.Hooks {
		if h == nil || h.Disabled {
			continue
		}
		event, matcher := resolveClineEvent(h)
		if event == "" {
			continue
		}

		// Determine the command path to invoke for legacy ScriptPath /
		// command handlers. Wrapper precedence applies here too.
		cmdPath := h.ScriptPath
		if w, ok := wrapperPaths[h]; ok {
			cmdPath = w
		} else if w, ok := permsHookWrappers[h]; ok {
			cmdPath = w
		}

		source := proj.SourceTag(h.ScriptPath)
		if source == "" {
			source = "hooks.yaml"
		}

		var handlers []string
		var warnings []plugin.Warning

		// v2 Handlers take precedence when populated; otherwise fall back
		// to the legacy single ScriptPath command.
		if len(h.Handlers) > 0 {
			for _, hd := range h.Handlers {
				snippet, warn := renderClineHandler(hd, cmdPath, source, h)
				if warn != nil {
					warnings = append(warnings, *warn)
				}
				if snippet != "" {
					handlers = append(handlers, snippet)
				}
			}
		} else if cmdPath != "" {
			handlers = append(handlers, renderClineCommandSnippet(cmdPath))
		}

		if len(handlers) == 0 && len(warnings) == 0 {
			continue
		}

		s := ensureScript(event)
		s.warnings = append(s.warnings, warnings...)
		if len(handlers) > 0 {
			s.sections = append(s.sections, clineHookSection{
				matcher:  matcher,
				kind:     matcherKindFor(h),
				handlers: handlers,
				source:   source,
			})
		}
	}

	// Perms-guard bare gates → no-matcher sections on PreToolUse so they
	// fire on every tool call.
	for _, gate := range permsGateRefs {
		gateAbs := filepath.Join(projRoot, gate)
		s := ensureScript("PreToolUse")
		s.sections = append(s.sections, clineHookSection{
			matcher:  "",
			kind:     "",
			handlers: []string{renderClineCommandSnippet(gateAbs)},
			source:   "permissions.yaml",
		})
	}

	sort.Strings(eventOrder)

	var ops []plugin.Operation
	for _, event := range eventOrder {
		s := scripts[event]
		if s == nil || (len(s.sections) == 0 && len(s.warnings) == 0) {
			continue
		}

		body := renderClineEventScript(s)
		sources := collectClineHookSources(s)
		op := plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     filepath.ToSlash(filepath.Join(".clinerules", "hooks", event)),
			Content:  body,
			Mode:     plugin.ModeWrite,
			FileMode: 0o755,
			Sources:  sources,
			Plugin:   "cline",
			Warnings: s.warnings,
		}
		ops = append(ops, op)
	}
	return ops
}

// resolveClineEvent returns the wire event name + matcher hint for h on
// Cline. The v2 EventCanonical path consults MapHookEventFor (so per-action
// events translate to PreToolUse/PostToolUse + matcher per SPEC §4.4.4);
// the v0.8 Event string falls through mapClineEvent verbatim. When both a
// table-supplied matcher hint and a user-supplied pattern exist they are
// joined with `|` (an OR group), mirroring combineMatchers in
// hooks_claude_shape.go.
func resolveClineEvent(h *model.Hook) (string, string) {
	if h.EventCanonical != "" {
		raw := string(h.EventCanonical)
		if strings.HasPrefix(raw, "native:") {
			return strings.TrimPrefix(raw, "native:"), matcherStringFor(h)
		}
		if m, ok := MapHookEventFor("cline", h.EventCanonical); ok {
			user := matcherStringFor(h)
			return m.Event, combineMatchers(m.Matcher, user)
		}
		// Canonical event with no Cline mapping — fall through to the
		// raw event string. Plugins that lack the event will still emit
		// the script; the hook engine simply will not fire it.
		return mapClineEvent(raw), matcherStringFor(h)
	}
	return mapClineEvent(h.Event), h.Matcher
}

// matcherKindFor returns the matcher kind for h ("exact", "regex", or
// "all"). Prefers the typed MatcherV2 struct; falls back to "exact" when
// only the v0.8 Matcher string is set (the v0.8 contract treats the
// pipe-joined string as a list of exact tool names).
func matcherKindFor(h *model.Hook) string {
	if h.MatcherV2.Kind != "" {
		return h.MatcherV2.Kind
	}
	if h.Matcher == "" {
		return "all"
	}
	return "exact"
}

// renderClineHandler turns one HookHandler into a shell snippet that
// dispatches the handler. cmdPath is the (possibly wrapper-rewritten)
// command path for v0.8-style command handlers; v2 handlers carry their
// own Command field, which is preferred when set. Returns ("", warning)
// for unsupported handler kinds on Cline.
func renderClineHandler(hd model.HookHandler, cmdPath, source string, h *model.Hook) (string, *plugin.Warning) {
	switch hd.Kind {
	case "", model.HookHandlerCommand:
		cmd := hd.Command
		if cmd == "" {
			cmd = cmdPath
		} else if len(hd.Args) > 0 {
			cmd = cmd + " " + strings.Join(hd.Args, " ")
		}
		if cmd == "" {
			return "", nil
		}
		return renderClineCommandSnippet(cmd), nil
	case model.HookHandlerHTTP:
		if hd.URL == "" {
			return "", nil
		}
		return renderClineHTTPSnippet(hd), nil
	case model.HookHandlerMCPTool, model.HookHandlerPrompt, model.HookHandlerAgent:
		return "", &plugin.Warning{
			Source:   source,
			Severity: "warn",
			Message:  fmt.Sprintf("Cline cannot dispatch %q hook handlers; %s/%s skipped.", hd.Kind, h.Event, strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))),
		}
	default:
		return "", &plugin.Warning{
			Source:   source,
			Severity: "warn",
			Message:  fmt.Sprintf("Cline: unknown hook handler kind %q; skipped.", hd.Kind),
		}
	}
}

// renderClineCommandSnippet renders a shell statement that pipes the
// captured stdin payload into the user-supplied command, propagating its
// exit status. The hook payload is held in $payload (the dispatcher
// preamble captures stdin once).
func renderClineCommandSnippet(command string) string {
	// The command string may already contain arguments; emit it verbatim
	// so spaces and quoting in user-supplied commands keep working
	// (mirrors how the v0.8 JSON shape passed the field straight to the
	// hook engine).
	return fmt.Sprintf("  printf '%%s' \"$payload\" | %s", command)
}

// renderClineHTTPSnippet emits a curl invocation that POSTs the captured
// payload to hd.URL, propagating any user-supplied headers.
func renderClineHTTPSnippet(hd model.HookHandler) string {
	var b strings.Builder
	b.WriteString("  printf '%s' \"$payload\" | curl -sS -X POST")
	keys := make([]string, 0, len(hd.Headers))
	for k := range hd.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " -H %s", shellQuote(k+": "+hd.Headers[k]))
	}
	b.WriteString(" -H ")
	b.WriteString(shellQuote("Content-Type: application/json"))
	b.WriteString(" --data-binary @- ")
	b.WriteString(shellQuote(hd.URL))
	return b.String()
}

// collectClineHookSources flattens the per-section source tags into a
// stable, deduplicated slice for Operation.Sources.
func collectClineHookSources(s *clineEventScript) []string {
	seen := map[string]bool{}
	var out []string
	for _, sec := range s.sections {
		if sec.source == "" || seen[sec.source] {
			continue
		}
		seen[sec.source] = true
		out = append(out, sec.source)
	}
	if len(out) == 0 {
		out = []string{"hooks.yaml"}
	}
	return out
}

// renderClineEventScript writes the bash dispatcher script for one event.
// Layout:
//
//	#!/usr/bin/env bash
//	set -euo pipefail
//	payload=$(cat)
//	tool_name=$(jq -r '.tool_name // empty' <<<"$payload")   # or sed fallback
//	# section 1 (matcher: Edit|Write)
//	if case "$tool_name" in Edit|Write) true ;; *) false ;; esac; then
//	  ... handler ...
//	fi
//	# section 2 (no matcher)
//	... handler ...
//
// jq is preferred for tool_name extraction when available — the fallback
// sed pattern keeps the script jq-free for minimal container images.
func renderClineEventScript(s *clineEventScript) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# prism-generated Cline hook dispatcher (filename-dispatch per SPEC §4.4.5).\n")
	fmt.Fprintf(&b, "# Event: %s. One script per event; sections fan out sequentially.\n", s.event)
	b.WriteString("set -euo pipefail\n\n")
	b.WriteString("payload=$(cat)\n")
	b.WriteString("if command -v jq >/dev/null 2>&1; then\n")
	b.WriteString("  tool_name=$(printf '%s' \"$payload\" | jq -r '.tool_name // empty')\n")
	b.WriteString("else\n")
	b.WriteString("  tool_name=$(printf '%s' \"$payload\" | sed -n 's/.*\"tool_name\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p')\n")
	b.WriteString("fi\n\n")

	for i, sec := range s.sections {
		b.WriteString(renderClineSection(i+1, sec))
		b.WriteString("\n")
	}
	return b.String()
}

// renderClineSection emits one (matcher, handler-list) section. The
// matcher becomes a guard clause that skips this section's handlers when
// $tool_name does not match — subsequent sections still get a chance to
// run (handlers are not mutually exclusive across sections).
func renderClineSection(idx int, sec clineHookSection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# section %d", idx)
	if sec.matcher != "" {
		fmt.Fprintf(&b, " (matcher: %s)", sec.matcher)
	}
	b.WriteString("\n")

	patterns := clineMatcherPatterns(sec.matcher, sec.kind)
	if len(patterns) > 0 {
		if sec.kind == "regex" {
			fmt.Fprintf(&b, "if [[ \"$tool_name\" =~ %s ]]; then\n", shellQuoteRegex(sec.matcher))
		} else {
			fmt.Fprintf(&b, "if case \"$tool_name\" in %s) true ;; *) false ;; esac; then\n", strings.Join(patterns, "|"))
		}
	}

	for _, snippet := range sec.handlers {
		b.WriteString(snippet)
		b.WriteString("\n")
	}
	if len(patterns) > 0 {
		b.WriteString("fi\n")
	}
	return b.String()
}

// clineMatcherPatterns splits a pipe-joined matcher into its component
// patterns. Empty matcher returns nil (no guard needed). For regex
// kind the matcher is treated as a single pattern (no splitting).
func clineMatcherPatterns(matcher, kind string) []string {
	if matcher == "" {
		return nil
	}
	if kind == "regex" {
		return []string{matcher}
	}
	parts := strings.Split(matcher, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// shellQuoteRegex returns a bash =~ -safe rendering of a regex literal.
// =~ wants the regex unquoted, but for whitespace safety we wrap the
// pattern in double quotes and escape internal `"` and `\`.
func shellQuoteRegex(r string) string {
	esc := strings.ReplaceAll(r, "\\", "\\\\")
	esc = strings.ReplaceAll(esc, "\"", "\\\"")
	return "\"" + esc + "\""
}

// clineMCPServerJSON is the schema Cline expects for entries under
// `cline_mcp_settings.json`'s `mcpServers` map. Identical to Claude's
// .mcp.json shape, by convention, with Cline-specific richness for
// Headers, TimeoutMs, and AutoApprove (per SPEC §12 cli column).
type clineMCPServerJSON struct {
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	TimeoutMs   int               `json:"timeoutMs,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
}

// buildClineMCPOp emits an OpMerge for .cline/cline_mcp_settings.json.
// The op carries a Merger closure that merges proj.MCP into any existing
// file at that path, preserving unrelated keys the user may have added.
// Mirrors plugins/claude.go's buildMCPOp pattern.
func buildClineMCPOp(proj *model.Project) (plugin.Operation, error) {
	mcpRel := filepath.ToSlash(filepath.Join(".cline", "cline_mcp_settings.json"))

	var warnings []plugin.Warning
	for _, srv := range proj.MCP {
		if srv == nil || srv.ScopePath == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   srv.ScopePath + "/mcp.yaml",
			Message:  fmt.Sprintf("scoped MCP server %q from %s/mcp.yaml applied project-wide; Cline has no per-scope MCP", srv.Name, srv.ScopePath),
			Severity: "info",
		})
	}

	merger := func(existing []byte) (string, error) {
		doc := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &doc); err != nil {
				return "", fmt.Errorf("cline: parsing existing %s: %w", mcpRel, err)
			}
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		for _, srv := range proj.MCP {
			if srv == nil || srv.Name == "" {
				continue
			}
			entry := clineMCPServerJSON{
				Command:     srv.Command,
				Args:        srv.Args,
				Env:         srv.Env,
				Headers:     srv.Headers,
				TimeoutMs:   srv.TimeoutMs,
				AutoApprove: srv.AutoApprove,
				Disabled:    srv.Disabled,
			}
			if srv.Command == "" && srv.URL != "" {
				entry.URL = srv.URL
			}
			servers[srv.Name] = entry
		}
		doc["mcpServers"] = servers
		content, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		return string(content) + "\n", nil
	}

	// Pre-render Content for the empty-existing case so static inspection
	// (tests, dry-run reporting) sees the projected JSON without having to
	// invoke Merger. The engine still calls Merger at apply time to merge
	// onto any existing file.
	initial, err := merger(nil)
	if err != nil {
		return plugin.Operation{}, err
	}

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     mcpRel,
		Content:  initial,
		Mode:     plugin.ModeWrite,
		Sources:  []string{"mcp.yaml"},
		Plugin:   "cline",
		Warnings: warnings,
		Merger:   merger,
	}, nil
}

// renderClineScope builds the markdown body for a scope rule file with
// YAML frontmatter: `paths:` carries the scope's glob array (native
// scope enforcement), `description:` carries the human-readable trigger
// hint. The body keeps the legacy `## When working in <path>` preamble
// so the model sees the scope label even if it ignores frontmatter.
func renderClineScope(scope *model.Scope, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if scope.Name != "" {
		fmt.Fprintf(&b, "name: %s\n", renderYAMLScalar(scope.Name))
	}
	if scope.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", renderYAMLScalar(scope.Description))
	}
	globs := scope.Globs
	if len(globs) == 0 && scope.Path != "" {
		globs = []string{scope.Path + "/**"}
	}
	if len(globs) > 0 {
		b.WriteString("paths:\n")
		for _, g := range globs {
			fmt.Fprintf(&b, "  - %s\n", renderYAMLScalar(g))
		}
	}
	// extensions.cline.* verbatim passthrough (SPEC §5.1). Emitted
	// after the structured keys so user-supplied keys do not collide
	// with prism-generated ones.
	if ext := renderClineExtensions(scope.Extensions); ext != "" {
		b.WriteString(ext)
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "## When working in %s\n\n", scope.Path)
	if scope.Description != "" {
		fmt.Fprintf(&b, "> Description: %s\n", scope.Description)
	} else if len(scope.Globs) > 0 {
		fmt.Fprintf(&b, "> Triggers: %s\n", strings.Join(scope.Globs, ", "))
	}
	b.WriteString("\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderClineSkill builds the markdown body for a skill rule file. When
// the skill has a non-empty Globs slice (or inherits a scope), the
// frontmatter carries them as `paths:` so the rule loads only on
// matching files; otherwise no frontmatter is emitted.
//
// v2 additive read: globs are sourced via skillGlobsFor, which prefers
// the typed Activation.Globs over the v0.8 top-level Globs slice. The
// parser keeps both in sync, so existing inputs behave identically.
func renderClineSkill(skill *model.Skill, body string) string {
	var b strings.Builder
	skillGlobs := skillGlobsFor(skill)
	extLines := renderClineExtensions(skill.Extensions)
	hasFrontmatter := skill.Description != "" || len(skillGlobs) > 0 || skill.ScopePath != "" || extLines != ""
	if hasFrontmatter {
		b.WriteString("---\n")
		if skill.Description != "" {
			fmt.Fprintf(&b, "description: %s\n", renderYAMLScalar(skill.Description))
		}
		globs := skillGlobs
		if len(globs) == 0 && skill.ScopePath != "" {
			globs = []string{skill.ScopePath + "/**"}
		}
		if len(globs) > 0 {
			b.WriteString("paths:\n")
			for _, g := range globs {
				fmt.Fprintf(&b, "  - %s\n", renderYAMLScalar(g))
			}
		}
		// extensions.cline.* verbatim passthrough (SPEC §5.1).
		if extLines != "" {
			b.WriteString(extLines)
		}
		b.WriteString("---\n\n")
	}
	if skill.ScopePath != "" {
		fmt.Fprintf(&b, "## When working in %s\n\n", skill.ScopePath)
	}
	fmt.Fprintf(&b, "## Skill: %s\n\n", skill.Name)
	hint := skill.Description
	if hint == "" {
		hint = skill.Trigger
	}
	if hint != "" {
		fmt.Fprintf(&b, "> %s\n", hint)
	}
	b.WriteString("\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderClineWorkflow builds the markdown body for a workflow file at
// .clinerules/workflows/<name>.md. Cline reads the file when the user
// types `/<filename>` (sans extension) and replays the body into the
// next user turn. We retain a `## Command /<name>` header so the body
// is self-describing when read directly.
func renderClineWorkflow(cmd *model.Command, body string) string {
	var b strings.Builder
	extLines := renderClineExtensions(cmd.Extensions)
	hasFrontmatter := cmd.Description != "" || extLines != ""
	if hasFrontmatter {
		b.WriteString("---\n")
		if cmd.Description != "" {
			fmt.Fprintf(&b, "description: %s\n", renderYAMLScalar(cmd.Description))
		}
		// extensions.cline.* verbatim passthrough (SPEC §5.1).
		if extLines != "" {
			b.WriteString(extLines)
		}
		b.WriteString("---\n\n")
	}
	if cmd.ScopePath != "" {
		fmt.Fprintf(&b, "## When working in %s\n\n", cmd.ScopePath)
	}
	fmt.Fprintf(&b, "## Command /%s\n\n", cmd.Name)
	if cmd.Description != "" {
		fmt.Fprintf(&b, "> %s\n", cmd.Description)
	}
	b.WriteString("\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ensureTrailingNewline returns s with a trailing newline if it is
// non-empty. Empty input is returned as-is so we never write a lone "\n".
func ensureTrailingNewline(s string) string {
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// sourceOrEmpty returns the first element of sources, or "" if empty.
// Warnings need a Source field; this avoids out-of-range issues when a
// document has no source path.
func sourceOrEmpty(sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	return sources[0]
}

// renderClineExtensions returns the YAML frontmatter lines (each ending
// with \n) that pass `extensions.cline.*` verbatim into the emitted
// rule / workflow file. Returns "" when the primitive carries no
// cline-namespaced extensions block.
//
// SPEC §5.1: extensions are plugin-namespaced opaque pass-through. The
// renderer only touches the `cline` namespace and emits whatever scalar
// or list values the user wrote — it does NOT validate or transform.
// Nested maps / structured values are emitted via a minimal YAML
// flow-style fallback (json.Marshal) so they remain syntactically valid
// even when the user supplies a deeper shape.
//
// Keys are sorted for deterministic output (so golden tests stay
// stable). The slice of lines is the caller's to insert between
// `description:` / `paths:` and the closing `---`.
func renderClineExtensions(ext map[string]any) string {
	if len(ext) == 0 {
		return ""
	}
	raw, ok := ext["cline"].(map[string]any)
	if !ok || len(raw) == 0 {
		return ""
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		v := raw[k]
		switch tv := v.(type) {
		case string:
			fmt.Fprintf(&b, "%s: %s\n", k, renderYAMLScalar(tv))
		case bool:
			fmt.Fprintf(&b, "%s: %t\n", k, tv)
		case int, int32, int64, float32, float64:
			fmt.Fprintf(&b, "%s: %v\n", k, tv)
		case []any:
			// Render as YAML block-style list of scalars; complex items
			// fall through to JSON.
			fmt.Fprintf(&b, "%s:\n", k)
			for _, item := range tv {
				switch iv := item.(type) {
				case string:
					fmt.Fprintf(&b, "  - %s\n", renderYAMLScalar(iv))
				case bool:
					fmt.Fprintf(&b, "  - %t\n", iv)
				case int, int32, int64, float32, float64:
					fmt.Fprintf(&b, "  - %v\n", iv)
				default:
					if raw, err := json.Marshal(item); err == nil {
						fmt.Fprintf(&b, "  - %s\n", raw)
					}
				}
			}
		default:
			// Map or other structured value — fall back to JSON
			// (flow-style YAML is a JSON superset).
			if raw, err := json.Marshal(v); err == nil {
				fmt.Fprintf(&b, "%s: %s\n", k, raw)
			}
		}
	}
	return b.String()
}
