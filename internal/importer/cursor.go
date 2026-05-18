// cursor.go: imports .cursor/rules/*.mdc + .cursor/mcp.json plus the
// v0.8.0 Cursor 2.4+ surfaces (agents, skills, commands, hooks) into the
// canonical *model.Project shape.
//
// v0.8.0 additions (mirrors plugins/cursor.go emissions):
//
//   .cursor/agents/<n>.md              → model.Agent (frontmatter + body)
//   .cursor/skills/<dir>/SKILL.md      → model.Skill (frontmatter + body)
//   .cursor/commands/<n>.md            → model.Command (bare markdown body)
//   .cursor/hooks.json                 → model.Hook entries (one per (event, entry))
//
// Mapping (per /tmp/prism-v0.5-design.md lines 168-208):
//
//   alwaysApply: true                  → append to .agents/context.md
//   globs: ["src/billing/**"]          → scope at .agents/src/billing/context.md
//                                        with scopes.yaml (via Scope.Description
//                                        and Scope.Globs)
//   globs: ["**/*.pdf"], description   → .agents/skills/<name>/SKILL.md
//   no globs, description present      → .agents/skills/<name>/SKILL.md
//                                        (model-decision-style trigger)
//
// Heuristic for skill-vs-scope when globs are present:
//   - every glob is extension-only (starts with "**/*.")   → skill
//   - every glob has a path prefix (no leading "*", no "**" at start)
//                                                          → scope
//   - mixed                                                → scope + warning

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

// CursorImporter reads .cursor/rules/*.mdc and .cursor/mcp.json.
type CursorImporter struct{}

// NewCursor constructs a CursorImporter. Four importers share the
// internal/importer package, so each constructor is suffixed with the
// importer name to avoid a top-level New() collision.
func NewCursor() *CursorImporter { return &CursorImporter{} }

// Name returns "cursor".
func (i *CursorImporter) Name() string { return "cursor" }

