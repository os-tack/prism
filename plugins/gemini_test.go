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

	if ops[0].Path != "GEMINI.md" {
		t.Errorf("ops[0].Path = %q, want %q", ops[0].Path, "GEMINI.md")
	}

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

	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
		t.Fatalf("unmarshal settings: %v\ncontent: %s", err, mergedContent)
	}

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
	remote, _ := servers["remote"].(map[string]any)
	if got, want := remote["url"], "https://example.com/mcp"; got != want {
		t.Errorf("remote.url = %v, want %v", got, want)
	}
	fs, _ := servers["filesystem"].(map[string]any)
	if got, want := fs["command"], "npx"; got != want {
		t.Errorf("filesystem.command = %v, want %v", got, want)
	}
}

// TestGemini_UnknownModeErrors verifies the mode validator.
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

	bare := t.TempDir()
	if p.Detect(bare) {
		t.Errorf("Detect(empty dir) = true, want false")
	}

	dotDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dotDir, ".gemini"), 0o755); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	if !p.Detect(dotDir) {
		t.Errorf("Detect(dir with .gemini/) = false, want true")
	}

	mdDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mdDir, "GEMINI.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write GEMINI.md: %v", err)
	}
	if !p.Detect(mdDir) {
		t.Errorf("Detect(dir with GEMINI.md) = false, want true")
	}
}

// TestGemini_Agent_Projects verifies an Agent emits a .gemini/agents/<name>.md
// file with YAML frontmatter containing name + description and the document
// body underneath.
func TestGemini_Agent_Projects(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"
	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Agents: []*model.Agent{
			{
				Name:        "reviewer",
				Description: "reviews PRs",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/agents/reviewer.md",
					Body:       "You are a careful PR reviewer.\n",
				},
			},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPath := filepath.Join(".gemini", "agents", "reviewer.md")
	var agentOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantPath {
			agentOp = &ops[i]
			break
		}
	}
	if agentOp == nil {
		t.Fatalf("missing agent op at %q; have: %v", wantPath, opsPaths(ops))
	}
	if agentOp.Kind != plugin.OpWrite {
		t.Errorf("agent Kind = %v, want OpWrite", agentOp.Kind)
	}
	if !strings.HasPrefix(agentOp.Content, "---\n") {
		t.Errorf("agent content missing YAML opener:\n%s", agentOp.Content)
	}
	if !strings.Contains(agentOp.Content, `name: "reviewer"`) {
		t.Errorf("agent content missing name field:\n%s", agentOp.Content)
	}
	if !strings.Contains(agentOp.Content, `description: "reviews PRs"`) {
		t.Errorf("agent content missing description field:\n%s", agentOp.Content)
	}
	if !strings.Contains(agentOp.Content, "You are a careful PR reviewer.") {
		t.Errorf("agent content missing body:\n%s", agentOp.Content)
	}
	// Frontmatter must close before body begins.
	closer := strings.Index(agentOp.Content, "\n---\n")
	body := strings.Index(agentOp.Content, "You are a careful")
	if closer < 0 || body < 0 || closer > body {
		t.Errorf("frontmatter closer must precede body; content:\n%s", agentOp.Content)
	}
}

