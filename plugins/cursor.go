// Package plugins contains the projection plugins shipped with agents.
//
// CursorPlugin projects a canonical .agents/ directory into Cursor's
// on-disk layout (as of Cursor 2.4/2.5):
//
//   - `.cursor/rules/*.mdc`      — context + scoped rules (YAML frontmatter + body)
//   - `.cursor/mcp.json`         — MCP servers (merged with existing file)
//   - `.cursor/skills/<n>/SKILL.md` — native skills (frontmatter shape matches Claude)
//   - `.cursor/commands/<n>.md`  — slash commands (bare markdown, NO frontmatter)
//   - `.cursor/agents/<n>.md`    — subagents (markdown + YAML frontmatter)
//   - `.cursor/hooks.json`       — hooks dispatch table (Cursor 2.4+)
//   - `.cursor/hooks/__scope-guard__/*.sh` — wrappers for scoped hooks
//
// Skills format choice: this plugin emits ONLY the new `.cursor/skills/`
// format (no dual-emit). The previous `.cursor/rules/skill-<n>.mdc`
// degraded form is gone — users on Cursor < 2.4 should pin v0.7 or
// hand-author rules. Documented in CHANGELOG.
//
// Hook.Event → Cursor event mapping (canonical prism events on the left,
// Cursor 2.4+ event names on the right):
//
//	PreToolUse           → preToolUse
//	PostToolUse          → postToolUse
//	SessionStart         → sessionStart
//	SessionEnd           → sessionEnd
//	UserPromptSubmit     → beforeSubmitPrompt
//	Stop                 → stop
//	Notification         → (no Cursor analog; warn + drop)
//	SubagentStop         → (no Cursor analog; warn + drop)
//	PreCompact           → (no Cursor analog; warn + drop)
//
// Cursor-native event names (camelCase: `beforeShellExecution`,
// `afterFileEdit`, `beforeTabFileRead`, `afterTabFileEdit`,
// `workspaceOpen`, plus any of the above) are passed through verbatim
// so users can target Cursor-specific events without prism translation.
//
// Permissions remain unsupported in this version — a sandbox-profile
// generator is planned for v0.8.1.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/version"
)

// CursorPlugin projects Project state into Cursor's `.cursor/` layout.
type CursorPlugin struct {
	// DisableHookWrappers mirrors ClaudePlugin: when true, scoped hooks are
	// projected as if they were global (no `__scope-guard__` wrapper).
	DisableHookWrappers bool
}

// NewCursor constructs a CursorPlugin.
//
// The plugins package hosts multiple plugins that each want a `New`
// constructor, which would collide at package scope. We expose
// `NewCursor` as the canonical constructor for this plugin.
func NewCursor() *CursorPlugin { return &CursorPlugin{} }

// Name returns the stable plugin identifier.
func (p *CursorPlugin) Name() string { return "cursor" }

