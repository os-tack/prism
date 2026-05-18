// claude.go: imports Claude Code's on-disk layout into the canonical
// *model.Project shape. This is the inverse of plugins/claude.go.
//
// What we read:
//   CLAUDE.md                             → root context document
//   <subdir>/CLAUDE.md                    → scope at <subdir>/
//   .claude/skills/<name>/SKILL.md        → skill (with optional scripts/)
//   .claude/commands/<name>.md            → command
//   .claude/agents/<name>.md              → agent (subagent prompt)
//   .claude/settings.json                 → permissions + hooks
//   .mcp.json                             → MCP servers
//
// v0.9 / schema v2 transition: this importer now mirrors the Phase 2a
// emit-side population so that Plan(Import(P)) ≡ Plan(P). Every v2 field
// the plugin writes is read back ADDITIVELY here — the v0.8 fields
// (Event, Matcher, Description, Trigger, etc.) remain populated for
// back-compat. Frontmatter keys outside the canonical schema are stored
// under Extensions["claude"] so the projection can round-trip them.
//
// The existing --from claude walker at engine/init.go:62-94 is NOT
// removed by this file; the engine integration pass swaps it for a call
// into this importer.

package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
)

// ClaudeImporter reads CLAUDE.md (cascade), .claude/, and .mcp.json.
type ClaudeImporter struct{}

// NewClaude constructs a ClaudeImporter.
func NewClaude() *ClaudeImporter { return &ClaudeImporter{} }

// Name returns "claude".
func (i *ClaudeImporter) Name() string { return "claude" }

