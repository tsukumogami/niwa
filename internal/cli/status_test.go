package cli

import (
	"encoding/json"
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

// TestSourceLabel verifies that sourceLabel maps each SourceKind constant to its
// expected display string, and that an unknown kind falls back to the kind itself.
func TestSourceLabel(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{workspace.SourceKindPlaintext, "plaintext"},
		{workspace.SourceKindVault, "vault"},
		{workspace.SourceKindEnvExample, ".env.example"},
		{"unknown_kind", "unknown_kind"},
	}
	for _, tc := range cases {
		got := sourceLabel(tc.kind)
		if got != tc.want {
			t.Errorf("sourceLabel(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// TestShowDetailView_VerboseEnvExampleSource verifies that showDetailView with
// --verbose shows ".env.example" as the source label for a managed file whose
// Sources slice contains a SourceKindEnvExample entry.
func TestShowDetailView_VerboseEnvExampleSource(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)
	configName := "test-ws"

	// Create a managed file.
	envFile := filepath.Join(root, ".local.env")
	if err := os.WriteFile(envFile, []byte("PORT=8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := workspace.HashFile(envFile)
	if err != nil {
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
			{
				Path:        envFile,
				ContentHash: hash,
				Generated:   now,
				Sources: []workspace.SourceEntry{
					{Kind: workspace.SourceKindEnvExample, SourceID: ".env.example"},
				},
			},
		},
	}

	if err := workspace.SaveState(root, state); err != nil {
		t.Fatal(err)
	}

	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)

	// Set verbose flag, reset after test.
	statusVerbose = true
	defer func() { statusVerbose = false }()

	if err := showDetailView(statusCmd, root); err != nil {
		t.Fatalf("showDetailView: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, ".env.example") {
		t.Errorf("verbose output should contain .env.example source label, got:\n%s", output)
	}
	// Must not fall back to vault or plaintext label.
	if strings.Contains(output, "vault://.env.example") {
		t.Errorf("must not display vault label for env_example source, got:\n%s", output)
	}
	if strings.Contains(output, "plaintext://.env.example") {
		t.Errorf("must not display plaintext label for env_example source, got:\n%s", output)
	}
}

// TestBuildMeshSummary_ChanneledWorkspace seeds tasks in assorted states,
// writes a minimal channeled workspace.toml at the workspace root, and
// asserts the rendered summary line counts match the fixtures.
func TestBuildMeshSummary_ChanneledWorkspace(t *testing.T) {
	wsRoot := t.TempDir()
	instanceRoot := filepath.Join(wsRoot, "instance")
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa", "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Channels opt-in: workspace.toml at wsRoot with [channels.mesh].
	if err := os.MkdirAll(filepath.Join(wsRoot, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir ws .niwa: %v", err)
	}
	tomlData := "[workspace]\nname = \"test-ws\"\n\n[channels.mesh]\n"
	if err := os.WriteFile(filepath.Join(wsRoot, ".niwa", "workspace.toml"), []byte(tomlData), 0o600); err != nil {
		t.Fatalf("write workspace.toml: %v", err)
	}

	now := time.Now().UTC()
	recent := now.Add(-2 * time.Hour)
	old := now.Add(-48 * time.Hour)

	// 2 queued, 1 running, 1 completed in last 24h, 1 abandoned in last
	// 24h, 1 completed outside the 24h window (should NOT count).
	fixtures := []struct {
		id          string
		state       string
		transitions []struct{ To, At string }
	}{
		{id: "aaaaaaaa-1111-4111-8111-111111111111", state: "queued"},
		{id: "bbbbbbbb-2222-4222-8222-222222222222", state: "queued"},
		{id: "cccccccc-3333-4333-8333-333333333333", state: "running"},
		{id: "dddddddd-4444-4444-8444-444444444444", state: "completed",
			transitions: []struct{ To, At string }{
				{To: "completed", At: recent.Format(time.RFC3339)},
			}},
		{id: "eeeeeeee-5555-4555-8555-555555555555", state: "abandoned",
			transitions: []struct{ To, At string }{
				{To: "abandoned", At: recent.Format(time.RFC3339)},
			}},
		{id: "ffffffff-6666-4666-8666-666666666666", state: "completed",
			transitions: []struct{ To, At string }{
				{To: "completed", At: old.Format(time.RFC3339)},
			}},
	}
	for _, f := range fixtures {
		dir := filepath.Join(instanceRoot, ".niwa", "tasks", f.id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir task: %v", err)
		}
		state := map[string]any{
			"v":       1,
			"task_id": f.id,
			"state":   f.state,
			"state_transitions": append([]map[string]string{
				{"from": "", "to": "queued", "at": now.Format(time.RFC3339)},
			}, func() []map[string]string {
				var out []map[string]string
				for _, tr := range f.transitions {
					out = append(out, map[string]string{"from": "queued", "to": tr.To, "at": tr.At})
				}
				return out
			}()...),
			"max_restarts":   3,
			"delegator_role": "coordinator",
			"target_role":    "web",
			"worker":         map[string]any{"pid": 0, "start_time": 0, "role": "web"},
			"updated_at":     now.Format(time.RFC3339),
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal state: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
			t.Fatalf("write state.json: %v", err)
		}
	}

	got := buildMeshSummary(instanceRoot)
	want := "2 queued, 1 running, 1 completed (last 24h), 1 abandoned (last 24h)"
	if got != want {
		t.Errorf("buildMeshSummary = %q, want %q", got, want)
	}
}

func TestBuildMeshSummary_NotChanneledReturnsEmpty(t *testing.T) {
	wsRoot := t.TempDir()
	instanceRoot := filepath.Join(wsRoot, "instance")
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa", "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No workspace.toml → not channeled.
	if got := buildMeshSummary(instanceRoot); got != "" {
		t.Errorf("expected empty mesh summary when not channeled, got %q", got)
	}
}

func TestBuildMeshSummary_ChanneledWithNoTasks(t *testing.T) {
	wsRoot := t.TempDir()
	instanceRoot := filepath.Join(wsRoot, "instance")
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(wsRoot, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir ws .niwa: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsRoot, ".niwa", "workspace.toml"), []byte("[workspace]\nname = \"test-ws\"\n\n[channels.mesh]\n"), 0o600); err != nil {
		t.Fatalf("write workspace.toml: %v", err)
	}
	got := buildMeshSummary(instanceRoot)
	want := "0 queued, 0 running, 0 completed (last 24h), 0 abandoned (last 24h)"
	if got != want {
		t.Errorf("buildMeshSummary empty = %q, want %q", got, want)
	}
}
