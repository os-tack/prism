// windsurf.go — importer for Windsurf's .windsurf/rules/*.md format.
//
// Input (per prism v0.5 design lines 254-268):
//
//   .windsurf/rules/*.md   frontmatter: trigger, globs, description
//
// Mapping table:
//   trigger: always_on       → append body to .agents/context.md
//   trigger: glob            → scope at .agents/<inferred-path>/context.md
//                              (or skill if the globs are extension-only;
//                              if no common prefix, fall back to a skill)
//   trigger: model_decision  → .agents/skills/<slugified-description>/SKILL.md
//   trigger: manual          → .agents/commands/<slug>.md
//
// v0.8.0 additions (mirrors plugins/windsurf.go emissions):
//
//   .windsurf/hooks.json      → model.Hook entries (one per (event, entry))
//   .windsurf/mcp_config.json → model.MCPServer (standard mcpServers map)

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
	"agents.dev/agents/internal/scope"
)

const windsurfTool = "windsurf"

// WindsurfImporter reads `.windsurf/`.
type WindsurfImporter struct{}

// NewWindsurf returns a WindsurfImporter. See cline.go for naming notes.
func NewWindsurf() *WindsurfImporter { return &WindsurfImporter{} }

// Name returns "windsurf".
func (w *WindsurfImporter) Name() string { return windsurfTool }

// Detect returns true when `.windsurf/` exists at root.
func (w *WindsurfImporter) Detect(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".windsurf"))
	return err == nil && info.IsDir()
}