// TestGemini_Command_Projects verifies a Command emits a TOML file under
// .gemini/commands/<name>.toml with description + prompt fields.
func TestGemini_Command_Projects(t *testing.T) {
	root := "/tmp/fake"
	agentsDir := "/tmp/fake/.agents"
	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Commands: []*model.Command{
			{
				Name:        "deploy",
				Description: "Deploy the current branch",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/commands/deploy.md",
					Body:       "Run the deploy pipeline for {{branch}}.\n",
				},
			},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPath := filepath.Join(".gemini", "commands", "deploy.toml")
	var cmdOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantPath {
			cmdOp = &ops[i]
			break
		}
	}
	if cmdOp == nil {
		t.Fatalf("missing command op at %q; have: %v", wantPath, opsPaths(ops))
	}
	if cmdOp.Kind != plugin.OpWrite {
		t.Errorf("cmd Kind = %v, want OpWrite", cmdOp.Kind)
	}
	if !strings.Contains(cmdOp.Content, `description = "Deploy the current branch"`) {
		t.Errorf("cmd content missing description:\n%s", cmdOp.Content)
	}
	if !strings.Contains(cmdOp.Content, `prompt = """`) {
		t.Errorf("cmd content missing prompt opener:\n%s", cmdOp.Content)
	}
	if !strings.Contains(cmdOp.Content, "Run the deploy pipeline for {{branch}}.") {
		t.Errorf("cmd content missing body:\n%s", cmdOp.Content)
	}
	if !strings.HasSuffix(strings.TrimRight(cmdOp.Content, "\n"), `"""`) {
		t.Errorf("cmd content missing prompt closer:\n%s", cmdOp.Content)
	}
}

// TestGemini_Command_TripleQuoteEscape verifies bodies containing literal
// triple-quotes are escaped so the TOML string doesn't terminate early.
func TestGemini_Command_TripleQuoteEscape(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Commands: []*model.Command{
			{
				Name: "weird",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/commands/weird.md",
					Body:       `here is """a triple quote""" inline`,
				},
			},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var cmdOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == filepath.Join(".gemini", "commands", "weird.toml") {
			cmdOp = &ops[i]
		}
	}
	if cmdOp == nil {
		t.Fatalf("missing weird.toml op")
	}
	// Bare """ inside the body must be escaped (no three-in-a-row remain
	// other than the opener / closer fence).
	body := strings.TrimPrefix(cmdOp.Content, `prompt = """`+"\n")
	// strip trailing closer
	idx := strings.LastIndex(body, `"""`)
	if idx < 0 {
		t.Fatalf("no closer in content:\n%s", cmdOp.Content)
	}
	inner := body[:idx]
	if strings.Contains(inner, `"""`) {
		t.Errorf("triple-quote not escaped in inner content:\n%s", inner)
	}
}

// TestGemini_Hooks_SettingsMerge verifies a global hook is written into
// .gemini/settings.json's hooks block under the mapped Gemini event name
// (PreToolUse → BeforeTool) and that pre-existing user keys survive.
func TestGemini_Hooks_SettingsMerge(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")

	geminiDir := filepath.Join(root, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := `{"theme": "dark", "selectedAuthType": "oauth"}`
	if err := os.WriteFile(filepath.Join(geminiDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: filepath.Join(agentsDir, "hooks", "audit.sh"),
			},
			{
				Event:      "PostToolUse",
				Matcher:    "Edit",
				ScriptPath: filepath.Join(agentsDir, "hooks", "verify.sh"),
			},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	var settingsOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == filepath.Join(".gemini", "settings.json") {
			settingsOp = &ops[i]
			break
		}
	}
	if settingsOp == nil {
		t.Fatalf("missing settings.json op")
	}
	if settingsOp.Kind != plugin.OpMerge {
		t.Errorf("settings op Kind = %v, want OpMerge", settingsOp.Kind)
	}

	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
		t.Fatalf("unmarshal: %v\ncontent: %s", err, mergedContent)
	}

	// User keys preserved.
	if got, want := merged["theme"], "dark"; got != want {
		t.Errorf("merged.theme = %v, want %v", got, want)
	}
	if got, want := merged["selectedAuthType"], "oauth"; got != want {
		t.Errorf("merged.selectedAuthType = %v, want %v", got, want)
	}

	hooks, ok := merged["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("merged.hooks missing: %T %v", merged["hooks"], merged["hooks"])
	}
	// PreToolUse → BeforeTool.
	beforeTool, ok := hooks["BeforeTool"].([]any)
	if !ok {
		t.Fatalf("hooks.BeforeTool missing or wrong type: %T %v", hooks["BeforeTool"], hooks["BeforeTool"])
	}
	if len(beforeTool) != 1 {
		t.Fatalf("BeforeTool groups = %d, want 1", len(beforeTool))
	}
	group, _ := beforeTool[0].(map[string]any)
	if got, want := group["matcher"], "Bash"; got != want {
		t.Errorf("BeforeTool matcher = %v, want %v", got, want)
	}
	inner, _ := group["hooks"].([]any)
	if len(inner) != 1 {
		t.Fatalf("inner BeforeTool hooks = %d, want 1", len(inner))
	}
	entry, _ := inner[0].(map[string]any)
	if got, want := entry["type"], "command"; got != want {
		t.Errorf("hook type = %v, want %v", got, want)
	}
	cmdStr, _ := entry["command"].(string)
	if !strings.HasPrefix(cmdStr, "${PROJECT_DIR}/") {
		t.Errorf("hook command should use ${PROJECT_DIR} for portability: %q", cmdStr)
	}
	if !strings.HasSuffix(cmdStr, "audit.sh") {
		t.Errorf("hook command should end in audit.sh: %q", cmdStr)
	}

	// PostToolUse → AfterTool.
	afterTool, ok := hooks["AfterTool"].([]any)
	if !ok {
		t.Fatalf("hooks.AfterTool missing: %v", hooks)
	}
	if len(afterTool) != 1 {
		t.Errorf("AfterTool groups = %d, want 1", len(afterTool))
	}
}

