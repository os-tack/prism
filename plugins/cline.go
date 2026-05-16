// Package plugins contains the projection plugins shipped with agents.
//
// ClinePlugin projects a canonical .agents/ directory into Cline (and
// Roo Code, which uses the same convention) `.clinerules/` rule files.
// Cline reads every file under `.clinerules/` as plain markdown — there
// is no native frontmatter, no scope/glob enforcement, and no slash
// command or subagent mechanism. Users conventionally order rules with
// a numeric filename prefix (e.g. `00-context.md`, `10-scope-...md`),
// and we follow that convention so projection output sits naturally next
// to hand-authored rules.
//
// Degradation summary:
//   - Context: 1:1 (native plain-markdown rule file)
//   - ScopePaths/ScopeSemantic: degraded — documented as a "When working
//     in <path>" preamble in the rule body; loaded always.
//   - Skills: degraded — projected as standalone rule files; scripts
//     ignored with a per-skill warning.
//   - Commands: degraded — projected as rule files describing the
//     command; there is no slash-command runtime.
//   - Agents, Hooks, Permissions, MCP: unsupported. Warnings emitted on
//     the first available op.
package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// ClinePlugin projects Project state into `.clinerules/*.md` files.
type ClinePlugin struct{}

// NewCline constructs a ClinePlugin.
func NewCline() *ClinePlugin { return &ClinePlugin{} }

// Compile-time check that ClinePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ClinePlugin)(nil)

// Name returns the stable plugin identifier.
func (p *ClinePlugin) Name() string { return "cline" }

// Detect returns true if the project at root looks like it uses Cline.
// Either the `.clinerules/` directory (modern, multi-file form) or a
// single `.clinerules` file (legacy form) activates the plugin.
func (p *ClinePlugin) Detect(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ".clinerules")); err == nil {
		// Same path serves both forms (directory or file); presence is
		// sufficient signal in either case.
		return true
	}
	return false
}

// Capabilities returns Cline's capability matrix.
func (p *ClinePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportDegraded,
		ScopeSemantic: plugin.SupportDegraded,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportDegraded,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportUnsupported,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportUnsupported,
	}
}

// Plan produces the Operations needed to project proj into `.clinerules/`.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Cline never symlinks — rule files are not byte-identical to source
// (they get a documented preamble), and we want each rule self-contained
// so users can hand-tune individual files. Unknown modes return an error.
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

	// Root context → .clinerules/00-context.md.
	if proj.Context != nil {
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".clinerules/00-context.md",
			Content: ensureTrailingNewline(proj.Context.Body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		}
		ops = append(ops, op)
	}

	// Per-scope rule files at .clinerules/10-scope-<slug>.md.
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
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "10-scope-"+slugify(scope.Path)+".md")),
			Content: renderClineScope(scope, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
			Warnings: []plugin.Warning{
				{
					Source:   sourceOrEmpty(sources),
					Message:  "Cline has no scope enforcement; rule loaded always.",
					Severity: "info",
				},
			},
		}
		ops = append(ops, op)
	}

	// Skills → .clinerules/20-skill-<slug>.md (global) or
	// .clinerules/20-skill-<scopeSlug>-<name>.md (scoped, degraded).
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
		if skill.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline has no scope enforcement at runtime; scoped skill projected as a rule loaded always (scope: %s).", skill.ScopePath),
				Severity: "info",
			})
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

	// Commands → .clinerules/30-command-<slug>.md (global) or
	// .clinerules/30-command-<scopeSlug>-<name>.md (scoped, degraded).
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
		fileName := "30-command-" + skillSlug(cmd.Name) + ".md"
		if cmd.ScopePath != "" {
			fileName = "30-command-" + scopeSlug(cmd.ScopePath) + "-" + skillSlug(cmd.Name) + ".md"
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", fileName)),
			Content: renderClineCommand(cmd, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
			Warnings: []plugin.Warning{
				{
					Source:   sourceOrEmpty(sources),
					Message:  "Cline has no slash-command mechanism; documented as text.",
					Severity: "info",
				},
			},
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline has no scope enforcement at runtime; scoped command projected as a rule loaded always (scope: %s).", cmd.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Collect warnings for capabilities with no op host: agents, hooks,
	// permissions, MCP. We attach these to the first emitted op (if any).
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
	}
	for _, hook := range proj.Hooks {
		if hook == nil {
			continue
		}
		msg := fmt.Sprintf("Cline has no hook primitive; %s:%s not projected.", hook.Event, hook.Matcher)
		if hook.ScopePath != "" {
			msg = fmt.Sprintf("Cline has no hook primitive; scoped hook %s:%s (scope: %s) not projected.", hook.Event, hook.Matcher, hook.ScopePath)
		}
		orphanWarnings = append(orphanWarnings, plugin.Warning{
			Source:   hook.ScriptPath,
			Message:  msg,
			Severity: "info",
		})
	}
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			orphanWarnings = append(orphanWarnings, plugin.Warning{
				Source:   "",
				Message:  "Cline has no permissions primitive; permissions not projected.",
				Severity: "info",
			})
		}
	}
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		orphanWarnings = append(orphanWarnings, plugin.Warning{
			Source:   "",
			Message:  fmt.Sprintf("Cline configures MCP via VS Code settings; %s not projected.", srv.Name),
			Severity: "info",
		})
	}

	if len(orphanWarnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, orphanWarnings...)
	}

	return ops, nil
}

// renderClineScope builds the markdown body for a scope rule file. The
// preamble documents the scope so Cline (which has no native scope
// enforcement) surfaces the intended applicability to the model.
func renderClineScope(scope *model.Scope, body string) string {
	var b strings.Builder
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
// skill.ScopePath is non-empty, a `## When working in <scopePath>` preamble is
// prepended so Cline (which has no scope enforcement) surfaces the intended
// applicability to the model.
func renderClineSkill(skill *model.Skill, body string) string {
	var b strings.Builder
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

// renderClineCommand builds the markdown body for a command rule file. When
// cmd.ScopePath is non-empty, a `## When working in <scopePath>` preamble is
// prepended so Cline (which has no scope enforcement) surfaces the intended
// applicability to the model.
func renderClineCommand(cmd *model.Command, body string) string {
	var b strings.Builder
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
