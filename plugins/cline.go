// Package plugins contains the projection plugins shipped with agents.
//
// ClinePlugin projects a canonical .agents/ directory into Cline (and
// Roo Code, which uses the same convention) `.clinerules/` rule files,
// plus the modern Cline primitives added in late 2025.
//
// Hook.Event → Cline event mapping (canonical prism events on the left,
// Cline-native event names on the right):
//
//	PreToolUse        → PreToolUse
//	PostToolUse       → PostToolUse
//	UserPromptSubmit  → UserPromptSubmit
//	SessionStart      → TaskStart
//	SessionEnd        → TaskComplete
//	Stop              → TaskComplete
//	Notification      → (no Cline analog; pass-through)
//	SubagentStop      → (no Cline analog; pass-through)
//	PreCompact        → (no Cline analog; pass-through)
//
// Cline-native event names (TaskStart, TaskResume, UserPromptSubmit,
// PreToolUse, PostToolUse, TaskComplete, TaskCancel) pass through verbatim
// so users targeting Cline can use the native names directly.
//
// Note on hook portability: the project-relative wrapper paths baked into
// .clinerules/hooks/<event>.json are absolute paths from proj.Root. Cline's
// hooks.json format has no ${PROJECT_DIR}-style substitution, so moving the
// project tree (`mv`, `rsync`, container mount) requires re-running
// `prism compile` to refresh the wrapper paths. Same constraint applies to
// .cursor/hooks.json and .windsurf/hooks.json.
//
//   - YAML frontmatter on rule files (`paths:`, `description:`) — used
//     to express ScopePaths natively.
//   - Workflows at `.clinerules/workflows/<name>.md` — used for slash
//     commands (CMDS native, replacing the old `30-command-*` rules).
//   - Hooks at `.clinerules/hooks/<event>.json` — JSON-on-stdin /
//     exit-2-blocks contract, matching Claude Code's hook shape.
//   - MCP at `.cline/cline_mcp_settings.json` — project-local mirror of
//     the CLI settings file, written in the standard `{mcpServers:{…}}`
//     schema and merged into any existing file at that path.
//
// Capability summary (v0.8.0):
//   - Context:        native (plain-markdown rule file at 00-context.md)
//   - ScopePaths:     native (YAML frontmatter `paths:` glob array)
//   - ScopeSemantic:  native (same frontmatter; description hint)
//   - Skills:         degraded — no dedicated primitive; emitted as
//     scoped rules with `paths:` + description.
//   - Commands:       native (workflows/<name>.md slash commands)
//   - Hooks:          native (.clinerules/hooks/<event>.json)
//   - MCP:            native (.cline/cline_mcp_settings.json)
//   - Agents:         unsupported (Cline subagents are SDK-based)
//   - Permissions:    unsupported (no native primitive in v0.8.0; could
//     be wrapped via PreToolUse in a later release)
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

// ClinePlugin projects Project state into `.clinerules/` rule files,
// workflows, hooks, and the sibling `.cline/cline_mcp_settings.json`.
type ClinePlugin struct {
	// DisableHookWrappers, when true, projects scoped hooks as if they
	// were global (no `__scope-guard__` wrapper). Default false
	// (wrappers ON). Mirrors ClaudePlugin.DisableHookWrappers for
	// programmatic configuration in tests.
	DisableHookWrappers bool
}

// NewCline constructs a ClinePlugin.
func NewCline() *ClinePlugin { return &ClinePlugin{} }

// Compile-time check that ClinePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ClinePlugin)(nil)

// Name returns the stable plugin identifier.
func (p *ClinePlugin) Name() string { return "cline" }

// Detect returns true if the project at root looks like it uses Cline.
// Either the `.clinerules/` directory (modern, multi-file form) or a
// single `.clinerules` file (legacy form) activates the plugin.
func (p *ClinePlugin) Detect(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ".clinerules")); err == nil {
		return true
	}
	return false
}

