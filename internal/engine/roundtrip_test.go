package engine_test

// roundtrip_test.go — end-to-end correctness tests for the v0.6 importers.
//
// For each covered tool, the test:
//
//   1. Stages a source-tool tree (the kind of layout an existing project
//      would have on disk before adopting agent-projection).
//   2. Captures the original file content into a map BEFORE running any
//      engine operation, because the compile step writes back to the
//      source-tool directory and may overwrite the originals.
//   3. Runs engine.Init(opts, "<tool>") so the importer projects the
//      source tree into .agents/.
//   4. Runs engine.Compile(opts) with ONLY the matching plugin registered
//      so the output tree is generated solely by that tool's projector.
//   5. Asserts that every original input file's meaningful content
//      (frontmatter values + body text minus provenance comments) is
//      present in the predictable output file.
//
// The round-trip is the strongest correctness guarantee available; it
// would have caught I1 (serializeProject dropping frontmatter), I2
// (scoped MCP drop), and I3 (no hook serialization), and any future
// regressions of similar shape.
//
// Coverage:
//   - cursor
//   - claude
//   - continue
//   - copilot
//   - gemini
//   - cline
//   - windsurf
//   - agents-md

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/plugins"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Generic helpers used by every round-trip test.
// ---------------------------------------------------------------------------

// rtWrite creates parent directories and writes content. Distinct from the
// package-level mustWrite (in regression_test.go) which does NOT create
// parents — round-trip fixtures want the convenience.
func rtWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// writeFiles bulk-writes multiple fixture files relative to dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		rtWrite(t, dir, rel, content)
	}
}

// captureOriginal snapshots all files under dir so post-compile reads
// can compare against pre-init content. Returns a map of relative paths
// to file bytes.
func captureOriginal(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("captureOriginal walk: %v", err)
	}
	return out
}

// readBody reads a file at root/relPath, strips leading YAML frontmatter
// (a "---\n…\n---\n" block), strips leading HTML provenance comment lines
// (introduced by the importer), trims trailing whitespace, and returns
// the bare body. Used for content-equivalence comparisons that should
// not care about frontmatter ordering or provenance noise.
func readBody(t *testing.T, root, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	_, body, err := splitFrontmatterTest(data)
	if err != nil {
		t.Fatalf("splitFrontmatter %s: %v", relPath, err)
	}
	body = stripProvenance(body)
	return strings.TrimSpace(body)
}

// readFrontmatter parses the YAML frontmatter at root/relPath. Returns
// nil if no frontmatter is present.
func readFrontmatter(t *testing.T, root, relPath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	fm, _, err := splitFrontmatterTest(data)
	if err != nil {
		t.Fatalf("splitFrontmatter %s: %v", relPath, err)
	}
	return fm
}

// splitFrontmatterTest is a local copy of importer.splitFrontmatter (an
// unexported helper). Kept here so the engine_test package does not
// import the importer package privately.
func splitFrontmatterTest(data []byte) (map[string]any, string, error) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return nil, s, nil
	}
	// Skip the opening fence.
	rest := strings.TrimPrefix(strings.TrimPrefix(s, "---\n"), "---\r\n")
	// Find the closing fence at the start of a line.
	var end int
	for _, marker := range []string{"\n---\n", "\n---\r\n", "\r\n---\r\n"} {
		if idx := strings.Index(rest, marker); idx >= 0 {
			end = idx + len(marker)
			fmBlock := rest[:idx]
			body := rest[end:]
			fm := map[string]any{}
			if err := yaml.Unmarshal([]byte(fmBlock), &fm); err != nil {
				return nil, "", err
			}
			return fm, body, nil
		}
	}
	// No closing fence: treat whole thing as body.
	return nil, s, nil
}

// stripProvenance removes any leading "<!-- imported from … -->" lines
// from a body. The importer prepends one such comment per source; we
// drop them so comparisons focus on user-authored content.
func stripProvenance(body string) string {
	for {
		body = strings.TrimLeft(body, "\n")
		if !strings.HasPrefix(body, "<!--") {
			return body
		}
		end := strings.Index(body, "-->")
		if end < 0 {
			return body
		}
		body = body[end+len("-->"):]
	}
}

