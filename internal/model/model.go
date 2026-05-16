// Package model defines the canonical agent-configuration data model.
//
// A Project is the in-memory representation of a `.agents/` directory.
// Plugins consume a Project and produce Operations; the engine applies
// Operations to the filesystem. Plugins never touch the filesystem directly.
package model

// Priority controls how strongly a scope should be surfaced in projections
// that lack native priority semantics.
type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// Document is a parsed markdown source with optional YAML frontmatter.
type Document struct {
	SourcePath  string
	Frontmatter map[string]any
	Body        string
}

// Scope is a path- or trigger-scoped context document.
type Scope struct {
	Path        string
	Globs       []string
	Description string
	Priority    Priority
	Document    *Document
}

// Skill is a triggered, optionally-scripted capability.
type Skill struct {
	Name        string
	Description string
	Trigger     string
	Globs       []string
	Document    *Document
	Scripts     []string
}

// Command is a reusable prompt template.
type Command struct {
	Name        string
	Description string
	Document    *Document
}

// Agent is a specialized persona / subagent definition.
type Agent struct {
	Name        string
	Description string
	Document    *Document
}

// Hook is a behavioral hook.
type Hook struct {
	Event      string
	Matcher    string
	ScriptPath string
}

// Permissions is the canonical allow/deny configuration.
type Permissions struct {
	Allow []string
	Deny  []string
	Ask   []string
}

// MCPServer is a single MCP server configuration.
type MCPServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
}

// Project is the complete canonical model parsed from a .agents/ directory.
type Project struct {
	Root            string
	AgentsDir       string
	GlobalAgentsDir string
	Context         *Document
	Scopes          []*Scope
	Skills          []*Skill
	Commands        []*Command
	Agents          []*Agent
	Hooks           []*Hook
	Permissions     *Permissions
	MCP             []*MCPServer
	Ignore          []string
	Config          *Config
}

// Config is the user-controllable behavior knobs.
type Config struct {
	Targets       []string
	TargetOptions map[string]TargetOption
}

// TargetOption is a per-target override.
type TargetOption struct {
	Mode     string
	Disabled bool
}
