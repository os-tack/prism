package plugins

import (
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

func TestAgentsMDPlan_RootAndScopes(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "Top-level context",
		},
		Scopes: []*model.Scope{
			// Intentionally insert billing first to verify the plugin sorts.
			{
				Path:        "src/billing",
				Globs:       []string{"src/billing/**"},
				Description: "Billing scope description",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/context.md",
					Body:       "Billing context",
				},
			},
			{
				Path:        "src/auth",
				Globs:       []string{"src/auth/**", "src/auth/**/*.go"},
				Description: "Auth scope description",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/auth/context.md",
					Body:       "Auth context",
				},
			},
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	if got, want := len(ops), 1; got != want {
		t.Fatalf("expected %d operation, got %d (%+v)", want, got, ops)
	}

	op := ops[0]
	if op.Path != "AGENTS.md" {
		t.Errorf("op.Path = %q, want %q", op.Path, "AGENTS.md")
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("op.Kind = %q, want %q", op.Kind, plugin.OpWrite)
	}
	if op.Mode != plugin.ModeWrite {
		t.Errorf("op.Mode = %q, want %q", op.Mode, plugin.ModeWrite)
	}
	if op.Plugin != "agents-md" {
		t.Errorf("op.Plugin = %q, want %q", op.Plugin, "agents-md")
	}

	wantSubstrings := []string{
		"Top-level context",
		"## When working in src/auth",
		"## When working in src/billing",
		"Auth context",
		"Billing context",
		"Triggers: src/auth/**, src/auth/**/*.go",
		"Triggers: src/billing/**",
		"Auth scope description",
		"Billing scope description",
		generatedHeader,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(op.Content, sub) {
			t.Errorf("op.Content missing substring %q\n--- content ---\n%s", sub, op.Content)
		}
	}

	// Lexicographic order: auth section header must precede billing section header.
	idxAuth := strings.Index(op.Content, "## When working in src/auth")
	idxBill := strings.Index(op.Content, "## When working in src/billing")
	if idxAuth < 0 || idxBill < 0 {
		t.Fatalf("expected both scope headers present; got auth=%d billing=%d", idxAuth, idxBill)
	}
	if idxAuth >= idxBill {
		t.Errorf("scope headers out of order: src/auth (%d) should come before src/billing (%d)", idxAuth, idxBill)
	}

	// Sources include root + both scopes (as .agents/-relative paths).
	wantSources := map[string]bool{
		"context.md":             false,
		"src/auth/context.md":    false,
		"src/billing/context.md": false,
	}
	for _, s := range op.Sources {
		if _, ok := wantSources[s]; ok {
			wantSources[s] = true
		}
	}
	for s, seen := range wantSources {
		if !seen {
			t.Errorf("op.Sources missing %q (got %v)", s, op.Sources)
		}
	}

	// Warnings: one info-level note per scope about no enforcement.
	noteCount := 0
	for _, w := range op.Warnings {
		if w.Severity == "info" && strings.Contains(w.Message, "AGENTS.md has no scope enforcement") {
			noteCount++
		}
	}
	if noteCount != 2 {
		t.Errorf("expected 2 scope-semantic warnings, got %d (warnings=%+v)", noteCount, op.Warnings)
	}
}

func TestAgentsMDPlan_NoContextNoScopes(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
	}
	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if !strings.Contains(ops[0].Content, generatedHeader) {
		t.Errorf("expected generated header in empty project output, got:\n%s", ops[0].Content)
	}
	if strings.Contains(ops[0].Content, "## When working in") {
		t.Errorf("expected no scope sections, got:\n%s", ops[0].Content)
	}
}

func TestAgentsMDPlugin_BasicShape(t *testing.T) {
	p := NewAgentsMD()
	if p.Name() != "agents-md" {
		t.Errorf("Name() = %q, want %q", p.Name(), "agents-md")
	}
	if !p.Detect("/anywhere") {
		t.Errorf("Detect should always be true")
	}
	caps := p.Capabilities()
	if caps.Context != plugin.SupportNative {
		t.Errorf("Context support = %q, want %q", caps.Context, plugin.SupportNative)
	}
	// Per the v0.2 expansion, every non-context capability is degraded (we
	// document them as text, but cannot execute).
	degraded := map[string]plugin.Support{
		"ScopePaths":    caps.ScopePaths,
		"ScopeSemantic": caps.ScopeSemantic,
		"Skills":        caps.Skills,
		"Commands":      caps.Commands,
		"Agents":        caps.Agents,
		"Hooks":         caps.Hooks,
		"Permissions":   caps.Permissions,
		"MCP":           caps.MCP,
	}
	for name, got := range degraded {
		if got != plugin.SupportDegraded {
			t.Errorf("%s support = %q, want %q", name, got, plugin.SupportDegraded)
		}
	}
}

