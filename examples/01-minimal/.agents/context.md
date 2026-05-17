Project: a Go HTTP service that mirrors GitHub PR events into a Postgres
audit log. Single binary, single deploy, no Kafka.

Code lives under `cmd/` (entrypoints) and `internal/` (everything else).
The DB schema is in `migrations/`; run them via `go run ./cmd/migrate`.
Tests use the standard library plus `testify/require`; integration tests
expect a Postgres on `localhost:5432` (the `docker-compose.yaml` brings
one up).
