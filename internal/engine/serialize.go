package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/scope"

	"gopkg.in/yaml.v3"
)

// serializeProject writes a *model.Project to disk under <root>/.agents/.
// This is the inverse of the parser: it's what `agents init --from <tool>`
// uses after importers produce a canonical Project from a source tool.
//
// The given root must NOT already contain a .agents/ directory; caller
// (engine.initProject) checks this and errors before calling.
//
// Returns the list of created paths (relative to root) for caller reporting.
func serializeProject(root string, p *model.Project) ([]string, error) {
	agentsDir := filepath.Join(root, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: mkdir %s: %w", agentsDir, err)
	}

	var created []string
	writeFile := func(rel string, content []byte) error {
		full := filepath.Join(agentsDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return err
		}
		created = append(created, filepath.Join(".agents", rel))
		return nil
	}

	// Root context
	if p.Context != nil {
		body := p.Context.Body
		if body == "" || !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if err := writeFile("context.md", []byte(body)); err != nil {
			return nil, err
		}
	}

	// Scopes (sorted for determinism)
	scopes := append([]*model.Scope{}, p.Scopes...)
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Path < scopes[j].Path })
	for _, s := range scopes {
		if s.Document == nil {
			continue
		}
		if !scope.SafePath(s.Path) {
			return nil, fmt.Errorf("engine: refusing unsafe scope path %q", s.Path)
		}
		body := s.Document.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if err := writeFile(filepath.Join(s.Path, "context.md"), []byte(body)); err != nil {
			return nil, err
		}
		if len(s.Globs) > 0 || s.Description != "" || s.Priority == model.PriorityHigh {
			y, err := scopesYAML(s)
			if err != nil {
				return nil, err
			}
			if err := writeFile(filepath.Join(s.Path, "scopes.yaml"), y); err != nil {
				return nil, err
			}
		}
	}

	// Skills (with frontmatter)
	skills := append([]*model.Skill{}, p.Skills...)
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].ScopePath != skills[j].ScopePath {
			return skills[i].ScopePath < skills[j].ScopePath
		}
		return skills[i].Name < skills[j].Name
	})
	for _, sk := range skills {
		if sk.Document == nil {
			continue
		}
		dir := filepath.Join("skills", sk.Name)
		if sk.ScopePath != "" {
			if !scope.SafePath(sk.ScopePath) {
				return nil, fmt.Errorf("engine: refusing unsafe scope path %q for skill %q", sk.ScopePath, sk.Name)
			}
			dir = filepath.Join(sk.ScopePath, "skills", sk.Name)
		}
		body := renderSkillBody(sk)
		if err := writeFile(filepath.Join(dir, "SKILL.md"), []byte(body)); err != nil {
			return nil, err
		}
	}

	// Commands (with frontmatter)
	cmds := append([]*model.Command{}, p.Commands...)
	sort.Slice(cmds, func(i, j int) bool {
		if cmds[i].ScopePath != cmds[j].ScopePath {
			return cmds[i].ScopePath < cmds[j].ScopePath
		}
		return cmds[i].Name < cmds[j].Name
	})
	for _, c := range cmds {
		if c.Document == nil {
			continue
		}
		path := filepath.Join("commands", c.Name+".md")
		if c.ScopePath != "" {
			if !scope.SafePath(c.ScopePath) {
				return nil, fmt.Errorf("engine: refusing unsafe scope path %q for command %q", c.ScopePath, c.Name)
			}
			path = filepath.Join(c.ScopePath, "commands", c.Name+".md")
		}
		body := renderCommandBody(c)
		if err := writeFile(path, []byte(body)); err != nil {
			return nil, err
		}
	}

	// Agents (subagents) with frontmatter
	agentDocs := append([]*model.Agent{}, p.Agents...)
	sort.Slice(agentDocs, func(i, j int) bool {
		if agentDocs[i].ScopePath != agentDocs[j].ScopePath {
			return agentDocs[i].ScopePath < agentDocs[j].ScopePath
		}
		return agentDocs[i].Name < agentDocs[j].Name
	})
	for _, a := range agentDocs {
		if a.Document == nil {
			continue
		}
		path := filepath.Join("agents", a.Name+".md")
		if a.ScopePath != "" {
			if !scope.SafePath(a.ScopePath) {
				return nil, fmt.Errorf("engine: refusing unsafe scope path %q for agent %q", a.ScopePath, a.Name)
			}
			path = filepath.Join(a.ScopePath, "agents", a.Name+".md")
		}
		body := renderAgentBody(a)
		if err := writeFile(path, []byte(body)); err != nil {
			return nil, err
		}
	}

	// Global hooks → hooks/<event>-<basename>.yaml
	// Scoped hooks → <scope>/hooks/<basename>.yaml
	for i, h := range p.Hooks {
		if h == nil {
			continue
		}
		base := hookFilename(h, i)
		dir := "hooks"
		if h.ScopePath != "" {
			if !scope.SafePath(h.ScopePath) {
				return nil, fmt.Errorf("engine: refusing unsafe scope path %q for hook %d", h.ScopePath, i)
			}
			dir = filepath.Join(h.ScopePath, "hooks")
		}
		y, err := hookYAML(h)
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(dir, base), y); err != nil {
			return nil, err
		}
	}

	// permissions.yaml (global)
	if p.Permissions != nil {
		y, err := permissionsYAML(p.Permissions)
		if err != nil {
			return nil, err
		}
		if err := writeFile("permissions.yaml", y); err != nil {
			return nil, err
		}
	}

	// Scoped permissions → <scope>/permissions.yaml
	for _, sp := range p.ScopedPermissions {
		if sp == nil || sp.ScopePath == "" {
			continue
		}
		if !scope.SafePath(sp.ScopePath) {
			return nil, fmt.Errorf("engine: refusing unsafe scope path %q for scoped permissions", sp.ScopePath)
		}
		y, err := permissionsYAML(sp)
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(sp.ScopePath, "permissions.yaml"), y); err != nil {
			return nil, err
		}
	}

	// Global MCP → mcp.yaml
	globalMCP := make([]*model.MCPServer, 0, len(p.MCP))
	scopedMCP := map[string][]*model.MCPServer{}
	for _, m := range p.MCP {
		if m == nil {
			continue
		}
		if m.ScopePath == "" {
			globalMCP = append(globalMCP, m)
		} else {
			if !scope.SafePath(m.ScopePath) {
				return nil, fmt.Errorf("engine: refusing unsafe scope path %q for MCP %q", m.ScopePath, m.Name)
			}
			scopedMCP[m.ScopePath] = append(scopedMCP[m.ScopePath], m)
		}
	}
	if len(globalMCP) > 0 {
		y, err := mcpYAML(globalMCP)
		if err != nil {
			return nil, err
		}
		if err := writeFile("mcp.yaml", y); err != nil {
			return nil, err
		}
	}
	// Scoped MCP → <scope>/mcp.yaml
	scopedPaths := make([]string, 0, len(scopedMCP))
	for k := range scopedMCP {
		scopedPaths = append(scopedPaths, k)
	}
	sort.Strings(scopedPaths)
	for _, sp := range scopedPaths {
		y, err := mcpYAML(scopedMCP[sp])
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(sp, "mcp.yaml"), y); err != nil {
			return nil, err
		}
	}

	return created, nil
}

