package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"agents.dev/agents/internal/apply"
	"agents.dev/agents/internal/diff"
	"agents.dev/agents/internal/lockfile"
	"agents.dev/agents/internal/parser"
	"agents.dev/agents/internal/plan"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/version"
)

func compile(opts Options) (*Report, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("engine: Options.Root is required")
	}
	if opts.Registry == nil {
		return nil, fmt.Errorf("engine: Options.Registry is required")
	}

	proj, err := parser.ParseLayered(opts.GlobalRoot, opts.Root)
	if err != nil {
		return nil, err
	}

	ops, warnings, err := plan.Run(proj, opts.Registry, opts.Targets)
	if err != nil {
		return nil, err
	}

	// Normalize every Operation.Path to forward slashes. This is the single
	// chokepoint that guarantees lockfile keys and `agents which` lookups
	// agree on Windows just as they do on macOS/Linux: plugins build paths
	// with filepath.Join (backslashes on Windows) but the engine projects,
	// looks up, and persists by the slash form. On macOS/Linux this is a
	// no-op since filepath.Join already emits forward slashes.
	for i := range ops {
		ops[i].Path = filepath.ToSlash(ops[i].Path)
	}

	// Resolve OpMerge.Merger closures into op.Content exactly once here, then
	// clear Merger so apply.Apply writes the pre-computed bytes verbatim.
	// Running the closure twice (compile + apply) under concurrent edits
	// races: lockfile hash captures pass-1 output, disk gets pass-2 output,
	// and the next Check sees false-positive manual-edit drift.
	for i := range ops {
		if ops[i].Kind != plugin.OpMerge || ops[i].Merger == nil {
			continue
		}
		abs := filepath.Join(opts.Root, ops[i].Path)
		existing, rerr := os.ReadFile(abs)
		if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return nil, fmt.Errorf("engine: read %s: %w", abs, rerr)
		}
		if rerr != nil {
			existing = nil
		}
		merged, mErr := ops[i].Merger(existing)
		if mErr != nil {
			return nil, fmt.Errorf("engine: merger %s: %w", abs, mErr)
		}
		ops[i].Content = merged
		ops[i].Merger = nil
	}

	plannedPaths := make(map[string]struct{}, len(ops))
	for _, op := range ops {
		plannedPaths[op.Path] = struct{}{}
	}

	prev, err := lockfile.Load(opts.Root)
	if err != nil {
		return nil, err
	}

	var deletes []plugin.Operation
	if prev != nil {
		stalePaths := make([]string, 0, len(prev.Files))
		for path := range prev.Files {
			// Normalize so a legacy macOS-built lockfile (forward slashes)
			// and a legacy Windows-built lockfile (backslashes) both compare
			// against the slash-form plannedPaths.
			slash := filepath.ToSlash(path)
			if _, kept := plannedPaths[slash]; kept {
				continue
			}
			stalePaths = append(stalePaths, path)
		}
		sort.Strings(stalePaths)
		for _, path := range stalePaths {
			abs := filepath.Join(opts.Root, path)
			if _, statErr := os.Lstat(abs); statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("engine: stat %s: %w", abs, statErr)
			}
			entry := prev.Files[path]
			deletes = append(deletes, plugin.Operation{
				Kind:    plugin.OpDelete,
				Path:    filepath.ToSlash(path),
				Plugin:  entry.Plugin,
				Sources: entry.Sources,
			})
		}
	}

	// Manual-edit detection: warn if a tracked file has been edited since
	// last write and the new plan would overwrite those edits. Attach the
	// warning to the op so the CLI prints it inline.
	if prev != nil {
		// Build a slash-keyed view of prev.Files so the lookup matches
		// ops (which are now slash-form) even if the on-disk lockfile is
		// a legacy Windows artifact with backslash keys.
		prevBySlash := make(map[string]lockfile.Entry, len(prev.Files))
		for k, v := range prev.Files {
			prevBySlash[filepath.ToSlash(k)] = v
		}
		for i := range ops {
			op := &ops[i]
			if op.Kind != plugin.OpWrite && op.Kind != plugin.OpAppend && op.Kind != plugin.OpMerge {
				continue
			}
			entry, tracked := prevBySlash[op.Path]
			if !tracked || entry.Hash == "" {
				continue
			}
			abs := filepath.Join(opts.Root, op.Path)
			data, rerr := os.ReadFile(abs)
			if rerr != nil {
				continue
			}
			currentHash := diff.HashBytes(data)
			if currentHash == entry.Hash {
				continue
			}
			if diff.HashContent(op.Content) == currentHash {
				continue
			}
			w := plugin.Warning{
				Source:   op.Path,
				Message:  fmt.Sprintf("manual edits detected; will be overwritten by %s", op.Plugin),
				Severity: "warn",
			}
			op.Warnings = append(op.Warnings, w)
			warnings = append(warnings, w)
		}
	}

	changedCount, unchangedCount, err := apply.Apply(opts.Root, ops, opts.DryRun)
	if err != nil {
		return nil, err
	}

	removedCount, _, err := apply.Apply(opts.Root, deletes, opts.DryRun)
	if err != nil {
		return nil, err
	}

	if !opts.DryRun {
		newLF := &lockfile.Lockfile{
			Version:     1,
			GeneratedBy: "agents@" + version.Version,
			At:          time.Now().UTC(),
			Files:       make(map[string]lockfile.Entry, len(ops)),
		}
		for _, op := range ops {
			entry := lockfile.Entry{
				Sources: op.Sources,
				Plugin:  op.Plugin,
				Kind:    string(op.Kind),
			}
			switch op.Kind {
			case plugin.OpWrite, plugin.OpAppend, plugin.OpMerge:
				entry.Hash = diff.HashContent(op.Content)
			}
			newLF.Files[op.Path] = entry
		}
		if err := newLF.Save(opts.Root); err != nil {
			return nil, err
		}
	}

	allOps := append([]plugin.Operation{}, ops...)
	allOps = append(allOps, deletes...)

	return &Report{
		Operations: allOps,
		Warnings:   warnings,
		Changed:    changedCount,
		Unchanged:  unchangedCount,
		Removed:    removedCount,
	}, nil
}
