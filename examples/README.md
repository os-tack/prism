# Examples

Each subdirectory is a self-contained `.agents/` tree plus a README
that walks through what gets generated and why. `cd` into any one and
run `prism compile` — no extra setup required.

| Example                          | What it shows                                                 |
|----------------------------------|---------------------------------------------------------------|
| [01-minimal](01-minimal)         | The smallest useful `.agents/`: one context.md, three targets |
| [02-scoped-skills](02-scoped-skills) | Path-scoped context plus a skill bound to that scope      |
| [03-include-composition](03-include-composition) | `<!-- include: -->` to share markdown across contexts |
| [04-mcp-servers](04-mcp-servers) | One MCP config, four per-plugin output shapes                 |
| [05-hooks-with-scope-guard](05-hooks-with-scope-guard) | Scoped Claude hook + the generated scope-guard wrapper |
| [06-permissions-wrappers](06-permissions-wrappers) | Native perms in Claude + Continue (since v0.8); perms-guard wrapper for Gemini |
| [07-canonical-coverage](07-canonical-coverage) | Golden round-trip fixture for v0.9 schema v2: every canonical field of every primitive |

## Running

Each example is self-contained — `cd examples/01-minimal && prism compile`
works. There is no shared state and no setup script.

If you don't have `prism` installed yet, build from the repo root:

```
go build -o /tmp/prism ./cmd/agents
/tmp/prism compile --root examples/01-minimal --dry-run
```

## Recommended reading order

01 → 02 → 03 covers the core projection model (context, scopes,
composition). 04 → 05 → 06 covers the three areas where plugins
diverge most: MCP, hooks, permissions.

See the main [README.md](../README.md) for installation, the full
capability matrix, and the architectural overview.
