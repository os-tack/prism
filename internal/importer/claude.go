// claude.go: imports Claude Code's on-disk layout into the canonical
// *model.Project shape. This is the inverse of plugins/claude.go.
//
// What we read:
//   CLAUDE.md                             → root context document
//   <subdir>/CLAUDE.md                    → scope at <subdir>/
//   .claude/skills/<name>/SKILL.md        → skill (with optional scripts/)
//   .claude/commands/<name>.md            → command
//   .claude/agents/<name>.md              → agent (subagent prompt)
//   .claude/settings.json                 → permissions + hooks
//   .mcp.json                             → MCP servers
//
// The existing --from claude walker at engine/init.go:62-94 is NOT
// removed by this file; the engine integration pass swaps it for a call
// into this importer.

package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
)

// ClaudeImporter reads CLAUDE.md (cascade), .claude/, and .mcp.json.
type ClaudeImporter struct{}

// NewClaude constructs a ClaudeImporter.
func NewClaude() *ClaudeImporter { return &ClaudeImporter{} }

// Name returns "claude".
func (i *ClaudeImporter) Name() string { return "claude" }

// Detect returns true when .claude/ or CLAUDE.md is present at root.
func (i *ClaudeImporter) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".claude")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Import reads root and produces the canonical Project.
func (i *ClaudeImporter) Import(root string) (*model.Project, []Warning, error) {
	if !i.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	// 1. Root + nested CLAUDE.md.
	rootDoc, scopes, err := importNestedMarkdown(root, "CLAUDE.md", "claude")
	if err != nil {
		return nil, nil, err
	}
	proj.Context = rootDoc
	proj.Scopes = scopes

	// 2. Skills: .claude/skills/<name>/SKILL.md plus optional scripts/.
	skills, skillWarnings, err := importClaudeSkills(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Skills = skills
	warnings = append(warnings, skillWarnings...)

	// 3. Commands: .claude/commands/<name>.md.
	commands, err := importClaudeCommands(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Commands = commands

	// 4. Agents: .claude/agents/<name>.md.
	agents, err := importClaudeAgents(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Agents = agents

	// 5. Permissions + hooks: .claude/settings.json.
	perms, hooks, settingsWarnings, err := importClaudeSettings(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Permissions = perms
	proj.Hooks = hooks
	warnings = append(warnings, settingsWarnings...)

	// 6. MCP servers: .mcp.json.
	mcps, err := importClaudeMCP(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = mcps

	return proj, warnings, nil
}

// importClaudeSkills walks .claude/skills/<name>/SKILL.md and turns each
// into a *model.Skill. Scripts under .claude/skills/<name>/scripts/ are
// recorded as absolute paths on Skill.Scripts.
func importClaudeSkills(root string) ([]*model.Skill, []Warning, error) {
	skillsDir := filepath.Join(root, ".claude", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("claude: read %s: %w", skillsDir, err)
	}

	var skills []*model.Skill
	var warnings []Warning
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(skillsDir, e.Name())
		skillMD := filepath.Join(skillDir, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				warnings = append(warnings, Warning{
					SourcePath: filepath.ToSlash(filepath.Join(".claude", "skills", e.Name())),
					Heuristic:  "skill directory has no SKILL.md; skipping",
					Severity:   "warn",
				})
				continue
			}
			return nil, nil, fmt.Errorf("claude: read %s: %w", skillMD, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("claude: %s: %w", skillMD, err)
		}

		s := &model.Skill{
			Name: e.Name(),
			Document: &model.Document{
				SourcePath:  skillMD,
				Frontmatter: fm,
				Body:        provenanceComment("claude", skillMD) + body,
			},
		}
		if fm != nil {
			if v, ok := fm["description"].(string); ok {
				s.Description = v
			}
			if v, ok := fm["trigger"].(string); ok {
				s.Trigger = v
			}
			s.Globs = stringSliceAny(fm["globs"])
		}

		scripts, err := collectClaudeScripts(filepath.Join(skillDir, "scripts"))
		if err != nil {
			return nil, nil, err
		}
		s.Scripts = scripts

		skills = append(skills, s)
	}
	sort.Slice(skills, func(a, b int) bool { return skills[a].Name < skills[b].Name })
	return skills, warnings, nil
}

// collectClaudeScripts returns sorted absolute paths to every regular
// file under scriptsDir. Mirrors parser.collectScripts so the imported
// shape matches what Parse would produce.
func collectClaudeScripts(scriptsDir string) ([]string, error) {
	info, err := os.Stat(scriptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: stat %s: %w", scriptsDir, err)
	}
	if !info.IsDir() {
		return nil, nil
	}
	var scripts []string
	err = filepath.WalkDir(scriptsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeType != 0 {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		scripts = append(scripts, abs)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claude: walk %s: %w", scriptsDir, err)
	}
	sort.Strings(scripts)
	return scripts, nil
}

// importClaudeCommands walks .claude/commands/<name>.md.
func importClaudeCommands(root string) ([]*model.Command, error) {
	dir := filepath.Join(root, ".claude", "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: read %s: %w", dir, err)
	}
	var cmds []*model.Command
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("claude: read %s: %w", path, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, fmt.Errorf("claude: %s: %w", path, err)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		c := &model.Command{
			Name: name,
			Document: &model.Document{
				SourcePath:  path,
				Frontmatter: fm,
				Body:        provenanceComment("claude", path) + body,
			},
		}
		if fm != nil {
			if v, ok := fm["description"].(string); ok {
				c.Description = v
			}
		}
		cmds = append(cmds, c)
	}
	sort.Slice(cmds, func(a, b int) bool { return cmds[a].Name < cmds[b].Name })
	return cmds, nil
}

// importClaudeAgents walks .claude/agents/<name>.md.
func importClaudeAgents(root string) ([]*model.Agent, error) {
	dir := filepath.Join(root, ".claude", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: read %s: %w", dir, err)
	}
	var ags []*model.Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("claude: read %s: %w", path, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, fmt.Errorf("claude: %s: %w", path, err)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		a := &model.Agent{
			Name: name,
			Document: &model.Document{
				SourcePath:  path,
				Frontmatter: fm,
				Body:        provenanceComment("claude", path) + body,
			},
		}
		if fm != nil {
			if v, ok := fm["description"].(string); ok {
				a.Description = v
			}
		}
		ags = append(ags, a)
	}
	sort.Slice(ags, func(a, b int) bool { return ags[a].Name < ags[b].Name })
	return ags, nil
}

// claudeSettingsHookEntry is the shape inside settings.json["hooks"][event][n].hooks[m].
type claudeSettingsHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// claudeSettingsHookGroup mirrors settings.json["hooks"][event][n].
type claudeSettingsHookGroup struct {
	Matcher string                    `json:"matcher"`
	Hooks   []claudeSettingsHookEntry `json:"hooks"`
}

// importClaudeSettings reads .claude/settings.json and pulls out
// permissions + hooks into the canonical model. Returns nil values for
// missing pieces (the engine treats nil-permissions as "no permissions
// configured").
func importClaudeSettings(root string) (*model.Permissions, []*model.Hook, []Warning, error) {
	settingsPath := filepath.Join(root, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("claude: read %s: %w", settingsPath, err)
	}
	if len(data) == 0 {
		return nil, nil, nil, nil
	}

	// We parse loosely: settings.json may contain user-managed keys we
	// don't care about; only `permissions` and `hooks` are relevant.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, nil, fmt.Errorf("claude: parse %s: %w", settingsPath, err)
	}

	var perms *model.Permissions
	if pRaw, ok := raw["permissions"]; ok {
		var p struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
			Ask   []string `json:"ask"`
		}
		if err := json.Unmarshal(pRaw, &p); err != nil {
			return nil, nil, nil, fmt.Errorf("claude: parse permissions in %s: %w", settingsPath, err)
		}
		if len(p.Allow) > 0 || len(p.Deny) > 0 || len(p.Ask) > 0 {
			perms = &model.Permissions{
				Allow: p.Allow,
				Deny:  p.Deny,
				Ask:   p.Ask,
			}
		}
	}

	var hooks []*model.Hook
	var warnings []Warning
	if hRaw, ok := raw["hooks"]; ok {
		// hooks is a map of event → []hookGroup.
		var byEvent map[string][]claudeSettingsHookGroup
		if err := json.Unmarshal(hRaw, &byEvent); err != nil {
			return nil, nil, nil, fmt.Errorf("claude: parse hooks in %s: %w", settingsPath, err)
		}
		// Sort events for deterministic output.
		events := make([]string, 0, len(byEvent))
		for ev := range byEvent {
			events = append(events, ev)
		}
		sort.Strings(events)
		for _, ev := range events {
			for _, grp := range byEvent[ev] {
				for _, entry := range grp.Hooks {
					if entry.Type != "" && entry.Type != "command" {
						warnings = append(warnings, Warning{
							SourcePath: ".claude/settings.json",
							Heuristic:  fmt.Sprintf("hook entry type %q is not 'command'; canonical model only supports command hooks — dropping", entry.Type),
							Severity:   "warn",
						})
						continue
					}
					hooks = append(hooks, &model.Hook{
						Event:      ev,
						Matcher:    grp.Matcher,
						ScriptPath: entry.Command,
					})
				}
			}
		}
	}

	return perms, hooks, warnings, nil
}

// importClaudeMCP reads .mcp.json and returns the canonical MCP server
// slice. Returns (nil, nil) when the file is absent or empty.
func importClaudeMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("claude: parse %s: %w", path, err)
	}
	names := make([]string, 0, len(raw.MCPServers))
	for n := range raw.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		s := raw.MCPServers[n]
		out = append(out, &model.MCPServer{
			Name:    n,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
		})
	}
	return out, nil
}

// Compile-time check that *ClaudeImporter implements Importer.
var _ Importer = (*ClaudeImporter)(nil)
