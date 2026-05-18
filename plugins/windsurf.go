// WindsurfPlugin projects a canonical .agents/ directory into Windsurf's
// `.windsurf/rules/*.md` rule files. Each rule file uses YAML frontmatter
// followed by a markdown body. Windsurf's frontmatter has a `trigger` field
// controlling when the rule fires:
//
//   - always_on:       rule is always included
//   - glob:            rule attaches when files matching `globs` are in context
//   - model_decision:  rule attaches when the model thinks `description` is relevant
//   - manual:          rule only fires when explicitly invoked
//
// In addition to rules, the plugin now emits Windsurf's two file-based
// extension surfaces:
//
//   - `.windsurf/hooks.json` — Cascade Hooks (12 event types, JSON-stdin
//     contract similar to Claude Code's hook protocol). Pre-hooks can block
//     the action by exiting with code 2.
//
//   - `.windsurf/mcp_config.json` — MCP server registry, standard
//     {mcpServers: {...}} schema. Windsurf canonically expects this file
//     at the user-level path `~/.codeium/windsurf/mcp_config.json`; the
//     project-local copy is emitted for portability with an info warning
//     that the user must symlink / shell-init the file into the canonical
//     location for Windsurf to pick it up.
//
// Skills and Commands degrade to rules (with a description so the model
// can decide when to surface them); Agents and Permissions are still
// unsupported (Windsurf has no equivalent primitives). Scoped skills are
// projected natively via trigger: glob whenever Skill.Globs is populated
// (which the parser handles from frontmatter override or scope default);
// scoped hooks reuse a __scope-guard__ wrapper script identical in shape
// to the one Claude uses (Windsurf's hook JSON-stdin contract is
// compatible enough for the wrapper to gate on tool_input.file_path).

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

// WindsurfPlugin projects Project state into `.windsurf/rules/*.md`,
// `.windsurf/hooks.json`, and `.windsurf/mcp_config.json`.
type WindsurfPlugin struct {
	// DisableHookWrappers, when true, projects scoped hooks as if they
	// were global (no __scope-guard__ wrapper). Mirrors the equivalent
	// knob on ClaudePlugin. Default false (wrappers ON).
	DisableHookWrappers bool
}

// NewWindsurf constructs a WindsurfPlugin.
func NewWindsurf() *WindsurfPlugin { return &WindsurfPlugin{} }

// Name returns the stable plugin identifier.
func (p *WindsurfPlugin) Name() string { return "windsurf" }

