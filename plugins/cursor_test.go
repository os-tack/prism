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

func TestCursorPlan_RootAndScope(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/fake/.agents/context.md",
			Body:       "global context",
		},
		Scopes: []*model.Scope{
			{
				Path:        "src/billing",
				Globs:       []string{"src/billing/**"},
				Description: "Stripe webhook context",
				Document: &model.Document{
					SourcePath: "/tmp/fake/.agents/src/billing/context.md",
					Body:       "billing context",
				},
			},
		},
	}

	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(ops))
	}

	root := ops[0]
	if root.Path != ".cursor/rules/_root.mdc" {
		t.Errorf("root op path = %q, want .cursor/rules/_root.mdc", root.Path)
	}
	if !strings.Contains(root.Content, "alwaysApply: true") {
		t.Errorf("root op content missing alwaysApply: true\n---\n%s", root.Content)
	}
	if !strings.Contains(root.Content, "global context") {
		t.Errorf("root op content missing body\n---\n%s", root.Content)
	}
	if !strings.HasPrefix(root.Content, "---\n") {
		t.Errorf("root op content does not begin with frontmatter delimiter:\n%s", root.Content)
	}
	if root.Plugin != "cursor" {
		t.Errorf("root op plugin = %q, want cursor", root.Plugin)
	}

	sc := ops[1]
	if sc.Path != ".cursor/rules/src-billing.mdc" {
		t.Errorf("scope op path = %q, want .cursor/rules/src-billing.mdc", sc.Path)
	}
	if !strings.Contains(sc.Content, "description: Stripe webhook context") {
		t.Errorf("scope op content missing description\n---\n%s", sc.Content)
	}
	if !strings.Contains(sc.Content, `globs: ["src/billing/**"]`) {
		t.Errorf("scope op content missing globs frontmatter\n---\n%s", sc.Content)
	}
	if !strings.Contains(sc.Content, "billing context") {
		t.Errorf("scope op content missing body\n---\n%s", sc.Content)
	}
	if strings.Contains(sc.Content, "alwaysApply") {
		t.Errorf("scope op should not have alwaysApply\n---\n%s", sc.Content)
	}
	if len(sc.Sources) != 1 || sc.Sources[0] != "src/billing/context.md" {
		t.Errorf("scope op sources = %v, want [src/billing/context.md]", sc.Sources)
	}
}

