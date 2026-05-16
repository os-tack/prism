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
				skillName := uniqueName(slugifyName(baseName), skillExists)
				if skillName == "" {
					skillName = "rule"
				}
				proj.Skills = append(proj.Skills, &model.Skill{
					Name:        skillName,
					Description: description,
					Document: &model.Document{
						SourcePath:  full,
						Frontmatter: fm,
						Body:        bodyWithProv,
					},
				})
				continue
			}
			switch classifyGlobs(globs) {
			case globKindExtension:
				skillName := uniqueName(slugifyName(baseName), skillExists)
				if skillName == "" {
					skillName = "rule"
				}
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
				scopePath := inferScopePathFromGlobs(globs)
				if scopePath == "" {
					// No common directory prefix → fall back to a skill.
					warnings = append(warnings, Warning{
						SourcePath: relTo(root, full),
						Heuristic:  "trigger=glob with no common directory prefix; imported as a skill",
						Severity:   "info",
					})
					skillName := uniqueName(slugifyName(baseName), skillExists)
					if skillName == "" {
						skillName = "rule"
					}
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
					continue
				}
				windsurfAddScope(scopeByPath, scopePath, globs, description, bodyWithProv, full)
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
				windsurfAddScope(scopeByPath, scopePath, globs, description, bodyWithProv, full)
			}

		case "model_decision", "model-decision", "modeldecision":
			skillName := slugifyName(description)
			if skillName == "" {
				skillName = slugifyName(baseName)
			}
			if skillName == "" {
				skillName = "rule"
			}
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

		case "manual":
			cmdName := slugifyName(baseName)
			if cmdName == "" {
				cmdName = "command"
			}
			cmdName = uniqueName(cmdName, commandExists)
			proj.Commands = append(proj.Commands, &model.Command{
				Name:        cmdName,
				Description: description,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
			})

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

	return proj, warnings, nil
}

// windsurfAddScope merges a windsurf rule into the scope keyed by
// scopePath. Same merge semantics as cursor.addScope but free-function.
func windsurfAddScope(scopeByPath map[string]*model.Scope, scopePath string, globs []string, description, body, source string) {
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
var _ Importer = (*WindsurfImporter)(nil)
