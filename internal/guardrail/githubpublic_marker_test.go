package guardrail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnumerateGitHubRemotes_FromMarker(t *testing.T) {
	dir := t.TempDir()
	marker := `source_url = "tsukumogami/niwa:.niwa@main"
host = "github.com"
owner = "tsukumogami"
repo = "niwa"
subpath = ".niwa"
ref = "main"
resolved_commit = "abc123"
fetched_at = 2026-04-23T10:00:00Z
fetch_mechanism = "github-tarball"
`
	if err := os.WriteFile(filepath.Join(dir, ".niwa-snapshot.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	matches, haveGit := EnumerateGitHubRemotes(dir)
	if !haveGit {
		t.Fatal("expected haveGit=true when marker present")
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(matches), matches)
	}
	if matches[0] != "https://github.com/tsukumogami/niwa.git" {
		t.Errorf("synthesized URL: %q", matches[0])
	}
}

func TestEnumerateGitHubRemotes_MarkerNonGitHubHostReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	marker := `source_url = "gitlab.com/group/repo"
host = "gitlab.com"
owner = "group"
repo = "repo"
subpath = ""
ref = ""
resolved_commit = "abc"
fetched_at = 2026-04-23T10:00:00Z
fetch_mechanism = "git-clone-fallback"
`
	if err := os.WriteFile(filepath.Join(dir, ".niwa-snapshot.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	matches, haveGit := EnumerateGitHubRemotes(dir)
	if !haveGit {
		t.Error("expected haveGit=true (marker present), the guardrail just doesn't fire")
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for non-github marker, got %v", matches)
	}
}

func TestEnumerateGitHubRemotes_MalformedMarkerFallsThroughToGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".niwa-snapshot.toml"), []byte("not toml at all !!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No git either, so haveGit should be false (fell through, found nothing).
	_, haveGit := EnumerateGitHubRemotes(dir)
	if haveGit {
		t.Error("malformed marker + no git should yield haveGit=false")
	}
}

func TestEnumerateGitHubRemotes_MissingMarkerFallsThroughToGit(t *testing.T) {
	dir := t.TempDir()
	// Empty dir, no marker, no git.
	_, haveGit := EnumerateGitHubRemotes(dir)
	if haveGit {
		t.Error("empty dir should yield haveGit=false")
	}
}