// Detect returns true when .cursor/ or .cursorrules is present at root.
func (i *CursorImporter) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".cursor")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, ".cursorrules")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Import reads root and produces the canonical Project.
func (i *CursorImporter) Import(root string) (*model.Project, []Warning, error) {
	if !i.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	// Legacy .cursorrules → entire body becomes root context.
	legacyPath := filepath.Join(root, ".cursorrules")
	if data, err := os.ReadFile(legacyPath); err == nil {
		proj.Context = &model.Document{
			SourcePath: legacyPath,
			Body:       provenanceComment("cursor", legacyPath) + string(data),
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("cursor: read %s: %w", legacyPath, err)
	}

	// Scoped accumulator: scope.Path → *model.Scope. Used so multiple .mdc
	// files targeting the same directory append into a single context.md.
	scopeByPath := map[string]*model.Scope{}

	// Walk .cursor/rules/*.mdc.
	rulesDir := filepath.Join(root, ".cursor", "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("cursor: read %s: %w", rulesDir, err)
	}
	// Sort for determinism (filepath.WalkDir returns sorted, ReadDir too,
	// but make it explicit).
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".mdc") {
			continue
		}
		mdcPath := filepath.Join(rulesDir, e.Name())
		data, err := os.ReadFile(mdcPath)
		if err != nil {
			return nil, nil, fmt.Errorf("cursor: read %s: %w", mdcPath, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("cursor: %s: %w", mdcPath, err)
		}

		description, _ := fm["description"].(string)
		globs := stringSliceAny(fm["globs"])
		alwaysApply, _ := fm["alwaysApply"].(bool)

		bodyWithProv := provenanceComment("cursor", mdcPath) + body

		switch {
		case alwaysApply:
			// Append to root context.
			if proj.Context == nil {
				proj.Context = &model.Document{
					SourcePath: mdcPath,
					Body:       bodyWithProv,
				}
			} else {
				proj.Context.Body = strings.TrimRight(proj.Context.Body, "\n") + "\n\n" + bodyWithProv
			}

		case len(globs) == 0:
			// No globs, no alwaysApply → skill (model-decision trigger).
			i.addSkill(proj, e.Name(), description, body, mdcPath, nil, fm)

		default:
			classification := classifyGlobs(globs)
			switch classification {
			case globKindExtension:
				i.addSkill(proj, e.Name(), description, body, mdcPath, globs, fm)
			case globKindPath:
				i.addScope(scopeByPath, globs, description, bodyWithProv, mdcPath, fm, alwaysApply)
			case globKindMixed:
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, mdcPath),
					Heuristic: "globs mix path-prefix and extension-only patterns; " +
						"imported as scope (path prefix wins) — split into separate .mdc files for clean import",
					Severity: "warn",
				})
				i.addScope(scopeByPath, globs, description, bodyWithProv, mdcPath, fm, alwaysApply)
			}
		}
	}

	// Flush scopeByPath into proj.Scopes in stable order.
	paths := make([]string, 0, len(scopeByPath))
	for p := range scopeByPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		proj.Scopes = append(proj.Scopes, scopeByPath[p])
	}

	// .cursor/skills/<dir>/SKILL.md (Cursor 2.4+ native skills format).
	// The directory name becomes the skill name; the SKILL.md body carries
	// its own YAML frontmatter (description, trigger, globs) just like the
	// Claude format.
	skillsRoot := filepath.Join(root, ".cursor", "skills")
	cursorSkillEntries, err := os.ReadDir(skillsRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("cursor: read %s: %w", skillsRoot, err)
	}
	sort.Slice(cursorSkillEntries, func(a, b int) bool { return cursorSkillEntries[a].Name() < cursorSkillEntries[b].Name() })
	for _, e := range cursorSkillEntries {
		if !e.IsDir() {
			continue
		}
		skillMD := filepath.Join(skillsRoot, e.Name(), "SKILL.md")
		data, rerr := os.ReadFile(skillMD)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				continue
			}
			return nil, nil, fmt.Errorf("cursor: read %s: %w", skillMD, rerr)
		}
		fm, body, perr := splitFrontmatter(data)
		if perr != nil {
			return nil, nil, fmt.Errorf("cursor: %s: %w", skillMD, perr)
		}
		name := slugifyName(e.Name())
		if name == "" {
			name = "skill"
		}
		name = uniqueName("cursor", name, func(candidate string) bool {
			for _, sk := range proj.Skills {
				if sk.Name == candidate {
					return true
				}
			}
			return false
		})
		desc, _ := fm["description"].(string)
		trig, _ := fm["trigger"].(string)
		globs := stringSliceAny(fm["globs"])
		alwaysApply, _ := fm["alwaysApply"].(bool)
		sk := &model.Skill{
			Name:        name,
			Description: desc,
			Trigger:     trig,
			Globs:       globs,
			Document: &model.Document{
				SourcePath:  skillMD,
				Frontmatter: fm,
				Body:        provenanceComment("cursor", skillMD) + body,
			},
		}
		// v2 additive: SkillActivation (Modes + Globs) + Extensions.
		sk.Activation = cursorSkillActivation(alwaysApply, globs)
		if exts := cursorExtensionsFromFM(fm, cursorSkillKnownKeys); exts != nil {
			sk.Extensions = map[string]any{"cursor": exts}
		}
		proj.Skills = append(proj.Skills, sk)
	}

	// .cursor/agents/<n>.md (Cursor 2.4+ subagents).
	cursorAgents, err := readCursorAgents(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Agents = append(proj.Agents, cursorAgents...)

	// .cursor/commands/<n>.md (Cursor 2.4+ slash commands; bare markdown).
	cursorCmds, err := readCursorCommands(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Commands = append(proj.Commands, cursorCmds...)

	// .cursor/hooks.json (Cursor 2.4+ hook dispatch table).
	cursorHooks, err := readCursorHooks(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Hooks = append(proj.Hooks, cursorHooks...)

	// .cursor/mcp.json.
	mcpPath := filepath.Join(root, ".cursor", "mcp.json")
	mcps, err := readCursorMCP(mcpPath)
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = mcps

	return proj, warnings, nil
}

// readCursorAgents reads .cursor/agents/<n>.md into []*model.Agent.
// Each file is a markdown document with optional YAML frontmatter; the
// filename (sans .md) becomes the agent name.
func readCursorAgents(root string) ([]*model.Agent, error) {
	dir := filepath.Join(root, ".cursor", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cursor: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("cursor: read %s: %w", full, rerr)
		}
		fm, body, perr := splitFrontmatter(data)
		if perr != nil {
			return nil, fmt.Errorf("cursor: %s: %w", full, perr)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		desc, _ := fm["description"].(string)
		ag := &model.Agent{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment("cursor", full) + body,
			},
		}
		// v2 additive: SystemPrompt = body verbatim (no provenance prefix);
		// pull Model / ReadOnly / Background from frontmatter when present.
		ag.SystemPrompt = body
		if model, ok := fm["model"].(string); ok {
			ag.Model = model
		}
		if ro, ok := fm["readonly"].(bool); ok {
			b := ro
			ag.ReadOnly = &b
		}
		if bg, ok := fm["is_background"].(bool); ok {
			b := bg
			ag.Background = &b
		}
		if exts := cursorExtensionsFromFM(fm, cursorAgentKnownKeys); exts != nil {
			ag.Extensions = map[string]any{"cursor": exts}
		}
		out = append(out, ag)
	}
	return out, nil
}

// readCursorCommands reads .cursor/commands/<n>.md into []*model.Command.
// Cursor commands are bare markdown (no frontmatter required); we still
// pass any frontmatter through for forward-compat.
func readCursorCommands(root string) ([]*model.Command, error) {
	dir := filepath.Join(root, ".cursor", "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cursor: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Command
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("cursor: read %s: %w", full, rerr)
		}
		fm, body, perr := splitFrontmatter(data)
		if perr != nil {
			return nil, fmt.Errorf("cursor: %s: %w", full, perr)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		desc, _ := fm["description"].(string)
		out = append(out, &model.Command{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment("cursor", full) + body,
			},
		})
	}
	return out, nil
}

