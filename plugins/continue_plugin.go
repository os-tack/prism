// Package plugins contains the projection plugins that translate a canonical
// .agents/ model into per-tool config files.
//
// ContinuePlugin projects a model.Project into Continue's `.continue/` layout:
//
//   - `.continue/rules/<name>.md` — rule files with YAML frontmatter
//     (`description`, `globs`, `alwaysApply`) followed by a markdown body.
//     Continue uses the frontmatter to decide when to auto-attach a rule.
//   - `.continue/prompts/<name>.md` — invokable prompt files with YAML
//     frontmatter (`name`, `description`, `invokable: true`) followed by a
//     markdown body. Continue surfaces these as `/<name>` slash commands.
//   - `.continue/permissions.yaml` — flat allow/ask/exclude lists at the
//     project root, overriding `~/.continue/permissions.yaml`. Entries are
//     translated from prism's `tool:pattern` form to Continue's
//     `Tool(pattern)` form (e.g. `bash:rm -rf *` → `Bash(rm -rf *)`).
//   - `.continue/mcpServers/<name>.yaml` — one YAML file per MCP server
//     (Continue's per-file mcpServers convention) containing `name`,
//     `command`, `args`, `env`, `url`, `headers` (only the non-empty
//     fields). `${env:VAR}` in any field rewrites to `${{ secrets.VAR }}`.
//   - `.continue/hooks.yaml` — Continue's hook schema is verbatim Claude's
//     (SPEC §4.4) so emission goes through `plugins/hooks_claude_shape.go`
//     rendered as YAML rather than JSON.
//
// As of v0.8, permissions enforce natively via Continue's own permissions
// layer (replacing the prism perms-guard wrapper), and slash commands emit
// natively as prompt files. As of v0.9 / Phase 2.5, hooks emit natively
// via the shared Claude-shape serializer, and `${env:VAR}` references in
// MCP fields rewrite to `${{ secrets.VAR }}` with one info warning per
// affected server. Skills still degrade to scoped rule files (no
// dedicated skill primitive in Continue). Sub-agents stay unsupported.
package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/version"
)

// ContinuePlugin projects Project state into `.continue/rules/*.md`,
// `.continue/prompts/*.md`, `.continue/permissions.yaml`,
// `.continue/mcpServers/*.yaml`, and `.continue/hooks.yaml` files.
type ContinuePlugin struct{}

// NewContinue constructs a ContinuePlugin.
//
// The plugins package hosts multiple plugins; we expose `NewContinue` as the
// canonical constructor (the bare identifier `New` would collide).
func NewContinue() *ContinuePlugin { return &ContinuePlugin{} }

// Name returns the stable plugin identifier.
func (p *ContinuePlugin) Name() string { return "continue" }

