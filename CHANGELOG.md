# Changelog

All notable changes to **prism** are documented here. Format roughly follows
[Keep a Changelog](https://keepachangelog.com/) and the project uses
[Semantic Versioning](https://semver.org/).

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
