// gemini.go: imports GEMINI.md (root + nested cascade) and
// .gemini/settings.json (mcpServers map) into the canonical
// *model.Project shape.
//
// The cascade is structurally identical to CLAUDE.md — every directory
// can hold its own GEMINI.md and the tree of context files matches the
// tree of scopes.
//
// v0.8.0 additions (mirrors plugins/gemini.go emissions):
//
//   .gemini/agents/<n>.md      → model.Agent (frontmatter + body)
//   .gemini/commands/<n>.toml  → model.Command (TOML; extract `prompt` field)
//   .gemini/settings.json      → also reads `hooks` block alongside mcpServers
//
// v0.9.0 Phase 2a additions: populates v2 fields alongside v0.8 so v2
// readers and v0.8 readers see the same data through different access
// paths. Agents now carry SystemPrompt, Tools, Model, Temperature,
// MaxTurns, and Extensions["gemini"]. Commands carry Tools, Model,
// Arguments (["args"] when the body contains {{args}}), and
// Extensions["gemini"]. Hooks carry EventCanonical, MatcherV2, Handlers
// (Kind=command with Command + TimeoutMs from seconds), and Sequential;
// per-action Gemini events (BeforeTool + matcher: run_shell_command, …)
// are reversed into the canonical PreShell / PostShell / PreFileRead /
// PostFileEdit / PreMCPCall / PostMCPCall enums. MCP servers carry
// Transport (inferred from httpUrl/url/command), Headers, Cwd, Trust,
// IncludeTools, ExcludeTools, TimeoutMs, AutoApprove, and
// Extensions["gemini"].

package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
)

// GeminiImporter reads GEMINI.md (cascade) and .gemini/settings.json.
type GeminiImporter struct{}

// NewGemini constructs a GeminiImporter.
func NewGemini() *GeminiImporter { return &GeminiImporter{} }

// Name returns "gemini".
func (i *GeminiImporter) Name() string { return "gemini" }

// Detect returns true when .gemini/ or GEMINI.md is present at root.
func (i *GeminiImporter) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".gemini")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "GEMINI.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Import reads root and produces the canonical Project.
func (i *GeminiImporter) Import(root string) (*model.Project, []Warning, error) {
	if !i.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}

	rootDoc, scopes, err := importNestedMarkdown(root, "GEMINI.md", "gemini")
	if err != nil {
		return nil, nil, err
	}
	proj.Context = rootDoc
	proj.Scopes = scopes

	settingsPath := filepath.Join(root, ".gemini", "settings.json")
	mcps, err := readGeminiSettingsMCP(settingsPath)
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = mcps

	hooks, err := readGeminiSettingsHooks(settingsPath)
	if err != nil {
		return nil, nil, err
	}
	proj.Hooks = append(proj.Hooks, hooks...)

	agents, err := readGeminiAgents(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Agents = append(proj.Agents, agents...)

	cmds, err := readGeminiCommands(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Commands = append(proj.Commands, cmds...)

	return proj, nil, nil
}

// geminiAgentKnownKeys are the frontmatter keys the importer interprets
// natively. Any other key is captured under Extensions["gemini"] so the
// canonical model preserves passthrough Gemini-specific fields.
var geminiAgentKnownKeys = map[string]struct{}{
	"name":        {},
	"description": {},
	"tools":       {},
	"model":       {},
	"temperature": {},
	"max_turns":   {},
}

// readGeminiAgents reads .gemini/agents/<n>.md into []*model.Agent.
// Populates v0.8 (Name, Description, Document) plus v2 fields
// (SystemPrompt, Tools, Model, Temperature, MaxTurns, Extensions["gemini"]).
func readGeminiAgents(root string) ([]*model.Agent, error) {
	dir := filepath.Join(root, ".gemini", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("gemini: read %s: %w", full, rerr)
		}
		fm, body, perr := splitFrontmatter(data)
		if perr != nil {
			return nil, fmt.Errorf("gemini: %s: %w", full, perr)
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if n, ok := fm["name"].(string); ok && n != "" {
			name = n
		}
		desc, _ := fm["description"].(string)
		ag := &model.Agent{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment("gemini", full) + body,
			},
		}
		// v2 additive populations (SPEC §4.1.2). SystemPrompt mirrors the
		// document body so v2 readers can hit Agent.SystemPrompt directly
		// without descending into Document.Body. Tools / Model /
		// Temperature / MaxTurns come from the frontmatter when present.
		ag.SystemPrompt = body
		if tools := stringSliceAny(fm["tools"]); len(tools) > 0 {
			ag.Tools = tools
		}
		if m, ok := fm["model"].(string); ok && m != "" {
			ag.Model = m
		}
		if t, ok := readFloat(fm["temperature"]); ok {
			tt := t
			ag.Temperature = &tt
		}
		if mt, ok := readInt(fm["max_turns"]); ok {
			mm := mt
			ag.MaxTurns = &mm
		}
		if exts := geminiExtensionsFromFM(fm, geminiAgentKnownKeys); exts != nil {
			ag.Extensions = map[string]any{"gemini": exts}
		}
		out = append(out, ag)
	}
	return out, nil
}

