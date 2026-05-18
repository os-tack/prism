package plugins

import (
	"encoding/json"
	"sort"
	"strings"

	"agents.dev/agents/internal/model"
)

// hooks_claude_shape.go is the shared Claude-shape hook serializer.
//
// Claude and Continue share the hook schema verbatim per SPEC §4.4 —
// Continue's research notes establish that its hook contract is a literal
// copy of Claude's. Both plugins serialize through this file. If Claude's
// schema diverges from Continue's later, snapshot regressions in
// testdata/shared-hooks-shape.json will fire (recorded as a Phase 2 risk
// in IMPLEMENTATION_PLAN.md §7.3).
//
// Phase 1 scaffolding only. Phase 0 leaves Claude's existing inline emit
// untouched; Phase 2 swaps Claude over to call ClaudeShapeHooks and flips
// Continue Hooks to native via the same call.

// ClaudeHookEntry is a single hook handler entry in the Claude schema.
type ClaudeHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // SECONDS, not ms
	URL     string `json:"url,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// ClaudeHookGroup is one matcher group under an event. Matcher is a
// pipe-separated string (e.g. "Edit|Write|MultiEdit") or empty for
// match-all.
type ClaudeHookGroup struct {
	Matcher string            `json:"matcher,omitempty"`
	Hooks   []ClaudeHookEntry `json:"hooks"`
}

// ClaudeShapeHooks renders canonical hooks into the
// map[string][]ClaudeHookGroup form Claude's settings.json `hooks:` key
// carries. Continue uses the identical shape under .continue/hooks.yaml
// (Phase 2 wires Continue through this same function).
//
// The plugin parameter selects column from hookEventTable when translating
// per-action canonical events to (generic + matcher) form. Pass "claude"
// for Claude, "continue" for Continue.
func ClaudeShapeHooks(hooks []*model.Hook, plugin string) map[string][]ClaudeHookGroup {
	out := make(map[string][]ClaudeHookGroup)
	if len(hooks) == 0 {
		return out
	}
	type key struct {
		event   string
		matcher string
	}
	groups := make(map[key][]ClaudeHookEntry)
	eventOrder := []string{}
	seenEvent := make(map[string]bool)

	for _, h := range hooks {
		if h == nil || h.Disabled {
			continue
		}
		eventName, matcherStr := resolveClaudeEvent(h, plugin)
		if eventName == "" {
			continue
		}
		if !seenEvent[eventName] {
			eventOrder = append(eventOrder, eventName)
			seenEvent[eventName] = true
		}
		k := key{eventName, matcherStr}
		for _, hndlr := range hookEntriesFor(h) {
			groups[k] = append(groups[k], hndlr)
		}
	}

	for _, ev := range eventOrder {
		var matchers []string
		for k := range groups {
			if k.event == ev {
				matchers = append(matchers, k.matcher)
			}
		}
		sort.Strings(matchers)
		for _, m := range matchers {
			out[ev] = append(out[ev], ClaudeHookGroup{Matcher: m, Hooks: groups[key{ev, m}]})
		}
	}
	return out
}

// resolveClaudeEvent returns the wire event + matcher for a Hook. Prefers
// the canonical EventCanonical (typed) when set; falls back to the v0.8
// Event string. Custom "native:<verbatim>" pass through unchanged.
func resolveClaudeEvent(h *model.Hook, plugin string) (string, string) {
	if h.EventCanonical != "" {
		if strings.HasPrefix(string(h.EventCanonical), "native:") {
			return strings.TrimPrefix(string(h.EventCanonical), "native:"), matcherStringFor(h)
		}
		if m, ok := MapHookEventFor(plugin, h.EventCanonical); ok {
			match := m.Matcher
			if user := matcherStringFor(h); user != "" {
				match = combineMatchers(m.Matcher, user)
			}
			return m.Event, match
		}
		return "", ""
	}
	return h.Event, matcherStringFor(h)
}

// matcherStringFor returns the user-supplied matcher pattern from either
// the v2 MatcherV2 struct or the v0.8 Matcher string. Multiple exact
// patterns join with `|`.
func matcherStringFor(h *model.Hook) string {
	if len(h.MatcherV2.Patterns) > 0 {
		return strings.Join(h.MatcherV2.Patterns, "|")
	}
	return h.Matcher
}

// combineMatchers joins a target-table matcher hint with a user-supplied
// pattern. Empty inputs short-circuit; both non-empty join with `|`.
func combineMatchers(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "|" + b
	}
}

// hookEntriesFor renders all handlers on a Hook into ClaudeHookEntry slice.
// v0.8 Hooks expressed a single command via ScriptPath; v2 carries
// multiple typed Handlers. Render both forms (v2 takes precedence when
// populated).
func hookEntriesFor(h *model.Hook) []ClaudeHookEntry {
	var out []ClaudeHookEntry
	if len(h.Handlers) > 0 {
		for _, hd := range h.Handlers {
			out = append(out, claudeEntryFromHandler(hd))
		}
		return out
	}
	if h.ScriptPath != "" {
		out = append(out, ClaudeHookEntry{Type: "command", Command: h.ScriptPath})
	}
	return out
}

func claudeEntryFromHandler(hd model.HookHandler) ClaudeHookEntry {
	entry := ClaudeHookEntry{Type: string(hd.Kind)}
	if entry.Type == "" {
		entry.Type = "command"
	}
	if hd.TimeoutMs > 0 {
		entry.Timeout = msToSeconds(hd.TimeoutMs)
	}
	switch hd.Kind {
	case model.HookHandlerHTTP:
		entry.URL = hd.URL
	case model.HookHandlerPrompt:
		entry.Prompt = hd.Prompt
	default:
		if hd.Command != "" {
			entry.Command = hd.Command
			if len(hd.Args) > 0 {
				entry.Command = hd.Command + " " + strings.Join(hd.Args, " ")
			}
		}
	}
	return entry
}

// msToSeconds rounds milliseconds up to whole seconds for Claude's
// `timeout` field (Claude's unit is seconds; ms is the canonical unit).
func msToSeconds(ms int) int {
	if ms <= 0 {
		return 0
	}
	sec := ms / 1000
	if ms%1000 != 0 {
		sec++
	}
	return sec
}

// RenderClaudeShapeJSON marshals the output of ClaudeShapeHooks to indented
// JSON, with deterministic key order.
func RenderClaudeShapeJSON(groups map[string][]ClaudeHookGroup) (string, error) {
	if len(groups) == 0 {
		return "{}", nil
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string][]ClaudeHookGroup, len(groups))
	for _, k := range keys {
		ordered[k] = groups[k]
	}
	b, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
