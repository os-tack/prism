// Package model defines the canonical agent-configuration data model.
//
// A Project is the in-memory representation of a `.agents/` directory.
// Plugins consume a Project and produce Operations; the engine applies
// Operations to the filesystem. Plugins never touch the filesystem directly.
//
// v0.9 / schema v2 transition note. This file is the Phase 0 additive
// rewrite: every field that existed under v0.8 is retained at its original
// name/type so plugin and importer code keeps compiling. New v2 fields are
// added alongside under non-conflicting names (e.g. EventCanonical next to
// Event, MatcherV2 next to Matcher). The parser populates both shapes so
// that v0.8-shape inputs still produce identical projections during
// Phase 0; subsequent phases will retire the v0.8 names.
package model

// Priority controls how strongly a scope should be surfaced in projections
// that lack native priority semantics.
type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// ScopeActivation is the v2 enum for scope activation modes (SPEC §4.7.2).
type ScopeActivation string

const (
	// ScopeActivationAlways injects the document on every turn.
	ScopeActivationAlways ScopeActivation = "always"
	// ScopeActivationCascade is the default for Path-set scopes.
	ScopeActivationCascade ScopeActivation = "cascade"
	// ScopeActivationGlob activates when files matching Globs are in context.
	ScopeActivationGlob ScopeActivation = "glob"
	// ScopeActivationManual requires explicit `@name` invocation.
	ScopeActivationManual ScopeActivation = "manual"
	// ScopeActivationModelDecision lets the LLM decide based on Description.
	ScopeActivationModelDecision ScopeActivation = "model_decision"
)

// SkillActivationMode is one of the activation triggers a skill may carry
// (SPEC §4.2.4). Skills can combine modes.
type SkillActivationMode string

const (
	// SkillActivationAlways injects the body every turn.
	SkillActivationAlways SkillActivationMode = "always"
	// SkillActivationModelDecision lets the LLM choose based on description.
	SkillActivationModelDecision SkillActivationMode = "model_decision"
	// SkillActivationGlob activates when matching files are in context.
	SkillActivationGlob SkillActivationMode = "glob"
	// SkillActivationManual requires explicit /name invocation.
	SkillActivationManual SkillActivationMode = "manual"
)

// SkillActivation is the polymorphic activation block for a Skill.
type SkillActivation struct {
	Modes          []SkillActivationMode
	Globs          []string
	ContentRegex   string
	UserInvocable  *bool
	ModelInvocable *bool
}

// SkillArgument names one positional argument for {{arg:name}} substitution.
type SkillArgument struct {
	Name        string
	Description string
	Required    bool
}

// MCPServerRef refers to an MCP server, either by name or inline.
type MCPServerRef struct {
	// Name resolves against Project.MCP. Mutually exclusive with Inline.
	Name string
	// Inline defines an MCP server entirely on the referring primitive.
	// Mutually exclusive with Name.
	Inline *MCPServer
}

// HookEvent is the canonical event name (SPEC §4.4.2). String-typed so
// "native:<verbatim>" forms can pass through.
type HookEvent string

const (
	EventSessionStart       HookEvent = "session_start"
	EventSessionEnd         HookEvent = "session_end"
	EventSessionResume      HookEvent = "session_resume"
	EventUserPromptSubmit   HookEvent = "user_prompt_submit"
	EventPreToolUse         HookEvent = "pre_tool_use"
	EventPostToolUse        HookEvent = "post_tool_use"
	EventPostToolUseFailure HookEvent = "post_tool_use_failure"
	EventPermissionRequest  HookEvent = "permission_request"
	EventPreShell           HookEvent = "pre_shell"
	EventPostShell          HookEvent = "post_shell"
	EventPreFileRead        HookEvent = "pre_file_read"
	EventPostFileEdit       HookEvent = "post_file_edit"
	EventPreMCPCall         HookEvent = "pre_mcp_call"
	EventPostMCPCall        HookEvent = "post_mcp_call"
	EventSubagentStart      HookEvent = "subagent_start"
	EventSubagentStop       HookEvent = "subagent_stop"
	EventStop               HookEvent = "stop"
	EventPreCompact         HookEvent = "pre_compact"
	EventPostCompact        HookEvent = "post_compact"
	EventNotification       HookEvent = "notification"
	EventWorktreeCreate     HookEvent = "worktree_create"
	EventWorktreeRemove     HookEvent = "worktree_remove"
	EventTaskCompleted      HookEvent = "task_completed"
	EventConfigChange       HookEvent = "config_change"
	EventError              HookEvent = "error"
)

