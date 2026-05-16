package parser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeInclude writes content to path, creating parent dirs as needed.
// Test-local helper to avoid colliding with writeFile in parser_test.go.
func writeInclude(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestExpandIncludes_Basic(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	stylePath := filepath.Join(agentsDir, "common", "style.md")

	writeInclude(t, ctxPath, "head\n<!-- include: common/style.md -->\ntail\n")
	writeInclude(t, stylePath, "STYLE BODY\n")

	body := "head\n<!-- include: common/style.md -->\ntail\n"
	got, includes, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	want := "head\nSTYLE BODY\ntail\n"
	if got != want {
		t.Errorf("expanded body = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(includes, []string{stylePath}) {
		t.Errorf("includes = %v, want [%s]", includes, stylePath)
	}
}

func TestExpandIncludes_RelativeResolution(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	billingCtx := filepath.Join(agentsDir, "src", "billing", "context.md")
	securityMD := filepath.Join(agentsDir, "common", "security.md")

	writeInclude(t, billingCtx, "ignored")
	writeInclude(t, securityMD, "SECURITY RULES\n")

	body := "<!-- include: ../../common/security.md -->\n"
	got, includes, err := expandIncludes(body, billingCtx, agentsDir, "", 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	if got != "SECURITY RULES\n" {
		t.Errorf("expanded body = %q, want %q", got, "SECURITY RULES\n")
	}
	if len(includes) != 1 || includes[0] != securityMD {
		t.Errorf("includes = %v, want [%s]", includes, securityMD)
	}
}

func TestExpandIncludes_Global(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, "proj", ".agents")
	globalDir := filepath.Join(tmp, "home", ".agents")

	ctxPath := filepath.Join(agentsDir, "context.md")
	reviewer := filepath.Join(globalDir, "agents", "reviewer.md")

	writeInclude(t, ctxPath, "ignored")
	writeInclude(t, reviewer, "REVIEWER PROMPT\n")

	body := "before\n<!-- include: global:agents/reviewer.md -->\nafter\n"
	got, includes, err := expandIncludes(body, ctxPath, agentsDir, globalDir, 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	want := "before\nREVIEWER PROMPT\nafter\n"
	if got != want {
		t.Errorf("expanded body = %q, want %q", got, want)
	}
	if len(includes) != 1 || includes[0] != reviewer {
		t.Errorf("includes = %v, want [%s]", includes, reviewer)
	}
}

func TestExpandIncludes_GlobalRequiresGlobalDir(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	writeInclude(t, ctxPath, "ignored")

	body := "<!-- include: global:agents/reviewer.md -->\n"
	_, _, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err == nil {
		t.Fatal("expected error when global: include used with no globalAgentsDir")
	}
	if !errors.Is(err, ErrIncludeEscape) {
		t.Errorf("err = %v, want ErrIncludeEscape", err)
	}
}

func TestExpandIncludes_Cycle(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	aPath := filepath.Join(agentsDir, "a.md")
	bPath := filepath.Join(agentsDir, "b.md")

	writeInclude(t, aPath, "<!-- include: b.md -->\n")
	writeInclude(t, bPath, "<!-- include: a.md -->\n")

	body := "<!-- include: b.md -->\n"
	_, _, err := expandIncludes(body, aPath, agentsDir, "", 16)
	if err == nil {
		t.Fatal("expected ErrIncludeCycle, got nil")
	}
	if !errors.Is(err, ErrIncludeCycle) {
		t.Errorf("err = %v, want ErrIncludeCycle", err)
	}
}

func TestExpandIncludes_MaxDepth(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")

	// Build chain f0 → f1 → ... → f17. Total chain depth is 17 hops.
	for i := 0; i < 17; i++ {
		var content string
		if i < 16 {
			content = fmt.Sprintf("<!-- include: f%d.md -->\n", i+1)
		} else {
			content = "leaf\n"
		}
		writeInclude(t, filepath.Join(agentsDir, fmt.Sprintf("f%d.md", i)), content)
	}

	body := "<!-- include: f0.md -->\n"
	rootCtx := filepath.Join(agentsDir, "context.md")
	writeInclude(t, rootCtx, "stub")

	_, _, err := expandIncludes(body, rootCtx, agentsDir, "", 16)
	if err == nil {
		t.Fatal("expected ErrIncludeMaxDepth, got nil")
	}
	if !errors.Is(err, ErrIncludeMaxDepth) {
		t.Errorf("err = %v, want ErrIncludeMaxDepth", err)
	}
}

func TestExpandIncludes_AbsolutePathRejected(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	writeInclude(t, ctxPath, "ignored")

	body := "<!-- include: /etc/passwd -->\n"
	_, _, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err == nil {
		t.Fatal("expected ErrIncludeEscape, got nil")
	}
	if !errors.Is(err, ErrIncludeEscape) {
		t.Errorf("err = %v, want ErrIncludeEscape", err)
	}
}

func TestExpandIncludes_TraversalEscape(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	writeInclude(t, ctxPath, "ignored")

	body := "<!-- include: ../../../etc/passwd -->\n"
	_, _, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err == nil {
		t.Fatal("expected ErrIncludeEscape, got nil")
	}
	if !errors.Is(err, ErrIncludeEscape) {
		t.Errorf("err = %v, want ErrIncludeEscape", err)
	}
}

func TestExpandIncludes_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	writeInclude(t, ctxPath, "ignored")

	body := "<!-- include: nope.md -->\n"
	_, _, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err == nil {
		t.Fatal("expected missing-file error, got nil")
	}
	if !errors.Is(err, ErrIncludeMissing) {
		t.Errorf("err = %v, want ErrIncludeMissing", err)
	}
	if !strings.Contains(err.Error(), "nope.md") {
		t.Errorf("err = %v, want message containing 'nope.md'", err)
	}
}

func TestExpandIncludes_Deduped(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	aPath := filepath.Join(agentsDir, "a.md")
	bPath := filepath.Join(agentsDir, "b.md")

	writeInclude(t, aPath, "ignored")
	writeInclude(t, bPath, "B\n")

	body := "<!-- include: b.md -->\n<!-- include: b.md -->\n"
	got, includes, err := expandIncludes(body, aPath, agentsDir, "", 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	want := "B\nB\n"
	if got != want {
		t.Errorf("expanded body = %q, want %q", got, want)
	}
	if len(includes) != 1 || includes[0] != bPath {
		t.Errorf("includes = %v, want [%s] (deduped)", includes, bPath)
	}
}

func TestExpandIncludes_NoDirectives(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	writeInclude(t, ctxPath, "ignored")

	body := "line one\nline two\nline three\n"
	got, includes, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	if got != body {
		t.Errorf("expanded body = %q, want unchanged %q", got, body)
	}
	if len(includes) != 0 {
		t.Errorf("includes = %v, want empty slice", includes)
	}
}

func TestExpandIncludes_InlineCommentNotExpanded(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	fooPath := filepath.Join(agentsDir, "foo.md")
	writeInclude(t, ctxPath, "ignored")
	writeInclude(t, fooPath, "FOO\n")

	// The directive is on a line that also has trailing text — it must
	// be treated as plain text, NOT expanded. No error.
	body := "<!-- include: foo --> trailing text\n"
	got, includes, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	if got != body {
		t.Errorf("expanded body = %q, want unchanged %q", got, body)
	}
	if len(includes) != 0 {
		t.Errorf("includes = %v, want empty (no expansion on inline comment)", includes)
	}
}

// TestExpandIncludes_RecursiveDeduped exercises a nested case: a.md
// includes b.md which includes c.md; a.md ALSO includes c.md directly.
// Each unique file must appear exactly once in the returned slice.
func TestExpandIncludes_RecursiveDeduped(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	aPath := filepath.Join(agentsDir, "a.md")
	bPath := filepath.Join(agentsDir, "b.md")
	cPath := filepath.Join(agentsDir, "c.md")

	writeInclude(t, aPath, "ignored")
	writeInclude(t, bPath, "<!-- include: c.md -->\n")
	writeInclude(t, cPath, "C\n")

	body := "<!-- include: b.md -->\n<!-- include: c.md -->\n"
	_, includes, err := expandIncludes(body, aPath, agentsDir, "", 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	// b first (encountered before c when expanding a), then c.
	want := []string{bPath, cPath}
	if !reflect.DeepEqual(includes, want) {
		t.Errorf("includes = %v, want %v", includes, want)
	}
}

// TestExpandIncludes_FrontmatterStripped confirms the included file's
// frontmatter is dropped on substitution.
func TestExpandIncludes_FrontmatterStripped(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, ".agents")
	ctxPath := filepath.Join(agentsDir, "context.md")
	stylePath := filepath.Join(agentsDir, "common", "style.md")

	writeInclude(t, ctxPath, "ignored")
	writeInclude(t, stylePath, "---\ntitle: Style\n---\nSTYLE BODY\n")

	body := "<!-- include: common/style.md -->\n"
	got, _, err := expandIncludes(body, ctxPath, agentsDir, "", 16)
	if err != nil {
		t.Fatalf("expandIncludes: %v", err)
	}
	if got != "STYLE BODY\n" {
		t.Errorf("expanded body = %q, want %q (frontmatter must be stripped)", got, "STYLE BODY\n")
	}
}
