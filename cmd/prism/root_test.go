package main

import (
	"bytes"
	"testing"

	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/plugins"
	"github.com/spf13/cobra"
)

// TestNoHookWrappersFlag_Plumbing verifies the registerPlugins helper
// correctly sets DisableHookWrappers on the ClaudePlugin it registers.
// This locks the field-level half of the CLI flag plumbing.
func TestNoHookWrappersFlag_Plumbing(t *testing.T) {
	for _, c := range []struct {
		name   string
		noHook bool
	}{
		{"default_off_wrappers_on", false},
		{"flag_on_wrappers_off", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := plugin.NewRegistry()
			if err := registerPlugins(reg, c.noHook, false); err != nil {
				t.Fatalf("registerPlugins: %v", err)
			}

			got := reg.Get("claude")
			if got == nil {
				t.Fatal("claude plugin not registered")
			}
			claude, ok := got.(*plugins.ClaudePlugin)
			if !ok {
				t.Fatalf("registered plugin is %T, want *plugins.ClaudePlugin", got)
			}
			if claude.DisableHookWrappers != c.noHook {
				t.Errorf("DisableHookWrappers = %v, want %v", claude.DisableHookWrappers, c.noHook)
			}
		})
	}
}

// TestNoHookWrappersFlag_CobraDecl verifies the persistent flag exists
// on the root command with the documented default of false.
func TestNoHookWrappersFlag_CobraDecl(t *testing.T) {
	root := newRootCmd()
	f := root.PersistentFlags().Lookup("no-hook-wrappers")
	if f == nil {
		t.Fatal("--no-hook-wrappers persistent flag not declared on root")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-hook-wrappers default = %q, want %q (wrappers ON by default)", f.DefValue, "false")
	}
}

// TestNoHookWrappersFlag_ThroughExecute is the v0.6 review's blocker
// regression test. Earlier wiring registered plugins INSIDE newRootCmd()
// before cobra parsed flags, so DisableHookWrappers was always false at
// runtime regardless of --no-hook-wrappers. The fix made registration
// lazy via cliState.ensureRegistry(), called from the subcommand RunE
// after cobra's parse pass. This test drives the full cobra Execute path
// and inspects the registered plugin afterward.
func TestNoHookWrappersFlag_ThroughExecute(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want bool
	}{
		{"flag_omitted_wrappers_on", []string{"capabilities"}, false},
		{"flag_set_wrappers_off", []string{"--no-hook-wrappers", "capabilities"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			state := &cliState{}
			root := buildTestRoot(state)
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stdout)
			root.SetArgs(c.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute(%v) returned err: %v\noutput:\n%s", c.args, err, stdout.String())
			}
			if state.registry == nil {
				t.Fatal("registry was nil after Execute() — ensureRegistry never fired")
			}
			p := state.registry.Get("claude")
			if p == nil {
				t.Fatal("claude plugin not registered")
			}
			claude, ok := p.(*plugins.ClaudePlugin)
			if !ok {
				t.Fatalf("registered plugin is %T, want *plugins.ClaudePlugin", p)
			}
			if claude.DisableHookWrappers != c.want {
				t.Errorf("DisableHookWrappers = %v after Execute(%v); want %v",
					claude.DisableHookWrappers, c.args, c.want)
			}
		})
	}
}

// buildTestRoot mirrors newRootCmd's plumbing but takes the state from
// the caller so the test can inspect it after Execute(). Stays in sync
// with newRootCmd by calling the same helper functions.
func buildTestRoot(state *cliState) *cobra.Command {
	root := &cobra.Command{
		Use:           "prism",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&state.root, "root", ".", "")
	root.PersistentFlags().StringVar(&state.globalRoot, "global", "", "")
	root.PersistentFlags().BoolVar(&state.noGlobal, "no-global", false, "")
	root.PersistentFlags().BoolVar(&state.noHookWrappers, "no-hook-wrappers", false, "")
	root.AddCommand(newCapabilitiesCmd(state))
	return root
}
