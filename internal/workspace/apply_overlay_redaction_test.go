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

// TestApplyScrubsOverlayResolvedSecretFromSetupOutput closes the redactor's
// coverage gap on overlay-resolved values.
//
// The overlay pre-pass resolves the overlay's own env before the rest of the
// pipeline runs. While the redactor was constructed next to the resolver
// stage, those values were never registered: they merge into the effective
// config, the later resolution pass skips them because they are already marked
// resolved, and they are then materialized into the very working directory a
// setup script runs in. So on any workspace using an overlay, a class of
// secrets reached the script's cwd having never been seen by the redactor.
//
// This test fails with the redactor built at the resolver stage and passes
// with it built at the top of runPipeline.
func TestApplyScrubsOverlayResolvedSecretFromSetupOutput(t *testing.T) {
	withFakeVaultBackend(t)

	const plaintext = "overlay-resolved-secret-value"

	overlayTOML := `
[vault.provider]
kind = "fake"

[vault.provider.values]
OVERLAY_TOKEN = "` + plaintext + `"

[env.secrets]
OVERLAY_TOKEN = "vault://OVERLAY_TOKEN"
`
	overlaySrc := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

	// A setup script that reads the env file niwa materialized into its own
	// working directory one pipeline step earlier — the exact shape of the
	// exposure this covers.
	writeSetupScript(t, filepath.Join(instanceRoot, "all", "repo1"),
		"01-show-env.sh", "#!/bin/sh\ncat .local.env\n")

	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "abc123",
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	})
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = func(_ context.Context, _, dir string) (bool, int, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, 0, err
		}
		data, err := os.ReadFile(filepath.Join(overlaySrc, "workspace-overlay.toml"))
		if err != nil {
			return false, 0, err
		}
		return false, 0, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	applier.headSHA = func(string) (string, error) { return "abc123", nil }

	var out syncBuffer
	applier.Reporter = NewReporterWithTTY(&out, false)

	if err := applier.Apply(context.Background(), loaded.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The overlay secret really did reach the script's working directory —
	// otherwise the scrub assertion below would pass vacuously.
	envFile, err := os.ReadFile(filepath.Join(instanceRoot, "all", "repo1", ".local.env"))
	if err != nil {
		t.Fatalf("reading materialized env file: %v", err)
	}
	if !strings.Contains(string(envFile), plaintext) {
		t.Fatalf("overlay secret was not materialized into the repo dir; fixture is wrong:\n%s", envFile)
	}

	perm := permanentOutput(out.String())
	if !strings.Contains(perm, "[repo1/01-show-env.sh] OVERLAY_TOKEN=") {
		t.Fatalf("the setup script's output did not reach the operator:\n%s", perm)
	}
	if strings.Contains(perm, plaintext) {
		t.Errorf("overlay-resolved secret was printed unscrubbed:\n%s", perm)
	}
	if !strings.Contains(perm, "[repo1/01-show-env.sh] OVERLAY_TOKEN=***") {
		t.Errorf("expected the redacted placeholder in:\n%s", perm)
	}
}