// Detect returns true if the project at root contains a `.continue/`
// directory.
func (p *ContinuePlugin) Detect(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".continue"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// Capabilities returns Continue's capability matrix.
//
// Continue natively supports per-glob rule attachment (ScopePaths),
// description-triggered attachment (ScopeSemantic), invokable prompt files
// (Commands), permissions.yaml (Permissions), per-file MCP server
// configuration, and lifecycle hooks (Phase 2.5: schema is verbatim
// Claude's per SPEC §4.4). Skills degrade to scoped rule files (no
// dedicated skill primitive). Agents stay unsupported.
//
// v0.8 coarse cells (Context, ScopePaths, …, MCP) are preserved unchanged
// so existing engine paths keep working. v2 per-field FieldCapabilities
// follow SPEC §12 — Continue column (`con`); fields not listed default to
// FieldNative per the engine fallback.
func (p *ContinuePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		// v0.8 coarse fields — unchanged.
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative, // .continue/prompts/<name>.md with invokable: true
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportNative, // .continue/hooks.yaml (verbatim Claude schema, SPEC §4.4)
		Permissions:   plugin.SupportNative, // .continue/permissions.yaml (project-local override)
		MCP:           plugin.SupportNative,

		// v2 per-field cells (SPEC §12, Continue column). Each block's
		// Extensions list names the plugin's `extensions.<name>:` keyspace.
		AgentFields: plugin.FieldCapabilities{
			Supported: false, // Continue has no subagent primitive.
			Fields: map[string]plugin.FieldSupport{
				"Name":             plugin.FieldDegraded,
				"Description":      plugin.FieldUnsupported,
				"SystemPrompt":     plugin.FieldDegraded,
				"Model":            plugin.FieldDegraded,
				"ModelFallbacks":   plugin.FieldSilent,
				"Tools":            plugin.FieldUnsupported,
				"DisallowedTools":  plugin.FieldUnsupported,
				"ReadOnly":         plugin.FieldUnsupported,
				"Background":       plugin.FieldUnsupported,
				"MaxTurns":         plugin.FieldUnsupported,
				"Temperature":      plugin.FieldDegraded,
				"MCPServers":       plugin.FieldDegraded,
				"AllowedSubagents": plugin.FieldUnsupported,
				"UserInvocable":    plugin.FieldUnsupported,
				"ModelInvocable":   plugin.FieldUnsupported,
				"InitialPrompt":    plugin.FieldUnsupported,
				"ScopePath":        plugin.FieldUnsupported,
			},
			Extensions: []string{p.Name()},
		},
		SkillFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":                      plugin.FieldNative,
				"Description":               plugin.FieldNative,
				"WhenToUse":                 plugin.FieldDegraded,
				"Activation.Modes.Always":   plugin.FieldNative,
				"Activation.Modes.ModelDec": plugin.FieldNative,
				"Activation.Modes.Glob":     plugin.FieldNative,
				"Activation.Modes.Manual":   plugin.FieldNative,
				"Activation.Globs":          plugin.FieldNative,
				"Activation.ContentRegex":   plugin.FieldNative,
				"Activation.UserInvocable":  plugin.FieldSilent,
				"Activation.ModelInvocable": plugin.FieldSilent,
				"AllowedTools":              plugin.FieldUnsupported,
				"Arguments":                 plugin.FieldDegraded,
				"Scripts":                   plugin.FieldUnsupported,
				"References":                plugin.FieldUnsupported,
				"Model":                     plugin.FieldUnsupported,
				"Subagent":                  plugin.FieldUnsupported,
				"ScopePath":                 plugin.FieldUnsupported,
			},
			Extensions: []string{p.Name()},
		},
		CommandFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":         plugin.FieldNative,
				"Description":  plugin.FieldNative,
				"ArgumentHint": plugin.FieldSilent,
				"Arguments":    plugin.FieldDegraded,
				"Model":        plugin.FieldNative,
				"Tools":        plugin.FieldUnsupported,
				"Agent":        plugin.FieldUnsupported,
				"AutoInvoke":   plugin.FieldSilent,
				"ScopePath":    plugin.FieldDegraded,
			},
			Extensions: []string{p.Name()},
		},
		// HookFields: Continue's hooks schema is verbatim Claude's
		// (SPEC §4.4) — emission delegates to
		// `plugins/hooks_claude_shape.go` rendered as YAML at
		// `.continue/hooks.yaml`. Per-field cells follow SPEC §12 Hook
		// table (Continue column `con`).
		HookFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":              plugin.FieldSilent,
				"Description":       plugin.FieldSilent,
				"Event":             plugin.FieldNative,
				"Event.ClaudeOnly":  plugin.FieldNative, // Continue mirrors Claude verbatim
				"Matcher.exact":     plugin.FieldNative,
				"Matcher.regex":     plugin.FieldNative,
				"Handlers.command":  plugin.FieldNative,
				"Handlers.http":     plugin.FieldNative,
				"Handlers.mcp_tool": plugin.FieldUnsupported,
				"Handlers.prompt":   plugin.FieldNative,
				"Handlers.agent":    plugin.FieldNative,
				"Sequential":        plugin.FieldSilent,
				"Disabled":          plugin.FieldNative,
				"TimeoutMs":         plugin.FieldNative,
				"StatusMessage":     plugin.FieldNative,
				"Async":             plugin.FieldNative,
				"FailClosed":        plugin.FieldUnsupported,
				"Once":              plugin.FieldNative,
				"If":                plugin.FieldNative,
				"Cwd":               plugin.FieldSilent,
				"Env":               plugin.FieldUnsupported,
				"BashPowershell":    plugin.FieldUnsupported,
				"ScopePath":         plugin.FieldDegraded,
			},
			Extensions: []string{p.Name()},
		},
		MCPServerFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":               plugin.FieldNative,
				"Transport.stdio":    plugin.FieldNative,
				"Transport.http":     plugin.FieldNative,
				"Transport.sse":      plugin.FieldDegraded,
				"Command":            plugin.FieldNative,
				"Args":               plugin.FieldNative,
				"Env":                plugin.FieldNative,
				"Cwd":                plugin.FieldNative,
				"URL":                plugin.FieldNative,
				"Headers":            plugin.FieldNative,
				"Auth.Scheme.bearer": plugin.FieldNative,
				"Auth.Scheme.header": plugin.FieldNative,
				"Auth.Scheme.oauth":  plugin.FieldDegraded,
				"TimeoutMs":          plugin.FieldUnsupported,
				"Disabled":           plugin.FieldNative,
				"AutoApprove":        plugin.FieldUnsupported,
				"Trust":              plugin.FieldUnsupported,
				"IncludeTools":       plugin.FieldUnsupported,
				"ExcludeTools":       plugin.FieldUnsupported,
				"ScopePath":          plugin.FieldDegraded,
			},
			Extensions: []string{p.Name()},
		},
		PermissionsFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Allow.global":         plugin.FieldNative,
				"Ask.global":           plugin.FieldNative,
				"Deny.global":          plugin.FieldNative, // spelled `exclude` in Continue
				"Allow.scoped":         plugin.FieldDegraded,
				"Ask.scoped":           plugin.FieldDegraded,
				"Deny.scoped":          plugin.FieldDegraded,
				"target.bash":          plugin.FieldNative,
				"target.editReadWrite": plugin.FieldNative,
				"target.fs":            plugin.FieldDegraded,
				"target.network":       plugin.FieldDegraded,
				"target.mcp":           plugin.FieldDegraded,
				"glob.recursive":       plugin.FieldDegraded,
				"glob.negation":        plugin.FieldDegraded,
			},
			Extensions: []string{p.Name()},
		},
		ScopeFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Path.cascade":        plugin.FieldDegraded,
				"Path.empty":          plugin.FieldNative,
				"Name":                plugin.FieldNative,
				"Description":         plugin.FieldNative,
				"Globs":               plugin.FieldNative,
				"Activation.Always":   plugin.FieldNative,
				"Activation.Cascade":  plugin.FieldDegraded,
				"Activation.Glob":     plugin.FieldNative,
				"Activation.Manual":   plugin.FieldUnsupported,
				"Activation.ModelDec": plugin.FieldNative,
				"Priority":            plugin.FieldNative, // lexicographic filename order
				"Tags":                plugin.FieldSilent,
				"IsOverride":          plugin.FieldUnsupported,
			},
			Extensions: []string{p.Name()},
		},
	}
}

