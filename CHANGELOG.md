# Changelog

All notable changes to **prism** are documented here. Format roughly follows
[Keep a Changelog](https://keepachangelog.com/) and the project uses
[Semantic Versioning](https://semver.org/).

## v0.6.0

### Added
- **Windows path handling**: `Operation.Path` is now consistently
  forward-slashed engine-side, so lockfile keys are portable across
  macOS/Linux/Windows builds. Legacy backslash lockfiles from earlier
  Windows runs are reconciled on read (no spurious "stale" deletes).
- **Symlink fallback on Windows**: when `runtime.GOOS == "windows"`,
  `OpSymlink` ops downgrade to `OpWrite` automatically (the symlink
  target's bytes are read in and written). Windows users without
  developer mode (where `os.Symlink` fails) now get a working content
  copy instead of an apply error.
- **Per-file SHA-256 hashes in `.agents/packages.yaml`**: `agents
  remove` now precisely identifies which package files have been
  manually edited and preserves only those, deleting the unchanged
  ones. Backward-compatible: v0.5-installed packages (no per-file
  hashes) fall back to the aggregate-SHA all-or-nothing semantics
  with a clear warning.
- **Importer round-trip tests**: new `internal/engine/roundtrip_test.go`
  covers cursor / claude / continue / copilot end-to-end (stage source
  tree → `engine.Init` → `engine.Compile` → assert original content
  survives the canonical model). Gemini / cline / windsurf / agents-md
  round-trips are TODO for v0.7.
- **`--no-hook-wrappers` CLI flag**: the v0.5 CHANGELOG promised it;
  v0.6 wires it through. Defaults to wrappers ON; pass `--no-hook-wrappers`
  to skip generating Claude `__scope-guard__` wrappers and fall back to
  raw source-script paths.

### Changed
- `importer.Registry.Register` returns an error on duplicate instead
  of panicking. `plugin.Registry.Register` still panics (deferred to
  v0.7 because of multiple call sites).
- `agents remove` returns the drift error rather than calling
  `os.Exit(1)` inside cobra `RunE`; cobra's exit pipeline handles the
  non-zero exit, so deferred cleanup runs.
- AGENTS.md importer `Detect` is now O(1): it stats `<root>/AGENTS.md`
  and `<root>/.github/AGENTS.md` only, never walks the tree. `--from
  auto` against monorepos no longer pays an O(tree) cost per importer.
  Nested-only `<some/dir>/AGENTS.md` projects need explicit
  `--from agents-md`.
- `parseGitSource` rejects non-`github.com` URLs with a clear error
  (`registry: only github.com URLs supported in v0.6`). v0.5 silently
  mis-parsed gitlab group/subgroup paths.
- `looksLikeRef` requires at least one a-f hex letter for 7-39 char
  refs, so a numeric branch like `1234567` is no longer mis-classified
  as a SHA. Full 40-char all-numeric strings still treat as SHA (rare,
  indistinguishable).

### Fixed
- **`--no-hook-wrappers` propagation through cobra** (caught by v0.6
  review): plugin registration was running before cobra parsed flags,
  so the field was always false at runtime. Moved to lazy
  registration via `cliState.ensureRegistry()` invoked from
  per-subcommand RunE. Regression test (`TestNoHookWrappersFlag_ThroughExecute`)
  drives the full Execute() path.
- **`agents remove` aggregate SHA staleness on partial drift**:
  when files are preserved (per-file Hash mismatch), the entry now
  zeroes `pkg.SHA` so a future Remove of the narrowed set falls back
  to per-file Hash checks rather than comparing against the original
  install's aggregate.
- **Symlink fallback no longer write-through-existing-symlink** on
  Windows. If `abs` is an existing symlink (e.g. project synced from
  a Unix prism install), the fallback now removes the link before
  the OpWrite re-entry, so `os.WriteFile` doesn't follow it and
  silently overwrite the canonical `.agents/` source.
- `@include` recursive stack append explicitly copies the slice before
  appending, avoiding backing-array aliasing if the slice's cap allowed
  in-place mutation. Latent corruption that only the right depth/order
  would have triggered.
- `Package.Files` is now `[]FileEntry{Path, Hash}` (model change);
  serializer and parser updated.
- `uniqueName` (cursor importer) now logs to stderr on the 1000-cap
  overflow instead of silently returning the base name.
- Removed dead `var _ = errors.New` from cline importer + unused
  `errors` import.

### Test
- Cross-compile to `GOOS=windows GOARCH=amd64` and `GOOS=linux GOARCH=arm64`
  verified locally; release workflow already builds all five targets
  on tag push.

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