// TestAgentsMD_SkillsSection verifies that multiple skills produce a `## Skills`
// header and each skill name appears under `### <name>`, in alphabetical order.
func TestAgentsMD_SkillsSection(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Skills: []*model.Skill{
			// Insert in reverse-alphabetical order to confirm sorting.
			{
				Name:        "review-pr",
				Description: "Review a pull request",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/skills/review-pr/SKILL.md",
					Body:       "review-pr body content",
				},
			},
			{
				Name:        "lint-fix",
				Description: "Apply automated lint fixes",
				Globs:       []string{"**/*.go"},
				Scripts:     []string{"scripts/lint.sh"},
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/skills/lint-fix/SKILL.md",
					Body:       "lint-fix body content",
				},
			},
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	content := ops[0].Content

	if !strings.Contains(content, "## Skills") {
		t.Errorf("expected `## Skills` header in output:\n%s", content)
	}
	if !strings.Contains(content, "### lint-fix") {
		t.Errorf("expected `### lint-fix` heading in output:\n%s", content)
	}
	if !strings.Contains(content, "### review-pr") {
		t.Errorf("expected `### review-pr` heading in output:\n%s", content)
	}
	// Alphabetical: lint-fix before review-pr.
	idxLint := strings.Index(content, "### lint-fix")
	idxReview := strings.Index(content, "### review-pr")
	if idxLint < 0 || idxReview < 0 {
		t.Fatalf("missing one of the skill headings: lint=%d review=%d", idxLint, idxReview)
	}
	if idxLint >= idxReview {
		t.Errorf("skill headings out of order: lint-fix (%d) should come before review-pr (%d)", idxLint, idxReview)
	}

	// Skill bodies should appear.
	if !strings.Contains(content, "lint-fix body content") {
		t.Errorf("expected lint-fix body in output")
	}
	if !strings.Contains(content, "review-pr body content") {
		t.Errorf("expected review-pr body in output")
	}

	// Each skill produces an info warning.
	skillWarnings := 0
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no skill scope enforcement") {
			skillWarnings++
		}
	}
	if skillWarnings != 2 {
		t.Errorf("expected 2 skill warnings, got %d", skillWarnings)
	}
}

// TestAgentsMD_CommandsSection verifies the slash-commands section renders
// `### /<name>` entries in alphabetical order.
func TestAgentsMD_CommandsSection(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Commands: []*model.Command{
			{
				Name:        "ship",
				Description: "Ship the current branch",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/commands/ship.md",
					Body:       "ship command body",
				},
			},
			{
				Name:        "review",
				Description: "Open code review",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/commands/review.md",
					Body:       "review command body",
				},
			},
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	content := ops[0].Content

	if !strings.Contains(content, "## Slash commands") {
		t.Errorf("expected `## Slash commands` header:\n%s", content)
	}
	if !strings.Contains(content, "### /review") {
		t.Errorf("expected `### /review` heading")
	}
	if !strings.Contains(content, "### /ship") {
		t.Errorf("expected `### /ship` heading")
	}

	idxReview := strings.Index(content, "### /review")
	idxShip := strings.Index(content, "### /ship")
	if idxReview >= idxShip {
		t.Errorf("commands out of order: /review (%d) should precede /ship (%d)", idxReview, idxShip)
	}

	cmdWarnings := 0
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "cannot invoke as a command") {
			cmdWarnings++
		}
	}
	if cmdWarnings != 2 {
		t.Errorf("expected 2 command warnings, got %d", cmdWarnings)
	}
}

// TestAgentsMD_AgentsSection verifies the subagents section renders
// `### @<name>` entries.
func TestAgentsMD_AgentsSection(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Agents: []*model.Agent{
			{
				Name:        "tester",
				Description: "Runs the test suite",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/agents/tester.md",
					Body:       "tester body",
				},
			},
			{
				Name:        "researcher",
				Description: "Investigates the codebase",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/agents/researcher.md",
					Body:       "researcher body",
				},
			},
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	content := ops[0].Content

	if !strings.Contains(content, "## Subagents") {
		t.Errorf("expected `## Subagents` header:\n%s", content)
	}
	if !strings.Contains(content, "### @researcher") {
		t.Errorf("expected `### @researcher` heading")
	}
	if !strings.Contains(content, "### @tester") {
		t.Errorf("expected `### @tester` heading")
	}

	idxResearcher := strings.Index(content, "### @researcher")
	idxTester := strings.Index(content, "### @tester")
	if idxResearcher >= idxTester {
		t.Errorf("agents out of order: @researcher (%d) should precede @tester (%d)", idxResearcher, idxTester)
	}

	agentWarnings := 0
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "cannot dispatch to subagents") {
			agentWarnings++
		}
	}
	if agentWarnings != 2 {
		t.Errorf("expected 2 subagent warnings, got %d", agentWarnings)
	}
}