// readCursorHooks reads .cursor/hooks.json into []*model.Hook. Hook entries
// pointing at our own scope-guard wrappers (.cursor/hooks/__scope-guard__/)
// are recognized and skipped from re-import — they're projection artifacts,
// not source-of-truth hooks.
func readCursorHooks(root string) ([]*model.Hook, error) {
	path := filepath.Join(root, ".cursor", "hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cursor: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Decode loosely so unknown keys (e.g. failClosed, timeout) can flow
	// into Extensions["cursor"]. The first decode pulls typed fields out
	// of each entry; the second pass keeps the raw map for ext capture.
	var rawRich struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &rawRich); err != nil {
		return nil, fmt.Errorf("cursor: parse %s: %w", path, err)
	}
	events := make([]string, 0, len(rawRich.Hooks))
	for ev := range rawRich.Hooks {
		events = append(events, ev)
	}
	sort.Strings(events)
	var out []*model.Hook
	for _, ev := range events {
		for _, entry := range rawRich.Hooks[ev] {
			cmd, _ := entry["command"].(string)
			matcher, _ := entry["matcher"].(string)
			if strings.Contains(cmd, "__scope-guard__") {
				continue
			}
			h := &model.Hook{
				Event:      ev,
				Matcher:    matcher,
				ScriptPath: cmd,
			}
			// v2 additive populations.
			h.EventCanonical = cursorEventToCanonical(ev)
			if matcher != "" {
				h.MatcherV2 = model.HookMatcher{Kind: "regex", Patterns: []string{matcher}}
			}
			handler := model.HookHandler{
				Kind:    model.HookHandlerCommand,
				Command: cmd,
			}
			if t, ok := entry["timeout"]; ok {
				handler.TimeoutMs = cursorTimeoutSecondsToMs(t)
			}
			if fc, ok := entry["failClosed"].(bool); ok {
				handler.FailClosed = fc
			}
			h.Handlers = []model.HookHandler{handler}
			if exts := cursorExtensionsFromFM(entry, cursorHookEntryKnownKeys); exts != nil {
				h.Extensions = map[string]any{"cursor": exts}
			}
			out = append(out, h)
		}
	}
	return out, nil
}

// cursorEventToCanonical maps a Cursor 2.4+ wire event name back to the
// canonical prism HookEvent. Unknown / Cursor-native-only events return
// "" (caller leaves EventCanonical zero so v0.8 readers keep the
// passthrough string in Event).
func cursorEventToCanonical(ev string) model.HookEvent {
	switch ev {
	case "preToolUse":
		return model.EventPreToolUse
	case "postToolUse":
		return model.EventPostToolUse
	case "sessionStart":
		return model.EventSessionStart
	case "sessionEnd":
		return model.EventSessionEnd
	case "beforeSubmitPrompt":
		return model.EventUserPromptSubmit
	case "stop":
		return model.EventStop
	case "beforeShellExecution":
		return model.EventPreShell
	case "afterShellExecution":
		return model.EventPostShell
	case "beforeTabFileRead":
		return model.EventPreFileRead
	case "afterFileEdit", "afterTabFileEdit":
		return model.EventPostFileEdit
	}
	return ""
}

