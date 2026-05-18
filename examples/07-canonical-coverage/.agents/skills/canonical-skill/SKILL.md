---
name: canonical-skill
description: |
  Exercise every Skill field. The activation block uses polymorphic
  modes (glob + model_decision); user_invocable and model_invocable
  flip explicitly; arguments, scripts, references, model, and
  subagent all appear.
when_to_use: |
  Invoke when reviewing the contract-test fixture, or when you need a
  reference for what a fully-populated Skill looks like.
activation:
  modes: [glob, model_decision]
  globs:
    - src/canonical/**
    - tests/canonical/**
  content_regex: 'canonical_skill_marker'
  user_invocable: true
  model_invocable: true
allowed_tools:
  - Bash(./scripts/verify.sh)
  - Read
  - Edit
arguments:
  - name: target
    description: The target subsystem to inspect.
    required: true
  - name: scope
    description: Optional scope override (default project-wide).
    required: false
scripts:
  - scripts/verify.sh
references:
  - references/contract.md
model: inherit
subagent: code-reviewer
extensions:
  claude:
    effort: high
  cursor:
    alwaysApply: false
---

# Canonical skill

Invoking with target `{{arg:target}}`{{arg:scope}}.

1. Verify the contract by running `{{shell:./scripts/verify.sh}}`.
2. Read the canonical model definitions: see {{file:internal/model/types.go}}.
3. Reference the [contract spec](references/contract.md) for field expectations.
