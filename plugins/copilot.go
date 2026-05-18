package plugins

// CopilotPlugin projects a canonical .agents/ directory into GitHub Copilot's
// repo-local configuration surface under `.github/`:
//
//   - .github/copilot-instructions.md            — single repo-wide instructions
//   - .github/instructions/<slug>.instructions.md — per-glob scoped instructions
//                                                   (frontmatter: applyTo)
//   - .github/prompts/<name>.prompt.md           — prompt files (slash-command analog)
//                                                   (frontmatter: description, mode)
//   - .github/agents/<name>.agent.md             — Copilot custom agents (GA)
//                                                   (frontmatter: name, description,
//                                                   tools, model, agents, etc.)
//   - .github/mcp.json                           — Copilot-discovered MCP servers
//                                                   (same {mcpServers: {...}} schema
//                                                   the Claude/MCP ecosystem uses)
//
// Copilot's `applyTo` is a single glob string (not a list); when a Scope or
// Skill has multiple globs we use the first and emit a degradation warning
// naming the dropped patterns.
//
// Scoped skills include the scope slug as a filename prefix (skill-<scopeSlug>-<name>)
// so same-named skills across scopes don't collide; the parser populates
// Skill.Globs from frontmatter override or the scope's default, so the
// applyTo value comes out correctly without extra plumbing.
//
// Scoped commands are projected as scoped prompt files; Copilot prompts have
// no path scoping mechanism (no applyTo on prompts) so a warning notes the
// degradation alongside the filename-only disambiguation.
//
// Hook emission is intentionally not implemented: Copilot hooks are in
// public preview at the time of writing, so Hooks remains SupportUnsupported
// and hook-bearing projects get an info warning per hook. A future flag
// (e.g. --enable-preview-hooks on the compile command) can flip this on.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/version"
)

// CopilotPlugin projects Project state into `.github/` files Copilot reads.
//
// v0.8.2 additions (gated by EnablePreviewHooks):
//   - .github/hooks/hooks.json — repo-local hook config (Copilot preview;
//     spec: https://docs.github.com/en/copilot/reference/hooks-configuration)
//   - .github/hooks/__scope-guard__/<wrapper>.sh — scoped-hook wrappers
//   - .github/hooks/__perms-guard__/{policy.json,wrapper.sh} — perms-guard
//     sidecar policy + PreToolUse wrapper that enforces it
type CopilotPlugin struct {
	// DisableHookWrappers, when true, suppresses both the perms-guard
	// wrapper script + sidecar policy emission AND the scope-guard
	// wrappers for scoped hooks. Mirrors ClaudePlugin/GeminiPlugin.
	DisableHookWrappers bool

	// EnablePreviewHooks, when true, opts into the Copilot preview hook
	// surface: .github/hooks/hooks.json emission plus the perms-guard
	// PreToolUse wiring that depends on it. Off by default since the
	// preview JSON schema can still change before GA.
	EnablePreviewHooks bool
}

// NewCopilot constructs a CopilotPlugin.
//
// As with the other plugins in this package, we use a per-plugin constructor
// name (`NewCopilot`) because Go does not permit a package-scope `New`
// shared between multiple types.
func NewCopilot() *CopilotPlugin { return &CopilotPlugin{} }

// Name returns the stable plugin identifier.
func (p *CopilotPlugin) Name() string { return "copilot" }

