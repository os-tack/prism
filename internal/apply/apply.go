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
	"runtime"

	"agents.dev/agents/internal/diff"
	"agents.dev/agents/internal/plugin"
)

// shouldDowngradeSymlink reports whether OpSymlink should be downgraded to
// OpWrite on the current platform. Windows symlinks require either admin
// rights or Developer Mode, so silently degrade to a content copy.
//
// Indirected through a package-level var so tests on any OS can exercise
// the downgrade path; production callers see runtime.GOOS.
var shouldDowngradeSymlink = func() bool {
	return runtime.GOOS == "windows"
}

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
		// Windows fallback: read the symlink target's contents and treat
		// the op as OpWrite. os.Symlink usually fails on Windows without
		// admin/dev mode; a content copy keeps projection working at the
		// cost of decoupling from upstream edits (documented in CHANGELOG).
		if shouldDowngradeSymlink() {
			targetDir := filepath.Dir(abs)
			targetAbs := op.LinkTarget
			if !filepath.IsAbs(targetAbs) {
				targetAbs = filepath.Join(targetDir, op.LinkTarget)
			}
			data, rerr := os.ReadFile(targetAbs)
			if rerr != nil {
				return false, fmt.Errorf("apply: symlink fallback read %s: %w", targetAbs, rerr)
			}
			// If abs is itself an existing symlink (e.g. project was synced
			// from a Unix prism install before the user opened it on Windows),
			// remove it first. os.WriteFile follows symlinks and would
			// silently overwrite the canonical .agents/ source through the
			// link. Skip-on-dryRun so the dry-run preview stays side-effect-
			// free.
			if fi, statErr := os.Lstat(abs); statErr == nil && fi.Mode()&os.ModeSymlink != 0 {
				if !dryRun {
					if rmErr := os.Remove(abs); rmErr != nil {
						return false, fmt.Errorf("apply: symlink fallback remove stale link %s: %w", abs, rmErr)
					}
				}
			}
			downgrade := op
			downgrade.Kind = plugin.OpWrite
			downgrade.Content = string(data)
			downgrade.LinkTarget = ""
			return applyOne(root, downgrade, dryRun)
		}
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
