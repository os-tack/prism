package plugins

import "agents.dev/agents/internal/model"

// hook_envelope.go is the single source of truth for translating canonical
// HookEvent constants (SPEC §4.4.2) into per-target wire forms. Each plugin
// calls MapHookEventFor with its own name and the canonical event; the
// returned mapping carries the target's native event name plus a matcher
// pattern that the plugin SHOULD apply when the canonical event was
// per-action (e.g. pre_shell → PreToolUse + matcher "Bash" on Claude).
//
// When ok == false the plugin has no native expression of the event and
// MUST emit one info or warn level Warning per touched canonical source.
// Per-tool wiring lives in the plugin; this file only owns the table.
//
// The mapping is derived directly from SPEC §4.4.4 ("Per-action canonical
// event translation"). New tools or new canonical events extend this map.

// HookEventMapping is the translated event name + optional matcher hint.
type HookEventMapping struct {
	// Event is the target's native event name (e.g. "PreToolUse",
	// "beforeShellExecution", "pre_run_command").
	Event string
	// Matcher is a matcher pattern to apply when the canonical event was
	// per-action and the target lacks the specific event. Empty when the
	// target expresses the event natively (no matcher needed) or when the
	// canonical event is itself generic (PreToolUse).
	Matcher string
}

// MapHookEventFor returns the target plugin's wire-form event + optional
// matcher for a canonical event. ok==false when the target has no native
// expression and the plugin must drop with a warning.
func MapHookEventFor(plugin string, ev model.HookEvent) (HookEventMapping, bool) {
	t, has := hookEventTable[ev]
	if !has {
		return HookEventMapping{}, false
	}
	m, ok := t[plugin]
	return m, ok
}

