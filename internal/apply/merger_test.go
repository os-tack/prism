package apply

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agents.dev/agents/internal/plugin"
)

func TestApply_OpMerge_NewFile(t *testing.T) {
	root := t.TempDir()
	var seen []byte
	var sawCall bool
	op := plugin.Operation{
		Kind: plugin.OpMerge,
		Path: "out/settings.json",
		Merger: func(existing []byte) (string, error) {
			sawCall = true
			seen = append([]byte(nil), existing...)
			return `{"k":"v"}`, nil
		},
	}
	changed, unchanged, err := Apply(root, []plugin.Operation{op}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	if !sawCall {
		t.Fatal("Merger was never invoked")
	}
	if len(seen) != 0 {
		t.Errorf("merger saw non-empty existing for missing file: %q", seen)
	}
	got, err := os.ReadFile(filepath.Join(root, "out/settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != `{"k":"v"}` {
		t.Errorf("content = %q, want %q", got, `{"k":"v"}`)
	}
}

func TestApply_OpMerge_IdempotentNoOp(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "x.json")
	if err := os.WriteFile(target, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	op := plugin.Operation{
		Kind: plugin.OpMerge,
		Path: "x.json",
		Merger: func(existing []byte) (string, error) {
			return string(existing), nil
		},
	}
	changed, unchanged, err := Apply(root, []plugin.Operation{op}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed != 0 || unchanged != 1 {
		t.Fatalf("counts changed=%d unchanged=%d, want 0/1", changed, unchanged)
	}
	got, _ := os.ReadFile(target)
	if string(got) != `{"a":1}` {
		t.Errorf("content mutated: %q", got)
	}
}

func TestApply_OpMerge_MergerError(t *testing.T) {
	root := t.TempDir()
	wantErr := errors.New("synthetic merger failure")
	op := plugin.Operation{
		Kind: plugin.OpMerge,
		Path: "fail.json",
		Merger: func(existing []byte) (string, error) {
			return "", wantErr
		},
	}
	_, _, err := Apply(root, []plugin.Operation{op}, false)
	if err == nil {
		t.Fatal("Apply: want error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wrapped %v", err, wantErr)
	}
}

func TestApply_OpMerge_NoMerger_LegacyFallback(t *testing.T) {
	root := t.TempDir()
	op := plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    "legacy.json",
		Content: `{"legacy":true}`,
	}
	changed, unchanged, err := Apply(root, []plugin.Operation{op}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed != 1 || unchanged != 0 {
		t.Fatalf("counts changed=%d unchanged=%d, want 1/0", changed, unchanged)
	}
	got, err := os.ReadFile(filepath.Join(root, "legacy.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != `{"legacy":true}` {
		t.Errorf("content = %q, want fallback content", got)
	}
}
