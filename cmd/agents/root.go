package main

import (
	"os"
	"path/filepath"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/plugin"
	"github.com/spf13/cobra"
)

// cliState carries shared state between subcommands.
type cliState struct {
	root           string
	globalRoot     string
	noGlobal       bool
	noHookWrappers bool
	// registry is rebuilt by ensureRegistry on first options() call so it
	// reflects post-parse flag values (notably noHookWrappers). Subcommands
	// should never read this directly — go through options().
	registry *plugin.Registry
}

// ensureRegistry lazily builds the plugin registry the first time a
// subcommand needs one. Building it lazily (rather than at root command
// construction) is critical: cobra populates persistent-flag values
// during Execute(), AFTER newRootCmd() returns. Registering plugins at
// construction time would capture the zero value of noHookWrappers
// (false) regardless of what the user passed on the command line.
//
// Discovered during v0.6 review — the symptom was a silently-dead
// --no-hook-wrappers flag.
func (s *cliState) ensureRegistry() {
	if s.registry != nil {
		return
	}
	s.registry = plugin.NewRegistry()
	registerPlugins(s.registry, s.noHookWrappers)
}

func (s *cliState) options(targets []string, dryRun, quiet bool) engine.Options {
	s.ensureRegistry()
	global := ""
	if !s.noGlobal {
		global = s.globalRoot
	}
	return engine.Options{
		Root:       s.root,
		GlobalRoot: global,
		Registry:   s.registry,
		Targets:    targets,
		DryRun:     dryRun,
		Quiet:      quiet,
	}
}

func newRootCmd() *cobra.Command {
	state := &cliState{}

	root := &cobra.Command{
		Use:           "agents",
		Short:         "Project a canonical .agents/ directory into per-tool config files",
		Long:          "agents projects a canonical .agents/ directory into per-AI-tool config files (CLAUDE.md, .cursor/rules/*, AGENTS.md, GEMINI.md, .clinerules, .continue/rules, .windsurf/rules, .github/instructions).",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cwd, _ := os.Getwd()
	defaultGlobal := ""
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".agents")
		// Only enable global layering by default if ~/.agents/ exists.
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			defaultGlobal = home
		}
	}

	root.PersistentFlags().StringVar(&state.root, "root", cwd, "project root directory")
	root.PersistentFlags().StringVar(&state.globalRoot, "global", defaultGlobal, "global layer root (parent of ~/.agents/); empty disables")
	root.PersistentFlags().BoolVar(&state.noGlobal, "no-global", false, "skip the global layer even if --global is set")
	root.PersistentFlags().BoolVar(&state.noHookWrappers, "no-hook-wrappers", false, "disable __scope-guard__ wrapper scripts for scoped Claude hooks (projects them as global hooks instead)")

	// `capabilities` is the only subcommand that reads from state.registry
	// without going through cliState.options() — give it the same lazy
	// registry by routing through a small helper.
	root.AddCommand(newCompileCmd(state))
	root.AddCommand(newCheckCmd(state))
	root.AddCommand(newInitCmd(state))
	root.AddCommand(newDiffCmd(state))
	root.AddCommand(newWhichCmd(state))
	root.AddCommand(newWatchCmd(state))
	root.AddCommand(newCapabilitiesCmd(state))
	root.AddCommand(newScopeGuardCmd())
	root.AddCommand(newAddCmd(state))
	root.AddCommand(newRemoveCmd(state))
	root.AddCommand(newListCmd(state))

	return root
}

func Execute() error {
	return newRootCmd().Execute()
}