// readGeminiCommands reads .gemini/commands/<n>.toml into []*model.Command.
// The TOML schema (per plugins/gemini.go:renderGeminiCommand) is minimal:
// `description = "..."` plus a triple-quoted multi-line `prompt`. The prompt
// body becomes the canonical Document.Body (markdown), and triple-quote
// escaping is reversed.
//
// Phase 2a (v0.9.0): also captures any extra top-level TOML keys (model,
// tools, …) into Command.Extensions["gemini"] for round-trip safety, and
// populates v2 fields (Tools, Model, Arguments). Gemini's commands use
// `{{args}}` for the positional placeholder; when the prompt body
// references it we populate Command.Arguments with `["args"]` as a
// single-positional placeholder.
func readGeminiCommands(root string) ([]*model.Command, error) {
	dir := filepath.Join(root, ".gemini", "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Command
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("gemini: read %s: %w", full, rerr)
		}
		desc, body, extras := parseGeminiCommandTOML(string(data))
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		c := &model.Command{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath: full,
				Body:       provenanceComment("gemini", full) + body,
			},
		}
		// v2 additive populations (SPEC §4.3.2). When a `model` or
		// `tools` key was passed through, lift it to the canonical typed
		// slots so v2 readers see it directly; the leftover passthrough
		// goes under Extensions["gemini"].
		if extras != nil {
			if m, ok := extras["model"].(string); ok && m != "" {
				c.Model = m
			}
			if tools := stringSliceAny(extras["tools"]); len(tools) > 0 {
				c.Tools = tools
			}
			passthrough := map[string]any{}
			for k, v := range extras {
				switch k {
				case "description", "prompt", "model", "tools":
					continue
				}
				passthrough[k] = v
			}
			if len(passthrough) > 0 {
				c.Extensions = map[string]any{"gemini": passthrough}
			}
		}
		// Gemini's slash-command bodies use `{{args}}` as the positional
		// placeholder. When present we record a single-name arg list so
		// v2 readers see Arguments alongside the body's substitution
		// marker.
		if strings.Contains(body, "{{args}}") {
			c.Arguments = []string{"args"}
		}
		out = append(out, c)
	}
	return out, nil
}

