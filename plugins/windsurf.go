// WindsurfPlugin projects a canonical .agents/ directory into Windsurf's
// `.windsurf/rules/*.md` rule files. Each rule file uses YAML frontmatter
// followed by a markdown body. Windsurf's frontmatter has a `trigger` field
// controlling when the rule fires:
//
//   - always_on:       rule is always included
//   - glob:            rule attaches when files matching `globs` are in context
//   - model_decision:  rule attaches when the model thinks `description` is relevant
//   - manual:          rule only fires when explicitly invoked
//
// In addition to rules, the plugin now emits Windsurf's two file-based
// extension surfaces:
//
//   - `.windsurf/hooks.json` — Cascade Hooks (12 event types, JSON-stdin
//     contract similar to Claude Code's hook protocol). Pre-hooks can block
//     the action by exiting with code 2.
//
//   - `.windsurf/mcp_config.json` — MCP server registry, standard
//     {mcpServers: {...}} schema. Windsurf canonically expects this file
//     at the user-level path `~/.codeium/windsurf/mcp_config.json`; the
//     project-local copy is emitted for portability with an info warning
//     that the user must symlink / shell-init the file into the canonical
//     location for Windsurf to pick it up.
//
// Skills and Commands degrade to rules (with a description so the model
// can decide when to surface them); Agents and Permissions are still
// unsupported (Windsurf has no equivalent primitives). Scoped skills are
// projected natively via trigger: glob whenever Skill.Globs is populated
// (which the parser handles from frontmatter override or scope default);
// scoped hooks reuse a __scope-guard__ wrapper script identical in shape
// to the one Claude uses (Windsurf's hook JSON-stdin contract is
// compatible enough for the wrapper to gate on tool_input.file_path).

package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// WindsurfPlugin projects Project state into `.windsurf/rules/*.md`,
// `.windsurf/hooks.json`, and `.windsurf/mcp_config.json`.
type WindsurfPlugin struct {
	// DisableHookWrappers, when true, projects scoped hooks as if they
	// were global (no __scope-guard__ wrapper). Mirrors the equivalent
	// knob on ClaudePlugin. Default false (wrappers ON).
	DisableHookWrappers bool
}

// NewWindsurf constructs a WindsurfPlugin.
func NewWindsurf() *WindsurfPlugin { return &WindsurfPlugin{} }

// Name returns the stable plugin identifier.
func (p *WindsurfPlugin) Name() string { return "windsurf" }

// Detect returns true if the project at root looks like it uses Windsurf.
// Presence of either the modern `.windsurf/` directory or the legacy flat
// `.windsurfrules` file at the project root counts as an activation signal.
func (p *WindsurfPlugin) Detect(root string) bool {
	if info, err := os.Stat(filepath.Join(root, ".windsurf")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, ".windsurfrules")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// Capabilities returns Windsurf's capability matrix.
//
// Windsurf natively supports always-on context (Context), per-glob rule
// attachment (ScopePaths via trigger: glob), and description-triggered
// attachment (ScopeSemantic via trigger: model_decision). Skills and
// Commands degrade (no script execution, no slash-command mechanism).
// Hooks are natively supported via Cascade Hooks (.windsurf/hooks.json)
// and MCP via .windsurf/mcp_config.json. Agents and Permissions remain
// unsupported.
func (p *WindsurfPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportDegraded,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportNative,
	}
}