// TestGemini_Hooks_EventPassthrough verifies that a hook with a
// Gemini-native event name (SessionStart) passes through unchanged.
func TestGemini_Hooks_EventPassthrough(t *testing.T) {
	root := t.TempDir()
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Hooks: []*model.Hook{
			{
				Event:      "SessionStart",
				ScriptPath: filepath.Join(root, ".agents", "hooks", "boot.sh"),
			},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var settingsOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == filepath.Join(".gemini", "settings.json") {
			settingsOp = &ops[i]
		}
	}
	if settingsOp == nil {
		t.Fatalf("missing settings.json op")
	}
	mergedContent := mergeContent(t, root, settingsOp)
	if !strings.Contains(mergedContent, `"SessionStart"`) {
		t.Errorf("SessionStart event should pass through unchanged:\n%s", mergedContent)
	}
}

// TestGemini_ScopedHook_WrapperEmitted verifies that a scoped hook
// produces a wrapper script under .gemini/hooks/__scope-guard__/ that
// exec's `prism scope-guard`, and that settings.json's hook command
// points at the wrapper via ${PROJECT_DIR}.
func TestGemini_ScopedHook_WrapperEmitted(t *testing.T) {
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
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantWrapperRel := filepath.Join(".gemini", "hooks", "__scope-guard__", "src-billing-BeforeTool-guard.sh")
	var wrapperOp *plugin.Operation
	var settingsOp *plugin.Operation
	for i := range ops {
		switch ops[i].Path {
		case wantWrapperRel:
			wrapperOp = &ops[i]
		case filepath.Join(".gemini", "settings.json"):
			settingsOp = &ops[i]
		}
	}
	if wrapperOp == nil {
		t.Fatalf("missing wrapper at %q; have: %v", wantWrapperRel, opsPaths(ops))
	}
	if wrapperOp.FileMode != 0o755 {
		t.Errorf("wrapper FileMode = %o, want 0755", wrapperOp.FileMode)
	}
	if !strings.Contains(wrapperOp.Content, "prism scope-guard") {
		t.Errorf("wrapper missing prism scope-guard:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "--scope 'src/billing'") {
		t.Errorf("wrapper missing --scope:\n%s", wrapperOp.Content)
	}
	relScript, _ := filepath.Rel(root, sourceScript)
	if !strings.Contains(wrapperOp.Content, `--script "${PROJECT_DIR}"/'`+relScript+`'`) {
		t.Errorf("wrapper missing --script with project-dir form:\n%s", wrapperOp.Content)
	}

	// settings.json hook command must reference the wrapper via ${PROJECT_DIR}.
	if settingsOp == nil {
		t.Fatalf("missing settings.json op")
	}
	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks, _ := merged["hooks"].(map[string]any)
	beforeTool, _ := hooks["BeforeTool"].([]any)
	if len(beforeTool) != 1 {
		t.Fatalf("BeforeTool groups = %d, want 1", len(beforeTool))
	}
	group, _ := beforeTool[0].(map[string]any)
	inner, _ := group["hooks"].([]any)
	if len(inner) != 1 {
		t.Fatalf("inner = %d, want 1", len(inner))
	}
	entry, _ := inner[0].(map[string]any)
	wantCmd := "${PROJECT_DIR}/" + filepath.ToSlash(wantWrapperRel)
	if got, _ := entry["command"].(string); got != wantCmd {
		t.Errorf("hook command = %q, want %q", got, wantCmd)
	}
}

// TestGemini_SkillsProjectedAsAgents verifies skills become .gemini/agents
// entries with the trigger embedded in the description, and an info warning
// flags the degradation.
func TestGemini_SkillsProjectedAsAgents(t *testing.T) {
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
				Name:    "format-go",
				Trigger: "after editing *.go",
				Globs:   []string{"*.go"},
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/skills/format-go/SKILL.md",
					Body:       "Run gofmt.",
				},
			},
		},
	}

	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPath := filepath.Join(".gemini", "agents", "format-go.md")
	var skOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantPath {
			skOp = &ops[i]
		}
	}
	if skOp == nil {
		t.Fatalf("skill not projected as agent at %q; have: %v", wantPath, opsPaths(ops))
	}
	if !strings.Contains(skOp.Content, "trigger: after editing *.go") {
		t.Errorf("trigger missing from description:\n%s", skOp.Content)
	}
	if !strings.Contains(skOp.Content, "globs: *.go") {
		t.Errorf("globs missing from description:\n%s", skOp.Content)
	}
	if !strings.Contains(skOp.Content, "Run gofmt.") {
		t.Errorf("skill body missing:\n%s", skOp.Content)
	}
	// Degradation warning attached.
	found := false
	for _, w := range skOp.Warnings {
		if strings.Contains(w.Message, "format-go") && strings.Contains(w.Message, "Gemini agent") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected degradation warning for skill format-go; got: %+v", skOp.Warnings)
	}
}

