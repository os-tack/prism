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

func TestCursorPlan_RootAndScope(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "global context",
		},
		Scopes: []*model.Scope{
			{
				Path:        "src/billing",
				Globs:       []string{"src/billing/**"},
				Description: "Stripe webhook context",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/context.md",
					Body:       "billing context",
				},
			},
		},
	}

	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(ops))
	}

	// First op: root context.
	root := ops[0]
	if root.Path != ".cursor/rules/_root.mdc" {
		t.Errorf("root op path = %q, want .cursor/rules/_root.mdc", root.Path)
	}
	if !strings.Contains(root.Content, "alwaysApply: true") {
		t.Errorf("root op content missing alwaysApply: true\n---\n%s", root.Content)
	}
	if !strings.Contains(root.Content, "global context") {
		t.Errorf("root op content missing body\n---\n%s", root.Content)
	}
	if !strings.HasPrefix(root.Content, "---\n") {
		t.Errorf("root op content does not begin with frontmatter delimiter:\n%s", root.Content)
	}
	if root.Plugin != "cursor" {
		t.Errorf("root op plugin = %q, want cursor", root.Plugin)
	}

	// Second op: scope.
	sc := ops[1]
	if sc.Path != ".cursor/rules/src-billing.mdc" {
		t.Errorf("scope op path = %q, want .cursor/rules/src-billing.mdc", sc.Path)
	}
	if !strings.Contains(sc.Content, "description: Stripe webhook context") {
		t.Errorf("scope op content missing description\n---\n%s", sc.Content)
	}
	if !strings.Contains(sc.Content, `globs: ["src/billing/**"]`) {
		t.Errorf("scope op content missing globs frontmatter\n---\n%s", sc.Content)
	}
	if !strings.Contains(sc.Content, "billing context") {
		t.Errorf("scope op content missing body\n---\n%s", sc.Content)
	}
	if strings.Contains(sc.Content, "alwaysApply") {
		t.Errorf("scope op should not have alwaysApply\n---\n%s", sc.Content)
	}
	if len(sc.Sources) != 1 || sc.Sources[0] != "src/billing/context.md" {
		t.Errorf("scope op sources = %v, want [src/billing/context.md]", sc.Sources)
	}
}

