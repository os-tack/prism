Public REST API. OpenAPI spec lives at `src/api/openapi.yaml`; the spec
is the source of truth — handlers and types are generated from it.

CI rejects any edit to `src/api/openapi.yaml` that fails
`oasdiff lint`. The PreToolUse hook below catches the same problem
locally so the model doesn't have to round-trip through CI.