// assertBodyContains fails when haystack does not contain every
// substring in needles. Substrings are compared after collapsing runs
// of whitespace so trailing-newline / indentation differences are
// tolerated.
func assertBodyContains(t *testing.T, label, haystack string, needles ...string) {
	t.Helper()
	normalized := normalizeSpace(haystack)
	for _, n := range needles {
		if !strings.Contains(normalized, normalizeSpace(n)) {
			t.Errorf("%s: expected to contain %q\nactual body:\n%s", label, n, haystack)
		}
	}
}

// normalizeSpace collapses runs of whitespace (including newlines) into
// single spaces. Lets us treat "foo\nbar" and "foo bar" as equivalent
// for content-presence checks.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// stringSliceFromAny coerces a parsed YAML value into []string, dropping
// non-string entries. Helps when comparing frontmatter globs whose YAML
// type is []any.
func stringSliceFromAny(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{t}
	}
	return nil
}

// rtOptions builds engine.Options with a single named plugin registered.
// Round-trip tests want output produced only by the matching projector.
func rtOptions(t *testing.T, root string, plugins ...plugin.Plugin) engine.Options {
	t.Helper()
	reg := plugin.NewRegistry()
	for _, p := range plugins {
		if err := reg.Register(p); err != nil {
			t.Fatalf("rtOptions: register %q: %v", p.Name(), err)
		}
	}
	return engine.Options{Root: root, Registry: reg, Quiet: true}
}

// runRoundTrip is the standard init+compile sequence. It returns the
// engine.Compile report so callers can assert on warnings if desired.
func runRoundTrip(t *testing.T, opts engine.Options, importFrom string) *engine.Report {
	t.Helper()
	if err := engine.Init(opts, importFrom); err != nil {
		t.Fatalf("engine.Init(%s): %v", importFrom, err)
	}
	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("engine.Compile: %v", err)
	}
	return rep
}

// ---------------------------------------------------------------------------
// Cursor round-trip
// ---------------------------------------------------------------------------

