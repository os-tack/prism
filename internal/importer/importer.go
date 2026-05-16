// Package importer reads a source AI tool's on-disk config and emits a
// canonical *model.Project that engine.Init can serialize into .agents/.
//
// Importers are pure: they read the source tree, return a *model.Project +
// warnings, and never write to disk themselves. The engine package owns the
// write step so that a multi-tool import can merge multiple Importer outputs
// before serialization.
package importer

import (
	"errors"

	"agents.dev/agents/internal/model"
)

// ErrSourceNotPresent indicates Detect would have returned false.
var ErrSourceNotPresent = errors.New("importer: source tool not present in root")

// Importer reads a source tool's marker files and emits canonical content.
type Importer interface {
	// Name is the stable identifier matching `agents init --from <name>`.
	Name() string

	// Detect returns true if root contains marker files this importer recognizes.
	// E.g., cursor.Detect checks for .cursor/ or .cursorrules.
	Detect(root string) bool

	// Import reads root and returns the canonical Project to write into .agents/.
	// Returns ErrSourceNotPresent if Detect would have returned false.
	// Warnings carry heuristic-decision notes ("imported X as a skill
	// because Y") so users can audit ambiguous mappings.
	Import(root string) (*model.Project, []Warning, error)
}

// Warning is a heuristic-decision note attached to an import operation.
type Warning struct {
	// SourcePath is the input file the heuristic acted on (e.g. .cursor/rules/api.mdc).
	SourcePath string
	// Heuristic is a human-readable explanation of the decision.
	Heuristic string
	// Severity is "info" or "warn".
	Severity string
}

// Registry holds the importers available to the engine.
type Registry struct {
	importers map[string]Importer
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{importers: map[string]Importer{}}
}

// Register adds an importer. Panics on duplicate name.
func (r *Registry) Register(i Importer) {
	if _, dup := r.importers[i.Name()]; dup {
		panic("importer already registered: " + i.Name())
	}
	r.importers[i.Name()] = i
}

// Get returns the importer with the given name, or nil if absent.
func (r *Registry) Get(name string) Importer {
	return r.importers[name]
}

// Names returns the names of every registered importer, in no particular order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.importers))
	for n := range r.importers {
		out = append(out, n)
	}
	return out
}

// All returns every registered importer.
func (r *Registry) All() []Importer {
	out := make([]Importer, 0, len(r.importers))
	for _, i := range r.importers {
		out = append(out, i)
	}
	return out
}
