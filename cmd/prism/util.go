package main

import (
	"fmt"
	"io"
	"strings"

	"agents.dev/agents/internal/plugin"
)

// splitTargets accepts repeated --target values, each of which may itself
// be comma-separated, and returns a flat, trimmed, deduplicated list.
func splitTargets(raw []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, r := range raw {
		for _, part := range strings.Split(r, ",") {
			t := strings.TrimSpace(part)
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// opActionLabel maps an OpKind onto the short string we print after the
// plugin name, e.g. "symlink" / "write" / "merge".
func opActionLabel(op plugin.Operation) string {
	switch op.Kind {
	case plugin.OpSymlink:
		return "symlink"
	case plugin.OpAppend:
		return "append"
	case plugin.OpMerge:
		return "merge"
	case plugin.OpDelete:
		return "delete"
	case plugin.OpWrite:
		return "write"
	default:
		return string(op.Kind)
	}
}

// printOperation prints a single op line, e.g.
//
//	"✓ CLAUDE.md (claude, symlink)"
//	"✓ .cursor/rules/_root.mdc (cursor, write, 247 bytes)"
//
// For write/append/merge ops we include the byte count of the produced content.
func printOperation(w io.Writer, op plugin.Operation) {
	action := opActionLabel(op)
	switch op.Kind {
	case plugin.OpWrite, plugin.OpAppend, plugin.OpMerge:
		fmt.Fprintf(w, "✓ %s (%s, %s, %d bytes)\n", op.Path, op.Plugin, action, len(op.Content))
	default:
		fmt.Fprintf(w, "✓ %s (%s, %s)\n", op.Path, op.Plugin, action)
	}
	for _, warn := range op.Warnings {
		printWarning(w, warn)
	}
}

// printWarning prints a single warning, indented.
func printWarning(w io.Writer, warn plugin.Warning) {
	sev := warn.Severity
	if sev == "" {
		sev = "warn"
	}
	if warn.Source != "" {
		fmt.Fprintf(w, "  ⚠ [%s] %s: %s\n", sev, warn.Source, warn.Message)
	} else {
		fmt.Fprintf(w, "  ⚠ [%s] %s\n", sev, warn.Message)
	}
}
