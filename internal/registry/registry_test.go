package registry

import (
	"errors"

	"agents.dev/agents/internal/model"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Helpers ---------------------------------------------------------------

// writeFile writes data to root/relPath, creating parents.
func writeFile(t *testing.T, root, relPath, data string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(abs, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// makeProject creates a project root with an empty .agents/ inside.
func makeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir .agents: %v", err)
	}
	return root
}

// makePackage creates a local "package source" directory with the given
// manifest YAML and a small set of skill files. The package always declares
// `skills/pdf-editing/` as its content.
func makePackage(t *testing.T, manifest string) string {
	t.Helper()
	pkgRoot := t.TempDir()
	if manifest != "" {
		writeFile(t, pkgRoot, "package.yaml", manifest)
	}
	writeFile(t, pkgRoot, "skills/pdf-editing/SKILL.md", "---\nname: pdf-editing\n---\n# Skill body")
	writeFile(t, pkgRoot, "skills/pdf-editing/scripts/pdfgen.sh", "#!/bin/sh\necho hi\n")
	return pkgRoot
}

const standardManifest = `
name: pdf-editing
version: 1.0.0
author: Test
description: A test package.
schema: 1
contents:
  - skills/pdf-editing/
`

// Tests -----------------------------------------------------------------

func TestInstall_Local(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, standardManifest)

	got, err := Install(project, pkg, InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got.Name != "pdf-editing" {
		t.Errorf("Name = %q, want pdf-editing", got.Name)
	}
	if len(got.Files) != 2 {
		t.Errorf("Files = %v, want 2 entries", got.Files)
	}

	// Files must exist on disk.
	skill := filepath.Join(project, ".agents", "skills", "pdf-editing", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("expected SKILL.md installed: %v", err)
	}

	// packages.yaml must be readable + contain the package.
	loaded, err := Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "pdf-editing" {
		t.Errorf("Load = %v, want one pdf-editing entry", loaded)
	}
}

func TestInstall_AlreadyInstalled(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, standardManifest)

	if _, err := Install(project, pkg, InstallOptions{}); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	_, err := Install(project, pkg, InstallOptions{})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Errorf("second Install err = %v, want ErrAlreadyInstalled", err)
	}

	// With Force it should succeed.
	if _, err := Install(project, pkg, InstallOptions{Force: true}); err != nil {
		t.Errorf("Install with Force: %v", err)
	}
}

func TestInstall_NoAgentsDir(t *testing.T) {
	project := t.TempDir() // no .agents/
	pkg := makePackage(t, standardManifest)
	_, err := Install(project, pkg, InstallOptions{})
	if !errors.Is(err, ErrNoAgentsDir) {
		t.Errorf("Install err = %v, want ErrNoAgentsDir", err)
	}
}

func TestInstall_SchemaMismatch(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, `
name: pdf-editing
schema: 99
contents:
  - skills/pdf-editing/
`)
	_, err := Install(project, pkg, InstallOptions{})
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Errorf("err = %v, want ErrSchemaMismatch", err)
	}
}

func TestInstall_PathTraversal(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, `
name: evil
schema: 1
contents:
  - ../../etc
`)
	_, err := Install(project, pkg, InstallOptions{})
	if !errors.Is(err, ErrPathTraversal) {
		t.Errorf("err = %v, want ErrPathTraversal", err)
	}
}

func TestInstall_NoManifest(t *testing.T) {
	project := makeProject(t)
	pkgRoot := t.TempDir()
	writeFile(t, pkgRoot, "SKILL.md", "# whole-dir package")
	// No package.yaml; the registry should synthesize one and use the
	// directory's basename as the name.
	pkg, err := Install(project, pkgRoot, InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if pkg.Name == "" {
		t.Errorf("synthesized package missing name")
	}
}

func TestRemove_Clean(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, standardManifest)
	if _, err := Install(project, pkg, InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := Remove(project, "pdf-editing"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Files must be gone.
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "pdf-editing", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("SKILL.md still present: %v", err)
	}
	loaded, err := Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("Load = %v, want empty", loaded)
	}
}

