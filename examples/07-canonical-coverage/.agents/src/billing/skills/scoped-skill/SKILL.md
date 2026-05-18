---
name: scoped-skill
description: |
  Scoped skill living under src/billing/. Exercises ScopePath
  derivation from filesystem location.
activation:
  modes: [glob]
  globs:
    - src/billing/**
allowed_tools:
  - Read
  - Edit
arguments: []
extensions:
  claude:
    effort: medium
---

# Scoped skill

This skill is automatically scoped to `src/billing/` based on its
filesystem location under `.agents/src/billing/skills/scoped-skill/`.

Plugins that support per-scope skills emit it under their scoped
location; plugins that don't fall back to a `<scope-slug>-<name>`
filename prefix.
