// continue.go — importer for Continue's .continue/ format.
//
// Inputs (per prism v0.5 design lines 242-252):
//
//   .continue/rules/*.md         frontmatter: name, globs, regex,
//                                description, alwaysApply
//   .continue/mcpServers/*.yaml  one server per file
//
// Mapping:
//   - alwaysApply: true AND no globs    → append body to .agents/context.md
//   - regex:                            → warn and drop (not in canonical model)
//   - globs present                     → cursor-style skill-vs-scope heuristic
//                                         (extension globs → skill,
//                                          path globs      → scope,
//                                          mixed           → scope + warning)
//   - no globs, description present     → skill (model-decision style)
//   - one server per YAML               → one entry in proj.MCP

package importer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

		// Regex trigger → warn and drop.
		if strings.TrimSpace(regex) != "" {
			warnings = append(warnings, Warning{
				SourcePath: relTo(root, full),
				Heuristic:  "regex triggers are unsupported in the canonical model; rule dropped",
				Severity:   "warn",
			})
			continue
		}

		bodyWithProv := provenanceComment(continueTool, full) + body

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
			skillName = uniqueName(skillName, skillExists)
			proj.Skills = append(proj.Skills, &model.Skill{
				Name:        skillName,
				Description: description,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
			})

		default:
			classification := classifyGlobs(globs)
			switch classification {
			case globKindExtension:
				skillName := continueDeriveSkillName(fmName, e.Name())
				skillName = uniqueName(skillName, skillExists)
				proj.Skills = append(proj.Skills, &model.Skill{
					Name:        skillName,
					Description: description,
					Globs:       globs,
					Document: &model.Document{
						SourcePath:  full,
						Frontmatter: fm,
						Body:        bodyWithProv,
					},
				})
			case globKindPath:
				continueAddScope(scopeByPath, globs, description, bodyWithProv, full)
			case globKindMixed:
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, full),
					Heuristic: "globs mix path-prefix and extension-only patterns; " +
						"imported as scope (path prefix wins) — split into separate rules for clean import",
					Severity: "warn",
				})
				continueAddScope(scopeByPath, globs, description, bodyWithProv, full)
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
			Name    string            `yaml:"name"`
			Command string            `yaml:"command"`
			Args    []string          `yaml:"args"`
			Env     map[string]string `yaml:"env"`
			URL     string            `yaml:"url"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, nil, fmt.Errorf("continue: parse %s: %w", full, err)
		}
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".yaml"), ".yml")
		}
		proj.MCP = append(proj.MCP, &model.MCPServer{
			Name:    name,
			Command: raw.Command,
			Args:    raw.Args,
			Env:     raw.Env,
			URL:     raw.URL,
		})
	}
	sort.Slice(proj.MCP, func(i, j int) bool { return proj.MCP[i].Name < proj.MCP[j].Name })

	return proj, warnings, nil
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
func continueAddScope(scopeByPath map[string]*model.Scope, globs []string, description, body, source string) {
	scopePath := inferScopePathFromGlobs(globs)
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

// Compile-time interface check.
var _ Importer = (*ContinueImporter)(nil)