// cursorTimeoutSecondsToMs converts a numeric `timeout:` (seconds, per
// Cursor's hooks.json schema) into milliseconds. Accepts the typical
// JSON number kinds yaml/json unmarshalers emit (float64, int).
func cursorTimeoutSecondsToMs(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n * 1000)
	case int:
		return n * 1000
	case int64:
		return int(n) * 1000
	}
	return 0
}

// addSkill appends a Skill derived from one .mdc file.
//
// Skill.Name is derived from the .mdc basename (without the extension);
// the skill body is prefixed with the provenance comment via the
// caller-passed body bytes wrapped into a synthetic Document.
//
// v0.9.0 (Phase 2a): additionally populates SkillActivation (Modes +
// Globs) and Extensions["cursor"] for any non-canonical .mdc
// frontmatter keys.
func (i *CursorImporter) addSkill(proj *model.Project, mdcBase, description, body, source string, globs []string, fm map[string]any) {
	name := slugifyName(strings.TrimSuffix(mdcBase, ".mdc"))
	if name == "" {
		name = "rule"
	}
	// Detect duplicate names — if the same slug already exists, suffix it.
	name = uniqueName("cursor", name, func(candidate string) bool {
		for _, sk := range proj.Skills {
			if sk.Name == candidate {
				return true
			}
		}
		return false
	})

	doc := &model.Document{
		SourcePath: source,
		// SKILL.md carries its own frontmatter when serialized; the
		// parser populates Skill.Description/Globs from the Document
		// frontmatter, so we keep the Document body as just the
		// (provenance-prefixed) markdown text.
		Body: provenanceComment("cursor", source) + body,
	}
	alwaysApply, _ := fm["alwaysApply"].(bool)
	sk := &model.Skill{
		Name:        name,
		Description: description,
		Globs:       globs,
		Document:    doc,
		Activation:  cursorSkillActivation(alwaysApply, globs),
	}
	if exts := cursorExtensionsFromFM(fm, cursorSkillKnownKeys); exts != nil {
		sk.Extensions = map[string]any{"cursor": exts}
	}
	proj.Skills = append(proj.Skills, sk)
}

// addScope merges the .mdc into a scope keyed by the longest common
// directory prefix of its globs. If the result has zero useful prefix
// (e.g. globs all start with `**`), the scope is placed at the root —
// but that case is already filtered upstream (the classifier returns
// globKindExtension in that situation).
//
// v0.9.0 (Phase 2a): populates Activation (Always | Cascade |
// ModelDecision) and merges Extensions["cursor"] from non-canonical
// frontmatter keys.
func (i *CursorImporter) addScope(scopeByPath map[string]*model.Scope, globs []string, description, body, source string, fm map[string]any, alwaysApply bool) {
	scopePath := inferScopePathFromGlobs(globs)
	sc, ok := scopeByPath[scopePath]
	if !ok {
		sc = &model.Scope{
			Path:        scopePath,
			Globs:       append([]string(nil), globs...),
			Description: description,
			Priority:    model.PriorityNormal,
			Activation:  cursorScopeActivation(alwaysApply, scopePath),
			Document: &model.Document{
				SourcePath: source,
				Body:       body,
			},
		}
		// Fall back to the default scope globs when the inferred path
		// produces something distinct from the user's globs — but per
		// the design, we prefer the user's globs verbatim. Only use
		// DefaultGlobs if the user provided none (shouldn't happen in
		// this branch, but defensive).
		if len(sc.Globs) == 0 {
			sc.Globs = scope.DefaultGlobs(scopePath)
		}
		if exts := cursorExtensionsFromFM(fm, cursorScopeKnownKeys); exts != nil {
			sc.Extensions = map[string]any{"cursor": exts}
		}
		scopeByPath[scopePath] = sc
		return
	}
	// Existing scope: append body, union globs, take first non-empty
	// description.
	sc.Document.Body = strings.TrimRight(sc.Document.Body, "\n") + "\n\n" + body
	sc.Globs = unionStrings(sc.Globs, globs)
	if sc.Description == "" {
		sc.Description = description
	}
	// v2: upgrade Activation only if previously unset (preserve the
	// stronger Always over a later Cascade).
	if sc.Activation == "" {
		sc.Activation = cursorScopeActivation(alwaysApply, sc.Path)
	} else if alwaysApply && sc.Activation != model.ScopeActivationAlways {
		sc.Activation = model.ScopeActivationAlways
	}
	// v2: merge Extensions["cursor"] from this additional .mdc into the
	// existing scope's extension map. Newer keys win on collision.
	if exts := cursorExtensionsFromFM(fm, cursorScopeKnownKeys); exts != nil {
		if sc.Extensions == nil {
			sc.Extensions = map[string]any{}
		}
		existing, _ := sc.Extensions["cursor"].(map[string]any)
		if existing == nil {
			existing = map[string]any{}
		}
		for k, v := range exts {
			existing[k] = v
		}
		sc.Extensions["cursor"] = existing
	}
}