// Detect returns true if the project at root looks like it uses Cursor.
// We treat the presence of `.cursor/` (the modern rules dir) OR the legacy
// `.cursorrules` file at the project root as activation signals.
func (p *CursorPlugin) Detect(root string) bool {
	if info, err := os.Stat(filepath.Join(root, ".cursor")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, ".cursorrules")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// Capabilities returns Cursor's capability matrix.
//
// Cursor 2.4/2.5 added native support for skills, slash commands,
// subagents, and hooks (in addition to the long-standing context rules
// and MCP). Permissions remain without a native primitive; deferring
// the sandbox-profile generator to v0.8.1.
//
// v0.9.0 (Phase 2a): per-field cells are populated under
// {Agent,Skill,Command,Hook,MCPServer,Permissions,Scope}Fields per
// SPEC §12 (Cursor column, "cur"). Coarse v0.8 cells preserved for
// backward compatibility — only NON-native fields are listed (absent =
// native). Extensions namespaces the plugin reads under
// `extensions.cursor:` are declared per-primitive.
func (p *CursorPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportNative,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportNative,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportNative,

		// SPEC §12 Agent (cur column).
		AgentFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"ModelFallbacks":   plugin.FieldSilent,
				"Tools":            plugin.FieldSilent,
				"DisallowedTools":  plugin.FieldDegraded,
				"MaxTurns":         plugin.FieldSilent,
				"Temperature":      plugin.FieldSilent,
				"MCPServers":       plugin.FieldUnsupported,
				"AllowedSubagents": plugin.FieldUnsupported,
				"UserInvocable":    plugin.FieldSilent,
				"ModelInvocable":   plugin.FieldSilent,
				"InitialPrompt":    plugin.FieldUnsupported,
				"ScopePath":        plugin.FieldDegraded,
			},
			Extensions: []string{"cursor"},
		},

		// SPEC §12 Skill (cur column).
		SkillFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"WhenToUse":                 plugin.FieldDegraded,
				"Activation.ContentRegex":   plugin.FieldUnsupported,
				"Activation.UserInvocable":  plugin.FieldSilent,
				"Activation.ModelInvocable": plugin.FieldSilent,
				"AllowedTools":              plugin.FieldDegraded,
				"Arguments":                 plugin.FieldDegraded,
				"Model":                     plugin.FieldUnsupported,
				"Subagent":                  plugin.FieldDegraded,
				"ScopePath":                 plugin.FieldDegraded,
			},
			Extensions: []string{"cursor"},
		},

		// SPEC §12 Command (cur column).
		CommandFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Description":  plugin.FieldUnsupported,
				"ArgumentHint": plugin.FieldSilent,
				"Arguments":    plugin.FieldDegraded,
				"Model":        plugin.FieldUnsupported,
				"Tools":        plugin.FieldUnsupported,
				"Agent":        plugin.FieldDegraded,
				"AutoInvoke":   plugin.FieldSilent,
				"ScopePath":    plugin.FieldDegraded,
			},
			Extensions: []string{"cursor"},
		},

		// SPEC §12 Hook (cur column).
		HookFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":                plugin.FieldSilent,
				"Description":         plugin.FieldSilent,
				"Event (Claude-only)": plugin.FieldUnsupported,
				"Handlers (http)":     plugin.FieldUnsupported,
				"Handlers (mcp_tool)": plugin.FieldUnsupported,
				"Handlers (agent)":    plugin.FieldUnsupported,
				"Sequential":          plugin.FieldSilent,
				"StatusMessage":       plugin.FieldSilent,
				"Async":               plugin.FieldUnsupported,
				"Once":                plugin.FieldUnsupported,
				"If":                  plugin.FieldUnsupported,
				"Cwd":                 plugin.FieldSilent,
				"Env":                 plugin.FieldUnsupported,
				"Bash + Powershell":   plugin.FieldUnsupported,
				"ScopePath":           plugin.FieldDegraded,
			},
			Extensions: []string{"cursor"},
		},

		// SPEC §12 MCPServer (cur column).
		MCPServerFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Auth.Scheme=oauth": plugin.FieldDegraded,
				"Cwd":               plugin.FieldSilent,
				"TimeoutMs":         plugin.FieldUnsupported,
				"AutoApprove":       plugin.FieldUnsupported,
				"Trust":             plugin.FieldUnsupported,
				"IncludeTools":      plugin.FieldUnsupported,
				"ExcludeTools":      plugin.FieldUnsupported,
				"ScopePath":         plugin.FieldDegraded,
			},
			Extensions: []string{"cursor"},
		},

		// SPEC §12 Permissions (cur column).
		// Cursor sandbox has no `ask` bucket; allow/deny degrade.
		PermissionsFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Allow (global)":   plugin.FieldDegraded,
				"Ask (global)":     plugin.FieldUnsupported,
				"Deny (global)":    plugin.FieldDegraded,
				"Allow (scoped)":   plugin.FieldUnsupported,
				"Ask (scoped)":     plugin.FieldUnsupported,
				"Deny (scoped)":    plugin.FieldUnsupported,
				"Edit/Read/Write:": plugin.FieldDegraded,
				"mcp: target":      plugin.FieldUnsupported,
			},
			Extensions: []string{"cursor"},
		},

		// SPEC §12 Scope (cur column).
		ScopeFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Path (cascade)":     plugin.FieldDegraded,
				"Activation=Cascade": plugin.FieldDegraded,
				"Priority":           plugin.FieldDegraded,
				"Tags":               plugin.FieldSilent,
				"IsOverride":         plugin.FieldUnsupported,
			},
			Extensions: []string{"cursor"},
		},
	}
}