// TestGemini_ScopedSkill: a scoped Skill projects to a prefixed file
// under .gemini/agents/ and emits both a "projected as agent" warning
// and a per-scope info warning.
func TestGemini_ScopedSkill(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Skills: []*model.Skill{
			{
				Name:      "audit-trail",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/skills/audit-trail/SKILL.md",
					Body:       "audit",
				},
			},
		},
	}

	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPath := filepath.Join(".gemini", "agents", "src-billing-audit-trail.md")
	var skOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == wantPath {
			skOp = &ops[i]
		}
	}
	if skOp == nil {
		t.Fatalf("scoped skill not projected at %q; have: %v", wantPath, opsPaths(ops))
	}
	foundScope := false
	for _, w := range skOp.Warnings {
		if strings.Contains(w.Message, "src/billing") && strings.Contains(w.Message, "audit-trail") {
			foundScope = true
		}
	}
	if !foundScope {
		t.Errorf("expected per-scope warning for audit-trail; got: %+v", skOp.Warnings)
	}
}

// TestGemini_ScopedSkillCollision: two scoped skills sharing a Name but
// at different ScopePaths both project successfully with no path clash.
func TestGemini_ScopedSkillCollision(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Skills: []*model.Skill{
			{
				Name:      "audit-trail",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/skills/audit-trail/SKILL.md",
				},
			},
			{
				Name:      "audit-trail",
				ScopePath: "src/payments",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/payments/skills/audit-trail/SKILL.md",
				},
			},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantBilling := filepath.Join(".gemini", "agents", "src-billing-audit-trail.md")
	wantPayments := filepath.Join(".gemini", "agents", "src-payments-audit-trail.md")
	have := map[string]bool{}
	for _, op := range ops {
		have[op.Path] = true
	}
	if !have[wantBilling] {
		t.Errorf("missing %s; have: %v", wantBilling, opsPaths(ops))
	}
	if !have[wantPayments] {
		t.Errorf("missing %s; have: %v", wantPayments, opsPaths(ops))
	}
}

