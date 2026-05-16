// gemini.go: imports GEMINI.md (root + nested cascade) and
// .gemini/settings.json (mcpServers map) into the canonical
// *model.Project shape.
//
// The cascade is structurally identical to CLAUDE.md — every directory
// can hold its own GEMINI.md and the tree of context files matches the
// tree of scopes.

package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

	mcps, err := readGeminiSettingsMCP(filepath.Join(root, ".gemini", "settings.json"))
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = mcps

	return proj, nil, nil
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