// Plan produces the Operations needed to project proj into `.cursor/`.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Cursor projection never symlinks — the .mdc files are not byte-identical
// to source (frontmatter is injected) and `.cursor/mcp.json` is a merged
// file. Unknown modes return an error.
func (p *CursorPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	switch opts.Mode {
	case "", "write":
	default:
		return nil, fmt.Errorf("cursor: unsupported mode %q", opts.Mode)
	}

	var ops []plugin.Operation
	var warnings []plugin.Warning

	// Cross-emit dedup (SPEC §4.1.4 / IMPLEMENTATION_PLAN §7.4): when both
	// `claude` and `cursor` targets are enabled, Cursor reads
	// `.claude/agents/` natively, so we avoid double-emission by emitting
	// agents to `.cursor/agents/` only and surfacing ONE info-level
	// warning per project (not per agent) at the start of the agent emit
	// loop.
	//
	// Override: `extensions.cursor.disable_dedup: true` on the project
	// Config skips the dedup logic and suppresses the warning (agents
	// still emit to `.cursor/agents/` as their native location regardless;
	// the override exists so the `.claude/` plugin also keeps emitting
	// agents in its own tree without the extra info warning here).
	emitCursorDedupWarning := cursorCrossEmitActive(proj) && !cursorDedupDisabled(proj)

	if proj.Context != nil {
		content := renderMDC("Project-wide context", nil, true, proj.Context.Body, nil)
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".cursor/rules/_root.mdc",
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		})
	}

	for _, sc := range proj.Scopes {
		if sc == nil {
			continue
		}
		desc := sc.Description
		if desc == "" {
			desc = fmt.Sprintf("Context for %s", sc.Path)
		}
		body := ""
		var sources []string
		if sc.Document != nil {
			body = sc.Document.Body
			sources = []string{proj.SourceTag(sc.Document.SourcePath)}
		}
		content := renderMDC(desc, sc.Globs, false, body, cursorExtensionKVs(sc.Extensions))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".cursor", "rules", slugify(sc.Path)+".mdc")),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		src := ""
		if sc.Document != nil {
			src = proj.SourceTag(sc.Document.SourcePath)
		}
		// Scope/Priority (degraded — synthesized as filename prefix, but the
		// canonical Priority enum is not preserved; emit info warning).
		if sc.Priority != "" && sc.Priority != model.PriorityNormal {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("scope %q: Scope.Priority %q degraded to filename-prefix ordering only (Cursor has no priority field).", sc.Path, sc.Priority),
				Severity: "info",
			})
		}
		// Scope/IsOverride (unsupported — Cursor has no override semantic).
		if sc.IsOverride {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("scope %q: Scope.IsOverride dropped (Cursor has no override semantic).", sc.Path),
				Severity: "warn",
			})
		}
		ops = append(ops, op)
	}

	// Skills → `.cursor/skills/<dirname>/SKILL.md` (native, Cursor 2.4+).
	// Source body already contains YAML frontmatter (same shape Claude
	// uses), so we project the body verbatim.
	for _, sk := range proj.Skills {
		if sk == nil || sk.Document == nil {
			continue
		}
		dirName := scopedName(sk.ScopePath, skillSlug(sk.Name))
		skillPath := filepath.ToSlash(filepath.Join(".cursor", "skills", dirName, "SKILL.md"))
		skillOp := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    skillPath,
			Content: renderCursorSkillBody(sk),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(sk.Document.SourcePath)},
		}
		src := proj.SourceTag(sk.Document.SourcePath)
		// Skill/WhenToUse (degraded — Cursor SKILL.md has no when_to_use
		// frontmatter key; surface as part of description only).
		if sk.WhenToUse != "" {
			skillOp.Warnings = append(skillOp.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("skill %q: Skill.WhenToUse degraded — Cursor has no when_to_use frontmatter key; content folded into description.", sk.Name),
				Severity: "info",
			})
		}
		// Skill/AllowedTools (degraded — Cursor skills don't gate tools).
		if len(sk.AllowedTools) > 0 {
			skillOp.Warnings = append(skillOp.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("skill %q: Skill.AllowedTools degraded — Cursor skills inherit ambient tool permissions; allowlist not enforced.", sk.Name),
				Severity: "info",
			})
		}
		// Skill/Arguments (degraded — no native arguments contract).
		if len(sk.Arguments) > 0 {
			skillOp.Warnings = append(skillOp.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("skill %q: Skill.Arguments degraded — Cursor has no structured Arguments contract; document them in the body.", sk.Name),
				Severity: "info",
			})
		}
		// Skill/Subagent (degraded — pinning a subagent is not supported,
		// invocation falls back to the active model).
		if sk.Subagent != "" {
			skillOp.Warnings = append(skillOp.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("skill %q: Skill.Subagent %q degraded — Cursor skills cannot pin a specific subagent.", sk.Name, sk.Subagent),
				Severity: "info",
			})
		}
		// Skill/Model (unsupported — Cursor skills have no model pin).
		if sk.Model != "" {
			skillOp.Warnings = append(skillOp.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("skill %q: Skill.Model %q dropped (Cursor has no per-skill model pin).", sk.Name, sk.Model),
				Severity: "warn",
			})
		}
		// Skill/Activation.ContentRegex (unsupported).
		if sk.Activation.ContentRegex != "" {
			skillOp.Warnings = append(skillOp.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("skill %q: Skill.Activation.ContentRegex dropped (Cursor has no content-regex trigger).", sk.Name),
				Severity: "warn",
			})
		}
		// Skill/ScopePath (degraded — Cursor skills are global, scope is
		// only preserved via filename prefix).
		if sk.ScopePath != "" {
			skillOp.Warnings = append(skillOp.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("scoped skill %q (scopepath %s) projected without path enforcement (Cursor skills are global).", sk.Name, sk.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, skillOp)

		// Scripts are co-located under scripts/<basename>; since Plan is
		// pure (no file I/O) we emit symlinks pointing back at the source
		// and let the engine downgrade to copy as needed.
		for _, scriptPath := range sk.Scripts {
			if scriptPath == "" {
				continue
			}
			scriptDir := filepath.ToSlash(filepath.Join(".cursor", "skills", dirName))
			scriptOp, err := buildCursorScriptOp(proj, scriptPath, scriptDir)
			if err != nil {
				return nil, err
			}
			ops = append(ops, scriptOp)
		}
	}

	// Commands → `.cursor/commands/<n>.md` (native).
	// Cursor command files have NO frontmatter — the filename (minus .md)
	// becomes the slash command. Scoped commands keep the scopeSlug
	// prefix and carry a degrade warning (Cursor commands are global).
	for _, cmd := range proj.Commands {
		if cmd == nil || cmd.Document == nil {
			continue
		}
		fileName := scopedName(cmd.ScopePath, cmd.Name) + ".md"
		path := filepath.ToSlash(filepath.Join(".cursor", "commands", fileName))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: ensureTrailingNewline(cmd.Document.Body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(cmd.Document.SourcePath)},
		}
		src := proj.SourceTag(cmd.Document.SourcePath)
		// Command/Description (unsupported — Cursor command files have NO
		// frontmatter, so any Description is dropped).
		if cmd.Description != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("command %q: Command.Description dropped (Cursor commands have no frontmatter and cannot carry a description).", cmd.Name),
				Severity: "warn",
			})
		}
		// Command/Arguments (degraded — no native arguments schema).
		if len(cmd.Arguments) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("command %q: Command.Arguments degraded — Cursor has no structured Arguments schema; document them in the body.", cmd.Name),
				Severity: "info",
			})
		}
		// Command/Model (unsupported — no model pin).
		if cmd.Model != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("command %q: Command.Model %q dropped (Cursor commands cannot pin a model).", cmd.Name, cmd.Model),
				Severity: "warn",
			})
		}
		// Command/Tools (unsupported — no per-command tool gating).
		if len(cmd.Tools) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("command %q: Command.Tools dropped (Cursor commands inherit ambient tools).", cmd.Name),
				Severity: "warn",
			})
		}
		// Command/Agent (degraded — no native agent pin).
		if cmd.Agent != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("command %q: Command.Agent %q degraded — Cursor commands cannot pin a subagent.", cmd.Name, cmd.Agent),
				Severity: "info",
			})
		}
		// Command/ScopePath (degraded — Cursor commands are global).
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("scoped command %q (scopepath %s) projected without path enforcement (Cursor commands are global)", cmd.Name, cmd.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Agents → `.cursor/agents/<n>.md` (native).
	//
	// When the cross-emit dedup condition holds (claude target also
	// active and override not set), emit a single project-level info
	// warning explaining that we're skipping the `.claude/agents/`
	// double-emission. The warning fires once regardless of how many
	// agents the project has (SPEC §4.1.4 / IMPLEMENTATION_PLAN §7.4).
	if emitCursorDedupWarning && len(proj.Agents) > 0 {
		warnings = append(warnings, plugin.Warning{
			Source:   "",
			Message:  "cursor: emitting agents to .cursor/agents/ only; Cursor reads .claude/agents/ natively when present, so we're avoiding double-emission. Set extensions.cursor.disable_dedup: true on the project to override.",
			Severity: "info",
		})
	}
	for _, ag := range proj.Agents {
		if ag == nil || ag.Document == nil {
			continue
		}
		fileName := scopedName(ag.ScopePath, ag.Name) + ".md"
		path := filepath.ToSlash(filepath.Join(".cursor", "agents", fileName))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: ensureTrailingNewline(ag.Document.Body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(ag.Document.SourcePath)},
		}
		src := proj.SourceTag(ag.Document.SourcePath)
		// Agent/DisallowedTools (degraded — Cursor agents have no native
		// disallowed-tools list; the body must encode any restrictions).
		if len(ag.DisallowedTools) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("agent %q: Agent.DisallowedTools degraded — Cursor agents have no native disallowed-tools list; describe restrictions in the body.", ag.Name),
				Severity: "info",
			})
		}
		// Agent/MCPServers (unsupported — Cursor agents inherit the global
		// MCP set; per-agent MCP pinning is not honored).
		if len(ag.MCPServers) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("agent %q: Agent.MCPServers dropped (Cursor agents inherit the project's MCP set; per-agent MCPServers is unsupported).", ag.Name),
				Severity: "warn",
			})
		}
		// Agent/AllowedSubagents (unsupported — Cursor has no nested-agent
		// allowlist).
		if len(ag.AllowedSubagents) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("agent %q: Agent.AllowedSubagents dropped (Cursor has no nested-agent allowlist).", ag.Name),
				Severity: "warn",
			})
		}
		// Agent/InitialPrompt (unsupported — Cursor agents do not accept
		// an initial-prompt boot directive).
		if ag.InitialPrompt != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("agent %q: Agent.InitialPrompt dropped (Cursor agents do not honor an InitialPrompt boot directive).", ag.Name),
				Severity: "warn",
			})
		}
		// Agent/ScopePath (degraded — Cursor agents are global).
		if ag.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("scoped agent %q (scopepath %s) projected without path enforcement (Cursor agents are global)", ag.Name, ag.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts. Mirror ClaudePlugin: each scoped hook
	// gets a wrapper at `.cursor/hooks/__scope-guard__/<slug>-<event>-<basename>.sh`
	// that exec's `prism scope-guard --scope X --script Y`. Cursor's hook
	// contract is JSON-over-stdio (same as Claude), so the wrapper body is
	// identical.
	wrapperPaths := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		evt := cursorEventName(resolveHookEvent(h))
		if evt == "" {
			continue
		}
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + evt + "-" + hookName + ".sh"
		wrapperRel := filepath.ToSlash(filepath.Join(".cursor", "hooks", "__scope-guard__", wrapperFile))
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

	// `.cursor/hooks.json` — Cursor 2.4+ hooks dispatch table.
	if hasUsableHooks(proj.Hooks) {
		hookOp, hookWarnings := p.buildHooksOp(proj, wrapperPaths)
		ops = append(ops, hookOp)
		warnings = append(warnings, hookWarnings...)
	}

	// MCP → `.cursor/mcp.json` (native). Scoped MCP servers merge into the
	// global block with one info warning per server (no per-scope MCP).
	if len(proj.MCP) > 0 {
		mcpOp, err := p.buildMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}
	for _, srv := range proj.MCP {
		if srv == nil {
			continue
		}
		// MCPServer/ScopePath (degraded — Cursor has no per-scope MCP
		// block; scoped servers merge into the global one).
		if srv.ScopePath != "" {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("Cursor has no per-scope MCP; scoped server %q (scopepath %s) merged into global block.", srv.Name, srv.ScopePath),
				Severity: "info",
			})
		}
		// MCPServer/TimeoutMs (unsupported — no timeout field in
		// .cursor/mcp.json).
		if srv.TimeoutMs != 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("mcp server %q: TimeoutMs dropped (Cursor mcp.json has no timeout field).", srv.Name),
				Severity: "warn",
			})
		}
		// MCPServer/AutoApprove (unsupported — Cursor has no auto-approve list).
		if len(srv.AutoApprove) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("mcp server %q: AutoApprove dropped (Cursor has no per-server auto-approve list).", srv.Name),
				Severity: "warn",
			})
		}
		// MCPServer/Trust (unsupported — no trust toggle).
		if srv.Trust {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("mcp server %q: Trust flag dropped (Cursor mcp.json has no trust toggle).", srv.Name),
				Severity: "warn",
			})
		}
		// MCPServer/IncludeTools (unsupported — no tool allowlist).
		if len(srv.IncludeTools) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("mcp server %q: IncludeTools dropped (Cursor has no per-server tool allowlist).", srv.Name),
				Severity: "warn",
			})
		}
		// MCPServer/ExcludeTools (unsupported — no tool denylist).
		if len(srv.ExcludeTools) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("mcp server %q: ExcludeTools dropped (Cursor has no per-server tool denylist).", srv.Name),
				Severity: "warn",
			})
		}
	}

	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  "Cursor has no permissions primitive; permissions not projected.",
				Severity: "info",
			})
		}
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		if len(sp.Allow) == 0 && len(sp.Deny) == 0 && len(sp.Ask) == 0 {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   "",
			Message:  fmt.Sprintf("Cursor has no permissions primitive; scoped permissions (scope: %s) not projected.", sp.ScopePath),
			Severity: "info",
		})
	}

	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// cursorEventName maps a prism Hook.Event to its Cursor 2.4+ event name,
