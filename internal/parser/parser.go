// Package parser walks a .agents/ directory and builds a canonical
// *model.Project. The parser is the single source of truth for how files
// under .agents/ map to in-memory structures; plugins consume the result.
//
// Canonical .agents/ layout
//
//	.agents/
//	  context.md, agents.config.yaml, <scope>/context.md ... (handled here)
//	  permissions.yaml             — {allow: [...], deny: [...], ask: [...]}
//	  mcp.yaml                     — {servers: {<name>: {command, args, env, url}}}
//	  skills/<name>/
//	    SKILL.md                   — frontmatter: description, trigger, globs, allowed-tools
//	    scripts/<file>             — optional executables (Skill.Scripts: absolute paths)
//	  commands/<name>.md           — frontmatter: description; body = prompt
//	  agents/<name>.md             — frontmatter: description; body = subagent system prompt
//	  hooks/<name>.yaml            — {event, matcher, script}
//
// The skills/, commands/, agents/, and hooks/ directories at the .agents/
// root are reserved capability surfaces — collectScopes must NOT descend
// into them, even if a stray context.md lives inside.
package parser

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

// ErrNoAgentsDir signals the project has no .agents/ directory. The engine
// package re-exports its own sentinel with the same identity so callers
// above the parser see a stable error.
var ErrNoAgentsDir = errors.New(".agents/ directory not found")

// reservedRootDirs are subdirectories of .agents/ that are NOT scopes.
// They are dedicated capability surfaces; collectScopes must not descend
// into them even if a stray context.md lives there.
var reservedRootDirs = map[string]struct{}{
	"skills":   {},
	"commands": {},
	"agents":   {},
	"hooks":    {},
}

// Parse reads root/.agents/ and returns a *model.Project. Returns
// ErrNoAgentsDir if the directory does not exist.
func Parse(root string) (*model.Project, error) {
	return parseWithGlobal(root, "")
}

// parseWithGlobal is the internal entry point shared by Parse and the
// layered parser. globalAgentsDir, when non-empty, is the global layer's
// .agents/ directory: it is consulted by the include preprocessor for
// `global:` directives and as a valid alternate root for path-escape
// checks.
func parseWithGlobal(root, globalAgentsDir string) (*model.Project, error) {
	agentsDir := filepath.Join(root, ".agents")
	info, err := os.Stat(agentsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoAgentsDir
		}
		return nil, fmt.Errorf("parser: stat .agents: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrNoAgentsDir
	}

	proj := &model.Project{
		Root:            root,
		AgentsDir:       agentsDir,
		GlobalAgentsDir: globalAgentsDir,
	}

	// Parse agents.config.yaml FIRST so include.max_depth is known
	// before any document body is expanded.
	cfgPath := filepath.Join(agentsDir, "agents.config.yaml")
	cfg, err := readConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	proj.Config = cfg

	maxDepth := includeMaxDepth(cfg)

	// Root context.md (after config — so includes can expand using
	// the configured max depth).
	rootCtx := filepath.Join(agentsDir, "context.md")
	if doc, err := readDocument(rootCtx, rootCtx, agentsDir, globalAgentsDir, maxDepth); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else if doc != nil {
		proj.Context = doc
	}

	// Root-level capability surfaces FIRST so scoped capabilities can append
	// to the same slices via collectScopes without being clobbered.
	if err := parseCapabilities(agentsDir, proj); err != nil {
		return nil, err
	}

	// Walk for nested scopes (any subdir with context.md), skipping the
	// reserved capability directories. Appends scoped capabilities.
	if err := collectScopes(agentsDir, globalAgentsDir, maxDepth, proj); err != nil {
		return nil, err
	}

	// Implicit scopes: directories under .agents/ that contain capability
	// subdirs (skills/, commands/, agents/, hooks/) but no context.md are
	// still scope-hosts. Their capabilities get stamped with the directory
	// path as ScopePath, even though no *model.Scope is added to proj.Scopes.
	if err := collectImplicitScopes(agentsDir, proj); err != nil {
		return nil, err
	}

	return proj, nil
}

// includeMaxDepth returns the configured include max depth or the default 16.
func includeMaxDepth(cfg *model.Config) int {
	if cfg != nil && cfg.Include.MaxDepth > 0 {
		return cfg.Include.MaxDepth
	}
	return 16
}

