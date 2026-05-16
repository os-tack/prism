package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"agents.dev/agents/internal/engine"
	"agents.dev/agents/internal/lockfile"
	"agents.dev/agents/internal/plugin"
	"agents.dev/agents/plugins"
)

// setupFixture creates an isolated project root containing a canonical
// .agents/ directory plus the .claude/ and .cursor/ marker directories that
// the Claude and Cursor plugins look for in Detect. It returns the root.
func setupFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustMkdir := func(dirs ...string) {
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", d, err)
			}
		}
	}

	mustWrite := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustMkdir(".claude", ".cursor")
	mustWrite(".agents/context.md", "# Root\n\nGlobal context.\n")
	mustWrite(".agents/src/billing/context.md", "# Billing\n\nWebhook stuff.\n")
	mustWrite(".agents/src/billing/scopes.yaml", "description: Stripe webhooks\npriority: high\n")
	return root
}

// newOptions builds an engine.Options with all three real plugins registered.
func newOptions(t *testing.T, root string) engine.Options {
	t.Helper()
	reg := plugin.NewRegistry()
	reg.Register(plugins.NewClaude())
	reg.Register(plugins.NewCursor())
	reg.Register(plugins.NewAgentsMD())
	return engine.Options{
		Root:     root,
		Registry: reg,
		DryRun:   false,
		Quiet:    true,
	}
}

// mustExist fails the test if the path does not exist (Lstat, so symlinks ok).
func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

// mustNotExist fails the test if the path exists.
func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("expected %s NOT to exist", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected error stat %s: %v", path, err)
	}
}

func TestCompile_FullProjection(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if rep.Changed != 5 {
		t.Errorf("Changed = %d, want 5; ops=%+v", rep.Changed, rep.Operations)
	}
	if rep.Unchanged != 0 {
		t.Errorf("Unchanged = %d, want 0", rep.Unchanged)
	}
	if len(rep.Warnings) < 1 {
		t.Errorf("Warnings = %d, want >=1", len(rep.Warnings))
	}

	// Expected output files.
	expected := []string{
		"AGENTS.md",
		"CLAUDE.md",
		"src/billing/CLAUDE.md",
		".cursor/rules/_root.mdc",
		".cursor/rules/src-billing.mdc",
	}
	for _, p := range expected {
		mustExist(t, filepath.Join(root, p))
	}

	// Symlinks: CLAUDE.md and src/billing/CLAUDE.md should be symlinks.
	for _, p := range []string{"CLAUDE.md", "src/billing/CLAUDE.md"} {
		full := filepath.Join(root, p)
		fi, err := os.Lstat(full)
		if err != nil {
			t.Fatalf("Lstat %s: %v", full, err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s should be a symlink, mode=%v", p, fi.Mode())
		}
	}

	// AGENTS.md content checks.
	agentsMD, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	s := string(agentsMD)
	for _, sub := range []string{"Global context", "Webhook stuff", "## When working in src/billing"} {
		if !strings.Contains(s, sub) {
			t.Errorf("AGENTS.md missing substring %q\nfull content:\n%s", sub, s)
		}
	}
}

func TestCompile_Idempotent(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("first Compile: %v", err)
	}

	// Snapshot mtimes for projected files.
	files := []string{
		"AGENTS.md",
		"CLAUDE.md",
		"src/billing/CLAUDE.md",
		".cursor/rules/_root.mdc",
		".cursor/rules/src-billing.mdc",
	}
	before := make(map[string]os.FileInfo, len(files))
	for _, f := range files {
		fi, err := os.Lstat(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("Lstat %s: %v", f, err)
		}
		before[f] = fi
	}

	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("second Compile: %v", err)
	}
	if rep.Changed != 0 {
		t.Errorf("second Compile Changed = %d, want 0; ops=%+v", rep.Changed, rep.Operations)
	}
	if rep.Unchanged != 5 {
		t.Errorf("second Compile Unchanged = %d, want 5", rep.Unchanged)
	}

	for _, f := range files {
		fi, err := os.Lstat(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("Lstat after %s: %v", f, err)
		}
		if !fi.ModTime().Equal(before[f].ModTime()) {
			t.Errorf("%s mtime changed: before=%v after=%v", f, before[f].ModTime(), fi.ModTime())
		}
	}
}

func TestCompile_DryRun(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)
	opts.DryRun = true

	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("Compile dry: %v", err)
	}
	if rep.Changed != 5 {
		t.Errorf("Changed = %d, want 5", rep.Changed)
	}

	for _, p := range []string{
		"AGENTS.md",
		"CLAUDE.md",
		"src/billing/CLAUDE.md",
		".cursor/rules/_root.mdc",
		".cursor/rules/src-billing.mdc",
	} {
		mustNotExist(t, filepath.Join(root, p))
	}
	mustNotExist(t, filepath.Join(root, ".agents", ".lock"))
}

