package parser

import (
	"os"
	"path/filepath"
	"testing"
)

// helper: write a tree of files under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

// TestParseLayered_EmptyGlobal: globalRoot=="" behaves exactly like Parse(projectRoot).
func TestParseLayered_EmptyGlobal(t *testing.T) {
	project := t.TempDir()
	writeTree(t, project, map[string]string{
		".agents/context.md": "# project\n",
	})

	proj, err := ParseLayered("", project)
	if err != nil {
		t.Fatalf("ParseLayered: %v", err)
	}
	if proj.Context == nil || proj.Context.Body != "# project\n" {
		t.Fatalf("expected project context, got %+v", proj.Context)
	}
}

// TestParseLayered_GlobalRootWithoutAgentsDir: silently skipped.
func TestParseLayered_GlobalRootWithoutAgentsDir(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir() // no .agents/ inside
	writeTree(t, project, map[string]string{
		".agents/context.md": "# project\n",
	})

	proj, err := ParseLayered(global, project)
	if err != nil {
		t.Fatalf("ParseLayered: %v", err)
	}
	if proj.Context.Body != "# project\n" {
		t.Fatalf("expected project context, got %q", proj.Context.Body)
	}
}

// TestParseLayered_GlobalContextFallback: project has no Context, global does.
func TestParseLayered_GlobalContextFallback(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	// Project has only a skill, no context.md.
	writeTree(t, project, map[string]string{
		".agents/skills/p/SKILL.md": "project skill",
	})
	writeTree(t, global, map[string]string{
		".agents/context.md": "# global\n",
	})

	proj, err := ParseLayered(global, project)
	if err != nil {
		t.Fatalf("ParseLayered: %v", err)
	}
	if proj.Context == nil || proj.Context.Body != "# global\n" {
		t.Fatalf("expected global context to fill in, got %+v", proj.Context)
	}
}

// TestParseLayered_ProjectContextWins.
func TestParseLayered_ProjectContextWins(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, project, map[string]string{".agents/context.md": "P"})
	writeTree(t, global, map[string]string{".agents/context.md": "G"})

	proj, _ := ParseLayered(global, project)
	if proj.Context.Body != "P" {
		t.Fatalf("project context should win, got %q", proj.Context.Body)
	}
}

// TestParseLayered_ScopesMergeByPath.
func TestParseLayered_ScopesMergeByPath(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, project, map[string]string{
		".agents/context.md":            "P\n",
		".agents/src/auth/context.md":   "project-auth",
		".agents/src/shared/context.md": "project-shared",
	})
	writeTree(t, global, map[string]string{
		".agents/context.md":              "G\n",
		".agents/src/shared/context.md":   "global-shared",   // collision
		".agents/src/personal/context.md": "global-personal", // global-only
	})

	proj, err := ParseLayered(global, project)
	if err != nil {
		t.Fatalf("ParseLayered: %v", err)
	}
	byPath := make(map[string]string)
	for _, s := range proj.Scopes {
		byPath[s.Path] = s.Document.Body
	}
	if byPath["src/auth"] != "project-auth" {
		t.Fatalf("src/auth not from project: %v", byPath)
	}
	if byPath["src/shared"] != "project-shared" {
		t.Fatalf("src/shared collision: project should win, got %q", byPath["src/shared"])
	}
	if byPath["src/personal"] != "global-personal" {
		t.Fatalf("src/personal not from global: %v", byPath)
	}
}

// TestParseLayered_SkillsAndCommandsMergeByName.
func TestParseLayered_SkillsAndCommandsMergeByName(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, project, map[string]string{
		".agents/context.md":          "P\n",
		".agents/skills/pdf/SKILL.md": "project-pdf", // collision
		".agents/commands/review.md":  "project-review",
	})
	writeTree(t, global, map[string]string{
		".agents/context.md":            "G\n",
		".agents/skills/pdf/SKILL.md":   "global-pdf", // overridden
		".agents/skills/excel/SKILL.md": "global-excel",
		".agents/commands/lint.md":      "global-lint", // global-only
	})

	proj, _ := ParseLayered(global, project)

	skillBody := map[string]string{}
	for _, s := range proj.Skills {
		skillBody[s.Name] = s.Document.Body
	}
	if skillBody["pdf"] != "project-pdf" {
		t.Fatalf("project should win on pdf, got %q", skillBody["pdf"])
	}
	if skillBody["excel"] != "global-excel" {
		t.Fatalf("global excel missing")
	}

	cmdBody := map[string]string{}
	for _, c := range proj.Commands {
		cmdBody[c.Name] = c.Document.Body
	}
	if cmdBody["review"] != "project-review" || cmdBody["lint"] != "global-lint" {
		t.Fatalf("commands merge failed: %v", cmdBody)
	}
}

// TestParseLayered_PermissionsUnion.
func TestParseLayered_PermissionsUnion(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, project, map[string]string{
		".agents/context.md":       "P\n",
		".agents/permissions.yaml": "allow:\n  - 'Bash(go test:*)'\n  - 'Bash(go build:*)'\ndeny:\n  - 'Bash(rm -rf:*)'\n",
	})
	writeTree(t, global, map[string]string{
		".agents/context.md":       "G\n",
		".agents/permissions.yaml": "allow:\n  - 'Bash(go build:*)'\n  - 'Bash(ls:*)'\nask:\n  - 'Bash(curl:*)'\n",
	})

	proj, _ := ParseLayered(global, project)
	p := proj.Permissions
	if p == nil {
		t.Fatal("expected merged permissions")
	}
	// Allow: project's two + global's "Bash(ls:*)" (build is dedup).
	if !containsAll(p.Allow, "Bash(go test:*)", "Bash(go build:*)", "Bash(ls:*)") || len(p.Allow) != 3 {
		t.Fatalf("allow merge wrong: %v", p.Allow)
	}
	if !containsAll(p.Deny, "Bash(rm -rf:*)") || len(p.Deny) != 1 {
		t.Fatalf("deny merge wrong: %v", p.Deny)
	}
	if !containsAll(p.Ask, "Bash(curl:*)") || len(p.Ask) != 1 {
		t.Fatalf("ask merge wrong: %v", p.Ask)
	}
}

// TestParseLayered_NoProjectAgentsDir: project missing .agents/ still errors.
func TestParseLayered_NoProjectAgentsDir(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, global, map[string]string{".agents/context.md": "G"})
	_, err := ParseLayered(global, project)
	if err == nil {
		t.Fatal("expected ErrNoAgentsDir, got nil")
	}
}

func containsAll(slice []string, items ...string) bool {
	set := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		set[s] = struct{}{}
	}
	for _, want := range items {
		if _, ok := set[want]; !ok {
			return false
		}
	}
	return true
}
