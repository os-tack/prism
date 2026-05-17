# 04-mcp-servers

One canonical MCP block, four different on-disk shapes.

## What this shows

`.agents/mcp.yaml` is the single source of truth for MCP server config.
Each plugin emits it in the file and schema its tool actually expects.

## The `.agents/` structure

```
.agents/
  context.md
  agents.config.yaml
  mcp.yaml                # two servers: postgres (stdio) and linear (URL)
```

`mcp.yaml` schema:

```yaml
servers:
  <name>:
    command: <executable>     # stdio transport
    args: [...]
    env: { ... }
    url: <https-url>          # URL transport (instead of command/args/env)
```

## Run it

```
cd examples/04-mcp-servers
prism compile
```

## What you get

```
.mcp.json                            # Claude — merges into existing file if present
.cursor/mcp.json                     # Cursor — same JSON schema, different path
.continue/mcpServers/postgres.yaml   # Continue — one YAML file per server
.continue/mcpServers/linear.yaml
.gemini/settings.json                # Gemini — merged under "mcpServers" key
```

- **Claude** writes `.mcp.json` at the project root, the path Claude
  Code reads natively. The op is a `merge`, so user-managed keys in
  an existing `.mcp.json` are preserved.
- **Cursor** writes `.cursor/mcp.json` — same `mcpServers` schema as
  Claude, also a merge.
- **Continue** does not have a single MCP file; instead, each server
  is its own YAML under `.continue/mcpServers/`. Plugin emits one
  file per entry.
- **Gemini** has no dedicated MCP file; it nests `mcpServers` inside
  the general-purpose `.gemini/settings.json`. The plugin merges into
  any existing settings.

## Things to try

1. Pre-create `.cursor/mcp.json` with a `"theme": "dark"` key and
   recompile. The key survives the merge.
2. Drop `linear` from `mcp.yaml`. On the next `prism compile`, the
   entries are removed from `.mcp.json` / `.cursor/mcp.json` and the
   `.continue/mcpServers/linear.yaml` file is deleted (the lockfile
   tracks the previous projection).
3. Add a `cline` target. Cline has no MCP primitive; the plugin emits
   an info warning and skips it. Inspect with `prism diff`.

## Notes

This example just shows config shapes. None of these servers actually
have to be running to compile — `prism` doesn't talk to MCP servers.
