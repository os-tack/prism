// Package plugins contains the projection plugins shipped with agents.
//
// CursorPlugin projects a canonical .agents/ directory into Cursor's
// `.cursor/rules/*.mdc` rule file format. Each .mdc file consists of YAML
// frontmatter (delimited by `---`) followed by a markdown body. The
// frontmatter is used by Cursor to decide when to auto-attach a rule:
//   - alwaysApply: always include the rule
//   - globs:       attach when matching files are in context
//   - description: attach when the description matches the current task
//
// CursorPlugin also emits a native `.cursor/mcp.json` for MCP server
// configuration (merged with any pre-existing file at the project root).
// Skills are projected as degraded scoped rules (no script execution).
// Commands, Agents, Hooks, and Permissions have no Cursor analog and only
// emit informational warnings.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// CursorPlugin projects Project state into `.cursor/rules/*.mdc` files and
// `.cursor/mcp.json`.
type CursorPlugin struct{}

// NewCursor constructs a CursorPlugin.
//
// The plugins package hosts multiple plugins that each want a `New`
// constructor, which would collide at package scope. We expose
// `NewCursor` as the canonical constructor for this plugin.
func NewCursor() *CursorPlugin { return &CursorPlugin{} }

// Name returns the stable plugin identifier.
func (p *CursorPlugin) Name() string { return "cursor" }

// Detect returns true if the project at root looks like it uses Cursor.
// We treat the presence of `.cursor/` (the modern rules dir) OR the legacy
// `.cursorrules` file at the project root as activation signals.
func (p *CursorPlugin) Detect(root string) bool {
	if info, err := os.Stat(filepath.Join(root, ".cursor")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, ".cursorrules")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// Capabilities returns Cursor's capability matrix.
//
// Cursor natively supports per-glob rule attachment (ScopePaths),
// description-triggered attachment (ScopeSemantic), and MCP server
// configuration via `.cursor/mcp.json`. Skills and Commands degrade
// (skills become scoped rules with no script execution; commands have no
// analog at all). Agents, Hooks, and Permissions are unsupported.
func (p *CursorPlugin) Capabilities() plugin.Capabilities {
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

// Plan produces the Operations needed to project proj into `.cursor/`.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Cursor projection never symlinks — the .mdc files are not byte-identical
// to source (frontmatter is injected) and `.cursor/mcp.json` is a merged
// file. Unknown modes return an error.
func (p *CursorPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	// Validate mode early. Empty and "write" are accepted; anything else
	// is a programming error from the caller.
	switch opts.Mode {
	case "", "write":
		// ok
	default:
		return nil, fmt.Errorf("cursor: unsupported mode %q", opts.Mode)
	}

	var ops []plugin.Operation

	// Root context → .cursor/rules/_root.mdc with alwaysApply: true.
	if proj.Context != nil {
		content := renderMDC("Project-wide context", nil, true, proj.Context.Body)
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".cursor/rules/_root.mdc",
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		}
		ops = append(ops, op)
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
		content := renderMDC(desc, scope.Globs, false, body)
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".cursor", "rules", slugify(scope.Path)+".mdc")),
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		ops = append(ops, op)
	}

	// Skills → degraded scoped rule files at .cursor/rules/skill-<slug>.mdc.
	// Each skill becomes a rule that loads when description matches user
	// intent or files match globs. Scripts are dropped with a warning.
	var skillWarnings []plugin.Warning
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
		content := renderMDC(desc, skill.Globs, false, body)
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".cursor", "rules", "skill-"+skillSlug(skill.Name)+".mdc")),
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
				Message:  fmt.Sprintf("Cursor has no skill primitive — projected as a scoped rule with no script execution. Scripts ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
			// Track the warning for the aggregate fallback too — but it's
			// already attached to the skill op itself, so don't duplicate.
			_ = skillWarnings
		}
		ops = append(ops, op)
	}

	// Collect degradation/unsupported warnings for capability types we
	// do not project. Each warning is severity=info — these are not
	// errors, just transparency about what got dropped.
	var warnings []plugin.Warning
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		src := ""
		if cmd.Document != nil {
			src = proj.SourceTag(cmd.Document.SourcePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   src,
			Message:  fmt.Sprintf("Cursor has no slash-command equivalent; %s not projected.", cmd.Name),
			Severity: "info",
		})
	}
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
			Message:  fmt.Sprintf("Cursor has no subagent primitive; %s not projected.", agent.Name),
			Severity: "info",
		})
	}
	for _, hook := range proj.Hooks {
		if hook == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   hook.ScriptPath,
			Message:  fmt.Sprintf("Cursor has no hook primitive; %s:%s not projected.", hook.Event, hook.Matcher),
			Severity: "info",
		})
	}
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  "Cursor has no permissions primitive; permissions not projected.",
				Severity: "info",
			})
		}
	}

	// MCP → `.cursor/mcp.json` (native). Merge with any existing file at
	// proj.Root/.cursor/mcp.json so user-managed keys survive.
	if len(proj.MCP) > 0 {
		mcpOp, err := p.buildMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	// Attach unsupported-type warnings to the first available op.
	// Preference: root op > first scope op > first skill op > mcp op.
	// If no op exists at all, the warnings have nowhere to land — drop them.
	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// buildMCPOp constructs the OpMerge operation for `.cursor/mcp.json`. It
// reads any existing file at <root>/.cursor/mcp.json, decodes it as a
// generic JSON object, and overlays the `mcpServers` key with the contents
// of proj.MCP. All other top-level keys in the existing file are preserved.
func (p *CursorPlugin) buildMCPOp(proj *model.Project) (plugin.Operation, error) {
	// Existing file — parse if present, else start from {}.
	existing := map[string]any{}
	if proj.Root != "" {
		raw, err := os.ReadFile(filepath.Join(proj.Root, ".cursor", "mcp.json"))
		if err == nil {
			if jerr := json.Unmarshal(raw, &existing); jerr != nil {
				return plugin.Operation{}, fmt.Errorf("cursor: parse existing .cursor/mcp.json: %w", jerr)
			}
		} else if !os.IsNotExist(err) {
			return plugin.Operation{}, fmt.Errorf("cursor: read existing .cursor/mcp.json: %w", err)
		}
	}

	// Build mcpServers map from proj.MCP. Emit only non-empty fields.
	servers := map[string]any{}
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		entry := map[string]any{}
		if srv.Command != "" {
			entry["command"] = srv.Command
		}
		if len(srv.Args) > 0 {
			entry["args"] = srv.Args
		}
		if len(srv.Env) > 0 {
			// Sort env keys for deterministic output.
			env := map[string]string{}
			for k, v := range srv.Env {
				env[k] = v
			}
			entry["env"] = env
		}
		if srv.URL != "" {
			entry["url"] = srv.URL
		}
		servers[srv.Name] = entry
	}
	existing["mcpServers"] = servers

	// Marshal with stable key ordering. encoding/json sorts map keys
	// alphabetically by default, so this is already deterministic, but we
	// pretty-print for human readability.
	content, err := marshalJSONStable(existing)
	if err != nil {
		return plugin.Operation{}, fmt.Errorf("cursor: marshal .cursor/mcp.json: %w", err)
	}

	return plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    ".cursor/mcp.json",
		Content: content,
		Mode:    plugin.ModeWrite,
		Plugin:  p.Name(),
		Sources: []string{"mcp.yaml"},
	}, nil
}

