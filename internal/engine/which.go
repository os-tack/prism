package engine

import (
	"fmt"
	"path/filepath"
	"strings"

	"agents.dev/agents/internal/lockfile"
)

func which(opts Options, projectedPath string) ([]string, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("engine: Options.Root is required")
	}

	rel := projectedPath
	if filepath.IsAbs(rel) {
		r, err := filepath.Rel(opts.Root, rel)
		if err != nil {
			return nil, fmt.Errorf("engine: %s is not under %s: %w", projectedPath, opts.Root, err)
		}
		rel = r
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	rel = strings.TrimPrefix(rel, "./")

	lf, err := lockfile.Load(opts.Root)
	if err != nil {
		return nil, err
	}
	entry, ok := lf.Files[rel]
	if !ok {
		return nil, fmt.Errorf("engine: %s is not tracked in the lockfile", rel)
	}
	return entry.Sources, nil
}
