package plugins

import (
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

// TestCopilot_AgentWarning verifies that an Agent produces no file but
// emits an info warning attached to whatever the first op happens to be.
func TestCopilot_AgentWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Agents: []*model.Agent{
			{
				Name:        "code-reviewer",
				Description: "Reviews code",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/code-reviewer.md",
				},
			},
		},
	}
	p := NewCopilot()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "write"})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	// Only the root context op exists.
	if len(ops) != 1 {
		t.Fatalf("expected 1 op (no agent file), got %d", len(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "/agents/") || strings.Contains(op.Path, "code-reviewer") {
			t.Errorf("unexpected agent file: %s", op.Path)
		}
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no subagent primitive") && strings.Contains(w.Message, "code-reviewer") {
			found = true
			if w.Severity != "info" {
				t.Errorf("warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected agent warning, got %#v", ops[0].Warnings)
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
	if caps.Agents != plugin.SupportUnsupported {
		t.Errorf("Agents = %v, want unsupported", caps.Agents)
	}
	if caps.Hooks != plugin.SupportUnsupported {
		t.Errorf("Hooks = %v, want unsupported", caps.Hooks)
	}
	if caps.Permissions != plugin.SupportUnsupported {
		t.Errorf("Permissions = %v, want unsupported", caps.Permissions)
	}
	if caps.MCP != plugin.SupportUnsupported {
		t.Errorf("MCP = %v, want unsupported", caps.MCP)
	}
}
