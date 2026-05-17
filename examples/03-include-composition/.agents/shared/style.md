## House style

- Go: `gofmt -s`; no naked `interface{}` — use `any`.
- Errors wrap with `fmt.Errorf("op: %w", err)`. The verb names the
  operation, not the type.
- Tests use `t.Helper()` in helpers and `t.Run` for subcases.
- No `init()` for side effects. Wire dependencies in `main`.
