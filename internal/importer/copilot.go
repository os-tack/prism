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
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/scope"
)

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
			proj.Skills = append(proj.Skills, &model.Skill{
				Name: skillName,
				Document: &model.Document{
					SourcePath:  full,
					Frontmatter: fm,
					Body:        bodyWithProv,
				},
			})
		default:
			switch classifyGlobs(applyTo) {
			case globKindExtension:
				skillName := uniqueName("copilot", slugifyName(baseName), skillExists)
				if skillName == "" {
					skillName = "instructions"
				}
				proj.Skills = append(proj.Skills, &model.Skill{
					Name:  skillName,
					Globs: applyTo,
					Document: &model.Document{
						SourcePath:  full,
						Frontmatter: fm,
						Body:        bodyWithProv,
					},
				})
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
					proj.Skills = append(proj.Skills, &model.Skill{
						Name:  skillName,
						Globs: applyTo,
						Document: &model.Document{
							SourcePath:  full,
							Frontmatter: fm,
							Body:        bodyWithProv,
						},
					})
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
		proj.Commands = append(proj.Commands, &model.Command{
			Name:        cmdName,
			Description: description,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment(copilotTool, full) + body,
			},
		})
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

	return proj, warnings, nil
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
		out = append(out, &model.Agent{
			Name:        name,
			Description: desc,
			Document: &model.Document{
				SourcePath:  full,
				Frontmatter: fm,
				Body:        provenanceComment(copilotTool, full) + body,
			},
		})
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
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
	}
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		return nil, fmt.Errorf("copilot: parse %s: %w", path, jerr)
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