// parseGeminiCommandTOML extracts the description and prompt body from a
// gemini command file, plus any other top-level key/value pairs it
// encounters.
//
// It is intentionally minimal — the only writer is
// plugins/gemini.go:renderGeminiCommand, whose output it inverts.
// Additional top-level keys (model = "...", tools = [...], …) are decoded
// via json.Unmarshal of the right-hand side; JSON is a valid TOML inline
// subset for scalars/arrays/objects, which matches the way the emit side
// serializes Extensions["gemini"] entries.
func parseGeminiCommandTOML(s string) (description, prompt string, extras map[string]any) {
	lines := strings.Split(s, "\n")
	const tq = "\"\"\""
	extras = map[string]any{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "description") && strings.Contains(trimmed, "=") {
			eq := strings.Index(trimmed, "=")
			rhs := strings.TrimSpace(trimmed[eq+1:])
			if len(rhs) >= 2 && rhs[0] == '"' && rhs[len(rhs)-1] == '"' {
				var decoded string
				if err := json.Unmarshal([]byte(rhs), &decoded); err == nil {
					description = decoded
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "prompt") && strings.Contains(line, tq) {
			var bodyLines []string
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimRight(lines[j], "\r") == tq {
					break
				}
				bodyLines = append(bodyLines, lines[j])
			}
			prompt = strings.Join(bodyLines, "\n")
			prompt = strings.ReplaceAll(prompt, "\"\"\\\"", tq)
			if prompt != "" && !strings.HasSuffix(prompt, "\n") {
				prompt += "\n"
			}
			if len(extras) == 0 {
				extras = nil
			}
			return description, prompt, extras
		}
		// Generic single-line `key = <json>` capture. Skips blanks,
		// comments, and section headers.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		if key == "" || key == "description" || key == "prompt" {
			continue
		}
		rhs := strings.TrimSpace(trimmed[eq+1:])
		if rhs == "" {
			continue
		}
		var val any
		if err := json.Unmarshal([]byte(rhs), &val); err == nil {
			extras[key] = val
		}
	}
	if len(extras) == 0 {
		extras = nil
	}
	return description, prompt, extras
}

// geminiHookEntryRaw is the parsed shape of one entry inside
// settings.json's `hooks` block: matcher + sequential at the group level,
// then a list of {type, command, name, timeout} handlers. Captured
// separately so we can populate the v2 Handlers slice fully.
type geminiHookEntryRaw struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Name    string `json:"name"`
	Timeout int    `json:"timeout"`
}

type geminiHookGroupRaw struct {
	Matcher    string               `json:"matcher"`
	Sequential *bool                `json:"sequential"`
	Hooks      []geminiHookEntryRaw `json:"hooks"`
}

// readGeminiSettingsHooks parses .gemini/settings.json's `hooks` block.
// Same shape as Claude's settings.json: a map of event → []group, each
// group with `matcher` and `hooks: [{type, command, name?, timeout?}]`.
// Commands pointing into __scope-guard__ wrappers are skipped.
//
// Phase 2a (v0.9.0): populates v2 Hook fields:
//   - EventCanonical: reverses Gemini's BeforeTool/AfterTool + matcher
//     spellings (run_shell_command, read_file, write_file, mcp_*) into
//     the canonical per-action enum (PreShell, PostShell, PreFileRead,
//     PostFileEdit, PreMCPCall, PostMCPCall). Plain BeforeTool/AfterTool
//     without a recognized matcher map to PreToolUse / PostToolUse.
//   - MatcherV2: when the group carries a matcher AND we did NOT consume
//     it into a per-action canonical event, record it as Kind: "exact".
//   - Handlers: one HookHandler per entry, Kind = command, with
//     TimeoutMs converted from seconds (Gemini's wire timeout is in
//     seconds, the canonical model uses milliseconds).
//   - Sequential: copied through when present in the source (nil
//     otherwise, which preserves "host default" semantics).
func readGeminiSettingsHooks(path string) ([]*model.Hook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		return nil, fmt.Errorf("gemini: parse %s: %w", path, jerr)
	}
	hRaw, ok := raw["hooks"]
	if !ok {
		return nil, nil
	}
	var byEvent map[string][]geminiHookGroupRaw
	if jerr := json.Unmarshal(hRaw, &byEvent); jerr != nil {
		return nil, fmt.Errorf("gemini: parse hooks in %s: %w", path, jerr)
	}
	events := make([]string, 0, len(byEvent))
	for ev := range byEvent {
		events = append(events, ev)
	}
	sort.Strings(events)
	var out []*model.Hook
	for _, ev := range events {
		for _, grp := range byEvent[ev] {
			for _, entry := range grp.Hooks {
				if strings.Contains(entry.Command, "__scope-guard__") {
					continue
				}
				h := &model.Hook{
					Event:      ev,
					Matcher:    grp.Matcher,
					ScriptPath: entry.Command,
				}
				// v2 reverse-mapping. canonicalEv may be one of the
				// per-action enums (pre_shell, post_file_edit, …) — in
				// which case the wire matcher is intrinsic to that
				// canonical event and we deliberately DO NOT echo it
				// into MatcherV2. Otherwise it's a generic event whose
				// matcher is user-supplied and gets preserved.
				canonicalEv, consumedMatcher := reverseGeminiHookEvent(ev, grp.Matcher)
				if canonicalEv != "" {
					h.EventCanonical = canonicalEv
				}
				if grp.Matcher != "" && !consumedMatcher {
					h.MatcherV2 = model.HookMatcher{
						Kind:     "exact",
						Patterns: []string{grp.Matcher},
					}
				}
				// Handlers slice. Gemini's wire timeout is seconds;
				// convert to milliseconds for the canonical TimeoutMs.
				handler := model.HookHandler{
					Kind:      model.HookHandlerCommand,
					Command:   entry.Command,
					TimeoutMs: entry.Timeout * 1000,
				}
				h.Handlers = []model.HookHandler{handler}
				if grp.Sequential != nil {
					seq := *grp.Sequential
					h.Sequential = &seq
				}
				out = append(out, h)
			}
		}
	}
	return out, nil
}

