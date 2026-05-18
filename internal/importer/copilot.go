// copilot.go — importer for GitHub Copilot's .github/ configuration.
//
// Inputs (per prism v0.5 design lines 270-282):
//
//   .github/copilot-instructions.md
//   .github/instructions/*.instructions.md    frontmatter: applyTo
//   .github/prompts/*.prompt.md               frontmatter: description,
//                                                          agent, model, tools
//
// v0.8.0 additions (mirrors plugins/copilot.go emissions):
//
//   .github/agents/<slug>.agent.md  → model.Agent (frontmatter + body)
//   .github/mcp.json                → model.MCPServer (standard mcpServers)
//   .mcp.json (project root)        → also recognized (the Copilot CLI walks
//                                     cwd→git-root loading every .mcp.json)
//
// Mapping:
//   - .github/copilot-instructions.md         → .agents/context.md
//   - <slug>.instructions.md (extension-only applyTo)
//                                              → .agents/skills/<slug>/SKILL.md
//   - <slug>.instructions.md (path-style applyTo)
//                                              → .agents/<applyTo-inferred>/context.md
//   - <name>.prompt.md                         → .agents/commands/<name>.md
//     (`agent`, `model`, `tools` fields warned-and-dropped — no canonical analog)

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
	"agents.dev/agents/internal/scope"
)

// copilotInputArgRE matches `${input:NAME}` substitutions in Copilot prompt
// and instruction bodies. The importer uses captured names to populate the
// canonical Arguments slots on Skill and Command (SPEC §4.2.5 / §4.3.5 —
// "Arguments" column for the Copilot target).
var copilotInputArgRE = regexp.MustCompile(`\$\{input:([A-Za-z_][A-Za-z0-9_]*)\}`)

const copilotTool = "copilot"

// CopilotImporter reads `.github/` Copilot configuration.
type CopilotImporter struct{}

// NewCopilot returns a CopilotImporter. See cline.go for naming notes.
func NewCopilot() *CopilotImporter { return &CopilotImporter{} }

// Name returns "copilot".
func (c *CopilotImporter) Name() string { return copilotTool }

