package plugins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
		t.Fatalf("unmarshal settings: %v\ncontent: %s", err, mergedContent)
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

	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
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

// --- v0.5 scoped capability tests ------------------------------------------

func TestClaude_ScopedSkill(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Skills: []*model.Skill{
			{
				Name:      "audit-trail",
				ScopePath: "src/billing",
				Globs:     []string{"src/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/skills/audit-trail/SKILL.md",
					Body:       "audit trail body",
				},
				Scripts: []string{
					"/tmp/fake/.agents/src/billing/skills/audit-trail/scripts/run.sh",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantSkillPath := filepath.Join(".claude", "skills", "src-billing-audit-trail", "SKILL.md")
	wantScriptPath := filepath.Join(".claude", "skills", "src-billing-audit-trail", "scripts", "run.sh")
	wantSource := "src/billing/skills/audit-trail/SKILL.md"

	var skillOp *plugin.Operation
	var scriptOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantSkillPath {
			skillOp = &ops[i]
		}
		if ops[i].Path == wantScriptPath {
			scriptOp = &ops[i]
		}
	}
	if skillOp == nil {
		t.Fatalf("missing scoped skill op at %q; got: %+v", wantSkillPath, ops)
	}
	if scriptOp == nil {
		t.Fatalf("missing scoped skill script op at %q; got: %+v", wantScriptPath, ops)
	}
	if skillOp.Kind != plugin.OpSymlink {
		t.Errorf("scoped skill Kind = %q, want %q", skillOp.Kind, plugin.OpSymlink)
	}
	if len(skillOp.Sources) == 0 || skillOp.Sources[0] != wantSource {
		t.Errorf("scoped skill Sources = %v, want first entry = %q", skillOp.Sources, wantSource)
	}
	// LinkTarget should resolve to the scoped source under .agents/.
	wantLink, _ := filepath.Rel(filepath.Join(root, filepath.Dir(wantSkillPath)), proj.Skills[0].Document.SourcePath)
	if skillOp.LinkTarget != wantLink {
		t.Errorf("scoped skill LinkTarget = %q, want %q", skillOp.LinkTarget, wantLink)
	}
}

func TestClaude_ScopedCommand_Degrade(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Commands: []*model.Command{
			{
				Name:      "build",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/commands/build.md",
					Body:       "scoped build body",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPath := filepath.Join(".claude", "commands", "src-billing-build.md")

	var cmdOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantPath {
			cmdOp = &ops[i]
		}
	}
	if cmdOp == nil {
		t.Fatalf("missing scoped command op at %q; got: %+v", wantPath, ops)
	}
	if len(cmdOp.Warnings) == 0 {
		t.Fatalf("scoped command op missing degrade warning; got: %+v", cmdOp.Warnings)
	}
	foundDegrade := false
	for _, w := range cmdOp.Warnings {
		if w.Severity == "info" && strings.Contains(w.Message, "Claude commands are global") {
			foundDegrade = true
		}
	}
	if !foundDegrade {
		t.Errorf("scoped command op warnings missing degrade message; got: %+v", cmdOp.Warnings)
	}
}

func TestClaude_ScopedAgent_Degrade(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Agents: []*model.Agent{
			{
				Name:      "reviewer",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/agents/reviewer.md",
					Body:       "scoped reviewer body",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPath := filepath.Join(".claude", "agents", "src-billing-reviewer.md")

	var agOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantPath {
			agOp = &ops[i]
		}
	}
	if agOp == nil {
		t.Fatalf("missing scoped agent op at %q; got: %+v", wantPath, ops)
	}
	foundDegrade := false
	for _, w := range agOp.Warnings {
		if w.Severity == "info" && strings.Contains(w.Message, "Claude agents are global") {
			foundDegrade = true
		}
	}
	if !foundDegrade {
		t.Errorf("scoped agent op missing degrade warning; got: %+v", agOp.Warnings)
	}
}

func TestClaude_ScopedHook_Wrapper(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

	sourceScript := filepath.Join(agentsDir, "src", "billing", "hooks", "guard.sh")
	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Edit",
				ScriptPath: sourceScript,
				ScopePath:  "src/billing",
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantWrapperRel := filepath.Join(".claude", "hooks", "__scope-guard__", "src-billing-guard.sh")
	wantWrapperAbs := filepath.Join(root, wantWrapperRel)

	var wrapperOp *plugin.Operation
	var settingsOp *plugin.Operation
	for i := range ops {
		switch ops[i].Path {
		case wantWrapperRel:
			wrapperOp = &ops[i]
		case filepath.Join(".claude", "settings.json"):
			settingsOp = &ops[i]
		}
	}
	if wrapperOp == nil {
		t.Fatalf("missing wrapper script op at %q; got paths:\n%s", wantWrapperRel, opsPaths(ops))
	}
	if wrapperOp.Kind != plugin.OpWrite {
		t.Errorf("wrapper op Kind = %q, want %q", wrapperOp.Kind, plugin.OpWrite)
	}
	if wrapperOp.FileMode != 0o755 {
		t.Errorf("wrapper FileMode = %o, want 0755", wrapperOp.FileMode)
	}
	// Wrapper body must reference the scope path in the case statement.
	// Wrapper invokes `prism scope-guard` with the scope and source script.
	if !strings.Contains(wrapperOp.Content, "prism scope-guard") {
		t.Errorf("wrapper body missing scope-guard invocation; got:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "--scope 'src/billing'") {
		t.Errorf("wrapper body missing --scope flag; got:\n%s", wrapperOp.Content)
	}
	relScript, _ := filepath.Rel(root, sourceScript)
	if !strings.Contains(wrapperOp.Content, `--script "${PROJECT_DIR}"/'`+relScript+`'`) {
		t.Errorf("wrapper body missing --script ${PROJECT_DIR}/<rel> for source; got:\n%s", wrapperOp.Content)
	}

	// Settings hook command should point at the wrapper's absolute path,
	// not the raw source script.
	if settingsOp == nil {
		t.Fatalf("missing settings.json op")
	}
	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
		t.Fatalf("unmarshal settings: %v\ncontent: %s", err, mergedContent)
	}
	hooks, _ := merged["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("PreToolUse groups = %d, want 1; merged: %+v", len(pre), merged)
	}
	group, _ := pre[0].(map[string]any)
	inner, _ := group["hooks"].([]any)
	if len(inner) != 1 {
		t.Fatalf("inner hooks = %d, want 1", len(inner))
	}
	entry, _ := inner[0].(map[string]any)
	if got, want := entry["command"], wantWrapperAbs; got != want {
		t.Errorf("hook command = %v, want wrapper absolute path %v", got, want)
	}
}

func TestClaude_ScopedPermissions_Merge(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Permissions: &model.Permissions{
			Allow: []string{"Read"},
		},
		ScopedPermissions: []*model.Permissions{
			{
				ScopePath: "src/billing",
				Allow:     []string{"Bash(go test ./billing/...)"},
				Deny:      []string{"Bash(rm -rf /)"},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	var settingsOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == filepath.Join(".claude", "settings.json") {
			settingsOp = &ops[i]
		}
	}
	if settingsOp == nil {
		t.Fatalf("missing settings.json op; got: %+v", ops)
	}

	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	perms, _ := merged["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("permissions missing: %v", merged)
	}
	allow, _ := perms["allow"].([]any)
	if len(allow) != 2 {
		t.Errorf("allow = %v, want union of global + scoped (2 entries)", allow)
	}
	deny, _ := perms["deny"].([]any)
	if len(deny) != 1 {
		t.Errorf("deny = %v, want 1 (from scoped)", deny)
	}

	// Verify the degrade warning was emitted on the settings op.
	foundDegrade := false
	for _, w := range settingsOp.Warnings {
		if w.Severity == "info" && strings.Contains(w.Message, "src/billing/permissions.yaml") && strings.Contains(w.Message, "Claude Code has no per-scope permissions") {
			foundDegrade = true
		}
	}
	if !foundDegrade {
		t.Errorf("missing scoped-permissions degrade warning; got: %+v", settingsOp.Warnings)
	}
}

func TestClaude_ScopedMCP_Merge(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		MCP: []*model.MCPServer{
			{
				Name:      "billing-db",
				Command:   "uvx",
				Args:      []string{"billing-mcp"},
				ScopePath: "src/billing",
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".mcp.json" {
			mcpOp = &ops[i]
		}
	}
	if mcpOp == nil {
		t.Fatalf("missing .mcp.json op; got: %+v", ops)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(mcpOp.Content), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["billing-db"]; !ok {
		t.Errorf("scoped MCP server missing under mcpServers; got: %v", servers)
	}

	foundDegrade := false
	for _, w := range mcpOp.Warnings {
		if w.Severity == "info" && strings.Contains(w.Message, "billing-db") && strings.Contains(w.Message, "src/billing") {
			foundDegrade = true
		}
	}
	if !foundDegrade {
		t.Errorf("missing scoped-MCP degrade warning; got: %+v", mcpOp.Warnings)
	}
}

func TestClaude_NoCollision(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"

	// Same skill name "audit-trail" under two different scope paths must
	// not collide on disk; the scope-slug prefix differentiates them.
	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Skills: []*model.Skill{
			{
				Name:      "audit-trail",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/skills/audit-trail/SKILL.md",
					Body:       "billing audit",
				},
			},
			{
				Name:      "audit-trail",
				ScopePath: "src/orders",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/orders/skills/audit-trail/SKILL.md",
					Body:       "orders audit",
				},
			},
		},
	}

	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantBilling := filepath.Join(".claude", "skills", "src-billing-audit-trail", "SKILL.md")
	wantOrders := filepath.Join(".claude", "skills", "src-orders-audit-trail", "SKILL.md")

	var billingOp, ordersOp *plugin.Operation
	for i := range ops {
		switch ops[i].Path {
		case wantBilling:
			billingOp = &ops[i]
		case wantOrders:
			ordersOp = &ops[i]
		}
	}
	if billingOp == nil {
		t.Fatalf("missing billing-scoped skill at %q; ops:\n%s", wantBilling, opsPaths(ops))
	}
	if ordersOp == nil {
		t.Fatalf("missing orders-scoped skill at %q; ops:\n%s", wantOrders, opsPaths(ops))
	}
	if billingOp.Path == ordersOp.Path {
		t.Fatalf("collision: both skills landed at %q", billingOp.Path)
	}

	// Lockfile sources should reflect each scope's real source path.
	wantBillingSrc := "src/billing/skills/audit-trail/SKILL.md"
	wantOrdersSrc := "src/orders/skills/audit-trail/SKILL.md"
	if len(billingOp.Sources) == 0 || billingOp.Sources[0] != wantBillingSrc {
		t.Errorf("billing skill Sources = %v, want first entry %q", billingOp.Sources, wantBillingSrc)
	}
	if len(ordersOp.Sources) == 0 || ordersOp.Sources[0] != wantOrdersSrc {
		t.Errorf("orders skill Sources = %v, want first entry %q", ordersOp.Sources, wantOrdersSrc)
	}
}

// mergeContent invokes op.Merger with the current contents of root/op.Path
// (or nil if absent), returning the merged bytes. Falls back to op.Content
// when Merger is unset. Test-only helper for OpMerge inspection.
func mergeContent(t *testing.T, root string, op *plugin.Operation) string {
	t.Helper()
	abs := filepath.Join(root, op.Path)
	existing, err := os.ReadFile(abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read existing %s: %v", abs, err)
	}
	if err != nil {
		existing = nil
	}
	if op.Merger == nil {
		return op.Content
	}
	out, mErr := op.Merger(existing)
	if mErr != nil {
		t.Fatalf("merger %s: %v", abs, mErr)
	}
	return out
}