// Capabilities returns Cline's capability matrix (v0.8.0).
func (p *ClinePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportNative,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportNative, // wrapper-implemented via prism perms-guard (v0.8.2)
		MCP:           plugin.SupportNative,
	}
}

// clineHookEvents enumerates Cline's native hook event names. Used by
// mapClineEvent to pass-through Cline-shaped event names verbatim.
var clineHookEvents = map[string]struct{}{
	"TaskStart":        {},
	"TaskResume":       {},
	"UserPromptSubmit": {},
	"PreToolUse":       {},
	"PostToolUse":      {},
	"TaskComplete":     {},
	"TaskCancel":       {},
}

// mapClineEvent translates a prism Hook.Event into a Cline-native event
// name. Cline-shaped events pass through unchanged; Claude-style aliases
// that map cleanly (SessionStart → TaskStart, Stop → TaskComplete) are
// rewritten. Returns the input unchanged when no mapping is known — the
// JSON file lands at .clinerules/hooks/<event>.json either way, but
// users targeting Cline should prefer Cline-native names.
func mapClineEvent(event string) string {
	if event == "" {
		return event
	}
	if _, ok := clineHookEvents[event]; ok {
		return event
	}
	switch event {
	case "SessionStart":
		return "TaskStart"
	case "SessionEnd", "Stop":
		return "TaskComplete"
	case "UserPrompt", "UserPromptSubmit":
		return "UserPromptSubmit"
	}
	return event
}