// Detect returns true when any of the marker files exist.
func (c *CopilotImporter) Detect(root string) bool {
	markers := []string{
		filepath.Join(root, ".github", "copilot-instructions.md"),
		filepath.Join(root, ".github", "instructions"),
		filepath.Join(root, ".github", "prompts"),
		filepath.Join(root, ".github", "agents"),
		filepath.Join(root, ".github", "mcp.json"),
		filepath.Join(root, ".github", "hooks"),
	}
	for _, p := range markers {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// Import reads root and returns the canonical Project.
func (c *CopilotImporter) Import(root string) (*model.Project, []Warning, error) {
	if !c.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	// --- Root copilot-instructions.md ---
	rootInstr := filepath.Join(root, ".github", "copilot-instructions.md")
	if data, err := os.ReadFile(rootInstr); err == nil {
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("copilot: %s: %w", rootInstr, err)
		}
		proj.Context = &model.Document{
			SourcePath:  rootInstr,
			Frontmatter: fm,
			Body:        provenanceComment(copilotTool, rootInstr) + body,
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("copilot: read %s: %w", rootInstr, err)
	}

	// --- .github/instructions/*.instructions.md ---
	instrDir := filepath.Join(root, ".github", "instructions")
	instrEntries, err := os.ReadDir(instrDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("copilot: read %s: %w", instrDir, err)
	}
	sort.Slice(instrEntries, func(i, j int) bool { return instrEntries[i].Name() < instrEntries[j].Name() })

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
		for _, cm := range proj.Commands {
			if cm.Name == name {
				return true
			}
		}
		return false
	}

	for _, e := range instrEntries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".instructions.md") {
			continue
		}
		full := filepath.Join(instrDir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("copilot: read %s: %w", full, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("copilot: %s: %w", full, err)
		}
		applyTo := copilotApplyToList(fm["applyTo"])
		bodyWithProv := provenanceComment(copilotTool, full) + body
		baseName := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".md"), ".instructions")

		switch {
		case len(applyTo) == 0:
			warnings = append(warnings, Warning{
				SourcePath: relTo(root, full),
				Heuristic:  "missing applyTo frontmatter; imported as a skill (no target known)",
				Severity:   "warn",
			})
			skillName := uniqueName("copilot", slugifyName(baseName), skillExists)
			if skillName == "" {
				skillName = "instructions"
			}
			sk := &model.Skill{
				Name: skillName,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
			}
			copilotApplyV2Skill(sk, fm, nil, body)
			proj.Skills = append(proj.Skills, sk)
		default:
			switch classifyGlobs(applyTo) {
			case globKindExtension:
				skillName := uniqueName("copilot", slugifyName(baseName), skillExists)
				if skillName == "" {
					skillName = "instructions"
				}
				sk := &model.Skill{
					Name:  skillName,
					Globs: applyTo,
					Document: &model.Document{
						SourcePath:  full,
						Frontmatter: fm,
						Body:        bodyWithProv,
					},
				}
				copilotApplyV2Skill(sk, fm, applyTo, body)
				proj.Skills = append(proj.Skills, sk)
			case globKindPath:
				scopePath := inferScopePathFromGlobs(applyTo)
				if scopePath == "" {
					// Fall back to a skill — applyTo had no usable prefix.
					warnings = append(warnings, Warning{
						SourcePath: relTo(root, full),
						Heuristic:  "applyTo path glob had no common directory prefix; imported as a skill",
						Severity:   "info",
					})
					skillName := uniqueName("copilot", slugifyName(baseName), skillExists)
					if skillName == "" {
						skillName = "instructions"
					}
					sk := &model.Skill{
						Name:  skillName,
						Globs: applyTo,
						Document: &model.Document{
							SourcePath:  full,
							Frontmatter: fm,
							Body:        bodyWithProv,
						},
					}
					copilotApplyV2Skill(sk, fm, applyTo, body)
					proj.Skills = append(proj.Skills, sk)
					continue
				}
				copilotAddScope(scopeByPath, scopePath, applyTo, "", bodyWithProv, full)
			case globKindMixed:
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, full),
					Heuristic: "applyTo mixes path-prefix and extension-only patterns; " +
						"imported as scope (path prefix wins)",
					Severity: "warn",
				})
				scopePath := inferScopePathFromGlobs(applyTo)
				if scopePath == "" {
					scopePath = "_unknown"
				}
				copilotAddScope(scopeByPath, scopePath, applyTo, "", bodyWithProv, full)
			}
		}
	}

	// --- .github/prompts/*.prompt.md ---
	promptsDir := filepath.Join(root, ".github", "prompts")
	promptEntries, err := os.ReadDir(promptsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("copilot: read %s: %w", promptsDir, err)
	}
	sort.Slice(promptEntries, func(i, j int) bool { return promptEntries[i].Name() < promptEntries[j].Name() })

	for _, e := range promptEntries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".prompt.md") {
			continue
		}
		full := filepath.Join(promptsDir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("copilot: read %s: %w", full, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("copilot: %s: %w", full, err)
		}
		description, _ := fm["description"].(string)

		// Warn-and-drop unsupported frontmatter fields.
		for _, k := range []string{"agent", "model", "tools"} {
			if v, ok := fm[k]; ok && !copilotIsZero(v) {
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, full),
					Heuristic: fmt.Sprintf(
						"prompt frontmatter field %q dropped — no canonical model equivalent",
						k),
					Severity: "info",
				})
			}
		}

		baseName := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".md"), ".prompt")
		cmdName := slugifyName(baseName)
		if cmdName == "" {
			cmdName = "prompt"
		}
		cmdName = uniqueName("copilot", cmdName, commandExists)
		cmd := &model.Command{
			Name:        cmdName,
			Description: description,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment(copilotTool, full) + body,
			},
		}
		copilotApplyV2Command(cmd, fm, body)
		proj.Commands = append(proj.Commands, cmd)
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

	// .github/agents/<slug>.agent.md — v0.8 native Copilot agents.
	agents, aerr := readCopilotAgents(root)
	if aerr != nil {
		return nil, nil, aerr
	}
	proj.Agents = append(proj.Agents, agents...)

	// MCP: .github/mcp.json (project-local) and .mcp.json (root). The CLI
	// walks from cwd toward the git root loading every .mcp.json it finds;
	// the closer one wins. We import both and merge by name (project-local
	// overrides root). Wrappers (none expected for Copilot but defensive).
	rootMCP, merr := readCopilotMCPFile(filepath.Join(root, ".mcp.json"))
	if merr != nil {
		return nil, nil, merr
	}
	dotMCP, merr := readCopilotMCPFile(filepath.Join(root, ".github", "mcp.json"))
	if merr != nil {
		return nil, nil, merr
	}
	proj.MCP = mergeCopilotMCP(rootMCP, dotMCP)

	// .github/hooks/hooks.json — v0.8.2 preview hooks (gated behind
	// --enable-preview-hooks on emit; importer accepts unconditionally).
	hooks, herr := readCopilotPreviewHooks(root)
	if herr != nil {
		return nil, nil, herr
	}
	proj.Hooks = append(proj.Hooks, hooks...)

	// .github/hooks/__perms-guard__/{policy.json,*.policy.json} — v0.8.2
	// perms-via-hook sidecars. Same shape as cline; companion to
	// emitPermsGuardWrappers in plugins/copilot.go.
	global, scoped, perr := readPermsGuardSidecars(filepath.Join(root, ".github", "hooks", "__perms-guard__"))
	if perr != nil {
		return nil, nil, perr
	}
	if global != nil {
		proj.Permissions = global
	}
	proj.ScopedPermissions = append(proj.ScopedPermissions, scoped...)

	return proj, warnings, nil
}

