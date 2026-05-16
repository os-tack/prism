package parser

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

// writeFile writes content to path, creating parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestParse_RootContextOnly(t *testing.T) {
	tmp := t.TempDir()
	rootCtx := filepath.Join(tmp, ".agents", "context.md")
	writeFile(t, rootCtx, "hello world\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj.Context == nil {
		t.Fatal("Project.Context is nil")
	}
	if !filepath.IsAbs(proj.Context.SourcePath) {
		t.Errorf("Context.SourcePath = %q, want absolute", proj.Context.SourcePath)
	}
	if proj.Context.SourcePath != rootCtx {
		t.Errorf("Context.SourcePath = %q, want %q", proj.Context.SourcePath, rootCtx)
	}
	if proj.Context.Body != "hello world\n" {
		t.Errorf("Context.Body = %q, want %q", proj.Context.Body, "hello world\n")
	}
	if len(proj.Scopes) != 0 {
		t.Errorf("Scopes = %v, want empty", proj.Scopes)
	}
}

func TestParse_NoAgentsDir(t *testing.T) {
	tmp := t.TempDir()
	_, err := Parse(tmp)
	if !errors.Is(err, ErrNoAgentsDir) {
		t.Fatalf("Parse returned %v, want ErrNoAgentsDir", err)
	}
}

func TestParse_NestedScope(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	scopeCtx := filepath.Join(tmp, ".agents", "src", "billing", "context.md")
	writeFile(t, scopeCtx, "billing scope\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Scopes) != 1 {
		t.Fatalf("len(Scopes) = %d, want 1", len(proj.Scopes))
	}
	s := proj.Scopes[0]
	if s.Path != "src/billing" {
		t.Errorf("Scope.Path = %q, want %q", s.Path, "src/billing")
	}
	if !reflect.DeepEqual(s.Globs, []string{"src/billing/**"}) {
		t.Errorf("Scope.Globs = %v, want [src/billing/**]", s.Globs)
	}
	if s.Priority != model.PriorityNormal {
		t.Errorf("Scope.Priority = %q, want %q", s.Priority, model.PriorityNormal)
	}
	if s.Document == nil {
		t.Fatal("Scope.Document is nil")
	}
	if !filepath.IsAbs(s.Document.SourcePath) {
		t.Errorf("Scope.Document.SourcePath = %q, want absolute", s.Document.SourcePath)
	}
	if s.Document.SourcePath != scopeCtx {
		t.Errorf("Scope.Document.SourcePath = %q, want %q", s.Document.SourcePath, scopeCtx)
	}
}

func TestParse_ScopesYAMLOverride(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "scopes.yaml"),
		"globs:\n  - src/billing/**\n  - tests/billing/**\ndescription: Stripe stuff\npriority: high\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Scopes) != 1 {
		t.Fatalf("len(Scopes) = %d, want 1", len(proj.Scopes))
	}
	s := proj.Scopes[0]
	wantGlobs := []string{"src/billing/**", "tests/billing/**"}
	if !reflect.DeepEqual(s.Globs, wantGlobs) {
		t.Errorf("Scope.Globs = %v, want %v", s.Globs, wantGlobs)
	}
	if s.Description != "Stripe stuff" {
		t.Errorf("Scope.Description = %q, want %q", s.Description, "Stripe stuff")
	}
	if s.Priority != model.PriorityHigh {
		t.Errorf("Scope.Priority = %q, want %q", s.Priority, model.PriorityHigh)
	}
}

func TestParse_ScopesOrdered(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	// Create paths in deliberately non-sorted order.
	paths := []string{
		filepath.Join(tmp, ".agents", "zeta", "context.md"),
		filepath.Join(tmp, ".agents", "alpha", "context.md"),
		filepath.Join(tmp, ".agents", "src", "billing", "context.md"),
		filepath.Join(tmp, ".agents", "src", "api", "context.md"),
		filepath.Join(tmp, ".agents", "mid", "context.md"),
	}
	for _, p := range paths {
		writeFile(t, p, "body\n")
	}

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := make([]string, len(proj.Scopes))
	for i, s := range proj.Scopes {
		got[i] = s.Path
	}
	want := []string{"alpha", "mid", "src/api", "src/billing", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scope order = %v, want %v", got, want)
	}
}

