// continue.go — importer for Continue's .continue/ format.
//
// Inputs (per prism v0.5 design lines 242-252):
//
//   .continue/rules/*.md         frontmatter: name, globs, regex,
//                                description, alwaysApply
//   .continue/mcpServers/*.yaml  one server per file
//
// v0.8.0 additions (mirrors plugins/continue_plugin.go emissions):
//
//   .continue/prompts/<n>.md    → model.Command (frontmatter: name,
//                                  description, invokable: true)
//   .continue/permissions.yaml  → model.Permissions (allow / ask / exclude
//                                  with Tool(pattern) syntax — translated
//                                  back to tool:pattern for the canonical model)
//
// v0.9 / schema v2 additions (mirrors plugins/continue_plugin.go Phase 2a
// emissions). The importer populates new v2 fields alongside the v0.8
// shape so round-trips keep the canonical model in sync:
//
//   - Skill.Activation.{Modes,Globs,ContentRegex}, Skill.Extensions["continue"]
//   - Command.{Description,Arguments,Model}, Command.Extensions["continue"]
//   - Scope.{Activation,Globs,Description,Priority}, Scope.Extensions["continue"]
//   - MCPServer.{Transport,Headers,Auth}, MCPServer.Extensions["continue"]
//   - Permissions.Extensions["continue"]
//
// Mapping:
//   - alwaysApply: true AND no globs    → append body to .agents/context.md
//   - regex: (sole anchor; no globs/description/alwaysApply)
//                                       → warn and drop
//   - regex: (combined with other anchors)
//                                       → populate Skill.Activation.ContentRegex
//   - globs present                     → cursor-style skill-vs-scope heuristic
//                                         (extension globs → skill,
//                                          path globs      → scope,
//                                          mixed           → scope + warning)
//   - no globs, description present     → skill (model-decision style)
//   - one server per YAML               → one entry in proj.MCP
//
// TODO(Phase 2.5, SPEC §4.4): Continue's hooks schema is verbatim Claude's
// (shared via plugins/hooks_claude_shape.go). v0.8 importer treats hooks as
// Unsupported; this importer does NOT yet read .continue/hooks/. Wire up
// once the Phase 2.5 flip lands.

package importer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/scope"

	"gopkg.in/yaml.v3"
)

const continueTool = "continue"

// ContinueImporter reads `.continue/`.
type ContinueImporter struct{}

// NewContinue returns a ContinueImporter. See cline.go for naming notes.
func NewContinue() *ContinueImporter { return &ContinueImporter{} }

// Name returns "continue".
func (c *ContinueImporter) Name() string { return continueTool }

// Detect returns true when `.continue/` exists at root.
func (c *ContinueImporter) Detect(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".continue"))
	return err == nil && info.IsDir()
}

