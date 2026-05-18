// Command prism-schema generates Draft 2020-12 JSON Schemas from the
// canonical model in internal/model and writes one file per primitive
// (plus the top-level agents.config schema and the extensions schema)
// under schema/v2/.
//
// Usage:
//
//	go run ./cmd/prism-schema            # writes into ./schema/v2
//	go run ./cmd/prism-schema -out path  # custom output directory
//
// The generator is intentionally a thin wrapper around invopop/jsonschema:
// the canonical authority is the Go struct definitions in
// internal/model/model.go. Tags drive schema metadata; map[string]any
// fields render as `additionalProperties: true` so extensions blocks
// remain open-ended per SPEC §9.2.
//
// Wired into `go generate ./...` via schema/v2/generate.go.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/invopop/jsonschema"

	"agents.dev/agents/internal/model"
)

// target names one schema file to be emitted.
type target struct {
	// File is the basename written under schema/v2/.
	File string
	// Title is the human-readable schema title.
	Title string
	// Type is the Go type to reflect.
	Type any
}

// targets is the ordered list of schemas emitted. Order is alphabetical
// by file name for stable directory listings.
var targets = []target{
	{File: "agent.schema.json", Title: "Agent", Type: model.Agent{}},
	{File: "agents.config.schema.json", Title: "AgentsConfig", Type: model.Config{}},
	{File: "command.schema.json", Title: "Command", Type: model.Command{}},
	{File: "extensions.schema.json", Title: "Extensions", Type: extensionsHolder{}},
	{File: "hook.schema.json", Title: "Hook", Type: model.Hook{}},
	{File: "mcpserver.schema.json", Title: "MCPServer", Type: model.MCPServer{}},
	{File: "permissions.schema.json", Title: "Permissions", Type: model.Permissions{}},
	{File: "scope.schema.json", Title: "Scope", Type: model.Scope{}},
	{File: "skill.schema.json", Title: "Skill", Type: model.Skill{}},
}

// extensionsHolder is a tiny shell so the extensions block has its own
// reflectable type. The canonical model represents extensions as a free
// `map[string]any` field on every primitive (SPEC §3.1.1). This holder
// renders the same shape into its own schema file so plugins / editors
// can reference it.
type extensionsHolder struct {
	// Extensions is a free-form pass-through namespace, opaque to the
	// engine. Schema-level: additionalProperties: true.
	Extensions map[string]any `json:"extensions,omitempty"`
}

func main() {
	outDir := flag.String("out", "schema/v2", "output directory")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "prism-schema: mkdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	for _, t := range targets {
		if err := emit(*outDir, t); err != nil {
			fmt.Fprintf(os.Stderr, "prism-schema: %s: %v\n", t.File, err)
			os.Exit(1)
		}
	}
}

// emit reflects t.Type and writes a JSON Schema file to outDir/t.File.
// Output is deterministic: sorted keys, two-space indent, trailing
// newline as written by json.Encoder.
func emit(outDir string, t target) error {
	r := &jsonschema.Reflector{
		// Inline the root primitive's fields at the top of the schema
		// instead of producing a single $ref to a $defs entry. Editors
		// and CI can then validate a primitive against its schema file
		// without dereferencing.
		ExpandedStruct: true,
		// Stable base ID for documentation cross-refs (SPEC §9.1).
		BaseSchemaID: jsonschema.ID("https://prism.dev/schema/v2"),
	}

	s := r.Reflect(t.Type)
	if s == nil {
		return fmt.Errorf("reflector returned nil schema for %T", t.Type)
	}
	s.Title = t.Title

	// Marshal canonically: round-trip through a generic any tree, then
	// re-encode through a sorted intermediate so output ordering is
	// stable across Go versions and library upgrades. invopop already
	// emits map[string]any fields with additionalProperties:true, so we
	// rely on that and avoid touching the schema tree directly.
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return fmt.Errorf("unmarshal-for-sort: %w", err)
	}
	generic = sortKeys(generic)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	path := filepath.Join(outDir, t.File)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// sortKeys returns a deep copy of v with every map[string]any rewritten
// into a sortedMap that json.Marshal emits in stable order. Slices and
// scalars pass through unchanged.
func sortKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(sortedMap, 0, len(keys))
		for _, k := range keys {
			out = append(out, kv{Key: k, Value: sortKeys(x[k])})
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = sortKeys(x[i])
		}
		return out
	default:
		return v
	}
}

// kv is one key/value pair in a sortedMap.
type kv struct {
	Key   string
	Value any
}

// sortedMap is an ordered JSON object: it marshals like a map but
// preserves insertion order. Used by sortKeys to emit deterministic
// JSON without depending on Go's map iteration order.
type sortedMap []kv

// MarshalJSON renders as a JSON object with the entries in slice order.
func (m sortedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(e.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(e.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
