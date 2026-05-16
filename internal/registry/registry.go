// Package registry implements `agents add` / `agents remove` / `agents list`
// for prism v0.5.
//
// A "package" is a directory of canonical `.agents/`-shaped content
// (`skills/foo/SKILL.md`, `commands/bar.md`, etc.) plus an optional
// `package.yaml` manifest. Installing a package copies its declared contents
// into the project's `.agents/` tree and records the operation in
// `.agents/packages.yaml`.
//
// Sources (v0.5):
//   - github.com/owner/repo[/subpath][@ref]  → git clone, optional checkout
//   - ./relative/path or /absolute/path      → directory copy
//
// The on-disk bookkeeping schema (.agents/packages.yaml):
//
//	packages:
//	  pdf-editing:
//	    source: github.com/anthropic/skills/pdf-editing
//	    ref: v1.2.0
//	    sha: a1b2c3d4...
//	    installed_at: 2026-05-16T10:00:00Z
//	    target: skills/pdf-editing
//	    files:
//	      - skills/pdf-editing/SKILL.md
//	      - skills/pdf-editing/scripts/pdfgen.sh
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agents.dev/agents/internal/model"
)

// Sentinel errors. Callers compare with errors.Is.
var (
	ErrAlreadyInstalled = errors.New("package already installed")
	ErrSchemaMismatch   = errors.New("package schema not supported")
	ErrPathTraversal    = errors.New("package contains unsafe path")
	ErrNoAgentsDir      = errors.New("no .agents/ directory")
)

// InstallOptions controls Install behavior.
type InstallOptions struct {
	// Ref pins a git ref (tag / branch / sha). Ignored for local sources.
	// If set, it takes precedence over any "@ref" suffix on the source URL.
	Ref string
	// Target overrides the install target directory under .agents/.
	// Empty falls back to the package's declared target (or skills/<name>).
	Target string
	// Global selects ~/.agents/ vs the project root. (Wired by callers; the
	// registry itself just respects whatever projectRoot it is given.)
	Global bool
	// Force allows reinstalling over an existing package entry.
	Force bool
	// Yes auto-confirms any interactive prompts. The registry itself is
	// non-interactive; this is a CLI hint plumbed through for completeness.
	Yes bool
}

// Install installs a package from `source` into projectRoot.
//
// The flow is:
//  1. Resolve source → a directory on disk (clone for git, identity for local).
//  2. Load + validate package.yaml (synthesize one if missing).
//  3. Stage files in a temp dir to keep partial failures off the project tree.
//  4. Move staged files into place, compute hashes, build the Package record.
//  5. Read-modify-write .agents/packages.yaml.
//
// Returns the recorded *model.Package on success.
func Install(projectRoot, source string, opts InstallOptions) (*model.Package, error) {
	if !agentsDirExists(projectRoot) {
		return nil, fmt.Errorf("%w: %s", ErrNoAgentsDir, filepath.Join(projectRoot, ".agents"))
	}

	// 1. Materialize source onto local disk.
	pkgRoot, cleanup, err := materializeSource(source, opts.Ref)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// 2. Load + validate manifest. A missing manifest is treated as a single
	// "whole directory" package, which is friendly for ad-hoc shares.
	manifest, err := LoadManifest(pkgRoot)
	if err != nil {
		return nil, err
	}
	synthesized := false
	if manifest == nil {
		synthesized = true
		manifest = &Manifest{
			Name:     defaultPackageName(source),
			Schema:   SupportedSchemas[0],
			Contents: []string{"."},
		}
	}
	if manifest.Name == "" {
		manifest.Name = defaultPackageName(source)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}

	// 3. Already-installed guard.
	existing, err := Load(projectRoot)
	if err != nil {
		return nil, err
	}
	if idx := findPackage(existing, manifest.Name); idx >= 0 && !opts.Force {
		return nil, fmt.Errorf("%w: %s (use --force or `agents remove` first)", ErrAlreadyInstalled, manifest.Name)
	}

	// 4. Determine install target. The CLI's --as overrides everything; then
	// the package's manifest can override; otherwise default to skills/<name>.
	target := opts.Target
	if target == "" {
		target = defaultTarget(manifest)
	}
	if target != "" {
		if err := validateContentPath(target); err != nil {
			return nil, err
		}
	}

	// 5. Stage in temp dir, then move into place atomically per-file. We do
	// not try to roll back successful copies if a later copy fails — those
	// files belong to the same package and will be cleaned up by `agents
	// remove`. We do, however, refuse to write files that escape .agents/.
	stage, err := os.MkdirTemp("", "agents-install-*")
	if err != nil {
		return nil, fmt.Errorf("registry: mkdir stage: %w", err)
	}
	defer os.RemoveAll(stage)

	relFiles, err := stageContents(pkgRoot, manifest.Contents, stage, target, synthesized)
	if err != nil {
		return nil, err
	}

	dotAgents := filepath.Join(projectRoot, ".agents")
	installed, hashes, err := promoteStaged(stage, dotAgents, relFiles)
	if err != nil {
		return nil, err
	}

	// 6. Build the package record. SHA is the rolled-up hash of all files.
	pkg := &model.Package{
		Name:        manifest.Name,
		Source:      sourceWithoutRef(source),
		Ref:         resolvedRef(source, opts.Ref),
		SHA:         aggregateHash(hashes),
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Target:      filepath.ToSlash(target),
		Files:       installed,
	}

	// 7. Read-modify-write packages.yaml.
	if idx := findPackage(existing, pkg.Name); idx >= 0 {
		existing[idx] = pkg
	} else {
		existing = append(existing, pkg)
	}
	if err := Save(projectRoot, existing); err != nil {
		return nil, err
	}
	return pkg, nil
}

