package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestStatusCmd_AcceptsArgs(t *testing.T) {
	if err := statusCmd.Args(statusCmd, []string{}); err != nil {
		t.Errorf("should accept zero args: %v", err)
	}
	if err := statusCmd.Args(statusCmd, []string{"my-instance"}); err != nil {
		t.Errorf("should accept one arg: %v", err)
	}
	if err := statusCmd.Args(statusCmd, []string{"a", "b"}); err == nil {
		t.Error("should reject two args")
	}
}

func TestFormatRelativeTime(t *testing.T) {
	tests := []struct {
		name     string
		offset   time.Duration
		expected string
	}{
		{"just now", 30 * time.Second, "just now"},
		{"minutes", 5 * time.Minute, "5m ago"},
		{"one minute", 1 * time.Minute, "1m ago"},
		{"hours", 3 * time.Hour, "3h ago"},
		{"one hour", 1 * time.Hour, "1h ago"},
		{"days", 2 * 24 * time.Hour, "2d ago"},
		{"one day", 24 * time.Hour, "1d ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := time.Now().Add(-tt.offset)
			result := formatRelativeTime(ts)
			if result != tt.expected {
				t.Errorf("formatRelativeTime(%v ago) = %q, want %q", tt.offset, result, tt.expected)
			}
		})
	}
}

func TestShowDetailView(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 3, 25, 14, 30, 0, 0, time.UTC)
	configName := "test-ws"

	// Create a managed file.
	claudeMD := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(claudeMD, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := workspace.HashFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}

	// Create a repo directory.
	if err := os.MkdirAll(filepath.Join(root, "public", "app"), 0o755); err != nil {
		t.Fatal(err)
	}

	state := &workspace.InstanceState{
		SchemaVersion:  workspace.SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           root,
		Created:        now,
		LastApplied:    now,
		ManagedFiles: []workspace.ManagedFile{
			{Path: claudeMD, ContentHash: hash, Generated: now},
		},
		Repos: map[string]workspace.RepoState{
			"app": {URL: "git@github.com:org/app.git", Cloned: true},
		},
	}

	if err := workspace.SaveState(root, state); err != nil {
		t.Fatal(err)
	}

	// Use a cobra command to capture output.
	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)

	if err := showDetailView(statusCmd, root); err != nil {
		t.Fatalf("showDetailView: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Instance: test-ws") {
		t.Errorf("output should contain instance name, got:\n%s", output)
	}
	if !strings.Contains(output, "Config:   test-ws") {
		t.Errorf("output should contain config name, got:\n%s", output)
	}
	if !strings.Contains(output, "Repos:") {
		t.Errorf("output should contain repos header, got:\n%s", output)
	}
	if !strings.Contains(output, "app") {
		t.Errorf("output should contain repo name, got:\n%s", output)
	}
	if !strings.Contains(output, "cloned") {
		t.Errorf("output should contain clone status, got:\n%s", output)
	}
	if !strings.Contains(output, "Managed files:") {
		t.Errorf("output should contain managed files header, got:\n%s", output)
	}
	if !strings.Contains(output, "CLAUDE.md") {
		t.Errorf("output should contain managed file name, got:\n%s", output)
	}
	if !strings.Contains(output, "ok") {
		t.Errorf("output should contain file status, got:\n%s", output)
	}
}

func TestShowSummaryView(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)
	configName := "test-ws"

	// Create an instance.
	instanceDir := filepath.Join(root, "test-ws")
	if err := os.MkdirAll(instanceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	state := &workspace.InstanceState{
		SchemaVersion:  workspace.SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceDir,
		Created:        now,
		LastApplied:    now,
		Repos: map[string]workspace.RepoState{
			"app": {URL: "git@github.com:org/app.git", Cloned: true},
		},
	}

	if err := workspace.SaveState(instanceDir, state); err != nil {
		t.Fatal(err)
	}

	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)

	if err := showSummaryView(statusCmd, root); err != nil {
		t.Fatalf("showSummaryView: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Instances:") {
		t.Errorf("output should contain instances header, got:\n%s", output)
	}
	if !strings.Contains(output, "test-ws") {
		t.Errorf("output should contain instance name, got:\n%s", output)
	}
	if !strings.Contains(output, "1 repos") {
		t.Errorf("output should contain repo count, got:\n%s", output)
	}
	if !strings.Contains(output, "0 drifted") {
		t.Errorf("output should contain drift count, got:\n%s", output)
	}
	if !strings.Contains(output, "applied") {
		t.Errorf("output should contain applied time, got:\n%s", output)
	}
}

// TestStatusSummaryLineReflectsShadowCount locks in the state:
//   - state.Shadows empty → no summary line.
//   - state.Shadows non-empty → a summary line reflecting the count
//     (with proper singular/plural "key"/"keys").
func TestStatusSummaryLineReflectsShadowCount(t *testing.T) {
	tests := []struct {
		name    string
		shadows []workspace.Shadow
		want    string // empty means "no summary line should appear"
	}{
		{
			name:    "zero shadows emits no summary",
			shadows: nil,
			want:    "",
		},
		{
			name: "one shadow uses singular noun",
			shadows: []workspace.Shadow{
				{Kind: "env-var", Name: "FOO", TeamSource: "workspace.toml", PersonalSource: "niwa.toml", Layer: "personal-overlay"},
			},
			want: "1 key shadowed by personal overlay",
		},
		{
			name: "multiple shadows use plural noun",
			shadows: []workspace.Shadow{
				{Kind: "env-var", Name: "FOO", TeamSource: "workspace.toml", PersonalSource: "niwa.toml", Layer: "personal-overlay"},
				{Kind: "env-var", Name: "BAR", TeamSource: "workspace.toml", PersonalSource: "niwa.toml", Layer: "personal-overlay"},
			},
			want: "2 keys shadowed by personal overlay",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
			configName := "shadow-ws"

			state := &workspace.InstanceState{
				SchemaVersion:  workspace.SchemaVersion,
				ConfigName:     &configName,
				InstanceName:   "shadow-ws",
				InstanceNumber: 1,
				Root:           root,
				Created:        now,
				LastApplied:    now,
				Shadows:        tt.shadows,
			}
			if err := workspace.SaveState(root, state); err != nil {
				t.Fatalf("SaveState: %v", err)
			}

			buf := &strings.Builder{}
			statusCmd.SetOut(buf)
			defer statusCmd.SetOut(os.Stdout)

			if err := showDetailView(statusCmd, root); err != nil {
				t.Fatalf("showDetailView: %v", err)
			}
			output := buf.String()

			if tt.want == "" {
				if strings.Contains(output, "shadowed by personal overlay") {
					t.Errorf("expected no summary line, got:\n%s", output)
				}
			} else {
				if !strings.Contains(output, tt.want) {
					t.Errorf("expected %q in output, got:\n%s", tt.want, output)
				}
			}
		})
	}
}

func TestShowSummaryView_NoInstances(t *testing.T) {
	root := t.TempDir()

	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)

	if err := showSummaryView(statusCmd, root); err != nil {
		t.Fatalf("showSummaryView: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No instances found.") {
		t.Errorf("output should indicate no instances, got:\n%s", output)
	}
}
