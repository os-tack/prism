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
