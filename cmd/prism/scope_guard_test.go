package main

import (
	"testing"
)

func TestExtractToolFilePath(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "edit tool_input.file_path",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"/abs/src/billing/foo.go","old_string":"a","new_string":"b"}}`,
			want:    "/abs/src/billing/foo.go",
		},
		{
			name:    "read tool_input.path",
			payload: `{"tool_name":"Read","tool_input":{"path":"/abs/src/foo.go"}}`,
			want:    "/abs/src/foo.go",
		},
		{
			name:    "no file path → empty",
			payload: `{"tool_name":"Bash","tool_input":{"command":"ls"}}`,
			want:    "",
		},
		{
			name:    "empty stdin",
			payload: ``,
			want:    "",
		},
		{
			name:    "malformed json → empty",
			payload: `{not json`,
			want:    "",
		},
		{
			name:    "notebook_path fallback",
			payload: `{"tool_input":{"notebook_path":"/abs/nb.ipynb"}}`,
			want:    "/abs/nb.ipynb",
		},
		{
			// Windsurf Cascade envelope: file_path at root, no tool_input wrapper.
			// v0.8.1 (review item 2) widened the extractor to handle this.
			name:    "windsurf root-level file_path",
			payload: `{"event":"pre_write_code","file_path":"/abs/src/api/handler.go"}`,
			want:    "/abs/src/api/handler.go",
		},
		{
			// tool_input takes precedence over root-level when both present.
			name:    "tool_input wins over root-level",
			payload: `{"tool_input":{"file_path":"/from/tool_input"},"file_path":"/from/root"}`,
			want:    "/from/tool_input",
		},
		{
			name:    "windsurf root-level path key",
			payload: `{"event":"pre_read_code","path":"/abs/foo"}`,
			want:    "/abs/foo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractToolFilePath([]byte(tc.payload))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPathInScope(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	cases := []struct {
		name  string
		path  string
		scope string
		want  bool
	}{
		{"empty path", "", "src/billing", false},
		{"empty scope", "src/billing/foo.go", "", false},
		{"exact match", "src/billing", "src/billing", true},
		{"under scope", "src/billing/api.go", "src/billing", true},
		{"deeply under scope", "src/billing/api/v1/handler.go", "src/billing", true},
		{"sibling not in scope", "src/payments/api.go", "src/billing", false},
		{"prefix-collision not in scope", "src/billing-extras/foo.go", "src/billing", false},
		{"./ stripped", "./src/billing/foo.go", "src/billing", true},
		{"absolute path without project_dir hint", "/Users/me/proj/src/billing/foo.go", "src/billing", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pathInScope(tc.path, tc.scope)
			if got != tc.want {
				t.Errorf("pathInScope(%q, %q) = %v, want %v", tc.path, tc.scope, got, tc.want)
			}
		})
	}
}

func TestPathInScope_RespectsProjectDir(t *testing.T) {
	// With CLAUDE_PROJECT_DIR set, the absolute path is converted to project-relative
	// first. That kills the substring-collision risk for files under unrelated trees.
	t.Setenv("CLAUDE_PROJECT_DIR", "/Users/me/proj")
	if pathInScope("/Users/me/proj/src/billing/foo.go", "src/billing") != true {
		t.Errorf("expected in-scope match with PROJECT_DIR")
	}
	if pathInScope("/elsewhere/src/billing/foo.go", "src/billing") {
		t.Errorf("expected out-of-scope when path is outside PROJECT_DIR")
	}
}
