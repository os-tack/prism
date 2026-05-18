# prism

Project a canonical `.agents/` directory into per-AI-tool config files.

Write your project context, skills, commands, subagents, hooks, permissions,
and MCP server config **once**. `prism` compiles them into the right shape
for Claude Code, Cursor, Gemini CLI, GitHub Copilot, Cline, Continue,
Windsurf, and AGENTS.md — preserving native semantics where the tool
supports them and degrading visibly where it doesn't.

```
$ prism compile
✓ AGENTS.md                                       (agents-md, write)
✓ CLAUDE.md → .agents/context.md                  (claude, symlink)
✓ src/billing/CLAUDE.md → ../../.agents/...       (claude, symlink, cascade)
✓ .claude/skills/pdf-editing/SKILL.md             (claude, symlink)
✓ .claude/settings.json                           (claude, merge; preserves user keys)
✓ .claude/hooks/__scope-guard__/api-validate.sh   (claude, write; wrapper)
✓ .mcp.json                                       (claude, merge)
✓ .cursor/rules/_root.mdc                         (cursor, write)
✓ .cursor/rules/src-billing.mdc                   (cursor, write; globs frontmatter)
✓ .cursor/mcp.json                                (cursor, merge)
✓ GEMINI.md → .agents/context.md                  (gemini, symlink)
✓ .gemini/settings.json                           (gemini, merge)
✓ .gemini/hooks/__perms-guard__/policy.json       (gemini, write; perms wrapper)
✓ .clinerules/00-context.md                       (cline, write)
✓ .continue/rules/_root.md                        (continue, write)
✓ .continue/mcpServers/linear.yaml                (continue, write)
✓ .windsurf/rules/_root.md                        (windsurf, write; trigger frontmatter)
✓ .github/copilot-instructions.md                 (copilot, symlink)
✓ .github/instructions/src-billing.instructions.md (copilot, write; applyTo)
✓ .github/prompts/review.prompt.md                (copilot, write)
Compiled: 38 changed, 0 unchanged, 0 removed, 9 warnings
```

## Install

