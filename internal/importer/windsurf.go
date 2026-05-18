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
				skillName := uniqueName("windsurf", slugifyName(baseName), skillExists)
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
					skillName := uniqueName("windsurf", slugifyName(baseName), skillExists)
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
			skillName = uniqueName("windsurf", skillName, skillExists)
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
			cmdName = uniqueName("windsurf", cmdName, commandExists)
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
			skillName = uniqueName("windsurf", skillName, skillExists)
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
			out = append(out, &model.Hook{
				Event:      ev,
				ScriptPath: cmd,
			})
		}
	}
	return out, nil
}

// readWindsurfMCP parses .windsurf/mcp_config.json (standard {"mcpServers":
// {...}} schema) into []*model.MCPServer.
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
	var raw struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
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
