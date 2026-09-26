package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
)

// writeConfigForReconcileTest lays down a config dir holding a workspace.toml
// with the given workspace name, and returns the config dir and config path.
func writeConfigForReconcileTest(t *testing.T, name string) (configDir, configPath string) {
	t.Helper()
	configDir = filepath.Join(t.TempDir(), ".niwa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	configPath = filepath.Join(configDir, config.ConfigFile)
	writeWorkspaceNameForReconcileTest(t, configPath, name)
	return configDir, configPath
}

func writeWorkspaceNameForReconcileTest(t *testing.T, configPath, name string) {
	t.Helper()
	body := "[workspace]\nname = \"" + name + "\"\n"
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}

// markSnapshotForReconcileTest writes a provenance marker naming a GitHub
// source. Paired with a nil fetcher, refreshSnapshot keeps the cached snapshot
// and performs no network I/O, which isolates the reload this test is about.
func markSnapshotForReconcileTest(t *testing.T, configDir string) {
	t.Helper()
	err := WriteProvenance(configDir, Provenance{
		SourceURL:      "https://github.com/acme/ws",
		Host:           "github.com",
		Owner:          "acme",
		Repo:           "ws",
		Subpath:        ".niwa",
		Ref:            "main",
		ResolvedCommit: "0000000000000000000000000000000000000000",
		FetchedAt:      time.Now().UTC(),
		FetchMechanism: "tarball",
	})
	if err != nil {
		t.Fatalf("writing provenance marker: %v", err)
	}
}

// TestReconcileAndReloadConfig_ReloadsFromDisk is the unit-level guard for
// issue #227: the caller must get the config that is on disk now, not the one
// it loaded earlier. Rewriting the file behind the initial load stands in for
// the atomic swap.
//
// It pins the reload, not the reconcile-then-reload ordering: the nil fetcher
// makes refreshSnapshot a documented no-op, so this would also pass on an
// implementation that reloaded first. The ordering -- the thing the bug was
// actually about -- is covered end-to-end by the @critical scenarios in
// test/functional/features/workspace-config-sources.feature, which drive a real
// upstream change through the compiled binary.
func TestReconcileAndReloadConfig_ReloadsFromDisk(t *testing.T) {
	configDir, configPath := writeConfigForReconcileTest(t, "before")

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if loaded.Config.Workspace.Name != "before" {
		t.Fatalf("expected initial name \"before\", got %q", loaded.Config.Workspace.Name)
	}

	markSnapshotForReconcileTest(t, configDir)
	writeWorkspaceNameForReconcileTest(t, configPath, "after")

	got, err := ReconcileAndReloadConfig(context.Background(), configPath, nil, nil, loaded)
	if err != nil {
		t.Fatalf("ReconcileAndReloadConfig: %v", err)
	}
	if got.Config.Workspace.Name != "after" {
		t.Errorf("expected reconcile to reload the swapped config (name \"after\"), got %q -- "+
			"materialization would run off the pre-refresh read", got.Config.Workspace.Name)
	}
}

// TestReconcileAndReloadConfig_NoMarkerPassesThrough covers the gate that
// keeps this off local-only workspaces: with no provenance marker there is no
// source to track, so the caller's already-loaded config is returned as-is and
// no reload happens.
func TestReconcileAndReloadConfig_NoMarkerPassesThrough(t *testing.T) {
	_, configPath := writeConfigForReconcileTest(t, "before")

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	writeWorkspaceNameForReconcileTest(t, configPath, "after")

	got, err := ReconcileAndReloadConfig(context.Background(), configPath, nil, nil, loaded)
	if err != nil {
		t.Fatalf("ReconcileAndReloadConfig: %v", err)
	}
	if got != loaded {
		t.Errorf("expected the caller's own load result back when there is no marker")
	}
	if got.Config.Workspace.Name != "before" {
		t.Errorf("expected no reload without a marker, got name %q", got.Config.Workspace.Name)
	}
}
