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

	mergedContent := mergeContent(t, root, settingsOp)
	var merged map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &merged); err != nil {
		t.Fatalf("unmarshal settings: %v\ncontent: %s", err, mergedContent)
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

// TestGemini_ScopedSkill: a scoped Skill is dropped (no file produced) and
// a per-skill info warning is emitted naming the skill and scope path.
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
		t.Fatalf("Plan error: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "audit-trail") || strings.Contains(op.Path, "SKILL") {
			t.Errorf("unexpected skill file projected: %s", op.Path)
		}
	}
	found := false
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "Gemini has no skills primitive") &&
				strings.Contains(w.Message, `"audit-trail"`) &&
				strings.Contains(w.Message, "src/billing") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected scoped-skill drop warning naming skill and scope, got ops: %+v", ops)
	}
}

// TestGemini_ScopedSkillCollision: two scoped skills sharing a Name but at
// different ScopePaths each emit their own info warning and neither
// projects a file.
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
		t.Fatalf("Plan error: %v", err)
	}

	for _, op := range ops {
		if strings.Contains(op.Path, "audit-trail") {
			t.Errorf("unexpected skill file projected: %s", op.Path)
		}
	}

	wantScopes := map[string]bool{"src/billing": false, "src/payments": false}
	for _, op := range ops {
		for _, w := range op.Warnings {
			if !strings.Contains(w.Message, "Gemini has no skills primitive") {
				continue
			}
			for sp := range wantScopes {
				if strings.Contains(w.Message, sp) {
					wantScopes[sp] = true
				}
			}
		}
	}
	for sp, seen := range wantScopes {
		if !seen {
			t.Errorf("missing warning for scope %q", sp)
		}
	}
}

// TestGemini_ScopedCommandAgentHookMCP: verifies scoped command, agent,
// hook, scoped-permission, and scoped MCP each emit their own per-item
// info warning that mentions the scope path.
func TestGemini_ScopedCommandAgentHookMCP(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "root",
		},
		Commands: []*model.Command{
			{Name: "deploy", ScopePath: "src/billing"},
		},
		Agents: []*model.Agent{
			{Name: "reviewer", ScopePath: "src/billing"},
		},
		Hooks: []*model.Hook{
			{Event: "PostToolUse", Matcher: "Edit", ScopePath: "src/billing", ScriptPath: "/tmp/.agents/src/billing/hooks/verify.sh"},
		},
		ScopedPermissions: []*model.Permissions{
			{ScopePath: "src/billing", Allow: []string{"Read(*)"}},
		},
		MCP: []*model.MCPServer{
			{Name: "linear", Command: "npx", ScopePath: "src/billing"},
		},
	}
	p := NewGemini()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	matchers := []struct {
		needle string
		kind   string
	}{
		{`scoped command "deploy"`, "command"},
		{`scoped agent "reviewer"`, "agent"},
		{`scoped hook PostToolUse:Edit`, "hook"},
		// Scoped permissions now project to a perms-guard wrapper (covered
		// by TestGemini_PermsGuard_*) instead of emitting an info warning.
		{`scoped MCP server "linear"`, "mcp"},
	}
	found := map[string]bool{}
	for _, op := range ops {
		for _, w := range op.Warnings {
			if !strings.Contains(w.Message, "src/billing") {
				continue
			}
			for _, m := range matchers {
				if strings.Contains(w.Message, m.needle) {
					found[m.kind] = true
				}
			}
		}
	}
	for _, m := range matchers {
		if !found[m.kind] {
			t.Errorf("missing %s scoped warning containing %q", m.kind, m.needle)
		}
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
