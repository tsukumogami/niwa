package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerPermissionMode(t *testing.T) {
	tests := []struct {
		name    string
		content string // empty string means no file is created
		want    string
	}{
		{
			name:    "file absent",
			content: "",
			want:    "acceptEdits",
		},
		{
			name:    "bypassPermissions",
			content: `{"permissions":{"defaultMode":"bypassPermissions"}}`,
			want:    "bypassPermissions",
		},
		{
			name:    "askPermissions returns acceptEdits",
			content: `{"permissions":{"defaultMode":"askPermissions"}}`,
			want:    "acceptEdits",
		},
		{
			name:    "permissions key absent",
			content: `{"env":{"FOO":"bar"}}`,
			want:    "acceptEdits",
		},
		{
			name:    "malformed JSON",
			content: `not valid json {`,
			want:    "acceptEdits",
		},
		{
			name:    "empty defaultMode",
			content: `{"permissions":{"defaultMode":""}}`,
			want:    "acceptEdits",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.content != "" {
				claudeDir := filepath.Join(root, ".claude")
				if err := os.MkdirAll(claudeDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := WorkerPermissionMode(root)
			if got != tc.want {
				t.Errorf("WorkerPermissionMode() = %q, want %q", got, tc.want)
			}
		})
	}
}
