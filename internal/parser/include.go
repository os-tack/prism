package parser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Sentinel errors returned by expandIncludes. Callers may wrap these for
// additional context; use errors.Is to check the underlying cause.
var (
	// ErrIncludeCycle is returned when expansion would re-enter a file that
	// is already on the current expansion stack.
	ErrIncludeCycle = errors.New("include: cycle detected")
	// ErrIncludeMaxDepth is returned when nested expansion exceeds the
	// configured max depth.
	ErrIncludeMaxDepth = errors.New("include: max depth exceeded")
	// ErrIncludeEscape is returned when an include path would resolve
	// outside both the project agentsDir and the globalAgentsDir, or when
	// an absolute host-filesystem path is supplied.
	ErrIncludeEscape = errors.New("include: path escapes .agents/")
	// ErrIncludeMissing is returned when the referenced include file does
	// not exist on disk.
	ErrIncludeMissing = errors.New("include: file not found")
)

// includeLineRE matches a line whose entire content is an HTML-comment
// include directive (with optional surrounding whitespace).
//
//	<!-- include: <path> -->
//
// The directive must be on its own line; trailing text on the same line
// disqualifies the match (the comment is then treated as literal text).
var includeLineRE = regexp.MustCompile(`^[ \t]*<!--[ \t]*include:[ \t]*([^\s][^>]*?)[ \t]*-->[ \t]*$`)

// expandIncludes scans body for `<!-- include: <path> -->` directives, each
// occupying an entire line, and substitutes them with the (recursively
// expanded) body of the referenced file. Frontmatter in included files is
// stripped — only the body is inserted.
//
// Path resolution:
//   - "global:<path>" resolves under globalAgentsDir (error if global is empty).
//   - Absolute paths starting with "/" are rejected with ErrIncludeEscape.
//   - Other paths are resolved against the directory of the including file.
//
// Every resolved path must live under agentsDir or globalAgentsDir; any
// escape (via .. traversal or otherwise) returns ErrIncludeEscape.
//
// Cycles return ErrIncludeCycle; nesting beyond maxDepth returns
// ErrIncludeMaxDepth.
//
// Returns the expanded body and a deduped slice of absolute paths of every
// file pulled in at any depth. The order of the returned slice matches the
// order in which each unique file was first encountered.
func expandIncludes(body, sourcePath, agentsDir, globalAgentsDir string, maxDepth int) (string, []string, error) {
	if maxDepth <= 0 {
		maxDepth = 16
	}
	state := &includeState{
		agentsDir:       agentsDir,
		globalAgentsDir: globalAgentsDir,
		maxDepth:        maxDepth,
		seen:            map[string]struct{}{},
	}
	expanded, err := state.expand(body, sourcePath, []string{sourcePath}, 0)
	if err != nil {
		return "", nil, err
	}
	return expanded, state.includes, nil
}

// includeState carries traversal-wide configuration and accumulators
// across recursive expansion calls.
type includeState struct {
	agentsDir       string
	globalAgentsDir string
	maxDepth        int
	// includes is the ordered, deduped list of absolute paths included.
	includes []string
	// seen tracks which absolute paths have already been added to includes,
	// keyed by cleaned absolute path.
	seen map[string]struct{}
}

