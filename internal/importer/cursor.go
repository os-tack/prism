// cursor.go: imports .cursor/rules/*.mdc + .cursor/mcp.json into the
// canonical *model.Project shape.
//
// Mapping (per /tmp/prism-v0.5-design.md lines 168-208):
//
//   alwaysApply: true                  → append to .agents/context.md
//   globs: ["src/billing/**"]          → scope at .agents/src/billing/context.md
//                                        with scopes.yaml (via Scope.Description
//                                        and Scope.Globs)
//   globs: ["**/*.pdf"], description   → .agents/skills/<name>/SKILL.md
//   no globs, description present      → .agents/skills/<name>/SKILL.md
//                                        (model-decision-style trigger)
//
// Heuristic for skill-vs-scope when globs are present:
//   - every glob is extension-only (starts with "**/*.")   → skill
//   - every glob has a path prefix (no leading "*", no "**" at start)
//                                                          → scope
//   - mixed                                                → scope + warning

package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/scope"
)

// CursorImporter reads .cursor/rules/*.mdc and .cursor/mcp.json.
type CursorImporter struct{}

// NewCursor constructs a CursorImporter. Four importers share the
// internal/importer package, so each constructor is suffixed with the
// importer name to avoid a top-level New() collision.
func NewCursor() *CursorImporter { return &CursorImporter{} }

// Name returns "cursor".
func (i *CursorImporter) Name() string { return "cursor" }

// Detect returns true when .cursor/ or .cursorrules is present at root.
func (i *CursorImporter) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".cursor")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, ".cursorrules")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Import reads root and produces the canonical Project.
func (i *CursorImporter) Import(root string) (*model.Project, []Warning, error) {
	if !i.Detect(root) {
		return nil, nil, ErrSourceNotPresent
	}

	proj := &model.Project{}
	var warnings []Warning

	// Legacy .cursorrules → entire body becomes root context.
	legacyPath := filepath.Join(root, ".cursorrules")
	if data, err := os.ReadFile(legacyPath); err == nil {
		proj.Context = &model.Document{
			SourcePath: legacyPath,
			Body:       provenanceComment("cursor", legacyPath) + string(data),
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("cursor: read %s: %w", legacyPath, err)
	}

	// Scoped accumulator: scope.Path → *model.Scope. Used so multiple .mdc
	// files targeting the same directory append into a single context.md.
	scopeByPath := map[string]*model.Scope{}

	// Walk .cursor/rules/*.mdc.
	rulesDir := filepath.Join(root, ".cursor", "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("cursor: read %s: %w", rulesDir, err)
	}
	// Sort for determinism (filepath.WalkDir returns sorted, ReadDir too,
	// but make it explicit).
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".mdc") {
			continue
		}
		mdcPath := filepath.Join(rulesDir, e.Name())
		data, err := os.ReadFile(mdcPath)
		if err != nil {
			return nil, nil, fmt.Errorf("cursor: read %s: %w", mdcPath, err)
		}
		fm, body, err := splitFrontmatter(data)
		if err != nil {
			return nil, nil, fmt.Errorf("cursor: %s: %w", mdcPath, err)
		}

		description, _ := fm["description"].(string)
		globs := stringSliceAny(fm["globs"])
		alwaysApply, _ := fm["alwaysApply"].(bool)

		bodyWithProv := provenanceComment("cursor", mdcPath) + body

		switch {
		case alwaysApply:
			// Append to root context.
			if proj.Context == nil {
				proj.Context = &model.Document{
					SourcePath: mdcPath,
					Body:       bodyWithProv,
				}
			} else {
				proj.Context.Body = strings.TrimRight(proj.Context.Body, "\n") + "\n\n" + bodyWithProv
			}

		case len(globs) == 0:
			// No globs, no alwaysApply → skill (model-decision trigger).
			i.addSkill(proj, e.Name(), description, body, mdcPath, nil)

		default:
			classification := classifyGlobs(globs)
			switch classification {
			case globKindExtension:
				i.addSkill(proj, e.Name(), description, body, mdcPath, globs)
			case globKindPath:
				i.addScope(scopeByPath, globs, description, bodyWithProv, mdcPath)
			case globKindMixed:
				warnings = append(warnings, Warning{
					SourcePath: relTo(root, mdcPath),
					Heuristic: "globs mix path-prefix and extension-only patterns; " +
						"imported as scope (path prefix wins) — split into separate .mdc files for clean import",
					Severity: "warn",
				})
				i.addScope(scopeByPath, globs, description, bodyWithProv, mdcPath)
			}
		}
	}

	// Flush scopeByPath into proj.Scopes in stable order.
	paths := make([]string, 0, len(scopeByPath))
	for p := range scopeByPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		proj.Scopes = append(proj.Scopes, scopeByPath[p])
	}

	// .cursor/mcp.json.
	mcpPath := filepath.Join(root, ".cursor", "mcp.json")
	mcps, err := readCursorMCP(mcpPath)
	if err != nil {
		return nil, nil, err
	}
	proj.MCP = mcps

	return proj, warnings, nil
}

// addSkill appends a Skill derived from one .mdc file.
//
// Skill.Name is derived from the .mdc basename (without the extension);
// the skill body is prefixed with the provenance comment via the
// caller-passed body bytes wrapped into a synthetic Document.
func (i *CursorImporter) addSkill(proj *model.Project, mdcBase, description, body, source string, globs []string) {
	name := slugifyName(strings.TrimSuffix(mdcBase, ".mdc"))
	if name == "" {
		name = "rule"
	}
	// Detect duplicate names — if the same slug already exists, suffix it.
	name = uniqueName("cursor", name, func(candidate string) bool {
		for _, sk := range proj.Skills {
			if sk.Name == candidate {
				return true
			}
		}
		return false
	})

	doc := &model.Document{
		SourcePath: source,
		// SKILL.md carries its own frontmatter when serialized; the
		// parser populates Skill.Description/Globs from the Document
		// frontmatter, so we keep the Document body as just the
		// (provenance-prefixed) markdown text.
		Body: provenanceComment("cursor", source) + body,
	}
	proj.Skills = append(proj.Skills, &model.Skill{
		Name:        name,
		Description: description,
		Globs:       globs,
		Document:    doc,
	})
}

