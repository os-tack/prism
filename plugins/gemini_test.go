package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

func TestGemini_RootContext(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root context body",
		},
	}

	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %+v", len(ops), ops)
	}

	op := ops[0]
	if op.Path != "GEMINI.md" {
		t.Errorf("op.Path = %q, want %q", op.Path, "GEMINI.md")
	}
	if op.Kind != plugin.OpSymlink {
		t.Errorf("op.Kind = %q, want %q", op.Kind, plugin.OpSymlink)
	}
	if op.Mode != plugin.ModeSymlink {
		t.Errorf("op.Mode = %q, want %q", op.Mode, plugin.ModeSymlink)
	}
	if op.Plugin != "gemini" {
		t.Errorf("op.Plugin = %q, want %q", op.Plugin, "gemini")
	}
	wantLink, err := filepath.Rel(root, "/tmp/fake/.agents/context.md")
	if err != nil {
		t.Fatalf("filepath.Rel for root link: %v", err)
	}
	if op.LinkTarget != wantLink {
		t.Errorf("op.LinkTarget = %q, want %q", op.LinkTarget, wantLink)
	}
	wantSrc, err := filepath.Rel(agentsDir, "/tmp/fake/.agents/context.md")
	if err != nil {
		t.Fatalf("filepath.Rel for root source: %v", err)
	}
	if len(op.Sources) != 1 || op.Sources[0] != wantSrc {
		t.Errorf("op.Sources = %v, want [%q]", op.Sources, wantSrc)
	}
}

func TestGemini_ScopeCascade(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Scopes: []*model.Scope{
			{
				Path: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/context.md",
					Body:       "billing scope",
				},
			},
		},
	}

	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (root + scope), got %d: %+v", len(ops), ops)
	}

	// Root op first.
	if ops[0].Path != "GEMINI.md" {
		t.Errorf("ops[0].Path = %q, want %q", ops[0].Path, "GEMINI.md")
	}

	// Scope op.
	sc := ops[1]
	wantScopePath := filepath.Join("src/billing", "GEMINI.md")
	if sc.Path != wantScopePath {
		t.Errorf("ops[1].Path = %q, want %q", sc.Path, wantScopePath)
	}
	if sc.Kind != plugin.OpSymlink {
		t.Errorf("ops[1].Kind = %q, want %q", sc.Kind, plugin.OpSymlink)
	}
	if sc.Plugin != "gemini" {
		t.Errorf("ops[1].Plugin = %q, want %q", sc.Plugin, "gemini")
	}
	scopeDir := filepath.Join(root, "src/billing")
	wantLink, err := filepath.Rel(scopeDir, "/tmp/fake/.agents/src/billing/context.md")
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	if sc.LinkTarget != wantLink {
		t.Errorf("ops[1].LinkTarget = %q, want %q (must be relative from scope dir)", sc.LinkTarget, wantLink)
	}
}

func TestGemini_MCP(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

	// Pre-existing .gemini/settings.json with an unrelated user key.
	geminiDir := filepath.Join(root, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := `{"theme": "default"}`
	if err := os.WriteFile(filepath.Join(geminiDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing settings: %v", err)
	}

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		MCP: []*model.MCPServer{
			{
				Name:    "filesystem",
				Command: "npx",
				Args:    []string{"@modelcontextprotocol/server-filesystem", "/tmp"},
			},
			{
				Name: "remote",
				URL:  "https://example.com/mcp",
			},
		},
	}

	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	wantPath := filepath.Join(".gemini", "settings.json")
	var settingsOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantPath {
			settingsOp = &ops[i]
			break
		}
	}
	if settingsOp == nil {
		t.Fatalf("missing .gemini/settings.json op; got: %+v", ops)
	}
	if settingsOp.Mode != plugin.ModeWrite {
		t.Errorf("settings op Mode = %q, want %q", settingsOp.Mode, plugin.ModeWrite)
	}
	if settingsOp.Kind != plugin.OpMerge {
		t.Errorf("settings op Kind = %q, want %q", settingsOp.Kind, plugin.OpMerge)
	}

	var merged map[string]any
	if err := json.Unmarshal([]byte(settingsOp.Content), &merged); err != nil {
		t.Fatalf("unmarshal settings: %v\ncontent: %s", err, settingsOp.Content)
	}

	// User key preserved.
	if got, want := merged["theme"], "default"; got != want {
		t.Errorf("merged.theme = %v, want %v", got, want)
	}

	servers, ok := merged["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing: %v", merged)
	}
	if _, ok := servers["filesystem"]; !ok {
		t.Errorf("filesystem server missing: %v", servers)
	}
	if _, ok := servers["remote"]; !ok {
		t.Errorf("remote server missing: %v", servers)
	}
	// Verify URL came through for "remote".
	remote, _ := servers["remote"].(map[string]any)
	if got, want := remote["url"], "https://example.com/mcp"; got != want {
		t.Errorf("remote.url = %v, want %v", got, want)
	}
	// Verify command/args came through for "filesystem".
	fs, _ := servers["filesystem"].(map[string]any)
	if got, want := fs["command"], "npx"; got != want {
		t.Errorf("filesystem.command = %v, want %v", got, want)
	}
}

func TestGemini_SkillsEmitWarnings(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "x",
		},
		Skills: []*model.Skill{
			{
				Name: "format-go",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/skills/format-go/SKILL.md",
					Body:       "format go",
				},
			},
		},
	}

	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	// No skill file should be emitted.
	for _, op := range ops {
		if strings.Contains(op.Path, "format-go") || strings.Contains(op.Path, "SKILL") {
			t.Errorf("unexpected skill projection op %q (skills unsupported)", op.Path)
		}
	}

	// Warning should be attached to one of the existing ops.
	var found *plugin.Warning
	for i := range ops {
		for j := range ops[i].Warnings {
			w := ops[i].Warnings[j]
			if strings.Contains(w.Message, "format-go") && strings.Contains(w.Message, "Gemini") {
				found = &ops[i].Warnings[j]
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a warning mentioning Gemini and format-go; got ops: %+v", ops)
	}
}

func TestGemini_UnknownModeErrors(t *testing.T) {
	p := NewGemini()
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "x",
		},
	}
	_, err := p.Plan(proj, model.TargetOption{Mode: "writes"})
	if err == nil {
		t.Fatalf("expected error on unknown mode, got nil")
	}
	if !strings.Contains(err.Error(), "writes") {
		t.Fatalf("error should mention the bad value %q, got: %v", "writes", err)
	}
}

func TestGemini_DetectMarkers(t *testing.T) {
	p := NewGemini()

	// Bare dir: no markers → false.
	bare := t.TempDir()
	if p.Detect(bare) {
		t.Errorf("Detect(empty dir) = true, want false")
	}

	// .gemini/ dir → true.
	dotDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dotDir, ".gemini"), 0o755); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	if !p.Detect(dotDir) {
		t.Errorf("Detect(dir with .gemini/) = false, want true")
	}

	// GEMINI.md file → true.
	mdDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mdDir, "GEMINI.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write GEMINI.md: %v", err)
	}
	if !p.Detect(mdDir) {
		t.Errorf("Detect(dir with GEMINI.md) = false, want true")
	}
}
