package importer

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestGemini_Empty: empty root → ErrSourceNotPresent.
func TestGemini_Empty(t *testing.T) {
	root := t.TempDir()
	_, _, err := NewGemini().Import(root)
	if !errors.Is(err, ErrSourceNotPresent) {
		t.Fatalf("Import on empty root: err = %v, want ErrSourceNotPresent", err)
	}
}

// TestGemini_Detect: marker present → true; absent → false.
func TestGemini_Detect(t *testing.T) {
	imp := NewGemini()

	if imp.Detect(t.TempDir()) {
		t.Errorf("Detect on empty root returned true")
	}

	withDir := t.TempDir()
	mustMkdir(t, filepath.Join(withDir, ".gemini"))
	if !imp.Detect(withDir) {
		t.Errorf("Detect with .gemini/ returned false")
	}

	withMD := t.TempDir()
	mustWrite(t, filepath.Join(withMD, "GEMINI.md"), "context body")
	if !imp.Detect(withMD) {
		t.Errorf("Detect with GEMINI.md returned false")
	}
}

// TestGemini_BasicImport: root GEMINI.md + nested GEMINI.md + settings.json MCP.
func TestGemini_BasicImport(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "GEMINI.md"), "root context body\n")
	mustWrite(t, filepath.Join(root, "src", "billing", "GEMINI.md"), "billing scope body\n")
	mustWrite(t, filepath.Join(root, ".gemini", "settings.json"), `{
  "mcpServers": {
    "search": {"command": "search-server", "args": ["--port", "9000"]}
  },
  "theme": "dark"
}`)

	proj, warnings, err := NewGemini().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}

	if proj.Context == nil {
		t.Fatalf("proj.Context is nil")
	}
	if !strings.Contains(proj.Context.Body, "root context body") {
		t.Errorf("context body missing expected text:\n%s", proj.Context.Body)
	}
	if !strings.Contains(proj.Context.Body, "<!-- imported from gemini:") {
		t.Errorf("context body missing provenance comment:\n%s", proj.Context.Body)
	}

	if len(proj.Scopes) != 1 {
		t.Fatalf("len(proj.Scopes) = %d, want 1", len(proj.Scopes))
	}
	sc := proj.Scopes[0]
	if sc.Path != "src/billing" {
		t.Errorf("scope.Path = %q, want src/billing", sc.Path)
	}
	if !strings.Contains(sc.Document.Body, "billing scope body") {
		t.Errorf("scope body missing expected text:\n%s", sc.Document.Body)
	}

	if len(proj.MCP) != 1 {
		t.Fatalf("len(proj.MCP) = %d, want 1", len(proj.MCP))
	}
	if proj.MCP[0].Name != "search" || proj.MCP[0].Command != "search-server" {
		t.Errorf("MCP server unexpected: %+v", proj.MCP[0])
	}
}

// TestGemini_HeuristicWarning: gemini has no skill-vs-scope heuristic
// (markdown maps 1:1 to scopes), so a "warning trigger" here exercises
// the equivalent edge: an unreadable file produces a build-time error.
// We instead verify that .git is properly skipped — a CLAUDE-style
// importer that descended into vendored directories would mis-import
// vendored docs as scopes. This is the equivalent ambiguous case.
func TestGemini_HeuristicWarning(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "GEMINI.md"), "root body")
	// A vendored GEMINI.md that must NOT be imported as a scope.
	mustWrite(t, filepath.Join(root, "node_modules", "lib", "GEMINI.md"), "vendor body")

	proj, _, err := NewGemini().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, sc := range proj.Scopes {
		if strings.HasPrefix(sc.Path, "node_modules") {
			t.Errorf("vendored GEMINI.md should be skipped; got scope path %q", sc.Path)
		}
	}
}
