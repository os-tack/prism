package importer

import (
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

// stubImporter is a minimal Importer for registry tests.
type stubImporter struct{ name string }

func (s *stubImporter) Name() string       { return s.name }
func (s *stubImporter) Detect(string) bool { return false }
func (s *stubImporter) Import(string) (*model.Project, []Warning, error) {
	return nil, nil, ErrSourceNotPresent
}

// TestRegistry_Register verifies the v0.6 contract: first registration
// succeeds, a duplicate returns a non-nil error (replacing the v0.5
// panic) so tests can assert the dup path without a `recover()` dance.
func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(&stubImporter{name: "first"}); err != nil {
		t.Fatalf("first Register: unexpected err %v", err)
	}

	err := r.Register(&stubImporter{name: "first"})
	if err == nil {
		t.Fatal("duplicate Register returned nil; want non-nil error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate Register err = %q, want substring \"already registered\"", err.Error())
	}
	if !strings.Contains(err.Error(), "first") {
		t.Errorf("duplicate Register err = %q, want substring \"first\"", err.Error())
	}

	// A different name still succeeds after a dup.
	if err := r.Register(&stubImporter{name: "second"}); err != nil {
		t.Errorf("Register(second) after dup: unexpected err %v", err)
	}

	got := r.Names()
	if len(got) != 2 {
		t.Errorf("Names = %v, want length 2", got)
	}
}
