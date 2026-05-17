package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// mergeCounterPlugin emits a single OpMerge whose Merger increments a shared
// atomic counter, letting tests verify the engine-resolves-once contract
// across the full Compile -> Apply pipeline.
type mergeCounterPlugin struct {
	calls *int64
	path  string
}

func (mergeCounterPlugin) Name() string       { return "merge-counter" }
func (mergeCounterPlugin) Detect(string) bool { return true }
func (mergeCounterPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{}
}

func (p mergeCounterPlugin) Plan(_ *model.Project, _ model.TargetOption) ([]plugin.Operation, error) {
	calls := p.calls
	return []plugin.Operation{{
		Kind:   plugin.OpMerge,
		Path:   p.path,
		Plugin: "merge-counter",
		Merger: func(existing []byte) (string, error) {
			n := atomic.AddInt64(calls, 1)
			return fmt.Sprintf("merged-call-%d\n", n), nil
		},
	}}, nil
}

// TestCompile_MergerInvokedOnce locks in the v0.7.1 contract: when a plugin
// supplies an OpMerge.Merger closure, engine.Compile resolves it exactly once
// (in compile.go), stashes the bytes in op.Content, and apply.Apply writes
// those bytes verbatim instead of re-running the closure. Re-running races
// against concurrent edits and yields lockfile/disk hash drift.
func TestCompile_MergerInvokedOnce(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".agents"))
	mustWrite(t, filepath.Join(root, ".agents", "context.md"), "# root\n")

	var calls int64
	reg := plugin.NewRegistry()
	if err := reg.Register(mergeCounterPlugin{calls: &calls, path: "merged.txt"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	rep, err := engine.Compile(engine.Options{Root: root, Registry: reg, Quiet: true})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if rep.Changed != 1 {
		t.Errorf("Changed = %d, want 1", rep.Changed)
	}

	got := atomic.LoadInt64(&calls)
	if got != 1 {
		t.Fatalf("Merger invocation count = %d, want exactly 1 (compile->apply must reuse compile's result, not re-run the closure)", got)
	}

	on, err := os.ReadFile(filepath.Join(root, "merged.txt"))
	if err != nil {
		t.Fatalf("read merged.txt: %v", err)
	}
	if string(on) != "merged-call-1\n" {
		t.Errorf("disk content = %q, want %q (lockfile and disk must hash the same Merger output)", on, "merged-call-1\n")
	}
}
