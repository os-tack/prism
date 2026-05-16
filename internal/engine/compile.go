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
)

const version = "0.4.0"

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
			if _, kept := plannedPaths[path]; kept {
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
				Path:    path,
				Plugin:  entry.Plugin,
				Sources: entry.Sources,
			})
		}
	}

	// Manual-edit detection: warn if a tracked file has been edited since
	// last write and the new plan would overwrite those edits. Attach the
	// warning to the op so the CLI prints it inline.
	if prev != nil {
		for i := range ops {
			op := &ops[i]
			if op.Kind != plugin.OpWrite && op.Kind != plugin.OpAppend && op.Kind != plugin.OpMerge {
				continue
			}
			entry, tracked := prev.Files[op.Path]
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
			GeneratedBy: "agents@" + version,
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
