package registry

// packages_file.go: read/write of `.agents/packages.yaml`, the bookkeeping
// ledger of installed registry packages. Schema is documented at the top of
// registry.go.

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

// packagesFileShape is the YAML envelope. It exists as a separate type so we
// can keep the on-disk representation independent of model.Package internals.
type packagesFileShape struct {
	Packages map[string]packageEntry `yaml:"packages"`
}

type packageEntry struct {
	Source      string   `yaml:"source"`
	Ref         string   `yaml:"ref,omitempty"`
	SHA         string   `yaml:"sha,omitempty"`
	InstalledAt string   `yaml:"installed_at,omitempty"`
	Target      string   `yaml:"target,omitempty"`
	Files       []string `yaml:"files,omitempty"`
}

// Load reads .agents/packages.yaml. Missing file → empty slice, no error.
// Returned slice is sorted by Name for deterministic downstream output.
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
		files := append([]string(nil), e.Files...)
		sort.Strings(files)
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
// are emitted in Name order, and files within each package are sorted.
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
			files := append([]string(nil), p.Files...)
			sort.Strings(files)
			seq := &yaml.Node{Kind: yaml.SequenceNode}
			for _, f := range files {
				seq.Content = append(seq.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: f})
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
