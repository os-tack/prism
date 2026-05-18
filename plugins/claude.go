// Package plugins contains the projection plugins that translate a canonical
// .agents/ model into per-tool config files.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/version"
)

// ClaudePlugin projects a model.Project into Claude Code's on-disk layout:
// CLAUDE.md (root + per-scope), .claude/skills/<name>/SKILL.md (+ scripts/),
// .claude/commands/<name>.md, .claude/agents/<name>.md, .claude/settings.json
// (permissions + hooks), and .mcp.json (MCP servers).
//
// Scoped capabilities (v0.5): skills with ScopePath are projected natively
// using a `<scopeSlug>-<name>` prefix to avoid name collisions across scopes;
// commands and agents are projected with the same prefix and a "degraded"
// warning (Claude Code has no native per-path command/agent scoping); hooks
// are projected via a generated `__scope-guard__` wrapper script that gates
// on $CLAUDE_TOOL_INPUT_FILE_PATH at runtime; permissions and MCP servers
// are merged into the global blocks with a degradation warning per scope.
type ClaudePlugin struct {
	// DisableHookWrappers, when true, projects scoped hooks as if they were
	// global (no `__scope-guard__` wrapper). Default false (wrappers ON).
	//
	// TODO(v0.6): wire CLI flag `--no-hook-wrappers` into this field via
	// plugin opts. For now this is exposed only as a struct field for
	// programmatic configuration in tests / library consumers.
	DisableHookWrappers bool
}

// NewClaude returns a fresh ClaudePlugin.
func NewClaude() *ClaudePlugin {
	return &ClaudePlugin{}
}

// Name is the stable plugin identifier.
func (p *ClaudePlugin) Name() string {
	return "claude"
}

// Detect returns true if a .claude/ directory or a CLAUDE.md file is present
// at the given project root.
func (p *ClaudePlugin) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".claude")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Capabilities returns the capability matrix entry for Claude Code.
//
// The v0.8 coarse fields (Context, ScopePaths, Skills, Commands, Agents,
// Hooks, Permissions, MCP) are retained for back-compat with older
// callers (validator and `prism capabilities` may still consult them).
//
// The v2 per-field tables (AgentFields, SkillFields, ...) declare every
// non-native cell from SPEC §12's Claude column. Native cells are
// absent from the Fields map and default to FieldNative per the
// FieldCapabilities contract. Field keys use the canonical
// dot-notation paths from SPEC §4.x.y per-plugin field-mapping tables
// (e.g. "Activation.Globs", "Auth.Scheme").
func (p *ClaudePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportDegraded, // no native trigger description; surfaced via IMPORTANT prefix
		Skills:        plugin.SupportNative,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportNative,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportNative,
		MCP:           plugin.SupportNative,

		// v2 per-primitive declarations (SPEC §12, "cla" column).
		AgentFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"claude"},
			Fields: map[string]plugin.FieldSupport{
				// Non-native cells from §12 Agent table.
				"ModelFallbacks": plugin.FieldSilent,
				"ReadOnly":       plugin.FieldDegraded, // → permissionMode: plan
				"Temperature":    plugin.FieldSilent,
				"UserInvocable":  plugin.FieldDegraded, // → non-scanned dir convention
				"ModelInvocable": plugin.FieldDegraded, // → permissions.deny rule
				"ScopePath":      plugin.FieldDegraded, // → <slug>-<name> prefix
			},
		},
		SkillFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"claude"},
			Fields: map[string]plugin.FieldSupport{
				// Non-native cells from §12 Skill table.
				"Activation.Modes.Always": plugin.FieldDegraded, // body persists; no native always
				"Activation.ContentRegex": plugin.FieldUnsupported,
				"ScopePath":               plugin.FieldDegraded,
			},
		},
		CommandFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"claude"},
			Fields: map[string]plugin.FieldSupport{
				"ScopePath": plugin.FieldDegraded,
			},
		},
		HookFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"claude"},
			Fields: map[string]plugin.FieldSupport{
				// Non-native cells from §12 Hook table.
				"Name":                     plugin.FieldSilent,
				"Description":              plugin.FieldSilent,
				"Sequential":               plugin.FieldSilent,
				"Cwd":                      plugin.FieldDegraded,
				"Env":                      plugin.FieldUnsupported,
				"Handlers.bash_powershell": plugin.FieldUnsupported,
				"FailClosed":               plugin.FieldUnsupported,
				"ScopePath":                plugin.FieldDegraded,
			},
		},
		MCPServerFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"claude"},
			Fields: map[string]plugin.FieldSupport{
				// Non-native cells from §12 MCPServer table.
				"Cwd":          plugin.FieldSilent,
				"Auth.Scheme":  plugin.FieldDegraded, // oauth → informational warn
				"TimeoutMs":    plugin.FieldUnsupported,
				"AutoApprove":  plugin.FieldUnsupported,
				"Trust":        plugin.FieldUnsupported,
				"IncludeTools": plugin.FieldUnsupported,
				"ExcludeTools": plugin.FieldUnsupported,
				"ScopePath":    plugin.FieldDegraded,
			},
		},
		PermissionsFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"claude"},
			Fields: map[string]plugin.FieldSupport{
				// "Global"/"Scoped" special keys per SPEC §6.2.
				"Scoped":         plugin.FieldDegraded,
				"fs":             plugin.FieldDegraded, // fans out to Edit+Read+Write
				"network":        plugin.FieldDegraded, // → WebFetch(domain:...)
				"mcp":            plugin.FieldDegraded, // → mcp__server__tool
				"recursive_glob": plugin.FieldDegraded, // **; deny-only fallback
				"negation":       plugin.FieldDegraded, // ! splits to allow+deny
			},
		},
		ScopeFields: plugin.FieldCapabilities{
			Supported:  true,
			Extensions: []string{"claude"},
			Fields: map[string]plugin.FieldSupport{
				// Non-native cells from §12 Scope table.
				"Name":                     plugin.FieldDegraded,
				"Description":              plugin.FieldSilent,
				"Activation.Always":        plugin.FieldDegraded, // root-only implicit
				"Activation.Manual":        plugin.FieldUnsupported,
				"Activation.ModelDecision": plugin.FieldUnsupported,
				"Priority":                 plugin.FieldDegraded, // synthesized as cascade depth
				"Tags":                     plugin.FieldSilent,
				"IsOverride":               plugin.FieldUnsupported, // leading comment fallback
			},
		},
	}
}

// scopeSlug converts a scope path (e.g. "src/billing") to a filesystem-safe
// slug used as a prefix for scoped artifacts (e.g. "src-billing"). Empty
// scope path returns the empty string.
func scopeSlug(scopePath string) string {
	if scopePath == "" {
		return ""
	}
	return strings.ReplaceAll(scopePath, "/", "-")
}

