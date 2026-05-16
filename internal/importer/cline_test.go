package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCline_Empty(t *testing.T) {
	root := t.TempDir()
	imp := NewCline()
	if imp.Detect(root) {
		t.Fatalf("Detect on empty root returned true")
	}
	_, _, err := imp.Import(root)
	if !errors.Is(err, ErrSourceNotPresent) {
		t.Fatalf("Import on empty root: want ErrSourceNotPresent, got %v", err)
	}
}

func TestCline_Detect(t *testing.T) {
	// Legacy file.
	root1 := t.TempDir()
	if err := os.WriteFile(filepath.Join(root1, ".clinerules"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NewCline().Detect(root1) {
		t.Fatalf("Detect on legacy file returned false")
	}

	// Modern directory.
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, ".clinerules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !NewCline().Detect(root2) {
		t.Fatalf("Detect on modern directory returned false")
	}

	// Neither.
	root3 := t.TempDir()
	if NewCline().Detect(root3) {
		t.Fatalf("Detect with no marker returned true")
	}
}

func TestCline_LegacyFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, ".clinerules")
	if err := os.WriteFile(src, []byte("legacy body"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj, warns, err := NewCline().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warns: %#v", warns)
	}
	if proj.Context == nil {
		t.Fatalf("expected Context document, got nil")
	}
	if proj.Context.SourcePath != src {
		t.Fatalf("SourcePath = %q, want %q", proj.Context.SourcePath, src)
	}
	if !strings.Contains(proj.Context.Body, "legacy body") {
		t.Fatalf("body missing legacy text: %q", proj.Context.Body)
	}
	if !strings.Contains(proj.Context.Body, "imported from cline:") {
		t.Fatalf("body missing provenance: %q", proj.Context.Body)
	}
}

func TestCline_BasicImport(t *testing.T) {
	root := t.TempDir()
	// Real project directory used by the de-slug warning check.
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, ".clinerules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"00-overview.md":          "global guidance",
		"10-scope-src-billing.md": "---\npaths:\n  - src/billing/**\n---\nbilling rules",
		"20-skill-pdf.md":         "---\ndescription: handle PDFs\n---\npdf body",
		"30-command-deploy.md":    "---\ndescription: ship it\n---\ndeploy body",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	proj, warns, err := NewCline().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	// The 10-scope-src-billing path de-slugifies to src/billing — src/ exists,
	// so no warning should fire.
	for _, w := range warns {
		if strings.Contains(w.Heuristic, "de-slugified scope") {
			t.Fatalf("unexpected de-slug warning: %#v", w)
		}
	}

	if proj.Context == nil || !strings.Contains(proj.Context.Body, "global guidance") {
		t.Fatalf("missing context.md content: %#v", proj.Context)
	}
	if len(proj.Scopes) != 1 {
		t.Fatalf("scopes: want 1, got %d (%#v)", len(proj.Scopes), proj.Scopes)
	}
	sc := proj.Scopes[0]
	if sc.Path != "src/billing" {
		t.Fatalf("scope path = %q, want src/billing", sc.Path)
	}
	if len(sc.Globs) == 0 || sc.Globs[0] != "src/billing/**" {
		t.Fatalf("scope globs = %#v, want [src/billing/**]", sc.Globs)
	}
	if !strings.Contains(sc.Document.Body, "billing rules") {
		t.Fatalf("scope body missing: %q", sc.Document.Body)
	}

	if len(proj.Skills) != 1 || proj.Skills[0].Name != "pdf" {
		t.Fatalf("skills: %#v", proj.Skills)
	}
	if proj.Skills[0].Description != "handle PDFs" {
		t.Fatalf("skill description = %q", proj.Skills[0].Description)
	}

	if len(proj.Commands) != 1 || proj.Commands[0].Name != "deploy" {
		t.Fatalf("commands: %#v", proj.Commands)
	}
	if proj.Commands[0].Description != "ship it" {
		t.Fatalf("command description = %q", proj.Commands[0].Description)
	}
}

func TestCline_HeuristicWarning(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".clinerules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 10-scope-nonexistent → de-slugifies to "nonexistent", which doesn't
	// exist in the project root → warn.
	if err := os.WriteFile(filepath.Join(dir, "10-scope-nonexistent.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unrecognized prefix → info-level warning + concatenated into context.md.
	if err := os.WriteFile(filepath.Join(dir, "99-mystery.md"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, warns, err := NewCline().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	gotDeslug := false
	gotOrphan := false
	for _, w := range warns {
		if strings.Contains(w.Heuristic, "de-slugified scope") {
			gotDeslug = true
			if w.Severity != "warn" {
				t.Fatalf("de-slug warning severity = %q, want warn", w.Severity)
			}
		}
		if strings.Contains(w.Heuristic, "no recognized prefix") {
			gotOrphan = true
			if w.Severity != "info" {
				t.Fatalf("orphan warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !gotDeslug {
		t.Fatalf("missing de-slug warning in %#v", warns)
	}
	if !gotOrphan {
		t.Fatalf("missing orphan-prefix warning in %#v", warns)
	}
}
