package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExtractToolAndAction(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantTool   string
		wantAction string
	}{
		{
			name:       "bash command",
			payload:    `{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`,
			wantTool:   "Bash",
			wantAction: "rm -rf /",
		},
		{
			name:       "edit file_path",
			payload:    `{"tool_name":"Edit","tool_input":{"file_path":"/tmp/x.go","old_string":"a","new_string":"b"}}`,
			wantTool:   "Edit",
			wantAction: "/tmp/x.go",
		},
		{
			name:       "webfetch url",
			payload:    `{"tool_name":"WebFetch","tool_input":{"url":"https://example.com"}}`,
			wantTool:   "WebFetch",
			wantAction: "https://example.com",
		},
		{
			name:       "no action",
			payload:    `{"tool_name":"Bash","tool_input":{}}`,
			wantTool:   "Bash",
			wantAction: "",
		},
		{
			name:       "malformed json",
			payload:    `{not json`,
			wantTool:   "",
			wantAction: "",
		},
		{
			name:       "empty",
			payload:    ``,
			wantTool:   "",
			wantAction: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, action := extractToolAndAction([]byte(tc.payload))
			if tool != tc.wantTool || action != tc.wantAction {
				t.Errorf("extractToolAndAction = (%q, %q), want (%q, %q)", tool, action, tc.wantTool, tc.wantAction)
			}
		})
	}
}

// stubStdin replaces os.Stdin with a pipe whose read end contains
// payload, returning a restore func the caller MUST defer. We use a
// real os.File (rather than a bytes.Buffer) because perms-guard reads
// from os.Stdin directly via io.ReadAll.
func stubStdin(t *testing.T, payload string) func() {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	go func() {
		defer w.Close()
		_, _ = io.WriteString(w, payload)
	}()
	prev := os.Stdin
	os.Stdin = r
	return func() {
		os.Stdin = prev
		_ = r.Close()
	}
}

// runPermsGuard drives the perms-guard subcommand with the given flags
// and a payload on stdin, capturing the returned error (if any) without
// terminating the test process.
func runPermsGuard(t *testing.T, args []string, payload string) (string, error) {
	t.Helper()
	restore := stubStdin(t, payload)
	defer restore()

	root := &cobra.Command{
		Use:           "prism",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newPermsGuardCmd())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"perms-guard"}, args...))
	err := root.Execute()
	return out.String(), err
}

// TestPermsGuard_DenyReturnsError is the N1 regression test: a Deny
// decision must surface as a returned error rather than calling
// os.Exit, so cobra can clean up and tests can exercise the RunE in
// the parent process.
func TestPermsGuard_DenyReturnsError(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"deny":["Bash:rm *"]}`), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	_, err := runPermsGuard(t,
		[]string{"--policy", policyPath},
		`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`)
	if err == nil {
		t.Fatal("expected error from deny decision, got nil (did os.Exit fire?)")
	}
	if !strings.Contains(err.Error(), "denied by policy") {
		t.Errorf("error = %q, want substring %q", err.Error(), "denied by policy")
	}
}

// TestPermsGuard_AskNoTTYReturnsError covers the second os.Exit site:
// an ask-rule that fires without a TTY must return an error (we stub
// stdin as a pipe in the test, which is not a character device).
func TestPermsGuard_AskNoTTYReturnsError(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"ask":["Bash:git *"]}`), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	_, err := runPermsGuard(t,
		[]string{"--policy", policyPath},
		`{"tool_name":"Bash","tool_input":{"command":"git status"}}`)
	if err == nil {
		t.Fatal("expected error from ask-without-tty, got nil")
	}
	if !strings.Contains(err.Error(), "no TTY available") {
		t.Errorf("error = %q, want substring %q", err.Error(), "no TTY available")
	}
}

// TestPermsGuard_AllowNoScriptSucceeds verifies the happy path: an
// allowed (or default-allowed) decision with no --script returns nil
// from RunE so the wrapper exits 0.
func TestPermsGuard_AllowNoScriptSucceeds(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"allow":["Bash:ls *"]}`), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	_, err := runPermsGuard(t,
		[]string{"--policy", policyPath},
		`{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if err != nil {
		t.Fatalf("expected no error on allow, got %v", err)
	}
}

// TestPermsGuard_AllowExecsScript covers the second half of the happy
// path: an allowed decision with a --script forks the script and
// returns nil on a clean exit. We write a tiny shell script that
// touches a sentinel file so we can confirm it actually ran.
func TestPermsGuard_AllowExecsScript(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"allow":["Bash:ls *"]}`), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	sentinel := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "hook.sh")
	body := "#!/bin/sh\ntouch " + sentinel + "\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	_, err := runPermsGuard(t,
		[]string{"--policy", policyPath, "--script", script},
		`{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if err != nil {
		t.Fatalf("expected no error on allow+exec, got %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("hook script did not run (sentinel %s missing): %v", sentinel, err)
	}
}

// TestPermsGuard_ScriptFailureExitsWithChildCode is the v0.7.1 I-2
// regression: a script that exits non-zero must preserve its exit code
// (via permsGuardExit) so Claude Code's exit-2 "block" semantic isn't
// lost. We stub permsGuardExit to record the code rather than calling
// os.Exit in the test process.
func TestPermsGuard_ScriptFailureExitsWithChildCode(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"allow":["Bash:ls *"]}`), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	var recorded int = -1
	orig := permsGuardExit
	permsGuardExit = func(code int) { recorded = code }
	t.Cleanup(func() { permsGuardExit = orig })
	_, err := runPermsGuard(t,
		[]string{"--policy", policyPath, "--script", script},
		`{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if err != nil {
		t.Fatalf("unexpected RunE error: %v", err)
	}
	if recorded != 2 {
		t.Errorf("recorded exit code = %d, want 2 (Claude block semantic must round-trip)", recorded)
	}
}

func TestPermsGuard_UserDeclined_ErrorsIs_Nc(t *testing.T) {
	if !errors.Is(errPermsGuardUserDeclined, errPermsGuardUserDeclined) {
		t.Fatal("sentinel is not errors.Is-comparable to itself")
	}
	wrapped := fmt.Errorf("ask path: %w", errPermsGuardUserDeclined)
	if !errors.Is(wrapped, errPermsGuardUserDeclined) {
		t.Errorf("wrapped sentinel not detectable via errors.Is")
	}
	other := errors.New("perms-guard: user declined")
	if errors.Is(other, errPermsGuardUserDeclined) {
		t.Errorf("a freshly-allocated error with the same string should NOT match the sentinel (N-c rationale)")
	}
}
