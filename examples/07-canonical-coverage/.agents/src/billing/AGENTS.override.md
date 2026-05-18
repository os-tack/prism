---
name: billing-override
description: |
  Billing subsystem REPLACEMENT (override semantic). This document
  replaces the parent CLAUDE.md / AGENTS.md cascade rather than
  augmenting it.
activation: cascade
is_override: true
priority: 20
tags:
  - billing
  - override
extensions:
  agents-md:
    emit_filename: AGENTS.override.md
---

# Billing subsystem (override)

When this override is in effect, the parent project-root cascade is
SUPPRESSED inside `src/billing/`. Only the rules below apply.

- All money types are `Cents` (alias for `int64`). Never `float64`.
- All migrations under `migrations/billing/` are reversible.