// Plan produces the Operations needed to project proj into `.continue/`.
//
// Mode handling: only "" / "write" are accepted (rules always have injected
// frontmatter so a symlink would not be byte-identical to source, and MCP
// YAML is freshly synthesized). Unknown modes return an error.
func (p *ContinuePlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	switch opts.Mode {
	case "", "write":
		// ok
	default:
		return nil, fmt.Errorf("continue: unsupported mode %q", opts.Mode)
	}

	var ops []plugin.Operation

	// Root context → .continue/rules/_root.md with alwaysApply: true.
	if proj.Context != nil {
		// Root context has no Extensions slot on Document; pass nil.
		content := renderContinueRule("Project-wide context", nil, true, proj.Context.Body, nil)
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".continue/rules/_root.md",
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		})
	}

	// Per-scope rule files.
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
		// extensions.continue.* passthrough (SPEC §5.1, §12 Extensions=N).
		extra := continueExtensions(sc.Extensions)
		// Scope.Name and Scope.Priority are surfaced as frontmatter keys
		// (SPEC §12 con column: Name=N, Priority=N⁷ lexicographic
		// filename order; we keep both the frontmatter key and the slug
		// sort so users can inspect priority without parsing filenames).
		content := renderContinueScopeRule(sc, desc, body, extra)
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "rules", slugify(sc.Path)+".md")),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		// Scope.IsOverride is unsupported (SPEC §12 con column: U⁹).
		// Continue has no equivalent semantic; surface a warn-level
		// warning so users know the cell drops.
		if sc.IsOverride {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("Continue has no scope-override semantic; IsOverride dropped on scope %q", sc.Path),
				Severity: "warn",
			})
		}
		ops = append(ops, op)
	}

	// Skills → degraded scoped rule files at .continue/rules/skill-<slug>.md.
	// Scoped skills include the scope slug as a filename prefix so same-named
	// skills across scopes do not collide. Skill.Globs is already populated by
	// the parser (frontmatter override or scope default) so we just pass it
	// through.
	for _, skill := range proj.Skills {
		if skill == nil {
			continue
		}
		desc := skill.Description
		if desc == "" {
			desc = skill.Trigger
		}
		body := ""
		var sources []string
		if skill.Document != nil {
			body = skill.Document.Body
			sources = []string{proj.SourceTag(skill.Document.SourcePath)}
		}
		// v2 additive read: fall back to Activation.Globs when the v0.8
		// flat Globs is empty (SPEC §4.2.2). The parser populates both,
		// but downstream test fixtures may only set one form; reading
		// either preserves backwards compatibility.
		globs := skill.Globs
		if len(globs) == 0 {
			globs = skill.Activation.Globs
		}
		// extensions.continue.* passthrough.
		extra := continueExtensions(skill.Extensions)
		content := renderContinueRule(desc, globs, false, body, extra)
		fname := "skill-" + scopedSkillSlug(skill.ScopePath, skill.Name) + ".md"
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "rules", fname)),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if len(skill.Scripts) > 0 {
			src := ""
			if skill.Document != nil {
				src = proj.SourceTag(skill.Document.SourcePath)
			}
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Continue has no script execution; scripts ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		op.Warnings = append(op.Warnings, continueSkillWarnings(proj, skill)...)
		ops = append(ops, op)
	}

	// Commands → .continue/prompts/<slug>.md (invokable prompt files).
	//
	// Each command becomes a prompt file with YAML frontmatter (`name`,
	// `description`, `invokable: true`) and the command body as the prompt
	// body. Scoped commands get the scope slug prefixed in the filename and
	// a `(scope: ...)` note in the description (Continue's prompt files have
	// no per-scope attachment mechanism).
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
		desc := cmd.Description
		if cmd.ScopePath != "" {
			if desc == "" {
				desc = fmt.Sprintf("(scope: %s)", cmd.ScopePath)
			} else {
				desc = fmt.Sprintf("%s (scope: %s)", desc, cmd.ScopePath)
			}
		}
		var fname string
		if cmd.ScopePath != "" {
			fname = scopedSkillSlug(cmd.ScopePath, cmd.Name) + ".md"
		} else {
			fname = skillSlug(cmd.Name) + ".md"
		}
		// extensions.continue.* passthrough.
		extra := continueExtensions(cmd.Extensions)
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "prompts", fname)),
			Content: renderContinuePrompt(cmd.Name, desc, cmd.Model, body, extra),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		src := ""
		if len(sources) > 0 {
			src = sources[0]
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Continue prompt files have no per-scope attachment; ScopePath on scoped command %q (scope: %s) projected as a global slash command", cmd.Name, cmd.ScopePath),
				Severity: "info",
			})
		}
		// Per-field cells (SPEC §12 con Command column).
		// Arguments=D — Continue prompt files have no structured arg
		// schema; arg names are surfaced inside the body only.
		if len(cmd.Arguments) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Continue prompts have no structured argument schema; Arguments on command %q surfaced verbatim in the prompt body", cmd.Name),
				Severity: "info",
			})
		}
		// Tools=U — Continue prompts cannot scope tool access per-prompt.
		if len(cmd.Tools) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Continue prompts cannot restrict Tools per-command; dropped on %q", cmd.Name),
				Severity: "warn",
			})
		}
		// Agent=U — Continue has no subagent primitive; the Agent
		// pointer on a Command cannot be honored.
		if cmd.Agent != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Continue has no subagent primitive; Agent reference %q on command %q dropped", cmd.Agent, cmd.Name),
				Severity: "warn",
			})
		}
		ops = append(ops, op)
	}

	// Capability degradation/unsupported warnings without a host op of their
	// own. These are attached to the first emitted op (if any).
	var warnings []plugin.Warning
	for _, ag := range proj.Agents {
		if ag == nil {
			continue
		}
		warnings = append(warnings, continueAgentWarnings(proj, ag)...)
	}

	// Hooks → .continue/hooks.yaml (verbatim Claude schema, YAML form).
	// Continue's hook contract is a literal copy of Claude's per SPEC §4.4,
	// so we route through plugins/hooks_claude_shape.go and render the
	// resulting groups as YAML.
	hookOp, hookWarnings, err := buildContinueHooksOp(p, proj)
	if err != nil {
		return nil, err
	}
	if hookOp != nil {
		ops = append(ops, *hookOp)
	}
	warnings = append(warnings, hookWarnings...)

	// Native permissions → .continue/permissions.yaml.
	//
	// Continue's permissions schema is a flat three-key map (allow / ask /
	// exclude) with `Tool(pattern)` entries. Prism's global block and any
	// ScopedPermissions are merged into the same file: Continue has no
	// per-scope permission attachment, so each scoped block gets an info
	// warning when it's folded in. Patterns that don't translate cleanly
	// (mid-string `*`, `?`, character classes) get a deprecation warning.
	permsOp, permsWarnings, err := buildContinuePermissionsOp(p, proj)
	if err != nil {
		return nil, err
	}
	if permsOp != nil {
		ops = append(ops, *permsOp)
	}
	warnings = append(warnings, permsWarnings...)

	// MCP servers → one .continue/mcpServers/<slug>.yaml per server.
	// Scoped MCP servers project to the same global file set with an info
	// warning per server (Continue has no per-scope MCP). One pass emits both
	// the op and the warning (was two passes — collapsed in v0.8.1).
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		op, mcpWarnings, err := buildContinueMCPOp(p, srv)
		if err != nil {
			return nil, err
		}
		op.Warnings = append(op.Warnings, mcpWarnings...)
		if srv.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("Continue has no per-scope MCP; ScopePath on server %q (scope: %s) merged into global block", srv.Name, srv.ScopePath),
				Severity: "info",
			})
		}
		// Per-field unsupported cells (SPEC §12 con MCPServer column):
		// TimeoutMs, AutoApprove, Trust, IncludeTools, ExcludeTools.
		// Continue's mcpServers schema has no analogues, so we emit one
		// warn-level Warning per populated field.
		if srv.TimeoutMs > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("Continue mcpServers has no TimeoutMs key; %dms dropped on server %q", srv.TimeoutMs, srv.Name),
				Severity: "warn",
			})
		}
		if len(srv.AutoApprove) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("Continue mcpServers has no AutoApprove key; %d entries dropped on server %q", len(srv.AutoApprove), srv.Name),
				Severity: "warn",
			})
		}
		if srv.Trust {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("Continue mcpServers has no Trust key; flag dropped on server %q", srv.Name),
				Severity: "warn",
			})
		}
		if len(srv.IncludeTools) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("Continue mcpServers has no IncludeTools allowlist; %d entries dropped on server %q", len(srv.IncludeTools), srv.Name),
				Severity: "warn",
			})
		}
		if len(srv.ExcludeTools) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("Continue mcpServers has no ExcludeTools denylist; %d entries dropped on server %q", len(srv.ExcludeTools), srv.Name),
				Severity: "warn",
			})
		}
		ops = append(ops, op)
	}

	// Attach orphan warnings to the first available op. When the plan
	// would otherwise be empty (only unsupported primitives like
	// stand-alone Agents in the input), emit a synthetic no-op operation
	// with an empty path so the warnings still reach the engine. The
	// engine treats empty-path ops as warning-only; the filesystem stays
	// untouched.
	if len(warnings) > 0 && len(ops) == 0 {
		ops = append(ops, plugin.Operation{
			Kind:   plugin.OpWrite,
			Path:   "",
			Mode:   plugin.ModeWrite,
			Plugin: p.Name(),
		})
	}
	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// buildContinueHooksOp builds the `.continue/hooks.yaml` op (or returns
