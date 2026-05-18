package engine_test

// roundtrip_v08_test.go — round-trip coverage for the v0.8 emissions.
// These tests seed v0.8-shape source fixtures (agents, native skills, hooks,
// MCP, workflows, permissions.yaml, etc.) so an importer regression that
// silently drops the new primitives is caught in CI.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/plugins"
)

// TestRoundTrip_Cursor_V08 seeds a .cursor/ tree using the v0.8 surfaces
// (native skills dir, agents, commands, hooks.json) and verifies each
// primitive survives import + compile.
func TestRoundTrip_Cursor_V08(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".cursor/skills/pdf-edit/SKILL.md": "---\n" +
			"description: PDF editing helper\n" +
			"globs: [\"**/*.pdf\"]\n" +
			"---\n" +
			"Use pdftk for PDF manipulation.\n",
		".cursor/agents/reviewer.md": "---\n" +
			"description: Code reviewer\n" +
			"---\n" +
			"You are a thorough code reviewer.\n",
		".cursor/commands/deploy.md": "Run the release script with --confirm.\n",
		".cursor/hooks.json": `{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {"matcher": "Bash", "command": "/usr/local/bin/preflight.sh"}
    ]
  }
}`,
	})

	opts := rtOptions(t, root, plugins.NewCursor())
	runRoundTrip(t, opts, "cursor")

	// Skill survives.
	skillOut := ".cursor/skills/pdf-edit/SKILL.md"
	skillBody := readBody(t, root, skillOut)
	assertBodyContains(t, skillOut, skillBody, "Use pdftk for PDF manipulation.")
	skillFM := readFrontmatter(t, root, skillOut)
	if d, _ := skillFM["description"].(string); !strings.Contains(d, "PDF editing helper") {
		t.Errorf("%s description = %q", skillOut, d)
	}

	// Agent survives.
	agentOut := ".cursor/agents/reviewer.md"
	agentBody := readBody(t, root, agentOut)
	assertBodyContains(t, agentOut, agentBody, "You are a thorough code reviewer.")

	// Command survives.
	cmdOut := ".cursor/commands/deploy.md"
	cmdBody := readBody(t, root, cmdOut)
	assertBodyContains(t, cmdOut, cmdBody, "Run the release script with --confirm.")

	// Hook re-emitted into hooks.json.
	hooksData, err := os.ReadFile(filepath.Join(root, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(hooksData, &doc); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil || hooks["preToolUse"] == nil {
		t.Errorf("hooks.json missing preToolUse entries:\n%s", string(hooksData))
	}
	entries, _ := hooks["preToolUse"].([]any)
	if len(entries) != 1 {
		t.Errorf("preToolUse entries = %d, want 1: %v", len(entries), entries)
	}
}

// TestRoundTrip_Cline_V08 seeds a .clinerules/ tree using the v0.8 workflow
// + hook + MCP surfaces. The critical assertion is that
// .clinerules/workflows/deploy.md round-trips back to itself (not to the
// old 30-command-*.md degraded form).
func TestRoundTrip_Cline_V08(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".clinerules/00-overview.md": "Project-wide Go conventions: gofmt.\n",
		".clinerules/workflows/deploy.md": "---\n" +
			"description: Ship a release\n" +
			"---\n" +
			"Run the release script with --confirm.\n",
		".clinerules/hooks/PreToolUse.json": `{
  "hooks": [
    {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/preflight.sh"}]}
  ]
}`,
		".cline/cline_mcp_settings.json": `{"mcpServers": {"linear": {"command": "npx", "args": ["@linear/mcp"]}}}`,
	})

	opts := rtOptions(t, root, plugins.NewCline())
	runRoundTrip(t, opts, "cline")

	// Workflow round-trips at the same path (NOT 30-command-deploy.md).
	cmdOut := ".clinerules/workflows/deploy.md"
	cmdBody := readBody(t, root, cmdOut)
	assertBodyContains(t, cmdOut, cmdBody, "Run the release script with --confirm.")

	// Hook re-emitted as a filename-dispatch script (no .json extension)
	// per SPEC §4.4.5. The v0.8 fixture's .json shape is imported; the
	// new emit is an executable bash script.
	hooksData, err := os.ReadFile(filepath.Join(root, ".clinerules", "hooks", "PreToolUse"))
	if err != nil {
		t.Fatalf("read hooks/PreToolUse: %v", err)
	}
	if !strings.Contains(string(hooksData), "/usr/local/bin/preflight.sh") {
		t.Errorf("hooks/PreToolUse missing preflight.sh: %s", string(hooksData))
	}

	// MCP merged into project-local file.
	mcpData, err := os.ReadFile(filepath.Join(root, ".cline", "cline_mcp_settings.json"))
	if err != nil {
		t.Fatalf("read cline_mcp_settings.json: %v", err)
	}
	if !strings.Contains(string(mcpData), "linear") {
		t.Errorf("cline_mcp_settings.json missing linear: %s", string(mcpData))
	}
}

