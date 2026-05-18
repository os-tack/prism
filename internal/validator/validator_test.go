package validator

import (
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

// hasError reports whether any error in r references the given field
// substring. Used by per-rule tests so we can match on the canonical
// dot-path without depending on exact message wording.
func hasError(r ValidationReport, fieldSubstr string) bool {
	for _, e := range r.Errors {
		if strings.Contains(e.Field, fieldSubstr) {
			return true
		}
	}
	return false
}

// hasWarning is the warning counterpart of hasError.
func hasWarning(r ValidationReport, fieldSubstr string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w.Field, fieldSubstr) {
			return true
		}
	}
	return false
}

func ptrBool(b bool) *bool { return &b }

// --- name-required tests --------------------------------------------------

func TestAgentMissingName(t *testing.T) {
	proj := &model.Project{
		Agents: []*model.Agent{
			{Name: "", Description: "a thing"},
		},
	}
	r := Validate(proj)
	if !hasError(r, "agents.<unnamed>.name") {
		t.Fatalf("expected agents.<unnamed>.name error, got %+v", r.Errors)
	}
}

func TestSkillMissingName(t *testing.T) {
	proj := &model.Project{
		Skills: []*model.Skill{
			{Name: "", Description: "do a thing"},
		},
	}
	r := Validate(proj)
	if !hasError(r, "skills.<unnamed>.name") {
		t.Fatalf("expected skills.<unnamed>.name error, got %+v", r.Errors)
	}
}

func TestCommandMissingName(t *testing.T) {
	proj := &model.Project{
		Commands: []*model.Command{
			{Name: ""},
		},
	}
	r := Validate(proj)
	if !hasError(r, "commands.<unnamed>.name") {
		t.Fatalf("expected commands.<unnamed>.name error, got %+v", r.Errors)
	}
}

// --- description-required tests -------------------------------------------

func TestSkillMissingDescriptionForModelDecision(t *testing.T) {
	// Skill with Modes including ModelDecision (default fill) and no
	// description should error.
	proj := &model.Project{
		Skills: []*model.Skill{
			{Name: "stripe", Description: ""},
		},
	}
	r := Validate(proj)
	if !hasError(r, "skills.stripe.description") {
		t.Fatalf("expected skills.stripe.description error, got %+v", r.Errors)
	}
	// Mutation contract: empty Modes filled with ModelDecision.
	if len(proj.Skills[0].Activation.Modes) != 1 ||
		proj.Skills[0].Activation.Modes[0] != model.SkillActivationModelDecision {
		t.Fatalf("expected Activation.Modes filled with [ModelDecision], got %+v",
			proj.Skills[0].Activation.Modes)
	}
}

func TestScopeMissingDescriptionForModelDecision(t *testing.T) {
	proj := &model.Project{
		Scopes: []*model.Scope{
			{Name: "ctx", Activation: model.ScopeActivationModelDecision},
		},
	}
	r := Validate(proj)
	if !hasError(r, "scopes.ctx.description") {
		t.Fatalf("expected scopes.ctx.description error, got %+v", r.Errors)
	}
}

// --- glob-required tests --------------------------------------------------

func TestSkillMissingGlobsForGlobMode(t *testing.T) {
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "stripe",
			Description: "verify stripe webhooks",
			Activation: model.SkillActivation{
				Modes: []model.SkillActivationMode{model.SkillActivationGlob},
			},
		}},
	}
	r := Validate(proj)
	if !hasError(r, "skills.stripe.activation.globs") {
		t.Fatalf("expected skills.stripe.activation.globs error, got %+v", r.Errors)
	}
}

func TestScopeMissingGlobsForGlobActivation(t *testing.T) {
	proj := &model.Project{
		Scopes: []*model.Scope{{
			Name:        "billing",
			Description: "billing rules",
			Activation:  model.ScopeActivationGlob,
		}},
	}
	r := Validate(proj)
	if !hasError(r, "scopes.billing.globs") {
		t.Fatalf("expected scopes.billing.globs error, got %+v", r.Errors)
	}
}

// --- ContentRegex syntax --------------------------------------------------