func TestCursorPlan_EmptyDescriptionFallback(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Scopes: []*model.Scope{
			{
				Path:  "src/api",
				Globs: []string{"src/api/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/src/api/context.md",
					Body:       "api context",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if !strings.Contains(ops[0].Content, "description: Context for src/api") {
		t.Errorf("fallback description missing\n---\n%s", ops[0].Content)
	}
}

func TestCursorPlan_Nil(t *testing.T) {
	p := NewCursor()
	ops, err := p.Plan(nil, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan(nil) error: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("Plan(nil) ops = %d, want 0", len(ops))
	}
}

func TestCursorSlugify(t *testing.T) {
	cases := map[string]string{
		"src/billing":     "src-billing",
		"src/billing/api": "src-billing-api",
		"/src/billing/":   "src-billing",
		"SRC/Billing":     "src-billing",
		"src\\billing":    "src-billing",
		"":                "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCursorCapabilities(t *testing.T) {
	p := NewCursor()
	caps := p.Capabilities()
	if caps.Context != "native" {
		t.Errorf("Context support = %q, want native", caps.Context)
	}
	if caps.Agents != "native" {
		t.Errorf("Agents support = %q, want native", caps.Agents)
	}
	if caps.Commands != "native" {
		t.Errorf("Commands support = %q, want native", caps.Commands)
	}
	if caps.Skills != "native" {
		t.Errorf("Skills support = %q, want native", caps.Skills)
	}
	if caps.Hooks != "native" {
		t.Errorf("Hooks support = %q, want native", caps.Hooks)
	}
	if caps.MCP != "native" {
		t.Errorf("MCP support = %q, want native", caps.MCP)
	}
	if caps.Permissions != "unsupported" {
		t.Errorf("Permissions support = %q, want unsupported", caps.Permissions)
	}
}

func TestCursorPlan_UnknownModeErrors(t *testing.T) {
	p := NewCursor()
	proj := &model.Project{AgentsDir: "/tmp/.agents"}
	_, err := p.Plan(proj, model.TargetOption{Mode: "bogus"})
	if err == nil {
		t.Fatalf("expected error for unknown mode")
	}
}

// TestCursor_Skill_DedicatedFormat verifies that a Skill projects natively
// to .cursor/skills/<slug>/SKILL.md (Cursor 2.4+ format), NOT to the legacy
// .cursor/rules/skill-*.mdc path. The source body (which already carries
// YAML frontmatter from the parser) is written verbatim.
func TestCursor_Skill_DedicatedFormat(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Skills: []*model.Skill{
			{
				Name:        "Stripe Webhook Validator",
				Description: "Validate Stripe webhook signatures end-to-end",
				Globs:       []string{"src/billing/**", "tests/billing/**"},
				Document: &model.Document{
					SourcePath: "/tmp/.agents/skills/stripe-webhook/SKILL.md",
					Body:       "---\ndescription: Validate Stripe webhook signatures end-to-end\n---\n\nHow to validate Stripe webhooks step by step.",
				},
			},
		},
	}

	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d (paths: %v)", len(ops), opsPaths(ops))
	}
	op := ops[0]
	wantPath := ".cursor/skills/stripe-webhook-validator/SKILL.md"
	if op.Path != wantPath {
		t.Errorf("skill op path = %q, want %q", op.Path, wantPath)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("skill op kind = %v, want OpWrite", op.Kind)
	}
	if !strings.Contains(op.Content, "Validate Stripe webhook signatures end-to-end") {
		t.Errorf("skill op body missing\n---\n%s", op.Content)
	}
	// Body should NOT have been wrapped in renderMDC frontmatter — that
	// would double the frontmatter the parser already produced.
	if strings.Count(op.Content, "---\n") < 2 {
		t.Errorf("expected source body's own frontmatter to be preserved\n---\n%s", op.Content)
	}
	// Sanity: must not project to the legacy rules path anymore.
	for _, o := range ops {
		if strings.HasPrefix(o.Path, ".cursor/rules/skill-") {
			t.Errorf("unexpected legacy rules-form skill projection: %s", o.Path)
		}
	}
}

// TestCursor_Skill_WithScripts verifies that skill scripts co-locate at
// .cursor/skills/<slug>/scripts/<basename>.
func TestCursor_Skill_WithScripts(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Skills: []*model.Skill{
			{
				Name: "format-go",
				Document: &model.Document{
					SourcePath: "/tmp/proj/.agents/skills/format-go/SKILL.md",
					Body:       "format go body",
				},
				Scripts: []string{
					"/tmp/proj/.agents/skills/format-go/scripts/run.sh",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (SKILL.md + script), got %d (%v)", len(ops), opsPaths(ops))
	}
	wantScript := ".cursor/skills/format-go/scripts/run.sh"
	foundScript := false
	for _, op := range ops {
		if op.Path == wantScript {
			foundScript = true
			if op.Kind != plugin.OpSymlink {
				t.Errorf("script op kind = %v, want OpSymlink", op.Kind)
			}
		}
	}
	if !foundScript {
		t.Errorf("missing script op at %q; got: %v", wantScript, opsPaths(ops))
	}
}

// TestCursor_Command_Projects verifies a slash command projects to
// .cursor/commands/<name>.md with NO frontmatter — bare markdown only.
func TestCursor_Command_Projects(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Commands: []*model.Command{
			{
				Name:        "deploy",
				Description: "Deploy to staging",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/commands/deploy.md",
					Body:       "Deploy the service to staging.\n\nUsage: /deploy [env]",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d (%v)", len(ops), opsPaths(ops))
	}
	op := ops[0]
	wantPath := ".cursor/commands/deploy.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if strings.HasPrefix(strings.TrimLeft(op.Content, " \n"), "---") {
		t.Errorf("command body should have no frontmatter:\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "Deploy the service to staging.") {
		t.Errorf("command body missing\n---\n%s", op.Content)
	}
	if !strings.HasSuffix(op.Content, "\n") {
		t.Errorf("command content should end with newline:\n%q", op.Content)
	}
}

// TestCursor_ScopedCommand verifies a scoped command projects to
// .cursor/commands/<scope-slug>-<name>.md (filename prefix preserves
// disambiguation across scopes) and carries an info warning explaining
// that Cursor commands are globally scoped.
func TestCursor_ScopedCommand(t *testing.T) {
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
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d (%v)", len(ops), opsPaths(ops))
	}
	op := ops[0]
	wantPath := ".cursor/commands/src-billing-deploy.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "Cursor commands are global") && strings.Contains(w.Message, "deploy") {
			found = true
			if w.Severity != "info" {
				t.Errorf("warning severity = %q, want info", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected scoped-command degrade warning, got %#v", op.Warnings)
	}
}

// TestCursor_Agent_Projects verifies a subagent projects to
// .cursor/agents/<name>.md (body verbatim — parser produces frontmatter).
func TestCursor_Agent_Projects(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Agents: []*model.Agent{
			{
				Name:        "code-reviewer",
				Description: "Reviews code for style and bugs",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/code-reviewer.md",
					Body:       "---\nname: code-reviewer\ndescription: Reviews code for style and bugs\nmodel: inherit\n---\n\nYou are a careful code reviewer.",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d (%v)", len(ops), opsPaths(ops))
	}
	op := ops[0]
	wantPath := ".cursor/agents/code-reviewer.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if op.Kind != plugin.OpWrite {
		t.Errorf("kind = %v, want OpWrite", op.Kind)
	}
	if !strings.Contains(op.Content, "name: code-reviewer") {
		t.Errorf("body should carry source frontmatter:\n%s", op.Content)
	}
	if !strings.Contains(op.Content, "You are a careful code reviewer.") {
		t.Errorf("body missing\n---\n%s", op.Content)
	}
	if len(op.Warnings) != 0 {
		t.Errorf("global agent should not warn, got %#v", op.Warnings)
	}
}

// TestCursor_ScopedAgent verifies a scoped agent projects with a
// scope-prefixed filename and carries a degrade info warning.
func TestCursor_ScopedAgent(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/proj/.agents",
		Agents: []*model.Agent{
			{
				Name:      "reviewer",
				ScopePath: "src/billing",
				Document: &model.Document{
					SourcePath: "/tmp/proj/.agents/src/billing/agents/reviewer.md",
					Body:       "scoped reviewer body",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".cursor/agents/src-billing-reviewer.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	found := false
	for _, w := range op.Warnings {
		if strings.Contains(w.Message, "Cursor agents are global") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected scoped-agent degrade warning, got %#v", op.Warnings)
	}
}

// TestCursor_Hooks_Emit verifies global hooks land in .cursor/hooks.json
// with the prism → Cursor event mapping applied (PreToolUse → preToolUse,
// etc.). Verifies the JSON shape: {"version":1,"hooks":{"<evt>":[...]}}.
func TestCursor_Hooks_Emit(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: "/tmp/proj/.agents/hooks/audit.sh",
			},
			{
				Event:      "PostToolUse",
				Matcher:    "Edit",
				ScriptPath: "/tmp/proj/.agents/hooks/log.sh",
			},
			{
				Event:      "SessionStart",
				ScriptPath: "/tmp/proj/.agents/hooks/welcome.sh",
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	var hooksOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".cursor/hooks.json" {
			hooksOp = &ops[i]
		}
	}
	if hooksOp == nil {
		t.Fatalf("missing .cursor/hooks.json op; got: %v", opsPaths(ops))
	}
	if hooksOp.Kind != plugin.OpWrite {
		t.Errorf("hooks op kind = %v, want OpWrite", hooksOp.Kind)
	}
	if hooksOp.Mode != plugin.ModeWrite {
		t.Errorf("hooks op mode = %v, want ModeWrite", hooksOp.Mode)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(hooksOp.Content), &doc); err != nil {
		t.Fatalf("hooks.json does not parse: %v\n---\n%s", err, hooksOp.Content)
	}
	if v, _ := doc["version"].(float64); v != 1 {
		t.Errorf("version = %v, want 1", doc["version"])
	}
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing or wrong type: %v", doc["hooks"])
	}
	for _, evt := range []string{"preToolUse", "postToolUse", "sessionStart"} {
		entries, ok := hooks[evt].([]any)
		if !ok || len(entries) == 0 {
			t.Errorf("hooks[%q] missing or empty: %v", evt, hooks[evt])
		}
	}
	// preToolUse entry should carry matcher="Bash" and the script absolute path.
	preEntries, _ := hooks["preToolUse"].([]any)
	if len(preEntries) != 1 {
		t.Fatalf("preToolUse entries = %d, want 1", len(preEntries))
	}
	pre, _ := preEntries[0].(map[string]any)
	if pre["matcher"] != "Bash" {
		t.Errorf("preToolUse[0].matcher = %v, want Bash", pre["matcher"])
	}
	if pre["command"] != "/tmp/proj/.agents/hooks/audit.sh" {
		t.Errorf("preToolUse[0].command = %v, want audit.sh path", pre["command"])
	}
	// sessionStart entry should omit empty matcher (no matcher → key absent).
	startEntries, _ := hooks["sessionStart"].([]any)
	if len(startEntries) != 1 {
		t.Fatalf("sessionStart entries = %d, want 1", len(startEntries))
	}
	start, _ := startEntries[0].(map[string]any)
	if _, has := start["matcher"]; has {
		t.Errorf("sessionStart entry should omit matcher when empty, got %v", start)
	}
}

// TestCursor_Hooks_UnsupportedEvent_Warns confirms events with no Cursor
// analog (Notification, SubagentStop, PreCompact) are dropped with an
// info warning, and do not appear in hooks.json.
func TestCursor_Hooks_UnsupportedEvent_Warns(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/proj",
		AgentsDir: "/tmp/proj/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/proj/.agents/context.md",
			Body:       "ctx",
		},
		Hooks: []*model.Hook{
			{
				Event:      "Notification",
				ScriptPath: "/tmp/proj/.agents/hooks/notify.sh",
			},
			{
				Event:      "PreToolUse",
				ScriptPath: "/tmp/proj/.agents/hooks/audit.sh",
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var hooksOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".cursor/hooks.json" {
			hooksOp = &ops[i]
		}
	}
	if hooksOp == nil {
		t.Fatalf("expected hooks.json op (one usable hook present)")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(hooksOp.Content), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if _, has := hooks["Notification"]; has {
		t.Errorf("Notification should not be in hooks.json")
	}
	// Warning must be present somewhere in the projection.
	found := false
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "Notification") && strings.Contains(w.Message, "no analog") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected info warning for dropped Notification hook")
	}
}

// TestCursor_ScopedHook_WrapperEmitted verifies a scoped hook produces
// both a wrapper script at .cursor/hooks/__scope-guard__/... AND a
// hooks.json entry whose command points at the wrapper's absolute path.
func TestCursor_ScopedHook_WrapperEmitted(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")
	sourceScript := filepath.Join(agentsDir, "src", "billing", "hooks", "guard.sh")

	proj := &model.Project{
		Root:      root,
		AgentsDir: agentsDir,
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Edit",
				ScriptPath: sourceScript,
				ScopePath:  "src/billing",
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantWrapperRel := ".cursor/hooks/__scope-guard__/src-billing-preToolUse-guard.sh"
	wantWrapperAbs := filepath.Join(root, wantWrapperRel)

	var wrapperOp *plugin.Operation
	var hooksOp *plugin.Operation
	for i := range ops {
		switch ops[i].Path {
		case wantWrapperRel:
			wrapperOp = &ops[i]
		case ".cursor/hooks.json":
			hooksOp = &ops[i]
		}
	}
	if wrapperOp == nil {
		t.Fatalf("missing wrapper script at %q; got paths:\n%v", wantWrapperRel, opsPaths(ops))
	}
	if wrapperOp.Kind != plugin.OpWrite {
		t.Errorf("wrapper kind = %v, want OpWrite", wrapperOp.Kind)
	}
	if wrapperOp.FileMode != 0o755 {
		t.Errorf("wrapper FileMode = %o, want 0755", wrapperOp.FileMode)
	}
	if !strings.Contains(wrapperOp.Content, "prism scope-guard") {
		t.Errorf("wrapper body missing scope-guard invocation:\n%s", wrapperOp.Content)
	}
	if !strings.Contains(wrapperOp.Content, "--scope 'src/billing'") {
		t.Errorf("wrapper body missing --scope flag:\n%s", wrapperOp.Content)
	}
	relScript, _ := filepath.Rel(root, sourceScript)
	if !strings.Contains(wrapperOp.Content, `--script "${PROJECT_DIR}"/'`+relScript+`'`) {
		t.Errorf("wrapper body missing --script ${PROJECT_DIR}/<rel>:\n%s", wrapperOp.Content)
	}

	if hooksOp == nil {
		t.Fatalf("missing hooks.json op; got paths: %v", opsPaths(ops))
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(hooksOp.Content), &doc); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	entries, _ := hooks["preToolUse"].([]any)
	if len(entries) != 1 {
		t.Fatalf("preToolUse entries = %d, want 1", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if entry["command"] != wantWrapperAbs {
		t.Errorf("hook command = %v, want wrapper abs %v", entry["command"], wantWrapperAbs)
	}
	if entry["matcher"] != "Edit" {
		t.Errorf("hook matcher = %v, want Edit", entry["matcher"])
	}
}

// TestCursor_ScopedHook_DisableWrappers verifies that when
// DisableHookWrappers is true, scoped hooks are emitted as global
// (no wrapper) — hooks.json command points at the source script.
func TestCursor_ScopedHook_DisableWrappers(t *testing.T) {
	root := t.TempDir()
	sourceScript := filepath.Join(root, ".agents", "hooks", "guard.sh")
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				ScriptPath: sourceScript,
				ScopePath:  "src/billing",
			},
		},
	}
	p := &CursorPlugin{DisableHookWrappers: true}
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.Path, "__scope-guard__") {
			t.Errorf("unexpected wrapper with DisableHookWrappers=true: %s", op.Path)
		}
	}
	var hooksOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".cursor/hooks.json" {
			hooksOp = &ops[i]
		}
	}
	if hooksOp == nil {
		t.Fatalf("missing hooks.json")
	}
	var doc map[string]any
	_ = json.Unmarshal([]byte(hooksOp.Content), &doc)
	hooks, _ := doc["hooks"].(map[string]any)
	entries, _ := hooks["preToolUse"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if entry["command"] != sourceScript {
		t.Errorf("command = %v, want source script %v", entry["command"], sourceScript)
	}
}

// TestCursor_Permissions_Warning verifies that non-empty Permissions emit
// no permissions file but attach an info warning to some op (Permissions
// remain SupportUnsupported in v0.8.0; sandbox profile generator slated
// for v0.8.1).
func TestCursor_Permissions_Warning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Permissions: &model.Permissions{
			Allow: []string{"Bash(ls)", "Read"},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	found := false
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "no permissions primitive") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected permissions warning, got %#v", ops[0].Warnings)
	}
}

// TestCursor_Permissions_EmptyNoWarning verifies that an empty Permissions
// struct (allocated but all lists nil/empty) does NOT emit a warning.
func TestCursor_Permissions_EmptyNoWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Context: &model.Document{
			SourcePath: "/tmp/.agents/context.md",
			Body:       "ctx",
		},
		Permissions: &model.Permissions{},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	for _, w := range ops[0].Warnings {
		if strings.Contains(w.Message, "permissions") {
			t.Errorf("unexpected permissions warning on empty permissions: %#v", w)
		}
	}
}

