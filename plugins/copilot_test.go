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

// TestCopilot_RootContext_Symlink verifies a project with Context produces
// a symlink op at .github/copilot-instructions.md pointing back at the
// source via a relative LinkTarget.
func TestCopilot_RootContext_Symlink(t *testing.T) {
	root := "/tmp/repo"
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Context: &model.Document{
			SourcePath: filepath.Join(root, ".agents", "context.md"),
			Body:       "global context",
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".github/copilot-instructions.md" {
		t.Errorf("path = %q, want .github/copilot-instructions.md", op.Path)
	}
	if op.Kind != plugin.OpSymlink {
		t.Errorf("kind = %v, want OpSymlink", op.Kind)
	}
	if op.Mode != plugin.ModeSymlink {
		t.Errorf("mode = %v, want ModeSymlink", op.Mode)
	}
	// LinkTarget is relative to .github/ (target dir). From /tmp/repo/.github
	// back to /tmp/repo/.agents/context.md → "../.agents/context.md".
	wantTarget := "../.agents/context.md"
	if op.LinkTarget != wantTarget {
		t.Errorf("LinkTarget = %q, want %q", op.LinkTarget, wantTarget)
	}
	if op.Plugin != "copilot" {
		t.Errorf("plugin = %q, want copilot", op.Plugin)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "context.md" {
		t.Errorf("Sources = %v, want [context.md]", op.Sources)
	}
}

// TestCopilot_RootContext_Write verifies that opts.Mode="write" produces a
// write op with body content (no LinkTarget, no symlink kind).
func TestCopilot_RootContext_Write(t *testing.T) {
	root := "/tmp/repo"
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Context: &model.Document{
			SourcePath: filepath.Join(root, ".agents", "context.md"),
			Body:       "global context",
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if op.Mode != plugin.ModeWrite {
		t.Errorf("mode = %v, want ModeWrite", op.Mode)
	}
	if op.Content != "global context" {
		t.Errorf("content = %q, want %q", op.Content, "global context")
	}
	if op.LinkTarget != "" {
		t.Errorf("LinkTarget = %q, want empty in write mode", op.LinkTarget)
	}
}

// TestCopilot_Scope_SingleGlob verifies that a Scope with a single glob
// produces a .github/instructions/<slug>.instructions.md file with the
// correct applyTo frontmatter and no degradation warning.
func TestCopilot_Scope_SingleGlob(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:        "src/billing",
				Globs:       []string{"src/billing/**"},
				Description: "Stripe webhook context",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/context.md",
					Body:       "billing context",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/instructions/src-billing.instructions.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if op.Mode != plugin.ModeWrite {
		t.Errorf("mode = %v, want ModeWrite", op.Mode)
	}
	if !strings.Contains(op.Content, `applyTo: "src/billing/**"`) {
		t.Errorf("missing applyTo in frontmatter\n---\n%s", op.Content)
	}
	if strings.Contains(op.Content, "globs:") {
		t.Errorf("must not emit 'globs:' key (not a Copilot field)\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "billing context") {
		t.Errorf("missing body\n---\n%s", op.Content)
	}
	if len(op.Warnings) != 0 {
		t.Errorf("expected no warnings for single-glob scope, got %#v", op.Warnings)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "src/billing/context.md" {
		t.Errorf("Sources = %v, want [src/billing/context.md]", op.Sources)
	}
}

// TestCopilot_Scope_MultipleGlobs_Warns verifies that a Scope with multiple
// globs uses the first glob in applyTo and emits a warning naming the
// ignored second.
func TestCopilot_Scope_MultipleGlobs_Warns(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:  "src/billing",
				Globs: []string{"src/billing/**", "tests/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/context.md",
					Body:       "billing",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if !strings.Contains(op.Content, `applyTo: "src/billing/**"`) {
		t.Errorf("expected first glob in applyTo\n---\n%s", op.Content)
	}
	if strings.Contains(op.Content, "tests/billing/**\"") {
		t.Errorf("second glob must not appear in frontmatter\n---\n%s", op.Content)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "single-valued") &&
			strings.Contains(w.Message, "src/billing/**") &&
			strings.Contains(w.Message, "tests/billing/**") {
			found = true
			if w.Severity != "info" {
				t.Errorf("warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected multi-glob warning, got %#v", op.Warnings)
	}
}

// TestCopilot_Scope_NoGlobs_FallsBackToStar verifies that a Scope with no
// globs falls back to applyTo "**" with no warning.
func TestCopilot_Scope_NoGlobs_FallsBackToStar(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path: "global",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/global/context.md",
					Body:       "global",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if !strings.Contains(ops[0].Content, `applyTo: "**"`) {
		t.Errorf("expected applyTo \"**\" fallback\n---\n%s", ops[0].Content)
	}
	if len(ops[0].Warnings) != 0 {
		t.Errorf("expected no warnings for empty-globs fallback, got %#v", ops[0].Warnings)
	}
}

// TestCopilot_Command_AsPrompt verifies that a Command produces a
// .github/prompts/<name>.prompt.md file with description + mode frontmatter.
func TestCopilot_Command_AsPrompt(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Commands: []*model.Command{
			{
				Name:        "review",
				Description: "Review the pending changes",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/commands/review.md",
					Body:       "Walk through the diff and flag issues.",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/prompts/review.prompt.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if !strings.Contains(op.Content, `description: "Review the pending changes"`) {
		t.Errorf("missing description frontmatter\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "mode: ask") {
		t.Errorf("missing mode frontmatter\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Walk through the diff and flag issues.") {
		t.Errorf("missing body\n---\n%s", op.Content)
	}
}

// TestCopilot_Skill_AsInstructions verifies that a Skill produces an
// .instructions.md file under .github/instructions with a skill- prefix and
// emits a script-warning when Scripts is non-empty.
func TestCopilot_Skill_AsInstructions(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "Stripe Webhook Validator",
				Description: "Validate Stripe webhooks",
				Globs:       []string{"src/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/stripe-webhook/SKILL.md",
					Body:       "Steps to validate.",
				},
				Scripts: []string{"verify.sh"},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/instructions/skill-stripe-webhook-validator.instructions.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, `applyTo: "src/billing/**"`) {
		t.Errorf("missing applyTo\n---\n%s", op.Content)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no script execution") && strings.Contains(w.Message, "verify.sh") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected script warning, got %#v", op.Warnings)
	}
}

// TestCopilot_Agent_EmitsFile verifies that an Agent now produces a
// .github/agents/<slug>.agent.md file with the expected frontmatter and
// body — v0.8 promoted Copilot agents from SupportUnsupported (warning
// only) to SupportNative.
func TestCopilot_Agent_EmitsFile(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Agents: []*model.Agent{
			{
				Name:        "code-reviewer",
				Description: "Reviews code",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/code-reviewer.md",
					Body:       "system prompt body",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/agents/code-reviewer.agent.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if !strings.Contains(op.Content, `name: "code-reviewer"`) {
		t.Errorf("missing name frontmatter\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `description: "Reviews code"`) {
		t.Errorf("missing description frontmatter\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "system prompt body") {
		t.Errorf("missing body\n---\n%s", op.Content)
	}
}

// TestCopilot_UnknownModeErrors verifies that a bogus Mode value yields an
// error from Plan.
func TestCopilot_UnknownModeErrors(t *testing.T) {
	p := NewCopilot()
	proj := &model.Project{AgentsDir: "/tmp/.agents"}
	_, err := p.Plan(proj, model.TargetOption{Mode: "asdf"})
	if err == nil {
		t.Fatalf("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "asdf") {
		t.Errorf("error %q should mention the bad mode value", err)
	}
}

// TestCopilot_DetectMarkers verifies the Detect signal: empty dir → false,
// .github/ → true.
func TestCopilot_DetectMarkers(t *testing.T) {
	p := NewCopilot()

	// Empty temp dir → no markers → false.
	empty := t.TempDir()
	if p.Detect(empty) {
		t.Errorf("Detect(empty) = true, want false")
	}

	// With .github/ directory → true.
	withGithub := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withGithub, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !p.Detect(withGithub) {
		t.Errorf("Detect(.github/) = false, want true")
	}
}

// TestCopilot_Capabilities sanity-checks the capability matrix.
func TestCopilot_Capabilities(t *testing.T) {
	caps := NewCopilot().Capabilities()
	if caps.Context != plugin.SupportNative {
		t.Errorf("Context = %v, want native", caps.Context)
	}
	if caps.ScopePaths != plugin.SupportNative {
		t.Errorf("ScopePaths = %v, want native", caps.ScopePaths)
	}
	if caps.ScopeSemantic != plugin.SupportDegraded {
		t.Errorf("ScopeSemantic = %v, want degraded", caps.ScopeSemantic)
	}
	if caps.Commands != plugin.SupportNative {
		t.Errorf("Commands = %v, want native", caps.Commands)
	}
	if caps.Agents != plugin.SupportNative {
		t.Errorf("Agents = %v, want native", caps.Agents)
	}
	if caps.Hooks != plugin.SupportUnsupported {
		t.Errorf("Hooks = %v, want unsupported", caps.Hooks)
	}
	if caps.Permissions != plugin.SupportUnsupported {
		t.Errorf("Permissions = %v, want unsupported", caps.Permissions)
	}
	if caps.MCP != plugin.SupportNative {
		t.Errorf("MCP = %v, want native", caps.MCP)
	}
}

// TestCopilot_ScopedSkill verifies that a Skill with ScopePath produces a
// scoped instructions file with the scope slug as a filename prefix and the
// scope's glob in applyTo.
func TestCopilot_ScopedSkill(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Skills: []*model.Skill{
			{
				Name:        "Audit Trail",
				Description: "Track billing changes",
				ScopePath:   "src/billing",
				Globs:       []string{"src/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/proj/.agents/src/billing/skills/audit-trail/SKILL.md",
					Body:       "Body of audit trail skill.",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/instructions/skill-src-billing-audit-trail.instructions.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, `applyTo: "src/billing/**"`) {
		t.Errorf("missing applyTo for the scope's glob\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || !strings.Contains(op.Sources[0], "src/billing/skills/audit-trail/SKILL.md") {
		t.Errorf("Sources = %v, want path containing src/billing/skills/audit-trail/SKILL.md", op.Sources)
	}
}

// TestCopilot_ScopedSkillCollision verifies that the same skill name in two
// different scopes produces two distinct instruction files, one per scope,
// with different prefixes.
func TestCopilot_ScopedSkillCollision(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/proj/.agents",
		Skills: []*model.Skill{
			{
				Name:      "Validator",
				ScopePath: "src/billing",
				Globs:     []string{"src/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/proj/.agents/src/billing/skills/validator/SKILL.md",
					Body:       "billing validator",
				},
			},
			{
				Name:      "Validator",
				ScopePath: "src/auth",
				Globs:     []string{"src/auth/**"},
				Document: &model.Document{
					SourcePath: "/tmp/proj/.agents/src/auth/skills/validator/SKILL.md",
					Body:       "auth validator",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
	paths := map[string]bool{}
	for _, op := range ops {
		paths[op.Path] = true
	}
	if !paths[".github/instructions/skill-src-billing-validator.instructions.md"] {
		t.Errorf("missing billing-validator op, got paths: %v", paths)
	}
	if !paths[".github/instructions/skill-src-auth-validator.instructions.md"] {
		t.Errorf("missing auth-validator op, got paths: %v", paths)
	}
}

// TestCopilot_ScopedCommand_Warning verifies that a scoped command produces
// a prompt file with the scope slug as a filename prefix and an info warning
// noting prompts have no path scoping.
func TestCopilot_ScopedCommand_Warning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/proj/.agents",
		Commands: []*model.Command{
			{
				Name:        "deploy",
				Description: "Deploy billing service",
				ScopePath:   "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/proj/.agents/src/billing/commands/deploy.md",
					Body:       "deploy steps",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/prompts/src-billing-deploy.prompt.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no path scoping") && strings.Contains(w.Message, "src/billing") {
			found = true
			if w.Severity != "info" {
				t.Errorf("warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected scoped-command warning, got %#v", op.Warnings)
	}
}

// TestCopilot_ScopedHook_Warning verifies that a scoped hook produces no
// file but emits an info warning naming the scope.
func TestCopilot_ScopedHook_Warning(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/proj/.agents/context.md",
			Body:       "ctx",
		},
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/proj/.agents/src/billing/hooks/audit.sh",
				ScopePath:  "src/billing",
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "hooks") {
			t.Errorf("unexpected hook file: %s", op.Path)
		}
	}
	found := false
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "hooks are in preview") && strings.Contains(w.Message, "src/billing") && strings.Contains(w.Message, "PreToolUse:Bash") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected scoped-hook warning, got ops=%#v", ops)
	}
}

// TestCopilot_Agent_Projects verifies the full agent emission path: a
// project with an Agent (and other capabilities mixed in) produces a
// .github/agents/<slug>.agent.md file with frontmatter that passes through
// non-canonical keys (tools, model) from the source document's frontmatter,
// alongside the canonical name/description that the parser computed.
func TestCopilot_Agent_Projects(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Agents: []*model.Agent{
			{
				Name:        "reviewer",
				Description: "Reviews diffs",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/reviewer.md",
					Body:       "Walk the diff line by line.",
					Frontmatter: map[string]any{
						"description":    "old description (overridden)",
						"tools":          []any{"Read", "Grep"},
						"model":          "claude-sonnet-4-5",
						"user-invocable": true,
						"agents":         []any{"summarizer"},
						"handoffs":       []any{"code-fixer"},
					},
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %+v", len(ops), ops)
	}
	op := ops[0]
	wantPath := ".github/agents/reviewer.agent.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if op.Mode != plugin.ModeWrite {
		t.Errorf("mode = %v, want ModeWrite", op.Mode)
	}
	// Canonical fields take precedence over source frontmatter.
	if !strings.Contains(op.Content, `name: "reviewer"`) {
		t.Errorf("missing name\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `description: "Reviews diffs"`) {
		t.Errorf("missing canonical description\n---\n%s", op.Content)
	}
	if strings.Contains(op.Content, "old description (overridden)") {
		t.Errorf("source description should be overridden by canonical, got\n---\n%s", op.Content)
	}
	// Pass-through fields appear in the frontmatter.
	for _, want := range []string{
		`tools: ["Read", "Grep"]`,
		`model: "claude-sonnet-4-5"`,
		`user-invocable: true`,
		`agents: ["summarizer"]`,
		`handoffs: ["code-fixer"]`,
	} {
		if !strings.Contains(op.Content, want) {
			t.Errorf("missing %q\n---\n%s", want, op.Content)
		}
	}
	if !strings.Contains(op.Content, "Walk the diff line by line.") {
		t.Errorf("missing body\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "agents/reviewer.md" {
		t.Errorf("Sources = %v, want [agents/reviewer.md]", op.Sources)
	}
}

// TestCopilot_MCP_Projects + Merger preserves user keys verifies that an
// MCP server in the canonical model produces a .github/mcp.json OpMerge
// whose Merger preserves any user-authored keys (both top-level and inside
// mcpServers) that the canonical model doesn't know about.
func TestCopilot_MCP_Projects(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
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
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".github/mcp.json" {
			mcpOp = &ops[i]
			break
		}
	}
	if mcpOp == nil {
		t.Fatalf("missing .github/mcp.json op; got: %+v", ops)
	}
	if mcpOp.Kind != plugin.OpMerge {
		t.Errorf("kind = %v, want OpMerge", mcpOp.Kind)
	}
	if mcpOp.Mode != plugin.ModeWrite {
		t.Errorf("mode = %v, want ModeWrite", mcpOp.Mode)
	}
	if mcpOp.Plugin != "copilot" {
		t.Errorf("plugin = %q, want copilot", mcpOp.Plugin)
	}
	if mcpOp.Merger == nil {
		t.Fatalf("Merger must be set for OpMerge")
	}

	// 1. Empty existing file → produces the two servers.
	out, err := mcpOp.Merger(nil)
	if err != nil {
		t.Fatalf("merger(nil): %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal: %v\ncontent:\n%s", err, out)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		t.Fatalf("mcpServers missing\n%s", out)
	}
	if _, ok := servers["filesystem"]; !ok {
		t.Errorf("filesystem missing in fresh merge: %v", servers)
	}
	remote, _ := servers["remote"].(map[string]any)
	if got, want := remote["url"], "https://example.com/mcp"; got != want {
		t.Errorf("remote.url = %v, want %v", got, want)
	}

	// 2. Existing file with a user-authored top-level key AND a user-authored
	// mcpServer entry: both must survive the merge.
	existing := []byte(`{
  "inputs": [{"id": "github-token", "type": "promptString"}],
  "mcpServers": {
    "user-server": {"command": "user-cmd"}
  }
}`)
	out2, err := mcpOp.Merger(existing)
	if err != nil {
		t.Fatalf("merger(existing): %v", err)
	}
	var doc2 map[string]any
	if err := json.Unmarshal([]byte(out2), &doc2); err != nil {
		t.Fatalf("unmarshal merged: %v\ncontent:\n%s", err, out2)
	}
	// Top-level user key preserved.
	if _, ok := doc2["inputs"]; !ok {
		t.Errorf("top-level user key 'inputs' was dropped\n%s", out2)
	}
	srv2, _ := doc2["mcpServers"].(map[string]any)
	if _, ok := srv2["user-server"]; !ok {
		t.Errorf("user-authored mcpServer 'user-server' was dropped\n%s", out2)
	}
	if _, ok := srv2["filesystem"]; !ok {
		t.Errorf("prism 'filesystem' server missing after merge\n%s", out2)
	}
}

// TestCopilot_Semantic_ApplyToFrontmatter is a regression-style check that
// scope projection emits applyTo: frontmatter (single-glob today). This is
// the native semantic-targeting surface Copilot already supports, and the
// test fixes the contract so we don't accidentally regress to a less
// specific shape.
func TestCopilot_Semantic_ApplyToFrontmatter(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:  "src/auth",
				Globs: []string{"src/auth/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/auth/context.md",
					Body:       "auth context",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/instructions/src-auth.instructions.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.HasPrefix(op.Content, "---\napplyTo: ") {
		t.Errorf("content must lead with applyTo frontmatter\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `applyTo: "src/auth/**"`) {
		t.Errorf("missing applyTo with scope glob\n---\n%s", op.Content)
	}
	// Make sure the body is delimited by the closing fence.
	if !strings.Contains(op.Content, "---\nauth context") {
		t.Errorf("body must follow closing --- fence\n---\n%s", op.Content)
	}
}

// TestCopilot_ClaudeAgentsOverlap_InfoWarning verifies that when the Claude
// target is also enabled, Copilot skips its own .github/agents/*.agent.md
// emission (VS Code/Copilot auto-discovers .claude/agents/) and attaches
// an info warning to the first emitted op explaining the suppression.
func TestCopilot_ClaudeAgentsOverlap_InfoWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Agents: []*model.Agent{
			{
				Name:        "reviewer",
				Description: "Reviews",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/reviewer.md",
					Body:       "body",
				},
			},
		},
		Config: &model.Config{
			Targets: []string{"claude", "copilot"},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	// Context op exists; no agent op.
	for _, op := range ops {
		if strings.Contains(op.Path, "/agents/") {
			t.Errorf("Copilot must not emit agent files when claude is enabled: %s", op.Path)
		}
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (context only), got %d: %+v", len(ops), ops)
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "not emitted") && strings.Contains(w.Message, "reviewer") && strings.Contains(w.Message, ".claude/agents/") {
			found = true
			if w.Severity != "info" {
				t.Errorf("warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected overlap warning, got %#v", ops[0].Warnings)
	}
}

// TestCopilot_AgentScoped_Warns verifies that a scoped agent gets a
// scope-slug-prefixed filename and a degradation info warning noting
// Copilot has no per-path agent scoping.
func TestCopilot_AgentScoped_Warns(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Agents: []*model.Agent{
			{
				Name:        "auditor",
				Description: "Audit billing",
				ScopePath:   "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/agents/auditor.md",
					Body:       "audit body",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".github/agents/src-billing-auditor.agent.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no path scoping") && strings.Contains(w.Message, "src/billing") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected scoped-agent degradation warning, got %#v", op.Warnings)
	}
}

// TestCopilot_Preview_HookFlagOff verifies the v0.8.1 default behaviour:
// without --enable-preview-hooks, a hook produces a warning and NO
// .github/hooks/hooks.json file.
func TestCopilot_Preview_HookFlagOff(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/proj/.agents/context.md",
			Body:       "ctx",
		},
		Hooks: []*model.Hook{
			{Event: "PreToolUse", Matcher: "Bash", ScriptPath: "/tmp/proj/.agents/hooks/lint.sh"},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "hooks.json") || strings.Contains(op.Path, "__scope-guard__") || strings.Contains(op.Path, "__perms-guard__") {
			t.Errorf("unexpected hook artifact with flag off: %s", op.Path)
		}
	}
	found := false
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "preview") && strings.Contains(w.Message, "--enable-preview-hooks") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected preview-hint warning when flag is off")
	}
}

// TestCopilot_Preview_HookFlagOn verifies that --enable-preview-hooks
// emits .github/hooks/hooks.json with the user's hook routed through
// the preview event-name mapping (PreToolUse → preToolUse).
func TestCopilot_Preview_HookFlagOn(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/proj/.agents/context.md",
			Body:       "ctx",
		},
		Hooks: []*model.Hook{
			{Event: "PreToolUse", Matcher: "Bash", ScriptPath: "/tmp/proj/.agents/hooks/lint.sh"},
		},
	}
	p := &CopilotPlugin{EnablePreviewHooks: true}
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	hooksPath := filepath.Join(".github", "hooks", "hooks.json")
	hooksOp := findCopilotOp(ops, hooksPath)
	if hooksOp == nil {
		t.Fatalf("missing hooks.json op; got %v", copilotOpPaths(ops))
	}
	if !strings.Contains(hooksOp.Content, `"preToolUse"`) {
		t.Errorf("hooks.json missing preToolUse event:\n%s", hooksOp.Content)
	}
	if !strings.Contains(hooksOp.Content, "lint.sh") {
		t.Errorf("hooks.json missing user script:\n%s", hooksOp.Content)
	}
	if !strings.Contains(hooksOp.Content, "${PROJECT_DIR}") {
		t.Errorf("hooks.json missing ${PROJECT_DIR} for portability:\n%s", hooksOp.Content)
	}
}

// TestCopilot_Preview_PermsFlagOn verifies that --enable-preview-hooks
// projects Permissions through .github/hooks/__perms-guard__/policy.json
// + a gate wrapper, and wires the gate into hooks.json under preToolUse.
func TestCopilot_Preview_PermsFlagOn(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/proj/.agents/context.md",
			Body:       "ctx",
		},
		Permissions: &model.Permissions{
			Allow: []string{"bash:ls *"},
			Deny:  []string{"bash:rm -rf *"},
		},
	}
	p := &CopilotPlugin{EnablePreviewHooks: true}
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPolicy := filepath.Join(".github", "hooks", "__perms-guard__", "policy.json")
	wantGate := filepath.Join(".github", "hooks", "__perms-guard__", "global-gate.sh")
	if findCopilotOp(ops, wantPolicy) == nil {
		t.Fatalf("missing perms policy at %s; got %v", wantPolicy, copilotOpPaths(ops))
	}
	if findCopilotOp(ops, wantGate) == nil {
		t.Fatalf("missing perms gate at %s; got %v", wantGate, copilotOpPaths(ops))
	}
	hooksOp := findCopilotOp(ops, filepath.Join(".github", "hooks", "hooks.json"))
	if hooksOp == nil {
		t.Fatalf("missing hooks.json op")
	}
	if !strings.Contains(hooksOp.Content, "global-gate.sh") {
		t.Errorf("hooks.json missing perms-guard gate wiring:\n%s", hooksOp.Content)
	}
	if !strings.Contains(hooksOp.Content, `"preToolUse"`) {
		t.Errorf("hooks.json missing preToolUse event for gate:\n%s", hooksOp.Content)
	}
}

// TestCopilot_Preview_ScopedHook_WrapperEmitted verifies that with the
// flag on, scoped hooks materialize a __scope-guard__ wrapper just like
// the other plugins, and hooks.json points at the wrapper via PROJECT_DIR.
func TestCopilot_Preview_ScopedHook_WrapperEmitted(t *testing.T) {
	projRoot := "/tmp/proj"
	proj := &model.Project{
		Root:      projRoot,
		AgentsDir: filepath.Join(projRoot, ".agents"),
		Context: &model.Document{
			SourcePath: filepath.Join(projRoot, ".agents", "context.md"),
			Body:       "ctx",
		},
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Edit",
				ScriptPath: filepath.Join(projRoot, ".agents", "src", "billing", "hooks", "verify.sh"),
				ScopePath:  "src/billing",
			},
		},
	}
	p := &CopilotPlugin{EnablePreviewHooks: true}
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wrapperRel := filepath.Join(".github", "hooks", "__scope-guard__", "src-billing-preToolUse-verify.sh")
	if findCopilotOp(ops, wrapperRel) == nil {
		t.Fatalf("missing scope-guard wrapper at %s; got %v", wrapperRel, copilotOpPaths(ops))
	}
	hooksOp := findCopilotOp(ops, filepath.Join(".github", "hooks", "hooks.json"))
	if hooksOp == nil {
		t.Fatalf("missing hooks.json op")
	}
	if !strings.Contains(hooksOp.Content, filepath.ToSlash(wrapperRel)) {
		t.Errorf("hooks.json doesn't reference wrapper %s:\n%s", wrapperRel, hooksOp.Content)
	}
}

// TestCopilot_Preview_CapabilityMatrixFlips verifies the Capabilities()
// matrix reflects the flag: Hooks + Permissions native when on, both
// unsupported when off.
func TestCopilot_Preview_CapabilityMatrixFlips(t *testing.T) {
	on := &CopilotPlugin{EnablePreviewHooks: true}
	off := &CopilotPlugin{}
	if on.Capabilities().Hooks != plugin.SupportNative {
		t.Errorf("flag on Hooks = %v, want native", on.Capabilities().Hooks)
	}
	if on.Capabilities().Permissions != plugin.SupportNative {
		t.Errorf("flag on Permissions = %v, want native", on.Capabilities().Permissions)
	}
	if off.Capabilities().Hooks != plugin.SupportUnsupported {
		t.Errorf("flag off Hooks = %v, want unsupported", off.Capabilities().Hooks)
	}
	if off.Capabilities().Permissions != plugin.SupportUnsupported {
		t.Errorf("flag off Permissions = %v, want unsupported", off.Capabilities().Permissions)
	}
}

// findCopilotOp returns the operation matching path, or nil. Mirrors
// the findOpByPath helper in cline_test.go.
func findCopilotOp(ops []plugin.Operation, path string) *plugin.Operation {
	for i := range ops {
		if ops[i].Path == path {
			return &ops[i]
		}
	}
	return nil
}

// copilotOpPaths returns just the path slice for error messages.
func copilotOpPaths(ops []plugin.Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.Path)
	}
	return out
}