// nil if no hooks are set). Continue's hook schema is verbatim Claude's
// per SPEC §4.4, so emission routes through ClaudeShapeHooks. The shared
// helper returns map[string][]ClaudeHookGroup keyed by event; we render
// it as YAML rather than JSON since Continue's hook config file is YAML.
//
// Hooks whose canonical event has no Continue mapping are dropped with a
// per-hook info warning (rare — Continue's mapping table tracks every
// canonical event Claude supports). Scoped hooks fold into the same flat
// file with a per-hook merge warning (Continue has no per-scope hooks).
func buildContinueHooksOp(p *ContinuePlugin, proj *model.Project) (*plugin.Operation, []plugin.Warning, error) {
	if len(proj.Hooks) == 0 {
		return nil, nil, nil
	}

	var warnings []plugin.Warning
	var sources []string
	for _, h := range proj.Hooks {
		if h == nil || h.Disabled {
			continue
		}
		// Probe the resolver: if the event has no mapping, emit a
		// per-hook info warning. ClaudeShapeHooks itself silently
		// drops unmappable events; we shadow the check here for the
		// warning surface.
		ev, _ := resolveClaudeEvent(h, "continue")
		if ev == "" {
			eventName := h.Event
			if eventName == "" {
				eventName = string(h.EventCanonical)
			}
			label := h.Name
			if label == "" {
				label = fmt.Sprintf("%s:%s", eventName, h.Matcher)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   h.ScriptPath,
				Message:  fmt.Sprintf("Continue has no native expression for hook event %q; %s not projected", eventName, label),
				Severity: "info",
			})
			continue
		}
		if h.ScriptPath != "" {
			sources = append(sources, h.ScriptPath)
		}
		if h.ScopePath != "" {
			label := h.Name
			if label == "" {
				label = fmt.Sprintf("%s:%s", ev, h.Matcher)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   h.ScriptPath,
				Message:  fmt.Sprintf("Continue has no per-scope hooks; ScopePath on hook %s (scope: %s) merged into .continue/hooks.yaml", label, h.ScopePath),
				Severity: "info",
			})
		}
		// Per-handler field-level unsupported warnings (SPEC §12 Continue
		// column: FailClosed, Env, BashPowershell, mcp_tool/agent handler
		// kinds are unsupported on Continue). The shared ClaudeShapeHooks
		// serializer drops these silently; we surface one warn-level
		// Warning per affected handler so users know the wire form omits
		// the field.
		for _, hd := range h.Handlers {
			if hd.FailClosed {
				warnings = append(warnings, plugin.Warning{
					Source:   h.ScriptPath,
					Message:  fmt.Sprintf("Continue: hook handler FailClosed unsupported; dropped (event %q)", ev),
					Severity: "warn",
				})
			}
			if len(hd.Env) > 0 {
				warnings = append(warnings, plugin.Warning{
					Source:   h.ScriptPath,
					Message:  fmt.Sprintf("Continue: hook handler Env unsupported; %d entries dropped (event %q)", len(hd.Env), ev),
					Severity: "warn",
				})
			}
			if hd.Bash != "" || hd.Powershell != "" {
				warnings = append(warnings, plugin.Warning{
					Source:   h.ScriptPath,
					Message:  fmt.Sprintf("Continue: hook handler Bash/Powershell platform-override script keys unsupported (event %q)", ev),
					Severity: "warn",
				})
			}
			switch hd.Kind {
			case model.HookHandlerMCPTool:
				warnings = append(warnings, plugin.Warning{
					Source:   h.ScriptPath,
					Message:  fmt.Sprintf("Continue: hook handler kind mcp_tool unsupported; dropped (event %q)", ev),
					Severity: "warn",
				})
			}
		}
	}

	groups := ClaudeShapeHooks(proj.Hooks, "continue")
	if len(groups) == 0 {
		// Every hook was dropped (disabled or unmappable). Warnings
		// already emitted above; no file op.
		return nil, warnings, nil
	}

	content, err := renderClaudeShapeYAML(groups)
	if err != nil {
		return nil, warnings, fmt.Errorf("continue: marshal hooks: %w", err)
	}

	if len(sources) == 0 {
		sources = []string{"hooks.yaml"}
	}

	op := &plugin.Operation{
		Kind:    plugin.OpWrite,
		Path:    ".continue/hooks.yaml",
		Content: content,
		Mode:    plugin.ModeWrite,
		Plugin:  p.Name(),
		Sources: sources,
	}
	return op, warnings, nil
}