// scopedName returns the projected artifact name for a scoped capability,
// prefixed by the scope slug. For global capabilities (empty scopePath) it
// returns name unchanged.
func scopedName(scopePath, name string) string {
	if scopePath == "" {
		return name
	}
	return scopeSlug(scopePath) + "-" + name
}

// Plan produces the Operations needed to project proj into Claude Code's layout.
//
// Context + scopes → CLAUDE.md (symlink by default, write per opts.Mode).
// Skills → .claude/skills/<name>/SKILL.md (+ scripts/<basename>).
// Commands → .claude/commands/<name>.md.
// Agents → .claude/agents/<name>.md.
// Hooks + Permissions → merged into .claude/settings.json (always write).
// MCP servers → merged into .mcp.json (always write).
//
// Scoped variants:
//
//	Skill   (ScopePath != "")  → .claude/skills/<scopeSlug>-<name>/SKILL.md (Native)
//	Command (ScopePath != "")  → .claude/commands/<scopeSlug>-<name>.md     (Degrade)
//	Agent   (ScopePath != "")  → .claude/agents/<scopeSlug>-<name>.md       (Degrade)
//	Hook    (ScopePath != "")  → wrapper at .claude/hooks/__scope-guard__/
//	                             <scopeSlug>-<hookname>.sh; settings.json
//	                             command points at wrapper (Wrapper script)
//	Permissions (per scope)    → merged into global allow/deny/ask (Degrade)
//	MCPServer (ScopePath!="")  → merged into global .mcp.json     (Degrade)
func (p *ClaudePlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	mode := plugin.ModeSymlink
	switch opts.Mode {
	case "write":
		mode = plugin.ModeWrite
	case "symlink", "":
		mode = plugin.ModeSymlink
	default:
		return nil, fmt.Errorf("claude: unknown mode %q (want \"write\" or \"symlink\")", opts.Mode)
	}

	if proj == nil {
		return nil, nil
	}

	var ops []plugin.Operation

	// Root CLAUDE.md.
	if proj.Context != nil {
		op, err := buildOp(proj, proj.Context, "CLAUDE.md", mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Per-scope CLAUDE.md files. Pass extensions.claude.* through. Scopes
	// with non-empty Globs additionally project as `.claude/rules/<name>.md`
	// below (the cascade form is preserved for filesystem-anchored scopes).
	for _, sc := range proj.Scopes {
		if sc == nil || sc.Document == nil {
			continue
		}
		// Skip the cascade emission when the scope is glob-only (no Path).
		if sc.Path == "" && len(sc.Globs) > 0 {
			continue
		}
		path := filepath.Join(sc.Path, "CLAUDE.md")
		op, err := buildOpExt(proj, sc.Document, path, mode, claudeExtensions(sc.Extensions))
		if err != nil {
			return nil, err
		}
		// Degradation warnings for non-native scope fields (Name,
		// Priority, IsOverride). Tags are silent per SPEC §4.7.4.
		if sc.Name != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(sc.Document.SourcePath),
				Message:  fmt.Sprintf("scope Name=%q degraded on Claude (filename-derived; no `name:` frontmatter)", sc.Name),
				Severity: "info",
			})
		}
		if sc.Priority != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(sc.Document.SourcePath),
				Message:  fmt.Sprintf("scope Priority=%s degraded on Claude (approximated via cascade depth; no native priority key)", sc.Priority),
				Severity: "info",
			})
		}
		if sc.IsOverride {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(sc.Document.SourcePath),
				Message:  fmt.Sprintf("scope IsOverride unsupported on Claude (no native override semantic; emitted as leading HTML comment in scope %q)", sc.Path),
				Severity: "warn",
			})
		}
		ops = append(ops, op)
	}

	// Per-scope rule files (frontmatter-globbed). Scopes with non-empty
	// Globs and a glob-style activation project to
	// `.claude/rules/<name>.md` with a `paths:` key in frontmatter so
	// Claude can match the rule at runtime. Path-cascaded scopes (Globs
	// empty OR Activation=Cascade) continue to project as
	// `<scope>/CLAUDE.md` per the cascade above.
	for _, sc := range proj.Scopes {
		if sc == nil || sc.Document == nil {
			continue
		}
		if len(sc.Globs) == 0 {
			continue
		}
		// Only EXPLICITLY glob-active scopes project as rule files;
		// cascade scopes (including those with empty Activation, which
		// defaults to cascade per SPEC §4.7.2) already projected above
		// as <Path>/CLAUDE.md.
		if sc.Activation != model.ScopeActivationGlob {
			continue
		}
		name := sc.Name
		if name == "" {
			// Fall back to a deterministic slug from the path.
			name = strings.ReplaceAll(strings.Trim(sc.Path, "/"), "/", "-")
		}
		if name == "" {
			continue
		}
		path := filepath.Join(".claude", "rules", name+".md")
		synth := map[string]any{
			"paths": sc.Globs,
		}
		op, err := buildOpExtSynth(proj, sc.Document, path, mode, synth, claudeExtensions(sc.Extensions))
		if err != nil {
			return nil, err
		}
		if sc.IsOverride {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(sc.Document.SourcePath),
				Message:  fmt.Sprintf("scope %q IsOverride degraded on Claude (emitted as leading HTML comment; no native override semantic)", name),
				Severity: "warn",
			})
		}
		if sc.Name != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(sc.Document.SourcePath),
				Message:  fmt.Sprintf("scope %q Name degraded on Claude (filename-derived; no `name:` frontmatter key)", sc.Name),
				Severity: "info",
			})
		}
		if sc.Priority != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(sc.Document.SourcePath),
				Message:  fmt.Sprintf("scope priority=%s degraded on Claude (cascade depth approximation; no native priority key)", sc.Priority),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Skills (global + scoped). Scoped skills project natively under
	// `.claude/skills/<scopeSlug>-<name>/SKILL.md`. v2 frontmatter
	// synthesis (paths, allowed-tools, arguments, when_to_use, model,
	// context/agent) is folded with the source's existing frontmatter
	// and any `extensions.claude.*` pass-through.
	for _, sk := range proj.Skills {
		if sk == nil || sk.Document == nil {
			continue
		}
		dirName := scopedName(sk.ScopePath, sk.Name)
		skillDir := filepath.Join(".claude", "skills", dirName)
		skillPath := filepath.Join(skillDir, "SKILL.md")
		synth, skillWarnings := synthClaudeSkillFrontmatter(proj, sk)
		op, err := buildOpExtSynth(proj, sk.Document, skillPath, mode, synth, claudeExtensions(sk.Extensions))
		if err != nil {
			return nil, err
		}
		op.Warnings = append(op.Warnings, skillWarnings...)
		if sk.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(sk.Document.SourcePath),
				Message:  fmt.Sprintf("scoped skill %q projected with scopepath %q via <slug>-<name> dir prefix (Claude skills are global)", sk.Name, sk.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)

		// Each script becomes a symlink under scripts/<basename>.
		for _, scriptPath := range sk.Scripts {
			if scriptPath == "" {
				continue
			}
			scriptOp, err := buildScriptOp(proj, scriptPath, skillDir, mode)
			if err != nil {
				return nil, err
			}
			ops = append(ops, scriptOp)
		}
	}

	// Commands (global + scoped). Scoped commands are degraded — Claude
	// Code commands are global, so we prefix the projected name and emit
	// an info warning explaining the loss of path enforcement.
	for _, cmd := range proj.Commands {
		if cmd == nil || cmd.Document == nil {
			continue
		}
		fileName := scopedName(cmd.ScopePath, cmd.Name) + ".md"
		path := filepath.Join(".claude", "commands", fileName)
		synth := synthClaudeCommandFrontmatter(cmd)
		op, err := buildOpExtSynth(proj, cmd.Document, path, mode, synth, claudeExtensions(cmd.Extensions))
		if err != nil {
			return nil, err
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(cmd.Document.SourcePath),
				Message:  fmt.Sprintf("scoped command %q (scopepath %s) projected without path enforcement (Claude commands are global)", cmd.Name, cmd.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Agents (global + scoped). Scoped agents are degraded — same rationale
	// as commands above.
	for _, ag := range proj.Agents {
		if ag == nil || ag.Document == nil {
			continue
		}
		fileName := scopedName(ag.ScopePath, ag.Name) + ".md"
		path := filepath.Join(".claude", "agents", fileName)
		synth, agentWarnings := synthClaudeAgentFrontmatter(proj, ag)
		op, err := buildOpExtSynth(proj, ag.Document, path, mode, synth, claudeExtensions(ag.Extensions))
		if err != nil {
			return nil, err
		}
		op.Warnings = append(op.Warnings, agentWarnings...)
		if ag.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(ag.Document.SourcePath),
				Message:  fmt.Sprintf("scoped agent %q (scopepath %s) projected without path enforcement (Claude agents are global)", ag.Name, ag.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (must be emitted before the settings op
	// so the settings op can reference the wrapper's absolute path).
	// Each scoped hook gets its own wrapper script under
	// `.claude/hooks/__scope-guard__/`. The wrapper inspects
	// $CLAUDE_TOOL_INPUT_FILE_PATH at runtime and only exec's the source
	// script when the path is under the hook's scope.
	wrapperPaths := map[*model.Hook]string{} // hook → absolute wrapper path
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		// Use the script's basename (sans extension) as the hook name
		// component. This keeps wrapper file names stable across renames
		// of unrelated hooks.
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + hookName + ".sh"
		wrapperRel := filepath.Join(".claude", "hooks", "__scope-guard__", wrapperFile)
		wrapperAbs := filepath.Join(proj.Root, wrapperRel)

		body := buildScopeGuardScript(wrapperRel, h.ScopePath, h.ScriptPath, formatScriptArg(h.ScriptPath, proj.Root))
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     wrapperRel,
			Content:  body,
			Mode:     plugin.ModeWrite, // ModeHook semantics not wired yet; treat as write
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   "claude",
		})
		wrapperPaths[h] = wrapperAbs
	}

	// .claude/settings.json (hooks + permissions). Always write. We emit
	// settings.json whenever there is any global or scoped permission, or
	// any hook (global or scoped).
	hasPerms := proj.Permissions != nil || len(proj.ScopedPermissions) > 0
	if hasPerms || len(proj.Hooks) > 0 {
		settingsOp, err := buildSettingsOp(proj, wrapperPaths)
		if err != nil {
			return nil, err
		}
		ops = append(ops, settingsOp)
	}

	// .mcp.json (MCP servers). Always write.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	return ops, nil
}

// buildScopeGuardScript renders the wrapper-script body that gates a
// scoped Claude hook on the file-path scope at runtime.
//
// Claude Code invokes hook scripts with the tool input as JSON on stdin
// (see https://code.claude.com/docs/en/hooks). Earlier prism versions
// gated on a non-existent $CLAUDE_TOOL_INPUT_FILE_PATH env var; the
// real contract is JSON, so the wrapper now exec's `prism scope-guard
// --scope <path> --script <abs>`, which parses the JSON and either
// invokes the source script (passing stdin through) or exits 0.
//
// wrapperRel is the project-relative path of the wrapper itself; we
// embed neither the project root nor any absolute path into the
// rendered bash. At runtime the wrapper resolves the project root from
// ${BASH_SOURCE[0]} (with PRISM_PROJECT_DIR and CLAUDE_PROJECT_DIR
// taking precedence), so the wrapper survives `mv` of the project (I4).
// sourceScript is the absolute path to the user's authored hook (used
// only for the comment header / filename basename). scriptArg is the
// pre-quoted shell argument for --script, formatted at the call site
// via formatScriptArg so that scripts under proj.Root are rewritten to
// "${PROJECT_DIR}"/<rel> — the wrapper survives `mv` of the project
// for both PROJECT_DIR resolution and the --script reference (fixes
// I-1 from v0.7.1 review).
//
// The wrapper requires `prism` on PATH at hook-firing time. That's a
// reasonable assumption since the user installed prism to project the
// hook in the first place.
func buildScopeGuardScript(wrapperRel, scopePath, sourceScript, scriptArg string) string {
	upDots := rootRelativeFromWrapper(wrapperRel)
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# prism-generated scope guard for ")
	b.WriteString(sanitizeBashComment(scopePath))
	b.WriteString("/hooks/")
	b.WriteString(sanitizeBashComment(strings.TrimSuffix(filepath.Base(sourceScript), filepath.Ext(sourceScript))))
	b.WriteString(".yaml\n")
	b.WriteString("#\n")
	b.WriteString("# Reads Claude Code's hook JSON from stdin, dispatches to the source\n")
	b.WriteString("# script when tool_input.file_path falls under the scope.\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("SCRIPT_DIR=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"\n")
	b.WriteString("PROJECT_DIR=\"${PRISM_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$(cd \"${SCRIPT_DIR}/")
	b.WriteString(upDots)
	b.WriteString("\" && pwd)}}\"\n")
	b.WriteString("export CLAUDE_PROJECT_DIR=\"${CLAUDE_PROJECT_DIR:-${PROJECT_DIR}}\"\n")
	b.WriteString("exec prism scope-guard --scope ")
	b.WriteString(shellQuote(scopePath))
	b.WriteString(" --script ")
	b.WriteString(scriptArg)
	b.WriteString("\n")
	return b.String()
}

// shellQuote single-quotes a string for safe shell interpolation. Single
// quotes inside the input are emitted as '\'\'.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sanitizeBashComment replaces characters that would break a `# ...` line
// in the wrapper preamble. Newlines and carriage returns become `?` so a
// scope path or filename containing them does not split the comment into
// a second line that bash would interpret (N-b from v0.7.1 review).
func sanitizeBashComment(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	r := strings.NewReplacer("\n", "?", "\r", "?")
	return r.Replace(s)
}

// buildOp constructs a single Operation for a Document being projected to the
// given target path (relative to project root) in the given Mode.
//
// When doc.NeedsWrite() is true (the document had @include directives
// expanded into its body) symlink mode is downgraded to write mode and
// an info-severity warning is attached describing the reason. Every
// included file's source tag is appended to op.Sources so lockfile /
// `agents which` traces flow back to the included content too.
func buildOp(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode) (plugin.Operation, error) {
	return buildOpExt(proj, doc, targetPath, mode, nil)
}

// buildOpExt is buildOp + an optional extensions map sourced from the
// primitive's Extensions["claude"]. When the map is non-empty its keys
// are hoisted to top-level frontmatter on the projected file. Hoisting
// forces a write-mode emission (no symlink): the projected file must
// carry the merged frontmatter, which differs from the on-disk source.
// When the map is empty/nil this is exactly buildOp.
//
// Hoisting policy (SPEC §5.1, §12 "Extensions[plugin] = N"): the v2
// contract says extensions.claude.* passes through verbatim to the
// projected frontmatter. Keys that collide with an existing frontmatter
// key are preserved at top level (extension wins); the original keys
// remain alongside any non-colliding ones. Plugins MUST NOT interpret
// the values — they are opaque pass-through.
func buildOpExt(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode, claudeExt map[string]any) (plugin.Operation, error) {
	downgraded := false
	hoist := len(claudeExt) > 0
	if (doc.NeedsWrite() || hoist) && mode == plugin.ModeSymlink {
		mode = plugin.ModeWrite
		if doc.NeedsWrite() {
			downgraded = true
		}
	}

	sources := []string{proj.SourceTag(doc.SourcePath)}
	for _, inc := range doc.Includes {
		sources = append(sources, proj.SourceTag(inc))
	}

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: sources,
		Plugin:  "claude",
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
		if hoist {
			op.Content = renderBodyWithExtensions(doc, claudeExt)
		} else {
			op.Content = doc.Body
		}
	}

	if downgraded {
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   proj.SourceTag(doc.SourcePath),
			Message:  "downgraded to write mode: contains @include directives",
			Severity: "info",
		})
	}

	return op, nil
}

