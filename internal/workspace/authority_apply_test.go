package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// TestMergeWorkspaceOverlayLeavesAuthority: the overlay's [authority] is a
// separate layer, never folded into the base config.
func TestMergeWorkspaceOverlayLeavesAuthority(t *testing.T) {
	base := &config.WorkspaceConfig{}
	base.Workspace.Name = "ws"
	base.Authority.Level = "autonomous"
	base.Authority.Ceiling = []any{"merge-pr"}
	overlay := &config.WorkspaceOverlay{}
	overlay.Authority.Level = "strict"
	overlay.Authority.Ceiling = []any{}
	overlay.Authority.Roles = map[string]any{"x": map[string]any{"authorities": []any{"merge-pr"}}}

	merged, err := MergeWorkspaceOverlay(base, overlay, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged.Authority, base.Authority) {
		t.Errorf("merged authority = %+v, want the base's %+v", merged.Authority, base.Authority)
	}
}

// TestApplyWarnsOnAuthorityProblems runs the pipeline over a config with an
// invalid authority value, an unknown [authority] key, an overlay roles
// table and ceilings naming an authority the base lacks: apply completes and
// prints each problem once as a warning.
func TestApplyWarnsOnAuthorityProblems(t *testing.T) {
	configTOML := `
[workspace]
name = "authority-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"

[authority]
level = "bogus"
ceiling = ["merge-pr"]
ceilng = ["merge-pr"]
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})
	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	overlayDir := setupOverlayDir(t, "[authority]\nceiling = [\"cut-release\"]\n\n[authority.roles.x]\nauthorities = [\"merge-pr\"]\n")
	if err := SaveState(instanceRoot, &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "authority-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/authority-ws-overlay",
		OverlayCommit:  "abc123",
	}); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(&mockGitHubClient{repos: map[string][]github.Repo{
		"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
	}})
	applier.Cloner = &Cloner{}
	machine, err := config.ParseGlobalConfig([]byte("[global]\nauthority_ceiling = [\"cut-release\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	applier.MachineConfig = machine
	var out strings.Builder
	applier.Reporter = NewReporterWithTTY(&out, false)
	applier.cloneOrSync = func(_ context.Context, _, dir string) (bool, int, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, 0, err
		}
		data, readErr := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if readErr != nil {
			return false, 0, readErr
		}
		return false, 0, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	applier.headSHA = func(string) (string, error) { return "abc123", nil }

	if err := applier.Apply(context.Background(), parsed.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("an authority problem failed the apply: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`invalid [authority] level "bogus"`,
		`invalid [authority] key "ceilng"`,
		"workspace overlay sets [authority.roles], which has no effect",
		"overlay [authority] ceiling names cut-release",
		"[global] authority_ceiling names cut-release",
	} {
		if n := strings.Count(got, want); n != 1 {
			t.Errorf("output carries %q %d times, want once:\n%s", want, n, got)
		}
	}
	if strings.Contains(got, "unknown config field: authority") {
		t.Errorf("an [authority] key was also reported as an unknown field:\n%s", got)
	}
	for _, w := range parsed.Warnings {
		if strings.Contains(w, "authority") {
			t.Errorf("parse warning %q also reports an [authority] key", w)
		}
	}
}
