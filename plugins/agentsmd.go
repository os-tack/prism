package plugins

import (
	"fmt"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/internal/version"
)

// AgentsMDPlugin projects a model.Project into a single AGENTS.md file at the
// project root. AGENTS.md is the flat-file cross-tool standard read by Cursor,
// Codex, recent Claude Code, Aider, and others. There is no native scoping
// support — the file is always loaded in its entirety — so scopes are emitted
// as documented section headers that smarter readers can use to skip
// irrelevant material.
//
// Beyond context and scopes, the plugin also documents Skills, Slash commands,
// Subagents, Hooks, Permissions, and MCP servers as plain markdown sections.
// AGENTS.md-only tools cannot execute any of these — every such section is
// informational and carries a degradation warning.
type AgentsMDPlugin struct{}

// NewAgentsMD returns a fresh AgentsMDPlugin.
//
// Named NewAgentsMD rather than New because the plugins package has multiple
// plugin constructors and Go does not allow function-name overloading.
func NewAgentsMD() *AgentsMDPlugin {
	return &AgentsMDPlugin{}
}

// Name is the stable plugin identifier.
func (p *AgentsMDPlugin) Name() string {
	return "agents-md"
}

// Detect always returns true: most modern coding agents read AGENTS.md, and we
// produce it as the safest fallback regardless of which other plugins are
// active.
func (p *AgentsMDPlugin) Detect(root string) bool {
	return true
}

