package engine

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

func sampleProject() *model.Project {
	return &model.Project{
		Context: &model.Document{Body: "root context"},
		Scopes: []*model.Scope{
			{Path: "src/billing", Document: &model.Document{SourcePath: "src/billing/CLAUDE.md", Body: "billing"}},
			{Path: "src/auth", Document: &model.Document{SourcePath: "src/auth/CLAUDE.md", Body: "auth"}},
		},
		Skills: []*model.Skill{
			{Name: "test-runner", Document: &model.Document{SourcePath: ".claude/skills/test-runner.md"}},
			{Name: "billing-helper", ScopePath: "src/billing", Document: &model.Document{SourcePath: "src/billing/.claude/skills/billing-helper.md"}},
			{Name: "auth-helper", ScopePath: "src/auth", Document: &model.Document{SourcePath: "src/auth/.claude/skills/auth-helper.md"}},
		},
		Commands: []*model.Command{
			{Name: "deploy", Document: &model.Document{SourcePath: ".claude/commands/deploy.md"}},
		},
		Agents: []*model.Agent{
			{Name: "reviewer", Document: &model.Document{SourcePath: ".claude/agents/reviewer.md"}},
		},
		MCP: []*model.MCPServer{
			{Name: "github", Command: "gh-mcp"},
		},
	}
}

func TestFilterProjectInteractively_AcceptAll(t *testing.T) {
	p := sampleProject()
	in := bufio.NewReader(strings.NewReader("a\n"))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("scopes = %d, want 2", len(got.Scopes))
	}
	if len(got.Skills) != 3 {
		t.Errorf("skills = %d, want 3", len(got.Skills))
	}
	if len(got.Commands) != 1 {
		t.Errorf("commands = %d, want 1", len(got.Commands))
	}
	if len(got.Agents) != 1 {
		t.Errorf("agents = %d, want 1", len(got.Agents))
	}
	if len(got.MCP) != 1 {
		t.Errorf("mcp = %d, want 1", len(got.MCP))
	}
}

func TestFilterProjectInteractively_DeclineAll(t *testing.T) {
	p := sampleProject()
	// Decline-all from the very first scope prompt, then expect ErrInteractiveDeclinedAll.
	// Context.Body is set, so we need to clear it for the empty check to fire.
	p.Context = nil
	in := bufio.NewReader(strings.NewReader("d\n"))
	out := &bytes.Buffer{}
	_, err := filterProjectInteractively(p, in, out)
	if !errors.Is(err, ErrInteractiveDeclinedAll) {
		t.Fatalf("err = %v, want ErrInteractiveDeclinedAll", err)
	}
}

func TestFilterProjectInteractively_DeclineAllKeepsContext(t *testing.T) {
	// When Context is non-empty, declining everything else still yields a
	// usable project (the root context survives).
	p := sampleProject()
	in := bufio.NewReader(strings.NewReader("d\n"))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Scopes) != 0 || len(got.Skills) != 0 || len(got.Commands) != 0 {
		t.Errorf("expected empty lists, got scopes=%d skills=%d commands=%d", len(got.Scopes), len(got.Skills), len(got.Commands))
	}
	if got.Context == nil || got.Context.Body == "" {
		t.Errorf("context should be preserved")
	}
}

func TestFilterProjectInteractively_Mixed(t *testing.T) {
	p := sampleProject()
	// scope src/billing: y
	// scope src/auth: n  (drops auth-helper skill via cascade)
	// skill test-runner: y
	// skill billing-helper: n
	// command deploy: y
	// agent reviewer: n
	// mcp github: y
	answers := strings.Join([]string{"y", "n", "y", "n", "y", "n", "y"}, "\n") + "\n"
	in := bufio.NewReader(strings.NewReader(answers))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Scopes) != 1 || got.Scopes[0].Path != "src/billing" {
		t.Errorf("scopes = %+v, want [src/billing]", scopePaths(got.Scopes))
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "test-runner" {
		t.Errorf("skills = %+v, want [test-runner]", skillNames(got.Skills))
	}
	if len(got.Commands) != 1 || got.Commands[0].Name != "deploy" {
		t.Errorf("commands = %+v, want [deploy]", commandNames(got.Commands))
	}
	if len(got.Agents) != 0 {
		t.Errorf("agents = %+v, want []", agentNames(got.Agents))
	}
	if len(got.MCP) != 1 || got.MCP[0].Name != "github" {
		t.Errorf("mcp = %+v, want [github]", mcpNames(got.MCP))
	}
}