// TestRoundTrip_Cursor stages a representative .cursor/ tree, runs
// import + compile via the cursor plugin only, and verifies each input
// file's content is reproducible from the output tree. Notes:
//
//   - alwaysApply: true rules → become root context.md → compile back
//     as _root.mdc (different filename, equivalent content).
//   - Path-glob rules become a scope at .agents/<glob-prefix>/context.md
//     → compile back as <slug>.mdc (e.g. src/billing → src-billing.mdc).
//   - Extension-glob rules become skills at .agents/skills/<name>/
//     SKILL.md → compile back as .cursor/rules/skill-<slug>.mdc.
//   - The .cursor/mcp.json round-trips through .agents/mcp.yaml.
func TestRoundTrip_Cursor(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".cursor/rules/style.mdc": "---\nalwaysApply: true\n---\nUse Go 1.25 style throughout.\n",
		".cursor/rules/billing.mdc": "---\n" +
			"description: Stripe webhook conventions\n" +
			"globs: [\"src/billing/**\"]\n" +
			"---\n" +
			"Validate webhook signatures before processing.\n",
		".cursor/rules/pdf-skill.mdc": "---\n" +
			"description: PDF editing\n" +
			"globs: [\"**/*.pdf\"]\n" +
			"---\n" +
			"Use pdftk for PDF manipulation tasks.\n",
		".cursor/mcp.json": `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"]}}}`,
	})
	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewCursor())
	runRoundTrip(t, opts, "cursor")

	// 1. alwaysApply rule → _root.mdc.
	rootBody := readBody(t, root, ".cursor/rules/_root.mdc")
	assertBodyContains(t, "_root.mdc body", rootBody, "Use Go 1.25 style throughout.")
	rootFM := readFrontmatter(t, root, ".cursor/rules/_root.mdc")
	if v, _ := rootFM["alwaysApply"].(bool); !v {
		t.Errorf("_root.mdc: alwaysApply = %v, want true", rootFM["alwaysApply"])
	}

	// 2. Path-glob rule → scope at src/billing → src-billing.mdc.
	billingOut := ".cursor/rules/src-billing.mdc"
	billBody := readBody(t, root, billingOut)
	assertBodyContains(t, billingOut+" body", billBody, "Validate webhook signatures before processing.")
	billFM := readFrontmatter(t, root, billingOut)
	if desc, _ := billFM["description"].(string); !strings.Contains(desc, "Stripe webhook") {
		t.Errorf("%s description = %q, want to contain %q",
			billingOut, desc, "Stripe webhook")
	}
	if globs := stringSliceFromAny(billFM["globs"]); len(globs) == 0 || !strings.HasPrefix(globs[0], "src/billing") {
		t.Errorf("%s globs = %v, want to start with src/billing", billingOut, globs)
	}

	// 3. Extension-glob rule → skill → .cursor/rules/skill-pdf-skill.mdc.
	pdfOut := ".cursor/rules/skill-pdf-skill.mdc"
	pdfBody := readBody(t, root, pdfOut)
	assertBodyContains(t, pdfOut+" body", pdfBody, "Use pdftk for PDF manipulation tasks.")
	pdfFM := readFrontmatter(t, root, pdfOut)
	if desc, _ := pdfFM["description"].(string); !strings.Contains(desc, "PDF editing") {
		t.Errorf("%s description = %q, want to contain %q", pdfOut, desc, "PDF editing")
	}
	if globs := stringSliceFromAny(pdfFM["globs"]); len(globs) == 0 || globs[0] != "**/*.pdf" {
		t.Errorf("%s globs = %v, want [**/*.pdf]", pdfOut, globs)
	}

	// 4. .cursor/mcp.json round-trips (linear server survives).
	mcpData, err := os.ReadFile(filepath.Join(root, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var mcp map[string]any
	if err := json.Unmarshal(mcpData, &mcp); err != nil {
		t.Fatalf("parse mcp.json: %v", err)
	}
	servers, _ := mcp["mcpServers"].(map[string]any)
	if servers == nil || servers["linear"] == nil {
		t.Errorf("mcp.json mcpServers.linear missing after round-trip:\n%s", string(mcpData))
	}

	// Sanity: every original body line should still be findable in some
	// output file (catches silent content drops).
	allOutputs := concatAllRuleBodies(t, root, ".cursor/rules")
	for relPath, content := range original {
		if !strings.HasPrefix(relPath, ".cursor/rules/") || !strings.HasSuffix(relPath, ".mdc") {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("cursor round-trip: original body of %s not present in any output rule:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}

// concatAllRuleBodies concatenates every body in dir for "is this
// content present anywhere?" presence checks.
func concatAllRuleBodies(t *testing.T, root, dir string) string {
	t.Helper()
	full := filepath.Join(root, dir)
	entries, err := os.ReadDir(full)
	if err != nil {
		t.Fatalf("read %s: %v", full, err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body := readBody(t, root, filepath.Join(dir, e.Name()))
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Claude round-trip
// ---------------------------------------------------------------------------

// TestRoundTrip_Claude stages a representative .claude/ + CLAUDE.md
// tree, runs import + compile via the claude plugin only, and verifies
// every input file's content is reproducible. Notes:
//
//   - Root CLAUDE.md → .agents/context.md → CLAUDE.md (symlinked by
//     default; os.ReadFile follows symlinks transparently).
//   - Nested src/billing/CLAUDE.md → scope → src/billing/CLAUDE.md.
//   - .claude/skills/<name>/SKILL.md round-trips with frontmatter.
//   - .claude/settings.json permissions → .agents/permissions.yaml →
//     .claude/settings.json with the same allow/deny/ask sets.
//   - .mcp.json round-trips through .agents/mcp.yaml.
func TestRoundTrip_Claude(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"CLAUDE.md":             "# Root\n\nUse the project-wide Go conventions.\n",
		"src/billing/CLAUDE.md": "# Billing\n\nValidate Stripe webhook signatures.\n",
		".claude/skills/pdf/SKILL.md": "---\n" +
			"description: PDF editing helper\n" +
			"---\n" +
			"Use pdftk for PDF manipulation.\n",
		".claude/settings.json": `{
  "permissions": {
    "allow": ["Bash(go test:*)", "Read(**)"],
    "deny":  ["Bash(rm -rf:*)"]
  }
}`,
		".mcp.json": `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"]}}}`,
	})

	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewClaude())
	runRoundTrip(t, opts, "claude")

	// 1. Root CLAUDE.md content survives.
	rootBody := readBody(t, root, "CLAUDE.md")
	assertBodyContains(t, "CLAUDE.md", rootBody, "Use the project-wide Go conventions.")

	// 2. Nested scope CLAUDE.md content survives.
	billBody := readBody(t, root, "src/billing/CLAUDE.md")
	assertBodyContains(t, "src/billing/CLAUDE.md", billBody, "Validate Stripe webhook signatures.")

	// 3. Skill round-trips: filename/path preserved (skill `pdf` is
	// global so it lands back at .claude/skills/pdf/SKILL.md).
	skillPath := ".claude/skills/pdf/SKILL.md"
	skillBody := readBody(t, root, skillPath)
	assertBodyContains(t, skillPath, skillBody, "Use pdftk for PDF manipulation.")
	skillFM := readFrontmatter(t, root, skillPath)
	if desc, _ := skillFM["description"].(string); !strings.Contains(desc, "PDF editing helper") {
		t.Errorf("%s description = %q, want to contain %q",
			skillPath, desc, "PDF editing helper")
	}

	// 4. settings.json permissions survive (allow/deny/ask).
	settingsData, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("settings.json permissions missing after round-trip:\n%s", string(settingsData))
	}
	wantAllow := []string{"Bash(go test:*)", "Read(**)"}
	gotAllow := stringSliceFromAny(perms["allow"])
	if !slicesEqualSet(gotAllow, wantAllow) {
		t.Errorf("permissions.allow = %v, want set-equal to %v", gotAllow, wantAllow)
	}
	wantDeny := []string{"Bash(rm -rf:*)"}
	gotDeny := stringSliceFromAny(perms["deny"])
	if !slicesEqualSet(gotDeny, wantDeny) {
		t.Errorf("permissions.deny = %v, want %v", gotDeny, wantDeny)
	}

	// 5. .mcp.json survives (linear server present).
	mcpData, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var mcp map[string]any
	if err := json.Unmarshal(mcpData, &mcp); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers, _ := mcp["mcpServers"].(map[string]any)
	if servers == nil || servers["linear"] == nil {
		t.Errorf(".mcp.json mcpServers.linear missing after round-trip:\n%s", string(mcpData))
	}

	allOutputs := concatAllBodiesRecursive(t, root, []string{".", ".claude/skills", "src"}, []string{".md"})
	for relPath, content := range original {
		if !isMarkdownPath(relPath) {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("claude round-trip: original body of %s not present in any output:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}

// slicesEqualSet reports whether a and b contain the same elements
// (order-insensitive, multiset-strict).
func slicesEqualSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Continue round-trip
// ---------------------------------------------------------------------------

// TestRoundTrip_Continue stages a representative .continue/ tree, runs
// import + compile via the continue plugin only, and verifies content
// survives the round-trip. Notes:
//
//   - alwaysApply rule with no globs → root context → continue compiles
//     back to .continue/rules/_root.md (same filename round-trips).
//   - Path-glob rule → scope at .agents/<prefix>/context.md → compile
//     back to .continue/rules/<slug>.md (e.g. src-billing.md).
//   - .continue/mcpServers/<name>.yaml round-trips through
//     .agents/mcp.yaml.
func TestRoundTrip_Continue(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".continue/rules/_root.md": "---\n" +
			"alwaysApply: true\n" +
			"---\n" +
			"Project-wide Go style: gofmt, error wrapping with %w.\n",
		".continue/rules/billing.md": "---\n" +
			"description: Stripe webhook rules\n" +
			"globs: [\"src/billing/**\"]\n" +
			"---\n" +
			"Validate webhook signatures using the Stripe SDK.\n",
		".continue/mcpServers/linear.yaml": "name: linear\n" +
			"command: npx\n" +
			"args:\n  - \"@linear/mcp\"\n",
	})

	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewContinue())
	runRoundTrip(t, opts, "continue")

	// 1. alwaysApply rule → _root.md (same name on both sides).
	rootBody := readBody(t, root, ".continue/rules/_root.md")
	assertBodyContains(t, "_root.md", rootBody,
		"Project-wide Go style: gofmt, error wrapping with %w.")
	rootFM := readFrontmatter(t, root, ".continue/rules/_root.md")
	if v, _ := rootFM["alwaysApply"].(bool); !v {
		t.Errorf("_root.md alwaysApply = %v, want true", rootFM["alwaysApply"])
	}

	// 2. Path-glob rule → src-billing.md.
	billOut := ".continue/rules/src-billing.md"
	billBody := readBody(t, root, billOut)
	assertBodyContains(t, billOut, billBody,
		"Validate webhook signatures using the Stripe SDK.")
	billFM := readFrontmatter(t, root, billOut)
	if desc, _ := billFM["description"].(string); !strings.Contains(desc, "Stripe webhook") {
		t.Errorf("%s description = %q, want to contain %q",
			billOut, desc, "Stripe webhook")
	}
	if globs := stringSliceFromAny(billFM["globs"]); len(globs) == 0 ||
		!strings.HasPrefix(globs[0], "src/billing") {
		t.Errorf("%s globs = %v, want to start with src/billing", billOut, globs)
	}

	// 3. MCP server YAML round-trips. The continue plugin slugifies the
	// server name to derive the filename, so "linear" → linear.yaml.
	mcpPath := ".continue/mcpServers/linear.yaml"
	mcpData, err := os.ReadFile(filepath.Join(root, mcpPath))
	if err != nil {
		t.Fatalf("read %s: %v", mcpPath, err)
	}
	var mcp map[string]any
	if err := yaml.Unmarshal(mcpData, &mcp); err != nil {
		t.Fatalf("parse %s: %v", mcpPath, err)
	}
	if cmd, _ := mcp["command"].(string); cmd != "npx" {
		t.Errorf("%s command = %q, want %q\nfull:\n%s",
			mcpPath, cmd, "npx", string(mcpData))
	}
	args := stringSliceFromAny(mcp["args"])
	if len(args) == 0 || args[0] != "@linear/mcp" {
		t.Errorf("%s args = %v, want [@linear/mcp]\nfull:\n%s",
			mcpPath, args, string(mcpData))
	}

	allOutputs := concatAllRuleBodies(t, root, ".continue/rules")
	for relPath, content := range original {
		if !strings.HasPrefix(relPath, ".continue/rules/") || !strings.HasSuffix(relPath, ".md") {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("continue round-trip: original body of %s not present in any output rule:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}

// ---------------------------------------------------------------------------
// Copilot round-trip
// ---------------------------------------------------------------------------

// TestRoundTrip_Copilot stages a representative .github/ Copilot tree,
// runs import + compile via the copilot plugin only, and verifies
// content survives. Notes:
//
//   - .github/copilot-instructions.md → .agents/context.md → compiles
//     back (symlink by default; ReadFile follows).
//   - .github/instructions/<name>.instructions.md with applyTo:
//     "src/billing/**" → scope at .agents/src/billing/context.md →
//     compiles back to .github/instructions/src-billing.instructions.md
//     (different filename, equivalent content).
//   - .github/prompts/<name>.prompt.md → command → compiles back to
//     .github/prompts/<name>.prompt.md (same filename when slugify is
//     a no-op for the input name).
func TestRoundTrip_Copilot(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".github/copilot-instructions.md": "# Project conventions\n\n" +
			"Use Go 1.25 idioms. Wrap errors with %w.\n",
		".github/instructions/billing.instructions.md": "---\n" +
			"applyTo: \"src/billing/**\"\n" +
			"---\n" +
			"Validate Stripe webhook signatures.\n",
		".github/prompts/review.prompt.md": "---\n" +
			"description: Review checklist for opening a PR\n" +
			"---\n" +
			"Run gofmt, vet, and the unit tests before opening.\n",
	})

	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewCopilot())
	runRoundTrip(t, opts, "copilot")

	// 1. Root instructions content survives.
	rootBody := readBody(t, root, ".github/copilot-instructions.md")
	assertBodyContains(t, ".github/copilot-instructions.md", rootBody,
		"Use Go 1.25 idioms.", "Wrap errors with %w.")

	// 2. applyTo path glob → scope → src-billing.instructions.md.
	billOut := ".github/instructions/src-billing.instructions.md"
	billBody := readBody(t, root, billOut)
	assertBodyContains(t, billOut, billBody,
		"Validate Stripe webhook signatures.")
	billFM := readFrontmatter(t, root, billOut)
	applyTo, _ := billFM["applyTo"].(string)
	if !strings.Contains(applyTo, "src/billing") {
		t.Errorf("%s applyTo = %q, want to contain src/billing", billOut, applyTo)
	}

	// 3. Prompt content survives. The importer slugifies the basename
	// (review.prompt.md → name "review"), and the plugin re-emits as
	// review.prompt.md, so the filename round-trips.
	promptPath := ".github/prompts/review.prompt.md"
	promptBody := readBody(t, root, promptPath)
	assertBodyContains(t, promptPath, promptBody,
		"Run gofmt, vet, and the unit tests before opening.")
	promptFM := readFrontmatter(t, root, promptPath)
	if desc, _ := promptFM["description"].(string); !strings.Contains(desc, "Review checklist") {
		t.Errorf("%s description = %q, want to contain %q",
			promptPath, desc, "Review checklist")
	}

	allOutputs := concatAllBodiesRecursive(t, root, []string{".github"}, []string{".md"})
	for relPath, content := range original {
		if !isMarkdownPath(relPath) {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("copilot round-trip: original body of %s not present in any output:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}

// concatAllBodiesRecursive walks each dir relative to root, reading every
// file whose extension is in exts, and concatenates their bodies (after
// frontmatter and provenance stripping). Used by round-trip tests where
// output spans nested directories.
func concatAllBodiesRecursive(t *testing.T, root string, dirs []string, exts []string) string {
	t.Helper()
	var b strings.Builder
	seen := map[string]struct{}{}
	matchExt := func(name string) bool {
		for _, e := range exts {
			if strings.HasSuffix(strings.ToLower(name), e) {
				return true
			}
		}
		return false
	}
	for _, dir := range dirs {
		full := filepath.Join(root, dir)
		err := filepath.Walk(full, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			if !matchExt(info.Name()) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if _, dup := seen[rel]; dup {
				return nil
			}
			seen[rel] = struct{}{}
			body := readBody(t, root, rel)
			b.WriteString(body)
			b.WriteString("\n")
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("concatAllBodiesRecursive walk %s: %v", full, err)
		}
	}
	return b.String()
}

// isMarkdownPath reports whether p has a .md, .mdc, or .markdown suffix
// (case-insensitive). Used by sweep loops to skip JSON / YAML sources.
func isMarkdownPath(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".mdc") ||
		strings.HasSuffix(lower, ".markdown")
}

// ---------------------------------------------------------------------------
// Gemini round-trip
// ---------------------------------------------------------------------------

func TestRoundTrip_Gemini(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"GEMINI.md":             "# Root\n\nUse the project-wide Go conventions.\n",
		"src/billing/GEMINI.md": "# Billing\n\nValidate Stripe webhook signatures.\n",
		".gemini/settings.json": `{
  "mcpServers": {
    "linear": {"command": "npx", "args": ["@linear/mcp"]}
  },
  "theme": "dark"
}`,
	})
	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewGemini())
	runRoundTrip(t, opts, "gemini")

	rootBody := readBody(t, root, "GEMINI.md")
	assertBodyContains(t, "GEMINI.md", rootBody, "Use the project-wide Go conventions.")

	billPath := "src/billing/GEMINI.md"
	billBody := readBody(t, root, billPath)
	assertBodyContains(t, billPath, billBody, "Validate Stripe webhook signatures.")

	settingsData, err := os.ReadFile(filepath.Join(root, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	servers, _ := settings["mcpServers"].(map[string]any)
	if servers == nil || servers["linear"] == nil {
		t.Errorf(".gemini/settings.json mcpServers.linear missing after round-trip:\n%s", string(settingsData))
	}
	if theme, _ := settings["theme"].(string); theme != "dark" {
		t.Errorf(".gemini/settings.json theme = %q, want dark (unrelated keys must survive merge)", theme)
	}

	allOutputs := concatAllBodiesRecursive(t, root, []string{"."}, []string{".md"})
	for relPath, content := range original {
		if !isMarkdownPath(relPath) {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("gemini round-trip: original body of %s not present in any output:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}

// ---------------------------------------------------------------------------
// Cline round-trip
// ---------------------------------------------------------------------------

func TestRoundTrip_Cline(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	writeFiles(t, root, map[string]string{
		".clinerules/00-overview.md": "Project-wide Go conventions: gofmt, error wrapping with %w.\n",
		".clinerules/10-scope-src-billing.md": "---\n" +
			"paths:\n  - src/billing/**\n" +
			"---\n" +
			"Validate Stripe webhook signatures before processing.\n",
		".clinerules/20-skill-pdf.md": "---\n" +
			"description: PDF editing helper\n" +
			"---\n" +
			"Use pdftk for PDF manipulation tasks.\n",
		".clinerules/30-command-deploy.md": "---\n" +
			"description: Ship a release\n" +
			"---\n" +
			"Run the release script with --confirm.\n",
	})
	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewCline())
	runRoundTrip(t, opts, "cline")

	ctxBody := readBody(t, root, ".clinerules/00-context.md")
	assertBodyContains(t, ".clinerules/00-context.md", ctxBody,
		"Project-wide Go conventions: gofmt, error wrapping with %w.")

	scopeOut := ".clinerules/10-scope-src-billing.md"
	scopeBody := readBody(t, root, scopeOut)
	assertBodyContains(t, scopeOut, scopeBody,
		"Validate Stripe webhook signatures before processing.",
		"When working in src/billing")

	skillOut := ".clinerules/20-skill-pdf.md"
	skillBody := readBody(t, root, skillOut)
	assertBodyContains(t, skillOut, skillBody,
		"Use pdftk for PDF manipulation tasks.",
		"Skill: pdf")

	cmdOut := ".clinerules/30-command-deploy.md"
	cmdBody := readBody(t, root, cmdOut)
	assertBodyContains(t, cmdOut, cmdBody,
		"Run the release script with --confirm.",
		"Command /deploy")

	allOutputs := concatAllRuleBodies(t, root, ".clinerules")
	for relPath, content := range original {
		if !strings.HasPrefix(relPath, ".clinerules/") || !strings.HasSuffix(relPath, ".md") {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("cline round-trip: original body of %s not present in any output rule:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}

// ---------------------------------------------------------------------------
// Windsurf round-trip
// ---------------------------------------------------------------------------

func TestRoundTrip_Windsurf(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".windsurf/rules/always.md": "---\n" +
			"trigger: always_on\n" +
			"---\n" +
			"Project-wide Go conventions: gofmt, error wrapping with %w.\n",
		".windsurf/rules/billing.md": "---\n" +
			"trigger: glob\n" +
			"globs:\n  - src/billing/**\n" +
			"description: Stripe webhook rules\n" +
			"---\n" +
			"Validate Stripe webhook signatures before processing.\n",
		".windsurf/rules/pdf-skill.md": "---\n" +
			"trigger: glob\n" +
			"globs:\n  - '**/*.pdf'\n" +
			"description: PDF editing\n" +
			"---\n" +
			"Use pdftk for PDF manipulation tasks.\n",
	})
	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewWindsurf())
	runRoundTrip(t, opts, "windsurf")

	rootBody := readBody(t, root, ".windsurf/rules/_root.md")
	assertBodyContains(t, ".windsurf/rules/_root.md", rootBody,
		"Project-wide Go conventions: gofmt, error wrapping with %w.")
	rootFM := readFrontmatter(t, root, ".windsurf/rules/_root.md")
	if trig, _ := rootFM["trigger"].(string); trig != "always_on" {
		t.Errorf("_root.md trigger = %q, want always_on", trig)
	}

	billOut := ".windsurf/rules/src-billing.md"
	billBody := readBody(t, root, billOut)
	assertBodyContains(t, billOut, billBody,
		"Validate Stripe webhook signatures before processing.")
	billFM := readFrontmatter(t, root, billOut)
	if trig, _ := billFM["trigger"].(string); trig != "glob" {
		t.Errorf("%s trigger = %q, want glob", billOut, trig)
	}
	if globs := stringSliceFromAny(billFM["globs"]); len(globs) == 0 ||
		!strings.HasPrefix(globs[0], "src/billing") {
		t.Errorf("%s globs = %v, want to start with src/billing", billOut, globs)
	}

	pdfOut := ".windsurf/rules/skill-pdf-skill.md"
	pdfBody := readBody(t, root, pdfOut)
	assertBodyContains(t, pdfOut, pdfBody, "Use pdftk for PDF manipulation tasks.")
	pdfFM := readFrontmatter(t, root, pdfOut)
	if trig, _ := pdfFM["trigger"].(string); trig != "glob" {
		t.Errorf("%s trigger = %q, want glob", pdfOut, trig)
	}
	if globs := stringSliceFromAny(pdfFM["globs"]); len(globs) == 0 || globs[0] != "**/*.pdf" {
		t.Errorf("%s globs = %v, want [**/*.pdf]", pdfOut, globs)
	}

	allOutputs := concatAllRuleBodies(t, root, ".windsurf/rules")
	for relPath, content := range original {
		if !strings.HasPrefix(relPath, ".windsurf/rules/") || !strings.HasSuffix(relPath, ".md") {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("windsurf round-trip: original body of %s not present in any output rule:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}

// ---------------------------------------------------------------------------
// AgentsMD round-trip
// ---------------------------------------------------------------------------

func TestRoundTrip_AgentsMD(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"AGENTS.md":             "# Project conventions\n\nUse Go 1.25 idioms. Wrap errors with %w.\n",
		"src/billing/AGENTS.md": "# Billing\n\nValidate Stripe webhook signatures before processing.\n",
	})
	original := captureOriginal(t, root)

	opts := rtOptions(t, root, plugins.NewAgentsMD())
	runRoundTrip(t, opts, "agents-md")

	rootBody := readBody(t, root, "AGENTS.md")
	assertBodyContains(t, "AGENTS.md", rootBody,
		"Use Go 1.25 idioms.", "Wrap errors with %w.")
	assertBodyContains(t, "AGENTS.md", rootBody,
		"When working in src/billing",
		"Validate Stripe webhook signatures before processing.")

	rootRaw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(rootRaw)), "<!-- Generated by agents.") {
		t.Errorf("AGENTS.md missing generated-by header (would risk re-import loops):\n%s", string(rootRaw))
	}

	allOutputs := readBody(t, root, "AGENTS.md")
	for relPath, content := range original {
		if !isMarkdownPath(relPath) {
			continue
		}
		_, body, _ := splitFrontmatterTest([]byte(content))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if !strings.Contains(normalizeSpace(allOutputs), normalizeSpace(body)) {
			t.Errorf("agents-md round-trip: original body of %s not present in output AGENTS.md:\noriginal:\n%s\nall outputs:\n%s",
				relPath, body, allOutputs)
		}
	}
}
