// cline.go — importer for Cline's .clinerules format.
//
// Two layouts are supported:
//
//   - Legacy single file:  <root>/.clinerules
//       The entire body becomes .agents/context.md.
//
//   - Modern directory:    <root>/.clinerules/*.md
//       Prefix-based mapping (per prism v0.5 design lines 221-240):
//         00-*.md                  → concat into .agents/context.md
//         10-scope-<slug>.md       → .agents/<deslug>/context.md
//         20-skill-<name>.md       → .agents/skills/<name>/SKILL.md
//         30-command-<name>.md     → .agents/commands/<name>.md (legacy)
//         (other names)            → appended to .agents/context.md (warn)
//
// v0.8.0 additions (mirrors plugins/cline.go emissions):
//
//   .clinerules/workflows/<n>.md     → model.Command (native slash commands;
//                                      replaces the legacy 30-command-* form)
//   .clinerules/hooks/<event>.json   → model.Hook (matcher groups, each with
//                                      one or more {type, command} entries)
//   .cline/cline_mcp_settings.json   → model.MCPServer (standard mcpServers map)
//
// Files with `paths:` (or `globs:`) frontmatter carry that list into the
// scope's Globs (for scope-shaped files) or the skill's Globs (for skills).

package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
)

const clineTool = "cline"

// ClineImporter reads `.clinerules` (file or directory).
type ClineImporter struct{}

// NewCline returns a ClineImporter.
//
// NOTE on naming: the v0.5 design spec says `func New() *ClineImporter`,
// but the importer package already hosts the sibling agent's four
// importers (cursor, gemini, agents-md, claude) plus the four in this
// batch (cline, continue, windsurf, copilot). Go disallows overloading
// by return type, so eight `func New()` functions in one package will
// not compile. Each importer here uses a tool-specific constructor
// (NewCline / NewContinue / NewWindsurf / NewCopilot), matching the
// convention the sibling already established with NewCursor() etc.
func NewCline() *ClineImporter { return &ClineImporter{} }

// Name reports the stable importer identifier.
func (c *ClineImporter) Name() string { return clineTool }

// Detect returns true when either the legacy file or the modern directory
// exists at root.
func (c *ClineImporter) Detect(root string) bool {
	p := filepath.Join(root, ".clinerules")
	_, err := os.Stat(p)
	return err == nil
}

