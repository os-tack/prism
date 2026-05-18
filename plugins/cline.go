// Package plugins contains the projection plugins shipped with agents.
//
// ClinePlugin projects a canonical .agents/ directory into Cline (and
// Roo Code, which uses the same convention) `.clinerules/` rule files,
// plus the modern Cline primitives added in late 2025:
//
//   - YAML frontmatter on rule files (`paths:`, `description:`) — used
//     to express ScopePaths natively.
//   - Workflows at `.clinerules/workflows/<name>.md` — used for slash
//     commands (CMDS native, replacing the old `30-command-*` rules).
//   - Hooks at `.clinerules/hooks/<event>.json` — JSON-on-stdin /
//     exit-2-blocks contract, matching Claude Code's hook shape.
//   - MCP at `.cline/cline_mcp_settings.json` — project-local mirror of
//     the CLI settings file, written in the standard `{mcpServers:{…}}`
//     schema and merged into any existing file at that path.
//
// Capability summary (v0.8.0):
//   - Context:        native (plain-markdown rule file at 00-context.md)
//   - ScopePaths:     native (YAML frontmatter `paths:` glob array)
//   - ScopeSemantic:  native (same frontmatter; description hint)
//   - Skills:         degraded — no dedicated primitive; emitted as
//     scoped rules with `paths:` + description.
//   - Commands:       native (workflows/<name>.md slash commands)
//   - Hooks:          native (.clinerules/hooks/<event>.json)
//   - MCP:            native (.cline/cline_mcp_settings.json)
//   - Agents:         unsupported (Cline subagents are SDK-based)
//   - Permissions:    unsupported (no native primitive in v0.8.0; could
//     be wrapped via PreToolUse in a later release)
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
)

// ClinePlugin projects Project state into `.clinerules/` rule files,
// workflows, hooks, and the sibling `.cline/cline_mcp_settings.json`.
type ClinePlugin struct {
	// DisableHookWrappers, when true, projects scoped hooks as if they
	// were global (no `__scope-guard__` wrapper). Default false
	// (wrappers ON). Mirrors ClaudePlugin.DisableHookWrappers for
	// programmatic configuration in tests.
	DisableHookWrappers bool
}

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
		return true
	}
	return false
}

// Capabilities returns Cline's capability matrix (v0.8.0).
func (p *ClinePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportNative,
	}
}

// Plan produces the Operations needed to project proj into Cline's layout.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Cline never symlinks — rule files carry a documented preamble and
// frontmatter, and we want each file self-contained so users can hand
// tune individual files. Unknown modes return an error.
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

	// Root context → .clinerules/00-context.md (no frontmatter; loads always).
	if proj.Context != nil {
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".clinerules/00-context.md",
			Content: ensureTrailingNewline(proj.Context.Body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		})
	}

	// Per-scope rule files at .clinerules/10-scope-<slug>.md with native
	// YAML frontmatter (`paths:` glob array, optional `description:`).
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
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "10-scope-"+slugify(scope.Path)+".md")),
			Content: renderClineScope(scope, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
	}

	// Skills → .clinerules/20-skill-<slug>.md (global) or
	// .clinerules/20-skill-<scopeSlug>-<name>.md (scoped). Skills get
	// a `paths:` frontmatter when a glob list is available; scripts are
	// noted as ignored (Cline has no script-execution mechanism).
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
		if len(skill.Scripts) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline cannot execute scripts; ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Commands → .clinerules/workflows/<name>.md (native slash commands).
	// Scoped commands get a <scopeSlug>-<name>.md filename and an info
	// warning — workflows are global in Cline, so the scope path is
	// preserved only as a "When working in <path>" preamble.
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
		fileName := skillSlug(cmd.Name) + ".md"
		if cmd.ScopePath != "" {
			fileName = scopeSlug(cmd.ScopePath) + "-" + skillSlug(cmd.Name) + ".md"
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "workflows", fileName)),
			Content: renderClineWorkflow(cmd, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline workflows are global; scoped command %q (scope: %s) projected without path enforcement.", cmd.Name, cmd.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (must be emitted before the per-event
	// hook JSON so the JSON can reference the wrapper's absolute path).
	// Each scoped hook gets its own wrapper script at
	// .clinerules/hooks/__scope-guard__/<scopeSlug>-<event>-<basename>.sh.
	// The wrapper reads JSON from stdin and exec's `prism scope-guard`,
	// matching the Claude wrapper contract in plugins/claude.go.
	wrapperPaths := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + h.Event + "-" + hookName + ".sh"
		wrapperRel := filepath.ToSlash(filepath.Join(".clinerules", "hooks", "__scope-guard__", wrapperFile))
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

	// Hooks → .clinerules/hooks/<event>.json. One JSON file per event,
	// each carrying the standard {matcher, hooks: [{type, command}]}
	// shape. Scoped hooks point at their scope-guard wrapper.
	if hookOps := buildClineHookOps(proj, wrapperPaths); len(hookOps) > 0 {
		ops = append(ops, hookOps...)
	}

	// MCP → .cline/cline_mcp_settings.json (project-local).
	//
	// Rationale: the CLI install reads the file from
	// ~/.cline/data/settings/cline_mcp_settings.json, which is per-user
	// and would surprise users by mutating their home directory. The
	// VS Code extension reads from extension globalStorage which is not
	// addressable from a project at all. We pick a project-local path
	// (.cline/cline_mcp_settings.json) — users can `ln -s` or copy it
	// into the CLI location explicitly, which keeps prism's projection
	// safe to run anywhere. The schema is identical to Claude's
	// .mcp.json (standard MCP `{mcpServers: {...}}`), so users with
	// existing tooling can swap files trivially.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildClineMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	// Orphan warnings for capabilities Cline still lacks: agents,
	// permissions. Attach to the first emitted op (if any).
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
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			orphanWarnings = append(orphanWarnings, plugin.Warning{
				Source:   "permissions.yaml",
				Message:  "Cline has no permissions primitive in v0.8.0; permissions not projected.",
				Severity: "info",
			})
		}
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		if len(sp.Allow)+len(sp.Deny)+len(sp.Ask) == 0 {
			continue
		}
		orphanWarnings = append(orphanWarnings, plugin.Warning{
			Source:   sp.ScopePath + "/permissions.yaml",
			Message:  fmt.Sprintf("Cline has no permissions primitive; scoped permissions (scope: %s) not projected.", sp.ScopePath),
			Severity: "info",
		})
	}

	if len(orphanWarnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, orphanWarnings...)
	}

	return ops, nil
}

