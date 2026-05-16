package importer

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaude_Empty: empty root → ErrSourceNotPresent.
func TestClaude_Empty(t *testing.T) {
	root := t.TempDir()
	_, _, err := NewClaude().Import(root)
	if !errors.Is(err, ErrSourceNotPresent) {
		t.Fatalf("Import on empty root: err = %v, want ErrSourceNotPresent", err)
	}
}

// TestClaude_Detect: marker present → true; absent → false.
func TestClaude_Detect(t *testing.T) {
	imp := NewClaude()

	if imp.Detect(t.TempDir()) {
		t.Errorf("Detect on empty root returned true")
	}

	withDir := t.TempDir()
	mustMkdir(t, filepath.Join(withDir, ".claude"))
	if !imp.Detect(withDir) {
		t.Errorf("Detect with .claude/ returned false")
	}

	withMD := t.TempDir()
	mustWrite(t, filepath.Join(withMD, "CLAUDE.md"), "body")
	if !imp.Detect(withMD) {
		t.Errorf("Detect with CLAUDE.md returned false")
	}
}

// TestClaude_BasicImport: root + nested CLAUDE.md (the original
// v0.4 walker behavior) maps to context + scopes.
func TestClaude_BasicImport(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "# Root context\n")
	mustWrite(t, filepath.Join(root, "src", "billing", "CLAUDE.md"), "Billing scope\n")

	proj, warnings, err := NewClaude().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	if proj.Context == nil {
		t.Fatalf("proj.Context is nil")
	}
	if !strings.Contains(proj.Context.Body, "# Root context") {
		t.Errorf("context body missing expected text:\n%s", proj.Context.Body)
	}
	if len(proj.Scopes) != 1 || proj.Scopes[0].Path != "src/billing" {
		t.Errorf("scopes unexpected: %+v", proj.Scopes)
	}
}

// TestClaude_HeuristicWarning: a skill directory missing SKILL.md
// produces a warn-severity Warning.
func TestClaude_HeuristicWarning(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "root\n")
	mustMkdir(t, filepath.Join(root, ".claude", "skills", "incomplete"))

	_, warnings, err := NewClaude().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	var found bool
	for _, w := range warnings {
		if w.Severity == "warn" && strings.Contains(w.Heuristic, "no SKILL.md") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a warn-severity Warning about missing SKILL.md; got %+v", warnings)
	}
}

