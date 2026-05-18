// Package validator implements prism v0.9's canonical pre-plugin validation
// pass (SPEC §5.4). Validate runs over a parsed model.Project, applies the
// fully-enumerated mutation contract (default-fills Skill.Activation.Modes
// and infers MCPServer.Transport), and returns a ValidationReport.
//
// Plugins consume model.Project AFTER Validate has succeeded with no
// errors. Plugins MUST NOT default-fill canonical fields themselves; any
// "treat absent as X" semantic relies on Validate having normalized it.
package validator

import (
	"fmt"
	"regexp"
	"strings"

	"agents.dev/agents/internal/model"
)

// ValidationError is one validation finding. File is .agents/-relative
// where known; Line/Column are 1-based or 0 when not applicable; Field is
// a canonical dot-path (e.g. "skills.stripe-webhook.activation.globs");
// Severity is "error" or "warning".
type ValidationError struct {
	File     string
	Line     int
	Column   int
	Field    string
	Severity string
	Message  string
}

// ValidationReport groups the report's errors and warnings.
type ValidationReport struct {
	Errors   []ValidationError
	Warnings []ValidationError
}

// knownPlugins is the canonical list of registered plugin names. Used by
// the extensions-block typo-catcher.
var knownPlugins = map[string]bool{
	"claude":   true,
	"cursor":   true,
	"gemini":   true,
	"copilot":  true,
	"cline":    true,
	"continue": true,
	"windsurf": true,
	"agentsmd": true,
}

// canonicalFrontmatterKeys lists the top-level frontmatter keys the
// validator recognizes as part of the canonical schema. Unknown keys
// (outside `extensions:`) produce a warning. This list is best-effort:
// per SPEC §4 each primitive declares its own canonical keys; this set
// is the union.
var canonicalFrontmatterKeys = map[string]bool{
	"name":                     true,
	"description":              true,
	"activation":               true,
	"globs":                    true,
	"paths":                    true,
	"trigger":                  true,
	"when_to_use":              true,
	"allowed_tools":            true,
	"allowed-tools":            true,
	"arguments":                true,
	"argument_hint":            true,
	"argument-hint":            true,
	"scripts":                  true,
	"references":               true,
	"model":                    true,
	"models":                   true,
	"model_fallbacks":          true,
	"subagent":                 true,
	"tools":                    true,
	"disallowed_tools":         true,
	"disallowed-tools":         true,
	"read_only":                true,
	"readonly":                 true,
	"background":               true,
	"is_background":            true,
	"max_turns":                true,
	"max-turns":                true,
	"temperature":              true,
	"mcp_servers":              true,
	"mcpServers":               true,
	"mcp-servers":              true,
	"allowed_subagents":        true,
	"user_invocable":           true,
	"user-invocable":           true,
	"model_invocable":          true,
	"disable-model-invocation": true,
	"initial_prompt":           true,
	"priority":                 true,
	"tags":                     true,
	"is_override":              true,
	"scope_path":               true,
	"event":                    true,
	"matcher":                  true,
	"handlers":                 true,
	"sequential":               true,
	"disabled":                 true,
	"transport":                true,
	"command":                  true,
	"args":                     true,
	"env":                      true,
	"cwd":                      true,
	"url":                      true,
	"headers":                  true,
	"auth":                     true,
	"timeout_ms":               true,
	"timeout":                  true,
	"auto_approve":             true,
	"autoApprove":              true,
	"trust":                    true,
	"include_tools":            true,
	"exclude_tools":            true,
	"includeTools":             true,
	"excludeTools":             true,
	"allow":                    true,
	"deny":                     true,
	"ask":                      true,
	"alwaysApply":              true,
	"extensions":               true,
	"applyTo":                  true,
}

// recognizedPermTargets is the canonical set of permission rule targets
// per SPEC §4.6.2.
var recognizedPermTargets = map[string]bool{
	"bash":      true,
	"read":      true,
	"write":     true,
	"edit":      true,
	"multiedit": true,
	"fs":        true,
	"network":   true,
	"mcp":       true,
	"webfetch":  true,
}