func TestCheck_CleanState(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	rep, err := engine.Check(opts)
	if err != nil {
		t.Fatalf("Check returned %v, want nil", err)
	}
	if rep.Changed != 0 {
		t.Errorf("Check after Compile Changed = %d, want 0", rep.Changed)
	}
}

func TestCheck_DriftDetected(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	src := filepath.Join(root, ".agents", "src", "billing", "context.md")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if err := os.WriteFile(src, append(data, []byte("\nMore details.\n")...), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}

	rep, err := engine.Check(opts)
	if !errors.Is(err, engine.ErrDrift) {
		t.Fatalf("Check error = %v, want ErrDrift", err)
	}
	if rep == nil || rep.Changed == 0 {
		t.Errorf("Check report Changed should be > 0; rep=%+v", rep)
	}
}

func TestCompile_AppliesDriftAndCheckClears(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile 1: %v", err)
	}

	src := filepath.Join(root, ".agents", "src", "billing", "context.md")
	const marker = "Specifically handles Stripe.\n"
	if err := os.WriteFile(src, []byte("# Billing\n\nWebhook stuff.\n\n"+marker), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}

	if _, err := engine.Check(opts); !errors.Is(err, engine.ErrDrift) {
		t.Fatalf("Check before recompile = %v, want ErrDrift", err)
	}

	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile 2: %v", err)
	}

	if _, err := engine.Check(opts); err != nil {
		t.Fatalf("Check after recompile: %v", err)
	}

	agentsMD, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(agentsMD), "Specifically handles Stripe.") {
		t.Errorf("AGENTS.md does not reflect edit:\n%s", string(agentsMD))
	}
}

func TestWhich_TracksSources(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)
	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	srcs, err := engine.Which(opts, "AGENTS.md")
	if err != nil {
		t.Fatalf("Which AGENTS.md: %v", err)
	}
	want := map[string]bool{
		"context.md":             false,
		"src/billing/context.md": false,
	}
	for _, s := range srcs {
		s = filepath.ToSlash(s)
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("Which(AGENTS.md): missing source %q; got %v", k, srcs)
		}
	}

	srcs, err = engine.Which(opts, "CLAUDE.md")
	if err != nil {
		t.Fatalf("Which CLAUDE.md: %v", err)
	}
	if len(srcs) != 1 || filepath.ToSlash(srcs[0]) != "context.md" {
		t.Errorf("Which(CLAUDE.md) = %v, want [context.md]", srcs)
	}

	srcs, err = engine.Which(opts, "src/billing/CLAUDE.md")
	if err != nil {
		t.Fatalf("Which src/billing/CLAUDE.md: %v", err)
	}
	if len(srcs) != 1 || filepath.ToSlash(srcs[0]) != "src/billing/context.md" {
		t.Errorf("Which(src/billing/CLAUDE.md) = %v, want [src/billing/context.md]", srcs)
	}
}

func TestWhich_NotTracked(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)
	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := engine.Which(opts, "does/not/exist.md"); err == nil {
		t.Fatalf("Which on missing path should error")
	}
}

func TestWhich_AbsolutePath(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)
	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	relSrcs, err := engine.Which(opts, "AGENTS.md")
	if err != nil {
		t.Fatalf("Which rel: %v", err)
	}
	absSrcs, err := engine.Which(opts, filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("Which abs: %v", err)
	}

	sort.Strings(relSrcs)
	sort.Strings(absSrcs)
	if len(relSrcs) != len(absSrcs) {
		t.Fatalf("len mismatch rel=%v abs=%v", relSrcs, absSrcs)
	}
	for i := range relSrcs {
		if relSrcs[i] != absSrcs[i] {
			t.Errorf("rel vs abs mismatch at %d: %q vs %q", i, relSrcs[i], absSrcs[i])
		}
	}
}

func TestCompile_StaleCleanup(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)
	if _, err := engine.Compile(opts); err != nil {
		t.Fatalf("Compile 1: %v", err)
	}
	mustExist(t, filepath.Join(root, "src/billing/CLAUDE.md"))

	if err := os.RemoveAll(filepath.Join(root, ".agents", "src")); err != nil {
		t.Fatalf("remove billing scope: %v", err)
	}

	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("Compile 2: %v", err)
	}
	if rep.Removed == 0 {
		t.Errorf("Removed = 0, want > 0; ops=%+v", rep.Operations)
	}

	// The billing CLAUDE.md should be gone.
	mustNotExist(t, filepath.Join(root, "src/billing/CLAUDE.md"))
	mustNotExist(t, filepath.Join(root, ".cursor/rules/src-billing.mdc"))

	// Lockfile should no longer track them.
	lf, err := lockfile.Load(root)
	if err != nil {
		t.Fatalf("lockfile.Load: %v", err)
	}
	if _, ok := lf.Files["src/billing/CLAUDE.md"]; ok {
		t.Errorf("lockfile still tracks src/billing/CLAUDE.md")
	}
	if _, ok := lf.Files[".cursor/rules/src-billing.mdc"]; ok {
		t.Errorf("lockfile still tracks .cursor/rules/src-billing.mdc")
	}

	// AGENTS.md should be re-rendered without the billing section.
	agentsMD, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if strings.Contains(string(agentsMD), "When working in src/billing") {
		t.Errorf("AGENTS.md still has billing section:\n%s", string(agentsMD))
	}
}