Pre-built binaries for darwin/amd64, darwin/arm64, linux/amd64, linux/arm64,
and windows/amd64 are on the [releases page](https://github.com/os-tack/prism/releases/latest).
SHA-256 sidecars next to each binary.

```
# darwin/arm64 example
curl -L -o prism https://github.com/os-tack/prism/releases/latest/download/prism-v0.8.0-darwin-arm64
chmod +x prism
./prism --help
```

Or build from source:

```
git clone https://github.com/os-tack/prism
cd prism
go build -o prism ./cmd/prism
```

## Quickstart

```
cd your-project
prism init                # scaffold .agents/ + agents.config.yaml
prism compile             # generate the projections
prism check               # verify (use in CI; exit 1 on drift)
prism watch               # rebuild on .agents/ changes
```

Already using one of the supported tools? Import your existing config:

```
prism init --from claude     # also: cursor, gemini, cline, continue, windsurf, copilot, agents-md
prism init --from cursor -i  # interactive: pick which rules to import
```

Browse [`examples/`](examples/) for runnable `.agents/` layouts.

## Canonical layout

```
.agents/
  context.md                  # root context, projected to CLAUDE.md / AGENTS.md / GEMINI.md / ...
  agents.config.yaml          # target enablement + per-target sync mode

  <path>/context.md           # nested scope: src/billing/context.md → src/billing/CLAUDE.md (Claude cascade)
  <path>/scopes.yaml          # optional: description, priority, custom globs

  skills/<name>/
    SKILL.md                  # frontmatter: description, trigger, globs, allowed-tools
    scripts/*                 # optional executables (Claude symlinks them; Cursor warns; etc.)

  commands/<name>.md          # slash-command analog
  agents/<name>.md            # subagent definitions
  hooks/<name>.yaml           # { event, matcher, script }
  permissions.yaml            # { allow: [...], deny: [...], ask: [...] }
  mcp.yaml                    # { servers: { <name>: { command, args, env, url } } }

  <path>/permissions.yaml     # scoped: applies under <path>/ only
  <path>/hooks/<name>.yaml    # scoped hooks (Claude gets a scope-guard wrapper)
  <path>/skills/<name>/       # scoped skills

  shared/<file>.md            # snippet library for @include composition
```

`<!-- include: path/to/file.md -->` inside a `context.md` body splices
the file's content in. Includes are recursive (depth-limited) and contribute
to the source trace. Currently expanded only in `context.md` files (root and
scoped); `SKILL.md`, `commands/*.md`, and `agents/*.md` use a no-expand
read path.

## Capability matrix

```
PLUGIN     CONTEXT  PATHS   SEMANTIC  SKILLS  CMDS    AGENTS  HOOKS   PERMS   MCP
agents-md  native   degr.   degr.     degr.   degr.   degr.   degr.   degr.   degr.
claude     native   native  degr.     native  native  native  native  native  native
cline      native   native  native    degr.   native  ----    native  ----    native
continue   native   native  native    degr.   native  ----    ----    native  native
copilot    native   native  degr.     degr.   native  native  ----    ----    native
cursor     native   native  native    native  native  native  native  ----    native
gemini     native   native  degr.     degr.   native  native  native  native  native
windsurf   native   native  native    degr.   degr.   ----    native  ----    native
```

- **native**: 1:1 mapping; full fidelity.
- **degr.** (degraded): approximated in the target's nearest equivalent; some semantics lost. The plugin emits an info warning explaining what was lost.
- **----** (unsupported): not projected. Plugin emits a warning naming the dropped item.

As of v0.8.0, **17 cells flipped from `----` or `degr.` to `native`** when six of eight plugins were rewritten to match each tool's May 2026 feature surface. Cursor, Gemini, Cline, Continue, Copilot, and Windsurf all gained meaningful new emissions (hooks, agents, slash commands, MCP, dedicated skill formats, native permissions). See [CHANGELOG.md](CHANGELOG.md) for the per-plugin breakdown.

Show the matrix with `prism capabilities`.

## Importing existing configs

`prism init --from <tool>` reads the tool's native config and writes the
canonical `.agents/` shape. Round-trip tested (`init --from X` then
`compile` reproduces the original).

| Source                        | Importer  |
|-------------------------------|-----------|
| `CLAUDE.md` + `.claude/`      | claude    |
| `.cursor/rules/*.mdc`         | cursor    |
| `GEMINI.md` + `.gemini/`      | gemini    |
| `.clinerules/`                | cline     |
| `.continue/rules/`            | continue  |
| `.windsurf/rules/`            | windsurf  |
| `.github/instructions/`       | copilot   |
| `AGENTS.md`                   | agents-md |

Pass `-i` / `--interactive` to pick which imported items make it into
`.agents/` (per skill, command, scope, MCP server, agent):

```
prism init --from cursor -i
# include skill `pdf-editing`? [Y/n/a/d] y
# include scope `src/billing`? [Y/n/a/d/s] s   # keep scope, skip its skills
# include MCP server `linear`? [Y/n/a/d] a     # accept all remaining
```

EOF on stdin (Ctrl-D) auto-accepts the rest. Non-TTY stdin is refused
loudly rather than silently filtering nothing in CI.

## Layered config (`~/.agents/` + project)

`~/.agents/` is your personal global layer. Project `.agents/` is project-specific.

```
~/.agents/
  agents/reviewer.md          # your personal code reviewer
  skills/code-review/         # your house style
  permissions.yaml            # always-on Bash allowlist

your-project/.agents/
  context.md                  # project-specific context
  src/billing/context.md      # project-specific scope
```

Compile merges them: **project wins on collision** for scopes / skills /
commands / agents / MCP (matched by name); permissions are unioned;
hooks are concatenated; Context falls back to global only if project has none.

Auto-detected when `~/.agents/` exists. Override:

```
prism compile --global /other/path
prism compile --no-global             # ignore the global layer
```

Lockfile and `prism which` show clean tagged sources so you can tell what
came from where:

```
$ prism which AGENTS.md
context.md
global:agents/reviewer.md
global:skills/code-review/SKILL.md
src/billing/context.md
```

## Sync modes

Each plugin emits one of four op kinds per file:

- **`symlink`** — used when the projected content is identical to the source
  (e.g., `CLAUDE.md → .agents/context.md`). Zero runtime cost; source-of-truth
  visible in the tree. On Windows where `os.Symlink` requires admin/dev mode,
  symlinks auto-downgrade to content writes.
- **`write`** — used when transformation is needed (frontmatter injection,
  glob translation, section sectioning). Output is committed.
- **`merge`** — used for `settings.json`, `.mcp.json`, etc. The engine reads
  the existing file's bytes, the plugin's `Merger` closure produces the merged
  content, and the engine writes the result. **Preserves your manual edits**
  to keys the plugin doesn't own.
- **`append`** — used for hook lists and similar additive cases.

Per-target override in `.agents/agents.config.yaml`:

```yaml
targets: [claude, cursor, agents-md]   # or omit to autodetect
target_options:
  claude:
    mode: write                        # force write instead of default symlink
  cursor:
    disabled: true                     # skip even if autodetected
```

## Wrapper scripts: scope-guard and perms-guard

Some tools' hook contracts don't natively understand path-scoping or
permission policies. `prism` ships two hidden subcommands that fill the gap
via generated bash wrappers:

- **`prism scope-guard`** — gates a hook on a file-path scope. The
  generated wrapper at `.{plugin}/hooks/__scope-guard__/<scope>-<event>-<hook>.sh`
  reads the tool's hook JSON from stdin, extracts `tool_input.file_path`, and
  invokes the user's hook only if the path falls under the scope. Used by
  claude / cursor / gemini / cline / windsurf — every tool whose hook
  contract is JSON-on-stdin with exit-2-blocks semantics (which is most of
  them after the v0.8.0 plugin upgrades).
- **`prism perms-guard`** — enforces an allow/deny/ask policy for plugins
  without a native permission primitive. After v0.8.0 this is just **gemini**
  — continue moved to native `.continue/permissions.yaml`. The wrapper at
  `.gemini/hooks/__perms-guard__/<...>.sh` reads the hook JSON, consults a
  sidecar `policy.json`, and either exec's the underlying script (allow),
  exits non-zero (deny), or prompts on TTY (ask). Policy rules use a
  `tool:pattern` grammar — `bash:rm -rf *` matches any bash command starting
  with `rm -rf `; `bash` (no colon) matches any bash action. Deny dominates
  Allow, which dominates Ask. Plugins with native enforcement (claude →
  `.claude/settings.json`, continue → `.continue/permissions.yaml`) translate
  the same canonical `.agents/permissions.yaml` directly to their native
  format and don't go through perms-guard at all.

Both wrappers resolve the project root at runtime via
`${PRISM_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$(cd "${SCRIPT_DIR}/../../.." && pwd)}}`,
so the generated scripts survive `mv` of the project directory. Pass
`--no-hook-wrappers` to disable wrapper generation and project hooks as
global instead.

## Central registry

`prism add <name>` resolves bare names through a JSON index instead of
requiring a full `host/owner/repo` URL:

```
prism add billing-skills              # resolves via the registry index
prism add github.com/you/my-skills    # explicit git URL still works
prism add ./local/path                # local path still works
```

Default index URL is
`https://raw.githubusercontent.com/os-tack/prism-registry/main/index.json`;
override with `--registry <url>` or `PRISM_REGISTRY` env var. Cache lives
at `os.UserCacheDir()/prism/registry-index.json` with a 24h TTL. Flags:
`--registry`, `--refresh-registry`, `--no-fetch`.

## CI usage

```yaml
- run: prism check
```

`prism check` exits non-zero if the committed projections do not match what
`.agents/` would produce. Drives the "source-of-truth" invariant: edit
`.agents/`, never the generated files.

If you do edit a generated file manually, the next `prism compile` warns:

```
✓ AGENTS.md (agents-md, write, 1391 bytes)
  ⚠ [warn] AGENTS.md: manual edits detected; will be overwritten by agents-md
```

`prism` knows because the lockfile records the SHA-256 of every file it
wrote; the warning fires when the on-disk hash differs from both the
lockfile hash and the new planned content.

## Commands

```
prism init [--from <tool>] [-i]        # scaffold or import; -i for interactive
prism compile [--target X] [--dry-run] [--quiet]
prism check                            # CI-friendly; exits 1 on drift
prism diff                             # show what compile would change
prism which <projected-file>           # reverse-trace: which .agents/ source produced this
prism watch                            # rebuild on .agents/ changes (debounced; clean SIGINT)
prism capabilities [--target X]        # capability matrix
prism add <name|url|path> [--ref X]    # install a package into .agents/
prism remove <name>                    # uninstall a package
prism list                             # show installed packages
```

Global flags:

- `--root <dir>` — project root (default: current directory)
- `--global <dir>` — global layer parent (default: `~/` if `~/.agents/` exists)
- `--no-global` — skip the global layer
- `--no-hook-wrappers` — disable scope-guard / perms-guard wrapper generation

## How it works

```
.agents/ + ~/.agents/              [ParseLayered → *model.Project]
        │
        ▼
   ┌─────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
   │  Parse  │───▶│   Plan   │───▶│   Diff   │───▶│  Apply   │
   │ + @incl │    │ per-plugin│    │ vs disk  │    │  (or DR) │
   └─────────┘    └──────────┘    └──────────┘    └────┬─────┘
                                                       │
                                                       ▼
                                                 .agents/.lock
                                          (sources, plugin, kind, hash)
```

Plugins are pure: given a `*model.Project`, they return `[]plugin.Operation`.
The engine owns all filesystem I/O — including reading existing bytes for
`OpMerge` ops, which the plugin's `Merger` closure transforms into the merged
content. This contract means plugins are testable without a filesystem and
manual-edit detection is deterministic (the merge happens exactly once per
compile).

The lockfile tracks every file written: sources (with `global:` prefix for
the personal layer), plugin name, op kind, SHA-256 hash. Stale entries get
cleaned up on the next compile. Lockfile paths are forward-slashed
engine-side so they're portable across macOS/Linux/Windows.

## Releases

[See CHANGELOG.md](CHANGELOG.md) for full release notes. Highlights:

- **v0.8.0** — major plugin parity release: 17 capability cells flipped
  to native across cursor / gemini / cline / continue / copilot / windsurf
- **v0.7.x** — central registry, `--interactive` importers, perms-guard
  wrappers, OpMerge contract refactor, round-trip test coverage, path-portable
  wrappers, version package, nit sweep
- **v0.6.0** — Windows path handling, per-file hashes in `packages.yaml`,
  importer round-trip tests, `--no-hook-wrappers`
- **v0.5.0** — 8 importers, `@include` directive, skill registry, scoped capabilities
- **v0.4.0** — initial release: 8 plugins, layered config, drift detection

## License

MIT. See [LICENSE](LICENSE).