// Validate runs the canonical v0.9 validation rules over proj. It MAY
// mutate proj in the small set of ways enumerated in SPEC §5.4 (default
// Skill.Activation.Modes; infer MCPServer.Transport). The caller decides
// whether errors block compile.
func Validate(proj *model.Project) ValidationReport {
	r := &ValidationReport{}
	if proj == nil {
		return *r
	}

	validateAgents(proj, r)
	validateSkills(proj, r)
	validateCommands(proj, r)
	validateHooks(proj, r)
	validateMCPServers(proj, r)
	validatePermissions(proj, r)
	validateScopes(proj, r)
	validateIncludeCycles(proj, r)
	validateExtensionsAndFrontmatter(proj, r)

	return *r
}

// --- Agents ---------------------------------------------------------------

func validateAgents(proj *model.Project, r *ValidationReport) {
	hasNonClaudeTarget := projectHasNonClaudeTarget(proj)
	for _, a := range proj.Agents {
		base := fmt.Sprintf("agents.%s", agentIdent(a))
		src := docSourcePath(a.Document)

		if strings.TrimSpace(a.Name) == "" {
			r.addError(src, base+".name", "agent is missing required `name`")
		}

		// description is required when the agent is model-invocable.
		if isModelInvocableAgent(a) && strings.TrimSpace(a.Description) == "" {
			r.addError(src, base+".description",
				"agent is model-invocable so `description` is required")
		}

		// SystemPrompt empty AND Description empty → warning.
		if strings.TrimSpace(a.SystemPrompt) == "" && strings.TrimSpace(a.Description) == "" {
			r.addWarning(src, base+".description",
				"agent has empty body AND empty description; nothing to project")
		}

		// Tools + DisallowedTools both set for a non-Claude target → warning.
		if len(a.Tools) > 0 && len(a.DisallowedTools) > 0 && hasNonClaudeTarget {
			r.addWarning(src, base+".disallowed_tools",
				"both `tools` and `disallowed_tools` set; non-Claude targets must compute the difference at emit (degraded)")
		}
	}
}

// --- Skills ---------------------------------------------------------------

func validateSkills(proj *model.Project, r *ValidationReport) {
	for _, s := range proj.Skills {
		base := fmt.Sprintf("skills.%s", skillIdent(s))
		src := docSourcePath(s.Document)

		if strings.TrimSpace(s.Name) == "" {
			r.addError(src, base+".name", "skill is missing required `name`")
		}

		// Mutation: empty Modes → [ModelDecision] (SPEC §5.4 #1).
		if len(s.Activation.Modes) == 0 {
			s.Activation.Modes = []model.SkillActivationMode{model.SkillActivationModelDecision}
		}

		// description required when Modes contains ModelDecision.
		if containsSkillMode(s.Activation.Modes, model.SkillActivationModelDecision) {
			if strings.TrimSpace(s.Description) == "" {
				r.addError(src, base+".description",
					"skill is model-decision-activated so `description` is required")
			}
		}

		// Globs required when Modes contains Glob.
		if containsSkillMode(s.Activation.Modes, model.SkillActivationGlob) {
			if len(s.Activation.Globs) == 0 {
				r.addError(src, base+".activation.globs",
					"skill activation includes `glob` so `activation.globs` is required")
			}
		}

		// ContentRegex must compile.
		if s.Activation.ContentRegex != "" {
			if _, err := regexp.Compile(s.Activation.ContentRegex); err != nil {
				r.addError(src, base+".activation.content_regex",
					fmt.Sprintf("`activation.content_regex` does not compile: %v", err))
			}
		}

		// UserInvocable AND ModelInvocable both explicitly false → error
		// (the skill becomes inaccessible).
		if s.Activation.UserInvocable != nil && !*s.Activation.UserInvocable &&
			s.Activation.ModelInvocable != nil && !*s.Activation.ModelInvocable {
			r.addError(src, base+".activation",
				"`activation.user_invocable: false` AND `activation.model_invocable: false` makes the skill inaccessible")
		}

		if s.ScopePath != "" {
			validateScopePath(r, src, base+".scope_path", s.ScopePath)
		}
	}
}

