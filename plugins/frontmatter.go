package plugins

import (
	"encoding/json"
	"strings"
)

// frontmatter.go consolidates the YAML-flow-scalar and YAML-flow-array
// helpers that plugins use to render frontmatter without pulling in a full
// YAML emitter. The historical state had one renderer per plugin (yamlScalar
// in cline.go, yamlQuote in copilot.go, plus 7 inline json.Marshal sites
// for globs/descriptions). Centralizing them here removes the duplication
// and unifies the (slightly different) escaping rules.
//
// JSON-marshal-as-YAML rationale: a JSON-quoted string is also a valid YAML
// flow scalar, and json.Marshal handles escape sequences (quotes, control
// chars, non-ASCII) deterministically. The same holds for arrays: a JSON
// `["a","b"]` is a valid YAML flow-array literal. Using json.Marshal as the
// emitter sidesteps the need for a hand-rolled YAML escaper.

// renderYAMLScalar returns s formatted as a YAML flow-style scalar
// (i.e. JSON-quoted). Empty input returns `""`. Used everywhere a plugin
// emits a frontmatter value that may contain a colon, quote, or other
// YAML-interpreted character.
func renderYAMLScalar(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(raw)
}

// renderGlobs returns a YAML flow-array of globs (e.g. `["src/**","docs/**"]`).
// Empty input returns `[]`. Output is suitable as the right-hand side of a
// `globs: <value>` frontmatter line.
func renderGlobs(globs []string) string {
	if len(globs) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(globs)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// renderFrontmatterBlock assembles a `---\n<key>: <value>\n…\n---\n<body>`
// document. fm key order is the iteration order of the input map, which
// Go randomizes — callers that need determinism should pre-sort keys and
// build the body manually, or use the typed renderers in the individual
// plugins. This helper is a convenience for the common case (3-4 keys
// where order isn't load-bearing for the host parser).
//
// Both keys and values are emitted as-is (no escaping); pass values through
// renderYAMLScalar / renderGlobs first when they may contain interpreted
// characters.
func renderFrontmatterBlock(orderedKVs [][2]string, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, kv := range orderedKVs {
		b.WriteString(kv[0])
		b.WriteString(": ")
		b.WriteString(kv[1])
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