func TestRemove_DriftedFiles(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, standardManifest)
	if _, err := Install(project, pkg, InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Drift one of the files.
	skill := filepath.Join(project, ".agents", "skills", "pdf-editing", "SKILL.md")
	if err := os.WriteFile(skill, []byte("HAND EDITED"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}

	err := Remove(project, "pdf-editing")
	if err == nil {
		t.Fatal("Remove succeeded silently on drift; expected RemoveDriftError")
	}
	var drift *RemoveDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("err = %T, want *RemoveDriftError: %v", err, err)
	}

	// Files MUST still exist (we preserved them).
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("SKILL.md should be preserved: %v", err)
	}
	// Entry must still be present in packages.yaml.
	loaded, err := Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("packages.yaml = %v, want 1 entry preserved", loaded)
	}
}

func TestRemove_NotInstalled(t *testing.T) {
	project := makeProject(t)
	err := Remove(project, "missing")
	if err == nil {
		t.Fatal("Remove(missing) succeeded; expected error")
	}
}

func TestList_SortedByName(t *testing.T) {
	project := makeProject(t)

	mk := func(name string) {
		t.Helper()
		pkgRoot := t.TempDir()
		writeFile(t, pkgRoot, "package.yaml",
			"name: "+name+"\nschema: 1\ncontents:\n  - skills/"+name+"/\n")
		writeFile(t, pkgRoot, "skills/"+name+"/SKILL.md", "# "+name)
		if _, err := Install(project, pkgRoot, InstallOptions{}); err != nil {
			t.Fatalf("Install %s: %v", name, err)
		}
	}
	mk("charlie")
	mk("alpha")
	mk("bravo")

	loaded, err := List(project)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, len(loaded))
	for i, p := range loaded {
		got[i] = p.Name
	}
	want := []string{"alpha", "bravo", "charlie"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List names = %v, want %v", got, want)
	}
}

func TestPackagesFile_Roundtrip(t *testing.T) {
	project := makeProject(t)
	in := []*model.Package{
		{Name: "b-pkg", Source: "github.com/b/b", Ref: "v1", SHA: "abc",
			InstalledAt: "2026-05-16T10:00:00Z", Target: "skills/b",
			Files: []model.FileEntry{
				{Path: "skills/b/SKILL.md", Hash: "h1"},
				{Path: "skills/b/extra.md", Hash: "h2"},
			}},
		{Name: "a-pkg", Source: "github.com/a/a", Ref: "v2", SHA: "def",
			InstalledAt: "2026-05-16T11:00:00Z", Target: "skills/a",
			Files: []model.FileEntry{{Path: "skills/a/SKILL.md", Hash: "h3"}}},
	}
	if err := Save(project, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	// Sorted by name.
	if out[0].Name != "a-pkg" || out[1].Name != "b-pkg" {
		t.Errorf("order = [%s, %s], want [a-pkg, b-pkg]", out[0].Name, out[1].Name)
	}
}

func TestManifest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		m       Manifest
		wantErr error
	}{
		{"ok", Manifest{Schema: 1, Contents: []string{"skills/a/"}}, nil},
		{"bad schema", Manifest{Schema: 99, Contents: []string{"skills/a/"}}, ErrSchemaMismatch},
		{"abs path", Manifest{Schema: 1, Contents: []string{"/etc/passwd"}}, ErrPathTraversal},
		{"parent path", Manifest{Schema: 1, Contents: []string{"../escape"}}, ErrPathTraversal},
		{"empty content", Manifest{Schema: 1, Contents: []string{""}}, ErrPathTraversal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.m.Validate()
			if c.wantErr == nil {
				if err != nil {
					t.Errorf("got err %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Errorf("got err %v, want %v", err, c.wantErr)
			}
		})
	}
}

func TestParseGitSource(t *testing.T) {
	cases := []struct {
		in      string
		repo    string
		subpath string
		ref     string
		wantErr bool
	}{
		{"github.com/o/r", "github.com/o/r", "", "", false},
		{"github.com/o/r@v1.2.0", "github.com/o/r", "", "v1.2.0", false},
		{"github.com/o/r/skills/foo", "github.com/o/r", "skills/foo", "", false},
		{"github.com/o/r/skills/foo@main", "github.com/o/r", "skills/foo", "main", false},
		{"bad", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			repo, sub, ref, err := parseGitSource(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if err != nil {
				return
			}
			if repo != c.repo || sub != c.subpath || ref != c.ref {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					repo, sub, ref, c.repo, c.subpath, c.ref)
			}
		})
	}
}

