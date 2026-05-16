package main

import (
	"errors"
	"fmt"
	"os"

	"agents.dev/agents/internal/registry"
	"github.com/spf13/cobra"
)

func newRemoveCmd(state *cliState) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Uninstall a previously-added skill / capability package",
		Long: `Remove a package installed with 'agents add'. Tracked files whose
on-disk hash still matches what was recorded at install time are deleted;
files that have been modified are preserved and listed as warnings (and
the package entry is also preserved so you can resolve the drift and
re-run remove).`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			root := installRoot(state, global)
			out := cmd.OutOrStdout()

			err := registry.Remove(root, name)
			if err == nil {
				fmt.Fprintf(out, "Removed %s\n", name)
				return nil
			}

			var drift *registry.RemoveDriftError
			if errors.As(err, &drift) {
				fmt.Fprintf(os.Stderr, "remove %s: preserved %d file(s) due to drift:\n", drift.Package, len(drift.Warnings))
				for _, w := range drift.Warnings {
					fmt.Fprintf(os.Stderr, "  - %s\n", w)
				}
				os.Exit(1)
			}
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
			return nil
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "remove from ~/.agents/ instead of the project")
	return cmd
}
