// Package engine exposes the top-level operations the CLI invokes.
// All commands route through these entry points; the package wires
// parser + plugin registry + planner + applier + lockfile together.
package engine

import (
	"agents.dev/agents/internal/plugin"
)

// Options controls the behavior of a top-level engine operation.
type Options struct {
	// Root is the project root (parent of .agents/). Required.
	Root string
	// GlobalRoot is the optional global layer (typically ~/.agents/'s parent).
	// If set and its .agents/ subdir exists, its content is merged underneath
	// the project's .agents/. Project content always wins on collisions.
	// Empty means no global layering.
	GlobalRoot string
	// Registry is the set of registered plugins.
	Registry *plugin.Registry
	// Targets filters which plugins to run. Empty = autodetect or honor
	// proj.Config.Targets.
	Targets []string
	// DryRun produces a plan + report without touching the filesystem.
	DryRun bool
	// Quiet suppresses non-error output.
	Quiet bool
}

// Report summarizes the result of an operation.
type Report struct {
	Operations []plugin.Operation
	Warnings   []plugin.Warning
	Changed    int
	Unchanged  int
	Removed    int
}

func Compile(opts Options) (*Report, error) { return compile(opts) }

func Check(opts Options) (*Report, error) {
	opts.DryRun = true
	rep, err := compile(opts)
	if err != nil {
		return rep, err
	}
	if rep.Changed > 0 || rep.Removed > 0 {
		return rep, ErrDrift
	}
	return rep, nil
}

func Init(opts Options, importFrom string) error { return initProject(opts, importFrom) }

func Diff(opts Options) (*Report, error) {
	opts.DryRun = true
	return compile(opts)
}

func Which(opts Options, projectedPath string) ([]string, error) {
	return which(opts, projectedPath)
}

func Watch(opts Options) error { return watch(opts) }
