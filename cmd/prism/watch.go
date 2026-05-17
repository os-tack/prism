package main

import (
	"fmt"

	"agents.dev/agents/internal/engine"
	"github.com/spf13/cobra"
)

func newWatchCmd(state *cliState) *cobra.Command {
	var rawTargets []string

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Compile and watch .agents/ for changes",
		Long:  "Run compile once, then watch .agents/ for changes and re-run on each change (debounced). Blocks until interrupted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(splitTargets(rawTargets), false, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Watching %s (Ctrl+C to exit)...\n", state.root)
			// engine.Watch is responsible for installing signal handlers and
			// returning cleanly on Ctrl+C; we just propagate its error.
			return engine.Watch(opts)
		},
	}

	cmd.Flags().StringSliceVar(&rawTargets, "target", nil, "restrict to specific plugin (repeatable or comma-separated)")
	return cmd
}
