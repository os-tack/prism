// Package plugins contains the projection plugins that translate a canonical
// .agents/ model into per-tool config files.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// ClaudePlugin projects a model.Project into Claude Code's on-disk layout:
// CLAUDE.md (root + per-scope), .claude/skills/<name>/SKILL.md (+ scripts/),
// .claude/commands/<name>.md, .claude/agents/<name>.md, .claude/settings.json
// (permissions + hooks), and .mcp.json (MCP servers).
type ClaudePlugin struct{}

// NewClaude returns a fresh ClaudePlugin.
func NewClaude() *ClaudePlugin {
	return &ClaudePlugin{}
}

// Name is the stable plugin identifier.
func (p *ClaudePlugin) Name() string {
	return "claude"
}

// Detect returns true if a .claude/ directory or a CLAUDE.md file is present
// at the given project root.
func (p *ClaudePlugin) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".claude")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Capabilities returns the capability matrix entry for Claude Code.
func (p *ClaudePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportDegraded, // no native trigger description; surfaced via IMPORTANT prefix
		Skills:        plugin.SupportNative,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportNative,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportNative,
		MCP:           plugin.SupportNative,
	}
}

// Plan produces the Operations needed to project proj into Claude Code's layout.
//
// Context + scopes → CLAUDE.md (symlink by default, write per opts.Mode).
// Skills → .claude/skills/<name>/SKILL.md (+ scripts/<basename>).
// Commands → .claude/commands/<name>.md.
// Agents → .claude/agents/<name>.md.
// Hooks + Permissions → merged into .claude/settings.json (always write).
// MCP servers → merged into .mcp.json (always write).
func (p *ClaudePlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	mode := plugin.ModeSymlink
	switch opts.Mode {
	case "write":
		mode = plugin.ModeWrite
	case "symlink", "":
		mode = plugin.ModeSymlink
	default:
		return nil, fmt.Errorf("claude: unknown mode %q (want \"write\" or \"symlink\")", opts.Mode)
	}

	if proj == nil {
		return nil, nil
	}

	var ops []plugin.Operation

	// Root CLAUDE.md.
	if proj.Context != nil {
		op, err := buildOp(proj, proj.Context, "CLAUDE.md", mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Per-scope CLAUDE.md files.
	for _, sc := range proj.Scopes {
		if sc == nil || sc.Document == nil {
			continue
		}
		path := filepath.Join(sc.Path, "CLAUDE.md")
		op, err := buildOp(proj, sc.Document, path, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Skills.
	for _, sk := range proj.Skills {
		if sk == nil || sk.Document == nil {
			continue
		}
		skillDir := filepath.Join(".claude", "skills", sk.Name)
		skillPath := filepath.Join(skillDir, "SKILL.md")
		op, err := buildOp(proj, sk.Document, skillPath, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)

		// Each script becomes a symlink under scripts/<basename>.
		for _, scriptPath := range sk.Scripts {
			if scriptPath == "" {
				continue
			}
			scriptOp, err := buildScriptOp(proj, scriptPath, skillDir, mode)
			if err != nil {
				return nil, err
			}
			ops = append(ops, scriptOp)
		}
	}

	// Commands.
	for _, cmd := range proj.Commands {
		if cmd == nil || cmd.Document == nil {
			continue
		}
		path := filepath.Join(".claude", "commands", cmd.Name+".md")
		op, err := buildOp(proj, cmd.Document, path, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Agents.
	for _, ag := range proj.Agents {
		if ag == nil || ag.Document == nil {
			continue
		}
		path := filepath.Join(".claude", "agents", ag.Name+".md")
		op, err := buildOp(proj, ag.Document, path, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// .claude/settings.json (hooks + permissions). Always write.
	if proj.Permissions != nil || len(proj.Hooks) > 0 {
		settingsOp, err := buildSettingsOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, settingsOp)
	}

	// .mcp.json (MCP servers). Always write.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	return ops, nil
}

// buildOp constructs a single Operation for a Document being projected to the
// given target path (relative to project root) in the given Mode.
func buildOp(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode) (plugin.Operation, error) {
	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: []string{proj.SourceTag(doc.SourcePath)},
		Plugin:  "claude",
	}

	if mode == plugin.ModeSymlink {
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, doc.SourcePath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.Kind = plugin.OpSymlink
		op.LinkTarget = linkTarget
	} else {
		op.Kind = plugin.OpWrite
		op.Content = doc.Body
	}

	return op, nil
}

// buildScriptOp constructs an Operation that places a skill script under
// `<skillDir>/scripts/<basename>`. Scripts are symlinks (or copies in write
// mode) pointing at the original absolute path in .agents/.
func buildScriptOp(proj *model.Project, scriptPath, skillDir string, mode plugin.Mode) (plugin.Operation, error) {
	base := filepath.Base(scriptPath)
	targetPath := filepath.Join(skillDir, "scripts", base)

	srcRel := proj.SourceTag(scriptPath)

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: []string{srcRel},
		Plugin:  "claude",
	}

	if mode == plugin.ModeSymlink {
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, scriptPath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.Kind = plugin.OpSymlink
		op.LinkTarget = linkTarget
	} else {
		// In write mode we still emit a symlink — engine may downgrade later.
		// We don't read the script bytes here because plugins are pure.
		op.Kind = plugin.OpSymlink
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, scriptPath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.LinkTarget = linkTarget
	}

	return op, nil
}

// hookEntry mirrors Claude Code's settings.json hook schema:
//
//	{"type": "command", "command": "<absolute-script-path>"}
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// hookGroup mirrors Claude Code's settings.json hook group schema:
//
//	{"matcher": "...", "hooks": [{"type": "command", "command": "..."}]}
type hookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// buildSettingsOp merges proj.Permissions and proj.Hooks into any existing
// .claude/settings.json at proj.Root. Returns an OpMerge with the fully
// merged content (plugin produces the final bytes; the engine's OpMerge
// handler currently treats it as a write).
func buildSettingsOp(proj *model.Project) (plugin.Operation, error) {
	settingsPath := filepath.Join(proj.Root, ".claude", "settings.json")

	// Start with whatever the user already has.
	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return plugin.Operation{}, fmt.Errorf("claude: parsing existing %s: %w", settingsPath, err)
		}
	}

	// Merge permissions. Only overwrite the keys we own if our slices are
	// non-empty; leave the user's value alone otherwise.
	if proj.Permissions != nil {
		perms, _ := settings["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		if len(proj.Permissions.Allow) > 0 {
			perms["allow"] = proj.Permissions.Allow
		}
		if len(proj.Permissions.Deny) > 0 {
			perms["deny"] = proj.Permissions.Deny
		}
		if len(proj.Permissions.Ask) > 0 {
			perms["ask"] = proj.Permissions.Ask
		}
		settings["permissions"] = perms
	}

	// Merge hooks: group by Event, then by Matcher within each event.
	if len(proj.Hooks) > 0 {
		hooksRoot, _ := settings["hooks"].(map[string]any)
		if hooksRoot == nil {
			hooksRoot = map[string]any{}
		}

		// Bucket: event → matcher → []hookEntry
		buckets := map[string]map[string][]hookEntry{}
		eventOrder := []string{}
		for _, h := range proj.Hooks {
			if h == nil || h.Event == "" {
				continue
			}
			if _, ok := buckets[h.Event]; !ok {
				buckets[h.Event] = map[string][]hookEntry{}
				eventOrder = append(eventOrder, h.Event)
			}
			buckets[h.Event][h.Matcher] = append(buckets[h.Event][h.Matcher], hookEntry{
				Type:    "command",
				Command: h.ScriptPath,
			})
		}

		for _, event := range eventOrder {
			matcherMap := buckets[event]
			// Stable order for matchers for deterministic output.
			matchers := make([]string, 0, len(matcherMap))
			for m := range matcherMap {
				matchers = append(matchers, m)
			}
			sort.Strings(matchers)

			var groups []hookGroup
			for _, m := range matchers {
				groups = append(groups, hookGroup{
					Matcher: m,
					Hooks:   matcherMap[m],
				})
			}
			hooksRoot[event] = groups
		}
		settings["hooks"] = hooksRoot
	}

	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return plugin.Operation{}, err
	}

	// Synthesize sources from contributing inputs.
	var sources []string
	if proj.Permissions != nil {
		sources = append(sources, "permissions.yaml")
	}
	if len(proj.Hooks) > 0 {
		sources = append(sources, "hooks.yaml")
	}

	return plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    filepath.Join(".claude", "settings.json"),
		Content: string(content) + "\n",
		Mode:    plugin.ModeWrite,
		Sources: sources,
		Plugin:  "claude",
	}, nil
}

// mcpServerJSON is the schema Claude Code expects for entries under
// `.mcp.json`'s `mcpServers` map.
type mcpServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// buildMCPOp merges proj.MCP into any existing .mcp.json at proj.Root.
func buildMCPOp(proj *model.Project) (plugin.Operation, error) {
	mcpPath := filepath.Join(proj.Root, ".mcp.json")

	doc := map[string]any{}
	if data, err := os.ReadFile(mcpPath); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return plugin.Operation{}, fmt.Errorf("claude: parsing existing %s: %w", mcpPath, err)
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
		entry := mcpServerJSON{
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
		return plugin.Operation{}, err
	}

	return plugin.Operation{
		Kind:    plugin.OpWrite,
		Path:    ".mcp.json",
		Content: string(content) + "\n",
		Mode:    plugin.ModeWrite,
		Sources: []string{"mcp.yaml"},
		Plugin:  "claude",
	}, nil
}

// Compile-time check that ClaudePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ClaudePlugin)(nil)
