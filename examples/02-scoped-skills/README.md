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
src/billing/GEMINI.md                   # cascade (Gemini also walks up from cwd)
.gemini/agents/stripe-webhook.md        # v0.8.0: skill projects AS a Gemini agent with trigger+globs in description
.cursor/rules/_root.mdc                 # root rule, alwaysApply: true
.cursor/rules/src-billing.mdc           # scoped rule, globs: src/billing/**
.cursor/skills/stripe-webhook/SKILL.md  # v0.8.0: native Cursor 2.4+ skill format (was .cursor/rules/skill-*.mdc)
AGENTS.md                               # root + scope sections concatenated
```

What each plugin does with the scope:

- **Claude** uses the native cascade: it auto-loads `src/billing/CLAUDE.md`
  for any file under that directory. The skill keeps `globs:` in its
  frontmatter and Claude evaluates them itself.
- **Cursor** emits one `.mdc` file per scope with `globs:` in the
  frontmatter — Cursor's native trigger mechanism. Skills land at
  `.cursor/skills/<name>/SKILL.md` (the Cursor 2.4+ native format that
  v0.8.0 emits instead of the legacy `.cursor/rules/skill-*.mdc`).
- **Gemini** now has scope cascade (multiple `GEMINI.md` walked from cwd).
  Skills have no dedicated Gemini primitive, so the plugin projects each
  skill as a Gemini agent at `.gemini/agents/<name>.md` with the trigger
  and globs appended to the description. Auto-glob activation is lost
  (info warning); manual activation via `@<name>` works.
- **AGENTS.md** is a single concatenated file with one section per scope
  — degraded, but at least visible.

## Things to try

1. Run `prism which src/billing/CLAUDE.md`. It prints
   `src/billing/context.md` — the reverse trace works.
2. Add `cline` to `targets:`. Cline gets the same degradation as
   Gemini for paths; check the compile warnings.
3. Drop the `globs:` line from `SKILL.md`. The Claude projection still
   works (Claude treats the skill as global); the Cursor projection
   loses its glob filter and becomes always-applied.
