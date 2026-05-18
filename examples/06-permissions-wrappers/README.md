# 06-permissions-wrappers

Permissions in the canonical YAML, projected natively where the tool
supports it and via a `perms-guard` wrapper where it doesn't.

## What this shows

Two `permissions.yaml` files — one global, one scoped to
`src/billing/`. Each plugin handles them differently after v0.8.0:

- **Claude** has a native permissions primitive; the rules go into
  `.claude/settings.json` under the `permissions` key.
- **Continue** (since v0.8.0) has a native permissions primitive too;
  rules go into `.continue/permissions.yaml` translated to Continue's
  `Tool(pattern)` grammar.
- **Gemini** still has no native permissions primitive in 2026; the
  plugin emits a `perms-guard` wrapper script plus a sidecar JSON
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
Edit/Read/Write) the file path. This is the canonical syntax `prism`
understands; each plugin translates to its target's native shape.

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

### Continue (native, since v0.8.0)

```
.continue/permissions.yaml
```

Rules are translated from prism's `tool:pattern` form into Continue's
`Tool(pattern)` form (e.g. `bash:rm -rf *` → `Bash(rm -rf *)`,
`read` → `Read`). Continue has no per-scope permissions so scoped rules
are merged into the same flat file with an info warning. Patterns
with complex glob syntax that don't translate cleanly emit a
deprecation warning.

The pre-v0.8 perms-guard wrapper path is gone for Continue — this is
now native enforcement at the Continue CLI's own permissions layer.

### Gemini (perms-guard wrapper)

```
.gemini/hooks/__perms-guard__/policy.json                # global policy
.gemini/hooks/__perms-guard__/src-billing.policy.json    # scoped policy
.gemini/hooks/__perms-guard__/global-gate.sh             # global gate wrapper
.gemini/hooks/__perms-guard__/src-billing-gate.sh        # scoped gate wrapper
```

Each scope produces its own JSON policy file and its own gate wrapper.
When no hooks are declared (this example), the plugin emits a bare
gate per scope that the user can wire into their own pipeline — each
gate exec's `prism perms-guard`, which reads the proposed command from
stdin and exits 0 (allow), 2 (deny — Claude's "block" signal preserved
since v0.7.1), or after prompting (ask).

Note that Gemini also gained a native hooks primitive in 2026; v0.8.0+
of prism could wire the perms-guard into a `BeforeTool` hook
automatically. Today this example shows the standalone-wrapper path;
the auto-hook variant is queued as a v0.8.1 follow-up.

The wrappers resolve the project root at runtime via
`PRISM_PROJECT_DIR` / `CLAUDE_PROJECT_DIR` / `${BASH_SOURCE[0]}`, so
they survive a project `mv`.

## Things to try

1. Inspect `.continue/permissions.yaml` and `.claude/settings.json`
   side by side — same source rules, different target syntax.
2. Inspect `.gemini/hooks/__perms-guard__/policy.json` — it is just
   the canonical YAML, re-serialized to JSON with `allow` / `deny` /
   `ask` arrays.
3. Run the Gemini gate by hand. It expects Claude-style hook JSON on
   stdin (the same envelope Claude Code sends to its own hooks):
   ```
   echo '{"tool_name":"Bash","tool_input":{"command":"npm test ./..."}}' \
     | ./.gemini/hooks/__perms-guard__/global-gate.sh ; echo $?
   echo '{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}' \
     | ./.gemini/hooks/__perms-guard__/global-gate.sh ; echo $?
   ```
   The first allows (exit 0); the second denies (exit 2, with
   `perms-guard: denied by policy` on stderr).
4. Run `prism compile --target claude` and inspect
   `.claude/settings.json`. The global `deny` and the scoped `deny`
   are unioned into one list.