// TestGemini_ScopedCommandAgentHookMCP: verifies scoped command and agent
// project with scope-prefixed filenames; scoped hooks materialize wrappers;
// scoped MCP still emits its degradation warning.
func TestGemini_ScopedCommandAgentHookMCP(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Commands: []*model.Command{
			{
				Name:      "deploy",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/commands/deploy.md",
					Body:       "deploy body",
				},
			},
		},
		Agents: []*model.Agent{
			{
				Name:      "reviewer",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/agents/reviewer.md",
					Body:       "reviewer body",
				},
			},
		},
		Hooks: []*model.Hook{
			{
				Event:      "PostToolUse",
				Matcher:    "Edit",
				ScopePath:  "src/billing",
				ScriptPath: "/tmp/fake/.agents/src/billing/hooks/verify.sh",
			},
		},
		MCP: []*model.MCPServer{
			{Name: "linear", Command: "npx", ScopePath: "src/billing"},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	have := map[string]bool{}
	for _, op := range ops {
		have[op.Path] = true
	}
	wantCmd := filepath.Join(".gemini", "commands", "src-billing-deploy.toml")
	wantAgent := filepath.Join(".gemini", "agents", "src-billing-reviewer.md")
	wantWrapper := filepath.Join(".gemini", "hooks", "__scope-guard__", "src-billing-AfterTool-verify.sh")
	if !have[wantCmd] {
		t.Errorf("missing scoped command at %q; have: %v", wantCmd, opsPaths(ops))
	}
	if !have[wantAgent] {
		t.Errorf("missing scoped agent at %q; have: %v", wantAgent, opsPaths(ops))
	}
	if !have[wantWrapper] {
		t.Errorf("missing scoped hook wrapper at %q; have: %v", wantWrapper, opsPaths(ops))
	}
	// Scoped MCP server still degraded — surfaces an info warning.
	foundMCP := false
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "src/billing") && strings.Contains(w.Message, `scoped MCP server "linear"`) {
				foundMCP = true
			}
		}
	}
	if !foundMCP {
		t.Errorf("missing scoped MCP degradation warning")
	}
}

