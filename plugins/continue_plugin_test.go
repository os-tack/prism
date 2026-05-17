package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// TestContinue_RootContext verifies that a project with a root Context
// produces a `.continue/rules/_root.md` op with `alwaysApply: true`.
func TestContinue_RootContext(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "global continue context",
		},
	}
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".continue/rules/_root.md" {
		t.Errorf("path = %q, want .continue/rules/_root.md", op.Path)
	}
	if op.Plugin != "continue" {
		t.Errorf("plugin = %q, want continue", op.Plugin)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if op.Mode != plugin.ModeWrite {
		t.Errorf("mode = %v, want ModeWrite", op.Mode)
	}
	if !strings.HasPrefix(op.Content, "---\n") {
		t.Errorf("content does not begin with frontmatter delimiter:\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "alwaysApply: true") {
		t.Errorf("content missing alwaysApply: true\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "description: \"Project-wide context\"") {
		t.Errorf("content missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "global continue context") {
		t.Errorf("content missing body\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "context.md" {
		t.Errorf("sources = %v, want [context.md]", op.Sources)
	}
}

// TestContinue_Scope verifies that a scope projects to
// `.continue/rules/<slug>.md` with description + globs frontmatter.
func TestContinue_Scope(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:        "src/billing",
				Globs:       []string{"src/billing/**"},
				Description: "Stripe webhook context",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/context.md",
					Body:       "billing context body",
				},
			},
		},
	}
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".continue/rules/src-billing.md" {
		t.Errorf("path = %q, want .continue/rules/src-billing.md", op.Path)
	}
	if !strings.Contains(op.Content, "description: \"Stripe webhook context\"") {
		t.Errorf("missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `globs: ["src/billing/**"]`) {
		t.Errorf("missing globs frontmatter\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "billing context body") {
		t.Errorf("missing body\n---\n%s", op.Content)
	}
	if strings.Contains(op.Content, "alwaysApply") {
		t.Errorf("scope op should not have alwaysApply\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "src/billing/context.md" {
		t.Errorf("sources = %v, want [src/billing/context.md]", op.Sources)
	}
}

// TestContinue_Skill verifies that a Skill projects to a scoped rule
// file at `.continue/rules/skill-<slug>.md` with description + globs.
func TestContinue_Skill(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "pdf-editing",
				Description: "Edit PDF content programmatically",
				Globs:       []string{"**/*.pdf"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/pdf-editing/SKILL.md",
					Body:       "How to edit a PDF",
				},
			},
		},
	}
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".continue/rules/skill-pdf-editing.md" {
		t.Errorf("path = %q, want .continue/rules/skill-pdf-editing.md", op.Path)
	}
	if !strings.Contains(op.Content, "description: \"Edit PDF content programmatically\"") {
		t.Errorf("missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, `globs: ["**/*.pdf"]`) {
		t.Errorf("missing globs\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "How to edit a PDF") {
		t.Errorf("missing body\n---\n%s", op.Content)
	}
	if len(op.Warnings) != 0 {
		t.Errorf("expected no warnings (no scripts), got %#v", op.Warnings)
	}
}

// TestContinue_Skill_ScriptsWarning verifies that a skill with scripts
// gets a warning attached to its rule op.
func TestContinue_Skill_ScriptsWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "stripe-webhook",
				Description: "Validate webhooks",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/stripe-webhook/SKILL.md",
					Body:       "body",
				},
				Scripts: []string{"verify.sh", "diagnose.py"},
			},
		},
	}
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no script execution") &&
			strings.Contains(w.Message, "verify.sh") &&
			strings.Contains(w.Message, "diagnose.py") {
			found = true
			if w.Severity != "info" {
				t.Errorf("warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected scripts warning, got %#v", ops[0].Warnings)
	}
}

// TestContinue_MCP verifies that each MCP server projects to its own
// `.continue/mcpServers/<slug>.yaml` file with only non-empty fields.
func TestContinue_MCP(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
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
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	// Collect MCP ops.
	var mcpOps []plugin.Operation
	for _, op := range ops {
		if strings.HasPrefix(op.Path, ".continue/mcpServers/") {
			mcpOps = append(mcpOps, op)
		}
	}
	if len(mcpOps) != 2 {
		t.Fatalf("expected 2 mcpServer ops, got %d (paths: %v)", len(mcpOps), opsPaths(ops))
	}

	// Index by path.
	byPath := map[string]plugin.Operation{}
	for _, op := range mcpOps {
		byPath[op.Path] = op
	}

	linearOp, ok := byPath[".continue/mcpServers/linear.yaml"]
	if !ok {
		t.Fatalf("missing linear op; have %v", opsPaths(mcpOps))
	}
	if linearOp.Kind != plugin.OpWrite {
		t.Errorf("linear kind = %v, want OpWrite", linearOp.Kind)
	}
	if linearOp.Mode != plugin.ModeWrite {
		t.Errorf("linear mode = %v, want ModeWrite", linearOp.Mode)
	}
	if linearOp.Plugin != "continue" {
		t.Errorf("linear plugin = %q, want continue", linearOp.Plugin)
	}
	if len(linearOp.Sources) != 1 || linearOp.Sources[0] != "mcp.yaml" {
		t.Errorf("linear sources = %v, want [mcp.yaml]", linearOp.Sources)
	}

	// Parse linear YAML and verify fields.
	type mcpFile struct {
		Name    string            `yaml:"name"`
		Command string            `yaml:"command"`
		Args    []string          `yaml:"args"`
		Env     map[string]string `yaml:"env"`
		URL     string            `yaml:"url"`
	}
	var linearParsed mcpFile
	if err := yaml.Unmarshal([]byte(linearOp.Content), &linearParsed); err != nil {
		t.Fatalf("parse linear yaml: %v\n---\n%s", err, linearOp.Content)
	}
	if linearParsed.Name != "linear" {
		t.Errorf("linear.name = %q, want linear", linearParsed.Name)
	}
	if linearParsed.Command != "npx" {
		t.Errorf("linear.command = %q, want npx", linearParsed.Command)
	}
	if len(linearParsed.Args) != 2 || linearParsed.Args[0] != "-y" || linearParsed.Args[1] != "@linear/mcp" {
		t.Errorf("linear.args = %v, want [-y @linear/mcp]", linearParsed.Args)
	}
	if linearParsed.Env["LINEAR_TOKEN"] != "xxx" {
		t.Errorf("linear.env = %v, want {LINEAR_TOKEN: xxx}", linearParsed.Env)
	}
	if linearParsed.URL != "" {
		t.Errorf("linear.url should be empty, got %q", linearParsed.URL)
	}

	// Remote-tools: URL only.
	remoteOp, ok := byPath[".continue/mcpServers/remote-tools.yaml"]
	if !ok {
		t.Fatalf("missing remote-tools op; have %v", opsPaths(mcpOps))
	}
	var remoteParsed mcpFile
	if err := yaml.Unmarshal([]byte(remoteOp.Content), &remoteParsed); err != nil {
		t.Fatalf("parse remote yaml: %v\n---\n%s", err, remoteOp.Content)
	}
	if remoteParsed.Name != "remote-tools" {
		t.Errorf("remote.name = %q, want remote-tools", remoteParsed.Name)
	}
	if remoteParsed.URL != "https://mcp.example.com/sse" {
		t.Errorf("remote.url = %q", remoteParsed.URL)
	}
	if remoteParsed.Command != "" {
		t.Errorf("remote.command should be empty, got %q", remoteParsed.Command)
	}
	if len(remoteParsed.Args) != 0 {
		t.Errorf("remote.args should be empty, got %v", remoteParsed.Args)
	}
	if len(remoteParsed.Env) != 0 {
		t.Errorf("remote.env should be empty, got %v", remoteParsed.Env)
	}

	// The raw remote YAML should not contain the omitted keys at all.
	if strings.Contains(remoteOp.Content, "command:") {
		t.Errorf("remote yaml should omit empty command:\n%s", remoteOp.Content)
	}
	if strings.Contains(remoteOp.Content, "args:") {
		t.Errorf("remote yaml should omit empty args:\n%s", remoteOp.Content)
	}
	if strings.Contains(remoteOp.Content, "env:") {
		t.Errorf("remote yaml should omit empty env:\n%s", remoteOp.Content)
	}
}

// TestContinue_AgentWarning verifies that a project with an Agent emits a
// warning (attached to another op) and no agent file.
func TestContinue_AgentWarning(t *testing.T) {
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
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "agents") {
			t.Errorf("unexpected agent file op: %s", op.Path)
		}
	}
	// Warning must be attached somewhere.
	found := false
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "no subagent primitive") &&
				strings.Contains(w.Message, "code-reviewer") &&
				strings.Contains(w.Message, "not projected") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected agent warning, got ops=%#v", ops)
	}
}

// TestContinue_UnknownModeErrors verifies that requesting a non-write
// mode (e.g. symlink) returns an error.
func TestContinue_UnknownModeErrors(t *testing.T) {
	p := NewContinue()
	proj := &model.Project{AgentsDir: "/tmp/.agents"}
	_, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err == nil {
		t.Fatalf("expected error for unsupported mode")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention the bad mode, got %q", err.Error())
	}
}

// TestContinue_DetectMarkers verifies that Detect responds to `.continue/`.
func TestContinue_DetectMarkers(t *testing.T) {
	p := NewContinue()

	// Empty dir → false.
	empty := t.TempDir()
	if p.Detect(empty) {
		t.Errorf("Detect(empty) = true, want false")
	}

	// With `.continue/` dir → true.
	withContinue := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withContinue, ".continue"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !p.Detect(withContinue) {
		t.Errorf("Detect(withContinue) = false, want true")
	}
}

// opsPaths is a small helper to surface op paths in failure messages.
func opsPaths(ops []plugin.Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.Path)
	}
	return out
}

// TestContinue_ScopedSkill verifies that a Skill with ScopePath produces a
// scoped rule file with the scope slug as a filename prefix, the populated
// globs in frontmatter, and the source path in the lockfile tag.
func TestContinue_ScopedSkill(t *testing.T) {
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
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".continue/rules/skill-src-billing-audit-trail.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, `globs: ["src/billing/**"]`) {
		t.Errorf("missing globs frontmatter\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || !strings.Contains(op.Sources[0], "src/billing/skills/audit-trail/SKILL.md") {
		t.Errorf("Sources = %v, want path containing src/billing/skills/audit-trail/SKILL.md", op.Sources)
	}
}

// TestContinue_ScopedSkillCollision verifies that the same skill name in two
// different scopes produces two distinct files, one per scope.
func TestContinue_ScopedSkillCollision(t *testing.T) {
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
	p := NewContinue()
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
	if !paths[".continue/rules/skill-src-billing-validator.md"] {
		t.Errorf("missing billing-validator op, got paths: %v", paths)
	}
	if !paths[".continue/rules/skill-src-auth-validator.md"] {
		t.Errorf("missing auth-validator op, got paths: %v", paths)
	}
}

// TestContinue_ScopedCommand_Warning verifies that a scoped command produces
// a rule file under .continue/rules/ with the scope slug prefix and an info
// warning naming the scope.
func TestContinue_ScopedCommand_Warning(t *testing.T) {
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
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".continue/rules/command-src-billing-deploy.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, `globs: ["src/billing/**"]`) {
		t.Errorf("missing scope default globs in frontmatter\n---\n%s", op.Content)
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

// TestContinue_ScopedHook_Warning verifies that a scoped hook produces no
// file but emits an info warning naming the scope.
func TestContinue_ScopedHook_Warning(t *testing.T) {
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
	p := NewContinue()
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

// TestContinue_PermsGuard_GlobalNoHooks verifies that a global Permissions
// block with no hooks projects a sidecar policy + a bare gate wrapper
// under .continue/hooks/__perms-guard__/.
func TestContinue_PermsGuard_GlobalNoHooks(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "x",
		},
		Permissions: &model.Permissions{
			Allow: []string{"bash:ls *"},
			Deny:  []string{"bash:rm -rf *"},
		},
	}
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	paths := map[string]plugin.Operation{}
	for _, op := range ops {
		paths[op.Path] = op
	}
	wantPolicy := filepath.Join(".continue", "hooks", "__perms-guard__", "policy.json")
	wantGate := filepath.Join(".continue", "hooks", "__perms-guard__", "global-gate.sh")
	if _, ok := paths[wantPolicy]; !ok {
		t.Fatalf("missing policy at %q; have: %v", wantPolicy, opsPaths(ops))
	}
	if _, ok := paths[wantGate]; !ok {
		t.Fatalf("missing gate at %q; have: %v", wantGate, opsPaths(ops))
	}
	if !strings.Contains(paths[wantPolicy].Content, `"bash:rm -rf *"`) {
		t.Errorf("policy content missing deny:\n%s", paths[wantPolicy].Content)
	}
	if !strings.Contains(paths[wantGate].Content, "prism perms-guard") {
		t.Errorf("gate doesn't exec perms-guard:\n%s", paths[wantGate].Content)
	}
}

// TestContinue_PermsGuard_ScopedWithHook verifies scoped permissions +
// scoped hook produces a scoped sidecar + wrapper that wires the source
// script to the scope's policy.
func TestContinue_PermsGuard_ScopedWithHook(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/fake/.agents/src/billing/hooks/audit.sh",
				ScopePath:  "src/billing",
			},
		},
		ScopedPermissions: []*model.Permissions{
			{ScopePath: "src/billing", Deny: []string{"bash:rm *"}},
		},
	}
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	paths := map[string]plugin.Operation{}
	for _, op := range ops {
		paths[op.Path] = op
	}
	wantPolicy := filepath.Join(".continue", "hooks", "__perms-guard__", "src-billing.policy.json")
	wantWrapper := filepath.Join(".continue", "hooks", "__perms-guard__", "src-billing-PreToolUse-audit.sh")
	if _, ok := paths[wantPolicy]; !ok {
		t.Fatalf("missing scoped policy at %q; have: %v", wantPolicy, opsPaths(ops))
	}
	wrapperOp, ok := paths[wantWrapper]
	if !ok {
		t.Fatalf("missing scoped wrapper at %q; have: %v", wantWrapper, opsPaths(ops))
	}
	if !strings.Contains(wrapperOp.Content, "src-billing.policy.json") {
		t.Errorf("wrapper missing scoped-policy reference:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "audit.sh") {
		t.Errorf("wrapper missing source script:\n%s", wrapperOp.Content)
	}
	if wrapperOp.FileMode != 0o755 {
		t.Errorf("wrapper FileMode = %o, want 0755", wrapperOp.FileMode)
	}
}

// TestContinue_PermsGuard_DisableHookWrappers verifies the disable knob
// suppresses wrapper + policy emission entirely.
func TestContinue_PermsGuard_DisableHookWrappers(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Permissions: &model.Permissions{
			Allow: []string{"bash:ls *"},
		},
	}
	p := &ContinuePlugin{DisableHookWrappers: true}
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "__perms-guard__") {
			t.Errorf("unexpected perms-guard artifact with DisableHookWrappers=true: %s", op.Path)
		}
	}
}

// TestContinue_PermsGuard_CapabilityNative verifies the matrix entry.
func TestContinue_PermsGuard_CapabilityNative(t *testing.T) {
	p := NewContinue()
	if got := p.Capabilities().Permissions; got != plugin.SupportNative {
		t.Errorf("Capabilities().Permissions = %v, want SupportNative", got)
	}
}

func TestContinue_Skill_DescriptionWithColon_YAMLValid(t *testing.T) {
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
	p := NewContinue()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) == 0 {
		t.Fatalf("expected at least 1 op")
	}
	var skillOp *plugin.Operation
	for i := range ops {
		if strings.HasSuffix(ops[i].Path, "skill-deploy-skill.md") {
			skillOp = &ops[i]
			break
		}
	}
	if skillOp == nil {
		t.Fatalf("skill op not found among ops; paths: %v", opPaths(ops))
	}
	parts := strings.SplitN(skillOp.Content, "---\n", 3)
	if len(parts) < 3 {
		t.Fatalf("frontmatter delimiters missing in:\n%s", skillOp.Content)
	}
	var fm struct {
		Description string   `yaml:"description"`
		Globs       []string `yaml:"globs"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		t.Fatalf("frontmatter is not valid YAML (the v0.7 colon-in-description bug): %v\n---\n%s", err, parts[1])
	}
	if fm.Description != "Ship a release: cuts a tag, builds artifacts, publishes" {
		t.Errorf("description = %q after YAML round-trip, want full string", fm.Description)
	}
}

func opPaths(ops []plugin.Operation) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.Path
	}
	return out
}