// Import reads root and returns the canonical Project.
func (c *ContinueImporter) Import(root string) (*model.Project, []Warning, error) {
	if !c.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	// --- .continue/rules/*.md ---
	rulesDir := filepath.Join(root, ".continue", "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("continue: read %s: %w", rulesDir, err)
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
			return nil, nil, fmt.Errorf("continue: read %s: %w", full, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("continue: %s: %w", full, err)
		}

		fmName, _ := fm["name"].(string)
		description, _ := fm["description"].(string)
		globs := stringSliceAny(fm["globs"])
		alwaysApply, _ := fm["alwaysApply"].(bool)
		regex, _ := fm["regex"].(string)
		regex = strings.TrimSpace(regex)

		// Regex with no other anchors → warn and drop. The canonical model
		// has nowhere meaningful to hang the rule (it would be a skill
		// with no description, no globs, and no body anchor — the LLM
		// has no way to know when to fire it). When regex coexists with
		// globs / description / alwaysApply we populate
		// Skill.Activation.ContentRegex on the resulting primitive
		// instead (v2 additive — see SPEC §4.2.4).
		if regex != "" && len(globs) == 0 && description == "" && !alwaysApply {
			warnings = append(warnings, Warning{
				SourcePath: relTo(root, full),
				Heuristic:  "regex triggers are unsupported in the canonical model; rule dropped",
				Severity:   "warn",
			})
			continue
		}

		bodyWithProv := provenanceComment(continueTool, full) + body
		ext := continueRuleExtensions(fm)

		switch {
		case alwaysApply && len(globs) == 0:
			// Append to root context.
			if proj.Context == nil {
				proj.Context = &model.Document{
					SourcePath: full,
					Body:       bodyWithProv,
				}
			} else {
				proj.Context.Body = strings.TrimRight(proj.Context.Body, "\n") + "\n\n" + bodyWithProv
			}

		case len(globs) == 0:
			// No globs → skill (model-decision trigger).
			skillName := continueDeriveSkillName(fmName, e.Name())
			skillName = uniqueName("continue", skillName, skillExists)
			proj.Skills = append(proj.Skills, &model.Skill{
				Name:        skillName,
				Description: description,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
				// v2 additive (SPEC §4.2.4): activation modes mirror the
				// frontmatter triggers. Model-decision is the default when
				// only description anchors the rule; manual is added if
				// invokable: true is set.
				Activation: buildSkillActivation(fm, globs, regex, alwaysApply),
				Extensions: wrapContinueExtensions(ext),
			})

		default:
			classification := classifyGlobs(globs)
			switch classification {
			case globKindExtension:
				skillName := continueDeriveSkillName(fmName, e.Name())
				skillName = uniqueName("continue", skillName, skillExists)
				proj.Skills = append(proj.Skills, &model.Skill{
					Name:        skillName,
					Description: description,
					Globs:       globs,
					Document: &model.Document{
						SourcePath:  full,
						Frontmatter: fm,
						Body:        bodyWithProv,
					},
					Activation: buildSkillActivation(fm, globs, regex, alwaysApply),
					Extensions: wrapContinueExtensions(ext),
				})
			case globKindPath:
				continueAddScope(scopeByPath, globs, description, bodyWithProv, full, fm, alwaysApply, e.Name())
			case globKindMixed:
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, full),
					Heuristic: "globs mix path-prefix and extension-only patterns; " +
						"imported as scope (path prefix wins) — split into separate rules for clean import",
					Severity: "warn",
				})
				continueAddScope(scopeByPath, globs, description, bodyWithProv, full, fm, alwaysApply, e.Name())
			}
		}
	}

	// Flush scopes.
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

	// --- .continue/mcpServers/*.yaml ---
	mcpDir := filepath.Join(root, ".continue", "mcpServers")
	mcpEntries, err := os.ReadDir(mcpDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("continue: read %s: %w", mcpDir, err)
	}
	sort.Slice(mcpEntries, func(i, j int) bool { return mcpEntries[i].Name() < mcpEntries[j].Name() })

	for _, e := range mcpEntries {
		if e.IsDir() {
			continue
		}
		ln := strings.ToLower(e.Name())
		if !(strings.HasSuffix(ln, ".yaml") || strings.HasSuffix(ln, ".yml")) {
			continue
		}
		full := filepath.Join(mcpDir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("continue: read %s: %w", full, err)
		}
		var raw struct {
			Name      string            `yaml:"name"`
			Command   string            `yaml:"command"`
			Args      []string          `yaml:"args"`
			Env       map[string]string `yaml:"env"`
			URL       string            `yaml:"url"`
			Type      string            `yaml:"type"`
			Transport string            `yaml:"transport"`
			Headers   map[string]string `yaml:"headers"`
			Cwd       string            `yaml:"cwd"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, nil, fmt.Errorf("continue: parse %s: %w", full, err)
		}
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".yaml"), ".yml")
		}
		// Continue's transport key may be spelled `type:` (the canonical
		// schema in Continue's docs) or `transport:` (informal usage we
		// accept defensively). Map the wire spelling to prism's
		// canonical enum: `streamable-http` (Continue's hyphenated form)
		// → `http`; `stdio`, `sse` ride through unchanged.
		rawTransport := raw.Type
		if rawTransport == "" {
			rawTransport = raw.Transport
		}
		transport := continueTransportToCanonical(rawTransport)
		// Auth extraction: when the headers carry an
		// `Authorization: Bearer ${{ secrets.VAR }}` entry, lift it
		// onto MCPAuth.Token (canonical `${env:VAR}` form is the
		// closest we have to Continue's secrets store reference). The
		// header itself stays in Headers so emit can re-render it
		// faithfully if the engine round-trips back through Continue.
		auth := continueExtractMCPAuth(raw.Headers)
		// Preserve Continue's source spelling of the transport key so a
		// round-trip back through the emitter can re-emit the verbatim
		// wire form. The emit currently drops type/transport entirely;
		// Phase 2.5 will wire it in. Until then this is a passthrough.
		var mcpExt map[string]any
		if rawTransport != "" {
			mcpExt = map[string]any{"type": rawTransport}
		}
		proj.MCP = append(proj.MCP, &model.MCPServer{
			Name:       name,
			Command:    raw.Command,
			Args:       raw.Args,
			Env:        raw.Env,
			URL:        raw.URL,
			Transport:  transport,
			Cwd:        raw.Cwd,
			Headers:    raw.Headers,
			Auth:       auth,
			Extensions: wrapContinueExtensions(mcpExt),
		})
	}
	sort.Slice(proj.MCP, func(i, j int) bool { return proj.MCP[i].Name < proj.MCP[j].Name })

	// .continue/prompts/<n>.md — v0.8 native invokable slash commands.
	prompts, perr := readContinuePrompts(root)
	if perr != nil {
		return nil, nil, perr
	}
	for _, cmd := range prompts {
		proj.Commands = append(proj.Commands, cmd)
	}

	// .continue/permissions.yaml — v0.8 native permissions (replaces the
	// perms-guard wrapper round-trip).
	perms, perr := readContinuePermissions(root)
	if perr != nil {
		return nil, nil, perr
	}
	if perms != nil {
		proj.Permissions = perms
	}

	return proj, warnings, nil
}

// readContinuePrompts reads .continue/prompts/<n>.md into []*model.Command.
// Frontmatter `name` overrides the basename when present; the filename
// (sans .md) is the fallback. Plugin sets `invokable: true` in every
// prompt; we keep that in the frontmatter pass-through.
func readContinuePrompts(root string) ([]*model.Command, error) {
	dir := filepath.Join(root, ".continue", "prompts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("continue: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Command
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("continue: read %s: %w", full, rerr)
		}
		fm, body, fmerr := splitFrontmatter(data)
		if fmerr != nil {
			return nil, fmt.Errorf("continue: %s: %w", full, fmerr)
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if n, ok := fm["name"].(string); ok && n != "" {
			name = n
		}
		desc, _ := fm["description"].(string)
		model_, _ := fm["model"].(string)
		// v2 additive: extract Handlebars-style {{name}} placeholders
		// from the prompt body as command arguments. SPEC §4.3.2 uses
		// the canonical model's []string Arguments form (positional
		// names). De-duplicate so repeated `{{foo}}` references in the
		// body produce one argument entry.
		args := extractHandlebarsArgs(body)
		ext := continuePromptExtensions(fm)
		out = append(out, &model.Command{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment(continueTool, full) + body,
			},
			Arguments:  args,
			Model:      model_,
			Extensions: wrapContinueExtensions(ext),
		})
	}
	return out, nil
}

// readContinuePermissions reads .continue/permissions.yaml into
// *model.Permissions, reversing the plugin's Tool(pattern) → tool:pattern
// translation. Returns (nil, nil) when absent or empty.
func readContinuePermissions(root string) (*model.Permissions, error) {
	path := filepath.Join(root, ".continue", "permissions.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("continue: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw struct {
		Allow   []string `yaml:"allow"`
		Ask     []string `yaml:"ask"`
		Exclude []string `yaml:"exclude"`
	}
	if uerr := yaml.Unmarshal(data, &raw); uerr != nil {
		return nil, fmt.Errorf("continue: parse %s: %w", path, uerr)
	}
	convert := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, r := range in {
			out = append(out, continueRuleToCanonical(r))
		}
		return out
	}
	allow := convert(raw.Allow)
	ask := convert(raw.Ask)
	deny := convert(raw.Exclude)
	if len(allow)+len(ask)+len(deny) == 0 {
		return nil, nil
	}
	return &model.Permissions{
		Allow: allow,
		Deny:  deny,
		Ask:   ask,
	}, nil
}

// continueRuleToCanonical translates Continue's `Tool(pattern)` syntax
// back to prism's canonical `tool:pattern`. Bare `Tool` (no parens)
// becomes lowercased `tool` (no colon).
func continueRuleToCanonical(rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return ""
	}
	open := strings.Index(rule, "(")
	if open < 0 {
		return strings.ToLower(rule)
	}
	closeP := strings.LastIndex(rule, ")")
	if closeP <= open {
		return strings.ToLower(rule)
	}
	tool := strings.ToLower(strings.TrimSpace(rule[:open]))
	pattern := rule[open+1 : closeP]
	return tool + ":" + pattern
}

// continueDeriveSkillName prefers the frontmatter `name:` value, falling
// back to the file basename (without extension). Slugified for safety.
func continueDeriveSkillName(fmName, filename string) string {
	if s := slugifyName(fmName); s != "" {
		return s
	}
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if s := slugifyName(base); s != "" {
		return s
	}
	return "rule"
}

// continueAddScope merges a rule into the scope keyed by the longest
// common directory prefix of its globs. Mirrors cursor's addScope but is
// a free function (no receiver) since the continue importer carries no
// state beyond its return value.
//
// fm is the source rule's frontmatter (used to pull extensions and the
// optional `priority:` override); alwaysApply lets us pick the right v2
// Activation enum; filename is the basename of the source rule file
// (used to read a `NN-name.md` priority prefix into extensions).
func continueAddScope(scopeByPath map[string]*model.Scope, globs []string, description, body, source string, fm map[string]any, alwaysApply bool, filename string) {
	scopePath := inferScopePathFromGlobs(globs)
	ext := continueRuleExtensions(fm)
	// NN-name.md filename prefix → numeric priority stash. The canonical
	// model only supports normal|high, so the raw integer rides in
	// Extensions["continue"]["priority"] for round-trip fidelity; emit
	// can decide whether to surface it. We do NOT promote it onto
	// model.Scope.Priority since the enum doesn't have integer slots.
	if pri, ok := continueFilenamePriority(filename); ok {
		ext = mergeExtensions(ext, map[string]any{"priority": pri})
	}
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
			// v2 additive (SPEC §4.7.2): activation mirrors the rule's
			// trigger. Always wins over Glob; Cascade is reserved for
			// path-set scopes derived from nested CLAUDE.md-style sources.
			Activation: continueScopeActivation(alwaysApply, len(globs) > 0, description),
			Extensions: wrapContinueExtensions(ext),
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
	// Merge the new rule's extensions into the existing scope's
	// extensions.continue map (additive — keep both rules' keys when
	// they don't collide; the later rule wins on collision so the
	// last-merged file matches user expectation).
	if len(ext) > 0 {
		existing := readContinueExtensions(sc.Extensions)
		sc.Extensions = wrapContinueExtensions(mergeExtensions(existing, ext))
	}
	// If the new rule supplies a stronger activation (Always trumps
	// Glob trumps ModelDecision), upgrade.
	if upgrade := continueScopeActivation(alwaysApply, len(globs) > 0, description); scopeActivationRank(upgrade) > scopeActivationRank(sc.Activation) {
		sc.Activation = upgrade
	}
}

// ---------------------------------------------------------------------------
// v2 helpers (SPEC §4.x — additive fields populated alongside v0.8).
// ---------------------------------------------------------------------------

// handlebarsRE matches `{{name}}` placeholders in command bodies, where
// `name` is a simple identifier (letters/digits/underscore/dash). Continue
// uses Handlebars-style substitution; we extract the placeholder names
// (without braces) as the command's positional argument list.
var handlebarsRE = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_\-]*)\s*\}\}`)

// extractHandlebarsArgs scans body for `{{name}}` placeholders and returns
// the unique names in first-seen order. Used to populate Command.Arguments
// for v2 (SPEC §4.3.2) from Continue's prompt-file bodies. We deliberately
// ignore Handlebars helpers and block expressions (`{{#each}}`, `{{> partial}}`
// etc.) since Continue's docs only call out bare `{{name}}` placeholders;
// anything richer is left as-is in the body for the LLM to consume.
func extractHandlebarsArgs(body string) []string {
	matches := handlebarsRE.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
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

// buildSkillActivation returns the v2 SkillActivation for a Continue rule.
// The mode set mirrors the rule's frontmatter triggers (SPEC §4.2.4):
//
//   - alwaysApply: true        → Always
//   - invokable:  true         → Manual
//   - globs:      [...]        → Glob (added when globs are present)
//   - default                  → ModelDecision (the LLM picks based on description)
//
// Multiple modes can apply simultaneously — e.g. an invokable rule with
// globs gets both Manual and Glob. ContentRegex carries the verbatim
// `regex:` field; Globs carries the verbatim `globs:` list.
func buildSkillActivation(fm map[string]any, globs []string, regex string, alwaysApply bool) model.SkillActivation {
	act := model.SkillActivation{
		Globs:        append([]string(nil), globs...),
		ContentRegex: regex,
	}
	if alwaysApply {
		act.Modes = append(act.Modes, model.SkillActivationAlways)
	}
	// Continue's prompt files use `invokable: true`; rules don't normally
	// carry this key but we tolerate it for completeness so a rule that
	// graduated from prompt → rule keeps its invocation surface.
	if v, ok := fm["invokable"].(bool); ok && v {
		act.Modes = append(act.Modes, model.SkillActivationManual)
		t := true
		act.UserInvocable = &t
	}
	if len(globs) > 0 {
		act.Modes = append(act.Modes, model.SkillActivationGlob)
	}
	if len(act.Modes) == 0 {
		act.Modes = []model.SkillActivationMode{model.SkillActivationModelDecision}
	}
	return act
}

// continueScopeActivation picks the v2 ScopeActivation enum for an
// imported rule (SPEC §4.7.2). Path-set scopes default to Cascade; an
// alwaysApply rule promotes to Always; a glob-only rule with a meaningful
// scope path keeps Glob (the engine still files it under the scope
// directory but activates on glob match too); description-only rules
// (rare here — those go through the skill branch) map to ModelDecision.
func continueScopeActivation(alwaysApply, hasGlobs bool, description string) model.ScopeActivation {
	switch {
	case alwaysApply:
		return model.ScopeActivationAlways
	case hasGlobs:
		return model.ScopeActivationGlob
	case description != "":
		return model.ScopeActivationModelDecision
	default:
		return model.ScopeActivationCascade
	}
}

// scopeActivationRank totally-orders ScopeActivation values so the scope
// merge can pick the strongest trigger when multiple rules land in the
// same scope. Always > Glob > ModelDecision > Cascade > Manual.
func scopeActivationRank(a model.ScopeActivation) int {
	switch a {
	case model.ScopeActivationAlways:
		return 4
	case model.ScopeActivationGlob:
		return 3
	case model.ScopeActivationModelDecision:
		return 2
	case model.ScopeActivationCascade:
		return 1
	case model.ScopeActivationManual:
		return 0
	}
	return 0
}

// continueFilenamePriority parses a `NN-name.md` filename prefix into an
// integer. Returns (n, true) when the prefix is a positive integer
// followed by `-`; otherwise (0, false). This is Continue's convention
// for ordering rules in the auto-attach pipeline.
func continueFilenamePriority(filename string) (int, bool) {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	idx := strings.Index(base, "-")
	if idx <= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(base[:idx])
	if err != nil {
		return 0, false
	}
	return n, true
}

// continueRuleExtensions extracts the `extensions.continue` block from a
// rule's frontmatter (SPEC §5.1). Returns nil when absent or unparseable.
// Mirrors plugins/continue_plugin.go's continueExtensions accessor; kept
// inverted here so the importer round-trips the emit's passthrough block.
func continueRuleExtensions(fm map[string]any) map[string]any {
	if fm == nil {
		return nil
	}
	raw, ok := fm["extensions"]
	if !ok {
		return nil
	}
	outer, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	inner, ok := outer["continue"]
	if !ok {
		return nil
	}
	m, ok := inner.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	return m
}

// continuePromptExtensions is the prompt-file analogue of
// continueRuleExtensions. Prompts and rules share the
// `extensions.continue.*` namespace (SPEC §5.1) so the accessor is the
// same — kept as a named alias for clarity at call sites.
func continuePromptExtensions(fm map[string]any) map[string]any {
	return continueRuleExtensions(fm)
}

// readContinueExtensions reads the existing `extensions["continue"]`
// payload back from a primitive's Extensions map. Returns nil when the
// outer map is nil, when "continue" is missing, or when the value isn't
// a map. Used by continueAddScope's merge path.
func readContinueExtensions(ext map[string]any) map[string]any {
	if ext == nil {
		return nil
	}
	raw, ok := ext["continue"]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// wrapContinueExtensions wraps an extensions.continue payload into the
// canonical primitive-level Extensions map shape, namespacing under the
// "continue" key per SPEC §5.1. Returns nil for empty payloads so we
// don't decorate primitives with empty extension blocks.
func wrapContinueExtensions(ext map[string]any) map[string]any {
	if len(ext) == 0 {
		return nil
	}
	return map[string]any{"continue": ext}
}

// mergeExtensions returns a new map containing all keys from base
// followed by all keys from overlay (overlay wins on collision). Either
// argument may be nil. The result is a fresh map so callers don't share
// state across primitives.
func mergeExtensions(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// continueTransportToCanonical maps Continue's transport spelling to
// prism's canonical enum (SPEC §4.5.2). `streamable-http` (Continue's
// hyphenated form for the HTTP transport) → `http`; `stdio` and `sse`
// pass through. Empty input → empty (the engine treats unspecified as
// stdio-or-http inferred from Command/URL).
func continueTransportToCanonical(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "":
		return ""
	case "streamable-http", "streamable_http", "streamablehttp":
		return "http"
	case "stdio":
		return "stdio"
	case "sse":
		return "sse"
	case "http":
		return "http"
	}
	// Unknown — pass through verbatim. The validator decides whether to
	// reject; we don't want the importer to swallow new transports the
	// schema may add upstream.
	return t
}

// continueBearerRE detects an `Authorization: Bearer ${{ secrets.VAR }}`
// header so the importer can lift it onto MCPAuth.Token. The `${{ secrets.X }}`
// reference is rewritten to the canonical `${env:X}` form (the closest
// prism-native shape for a deferred secret resolution).
var continueBearerRE = regexp.MustCompile(`^\s*Bearer\s+\$\{\{\s*secrets\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}\s*$`)

// continueExtractMCPAuth inspects an MCP server's Headers map for an
// `Authorization: Bearer ${{ secrets.VAR }}` entry and returns the
// corresponding MCPAuth. Returns nil when no matching header is found.
// The Authorization header itself is left in place so the emit can
// re-render the verbatim wire form.
func continueExtractMCPAuth(headers map[string]string) *model.MCPAuth {
	if len(headers) == 0 {
		return nil
	}
	// Case-insensitive lookup — HTTP header names aren't case-sensitive
	// and Continue's docs use mixed-case `Authorization` while users
	// occasionally write `authorization`.
	var raw string
	for k, v := range headers {
		if strings.EqualFold(k, "Authorization") {
			raw = v
			break
		}
	}
	if raw == "" {
		return nil
	}
	m := continueBearerRE.FindStringSubmatch(raw)
	if len(m) != 2 {
		return nil
	}
	return &model.MCPAuth{
		Scheme: "bearer",
		Token:  "${env:" + m[1] + "}",
	}
}

// Compile-time interface check.
var _ Importer = (*ContinueImporter)(nil)
