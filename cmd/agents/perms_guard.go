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
//  4. Calls perms.Check; Deny exits 1; Ask without a TTY exits 1 with a
//     clear message; Ask with a TTY prompts the user; Allow / Default
//     fall through to exec'ing the underlying hook script with the
//     original payload on stdin.
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
				fmt.Fprintf(os.Stderr, "perms-guard: denied by policy: tool=%s action=%q\n", tool, action)
				os.Exit(1)
			case perms.DecisionAsk:
				if !isTTY(os.Stdin) {
					fmt.Fprintf(os.Stderr, "perms-guard: ask-rule matched but no TTY available; denying: tool=%s action=%q\n", tool, action)
					os.Exit(1)
				}
				if !promptYesNo(fmt.Sprintf("permit %s %q? [y/N] ", tool, action)) {
					fmt.Fprintln(os.Stderr, "perms-guard: user declined")
					os.Exit(1)
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
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					os.Exit(exitErr.ExitCode())
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
// JSON envelope. The action is whichever of command / file_path / path /
// notebook_path the tool_input map exposes — first hit wins.
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
