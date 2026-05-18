package importer

import (
	"path/filepath"
	"testing"
)

// TestContinue_V08_PromptsAndPermissions covers .continue/prompts/<n>.md
// (native slash commands) and .continue/permissions.yaml (native perms,
// replaces the perms-guard wrapper round-trip).
func TestContinue_V08_PromptsAndPermissions(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".continue"))

	mustWrite(t, filepath.Join(root, ".continue", "prompts", "deploy.md"),
		"---\nname: deploy\ndescription: Ship a release\ninvokable: true\n---\nRun the release script.\n")

	mustWrite(t, filepath.Join(root, ".continue", "permissions.yaml"),
		"allow:\n  - Bash(go test *)\n  - Read\nask:\n  - Bash(git push *)\nexclude:\n  - Bash(rm -rf *)\n")

	proj, _, err := NewContinue().Import(root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(proj.Commands) != 1 || proj.Commands[0].Name != "deploy" {
		t.Errorf("Commands = %+v", proj.Commands)
	}

	if proj.Permissions == nil {
		t.Fatalf("Permissions = nil")
	}
	if len(proj.Permissions.Allow) != 2 {
		t.Errorf("Allow = %v", proj.Permissions.Allow)
	}
	// First allow round-trips as "bash:go test *", second as bare "read".
	want := map[string]bool{"bash:go test *": false, "read": false}
	for _, a := range proj.Permissions.Allow {
		if _, ok := want[a]; ok {
			want[a] = true
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("expected Allow entry %q, got %v", k, proj.Permissions.Allow)
		}
	}
	if len(proj.Permissions.Ask) != 1 || proj.Permissions.Ask[0] != "bash:git push *" {
		t.Errorf("Ask = %v", proj.Permissions.Ask)
	}
	if len(proj.Permissions.Deny) != 1 || proj.Permissions.Deny[0] != "bash:rm -rf *" {
		t.Errorf("Deny = %v", proj.Permissions.Deny)
	}
}
