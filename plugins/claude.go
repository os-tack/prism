// Package plugins contains the projection plugins that translate a canonical
// .agents/ model into per-tool config files.
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

// ClaudePlugin projects a model.Project into Claude Code's on-disk layout:
// CLAUDE.md (root + per-scope), .claude/skills/<name>/SKILL.md (+ scripts/),
// .claude/commands/<name>.md, .claude/agents/<name>.md, .claude/settings.json
// (permissions + hooks), and .mcp.json (MCP servers).
//
// Scoped capabilities (v0.5): skills with ScopePath are projected natively
// using a `<scopeSlug>-<name>` prefix to avoid name collisions across scopes;
// commands and agents are projected with the same prefix and a "degraded"
// warning (Claude Code has no native per-path command/agent scoping); hooks
// are projected via a generated `__scope-guard__` wrapper script that gates
// on $CLAUDE_TOOL_INPUT_FILE_PATH at runtime; permissions and MCP servers
// are merged into the global blocks with a degradation warning per scope.
type ClaudePlugin struct {
	// DisableHookWrappers, when true, projects scoped hooks as if they were
	// global (no `__scope-guard__` wrapper). Default false (wrappers ON).
	//
	// TODO(v0.6): wire CLI flag `--no-hook-wrappers` into this field via
	// plugin opts. For now this is exposed only as a struct field for
	// programmatic configuration in tests / library consumers.
	DisableHookWrappers bool
}

// NewClaude returns a fresh ClaudePlugin.
func NewClaude() *ClaudePlugin {
	return &ClaudePlugin{}
}

// Name is the stable plugin identifier.
func (p *ClaudePlugin) Name() string {
	return "claude"
}

// Detect returns true if a .claude/ directory or a CLAUDE.md file is present
// at the given project root.
func (p *ClaudePlugin) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".claude")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Capabilities returns the capability matrix entry for Claude Code.
func (p *ClaudePlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportDegraded, // no native trigger description; surfaced via IMPORTANT prefix
		Skills:        plugin.SupportNative,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportNative,
		Hooks:         plugin.SupportNative,
		Permissions:   plugin.SupportNative,
		MCP:           plugin.SupportNative,
	}
}

// scopeSlug converts a scope path (e.g. "src/billing") to a filesystem-safe
// slug used as a prefix for scoped artifacts (e.g. "src-billing"). Empty
// scope path returns the empty string.
func scopeSlug(scopePath string) string {
	if scopePath == "" {
		return ""
	}
	return strings.ReplaceAll(scopePath, "/", "-")
}

// scopedName returns the projected artifact name for a scoped capability,
// prefixed by the scope slug. For global capabilities (empty scopePath) it
// returns name unchanged.
func scopedName(scopePath, name string) string {
	if scopePath == "" {
		return name
	}
	return scopeSlug(scopePath) + "-" + name
}