// HookMatcher selects which event payloads fire the handler.
type HookMatcher struct {
	// Kind is "all", "exact", or "regex".
	Kind string
	// Patterns are exact strings or a single regex. Empty Patterns with
	// Kind == "all" matches everything.
	Patterns []string
}

// HookHandlerKind selects the shape of one handler.
type HookHandlerKind string

const (
	HookHandlerCommand HookHandlerKind = "command"
	HookHandlerHTTP    HookHandlerKind = "http"
	HookHandlerMCPTool HookHandlerKind = "mcp_tool"
	HookHandlerPrompt  HookHandlerKind = "prompt"
	HookHandlerAgent   HookHandlerKind = "agent"
)

// HookHandler is one fired callback in a Hook's handler list.
type HookHandler struct {
	Kind          HookHandlerKind
	TimeoutMs     int
	StatusMessage string
	Async         bool
	FailClosed    bool
	Once          bool
	If            string

	// Command — set when Kind == HookHandlerCommand.
	Command    string
	Args       []string
	Cwd        string
	Env        map[string]string
	Shell      string // "bash", "powershell", or empty
	Bash       string // platform-override script path
	Powershell string // platform-override script path

	// HTTP — set when Kind == HookHandlerHTTP.
	URL            string
	Headers        map[string]string
	AllowedEnvVars []string

	// MCPTool — set when Kind == HookHandlerMCPTool.
	MCPServer string
	MCPName   string
	MCPInput  map[string]any

	// Prompt / Agent — set when Kind == HookHandlerPrompt or HookHandlerAgent.
	Prompt string
	Model  string
}

// MCPAuth carries static auth declarations for an MCP server.
type MCPAuth struct {
	// Scheme is "none", "bearer", "header", or "oauth".
	Scheme string
	// Token holds the bearer token (typically a "${env:VAR}" reference).
	Token string
	// Headers are arbitrary headers merged into MCPServer.Headers.
	Headers map[string]string
}

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
	// FrontmatterLineOffset is the 1-based line on which the document body
	// begins (i.e. the line immediately after the closing `---` fence). The
	// validator uses this offset so error messages can point at the user's
	// source file rather than the trimmed body. Zero when there is no
	// frontmatter.
	FrontmatterLineOffset int
}

// NeedsWrite reports whether plugins must use ModeWrite rather than
// ModeSymlink for this document. True when @include expansion produced
// content that differs from the on-disk source.
func (d *Document) NeedsWrite() bool {
	return d != nil && len(d.Includes) > 0
}

// Scope is a path- or trigger-scoped context document.
//
// v0.8 fields: Path, Globs, Description, Priority, Document.
// v2 fields (additive): Name, Activation, Tags, IsOverride, Extensions.
type Scope struct {
	Path        string
	Globs       []string
	Description string
	Priority    Priority
	Document    *Document

	// v2 additions (SPEC §4.7.2).
	Name       string
	Activation ScopeActivation
	Tags       []string
	IsOverride bool
	Extensions map[string]any
}

// Skill is a triggered, optionally-scripted capability.
//
// ScopePath is the .agents/-relative directory the skill lives under, e.g.
// "src/billing". Empty = global (the skill lives at the .agents/ root).
// When non-empty, the skill inherits the scope's globs as a default if its
// own Globs slice is empty.
//
// v0.8 fields: Name, Description, Trigger, Globs, Document, Scripts,
// ScopePath. v2 fields (additive): Activation, WhenToUse, AllowedTools,
// Arguments, References, Model, Subagent, Extensions. The parser populates
// both the top-level Trigger/Globs and Activation.{Globs,Modes} so the
// v0.8 access paths and v2 access paths both observe the same data.
type Skill struct {
	Name        string
	Description string
	Trigger     string
	Globs       []string
	Document    *Document
	Scripts     []string
	ScopePath   string

	// v2 additions (SPEC §4.2.2).
	Activation   SkillActivation
	WhenToUse    string
	AllowedTools []string
	Arguments    []SkillArgument
	References   []string
	Model        string
	Subagent     string
	Extensions   map[string]any
}

