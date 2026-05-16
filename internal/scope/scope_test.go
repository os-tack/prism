package scope

import (
	"reflect"
	"testing"
)

func TestDefaultGlobs_Empty(t *testing.T) {
	got := DefaultGlobs("")
	want := []string{"**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultGlobs(\"\") = %v, want %v", got, want)
	}
}

func TestDefaultGlobs_Single(t *testing.T) {
	got := DefaultGlobs("src/billing")
	want := []string{"src/billing/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultGlobs(\"src/billing\") = %v, want %v", got, want)
	}
}

func TestSlug_Simple(t *testing.T) {
	got := Slug("src/billing")
	want := "src-billing"
	if got != want {
		t.Fatalf("Slug(\"src/billing\") = %q, want %q", got, want)
	}
}

func TestSlug_Nested(t *testing.T) {
	got := Slug("src/billing/api")
	want := "src-billing-api"
	if got != want {
		t.Fatalf("Slug(\"src/billing/api\") = %q, want %q", got, want)
	}
}

func TestSlug_Empty(t *testing.T) {
	// Implementation returns "" for empty input — lock that in.
	got := Slug("")
	want := ""
	if got != want {
		t.Fatalf("Slug(\"\") = %q, want %q", got, want)
	}
}

func TestSlug_Idempotent(t *testing.T) {
	cases := []string{
		"src/billing",
		"src/billing/api/v2",
		"a",
		"already-dashed",
	}
	for _, in := range cases {
		once := Slug(in)
		twice := Slug(once)
		if once != twice {
			t.Errorf("Slug not idempotent for %q: once=%q, twice=%q", in, once, twice)
		}
	}
}

func TestSafePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"src/billing", true},
		{"src/billing/api", true},
		{"/abs/path", false},
		{"../../etc", false},
		{"src/../escape", false},
		{"src/billing/../../etc", false},
		{".", true},
		{"./src/billing", true},
		{"src/./billing", true},
	}
	for _, tc := range cases {
		if got := SafePath(tc.in); got != tc.want {
			t.Errorf("SafePath(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
