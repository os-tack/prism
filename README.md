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
curl -L -o prism https://github.com/os-tack/prism/releases/latest/download/prism-v0.8.2-darwin-arm64
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

## Schema version

prism's canonical model is versioned. v0.9.0 ships **schema v2** — the
contract that the rest of the v0.9 → v1.0 line is committed to. Every
`agents.config.yaml` MUST declare `schema_version: 2` at the top; v0.9
reads only `2`, and a forward-incompat value (e.g. `3`) is a hard error
with an upgrade message.

The full spec — seven canonical primitives, polymorphic activation,
dimension-aware permissions, the MCP transport/auth/policy surface, the
hook event taxonomy, the `extensions:` block, JSON Schema pointers, and
the per-field × per-plugin capability matrix — lives in
[SPEC.md](SPEC.md). The v0.9 line is the **soak cycle** before v1.0
declares stability per SPEC §11.2.

JSON Schemas for editor integration live at `schema/v2/`. Each example
under `examples/` ships a `# yaml-language-server: $schema=...` hint
at the top so editors auto-resolve.

## Capability matrix

```
PLUGIN     CONTEXT  PATHS   SEMANTIC  SKILLS  CMDS    AGENTS  HOOKS   PERMS   MCP
agents-md  native   degr.   degr.     degr.   degr.   degr.   degr.   degr.   degr.
claude     native   native  degr.     native  native  native  native  native  native
cline      native   native  native    degr.   native  ----    native  native  native
continue   native   native  native    degr.   native  ----    native native  native
copilot    native   native  degr.     degr.   native  native  native* native* native
cursor     native   native  native    native  native  native  native  ----    native
gemini     native   native  degr.     degr.   native  native  native  native  native
windsurf   native   native  native    degr.   degr.   ----    native  ----    native
```

- **native**: 1:1 mapping; full fidelity.
- **native\*** (copilot Hooks + Perms): native projection, opt-in via `--enable-preview-hooks` because the underlying Copilot hook API is in public preview at the GitHub side. Default OFF; flip on per-run or in CI.
- All v0.9.0 Phase 2.5 plumbing items (continue hooks-native, claude permission rule fan-out, MCP transport-aware wire shape, cline filename-dispatch hooks, cursor cross-emit dedup, gemini per-action event translation, AGENTS.override.md split) **landed in the v0.9.0 line**. See [CHANGELOG.md](CHANGELOG.md) for the per-cell breakdown. Remaining out-of-scope items (rate-limiting, http handler shimming, single-tool events, etc.) are listed under "Still deferred".
- **degr.** (degraded): approximated in the target's nearest equivalent; some semantics lost. The plugin emits an info warning explaining what was lost.
- **----** (unsupported): not projected. Plugin emits a warning naming the dropped item.

Schema v2 is in **soak cycle** for the v0.9 line — additive changes
resume at v0.10 once the contract test suite has run against the
registry and any third-party plugins. The fine-grained per-field
capability matrix (which fields are silent vs unsupported on which
plugins) lives in [SPEC.md §12](SPEC.md).

As of v0.9.0, **the schema v2 canonical contract is live and every plugin honors it in wire form** — Continue HOOKS flipped from `----` to `native` via the shared Claude-shape serializer, cline hooks rewrote to filename-dispatch scripts per SPEC §4.4.5, claude permissions fan out through the dimension-aware grammar, MCP servers carry full transport + auth + 6 policy fields. The 218-entry Phase 2.5 punch list in the capability contract test (`plugins/capability_contract_test.go`) is empty after Phase 2.6 — every (plugin × primitive × field) cell now passes the strict contract. See [CHANGELOG.md](CHANGELOG.md) v0.9.0 for the per-cell breakdown.

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

Two enforcement concepts — path-scoping and permission policies — are
spec'd canonically in `.agents/` but not every tool has the native
runtime surface to enforce them. `prism` ships two hidden subcommands
(`prism scope-guard`, `prism perms-guard`) that fill the gap as
generated bash wrappers. Plugins emit a wrapper alongside each hook
that needs the gate; the wrapper calls back into the prism binary at
runtime.

### scope-guard — file-path scoping for hooks

When a hook lives under a scope (`.agents/src/billing/hooks/...`) but
the target tool fires hooks globally, the plugin emits:

```
.{plugin}/hooks/__scope-guard__/<scope>-<event>-<hook>.sh
```

The script reads the tool's hook JSON from stdin, extracts
`tool_input.file_path`, and invokes the user's hook only if the path
falls under the scope. Used by every tool whose hook contract is
JSON-on-stdin with exit-2-blocks semantics:

| Plugin   | scope-guard | Native scoping? |
|----------|-------------|-----------------|
| claude   | yes         | no              |
| cursor   | yes         | no              |
| gemini   | yes         | no              |
| cline    | yes         | no              |
| windsurf | yes         | no              |
| copilot  | preview-on  | preview-on      |
| continue | —           | folded (warn)   |
| agentsmd | —           | (no hook primitive) |

### perms-guard — allow / ask / deny enforcement for plugins without a native permission primitive

Sidecar at `.{plugin}/hooks/__perms-guard__/policy.json`; wrapper at
`.{plugin}/hooks/__perms-guard__/<scope>.sh`. The wrapper reads the
hook JSON, consults the policy, and either `exec`s the underlying
script (Allow), exits non-zero (Deny), or prompts on TTY (Ask).

Policy rules use the canonical SPEC §4.6.2 grammar: `<target>:<pattern>`
plus the dimension synonyms `fs:`, `network:`, `mcp:<server>[:<tool>]`,
recursive `**` glob, and `!` negation prefix. **Deny dominates Allow,
which dominates Ask.** In non-TTY contexts, Ask degrades to Deny.

| Plugin   | perms-guard          | Native?                           |
|----------|----------------------|-----------------------------------|
| gemini   | yes                  | no                                |
| cline    | yes                  | no                                |
| copilot  | yes (preview-off)    | yes (preview-on, opt-in)          |
| claude   | —                    | `.claude/settings.json`           |
| continue | —                    | `.continue/permissions.yaml`      |
| cursor   | —                    | (folded into sandbox profile)     |
| windsurf | —                    | unsupported                       |
| agentsmd | —                    | informational text only           |

Plugins with native enforcement translate the canonical
`.agents/permissions.yaml` directly to their wire format and don't go
through perms-guard at all.

### Project-root resolution

Both wrappers resolve the project root at runtime via:

```
${PRISM_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$(cd "${SCRIPT_DIR}/../../.." && pwd)}}
```

so the generated scripts survive `mv` of the project directory. Pass
`--no-hook-wrappers` to `prism compile` to disable wrapper generation
and emit project hooks as global instead.

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
- `--enable-preview-hooks` — opt into Copilot preview hooks (.github/hooks/hooks.json + perms-guard wiring); off by default until GA

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

- **v0.9.0** — canonical schema v2: seven primitives, per-field
  capability declarations, polymorphic skill activation,
  dimension-aware permissions (`fs:`/`network:`/`mcp:`/`**`/`!`),
  explicit MCP transport+auth+policy surface, 25 canonical hook events,
  project-wide `extensions:` block, `Validate()` pass with `--strict`,
  JSON Schema generation at `schema/v2/`, soak cycle for v1.0
- **v0.8.2** — copilot HOOKS (preview, opt-in), copilot PERMS, cline PERMS;
  three more cells flipped to native (issues #2/#3/#4)
- **v0.8.1** — importer parity for the v0.8.0 emissions; round-trip lock-in
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
