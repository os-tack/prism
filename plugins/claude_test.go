package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

func TestClaudePlan_SymlinkContextAndScope(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root context body",
		},
		Scopes: []*model.Scope{
			{
				Path: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/context.md",
					Body:       "billing scope body",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	if got, want := len(ops), 2; got != want {
		t.Fatalf("expected %d operations, got %d (%+v)", want, got, ops)
	}

	// Root op.
	root0 := ops[0]
	if root0.Path != "CLAUDE.md" {
		t.Errorf("ops[0].Path = %q, want %q", root0.Path, "CLAUDE.md")
	}
	if root0.Kind != plugin.OpSymlink {
		t.Errorf("ops[0].Kind = %q, want %q", root0.Kind, plugin.OpSymlink)
	}
	if root0.Mode != plugin.ModeSymlink {
		t.Errorf("ops[0].Mode = %q, want %q", root0.Mode, plugin.ModeSymlink)
	}
	if root0.Plugin != "claude" {
		t.Errorf("ops[0].Plugin = %q, want %q", root0.Plugin, "claude")
	}
	wantLink0, err := filepath.Rel(root, "/tmp/fake/.agents/context.md")
	if err != nil {
		t.Fatalf("filepath.Rel for root link: %v", err)
	}
	if root0.LinkTarget != wantLink0 {
		t.Errorf("ops[0].LinkTarget = %q, want %q", root0.LinkTarget, wantLink0)
	}
	wantSrc0, err := filepath.Rel(agentsDir, "/tmp/fake/.agents/context.md")
	if err != nil {
		t.Fatalf("filepath.Rel for root source: %v", err)
	}
	if len(root0.Sources) != 1 || root0.Sources[0] != wantSrc0 {
		t.Errorf("ops[0].Sources = %v, want [%q]", root0.Sources, wantSrc0)
	}

	// Scope op.
	sc := ops[1]
	wantScopePath := filepath.Join("src/billing", "CLAUDE.md")
	if sc.Path != wantScopePath {
		t.Errorf("ops[1].Path = %q, want %q", sc.Path, wantScopePath)
	}
	if sc.Kind != plugin.OpSymlink {
		t.Errorf("ops[1].Kind = %q, want %q", sc.Kind, plugin.OpSymlink)
	}
	if sc.Mode != plugin.ModeSymlink {
		t.Errorf("ops[1].Mode = %q, want %q", sc.Mode, plugin.ModeSymlink)
	}
	if sc.Plugin != "claude" {
		t.Errorf("ops[1].Plugin = %q, want %q", sc.Plugin, "claude")
	}
	scopeDir := filepath.Join(root, "src/billing")
	wantLink1, err := filepath.Rel(scopeDir, "/tmp/fake/.agents/src/billing/context.md")
	if err != nil {
		t.Fatalf("filepath.Rel for scope link: %v", err)
	}
	if sc.LinkTarget != wantLink1 {
		t.Errorf("ops[1].LinkTarget = %q, want %q", sc.LinkTarget, wantLink1)
	}
	wantSrc1, err := filepath.Rel(agentsDir, "/tmp/fake/.agents/src/billing/context.md")
	if err != nil {
		t.Fatalf("filepath.Rel for scope source: %v", err)
	}
	if len(sc.Sources) != 1 || sc.Sources[0] != wantSrc1 {
		t.Errorf("ops[1].Sources = %v, want [%q]", sc.Sources, wantSrc1)
	}
}

func TestClaude_Skills(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Skills: []*model.Skill{
			{
				Name: "format-go",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/skills/format-go/SKILL.md",
					Body:       "format go body",
				},
				Scripts: []string{
					"/tmp/fake/.agents/skills/format-go/scripts/run.sh",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (SKILL.md + script), got %d: %+v", len(ops), ops)
	}

	wantSkillPath := filepath.Join(".claude", "skills", "format-go", "SKILL.md")
	wantScriptPath := filepath.Join(".claude", "skills", "format-go", "scripts", "run.sh")

	foundSkill := false
	foundScript := false
	for _, op := range ops {
		switch op.Path {
		case wantSkillPath:
			foundSkill = true
			if op.Kind != plugin.OpSymlink {
				t.Errorf("skill op Kind = %q, want %q", op.Kind, plugin.OpSymlink)
			}
			if op.Plugin != "claude" {
				t.Errorf("skill op Plugin = %q, want %q", op.Plugin, "claude")
			}
		case wantScriptPath:
			foundScript = true
			if op.Kind != plugin.OpSymlink {
				t.Errorf("script op Kind = %q, want %q", op.Kind, plugin.OpSymlink)
			}
			if op.Plugin != "claude" {
				t.Errorf("script op Plugin = %q, want %q", op.Plugin, "claude")
			}
		}
	}
	if !foundSkill {
		t.Errorf("missing SKILL.md op at %q; got: %+v", wantSkillPath, ops)
	}
	if !foundScript {
		t.Errorf("missing script op at %q; got: %+v", wantScriptPath, ops)
	}
}

func TestClaude_Commands(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Commands: []*model.Command{
			{
				Name: "build",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/commands/build.md",
					Body:       "build body",
				},
			},
			{
				Name: "test",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/commands/test.md",
					Body:       "test body",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d: %+v", len(ops), ops)
	}

	wantPaths := map[string]bool{
		filepath.Join(".claude", "commands", "build.md"): false,
		filepath.Join(".claude", "commands", "test.md"):  false,
	}
	for _, op := range ops {
		if _, ok := wantPaths[op.Path]; ok {
			wantPaths[op.Path] = true
			if op.Kind != plugin.OpSymlink {
				t.Errorf("command op %q Kind = %q, want %q", op.Path, op.Kind, plugin.OpSymlink)
			}
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Errorf("missing command op at %q", path)
		}
	}
}

func TestClaude_Agents(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Agents: []*model.Agent{
			{
				Name: "reviewer",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/agents/reviewer.md",
					Body:       "reviewer body",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %+v", len(ops), ops)
	}
	wantPath := filepath.Join(".claude", "agents", "reviewer.md")
	if ops[0].Path != wantPath {
		t.Errorf("agent op Path = %q, want %q", ops[0].Path, wantPath)
	}
	if ops[0].Kind != plugin.OpSymlink {
		t.Errorf("agent op Kind = %q, want %q", ops[0].Kind, plugin.OpSymlink)
	}
}

func TestClaude_Hooks_SettingsMerge(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

	// Pre-existing settings.json with an unrelated user key.
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := `{"model": "claude-opus"}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing settings: %v", err)
	}

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Hooks: []*model.Hook{
			{
				Event:      "PostToolUse",
				Matcher:    "Edit",
				ScriptPath: "/abs/path/to/script.sh",
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	var settingsOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == filepath.Join(".claude", "settings.json") {
			settingsOp = &ops[i]
			break
		}
	}
	if settingsOp == nil {
		t.Fatalf("missing settings.json op; got: %+v", ops)
	}
	if settingsOp.Kind != plugin.OpMerge {
		t.Errorf("settings op Kind = %q, want %q", settingsOp.Kind, plugin.OpMerge)
	}
	if settingsOp.Mode != plugin.ModeWrite {
		t.Errorf("settings op Mode = %q, want %q", settingsOp.Mode, plugin.ModeWrite)
	}

	var merged map[string]any
	if err := json.Unmarshal([]byte(settingsOp.Content), &merged); err != nil {
		t.Fatalf("unmarshal settings: %v\ncontent: %s", err, settingsOp.Content)
	}

	// User key preserved.
	if got, want := merged["model"], "claude-opus"; got != want {
		t.Errorf("merged.model = %v, want %v", got, want)
	}

	// hooks key present and shaped correctly.
	hooks, ok := merged["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("merged.hooks missing or wrong type: %T %v", merged["hooks"], merged["hooks"])
	}
	postToolUse, ok := hooks["PostToolUse"].([]any)
	if !ok {
		t.Fatalf("hooks.PostToolUse missing or wrong type: %T %v", hooks["PostToolUse"], hooks["PostToolUse"])
	}
	if len(postToolUse) != 1 {
		t.Fatalf("PostToolUse groups = %d, want 1", len(postToolUse))
	}
	group, ok := postToolUse[0].(map[string]any)
	if !ok {
		t.Fatalf("PostToolUse[0] not an object: %T", postToolUse[0])
	}
	if got, want := group["matcher"], "Edit"; got != want {
		t.Errorf("matcher = %v, want %v", got, want)
	}
	inner, ok := group["hooks"].([]any)
	if !ok || len(inner) != 1 {
		t.Fatalf("inner hooks malformed: %v", group["hooks"])
	}
	entry, ok := inner[0].(map[string]any)
	if !ok {
		t.Fatalf("inner[0] not an object: %T", inner[0])
	}
	if got, want := entry["type"], "command"; got != want {
		t.Errorf("hook type = %v, want %v", got, want)
	}
	if got, want := entry["command"], "/abs/path/to/script.sh"; got != want {
		t.Errorf("hook command = %v, want %v", got, want)
	}
}

func TestClaude_Permissions_SettingsMerge(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Permissions: &model.Permissions{
			Allow: []string{"Bash(npm test)", "Read"},
			Deny:  []string{"Bash(rm -rf /)"},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	var settingsOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == filepath.Join(".claude", "settings.json") {
			settingsOp = &ops[i]
			break
		}
	}
	if settingsOp == nil {
		t.Fatalf("missing settings.json op; got: %+v", ops)
	}

	var merged map[string]any
	if err := json.Unmarshal([]byte(settingsOp.Content), &merged); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	perms, ok := merged["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions missing: %v", merged)
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) != 2 {
		t.Errorf("allow = %v, want 2 entries", perms["allow"])
	}
	deny, ok := perms["deny"].([]any)
	if !ok || len(deny) != 1 {
		t.Errorf("deny = %v, want 1 entry", perms["deny"])
	}
	if _, has := perms["ask"]; has {
		t.Errorf("ask should not be set when Permissions.Ask is empty; got %v", perms["ask"])
	}
}

func TestClaude_MCP(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

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

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".mcp.json" {
			mcpOp = &ops[i]
			break
		}
	}
	if mcpOp == nil {
		t.Fatalf("missing .mcp.json op; got: %+v", ops)
	}
	if mcpOp.Mode != plugin.ModeWrite {
		t.Errorf("mcp op Mode = %q, want %q", mcpOp.Mode, plugin.ModeWrite)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(mcpOp.Content), &doc); err != nil {
		t.Fatalf("unmarshal mcp: %v\ncontent: %s", err, mcpOp.Content)
	}
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing: %v", doc)
	}
	if _, ok := servers["filesystem"]; !ok {
		t.Errorf("filesystem server missing: %v", servers)
	}
	if _, ok := servers["remote"]; !ok {
		t.Errorf("remote server missing: %v", servers)
	}
	// Verify URL fallback worked for "remote".
	remote, _ := servers["remote"].(map[string]any)
	if got, want := remote["url"], "https://example.com/mcp"; got != want {
		t.Errorf("remote.url = %v, want %v", got, want)
	}
}