// Import reads root and returns the canonical Project.
func (c *ClineImporter) Import(root string) (*model.Project, []Warning, error) {
	if !c.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	p := filepath.Join(root, ".clinerules")
	info, err := os.Stat(p)
	if err != nil {
		return nil, nil, fmt.Errorf("cline: stat %s: %w", p, err)
	}

	if !info.IsDir() {
		// Legacy: single file → context.md.
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, fmt.Errorf("cline: read %s: %w", p, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("cline: %s: %w", p, err)
		}
		proj.Context = &model.Document{
			SourcePath:  p,
			Frontmatter: fm,
			Body:        provenanceComment(clineTool, p) + body,
		}
		return proj, warnings, nil
	}

	// Modern: directory of *.md files.
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, nil, fmt.Errorf("cline: read %s: %w", p, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	// Accumulate context.md body across multiple files.
	var contextParts []string
	var firstContextSource string

	scopesByPath := map[string]*model.Scope{}
	skillsByName := map[string]*model.Skill{}
	cmdsByName := map[string]*model.Command{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		full := filepath.Join(p, name)
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("cline: read %s: %w", full, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("cline: %s: %w", full, err)
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		lower := strings.ToLower(base)

		switch {
		case strings.HasPrefix(lower, "00-"):
			if firstContextSource == "" {
				firstContextSource = full
			}
			contextParts = append(contextParts, provenanceComment(clineTool, full)+body)

		case strings.HasPrefix(lower, "10-scope-"):
			slug := base[len("10-scope-"):]
			scopePath := clineDeslugScope(slug)
			if !clineScopeFirstSegExists(root, scopePath) {
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, full),
					Heuristic: fmt.Sprintf(
						"de-slugified scope %q has no matching first-level directory in project root; mapping kept as-is",
						scopePath),
					Severity: "warn",
				})
			}
			scopesByPath[scopePath] = &model.Scope{
				Path:     scopePath,
				Globs:    clineGlobsFromFrontmatter(fm),
				Priority: model.PriorityNormal,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        provenanceComment(clineTool, full) + body,
				},
			}

		case strings.HasPrefix(lower, "20-skill-"):
			skillName := base[len("20-skill-"):]
			desc, _ := stringFromFM(fm, "description")
			trig, _ := stringFromFM(fm, "trigger")
			globs := clineGlobsFromFrontmatter(fm)
			skillsByName[skillName] = &model.Skill{
				Name:        skillName,
				Description: desc,
				Trigger:     trig,
				Globs:       globs,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        provenanceComment(clineTool, full) + body,
				},
			}

		case strings.HasPrefix(lower, "30-command-"):
			cmdName := base[len("30-command-"):]
			desc, _ := stringFromFM(fm, "description")
			cmdsByName[cmdName] = &model.Command{
				Name:        cmdName,
				Description: desc,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        provenanceComment(clineTool, full) + body,
				},
			}

		default:
			warnings = append(warnings, Warning{
				SourcePath: relTo(root, full),
				Heuristic: fmt.Sprintf(
					"file %q has no recognized prefix (00-/10-scope-/20-skill-/30-command-); concatenated into context.md",
					name),
				Severity: "info",
			})
			contextParts = append(contextParts, provenanceComment(clineTool, full)+body)
			if firstContextSource == "" {
				firstContextSource = full
			}
		}
	}

	if len(contextParts) > 0 {
		proj.Context = &model.Document{
			SourcePath: firstContextSource,
			Body:       strings.Join(contextParts, "\n\n"),
		}
	}

	// Materialise the maps into sorted slices for stable output.
	if len(scopesByPath) > 0 {
		paths := make([]string, 0, len(scopesByPath))
		for k := range scopesByPath {
			paths = append(paths, k)
		}
		sort.Strings(paths)
		for _, k := range paths {
			proj.Scopes = append(proj.Scopes, scopesByPath[k])
		}
	}
	if len(skillsByName) > 0 {
		names := make([]string, 0, len(skillsByName))
		for k := range skillsByName {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			proj.Skills = append(proj.Skills, skillsByName[k])
		}
	}
	if len(cmdsByName) > 0 {
		names := make([]string, 0, len(cmdsByName))
		for k := range cmdsByName {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			proj.Commands = append(proj.Commands, cmdsByName[k])
		}
	}

	// .clinerules/workflows/<n>.md — v0.8 native slash commands. Existing
	// 30-command-*.md entries (parsed above) and workflow files coexist; the
	// uniqueName helper guards against name collisions.
	workflowCmds, err := readClineWorkflows(root)
	if err != nil {
		return nil, nil, err
	}
	for _, cmd := range workflowCmds {
		name := cmd.Name
		cmd.Name = uniqueName("cline", name, func(candidate string) bool {
			for _, existing := range proj.Commands {
				if existing.Name == candidate {
					return true
				}
			}
			return false
		})
		proj.Commands = append(proj.Commands, cmd)
	}

	// .clinerules/hooks/<event>.json — v0.8 native hooks (per-event JSON
	// file with matcher groups and command entries).
	hooks, err := readClineHooks(root)
	if err != nil {
		return nil, nil, err
	}
	proj.Hooks = append(proj.Hooks, hooks...)

	// .cline/cline_mcp_settings.json — v0.8 native MCP servers (project-local
	// mirror of the user-canonical ~/.cline/data/settings file).
	mcps, err := readClineMCP(filepath.Join(root, ".cline", "cline_mcp_settings.json"))
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = append(proj.MCP, mcps...)

	return proj, warnings, nil
}