// or returns "" if the event has no Cursor analog (caller should warn +
// drop). Cursor-native camelCase names pass through verbatim.
//
// v0.9.0 (Phase 2a): when the canonical (string) Event is empty, this
// also consults the typed EventCanonical enum, since the v2 parser may
// populate only the canonical form for hooks declared via the v2 shape.
// String() on HookEvent produces the same casing used by the v0.8
// switch above.
func cursorEventName(event string) string {
	switch event {
	case "PreToolUse":
		return "preToolUse"
	case "PostToolUse":
		return "postToolUse"
	case "SessionStart":
		return "sessionStart"
	case "SessionEnd":
		return "sessionEnd"
	case "UserPromptSubmit":
		return "beforeSubmitPrompt"
	case "Stop":
		return "stop"
	case "Notification", "SubagentStop", "PreCompact":
		return ""
	}
	// Already-Cursor-shaped event names pass through.
	if event != "" && unicode.IsLower(rune(event[0])) {
		return event
	}
	return ""
}

// resolveHookEvent picks the effective event string for a hook, preferring
// the v0.8 Event field but falling back to EventCanonical (v2). Used at
// every call site that previously read h.Event directly.
//
// EventCanonical is snake_case (`pre_tool_use`); the v0.8 Event field is
// PascalCase (`PreToolUse`). When falling back we map the canonical enum
// to the equivalent PascalCase string so the cursor event switch above
// continues to match.
func resolveHookEvent(h *model.Hook) string {
	if h == nil {
		return ""
	}
	if h.Event != "" {
		return h.Event
	}
	switch h.EventCanonical {
	case model.EventPreToolUse:
		return "PreToolUse"
	case model.EventPostToolUse:
		return "PostToolUse"
	case model.EventSessionStart:
		return "SessionStart"
	case model.EventSessionEnd:
		return "SessionEnd"
	case model.EventUserPromptSubmit:
		return "UserPromptSubmit"
	case model.EventStop:
		return "Stop"
	case model.EventSubagentStop:
		return "SubagentStop"
	case model.EventPreCompact:
		return "PreCompact"
	}
	// Any other canonical event (or empty) — pass the raw string through;
	// cursorEventName will drop it if there's no analog.
	return string(h.EventCanonical)
}

