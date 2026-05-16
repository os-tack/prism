// Package plan runs plugins against a parsed project and collects their
// Operations, detecting plugin-vs-plugin write conflicts.
package plan

import (
	"fmt"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// Run executes plugin Plan() calls and returns the merged Operation list,
// plus any plugin-emitted warnings.
//
// If targets is empty, every registered plugin whose Detect(proj.Root)
// returns true is run. If targets is set, only those plugins run
// (regardless of Detect), and any whose TargetOption.Disabled is true is
// skipped silently.
//
// Two plugins writing the same path is an error.
func Run(proj *model.Project, registry *plugin.Registry, targets []string) ([]plugin.Operation, []plugin.Warning, error) {
	if proj == nil {
		return nil, nil, fmt.Errorf("plan: nil project")
	}
	if registry == nil {
		return nil, nil, fmt.Errorf("plan: nil registry")
	}

	var selected []plugin.Plugin

	// If no explicit targets, honor proj.Config.Targets; otherwise auto-detect.
	if len(targets) == 0 && proj.Config != nil && len(proj.Config.Targets) > 0 {
		targets = proj.Config.Targets
	}

	if len(targets) == 0 {
		// Auto-detect.
		all := registry.All()
		sort.Slice(all, func(i, j int) bool { return all[i].Name() < all[j].Name() })
		for _, p := range all {
			if isDisabled(proj, p.Name()) {
				continue
			}
			if p.Detect(proj.Root) {
				selected = append(selected, p)
			}
		}
	} else {
		for _, name := range targets {
			p := registry.Get(name)
			if p == nil {
				return nil, nil, fmt.Errorf("plan: unknown target %q", name)
			}
			if isDisabled(proj, name) {
				continue
			}
			selected = append(selected, p)
		}
	}

	var ops []plugin.Operation
	var warnings []plugin.Warning
	// Track which plugin wrote each path so conflicts list both producers.
	owner := make(map[string]string)

	for _, p := range selected {
		opt := targetOption(proj, p.Name())
		planOps, err := p.Plan(proj, opt)
		if err != nil {
			return nil, nil, fmt.Errorf("plan: %s: %w", p.Name(), err)
		}
		for i := range planOps {
			op := planOps[i]
			if op.Plugin == "" {
				op.Plugin = p.Name()
			}
			if existing, dup := owner[op.Path]; dup && existing != op.Plugin {
				return nil, nil, fmt.Errorf("plan: write conflict on %s: %s vs %s",
					op.Path, existing, op.Plugin)
			}
			owner[op.Path] = op.Plugin
			ops = append(ops, op)
			warnings = append(warnings, op.Warnings...)
		}
	}

	// Deterministic order for stable output.
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Plugin != ops[j].Plugin {
			return ops[i].Plugin < ops[j].Plugin
		}
		return ops[i].Path < ops[j].Path
	})

	return ops, warnings, nil
}

func isDisabled(proj *model.Project, name string) bool {
	if proj.Config == nil {
		return false
	}
	opt, ok := proj.Config.TargetOptions[name]
	if !ok {
		return false
	}
	return opt.Disabled
}

func targetOption(proj *model.Project, name string) model.TargetOption {
	if proj.Config == nil {
		return model.TargetOption{}
	}
	return proj.Config.TargetOptions[name]
}

// JoinPlugins is a tiny convenience used by tests.
func JoinPlugins(ps []plugin.Plugin) string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name())
	}
	return strings.Join(names, ",")
}
