# 02-scoped-skills

Path-scoped context plus a skill that only applies inside that scope.

## What this shows

How a nested `<path>/context.md` becomes a per-tool scoped rule, and how
a skill with `globs:` frontmatter rides along — natively in Claude and
Cursor, degraded in Gemini.

## The `.agents/` structure

```
.agents/
  context.md
  agents.config.yaml
  src/billing/
    context.md                          # scoped to src/billing/**
  skills/stripe-webhook/
    SKILL.md                            # frontmatter: globs, allowed-tools
```

## Run it

```
cd examples/02-scoped-skills
prism compile
```

## What you get

```
CLAUDE.md                               # root, symlink
src/billing/CLAUDE.md                   # cascade — Claude reads it for files under src/billing/
.claude/skills/stripe-webhook/SKILL.md  # native skill, globs preserved
GEMINI.md                               # root
.cursor/rules/_root.mdc                 # root rule, alwaysApply: true
.cursor/rules/src-billing.mdc           # scoped rule, globs: src/billing/**
AGENTS.md                               # root + scope sections concatenated
```

What each plugin does with the scope:

- **Claude** uses the native cascade: it auto-loads `src/billing/CLAUDE.md`
  for any file under that directory. The skill keeps `globs:` in its
  frontmatter and Claude evaluates them itself.
- **Cursor** emits one `.mdc` file per scope with `globs:` in the
  frontmatter — Cursor's native trigger mechanism. Skills also map to
  scoped MDC rules.
- **Gemini** has no path-scope primitive, so the plugin warns and emits
  only the root `GEMINI.md`. The skill is dropped entirely (Gemini has
  no skills primitive); look for an info-severity warning in compile
  output.
- **AGENTS.md** is a single concatenated file with one section per scope
  — also degraded, but at least visible.

## Things to try

1. Run `prism which src/billing/CLAUDE.md`. It prints
   `src/billing/context.md` — the reverse trace works.
2. Add `cline` to `targets:`. Cline gets the same degradation as
   Gemini for paths; check the compile warnings.
3. Drop the `globs:` line from `SKILL.md`. The Claude projection still
   works (Claude treats the skill as global); the Cursor projection
   loses its glob filter and becomes always-applied.
