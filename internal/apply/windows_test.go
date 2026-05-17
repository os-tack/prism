package apply

import (
	"os"
	"path/filepath"
	"testing"

	"agents.dev/agents/internal/plugin"
)

// TestApply_SymlinkFallback_ForcedDowngrade exercises the Windows
// symlink-fallback branch on any OS by overriding the package-level
// shouldDowngradeSymlink hook. The downgrade must read the symlink
// target's bytes and write them at op.Path, leaving no symlink behind.
func TestApply_SymlinkFallback_ForcedDowngrade(t *testing.T) {
	root := t.TempDir()

	// Seed the canonical source the "symlink" would otherwise point at.
	srcAbs := filepath.Join(root, ".agents", "context.md")
	if err := os.MkdirAll(filepath.Dir(srcAbs), 0o755); err != nil {
		t.Fatalf("mkdir .agents: %v", err)
	}
	if err := os.WriteFile(srcAbs, []byte("canonical body"), 0o644); err != nil {
		t.Fatalf("seed context.md: %v", err)
	}

	prev := shouldDowngradeSymlink
	shouldDowngradeSymlink = func() bool { return true }
	t.Cleanup(func() { shouldDowngradeSymlink = prev })

	// The op points at CLAUDE.md, with LinkTarget relative to that file's
	// directory — same shape the plugins emit.
	ops := []plugin.Operation{
		{
			Kind:       plugin.OpSymlink,
			Path:       "CLAUDE.md",
			LinkTarget: ".agents/context.md",
			Plugin:     "claude",
		},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}

	dst := filepath.Join(root, "CLAUDE.md")
	fi, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("lstat dst: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("downgrade left a symlink behind: mode=%v", fi.Mode())
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "canonical body" {
		t.Fatalf("dst content = %q, want %q", data, "canonical body")
	}
}

// TestApply_SymlinkFallback_NotTriggered confirms the production path is
// unaffected by the hook's default value on non-Windows: when the hook
// returns false, OpSymlink behaves exactly as before.
func TestApply_SymlinkFallback_NotTriggered(t *testing.T) {
	root := t.TempDir()

	prev := shouldDowngradeSymlink
	shouldDowngradeSymlink = func() bool { return false }
	t.Cleanup(func() { shouldDowngradeSymlink = prev })

	ops := []plugin.Operation{
		{
			Kind:       plugin.OpSymlink,
			Path:       "CLAUDE.md",
			LinkTarget: ".agents/context.md",
		},
	}
	if _, _, err := Apply(root, ops, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink, mode=%v", fi.Mode())
	}
}

// TestApply_SymlinkFallback_MissingTargetErrors confirms the downgrade
// reports a useful error if the source file the "symlink" would have
// pointed at is missing.
func TestApply_SymlinkFallback_MissingTargetErrors(t *testing.T) {
	root := t.TempDir()

	prev := shouldDowngradeSymlink
	shouldDowngradeSymlink = func() bool { return true }
	t.Cleanup(func() { shouldDowngradeSymlink = prev })

	ops := []plugin.Operation{
		{
			Kind:       plugin.OpSymlink,
			Path:       "CLAUDE.md",
			LinkTarget: ".agents/missing.md",
		},
	}
	_, _, err := Apply(root, ops, false)
	if err == nil {
		t.Fatalf("expected error when symlink target is missing, got nil")
	}
}

// TestApply_SymlinkFallback_RemovesPreexistingSymlink is the v0.6 review's
// I5 regression test: when the Windows downgrade fires and abs is itself
// an existing symlink (e.g. project was Unix-synced before being opened on
// Windows), the fallback must remove the link before the OpWrite re-entry.
// Otherwise os.WriteFile follows the symlink and silently overwrites the
// canonical source through the link.
func TestApply_SymlinkFallback_RemovesPreexistingSymlink(t *testing.T) {
	prev := shouldDowngradeSymlink
	shouldDowngradeSymlink = func() bool { return true }
	t.Cleanup(func() { shouldDowngradeSymlink = prev })

	root := t.TempDir()
	canonical := filepath.Join(root, ".agents", "context.md")
	mustWriteApplyTest(t, canonical, "canonical content")
	target := filepath.Join(root, "CLAUDE.md")
	// Pre-seed an existing symlink at the target — this is the cross-platform
	// sync scenario described in the v0.6 review.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(".agents", "context.md"), target); err != nil {
		t.Fatal(err)
	}

	op := plugin.Operation{
		Kind:       plugin.OpSymlink,
		Path:       "CLAUDE.md",
		LinkTarget: filepath.Join(".agents", "context.md"),
	}
	if _, _, err := Apply(root, []plugin.Operation{op}, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Canonical source must be unchanged.
	got, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("read canonical: %v", err)
	}
	if string(got) != "canonical content" {
		t.Errorf("canonical source mutated by write-through symlink; got %q want %q",
			string(got), "canonical content")
	}
	// Target must be a regular file now (the fallback's downgrade).
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("target is still a symlink; fallback didn't remove it")
	}
}

// mustWriteApplyTest creates parent dirs and writes content; sibling of the
// other write helpers in this package's test files.
func mustWriteApplyTest(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