// clineHookEntry mirrors the Claude / Cline hook entry shape used inside
// the per-event JSON file at .clinerules/hooks/<event>.json.
type clineHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// clineHookGroup is one matcher group within a hook event.
type clineHookGroup struct {
	Matcher string           `json:"matcher"`
	Hooks   []clineHookEntry `json:"hooks"`
}

// clineHookFile is the JSON document written to
// .clinerules/hooks/<event>.json. The top-level array of matcher groups
// matches the schema Cline uses internally; the engine that reads it
// dispatches by matcher and runs each command with the standard
// JSON-on-stdin / exit-2-blocks contract.
type clineHookFile struct {
	Hooks []clineHookGroup `json:"hooks"`
}

// buildClineHookOps emits one OpWrite per Hook.Event. The known events
// are: TaskStart, TaskResume, UserPromptSubmit, PreToolUse, PostToolUse,
// TaskComplete, TaskCancel. We do not validate the event name here —
// any non-empty event becomes a JSON filename, so callers can stage
// future events without code changes.
func buildClineHookOps(proj *model.Project, wrapperPaths map[*model.Hook]string) []plugin.Operation {
	if len(proj.Hooks) == 0 {
		return nil
	}

	// event → matcher → []entry, preserving Hooks slice order within each
	// matcher bucket for stable output.
	buckets := map[string]map[string][]clineHookEntry{}
	matcherOrder := map[string][]string{}
	eventOrder := []string{}
	for _, h := range proj.Hooks {
		if h == nil || h.Event == "" {
			continue
		}
		if _, ok := buckets[h.Event]; !ok {
			buckets[h.Event] = map[string][]clineHookEntry{}
			eventOrder = append(eventOrder, h.Event)
		}
		if _, ok := buckets[h.Event][h.Matcher]; !ok {
			matcherOrder[h.Event] = append(matcherOrder[h.Event], h.Matcher)
		}
		cmdPath := h.ScriptPath
		if w, ok := wrapperPaths[h]; ok {
			cmdPath = w
		}
		buckets[h.Event][h.Matcher] = append(buckets[h.Event][h.Matcher], clineHookEntry{
			Type:    "command",
			Command: cmdPath,
		})
	}

	sort.Strings(eventOrder)

	var ops []plugin.Operation
	for _, event := range eventOrder {
		matchers := append([]string(nil), matcherOrder[event]...)
		sort.Strings(matchers)
		groups := make([]clineHookGroup, 0, len(matchers))
		for _, m := range matchers {
			groups = append(groups, clineHookGroup{
				Matcher: m,
				Hooks:   buckets[event][m],
			})
		}
		doc := clineHookFile{Hooks: groups}
		content, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			// Encoding fixed-shape structs cannot fail in practice; surface
			// any breach as an empty file rather than dropping the op.
			content = []byte("{}")
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "hooks", event+".json")),
			Content: string(content) + "\n",
			Mode:    plugin.ModeWrite,
			Sources: []string{"hooks.yaml"},
			Plugin:  "cline",
		})
	}
	return ops
}