// readCursorMCP parses .cursor/mcp.json into []*model.MCPServer.
// Returns (nil, nil) when the file is absent.
func readCursorMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cursor: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Loose decode to capture v2 fields (transport, headers) and any
	// unknown keys for Extensions["cursor"] pass-through.
	var rich struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &rich); err != nil {
		return nil, fmt.Errorf("cursor: parse %s: %w", path, err)
	}
	names := make([]string, 0, len(rich.MCPServers))
	for n := range rich.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		entry := rich.MCPServers[n]
		srv := &model.MCPServer{Name: n}
		if cmd, ok := entry["command"].(string); ok {
			srv.Command = cmd
		}
		if args := stringSliceAny(entry["args"]); len(args) > 0 {
			srv.Args = args
		}
		if envMap, ok := entry["env"].(map[string]any); ok && len(envMap) > 0 {
			env := make(map[string]string, len(envMap))
			for k, v := range envMap {
				if s, ok := v.(string); ok {
					env[k] = s
				}
			}
			srv.Env = env
		}
		if u, ok := entry["url"].(string); ok {
			srv.URL = u
		}
		// v2 additive: transport, headers.
		if t, ok := entry["transport"].(string); ok {
			srv.Transport = t
		}
		if h, ok := entry["headers"].(map[string]any); ok && len(h) > 0 {
			hdrs := make(map[string]string, len(h))
			for k, v := range h {
				if s, ok := v.(string); ok {
					hdrs[k] = s
				}
			}
			srv.Headers = hdrs
		}
		if exts := cursorExtensionsFromFM(entry, cursorMCPKnownKeys); exts != nil {
			srv.Extensions = map[string]any{"cursor": exts}
		}
		out = append(out, srv)
	}
	return out, nil
}

// globKind classifies the shape of a frontmatter globs list.
type globKind int

const (
	globKindExtension globKind = iota // every glob is extension-only ("**/*.ext")
	globKindPath                      // every glob has a directory prefix ("src/**")
	globKindMixed                     // a mix of the two
)

func classifyGlobs(globs []string) globKind {
	if len(globs) == 0 {
		return globKindExtension
	}
	allExt := true
	allPath := true
	for _, g := range globs {
		if isExtensionGlob(g) {
			allPath = false
		} else if isPathGlob(g) {
			allExt = false
		} else {
			// Pattern looks like neither (e.g. just "**" or "*") —
			// treat as extension so it falls into skill-land rather
			// than creating a confusing root-level scope.
			allPath = false
		}
	}
	switch {
	case allExt:
		return globKindExtension
	case allPath:
		return globKindPath
	default:
		return globKindMixed
	}
}

// isExtensionGlob reports whether g looks like "**/*.ext" — i.e. it
// matches files anywhere in the tree by extension only, without
// constraining the directory.
func isExtensionGlob(g string) bool {
	g = strings.TrimSpace(g)
	return strings.HasPrefix(g, "**/*.") || strings.HasPrefix(g, "*.")
}

// isPathGlob reports whether g looks like it constrains a directory
// prefix (e.g. "src/**", "tests/unit/**", "docs/foo.md").
func isPathGlob(g string) bool {
	g = strings.TrimSpace(g)
	if g == "" {
		return false
	}
	// Reject anything that leads with a glob meta-char.
	switch g[0] {
	case '*', '?', '[', '{':
		return false
	}
	return true
}