// Plan produces the Operations needed to project proj into `.windsurf/`.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Unknown modes return an error.
func (p *WindsurfPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	switch opts.Mode {
	case "", "write":
		// ok
	default:
		return nil, fmt.Errorf("windsurf: unsupported mode %q", opts.Mode)
	}

	var ops []plugin.Operation

	// Root context → .windsurf/rules/_root.md with trigger: always_on.
	if proj.Context != nil {
		content := renderWindsurfRule(windsurfFrontmatter{
			Trigger: "always_on",
		}, proj.Context.Body)
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".windsurf/rules/_root.md",
			Content: content,
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		})
	}

	// Per-scope rule files.
	for _, sc := range proj.Scopes {
		if sc == nil {
			continue
		}
		body := ""
		var sources []string
		if sc.Document != nil {
			body = sc.Document.Body
			sources = []string{proj.SourceTag(sc.Document.SourcePath)}
		}
		fm := windsurfFrontmatter{
			Trigger:     "glob",
			Globs:       sc.Globs,
			Description: sc.Description,
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", slugify(sc.Path)+".md")),
			Content: renderWindsurfRule(fm, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
	}

	// Skills → degraded rule files. Globbed skills become trigger: glob,
	// otherwise trigger: model_decision (which requires a description).
	// Scoped skills include the scope slug as a filename prefix to avoid
	// collisions across scopes. Skill.Globs is already populated by the
	// parser (frontmatter override or scope default) so a scoped skill
	// naturally lands on trigger: glob.
	for _, skill := range proj.Skills {
		if skill == nil {
			continue
		}
		body := ""
		var sources []string
		if skill.Document != nil {
			body = skill.Document.Body
			sources = []string{proj.SourceTag(skill.Document.SourcePath)}
		}
		var fm windsurfFrontmatter
		if len(skill.Globs) > 0 {
			fm = windsurfFrontmatter{
				Trigger:     "glob",
				Globs:       skill.Globs,
				Description: skill.Description,
			}
		} else {
			desc := skill.Description
			if desc == "" {
				desc = skill.Trigger
			}
			fm = windsurfFrontmatter{
				Trigger:     "model_decision",
				Description: desc,
			}
		}
		fname := "skill-" + scopedSkillSlug(skill.ScopePath, skill.Name) + ".md"
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", fname)),
			Content: renderWindsurfRule(fm, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if len(skill.Scripts) > 0 {
			src := ""
			if skill.Document != nil {
				src = proj.SourceTag(skill.Document.SourcePath)
			}
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Windsurf has no script execution; scripts ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Commands → degraded rule files with trigger: model_decision and a
	// description prefixed with "Command /<name>: ". Each command also emits
	// an info warning on its own op.
	//
	// Scoped commands get the scope slug as a filename prefix, the scope path
	// surfaced in the description (so the model_decision trigger knows about
	// it), and a warning naming the scope.
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		body := ""
		var sources []string
		if cmd.Document != nil {
			body = cmd.Document.Body
			sources = []string{proj.SourceTag(cmd.Document.SourcePath)}
		}
		desc := fmt.Sprintf("Command /%s: %s", cmd.Name, cmd.Description)
		warningMsg := fmt.Sprintf("Windsurf has no slash-command mechanism; %s documented as a rule.", cmd.Name)
		if cmd.ScopePath != "" {
			desc = fmt.Sprintf("Command /%s (scoped to %s): %s", cmd.Name, cmd.ScopePath, cmd.Description)
			warningMsg = fmt.Sprintf("Windsurf has no slash-command mechanism; scoped command %q (scope: %s) documented as a rule.", cmd.Name, cmd.ScopePath)
		}
		fm := windsurfFrontmatter{
			Trigger:     "model_decision",
			Description: desc,
		}
		fname := "command-" + scopedSkillSlug(cmd.ScopePath, cmd.Name) + ".md"
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".windsurf", "rules", fname)),
			Content: renderWindsurfRule(fm, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   sourceFromCommand(proj, cmd),
			Message:  warningMsg,
			Severity: "info",
		})
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (emitted before the hooks.json op so the
	// hooks.json op can reference each wrapper's absolute path). Windsurf's
	// hook JSON-stdin contract is compatible enough with Claude's
	// scope-guard model that we reuse the same wrapper renderer: the
	// wrapper exec's `prism scope-guard --scope <path> --script <abs>`,
	// which parses stdin JSON and only invokes the source script when
	// tool_input.file_path falls under the scope. See the scope-guard
	// docstring in claude.go for full details on the runtime contract.
	wrapperPaths := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + hookName + ".sh"
		wrapperRel := filepath.Join(".windsurf", "hooks", "__scope-guard__", wrapperFile)
		wrapperAbs := filepath.Join(proj.Root, wrapperRel)

		body := buildScopeGuardScript(wrapperRel, h.ScopePath, h.ScriptPath, formatScriptArg(h.ScriptPath, proj.Root))
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     filepath.ToSlash(wrapperRel),
			Content:  body,
			Mode:     plugin.ModeWrite,
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   p.Name(),
		})
		wrapperPaths[h] = wrapperAbs
	}

	// .windsurf/hooks.json — Cascade Hooks. Each prism Hook.Event is
	// mapped to a Windsurf event via mapWindsurfEvent (which uses the
	// matcher for the Claude-style PreToolUse/PostToolUse cases).
	// Unmappable hooks attach an info warning instead of being emitted.
	var hooksWarnings []plugin.Warning
	if len(proj.Hooks) > 0 {
		hooksOp, hw, err := buildWindsurfHooksOp(proj, wrapperPaths)
		if err != nil {
			return nil, err
		}
		if hooksOp != nil {
			ops = append(ops, *hooksOp)
		}
		hooksWarnings = hw
	}

	// .windsurf/mcp_config.json — MCP servers. Project-local for
	// portability; canonical location is ~/.codeium/windsurf/mcp_config.json
	// so an info warning fires whenever any MCP server is projected.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildWindsurfMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	// Collect un-attached warnings for capability types we do not project.
	// These attach to the first emitted op below.
	var warnings []plugin.Warning
	warnings = append(warnings, hooksWarnings...)
	for _, agent := range proj.Agents {
		if agent == nil {
			continue
		}
		src := ""
		if agent.Document != nil {
			src = proj.SourceTag(agent.Document.SourcePath)
		}
		msg := fmt.Sprintf("Windsurf has no subagent primitive; %s not projected.", agent.Name)
		if agent.ScopePath != "" {
			msg = fmt.Sprintf("Windsurf has no subagent primitive; scoped agent %s (scope: %s) not projected.", agent.Name, agent.ScopePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   src,
			Message:  msg,
			Severity: "info",
		})
	}
	if proj.Permissions != nil {
		if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
			warnings = append(warnings, plugin.Warning{
				Source:   "",
				Message:  "Windsurf has no permissions primitive; permissions not projected.",
				Severity: "info",
			})
		}
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		if len(sp.Allow) == 0 && len(sp.Deny) == 0 && len(sp.Ask) == 0 {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   "",
			Message:  fmt.Sprintf("Windsurf has no permissions primitive; scoped permissions (scope: %s) not projected.", sp.ScopePath),
			Severity: "info",
		})
	}

	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// sourceFromCommand returns the .agents/-relative source path for a Command
