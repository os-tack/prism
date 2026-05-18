package plugins

import "strings"

// perms_grammar.go is the shared parser for canonical Permission rules
// (SPEC §4.6.2). Each plugin's permission emitter consults this helper to
// classify a rule before deciding how to project it.
//
// Runtime matching ("does this rule match this in-flight tool call?")
// lives in internal/perms/perms.go — single source of truth for the
// perms-guard wrapper family. This file owns the EMIT-side parsing: given
// a rule string, return the typed shape so a plugin can fan out fs: →
// Edit/Read/Write, render network: → WebFetch(domain:…), etc.
//
// Phase 1 scaffolding only. Phase 2 plugins migrate from ad-hoc string
// splitting to ParsePermRule().

// PermTarget identifies the canonical target a rule applies to.
type PermTarget string

const (
	PermTargetBash      PermTarget = "bash"
	PermTargetRead      PermTarget = "Read"
	PermTargetWrite     PermTarget = "Write"
	PermTargetEdit      PermTarget = "Edit"
	PermTargetMultiEdit PermTarget = "MultiEdit"
	PermTargetFS        PermTarget = "fs"      // Read|Write|Edit|MultiEdit synonym
	PermTargetNetwork   PermTarget = "network" // WebFetch / Fetch domain
	PermTargetMCP       PermTarget = "mcp"     // mcp:<server>[:<tool>]
	PermTargetWebFetch  PermTarget = "WebFetch"
	PermTargetToolOnly  PermTarget = ""        // bare tool name with no pattern ("Bash" alone)
	PermTargetUnknown   PermTarget = "unknown"
)

// PermRule is the parsed shape of one canonical rule. Negated reduces to a
// deny entry on emit (per SPEC §4.6.2). MCPServer/MCPTool are set when
// Target == PermTargetMCP.
type PermRule struct {
	Raw       string
	Target    PermTarget
	Pattern   string
	Negated   bool   // "!" prefix
	MCPServer string // "github" in mcp:github:create_issue
	MCPTool   string // "create_issue" in mcp:github:create_issue; "" or "*" for any-tool
}

// ParsePermRule classifies a canonical permission rule string. Unknown
// targets return Target=PermTargetUnknown so the caller can warn and
// best-effort emit.
func ParsePermRule(rule string) PermRule {
	out := PermRule{Raw: rule}
	rest := rule
	if strings.HasPrefix(rest, "!") {
		out.Negated = true
		rest = rest[1:]
	}
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		out.Target = PermTarget(rest)
		out.Pattern = ""
		switch out.Target {
		case PermTargetBash, PermTargetRead, PermTargetWrite, PermTargetEdit, PermTargetMultiEdit, PermTargetWebFetch:
			return out
		}
		out.Target = PermTargetUnknown
		return out
	}
	target := rest[:colon]
	pattern := rest[colon+1:]
	switch target {
	case "bash", "Bash":
		out.Target = PermTargetBash
		out.Pattern = pattern
	case "Read":
		out.Target = PermTargetRead
		out.Pattern = pattern
	case "Write":
		out.Target = PermTargetWrite
		out.Pattern = pattern
	case "Edit":
		out.Target = PermTargetEdit
		out.Pattern = pattern
	case "MultiEdit":
		out.Target = PermTargetMultiEdit
		out.Pattern = pattern
	case "fs":
		out.Target = PermTargetFS
		out.Pattern = pattern
	case "network":
		out.Target = PermTargetNetwork
		out.Pattern = pattern
	case "mcp":
		out.Target = PermTargetMCP
		if sub := strings.IndexByte(pattern, ':'); sub >= 0 {
			out.MCPServer = pattern[:sub]
			out.MCPTool = pattern[sub+1:]
		} else {
			out.MCPServer = pattern
		}
		out.Pattern = pattern
	case "WebFetch":
		out.Target = PermTargetWebFetch
		out.Pattern = pattern
	default:
		out.Target = PermTargetUnknown
		out.Pattern = pattern
	}
	return out
}

// FSFanOut returns the three concrete file-system tool targets the `fs:`
// synonym expands to on plugins (like Claude) that lack a native fs:-typed
// rule. Each returned entry shares the original pattern.
func (r PermRule) FSFanOut() []PermRule {
	if r.Target != PermTargetFS {
		return []PermRule{r}
	}
	return []PermRule{
		{Raw: r.Raw, Target: PermTargetEdit, Pattern: r.Pattern, Negated: r.Negated},
		{Raw: r.Raw, Target: PermTargetRead, Pattern: r.Pattern, Negated: r.Negated},
		{Raw: r.Raw, Target: PermTargetWrite, Pattern: r.Pattern, Negated: r.Negated},
	}
}

// ClaudeRuleForm renders a parsed rule into Claude's wire form
// (e.g. "Bash(go test *)", "Edit(src/**)", "WebFetch(domain:github.com)",
// "mcp__github__create_issue"). Negated rules drop the negation — the
// caller is expected to route them to the deny bucket.
func (r PermRule) ClaudeRuleForm() string {
	switch r.Target {
	case PermTargetBash:
		return "Bash(" + r.Pattern + ")"
	case PermTargetRead:
		return "Read(" + r.Pattern + ")"
	case PermTargetWrite:
		return "Write(" + r.Pattern + ")"
	case PermTargetEdit:
		return "Edit(" + r.Pattern + ")"
	case PermTargetMultiEdit:
		return "MultiEdit(" + r.Pattern + ")"
	case PermTargetNetwork:
		return "WebFetch(domain:" + r.Pattern + ")"
	case PermTargetWebFetch:
		return "WebFetch(" + r.Pattern + ")"
	case PermTargetMCP:
		if r.MCPTool == "" || r.MCPTool == "*" {
			return "mcp__" + r.MCPServer + "__*"
		}
		return "mcp__" + r.MCPServer + "__" + r.MCPTool
	case PermTargetToolOnly, PermTargetUnknown:
		return r.Raw
	}
	return r.Raw
}
