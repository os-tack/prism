package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// TestCline_RootContext: a project with only a root Context document
// produces a single op at .clinerules/00-context.md whose body matches
// the Document.Body verbatim (with a trailing newline ensured).
func TestCline_RootContext(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "global cline context",
		},
	}

	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".clinerules/00-context.md" {
		t.Errorf("path = %q, want .clinerules/00-context.md", op.Path)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if op.Mode != plugin.ModeWrite {
		t.Errorf("mode = %v, want ModeWrite", op.Mode)
	}
	if op.Plugin != "cline" {
		t.Errorf("plugin = %q, want cline", op.Plugin)
	}
	if !strings.Contains(op.Content, "global cline context") {
		t.Errorf("content missing body\n---\n%s", op.Content)
	}
	if !strings.HasSuffix(op.Content, "\n") {
		t.Errorf("content should end with newline\n---\n%q", op.Content)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "context.md" {
		t.Errorf("sources = %v, want [context.md]", op.Sources)
	}
}

// TestCline_Scope: a scope with Description and Globs projects to a
// scope rule file with a "When working in <path>" preamble, includes
// the scope body, and emits the "no scope enforcement" info warning.
func TestCline_Scope(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/fake/.agents",
		Scopes: []*model.Scope{
			{
				Path:        "src/billing",
				Globs:       []string{"src/billing/**"},
				Description: "Stripe webhook context",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/context.md",
					Body:       "billing rules go here",
				},
			},
		},
	}

	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".clinerules/10-scope-src-billing.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, "## When working in src/billing") {
		t.Errorf("content missing scope header\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Stripe webhook context") {
		t.Errorf("content missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "billing rules go here") {
		t.Errorf("content missing body\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "src/billing/context.md" {
		t.Errorf("sources = %v, want [src/billing/context.md]", op.Sources)
	}
	// Warning about no scope enforcement.
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no scope enforcement") {
			found = true
			if w.Severity != "info" {
				t.Errorf("scope warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected scope-enforcement warning, got %#v", op.Warnings)
	}
}

// TestCline_Scope_GlobsOnly: a scope with no Description but with Globs
// renders the "Triggers:" preamble line.
func TestCline_Scope_GlobsOnly(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:  "src/api",
				Globs: []string{"src/api/**", "tests/api/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/api/context.md",
					Body:       "api ctx",
				},
			},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if !strings.Contains(ops[0].Content, "> Triggers: src/api/**, tests/api/**") {
		t.Errorf("missing globs trigger line\n---\n%s", ops[0].Content)
	}
}

// TestCline_Skill: a skill projects to .clinerules/20-skill-<slug>.md
// with a "Skill: <name>" header. Scripts present → an "ignored" info
// warning is attached.
func TestCline_Skill(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "pdf-editing",
				Description: "Edit PDFs in place",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/pdf-editing/SKILL.md",
					Body:       "Steps for editing PDFs.",
				},
				Scripts: []string{"merge.sh", "rotate.py"},
			},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".clinerules/20-skill-pdf-editing.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, "## Skill: pdf-editing") {
		t.Errorf("content missing skill header\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Edit PDFs in place") {
		t.Errorf("content missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Steps for editing PDFs.") {
		t.Errorf("content missing body\n---\n%s", op.Content)
	}
	// Script warning.
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "cannot execute scripts") &&
			strings.Contains(w.Message, "merge.sh") &&
			strings.Contains(w.Message, "rotate.py") {
			found = true
			if w.Severity != "info" {
				t.Errorf("script warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected script-ignored warning naming both scripts, got %#v", op.Warnings)
	}
}

// TestCline_AgentWarning: an Agent emits no file but produces a warning
// attached to whatever op exists (here, the root context op).
func TestCline_AgentWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Agents: []*model.Agent{
			{
				Name:        "code-reviewer",
				Description: "Reviews diffs",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/code-reviewer.md",
				},
			},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "agents") {
			t.Errorf("unexpected agents file: %s", op.Path)
		}
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