// Capabilities returns the capability matrix entry for AGENTS.md.
//
// Coarse v0.8 cells (kept for backward compatibility): everything beyond
// Context is degraded — scopes lose enforcement (loaded always), and
// skills/commands/agents/hooks/permissions/MCP are all rendered as
// informational text where they have any presence at all.
//
// v2 per-field cells (SPEC §12 AGENTS.md column, `agm`): refined to match
// what AGENTS.md actually projects.
//
//   - Skills:      ALL silent — AGENTS.md describes skill prose but the
//     individual fields (Name/Description/Activation/...)
//     have no AGENTS.md-side semantic to map onto. The
//     whole primitive is dropped at the semantic layer
//     (text body survives, but is unparseable as a skill).
//     Supported=false; "*"=FieldSilent.
//   - MCPServer:   ALL unsupported — AGENTS.md cannot wire MCP at all.
//     Supported=false; "*"=FieldUnsupported.
//   - Hooks:       ALL unsupported — AGENTS.md cannot execute hooks.
//     Supported=false; "*"=FieldUnsupported.
//   - Agent:       mostly unsupported, except SystemPrompt (degraded —
//     body prose) and Extensions[plugin] (native pass-through).
//   - Command:     Name/Description/ScopePath degraded (rendered as text);
//     all other invocation-shape fields silent;
//     Extensions[plugin] native.
//   - Permissions: all buckets and targets degraded (informational text
//     only); Extensions[plugin] native.
//   - Scope:       Path cascade native (the only plugin that nests
//     AGENTS.md per directory); Name/Description/Priority/
//     Tags degraded; Globs and non-cascade activation modes
//     unsupported; IsOverride native (the only plugin that
//     round-trips AGENTS.override.md). Tags is degraded —
//     surfaced as a sub-heading per SPEC §12 footnote 8.
//
// Extensions: lists "agentsmd" as the namespace this plugin owns under
// `extensions.<name>:`. AGENTS.md v0.9 has no frontmatter, so the
// passthrough is currently a no-op — v1.1 (SPEC §4.7.4 last row) will
// add frontmatter for Tags and Description, at which point this
// namespace will start emitting data.
func (p *AgentsMDPlugin) Capabilities() plugin.Capabilities {
	const ext = "agentsmd"

	return plugin.Capabilities{
		// v0.8 coarse cells — unchanged from prior shape so existing
		// callers and the existing test continue to observe the same
		// values.
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportDegraded, // becomes documented sections; loaded always
		ScopeSemantic: plugin.SupportDegraded,
		Skills:        plugin.SupportDegraded,
		Commands:      plugin.SupportDegraded,
		Agents:        plugin.SupportDegraded,
		Hooks:         plugin.SupportDegraded,
		Permissions:   plugin.SupportDegraded,
		MCP:           plugin.SupportDegraded,

		// v2 per-field cells (SPEC §12 AGENTS.md column).

		// Skill: whole primitive silent.
		SkillFields: plugin.FieldCapabilities{
			Supported:  false,
			Fields:     map[string]plugin.FieldSupport{"*": plugin.FieldSilent},
			Extensions: []string{ext},
		},

		// MCPServer: whole primitive unsupported.
		MCPServerFields: plugin.FieldCapabilities{
			Supported:  false,
			Fields:     map[string]plugin.FieldSupport{"*": plugin.FieldUnsupported},
			Extensions: []string{ext},
		},

		// Hook: whole primitive unsupported.
		HookFields: plugin.FieldCapabilities{
			Supported:  false,
			Fields:     map[string]plugin.FieldSupport{"*": plugin.FieldUnsupported},
			Extensions: []string{ext},
		},

		// Agent: mostly unsupported; SystemPrompt degrades to prose;
		// Extensions native (round-trips opaque blob).
		AgentFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"*":                  plugin.FieldUnsupported,
				"SystemPrompt":       plugin.FieldDegraded,
				"ScopePath":          plugin.FieldUnsupported,
				"Extensions[plugin]": plugin.FieldNative,
			},
			Extensions: []string{ext},
		},

		// Command: Name/Description/ScopePath rendered as text;
		// invocation-shape fields silent; Extensions native.
		CommandFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Name":               plugin.FieldDegraded,
				"Description":        plugin.FieldDegraded,
				"ArgumentHint":       plugin.FieldSilent,
				"Arguments":          plugin.FieldSilent,
				"Model":              plugin.FieldSilent,
				"Tools":              plugin.FieldSilent,
				"Agent":              plugin.FieldSilent,
				"AutoInvoke":         plugin.FieldSilent,
				"ScopePath":          plugin.FieldDegraded,
				"Extensions[plugin]": plugin.FieldNative,
			},
			Extensions: []string{ext},
		},

		// Permissions: every bucket and target is degraded (text only);
		// Extensions native.
		PermissionsFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Allow":              plugin.FieldDegraded,
				"Deny":               plugin.FieldDegraded,
				"Ask":                plugin.FieldDegraded,
				"AllowScoped":        plugin.FieldDegraded,
				"DenyScoped":         plugin.FieldDegraded,
				"AskScoped":          plugin.FieldDegraded,
				"bash":               plugin.FieldDegraded,
				"Edit/Read/Write":    plugin.FieldDegraded,
				"fs":                 plugin.FieldDegraded,
				"network":            plugin.FieldDegraded,
				"mcp":                plugin.FieldDegraded,
				"**":                 plugin.FieldDegraded,
				"!":                  plugin.FieldDegraded,
				"Extensions[plugin]": plugin.FieldNative,
			},
			Extensions: []string{ext},
		},

		// Scope: cascade native; IsOverride native (round-trips to
		// AGENTS.override.md); descriptive metadata degraded; globs and
		// non-cascade activation modes unsupported.
		ScopeFields: plugin.FieldCapabilities{
			Supported: true,
			Fields: map[string]plugin.FieldSupport{
				"Path":                 plugin.FieldNative,
				"Path==\"\"":           plugin.FieldNative,
				"Name":                 plugin.FieldDegraded,
				"Description":          plugin.FieldDegraded,
				"Globs":                plugin.FieldUnsupported,
				"Activation=Always":    plugin.FieldDegraded,
				"Activation=Cascade":   plugin.FieldNative,
				"Activation=Glob":      plugin.FieldUnsupported,
				"Activation=Manual":    plugin.FieldUnsupported,
				"Activation=ModelDec.": plugin.FieldUnsupported,
				"Priority":             plugin.FieldDegraded,
				"Tags":                 plugin.FieldDegraded,
				"IsOverride":           plugin.FieldNative,
				"Extensions[plugin]":   plugin.FieldNative,
			},
			Extensions: []string{ext},
		},
	}
}

// generatedHeader is the banner emitted at the top of every AGENTS.md so
// readers (human and machine) know the file is derived from .agents/.
const generatedHeader = "<!-- Generated by agents. Do not edit by hand; edit .agents/ instead. -->"

