package main

import (
	"fmt"

	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/plugins"
)

// registerPlugins registers every built-in projection plugin onto reg. Returns
// the first registration error (currently only possible if a plugin name
// collides — a programming error caught by tests). The error return mirrors
// importer.Registry.Register's contract; in production the static list below
// is collision-free and this never returns non-nil.
//
// noHookWrappers is plumbed through to ClaudePlugin.DisableHookWrappers (which
// gates the __scope-guard__ wrapper for scoped Claude hooks) and to
// GeminiPlugin.DisableHookWrappers (which gates the __perms-guard__ wrapper
// + sidecar policy emitted for permission projection on that plugin).
// We construct those plugins directly instead of calling the New*
// constructors so the field can be set at construction time.
// ContinuePlugin migrated to native permissions in v0.8 and no longer
// participates in the wrapper path.
func registerPlugins(reg *plugin.Registry, noHookWrappers bool) error {
	toRegister := []plugin.Plugin{
		&plugins.ClaudePlugin{DisableHookWrappers: noHookWrappers},
		plugins.NewCursor(),
		plugins.NewAgentsMD(),
		&plugins.GeminiPlugin{DisableHookWrappers: noHookWrappers},
		plugins.NewCline(),
		plugins.NewContinue(),
		plugins.NewWindsurf(),
		plugins.NewCopilot(),
	}
	for _, p := range toRegister {
		if err := reg.Register(p); err != nil {
			return fmt.Errorf("register %q: %w", p.Name(), err)
		}
	}
	return nil
}