// --- Commands -------------------------------------------------------------

func validateCommands(proj *model.Project, r *ValidationReport) {
	for _, c := range proj.Commands {
		base := fmt.Sprintf("commands.%s", commandIdent(c))
		src := docSourcePath(c.Document)

		if strings.TrimSpace(c.Name) == "" {
			r.addError(src, base+".name", "command is missing required `name`")
		}

		if c.ScopePath != "" {
			validateScopePath(r, src, base+".scope_path", c.ScopePath)
		}
	}
}

// --- Hooks ----------------------------------------------------------------

func validateHooks(proj *model.Project, r *ValidationReport) {
	for i, h := range proj.Hooks {
		base := fmt.Sprintf("hooks[%d]", i)
		if h.Name != "" {
			base = fmt.Sprintf("hooks.%s", h.Name)
		}
		src := ""

		// Prefer the v2 typed EventCanonical; fall back to the v0.8 Event
		// string. Both are populated by the parser during the Phase 0
		// additive-rewrite window.
		ev := string(h.EventCanonical)
		if ev == "" {
			ev = h.Event
		}
		if ev == "" {
			r.addError(src, base+".event", "hook is missing required `event`")
		} else if !isValidHookEvent(ev) {
			r.addError(src, base+".event",
				fmt.Sprintf("hook event %q is not a canonical HookEvent and lacks the `native:` prefix", ev))
		}

		// Sequential nil is left as nil per SPEC §5.4 #2 — no action.

		if h.ScopePath != "" {
			validateScopePath(r, src, base+".scope_path", h.ScopePath)
		}
	}
}

// isValidHookEvent reports whether ev is one of the canonical HookEvent
// constants or starts with "native:". The list mirrors SPEC §4.4.2.
func isValidHookEvent(ev string) bool {
	if strings.HasPrefix(ev, "native:") {
		// allow any non-empty verbatim event under the native: namespace.
		return len(ev) > len("native:")
	}
	switch model.HookEvent(ev) {
	case model.EventSessionStart,
		model.EventSessionEnd,
		model.EventSessionResume,
		model.EventUserPromptSubmit,
		model.EventPreToolUse,
		model.EventPostToolUse,
		model.EventPostToolUseFailure,
		model.EventPermissionRequest,
		model.EventPreShell,
		model.EventPostShell,
		model.EventPreFileRead,
		model.EventPostFileEdit,
		model.EventPreMCPCall,
		model.EventPostMCPCall,
		model.EventSubagentStart,
		model.EventSubagentStop,
		model.EventStop,
		model.EventPreCompact,
		model.EventPostCompact,
		model.EventNotification,
		model.EventWorktreeCreate,
		model.EventWorktreeRemove,
		model.EventTaskCompleted,
		model.EventConfigChange,
		model.EventError:
		return true
	}
	return false
}

// --- MCP Servers ----------------------------------------------------------

