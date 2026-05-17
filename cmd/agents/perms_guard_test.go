package main

import (
	"testing"
)

func TestExtractToolAndAction(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantTool   string
		wantAction string
	}{
		{
			name:       "bash command",
			payload:    `{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`,
			wantTool:   "Bash",
			wantAction: "rm -rf /",
		},
		{
			name:       "edit file_path",
			payload:    `{"tool_name":"Edit","tool_input":{"file_path":"/tmp/x.go","old_string":"a","new_string":"b"}}`,
			wantTool:   "Edit",
			wantAction: "/tmp/x.go",
		},
		{
			name:       "webfetch url",
			payload:    `{"tool_name":"WebFetch","tool_input":{"url":"https://example.com"}}`,
			wantTool:   "WebFetch",
			wantAction: "https://example.com",
		},
		{
			name:       "no action",
			payload:    `{"tool_name":"Bash","tool_input":{}}`,
			wantTool:   "Bash",
			wantAction: "",
		},
		{
			name:       "malformed json",
			payload:    `{not json`,
			wantTool:   "",
			wantAction: "",
		},
		{
			name:       "empty",
			payload:    ``,
			wantTool:   "",
			wantAction: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, action := extractToolAndAction([]byte(tc.payload))
			if tool != tc.wantTool || action != tc.wantAction {
				t.Errorf("extractToolAndAction = (%q, %q), want (%q, %q)", tool, action, tc.wantTool, tc.wantAction)
			}
		})
	}
}
