package functional

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestTarballFakeServer_EndToEnd verifies the fake server, the github
// client, and EnsureConfigSnapshot compose end-to-end without a real
// network call. This is the integration test that backs the design's
// Test Strategy commitment per PRD: "the GitHub-path acceptance
// criteria can be verified mechanically via tarballFakeServer."
//
// Run with: go test ./test/functional/... -run TestTarballFakeServer
// (does NOT require NIWA_TEST_BINARY since it doesn't shell out).
func TestTarballFakeServer_EndToEnd(t *testing.T) {
	srv := newTarballFakeServer()
	defer srv.Close()

	srv.SetTarball("tsukumogami", "niwa", "HEAD", map[string]string{
		"wrap/":       "",
		"wrap/.niwa/": "",
		"wrap/.niwa/workspace.toml": `[workspace]
name = "demo"
`,
		"wrap/.niwa/hooks/start.sh": "#!/bin/bash\necho hi\n",
	})
	srv.SetCommit("tsukumogami", "niwa", "HEAD", "9f8e7d6c5b4a3210abcdef0123456789abcdef01")

	t.Setenv("NIWA_GITHUB_API_URL", srv.URL())
	gh := github.NewAPIClient("")

	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant a marker so case-1 "snapshot present, refresh on drift" path
	// dispatches. Stale commit oid forces a refresh.
	mustWriteScenarioMarker(t, dir, "tsukumogami", "niwa", ".niwa", "stale-oid")

	if err := workspace.EnsureConfigSnapshot(context.Background(), dir, gh, nil); err != nil {
		t.Fatalf("EnsureConfigSnapshot: %v", err)
	}

	// Snapshot materialized.
	got, err := os.ReadFile(filepath.Join(dir, "workspace.toml"))
	if err != nil {
		t.Fatalf("workspace.toml not extracted: %v", err)
	}
	if !strings.Contains(string(got), `name = "demo"`) {
		t.Errorf("workspace.toml unexpected: %q", got)
	}
	hook, err := os.ReadFile(filepath.Join(dir, "hooks/start.sh"))
	if err != nil {
		t.Fatalf("hook not extracted: %v", err)
	}
	if !strings.Contains(string(hook), "echo hi") {
		t.Errorf("hook unexpected: %q", hook)
	}

	// Marker reflects the new oid.
	prov, err := workspace.ReadProvenance(dir)
	if err != nil {
		t.Fatal(err)
	}
	if prov.ResolvedCommit != "9f8e7d6c5b4a3210abcdef0123456789abcdef01" {
		t.Errorf("marker oid: %q", prov.ResolvedCommit)
	}

	// Second EnsureConfigSnapshot: oid matches, should NOT re-fetch tarball.
	srv.ResetLog()
	if err := workspace.EnsureConfigSnapshot(context.Background(), dir, gh, nil); err != nil {
		t.Fatalf("second EnsureConfigSnapshot: %v", err)
	}
	if got := srv.CountRequests("/tarball/"); got != 0 {
		t.Errorf("expected 0 tarball requests when oid matches, got %d", got)
	}
	if got := srv.CountRequests("/commits/"); got == 0 {
		t.Errorf("expected commits SHA endpoint to be hit at least once for drift check")
	}
}

func TestTarballFakeServer_RenameRedirect(t *testing.T) {
	srv := newTarballFakeServer()
	defer srv.Close()

	srv.SetRename("oldorg", "oldrepo", "neworg", "newrepo")
	srv.SetTarball("neworg", "newrepo", "HEAD", map[string]string{
		"wrap/":         "",
		"wrap/file.txt": "renamed-content",
	})
	srv.SetCommit("neworg", "newrepo", "HEAD", "renamed-oid-1234567890")

	t.Setenv("NIWA_GITHUB_API_URL", srv.URL())
	gh := github.NewAPIClient("")

	body, _, status, redirect, err := gh.FetchTarball(context.Background(), "oldorg", "oldrepo", "HEAD", "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer body.Close()
	if status != 200 {
		t.Errorf("status = %d", status)
	}
	if redirect == nil {
		t.Fatal("expected RenameRedirect, got nil")
	}
	if redirect.OldOwner != "oldorg" || redirect.NewOwner != "neworg" {
		t.Errorf("redirect: %+v", redirect)
	}
}

func mustWriteScenarioMarker(t *testing.T, dir, owner, repo, subpath, oid string) {
	t.Helper()
	prov := workspace.Provenance{
		SourceURL:      owner + "/" + repo + ":" + subpath,
		Host:           "github.com",
		Owner:          owner,
		Repo:           repo,
		Subpath:        subpath,
		ResolvedCommit: oid,
		FetchedAt:      time.Now().UTC(),
		FetchMechanism: workspace.FetchMechanismGitHubTarball,
	}
	if err := workspace.WriteProvenance(dir, prov); err != nil {
		t.Fatal(err)
	}
}
