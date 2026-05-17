package main

import (
	"fmt"

	"agents.dev/agents/internal/engine"
	"github.com/spf13/cobra"
)

func newDiffCmd(state *cliState) *cobra.Command {
	var rawTargets []string

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show what compile would change without writing",
		Long:  "Like `compile --dry-run`, but exits 0 regardless of drift.",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(splitTargets(rawTargets), true, false)
			if err != nil {
				return err
			}
			rep, err := engine.Diff(opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, op := range rep.Operations {
				printOperation(out, op)
			}
			fmt.Fprintf(out, "Diff: %d would change, %d unchanged, %d removed, %d warnings\n",
				rep.Changed, rep.Unchanged, rep.Removed, len(rep.Warnings))
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&rawTargets, "target", nil, "restrict to specific plugin (repeatable or comma-separated)")
	return cmd
}