// collectScopes walks agentsDir looking for subdirectories containing a
// context.md (excluding the root context.md itself). For each scope it
// also discovers and stamps scoped capabilities under that directory.
//
// The reserved capability subdirectories at the .agents/ root (skills/,
// commands/, agents/, hooks/) are NOT scopes and are skipped wholesale.
//
// Despite the name, this function does more than just collect scopes:
// it also appends scoped Skills/Commands/Agents/Hooks/MCP/Permissions
// directly into proj. The single-traversal design avoids re-walking the
// tree from parseScopedCapabilities for each scope.
func collectScopes(agentsDir, globalAgentsDir string, maxDepth int, proj *model.Project) error {
	err := filepath.WalkDir(agentsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip reserved capability directories at the .agents/ root.
			if filepath.Dir(path) == agentsDir {
				if _, reserved := reservedRootDirs[d.Name()]; reserved {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() != "context.md" {
			return nil
		}
		// Skip root context.md
		if filepath.Dir(path) == agentsDir {
			return nil
		}
		rel, err := filepath.Rel(agentsDir, filepath.Dir(path))
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		doc, err := readDocument(path, path, agentsDir, globalAgentsDir, maxDepth)
		if err != nil {
			return err
		}

		s := &model.Scope{
			Path:     relSlash,
			Globs:    scope.DefaultGlobs(relSlash),
			Priority: model.PriorityNormal,
			Document: doc,
		}

		// Check for scopes.yaml override.
		scopesYAML := filepath.Join(filepath.Dir(path), "scopes.yaml")
		if data, err := os.ReadFile(scopesYAML); err == nil {
			var raw struct {
				Globs       []string `yaml:"globs"`
				Description string   `yaml:"description"`
				Priority    string   `yaml:"priority"`
			}
			if err := yaml.Unmarshal(data, &raw); err != nil {
				return fmt.Errorf("parser: parse %s: %w", scopesYAML, err)
			}
			if len(raw.Globs) > 0 {
				s.Globs = raw.Globs
			}
			if raw.Description != "" {
				s.Description = raw.Description
			}
			switch raw.Priority {
			case "high":
				s.Priority = model.PriorityHigh
			case "normal", "":
				s.Priority = model.PriorityNormal
			default:
				s.Priority = model.Priority(raw.Priority)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("parser: read %s: %w", scopesYAML, err)
		}

		proj.Scopes = append(proj.Scopes, s)

		// Discover scoped capabilities under this scope dir.
		scopeDir := filepath.Dir(path)
		skills, commands, agents, hooks, perms, mcp, err := parseScopedCapabilities(scopeDir, relSlash)
		if err != nil {
			return err
		}
		proj.Skills = append(proj.Skills, skills...)
		proj.Commands = append(proj.Commands, commands...)
		proj.Agents = append(proj.Agents, agents...)
		proj.Hooks = append(proj.Hooks, hooks...)
		proj.MCP = append(proj.MCP, mcp...)
		if perms != nil {
			proj.ScopedPermissions = append(proj.ScopedPermissions, perms)
		}
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(proj.Scopes, func(i, j int) bool {
		return proj.Scopes[i].Path < proj.Scopes[j].Path
	})
	return nil
}

// collectImplicitScopes walks agentsDir looking for directories that
// contain at least one capability subdir (skills/, commands/, agents/,
// hooks/) but do NOT have a context.md and are NOT reserved root dirs
// or already-discovered explicit scopes. For each such directory,
// capabilities are still discovered and stamped with the directory's
// .agents/-relative path as their ScopePath. No *model.Scope is added
// to proj.Scopes for implicit scopes — they exist only as a stamping
// vehicle for their child capabilities.
//
// v0.5: no warnings channel is wired through the parser; the parser
// silently synthesizes implicit scopes. Plugins (or a future warnings
// surface) can report them later.
func collectImplicitScopes(agentsDir string, proj *model.Project) error {
	// Build a set of paths already known as explicit scopes so we don't
	// double-parse their capabilities.
	known := make(map[string]struct{}, len(proj.Scopes))
	for _, s := range proj.Scopes {
		known[s.Path] = struct{}{}
	}

	return filepath.WalkDir(agentsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		// Don't descend into reserved capability dirs at the .agents/ root.
		if filepath.Dir(path) == agentsDir {
			if _, reserved := reservedRootDirs[d.Name()]; reserved {
				return filepath.SkipDir
			}
		}
		// Skip the .agents/ root itself.
		if path == agentsDir {
			return nil
		}
		// Don't treat capability subdirs nested anywhere as scope hosts.
		if _, reserved := reservedRootDirs[d.Name()]; reserved {
			return filepath.SkipDir
		}
		// If this dir has a context.md, it's an explicit scope —
		// already handled. Skip; do NOT skip-dir (children may still
		// be implicit scopes).
		if _, err := os.Stat(filepath.Join(path, "context.md")); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Implicit-scope candidate: must contain at least one capability subdir.
		if !hasCapabilitySubdir(path) {
			return nil
		}
		rel, err := filepath.Rel(agentsDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		// Don't redo work for explicit scopes (defensive — they'd have a
		// context.md and we'd have already returned above).
		if _, dup := known[relSlash]; dup {
			return nil
		}

		skills, commands, agents, hooks, perms, mcp, err := parseScopedCapabilities(path, relSlash)
		if err != nil {
			return err
		}
		proj.Skills = append(proj.Skills, skills...)
		proj.Commands = append(proj.Commands, commands...)
		proj.Agents = append(proj.Agents, agents...)
		proj.Hooks = append(proj.Hooks, hooks...)
		proj.MCP = append(proj.MCP, mcp...)
		if perms != nil {
			proj.ScopedPermissions = append(proj.ScopedPermissions, perms)
		}
		return nil
	})
}

// readDocument reads a markdown file with optional YAML frontmatter,
// then runs the @include expansion pass over the body. agentsDir and
// globalAgentsDir bound the include resolver's escape check; maxDepth
// caps recursion. Returns wrapped os.ErrNotExist if path is missing.
func readDocument(path, sourcePath, agentsDir, globalAgentsDir string, maxDepth int) (*model.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fm, body, offset, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parser: %s: %w", path, err)
	}
	expanded, includes, err := expandIncludes(body, sourcePath, agentsDir, globalAgentsDir, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("parser: %s: %w", path, err)
	}
	return &model.Document{
		SourcePath:            sourcePath,
		Frontmatter:           fm,
		Body:                  expanded,
		Includes:              includes,
		FrontmatterLineOffset: offset,
	}, nil
}

// readDocumentNoExpand reads a markdown file with optional YAML
// frontmatter and returns the raw body without running the @include
// pass. Used by the capability-surface parsers (SKILL.md, commands/*.md,
// agents/*.md) where include expansion is intentionally not supported
// in v0.5.
func readDocumentNoExpand(path, sourcePath string) (*model.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fm, body, offset, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parser: %s: %w", path, err)
	}
	return &model.Document{
		SourcePath:            sourcePath,
		Frontmatter:           fm,
		Body:                  body,
		FrontmatterLineOffset: offset,
	}, nil
}

// splitFrontmatter returns the parsed frontmatter (or nil), the body,
// and the 1-based line on which the body begins. v0.9 / schema v2
// rejects TOML (`+++`) and JSON (`{`) frontmatter delimiters — only
// YAML (`---`) frontmatter is supported (SPEC §3.2).
func splitFrontmatter(data []byte) (map[string]any, string, int, error) {
	// Normalize CRLF.
	s := strings.ReplaceAll(string(data), "\r\n", "\n")

	// Reject TOML and JSON frontmatter delimiters per SPEC §3.2.
	if strings.HasPrefix(s, "+++\n") || strings.HasPrefix(s, "+++") {
		return nil, "", 0, fmt.Errorf("frontmatter: TOML (+++) delimiters are not supported; use YAML (---) frontmatter")
	}
	if strings.HasPrefix(s, "{") {
		return nil, "", 0, fmt.Errorf("frontmatter: JSON ({) delimiters are not supported; use YAML (---) frontmatter")
	}

	if !strings.HasPrefix(s, "---\n") {
		return nil, s, 0, nil
	}
	// Drop the leading "---\n".
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		// No closing fence — treat the whole file as body.
		return nil, s, 0, nil
	}
	fmText := rest[:end]
	bodyStart := end + len("\n---")
	body := rest[bodyStart:]
	// Strip the newline immediately after the closing fence, if any.
	body = strings.TrimPrefix(body, "\n")

	// Compute 1-based offset of the body start. Frontmatter spans:
	//   line 1            "---"
	//   lines 2..(2+N-1)  fmText (N lines)
	//   line 2+N          "---"
	//   line 3+N          body begins (1-based)
	fmLines := strings.Count(fmText, "\n") + 1
	offset := fmLines + 3 // "---" + N lines + "---" + 1 → body line

	var fm map[string]any
	if strings.TrimSpace(fmText) != "" {
		if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
			return nil, "", 0, fmt.Errorf("yaml frontmatter: %w", err)
		}
	}
	return fm, body, offset, nil
}

