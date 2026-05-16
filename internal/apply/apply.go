// Package apply executes plugin Operations against the filesystem.
// Callers pass the project root, the planned ops, and a dry-run flag;
// apply returns counts of changed vs. unchanged files.
package apply

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"agents.dev/agents/internal/diff"
	"agents.dev/agents/internal/plugin"
)

// Apply executes ops at root. If dryRun is true, it tallies changes
// without touching the filesystem.
func Apply(root string, ops []plugin.Operation, dryRun bool) (changed int, unchanged int, err error) {
	for _, op := range ops {
		didChange, e := applyOne(root, op, dryRun)
		if e != nil {
			return changed, unchanged, e
		}
		if didChange {
			changed++
		} else {
			unchanged++
		}
	}
	return changed, unchanged, nil
}

func applyOne(root string, op plugin.Operation, dryRun bool) (bool, error) {
	abs := filepath.Join(root, op.Path)

	switch op.Kind {
	case plugin.OpDelete:
		_, err := os.Lstat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("apply: stat %s: %w", abs, err)
		}
		if dryRun {
			return true, nil
		}
		if err := os.Remove(abs); err != nil {
			return false, fmt.Errorf("apply: delete %s: %w", abs, err)
		}
		return true, nil

	case plugin.OpSymlink:
		fi, err := os.Lstat(abs)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				cur, rerr := os.Readlink(abs)
				if rerr == nil && cur == op.LinkTarget {
					return false, nil
				}
			}
			if dryRun {
				return true, nil
			}
			if rmErr := os.Remove(abs); rmErr != nil {
				return false, fmt.Errorf("apply: replace %s: %w", abs, rmErr)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("apply: stat %s: %w", abs, err)
		}

		if dryRun {
			return true, nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return false, fmt.Errorf("apply: mkdir %s: %w", filepath.Dir(abs), err)
		}
		if err := os.Symlink(op.LinkTarget, abs); err != nil {
			return false, fmt.Errorf("apply: symlink %s -> %s: %w", abs, op.LinkTarget, err)
		}
		return true, nil

	case plugin.OpAppend:
		existing, rerr := os.ReadFile(abs)
		alreadyEnds := false
		if rerr == nil {
			if len(op.Content) > 0 && len(existing) >= len(op.Content) &&
				string(existing[len(existing)-len(op.Content):]) == op.Content {
				alreadyEnds = true
			}
		} else if !errors.Is(rerr, os.ErrNotExist) {
			return false, fmt.Errorf("apply: read %s: %w", abs, rerr)
		}
		if alreadyEnds {
			return false, nil
		}
		if op.Content == "" {
			return false, nil
		}
		if dryRun {
			return true, nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return false, fmt.Errorf("apply: mkdir %s: %w", filepath.Dir(abs), err)
		}
		mode := op.FileMode
		if mode == 0 {
			mode = 0o644
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, mode)
		if err != nil {
			return false, fmt.Errorf("apply: open %s: %w", abs, err)
		}
		defer f.Close()
		if _, err := f.WriteString(op.Content); err != nil {
			return false, fmt.Errorf("apply: append %s: %w", abs, err)
		}
		return true, nil

	case plugin.OpWrite, plugin.OpMerge:
		// Compare existing content using the diff hash to detect unchanged.
		data, err := os.ReadFile(abs)
		if err == nil && diff.HashBytes(data) == diff.HashContent(op.Content) {
			return false, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("apply: read %s: %w", abs, err)
		}
		if dryRun {
			return true, nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return false, fmt.Errorf("apply: mkdir %s: %w", filepath.Dir(abs), err)
		}
		mode := fileMode(op)
		if err := os.WriteFile(abs, []byte(op.Content), mode); err != nil {
			return false, fmt.Errorf("apply: write %s: %w", abs, err)
		}
		return true, nil

	default:
		return false, fmt.Errorf("apply: unsupported op kind %q", op.Kind)
	}
}

func fileMode(op plugin.Operation) fs.FileMode {
	if op.FileMode == 0 {
		return 0o644
	}
	return op.FileMode
}
