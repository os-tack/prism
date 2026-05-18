---
name: project-root
description: |
  Top-level project context for the canonical-coverage fixture.
activation: always
priority: 0
tags:
  - root
  - fixture
extensions:
  agents-md:
    section_marker: "<!-- prism: canonical-coverage root -->"
---

# acme-billing (canonical-coverage fixture)

This project exercises every canonical primitive's every field. It is
intended for prism's contract test and as a worked reference for what
a fully-populated v2 source tree looks like.

## Stack

Go 1.24, PostgreSQL 16, pub/sub via Redis Streams.

## Build commands

- `go build ./...` — full build
- `go test ./...` — full test suite

## House style

- Errors are wrapped with `%w` so `errors.Is` works.
- No `panic` outside `cmd/` or test setup.
- All exported types have godoc.
