package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"

	"gopkg.in/yaml.v3"
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
	if !strings.Contains(sc.Content, "description: \"Stripe webhook context\"") {
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
	if !strings.Contains(op.Content, "description: \"Validate Stripe webhook signatures\"") {
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
	if !strings.Contains(op.Content, "description: \"Triggered when the user asks about purity\"") {
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

func TestWindsurf_Skill_DescriptionWithColon_YAMLValid(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "Deploy Skill",
				Description: "Ship a release: cuts a tag, builds artifacts, publishes",
				Globs:       []string{"deploy/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/deploy/SKILL.md",
					Body:       "steps",
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
	parts := strings.SplitN(ops[0].Content, "---\n", 3)
	if len(parts) < 3 {
		t.Fatalf("frontmatter delimiters missing in:\n%s", ops[0].Content)
	}
	var fm struct {
		Trigger     string   `yaml:"trigger"`
		Globs       []string `yaml:"globs"`
		Description string   `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		t.Fatalf("frontmatter is not valid YAML (the v0.7 colon-in-description bug): %v\n---\n%s", err, parts[1])
	}
	if fm.Description != "Ship a release: cuts a tag, builds artifacts, publishes" {
		t.Errorf("description = %q after YAML round-trip, want full string", fm.Description)
	}
}

// TestWindsurf_Hooks_Emit verifies that proj.Hooks → .windsurf/hooks.json
// with the expected event mapping, JSON schema, and that the Merger
// preserves user-authored top-level keys on re-projection.
func TestWindsurf_Hooks_Emit(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/proj/.agents/hooks/lint.sh",
			},
			{
				Event:      "PreToolUse",
				Matcher:    "Read",
				ScriptPath: "/tmp/proj/.agents/hooks/audit-read.sh",
			},
			{
				Event:      "PreToolUse",
				Matcher:    "Write",
				ScriptPath: "/tmp/proj/.agents/hooks/audit-write.sh",
			},
			{
				Event:      "PostToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/proj/.agents/hooks/post-run.sh",
			},
			{
				// Canonical Windsurf event name → pass-through.
				Event:      "post_cascade_response",
				ScriptPath: "/tmp/proj/.agents/hooks/transcript.sh",
			},
			{
				// Claude UserPromptSubmit → pre_user_prompt.
				Event:      "UserPromptSubmit",
				ScriptPath: "/tmp/proj/.agents/hooks/prompt-cap.sh",
			},
		},
	}
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	var hooksOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".windsurf/hooks.json" {
			hooksOp = &ops[i]
			break
		}
	}
	if hooksOp == nil {
		t.Fatalf(".windsurf/hooks.json op missing; got paths: %s", opsPaths(ops))
	}
	if hooksOp.Kind != plugin.OpMerge {
		t.Errorf("hooks op Kind = %q, want %q", hooksOp.Kind, plugin.OpMerge)
	}
	if hooksOp.Merger == nil {
		t.Fatalf("hooks op missing Merger closure")
	}
	if hooksOp.Plugin != "windsurf" {
		t.Errorf("hooks op Plugin = %q, want windsurf", hooksOp.Plugin)
	}

	// Render the merge against nil to inspect the emitted JSON.
	rendered, err := hooksOp.Merger(nil)
	if err != nil {
		t.Fatalf("Merger error: %v", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Command    string `json:"command"`
			ShowOutput bool   `json:"show_output"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered hooks.json is not valid JSON: %v\n---\n%s", err, rendered)
	}

	want := map[string]string{
		"pre_run_command":       "lint.sh",
		"pre_read_code":         "audit-read.sh",
		"pre_write_code":        "audit-write.sh",
		"post_run_command":      "post-run.sh",
		"post_cascade_response": "transcript.sh",
		"pre_user_prompt":       "prompt-cap.sh",
	}
	for event, wantBasename := range want {
		entries, ok := doc.Hooks[event]
		if !ok {
			t.Errorf("expected event %q in hooks.json, present: %v", event, mapKeys(doc.Hooks))
			continue
		}
		if len(entries) == 0 {
			t.Errorf("event %q has no entries", event)
			continue
		}
		if !strings.Contains(entries[0].Command, wantBasename) {
			t.Errorf("event %q command = %q, want to contain %q", event, entries[0].Command, wantBasename)
		}
		// Hooks should be invoked via `bash <path>` for cross-platform safety.
		if !strings.HasPrefix(entries[0].Command, "bash ") {
			t.Errorf("event %q command should start with 'bash ', got %q", event, entries[0].Command)
		}
	}

	// Re-merge: feed back the rendered output as "existing" and add a
	// user-authored top-level key. The Merger MUST preserve it.
	withUser := strings.Replace(rendered, "{\n", "{\n  \"custom_user_key\": \"keep-me\",\n", 1)
	merged, err := hooksOp.Merger([]byte(withUser))
	if err != nil {
		t.Fatalf("re-merge error: %v", err)
	}
	if !strings.Contains(merged, "custom_user_key") || !strings.Contains(merged, "keep-me") {
		t.Errorf("Merger dropped user-authored top-level key:\n%s", merged)
	}
}

// TestWindsurf_Hooks_UnmappableEventWarns verifies that a prism Hook with
// an event Windsurf cannot express (e.g. SessionStart) is dropped with an
// info-severity warning rather than crashing or silently disappearing.
func TestWindsurf_Hooks_UnmappableEventWarns(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/proj/.agents/context.md",
			Body:       "ctx",
		},
		Hooks: []*model.Hook{
			{
				Event:      "SessionStart",
				ScriptPath: "/tmp/proj/.agents/hooks/session-start.sh",
			},
		},
	}
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	for _, op := range ops {
		if op.Path == ".windsurf/hooks.json" {
			t.Errorf("expected no hooks.json op when every hook is unmappable, got one")
		}
	}
	found := false
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "SessionStart") && w.Severity == "info" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected info warning for unmappable SessionStart hook, got ops=%s", opsPaths(ops))
	}
}

