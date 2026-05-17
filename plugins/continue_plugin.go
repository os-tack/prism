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
// rule files (no script execution). Scoped skills include their scope slug as
// a filename prefix to avoid collisions when same-named skills live in two
// scopes; their globs come from the parser (frontmatter override or the
// scope's default). Scoped commands degrade to scoped rule files with the
// scope's default globs plus an info warning.
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
	"agents.dev/agents/internal/scope"
)

// ContinuePlugin projects Project state into `.continue/rules/*.md` and
// `.continue/mcpServers/*.yaml` files.
type ContinuePlugin struct {
	// DisableHookWrappers, when true, suppresses the perms-guard wrapper
	// script + sidecar policy emission used to enforce prism permissions.
	// Default false (wrappers ON). Mirrors ClaudePlugin.DisableHookWrappers.
	DisableHookWrappers bool
}

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
		Permissions:   plugin.SupportNative, // via prism perms-guard wrapper + sidecar policy
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
		content := renderContinueRule(desc, sc.Globs, false, body)
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "rules", slugify(sc.Path)+".md")),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
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
		content := renderContinueRule(desc, skill.Globs, false, body)
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
		ops = append(ops, op)
	}

	// Commands → degraded rule files at .continue/rules/command-<slug>.md.
	// The body documents the command; Continue has no slash-command mechanism
	// so each command also gets an info warning attached to its own op.
	//
	// Scoped commands (ScopePath != "") get the scope slug prefixed and use
	// the scope's default globs in the rule frontmatter; the warning notes
	// the scope and explains the degradation.
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
		var globs []string
		desc := fmt.Sprintf("Command /%s: %s", cmd.Name, cmd.Description)
		warningMsg := "Continue has no slash-command mechanism; documented as a rule"
		if cmd.ScopePath != "" {
			globs = scope.DefaultGlobs(cmd.ScopePath)
			desc = fmt.Sprintf("Command /%s (scoped to %s): %s", cmd.Name, cmd.ScopePath, cmd.Description)
			warningMsg = fmt.Sprintf("Continue has no slash-command mechanism; scoped command %q projected as a rule (scope: %s)", cmd.Name, cmd.ScopePath)
		}
		content := renderContinueRule(desc, globs, false, body)
		fname := "command-" + scopedSkillSlug(cmd.ScopePath, cmd.Name) + ".md"
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "rules", fname)),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   "",
			Message:  warningMsg,
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
		msg := fmt.Sprintf("Continue has no subagent primitive; %s not projected", ag.Name)
		if ag.ScopePath != "" {
			msg = fmt.Sprintf("Continue has no subagent primitive; scoped agent %s (scope: %s) not projected", ag.Name, ag.ScopePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   src,
			Message:  msg,
			Severity: "info",
		})
	}
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		msg := fmt.Sprintf("Continue has no hook primitive; %s:%s not projected", h.Event, h.Matcher)
		if h.ScopePath != "" {
			msg = fmt.Sprintf("Continue has no hook primitive; scoped hook %s:%s (scope: %s) not projected", h.Event, h.Matcher, h.ScopePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   h.ScriptPath,
			Message:  msg,
			Severity: "info",
		})
	}
	// Permissions (global + scoped) project via prism perms-guard wrappers.
	// Each non-empty Permissions block becomes a sidecar JSON policy plus
	// either per-hook wrappers (when hooks exist) or a bare gate wrapper.
	// The CHANGELOG documents the on-disk layout and JSON policy schema.
	wrapperOps, wrapperWarnings, err := emitPermsGuardWrappers(p.Name(), proj, p.DisableHookWrappers)
	if err != nil {
		return nil, err
	}
	ops = append(ops, wrapperOps...)
	warnings = append(warnings, wrapperWarnings...)

	// MCP servers → one .continue/mcpServers/<slug>.yaml per server.
	// Scoped MCP servers project to the same global file set with an info
	// warning per server (Continue has no per-scope MCP).
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
	for _, srv := range proj.MCP {
		if srv == nil || srv.ScopePath == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   "",
			Message:  fmt.Sprintf("Continue has no per-scope MCP; scoped server %q (scope: %s) merged into global block", srv.Name, srv.ScopePath),
			Severity: "info",
		})
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
		raw, err := json.Marshal(description)
		if err != nil {
			raw = []byte("\"\"")
		}
		b.WriteString("description: ")
		b.Write(raw)
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
