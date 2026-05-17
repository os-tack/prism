# 03-include-composition

Share a chunk of markdown across multiple contexts with one directive.

## What this shows

The `<!-- include: <path> -->` directive: write the house-style block
once under `shared/`, pull it into the root context and into a scoped
context. `prism which` traces both consumers back to the source.

## The `.agents/` structure

```
.agents/
  context.md              # includes shared/style.md
  agents.config.yaml
  shared/
    style.md              # the included content (one source of truth)
  cmd/
    context.md            # also includes shared/style.md
```

The directive is an HTML-style comment on its own line:

```
<!-- include: shared/style.md -->
```

It must occupy the whole line (anything trailing disqualifies the
match — at which point the comment is treated as literal text).
Relative paths resolve from the directory of the file that contains
the directive. `<!-- include: global:foo.md -->` resolves under
`~/.agents/` when the global layer is active.

## Run it

```
cd examples/03-include-composition
prism compile
```

## What you get

```
CLAUDE.md                 # written (not symlink — content differs from source after expansion)
cmd/CLAUDE.md             # also written; same reason
AGENTS.md                 # written
.cursor/rules/_root.mdc
.cursor/rules/cmd.mdc
```

Symlink mode degrades to write mode automatically when a document
contains `@include` expansion: the symlink target would only hold the
unexpanded source. The plugin attaches an info warning explaining the
downgrade.

## Things to try

1. `prism which CLAUDE.md` — lists both `context.md` and
   `shared/style.md`.
2. `prism which cmd/CLAUDE.md` — same: both sources show up.
3. Edit `shared/style.md` and recompile. Both `CLAUDE.md` and
   `cmd/CLAUDE.md` re-emit. (In `prism watch`, the included file is
   tracked too — saving it retriggers compile.)
4. Try `<!-- include: ../../etc/passwd -->`. The parser rejects it
   with `include: path escapes .agents/`.

## Limits

- Include expansion runs only inside `context.md` (root and scoped).
  Skills, commands, and subagent files do not expand `@include`
  directives — the directive is left literal in their output. This is
  intentional for v0.5 and may change.
- Max nesting depth defaults to 16; override with
  `include.max_depth:` in `agents.config.yaml`.
- Cycles (A → B → A) return `include: cycle detected`.