// renderClaudeShapeYAML mirrors RenderClaudeShapeJSON but emits YAML.
// Continue's hooks config file is `.continue/hooks.yaml` — the same shape
// Claude carries under settings.json's `hooks:` key, just YAML syntax.
//
// Keys are emitted in sorted order for deterministic output. Within each
// event the group order follows the slice order returned by
// ClaudeShapeHooks (already sorted by matcher).
func renderClaudeShapeYAML(groups map[string][]ClaudeHookGroup) (string, error) {
	if len(groups) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, ev := range keys {
		evSeq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, g := range groups[ev] {
			gNode := &yaml.Node{Kind: yaml.MappingNode}
			if g.Matcher != "" {
				gNode.Content = append(gNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "matcher"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: g.Matcher},
				)
			}
			hooksSeq := &yaml.Node{Kind: yaml.SequenceNode}
			for _, h := range g.Hooks {
				hNode := &yaml.Node{Kind: yaml.MappingNode}
				if h.Type != "" {
					hNode.Content = append(hNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: h.Type},
					)
				}
				if h.Command != "" {
					hNode.Content = append(hNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "command"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: h.Command},
					)
				}
				if h.Timeout > 0 {
					hNode.Content = append(hNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "timeout"},
						&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", h.Timeout)},
					)
				}
				if h.URL != "" {
					hNode.Content = append(hNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "url"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: h.URL},
					)
				}
				if h.Prompt != "" {
					hNode.Content = append(hNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "prompt"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: h.Prompt},
					)
				}
				hooksSeq.Content = append(hooksSeq.Content, hNode)
			}
			gNode.Content = append(gNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "hooks"},
				hooksSeq,
			)
			evSeq.Content = append(evSeq.Content, gNode)
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: ev},
			evSeq,
		)
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// buildContinuePermissionsOp builds the .continue/permissions.yaml op (or
// returns nil if no permissions are set). It also returns any deprecation
// warnings for rules that don't translate cleanly into Continue's format,
// plus info warnings for scoped blocks that get merged into the flat file.
func buildContinuePermissionsOp(p *ContinuePlugin, proj *model.Project) (*plugin.Operation, []plugin.Warning, error) {
	hasGlobal := proj.Permissions != nil && (len(proj.Permissions.Allow)+len(proj.Permissions.Deny)+len(proj.Permissions.Ask) > 0)
	var scopedNonEmpty []*model.Permissions
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		if len(sp.Allow)+len(sp.Deny)+len(sp.Ask) == 0 {
			continue
		}
		scopedNonEmpty = append(scopedNonEmpty, sp)
	}
	if !hasGlobal && len(scopedNonEmpty) == 0 {
		return nil, nil, nil
	}

	var warnings []plugin.Warning
	var allow, ask, exclude []string
	sources := []string{}

	// translateRule converts a prism `tool:pattern` rule into a Continue
	// `Tool(pattern)` rule. Tool names are PascalCased (bash → Bash,
	// edit → Edit, etc.). Rules with mid-string wildcards beyond a single
	// trailing `*` produce a deprecation warning since Continue's pattern
	// matcher uses its own glob dialect.
	translate := func(rule, sourceTag string) (string, []plugin.Warning) {
		var local []plugin.Warning
		rule = strings.TrimSpace(rule)
		if rule == "" {
			return "", nil
		}
		idx := strings.Index(rule, ":")
		var tool, pattern string
		if idx < 0 {
			tool = rule
			pattern = ""
		} else {
			tool = rule[:idx]
			pattern = rule[idx+1:]
		}
		tool = pascalToolName(tool)
		// Detect potentially-lossy patterns: anything with a `?`,
		// character class, or interior `*` that isn't a clean trailing
		// wildcard. Continue accepts globbing but its dialect differs
		// from prism's prefix-only matching; warn so users review.
		if pattern != "" && hasUntranslatableGlob(pattern) {
			local = append(local, plugin.Warning{
				Source:   sourceTag,
				Message:  fmt.Sprintf("permission rule %q uses a glob pattern that may not translate cleanly to Continue's permissions.yaml format", rule),
				Severity: "info",
			})
		}
		if pattern == "" {
			return tool, local
		}
		return tool + "(" + pattern + ")", local
	}

	addBlock := func(perms *model.Permissions, sourceTag string) {
		for _, r := range perms.Allow {
			t, ws := translate(r, sourceTag)
			warnings = append(warnings, ws...)
			if t != "" {
				allow = append(allow, t)
			}
		}
		for _, r := range perms.Ask {
			t, ws := translate(r, sourceTag)
			warnings = append(warnings, ws...)
			if t != "" {
				ask = append(ask, t)
			}
		}
		for _, r := range perms.Deny {
			t, ws := translate(r, sourceTag)
			warnings = append(warnings, ws...)
			if t != "" {
				exclude = append(exclude, t)
			}
		}
	}

	if hasGlobal {
		addBlock(proj.Permissions, "permissions.yaml")
		sources = append(sources, "permissions.yaml")
	}
	for _, sp := range scopedNonEmpty {
		tag := filepath.ToSlash(filepath.Join(sp.ScopePath, "permissions.yaml"))
		addBlock(sp, tag)
		sources = append(sources, tag)
		warnings = append(warnings, plugin.Warning{
			Source:   tag,
			Message:  fmt.Sprintf("Continue has no per-scope permissions; scoped permissions for %q merged into .continue/permissions.yaml", sp.ScopePath),
			Severity: "info",
		})
	}

	// Render YAML with stable key order: allow, ask, exclude. Each list is
	// emitted even when empty so consumers see the canonical shape (Continue
	// itself accepts `exclude: []`).
	root := &yaml.Node{Kind: yaml.MappingNode}
	appendSeq := func(key string, items []string) {
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		seq.Style = yaml.FlowStyle
		if len(items) > 0 {
			seq.Style = 0 // block style when populated, flow when empty
		}
		for _, it := range items {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: it})
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			seq,
		)
	}
	appendSeq("allow", allow)
	appendSeq("ask", ask)
	appendSeq("exclude", exclude)

	raw, err := yaml.Marshal(root)
	if err != nil {
		return nil, nil, fmt.Errorf("continue: marshal permissions: %w", err)
	}

	op := &plugin.Operation{
		Kind:    plugin.OpWrite,
		Path:    ".continue/permissions.yaml",
		Content: string(raw),
		Mode:    plugin.ModeWrite,
		Plugin:  p.Name(),
		Sources: sources,
	}
	return op, warnings, nil
}

