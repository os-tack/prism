# Changelog

All notable changes to **prism** are documented here. Format roughly follows
[Keep a Changelog](https://keepachangelog.com/) and the project uses
[Semantic Versioning](https://semver.org/).

## v0.5.0

### Added
- **Importers**: `agents init --from <tool>` now works for all 8 tools
  — `claude`, `cursor`, `gemini`, `cline`, `continue`, `windsurf`,
  `copilot`, `agents-md`. Comma-separated for multi-source merge
  (`--from cursor,gemini`); `--from auto` detects every tool with marker
  files. Heuristic decisions emit warnings naming the source file and
  the reasoning, so users can audit ambiguous mappings.
- **`@include` directive**: `<!-- include: path -->` in any `.agents/`
  markdown expands at parse time. Supports relative paths (resolved
  against the including file), `global:<path>` for the global layer,
  cycle detection, and a configurable depth cap (`include.max_depth`
  in `agents.config.yaml`, default 16). Lockfile sources and
  `agents which` reflect included files; `agents watch` recompiles on
  include-file change. Plugins downgrade `symlink → write` when a doc
  has includes (since the symlink target would only carry the
  unexpanded source).
- **Skill registry**: `agents add <git-url-or-path>`,
  `agents remove <name>`, `agents list`. Packages are tarballs of
  canonical `.agents/` content + a `package.yaml` manifest. Installs
  tracked in `.agents/packages.yaml`. Remove preserves manually-edited
  files (drift-detected via SHA-256 against install-time hashes).
  v0.5 ships install-from-Git-URL and local-path only; central
  registry, signatures, and updates wait for v0.6+.
- **Scoped capabilities**: skills, commands, agents, hooks, permissions,
  and MCP can live under a scope directory and inherit its path scope.
  `.agents/src/billing/skills/audit-trail/SKILL.md` is automatically
  scoped to `src/billing/`. Plugins that natively support per-path
  scoping (Cursor, Continue, Windsurf, Copilot, Claude for skills)
  project these correctly via their glob/applyTo/trigger frontmatter.
  For Claude hooks (no native path-scoping), prism generates a
  `__scope-guard__` wrapper script that invokes the hidden `prism scope-guard` subcommand, which reads Claude Code's hook JSON from stdin and dispatches to the source script only when `tool_input.file_path` falls under the scope. `CLAUDE_PROJECT_DIR` (set by Claude Code for hooks) converts absolute paths to project-relative for matching.
  Permissions and MCP are degraded to global with a warning.

### Changed
- `lockfile.Entry.Mode` (string holding the OpKind) — already renamed
  to `Kind` in v0.4 cleanup; no further change.
- Parser order: capability surfaces (`skills/`, `commands/`, etc.) at
  the `.agents/` root are parsed BEFORE nested scopes, so scoped
  capabilities can append to the same slices without being clobbered.
- Layered merge key for Skills/Commands/Agents/MCP now includes the
  ScopePath (`<scopePath>/<name>`) so a project-scope capability and
  a global capability with the same name can coexist.
- Global-layer scoped capabilities (`~/.agents/src/billing/...`) load
  broadly into the merged project; their native globs ensure plugins
  only fire them for matching files.

### Fixed
- `parser.Parse` no longer clobbers scoped slices with the global
  capability-surface assignments (the v0.4 ordering had `proj.Skills =
  skills` running after scope discovery; v0.5 reorders so the
  capability walk runs first and the scope walk appends).

### Removed
- Nothing.

## v0.4.0

### Added
- Initial public release.
- 8 plugins: claude, cursor, gemini, cline, continue, windsurf, copilot,
  agents-md.
- Layered config: project `.agents/` + global `~/.agents/`, with project
  winning on collisions.
- `OpMerge` preserves user keys in `settings.json` / `.mcp.json` across
  recompiles.
- Manual-edit detection via lockfile SHA-256 hash before clobber.
- `agents check` for CI drift detection (exit 1 on any diff).
- 171 tests across 9 packages.

### Commands
```
agents init [--from claude]
agents compile [--target X] [--dry-run] [--quiet]
agents check
agents diff
agents watch
agents which <projected-file>
agents capabilities [--target X]

Global flags: --root, --global, --no-global
```

### Notes
- Source-tag prefix (`project:` is implicit / no prefix; `global:` for
  global-layer content) used in lockfile `sources` and `agents which`.
- Capability matrix:
```
PLUGIN     CONTEXT  PATHS   SEMANTIC  SKILLS  CMDS    AGENTS  HOOKS   PERMS   MCP
agents-md  native   degr.   degr.     degr.   degr.   degr.   degr.   degr.   degr.
claude     native   native  degr.     native  native  native  native  native  native
cline      native   degr.   degr.     degr.   degr.   ----    ----    ----    ----
continue   native   native  native    degr.   degr.   ----    ----    ----    native
copilot    native   native  degr.     degr.   native  ----    ----    ----    ----
cursor     native   native  native    degr.   degr.   ----    ----    ----    native
gemini     native   native  degr.     ----    ----    ----    ----    ----    native
windsurf   native   native  native    degr.   degr.   ----    ----    ----    ----
```