// Detect returns true when .claude/ or CLAUDE.md is present at root.
func (i *ClaudeImporter) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".claude")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Import reads root and produces the canonical Project.
func (i *ClaudeImporter) Import(root string) (*model.Project, []Warning, error) {
	if !i.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	// 1. Root + nested CLAUDE.md.
	rootDoc, scopes, err := importNestedMarkdown(root, "CLAUDE.md", "claude")
	if err != nil {
		return nil, nil, err
	}
	proj.Context = rootDoc
	proj.Scopes = scopes

	// 2. Skills: .claude/skills/<name>/SKILL.md plus optional scripts/.
	skills, skillWarnings, err := importClaudeSkills(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Skills = skills
	warnings = append(warnings, skillWarnings...)

	// 3. Commands: .claude/commands/<name>.md.
	commands, err := importClaudeCommands(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Commands = commands

	// 4. Agents: .claude/agents/<name>.md.
	agents, err := importClaudeAgents(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Agents = agents

	// 5. Permissions + hooks: .claude/settings.json.
	perms, hooks, settingsWarnings, err := importClaudeSettings(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Permissions = perms
	proj.Hooks = hooks
	warnings = append(warnings, settingsWarnings...)

	// 6. MCP servers: .mcp.json.
	mcps, err := importClaudeMCP(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = mcps

	return proj, warnings, nil
}

// claudeSkillCanonicalKeys are frontmatter keys that map to canonical
// Skill model fields. Anything outside this set passes through under
// Extensions["claude"].
var claudeSkillCanonicalKeys = map[string]struct{}{
	"description":   {},
	"trigger":       {},
	"globs":         {},
	"paths":         {},
	"allowed-tools": {},
	"arguments":     {},
	"when_to_use":   {},
	"when-to-use":   {},
	"model":         {},
	"context":       {},
	"agent":         {},
}

// claudeAgentCanonicalKeys: frontmatter keys with canonical mappings on
// model.Agent. Unknown keys land under Extensions["claude"].
var claudeAgentCanonicalKeys = map[string]struct{}{
	"description":      {},
	"tools":            {},
	"allowed-tools":    {},
	"disallowed-tools": {},
	"model":            {},
	"permissionMode":   {},
	"permission-mode":  {},
	"background":       {},
	"max-turns":        {},
	"maxTurns":         {},
	"user-invocable":   {},
	"user_invocable":   {},
	"model-invocable":  {},
	"model_invocable":  {},
	"initial-prompt":   {},
	"initialPrompt":    {},
}

// claudeCommandCanonicalKeys: keys with canonical mappings on Command.
var claudeCommandCanonicalKeys = map[string]struct{}{
	"description":              {},
	"argument-hint":            {},
	"argumentHint":             {},
	"arguments":                {},
	"model":                    {},
	"allowed-tools":            {},
	"agent":                    {},
	"disable-model-invocation": {},
}

// importClaudeSkills walks .claude/skills/<name>/SKILL.md and turns each
// into a *model.Skill. Scripts under .claude/skills/<name>/scripts/ are
// recorded as absolute paths on Skill.Scripts.
func importClaudeSkills(root string) ([]*model.Skill, []Warning, error) {
	skillsDir := filepath.Join(root, ".claude", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("claude: read %s: %w", skillsDir, err)
	}

	var skills []*model.Skill
	var warnings []Warning
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(skillsDir, e.Name())
		skillMD := filepath.Join(skillDir, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				warnings = append(warnings, Warning{
					SourcePath: filepath.ToSlash(filepath.Join(".claude", "skills", e.Name())),
					Heuristic:  "skill directory has no SKILL.md; skipping",
					Severity:   "warn",
				})
				continue
			}
			return nil, nil, fmt.Errorf("claude: read %s: %w", skillMD, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("claude: %s: %w", skillMD, err)
		}

		s := &model.Skill{
			Name: e.Name(),
			Document: &model.Document{
				SourcePath:  skillMD,
				Frontmatter: fm,
				Body:        provenanceComment("claude", skillMD) + body,
			},
		}
		if fm != nil {
			if v, ok := fm["description"].(string); ok {
				s.Description = v
			}
			if v, ok := fm["trigger"].(string); ok {
				s.Trigger = v
			}
			s.Globs = stringSliceAny(fm["globs"])

			// v2 additive populations (SPEC §4.2.2).
			paths := stringSliceAny(fm["paths"])
			s.Activation.Globs = paths
			if len(paths) > 0 {
				s.Activation.Modes = []model.SkillActivationMode{model.SkillActivationGlob}
			} else {
				s.Activation.Modes = []model.SkillActivationMode{model.SkillActivationModelDecision}
			}
			s.AllowedTools = stringSliceAny(fm["allowed-tools"])
			s.Arguments = parseClaudeSkillArguments(fm["arguments"])
			if v, ok := fm["when_to_use"].(string); ok {
				s.WhenToUse = v
			} else if v, ok := fm["when-to-use"].(string); ok {
				s.WhenToUse = v
			}
			if v, ok := fm["model"].(string); ok {
				s.Model = v
			}
			// Subagent: context: fork + agent: <name>.
			if ctx, ok := fm["context"].(string); ok && ctx == "fork" {
				if name, ok := fm["agent"].(string); ok {
					s.Subagent = name
				}
			}
			if exts := extractExtensions(fm, claudeSkillCanonicalKeys); exts != nil {
				s.Extensions = map[string]any{"claude": exts}
			}
		}

		scripts, err := collectClaudeScripts(filepath.Join(skillDir, "scripts"))
		if err != nil {
			return nil, nil, err
		}
		s.Scripts = scripts

		skills = append(skills, s)
	}
	sort.Slice(skills, func(a, b int) bool { return skills[a].Name < skills[b].Name })
	return skills, warnings, nil
}

// collectClaudeScripts returns sorted absolute paths to every regular
// file under scriptsDir. Mirrors parser.collectScripts so the imported
// shape matches what Parse would produce.
func collectClaudeScripts(scriptsDir string) ([]string, error) {
	info, err := os.Stat(scriptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: stat %s: %w", scriptsDir, err)
	}
	if !info.IsDir() {
		return nil, nil
	}
	var scripts []string
	err = filepath.WalkDir(scriptsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeType != 0 {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		scripts = append(scripts, abs)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claude: walk %s: %w", scriptsDir, err)
	}
	sort.Strings(scripts)
	return scripts, nil
}

// importClaudeCommands walks .claude/commands/<name>.md.
func importClaudeCommands(root string) ([]*model.Command, error) {
	dir := filepath.Join(root, ".claude", "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: read %s: %w", dir, err)
	}
	var cmds []*model.Command
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("claude: read %s: %w", path, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, fmt.Errorf("claude: %s: %w", path, err)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		c := &model.Command{
			Name: name,
			Document: &model.Document{
				SourcePath:  path,
				Frontmatter: fm,
				Body:        provenanceComment("claude", path) + body,
			},
			// AutoInvoke default true; flipped only when frontmatter sets
			// disable-model-invocation: true (Claude convention).
			AutoInvoke: true,
		}
		if fm != nil {
			if v, ok := fm["description"].(string); ok {
				c.Description = v
			}
			// v2 additive populations (SPEC §4.3.2).
			if v, ok := fm["argument-hint"].(string); ok {
				c.ArgumentHint = v
			} else if v, ok := fm["argumentHint"].(string); ok {
				c.ArgumentHint = v
			}
			c.Arguments = stringSliceAny(fm["arguments"])
			if v, ok := fm["model"].(string); ok {
				c.Model = v
			}
			c.Tools = stringSliceAny(fm["allowed-tools"])
			if v, ok := fm["agent"].(string); ok {
				c.Agent = v
			}
			if v, ok := fm["disable-model-invocation"].(bool); ok && v {
				c.AutoInvoke = false
			}
			if exts := extractExtensions(fm, claudeCommandCanonicalKeys); exts != nil {
				c.Extensions = map[string]any{"claude": exts}
			}
		}
		cmds = append(cmds, c)
	}
	sort.Slice(cmds, func(a, b int) bool { return cmds[a].Name < cmds[b].Name })
	return cmds, nil
}

// claudeAgentInToolsRE captures `Agent(<name>)` entries inside a tools
// list so we can lift the subagent allowlist onto Agent.AllowedSubagents.
var claudeAgentInToolsRE = regexp.MustCompile(`^Agent\(([^)]+)\)$`)

// importClaudeAgents walks .claude/agents/<name>.md.
func importClaudeAgents(root string) ([]*model.Agent, error) {
	dir := filepath.Join(root, ".claude", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: read %s: %w", dir, err)
	}
	var ags []*model.Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("claude: read %s: %w", path, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, fmt.Errorf("claude: %s: %w", path, err)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		a := &model.Agent{
			Name: name,
			Document: &model.Document{
				SourcePath:  path,
				Frontmatter: fm,
				Body:        provenanceComment("claude", path) + body,
			},
			// SystemPrompt mirrors the projected body (sans frontmatter)
			// so v2 readers don't have to walk Document.Body.
			SystemPrompt: body,
		}
		if fm != nil {
			if v, ok := fm["description"].(string); ok {
				a.Description = v
			}
			// v2 additive populations (SPEC §4.1.2).
			tools := stringSliceAny(fm["tools"])
			if tools == nil {
				tools = stringSliceAny(fm["allowed-tools"])
			}
			// Partition the tools list: bare tool names stay on Tools,
			// `Agent(<name>)` entries lift to AllowedSubagents.
			if len(tools) > 0 {
				bare := make([]string, 0, len(tools))
				var subs []string
				for _, t := range tools {
					if m := claudeAgentInToolsRE.FindStringSubmatch(t); len(m) == 2 {
						subs = append(subs, strings.TrimSpace(m[1]))
						continue
					}
					bare = append(bare, t)
				}
				a.Tools = bare
				a.AllowedSubagents = subs
			}
			a.DisallowedTools = stringSliceAny(fm["disallowed-tools"])
			if v, ok := fm["model"].(string); ok {
				a.Model = v
			}
			// permissionMode: plan → ReadOnly true.
			permMode, _ := fm["permissionMode"].(string)
			if permMode == "" {
				permMode, _ = fm["permission-mode"].(string)
			}
			if permMode == "plan" {
				t := true
				a.ReadOnly = &t
			}
			if v, ok := fm["background"].(bool); ok {
				bv := v
				a.Background = &bv
			}
			if v, ok := intFromFM(fm, "max-turns"); ok {
				a.MaxTurns = &v
			} else if v, ok := intFromFM(fm, "maxTurns"); ok {
				a.MaxTurns = &v
			}
			if v, ok := boolFromFM(fm, "user_invocable"); ok {
				bv := v
				a.UserInvocable = &bv
			} else if v, ok := boolFromFM(fm, "user-invocable"); ok {
				bv := v
				a.UserInvocable = &bv
			}
			if v, ok := boolFromFM(fm, "model_invocable"); ok {
				bv := v
				a.ModelInvocable = &bv
			} else if v, ok := boolFromFM(fm, "model-invocable"); ok {
				bv := v
				a.ModelInvocable = &bv
			}
			if v, ok := fm["initial-prompt"].(string); ok {
				a.InitialPrompt = v
			} else if v, ok := fm["initialPrompt"].(string); ok {
				a.InitialPrompt = v
			}
			if exts := extractExtensions(fm, claudeAgentCanonicalKeys); exts != nil {
				a.Extensions = map[string]any{"claude": exts}
			}
		}
		ags = append(ags, a)
	}
	sort.Slice(ags, func(a, b int) bool { return ags[a].Name < ags[b].Name })
	return ags, nil
}

// claudeSettingsHookEntry is the shape inside settings.json["hooks"][event][n].hooks[m].
// Claude Code accepts an optional `timeout` field (seconds) per entry; we
// promote it to TimeoutMs on the v2 HookHandler.
type claudeSettingsHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// claudeSettingsHookGroup mirrors settings.json["hooks"][event][n].
type claudeSettingsHookGroup struct {
	Matcher string                    `json:"matcher"`
	Hooks   []claudeSettingsHookEntry `json:"hooks"`
}

// importClaudeSettings reads .claude/settings.json and pulls out
// permissions + hooks into the canonical model. Returns nil values for
// missing pieces (the engine treats nil-permissions as "no permissions
// configured").
func importClaudeSettings(root string) (*model.Permissions, []*model.Hook, []Warning, error) {
	settingsPath := filepath.Join(root, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("claude: read %s: %w", settingsPath, err)
	}
	if len(data) == 0 {
		return nil, nil, nil, nil
	}

	// We parse loosely: settings.json may contain user-managed keys we
	// don't care about; only `permissions` and `hooks` are relevant.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, nil, fmt.Errorf("claude: parse %s: %w", settingsPath, err)
	}

	var perms *model.Permissions
	if pRaw, ok := raw["permissions"]; ok {
		var p map[string]any
		if err := json.Unmarshal(pRaw, &p); err != nil {
			return nil, nil, nil, fmt.Errorf("claude: parse permissions in %s: %w", settingsPath, err)
		}
		allow := stringSliceAny(p["allow"])
		deny := stringSliceAny(p["deny"])
		ask := stringSliceAny(p["ask"])
		if len(allow) > 0 || len(deny) > 0 || len(ask) > 0 {
			perms = &model.Permissions{
				Allow: allow,
				Deny:  deny,
				Ask:   ask,
			}
			// v2 additive: capture any sibling keys on `permissions:` as
			// Extensions["claude"].
			permCanonical := map[string]struct{}{
				"allow": {}, "deny": {}, "ask": {},
			}
			if exts := extractExtensions(p, permCanonical); exts != nil {
				perms.Extensions = map[string]any{"claude": exts}
			}
		}
	}

	var hooks []*model.Hook
	var warnings []Warning
	if hRaw, ok := raw["hooks"]; ok {
		// hooks is a map of event → []hookGroup.
		var byEvent map[string][]claudeSettingsHookGroup
		if err := json.Unmarshal(hRaw, &byEvent); err != nil {
			return nil, nil, nil, fmt.Errorf("claude: parse hooks in %s: %w", settingsPath, err)
		}
		// Sort events for deterministic output.
		events := make([]string, 0, len(byEvent))
		for ev := range byEvent {
			events = append(events, ev)
		}
		sort.Strings(events)
		for _, ev := range events {
			for _, grp := range byEvent[ev] {
				for _, entry := range grp.Hooks {
					if entry.Type != "" && entry.Type != "command" {
						warnings = append(warnings, Warning{
							SourcePath: ".claude/settings.json",
							Heuristic:  fmt.Sprintf("hook entry type %q is not 'command'; canonical model only supports command hooks — dropping", entry.Type),
							Severity:   "warn",
						})
						continue
					}
					h := &model.Hook{
						Event:      ev,
						Matcher:    grp.Matcher,
						ScriptPath: entry.Command,
					}
					// v2 additive populations (SPEC §4.4.2).
					h.EventCanonical = claudeEventCanonical(ev)
					h.MatcherV2 = claudeMatcherV2(grp.Matcher)
					handler := model.HookHandler{
						Kind:    model.HookHandlerCommand,
						Command: entry.Command,
					}
					if entry.Timeout > 0 {
						handler.TimeoutMs = entry.Timeout * 1000
					}
					h.Handlers = []model.HookHandler{handler}
					hooks = append(hooks, h)
				}
			}
		}
	}

	return perms, hooks, warnings, nil
}

// claudeMCPRawServer is the shape Claude Code accepts under .mcp.json's
// mcpServers map; we parse loosely so unknown keys can pass through
// under Extensions["claude"].
type claudeMCPRawServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// importClaudeMCP reads .mcp.json and returns the canonical MCP server
// slice. Returns (nil, nil) when the file is absent or empty.
func importClaudeMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Two-pass parse: first the typed shape, then a generic map so we
	// can sweep unknown keys into Extensions["claude"].
	var raw struct {
		MCPServers map[string]claudeMCPRawServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("claude: parse %s: %w", path, err)
	}
	var rawGeneric struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal(data, &rawGeneric) // best-effort; typed pass validated structure
	names := make([]string, 0, len(raw.MCPServers))
	for n := range raw.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		s := raw.MCPServers[n]
		ms := &model.MCPServer{
			Name:    n,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
			// v2 additive (SPEC §4.5.2).
			Transport: s.Type,
			Headers:   s.Headers,
		}
		// Bearer token detection: an Authorization: Bearer <token> header
		// surfaces as MCPAuth so a v2 reader doesn't have to dig through
		// Headers. We leave the original entry in Headers — both shapes
		// observe the same data.
		if tok := bearerFromHeaders(s.Headers); tok != "" {
			ms.Auth = &model.MCPAuth{
				Scheme: "bearer",
				Token:  tok,
			}
		}
		// Extensions["claude"] — any keys outside the canonical set.
		mcpCanonical := map[string]struct{}{
			"type": {}, "command": {}, "args": {}, "env": {}, "url": {}, "headers": {},
		}
		if rawGeneric.MCPServers != nil {
			if exts := extractExtensions(rawGeneric.MCPServers[n], mcpCanonical); exts != nil {
				ms.Extensions = map[string]any{"claude": exts}
			}
		}
		out = append(out, ms)
	}
	return out, nil
}

// claudeEventCanonical normalizes Claude's PascalCase event names
// (PreToolUse, PostToolUse, etc.) into the canonical snake_case enum
// values defined in model.HookEvent. Unknown events round-trip
// verbatim under the typed alias.
func claudeEventCanonical(claudeEvent string) model.HookEvent {
	switch claudeEvent {
	case "SessionStart":
		return model.EventSessionStart
	case "SessionEnd":
		return model.EventSessionEnd
	case "SessionResume":
		return model.EventSessionResume
	case "UserPromptSubmit":
		return model.EventUserPromptSubmit
	case "PreToolUse":
		return model.EventPreToolUse
	case "PostToolUse":
		return model.EventPostToolUse
	case "Notification":
		return model.EventNotification
	case "Stop":
		return model.EventStop
	case "PreCompact":
		return model.EventPreCompact
	case "SubagentStop":
		return model.EventSubagentStop
	}
	return model.HookEvent(claudeEvent)
}

// claudeMatcherV2 derives a v2 HookMatcher from Claude's free-form
// matcher string. Empty → Kind "all" (matches everything per SPEC
// §4.4.3); contains "|" → split into exact-patterns; otherwise a single
// exact pattern. Regex matchers (those wrapped in `/.../`) round-trip as
// Kind "regex" with the bare body in Patterns[0].
func claudeMatcherV2(matcher string) model.HookMatcher {
	if matcher == "" {
		return model.HookMatcher{Kind: "all"}
	}
	if strings.HasPrefix(matcher, "/") && strings.HasSuffix(matcher, "/") && len(matcher) > 1 {
		return model.HookMatcher{
			Kind:     "regex",
			Patterns: []string{strings.TrimPrefix(strings.TrimSuffix(matcher, "/"), "/")},
		}
	}
	parts := strings.Split(matcher, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return model.HookMatcher{Kind: "exact", Patterns: parts}
}

// bearerFromHeaders detects an `Authorization: Bearer <token>` header
// and returns the token (or "" when absent). Case-insensitive on the
// header name to match HTTP norms.
func bearerFromHeaders(headers map[string]string) string {
	for k, v := range headers {
		if !strings.EqualFold(k, "Authorization") {
			continue
		}
		if strings.HasPrefix(v, "Bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
		}
	}
	return ""
}

// parseClaudeSkillArguments accepts either of the two SPEC §4.2.2
// argument shapes:
//
//	arguments: [name1, name2]                       # bare list of names
//	arguments:
//	  - name: foo
//	    description: ...
//	    required: true
//
// and returns []SkillArgument. Unknown shapes return nil (lossy but
// safe; the original frontmatter remains under Extensions["claude"]).
func parseClaudeSkillArguments(v any) []model.SkillArgument {
	if v == nil {
		return nil
	}
	switch typed := v.(type) {
	case []any:
		out := make([]model.SkillArgument, 0, len(typed))
		for _, item := range typed {
			switch it := item.(type) {
			case string:
				out = append(out, model.SkillArgument{Name: it})
			case map[string]any:
				arg := model.SkillArgument{}
				if s, ok := it["name"].(string); ok {
					arg.Name = s
				}
				if s, ok := it["description"].(string); ok {
					arg.Description = s
				}
				if b, ok := it["required"].(bool); ok {
					arg.Required = b
				}
				if arg.Name != "" {
					out = append(out, arg)
				}
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// extractExtensions returns a fresh map of every fm key that is NOT in
// canonical. Nil when nothing remains. Used by Skill/Agent/Command/
// MCPServer/Permissions to round-trip unknown frontmatter under
// Extensions["claude"].
func extractExtensions(fm map[string]any, canonical map[string]struct{}) map[string]any {
	if len(fm) == 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range fm {
		if _, isCanonical := canonical[k]; isCanonical {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// intFromFM accepts int, int64, float64 (YAML may decode any numeric
// scalar as float64) and returns (value, true) when present. Returns
// (0, false) when the key is missing or not numeric.
func intFromFM(fm map[string]any, key string) (int, bool) {
	v, ok := fm[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// boolFromFM accepts bool and the strings "true"/"false". Returns
// (value, true) on a recognized scalar, else (false, false).
func boolFromFM(fm map[string]any, key string) (bool, bool) {
	v, ok := fm[key]
	if !ok {
		return false, false
	}
	switch n := v.(type) {
	case bool:
		return n, true
	case string:
		switch strings.ToLower(n) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

// Compile-time check that *ClaudeImporter implements Importer.
var _ Importer = (*ClaudeImporter)(nil)