// Detect returns true if the project at root looks like it uses Windsurf.
// Presence of either the modern `.windsurf/` directory or the legacy flat
// `.windsurfrules` file at the project root counts as an activation signal.
func (p *WindsurfPlugin) Detect(root string) bool {
	if info, err := os.Stat(filepath.Join(root, ".windsurf")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, ".windsurfrules")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// Capabilities returns Windsurf's capability matrix.
//
// Windsurf natively supports always-on context (Context), per-glob rule
// attachment (ScopePaths via trigger: glob), and description-triggered
// attachment (ScopeSemantic via trigger: model_decision). Skills and
// Commands degrade (no script execution, no slash-command mechanism).
// Hooks are natively supported via Cascade Hooks (.windsurf/hooks.json)
// and MCP via .windsurf/mcp_config.json. Agents and Permissions remain
// unsupported.
func (p *WindsurfPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		// v0.8 coarse cells (unchanged for back-compat).
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportDegraded,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportNative,

		// v2 per-field cells per SPEC §12 (Windsurf / `win` column).

		// Agent: Windsurf has no subagent primitive — every field U.
		AgentFields: plugin.FieldCapabilities{
			Supported: false,
			Fields: map[string]plugin.FieldSupport{
				"Name":               plugin.FieldUnsupported,
				"Description":        plugin.FieldUnsupported,
				"SystemPrompt":       plugin.FieldUnsupported,
				"Model":              plugin.FieldUnsupported,
				"ModelFallbacks":     plugin.FieldSilent,
				"Tools":              plugin.FieldUnsupported,
				"DisallowedTools":    plugin.FieldUnsupported,
				"ReadOnly":           plugin.FieldUnsupported,
				"Background":         plugin.FieldUnsupported,
				"MaxTurns":           plugin.FieldUnsupported,
				"Temperature":        plugin.FieldUnsupported,
				"MCPServers":         plugin.FieldUnsupported,
				"AllowedSubagents":   plugin.FieldUnsupported,
				"UserInvocable":      plugin.FieldUnsupported,
				"ModelInvocable":     plugin.FieldUnsupported,
				"InitialPrompt":      plugin.FieldUnsupported,
				"ScopePath":          plugin.FieldUnsupported,
				"Extensions[plugin]": plugin.FieldNative,
			},
			Extensions: []string{"windsurf"},
		},

		// Skill: rules-file projection with trigger: glob /
		// model_decision. Scripts/Refs/AllowedTools/Arguments/Model/
		// Subagent unsupported (no script execution, no per-skill model).
		SkillFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":                             plugin.FieldNative,
				"Description":                      plugin.FieldNative,
				"WhenToUse":                        plugin.FieldDegraded,
				"Activation.Modes={Always}":        plugin.FieldNative,
				"Activation.Modes={ModelDecision}": plugin.FieldNative,
				"Activation.Modes={Glob}":          plugin.FieldNative,
				"Activation.Modes={Manual}":        plugin.FieldNative,
				"Activation.Globs":                 plugin.FieldNative,
				"Activation.ContentRegex":          plugin.FieldUnsupported,
				"Activation.UserInvocable":         plugin.FieldSilent,
				"Activation.ModelInvocable":        plugin.FieldSilent,
				"AllowedTools":                     plugin.FieldUnsupported,
				"Arguments":                        plugin.FieldUnsupported,
				"Scripts":                          plugin.FieldUnsupported,
				"References":                       plugin.FieldUnsupported,
				"Model":                            plugin.FieldUnsupported,
				"Subagent":                         plugin.FieldUnsupported,
				"ScopePath":                        plugin.FieldUnsupported,
				"Extensions[plugin]":               plugin.FieldNative,
			},
			Extensions: []string{"windsurf"},
		},

		// Command: degrades to a model_decision rule. Description is
		// dropped per SPEC §12 win column.
		CommandFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":               plugin.FieldNative,
				"Description":        plugin.FieldUnsupported,
				"ArgumentHint":       plugin.FieldSilent,
				"Arguments":          plugin.FieldDegraded,
				"Model":              plugin.FieldUnsupported,
				"Tools":              plugin.FieldUnsupported,
				"Agent":              plugin.FieldDegraded,
				"AutoInvoke":         plugin.FieldSilent,
				"ScopePath":          plugin.FieldDegraded,
				"Extensions[plugin]": plugin.FieldNative,
			},
			Extensions: []string{"windsurf"},
		},

		// Hook: Cascade Hooks native; per-action events (pre_run_command
		// etc.) 1:1. Matcher regex unsupported, FailClosed unsupported,
		// Claude-only events (SessionStart etc.) unsupported.
		HookFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":               plugin.FieldSilent,
				"Description":        plugin.FieldSilent,
				"Event":              plugin.FieldNative,
				"EventClaudeOnly":    plugin.FieldUnsupported,
				"Matcher":            plugin.FieldDegraded,
				"MatcherRegex":       plugin.FieldUnsupported,
				"Handlers.command":   plugin.FieldNative,
				"Handlers.http":      plugin.FieldUnsupported,
				"Handlers.mcp_tool":  plugin.FieldUnsupported,
				"Handlers.prompt":    plugin.FieldUnsupported,
				"Handlers.agent":     plugin.FieldUnsupported,
				"Sequential":         plugin.FieldSilent,
				"Disabled":           plugin.FieldNative,
				"TimeoutMs":          plugin.FieldUnsupported,
				"StatusMessage":      plugin.FieldSilent,
				"Async":              plugin.FieldDegraded,
				"FailClosed":         plugin.FieldUnsupported,
				"Once":               plugin.FieldUnsupported,
				"If":                 plugin.FieldUnsupported,
				"Cwd":                plugin.FieldNative,
				"Env":                plugin.FieldUnsupported,
				"Bash+Powershell":    plugin.FieldNative,
				"ScopePath":          plugin.FieldDegraded,
				"Extensions[plugin]": plugin.FieldNative,
			},
			Extensions: []string{"windsurf"},
		},

		// MCPServer: .windsurf/mcp_config.json. Most fields native;
		// Cwd silent, TimeoutMs/AutoApprove/Trust/Include-Exclude
		// unsupported per SPEC §12 win column.
		MCPServerFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":               plugin.FieldNative,
				"Transport.stdio":    plugin.FieldNative,
				"Transport.http":     plugin.FieldNative,
				"Transport.sse":      plugin.FieldNative,
				"Command":            plugin.FieldNative,
				"Args":               plugin.FieldNative,
				"Env":                plugin.FieldNative,
				"Cwd":                plugin.FieldSilent,
				"URL":                plugin.FieldNative,
				"Headers":            plugin.FieldNative,
				"Auth.Scheme=bearer": plugin.FieldNative,
				"Auth.Scheme=header": plugin.FieldNative,
				"Auth.Scheme=oauth":  plugin.FieldDegraded,
				"TimeoutMs":          plugin.FieldUnsupported,
				"Disabled":           plugin.FieldNative,
				"AutoApprove":        plugin.FieldUnsupported,
				"Trust":              plugin.FieldUnsupported,
				"IncludeTools":       plugin.FieldUnsupported,
				"ExcludeTools":       plugin.FieldUnsupported,
				"ScopePath":          plugin.FieldDegraded,
				"Extensions[plugin]": plugin.FieldNative,
			},
			Extensions: []string{"windsurf"},
		},

		// Permissions: Windsurf has no permissions primitive — every
		// field unsupported (no perms-guard wrapper today).
		PermissionsFields: plugin.FieldCapabilities{
			Supported: false,
			Fields: map[string]plugin.FieldSupport{
				"Allow":                plugin.FieldUnsupported,
				"Ask":                  plugin.FieldUnsupported,
				"Deny":                 plugin.FieldUnsupported,
				"AllowScoped":          plugin.FieldUnsupported,
				"AskScoped":            plugin.FieldUnsupported,
				"DenyScoped":           plugin.FieldUnsupported,
				"Target.bash":          plugin.FieldUnsupported,
				"Target.editreadwrite": plugin.FieldUnsupported,
				"Target.fs":            plugin.FieldUnsupported,
				"Target.network":       plugin.FieldUnsupported,
				"Target.mcp":           plugin.FieldUnsupported,
				"GlobRecursive":        plugin.FieldUnsupported,
				"GlobNegation":         plugin.FieldUnsupported,
				"Extensions[plugin]":   plugin.FieldUnsupported,
			},
		},

		// Scope: rule files via trigger glob/model_decision/manual; full
		// activation matrix supported.
		ScopeFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Path":                     plugin.FieldDegraded,
				"PathEmpty":                plugin.FieldNative,
				"Name":                     plugin.FieldNative,
				"Description":              plugin.FieldNative,
				"Globs":                    plugin.FieldNative,
				"Activation=Always":        plugin.FieldNative,
				"Activation=Cascade":       plugin.FieldDegraded,
				"Activation=Glob":          plugin.FieldNative,
				"Activation=Manual":        plugin.FieldNative,
				"Activation=ModelDecision": plugin.FieldNative,
				"Priority":                 plugin.FieldSilent,
				"Tags":                     plugin.FieldSilent,
				"IsOverride":               plugin.FieldUnsupported,
				"Extensions[plugin]":       plugin.FieldNative,
			},
			Extensions: []string{"windsurf"},
		},
	}
}

