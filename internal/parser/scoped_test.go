package parser

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agents.dev/agents/internal/model"
)

// TestParse_ScopedSkill verifies a skill nested under a scope directory is
// discovered and stamped with the scope's path.
func TestParse_ScopedSkill(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing scope\n")
	scoped := filepath.Join(tmp, ".agents", "src", "billing", "skills", "audit-trail", "SKILL.md")
	writeFile(t, scoped, "---\ndescription: PCI audit trail\n---\nbody\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(proj.Skills))
	}
	s := proj.Skills[0]
	if s.Name != "audit-trail" {
		t.Errorf("Skill.Name = %q, want audit-trail", s.Name)
	}
	if s.ScopePath != "src/billing" {
		t.Errorf("Skill.ScopePath = %q, want src/billing", s.ScopePath)
	}
}

// TestParse_ScopedSkillInheritsScopeGlobs verifies a scoped skill with no
// explicit globs picks up scope.DefaultGlobs(scopePath).
func TestParse_ScopedSkillInheritsScopeGlobs(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "skills", "audit-trail", "SKILL.md"),
		"---\ndescription: PCI\n---\nbody\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(proj.Skills))
	}
	s := proj.Skills[0]
	want := []string{"src/billing/**"}
	if !reflect.DeepEqual(s.Globs, want) {
		t.Errorf("Skill.Globs = %v, want %v", s.Globs, want)
	}
}

// TestParse_ScopedSkillExplicitGlobsOverride verifies an explicit globs
// frontmatter wins over the scope default.
func TestParse_ScopedSkillExplicitGlobsOverride(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "skills", "narrow", "SKILL.md"),
		"---\ndescription: narrow\nglobs:\n  - \"**/*.pci\"\n---\nbody\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(proj.Skills))
	}
	s := proj.Skills[0]
	if !reflect.DeepEqual(s.Globs, []string{"**/*.pci"}) {
		t.Errorf("Skill.Globs = %v, want [**/*.pci]", s.Globs)
	}
}

// TestParse_ScopedHookScriptResolution verifies a relative script:
// reference in a scoped hook resolves against the scope's hooks dir.
func TestParse_ScopedHookScriptResolution(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "hooks", "verify-pci.yaml"),
		"event: PreToolUse\nmatcher: Edit\nscript: verify-pci.sh\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "hooks", "verify-pci.sh"),
		"#!/bin/sh\necho ok\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Hooks) != 1 {
		t.Fatalf("len(Hooks) = %d, want 1", len(proj.Hooks))
	}
	h := proj.Hooks[0]
	if h.ScopePath != "src/billing" {
		t.Errorf("Hook.ScopePath = %q, want src/billing", h.ScopePath)
	}
	if !filepath.IsAbs(h.ScriptPath) {
		t.Errorf("Hook.ScriptPath = %q, want absolute", h.ScriptPath)
	}
	wantSuffix := filepath.Join(".agents", "src", "billing", "hooks", "verify-pci.sh")
	if !strings.HasSuffix(h.ScriptPath, wantSuffix) {
		t.Errorf("Hook.ScriptPath = %q, want suffix %q", h.ScriptPath, wantSuffix)
	}
	// Importantly, it must NOT resolve under the global hooks dir.
	globalHooks := filepath.Join(".agents", "hooks", "verify-pci.sh")
	if strings.HasSuffix(h.ScriptPath, globalHooks) {
		t.Errorf("Hook.ScriptPath = %q resolved against global hooks dir", h.ScriptPath)
	}
}

// TestParse_ImplicitScope verifies a directory with a capability subdir
// but no context.md still produces scoped capabilities (stamped with the
// directory path) even though no *model.Scope is added.
func TestParse_ImplicitScope(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	// No context.md under src/billing — only a skill.
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "skills", "audit-trail", "SKILL.md"),
		"---\ndescription: implicit\n---\nbody\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// No Scope object should have been added for the implicit dir.
	for _, s := range proj.Scopes {
		if s.Path == "src/billing" {
			t.Errorf("implicit scope src/billing was added to Scopes; want NOT added")
		}
	}
	if len(proj.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(proj.Skills))
	}
	s := proj.Skills[0]
	if s.Name != "audit-trail" {
		t.Errorf("Skill.Name = %q, want audit-trail", s.Name)
	}
	if s.ScopePath != "src/billing" {
		t.Errorf("Skill.ScopePath = %q, want src/billing (implicit)", s.ScopePath)
	}
	// Inherits default globs from its (implicit) scope path.
	if !reflect.DeepEqual(s.Globs, []string{"src/billing/**"}) {
		t.Errorf("Skill.Globs = %v, want [src/billing/**]", s.Globs)
	}
}