// TestRoundTrip_Windsurf_V08 seeds .windsurf/hooks.json and
// .windsurf/mcp_config.json.
func TestRoundTrip_Windsurf_V08(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".windsurf/rules/always.md": "---\ntrigger: always_on\n---\nUse gofmt.\n",
		".windsurf/hooks.json": `{
  "hooks": {
    "pre_run_command": [
      {"command": "bash '/usr/local/bin/preflight.sh'", "show_output": false}
    ]
  }
}`,
		".windsurf/mcp_config.json": `{"mcpServers": {"linear": {"command": "npx", "args": ["@linear/mcp"]}}}`,
	})

	opts := rtOptions(t, root, plugins.NewWindsurf())
	runRoundTrip(t, opts, "windsurf")

	hooksData, err := os.ReadFile(filepath.Join(root, ".windsurf", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	if !strings.Contains(string(hooksData), "/usr/local/bin/preflight.sh") {
		t.Errorf("hooks.json missing preflight.sh after round-trip:\n%s", string(hooksData))
	}

	mcpData, err := os.ReadFile(filepath.Join(root, ".windsurf", "mcp_config.json"))
	if err != nil {
		t.Fatalf("read mcp_config.json: %v", err)
	}
	if !strings.Contains(string(mcpData), "linear") {
		t.Errorf("mcp_config.json missing linear:\n%s", string(mcpData))
	}
}

// TestRoundTrip_Gemini_V08 seeds .gemini/agents/, .gemini/commands/, and
// hooks in settings.json.
func TestRoundTrip_Gemini_V08(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"GEMINI.md": "# Root\n\nUse gofmt.\n",
		".gemini/agents/reviewer.md": "---\n" +
			"name: reviewer\n" +
			"description: Code reviewer\n" +
			"---\n" +
			"Review thoroughly.\n",
		".gemini/commands/deploy.toml": `description = "Ship a release"
prompt = """
Run the release script with --confirm.
"""
`,
		".gemini/settings.json": `{
  "mcpServers": {"linear": {"command": "npx", "args": ["@linear/mcp"]}},
  "hooks": {
    "BeforeTool": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/preflight.sh"}]}
    ]
  }
}`,
	})

	opts := rtOptions(t, root, plugins.NewGemini())
	runRoundTrip(t, opts, "gemini")

	// Agent round-trips.
	agOut := ".gemini/agents/reviewer.md"
	agBody := readBody(t, root, agOut)
	assertBodyContains(t, agOut, agBody, "Review thoroughly.")

	// Command round-trips as TOML.
	cmdData, err := os.ReadFile(filepath.Join(root, ".gemini", "commands", "deploy.toml"))
	if err != nil {
		t.Fatalf("read deploy.toml: %v", err)
	}
	if !strings.Contains(string(cmdData), "Run the release script with --confirm.") {
		t.Errorf("deploy.toml missing body:\n%s", string(cmdData))
	}

	// settings.json: hooks block + mcpServers.
	settingsData, err := os.ReadFile(filepath.Join(root, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(settingsData), "/usr/local/bin/preflight.sh") {
		t.Errorf("settings.json missing preflight.sh:\n%s", string(settingsData))
	}
	if !strings.Contains(string(settingsData), "linear") {
		t.Errorf("settings.json missing linear:\n%s", string(settingsData))
	}
}

// TestRoundTrip_Copilot_V08 seeds .github/agents/ and a project-local
// .github/mcp.json.
func TestRoundTrip_Copilot_V08(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".github/copilot-instructions.md": "# Conventions\n\nUse Go 1.25.\n",
		".github/agents/reviewer.agent.md": "---\n" +
			"name: reviewer\n" +
			"description: Code reviewer\n" +
			"---\n" +
			"Review thoroughly.\n",
		".github/mcp.json": `{"mcpServers": {"linear": {"command": "npx", "args": ["@linear/mcp"]}}}`,
	})

	opts := rtOptions(t, root, plugins.NewCopilot())
	runRoundTrip(t, opts, "copilot")

	// Agent round-trips.
	agOut := ".github/agents/reviewer.agent.md"
	agBody := readBody(t, root, agOut)
	assertBodyContains(t, agOut, agBody, "Review thoroughly.")

	// MCP json survives.
	mcpData, err := os.ReadFile(filepath.Join(root, ".github", "mcp.json"))
	if err != nil {
		t.Fatalf("read .github/mcp.json: %v", err)
	}
	if !strings.Contains(string(mcpData), "linear") {
		t.Errorf(".github/mcp.json missing linear:\n%s", string(mcpData))
	}
}

