package main

import (
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/plugins"
)

// registerPlugins registers every built-in projection plugin onto reg.
//
// noHookWrappers is plumbed through to ClaudePlugin.DisableHookWrappers so
// the --no-hook-wrappers persistent flag (declared in root.go) controls
// whether scoped Claude hooks get a __scope-guard__ wrapper script. We
// construct ClaudePlugin directly instead of calling NewClaude() so we can
// pass the field at construction time.
func registerPlugins(reg *plugin.Registry, noHookWrappers bool) {
	reg.Register(&plugins.ClaudePlugin{DisableHookWrappers: noHookWrappers})
	reg.Register(plugins.NewCursor())
	reg.Register(plugins.NewAgentsMD())
	reg.Register(plugins.NewGemini())
	reg.Register(plugins.NewCline())
	reg.Register(plugins.NewContinue())
	reg.Register(plugins.NewWindsurf())
	reg.Register(plugins.NewCopilot())
}