// Plan produces the Operations needed to project proj into `.windsurf/`.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Unknown modes return an error.
func (p *WindsurfPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	switch opts.Mode {
	case "", "write":
		// ok
	default:
		return nil, fmt.Errorf("windsurf: unsupported mode %q", opts.Mode)
	}

	var ops []plugin.Operation

	// Root context → .windsurf/rules/_root.md with trigger: always_on.
	if proj.Context != nil {
		content := renderWindsurfRule(windsurfFrontmatter{
			Trigger: "always_on",
		}, proj.Context.Body)
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".windsurf/rules/_root.md",
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
		body := ""
		var sources []string
		if sc.Document != nil {
			body = sc.Document.Body
			sources = []string{proj.SourceTag(sc.Document.SourcePath)}
		}
		// v2 read (additive, SPEC §4.7.2): fall back to Activation.Globs
		// when the v0.8 Scope.Globs slice is empty. Same emission shape.
		globs := sc.Globs
		// Note: Scope.Activation is a scalar ScopeActivation, not a struct
		// — Globs live only on Scope.Globs in the v2 model. No-op here for
		// scopes; the skill loop below carries the Activation.Globs read.
		fm := windsurfFrontmatter{
			Trigger:     "glob",
			Globs:       globs,
			Description: sc.Description,
			Name:        sc.Name,
			Extensions:  pluginExtensions(sc.Extensions, "windsurf"),
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", slugify(sc.Path)+".md")),
			Content: renderWindsurfRule(fm, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
	}

	// Skills → degraded rule files. Globbed skills become trigger: glob,
	// otherwise trigger: model_decision (which requires a description).
	// Scoped skills include the scope slug as a filename prefix to avoid
	// collisions across scopes. Skill.Globs is already populated by the
	// parser (frontmatter override or scope default) so a scoped skill
	// naturally lands on trigger: glob.
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
		// v2 read (additive, SPEC §4.2.2): fall back to Activation.Globs
		// when v0.8 Skill.Globs is empty. The parser already mirrors them,
		// but reading both makes the plugin robust to direct-API callers
		// that only populate the v2 field. No emission shape change.
		skillGlobs := skill.Globs
		if len(skillGlobs) == 0 {
			skillGlobs = skill.Activation.Globs
		}
		skillExts := pluginExtensions(skill.Extensions, "windsurf")
		var fm windsurfFrontmatter
		if len(skillGlobs) > 0 {
			fm = windsurfFrontmatter{
				Trigger:     "glob",
				Globs:       skillGlobs,
				Description: skill.Description,
				Extensions:  skillExts,
			}
		} else {
			desc := skill.Description
			if desc == "" {
				desc = skill.Trigger
			}
			fm = windsurfFrontmatter{
				Trigger:     "model_decision",
				Description: desc,
				Extensions:  skillExts,
			}
		}
		fname := "skill-" + scopedSkillSlug(skill.ScopePath, skill.Name) + ".md"
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", fname)),
			Content: renderWindsurfRule(fm, body),
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
				Message:  fmt.Sprintf("Windsurf has no script execution; scripts ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Commands → degraded rule files with trigger: model_decision and a
	// description prefixed with "Command /<name>: ". Each command also emits
	// an info warning on its own op.
	//
	// Scoped commands get the scope slug as a filename prefix, the scope path
	// surfaced in the description (so the model_decision trigger knows about
	// it), and a warning naming the scope.
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
		desc := fmt.Sprintf("Command /%s: %s", cmd.Name, cmd.Description)
		warningMsg := fmt.Sprintf("Windsurf has no slash-command mechanism; %s documented as a rule.", cmd.Name)
		if cmd.ScopePath != "" {
			desc = fmt.Sprintf("Command /%s (scoped to %s): %s", cmd.Name, cmd.ScopePath, cmd.Description)
			warningMsg = fmt.Sprintf("Windsurf has no slash-command mechanism; scoped command %q (scope: %s) documented as a rule.", cmd.Name, cmd.ScopePath)
		}
		fm := windsurfFrontmatter{
			Trigger:     "model_decision",
			Description: desc,
			Extensions:  pluginExtensions(cmd.Extensions, "windsurf"),
		}
		fname := "command-" + scopedSkillSlug(cmd.ScopePath, cmd.Name) + ".md"
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", fname)),
			Content: renderWindsurfRule(fm, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   sourceFromCommand(proj, cmd),
			Message:  warningMsg,
			Severity: "info",
		})
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (emitted before the hooks.json op so the
	// hooks.json op can reference each wrapper's absolute path). Windsurf's
	// hook JSON-stdin contract is compatible enough with Claude's
	// scope-guard model that we reuse the same wrapper renderer: the
	// wrapper exec's `prism scope-guard --scope <path> --script <abs>`,
	// which parses stdin JSON and only invokes the source script when
	// tool_input.file_path falls under the scope. See the scope-guard
	// docstring in claude.go for full details on the runtime contract.
	wrapperPaths := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + hookName + ".sh"
		wrapperRel := filepath.Join(".windsurf", "hooks", "__scope-guard__", wrapperFile)
		wrapperAbs := filepath.Join(proj.Root, wrapperRel)

		body := buildScopeGuardScript(wrapperRel, h.ScopePath, h.ScriptPath, formatScriptArg(h.ScriptPath, proj.Root))
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     filepath.ToSlash(wrapperRel),
			Content:  body,
			Mode:     plugin.ModeWrite,
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   p.Name(),
		})
		wrapperPaths[h] = wrapperAbs
	}

	// .windsurf/hooks.json — Cascade Hooks. Each prism Hook.Event is
	// mapped to a Windsurf event via mapWindsurfEvent (which uses the
	// matcher for the Claude-style PreToolUse/PostToolUse cases).
	// Unmappable hooks attach an info warning instead of being emitted.
	var hooksWarnings []plugin.Warning
	if len(proj.Hooks) > 0 {
		hooksOp, hw, err := buildWindsurfHooksOp(proj, wrapperPaths)
		if err != nil {
			return nil, err
		}
		if hooksOp != nil {
			ops = append(ops, *hooksOp)
		}
		hooksWarnings = hw
	}

	// .windsurf/mcp_config.json — MCP servers. Project-local for
	// portability; canonical location is ~/.codeium/windsurf/mcp_config.json
	// so an info warning fires whenever any MCP server is projected.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildWindsurfMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	// Collect un-attached warnings for capability types we do not project.
	// These attach to the first emitted op below (or to a synthesized
	// no-op when no other op was produced — the warning-only path is the
	// permissions/scope-IsOverride case where we still need to surface
	// per-field info/warn warnings).
	var warnings []plugin.Warning
	warnings = append(warnings, hooksWarnings...)
	warnings = append(warnings, windsurfAgentWarnings(proj)...)
	warnings = append(warnings, windsurfSkillWarnings(proj)...)
	warnings = append(warnings, windsurfCommandWarnings(proj)...)
	warnings = append(warnings, windsurfHookFieldWarnings(proj)...)
	warnings = append(warnings, windsurfMCPFieldWarnings(proj)...)
	warnings = append(warnings, windsurfPermissionsWarnings(proj)...)
	warnings = append(warnings, windsurfScopeWarnings(proj)...)

	if len(warnings) == 0 {
		return ops, nil
	}
	if len(ops) == 0 {
		// Synthesize a no-content carrier op so warnings surface to the
		// caller even when no rules / hooks / MCP files would otherwise
		// be emitted (e.g. permissions-only or scope-IsOverride-only
		// inputs). The warning-only op has Kind=OpWrite + empty Content,
		// which the engine no-ops on while still propagating warnings.
		ops = append(ops, plugin.Operation{
			Kind:   plugin.OpWrite,
			Path:   ".windsurf/.warnings",
			Mode:   plugin.ModeWrite,
			Plugin: p.Name(),
		})
	}
	ops[0].Warnings = append(ops[0].Warnings, warnings...)

	return ops, nil
}

