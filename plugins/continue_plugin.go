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
//     `command`, `args`, `env`, `url` (only the non-empty fields).
//
// As of v0.8, permissions enforce natively via Continue's own permissions
// layer (replacing the prism perms-guard wrapper), and slash commands emit
// natively as prompt files. Skills still degrade to scoped rule files (no
// dedicated skill primitive in Continue). Sub-agents and hooks are
// unsupported and emit info warnings.
package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// ContinuePlugin projects Project state into `.continue/rules/*.md`,
// `.continue/prompts/*.md`, `.continue/permissions.yaml`, and
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
// description-triggered attachment (ScopeSemantic), invokable prompt files
// (Commands), permissions.yaml (Permissions), and per-file MCP server
// configuration. Skills degrade to scoped rule files (no dedicated skill
// primitive). Agents and Hooks are unsupported.
func (p *ContinuePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative, // .continue/prompts/<name>.md with invokable: true
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportUnsupported,
		Permissions:   plugin.SupportNative, // .continue/permissions.yaml (project-local override)
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
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".continue", "prompts", fname)),
			Content: renderContinuePrompt(cmd.Name, desc, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if cmd.ScopePath != "" {
			src := ""
			if len(sources) > 0 {
				src = sources[0]
			}
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Continue prompt files have no per-scope attachment; scoped command %q (scope: %s) projected as a global slash command", cmd.Name, cmd.ScopePath),
				Severity: "info",
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
		op, err := buildContinueMCPOp(p, srv)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
		if srv.ScopePath != "" {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  fmt.Sprintf("Continue has no per-scope MCP; scoped server %q (scope: %s) merged into global block", srv.Name, srv.ScopePath),
				Severity: "info",
			})
		}
	}

	// Attach orphan warnings to the first available op.
	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
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
// YAML frontmatter (`name`, `description`, `invokable: true`) followed by
// the prompt body. The encoding/json shim is used to escape the
// description and name strings safely (JSON strings are valid YAML
// scalars, including the colon-in-description case).
func renderContinuePrompt(name, description, body string) string {
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
	b.WriteString("invokable: true\n")
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
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
		sort.Strings(keys)
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
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// Compile-time check that ContinuePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ContinuePlugin)(nil)
