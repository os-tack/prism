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
// Windsurf has no skill / command / agent / hook / permissions / MCP
// primitives. Skills and Commands degrade to rules (with a description so the
// model can decide when to surface them); Agents, Hooks, Permissions, and MCP
// only emit informational warnings — Windsurf configures MCP separately and
// has no equivalent for the rest.

package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// WindsurfPlugin projects Project state into `.windsurf/rules/*.md` files.
type WindsurfPlugin struct{}

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
// attachment (ScopeSemantic via trigger: model_decision). Skills and Commands
// degrade (no script execution, no slash-command mechanism). Agents, Hooks,
// Permissions, and MCP are unsupported — Windsurf configures MCP separately.
func (p *WindsurfPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportDegraded,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportUnsupported,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportUnsupported,
	}
}

// Plan produces the Operations needed to project proj into `.windsurf/rules/`.
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
		fm := windsurfFrontmatter{
			Trigger:     "glob",
			Globs:       scope.Globs,
			Description: scope.Description,
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", slugify(scope.Path)+".md")),
			Content: renderWindsurfRule(fm, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
	}

	// Skills → degraded rule files. Globbed skills become trigger: glob,
	// otherwise trigger: model_decision (which requires a description).
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
		var fm windsurfFrontmatter
		if len(skill.Globs) > 0 {
			fm = windsurfFrontmatter{
				Trigger:     "glob",
				Globs:       skill.Globs,
				Description: skill.Description,
			}
		} else {
			desc := skill.Description
			if desc == "" {
				desc = skill.Trigger
			}
			fm = windsurfFrontmatter{
				Trigger:     "model_decision",
				Description: desc,
			}
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", "skill-"+skillSlug(skill.Name)+".md")),
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
		fm := windsurfFrontmatter{
			Trigger:     "model_decision",
			Description: desc,
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", "command-"+skillSlug(cmd.Name)+".md")),
			Content: renderWindsurfRule(fm, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   sourceFromCommand(proj, cmd),
			Message:  fmt.Sprintf("Windsurf has no slash-command mechanism; %s documented as a rule.", cmd.Name),
			Severity: "info",
		})
		ops = append(ops, op)
	}

	// Collect un-attached warnings for capability types we do not project.
	// These attach to the first emitted op below.
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
			Message:  fmt.Sprintf("Windsurf has no subagent primitive; %s not projected.", agent.Name),
			Severity: "info",
		})
	}
	for _, hook := range proj.Hooks {
		if hook == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   hook.ScriptPath,
			Message:  fmt.Sprintf("Windsurf has no hook primitive; %s:%s not projected.", hook.Event, hook.Matcher),
			Severity: "info",
		})
	}
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  "Windsurf has no permissions primitive; permissions not projected.",
				Severity: "info",
			})
		}
	}
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   "mcp.yaml",
			Message:  fmt.Sprintf("Windsurf configures MCP separately; %s not projected.", srv.Name),
			Severity: "info",
		})
	}

	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
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
// otherwise.
type windsurfFrontmatter struct {
	Trigger     string
	Globs       []string
	Description string
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
		raw, err := json.Marshal(fm.Globs)
		if err != nil {
			raw = []byte("[]")
		}
		b.WriteString("globs: ")
		b.Write(raw)
		b.WriteString("\n")
	}
	if fm.Description != "" {
		b.WriteString("description: ")
		b.WriteString(fm.Description)
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

// Compile-time check that WindsurfPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*WindsurfPlugin)(nil)
