#!/usr/bin/env bash
# Lint src/api/openapi.yaml before any Edit/Write under src/api/ lands.
# Reads Claude Code's hook JSON from stdin; the scope-guard wrapper has
# already filtered to events under src/api/.
set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
SPEC="${PROJECT_DIR}/src/api/openapi.yaml"

# If the spec file doesn't exist yet, nothing to lint.
[ -f "${SPEC}" ] || exit 0

if ! command -v oasdiff >/dev/null 2>&1; then
  # Tool missing on this machine; warn but don't block.
  echo "validate-openapi: oasdiff not installed, skipping" >&2
  exit 0
fi

if ! oasdiff lint "${SPEC}" >&2; then
  echo "validate-openapi: spec failed lint; refusing the edit" >&2
  exit 2
fi