// renderBodyWithExtensions hoists claudeExt keys into the projected
// frontmatter on top of doc's existing frontmatter, then concatenates
// doc.Body. Existing frontmatter keys not present in claudeExt are
// preserved; colliding keys take the extension value (extensions are
// authored intentionally to override). Returns "---\n…\n---\n<body>".
//
// Marshaling uses encoding/json with stable key order: existing
// frontmatter keys first (sorted), then extension-only keys (sorted).
// JSON encoding produces valid YAML flow scalars/maps for the value
// types extensions carry (string, number, bool, list, map). Plugins
// never invent values — they only stream what the parser already
// validated as YAML, so a round-trip through JSON cannot lose
// precision for the legal extension value types.
func renderBodyWithExtensions(doc *model.Document, claudeExt map[string]any) string {
	merged := map[string]any{}
	for k, v := range doc.Frontmatter {
		merged[k] = v
	}
	for k, v := range claudeExt {
		merged[k] = v
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		raw, err := json.Marshal(merged[k])
		if err != nil {
			// Fall back to an empty value rather than dropping the key
			// silently; the projected file remains valid YAML and the
			// user can inspect the discrepancy via `prism diff`.
			b.WriteString(k)
			b.WriteString(": \"\"\n")
			continue
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.Write(raw)
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	body := doc.Body
	if body == "" {
		return b.String()
	}
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

// claudeExtensions extracts the `claude` namespace from a primitive's
// Extensions map, returning nil when absent or shaped wrong. Used by the
// Plan() call sites for Agent/Skill/Command/Scope to feed buildOpExt.
func claudeExtensions(ext map[string]any) map[string]any {
	if ext == nil {
		return nil
	}
	v, ok := ext["claude"].(map[string]any)
	if !ok {
		return nil
	}
	if len(v) == 0 {
		return nil
	}
	return v
}

// buildOpExtSynth is buildOpExt with a v2-synthesized frontmatter layer
// merged underneath the extensions.claude.* hoist. Synth keys are
// populated by the per-primitive helpers (synthClaudeAgentFrontmatter,
// synthClaudeSkillFrontmatter, synthClaudeCommandFrontmatter). Layering
// order (later wins): Document.Frontmatter, synth, extensions.claude.*.
// When both synth and claudeExt are empty this collapses to buildOpExt.
func buildOpExtSynth(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode, synth, claudeExt map[string]any) (plugin.Operation, error) {
	if len(synth) == 0 {
		return buildOpExt(proj, doc, targetPath, mode, claudeExt)
	}
	// Synthesized frontmatter forces write mode (we mutate the body).
	merged := map[string]any{}
	for k, v := range synth {
		merged[k] = v
	}
	for k, v := range claudeExt {
		merged[k] = v
	}
	downgraded := doc.NeedsWrite()
	if mode == plugin.ModeSymlink {
		mode = plugin.ModeWrite
	}
	sources := []string{proj.SourceTag(doc.SourcePath)}
	for _, inc := range doc.Includes {
		sources = append(sources, proj.SourceTag(inc))
	}
	op := plugin.Operation{
		Kind:    plugin.OpWrite,
		Path:    targetPath,
		Mode:    mode,
		Sources: sources,
		Plugin:  "claude",
		Content: renderBodyWithExtensions(doc, merged),
	}
	if downgraded {
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   proj.SourceTag(doc.SourcePath),
			Message:  "downgraded to write mode: contains @include directives",
			Severity: "info",
		})
	}
	return op, nil
}

// synthClaudeAgentFrontmatter renders the v2 Agent-shape frontmatter
// Claude understands (SPEC §4.1.4 / §12 cla column). Returns the
// synthesized keys plus warnings for non-native fields (ReadOnly,
// UserInvocable, ModelInvocable degrade with info warnings; nothing
// else triggers a warning at the agent layer — extensions take care of
// arbitrary overrides).
//
// Layout:
//   - model:               from Agent.Model
//   - tools:               Agent.Tools + Agent(name) entries from AllowedSubagents
//   - disallowedTools:     from Agent.DisallowedTools
//   - permissionMode: plan when *Agent.ReadOnly == true (degraded)
//   - background: true     when *Agent.Background == true
//   - maxTurns:            from *Agent.MaxTurns
//   - initialPrompt:       from Agent.InitialPrompt
//   - mcpServers:          name list from Agent.MCPServers
func synthClaudeAgentFrontmatter(proj *model.Project, ag *model.Agent) (map[string]any, []plugin.Warning) {
	if ag == nil {
		return nil, nil
	}
	out := map[string]any{}
	var warnings []plugin.Warning
	if ag.Model != "" {
		out["model"] = ag.Model
	}
	tools := append([]string(nil), ag.Tools...)
	for _, sub := range ag.AllowedSubagents {
		if sub == "" {
			continue
		}
		tools = append(tools, "Agent("+sub+")")
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	if len(ag.DisallowedTools) > 0 {
		out["disallowedTools"] = ag.DisallowedTools
	}
	if ag.ReadOnly != nil && *ag.ReadOnly {
		out["permissionMode"] = "plan"
		warnings = append(warnings, plugin.Warning{
			Source:   proj.SourceTag(ag.Document.SourcePath),
			Message:  fmt.Sprintf("agent %q ReadOnly degraded to permissionMode: plan on Claude (no native readonly flag)", ag.Name),
			Severity: "info",
		})
	}
	if ag.Background != nil && *ag.Background {
		out["background"] = true
	}
	if ag.MaxTurns != nil {
		out["maxTurns"] = *ag.MaxTurns
	}
	if ag.InitialPrompt != "" {
		out["initialPrompt"] = ag.InitialPrompt
	}
	if ag.UserInvocable != nil {
		warnings = append(warnings, plugin.Warning{
			Source:   proj.SourceTag(ag.Document.SourcePath),
			Message:  fmt.Sprintf("agent %q UserInvocable=%v degraded on Claude (no native flag; convention is to move the agent file under a non-scanned dir to suppress invocation)", ag.Name, *ag.UserInvocable),
			Severity: "info",
		})
	}
	if ag.ModelInvocable != nil {
		warnings = append(warnings, plugin.Warning{
			Source:   proj.SourceTag(ag.Document.SourcePath),
			Message:  fmt.Sprintf("agent %q ModelInvocable=%v degraded on Claude (no native flag; emit a permissions.deny rule on the agent name to suppress model invocation)", ag.Name, *ag.ModelInvocable),
			Severity: "info",
		})
	}
	if len(ag.MCPServers) > 0 {
		names := make([]string, 0, len(ag.MCPServers))
		for _, ref := range ag.MCPServers {
			if ref.Name != "" {
				names = append(names, ref.Name)
			}
		}
		if len(names) > 0 {
			out["mcpServers"] = names
		}
	}
	return out, warnings
}

// synthClaudeSkillFrontmatter renders the v2 Skill-shape frontmatter
// Claude understands (SPEC §4.2.5).
//
// Layout:
//   - paths:                from Activation.Globs (falls back to Skill.Globs)
//   - allowed-tools:        from AllowedTools
//   - arguments:            name list from Arguments
//   - when_to_use:          from WhenToUse
//   - model:                from Model
//   - context: fork, agent: <subagent> when Subagent != ""
//   - user-invocable:       from Activation.UserInvocable
//   - disable-model-invocation: true when Activation.ModelInvocable == false
//
// Warnings: Activation.ContentRegex is unsupported on Claude;
// Activation.Modes.Always degrades (body persists; no native always).
func synthClaudeSkillFrontmatter(proj *model.Project, sk *model.Skill) (map[string]any, []plugin.Warning) {
	if sk == nil {
		return nil, nil
	}
	out := map[string]any{}
	var warnings []plugin.Warning

	globs := sk.Activation.Globs
	if len(globs) == 0 {
		globs = sk.Globs
	}
	if len(globs) > 0 {
		out["paths"] = globs
	}
	if len(sk.AllowedTools) > 0 {
		out["allowed-tools"] = sk.AllowedTools
	}
	if len(sk.Arguments) > 0 {
		names := make([]string, 0, len(sk.Arguments))
		for _, a := range sk.Arguments {
			if a.Name != "" {
				names = append(names, a.Name)
			}
		}
		if len(names) > 0 {
			out["arguments"] = names
		}
	}
	if sk.WhenToUse != "" {
		out["when_to_use"] = sk.WhenToUse
	}
	if sk.Model != "" {
		out["model"] = sk.Model
	}
	if sk.Subagent != "" {
		out["context"] = "fork"
		out["agent"] = sk.Subagent
	}
	if sk.Activation.UserInvocable != nil && *sk.Activation.UserInvocable {
		out["user-invocable"] = true
	}
	if sk.Activation.ModelInvocable != nil && !*sk.Activation.ModelInvocable {
		out["disable-model-invocation"] = true
	}
	if sk.Activation.ContentRegex != "" {
		warnings = append(warnings, plugin.Warning{
			Source:   proj.SourceTag(sk.Document.SourcePath),
			Message:  fmt.Sprintf("skill %q Activation.ContentRegex unsupported by Claude (no content-regex activation; drop or move to a custom hook)", sk.Name),
			Severity: "warn",
		})
	}
	for _, m := range sk.Activation.Modes {
		if m == model.SkillActivationAlways {
			warnings = append(warnings, plugin.Warning{
				Source:   proj.SourceTag(sk.Document.SourcePath),
				Message:  fmt.Sprintf("skill %q Activation.Modes.Always degraded on Claude (body persists; no native always)", sk.Name),
				Severity: "info",
			})
		}
	}
	return out, warnings
}

// synthClaudeCommandFrontmatter renders the v2 Command-shape frontmatter
// Claude understands (SPEC §4.3.4).
//
// Layout:
//   - description:                from Command.Description
//   - argument-hint:              from ArgumentHint
//   - arguments:                  list from Arguments
//   - allowed-tools:              from Tools
//   - model:                      from Model
//   - agent:                      from Agent (when set; emits context: fork)
//   - disable-model-invocation:   true when AutoInvoke == false
func synthClaudeCommandFrontmatter(cmd *model.Command) map[string]any {
	if cmd == nil {
		return nil
	}
	out := map[string]any{}
	if cmd.Description != "" {
		out["description"] = cmd.Description
	}
	if cmd.ArgumentHint != "" {
		out["argument-hint"] = cmd.ArgumentHint
	}
	if len(cmd.Arguments) > 0 {
		out["arguments"] = cmd.Arguments
	}
	if len(cmd.Tools) > 0 {
		out["allowed-tools"] = cmd.Tools
	}
	if cmd.Model != "" {
		out["model"] = cmd.Model
	}
	if cmd.Agent != "" {
		out["agent"] = cmd.Agent
		out["context"] = "fork"
	}
	if !cmd.AutoInvoke {
		out["disable-model-invocation"] = true
	}
	return out
}

// buildScriptOp constructs an Operation that places a skill script under
// `<skillDir>/scripts/<basename>`. Scripts are symlinks (or copies in write
// mode) pointing at the original absolute path in .agents/.
func buildScriptOp(proj *model.Project, scriptPath, skillDir string, mode plugin.Mode) (plugin.Operation, error) {
	base := filepath.Base(scriptPath)
	targetPath := filepath.Join(skillDir, "scripts", base)

	srcRel := proj.SourceTag(scriptPath)

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: []string{srcRel},
		Plugin:  "claude",
	}

	if mode == plugin.ModeSymlink {
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, scriptPath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.Kind = plugin.OpSymlink
		op.LinkTarget = filepath.ToSlash(linkTarget)
	} else {
		// In write mode we still emit a symlink — engine may downgrade later.
		// We don't read the script bytes here because plugins are pure.
		op.Kind = plugin.OpSymlink
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, scriptPath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.LinkTarget = filepath.ToSlash(linkTarget)
	}

	return op, nil
}

// hookEntry mirrors Claude Code's settings.json hook schema:
//
//	{"type": "command", "command": "<absolute-script-path>"}
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// hookGroup mirrors Claude Code's settings.json hook group schema:
//
//	{"matcher": "...", "hooks": [{"type": "command", "command": "..."}]}
type hookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// buildSettingsOp emits the OpMerge for .claude/settings.json. The op carries
// a Merger closure that the engine invokes with the file's existing bytes;
// Plan() itself does no filesystem I/O so plugin behavior is reproducible
// from (proj, wrapperPaths) alone.
//
// Permissions are fanned out through the shared canonical permission grammar
// (`plugins/perms_grammar.go`): `fs:` expands to Edit/Read/Write triples,
// `network:` becomes `WebFetch(domain:...)`, `mcp:` becomes
// `mcp__<server>__<tool>`, and a leading `!` routes the rule to the Deny
// bucket. Hooks are serialized through the shared Claude-shape emitter
// (`plugins/hooks_claude_shape.go`) so per-action canonical events translate
// to (PreToolUse/PostToolUse + matcher) and typed Handlers materialize.
func buildSettingsOp(proj *model.Project, wrapperPaths map[*model.Hook]string) (plugin.Operation, error) {
	var warnings []plugin.Warning

	allow, deny, ask, permWarnings := claudePermissionEntries(proj)
	warnings = append(warnings, permWarnings...)

	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   sp.ScopePath + "/permissions.yaml",
			Message:  fmt.Sprintf("permissions from %s/permissions.yaml applied project-wide; Claude Code has no per-scope permissions", sp.ScopePath),
			Severity: "info",
		})
	}

	// Hook warnings for non-native handler fields (Cwd, Env, FailClosed) and
	// scoped hooks (ScopePath). Emitted regardless of wrapper materialization.
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		if h.ScopePath != "" {
			warnings = append(warnings, plugin.Warning{
				Source:   proj.SourceTag(h.ScriptPath),
				Message:  fmt.Sprintf("scoped hook (scopepath %s) projected with __scope-guard__ wrapper; Claude Code has no per-scope hooks", h.ScopePath),
				Severity: "info",
			})
		}
		for _, hd := range h.Handlers {
			if hd.Cwd != "" {
				warnings = append(warnings, plugin.Warning{
					Source:   proj.SourceTag(h.ScriptPath),
					Message:  fmt.Sprintf("hook handler cwd=%q not natively honored by Claude (degraded; wrap the command in a `cd && exec` shell script)", hd.Cwd),
					Severity: "info",
				})
			}
			if len(hd.Env) > 0 {
				envKeys := make([]string, 0, len(hd.Env))
				for k := range hd.Env {
					envKeys = append(envKeys, k)
				}
				sort.Strings(envKeys)
				warnings = append(warnings, plugin.Warning{
					Source:   proj.SourceTag(h.ScriptPath),
					Message:  fmt.Sprintf("hook handler env=%v unsupported by Claude (drop or wrap in a shell script)", envKeys),
					Severity: "warn",
				})
			}
			if hd.FailClosed {
				warnings = append(warnings, plugin.Warning{
					Source:   proj.SourceTag(h.ScriptPath),
					Message:  "hook handler FailClosed unsupported by Claude (Claude treats non-block exit codes as fail-open; wrap in a shell script that maps non-zero to a block-exit code)",
					Severity: "warn",
				})
			}
		}
	}

	// Render hooks via the shared Claude-shape serializer so per-action
	// canonical events translate to (generic + matcher) form and typed
	// Handlers (HTTP/Prompt/Command) materialize correctly.
	hookGroups := ClaudeShapeHooks(proj.Hooks, "claude")
	// Apply wrapper-path substitution: scoped hooks that materialized a
	// __scope-guard__ wrapper should point at that wrapper's absolute path
	// rather than the user's source script (so the gate fires at hook
	// invocation time). We map by source command path here.
	wrapperByScript := map[string]string{}
	for h, wp := range wrapperPaths {
		if h == nil {
			continue
		}
		wrapperByScript[h.ScriptPath] = wp
	}
	if len(wrapperByScript) > 0 {
		for event, groups := range hookGroups {
			for gi := range groups {
				for hi := range groups[gi].Hooks {
					if w, ok := wrapperByScript[groups[gi].Hooks[hi].Command]; ok {
						groups[gi].Hooks[hi].Command = w
					}
				}
			}
			hookGroups[event] = groups
		}
	}

	relPath := filepath.Join(".claude", "settings.json")

	merger := func(existing []byte) (string, error) {
		settings := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &settings); err != nil {
				return "", fmt.Errorf("claude: parsing existing %s: %w", relPath, err)
			}
		}

		if len(allow) > 0 || len(deny) > 0 || len(ask) > 0 {
			perms, _ := settings["permissions"].(map[string]any)
			if perms == nil {
				perms = map[string]any{}
			}
			if len(allow) > 0 {
				perms["allow"] = allow
			}
			if len(deny) > 0 {
				perms["deny"] = deny
			}
			if len(ask) > 0 {
				perms["ask"] = ask
			}
			settings["permissions"] = perms
		}

		if len(hookGroups) > 0 {
			hooksRoot, _ := settings["hooks"].(map[string]any)
			if hooksRoot == nil {
				hooksRoot = map[string]any{}
			}
			for event, groups := range hookGroups {
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
	if proj.Permissions != nil {
		sources = append(sources, "permissions.yaml")
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		sources = append(sources, sp.ScopePath+"/permissions.yaml")
	}
	if len(proj.Hooks) > 0 {
		sources = append(sources, "hooks.yaml")
	}

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     relPath,
		Mode:     plugin.ModeWrite,
		Sources:  sources,
		Plugin:   "claude",
		Warnings: warnings,
		Merger:   merger,
	}, nil
}

