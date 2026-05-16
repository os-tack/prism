package engine

import (
	"errors"

	"agents.dev/agents/internal/parser"
)

// ErrDrift is returned by Check when committed projections do not match
// what .agents/ would produce. Callers should exit non-zero.
var ErrDrift = errors.New("projection drift detected")

// ErrNoAgentsDir signals the project has no .agents/ directory. Aliased to
// parser.ErrNoAgentsDir so that errors.Is works in both directions.
var ErrNoAgentsDir = parser.ErrNoAgentsDir
