package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCursor_Empty: an empty root → ErrSourceNotPresent.
func TestCursor_Empty(t *testing.T) {
	root := t.TempDir()
	_, _, err := NewCursor().Import(root)
	if !errors.Is(err, ErrSourceNotPresent) {
		t.Fatalf("Import on empty root: err = %v, want ErrSourceNotPresent", err)
	}
}

// TestCursor_Detect: marker present → true; absent → false.
func TestCursor_Detect(t *testing.T) {
	imp := NewCursor()

	empty := t.TempDir()
	if imp.Detect(empty) {
		t.Errorf("Detect on empty root returned true, want false")
	}

	withDir := t.TempDir()
	mustMkdir(t, filepath.Join(withDir, ".cursor"))
	if !imp.Detect(withDir) {
		t.Errorf("Detect with .cursor/ returned false, want true")
	}

	withLegacy := t.TempDir()
	mustWrite(t, filepath.Join(withLegacy, ".cursorrules"), "legacy rules")
	if !imp.Detect(withLegacy) {
		t.Errorf("Detect with .cursorrules returned false, want true")
	}
}

// TestCursor_BasicImport: cover every mapping branch in one fixture.
//
//	alwaysApply: true                 → root context
//	globs: ["src/billing/**"]         → scope at src/billing/
//	globs: ["**/*.pdf"], description  → skill
//	no globs, description present     → skill
//	.cursor/mcp.json                  → MCP servers
func TestCursor_BasicImport(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".cursor", "rules")
	mustMkdir(t, rulesDir)

	mustWrite(t, filepath.Join(rulesDir, "always.mdc"), `---
description: project-wide
alwaysApply: true
---
Always-on context body.
`)
	mustWrite(t, filepath.Join(rulesDir, "billing.mdc"), `---
description: Stripe webhook conventions
globs: ["src/billing/**"]
---
Always validate signatures before processing.
`)
	mustWrite(t, filepath.Join(rulesDir, "pdf.mdc"), `---
description: PDF handling rules
globs: ["**/*.pdf"]
---
Use the pdf-extract helper for PDFs.
`)
	mustWrite(t, filepath.Join(rulesDir, "review.mdc"), `---
description: Run before opening a PR
---
Review checklist body.
`)

	mustWrite(t, filepath.Join(root, ".cursor", "mcp.json"), `{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    }
  }
}`)

	proj, warnings, err := NewCursor().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, w := range warnings {
		if w.Severity == "warn" {
			t.Errorf("unexpected warn-severity warning: %+v", w)
		}
	}

	// Root context populated from alwaysApply rule.
	if proj.Context == nil {
		t.Fatalf("proj.Context is nil; want populated from alwaysApply rule")
	}
	if !strings.Contains(proj.Context.Body, "Always-on context body.") {
		t.Errorf("proj.Context.Body missing always.mdc body:\n%s", proj.Context.Body)
	}
	// SourcePath points back at the .mdc.
	if !strings.HasSuffix(proj.Context.SourcePath, "always.mdc") {
		t.Errorf("proj.Context.SourcePath = %q, want suffix always.mdc", proj.Context.SourcePath)
	}

	// One scope from billing.mdc.
	if len(proj.Scopes) != 1 {
		t.Fatalf("len(proj.Scopes) = %d, want 1", len(proj.Scopes))
	}
	sc := proj.Scopes[0]
	if sc.Path != "src/billing" {
		t.Errorf("scope.Path = %q, want src/billing", sc.Path)
	}
	if len(sc.Globs) != 1 || sc.Globs[0] != "src/billing/**" {
		t.Errorf("scope.Globs = %v, want [src/billing/**]", sc.Globs)
	}
	if sc.Description != "Stripe webhook conventions" {
		t.Errorf("scope.Description = %q", sc.Description)
	}
	if !strings.Contains(sc.Document.Body, "Always validate signatures") {
		t.Errorf("scope.Document.Body missing body:\n%s", sc.Document.Body)
	}

	// Two skills (pdf, review).
	if len(proj.Skills) != 2 {
		t.Fatalf("len(proj.Skills) = %d, want 2", len(proj.Skills))
	}
	bySkill := map[string]bool{}
	for _, sk := range proj.Skills {
		bySkill[sk.Name] = true
	}
	if !bySkill["pdf"] || !bySkill["review"] {
		t.Errorf("expected skills [pdf, review], got %v", bySkill)
	}

	// MCP.
	if len(proj.MCP) != 1 {
		t.Fatalf("len(proj.MCP) = %d, want 1", len(proj.MCP))
	}
	srv := proj.MCP[0]
	if srv.Name != "filesystem" || srv.Command != "npx" {
		t.Errorf("MCP server unexpected: %+v", srv)
	}

	// Root/AgentsDir are NOT set by the importer.
	if proj.Root != "" {
		t.Errorf("proj.Root = %q, want empty (engine fills in)", proj.Root)
	}
	if proj.AgentsDir != "" {
		t.Errorf("proj.AgentsDir = %q, want empty (engine fills in)", proj.AgentsDir)
	}
}

// TestCursor_HeuristicWarning: a .mdc with mixed (path + extension)
// globs should produce a warn-severity Warning.
func TestCursor_HeuristicWarning(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".cursor", "rules")
	mustMkdir(t, rulesDir)

	mustWrite(t, filepath.Join(rulesDir, "mixed.mdc"), `---
description: mixed globs
globs: ["src/**", "**/*.pdf"]
---
Ambiguous mapping body.
`)

	_, warnings, err := NewCursor().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	var found bool
	for _, w := range warnings {
		if w.Severity == "warn" && strings.Contains(w.Heuristic, "mix") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a warn-severity Warning about mixed globs; got %+v", warnings)
	}
}

// helper: mkdir -p.
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// helper: write file (parents implied).
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestCursor_PathTraversal_Rejected: a hostile .cursor/rules/api.mdc with
// globs starting with ".." must NOT produce a scope path that escapes
// .agents/. The importer should either fall back to "no scope" (skill
// classification) or drop the rule entirely, never write outside the tree.
func TestCursor_PathTraversal_Rejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
description: hostile
globs: ["../../etc/**"]
---
attacker payload
`
	if err := os.WriteFile(filepath.Join(dir, ".cursor", "rules", "api.mdc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	imp := NewCursor()
	proj, _, err := imp.Import(dir)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	// Either: scope rejected → no Scope with the bad path, OR rule treated as
	// global context/skill. The MUST: no scope.Path containing "..".
	for _, s := range proj.Scopes {
		if strings.Contains(s.Path, "..") {
			t.Fatalf("scope path escapes .agents/: %q", s.Path)
		}
	}
}
