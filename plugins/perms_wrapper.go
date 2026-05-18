package plugins

import (
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/perms"
	"agents.dev/agents/internal/plugin"
)

// emitPermsGuardWrappers builds the OpWrite operations that materialize the
// prism perms-guard wrapper script + sidecar policy JSON for a plugin that
// lacks a native permissions primitive (currently gemini, cline, copilot).
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
// The on-disk layout (under <hooksRoot>/__perms-guard__/, where hooksRoot
// defaults to .<pluginName>/hooks but can be overridden — copilot uses
// .github/hooks, for example):
//
//	<hooksRoot>/__perms-guard__/policy.json                — global policy
//	<hooksRoot>/__perms-guard__/<scope-slug>.policy.json   — scoped policy
//	<hooksRoot>/__perms-guard__/<event>-<basename>.sh      — wrapper per hook
//	<hooksRoot>/__perms-guard__/<scope-slug>-<event>-<basename>.sh
//	<hooksRoot>/__perms-guard__/global-gate.sh             — bare gate when no hooks
//
// All paths are project-relative; the engine resolves absolute paths.
func emitPermsGuardWrappers(pluginName string, proj *model.Project, disabled bool) ([]plugin.Operation, []plugin.Warning, error) {
	return emitPermsGuardWrappersAt(pluginName, filepath.Join("."+pluginName, "hooks"), proj, disabled)
}

// emitPermsGuardWrappersAt is the same as emitPermsGuardWrappers but lets
// the caller specify an explicit hooksRoot (project-relative). Plugins
// whose hook config lives outside their plugin-named dotdir (copilot
// emits to .github/) use this entry point. hooksRoot is the parent of
// the __perms-guard__ directory — e.g. ".github/hooks" yields
// ".github/hooks/__perms-guard__/...".
func emitPermsGuardWrappersAt(pluginName, hooksRoot string, proj *model.Project, disabled bool) ([]plugin.Operation, []plugin.Warning, error) {
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

	hooksDir := filepath.Join(hooksRoot, "__perms-guard__")

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
		body := buildPermsGuardScript(wrapperRel, policyRel, formatScriptArg(h.ScriptPath, proj.Root))
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
	// Iterate map keys in sorted order so OpWrite ops emit deterministically
	// across runs (fixes I5: `agents diff` drift from map iteration).
	if !wroteHookWrapper {
		scopes := make([]string, 0, len(policyPaths))
		for k := range policyPaths {
			scopes = append(scopes, k)
		}
		sort.Strings(scopes)
		for _, scopePath := range scopes {
			policyRel := policyPaths[scopePath]
			gateName := "global-gate.sh"
			if scopePath != "" {
				gateName = permsScopeSlug(scopePath) + "-gate.sh"
			}
			wrapperRel := filepath.Join(hooksDir, gateName)
			body := buildPermsGuardScript(wrapperRel, policyRel, "")
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
//
// wrapperRel and policyRel are project-relative paths. scriptArg is the
// already-formatted shell argument for --script: empty for pure gate,
// otherwise a pre-quoted string (either shellQuote(absolute) or
// "${PROJECT_DIR}"/shellQuote(rel) — produced by formatScriptArg at the
// call site so the renderer doesn't need proj.Root).
//
// The script resolves the project root at runtime from ${BASH_SOURCE[0]}
// so the generated wrapper survives `mv` of the project directory
// (fixes I4). Env-var precedence: PRISM_PROJECT_DIR > CLAUDE_PROJECT_DIR
// > computed from BASH_SOURCE via the wrapper's path-from-root depth.
func buildPermsGuardScript(wrapperRel, policyRel, scriptArg string) string {
	upDots := rootRelativeFromWrapper(wrapperRel)

	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# prism-generated perms-guard wrapper\n")
	b.WriteString("# Reads hook JSON from stdin, enforces sidecar policy, then\n")
	b.WriteString("# exec's the underlying script (or exits 0) on allow.\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("SCRIPT_DIR=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"\n")
	b.WriteString("PROJECT_DIR=\"${PRISM_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$(cd \"${SCRIPT_DIR}/")
	b.WriteString(upDots)
	b.WriteString("\" && pwd)}}\"\n")
	b.WriteString("exec prism perms-guard --policy \"${PROJECT_DIR}\"/")
	b.WriteString(shellQuote(policyRel))
	if scriptArg != "" {
		b.WriteString(" --script ")
		b.WriteString(scriptArg)
	}
	b.WriteString("\n")
	return b.String()
}

// formatScriptArg builds the pre-quoted shell argument for --script that
// callers pass to buildPermsGuardScript or buildScopeGuardScript. When
// the script lives under projRoot we rewrite to "${PROJECT_DIR}"/<rel>
// so the wrapper survives `mv` of the project (fixes I-1 from v0.7.1
// review). When it lives outside (e.g., a global hook from ~/.agents),
// we fall back to shellQuote(absolute).
func formatScriptArg(scriptPath, projRoot string) string {
	if scriptPath == "" {
		return ""
	}
	absScript := scriptPath
	if !filepath.IsAbs(absScript) {
		absScript = filepath.Join(projRoot, absScript)
	}
	absRoot := projRoot
	if !filepath.IsAbs(absRoot) {
		absRoot, _ = filepath.Abs(absRoot)
	}
	rel, err := filepath.Rel(absRoot, absScript)
	if err != nil || strings.HasPrefix(rel, "..") {
		return shellQuote(scriptPath)
	}
	return "\"${PROJECT_DIR}\"/" + shellQuote(filepath.ToSlash(rel))
}

// rootRelativeFromWrapper returns the "../../.."-style suffix that, joined
// onto the wrapper's directory, lands at the project root. wrapperRel is
// project-relative; the result has one ".." per path component in
// filepath.Dir(wrapperRel). Returns "." when the wrapper lives at root.
func rootRelativeFromWrapper(wrapperRel string) string {
	// Clean defensively so callers passing `.gemini/./hooks/wrapper.sh` or
	// `.gemini//hooks/wrapper.sh` get the same depth as the canonical form.
	// filepath.Dir already Cleans internally on every platform Go supports,
	// but the explicit Clean here makes the function safe for direct unit
	// tests passing arbitrary strings (N-a from v0.7.1 review).
	cleaned := filepath.Clean(wrapperRel)
	dir := filepath.ToSlash(filepath.Dir(cleaned))
	if dir == "." || dir == "" {
		return "."
	}
	parts := strings.Split(dir, "/")
	ups := make([]string, len(parts))
	for i := range parts {
		ups[i] = ".."
	}
	return strings.Join(ups, "/")
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
