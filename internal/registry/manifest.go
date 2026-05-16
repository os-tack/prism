package registry

// manifest.go: parsing and validation of the per-package `package.yaml`
// manifest. A manifest is what publishers ship alongside the canonical
// `.agents/`-shaped content that makes up a package.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SupportedSchemas enumerates the package.yaml schema versions this prism
// build understands. Bumping the list is how we add v0.6+ extensions.
var SupportedSchemas = []int{1}

// Manifest is the parsed package.yaml.
type Manifest struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Author      string   `yaml:"author"`
	Description string   `yaml:"description"`
	Schema      int      `yaml:"schema"`
	Contents    []string `yaml:"contents"`
}

// LoadManifest reads <packageRoot>/package.yaml. Missing file returns
// (nil, nil); callers may synthesize a default manifest in that case.
func LoadManifest(packageRoot string) (*Manifest, error) {
	path := filepath.Join(packageRoot, "package.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("registry: read %s: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", path, err)
	}
	return &m, nil
}

// Validate checks that the manifest's schema is supported and that all
// declared content paths are safe (no absolute paths, no `..` traversal).
func (m *Manifest) Validate() error {
	if m == nil {
		return nil
	}
	if !schemaSupported(m.Schema) {
		return fmt.Errorf("%w: package schema %d, supported: %v", ErrSchemaMismatch, m.Schema, SupportedSchemas)
	}
	for _, c := range m.Contents {
		if err := validateContentPath(c); err != nil {
			return err
		}
	}
	return nil
}

func schemaSupported(schema int) bool {
	for _, s := range SupportedSchemas {
		if s == schema {
			return true
		}
	}
	return false
}

// validateContentPath rejects absolute paths and any `..` segment. Empty
// strings are also rejected.
func validateContentPath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty content path", ErrPathTraversal)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%w: absolute path %q", ErrPathTraversal, p)
	}
	// Normalize and walk segments.
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.HasSuffix(clean, "/..") {
		return fmt.Errorf("%w: path %q escapes package root", ErrPathTraversal, p)
	}
	return nil
}
