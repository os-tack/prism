package plugins

import (
	"path/filepath"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/perms"
	"agents.dev/agents/internal/plugin"
)

// emitPermsGuardWrappers builds the OpWrite operations that materialize the
// prism perms-guard wrapper script + sidecar policy JSON for a plugin that
// lacks a native permissions primitive (currently gemini, continue).
//
// One sidecar policy file is emitted per scope (global or scoped). Each
// proj.Hook (if any) is wrapped with its own perms-guard script pointed
// at the matching scope's policy; if no hooks exist but permissions do,
// a single gate wrapper is emitted per policy so callers can wire it
// into their own pipeline (the wrapper is a no-op exec when no script
// is supplied, returning exit 0 on allow).
//
// disabled mirrors ClaudePlugin.DisableHookWrappers — when true, the
// function returns nil ops + nil warnings, leaving the projection
// untouched (callers can still emit their own warnings if they want).
//
// The on-disk layout (under <pluginName>):
//
//	.<pluginName>/hooks/__perms-guard__/policy.json                — global policy
//	.<pluginName>/hooks/__perms-guard__/<scope-slug>.policy.json   — scoped policy
//	.<pluginName>/hooks/__perms-guard__/<event>-<basename>.sh      — wrapper per hook
//	.<pluginName>/hooks/__perms-guard__/<scope-slug>-<event>-<basename>.sh
//	.<pluginName>/hooks/__perms-guard__/global-gate.sh             — bare gate when no hooks
//
// All paths are project-relative; the engine resolves absolute paths.
func emitPermsGuardWrappers(pluginName string, proj *model.Project, disabled bool) ([]plugin.Operation, []plugin.Warning, error) {
	if disabled || proj == nil {
		return nil, nil, nil
	}

	hasGlobal := proj.Permissions != nil && (len(proj.Permissions.Allow)+len(proj.Permissions.Deny)+len(proj.Permissions.Ask) > 0)
	var scopedNonEmpty []*model.Permissions
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		if len(sp.Allow)+len(sp.Deny)+len(sp.Ask) == 0 {
			continue
		}
		scopedNonEmpty = append(scopedNonEmpty, sp)
	}
	if !hasGlobal && len(scopedNonEmpty) == 0 {
		return nil, nil, nil
	}

	hooksDir := filepath.Join("."+pluginName, "hooks", "__perms-guard__")

	var ops []plugin.Operation
	policyPaths := map[string]string{} // scope-path (or "") → project-relative policy path

	if hasGlobal {
		policyRel := filepath.Join(hooksDir, "policy.json")
		raw, err := perms.Marshal(&perms.Policy{
			Allow: proj.Permissions.Allow,
			Deny:  proj.Permissions.Deny,
			Ask:   proj.Permissions.Ask,
		})
		if err != nil {
			return nil, nil, err
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    policyRel,
			Content: string(raw),
			Mode:    plugin.ModeWrite,
			Sources: []string{"permissions.yaml"},
			Plugin:  pluginName,
		})
		policyPaths[""] = policyRel
	}
	for _, sp := range scopedNonEmpty {
		policyRel := filepath.Join(hooksDir, permsScopeSlug(sp.ScopePath)+".policy.json")
		raw, err := perms.Marshal(&perms.Policy{
			Allow: sp.Allow,
			Deny:  sp.Deny,
			Ask:   sp.Ask,
		})
		if err != nil {
			return nil, nil, err
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    policyRel,
			Content: string(raw),
			Mode:    plugin.ModeWrite,
			Sources: []string{filepath.Join(sp.ScopePath, "permissions.yaml")},
			Plugin:  pluginName,
		})
		policyPaths[sp.ScopePath] = policyRel
	}

	// Per-hook wrappers when hooks exist. Scoped hooks pick up the matching
	// scope policy (falling back to global if no scoped policy is set).
	var wroteHookWrapper bool
	for _, h := range proj.Hooks {
		if h == nil || h.Event == "" {
			continue
		}
		policyRel, ok := policyPaths[h.ScopePath]
		if !ok {
			policyRel = policyPaths[""]
		}
		if policyRel == "" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		var wrapperName string
		if h.ScopePath == "" {
			wrapperName = h.Event + "-" + base + ".sh"
		} else {
			wrapperName = permsScopeSlug(h.ScopePath) + "-" + h.Event + "-" + base + ".sh"
		}
		wrapperRel := filepath.Join(hooksDir, wrapperName)
		body := buildPermsGuardScript(filepath.Join(proj.Root, policyRel), h.ScriptPath)
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     wrapperRel,
			Content:  body,
			Mode:     plugin.ModeWrite,
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   pluginName,
		})
		wroteHookWrapper = true
	}

	// If permissions exist but no hooks were wrapped, drop a bare gate
	// wrapper per policy so users can integrate the guard into their own
	// hook pipeline (alias, shell function, tool-specific config).
	if !wroteHookWrapper {
		for scopePath, policyRel := range policyPaths {
			gateName := "global-gate.sh"
			if scopePath != "" {
				gateName = permsScopeSlug(scopePath) + "-gate.sh"
			}
			wrapperRel := filepath.Join(hooksDir, gateName)
			body := buildPermsGuardScript(filepath.Join(proj.Root, policyRel), "")
			ops = append(ops, plugin.Operation{
				Kind:     plugin.OpWrite,
				Path:     wrapperRel,
				Content:  body,
				Mode:     plugin.ModeWrite,
				FileMode: 0o755,
				Sources:  []string{"permissions.yaml"},
				Plugin:   pluginName,
			})
		}
	}

	return ops, nil, nil
}

// buildPermsGuardScript renders the bash wrapper that exec's prism perms-guard
// with the given policy and optional underlying hook script. Empty script
// = pure gate (perms-guard exits 0 on allow, non-zero on deny / ask-decline).
func buildPermsGuardScript(policyAbs, sourceScript string) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# prism-generated perms-guard wrapper\n")
	b.WriteString("# Reads hook JSON from stdin, enforces sidecar policy, then\n")
	b.WriteString("# exec's the underlying script (or exits 0) on allow.\n")
	b.WriteString("set -euo pipefail\n")
	if sourceScript == "" {
		b.WriteString("exec prism perms-guard --policy ")
		b.WriteString(shellQuote(policyAbs))
		b.WriteString("\n")
	} else {
		b.WriteString("exec prism perms-guard --policy ")
		b.WriteString(shellQuote(policyAbs))
		b.WriteString(" --script ")
		b.WriteString(shellQuote(sourceScript))
		b.WriteString("\n")
	}
	return b.String()
}

// permsScopeSlug renders a scope path as a filename-safe slug. Mirrors
// scope.Slug but keeps the plugins package self-contained (the cursor /
// claude scopeSlug helper lives in claude.go).
func permsScopeSlug(scopePath string) string {
	if scopePath == "" {
		return "global"
	}
	return strings.ReplaceAll(scopePath, "/", "-")
}
