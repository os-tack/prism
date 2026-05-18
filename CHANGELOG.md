# Changelog

All notable changes to **prism** are documented here. Format roughly follows
[Keep a Changelog](https://keepachangelog.com/) and the project uses
[Semantic Versioning](https://semver.org/).

## v0.9.0

The canonical-schema release. v0.9 is the first cut of the **schema v2**
contract: seven canonical primitives, per-field capability declarations,
polymorphic activation, dimension-aware permissions, an explicit MCP
transport enum + auth + policy surface, a canonical hook event taxonomy,
and a project-wide `extensions:` pass-through. The v0.9 line is the
v1.0 soak cycle per SPEC §11.2 — additive schema changes resume at v0.10
once the contract test suite has run against the registry and any
third-party plugins for a release.

This is a greenfield release. There is no installed base of the implicit
v1 form (schema_version-less) to migrate from; the parser warns on
missing `schema_version:` for v0.9.x and will error in a follow-up
hardening release.

### Added — canonical model (`internal/model/`)

- **Seven canonical primitives** per SPEC §2.1 + §4: Agent, Skill,
  Command, Hook, MCPServer, Permissions, Scope. The Context and Rule
  primitives were collapsed (a Scope with `Path: ""` and
  `Activation: Always` IS the root context; Rules and Skills share the
  polymorphic SkillActivation shape). Workflows are Skills with
  `Activation.Modes = [Manual]`.
- **Polymorphic `SkillActivation`** (SPEC §4.2.4): a `Modes` set
  (`always`, `model_decision`, `glob`, `manual`) combined with `Globs`,
  `ContentRegex`, `UserInvocable`, `ModelInvocable`. Empty Modes
  defaults to `[ModelDecision]`. Plugins that can only express one
  mode pick the most permissive available.
- **Canonical hook event taxonomy** (SPEC §4.4): 25 canonical
  `HookEvent` constants spanning generic (`pre_tool_use`,
  `post_tool_use`) AND per-action (`pre_shell`, `pre_file_read`,
  `post_file_edit`, `pre_mcp_call`, `post_mcp_call`) levels. Per-action
  events translate to (generic + matcher) on plugins lacking the
  specific event. Tool-specific events not in the canonical set are
  carried verbatim via the `native:<verbatim>` escape — prism doesn't
  translate; only the matching plugin emits.
- **`MCPServer.Transport` required enum** (`"stdio" | "http" | "sse"`)
  per SPEC §4.5. Plugins map to their native wire spelling (Cline
  `streamableHttp`, Continue `streamable-http`, Gemini
  `httpUrl`-vs-`url`). The canonical layer never infers transport.
- **`MCPAuth` shape** with `Scheme` (`none | bearer | header | oauth`),
  `Token`, `Headers`. OAuth is informational only (no projector
  materializes it; prism warns and the user wires OAuth in the tool's
  UI).
- **Six MCP policy fields**: `TimeoutMs`, `Disabled`, `AutoApprove`,
  `Trust`, `IncludeTools`, `ExcludeTools`. Plugins MAY honor; plugins
  without support emit an info warning when set. `Disabled` is honored
  universally by skipping emit (no warning).
- **Dimension-aware permission grammar** (SPEC §4.6.2):
  `<target>:<pattern>` and `<target>:<subaction>:<pattern>` with
  recognized targets `bash`, `Read|Write|Edit|MultiEdit`, `fs`,
  `network`, `mcp:<server>[:<tool>]`. Patterns support exact, trailing
  `*` (prefix), recursive `**`, and negation prefix `!`. Resolution
  order: **Deny > Allow > Ask > Default**. Regex patterns are rejected.
- **`extensions:` block on every primitive** + on `agents.config.yaml`
  itself (SPEC §5.1). Plugin-namespaced opaque pass-through; unknown
  plugin keys produce a typo warning. Promoting an extension to a
  canonical field is a minor bump with a one-cycle deprecation window.
- **Canonical argument substitution** for Command and Skill bodies:
  `{{arg:NAME}}`, `{{shell:CMD}}`, `{{file:PATH}}`. prism never
  evaluates substitutions at compile time — plugins rewrite to the
  target's native syntax at emit (Claude `$NAME` / `` !`...` ``;
  Gemini `{{args}}` collapse; Copilot `${input:NAME}`; etc.). Variable
  substitution `${env:VAR}` (env vars) and `${project_dir}` (project
  root) are documented per SPEC §4.5.3.
- **`Scope.IsOverride`** (SPEC §4.7) for the `AGENTS.override.md`
  semantic. Round-trips only on AGENTS.md; other plugins emit as a
  regular scope with a leading comment noting the intent so future
  round-trips can detect.
- **Virtual scopes** (SPEC §4.7.5): a Scope with `Path: ""` and
  `Globs: [...]` has no cascade home; cascade plugins handle via
  `extensions.<plugin>.virtual_hoist` (default true → hoist into
  `_virtual_rules/<Name>.md`; false → skip with warning).

### Added — schema, validation, lockfile

- **`schema_version: 2` required in `agents.config.yaml`** (SPEC §3.1.1
  + §8.1). v0.9 reads only `2`. Forward-incompat (e.g. `3` on a v0.9
  binary) is a hard error with an upgrade message. Missing
  `schema_version` is a parser warning in v0.9; phase-2 hardening
  promotes to error.
- **Lockfile `version: 2`** with Cargo-style forward-incompat error.
  Lockfiles written by a newer prism are unreadable by an older one
  ("lockfile version 3 is newer than this binary understands; upgrade
  prism or delete .agents/.lock").
- **Single pre-plugin `Validate()` pass** (SPEC §5.4) returning
  `ValidationReport{Errors, Warnings}` with `File`, `Line`, `Column`,
  `Field` (dot-path), `Severity`, `Message`. Errors block compile;
  warnings are surfaced and compile proceeds.
- **`--strict` flag on `prism check`** promotes warnings to errors for
  CI. `compile`/`diff`/`watch` honor warnings as warnings.
- **JSON Schema generation** at `schema/v2/*.schema.json` (one per
  primitive + agents.config + extensions). Draft 2020-12. Generated
  via `invopop/jsonschema` from struct tags. New `cmd/prism-schema`
  regenerates from struct definitions. Editors discover via the
  `# yaml-language-server: $schema=https://prism.dev/schema/v2/...`
  hint at the top of each example.

### Added — plugin interface (`internal/plugin/`)

- **Per-field capability declarations** (SPEC §6.2). `Plugin.Capabilities()`
  now returns a typed `Capabilities` struct that includes a
  `FieldCapabilities` map keyed by primitive + field, with values from
  the enum `{ SupportNative, SupportDegraded, SupportUnsupported,
  SupportSilent, FieldSilent }`. The engine cross-references
  per-project usage against per-plugin capabilities and surfaces a
  single coherent warning report (no more per-plugin scattered
  warnings).
- **`SupportSilent` / `FieldSilent` level**: the field is dropped
  without warning because the field is semantically meaningless on
  that target (versus `SupportUnsupported`, which warns). Used heavily
  on AGENTS.md (skill / hook fields silent, not unsupported) and on
  fields like `ModelFallbacks` that only one plugin honors.
- **`Plugin.SchemaVersion()` expectations**: plugins declare the
  schema version they understand. The engine refuses to load any
  plugin whose declared version predates the project's
  `schema_version:`.
- **`Plan()` purity invariant** preserved from v0.7: plugins never
  touch the filesystem in `Plan()`; the engine handles all I/O and
  passes existing bytes to `Merger` closures.

### Added — plugin updates

- **All 8 plugins declare per-field Capabilities** per the §12 matrix:
  agents-md, claude, cline, continue, copilot, cursor, gemini,
  windsurf. The §6.2 cross-reference replaces the v0.8 per-plugin
  warning sites.

### Added — documentation, examples

- **SPEC.md** (2772 lines) covers the seven primitives, scoping rules,
  cross-cutting concerns, plugin interface, lockfile, schema
  versioning, JSON Schema pointers, open questions, and the full
  per-field × per-plugin capability matrix (§12).
- **IMPLEMENTATION_PLAN.md** sequences the v0.9.0 cut into phases (0
  scaffolding, 1 model + parser, 2 validate, 3 plugin contract, 4
  contract tests, 5 docs + examples, 6 lockfile + cmd surface).
- **`examples/07-canonical-coverage/`**: a comprehensive fixture
  exercising every canonical field of every primitive at least once
  (the golden round-trip fixture per IMPLEMENTATION_PLAN §4.5). The
  contract test (Phase 4) uses this fixture to assert
  parse → validate → plan → emit → re-parse stability.
- **YAML language-server hints** at the top of every example's
  `agents.config.yaml` so editors auto-resolve the v2 schema.

### Changed

- **Plugin warning sites consolidated** behind the per-field
  capability cross-reference. Plugins that previously emitted ad-hoc
  warnings on each unsupported field now declare the field's level in
  `Capabilities()` and let the engine collate.
- **Cline `paths:` frontmatter on rule files** is the canonical scope
  encoding (was filename-prefix in v0.7 and earlier). The filename
  prefix is still readable by the importer for backward compat.

### Deferred / Phase 2.5 (still pending in this release)

Several Phase 2.5 plumbing items remain — the canonical schema lands
in v0.9.0 but a handful of plugin-side emit paths still need the new
shared serializer wiring:

- **Continue HOOKS** (`plugins/continue_plugin.go`). Continue grew a
  native hooks surface; the canonical model carries the events but the
  Continue plugin still emits "unsupported (warn)" for every hook
  pending the shared serializer flip. Planned for Phase 2.5.
- **Claude permission rule fan-out**: the dimension-aware `fs:` /
  `network:` / `mcp:` grammar parses canonically, but Claude's
  `Edit/Read/Write/WebFetch/mcp__*` fan-out (SPEC §4.6.3) is still
  driven by the v0.8 string-rewrite path. Migration to the shared
  emitter is Phase 2.5.
- **MCP wire shape from typed transport/auth**: plugins still hand-roll
  the JSON for the most part. The shared `mcp.WireFormat(server,
  plugin)` helper is wired in only on claude + gemini today; the
  remaining six plugins continue to use their v0.8 path.
- **Cline filename-dispatch hooks emission rewrite**: per SPEC §4.4.5,
  Cline writes one executable script per `(event, matcher)` pair with
  guard-clause inlining. The current Cline plugin still emits the v0.8
  JSON-block shape; the filename-dispatch rewrite is Phase 2.5.
- **Cursor cross-emit dedup + per-project warning**: Cursor reads
  `.claude/agents/` directly when present. SPEC §4.1.4 says Cursor
  should suppress `.cursor/agents/` emission when claude is also a
  target, with **one info warning per project**. v0.9.0 still
  double-emits.
- **Gemini per-action event translation via shared map**: §4.4.4's
  translation table is hand-maintained in the Gemini plugin today; the
  shared `hookevents.TranslateForPlugin(event, plugin)` helper is
  wired into the canonical model but the Gemini plugin still uses its
  v0.8 inline switch.
- **AGENTS.override.md filename split**: the AGENTS.md plugin
  recognizes `IsOverride` but emits everything as `AGENTS.md` for now;
  the filename split (`AGENTS.override.md` vs `AGENTS.md`) is Phase
  2.5.

These items are tracked as v0.9.x follow-ups; none of them block the
canonical-schema contract.

## v0.8.2

Three cells flipped: **copilot HOOKS** (preview, opt-in), **copilot PERMS**
(wrapper, preview-gated), and **cline PERMS** (wrapper, always-on). Closes
the three deferrals tracked in v0.8.1's "Sequenced for v0.8.2+" list. Issues
#2 (cline perms-via-hook), #3 (copilot perms-via-hook), and #4 (copilot
preview hooks).

### Added
- **`--enable-preview-hooks` persistent flag** (`cmd/prism/root.go`). Opts
  into Copilot's preview-stage hook surface — `.github/hooks/hooks.json`
  + the perms-guard wiring layered on top of it. Off by default since the
  preview JSON schema can still drift before GA. Flag is persistent so
  subcommands (`compile`, `check`, `diff`, `watch`) all honour it without
  per-subcommand wiring.
- **Copilot preview hooks** (`plugins/copilot.go`,
  `internal/importer/copilot.go`). When the flag is on, `proj.Hooks` projects
  to `.github/hooks/hooks.json` using the preview event-name mapping
  (`PreToolUse` → `preToolUse`, `SessionStart` → `sessionStart`,
  `UserPromptSubmit` → `userPromptSubmitted`, etc.). Scoped hooks reuse the
  existing `prism scope-guard` wrapper at
  `.github/hooks/__scope-guard__/<scope>-<event>-<hook>.sh` — Copilot's
  preview hook contract is JSON-on-stdin with exit-2-blocks, matching the
  Claude/Cline/Gemini shape the wrapper already handles. Hook commands in
  `hooks.json` use `${PROJECT_DIR}/<rel>` so the projection survives `mv`
  of the project tree (Copilot honours the same env var Claude does).
  Importer round-trips the file back into `model.Hook`, filtering wrapper
  paths as projection artifacts.
- **Copilot perms-via-hook** (`plugins/copilot.go`,
  `internal/importer/copilot.go`). Re-uses `emitPermsGuardWrappers` (now via
  the new `emitPermsGuardWrappersAt` entry point that takes an explicit
  hooks root, since copilot writes under `.github/hooks` rather than
  `.copilot/hooks`). Wraps every user hook with a per-hook perms-guard
  script + sidecar `policy.json`; when no user hooks exist, emits a bare
  `global-gate.sh` and appends it as a `preToolUse` entry in `hooks.json`.
  Importer reads `.github/hooks/__perms-guard__/policy.json` (and any
  `<scope>.policy.json` siblings) back into `model.Permissions` /
  `model.ScopedPermissions`.
- **Cline perms-via-hook** (`plugins/cline.go`,
  `internal/importer/cline.go`). Same wrapper pattern as gemini/copilot,
  but always-on (Cline's hooks are GA, no preview flag needed). Hook
  commands in `.clinerules/hooks/PreToolUse.json` are rewritten to point
  at per-hook perms-guard wrappers when permissions exist; bare gates fall
  back to standalone entries. Coexists cleanly with user-authored hooks
  (the wrapper invokes the user script on allow).
- **`emitPermsGuardWrappersAt` helper** (`plugins/perms_wrapper.go`). New
  entry point that takes an explicit `hooksRoot` so plugins whose hook
  config lives outside their plugin-named dotdir (copilot → `.github/hooks`)
  can re-use the wrapper machinery. The existing `emitPermsGuardWrappers`
  is now a thin shim over `emitPermsGuardWrappersAt` that defaults
  `hooksRoot` to `.<pluginName>/hooks`. Gemini/cline callers are
  source-compatible (they keep using the simpler name).
- **`readPermsGuardSidecars` importer helper** (`internal/importer/helpers.go`).
  Reads the perms-guard policy JSON sidecars (`policy.json` and
  `<scope>.policy.json`) into `model.Permissions` / `model.ScopedPermissions`.
  Shared by cline and copilot importers. Accepts either JSON or YAML
  defensively in case users hand-edit.
- **Round-trip coverage** (`internal/engine/roundtrip_v08_test.go`): two
  new tests — `TestRoundTrip_Cline_V082_Perms` and
  `TestRoundTrip_Copilot_V082_PreviewHooks` — lock the new emissions
  closed. Existing v0.8.0/v0.8.1 round-trips remain to preserve the
  legacy paths.

### Changed
- **`CopilotPlugin` struct** gains `DisableHookWrappers` and
  `EnablePreviewHooks` bool fields, both wired through
  `cmd/prism/plugins.go`. `ClinePlugin` was already structured this way
  (only needed the registry switch from `NewCline()` to the explicit
  struct literal so `DisableHookWrappers` propagates).
- **Capability matrix** (`prism capabilities`):
  - `cline    PERMS  ----  →  native`
  - `copilot  HOOKS  ----  →  native` (when `--enable-preview-hooks`;
    `----` otherwise)
  - `copilot  PERMS  ----  →  native` (when `--enable-preview-hooks`;
    `----` otherwise)
- **`internal/version.Version` bumped to `0.8.2`**.

### Notes
- The `--enable-preview-hooks` flag is the ONLY thing keeping Copilot
  hooks/perms off by default. The on-disk artifacts (hooks.json, policy
  sidecar, wrappers) are believed-stable, but the Copilot JSON schema is
  still in public preview at the GitHub side. When the schema GAs we'll
  drop the flag and flip the default. The default-off behaviour preserves
  the v0.8.1 contract for users who haven't opted in.
- Cline's perms-via-hook flips on by default (no flag). Cline's hook
  surface is GA, and the wrapper pattern was already validated through
  gemini in v0.8.0 — no preview risk to gate on.

## v0.8.1

Importer parity for the v0.8.0 emissions, helper consolidation, and a
nit sweep from the v0.8.0 reviewer. Closes the silent-data-loss path
where `prism init --from <tool>` against a v0.8-projected repo dropped
the new primitives (agents, native skills, hooks, MCP, workflows,
permissions).

### Fixed
- **Importer parity for all six rewritten plugins** (the headline fix —
  silent data loss on round-trip).
  - **cursor** (`internal/importer/cursor.go`): reads `.cursor/agents/`,
    `.cursor/skills/<dir>/SKILL.md`, `.cursor/commands/`, and
    `.cursor/hooks.json`. Wrapper paths under `__scope-guard__/` are
    skipped (projection artifacts, not source-of-truth hooks).
  - **cline** (`internal/importer/cline.go`): reads
    `.clinerules/workflows/<n>.md` (the v0.8 native slash-command form
    that replaced `30-command-*.md`), `.clinerules/hooks/<event>.json`,
    and `.cline/cline_mcp_settings.json`. The 30-command-* prefix is
    still recognized for backward compat; workflows take precedence.
  - **gemini** (`internal/importer/gemini.go`): reads `.gemini/agents/`,
    `.gemini/commands/<n>.toml` (extracts the `prompt` body and
    description, reverses TOML triple-quote escaping), and the `hooks`
    block in `.gemini/settings.json`.
  - **windsurf** (`internal/importer/windsurf.go`): reads
    `.windsurf/hooks.json` (strips the `bash '...'` wrapper the plugin
    adds) and `.windsurf/mcp_config.json`.
  - **copilot** (`internal/importer/copilot.go`): reads
    `.github/agents/<slug>.agent.md`, `.github/mcp.json`, and root
    `.mcp.json` (the Copilot CLI walks cwd→git-root loading every
    `.mcp.json` — project-local overrides root by name).
  - **continue** (`internal/importer/continue.go`): reads
    `.continue/prompts/<n>.md` and `.continue/permissions.yaml`. The
    permissions translation reverses the plugin's
    `tool:pattern` → `Tool(pattern)` rewrite.
- **`sanitizeTOMLTripleQuoted` was lossy for 4+ consecutive quotes**
  (`plugins/gemini.go`): the prior `"""` → `""\"` form produced
  `""\""` when followed by a bare `"`, which TOML reads as `""` plus
  a stray `\""` (premature termination). The replacement walks every
  run of three-or-more consecutive quotes and backslash-escapes each
  quote individually. Locked by `TestGemini_Command_FourQuoteEscape`.
- **Windsurf hooks Sources append could mutate the input slice** if
  cap > len (`plugins/windsurf.go`): switched to a defensive copy via
  `append([]string{}, "hooks.yaml")`.

### Added
- **mapClineEvent**: `plugins/cline.go` now translates Claude-style
  hook event aliases (`SessionStart` → `TaskStart`, `Stop` →
  `TaskComplete`) into Cline's native event names. Cline-native event
  names (TaskStart, TaskResume, UserPromptSubmit, PreToolUse,
  PostToolUse, TaskComplete, TaskCancel) pass through verbatim.
  Documented in the plugin doc comment alongside the mapping table.
- **`plugins/frontmatter.go`**: shared `renderYAMLScalar(s string)`
  and `renderGlobs(globs []string)` helpers that JSON-marshal as a
  YAML flow scalar/array. Replaces `yamlScalar` (cline.go), `yamlQuote`
  (copilot.go), and 7 inline `json.Marshal(...)` sites across cursor,
  continue, gemini, windsurf, and cursor's skill renderer. Unifies the
  (slightly different) escaping rules; everything is now JSON-quoted
  which is more conservative but always valid YAML.
- **Round-trip v0.8 coverage** (`internal/engine/roundtrip_v08_test.go`):
  six new tests seeding v0.8-shape inputs (`.cursor/agents/`,
  `.cursor/skills/`, `.cursor/hooks.json`,
  `.cline/cline_mcp_settings.json`, `.clinerules/workflows/`,
  `.clinerules/hooks/`, `.windsurf/hooks.json`, `.windsurf/mcp_config.json`,
  `.gemini/agents/`, `.gemini/commands/*.toml`, hooks in settings.json,
  `.github/agents/`, `.continue/prompts/`, `.continue/permissions.yaml`).
  The existing v0.7-shape round-trips remain to lock the legacy paths.

### Changed
- **Hook portability doc notes** (`plugins/cursor.go`, `plugins/cline.go`,
  `plugins/windsurf.go`, `plugins/gemini.go`): added comment headers
  explaining that cursor/cline/windsurf hooks.json have no
  `${PROJECT_DIR}`-style substitution (wrapper paths bake in as
  absolute; `mv` of the project requires re-running `prism compile`).
  Gemini's settings.json hooks DO support env-var interpolation, so
  the `${PROJECT_DIR}/<rel>` form there survives a `mv`.
- **Continue plugin MCP loop**: collapsed the two passes over `proj.MCP`
  (one for ops, one for warnings) into a single pass that emits both
  in the same iteration. No behavior change.
- **`internal/version.Version` bumped to `0.8.1`**. (v0.8.0 was tagged
  with the version literal still at `0.7.3` — this release also fixes
  that drift.)

### Sequenced for v0.8.2+

- **Copilot preview hooks**: gate behind `--enable-preview-hooks` when
  GA lands.
- **Cursor PERMS via sandbox profile** (`sandbox.json` domain
  allowlists): could upgrade Permissions from `----` to `native`.
- **Cline + Copilot PERMS via PreToolUse hooks**: both now have native
  hooks, so perms could be enforced via the perms-guard wrapper pattern.

## v0.8.0

Major plugin parity release. **Six of eight plugins** rewritten to match each
tool's May 2026 feature surface. The capability matrix went from sparse
"context-and-rules" coverage to honest "what the tool can actually accept"
coverage. **17 cells flipped from `----` or `degr.` to `native`**.

### The matrix, before and after

```
v0.7.3                                       v0.8.0
PLUGIN     SKILLS CMDS AGENTS HOOKS MCP      PLUGIN     SKILLS CMDS AGENTS HOOKS MCP
cline      degr.  degr.----   ----  ----     cline      degr.  nat. ----   nat.  nat.
continue   degr.  degr.----   ----  nat.     continue   degr.  nat. ----   ----  nat.
copilot    degr.  nat. ----   ----  ----     copilot    degr.  nat. nat.   ----  nat.
cursor     degr.  degr.----   ----  nat.     cursor     nat.   nat. nat.   nat.  nat.
gemini     ----   ----  ----  ----  nat.     gemini     degr.  nat. nat.   nat.  nat.
windsurf   degr.  degr.----   ----  ----     windsurf   degr.  degr.----   nat.  nat.
```

(claude unchanged; agents-md unchanged — both already accurate.)

### Added — per plugin

- **gemini** (`plugins/gemini.go` full rewrite): `.gemini/agents/<name>.md`
  (markdown + YAML frontmatter — `name`, `description`, `tools` with wildcards,
  `model`, `temperature`, `max_turns`, `max_timeout`), `.gemini/commands/<name>.toml`
  (TOML, not markdown — Gemini's slash-command format), and a `hooks` block in
  `.gemini/settings.json` covering all 11 Cascade event types. Skills now
  project AS agents with trigger + globs appended to the description (loses
  auto-glob activation; emits info warning). Scoped hooks reuse the existing
  `prism scope-guard` wrapper unchanged — Gemini's hook contract (JSON on
  stdin, exit-2 blocks) matches Claude's exactly.
- **cursor** (`plugins/cursor.go` full rewrite): `.cursor/hooks.json` (Cursor
  2.4+ events: sessionStart/End, preToolUse/postToolUse, beforeShellExecution,
  afterFileEdit, beforeSubmitPrompt, stop, plus Tab hooks), `.cursor/agents/<name>.md`
  (frontmatter: name, description, model, readonly, is_background),
  `.cursor/commands/<name>.md` (bare markdown — filename = slash command),
  `.cursor/skills/<dir>/SKILL.md` (Cursor 2.4 native skills format with
  YAML frontmatter). Phase F added `renderCursorSkillBody` to re-render
  frontmatter from canonical Skill struct fields (parser strips frontmatter
  into struct fields; plugin must reconstruct).
- **cline** (`plugins/cline.go` full rewrite): MCP via
  `.cline/cline_mcp_settings.json` (project-local; canonical user path is
  `~/.cline/data/settings/cline_mcp_settings.json` — document the symlink
  workaround), hooks via `.clinerules/hooks/<event>.json` (TaskStart/Resume,
  UserPromptSubmit, PreToolUse/PostToolUse, TaskComplete/Cancel), workflows
  via `.clinerules/workflows/<name>.md` (replaces `30-command-*` degraded form),
  and YAML-frontmatter `paths:` globs on individual rule files (replaces
  filename-prefix scope encoding for path targeting).
- **windsurf** (`plugins/windsurf.go`): `.windsurf/hooks.json` with all 12
  Cascade hook types (5 pre-hooks that can block, 4 post-hooks, plus
  post_cascade_response, post_cascade_response_with_transcript,
  post_setup_worktree). Claude's PreToolUse/PostToolUse map to Cascade events
  via matcher inspection (bash→run, read→read, write→write, mcp→mcp).
  `.windsurf/mcp_config.json` (project-local; canonical user path is
  `~/.codeium/windsurf/mcp_config.json` — info warning with activation hint).
- **copilot** (`plugins/copilot.go`): `.github/agents/<slug>.agent.md`
  (GA in May 2026 — frontmatter: description, name, tools, model, agents,
  user-invocable, handoffs), `.github/mcp.json` (CLI walks cwd→git-root
  loading every `.mcp.json` it finds; closer wins). When `claude` target is
  also enabled, suppresses agent emission per-agent with info warning
  since VS Code/Copilot auto-discovers `.claude/agents/` already. Hooks
  remain unsupported (preview status; deferred to v0.8.1).
- **continue** (`plugins/continue_plugin.go` full rewrite): native
  `.continue/permissions.yaml` (replaces the perms-guard wrapper for continue
  — wrapper code path retained for gemini), native `.continue/prompts/<name>.md`
  slash commands (frontmatter: name, description, invokable: true). Permissions
  translation table: `bash:rm *` → `Bash(rm *)`, `read` → `Read`, etc., with
  a deprecation warning when pattern syntax doesn't round-trip cleanly.

### Changed

- **Cursor skills format**: stopped emitting the legacy
  `.cursor/rules/skill-<name>.mdc` degraded form. Cursor 2.4+ users get
  `.cursor/skills/<dir>/SKILL.md` exclusively. Documented in plugin doc
  comment; users on Cursor < 2.4 should pin v0.7.x or hand-author rules.
- **Cline rules**: now use YAML frontmatter `paths:` glob arrays for path
  targeting (was filename-prefix scope encoding). Backward compatible —
  importer still reads the prefix scheme.
- **Continue plugin no longer uses `plugins/perms_wrapper.go`**: native
  `.continue/permissions.yaml` replaces the wrapper script. The wrapper
  code stays in place for gemini until gemini's own native path matures.
  `ContinuePlugin.DisableHookWrappers` field removed (no wrappers to disable).

### Plugin-internal Capabilities() flips

- cline: ScopePaths+ScopeSemantic degr→native; Commands degr→native;
  Hooks ----→native; MCP ----→native.
- continue: Commands degr→native; Permissions was native-labeled, now actually native.
- copilot: Agents ----→native; MCP ----→native.
- cursor: Skills degr→native; Commands degr→native; Agents ----→native;
  Hooks ----→native.
- gemini: Skills ----→degr (projected as agents); Commands ----→native;
  Agents ----→native; Hooks ----→native.
- windsurf: Hooks ----→native; MCP ----→native.

### Known carryovers (v0.8.1+)

- **Importer parity**: the new emissions need matching importer paths so
  `prism init --from <tool>` picks up `.cursor/agents/`, `.cursor/skills/`,
  `.cline/cline_mcp_settings.json`, etc. Today the importers only know about
  the v0.7.x source shapes.
- **Cursor PERMS via sandbox profile**: sandbox.json domain allowlists could
  upgrade PERMS from `----` to `native`. Non-trivial — deferred.
- **Cline + Copilot PERMS via hooks**: now that both have native hooks, perms
  could be enforced via PreToolUse-hook wrappers (perms-guard pattern).
- **Copilot hooks (preview)**: gate behind an `--enable-preview-hooks` flag
  when GA lands.
- **Continue subagents**: file-shape vs config-shape mismatch — Continue
  agents live in `~/.continue/agents/*.yaml` (YAML, composing models +
  rules + tools), not markdown. A real translator is non-trivial.

## v0.7.3

Documentation + tooling round. README rewritten for v0.7.x features,
runnable `examples/` tree added, and a binary-name fix that unblocks
`go install`.

### Fixed
- **Binary defaulted to `agents` instead of `prism` on `go install` /
  bare `go build`**: the command package lived at `cmd/agents/`, so
  `go install agents.dev/agents/cmd/agents@latest` (and `go build
  ./cmd/agents`) produced a binary named `agents` on PATH. The v0.7.x
  scope-guard and perms-guard wrappers exec `prism` at hook-firing time
  — without a binary by that name, the wrappers would fail. Renamed
  `cmd/agents/` → `cmd/prism/` so the default name now matches what
  wrappers expect. Module path stays `agents.dev/agents` (that's the
  Go import path; only the command directory moved).
  - `go install agents.dev/agents/cmd/prism@latest` → installs as `prism`
  - `go build -o prism ./cmd/prism` → same
  - Release artifacts unchanged (CI matrix already builds `-o
    prism-${version}-${platform}`).
- **Cobra `Use` and `Long` strings updated** from "agents" to "prism" so
  `prism --help` shows the correct command name in usage lines.
- **Lockfile `generated_by` updated** from `agents@<ver>` to
  `prism@<ver>` for consistency. Plays through on the next `compile`;
  not a breaking schema change (the field is a free-form string).

### Added
- **`README.md` rewritten** for v0.7.x: covers importers, layered
  config, `@include`-style composition (with correct `<!-- include:
  path -->` syntax), central registry, `--interactive` importers,
  scope-guard / perms-guard wrappers (with policy-rule grammar),
  releases, and current capability matrix. Stale "TODO" items from
  the v0.4 README removed.
- **`examples/` directory** with 6 runnable `.agents/` layouts:
  - `01-minimal/` — bare-minimum getting started
  - `02-scoped-skills/` — scoped context + path-bound skill
  - `03-include-composition/` — `<!-- include: path -->` directive
  - `04-mcp-servers/` — MCP config across plugins
  - `05-hooks-with-scope-guard/` — scoped Claude hook via scope-guard wrapper
  - `06-permissions-wrappers/` — perms-guard wrapper for Gemini/Continue
  Each is self-contained — `cd examples/N-name && prism compile` works.
  All six verified end-to-end (compile + check + drift-free).

## v0.7.2

Nit sweep on top of v0.7.1. Three small fixes from the v0.7.1 reviewer's
deferred list, all with regression tests.

### Fixed
- **`rootRelativeFromWrapper` not defensive against noisy inputs** (N-a):
  `plugins/perms_wrapper.go` now `filepath.Clean`s `wrapperRel` before
  splitting on `/`. Inputs like `.gemini/./hooks/wrapper.sh` or
  `.gemini//hooks/wrapper.sh` now produce the canonical `../..` instead
  of the over-counted depth. Behavior unchanged for callers that already
  go through `filepath.Join` (which Cleans), but the function is now safe
  for direct unit-test inputs. Locked by 3 new cases in
  `TestRootRelativeFromWrapper`.
- **Comment-line injection in scope-guard wrapper** (N-b): the
  `# prism-generated scope guard for ...` header in
  `plugins/claude.go:buildScopeGuardScript` interpolated `scopePath` and
  the source-script basename unescaped. A scope path or filename
  containing `\n` or `\r` could split the comment into a second line
  that bash would interpret. New `sanitizeBashComment` helper replaces
  control chars with `?`. Pre-existing (not introduced by v0.7.x);
  flagged by the v0.7.1 reviewer for closure. Locked by
  `TestBuildScopeGuardScript_SanitizesCommentHeader_Nb`.
- **User-declined error not addressable via `errors.Is`** (N-c):
  `cmd/agents/perms_guard.go` had an inline `errors.New("perms-guard:
  user declined")` at the ask-rule TTY-decline site. Extracted to
  package-level `errPermsGuardUserDeclined` so callers can distinguish
  user-decline from policy-deny via `errors.Is`. Locked by
  `TestPermsGuard_UserDeclined_ErrorsIs_Nc` (positive match, wrapping,
  and negative test that a freshly-allocated error with the same string
  does NOT match — the point of the sentinel).

### Changed
- `internal/version.Version` bumped to `0.7.2`. Single source of truth;
  picked up by `internal/registry/index.go`'s User-Agent and
  `internal/engine/compile.go`'s lockfile `GeneratedBy` automatically.

## v0.7.1

A fixes/polish release on top of v0.7.0. Addresses the three Important
items + five Nits flagged by the v0.7.0 reviewer, plus expanded round-trip
coverage now that the YAML colon bug is fixed.

### Fixed
- **Dual `Merger` invocation TOCTOU window** (I2 from v0.7.0 review):
  `internal/engine/compile.go` now sets `op.Merger = nil` after resolving
  the closure, so `internal/apply/apply.go` writes `op.Content` directly
  instead of re-invoking `Merger`. The previous design ran Merger twice —
  once in compile (for lockfile hashing) and once in apply (for the
  write) — which under concurrent edits could see different `existing`
  bytes and produce divergent merged output, triggering a false-positive
  "manual edits detected" loop on the next compile. apply.go retains the
  Merger fallback for direct callers that bypass engine.Compile. Locked
  by `TestCompile_MergerInvokedOnce` (atomic-counter Merger asserts call
  count == 1).
- **Wrapper scripts bake absolute paths** (I4): both `plugins/perms_wrapper.go`
  (`buildPermsGuardScript`) and `plugins/claude.go` (`buildScopeGuardScript`)
  used to hardcode `proj.Root` into the generated bash bodies, so `mv` /
  `rsync` / container mounts would leave the wrappers pointing at the old
  path. The renderers now emit bash that resolves PROJECT_DIR at runtime
  via `${PRISM_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$(cd "${SCRIPT_DIR}/../../.." && pwd)}}`,
  where SCRIPT_DIR is `dirname "${BASH_SOURCE[0]}"`. The scope-guard
  wrapper also exports `CLAUDE_PROJECT_DIR=${PROJECT_DIR}` so the
  existing `prism scope-guard` runtime lookup inherits the resolved root.
  Env-var precedence: `PRISM_PROJECT_DIR` > `CLAUDE_PROJECT_DIR` >
  `${BASH_SOURCE[0]}`-relative fallback. End-to-end locked by
  `TestPermsGuardWrapper_SurvivesMv` (writes a wrapper, renames the
  project tree, executes the wrapper from a third directory, verifies
  PROJECT_DIR resolves to the moved location).
- **Non-deterministic op order for gemini/continue perms wrappers** (I5):
  `plugins/perms_wrapper.go` iterated `policyPaths` (a Go map) when
  emitting bare-gate wrappers for projects with multiple scoped
  permissions but no hooks, producing non-deterministic `agents diff`
  output (lockfile was still stable). Sorted the keys before iteration.
  Locked by a 20-run determinism test.
- **`cmd/agents/perms_guard.go` used `os.Exit` inside cobra `RunE`** (N1):
  four `os.Exit` calls replaced with `return err`. Exit-code semantics
  simplified — any failure now exits 1 (was: deny=1, forked-subcommand
  err=child-exit-code). Documented in the function doc comment. The new
  approach is testable via `cobra.Execute` (5 new tests in
  `cmd/agents/perms_guard_test.go` drive the subcommand via `stubStdin`
  + cobra harness without spawning a subprocess).
- **`http.Client` in `internal/registry/index.go` missing User-Agent** (N2):
  fetches now send `User-Agent: prism/<Version> (+https://github.com/os-tack/prism)`.
- **Registry cache file world-readable** (N3): cache write mode changed
  from `0o644` to `0o600` since the file may contain registry metadata
  the cache owner doesn't want exposed on multi-user systems.
- **Version literal drift** (v0.7.1 review C1): consolidated the duplicate
  `"0.7.1"` literals in `internal/engine/compile.go` and the new
  `internal/version` package — compile.go now imports
  `agents.dev/agents/internal/version` and uses `version.Version`. One
  literal to bump for v0.7.2.
- **Wrapper-script shell injection on policy path** (v0.7.1 review C2):
  `plugins/perms_wrapper.go` interpolated `policyRel` into bash unquoted,
  so a scope path containing `$x` or whitespace would either undergo
  parameter expansion or word-split. Now uses `shellQuote(policyRel)`
  (single-quoted, regardless of input) with `"${PROJECT_DIR}"/` adjacency
  for the runtime root prefix. Locked by
  `TestBuildPermsGuardScript_PolicyShellEscaping_C2`.
- **Wrapper `--script` argument also baked absolute paths** (v0.7.1
  review I-1): the I4 fix made `${PROJECT_DIR}` resolution survive `mv`,
  but the `--script` argument still hardcoded the project-root prefix.
  New `formatScriptArg(scriptPath, projRoot)` helper rewrites scripts
  living under `proj.Root` to `"${PROJECT_DIR}"/<rel>` (falls back to
  `shellQuote(absolute)` for global hooks from `~/.agents/`). The
  "survives mv" claim now holds for both the PROJECT_DIR resolver AND
  the underlying script reference.
- **`perms-guard` script-fork exit code collapsed to 1** (v0.7.1 review
  I-2): mirroring the v0.7.0 N1 fix removed `os.Exit` from RunE, which
  also lost Claude Code's exit-2 ("block with stderr") signal on the
  script-fork path. Restored exit-code preservation via a testable
  `permsGuardExit` indirection (production: `os.Exit(code)`; tests:
  recorder hook). Deny / ask-decline paths still `return err` →
  cobra-exit-1. Mirrors `scope_guard.go`'s behavior. Locked by
  `TestPermsGuard_ScriptFailureExitsWithChildCode`.

### Added
- **`internal/version` package**: shared `Version = "0.7.1"` constant
  consumed by `internal/registry/index.go` (User-Agent header) and
  `internal/engine/compile.go` (lockfile `GeneratedBy` field). Single
  source of truth; future bumps update one literal.
- **Round-trip command coverage for windsurf and continue**
  (`internal/engine/roundtrip_test.go`): both tests now include a
  `manual` / command fixture with a colon-bearing description
  (`"Ship a release: cuts a tag, builds artifacts, publishes"`). This
  closes the v0.7.0 reviewer's flagged coverage gap and provides ongoing
  regression protection for the v0.7.0 YAML colon fix. (The Continue
  importer has no command primitive in its source format, so its
  round-trip is structurally asymmetric: Init seeds .agents/ from the
  Continue source, the test writes `.agents/commands/deploy.md`
  directly, then Compile asserts the windsurf-style output.)
- **Doc comment for `extractToolAndAction` tool→key mapping** (N5) in
  `cmd/agents/perms_guard.go`: per-tool key list (command / file_path /
  path / filepath / notebook_path / url) is now part of the wrapper-script
  contract documentation.
- **`--interactive` flag help bolstered** (N4): `cmd/agents/init.go`
  mentions EOF=accept-all and non-TTY refusal. `interactive.go` doc
  comment got concrete CLI examples (Ctrl-D, non-newline-terminated
  pipe, closed pipe mid-stream) and the "fail safe / over-include"
  rationale.

### Changed
- **Wrapper-renderer signatures** (`plugins/perms_wrapper.go`,
  `plugins/claude.go`): `buildPermsGuardScript` and `buildScopeGuardScript`
  now take a `wrapperRel` first argument so the renderer can compute the
  correct `../..` depth via the new `rootRelativeFromWrapper` helper.
  Callers in the same packages updated. No external API impact.

## v0.7.0

### Added
- **Central registry resolution for `agents add`** (`internal/registry/index.go`,
  `cmd/agents/add.go`): bare names like `agents add billing-skills` now
  resolve through a JSON index instead of requiring a full
  `host/owner/repo` URL. Default index URL is
  `https://raw.githubusercontent.com/os-tack/prism-registry/main/index.json`;
  override with `--registry <url>` or `PRISM_REGISTRY` env var. Cache lives
  at `os.UserCacheDir() + "/prism/registry-index.json"` with a 24h TTL.
  New flags: `--registry`, `--refresh-registry`, `--no-fetch`. Index shape:
  ```json
  {
    "version": 1,
    "packages": {
      "billing-skills": {
        "source": "github.com/os-tack/prism-pkg-billing-skills",
        "default_ref": "v1.0.0",
        "description": "Billing-domain skills bundle"
      }
    }
  }
  ```
  The `prism-registry` repo is a v0.7+ deliverable; resolution layer ships
  now with offline-friendly cache fallback and clear errors when the index
  isn't reachable.
- **`--interactive` / `-i` on `agents init`** (`internal/engine/interactive.go`,
  `cmd/agents/init.go`): `agents init --from <tool> -i` prompts per-item
  (skills, commands, scopes, MCP servers, agents) with `Y/n/a/d` (plus `s`
  on scopes to skip the scope's children). EOF → accept-all. Non-TTY stdin
  is refused loudly (`ErrInteractiveNoTTY`) rather than silently filtering
  nothing in CI. Declining a scope cascades to its children — no "you
  already said no" noise.
- **Round-trip coverage for gemini, cline, windsurf, agents-md**
  (`internal/engine/roundtrip_test.go`): the four importers deferred from
  v0.6 are now end-to-end tested. Existing claude/continue/copilot tests
  upgraded with the Cursor-style body-sweep pattern (loop over every
  original body string, assert it appears somewhere in the post-compile
  output) so silently-dropped content is caught.
- **Permissions wrapper for Gemini and Continue** (`plugins/gemini.go`,
  `plugins/continue_plugin.go`): both plugins now project
  `model.Permissions` to a perms-guard wrapper + sidecar JSON policy
  instead of just logging a warning. Capabilities matrix for both moved
  `Permissions: SupportUnsupported` → `Permissions: SupportNative`.
- **`prism perms-guard` hidden subcommand** (`cmd/agents/perms_guard.go`):
  runtime enforcement called from the generated wrapper scripts. Reads
  Claude-style hook JSON from stdin, derives `(tool_name, action)`,
  consults the sidecar policy, and either exec's the underlying hook
  (allow), exits non-zero (deny), or prompts on a TTY (ask).
- **`internal/perms` package**: shared policy load/check used by
  `perms-guard` and the projection plugins. JSON shape:
  ```json
  {
    "allow": ["bash:ls *", "bash:cat *"],
    "deny":  ["bash:rm -rf *", "bash:curl *"],
    "ask":   ["bash:git *"]
  }
  ```
  Each rule is `<tool>:<pattern>`. Tool match is case-insensitive. The
  pattern supports exact match (`bash:ls`) and trailing-wildcard glob
  (`bash:git *` matches any bash action beginning with `git `). Rules
  with no colon are tool-only matchers. Deny dominates Allow, which
  dominates Ask; no match returns the default (the wrapper allows).
- **`DisableHookWrappers` on `GeminiPlugin` and `ContinuePlugin`**:
  mirrors `ClaudePlugin.DisableHookWrappers`. The existing
  `--no-hook-wrappers` CLI flag now suppresses perms-guard wrappers for
  these plugins too.

### Changed
- **OpMerge contract** (`internal/plugin/plugin.go`, `internal/apply/apply.go`,
  `plugins/claude.go`, `plugins/gemini.go`, `plugins/cursor.go`): plugins no
  longer read existing files from disk inside `Plan()`. Instead,
  `plugin.Operation` got a new `Merger func(existing []byte) (string, error)`
  field; the engine reads the existing bytes (or nil if absent) and calls
  the closure. The three plugins that previously violated purity in their
  Plan() — claude `buildSettingsOp`, gemini `buildGeminiSettingsOp`, cursor
  `buildMCPOp` — now use Merger closures. **Back-compat**: an `OpMerge` op
  with `Merger == nil` still works (engine falls back to `op.Content`), so
  third-party plugins built against the v0.6 contract keep working.
  `compile.go` also calls Merger to materialize `op.Content` for lockfile
  hashing, so Merger closures must be deterministic (given the same
  `existing`, return the same bytes).
- **`plugin.Registry.Register` returns an error** instead of panicking on
  duplicate. Mirrors `importer.Registry.Register` (already error-returning
  in v0.6). Cascades: `registerPlugins` in `cmd/agents/plugins.go`,
  `cliState.ensureRegistry()`, and `cliState.options(...)` all now return
  error. All subcommand `RunE` handlers propagate it. The TODO(v0.7) at
  `internal/plugin/plugin.go:126` is removed.
- **`uniqueName` extracted** from `internal/importer/cursor.go` into a new
  `internal/importer/helpers.go` (4 importers were calling it across files).
  Added a `caller string` parameter so the stderr cap-warning identifies
  which importer hit it (cursor / copilot / windsurf / continue).

### On-disk layout for permissions projection

Wrapper scripts and sidecar JSON live under
`.{plugin}/hooks/__perms-guard__/`:

  - `policy.json` — global Permissions block
  - `<scope-slug>.policy.json` — per-scope Permissions block
  - `<event>-<basename>.sh` — wrapper per global hook
  - `<scope-slug>-<event>-<basename>.sh` — wrapper per scoped hook
  - `global-gate.sh` / `<scope-slug>-gate.sh` — bare gate when no hooks
    exist; users can wire it into their tool's pipeline manually

### Fixed
- **Windsurf and Continue YAML colon bug** (`plugins/windsurf.go`,
  `plugins/continue_plugin.go`): both plugins emitted raw unquoted YAML
  scalars for skill/command descriptions, so any description containing a
  colon (e.g., `Ship a release: cuts a tag`) produced invalid YAML. Both
  renderers now `json.Marshal` the description before writing (a JSON-quoted
  string is also a valid YAML flow scalar). Regression tests
  (`TestWindsurf_Skill_DescriptionWithColon_YAMLValid`,
  `TestContinue_Skill_DescriptionWithColon_YAMLValid`) lock the contract.
- **`roundtrip_test.go` was dropping `Register` errors silently** (the only
  bare `Register` call in the repo). Now uses the standard `if err :=
  reg.Register(p); err != nil { t.Fatalf(...) }` pattern.

### Known issues (deferred to v0.7.1)

- **Dual Merger invocation** (`internal/engine/compile.go` + `internal/apply/apply.go`):
  the engine reads existing bytes and runs `Merger` in both compile (to
  materialize `op.Content` for lockfile hashing) and apply (the actual
  write). Under concurrent edits between those two reads, the on-disk hash
  and lockfile hash can disagree, producing a false-positive
  "manual edits detected" loop on the next `agents compile`. Window is
  microseconds in a one-shot compile; only a concern for `agents watch`.
- **Wrapper scripts bake `proj.Root` absolute paths** into the generated
  bash wrapper bodies (`plugins/perms_wrapper.go`, `plugins/claude.go`).
  Moving the project root via `mv` / `rsync` / container mount leaves the
  wrappers pointing at the old path. **Workaround:** rerun
  `agents compile` after relocating the project.
- **`agents diff` op ordering for gemini/continue perms wrappers is
  non-deterministic** when only multiple scoped permissions exist
  (`plugins/perms_wrapper.go` iterates a Go map). The lockfile is still
  deterministic (keyed by Path); only the report output drifts.

## v0.6.0

### Added
- **Windows path handling**: `Operation.Path` is now consistently
  forward-slashed engine-side, so lockfile keys are portable across
  macOS/Linux/Windows builds. Legacy backslash lockfiles from earlier
  Windows runs are reconciled on read (no spurious "stale" deletes).
- **Symlink fallback on Windows**: when `runtime.GOOS == "windows"`,
  `OpSymlink` ops downgrade to `OpWrite` automatically (the symlink
  target's bytes are read in and written). Windows users without
  developer mode (where `os.Symlink` fails) now get a working content
  copy instead of an apply error.
- **Per-file SHA-256 hashes in `.agents/packages.yaml`**: `agents
  remove` now precisely identifies which package files have been
  manually edited and preserves only those, deleting the unchanged
  ones. Backward-compatible: v0.5-installed packages (no per-file
  hashes) fall back to the aggregate-SHA all-or-nothing semantics
  with a clear warning.
- **Importer round-trip tests**: new `internal/engine/roundtrip_test.go`
  covers cursor / claude / continue / copilot end-to-end (stage source
  tree → `engine.Init` → `engine.Compile` → assert original content
  survives the canonical model). Gemini / cline / windsurf / agents-md
  round-trips are TODO for v0.7.
- **`--no-hook-wrappers` CLI flag**: the v0.5 CHANGELOG promised it;
  v0.6 wires it through. Defaults to wrappers ON; pass `--no-hook-wrappers`
  to skip generating Claude `__scope-guard__` wrappers and fall back to
  raw source-script paths.

### Changed
- `importer.Registry.Register` returns an error on duplicate instead
  of panicking. `plugin.Registry.Register` still panics (deferred to
  v0.7 because of multiple call sites).
- `agents remove` returns the drift error rather than calling
  `os.Exit(1)` inside cobra `RunE`; cobra's exit pipeline handles the
  non-zero exit, so deferred cleanup runs.
- AGENTS.md importer `Detect` is now O(1): it stats `<root>/AGENTS.md`
  and `<root>/.github/AGENTS.md` only, never walks the tree. `--from
  auto` against monorepos no longer pays an O(tree) cost per importer.
  Nested-only `<some/dir>/AGENTS.md` projects need explicit
  `--from agents-md`.
- `parseGitSource` rejects non-`github.com` URLs with a clear error
  (`registry: only github.com URLs supported in v0.6`). v0.5 silently
  mis-parsed gitlab group/subgroup paths.
- `looksLikeRef` requires at least one a-f hex letter for 7-39 char
  refs, so a numeric branch like `1234567` is no longer mis-classified
  as a SHA. Full 40-char all-numeric strings still treat as SHA (rare,
  indistinguishable).

### Fixed
- **`--no-hook-wrappers` propagation through cobra** (caught by v0.6
  review): plugin registration was running before cobra parsed flags,
  so the field was always false at runtime. Moved to lazy
  registration via `cliState.ensureRegistry()` invoked from
  per-subcommand RunE. Regression test (`TestNoHookWrappersFlag_ThroughExecute`)
  drives the full Execute() path.
- **`agents remove` aggregate SHA staleness on partial drift**:
  when files are preserved (per-file Hash mismatch), the entry now
  zeroes `pkg.SHA` so a future Remove of the narrowed set falls back
  to per-file Hash checks rather than comparing against the original
  install's aggregate.
- **Symlink fallback no longer write-through-existing-symlink** on
  Windows. If `abs` is an existing symlink (e.g. project synced from
  a Unix prism install), the fallback now removes the link before
  the OpWrite re-entry, so `os.WriteFile` doesn't follow it and
  silently overwrite the canonical `.agents/` source.
- `@include` recursive stack append explicitly copies the slice before
  appending, avoiding backing-array aliasing if the slice's cap allowed
  in-place mutation. Latent corruption that only the right depth/order
  would have triggered.
- `Package.Files` is now `[]FileEntry{Path, Hash}` (model change);
  serializer and parser updated.
- `uniqueName` (cursor importer) now logs to stderr on the 1000-cap
  overflow instead of silently returning the base name.
- Removed dead `var _ = errors.New` from cline importer + unused
  `errors` import.

### Test
- Cross-compile to `GOOS=windows GOARCH=amd64` and `GOOS=linux GOARCH=arm64`
  verified locally; release workflow already builds all five targets
  on tag push.

## v0.5.0

### Added
- **Importers**: `agents init --from <tool>` now works for all 8 tools
  — `claude`, `cursor`, `gemini`, `cline`, `continue`, `windsurf`,
  `copilot`, `agents-md`. Comma-separated for multi-source merge
  (`--from cursor,gemini`); `--from auto` detects every tool with marker
  files. Heuristic decisions emit warnings naming the source file and
  the reasoning, so users can audit ambiguous mappings.
- **`@include` directive**: `<!-- include: path -->` in any `.agents/`
  markdown expands at parse time. Supports relative paths (resolved
  against the including file), `global:<path>` for the global layer,
  cycle detection, and a configurable depth cap (`include.max_depth`
  in `agents.config.yaml`, default 16). Lockfile sources and
  `agents which` reflect included files; `agents watch` recompiles on
  include-file change. Plugins downgrade `symlink → write` when a doc
  has includes (since the symlink target would only carry the
  unexpanded source).
- **Skill registry**: `agents add <git-url-or-path>`,
  `agents remove <name>`, `agents list`. Packages are tarballs of
  canonical `.agents/` content + a `package.yaml` manifest. Installs
  tracked in `.agents/packages.yaml`. Remove preserves manually-edited
  files (drift-detected via SHA-256 against install-time hashes).
  v0.5 ships install-from-Git-URL and local-path only; central
  registry, signatures, and updates wait for v0.6+.
- **Scoped capabilities**: skills, commands, agents, hooks, permissions,
  and MCP can live under a scope directory and inherit its path scope.
  `.agents/src/billing/skills/audit-trail/SKILL.md` is automatically
  scoped to `src/billing/`. Plugins that natively support per-path
  scoping (Cursor, Continue, Windsurf, Copilot, Claude for skills)
  project these correctly via their glob/applyTo/trigger frontmatter.
  For Claude hooks (no native path-scoping), prism generates a
  `__scope-guard__` wrapper script that invokes the hidden `prism scope-guard` subcommand, which reads Claude Code's hook JSON from stdin and dispatches to the source script only when `tool_input.file_path` falls under the scope. `CLAUDE_PROJECT_DIR` (set by Claude Code for hooks) converts absolute paths to project-relative for matching.
  Permissions and MCP are degraded to global with a warning.

### Changed
- `lockfile.Entry.Mode` (string holding the OpKind) — already renamed
  to `Kind` in v0.4 cleanup; no further change.
- Parser order: capability surfaces (`skills/`, `commands/`, etc.) at
  the `.agents/` root are parsed BEFORE nested scopes, so scoped
  capabilities can append to the same slices without being clobbered.
- Layered merge key for Skills/Commands/Agents/MCP now includes the
  ScopePath (`<scopePath>/<name>`) so a project-scope capability and
  a global capability with the same name can coexist.
- Global-layer scoped capabilities (`~/.agents/src/billing/...`) load
  broadly into the merged project; their native globs ensure plugins
  only fire them for matching files.

### Fixed
- `parser.Parse` no longer clobbers scoped slices with the global
  capability-surface assignments (the v0.4 ordering had `proj.Skills =
  skills` running after scope discovery; v0.5 reorders so the
  capability walk runs first and the scope walk appends).

### Removed
- Nothing.

## v0.4.0

### Added
- Initial public release.
- 8 plugins: claude, cursor, gemini, cline, continue, windsurf, copilot,
  agents-md.
- Layered config: project `.agents/` + global `~/.agents/`, with project
  winning on collisions.
- `OpMerge` preserves user keys in `settings.json` / `.mcp.json` across
  recompiles.
- Manual-edit detection via lockfile SHA-256 hash before clobber.
- `agents check` for CI drift detection (exit 1 on any diff).
- 171 tests across 9 packages.

### Commands
```
agents init [--from claude]
agents compile [--target X] [--dry-run] [--quiet]
agents check
agents diff
agents watch
agents which <projected-file>
agents capabilities [--target X]

Global flags: --root, --global, --no-global
```

### Notes
- Source-tag prefix (`project:` is implicit / no prefix; `global:` for
  global-layer content) used in lockfile `sources` and `agents which`.
- Capability matrix:
```
PLUGIN     CONTEXT  PATHS   SEMANTIC  SKILLS  CMDS    AGENTS  HOOKS   PERMS   MCP
agents-md  native   degr.   degr.     degr.   degr.   degr.   degr.   degr.   degr.
claude     native   native  degr.     native  native  native  native  native  native
cline      native   degr.   degr.     degr.   degr.   ----    ----    ----    ----
continue   native   native  native    degr.   degr.   ----    ----    ----    native
copilot    native   native  degr.     degr.   native  ----    ----    ----    ----
cursor     native   native  native    degr.   degr.   ----    ----    ----    native
gemini     native   native  degr.     ----    ----    ----    ----    ----    native
windsurf   native   native  native    degr.   degr.   ----    ----    ----    ----
```
