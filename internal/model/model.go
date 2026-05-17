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
	// Includes lists the absolute paths of every file included into Body via
	// the `<!-- include: path -->` directive. Used by the lockfile for
	// reverse-trace (agents which) and by watch mode to retrigger compile
	// when an included file changes. Plugins that support ModeSymlink must
	// downgrade to ModeWrite when len(Includes) > 0 since the symlink
	// target would only contain the unexpanded source.
	Includes []string
}

// NeedsWrite reports whether plugins must use ModeWrite rather than
// ModeSymlink for this document. True when @include expansion produced
// content that differs from the on-disk source.
func (d *Document) NeedsWrite() bool {
	return d != nil && len(d.Includes) > 0
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
//
// ScopePath is the .agents/-relative directory the skill lives under, e.g.
// "src/billing". Empty = global (the skill lives at the .agents/ root).
// When non-empty, the skill inherits the scope's globs as a default if its
// own Globs slice is empty.
type Skill struct {
	Name        string
	Description string
	Trigger     string
	Globs       []string
	Document    *Document
	Scripts     []string
	ScopePath   string
}

// Command is a reusable prompt template.
type Command struct {
	Name        string
	Description string
	Document    *Document
	ScopePath   string
}

// Agent is a specialized persona / subagent definition.
type Agent struct {
	Name        string
	Description string
	Document    *Document
	ScopePath   string
}

// Hook is a behavioral hook.
type Hook struct {
	Event      string
	Matcher    string
	ScriptPath string
	ScopePath  string
}

// Permissions is the canonical allow/deny configuration.
// ScopePath is empty for the global permissions block on Project.Permissions
// and non-empty for entries under Project.ScopedPermissions.
type Permissions struct {
	Allow     []string
	Deny      []string
	Ask       []string
	ScopePath string
}

// MCPServer is a single MCP server configuration.
type MCPServer struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	ScopePath string
}

// Project is the complete canonical model parsed from a .agents/ directory.
type Project struct {
	Root              string
	AgentsDir         string
	GlobalAgentsDir   string
	Context           *Document
	Scopes            []*Scope
	Skills            []*Skill
	Commands          []*Command
	Agents            []*Agent
	Hooks             []*Hook
	Permissions       *Permissions
	ScopedPermissions []*Permissions
	MCP               []*MCPServer
	Ignore            []string
	Config            *Config
	// Packages records installed packages (via `agents add`) for removal,
	// update, and conflict detection. The on-disk source is .agents/packages.yaml.
	Packages []*Package
}

// Package records one installed registry package. The package's content
// (skills, commands, etc.) lives at the canonical paths and is parsed via
// the normal capability walkers; this struct is only bookkeeping.
type Package struct {
	Name        string
	Source      string
	Ref         string
	SHA         string // aggregate, kept for back-compat with v0.5 lockfiles
	InstalledAt string
	Target      string
	Files       []FileEntry
}

// FileEntry is one file tracked by an installed Package. Hash is the
// SHA-256 of the file content at install time. Empty Hash indicates a
// v0.5-migrated entry where per-file hashes weren't recorded; callers
// should fall back to aggregate-SHA semantics for drift detection in
// that case.
type FileEntry struct {
	Path string
	Hash string
}

// Config is the user-controllable behavior knobs.
type Config struct {
	Targets       []string
	TargetOptions map[string]TargetOption
	// Include controls the @include preprocessor behavior.
	Include IncludeConfig
}

// IncludeConfig is the include-directive configuration loaded from
// agents.config.yaml under the "include:" key.
type IncludeConfig struct {
	// MaxDepth caps how many nested includes will be expanded before erroring.
	// Defaults to 16 when zero.
	MaxDepth int
}

// TargetOption is a per-target override.
type TargetOption struct {
	Mode     string
	Disabled bool
}
