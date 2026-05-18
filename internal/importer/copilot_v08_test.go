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

// TestCopilot_V08_PreviewHooks verifies the v0.8.2 preview-hooks importer
// reads .github/hooks/hooks.json back into model.Hook with the canonical
// event name (preToolUse → PreToolUse), and filters out wrapper-script
// references (they're projection artifacts).
func TestCopilot_V08_PreviewHooks(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".github"))

	mustWrite(t, filepath.Join(root, ".github", "hooks", "hooks.json"),
		`{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "/usr/local/bin/preflight.sh"},
          {"type": "command", "command": "${PROJECT_DIR}/.github/hooks/__scope-guard__/api-pre-preToolUse-x.sh"}
        ]
      }
    ],
    "sessionStart": [
      {"hooks": [{"type": "command", "command": "/usr/local/bin/setup.sh"}]}
    ]
  }
}`)

	proj, _, err := NewCopilot().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(proj.Hooks) != 2 {
		t.Fatalf("Hooks = %d, want 2 (wrapper filtered): %+v", len(proj.Hooks), proj.Hooks)
	}
	want := map[string]string{
		"PreToolUse":   "/usr/local/bin/preflight.sh",
		"SessionStart": "/usr/local/bin/setup.sh",
	}
	got := map[string]string{}
	for _, h := range proj.Hooks {
		got[h.Event] = h.ScriptPath
	}
	for evt, path := range want {
		if got[evt] != path {
			t.Errorf("hook %s = %q, want %q", evt, got[evt], path)
		}
	}
}

// TestCopilot_V08_PermsGuardSidecar verifies the v0.8.2 importer reads
// the perms-guard policy sidecar at .github/hooks/__perms-guard__/policy.json
// back into model.Permissions.
func TestCopilot_V08_PermsGuardSidecar(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".github"))

	mustWrite(t, filepath.Join(root, ".github", "hooks", "__perms-guard__", "policy.json"),
		`{"allow": ["bash:ls *"], "deny": ["bash:rm -rf *"]}`)

	proj, _, err := NewCopilot().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if proj.Permissions == nil {
		t.Fatalf("Permissions = nil")
	}
	if len(proj.Permissions.Allow) != 1 || proj.Permissions.Allow[0] != "bash:ls *" {
		t.Errorf("Permissions.Allow = %v", proj.Permissions.Allow)
	}
	if len(proj.Permissions.Deny) != 1 || proj.Permissions.Deny[0] != "bash:rm -rf *" {
		t.Errorf("Permissions.Deny = %v", proj.Permissions.Deny)
	}
}
