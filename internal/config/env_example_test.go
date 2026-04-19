package config

import (
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestEffectiveReadEnvExample(t *testing.T) {
	tests := []struct {
		name           string
		workspaceLevel *bool
		repoLevel      *bool // nil means no per-repo entry
		repoName       string
		want           bool
	}{
		{
			name:           "both nil — default on",
			workspaceLevel: nil,
			repoLevel:      nil,
			repoName:       "myrepo",
			want:           true,
		},
		{
			name:           "workspace false, no per-repo — false",
			workspaceLevel: boolPtr(false),
			repoLevel:      nil,
			repoName:       "myrepo",
			want:           false,
		},
		{
			name:           "workspace false, per-repo true — per-repo wins",
			workspaceLevel: boolPtr(false),
			repoLevel:      boolPtr(true),
			repoName:       "myrepo",
			want:           true,
		},
		{
			name:           "workspace true, per-repo false — per-repo suppresses",
			workspaceLevel: boolPtr(true),
			repoLevel:      boolPtr(false),
			repoName:       "myrepo",
			want:           false,
		},
		{
			name:           "workspace true, no per-repo — true",
			workspaceLevel: boolPtr(true),
			repoLevel:      nil,
			repoName:       "myrepo",
			want:           true,
		},
		{
			name:           "repo not in repos map — falls back to workspace",
			workspaceLevel: boolPtr(false),
			repoLevel:      nil,
			repoName:       "unknown-repo",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := &WorkspaceConfig{
				Workspace: WorkspaceMeta{
					Name:           "test-ws",
					ReadEnvExample: tt.workspaceLevel,
				},
			}

			if tt.repoLevel != nil {
				if ws.Repos == nil {
					ws.Repos = make(map[string]RepoOverride)
				}
				ws.Repos[tt.repoName] = RepoOverride{ReadEnvExample: tt.repoLevel}
			}

			got := EffectiveReadEnvExample(ws, tt.repoName)
			if got != tt.want {
				t.Errorf("EffectiveReadEnvExample(..., %q) = %v, want %v", tt.repoName, got, tt.want)
			}
		})
	}
}

func TestEffectiveReadEnvExampleNilWorkspace(t *testing.T) {
	// A nil workspace should return true (safe default).
	if got := EffectiveReadEnvExample(nil, "any"); !got {
		t.Error("EffectiveReadEnvExample(nil, ...) = false, want true")
	}
}

func TestReadEnvExampleRoundTrip(t *testing.T) {
	// Verify that the TOML tags work for both WorkspaceMeta and RepoOverride.
	input := `
[workspace]
name = "test-ws"
read_env_example = false

[[sources]]
org = "myorg"

[repos.myrepo]
read_env_example = true
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	cfg := result.Config

	if cfg.Workspace.ReadEnvExample == nil {
		t.Fatal("workspace.read_env_example should not be nil")
	}
	if *cfg.Workspace.ReadEnvExample != false {
		t.Errorf("workspace.read_env_example = %v, want false", *cfg.Workspace.ReadEnvExample)
	}

	repo, ok := cfg.Repos["myrepo"]
	if !ok {
		t.Fatal("repos[myrepo] missing")
	}
	if repo.ReadEnvExample == nil {
		t.Fatal("repos[myrepo].read_env_example should not be nil")
	}
	if *repo.ReadEnvExample != true {
		t.Errorf("repos[myrepo].read_env_example = %v, want true", *repo.ReadEnvExample)
	}
}

func TestReadEnvExampleAbsentMeansNil(t *testing.T) {
	// When read_env_example is not present in TOML, the field must be nil.
	input := `
[workspace]
name = "test-ws"

[[sources]]
org = "myorg"

[repos.myrepo]
scope = "tactical"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	cfg := result.Config

	if cfg.Workspace.ReadEnvExample != nil {
		t.Errorf("workspace.read_env_example should be nil when absent, got %v", *cfg.Workspace.ReadEnvExample)
	}

	repo := cfg.Repos["myrepo"]
	if repo.ReadEnvExample != nil {
		t.Errorf("repos[myrepo].read_env_example should be nil when absent, got %v", *repo.ReadEnvExample)
	}
}