// windsurfAgentWarnings emits one Warning per (agent × field) cell. Every
// Agent field is unsupported on Windsurf (no subagent primitive); we
// surface a `warn`-level warning naming the specific field so callers can
// detect which fields would have round-tripped on other targets.
func windsurfAgentWarnings(proj *model.Project) []plugin.Warning {
	var out []plugin.Warning
	for _, ag := range proj.Agents {
		if ag == nil {
			continue
		}
		src := ""
		if ag.Document != nil {
			src = proj.SourceTag(ag.Document.SourcePath)
		}
		warn := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf has no subagent primitive; agent %q field %s %s.", ag.Name, field, detail),
				Severity: "warn",
			})
		}
		// Per SPEC §12 win column, every Agent field is U. The Name +
		// Description warnings double as the legacy "agent not projected"
		// info so we keep one info-severity carrier for the agent itself.
		out = append(out, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("Windsurf has no subagent primitive; agent %s not projected.", ag.Name),
			Severity: "info",
		})
		if ag.SystemPrompt != "" {
			warn("SystemPrompt", "dropped")
		}
		if ag.Model != "" {
			warn("Model", "dropped")
		}
		if len(ag.Tools) > 0 {
			warn("Tools", "dropped")
		}
		if len(ag.DisallowedTools) > 0 {
			warn("DisallowedTools", "dropped")
		}
		if ag.ReadOnly != nil {
			warn("ReadOnly", "dropped")
		}
		if ag.Background != nil {
			warn("Background", "dropped")
		}
		if ag.MaxTurns != nil {
			warn("MaxTurns", "dropped")
		}
		if ag.Temperature != nil {
			warn("Temperature", "dropped")
		}
		if len(ag.MCPServers) > 0 {
			warn("MCPServers", "dropped")
		}
		if len(ag.AllowedSubagents) > 0 {
			warn("AllowedSubagents", "dropped")
		}
		if ag.UserInvocable != nil {
			warn("UserInvocable", "dropped")
		}
		if ag.ModelInvocable != nil {
			warn("ModelInvocable", "dropped")
		}
		if ag.InitialPrompt != "" {
			warn("InitialPrompt", "dropped")
		}
		if ag.ScopePath != "" {
			warn("ScopePath", "dropped")
		}
	}
	return out
}

// windsurfSkillWarnings emits one Warning per (skill × field) cell for
// fields that Windsurf does not natively support on the rules-file
// projection (degraded WhenToUse, unsupported AllowedTools / Arguments /
// References / Model / Subagent / ScopePath / Activation.ContentRegex).
func windsurfSkillWarnings(proj *model.Project) []plugin.Warning {
	var out []plugin.Warning
	for _, sk := range proj.Skills {
		if sk == nil {
			continue
		}
		src := ""
		if sk.Document != nil {
			src = proj.SourceTag(sk.Document.SourcePath)
		}
		info := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf rule projection for skill %q: %s %s.", sk.Name, field, detail),
				Severity: "info",
			})
		}
		warn := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf rule projection for skill %q: %s %s.", sk.Name, field, detail),
				Severity: "warn",
			})
		}
		if sk.WhenToUse != "" {
			info("WhenToUse", "merged into the model_decision description (degraded)")
		}
		if len(sk.AllowedTools) > 0 {
			warn("AllowedTools", "unsupported (Windsurf rules have no allowed-tools filter); dropped")
		}
		if len(sk.Arguments) > 0 {
			warn("Arguments", "unsupported (Windsurf rules cannot accept arguments); dropped")
		}
		if len(sk.References) > 0 {
			warn("References", "unsupported (Windsurf rules have no reference file mechanism); dropped")
		}
		if sk.Model != "" {
			warn("Model", "unsupported (Windsurf rules cannot pin a model); dropped")
		}
		if sk.Subagent != "" {
			warn("Subagent", "unsupported (Windsurf has no subagent primitive); dropped")
		}
		if sk.ScopePath != "" {
			warn("ScopePath", "unsupported as a scope filter (rule emitted globally); scope encoded only in filename")
		}
		if sk.Activation.ContentRegex != "" {
			warn("Activation.ContentRegex", "unsupported (Windsurf rules cannot gate on content regex); dropped")
		}
	}
	return out
}

// windsurfCommandWarnings emits one Warning per (command × field) cell
// for fields that Windsurf cannot honor on the degraded
// command-as-rule projection (Description dropped per SPEC §12 win
// column; Arguments/Agent/ScopePath degraded; Model/Tools unsupported).
//
// The base "documented as a rule" info warning is emitted in the command
// loop above; this function adds per-field warnings on top, leaving the
// base warning intact.
func windsurfCommandWarnings(proj *model.Project) []plugin.Warning {
	var out []plugin.Warning
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		src := sourceFromCommand(proj, cmd)
		info := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf rule projection for command %q: %s %s.", cmd.Name, field, detail),
				Severity: "info",
			})
		}
		warn := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf rule projection for command %q: %s %s.", cmd.Name, field, detail),
				Severity: "warn",
			})
		}
		if cmd.Description != "" {
			warn("Description", "dropped (Windsurf rules have no description-as-command surface)")
		}
		if len(cmd.Arguments) > 0 {
			info("Arguments", "documented in body only (degraded; Windsurf rules cannot bind arguments)")
		}
		if cmd.Model != "" {
			warn("Model", "unsupported (Windsurf rules cannot pin a model); dropped")
		}
		if len(cmd.Tools) > 0 {
			warn("Tools", "unsupported (Windsurf rules have no allowed-tools filter); dropped")
		}
		if cmd.Agent != "" {
			info("Agent", "documented in body only (degraded; Windsurf has no subagent primitive)")
		}
		if cmd.ScopePath != "" {
			info("ScopePath", "documented in description only (degraded; Windsurf rules have no scope filter)")
		}
	}
	return out
}

