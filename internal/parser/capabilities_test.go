package parser

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParse_Skills(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")

	pdfSkill := filepath.Join(tmp, ".agents", "skills", "pdf-editing", "SKILL.md")
	writeFile(t, pdfSkill,
		"---\ndescription: PDF editing\ntrigger: Loaded when working with PDFs\nglobs:\n  - \"**/*.pdf\"\n---\ninstructions")
	scriptA := filepath.Join(tmp, ".agents", "skills", "pdf-editing", "scripts", "extract.sh")
	scriptB := filepath.Join(tmp, ".agents", "skills", "pdf-editing", "scripts", "lib", "util.sh")
	writeFile(t, scriptA, "#!/bin/sh\n")
	writeFile(t, scriptB, "#!/bin/sh\n")

	// Add a second skill to verify alphabetic ordering.
	zSkill := filepath.Join(tmp, ".agents", "skills", "zoo", "SKILL.md")
	writeFile(t, zSkill, "---\ndescription: zoo\n---\nbody")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Skills) != 2 {
		t.Fatalf("len(Skills) = %d, want 2", len(proj.Skills))
	}
	if proj.Skills[0].Name != "pdf-editing" {
		t.Errorf("Skills[0].Name = %q, want pdf-editing", proj.Skills[0].Name)
	}
	if proj.Skills[1].Name != "zoo" {
		t.Errorf("Skills[1].Name = %q, want zoo", proj.Skills[1].Name)
	}
	s := proj.Skills[0]
	if s.Description != "PDF editing" {
		t.Errorf("Description = %q, want %q", s.Description, "PDF editing")
	}
	if s.Trigger != "Loaded when working with PDFs" {
		t.Errorf("Trigger = %q", s.Trigger)
	}
	if !reflect.DeepEqual(s.Globs, []string{"**/*.pdf"}) {
		t.Errorf("Globs = %v, want [**/*.pdf]", s.Globs)
	}
	if s.Document == nil {
		t.Fatal("Document nil")
	}
	if !filepath.IsAbs(s.Document.SourcePath) {
		t.Errorf("SourcePath = %q, want absolute", s.Document.SourcePath)
	}
	if s.Document.SourcePath != pdfSkill {
		t.Errorf("SourcePath = %q, want %q", s.Document.SourcePath, pdfSkill)
	}
	if len(s.Scripts) != 2 {
		t.Fatalf("len(Scripts) = %d, want 2: %v", len(s.Scripts), s.Scripts)
	}
	for _, sp := range s.Scripts {
		if !filepath.IsAbs(sp) {
			t.Errorf("Script %q not absolute", sp)
		}
	}
	// Sorted: extract.sh comes before lib/util.sh (e < l).
	if !strings.HasSuffix(s.Scripts[0], "scripts/extract.sh") {
		t.Errorf("Scripts[0] = %q, want suffix scripts/extract.sh", s.Scripts[0])
	}
	if !strings.HasSuffix(s.Scripts[1], "scripts/lib/util.sh") {
		t.Errorf("Scripts[1] = %q, want suffix scripts/lib/util.sh", s.Scripts[1])
	}
	prev := ""
	for _, sp := range s.Scripts {
		if prev != "" && sp < prev {
			t.Errorf("Scripts not sorted: %v", s.Scripts)
			break
		}
		prev = sp
	}
}

func TestParse_Commands(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "commands", "review.md"),
		"---\ndescription: Review a PR\n---\nPlease review.")
	writeFile(t, filepath.Join(tmp, ".agents", "commands", "ship.md"),
		"---\ndescription: Ship it\n---\nDeploying now.")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Commands) != 2 {
		t.Fatalf("len(Commands) = %d, want 2", len(proj.Commands))
	}
	if proj.Commands[0].Name != "review" || proj.Commands[1].Name != "ship" {
		t.Errorf("command order = [%q, %q], want [review, ship]",
			proj.Commands[0].Name, proj.Commands[1].Name)
	}
	if proj.Commands[0].Description != "Review a PR" {
		t.Errorf("Description = %q, want %q",
			proj.Commands[0].Description, "Review a PR")
	}
	if proj.Commands[0].Document == nil {
		t.Fatal("Document nil")
	}
	if proj.Commands[0].Document.Body != "Please review." {
		t.Errorf("Body = %q", proj.Commands[0].Document.Body)
	}
	if !filepath.IsAbs(proj.Commands[0].Document.SourcePath) {
		t.Errorf("SourcePath = %q, want absolute",
			proj.Commands[0].Document.SourcePath)
	}
}

func TestParse_Agents(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "agents", "explorer.md"),
		"---\ndescription: Explores the codebase\n---\nYou are an explorer.")
	writeFile(t, filepath.Join(tmp, ".agents", "agents", "tester.md"),
		"---\ndescription: Runs tests\n---\nYou run tests.")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Agents) != 2 {
		t.Fatalf("len(Agents) = %d, want 2", len(proj.Agents))
	}
	if proj.Agents[0].Name != "explorer" || proj.Agents[1].Name != "tester" {
		t.Errorf("agent order = [%q, %q], want [explorer, tester]",
			proj.Agents[0].Name, proj.Agents[1].Name)
	}
	if proj.Agents[0].Description != "Explores the codebase" {
		t.Errorf("Description = %q", proj.Agents[0].Description)
	}
	if proj.Agents[0].Document == nil {
		t.Fatal("Document nil")
	}
	if !filepath.IsAbs(proj.Agents[0].Document.SourcePath) {
		t.Errorf("SourcePath = %q, want absolute",
			proj.Agents[0].Document.SourcePath)
	}
}