func TestSkillContentRegexInvalid(t *testing.T) {
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "buggy",
			Description: "regex sanity",
			Activation: model.SkillActivation{
				ContentRegex: "[unclosed",
			},
		}},
	}
	r := Validate(proj)
	if !hasError(r, "skills.buggy.activation.content_regex") {
		t.Fatalf("expected content_regex compile error, got %+v", r.Errors)
	}
}

// --- ScopePath containment ------------------------------------------------

func TestScopePathDoubleDot(t *testing.T) {
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "esc",
			Description: "escaping scope",
			ScopePath:   "../outside",
		}},
	}
	r := Validate(proj)
	if !hasError(r, "skills.esc.scope_path") {
		t.Fatalf("expected scope_path escape error, got %+v", r.Errors)
	}
}

func TestScopePathAbsolute(t *testing.T) {
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "abs",
			Description: "absolute scope",
			ScopePath:   "/etc/secrets",
		}},
	}
	r := Validate(proj)
	if !hasError(r, "skills.abs.scope_path") {
		t.Fatalf("expected scope_path absolute-prefix error, got %+v", r.Errors)
	}
}

func TestScopePathTildePrefix(t *testing.T) {
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "tilde",
			Description: "tilde scope",
			ScopePath:   "~/home",
		}},
	}
	r := Validate(proj)
	if !hasError(r, "skills.tilde.scope_path") {
		t.Fatalf("expected scope_path tilde-prefix error, got %+v", r.Errors)
	}
}

// --- MCP transport --------------------------------------------------------

func TestMCPTransportInferenceStdio(t *testing.T) {
	proj := &model.Project{
		MCP: []*model.MCPServer{{
			Name:    "local-fs",
			Command: "/usr/bin/fs-mcp",
		}},
	}
	r := Validate(proj)
	if len(r.Errors) != 0 {
		t.Fatalf("expected no errors after stdio inference, got %+v", r.Errors)
	}
	if proj.MCP[0].Transport != "stdio" {
		t.Fatalf("expected transport inferred to stdio, got %q", proj.MCP[0].Transport)
	}
}

func TestMCPTransportInferenceHTTP(t *testing.T) {
	proj := &model.Project{
		MCP: []*model.MCPServer{{
			Name: "remote",
			URL:  "https://mcp.example.com",
		}},
	}
	r := Validate(proj)
	if len(r.Errors) != 0 {
		t.Fatalf("expected no errors after http inference, got %+v", r.Errors)
	}
	if proj.MCP[0].Transport != "http" {
		t.Fatalf("expected transport inferred to http, got %q", proj.MCP[0].Transport)
	}
}

func TestMCPTransportAmbiguousBothSet(t *testing.T) {
	proj := &model.Project{
		MCP: []*model.MCPServer{{
			Name:    "ambig",
			Command: "/x",
			URL:     "https://y",
		}},
	}
	r := Validate(proj)
	if !hasError(r, "mcp.ambig.transport") {
		t.Fatalf("expected ambiguous transport error, got %+v", r.Errors)
	}
}

func TestMCPTransportAmbiguousNeitherSet(t *testing.T) {
	proj := &model.Project{
		MCP: []*model.MCPServer{{Name: "blank"}},
	}
	r := Validate(proj)
	if !hasError(r, "mcp.blank.transport") {
		t.Fatalf("expected ambiguous-transport error for empty server, got %+v", r.Errors)
	}
}

func TestMCPTransportUnknownValue(t *testing.T) {
	proj := &model.Project{
		MCP: []*model.MCPServer{{
			Name:      "weird",
			Transport: "websocket",
			URL:       "wss://x",
		}},
	}
	r := Validate(proj)
	if !hasError(r, "mcp.weird.transport") {
		t.Fatalf("expected unknown-transport error, got %+v", r.Errors)
	}
}

func TestMCPStdioMissingCommand(t *testing.T) {
	proj := &model.Project{
		MCP: []*model.MCPServer{{
			Name:      "stdio-no-cmd",
			Transport: "stdio",
		}},
	}
	r := Validate(proj)
	if !hasError(r, "mcp.stdio-no-cmd.command") {
		t.Fatalf("expected stdio-requires-command error, got %+v", r.Errors)
	}
}