// inferScopePathFromGlobs returns the longest common directory prefix of
// the (path-prefixed) globs. Used to decide where to root the imported
// scope.
//
// Caller has already established that every glob is a path glob (via
// classifyGlobs == globKindPath), so we can split on '/' and walk
// segments.
func inferScopePathFromGlobs(globs []string) string {
	if len(globs) == 0 {
		return ""
	}
	// Split each glob into segments, dropping the trailing pattern
	// segments (any segment containing a meta-char terminates the prefix).
	prefixes := make([][]string, 0, len(globs))
	for _, g := range globs {
		segs := strings.Split(g, "/")
		var pre []string
		for _, s := range segs {
			if strings.ContainsAny(s, "*?[]{}") {
				break
			}
			pre = append(pre, s)
		}
		prefixes = append(prefixes, pre)
	}
	if len(prefixes) == 0 {
		return ""
	}
	// Compute the longest common prefix across all globs.
	common := prefixes[0]
	for _, p := range prefixes[1:] {
		common = commonPrefix(common, p)
		if len(common) == 0 {
			break
		}
	}
	result := strings.Join(common, "/")
	if !scope.SafePath(result) {
		return ""
	}
	return result
}

func commonPrefix(a, b []string) []string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			break
		}
		out = append(out, a[i])
	}
	return out
}

// unionStrings returns the deduplicated union of two string slices,
// preserving order (a first, then any new entries from b).
func unionStrings(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// relTo returns path relative to root for use in user-facing warnings.
// Falls back to the input path if relativization fails.
func relTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// --- v0.9.0 / Phase 2a helpers (additive v2 field population) ----------

// Canonical-key sets per primitive: anything in the frontmatter that's
// NOT in the set is captured into extensions.cursor.* verbatim so the
// emit side (plugins/cursor.go) can round-trip it.
var (
	cursorSkillKnownKeys = map[string]struct{}{
		"description": {}, "trigger": {}, "globs": {}, "alwaysApply": {},
	}
	cursorScopeKnownKeys = map[string]struct{}{
		"description": {}, "globs": {}, "alwaysApply": {},
	}
	cursorAgentKnownKeys = map[string]struct{}{
		"description": {}, "model": {}, "readonly": {}, "is_background": {},
	}
	cursorHookEntryKnownKeys = map[string]struct{}{
		"matcher": {}, "command": {}, "timeout": {}, "failClosed": {},
	}
	cursorMCPKnownKeys = map[string]struct{}{
		"command": {}, "args": {}, "env": {}, "url": {},
		"transport": {}, "headers": {},
	}
)

// cursorExtensionsFromFM returns the subset of fm whose keys are NOT in
// the canonical-key set, or nil when no such keys exist. Used to fill
// Extensions["cursor"] on each primitive for verbatim round-trip.
func cursorExtensionsFromFM(fm map[string]any, known map[string]struct{}) map[string]any {
	if len(fm) == 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range fm {
		if _, isKnown := known[k]; isKnown {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cursorSkillActivation derives the SkillActivation block from the
// alwaysApply flag and globs list:
//   - alwaysApply: true            → Always
//   - else if len(globs) > 0       → Glob
//   - else                         → ModelDecision
//
// Globs are always copied through so v2 readers see them under
// Activation.Globs even when the top-level Skill.Globs slice is also
// populated.
func cursorSkillActivation(alwaysApply bool, globs []string) model.SkillActivation {
	a := model.SkillActivation{Globs: append([]string(nil), globs...)}
	switch {
	case alwaysApply:
		a.Modes = []model.SkillActivationMode{model.SkillActivationAlways}
	case len(globs) > 0:
		a.Modes = []model.SkillActivationMode{model.SkillActivationGlob}
	default:
		a.Modes = []model.SkillActivationMode{model.SkillActivationModelDecision}
	}
	return a
}

// cursorScopeActivation picks the ScopeActivation for an imported scope:
//   - alwaysApply: true            → Always
//   - else if scopePath != ""      → Cascade (path-rooted scope)
//   - else                         → ModelDecision
func cursorScopeActivation(alwaysApply bool, scopePath string) model.ScopeActivation {
	switch {
	case alwaysApply:
		return model.ScopeActivationAlways
	case scopePath != "":
		return model.ScopeActivationCascade
	default:
		return model.ScopeActivationModelDecision
	}
}

// Compile-time check that *CursorImporter implements Importer.
var _ Importer = (*CursorImporter)(nil)