// windsurfHookFieldWarnings emits per-handler field warnings for Hook
// fields that Windsurf does not honor natively: Env, FailClosed, TimeoutMs
// (unsupported), ScopePath (degraded). Cwd is natively honored via the
// `cd && exec` envelope in buildWindsurfHookEntries, so no warning fires.
func windsurfHookFieldWarnings(proj *model.Project) []plugin.Warning {
	var out []plugin.Warning
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		src := h.ScriptPath
		hookName := h.Name
		if hookName == "" {
			hookName = "(unnamed)"
		}
		info := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf hook %q: %s %s.", hookName, field, detail),
				Severity: "info",
			})
		}
		warn := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf hook %q: %s %s.", hookName, field, detail),
				Severity: "warn",
			})
		}
		if h.ScopePath != "" {
			info("ScopePath", "projected via __scope-guard__ wrapper (degraded; Windsurf has no per-scope hooks)")
		}
		for _, hd := range h.Handlers {
			if len(hd.Env) > 0 {
				warn("Env", "unsupported (Windsurf hooks have no env field); merge into your shell command if required")
			}
			if hd.FailClosed {
				warn("FailClosed", "unsupported (Windsurf hooks always fail-open on non-zero exit beyond pre_*); wrap as needed")
			}
			if hd.TimeoutMs > 0 {
				warn("TimeoutMs", "unsupported (Windsurf hooks have no timeout field); dropped")
			}
		}
	}
	return out
}

// windsurfMCPFieldWarnings emits per-server field warnings for MCP fields
// Windsurf does not honor: AutoApprove / Trust / IncludeTools /
// ExcludeTools / TimeoutMs (unsupported, warn), ScopePath (degraded,
// info — supplements the existing "applied project-wide" warning so the
// per-field warning mentions "ScopePath" explicitly for the contract).
func windsurfMCPFieldWarnings(proj *model.Project) []plugin.Warning {
	var out []plugin.Warning
	for _, srv := range proj.MCP {
		if srv == nil {
			continue
		}
		src := "mcp.yaml"
		if srv.ScopePath != "" {
			src = filepath.ToSlash(filepath.Join(srv.ScopePath, "mcp.yaml"))
		}
		warn := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf MCP server %q: %s %s.", srv.Name, field, detail),
				Severity: "warn",
			})
		}
		info := func(field, detail string) {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf MCP server %q: %s %s.", srv.Name, field, detail),
				Severity: "info",
			})
		}
		if len(srv.AutoApprove) > 0 {
			warn("AutoApprove", "unsupported (Windsurf has no auto-approve list); dropped")
		}
		if srv.Trust {
			warn("Trust", "unsupported (Windsurf has no trust flag); dropped")
		}
		if len(srv.IncludeTools) > 0 {
			warn("IncludeTools", "unsupported (Windsurf has no include-tools filter); dropped")
		}
		if len(srv.ExcludeTools) > 0 {
			warn("ExcludeTools", "unsupported (Windsurf has no exclude-tools filter); dropped")
		}
		if srv.TimeoutMs > 0 {
			warn("TimeoutMs", "unsupported (Windsurf has no per-server timeout); dropped")
		}
		if srv.ScopePath != "" {
			info("ScopePath", "applied project-wide (degraded; Windsurf has no per-scope MCP)")
		}
	}
	return out
}

// windsurfPermissionsWarnings emits one warn-level warning per permission
// bucket per source (global vs scoped) naming the field. Per SPEC §12 win
// column every permissions row is U.
func windsurfPermissionsWarnings(proj *model.Project) []plugin.Warning {
	var out []plugin.Warning
	emit := func(src, field string) {
		out = append(out, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("Windsurf has no permissions primitive; %s entries dropped.", field),
			Severity: "warn",
		})
	}
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 {
			emit("permissions.yaml", "Allow")
		}
		if len(proj.Permissions.Ask) > 0 {
			emit("permissions.yaml", "Ask")
		}
		if len(proj.Permissions.Deny) > 0 {
			emit("permissions.yaml", "Deny")
		}
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		src := filepath.ToSlash(filepath.Join(sp.ScopePath, "permissions.yaml"))
		if len(sp.Allow) > 0 {
			emit(src, "Allow (scoped)")
		}
		if len(sp.Ask) > 0 {
			emit(src, "Ask (scoped)")
		}
		if len(sp.Deny) > 0 {
			emit(src, "Deny (scoped)")
		}
	}
	return out
}

// windsurfScopeWarnings emits per-field warnings for Scope fields that
// Windsurf does not natively honor. Only IsOverride is U today (SPEC §12
// win column row); every other scope field is N or D and handled by the
// per-scope rule emission above.
func windsurfScopeWarnings(proj *model.Project) []plugin.Warning {
	var out []plugin.Warning
	for _, sc := range proj.Scopes {
		if sc == nil {
			continue
		}
		src := ""
		if sc.Document != nil {
			src = proj.SourceTag(sc.Document.SourcePath)
		}
		if sc.IsOverride {
			out = append(out, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf scope %q: IsOverride unsupported (no AGENTS.override semantic); flag dropped.", sc.Path),
				Severity: "warn",
			})
		}
	}
	return out
}

// sourceFromCommand returns the .agents/-relative source path for a Command
// if one is available, else empty.
func sourceFromCommand(proj *model.Project, cmd *model.Command) string {
	if cmd == nil || cmd.Document == nil {
		return ""
	}
	return proj.SourceTag(cmd.Document.SourcePath)
}

