// Package lockfile reads and writes .agents/.lock, which records what was
// projected, by which plugin, from which canonical sources.
package lockfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// Entry is the bookkeeping for one projected file.
type Entry struct {
	Sources []string `yaml:"sources"`
	Plugin  string   `yaml:"plugin"`
	Kind    string   `yaml:"kind"`
	Hash    string   `yaml:"hash,omitempty"`
}

// Lockfile is the persisted .agents/.lock structure.
type Lockfile struct {
	Version     int              `yaml:"version"`
	GeneratedBy string           `yaml:"generated_by"`
	At          time.Time        `yaml:"at"`
	Files       map[string]Entry `yaml:"files"`
}

// Path returns the absolute path to the lockfile under root.
func Path(root string) string {
	return filepath.Join(root, ".agents", ".lock")
}

// Load reads root/.agents/.lock. If the file is missing, an empty Lockfile
// (Version: 1) is returned, no error.
func Load(root string) (*Lockfile, error) {
	data, err := os.ReadFile(Path(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Lockfile{Version: 1, Files: map[string]Entry{}}, nil
		}
		return nil, fmt.Errorf("lockfile: read: %w", err)
	}
	var lf Lockfile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("lockfile: parse: %w", err)
	}
	if lf.Files == nil {
		lf.Files = map[string]Entry{}
	}
	if lf.Version == 0 {
		lf.Version = 1
	}
	return &lf, nil
}

// Save writes the lockfile, creating .agents/ if needed (it should already
// exist by the time we save). Files are written in sorted order for stable
// diffs.
func (l *Lockfile) Save(root string) error {
	if l.Version == 0 {
		l.Version = 1
	}
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	if l.Files == nil {
		l.Files = map[string]Entry{}
	}

	// Re-marshal via a sorted intermediate to guarantee stable output.
	keys := make([]string, 0, len(l.Files))
	for k := range l.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf yaml.Node
	buf.Kind = yaml.MappingNode

	addScalar := func(k, v string) {
		buf.Content = append(buf.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Value: v},
		)
	}

	addScalar("version", fmt.Sprintf("%d", l.Version))
	addScalar("generated_by", l.GeneratedBy)
	addScalar("at", l.At.UTC().Format(time.RFC3339))

	// files: mapping in sorted order
	filesNode := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		entry := l.Files[k]
		entryNode := &yaml.Node{Kind: yaml.MappingNode}

		// sources: list
		srcSeq := &yaml.Node{Kind: yaml.SequenceNode}
		srcs := append([]string{}, entry.Sources...)
		sort.Strings(srcs)
		for _, s := range srcs {
			srcSeq.Content = append(srcSeq.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
		entryNode.Content = append(entryNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "sources"}, srcSeq,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "plugin"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: entry.Plugin},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "kind"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: entry.Kind},
		)
		if entry.Hash != "" {
			entryNode.Content = append(entryNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "hash"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: entry.Hash},
			)
		}

		filesNode.Content = append(filesNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k},
			entryNode,
		)
	}
	buf.Content = append(buf.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "files"},
		filesNode,
	)

	data, err := yaml.Marshal(&buf)
	if err != nil {
		return fmt.Errorf("lockfile: marshal: %w", err)
	}

	dir := filepath.Dir(Path(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("lockfile: mkdir: %w", err)
	}
	if err := os.WriteFile(Path(root), data, 0o644); err != nil {
		return fmt.Errorf("lockfile: write: %w", err)
	}
	return nil
}
