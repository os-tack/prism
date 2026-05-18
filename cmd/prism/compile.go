package main

import (
	"fmt"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/parser"
	"agents.dev/agents/internal/validator"
	"github.com/spf13/cobra"
)

func newCompileCmd(state *cliState) *cobra.Command {
	var (
		rawTargets []string
		dryRun     bool
		quiet      bool
		strict     bool
	)

	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Compile .agents/ into per-tool projections",
		Long: "Parse .agents/, run Validate, run all enabled plugins, and " +
			"apply the resulting operations to the filesystem. Validation " +
			"errors abort the compile; warnings are surfaced and compile " +
			"proceeds. With --strict, warnings are promoted to errors.",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(splitTargets(rawTargets), dryRun, quiet)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Validate before invoking the engine. SPEC §5.4: errors block
			// compile, warnings proceed. If parser.ParseLayered itself
			// fails here, fall through and let engine.Compile produce the
			// canonical error message (single-source the parse-error path).
			if proj, perr := parser.ParseLayered(opts.GlobalRoot, opts.Root); perr == nil && proj != nil {
				rep := validator.Validate(proj)
				if !quiet {
					printValidationReport(out, errOut, rep)
				}
				if len(rep.Errors) > 0 {
					return fmt.Errorf("validation failed: %d error(s), %d warning(s)",
						len(rep.Errors), len(rep.Warnings))
				}
				if strict && len(rep.Warnings) > 0 {
					return fmt.Errorf("validation failed under --strict: %d warning(s) promoted to errors",
						len(rep.Warnings))
				}
			}

			rep, err := engine.Compile(opts)
			if err != nil {
				return err
			}

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
	cmd.Flags().BoolVar(&strict, "strict", false, "promote validation warnings to errors")
	return cmd
}
