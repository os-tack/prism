package main

import (
	"fmt"

	"agents.dev/agents/internal/engine"
	"github.com/spf13/cobra"
)

func newInitCmd(state *cliState) *cobra.Command {
	var (
		from        string
		interactive bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a new .agents/ directory",
		Long:  "Autodetect AI tools in use, scaffold .agents/, and optionally import existing config from another tool (e.g., --from claude). With --interactive, pick which imported items to keep.",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(nil, false, false)
			if err != nil {
				return err
			}
			opts.Interactive = interactive && from != ""
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

	cmd.Flags().StringVar(&from, "from", "", "import from existing tool config: claude,cursor,gemini,cline,continue,windsurf,copilot,agents-md (comma-separated for multi-source merge; \"auto\" detects all)")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "prompt for each imported item (skill/command/scope/agent/MCP) before writing; requires --from and a TTY (non-TTY stdin is refused; EOF / Ctrl-D at a prompt accepts that item and every remaining one)")
	return cmd
}
