package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newMigrateCmd is the deferred-for-v0.9.0 migrate stub per
// IMPLEMENTATION_PLAN.md §3. The command name is reserved so future
// schema bumps (v2 → v3 etc.) have a stable place to land migration
// logic.
//
// v0.9.0 is the greenfield introduction of canonical schema v2 (SPEC
// §10). There is no v1 source format to migrate from, so the command
// runs as a no-op and exits 0 with an informational message. Real
// migration logic ships with the v1.0+ schema bumps per SPEC §8.
func newMigrateCmd(state *cliState) *cobra.Command {
	_ = state // reserved for v1.0+ when the command becomes real
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Reserved: migrate .agents/ between canonical schema versions",
		Long: `Reserved for v1.0+. v0.9.0 is the greenfield introduction of canonical
schema v2, so there is no migration source format yet. The command
exits 0 and prints an informational notice so scripts that pre-add
"prism migrate" to CI never error.

When v3 ships, this command will gain "--from v2 --to v3" flags per
SPEC §8 and migrate .agents/ files in place.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(),
				"prism migrate: no-op in v0.9.0 (greenfield schema v2 — nothing to migrate).")
			fmt.Fprintln(cmd.OutOrStdout(),
				"  Real migration tooling ships with the first post-v2 schema bump.")
			fmt.Fprintln(cmd.OutOrStdout(),
				"  See SPEC.md §8 (versioning policy) and §10 (migration).")
			return nil
		},
	}
	return cmd
}