// Plan produces a single Operation that writes AGENTS.md at the project root.
//
// Section order (each is skipped when its source slice/map is empty):
//
//  1. Generated-by header
//  2. Root context body
//  3. Scopes (## When working in <path>)
//  4. Skills (## Skills, with ### <name> entries)
//  5. Slash commands (## Slash commands, with ### /<name> entries)
//  6. Subagents (## Subagents, with ### @<name> entries)
//  7. Hooks (## Hooks, bulleted)
//  8. Permissions (## Permissions, allow/deny/ask lines)
//  9. MCP servers (## MCP servers, bulleted)
//
// Scopes, skills, commands, agents, and MCP servers are sorted by Name/Path
// so the output is byte-stable across runs.
func (p *AgentsMDPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	// Validate Mode early — AGENTS.md is always write mode, no symlink option.
	// Empty string means "default", which we treat as write.
	if opts.Mode != "" && opts.Mode != "write" {
		return nil, fmt.Errorf("agents-md: unknown mode %q (want \"write\")", opts.Mode)
	}

	var sources []string
	var warnings []plugin.Warning
	// overrideOps collects per-scope AGENTS.override.md emissions for scopes
	// with IsOverride==true && Path!="". SPEC §4.7.4 marks AGENTS.md as the
	// only plugin that round-trips IsOverride natively, via this filename
	// split. The override body replaces — rather than augments — the parent
	// guidance, so it lives in its own file at the scope path rather than
	// being inlined into the root AGENTS.md.
	var overrideOps []plugin.Operation

	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n")

	// 1. Root context body.
	if proj.Context != nil {
		body := strings.TrimSpace(proj.Context.Body)
		if body != "" {
			b.WriteString("\n")
			b.WriteString(body)
			b.WriteString("\n")
		}
		if src := relAgentsMDSource(proj, proj.Context.SourcePath); src != "" {
			sources = append(sources, src)
		}
	}

	// 2. Scopes. Sort by Path for stable, lexicographic output.
	scopes := make([]*model.Scope, 0, len(proj.Scopes))
	for _, sc := range proj.Scopes {
		if sc == nil || sc.Document == nil {
			continue
		}
		scopes = append(scopes, sc)
	}
	sort.SliceStable(scopes, func(i, j int) bool {
		return scopes[i].Path < scopes[j].Path
	})

	for _, sc := range scopes {
		src := relAgentsMDSource(proj, sc.Document.SourcePath)

		// v2 IsOverride round-trip (SPEC §4.7.4): AGENTS.md is the only
		// plugin that honors IsOverride natively, by emitting the scope body
		// to a sibling `AGENTS.override.md` file at the scope path instead
		// of inlining a `## When working in <path>` section in the root
		// AGENTS.md. Per SPEC, the override file REPLACES — rather than
		// augments — the parent guidance.
		//
		// Edge case: IsOverride with Path=="" is meaningless (there is no
		// parent to override at the root). Emit an info warning and fall
		// through to the regular inline-section emission below.
		if sc.IsOverride && sc.Path != "" {
			body := strings.TrimSpace(sc.Document.Body)
			var ob strings.Builder
			ob.WriteString(generatedHeader)
			ob.WriteString("\n")
			if body != "" {
				ob.WriteString("\n")
				ob.WriteString(body)
				ob.WriteString("\n")
			}

			overrideSources := []string{}
			if src != "" {
				overrideSources = append(overrideSources, src)
			}

			overrideOps = append(overrideOps, plugin.Operation{
				Kind:    plugin.OpWrite,
				Path:    sc.Path + "/AGENTS.override.md",
				Content: ob.String(),
				Mode:    plugin.ModeWrite,
				Sources: overrideSources,
				Plugin:  "agents-md",
			})

			// Source still belongs in the root op's source list for
			// provenance bookkeeping.
			if src != "" {
				sources = append(sources, src)
			}
			continue
		}

		if sc.IsOverride && sc.Path == "" {
			warnings = append(warnings, plugin.Warning{
				Source:   src,
				Message:  "IsOverride on root scope has no parent to override; emitting to root AGENTS.md as a regular section.",
				Severity: "info",
			})
		}

		// Separator between root context (or previous scope) and this scope.
		b.WriteString("\n---\n\n")
		b.WriteString("## When working in ")
		b.WriteString(sc.Path)
		b.WriteString("\n\n")

		// Trigger + description blockquote. Always include the triggers line
		// (even with empty globs list) so the section shape is predictable;
		// description is optional on its own line.
		//
		// v2 additive read: when the v0.8 Description is empty but the v2
		// Scope.Name is set (SPEC §4.7.2), use Name as the description
		// fallback. Strictly additive — existing v0.8 sources with non-empty
		// Description observe identical output.
		b.WriteString("> Triggers: ")
		b.WriteString(strings.Join(sc.Globs, ", "))
		b.WriteString("\n")
		desc := strings.TrimSpace(sc.Description)
		if desc == "" {
			desc = strings.TrimSpace(sc.Name)
		}
		if desc != "" {
			b.WriteString("> ")
			b.WriteString(desc)
			b.WriteString("\n")
		}
		b.WriteString("\n")

		body := strings.TrimSpace(sc.Document.Body)
		if body != "" {
			b.WriteString(body)
			b.WriteString("\n")
		}

		if src != "" {
			sources = append(sources, src)
		}

		// AGENTS.md has no native scope enforcement — note this on every
		// scope. The message names every degraded Scope field (Name,
		// Description, Priority, Tags) so SPEC §12 per-field contract tests
		// see the field-name mention they require.
		warnings = append(warnings, plugin.Warning{
			Source:   src,
			Message:  "scope Name, Description, Priority, and Tags rendered as text only — AGENTS.md has no scope enforcement (loaded always).",
			Severity: "info",
		})
	}

	// 3. Skills (global only — scoped skills go under their per-scope
	// capability section below). Sorted by Name.
	skills := make([]*model.Skill, 0, len(proj.Skills))
	for _, s := range proj.Skills {
		if s == nil || s.ScopePath != "" {
			continue
		}
		skills = append(skills, s)
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	if len(skills) > 0 {
		b.WriteString("\n---\n\n")
		b.WriteString("## Skills\n\n")
		for _, s := range skills {
			b.WriteString("### ")
			b.WriteString(s.Name)
			b.WriteString("\n\n")

			// Lead blockquote: description (or trigger fallback), globs,
			// script count if present.
			tagline := strings.TrimSpace(s.Description)
			if tagline == "" {
				tagline = strings.TrimSpace(s.Trigger)
			}
			if tagline != "" {
				b.WriteString("> ")
				b.WriteString(tagline)
				b.WriteString("\n")
			}
			if len(s.Globs) > 0 {
				b.WriteString("> Triggers: ")
				b.WriteString(strings.Join(s.Globs, ", "))
				b.WriteString("\n")
			}
			if len(s.Scripts) > 0 {
				fmt.Fprintf(&b, "> Scripts: %d (not executable from AGENTS.md-only tools)\n", len(s.Scripts))
			}
			b.WriteString("\n")

			if s.Document != nil {
				body := strings.TrimSpace(s.Document.Body)
				if body != "" {
					b.WriteString(body)
					b.WriteString("\n\n")
				}
				if src := relAgentsMDSource(proj, s.Document.SourcePath); src != "" {
					sources = append(sources, src)
				}
			}

			src := ""
			if s.Document != nil {
				src = relAgentsMDSource(proj, s.Document.SourcePath)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   src,
				Message:  "loaded always — AGENTS.md has no skill scope enforcement; scripts cannot execute.",
				Severity: "info",
			})
		}
	}

	// 4. Slash commands (global only — scoped commands go under their
	// per-scope capability section below). Sorted by Name.
	commands := make([]*model.Command, 0, len(proj.Commands))
	for _, c := range proj.Commands {
		if c == nil || c.ScopePath != "" {
			continue
		}
		commands = append(commands, c)
	}
	sort.SliceStable(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})
	if len(commands) > 0 {
		b.WriteString("\n---\n\n")
		b.WriteString("## Slash commands\n\n")
		for _, c := range commands {
			b.WriteString("### /")
			b.WriteString(c.Name)
			b.WriteString("\n\n")

			if desc := strings.TrimSpace(c.Description); desc != "" {
				b.WriteString("> ")
				b.WriteString(desc)
				b.WriteString("\n")
			}
			b.WriteString("\n")

			if c.Document != nil {
				body := strings.TrimSpace(c.Document.Body)
				if body != "" {
					b.WriteString(body)
					b.WriteString("\n\n")
				}
				if src := relAgentsMDSource(proj, c.Document.SourcePath); src != "" {
					sources = append(sources, src)
				}
			}

			src := ""
			if c.Document != nil {
				src = relAgentsMDSource(proj, c.Document.SourcePath)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   src,
				Message:  "slash command rendered as prose: Name, Description, and ScopePath emitted as documentation; cannot invoke as a command.",
				Severity: "info",
			})
		}
	}

	// 5. Subagents (global only — scoped agents go under their per-scope
	// capability section below). Sorted by Name.
	agents := make([]*model.Agent, 0, len(proj.Agents))
	for _, a := range proj.Agents {
		if a == nil || a.ScopePath != "" {
			continue
		}
		agents = append(agents, a)
	}
	sort.SliceStable(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})
	if len(agents) > 0 {
		b.WriteString("\n---\n\n")
		b.WriteString("## Subagents\n\n")
		for _, a := range agents {
			b.WriteString("### @")
			b.WriteString(a.Name)
			b.WriteString("\n\n")

			if desc := strings.TrimSpace(a.Description); desc != "" {
				b.WriteString("> ")
				b.WriteString(desc)
				b.WriteString("\n")
			}
			b.WriteString("\n")

			// v2 additive read: when the v0.8 Document body is empty (or
			// the Document is nil) but the canonical v2 Agent.SystemPrompt
			// is set, render SystemPrompt as the body. Strictly additive —
			// SPEC §12 marks SystemPrompt as D for agm and existing v0.8
			// sources with non-empty Document.Body see identical output.
			rendered := false
			if a.Document != nil {
				body := strings.TrimSpace(a.Document.Body)
				if body != "" {
					b.WriteString(body)
					b.WriteString("\n\n")
					rendered = true
				}
				if src := relAgentsMDSource(proj, a.Document.SourcePath); src != "" {
					sources = append(sources, src)
				}
			}
			if !rendered {
				if sp := strings.TrimSpace(a.SystemPrompt); sp != "" {
					b.WriteString(sp)
					b.WriteString("\n\n")
				}
			}

			src := ""
			if a.Document != nil {
				src = relAgentsMDSource(proj, a.Document.SourcePath)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   src,
				Message:  "documented as text; AGENTS.md-only tools cannot dispatch to subagents.",
				Severity: "info",
			})
			// Sub-agent SystemPrompt is SPEC §12 D for agm — surfaces as
			// body prose but no native dispatch.
			warnings = append(warnings, plugin.Warning{
				Source:   src,
				Message:  "sub-agent SystemPrompt rendered as body prose; not natively dispatchable.",
				Severity: "info",
			})
		}
	}

	// 6. Hooks (global only — scoped hooks go under their per-scope
	// capability section below). Preserve declaration order — events fire in
	// event-order, not alphabetical, and reshuffling would mislead readers.
	hooks := make([]*model.Hook, 0, len(proj.Hooks))
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath != "" {
			continue
		}
		hooks = append(hooks, h)
	}
	if len(hooks) > 0 {
		b.WriteString("\n---\n\n")
		b.WriteString("## Hooks\n\n")
		b.WriteString("Documented for transparency; AGENTS.md-only tools cannot execute hooks.\n\n")
		for _, h := range hooks {
			b.WriteString("- **")
			b.WriteString(h.Event)
			b.WriteString("**")
			if m := strings.TrimSpace(h.Matcher); m != "" {
				b.WriteString(" matcher `")
				b.WriteString(m)
				b.WriteString("`")
			}
			b.WriteString(": ")
			b.WriteString(h.ScriptPath)
			b.WriteString("\n")

			if src := relAgentsMDSource(proj, h.ScriptPath); src != "" {
				sources = append(sources, src)
			}
			warnings = append(warnings, plugin.Warning{
				Source:   h.ScriptPath,
				Message:  "documented as text; AGENTS.md-only tools cannot execute hooks.",
				Severity: "info",
			})
		}
	}

	// 7. Permissions. Only emit a section if at least one list is non-empty;
	// an empty Permissions struct contributes no useful content.
	if proj.Permissions != nil && (len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0) {
		b.WriteString("\n---\n\n")
		b.WriteString("## Permissions\n\n")
		b.WriteString("Documented for transparency; AGENTS.md-only tools cannot enforce permissions.\n\n")
		if len(proj.Permissions.Allow) > 0 {
			b.WriteString("- Allow: ")
			b.WriteString(strings.Join(proj.Permissions.Allow, ", "))
			b.WriteString("\n")
		}
		if len(proj.Permissions.Deny) > 0 {
			b.WriteString("- Deny: ")
			b.WriteString(strings.Join(proj.Permissions.Deny, ", "))
			b.WriteString("\n")
		}
		if len(proj.Permissions.Ask) > 0 {
			b.WriteString("- Ask: ")
			b.WriteString(strings.Join(proj.Permissions.Ask, ", "))
			b.WriteString("\n")
		}

		// Permissions.yaml is the canonical source. Include it if the engine
		// has populated permissions and the file exists at the conventional
		// path; we don't have a SourcePath field on Permissions so we add
		// the conventional name relative to .agents/.
		sources = append(sources, "permissions.yaml")
		warnings = append(warnings, plugin.Warning{
			Source:   "permissions.yaml",
			Message:  "Permissions Allow/Deny/Ask documented as text; AGENTS.md-only tools cannot enforce permissions.",
			Severity: "info",
		})
	}

	// 8. MCP servers (global only — scoped MCP servers go under their
	// per-scope capability section below). Sorted by Name for stable output.
	mcps := make([]*model.MCPServer, 0, len(proj.MCP))
	for _, m := range proj.MCP {
		if m == nil || m.ScopePath != "" {
			continue
		}
		mcps = append(mcps, m)
	}
	sort.SliceStable(mcps, func(i, j int) bool {
		return mcps[i].Name < mcps[j].Name
	})
	if len(mcps) > 0 {
		b.WriteString("\n---\n\n")
		b.WriteString("## MCP servers\n\n")
		b.WriteString("Documented for transparency. Configure these in your tool's MCP setup.\n\n")
		for _, m := range mcps {
			b.WriteString("- **")
			b.WriteString(m.Name)
			b.WriteString("**: ")
			if u := strings.TrimSpace(m.URL); u != "" {
				b.WriteString("`")
				b.WriteString(u)
				b.WriteString("`")
			} else {
				b.WriteString("`")
				b.WriteString(m.Command)
				if len(m.Args) > 0 {
					b.WriteString(" ")
					b.WriteString(strings.Join(m.Args, " "))
				}
				b.WriteString("`")
			}
			b.WriteString("\n")

			warnings = append(warnings, plugin.Warning{
				Source:   "mcp.yaml",
				Message:  "documented as text; AGENTS.md-only tools must configure MCP separately.",
				Severity: "info",
			})
		}
		// mcp.yaml is the canonical source for the whole block.
		sources = append(sources, "mcp.yaml")
	}

	// 9. Scoped capabilities. For each scope that has at least one scoped
	// capability (skill / command / agent / hook / permission / MCP server),
	// emit a "## Capabilities for scope: <path>" section after the existing
	// capability sections. AGENTS.md has no native scope enforcement, so each
	// scoped entry also carries an info warning so users know the capability
	// is loaded always at the consumer side.
	scopedSrcs, scopedWarns := renderAgentsMDScopedCapabilities(&b, proj)
	sources = append(sources, scopedSrcs...)
	warnings = append(warnings, scopedWarns...)

	op := plugin.Operation{
		Kind:     plugin.OpWrite,
		Path:     "AGENTS.md",
		Content:  b.String(),
		Mode:     plugin.ModeWrite,
		Sources:  sources,
		Plugin:   "agents-md",
		Warnings: warnings,
	}

	ops := []plugin.Operation{op}
	ops = append(ops, overrideOps...)
	return ops, nil
}

