// Package scope provides small helpers for working with .agents/ scope paths.
package scope

import "strings"

// DefaultGlobs returns the default glob patterns for a scope at the given path.
// Empty path returns ["**"]; otherwise [path+"/**"].
func DefaultGlobs(path string) []string {
	if path == "" {
		return []string{"**"}
	}
	return []string{path + "/**"}
}

// Slug converts a scope path like "src/billing/foo" into a filesystem-safe
// identifier like "src-billing-foo".
func Slug(path string) string {
	if path == "" {
		return ""
	}
	return strings.ReplaceAll(path, "/", "-")
}

// SafePath reports whether path is a safe relative scope path: not empty,
// not absolute, and contains no ".." traversal segments after cleaning.
// Used by importers and serializers to reject malicious or malformed scope
// paths that would escape .agents/ on disk.
func SafePath(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "/") {
		return false
	}
	// Normalize separators and check for ..
	parts := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	for _, p := range parts {
		if p == ".." {
			return false
		}
	}
	return true
}
