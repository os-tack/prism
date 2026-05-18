# 07-canonical-coverage

The **golden round-trip fixture** for prism's schema v2. This project
exercises every canonical field of every primitive at least once. Phase
4's contract test runs:

```
parse → validate → plan → emit → re-parse
```

against this tree and asserts byte-identical round-trip on the canonical
model. If you add a canonical field to `internal/model/`, **add an
exerciser here in the same PR**.

## What's in here

| Path                                              | Purpose                                                          |
|---------------------------------------------------|------------------------------------------------------------------|
| `.agents/agents.config.yaml`                      | `schema_version: 2`, all 8 targets, `target_options`, `include`, project-level `extensions:` |
| `.agents/agents/code-reviewer.md`                 | Agent exercising every field (Name, Description, Model, ModelFallbacks, Tools, DisallowedTools, ReadOnly, Background, MaxTurns, Temperature, MCPServers — both `name:` ref and inline — AllowedSubagents, UserInvocable, ModelInvocable, InitialPrompt, Extensions) |
| `.agents/skills/canonical-skill/SKILL.md`         | Polymorphic Activation (`modes: [glob, model_decision]`, globs, content_regex, user_invocable, model_invocable), AllowedTools, Arguments (with descriptions + required flags), Scripts, References, Model, Subagent, Extensions |
| `.agents/skills/canonical-skill/scripts/verify.sh`| Concrete script for the `scripts:` field                         |
| `.agents/skills/canonical-skill/references/contract.md`| Concrete reference for the `references:` field             |
| `.agents/commands/deploy.md`                      | Command with Description, ArgumentHint, Arguments, Model, Tools, Agent, AutoInvoke, Extensions, plus all three substitution forms (`{{arg:name}}`, `{{shell:cmd}}`, `{{file:path}}`) |
| `.agents/hooks/canonical-hook.yaml`               | Hook with canonical event, Matcher (kind/patterns), three handler kinds (command + http + mcp_tool), sequential, disabled, extensions |
| `.agents/mcp.yaml`                                | All three transports (stdio + http + sse), Auth bearer + Auth header, all six MCP policy fields |
| `.agents/permissions.yaml`                        | Global perms exercising bash/Edit/Read/Write/MultiEdit/fs/network/mcp targets with exact, prefix `*`, recursive `**`, and negated `!` patterns |
| `.agents/AGENTS.md`                               | Root Scope with `activation: always`                             |
| `.agents/src/billing/AGENTS.md`                   | Scoped Scope with `activation: cascade`                          |
| `.agents/src/billing/AGENTS.override.md`          | Override Scope (`is_override: true`)                             |
| `.agents/src/billing/permissions.yaml`            | Scoped permissions                                               |
| `.agents/src/billing/skills/scoped-skill/SKILL.md`| Scoped skill (ScopePath derived from filesystem location)        |

## Running it

```
cd examples/07-canonical-coverage
prism compile          # produces projections for all 8 targets
prism check            # asserts no drift
prism check --strict   # promotes warnings to errors (CI-friendly)
```

Expect warnings — every projection will surface fields that some plugins
declare `Unsupported`. That's the point: this fixture is the canonical
warning catalog.

## Contract test

`internal/engine/contract_test.go` (Phase 4) loads this fixture and
asserts:

1. **Parse**: every file produces no `ValidationError`-level errors.
2. **Validate**: warnings count matches the SPEC §12 capability matrix.
3. **Plan**: every plugin produces a non-empty `[]plugin.Operation`.
4. **Emit + re-parse**: the emitted artifacts re-parse into a canonical
   model that's structurally identical to the original (modulo lossy
   fields, which are tracked in the warning report).

The fixture should be **stable** — changes to it should be either
additive (new field exerciser) or coordinated with the contract test.