// TestAgentsMD_HooksSection verifies hooks render as a bulleted list with
// the event and script path inline.
func TestAgentsMD_HooksSection(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Hooks: []*model.Hook{
			{
				Event:      "PostToolUse",
				Matcher:    "Write|Edit",
				ScriptPath: ".agents/hooks/format.sh",
			},
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	content := ops[0].Content

	if !strings.Contains(content, "## Hooks") {
		t.Errorf("expected `## Hooks` header:\n%s", content)
	}
	// The bullet line should contain the event name and script path.
	if !strings.Contains(content, "- **PostToolUse**") {
		t.Errorf("expected `- **PostToolUse**` bullet:\n%s", content)
	}
	if !strings.Contains(content, ".agents/hooks/format.sh") {
		t.Errorf("expected script path in hook bullet:\n%s", content)
	}
	if !strings.Contains(content, "matcher `Write|Edit`") {
		t.Errorf("expected matcher in hook bullet:\n%s", content)
	}

	hookWarnings := 0
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "cannot execute hooks") {
			hookWarnings++
		}
	}
	if hookWarnings != 1 {
		t.Errorf("expected 1 hook warning, got %d", hookWarnings)
	}
}

// TestAgentsMD_PermissionsSection verifies allow/deny render and ask is
// omitted when empty.
func TestAgentsMD_PermissionsSection(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Permissions: &model.Permissions{
			Allow: []string{"a", "b"},
			Deny:  []string{"c"},
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	content := ops[0].Content

	if !strings.Contains(content, "## Permissions") {
		t.Errorf("expected `## Permissions` header:\n%s", content)
	}
	if !strings.Contains(content, "- Allow: a, b") {
		t.Errorf("expected `- Allow: a, b` line:\n%s", content)
	}
	if !strings.Contains(content, "- Deny: c") {
		t.Errorf("expected `- Deny: c` line:\n%s", content)
	}
	if strings.Contains(content, "- Ask:") {
		t.Errorf("did not expect Ask line when Ask list is empty:\n%s", content)
	}

	permWarning := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "cannot enforce permissions") {
			permWarning = true
		}
	}
	if !permWarning {
		t.Errorf("expected at least one permissions warning")
	}
}

// TestAgentsMD_MCPSection verifies MCP servers render with command/args and
// URL variants, sorted alphabetically.
func TestAgentsMD_MCPSection(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		MCP: []*model.MCPServer{
			// Insert in reverse-alphabetical order to confirm sorting.
			{
				Name: "zeta",
				URL:  "https://example.com/mcp",
			},
			{
				Name:    "alpha",
				Command: "node",
				Args:    []string{"server.js", "--port=3000"},
			},
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	content := ops[0].Content

	if !strings.Contains(content, "## MCP servers") {
		t.Errorf("expected `## MCP servers` header:\n%s", content)
	}
	if !strings.Contains(content, "- **alpha**: `node server.js --port=3000`") {
		t.Errorf("expected alpha command line in output:\n%s", content)
	}
	if !strings.Contains(content, "- **zeta**: `https://example.com/mcp`") {
		t.Errorf("expected zeta URL line in output:\n%s", content)
	}

	idxAlpha := strings.Index(content, "- **alpha**")
	idxZeta := strings.Index(content, "- **zeta**")
	if idxAlpha < 0 || idxZeta < 0 {
		t.Fatalf("missing MCP bullets: alpha=%d zeta=%d", idxAlpha, idxZeta)
	}
	if idxAlpha >= idxZeta {
		t.Errorf("MCP servers out of order: alpha (%d) should precede zeta (%d)", idxAlpha, idxZeta)
	}

	mcpWarnings := 0
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "must configure MCP separately") {
			mcpWarnings++
		}
	}
	if mcpWarnings != 2 {
		t.Errorf("expected 2 MCP warnings, got %d", mcpWarnings)
	}
}

// TestAgentsMD_EmptyFieldsSkipped verifies that sections without source data
// are omitted entirely — only context appears.
func TestAgentsMD_EmptyFieldsSkipped(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "Just root context, nothing else.",
		},
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	content := ops[0].Content

	if !strings.Contains(content, "Just root context, nothing else.") {
		t.Errorf("expected root context body in output:\n%s", content)
	}

	mustNotContain := []string{
		"## Skills",
		"## Slash commands",
		"## Subagents",
		"## Hooks",
		"## Permissions",
		"## MCP servers",
	}
	for _, h := range mustNotContain {
		if strings.Contains(content, h) {
			t.Errorf("did not expect %q section in empty project:\n%s", h, content)
		}
	}
}

// TestAgentsMD_UnknownModeErrors verifies an unknown Mode is rejected up-front.
func TestAgentsMD_UnknownModeErrors(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
	}

	p := NewAgentsMD()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "writes"})
	if err == nil {
		t.Fatalf("expected error for unknown mode, got ops=%+v", ops)
	}
	if !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("expected error to mention 'unknown mode', got %v", err)
	}
}