// Import reads root and returns the canonical Project.
func (w *WindsurfImporter) Import(root string) (*model.Project, []Warning, error) {
	if !w.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	rulesDir := filepath.Join(root, ".windsurf", "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("windsurf: read %s: %w", rulesDir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	scopeByPath := map[string]*model.Scope{}
	skillExists := func(name string) bool {
		for _, s := range proj.Skills {
			if s.Name == name {
				return true
			}
		}
		return false
	}
	commandExists := func(name string) bool {
		for _, c := range proj.Commands {
			if c.Name == name {
				return true
			}
		}
		return false
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lname := strings.ToLower(e.Name())
		if !(strings.HasSuffix(lname, ".md") || strings.HasSuffix(lname, ".markdown")) {
			continue
		}
		full := filepath.Join(rulesDir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("windsurf: read %s: %w", full, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("windsurf: %s: %w", full, err)
		}
		trigger, _ := fm["trigger"].(string)
		description, _ := fm["description"].(string)
		globs := stringSliceAny(fm["globs"])
		// v2 additive: surface non-standard frontmatter keys via the
		// `windsurf` namespace so a round-trip preserves user data the
		// importer doesn't recognize.
		windsurfExts := windsurfExtensionsFromFM(fm, windsurfRuleKnownKeys)
		// v2 additive: pre-compute Skill.Activation and Scope.Activation
		// from the trigger so each capability creation site can attach
		// them without re-deriving.
		skillModes := windsurfSkillModes(trigger)
		scopeAct := windsurfScopeActivation(trigger)

		bodyWithProv := provenanceComment(windsurfTool, full) + body
		baseName := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".md"), ".markdown")

		switch strings.ToLower(strings.TrimSpace(trigger)) {

		case "always_on", "always-on", "alwayson":
			if proj.Context == nil {
				proj.Context = &model.Document{
					SourcePath: full,
					Body:       bodyWithProv,
				}
			} else {
				proj.Context.Body = strings.TrimRight(proj.Context.Body, "\n") + "\n\n" + bodyWithProv
			}

		case "glob":
			if len(globs) == 0 {
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, full),
					Heuristic:  "trigger=glob but no globs frontmatter; imported as a skill",
					Severity:   "warn",
				})
				skillName := uniqueName("windsurf", slugifyName(baseName), skillExists)
				if skillName == "" {
					skillName = "rule"
				}
				sk := &model.Skill{
					Name:        skillName,
					Description: description,
					Document: &model.Document{
						SourcePath:  full,
						Frontmatter: fm,
						Body:        bodyWithProv,
					},
					Activation: model.SkillActivation{Modes: skillModes},
				}
				if windsurfExts != nil {
					sk.Extensions = map[string]any{windsurfTool: windsurfExts}
				}
				proj.Skills = append(proj.Skills, sk)
				continue
			}
			switch classifyGlobs(globs) {
			case globKindExtension:
				skillName := uniqueName("windsurf", slugifyName(baseName), skillExists)
				if skillName == "" {
					skillName = "rule"
				}
				sk := &model.Skill{
					Name:        skillName,
					Description: description,
					Globs:       globs,
					Document: &model.Document{
						SourcePath:  full,
						Frontmatter: fm,
						Body:        bodyWithProv,
					},
					Activation: model.SkillActivation{
						Modes: skillModes,
						Globs: append([]string(nil), globs...),
					},
				}
				if windsurfExts != nil {
					sk.Extensions = map[string]any{windsurfTool: windsurfExts}
				}
				proj.Skills = append(proj.Skills, sk)
			case globKindPath:
				scopePath := inferScopePathFromGlobs(globs)
				if scopePath == "" {
					// No common directory prefix → fall back to a skill.
					warnings = append(warnings, Warning{
						SourcePath: relTo(root, full),
						Heuristic:  "trigger=glob with no common directory prefix; imported as a skill",
						Severity:   "info",
					})
					skillName := uniqueName("windsurf", slugifyName(baseName), skillExists)
					if skillName == "" {
						skillName = "rule"
					}
					sk := &model.Skill{
						Name:        skillName,
						Description: description,
						Globs:       globs,
						Document: &model.Document{
							SourcePath:  full,
							Frontmatter: fm,
							Body:        bodyWithProv,
						},
						Activation: model.SkillActivation{
							Modes: skillModes,
							Globs: append([]string(nil), globs...),
						},
					}
					if windsurfExts != nil {
						sk.Extensions = map[string]any{windsurfTool: windsurfExts}
					}
					proj.Skills = append(proj.Skills, sk)
					continue
				}
				windsurfAddScope(scopeByPath, scopePath, globs, description, bodyWithProv, full, scopeAct, windsurfExts)
			case globKindMixed:
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, full),
					Heuristic: "globs mix path-prefix and extension-only patterns; " +
						"imported as scope (path prefix wins) — split into separate rules for clean import",
					Severity: "warn",
				})
				scopePath := inferScopePathFromGlobs(globs)
				if scopePath == "" {
					scopePath = "_unknown"
				}
				windsurfAddScope(scopeByPath, scopePath, globs, description, bodyWithProv, full, scopeAct, windsurfExts)
			}

		case "model_decision", "model-decision", "modeldecision":
			skillName := slugifyName(description)
			if skillName == "" {
				skillName = slugifyName(baseName)
			}
			if skillName == "" {
				skillName = "rule"
			}
			skillName = uniqueName("windsurf", skillName, skillExists)
			sk := &model.Skill{
				Name:        skillName,
				Description: description,
				Globs:       globs,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
				Activation: model.SkillActivation{
					Modes: skillModes,
					Globs: append([]string(nil), globs...),
				},
			}
			if windsurfExts != nil {
				sk.Extensions = map[string]any{windsurfTool: windsurfExts}
			}
			proj.Skills = append(proj.Skills, sk)

		case "manual":
			cmdName := slugifyName(baseName)
			if cmdName == "" {
				cmdName = "command"
			}
			cmdName = uniqueName("windsurf", cmdName, commandExists)
			cmd := &model.Command{
				Name:        cmdName,
				Description: description,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
			}
			if windsurfExts != nil {
				cmd.Extensions = map[string]any{windsurfTool: windsurfExts}
			}
			proj.Commands = append(proj.Commands, cmd)

		default:
			// Unknown / missing trigger → warn and treat as a model-decision skill.
			warnings = append(warnings, Warning{
				SourcePath: relTo(root, full),
				Heuristic: fmt.Sprintf(
					"unknown trigger %q; imported as a skill", trigger),
				Severity: "warn",
			})
			skillName := slugifyName(baseName)
			if skillName == "" {
				skillName = "rule"
			}
			skillName = uniqueName("windsurf", skillName, skillExists)
			sk := &model.Skill{
				Name:        skillName,
				Description: description,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
				Activation: model.SkillActivation{Modes: skillModes},
			}
			if windsurfExts != nil {
				sk.Extensions = map[string]any{windsurfTool: windsurfExts}
			}
			proj.Skills = append(proj.Skills, sk)
		}
	}

	// Flush scopes in stable order.
	if len(scopeByPath) > 0 {
		paths := make([]string, 0, len(scopeByPath))
		for k := range scopeByPath {
			paths = append(paths, k)
		}
		sort.Strings(paths)
		for _, k := range paths {
			proj.Scopes = append(proj.Scopes, scopeByPath[k])
		}
	}

	// .windsurf/hooks.json — v0.8 Cascade Hooks dispatch table.
	hooks, herr := readWindsurfHooks(root)
	if herr != nil {
		return nil, nil, herr
	}
	proj.Hooks = append(proj.Hooks, hooks...)

	// .windsurf/mcp_config.json — v0.8 project-local MCP config.
	mcps, merr := readWindsurfMCP(filepath.Join(root, ".windsurf", "mcp_config.json"))
	if merr != nil {
		return nil, nil, merr
	}
	proj.MCP = append(proj.MCP, mcps...)

	return proj, warnings, nil
}