// scopedCapsBundle groups every scoped capability that targets the same
// ScopePath. Used internally by renderAgentsMDScopedCapabilities.
type scopedCapsBundle struct {
	Skills   []*model.Skill
	Commands []*model.Command
	Agents   []*model.Agent
	Hooks    []*model.Hook
	Perms    *model.Permissions
	MCP      []*model.MCPServer
}

// renderAgentsMDScopedCapabilities groups every scoped capability by ScopePath
// and emits a `## Capabilities for scope: <path>` section per non-empty
// bundle. The section header is followed by a `> Triggers: <globs>` line when
// the project has a *Scope with matching Path (so users can see the glob
// definition without leaving AGENTS.md), then subsection headers (### Skills,
// ### Commands, ### Subagents, ### Hooks, ### Permissions, ### MCP servers)
// for the categories that actually have entries.
//
// Returns the .agents/-relative source paths and per-entry info warnings to
// merge into the parent op.
func renderAgentsMDScopedCapabilities(b *strings.Builder, proj *model.Project) ([]string, []plugin.Warning) {
	bundles := map[string]*scopedCapsBundle{}
	get := func(scopePath string) *scopedCapsBundle {
		bn, ok := bundles[scopePath]
		if !ok {
			bn = &scopedCapsBundle{}
			bundles[scopePath] = bn
		}
		return bn
	}

	for _, s := range proj.Skills {
		if s == nil || s.ScopePath == "" {
			continue
		}
		get(s.ScopePath).Skills = append(get(s.ScopePath).Skills, s)
	}
	for _, c := range proj.Commands {
		if c == nil || c.ScopePath == "" {
			continue
		}
		get(c.ScopePath).Commands = append(get(c.ScopePath).Commands, c)
	}
	for _, a := range proj.Agents {
		if a == nil || a.ScopePath == "" {
			continue
		}
		get(a.ScopePath).Agents = append(get(a.ScopePath).Agents, a)
	}
	for _, h := range proj.Hooks {
		if h == nil || h.ScopePath == "" {
			continue
		}
		get(h.ScopePath).Hooks = append(get(h.ScopePath).Hooks, h)
	}
	for _, sp := range proj.ScopedPermissions {
		if sp == nil || sp.ScopePath == "" {
			continue
		}
		if len(sp.Allow) == 0 && len(sp.Deny) == 0 && len(sp.Ask) == 0 {
			continue
		}
		// At most one per scope; later entries (if any) overwrite. Parser is
		// expected to produce one block per scope path.
		get(sp.ScopePath).Perms = sp
	}
	for _, m := range proj.MCP {
		if m == nil || m.ScopePath == "" {
			continue
		}
		get(m.ScopePath).MCP = append(get(m.ScopePath).MCP, m)
	}

	if len(bundles) == 0 {
		return nil, nil
	}

	// Build a map from scope path → *Scope so we can read globs for the
	// "> Triggers:" line. Missing entries are fine; we just omit the line.
	scopeIdx := map[string]*model.Scope{}
	for _, sc := range proj.Scopes {
		if sc == nil {
			continue
		}
		scopeIdx[sc.Path] = sc
	}

	paths := make([]string, 0, len(bundles))
	for p := range bundles {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var sources []string
	var warnings []plugin.Warning

	for _, scopePath := range paths {
		bn := bundles[scopePath]
		b.WriteString("\n---\n\n")
		b.WriteString("## Capabilities for scope: ")
		b.WriteString(scopePath)
		b.WriteString("\n\n")

		if sc, ok := scopeIdx[scopePath]; ok && len(sc.Globs) > 0 {
			b.WriteString("> Triggers: ")
			b.WriteString(strings.Join(sc.Globs, ", "))
			b.WriteString("\n\n")
		}

		// ### Skills
		if len(bn.Skills) > 0 {
			sks := append([]*model.Skill(nil), bn.Skills...)
			sort.SliceStable(sks, func(i, j int) bool { return sks[i].Name < sks[j].Name })
			b.WriteString("### Skills\n\n")
			for _, s := range sks {
				b.WriteString("- ")
				b.WriteString(s.Name)
				if desc := strings.TrimSpace(s.Description); desc != "" {
					b.WriteString(": ")
					b.WriteString(desc)
				}
				b.WriteString("\n")

				src := ""
				if s.Document != nil {
					src = relAgentsMDSource(proj, s.Document.SourcePath)
					if src != "" {
						sources = append(sources, src)
					}
				}
				warnings = append(warnings, plugin.Warning{
					Source:   src,
					Message:  fmt.Sprintf("loaded always \u2014 AGENTS.md has no scope enforcement; scoped skill %q (scope: %s) listed as documentation only.", s.Name, scopePath),
					Severity: "info",
				})
			}
			b.WriteString("\n")
		}

		// ### Commands
		if len(bn.Commands) > 0 {
			cmds := append([]*model.Command(nil), bn.Commands...)
			sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
			b.WriteString("### Commands\n\n")
			for _, c := range cmds {
				b.WriteString("- /")
				b.WriteString(c.Name)
				if desc := strings.TrimSpace(c.Description); desc != "" {
					b.WriteString(": ")
					b.WriteString(desc)
				}
				b.WriteString("\n")

				src := ""
				if c.Document != nil {
					src = relAgentsMDSource(proj, c.Document.SourcePath)
					if src != "" {
						sources = append(sources, src)
					}
				}
				warnings = append(warnings, plugin.Warning{
					Source:   src,
					Message:  fmt.Sprintf("loaded always \u2014 AGENTS.md has no scope enforcement; scoped command /%s ScopePath=%s listed as documentation only.", c.Name, scopePath),
					Severity: "info",
				})
			}
			b.WriteString("\n")
		}

		// ### Subagents
		if len(bn.Agents) > 0 {
			ags := append([]*model.Agent(nil), bn.Agents...)
			sort.SliceStable(ags, func(i, j int) bool { return ags[i].Name < ags[j].Name })
			b.WriteString("### Subagents\n\n")
			for _, a := range ags {
				b.WriteString("- @")
				b.WriteString(a.Name)
				if desc := strings.TrimSpace(a.Description); desc != "" {
					b.WriteString(": ")
					b.WriteString(desc)
				}
				b.WriteString("\n")

				src := ""
				if a.Document != nil {
					src = relAgentsMDSource(proj, a.Document.SourcePath)
					if src != "" {
						sources = append(sources, src)
					}
				}
				warnings = append(warnings, plugin.Warning{
					Source:   src,
					Message:  fmt.Sprintf("loaded always \u2014 AGENTS.md cannot enforce scoped sub-agent ScopePath; scoped agent @%s ScopePath=%s dropped from native dispatch (rendered as documentation only).", a.Name, scopePath),
					Severity: "warn",
				})
			}
			b.WriteString("\n")
		}

		// ### Hooks (preserve declaration order, same rationale as global section)
		if len(bn.Hooks) > 0 {
			b.WriteString("### Hooks\n\n")
			for _, h := range bn.Hooks {
				b.WriteString("- **")
				b.WriteString(h.Event)
				b.WriteString("**")
				if m := strings.TrimSpace(h.Matcher); m != "" {
					b.WriteString(" matcher `")
					b.WriteString(m)
					b.WriteString("`")
				}
				b.WriteString(": ")
				b.WriteString(h.ScriptPath)
				b.WriteString("\n")

				if src := relAgentsMDSource(proj, h.ScriptPath); src != "" {
					sources = append(sources, src)
				}
				warnings = append(warnings, plugin.Warning{
					Source:   h.ScriptPath,
					Message:  fmt.Sprintf("loaded always \u2014 AGENTS.md cannot execute hooks; scoped hook %s (scope: %s) listed as documentation only.", h.Event, scopePath),
					Severity: "info",
				})
			}
			b.WriteString("\n")
		}

		// ### Permissions
		if bn.Perms != nil {
			b.WriteString("### Permissions\n\n")
			if len(bn.Perms.Allow) > 0 {
				b.WriteString("- Allow: ")
				b.WriteString(strings.Join(bn.Perms.Allow, ", "))
				b.WriteString("\n")
			}
			if len(bn.Perms.Deny) > 0 {
				b.WriteString("- Deny: ")
				b.WriteString(strings.Join(bn.Perms.Deny, ", "))
				b.WriteString("\n")
			}
			if len(bn.Perms.Ask) > 0 {
				b.WriteString("- Ask: ")
				b.WriteString(strings.Join(bn.Perms.Ask, ", "))
				b.WriteString("\n")
			}
			b.WriteString("\n")

			permSrc := scopePath + "/permissions.yaml"
			sources = append(sources, permSrc)
			warnings = append(warnings, plugin.Warning{
				Source:   permSrc,
				Message:  fmt.Sprintf("loaded always \u2014 AGENTS.md cannot enforce permissions; scoped permissions (scope: %s) listed as documentation only.", scopePath),
				Severity: "info",
			})
		}

		// ### MCP servers
		if len(bn.MCP) > 0 {
			servers := append([]*model.MCPServer(nil), bn.MCP...)
			sort.SliceStable(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
			b.WriteString("### MCP servers\n\n")
			for _, m := range servers {
				b.WriteString("- **")
				b.WriteString(m.Name)
				b.WriteString("**: ")
				if u := strings.TrimSpace(m.URL); u != "" {
					b.WriteString("`")
					b.WriteString(u)
					b.WriteString("`")
				} else {
					b.WriteString("`")
					b.WriteString(m.Command)
					if len(m.Args) > 0 {
						b.WriteString(" ")
						b.WriteString(strings.Join(m.Args, " "))
					}
					b.WriteString("`")
				}
				b.WriteString("\n")

				warnings = append(warnings, plugin.Warning{
					Source:   scopePath + "/mcp.yaml",
					Message:  fmt.Sprintf("loaded always \u2014 AGENTS.md must configure MCP separately; scoped MCP server %q (scope: %s) listed as documentation only.", m.Name, scopePath),
					Severity: "info",
				})
			}
			sources = append(sources, scopePath+"/mcp.yaml")
			b.WriteString("\n")
		}
	}

	return sources, warnings
}

// relAgentsMDSource returns the tagged source path for abs (project: or global:),
// or empty if abs is empty.
func relAgentsMDSource(proj *model.Project, abs string) string {
	return proj.SourceTag(abs)
}

// SchemaVersion returns the canonical schema version this plugin
// understands (SPEC §6.4).
func (p *AgentsMDPlugin) SchemaVersion() int { return version.SchemaVersion }

// Compile-time check that AgentsMDPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*AgentsMDPlugin)(nil)
