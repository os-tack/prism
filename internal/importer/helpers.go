// helpers.go: shared utilities used across multiple importers.
//
// All importers in this package produce *model.Project values whose
// Document.SourcePath points back at the original source file (the file
// the user actually edits — e.g. .cursor/rules/api.mdc, GEMINI.md,
// AGENTS.md). The engine fills in Project.Root and Project.AgentsDir at
// serialization time; importers MUST NOT set those fields.

package importer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/scope"

	"gopkg.in/yaml.v3"
)

// skippedDirs are the well-known vendored / hidden directories that no
// importer should descend into when walking a project root looking for
// nested context files (CLAUDE.md / GEMINI.md / AGENTS.md).
//
// We intentionally do NOT skip every dotfile dir wholesale because some
// tools live under dotfile dirs (.claude, .cursor, .gemini) — but those
// dirs are read by their importers directly, not via the markdown walk.
var skippedDirs = map[string]struct{}{
	".git":         {},
	".agents":      {},
	".hg":          {},
	".svn":         {},
	"node_modules": {},
	"vendor":       {},
}

// importNestedMarkdown walks root looking for files named exactly
// filename (e.g. "CLAUDE.md" or "GEMINI.md" or "AGENTS.md") and turns
// each one into either the root context document or a nested scope
// document.
//
// Returns:
//   - rootDoc:  the parsed root-level <filename>, or nil if absent.
//   - scopes:   one *model.Scope per nested <filename>, with the scope's
//     Path set to the relative directory of that file and Globs
//     defaulted via scope.DefaultGlobs.
//
// The returned Document.SourcePath points at the ORIGINAL source file so
// users can trace where the imported content came from.
func importNestedMarkdown(root, filename, toolName string) (*model.Document, []*model.Scope, error) {
	if root == "" {
		return nil, nil, errors.New("importer: root is required")
	}

	var rootDoc *model.Document
	var scopes []*model.Scope

	rootPath := filepath.Join(root, filename)
	if data, err := os.ReadFile(rootPath); err == nil {
		doc, derr := newDocumentFromBytes(rootPath, data, toolName, rootPath)
		if derr != nil {
			return nil, nil, derr
		}
		rootDoc = doc
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("importer: read %s: %w", rootPath, err)
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if path != root {
				if _, skip := skippedDirs[name]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() != filename {
			return nil
		}
		// Skip the root file — already handled above.
		if filepath.Dir(path) == root {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("importer: read %s: %w", path, err)
		}
		doc, err := newDocumentFromBytes(path, data, toolName, path)
		if err != nil {
			return err
		}
		scopes = append(scopes, &model.Scope{
			Path:     relSlash,
			Globs:    scope.DefaultGlobs(relSlash),
			Priority: model.PriorityNormal,
			Document: doc,
		})
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}

	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Path < scopes[j].Path })
	return rootDoc, scopes, nil
}

// newDocumentFromBytes builds a *model.Document with a provenance
// comment prepended to the body. The frontmatter (if any) is parsed off
// the leading bytes.
func newDocumentFromBytes(sourcePath string, data []byte, toolName, originalPath string) (*model.Document, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("importer: %s: %w", sourcePath, err)
	}
	body = provenanceComment(toolName, originalPath) + body
	return &model.Document{
		SourcePath:  sourcePath,
		Frontmatter: fm,
		Body:        body,
	}, nil
}

// splitFrontmatter parses optional `---`-delimited YAML frontmatter.
// Mirrors parser.splitFrontmatter so importers and the parser see the
// same shape.
func splitFrontmatter(data []byte) (map[string]any, string, error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, s, nil
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, s, nil
	}
	fmText := rest[:end]
	bodyStart := end + len("\n---")
	body := rest[bodyStart:]
	body = strings.TrimPrefix(body, "\n")

	var fm map[string]any
	if strings.TrimSpace(fmText) != "" {
		if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
			return nil, "", fmt.Errorf("yaml frontmatter: %w", err)
		}
	}
	return fm, body, nil
}

// provenanceComment returns the standard "imported from" comment to
// prepend to a document body so users tracing the canonical .agents/ can
// see which source file produced this content.
func provenanceComment(toolName, sourcePath string) string {
	return "<!-- imported from " + toolName + ": " + sourcePath + " -->\n\n"
}

// slugifyName normalizes a name into a filename-safe slug. Lowercases,
// replaces non-word characters with dashes, collapses runs of dashes,
// trims leading/trailing dashes. Used to derive skill / command / agent
// names from arbitrary input strings.
var slugRE = regexp.MustCompile(`[^a-z0-9_]+`)

func slugifyName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// stringSliceAny coerces a frontmatter value (which yaml decodes as
// []any) into []string, dropping non-string entries.
func stringSliceAny(v any) []string {
	switch typed := v.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		// A single glob written as a bare string — accept it.
		if typed == "" {
			return nil
		}
		return []string{typed}
	}
	return nil
}

// hasGeneratedHeader reports whether data begins with the marker our own
// agents-md plugin emits ("<!-- Generated by agents."). Used to skip
// round-trip imports from prism-managed repos.
var generatedHeaderRE = regexp.MustCompile(`^\s*<!--\s*Generated by agents\.`)

func hasGeneratedHeader(data []byte) bool {
	// Tolerate a UTF-8 BOM at the start.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	return generatedHeaderRE.Match(data)
}
