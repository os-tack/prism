package registry

// packages_file.go: read/write of `.agents/packages.yaml`, the bookkeeping
// ledger of installed registry packages. Schema is documented at the top of
// registry.go.
//
// v0.6 introduces per-file hashes. The on-disk `files:` sequence is a list
// of `{path, hash}` maps; the v0.5 list-of-strings form is still parsed for
// back-compat (each string becomes a FileEntry with empty Hash, which the
// remove path interprets as "use the aggregate SHA fallback").

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"agents.dev/agents/internal/model"

	"gopkg.in/yaml.v3"
)

// PackagesFileName is the on-disk filename, relative to .agents/.
const PackagesFileName = "packages.yaml"

// PackagesFilePath returns the absolute path to .agents/packages.yaml.
func PackagesFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".agents", PackagesFileName)
}

// packagesFileShape is the YAML envelope. Files is yaml.Node so we can
// decode either the modern []{path, hash} form or the legacy []string form
// without two passes over the whole document.
type packagesFileShape struct {
	Packages map[string]packageEntry `yaml:"packages"`
}

type packageEntry struct {
	Source      string    `yaml:"source"`
	Ref         string    `yaml:"ref,omitempty"`
	SHA         string    `yaml:"sha,omitempty"`
	InstalledAt string    `yaml:"installed_at,omitempty"`
	Target      string    `yaml:"target,omitempty"`
	Files       yaml.Node `yaml:"files,omitempty"`
}

// decodeFiles handles both v0.6 modern ({path, hash} maps) and v0.5 legacy
// (plain strings) shapes for the `files:` sequence. Returns FileEntries
// sorted by Path for determinism.
func decodeFiles(n *yaml.Node) ([]model.FileEntry, error) {
	if n == nil || n.Kind == 0 {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected sequence for files, got kind %d", n.Kind)
	}
	out := make([]model.FileEntry, 0, len(n.Content))
	for _, item := range n.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			// v0.5 legacy: plain string path, no hash.
			out = append(out, model.FileEntry{Path: item.Value})
		case yaml.MappingNode:
			// v0.6 modern: {path, hash}.
			var fe struct {
				Path string `yaml:"path"`
				Hash string `yaml:"hash"`
			}
			if err := item.Decode(&fe); err != nil {
				return nil, fmt.Errorf("decode file entry: %w", err)
			}
			out = append(out, model.FileEntry{Path: fe.Path, Hash: fe.Hash})
		default:
			return nil, fmt.Errorf("unexpected node kind %d in files sequence", item.Kind)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Load reads .agents/packages.yaml. Missing file → empty slice, no error.
// Returned slice is sorted by Name for deterministic downstream output.
// Both v0.6 (per-file-hash) and v0.5 (path-only) `files:` shapes are
// accepted; v0.5 entries yield FileEntries with empty Hash.
func Load(projectRoot string) ([]*model.Package, error) {
	path := PackagesFilePath(projectRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("registry: read %s: %w", path, err)
	}
	var raw packagesFileShape
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", path, err)
	}
	out := make([]*model.Package, 0, len(raw.Packages))
	for name, e := range raw.Packages {
		files, err := decodeFiles(&e.Files)
		if err != nil {
			return nil, fmt.Errorf("registry: parse %s: files for %q: %w", path, name, err)
		}
		out = append(out, &model.Package{
			Name:        name,
			Source:      e.Source,
			Ref:         e.Ref,
			SHA:         e.SHA,
			InstalledAt: e.InstalledAt,
			Target:      e.Target,
			Files:       files,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Save writes .agents/packages.yaml. The output is deterministic: packages
// are emitted in Name order, and files within each package are sorted by
// path. The modern v0.6 `{path, hash}` map shape is always emitted.
// Creates .agents/ if missing.
func Save(projectRoot string, packages []*model.Package) error {
	dir := filepath.Dir(PackagesFilePath(projectRoot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("registry: mkdir %s: %w", dir, err)
	}

	// Sort packages by name; we serialize via a yaml.Node mapping to guarantee
	// the key order in the output rather than relying on map iteration.
	sorted := append([]*model.Package(nil), packages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	root := &yaml.Node{Kind: yaml.MappingNode}
	pkgsNode := &yaml.Node{Kind: yaml.MappingNode}

	for _, p := range sorted {
		entry := &yaml.Node{Kind: yaml.MappingNode}
		addStr := func(k, v string) {
			if v == "" {
				return
			}
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Value: v},
			)
		}
		addStr("source", p.Source)
		addStr("ref", p.Ref)
		addStr("sha", p.SHA)
		addStr("installed_at", p.InstalledAt)
		addStr("target", p.Target)

		if len(p.Files) > 0 {
			files := append([]model.FileEntry(nil), p.Files...)
			sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
			seq := &yaml.Node{Kind: yaml.SequenceNode}
			for _, f := range files {
				fileMap := &yaml.Node{Kind: yaml.MappingNode}
				fileMap.Content = append(fileMap.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "path"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: f.Path},
				)
				if f.Hash != "" {
					fileMap.Content = append(fileMap.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "hash"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: f.Hash},
					)
				}
				seq.Content = append(seq.Content, fileMap)
			}
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "files"},
				seq,
			)
		}

		pkgsNode.Content = append(pkgsNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: p.Name},
			entry,
		)
	}

	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "packages"},
		pkgsNode,
	)

	data, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("registry: marshal packages.yaml: %w", err)
	}
	if err := os.WriteFile(PackagesFilePath(projectRoot), data, 0o644); err != nil {
		return fmt.Errorf("registry: write %s: %w", PackagesFilePath(projectRoot), err)
	}
	return nil
}
