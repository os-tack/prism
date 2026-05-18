// Package perms is the shared permission-policy loader and matcher used by
// the wrapper-script enforcement path (plugins/gemini.go, plugins/continue_plugin.go)
// and the runtime `prism perms-guard` subcommand (cmd/prism/perms_guard.go).
//
// Plugins serialize a model.Permissions block to a sidecar JSON file next
// to the generated hook wrapper. At hook-firing time the wrapper exec's
// `prism perms-guard --policy <sidecar>`, which loads the JSON, inspects
// the hook payload on stdin, and decides allow / deny / ask.
package perms

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Policy is the on-disk JSON shape written next to a wrapper script.
//
// Each entry is `<tool>:<pattern>`. The tool name is matched case-insensitively
// against the hook payload's `tool_name` field (e.g. "Bash", "Edit"). The
// pattern after the colon supports two forms:
//
//   - exact match: "bash:ls"
//   - trailing-wildcard glob: "bash:git *" matches any bash command
//     beginning with "git ".
//
// More complex glob syntax (mid-string `*`, `?`, character classes) is NOT
// supported in v0.7 — the rule set stays small and easy to audit.
type Policy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
	Ask   []string `json:"ask,omitempty"`
}

// Decision is the resolution of a single hook event against a Policy.
type Decision int

const (
	// DecisionAllow means the wrapper should exec the underlying hook.
	DecisionAllow Decision = iota
	// DecisionDeny means the wrapper should exit non-zero before exec.
	DecisionDeny
	// DecisionAsk means the wrapper should prompt the user (TTY only;
	// non-TTY callers should treat Ask as Deny for safety).
	DecisionAsk
	// DecisionDefault means no rule matched; the wrapper should fall
	// through to its default-allow behavior (matching the scope-guard
	// pattern, which only blocks when an explicit scope rules it out).
	DecisionDefault
)

// String returns the lowercase token form of d, used in the wrapper's exit
// message so tests can grep deterministically.
func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	case DecisionAsk:
		return "ask"
	default:
		return "default"
	}
}

// Load reads a policy file from path. A missing file is not an error — the
// wrapper should treat that as "no policy, default allow".
func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Policy{}, nil
		}
		return nil, fmt.Errorf("perms: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return &Policy{}, nil
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("perms: parse %s: %w", path, err)
	}
	return &p, nil
}

