# prism v0.9.0 — implementation plan

This document is the v0.9.0 release plan, derived from SPEC.md. It is the
work breakdown a fresh-context implementation team would follow to ship the
canonical schema laid out in SPEC.md, sequenced into phases and risk-rated
where appropriate.

Cross-references: SPEC.md sections are cited by number (e.g. "see SPEC §4.1
for the Agent struct").

## 1. Overview

### 1.1 v0.9.0 scope — what ships

- **Canonical schema v2** as defined in SPEC.md, with full per-field
  fidelity across 7 primitives: Agent (§4.1), Skill (§4.2), Command
  (§4.3), Hook (§4.4), MCPServer (§4.5), Permissions (§4.6), Scope
  (§4.7).
- **Per-field capability declarations** on every plugin via the new
  `FieldCapabilities` struct (SPEC §6.2).
- **`SupportSilent` field-support level** distinct from
  `SupportUnsupported` (SPEC §6.2).
- **Polymorphic `SkillActivation`** (SPEC §4.2.4) and simple
  `ScopeActivation` enum (SPEC §4.7.2).
- **Dimension-aware permission grammar** with `fs:`, `network:`,
  `mcp:`, `**` recursion, and `!` negation (SPEC §4.6.2).
- **MCP `Transport` enum** required, plus `Headers`, `MCPAuth`, and
  six policy fields (SPEC §4.5).
- **Canonical hook event taxonomy** including per-action events
  (SPEC §4.4.2), with `native:<verbatim>` escape hatch.
- **`extensions:` block** on every primitive (SPEC §5.1).
- **Argument substitution canonicalized** to `{{arg:name}}`,
  `{{shell:cmd}}`, `{{file:path}}` (SPEC §4.3.2).
- **`schema_version: 2`** required at top of `agents.config.yaml`
  (SPEC §8).
- **Lockfile `version: 2`** with Cargo-style forward-incompat hard
  error (SPEC §7).
- **`Validate()` pass** with `ValidationReport{Errors, Warnings}` and
  file:line refs (SPEC §5.4).
- **`--strict` flag** on `prism check` promoting warnings to errors
  (SPEC §5.4).
- **JSON Schema generation** via `invopop/jsonschema`, Draft 2020-12,
  output to `schema/v2/` (SPEC §9).
- **8 plugins fully updated**: claude, cursor, gemini, copilot, cline,
  continue, windsurf, agentsmd. Continue HOOKS flips to native (Claude-
  compatible schema). Cursor cross-emit: `.cursor/agents/` only.
- **All importers updated** to mirror the new fields (round-trip).

### 1.2 What does NOT ship in v0.9.0

- No migration path from v1 (schema-less) — greenfield (SPEC §10).
- No `prism migrate` command (deferred to v1.0+; see SPEC §11.1).
- No Permissions rate-limiting (SPEC §11.1).
- No HTTP handler shimming (SPEC §11.1) — drops with warning today.
- No Continue per-agent file shapes (SPEC §11.1) — first agent wins.
- No gRPC plugins (SPEC §6, decision 19).
- No scope-guard envelope widening to handle Cursor file-event
  root-level `file_path` — recorded as v0.9.x follow-up.

## 2. Affected files

This section lists every file touched, with a one- or two-line sketch of
the change.

### internal/model/model.go
**Full rewrite.** Replace the existing struct definitions with the
canonical types from SPEC §4. Key changes:
- Add `Extensions map[string]any` to every primitive.
- Add Activation enums (SkillActivation, ScopeActivation).
- Add MCPAuth subtype + Transport required field on MCPServer.
- Add per-event constants on Hook (canonical HookEvent enum).
- Add Subagent, Scripts, References, AllowedTools, Arguments,
  WhenToUse, Model fields to Skill.
- Add Tools, Agent, ArgumentHint, AutoInvoke, ManualOnly fields to Command.
- Add comprehensive godoc on every field per SPEC §4.x.2 sections.

### internal/plugin/plugin.go
**Capabilities expansion.** Replace coarse `Capabilities` struct with
`{Agent, Skill, Command, Hook, MCPServer, Permissions, Scope}` each
typed `FieldCapabilities`. Add `FieldSupport` with four constants
including new `SupportSilent`. Add `SchemaVersion() int` to Plugin
interface.

### internal/parser/parser.go (+ new internal/validator/)
**Validate() addition.** Split parse / validate / project resolution:
- Parser produces the in-memory Project from `.agents/` files.
- New `internal/validator/validator.go` exports
  `Validate(*model.Project) ValidationReport` with file:line refs
  (per SPEC §5.4).
- Frontmatter parsing tracks the offset of the frontmatter block within
  the source file so line numbers map back to the user's file, not
  the frontmatter substring.
- Existing `splitFrontmatter` keeps its CRLF normalisation; add line
  offset tracking.

### internal/version/schema.go (new)
Single source for the canonical schema version constant:
```go
package version
const SchemaVersion = 2
```

### internal/lockfile/lockfile.go
Bump default `version:` from 1 to 2. Add `schema_version: 2` field
mirroring the project. Cargo-style hard error on forward-incompat:
when reading a lockfile whose `version:` exceeds the binary's known
max, fail with a pointer to the binary version (SPEC §7.4). Keep
the sorted-keys/RFC3339 stable serialization (SPEC §7.5).

### internal/perms/perms.go
Extend the matcher to recognise:
- `fs:` namespace (synonym for "Read|Write|Edit" when matching tool
  names).
- `network:` with a domain/URL pattern matcher.
- `mcp:<server>[:<tool>]` matching against MCP tool payloads
  (`mcp__server__tool` runtime name).
- `**` recursive glob in matchRule.
- `!` negation prefix; reduces to a separate deny entry at emit time.

### internal/parser/parser.go (frontmatter rules)
- Reject TOML (`+++`) and JSON (`{`) frontmatter delimiters with a
  parser error pointing at SPEC §3.2.
- Surface unknown top-level frontmatter keys (outside `extensions:`)
  as warnings.
- Recognize the `extensions:` block and stash under the relevant
  primitive's `Extensions map[string]any`.

### plugins/claude.go
Update Capabilities to the new per-field shape. Pass per-field support
declarations:
- All Agent fields: see SPEC §12 / Agent row.
- Skill `Subagent`: native (context: fork + agent:).
- Hook events: full Claude set; per-action events translate to
  generic + matcher.
- MCP: `${env:VAR}` → `${VAR}` at emit. Emit known-bug warning when
  `${env:VAR}` appears in headers on HTTP/SSE transport.
- Permissions: dimension fan-out (`fs:` → Edit/Read/Write triple;
  `network:` → `WebFetch(domain:...)`; `mcp:` → `mcp__*` pattern).
  `**` glob and `!` negation translate to deny entries.
- Pass through `extensions.claude.*` verbatim into Claude-specific
  frontmatter fields.

### plugins/cursor.go
- Cross-emit policy: emit Agents to `.cursor/agents/` ONLY. When
  Claude is also a target, emit a single info warning ("Cursor reads
  .claude/agents/ itself; emitting to .cursor/agents/ instead").
- Build `sandbox.json` from canonical permissions (dimensional fan-in:
  `bash:` → `shell.allow`; `fs:` / `Edit:` / `Read:` / `Write:` →
  `filesystem.{allow,deny}`; `network:` → `network.domains`).
- Surface known-bug warning for header `${env:VAR}` substitution on
  remote MCP servers.
- Honour `extensions.cursor.sandbox.*` as verbatim override.

### plugins/gemini.go
- MCP: handle `httpUrl` vs `url` field-name split per transport.
- Hooks: translate canonical per-action events to `BeforeTool +
  matcher` form. Native `sequential:` honored.
- Permissions: continue using the perms-guard wrapper family. Update
  the sidecar's policy.json grammar to handle `fs:`, `network:`,
  `mcp:`, `**`, `!`. The runtime matcher in `internal/perms/perms.go`
  already covers these (single source of truth).

### plugins/copilot.go
- Hooks: support both camelCase and PascalCase event names; default to
  camelCase. Cross-platform script keys (`bash` + `powershell`) emit
  natively.
- MCP: emit to `.github/mcp.json` by default; respect VS Code shape
  (`servers` + `inputs`) when `extensions.copilot.vscode_shape: true`.
- Permissions: preview-hooks-opt-in path unchanged; perms-guard wrapper
  used when enabled.
- `extensions.copilot.preview_hooks: true` enables Permissions /
  scoped-permissions / preview Hook handlers.

### plugins/cline.go
- Hooks: filename-dispatch model. Write one script per (event,
  matcher) pair; inline matcher guard clause at script head when
  `Matcher.Kind == "exact"` or `"regex"`. Multi-handler events become
  a dispatcher script.
- MCP: use `type: streamableHttp` (camelCase) for http transport;
  emit `disabled`, `autoApprove`, `timeout` natively.
- Permissions: keep perms-guard wrapper; grammar pickup from shared
  matcher.

### plugins/continue_plugin.go
- **HOOKS flips from unsupported → native.** Mirror Claude's emit
  exactly (per SPEC §4.4 — Continue's schema is verbatim Claude). Share
  the Claude hook serializer where possible (extract to
  `plugins/hooks_claude_shape.go`).
- MCP: emit YAML at `.continue/mcpServers/<slug>.yaml`. Use
  `type: streamable-http` (hyphenated). `${env:VAR}` →
  `${{ secrets.VAR }}` with one info warning per server. SSE on YAML
  emits SPEC §4.5.4 known-bug warning (#5359).
- Permissions: keep existing native emit; extend `pascalToolName`
  table to recognize `fs`, `network`, `mcp` namespaces and degrade
  with warnings.
- Continue Agent: first Agent → `~/.continue/config.yaml`; subsequent
  agents emit one info warning per agent ("Continue Assistant is 1:1
  with config.yaml; agent <name> not projected").

### plugins/windsurf.go
- MCP: support `serverUrl` and `url` aliases for http transport.
  `${env:VAR}` and `${file:/path}` syntaxes pass through.
- Hooks: emit to `.windsurf/hooks.json` with snake_case event names.
  Per-action canonical events map natively (pre_run_command, etc.).
  Cross-platform script keys (`command` + `powershell`).
- Permissions: stays unsupported (no perms-guard wiring in v0.9).
- Per-character limit: 12,000 chars per workflow / scope file.
  Plugin emits an error when a command body or scope body post-
  substitution exceeds the limit.

### plugins/agentsmd.go
- Scope: gets the Scope cell, not Skill (per decision 6). Skill cell
  is SupportSilent.
- AGENTS.md / AGENTS.override.md round-trip native.
- Commands: emit as `## Commands` documentation section in root
  AGENTS.md (informational only).
- Permissions: `## Permissions` section in root AGENTS.md (text only).
- Hooks: SupportUnsupported per primitive (one warning per project, not
  per hook).
- MCP: SupportUnsupported per primitive.

### plugins/frontmatter.go
Helpers for YAML frontmatter serialization (block-scalar `|`,
block-sequence form for lists, single-quote scalars). Used by all
markdown-emitting plugins.

### cmd/prism/*
- `cmd/prism/check.go`: cross-reference Project against active
  plugins' Capabilities. Emit warnings for every Degraded/Unsupported
  field actually set. Add `--strict` flag.
- `cmd/prism/compile.go`: run Validate first; abort on error; proceed
  with warnings.
- `cmd/prism/capabilities.go`: grow the table to show per-field cells.
  Format `cursor SKILLS native; AllowedTools degraded; ContentRegex
  silent`.
- `cmd/prism/which.go`: trace any generated file back to the
  `.agents/` sources via the lockfile's `sources:` array (unchanged
  behavior; lockfile shape now richer).
- `cmd/prism-schema/main.go` (new): build target that regenerates
  `schema/v2/*.json` from struct tags via invopop/jsonschema.

### schema/v2/*.json (new)
Autogenerated by `cmd/prism-schema`. One file per primitive plus the
top-level `agents.config.schema.json`. See SPEC §9.1.

### CHANGELOG.md
Add the v0.9.0 entry summarizing the schema-version-2 release, the
seven canonical primitives, dimension-aware permissions, MCP
authentication, Continue hooks-native uplift, and the per-field
capability matrix.

### README.md
Update the "supported tools" matrix to reflect Continue's hooks
upgrade. Point to SPEC.md for canonical reference. Update the
"current limitations" section to match SPEC §11.1.

### examples/*
- `examples/01-minimal/.agents/agents.config.yaml`: add
  `schema_version: 2`.
- For each existing example, update primitive frontmatter to v2
  shape (Activation enums, MCP transport, etc.).
- Add `examples/07-canonical-coverage/`: a project that exercises
  every field of every primitive, used as the round-trip golden
  fixture (see Test plan).

## 3. New files

### schema/v2/*.json
Autogenerated. See §2 above.

### internal/validator/validator.go
Exports `Validate(*model.Project) ValidationReport`. Implements the
rules listed in SPEC §5.4. ValidationError struct with file:line refs.

### internal/version/schema.go
```go
package version

// SchemaVersion is the major version of the canonical schema this
// binary implements. Bumps require migration tooling and back-compat
// reads per SPEC §8.
const SchemaVersion = 2
```

### plugins/hooks_claude_shape.go
Shared serializer for Claude / Continue hooks (their schemas are
verbatim equivalent). Used by both `plugins/claude.go` and
`plugins/continue_plugin.go`.

### cmd/prism-schema/main.go
Build target: `go run ./cmd/prism-schema > schema/v2/...`. Wires
invopop/jsonschema across the canonical model. Run as part of
`go generate ./...`.

### cmd/prism/migrate.go (deferred)
Stub committed in v0.9.0 to reserve the command name; emits
"migration not required in v0.9 (greenfield); will be implemented
when v3 ships." Real implementation deferred to v1.0+ (SPEC §10).

## 4. Test plan

### 4.1 Unit tests per primitive

For each of the 7 primitives, in `internal/model/`:
- Marshal/unmarshal round-trip from canonical YAML.
- Validate() error cases for required-field gaps (name, description,
  globs when Activation==Glob, MCP transport).
- Activation enum validation (Skill polymorphic; Scope simple).
- Extensions block pass-through preserves nested map.

### 4.2 Round-trip tests per plugin × primitive

In `plugins/<plugin>_test.go`:
- For each primitive × plugin combination, plan operations from a
  canonical fixture.
- Confirm:
  1. Native fields emit exactly the expected wire form.
  2. Degraded fields emit the documented degradation and add a
     warning with the documented message.
  3. Unsupported fields drop and add a warn-level warning.
  4. Silent fields drop with no warning.
- Special tests:
  - Claude `fs:` permission fan-out produces 3 entries.
  - Continue `${env:VAR}` → `${{ secrets.VAR }}` rewrite + warning.
  - Cline filename-dispatch hooks: matcher inlined as guard clause.
  - Cursor cross-emit: agent files land in `.cursor/agents/` only when
    both targets enabled; one warning surfaced.
  - Gemini per-action hook events emit `BeforeTool + matcher`.
  - Windsurf 12 KB cap: bodies past the limit produce errors, not
    silent truncation.
  - AGENTS.override.md round-trip preserves IsOverride.

### 4.3 Field-capability contract tests

New `plugins/capability_contract_test.go`:
- For each plugin, walk `Capabilities()` and for every field marked
  `SupportNative`, verify that a Project where that field is set
  produces a planned Operation whose content includes a non-empty
  serialization of that field (or for Hook handlers, a corresponding
  handler in the output).
- For every field marked `SupportDegraded` / `SupportUnsupported`,
  verify that a Project setting that field produces a Warning whose
  Severity matches (info for degraded, warn for unsupported).
- For every field marked `SupportSilent`, verify that a Project
  setting that field produces NO Warning mentioning that field name.

This is the load-bearing test: it prevents capabilities-vs-reality
drift, which is the most common refactor bug. The test is mechanical
and reads the capability declaration as the spec.

### 4.4 Validation error path tests

In `internal/validator/validator_test.go`:
- Each rule from SPEC §5.4 produces the documented error / warning.
- File:line refs land on the right source line (use Examples mocked
  on disk with known line numbers).
- `--strict` flag promotes warnings to errors.
- Cycle detection in @include directives.

### 4.5 End-to-end golden tests

In `testdata/`:
- `testdata/canonical-coverage/` mirrors
  `examples/07-canonical-coverage/`. Run `prism compile` and snapshot
  the entire output tree under `testdata/canonical-coverage/expected/`.
- Snapshot tests confirm bit-for-bit reproducibility of the
  projection.
- Failure mode tests: each "bad" canonical input under
  `testdata/canonical-coverage/bad/<name>.yaml` produces a known
  ValidationReport with a known message.

### 4.6 Lockfile round-trip

In `internal/lockfile/lockfile_test.go`:
- v2 lockfile round-trips (write, re-read, equal).
- v1 lockfile read produces an error pointing at v0.10 deprecation
  (greenfield; SPEC §8).
- Forward-incompat lockfile (`version: 3`) is rejected with the
  Cargo-style error message.

### 4.7 JSON Schema validation

In `schema/v2/*_test.go`:
- Each generated schema validates the corresponding example
  primitives in `examples/`.
- Each schema validates the canonical-coverage fixture.

## 5. Sequenced phases

Phases are sequenced so each one leaves the codebase in a building
state. Phase 0 is the canonical-types-and-interface atomic switch (the
project genuinely does not build mid-Phase 0; everything else builds).
Phase 1 extracts shared plugin scaffolding so Phase 2 can fan out 8
plugins in parallel without a shared-file race.

### Phase 0 — atomic canonical-types + plugin-interface switch

**Goal:** introduce the v2 canonical types, validator, perms grammar,
AND the new per-field Capabilities interface in a single coordinated
landing. Plugin `Plan()` bodies are mechanically translated to the new
field names with stub fidelity (read new field, emit same outputs as
v0.8) so the project compiles at the Phase 0/1 boundary.

This phase is monolithic by necessity — the model rewrite and the
plugin-interface extension cannot land separately because every
plugin's `Plan()` references both. Splitting them would leave the tree
uncompilable between phases. Within the phase, work proceeds in
ordered substeps but the commit lands as one atomic change.

**Substeps (single commit):**
1. Rewrite `internal/model/model.go` per SPEC §4 (godoc on every
   field).
2. Add `internal/version/schema.go` constant.
3. Update `internal/parser/parser.go` for `schema_version` reading,
   `extensions:` recognition, frontmatter rules (reject TOML/JSON,
   warn on unknown keys, track line offsets).
4. New `internal/validator/validator.go` implementing SPEC §5.4
   rules (including the Validator mutation contract: empty Modes
   → [ModelDecision], Transport inference, etc.).
5. New `internal/perms/perms.go` extensions: `fs:`, `network:`,
   `mcp:`, `**`, `!`.
6. Update `internal/lockfile/lockfile.go` to schema_version: 2 +
   version: 2.
7. Update `internal/plugin/plugin.go` per SPEC §6.1, §6.2
   (`FieldCapabilities`, `SupportSilent`, `SchemaVersion()` method).
8. Mechanically translate each plugin's `Plan()` field accesses:
   `sk.Globs` → `sk.Activation.Globs`, `sk.Trigger` → derived from
   `sk.Activation.Modes`, etc. The translation is structural only —
   semantic enrichment (new fields, per-field capability honoring)
   waits for Phase 2.
9. Every plugin's `Capabilities()` returns a flat translation from
   v0.8 Support per-primitive enum (every field defaults to the
   primitive-level support level). Honest per-field capability
   declaration is a Phase 2 deliverable.
10. Update `cmd/prism/check.go` and `cmd/prism/compile.go` to call
    Validate before plugin dispatch.
11. Update `cmd/prism/capabilities.go` to print per-field columns.

**Tests:** unit tests for model marshal, validator rules, lockfile
round-trip, perms matcher. Existing plugin tests pass against the
mechanical translation (output bytes unchanged vs v0.8.x).

**Risk:** medium. The parser changes are subtle; the line-offset
tracking has to be exact for validator messages to be useful. The
mechanical translation across 8 plugins is voluminous but well-defined
— pair with a structural test that asserts `Plan()` output bytes are
identical to v0.8 for the existing fixtures.

### Phase 1 — extract shared scaffolding

**Goal:** create the shared helper files Phase 2's 8 parallel plugin
tracks will depend on. Doing this here (single commit) avoids the
Phase-2 race where two parallel tracks both need to author the same
shared file.

**Substeps (single commit):**
1. Author `plugins/hooks_claude_shape.go` — the shared Claude-compatible
   hook emission helper. Used by `claude` and `continue` (Continue's
   hooks deliberately mirror Claude's contract per research).
2. Author `plugins/frontmatter.go` — shared YAML frontmatter renderer
   (rendering, escape, `extensions:` passthrough). Used by every
   plugin's `Plan()`.
3. Author `plugins/hook_envelope.go` — shared canonical-event-name
   translation table (`mapClineEvent`, `mapWindsurfEvent`, etc., as
   a single `mapHookEventFor(plugin, canonicalEvent) string`).
4. Author `plugins/perms_grammar.go` — shared `fs:` / `network:` /
   `mcp:` / `<tool>:` parser used by the perms-guard wrapper and
   per-plugin permission emitters.

**Tests:** unit tests for each helper in isolation. No plugin behavior
should change.

**Risk:** low. Pure extraction; no policy decisions land here.

### Phase 2 — 8 plugins update (parallel-safe)

**Goal:** every plugin honors the v2 fields per SPEC §4. Eight tracks,
truly parallelizable now that shared scaffolding (Phase 1) is in place.

**Per-plugin checklist** (apply to each of claude, cursor, gemini,
copilot, cline, continue, windsurf, agentsmd):

1. Refine `Capabilities()` per the SPEC §12 appendix matrix for that
   plugin's column. Drop the v0.8 flat-translation defaults.
2. Update Plan() to:
   - Read the v2 fields fully (Subagent, Scripts, References,
     AllowedTools, Arguments, Activation polymorphism, etc.).
   - Emit warnings matching the per-field FieldSupport.
   - Pass `extensions.<plugin>.*` verbatim into target frontmatter.
   - Translate canonical hook events / per-action events to
     target-native events via the shared `mapHookEventFor` (Phase 1
     scaffolding).
   - Translate canonical permission rules through dimension fan-out
     (where the target's grammar lacks the dimension), via the shared
     `plugins/perms_grammar.go` (Phase 1).
   - For MCP: emit Transport-aware wire shape; apply env-substitution
     rewrite; emit known-bug warnings.
3. Wire scoped permissions per SPEC §4.6.4 (perms-guard family
   natively; others fold-and-warn).

**Per-plugin notes worth calling out:**
- `claude` + `continue` both use `plugins/hooks_claude_shape.go`
  (extracted in Phase 1) for hook emission — Continue's hooks
  deliberately mirror Claude's contract per research.
- **`cline` is a hook-emission REWRITE, not a preservation.** The v0.8
  plugin emits `.clinerules/hooks/<event>.json` (a JSON-config model
  Cline does NOT have — see GitHub issue #6). SPEC §4.4.5 mandates
  filename-dispatch scripts at `.clinerules/hooks/<EventName>` with no
  extension. The Plan() rewrites to emit executable scripts; the
  matcher inlining moves from `matcher:` JSON keys to bash branches
  inside the composite per-event script.
- `cursor` + `claude`: when both targets enabled, cursor de-dupes by
  emitting only to `.cursor/agents/` per SPEC §4.1.4 (Cursor cross-
  reads `.claude/agents/`). One info warning per project.
- `windsurf` + `gemini`: MCP project-local emission to
  `.windsurf/mcp_config.json` and `.gemini/settings.json` respectively,
  with the existing v0.8 user-path migration warning.

**Risk:** medium. The largest surface area; mechanical but voluminous
(~14k lines of plugin code touched). Mitigate by farming the 8 tracks
to 8 reviewers with the SPEC §4.x.4 mapping tables as their contract.
The cline hook rewrite needs all golden tests regenerated — call this
out explicitly to that track.

### Phase 3 — 8 importers update (parallel)

**Goal:** the v2 canonical types round-trip through every plugin's
importer (the inverse of Plan() — reading existing tool config back
into canonical). Mirror the new fields per primitive.

**Per-importer checklist:**
1. Recognize new fields and parse into v2 model.
2. Set Capabilities-aware defaults when fields are absent.
3. Round-trip tests: `Plan(Import(P)) ≡ Plan(P)` for every fixture in
   testdata/.

**Risk:** medium-low. Importers are mechanically symmetric to
emitters; the round-trip test catches drift.

### Phase 4 — JSON Schema generation + Cargo-style lockfile v2

**Goal:** shipping artifacts (schema files + lockfile shape) match
SPEC.

**Steps:**
1. Build `cmd/prism-schema/main.go` and wire it into `go generate
   ./...`.
2. Generate `schema/v2/*.json`; commit and add to release artifacts.
3. Add YAML language-server hint to all `examples/` files.
4. Add `cmd/prism/check.go` schema validation against the generated
   schema (optional, but turn on for `--strict`).
5. Verify lockfile back-compat read of v1 (greenfield: just error
   with the right message).

**Risk:** low. Library-driven; behavior is well-defined.

### Phase 5 — docs (README, examples, CHANGELOG)

**Goal:** the public-facing surface matches SPEC.

**Steps:**
1. CHANGELOG.md v0.9.0 entry.
2. README.md: update tool matrix (Continue hooks now native); pointer
   to SPEC.md.
3. Examples: bump all `agents.config.yaml` to schema_version: 2;
   refresh primitives to v2 frontmatter shape. Add
   `examples/07-canonical-coverage/` as the comprehensive fixture.
4. Migration note in CHANGELOG: greenfield — no migration; `prism
   migrate` deferred per SPEC §10.

**Risk:** low. Documentation churn; reviewer-amenable.

## 6. Fresh-context kickoff brief

The text below is what the fresh-session agent picks up at the start of
v0.9.0 implementation. It cites SPEC.md by section and IMPLEMENTATION_PLAN.md
by phase. Copy verbatim into the session prompt.

> ## v0.9.0 implementation kickoff
>
> You are implementing prism v0.9.0 — the canonical schema bump from v1
> (implicit, schema-less) to v2. Project root:
> `/Users/scottmeyer/projects/agent-projection`. Module path:
> `agents.dev/agents`.
>
> ### Sources of truth
> - `SPEC.md` at repo root — the canonical schema specification. EVERY
>   primitive's struct, every field's per-plugin behavior, every
>   validation rule, the lockfile shape, the JSON Schema strategy.
> - `IMPLEMENTATION_PLAN.md` at repo root — this plan. Phases, affected
>   files, test plan, risks.
> - `CHANGELOG.md` — write the v0.9.0 entry as you go.
>
> ### What's locked
> Every design decision is locked. See SPEC §1 (overview) and §11.1
> (deferred items). Do not re-litigate:
> - 7 primitives (Agent, Skill, Command, Hook, MCPServer, Permissions,
>   Scope). Context absorbs into Scope (SPEC §2.2).
> - Activation is polymorphic on Skill (SPEC §4.2.4) and simple on Scope
>   (SPEC §4.7.2).
> - Permissions split into Global / Scoped capability cells (SPEC §4.6.4).
> - Argument substitution canonical syntax is `{{arg:name}}` /
>   `{{shell:cmd}}` / `{{file:path}}` (SPEC §4.3.2).
> - `schema_version: 2` required (SPEC §8).
> - `extensions:` block keyed by plugin name (SPEC §5.1).
> - In-process Go plugins (SPEC §6).
>
> ### Phase ordering
> Follow the five phases in IMPLEMENTATION_PLAN.md §5. Do not start
> Phase 2 (plugins) until Phase 0 (model + Validate) and Phase 1
> (per-field capabilities) are both complete and tests pass.
>
> ### Working style
> - Run `go build ./...` and `go test ./...` after every Phase.
> - When you touch a plugin, run that plugin's tests (`go test
>   ./plugins/<name>_test.go`).
> - For the round-trip test fixture, use
>   `examples/07-canonical-coverage/` (create this in Phase 5).
> - Capability matrix in SPEC §12 is the contract for capability
>   declarations.
> - When uncertain about a degradation, prefer the documented one in
>   SPEC §4.x.4 over inventing a new one.
>
> ### What to ship in v0.9.0
> Everything in IMPLEMENTATION_PLAN.md §1.1. Nothing in §1.2.
>
> Start by reading SPEC.md end-to-end. Then read this file
> (IMPLEMENTATION_PLAN.md) §2 and §5. Then begin Phase 0.

## 7. Risks + open questions for implementation

### 7.1 Capability declaration drift

**Risk:** A plugin's `Capabilities().Skill.Fields["AllowedTools"]`
claims `SupportNative` but the plugin's Plan() actually drops the
field. The CLI's warning surface stays clean (it trusts the
declaration), and the user has no way to know.

**Mitigation:** §4.3 contract tests. Every native field must produce
an Operation whose content contains a non-empty serialization of that
field. Every degraded field must produce a warning. Mechanical to
implement (read capability matrix, generate one test case per cell);
non-trivial to write but cheap to maintain.

### 7.2 Validator line-offset tracking

**Risk:** Frontmatter parsing wraps the YAML in a substring; line
numbers reported by `gopkg.in/yaml.v3` are relative to the substring,
not to the source file. Naive code reports line 2 when the user's file
has the field on line 27.

**Mitigation:** `splitFrontmatter` returns a `BlockOffset int` field
(line number of the start of the frontmatter in the source file). The
parser adds this to every YAML-derived line number before storing in
`ValidationError.Line`. Test with fixtures that have frontmatter
preceded by varying lines of comments.

### 7.3 Continue Hook serializer sharing

**Risk:** Claude and Continue share a hook schema verbatim
(SPEC §4.4). Sharing the serializer at
`plugins/hooks_claude_shape.go` couples the two plugins; a future
Claude change would risk silently breaking Continue.

**Mitigation:** Add a contract test that runs the shared serializer on
the canonical-coverage fixture and snapshots the output. If Claude
diverges from Continue, the test must be reviewed (someone has to
re-validate that Continue still matches Claude's docs). Snapshot file:
`testdata/shared-hooks-shape.json`.

### 7.4 Cursor cross-emit warning fatigue

**Risk:** When both Cursor and Claude targets are enabled, Cursor
emits an info warning per agent ("emitting to .cursor/agents/, Cursor
reads .claude/agents/ itself"). A project with 20 agents drowns the
user.

**Mitigation:** The warning is emitted ONCE per target combination,
not per agent. Implement in cursor plugin's Plan() by checking
TargetOption for Claude's presence and emitting one project-level
warning at the start of the agent emit loop.

### 7.5 Dimension fan-out ambiguity in importers

**Risk:** Importing a Claude `.claude/settings.json` permissions block
that contains `Edit(src/**)`, `Read(src/**)`, `Write(src/**)` — does
the importer reconstruct `fs:src/**` (single canonical rule) or three
separate Edit/Read/Write rules? Either is defensible.

**Recommendation:** Reconstruct `fs:` when ALL THREE of Edit/Read/Write
appear with identical patterns; emit three separate rules otherwise.
Document in `internal/parser/`. Test with fixtures.

### 7.6 Hook native:<verbatim> event handling

**Risk:** A user writes `event: native:InstructionsLoaded` (Claude-
only event). The Claude plugin emits it verbatim; other plugins do
what?

**Recommendation:** Plugins for non-Claude targets emit one info
warning per native event ("event `native:InstructionsLoaded` is
Claude-specific; not emitted to <plugin>"). The Validator does NOT
reject native events (no validation rule); they're an opt-in escape
hatch and the warning is the safety net.

### 7.7 SkillActivation Modes default

**Risk:** `Activation.Modes: []` (empty) is supposed to imply
`[ModelDecision]` per SPEC §4.2.4. Plugins must apply this default
consistently.

**Mitigation:** SPEC §5.4 documents the Validator's mutation
contract (empty Modes → [ModelDecision], Transport inference, etc.).
After Validate runs, every plugin sees an identical normalized shape.
This is the only canonical default-fill point — plugins MUST NOT
default-fill in their own logic. Refer to SPEC §5.4 "Validator mutation
contract" for the full enumerated list and the policy for adding
new normalizations.
package.

### 7.8 Lockfile schema_version mirror

**Risk:** The lockfile carries both `schema_version: 2` AND `version:
2` (canonical schema + lockfile format). If a user manually edits one
without the other, the lockfile reads as inconsistent.

**Mitigation:** Lockfile parser warns when the two values diverge;
treats lockfile `version` as authoritative for the lockfile shape and
ignores `schema_version` mismatches. The lockfile's `schema_version`
is informational — useful when a stale lockfile is read by a binary
that produced a different schema version.

### 7.9 invopop/jsonschema description tags vs godoc

**Risk:** SPEC §9.2 says "descriptions derived from
`jsonschema_description:` struct tags". Duplicating godoc as tag
content is repetitive and drift-prone.

**Mitigation:** For v0.9, accept the duplication — populate
`jsonschema_description:` from a one-line summary of the godoc. For
v1.0+ consider a `go generate` step that reads godoc via the
`go/doc` package and emits a side file that invopop/jsonschema
consumes. The drift risk is small (~28 fields total across
primitives); we can audit each release.

### 7.10 v0.9.x soak vs early v1.0

**Risk:** v0.9.0 is the schema-soak release. If the canonical fields
turn out to need adjustment based on real use (the most likely source:
edge cases in Hook handler kinds that don't map cleanly), v0.9.x
patches versus v0.10.0 vs sliding into v1.0 is a release-management
call.

**Recommendation:** v0.9.x is bug-fix-only for the schema; field
additions land at v0.10.0. v1.0.0 is the schema freeze. Document this
in the CHANGELOG and in SPEC §11.2.