// readWindsurfHooks parses .windsurf/hooks.json into []*model.Hook. The
// document shape is {"hooks": {"<event>": [{"command": "...", ...}]}};
// command strings are kept verbatim in Hook.ScriptPath. The plugin wraps
// scripts in `bash <quoted-path>`, so we strip a single leading `bash `
// when present so the canonical model holds the bare script path.
// Wrappers under __scope-guard__/ are skipped (projection artifacts).
//
// v2 additive (mirrors plugins/windsurf.go capability matrix): each Hook
// also carries an EventCanonical (snake_case → typed HookEvent), an empty
// MatcherV2 (Windsurf uses native per-action events, no matcher), and a
// Handlers list with one command handler. The plugin emits only the bash
// shape (no powershell, no cwd), but the reader is permissive: it accepts
// per-entry `bash`, `powershell`, and `cwd` fields if a user authored
// them by hand, and surfaces them on the Handler.
func readWindsurfHooks(root string) ([]*model.Hook, error) {
	path := filepath.Join(root, ".windsurf", "hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("windsurf: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var doc struct {
		Hooks map[string][]struct {
			Command    string `json:"command"`
			ShowOutput bool   `json:"show_output"`
			Bash       string `json:"bash"`
			Powershell string `json:"powershell"`
			Cwd        string `json:"cwd"`
			Disabled   bool   `json:"disabled"`
		} `json:"hooks"`
	}
	if jerr := json.Unmarshal(data, &doc); jerr != nil {
		return nil, fmt.Errorf("windsurf: parse %s: %w", path, jerr)
	}
	events := make([]string, 0, len(doc.Hooks))
	for ev := range doc.Hooks {
		events = append(events, ev)
	}
	sort.Strings(events)
	var out []*model.Hook
	for _, ev := range events {
		for _, entry := range doc.Hooks[ev] {
			cmd := strings.TrimSpace(entry.Command)
			if strings.Contains(cmd, "__scope-guard__") {
				continue
			}
			cmd = strings.TrimPrefix(cmd, "bash ")
			cmd = strings.Trim(cmd, "'")
			h := &model.Hook{
				Event:          ev,
				ScriptPath:     cmd,
				EventCanonical: windsurfMapEventToCanonical(ev),
				MatcherV2:      model.HookMatcher{Kind: "all"},
				Disabled:       entry.Disabled,
			}
			handler := model.HookHandler{
				Kind:       model.HookHandlerCommand,
				Command:    cmd,
				Bash:       entry.Bash,
				Powershell: entry.Powershell,
				Cwd:        entry.Cwd,
			}
			h.Handlers = []model.HookHandler{handler}
			// v2 extensions: surface the windsurf-specific show_output
			// flag so a round-trip preserves it. Other targets ignore.
			if entry.ShowOutput {
				h.Extensions = map[string]any{
					windsurfTool: map[string]any{"show_output": true},
				}
			}
			out = append(out, h)
		}
	}
	return out, nil
}

// windsurfMapEventToCanonical maps a Windsurf snake_case event name onto
// the canonical HookEvent enum per SPEC §12. Unknown names fall through
// to "native:<verbatim>" so the v2 EventCanonical channel carries the raw
// value through without loss (a future projection back to Windsurf can
// recover the original event name verbatim).
func windsurfMapEventToCanonical(ev string) model.HookEvent {
	switch strings.ToLower(strings.TrimSpace(ev)) {
	case "pre_run_command":
		return model.EventPreShell
	case "post_run_command":
		return model.EventPostShell
	case "pre_read_code":
		return model.EventPreFileRead
	case "post_write_code":
		return model.EventPostFileEdit
	case "pre_mcp_tool_use":
		return model.EventPreMCPCall
	case "post_mcp_tool_use":
		return model.EventPostMCPCall
	case "pre_user_prompt":
		return model.EventUserPromptSubmit
	case "post_user_prompt":
		// Closest canonical equivalent: there is no canonical
		// "post_user_prompt"; fall through to native pass-through so the
		// information survives the round-trip.
		return model.HookEvent("native:" + ev)
	case "post_setup_worktree":
		return model.EventWorktreeCreate
	}
	// Pass-through any other recognized known event; if it matches a
	// canonical name verbatim (snake_case), preserve as-is so the round-trip
	// is lossless. Otherwise prefix with native: so EventCanonical is
	// never silently overwritten with garbage.
	canonical := model.HookEvent(strings.ToLower(strings.TrimSpace(ev)))
	if windsurfIsCanonicalEvent(canonical) {
		return canonical
	}
	return model.HookEvent("native:" + ev)
}

// windsurfIsCanonicalEvent reports whether ev is one of the canonical
// HookEvent constants defined in model.go. Kept inline to avoid a
// reflection-based check.
func windsurfIsCanonicalEvent(ev model.HookEvent) bool {
	switch ev {
	case model.EventSessionStart, model.EventSessionEnd, model.EventSessionResume,
		model.EventUserPromptSubmit, model.EventPreToolUse, model.EventPostToolUse,
		model.EventPostToolUseFailure, model.EventPermissionRequest,
		model.EventPreShell, model.EventPostShell, model.EventPreFileRead,
		model.EventPostFileEdit, model.EventPreMCPCall, model.EventPostMCPCall,
		model.EventSubagentStart, model.EventSubagentStop, model.EventStop,
		model.EventPreCompact, model.EventPostCompact, model.EventNotification,
		model.EventWorktreeCreate, model.EventWorktreeRemove,
		model.EventTaskCompleted, model.EventConfigChange, model.EventError:
		return true
	}
	return false
}

// readWindsurfMCP parses .windsurf/mcp_config.json (standard {"mcpServers":
// {...}} schema) into []*model.MCPServer.
//
// v2 additive: each server also surfaces Transport (stdio / http / sse,
// inferred from the entry shape), Headers, Auth, Disabled, and any
// unrecognized top-level keys via Extensions["windsurf"]. Windsurf supports
// both `url` and `serverUrl` as aliases for the HTTP endpoint; both are
// accepted with `url` winning on conflict (matches the plugin's emission).
func readWindsurfMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("windsurf: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Decode permissively into map[string]any so we can capture both the
	// recognized fields and any unknown ones for Extensions.
	var raw struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		return nil, fmt.Errorf("windsurf: parse %s: %w", path, jerr)
	}
	names := make([]string, 0, len(raw.MCPServers))
	for n := range raw.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		s := raw.MCPServers[n]
		srv := &model.MCPServer{Name: n}
		// Recognized fields. Use stringFromAny / stringSliceAny so YAML/JSON
		// shape drift doesn't crash the importer.
		srv.Command, _ = s["command"].(string)
		srv.Args = stringSliceAny(s["args"])
		srv.Env = stringMapAny(s["env"])
		srv.URL, _ = s["url"].(string)
		if srv.URL == "" {
			// Windsurf also accepts `serverUrl` as an alias for `url`.
			srv.URL, _ = s["serverUrl"].(string)
		}
		srv.Cwd, _ = s["cwd"].(string)
		srv.Headers = stringMapAny(s["headers"])
		srv.Disabled, _ = s["disabled"].(bool)
		if t, ok := s["transport"].(string); ok && t != "" {
			srv.Transport = t
		} else {
			// Infer transport from the entry shape.
			switch {
			case srv.Command != "":
				srv.Transport = "stdio"
			case srv.URL != "":
				srv.Transport = "http"
			}
		}
		if auth := windsurfParseMCPAuth(s["auth"]); auth != nil {
			srv.Auth = auth
		}
		// Surface unrecognized keys via Extensions["windsurf"].
		if exts := windsurfExtensionsFromFM(s, windsurfMCPKnownKeys); exts != nil {
			srv.Extensions = map[string]any{windsurfTool: exts}
		}
		out = append(out, srv)
	}
	return out, nil
}