// TestWindsurf_MCP_Projects verifies that proj.MCP → .windsurf/mcp_config.json
// using the standard {mcpServers: {...}} schema, that the canonical-path
// warning is attached, and that the Merger preserves user-authored entries
// (both top-level keys and unrelated entries under mcpServers).
func TestWindsurf_MCP_Projects(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		MCP: []*model.MCPServer{
			{
				Name:    "stripe",
				Command: "node",
				Args:    []string{"--", "stripe-mcp.js"},
				Env:     map[string]string{"STRIPE_KEY": "sk_test"},
			},
			{
				Name: "remote-sse",
				URL:  "https://example.com/mcp",
			},
		},
	}
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".windsurf/mcp_config.json" {
			mcpOp = &ops[i]
			break
		}
	}
	if mcpOp == nil {
		t.Fatalf(".windsurf/mcp_config.json op missing; got paths: %s", opsPaths(ops))
	}
	if mcpOp.Kind != plugin.OpMerge {
		t.Errorf("mcp op Kind = %q, want %q", mcpOp.Kind, plugin.OpMerge)
	}
	if mcpOp.Merger == nil {
		t.Fatalf("mcp op missing Merger closure")
	}

	// Canonical-path warning must be present.
	hasCanonical := false
	for _, w := range mcpOp.Warnings {
		if strings.Contains(w.Message, "~/.codeium/windsurf/mcp_config.json") {
			hasCanonical = true
			if w.Severity != "info" {
				t.Errorf("canonical-path warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !hasCanonical {
		t.Errorf("expected canonical-path warning on MCP op, got %#v", mcpOp.Warnings)
	}

	// Render the merge against nil and verify the schema.
	rendered, err := mcpOp.Merger(nil)
	if err != nil {
		t.Fatalf("Merger error: %v", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered mcp_config.json is not valid JSON: %v\n---\n%s", err, rendered)
	}
	stripe, ok := doc.MCPServers["stripe"]
	if !ok {
		t.Fatalf("stripe server missing from mcp_config.json:\n%s", rendered)
	}
	if stripe.Command != "node" {
		t.Errorf("stripe command = %q, want node", stripe.Command)
	}
	if len(stripe.Args) != 2 || stripe.Args[1] != "stripe-mcp.js" {
		t.Errorf("stripe args = %v, want [-- stripe-mcp.js]", stripe.Args)
	}
	if stripe.Env["STRIPE_KEY"] != "sk_test" {
		t.Errorf("stripe env missing STRIPE_KEY: %v", stripe.Env)
	}
	sse, ok := doc.MCPServers["remote-sse"]
	if !ok {
		t.Fatalf("remote-sse server missing from mcp_config.json:\n%s", rendered)
	}
	if sse.URL != "https://example.com/mcp" {
		t.Errorf("remote-sse url = %q, want https://example.com/mcp", sse.URL)
	}

	// Re-merge: simulate a user-authored mcp_config.json that has its own
	// server plus an unrelated top-level key. Both must survive.
	existing := `{
  "user_custom": "preserve-me",
  "mcpServers": {
    "user-authored": {"command": "/usr/local/bin/my-mcp"}
  }
}`
	merged, err := mcpOp.Merger([]byte(existing))
	if err != nil {
		t.Fatalf("re-merge error: %v", err)
	}
	if !strings.Contains(merged, "user_custom") || !strings.Contains(merged, "preserve-me") {
		t.Errorf("Merger dropped user-authored top-level key:\n%s", merged)
	}
	if !strings.Contains(merged, "user-authored") || !strings.Contains(merged, "/usr/local/bin/my-mcp") {
		t.Errorf("Merger dropped user-authored mcpServers entry:\n%s", merged)
	}
	// And the new entries must be present.
	if !strings.Contains(merged, "stripe") || !strings.Contains(merged, "remote-sse") {
		t.Errorf("Merger dropped projected entries:\n%s", merged)
	}
}

// TestWindsurf_MCP_ScopedWarning verifies that a scoped MCP server triggers
// the per-scope info warning (Windsurf has no per-scope MCP) in addition
// to the canonical-path warning.
func TestWindsurf_MCP_ScopedWarning(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		MCP: []*model.MCPServer{
			{
				Name:      "billing-db",
				Command:   "psql",
				ScopePath: "src/billing",
			},
		},
	}
	p := NewWindsurf()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".windsurf/mcp_config.json" {
			mcpOp = &ops[i]
		}
	}
	if mcpOp == nil {
		t.Fatalf(".windsurf/mcp_config.json op missing")
	}
	found := false
	for _, w := range mcpOp.Warnings {
		if strings.Contains(w.Message, "no per-scope MCP") && strings.Contains(w.Message, "src/billing") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected per-scope MCP warning, got %#v", mcpOp.Warnings)
	}
}

// TestWindsurf_ScopedHook_WrapperEmitted verifies that a scoped hook
// produces (1) a wrapper script under .windsurf/hooks/__scope-guard__/,
// (2) a hooks.json entry whose command points at the wrapper's absolute
// path (not the source script), and (3) NO "no hook primitive" warning
// (since Windsurf hooks are now Native).
func TestWindsurf_ScopedHook_WrapperEmitted(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
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

	// (1) Wrapper script under .windsurf/hooks/__scope-guard__/.
	var wrapperOp *plugin.Operation
	for i := range ops {
		if strings.HasPrefix(ops[i].Path, ".windsurf/hooks/__scope-guard__/") {
			wrapperOp = &ops[i]
		}
	}
	if wrapperOp == nil {
		t.Fatalf("scope-guard wrapper op missing; got paths: %s", opsPaths(ops))
	}
	if wrapperOp.FileMode == 0 {
		t.Errorf("wrapper op FileMode = 0, want 0755 (executable)")
	}
	if !strings.Contains(wrapperOp.Content, "prism scope-guard") {
		t.Errorf("wrapper script missing prism scope-guard exec line:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "src/billing") {
		t.Errorf("wrapper script missing scope path:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "audit.sh") {
		t.Errorf("wrapper script missing source script reference:\n%s", wrapperOp.Content)
	}

	// (2) hooks.json entry points at the wrapper's absolute path (not the
	// source script path).
	var hooksOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".windsurf/hooks.json" {
			hooksOp = &ops[i]
		}
	}
	if hooksOp == nil {
		t.Fatalf(".windsurf/hooks.json op missing; got paths: %s", opsPaths(ops))
	}
	rendered, err := hooksOp.Merger(nil)
	if err != nil {
		t.Fatalf("Merger error: %v", err)
	}
	wantWrapperAbs := "/tmp/proj/.windsurf/hooks/__scope-guard__/src-billing-audit.sh"
	if !strings.Contains(rendered, wantWrapperAbs) {
		t.Errorf("hooks.json should reference wrapper at %s, got:\n%s", wantWrapperAbs, rendered)
	}
	if strings.Contains(rendered, "/tmp/proj/.agents/src/billing/hooks/audit.sh") {
		t.Errorf("hooks.json should NOT reference the source script directly (must go through wrapper):\n%s", rendered)
	}

	// (3) No "no hook primitive" warning (legacy behavior, now Native).
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "no hook primitive") {
				t.Errorf("unexpected legacy 'no hook primitive' warning: %s", w.Message)
			}
		}
	}
}

