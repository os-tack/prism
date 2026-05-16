package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContinue_Empty(t *testing.T) {
	root := t.TempDir()
	imp := NewContinue()
	if imp.Detect(root) {
		t.Fatalf("Detect on empty root returned true")
	}
	_, _, err := imp.Import(root)
	if !errors.Is(err, ErrSourceNotPresent) {
		t.Fatalf("want ErrSourceNotPresent, got %v", err)
	}
}

func TestContinue_Detect(t *testing.T) {
	root := t.TempDir()
	if NewContinue().Detect(root) {
		t.Fatalf("Detect on empty: want false")
	}
	if err := os.MkdirAll(filepath.Join(root, ".continue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !NewContinue().Detect(root) {
		t.Fatalf("Detect after mkdir: want true")
	}
}

func TestContinue_BasicImport(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".continue", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpDir := filepath.Join(root, ".continue", "mcpServers")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// alwaysApply, no globs → context.md.
	if err := os.WriteFile(filepath.Join(rulesDir, "always.md"),
		[]byte("---\nalwaysApply: true\n---\nbe excellent"), 0o644); err != nil {
		t.Fatal(err)
	}
	// extension-only globs → skill.
	if err := os.WriteFile(filepath.Join(rulesDir, "py.md"),
		[]byte("---\nname: py-style\ndescription: python style\nglobs:\n  - '**/*.py'\n---\npy body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// path globs → scope.
	if err := os.WriteFile(filepath.Join(rulesDir, "billing.md"),
		[]byte("---\nname: billing\ndescription: billing rules\nglobs:\n  - src/billing/**\n---\nbilling body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// MCP server with explicit name.
	if err := os.WriteFile(filepath.Join(mcpDir, "search.yaml"),
		[]byte("name: search\ncommand: /usr/bin/search\nargs:\n  - --port=8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// MCP server without name (filename-derived).
	if err := os.WriteFile(filepath.Join(mcpDir, "github.yaml"),
		[]byte("command: gh-mcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, warns, err := NewContinue().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warns: %#v", warns)
	}
	if proj.Context == nil || !strings.Contains(proj.Context.Body, "be excellent") {
		t.Fatalf("context.md missing alwaysApply body: %#v", proj.Context)
	}
	if len(proj.Skills) != 1 || proj.Skills[0].Name != "py-style" {
		t.Fatalf("skills: %#v", proj.Skills)
	}
	if len(proj.Scopes) != 1 || proj.Scopes[0].Path != "src/billing" {
		t.Fatalf("scopes: %#v", proj.Scopes)
	}
	if len(proj.MCP) != 2 {
		t.Fatalf("MCP: want 2, got %d (%#v)", len(proj.MCP), proj.MCP)
	}
	// Sorted by name → github, search.
	if proj.MCP[0].Name != "github" || proj.MCP[1].Name != "search" {
		t.Fatalf("MCP order: %#v", proj.MCP)
	}
	if proj.MCP[1].Command != "/usr/bin/search" {
		t.Fatalf("MCP[search].Command = %q", proj.MCP[1].Command)
	}
}

func TestContinue_HeuristicWarning(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".continue", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Regex rule → warn and drop.
	if err := os.WriteFile(filepath.Join(rulesDir, "regex.md"),
		[]byte("---\nname: nope\nregex: \"foo.*\"\n---\nshould drop"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Mixed globs → warn + scope.
	if err := os.WriteFile(filepath.Join(rulesDir, "mixed.md"),
		[]byte("---\nname: mixed\ndescription: mixed globs\nglobs:\n  - src/**\n  - '**/*.py'\n---\nmixed body"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, warns, err := NewContinue().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	gotRegex := false
	gotMixed := false
	for _, w := range warns {
		if strings.Contains(w.Heuristic, "regex triggers are unsupported") {
			gotRegex = true
		}
		if strings.Contains(w.Heuristic, "mix path-prefix and extension-only") {
			gotMixed = true
		}
	}
	if !gotRegex {
		t.Fatalf("missing regex warning: %#v", warns)
	}
	if !gotMixed {
		t.Fatalf("missing mixed-globs warning: %#v", warns)
	}
	// Regex rule must have been dropped entirely.
	for _, s := range proj.Skills {
		if s.Name == "nope" {
			t.Fatalf("regex rule was not dropped: %#v", s)
		}
	}
	for _, sc := range proj.Scopes {
		if strings.Contains(sc.Document.Body, "should drop") {
			t.Fatalf("regex rule body leaked into scope")
		}
	}
}