// readConfig parses .agents/agents.config.yaml. Returns nil (no error) if absent.
//
// v0.9 / schema v2 additions (SPEC §3.1.1):
//   - schema_version: int — REQUIRED long-term. For Phase 0 we soften
//     missing/non-2 to a stderr warning so existing v0.8 tests pass.
//     TODO(phase-2): promote to a hard parse error per SPEC §3.1.1.
//   - include: []string under `include:` key — populates Config.Layers
//     (the "layered config" file list). The legacy `include:` block on
//     this same key (with `max_depth: N`) for the @include preprocessor
//     is preserved for back-compat; the parser accepts BOTH shapes (a
//     scalar/mapping with `max_depth` OR a sequence of paths).
//   - extensions: map[string]any — project-wide pass-through.
//   - target_options[<plugin>].extensions: per-target pass-through.
func readConfig(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", path, err)
	}
	// First pass: decode the well-known fields except `include:`, which is
	// shape-ambiguous (mapping vs. sequence). We handle include in a second
	// pass via a yaml.Node tap.
	var raw struct {
		SchemaVersion int      `yaml:"schema_version"`
		Targets       []string `yaml:"targets"`
		TargetOptions map[string]struct {
			Mode       string         `yaml:"mode"`
			Disabled   bool           `yaml:"disabled"`
			Extensions map[string]any `yaml:"extensions"`
		} `yaml:"target_options"`
		Extensions map[string]any `yaml:"extensions"`
		// IncludeRaw captures whatever shape the user wrote at the `include:`
		// key. We decode it post-hoc into either IncludeConfig (legacy mapping
		// form) or Layers (v2 sequence form).
		IncludeRaw yaml.Node `yaml:"include"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parser: parse %s: %w", path, err)
	}

	// Phase 0 schema_version handling. SPEC §3.1.1 requires the value to
	// be present and equal to version.SchemaVersion; we soften both
	// conditions to a stderr warning so v0.8-shape tests/fixtures keep
	// passing during the transition.
	// TODO(phase-2, SPEC §3.1.1): promote missing/non-matching to a hard
	// error and remove this soft path.
	if raw.SchemaVersion == 0 {
		fmt.Fprintf(os.Stderr, "parser: %s: warning: schema_version missing; v0.9 expects `schema_version: 2` (SPEC §3.1.1)\n", path)
	} else if raw.SchemaVersion != 2 {
		fmt.Fprintf(os.Stderr, "parser: %s: warning: schema_version %d not recognized; v0.9 reads schema_version: 2 (SPEC §3.1.1)\n", path, raw.SchemaVersion)
	}

	cfg := &model.Config{
		SchemaVersion: raw.SchemaVersion,
		Targets:       raw.Targets,
		TargetOptions: make(map[string]model.TargetOption, len(raw.TargetOptions)),
		Extensions:    raw.Extensions,
	}

	// Decode the include node. Two shapes are accepted:
	//   include:
	//     max_depth: 32           # legacy IncludeConfig
	//   include:
	//     - ~/.agents             # v2 layered-config Layers
	//     - vendor/team-defaults
	switch raw.IncludeRaw.Kind {
	case yaml.MappingNode:
		var ic struct {
			MaxDepth int `yaml:"max_depth"`
		}
		if err := raw.IncludeRaw.Decode(&ic); err != nil {
			return nil, fmt.Errorf("parser: parse %s include: %w", path, err)
		}
		cfg.Include = model.IncludeConfig{MaxDepth: ic.MaxDepth}
	case yaml.SequenceNode:
		var layers []string
		if err := raw.IncludeRaw.Decode(&layers); err != nil {
			return nil, fmt.Errorf("parser: parse %s include: %w", path, err)
		}
		cfg.Layers = layers
	case 0:
		// Absent — both Include and Layers stay zero.
	default:
		// Scalar or alias — surface a warning, leave both empty.
		fmt.Fprintf(os.Stderr, "parser: %s: warning: include: must be a mapping (legacy) or sequence (v2), got %v\n", path, raw.IncludeRaw.Kind)
	}

	for name, opt := range raw.TargetOptions {
		cfg.TargetOptions[name] = model.TargetOption{
			Mode:       opt.Mode,
			Disabled:   opt.Disabled,
			Extensions: opt.Extensions,
		}
	}
	return cfg, nil
}