// hookEventTable is keyed by canonical event then by plugin name. Per-plugin
// absence means "unsupported on that target" — callers translate that into
// the per-field-capability warning.
var hookEventTable = map[model.HookEvent]map[string]HookEventMapping{
	model.EventSessionStart: claudeContinueMap("SessionStart", "", map[string]HookEventMapping{
		"cursor":   {Event: "sessionStart"},
		"gemini":   {Event: "SessionStart"},
		"copilot":  {Event: "SessionStart"},
		"cline":    {Event: "SessionStart"},
		"windsurf": {Event: "session_start"},
	}),
	model.EventSessionEnd: claudeContinueMap("SessionEnd", "", map[string]HookEventMapping{
		"cursor":   {Event: "sessionEnd"},
		"gemini":   {Event: "SessionEnd"},
		"copilot":  {Event: "SessionEnd"},
		"cline":    {Event: "SessionEnd"},
		"windsurf": {Event: "session_end"},
	}),
	model.EventUserPromptSubmit: claudeContinueMap("UserPromptSubmit", "", map[string]HookEventMapping{
		"cursor":   {Event: "userPromptSubmit"},
		"gemini":   {Event: "UserPromptSubmit"},
		"copilot":  {Event: "UserPromptSubmit"},
		"cline":    {Event: "UserPromptSubmit"},
		"windsurf": {Event: "user_prompt_submit"},
	}),
	model.EventPreToolUse: claudeContinueMap("PreToolUse", "", map[string]HookEventMapping{
		"cursor":   {Event: "beforeToolUse"},
		"gemini":   {Event: "BeforeTool"},
		"copilot":  {Event: "PreToolUse"},
		"cline":    {Event: "PreToolUse"},
		"windsurf": {Event: "pre_tool_use"},
	}),
	model.EventPostToolUse: claudeContinueMap("PostToolUse", "", map[string]HookEventMapping{
		"cursor":   {Event: "afterToolUse"},
		"gemini":   {Event: "AfterTool"},
		"copilot":  {Event: "PostToolUse"},
		"cline":    {Event: "PostToolUse"},
		"windsurf": {Event: "post_tool_use"},
	}),
	model.EventPreShell: {
		"claude":   {Event: "PreToolUse", Matcher: "Bash"},
		"continue": {Event: "PreToolUse", Matcher: "Bash"},
		"cursor":   {Event: "beforeShellExecution"},
		"gemini":   {Event: "BeforeTool", Matcher: "run_shell_command"},
		"copilot":  {Event: "PreToolUse", Matcher: "Bash"},
		"cline":    {Event: "PreToolUse", Matcher: "execute_command"},
		"windsurf": {Event: "pre_run_command"},
	},
	model.EventPostShell: {
		"claude":   {Event: "PostToolUse", Matcher: "Bash"},
		"continue": {Event: "PostToolUse", Matcher: "Bash"},
		"cursor":   {Event: "afterShellExecution"},
		"gemini":   {Event: "AfterTool", Matcher: "run_shell_command"},
		"copilot":  {Event: "PostToolUse", Matcher: "Bash"},
		"cline":    {Event: "PostToolUse", Matcher: "execute_command"},
		"windsurf": {Event: "post_run_command"},
	},
	model.EventPreFileRead: {
		"claude":   {Event: "PreToolUse", Matcher: "Read"},
		"continue": {Event: "PreToolUse", Matcher: "Read"},
		"cursor":   {Event: "beforeReadFile"},
		"gemini":   {Event: "BeforeTool", Matcher: "read_file"},
		"copilot":  {Event: "PreToolUse", Matcher: "Read"},
		"cline":    {Event: "PreToolUse", Matcher: "read_file"},
		"windsurf": {Event: "pre_read_code"},
	},
	model.EventPostFileEdit: {
		"claude":   {Event: "PostToolUse", Matcher: "Edit|Write|MultiEdit"},
		"continue": {Event: "PostToolUse", Matcher: "Edit|Write|MultiEdit"},
		"cursor":   {Event: "afterFileEdit"},
		"gemini":   {Event: "AfterTool", Matcher: "write_file"},
		"copilot":  {Event: "PostToolUse", Matcher: "Edit|Write"},
		"cline":    {Event: "PostToolUse", Matcher: "edit"},
		"windsurf": {Event: "post_write_code"},
	},
	model.EventPreMCPCall: {
		"claude":   {Event: "PreToolUse", Matcher: "mcp__*"},
		"continue": {Event: "PreToolUse", Matcher: "mcp__*"},
		"cursor":   {Event: "beforeMCPExecution"},
		"gemini":   {Event: "BeforeTool", Matcher: "mcp_*"},
		"copilot":  {Event: "PreToolUse", Matcher: "mcp__*"},
		"cline":    {Event: "PreToolUse", Matcher: "mcp_*"},
		"windsurf": {Event: "pre_mcp_tool_use"},
	},
	model.EventPostMCPCall: {
		"claude":   {Event: "PostToolUse", Matcher: "mcp__*"},
		"continue": {Event: "PostToolUse", Matcher: "mcp__*"},
		"cursor":   {Event: "afterMCPExecution"},
		"gemini":   {Event: "AfterTool", Matcher: "mcp_*"},
		"copilot":  {Event: "PostToolUse", Matcher: "mcp__*"},
		"cline":    {Event: "PostToolUse", Matcher: "mcp_*"},
		"windsurf": {Event: "post_mcp_tool_use"},
	},
	model.EventSubagentStart: claudeContinueMap("SubagentStart", "", map[string]HookEventMapping{
		"copilot": {Event: "SubagentStart"},
	}),
	model.EventSubagentStop: claudeContinueMap("SubagentStop", "", map[string]HookEventMapping{
		"copilot": {Event: "SubagentStop"},
	}),
	model.EventStop: claudeContinueMap("Stop", "", map[string]HookEventMapping{
		"copilot": {Event: "Stop"},
	}),
	model.EventPreCompact:         claudeContinueMap("PreCompact", "", map[string]HookEventMapping{}),
	model.EventPostCompact:        claudeContinueMap("PostCompact", "", map[string]HookEventMapping{}),
	model.EventNotification:       claudeContinueMap("Notification", "", map[string]HookEventMapping{}),
	model.EventSessionResume:      claudeContinueMap("SessionResume", "", map[string]HookEventMapping{}),
	model.EventPostToolUseFailure: claudeContinueMap("PostToolUseFailure", "", map[string]HookEventMapping{}),
	model.EventPermissionRequest:  claudeContinueMap("PermissionRequest", "", map[string]HookEventMapping{}),
	model.EventWorktreeCreate:     claudeContinueMap("WorktreeCreate", "", map[string]HookEventMapping{}),
	model.EventWorktreeRemove:     claudeContinueMap("WorktreeRemove", "", map[string]HookEventMapping{}),
	model.EventTaskCompleted:      claudeContinueMap("TaskCompleted", "", map[string]HookEventMapping{}),
	model.EventConfigChange:       claudeContinueMap("ConfigChange", "", map[string]HookEventMapping{}),
	model.EventError:              claudeContinueMap("Error", "", map[string]HookEventMapping{}),
}

// claudeContinueMap is a small helper that seeds the Claude + Continue
// columns of the table with the same wire form (they share a hook schema
// per SPEC §4.4) and merges in the per-plugin overrides for the other
// targets. Keeps the table readable.
func claudeContinueMap(event, matcher string, rest map[string]HookEventMapping) map[string]HookEventMapping {
	out := map[string]HookEventMapping{
		"claude":   {Event: event, Matcher: matcher},
		"continue": {Event: event, Matcher: matcher},
	}
	for k, v := range rest {
		out[k] = v
	}
	return out
}
