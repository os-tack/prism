// Package diff classifies planned Operations against the current state of
// the filesystem so callers can decide what to apply.
package diff

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"agents.dev/agents/internal/plugin"
)

// HashContent returns the lowercase hex SHA-256 of s.
func HashContent(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HashBytes returns the lowercase hex SHA-256 of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Classify reads the current state of each op's target file and splits the
// op list into changed and unchanged buckets. Errors that aren't "file
// missing" are propagated.
func Classify(root string, ops []plugin.Operation) (changed []plugin.Operation, unchanged []plugin.Operation, err error) {
	for _, op := range ops {
		c, e := isChanged(root, op)
		if e != nil {
			return nil, nil, e
		}
		if c {
			changed = append(changed, op)
		} else {
			unchanged = append(unchanged, op)
		}
	}
	return changed, unchanged, nil
}

// isChanged returns true if applying op would alter the filesystem.
func isChanged(root string, op plugin.Operation) (bool, error) {
	abs := filepath.Join(root, op.Path)
	switch op.Kind {
	case plugin.OpDelete:
		_, err := os.Lstat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("diff: stat %s: %w", abs, err)
		}
		return true, nil
	case plugin.OpSymlink:
		fi, err := os.Lstat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}
			return false, fmt.Errorf("diff: stat %s: %w", abs, err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return true, nil
		}
		target, err := os.Readlink(abs)
		if err != nil {
			return false, fmt.Errorf("diff: readlink %s: %w", abs, err)
		}
		return target != op.LinkTarget, nil
	case plugin.OpAppend:
		data, err := os.ReadFile(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return op.Content != "", nil
			}
			return false, fmt.Errorf("diff: read %s: %w", abs, err)
		}
		// Append is unchanged only if the file already ends with the content.
		if len(op.Content) == 0 {
			return false, nil
		}
		if len(data) < len(op.Content) {
			return true, nil
		}
		return string(data[len(data)-len(op.Content):]) != op.Content, nil
	case plugin.OpWrite, plugin.OpMerge:
		data, err := os.ReadFile(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}
			return false, fmt.Errorf("diff: read %s: %w", abs, err)
		}
		return string(data) != op.Content, nil
	default:
		return true, fmt.Errorf("diff: unknown op kind %q", op.Kind)
	}
}