// TestClaude_FullRoundTrip: every canonical slot populated.
//
// Fixture:
//
//	CLAUDE.md
//	src/api/CLAUDE.md                              → scope
//	.claude/skills/lint/SKILL.md                   → skill (with script)
//	.claude/skills/lint/scripts/run.sh             → skill script
//	.claude/commands/review.md                     → command
//	.claude/agents/reviewer.md                     → agent
//	.claude/settings.json (permissions + hooks)    → permissions + hooks
//	.mcp.json                                      → MCP server
func TestClaude_FullRoundTrip(t *testing.T) {
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "Root context.\n")
	mustWrite(t, filepath.Join(root, "src", "api", "CLAUDE.md"), "API scope.\n")

	mustWrite(t, filepath.Join(root, ".claude", "skills", "lint", "SKILL.md"), `---
description: Run the linter
trigger: when the user mentions lint
globs: ["**/*.go"]
---
Skill body.
`)
	mustWrite(t, filepath.Join(root, ".claude", "skills", "lint", "scripts", "run.sh"), "#!/bin/sh\necho lint\n")

	mustWrite(t, filepath.Join(root, ".claude", "commands", "review.md"), `---
description: PR review prompt
---
Review body.
`)

	mustWrite(t, filepath.Join(root, ".claude", "agents", "reviewer.md"), `---
description: Code reviewer subagent
---
Reviewer system prompt.
`)

	mustWrite(t, filepath.Join(root, ".claude", "settings.json"), `{
  "permissions": {
    "allow": ["Bash(npm test)"],
    "deny": ["Bash(rm -rf /)"]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "/usr/local/bin/audit-bash"}
        ]
      }
    ]
  }
}`)

	mustWrite(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    }
  }
}`)

	proj, warnings, err := NewClaude().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, w := range warnings {
		if w.Severity == "warn" {
			t.Errorf("unexpected warn-severity warning: %+v", w)
		}
	}

	// Context.
	if proj.Context == nil || !strings.Contains(proj.Context.Body, "Root context.") {
		t.Errorf("proj.Context bad: %+v", proj.Context)
	}

	// Scope.
	if len(proj.Scopes) != 1 || proj.Scopes[0].Path != "src/api" {
		t.Errorf("scopes unexpected: %+v", proj.Scopes)
	}

	// Skill.
	if len(proj.Skills) != 1 {
		t.Fatalf("len(proj.Skills) = %d, want 1", len(proj.Skills))
	}
	sk := proj.Skills[0]
	if sk.Name != "lint" {
		t.Errorf("skill name = %q, want lint", sk.Name)
	}
	if sk.Description != "Run the linter" {
		t.Errorf("skill description = %q", sk.Description)
	}
	if sk.Trigger != "when the user mentions lint" {
		t.Errorf("skill trigger = %q", sk.Trigger)
	}
	if len(sk.Globs) != 1 || sk.Globs[0] != "**/*.go" {
		t.Errorf("skill globs = %v", sk.Globs)
	}
	if len(sk.Scripts) != 1 {
		t.Fatalf("len(skill.Scripts) = %d, want 1", len(sk.Scripts))
	}
	if !strings.HasSuffix(sk.Scripts[0], "run.sh") {
		t.Errorf("skill script path = %q, want suffix run.sh", sk.Scripts[0])
	}

	// Command.
	if len(proj.Commands) != 1 || proj.Commands[0].Name != "review" {
		t.Fatalf("commands unexpected: %+v", proj.Commands)
	}
	if proj.Commands[0].Description != "PR review prompt" {
		t.Errorf("command description = %q", proj.Commands[0].Description)
	}

	// Agent.
	if len(proj.Agents) != 1 || proj.Agents[0].Name != "reviewer" {
		t.Fatalf("agents unexpected: %+v", proj.Agents)
	}
	if proj.Agents[0].Description != "Code reviewer subagent" {
		t.Errorf("agent description = %q", proj.Agents[0].Description)
	}

	// Permissions.
	if proj.Permissions == nil {
		t.Fatalf("permissions nil")
	}
	if len(proj.Permissions.Allow) != 1 || proj.Permissions.Allow[0] != "Bash(npm test)" {
		t.Errorf("permissions.Allow = %v", proj.Permissions.Allow)
	}
	if len(proj.Permissions.Deny) != 1 || proj.Permissions.Deny[0] != "Bash(rm -rf /)" {
		t.Errorf("permissions.Deny = %v", proj.Permissions.Deny)
	}

	// Hooks.
	if len(proj.Hooks) != 1 {
		t.Fatalf("len(proj.Hooks) = %d, want 1", len(proj.Hooks))
	}
	h := proj.Hooks[0]
	if h.Event != "PreToolUse" || h.Matcher != "Bash" || h.ScriptPath != "/usr/local/bin/audit-bash" {
		t.Errorf("hook unexpected: %+v", h)
	}

	// MCP.
	if len(proj.MCP) != 1 {
		t.Fatalf("len(proj.MCP) = %d, want 1", len(proj.MCP))
	}
	if proj.MCP[0].Name != "filesystem" || proj.MCP[0].Command != "npx" {
		t.Errorf("MCP server unexpected: %+v", proj.MCP[0])
	}

	// Root/AgentsDir not set.
	if proj.Root != "" || proj.AgentsDir != "" {
		t.Errorf("Root/AgentsDir should not be set by importer; got %q / %q", proj.Root, proj.AgentsDir)
	}
}
