// Package plugins contains the projection plugins that translate a canonical
// .agents/ model into per-tool config files.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// GeminiPlugin projects a model.Project into Gemini CLI's on-disk layout:
// GEMINI.md (root + per-scope, hierarchical cascade) and .gemini/settings.json
// (MCP servers under mcpServers). Gemini CLI has no native primitive for
// skills, slash-commands, sub-agents, hooks, or permissions; those degrade
// to warnings.
type GeminiPlugin struct {
	// DisableHookWrappers, when true, suppresses the perms-guard wrapper
	// script + sidecar policy emission used to enforce prism permissions
	// for plugins that lack a native permissions primitive. Default false
	// (wrappers ON). Mirrors ClaudePlugin.DisableHookWrappers.
	DisableHookWrappers bool
}

// NewGemini returns a fresh GeminiPlugin.
func NewGemini() *GeminiPlugin {
	return &GeminiPlugin{}
}

// Name is the stable plugin identifier.
func (p *GeminiPlugin) Name() string {
	return "gemini"
}

// Detect returns true if a .gemini/ directory or a GEMINI.md file is present
// at the given project root.
func (p *GeminiPlugin) Detect(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ".gemini")); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(root, "GEMINI.md")); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Capabilities returns the capability matrix entry for Gemini CLI.
func (p *GeminiPlugin) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		Context:       plugin.SupportNative,
		ScopePaths:    plugin.SupportNative,   // hierarchical cascade
		ScopeSemantic: plugin.SupportDegraded, // no native trigger description
		Skills:        plugin.SupportUnsupported,
		Commands:      plugin.SupportUnsupported,
		Agents:        plugin.SupportUnsupported,
		Hooks:         plugin.SupportUnsupported,
		Permissions:   plugin.SupportNative, // via prism perms-guard wrapper + sidecar policy
		MCP:           plugin.SupportNative,
	}
}