// Plan produces the Operations needed to project proj into Cline's layout.
//
// Mode handling: write (default) emits Operations with Mode=ModeWrite.
// Cline never symlinks — rule files carry a documented preamble and
// frontmatter, and we want each file self-contained so users can hand
// tune individual files. Unknown modes return an error.
func (p *ClinePlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	switch opts.Mode {
	case "", "write":
		// ok
	default:
		return nil, fmt.Errorf("cline: unsupported mode %q", opts.Mode)
	}

	var ops []plugin.Operation

	// Root context → .clinerules/00-context.md (no frontmatter; loads always).
	if proj.Context != nil {
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    ".clinerules/00-context.md",
			Content: ensureTrailingNewline(proj.Context.Body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: []string{proj.SourceTag(proj.Context.SourcePath)},
		})
	}

	// Per-scope rule files at .clinerules/10-scope-<slug>.md with native
	// YAML frontmatter (`paths:` glob array, optional `description:`).
	for _, scope := range proj.Scopes {
		if scope == nil {
			continue
		}
		body := ""
		var sources []string
		if scope.Document != nil {
			body = scope.Document.Body
			sources = []string{proj.SourceTag(scope.Document.SourcePath)}
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "10-scope-"+slugify(scope.Path)+".md")),
			Content: renderClineScope(scope, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		})
	}

	// Skills → .clinerules/20-skill-<slug>.md (global) or
	// .clinerules/20-skill-<scopeSlug>-<name>.md (scoped). Skills get
	// a `paths:` frontmatter when a glob list is available; scripts are
	// noted as ignored (Cline has no script-execution mechanism).
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
		fileName := "20-skill-" + skillSlug(skill.Name) + ".md"
		if skill.ScopePath != "" {
			fileName = "20-skill-" + scopeSlug(skill.ScopePath) + "-" + skillSlug(skill.Name) + ".md"
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", fileName)),
			Content: renderClineSkill(skill, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if len(skill.Scripts) > 0 {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline cannot execute scripts; ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Commands → .clinerules/workflows/<name>.md (native slash commands).
	// Scoped commands get a <scopeSlug>-<name>.md filename and an info
	// warning — workflows are global in Cline, so the scope path is
	// preserved only as a "When working in <path>" preamble.
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
		fileName := skillSlug(cmd.Name) + ".md"
		if cmd.ScopePath != "" {
			fileName = scopeSlug(cmd.ScopePath) + "-" + skillSlug(cmd.Name) + ".md"
		}
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "workflows", fileName)),
			Content: renderClineWorkflow(cmd, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   sourceOrEmpty(sources),
				Message:  fmt.Sprintf("Cline workflows are global; scoped command %q (scope: %s) projected without path enforcement.", cmd.Name, cmd.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (must be emitted before the per-event
	// hook JSON so the JSON can reference the wrapper's absolute path).
	// Each scoped hook gets its own wrapper script at
	// .clinerules/hooks/__scope-guard__/<scopeSlug>-<event>-<basename>.sh.
	// The wrapper reads JSON from stdin and exec's `prism scope-guard`,
	// matching the Claude wrapper contract in plugins/claude.go.
	wrapperPaths := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + mapClineEvent(h.Event) + "-" + hookName + ".sh"
		wrapperRel := filepath.ToSlash(filepath.Join(".clinerules", "hooks", "__scope-guard__", wrapperFile))
		wrapperAbs := filepath.Join(proj.Root, wrapperRel)

		body := buildScopeGuardScript(wrapperRel, h.ScopePath, h.ScriptPath, formatScriptArg(h.ScriptPath, proj.Root))
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     wrapperRel,
			Content:  body,
			Mode:     plugin.ModeWrite,
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   p.Name(),
		})
		wrapperPaths[h] = wrapperAbs
	}

	// Perms-guard wrappers + sidecar policy. v0.8.2 flipped Cline's
	// Permissions cell to native by re-using the gemini wrapper pattern:
	// .cline/hooks/__perms-guard__/{policy.json, *-gate.sh,
	// <event>-<basename>.sh}. The wrappers are wired into PreToolUse.json
	// below — per-hook wrappers replace the raw script command; bare
	// gate wrappers append as their own PreToolUse entries.
	permsOps, permsWarnings, perr := emitPermsGuardWrappers(p.Name(), proj, p.DisableHookWrappers)
	if perr != nil {
		return nil, perr
	}
	ops = append(ops, permsOps...)

	// Build (hook → perms-guard wrapper path) so the hook JSON command
	// points at the wrapper that enforces the policy, then executes the
	// user script on allow.
	permsHookWrappers := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.Event == "" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		var wrapperName string
		if h.ScopePath == "" {
			wrapperName = h.Event + "-" + base + ".sh"
		} else {
			wrapperName = permsScopeSlug(h.ScopePath) + "-" + h.Event + "-" + base + ".sh"
		}
		wrapperRel := filepath.Join("."+p.Name(), "hooks", "__perms-guard__", wrapperName)
		for _, op := range permsOps {
			if op.Path == wrapperRel {
				permsHookWrappers[h] = filepath.Join(proj.Root, wrapperRel)
				break
			}
		}
	}

	var permsGateRefs []string
	for _, op := range permsOps {
		if !strings.Contains(op.Path, "__perms-guard__") {
			continue
		}
		if strings.HasSuffix(op.Path, "global-gate.sh") || strings.HasSuffix(op.Path, "-gate.sh") {
			permsGateRefs = append(permsGateRefs, op.Path)
		}
	}
	sort.Strings(permsGateRefs)

	// Hooks → .clinerules/hooks/<event>.json. One JSON file per event,
	// each carrying the standard {matcher, hooks: [{type, command}]}
	// shape. Scoped hooks point at their scope-guard wrapper. The
	// perms-guard gate wrappers (when permissions exist but no user
	// hooks) are appended to PreToolUse so every tool call flows
	// through the policy. When user hooks DO exist, each hook's
	// command is rewritten to its per-hook perms-guard wrapper
	// (which wraps the user script).
	if hookOps := buildClineHookOps(proj, wrapperPaths, permsHookWrappers, proj.Root, permsGateRefs); len(hookOps) > 0 {
		ops = append(ops, hookOps...)
	}

	// MCP → .cline/cline_mcp_settings.json (project-local).
	//
	// Rationale: the CLI install reads the file from
	// ~/.cline/data/settings/cline_mcp_settings.json, which is per-user
	// and would surprise users by mutating their home directory. The
	// VS Code extension reads from extension globalStorage which is not
	// addressable from a project at all. We pick a project-local path
	// (.cline/cline_mcp_settings.json) — users can `ln -s` or copy it
	// into the CLI location explicitly, which keeps prism's projection
	// safe to run anywhere. The schema is identical to Claude's
	// .mcp.json (standard MCP `{mcpServers: {...}}`), so users with
	// existing tooling can swap files trivially.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildClineMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	// Orphan warnings for capabilities Cline still lacks: agents,
	// permissions. Attach to the first emitted op (if any).
	var orphanWarnings []plugin.Warning
	for _, agent := range proj.Agents {
		if agent == nil {
			continue
		}
		src := ""
		if agent.Document != nil {
			src = proj.SourceTag(agent.Document.SourcePath)
		}
		msg := fmt.Sprintf("Cline has no subagent primitive; %s not projected.", agent.Name)
		if agent.ScopePath != "" {
			msg = fmt.Sprintf("Cline has no subagent primitive; scoped agent %s (scope: %s) not projected.", agent.Name, agent.ScopePath)
		}
		orphanWarnings = append(orphanWarnings, plugin.Warning{
			Source:   src,
			Message:  msg,
			Severity: "info",
		})
	}
	orphanWarnings = append(orphanWarnings, permsWarnings...)

	if len(orphanWarnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, orphanWarnings...)
	}

	return ops, nil
}

