package model

import "testing"

func TestSourceTag_ProjectPath(t *testing.T) {
	p := &Project{AgentsDir: "/proj/.agents"}
	got := p.SourceTag("/proj/.agents/context.md")
	if got != "context.md" {
		t.Fatalf("got %q want %q", got, "context.md")
	}
}

func TestSourceTag_ProjectNested(t *testing.T) {
	p := &Project{AgentsDir: "/proj/.agents"}
	got := p.SourceTag("/proj/.agents/src/billing/context.md")
	if got != "src/billing/context.md" {
		t.Fatalf("got %q", got)
	}
}

func TestSourceTag_GlobalPrefixed(t *testing.T) {
	p := &Project{
		AgentsDir:       "/proj/.agents",
		GlobalAgentsDir: "/home/me/.agents",
	}
	got := p.SourceTag("/home/me/.agents/agents/reviewer.md")
	if got != "global:agents/reviewer.md" {
		t.Fatalf("got %q want %q", got, "global:agents/reviewer.md")
	}
}

func TestSourceTag_UnknownRootFallsBack(t *testing.T) {
	p := &Project{AgentsDir: "/proj/.agents"}
	got := p.SourceTag("/elsewhere/foo.md")
	if got != "/elsewhere/foo.md" {
		t.Fatalf("got %q", got)
	}
}

func TestSourceTag_Empty(t *testing.T) {
	p := &Project{}
	if got := p.SourceTag(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestSourceTag_NoTraversalForSiblingPaths(t *testing.T) {
	// /proj/.agents-other/foo.md is not under /proj/.agents
	p := &Project{AgentsDir: "/proj/.agents"}
	got := p.SourceTag("/proj/.agents-other/foo.md")
	if got != "/proj/.agents-other/foo.md" {
		t.Fatalf("expected absolute fallback, got %q", got)
	}
}
