package apply

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"agents.dev/agents/internal/plugin"
)

func TestApply_Write_CreatesFileAndParentDirs(t *testing.T) {
	root := t.TempDir()
	ops := []plugin.Operation{
		{
			Kind:    plugin.OpWrite,
			Path:    "src/billing/CLAUDE.md",
			Content: "hello",
		},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	full := filepath.Join(root, "src/billing/CLAUDE.md")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want %q", data, "hello")
	}
	// Verify parent dirs exist with reasonable mode
	di, err := os.Stat(filepath.Join(root, "src/billing"))
	if err != nil {
		t.Fatalf("stat parent dir: %v", err)
	}
	if !di.IsDir() {
		t.Fatalf("parent is not a dir")
	}
	if di.Mode().Perm()&0o500 == 0 {
		t.Fatalf("parent dir mode lacks read/exec for owner: %v", di.Mode())
	}
}

func TestApply_Write_HashShortCircuit(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "x.md")
	if err := os.WriteFile(target, []byte("already-here"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Capture mtime BEFORE applying (truncate to second to be safe)
	preInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("pre-stat: %v", err)
	}
	preMtime := preInfo.ModTime()

	// Wait a moment so we can detect a re-write
	time.Sleep(15 * time.Millisecond)

	ops := []plugin.Operation{
		{Kind: plugin.OpWrite, Path: "x.md", Content: "already-here"},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 0 || unchanged != 1 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 0/1", changed, unchanged)
	}

	postInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("post-stat: %v", err)
	}
	if !postInfo.ModTime().Equal(preMtime) {
		t.Fatalf("mtime changed despite identical content: pre=%v post=%v", preMtime, postInfo.ModTime())
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "already-here" {
		t.Fatalf("content changed: %q", data)
	}
}

func TestApply_Write_OverwritesDifferentContent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "y.md")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpWrite, Path: "y.md", Content: "new"},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q, want %q", data, "new")
	}
}

func TestApply_Symlink_CreatesLink(t *testing.T) {
	root := t.TempDir()
	ops := []plugin.Operation{
		{
			Kind:       plugin.OpSymlink,
			Path:       "sub/CLAUDE.md",
			LinkTarget: ".agents/context.md",
		},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	full := filepath.Join(root, "sub/CLAUDE.md")
	fi, err := os.Lstat(full)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("not a symlink: mode=%v", fi.Mode())
	}
	tgt, err := os.Readlink(full)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if tgt != ".agents/context.md" {
		t.Fatalf("symlink target = %q, want %q", tgt, ".agents/context.md")
	}
	// Parent dir exists
	if _, err := os.Stat(filepath.Join(root, "sub")); err != nil {
		t.Fatalf("parent dir missing: %v", err)
	}
}

func TestApply_Symlink_ReplacesStale(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "CLAUDE.md")
	if err := os.Symlink("old-target.md", target); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}
	ops := []plugin.Operation{
		{
			Kind:       plugin.OpSymlink,
			Path:       "CLAUDE.md",
			LinkTarget: "new-target.md",
		},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	tgt, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if tgt != "new-target.md" {
		t.Fatalf("symlink target = %q, want %q", tgt, "new-target.md")
	}
}

func TestApply_Symlink_MatchingExistingLink_Unchanged(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "CLAUDE.md")
	if err := os.Symlink(".agents/context.md", target); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}
	ops := []plugin.Operation{
		{
			Kind:       plugin.OpSymlink,
			Path:       "CLAUDE.md",
			LinkTarget: ".agents/context.md",
		},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 0 || unchanged != 1 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 0/1", changed, unchanged)
	}
}

func TestApply_Append_CreatesIfAbsent(t *testing.T) {
	root := t.TempDir()
	ops := []plugin.Operation{
		{Kind: plugin.OpAppend, Path: "notes/log.md", Content: "first line\n"},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes/log.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "first line\n" {
		t.Fatalf("content = %q, want %q", data, "first line\n")
	}
}

func TestApply_Append_AppendsToExisting(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "log.md")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpAppend, Path: "log.md", Content: "world"},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello\nworld" {
		t.Fatalf("content = %q, want %q", data, "hello\nworld")
	}
}

func TestApply_Delete_RemovesExisting(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "gone.md")
	if err := os.WriteFile(target, []byte("bye"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpDelete, Path: "gone.md"},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("file still exists or wrong error: %v", err)
	}
}

func TestApply_Delete_MissingIsNoOp(t *testing.T) {
	root := t.TempDir()
	ops := []plugin.Operation{
		{Kind: plugin.OpDelete, Path: "never-existed.md"},
	}
	changed, unchanged, err := Apply(root, ops, false)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 0 || unchanged != 1 {
		t.Fatalf("counts: changed=%d unchanged=%d, want 0/1", changed, unchanged)
	}
}

func TestApply_DryRun_DoesNotTouchFS(t *testing.T) {
	root := t.TempDir()

	// Existing file we'll pretend to overwrite and append to and delete
	existing := filepath.Join(root, "existing.md")
	if err := os.WriteFile(existing, []byte("orig\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ops := []plugin.Operation{
		{Kind: plugin.OpWrite, Path: "new-file.md", Content: "fresh"},
		{Kind: plugin.OpWrite, Path: "existing.md", Content: "overwritten"},
		{Kind: plugin.OpAppend, Path: "existing.md", Content: "appended"},
		{Kind: plugin.OpDelete, Path: "existing.md"},
	}
	changed, _, err := Apply(root, ops, true)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if changed != 4 {
		t.Fatalf("changed = %d, want 4", changed)
	}
	// new-file.md must NOT exist
	if _, err := os.Stat(filepath.Join(root, "new-file.md")); !os.IsNotExist(err) {
		t.Fatalf("new-file.md should not exist after dry run, got err=%v", err)
	}
	// existing.md must still exist with original content
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("existing.md gone or unreadable: %v", err)
	}
	if string(data) != "orig\n" {
		t.Fatalf("existing.md content modified: %q", data)
	}
}