// Sanity-check that Files in a roundtripped packages.yaml are sorted.
func TestSave_FilesSorted(t *testing.T) {
	project := makeProject(t)
	in := []*model.Package{{
		Name: "p",
		Files: []model.FileEntry{
			{Path: "skills/p/z.md", Hash: "hz"},
			{Path: "skills/p/a.md", Hash: "ha"},
			{Path: "skills/p/m.md", Hash: "hm"},
		},
		Source: "github.com/x/y",
	}}
	if err := Save(project, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(PackagesFilePath(project))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Just check that a.md appears before z.md in the serialized output.
	a := strings.Index(string(data), "a.md")
	z := strings.Index(string(data), "z.md")
	if a < 0 || z < 0 || a > z {
		t.Errorf("files not sorted in output:\n%s", string(data))
	}
	// And the loaded form is sorted.
	out, _ := Load(project)
	if len(out) != 1 {
		t.Fatalf("Load len = %d", len(out))
	}
	sortedCopy := append([]model.FileEntry(nil), out[0].Files...)
	sort.Slice(sortedCopy, func(i, j int) bool { return sortedCopy[i].Path < sortedCopy[j].Path })
	if !reflect.DeepEqual(sortedCopy, out[0].Files) {
		t.Errorf("Files not sorted: %v", out[0].Files)
	}
}

// v0.6 per-file hash tests --------------------------------------------------

// TestInstall_RecordsPerFileHashes verifies that Install populates each
// FileEntry with a non-empty Hash equal to the SHA-256 of the on-disk file.
func TestInstall_RecordsPerFileHashes(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, standardManifest)

	got, err := Install(project, pkg, InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(got.Files) != 2 {
		t.Fatalf("Files = %d entries, want 2: %+v", len(got.Files), got.Files)
	}
	for _, fe := range got.Files {
		if fe.Path == "" {
			t.Errorf("FileEntry missing Path: %+v", fe)
		}
		if fe.Hash == "" {
			t.Errorf("FileEntry %q missing Hash", fe.Path)
		}
		// Verify hash matches the actual file content.
		abs := filepath.Join(project, ".agents", filepath.FromSlash(fe.Path))
		h, err := hashFile(abs)
		if err != nil {
			t.Errorf("hashFile %s: %v", fe.Path, err)
			continue
		}
		if h != fe.Hash {
			t.Errorf("FileEntry %q Hash = %s, on-disk = %s", fe.Path, fe.Hash, h)
		}
	}
	// Aggregate SHA must also still be populated for back-compat.
	if got.SHA == "" {
		t.Errorf("aggregate SHA empty; expected back-compat aggregate to be populated")
	}
}

// TestRemove_PreservesOnlyDriftedFiles verifies the per-file remove path:
// the drifted file is preserved on disk, the clean file is deleted, and the
// package entry is kept in packages.yaml with Files narrowed to just the
// preserved file.
func TestRemove_PreservesOnlyDriftedFiles(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, standardManifest)
	if _, err := Install(project, pkg, InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Edit just SKILL.md; leave the script alone.
	skill := filepath.Join(project, ".agents", "skills", "pdf-editing", "SKILL.md")
	script := filepath.Join(project, ".agents", "skills", "pdf-editing", "scripts", "pdfgen.sh")
	if err := os.WriteFile(skill, []byte("HAND EDITED"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}

	err := Remove(project, "pdf-editing")
	if err == nil {
		t.Fatal("Remove succeeded silently; expected RemoveDriftError for the drifted file")
	}
	var drift *RemoveDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("err = %T, want *RemoveDriftError: %v", err, err)
	}

	// Drifted file must still exist.
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("SKILL.md should be preserved: %v", err)
	}
	// Clean file must be gone.
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Errorf("pdfgen.sh should be deleted (err=%v)", err)
	}
	// Warnings should reference the drifted file specifically.
	matched := false
	for _, w := range drift.Warnings {
		if strings.Contains(w, "SKILL.md") {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("warnings %v should mention SKILL.md", drift.Warnings)
	}

	// Entry must remain in packages.yaml, narrowed to just the drifted file.
	loaded, err := Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("packages.yaml = %v, want 1 entry preserved", loaded)
	}
	if len(loaded[0].Files) != 1 {
		t.Errorf("preserved entry Files = %v, want only the drifted one", loaded[0].Files)
	}
	if len(loaded[0].Files) == 1 && !strings.Contains(loaded[0].Files[0].Path, "SKILL.md") {
		t.Errorf("preserved file = %q, want SKILL.md", loaded[0].Files[0].Path)
	}
}