// Marshal serializes p as deterministic, indented JSON suitable for the
// sidecar file the projection plugins emit.
func Marshal(p *Policy) ([]byte, error) {
	if p == nil {
		p = &Policy{}
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Check evaluates a single hook event against the policy. Deny rules
// dominate Allow, which dominates Ask; no match returns DecisionDefault
// so the caller can apply its own fallback (the wrapper script currently
// defaults-allow to preserve hook semantics for projects that haven't
// adopted permissions yet).
//
// toolName is matched case-insensitively against rules. action is the
// operative input — for Bash it's the command string, for Edit/Write the
// file path. The caller passes whatever the hook payload exposed; perms
// doesn't dig into the JSON.
func Check(p *Policy, toolName, action string) Decision {
	if p == nil {
		return DecisionDefault
	}
	for _, rule := range p.Deny {
		if matchRule(rule, toolName, action) {
			return DecisionDeny
		}
	}
	for _, rule := range p.Allow {
		if matchRule(rule, toolName, action) {
			return DecisionAllow
		}
	}
	for _, rule := range p.Ask {
		if matchRule(rule, toolName, action) {
			return DecisionAsk
		}
	}
	return DecisionDefault
}

// matchRule reports whether a `tool:pattern` rule matches the given
// (toolName, action) pair. Rules without a colon are treated as
// tool-only matchers (any action under that tool matches).
//
// v0.9 / schema v2 grammar extensions (SPEC §4.6.2):
//
//   - `fs:<pattern>` is a synonym for Read|Write|Edit; a rule like
//     `fs:src/**` matches when toolName is any of Read/Write/Edit and the
//     action matches the pattern. Plugins emit-time fan-out continues to
//     handle the Claude wire format; the matcher's job is just to detect a
//     hit.
//   - `network:<domain>` matches WebFetch / Fetch tool calls where the
//     action is a URL or domain. Trailing `*` and recursive `**` glob
//     forms are honored.
//   - `mcp:<server>[:<tool>]` matches MCP tool dispatch. The runtime
//     emits `tool_name` as `mcp__<server>__<tool>`; the matcher accepts
//     either that wire form or the canonical `mcp__` payload form.
//   - `**` recursive-glob is honored alongside the trailing-`*` form.
//   - `!`-prefixed patterns negate within their list (e.g. an entry
//     `Edit:!src/billing/migrations/*` inside an allow list reduces to a
//     deny entry at emit time; the matcher itself reports the rule as
//     non-matching so the negation cannot be used to bypass deny).
func matchRule(rule, toolName, action string) bool {
	if rule == "" {
		return false
	}
	idx := strings.Index(rule, ":")
	if idx < 0 {
		return strings.EqualFold(rule, toolName)
	}
	ruleTool := rule[:idx]
	rulePattern := rule[idx+1:]

	// Negation prefix `!`. Within the rule's pattern, a leading `!` reverses
	// the match — but only meaningfully at emit time (the engine expands a
	// negated allow rule into a deny). For runtime matching we treat the
	// negated rule as not-matching so it cannot smuggle a permission past
	// a deny check.
	if strings.HasPrefix(rulePattern, "!") {
		return false
	}

	// `fs:` synonym for Read|Write|Edit/MultiEdit.
	if strings.EqualFold(ruleTool, "fs") {
		switch {
		case strings.EqualFold(toolName, "read"),
			strings.EqualFold(toolName, "write"),
			strings.EqualFold(toolName, "edit"),
			strings.EqualFold(toolName, "multiedit"):
			return matchPattern(rulePattern, action)
		}
		return false
	}

	// `network:` matches WebFetch / Fetch by domain or URL pattern.
	if strings.EqualFold(ruleTool, "network") {
		switch {
		case strings.EqualFold(toolName, "webfetch"),
			strings.EqualFold(toolName, "fetch"):
			return matchPattern(rulePattern, action)
		}
		return false
	}

	// `mcp:<server>[:<tool>]` matches MCP tool dispatch.
	if strings.EqualFold(ruleTool, "mcp") {
		// rulePattern is `<server>[:<tool>]`. action is typically the MCP
		// tool name in the Claude wire form `mcp__<server>__<tool>` or the
		// canonical `<server>:<tool>` form.
		server, mcpTool, hasTool := splitMCP(rulePattern)
		actServer, actTool := normalizeMCPAction(toolName, action)
		if actServer == "" {
			return false
		}
		if !globMatch(server, actServer) {
			return false
		}
		if !hasTool || mcpTool == "" || mcpTool == "*" {
			return true
		}
		return globMatch(mcpTool, actTool)
	}

	if !strings.EqualFold(ruleTool, toolName) {
		return false
	}
	return matchPattern(rulePattern, action)
}

// splitMCP splits a `mcp:` rule's pattern into (server, tool, hasTool).
// Accepts `<server>`, `<server>:<tool>`, or `<server>:*`.
func splitMCP(pat string) (string, string, bool) {
	if idx := strings.Index(pat, ":"); idx >= 0 {
		return pat[:idx], pat[idx+1:], true
	}
	return pat, "", false
}

// normalizeMCPAction returns (server, tool) for an MCP dispatch payload.
// Accepts the Claude wire form `mcp__<server>__<tool>` (toolName is the
// dispatch tool itself) and the canonical `<server>:<tool>` action form.
func normalizeMCPAction(toolName, action string) (string, string) {
	if strings.HasPrefix(toolName, "mcp__") {
		rest := strings.TrimPrefix(toolName, "mcp__")
		if idx := strings.Index(rest, "__"); idx >= 0 {
			return rest[:idx], rest[idx+2:]
		}
		return rest, ""
	}
	if strings.EqualFold(toolName, "mcp") {
		if idx := strings.Index(action, ":"); idx >= 0 {
			return action[:idx], action[idx+1:]
		}
		return action, ""
	}
	return "", ""
}

// matchPattern is the shared glob check used by every namespace branch.
// Honors exact match, trailing `*` prefix, and `**` recursive forms.
func matchPattern(pat, action string) bool {
	if pat == "" || pat == "*" || pat == "**" {
		return true
	}
	return globMatch(pat, action)
}

// globMatch is a simple glob with `*` (single-segment) and `**`
// (recursive) semantics. Conservative implementation: split the pattern
// on `**` and require the action to contain each literal segment in
// order; within segments, `*` matches any run of non-`/` characters via
// prefix/suffix anchoring.
func globMatch(pat, s string) bool {
	if pat == s {
		return true
	}
	// Fast path: trailing `*` glob (the v0.7 default).
	if strings.HasSuffix(pat, "*") && !strings.Contains(pat[:len(pat)-1], "*") {
		return strings.HasPrefix(s, pat[:len(pat)-1])
	}
	// Recursive `**` glob: split into anchored segments and match in order.
	if strings.Contains(pat, "**") {
		segs := strings.Split(pat, "**")
		rest := s
		for i, seg := range segs {
			if seg == "" {
				continue
			}
			switch {
			case i == 0:
				if !strings.HasPrefix(rest, seg) {
					return false
				}
				rest = rest[len(seg):]
			case i == len(segs)-1:
				if !strings.HasSuffix(rest, seg) {
					return false
				}
			default:
				idx := strings.Index(rest, seg)
				if idx < 0 {
					return false
				}
				rest = rest[idx+len(seg):]
			}
		}
		return true
	}
	// Single `*` somewhere in the middle: split and match prefix/suffix.
	if strings.Contains(pat, "*") {
		idx := strings.Index(pat, "*")
		prefix, suffix := pat[:idx], pat[idx+1:]
		if !strings.HasPrefix(s, prefix) {
			return false
		}
		return strings.HasSuffix(s, suffix)
	}
	return false
}