// readClineWorkflows reads .clinerules/workflows/<n>.md into []*model.Command.
// Each workflow file becomes one Command; the filename (sans .md) is the
// slash-command name. Optional frontmatter (`description`) is consumed.
func readClineWorkflows(root string) ([]*model.Command, error) {
	dir := filepath.Join(root, ".clinerules", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cline: read %s: %w", dir, err)
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
			return nil, fmt.Errorf("cline: read %s: %w", full, rerr)
		}
		fm, body, perr := splitFrontmatter(data)
		if perr != nil {
			return nil, fmt.Errorf("cline: %s: %w", full, perr)
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		desc, _ := fm["description"].(string)
		out = append(out, &model.Command{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment(clineTool, full) + body,
			},
		})
	}
	return out, nil
}

// readClineHooks reads .clinerules/hooks/<event>.json files into
// []*model.Hook. The filename (sans .json) is the Hook.Event; each
// matcher group contributes one Hook per command entry. Hook commands
// pointing into __scope-guard__ wrappers are skipped (projection artifacts,
// not source-of-truth hooks).
func readClineHooks(root string) ([]*model.Hook, error) {
	dir := filepath.Join(root, ".clinerules", "hooks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cline: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []*model.Hook
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("cline: read %s: %w", full, rerr)
		}
		if len(data) == 0 {
			continue
		}
		var doc struct {
			Hooks []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if jerr := json.Unmarshal(data, &doc); jerr != nil {
			return nil, fmt.Errorf("cline: parse %s: %w", full, jerr)
		}
		event := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		for _, grp := range doc.Hooks {
			for _, entry := range grp.Hooks {
				if strings.Contains(entry.Command, "__scope-guard__") {
					continue
				}
				out = append(out, &model.Hook{
					Event:      event,
					Matcher:    grp.Matcher,
					ScriptPath: entry.Command,
				})
			}
		}
	}
	return out, nil
}

// readClineMCP reads .cline/cline_mcp_settings.json (standard mcpServers
// map) into []*model.MCPServer. Returns (nil, nil) when the file is absent.
func readClineMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cline: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
	}
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		return nil, fmt.Errorf("cline: parse %s: %w", path, jerr)
	}
	names := make([]string, 0, len(raw.MCPServers))
	for n := range raw.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		s := raw.MCPServers[n]
		out = append(out, &model.MCPServer{
			Name:    n,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
		})
	}
	return out, nil
}

// clineDeslugScope replaces "-" with "/" in a scope slug to recover the
// likely .agents/-relative path. Best-effort: not always invertible.
func clineDeslugScope(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "_unknown"
	}
	return strings.ReplaceAll(slug, "-", "/")
}

// clineScopeFirstSegExists tests whether the first segment of scopePath
// exists as a directory in root. Used to warn on probable
// mis-de-slugifications (e.g. "src/billing" warns when there's no src/).
func clineScopeFirstSegExists(root, scopePath string) bool {
	parts := strings.SplitN(scopePath, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, parts[0]))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// clineGlobsFromFrontmatter pulls a glob-like list from frontmatter,
// preferring modern Cline's `paths:` field and falling back to `globs:`.
func clineGlobsFromFrontmatter(fm map[string]any) []string {
	if fm == nil {
		return nil
	}
	if v, ok := fm["paths"]; ok {
		if out := stringSliceAny(v); len(out) > 0 {
			return out
		}
	}
	if v, ok := fm["globs"]; ok {
		return stringSliceAny(v)
	}
	return nil
}

// stringFromFM is a small helper: pull a string from frontmatter by key.
func stringFromFM(fm map[string]any, key string) (string, bool) {
	if fm == nil {
		return "", false
	}
	if v, ok := fm[key].(string); ok {
		return v, true
	}
	return "", false
}

// Compile-time interface check.
var _ Importer = (*ClineImporter)(nil)