// Remove deletes the package's tracked files (those whose on-disk hash still
// matches what we recorded) and updates packages.yaml.
//
// If any tracked file has drifted (or is missing), it is preserved on disk
// and the package entry is also preserved in packages.yaml, so the user can
// resolve the drift and re-run remove. The returned error wraps the drift
// list in that case.
func Remove(projectRoot, name string) error {
	pkgs, err := Load(projectRoot)
	if err != nil {
		return err
	}
	idx := findPackage(pkgs, name)
	if idx < 0 {
		return fmt.Errorf("registry: no package named %q is installed", name)
	}
	pkg := pkgs[idx]

	dotAgents := filepath.Join(projectRoot, ".agents")

	// We currently store a single aggregate hash on the Package, not a
	// per-file hash. v0.5 drift detection therefore reduces to: hash all the
	// tracked files we can find, compare against the recorded aggregate.
	// If they don't match, treat ALL tracked files as drifted and preserve
	// them. A future v0.6 can store per-file hashes.
	currentHashes, missing := hashTrackedFiles(dotAgents, pkg.Files)
	current := aggregateHash(currentHashes)

	var warnings []string
	for _, m := range missing {
		warnings = append(warnings, fmt.Sprintf("missing on disk: %s", m))
	}

	if pkg.SHA != "" && current != pkg.SHA {
		warnings = append(warnings, fmt.Sprintf(
			"package %q has been modified since install (sha %s, expected %s); files preserved",
			pkg.Name, current, pkg.SHA))
		return &RemoveDriftError{Package: pkg.Name, Warnings: warnings}
	}

	// Safe to delete. Walk files, then prune empty parent directories up to
	// .agents/.
	var deletedDirs []string
	for _, rel := range pkg.Files {
		abs := filepath.Join(dotAgents, filepath.FromSlash(rel))
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			warnings = append(warnings, fmt.Sprintf("remove %s: %v", rel, err))
		}
		deletedDirs = append(deletedDirs, filepath.Dir(abs))
	}
	pruneEmptyDirs(dotAgents, deletedDirs)

	// Drop the entry.
	pkgs = append(pkgs[:idx], pkgs[idx+1:]...)
	if err := Save(projectRoot, pkgs); err != nil {
		return err
	}

	if len(warnings) > 0 {
		return &RemoveDriftError{Package: name, Warnings: warnings}
	}
	return nil
}

// List returns the installed packages, sorted by Name.
func List(projectRoot string) ([]*model.Package, error) {
	return Load(projectRoot)
}

// RemoveDriftError is returned by Remove when one or more tracked files were
// preserved due to drift, missing-from-disk state, or removal errors. The
// CLI surfaces .Warnings to the user.
type RemoveDriftError struct {
	Package  string
	Warnings []string
}

