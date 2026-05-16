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
type GeminiPlugin struct{}

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
		Permissions:   plugin.SupportUnsupported,
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

	// Degradation warnings for primitives Gemini CLI cannot express.
	var warnings []plugin.Warning
	for _, sk := range proj.Skills {
		if sk == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join("skills", sk.Name),
			Message:  fmt.Sprintf("Gemini CLI has no skills primitive; %s not projected.", sk.Name),
			Severity: "info",
		})
	}
	for _, cmd := range proj.Commands {
		if cmd == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join("commands", cmd.Name+".md"),
			Message:  fmt.Sprintf("Gemini CLI has no commands primitive; %s not projected.", cmd.Name),
			Severity: "info",
		})
	}
	for _, ag := range proj.Agents {
		if ag == nil {
			continue
		}
		warnings = append(warnings, plugin.Warning{
			Source:   filepath.Join("agents", ag.Name+".md"),
			Message:  fmt.Sprintf("Gemini CLI has no agents primitive; %s not projected.", ag.Name),
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
		warnings = append(warnings, plugin.Warning{
			Source:   "hooks.yaml",
			Message:  fmt.Sprintf("Gemini CLI has no hooks primitive; %s not projected.", name),
			Severity: "info",
		})
	}
	if proj.Permissions != nil && (len(proj.Permissions.Allow) > 0 || len(proj.Permissions.Deny) > 0 || len(proj.Permissions.Ask) > 0) {
		warnings = append(warnings, plugin.Warning{
			Source:   "permissions.yaml",
			Message:  "Gemini CLI has no permissions primitive; permissions not projected.",
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
func buildGeminiContextOp(proj *model.Project, doc *model.Document, targetPath string, mode plugin.Mode) (plugin.Operation, error) {
	srcRel := proj.SourceTag(doc.SourcePath)

	op := plugin.Operation{
		Path:    targetPath,
		Mode:    mode,
		Sources: []string{srcRel},
		Plugin:  "gemini",
	}

	if mode == plugin.ModeSymlink {
		targetDir := filepath.Join(proj.Root, filepath.Dir(targetPath))
		linkTarget, err := filepath.Rel(targetDir, doc.SourcePath)
		if err != nil {
			return plugin.Operation{}, err
		}
		op.Kind = plugin.OpSymlink
		op.LinkTarget = linkTarget
	} else {
		op.Kind = plugin.OpWrite
		op.Content = doc.Body
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

// buildGeminiSettingsOp merges proj.MCP into any existing
// .gemini/settings.json at proj.Root, preserving unrelated keys.
func buildGeminiSettingsOp(proj *model.Project) (plugin.Operation, error) {
	settingsPath := filepath.Join(proj.Root, ".gemini", "settings.json")

	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return plugin.Operation{}, fmt.Errorf("gemini: parsing existing %s: %w", settingsPath, err)
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
		// Only set URL when there's no command — keeps the JSON minimal
		// and matches the "only non-empty fields" rule.
		if srv.Command == "" && srv.URL != "" {
			entry.URL = srv.URL
		}
		servers[srv.Name] = entry
	}
	settings["mcpServers"] = servers

	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return plugin.Operation{}, err
	}

	return plugin.Operation{
		Kind:    plugin.OpMerge,
		Path:    filepath.Join(".gemini", "settings.json"),
		Content: string(content) + "\n",
		Mode:    plugin.ModeWrite,
		Sources: []string{"mcp.yaml"},
		Plugin:  "gemini",
	}, nil
}

// Compile-time check that GeminiPlugin satisfies plugin.Plugin.
var _ plugin.Plugin = (*GeminiPlugin)(nil)
