# prism v0.9.0 canonical schema

**Document version:** 2 (schema_version: 2)
**Binary version:** 0.9.0
**Status:** soak-cycle for v1.0
**License:** see `LICENSE`

This is the canonical schema specification for prism v0.9.0. It defines the
data model, file layout, plugin contract, lockfile shape, and JSON Schema
strategy for projecting a `.agents/` source tree into the configuration files
of eight AI agent tools: Claude Code, Cursor, Gemini CLI, GitHub Copilot,
Cline, Continue, Windsurf, and the AGENTS.md cross-tool spec.

Anyone reading this document should be able to implement prism, author
projects against it, or write a new plugin without external references.

## Table of contents

1. [Overview](#1-overview)
2. [Conceptual model](#2-conceptual-model)
3. [File layout (`.agents/` tree)](#3-file-layout-agents-tree)
4. [Primitives](#4-primitives)
   - 4.1 [Agent](#41-agent)
   - 4.2 [Skill](#42-skill)
   - 4.3 [Command](#43-command)
   - 4.4 [Hook](#44-hook)
   - 4.5 [MCPServer](#45-mcpserver)
   - 4.6 [Permissions](#46-permissions)
   - 4.7 [Scope](#47-scope)
5. [Cross-cutting](#5-cross-cutting)
6. [Plugin interface](#6-plugin-interface)
7. [Lockfile](#7-lockfile)
8. [Schema versioning](#8-schema-versioning)
9. [JSON Schema](#9-json-schema)
10. [Migration from v0.8 (out of scope — greenfield)](#10-migration-from-v08)
11. [Open questions / future work](#11-open-questions--future-work)
12. [Appendix: capability matrix](#12-appendix-capability-matrix)

## 1. Overview

### What prism does

prism translates a single canonical source tree (`.agents/`) into the
configuration files expected by N agent tools. Authors maintain one set of
agents, skills, commands, hooks, MCP servers, permissions, and scoped
guidance; prism's plugins emit each target tool's native layout.

A `prism compile` run reads `.agents/`, parses it into an in-memory
`Project`, runs validation, then asks each registered plugin to `Plan()` a
list of filesystem operations. The engine applies those operations, records
each file in a lockfile, and reports degradation warnings emitted by
plugins along the way.

### The contract between user and tool

prism is the source of truth for a project's agent configuration. Authors
edit only `.agents/`. The generated files (`.claude/`, `.cursor/`,
`.github/`, etc.) are reproducible from `.agents/` and the lockfile. The
lockfile lets prism detect drift (a generated file was hand-edited),
identify orphans (a file prism used to generate but no longer does), and
attribute each generated file back to its canonical source via the
`prism which` command.

Authors do not need to know which plugins are active to write canonical
files. They MAY use the `extensions:` block (§5.1) to pass plugin-specific
fields verbatim when a tool exposes a richer surface than the canonical
model carries.

### The contract between prism and plugins

A plugin is a Go type implementing the `Plugin` interface (§6.1). Plugins
are pure: their `Plan()` method takes an in-memory model, returns a slice
of operations, and never touches the filesystem. The engine handles all
I/O, conflict resolution, and lockfile bookkeeping.

Plugins declare per-field capabilities (§6.2) so the engine can warn at
compile time when a canonical field is dropped, degraded, or silently
ignored by a target. The engine cross-references the per-project usage
against per-plugin capabilities and surfaces a single coherent warning
report.

The canonical schema is versioned (§8). Plugins declare which schema
version they understand; the engine refuses to load any plugin whose
declared version predates the project's `schema_version:`.

## 2. Conceptual model

prism's canonical model has exactly **seven primitives**. Each primitive
captures one semantic axis of agent configuration. The primitives are
independent of any one tool's vocabulary.

### 2.1 The seven primitives

**Agent.** A named, delegatable persona — a configuration entity with a
system prompt that the main agent (or the user) can invoke. Distinct from
guidance (always-on context), from skills (procedure libraries), and from
commands (single-turn templates). Vendor synonyms: subagent, custom
agent, assistant.

**Skill.** A reusable named capability — a markdown how-to document whose
body carries instructions and whose frontmatter carries activation hints.
Activation can be glob-attached, model-decision, manual, or always-on, in
any combination (see §4.2.4). Vendor synonyms: skill, rule, workflow,
prompt-file, custom instruction.

**Command.** A reusable, user-invocable prompt template. Distinct from
Skill because the file shape is one file per command (not a directory),
the invocation model is `/name` (not glob/description), and the lifecycle
is single-turn (not persistent in context). Vendor synonyms: slash
command, custom command, prompt, workflow.

**Hook.** A lifecycle event handler. A script (or HTTP endpoint, MCP tool,
prompt, or sub-agent) that fires on a canonical event (`pre_tool_use`,
`session_start`, etc.) and optionally blocks or modifies the agent's
behavior. Vendor synonyms: hook, lifecycle hook, callback, cascade hook.

**MCPServer.** A Model Context Protocol server configuration — transport
choice, command/URL, environment, headers, auth, and per-server policy
fields. Vendor synonyms: mcpServers entry, server, MCP tool source.

**Permissions.** An allow / ask / deny policy controlling which tools and
operations the agent may invoke without explicit user approval. Grammar is
dimension-aware (§4.6.2): `bash:`, `Edit:`, `fs:`, `network:`, `mcp:`,
etc. Vendor synonyms: permissions, allowlist, sandbox profile.

**Scope.** A path- or trigger-scoped context document — the canonical
type for both filesystem-cascaded guidance (CLAUDE.md, AGENTS.md,
GEMINI.md) and frontmatter-glob rules (Cursor `.mdc`, Copilot
`.instructions.md`, Cline `.clinerules`, Continue `.continue/rules/`,
Windsurf `.windsurf/rules/`).

### 2.2 What's NOT a separate primitive

**Context.** The root-level project description has no separate type.
A `Scope` with `Path: ""` and `Activation: Always` IS the root context.
This collapse eliminates the maintenance burden of two near-identical
types.

**Rule.** Rules and Skills are the same primitive viewed from different
ends of the activation spectrum. The polymorphic `SkillActivation`
(§4.2.4) covers always-on, glob-attached, model-decision, and manual
modes in one shape.

**Workflow.** Cline and Windsurf workflows are Skills with
`Activation.Modes = [Manual]`. The Skill primitive subsumes them.

**Assistant / Mode.** Continue's `Assistant` is the entire `config.yaml`;
Windsurf Cascade's `Plan/Code/Ask` modes are UI states with no
user-definable configuration. Neither is a primitive in the prism sense.

### 2.3 Scoping

Every primitive carries a `ScopePath` field. An empty `ScopePath` means
"global / project-wide". A non-empty `ScopePath` (e.g. `src/billing`)
means "this primitive lives under that subtree of the project."

Different plugins honor `ScopePath` differently:
- Cascade plugins (Claude, Gemini, AGENTS.md) write scoped Scope
  documents at the actual filesystem path.
- Frontmatter-glob plugins (Cursor, Cline, Continue, Copilot, Windsurf)
  synthesize a `globs: ["<ScopePath>/**"]` entry on a Skill or Scope.
- Per-scope Permissions enforcement requires the perms-guard wrapper
  family (Gemini, Cline, Copilot); all other plugins fold scoped
  permissions into the global block with one info warning per scope.

Plugins announce which scope semantics they support per primitive via the
`Permissions.Global` / `Permissions.Scoped` capability split (§6.2) and
the `ScopePath` cells in each primitive's field matrix (§12).

## 3. File layout (`.agents/` tree)

### 3.1 Directory structure

```
.agents/
  agents.config.yaml             # required; project-level config
  agents/                        # one file per Agent
    code-reviewer.md
    security-auditor.md
  skills/                        # one directory per Skill
    stripe-webhook/
      SKILL.md
      scripts/
        verify-signature.sh
      references/
        webhook-schema.md
  commands/                      # one file per Command
    deploy.md
    git/
      commit.md                  # → "git:commit" (namespaced)
  hooks/                         # one file per Hook
    block-secrets.yaml
  mcp.yaml                       # all MCP servers (project-wide)
  permissions.yaml               # global permissions
  AGENTS.md                      # root Scope (Path: "", Cascade)
  src/billing/                   # scoped subtree
    AGENTS.md                    # Scope (Path: "src/billing")
    permissions.yaml             # scoped Permissions
    skills/                      # scoped Skills
      reconcile-charges/
        SKILL.md
  packages.yaml                  # bookkeeping for `prism add`
```

Rules:
- The `.agents/` directory is the source of truth. Every other tree
  prism touches is generated.
- One file per Agent (`agents/<name>.md`), one directory per Skill
  (`skills/<name>/SKILL.md`), one file per Command
  (`commands/<name>.md`), one file per Hook (`hooks/<name>.yaml`).
- Multiple MCP servers live in a single `mcp.yaml`. Multiple
  permissions lists live in a single `permissions.yaml`. (Scoped
  blocks live under their subdirectory.)
- Scope files (`AGENTS.md`) sit at the directory they cascade over.
- Scoped Skills, Commands, and Agents live under the scope's
  subdirectory (e.g. `src/billing/skills/reconcile-charges/`).
- `packages.yaml` is bookkeeping for `prism add`-installed registry
  packages; not authored by hand.

#### 3.1.1 `agents.config.yaml` schema

The project's entry-point config. Required at the top of every prism
project. Loaded by the parser before any other file.

```go
type ProjectConfig struct {
    // SchemaVersion declares which canonical schema major version this
    // project authors against. Required. v0.9.0 only reads `2`.
    // Forward-incompat (e.g. `3` on a v0.9 binary) is a hard error
    // with an upgrade message (see §8.1).
    SchemaVersion int `yaml:"schema_version"`

    // Targets is the explicit list of plugin names to project to.
    // When empty/omitted, prism autodetects based on which target
    // toolchains it sees on disk (e.g. `.claude/` directory present
    // → claude target). Names MUST match registered plugin names.
    Targets []string `yaml:"targets,omitempty"`

    // TargetOptions provides per-plugin overrides keyed by plugin
    // name. Values are plugin-specific (sync mode, disabled flag,
    // etc.); see each plugin's docs.
    TargetOptions map[string]TargetOption `yaml:"target_options,omitempty"`

    // Include lists additional `.agents/`-style trees to layer on top
    // of this project (e.g. `~/.agents/` for personal config). Paths
    // resolved relative to the project root; `~` is expanded. Later
    // entries shadow earlier ones for collision resolution.
    Include []string `yaml:"include,omitempty"`

    // Extensions namespace plugin-specific project-wide config that
    // doesn't belong on individual primitives. Same K8s-annotation
    // pattern as primitive-level extensions (§5.1).
    Extensions map[string]any `yaml:"extensions,omitempty"`
}

type TargetOption struct {
    // Disabled skips this plugin even if it would otherwise emit.
    Disabled bool `yaml:"disabled,omitempty"`

    // Mode overrides the default sync mode for the plugin: "write"
    // forces content writes where the plugin would normally symlink.
    // Recognized values: "" (default), "write", "symlink".
    Mode string `yaml:"mode,omitempty"`

    // Extensions for plugin-specific target options.
    Extensions map[string]any `yaml:"extensions,omitempty"`
}
```

Worked example:

```yaml
# .agents/agents.config.yaml
schema_version: 2
targets: [claude, cursor, gemini]
target_options:
  cursor:
    mode: write          # force write instead of symlink
  agentsmd:
    disabled: true       # skip even though .agents/AGENTS.md exists
include:
  - ~/.agents            # personal layer
  - vendor/team-defaults
extensions:
  myorg:
    org_id: acme-corp    # passed through to plugins that read myorg.* namespace
```

Validation (see §5.4):
- `schema_version` required; missing or non-integer → error.
- `targets` entries that don't match a registered plugin → error
  with a "did you mean …?" suggestion.
- `include` paths that don't exist or aren't `.agents/`-shaped trees
  → warning; the project compiles without that layer.
- `extensions.<namespace>` for an unregistered plugin → warning.

### 3.2 Frontmatter conventions

All primitives that carry frontmatter use YAML, delimited by `---\n` on
its own line at file start and at the end of the frontmatter block.
CRLF is normalized to LF at parse time.

```yaml
---
name: stripe-webhook
description: |
  Verify Stripe webhook signatures and parse event payloads.
  Use whenever the agent is handling /api/webhooks/stripe traffic.
activation:
  modes: [glob, model_decision]
  globs:
    - app/webhooks/stripe/**
    - tests/webhooks/stripe/**
extensions:
  claude:
    effort: high
---

# Stripe webhook handling

Body content (markdown) begins here.
```

Rules:
- `name:` is REQUIRED at the top of every primitive that has one (Agent,
  Skill, Command). Filename-derivation is a fallback that emits a
  parser warning suggesting the user add an explicit name.
- `description:` is REQUIRED whenever the primitive's activation
  includes ModelDecision (Skill, Scope) or whenever the primitive is
  meant to be model-invocable (Agent, Command). Multi-line is via YAML
  block scalar `|`.
- Lists in canonical sources use YAML block-sequence form:
  ```yaml
  globs:
    - src/**
    - docs/**
  ```
  Flow form (`[src/**, docs/**]`) is permitted but the canonical
  serializer emits block form for stable diffs.
- Scalar quoting follows YAML rules: unquoted unless the scalar
  contains YAML-significant characters (`:`, leading `-`, `[`, `{`,
  `#`, `&`, `*`, `!`, `|`, `>`, `'`, `"`, `%`, `@`, space). When
  quoting is needed, prefer single quotes.
- TOML and JSON frontmatter are REJECTED. A leading `+++` or `{`
  produces a parser error pointing at the YAML convention.
- Unknown top-level frontmatter keys outside `extensions:` produce a
  warning at parse time ("did you mean `extensions.<plugin>.<key>`?").

### 3.3 Scoping rules

The `.agents/<subdir>/...` layout determines the primitive's `ScopePath`.
A file at `.agents/skills/foo/SKILL.md` has `ScopePath: ""`. A file at
`.agents/src/billing/skills/foo/SKILL.md` has `ScopePath: "src/billing"`.

Scope paths MUST be project-relative and MUST NOT escape the project
root via `..` segments. The validator rejects scopes whose path begins
with `/`, `~`, or `..`.

When a primitive's `Activation` semantics make `ScopePath` irrelevant
(e.g. a `Scope` with `Activation: Always` placed under `src/billing/`),
the validator emits a warning suggesting that the primitive be hoisted
to the root or its activation changed.

## 4. Primitives

Every primitive subsection follows the same structure: definition,
canonical Go struct (with godoc), worked example with two projected
outputs, per-plugin field-mapping table, per-plugin behavior table for
unsupported fields.

### 4.1 Agent

#### 4.1.1 Definition

An Agent is a named, delegatable persona — a behavior profile the main
agent or the user can invoke by name. Each Agent has at minimum a name,
a description (used by the host's auto-delegation logic), and a system
prompt (the markdown body of the source file). Optional fields control
the agent's tool access, model, turn cap, sub-agents, and many tool-
specific runtime knobs.

Agents differ from Skills: an Agent is the persona running the work; a
Skill is a procedure the persona invokes. Agents differ from Commands:
an Agent is persistent within a session once delegated; a Command is a
single-turn prompt template.

#### 4.1.2 Go struct

```go
// Agent is a named persona the main agent (or the user) can delegate to.
//
// Windsurf and agents-md have no Agent primitive (the field cells under
// those plugins are silent or unsupported). Cline subagents are
// vendor-internal and not user-definable. Continue Assistants are 1:1
// with the host config.yaml; multi-agent projection to Continue emits
// one config file per Agent with the first agent winning the canonical
// path.
type Agent struct {
    // Name is the unique identifier (lowercase ASCII + hyphens).
    // Required. Maps to Claude `name`, Cursor filename, Gemini `name`,
    // Copilot filename, Continue top-level `name`.
    Name string

    // Description is the routing hint used by the host for delegation
    // and picker UIs. Required when the agent is model-invocable.
    Description string

    // SystemPrompt is the agent's behavioral instructions. Rendered into
    // the markdown body of the projected file. Optional; empty bodies
    // are valid but the validator warns if Description is also empty.
    SystemPrompt string

    // Model is the model id, "inherit", or empty.
    Model string

    // ModelFallbacks is tried in order if the primary model is
    // unavailable (Copilot-native; silent on others).
    ModelFallbacks []string

    // Tools is the allowlist of host tools this agent may use.
    // nil means "inherit the session's tools".
    Tools []string

    // DisallowedTools is a denylist applied BEFORE Tools (Claude
    // semantics). Other tools degrade by computing
    // (inherited − DisallowedTools) at emit and writing the result to
    // their own allowlist; the validator warns when both Tools and
    // DisallowedTools are set together for a non-Claude target.
    DisallowedTools []string

    // ReadOnly restricts the agent to read-only operations.
    // Cursor-native; Claude maps to permissionMode:plan; Gemini
    // restricts tools to a known read-only subset and warns; Copilot
    // injects a "must not modify files" sentence into the body and
    // warns.
    ReadOnly *bool

    // Background hints that the agent runs without blocking the
    // session. Claude `background`, Cursor `is_background`. Silent
    // elsewhere.
    Background *bool

    // MaxTurns caps the agent's turn count. Pointer preserves the
    // distinction between 0 (cap) and unset (no cap).
    MaxTurns *int

    // Temperature overrides sampling temperature (0.0–2.0).
    // Gemini-native. Continue attaches to the first chat-role model's
    // defaultCompletionOptions.temperature (if a chat model exists;
    // otherwise warns).
    Temperature *float64

    // MCPServers are the MCP servers this agent has access to. Each
    // MCPServerRef is either a name reference (resolved against
    // Project.MCP) or an inline server definition.
    MCPServers []MCPServerRef

    // AllowedSubagents lists which other agents this one may spawn.
    // nil = inherit (all); empty slice = none.
    // Claude expresses via `tools: Agent(a,b)`; Copilot has
    // first-class `agents:`. Others drop with warning.
    AllowedSubagents []string

    // UserInvocable controls whether the user can invoke this agent
    // directly from the picker. Default true (when nil).
    UserInvocable *bool

    // ModelInvocable controls whether the host LLM can auto-select
    // this agent. Default true (when nil). Copilot expresses via
    // `disable-model-invocation` (inverted polarity).
    ModelInvocable *bool

    // InitialPrompt is auto-submitted as the first turn when the
    // agent is run as a main session. Claude-native; unsupported
    // elsewhere.
    InitialPrompt string

    // ScopePath is the .agents/-relative scope this agent belongs to.
    // Empty = global. Plugins emit scoped agents using a
    // <scopeSlug>-<name> prefix; degraded since no plugin supports
    // true per-scope agents.
    ScopePath string

    // Document carries the parsed markdown source. SystemPrompt is
    // typically Document.Body; the field exists separately so plugins
    // that synthesize prompts (e.g. injecting "read-only" sentences)
    // can do so without mutating the source.
    Document *Document

    // Extensions is plugin-namespaced opaque pass-through. See §5.1.
    Extensions map[string]any
}

// MCPServerRef refers to an MCP server, either by name or inline.
type MCPServerRef struct {
    // Name resolves against Project.MCP. Mutually exclusive with Inline.
    Name string
    // Inline defines an MCP server entirely on this Agent.
    // Mutually exclusive with Name.
    Inline *MCPServer
}
```

#### 4.1.3 Worked example

Source `.agents/agents/code-reviewer.md`:

```markdown
---
name: code-reviewer
description: Review pull requests for security, correctness, and style.
model: inherit
tools:
  - Read
  - Grep
  - Glob
read_only: true
allowed_subagents:
  - security-auditor
extensions:
  claude:
    effort: high
    permissionMode: plan
  cursor:
    is_background: false
---

You are a senior code reviewer. Read the diff carefully. Flag:
- security issues (input validation, secret leakage, authz holes)
- correctness issues (off-by-one, race conditions, error handling)
- style issues (last priority — only call out when egregious)

Cite line numbers. Be specific. Trust nothing.
```

Projected to `.claude/agents/code-reviewer.md`:

```markdown
---
name: code-reviewer
description: Review pull requests for security, correctness, and style.
model: inherit
tools: [Read, Grep, Glob, Agent(security-auditor)]
permissionMode: plan
effort: high
---

You are a senior code reviewer. Read the diff carefully. Flag:
- security issues (input validation, secret leakage, authz holes)
- correctness issues (off-by-one, race conditions, error handling)
- style issues (last priority — only call out when egregious)

Cite line numbers. Be specific. Trust nothing.
```

Projected to `.cursor/agents/code-reviewer.md`:

```markdown
---
name: code-reviewer
description: Review pull requests for security, correctness, and style.
model: inherit
readonly: true
is_background: false
---

You are a senior code reviewer. Read the diff carefully. Flag:
- security issues (input validation, secret leakage, authz holes)
- correctness issues (off-by-one, race conditions, error handling)
- style issues (last priority — only call out when egregious)

Cite line numbers. Be specific. Trust nothing.
```

Notes on the differences:
- Claude folds `read_only: true` into `permissionMode: plan` via the
  extensions block (`extensions.claude.permissionMode` wins because
  the user explicitly set it).
- Claude expresses `allowed_subagents` via the `Agent(...)` syntax
  inside `tools:`.
- Cursor drops `tools:` (silent — Cursor agents don't carry a tool
  allowlist); drops `allowed_subagents:` (unsupported, warns once);
  honors `read_only` and `is_background` natively.

#### 4.1.4 Per-plugin field-mapping table

| Canonical field    | Claude                          | Cursor              | Gemini                       | Copilot                      |
|--------------------|---------------------------------|---------------------|------------------------------|------------------------------|
| Name               | `name`                          | `name` (or filename)| `name`                       | `name` (or filename)         |
| Description        | `description`                   | `description`       | `description`                | `description`                |
| SystemPrompt       | body                            | body                | body                         | body                         |
| Model              | `model`                         | `model`             | `model`                      | `model`                      |
| ModelFallbacks     | silent                          | silent              | silent                       | `model` array                |
| Tools              | `tools:` list                   | silent              | `tools:` list                | `tools:` list                |
| DisallowedTools    | `disallowedTools:`              | degraded (compute)  | degraded (compute)           | degraded (compute)           |
| ReadOnly           | `permissionMode: plan` (degraded)| `readonly: true`   | restrict tools (degraded)    | inject body sentence (degraded)|
| Background         | `background: true`              | `is_background: true`| silent                     | silent                       |
| MaxTurns           | `maxTurns:`                     | silent              | `max_turns:`                 | silent                       |
| Temperature        | silent                          | silent              | `temperature:`               | silent                       |
| MCPServers         | `mcpServers:`                   | unsupported (warn)  | `mcpServers:`                | `mcp-servers:`               |
| AllowedSubagents   | `tools: Agent(...)`             | unsupported (warn)  | unsupported (warn)           | `agents:`                    |
| UserInvocable      | non-scanned dir (lossy, warn)   | silent              | silent                       | `user-invocable:`            |
| ModelInvocable     | `permissions.deny` rule (warn)  | silent              | silent                       | `disable-model-invocation:` (inverted) |
| InitialPrompt      | `initialPrompt:`                | unsupported (warn)  | unsupported (warn)           | unsupported (warn)           |
| ScopePath          | `<slug>-<name>` prefix (degraded)| `<slug>-<name>` (degraded)| `<slug>-<name>` (degraded)| `<slug>-<name>` (degraded) |

Plugins that have no Agent primitive at all are `cline`, `windsurf`, and
`agentsmd`. Each emits one info warning per Agent in the project and
drops the projection.

**Cursor cross-emit policy.** Cursor reads `.claude/agents/` directly
when present, in addition to its own `.cursor/agents/`. prism takes
advantage of this: when both `claude` and `cursor` targets are enabled,
**Cursor only writes to `.cursor/agents/`** rather than double-emitting
the same agent to both locations. The `cursor` plugin emits **one info
warning per project** (not per agent) when this de-duplication kicks
in, so users understand why their Cursor agents directory looks lighter
than expected.

`continue` projects each Agent to `.continue/agents/<name>.yaml`
(project-local; matches §7.2's "all tracked files live under target plugin
trees" invariant). Continue's runtime currently selects one "active"
assistant via its own config; users wire that selection up out-of-band.
Future work (v0.10+, see §11) tracks the full Continue assistant
composition model (rules + models + tools).

#### 4.1.5 Per-plugin behavior for unsupported fields

When a plugin's `FieldCapabilities.Fields[<field>]` is `Unsupported`, the
plugin's `Plan()` MUST emit a `Warning` with `Severity: "warn"` and a
`Message` naming the dropped field. When the cell is `Silent`, the field
is dropped without warning (the field is semantically meaningless on
that target).

The full per-field × per-plugin matrix lives in §12 (appendix).

### 4.2 Skill

#### 4.2.1 Definition

A Skill is a reusable named capability: a markdown how-to document plus
optional bundled scripts and references, whose frontmatter declares
when the skill becomes active. Activation can combine modes (glob +
model-decision + manual coexist legitimately). Bodies use prism's
canonical substitution syntax (`{{arg:NAME}}`, `{{shell:CMD}}`,
`{{file:PATH}}`) which plugins translate to native syntax at emit.

A Skill differs from a Command in three ways: file shape (Skill is a
directory; Command is a file), invocation model (Skill is
activation-polymorphic; Command is manual-first slash), and lifecycle
(Skill persists in context once invoked; Command is single-turn).

A Skill differs from a Scope in two ways: scope is
filesystem-cascaded; Skill is glob/description/manual activation, and
Skills are procedures while Scopes are guidance.

#### 4.2.2 Go struct

```go
// Skill is a reusable activation-gated procedure. Bodies are markdown;
// frontmatter is the union of every supported tool's skill / rule /
// prompt / workflow schema, projected per-target.
type Skill struct {
    // Name is the canonical identifier (lowercase ASCII + hyphens,
    // max 64 chars to satisfy Claude's cap). Required.
    Name string

    // Description is the model's auto-selection hinge — what + when,
    // one paragraph. Required whenever Activation.Modes includes
    // ModelDecision (validator enforces).
    Description string

    // WhenToUse is supplementary trigger guidance (example phrases,
    // negative cases). Claude-native; degrades to description suffix
    // on tools that don't have a second field.
    WhenToUse string

    // Activation declares how the skill becomes active. See §4.2.4.
    Activation SkillActivation

    // AllowedTools pre-approves a tool subset while the skill is
    // active. Empty = inherit session permissions.
    AllowedTools []string

    // Arguments declares named positional arguments for {{arg:name}}
    // substitution in the body. Order is significant.
    Arguments []SkillArgument

    // Scripts lists bundled executables (relative paths inside the
    // skill directory). Plugins that support skill directories
    // materialize these; flat-file plugins drop them with an info
    // warning.
    Scripts []string

    // References lists bundled supporting markdown files (relative
    // paths). Lazy-loaded by the host tool when the body links to them.
    References []string

    // Model overrides the session model for the skill's invocation.
    // "inherit" is also valid. Empty inherits.
    Model string

    // Subagent runs the skill in a forked subagent context with the
    // named agent type. Maps to Claude's context:fork + agent:<type>;
    // Copilot maps to its `agent:` field. Others drop with warning.
    Subagent string

    // ScopePath is the .agents/-relative scope this skill belongs to.
    ScopePath string

    // Document carries the markdown body with {{arg:name}} placeholders
    // unresolved (plugins resolve to their native syntax at emit).
    Document *Document

    // Extensions is plugin-namespaced opaque pass-through. See §5.1.
    Extensions map[string]any
}

// SkillArgument names one positional argument.
type SkillArgument struct {
    Name        string
    Description string
    Required    bool
}
```

#### 4.2.3 Worked example

Source `.agents/skills/stripe-webhook/SKILL.md`:

```markdown
---
name: stripe-webhook
description: |
  Verify Stripe webhook signatures and parse event payloads.
  Use whenever the agent is handling /api/webhooks/stripe traffic.
activation:
  modes: [glob, model_decision]
  globs:
    - app/webhooks/stripe/**
    - tests/webhooks/stripe/**
allowed_tools:
  - Bash(./scripts/verify-signature.sh)
  - Read
  - Edit
arguments:
  - name: event_type
    required: false
scripts:
  - scripts/verify-signature.sh
references:
  - references/webhook-schema.md
---

# Stripe webhook handling

To verify a webhook signature for event type `{{arg:event_type}}`:

1. Read the request body and Stripe-Signature header.
2. Run `./scripts/verify-signature.sh` with the body as stdin.
3. Match against schema in [webhook-schema](references/webhook-schema.md).
```

Projected to `.claude/skills/stripe-webhook/SKILL.md`:

```markdown
---
name: stripe-webhook
description: |
  Verify Stripe webhook signatures and parse event payloads.
  Use whenever the agent is handling /api/webhooks/stripe traffic.
paths:
  - app/webhooks/stripe/**
  - tests/webhooks/stripe/**
allowed-tools:
  - Bash(./scripts/verify-signature.sh)
  - Read
  - Edit
arguments: [event_type]
---

# Stripe webhook handling

To verify a webhook signature for event type `$event_type`:

1. Read the request body and Stripe-Signature header.
2. Run `./scripts/verify-signature.sh` with the body as stdin.
3. Match against schema in [webhook-schema](references/webhook-schema.md).
```

Plus the bundled `.claude/skills/stripe-webhook/scripts/verify-signature.sh`
and `.claude/skills/stripe-webhook/references/webhook-schema.md`.

Projected to `.cursor/rules/stripe-webhook/RULE.md`:

```markdown
---
description: |
  Verify Stripe webhook signatures and parse event payloads.
  Use whenever the agent is handling /api/webhooks/stripe traffic.
globs: [app/webhooks/stripe/**, tests/webhooks/stripe/**]
alwaysApply: false
---

# Stripe webhook handling

To verify a webhook signature for event type ``:

1. Read the request body and Stripe-Signature header.
2. Run `./scripts/verify-signature.sh` with the body as stdin.
3. Match against schema in references/webhook-schema.md.
```

Notes:
- Cursor has no per-skill argument substitution; `{{arg:event_type}}`
  becomes empty with one info warning. Authors targeting Cursor
  should rewrite to expect the value in follow-up chat.
- Cursor drops `allowed_tools` and `scripts` with warnings; references
  are inlined as plain markdown.

Projected to `.gemini/skills/stripe-webhook/SKILL.md`:

```markdown
---
name: stripe-webhook
description: |
  Verify Stripe webhook signatures and parse event payloads.
  Use whenever the agent is handling /api/webhooks/stripe traffic.
---

# Stripe webhook handling

To verify a webhook signature for event type `{{args}}`:

1. Read the request body and Stripe-Signature header.
2. Run `./scripts/verify-signature.sh` with the body as stdin.
3. Match against schema in [webhook-schema](references/webhook-schema.md).
```

Plus the bundled scripts/ and references/. Gemini drops `globs` (info
warning — Gemini skills are description-activated), drops
`allowed_tools` (warning — Gemini's `trust:true` is the coarsest
approximation), and rewrites `{{arg:event_type}}` to `{{args}}` (the
sole supported substitution).

#### 4.2.4 SkillActivation

```go
// SkillActivation is polymorphic. Any combination of Modes is legal;
// empty Modes implies {ModelDecision} (the universal default).
type SkillActivation struct {
    // Modes is the set of triggers under which the skill becomes
    // active. Tools that can express only one mode get the first
    // compatible mode honored.
    Modes []SkillActivationMode

    // Globs activates the skill when the agent is working on files
    // matching these patterns. Required if Modes includes Glob.
    Globs []string

    // ContentRegex activates the skill when in-context file contents
    // match the regex. Continue-native; silent elsewhere (the regex
    // carries no semantic meaning to other tools — silent, not
    // warning).
    ContentRegex string

    // UserInvocable lets a human type /name to invoke. Default true.
    // Setting false maps to Claude `user-invocable: false`.
    UserInvocable *bool

    // ModelInvocable lets the LLM auto-select the skill. Default true.
    // Setting false maps to Claude `disable-model-invocation: true`.
    ModelInvocable *bool
}

type SkillActivationMode string

const (
    // SkillActivationAlways injects the body every turn. Use sparingly
    // — context cost is per-turn. (Cursor `alwaysApply: true`,
    // Windsurf `trigger: always_on`.)
    SkillActivationAlways SkillActivationMode = "always"

    // SkillActivationModelDecision lets the LLM choose based on the
    // description. The universal default.
    SkillActivationModelDecision SkillActivationMode = "model_decision"

    // SkillActivationGlob activates when files matching Globs are in
    // context. Requires Globs to be non-empty.
    SkillActivationGlob SkillActivationMode = "glob"

    // SkillActivationManual requires explicit /name invocation.
    SkillActivationManual SkillActivationMode = "manual"
)
```

When `Activation.Modes` is empty, the validator treats it as
`[ModelDecision]`. Plugins that can only express one mode pick the
most permissive available (Always > Glob > ModelDecision > Manual).

#### 4.2.5 Per-plugin field-mapping table (skill)

| Canonical field           | Claude              | Cursor                  | Gemini             | Copilot                    | Cline                  | Continue              | Windsurf               |
|---------------------------|---------------------|-------------------------|--------------------|----------------------------|------------------------|-----------------------|------------------------|
| Name                      | dir name + `name:`  | dir name                | dir name + `name:` | dir name + `name:`         | filename               | `name:`               | filename               |
| Description               | `description:`      | `description:`          | `description:`    | `description:`             | (frontmatter, unsurfaced)| `description:`     | `description:` (req for model_decision)|
| WhenToUse                 | `when_to_use:`      | appended to description | appended to description| appended to description| appended to description| appended to description| appended to description|
| Activation.Always         | (no native; body persists)| `alwaysApply: true`| unsupported (warn)| `applyTo: "**"`        | (no frontmatter)       | `alwaysApply: true`   | `trigger: always_on`   |
| Activation.ModelDecision  | (default)           | (default)               | (default)         | (default)                  | unsupported (warn)     | (default; no globs)   | `trigger: model_decision`|
| Activation.Glob           | `paths:`            | `globs:`                | unsupported (warn)| `applyTo:`                 | `paths:`               | `globs:`              | `trigger: glob` + `globs:`|
| Activation.Manual         | `disable-model-invocation:`| (no globs/desc)| TOML command file| `.prompt.md`               | workflow file          | `invokable: true`     | `trigger: manual`      |
| Activation.Globs          | `paths:`            | `globs:`                | unsupported (warn)| `applyTo:`                 | `paths:`               | `globs:`              | `globs:`               |
| Activation.ContentRegex   | unsupported (warn)  | unsupported (warn)      | unsupported (warn)| unsupported (warn)         | unsupported (warn)     | `regex:`              | unsupported (warn)     |
| Activation.UserInvocable  | `user-invocable:`   | silent                  | silent            | silent                     | toggle UI (degraded)   | silent                | silent                 |
| Activation.ModelInvocable | `disable-model-invocation:`| silent          | silent            | silent                     | silent                 | silent                | silent                 |
| AllowedTools              | `allowed-tools:`    | (no tool field; warn)   | unsupported (warn)| `tools:`                   | unsupported (warn)     | unsupported (warn)    | unsupported (warn)     |
| Arguments                 | `arguments: [list]` | inline text fallback (degraded)| `{{args}}` placeholder (degraded; full string only)| `${input:NAME}` substitutions| degraded| Handlebars (degraded)| degraded |
| Scripts                   | `scripts/` dir      | `scripts/` dir          | `scripts/` dir    | `scripts/` dir             | unsupported (warn)     | unsupported (warn)    | unsupported (warn)     |
| References                | `references/` dir   | `references/` dir       | `references/` dir | `references/` dir          | unsupported (warn)     | unsupported (warn)    | unsupported (warn)     |
| Model                     | `model:`            | unsupported (warn)      | unsupported (warn)| `model:`                   | unsupported (warn)     | unsupported (warn)    | unsupported (warn)     |
| Subagent                  | `context: fork` + `agent:`| inline persona (degraded)| unsupported (warn)| `agent:` ref            | unsupported (warn)     | unsupported (warn)    | unsupported (warn)     |

`agentsmd` is the eighth column; for every Skill field the cell is
silent: AGENTS.md has no skill primitive, and warning per-field per-skill
would be deafening noise.

### 4.3 Command

#### 4.3.1 Definition

A Command is a reusable, user-invocable prompt template. The user types
`/<name>` (with optional arguments) and the host expands the command's
body, optionally with arguments, shell injections, and file references
substituted in.

Distinct from Skill (see §4.2.1). Distinct from Agent (a Command is a
prompt, not a persona).

#### 4.3.2 Go struct

```go
// Command is a manual-first prompt template.
type Command struct {
    // Name is the slash-command identifier (kebab-case; ':' permitted
    // from subdirectory namespacing — e.g. "git/commit.md" →
    // "git:commit"). Required; source filename is the fallback with a
    // parser warning.
    Name string

    // Description is shown in the picker / autocomplete and used by
    // Claude for auto-invocation. Required for high-quality projection
    // (Gemini, Copilot, Claude all surface it).
    Description string

    // ArgumentHint is the placeholder text shown in autocomplete
    // (e.g. "[issue-number]"). Cosmetic; drops silently on tools that
    // don't render it.
    ArgumentHint string

    // Arguments names positional arguments accessible as {{arg:name}}
    // in the body. Plugins translate to native syntax at emit.
    Arguments []string

    // Model is the model override for this command's invocation.
    Model string

    // Tools is the per-command tools allowlist. Pre-approves these
    // tools during the command's execution (Claude `allowed-tools`,
    // Copilot `tools:` — semantic drift acceptable; both target the
    // closest fit).
    Tools []string

    // Agent names a subagent (from Project.Agents) this command
    // delegates to. The command body becomes the task prompt.
    // Claude-native and Copilot-native; other tools inline the agent's
    // persona as a "## Persona" section at the head of the body and
    // warn. If the resulting body would exceed the target's hard size
    // cap (Windsurf 12 KB) the plugin SKIPS projection entirely.
    Agent string

    // AutoInvoke opts INTO Claude's auto-invocation behavior. Default
    // false (commands are manual-first by convention; Claude is the
    // outlier). When false (the default), the Claude plugin emits
    // `disable-model-invocation: true` to suppress auto-invoke; when
    // true, that field is omitted. Silent on every non-Claude target
    // since they're already manual-first natively.
    AutoInvoke bool

    // ScopePath is the .agents/-relative scope this command belongs to.
    // Plugins use a <scopeSlug>-<name> prefix; degraded because no
    // plugin supports true per-scope commands.
    ScopePath string

    // Document carries the markdown body with canonical macros:
    // {{arg:NAME}}, {{shell:CMD}}, {{file:PATH}}.
    Document *Document

    // Extensions is plugin-namespaced opaque pass-through. See §5.1.
    Extensions map[string]any
}
```

#### 4.3.3 Worked example

Source `.agents/commands/deploy.md`:

```markdown
---
name: deploy
description: Deploy the current branch to production via the deploy script.
argument_hint: "[environment]"
arguments:
  - environment
tools:
  - Bash(./scripts/deploy.sh)
  - Bash(git status)
manual_only: true
---

Deploy the current branch to `{{arg:environment}}`.

Current branch: {{shell:git rev-parse --abbrev-ref HEAD}}
Last commit: {{shell:git log -1 --oneline}}

Proceed by running `./scripts/deploy.sh {{arg:environment}}`.
```

Projected to `.claude/commands/deploy.md`:

```markdown
---
description: Deploy the current branch to production via the deploy script.
argument-hint: "[environment]"
arguments: [environment]
allowed-tools:
  - Bash(./scripts/deploy.sh)
  - Bash(git status)
disable-model-invocation: true
---

Deploy the current branch to `$environment`.

Current branch: !`git rev-parse --abbrev-ref HEAD`
Last commit: !`git log -1 --oneline`

Proceed by running `./scripts/deploy.sh $environment`.
```

Projected to `.gemini/commands/deploy.toml`:

```toml
description = "Deploy the current branch to production via the deploy script."
prompt = """
Deploy the current branch to `{{args}}`.

Current branch: !{git rev-parse --abbrev-ref HEAD}
Last commit: !{git log -1 --oneline}

Proceed by running `./scripts/deploy.sh {{args}}`.
"""
```

Gemini drops `tools:` (warning) and `argument-hint` (silent). Gemini's
single-placeholder `{{args}}` model means named arguments collapse;
the plugin emits one info warning per command when more than one
named argument is declared.

#### 4.3.4 Per-plugin field-mapping table (command)

| Canonical field | Claude                       | Cursor                          | Gemini                              | Copilot              | Cline                  | Continue              | Windsurf               |
|-----------------|------------------------------|---------------------------------|-------------------------------------|----------------------|------------------------|-----------------------|------------------------|
| Name            | filename / subdir-namespaced | filename                        | filename / subdir-namespaced        | filename             | filename + `.md`       | `name:`               | filename               |
| Description     | `description:`               | drop (no frontmatter)           | `description =`                     | `description:`       | frontmatter (unsurfaced)| `description:`       | drop (warn)            |
| ArgumentHint    | `argument-hint:`             | silent                          | silent                              | `argument-hint:`     | silent                 | silent                | silent                 |
| Arguments       | `arguments:` + `$name`       | inline text fallback (degraded) | `{{args}}` (degraded, collapsed)   | `${input:NAME}`      | inline text (degraded) | Handlebars (degraded) | inline text (degraded) |
| Model           | `model:`                     | drop (warn)                     | drop (warn)                         | `model:`             | drop (warn)            | preamble              | drop (warn)            |
| Tools           | `allowed-tools:`             | drop (warn)                     | drop (warn) + body hint             | `tools:`             | drop (warn)            | drop (warn)           | drop (warn)            |
| Agent           | `context: fork` + `agent:`   | inline persona (degraded, warn) | drop (warn)                         | `agent:` ref         | inline persona (degraded, warn)| drop (warn)   | inline persona (degraded, warn) |
| AutoInvoke      | omits `disable-model-invocation`| silent                       | silent                              | silent               | silent                 | silent                | silent                 |
| ScopePath       | `<slug>-<name>` (degraded)   | `<slug>-<name>` (degraded)      | `<slug>-<name>` (degraded)          | `<slug>-<name>` (degraded)| `<slug>-<name>` (degraded)| `<slug>-<name>` (degraded)| `<slug>-<name>` (degraded)|

`agentsmd` projects commands as a `## Commands` documentation section in
the root AGENTS.md — informational only (AGENTS.md has no invocation
surface). Each command produces one bullet with name + description.

#### 4.3.5 Subdirectory namespacing

`commands/git/commit.md` produces:
- Claude / Gemini: native `git:commit` (the slash → colon convention).
- Cursor / Copilot / Cline / Continue / Windsurf: flattened to
  `git_commit.md` with one info warning per project.

The underscore is chosen as the flattener because it is the only ASCII
non-letter character safe across every target's filename grammar.

### 4.4 Hook

#### 4.4.1 Definition

A Hook is a lifecycle event handler — a script (or HTTP endpoint, MCP
tool, prompt, or agent) that fires when the host emits a canonical
event (`pre_tool_use`, `session_start`, `post_file_edit`, etc.). Hooks
can optionally block the agent, modify tool input, inject context,
or just observe.

prism canonicalizes events at the **per-action level** (`pre_shell`,
`pre_file_read`, `post_file_edit`, `pre_mcp_call`) AS WELL AS the
**generic level** (`pre_tool_use`, `post_tool_use`). The per-action
events are first-class so Windsurf (which has no umbrella
`pre_tool_use`) can express them natively; plugins for tools with the
generic event translate per-action canonical events to generic +
matcher at emit.

#### 4.4.2 Go struct

```go
// Hook is a callback fired on a lifecycle event. Events are canonical
// strings; matcher and handler shapes converge on Claude/Continue
// (which share a verbatim schema) with degradations elsewhere.
type Hook struct {
    // Name is an optional friendly identifier. Gemini surfaces it;
    // others derive from filename or event name.
    Name string

    // Description is free-text purpose. Gemini-surface; documentation
    // value on other tools.
    Description string

    // Event is the canonical event name. See HookEvent constants.
    // Required.
    Event HookEvent

    // Matcher decides whether the handler fires for a given event
    // payload. What it matches against is event-specific (tool name
    // for PreToolUse, agent name for SubagentStart, trigger for
    // PreCompact, etc.).
    Matcher HookMatcher

    // Handlers fire when the event + matcher both hit.
    Handlers []HookHandler

    // Sequential runs handlers one-at-a-time. Pointer-typed so an
    // omitted YAML field (nil) is distinguishable from explicit false.
    // Validator treats nil as "use the target's default" — which is
    // sequential=true on every tool today; Gemini is the only one that
    // ever flips it to false. Plugins that don't expose the knob emit
    // SupportSilent for this field.
    Sequential *bool

    // Disabled skips emission entirely on every plugin. Universal.
    Disabled bool

    // ScopePath is the .agents/-relative scope this hook belongs to.
    // Plugins in the perms-guard / scope-guard wrapper family
    // (Gemini, Copilot, Cline) emit per-scope wrappers; others fold
    // scoped hooks into the global block with one info warning per
    // scope.
    ScopePath string

    // Extensions is plugin-namespaced opaque pass-through. See §5.1.
    Extensions map[string]any
}

// HookEvent is the canonical event name. Spelled "native:<verbatim>"
// for tool-specific events not in the canonical set; prism doesn't
// translate, only the matching plugin emits.
type HookEvent string

const (
    EventSessionStart       HookEvent = "session_start"
    EventSessionEnd         HookEvent = "session_end"
    EventSessionResume      HookEvent = "session_resume"
    EventUserPromptSubmit   HookEvent = "user_prompt_submit"
    EventPreToolUse         HookEvent = "pre_tool_use"
    EventPostToolUse        HookEvent = "post_tool_use"
    EventPostToolUseFailure HookEvent = "post_tool_use_failure"
    EventPermissionRequest  HookEvent = "permission_request"
    EventPreShell           HookEvent = "pre_shell"
    EventPostShell          HookEvent = "post_shell"
    EventPreFileRead        HookEvent = "pre_file_read"
    EventPostFileEdit       HookEvent = "post_file_edit"
    EventPreMCPCall         HookEvent = "pre_mcp_call"
    EventPostMCPCall        HookEvent = "post_mcp_call"
    EventSubagentStart      HookEvent = "subagent_start"
    EventSubagentStop       HookEvent = "subagent_stop"
    EventStop               HookEvent = "stop"
    EventPreCompact         HookEvent = "pre_compact"
    EventPostCompact        HookEvent = "post_compact"
    EventNotification       HookEvent = "notification"
    EventWorktreeCreate     HookEvent = "worktree_create"
    EventWorktreeRemove     HookEvent = "worktree_remove"
    EventTaskCompleted      HookEvent = "task_completed"
    EventConfigChange       HookEvent = "config_change"
    EventError              HookEvent = "error"
)

// HookMatcher selects which event payloads fire the handler.
type HookMatcher struct {
    // Kind is "all", "exact", or "regex".
    Kind string
    // Patterns are exact strings (Kind=="exact"; pipe-separation
    // permitted: "Bash|Edit|Write") or a single regex (Kind=="regex").
    // Empty Patterns with Kind=="all" matches everything.
    Patterns []string
}

// HookHandler is one fired callback.
type HookHandler struct {
    // Kind selects the handler shape. "command" is universal; others
    // degrade per-tool.
    Kind HookHandlerKind

    // TimeoutMs normalises every tool's unit drift to milliseconds.
    // Plugins serialize down (Claude/Cline/Continue/Copilot in
    // seconds; Gemini in ms; Windsurf no limit).
    TimeoutMs int

    // StatusMessage is shown in the spinner (Claude/Continue native).
    StatusMessage string

    // Async fires the hook without waiting (Continue native; Windsurf
    // post-hooks implicit).
    Async bool

    // FailClosed treats handler error as a hard block (Cursor-native).
    // Default false (fail-open) on every other tool.
    FailClosed bool

    // Once fires the handler once per session, then drops (Claude /
    // Continue native).
    Once bool

    // If is a conditional expression (Claude-native; e.g.
    // "Bash(git *)").
    If string

    // Command — set when Kind == HookHandlerCommand.
    Command    string
    Args       []string
    Cwd        string
    Env        map[string]string
    Shell      string // "bash", "powershell", or empty
    Bash       string // platform-override script path (Copilot, Windsurf)
    Powershell string // platform-override script path

    // HTTP — set when Kind == HookHandlerHTTP.
    URL            string
    Headers        map[string]string
    AllowedEnvVars []string

    // MCPTool — set when Kind == HookHandlerMCPTool.
    MCPServer string
    MCPName   string
    MCPInput  map[string]any

    // Prompt / Agent — set when Kind == HookHandlerPrompt or HookHandlerAgent.
    Prompt string
    Model  string
}

type HookHandlerKind string

const (
    HookHandlerCommand HookHandlerKind = "command"
    HookHandlerHTTP    HookHandlerKind = "http"
    HookHandlerMCPTool HookHandlerKind = "mcp_tool"
    HookHandlerPrompt  HookHandlerKind = "prompt"
    HookHandlerAgent   HookHandlerKind = "agent"
)
```

#### 4.4.3 Worked example

Source `.agents/hooks/block-secret-files.yaml`:

```yaml
name: block-secret-files
description: Refuse Edit/Write into .env files.
event: pre_tool_use
matcher:
  kind: exact
  patterns: [Edit, Write, MultiEdit]
handlers:
  - kind: command
    command: "${PROJECT_DIR}/.agents/hooks/scripts/block-secrets.sh"
    timeout_ms: 5000
    fail_closed: true
```

Projected to `.claude/settings.json` (under `hooks` key, merged with
existing settings):

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit",
        "hooks": [
          {
            "type": "command",
            "command": "${CLAUDE_PROJECT_DIR}/.agents/hooks/scripts/block-secrets.sh",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

Note Claude's `timeout` is in seconds; the plugin converts from ms.
Claude does not natively support `fail_closed`; the plugin emits one
info warning per hook with `FailClosed: true` ("FailClosed not honored;
Claude defaults to fail-open for non-block exit codes").

Projected to `.cursor/hooks.json`:

```json
{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {
        "type": "command",
        "command": "${PROJECT_DIR}/.agents/hooks/scripts/block-secrets.sh",
        "matcher": "Edit|Write|MultiEdit",
        "timeout": 5,
        "failClosed": true
      }
    ]
  }
}
```

#### 4.4.4 Per-action canonical event translation

Per-action events translate to (generic + matcher) on tools that lack the
specific event:

| Canonical              | Claude / Continue / Copilot       | Cursor                    | Gemini                                | Cline                          | Windsurf                |
|------------------------|-----------------------------------|---------------------------|---------------------------------------|--------------------------------|-------------------------|
| `pre_shell`            | `PreToolUse + matcher: Bash`      | `beforeShellExecution`    | `BeforeTool + matcher: run_shell_command` | `PreToolUse + matcher: execute_command` | `pre_run_command` |
| `post_shell`           | `PostToolUse + matcher: Bash`     | `afterShellExecution`     | `AfterTool + matcher: run_shell_command`  | `PostToolUse + matcher: execute_command`| `post_run_command` |
| `pre_file_read`        | `PreToolUse + matcher: Read`      | `beforeReadFile`          | `BeforeTool + matcher: read_file`      | `PreToolUse + matcher: read_file`      | `pre_read_code`    |
| `post_file_edit`       | `PostToolUse + matcher: Edit\|Write\|MultiEdit`| `afterFileEdit` | `AfterTool + matcher: write_file`      | `PostToolUse + matcher: edit`          | `post_write_code`  |
| `pre_mcp_call`         | `PreToolUse + matcher: mcp__*`    | `beforeMCPExecution`      | `BeforeTool + matcher: mcp_*`         | `PreToolUse + matcher: mcp_*`         | `pre_mcp_tool_use` |
| `post_mcp_call`        | `PostToolUse + matcher: mcp__*`   | `afterMCPExecution`       | `AfterTool + matcher: mcp_*`          | `PostToolUse + matcher: mcp_*`        | `post_mcp_tool_use`|

#### 4.4.5 Cline filename dispatch

Cline uses filenames, not JSON, for hook dispatch (per
`.clinerules/hooks/<EventName>`). prism's Cline plugin:
- Writes one executable script per `(event, matcher)` pair.
- When `Matcher.Kind == "exact"` or `Matcher.Kind == "regex"`, inlines
  a guard clause at the top of the script that reads stdin, parses
  `tool_name`, and exits 0 (no-op) if the matcher misses.
- Multiple handlers for the same event become a dispatcher script that
  fans out sequentially.

### 4.5 MCPServer

#### 4.5.1 Definition

An MCPServer is a Model Context Protocol server configuration. Transport
is required and explicit (no inference at the canonical layer — every
plugin emits the right wire spelling at projection).

#### 4.5.2 Go struct

```go
// MCPServer is one MCP server configuration.
type MCPServer struct {
    // Name is the server identifier; map key in most tools' wire
    // formats.
    Name string

    // Transport is one of "stdio", "http", "sse". REQUIRED. Plugins
    // map to their native wire spelling (Cline `streamableHttp`,
    // Continue `streamable-http`, Gemini `httpUrl`-vs-`url`, etc.).
    Transport string

    // stdio fields — set when Transport == "stdio".
    Command string
    Args    []string
    Env     map[string]string
    Cwd     string // honored by Gemini and Continue; silent elsewhere

    // http / sse fields — set when Transport == "http" or "sse".
    URL     string
    Headers map[string]string

    // Auth carries static auth declarations. OAuth is informational
    // only (no projector materializes it; the field exists so prism
    // can warn at compile time and the user configures OAuth in the
    // tool's UI).
    Auth *MCPAuth

    // Policy fields. Plugins MAY honor; plugins without support emit
    // an info warning at compile time when the field is set.
    // Disabled is honored UNIVERSALLY by skipping emit (no warning).
    TimeoutMs    int
    Disabled     bool
    AutoApprove  []string
    Trust        bool
    IncludeTools []string
    ExcludeTools []string

    // ScopePath is source attribution only — no plugin materializes
    // per-scope MCP. Preserved for lockfile / `prism which`.
    ScopePath string

    // Extensions is plugin-namespaced opaque pass-through. See §5.1.
    Extensions map[string]any
}

// MCPAuth carries static auth declarations.
type MCPAuth struct {
    // Scheme is "none", "bearer", "header", or "oauth".
    // "oauth" emits an info warning ("configure OAuth in <tool> UI").
    Scheme string

    // Token holds the bearer token (typically a "${env:VAR}"
    // reference). Used when Scheme == "bearer".
    Token string

    // Headers are arbitrary headers merged into MCPServer.Headers.
    // Used when Scheme == "header".
    Headers map[string]string
}
```

#### 4.5.3 Variable substitution

prism defines two canonical substitution forms that plugins rewrite to
their target's native syntax at emit time. **prism NEVER evaluates
substitutions at compile time** — all evaluation happens at the host
tool's runtime.

**`${env:VAR}` — environment variables** (used in MCP `command`, `args`,
`env`, `url`, `headers`, and hook `command`, `args`, `cwd`):

Per-plugin rewrite at emit:
- Claude, Gemini, Cline: `${env:VAR}` → `${VAR}`
- Cursor, Copilot, Windsurf: passes through unchanged
- Continue: `${env:VAR}` → `${{ secrets.VAR }}` PLUS one info warning
  per server ("value must live in Continue secrets store, not just
  shell env")

Bare `${VAR}` in user input is treated as a literal; the validator
suggests `${env:VAR}` form.

**`${project_dir}` — the project root** (used in hook `command`, `args`,
`cwd` and any other path field where the file lives under the project
tree):

Per-plugin rewrite at emit:
- Claude: `${project_dir}` → `${CLAUDE_PROJECT_DIR}` (Claude's native
  hook variable)
- Cursor, Gemini, Cline, Continue, Windsurf, Copilot: emit a wrapper
  script that resolves `${PROJECT_DIR}` at runtime from
  `${BASH_SOURCE[0]}` (the same scope-guard / perms-guard pattern
  from v0.7.1). The native hook command points at the wrapper, not at
  the user's script directly.

This is the canonical way to write hook scripts that survive `mv` of the
project. Plugins MAY emit a warning if `${project_dir}` appears in a
field where they can't provide runtime resolution (rare; document on
the relevant primitive).

Command bodies (`{{arg:NAME}}`, `{{shell:CMD}}`, `{{file:PATH}}`,
`{{args}}`) have their own canonical substitution forms — see §4.3.2.
They are evaluated by the host tool, NOT by prism.

#### 4.5.4 Known upstream bugs (emitted as warnings)

The Validator and each plugin emit info warnings citing the upstream
issue number when a known-broken path is exercised:

- Claude `headers` with `${env:VAR}` substitution: "anthropics/claude-code#6204, #51581"
- Cursor `headers` for remote servers: "Cursor forum Q1 2026 — header substitution unreliable"
- Cline host-env in remote launcher: "cline/cline#3129"
- Continue SSE in YAML config: "continuedev/continue#5359 — SSE not fully supported"

Warnings decay as upstream issues close. Plugins MAY check
`prism.dev/upstream-bugs.json` (or a similar registry) and skip the
warning when the upstream is fixed; for v0.9 the warnings are
unconditional.

#### 4.5.5 Worked example

Source `.agents/mcp.yaml`:

```yaml
servers:
  - name: github
    transport: stdio
    command: uvx
    args: [mcp-server-github]
    env:
      GITHUB_TOKEN: ${env:GITHUB_TOKEN}
    timeout_ms: 30000
    auto_approve:
      - list_issues
      - get_pull_request

  - name: linear
    transport: http
    url: https://mcp.linear.app/sse
    headers:
      Authorization: Bearer ${env:LINEAR_API_KEY}
    auth:
      scheme: bearer
      token: ${env:LINEAR_API_KEY}
```

Projected to `.mcp.json` (Claude):

```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "command": "uvx",
      "args": ["mcp-server-github"],
      "env": {
        "GITHUB_TOKEN": "${GITHUB_TOKEN}"
      }
    },
    "linear": {
      "type": "http",
      "url": "https://mcp.linear.app/sse",
      "headers": {
        "Authorization": "Bearer ${LINEAR_API_KEY}"
      }
    }
  }
}
```

Plugin emits one info warning per server with `TimeoutMs` set ("Claude
does not honor per-server timeout; use --request-timeout CLI flag") and
one per server with `AutoApprove` set ("Claude does not honor
auto_approve; use --dangerously-skip-permissions for the whole session
or accept per-call prompts"). Linear's `${env:LINEAR_API_KEY}` in the
headers value emits the known-bug warning per §4.5.4.

Projected to `.cline/cline_mcp_settings.json`:

```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "command": "uvx",
      "args": ["mcp-server-github"],
      "env": {
        "GITHUB_TOKEN": "${GITHUB_TOKEN}"
      },
      "timeout": 30,
      "autoApprove": ["list_issues", "get_pull_request"]
    },
    "linear": {
      "type": "streamableHttp",
      "url": "https://mcp.linear.app/sse",
      "headers": {
        "Authorization": "Bearer ${env:LINEAR_API_KEY}"
      }
    }
  }
}
```

Cline natively honors `timeout` (converted seconds) and `autoApprove`;
no warnings on those fields.

### 4.6 Permissions

#### 4.6.1 Definition

Permissions is the allow / ask / deny policy controlling which tools
and operations the agent may invoke without explicit approval. The
grammar is dimension-aware: rules name a target (tool, file system,
network, MCP) and a pattern.

#### 4.6.2 Go struct + grammar

```go
// Permissions is the canonical allow/ask/deny policy. ScopePath is
// empty for Project.Permissions and non-empty for entries in
// Project.ScopedPermissions.
type Permissions struct {
    Allow []string // canonical rule strings, see grammar
    Ask   []string
    Deny  []string

    ScopePath  string
    Extensions map[string]any
}
```

Grammar:

```
<target>:<pattern>
<target>:<subaction>:<pattern>
```

Recognized targets:

| Target                            | Example                                | Meaning                                       |
|-----------------------------------|----------------------------------------|-----------------------------------------------|
| `bash` (case-insensitive `Bash`)  | `bash:npm test *`                      | shell command prefix                          |
| `Read`, `Write`, `Edit`, `MultiEdit` | `Edit:.env*`                        | file-path match                               |
| `fs`                              | `fs:secrets/**`                        | applies to Read+Write+Edit collectively       |
| `network`                         | `network:github.com`                   | domain or URL allowlist/denylist              |
| `mcp:<server>`                    | `mcp:github:*`                         | any tool from MCP server `github`             |
| `mcp:<server>:<tool>`             | `mcp:github:create_issue`              | specific MCP tool                             |
| `WebFetch` (Claude form)          | `WebFetch(domain:github.com)`          | pass-through                                  |
| Tool only                         | `Bash`                                 | matches any action under tool                 |

Pattern grammar (v0.9 baseline):
- Exact match.
- Trailing `*` (prefix match).
- Recursive `**` (matches across path segments).
- Negation prefix `!` (within an allow list, an entry like
  `Edit:!src/billing/migrations/*` reduces to a deny entry at emit).
- Regex is REJECTED.

Resolution order: **Deny > Allow > Ask > Default**. Matches every
target's runtime; no plugin re-ranks. In non-TTY contexts, the
perms-guard wrapper treats `Ask` as `Deny` for safety.

#### 4.6.3 Worked example

Source `.agents/permissions.yaml`:

```yaml
allow:
  - bash:go test *
  - bash:npm test *
  - network:github.com
  - network:*.npmjs.org
  - fs:src/**
  - fs:!src/billing/migrations/*
  - mcp:github:*

ask:
  - bash:npm install *
  - Edit:.env*
  - mcp:github:delete_repository

deny:
  - bash:rm -rf *
  - bash:git push *
  - fs:.ssh/**
  - network:*
```

Projected to `.claude/settings.json` (under `permissions` key):

```json
{
  "permissions": {
    "allow": [
      "Bash(go test *)",
      "Bash(npm test *)",
      "WebFetch(domain:github.com)",
      "WebFetch(domain:*.npmjs.org)",
      "Edit(src/**)",
      "Read(src/**)",
      "Write(src/**)",
      "mcp__github__*"
    ],
    "ask": [
      "Bash(npm install *)",
      "Edit(.env*)",
      "mcp__github__delete_repository"
    ],
    "deny": [
      "Bash(rm -rf *)",
      "Bash(git push *)",
      "Edit(src/billing/migrations/*)",
      "Read(src/billing/migrations/*)",
      "Write(src/billing/migrations/*)",
      "Edit(.ssh/**)",
      "Read(.ssh/**)",
      "Write(.ssh/**)",
      "WebFetch(domain:*)"
    ]
  }
}
```

Notes on the fan-out:
- `fs:src/**` fans out to three Edit/Read/Write rules.
- `fs:!src/billing/migrations/*` becomes three deny entries (the
  negation expands at emit).
- `network:github.com` translates to Claude's `WebFetch(domain:…)`.
- `mcp:github:*` translates to Claude's `mcp__github__*` pattern.

Projected to `.continue/permissions.yaml`:

```yaml
allow:
  - Bash(go test *)
  - Bash(npm test *)
  - Fetch(github.com)
  - Fetch(*.npmjs.org)
  - Edit(src/**)
  - Read(src/**)
  - Write(src/**)
  - MCP(github:*)

ask:
  - Bash(npm install *)
  - Edit(.env*)
  - MCP(github:delete_repository)

exclude:
  - Bash(rm -rf *)
  - Bash(git push *)
  - Edit(src/billing/migrations/*)
  - Read(src/billing/migrations/*)
  - Write(src/billing/migrations/*)
  - Edit(.ssh/**)
  - Read(.ssh/**)
  - Write(.ssh/**)
  - Fetch(*)
```

Continue uses `exclude` instead of `deny`; the rule grammar otherwise
matches.

Projected to `.gemini/hooks/__perms-guard__/policy.json`:

```json
{
  "allow": [
    "bash:go test *",
    "bash:npm test *",
    "network:github.com",
    "network:*.npmjs.org",
    "fs:src/**",
    "fs:!src/billing/migrations/*",
    "mcp:github:*"
  ],
  "ask": [
    "bash:npm install *",
    "Edit:.env*",
    "mcp:github:delete_repository"
  ],
  "deny": [
    "bash:rm -rf *",
    "bash:git push *",
    "fs:.ssh/**",
    "network:*"
  ]
}
```

The perms-guard wrapper consumes the sidecar verbatim at runtime via
`prism perms-guard --policy <sidecar>`. The canonical grammar (with
`fs:` / `network:` / `mcp:` / `**` / `!`) is supported natively by
prism's own matcher — no translation loss.

#### 4.6.4 Per-plugin capability split

| Plugin    | Permissions.Global         | Permissions.Scoped          |
|-----------|----------------------------|------------------------------|
| Claude    | Native                     | Degraded (folded with warning) |
| Cursor    | Degraded (sandbox profile) | Unsupported                  |
| Gemini    | Native (perms-guard)       | Native (perms-guard)         |
| Copilot   | Native (preview, opt-in)   | Native (preview, opt-in)     |
| Cline     | Native (perms-guard)       | Native (perms-guard)         |
| Continue  | Native                     | Degraded (folded with warning) |
| Windsurf  | Unsupported                | Unsupported                  |
| AGENTS.md | Degraded (informational)   | Degraded (informational)     |

The full per-rule capability matrix lives in §12.

### 4.7 Scope

#### 4.7.1 Definition

A Scope is a path- or trigger-scoped context document. Models both
cascading guidance (CLAUDE.md, AGENTS.md, GEMINI.md) and
frontmatter-glob rules (Cursor `.mdc`, Copilot `.instructions.md`,
Cline `.clinerules`, Continue `.continue/rules/`, Windsurf
`.windsurf/rules/`).

A Scope with `Path: ""` and `Activation: Always` IS the canonical
"root context" — there is no separate Context primitive.

#### 4.7.2 Go struct

```go
// Scope is a path- or predicate-scoped context document.
type Scope struct {
    // Path is the project-relative directory the scope cascades over.
    // Empty = virtual scope (cascade plugins must synthesize a
    // location or skip — controlled per-plugin via
    // Extensions.<plugin>.virtual_hoist).
    Path string

    // Name is a stable identifier. Auto-derived as a slug of
    // Description if empty.
    Name string

    // Description is the semantic hint. Required when Activation is
    // ModelDecision or Manual (validator enforces).
    Description string

    // Globs is the path predicate. Defaults to "<Path>/**" for cascade
    // plugins; required when Activation == Glob.
    Globs []string

    // Activation is one of "always", "cascade", "glob", "manual",
    // "model_decision". Simple enum (Scopes pick one; Skills can
    // combine).
    Activation ScopeActivation

    // Priority is an optional sort key (lower loads first). Plugins
    // that lack explicit priority synthesize from filename order
    // (Continue) or cascade depth (Claude / Gemini / AGENTS.md).
    Priority *int

    // Tags are categorical tags for filtering.
    Tags []string

    // IsOverride marks this scope as REPLACING its ancestor's
    // guidance (AGENTS.override.md semantic). Meaningful only when
    // Path != "". Round-trips only on agentsmd plugin; others emit
    // as a regular scope with a leading comment noting the override
    // semantic.
    IsOverride bool

    // Document is the markdown body.
    Document *Document

    // Extensions is plugin-namespaced opaque pass-through. See §5.1.
    Extensions map[string]any
}

type ScopeActivation string

const (
    // ScopeActivationAlways injects the document on every turn.
    // (Cursor `alwaysApply: true`, Windsurf `trigger: always_on`,
    // root cascade files in Claude/Gemini/AGENTS.md.)
    ScopeActivationAlways ScopeActivation = "always"

    // ScopeActivationCascade is the default for Path-set scopes.
    // Produces nearest-wins concatenation on cascade plugins;
    // frontmatter-glob plugins emit globs: ["<Path>/**"] as an
    // approximation.
    ScopeActivationCascade ScopeActivation = "cascade"

    // ScopeActivationGlob activates when files matching Globs are in
    // context. Requires Globs to be non-empty.
    ScopeActivationGlob ScopeActivation = "glob"

    // ScopeActivationManual requires explicit `@name` invocation
    // (Cursor manual, Windsurf trigger:manual).
    ScopeActivationManual ScopeActivation = "manual"

    // ScopeActivationModelDecision lets the LLM decide based on
    // Description.
    ScopeActivationModelDecision ScopeActivation = "model_decision"
)
```

#### 4.7.3 Worked example

Source `.agents/AGENTS.md`:

```markdown
---
name: project-overview
description: Top-level project context.
activation: always
---

# acme-billing

Production billing service for ACME Corp. Stack: Go 1.24, PostgreSQL 16,
pub/sub via Redis Streams.

## Build commands

- `go build ./...` — full build
- `go test ./...` — full test suite
- `make migrate` — apply database migrations

## House style

- Errors are wrapped with `%w` so `errors.Is` works.
- No `panic` outside `cmd/` or test setup.
- All exported types have godoc.
```

Projected to `CLAUDE.md` (root):

```markdown
# acme-billing

Production billing service for ACME Corp. Stack: Go 1.24, PostgreSQL 16,
pub/sub via Redis Streams.

## Build commands

- `go build ./...` — full build
- `go test ./...` — full test suite
- `make migrate` — apply database migrations

## House style

- Errors are wrapped with `%w` so `errors.Is` works.
- No `panic` outside `cmd/` or test setup.
- All exported types have godoc.
```

(Claude's CLAUDE.md has no frontmatter; the name/description/activation
fields are silent.)

Projected to `.cursor/rules/project-overview/RULE.md`:

```markdown
---
description: Top-level project context.
alwaysApply: true
---

# acme-billing

Production billing service for ACME Corp. Stack: Go 1.24, PostgreSQL 16,
pub/sub via Redis Streams.

[…body unchanged…]
```

Projected to `AGENTS.md`:

```markdown
# acme-billing

Production billing service for ACME Corp. […]
```

Source `.agents/src/billing/AGENTS.md`:

```markdown
---
name: billing-subsystem
description: Billing subsystem conventions (overrides project defaults).
activation: cascade
is_override: true
---

# Billing subsystem

This subsystem owns:
- `pkg/billing` — domain model
- `cmd/billing-worker` — async charge reconciliation

## Subsystem-specific rules

- Charge amounts are integers (cents). Never floats.
- All charges go through `billing.Process()`; never the Stripe SDK directly.
- Migrations under `migrations/billing/` use `dbmate`, not `golang-migrate`.
```

Projected to `src/billing/CLAUDE.md`:

```markdown
# Billing subsystem

[…body unchanged…]
```

(Claude has no override semantic; the plugin emits a leading
HTML comment "<!-- prism: AGENTS.override.md semantic; intended to
replace, not augment, parent guidance -->" so a future round-trip can
detect the intent.)

Projected to `src/billing/AGENTS.override.md`:

```markdown
# Billing subsystem

[…body unchanged…]
```

(AGENTS.md is the only plugin that honors `IsOverride` natively.)

#### 4.7.4 Per-plugin field-mapping table (scope)

| Canonical field      | Claude                          | Cursor                          | Gemini                          | Copilot                        | Cline                       | Continue                    | Windsurf                       | AGENTS.md                       |
|----------------------|---------------------------------|---------------------------------|---------------------------------|--------------------------------|-----------------------------|-----------------------------|--------------------------------|---------------------------------|
| Path (cascade)       | nested CLAUDE.md                | rules dir at subtree (degraded) | nested GEMINI.md                | flat (degraded)                | flat (degraded)             | flat + globs (degraded)     | flat + globs (degraded)        | nested AGENTS.md                |
| Path == ""           | root CLAUDE.md                  | `.cursor/rules/00-overview.mdc` | root GEMINI.md                  | `.github/instructions/00-overview.instructions.md`| `.clinerules/00-overview.md` | `.continue/rules/00-overview.md` | `.windsurf/rules/00-overview.md` | root AGENTS.md              |
| Name                 | (filename derived)              | filename                        | (filename derived)              | filename                       | filename                    | `name:`                     | filename                       | filename                        |
| Description          | silent                          | `description:`                  | silent                          | description field (in body)    | `description:`              | `description:`              | `description:` (req for model_decision) | (v1.1 only) frontmatter        |
| Globs                | `.claude/rules/*` `paths:`      | `globs:`                        | unsupported (warn)              | `applyTo:`                     | `paths:`                    | `globs:`                    | `globs:`                       | unsupported (warn)              |
| Activation=Always    | (root only, implicit)           | `alwaysApply: true`             | (root only, implicit)           | `applyTo: "**"` convention     | (no frontmatter)            | `alwaysApply: true`         | `trigger: always_on`           | (root, implicit)                |
| Activation=Cascade   | native nested file              | flatten + globs (degraded)      | native nested file              | flatten + globs (degraded)     | flatten + globs (degraded)  | flatten + globs (degraded)  | flatten + globs (degraded)     | native nested file              |
| Activation=Glob      | (Claude rules only)             | `globs:`                        | unsupported (warn)              | `applyTo:`                     | `paths:`                    | `globs:`                    | `trigger: glob`                | unsupported (warn)              |
| Activation=Manual    | unsupported (warn)              | (no globs/desc)                 | unsupported (warn)              | unsupported (warn)             | unsupported (warn)          | unsupported (warn)          | `trigger: manual`              | unsupported (warn)              |
| Activation=ModelDec. | unsupported (warn)              | (description only)              | unsupported (warn)              | unsupported (warn)             | unsupported (warn)          | (description, no globs)     | `trigger: model_decision`      | unsupported (warn)              |
| Priority             | cascade depth (degraded)        | filename prefix (degraded)      | cascade depth (degraded)        | silent                         | silent                      | `NN-name` filename prefix   | silent                         | cascade depth (degraded)        |
| Tags                 | silent                          | silent                          | silent                          | silent                         | silent                      | silent                      | silent                         | (v1.1) frontmatter (degraded)   |
| IsOverride           | leading comment (warn)          | leading comment (warn)          | leading comment (warn)          | leading comment (warn)         | leading comment (warn)      | leading comment (warn)      | leading comment (warn)         | `AGENTS.override.md` filename   |

#### 4.7.5 Virtual scopes

A Scope with `Path: ""` and `Globs: [...]` is a "virtual" scope — no
filesystem cascade home. Cascade plugins handle this in one of two
ways, controlled per-plugin via
`extensions.<plugin>.virtual_hoist`:

- `virtual_hoist: true` (default): hoist the virtual scope into a
  generated `_virtual_rules/<Name>.md` at root with a leading comment
  recording the source globs.
- `virtual_hoist: false`: skip projection and emit one info warning
  per scope ("virtual scope <name> not projectable to <plugin>;
  source globs not honored by cascade tools").

## 5. Cross-cutting

### 5.1 `extensions:` block

Every primitive's frontmatter and `agents.config.yaml` accept an
`extensions:` block keyed by plugin name. The contents are pass-through
to the matching plugin; the canonical model carries `map[string]any`.

```yaml
extensions:
  claude:
    effort: high
    permissionMode: plan
    skills:
      - bash-helper
      - git-context
  cursor:
    is_background: true
  gemini:
    timeout_mins: 15
```

Rules:
1. Plugin keys MUST match a registered plugin name. Unknown plugin
   keys produce a warning (typo catcher). Disable with
   `prism check --ignore-unknown-extensions`.
2. The contents under `extensions.<plugin>` are pass-through verbatim;
   prism does not interpret them.
3. Plugins MUST ignore extensions blocks for other plugins (no
   cross-reading).
4. Promoting an extension to a canonical field is a minor bump: both
   forms are accepted for one minor cycle (extension key emits an
   info warning suggesting the canonical), then the extension key is
   removed at the next minor.
5. The `x-` OpenAPI prefix style is REJECTED.

### 5.2 ScopePath: how scoping works across primitives

Every primitive has a `ScopePath` field. Empty = global. Non-empty =
scoped (the primitive lives under a subtree of the project).

The `ScopePath` is derived from the source file's location:
- `.agents/skills/foo/SKILL.md` → `ScopePath: ""`
- `.agents/src/billing/skills/foo/SKILL.md` → `ScopePath: "src/billing"`

Plugins handle scoping per primitive (§4.x.4 / §4.x.5 tables); the
universal pattern is:
- Cascade plugins (Claude, Gemini, AGENTS.md) emit Scope-shaped
  primitives at the actual scoped filesystem path.
- Frontmatter-glob plugins synthesize `globs: ["<ScopePath>/**"]`.
- Skills, Commands, and Agents get a `<scopeSlug>-<name>` filename
  prefix on every plugin that supports them but lacks per-scope
  identity, with the lossy mapping flagged as degraded.
- Per-scope Permissions and Hooks: native only on the perms-guard /
  scope-guard wrapper family (Gemini, Cline, Copilot); folded into
  global elsewhere with one info warning per scope.

`ScopePath` MUST be project-relative and MUST NOT escape via `..`
segments. The validator rejects scopes whose path begins with `/`,
`~`, or `..`.

### 5.3 `@include` directive

Markdown bodies may include `<!-- include: path -->` directives that
expand at parse time:

```markdown
# Stripe webhook handling

<!-- include: snippets/webhook-prelude.md -->

To verify a signature: …
```

Rules:
- Paths are resolved relative to the including file.
- Includes nest up to `Include.MaxDepth` levels (default 16).
- Each include is recorded in `Document.Includes` so:
  - the lockfile can reverse-trace via `prism which`,
  - watch mode retriggers compile when an included file changes,
  - plugins downgrade `ModeSymlink` → `ModeWrite` when the document
    has any includes (the symlink target would contain the
    unexpanded source).
- Include cycles are detected at parse time and reported via the
  Validator (not via a parser panic).

### 5.4 Validation rules

A single pre-plugin `Validate()` pass over the parsed Project returns
a `ValidationReport`:

```go
type ValidationError struct {
    File     string  // .agents/-relative
    Line     int     // 1-based; 0 if not applicable
    Column   int     // 1-based; 0 if not applicable
    Field    string  // dot-path: "skills.stripe-webhook.activation.globs"
    Severity string  // "error" | "warning"
    Message  string
}

type ValidationReport struct {
    Errors   []ValidationError
    Warnings []ValidationError
}
```

Errors block compile; warnings are surfaced but compile proceeds. The
`--strict` flag promotes warnings to errors for CI.

**Validator mutation contract.** `Validate()` is the only canonical
default-fill point in the pipeline. It performs a small, fully-enumerated
set of in-place mutations on the Project to canonicalize defaults; after
Validate returns, every plugin's `Plan()` sees an identical normalized
shape and is not permitted to default-fill in its own logic. Plugins
that need a "treat absent as X" semantic must rely on Validate having
populated the field, not on the plugin's own zero-value check.

Current normalizations Validate is permitted to perform:
1. `Skill.Activation.Modes == []` is rewritten to `[ModelDecision]`.
   ModelDecision is the universal skill-default per the synthesis (skill
   research §3.2).
2. `Hook.Sequential == nil` is left as nil; plugins read nil as
   "use the target's default" (which is `true` everywhere except Gemini
   when explicitly flipped). Validate does NOT mutate nil to true,
   because that would lose the "use target default" intent.
3. `MCPServer.Transport == ""` when `Command != ""` and `URL == ""` is
   rewritten to `"stdio"`. The same inference is applied to `http` when
   `URL != ""` and `Command == ""`. Ambiguous cases (both populated or
   both empty) → error, not silent default.

Any future normalization is a v0.x.y SPEC change with a release-note
entry; plugins MUST be updated in lockstep.

Rules enforced by Validate (errors block compile; severity defaults to
"error" unless marked):
- `name:` is required on every primitive that has one (Agent, Skill,
  Command). Missing → error.
- `description:` is required on Skills with Modes containing
  ModelDecision, on Scopes with Activation ModelDecision/Manual, on
  Agents that are model-invocable. Missing → error.
- `Activation.Globs` is required when Modes includes Glob (Skill) or
  Activation is Glob (Scope). Missing → error.
- `ContentRegex` regex syntax sanity (compiles with Go regexp).
  Malformed → error.
- ScopePath containment (no `..`, no absolute paths). Violation → error.
- MCP `Transport` is one of `stdio`, `http`, `sse`. Other → error.
- MCP `Transport: stdio` requires `Command`. Missing → error.
- MCP `Transport: http|sse` requires `URL`. Missing → error.
- Permission rule grammar sanity (recognized target, valid glob,
  no regex). Violation → error.
- Hook `Event` is one of the canonical constants or `native:<verbatim>`.
  Unknown → error.
- `Tools` + `DisallowedTools` on an Agent for a non-Claude target → warning
  (the degraded mapping loses semantics).
- `Activation.UserInvocable: false` AND
  `Activation.ModelInvocable: false` → error (the skill becomes
  inaccessible).
- Cycles in `@include` directives → error.
- `extensions.<plugin>` for unregistered plugin → warning (typo
  catcher).
- `SystemPrompt` empty AND `Description` empty on Agent → warning.
- Frontmatter top-level key outside `extensions:` not in the canonical
  schema → warning.

Plugins consume `model.Project` AFTER Validate has succeeded with no
errors. Plugins emit per-Operation `plugin.Warning` for
projection-time degradations (lossy mapping ≠ ill-formed input).

CLI:
- `prism check` → run Validate, print errors + warnings, exit 1 only on
  errors.
- `prism compile` → run Validate, abort on any error, proceed with
  warnings.
- `prism check --strict` → treat warnings as errors.

## 6. Plugin interface

### 6.1 Go types

```go
// Package plugin defines the Plugin interface and the Operation type
// plugins return.
package plugin

// Plugin is the interface every projection plugin implements.
type Plugin interface {
    // Name is the stable identifier (e.g. "claude", "cursor").
    Name() string

    // Detect returns true if this plugin should be active for the
    // project at root. Implementations look for marker
    // files/directories (e.g. .claude/, .cursor/).
    Detect(root string) bool

    // Capabilities returns the per-field capability matrix entry.
    Capabilities() Capabilities

    // Plan produces the Operations needed to project Project into
    // this plugin's target tool. MUST be pure: no filesystem
    // mutations, no network. The engine handles all IO and conflict
    // resolution.
    Plan(proj *model.Project, opts model.TargetOption) ([]Operation, error)

    // SchemaVersion returns the canonical schema version this plugin
    // understands. The engine refuses to load any plugin whose
    // declared schema version is older than the project's. In-tree
    // plugins always return the current version; the method is
    // defensive for out-of-tree plugin authors.
    SchemaVersion() int
}
```

### 6.2 Capability declaration contract

```go
// Capabilities describes per-field support for each primitive.
// Replaces the v0.8 coarse Support-per-primitive enum.
type Capabilities struct {
    Agent       FieldCapabilities
    Skill       FieldCapabilities
    Command     FieldCapabilities
    Hook        FieldCapabilities
    MCPServer   FieldCapabilities
    Permissions FieldCapabilities
    Scope       FieldCapabilities
}

// FieldCapabilities describes the support for one primitive's fields.
type FieldCapabilities struct {
    // Supported is false when the WHOLE primitive is dropped (no Agent
    // primitive on Windsurf, no Hook primitive on agentsmd, etc.).
    Supported bool

    // Fields maps canonical field paths (dot-notation, e.g.
    // "Activation.Globs", "Auth.Scheme") to their support level.
    // Absent fields are assumed SupportNative.
    Fields map[string]FieldSupport

    // Extensions lists the extension namespaces this plugin reads
    // under `extensions.<name>:`. Typically just `[Name()]`. Other
    // names are silently ignored.
    Extensions []string
}

// FieldSupport describes how a plugin handles a single canonical
// field.
type FieldSupport string

const (
    // SupportNative — 1:1 mapping. No warning emitted.
    SupportNative FieldSupport = "native"

    // SupportDegraded — lossy mapping; the field is emitted with some
    // semantic loss. Plugin emits one info warning per touched
    // canonical source.
    SupportDegraded FieldSupport = "degraded"

    // SupportUnsupported — the field is DROPPED; plugin emits one
    // warn-level warning per touched canonical source.
    SupportUnsupported FieldSupport = "unsupported"

    // SupportSilent — the field is dropped because it carries no
    // semantic meaning on this target; no warning emitted. Use when
    // warning would be noise (e.g. Continue ContentRegex on plugins
    // that have no content-match concept).
    SupportSilent FieldSupport = "silent"
)
```

`Permissions.Global` and `Permissions.Scoped` are independently declared
via the `Fields` map (keys `"Global"` and `"Scoped"`). The CLI and
validator recognise these two keys as special, surfacing them in
`prism capabilities` as a permissions sub-table.

### 6.3 Plan() purity requirements

`Plan()`:
- MUST NOT read or write any file outside the input `Project`.
- MUST NOT make any network calls.
- MUST NOT modify the input `Project`.
- MAY use `os.Getenv` to consult environment variables that affect
  output shape (e.g. an opt-in feature flag like
  `PRISM_COPILOT_PREVIEW_HOOKS`).
- MUST be deterministic with respect to its inputs: the same Project
  + TargetOption produce the same Operations (modulo `os.Getenv`).

When a plugin needs to merge with existing on-disk state (e.g.
`.claude/settings.json` that the user has hand-edited fields in), it
returns an `Operation` with `Kind: OpMerge` and a non-nil `Merger`
function. The engine reads the existing file's bytes (nil if absent)
and calls `Merger(existing) (string, error)`. This keeps `Plan()` pure
and testable; the file read happens in the engine.

### 6.4 SchemaVersion() expectations

In-tree plugins always return the current schema version constant
(`schema.Version = 2` in v0.9.0). The engine cross-references this
against the project's `schema_version:` and refuses to load any plugin
whose `SchemaVersion()` is older. This is defensive — in-tree plugins
never violate it, but out-of-tree plugins shipped via `prism add` could.

A plugin MAY return a newer schema version than the project's: the
engine accepts and only emits a one-time info notice that the plugin
supports a newer schema.

## 7. Lockfile

### 7.1 Schema

`.agents/.lock` is YAML; v0.9 lockfiles carry `version: 2`:

```yaml
schema_version: 2          # canonical schema version
version: 2                 # lockfile format version
generated_at: 2026-05-17T12:00:00Z
binary_version: 0.9.0
files:
  - path: .claude/agents/code-reviewer.md
    sha256: abc123...
    plugin: claude
    sources:
      - .agents/agents/code-reviewer.md
  - path: .cursor/agents/code-reviewer.md
    sha256: def456...
    plugin: cursor
    sources:
      - .agents/agents/code-reviewer.md
  ...
```

### 7.2 What's tracked

- Every file prism emits (under target plugin trees).
- Per-file SHA-256 of the on-disk content at generation time.
- Per-file source attribution: which `.agents/` files contributed
  (multi-source for merged outputs like `.claude/settings.json`).
- Per-file plugin name.
- Top-level generation metadata (timestamp, binary version, schema
  version).

### 7.3 Drift detection algorithm

For each tracked file, on the next `prism check` or `prism compile`:
1. Read the on-disk file. If missing → orphan; the file was deleted
   externally (warn).
2. Compute its SHA-256. Compare to the lockfile's recorded SHA. If
   different → drift; the file was hand-edited (warn; `--strict` →
   error).
3. After Plan() runs, compute the expected content hash from the
   planned Operations. If different from the lockfile → the canonical
   source changed (normal — compile proceeds, lockfile updates).

For each newly-emitted file not in the lockfile:
- Add to lockfile.

For each lockfile entry whose source no longer exists in the
Operations:
- Orphan: the canonical source was deleted. Remove the file from disk
  AND from the lockfile.

### 7.4 Forward-incompat policy

Cargo-style hard error. If the lockfile's `version:` exceeds the
binary's known maximum, the engine refuses to read it and emits:

```
lockfile is version 3 but this binary (0.9.0) supports up to version 2.
Update prism: https://github.com/agents-dev/agents/releases
```

No dual-write. The lockfile is a single shape per major version; a
newer lockfile MAY contain fields the older binary doesn't know, and
silently truncating would let stale tooling overwrite a team's pinned
set.

### 7.5 Stable serialization

The lockfile MUST round-trip deterministically. Rules:
- Keys are sorted alphabetically at every level.
- Lists are sorted by `path` for `files:`, then by `sha256`.
- Timestamps are RFC3339 with UTC offset (`Z`).
- Strings are single-quoted when YAML-quoting is required; bare
  otherwise.

## 8. Schema versioning

### 8.1 `schema_version: 2` semantics

The integer at the top of `.agents/agents.config.yaml` declares which
major version of the canonical schema the project targets.

```yaml
schema_version: 2
targets:
  - claude
  - cursor
  - gemini
```

- Major (integer bump: 2 → 3) is BREAKING. The parser refuses to read
  older majors without explicit `prism migrate`. New majors MAY read
  one previous major for back-compat reads (lockfiles and projects).
- Minor changes (additive new fields, new constants, new tool support)
  do NOT bump `schema_version`. The CLI's understanding is gated on
  the binary's version, not the schema version.
- Patch / clarification changes do not touch this field; SPEC.md is
  re-issued.

v0.9.0 ships with `schema_version: 2`. The project is greenfield —
v1 (the implicit, missing-field form) is not a supported read path.
A file without `schema_version:` produces a parser error pointing at
this section.

### 8.2 Versioning policy going forward

Post-v1.0:
- Schema major bumps require:
  1. Bump declared in SPEC.md.
  2. Deprecation warnings for one full minor cycle BEFORE the bump
     ships.
  3. `prism migrate --from vN-1 --to vN` available in the release that
     ships vN.
  4. Back-compat read of vN-1 for one minor cycle of the binary that
     shipped vN.
- Additive (minor) changes don't bump schema_version. The parser
  accepts the missing-field form (warning if "you should set this
  now", silent if "you can set this to opt into new behavior").

### 8.3 Deprecation cycles (post-v1.0)

A field marked deprecated is kept readable for one full minor cycle of
the binary. Example: if v1.4 deprecates `old_field`, v1.5 still reads
it with warning, v1.6 may remove it. Major-bump removals require
`prism migrate` to be available in the release that drops the field.

Renaming a field = add-new + deprecate-old in the same release, then
remove-old at the next major schema bump.

## 9. JSON Schema

### 9.1 Pointer to `schema/v2/`

Generated schemas live at `schema/v2/`:

```
schema/v2/
  agents.config.schema.json     # top-level config
  agent.schema.json             # one per primitive
  skill.schema.json
  command.schema.json
  hook.schema.json
  mcpserver.schema.json
  permissions.schema.json
  scope.schema.json
  extensions.schema.json        # the extensions block
```

Schemas target JSON Schema Draft 2020-12. Generated via
`invopop/jsonschema` from the Go struct definitions in
`internal/model/`. Build target: `cmd/prism-schema` regenerates from
struct tags.

Editor discovery: each example file under `examples/` includes a YAML
language-server hint at the top:

```yaml
# yaml-language-server: $schema=https://prism.dev/schema/v2/agent.schema.json
---
name: code-reviewer
...
```

### 9.2 What's schema-validated vs prose-only

Schema-validated:
- Field names, types, required/optional via `omitempty`.
- Enum constants (`Priority`, `FieldSupport`, `HookEvent`,
  `ScopeActivation`, `SkillActivationMode`, MCP `Transport`).
- Field descriptions derived from `jsonschema_description:` struct
  tags (avoiding a build-time AST pass).

Prose-only (SPEC.md alone):
- Cross-field invariants ("if Activation includes ModelDecision,
  Description is required"). JSON Schema can express these with
  `dependentRequired` / `if/then/else`, but the prose form is what
  humans read.
- Filesystem layout rules (file location, scoped-subdirectory layout).
  Schema describes one document; layout requires per-file path
  validation done in the parser.
- Extension semantics (per-plugin pass-through). The `extensions:`
  block is `additionalProperties: true` in the schema.
- Plugin warning catalog.
- Migration notes.

## 10. Migration from v0.8 (out of scope — greenfield)

v0.9.0 is the first public release of the canonical schema. The
project is greenfield — there is no installed base of v1 (the implicit,
schema_version-less form) to migrate from. v0.9 errors on files that
lack `schema_version:` or set it to a value other than 2.

A future `prism migrate` command is planned for post-v1.0 schema
bumps; see §8 and §11.

## 11. Open questions / future work

### 11.1 Per-section deferrals

- **Permissions rate-limiting** (e.g. "allow `bash:curl *` up to 10
  calls per session, then ask"). No surveyed tool supports it. Defer
  until one does; meanwhile, `extensions.<plugin>.rate_limits` is the
  pass-through.
- **Continue Agent multi-projection**. Continue's Assistant is 1:1
  with `config.yaml`. v0.9 projects the first Agent and warns per
  extra. v0.10+ may emit N config files or wait for Continue to grow
  per-agent file shapes.
- **Hook handler HTTP shimming**. Cursor / Gemini / Cline / Windsurf
  lack HTTP handlers. v0.9 drops with warning; v0.10+ may ship an
  opt-in `--shim-http` flag that synthesizes a `curl`-wrapping command.
- **Hook events outside the v0.9 set**. `pre_model`, `post_model`,
  `pre_tool_selection`, `instructions_loaded`, `teammate_idle`,
  `task_created`, `elicitation`, `workspace_open`, `setup`,
  `cwd_change`, `file_change`, `slash_command_expand` are all
  single-tool today (Claude or Cursor only). Add to canonical when a
  second tool supports them. Authors can use `native:<verbatim>` to
  carry them through without translation.
- **MCP per-server per-scope enforcement**. No tool supports it today;
  `ScopePath` is preserved on MCPServer for attribution only.
- **Cline hook emission**. The current Cline plugin's hook emission
  was last verified against Cline v3.36; verify against any later
  release before v1.0.
- **Scope-guard envelope widening**. Cursor's `beforeReadFile` and
  `afterFileEdit` events put `file_path` at the root of the stdin
  envelope, not under `tool_input`. The scope-guard subcommand needs
  to extract `file_path` from three locations: `.tool_input.file_path`
  (most), `.tool_info.file_path` (Windsurf), and root-level
  `.file_path` (Cursor file events). Recorded as a small
  v0.9.x follow-up to `cmd/prism/scope-guard.go`.

### 11.2 Stability commitment timeline (v0.9 → v1.0 soak)

The v0.9 → v1.0 path is the schema's soak cycle. The full v2 schema
gets exercised against `examples/`, the registry, and any third-party
users for a release cycle before v1.0 declares stability.

Targets:
- v0.9.0: schema v2 lands; all in-tree plugins updated.
- v0.9.x: per-field capability declarations validated against real
  plugin emit (round-trip contract tests). Bug fixes only — no
  additive schema changes.
- v0.10.0: extension-promotion candidates identified (which
  per-plugin extension fields have proven cross-cutting enough to
  graduate to canonical).
- v1.0.0: schema is frozen; deprecation cycles per §8.3 begin.

## 12. Appendix: capability matrix

The complete per-field × per-plugin matrix across all seven primitives.
Legend: **N** = native; **D** = degraded (lossy but emitted); **U** =
unsupported (dropped, warn); **S** = silent (dropped, no warning).

Plugin column order: cla (claude), cur (cursor), gem (gemini), cop
(copilot), cli (cline), con (continue), win (windsurf), agm (agentsmd).

### Agent

| Field              | cla | cur | gem | cop | cli | con | win | agm |
|--------------------|-----|-----|-----|-----|-----|-----|-----|-----|
| Name               | N   | N   | N   | N   | U   | D   | U   | U   |
| Description        | N   | N   | N   | N   | U   | U   | U   | U   |
| SystemPrompt       | N   | N   | N   | N   | U   | D   | U   | D   |
| Model              | N   | N   | N   | N   | U   | D   | U   | U   |
| ModelFallbacks     | S   | S   | S   | N   | S   | S   | S   | S   |
| Tools              | N   | S   | N   | N   | U   | U   | U   | U   |
| DisallowedTools    | N   | D   | D   | D   | U   | U   | U   | U   |
| ReadOnly           | D   | N   | D   | D   | U   | U   | U   | U   |
| Background         | N   | N   | S   | S   | U   | U   | U   | U   |
| MaxTurns           | N   | S   | N   | S   | U   | U   | U   | U   |
| Temperature        | S   | S   | N   | S   | U   | D   | U   | U   |
| MCPServers         | N   | U   | N   | N   | U   | D   | U   | U   |
| AllowedSubagents   | N   | U   | U   | N   | U   | U   | U   | U   |
| UserInvocable      | D   | S   | S   | N   | U   | U   | U   | U   |
| ModelInvocable     | D   | S   | S   | N   | U   | U   | U   | U   |
| InitialPrompt      | N   | U   | U   | U   | U   | U   | U   | U   |
| ScopePath          | D   | D   | D   | D   | U   | U   | U   | U   |
| Extensions[plugin] | N   | N   | N   | N   | N   | N   | N   | N   |

### Skill

| Field                         | cla | cur | gem | cop | cli | con | win | agm |
|-------------------------------|-----|-----|-----|-----|-----|-----|-----|-----|
| Name                          | N   | N   | N   | N   | N   | N   | N   | S   |
| Description                   | N   | N   | N   | N   | D   | N   | N   | S   |
| WhenToUse                     | N   | D   | D   | D   | D   | D   | D   | S   |
| Activation.Modes={Always}     | D   | N   | U   | D   | N   | N   | N   | S   |
| Activation.Modes={ModelDec.}  | N   | N   | N   | N   | U   | N   | N   | S   |
| Activation.Modes={Glob}       | N   | N   | U   | N   | N   | N   | N   | S   |
| Activation.Modes={Manual}     | N   | N   | N   | N   | N   | N   | N   | S   |
| Activation.Globs              | N   | N   | U   | N   | N   | N   | N   | S   |
| Activation.ContentRegex       | U   | U   | U   | U   | U   | N   | U   | S   |
| Activation.UserInvocable      | N   | S   | S   | S   | N   | S   | S   | S   |
| Activation.ModelInvocable     | N   | S   | S   | S   | S   | S   | S   | S   |
| AllowedTools                  | N   | D   | U   | N   | U   | U   | U   | S   |
| Arguments                     | N   | D   | D   | N   | U   | D   | U   | S   |
| Scripts                       | N   | N   | N   | N   | U   | U   | U   | S   |
| References                    | N   | N   | N   | N   | U   | U   | U   | S   |
| Model                         | N   | U   | U   | N   | U   | U   | U   | S   |
| Subagent                      | N   | D   | U   | N   | U   | U   | U   | S   |
| ScopePath                     | D   | D   | D   | D   | U   | U   | U   | S   |
| Extensions[plugin]            | N   | N   | N   | N   | N   | N   | N   | S   |

### Command

| Field              | cla | cur | gem | cop | cli | con | win | agm |
|--------------------|-----|-----|-----|-----|-----|-----|-----|-----|
| Name               | N   | N   | N   | N   | N   | N   | N   | D   |
| Description        | N   | U   | N   | N   | D   | N   | U   | D   |
| ArgumentHint       | N   | S   | S   | N   | S   | S   | S   | S   |
| Arguments          | N   | D   | D   | N   | D   | D   | D   | S   |
| Model              | N   | U   | U   | N   | U   | N   | U   | S   |
| Tools              | N   | U   | U   | N   | U   | U   | U   | S   |
| Agent              | N   | D   | U   | N   | D   | U   | D   | S   |
| AutoInvoke         | N   | S   | S   | S   | S   | S   | S   | S   |
| ScopePath          | D   | D   | D   | D   | D   | D   | D   | D   |
| Extensions[plugin] | N   | N   | N   | N   | N   | N   | N   | N   |

### Hook

| Field                       | cla | cur | gem | cop | cli | con | win | agm |
|-----------------------------|-----|-----|-----|-----|-----|-----|-----|-----|
| Name                        | S   | S   | N   | S   | S   | S   | S   | U   |
| Description                 | S   | S   | N   | S   | S   | S   | S   | U   |
| Event (core ≥4-tool subset) | N   | N   | N   | N   | N   | N   | N   | U   |
| Event (Claude-only)         | N   | U   | U   | U   | U   | N   | U   | U   |
| Matcher (exact)             | N   | N   | N   | N   | D   | N   | D   | U   |
| Matcher (regex)             | N   | N   | N   | N   | D   | N   | U   | U   |
| Handlers (command)          | N   | N   | N   | N   | N   | N   | N   | U   |
| Handlers (http)             | N   | U   | U   | N   | U   | N   | U   | U   |
| Handlers (mcp_tool)         | N   | U   | U   | U   | U   | U   | U   | U   |
| Handlers (prompt)           | N   | N   | U   | D   | U   | N   | U   | U   |
| Handlers (agent)            | N   | U   | U   | U   | U   | N   | U   | U   |
| Sequential                  | S   | S   | N   | S   | S   | S   | S   | U   |
| Disabled                    | N   | N   | N   | N   | N   | N   | N   | U   |
| TimeoutMs                   | N   | N   | N   | N   | D   | N   | U   | U   |
| StatusMessage               | N   | S   | S   | S   | S   | N   | S   | U   |
| Async                       | N   | U   | U   | U   | U   | N   | D   | U   |
| FailClosed                  | U   | N   | U   | U   | U   | U   | U   | U   |
| Once                        | N   | U   | U   | U   | U   | N   | U   | U   |
| If                          | N   | U   | U   | U   | U   | N   | U   | U   |
| Cwd                         | D   | S   | S   | N   | S   | S   | N   | U   |
| Env                         | U   | U   | U   | N   | U   | U   | U   | U   |
| Bash + Powershell           | U   | U   | U   | N   | U   | U   | N   | U   |
| ScopePath                   | D   | D   | N¹  | N¹  | N¹  | D   | D   | U   |
| Extensions[plugin]          | N   | N   | N   | N   | N   | N   | N   | U   |

¹ Native via the perms-guard / scope-guard wrapper family — one sidecar
per scope.

### MCPServer

| Field              | cla | cur | gem | cop | cli | con | win | agm |
|--------------------|-----|-----|-----|-----|-----|-----|-----|-----|
| Name               | N   | N   | N   | N   | N   | N   | N   | U   |
| Transport (stdio)  | N   | N   | N   | N   | N   | N   | N   | U   |
| Transport (http)   | N   | N   | N   | N   | N   | N   | N   | U   |
| Transport (sse)    | N   | N   | N   | N   | N   | D   | N   | U   |
| Command            | N   | N   | N   | N   | N   | N   | N   | U   |
| Args               | N   | N   | N   | N   | N   | N   | N   | U   |
| Env                | N   | N   | N   | N   | N   | N   | N   | U   |
| Cwd                | S   | S   | N   | S   | S   | N   | S   | U   |
| URL                | N   | N   | N   | N   | N   | N   | N   | U   |
| Headers            | N   | N   | N   | N   | N   | N   | N   | U   |
| Auth.Scheme=bearer | N   | N   | N   | N   | N   | N   | N   | U   |
| Auth.Scheme=header | N   | N   | N   | N   | N   | N   | N   | U   |
| Auth.Scheme=oauth  | D   | D   | D   | D   | D   | D   | D   | U   |
| TimeoutMs          | U   | U   | N   | U   | N   | U   | U   | U   |
| Disabled           | N   | N   | N   | N   | N   | N   | N   | U   |
| AutoApprove        | U   | U   | D   | U   | N   | U   | U   | U   |
| Trust              | U   | U   | N   | U   | U   | U   | U   | U   |
| IncludeTools       | U   | U   | N   | U   | U   | U   | U   | U   |
| ExcludeTools       | U   | U   | N   | U   | U   | U   | U   | U   |
| ScopePath          | D   | D   | D   | D   | D   | D   | D   | U   |
| Extensions[plugin] | N   | N   | N   | N   | N   | N   | N   | U   |

### Permissions

| Field/Capability    | cla | cur | gem | cop | cli | con | win | agm |
|---------------------|-----|-----|-----|-----|-----|-----|-----|-----|
| Allow (global)      | N   | D   | N¹  | N¹  | N¹  | N   | U   | D   |
| Ask (global)        | N   | U²  | N¹  | N¹  | N¹  | N   | U   | D   |
| Deny (global)       | N   | D   | N¹  | N¹  | N¹  | N³  | U   | D   |
| Allow (scoped)      | D   | U   | N¹  | N¹  | N¹  | D   | U   | D   |
| Ask (scoped)        | D   | U   | N¹  | N¹  | N¹  | D   | U   | D   |
| Deny (scoped)       | D   | U   | N¹  | N¹  | N¹  | D   | U   | D   |
| `bash:` target      | N   | N   | N¹  | N¹  | N¹  | N   | U   | D   |
| `Edit/Read/Write:`  | N   | D   | N¹  | N¹  | N¹  | N   | U   | D   |
| `fs:` target        | D⁴  | N   | N¹  | N¹  | N¹  | D⁴  | U   | D   |
| `network:` target   | D⁵  | N   | N¹  | N¹  | N¹  | D⁵  | U   | D   |
| `mcp:` target       | D⁶  | U   | N¹  | N¹  | N¹  | D⁶  | U   | D   |
| `**` recursive glob | D⁷  | N   | N¹  | N¹  | N¹  | D⁷  | U   | D   |
| `!` negation        | D⁸  | N   | N¹  | N¹  | N¹  | D⁸  | U   | D   |
| Extensions[plugin]  | N   | N   | N   | N   | N   | N   | U   | N   |

¹ Native via the perms-guard wrapper (sidecar JSON consumed by `prism
perms-guard` at runtime).
² Cursor sandbox has no `ask` bucket; drops with warning.
³ Continue spells it `exclude`.
⁴ Fan-out: `fs:src/**` → `Edit(src/**)` + `Read(src/**)` + `Write(src/**)`.
⁵ `network:github.com` → `WebFetch(domain:github.com)` (Claude) /
`Fetch(github.com)` (Continue).
⁶ `mcp:github:create_issue` → `mcp__github__create_issue` (Claude) /
`MCP(github:create_issue)` (Continue).
⁷ `**` translates only into the target's own glob dialect; deny-only
when the target's glob is weaker.
⁸ `!` splits into allow+deny entries at emit.

### Scope

| Field/Capability       | cla | cur | gem | cop | cli | con | win | agm |
|------------------------|-----|-----|-----|-----|-----|-----|-----|-----|
| Path (cascade)         | N   | D   | N   | D   | D   | D   | D   | N   |
| Path == ""             | N   | N   | N   | N   | N   | N   | N   | N   |
| Name                   | D   | N   | D   | N   | N   | N   | N   | D   |
| Description            | S   | N   | S   | N   | N   | N   | N   | D   |
| Globs                  | N¹  | N   | U   | N²  | N   | N   | N   | U   |
| Activation=Always      | D³  | N   | D³  | N⁴  | N   | N   | N   | D³  |
| Activation=Cascade     | N   | D   | N   | D   | D   | D   | D   | N   |
| Activation=Glob        | N¹  | N   | U   | N   | N   | N   | N   | U   |
| Activation=Manual      | U   | N   | U   | U   | U   | U   | N   | U   |
| Activation=ModelDec.   | U   | N   | U   | U   | U   | N   | N   | U   |
| Priority               | D⁵  | D⁶  | D⁵  | S   | S   | N⁷  | S   | D⁵  |
| Tags                   | S   | S   | S   | S   | S   | S   | S   | D⁸  |
| IsOverride             | U⁹  | U⁹  | U⁹  | U⁹  | U⁹  | U⁹  | U⁹  | N   |
| Extensions[plugin]     | N   | N   | N   | N   | N   | N   | N   | N   |

¹ Claude `.claude/rules/*.md` `paths:` frontmatter.
² Copilot `applyTo:`.
³ "Always" is implicit when the file is at project root.
⁴ `applyTo: "**"` convention.
⁵ Synthesized as cascade depth.
⁶ Synthesized as filename prefix.
⁷ Lexicographic filename order; documented in Continue.
⁸ Surfaced as a markdown sub-heading.
⁹ Emit with a leading comment noting the AGENTS.override semantic.