// TestCursor_MCP verifies that proj.MCP servers project to .cursor/mcp.json
// via OpMerge and that existing unrelated top-level keys at
// <root>/.cursor/mcp.json are preserved.
func TestCursor_MCP(t *testing.T) {
	root := t.TempDir()
	cursorDir := filepath.Join(root, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := `{
  "experimental": true,
  "mcpServers": {
    "stale": { "command": "this-will-be-overwritten" }
  }
}
`
	if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
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

	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}

	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".cursor/mcp.json" {
			mcpOp = &ops[i]
			break
		}
	}
	if mcpOp == nil {
		t.Fatalf("no .cursor/mcp.json op found in %d ops", len(ops))
	}
	if mcpOp.Kind != plugin.OpMerge {
		t.Errorf("mcp op kind = %v, want OpMerge", mcpOp.Kind)
	}
	if mcpOp.Mode != plugin.ModeWrite {
		t.Errorf("mcp op mode = %v, want ModeWrite", mcpOp.Mode)
	}
	if len(mcpOp.Sources) != 1 || mcpOp.Sources[0] != "mcp.yaml" {
		t.Errorf("mcp op sources = %v, want [mcp.yaml]", mcpOp.Sources)
	}

	mergedContent := mergeContent(t, root, mcpOp)
	var out map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &out); err != nil {
		t.Fatalf("mcp op content does not parse as JSON: %v\n---\n%s", err, mergedContent)
	}
	if exp, ok := out["experimental"].(bool); !ok || !exp {
		t.Errorf("expected experimental=true preserved; got %v", out["experimental"])
	}
	servers, ok := out["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or wrong type: %v", out["mcpServers"])
	}
	linear, ok := servers["linear"].(map[string]any)
	if !ok {
		t.Fatalf("linear server missing or wrong type: %v", servers["linear"])
	}
	if linear["command"] != "npx" {
		t.Errorf("linear.command = %v, want npx", linear["command"])
	}
	args, ok := linear["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "-y" || args[1] != "@linear/mcp" {
		t.Errorf("linear.args = %v, want [-y @linear/mcp]", linear["args"])
	}
	env, ok := linear["env"].(map[string]any)
	if !ok || env["LINEAR_TOKEN"] != "xxx" {
		t.Errorf("linear.env = %v, want {LINEAR_TOKEN: xxx}", linear["env"])
	}
	remote, ok := servers["remote-tools"].(map[string]any)
	if !ok {
		t.Fatalf("remote-tools missing or wrong type: %v", servers["remote-tools"])
	}
	if remote["url"] != "https://mcp.example.com/sse" {
		t.Errorf("remote-tools.url = %v, want https://mcp.example.com/sse", remote["url"])
	}
	if _, exists := servers["stale"]; exists {
		t.Errorf("stale server should have been replaced; got %v", servers["stale"])
	}
}

