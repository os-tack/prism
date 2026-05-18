---
name: deploy
description: Deploy the current branch to the named environment.
argument_hint: "[environment]"
arguments:
  - environment
  - rollback_tag
model: inherit
tools:
  - Bash(./scripts/deploy.sh)
  - Bash(git status)
  - Bash(git log -1 --oneline)
agent: code-reviewer
auto_invoke: false
extensions:
  claude:
    permissionMode: plan
  copilot:
    handoffs:
      - security-auditor
---

Deploy the current branch to `{{arg:environment}}` (rollback tag:
`{{arg:rollback_tag}}`).

Current branch: {{shell:git rev-parse --abbrev-ref HEAD}}
Last commit: {{shell:git log -1 --oneline}}
Deploy manifest: {{file:./manifests/deploy.yaml}}

Proceed by running `./scripts/deploy.sh {{arg:environment}}`.
