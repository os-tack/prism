package plan

import (
	"strings"
	"sync"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// mockPlugin is a configurable plugin.Plugin implementation for tests.
type mockPlugin struct {
	name    string
	detect  bool
	ops     []plugin.Operation
	caps    plugin.Capabilities
	planErr error
	// receivedOpt captures whatever TargetOption the engine handed in.
	mu          sync.Mutex
	called      int
	receivedOpt model.TargetOption
}

func (m *mockPlugin) Name() string                      { return m.name }
func (m *mockPlugin) Detect(root string) bool           { return m.detect }
func (m *mockPlugin) Capabilities() plugin.Capabilities { return m.caps }
func (m *mockPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	m.mu.Lock()
	m.called++
	m.receivedOpt = opts
	m.mu.Unlock()
	if m.planErr != nil {
		return nil, m.planErr
	}
	// Return a copy so the engine can mutate Plugin fields without affecting the mock state.
	out := make([]plugin.Operation, len(m.ops))
	copy(out, m.ops)
	return out, nil
}

func newProject() *model.Project {
	return &model.Project{
		Root: "/fake-root",
		Config: &model.Config{
			TargetOptions: map[string]model.TargetOption{},
		},
	}
}

func TestRun_AutoDetect(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "a.md", Content: "a"}},
	}
	b := &mockPlugin{
		name:   "beta",
		detect: false,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "b.md", Content: "b"}},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	reg.Register(b)
	proj := newProject()

	ops, _, err := Run(proj, reg, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.called != 1 {
		t.Fatalf("alpha called %d times, want 1", a.called)
	}
	if b.called != 0 {
		t.Fatalf("beta called %d times, want 0", b.called)
	}
	if len(ops) != 1 || ops[0].Path != "a.md" {
		t.Fatalf("ops = %+v, want only a.md", ops)
	}
}

func TestRun_ExplicitTargets(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "a.md", Content: "a"}},
	}
	b := &mockPlugin{
		name:   "beta",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "b.md", Content: "b"}},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	reg.Register(b)
	proj := newProject()

	ops, _, err := Run(proj, reg, []string{"alpha"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.called != 1 {
		t.Fatalf("alpha called %d, want 1", a.called)
	}
	if b.called != 0 {
		t.Fatalf("beta called %d, want 0 (not in targets)", b.called)
	}
	if len(ops) != 1 || ops[0].Path != "a.md" {
		t.Fatalf("ops = %+v, want only a.md", ops)
	}
}

func TestRun_ExplicitTargetBypassesDetect(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: false, // Detect says NO
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "a.md", Content: "a"}},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	proj := newProject()

	ops, _, err := Run(proj, reg, []string{"alpha"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.called != 1 {
		t.Fatalf("alpha called %d, want 1 (explicit target bypasses detect)", a.called)
	}
	if len(ops) != 1 {
		t.Fatalf("ops len = %d, want 1", len(ops))
	}
}

func TestRun_Disabled(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "a.md", Content: "a"}},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	proj := newProject()
	proj.Config.TargetOptions["alpha"] = model.TargetOption{Disabled: true}

	ops, _, err := Run(proj, reg, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.called != 0 {
		t.Fatalf("alpha called %d times despite being disabled", a.called)
	}
	if len(ops) != 0 {
		t.Fatalf("ops len = %d, want 0", len(ops))
	}
}

func TestRun_ConflictDetection(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "shared.md", Content: "from-a"}},
	}
	b := &mockPlugin{
		name:   "beta",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "shared.md", Content: "from-b"}},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	reg.Register(b)
	proj := newProject()

	_, _, err := Run(proj, reg, nil)
	if err == nil {
		t.Fatalf("expected conflict error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "alpha") || !strings.Contains(msg, "beta") {
		t.Fatalf("error %q must mention both plugin names", msg)
	}
	if !strings.Contains(msg, "shared.md") {
		t.Fatalf("error %q should mention conflicting path", msg)
	}
}

func TestRun_NoConflictDifferentPaths(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "a.md", Content: "a"}},
	}
	b := &mockPlugin{
		name:   "beta",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "b.md", Content: "b"}},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	reg.Register(b)
	proj := newProject()

	ops, _, err := Run(proj, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("ops len = %d, want 2", len(ops))
	}
	paths := map[string]bool{}
	for _, o := range ops {
		paths[o.Path] = true
	}
	if !paths["a.md"] || !paths["b.md"] {
		t.Fatalf("missing expected ops: got %+v", ops)
	}
}