// pascalToolName converts a prism tool token (`bash`, `edit`, `multiEdit`)
// into Continue's PascalCase convention (`Bash`, `Edit`, `MultiEdit`). The
// rules engine in prism is case-insensitive so the canonical form on disk
// can differ; Continue's matcher is also tolerant but the docs use
// PascalCase exclusively.
func pascalToolName(tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return ""
	}
	// Known short forms map directly. Anything else gets a simple
	// uppercase-first-letter rewrite.
	switch strings.ToLower(tool) {
	case "bash":
		return "Bash"
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "edit":
		return "Edit"
	case "multiedit":
		return "MultiEdit"
	case "list":
		return "List"
	case "search":
		return "Search"
	case "fetch":
		return "Fetch"
	case "diff":
		return "Diff"
	case "askquestion":
		return "AskQuestion"
	case "checklist":
		return "Checklist"
	case "status":
		return "Status"
	}
	// Fallback: uppercase the first ASCII letter, keep the rest.
	r := []rune(tool)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
}

// hasUntranslatableGlob reports whether a prism permission pattern uses
// glob features that don't map 1:1 onto Continue's pattern matcher. Prism's
// matcher only supports a single trailing `*`; anything richer needs a
// deprecation warning so users review.
func hasUntranslatableGlob(pattern string) bool {
	if strings.ContainsAny(pattern, "?[]") {
		return true
	}
	// More than one `*`, or `*` not at the end.
	if idx := strings.Index(pattern, "*"); idx >= 0 {
		if idx != len(pattern)-1 {
			return true
		}
		// Trailing-only `*` is fine. Check there's no other `*` (cheap
		// double-check on the byte slice).
		if strings.Count(pattern, "*") > 1 {
			return true
		}
	}
	return false
}