// clineHookEntry mirrors the Claude / Cline hook entry shape used inside
// the per-event JSON file at .clinerules/hooks/<event>.json.
type clineHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// clineHookGroup is one matcher group within a hook event.
type clineHookGroup struct {
	Matcher string           `json:"matcher"`
	Hooks   []clineHookEntry `json:"hooks"`
}

// clineHookFile is the JSON document written to
// .clinerules/hooks/<event>.json. The top-level array of matcher groups
// matches the schema Cline uses internally; the engine that reads it
// dispatches by matcher and runs each command with the standard
// JSON-on-stdin / exit-2-blocks contract.
type clineHookFile struct {
	Hooks []clineHookGroup `json:"hooks"`
}

// buildClineHookOps emits one OpWrite per Hook.Event. The known events
// are: TaskStart, TaskResume, UserPromptSubmit, PreToolUse, PostToolUse,
// TaskComplete, TaskCancel. We do not validate the event name here —
// any non-empty event becomes a JSON filename, so callers can stage
// future events without code changes.
//
// permsHookWrappers maps user hooks to their perms-guard wrapper path
// (absolute); when present, the wrapper replaces the raw script command.
// permsGateRefs are project-relative paths to bare perms-guard gates
// (from emitPermsGuardWrappers, when permissions exist but no user
// hooks did); each gate is appended as a PreToolUse entry with empty
// matcher so the policy fires on every tool call.
//
// Precedence: scope-guard wrappers > perms-guard wrappers > raw script.
// (Scope-guard already wraps the perms-guard at the wrapper-script
// level when both apply — the perms-guard wrapper invokes the user
// script which can in turn be scope-gated by the scope-guard wrapper
// the projection emitted alongside.)
func buildClineHookOps(proj *model.Project, wrapperPaths map[*model.Hook]string, permsHookWrappers map[*model.Hook]string, projRoot string, permsGateRefs []string) []plugin.Operation {
	if len(proj.Hooks) == 0 && len(permsGateRefs) == 0 {
		return nil
	}

	buckets := map[string]map[string][]clineHookEntry{}
	matcherOrder := map[string][]string{}
	eventOrder := []string{}
	addEntry := func(ev, matcher string, entry clineHookEntry) {
		if _, ok := buckets[ev]; !ok {
			buckets[ev] = map[string][]clineHookEntry{}
			eventOrder = append(eventOrder, ev)
		}
		if _, ok := buckets[ev][matcher]; !ok {
			matcherOrder[ev] = append(matcherOrder[ev], matcher)
		}
		buckets[ev][matcher] = append(buckets[ev][matcher], entry)
	}
	for _, h := range proj.Hooks {
		if h == nil || h.Event == "" {
			continue
		}
		ev := mapClineEvent(h.Event)
		cmdPath := h.ScriptPath
		if w, ok := wrapperPaths[h]; ok {
			cmdPath = w
		} else if w, ok := permsHookWrappers[h]; ok {
			cmdPath = w
		}
		addEntry(ev, h.Matcher, clineHookEntry{Type: "command", Command: cmdPath})
	}
	for _, gate := range permsGateRefs {
		addEntry("PreToolUse", "", clineHookEntry{
			Type:    "command",
			Command: filepath.Join(projRoot, gate),
		})
	}

	sort.Strings(eventOrder)

	var ops []plugin.Operation
	for _, event := range eventOrder {
		matchers := append([]string(nil), matcherOrder[event]...)
		sort.Strings(matchers)
		groups := make([]clineHookGroup, 0, len(matchers))
		for _, m := range matchers {
			groups = append(groups, clineHookGroup{
				Matcher: m,
				Hooks:   buckets[event][m],
			})
		}
		doc := clineHookFile{Hooks: groups}
		content, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			// Encoding fixed-shape structs cannot fail in practice; surface
			// any breach as an empty file rather than dropping the op.
			content = []byte("{}")
		}
		ops = append(ops, plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    filepath.ToSlash(filepath.Join(".clinerules", "hooks", event+".json")),
			Content: string(content) + "\n",
			Mode:    plugin.ModeWrite,
			Sources: []string{"hooks.yaml"},
			Plugin:  "cline",
		})
	}
	return ops
}

