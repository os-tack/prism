package importer

import (
	"path/filepath"
	"testing"
)

// TestWindsurf_V08_HooksAndMCP covers .windsurf/hooks.json and
// .windsurf/mcp_config.json.
func TestWindsurf_V08_HooksAndMCP(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".windsurf"))

	mustWrite(t, filepath.Join(root, ".windsurf", "hooks.json"),
		`{
  "hooks": {
    "pre_run_command": [
      {"command": "bash '/usr/local/bin/preflight.sh'", "show_output": false},
      {"command": "bash '/abs/.windsurf/hooks/__scope-guard__/api-x.sh'", "show_output": false}
    ]
  }
}`)

	mustWrite(t, filepath.Join(root, ".windsurf", "mcp_config.json"),
		`{"mcpServers": {"linear": {"command": "npx", "args": ["@linear/mcp"]}}}`)

	proj, _, err := NewWindsurf().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(proj.Hooks) != 1 {
		t.Fatalf("Hooks = %d, want 1 (wrapper filtered): %+v", len(proj.Hooks), proj.Hooks)
	}
	h := proj.Hooks[0]
	if h.Event != "pre_run_command" {
		t.Errorf("hook Event = %q", h.Event)
	}
	if h.ScriptPath != "/usr/local/bin/preflight.sh" {
		t.Errorf("hook ScriptPath = %q (bash prefix and quotes should be stripped)", h.ScriptPath)
	}

	if len(proj.MCP) != 1 || proj.MCP[0].Name != "linear" {
		t.Errorf("MCP = %+v", proj.MCP)
	}
}