func validateMCPServers(proj *model.Project, r *ValidationReport) {
	for _, m := range proj.MCP {
		base := fmt.Sprintf("mcp.%s", mcpIdent(m))
		src := ""

		// Mutation contract (SPEC §5.4 #3): infer Transport when unambiguous.
		if m.Transport == "" {
			hasCmd := strings.TrimSpace(m.Command) != ""
			hasURL := strings.TrimSpace(m.URL) != ""
			switch {
			case hasCmd && !hasURL:
				m.Transport = "stdio"
			case !hasCmd && hasURL:
				m.Transport = "http"
			default:
				r.addError(src, base+".transport",
					"MCP transport is empty and ambiguous (both command and url populated, or both empty); set transport explicitly")
				continue
			}
		}

		switch m.Transport {
		case "stdio":
			if strings.TrimSpace(m.Command) == "" {
				r.addError(src, base+".command",
					"MCP transport `stdio` requires `command`")
			}
		case "http", "sse":
			if strings.TrimSpace(m.URL) == "" {
				r.addError(src, base+".url",
					fmt.Sprintf("MCP transport %q requires `url`", m.Transport))
			}
		default:
			r.addError(src, base+".transport",
				fmt.Sprintf("MCP transport %q is not one of stdio|http|sse", m.Transport))
		}

		if m.ScopePath != "" {
			validateScopePath(r, src, base+".scope_path", m.ScopePath)
		}
	}
}

// --- Permissions ----------------------------------------------------------

func validatePermissions(proj *model.Project, r *ValidationReport) {
	if proj.Permissions != nil {
		validatePermissionsBlock(r, "permissions", proj.Permissions)
	}
	for i, p := range proj.ScopedPermissions {
		base := fmt.Sprintf("scoped_permissions[%d]", i)
		if p.ScopePath != "" {
			base = fmt.Sprintf("scoped_permissions.%s", p.ScopePath)
			validateScopePath(r, "", base+".scope_path", p.ScopePath)
		}
		validatePermissionsBlock(r, base, p)
	}
}

func validatePermissionsBlock(r *ValidationReport, base string, p *model.Permissions) {
	for i, rule := range p.Allow {
		validatePermRule(r, "", fmt.Sprintf("%s.allow[%d]", base, i), rule)
	}
	for i, rule := range p.Deny {
		validatePermRule(r, "", fmt.Sprintf("%s.deny[%d]", base, i), rule)
	}
	for i, rule := range p.Ask {
		validatePermRule(r, "", fmt.Sprintf("%s.ask[%d]", base, i), rule)
	}
}

// validatePermRule checks one permission rule string against SPEC §4.6.2
// grammar: `<target>:<pattern>` or `<target>:<subaction>:<pattern>` or
// bare tool name. Patterns may use `*`, `**`, or `!` negation; regex is
// rejected.
func validatePermRule(r *ValidationReport, file, field, rule string) {
	if strings.TrimSpace(rule) == "" {
		r.addError(file, field, "permission rule is empty")
		return
	}

	// Tolerate Claude's `WebFetch(domain:...)` shorthand.
	if strings.HasPrefix(rule, "WebFetch(") && strings.HasSuffix(rule, ")") {
		return
	}

	idx := strings.Index(rule, ":")
	if idx < 0 {
		// bare tool name (e.g. "Bash"); accept if it's a known target.
		if !recognizedPermTargets[strings.ToLower(rule)] {
			r.addError(file, field,
				fmt.Sprintf("permission rule %q is not a recognized bare tool name", rule))
		}
		return
	}

	target := strings.ToLower(rule[:idx])
	rest := rule[idx+1:]

	if !recognizedPermTargets[target] {
		r.addError(file, field,
			fmt.Sprintf("permission rule %q has unrecognized target %q", rule, target))
		return
	}

	// For mcp:<server>[:<tool>] the rest may contain a colon. That's fine.
	// For other targets, the pattern is the rest (which may have `!` prefix).
	pattern := rest
	if target == "mcp" {
		// Split off the server segment; whatever follows is the tool pattern.
		if jdx := strings.Index(rest, ":"); jdx >= 0 {
			pattern = rest[jdx+1:]
		} else {
			pattern = rest
		}
	}

	pattern = strings.TrimPrefix(pattern, "!")

	// Reject regex-ish patterns ($, ^, (), [], |, ?, \). The grammar allows
	// only `*` and `**` globs.
	if containsRegexMetachar(pattern) {
		r.addError(file, field,
			fmt.Sprintf("permission rule %q uses regex-like syntax; only `*` and `**` globs are allowed", rule))
		return
	}
}

