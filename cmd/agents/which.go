package main

import (
	"fmt"
	"os"

	"agents.dev/agents/internal/engine"
	"github.com/spf13/cobra"
)

func newWhichCmd(state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "which <projected-file>",
		Short: "Show the .agents/ source(s) for a projected file",
		Long:  "Given a projected file path, print the .agents/-relative source paths that produced it, one per line. Exits 1 if the file is not tracked.",
		Args:  cobra.ExactArgs(1),
		// We manage our own exit codes here.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(nil, true, true)
			if err != nil {
				return err
			}
			sources, err := engine.Which(opts, args[0])
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			if len(sources) == 0 {
				fmt.Fprintf(os.Stderr, "not tracked: %s\n", args[0])
				os.Exit(1)
			}
			out := cmd.OutOrStdout()
			for _, s := range sources {
				fmt.Fprintln(out, s)
			}
			return nil
		},
	}
	return cmd
}