// TestCursor_MCP_NoExisting verifies that without an existing mcp.json,
// the op still emits with just the new servers.
func TestCursor_MCP_NoExisting(t *testing.T) {
	root := t.TempDir()
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		MCP: []*model.MCPServer{
			{Name: "linear", Command: "npx"},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	var mcpOp *plugin.Operation
	for i := range ops {
		if ops[i].Path == ".cursor/mcp.json" {
			mcpOp = &ops[i]
		}
	}
	if mcpOp == nil {
		t.Fatalf("no mcp op")
	}
	mergedContent := mergeContent(t, root, mcpOp)
	var out map[string]any
	if err := json.Unmarshal([]byte(mergedContent), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	servers, _ := out["mcpServers"].(map[string]any)
	if _, ok := servers["linear"]; !ok {
		t.Errorf("expected linear in output: %s", mergedContent)
	}
}

// TestCursor_ScopedSkill verifies the new dedicated-format path includes
// the scope-slug prefix to disambiguate same-named skills across scopes.
func TestCursor_ScopedSkill(t *testing.T) {
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
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	wantPath := ".cursor/skills/src-billing-audit-trail/SKILL.md"
	if op.Path != wantPath {
		t.Errorf("path = %q, want %q", op.Path, wantPath)
	}
	if len(op.Sources) != 1 || !strings.Contains(op.Sources[0], "src/billing/skills/audit-trail/SKILL.md") {
		t.Errorf("Sources = %v, want path containing src/billing/skills/audit-trail/SKILL.md", op.Sources)
	}
}

// TestCursor_CrossEmitDedup_ExplicitTargets verifies that when both
// `claude` AND `cursor` are in proj.Config.Targets, the Cursor plugin
// emits EXACTLY ONE info warning ("emitting agents to .cursor/agents/
// only ...") at the project level, regardless of how many agents are
// present. Agent operations still land at .cursor/agents/<name>.md.
func TestCursor_CrossEmitDedup_ExplicitTargets(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Config: &model.Config{
			Targets: []string{"claude", "cursor"},
		},
		Agents: []*model.Agent{
			{
				Name: "alpha",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/alpha.md",
					Body:       "alpha body",
				},
			},
			{
				Name: "beta",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/beta.md",
					Body:       "beta body",
				},
			},
			{
				Name: "gamma",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/gamma.md",
					Body:       "gamma body",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Count dedup warnings across every op; expect exactly one.
	dedupWarnings := 0
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "emitting agents to .cursor/agents/ only") {
				dedupWarnings++
				if w.Severity != "info" {
					t.Errorf("dedup warning severity = %q, want info", w.Severity)
				}
			}
		}
	}
	if dedupWarnings != 1 {
		t.Errorf("expected exactly 1 dedup warning, got %d", dedupWarnings)
	}

	// Agents still land at .cursor/agents/<name>.md.
	wantPaths := map[string]bool{
		".cursor/agents/alpha.md": true,
		".cursor/agents/beta.md":  true,
		".cursor/agents/gamma.md": true,
	}
	gotAgents := map[string]bool{}
	for _, op := range ops {
		if strings.HasPrefix(op.Path, ".cursor/agents/") {
			gotAgents[op.Path] = true
		}
	}
	for want := range wantPaths {
		if !gotAgents[want] {
			t.Errorf("missing agent op at %q; got: %v", want, opsPaths(ops))
		}
	}
}