// TestParse_ScopedPermissions verifies a permissions.yaml under a scope
// directory lands in Project.ScopedPermissions (not Project.Permissions)
// and carries the scope path.
func TestParse_ScopedPermissions(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "permissions.yaml"),
		"allow:\n  - 'Bash(stripe:*)'\ndeny:\n  - 'Bash(rm:*)'\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj.Permissions != nil {
		t.Errorf("Project.Permissions = %+v, want nil (scoped perms must not pollute global)", proj.Permissions)
	}
	if len(proj.ScopedPermissions) != 1 {
		t.Fatalf("len(ScopedPermissions) = %d, want 1", len(proj.ScopedPermissions))
	}
	p := proj.ScopedPermissions[0]
	if p.ScopePath != "src/billing" {
		t.Errorf("ScopedPermissions[0].ScopePath = %q, want src/billing", p.ScopePath)
	}
	if !reflect.DeepEqual(p.Allow, []string{"Bash(stripe:*)"}) {
		t.Errorf("Allow = %v", p.Allow)
	}
	if !reflect.DeepEqual(p.Deny, []string{"Bash(rm:*)"}) {
		t.Errorf("Deny = %v", p.Deny)
	}
}

// TestParseLayered_ScopedAndGlobalSkillCoexist verifies a global skill
// `audit-trail` (ScopePath="") and a project skill `audit-trail`
// (ScopePath="src/billing") both end up in the merged Project.Skills
// because the merge key is ScopePath+"/"+Name.
func TestParseLayered_ScopedAndGlobalSkillCoexist(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, project, map[string]string{
		".agents/context.md":                              "P\n",
		".agents/src/billing/context.md":                  "billing\n",
		".agents/src/billing/skills/audit-trail/SKILL.md": "---\ndescription: scoped\n---\nscoped\n",
	})
	writeTree(t, global, map[string]string{
		".agents/context.md":                  "G\n",
		".agents/skills/audit-trail/SKILL.md": "---\ndescription: global\n---\nglobal\n",
	})

	proj, err := ParseLayered(global, project)
	if err != nil {
		t.Fatalf("ParseLayered: %v", err)
	}

	type key struct {
		name, scope string
	}
	seen := make(map[key]string)
	for _, s := range proj.Skills {
		seen[key{s.Name, s.ScopePath}] = s.Document.Body
	}
	if _, ok := seen[key{"audit-trail", ""}]; !ok {
		t.Errorf("global audit-trail missing; got %+v", seen)
	}
	if _, ok := seen[key{"audit-trail", "src/billing"}]; !ok {
		t.Errorf("scoped audit-trail missing; got %+v", seen)
	}
	if len(proj.Skills) != 2 {
		t.Errorf("len(Skills) = %d, want 2; got %+v", len(proj.Skills), seen)
	}
}

// TestParseLayered_GlobalScopeProjectWins verifies that when global and
// project both supply a skill at the same Name AND same ScopePath (""),
// the project wins.
func TestParseLayered_GlobalScopeProjectWins(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, project, map[string]string{
		".agents/context.md":                  "P\n",
		".agents/skills/audit-trail/SKILL.md": "---\ndescription: project-wins\n---\nproject\n",
	})
	writeTree(t, global, map[string]string{
		".agents/context.md":                  "G\n",
		".agents/skills/audit-trail/SKILL.md": "---\ndescription: global-loses\n---\nglobal\n",
	})

	proj, err := ParseLayered(global, project)
	if err != nil {
		t.Fatalf("ParseLayered: %v", err)
	}
	if len(proj.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(proj.Skills))
	}
	s := proj.Skills[0]
	if s.Document.Body != "project\n" {
		t.Errorf("project should win on (name=audit-trail, scope=\"\") collision; got body=%q", s.Document.Body)
	}
}

// TestParseLayered_ScopedPermissionsMergeByPath verifies that scoped
// permissions from both layers coexist (by ScopePath) and that project
// wins on collision.
func TestParseLayered_ScopedPermissionsMergeByPath(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeTree(t, project, map[string]string{
		".agents/context.md":                   "P\n",
		".agents/src/billing/context.md":       "billing\n",
		".agents/src/billing/permissions.yaml": "allow:\n  - 'Bash(project-stripe:*)'\n",
	})
	writeTree(t, global, map[string]string{
		".agents/context.md":                    "G\n",
		".agents/src/billing/context.md":        "billing-g\n",
		".agents/src/billing/permissions.yaml":  "allow:\n  - 'Bash(global-stripe:*)'\n",
		".agents/src/personal/context.md":       "personal\n",
		".agents/src/personal/permissions.yaml": "allow:\n  - 'Bash(personal:*)'\n",
	})

	proj, err := ParseLayered(global, project)
	if err != nil {
		t.Fatalf("ParseLayered: %v", err)
	}
	byPath := make(map[string]*model.Permissions)
	for _, p := range proj.ScopedPermissions {
		byPath[p.ScopePath] = p
	}
	if len(byPath) != 2 {
		t.Fatalf("len(byPath) = %d, want 2; got %+v", len(byPath), byPath)
	}
	billing, ok := byPath["src/billing"]
	if !ok {
		t.Fatalf("src/billing missing from ScopedPermissions: %+v", byPath)
	}
	if !reflect.DeepEqual(billing.Allow, []string{"Bash(project-stripe:*)"}) {
		t.Errorf("src/billing.Allow = %v, want project-only (project should win)", billing.Allow)
	}
	personal, ok := byPath["src/personal"]
	if !ok {
		t.Fatalf("src/personal missing from ScopedPermissions (global-only entry was dropped): %+v", byPath)
	}
	if !reflect.DeepEqual(personal.Allow, []string{"Bash(personal:*)"}) {
		t.Errorf("src/personal.Allow = %v", personal.Allow)
	}
}
