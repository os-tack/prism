package parser

// parser_capabilities.go: parsing for the dedicated capability surfaces
// at the .agents/ root — skills/, commands/, agents/, hooks/,
// permissions.yaml, and mcp.yaml.
//
// All capability surfaces share these contracts:
//   - Missing directories/files are NOT errors; they produce empty slices
//     or nil pointers.
//   - Malformed YAML IS an error, wrapped with the offending file path.
//   - Slices are returned in lexicographic order by Name for stable
//     downstream output.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/registry"

	"gopkg.in/yaml.v3"
)

// parseCapabilities populates the capability-surface fields on proj
// (Skills, Commands, Agents, Hooks, Permissions, MCP).
func parseCapabilities(agentsDir string, proj *model.Project) error {
	skills, err := parseSkills(filepath.Join(agentsDir, "skills"))
	if err != nil {
		return err
	}
	proj.Skills = skills

	commands, err := parseCommands(filepath.Join(agentsDir, "commands"))
	if err != nil {
		return err
	}
	proj.Commands = commands

	agents, err := parseAgents(filepath.Join(agentsDir, "agents"))
	if err != nil {
		return err
	}
	proj.Agents = agents

	hooks, err := parseHooks(filepath.Join(agentsDir, "hooks"))
	if err != nil {
		return err
	}
	proj.Hooks = hooks

	perms, err := parsePermissions(filepath.Join(agentsDir, "permissions.yaml"))
	if err != nil {
		return err
	}
	proj.Permissions = perms

	mcp, err := parseMCP(filepath.Join(agentsDir, "mcp.yaml"))
	if err != nil {
		return err
	}
	proj.MCP = mcp

	// Load .agents/packages.yaml bookkeeping (see internal/registry).
	// projectRoot is the parent of agentsDir.
	projectRoot := filepath.Dir(agentsDir)
	packages, err := registry.Load(projectRoot)
	if err != nil {
		return err
	}
	proj.Packages = packages

	return nil
}

// parseSkills scans .agents/skills/<name>/SKILL.md.
func parseSkills(skillsDir string) ([]*model.Skill, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", skillsDir, err)
	}
	var skills []*model.Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(skillsDir, e.Name())
		skillMD := filepath.Join(skillDir, "SKILL.md")
		doc, err := readDocumentNoExpand(skillMD, skillMD)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}

		s := &model.Skill{
			Name:     e.Name(),
			Document: doc,
		}
		if doc.Frontmatter != nil {
			if v, ok := doc.Frontmatter["description"].(string); ok {
				s.Description = v
			}
			if v, ok := doc.Frontmatter["trigger"].(string); ok {
				s.Trigger = v
			}
			s.Globs = stringSliceFromFrontmatter(doc.Frontmatter, "globs")
			// v2 additive populations (SPEC §4.2.2). Trigger and Globs above
			// are the v0.8 top-level fields; Activation mirrors the same data
			// so v2 readers see it under sk.Activation.{Modes,Globs}.
			if v, ok := doc.Frontmatter["extensions"].(map[string]any); ok {
				s.Extensions = v
			}
			s.Activation.Globs = s.Globs
			if s.Trigger == "glob" || len(s.Globs) > 0 {
				s.Activation.Modes = []model.SkillActivationMode{model.SkillActivationGlob}
			}
		}

		scripts, err := collectScripts(filepath.Join(skillDir, "scripts"))
		if err != nil {
			return nil, err
		}
		s.Scripts = scripts

		skills = append(skills, s)
	}

	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// collectScripts returns sorted absolute paths to every regular file