func (e *RemoveDriftError) Error() string {
	return fmt.Sprintf("remove %s: %s", e.Package, strings.Join(e.Warnings, "; "))
}

// ---- helpers ----------------------------------------------------------

func agentsDirExists(projectRoot string) bool {
	info, err := os.Stat(filepath.Join(projectRoot, ".agents"))
	return err == nil && info.IsDir()
}

func findPackage(pkgs []*model.Package, name string) int {
	for i, p := range pkgs {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// materializeSource resolves a source string to a local directory containing
// the package files. cleanup MUST always be called (it's a noop for local
// sources). Returns the package-root path that callers should look for
// package.yaml in.
func materializeSource(source, refOverride string) (string, func(), error) {
	noop := func() {}

	// Local paths: ./foo, ../foo, /foo. Anything else with a dot or slash
	// that doesn't start with "github.com/" is treated as a path too — but
	// we limit the fallback to existing directories to avoid swallowing typos.
	if isLocalSource(source) {
		abs, err := filepath.Abs(source)
		if err != nil {
			return "", noop, fmt.Errorf("registry: resolve local source %q: %w", source, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", noop, fmt.Errorf("registry: stat local source %q: %w", source, err)
		}
		if !info.IsDir() {
			return "", noop, fmt.Errorf("registry: local source %q is not a directory", source)
		}
		return abs, noop, nil
	}

	// Otherwise: treat as a git source. Parse out host/owner/repo, optional
	// subpath, optional ref.
	repo, subpath, ref, err := parseGitSource(source)
	if err != nil {
		return "", noop, err
	}
	if refOverride != "" {
		ref = refOverride
	}

	tmp, err := os.MkdirTemp("", "agents-clone-*")
	if err != nil {
		return "", noop, fmt.Errorf("registry: mkdir clone tmp: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmp) }

	url := "https://" + repo + ".git"
	cloneArgs := []string{"clone", "--depth=1"}
	// `git clone --depth=1 --branch=<ref>` works for both branches and tags;
	// for arbitrary commit SHAs we have to clone full then checkout.
	if ref != "" && looksLikeRef(ref) {
		cloneArgs = append(cloneArgs, "--branch="+ref)
	}
	cloneArgs = append(cloneArgs, url, tmp)
	if out, err := runGit("", cloneArgs...); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("registry: git clone %s: %w (%s)", url, err, strings.TrimSpace(out))
	}
	if ref != "" && !looksLikeRef(ref) {
		// SHA-ish; we cloned shallow without --branch, so fetch + checkout.
		if out, err := runGit(tmp, "fetch", "--depth=1", "origin", ref); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("registry: git fetch %s: %w (%s)", ref, err, strings.TrimSpace(out))
		}
		if out, err := runGit(tmp, "checkout", ref); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("registry: git checkout %s: %w (%s)", ref, err, strings.TrimSpace(out))
		}
	}

	root := tmp
	if subpath != "" {
		root = filepath.Join(tmp, filepath.FromSlash(subpath))
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			cleanup()
			return "", noop, fmt.Errorf("registry: subpath %q not found in %s", subpath, repo)
		}
	}
	return root, cleanup, nil
}

// isLocalSource returns true for paths the user clearly meant as filesystem
// paths (./, ../, absolute paths).
func isLocalSource(source string) bool {
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
		return true
	}
	if filepath.IsAbs(source) {
		return true
	}
	if source == "." || source == ".." {
		return true
	}
	return false
}

// parseGitSource splits "github.com/owner/repo[/subpath][@ref]" into its
// parts. The "repo" component is exactly "host/owner/repo".
func parseGitSource(source string) (repo, subpath, ref string, err error) {
	src := source
	if at := strings.LastIndex(src, "@"); at >= 0 {
		ref = src[at+1:]
		src = src[:at]
	}
	parts := strings.Split(src, "/")
	if len(parts) < 3 {
		return "", "", "", fmt.Errorf("registry: source %q is not a host/owner/repo[/subpath][@ref] URL", source)
	}
	repo = strings.Join(parts[:3], "/")
	if len(parts) > 3 {
		subpath = strings.Join(parts[3:], "/")
	}
	return repo, subpath, ref, nil
}

