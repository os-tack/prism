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
	root       string
	globalRoot string
	noGlobal   bool
	registry   *plugin.Registry
}

func (s *cliState) options(targets []string, dryRun, quiet bool) engine.Options {
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
	state := &cliState{
		registry: plugin.NewRegistry(),
	}
	registerPlugins(state.registry)

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

	root.AddCommand(newCompileCmd(state))
	root.AddCommand(newCheckCmd(state))
	root.AddCommand(newInitCmd(state))
	root.AddCommand(newDiffCmd(state))
	root.AddCommand(newWhichCmd(state))
	root.AddCommand(newWatchCmd(state))
	root.AddCommand(newCapabilitiesCmd(state))

	return root
}

func Execute() error {
	return newRootCmd().Execute()
}
