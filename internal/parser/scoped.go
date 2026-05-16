package parser

// scoped.go: discovery for capabilities that live under a scope directory
// rather than at the .agents/ root.
//
// A scoped capability lives at `.agents/<scopePath>/<capability>/...`.
// For example, a skill at
//
//	.agents/src/billing/skills/audit-trail/SKILL.md
//
// has ScopePath = "src/billing". The scope itself may be declared
// explicitly by a `<scopeDir>/context.md`, OR it may exist implicitly:
// a directory with capability subdirs but no `context.md` is still
// recognized as a scope-host for the purpose of stamping ScopePath
// onto its capabilities. (See collectImplicitScopes in parser.go.)
//
// The merge layer (layered.go) keys Skills/Commands/Agents/MCP by
// "<ScopePath>/<Name>", so a global capability and a scoped one with
// the same Name coexist without clobbering each other.

import (
	"errors"
	"os"
	"path/filepath"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/scope"
)

// parseScopedCapabilities runs the capability-surface parsers against a
// scope directory and stamps ScopePath on every returned item.
//
// scopeDir is the absolute path of the scope directory (e.g.
// "/repo/.agents/src/billing"); scopePath is the .agents/-relative
// scope identifier (e.g. "src/billing"). Missing subdirs return nil
// slices, mirroring the behavior of the global-layer parsers.
//
// Hook script resolution honors the scope: a relative `script:` value
// in `<scopeDir>/hooks/<name>.yaml` resolves against
// `<scopeDir>/hooks/` (not the global `.agents/hooks/`). This is already
// the contract of parseHooks in parser_capabilities.go — it resolves
// against whatever hooksDir it was handed — so no special-casing is
// needed beyond passing the scope-local hooks dir.
//
// Scoped Skills with no explicit globs inherit scope.DefaultGlobs(scopePath)
// so the projection layer can target the scope's files by default.
//
// Scoped Permissions are returned as a single *Permissions stamped with
// ScopePath; nil means there was no permissions.yaml under the scope.
func parseScopedCapabilities(scopeDir, scopePath string) (
	skills []*model.Skill,
	commands []*model.Command,
	agents []*model.Agent,
	hooks []*model.Hook,
	perms *model.Permissions,
	mcp []*model.MCPServer,
	err error,
) {
	skills, err = parseSkills(filepath.Join(scopeDir, "skills"))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	for _, s := range skills {
		s.ScopePath = scopePath
		if len(s.Globs) == 0 {
			s.Globs = scope.DefaultGlobs(scopePath)
		}
	}

	commands, err = parseCommands(filepath.Join(scopeDir, "commands"))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	for _, c := range commands {
		c.ScopePath = scopePath
	}

	agents, err = parseAgents(filepath.Join(scopeDir, "agents"))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	for _, a := range agents {
		a.ScopePath = scopePath
	}

	hooks, err = parseHooks(filepath.Join(scopeDir, "hooks"))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	for _, h := range hooks {
		h.ScopePath = scopePath
	}

	permsPath := filepath.Join(scopeDir, "permissions.yaml")
	perms, err = parsePermissions(permsPath)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if perms != nil {
		perms.ScopePath = scopePath
	}

	mcp, err = parseMCP(filepath.Join(scopeDir, "mcp.yaml"))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	for _, m := range mcp {
		m.ScopePath = scopePath
	}

	return skills, commands, agents, hooks, perms, mcp, nil
}

// hasCapabilitySubdir reports whether dir contains at least one of the
// capability subdirectory names (skills, commands, agents, hooks) as an
// actual directory. Used by implicit-scope discovery.
func hasCapabilitySubdir(dir string) bool {
	for _, name := range []string{"skills", "commands", "agents", "hooks"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			// Stat error on a sibling we don't own — treat as absent.
			continue
		}
		if info.IsDir() {
			return true
		}
	}
	return false
}