// under scriptsDir (recursive). Missing directory → nil, no error.
func collectScripts(scriptsDir string) ([]string, error) {
	info, err := os.Stat(scriptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: stat %s: %w", scriptsDir, err)
	}
	if !info.IsDir() {
		return nil, nil
	}
	var scripts []string
	err = filepath.WalkDir(scriptsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// Regular files only — skip symlinks / sockets / devices.
		if d.Type()&os.ModeType != 0 {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		scripts = append(scripts, abs)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("parser: walk %s: %w", scriptsDir, err)
	}
	sort.Strings(scripts)
	return scripts, nil
}

// parseCommands scans .agents/commands/<name>.md.
func parseCommands(commandsDir string) ([]*model.Command, error) {
	entries, err := os.ReadDir(commandsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", commandsDir, err)
	}
	var commands []*model.Command
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(commandsDir, e.Name())
		doc, err := readDocumentNoExpand(path, path)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		c := &model.Command{Name: name, Document: doc}
		if doc.Frontmatter != nil {
			if v, ok := doc.Frontmatter["description"].(string); ok {
				c.Description = v
			}
			// v2 additive populations (SPEC §4.3.2).
			if v, ok := doc.Frontmatter["extensions"].(map[string]any); ok {
				c.Extensions = v
			}
		}
		commands = append(commands, c)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands, nil
}

// parseAgents scans .agents/agents/<name>.md.
func parseAgents(agentsSubdir string) ([]*model.Agent, error) {
	entries, err := os.ReadDir(agentsSubdir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", agentsSubdir, err)
	}
	var agents []*model.Agent
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(agentsSubdir, e.Name())
		doc, err := readDocumentNoExpand(path, path)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		a := &model.Agent{Name: name, Document: doc}
		if doc.Frontmatter != nil {
			if v, ok := doc.Frontmatter["description"].(string); ok {
				a.Description = v
			}
			// v2 additive populations (SPEC §4.1.2). SystemPrompt mirrors
			// the document body so v2 readers don't have to dig through
			// Document.Body.
			if v, ok := doc.Frontmatter["extensions"].(map[string]any); ok {
				a.Extensions = v
			}
		}
		if doc != nil {
			a.SystemPrompt = doc.Body
		}
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, nil
}

// parseHooks scans .agents/hooks/<name>.yaml.
func parseHooks(hooksDir string) ([]*model.Hook, error) {
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", hooksDir, err)
	}
	type hookYAML struct {
		Event   string `yaml:"event"`
		Matcher string `yaml:"matcher"`
		Script  string `yaml:"script"`
	}
	type named struct {
		name string
		hook *model.Hook
	}
	var named_hooks []named
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !(strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml")) {
			continue
		}
		path := filepath.Join(hooksDir, n)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("parser: read %s: %w", path, err)
		}
		var raw hookYAML
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parser: parse %s: %w", path, err)
		}
		scriptPath := raw.Script
		if scriptPath != "" && !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(hooksDir, scriptPath)
			abs, err := filepath.Abs(scriptPath)
			if err != nil {
				return nil, fmt.Errorf("parser: resolve script %s: %w", path, err)
			}
			scriptPath = abs
		}
		baseName := strings.TrimSuffix(strings.TrimSuffix(n, ".yaml"), ".yml")
		h := &model.Hook{
			Event:      raw.Event,
			Matcher:    raw.Matcher,
			ScriptPath: scriptPath,
		}
		// v2 additive populations (SPEC §4.4.2). EventCanonical mirrors
		// Event as a typed enum; MatcherV2 mirrors Matcher as the
		// {Kind, Patterns} struct so v2 readers and v0.8 readers see the
		// same hook data through different access paths.
		h.EventCanonical = model.HookEvent(raw.Event)
		if raw.Matcher != "" {
			h.MatcherV2 = model.HookMatcher{
				Kind:     "exact",
				Patterns: []string{raw.Matcher},
			}
		}
		named_hooks = append(named_hooks, named{
			name: baseName,
			hook: h,
		})
	}
	sort.Slice(named_hooks, func(i, j int) bool { return named_hooks[i].name < named_hooks[j].name })
	out := make([]*model.Hook, 0, len(named_hooks))
	for _, nh := range named_hooks {
		out = append(out, nh.hook)
	}
	return out, nil
}

// parsePermissions parses .agents/permissions.yaml. Missing file → nil.
func parsePermissions(path string) (*model.Permissions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", path, err)
	}
	var raw struct {
		Allow      []string       `yaml:"allow"`
		Deny       []string       `yaml:"deny"`
		Ask        []string       `yaml:"ask"`
		Extensions map[string]any `yaml:"extensions"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parser: parse %s: %w", path, err)
	}
	return &model.Permissions{
		Allow:      raw.Allow,
		Deny:       raw.Deny,
		Ask:        raw.Ask,
		Extensions: raw.Extensions,
	}, nil
}

// parseMCP parses .agents/mcp.yaml. Missing file → nil slice.
func parseMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", path, err)
	}
	var raw struct {
		Servers map[string]struct {
			Command string            `yaml:"command"`
			Args    []string          `yaml:"args"`
			Env     map[string]string `yaml:"env"`
			URL     string            `yaml:"url"`
		} `yaml:"servers"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parser: parse %s: %w", path, err)
	}
	names := make([]string, 0, len(raw.Servers))
	for n := range raw.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		s := raw.Servers[n]
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

// stringSliceFromFrontmatter coerces a frontmatter value to []string.
// YAML decodes scalar arrays into []any, so we accept both shapes.
func stringSliceFromFrontmatter(fm map[string]any, key string) []string {
	v, ok := fm[key]
	if !ok {
		return nil
	}
	switch typed := v.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