// clineMCPServerJSON is the schema Cline expects for entries under
// `cline_mcp_settings.json`'s `mcpServers` map. Identical to Claude's
// .mcp.json shape, by convention.
type clineMCPServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// buildClineMCPOp emits an OpMerge for .cline/cline_mcp_settings.json.
// The op carries a Merger closure that merges proj.MCP into any existing
// file at that path, preserving unrelated keys the user may have added.
// Mirrors plugins/claude.go's buildMCPOp pattern.
func buildClineMCPOp(proj *model.Project) (plugin.Operation, error) {
	mcpRel := filepath.ToSlash(filepath.Join(".cline", "cline_mcp_settings.json"))

	var warnings []plugin.Warning
	for _, srv := range proj.MCP {
		if srv == nil || srv.ScopePath == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   srv.ScopePath + "/mcp.yaml",
			Message:  fmt.Sprintf("scoped MCP server %q from %s/mcp.yaml applied project-wide; Cline has no per-scope MCP", srv.Name, srv.ScopePath),
			Severity: "info",
		})
	}

	merger := func(existing []byte) (string, error) {
		doc := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &doc); err != nil {
				return "", fmt.Errorf("cline: parsing existing %s: %w", mcpRel, err)
			}
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		for _, srv := range proj.MCP {
			if srv == nil || srv.Name == "" {
				continue
			}
			entry := clineMCPServerJSON{
				Command: srv.Command,
				Args:    srv.Args,
				Env:     srv.Env,
			}
			if srv.Command == "" && srv.URL != "" {
				entry.URL = srv.URL
			}
			servers[srv.Name] = entry
		}
		doc["mcpServers"] = servers
		content, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		return string(content) + "\n", nil
	}

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     mcpRel,
		Mode:     plugin.ModeWrite,
		Sources:  []string{"mcp.yaml"},
		Plugin:   "cline",
		Warnings: warnings,
		Merger:   merger,
	}, nil
}

// renderClineScope builds the markdown body for a scope rule file with
// YAML frontmatter: `paths:` carries the scope's glob array (native
// scope enforcement), `description:` carries the human-readable trigger
// hint. The body keeps the legacy `## When working in <path>` preamble
// so the model sees the scope label even if it ignores frontmatter.
func renderClineScope(scope *model.Scope, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if scope.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", yamlScalar(scope.Description))
	}
	globs := scope.Globs
	if len(globs) == 0 && scope.Path != "" {
		globs = []string{scope.Path + "/**"}
	}
	if len(globs) > 0 {
		b.WriteString("paths:\n")
		for _, g := range globs {
			fmt.Fprintf(&b, "  - %s\n", yamlScalar(g))
		}
	}
	b.WriteString("---\n\n")
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
// the skill has a non-empty Globs slice (or inherits a scope), the
// frontmatter carries them as `paths:` so the rule loads only on
// matching files; otherwise no frontmatter is emitted.
func renderClineSkill(skill *model.Skill, body string) string {
	var b strings.Builder
	hasFrontmatter := skill.Description != "" || len(skill.Globs) > 0 || skill.ScopePath != ""
	if hasFrontmatter {
		b.WriteString("---\n")
		if skill.Description != "" {
			fmt.Fprintf(&b, "description: %s\n", yamlScalar(skill.Description))
		}
		globs := skill.Globs
		if len(globs) == 0 && skill.ScopePath != "" {
			globs = []string{skill.ScopePath + "/**"}
		}
		if len(globs) > 0 {
			b.WriteString("paths:\n")
			for _, g := range globs {
				fmt.Fprintf(&b, "  - %s\n", yamlScalar(g))
			}
		}
		b.WriteString("---\n\n")
	}
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

// renderClineWorkflow builds the markdown body for a workflow file at
// .clinerules/workflows/<name>.md. Cline reads the file when the user
// types `/<filename>` (sans extension) and replays the body into the
// next user turn. We retain a `## Command /<name>` header so the body
// is self-describing when read directly.
func renderClineWorkflow(cmd *model.Command, body string) string {
	var b strings.Builder
	hasFrontmatter := cmd.Description != ""
	if hasFrontmatter {
		b.WriteString("---\n")
		fmt.Fprintf(&b, "description: %s\n", yamlScalar(cmd.Description))
		b.WriteString("---\n\n")
	}
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

// yamlScalar quotes a string for safe inclusion as a YAML scalar value
// in our hand-rolled frontmatter writer. Strings containing characters
// that YAML would interpret (`:`, `#`, leading/trailing whitespace, or
// newlines) are wrapped in double quotes with " and \ escaped; plain
// inputs are emitted unquoted. We intentionally keep this minimal
// rather than pulling in a full YAML emitter, since the inputs are
// human-authored description / glob strings, not arbitrary data.
func yamlScalar(s string) string {
	needsQuote := false
	if s == "" {
		return "\"\""
	}
	if strings.ContainsAny(s, ":#\n\r\t\"") {
		needsQuote = true
	}
	if s[0] == ' ' || s[len(s)-1] == ' ' || s[0] == '-' || s[0] == '?' || s[0] == '!' || s[0] == '&' || s[0] == '*' || s[0] == '[' || s[0] == ']' || s[0] == '{' || s[0] == '}' || s[0] == '|' || s[0] == '>' || s[0] == '%' || s[0] == '@' || s[0] == '`' {
		needsQuote = true
	}
	if !needsQuote {
		return s
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\t", `\t`)
	return "\"" + escaped + "\""
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