// readCopilotPreviewHooks parses .github/hooks/hooks.json (Copilot preview
// schema, v0.8.2 emission) back into []*model.Hook. The schema:
//
//	{"version": 1, "hooks": {"<event>": [{"matcher": "...", "hooks":
//	  [{"type": "command", "command": "..."}]}]}}
//
// Commands pointing at __scope-guard__ or __perms-guard__ wrappers are
// projection artifacts — they're skipped (the scoped/perms sources live
// in the canonical .agents/ tree, not this projection).
func readCopilotPreviewHooks(root string) ([]*model.Hook, error) {
	hooksPath := filepath.Join(root, ".github", "hooks", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("copilot: read %s: %w", hooksPath, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Extended Copilot preview hook schema (SPEC §12 — Copilot natively
	// supports `bash` + `powershell` platform-override script keys plus
	// optional `timeout` (seconds), `cwd`, and `env`).
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type       string            `json:"type"`
				Command    string            `json:"command"`
				Bash       string            `json:"bash"`
				Powershell string            `json:"powershell"`
				Timeout    *float64          `json:"timeout"`
				Cwd        string            `json:"cwd"`
				Env        map[string]string `json:"env"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if jerr := json.Unmarshal(data, &doc); jerr != nil {
		return nil, fmt.Errorf("copilot: parse %s: %w", hooksPath, jerr)
	}
	// Reverse the canonical→Copilot event-name mapping. Anything we don't
	// recognize keeps its name verbatim; users targeting other tools can
	// still re-emit (mapClineEvent / mapGeminiHookEvent already accept
	// pass-through events). Copilot uses both PascalCase (Claude-shaped)
	// and camelCase event names in the wild; we normalize either to the
	// canonical v0.8 Event string AND fill EventCanonical (SPEC §4.4.2
	// snake_case enum) at the same time.
	reverse := map[string]string{
		"preToolUse":          "PreToolUse",
		"postToolUse":         "PostToolUse",
		"sessionStart":        "SessionStart",
		"sessionEnd":          "SessionEnd",
		"userPromptSubmitted": "UserPromptSubmit",
		"agentStop":           "Stop",
		"subagentStop":        "SubagentStop",
		"errorOccurred":       "Notification",
		// PascalCase pass-through — Copilot accepts the Claude-style
		// event names directly per the preview docs.
		"PreToolUse":       "PreToolUse",
		"PostToolUse":      "PostToolUse",
		"SessionStart":     "SessionStart",
		"SessionEnd":       "SessionEnd",
		"UserPromptSubmit": "UserPromptSubmit",
		"Stop":             "Stop",
		"SubagentStop":     "SubagentStop",
		"Notification":     "Notification",
	}
	events := make([]string, 0, len(doc.Hooks))
	for k := range doc.Hooks {
		events = append(events, k)
	}
	sort.Strings(events)
	var out []*model.Hook
	for _, event := range events {
		canonical, ok := reverse[event]
		if !ok {
			canonical = event
		}
		for _, grp := range doc.Hooks[event] {
			for _, entry := range grp.Hooks {
				cmd := entry.Command
				if entry.Bash != "" && cmd == "" {
					cmd = entry.Bash
				}
				if strings.Contains(cmd, "__scope-guard__") || strings.Contains(cmd, "__perms-guard__") {
					continue
				}
				h := &model.Hook{
					Event:      canonical,
					Matcher:    grp.Matcher,
					ScriptPath: cmd,
				}
				copilotApplyV2Hook(h, canonical, grp.Matcher, entry.Command, entry.Bash, entry.Powershell, entry.Timeout, entry.Cwd, entry.Env)
				out = append(out, h)
			}
		}
	}
	return out, nil
}

// readCopilotAgents reads .github/agents/<slug>.agent.md into []*model.Agent.
// Strips the `.agent.md` suffix from the filename to recover the agent name;
// `name` and `description` come from frontmatter when present.
func readCopilotAgents(root string) ([]*model.Agent, error) {
	dir := filepath.Join(root, ".github", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("copilot: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Agent
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lname := strings.ToLower(e.Name())
		if !strings.HasSuffix(lname, ".agent.md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("copilot: read %s: %w", full, rerr)
		}
		fm, body, perr := splitFrontmatter(data)
		if perr != nil {
			return nil, fmt.Errorf("copilot: %s: %w", full, perr)
		}
		base := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".md"), ".agent")
		name := base
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
				Body:        provenanceComment(copilotTool, full) + body,
			},
		}
		copilotApplyV2Agent(ag, fm, body)
		out = append(out, ag)
	}
	return out, nil
}

// readCopilotMCPFile reads one .mcp.json (standard {"mcpServers": {...}}
// schema). Returns (nil, nil) when absent.
func readCopilotMCPFile(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("copilot: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw struct {
		MCPServers map[string]struct {
			Command   string            `json:"command"`
			Args      []string          `json:"args"`
			Env       map[string]string `json:"env"`
			URL       string            `json:"url"`
			Type      string            `json:"type"`
			Transport string            `json:"transport"`
			Headers   map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		return nil, fmt.Errorf("copilot: parse %s: %w", path, jerr)
	}
	// Second-pass parse to capture pass-through extension keys (any field the
	// canonical model does not name) so we can park them under
	// Extensions["copilot"] for round-trip.
	var rawMap struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal(data, &rawMap)

	names := make([]string, 0, len(raw.MCPServers))
	for n := range raw.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		s := raw.MCPServers[n]
		// v2-additive Transport: prefer explicit `transport`/`type` keys; fall
		// back to URL-vs-Command heuristic (SPEC §4.5.2 — http when URL set,
		// stdio when Command set).
		transport := s.Transport
		if transport == "" {
			transport = s.Type
		}
		if transport == "" {
			if s.URL != "" {
				transport = "http"
			} else if s.Command != "" {
				transport = "stdio"
			}
		}
		srv := &model.MCPServer{
			Name:      n,
			Command:   s.Command,
			Args:      s.Args,
			Env:       s.Env,
			URL:       s.URL,
			Transport: transport,
			Headers:   s.Headers,
		}
		if entry, ok := rawMap.MCPServers[n]; ok {
			ext := copilotMCPExtensionPassthrough(entry)
			if len(ext) > 0 {
				srv.Extensions = map[string]any{"copilot": ext}
			}
		}
		out = append(out, srv)
	}
	return out, nil
}

// copilotMCPExtensionPassthrough returns a copy of the raw mcp.json server
// entry stripped of keys the canonical MCPServer model already names. The
// residue is parked under Extensions["copilot"] for round-trip fidelity.
func copilotMCPExtensionPassthrough(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	known := map[string]struct{}{
		"command":   {},
		"args":      {},
		"env":       {},
		"url":       {},
		"type":      {},
		"transport": {},
		"headers":   {},
	}
	out := map[string]any{}
	for k, v := range raw {
		if _, ok := known[k]; ok {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergeCopilotMCP combines two MCP server slices, with later entries
// overriding earlier ones by Name. Output is sorted by Name for stability.
func mergeCopilotMCP(base, override []*model.MCPServer) []*model.MCPServer {
	byName := map[string]*model.MCPServer{}
	for _, s := range base {
		if s == nil || s.Name == "" {
			continue
		}
		byName[s.Name] = s
	}
	for _, s := range override {
		if s == nil || s.Name == "" {
			continue
		}
		byName[s.Name] = s
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out
}

// copilotApplyToList coerces the `applyTo` frontmatter value into a
// []string. Copilot accepts either a single glob string ("**/*.py") or a
// list ("['**/*.py', 'src/**']") — we treat both shapes uniformly.
func copilotApplyToList(v any) []string {
	return stringSliceAny(v)
}

// copilotAddScope merges a copilot instruction file into a scope keyed by
// scopePath. Same merge semantics as cursor.addScope.
func copilotAddScope(scopeByPath map[string]*model.Scope, scopePath string, globs []string, description, body, source string) {
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
		}
		if len(sc.Globs) == 0 {
			sc.Globs = scope.DefaultGlobs(scopePath)
		}
		scopeByPath[scopePath] = sc
		return
	}
	sc.Document.Body = strings.TrimRight(sc.Document.Body, "\n") + "\n\n" + body
	sc.Globs = unionStrings(sc.Globs, globs)
	if sc.Description == "" {
		sc.Description = description
	}
}

// copilotIsZero reports whether a frontmatter value is effectively empty
// (nil, empty string, empty slice/map). Used so we don't warn about
// fields that are merely declared with no value.
func copilotIsZero(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	case bool:
		return !t
	}
	return false
}

// Compile-time interface check.
var _ Importer = (*CopilotImporter)(nil)

// -----------------------------------------------------------------------------
// v0.9 / schema-v2 additive population helpers.
//
// These helpers populate the v2 fields described in SPEC §4 alongside the
// existing v0.8 fields the rest of this file fills in. They are STRICTLY
// additive — every v0.8 field still gets its existing value, so existing
// plugin / engine code that reads only v0.8 names keeps working untouched.
//
// The mirror invariant matches the emit side in plugins/copilot.go: the
// canonical (v0.8) frontmatter keys are kept, AND the full frontmatter is
// parked under Extensions["copilot"] for verbatim round-trip on the next
// emit pass. Reserved canonical keys (name, description) are filtered out
// of the extension copy so an extension can never override the parser-
// computed canonical fields (matches copilotExtensionKeys in plugins/).
// -----------------------------------------------------------------------------

// copilotExtensionMap returns a shallow copy of fm with reserved keys
// stripped. Used to seed Extensions["copilot"] for every primitive the
// Copilot importer produces from a frontmatter source.
func copilotExtensionMap(fm map[string]any, reserved ...string) map[string]any {
	if len(fm) == 0 {
		return nil
	}
	skip := map[string]struct{}{}
	for _, k := range reserved {
		skip[k] = struct{}{}
	}
	out := make(map[string]any, len(fm))
	for k, v := range fm {
		if _, ok := skip[k]; ok {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// copilotExtractInputArgNames pulls unique `${input:NAME}` capture names
// from body in first-seen order. The result drives Skill.Arguments and
// Command.Arguments per SPEC §4.2.5 / §4.3.5.
func copilotExtractInputArgNames(body string) []string {
	matches := copilotInputArgRE.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := m[1]
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// copilotApplyV2Skill fills the v2-additive Skill fields from Copilot
// instructions-file frontmatter. applyTo is the canonical applyTo slice
// (may be nil); body is the raw markdown body (used to recover argument
// names from `${input:NAME}` placeholders).
//
// Activation.Modes follows SPEC §4.2.5 Copilot column:
//   - `applyTo: "**"` → Always
//   - any other applyTo  → Glob (with Activation.Globs populated)
//   - missing applyTo    → ModelDecision (the model picks based on description)
func copilotApplyV2Skill(sk *model.Skill, fm map[string]any, applyTo []string, body string) {
	if sk == nil {
		return
	}
	// Activation.Modes + Activation.Globs.
	switch {
	case len(applyTo) == 1 && applyTo[0] == "**":
		sk.Activation.Modes = []model.SkillActivationMode{model.SkillActivationAlways}
	case len(applyTo) > 0:
		sk.Activation.Modes = []model.SkillActivationMode{model.SkillActivationGlob}
		sk.Activation.Globs = append([]string(nil), applyTo...)
	default:
		sk.Activation.Modes = []model.SkillActivationMode{model.SkillActivationModelDecision}
	}

	// AllowedTools — Copilot's `tools:` list.
	if tools := stringSliceAny(fm["tools"]); len(tools) > 0 {
		sk.AllowedTools = tools
	}

	// Model — bare string `model:` slot.
	if m, ok := fm["model"].(string); ok && m != "" {
		sk.Model = m
	}

	// Subagent — Copilot prompts reference an agent via `agent:`. Skills
	// rarely carry this but we mirror the same key for parity.
	if a, ok := fm["agent"].(string); ok && a != "" {
		sk.Subagent = a
	}

	// Description / WhenToUse pass-through if the source carried them.
	if d, ok := fm["description"].(string); ok && d != "" && sk.Description == "" {
		sk.Description = d
	}

	// Arguments — recover `${input:NAME}` names from the body. Names only,
	// per SPEC §4.2.5 (Copilot Arguments column) and the Phase 2a plan.
	for _, name := range copilotExtractInputArgNames(body) {
		sk.Arguments = append(sk.Arguments, model.SkillArgument{Name: name})
	}

	// Extensions["copilot"] — full frontmatter passthrough, sans canonical
	// keys that the v2 fields already cover. The emit side's
	// renderCopilotInstructions strips Skill.Extensions["copilot"] onto
	// frontmatter alongside applyTo for round-trip parity.
	if ext := copilotExtensionMap(fm, "applyTo", "description"); len(ext) > 0 {
		if sk.Extensions == nil {
			sk.Extensions = map[string]any{}
		}
		sk.Extensions["copilot"] = ext
	}
}

// copilotApplyV2Command fills the v2-additive Command fields from Copilot
// prompt-file frontmatter. Mirrors the renderCopilotPrompt emit shape.
func copilotApplyV2Command(cmd *model.Command, fm map[string]any, body string) {
	if cmd == nil {
		return
	}
	if m, ok := fm["model"].(string); ok && m != "" {
		cmd.Model = m
	}
	if tools := stringSliceAny(fm["tools"]); len(tools) > 0 {
		cmd.Tools = tools
	}
	if a, ok := fm["agent"].(string); ok && a != "" {
		cmd.Agent = a
	}
	if hint, ok := fm["argument-hint"].(string); ok && hint != "" {
		cmd.ArgumentHint = hint
	}
	// Arguments — names only, recovered from `${input:NAME}` substitutions
	// in the body (SPEC §4.3.5 Copilot column).
	cmd.Arguments = copilotExtractInputArgNames(body)

	if ext := copilotExtensionMap(fm, "description"); len(ext) > 0 {
		if cmd.Extensions == nil {
			cmd.Extensions = map[string]any{}
		}
		cmd.Extensions["copilot"] = ext
	}
}

// copilotApplyV2Agent fills the v2-additive Agent fields from Copilot
// `.agent.md` frontmatter. The v0.8 emit side (renderCopilotAgent) writes
// every non-reserved frontmatter key verbatim; we round-trip everything
// the canonical model names and park the rest under Extensions["copilot"].
//
// Model handling: Copilot's `model:` can be a single string OR a list
// (model fallbacks). We populate Model with the first entry and
// ModelFallbacks with the remainder so v2 consumers can re-emit a list.
// ModelInvocable is the inverse of `disable-model-invocation:`.
func copilotApplyV2Agent(ag *model.Agent, fm map[string]any, body string) {
	if ag == nil {
		return
	}
	ag.SystemPrompt = body

	switch m := fm["model"].(type) {
	case string:
		if m != "" {
			ag.Model = m
		}
	case []any, []string:
		models := stringSliceAny(m)
		if len(models) > 0 {
			ag.Model = models[0]
			if len(models) > 1 {
				ag.ModelFallbacks = append([]string(nil), models[1:]...)
			}
		}
	}

	if tools := stringSliceAny(fm["tools"]); len(tools) > 0 {
		ag.Tools = tools
	}
	if subs := stringSliceAny(fm["agents"]); len(subs) > 0 {
		ag.AllowedSubagents = subs
	}
	if b, ok := fm["user-invocable"].(bool); ok {
		bb := b
		ag.UserInvocable = &bb
	}
	if b, ok := fm["disable-model-invocation"].(bool); ok {
		inv := !b
		ag.ModelInvocable = &inv
	}

	if ext := copilotExtensionMap(fm, "name", "description"); len(ext) > 0 {
		if ag.Extensions == nil {
			ag.Extensions = map[string]any{}
		}
		ag.Extensions["copilot"] = ext
	}
}

// copilotCanonicalHookEvent maps the v0.8 PascalCase Event string used by
// every plugin to the SPEC §4.4.2 snake_case HookEvent enum. Unknown
// events return the empty HookEvent ("") and the caller leaves
// EventCanonical zero (which is exactly the right "pass-through" behavior
// for native:* events).
var copilotCanonicalHookEvent = map[string]model.HookEvent{
	"PreToolUse":       model.EventPreToolUse,
	"PostToolUse":      model.EventPostToolUse,
	"SessionStart":     model.EventSessionStart,
	"SessionEnd":       model.EventSessionEnd,
	"UserPromptSubmit": model.EventUserPromptSubmit,
	"Stop":             model.EventStop,
	"SubagentStop":     model.EventSubagentStop,
	"Notification":     model.EventNotification,
}

// copilotApplyV2Hook fills the v2-additive Hook fields. Mirrors the
// `{type: "command", command, bash, powershell, timeout, cwd, env}` shape
// of the Copilot preview hooks.json schema (SPEC §12 — Copilot supports
// `Bash + Powershell` script keys natively, so when both are present we
// keep both on a single Handler).
func copilotApplyV2Hook(h *model.Hook, canonicalEvent, matcher, command, bash, powershell string, timeoutSec *float64, cwd string, env map[string]string) {
	if h == nil {
		return
	}
	// EventCanonical (snake_case enum) alongside the v0.8 Event string.
	if mapped, ok := copilotCanonicalHookEvent[canonicalEvent]; ok {
		h.EventCanonical = mapped
	}

	// MatcherV2 — Copilot preview hooks use a single-pattern matcher field;
	// empty matcher ≡ "all", otherwise an exact-pattern match.
	if matcher == "" {
		h.MatcherV2 = model.HookMatcher{Kind: "all"}
	} else {
		h.MatcherV2 = model.HookMatcher{Kind: "exact", Patterns: []string{matcher}}
	}

	// Handlers — single command handler. `bash` + `powershell` are
	// platform-override script keys per SPEC §12 (Copilot row). If both
	// are present we keep the Command empty and rely on the platform-
	// override slots; otherwise Command holds the universal script.
	handler := model.HookHandler{
		Kind: model.HookHandlerCommand,
	}
	switch {
	case bash != "" && powershell != "":
		handler.Bash = bash
		handler.Powershell = powershell
	case bash != "":
		handler.Command = bash
		handler.Bash = bash
	case powershell != "":
		handler.Command = powershell
		handler.Powershell = powershell
	default:
		handler.Command = command
	}
	if timeoutSec != nil {
		handler.TimeoutMs = int(*timeoutSec * 1000)
	}
	if cwd != "" {
		handler.Cwd = cwd
	}
	if len(env) > 0 {
		handler.Env = env
	}
	h.Handlers = []model.HookHandler{handler}

	// The preview JSON schema has no extension-namespace today, so
	// Extensions["copilot"] stays unset by default. Future preview-schema
	// keys land in this slot via the same mechanism MCP uses above.
}
