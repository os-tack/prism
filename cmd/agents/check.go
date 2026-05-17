package main

import (
	"errors"
	"fmt"
	"os"

	"agents.dev/agents/internal/engine"
	"github.com/spf13/cobra"
)

func newCheckCmd(state *cliState) *cobra.Command {
	var rawTargets []string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify that projected files are up to date",
		Long:  "Run compile in dry-run mode. Exit 0 if no drift, 1 if drift is detected, 2 on any other error.",
		// We handle exit codes ourselves rather than going through cobra's
		// default error path, which would always return 2.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(splitTargets(rawTargets), true, false)
			if err != nil {
				return err
			}
			rep, runErr := engine.Check(opts)
			out := cmd.OutOrStdout()

			if runErr != nil && !errors.Is(runErr, engine.ErrDrift) {
				fmt.Fprintln(os.Stderr, "error:", runErr)
				os.Exit(2)
			}

			if errors.Is(runErr, engine.ErrDrift) {
				fmt.Fprintln(out, "Drift detected. The following changes would be applied:")
				if rep != nil {
					for _, op := range rep.Operations {
						printOperation(out, op)
					}
					fmt.Fprintf(out, "Drift: %d would change, %d unchanged, %d removed, %d warnings\n",
						rep.Changed, rep.Unchanged, rep.Removed, len(rep.Warnings))
				}
				os.Exit(1)
			}

			if rep != nil {
				fmt.Fprintf(out, "No drift. %d files in sync.\n", rep.Unchanged)
			} else {
				fmt.Fprintln(out, "No drift.")
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&rawTargets, "target", nil, "restrict to specific plugin (repeatable or comma-separated)")
	return cmd
}
