package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newScopeGuardCmd is the runtime side of scoped Claude hooks. Claude Code
// invokes hook scripts with the tool input as JSON on stdin (see
// https://code.claude.com/docs/en/hooks). This command parses that JSON,
// extracts the file path of the targeted tool input, and either invokes
// the user's hook script (passing the original stdin through) when the
// path falls under the scope, or exits 0 silently when it doesn't.
//
// The agents compile pipeline emits a small wrapper at
// .claude/hooks/__scope-guard__/<scope-slug>-<hook>.sh whose body is
// effectively `exec prism scope-guard --scope <path> --script <abs>`,
// so the runtime binary owns the JSON parsing rather than relying on jq.
//
// The command is marked Hidden because it's an implementation detail,
// not part of the daily-driver UX.
func newScopeGuardCmd() *cobra.Command {
	var (
		scope  string
		script string
	)
	cmd := &cobra.Command{
		Use:    "scope-guard",
		Short:  "Internal: gate a Claude Code hook on a file-path scope",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if scope == "" {
				return errors.New("scope-guard: --scope is required")
			}
			if script == "" {
				return errors.New("scope-guard: --script is required")
			}
			payload, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("scope-guard: read stdin: %w", err)
			}
			path := extractToolFilePath(payload)
			if !pathInScope(path, scope) {
				return nil
			}
			c := exec.Command(script)
			c.Stdin = strings.NewReader(string(payload))
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				// Propagate exit code so Claude Code surfaces the hook
				// rejection / failure to the user.
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					os.Exit(exitErr.ExitCode())
				}
				return fmt.Errorf("scope-guard: exec %s: %w", script, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope path under which the hook should fire (e.g. src/billing)")
	cmd.Flags().StringVar(&script, "script", "", "absolute path to the user's hook script")
	return cmd
}

// extractToolFilePath digs the targeted file path out of Claude Code's
// hook JSON payload. The shape varies slightly across tools; we try the
// known keys in order of likelihood and return the first match. Empty
// string means "no file path in the payload" — the caller should treat
// that as out-of-scope.
//
// Two envelope shapes are supported:
//
//   - Claude / Cursor / Gemini / Cline-style: file path lives under
//     tool_input.file_path (or aliases path/filepath/notebook_path).
//   - Windsurf Cascade Hooks: file path lives at the envelope root
//     (e.g. pre_write_code emits {"file_path": "..."} directly).
//
// We probe tool_input first (Claude-style is the most common), then fall
// back to the root-level keys.
func extractToolFilePath(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var env map[string]any
	if err := json.Unmarshal(payload, &env); err != nil {
		return ""
	}
	keys := []string{"file_path", "path", "filepath", "notebook_path"}
	// 1. tool_input.<key> (Claude-style envelope).
	if ti, _ := env["tool_input"].(map[string]any); ti != nil {
		for _, key := range keys {
			if v, ok := ti[key]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
	}
	// 2. Root-level <key> (Windsurf Cascade envelope).
	for _, key := range keys {
		if v, ok := env[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// pathInScope reports whether `path` falls under `scope`. Scope is the
// project-relative directory (e.g. "src/billing"). Path may be absolute
// (Claude Code typically sends absolute paths) — we convert both to
// forward-slash project-relative form before comparing.
//
// The match is conservative: empty path → out of scope. Path equal to
// scope → in scope. Path starting with scope + "/" → in scope. Anything
// else → out of scope.
func pathInScope(path, scope string) bool {
	if path == "" || scope == "" {
		return false
	}
	// Normalize separators to forward slash for cross-platform consistency.
	path = filepath.ToSlash(path)
	scope = filepath.ToSlash(scope)
	// If path is absolute, look for the scope as a substring after the
	// project root segment. Claude Code sets CLAUDE_PROJECT_DIR for hooks;
	// we prefer that, falling back to a substring match. The substring
	// match has a small false-positive risk (e.g. a file at
	// /unrelated/src/billing/foo would match scope "src/billing") but
	// gating on PROJECT_DIR removes it when available.
	if proj := os.Getenv("CLAUDE_PROJECT_DIR"); proj != "" {
		proj = filepath.ToSlash(proj)
		if rel, err := filepath.Rel(proj, path); err == nil {
			path = filepath.ToSlash(rel)
		}
	}
	// Strip leading ./
	path = strings.TrimPrefix(path, "./")
	scope = strings.TrimPrefix(scope, "./")
	if path == scope {
		return true
	}
	return strings.HasPrefix(path, scope+"/")
}