// TestWindsurf_ScopedHook_DisableWrappers verifies the DisableHookWrappers
// knob: when true, no wrapper script is emitted and hooks.json points
// directly at the source script (matching the Claude behavior).
func TestWindsurf_ScopedHook_DisableWrappers(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/proj/.agents/src/billing/hooks/audit.sh",
				ScopePath:  "src/billing",
			},
		},
	}
	p := &WindsurfPlugin{DisableHookWrappers: true}
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	for _, op := range ops {
		if strings.HasPrefix(op.Path, ".windsurf/hooks/__scope-guard__/") {
			t.Errorf("wrapper emitted despite DisableHookWrappers=true: %s", op.Path)
		}
	}
	var hooksOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".windsurf/hooks.json" {
			hooksOp = &ops[i]
		}
	}
	if hooksOp == nil {
		t.Fatalf(".windsurf/hooks.json op missing")
	}
	rendered, err := hooksOp.Merger(nil)
	if err != nil {
		t.Fatalf("Merger error: %v", err)
	}
	if !strings.Contains(rendered, "/tmp/proj/.agents/src/billing/hooks/audit.sh") {
		t.Errorf("hooks.json should reference source script directly when wrappers disabled:\n%s", rendered)
	}
}

// TestWindsurf_Capabilities_HooksAndMCPNative verifies the capability
// matrix flip from Unsupported to Native for both Hooks and MCP. Guards
// against accidental regressions.
func TestWindsurf_Capabilities_HooksAndMCPNative(t *testing.T) {
	caps := NewWindsurf().Capabilities()
	if caps.Hooks != plugin.SupportNative {
		t.Errorf("Hooks = %q, want native", caps.Hooks)
	}
	if caps.MCP != plugin.SupportNative {
		t.Errorf("MCP = %q, want native", caps.MCP)
	}
	// Unchanged neighbors — guard against accidental flips of the other
	// capability cells.
	if caps.Agents != plugin.SupportUnsupported {
		t.Errorf("Agents = %q, want unsupported", caps.Agents)
	}
	if caps.Permissions != plugin.SupportUnsupported {
		t.Errorf("Permissions = %q, want unsupported", caps.Permissions)
	}
}

// mapKeys returns the keys of a string-keyed map in sorted order (test
// helper for diagnostic output only).
func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