// renderContinuePrompt formats a `.continue/prompts/<name>.md` file with
// YAML frontmatter (`name`, `description`, `model`, `invokable: true`)
// followed by the prompt body. The encoding/json shim is used to escape
// the description and name strings safely (JSON strings are valid YAML
// scalars, including the colon-in-description case).
//
// model is the optional model override (SPEC §12 con Command/Model = N).
// extra is the verbatim `extensions.continue.*` map (typically nil); when
// non-empty its keys are appended to the frontmatter after the canonical
// keys. Plugin-namespaced pass-through per SPEC §5.1.
func renderContinuePrompt(name, description, model, body string, extra map[string]any) string {
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
	if model != "" {
		b.WriteString("model: ")
		b.WriteString(renderYAMLScalar(model))
		b.WriteString("\n")
	}
	b.WriteString("invokable: true\n")
	writeExtensionsYAML(&b, extra)
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// continueEnvVarRegex matches the canonical `${env:VAR}` substitution form
// (SPEC §4.5.3). Variable names follow shell convention: letter or
// underscore followed by alphanumerics/underscores.
var continueEnvVarRegex = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// rewriteContinueEnvVar replaces every `${env:VAR}` occurrence in s with
// Continue's secrets-store syntax `${{ secrets.VAR }}`. Returns the new
// string plus true if any rewrite happened (used by the caller to decide
// whether to emit the secrets-store info warning).
func rewriteContinueEnvVar(s string) (string, bool) {
	if !strings.Contains(s, "${env:") {
		return s, false
	}
	out := continueEnvVarRegex.ReplaceAllString(s, "${{ secrets.$1 }}")
	return out, out != s
}

// buildContinueMCPOp constructs the OpWrite for a single MCP server file at
// `.continue/mcpServers/<slug>.yaml`. Only non-empty fields from srv are
// emitted. URL-only servers omit command/args/env.
//
// `${env:VAR}` references in any string field (command, args, env values,
// url, headers) rewrite to Continue's secrets-store form
// `${{ secrets.VAR }}` per SPEC §4.5.3. When at least one rewrite fires
// for a given server, the returned warnings include one info-level entry
// pointing the user at the Continue secrets store. SSE transport on a
// YAML mcpServers file emits the SPEC §4.5.4 known-bug warning
// (continuedev/continue#5359).
func buildContinueMCPOp(p *ContinuePlugin, srv *model.MCPServer) (plugin.Operation, []plugin.Warning, error) {
	var warnings []plugin.Warning
	var rewrote bool

	// Apply ${env:VAR} → ${{ secrets.VAR }} rewrite to every string
	// field. We deep-copy the slices/maps so the canonical model
	// (read by other plugins in the same Plan() pass) stays untouched.
	rewrite := func(s string) string {
		out, did := rewriteContinueEnvVar(s)
		if did {
			rewrote = true
		}
		return out
	}

	cmd := rewrite(srv.Command)
	url := rewrite(srv.URL)
	args := make([]string, len(srv.Args))
	for i, a := range srv.Args {
		args[i] = rewrite(a)
	}
	env := make(map[string]string, len(srv.Env))
	for k, v := range srv.Env {
		env[k] = rewrite(v)
	}
	headers := make(map[string]string, len(srv.Headers))
	for k, v := range srv.Headers {
		headers[k] = rewrite(v)
	}

	// Use yaml.Node so we control key order deterministically and emit only
	// non-empty fields. A plain map[string]any would still serialize but
	// yaml.v3 sorts map keys non-alphabetically; a node tree keeps the order
	// stable: name, command, args, env, url, headers.
	root := &yaml.Node{Kind: yaml.MappingNode}
	addStr := func(key, val string) {
		if val == "" {
			return
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: val},
		)
	}
	addStr("name", srv.Name)
	addStr("command", cmd)
	if len(args) > 0 {
		argsNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, a := range args {
			argsNode.Content = append(argsNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: a})
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "args"},
			argsNode,
		)
	}
	if len(env) > 0 {
		envNode := &yaml.Node{Kind: yaml.MappingNode}
		// Sort env keys for deterministic output.
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			envNode.Content = append(envNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Value: env[k]},
			)
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "env"},
			envNode,
		)
	}
	addStr("url", url)
	if len(headers) > 0 {
		hdrNode := &yaml.Node{Kind: yaml.MappingNode}
		keys := make([]string, 0, len(headers))
		for k := range headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			hdrNode.Content = append(hdrNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Value: headers[k]},
			)
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "headers"},
			hdrNode,
		)
	}

	raw, err := yaml.Marshal(root)
	if err != nil {
		return plugin.Operation{}, nil, fmt.Errorf("continue: marshal mcp server %q: %w", srv.Name, err)
	}

	if rewrote {
		warnings = append(warnings, plugin.Warning{
			Source:   "mcp.yaml",
			Message:  fmt.Sprintf("Continue: ${env:VAR} rewritten to ${{ secrets.VAR }} for server %q; value must live in Continue secrets store, not just shell env.", srv.Name),
			Severity: "info",
		})
	}
	// SSE on YAML config: known upstream bug per SPEC §4.5.4 (#5359).
	if strings.EqualFold(srv.Transport, "sse") {
		warnings = append(warnings, plugin.Warning{
			Source:   "mcp.yaml",
			Message:  fmt.Sprintf("Continue: SSE transport on YAML mcpServers config is not fully supported (continuedev/continue#5359); server %q may not connect.", srv.Name),
			Severity: "info",
		})
	}

	op := plugin.Operation{
		Kind:    plugin.OpWrite,
		Path:    filepath.ToSlash(filepath.Join(".continue", "mcpServers", skillSlug(srv.Name)+".yaml")),
		Content: string(raw),
		Mode:    plugin.ModeWrite,
		Plugin:  p.Name(),
		Sources: []string{"mcp.yaml"},
	}
	return op, warnings, nil
}

