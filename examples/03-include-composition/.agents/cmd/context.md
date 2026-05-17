The CLI lives here. Each subcommand is its own file under `cmd/<name>/`
with a `Run()` function; `cmd/root.go` wires them into cobra.

<!-- include: ../shared/style.md -->

## CLI specifics

- Subcommand flags are defined inline (no shared flag-set globals).
- Tests for a subcommand live next to it (`cmd/foo/foo_test.go`).
