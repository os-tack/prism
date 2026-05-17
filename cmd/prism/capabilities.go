package main

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"agents.dev/agents/internal/plugin"
	"github.com/spf13/cobra"
)

// supportLabel maps Support values to the compact display string used in
// the capability matrix.
func supportLabel(s plugin.Support) string {
	switch s {
	case plugin.SupportNative:
		return "native"
	case plugin.SupportDegraded:
		return "degr."
	case plugin.SupportUnsupported:
		return "----"
	default:
		// Unknown / zero-value support — render as unsupported.
		return "----"
	}
}

func newCapabilitiesCmd(state *cliState) *cobra.Command {
	var target string

	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Show the capability matrix for every registered plugin",
		Long:  "Print a matrix of how each plugin supports each canonical capability (native / degraded / unsupported). Does not touch the filesystem.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.ensureRegistry(); err != nil {
				return err
			}
			plugins := state.registry.All()

			if target != "" {
				p := state.registry.Get(target)
				if p == nil {
					return fmt.Errorf("no plugin registered with name %q", target)
				}
				plugins = []plugin.Plugin{p}
			} else {
				sort.Slice(plugins, func(i, j int) bool {
					return plugins[i].Name() < plugins[j].Name()
				})
			}

			out := cmd.OutOrStdout()
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PLUGIN\tCONTEXT\tPATHS\tSEMANTIC\tSKILLS\tCMDS\tAGENTS\tHOOKS\tPERMS\tMCP")
			for _, p := range plugins {
				caps := p.Capabilities()
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					p.Name(),
					supportLabel(caps.Context),
					supportLabel(caps.ScopePaths),
					supportLabel(caps.ScopeSemantic),
					supportLabel(caps.Skills),
					supportLabel(caps.Commands),
					supportLabel(caps.Agents),
					supportLabel(caps.Hooks),
					supportLabel(caps.Permissions),
					supportLabel(caps.MCP),
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "show only this plugin's capability row")
	return cmd
}