// if one is available, else empty.
func sourceFromCommand(proj *model.Project, cmd *model.Command) string {
	if cmd == nil || cmd.Document == nil {
		return ""
	}
	return proj.SourceTag(cmd.Document.SourcePath)
}

// windsurfFrontmatter holds the fields renderWindsurfRule emits. Trigger is
// required for every rule; Globs is required when Trigger == "glob";
// Description is required when Trigger == "model_decision" and optional
// otherwise.
type windsurfFrontmatter struct {
	Trigger     string
	Globs       []string
	Description string
}

// renderWindsurfRule formats the YAML frontmatter + markdown body for a
// Windsurf rule file. globs are serialized as a JSON-flow-style array (valid
// YAML); json.Marshal handles escaping.
func renderWindsurfRule(fm windsurfFrontmatter, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if fm.Trigger != "" {
		b.WriteString("trigger: ")
		b.WriteString(fm.Trigger)
		b.WriteString("\n")
	}
	if len(fm.Globs) > 0 {
		raw, err := json.Marshal(fm.Globs)
		if err != nil {
			raw = []byte("[]")
		}
		b.WriteString("globs: ")
		b.Write(raw)
		b.WriteString("\n")
	}
	if fm.Description != "" {
		raw, err := json.Marshal(fm.Description)
		if err != nil {
			raw = []byte("\"\"")
		}
		b.WriteString("description: ")
		b.Write(raw)
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// windsurfHookEntry mirrors Windsurf's Cascade Hooks JSON schema for a
// single hook command:
//
//	{"command": "<shell command>", "show_output": false}
//
// We do not emit `powershell` (Windows-specific variant) — the source
// script is bash so Windsurf on Windows would not run it anyway; users
// targeting Windows should declare a powershell variant in their .agents/
// hooks and wire it through a separate projection pass (out of scope for
// v0.8).
type windsurfHookEntry struct {
	Command    string `json:"command"`
	ShowOutput bool   `json:"show_output,omitempty"`
}

// windsurfHookEvents is the canonical set of Cascade hook event names. The
// authoritative list (per docs.windsurf.com/windsurf/cascade/hooks) is 12
// events: 5 pre-hooks that can block (pre_user_prompt, pre_read_code,
// pre_write_code, pre_run_command, pre_mcp_tool_use), 4 post-hooks
// (post_user_prompt, post_read_code, post_write_code, post_run_command),
// and 3 lifecycle hooks (post_cascade_response,
// post_cascade_response_with_transcript, post_setup_worktree).
//
// Note: the live docs at the time of this writing also enumerate
// post_mcp_tool_use, which the v0.8 spec did not include. The mapping
// table below accepts that event by name pass-through but does not
// auto-derive it from Claude's PostToolUse + mcp matcher unless the
// matcher unambiguously names mcp.
var windsurfHookEvents = map[string]struct{}{
	"pre_user_prompt":                       {},
	"pre_read_code":                         {},
	"pre_write_code":                        {},
	"pre_run_command":                       {},
	"pre_mcp_tool_use":                      {},
	"post_user_prompt":                      {},
	"post_read_code":                        {},
	"post_write_code":                       {},
	"post_run_command":                      {},
	"post_mcp_tool_use":                     {},
	"post_cascade_response":                 {},
	"post_cascade_response_with_transcript": {},
	"post_setup_worktree":                   {},
}

// mapWindsurfEvent translates a prism Hook event+matcher into a Windsurf
// Cascade hook event name. The translation rules:
//
//  1. If event matches a Windsurf event name exactly (case-insensitive),
//     pass through unchanged. This is the canonical path for hooks
//     authored with Windsurf as the target.
//
//  2. Claude-style PreToolUse / PostToolUse routes by matcher:
//     - Bash, Shell, *Bash*       → pre_run_command / post_run_command
//     - Read, Glob, Grep, *Read*  → pre_read_code   / post_read_code
//     - Write, Edit, MultiEdit    → pre_write_code  / post_write_code
//     - mcp__* / *MCP*            → pre_mcp_tool_use / post_mcp_tool_use
//     - "" or unknown             → pre_run_command / post_run_command
//
//  3. Claude UserPromptSubmit (and aliases) → pre_user_prompt.
//
//  4. SessionStart / SessionEnd / SubagentStop / Stop / Notification /
//     PreCompact → no Windsurf equivalent, drop with warning.
//
// Returns ("", false) when no mapping exists; callers emit a warning.
func mapWindsurfEvent(event, matcher string) (string, bool) {
	ev := strings.ToLower(strings.TrimSpace(event))
	if ev == "" {
		return "", false
	}
	if _, ok := windsurfHookEvents[ev]; ok {
		return ev, true
	}

	m := strings.ToLower(strings.TrimSpace(matcher))
	switch ev {
	case "pretooluse":
		return classifyToolMatcher(m, "pre"), true
	case "posttooluse":
		return classifyToolMatcher(m, "post"), true
	case "userpromptsubmit", "userprompt", "pre_user_prompt_submit":
		return "pre_user_prompt", true
	case "stop", "subagentstop", "sessionstart", "sessionend", "notification", "precompact":
		return "", false
	}
	return "", false
}

// classifyToolMatcher routes a (lowercased) tool matcher onto one of
// run_command / read_code / write_code / mcp_tool_use, prefixed with the
// given phase ("pre" or "post"). The mapping favors substring matches
// so "Bash(*)" and "BashOutput" both land on run_command.
func classifyToolMatcher(matcher, phase string) string {
	switch {
	case strings.Contains(matcher, "mcp"):
		return phase + "_mcp_tool_use"
	case strings.Contains(matcher, "write") || strings.Contains(matcher, "edit") || strings.Contains(matcher, "multiedit"):
		return phase + "_write_code"
	case strings.Contains(matcher, "read") || strings.Contains(matcher, "glob") || strings.Contains(matcher, "grep"):
		return phase + "_read_code"
	case strings.Contains(matcher, "bash") || strings.Contains(matcher, "shell") || strings.Contains(matcher, "run"):
		return phase + "_run_command"
	}
	return phase + "_run_command"
}

// buildWindsurfHooksOp emits the OpMerge for `.windsurf/hooks.json`. The
// Merger closure preserves any user-authored top-level keys (anything
// other than "hooks") on the next prism run, mirroring
// claude.go:buildSettingsOp.
//
// Returns (op, warnings, err). Warnings are returned separately so the
// caller can fold them onto the first op (which may not be this one if
// every input hook drops with a warning).
func buildWindsurfHooksOp(proj *model.Project, wrapperPaths map[*model.Hook]string) (*plugin.Operation, []plugin.Warning, error) {
	buckets := map[string][]windsurfHookEntry{}
	var eventOrder []string
	var warnings []plugin.Warning
	var sources []string
	srcSeen := map[string]struct{}{}
	addSource := func(s string) {
		if s == "" {
			return
		}
		if _, ok := srcSeen[s]; ok {
			return
		}
		srcSeen[s] = struct{}{}
		sources = append(sources, s)
	}

	for _, h := range proj.Hooks {
		if h == nil || h.Event == "" || h.ScriptPath == "" {
			continue
		}
		wsEvent, ok := mapWindsurfEvent(h.Event, h.Matcher)
		if !ok {
			msg := fmt.Sprintf("Windsurf Cascade Hooks has no event matching %q (matcher=%q); hook not projected.", h.Event, h.Matcher)
			if h.ScopePath != "" {
				msg = fmt.Sprintf("Windsurf Cascade Hooks has no event matching %q (matcher=%q, scope: %s); hook not projected.", h.Event, h.Matcher, h.ScopePath)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   h.ScriptPath,
				Message:  msg,
				Severity: "info",
			})
			continue
		}

		cmdPath := h.ScriptPath
		if w, ok := wrapperPaths[h]; ok {
			cmdPath = w
		}
		// Wrap the script path in `bash` so users authoring shell hooks
		// don't need to mark them executable. shellQuote keeps paths
		// with spaces / quotes safe.
		entry := windsurfHookEntry{
			Command:    "bash " + shellQuote(cmdPath),
			ShowOutput: false,
		}
		if _, seen := buckets[wsEvent]; !seen {
			eventOrder = append(eventOrder, wsEvent)
		}
		buckets[wsEvent] = append(buckets[wsEvent], entry)
		addSource(proj.SourceTag(h.ScriptPath))
	}

	// Sort event names so output is deterministic regardless of input
	// hook order. Within each event, hooks preserve declaration order
	// (matches Claude's settings.json behavior).
	sort.Strings(eventOrder)

	if len(buckets) == 0 {
		// No projectable hooks (all dropped with warnings); skip the op
		// entirely so we don't clobber an existing hooks.json with an
		// empty merge.
		return nil, warnings, nil
	}

	relPath := ".windsurf/hooks.json"
	merger := func(existing []byte) (string, error) {
		root := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &root); err != nil {
				return "", fmt.Errorf("windsurf: parsing existing %s: %w", relPath, err)
			}
		}
		hooksRoot, _ := root["hooks"].(map[string]any)
		if hooksRoot == nil {
			hooksRoot = map[string]any{}
		}
		for _, ev := range eventOrder {
			entries := buckets[ev]
			// Convert to []any so json.MarshalIndent emits the schema
			// we want regardless of generic map[string]any nesting.
			out := make([]any, 0, len(entries))
			for _, e := range entries {
				out = append(out, e)
			}
			hooksRoot[ev] = out
		}
		root["hooks"] = hooksRoot

		raw, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return "", err
		}
		return string(raw) + "\n", nil
	}

	op := plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    relPath,
		Mode:    plugin.ModeWrite,
		Sources: append([]string{"hooks.yaml"}, sources...),
		Plugin:  "windsurf",
		Merger:  merger,
	}
	return &op, warnings, nil
}

