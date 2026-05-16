# agents

Project a canonical `.agents/` directory into per-AI-tool config files.

Write your project context, skills, commands, subagents, hooks, permissions,
and MCP server config **once**. `agents` compiles them into the right shape
for Claude Code, Cursor, Gemini CLI, GitHub Copilot, Cline, Continue,
Windsurf, and AGENTS.md — preserving native semantics where the tool
supports them and degrading visibly where it doesn't.

```
$ agents compile
✓ AGENTS.md                                       (agents-md, write)
✓ CLAUDE.md → .agents/context.md                  (claude, symlink)
✓ src/billing/CLAUDE.md → ../../.agents/...       (claude, symlink, cascade)
✓ .claude/skills/pdf-editing/SKILL.md             (claude, symlink)
✓ .claude/settings.json                           (claude, merge; preserves user keys)
✓ .mcp.json                                       (claude, merge)
✓ .cursor/rules/_root.mdc                         (cursor, write)
✓ .cursor/rules/src-billing.mdc                   (cursor, write; globs frontmatter)
✓ .cursor/mcp.json                                (cursor, merge)
✓ GEMINI.md → .agents/context.md                  (gemini, symlink)
✓ .gemini/settings.json                           (gemini, merge)
✓ .clinerules/00-context.md                       (cline, write)
✓ .continue/rules/_root.md                        (continue, write)
✓ .continue/mcpServers/linear.yaml                (continue, write)
✓ .windsurf/rules/_root.md                        (windsurf, write; trigger frontmatter)
✓ .github/copilot-instructions.md                 (copilot, symlink)
✓ .github/instructions/src-billing.instructions.md (copilot, write; applyTo)
✓ .github/prompts/review.prompt.md                (copilot, write)
Compiled: 33 changed, 0 unchanged, 0 removed, 14 warnings
```

## Install

```
go install agents.dev/agents/cmd/agents@latest
```

Or build from source:

```
git clone https://github.com/scottmeyer/agents
cd agents
go build -o agents ./cmd/agents
```

## Quickstart

```
cd your-project
agents init           # scaffold .agents/ + agents.config.yaml
agents compile        # generate the projections
agents check          # verify (use in CI; exit 1 on drift)
agents watch          # rebuild on .agents/ changes
```

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
```

## Capability matrix

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

- **native**: 1:1 mapping; full fidelity.
- **degr.** (degraded): approximated in the target's nearest equivalent; some semantics lost. The plugin emits an info warning explaining what was lost.
- **----** (unsupported): not projected. Plugin emits a warning naming the dropped item.

Show the matrix with `agents capabilities`.

## Layering (`~/.agents/` + project)

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
agents compile --global /other/path
agents compile --no-global             # ignore the global layer
```

Lockfile and `agents which` show clean tagged sources so you can tell what
came from where:

```
$ agents which AGENTS.md
context.md
global:agents/reviewer.md
global:skills/code-review/SKILL.md
src/billing/context.md
```

## Sync modes

Each plugin emits one of three op kinds per file:

- **`symlink`** — used when the projected content is identical to the source
  (e.g., `CLAUDE.md → .agents/context.md`). Zero runtime cost; source-of-truth
  visible in the tree.
- **`write`** — used when transformation is needed (frontmatter injection,
  section sectioning, glob translation). Output is committed.
- **`merge`** — used for `settings.json`, `.mcp.json`, etc. The plugin reads
  the existing file, deep-merges the canonical content, and writes the result.
  **Preserves your manual edits** to keys the plugin doesn't own.

Per-target override in `.agents/agents.config.yaml`:

```yaml
targets: [claude, cursor, agents-md]   # or omit to autodetect
target_options:
  claude:
    mode: write                        # force write instead of default symlink
  cursor:
    disabled: true                     # skip even if autodetected
```

## CI usage

```
- run: agents check
```

`agents check` exits non-zero (`engine.ErrDrift`) if the committed
projections do not match what `.agents/` would produce. Drives the
"source-of-truth" invariant: edit `.agents/`, never the generated files.

If you do edit a generated file manually, the next `agents compile` warns:

```
✓ AGENTS.md (agents-md, write, 1391 bytes)
  ⚠ [warn] AGENTS.md: manual edits detected; will be overwritten by agents-md
```

`agents` knows because the lockfile records the SHA-256 of every file it
wrote; the warning fires when the on-disk hash differs from both the lockfile
hash and the new planned content.

## Commands

```
agents init [--from claude]            # scaffold .agents/ (optionally import existing CLAUDE.md tree)
agents compile [--target X] [--dry-run] [--quiet]
agents check                           # CI-friendly; exits 1 on drift
agents diff                            # show what compile would change; exits 0 regardless
agents which <projected-file>          # reverse-trace: which .agents/ source produced this
agents watch                           # rebuild on .agents/ changes (debounced; clean SIGINT)
agents capabilities [--target X]       # capability matrix
```

Global flags:

- `--root <dir>` — project root (default: current directory)
- `--global <dir>` — global layer parent (default: `~/` if `~/.agents/` exists)
- `--no-global` — skip the global layer

## How it works

```
.agents/ + ~/.agents/              [ParseLayered → *model.Project]
        │
        ▼
   ┌─────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
   │  Parse  │───▶│   Plan   │───▶│   Diff   │───▶│  Apply   │
   │  layered│    │ per-plugin│    │ vs disk  │    │  (or DR) │
   └─────────┘    └──────────┘    └──────────┘    └────┬─────┘
                                                       │
                                                       ▼
                                                 .agents/.lock
                                          (sources, plugin, kind, hash)
```

Plugins are pure (given a `*model.Project`, they return `[]plugin.Operation`).
The engine owns all filesystem I/O. The lockfile tracks every file written:
sources (with `global:` prefix for the personal layer), plugin name, op kind,
SHA-256 hash. Stale entries get cleaned up on the next compile.

## Project status

This is v0.4. The architecture is settled, 8 plugins are implemented, layering
works, 165+ tests cover the critical paths. Known gaps:

- **Windows path separators** — `filepath.Join` produces backslashes on Windows;
  lockfile keys and `Which` lookup would mismatch. No Windows CI yet.
- **`init --from <tool>`** — only `--from claude` works today; importers for
  Gemini / Cursor / Cline / Continue / Windsurf / Copilot are TODO.
- **`@include` in markdown** — cross-scope content sharing not yet supported.
- **Skill registry** — `agents add <package>` for community-shared skills.

## License

MIT. See [LICENSE](LICENSE).