// windsurfFrontmatter holds the fields renderWindsurfRule emits. Trigger is
// required for every rule; Globs is required when Trigger == "glob";
// Description is required when Trigger == "model_decision" and optional
// otherwise. Name surfaces Scope.Name for downstream introspection (not
// part of Windsurf's wire schema but emitted as a comment-style key for
// round-trip parity). Extensions carries the v2 `extensions.windsurf.*`
// pass-through payload (SPEC §4.x); when non-empty it is rendered verbatim
// into the frontmatter under sorted keys.
//
// TODO(v0.9, SPEC §2): Windsurf has a 12,000 char per-workflow / per-scope
// cap on rule bodies (see windsurf.com docs). Phase 2a does not enforce
// this; a subsequent phase should surface a warn-severity warning when the
// emitted body exceeds 12000 chars.
type windsurfFrontmatter struct {
	Trigger     string
	Globs       []string
	Description string
	Name        string
	Extensions  map[string]any
}

// renderWindsurfRule formats the YAML frontmatter + markdown body for a
// Windsurf rule file. globs are serialized as a JSON-flow-style array (valid
// YAML); json.Marshal handles escaping.
func renderWindsurfRule(fm windsurfFrontmatter, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if fm.Trigger != "" {
		b.WriteString("trigger: ")
		b.WriteString(fm.Trigger)
		b.WriteString("\n")
	}
	if len(fm.Globs) > 0 {
		b.WriteString("globs: ")
		b.WriteString(renderGlobs(fm.Globs))
		b.WriteString("\n")
	}
	if fm.Description != "" {
		b.WriteString("description: ")
		b.WriteString(renderYAMLScalar(fm.Description))
		b.WriteString("\n")
	}
	if fm.Name != "" {
		b.WriteString("name: ")
		b.WriteString(renderYAMLScalar(fm.Name))
		b.WriteString("\n")
	}
	if len(fm.Extensions) > 0 {
		// Deterministic key order: sort then emit each as a JSON-flow
		// scalar. A JSON-encoded value is a valid YAML flow scalar/map,
		// so this round-trips cleanly through yaml.v3.
		keys := make([]string, 0, len(fm.Extensions))
		for k := range fm.Extensions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			raw, err := json.Marshal(fm.Extensions[k])
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

// pluginExtensions extracts the `extensions.<name>` sub-map from a
// canonical primitive's Extensions field and returns it as a map suitable
// for verbatim emission into a target's frontmatter. Returns nil if the
// namespace is absent or not a map. Per SPEC §4.x, plugins are expected
// to surface their own namespace's payload unchanged (no key remapping).
func pluginExtensions(ext map[string]any, name string) map[string]any {
	if ext == nil {
		return nil
	}
	sub, ok := ext[name]
	if !ok {
		return nil
	}
	m, ok := sub.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	return m
}

// windsurfHookEntry mirrors Windsurf's Cascade Hooks JSON schema for a
// single hook command:
//
//	{"command": "<shell command>", "show_output": false}
//
// We do not emit `powershell` (Windows-specific variant) — the source
// script is bash so Windsurf on Windows would not run it anyway; users
// targeting Windows should declare a powershell variant in their .agents/
// hooks and wire it through a separate projection pass (out of scope for
// v0.8).
type windsurfHookEntry struct {
	Command    string `json:"command"`
	ShowOutput bool   `json:"show_output,omitempty"`
}

// windsurfHookEvents is the canonical set of Cascade hook event names. The
// authoritative list (per docs.windsurf.com/windsurf/cascade/hooks) is 12
// events: 5 pre-hooks that can block (pre_user_prompt, pre_read_code,
// pre_write_code, pre_run_command, pre_mcp_tool_use), 4 post-hooks
// (post_user_prompt, post_read_code, post_write_code, post_run_command),
// and 3 lifecycle hooks (post_cascade_response,
// post_cascade_response_with_transcript, post_setup_worktree).
//
// Note: the live docs at the time of this writing also enumerate
// post_mcp_tool_use, which the v0.8 spec did not include. The mapping
// table below accepts that event by name pass-through but does not
// auto-derive it from Claude's PostToolUse + mcp matcher unless the
// matcher unambiguously names mcp.
var windsurfHookEvents = map[string]struct{}{
	"pre_user_prompt":                       {},
	"pre_read_code":                         {},
	"pre_write_code":                        {},
	"pre_run_command":                       {},
	"pre_mcp_tool_use":                      {},
	"post_user_prompt":                      {},
	"post_read_code":                        {},
	"post_write_code":                       {},
	"post_run_command":                      {},
	"post_mcp_tool_use":                     {},
	"post_cascade_response":                 {},
	"post_cascade_response_with_transcript": {},
	"post_setup_worktree":                   {},
}

// mapWindsurfEvent translates a prism Hook event+matcher into a Windsurf
// Cascade hook event name. The translation rules:
//
//  1. If event matches a Windsurf event name exactly (case-insensitive),
//     pass through unchanged. This is the canonical path for hooks
//     authored with Windsurf as the target.
//
//  2. Claude-style PreToolUse / PostToolUse routes by matcher:
//     - Bash, Shell, *Bash*       → pre_run_command / post_run_command
//     - Read, Glob, Grep, *Read*  → pre_read_code   / post_read_code
//     - Write, Edit, MultiEdit    → pre_write_code  / post_write_code
//     - mcp__* / *MCP*            → pre_mcp_tool_use / post_mcp_tool_use
//     - "" or unknown             → pre_run_command / post_run_command
//
//  3. Claude UserPromptSubmit (and aliases) → pre_user_prompt.
//
//  4. SessionStart / SessionEnd / SubagentStop / Stop / Notification /
//     PreCompact → no Windsurf equivalent, drop with warning.
//
// Returns ("", false) when no mapping exists; callers emit a warning.
func mapWindsurfEvent(event, matcher string) (string, bool) {
	ev := strings.ToLower(strings.TrimSpace(event))
	if ev == "" {
		return "", false
	}
	if _, ok := windsurfHookEvents[ev]; ok {
		return ev, true
	}

	m := strings.ToLower(strings.TrimSpace(matcher))
	switch ev {
	case "pretooluse":
		return classifyToolMatcher(m, "pre"), true
	case "posttooluse":
		return classifyToolMatcher(m, "post"), true
	case "userpromptsubmit", "userprompt", "pre_user_prompt_submit":
		return "pre_user_prompt", true
	case "stop", "subagentstop", "sessionstart", "sessionend", "notification", "precompact":
		return "", false
	}
	return "", false
}

// classifyToolMatcher routes a (lowercased) tool matcher onto one of
// run_command / read_code / write_code / mcp_tool_use, prefixed with the
// given phase ("pre" or "post"). The mapping favors substring matches
// so "Bash(*)" and "BashOutput" both land on run_command.
func classifyToolMatcher(matcher, phase string) string {
	switch {
	case strings.Contains(matcher, "mcp"):
		return phase + "_mcp_tool_use"
	case strings.Contains(matcher, "write") || strings.Contains(matcher, "edit") || strings.Contains(matcher, "multiedit"):
		return phase + "_write_code"
	case strings.Contains(matcher, "read") || strings.Contains(matcher, "glob") || strings.Contains(matcher, "grep"):
		return phase + "_read_code"
	case strings.Contains(matcher, "bash") || strings.Contains(matcher, "shell") || strings.Contains(matcher, "run"):
		return phase + "_run_command"
	}
	return phase + "_run_command"
}

// buildWindsurfHooksOp emits the OpMerge for `.windsurf/hooks.json`. The
// Merger closure preserves any user-authored top-level keys (anything
// other than "hooks") on the next prism run, mirroring
// claude.go:buildSettingsOp.
//
// Portability note: Windsurf's hooks.json has no ${PROJECT_DIR}-style
// substitution, so wrapper paths are baked in as absolute. Moving the
// project tree requires re-running `prism compile` to refresh paths.
//
// Returns (op, warnings, err). Warnings are returned separately so the
// caller can fold them onto the first op (which may not be this one if
// every input hook drops with a warning).
func buildWindsurfHooksOp(proj *model.Project, wrapperPaths map[*model.Hook]string) (*plugin.Operation, []plugin.Warning, error) {
	buckets := map[string][]windsurfHookEntry{}
	var eventOrder []string
	var warnings []plugin.Warning
	var sources []string
	srcSeen := map[string]struct{}{}
	addSource := func(s string) {
		if s == "" {
			return
		}
		if _, ok := srcSeen[s]; ok {
			return
		}
		srcSeen[s] = struct{}{}
		sources = append(sources, s)
	}

	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		// v2 read (additive, SPEC §4.4.2): fall back to EventCanonical
		// when the v0.8 Hook.Event string is empty. The parser populates
		// both shapes; reading both makes the plugin robust to direct-API
		// callers that only set the typed enum. We strip the optional
		// "native:" prefix that v2 uses to mark target-specific names.
		eventName := h.Event
		if eventName == "" && h.EventCanonical != "" {
			eventName = strings.TrimPrefix(string(h.EventCanonical), "native:")
		}
		// Map prism canonical events (pre_tool_use, post_tool_use) onto
		// Claude-shape names so mapWindsurfEvent's switch lands on the
		// right branch. Direct windsurf event names already pass through.
		switch strings.ToLower(eventName) {
		case "pre_tool_use":
			eventName = "PreToolUse"
		case "post_tool_use":
			eventName = "PostToolUse"
		case "user_prompt_submit":
			eventName = "UserPromptSubmit"
		}
		if eventName == "" {
			continue
		}
		wsEvent, ok := mapWindsurfEvent(eventName, h.Matcher)
		if !ok {
			msg := fmt.Sprintf("Windsurf Cascade Hooks has no event matching %q (matcher=%q); hook not projected.", eventName, h.Matcher)
			if h.ScopePath != "" {
				msg = fmt.Sprintf("Windsurf Cascade Hooks has no event matching %q (matcher=%q, scope: %s); hook not projected.", eventName, h.Matcher, h.ScopePath)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   h.ScriptPath,
				Message:  msg,
				Severity: "info",
			})
			continue
		}

		// Build the command string. v0.8 callers populate Hook.ScriptPath
		// directly; v2 callers populate Handlers[] with typed command
		// handlers. We render one Cascade entry per command handler (plus
		// a single entry for ScriptPath when set), so direct-API tests
		// that only seed Handlers still produce wire output.
		entries := buildWindsurfHookEntries(h, wrapperPaths)
		if len(entries) == 0 {
			continue
		}
		if _, seen := buckets[wsEvent]; !seen {
			eventOrder = append(eventOrder, wsEvent)
		}
		buckets[wsEvent] = append(buckets[wsEvent], entries...)
		if h.ScriptPath != "" {
			addSource(proj.SourceTag(h.ScriptPath))
		}
	}

	// Sort event names so output is deterministic regardless of input
	// hook order. Within each event, hooks preserve declaration order
	// (matches Claude's settings.json behavior).
	sort.Strings(eventOrder)

	if len(buckets) == 0 {
		// No projectable hooks (all dropped with warnings); skip the op
		// entirely so we don't clobber an existing hooks.json with an
		// empty merge.
		return nil, warnings, nil
	}

	relPath := ".windsurf/hooks.json"
	merger := func(existing []byte) (string, error) {
		root := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &root); err != nil {
				return "", fmt.Errorf("windsurf: parsing existing %s: %w", relPath, err)
			}
		}
		hooksRoot, _ := root["hooks"].(map[string]any)
		if hooksRoot == nil {
			hooksRoot = map[string]any{}
		}
		for _, ev := range eventOrder {
			entries := buckets[ev]
			// Convert to []any so json.MarshalIndent emits the schema
			// we want regardless of generic map[string]any nesting.
			out := make([]any, 0, len(entries))
			for _, e := range entries {
				out = append(out, e)
			}
			hooksRoot[ev] = out
		}
		root["hooks"] = hooksRoot

		raw, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return "", err
		}
		return string(raw) + "\n", nil
	}

	// Pre-render Content from the merger over empty bytes so plan-time
	// inspection (capability contract test, dry-run preview) can see the
	// projected output without invoking the engine merge pipeline. The
	// engine itself ignores Content when Merger is non-nil (per
	// plugin.Operation contract).
	preview, _ := merger(nil)

	op := plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    relPath,
		Mode:    plugin.ModeWrite,
		Content: preview,
		Sources: append(append([]string{}, "hooks.yaml"), sources...),
		Plugin:  "windsurf",
		Merger:  merger,
	}
	return &op, warnings, nil
}

