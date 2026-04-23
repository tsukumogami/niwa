package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/source"
)

func TestFetchSubpathViaGitClone_WholeRepo(t *testing.T) {
	tmp := t.TempDir()
	remote := overlaysyncMakeBareRepo(t, filepath.Join(tmp, "remote"), "workspace.toml", `name = "demo"`)
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	src, err := parseOverlaySlug(remote)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := FetchSubpathViaGitClone(context.Background(), src, staging)
	if err != nil {
		t.Fatalf("FetchSubpathViaGitClone: %v", err)
	}
	if oid == "" {
		t.Error("expected non-empty oid")
	}
	got, err := os.ReadFile(filepath.Join(staging, "workspace.toml"))
	if err != nil {
		t.Fatalf("workspace.toml not extracted: %v", err)
	}
	if !strings.Contains(string(got), `name = "demo"`) {
		t.Errorf("unexpected content: %s", got)
	}
	// `.git` metadata must not leak into the snapshot.
	if _, err := os.Stat(filepath.Join(staging, ".git")); err == nil {
		t.Error(".git directory leaked into staging")
	}
}

func TestFetchSubpathViaGitClone_Subpath(t *testing.T) {
	tmp := t.TempDir()
	remoteBare := filepath.Join(tmp, "remote")
	overlaysyncGitRun(t, "", "init", "--bare", "--initial-branch=main", remoteBare)

	work := filepath.Join(tmp, "work")
	overlaysyncGitRun(t, "", "clone", remoteBare, work)
	if err := os.MkdirAll(filepath.Join(work, "configs", "team"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "configs", "team", "workspace.toml"), []byte(`name = "team"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlaysyncGitRun(t, work, "config", "user.email", "test@test.com")
	overlaysyncGitRun(t, work, "config", "user.name", "Test")
	overlaysyncGitRun(t, work, "add", ".")
	overlaysyncGitRun(t, work, "commit", "-m", "init")
	overlaysyncGitRun(t, work, "push", "origin", "main")

	src, err := parseOverlaySlug("file://" + remoteBare)
	if err != nil {
		t.Fatal(err)
	}
	src.Subpath = "configs/team"

	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchSubpathViaGitClone(context.Background(), src, staging); err != nil {
		t.Fatalf("FetchSubpathViaGitClone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "workspace.toml")); err != nil {
		t.Errorf("expected workspace.toml at staging root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "README.md")); err == nil {
		t.Error("README.md from outside subpath leaked into staging")
	}
}

func TestFetchSubpathViaGitClone_RejectsMissingSubpath(t *testing.T) {
	tmp := t.TempDir()
	remote := overlaysyncMakeBareRepo(t, filepath.Join(tmp, "remote"), "workspace.toml", `name = "demo"`)
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := parseOverlaySlug(remote)
	if err != nil {
		t.Fatal(err)
	}
	src.Subpath = "does/not/exist"
	if _, err := FetchSubpathViaGitClone(context.Background(), src, staging); err == nil {
		t.Error("expected error for missing subpath, got nil")
	}
}

func TestFetchSubpathViaGitClone_SkipsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	remoteBare := filepath.Join(tmp, "remote")
	overlaysyncGitRun(t, "", "init", "--bare", "--initial-branch=main", remoteBare)

	work := filepath.Join(tmp, "work")
	overlaysyncGitRun(t, "", "clone", remoteBare, work)
	if err := os.WriteFile(filepath.Join(work, "regular.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("regular.txt", filepath.Join(work, "evil-link")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	overlaysyncGitRun(t, work, "config", "user.email", "test@test.com")
	overlaysyncGitRun(t, work, "config", "user.name", "Test")
	overlaysyncGitRun(t, work, "add", ".")
	overlaysyncGitRun(t, work, "commit", "-m", "init")
	overlaysyncGitRun(t, work, "push", "origin", "main")

	src, err := parseOverlaySlug("file://" + remoteBare)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchSubpathViaGitClone(context.Background(), src, staging); err != nil {
		t.Fatalf("FetchSubpathViaGitClone: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(staging, "evil-link")); err == nil {
		t.Error("symlink leaked into staging — should be skipped per security policy")
	}
	if _, err := os.Stat(filepath.Join(staging, "regular.txt")); err != nil {
		t.Errorf("regular file should be copied: %v", err)
	}
}

func TestValidateRelName_RejectsTraversal(t *testing.T) {
	cases := []string{"..", "\x00bad", "back\\slash", ""}
	for _, name := range cases {
		if err := validateRelName(name); err == nil {
			t.Errorf("validateRelName(%q) = nil, want error", name)
		}
	}
}

func TestSplitLocalPath(t *testing.T) {
	cases := []struct {
		path      string
		wantOwner string
		wantRepo  string
	}{
		{"/tmp/parent/repo.git", "parent", "repo"},
		{"/tmp/repo", "tmp", "repo"},
		{"/repo", "local", "repo"},
		{"/", "local", "local"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			owner, repo := splitLocalPath(tc.path)
			if owner != tc.wantOwner || repo != tc.wantRepo {
				t.Errorf("splitLocalPath(%q) = (%q, %q), want (%q, %q)",
					tc.path, owner, repo, tc.wantOwner, tc.wantRepo)
			}
		})
	}
}

// Compile-time check that source.Source satisfies the fallback contract.
var _ = source.Source{}
