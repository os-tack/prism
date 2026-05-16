package main

import (
	"fmt"

	"agents.dev/agents/internal/engine"
	"github.com/spf13/cobra"
)

func newInitCmd(state *cliState) *cobra.Command {
	var from string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a new .agents/ directory",
		Long:  "Autodetect AI tools in use, scaffold .agents/, and optionally import existing config from another tool (e.g., --from claude).",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := state.options(nil, false, false)
			if err := engine.Init(opts, from); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if from != "" {
				fmt.Fprintf(out, "Initialized .agents/ in %s (imported from %s)\n", state.root, from)
			} else {
				fmt.Fprintf(out, "Initialized .agents/ in %s\n", state.root)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "import from existing tool config (e.g., claude)")
	return cmd
}
