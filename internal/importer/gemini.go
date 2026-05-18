// gemini.go: imports GEMINI.md (root + nested cascade) and
// .gemini/settings.json (mcpServers map) into the canonical
// *model.Project shape.
//
// The cascade is structurally identical to CLAUDE.md — every directory
// can hold its own GEMINI.md and the tree of context files matches the
// tree of scopes.
//
// v0.8.0 additions (mirrors plugins/gemini.go emissions):
//
//   .gemini/agents/<n>.md      → model.Agent (frontmatter + body)
//   .gemini/commands/<n>.toml  → model.Command (TOML; extract `prompt` field)
//   .gemini/settings.json      → also reads `hooks` block alongside mcpServers

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

// GeminiImporter reads GEMINI.md (cascade) and .gemini/settings.json.
type GeminiImporter struct{}

// NewGemini constructs a GeminiImporter.
func NewGemini() *GeminiImporter { return &GeminiImporter{} }

// Name returns "gemini".
func (i *GeminiImporter) Name() string { return "gemini" }

// Detect returns true when .gemini/ or GEMINI.md is present at root.
func (i *GeminiImporter) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".gemini")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "GEMINI.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Import reads root and produces the canonical Project.
func (i *GeminiImporter) Import(root string) (*model.Project, []Warning, error) {
	if !i.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}

	rootDoc, scopes, err := importNestedMarkdown(root, "GEMINI.md", "gemini")
	if err != nil {
		return nil, nil, err
	}
	proj.Context = rootDoc
	proj.Scopes = scopes

	settingsPath := filepath.Join(root, ".gemini", "settings.json")
	mcps, err := readGeminiSettingsMCP(settingsPath)
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = mcps

	hooks, err := readGeminiSettingsHooks(settingsPath)
	if err != nil {
		return nil, nil, err
	}
	proj.Hooks = append(proj.Hooks, hooks...)

	agents, err := readGeminiAgents(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Agents = append(proj.Agents, agents...)

	cmds, err := readGeminiCommands(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Commands = append(proj.Commands, cmds...)

	return proj, nil, nil
}

// readGeminiAgents reads .gemini/agents/<n>.md into []*model.Agent.
func readGeminiAgents(root string) ([]*model.Agent, error) {
	dir := filepath.Join(root, ".gemini", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("gemini: read %s: %w", full, rerr)
		}
		fm, body, perr := splitFrontmatter(data)
		if perr != nil {
			return nil, fmt.Errorf("gemini: %s: %w", full, perr)
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if n, ok := fm["name"].(string); ok && n != "" {
			name = n
		}
		desc, _ := fm["description"].(string)
		out = append(out, &model.Agent{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment("gemini", full) + body,
			},
		})
	}
	return out, nil
}

// readGeminiCommands reads .gemini/commands/<n>.toml into []*model.Command.
// The TOML schema (per plugins/gemini.go:renderGeminiCommand) is minimal:
// `description = "..."` plus a triple-quoted multi-line `prompt`. The prompt
// body becomes the canonical Document.Body (markdown), and triple-quote
// escaping is reversed.
func readGeminiCommands(root string) ([]*model.Command, error) {
	dir := filepath.Join(root, ".gemini", "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Command
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("gemini: read %s: %w", full, rerr)
		}
		desc, body := parseGeminiCommandTOML(string(data))
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		out = append(out, &model.Command{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath: full,
				Body:       provenanceComment("gemini", full) + body,
			},
		})
	}
	return out, nil
}

// parseGeminiCommandTOML extracts the description and prompt body from a
// gemini command file. It is intentionally minimal — the only writer is
// plugins/gemini.go:renderGeminiCommand, whose output it inverts.
func parseGeminiCommandTOML(s string) (description, prompt string) {
	lines := strings.Split(s, "\n")
	const tq = "\"\"\""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "description") {
			eq := strings.Index(trimmed, "=")
			if eq < 0 {
				continue
			}
			rhs := strings.TrimSpace(trimmed[eq+1:])
			if len(rhs) >= 2 && rhs[0] == '"' && rhs[len(rhs)-1] == '"' {
				var decoded string
				if err := json.Unmarshal([]byte(rhs), &decoded); err == nil {
					description = decoded
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "prompt") && strings.Contains(line, tq) {
			var bodyLines []string
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimRight(lines[j], "\r") == tq {
					break
				}
				bodyLines = append(bodyLines, lines[j])
			}
			prompt = strings.Join(bodyLines, "\n")
			prompt = strings.ReplaceAll(prompt, "\"\"\\\"", tq)
			if prompt != "" && !strings.HasSuffix(prompt, "\n") {
				prompt += "\n"
			}
			return description, prompt
		}
	}
	return description, prompt
}

// readGeminiSettingsHooks parses .gemini/settings.json's `hooks` block.
// Same shape as Claude's settings.json: a map of event → []group, each
// group with `matcher` and `hooks: [{type, command, name?, timeout?}]`.
// Commands pointing into __scope-guard__ wrappers are skipped.
func readGeminiSettingsHooks(path string) ([]*model.Hook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		return nil, fmt.Errorf("gemini: parse %s: %w", path, jerr)
	}
	hRaw, ok := raw["hooks"]
	if !ok {
		return nil, nil
	}
	var byEvent map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if jerr := json.Unmarshal(hRaw, &byEvent); jerr != nil {
		return nil, fmt.Errorf("gemini: parse hooks in %s: %w", path, jerr)
	}
	events := make([]string, 0, len(byEvent))
	for ev := range byEvent {
		events = append(events, ev)
	}
	sort.Strings(events)
	var out []*model.Hook
	for _, ev := range events {
		for _, grp := range byEvent[ev] {
			for _, entry := range grp.Hooks {
				if strings.Contains(entry.Command, "__scope-guard__") {
					continue
				}
				out = append(out, &model.Hook{
					Event:      ev,
					Matcher:    grp.Matcher,
					ScriptPath: entry.Command,
				})
			}
		}
	}
	return out, nil
}

// readGeminiSettingsMCP parses .gemini/settings.json and returns the
// mcpServers entries. Returns (nil, nil) if the file is absent.
func readGeminiSettingsMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", path, err)
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
		return nil, fmt.Errorf("gemini: parse %s: %w", path, err)
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

// Compile-time check that *GeminiImporter implements Importer.
var _ Importer = (*GeminiImporter)(nil)
