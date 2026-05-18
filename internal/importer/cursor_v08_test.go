package importer

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCursor_V08_SkillsAgentsCommandsHooks covers the v0.8 emissions:
// .cursor/skills/<dir>/SKILL.md, .cursor/agents/, .cursor/commands/, and
// .cursor/hooks.json.
func TestCursor_V08_SkillsAgentsCommandsHooks(t *testing.T) {
	root := t.TempDir()

	// .cursor/skills/pdf-edit/SKILL.md (Cursor 2.4+ native)
	mustWrite(t, filepath.Join(root, ".cursor", "skills", "pdf-edit", "SKILL.md"),
		"---\n"+
			"description: PDF editing helper\n"+
			"globs: [\"**/*.pdf\"]\n"+
			"---\n"+
			"Use pdftk for PDF manipulation.\n")

	// .cursor/agents/reviewer.md
	mustWrite(t, filepath.Join(root, ".cursor", "agents", "reviewer.md"),
		"---\n"+
			"name: reviewer\n"+
			"description: Code reviewer\n"+
			"---\n"+
			"You are a thorough code reviewer.\n")

	// .cursor/commands/deploy.md
	mustWrite(t, filepath.Join(root, ".cursor", "commands", "deploy.md"),
		"Run the release script with --confirm.\n")

	// .cursor/hooks.json with one user-defined hook and one scope-guard wrapper
	mustWrite(t, filepath.Join(root, ".cursor", "hooks.json"),
		`{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {"matcher": "Bash", "command": "/usr/local/bin/preflight.sh"},
      {"matcher": "Bash", "command": "/abs/.cursor/hooks/__scope-guard__/api-preflight.sh"}
    ]
  }
}`)

	proj, _, err := NewCursor().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Skill
	if len(proj.Skills) != 1 {
		t.Fatalf("Skills = %d, want 1: %+v", len(proj.Skills), proj.Skills)
	}
	sk := proj.Skills[0]
	if sk.Name != "pdf-edit" {
		t.Errorf("skill Name = %q, want pdf-edit", sk.Name)
	}
	if sk.Description != "PDF editing helper" {
		t.Errorf("skill Description = %q", sk.Description)
	}
	if len(sk.Globs) != 1 || sk.Globs[0] != "**/*.pdf" {
		t.Errorf("skill Globs = %v", sk.Globs)
	}

	// Agent
	if len(proj.Agents) != 1 {
		t.Fatalf("Agents = %d, want 1", len(proj.Agents))
	}
	ag := proj.Agents[0]
	if ag.Name != "reviewer" {
		t.Errorf("agent Name = %q, want reviewer", ag.Name)
	}
	if ag.Description != "Code reviewer" {
		t.Errorf("agent Description = %q", ag.Description)
	}

	// Command
	if len(proj.Commands) != 1 {
		t.Fatalf("Commands = %d, want 1", len(proj.Commands))
	}
	if proj.Commands[0].Name != "deploy" {
		t.Errorf("cmd Name = %q, want deploy", proj.Commands[0].Name)
	}

	// Hook — scope-guard wrapper must be skipped, only one user-defined hook
	if len(proj.Hooks) != 1 {
		t.Fatalf("Hooks = %d, want 1 (wrapper should be skipped): %+v", len(proj.Hooks), proj.Hooks)
	}
	h := proj.Hooks[0]
	if h.Event != "preToolUse" || h.Matcher != "Bash" || h.ScriptPath != "/usr/local/bin/preflight.sh" {
		t.Errorf("hook = %+v", h)
	}
}

// TestCursor_V08_SkillsDirSkippedWhenMissingSkillMD: a stray subdir with
// no SKILL.md must not produce a Skill entry.
func TestCursor_V08_SkillsDirSkippedWhenMissingSkillMD(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cursor", "skills", "empty-dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Also need a .cursor signal that activates Detect
	mustMkdir(t, filepath.Join(root, ".cursor", "rules"))
	proj, _, err := NewCursor().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(proj.Skills) != 0 {
		t.Errorf("Skills = %d, want 0 when SKILL.md is absent", len(proj.Skills))
	}
}
