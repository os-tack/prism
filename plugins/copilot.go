package plugins

// CopilotPlugin projects a canonical .agents/ directory into GitHub Copilot's
// repo-local configuration surface under `.github/`:
//
//   - .github/copilot-instructions.md            — single repo-wide instructions
//   - .github/instructions/<slug>.instructions.md — per-glob scoped instructions
//                                                   (frontmatter: applyTo)
//   - .github/prompts/<name>.prompt.md           — prompt files (slash-command analog)
//                                                   (frontmatter: description, mode)
//
// Copilot's `applyTo` is a single glob string (not a list); when a Scope or
// Skill has multiple globs we use the first and emit a degradation warning
// naming the dropped patterns. Copilot has no subagent, hook, permission, or
// project-local MCP primitive — those emit warnings only and produce no files.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// CopilotPlugin projects Project state into `.github/` files Copilot reads.
type CopilotPlugin struct{}

// NewCopilot constructs a CopilotPlugin.
//
// As with the other plugins in this package, we use a per-plugin constructor
// name (`NewCopilot`) because Go does not permit a package-scope `New`
// shared between multiple types.
func NewCopilot() *CopilotPlugin { return &CopilotPlugin{} }

// Name returns the stable plugin identifier.
func (p *CopilotPlugin) Name() string { return "copilot" }

// Detect returns true if the project at root looks like it uses Copilot.
// The presence of a `.github/` directory is the primary signal — almost every
// real GitHub repo has one — and we also accept a bare
// `.github/copilot-instructions.md` file in case `.github/` is missing for
// some reason but Copilot wiring is already in place.
func (p *CopilotPlugin) Detect(root string) bool {
	if info, err := os.Stat(filepath.Join(root, ".github")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, ".github", "copilot-instructions.md")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// Capabilities returns Copilot's capability matrix.
//
// Copilot natively supports a repo-wide instructions file (Context), per-glob
// instruction attachment (ScopePaths), and prompt files / slash commands
// (Commands). ScopeSemantic is degraded — Copilot's `applyTo` is glob-only,
// with no description-driven trigger. Skills degrade to either instructions or
// prompts. Agents, Hooks, Permissions, and MCP are unsupported (project-local
// MCP config is not read by Copilot).
func (p *CopilotPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportDegraded,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportUnsupported,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportUnsupported,
	}
}