// reverseGeminiHookEvent maps a (wire event, wire matcher) pair back to
// the canonical HookEvent enum. When the pair matches one of the
// per-action canonical events (PreShell, PostShell, PreFileRead,
// PostFileEdit, PreMCPCall, PostMCPCall), the second return is true: the
// caller MUST NOT also record the matcher in MatcherV2 since it's
// intrinsic to the canonical event.
//
// For the generic BeforeTool/AfterTool case (no matcher, or a matcher
// we don't recognize), the function returns the corresponding canonical
// EventPreToolUse / EventPostToolUse and consumedMatcher = false so the
// user-authored matcher is preserved. For everything else
// (SessionStart, Notification, …) it returns the snake-cased canonical
// enum unchanged; unknown event names produce an empty canonical so the
// caller can fall back to the verbatim Event string.
func reverseGeminiHookEvent(wireEvent, wireMatcher string) (canonical model.HookEvent, consumedMatcher bool) {
	switch wireEvent {
	case "BeforeTool":
		switch wireMatcher {
		case "run_shell_command":
			return model.EventPreShell, true
		case "read_file":
			return model.EventPreFileRead, true
		case "mcp_*":
			return model.EventPreMCPCall, true
		default:
			return model.EventPreToolUse, false
		}
	case "AfterTool":
		switch wireMatcher {
		case "run_shell_command":
			return model.EventPostShell, true
		case "write_file":
			return model.EventPostFileEdit, true
		case "mcp_*":
			return model.EventPostMCPCall, true
		default:
			return model.EventPostToolUse, false
		}
	case "SessionStart":
		return model.EventSessionStart, false
	case "SessionEnd":
		return model.EventSessionEnd, false
	case "UserPromptSubmit":
		return model.EventUserPromptSubmit, false
	case "Notification":
		return model.EventNotification, false
	case "PreCompress":
		return model.EventPreCompact, false
	}
	return "", false
}

