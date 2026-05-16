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
		Root:      root,
		AgentsDir: agentsDir,
	}

	// Root context.md
	rootCtx := filepath.Join(agentsDir, "context.md")
	if doc, err := readDocument(rootCtx, rootCtx); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else if doc != nil {
		proj.Context = doc
	}

	// Walk for nested scopes (any subdir with context.md), skipping the
	// reserved capability directories.
	scopes, err := collectScopes(agentsDir)
	if err != nil {
		return nil, err
	}
	proj.Scopes = scopes

	// Parse agents.config.yaml if present.
	cfgPath := filepath.Join(agentsDir, "agents.config.yaml")
	cfg, err := readConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	proj.Config = cfg

	// Capability surfaces (skills, commands, agents, hooks, permissions, MCP).
	// See parser_capabilities.go.
	if err := parseCapabilities(agentsDir, proj); err != nil {
		return nil, err
	}

	return proj, nil
}

// collectScopes walks agentsDir looking for subdirectories containing a
// context.md (excluding the root context.md itself). The reserved
// capability subdirectories at the .agents/ root (skills/, commands/,
// agents/, hooks/) are NOT scopes and are skipped wholesale.
func collectScopes(agentsDir string) ([]*model.Scope, error) {
	var scopes []*model.Scope

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

		doc, err := readDocument(path, path)
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

		scopes = append(scopes, s)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(scopes, func(i, j int) bool {
		return scopes[i].Path < scopes[j].Path
	})
	return scopes, nil
}

// readDocument reads a markdown file with optional YAML frontmatter.
// Returns the wrapped os.ErrNotExist if the file doesn't exist.
func readDocument(path, sourcePath string) (*model.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parser: %s: %w", path, err)
	}
	return &model.Document{
		SourcePath:  sourcePath,
		Frontmatter: fm,
		Body:        body,
	}, nil
}

// splitFrontmatter returns the parsed frontmatter (or nil) and the body.
func splitFrontmatter(data []byte) (map[string]any, string, error) {
	// Normalize CRLF.
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, s, nil
	}
	// Drop the leading "---\n".
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		// No closing fence — treat the whole file as body.
		return nil, s, nil
	}
	fmText := rest[:end]
	bodyStart := end + len("\n---")
	body := rest[bodyStart:]
	// Strip the newline immediately after the closing fence, if any.
	body = strings.TrimPrefix(body, "\n")

	var fm map[string]any
	if strings.TrimSpace(fmText) != "" {
		if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
			return nil, "", fmt.Errorf("yaml frontmatter: %w", err)
		}
	}
	return fm, body, nil
}

// readConfig parses .agents/agents.config.yaml. Returns nil (no error) if absent.
func readConfig(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parser: read %s: %w", path, err)
	}
	var raw struct {
		Targets       []string `yaml:"targets"`
		TargetOptions map[string]struct {
			Mode     string `yaml:"mode"`
			Disabled bool   `yaml:"disabled"`
		} `yaml:"target_options"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parser: parse %s: %w", path, err)
	}
	cfg := &model.Config{
		Targets:       raw.Targets,
		TargetOptions: make(map[string]model.TargetOption, len(raw.TargetOptions)),
	}
	for name, opt := range raw.TargetOptions {
		cfg.TargetOptions[name] = model.TargetOption{
			Mode:     opt.Mode,
			Disabled: opt.Disabled,
		}
	}
	return cfg, nil
}
