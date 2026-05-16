package model

import (
	"path/filepath"
	"strings"
)

// SourceTag returns a stable, human-readable label for an absolute source
// path. The label is the path relative to whichever .agents/ root contains
// it, optionally prefixed by the layer name:
//
//   - Files under p.AgentsDir get no prefix (e.g. "context.md", "src/billing/context.md").
//   - Files under p.GlobalAgentsDir get a "global:" prefix (e.g. "global:agents/reviewer.md").
//   - Paths under neither root are returned as-is (absolute) — a fallback that
//     should not happen in practice; logged via Sources for traceability.
//
// Output always uses forward slashes for portability across lockfile reads.
func (p *Project) SourceTag(absPath string) string {
	if absPath == "" {
		return ""
	}
	if p.AgentsDir != "" {
		if rel, ok := relUnder(p.AgentsDir, absPath); ok {
			return rel
		}
	}
	if p.GlobalAgentsDir != "" {
		if rel, ok := relUnder(p.GlobalAgentsDir, absPath); ok {
			return "global:" + rel
		}
	}
	return absPath
}

// relUnder returns the cleaned forward-slash relative path of abs under
// base, and whether abs is actually under base (no ".." traversal needed).
func relUnder(base, abs string) (string, bool) {
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", false
	}
	return rel, true
}
