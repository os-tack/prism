package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopilot_Empty(t *testing.T) {
	root := t.TempDir()
	imp := NewCopilot()
	if imp.Detect(root) {
		t.Fatalf("Detect on empty root returned true")
	}
	_, _, err := imp.Import(root)
	if !errors.Is(err, ErrSourceNotPresent) {
		t.Fatalf("want ErrSourceNotPresent, got %v", err)
	}
}

func TestCopilot_Detect(t *testing.T) {
	root := t.TempDir()
	if NewCopilot().Detect(root) {
		t.Fatalf("Detect on empty: want false")
	}
	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "copilot-instructions.md"),
		[]byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NewCopilot().Detect(root) {
		t.Fatalf("Detect after marker: want true")
	}

	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, ".github", "instructions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !NewCopilot().Detect(root2) {
		t.Fatalf("Detect instructions/ dir: want true")
	}
}

func TestCopilot_BasicImport(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github", "instructions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".github", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, ".github", "copilot-instructions.md"),
		[]byte("root copilot guidance"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Path applyTo → scope.
	if err := os.WriteFile(filepath.Join(root, ".github", "instructions", "billing.instructions.md"),
		[]byte("---\napplyTo: src/billing/**\n---\nbilling body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Extension-only applyTo → skill.
	if err := os.WriteFile(filepath.Join(root, ".github", "instructions", "py-style.instructions.md"),
		[]byte("---\napplyTo: '**/*.py'\n---\npy body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Prompt → command.
	if err := os.WriteFile(filepath.Join(root, ".github", "prompts", "ship.prompt.md"),
		[]byte("---\ndescription: ship it\n---\ndeploy body"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, warns, err := NewCopilot().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warns: %#v", warns)
	}
	if proj.Context == nil || !strings.Contains(proj.Context.Body, "root copilot guidance") {
		t.Fatalf("context missing: %#v", proj.Context)
	}
	if len(proj.Scopes) != 1 || proj.Scopes[0].Path != "src/billing" {
		t.Fatalf("scopes: %#v", proj.Scopes)
	}
	if len(proj.Skills) != 1 || proj.Skills[0].Name != "py-style" {
		t.Fatalf("skills: %#v", proj.Skills)
	}
	if len(proj.Commands) != 1 || proj.Commands[0].Name != "ship" {
		t.Fatalf("commands: %#v", proj.Commands)
	}
	if proj.Commands[0].Description != "ship it" {
		t.Fatalf("command description = %q", proj.Commands[0].Description)
	}
}

func TestCopilot_HeuristicWarning(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github", "instructions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".github", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Missing applyTo → warn-as-skill.
	if err := os.WriteFile(filepath.Join(root, ".github", "instructions", "noapply.instructions.md"),
		[]byte("---\ndescription: orphan\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Mixed applyTo → warn.
	if err := os.WriteFile(filepath.Join(root, ".github", "instructions", "mixed.instructions.md"),
		[]byte("---\napplyTo:\n  - src/**\n  - '**/*.py'\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Prompt with agent/model/tools → info warnings on each.
	if err := os.WriteFile(filepath.Join(root, ".github", "prompts", "deploy.prompt.md"),
		[]byte("---\ndescription: deploy\nagent: copilot-agent\nmodel: gpt-4o\ntools:\n  - search\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, warns, err := NewCopilot().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	wantSubstrings := []string{
		"missing applyTo frontmatter",
		"applyTo mixes path-prefix and extension-only",
		"\"agent\"",
		"\"model\"",
		"\"tools\"",
	}
	for _, want := range wantSubstrings {
		found := false
		for _, w := range warns {
			if strings.Contains(w.Heuristic, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing warning containing %q in %#v", want, warns)
		}
	}
}