func TestRun_DeterministicOrder(t *testing.T) {
	// Build a registry whose iteration order would otherwise vary.
	// Run multiple times, assert ordering is stable.
	mkRegistry := func() *plugin.Registry {
		r := plugin.NewRegistry()
		r.Register(&mockPlugin{
			name:   "zeta",
			detect: true,
			ops: []plugin.Operation{
				{Kind: plugin.OpWrite, Path: "z2.md", Content: "z2"},
				{Kind: plugin.OpWrite, Path: "z1.md", Content: "z1"},
			},
		})
		r.Register(&mockPlugin{
			name:   "alpha",
			detect: true,
			ops: []plugin.Operation{
				{Kind: plugin.OpWrite, Path: "a2.md", Content: "a2"},
				{Kind: plugin.OpWrite, Path: "a1.md", Content: "a1"},
			},
		})
		r.Register(&mockPlugin{
			name:   "mid",
			detect: true,
			ops: []plugin.Operation{
				{Kind: plugin.OpWrite, Path: "m1.md", Content: "m1"},
			},
		})
		return r
	}

	var first []string
	for i := 0; i < 5; i++ {
		proj := newProject()
		ops, _, err := Run(proj, mkRegistry(), nil)
		if err != nil {
			t.Fatalf("iter %d Run: %v", i, err)
		}
		got := make([]string, len(ops))
		for j, o := range ops {
			got[j] = o.Plugin + ":" + o.Path
		}
		if i == 0 {
			first = got
			// Lock the contract: sorted by plugin name then path.
			want := []string{
				"alpha:a1.md",
				"alpha:a2.md",
				"mid:m1.md",
				"zeta:z1.md",
				"zeta:z2.md",
			}
			if len(got) != len(want) {
				t.Fatalf("ops len = %d, want %d; got=%v", len(got), len(want), got)
			}
			for j := range want {
				if got[j] != want[j] {
					t.Fatalf("ops[%d] = %q, want %q (full got=%v)", j, got[j], want[j], got)
				}
			}
		} else {
			if len(got) != len(first) {
				t.Fatalf("iter %d: len changed: got=%v first=%v", i, got, first)
			}
			for j := range first {
				if got[j] != first[j] {
					t.Fatalf("iter %d: ops[%d] = %q, first = %q", i, j, got[j], first[j])
				}
			}
		}
	}
}

func TestRun_PassesTargetOption(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: true,
		ops:    []plugin.Operation{{Kind: plugin.OpWrite, Path: "a.md", Content: "a"}},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	proj := newProject()
	proj.Config.TargetOptions["alpha"] = model.TargetOption{Mode: "symlink", Disabled: false}

	if _, _, err := Run(proj, reg, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.receivedOpt.Mode != "symlink" {
		t.Fatalf("received Mode = %q, want %q", a.receivedOpt.Mode, "symlink")
	}
	if a.receivedOpt.Disabled != false {
		t.Fatalf("received Disabled = %v, want false", a.receivedOpt.Disabled)
	}
}

func TestRun_AggregatesWarnings(t *testing.T) {
	a := &mockPlugin{
		name:   "alpha",
		detect: true,
		ops: []plugin.Operation{
			{
				Kind:    plugin.OpWrite,
				Path:    "a.md",
				Content: "a",
				Warnings: []plugin.Warning{
					{Source: ".agents/a.md", Message: "alpha-warn-1", Severity: "warn"},
					{Source: ".agents/a.md", Message: "alpha-warn-2", Severity: "info"},
				},
			},
		},
	}
	b := &mockPlugin{
		name:   "beta",
		detect: true,
		ops: []plugin.Operation{
			{
				Kind:    plugin.OpWrite,
				Path:    "b.md",
				Content: "b",
				Warnings: []plugin.Warning{
					{Source: ".agents/b.md", Message: "beta-warn", Severity: "error"},
				},
			},
		},
	}
	reg := plugin.NewRegistry()
	reg.Register(a)
	reg.Register(b)
	proj := newProject()

	_, warnings, err := Run(proj, reg, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings len = %d, want 3; got=%+v", len(warnings), warnings)
	}
	msgs := make(map[string]bool)
	for _, w := range warnings {
		msgs[w.Message] = true
	}
	for _, want := range []string{"alpha-warn-1", "alpha-warn-2", "beta-warn"} {
		if !msgs[want] {
			t.Fatalf("missing warning %q in %+v", want, warnings)
		}
	}
}