// TestCline_UnknownModeErrors: Mode "symlink" (or any other non-empty
// non-"write" value) is rejected with an error.
func TestCline_UnknownModeErrors(t *testing.T) {
	p := NewCline()
	proj := &model.Project{AgentsDir: "/tmp/.agents"}
	if _, err := p.Plan(proj, model.TargetOption{Mode: "symlink"}); err == nil {
		t.Fatalf("expected error for Mode=symlink")
	}
	if _, err := p.Plan(proj, model.TargetOption{Mode: "bogus"}); err == nil {
		t.Fatalf("expected error for unknown mode")
	}
}

// TestCline_DetectMarkers: Detect returns false in an empty directory,
// true when .clinerules is a directory, and true when .clinerules is a
// single file (legacy form).
func TestCline_DetectMarkers(t *testing.T) {
	p := NewCline()

	// Empty dir → false.
	empty := t.TempDir()
	if p.Detect(empty) {
		t.Errorf("Detect(empty) = true, want false")
	}

	// .clinerules/ as a directory → true.
	dirRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirRoot, ".clinerules"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !p.Detect(dirRoot) {
		t.Errorf("Detect(.clinerules/ dir) = false, want true")
	}

	// .clinerules as a single file → true.
	fileRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(fileRoot, ".clinerules"), []byte("# legacy\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !p.Detect(fileRoot) {
		t.Errorf("Detect(.clinerules file) = false, want true")
	}
}

// TestCline_Capabilities: the matrix matches the contract.
func TestCline_Capabilities(t *testing.T) {
	caps := NewCline().Capabilities()
	if caps.Context != plugin.SupportNative {
		t.Errorf("Context = %q, want native", caps.Context)
	}
	if caps.ScopePaths != plugin.SupportDegraded {
		t.Errorf("ScopePaths = %q, want degraded", caps.ScopePaths)
	}
	if caps.ScopeSemantic != plugin.SupportDegraded {
		t.Errorf("ScopeSemantic = %q, want degraded", caps.ScopeSemantic)
	}
	if caps.Skills != plugin.SupportDegraded {
		t.Errorf("Skills = %q, want degraded", caps.Skills)
	}
	if caps.Commands != plugin.SupportDegraded {
		t.Errorf("Commands = %q, want degraded", caps.Commands)
	}
	if caps.Agents != plugin.SupportUnsupported {
		t.Errorf("Agents = %q, want unsupported", caps.Agents)
	}
	if caps.Hooks != plugin.SupportUnsupported {
		t.Errorf("Hooks = %q, want unsupported", caps.Hooks)
	}
	if caps.Permissions != plugin.SupportUnsupported {
		t.Errorf("Permissions = %q, want unsupported", caps.Permissions)
	}
	if caps.MCP != plugin.SupportUnsupported {
		t.Errorf("MCP = %q, want unsupported", caps.MCP)
	}
}

// TestCline_NilProject: Plan(nil) is a no-op with no error.
func TestCline_NilProject(t *testing.T) {
	ops, err := NewCline().Plan(nil, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan(nil) error: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("ops = %d, want 0", len(ops))
	}
}

// TestCline_Command: a Command projects to .clinerules/30-command-<slug>.md
// with a "Command /<name>" header, and emits the "no slash-command" warning.
func TestCline_Command(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Commands: []*model.Command{
			{
				Name:        "deploy",
				Description: "Deploy to staging",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/commands/deploy.md",
					Body:       "Run deploy script and verify.",
				},
			},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != ".clinerules/30-command-deploy.md" {
		t.Errorf("path = %q, want .clinerules/30-command-deploy.md", op.Path)
	}
	if !strings.Contains(op.Content, "## Command /deploy") {
		t.Errorf("content missing command header\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Deploy to staging") {
		t.Errorf("content missing description\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Run deploy script and verify.") {
		t.Errorf("content missing body\n---\n%s", op.Content)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no slash-command mechanism") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected slash-command warning, got %#v", op.Warnings)
	}
}

// TestCline_MCPWarning: an MCP server emits no file but attaches the
// VS Code settings warning to the first op.
func TestCline_MCPWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		MCP: []*model.MCPServer{
			{Name: "linear", Command: "npx"},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "mcp") {
			t.Errorf("unexpected mcp file: %s", op.Path)
		}
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "MCP via VS Code settings") && strings.Contains(w.Message, "linear") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected mcp warning, got %#v", ops[0].Warnings)
	}
}