func TestCursorPlan_EmptyDescriptionFallback(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:  "src/api",
				Globs: []string{"src/api/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/api/context.md",
					Body:       "api context",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if !strings.Contains(ops[0].Content, "description: Context for src/api") {
		t.Errorf("fallback description missing\n---\n%s", ops[0].Content)
	}
}

func TestCursorPlan_Nil(t *testing.T) {
	p := NewCursor()
	ops, err := p.Plan(nil, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan(nil) error: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("Plan(nil) ops = %d, want 0", len(ops))
	}
}

func TestCursorSlugify(t *testing.T) {
	cases := map[string]string{
		"src/billing":     "src-billing",
		"src/billing/api": "src-billing-api",
		"/src/billing/":   "src-billing",
		"SRC/Billing":     "src-billing",
		"src\\billing":    "src-billing",
		"":                "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCursorCapabilities(t *testing.T) {
	p := NewCursor()
	caps := p.Capabilities()
	if caps.Context != "native" {
		t.Errorf("Context support = %q, want native", caps.Context)
	}
	if caps.Agents != "unsupported" {
		t.Errorf("Agents support = %q, want unsupported", caps.Agents)
	}
	if caps.MCP != "native" {
		t.Errorf("MCP support = %q, want native", caps.MCP)
	}
	if caps.Skills != "degraded" {
		t.Errorf("Skills support = %q, want degraded", caps.Skills)
	}
	if caps.Commands != "degraded" {
		t.Errorf("Commands support = %q, want degraded", caps.Commands)
	}
}

func TestCursorPlan_UnknownModeErrors(t *testing.T) {
	p := NewCursor()
	proj := &model.Project{AgentsDir: "/tmp/.agents"}
	_, err := p.Plan(proj, model.TargetOption{Mode: "bogus"})
	if err == nil {
		t.Fatalf("expected error for unknown mode")
	}
}

// TestCursor_Skill_AsScopedRule verifies that a Skill projects to a scoped
// rule file at .cursor/rules/skill-<slug>.mdc with description + globs in
// frontmatter and body from SKILL.md. Scripts present → warning attached.
func TestCursor_Skill_AsScopedRule(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "Stripe Webhook Validator",
				Description: "Validate Stripe webhook signatures end-to-end",
				Globs:       []string{"src/billing/**", "tests/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/stripe-webhook/SKILL.md",
					Body:       "How to validate Stripe webhooks step by step.",
				},
				Scripts: []string{"verify.sh", "diagnose.py"},
			},
		},
	}

	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".cursor/rules/skill-stripe-webhook-validator.mdc"
	if op.Path != wantPath {
		t.Errorf("skill op path = %q, want %q", op.Path, wantPath)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("skill op kind = %v, want OpWrite", op.Kind)
	}
	if op.Mode != plugin.ModeWrite {
		t.Errorf("skill op mode = %v, want ModeWrite", op.Mode)
	}
	if !strings.Contains(op.Content, "description: Validate Stripe webhook signatures end-to-end") {
		t.Errorf("skill op missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `globs: ["src/billing/**","tests/billing/**"]`) {
		t.Errorf("skill op missing globs\n---\n%s", op.Content)
	}
	if strings.Contains(op.Content, "alwaysApply") {
		t.Errorf("skill op should not have alwaysApply\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "How to validate Stripe webhooks step by step.") {
		t.Errorf("skill op missing body\n---\n%s", op.Content)
	}

	// Scripts → warning attached to the skill op itself.
	if len(op.Warnings) == 0 {
		t.Fatalf("expected at least one warning on skill op")
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no skill primitive") && strings.Contains(w.Message, "verify.sh") && strings.Contains(w.Message, "diagnose.py") {
			found = true
			if w.Severity != "info" {
				t.Errorf("skill warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected skill warning naming both scripts, got %#v", op.Warnings)
	}
}

// TestCursor_Skill_NoScripts_NoWarning verifies that a skill with no
// scripts still projects but emits no warning on the skill op.
func TestCursor_Skill_NoScripts_NoWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:    "Pure Skill",
				Trigger: "When the user asks about purity",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/pure/SKILL.md",
					Body:       "no scripts here",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if len(ops[0].Warnings) != 0 {
		t.Errorf("expected no warnings, got %#v", ops[0].Warnings)
	}
	// Trigger should fill in as the description when Description is empty.
	if !strings.Contains(ops[0].Content, "description: When the user asks about purity") {
		t.Errorf("expected Trigger to fill description\n---\n%s", ops[0].Content)
	}
}

// TestCursor_Commands_Warning verifies that a Command emits no file but
// attaches an info warning to some op.
func TestCursor_Commands_Warning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Commands: []*model.Command{
			{
				Name:        "deploy",
				Description: "Deploy to staging",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/commands/deploy.md",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	// No file-level command op; only the root context op.
	if len(ops) != 1 {
		t.Fatalf("expected 1 op (no command file), got %d", len(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "commands") {
			t.Errorf("unexpected command op: %s", op.Path)
		}
	}
	// Warning must be attached to the single op (root).
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no slash-command equivalent") && strings.Contains(w.Message, "deploy") {
			found = true
			if w.Severity != "info" {
				t.Errorf("command warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected command warning, got %#v", ops[0].Warnings)
	}
}

// TestCursor_Agents_Warning verifies that an Agent emits no file but
// attaches an info warning to some op.
func TestCursor_Agents_Warning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Agents: []*model.Agent{
			{
				Name:        "code-reviewer",
				Description: "Reviews code for style and bugs",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/code-reviewer.md",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no subagent primitive") && strings.Contains(w.Message, "code-reviewer") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected agent warning, got %#v", ops[0].Warnings)
	}
}

// TestCursor_Hooks_Warning verifies that a Hook emits no file but
// attaches an info warning to some op.
func TestCursor_Hooks_Warning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/.agents/hooks/audit.sh",
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no hook primitive") && strings.Contains(w.Message, "PreToolUse:Bash") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hook warning, got %#v", ops[0].Warnings)
	}
}

// TestCursor_Permissions_Warning verifies that non-empty Permissions emit
// no file but attach an info warning to some op.
func TestCursor_Permissions_Warning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Permissions: &model.Permissions{
			Allow: []string{"Bash(ls)", "Read"},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no permissions primitive") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected permissions warning, got %#v", ops[0].Warnings)
	}
}

// TestCursor_Permissions_EmptyNoWarning verifies that an empty Permissions
// struct (allocated but all lists nil/empty) does NOT emit a warning.
func TestCursor_Permissions_EmptyNoWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Permissions: &model.Permissions{},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "permissions") {
			t.Errorf("unexpected permissions warning on empty permissions: %#v", w)
		}
	}
}

// TestCursor_MCP verifies that proj.MCP servers project to .cursor/mcp.json
// via OpMerge and that existing unrelated top-level keys at
// <root>/.cursor/mcp.json are preserved.
func TestCursor_MCP(t *testing.T) {
	root := t.TempDir()
	cursorDir := filepath.Join(root, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := `{
  "experimental": true,
  "mcpServers": {
    "stale": { "command": "this-will-be-overwritten" }
  }
}
`
	if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		MCP: []*model.MCPServer{
			{
				Name:    "linear",
				Command: "npx",
				Args:    []string{"-y", "@linear/mcp"},
				Env:     map[string]string{"LINEAR_TOKEN": "xxx"},
			},
			{
				Name: "remote-tools",
				URL:  "https://mcp.example.com/sse",
			},
		},
	}

	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	// Find the mcp op.
	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".cursor/mcp.json" {
			mcpOp = &ops[i]
			break
		}
	}
	if mcpOp == nil {
		t.Fatalf("no .cursor/mcp.json op found in %d ops", len(ops))
	}
	if mcpOp.Kind != plugin.OpMerge {
		t.Errorf("mcp op kind = %v, want OpMerge", mcpOp.Kind)
	}
	if mcpOp.Mode != plugin.ModeWrite {
		t.Errorf("mcp op mode = %v, want ModeWrite", mcpOp.Mode)
	}
	if len(mcpOp.Sources) != 1 || mcpOp.Sources[0] != "mcp.yaml" {
		t.Errorf("mcp op sources = %v, want [mcp.yaml]", mcpOp.Sources)
	}

	// Content must parse to JSON containing experimental=true AND
	// mcpServers with both new servers.
	var out map[string]any
	if err := json.Unmarshal([]byte(mcpOp.Content), &out); err != nil {
		t.Fatalf("mcp op content does not parse as JSON: %v\n---\n%s", err, mcpOp.Content)
	}
	if exp, ok := out["experimental"].(bool); !ok || !exp {
		t.Errorf("expected experimental=true preserved; got %v", out["experimental"])
	}
	servers, ok := out["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or wrong type: %v", out["mcpServers"])
	}
	linear, ok := servers["linear"].(map[string]any)
	if !ok {
		t.Fatalf("linear server missing or wrong type: %v", servers["linear"])
	}
	if linear["command"] != "npx" {
		t.Errorf("linear.command = %v, want npx", linear["command"])
	}
	args, ok := linear["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "-y" || args[1] != "@linear/mcp" {
		t.Errorf("linear.args = %v, want [-y @linear/mcp]", linear["args"])
	}
	env, ok := linear["env"].(map[string]any)
	if !ok || env["LINEAR_TOKEN"] != "xxx" {
		t.Errorf("linear.env = %v, want {LINEAR_TOKEN: xxx}", linear["env"])
	}
	remote, ok := servers["remote-tools"].(map[string]any)
	if !ok {
		t.Fatalf("remote-tools missing or wrong type: %v", servers["remote-tools"])
	}
	if remote["url"] != "https://mcp.example.com/sse" {
		t.Errorf("remote-tools.url = %v, want https://mcp.example.com/sse", remote["url"])
	}
	// Stale entry must be gone.
	if _, exists := servers["stale"]; exists {
		t.Errorf("stale server should have been replaced; got %v", servers["stale"])
	}
}

// TestCursor_MCP_NoExisting verifies that without an existing mcp.json,
// the op still emits with just the new servers.
func TestCursor_MCP_NoExisting(t *testing.T) {
	root := t.TempDir()
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		MCP: []*model.MCPServer{
			{Name: "linear", Command: "npx"},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".cursor/mcp.json" {
			mcpOp = &ops[i]
		}
	}
	if mcpOp == nil {
		t.Fatalf("no mcp op")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(mcpOp.Content), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	servers, _ := out["mcpServers"].(map[string]any)
	if _, ok := servers["linear"]; !ok {
		t.Errorf("expected linear in output: %s", mcpOp.Content)
	}
}