// Command is a reusable prompt template.
//
// v0.8 fields: Name, Description, Document, ScopePath. v2 fields (additive):
// ArgumentHint, Arguments, Model, Tools, Agent, AutoInvoke, Extensions.
type Command struct {
	Name        string
	Description string
	Document    *Document
	ScopePath   string

	// v2 additions (SPEC §4.3.2).
	ArgumentHint string
	Arguments    []string
	Model        string
	Tools        []string
	Agent        string
	AutoInvoke   bool
	Extensions   map[string]any
}

// Agent is a specialized persona / subagent definition.
//
// v0.8 fields: Name, Description, Document, ScopePath. v2 fields (additive):
// SystemPrompt, Model, ModelFallbacks, Tools, DisallowedTools, ReadOnly,
// Background, MaxTurns, Temperature, MCPServers, AllowedSubagents,
// UserInvocable, ModelInvocable, InitialPrompt, Extensions.
type Agent struct {
	Name        string
	Description string
	Document    *Document
	ScopePath   string

	// v2 additions (SPEC §4.1.2).
	SystemPrompt     string
	Model            string
	ModelFallbacks   []string
	Tools            []string
	DisallowedTools  []string
	ReadOnly         *bool
	Background       *bool
	MaxTurns         *int
	Temperature      *float64
	MCPServers       []MCPServerRef
	AllowedSubagents []string
	UserInvocable    *bool
	ModelInvocable   *bool
	InitialPrompt    string
	Extensions       map[string]any
}

// Hook is a behavioral hook.
//
// v0.8 fields: Event (string), Matcher (string), ScriptPath, ScopePath.
// v2 fields are added under distinct names so v0.8 plugin code keeps
// compiling: Name, Description, EventCanonical (typed enum), MatcherV2
// (struct), Handlers, Sequential, Disabled, Extensions. The parser
// populates both shapes for every hook source it parses.
type Hook struct {
	Event      string
	Matcher    string
	ScriptPath string
	ScopePath  string

	// v2 additions (SPEC §4.4.2). EventCanonical mirrors Event as a typed
	// enum; MatcherV2 mirrors Matcher as the {Kind, Patterns} struct.
	Name           string
	Description    string
	EventCanonical HookEvent
	MatcherV2      HookMatcher
	Handlers       []HookHandler
	Sequential     *bool
	Disabled       bool
	Extensions     map[string]any
}

// Permissions is the canonical allow/deny configuration.
// ScopePath is empty for the global permissions block on Project.Permissions
// and non-empty for entries under Project.ScopedPermissions.
type Permissions struct {
	Allow     []string
	Deny      []string
	Ask       []string
	ScopePath string

	// v2 additions (SPEC §4.6.2).
	Extensions map[string]any
}

// MCPServer is a single MCP server configuration.
//
// v0.8 fields: Name, Command, Args, Env, URL, ScopePath. v2 fields are
// additive per SPEC §4.5.2.
type MCPServer struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	ScopePath string

	// v2 additions.
	Transport    string
	Cwd          string
	Headers      map[string]string
	Auth         *MCPAuth
	TimeoutMs    int
	Disabled     bool
	AutoApprove  []string
	Trust        bool
	IncludeTools []string
	ExcludeTools []string
	Extensions   map[string]any
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
//
// v2 additions: SchemaVersion is the integer from the top of
// agents.config.yaml; Extensions is the project-wide pass-through; Layers
// is the resolved `include:` list (the canonical schema renames it from
// "include" to express the layered-config intent; the parser fills Layers
// from the `include:` key).
type Config struct {
	Targets       []string
	TargetOptions map[string]TargetOption
	// Include controls the @include preprocessor behavior.
	Include IncludeConfig

	// v2 additions (SPEC §3.1.1).
	SchemaVersion int
	Extensions    map[string]any
	Layers        []string
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

	// v2 additions (SPEC §3.1.1).
	Extensions map[string]any
}
