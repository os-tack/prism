package importer

import (
	"path/filepath"
	"testing"
)

// TestGemini_V08_AgentsCommandsHooks covers the v0.8 emissions:
// .gemini/agents/<n>.md, .gemini/commands/<n>.toml, and the `hooks` block
// in .gemini/settings.json.
func TestGemini_V08_AgentsCommandsHooks(t *testing.T) {
	root := t.TempDir()
	// Detect signal — make sure .gemini/ exists at minimum.
	mustMkdir(t, filepath.Join(root, ".gemini"))

	mustWrite(t, filepath.Join(root, ".gemini", "agents", "reviewer.md"),
		"---\nname: reviewer\ndescription: Code reviewer\n---\nReview thoroughly.\n")

	mustWrite(t, filepath.Join(root, ".gemini", "commands", "deploy.toml"),
		"description = \"Ship a release\"\nprompt = \"\"\"\nRun the release script with --confirm.\n\"\"\"\n")

	mustWrite(t, filepath.Join(root, ".gemini", "settings.json"),
		`{
  "mcpServers": {"linear": {"command": "npx", "args": ["@linear/mcp"]}},
  "hooks": {
    "BeforeTool": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "/usr/local/bin/preflight.sh"},
          {"type": "command", "command": "/abs/.gemini/hooks/__scope-guard__/api-x.sh"}
        ]
      }
    ]
  }
}`)

	proj, _, err := NewGemini().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(proj.Agents) != 1 || proj.Agents[0].Name != "reviewer" {
		t.Errorf("Agents = %+v", proj.Agents)
	}

	if len(proj.Commands) != 1 || proj.Commands[0].Name != "deploy" {
		t.Errorf("Commands = %+v", proj.Commands)
	}
	if d := proj.Commands[0].Description; d != "Ship a release" {
		t.Errorf("cmd Description = %q", d)
	}
	// The body should include the prompt content (without provenance prefix);
	// readContinuePermissions stripping is not relevant here.

	// MCP from settings.
	if len(proj.MCP) != 1 || proj.MCP[0].Name != "linear" {
		t.Errorf("MCP = %+v", proj.MCP)
	}

	// Hooks — wrapper filtered.
	if len(proj.Hooks) != 1 {
		t.Fatalf("Hooks = %d, want 1: %+v", len(proj.Hooks), proj.Hooks)
	}
	h := proj.Hooks[0]
	if h.Event != "BeforeTool" || h.Matcher != "Bash" || h.ScriptPath != "/usr/local/bin/preflight.sh" {
		t.Errorf("hook = %+v", h)
	}
}