// looksLikeRef heuristically distinguishes refs that `git clone --branch`
// accepts (tag/branch names) from commit SHAs (which need a separate fetch).
func looksLikeRef(ref string) bool {
	if len(ref) < 7 {
		return true
	}
	for _, r := range ref {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return true
		}
	}
	// All-hex and >=7 chars: treat as SHA.
	return false
}

func runGit(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// defaultPackageName extracts a sensible package name from a source string
// when the manifest doesn't supply one (or the manifest is missing).
func defaultPackageName(source string) string {
	s := source
	if at := strings.LastIndex(s, "@"); at >= 0 {
		s = s[:at]
	}
	s = filepath.ToSlash(filepath.Clean(s))
	parts := strings.Split(s, "/")
	last := parts[len(parts)-1]
	if last == "" || last == "." {
		// e.g. "./skills/foo/"
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" && parts[i] != "." {
				return parts[i]
			}
		}
	}
	return last
}

// defaultTarget is `skills/<name>` unless the manifest's contents look like a
// full `.agents/`-shaped tree (e.g. `contents: [skills/foo/, commands/bar.md]`),
// in which case the target is the empty string and contents are copied at
// their declared paths.
func defaultTarget(m *Manifest) string {
	if m == nil {
		return ""
	}
	if looksLikeShapedContents(m.Contents) {
		return ""
	}
	return filepath.ToSlash(filepath.Join("skills", m.Name))
}