// hasUsableHooks reports whether any hook in hs will translate to a
// usable Cursor event (i.e., cursorEventName returns non-empty).
func hasUsableHooks(hs []*model.Hook) bool {
	for _, h := range hs {
		if h == nil {
			continue
		}
		if cursorEventName(resolveHookEvent(h)) != "" {
			return true
		}
	}
	return false
}

// buildHooksOp constructs the OpWrite for `.cursor/hooks.json`. The shape:
//
//	{
//	  "version": 1,
//	  "hooks": {
//	    "preToolUse": [
//	      {"matcher": "Bash", "command": "<abs path>"}
//	    ],
//	    ...
//	  }
//	}
//
// Scoped hooks point their `command` at the wrapper's absolute path
// (resolved via wrapperPaths) so the scope-guard runs first.
//
// Portability note: Cursor's hooks.json has no ${PROJECT_DIR}-style
// substitution, so wrapper paths are baked in as absolute. Moving the
// project tree (mv / rsync / container mount) requires re-running
// `prism compile` to refresh the paths. Gemini's settings.json hooks do
// support env-var interpolation — see plugins/gemini.go for the
// ${PROJECT_DIR} form used there.
func (p *CursorPlugin) buildHooksOp(proj *model.Project, wrapperPaths map[*model.Hook]string) (plugin.Operation, []plugin.Warning) {
	type hookEntry struct {
		Matcher string `json:"matcher,omitempty"`
		Command string `json:"command"`
	}

	buckets := map[string][]hookEntry{}
	eventOrder := []string{}
	var warnings []plugin.Warning
	var sources []string
	seenSource := map[string]bool{}

	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		eventStr := resolveHookEvent(h)
		if eventStr == "" {
			continue
		}
		evt := cursorEventName(eventStr)
		if evt == "" {
			warnings = append(warnings, plugin.Warning{
				Source:   proj.SourceTag(h.ScriptPath),
				Message:  fmt.Sprintf("Cursor has no analog for hook event %q; dropped.", eventStr),
				Severity: "info",
			})
			continue
		}
		cmdPath := h.ScriptPath
		if w, ok := wrapperPaths[h]; ok {
			cmdPath = w
		}
		if _, seen := buckets[evt]; !seen {
			eventOrder = append(eventOrder, evt)
		}
		buckets[evt] = append(buckets[evt], hookEntry{
			Matcher: h.Matcher,
			Command: cmdPath,
		})
		tag := proj.SourceTag(h.ScriptPath)
		if tag != "" && !seenSource[tag] {
			seenSource[tag] = true
			sources = append(sources, tag)
		}

		// Field-level degrade / unsupported warnings (SPEC §12 Hook cur
		// column). Hook/ScopePath is degraded (Cursor has no per-scope
		// hooks; the scope-guard wrapper enforces at runtime instead).
		if h.ScopePath != "" {
			warnings = append(warnings, plugin.Warning{
				Source:   tag,
				Message:  fmt.Sprintf("scoped hook %q (scopepath %s) projected via scope-guard wrapper; Cursor has no per-scope Hook.ScopePath field.", h.Name, h.ScopePath),
				Severity: "info",
			})
		}
		// Hook/Env (unsupported — Cursor hooks have no env-injection
		// contract; environment variables on the handler are dropped).
		for _, hh := range h.Handlers {
			if len(hh.Env) > 0 {
				warnings = append(warnings, plugin.Warning{
					Source:   tag,
					Message:  fmt.Sprintf("hook %q: HookHandler.Env dropped (Cursor has no per-hook environment-variable injection).", h.Name),
					Severity: "warn",
				})
				break
			}
		}
	}

	sort.Strings(eventOrder)

	hooks := map[string]any{}
	for _, evt := range eventOrder {
		entries := buckets[evt]
		raw := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			m := map[string]any{"command": e.Command}
			if e.Matcher != "" {
				m["matcher"] = e.Matcher
			}
			raw = append(raw, m)
		}
		hooks[evt] = raw
	}
	doc := map[string]any{
		"version": 1,
		"hooks":   hooks,
	}

	content, _ := marshalJSONStable(doc)
	if len(sources) == 0 {
		sources = []string{"hooks.yaml"}
	}

	return plugin.Operation{
		Kind:     plugin.OpWrite,
		Path:     ".cursor/hooks.json",
		Content:  content,
		Mode:     plugin.ModeWrite,
		Plugin:   p.Name(),
		Sources:  sources,
		Warnings: nil,
	}, warnings
}

