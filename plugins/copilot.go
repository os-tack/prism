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
)

// CopilotPlugin projects Project state into `.github/` files Copilot reads.
type CopilotPlugin struct{}

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

// Capabilities returns Copilot's capability matrix.
//
// Copilot natively supports a repo-wide instructions file (Context), per-glob
// instruction attachment (ScopePaths), prompt files / slash commands
// (Commands), custom agents via `.github/agents/<name>.agent.md` (Agents,
// GA as of 2026), and project-local MCP via `.github/mcp.json` (MCP).
//
// ScopeSemantic stays degraded — `applyTo` is glob-only, with no
// description-driven trigger. Skills degrade to scoped instructions. Hooks
// are SupportUnsupported pending the public-preview hook surface; a future
// `--enable-preview-hooks` flag can flip this. Permissions have no Copilot
// analog.
func (p *CopilotPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,
		ScopeSemantic: plugin.SupportDegraded,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportNative,
		Agents:        plugin.SupportNative,
		Hooks:         plugin.SupportUnsupported, // preview; defer
		Permissions:   plugin.SupportUnsupported,
		MCP:           plugin.SupportNative,
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
		applyTo, globWarn := pickApplyTo(skill.Globs, skill.Document)
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

	// 7. Capability gaps that produce no files: collect warnings.
	var warnings []plugin.Warning
	for _, hook := range proj.Hooks {
		if hook == nil {
			continue
		}
		msg := fmt.Sprintf("Copilot hooks are in preview and not emitted; %s:%s not projected.", hook.Event, hook.Matcher)
		if hook.ScopePath != "" {
			msg = fmt.Sprintf("Copilot hooks are in preview and not emitted; scoped hook %s:%s (scope: %s) not projected.", hook.Event, hook.Matcher, hook.ScopePath)
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
				Message:  "Copilot has no permissions primitive; permissions not projected.",
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
			Message:  fmt.Sprintf("Copilot has no permissions primitive; scoped permissions (scope: %s) not projected.", sp.ScopePath),
			Severity: "info",
		})
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

	// Pass-through everything else from the source frontmatter, alpha order.
	if agent.Document != nil && agent.Document.Frontmatter != nil {
		fm := agent.Document.Frontmatter
		keys := make([]string, 0, len(fm))
		for k := range fm {
			if k == "name" || k == "description" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(renderYAMLValue(fm[k]))
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

// Compile-time check that CopilotPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*CopilotPlugin)(nil)
