package engine

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/importer"
	"agents.dev/agents/internal/model"
)

const defaultContext = "# Project context\n\nDescribe your project here.\n"

// initProject scaffolds .agents/. With importFrom == "", writes default
// content. With importFrom set to a known importer name (or comma-separated
// list), runs each named importer in order, merging their *model.Project
// outputs (first wins on collisions), and serializes the merged result.
//
// importFrom == "auto" runs every importer whose Detect returns true.
func initProject(opts Options, importFrom string) error {
	if opts.Root == "" {
		return fmt.Errorf("engine: Options.Root is required")
	}
	if opts.Registry == nil {
		return fmt.Errorf("engine: Options.Registry is required")
	}

	agentsDir := filepath.Join(opts.Root, ".agents")
	if _, err := os.Stat(agentsDir); err == nil {
		return fmt.Errorf("engine: .agents/ already exists at %s", agentsDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("engine: stat .agents/: %w", err)
	}

	var (
		project  *model.Project
		warnings []importer.Warning
		err      error
	)

	if importFrom == "" {
		project = defaultProject()
	} else {
		project, warnings, err = runImporters(opts.Root, importFrom)
		if err != nil {
			return err
		}
		if project.Context == nil {
			// Importer produced no root context: scaffold a default so the
			// project tree is still useful.
			project.Context = &model.Document{Body: defaultContext}
		}
	}

	if opts.Interactive && importFrom != "" {
		if !stdinIsTTY() {
			return ErrInteractiveNoTTY
		}
		in := bufio.NewReader(os.Stdin)
		filtered, ferr := filterProjectInteractively(project, in, os.Stdout)
		if ferr != nil {
			if errors.Is(ferr, ErrInteractiveDeclinedAll) {
				fmt.Fprintln(os.Stdout, "Interactive selection declined all items; nothing to write.")
				return nil
			}
			return ferr
		}
		project = filtered
	}

	created, err := serializeProject(opts.Root, project)
	if err != nil {
		return err
	}

	// 3. agents.config.yaml with autodetected targets.
	var targets []string
	for _, p := range opts.Registry.All() {
		if p.Detect(opts.Root) {
			targets = append(targets, p.Name())
		}
	}
	sort.Strings(targets)
	cfg := buildConfigYAML(targets)
	cfgFull := filepath.Join(agentsDir, "agents.config.yaml")
	if err := os.WriteFile(cfgFull, []byte(cfg), 0o644); err != nil {
		return err
	}
	created = append(created, filepath.Join(".agents", "agents.config.yaml"))

	if !opts.Quiet {
		fmt.Println("Initialized .agents/:")
		sort.Strings(created)
		for _, c := range created {
			fmt.Printf("  created %s\n", c)
		}
		for _, w := range warnings {
			fmt.Printf("  ⚠ [%s] %s: %s\n", w.Severity, w.SourcePath, w.Heuristic)
		}
	}
	return nil
}

func defaultProject() *model.Project {
	return &model.Project{
		Context: &model.Document{Body: defaultContext},
	}
}

// runImporters resolves importFrom (comma-separated list or "auto") into
// a merged Project. Single-name calls run that one importer; multi-name
// runs them in order with first-wins-on-collision semantics.
func runImporters(root, importFrom string) (*model.Project, []importer.Warning, error) {
	reg := defaultImporterRegistry()

	var names []string
	if importFrom == "auto" {
		for _, imp := range reg.All() {
			if imp.Detect(root) {
				names = append(names, imp.Name())
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, nil, fmt.Errorf("engine: --from auto found no recognized tool config in %s", root)
		}
	} else {
		for _, n := range strings.Split(importFrom, ",") {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if reg.Get(n) == nil {
				return nil, nil, fmt.Errorf("engine: unknown importer %q (known: %s)", n, strings.Join(reg.Names(), ", "))
			}
			names = append(names, n)
		}
	}

	var (
		merged   *model.Project
		warnings []importer.Warning
	)
	for _, name := range names {
		imp := reg.Get(name)
		proj, ws, err := imp.Import(root)
		if err != nil {
			if errors.Is(err, importer.ErrSourceNotPresent) {
				continue
			}
			return nil, warnings, fmt.Errorf("engine: importer %s: %w", name, err)
		}
		warnings = append(warnings, ws...)
		if merged == nil {
			merged = proj
		} else {
			merged = mergeImported(merged, proj)
		}
	}
	if merged == nil {
		return nil, warnings, fmt.Errorf("engine: no importers produced content for %s", root)
	}
	return merged, warnings, nil
}

// mergeImported merges b into a with first-wins semantics: a's content
// wins on collision (by Scope.Path, Skill.Name, Command.Name, etc.).
// Used by `agents init --from cursor,gemini` to combine multiple sources.
func mergeImported(a, b *model.Project) *model.Project {
	if a.Context == nil && b.Context != nil {
		a.Context = b.Context
	}
	// Scopes
	seenScope := make(map[string]struct{}, len(a.Scopes))
	for _, s := range a.Scopes {
		seenScope[s.Path] = struct{}{}
	}
	for _, s := range b.Scopes {
		if _, dup := seenScope[s.Path]; dup {
			continue
		}
		a.Scopes = append(a.Scopes, s)
	}
	// Skills/Commands/Agents by name
	seenSkill := make(map[string]struct{})
	for _, s := range a.Skills {
		seenSkill[s.ScopePath+"/"+s.Name] = struct{}{}
	}
	for _, s := range b.Skills {
		if _, dup := seenSkill[s.ScopePath+"/"+s.Name]; dup {
			continue
		}
		a.Skills = append(a.Skills, s)
	}
	seenCmd := make(map[string]struct{})
	for _, c := range a.Commands {
		seenCmd[c.ScopePath+"/"+c.Name] = struct{}{}
	}
	for _, c := range b.Commands {
		if _, dup := seenCmd[c.ScopePath+"/"+c.Name]; dup {
			continue
		}
		a.Commands = append(a.Commands, c)
	}
	seenAgent := make(map[string]struct{})
	for _, ag := range a.Agents {
		seenAgent[ag.ScopePath+"/"+ag.Name] = struct{}{}
	}
	for _, ag := range b.Agents {
		if _, dup := seenAgent[ag.ScopePath+"/"+ag.Name]; dup {
			continue
		}
		a.Agents = append(a.Agents, ag)
	}
	// Hooks: concatenate (no stable key)
	a.Hooks = append(a.Hooks, b.Hooks...)
	// Permissions: union
	if b.Permissions != nil {
		if a.Permissions == nil {
			a.Permissions = b.Permissions
		} else {
			a.Permissions.Allow = unionStrings(a.Permissions.Allow, b.Permissions.Allow)
			a.Permissions.Deny = unionStrings(a.Permissions.Deny, b.Permissions.Deny)
			a.Permissions.Ask = unionStrings(a.Permissions.Ask, b.Permissions.Ask)
		}
	}
	// MCP by name
	seenMCP := make(map[string]struct{})
	for _, m := range a.MCP {
		seenMCP[m.ScopePath+"/"+m.Name] = struct{}{}
	}
	for _, m := range b.MCP {
		if _, dup := seenMCP[m.ScopePath+"/"+m.Name]; dup {
			continue
		}
		a.MCP = append(a.MCP, m)
	}
	return a
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		if _, dup := seen[s]; dup {
			continue
		}
		a = append(a, s)
		seen[s] = struct{}{}
	}
	return a
}

// defaultImporterRegistry is wired into engine.Init for v0.5. A future
// version may push this into Options so callers can override / mock.
func defaultImporterRegistry() *importer.Registry {
	r := importer.NewRegistry()
	// Registrations are static and known-good at compile time; the only
	// failure mode is a duplicate Name(), which would be a programming
	// error caught by tests. Discard the error.
	_ = r.Register(importer.NewClaude())
	_ = r.Register(importer.NewCursor())
	_ = r.Register(importer.NewGemini())
	_ = r.Register(importer.NewCline())
	_ = r.Register(importer.NewContinue())
	_ = r.Register(importer.NewWindsurf())
	_ = r.Register(importer.NewCopilot())
	_ = r.Register(importer.NewAgentsMD())
	return r
}

func buildConfigYAML(targets []string) string {
	var b strings.Builder
	b.WriteString("# .agents/agents.config.yaml — generated by `agents init`\n")
	b.WriteString("schema_version: 2\n")
	b.WriteString("targets:\n")
	if len(targets) == 0 {
		b.WriteString("  []\n")
	} else {
		for _, t := range targets {
			b.WriteString("  - ")
			b.WriteString(t)
			b.WriteString("\n")
		}
	}
	b.WriteString("target_options: {}\n")
	return b.String()
}