// Plan produces the Operations needed to project proj into Claude Code's layout.
//
// Context + scopes → CLAUDE.md (symlink by default, write per opts.Mode).
// Skills → .claude/skills/<name>/SKILL.md (+ scripts/<basename>).
// Commands → .claude/commands/<name>.md.
// Agents → .claude/agents/<name>.md.
// Hooks + Permissions → merged into .claude/settings.json (always write).
// MCP servers → merged into .mcp.json (always write).
//
// Scoped variants:
//
//	Skill   (ScopePath != "")  → .claude/skills/<scopeSlug>-<name>/SKILL.md (Native)
//	Command (ScopePath != "")  → .claude/commands/<scopeSlug>-<name>.md     (Degrade)
//	Agent   (ScopePath != "")  → .claude/agents/<scopeSlug>-<name>.md       (Degrade)
//	Hook    (ScopePath != "")  → wrapper at .claude/hooks/__scope-guard__/
//	                             <scopeSlug>-<hookname>.sh; settings.json
//	                             command points at wrapper (Wrapper script)
//	Permissions (per scope)    → merged into global allow/deny/ask (Degrade)
//	MCPServer (ScopePath!="")  → merged into global .mcp.json     (Degrade)
func (p *ClaudePlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	mode := plugin.ModeSymlink
	switch opts.Mode {
	case "write":
		mode = plugin.ModeWrite
	case "symlink", "":
		mode = plugin.ModeSymlink
	default:
		return nil, fmt.Errorf("claude: unknown mode %q (want \"write\" or \"symlink\")", opts.Mode)
	}

	if proj == nil {
		return nil, nil
	}

	var ops []plugin.Operation

	// Root CLAUDE.md.
	if proj.Context != nil {
		op, err := buildOp(proj, proj.Context, "CLAUDE.md", mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Per-scope CLAUDE.md files.
	for _, sc := range proj.Scopes {
		if sc == nil || sc.Document == nil {
			continue
		}
		path := filepath.Join(sc.Path, "CLAUDE.md")
		op, err := buildOp(proj, sc.Document, path, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Skills (global + scoped). Scoped skills are projected natively under
	// `.claude/skills/<scopeSlug>-<name>/SKILL.md`; the body (with `globs:`
	// frontmatter) is unchanged from a global skill.
	for _, sk := range proj.Skills {
		if sk == nil || sk.Document == nil {
			continue
		}
		dirName := scopedName(sk.ScopePath, sk.Name)
		skillDir := filepath.Join(".claude", "skills", dirName)
		skillPath := filepath.Join(skillDir, "SKILL.md")
		op, err := buildOp(proj, sk.Document, skillPath, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)

		// Each script becomes a symlink under scripts/<basename>.
		for _, scriptPath := range sk.Scripts {
			if scriptPath == "" {
				continue
			}
			scriptOp, err := buildScriptOp(proj, scriptPath, skillDir, mode)
			if err != nil {
				return nil, err
			}
			ops = append(ops, scriptOp)
		}
	}

	// Commands (global + scoped). Scoped commands are degraded — Claude
	// Code commands are global, so we prefix the projected name and emit
	// an info warning explaining the loss of path enforcement.
	for _, cmd := range proj.Commands {
		if cmd == nil || cmd.Document == nil {
			continue
		}
		fileName := scopedName(cmd.ScopePath, cmd.Name) + ".md"
		path := filepath.Join(".claude", "commands", fileName)
		op, err := buildOp(proj, cmd.Document, path, mode)
		if err != nil {
			return nil, err
		}
		if cmd.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(cmd.Document.SourcePath),
				Message:  fmt.Sprintf("scoped command %q projected without path enforcement (Claude commands are global)", cmd.Name),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Agents (global + scoped). Scoped agents are degraded — same rationale
	// as commands above.
	for _, ag := range proj.Agents {
		if ag == nil || ag.Document == nil {
			continue
		}
		fileName := scopedName(ag.ScopePath, ag.Name) + ".md"
		path := filepath.Join(".claude", "agents", fileName)
		op, err := buildOp(proj, ag.Document, path, mode)
		if err != nil {
			return nil, err
		}
		if ag.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   proj.SourceTag(ag.Document.SourcePath),
				Message:  fmt.Sprintf("scoped agent %q projected without path enforcement (Claude agents are global)", ag.Name),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// Scoped hook wrapper scripts (must be emitted before the settings op
	// so the settings op can reference the wrapper's absolute path).
	// Each scoped hook gets its own wrapper script under
	// `.claude/hooks/__scope-guard__/`. The wrapper inspects
	// $CLAUDE_TOOL_INPUT_FILE_PATH at runtime and only exec's the source
	// script when the path is under the hook's scope.
	wrapperPaths := map[*model.Hook]string{} // hook → absolute wrapper path
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		// Use the script's basename (sans extension) as the hook name
		// component. This keeps wrapper file names stable across renames
		// of unrelated hooks.
		hookName := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + hookName + ".sh"
		wrapperRel := filepath.Join(".claude", "hooks", "__scope-guard__", wrapperFile)
		wrapperAbs := filepath.Join(proj.Root, wrapperRel)

		body := buildScopeGuardScript(wrapperRel, h.ScopePath, h.ScriptPath, formatScriptArg(h.ScriptPath, proj.Root))
		ops = append(ops, plugin.Operation{
			Kind:     plugin.OpWrite,
			Path:     wrapperRel,
			Content:  body,
			Mode:     plugin.ModeWrite, // ModeHook semantics not wired yet; treat as write
			FileMode: 0o755,
			Sources:  []string{proj.SourceTag(h.ScriptPath)},
			Plugin:   "claude",
		})
		wrapperPaths[h] = wrapperAbs
	}

	// .claude/settings.json (hooks + permissions). Always write. We emit
	// settings.json whenever there is any global or scoped permission, or
	// any hook (global or scoped).
	hasPerms := proj.Permissions != nil || len(proj.ScopedPermissions) > 0
	if hasPerms || len(proj.Hooks) > 0 {
		settingsOp, err := buildSettingsOp(proj, wrapperPaths)
		if err != nil {
			return nil, err
		}
		ops = append(ops, settingsOp)
	}

	// .mcp.json (MCP servers). Always write.
	if len(proj.MCP) > 0 {
		mcpOp, err := buildMCPOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, mcpOp)
	}

	return ops, nil
}

// buildScopeGuardScript renders the wrapper-script body that gates a
// scoped Claude hook on the file-path scope at runtime.
//
// Claude Code invokes hook scripts with the tool input as JSON on stdin
// (see https://code.claude.com/docs/en/hooks). Earlier prism versions
// gated on a non-existent $CLAUDE_TOOL_INPUT_FILE_PATH env var; the
// real contract is JSON, so the wrapper now exec's `prism scope-guard
// --scope <path> --script <abs>`, which parses the JSON and either
// invokes the source script (passing stdin through) or exits 0.
//
// wrapperRel is the project-relative path of the wrapper itself; we
// embed neither the project root nor any absolute path into the
// rendered bash. At runtime the wrapper resolves the project root from
// ${BASH_SOURCE[0]} (with PRISM_PROJECT_DIR and CLAUDE_PROJECT_DIR
// taking precedence), so the wrapper survives `mv` of the project (I4).
// sourceScript is the absolute path to the user's authored hook (used
// only for the comment header / filename basename). scriptArg is the
// pre-quoted shell argument for --script, formatted at the call site
// via formatScriptArg so that scripts under proj.Root are rewritten to
// "${PROJECT_DIR}"/<rel> — the wrapper survives `mv` of the project
// for both PROJECT_DIR resolution and the --script reference (fixes
// I-1 from v0.7.1 review).
//
// The wrapper requires `prism` on PATH at hook-firing time. That's a
// reasonable assumption since the user installed prism to project the
// hook in the first place.
func buildScopeGuardScript(wrapperRel, scopePath, sourceScript, scriptArg string) string {
	upDots := rootRelativeFromWrapper(wrapperRel)
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# prism-generated scope guard for ")
	b.WriteString(sanitizeBashComment(scopePath))
	b.WriteString("/hooks/")
	b.WriteString(sanitizeBashComment(strings.TrimSuffix(filepath.Base(sourceScript), filepath.Ext(sourceScript))))
	b.WriteString(".yaml\n")
	b.WriteString("#\n")
	b.WriteString("# Reads Claude Code's hook JSON from stdin, dispatches to the source\n")
	b.WriteString("# script when tool_input.file_path falls under the scope.\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("SCRIPT_DIR=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"\n")
	b.WriteString("PROJECT_DIR=\"${PRISM_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$(cd \"${SCRIPT_DIR}/")
	b.WriteString(upDots)
	b.WriteString("\" && pwd)}}\"\n")
	b.WriteString("export CLAUDE_PROJECT_DIR=\"${CLAUDE_PROJECT_DIR:-${PROJECT_DIR}}\"\n")
	b.WriteString("exec prism scope-guard --scope ")
	b.WriteString(shellQuote(scopePath))
	b.WriteString(" --script ")
	b.WriteString(scriptArg)
	b.WriteString("\n")
	return b.String()
}

// shellQuote single-quotes a string for safe shell interpolation. Single
// quotes inside the input are emitted as '\'\'.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sanitizeBashComment replaces characters that would break a `# ...` line
// in the wrapper preamble. Newlines and carriage returns become `?` so a
// scope path or filename containing them does not split the comment into
// a second line that bash would interpret (N-b from v0.7.1 review).
func sanitizeBashComment(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	r := strings.NewReplacer("\n", "?", "\r", "?")
	return r.Replace(s)
}

// buildOp constructs a single Operation for a Document being projected to the
// given target path (relative to project root) in the given Mode.
//
// When doc.NeedsWrite() is true (the document had @include directives
// expanded into its body) symlink mode is downgraded to write mode and
// an info-severity warning is attached describing the reason. Every
// included file's source tag is appended to op.Sources so lockfile /
// `agents which` traces flow back to the included content too.
func buildOp(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode) (plugin.Operation, error) {
	downgraded := false
	if doc.NeedsWrite() && mode == plugin.ModeSymlink {
		mode = plugin.ModeWrite
		downgraded = true
	}

	sources := []string{proj.SourceTag(doc.SourcePath)}
	for _, inc := range doc.Includes {
		sources = append(sources, proj.SourceTag(inc))
	}

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: sources,
		Plugin:  "claude",
	}

	if mode == plugin.ModeSymlink {
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, doc.SourcePath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.Kind = plugin.OpSymlink
		op.LinkTarget = filepath.ToSlash(linkTarget)
	} else {
		op.Kind = plugin.OpWrite
		op.Content = doc.Body
	}

	if downgraded {
		op.Warnings = append(op.Warnings, plugin.Warning{
			Source:   proj.SourceTag(doc.SourcePath),
			Message:  "downgraded to write mode: contains @include directives",
			Severity: "info",
		})
	}

	return op, nil
}

// buildScriptOp constructs an Operation that places a skill script under
// `<skillDir>/scripts/<basename>`. Scripts are symlinks (or copies in write
// mode) pointing at the original absolute path in .agents/.
func buildScriptOp(proj *model.Project, scriptPath, skillDir string, mode plugin.Mode) (plugin.Operation, error) {
	base := filepath.Base(scriptPath)
	targetPath := filepath.Join(skillDir, "scripts", base)

	srcRel := proj.SourceTag(scriptPath)

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: []string{srcRel},
		Plugin:  "claude",
	}

	if mode == plugin.ModeSymlink {
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, scriptPath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.Kind = plugin.OpSymlink
		op.LinkTarget = filepath.ToSlash(linkTarget)
	} else {
		// In write mode we still emit a symlink — engine may downgrade later.
		// We don't read the script bytes here because plugins are pure.
		op.Kind = plugin.OpSymlink
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, scriptPath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.LinkTarget = filepath.ToSlash(linkTarget)
	}

	return op, nil
}

// hookEntry mirrors Claude Code's settings.json hook schema:
//
//	{"type": "command", "command": "<absolute-script-path>"}
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// hookGroup mirrors Claude Code's settings.json hook group schema:
//
//	{"matcher": "...", "hooks": [{"type": "command", "command": "..."}]}
type hookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// buildSettingsOp emits the OpMerge for .claude/settings.json. The op carries
// a Merger closure that the engine invokes with the file's existing bytes;
// Plan() itself does no filesystem I/O so plugin behavior is reproducible
// from (proj, wrapperPaths) alone.
func buildSettingsOp(proj *model.Project, wrapperPaths map[*model.Hook]string) (plugin.Operation, error) {
	var warnings []plugin.Warning

	allow, deny, ask := []string{}, []string{}, []string{}
	if proj.Permissions != nil {
		allow = append(allow, proj.Permissions.Allow...)
		deny = append(deny, proj.Permissions.Deny...)
		ask = append(ask, proj.Permissions.Ask...)
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		allow = append(allow, sp.Allow...)
		deny = append(deny, sp.Deny...)
		ask = append(ask, sp.Ask...)
		warnings = append(warnings, plugin.Warning{
			Source:   sp.ScopePath + "/permissions.yaml",
			Message:  fmt.Sprintf("permissions from %s/permissions.yaml applied project-wide; Claude Code has no per-scope permissions", sp.ScopePath),
			Severity: "info",
		})
	}
	allow = dedupeStrings(allow)
	deny = dedupeStrings(deny)
	ask = dedupeStrings(ask)
	buckets := map[string]map[string][]hookEntry{}
	eventOrder := []string{}
	for _, h := range proj.Hooks {
		if h == nil || h.Event == "" {
			continue
		}
		if _, ok := buckets[h.Event]; !ok {
			buckets[h.Event] = map[string][]hookEntry{}
			eventOrder = append(eventOrder, h.Event)
		}
		cmdPath := h.ScriptPath
		if w, ok := wrapperPaths[h]; ok {
			cmdPath = w
		}
		buckets[h.Event][h.Matcher] = append(buckets[h.Event][h.Matcher], hookEntry{
			Type:    "command",
			Command: cmdPath,
		})
	}
	relPath := filepath.Join(".claude", "settings.json")

	merger := func(existing []byte) (string, error) {
		settings := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &settings); err != nil {
				return "", fmt.Errorf("claude: parsing existing %s: %w", relPath, err)
			}
		}

		if len(allow) > 0 || len(deny) > 0 || len(ask) > 0 {
			perms, _ := settings["permissions"].(map[string]any)
			if perms == nil {
				perms = map[string]any{}
			}
			if len(allow) > 0 {
				perms["allow"] = allow
			}
			if len(deny) > 0 {
				perms["deny"] = deny
			}
			if len(ask) > 0 {
				perms["ask"] = ask
			}
			settings["permissions"] = perms
		}

		if len(proj.Hooks) > 0 {
			hooksRoot, _ := settings["hooks"].(map[string]any)
			if hooksRoot == nil {
				hooksRoot = map[string]any{}
			}
			for _, event := range eventOrder {
				matcherMap := buckets[event]
				matchers := make([]string, 0, len(matcherMap))
				for m := range matcherMap {
					matchers = append(matchers, m)
				}
				sort.Strings(matchers)
				var groups []hookGroup
				for _, m := range matchers {
					groups = append(groups, hookGroup{
						Matcher: m,
						Hooks:   matcherMap[m],
					})
				}
				hooksRoot[event] = groups
			}
			settings["hooks"] = hooksRoot
		}

		content, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return "", err
		}
		return string(content) + "\n", nil
	}

	var sources []string
	if proj.Permissions != nil {
		sources = append(sources, "permissions.yaml")
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil {
			continue
		}
		sources = append(sources, sp.ScopePath+"/permissions.yaml")
	}
	if len(proj.Hooks) > 0 {
		sources = append(sources, "hooks.yaml")
	}

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     relPath,
		Mode:     plugin.ModeWrite,
		Sources:  sources,
		Plugin:   "claude",
		Warnings: warnings,
		Merger:   merger,
	}, nil
}

