# 06-permissions-wrappers

Permissions in the canonical YAML, projected natively where the tool
supports it and via a `perms-guard` wrapper where it doesn't.

## What this shows

Two `permissions.yaml` files — one global, one scoped to
`src/billing/`. Each plugin handles them differently:

- **Claude** has a native permissions primitive; the rules go into
  `.claude/settings.json`.
- **Gemini** and **Continue** have no permissions primitive; the
  plugins emit a `perms-guard` wrapper script plus a sidecar JSON
  policy. The wrapper consults the policy at hook-firing time (or
  whenever the user wires it into their pipeline).

## The `.agents/` structure

```
.agents/
  context.md
  agents.config.yaml                          # targets: [claude, gemini, continue]
  permissions.yaml                            # global allow/deny/ask
  src/billing/
    context.md
    permissions.yaml                          # scoped overrides
```

`permissions.yaml` schema:

```yaml
allow: [...]      # auto-approve
deny:  [...]      # auto-block
ask:   [...]      # prompt user before running
```

Each entry is a `tool:pattern` string with an optional trailing `*`
glob — e.g. `bash:npm test *`, `Edit:.env*`. The tool match is
case-insensitive; the action is the Bash command string or (for
Edit/Read/Write) the file path. This is the format the
`prism perms-guard` runtime matches against.

These strings are passed through to `.claude/settings.json` verbatim.
Claude Code is generally lenient about unrecognized formats; if you
target Claude only, you can use its native `Tool(pattern)` syntax
instead (e.g. `Bash(npm test:*)`).

## Run it

```
cd examples/06-permissions-wrappers
prism compile
```

## What you get

### Claude (native)

```
.claude/settings.json
```

Permissions are folded into `settings.json` under the `permissions`
key. Scoped permissions degrade: Claude's primitive is global, so
rules from `src/billing/permissions.yaml` are merged into the global
allow/deny/ask lists. Plugin emits an info warning about the lost
path scope.

### Gemini and Continue (wrappers)

```
.gemini/hooks/__perms-guard__/policy.json                # global policy
.gemini/hooks/__perms-guard__/src-billing.policy.json    # scoped policy
.gemini/hooks/__perms-guard__/global-gate.sh             # global gate wrapper
.gemini/hooks/__perms-guard__/src-billing-gate.sh        # scoped gate wrapper
.continue/hooks/__perms-guard__/policy.json
.continue/hooks/__perms-guard__/src-billing.policy.json
.continue/hooks/__perms-guard__/global-gate.sh
.continue/hooks/__perms-guard__/src-billing-gate.sh
```

Each scope produces its own JSON policy file and its own gate wrapper.
When no hooks are declared (this example), the plugin emits a bare
gate per scope that the user can wire into their own pipeline — each
gate exec's
`prism perms-guard`, which reads the proposed command from stdin and
exits 0 (allow), 1 (deny), or after prompting (ask).

The wrappers resolve the project root at runtime via
`PRISM_PROJECT_DIR` / `CLAUDE_PROJECT_DIR` / `${BASH_SOURCE[0]}`, so
they survive a project `mv`.

## Things to try

1. Inspect `.gemini/hooks/__perms-guard__/policy.json` — it is just
   the canonical YAML, re-serialized to JSON with `allow` / `deny` /
   `ask` arrays.
2. Run the gate by hand. It expects Claude-style hook JSON on stdin
   (the same envelope Claude Code sends to its own hooks):
   ```
   echo '{"tool_name":"Bash","tool_input":{"command":"npm test ./..."}}' \
     | ./.gemini/hooks/__perms-guard__/global-gate.sh ; echo $?
   echo '{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}' \
     | ./.gemini/hooks/__perms-guard__/global-gate.sh ; echo $?
   ```
   The first allows (exit 0); the second denies (exit 2, with `perms-guard: denied by policy` on stderr).
3. Run `prism compile --target claude` and inspect
   `.claude/settings.json`. The global `deny` and the scoped `deny`
   are unioned into one list.
