// Package version holds the single source of truth for the prism release
// string. Embedding it in lockfiles, HTTP User-Agents, and any other
// build-stamp surfaces keeps them aligned automatically across releases.
package version

// Version is the current prism release. Bump this in lockstep with the
// project's tagged release; every consumer (engine.compile, registry HTTP
// client, etc.) reads from here so there is only one place to update.
const Version = "0.7.3"