// containsRegexMetachar reports whether s contains a character that would
// only be meaningful in a regex (and is illegal in the v0.9 perm grammar).
// `*` is intentionally NOT a metachar here.
func containsRegexMetachar(s string) bool {
	for _, ch := range s {
		switch ch {
		case '$', '^', '(', ')', '[', ']', '|', '?', '\\', '+', '{', '}':
			return true
		}
	}
	return false
}

// --- Scopes ---------------------------------------------------------------

func validateScopes(proj *model.Project, r *ValidationReport) {
	for _, s := range proj.Scopes {
		base := fmt.Sprintf("scopes.%s", scopeIdent(s))
		src := docSourcePath(s.Document)

		if s.Path != "" {
			validateScopePath(r, src, base+".path", s.Path)
		}

		switch s.Activation {
		case model.ScopeActivationModelDecision, model.ScopeActivationManual:
			if strings.TrimSpace(s.Description) == "" {
				r.addError(src, base+".description",
					fmt.Sprintf("scope with activation %q requires `description`", s.Activation))
			}
		case model.ScopeActivationGlob:
			if len(s.Globs) == 0 {
				r.addError(src, base+".globs",
					"scope activation `glob` requires `globs`")
			}
		}
	}
}

// validateScopePath enforces the SPEC §5.2 containment rules: no `..`
// segments, no absolute `/` prefix, no `~` home prefix.
func validateScopePath(r *ValidationReport, file, field, path string) {
	switch {
	case strings.HasPrefix(path, "/"):
		r.addError(file, field,
			fmt.Sprintf("scope path %q must be project-relative (no `/` prefix)", path))
		return
	case strings.HasPrefix(path, "~"):
		r.addError(file, field,
			fmt.Sprintf("scope path %q must not begin with `~`", path))
		return
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			r.addError(file, field,
				fmt.Sprintf("scope path %q escapes via `..` segment", path))
			return
		}
	}
}

// --- @include cycles ------------------------------------------------------

// validateIncludeCycles defensively detects cycles in Document.Includes.
// The parser already errors on cycle at parse time, but per SPEC §5.4 the
// validator surfaces the same condition with a structured ValidationError.
func validateIncludeCycles(proj *model.Project, r *ValidationReport) {
	// Build index of source-path → document so we can follow include edges.
	docs := map[string]*model.Document{}
	collectDoc := func(d *model.Document) {
		if d != nil && d.SourcePath != "" {
			docs[d.SourcePath] = d
		}
	}
	if proj.Context != nil {
		collectDoc(proj.Context)
	}
	for _, s := range proj.Scopes {
		collectDoc(s.Document)
	}
	for _, s := range proj.Skills {
		collectDoc(s.Document)
	}
	for _, c := range proj.Commands {
		collectDoc(c.Document)
	}
	for _, a := range proj.Agents {
		collectDoc(a.Document)
	}

	// Walk each document's include chain, looking for the doc's own path.
	for src, doc := range docs {
		visited := map[string]bool{}
		if hasCycleFrom(src, doc, docs, visited) {
			r.addError(src, "includes",
				fmt.Sprintf("@include cycle detected starting at %s", src))
		}
	}
}

func hasCycleFrom(start string, doc *model.Document, docs map[string]*model.Document, visited map[string]bool) bool {
	if doc == nil {
		return false
	}
	for _, inc := range doc.Includes {
		if inc == start {
			return true
		}
		if visited[inc] {
			continue
		}
		visited[inc] = true
		if sub, ok := docs[inc]; ok {
			if hasCycleFrom(start, sub, docs, visited) {
				return true
			}
		}
	}
	return false
}

// --- extensions + frontmatter --------------------------------------------