// clineMCPServerJSON is the schema Cline expects for entries under
// `cline_mcp_settings.json`'s `mcpServers` map. Identical to Claude's
// .mcp.json shape, by convention.
type clineMCPServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// buildClineMCPOp emits an OpMerge for .cline/cline_mcp_settings.json.
// The op carries a Merger closure that merges proj.MCP into any existing
// file at that path, preserving unrelated keys the user may have added.
// Mirrors plugins/claude.go's buildMCPOp pattern.
func buildClineMCPOp(proj *model.Project) (plugin.Operation, error) {
	mcpRel := filepath.ToSlash(filepath.Join(".cline", "cline_mcp_settings.json"))

	var warnings []plugin.Warning
	for _, srv := range proj.MCP {
		if srv == nil || srv.ScopePath == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   srv.ScopePath + "/mcp.yaml",
			Message:  fmt.Sprintf("scoped MCP server %q from %s/mcp.yaml applied project-wide; Cline has no per-scope MCP", srv.Name, srv.ScopePath),
			Severity: "info",
		})
	}

	merger := func(existing []byte) (string, error) {
		doc := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &doc); err != nil {
				return "", fmt.Errorf("cline: parsing existing %s: %w", mcpRel, err)
			}
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		for _, srv := range proj.MCP {
			if srv == nil || srv.Name == "" {
				continue
			}
			entry := clineMCPServerJSON{
				Command: srv.Command,
				Args:    srv.Args,
				Env:     srv.Env,
			}
			if srv.Command == "" && srv.URL != "" {
				entry.URL = srv.URL
			}
			servers[srv.Name] = entry
		}
		doc["mcpServers"] = servers
		content, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		return string(content) + "\n", nil
	}

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     mcpRel,
		Mode:     plugin.ModeWrite,
		Sources:  []string{"mcp.yaml"},
		Plugin:   "cline",
		Warnings: warnings,
		Merger:   merger,
	}, nil
}

