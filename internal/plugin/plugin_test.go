package plugin

import (
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

// stubPlugin is a no-op plugin used by registry tests.
type stubPlugin struct{ name string }

func (p *stubPlugin) Name() string               { return p.name }
func (p *stubPlugin) Detect(root string) bool    { return false }
func (p *stubPlugin) Capabilities() Capabilities { return Capabilities{} }
func (p *stubPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]Operation, error) {
	return nil, nil
}

var _ Plugin = (*stubPlugin)(nil)

// TestRegister_FirstSucceeds locks the happy path: registering a plugin with a
// fresh name returns nil.
func TestRegister_FirstSucceeds(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubPlugin{name: "first"}); err != nil {
		t.Fatalf("first Register: unexpected err: %v", err)
	}
	if r.Get("first") == nil {
		t.Fatalf("Get(%q) returned nil after Register", "first")
	}
}

// TestRegister_DuplicateReturnsError is the v0.7 regression test: prior to
// the migration off panic-on-duplicate (see internal/plugin/plugin.go), the
// second Register call would panic. Now it must return a non-nil error and
// leave the existing registration intact.
func TestRegister_DuplicateReturnsError(t *testing.T) {
	r := NewRegistry()
	first := &stubPlugin{name: "dup"}
	if err := r.Register(first); err != nil {
		t.Fatalf("first Register: unexpected err: %v", err)
	}

	second := &stubPlugin{name: "dup"}
	err := r.Register(second)
	if err == nil {
		t.Fatal("duplicate Register: want non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "dup") {
		t.Errorf("error message %q should mention the duplicate name", err.Error())
	}
	// The first registration must remain — Register must not overwrite on
	// conflict (matches importer.Registry.Register's contract).
	if r.Get("dup") != Plugin(first) {
		t.Error("duplicate Register must not overwrite the existing entry")
	}
}

// TestRegister_DifferentNamesCoexist locks that distinct names register
// independently and both surface through All/Names.
func TestRegister_DifferentNamesCoexist(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubPlugin{name: "a"}); err != nil {
		t.Fatalf("Register(a): %v", err)
	}
	if err := r.Register(&stubPlugin{name: "b"}); err != nil {
		t.Fatalf("Register(b): %v", err)
	}
	if got := len(r.All()); got != 2 {
		t.Fatalf("All() len = %d, want 2", got)
	}
	if got := len(r.Names()); got != 2 {
		t.Fatalf("Names() len = %d, want 2", got)
	}
}