func TestFilterProjectInteractively_ScopeSkipSkills(t *testing.T) {
	p := sampleProject()
	// scope src/billing: s (include scope, skip its skills)
	// scope src/auth: y
	// skill test-runner: y (global, not under billing or auth)
	// (billing-helper is auto-skipped via 's'; auth-helper still prompts)
	// skill auth-helper: y
	// command deploy: y
	// agent reviewer: y
	// mcp github: y
	answers := strings.Join([]string{"s", "y", "y", "y", "y", "y", "y"}, "\n") + "\n"
	in := bufio.NewReader(strings.NewReader(answers))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("scopes = %d, want 2", len(got.Scopes))
	}
	gotSkills := skillNames(got.Skills)
	wantSet := map[string]bool{"test-runner": true, "auth-helper": true}
	if len(gotSkills) != 2 {
		t.Errorf("skills = %v, want %v", gotSkills, wantSet)
	}
	for _, n := range gotSkills {
		if !wantSet[n] {
			t.Errorf("unexpected skill %q (skip-children should drop billing-helper)", n)
		}
	}
}

func TestFilterProjectInteractively_EOFAcceptsAll(t *testing.T) {
	p := sampleProject()
	// Empty reader: EOF on first prompt should accept everything remaining.
	in := bufio.NewReader(strings.NewReader(""))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Scopes) != 2 || len(got.Skills) != 3 || len(got.Commands) != 1 {
		t.Errorf("EOF should accept-all; got scopes=%d skills=%d commands=%d", len(got.Scopes), len(got.Skills), len(got.Commands))
	}
	if !strings.Contains(out.String(), "EOF") {
		t.Errorf("expected EOF notice in output, got %q", out.String())
	}
}

func TestFilterProjectInteractively_InvalidThenValid(t *testing.T) {
	p := &model.Project{
		Skills: []*model.Skill{{Name: "x", Document: &model.Document{SourcePath: "x.md"}}},
	}
	in := bufio.NewReader(strings.NewReader("?\nq\ny\n"))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Skills) != 1 {
		t.Errorf("skills = %d, want 1", len(got.Skills))
	}
	if !strings.Contains(out.String(), "Y=include") {
		t.Errorf("expected help text after bad input, got %q", out.String())
	}
}

func TestFilterProjectInteractively_DefaultYesOnEmptyLine(t *testing.T) {
	p := &model.Project{
		Skills: []*model.Skill{
			{Name: "a", Document: &model.Document{SourcePath: "a.md"}},
			{Name: "b", Document: &model.Document{SourcePath: "b.md"}},
		},
	}
	// Empty line then 'n'.
	in := bufio.NewReader(strings.NewReader("\nn\n"))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "a" {
		t.Errorf("skills = %v, want [a]", skillNames(got.Skills))
	}
}

func TestFilterProjectInteractively_DroppedScopeCascadesToChildren(t *testing.T) {
	p := &model.Project{
		Scopes: []*model.Scope{
			{Path: "src/dropped", Document: &model.Document{SourcePath: "src/dropped/CLAUDE.md"}},
		},
		Skills: []*model.Skill{
			{Name: "child", ScopePath: "src/dropped", Document: &model.Document{SourcePath: "src/dropped/.claude/skills/child.md"}},
		},
		Commands: []*model.Command{
			{Name: "child-cmd", ScopePath: "src/dropped", Document: &model.Document{SourcePath: "src/dropped/.claude/commands/child-cmd.md"}},
		},
	}
	// Decline the scope; no further prompts should fire for cascaded children.
	in := bufio.NewReader(strings.NewReader("n\n"))
	out := &bytes.Buffer{}
	got, err := filterProjectInteractively(p, in, out)
	if !errors.Is(err, ErrInteractiveDeclinedAll) {
		t.Fatalf("err = %v, want ErrInteractiveDeclinedAll (everything cascaded out)", err)
	}
	if got != nil {
		t.Errorf("expected nil project on decline-all, got %+v", got)
	}
}

func scopePaths(ss []*model.Scope) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Path
	}
	return out
}
func skillNames(ss []*model.Skill) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}
func commandNames(cs []*model.Command) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}
func agentNames(as []*model.Agent) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}
func mcpNames(ms []*model.MCPServer) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}