// expand walks body line by line, replacing include directives in place.
// stack is the current expansion chain (used for cycle detection); depth
// is the current nesting level (the top-level body is depth 0).
func (s *includeState) expand(body, sourcePath string, stack []string, depth int) (string, error) {
	if depth > s.maxDepth {
		return "", fmt.Errorf("%w: depth %d > %d at %s", ErrIncludeMaxDepth, depth, s.maxDepth, sourcePath)
	}

	// Preserve trailing-newline behavior: split on "\n" then re-join with "\n".
	lines := strings.Split(body, "\n")
	var out strings.Builder
	out.Grow(len(body))

	for i, line := range lines {
		match := includeLineRE.FindStringSubmatch(line)
		if match == nil {
			out.WriteString(line)
			if i < len(lines)-1 {
				out.WriteByte('\n')
			}
			continue
		}

		rawPath := strings.TrimSpace(match[1])
		resolved, err := s.resolve(rawPath, sourcePath)
		if err != nil {
			return "", err
		}

		// Cycle detection against the current expansion stack.
		for _, ancestor := range stack {
			if ancestor == resolved {
				return "", fmt.Errorf("%w: %s -> %s", ErrIncludeCycle, strings.Join(stack, " -> "), resolved)
			}
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("%w: %s (from %s)", ErrIncludeMissing, rawPath, sourcePath)
			}
			return "", fmt.Errorf("include: read %s: %w", rawPath, err)
		}

		// Strip frontmatter; only the body is substituted.
		_, includedBody, err := splitFrontmatter(data)
		if err != nil {
			return "", fmt.Errorf("include: %s: %w", resolved, err)
		}

		// Record this include before recursing so deeper levels see it in
		// the seen set (de-dupe handles the case where the same file
		// appears multiple times anywhere in the tree).
		if _, dup := s.seen[resolved]; !dup {
			s.seen[resolved] = struct{}{}
			s.includes = append(s.includes, resolved)
		}

		// Recurse: push onto stack, expand, then pop.
		nestedStack := append(stack, resolved)
		nestedExpanded, err := s.expand(includedBody, resolved, nestedStack, depth+1)
		if err != nil {
			return "", err
		}

		// Trim a single trailing newline on the included body so the
		// substitution does not introduce an extra blank line between
		// the included content and the line that followed the directive.
		nestedExpanded = strings.TrimSuffix(nestedExpanded, "\n")
		out.WriteString(nestedExpanded)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}

	return out.String(), nil
}

// resolve translates a directive's raw path argument to an absolute path
// on disk, enforcing the layering and escape rules described on
// expandIncludes.
func (s *includeState) resolve(rawPath, sourcePath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("%w: empty include path in %s", ErrIncludeMissing, sourcePath)
	}

	// "global:<path>" → resolve under globalAgentsDir.
	if strings.HasPrefix(rawPath, "global:") {
		rel := strings.TrimPrefix(rawPath, "global:")
		rel = strings.TrimSpace(rel)
		if s.globalAgentsDir == "" {
			return "", fmt.Errorf("%w: 'global:' include used but no global layer configured (%s in %s)", ErrIncludeEscape, rawPath, sourcePath)
		}
		if filepath.IsAbs(rel) {
			return "", fmt.Errorf("%w: 'global:' path must be relative (%s in %s)", ErrIncludeEscape, rawPath, sourcePath)
		}
		abs := filepath.Clean(filepath.Join(s.globalAgentsDir, rel))
		if !underRoot(abs, s.globalAgentsDir) {
			return "", fmt.Errorf("%w: %s resolves outside global .agents/ (%s)", ErrIncludeEscape, rawPath, abs)
		}
		return abs, nil
	}

	// Absolute host paths are forbidden.
	if filepath.IsAbs(rawPath) || strings.HasPrefix(rawPath, "/") {
		return "", fmt.Errorf("%w: absolute path not permitted (%s in %s)", ErrIncludeEscape, rawPath, sourcePath)
	}

	// Relative path → resolve against the including file's directory.
	base := filepath.Dir(sourcePath)
	abs := filepath.Clean(filepath.Join(base, rawPath))

	// The resolved path must sit under either layer's .agents/ root.
	if !underRoot(abs, s.agentsDir) && !underRoot(abs, s.globalAgentsDir) {
		return "", fmt.Errorf("%w: %s resolves outside .agents/ (%s in %s)", ErrIncludeEscape, rawPath, abs, sourcePath)
	}
	return abs, nil
}

// underRoot reports whether abs (a cleaned absolute path) lies inside
// root. An empty root never contains anything. Paths equal to root count
// as inside.
func underRoot(abs, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}
