package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
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

// perPrimitiveFieldGroups names the v2 per-primitive FieldCapabilities
// groups that may appear on plugin.Capabilities once SPEC §6.2 lands.
// Discovered via reflection so this file compiles against both the v0.8
// struct (these field names absent) and the v2 struct (these present).
// Each entry is (struct-field, header-label) — header-label is shown in
// the detail line for the plugin.
var perPrimitiveFieldGroups = []struct {
	StructField string
	Header      string
}{
	{"AgentFields", "AGENT"},
	{"SkillFields", "SKILL"},
	{"CommandFields", "COMMAND"},
	{"HookFields", "HOOK"},
	{"MCPServerFields", "MCP"},
	{"PermissionsFields", "PERMS"},
	{"ScopeFields", "SCOPE"},
}

func newCapabilitiesCmd(state *cliState) *cobra.Command {
	var (
		target     string
		showFields bool
	)

	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Show the capability matrix for every registered plugin",
		Long: "Print a matrix of how each plugin supports each canonical " +
			"capability (native / degraded / unsupported). Use --fields to " +
			"also emit a per-primitive, per-field breakdown when the plugin " +
			"exports v2 FieldCapabilities. Does not touch the filesystem.",
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
			if err := w.Flush(); err != nil {
				return err
			}

			// Per-field detail. The v2 plugin.Capabilities struct adds one
			// FieldCapabilities sub-struct per primitive (AgentFields,
			// SkillFields, etc., per SPEC §6.2). We reach for these via
			// reflection so this code keeps building during the v0.8 → v2
			// transition: when the fields are absent (v0.8 struct), the
			// detail block is silently skipped; when present and non-zero,
			// we emit one line per primitive per plugin.
			//
			// --fields forces the detail block to print even when the
			// fields are zero-valued; without it, primitives with no
			// declared per-field support are elided to keep the default
			// output close to the v0.8 shape (test parity).
			for _, p := range plugins {
				lines := perFieldLines(p, showFields)
				if len(lines) == 0 {
					continue
				}
				fmt.Fprintf(out, "\n%s per-field:\n", p.Name())
				for _, ln := range lines {
					fmt.Fprintf(out, "  %s\n", ln)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "show only this plugin's capability row")
	cmd.Flags().BoolVar(&showFields, "fields", false, "print every per-field capability cell, including zero-valued ones")
	return cmd
}

// perFieldLines returns one detail line per v2 FieldCapabilities group
// declared on the plugin's Capabilities() struct, or nil when the struct
// predates v2 (no per-primitive sub-structs).
//
// When force is false, groups whose sub-struct is the zero value are
// elided. When true, every group is emitted, with empty groups shown as
// "(no fields declared)" — useful for diagnosing plugins that haven't
// yet been updated to declare per-field support.
func perFieldLines(p plugin.Plugin, force bool) []string {
	caps := p.Capabilities()
	v := reflect.ValueOf(caps)
	if v.Kind() != reflect.Struct {
		return nil
	}

	var out []string
	for _, g := range perPrimitiveFieldGroups {
		fv := v.FieldByName(g.StructField)
		if !fv.IsValid() {
			// v0.8 struct — group absent. Skip the entire detail block
			// for this plugin since none of the groups will be present.
			return nil
		}
		if fv.Kind() != reflect.Struct {
			continue
		}
		isZero := fv.IsZero()
		if isZero && !force {
			continue
		}
		out = append(out, formatFieldGroup(g.Header, fv, isZero))
	}
	return out
}

// formatFieldGroup renders one FieldCapabilities sub-struct as a
// human-readable line: "AGENT  Name=native; Description=native; Tools=degraded".
// Each leaf field of the sub-struct is expected to be a plugin.Support
// (string-compatible) or a thin wrapper around one. We render any leaf
// whose Kind is reflect.String (covers Support) or whose underlying
// type implements Stringer.
func formatFieldGroup(header string, fv reflect.Value, isZero bool) string {
	if isZero {
		return fmt.Sprintf("%s  (no fields declared)", header)
	}
	var parts []string
	t := fv.Type()
	for i := 0; i < fv.NumField(); i++ {
		f := fv.Field(i)
		ft := t.Field(i)
		if !ft.IsExported() {
			continue
		}
		label := renderSupportLeaf(f)
		if label == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", ft.Name, label))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%s  (no fields declared)", header)
	}
	return fmt.Sprintf("%s  %s", header, strings.Join(parts, "; "))
}

// renderSupportLeaf converts a reflect.Value that is expected to hold a
// plugin.Support (or a Stringer wrapper around one) to its compact label.
// Returns "" for unrecognized shapes so the caller can skip the field.
func renderSupportLeaf(f reflect.Value) string {
	switch f.Kind() {
	case reflect.String:
		// plugin.Support is a string alias; route through supportLabel so
		// "native"/"degraded"/"unsupported" all render in the canonical
		// short form.
		return supportLabel(plugin.Support(f.String()))
	}
	if s, ok := f.Interface().(fmt.Stringer); ok {
		return s.String()
	}
	return ""
}