// Detect returns true if the project at root looks like it uses Copilot.
// The presence of a `.github/` directory is the primary signal — almost every
// real GitHub repo has one — and we also accept a bare
// `.github/copilot-instructions.md` file in case `.github/` is missing for
// some reason but Copilot wiring is already in place.
func (p *CopilotPlugin) Detect(root string) bool {
	if info, err := os.Stat(filepath.Join(root, ".github")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, ".github", "copilot-instructions.md")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// Capabilities returns Copilot.s capability matrix.
//
// Copilot natively supports a repo-wide instructions file (Context), per-glob
// instruction attachment (ScopePaths), prompt files / slash commands
// (Commands), custom agents via `.github/agents/<name>.agent.md` (Agents,
// GA as of 2026), and project-local MCP via `.github/mcp.json` (MCP).
//
// ScopeSemantic stays degraded — `applyTo` is glob-only, with no
// description-driven trigger. Skills degrade to scoped instructions.
//
// Hooks and Permissions flip on the EnablePreviewHooks opt-in (v0.8.2).
// Both surfaces ride the Copilot preview hooks API; Permissions enforcement
// is wrapper-based (PreToolUse + perms-guard sidecar), Hooks projects the
// canonical Hook slice into the preview .github/hooks/hooks.json schema.
// When the flag is off, both stay `----` (the v0.8.1 default behaviour).
func (p *CopilotPlugin) Capabilities() plugin.Capabilities {
	hooks := plugin.SupportUnsupported
	perms := plugin.SupportUnsupported
	if p.EnablePreviewHooks {
		hooks = plugin.SupportNative
		perms = plugin.SupportNative
	}

	// v2 per-field capabilities (SPEC §12 Copilot column). Absent
	// fields default to FieldNative; we only enumerate the non-native
	// cells. The Extensions slot declares that this plugin reads
	// extensions.copilot.* pass-through (including the
	// `extensions.copilot.preview_hooks: true` opt-in marker some
	// projects carry to keep the flag out of CLI flags).
	agentFields := plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"DisallowedTools": plugin.FieldDegraded,
			"ReadOnly":        plugin.FieldDegraded,
			"Background":      plugin.FieldSilent,
			"MaxTurns":        plugin.FieldSilent,
			"Temperature":     plugin.FieldSilent,
			"InitialPrompt":   plugin.FieldUnsupported,
			"ScopePath":       plugin.FieldDegraded,
		},
		Extensions: []string{"copilot"},
	}

	skillFields := plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"WhenToUse":                 plugin.FieldDegraded,
			"Activation.Modes={Always}": plugin.FieldDegraded,
			"Activation.ContentRegex":   plugin.FieldUnsupported,
			"Activation.UserInvocable":  plugin.FieldSilent,
			"Activation.ModelInvocable": plugin.FieldSilent,
			"ScopePath":                 plugin.FieldDegraded,
		},
		Extensions: []string{"copilot"},
	}

	commandFields := plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"AutoInvoke": plugin.FieldSilent,
			"ScopePath":  plugin.FieldDegraded,
		},
		Extensions: []string{"copilot"},
	}

	// Hook — entire primitive only flips Supported=true on the
	// preview-hooks opt-in. The field overrides describe what the
	// preview surface CAN'T express even when on.
	hookFields := plugin.FieldCapabilities{
		Supported: p.EnablePreviewHooks,
		Fields: map[string]plugin.FieldSupport{
			"Name":                plugin.FieldSilent,
			"Description":         plugin.FieldSilent,
			"Event (Claude-only)": plugin.FieldUnsupported,
			"Handlers (mcp_tool)": plugin.FieldUnsupported,
			"Handlers (prompt)":   plugin.FieldDegraded,
			"Handlers (agent)":    plugin.FieldUnsupported,
			"Sequential":          plugin.FieldSilent,
			"StatusMessage":       plugin.FieldSilent,
			"Async":               plugin.FieldUnsupported,
			"FailClosed":          plugin.FieldUnsupported,
			"Once":                plugin.FieldUnsupported,
			"If":                  plugin.FieldUnsupported,
			"ScopePath":           plugin.FieldNative, // scope-guard wrapper
		},
		Extensions: []string{"copilot"},
	}

	mcpServerFields := plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Cwd":               plugin.FieldSilent,
			"Auth.Scheme=oauth": plugin.FieldDegraded,
			"TimeoutMs":         plugin.FieldUnsupported,
			"AutoApprove":       plugin.FieldUnsupported,
			"Trust":             plugin.FieldUnsupported,
			"IncludeTools":      plugin.FieldUnsupported,
			"ExcludeTools":      plugin.FieldUnsupported,
			"ScopePath":         plugin.FieldDegraded,
		},
		Extensions: []string{"copilot"},
	}

	// Permissions — native via the preview perms-guard sidecar when
	// EnablePreviewHooks is on; unsupported otherwise. Every
	// sub-cell in SPEC §12 collapses to FieldNative on the wrapper
	// so we leave the Fields map empty (defaults to FieldNative).
	permsFields := plugin.FieldCapabilities{
		Supported:  p.EnablePreviewHooks,
		Extensions: []string{"copilot"},
	}

	scopeFields := plugin.FieldCapabilities{
		Supported: true,
		Fields: map[string]plugin.FieldSupport{
			"Path (cascade)":       plugin.FieldDegraded,
			"Activation=Cascade":   plugin.FieldDegraded,
			"Activation=Manual":    plugin.FieldUnsupported,
			"Activation=ModelDec.": plugin.FieldUnsupported,
			"Priority":             plugin.FieldSilent,
			"Tags":                 plugin.FieldSilent,
			"IsOverride":           plugin.FieldUnsupported,
		},
		Extensions: []string{"copilot"},
	}

	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportDegraded,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportNative,
		Hooks:         hooks,
		Permissions:   perms,
		MCP:           plugin.SupportNative,

		AgentFields:       agentFields,
		SkillFields:       skillFields,
		CommandFields:     commandFields,
		HookFields:        hookFields,
		MCPServerFields:   mcpServerFields,
		PermissionsFields: permsFields,
		ScopeFields:       scopeFields,
	}
}