// claudePermissionEntries fans out canonical Permission rules (SPEC §4.6)
// into Claude's wire form. Uses the shared `ParsePermRule` + `FSFanOut` +
// `ClaudeRuleForm` grammar so emit-side semantics stay in sync with every
// other plugin: `fs:src/**` allow → three Allow rules (Edit/Read/Write);
// `network:github.com` → `WebFetch(domain:github.com)`; `mcp:srv:tool` →
// `mcp__srv__tool`; a leading `!` reroutes the entry to the Deny bucket;
// `**` recursive globs pass through to Claude's native glob dialect.
//
// Warnings: degraded fan-outs (fs:, network:, mcp:, ** preservation,
// negation routing) emit an "info" Warning per rule per category so the
// per-field capability contract (§12) round-trips.
func claudePermissionEntries(proj *model.Project) (allow, deny, ask []string, warnings []plugin.Warning) {
	allow = []string{}
	deny = []string{}
	ask = []string{}
	if proj.Permissions != nil {
		a, d, k, w := claudeRuleBucket(proj.Permissions.Allow, "allow", "")
		allow = append(allow, a...)
		deny = append(deny, d...)
		ask = append(ask, k...)
		warnings = append(warnings, w...)
		_, d2, _, w2 := claudeRuleBucket(proj.Permissions.Deny, "deny", "")
		deny = append(deny, d2...)
		warnings = append(warnings, w2...)
		_, _, k2, w3 := claudeRuleBucket(proj.Permissions.Ask, "ask", "")
		ask = append(ask, k2...)
		warnings = append(warnings, w3...)
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		a, d, k, w := claudeRuleBucket(sp.Allow, "allow", sp.ScopePath)
		allow = append(allow, a...)
		deny = append(deny, d...)
		ask = append(ask, k...)
		warnings = append(warnings, w...)
		_, d2, _, w2 := claudeRuleBucket(sp.Deny, "deny", sp.ScopePath)
		deny = append(deny, d2...)
		warnings = append(warnings, w2...)
		_, _, k2, w3 := claudeRuleBucket(sp.Ask, "ask", sp.ScopePath)
		ask = append(ask, k2...)
		warnings = append(warnings, w3...)
	}
	allow = dedupeStrings(allow)
	deny = dedupeStrings(deny)
	ask = dedupeStrings(ask)
	return allow, deny, ask, warnings
}

