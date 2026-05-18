package importer

import (
	"path/filepath"
	"testing"
)

// TestCopilot_V08_AgentsAndMCP covers .github/agents/<n>.agent.md plus
// .github/mcp.json and root .mcp.json.
func TestCopilot_V08_AgentsAndMCP(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".github"))

	mustWrite(t, filepath.Join(root, ".github", "agents", "reviewer.agent.md"),
		"---\nname: reviewer\ndescription: Code reviewer\n---\nReview thoroughly.\n")

	// Project-local .github/mcp.json wins over root .mcp.json on collision.
	mustWrite(t, filepath.Join(root, ".github", "mcp.json"),
		`{"mcpServers": {"linear": {"command": "npx", "args": ["@linear/local"]}}}`)
	mustWrite(t, filepath.Join(root, ".mcp.json"),
		`{"mcpServers": {"linear": {"command": "npx", "args": ["@linear/root"]}, "github": {"command": "gh"}}}`)

	proj, _, err := NewCopilot().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(proj.Agents) != 1 || proj.Agents[0].Name != "reviewer" {
		t.Errorf("Agents = %+v", proj.Agents)
	}
	if d := proj.Agents[0].Description; d != "Code reviewer" {
		t.Errorf("agent Description = %q", d)
	}

	// MCP: both linear (overridden) and github (from root) must be present.
	byName := map[string]string{}
	for _, s := range proj.MCP {
		if len(s.Args) > 0 {
			byName[s.Name] = s.Args[0]
		} else {
			byName[s.Name] = s.Command
		}
	}
	if byName["linear"] != "@linear/local" {
		t.Errorf("linear args[0] = %q, want @linear/local (project-local overrides root)", byName["linear"])
	}
	if byName["github"] != "gh" {
		t.Errorf("github command = %q, want gh (root-only entry should survive)", byName["github"])
	}
}