func TestCompile_NoAgentsDir(t *testing.T) {
	root := t.TempDir()
	opts := newOptions(t, root)
	_, err := engine.Compile(opts)
	if !errors.Is(err, engine.ErrNoAgentsDir) {
		t.Fatalf("Compile error = %v, want ErrNoAgentsDir", err)
	}
}

func TestInit_FromScratch(t *testing.T) {
	root := t.TempDir()
	opts := newOptions(t, root)

	if err := engine.Init(opts, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctxPath := filepath.Join(root, ".agents", "context.md")
	cfgPath := filepath.Join(root, ".agents", "agents.config.yaml")

	ctxData, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("read context.md: %v", err)
	}
	if len(ctxData) == 0 {
		t.Errorf("context.md is empty")
	}
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read agents.config.yaml: %v", err)
	}
	if len(cfgData) == 0 {
		t.Errorf("agents.config.yaml is empty")
	}

	// Second Init should refuse to clobber.
	if err := engine.Init(opts, ""); err == nil {
		t.Fatalf("second Init should error")
	}
}

func TestInit_FromClaude(t *testing.T) {
	root := t.TempDir()
	opts := newOptions(t, root)

	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("ROOT CLAUDE"), 0o644); err != nil {
		t.Fatalf("write root CLAUDE.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src", "auth"), 0o755); err != nil {
		t.Fatalf("mkdir src/auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "auth", "CLAUDE.md"), []byte("AUTH CLAUDE"), 0o644); err != nil {
		t.Fatalf("write src/auth CLAUDE.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "CLAUDE.md"), []byte("SKIP ME"), 0o644); err != nil {
		t.Fatalf("write .git CLAUDE.md: %v", err)
	}

	if err := engine.Init(opts, "claude"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	rootCtx, err := os.ReadFile(filepath.Join(root, ".agents", "context.md"))
	if err != nil {
		t.Fatalf("read root context.md: %v", err)
	}
	if !strings.Contains(string(rootCtx), "ROOT CLAUDE") {
		t.Errorf("root .agents/context.md does not contain ROOT CLAUDE: %q", string(rootCtx))
	}

	authCtx, err := os.ReadFile(filepath.Join(root, ".agents", "src", "auth", "context.md"))
	if err != nil {
		t.Fatalf("read auth context.md: %v", err)
	}
	if !strings.Contains(string(authCtx), "AUTH CLAUDE") {
		t.Errorf("auth context.md does not contain AUTH CLAUDE: %q", string(authCtx))
	}

	gitImported := filepath.Join(root, ".agents", ".git", "context.md")
	mustNotExist(t, gitImported)
}

func TestCompile_TargetsFilter(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)
	opts.Targets = []string{"claude"}

	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// claude emits 2 ops: CLAUDE.md and src/billing/CLAUDE.md
	if rep.Changed != 2 {
		t.Errorf("Changed = %d, want 2; ops=%+v", rep.Changed, rep.Operations)
	}

	mustExist(t, filepath.Join(root, "CLAUDE.md"))
	mustExist(t, filepath.Join(root, "src/billing/CLAUDE.md"))
	mustNotExist(t, filepath.Join(root, "AGENTS.md"))
	mustNotExist(t, filepath.Join(root, ".cursor/rules/_root.mdc"))
	mustNotExist(t, filepath.Join(root, ".cursor/rules/src-billing.mdc"))
}

func TestCompile_DisabledTarget(t *testing.T) {
	root := setupFixture(t)
	opts := newOptions(t, root)

	cfg := "targets: []\ntarget_options:\n  cursor:\n    disabled: true\n"
	if err := os.WriteFile(filepath.Join(root, ".agents", "agents.config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rep, err := engine.Compile(opts)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// claude (2) + agents-md (1) = 3
	if rep.Changed != 3 {
		t.Errorf("Changed = %d, want 3; ops=%+v", rep.Changed, rep.Operations)
	}

	mustExist(t, filepath.Join(root, "AGENTS.md"))
	mustExist(t, filepath.Join(root, "CLAUDE.md"))
	mustExist(t, filepath.Join(root, "src/billing/CLAUDE.md"))
	mustNotExist(t, filepath.Join(root, ".cursor/rules/_root.mdc"))
	mustNotExist(t, filepath.Join(root, ".cursor/rules/src-billing.mdc"))
}
