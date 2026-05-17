package main

import (
	"fmt"

	"agents.dev/agents/internal/engine"
	"github.com/spf13/cobra"
)

func newCompileCmd(state *cliState) *cobra.Command {
	var (
		rawTargets []string
		dryRun     bool
		quiet      bool
	)

	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Compile .agents/ into per-tool projections",
		Long:  "Parse .agents/, run all enabled plugins, and apply the resulting operations to the filesystem.",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(splitTargets(rawTargets), dryRun, quiet)
			if err != nil {
				return err
			}
			rep, err := engine.Compile(opts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if !quiet {
				for _, op := range rep.Operations {
					printOperation(out, op)
				}
				fmt.Fprintf(out, "Compiled: %d changed, %d unchanged, %d removed, %d warnings\n",
					rep.Changed, rep.Unchanged, rep.Removed, len(rep.Warnings))
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&rawTargets, "target", nil, "restrict to specific plugin (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do not touch the filesystem")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress non-error output")
	return cmd
}