func validateExtensionsAndFrontmatter(proj *model.Project, r *ValidationReport) {
	// Walk every Document's frontmatter for unknown-plugin extensions and
	// unknown top-level keys.
	walk := func(d *model.Document, base string) {
		if d == nil || d.Frontmatter == nil {
			return
		}
		src := d.SourcePath
		for k, v := range d.Frontmatter {
			if k == "extensions" {
				if exts, ok := v.(map[string]any); ok {
					for pluginName := range exts {
						if !knownPlugins[pluginName] {
							r.addWarning(src,
								fmt.Sprintf("%s.extensions.%s", base, pluginName),
								fmt.Sprintf("extensions block references unregistered plugin %q", pluginName))
						}
					}
				}
				continue
			}
			if !canonicalFrontmatterKeys[k] {
				r.addWarning(src,
					fmt.Sprintf("%s.%s", base, k),
					fmt.Sprintf("frontmatter key %q is not a canonical schema field", k))
			}
		}
	}

	if proj.Context != nil {
		walk(proj.Context, "context")
	}
	for _, s := range proj.Scopes {
		walk(s.Document, fmt.Sprintf("scopes.%s", scopeIdent(s)))
	}
	for _, s := range proj.Skills {
		walk(s.Document, fmt.Sprintf("skills.%s", skillIdent(s)))
	}
	for _, c := range proj.Commands {
		walk(c.Document, fmt.Sprintf("commands.%s", commandIdent(c)))
	}
	for _, a := range proj.Agents {
		walk(a.Document, fmt.Sprintf("agents.%s", agentIdent(a)))
	}
}

// --- helpers --------------------------------------------------------------

func (r *ValidationReport) addError(file, field, msg string) {
	r.Errors = append(r.Errors, ValidationError{
		File:     file,
		Field:    field,
		Severity: "error",
		Message:  msg,
	})
}

func (r *ValidationReport) addWarning(file, field, msg string) {
	r.Warnings = append(r.Warnings, ValidationError{
		File:     file,
		Field:    field,
		Severity: "warning",
		Message:  msg,
	})
}

func docSourcePath(d *model.Document) string {
	if d == nil {
		return ""
	}
	return d.SourcePath
}

func agentIdent(a *model.Agent) string {
	if a == nil || a.Name == "" {
		return "<unnamed>"
	}
	return a.Name
}

func skillIdent(s *model.Skill) string {
	if s == nil || s.Name == "" {
		return "<unnamed>"
	}
	return s.Name
}

func commandIdent(c *model.Command) string {
	if c == nil || c.Name == "" {
		return "<unnamed>"
	}
	return c.Name
}

func scopeIdent(s *model.Scope) string {
	if s == nil {
		return "<nil>"
	}
	if s.Name != "" {
		return s.Name
	}
	if s.Path != "" {
		return s.Path
	}
	return "<root>"
}

func mcpIdent(m *model.MCPServer) string {
	if m == nil || m.Name == "" {
		return "<unnamed>"
	}
	return m.Name
}

func containsSkillMode(modes []model.SkillActivationMode, want model.SkillActivationMode) bool {
	for _, m := range modes {
		if m == want {
			return true
		}
	}
	return false
}

// isModelInvocableAgent reports whether an Agent is reachable via the host
// LLM's auto-delegation (i.e. needs a description). Default true unless the
// agent explicitly opts out via ModelInvocable == false.
func isModelInvocableAgent(a *model.Agent) bool {
	if a == nil {
		return false
	}
	if a.ModelInvocable != nil {
		return *a.ModelInvocable
	}
	return true
}

// projectHasNonClaudeTarget reports whether the project Config opts into a
// target other than "claude". Used to warn on Tools+DisallowedTools combos
// that only Claude can express natively.
func projectHasNonClaudeTarget(proj *model.Project) bool {
	if proj.Config == nil {
		return false
	}
	if len(proj.Config.Targets) == 0 {
		return false
	}
	hasOther := false
	for _, t := range proj.Config.Targets {
		if t != "claude" {
			hasOther = true
			break
		}
	}
	return hasOther
}