// windsurfParseMCPAuth decodes an MCP server's `auth` block into a typed
// *model.MCPAuth. The recognized shape mirrors SPEC §4.5.2:
//
//	{ "scheme": "bearer", "token": "${env:FOO}" }
//	{ "scheme": "header", "headers": {"X-API-Key": "..."} }
//	{ "scheme": "oauth", ... }
//
// Returns nil when v is absent or not a map.
func windsurfParseMCPAuth(v any) *model.MCPAuth {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	auth := &model.MCPAuth{}
	auth.Scheme, _ = m["scheme"].(string)
	auth.Token, _ = m["token"].(string)
	auth.Headers = stringMapAny(m["headers"])
	if auth.Scheme == "" && auth.Token == "" && len(auth.Headers) == 0 {
		return nil
	}
	return auth
}

// stringMapAny coerces a YAML/JSON-decoded value into map[string]string,
// dropping non-string values. Used for env/headers blocks where the wire
// format is map[string]string but the decoder hands us map[string]any.
func stringMapAny(v any) map[string]string {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]string); ok {
		return m
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// windsurfAddScope merges a windsurf rule into the scope keyed by
// scopePath. Same merge semantics as cursor.addScope but free-function.
//
// v2 additive: act carries the ScopeActivation derived from the source
// trigger (always/glob/manual/model_decision); exts carries the
// windsurf-namespaced frontmatter pass-through. Both are folded onto the
// freshly-created Scope; on the merge path the first non-empty Activation
// and Extensions win to keep the result deterministic.
func windsurfAddScope(scopeByPath map[string]*model.Scope, scopePath string, globs []string, description, body, source string, act model.ScopeActivation, exts map[string]any) {
	sc, ok := scopeByPath[scopePath]
	if !ok {
		sc = &model.Scope{
			Path:        scopePath,
			Globs:       append([]string(nil), globs...),
			Description: description,
			Priority:    model.PriorityNormal,
			Document: &model.Document{
				SourcePath: source,
				Body:       body,
			},
			Activation: act,
		}
		if len(sc.Globs) == 0 {
			sc.Globs = scope.DefaultGlobs(scopePath)
		}
		if exts != nil {
			sc.Extensions = map[string]any{windsurfTool: exts}
		}
		scopeByPath[scopePath] = sc
		return
	}
	sc.Document.Body = strings.TrimRight(sc.Document.Body, "\n") + "\n\n" + body
	sc.Globs = unionStrings(sc.Globs, globs)
	if sc.Description == "" {
		sc.Description = description
	}
	if sc.Activation == "" {
		sc.Activation = act
	}
	if sc.Extensions == nil && exts != nil {
		sc.Extensions = map[string]any{windsurfTool: exts}
	}
}

// windsurfRuleKnownKeys is the set of frontmatter keys the importer
// natively understands; any other key surfaces via Extensions["windsurf"]
// so a round-trip preserves user data.
var windsurfRuleKnownKeys = map[string]struct{}{
	"trigger":     {},
	"globs":       {},
	"description": {},
}

// windsurfMCPKnownKeys is the set of mcpServers[name] keys the importer
// recognizes directly. Anything else surfaces via Extensions["windsurf"].
var windsurfMCPKnownKeys = map[string]struct{}{
	"command":   {},
	"args":      {},
	"env":       {},
	"url":       {},
	"serverUrl": {},
	"cwd":       {},
	"headers":   {},
	"auth":      {},
	"disabled":  {},
	"transport": {},
}

// windsurfSkillModes returns the SkillActivation.Modes derived from a
// Windsurf trigger string. The four canonical mappings are:
//
//	always_on       → SkillActivationAlways
//	glob            → SkillActivationGlob
//	manual          → SkillActivationManual
//	model_decision  → SkillActivationModelDecision
//
// Unknown / missing triggers map to ModelDecision (which is how the
// importer's default branch treats them — as a model-decision skill).
func windsurfSkillModes(trigger string) []model.SkillActivationMode {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "always_on", "always-on", "alwayson":
		return []model.SkillActivationMode{model.SkillActivationAlways}
	case "glob":
		return []model.SkillActivationMode{model.SkillActivationGlob}
	case "manual":
		return []model.SkillActivationMode{model.SkillActivationManual}
	case "model_decision", "model-decision", "modeldecision":
		return []model.SkillActivationMode{model.SkillActivationModelDecision}
	}
	return []model.SkillActivationMode{model.SkillActivationModelDecision}
}

