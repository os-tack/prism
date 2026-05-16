package main

import (
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/plugins"
)

// registerPlugins registers every built-in projection plugin onto reg.
func registerPlugins(reg *plugin.Registry) {
	reg.Register(plugins.NewClaude())
	reg.Register(plugins.NewCursor())
	reg.Register(plugins.NewAgentsMD())
	reg.Register(plugins.NewGemini())
	reg.Register(plugins.NewCline())
	reg.Register(plugins.NewContinue())
	reg.Register(plugins.NewWindsurf())
	reg.Register(plugins.NewCopilot())
}