// Plan produces the Operations needed to project proj into Gemini CLI's layout.
//
// Context + scopes → GEMINI.md (symlink by default, write per opts.Mode).
// MCP servers → merged into .gemini/settings.json under mcpServers (always write).
// Skills / commands / agents / hooks / permissions → degradation warnings only.
func (p *GeminiPlugin) Plan(proj *model.Project, opts model.TargetOption) ([]plugin.Operation, error) {
	mode := plugin.ModeSymlink
	switch opts.Mode {
	case "write":
		mode = plugin.ModeWrite
	case "symlink", "":
		mode = plugin.ModeSymlink
	default:
		return nil, fmt.Errorf("gemini: unknown mode %q (want \"write\" or \"symlink\")", opts.Mode)
	}

	if proj == nil {
		return nil, nil
	}

	var ops []plugin.Operation

	// Root GEMINI.md.
	if proj.Context != nil {
		op, err := buildGeminiContextOp(proj, proj.Context, "GEMINI.md", mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// Per-scope GEMINI.md files (hierarchical cascade — Gemini walks up from
	// cwd merging GEMINI.md at each level).
	for _, sc := range proj.Scopes {
		if sc == nil || sc.Document == nil {
			continue
		}
		path := filepath.Join(sc.Path, "GEMINI.md")
		op, err := buildGeminiContextOp(proj, sc.Document, path, mode)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	// .gemini/settings.json (MCP servers). Always write.
	if len(proj.MCP) > 0 {
		settingsOp, err := buildGeminiSettingsOp(proj)
		if err != nil {
			return nil, err
		}
		ops = append(ops, settingsOp)
	}

	// Degradation warnings for primitives Gemini CLI cannot express. Scoped
	// variants get a slightly different message so users can grep their logs
	// for unfulfilled scoped expectations specifically.
	var warnings []plugin.Warning
	for _, sk := range proj.Skills {
		if sk == nil {
			continue
		}
		msg := fmt.Sprintf("Gemini has no skills primitive; %s not projected.", sk.Name)
		if sk.ScopePath != "" {
			msg = fmt.Sprintf("Gemini has no skills primitive; scoped skill %q (scope: %s) not projected.", sk.Name, sk.ScopePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join("skills", sk.Name),
			Message:  msg,
			Severity: "info",
		})
	}
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		msg := fmt.Sprintf("Gemini has no commands primitive; %s not projected.", cmd.Name)
		if cmd.ScopePath != "" {
			msg = fmt.Sprintf("Gemini has no commands primitive; scoped command %q (scope: %s) not projected.", cmd.Name, cmd.ScopePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join("commands", cmd.Name+".md"),
			Message:  msg,
			Severity: "info",
		})
	}
	for _, ag := range proj.Agents {
		if ag == nil {
			continue
		}
		msg := fmt.Sprintf("Gemini has no agents primitive; %s not projected.", ag.Name)
		if ag.ScopePath != "" {
			msg = fmt.Sprintf("Gemini has no agents primitive; scoped agent %q (scope: %s) not projected.", ag.Name, ag.ScopePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join("agents", ag.Name+".md"),
			Message:  msg,
			Severity: "info",
		})
	}
	for _, h := range proj.Hooks {
		if h == nil {
			continue
		}
		name := h.Event
		if h.Matcher != "" {
			name = h.Event + ":" + h.Matcher
		}
		msg := fmt.Sprintf("Gemini has no hooks primitive; %s not projected.", name)
		if h.ScopePath != "" {
			msg = fmt.Sprintf("Gemini has no hooks primitive; scoped hook %s (scope: %s) not projected.", name, h.ScopePath)
		}
		warnings = append(warnings, plugin.Warning{
			Source:   "hooks.yaml",
			Message:  msg,
			Severity: "info",
		})
	}
	// Permissions (global + scoped) project via prism perms-guard wrappers.
	// Each non-empty Permissions block becomes a sidecar JSON policy plus
	// either per-hook wrappers (when hooks exist) or a bare gate wrapper
	// the user can wire into their tool's pipeline. The CHANGELOG documents
	// the on-disk layout and the JSON policy schema.
	wrapperOps, wrapperWarnings, err := emitPermsGuardWrappers(p.Name(), proj, p.DisableHookWrappers)
	if err != nil {
		return nil, err
	}
	ops = append(ops, wrapperOps...)
	warnings = append(warnings, wrapperWarnings...)
	for _, srv := range proj.MCP {
		if srv == nil || srv.Name == "" || srv.ScopePath == "" {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join(srv.ScopePath, "mcp.yaml"),
			Message:  fmt.Sprintf("Gemini has no per-scope MCP; scoped MCP server %q (scope: %s) not projected.", srv.Name, srv.ScopePath),
			Severity: "info",
		})
	}

	if len(warnings) > 0 && len(ops) > 0 {
		ops[0].Warnings = append(ops[0].Warnings, warnings...)
	}

	return ops, nil
}

// buildGeminiContextOp constructs a single Operation for a Document being
// projected to the given target path (relative to project root) in the given
// Mode. Mirrors the Claude plugin's buildOp shape but stamps Plugin="gemini".
//
// When doc.NeedsWrite() is true (the document had @include directives
// expanded into its body) symlink mode is downgraded to write mode and
// an info-severity warning is attached describing the reason. Every
// included file's source tag is appended to op.Sources so lockfile /
// `agents which` traces flow back to the included content too.
func buildGeminiContextOp(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode) (plugin.Operation, error) {
	downgraded := false
	if doc.NeedsWrite() && mode == plugin.ModeSymlink {
		mode = plugin.ModeWrite
		downgraded = true
	}

	srcRel := proj.SourceTag(doc.SourcePath)
	sources := []string{srcRel}
	for _, inc := range doc.Includes {
		sources = append(sources, proj.SourceTag(inc))
	}

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: sources,
		Plugin:  "gemini",
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
			Source:   srcRel,
			Message:  "downgraded to write mode: contains @include directives",
			Severity: "info",
		})
	}

	return op, nil
}

// geminiMCPServerJSON is the schema Gemini CLI expects for entries under
// `.gemini/settings.json`'s `mcpServers` map. Anthropic/Gemini share this
// convention.
type geminiMCPServerJSON struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// buildGeminiSettingsOp emits the OpMerge for .gemini/settings.json. The
// engine reads the existing file and passes its bytes to the Merger; Plan()
// does not touch disk.
func buildGeminiSettingsOp(proj *model.Project) (plugin.Operation, error) {
	relPath := filepath.Join(".gemini", "settings.json")

	merger := func(existing []byte) (string, error) {
		settings := map[string]any{}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &settings); err != nil {
				return "", fmt.Errorf("gemini: parsing existing %s: %w", relPath, err)
			}
		}

		servers, _ := settings["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		for _, srv := range proj.MCP {
			if srv == nil || srv.Name == "" {
				continue
			}
			entry := geminiMCPServerJSON{
				Command: srv.Command,
				Args:    srv.Args,
				Env:     srv.Env,
			}
			if srv.Command == "" && srv.URL != "" {
				entry.URL = srv.URL
			}
			servers[srv.Name] = entry
		}
		settings["mcpServers"] = servers

		content, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return "", err
		}
		return string(content) + "\n", nil
	}

	return plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    relPath,
		Mode:    plugin.ModeWrite,
		Sources: []string{"mcp.yaml"},
		Plugin:  "gemini",
		Merger:  merger,
	}, nil
}

// Compile-time check that GeminiPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*GeminiPlugin)(nil)