// mcpServerJSON is the schema Claude Code expects for entries under
// `.mcp.json`'s `mcpServers` map.
type mcpServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// buildMCPOp merges proj.MCP into any existing .mcp.json at proj.Root.
// Scoped MCP servers are merged into the same mcpServers map as globals
// (Claude Code has no native per-scope MCP) with one info warning per
// scoped server.
func buildMCPOp(proj *model.Project) (plugin.Operation, error) {
	mcpPath := filepath.Join(proj.Root, ".mcp.json")

	doc := map[string]any{}
	if data, err := os.ReadFile(mcpPath); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return plugin.Operation{}, fmt.Errorf("claude: parsing existing %s: %w", mcpPath, err)
		}
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	var warnings []plugin.Warning
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" {
			continue
		}
		entry := mcpServerJSON{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
		}
		if srv.Command == "" && srv.URL != "" {
			entry.URL = srv.URL
		}
		servers[srv.Name] = entry
		if srv.ScopePath != "" {
			warnings = append(warnings, plugin.Warning{
				Source:   srv.ScopePath + "/mcp.yaml",
				Message:  fmt.Sprintf("scoped MCP server %q from %s/mcp.yaml applied project-wide; Claude Code has no per-scope MCP", srv.Name, srv.ScopePath),
				Severity: "info",
			})
		}
	}
	doc["mcpServers"] = servers

	content, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return plugin.Operation{}, err
	}

	return plugin.Operation{
		Kind:     plugin.OpWrite,
		Path:     ".mcp.json",
		Content:  string(content) + "\n",
		Mode:     plugin.ModeWrite,
		Sources:  []string{"mcp.yaml"},
		Plugin:   "claude",
		Warnings: warnings,
	}, nil
}

// Compile-time check that ClaudePlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*ClaudePlugin)(nil)

// dedupeStrings preserves first-seen order while dropping duplicates. Used
// when scoped and global permission lists are unioned into Claude's flat
// allow/deny/ask blocks, so the lockfile hash stays stable when a scoped
// rule repeats a global one.
func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