// TestRemove_AllClean_FullyRemoves verifies the no-drift path: all files
// deleted, entry dropped from packages.yaml.
func TestRemove_AllClean_FullyRemoves(t *testing.T) {
	project := makeProject(t)
	pkg := makePackage(t, standardManifest)
	if _, err := Install(project, pkg, InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := Remove(project, "pdf-editing"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// All files gone.
	skill := filepath.Join(project, ".agents", "skills", "pdf-editing", "SKILL.md")
	script := filepath.Join(project, ".agents", "skills", "pdf-editing", "scripts", "pdfgen.sh")
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Errorf("SKILL.md not removed: %v", err)
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Errorf("pdfgen.sh not removed: %v", err)
	}
	// Entry dropped.
	loaded, err := Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("Load = %v, want empty", loaded)
	}
}

// TestPackagesFile_BackwardCompat_V05Format verifies that a v0.5-style
// `files:` sequence (plain strings) is accepted and yields FileEntries with
// empty Hash. A round-trip via Save produces the modern map shape.
func TestPackagesFile_BackwardCompat_V05Format(t *testing.T) {
	project := makeProject(t)
	// Write a v0.5-format packages.yaml by hand.
	legacy := `packages:
  pdf-editing:
    source: github.com/anthropic/skills/pdf-editing
    ref: v1.0.0
    sha: aggregateXYZ
    target: skills/pdf-editing
    files:
      - skills/pdf-editing/SKILL.md
      - skills/pdf-editing/scripts/pdfgen.sh
`
	if err := os.WriteFile(PackagesFilePath(project), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	loaded, err := Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Load len = %d, want 1", len(loaded))
	}
	p := loaded[0]
	if len(p.Files) != 2 {
		t.Fatalf("Files len = %d, want 2", len(p.Files))
	}
	for _, fe := range p.Files {
		if fe.Path == "" {
			t.Errorf("legacy FileEntry has empty Path")
		}
		if fe.Hash != "" {
			t.Errorf("legacy FileEntry %q has Hash %q, want empty", fe.Path, fe.Hash)
		}
	}
	// SHA aggregate carries through.
	if p.SHA != "aggregateXYZ" {
		t.Errorf("SHA = %q, want aggregateXYZ", p.SHA)
	}

	// Round-trip: Save must emit the modern map shape.
	if err := Save(project, loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(PackagesFilePath(project))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "- path: skills/pdf-editing/SKILL.md") {
		t.Errorf("round-trip output missing modern `- path:` shape:\n%s", body)
	}
	// And re-loading still works.
	reloaded, err := Load(project)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded) != 1 || len(reloaded[0].Files) != 2 {
		t.Errorf("reload mismatch: %+v", reloaded)
	}
}