// renderContinueRule formats the YAML frontmatter + markdown body for a
// `.continue/rules/<name>.md` file. Empty values are omitted.
//
// The globs field is rendered via encoding/json — a JSON array of strings is
// also valid YAML flow-array syntax, and json.Marshal handles escaping.
//
// extra is the verbatim `extensions.continue.*` map (typically nil); when
// non-empty its keys are appended to the frontmatter after the canonical
// keys. Plugin-namespaced pass-through per SPEC §5.1.
func renderContinueRule(description string, globs []string, alwaysApply bool, body string, extra map[string]any) string {
	var b strings.Builder
	b.WriteString("---\n")
	if description != "" {
		b.WriteString("description: ")
		b.WriteString(renderYAMLScalar(description))
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
	writeExtensionsYAML(&b, extra)
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderContinueScopeRule formats a scope rule file with extended
// frontmatter that includes the v2 Scope.Name and Scope.Priority cells
// (SPEC §12 con column: Name=N, Priority=N⁷). The Continue rule loader
// ignores unknown keys, so emitting `name:` and `priority:` is safe even
// though Continue's stock attachment logic only reads description/globs.
func renderContinueScopeRule(sc *model.Scope, description, body string, extra map[string]any) string {
	var b strings.Builder
	b.WriteString("---\n")
	if sc.Name != "" {
		b.WriteString("name: ")
		b.WriteString(renderYAMLScalar(sc.Name))
		b.WriteString("\n")
	}
	if description != "" {
		b.WriteString("description: ")
		b.WriteString(renderYAMLScalar(description))
		b.WriteString("\n")
	}
	if len(sc.Globs) > 0 {
		b.WriteString("globs: ")
		b.WriteString(renderGlobs(sc.Globs))
		b.WriteString("\n")
	}
	if sc.Priority != "" {
		b.WriteString("priority: ")
		b.WriteString(renderYAMLScalar(string(sc.Priority)))
		b.WriteString("\n")
	}
	writeExtensionsYAML(&b, extra)
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// continueAgentWarnings emits one Warning per populated Agent field per
// SPEC §12 con Agent column (D = info, U = warn, S = silent). The whole
// Agent primitive drops on Continue (no subagent host), so each warning
// is the user's only signal that a specific field went missing. The
// "no subagent primitive" preamble surfaces the high-level reason; the
// per-field suffix names the exact cell so the contract test sees the
// field name in the message.
//
// A baseline drop warning is always emitted (one per agent) so users
// see the agent went missing even when no v2 fields are populated.
func continueAgentWarnings(proj *model.Project, ag *model.Agent) []plugin.Warning {
	if ag == nil {
		return nil
	}
	src := ""
	if ag.Document != nil {
		src = proj.SourceTag(ag.Document.SourcePath)
	}
	prefix := fmt.Sprintf("Continue has no subagent primitive; agent %q not projected", ag.Name)
	if ag.ScopePath != "" {
		prefix = fmt.Sprintf("Continue has no subagent primitive; scoped agent %q (scope: %s) not projected", ag.Name, ag.ScopePath)
	}
	out := []plugin.Warning{
		{
			Source:   src,
			Message:  prefix,
			Severity: "info",
		},
	}
	info := func(field, detail string) {
		out = append(out, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("%s; %s degraded (%s)", prefix, field, detail),
			Severity: "info",
		})
	}
	warn := func(field, detail string) {
		out = append(out, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("%s; %s dropped (%s)", prefix, field, detail),
			Severity: "warn",
		})
	}
	// Degraded fields (SPEC §12 con: D).
	if ag.SystemPrompt != "" {
		info("SystemPrompt", "no native agent host; SystemPrompt surfaces only inside the agents.md fallback")
	}
	if ag.Model != "" {
		info("Model", "no native agent host; Model is advisory only")
	}
	if ag.Temperature != nil {
		info("Temperature", "no native agent host; Temperature is advisory only")
	}
	if len(ag.MCPServers) > 0 {
		info("MCPServers", "no per-agent MCP attachment; servers must be configured globally")
	}
	// Unsupported fields (SPEC §12 con: U).
	if len(ag.Tools) > 0 {
		warn("Tools", "no per-agent tool allowlist")
	}
	if len(ag.DisallowedTools) > 0 {
		warn("DisallowedTools", "no per-agent tool denylist")
	}
	if ag.ReadOnly != nil {
		warn("ReadOnly", "no per-agent read-only flag")
	}
	if ag.Background != nil {
		warn("Background", "no background-agent mode")
	}
	if ag.MaxTurns != nil {
		warn("MaxTurns", "no MaxTurns control")
	}
	if len(ag.AllowedSubagents) > 0 {
		warn("AllowedSubagents", "no nested subagent control")
	}
	if ag.UserInvocable != nil {
		warn("UserInvocable", "no user-invocation control")
	}
	if ag.ModelInvocable != nil {
		warn("ModelInvocable", "no model-invocation control")
	}
	if ag.InitialPrompt != "" {
		warn("InitialPrompt", "no InitialPrompt seed")
	}
	if ag.ScopePath != "" {
		warn("ScopePath", "no per-scope agent attachment")
	}
	return out
}

// continueSkillWarnings emits one Warning per populated Skill field per
// SPEC §12 con Skill column. Most skill fields are degraded (D) because
// the skill itself emits as a rule file — fields that can't ride along
// in YAML frontmatter drop with an info warning. Unsupported (U) fields
// drop with a warn-level warning. The skill name is included for source
// localization.
func continueSkillWarnings(proj *model.Project, skill *model.Skill) []plugin.Warning {
	if skill == nil {
		return nil
	}
	src := ""
	if skill.Document != nil {
		src = proj.SourceTag(skill.Document.SourcePath)
	}
	var out []plugin.Warning
	info := func(field, detail string) {
		out = append(out, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("Continue degrades %s on skill %q to a rule file (%s)", field, skill.Name, detail),
			Severity: "info",
		})
	}
	warn := func(field, detail string) {
		out = append(out, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("Continue drops %s on skill %q (%s)", field, skill.Name, detail),
			Severity: "warn",
		})
	}
	// Degraded (D).
	if skill.WhenToUse != "" {
		info("WhenToUse", "Continue rules have no WhenToUse field; merged into description")
	}
	if len(skill.Arguments) > 0 {
		info("Arguments", "Continue rules have no structured Arguments schema")
	}
	// Unsupported (U).
	if len(skill.AllowedTools) > 0 {
		warn("AllowedTools", "no per-skill AllowedTools allowlist")
	}
	if len(skill.References) > 0 {
		warn("References", "no References mechanism on rule files")
	}
	if skill.Model != "" {
		warn("Model", "no per-skill Model override")
	}
	if skill.Subagent != "" {
		warn("Subagent", "no Subagent dispatch from skills")
	}
	if skill.ScopePath != "" {
		warn("ScopePath", "Continue rule attachment is glob-based, not scope-path-based")
	}
	return out
}

// continueExtensions extracts the `extensions.continue` block from a
// primitive's Extensions map (SPEC §5.1). Returns nil when absent or
// when the value isn't a map. Other extension namespaces are dropped
// silently — they belong to other plugins.
func continueExtensions(ext map[string]any) map[string]any {
	if ext == nil {
		return nil
	}
	raw, ok := ext["continue"]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		// `extensions.continue:` was provided but isn't a map; treat as
		// nothing-to-passthrough rather than crashing the emit.
		return nil
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// writeExtensionsYAML appends extension pass-through keys to a YAML
// frontmatter builder. Keys are emitted in sorted order for deterministic
// output. Values are marshaled via yaml.v3 so nested maps, lists, and
// scalars all round-trip without manual escaping.
//
// Extension blobs round-trip verbatim — the `${env:VAR}` rewrite is
// applied to MCP fields (command/args/env/url/headers) at emit, not to
// rule frontmatter passthrough. Plugin-namespaced extensions are an
// escape hatch for verbatim user input per SPEC §5.1.
func writeExtensionsYAML(b *strings.Builder, extra map[string]any) {
	if len(extra) == 0 {
		return
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Marshal the single key→value pair so yaml.v3 handles quoting,
		// indentation, and nested types. Trim the trailing newline so
		// our own "\n" terminator is uniform.
		out, err := yaml.Marshal(map[string]any{k: extra[k]})
		if err != nil {
			// Defensive: skip malformed values rather than failing the
			// whole emit. Should never trigger for parser-produced maps.
			continue
		}
		b.Write(out)
	}
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *ContinuePlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that ContinuePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ContinuePlugin)(nil)