// buildCursorScriptOp emits a symlink (engine may downgrade) co-locating
// a skill's helper script under `<skillDir>/scripts/<basename>`.
func buildCursorScriptOp(proj *model.Project, scriptPath, skillDir string) (plugin.Operation, error) {
	base := filepath.Base(scriptPath)
	targetPath := filepath.ToSlash(filepath.Join(skillDir, "scripts", base))
	srcRel := proj.SourceTag(scriptPath)

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    plugin.ModeWrite,
		Sources: []string{srcRel},
		Plugin:  "cursor",
	}
	targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
	linkTarget, err := filepath.Rel(targetDir, scriptPath)
	if err != nil {
		return plugin.Operation{}, err
	}
	op.Kind = plugin.OpSymlink
	op.LinkTarget = filepath.ToSlash(linkTarget)
	return op, nil
}

// buildMCPOp emits the OpMerge for `.cursor/mcp.json`. The engine reads the
// existing file and hands the bytes to the Merger closure; Plan() touches
// nothing on disk. The merger overlays the `mcpServers` key built from
// proj.MCP onto whatever top-level structure already exists.
func (p *CursorPlugin) buildMCPOp(proj *model.Project) (plugin.Operation, error) {
	merger := func(existingBytes []byte) (string, error) {
		existing := map[string]any{}
		if len(existingBytes) > 0 {
			if jerr := json.Unmarshal(existingBytes, &existing); jerr != nil {
				return "", fmt.Errorf("cursor: parse existing .cursor/mcp.json: %w", jerr)
			}
		}

		servers := map[string]any{}
		for _, srv := range proj.MCP {
			if srv == nil || srv.Name == "" {
				continue
			}
			entry := map[string]any{}
			if srv.Command != "" {
				entry["command"] = srv.Command
			}
			if len(srv.Args) > 0 {
				entry["args"] = srv.Args
			}
			if len(srv.Env) > 0 {
				env := map[string]string{}
				for k, v := range srv.Env {
					env[k] = v
				}
				entry["env"] = env
			}
			if srv.URL != "" {
				entry["url"] = srv.URL
			}
			servers[srv.Name] = entry
		}
		existing["mcpServers"] = servers

		content, err := marshalJSONStable(existing)
		if err != nil {
			return "", fmt.Errorf("cursor: marshal .cursor/mcp.json: %w", err)
		}
		return content, nil
	}

	return plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    ".cursor/mcp.json",
		Mode:    plugin.ModeWrite,
		Plugin:  p.Name(),
		Sources: []string{"mcp.yaml"},
		Merger:  merger,
	}, nil
}

