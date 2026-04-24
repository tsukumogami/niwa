package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/github"
)

// fakeFetcher implements FetchClient with configurable responses for
// snapshotwriter tests.
type fakeFetcher struct {
	tarball   []byte
	commitOID string
	headErr   error
	fetchErr  error
	headCalls int
}

func (f *fakeFetcher) HeadCommit(ctx context.Context, owner, repo, ref, etag string) (string, string, int, error) {
	f.headCalls++
	if f.headErr != nil {
		return "", "", 0, f.headErr
	}
	return f.commitOID, "", 200, nil
}

func (f *fakeFetcher) FetchTarball(ctx context.Context, owner, repo, ref, etag string) (io.ReadCloser, string, int, *github.RenameRedirect, error) {
	if f.fetchErr != nil {
		return nil, "", 0, nil, f.fetchErr
	}
	return io.NopCloser(bytes.NewReader(f.tarball)), "", 200, nil, nil
}

// makeFakeTarball builds a gzipped tarball with the given entries.
// keys ending in "/" are dir entries; others are regular files.
func makeFakeTarball(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for name := range entries {
		if !strings.HasSuffix(name, "/") {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range entries {
		if strings.HasSuffix(name, "/") {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestEnsureConfigSnapshot_NoOpWhenNeitherMarkerNorGit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty fetcher should never be called.
	fetcher := &fakeFetcher{}
	if err := EnsureConfigSnapshot(context.Background(), dir, fetcher, nil); err != nil {
		t.Errorf("expected no-op, got %v", err)
	}
	if fetcher.headCalls != 0 {
		t.Errorf("fetcher should not be called for plain dir, headCalls = %d", fetcher.headCalls)
	}
}

func TestEnsureConfigSnapshot_NilFetcherIsSilent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant a marker so case 1 dispatches.
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "abc", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})
	if err := EnsureConfigSnapshot(context.Background(), dir, nil, nil); err != nil {
		t.Errorf("nil fetcher should silently no-op, got %v", err)
	}
}

func TestEnsureConfigSnapshot_SnapshotPathRefreshesOnDrift(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "old-oid", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})

	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "name = updated",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "new-oid"}

	if err := EnsureConfigSnapshot(context.Background(), dir, fetcher, nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// New content materialized.
	got, err := os.ReadFile(filepath.Join(dir, "workspace.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "name = updated" {
		t.Errorf("snapshot not refreshed: got %q", got)
	}
	// Marker reflects the new oid.
	prov, err := ReadProvenance(dir)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if prov.ResolvedCommit != "new-oid" {
		t.Errorf("marker oid: %q", prov.ResolvedCommit)
	}
}

func TestEnsureConfigSnapshot_NoDriftJustUpdatesFetchedAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "abc", FetchedAt: earlier, FetchMechanism: "github-tarball",
	})

	fetcher := &fakeFetcher{commitOID: "abc"}
	if err := EnsureConfigSnapshot(context.Background(), dir, fetcher, nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	prov, err := ReadProvenance(dir)
	if err != nil {
		t.Fatal(err)
	}
	if prov.FetchedAt.Equal(earlier) {
		t.Error("FetchedAt should be updated even without drift")
	}
	if prov.ResolvedCommit != "abc" {
		t.Errorf("commit should be unchanged: %q", prov.ResolvedCommit)
	}
}

func TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "cached-oid", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})
	if err := os.WriteFile(filepath.Join(dir, "workspace.toml"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	fetcher := &fakeFetcher{headErr: errors.New("network unreachable")}
	if err := EnsureConfigSnapshot(context.Background(), dir, fetcher, nil); err != nil {
		t.Errorf("network error should not propagate: %v", err)
	}
	// Cached snapshot still intact.
	got, _ := os.ReadFile(filepath.Join(dir, "workspace.toml"))
	if string(got) != "cached" {
		t.Errorf("cached snapshot lost: %q", got)
	}
}

func TestEnsureConfigSnapshot_PreservesPreviousOnExtractionFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "old-oid", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})
	if err := os.WriteFile(filepath.Join(dir, "workspace.toml"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A tarball that refers to a subpath we can't find: extraction returns
	// "subpath not found" which should leave the previous snapshot intact.
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                "",
		"wrap/some-other-file": "noise",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "new-oid"}

	// Add a subpath to the marker so extraction filter looks for it.
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo:.niwa", Owner: "org", Repo: "repo", Subpath: ".niwa",
		ResolvedCommit: "old-oid", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})

	err := EnsureConfigSnapshot(context.Background(), dir, fetcher, nil)
	if err == nil {
		t.Fatal("expected extraction error to propagate")
	}
	// Previous snapshot intact.
	got, _ := os.ReadFile(filepath.Join(dir, "workspace.toml"))
	if string(got) != "cached" {
		t.Errorf("previous snapshot was clobbered: %q", got)
	}
	// Staging cleaned up.
	if _, err := os.Stat(dir + ".next"); err == nil {
		t.Error("staging not cleaned up")
	}
}

