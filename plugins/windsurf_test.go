package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

func TestWindsurf_RootContext(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "global context",
		},
	}
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	root := ops[0]
	if root.Path != ".windsurf/rules/_root.md" {
		t.Errorf("root op path = %q, want .windsurf/rules/_root.md", root.Path)
	}
	if !strings.HasPrefix(root.Content, "---\n") {
		t.Errorf("root op content does not begin with frontmatter delimiter:\n%s", root.Content)
	}
	if !strings.Contains(root.Content, "trigger: always_on") {
		t.Errorf("root op content missing trigger: always_on\n---\n%s", root.Content)
	}
	if !strings.Contains(root.Content, "global context") {
		t.Errorf("root op content missing body\n---\n%s", root.Content)
	}
	if root.Plugin != "windsurf" {
		t.Errorf("root op plugin = %q, want windsurf", root.Plugin)
	}
	if len(root.Sources) != 1 || root.Sources[0] != "context.md" {
		t.Errorf("root op sources = %v, want [context.md]", root.Sources)
	}
}

func TestWindsurf_Scope(t *testing.T) {
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
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	sc := ops[0]
	if sc.Path != ".windsurf/rules/src-billing.md" {
		t.Errorf("scope op path = %q, want .windsurf/rules/src-billing.md", sc.Path)
	}
	if !strings.Contains(sc.Content, "trigger: glob") {
		t.Errorf("scope op content missing trigger: glob\n---\n%s", sc.Content)
	}
	if !strings.Contains(sc.Content, `globs: ["src/billing/**"]`) {
		t.Errorf("scope op content missing globs\n---\n%s", sc.Content)
	}
	if !strings.Contains(sc.Content, "description: Stripe webhook context") {
		t.Errorf("scope op content missing description\n---\n%s", sc.Content)
	}
	if !strings.Contains(sc.Content, "billing context") {
		t.Errorf("scope op content missing body\n---\n%s", sc.Content)
	}
	if len(sc.Sources) != 1 || sc.Sources[0] != "src/billing/context.md" {
		t.Errorf("scope op sources = %v, want [src/billing/context.md]", sc.Sources)
	}
}

func TestWindsurf_Skill_WithGlobs(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "Stripe Webhook Validator",
				Description: "Validate Stripe webhook signatures",
				Globs:       []string{"src/billing/**", "tests/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/stripe-webhook/SKILL.md",
					Body:       "validation steps",
				},
				Scripts: []string{"verify.sh"},
			},
		},
	}
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".windsurf/rules/skill-stripe-webhook-validator.md" {
		t.Errorf("skill op path = %q, want .windsurf/rules/skill-stripe-webhook-validator.md", op.Path)
	}
	if !strings.Contains(op.Content, "trigger: glob") {
		t.Errorf("skill op content missing trigger: glob\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `globs: ["src/billing/**","tests/billing/**"]`) {
		t.Errorf("skill op content missing globs\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "description: Validate Stripe webhook signatures") {
		t.Errorf("skill op content missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "validation steps") {
		t.Errorf("skill op content missing body\n---\n%s", op.Content)
	}
	// Scripts → warning attached to the skill op.
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no script execution") && strings.Contains(w.Message, "verify.sh") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected script warning on skill op, got %#v", op.Warnings)
	}
}

func TestWindsurf_Skill_NoGlobs(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "Pure Skill",
				Description: "Triggered when the user asks about purity",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/pure/SKILL.md",
					Body:       "pure body",
				},
			},
		},
	}
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".windsurf/rules/skill-pure-skill.md" {
		t.Errorf("skill op path = %q", op.Path)
	}
	if !strings.Contains(op.Content, "trigger: model_decision") {
		t.Errorf("skill op content missing trigger: model_decision\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "description: Triggered when the user asks about purity") {
		t.Errorf("skill op content missing description\n---\n%s", op.Content)
	}
	if strings.Contains(op.Content, "globs:") {
		t.Errorf("skill op should not have globs field\n---\n%s", op.Content)
	}
	if len(op.Warnings) != 0 {
		t.Errorf("expected no warnings (no scripts), got %#v", op.Warnings)
	}
}

func TestWindsurf_AgentWarning(t *testing.T) {
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
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	// Only the root context op; no agent file.
	if len(ops) != 1 {
		t.Fatalf("expected 1 op (no agent file), got %d", len(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "agents") || strings.Contains(op.Path, "code-reviewer") {
			t.Errorf("unexpected agent op: %s", op.Path)
		}
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no subagent primitive") && strings.Contains(w.Message, "code-reviewer") {
			found = true
			if w.Severity != "info" {
				t.Errorf("agent warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected agent warning, got %#v", ops[0].Warnings)
	}
}

func TestWindsurf_UnknownModeErrors(t *testing.T) {
	p := NewWindsurf()
	proj := &model.Project{AgentsDir: "/tmp/.agents"}
	_, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err == nil {
		t.Fatalf("expected error for unknown mode")
	}
}

func TestWindsurf_DetectMarkers(t *testing.T) {
	p := NewWindsurf()

	// Empty directory → false.
	empty := t.TempDir()
	if p.Detect(empty) {
		t.Errorf("Detect(empty) = true, want false")
	}

	// .windsurf/ dir → true.
	withDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withDir, ".windsurf"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !p.Detect(withDir) {
		t.Errorf("Detect(withDir) = false, want true")
	}

	// .windsurfrules file → true.
	withFile := t.TempDir()
	if err := os.WriteFile(filepath.Join(withFile, ".windsurfrules"), []byte("legacy rules"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !p.Detect(withFile) {
		t.Errorf("Detect(withFile) = false, want true")
	}
}