// marshalJSONStable pretty-prints a JSON value with sorted map keys at all
// levels. encoding/json already sorts top-level map[string]any keys when
// marshaling, but we re-run via json.MarshalIndent and ensure trailing
// newline for diff stability.
func marshalJSONStable(v any) (string, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	out := string(raw)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

// renderMDC formats the YAML frontmatter + markdown body for a .mdc file.
//
// We use encoding/json to emit the globs array because a JSON array of
// strings (e.g. `["src/**","docs/**"]`) is also valid YAML flow-style array
// syntax, and json.Marshal handles escaping for us.
//
// extraKVs are extension keys (e.g. from `extensions.cursor:` on a scope
// or context primitive) spliced in verbatim after the built-in keys.
func renderMDC(description string, globs []string, alwaysApply bool, body string, extraKVs [][2]string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if description != "" {
		b.WriteString("description: ")
		b.WriteString(description)
		b.WriteString("\n")
	}
	if len(globs) > 0 {
		b.WriteString("globs: ")
		b.WriteString(renderGlobs(globs))
		b.WriteString("\n")
	}
	if alwaysApply {
		b.WriteString("alwaysApply: true\n")
	}
	for _, kv := range extraKVs {
		b.WriteString(kv[0])
		b.WriteString(": ")
		b.WriteString(kv[1])
		b.WriteString("\n")
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

// slugify converts a scope path like "src/billing/api" into a filename-safe
// slug like "src-billing-api". It lowercases the result and replaces path
// separators with dashes.
func slugify(path string) string {
	s := strings.TrimSpace(path)
	s = strings.Trim(s, "/")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.ReplaceAll(s, "/", "-")
	return s
}

// skillSlug normalizes a skill name into a filename-safe slug. It
// lowercases and replaces non-word characters (anything that is not a
// letter, digit, or underscore) with dashes, collapsing runs of dashes
// and trimming leading/trailing dashes.
var skillSlugRE = regexp.MustCompile(`[^\w]+`)

func skillSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = skillSlugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// scopedSkillSlug is the package-shared helper still used by
// continue/copilot/windsurf for scope-prefixed rule/prompt filenames.
// Cursor itself has moved to scopedName (from claude.go) for the same
// purpose.
func scopedSkillSlug(scopePath, name string) string {
	n := skillSlug(name)
	if scopePath == "" {
		return n
	}
	return slugify(scopePath) + "-" + n
}

// renderCursorSkillBody rebuilds a SKILL.md from the canonical Skill
// fields. The parser strips frontmatter from sk.Document.Body into struct
// fields, so we re-emit them as YAML frontmatter for Cursor's discovery.
// Mirrors internal/engine/serialize.go:renderSkillBody (kept inline to
// avoid a plugins → engine import).
//
// v0.9.0 (Phase 2a): additive v2 read — when sk.Globs is empty, fall
// back to sk.Activation.Globs. Emission shape is unchanged.
// extensions.cursor entries (if any) are merged into the frontmatter
// verbatim under their own key/value pairs.
func renderCursorSkillBody(sk *model.Skill) string {
	var b strings.Builder
	globs := sk.Globs
	if len(globs) == 0 {
		globs = sk.Activation.Globs
	}
	extKVs := cursorExtensionKVs(sk.Extensions)
	hasFM := sk.Description != "" || sk.Trigger != "" || len(globs) > 0 || len(extKVs) > 0
	if hasFM {
		b.WriteString("---\n")
		if sk.Description != "" {
			b.WriteString("description: ")
			b.WriteString(renderYAMLScalar(sk.Description))
			b.WriteString("\n")
		}
		if sk.Trigger != "" {
			b.WriteString("trigger: ")
			b.WriteString(renderYAMLScalar(sk.Trigger))
			b.WriteString("\n")
		}
		if len(globs) > 0 {
			b.WriteString("globs: ")
			b.WriteString(renderGlobs(globs))
			b.WriteString("\n")
		}
		for _, kv := range extKVs {
			b.WriteString(kv[0])
			b.WriteString(": ")
			b.WriteString(kv[1])
			b.WriteString("\n")
		}
		b.WriteString("---\n")
	}
	b.WriteString(ensureTrailingNewline(sk.Document.Body))
	return b.String()
}

// cursorExtensionKVs extracts the `cursor:` extension namespace from a
// canonical Extensions map (SPEC §4.x.2) and returns sorted (key, YAML
// scalar) pairs ready to splice into frontmatter. Returns nil when the
// `cursor` key is absent or not a map.
//
// Pass-through is verbatim per Phase 2a: scalars/maps/slices each render
// via JSON-as-YAML (json.Marshal). Sorting keeps output deterministic.
func cursorExtensionKVs(ext map[string]any) [][2]string {
	if len(ext) == 0 {
		return nil
	}
	raw, ok := ext["cursor"]
	if !ok {
		return nil
	}
	asMap, ok := raw.(map[string]any)
	if !ok || len(asMap) == 0 {
		return nil
	}
	keys := make([]string, 0, len(asMap))
	for k := range asMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		val, err := json.Marshal(asMap[k])
		if err != nil {
			continue
		}
		out = append(out, [2]string{k, string(val)})
	}
	return out
}

// cursorCrossEmitActive reports whether the project has both `claude`
// and `cursor` targets active simultaneously — the trigger for the
// cross-emit dedup info warning (SPEC §4.1.4).
//
// Two modes:
//
//   - Explicit: proj.Config.Targets is non-empty and contains both
//     "claude" and "cursor".
//   - Autodetect: proj.Config is nil OR Targets is empty — fall back
//     to checking whether `.claude/` exists under proj.Root (the
//     autodetect signal that Claude is also active). proj.Root is only
//     consulted in autodetect mode so test fixtures with synthetic
//     paths don't get tripped up.
func cursorCrossEmitActive(proj *model.Project) bool {
	if proj == nil {
		return false
	}
	if proj.Config != nil && len(proj.Config.Targets) > 0 {
		var hasClaude, hasCursor bool
		for _, t := range proj.Config.Targets {
			switch t {
			case "claude":
				hasClaude = true
			case "cursor":
				hasCursor = true
			}
		}
		return hasClaude && hasCursor
	}
	// Autodetect: claude is active iff `.claude/` exists under proj.Root.
	if proj.Root == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(proj.Root, ".claude"))
	return err == nil && info.IsDir()
}

// cursorDedupDisabled reports whether the project's
// `extensions.cursor.disable_dedup` key is truthy, in which case the
// cross-emit dedup warning is suppressed.
func cursorDedupDisabled(proj *model.Project) bool {
	if proj == nil || proj.Config == nil || proj.Config.Extensions == nil {
		return false
	}
	raw, ok := proj.Config.Extensions["cursor"]
	if !ok {
		return false
	}
	asMap, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	v, ok := asMap["disable_dedup"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *CursorPlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that CursorPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*CursorPlugin)(nil)
