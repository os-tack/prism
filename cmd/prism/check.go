package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/parser"
	"agents.dev/agents/internal/validator"
	"github.com/spf13/cobra"
)

func newCheckCmd(state *cliState) *cobra.Command {
	var (
		rawTargets []string
		strict     bool
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify that projected files are up to date",
		Long: "Run Validate over .agents/ then compile in dry-run mode. " +
			"Exit 0 if no validation errors and no drift, 1 if validation " +
			"errors are present or drift is detected, 2 on any other error. " +
			"With --strict, validation warnings are promoted to errors.",
		// We handle exit codes ourselves rather than going through cobra's
		// default error path, which would always return 2.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := state.options(splitTargets(rawTargets), true, false)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Run Validate up-front. The validator package is the canonical
			// pre-plugin pass (SPEC §5.4). If parsing fails here we let the
			// downstream engine.Check produce the canonical error so the
			// "no .agents/" / parse-failure messaging stays single-sourced.
			if proj, perr := parser.ParseLayered(opts.GlobalRoot, opts.Root); perr == nil && proj != nil {
				rep := validator.Validate(proj)
				printValidationReport(out, errOut, rep)
				if len(rep.Errors) > 0 {
					fmt.Fprintf(errOut, "Validation failed: %d error(s), %d warning(s)\n",
						len(rep.Errors), len(rep.Warnings))
					os.Exit(1)
				}
				if strict && len(rep.Warnings) > 0 {
					fmt.Fprintf(errOut, "Validation failed under --strict: %d warning(s) promoted to errors\n",
						len(rep.Warnings))
					os.Exit(1)
				}
			}

			rep, runErr := engine.Check(opts)

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
	cmd.Flags().BoolVar(&strict, "strict", false, "promote validation warnings to errors (exit 1 on any warning)")
	return cmd
}

// printValidationReport renders errors and warnings from the validator.
// Errors go to stderr (so CI scrapers find them); warnings go to stdout
// (informational, may be promoted to errors under --strict).
func printValidationReport(out, errOut io.Writer, rep validator.ValidationReport) {
	for _, e := range rep.Errors {
		fmt.Fprintln(errOut, formatValidationError(e))
	}
	for _, w := range rep.Warnings {
		fmt.Fprintln(out, formatValidationError(w))
	}
}

// formatValidationError renders a single ValidationError in a compact,
// CI-friendly form: "<severity>: <file>:<line>:<col>: <field>: <msg>".
// Empty fields are elided so plugins that lack line/column tracking
// still produce readable output.
func formatValidationError(e validator.ValidationError) string {
	loc := e.File
	if e.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, e.Line)
		if e.Column > 0 {
			loc = fmt.Sprintf("%s:%d", loc, e.Column)
		}
	}
	sev := e.Severity
	if sev == "" {
		sev = "error"
	}
	parts := []string{sev}
	if loc != "" {
		parts = append(parts, loc)
	}
	if e.Field != "" {
		parts = append(parts, e.Field)
	}
	parts = append(parts, e.Message)
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += ": " + parts[i]
	}
	return out
}