// buildWindsurfHookEntries returns one or more Cascade hook entries for
// a Hook. Per-handler command paths (including the optional handler Cwd)
// are baked into the rendered command so Windsurf's shell-level hook
// invocation honors them; the wrapper-path map overrides the raw script
// path with the scope-guard wrapper when present.
func buildWindsurfHookEntries(h *model.Hook, wrapperPaths map[*model.Hook]string) []windsurfHookEntry {
	var out []windsurfHookEntry
	wrap := wrapperPaths[h]

	// v2 typed handlers — emit one entry per Command handler.
	for _, hd := range h.Handlers {
		if hd.Kind != "" && hd.Kind != model.HookHandlerCommand {
			// non-command handler kinds are unsupported on Windsurf;
			// the field-level warnings loop surfaces them separately.
			continue
		}
		if hd.Command == "" && wrap == "" && h.ScriptPath == "" {
			continue
		}
		cmd := hd.Command
		if cmd == "" && h.ScriptPath != "" {
			cmd = h.ScriptPath
		}
		if wrap != "" {
			cmd = wrap
		}
		full := "bash " + shellQuote(cmd)
		if len(hd.Args) > 0 {
			for _, a := range hd.Args {
				full += " " + shellQuote(a)
			}
		}
		if hd.Cwd != "" {
			// Windsurf has no native cwd field on the hook entry, so we
			// bake the directory change into the shell command. This
			// keeps the projected output authoritative for Cwd (the
			// FieldNative contract checks the projected file content).
			full = fmt.Sprintf("cd %s && %s", shellQuote(hd.Cwd), full)
		}
		out = append(out, windsurfHookEntry{Command: full, ShowOutput: false})
	}
	if len(out) > 0 {
		return out
	}

	// v0.8 fall-through: ScriptPath only.
	if h.ScriptPath == "" {
		return nil
	}
	cmdPath := h.ScriptPath
	if wrap != "" {
		cmdPath = wrap
	}
	out = append(out, windsurfHookEntry{
		Command:    "bash " + shellQuote(cmdPath),
		ShowOutput: false,
	})
	return out
}