// TestCursor_CrossEmitDedup_CursorOnly_NoWarning verifies that when only
// `cursor` is enabled (no `claude` target), the dedup warning is NOT
// emitted. Agents continue to emit normally — this is the existing v0.8
// behavior and must remain unchanged.
func TestCursor_CrossEmitDedup_CursorOnly_NoWarning(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Config: &model.Config{
			Targets: []string{"cursor"},
		},
		Agents: []*model.Agent{
			{
				Name: "alpha",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/alpha.md",
					Body:       "alpha body",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "emitting agents to .cursor/agents/ only") {
				t.Errorf("unexpected dedup warning when claude target absent: %#v", w)
			}
		}
	}
	if len(ops) != 1 || ops[0].Path != ".cursor/agents/alpha.md" {
		t.Errorf("expected single agent op at .cursor/agents/alpha.md, got: %v", opsPaths(ops))
	}
}

// TestCursor_CrossEmitDedup_DisableOverride verifies that
// `extensions.cursor.disable_dedup: true` on the project config
// suppresses the cross-emit dedup warning even when both targets are
// active.
func TestCursor_CrossEmitDedup_DisableOverride(t *testing.T) {
	proj := &model.Project{
		AgentsDir: "/tmp/.agents",
		Config: &model.Config{
			Targets: []string{"claude", "cursor"},
			Extensions: map[string]any{
				"cursor": map[string]any{
					"disable_dedup": true,
				},
			},
		},
		Agents: []*model.Agent{
			{
				Name: "alpha",
				Document: &model.Document{
					SourcePath: "/tmp/.agents/agents/alpha.md",
					Body:       "alpha body",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "emitting agents to .cursor/agents/ only") {
				t.Errorf("disable_dedup override should suppress warning, got: %#v", w)
			}
		}
	}
}

// TestCursor_CrossEmitDedup_Autodetect verifies that when no explicit
// Targets list is set but `.claude/` exists under proj.Root, the dedup
// warning still fires (autodetect mode).
func TestCursor_CrossEmitDedup_Autodetect(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Agents: []*model.Agent{
			{
				Name: "alpha",
				Document: &model.Document{
					SourcePath: filepath.Join(root, ".agents/agents/alpha.md"),
					Body:       "alpha body",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dedupWarnings := 0
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "emitting agents to .cursor/agents/ only") {
				dedupWarnings++
			}
		}
	}
	if dedupWarnings != 1 {
		t.Errorf("expected 1 autodetect dedup warning, got %d", dedupWarnings)
	}
}

// TestCursor_CrossEmitDedup_Autodetect_NoClaudeDir verifies that
// without an explicit Targets list AND no `.claude/` under proj.Root,
// the dedup warning is NOT emitted.
func TestCursor_CrossEmitDedup_Autodetect_NoClaudeDir(t *testing.T) {
	root := t.TempDir()
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Agents: []*model.Agent{
			{
				Name: "alpha",
				Document: &model.Document{
					SourcePath: filepath.Join(root, ".agents/agents/alpha.md"),
					Body:       "alpha body",
				},
			},
		},
	}
	p := NewCursor()
	ops, err := p.Plan(proj, model.TargetOption{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, op := range ops {
		for _, w := range op.Warnings {
			if strings.Contains(w.Message, "emitting agents to .cursor/agents/ only") {
				t.Errorf("unexpected dedup warning when no .claude/ dir present: %#v", w)
			}
		}
	}
}

// TestCursor_ScopedSkillCollision verifies that the same skill name in two
// different scopes produces two distinct files, one per scope.
func TestCursor_ScopedSkillCollision(t *testing.T) {
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
	p := NewCursor()
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
	if !paths[".cursor/skills/src-billing-validator/SKILL.md"] {
		t.Errorf("missing billing-validator op, got: %v", paths)
	}
	if !paths[".cursor/skills/src-auth-validator/SKILL.md"] {
		t.Errorf("missing auth-validator op, got: %v", paths)
	}
}