// TestRoundTrip_Continue_V08 seeds .continue/prompts/ and
// .continue/permissions.yaml.
func TestRoundTrip_Continue_V08(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".continue/rules/_root.md":    "---\nalwaysApply: true\n---\nUse gofmt.\n",
		".continue/prompts/deploy.md": "---\nname: deploy\ndescription: Ship a release\ninvokable: true\n---\nRun the release script.\n",
		".continue/permissions.yaml":  "allow:\n  - Bash(go test *)\n  - Read\nask:\n  - Bash(git push *)\nexclude:\n  - Bash(rm -rf *)\n",
	})

	opts := rtOptions(t, root, plugins.NewContinue())
	runRoundTrip(t, opts, "continue")

	// Prompt round-trips.
	cmdOut := ".continue/prompts/deploy.md"
	cmdBody := readBody(t, root, cmdOut)
	assertBodyContains(t, cmdOut, cmdBody, "Run the release script.")

	// Permissions round-trip.
	permsData, err := os.ReadFile(filepath.Join(root, ".continue", "permissions.yaml"))
	if err != nil {
		t.Fatalf("read permissions.yaml: %v", err)
	}
	out := string(permsData)
	for _, needle := range []string{"Bash(go test *)", "Read", "Bash(git push *)", "Bash(rm -rf *)"} {
		if !strings.Contains(out, needle) {
			t.Errorf("permissions.yaml missing %q:\n%s", needle, out)
		}
	}
}

// TestRoundTrip_Cline_V082_Perms seeds a global permissions sidecar at
// .cline/hooks/__perms-guard__/policy.json (the v0.8.2 emission shape)
// and verifies the importer reads it back + the plugin re-emits the
// wrapper artifacts on the next compile. Locks the perms-via-hook
// round-trip closed.
func TestRoundTrip_Cline_V082_Perms(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".clinerules/00-overview.md": "Project-wide Go conventions: gofmt.\n",
		".cline/hooks/__perms-guard__/policy.json": `{
  "allow": ["bash:ls *"],
  "deny": ["bash:rm -rf *"]
}`,
	})

	opts := rtOptions(t, root, plugins.NewCline())
	runRoundTrip(t, opts, "cline")

	// Policy survives the round-trip.
	policyData, err := os.ReadFile(filepath.Join(root, ".cline", "hooks", "__perms-guard__", "policy.json"))
	if err != nil {
		t.Fatalf("read policy.json: %v", err)
	}
	if !strings.Contains(string(policyData), `"bash:ls *"`) {
		t.Errorf("policy.json missing allow rule:\n%s", policyData)
	}
	if !strings.Contains(string(policyData), `"bash:rm -rf *"`) {
		t.Errorf("policy.json missing deny rule:\n%s", policyData)
	}

	// PreToolUse (filename-dispatch script, no .json) wires the gate
	// wrapper.
	preData, err := os.ReadFile(filepath.Join(root, ".clinerules", "hooks", "PreToolUse"))
	if err != nil {
		t.Fatalf("read PreToolUse: %v", err)
	}
	if !strings.Contains(string(preData), "__perms-guard__") {
		t.Errorf("PreToolUse missing perms-guard reference:\n%s", preData)
	}
}

// TestRoundTrip_Copilot_V082_PreviewHooks seeds a .github/hooks/hooks.json
// and a perms-guard policy sidecar, runs init + compile via the copilot
// plugin (with EnablePreviewHooks on), and verifies both surfaces survive.
func TestRoundTrip_Copilot_V082_PreviewHooks(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".github/copilot-instructions.md": "# Conventions\n\nUse Go 1.25.\n",
		".github/hooks/hooks.json": `{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/preflight.sh"}]}
    ]
  }
}`,
		".github/hooks/__perms-guard__/policy.json": `{"allow": ["bash:ls *"], "deny": ["bash:rm -rf *"]}`,
	})

	opts := rtOptions(t, root, &plugins.CopilotPlugin{EnablePreviewHooks: true})
	runRoundTrip(t, opts, "copilot")

	hooksData, err := os.ReadFile(filepath.Join(root, ".github", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	if !strings.Contains(string(hooksData), "preflight.sh") {
		t.Errorf("hooks.json missing user hook:\n%s", hooksData)
	}
	if !strings.Contains(string(hooksData), "__perms-guard__") {
		t.Errorf("hooks.json missing perms-guard wiring:\n%s", hooksData)
	}

	policyData, err := os.ReadFile(filepath.Join(root, ".github", "hooks", "__perms-guard__", "policy.json"))
	if err != nil {
		t.Fatalf("read policy.json: %v", err)
	}
	if !strings.Contains(string(policyData), `"bash:rm -rf *"`) {
		t.Errorf("policy.json missing deny rule:\n%s", policyData)
	}
}