// TestGemini_PermsGuard_GlobalNoHooks verifies that a global Permissions
// block with no hooks projects a sidecar policy + a bare gate wrapper
// under .gemini/hooks/__perms-guard__/.
func TestGemini_PermsGuard_GlobalNoHooks(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Permissions: &model.Permissions{
			Allow: []string{"bash:ls *"},
			Deny:  []string{"bash:rm -rf *"},
			Ask:   []string{"bash:git *"},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	paths := map[string]plugin.Operation{}
	for _, op := range ops {
		paths[op.Path] = op
	}
	policyPath := filepath.Join(".gemini", "hooks", "__perms-guard__", "policy.json")
	gatePath := filepath.Join(".gemini", "hooks", "__perms-guard__", "global-gate.sh")
	policyOp, ok := paths[policyPath]
	if !ok {
		t.Fatalf("missing policy op at %q; have: %v", policyPath, opsPaths(ops))
	}
	if policyOp.Kind != plugin.OpWrite {
		t.Errorf("policy Kind = %v, want OpWrite", policyOp.Kind)
	}
	if !strings.Contains(policyOp.Content, `"bash:ls *"`) {
		t.Errorf("policy content missing Allow rule:\n%s", policyOp.Content)
	}
	if !strings.Contains(policyOp.Content, `"bash:rm -rf *"`) {
		t.Errorf("policy content missing Deny rule:\n%s", policyOp.Content)
	}
	gateOp, ok := paths[gatePath]
	if !ok {
		t.Fatalf("missing gate wrapper at %q; have: %v", gatePath, opsPaths(ops))
	}
	if gateOp.FileMode != 0o755 {
		t.Errorf("gate FileMode = %o, want 0755", gateOp.FileMode)
	}
	if !strings.Contains(gateOp.Content, "prism perms-guard") {
		t.Errorf("gate script doesn't exec prism perms-guard:\n%s", gateOp.Content)
	}
	if !strings.Contains(gateOp.Content, "policy.json") {
		t.Errorf("gate script doesn't reference policy path:\n%s", gateOp.Content)
	}
}

// TestGemini_PermsGuard_ScopedWithHook verifies that a scoped permissions
// block + a scoped hook produces a scoped sidecar and a wrapper that wires
// the source script to the scope's policy.
func TestGemini_PermsGuard_ScopedWithHook(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/fake/.agents/src/billing/hooks/audit.sh",
				ScopePath:  "src/billing",
			},
		},
		ScopedPermissions: []*model.Permissions{
			{
				ScopePath: "src/billing",
				Deny:      []string{"bash:rm *"},
			},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	paths := map[string]plugin.Operation{}
	for _, op := range ops {
		paths[op.Path] = op
	}
	wantPolicy := filepath.Join(".gemini", "hooks", "__perms-guard__", "src-billing.policy.json")
	wantWrapper := filepath.Join(".gemini", "hooks", "__perms-guard__", "src-billing-PreToolUse-audit.sh")
	policyOp, ok := paths[wantPolicy]
	if !ok {
		t.Fatalf("missing scoped policy at %q; have: %v", wantPolicy, opsPaths(ops))
	}
	if !strings.Contains(policyOp.Content, `"bash:rm *"`) {
		t.Errorf("scoped policy missing deny rule:\n%s", policyOp.Content)
	}
	wrapperOp, ok := paths[wantWrapper]
	if !ok {
		t.Fatalf("missing scoped wrapper at %q; have: %v", wantWrapper, opsPaths(ops))
	}
	if !strings.Contains(wrapperOp.Content, "src-billing.policy.json") {
		t.Errorf("wrapper doesn't reference scoped policy:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "audit.sh") {
		t.Errorf("wrapper doesn't reference source script:\n%s", wrapperOp.Content)
	}
}

// TestGemini_PermsGuard_DisableHookWrappers verifies that setting
// DisableHookWrappers suppresses wrapper + policy emission entirely.
func TestGemini_PermsGuard_DisableHookWrappers(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Permissions: &model.Permissions{
			Allow: []string{"bash:ls *"},
		},
	}
	p := &GeminiPlugin{DisableHookWrappers: true}
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "__perms-guard__") {
			t.Errorf("unexpected perms-guard artifact with DisableHookWrappers=true: %s", op.Path)
		}
	}
}

// TestGemini_PermsGuard_CapabilityNative verifies the Capabilities matrix
// reports Permissions as Native now that the wrapper provides enforcement.
func TestGemini_PermsGuard_CapabilityNative(t *testing.T) {
	p := NewGemini()
	if got := p.Capabilities().Permissions; got != plugin.SupportNative {
		t.Errorf("Capabilities().Permissions = %v, want SupportNative", got)
	}
}

// TestGemini_Capabilities_FeatureFlipped verifies the updated capability
// matrix after the feature-parity expansion (Phase B1).
func TestGemini_Capabilities_FeatureFlipped(t *testing.T) {
	caps := NewGemini().Capabilities()
	if caps.Commands != plugin.SupportNative {
		t.Errorf("Commands = %v, want SupportNative", caps.Commands)
	}
	if caps.Agents != plugin.SupportNative {
		t.Errorf("Agents = %v, want SupportNative", caps.Agents)
	}
	if caps.Hooks != plugin.SupportNative {
		t.Errorf("Hooks = %v, want SupportNative", caps.Hooks)
	}
	if caps.Skills != plugin.SupportDegraded {
		t.Errorf("Skills = %v, want SupportDegraded (projected as agents)", caps.Skills)
	}
}
