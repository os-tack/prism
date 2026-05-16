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

// TestWindsurf_ScopedSkill verifies that a Skill with ScopePath produces a
// scoped rule file with the scope slug as a filename prefix, trigger: glob
// (because Skill.Globs is populated by the parser), and the source path in
// the lockfile tag.
func TestWindsurf_ScopedSkill(t *testing.T) {
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
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".windsurf/rules/skill-src-billing-audit-trail.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, "trigger: glob") {
		t.Errorf("missing trigger: glob\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `globs: ["src/billing/**"]`) {
		t.Errorf("missing globs frontmatter\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || !strings.Contains(op.Sources[0], "src/billing/skills/audit-trail/SKILL.md") {
		t.Errorf("Sources = %v, want path containing src/billing/skills/audit-trail/SKILL.md", op.Sources)
	}
}

// TestWindsurf_ScopedSkillCollision verifies that the same skill name in two
// different scopes produces two distinct files, one per scope, with
// different filename prefixes.
func TestWindsurf_ScopedSkillCollision(t *testing.T) {
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
	p := NewWindsurf()
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
	if !paths[".windsurf/rules/skill-src-billing-validator.md"] {
		t.Errorf("missing billing-validator op, got paths: %v", paths)
	}
	if !paths[".windsurf/rules/skill-src-auth-validator.md"] {
		t.Errorf("missing auth-validator op, got paths: %v", paths)
	}
}

// TestWindsurf_ScopedCommand_Warning verifies that a scoped command produces
// a rule file with scope slug prefix and an info warning naming the scope.
func TestWindsurf_ScopedCommand_Warning(t *testing.T) {
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
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".windsurf/rules/command-src-billing-deploy.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, "trigger: model_decision") {
		t.Errorf("missing trigger: model_decision\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "src/billing") {
		t.Errorf("description should mention scope path\n---\n%s", op.Content)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no slash-command mechanism") && strings.Contains(w.Message, "src/billing") {
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

// TestWindsurf_ScopedHook_Warning verifies that a scoped hook produces no
// file but emits an info warning naming the scope.
func TestWindsurf_ScopedHook_Warning(t *testing.T) {
	proj := &model.Project{
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
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
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
			if strings.Contains(w.Message, "no hook primitive") && strings.Contains(w.Message, "src/billing") && strings.Contains(w.Message, "PreToolUse:Bash") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected scoped-hook warning, got ops=%#v", ops)
	}
}