func TestMCPHTTPMissingURL(t *testing.T) {
	proj := &model.Project{
		MCP: []*model.MCPServer{{
			Name:      "http-no-url",
			Transport: "http",
		}},
	}
	r := Validate(proj)
	if !hasError(r, "mcp.http-no-url.url") {
		t.Fatalf("expected http-requires-url error, got %+v", r.Errors)
	}
}

// --- Hook event -----------------------------------------------------------

func TestHookUnknownEvent(t *testing.T) {
	proj := &model.Project{
		Hooks: []*model.Hook{{
			Name:           "bogus",
			EventCanonical: model.HookEvent("not_a_real_event"),
		}},
	}
	r := Validate(proj)
	if !hasError(r, "hooks.bogus.event") {
		t.Fatalf("expected unknown-event error, got %+v", r.Errors)
	}
}

func TestHookNativeEventOK(t *testing.T) {
	proj := &model.Project{
		Hooks: []*model.Hook{{
			Name:           "native-thing",
			EventCanonical: model.HookEvent("native:cursor.something"),
		}},
	}
	r := Validate(proj)
	if hasError(r, "hooks.native-thing.event") {
		t.Fatalf("native: prefix should be accepted, got errors %+v", r.Errors)
	}
}

// --- Skill UserInvocable/ModelInvocable both false ------------------------

func TestSkillBothInvocableFalse(t *testing.T) {
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "inaccessible",
			Description: "broken",
			Activation: model.SkillActivation{
				UserInvocable:  ptrBool(false),
				ModelInvocable: ptrBool(false),
			},
		}},
	}
	r := Validate(proj)
	if !hasError(r, "skills.inaccessible.activation") {
		t.Fatalf("expected inaccessible-skill error, got %+v", r.Errors)
	}
}

// --- Permissions grammar --------------------------------------------------

func TestPermissionRegexRejected(t *testing.T) {
	proj := &model.Project{
		Permissions: &model.Permissions{
			Allow: []string{`bash:^npm test$`},
		},
	}
	r := Validate(proj)
	if !hasError(r, "permissions.allow[0]") {
		t.Fatalf("expected regex-rejected error, got %+v", r.Errors)
	}
}

func TestPermissionUnknownTarget(t *testing.T) {
	proj := &model.Project{
		Permissions: &model.Permissions{
			Deny: []string{"frobnicate:*"},
		},
	}
	r := Validate(proj)
	if !hasError(r, "permissions.deny[0]") {
		t.Fatalf("expected unknown-target error, got %+v", r.Errors)
	}
}

func TestPermissionValidRules(t *testing.T) {
	proj := &model.Project{
		Permissions: &model.Permissions{
			Allow: []string{"bash:go test *", "fs:src/**", "mcp:github:*"},
			Deny:  []string{"bash:rm -rf *", "fs:!src/billing/migrations/*"},
			Ask:   []string{"Edit:.env*"},
		},
	}
	r := Validate(proj)
	if len(r.Errors) != 0 {
		t.Fatalf("expected no errors for valid rules, got %+v", r.Errors)
	}
}

// --- Tools + DisallowedTools warning --------------------------------------

func TestAgentToolsAndDisallowedToolsWarning(t *testing.T) {
	proj := &model.Project{
		Config: &model.Config{Targets: []string{"cursor", "claude"}},
		Agents: []*model.Agent{{
			Name:            "rev",
			Description:     "review",
			Tools:           []string{"Read"},
			DisallowedTools: []string{"Bash"},
		}},
	}
	r := Validate(proj)
	if !hasWarning(r, "agents.rev.disallowed_tools") {
		t.Fatalf("expected tools+disallowed_tools warning, got %+v", r.Warnings)
	}
}

// --- Extensions block typo catcher ----------------------------------------

