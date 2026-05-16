package parser

import (
	"errors"
	"os"

	"agents.dev/agents/internal/model"
)

// ParseLayered parses a global .agents/ root (typically ~/.agents/) and a
// project .agents/ root, then merges the two into a single Project. The
// project's content takes precedence on collisions (by Path for Scopes; by
// Name for Skills, Commands, Agents, MCP servers; Project.Context wins if
// non-nil). Hooks are concatenated (project first). Permissions are merged
// field-by-field as deduped unions.
//
// If globalRoot is empty, behaves identically to Parse(projectRoot).
// If globalRoot is set but has no .agents/ subdirectory, the global layer
// is silently skipped (a typical user has no ~/.agents/ until they make one).
// If the project root has no .agents/, ErrNoAgentsDir is returned.
//
// The returned Project's Root and AgentsDir refer to the PROJECT (projections
// land in the project tree). Document.SourcePath remains the absolute path
// of each source file, so global-source docs have paths under globalRoot
// and project-source docs have paths under projectRoot.
func ParseLayered(globalRoot, projectRoot string) (*model.Project, error) {
	proj, err := Parse(projectRoot)
	if err != nil {
		return nil, err
	}

	if globalRoot == "" {
		return proj, nil
	}

	// Skip global layer silently if it has no .agents/ subdir.
	global, err := Parse(globalRoot)
	if err != nil {
		if errors.Is(err, ErrNoAgentsDir) || errors.Is(err, os.ErrNotExist) {
			return proj, nil
		}
		return nil, err
	}

	return mergeProjects(proj, global), nil
}

// mergeProjects returns a new Project with project taking precedence over
// global. The returned Project's Root/AgentsDir/Config come from project;
// the global layer contributes only content (Context fallback, scopes,
// skills, commands, agents, hooks, permissions, MCP).
func mergeProjects(project, global *model.Project) *model.Project {
	out := &model.Project{
		Root:            project.Root,
		AgentsDir:       project.AgentsDir,
		GlobalAgentsDir: global.AgentsDir,
		Config:          project.Config,
	}

	// Context: project wins if non-nil; else fall back to global.
	if project.Context != nil {
		out.Context = project.Context
	} else {
		out.Context = global.Context
	}

	// Scopes: merge by Path, project wins.
	out.Scopes = mergeByKey(global.Scopes, project.Scopes, func(s *model.Scope) string {
		return s.Path
	})

	// Skills/Commands/Agents/MCP: merge by Name.
	out.Skills = mergeByKey(global.Skills, project.Skills, func(s *model.Skill) string {
		return s.Name
	})
	out.Commands = mergeByKey(global.Commands, project.Commands, func(c *model.Command) string {
		return c.Name
	})
	out.Agents = mergeByKey(global.Agents, project.Agents, func(a *model.Agent) string {
		return a.Name
	})
	out.MCP = mergeByKey(global.MCP, project.MCP, func(m *model.MCPServer) string {
		return m.Name
	})

	// Hooks: project first, then global. Hooks lack a stable key; we don't
	// dedupe. Users wanting a single source of truth should keep hooks in
	// one layer.
	out.Hooks = append([]*model.Hook{}, project.Hooks...)
	out.Hooks = append(out.Hooks, global.Hooks...)

	// Permissions: union of slices, deduped.
	out.Permissions = mergePermissions(project.Permissions, global.Permissions)

	return out
}

// mergeByKey merges global + project slices, with project entries
// overriding global ones that share a key. Preserves the project's
// existing order for project entries; appends global-only entries.
func mergeByKey[T any](globalItems, projectItems []T, key func(T) string) []T {
	if len(globalItems) == 0 {
		return projectItems
	}
	projectKeys := make(map[string]struct{}, len(projectItems))
	for _, item := range projectItems {
		projectKeys[key(item)] = struct{}{}
	}
	out := make([]T, 0, len(projectItems)+len(globalItems))
	out = append(out, projectItems...)
	for _, item := range globalItems {
		if _, taken := projectKeys[key(item)]; taken {
			continue
		}
		out = append(out, item)
	}
	return out
}

// mergePermissions returns the union of allow/deny/ask slices, deduped,
// or nil if both layers have nothing.
func mergePermissions(project, global *model.Permissions) *model.Permissions {
	if project == nil && global == nil {
		return nil
	}
	out := &model.Permissions{}
	if project != nil {
		out.Allow = append(out.Allow, project.Allow...)
		out.Deny = append(out.Deny, project.Deny...)
		out.Ask = append(out.Ask, project.Ask...)
	}
	if global != nil {
		out.Allow = dedupeAppend(out.Allow, global.Allow)
		out.Deny = dedupeAppend(out.Deny, global.Deny)
		out.Ask = dedupeAppend(out.Ask, global.Ask)
	}
	if len(out.Allow) == 0 && len(out.Deny) == 0 && len(out.Ask) == 0 {
		return nil
	}
	return out
}

func dedupeAppend(base, extra []string) []string {
	seen := make(map[string]struct{}, len(base))
	for _, s := range base {
		seen[s] = struct{}{}
	}
	for _, s := range extra {
		if _, dup := seen[s]; dup {
			continue
		}
		base = append(base, s)
		seen[s] = struct{}{}
	}
	return base
}