// marshalJSONStable pretty-prints a JSON value with sorted map keys at all
// levels. encoding/json already sorts top-level map[string]any keys when
// marshaling, but we re-run via json.MarshalIndent and ensure trailing
// newline for diff stability.
func marshalJSONStable(v any) (string, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	out := string(raw)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

// renderMDC formats the YAML frontmatter + markdown body for a .mdc file.
//
// We use encoding/json to emit the globs array because a JSON array of
// strings (e.g. `["src/**","docs/**"]`) is also valid YAML flow-style array
// syntax, and json.Marshal handles escaping for us.
func renderMDC(description string, globs []string, alwaysApply bool, body string) string {
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
			// json.Marshal of []string cannot fail in practice; fall back to empty array.
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

// slugify converts a scope path like "src/billing/api" into a filename-safe
// slug like "src-billing-api". It lowercases the result and replaces path
// separators with dashes.
//
// This duplicates the behavior expected from internal/scope.Slugify; once
// that package lands we can switch over.
func slugify(path string) string {
	s := strings.TrimSpace(path)
	s = strings.Trim(s, "/")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.ReplaceAll(s, "/", "-")
	return s
}

// skillSlug normalizes a skill name into a filename-safe slug. It
// lowercases and replaces non-word characters (anything that is not a
// letter, digit, or underscore) with dashes, collapsing runs of dashes
// and trimming leading/trailing dashes.
var skillSlugRE = regexp.MustCompile(`[^\w]+`)

func skillSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = skillSlugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