// Plan produces the Operations needed to project proj into `.github/`.
//
// Mode handling: empty and "symlink" are accepted (symlink default for the
// root copilot-instructions.md, which is plain markdown). "write" forces
// write mode for everything. Per-scope/per-skill files and prompts are
// ALWAYS write mode because they inject frontmatter the canonical source
// does not contain; symlinking those would point at byte-different content.
// Any other mode value returns an error.
func (p *CopilotPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	mode := plugin.ModeSymlink
	switch opts.Mode {
	case "", "symlink":
		mode = plugin.ModeSymlink
	case "write":
		mode = plugin.ModeWrite
	default:
		return nil, fmt.Errorf("copilot: unsupported mode %q (want \"write\" or \"symlink\")", opts.Mode)
	}

	var ops []plugin.Operation

	// 1. Root context → .github/copilot-instructions.md. Plain markdown, no
	// frontmatter required, so symlink mode is honored when possible.
	if proj.Context != nil {
		const path = ".github/copilot-instructions.md"
		op := plugin.Operation{
			Path:    path,
			Mode:    mode,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		}
		if mode == plugin.ModeSymlink && proj.Root != "" && proj.Context.SourcePath != "" {
			targetDir := filepath.Join(proj.Root, filepath.Dir(path))
			linkTarget, err := filepath.Rel(targetDir, proj.Context.SourcePath)
			if err != nil {
				return nil, fmt.Errorf("copilot: compute symlink target for %s: %w", path, err)
			}
			op.Kind = plugin.OpSymlink
			op.LinkTarget = linkTarget
		} else {
			op.Kind = plugin.OpWrite
			op.Content = proj.Context.Body
		}
		ops = append(ops, op)
	}

	// 2. Per-scope instruction files. ALWAYS write mode (frontmatter injection).
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
		applyTo, scopeWarn := pickApplyTo(scope.Globs, scope.Document)
		path := filepath.ToSlash(filepath.Join(".github", "instructions", slugify(scope.Path)+".instructions.md"))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: renderCopilotInstructions(applyTo, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if scopeWarn != nil {
			op.Warnings = append(op.Warnings, *scopeWarn)
		}
		ops = append(ops, op)
	}

	// 3. Skills → degraded scoped instruction files under .github/instructions.
	// Filename prefix `skill-` keeps them distinguishable from canonical scopes.
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
		applyTo, globWarn := pickApplyTo(skill.Globs, skill.Document)
		path := filepath.ToSlash(filepath.Join(".github", "instructions", "skill-"+skillSlug(skill.Name)+".instructions.md"))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: renderCopilotInstructions(applyTo, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if globWarn != nil {
			op.Warnings = append(op.Warnings, *globWarn)
		}
		if len(skill.Scripts) > 0 {
			src := ""
			if skill.Document != nil {
				src = proj.SourceTag(skill.Document.SourcePath)
			}
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Copilot has no script execution; scripts ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// 4. Commands → .github/prompts/<name>.prompt.md.
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
		path := filepath.ToSlash(filepath.Join(".github", "prompts", cmd.Name+".prompt.md"))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: renderCopilotPrompt(cmd.Description, "ask", body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		ops = append(ops, op)
	}

	// 5. Capability gaps that produce no files: collect warnings.
	var warnings []plugin.Warning
	for _, agent := range proj.Agents {
		if agent == nil {
			continue
		}
		src := ""
		if agent.Document != nil {
			src = proj.SourceTag(agent.Document.SourcePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("Copilot has no subagent primitive; %s not projected.", agent.Name),
			Severity: "info",
		})
	}
	for _, hook := range proj.Hooks {
		if hook == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   hook.ScriptPath,
			Message:  fmt.Sprintf("Copilot has no hook primitive; %s:%s not projected.", hook.Event, hook.Matcher),
			Severity: "info",
		})
	}
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "permissions.yaml",
				Message:  "Copilot has no permissions primitive; permissions not projected.",
				Severity: "info",
			})
		}
	}
	for _, srv := range proj.MCP {
		if srv == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   "mcp.yaml",
			Message:  fmt.Sprintf("Copilot does not read project-local MCP config; %s not projected", srv.Name),
			Severity: "info",
		})
	}

	// Attach warnings without a host op to the first emitted op. If no op
	// exists, the warnings have nowhere to land — drop them rather than
	// invent a synthetic op.
	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// pickApplyTo selects a single applyTo glob from a list of canonical globs
// and produces a degradation warning when the list has more than one entry.
// When globs is empty, fall back to "**" (apply to everything) and emit no
// warning — the canonical model has nothing to drop.
//
// The doc argument is used only for warning attribution; nil is fine.
func pickApplyTo(globs []string, doc *model.Document) (string, *plugin.Warning) {
	if len(globs) == 0 {
		return "**", nil
	}
	first := globs[0]
	if len(globs) == 1 {
		return first, nil
	}
	src := ""
	if doc != nil {
		src = doc.SourcePath
	}
	return first, &plugin.Warning{
		Source: src,
		Message: fmt.Sprintf(
			"Copilot's applyTo is single-valued; using first glob of %d: %s; ignoring: %s",
			len(globs), first, strings.Join(globs[1:], ", "),
		),
		Severity: "info",
	}
}

// renderCopilotInstructions formats the YAML frontmatter + body for an
// .instructions.md file. Only `applyTo` appears in frontmatter; `globs` is
// NOT a Copilot key and we deliberately do not emit it. The applyTo value is
// quoted to keep YAML happy with glob characters like `**`.
func renderCopilotInstructions(applyTo, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("applyTo: ")
	b.WriteString(yamlQuote(applyTo))
	b.WriteString("\n")
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderCopilotPrompt formats a .prompt.md file with description + mode
// frontmatter and a markdown body. Description may be empty (we still emit
// the key with an empty string so downstream tools can edit it in place).
func renderCopilotPrompt(description, promptMode, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: ")
	b.WriteString(yamlQuote(description))
	b.WriteString("\n")
	if promptMode != "" {
		b.WriteString("mode: ")
		b.WriteString(promptMode)
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

// yamlQuote returns a double-quoted YAML scalar with the minimal escaping
// needed for the kinds of strings we emit (globs and descriptions). It
// always quotes — even when the string is "safe" — to avoid surprises with
// glob characters and YAML's many implicit-type rules.
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// Compile-time check that CopilotPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*CopilotPlugin)(nil)
