// Package version exposes both the binary release string (version.go) and
// the canonical schema major version this build implements (this file).
//
// SchemaVersion is the single source of truth for the canonical schema
// integer carried at the top of `.agents/agents.config.yaml`. Bumps require
// migration tooling per SPEC §8.
package version

// SchemaVersion is the major version of the canonical schema this binary
// implements. v0.9.0 ships schema v2; v1 (the implicit, schema_version-less
// form used in v0.8 and prior) is no longer a supported read path.
//
// Bumps require:
//   - A SPEC.md major-version section.
//   - Deprecation warnings for one full minor cycle BEFORE the bump.
//   - A `prism migrate --from N-1 --to N` available in the release that
//     ships version N.
//   - Back-compat read of N-1 for one minor cycle of the binary that
//     shipped N.
//
// See SPEC §8 for the full versioning policy.
const SchemaVersion = 2
