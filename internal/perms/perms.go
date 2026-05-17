// Package perms is the shared permission-policy loader and matcher used by
// the wrapper-script enforcement path (plugins/gemini.go, plugins/continue_plugin.go)
// and the runtime `prism perms-guard` subcommand (cmd/agents/perms_guard.go).
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
// tool-only matchers (any action under that tool matches). Rules with
// a trailing `*` glob match by prefix on the action; everything else is
// an exact match.
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
	if !strings.EqualFold(ruleTool, toolName) {
		return false
	}
	if rulePattern == "" || rulePattern == "*" {
		return true
	}
	if strings.HasSuffix(rulePattern, "*") {
		prefix := strings.TrimSuffix(rulePattern, "*")
		return strings.HasPrefix(action, prefix)
	}
	return rulePattern == action
}