// readGeminiSettingsMCP parses .gemini/settings.json and returns the
// mcpServers entries. Returns (nil, nil) if the file is absent.
//
// Phase 2a (v0.9.0): populates v2 MCP fields. Gemini's wire schema
// supports both `url` and `httpUrl` for HTTP transports — the former is
// the legacy SSE-friendly shape, the latter is the explicit HTTP
// indicator. We collapse both into Transport: "http"; the per-tool
// emitter re-renders the right wire spelling. `command` means stdio.
// Additional Gemini-specific keys (trust, headers, cwd, includeTools,
// excludeTools, timeout, autoApprove) project to the matching canonical
// fields; everything else is stashed under Extensions["gemini"] for
// round-trip safety.
func readGeminiSettingsMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gemini: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("gemini: parse %s: %w", path, err)
	}
	names := make([]string, 0, len(raw.MCPServers))
	for n := range raw.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		body := raw.MCPServers[n]
		var s struct {
			Command      string            `json:"command"`
			Args         []string          `json:"args"`
			Env          map[string]string `json:"env"`
			URL          string            `json:"url"`
			HTTPURL      string            `json:"httpUrl"`
			Cwd          string            `json:"cwd"`
			Headers      map[string]string `json:"headers"`
			Trust        *bool             `json:"trust"`
			Timeout      int               `json:"timeout"`
			AutoApprove  []string          `json:"autoApprove"`
			IncludeTools []string          `json:"includeTools"`
			ExcludeTools []string          `json:"excludeTools"`
		}
		if jerr := json.Unmarshal(body, &s); jerr != nil {
			return nil, fmt.Errorf("gemini: parse mcpServers[%s] in %s: %w", n, path, jerr)
		}
		// Resolve URL: httpUrl wins when both are present (more
		// specific). Either populates the canonical URL slot so v0.8
		// readers see a value.
		url := s.HTTPURL
		if url == "" {
			url = s.URL
		}
		srv := &model.MCPServer{
			Name:    n,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     url,
		}
		// v2 additive typed fields.
		switch {
		case s.HTTPURL != "":
			srv.Transport = "http"
		case s.URL != "":
			srv.Transport = "http"
		case s.Command != "":
			srv.Transport = "stdio"
		}
		srv.Cwd = s.Cwd
		srv.Headers = s.Headers
		if s.Trust != nil {
			srv.Trust = *s.Trust
		}
		srv.IncludeTools = s.IncludeTools
		srv.ExcludeTools = s.ExcludeTools
		srv.AutoApprove = s.AutoApprove
		// Gemini's MCP timeout is in milliseconds (per CLI docs);
		// canonical TimeoutMs is also milliseconds, so it's a direct
		// copy.
		srv.TimeoutMs = s.Timeout

		// Pick up any other top-level keys we don't model natively and
		// stash them under Extensions["gemini"] so re-emission can
		// preserve them.
		var generic map[string]any
		if jerr := json.Unmarshal(body, &generic); jerr == nil {
			passthrough := map[string]any{}
			for k, v := range generic {
				switch k {
				case "command", "args", "env", "url", "httpUrl",
					"cwd", "headers", "trust", "timeout",
					"autoApprove", "includeTools", "excludeTools":
					continue
				}
				passthrough[k] = v
			}
			if len(passthrough) > 0 {
				srv.Extensions = map[string]any{"gemini": passthrough}
			}
		}
		out = append(out, srv)
	}
	return out, nil
}

// geminiExtensionsFromFM returns a map of all keys in fm that are NOT in
// knownKeys. Used to populate Extensions["gemini"] with passthrough
// frontmatter data so we don't silently drop fields users wrote. Returns
// nil when no unknown keys are present so callers can avoid setting an
// empty Extensions map.
func geminiExtensionsFromFM(fm map[string]any, knownKeys map[string]struct{}) map[string]any {
	if fm == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range fm {
		if _, known := knownKeys[k]; known {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// readFloat coerces a YAML-decoded value into a float64. Accepts
// float64, int, int64, and json.Number forms; returns ok=false otherwise.
func readFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// readInt coerces a YAML-decoded value into an int. Accepts int, int64,
// float64 (when the value is integral); returns ok=false otherwise.
func readInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
	case json.Number:
		n, err := x.Int64()
		if err == nil {
			return int(n), true
		}
	}
	return 0, false
}

// Compile-time check that *GeminiImporter implements Importer.
var _ Importer = (*GeminiImporter)(nil)
