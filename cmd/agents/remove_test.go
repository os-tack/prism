package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/registry"
)

// TestRemove_ReturnsErrorNoExit verifies the v0.6 behavior: drift
// surfaces as a returned error (a *registry.RemoveDriftError) instead
// of calling os.Exit. The legacy v0.5 code called os.Exit(1) from
// inside RunE, which bypassed any cleanup defers in main() / Execute().
//
// We can't easily catch os.Exit in a test, so the contract we lock in
// is positive: the returned error is non-nil, and is the expected drift
// error type. If a future regression reintroduces os.Exit, the test
// process itself would die — which is a much louder failure than the
// silent skip you'd get from a generic err==nil check.
func TestRemove_ReturnsErrorNoExit(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir .agents: %v", err)
	}

	// Build a "package" source dir and install it.
	pkg := t.TempDir()
	mustWriteFile(t, filepath.Join(pkg, "package.yaml"),
		"name: drift-target\nschema: 1\ncontents:\n  - skills/drift-target/\n")
	mustWriteFile(t, filepath.Join(pkg, "skills", "drift-target", "SKILL.md"),
		"---\nname: drift-target\n---\n# body")

	if _, err := registry.Install(project, pkg, registry.InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Drift the file.
	skillPath := filepath.Join(project, ".agents", "skills", "drift-target", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("HAND EDITED"), 0o644); err != nil {
		t.Fatalf("drift: %v", err)
	}

	// Build the cobra command and exercise RunE.
	state := &cliState{root: project, registry: plugin.NewRegistry()}
	cmd := newRemoveCmd(state)
	cmd.SetArgs([]string{"drift-target"})
	// Capture output so it doesn't pollute test logs.
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("cmd.Execute() returned nil on drift; want non-nil (the cobra error pipeline must surface RemoveDriftError)")
	}
	var drift *registry.RemoveDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("returned err = %T (%v), want *registry.RemoveDriftError", err, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