func TestExtensionsUnknownPluginWarning(t *testing.T) {
	doc := &model.Document{
		SourcePath: "skills/foo/SKILL.md",
		Frontmatter: map[string]any{
			"extensions": map[string]any{
				"clauded": map[string]any{"effort": "high"},
			},
		},
	}
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "foo",
			Description: "thing",
			Document:    doc,
		}},
	}
	r := Validate(proj)
	if !hasWarning(r, "extensions.clauded") {
		t.Fatalf("expected unknown-plugin warning for `clauded`, got %+v", r.Warnings)
	}
}

func TestExtensionsKnownPluginNoWarning(t *testing.T) {
	doc := &model.Document{
		SourcePath: "skills/foo/SKILL.md",
		Frontmatter: map[string]any{
			"extensions": map[string]any{
				"claude": map[string]any{"effort": "high"},
			},
		},
	}
	proj := &model.Project{
		Skills: []*model.Skill{{
			Name:        "foo",
			Description: "thing",
			Document:    doc,
		}},
	}
	r := Validate(proj)
	for _, w := range r.Warnings {
		if strings.Contains(w.Field, "extensions.claude") {
			t.Fatalf("did not expect warning for known plugin `claude`, got %+v", w)
		}
	}
}

// --- Agent empty body+description warning ---------------------------------

func TestAgentEmptyBodyAndDescriptionWarning(t *testing.T) {
	proj := &model.Project{
		Agents: []*model.Agent{{
			Name: "blank",
		}},
	}
	r := Validate(proj)
	if !hasWarning(r, "agents.blank.description") {
		t.Fatalf("expected blank-agent warning, got %+v", r.Warnings)
	}
}

// --- @include cycle detection ---------------------------------------------

func TestIncludeCycleDetection(t *testing.T) {
	a := &model.Document{SourcePath: "a.md", Includes: []string{"b.md"}}
	b := &model.Document{SourcePath: "b.md", Includes: []string{"a.md"}}
	proj := &model.Project{
		Skills: []*model.Skill{
			{Name: "a", Description: "a", Document: a},
			{Name: "b", Description: "b", Document: b},
		},
	}
	r := Validate(proj)
	cycleFound := false
	for _, e := range r.Errors {
		if e.Field == "includes" {
			cycleFound = true
			break
		}
	}
	if !cycleFound {
		t.Fatalf("expected @include cycle error, got %+v", r.Errors)
	}
}

// --- Fully-valid project produces no errors -------------------------------

func TestFullyValidProjectNoErrors(t *testing.T) {
	doc := &model.Document{
		SourcePath: "skills/stripe-webhook/SKILL.md",
		Frontmatter: map[string]any{
			"name":        "stripe-webhook",
			"description": "verify stripe sigs",
			"activation":  map[string]any{},
			"extensions": map[string]any{
				"claude": map[string]any{"effort": "high"},
			},
		},
	}
	proj := &model.Project{
		Config: &model.Config{Targets: []string{"claude"}},
		Agents: []*model.Agent{{
			Name:         "reviewer",
			Description:  "code review",
			SystemPrompt: "You are a reviewer.",
		}},
		Skills: []*model.Skill{{
			Name:        "stripe-webhook",
			Description: "verify stripe sigs",
			Document:    doc,
			Activation: model.SkillActivation{
				Modes: []model.SkillActivationMode{
					model.SkillActivationModelDecision,
					model.SkillActivationGlob,
				},
				Globs: []string{"app/webhooks/stripe/**"},
			},
		}},
		Commands: []*model.Command{{Name: "deploy"}},
		Hooks: []*model.Hook{{
			Name:           "block-secrets",
			EventCanonical: model.EventPreToolUse,
		}},
		MCP: []*model.MCPServer{{
			Name:      "github",
			Transport: "http",
			URL:       "https://mcp.github.com",
		}},
		Permissions: &model.Permissions{
			Allow: []string{"bash:go test *"},
			Deny:  []string{"bash:rm -rf *"},
		},
		Scopes: []*model.Scope{{
			Name:        "billing",
			Path:        "src/billing",
			Description: "billing rules",
			Activation:  model.ScopeActivationCascade,
		}},
	}
	r := Validate(proj)
	if len(r.Errors) != 0 {
		t.Fatalf("expected no errors for fully-valid project, got %+v", r.Errors)
	}
}
