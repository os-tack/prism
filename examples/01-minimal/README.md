# 01-minimal

The smallest useful `.agents/`: one paragraph of project context, three
targets.

## What this shows

How a single `context.md` plus a target list becomes the per-tool root
context file each tool reads. No scopes, no skills, no MCP — just the
projection itself.

## The `.agents/` structure

```
.agents/
  context.md              # one paragraph of project context
  agents.config.yaml      # targets: [claude, cursor, agents-md]
```

## Run it

```
cd examples/01-minimal
prism compile
```

(Use `prism compile --dry-run` first if you want to see the planned ops
without writing.)

## What you get

```
CLAUDE.md                  # symlink → .agents/context.md
.cursor/rules/_root.mdc    # written; cursor needs frontmatter
AGENTS.md                  # symlink → .agents/context.md
.agents/.lock              # records every projected file + its source
```

- **Claude** and **AGENTS.md** are content-identical to `context.md`, so
  the plugin emits a symlink (zero runtime cost, source-of-truth visible
  in the tree).
- **Cursor** writes a real file because it injects MDC frontmatter
  (`description`, `globs`, `alwaysApply`) that the source doesn't have.

## Things to try

1. Add `gemini` to `targets:` and recompile. `GEMINI.md` shows up as a
   symlink alongside `CLAUDE.md`.
2. Set `target_options.claude.mode: write` in `agents.config.yaml`. The
   symlink becomes a regular file with copied content.
3. Edit `CLAUDE.md` directly, then run `prism check`. It exits non-zero
   and tells you the file drifted from source.
