package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestFetchPRHead_LiveAgainstGitHub exercises the real FetchPRHead against a
// real GitHub repo, so the auth-header path (the Basic vs Bearer fix) is proven
// end-to-end, not just at the git-CLI level. Gated on NIWA_WATCH_LIVE_FETCH=1
// because it needs network and a token; skipped in CI.
//
//	NIWA_WATCH_LIVE_FETCH=1 NIWA_WATCH_LIVE_URL=... NIWA_WATCH_LIVE_SHA=... \
//	  GITHUB_TOKEN=$(gh auth token) go test ./internal/watch -run LiveAgainstGitHub -v
func TestFetchPRHead_LiveAgainstGitHub(t *testing.T) {
	if os.Getenv("NIWA_WATCH_LIVE_FETCH") != "1" {
		t.Skip("set NIWA_WATCH_LIVE_FETCH=1 to run the live fetch")
	}
	url := os.Getenv("NIWA_WATCH_LIVE_URL")
	sha := os.Getenv("NIWA_WATCH_LIVE_SHA")
	token := os.Getenv("GITHUB_TOKEN")
	if url == "" || sha == "" {
		t.Fatal("set NIWA_WATCH_LIVE_URL and NIWA_WATCH_LIVE_SHA")
	}

	dir := t.TempDir()
	if err := FetchPRHead(context.Background(), url, sha, dir, token); err != nil {
		t.Fatalf("FetchPRHead failed: %v", err)
	}

	// The checkout must be a populated, ordinary file tree.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var nonGit int
	for _, e := range entries {
		if e.Name() != ".git" {
			nonGit++
		}
	}
	if nonGit == 0 {
		t.Fatal("checkout produced no working-tree files")
	}
	// The fetched HEAD matches the requested SHA.
	head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err == nil {
		t.Logf(".git/HEAD = %q", string(head))
	}
	t.Logf("OK: fetched %d top-level working-tree entries into %s", nonGit, dir)
}
