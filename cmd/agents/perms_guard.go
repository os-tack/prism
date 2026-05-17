package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"agents.dev/agents/internal/perms"
)

// newPermsGuardCmd is the runtime side of the wrapper-script permissions
// enforcement for plugins that lack a native permissions primitive (gemini,
// continue per Phase 0). The compile pipeline emits a small wrapper script
// alongside a sidecar JSON policy file; the wrapper's body exec's
// `prism perms-guard --policy <sidecar> --script <real-hook>`.
//
// At hook-firing time, perms-guard:
//
//  1. Reads the hook JSON payload from stdin (same format as scope-guard;
//     {tool_name, tool_input} envelope).
//  2. Loads the policy file (missing file → default-allow).
//  3. Derives a (tool, action) pair from the payload — Bash uses
//     tool_input.command, file tools use tool_input.file_path / path /
//     notebook_path.
//  4. Calls perms.Check; Deny returns an error (cobra exits 1); Ask
//     without a TTY returns an error with a clear message; Ask with a
//     TTY prompts the user; Allow / Default fall through to exec'ing
//     the underlying hook script with the original payload on stdin.
//
// Exit-code semantics: deny / no-TTY ask / user-declined ask all
// surface as `return err` from RunE (cobra exits 1) — testable via
// cobra.Execute without subprocess shenanigans. The script-fork path
// is the exception: when the forked script exits non-zero, we preserve
// the child's exit code via `permsGuardExit(...)` (an indirection
// through a package-level var so tests can intercept). Mirrors
// scope_guard.go's behavior and preserves Claude Code's exit-2
// ("block with stderr") signal — see v0.7.1 review I-2.
//
// The subcommand is Hidden because it's an implementation detail.
func newPermsGuardCmd() *cobra.Command {
	var (
		policyPath string
		script     string
	)
	cmd := &cobra.Command{
		Use:    "perms-guard",
		Short:  "Internal: enforce a permissions policy around a hook script",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if policyPath == "" {
				return errors.New("perms-guard: --policy is required")
			}
			payload, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("perms-guard: read stdin: %w", err)
			}
			policy, err := perms.Load(policyPath)
			if err != nil {
				return err
			}
			tool, action := extractToolAndAction(payload)
			switch perms.Check(policy, tool, action) {
			case perms.DecisionDeny:
				return fmt.Errorf("perms-guard: denied by policy: tool=%s action=%q", tool, action)
			case perms.DecisionAsk:
				if !isTTY(os.Stdin) {
					return fmt.Errorf("perms-guard: ask-rule matched but no TTY available; denying: tool=%s action=%q", tool, action)
				}
				if !promptYesNo(fmt.Sprintf("permit %s %q? [y/N] ", tool, action)) {
					return errors.New("perms-guard: user declined")
				}
			}
			// Allow + Default fall through. If no script was provided, the
			// wrapper is acting as a pure gate (some hook setups want
			// just the decision); exit 0 to signal "go ahead".
			if script == "" {
				return nil
			}
			c := exec.Command(script)
			c.Stdin = strings.NewReader(string(payload))
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				// Mirror scope_guard.go's exit-code preservation for the
				// script-fork path only: Claude Code's hook protocol treats
				// exit 2 as "block with stderr" (distinct from any other
				// non-zero). Collapsing to cobra's default-1 would lose that
				// signal. Deny / ask-decline paths still return err (no
				// child code to preserve). Documented divergence from N1's
				// "all failures collapse to 1" pattern (v0.7.1 review I-2).
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					permsGuardExit(exitErr.ExitCode())
					return nil
				}
				return fmt.Errorf("perms-guard: exec %s: %w", script, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&policyPath, "policy", "", "absolute path to a sidecar policy JSON")
	cmd.Flags().StringVar(&script, "script", "", "absolute path to the user's hook script (optional)")
	return cmd
}

// extractToolAndAction pulls the (tool_name, action) pair out of the hook
// JSON envelope. The tool_input map exposes the action under one of
// several keys depending on which tool fired; we probe them in fixed
// order and return the first non-empty string we find. Returns the
// tool name and an empty action when no known key is present, which
// upstream callers treat as a default-allow / no-context scenario.
//
// Keys probed (in order):
//
//   - "command" — Bash (Claude / Gemini / Continue): the full shell
//     command string about to run.
//   - "file_path" — Read / Write / Edit (Claude): absolute path of the
//     target file.
//   - "path" — Read (Claude variant), Glob (Claude / Gemini): the
//     search root or path argument.
//   - "filepath" — alternate spelling seen in older hook payloads;
//     kept for back-compat with third-party tools.
//   - "notebook_path" — NotebookEdit (Claude): absolute path of the
//     .ipynb file being edited.
//   - "url" — WebFetch (Claude): the URL being fetched.
//
// First hit wins. The list mirrors what extractToolFilePath in
// scope_guard.go consults, minus "command" / "url" (scope-guard cares
// only about file paths). Keep the two in lockstep when adding new
// tools.
func extractToolAndAction(payload []byte) (string, string) {
	if len(payload) == 0 {
		return "", ""
	}
	var env struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", ""
	}
	for _, key := range []string{"command", "file_path", "path", "filepath", "notebook_path", "url"} {
		if v, ok := env.ToolInput[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return env.ToolName, s
			}
		}
	}
	return env.ToolName, ""
}

// isTTY reports whether f looks interactive enough to prompt the user.
// We treat character devices as TTYs; pipes / files / sockets are not.
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// promptYesNo prints msg and returns true on a y/Y response. Anything
// else (including EOF) returns false.
func promptYesNo(msg string) bool {
	fmt.Fprint(os.Stderr, msg)
	buf := make([]byte, 1)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	return buf[0] == 'y' || buf[0] == 'Y'
}

// permsGuardExit is the indirection used when the forked script exits
// non-zero. Production: os.Exit(code). Tests override with a recorder
// to assert the code without killing the test process.
var permsGuardExit = func(code int) {
	os.Exit(code)
}