// renderSkillBody emits a SKILL.md with frontmatter populated from the
// canonical Skill fields, followed by the document body. Frontmatter is
// omitted entirely when no frontmatter-worthy field is set.
func renderSkillBody(sk *model.Skill) string {
	fm := map[string]any{}
	if sk.Description != "" {
		fm["description"] = sk.Description
	}
	if sk.Trigger != "" {
		fm["trigger"] = sk.Trigger
	}
	if len(sk.Globs) > 0 {
		fm["globs"] = sk.Globs
	}
	return renderWithFrontmatter(fm, sk.Document.Body)
}

func renderCommandBody(c *model.Command) string {
	fm := map[string]any{}
	if c.Description != "" {
		fm["description"] = c.Description
	}
	return renderWithFrontmatter(fm, c.Document.Body)
}

func renderAgentBody(a *model.Agent) string {
	fm := map[string]any{}
	if a.Description != "" {
		fm["description"] = a.Description
	}
	return renderWithFrontmatter(fm, a.Document.Body)
}

// renderWithFrontmatter prepends a YAML frontmatter block to body when fm
// has at least one key. Returns body unchanged if fm is empty.
func renderWithFrontmatter(fm map[string]any, body string) string {
	if len(fm) == 0 {
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		return body
	}
	data, err := yaml.Marshal(fm)
	if err != nil {
		// Fall back to body alone — we never want a serialize to crash on
		// frontmatter quirks; the parser will treat the missing frontmatter
		// as the user just not having set those fields.
		return body
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(data)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// hookFilename produces a deterministic filename for a hook, using the
// script basename when available, falling back to event-<index>.yaml.
func hookFilename(h *model.Hook, idx int) string {
	if h.ScriptPath != "" {
		base := filepath.Base(h.ScriptPath)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if base != "" && base != "." {
			return base + ".yaml"
		}
	}
	return fmt.Sprintf("%s-%d.yaml", strings.ToLower(h.Event), idx)
}

func hookYAML(h *model.Hook) ([]byte, error) {
	doc := map[string]any{
		"event":  h.Event,
		"script": h.ScriptPath,
	}
	if h.Matcher != "" {
		doc["matcher"] = h.Matcher
	}
	return yaml.Marshal(doc)
}

func scopesYAML(s *model.Scope) ([]byte, error) {
	doc := map[string]any{}
	if s.Description != "" {
		doc["description"] = s.Description
	}
	if len(s.Globs) > 0 {
		doc["globs"] = s.Globs
	}
	if s.Priority == model.PriorityHigh {
		doc["priority"] = "high"
	}
	return yaml.Marshal(doc)
}

func permissionsYAML(p *model.Permissions) ([]byte, error) {
	doc := map[string]any{}
	if len(p.Allow) > 0 {
		doc["allow"] = p.Allow
	}
	if len(p.Deny) > 0 {
		doc["deny"] = p.Deny
	}
	if len(p.Ask) > 0 {
		doc["ask"] = p.Ask
	}
	return yaml.Marshal(doc)
}

func mcpYAML(servers []*model.MCPServer) ([]byte, error) {
	srv := map[string]any{}
	for _, s := range servers {
		entry := map[string]any{}
		if s.Command != "" {
			entry["command"] = s.Command
		}
		if len(s.Args) > 0 {
			entry["args"] = s.Args
		}
		if len(s.Env) > 0 {
			entry["env"] = s.Env
		}
		if s.URL != "" {
			entry["url"] = s.URL
		}
		srv[s.Name] = entry
	}
	return yaml.Marshal(map[string]any{"servers": srv})
}