// TestRemove_LegacyEntryFallsBackToAggregate verifies that for v0.5-migrated
// packages (FileEntries with empty Hash) the aggregate-SHA fallback gates
// removal all-or-nothing. Clean: all files deleted. Dirty: all preserved.
func TestRemove_LegacyEntryFallsBackToAggregate(t *testing.T) {
	t.Run("clean_aggregate_removes_all", func(t *testing.T) {
		project := makeProject(t)
		pkgSrc := makePackage(t, standardManifest)
		// Install normally to lay down the files + compute aggregate SHA.
		pkg, err := Install(project, pkgSrc, InstallOptions{})
		if err != nil {
			t.Fatalf("Install: %v", err)
		}

		// Simulate v0.5 lockfile: clear per-file Hash but keep aggregate.
		for i := range pkg.Files {
			pkg.Files[i].Hash = ""
		}
		if err := Save(project, []*model.Package{pkg}); err != nil {
			t.Fatalf("Save: %v", err)
		}

		if err := Remove(project, "pdf-editing"); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		// All files gone, entry dropped.
		skill := filepath.Join(project, ".agents", "skills", "pdf-editing", "SKILL.md")
		if _, err := os.Stat(skill); !os.IsNotExist(err) {
			t.Errorf("SKILL.md not removed (legacy clean path): %v", err)
		}
		loaded, _ := Load(project)
		if len(loaded) != 0 {
			t.Errorf("entry not dropped: %v", loaded)
		}
	})

	t.Run("drift_aggregate_preserves_all", func(t *testing.T) {
		project := makeProject(t)
		pkgSrc := makePackage(t, standardManifest)
		pkg, err := Install(project, pkgSrc, InstallOptions{})
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		// Simulate v0.5 lockfile.
		for i := range pkg.Files {
			pkg.Files[i].Hash = ""
		}
		if err := Save(project, []*model.Package{pkg}); err != nil {
			t.Fatalf("Save: %v", err)
		}

		// Drift one file.
		skill := filepath.Join(project, ".agents", "skills", "pdf-editing", "SKILL.md")
		if err := os.WriteFile(skill, []byte("HAND EDITED"), 0o644); err != nil {
			t.Fatalf("edit: %v", err)
		}

		err = Remove(project, "pdf-editing")
		if err == nil {
			t.Fatal("expected RemoveDriftError on legacy drift")
		}
		var drift *RemoveDriftError
		if !errors.As(err, &drift) {
			t.Fatalf("err = %T, want *RemoveDriftError: %v", err, err)
		}
		// All files must still exist (all-or-nothing).
		if _, err := os.Stat(skill); err != nil {
			t.Errorf("SKILL.md should be preserved: %v", err)
		}
		script := filepath.Join(project, ".agents", "skills", "pdf-editing", "scripts", "pdfgen.sh")
		if _, err := os.Stat(script); err != nil {
			t.Errorf("pdfgen.sh should be preserved (legacy all-or-nothing): %v", err)
		}
		// Entry preserved with all files still listed.
		loaded, _ := Load(project)
		if len(loaded) != 1 || len(loaded[0].Files) != 2 {
			t.Errorf("legacy drift should preserve full entry, got: %+v", loaded)
		}
		// Warning should mention the v0.5 fallback.
		matched := false
		for _, w := range drift.Warnings {
			if strings.Contains(w, "v0.5") || strings.Contains(w, "aggregate") {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("expected warning to mention v0.5/aggregate fallback, got: %v", drift.Warnings)
		}
	})
}

// TestParseGitSource_RejectsNonGithub locks in the v0.6 behavior that only
// github.com URLs parse cleanly. Earlier versions silently mis-parsed
// gitlab.com/group/subgroup/project as repo=gitlab.com/group/subgroup +
// subpath=project, which is wrong for any host with nested groups.
func TestParseGitSource_RejectsNonGithub(t *testing.T) {
	hosts := []string{
		"gitlab.com/group/subgroup/project",
		"bitbucket.org/o/r",
		"git.example.com/o/r",
	}
	for _, src := range hosts {
		t.Run(src, func(t *testing.T) {
			_, _, _, err := parseGitSource(src)
			if err == nil {
				t.Fatalf("parseGitSource(%q) returned nil error; want non-github rejection", src)
			}
			if !strings.Contains(err.Error(), "only github.com") {
				t.Errorf("err = %q, want substring \"only github.com\"", err.Error())
			}
		})
	}
}

// TestLooksLikeRef covers the v0.6 fix: fully-numeric strings (e.g. a
// "1234567" branch) are no longer mis-classified as SHAs. The function
// returns true for refs that look like branch/tag names and false for
// refs that look like commit SHAs.
func TestLooksLikeRef(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		want bool // true = branch-ish, false = SHA-ish
	}{
		{"numeric_branch_7", "1234567", true},
		{"numeric_branch_long", "12345678901234567890", true},
		{"all_numeric_40", "1234567890123456789012345678901234567890", false}, // full SHA-1 length wins
		{"abbreviated_sha_7", "a1b2c3d", false},
		{"abbreviated_sha_10", "abcdef1234", false},
		{"full_sha_40", "a1b2c3d4e5f67890123456789abcdef012345678", false},
		{"branch_name", "main", true},
		{"tag_name", "v1.2.0", true},
		{"branch_too_short_for_sha", "abc", true},
		{"branch_with_slash", "feature/auth", true},
		{"branch_too_long", strings.Repeat("a", 41), true},
		{"uppercase_hex_sha", "ABCDEF1", false},
		{"mixed_hex_with_alpha", "abc1234", false},
		{"empty_string", "", true},
		{"head_ref", "HEAD", true},
		{"abbreviated_sha_7_uppercase_letter", "1234567A", false},
		{"non_hex_char_g", "1234567g", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := looksLikeRef(c.ref)
			if got != c.want {
				t.Errorf("looksLikeRef(%q) = %v, want %v", c.ref, got, c.want)
			}
		})
	}
}
