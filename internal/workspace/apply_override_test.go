package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// TestCreate_PropagatesConfigNameOverride: when the workspace-root init
// state carries a ConfigNameOverride, Applier.Create resolves the
// effective name via EffectiveConfigName and writes it into the
// resulting instance state. Covers PRD AC-5 / AC-6 / AC-8d at the
// apply.go layer.
func TestCreate_PropagatesConfigNameOverride(t *testing.T) {
	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Workspace config declares the upstream name.
	configTOML := `
[workspace]
name = "upstream"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Workspace-root init state carries the override (`niwa init my-name`
	// would have written this).
	initState := &InstanceState{
		SchemaVersion:      SchemaVersion,
		ConfigNameOverride: "my-name",
	}
	if err := SaveState(tmpDir, initState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	})
	applier.Cloner = &Cloner{}

	workspaceRoot := tmpDir
	// Pre-create the repo dir with a .git marker so the cloner is a no-op.
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "my-name", "all", "repo1", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	gotPath, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot, "my-name")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	wantInstance := filepath.Join(workspaceRoot, "my-name")
	if gotPath != wantInstance {
		t.Errorf("Create returned %q, want %q", gotPath, wantInstance)
	}

	// Load the resulting instance state and assert the override
	// flowed into ConfigName + InstanceNumber resolved correctly.
	state, err := LoadState(wantInstance)
	if err != nil {
		t.Fatalf("LoadState(instance): %v", err)
	}
	if state.ConfigName == nil {
		t.Fatal("instance state ConfigName is nil")
	}
	if *state.ConfigName != "my-name" {
		t.Errorf("instance ConfigName: got %q, want %q (override should win)", *state.ConfigName, "my-name")
	}
	// AC-5 / AC-8d invariant: subsequent readers of instance state see
	// the override, not the cloned config's [workspace] name.
	if *state.ConfigName == "upstream" {
		t.Errorf("instance ConfigName retained upstream value; override silently dropped")
	}
	// instanceNumberFromName must have received the effective name so
	// the equality check `instanceName == configName` matched and
	// returned 1, not 0.
	if state.InstanceNumber != 1 {
		t.Errorf("InstanceNumber: got %d, want 1 (instanceNumberFromName must use effective name)", state.InstanceNumber)
	}
}

// TestCreate_TamperedConfigNameOverride_Errors: defense in depth per
// Security §4. A persistence-boundary attacker who rewrites the
// workspace-root state file to carry a path-traversal value MUST NOT
// see Applier.Create silently use it.
func TestCreate_TamperedConfigNameOverride_Errors(t *testing.T) {
	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "upstream"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Tampered override: contains a path-traversal sentinel that the
	// init-time validator would reject. EffectiveConfigName must
	// re-validate at apply time.
	initState := &InstanceState{
		SchemaVersion:      SchemaVersion,
		ConfigNameOverride: "..",
	}
	if err := SaveState(tmpDir, initState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	})
	applier.Cloner = &Cloner{}

	_, err = applier.Create(context.Background(), cfg, niwaDir, tmpDir, "upstream")
	if err == nil {
		t.Fatal("Create succeeded with tampered ConfigNameOverride; expected error")
	}
	if !strings.Contains(err.Error(), "ConfigNameOverride") {
		t.Errorf("error %q does not name the field; future debuggers will struggle", err.Error())
	}
}

// TestApply_PropagatesConfigNameOverride: a subsequent Applier.Apply on
// an instance whose workspace root carries an override re-resolves the
// effective name on every run, so a future cfg edit doesn't rotate the
// surface name back to the upstream value.
func TestApply_PropagatesConfigNameOverride(t *testing.T) {
	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "upstream"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveState(tmpDir, &InstanceState{
		SchemaVersion:      SchemaVersion,
		ConfigNameOverride: "my-name",
	}); err != nil {
		t.Fatalf("SaveState init: %v", err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Config

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	})
	applier.Cloner = &Cloner{}

	if err := os.MkdirAll(filepath.Join(tmpDir, "my-name", "all", "repo1", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, tmpDir, "my-name")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Now run Apply on the same instance.
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState post-apply: %v", err)
	}
	if state.ConfigName == nil || *state.ConfigName != "my-name" {
		got := "<nil>"
		if state.ConfigName != nil {
			got = *state.ConfigName
		}
		t.Errorf("post-apply ConfigName: got %q, want %q (Apply must re-read override)", got, "my-name")
	}
}
