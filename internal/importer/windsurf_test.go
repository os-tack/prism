package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindsurf_Empty(t *testing.T) {
	root := t.TempDir()
	imp := NewWindsurf()
	if imp.Detect(root) {
		t.Fatalf("Detect on empty root returned true")
	}
	_, _, err := imp.Import(root)
	if !errors.Is(err, ErrSourceNotPresent) {
		t.Fatalf("want ErrSourceNotPresent, got %v", err)
	}
}

func TestWindsurf_Detect(t *testing.T) {
	root := t.TempDir()
	if NewWindsurf().Detect(root) {
		t.Fatalf("Detect on empty: want false")
	}
	if err := os.MkdirAll(filepath.Join(root, ".windsurf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !NewWindsurf().Detect(root) {
		t.Fatalf("Detect after mkdir: want true")
	}
}

func TestWindsurf_BasicImport(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".windsurf", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"always.md":    "---\ntrigger: always_on\n---\nalways body",
		"glob_path.md": "---\ntrigger: glob\nglobs:\n  - src/billing/**\n---\nbilling body",
		"glob_ext.md":  "---\ntrigger: glob\nglobs:\n  - '**/*.py'\n---\npy body",
		"decision.md":  "---\ntrigger: model_decision\ndescription: PDF parser helper\n---\npdf body",
		"manual.md":    "---\ntrigger: manual\ndescription: deploy\n---\ndeploy body",
	}
	for n, c := range cases {
		if err := os.WriteFile(filepath.Join(rulesDir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	proj, warns, err := NewWindsurf().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warns: %#v", warns)
	}
	if proj.Context == nil || !strings.Contains(proj.Context.Body, "always body") {
		t.Fatalf("context missing always body: %#v", proj.Context)
	}
	// One path scope (src/billing).
	if len(proj.Scopes) != 1 || proj.Scopes[0].Path != "src/billing" {
		t.Fatalf("scopes: %#v", proj.Scopes)
	}
	// Two skills: glob_ext (extension globs) + decision (model_decision).
	wantSkillNames := map[string]bool{"glob_ext": true, "pdf-parser-helper": true}
	gotSkillNames := map[string]bool{}
	for _, s := range proj.Skills {
		gotSkillNames[s.Name] = true
	}
	for name := range wantSkillNames {
		if !gotSkillNames[name] {
			t.Fatalf("missing skill %q in %#v", name, proj.Skills)
		}
	}
	if len(proj.Commands) != 1 || proj.Commands[0].Name != "manual" {
		t.Fatalf("commands: %#v", proj.Commands)
	}
}

func TestWindsurf_HeuristicWarning(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".windsurf", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mixed globs trigger a warning.
	if err := os.WriteFile(filepath.Join(rulesDir, "mixed.md"),
		[]byte("---\ntrigger: glob\nglobs:\n  - src/**\n  - '**/*.py'\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Glob trigger with no globs frontmatter → warn.
	if err := os.WriteFile(filepath.Join(rulesDir, "lonely.md"),
		[]byte("---\ntrigger: glob\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unknown trigger → warn.
	if err := os.WriteFile(filepath.Join(rulesDir, "weird.md"),
		[]byte("---\ntrigger: wat\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, warns, err := NewWindsurf().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	gotMixed, gotLonely, gotWeird := false, false, false
	for _, w := range warns {
		switch {
		case strings.Contains(w.Heuristic, "mix path-prefix and extension-only"):
			gotMixed = true
		case strings.Contains(w.Heuristic, "trigger=glob but no globs frontmatter"):
			gotLonely = true
		case strings.Contains(w.Heuristic, "unknown trigger"):
			gotWeird = true
		}
	}
	if !gotMixed || !gotLonely || !gotWeird {
		t.Fatalf("missing one of expected warnings: mixed=%v lonely=%v weird=%v in %#v", gotMixed, gotLonely, gotWeird, warns)
	}
}