// windsurfMCPServerJSON mirrors the entry schema under mcpServers in
// Windsurf's mcp_config.json. The schema matches Claude Code's .mcp.json
// (command + args + env, optional url for SSE servers), so we reuse the
// same shape.
type windsurfMCPServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// windsurfMCPCanonicalPathWarning is the info-severity message attached
// to every windsurf MCP op. It tells the user that the canonical location
// for MCP config in Windsurf is the user-level path, and gives the two
// workarounds (symlink or shell-init) for making the project-local copy
// take effect.
const windsurfMCPCanonicalPathWarning = "Windsurf canonically reads MCP servers from ~/.codeium/windsurf/mcp_config.json (user-level). " +
	"This project-local .windsurf/mcp_config.json is emitted for portability; " +
	"to activate it, either symlink (`ln -sf \"$PWD/.windsurf/mcp_config.json\" ~/.codeium/windsurf/mcp_config.json`) " +
	"or copy/merge it into the canonical path via your shell init."

// buildWindsurfMCPOp emits the OpMerge for `.windsurf/mcp_config.json`.
// The Merger closure preserves any user-authored entries in mcpServers
// and any top-level keys we don't own, mirroring claude.go:buildSettingsOp.
//
// One canonical-path info warning is emitted on the op itself (regardless
// of how many servers are projected). Scoped MCP servers add a second
// warning per scope (Windsurf has no per-scope MCP, same as Claude).
func buildWindsurfMCPOp(proj *model.Project) (plugin.Operation, error) {
	relPath := ".windsurf/mcp_config.json"

	// Build the project's contribution as a name→entry map; the merger
	// applies it over the existing file.
	contributions := make(map[string]windsurfMCPServerJSON)
	var warnings []plugin.Warning
	var sources []string
	srcSeen := map[string]struct{}{}
	addSource := func(s string) {
		if s == "" {
			return
		}
		if _, ok := srcSeen[s]; ok {
			return
		}
		srcSeen[s] = struct{}{}
		sources = append(sources, s)
	}
	// Always include the base mcp.yaml source tag so `agents which`
	// resolves the projected file even when every server is scoped.
	addSource("mcp.yaml")

	// Deterministic order: sort server names so the merger output is
	// stable across runs even when the input slice reorders.
	names := make([]string, 0, len(proj.MCP))
	servers := make(map[string]*model.MCPServer)
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		names = append(names, srv.Name)
		servers[srv.Name] = srv
	}
	sort.Strings(names)

	for _, name := range names {
		srv := servers[name]
		entry := windsurfMCPServerJSON{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
		}
		if srv.Command == "" && srv.URL != "" {
			entry.URL = srv.URL
		}
		contributions[srv.Name] = entry
		if srv.ScopePath != "" {
			addSource(filepath.ToSlash(filepath.Join(srv.ScopePath, "mcp.yaml")))
			warnings = append(warnings, plugin.Warning{
				Source:   filepath.ToSlash(filepath.Join(srv.ScopePath, "mcp.yaml")),
				Message:  fmt.Sprintf("scoped MCP server %q from %s/mcp.yaml applied project-wide; Windsurf has no per-scope MCP", srv.Name, srv.ScopePath),
				Severity: "info",
			})
		}
	}

	// Canonical-path warning emits whenever any server is projected.
	warnings = append([]plugin.Warning{{
		Source:   "mcp.yaml",
		Message:  windsurfMCPCanonicalPathWarning,
		Severity: "info",
	}}, warnings...)

	merger := func(existing []byte) (string, error) {
		root := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &root); err != nil {
				return "", fmt.Errorf("windsurf: parsing existing %s: %w", relPath, err)
			}
		}
		mcpServers, _ := root["mcpServers"].(map[string]any)
		if mcpServers == nil {
			mcpServers = map[string]any{}
		}
		for _, name := range names {
			mcpServers[name] = contributions[name]
		}
		root["mcpServers"] = mcpServers

		raw, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return "", err
		}
		return string(raw) + "\n", nil
	}

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     relPath,
		Mode:     plugin.ModeWrite,
		Sources:  sources,
		Plugin:   "windsurf",
		Warnings: warnings,
		Merger:   merger,
	}, nil
}

// Compile-time check that WindsurfPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*WindsurfPlugin)(nil)