// claudeRuleBucket parses each rule string and emits the appropriate
// Claude wire-form entries plus any degradation warnings. bucket names
// the source bucket ("allow"/"deny"/"ask") so warnings can be specific.
// Returns (allow, deny, ask, warnings); negated rules in the Allow
// bucket flip to Deny entries.
func claudeRuleBucket(rules []string, bucket, scopePath string) (allow, deny, ask []string, warnings []plugin.Warning) {
	source := "permissions.yaml"
	if scopePath != "" {
		source = scopePath + "/permissions.yaml"
	}
	for _, rule := range rules {
		pr := ParsePermRule(rule)
		// fs: rule fan-out → three FS entries.
		if pr.Target == PermTargetFS {
			warnings = append(warnings, plugin.Warning{
				Source:   source,
				Message:  fmt.Sprintf("fs rule %q fanned out to Edit/Read/Write on Claude (no native fs: target)", rule),
				Severity: "info",
			})
		}
		// network: → WebFetch(domain:...).
		if pr.Target == PermTargetNetwork {
			warnings = append(warnings, plugin.Warning{
				Source:   source,
				Message:  fmt.Sprintf("network rule %q projected as WebFetch(domain:%s) on Claude (no native network: target)", rule, pr.Pattern),
				Severity: "info",
			})
		}
		// mcp: → mcp__server__tool.
		if pr.Target == PermTargetMCP {
			warnings = append(warnings, plugin.Warning{
				Source:   source,
				Message:  fmt.Sprintf("mcp rule %q projected as %s on Claude", rule, pr.ClaudeRuleForm()),
				Severity: "info",
			})
		}
		// `**` recursive globs survive intact in Claude's dialect but are a
		// degraded fallback for tools that only support `*` — surface for
		// audit symmetry per SPEC §4.6.4.
		if strings.Contains(pr.Pattern, "**") {
			warnings = append(warnings, plugin.Warning{
				Source:   source,
				Message:  fmt.Sprintf("recursive_glob rule %q preserved verbatim in Claude's glob dialect", rule),
				Severity: "info",
			})
		}
		// Negation routes to deny regardless of the source bucket.
		if pr.Negated {
			warnings = append(warnings, plugin.Warning{
				Source:   source,
				Message:  fmt.Sprintf("negation rule %q routed to deny bucket (Claude has no inline negation form)", rule),
				Severity: "info",
			})
		}
		entries := claudeRuleWireForms(pr)
		targetBucket := bucket
		if pr.Negated {
			targetBucket = "deny"
		}
		switch targetBucket {
		case "allow":
			allow = append(allow, entries...)
		case "deny":
			deny = append(deny, entries...)
		case "ask":
			ask = append(ask, entries...)
		}
	}
	return allow, deny, ask, warnings
}

