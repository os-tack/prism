package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agents.dev/agents/internal/registry"
	"github.com/spf13/cobra"
)

func newAddCmd(state *cliState) *cobra.Command {
	var (
		ref    string
		as     string
		global bool
		force  bool
		yes    bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Install a skill / capability package from Git or a local path",
		Long: `Install a package of canonical .agents/-shaped content from a Git
repository or a local directory.

Sources:
  github.com/owner/repo[/subpath][@ref]   Clone over HTTPS, optional ref / subpath.
  ./local/path                            Copy from a local directory.

v0.5 supports installation only. HTTP tarball, central registry,
and signature verification are intentionally out-of-scope.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]
			root := installRoot(state, global)

			if _, err := os.Stat(filepath.Join(root, ".agents")); err != nil {
				return fmt.Errorf("no .agents/ directory at %s; run `agents init` first", root)
			}

			opts := registry.InstallOptions{
				Ref:    ref,
				Target: as,
				Global: global,
				Force:  force,
				Yes:    yes,
			}

			out := cmd.OutOrStdout()

			if dryRun {
				fmt.Fprintf(out, "[dry-run] would install %s into %s\n", source, root)
				if ref != "" {
					fmt.Fprintf(out, "  ref: %s\n", ref)
				}
				if as != "" {
					fmt.Fprintf(out, "  as:  %s\n", as)
				}
				if force {
					fmt.Fprintln(out, "  force: true (would overwrite existing package)")
				}
				return nil
			}

			// Interactive confirmation when stdin is a TTY and --yes wasn't given.
			if !yes && isTerminal(os.Stdin) {
				fmt.Fprintf(out, "Install %s into %s? [y/N] ", source, root)
				if !readYes(os.Stdin) {
					fmt.Fprintln(out, "aborted")
					return nil
				}
			}

			pkg, err := registry.Install(root, source, opts)
			if err != nil {
				if errors.Is(err, registry.ErrAlreadyInstalled) {
					return fmt.Errorf("%w (use --force to reinstall)", err)
				}
				return err
			}
			fmt.Fprintf(out, "Installed %s (%d files) at %s\n",
				pkg.Name, len(pkg.Files), filepath.Join(".agents", pkg.Target))
			if pkg.Ref != "" {
				fmt.Fprintf(out, "  ref: %s\n", pkg.Ref)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ref, "ref", "", "git ref (tag, branch, or commit SHA)")
	cmd.Flags().StringVar(&as, "as", "", "override install target directory (under .agents/)")
	cmd.Flags().BoolVar(&global, "global", false, "install into ~/.agents/ instead of the project")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing installation")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be installed without writing")
	return cmd
}

// installRoot returns the root directory the package should be installed
// into: ~/.agents/'s parent (i.e. $HOME) when --global, otherwise --root.
func installRoot(state *cliState, global bool) string {
	if global {
		if state.globalRoot != "" {
			return state.globalRoot
		}
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	return state.root
}

// readYes returns true if the user typed y / Y / yes.
func readYes(r *os.File) bool {
	s := bufio.NewScanner(r)
	if !s.Scan() {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(s.Text()))
	return resp == "y" || resp == "yes"
}

// isTerminal returns true when f appears to be a TTY. We avoid pulling in
// golang.org/x/term and just check the device mode on the file descriptor.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
