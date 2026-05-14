package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
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

// ----- ProbeAndFetchSubpath + probeAndResolveCloneRoot (Issue 3) -----

func TestProbeAndFetchSubpath_Rank1Resolves(t *testing.T) {
	tmp := t.TempDir()
	remoteBare := filepath.Join(tmp, "remote")
	overlaysyncGitRun(t, "", "init", "--bare", "--initial-branch=main", remoteBare)

	work := filepath.Join(tmp, "work")
	overlaysyncGitRun(t, "", "clone", remoteBare, work)
	if err := os.MkdirAll(filepath.Join(work, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".niwa", "workspace.toml"), []byte(`name = "rank1"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("outside subpath"), 0o644); err != nil {
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
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	subpath, rank, notice, oid, err := ProbeAndFetchSubpath(
		context.Background(), src, config.TeamConfigMarkerSet(), config.RankDecider, staging)
	if err != nil {
		t.Fatalf("probe-and-fetch: %v", err)
	}
	if subpath != ".niwa" {
		t.Errorf("subpath = %q, want %q", subpath, ".niwa")
	}
	if rank != 1 {
		t.Errorf("rank = %d, want 1", rank)
	}
	if notice != nil {
		t.Errorf("rank-1 must not emit a notice, got %+v", notice)
	}
	if oid == "" {
		t.Error("expected non-empty oid")
	}
	// Only the .niwa subpath landed.
	if _, err := os.Stat(filepath.Join(staging, "workspace.toml")); err != nil {
		t.Errorf("expected workspace.toml in staging: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md must not land in staging (outside subpath); err=%v", err)
	}
}

func TestProbeAndFetchSubpath_Rank2Resolves(t *testing.T) {
	tmp := t.TempDir()
	remote := overlaysyncMakeBareRepo(t, filepath.Join(tmp, "remote"), "workspace.toml", `name = "rank2"`)
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	src, err := parseOverlaySlug(remote)
	if err != nil {
		t.Fatal(err)
	}
	subpath, rank, notice, oid, err := ProbeAndFetchSubpath(
		context.Background(), src, config.TeamConfigMarkerSet(), config.RankDecider, staging)
	if err != nil {
		t.Fatalf("probe-and-fetch: %v", err)
	}
	if subpath != "" {
		t.Errorf("subpath = %q, want empty (whole-repo rank-2)", subpath)
	}
	if rank != 2 {
		t.Errorf("rank = %d, want 2", rank)
	}
	if notice == nil {
		t.Fatal("rank-2 must emit a deprecation notice")
	}
	if oid == "" {
		t.Error("expected non-empty oid")
	}
	if _, err := os.Stat(filepath.Join(staging, "workspace.toml")); err != nil {
		t.Errorf("workspace.toml missing from staging: %v", err)
	}
}

func TestProbeAndFetchSubpath_BothRanksAmbiguous(t *testing.T) {
	// PRD R3 / AC-D5: source with BOTH .niwa/workspace.toml AND root
	// workspace.toml is ambiguous. Staging stays empty.
	tmp := t.TempDir()
	remoteBare := filepath.Join(tmp, "remote")
	overlaysyncGitRun(t, "", "init", "--bare", "--initial-branch=main", remoteBare)

	work := filepath.Join(tmp, "work")
	overlaysyncGitRun(t, "", "clone", remoteBare, work)
	if err := os.MkdirAll(filepath.Join(work, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".niwa", "workspace.toml"), []byte(`name = "a"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "workspace.toml"), []byte(`name = "b"`), 0o644); err != nil {
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
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	subpath, rank, notice, _, err := ProbeAndFetchSubpath(
		context.Background(), src, config.TeamConfigMarkerSet(), config.RankDecider, staging)
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !config.IsAmbiguousMarkers(err) {
		t.Errorf("expected *AmbiguousMarkersError, got %T: %v", err, err)
	}
	if subpath != "" || rank != 0 || notice != nil {
		t.Errorf("on error want (\"\", 0, nil); got (%q, %d, %+v)", subpath, rank, notice)
	}
	// Staging stays empty (R5).
	dirents, _ := os.ReadDir(staging)
	if len(dirents) != 0 {
		t.Errorf("staging must be empty on ambiguity; got %d entries", len(dirents))
	}
}

func TestProbeAndFetchSubpath_NoMarkerError(t *testing.T) {
	tmp := t.TempDir()
	remote := overlaysyncMakeBareRepo(t, filepath.Join(tmp, "remote"), "README.md", "no niwa config here")
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	src, err := parseOverlaySlug(remote)
	if err != nil {
		t.Fatal(err)
	}
	subpath, rank, notice, _, err := ProbeAndFetchSubpath(
		context.Background(), src, config.TeamConfigMarkerSet(), config.RankDecider, staging)
	if err == nil {
		t.Fatal("expected no-marker error, got nil")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("expected *NoMarkerError, got %T: %v", err, err)
	}
	if subpath != "" || rank != 0 || notice != nil {
		t.Errorf("on error want (\"\", 0, nil); got (%q, %d, %+v)", subpath, rank, notice)
	}
	dirents, _ := os.ReadDir(staging)
	if len(dirents) != 0 {
		t.Errorf("staging must be empty on no-marker; got %d entries", len(dirents))
	}
}

func TestProbeAndResolveCloneRoot_EmptyNiwaDirectory(t *testing.T) {
	// PRD R6: a .niwa/ directory at source root that does NOT contain
	// workspace.toml MUST NOT count as rank-1. With a root workspace.toml
	// also present, discovery falls through to rank-2.
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	// .niwa/ exists but does NOT contain workspace.toml.
	if err := os.WriteFile(filepath.Join(tmp, "workspace.toml"), []byte(`name = "rank2"`), 0o644); err != nil {
		t.Fatal(err)
	}

	subpath, notice, err := probeAndResolveCloneRoot(tmp, config.TeamConfigMarkerSet(), config.RankDecider)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if subpath != "" {
		t.Errorf("subpath = %q, want empty (rank-2 resolved)", subpath)
	}
	if notice == nil {
		t.Fatal("rank-2 must emit a notice")
	}
}

func TestProbeAndResolveCloneRoot_SymlinkMarkerIsNotRank1(t *testing.T) {
	// Symmetric guard with TestProbeAndExtract_SymlinkMarkerIsNotRank1
	// in internal/github/tar_test.go. A symlink whose path matches the
	// rank-1 marker MUST NOT be detected as rank-1.
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a real workspace.toml elsewhere as the link target, then
	// symlink it into the rank-1 marker slot.
	target := filepath.Join(tmp, "other.toml")
	if err := os.WriteFile(target, []byte(`name = "linked"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(tmp, ".niwa", "workspace.toml")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	subpath, notice, err := probeAndResolveCloneRoot(tmp, config.TeamConfigMarkerSet(), config.RankDecider)
	// Symlink rejected by Mode().IsRegular() guard → probe sees no
	// markers → decider returns NoMarkerError.
	if err == nil {
		t.Fatal("expected no-marker error (symlink must not satisfy rank-1)")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("expected *NoMarkerError, got %T: %v", err, err)
	}
	if subpath != "" || notice != nil {
		t.Errorf("on error want (\"\", nil); got (%q, %+v)", subpath, notice)
	}
}

func TestProbeAndResolveCloneRoot_OverlayMarkers(t *testing.T) {
	// OverlayMarkerSet() probes for workspace-overlay.toml instead of
	// workspace.toml. Verify the probe parameterisation works end-to-end.
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".niwa", "workspace-overlay.toml"), []byte(`name = "overlay"`), 0o644); err != nil {
		t.Fatal(err)
	}

	subpath, notice, err := probeAndResolveCloneRoot(tmp, config.OverlayMarkerSet(), config.RankDecider)
	if err != nil {
		t.Fatalf("overlay rank-1 probe: %v", err)
	}
	if subpath != ".niwa" {
		t.Errorf("subpath = %q, want %q", subpath, ".niwa")
	}
	if notice != nil {
		t.Errorf("overlay rank-1 must not emit a notice; got %+v", notice)
	}
}
