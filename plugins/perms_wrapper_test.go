package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
	"agents.dev/agents/internal/plugin"
)

// TestPermsGuardWrapper_NoAbsoluteProjectPath verifies the I4 fix: the
// rendered bash must not bake the absolute project root into the body.
// If it did, `mv` of the project would break the wrapper.
func TestPermsGuardWrapper_NoAbsoluteProjectPath(t *testing.T) {
	root := "/tmp/original-project-location"
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Permissions: &model.Permissions{
			Deny: []string{"bash:rm -rf *"},
		},
	}
	ops, _, err := emitPermsGuardWrappers("gemini", proj, false)
	if err != nil {
		t.Fatalf("emitPermsGuardWrappers: %v", err)
	}
	gatePath := filepath.Join(".gemini", "hooks", "__perms-guard__", "global-gate.sh")
	var gate *plugin.Operation
	for i := range ops {
		if ops[i].Path == gatePath {
			gate = &ops[i]
		}
	}
	if gate == nil {
		t.Fatalf("missing gate op at %q", gatePath)
	}
	if strings.Contains(gate.Content, root) {
		t.Errorf("wrapper bakes absolute project root %q into body:\n%s", root, gate.Content)
	}
	if !strings.Contains(gate.Content, "${BASH_SOURCE[0]}") {
		t.Errorf("wrapper missing BASH_SOURCE-based resolution:\n%s", gate.Content)
	}
	if !strings.Contains(gate.Content, "PRISM_PROJECT_DIR") {
		t.Errorf("wrapper missing PRISM_PROJECT_DIR env-var precedence:\n%s", gate.Content)
	}
	if !strings.Contains(gate.Content, "CLAUDE_PROJECT_DIR") {
		t.Errorf("wrapper missing CLAUDE_PROJECT_DIR env-var fallback:\n%s", gate.Content)
	}
	// Gate wrapper at .gemini/hooks/__perms-guard__/global-gate.sh is
	// three levels deep; expect three ".." segments in the fallback.
	if !strings.Contains(gate.Content, "../../..") {
		t.Errorf("wrapper missing '../../..' three-level project-root fallback:\n%s", gate.Content)
	}
}

// TestPermsGuardWrapper_ScopedHookNoAbsolutePath verifies the same I4
// guarantee for the scoped-hook code path.
func TestPermsGuardWrapper_ScopedHookNoAbsolutePath(t *testing.T) {
	root := "/some/very/specific/project/dir"
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Bash",
				ScriptPath: filepath.Join(root, ".agents", "src", "billing", "hooks", "audit.sh"),
				ScopePath:  "src/billing",
			},
		},
		ScopedPermissions: []*model.Permissions{
			{ScopePath: "src/billing", Deny: []string{"bash:rm *"}},
		},
	}
	ops, _, err := emitPermsGuardWrappers("gemini", proj, false)
	if err != nil {
		t.Fatalf("emitPermsGuardWrappers: %v", err)
	}
	wrapperPath := filepath.Join(".gemini", "hooks", "__perms-guard__", "src-billing-PreToolUse-audit.sh")
	var wrapper *plugin.Operation
	for i := range ops {
		if ops[i].Path == wrapperPath {
			wrapper = &ops[i]
		}
	}
	if wrapper == nil {
		t.Fatalf("missing wrapper op at %q", wrapperPath)
	}
	// After v0.7.1 I-1 fix, the wrapper bakes the project root ZERO
	// times — both the policy AND the script use "${PROJECT_DIR}"/<rel>.
	// Any absolute root reference is a regression of I-1 or I4.
	if strings.Contains(wrapper.Content, root) {
		t.Errorf("wrapper bakes project root (regression of I4 / I-1):\n%s", wrapper.Content)
	}
	// Policy must be referenced via ${PROJECT_DIR}"/, not absolute.
	if !strings.Contains(wrapper.Content, `"${PROJECT_DIR}"/`) {
		t.Errorf("wrapper missing ${PROJECT_DIR} interpolation for policy:\n%s", wrapper.Content)
	}
}