// renderClineScope builds the markdown body for a scope rule file with
// YAML frontmatter: `paths:` carries the scope's glob array (native
// scope enforcement), `description:` carries the human-readable trigger
// hint. The body keeps the legacy `## When working in <path>` preamble
// so the model sees the scope label even if it ignores frontmatter.
func renderClineScope(scope *model.Scope, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if scope.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", renderYAMLScalar(scope.Description))
	}
	globs := scope.Globs
	if len(globs) == 0 && scope.Path != "" {
		globs = []string{scope.Path + "/**"}
	}
	if len(globs) > 0 {
		b.WriteString("paths:\n")
		for _, g := range globs {
			fmt.Fprintf(&b, "  - %s\n", renderYAMLScalar(g))
		}
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "## When working in %s\n\n", scope.Path)
	if scope.Description != "" {
		fmt.Fprintf(&b, "> Description: %s\n", scope.Description)
	} else if len(scope.Globs) > 0 {
		fmt.Fprintf(&b, "> Triggers: %s\n", strings.Join(scope.Globs, ", "))
	}
	b.WriteString("\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderClineSkill builds the markdown body for a skill rule file. When
// the skill has a non-empty Globs slice (or inherits a scope), the
// frontmatter carries them as `paths:` so the rule loads only on
// matching files; otherwise no frontmatter is emitted.
func renderClineSkill(skill *model.Skill, body string) string {
	var b strings.Builder
	hasFrontmatter := skill.Description != "" || len(skill.Globs) > 0 || skill.ScopePath != ""
	if hasFrontmatter {
		b.WriteString("---\n")
		if skill.Description != "" {
			fmt.Fprintf(&b, "description: %s\n", renderYAMLScalar(skill.Description))
		}
		globs := skill.Globs
		if len(globs) == 0 && skill.ScopePath != "" {
			globs = []string{skill.ScopePath + "/**"}
		}
		if len(globs) > 0 {
			b.WriteString("paths:\n")
			for _, g := range globs {
				fmt.Fprintf(&b, "  - %s\n", renderYAMLScalar(g))
			}
		}
		b.WriteString("---\n\n")
	}
	if skill.ScopePath != "" {
		fmt.Fprintf(&b, "## When working in %s\n\n", skill.ScopePath)
	}
	fmt.Fprintf(&b, "## Skill: %s\n\n", skill.Name)
	hint := skill.Description
	if hint == "" {
		hint = skill.Trigger
	}
	if hint != "" {
		fmt.Fprintf(&b, "> %s\n", hint)
	}
	b.WriteString("\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderClineWorkflow builds the markdown body for a workflow file at
// .clinerules/workflows/<name>.md. Cline reads the file when the user
// types `/<filename>` (sans extension) and replays the body into the
// next user turn. We retain a `## Command /<name>` header so the body
// is self-describing when read directly.
func renderClineWorkflow(cmd *model.Command, body string) string {
	var b strings.Builder
	hasFrontmatter := cmd.Description != ""
	if hasFrontmatter {
		b.WriteString("---\n")
		fmt.Fprintf(&b, "description: %s\n", renderYAMLScalar(cmd.Description))
		b.WriteString("---\n\n")
	}
	if cmd.ScopePath != "" {
		fmt.Fprintf(&b, "## When working in %s\n\n", cmd.ScopePath)
	}
	fmt.Fprintf(&b, "## Command /%s\n\n", cmd.Name)
	if cmd.Description != "" {
		fmt.Fprintf(&b, "> %s\n", cmd.Description)
	}
	b.WriteString("\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ensureTrailingNewline returns s with a trailing newline if it is
// non-empty. Empty input is returned as-is so we never write a lone "\n".
func ensureTrailingNewline(s string) string {
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// sourceOrEmpty returns the first element of sources, or "" if empty.
// Warnings need a Source field; this avoids out-of-range issues when a
// document has no source path.
func sourceOrEmpty(sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	return sources[0]
}
