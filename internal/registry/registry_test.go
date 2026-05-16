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
			Files: []string{"skills/b/SKILL.md", "skills/b/extra.md"}},
		{Name: "a-pkg", Source: "github.com/a/a", Ref: "v2", SHA: "def",
			InstalledAt: "2026-05-16T11:00:00Z", Target: "skills/a",
			Files: []string{"skills/a/SKILL.md"}},
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
		Name:   "p",
		Files:  []string{"skills/p/z.md", "skills/p/a.md", "skills/p/m.md"},
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
	sortedCopy := append([]string(nil), out[0].Files...)
	sort.Strings(sortedCopy)
	if !reflect.DeepEqual(sortedCopy, out[0].Files) {
		t.Errorf("Files not sorted: %v", out[0].Files)
	}
}