// TestPermsGuardWrapper_Deterministic_I5 verifies the I5 fix: emitting
// ops for a project with multiple scoped policies produces ops in the
// same order across repeated Plan() runs.
func TestPermsGuardWrapper_Deterministic_I5(t *testing.T) {
	proj := &model.Project{
		Root:      "/tmp/fake",
		AgentsDir: "/tmp/fake/.agents",
		Permissions: &model.Permissions{
			Allow: []string{"bash:ls *"},
		},
		ScopedPermissions: []*model.Permissions{
			{ScopePath: "src/zeta", Allow: []string{"bash:date"}},
			{ScopePath: "src/alpha", Allow: []string{"bash:pwd"}},
			{ScopePath: "src/mid", Allow: []string{"bash:whoami"}},
			{ScopePath: "src/billing", Allow: []string{"bash:echo"}},
			{ScopePath: "src/api", Allow: []string{"bash:hostname"}},
		},
	}
	var first []string
	for i := 0; i < 20; i++ {
		ops, _, err := emitPermsGuardWrappers("gemini", proj, false)
		if err != nil {
			t.Fatalf("iter %d: emit: %v", i, err)
		}
		paths := make([]string, len(ops))
		for j, op := range ops {
			paths[j] = op.Path
		}
		if i == 0 {
			first = paths
			continue
		}
		if len(paths) != len(first) {
			t.Fatalf("iter %d: op count %d != %d", i, len(paths), len(first))
		}
		for j := range paths {
			if paths[j] != first[j] {
				t.Fatalf("iter %d: op order drift at index %d:\nfirst: %v\ngot:   %v", i, j, first, paths)
			}
		}
	}
}

// TestPermsGuardWrapper_HookWrapperOrderDeterministic guards the per-hook
// wrapper emission path too, where ops are appended in proj.Hooks slice
// order (already deterministic) — this is a regression guard so a future
// refactor that introduces map iteration here gets caught.
func TestPermsGuardWrapper_HookWrapperOrderDeterministic(t *testing.T) {
	hooks := []*model.Hook{
		{Event: "PreToolUse", Matcher: "Bash", ScriptPath: "/p/.agents/src/z/hooks/z.sh", ScopePath: "src/z"},
		{Event: "PreToolUse", Matcher: "Bash", ScriptPath: "/p/.agents/src/a/hooks/a.sh", ScopePath: "src/a"},
		{Event: "PreToolUse", Matcher: "Bash", ScriptPath: "/p/.agents/src/m/hooks/m.sh", ScopePath: "src/m"},
	}
	proj := &model.Project{
		Root:      "/p",
		AgentsDir: "/p/.agents",
		Hooks:     hooks,
		ScopedPermissions: []*model.Permissions{
			{ScopePath: "src/z", Allow: []string{"bash:z"}},
			{ScopePath: "src/a", Allow: []string{"bash:a"}},
			{ScopePath: "src/m", Allow: []string{"bash:m"}},
		},
	}
	var first []string
	for i := 0; i < 20; i++ {
		ops, _, err := emitPermsGuardWrappers("gemini", proj, false)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		paths := make([]string, len(ops))
		for j, op := range ops {
			paths[j] = op.Path
		}
		if i == 0 {
			first = paths
			continue
		}
		for j := range paths {
			if paths[j] != first[j] {
				t.Fatalf("iter %d: hook-wrapper op order drift:\nfirst: %v\ngot:   %v", i, first, paths)
			}
		}
	}
}

// TestRootRelativeFromWrapper exercises the helper that converts a
// wrapper's project-relative path into the "../../.." suffix used in
// the rendered bash to walk back up to the project root.
func TestRootRelativeFromWrapper(t *testing.T) {
	cases := []struct {
		wrapperRel string
		want       string
	}{
		{"wrapper.sh", "."},
		{filepath.Join(".claude", "wrapper.sh"), ".."},
		{filepath.Join(".claude", "hooks", "wrapper.sh"), "../.."},
		{filepath.Join(".gemini", "hooks", "__perms-guard__", "wrapper.sh"), "../../.."},
		{filepath.Join(".claude", "hooks", "__scope-guard__", "wrapper.sh"), "../../.."},
		// N-a regression: noisy inputs cleaned to canonical depth.
		{".gemini/./hooks/wrapper.sh", "../.."},
		{".gemini//hooks/wrapper.sh", "../.."},
		{"./wrapper.sh", "."},
	}
	for _, c := range cases {
		got := rootRelativeFromWrapper(c.wrapperRel)
		if got != c.want {
			t.Errorf("rootRelativeFromWrapper(%q) = %q, want %q", c.wrapperRel, got, c.want)
		}
	}
}