// TestEnsureConfigSnapshot_PreservesInstanceStateAcrossRefresh asserts
// that instance.json survives a snapshot refresh. The snapshot writer's
// preserveInstanceState helper is the implementation; this test locks
// the contract in writing per the 2026-04-23 amendment to DESIGN
// Decision 2 (state stays in .niwa/, carried through swap).
func TestEnsureConfigSnapshot_PreservesInstanceStateAcrossRefresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "old-oid", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})

	// Plant a niwa-managed instance.json in the existing snapshot dir
	// alongside the marker. After refresh, the file MUST still be there
	// with byte-identical content.
	instanceData := []byte(`{"schema_version":3,"instance_name":"test","root":"/x"}`)
	if err := os.WriteFile(filepath.Join(dir, StateFile), instanceData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Upstream refresh brings new content. Crucially, the upstream tarball
	// does NOT contain instance.json — it's niwa-local state, not source
	// content. preserveInstanceState carries it across the swap.
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "name = updated",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "new-oid"}

	if err := EnsureConfigSnapshot(context.Background(), dir, fetcher, nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// Upstream content materialized at the new path.
	if got, err := os.ReadFile(filepath.Join(dir, "workspace.toml")); err != nil {
		t.Fatalf("workspace.toml missing after refresh: %v", err)
	} else if string(got) != "name = updated" {
		t.Errorf("upstream content not refreshed: got %q", got)
	}

	// instance.json survived.
	gotState, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		t.Fatalf("instance.json clobbered by snapshot swap: %v", err)
	}
	if !bytes.Equal(gotState, instanceData) {
		t.Errorf("instance.json content changed across refresh\n  was:  %s\n  now:  %s", instanceData, gotState)
	}
}

// TestEnsureConfigSnapshot_NoStateFileToPreserveIsBenign asserts that
// the carry-over is a no-op when no instance.json exists yet (fresh init,
// brand-new workspace).
func TestEnsureConfigSnapshot_NoStateFileToPreserveIsBenign(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".niwa")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteMarker(t, dir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "old-oid", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})
	// No instance.json planted.

	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "name = updated",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "new-oid"}
	if err := EnsureConfigSnapshot(context.Background(), dir, fetcher, nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// No instance.json should appear after refresh — the carry-over is a no-op.
	if _, err := os.Stat(filepath.Join(dir, StateFile)); err == nil {
		t.Error("instance.json appeared spuriously after refresh against empty state")
	}
}

func TestParseRemoteURLToSource(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want struct{ host, owner, repo string }
		err  bool
	}{
		{"https github", "https://github.com/org/repo.git", struct{ host, owner, repo string }{"", "org", "repo"}, false},
		{"https github no .git", "https://github.com/org/repo", struct{ host, owner, repo string }{"", "org", "repo"}, false},
		{"ssh github", "git@github.com:org/repo.git", struct{ host, owner, repo string }{"", "org", "repo"}, false},
		{"https gitlab", "https://gitlab.com/group/repo.git", struct{ host, owner, repo string }{"gitlab.com", "group", "repo"}, false},
		{"ssh gitlab", "git@gitlab.com:group/repo.git", struct{ host, owner, repo string }{"gitlab.com", "group", "repo"}, false},
		{"empty", "", struct{ host, owner, repo string }{}, true},
		{"no path", "https://github.com/", struct{ host, owner, repo string }{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRemoteURLToSource(tc.in)
			if tc.err {
				if err == nil {
					t.Errorf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Host != tc.want.host || got.Owner != tc.want.owner || got.Repo != tc.want.repo {
				t.Errorf("got host=%q owner=%q repo=%q, want %+v", got.Host, got.Owner, got.Repo, tc.want)
			}
		})
	}
}

func mustWriteMarker(t *testing.T, dir string, p Provenance) {
	t.Helper()
	if err := WriteProvenance(dir, p); err != nil {
		t.Fatal(err)
	}
}