func TestParse_Hooks(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "hooks", "format.yaml"),
		"event: PostToolUse\nmatcher: Edit\nscript: format.sh\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Hooks) != 1 {
		t.Fatalf("len(Hooks) = %d, want 1", len(proj.Hooks))
	}
	h := proj.Hooks[0]
	if h.Event != "PostToolUse" {
		t.Errorf("Event = %q, want PostToolUse", h.Event)
	}
	if h.Matcher != "Edit" {
		t.Errorf("Matcher = %q, want Edit", h.Matcher)
	}
	if !filepath.IsAbs(h.ScriptPath) {
		t.Errorf("ScriptPath = %q, want absolute", h.ScriptPath)
	}
	wantSuffix := filepath.Join(".agents", "hooks", "format.sh")
	if !strings.HasSuffix(h.ScriptPath, wantSuffix) {
		t.Errorf("ScriptPath = %q, want suffix %q", h.ScriptPath, wantSuffix)
	}
}

func TestParse_Permissions(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "permissions.yaml"),
		"allow:\n  - Bash(git:*)\ndeny:\n  - Bash(rm:*)\nask:\n  - Bash(npm:*)\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj.Permissions == nil {
		t.Fatal("Permissions nil")
	}
	if !reflect.DeepEqual(proj.Permissions.Allow, []string{"Bash(git:*)"}) {
		t.Errorf("Allow = %v", proj.Permissions.Allow)
	}
	if !reflect.DeepEqual(proj.Permissions.Deny, []string{"Bash(rm:*)"}) {
		t.Errorf("Deny = %v", proj.Permissions.Deny)
	}
	if !reflect.DeepEqual(proj.Permissions.Ask, []string{"Bash(npm:*)"}) {
		t.Errorf("Ask = %v", proj.Permissions.Ask)
	}

	// Missing file → nil.
	tmp2 := t.TempDir()
	writeFile(t, filepath.Join(tmp2, ".agents", "context.md"), "root\n")
	proj2, err := Parse(tmp2)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if proj2.Permissions != nil {
		t.Errorf("Permissions = %+v, want nil", proj2.Permissions)
	}
}

func TestParse_MCP(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	writeFile(t, filepath.Join(tmp, ".agents", "mcp.yaml"),
		"servers:\n"+
			"  zeta:\n"+
			"    command: /usr/bin/zeta\n"+
			"    args: [--flag]\n"+
			"    env:\n"+
			"      KEY: VALUE\n"+
			"  alpha:\n"+
			"    url: https://example.com/mcp\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.MCP) != 2 {
		t.Fatalf("len(MCP) = %d, want 2", len(proj.MCP))
	}
	if proj.MCP[0].Name != "alpha" || proj.MCP[1].Name != "zeta" {
		t.Errorf("MCP order = [%q, %q], want [alpha, zeta]",
			proj.MCP[0].Name, proj.MCP[1].Name)
	}
	if proj.MCP[0].URL != "https://example.com/mcp" {
		t.Errorf("alpha.URL = %q", proj.MCP[0].URL)
	}
	if proj.MCP[1].Command != "/usr/bin/zeta" {
		t.Errorf("zeta.Command = %q", proj.MCP[1].Command)
	}
	if !reflect.DeepEqual(proj.MCP[1].Args, []string{"--flag"}) {
		t.Errorf("zeta.Args = %v", proj.MCP[1].Args)
	}
	if proj.MCP[1].Env["KEY"] != "VALUE" {
		t.Errorf("zeta.Env[KEY] = %q", proj.MCP[1].Env["KEY"])
	}
}

func TestParse_ReservedDirsNotScopes(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	// Adversarial stray context.md files inside reserved directories.
	writeFile(t, filepath.Join(tmp, ".agents", "skills", "foo", "context.md"), "nope\n")
	writeFile(t, filepath.Join(tmp, ".agents", "skills", "foo", "SKILL.md"),
		"---\ndescription: foo skill\n---\nbody")
	writeFile(t, filepath.Join(tmp, ".agents", "commands", "context.md"), "also nope\n")
	writeFile(t, filepath.Join(tmp, ".agents", "agents", "context.md"), "still nope\n")
	writeFile(t, filepath.Join(tmp, ".agents", "hooks", "context.md"), "definitely nope\n")
	// A legit scope, to make sure scope walking still works.
	writeFile(t, filepath.Join(tmp, ".agents", "src", "billing", "context.md"), "billing\n")

	proj, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(proj.Scopes) != 1 {
		paths := make([]string, 0, len(proj.Scopes))
		for _, s := range proj.Scopes {
			paths = append(paths, s.Path)
		}
		t.Fatalf("len(Scopes) = %d, want 1; got paths=%v", len(proj.Scopes), paths)
	}
	if proj.Scopes[0].Path != "src/billing" {
		t.Errorf("Scope.Path = %q, want src/billing", proj.Scopes[0].Path)
	}
	// And the legitimate skill is still picked up.
	if len(proj.Skills) != 1 || proj.Skills[0].Name != "foo" {
		t.Errorf("Skills = %v, want one skill named foo", proj.Skills)
	}
}

func TestParse_MalformedSkillFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".agents", "context.md"), "root\n")
	badSkill := filepath.Join(tmp, ".agents", "skills", "broken", "SKILL.md")
	// Malformed YAML inside the frontmatter fences.
	writeFile(t, badSkill, "---\n: : : not yaml\n  - [unclosed\n---\nbody\n")

	_, err := Parse(tmp)
	if err == nil {
		t.Fatal("Parse returned nil error for malformed SKILL.md frontmatter")
	}
	if !strings.Contains(err.Error(), "SKILL.md") {
		t.Errorf("error %q does not reference SKILL.md path", err.Error())
	}
}