// windsurfMCPServerJSON mirrors the entry schema under mcpServers in
// Windsurf's mcp_config.json. The schema matches Claude Code's .mcp.json
// (command + args + env, optional url for SSE servers), so we reuse the
// same shape. Headers are emitted alongside URL for http/sse transports.
type windsurfMCPServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// windsurfMCPCanonicalPathWarning is the info-severity message attached
// to every windsurf MCP op. It tells the user that the canonical location
// for MCP config in Windsurf is the user-level path, and gives the two
// workarounds (symlink or shell-init) for making the project-local copy
// take effect.
const windsurfMCPCanonicalPathWarning = "Windsurf canonically reads MCP servers from ~/.codeium/windsurf/mcp_config.json (user-level). " +
	"This project-local .windsurf/mcp_config.json is emitted for portability; " +
	"to activate it, either symlink (`ln -sf \"$PWD/.windsurf/mcp_config.json\" ~/.codeium/windsurf/mcp_config.json`) " +
	"or copy/merge it into the canonical path via your shell init."

// buildWindsurfMCPOp emits the OpMerge for `.windsurf/mcp_config.json`.
// The Merger closure preserves any user-authored entries in mcpServers
// and any top-level keys we don't own, mirroring claude.go:buildSettingsOp.
//
// One canonical-path info warning is emitted on the op itself (regardless
// of how many servers are projected). Scoped MCP servers add a second
// warning per scope (Windsurf has no per-scope MCP, same as Claude).
func buildWindsurfMCPOp(proj *model.Project) (plugin.Operation, error) {
	relPath := ".windsurf/mcp_config.json"

	// Build the project's contribution as a name→entry map; the merger
	// applies it over the existing file.
	contributions := make(map[string]windsurfMCPServerJSON)
	var warnings []plugin.Warning
	var sources []string
	srcSeen := map[string]struct{}{}
	addSource := func(s string) {
		if s == "" {
			return
		}
		if _, ok := srcSeen[s]; ok {
			return
		}
		srcSeen[s] = struct{}{}
		sources = append(sources, s)
	}
	// Always include the base mcp.yaml source tag so `agents which`
	// resolves the projected file even when every server is scoped.
	addSource("mcp.yaml")

	// Deterministic order: sort server names so the merger output is
	// stable across runs even when the input slice reorders.
	names := make([]string, 0, len(proj.MCP))
	servers := make(map[string]*model.MCPServer)
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		names = append(names, srv.Name)
		servers[srv.Name] = srv
	}
	sort.Strings(names)

	for _, name := range names {
		srv := servers[name]
		entry := windsurfMCPServerJSON{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
			Headers: srv.Headers,
		}
		if srv.Command == "" && srv.URL != "" {
			entry.URL = srv.URL
		}
		contributions[srv.Name] = entry
		if srv.ScopePath != "" {
			addSource(filepath.ToSlash(filepath.Join(srv.ScopePath, "mcp.yaml")))
			warnings = append(warnings, plugin.Warning{
				Source:   filepath.ToSlash(filepath.Join(srv.ScopePath, "mcp.yaml")),
				Message:  fmt.Sprintf("scoped MCP server %q from %s/mcp.yaml applied project-wide; Windsurf has no per-scope MCP", srv.Name, srv.ScopePath),
				Severity: "info",
			})
		}
	}

	// Canonical-path warning emits whenever any server is projected.
	warnings = append([]plugin.Warning{{
		Source:   "mcp.yaml",
		Message:  windsurfMCPCanonicalPathWarning,
		Severity: "info",
	}}, warnings...)

	merger := func(existing []byte) (string, error) {
		root := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &root); err != nil {
				return "", fmt.Errorf("windsurf: parsing existing %s: %w", relPath, err)
			}
		}
		mcpServers, _ := root["mcpServers"].(map[string]any)
		if mcpServers == nil {
			mcpServers = map[string]any{}
		}
		for _, name := range names {
			mcpServers[name] = contributions[name]
		}
		root["mcpServers"] = mcpServers

		raw, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return "", err
		}
		return string(raw) + "\n", nil
	}

	// Pre-render Content via the merger over empty bytes so plan-time
	// inspection (capability contract test, dry-run preview) reflects the
	// projected mcp_config.json. The engine ignores Content when Merger
	// is set, so production behavior is unchanged.
	preview, _ := merger(nil)

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     relPath,
		Mode:     plugin.ModeWrite,
		Content:  preview,
		Sources:  sources,
		Plugin:   "windsurf",
		Warnings: warnings,
		Merger:   merger,
	}, nil
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *WindsurfPlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that WindsurfPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*WindsurfPlugin)(nil)
