package perms

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheck(t *testing.T) {
	pol := &Policy{
		Allow: []string{"bash:ls *", "bash:cat *", "Edit:/safe/*"},
		Deny:  []string{"bash:rm -rf *", "bash:curl *"},
		Ask:   []string{"bash:git *"},
	}
	cases := []struct {
		name string
		tool string
		act  string
		want Decision
	}{
		{"allow-ls", "Bash", "ls -la", DecisionAllow},
		{"allow-cat", "Bash", "cat /tmp/foo", DecisionAllow},
		{"deny-rm", "Bash", "rm -rf /tmp", DecisionDeny},
		{"deny-curl-overrides", "Bash", "curl https://x", DecisionDeny},
		{"ask-git", "Bash", "git status", DecisionAsk},
		{"unmatched-default", "Bash", "make build", DecisionDefault},
		{"edit-allow-prefix", "Edit", "/safe/foo.go", DecisionAllow},
		{"edit-unmatched", "Edit", "/unsafe/foo.go", DecisionDefault},
		{"tool-case-insensitive", "bash", "ls -la", DecisionAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(pol, tc.tool, tc.act)
			if got != tc.want {
				t.Errorf("Check(%q, %q) = %v, want %v", tc.tool, tc.act, got, tc.want)
			}
		})
	}
}

func TestCheck_NilPolicy(t *testing.T) {
	if got := Check(nil, "Bash", "ls"); got != DecisionDefault {
		t.Errorf("nil policy must be default, got %v", got)
	}
}

func TestCheck_DenyDominatesAllow(t *testing.T) {
	pol := &Policy{
		Allow: []string{"bash:*"},
		Deny:  []string{"bash:rm *"},
	}
	if got := Check(pol, "Bash", "rm -rf /"); got != DecisionDeny {
		t.Errorf("deny should dominate allow; got %v", got)
	}
	if got := Check(pol, "Bash", "ls"); got != DecisionAllow {
		t.Errorf("non-deny bash should allow; got %v", got)
	}
}

func TestMatchRule_ExactMatch(t *testing.T) {
	if !matchRule("Bash:ls", "Bash", "ls") {
		t.Error("exact match should hit")
	}
	if matchRule("Bash:ls", "Bash", "ls -la") {
		t.Error("exact match must not match prefix")
	}
}

func TestMatchRule_ToolOnly(t *testing.T) {
	if !matchRule("Bash", "Bash", "anything") {
		t.Error("tool-only rule should match any action")
	}
	if matchRule("Bash", "Edit", "anything") {
		t.Error("tool-only rule must not cross tools")
	}
}

func TestLoadAndMarshalRoundTrip(t *testing.T) {
	pol := &Policy{
		Allow: []string{"bash:ls *"},
		Deny:  []string{"bash:rm -rf *"},
		Ask:   []string{"bash:git *"},
	}
	data, err := Marshal(pol)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Allow) != 1 || got.Allow[0] != "bash:ls *" {
		t.Errorf("Allow round-trip: %#v", got.Allow)
	}
	if len(got.Deny) != 1 || got.Deny[0] != "bash:rm -rf *" {
		t.Errorf("Deny round-trip: %#v", got.Deny)
	}
	if len(got.Ask) != 1 || got.Ask[0] != "bash:git *" {
		t.Errorf("Ask round-trip: %#v", got.Ask)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if p == nil {
		t.Fatal("expected empty Policy, got nil")
	}
	if len(p.Allow)+len(p.Deny)+len(p.Ask) != 0 {
		t.Errorf("expected empty policy, got %#v", p)
	}
}

func TestDecisionString(t *testing.T) {
	cases := map[Decision]string{
		DecisionAllow:   "allow",
		DecisionDeny:    "deny",
		DecisionAsk:     "ask",
		DecisionDefault: "default",
	}
	for d, want := range cases {
		if got := d.String(); got != want {
			t.Errorf("Decision(%d).String() = %q, want %q", d, got, want)
		}
	}
}
