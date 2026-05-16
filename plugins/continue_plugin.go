// Package plugins contains the projection plugins that translate a canonical
// .agents/ model into per-tool config files.
//
// ContinuePlugin projects a model.Project into Continue's `.continue/` layout:
//
//   - `.continue/rules/<name>.md` — rule files with YAML frontmatter
//     (`description`, `globs`, `alwaysApply`) followed by a markdown body.
//     Continue uses the frontmatter to decide when to auto-attach a rule.
//   - `.continue/mcpServers/<name>.yaml` — one YAML file per MCP server
//     (Continue's per-file mcpServers convention) containing `name`,
//     `command`, `args`, `env`, `url` (only the non-empty fields).
//
// Continue has no native primitive for slash-commands, sub-agents, hooks, or
// permissions, so those degrade to warnings. Skills are projected as scoped
// rule files (no script execution).
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// ContinuePlugin projects Project state into `.continue/rules/*.md` and
// `.continue/mcpServers/*.yaml` files.
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
// description-triggered attachment (ScopeSemantic), and per-file MCP server
// configuration. Skills and Commands degrade to scoped rule files (no script
// execution and no slash-command mechanism). Agents, Hooks, and Permissions
// are unsupported.
func (p *ContinuePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportDegraded,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportUnsupported,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportNative,
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
		content := renderContinueRule("Project-wide context", nil, true, proj.Context.Body)
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
	for _, scope := range proj.Scopes {
		if scope == nil {
			continue
		}
		desc := scope.Description
		if desc == "" {
			desc = fmt.Sprintf("Context for %s", scope.Path)
		}
		body := ""
		var sources []string
		if scope.Document != nil {
			body = scope.Document.Body
			sources = []string{proj.SourceTag(scope.Document.SourcePath)}
		}
		content := renderContinueRule(desc, scope.Globs, false, body)
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "rules", slugify(scope.Path)+".md")),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
	}

	// Skills → degraded scoped rule files at .continue/rules/skill-<slug>.md.
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
		content := renderContinueRule(desc, skill.Globs, false, body)
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "rules", "skill-"+skillSlug(skill.Name)+".md")),
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
		ops = append(ops, op)
	}

	// Commands → degraded rule files at .continue/rules/command-<slug>.md.
	// The body documents the command; Continue has no slash-command mechanism
	// so each command also gets an info warning attached to its own op.
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
		content := renderContinueRule(desc, nil, false, body)
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "rules", "command-"+skillSlug(cmd.Name)+".md")),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   "",
			Message:  "Continue has no slash-command mechanism; documented as a rule",
			Severity: "info",
		})
		// Attach the source path to the warning if available.
		if len(sources) > 0 {
			op.Warnings[len(op.Warnings)-1].Source = sources[0]
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
		src := ""
		if ag.Document != nil {
			src = proj.SourceTag(ag.Document.SourcePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("Continue has no subagent primitive; %s not projected", ag.Name),
			Severity: "info",
		})
	}
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   h.ScriptPath,
			Message:  fmt.Sprintf("Continue has no hook primitive; %s:%s not projected", h.Event, h.Matcher),
			Severity: "info",
		})
	}
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  "Continue has no permissions primitive; permissions not projected",
				Severity: "info",
			})
		}
	}

	// MCP servers → one .continue/mcpServers/<slug>.yaml per server.
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		op, err := buildContinueMCPOp(p, srv)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Attach orphan warnings to the first available op.
	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// buildContinueMCPOp constructs the OpWrite for a single MCP server file at
// `.continue/mcpServers/<slug>.yaml`. Only non-empty fields from srv are
// emitted. URL-only servers omit command/args/env.
func buildContinueMCPOp(p *ContinuePlugin, srv *model.MCPServer) (plugin.Operation, error) {
	// Use yaml.Node so we control key order deterministically and emit only
	// non-empty fields. A plain map[string]any would still serialize but
	// yaml.v3 sorts map keys non-alphabetically; a node tree keeps the order
	// stable: name, command, args, env, url.
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
	addStr("command", srv.Command)
	if len(srv.Args) > 0 {
		argsNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, a := range srv.Args {
			argsNode.Content = append(argsNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: a})
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "args"},
			argsNode,
		)
	}
	if len(srv.Env) > 0 {
		envNode := &yaml.Node{Kind: yaml.MappingNode}
		// Sort env keys for deterministic output.
		keys := make([]string, 0, len(srv.Env))
		for k := range srv.Env {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			envNode.Content = append(envNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Value: srv.Env[k]},
			)
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "env"},
			envNode,
		)
	}
	addStr("url", srv.URL)

	raw, err := yaml.Marshal(root)
	if err != nil {
		return plugin.Operation{}, fmt.Errorf("continue: marshal mcp server %q: %w", srv.Name, err)
	}

	return plugin.Operation{
		Kind:    plugin.OpWrite,
		Path:    filepath.ToSlash(filepath.Join(".continue", "mcpServers", skillSlug(srv.Name)+".yaml")),
		Content: string(raw),
		Mode:    plugin.ModeWrite,
		Plugin:  p.Name(),
		Sources: []string{"mcp.yaml"},
	}, nil
}

// renderContinueRule formats the YAML frontmatter + markdown body for a
// `.continue/rules/<name>.md` file. Empty values are omitted.
//
// The globs field is rendered via encoding/json — a JSON array of strings is
// also valid YAML flow-array syntax, and json.Marshal handles escaping.
func renderContinueRule(description string, globs []string, alwaysApply bool, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if description != "" {
		b.WriteString("description: ")
		b.WriteString(description)
		b.WriteString("\n")
	}
	if len(globs) > 0 {
		raw, err := json.Marshal(globs)
		if err != nil {
			raw = []byte("[]")
		}
		b.WriteString("globs: ")
		b.Write(raw)
		b.WriteString("\n")
	}
	if alwaysApply {
		b.WriteString("alwaysApply: true\n")
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

// sortStrings is a tiny shim to avoid importing "sort" twice in the package
// (cursor.go and claude.go already import their own). Inlines insertion sort
// — env maps are always small.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// Compile-time check that ContinuePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ContinuePlugin)(nil)