// Plan produces the Operations needed to project proj into `.github/`.
//
// Mode handling: empty and "symlink" are accepted (symlink default for the
// root copilot-instructions.md, which is plain markdown). "write" forces
// write mode for everything. Per-scope/per-skill files and prompts are
// ALWAYS write mode because they inject frontmatter the canonical source
// does not contain; symlinking those would point at byte-different content.
// Agent files are also ALWAYS write mode (frontmatter is rebuilt). MCP is
// always write/merge.
// Any other mode value returns an error.
func (p *CopilotPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	if proj == nil {
		return nil, nil
	}

	mode := plugin.ModeSymlink
	switch opts.Mode {
	case "", "symlink":
		mode = plugin.ModeSymlink
	case "write":
		mode = plugin.ModeWrite
	default:
		return nil, fmt.Errorf("copilot: unsupported mode %q (want \"write\" or \"symlink\")", opts.Mode)
	}

	var ops []plugin.Operation

	// 1. Root context → .github/copilot-instructions.md. Plain markdown, no
	// frontmatter required, so symlink mode is honored when possible.
	//
	// When the document had @include directives expanded into its body
	// (Context.NeedsWrite() is true), symlink mode is downgraded to write
	// mode and an info warning is attached. Included source tags are
	// appended to op.Sources for lockfile / `agents which` traces.
	if proj.Context != nil {
		const path = ".github/copilot-instructions.md"
		ctxMode := mode
		downgraded := false
		if proj.Context.NeedsWrite() && ctxMode == plugin.ModeSymlink {
			ctxMode = plugin.ModeWrite
			downgraded = true
		}
		srcRel := proj.SourceTag(proj.Context.SourcePath)
		sources := []string{srcRel}
		for _, inc := range proj.Context.Includes {
			sources = append(sources, proj.SourceTag(inc))
		}
		op := plugin.Operation{
			Path:    path,
			Mode:    ctxMode,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if ctxMode == plugin.ModeSymlink && proj.Root != "" && proj.Context.SourcePath != "" {
			targetDir := filepath.Join(proj.Root, filepath.Dir(path))
			linkTarget, err := filepath.Rel(targetDir, proj.Context.SourcePath)
			if err != nil {
				return nil, fmt.Errorf("copilot: compute symlink target for %s: %w", path, err)
			}
			op.Kind = plugin.OpSymlink
			op.LinkTarget = filepath.ToSlash(linkTarget)
		} else {
			op.Kind = plugin.OpWrite
			op.Content = proj.Context.Body
		}
		if downgraded {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   srcRel,
				Message:  "downgraded to write mode: contains @include directives",
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// 2. Per-scope instruction files. ALWAYS write mode (frontmatter injection).
	for _, sc := range proj.Scopes {
		if sc == nil {
			continue
		}
		body := ""
		var sources []string
		if sc.Document != nil {
			body = sc.Document.Body
			sources = []string{proj.SourceTag(sc.Document.SourcePath)}
			for _, inc := range sc.Document.Includes {
				sources = append(sources, proj.SourceTag(inc))
			}
		}
		applyTo, scopeWarn := pickApplyTo(sc.Globs, sc.Document)
		path := filepath.ToSlash(filepath.Join(".github", "instructions", slugify(sc.Path)+".instructions.md"))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: renderCopilotInstructions(applyTo, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if scopeWarn != nil {
			op.Warnings = append(op.Warnings, *scopeWarn)
		}
		ops = append(ops, op)
	}

	// 3. Skills → degraded scoped instruction files under .github/instructions.
	// Filename prefix `skill-` keeps them distinguishable from canonical scopes.
	// Scoped skills include the scope slug as part of the filename to avoid
	// collisions; Skill.Globs is populated by the parser (frontmatter override
	// or scope default) so applyTo lands on the right path.
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
		// v2-additive: prefer the v0.8 Skill.Globs when populated, fall
		// back to Activation.Globs (SPEC §4.2.2). The parser mirrors
		// both shapes, but a v2-only producer might leave the v0.8
		// slot empty.
		globs := skill.Globs
		if len(globs) == 0 {
			globs = skill.Activation.Globs
		}
		applyTo, globWarn := pickApplyTo(globs, skill.Document)
		fname := "skill-" + scopedSkillSlug(skill.ScopePath, skill.Name) + ".instructions.md"
		path := filepath.ToSlash(filepath.Join(".github", "instructions", fname))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: renderCopilotInstructions(applyTo, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if globWarn != nil {
			op.Warnings = append(op.Warnings, *globWarn)
		}
		if len(skill.Scripts) > 0 {
			src := ""
			if skill.Document != nil {
				src = proj.SourceTag(skill.Document.SourcePath)
			}
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Copilot has no script execution; scripts ignored: %s", strings.Join(skill.Scripts, ", ")),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// 4. Commands → .github/prompts/<name>.prompt.md.
	//
	// Scoped commands get the scope slug in the prompt filename so same-named
	// commands across scopes don't collide. Copilot prompts have no applyTo
	// equivalent, so the scope path is purely a filename + warning concern.
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
		var fname string
		if cmd.ScopePath != "" {
			fname = scopedSkillSlug(cmd.ScopePath, cmd.Name) + ".prompt.md"
		} else {
			fname = cmd.Name + ".prompt.md"
		}
		path := filepath.ToSlash(filepath.Join(".github", "prompts", fname))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: renderCopilotPrompt(cmd.Description, "ask", body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if cmd.ScopePath != "" {
			src := ""
			if len(sources) > 0 {
				src = sources[0]
			}
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   src,
				Message:  fmt.Sprintf("Copilot prompts have no path scoping; scoped command %q (scope: %s) projected without applyTo", cmd.Name, cmd.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// 5. Agents → .github/agents/<name>.agent.md (GA Copilot custom agents).
	//
	// Frontmatter pulls from the source document's existing frontmatter for
	// `tools`, `model`, `agents`, `user-invocable`, `handoffs`, and any other
	// fields the user authored; `name` and `description` come from the
	// model.Agent (which the parser populated from frontmatter or filename).
	// The body is the document body verbatim.
	//
	// Overlap handling: VS Code/Copilot also auto-discovers `.claude/agents/`
	// when that directory exists. To avoid emitting two copies of the same
	// agent, we skip Copilot agent emission entirely when the Claude target
	// is enabled in proj.Config.Targets and just emit an info warning. We do
	// NOT inspect the filesystem for `.claude/agents/` — the canonical signal
	// is the target list (config), and that keeps Plan pure.
	claudeAlsoEnabled := false
	if proj.Config != nil {
		for _, t := range proj.Config.Targets {
			if t == "claude" {
				claudeAlsoEnabled = true
				break
			}
		}
	}
	for _, agent := range proj.Agents {
		if agent == nil {
			continue
		}
		body := ""
		var sources []string
		var srcTag string
		if agent.Document != nil {
			body = agent.Document.Body
			srcTag = proj.SourceTag(agent.Document.SourcePath)
			sources = []string{srcTag}
			for _, inc := range agent.Document.Includes {
				sources = append(sources, proj.SourceTag(inc))
			}
		}
		if claudeAlsoEnabled {
			// Drop the file but keep a trace warning that lands on the
			// first emitted op (see warnings flush below). We synthesize
			// a warning here and attach it via the global warnings bucket.
			// Doing this inside the agent loop keeps per-agent attribution.
			ops = appendAgentOverlapWarning(ops, agent, srcTag)
			continue
		}

		var fname string
		if agent.ScopePath != "" {
			fname = scopedSkillSlug(agent.ScopePath, agent.Name) + ".agent.md"
		} else {
			fname = skillSlug(agent.Name) + ".agent.md"
		}
		path := filepath.ToSlash(filepath.Join(".github", "agents", fname))
		op := plugin.Operation{
			Kind:    plugin.OpWrite,
			Path:    path,
			Content: renderCopilotAgent(agent, body),
			Mode:    plugin.ModeWrite,
			Plugin:  p.Name(),
			Sources: sources,
		}
		if agent.ScopePath != "" {
			op.Warnings = append(op.Warnings, plugin.Warning{
				Source:   srcTag,
				Message:  fmt.Sprintf("Copilot agents have no path scoping; scoped agent %q (scope: %s) projected without applyTo", agent.Name, agent.ScopePath),
				Severity: "info",
			})
		}
		ops = append(ops, op)
	}

	// 6. MCP → .github/mcp.json (merge with existing keys preserved).
	//
	// Schema is the standard {mcpServers: {<name>: {command, args, env, url}}}
	// shape that Copilot, Claude, and the rest of the MCP ecosystem share.
	// The CLI walks from cwd to git root loading every `.mcp.json` it finds
	// (closer wins) — so `.github/mcp.json` at project root is the right
	// target for repo-scoped MCP wiring.
	//
	// Scoped MCP servers are merged into the same map as global ones (Copilot
	// has no per-path MCP) with one info warning per scoped server.
	if hasMCPServers(proj.MCP) {
		mcpOp := buildCopilotMCPOp(proj)
		ops = append(ops, mcpOp)
	}

	// 7. Hooks + Permissions.
	//
	// Both ride the Copilot preview hooks API (.github/hooks/hooks.json);
	// they only emit when EnablePreviewHooks is set. When the flag is off
	// we surface info warnings so the user knows what was dropped.
	var warnings []plugin.Warning

	if p.EnablePreviewHooks {
		hookOps, hookWarnings, err := p.planPreviewHooks(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, hookOps...)
		warnings = append(warnings, hookWarnings...)
	} else {
		for _, hook := range proj.Hooks {
			if hook == nil {
				continue
			}
			eventName := copilotHookEventName(hook)
			msg := fmt.Sprintf("Copilot hooks are in preview and not emitted; %s:%s not projected (pass --enable-preview-hooks to opt in).", eventName, hook.Matcher)
			if hook.ScopePath != "" {
				msg = fmt.Sprintf("Copilot hooks are in preview and not emitted; scoped hook %s:%s (scope: %s) not projected (pass --enable-preview-hooks to opt in).", eventName, hook.Matcher, hook.ScopePath)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   hook.ScriptPath,
				Message:  msg,
				Severity: "info",
			})
		}
		if proj.Permissions != nil {
			if len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0 {
				warnings = append(warnings, plugin.Warning{
					Source:   "permissions.yaml",
					Message:  "Copilot has no permissions primitive without preview hooks; permissions not projected (pass --enable-preview-hooks to opt in).",
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
				Source:   "permissions.yaml",
				Message:  fmt.Sprintf("Copilot has no permissions primitive without preview hooks; scoped permissions (scope: %s) not projected (pass --enable-preview-hooks to opt in).", sp.ScopePath),
				Severity: "info",
			})
		}
	}

	// Attach warnings without a host op to the first emitted op. If no op
	// exists, the warnings have nowhere to land — drop them rather than
	// invent a synthetic op.
	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// appendAgentOverlapWarning attaches an info-warn explaining that a Copilot
// agent file was suppressed because the Claude target is also enabled (so
// VS Code/Copilot will already pick up the agent from `.claude/agents/`).
// The warning lands on the first available op; if no op exists yet we drop
// the warning rather than synthesize a carrier op — same policy used for
// the unsupported-capability warnings below.
func appendAgentOverlapWarning(ops []plugin.Operation, agent *model.Agent, srcTag string) []plugin.Operation {
	msg := fmt.Sprintf("Copilot agent %q not emitted: Claude target also enabled and VS Code/Copilot auto-discovers .claude/agents/", agent.Name)
	if agent.ScopePath != "" {
		msg = fmt.Sprintf("Copilot agent %q (scope: %s) not emitted: Claude target also enabled and VS Code/Copilot auto-discovers .claude/agents/", agent.Name, agent.ScopePath)
	}
	w := plugin.Warning{Source: srcTag, Message: msg, Severity: "info"}
	if len(ops) == 0 {
		return ops // dropped — no carrier
	}
	ops[0].Warnings = append(ops[0].Warnings, w)
	return ops
}

// hasMCPServers reports whether there is at least one non-nil, non-empty
// MCP server in the slice. Used to skip the `.github/mcp.json` op entirely
// when the canonical model has no servers — avoids writing an empty
// `{"mcpServers": {}}` file that would be lockfile-tracked but useless.
func hasMCPServers(servers []*model.MCPServer) bool {
	for _, s := range servers {
		if s != nil && s.Name != "" {
			return true
		}
	}
	return false
}

// pickApplyTo selects a single applyTo glob from a list of canonical globs
// and produces a degradation warning when the list has more than one entry.
// When globs is empty, fall back to "**" (apply to everything) and emit no
// warning — the canonical model has nothing to drop.
//
// The doc argument is used only for warning attribution; nil is fine.
func pickApplyTo(globs []string, doc *model.Document) (string, *plugin.Warning) {
	if len(globs) == 0 {
		return "**", nil
	}
	first := globs[0]
	if len(globs) == 1 {
		return first, nil
	}
	src := ""
	if doc != nil {
		src = doc.SourcePath
	}
	return first, &plugin.Warning{
		Source: src,
		Message: fmt.Sprintf(
			"Copilot's applyTo is single-valued; using first glob of %d: %s; ignoring: %s",
			len(globs), first, strings.Join(globs[1:], ", "),
		),
		Severity: "info",
	}
}

// renderCopilotInstructions formats the YAML frontmatter + body for an
// .instructions.md file. Only `applyTo` appears in frontmatter; `globs` is
// NOT a Copilot key and we deliberately do not emit it. The applyTo value is
// quoted to keep YAML happy with glob characters like `**`.
func renderCopilotInstructions(applyTo, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("applyTo: ")
	b.WriteString(renderYAMLScalar(applyTo))
	b.WriteString("\n")
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderCopilotPrompt formats a .prompt.md file with description + mode
// frontmatter and a markdown body. Description may be empty (we still emit
// the key with an empty string so downstream tools can edit it in place).
func renderCopilotPrompt(description, promptMode, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: ")
	b.WriteString(renderYAMLScalar(description))
	b.WriteString("\n")
	if promptMode != "" {
		b.WriteString("mode: ")
		b.WriteString(promptMode)
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

// renderCopilotAgent formats a `.agent.md` file with frontmatter that
// Copilot's GA custom-agent spec recognizes. Required keys: `name`,
// `description`. Optional keys passed through from the source document's
// frontmatter: `tools`, `model`, `agents` (subagent list), `user-invocable`,
// `handoffs`, and any other scalar/list/map the user authored. The body is
// the agent's system prompt verbatim.
//
// The merge rule: model.Agent's Name and Description override whatever was
// in the source frontmatter (those are the canonical values the parser
// computed), but every other key in agent.Document.Frontmatter is
// pass-through. Keys are emitted in a stable order: name, description, then
// the rest sorted alphabetically — so byte output is deterministic across
// runs even though Go maps aren't.
func renderCopilotAgent(agent *model.Agent, body string) string {
	var b strings.Builder
	b.WriteString("---\n")

	// Canonical keys first.
	b.WriteString("name: ")
	b.WriteString(renderYAMLScalar(agent.Name))
	b.WriteString("\n")
	b.WriteString("description: ")
	b.WriteString(renderYAMLScalar(agent.Description))
	b.WriteString("\n")

	// Merge pass-through frontmatter: source document Frontmatter first,
	// then extensions.copilot.* keys (which win on conflict — they are
	// the explicit per-plugin override slot per SPEC §5.1). Keys are
	// emitted in alpha order so byte output stays deterministic.
	merged := map[string]any{}
	if agent.Document != nil {
		for k, v := range agent.Document.Frontmatter {
			if k == "name" || k == "description" {
				continue
			}
			merged[k] = v
		}
	}
	for k, v := range copilotExtensionKeys(agent.Extensions) {
		merged[k] = v
	}
	if len(merged) > 0 {
		keys := make([]string, 0, len(merged))
		for k := range merged {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(renderYAMLValue(merged[k]))
			b.WriteString("\n")
		}
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

// renderYAMLValue serializes a single frontmatter value into YAML-ish text
// suitable for inline placement after `key: `. Strings are double-quoted
// for safety; numbers and bools are emitted raw; slices become bracketed
// flow-style lists (`[a, b, c]`); maps become flow-style objects
// (`{k: v, ...}`); nil becomes the empty string. This keeps the rendered
// frontmatter on a single line per key, which is what Copilot's agent spec
// shows in its docs and which lets us hash output byte-for-byte for the
// lockfile.
//
// For deeply nested or oddball values we fall back to %v formatting; the
// goal isn't a full YAML emitter (the canonical model already restricts
// what we'll see) but a deterministic round-trip for the common Copilot
// agent fields: `tools: [...]`, `model: "..."`, `user-invocable: true`,
// `handoffs: [...]`, `agents: [...]`.
func renderYAMLValue(v any) string {
	switch x := v.(type) {
	case nil:
		return `""`
	case string:
		return renderYAMLScalar(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		// YAML doesn't distinguish int / float; trim trailing zero if exact int.
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, renderYAMLValue(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, renderYAMLScalar(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s: %s", k, renderYAMLValue(x[k])))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		// Best-effort fallback.
		return renderYAMLScalar(fmt.Sprintf("%v", x))
	}
}

// copilotMCPServerJSON is the schema Copilot expects under `.github/mcp.json`'s
// `mcpServers` map. Identical in shape to Claude's `.mcp.json` schema; the
// MCP ecosystem has converged on this layout.
type copilotMCPServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// buildCopilotMCPOp returns an OpMerge for `.github/mcp.json`. The Merger
// closure parses any existing file (preserving user-authored keys at the
// top level and inside `mcpServers`) and writes a merged document. Plan
// itself does no filesystem I/O — the merger pattern keeps the plugin pure
// and reproducible from `proj` alone, which mirrors the
// `buildSettingsOp` Merger pattern in claude.go (and is the contract the
// engine expects for OpMerge).
//
// Scoped servers are merged into the same map as globals (Copilot has no
// per-path MCP) with one info warning per scoped server, attached to the
// MCP op so `agents which` can trace it.
func buildCopilotMCPOp(proj *model.Project) plugin.Operation {
	const relPath = ".github/mcp.json"

	// Deterministic ordering: take a snapshot of proj.MCP at Plan time so
	// the Merger closure (invoked later by the engine) sees a stable list.
	servers := make([]*model.MCPServer, 0, len(proj.MCP))
	servers = append(servers, proj.MCP...)

	var warnings []plugin.Warning
	for _, srv := range servers {
		if srv == nil || srv.ScopePath == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   srv.ScopePath + "/mcp.yaml",
			Message:  fmt.Sprintf("scoped MCP server %q from %s/mcp.yaml applied project-wide; Copilot has no per-scope MCP", srv.Name, srv.ScopePath),
			Severity: "info",
		})
	}

	merger := func(existing []byte) (string, error) {
		doc := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &doc); err != nil {
				return "", fmt.Errorf("copilot: parsing existing %s: %w", relPath, err)
			}
		}

		serversMap, _ := doc["mcpServers"].(map[string]any)
		if serversMap == nil {
			serversMap = map[string]any{}
		}

		for _, srv := range servers {
			if srv == nil || srv.Name == "" {
				continue
			}
			entry := copilotMCPServerJSON{
				Command: srv.Command,
				Args:    srv.Args,
				Env:     srv.Env,
			}
			if srv.Command == "" && srv.URL != "" {
				entry.URL = srv.URL
			}
			serversMap[srv.Name] = entry
		}
		doc["mcpServers"] = serversMap

		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out) + "\n", nil
	}

	return plugin.Operation{
		Kind:     plugin.OpMerge,
		Path:     relPath,
		Mode:     plugin.ModeWrite,
		Sources:  []string{"mcp.yaml"},
		Plugin:   "copilot",
		Warnings: warnings,
		Merger:   merger,
	}
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *CopilotPlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that CopilotPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*CopilotPlugin)(nil)

// copilotHookEventMap translates prism's canonical Hook.Event values into
// Copilot's preview hook event names (camelCase per the GitHub docs).
// Claude-style PreToolUse/PostToolUse names map cleanly; SessionStart,
// SessionEnd, UserPromptSubmit, Stop, and SubagentStop have direct
// preview-event counterparts (re-cased). Unknown events drop with an info
// warning rather than emit an unrecognized event name.
//
// Reference: https://docs.github.com/en/copilot/reference/hooks-configuration
var copilotHookEventMap = map[string]string{
	"PreToolUse":       "preToolUse",
	"PostToolUse":      "postToolUse",
	"SessionStart":     "sessionStart",
	"SessionEnd":       "sessionEnd",
	"UserPromptSubmit": "userPromptSubmitted",
	"UserPrompt":       "userPromptSubmitted",
	"Stop":             "agentStop",
	"AgentStop":        "agentStop",
	"SubagentStop":     "subagentStop",
	"ErrorOccurred":    "errorOccurred",
	"Notification":     "errorOccurred",
}

// mapCopilotHookEvent returns (event, ok). ok=false means the canonical
// event has no Copilot preview counterpart and the caller should warn-and-drop.
func mapCopilotHookEvent(event string) (string, bool) {
	if mapped, ok := copilotHookEventMap[event]; ok {
		return mapped, true
	}
	// Pass through anything that already looks camelCase Copilot-shaped.
	if event != "" && event[0] >= 'a' && event[0] <= 'z' {
		return event, true
	}
	return "", false
}

// copilotHookEventName picks the v0.8 Hook.Event when populated, otherwise
// falls back to the v2 EventCanonical typed enum (SPEC §4.4.2). The parser
// mirrors both shapes today, but a v2-only producer might populate only
// EventCanonical. Returns "" when neither is set.
func copilotHookEventName(h *model.Hook) string {
	if h == nil {
		return ""
	}
	if h.Event != "" {
		return h.Event
	}
	return string(h.EventCanonical)
}

// copilotExtensionKeys extracts the `extensions.copilot` map from a
// primitive's Extensions slot for verbatim pass-through into emitted
// frontmatter (SPEC §5.1). Returns nil when the namespace is absent or
// not a map (an info warning is the caller's responsibility, not ours).
// Reserved canonical keys (name, description) are filtered so the
// extension cannot override the parser-computed canonical fields.
func copilotExtensionKeys(ext map[string]any) map[string]any {
	if ext == nil {
		return nil
	}
	raw, ok := ext["copilot"]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k == "name" || k == "description" {
			continue
		}
		out[k] = v
	}
	return out
}

// copilotHookEntry mirrors a single hook command entry inside hooks.json.
// Schema is {type: "command", command: "<path>"} matching Claude's contract
// (Copilot picked the same shape for cross-tool compatibility).
type copilotHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// copilotHookGroup is one matcher group inside an event bucket.
type copilotHookGroup struct {
	Matcher string             `json:"matcher,omitempty"`
	Hooks   []copilotHookEntry `json:"hooks"`
}

// planPreviewHooks emits the Copilot preview hook surface: scope-guard
// wrapper scripts (one per scoped hook), perms-guard wrapper + sidecar
// policies (when permissions exist), and the unified .github/hooks/hooks.json
// that wires events to commands. Returns the ops, info warnings for any
// unmappable canonical events, and the first error encountered.
//
// Called only when EnablePreviewHooks is true; callers gate.
func (p *CopilotPlugin) planPreviewHooks(proj *model.Project) ([]plugin.Operation, []plugin.Warning, error) {
	var ops []plugin.Operation
	var warnings []plugin.Warning

	// Scope-guard wrappers for scoped hooks, mirroring claude/cline/gemini.
	wrapperPaths := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" || p.DisableHookWrappers {
			continue
		}
		mappedEvent, ok := mapCopilotHookEvent(copilotHookEventName(h))
		if !ok {
			continue
		}
		hookBase := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		wrapperFile := scopeSlug(h.ScopePath) + "-" + mappedEvent + "-" + hookBase + ".sh"
		wrapperRel := filepath.Join(".github", "hooks", "__scope-guard__", wrapperFile)
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
		wrapperPaths[h] = wrapperRel
	}

	// Perms-guard wrappers + sidecar policies. The helper emits the
	// policy.json sidecar plus either per-hook wrappers (when user
	// hooks exist) or bare gate wrappers (when only permissions exist).
	// Per-hook wrappers replace the raw command in hooks.json below;
	// gate wrappers append as standalone preToolUse entries.
	permsOps, permsWarnings, err := emitPermsGuardWrappersAt(p.Name(), filepath.Join(".github", "hooks"), proj, p.DisableHookWrappers)
	if err != nil {
		return nil, nil, err
	}
	ops = append(ops, permsOps...)
	warnings = append(warnings, permsWarnings...)

	permsHookWrappers := map[*model.Hook]string{}
	for _, h := range proj.Hooks {
		eventName := copilotHookEventName(h)
		if h == nil || eventName == "" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(h.ScriptPath), filepath.Ext(h.ScriptPath))
		var wrapperName string
		if h.ScopePath == "" {
			wrapperName = eventName + "-" + base + ".sh"
		} else {
			wrapperName = permsScopeSlug(h.ScopePath) + "-" + eventName + "-" + base + ".sh"
		}
		wrapperRel := filepath.Join(".github", "hooks", "__perms-guard__", wrapperName)
		for _, op := range permsOps {
			if op.Path == wrapperRel {
				permsHookWrappers[h] = wrapperRel
				break
			}
		}
	}

	var permsGateRefs []string
	for _, op := range permsOps {
		if !strings.Contains(op.Path, "__perms-guard__") {
			continue
		}
		if !strings.HasSuffix(op.Path, "-gate.sh") && !strings.HasSuffix(op.Path, "global-gate.sh") {
			continue
		}
		permsGateRefs = append(permsGateRefs, op.Path)
	}
	sort.Strings(permsGateRefs)

	// Build the hooks.json event buckets.
	buckets := map[string][]copilotHookGroup{}
	eventOrder := []string{}
	matcherOrder := map[string][]string{}
	addEntry := func(event, matcher string, entry copilotHookEntry) {
		if _, ok := buckets[event]; !ok {
			eventOrder = append(eventOrder, event)
		}
		matchers := matcherOrder[event]
		found := false
		for i, g := range buckets[event] {
			if g.Matcher == matcher {
				buckets[event][i].Hooks = append(buckets[event][i].Hooks, entry)
				found = true
				break
			}
		}
		if !found {
			buckets[event] = append(buckets[event], copilotHookGroup{
				Matcher: matcher,
				Hooks:   []copilotHookEntry{entry},
			})
			matcherOrder[event] = append(matchers, matcher)
		}
	}

	for _, h := range proj.Hooks {
		eventName := copilotHookEventName(h)
		if h == nil || eventName == "" {
			continue
		}
		mappedEvent, ok := mapCopilotHookEvent(eventName)
		if !ok {
			warnings = append(warnings, plugin.Warning{
				Source:   proj.SourceTag(h.ScriptPath),
				Message:  fmt.Sprintf("Copilot has no preview event for %q; hook not projected.", eventName),
				Severity: "info",
			})
			continue
		}
		var cmdPath string
		switch {
		case wrapperPaths[h] != "":
			cmdPath = "${PROJECT_DIR}/" + filepath.ToSlash(wrapperPaths[h])
		case permsHookWrappers[h] != "":
			cmdPath = "${PROJECT_DIR}/" + filepath.ToSlash(permsHookWrappers[h])
		case filepath.IsAbs(h.ScriptPath):
			if rel, rerr := filepath.Rel(proj.Root, h.ScriptPath); rerr == nil && !strings.HasPrefix(rel, "..") {
				cmdPath = "${PROJECT_DIR}/" + filepath.ToSlash(rel)
			} else {
				cmdPath = h.ScriptPath
			}
		default:
			cmdPath = "${PROJECT_DIR}/" + filepath.ToSlash(h.ScriptPath)
		}
		addEntry(mappedEvent, h.Matcher, copilotHookEntry{Type: "command", Command: cmdPath})
	}

	// Wire perms-guard gate wrappers into preToolUse, so every tool call
	// flows through the policy. Emitted regardless of whether user hooks
	// already populate preToolUse — the entries run in document order.
	for _, gate := range permsGateRefs {
		addEntry("preToolUse", "", copilotHookEntry{
			Type:    "command",
			Command: "${PROJECT_DIR}/" + filepath.ToSlash(gate),
		})
	}

	// No hooks to emit? Return any wrappers/policies + warnings; skip the
	// hooks.json file. (perms-guard policies might still exist as standalone
	// sidecars in this case — the gate wrappers were appended above so we
	// will have entries to wire.)
	if len(eventOrder) == 0 {
		return ops, warnings, nil
	}

	sort.Strings(eventOrder)
	for event, groups := range buckets {
		matchers := append([]string(nil), matcherOrder[event]...)
		sort.Strings(matchers)
		sorted := make([]copilotHookGroup, 0, len(groups))
		for _, m := range matchers {
			for _, g := range groups {
				if g.Matcher == m {
					sorted = append(sorted, g)
					break
				}
			}
		}
		buckets[event] = sorted
	}

	doc := map[string]any{
		"version": 1,
		"hooks":   buckets,
	}
	content, jerr := json.MarshalIndent(doc, "", "  ")
	if jerr != nil {
		return nil, nil, fmt.Errorf("copilot: marshal hooks.json: %w", jerr)
	}

	sources := []string{}
	if len(proj.Hooks) > 0 {
		sources = append(sources, "hooks.yaml")
	}
	if len(permsGateRefs) > 0 {
		sources = append(sources, "permissions.yaml")
	}
	if len(sources) == 0 {
		sources = []string{"hooks.yaml"}
	}

	ops = append(ops, plugin.Operation{
		Kind:    plugin.OpWrite,
		Path:    filepath.Join(".github", "hooks", "hooks.json"),
		Content: string(content) + "\n",
		Mode:    plugin.ModeWrite,
		Plugin:  p.Name(),
		Sources: sources,
	})

	return ops, warnings, nil
}