// claudeRuleWireForms returns the Claude wire form(s) for one parsed
// rule. fs: rules expand to three entries (Edit/Read/Write); every other
// rule produces a single entry.
func claudeRuleWireForms(pr PermRule) []string {
	if pr.Target == PermTargetFS {
		out := make([]string, 0, 3)
		for _, sub := range pr.FSFanOut() {
			out = append(out, sub.ClaudeRuleForm())
		}
		return out
	}
	return []string{pr.ClaudeRuleForm()}
}

// mcpServerJSON is the schema Claude Code expects for entries under
// `.mcp.json`'s `mcpServers` map. v2 fields: Type carries the transport
// (`stdio`/`http`/`sse`); Headers and URL accompany http/sse transports.
type mcpServerJSON struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// claudeEnvVarRewrite rewrites prism's canonical `${env:VAR}` substitution to
// Claude's native `${VAR}` form (SPEC §4.5.3). Applied to MCP `env`,
// `command`, `args`, `url`, and `headers` values at emit. Bare `${VAR}` in
// user input passes through unchanged.
var claudeEnvVarRewrite = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

func rewriteClaudeEnvVars(s string) string {
	return claudeEnvVarRewrite.ReplaceAllString(s, "${$1}")
}

func rewriteClaudeEnvVarsSlice(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = rewriteClaudeEnvVars(v)
	}
	return out
}

func rewriteClaudeEnvVarsMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = rewriteClaudeEnvVars(v)
	}
	return out
}

// buildMCPOp merges proj.MCP into any existing .mcp.json at proj.Root.
// Transport drives the `type` field (stdio/http/sse). Auth.Scheme controls
// header injection: bearer prepends `Authorization: Bearer <token>`,
// header merges Auth.Headers, oauth emits one info warning per server and
// does NOT emit credentials. `${env:VAR}` substitution rewrites to
// `${VAR}` (SPEC §4.5.3). Known upstream bugs (anthropics/claude-code#6204:
// `${env:VAR}` in headers + http/sse) emit a warning citing the issue.
//
// Scoped MCP servers are merged into the same mcpServers map as globals
// (Claude Code has no native per-scope MCP) with one info warning per
// scoped server.
func buildMCPOp(proj *model.Project) (plugin.Operation, error) {
	mcpPath := filepath.Join(proj.Root, ".mcp.json")

	doc := map[string]any{}
	if data, err := os.ReadFile(mcpPath); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return plugin.Operation{}, fmt.Errorf("claude: parsing existing %s: %w", mcpPath, err)
		}
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	var warnings []plugin.Warning
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		if srv.Disabled {
			continue
		}

		entry := mcpServerJSON{
			Type:    srv.Transport,
			Command: rewriteClaudeEnvVars(srv.Command),
			Args:    rewriteClaudeEnvVarsSlice(srv.Args),
			Env:     rewriteClaudeEnvVarsMap(srv.Env),
			URL:     rewriteClaudeEnvVars(srv.URL),
			Headers: rewriteClaudeEnvVarsMap(srv.Headers),
		}
		if entry.URL == "" && srv.Command == "" && srv.URL != "" {
			// Back-compat: when only the v0.8 URL is set with no
			// transport, still emit the URL.
			entry.URL = rewriteClaudeEnvVars(srv.URL)
		}

		// Auth handling (SPEC §4.5.2). One info warning per server with an
		// Auth.Scheme to surface the degradation (Claude has no native
		// `auth:` block; we inline the credentials into the headers when
		// possible, or warn-only for oauth).
		if srv.Auth != nil && srv.Auth.Scheme != "" {
			scheme := strings.ToLower(srv.Auth.Scheme)
			switch scheme {
			case "bearer":
				if srv.Auth.Token != "" {
					if entry.Headers == nil {
						entry.Headers = map[string]string{}
					}
					entry.Headers["Authorization"] = "Bearer " + rewriteClaudeEnvVars(srv.Auth.Token)
				}
				warnings = append(warnings, plugin.Warning{
					Source:   "mcp.yaml",
					Message:  fmt.Sprintf("MCP server %q Auth.Scheme=bearer degraded on Claude (no native auth block; injected as Authorization header)", srv.Name),
					Severity: "info",
				})
			case "header":
				if len(srv.Auth.Headers) > 0 {
					if entry.Headers == nil {
						entry.Headers = map[string]string{}
					}
					for k, v := range srv.Auth.Headers {
						entry.Headers[k] = rewriteClaudeEnvVars(v)
					}
				}
				warnings = append(warnings, plugin.Warning{
					Source:   "mcp.yaml",
					Message:  fmt.Sprintf("MCP server %q Auth.Scheme=header degraded on Claude (no native auth block; merged into headers)", srv.Name),
					Severity: "info",
				})
			case "oauth":
				warnings = append(warnings, plugin.Warning{
					Source:   "mcp.yaml",
					Message:  fmt.Sprintf("MCP server %q Auth.Scheme=oauth degraded on Claude (configure OAuth in Claude UI; no credentials emitted)", srv.Name),
					Severity: "info",
				})
			}
		}

		// Known upstream bug: `${env:VAR}` in http/sse headers
		// (anthropics/claude-code#6204).
		if (srv.Transport == "http" || srv.Transport == "sse") && hasEnvVarTemplate(srv.Headers) {
			warnings = append(warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("MCP server %q: ${env:VAR} in http/sse headers may not resolve at runtime (anthropics/claude-code#6204)", srv.Name),
				Severity: "info",
			})
		}

		// Drop unsupported / silent fields with warnings (SPEC §12).
		if srv.TimeoutMs > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("MCP server %q TimeoutMs=%d unsupported by Claude (no per-server timeout; use --request-timeout CLI flag)", srv.Name, srv.TimeoutMs),
				Severity: "warn",
			})
		}
		if len(srv.AutoApprove) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("MCP server %q AutoApprove unsupported by Claude (use --dangerously-skip-permissions or per-call prompts)", srv.Name),
				Severity: "warn",
			})
		}
		if srv.Trust {
			warnings = append(warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("MCP server %q Trust unsupported by Claude (no native trust flag; falls back to runtime prompts)", srv.Name),
				Severity: "warn",
			})
		}
		if len(srv.IncludeTools) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("MCP server %q IncludeTools unsupported by Claude (no per-server tool allowlist)", srv.Name),
				Severity: "warn",
			})
		}
		if len(srv.ExcludeTools) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  fmt.Sprintf("MCP server %q ExcludeTools unsupported by Claude (no per-server tool denylist)", srv.Name),
				Severity: "warn",
			})
		}

		servers[srv.Name] = entry
		if srv.ScopePath != "" {
			warnings = append(warnings, plugin.Warning{
				Source:   srv.ScopePath + "/mcp.yaml",
				Message:  fmt.Sprintf("scoped MCP server %q (scopepath %s) applied project-wide; Claude Code has no per-scope MCP", srv.Name, srv.ScopePath),
				Severity: "info",
			})
		}
	}
	doc["mcpServers"] = servers

	content, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return plugin.Operation{}, err
	}

	return plugin.Operation{
		Kind:     plugin.OpWrite,
		Path:     ".mcp.json",
		Content:  string(content) + "\n",
		Mode:     plugin.ModeWrite,
		Sources:  []string{"mcp.yaml"},
		Plugin:   "claude",
		Warnings: warnings,
	}, nil
}

// hasEnvVarTemplate reports whether any value in headers contains a
// `${env:VAR}` template that triggers anthropics/claude-code#6204.
func hasEnvVarTemplate(headers map[string]string) bool {
	for _, v := range headers {
		if claudeEnvVarRewrite.MatchString(v) {
			return true
		}
	}
	return false
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4). In-tree plugins always return the current
// version constant.
func (p *ClaudePlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that ClaudePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ClaudePlugin)(nil)

// dedupeStrings preserves first-seen order while dropping duplicates. Used
// when scoped and global permission lists are unioned into Claude's flat
// allow/deny/ask blocks, so the lockfile hash stays stable when a scoped
// rule repeats a global one.
func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
