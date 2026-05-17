package main

import (
	"fmt"
	"text/tabwriter"

	"agents.dev/agents/internal/registry"
	"github.com/spf13/cobra"
)

func newListCmd(state *cliState) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed skill / capability packages",
		Long:  "Print a table of packages installed in .agents/packages.yaml.",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := installRoot(state, global)
			pkgs, err := registry.List(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(pkgs) == 0 {
				fmt.Fprintln(out, "No packages installed.")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSOURCE\tREF\tFILES")
			for _, p := range pkgs {
				ref := p.Ref
				if ref == "" {
					ref = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", p.Name, p.Source, ref, len(p.Files))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "list packages in ~/.agents/ instead of the project")
	return cmd
}