// windsurfScopeActivation returns the ScopeActivation derived from a
// Windsurf trigger string. Mirrors windsurfSkillModes but for scopes:
//
//	always_on       → ScopeActivationAlways
//	glob            → ScopeActivationGlob
//	manual          → ScopeActivationManual
//	model_decision  → ScopeActivationModelDecision
//
// Unknown / missing triggers return ScopeActivationCascade (the default
// for Path-set scopes per SPEC §4.7.2).
func windsurfScopeActivation(trigger string) model.ScopeActivation {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "always_on", "always-on", "alwayson":
		return model.ScopeActivationAlways
	case "glob":
		return model.ScopeActivationGlob
	case "manual":
		return model.ScopeActivationManual
	case "model_decision", "model-decision", "modeldecision":
		return model.ScopeActivationModelDecision
	}
	return model.ScopeActivationCascade
}

// windsurfExtensionsFromFM returns the sub-map for Extensions["windsurf"]
// derived from a frontmatter (or any map[string]any), excluding any keys
// the importer already consumes directly (knownKeys). Returns nil when
// every key is recognized so callers can skip allocating an empty map.
//
// The captured values are kept verbatim — the canonical model treats
// Extensions as opaque pass-through.
func windsurfExtensionsFromFM(fm map[string]any, knownKeys map[string]struct{}) map[string]any {
	if len(fm) == 0 {
		return nil
	}
	var out map[string]any
	for k, v := range fm {
		if _, known := knownKeys[k]; known {
			continue
		}
		if out == nil {
			out = make(map[string]any)
		}
		out[k] = v
	}
	return out
}

// Compile-time interface check.
var _ Importer = (*WindsurfImporter)(nil)
