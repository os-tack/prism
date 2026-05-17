package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/lockfile"
)

// TestCompile_OperationPathsForwardSlash verifies that every op the
// engine returns in its Report uses forward slashes in Path. This is the
// invariant that makes lockfile keys and `agents which` lookups portable
// between Windows-built and macOS-built projects.
func TestCompile_OperationPathsForwardSlash(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	for _, op := range rep.Operations {
		if strings.Contains(op.Path, "\\") {
			t.Errorf("Operation.Path %q contains backslash; want forward slashes only", op.Path)
		}
	}
}

// TestCompile_LockfileKeysAreSlashPaths reads .agents/.lock directly via
// the lockfile package after a Compile and asserts every key is a
// forward-slash path. This is the contract `agents which` relies on.
func TestCompile_LockfileKeysAreSlashPaths(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	lf, err := lockfile.Load(root)
	if err != nil {
		t.Fatalf("lockfile.Load: %v", err)
	}
	if len(lf.Files) == 0 {
		t.Fatalf("lockfile has no entries after Compile")
	}
	for key := range lf.Files {
		if strings.Contains(key, "\\") {
			t.Errorf("lockfile key %q contains backslash; want forward slashes only", key)
		}
		// Bonus: assert at least one nested-scope path is present so we
		// know the test would catch a separator regression.
	}

	// Specifically, src/billing/CLAUDE.md must be a key (it is the
	// nested path most likely to fail on Windows).
	if _, ok := lf.Files["src/billing/CLAUDE.md"]; !ok {
		t.Errorf("lockfile missing expected slash-form key src/billing/CLAUDE.md; have keys: %v", keys(lf.Files))
	}
}

// TestCompile_LegacyBackslashLockfile_IsReconciled simulates a Windows-built
// lockfile (backslash keys) being read by the current engine. The compile
// pass should treat the existing slash-form planned paths as the same
// entries and NOT emit spurious deletes.
func TestCompile_LegacyBackslashLockfile_IsReconciled(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	// First compile to produce a real lockfile, then rewrite the
	// nested-scope key with a backslash to mimic a Windows-built artifact.
	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile (initial): %v", err)
	}
	lf, err := lockfile.Load(root)
	if err != nil {
		t.Fatalf("lockfile.Load: %v", err)
	}
	entry, ok := lf.Files["src/billing/CLAUDE.md"]
	if !ok {
		t.Fatalf("expected src/billing/CLAUDE.md in lockfile; have: %v", keys(lf.Files))
	}
	delete(lf.Files, "src/billing/CLAUDE.md")
	lf.Files[`src\billing\CLAUDE.md`] = entry
	if err := lf.Save(root); err != nil {
		t.Fatalf("re-save tainted lockfile: %v", err)
	}

	// Second compile: the engine must reconcile the backslash key with
	// the slash-form planned path and emit no deletes (Removed == 0).
	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("Compile (reconcile): %v", err)
	}
	if rep.Removed != 0 {
		t.Errorf("Removed = %d, want 0 (backslash key should be reconciled, not treated as stale); ops=%+v", rep.Removed, rep.Operations)
	}

	// And the rewritten lockfile must again use slash form.
	lf2, err := lockfile.Load(root)
	if err != nil {
		t.Fatalf("lockfile.Load (post): %v", err)
	}
	if _, ok := lf2.Files["src/billing/CLAUDE.md"]; !ok {
		t.Errorf("post-reconcile lockfile missing slash key; have: %v", keys(lf2.Files))
	}
	if _, ok := lf2.Files[`src\billing\CLAUDE.md`]; ok {
		t.Errorf("post-reconcile lockfile still has backslash key")
	}
}

// TestWhich_FindsSlashKeyedEntries verifies engine.Which round-trips
// through the slash-form lockfile keys for nested scopes.
func TestWhich_FindsSlashKeyedEntries(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Slash form succeeds (and Which calls filepath.ToSlash internally,
	// so an absolute path on any OS should also resolve).
	srcs, err := engine.Which(opts, "src/billing/CLAUDE.md")
	if err != nil {
		t.Fatalf("Which slash form: %v", err)
	}
	if len(srcs) == 0 {
		t.Fatalf("Which returned no sources")
	}

	abs := filepath.Join(root, "src", "billing", "CLAUDE.md")
	if _, err := os.Lstat(abs); err == nil {
		if _, err := engine.Which(opts, abs); err != nil {
			t.Fatalf("Which absolute path: %v", err)
		}
	}
}

func keys(m map[string]lockfile.Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