// addScope merges the .mdc into a scope keyed by the longest common
// directory prefix of its globs. If the result has zero useful prefix
// (e.g. globs all start with `**`), the scope is placed at the root —
// but that case is already filtered upstream (the classifier returns
// globKindExtension in that situation).
func (i *CursorImporter) addScope(scopeByPath map[string]*model.Scope, globs []string, description, body, source string) {
	scopePath := inferScopePathFromGlobs(globs)
	sc, ok := scopeByPath[scopePath]
	if !ok {
		sc = &model.Scope{
			Path:        scopePath,
			Globs:       append([]string(nil), globs...),
			Description: description,
			Priority:    model.PriorityNormal,
			Document: &model.Document{
				SourcePath: source,
				Body:       body,
			},
		}
		// Fall back to the default scope globs when the inferred path
		// produces something distinct from the user's globs — but per
		// the design, we prefer the user's globs verbatim. Only use
		// DefaultGlobs if the user provided none (shouldn't happen in
		// this branch, but defensive).
		if len(sc.Globs) == 0 {
			sc.Globs = scope.DefaultGlobs(scopePath)
		}
		scopeByPath[scopePath] = sc
		return
	}
	// Existing scope: append body, union globs, take first non-empty
	// description.
	sc.Document.Body = strings.TrimRight(sc.Document.Body, "\n") + "\n\n" + body
	sc.Globs = unionStrings(sc.Globs, globs)
	if sc.Description == "" {
		sc.Description = description
	}
}

// readCursorMCP parses .cursor/mcp.json into []*model.MCPServer.
// Returns (nil, nil) when the file is absent.
func readCursorMCP(path string) ([]*model.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cursor: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cursor: parse %s: %w", path, err)
	}
	names := make([]string, 0, len(raw.MCPServers))
	for n := range raw.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*model.MCPServer, 0, len(names))
	for _, n := range names {
		s := raw.MCPServers[n]
		out = append(out, &model.MCPServer{
			Name:    n,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
		})
	}
	return out, nil
}

// globKind classifies the shape of a frontmatter globs list.
type globKind int

const (
	globKindExtension globKind = iota // every glob is extension-only ("**/*.ext")
	globKindPath                      // every glob has a directory prefix ("src/**")
	globKindMixed                     // a mix of the two
)

func classifyGlobs(globs []string) globKind {
	if len(globs) == 0 {
		return globKindExtension
	}
	allExt := true
	allPath := true
	for _, g := range globs {
		if isExtensionGlob(g) {
			allPath = false
		} else if isPathGlob(g) {
			allExt = false
		} else {
			// Pattern looks like neither (e.g. just "**" or "*") —
			// treat as extension so it falls into skill-land rather
			// than creating a confusing root-level scope.
			allPath = false
		}
	}
	switch {
	case allExt:
		return globKindExtension
	case allPath:
		return globKindPath
	default:
		return globKindMixed
	}
}

// isExtensionGlob reports whether g looks like "**/*.ext" — i.e. it
// matches files anywhere in the tree by extension only, without
// constraining the directory.
func isExtensionGlob(g string) bool {
	g = strings.TrimSpace(g)
	return strings.HasPrefix(g, "**/*.") || strings.HasPrefix(g, "*.")
}

// isPathGlob reports whether g looks like it constrains a directory
// prefix (e.g. "src/**", "tests/unit/**", "docs/foo.md").
func isPathGlob(g string) bool {
	g = strings.TrimSpace(g)
	if g == "" {
		return false
	}
	// Reject anything that leads with a glob meta-char.
	switch g[0] {
	case '*', '?', '[', '{':
		return false
	}
	return true
}

// inferScopePathFromGlobs returns the longest common directory prefix of
// the (path-prefixed) globs. Used to decide where to root the imported
// scope.
//
// Caller has already established that every glob is a path glob (via
// classifyGlobs == globKindPath), so we can split on '/' and walk
// segments.
func inferScopePathFromGlobs(globs []string) string {
	if len(globs) == 0 {
		return ""
	}
	// Split each glob into segments, dropping the trailing pattern
	// segments (any segment containing a meta-char terminates the prefix).
	prefixes := make([][]string, 0, len(globs))
	for _, g := range globs {
		segs := strings.Split(g, "/")
		var pre []string
		for _, s := range segs {
			if strings.ContainsAny(s, "*?[]{}") {
				break
			}
			pre = append(pre, s)
		}
		prefixes = append(prefixes, pre)
	}
	if len(prefixes) == 0 {
		return ""
	}
	// Compute the longest common prefix across all globs.
	common := prefixes[0]
	for _, p := range prefixes[1:] {
		common = commonPrefix(common, p)
		if len(common) == 0 {
			break
		}
	}
	result := strings.Join(common, "/")
	if !scope.SafePath(result) {
		return ""
	}
	return result
}

func commonPrefix(a, b []string) []string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			break
		}
		out = append(out, a[i])
	}
	return out
}

// unionStrings returns the deduplicated union of two string slices,
// preserving order (a first, then any new entries from b).
func unionStrings(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// relTo returns path relative to root for use in user-facing warnings.
// Falls back to the input path if relativization fails.
func relTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// Compile-time check that *CursorImporter implements Importer.
var _ Importer = (*CursorImporter)(nil)