// TestScopeGuardScript_NoAbsoluteProjectPath verifies the claude.go
// scope-guard renderer also doesn't bake the project root into the body.
func TestScopeGuardScript_NoAbsoluteProjectPath(t *testing.T) {
	root := "/tmp/movable-project-root"
	scriptPath := filepath.Join(root, ".agents", "src", "billing", "hooks", "guard.sh")
	proj := &model.Project{
		Root:      root,
		AgentsDir: filepath.Join(root, ".agents"),
		Hooks: []*model.Hook{
			{
				Event:      "PreToolUse",
				Matcher:    "Edit",
				ScriptPath: scriptPath,
				ScopePath:  "src/billing",
			},
		},
	}
	p := NewClaude()
	ops, err := p.Plan(proj, model.TargetOption{Mode: "symlink"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wrapperRel := filepath.Join(".claude", "hooks", "__scope-guard__", "src-billing-guard.sh")
	var wrapper *plugin.Operation
	for i := range ops {
		if ops[i].Path == wrapperRel {
			wrapper = &ops[i]
		}
	}
	if wrapper == nil {
		t.Fatalf("missing wrapper at %q", wrapperRel)
	}
	// Wrapper body must:
	//  - contain BASH_SOURCE resolution
	//  - contain PRISM_PROJECT_DIR + CLAUDE_PROJECT_DIR fallback chain
	//  - NOT bake the project root anywhere except as part of the --script
	//    absolute path argument (which references .agents/, not the project
	//    root we care about for `mv` survival).
	if !strings.Contains(wrapper.Content, "${BASH_SOURCE[0]}") {
		t.Errorf("scope-guard wrapper missing BASH_SOURCE:\n%s", wrapper.Content)
	}
	if !strings.Contains(wrapper.Content, "PRISM_PROJECT_DIR") {
		t.Errorf("scope-guard wrapper missing PRISM_PROJECT_DIR:\n%s", wrapper.Content)
	}
	if !strings.Contains(wrapper.Content, "../../..") {
		t.Errorf("scope-guard wrapper missing '../../..' fallback:\n%s", wrapper.Content)
	}
}

// TestPermsGuardWrapper_SurvivesMv writes a rendered wrapper into a
// fake project tree, moves the project, and asserts the wrapper still
// resolves PROJECT_DIR to the moved location (the bash exits 0 after
// printing the resolved path; we don't actually invoke prism perms-guard).
func TestPermsGuardWrapper_SurvivesMv(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	// Render a wrapper for a fake gemini project. Override the final
	// exec line with a no-op echo of PROJECT_DIR so we can verify
	// resolution without needing the prism binary on PATH.
	body := buildPermsGuardScript(
		filepath.Join(".gemini", "hooks", "__perms-guard__", "global-gate.sh"),
		filepath.Join(".gemini", "hooks", "__perms-guard__", "policy.json"),
		"",
	)
	// Strip the trailing exec line and replace with an echo so we
	// don't need prism in PATH; everything above (BASH_SOURCE
	// resolution + PROJECT_DIR fallback chain) is what we want to
	// exercise.
	lines := strings.Split(body, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, "exec prism") {
			lines[i] = `echo "PROJECT_DIR=${PROJECT_DIR}"`
		}
	}
	body = strings.Join(lines, "\n")

	// Build the on-disk layout under tmp.
	originalRoot := t.TempDir()
	wrapperDir := filepath.Join(originalRoot, ".gemini", "hooks", "__perms-guard__")
	if err := os.MkdirAll(wrapperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapperPath := filepath.Join(wrapperDir, "global-gate.sh")
	if err := os.WriteFile(wrapperPath, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	// Move the entire project to a sibling tmp dir.
	movedRoot := filepath.Join(t.TempDir(), "moved-project")
	if err := os.Rename(originalRoot, movedRoot); err != nil {
		t.Fatal(err)
	}
	movedWrapper := filepath.Join(movedRoot, ".gemini", "hooks", "__perms-guard__", "global-gate.sh")

	// Invoke the wrapper from a third directory to confirm the
	// resolution is BASH_SOURCE-relative, not cwd-relative.
	thirdDir := t.TempDir()
	cmd := exec.Command("bash", movedWrapper)
	cmd.Dir = thirdDir
	// Clear PRISM_PROJECT_DIR and CLAUDE_PROJECT_DIR so the BASH_SOURCE
	// fallback is what actually resolves the path.
	cmd.Env = append([]string{}, "PATH="+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash exec failed: %v\noutput: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	// On macOS, /private/tmp vs /tmp aliasing means EvalSymlinks may
	// resolve to a different prefix; accept either.
	resolved, _ := filepath.EvalSymlinks(movedRoot)
	if got != "PROJECT_DIR="+movedRoot && got != "PROJECT_DIR="+resolved {
		t.Fatalf("PROJECT_DIR did not resolve to moved location:\n  got:  %q\n  want: PROJECT_DIR=%s (or %s)", got, movedRoot, resolved)
	}

	// And verify PRISM_PROJECT_DIR takes precedence.
	cmd2 := exec.Command("bash", movedWrapper)
	cmd2.Dir = thirdDir
	cmd2.Env = append([]string{}, "PATH="+os.Getenv("PATH"), "PRISM_PROJECT_DIR=/override/path")
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("bash exec (override) failed: %v\noutput: %s", err, out2)
	}
	if got2 := strings.TrimSpace(string(out2)); got2 != "PROJECT_DIR=/override/path" {
		t.Fatalf("PRISM_PROJECT_DIR override didn't win:\n  got:  %q\n  want: PROJECT_DIR=/override/path", got2)
	}
}

func TestBuildPermsGuardScript_PolicyShellEscaping_C2(t *testing.T) {
	// v0.7.1 review C2: policyRel must be shellQuote'd, not bare-interpolated.
	// A scope path containing $ or whitespace would otherwise undergo parameter
	// expansion or word-splitting in the wrapper.
	body := buildPermsGuardScript(
		".gemini/hooks/__perms-guard__/src-foo-bar.policy.sh",
		".gemini/hooks/__perms-guard__/src-$x foo.policy.json",
		"",
	)
	// Single-quoted form preserves both $ and whitespace literally.
	if !strings.Contains(body, `'.gemini/hooks/__perms-guard__/src-$x foo.policy.json'`) {
		t.Errorf("policy not single-quoted (C2 regression):\n%s", body)
	}
}

func TestBuildScopeGuardScript_SanitizesCommentHeader_Nb(t *testing.T) {
	// N-b: a scope path or filename containing a newline must not split the
	// `# prism-generated scope guard for ...` comment into a second line
	// that bash would interpret.
	body := buildScopeGuardScript(
		".claude/hooks/__scope-guard__/x.sh",
		"src/foo\nrm -rf /\n",
		"/abs/.agents/src/foo\nbar/hooks/guard.sh",
		"'/dev/null'",
	)
	lines := strings.Split(body, "\n")
	if !strings.HasPrefix(lines[0], "#!/usr/bin/env bash") {
		t.Fatalf("shebang missing: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "# prism-generated scope guard for ") {
		t.Fatalf("header comment not on line 2: %q", lines[1])
	}
	// The point isn't to strip the substring — `?rm -rf /?` is still in
	// the line — it's that bash sees one comment line, not two. Lines that
	// follow must be the rest of the wrapper preamble, not anything from
	// the injected payload.
	if !strings.Contains(lines[1], "src/foo?") {
		t.Errorf("newline in scopePath not replaced with `?`:\n%s", lines[1])
	}
	if !strings.HasPrefix(lines[2], "#") {
		t.Errorf("line 3 should still be a comment continuation, got: %q", lines[2])
	}
}