func TestParse_Frontmatter(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"),
		"---\nkey: value\n---\nbody text")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj.Context == nil {
		t.Fatal("Context is nil")
	}
	if proj.Context.Frontmatter == nil {
		t.Fatal("Frontmatter is nil")
	}
	got, ok := proj.Context.Frontmatter["key"]
	if !ok {
		t.Fatalf("Frontmatter missing key 'key': %v", proj.Context.Frontmatter)
	}
	if got != "value" {
		t.Errorf("Frontmatter[key] = %v, want \"value\"", got)
	}
	if proj.Context.Body != "body text" {
		t.Errorf("Body = %q, want %q", proj.Context.Body, "body text")
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	content := "just body content\nno frontmatter here\n"
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), content)

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj.Context == nil {
		t.Fatal("Context is nil")
	}
	if len(proj.Context.Frontmatter) != 0 {
		t.Errorf("Frontmatter = %v, want nil/empty", proj.Context.Frontmatter)
	}
	if proj.Context.Body != content {
		t.Errorf("Body = %q, want %q", proj.Context.Body, content)
	}
}

func TestParse_ConfigYAML(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "agents.config.yaml"),
		"targets:\n  - claude\n  - cursor\ntarget_options:\n  claude:\n    mode: write\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj.Config == nil {
		t.Fatal("Config is nil")
	}
	wantTargets := []string{"claude", "cursor"}
	if !reflect.DeepEqual(proj.Config.Targets, wantTargets) {
		t.Errorf("Targets = %v, want %v", proj.Config.Targets, wantTargets)
	}
	opt, ok := proj.Config.TargetOptions["claude"]
	if !ok {
		t.Fatalf("TargetOptions missing 'claude': %v", proj.Config.TargetOptions)
	}
	if opt.Mode != "write" {
		t.Errorf("TargetOptions[claude].Mode = %q, want \"write\"", opt.Mode)
	}
}

func TestParse_MalformedScopesYAML(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")
	bad := filepath.Join(tmp, ".agents", "src", "billing", "scopes.yaml")
	// Invalid YAML — a mapping with garbage indentation that won't parse into our struct shape.
	writeFile(t, bad, "globs: [unclosed\n: : not yaml\n  :::\n")

	_, err := Parse(tmp)
	if err == nil {
		t.Fatal("Parse returned nil error for malformed scopes.yaml")
	}
	if !strings.Contains(err.Error(), "scopes.yaml") {
		t.Errorf("error %q does not reference scopes.yaml path", err.Error())
	}
}

func TestParse_AbsoluteSourcePath(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj.Context == nil {
		t.Fatal("Context is nil")
	}
	if !filepath.IsAbs(proj.Context.SourcePath) {
		t.Errorf("Context.SourcePath = %q, want absolute", proj.Context.SourcePath)
	}
	// Resolve symlinks on both sides — macOS /tmp is a symlink to /private/tmp.
	absTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks(tmp): %v", err)
	}
	absSrc, err := filepath.EvalSymlinks(proj.Context.SourcePath)
	if err != nil {
		t.Fatalf("EvalSymlinks(SourcePath): %v", err)
	}
	if !strings.HasPrefix(absSrc, absTmp) {
		t.Errorf("Context.SourcePath %q does not start with tmpdir %q", absSrc, absTmp)
	}
	for _, s := range proj.Scopes {
		if s.Document == nil {
			t.Errorf("scope %q has nil Document", s.Path)
			continue
		}
		if !filepath.IsAbs(s.Document.SourcePath) {
			t.Errorf("scope %q Document.SourcePath = %q, want absolute", s.Path, s.Document.SourcePath)
		}
	}
}
