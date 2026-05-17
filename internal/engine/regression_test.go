package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/parser"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/plugins"
)

func regressionFixture(t *testing.T) (root string, reg *plugin.Registry) {
	t.Helper()
	root = t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".agents"))
	mustMkdirAll(t, filepath.Join(root, ".claude"))
	mustWrite(t, filepath.Join(root, ".agents", "context.md"), "# root\n")
	reg = plugin.NewRegistry()
	_ = reg.Register(plugins.NewClaude())
	_ = reg.Register(plugins.NewCursor())
	_ = reg.Register(plugins.NewAgentsMD())
	return root, reg
}

// Regression for review bug #2: Check must return ErrDrift when only deletes
// are pending, not just when Changed > 0.
func TestCheck_DriftFromRemovedOnly(t *testing.T) {
	root, reg := regressionFixture(t)
	// Add a scope, compile, then remove the scope.
	mustMkdirAll(t, filepath.Join(root, ".agents", "src", "auth"))
	mustWrite(t, filepath.Join(root, ".agents", "src", "auth", "context.md"), "# auth\n")
	if _, err := engine.Compile(engine.Options{Root: root, Registry: reg, Quiet: true}); err != nil {
		t.Fatalf("initial compile: %v", err)
	}
	// Remove the source scope entirely.
	if err := os.RemoveAll(filepath.Join(root, ".agents", "src")); err != nil {
		t.Fatalf("remove scope: %v", err)
	}
	rep, err := engine.Check(engine.Options{Root: root, Registry: reg, Quiet: true})
	if !errors.Is(err, engine.ErrDrift) {
		t.Fatalf("Check error = %v, want ErrDrift (rep=%+v)", err, rep)
	}
	if rep == nil || rep.Removed == 0 {
		t.Fatalf("expected Removed > 0, got rep=%+v", rep)
	}
}

// Regression for review bug #8: errors.Is must work in BOTH directions
// for the No-Agents-Dir sentinel.
func TestErrNoAgentsDir_AliasParity(t *testing.T) {
	root := t.TempDir()
	_, err := engine.Compile(engine.Options{Root: root, Registry: plugin.NewRegistry(), Quiet: true})
	if !errors.Is(err, engine.ErrNoAgentsDir) {
		t.Fatalf("errors.Is engine sentinel failed: %v", err)
	}
	if !errors.Is(err, parser.ErrNoAgentsDir) {
		t.Fatalf("errors.Is parser sentinel failed (sentinels should alias): %v", err)
	}
}

// Regression for review bug #9: manual-edit detection emits a warning when
// a tracked projected file has been edited since last write.
func TestCompile_DetectsManualEdits(t *testing.T) {
	root, reg := regressionFixture(t)
	mustMkdirAll(t, filepath.Join(root, ".cursor"))
	if _, err := engine.Compile(engine.Options{Root: root, Registry: reg, Quiet: true}); err != nil {
		t.Fatalf("initial compile: %v", err)
	}
	// Manually edit AGENTS.md (a tracked write op).
	agentsPath := filepath.Join(root, "AGENTS.md")
	mustWrite(t, agentsPath, "manually edited content\n")
	// Now change the source so compile would overwrite the manual edit.
	mustWrite(t, filepath.Join(root, ".agents", "context.md"), "# root v2\n")
	rep, err := engine.Compile(engine.Options{Root: root, Registry: reg, Quiet: true})
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	found := false
	for _, w := range rep.Warnings {
		if w.Severity == "warn" && w.Source == "AGENTS.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected manual-edit warning for AGENTS.md, got warnings=%+v", rep.Warnings)
	}
}

func mustMkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}
