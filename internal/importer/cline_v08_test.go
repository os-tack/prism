package importer

import (
	"path/filepath"
	"testing"
)

// TestCline_V08_Workflows_Hooks_MCP covers the v0.8 cline emissions:
// .clinerules/workflows/<n>.md, .clinerules/hooks/<event>.json, and
// .cline/cline_mcp_settings.json. The legacy 30-command-*.md path is
// still recognized too — both should populate proj.Commands without
// dropping either.
func TestCline_V08_Workflows_Hooks_MCP(t *testing.T) {
	root := t.TempDir()
	// Need .clinerules to exist as either file or dir for Detect.
	mustMkdir(t, filepath.Join(root, ".clinerules"))

	// Workflow: native slash command.
	mustWrite(t, filepath.Join(root, ".clinerules", "workflows", "deploy.md"),
		"---\n"+
			"description: Ship a release\n"+
			"---\n"+
			"Run the release script with --confirm.\n")

	// Hook: native per-event JSON, with one user hook and one wrapper.
	mustWrite(t, filepath.Join(root, ".clinerules", "hooks", "PreToolUse.json"),
		`{
  "hooks": [
    {
      "matcher": "Bash",
      "hooks": [
        {"type": "command", "command": "/usr/local/bin/preflight.sh"},
        {"type": "command", "command": "/abs/.clinerules/hooks/__scope-guard__/api-pre-PreToolUse-x.sh"}
      ]
    }
  ]
}`)

	// MCP: project-local cline_mcp_settings.json.
	mustWrite(t, filepath.Join(root, ".cline", "cline_mcp_settings.json"),
		`{"mcpServers": {"linear": {"command": "npx", "args": ["@linear/mcp"]}}}`)

	proj, _, err := NewCline().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Commands: just the workflow (no 30-command-*.md in this fixture).
	if len(proj.Commands) != 1 || proj.Commands[0].Name != "deploy" {
		t.Errorf("Commands = %+v", proj.Commands)
	}

	// Hooks: only the user-defined hook; wrapper should be filtered.
	if len(proj.Hooks) != 1 {
		t.Fatalf("Hooks = %d, want 1 (wrapper filtered): %+v", len(proj.Hooks), proj.Hooks)
	}
	h := proj.Hooks[0]
	if h.Event != "PreToolUse" || h.Matcher != "Bash" || h.ScriptPath != "/usr/local/bin/preflight.sh" {
		t.Errorf("hook = %+v", h)
	}

	// MCP
	if len(proj.MCP) != 1 || proj.MCP[0].Name != "linear" {
		t.Errorf("MCP = %+v", proj.MCP)
	}
}

// TestCline_V08_WorkflowsAndLegacyCommandsCoexist: both 30-command-*.md
// and workflows/*.md emit Commands; uniqueName avoids collision on name.
func TestCline_V08_WorkflowsAndLegacyCommandsCoexist(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".clinerules"))
	mustWrite(t, filepath.Join(root, ".clinerules", "30-command-deploy.md"),
		"Legacy deploy body.\n")
	mustWrite(t, filepath.Join(root, ".clinerules", "workflows", "deploy.md"),
		"Workflow deploy body.\n")

	proj, _, err := NewCline().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(proj.Commands) != 2 {
		t.Fatalf("Commands = %d, want 2: %+v", len(proj.Commands), proj.Commands)
	}
	names := map[string]bool{}
	for _, c := range proj.Commands {
		names[c.Name] = true
	}
	if !names["deploy"] {
		t.Errorf("expected a command named deploy, got %+v", names)
	}
}

// TestCline_V08_PermsGuardSidecar verifies the v0.8.2 importer reads the
// perms-guard policy sidecar back into model.Permissions, so a Cline
// projection round-trips its allow/deny/ask lists. Wrapper scripts are
// projection artifacts and not directly imported (the policy carries the
// canonical content).
func TestCline_V08_PermsGuardSidecar(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".clinerules"))

	mustWrite(t, filepath.Join(root, ".cline", "hooks", "__perms-guard__", "policy.json"),
		`{
  "allow": ["bash:ls *"],
  "deny": ["bash:rm -rf *"],
  "ask": ["bash:git *"]
}`)
	mustWrite(t, filepath.Join(root, ".cline", "hooks", "__perms-guard__", "src-billing.policy.json"),
		`{"deny": ["bash:psql *"]}`)

	proj, _, err := NewCline().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if proj.Permissions == nil {
		t.Fatalf("Permissions = nil; want global allow/deny/ask round-tripped")
	}
	if len(proj.Permissions.Allow) != 1 || proj.Permissions.Allow[0] != "bash:ls *" {
		t.Errorf("Permissions.Allow = %v", proj.Permissions.Allow)
	}
	if len(proj.Permissions.Deny) != 1 || proj.Permissions.Deny[0] != "bash:rm -rf *" {
		t.Errorf("Permissions.Deny = %v", proj.Permissions.Deny)
	}
	if len(proj.Permissions.Ask) != 1 || proj.Permissions.Ask[0] != "bash:git *" {
		t.Errorf("Permissions.Ask = %v", proj.Permissions.Ask)
	}
	if len(proj.ScopedPermissions) != 1 {
		t.Fatalf("ScopedPermissions = %d, want 1", len(proj.ScopedPermissions))
	}
	sp := proj.ScopedPermissions[0]
	if sp.ScopePath != "src/billing" || len(sp.Deny) != 1 || sp.Deny[0] != "bash:psql *" {
		t.Errorf("scoped perms = %+v", sp)
	}
}