// looksLikeShapedContents returns true when every declared content path
// begins with a known `.agents/` capability directory. That's the signal
// that the package is shipping pre-shaped content and we should NOT wrap it
// in `skills/<name>/`.
func looksLikeShapedContents(contents []string) bool {
	if len(contents) == 0 {
		return false
	}
	prefixes := []string{"skills/", "commands/", "agents/", "hooks/", "permissions.yaml", "mcp.yaml", "context.md", "scopes.yaml"}
	for _, c := range contents {
		c = filepath.ToSlash(filepath.Clean(c))
		matched := false
		for _, p := range prefixes {
			if c == strings.TrimSuffix(p, "/") || strings.HasPrefix(c, p) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// stageContents copies each declared content path from the package root into
// the staging directory, applying the target prefix. Returns the list of
// staged files (paths relative to .agents/, using forward slashes).
//
// `synthesized` is true when the manifest was built because no package.yaml
// was found; in that case we copy the entire pkgRoot, skipping
// `package.yaml` itself if present.
func stageContents(pkgRoot string, contents []string, stage, target string, synthesized bool) ([]string, error) {
	var staged []string
	for _, c := range contents {
		if err := validateContentPath(c); err != nil {
			return nil, err
		}
		src := filepath.Join(pkgRoot, filepath.FromSlash(c))
		info, err := os.Stat(src)
		if err != nil {
			return nil, fmt.Errorf("registry: content %q not found in package: %w", c, err)
		}
		// Compute the destination path relative to .agents/. Synthesized
		// manifests use "." as contents and want the whole tree copied under
		// the target.
		var destRel string
		if c == "." || c == "./" {
			destRel = target
		} else if target == "" {
			destRel = filepath.ToSlash(filepath.Clean(c))
		} else {
			destRel = filepath.ToSlash(filepath.Join(target, filepath.Base(filepath.Clean(c))))
			// If the content path is a directory and it matches a recognized
			// .agents/ shape, just append target+the-path.
			if info.IsDir() && looksLikeShapedContents([]string{c}) {
				destRel = filepath.ToSlash(filepath.Clean(c))
			}
		}
		dest := filepath.Join(stage, filepath.FromSlash(destRel))

		if info.IsDir() {
			files, err := copyDir(src, dest, synthesized)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				// f is absolute under stage; convert to .agents/-relative.
				rel, err := filepath.Rel(stage, f)
				if err != nil {
					return nil, err
				}
				staged = append(staged, filepath.ToSlash(rel))
			}
		} else {
			if err := copyFile(src, dest); err != nil {
				return nil, err
			}
			staged = append(staged, filepath.ToSlash(destRel))
		}
	}
	sort.Strings(staged)
	return staged, nil
}

// copyDir recursively copies srcDir into dstDir, creating dstDir if needed.
// When skipPackageYAML is true, the top-level package.yaml is skipped (used
// for synthesized whole-directory installs).
func copyDir(srcDir, dstDir string, skipPackageYAML bool) ([]string, error) {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, fmt.Errorf("registry: mkdir %s: %w", dstDir, err)
	}
	var out []string
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skipPackageYAML && rel == "package.yaml" {
			return nil
		}
		dest := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if d.Type()&os.ModeType != 0 {
			// Skip symlinks/sockets/devices for safety.
			return nil
		}
		if err := copyFile(path, dest); err != nil {
			return err
		}
		out = append(out, dest)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("registry: copy %s -> %s: %w", srcDir, dstDir, err)
	}
	return out, nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("registry: mkdir %s: %w", filepath.Dir(dst), err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("registry: open %s: %w", src, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("registry: stat %s: %w", src, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("registry: create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("registry: copy %s -> %s: %w", src, dst, err)
	}
	return out.Close()
}

// promoteStaged moves files from the staging directory into the destination
// tree under dotAgents and returns the relative paths plus per-file SHA-256
// hashes (in the same order).
func promoteStaged(stage, dotAgents string, relFiles []string) ([]string, []string, error) {
	var installed []string
	var hashes []string
	for _, rel := range relFiles {
		srcAbs := filepath.Join(stage, filepath.FromSlash(rel))
		destAbs := filepath.Join(dotAgents, filepath.FromSlash(rel))
		if !strings.HasPrefix(destAbs+string(filepath.Separator), dotAgents+string(filepath.Separator)) && destAbs != dotAgents {
			return nil, nil, fmt.Errorf("%w: %s would escape .agents/", ErrPathTraversal, rel)
		}
		if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
			return nil, nil, fmt.Errorf("registry: mkdir %s: %w", filepath.Dir(destAbs), err)
		}
		// Copy then remove rather than rename, to handle cross-device staging.
		if err := copyFile(srcAbs, destAbs); err != nil {
			return nil, nil, err
		}
		h, err := hashFile(destAbs)
		if err != nil {
			return nil, nil, err
		}
		installed = append(installed, rel)
		hashes = append(hashes, h)
	}
	return installed, hashes, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("registry: hash open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("registry: hash read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// aggregateHash rolls the per-file hashes into a single SHA-256 over the
// sorted concatenation. Order-independent given that callers pass hashes in
// the same order they pass files (and files are sorted at install time).
func aggregateHash(hashes []string) string {
	if len(hashes) == 0 {
		return ""
	}
	sorted := append([]string(nil), hashes...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, s := range sorted {
		_, _ = io.WriteString(h, s)
		_, _ = io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashTrackedFiles returns per-file hashes (for files that exist) and a list
// of files that are missing from disk.
func hashTrackedFiles(dotAgents string, files []string) (hashes []string, missing []string) {
	for _, rel := range files {
		abs := filepath.Join(dotAgents, filepath.FromSlash(rel))
		h, err := hashFile(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
				missing = append(missing, rel)
				continue
			}
			missing = append(missing, rel+": "+err.Error())
			continue
		}
		hashes = append(hashes, h)
	}
	return hashes, missing
}

// pruneEmptyDirs walks each given directory upward, removing empty
// directories until it hits dotAgents (which it does NOT remove).
func pruneEmptyDirs(dotAgents string, dirs []string) {
	seen := map[string]bool{}
	for _, d := range dirs {
		for {
			if d == dotAgents || !strings.HasPrefix(d+string(filepath.Separator), dotAgents+string(filepath.Separator)) {
				break
			}
			if seen[d] {
				break
			}
			seen[d] = true
			entries, err := os.ReadDir(d)
			if err != nil || len(entries) > 0 {
				break
			}
			if err := os.Remove(d); err != nil {
				break
			}
			d = filepath.Dir(d)
		}
	}
}

// sourceWithoutRef strips a trailing @ref suffix for storage.
func sourceWithoutRef(source string) string {
	if at := strings.LastIndex(source, "@"); at >= 0 {
		return source[:at]
	}
	return source
}

// resolvedRef returns the effective ref: the explicit override wins, else
// the suffix on the source URL, else "".
func resolvedRef(source, override string) string {
	if override != "" {
		return override
	}
	if at := strings.LastIndex(source, "@"); at >= 0 {
		return source[at+1:]
	}
	return ""
}
