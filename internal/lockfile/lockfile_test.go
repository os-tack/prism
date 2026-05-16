package lockfile

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoad_Missing(t *testing.T) {
	tmp := t.TempDir()
	// No .agents/.lock present.
	lf, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load(missing) returned error: %v", err)
	}
	if lf == nil {
		t.Fatal("Load(missing) returned nil Lockfile, want non-nil empty Lockfile")
	}
	if len(lf.Files) != 0 {
		t.Errorf("Load(missing) Files = %v, want empty", lf.Files)
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	original := &Lockfile{
		Version:     1,
		GeneratedBy: "agents-test/0.1",
		At:          now,
		Files: map[string]Entry{
			"CLAUDE.md": {
				Sources: []string{".agents/context.md"},
				Plugin:  "claude",
				Kind:    "symlink",
			},
			".cursor/rules/agents.mdc": {
				Sources: []string{".agents/context.md", ".agents/src/billing/context.md"},
				Plugin:  "cursor",
				Kind:    "write",
				Hash:    "sha256:abcdef0123456789",
			},
			"AGENTS.md": {
				Sources: []string{".agents/context.md"},
				Plugin:  "agents-md",
				Kind:    "write",
				Hash:    "sha256:fedcba9876543210",
			},
		},
	}

	if err := original.Save(tmp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !reflect.DeepEqual(loaded.Files, original.Files) {
		t.Errorf("Files differ after roundtrip:\nloaded  = %#v\noriginal= %#v", loaded.Files, original.Files)
	}
}

func TestSave_StableOrder(t *testing.T) {
	// Create entries in different orders; bytes must match.
	makeFiles := func() map[string]Entry {
		return map[string]Entry{
			"a/one":   {Sources: []string{".agents/context.md"}, Plugin: "p1", Kind: "write", Hash: "h1"},
			"b/two":   {Sources: []string{".agents/x.md"}, Plugin: "p2", Kind: "symlink"},
			"c/three": {Sources: []string{".agents/y.md"}, Plugin: "p3", Kind: "write", Hash: "h3"},
			"d/four":  {Sources: []string{".agents/z.md", ".agents/q.md"}, Plugin: "p4", Kind: "write", Hash: "h4"},
			"e/five":  {Sources: []string{".agents/w.md"}, Plugin: "p5", Kind: "symlink"},
		}
	}

	// Fixed "at" timestamp so bytes match across both saves.
	fixedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tmp1 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp1, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir1: %v", err)
	}
	lf1 := &Lockfile{
		Version:     1,
		GeneratedBy: "agents-test/0.1",
		At:          fixedAt,
		Files:       makeFiles(),
	}
	if err := lf1.Save(tmp1); err != nil {
		t.Fatalf("Save1: %v", err)
	}
	data1, err := os.ReadFile(Path(tmp1))
	if err != nil {
		t.Fatalf("ReadFile1: %v", err)
	}

	// Second save: load tmp1 back, save into tmp2 with same fixedAt.
	loaded, err := Load(tmp1)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded.At = fixedAt
	// Stir map order by re-inserting in a different (random) order.
	rng := rand.New(rand.NewSource(42))
	keys := make([]string, 0, len(loaded.Files))
	for k := range loaded.Files {
		keys = append(keys, k)
	}
	rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	shuffled := make(map[string]Entry, len(keys))
	for _, k := range keys {
		shuffled[k] = loaded.Files[k]
	}
	loaded.Files = shuffled

	tmp2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp2, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir2: %v", err)
	}
	if err := loaded.Save(tmp2); err != nil {
		t.Fatalf("Save2: %v", err)
	}
	data2, err := os.ReadFile(Path(tmp2))
	if err != nil {
		t.Fatalf("ReadFile2: %v", err)
	}

	if !bytes.Equal(data1, data2) {
		t.Errorf("Save output not deterministic:\n--- first ---\n%s\n--- second ---\n%s", data1, data2)
	}
}

func TestSave_CreatesAgentsDir(t *testing.T) {
	tmp := t.TempDir()
	// Note: .agents/ does NOT exist.
	lf := &Lockfile{
		Version:     1,
		GeneratedBy: "agents-test/0.1",
		At:          time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Files: map[string]Entry{
			"CLAUDE.md": {Sources: []string{".agents/context.md"}, Plugin: "claude", Kind: "symlink"},
		},
	}
	if err := lf.Save(tmp); err != nil {
		t.Fatalf("Save without pre-existing .agents/: %v", err)
	}
	// .agents/ must now exist.
	info, err := os.Stat(filepath.Join(tmp, ".agents"))
	if err != nil {
		t.Fatalf("stat .agents after Save: %v", err)
	}
	if !info.IsDir() {
		t.Errorf(".agents is not a directory after Save")
	}
	// .lock must exist too.
	if _, err := os.Stat(Path(tmp)); err != nil {
		t.Errorf("stat .lock after Save: %v", err)
	}
}
