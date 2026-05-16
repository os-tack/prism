package diff

import (
	"os"
	"path/filepath"
	"testing"

	"agents.dev/agents/internal/plugin"
)

// helper: assert exactly one of changed/unchanged contains the op.
func assertClassified(t *testing.T, changed, unchanged []plugin.Operation, wantChanged bool) {
	t.Helper()
	if wantChanged {
		if len(changed) != 1 || len(unchanged) != 0 {
			t.Fatalf("want classified as changed; got changed=%d unchanged=%d", len(changed), len(unchanged))
		}
	} else {
		if len(unchanged) != 1 || len(changed) != 0 {
			t.Fatalf("want classified as unchanged; got changed=%d unchanged=%d", len(changed), len(unchanged))
		}
	}
}

func TestClassify_Write_NewFile(t *testing.T) {
	root := t.TempDir()
	ops := []plugin.Operation{
		{Kind: plugin.OpWrite, Path: "new.md", Content: "x"},
	}
	c, u, err := Classify(root, ops)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertClassified(t, c, u, true)
}

func TestClassify_Write_MatchingContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("same"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpWrite, Path: "a.md", Content: "same"},
	}
	c, u, err := Classify(root, ops)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertClassified(t, c, u, false)
}

func TestClassify_Write_DifferentContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpWrite, Path: "a.md", Content: "new"},
	}
	c, u, err := Classify(root, ops)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertClassified(t, c, u, true)
}

func TestClassify_Symlink_Missing(t *testing.T) {
	root := t.TempDir()
	ops := []plugin.Operation{
		{Kind: plugin.OpSymlink, Path: "link", LinkTarget: ".agents/x"},
	}
	c, u, err := Classify(root, ops)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertClassified(t, c, u, true)
}

func TestClassify_Symlink_MatchingTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(".agents/x", filepath.Join(root, "link")); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpSymlink, Path: "link", LinkTarget: ".agents/x"},
	}
	c, u, err := Classify(root, ops)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertClassified(t, c, u, false)
}

func TestClassify_Symlink_DifferentTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("other", filepath.Join(root, "link")); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpSymlink, Path: "link", LinkTarget: ".agents/x"},
	}
	c, u, err := Classify(root, ops)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertClassified(t, c, u, true)
}

func TestClassify_Symlink_RegularFileExists(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "link"), []byte("plain"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	ops := []plugin.Operation{
		{Kind: plugin.OpSymlink, Path: "link", LinkTarget: ".agents/x"},
	}
	c, u, err := Classify(root, ops)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertClassified(t, c, u, true)
}

func TestClassify_Append(t *testing.T) {
	t.Run("missing_with_content", func(t *testing.T) {
		root := t.TempDir()
		ops := []plugin.Operation{
			{Kind: plugin.OpAppend, Path: "log.md", Content: "stuff"},
		}
		c, u, err := Classify(root, ops)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		assertClassified(t, c, u, true)
	})

	t.Run("exists_without_suffix", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "log.md"), []byte("hello\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		ops := []plugin.Operation{
			{Kind: plugin.OpAppend, Path: "log.md", Content: "world"},
		}
		c, u, err := Classify(root, ops)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		assertClassified(t, c, u, true)
	})

	t.Run("exists_with_suffix", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "log.md"), []byte("hello\nworld"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		ops := []plugin.Operation{
			{Kind: plugin.OpAppend, Path: "log.md", Content: "world"},
		}
		c, u, err := Classify(root, ops)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		assertClassified(t, c, u, false)
	})
}

func TestClassify_Delete(t *testing.T) {
	t.Run("exists", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "gone.md"), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		ops := []plugin.Operation{
			{Kind: plugin.OpDelete, Path: "gone.md"},
		}
		c, u, err := Classify(root, ops)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		assertClassified(t, c, u, true)
	})

	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		ops := []plugin.Operation{
			{Kind: plugin.OpDelete, Path: "never.md"},
		}
		c, u, err := Classify(root, ops)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		assertClassified(t, c, u, false)
	})
}

func TestHash_Consistency(t *testing.T) {
	root := t.TempDir()
	content := "hash me with sha-256"
	path := filepath.Join(root, "h.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got, want := HashBytes(data), HashContent(content); got != want {
		t.Fatalf("HashBytes(%q) = %s, HashContent(%q) = %s; want equal", data, got, content, want)
	}
	// Spot-check that the hash is 64 hex chars
	if len(HashContent(content)) != 64 {
		t.Fatalf("HashContent length = %d, want 64", len(HashContent(content)))
	}
	// Different content → different hash
	if HashContent("a") == HashContent("b") {
		t.Fatalf("expected different hashes for distinct inputs")
	}
}
