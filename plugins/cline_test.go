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

// findOpByPath is a small helper used throughout the cline tests to
// locate the op for a specific projected path. Returns nil if absent.
func findOpByPath(ops []plugin.Operation, path string) *plugin.Operation {
	for i := range ops {
		if ops[i].Path == path {
			return &ops[i]
		}
	}
	return nil
}

// opPathSet returns the set of projected paths for assertion errors.
func opPathSet(ops []plugin.Operation) []string {
	out := make([]string, len(ops))
	for i := range ops {
		out[i] = ops[i].Path
	}
	return out
}

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
		t.Fatalf("expected 1 op, got %d (%v)", len(ops), opPathSet(ops))
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
// scope rule file with YAML frontmatter (`description:` + `paths:`),
// the "When working in <path>" preamble, and the scope body. The
// "no scope enforcement" warning is gone — scope is now native via
// the `paths:` frontmatter.
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
		t.Fatalf("expected 1 op, got %d (%v)", len(ops), opPathSet(ops))
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
	// Frontmatter native: scope is enforced via `paths:`.
	if !strings.HasPrefix(op.Content, "---\n") {
		t.Errorf("content should start with YAML frontmatter\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "paths:\n  - src/billing/**") && !strings.Contains(op.Content, "paths:\n  - \"src/billing/**\"") {
		t.Errorf("content missing paths: frontmatter\n---\n%s", op.Content)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "src/billing/context.md" {
		t.Errorf("sources = %v, want [src/billing/context.md]", op.Sources)
	}
	// No longer emits a "no scope enforcement" warning — scope is native.
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no scope enforcement") {
			t.Errorf("unexpected legacy scope-enforcement warning: %#v", w)
		}
	}
}

// TestCline_Scope_GlobsOnly: a scope with no Description but with Globs
// renders the "Triggers:" preamble line in the body and `paths:` in the
// frontmatter.
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
	if !strings.Contains(ops[0].Content, "paths:\n  - src/api/**\n  - tests/api/**") && !strings.Contains(ops[0].Content, "paths:\n  - \"src/api/**\"\n  - \"tests/api/**\"") {
		t.Errorf("missing paths: frontmatter\n---\n%s", ops[0].Content)
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

// TestCline_Capabilities: the v0.8.0 matrix flips ScopePaths,
// ScopeSemantic, Commands, Hooks, MCP to native. Skills stay degraded
// (no dedicated primitive). Agents + Permissions stay unsupported.
func TestCline_Capabilities(t *testing.T) {
	caps := NewCline().Capabilities()
	if caps.Context != plugin.SupportNative {
		t.Errorf("Context = %q, want native", caps.Context)
	}
	if caps.ScopePaths != plugin.SupportNative {
		t.Errorf("ScopePaths = %q, want native", caps.ScopePaths)
	}
	if caps.ScopeSemantic != plugin.SupportNative {
		t.Errorf("ScopeSemantic = %q, want native", caps.ScopeSemantic)
	}
	if caps.Skills != plugin.SupportDegraded {
		t.Errorf("Skills = %q, want degraded", caps.Skills)
	}
	if caps.Commands != plugin.SupportNative {
		t.Errorf("Commands = %q, want native", caps.Commands)
	}
	if caps.Agents != plugin.SupportUnsupported {
		t.Errorf("Agents = %q, want unsupported", caps.Agents)
	}
	if caps.Hooks != plugin.SupportNative {
		t.Errorf("Hooks = %q, want native", caps.Hooks)
	}
	if caps.Permissions != plugin.SupportUnsupported {
		t.Errorf("Permissions = %q, want unsupported", caps.Permissions)
	}
	if caps.MCP != plugin.SupportNative {
		t.Errorf("MCP = %q, want native", caps.MCP)
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

// TestCline_Commands_AsWorkflows: a Command projects to
// .clinerules/workflows/<name>.md (native slash command) with a YAML
// `description:` frontmatter, and no longer emits the "no slash-command"
// warning. The filename (sans .md) is the slash command Cline users
// invoke with /<name>.
func TestCline_Commands_AsWorkflows(t *testing.T) {
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
		t.Fatalf("expected 1 op, got %d (%v)", len(ops), opPathSet(ops))
	}
	op := ops[0]
	wantPath := ".clinerules/workflows/deploy.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
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
	if !strings.HasPrefix(op.Content, "---\ndescription: Deploy to staging\n---\n") && !strings.HasPrefix(op.Content, "---\ndescription: \"Deploy to staging\"\n---\n") {
		t.Errorf("content missing description frontmatter\n---\n%s", op.Content)
	}
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no slash-command") {
			t.Errorf("unexpected legacy no-slash-command warning: %#v", w)
		}
	}
}

// TestCline_ScopedSkill: a Skill with a non-empty ScopePath projects to
// .clinerules/20-skill-<scopeSlug>-<name>.md with a "When working in
// <scopePath>" preamble before the Skill header and a `paths:`
// frontmatter scoped to the scope path's tree. No longer emits the
// "no scope enforcement" warning since scope is native via paths.
func TestCline_ScopedSkill(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "audit-trail",
				Description: "Tamper-evident audit log",
				ScopePath:   "src/billing",
				Globs:       []string{"src/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/skills/audit-trail/SKILL.md",
					Body:       "Append hash-chain entries to ledger.",
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
	wantPath := ".clinerules/20-skill-src-billing-audit-trail.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, "## When working in src/billing") {
		t.Errorf("content missing scope preamble\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "## Skill: audit-trail") {
		t.Errorf("content missing skill header\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Append hash-chain entries to ledger.") {
		t.Errorf("content missing body\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "paths:\n  - src/billing/**") && !strings.Contains(op.Content, "paths:\n  - \"src/billing/**\"") {
		t.Errorf("content missing paths: frontmatter\n---\n%s", op.Content)
	}
	// Scope must appear before the Skill header.
	idxScope := strings.Index(op.Content, "## When working in src/billing")
	idxSkill := strings.Index(op.Content, "## Skill: audit-trail")
	if idxScope < 0 || idxSkill < 0 || idxScope >= idxSkill {
		t.Errorf("preamble must precede skill header: scope=%d skill=%d\n%s", idxScope, idxSkill, op.Content)
	}
	if len(op.Sources) != 1 || op.Sources[0] != "src/billing/skills/audit-trail/SKILL.md" {
		t.Errorf("sources = %v, want [src/billing/skills/audit-trail/SKILL.md]", op.Sources)
	}
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "no scope enforcement") {
			t.Errorf("unexpected legacy scope-enforcement warning: %#v", w)
		}
	}
}

// TestCline_ScopedSkillCollision: same Skill.Name under two different
// scopes must produce two distinct files without overwriting either.
func TestCline_ScopedSkillCollision(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:      "audit-trail",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/skills/audit-trail/SKILL.md",
					Body:       "billing audit",
				},
			},
			{
				Name:      "audit-trail",
				ScopePath: "src/payments",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/payments/skills/audit-trail/SKILL.md",
					Body:       "payments audit",
				},
			},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (one per scope), got %d", len(ops))
	}

	seenPaths := map[string]bool{}
	for _, op := range ops {
		if seenPaths[op.Path] {
			t.Errorf("duplicate filename projected: %s", op.Path)
		}
		seenPaths[op.Path] = true
	}
	want := map[string]string{
		".clinerules/20-skill-src-billing-audit-trail.md":  "billing audit",
		".clinerules/20-skill-src-payments-audit-trail.md": "payments audit",
	}
	for path, body := range want {
		if !seenPaths[path] {
			t.Errorf("missing expected path %q (got %v)", path, seenPaths)
			continue
		}
		op := findOpByPath(ops, path)
		if op == nil {
			continue
		}
		if !strings.Contains(op.Content, body) {
			t.Errorf("op %q missing body %q", path, body)
		}
	}
}

// TestCline_ScopedCommand: a Command with a non-empty ScopePath projects
// to .clinerules/workflows/<scopeSlug>-<name>.md with the scope
// preamble in the body and a "workflows are global" warning explaining
// the loss of path enforcement for the slash command.
func TestCline_ScopedCommand(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Commands: []*model.Command{
			{
				Name:        "deploy",
				Description: "Deploy this scope",
				ScopePath:   "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/commands/deploy.md",
					Body:       "Run billing deploy script.",
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
	wantPath := ".clinerules/workflows/src-billing-deploy.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if !strings.Contains(op.Content, "## When working in src/billing") {
		t.Errorf("content missing scope preamble\n---\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "## Command /deploy") {
		t.Errorf("content missing command header\n---\n%s", op.Content)
	}
	hasScopeWarn := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "workflows are global") &&
			strings.Contains(w.Message, "src/billing") {
			hasScopeWarn = true
		}
	}
	if !hasScopeWarn {
		t.Errorf("expected scoped-command workflows-are-global warning, got %#v", op.Warnings)
	}
}

// TestCline_ScopedAgentWarning: a scoped Agent emits no file and produces
// a warning that includes the scope path.
func TestCline_ScopedAgentWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Agents: []*model.Agent{
			{
				Name:      "billing-reviewer",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/billing/agents/billing-reviewer.md",
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
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "scoped agent billing-reviewer") &&
			strings.Contains(w.Message, "src/billing") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected scoped agent warning naming scope, got %#v", ops[0].Warnings)
	}
}

// TestCline_MCP_Projects verifies an MCPServer projects to
// .cline/cline_mcp_settings.json as an OpMerge whose Merger emits a
// well-formed {mcpServers: {...}} document. Mirrors the Claude
// .mcp.json contract.
func TestCline_MCP_Projects(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		MCP: []*model.MCPServer{
			{Name: "linear", Command: "npx", Args: []string{"@linear/mcp"}, Env: map[string]string{"LINEAR_KEY": "abc"}},
			{Name: "github", URL: "https://example.invalid/sse"},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	wantPath := ".cline/cline_mcp_settings.json"
	op := findOpByPath(ops, wantPath)
	if op == nil {
		t.Fatalf("missing %s op; got %v", wantPath, opPathSet(ops))
	}
	if op.Kind != plugin.OpMerge {
		t.Errorf("kind = %v, want OpMerge (so existing settings are preserved)", op.Kind)
	}
	if op.Merger == nil {
		t.Fatalf("merger should be non-nil for OpMerge")
	}
	// Run the merger against an empty existing file (file absent → engine
	// hands the merger nil bytes).
	out, err := op.Merger(nil)
	if err != nil {
		t.Fatalf("merger(nil) error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("merger output is not valid JSON: %v\n%s", err, out)
	}
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok || servers == nil {
		t.Fatalf("mcpServers is not an object: %#v", doc["mcpServers"])
	}
	linear, ok := servers["linear"].(map[string]any)
	if !ok {
		t.Fatalf("linear entry missing: %#v", servers["linear"])
	}
	if linear["command"] != "npx" {
		t.Errorf("linear.command = %v, want npx", linear["command"])
	}
	github, ok := servers["github"].(map[string]any)
	if !ok {
		t.Fatalf("github entry missing: %#v", servers["github"])
	}
	if github["url"] != "https://example.invalid/sse" {
		t.Errorf("github.url = %v, want https://example.invalid/sse", github["url"])
	}
	// Merger must preserve unrelated keys in any existing file.
	existing := `{"version":2,"mcpServers":{"linear":{"command":"old"}}}`
	out2, err := op.Merger([]byte(existing))
	if err != nil {
		t.Fatalf("merger(existing) error: %v", err)
	}
	var doc2 map[string]any
	if err := json.Unmarshal([]byte(out2), &doc2); err != nil {
		t.Fatalf("merger output (with existing) is not valid JSON: %v\n%s", err, out2)
	}
	if v, ok := doc2["version"]; !ok || v.(float64) != 2 {
		t.Errorf("merger lost unrelated existing key 'version': %#v", doc2)
	}
	// The linear command should be overwritten by the projection.
	servers2 := doc2["mcpServers"].(map[string]any)
	linear2 := servers2["linear"].(map[string]any)
	if linear2["command"] != "npx" {
		t.Errorf("merger should overwrite linear.command with projection value; got %v", linear2["command"])
	}
}

// TestCline_Hooks_Emit verifies hooks project to
// .clinerules/hooks/<event>.json with one file per event, the
// {hooks:[{matcher,hooks:[{type,command}]}]} schema, and the global
// hook's command set to the raw script path (no wrapper, since it's
// unscoped).
func TestCline_Hooks_Emit(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Hooks: []*model.Hook{
			{Event: "PreToolUse", Matcher: "Edit", ScriptPath: "/tmp/.agents/hooks/lint.sh"},
			{Event: "PostToolUse", Matcher: "Bash", ScriptPath: "/tmp/.agents/hooks/audit.sh"},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	preOp := findOpByPath(ops, ".clinerules/hooks/PreToolUse.json")
	if preOp == nil {
		t.Fatalf("missing PreToolUse.json op; got %v", opPathSet(ops))
	}
	postOp := findOpByPath(ops, ".clinerules/hooks/PostToolUse.json")
	if postOp == nil {
		t.Fatalf("missing PostToolUse.json op; got %v", opPathSet(ops))
	}
	if preOp.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", preOp.Kind)
	}
	// Parse PreToolUse JSON and assert structure.
	var preDoc struct {
		Hooks []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(preOp.Content), &preDoc); err != nil {
		t.Fatalf("invalid JSON for PreToolUse: %v\n%s", err, preOp.Content)
	}
	if len(preDoc.Hooks) != 1 {
		t.Fatalf("expected 1 matcher group, got %d", len(preDoc.Hooks))
	}
	if preDoc.Hooks[0].Matcher != "Edit" {
		t.Errorf("matcher = %q, want Edit", preDoc.Hooks[0].Matcher)
	}
	if len(preDoc.Hooks[0].Hooks) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(preDoc.Hooks[0].Hooks))
	}
	entry := preDoc.Hooks[0].Hooks[0]
	if entry.Type != "command" {
		t.Errorf("type = %q, want command", entry.Type)
	}
	if entry.Command != "/tmp/.agents/hooks/lint.sh" {
		t.Errorf("command = %q, want raw script path /tmp/.agents/hooks/lint.sh", entry.Command)
	}
}

// TestCline_ScopedHook_WrapperEmitted verifies that a hook with a
// non-empty ScopePath produces both:
//   - a scope-guard wrapper script at
//     .clinerules/hooks/__scope-guard__/<scopeSlug>-<event>-<basename>.sh
//   - the hook JSON command field pointing at that wrapper's absolute path
//
// This mirrors the Claude plugin's wrapper contract so users get
// runtime scope enforcement even though Cline's hook engine doesn't
// natively understand scope.
func TestCline_ScopedHook_WrapperEmitted(t *testing.T) {
	projRoot := "/tmp/p"
	proj := &model.Project{
		Root:      projRoot,
		AgentsDir: filepath.Join(projRoot, ".agents"),
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Edit",
				ScriptPath: filepath.Join(projRoot, ".agents", "src", "billing", "hooks", "verify.sh"),
				ScopePath:  "src/billing",
			},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	wrapperRel := ".clinerules/hooks/__scope-guard__/src-billing-PreToolUse-verify.sh"
	wrapperOp := findOpByPath(ops, wrapperRel)
	if wrapperOp == nil {
		t.Fatalf("missing scope-guard wrapper at %s; got %v", wrapperRel, opPathSet(ops))
	}
	if wrapperOp.FileMode != 0o755 {
		t.Errorf("wrapper file mode = %v, want 0755", wrapperOp.FileMode)
	}
	if !strings.Contains(wrapperOp.Content, "prism scope-guard --scope 'src/billing'") {
		t.Errorf("wrapper missing prism scope-guard invocation:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "set -euo pipefail") {
		t.Errorf("wrapper missing strict-mode preamble:\n%s", wrapperOp.Content)
	}

	// The hook JSON's command should point at the wrapper's absolute path.
	preOp := findOpByPath(ops, ".clinerules/hooks/PreToolUse.json")
	if preOp == nil {
		t.Fatalf("missing PreToolUse.json op")
	}
	wantCmd := filepath.Join(projRoot, wrapperRel)
	if !strings.Contains(preOp.Content, wantCmd) {
		t.Errorf("hook JSON should reference wrapper at %s, got:\n%s", wantCmd, preOp.Content)
	}

	// DisableHookWrappers turns wrappers off and inlines the source script.
	p2 := &ClinePlugin{DisableHookWrappers: true}
	ops2, err := p2.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan(disable wrappers) error: %v", err)
	}
	if findOpByPath(ops2, wrapperRel) != nil {
		t.Errorf("wrapper should not be emitted when DisableHookWrappers=true")
	}
	preOp2 := findOpByPath(ops2, ".clinerules/hooks/PreToolUse.json")
	if preOp2 == nil {
		t.Fatalf("missing PreToolUse.json op (disable wrappers)")
	}
	if !strings.Contains(preOp2.Content, "/tmp/p/.agents/src/billing/hooks/verify.sh") {
		t.Errorf("disabled wrapper should inline raw script path; got:\n%s", preOp2.Content)
	}
}

// TestCline_Rules_FrontmatterPaths verifies that scope and skill rule
// files carry a YAML `paths:` frontmatter block — the v0.8.0 upgrade
// from the legacy filename-prefix scheme to native frontmatter-based
// scope enforcement.
func TestCline_Rules_FrontmatterPaths(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:        "src/api",
				Globs:       []string{"src/api/**/*.go", "tests/api/**"},
				Description: "REST handler conventions",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/api/context.md",
					Body:       "Body.",
				},
			},
		},
		Skills: []*model.Skill{
			{
				Name:        "trace-tagging",
				Description: "Tag spans with request IDs",
				Globs:       []string{"**/*.go"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/trace-tagging/SKILL.md",
					Body:       "Skill body.",
				},
			},
		},
	}
	p := NewCline()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	scopeOp := findOpByPath(ops, ".clinerules/10-scope-src-api.md")
	if scopeOp == nil {
		t.Fatalf("missing scope op; got %v", opPathSet(ops))
	}
	fm := extractFrontmatter(t, scopeOp.Content)
	if !strings.Contains(fm, `description: "REST handler conventions"`) && !strings.Contains(fm, "description: REST handler conventions") {
		t.Errorf("scope frontmatter missing description:\n%s", fm)
	}
	if !strings.Contains(fm, "paths:\n  - src/api/**/*.go\n  - tests/api/**") && !strings.Contains(fm, "paths:\n  - \"src/api/**/*.go\"\n  - \"tests/api/**\"") {
		t.Errorf("scope frontmatter missing paths array:\n%s", fm)
	}

	skillOp := findOpByPath(ops, ".clinerules/20-skill-trace-tagging.md")
	if skillOp == nil {
		t.Fatalf("missing skill op; got %v", opPathSet(ops))
	}
	sfm := extractFrontmatter(t, skillOp.Content)
	if !strings.Contains(sfm, `description: "Tag spans with request IDs"`) && !strings.Contains(sfm, "description: Tag spans with request IDs") {
		t.Errorf("skill frontmatter missing description:\n%s", sfm)
	}
	// `**/*.go` begins with `*` (a YAML alias indicator), so yamlScalar
	// double-quotes it. Accept any of the three forms a sane YAML emitter
	// might choose, so this test stays robust if the quoting rules change.
	if !strings.Contains(sfm, `paths:`+"\n"+`  - "**/*.go"`) &&
		!strings.Contains(sfm, `paths:`+"\n"+`  - '**/*.go'`) &&
		!strings.Contains(sfm, `paths:`+"\n"+`  - **/*.go`) {
		t.Errorf("skill frontmatter missing paths array (in any quoting form):\n%s", sfm)
	}
}

// extractFrontmatter pulls the YAML block between the first two `---`
// delimiters out of a markdown body. Fails the test if the body has no
// frontmatter at all (helpful for callers asserting native paths:).
func extractFrontmatter(t *testing.T, content string) string {
	t.Helper()
	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("content has no frontmatter:\n%s", content)
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatalf("frontmatter end delimiter missing:\n%s", content)
	}
	return rest[:end+1] // include trailing newline of last key
}
