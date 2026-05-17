# 05-hooks-with-scope-guard

A Claude Code PreToolUse hook scoped to one directory, plus the
wrapper script that gates it on file-path.

## What this shows

Claude Code's hook primitive is global — a hook fires for every tool
call, not just edits to a particular directory. `prism` plugs that
gap by generating a small wrapper under
`.claude/hooks/__scope-guard__/` that exec's `prism scope-guard`,
which parses the hook's JSON payload from stdin and only invokes the
real hook script when `tool_input.file_path` falls under the scope.

## The `.agents/` structure

```
.agents/
  context.md
  agents.config.yaml                            # targets: [claude]
  src/api/
    context.md
    hooks/
      validate-openapi.yaml                     # event, matcher, script
      validate-openapi.sh                       # the actual logic
```

The `hooks/` directory under `src/api/` is recognized as a scoped
capability: hooks declared here inherit `ScopePath = "src/api"`.

## Run it

```
cd examples/05-hooks-with-scope-guard
prism compile
```

## What you get

```
CLAUDE.md
src/api/CLAUDE.md
.claude/settings.json                                       # hook wired in
.claude/hooks/__scope-guard__/src-api-validate-openapi.sh   # generated wrapper
```

`.claude/settings.json` declares the hook with the wrapper's absolute
path as the command. When Claude Code fires the hook, it execs the
wrapper, which then:

1. Resolves the project root from `${BASH_SOURCE[0]}` (with
   `PRISM_PROJECT_DIR` / `CLAUDE_PROJECT_DIR` taking precedence).
2. Execs `prism scope-guard --scope src/api --script <abs>`.
3. `prism scope-guard` reads the hook JSON from stdin and either
   invokes `validate-openapi.sh` (passing stdin through) or exits 0
   without running it.

The wrapper does NOT bake the project root into the script body; it
resolves at runtime so the wrapper survives `mv` of the project tree
(v0.7.1 fix).

## Things to try

1. Run the wrapper by hand from outside the project to see the
   scope-guard logic:
   ```
   echo '{"tool_input":{"file_path":"src/api/openapi.yaml"}}' \
     | ./.claude/hooks/__scope-guard__/src-api-validate-openapi.sh
   echo '{"tool_input":{"file_path":"README.md"}}' \
     | ./.claude/hooks/__scope-guard__/src-api-validate-openapi.sh
   ```
   The first invokes `validate-openapi.sh`; the second exits 0
   silently.
2. Compile with `--no-hook-wrappers`. The wrapper is suppressed and
   the hook is wired in as a global Claude hook — fires on every
   tool call, leaving scope enforcement to your script.
3. Add a `gemini` target. Gemini has no hooks primitive: you get an
   info warning about the dropped hook.
