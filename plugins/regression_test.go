package plugins

import (
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

// Regression for review bug #3: unknown Mode value must error, not silently
// fall back to symlink.
func TestClaude_UnknownModeErrors(t *testing.T) {
	p := NewClaude()
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "x",
		},
	}
	_, err := p.Plan(proj, model.TargetOption{Mode: "writes"}) // typo
	if err == nil {
		t.Fatalf("expected error on unknown mode, got nil")
	}
	if !strings.Contains(err.Error(), "writes") {
		t.Fatalf("error should mention the bad value %q, got: %v", "writes", err)
	}
}

// Confirm the canonical values still work.
func TestClaude_KnownModesOK(t *testing.T) {
	p := NewClaude()
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "x",
		},
	}
	for _, m := range []string{"", "symlink", "write"} {
		if _, err := p.Plan(proj, model.TargetOption{Mode: m}); err != nil {
			t.Fatalf("Plan(mode=%q) returned err: %v", m, err)
		}
	}
}
