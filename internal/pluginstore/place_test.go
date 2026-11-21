package pluginstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	testCommit  = "3f9a2c1e0b1d2c3e4f5a6b7c8d9e0f1a2b3c4d5e"
	otherCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

var testCommitTime = time.Date(2026, 11, 2, 8, 41, 10, 0, time.UTC)

// archiveFor builds a GitHub-shaped tarball for commit: one wrapper
// directory, a manifest, a skill and an executable script, every entry
// stamped with testCommitTime.
func archiveFor(t *testing.T, wrapperCommit string) []byte {
	t.Helper()
	wrapper := "Acme-skills-" + wrapperCommit[:7]
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name string
		mode int64
		body string
	}{
		{wrapper + "/", 0o755, ""},
		{wrapper + "/.claude-plugin/", 0o755, ""},
		{wrapper + "/.claude-plugin/marketplace.json", 0o644, `{"name":"skills","plugins":[{"name":"demo","source":"./plugins/demo"}]}`},
		{wrapper + "/plugins/demo/skills/greet/SKILL.md", 0o644, "---\nname: greet\n---\n"},
		{wrapper + "/plugins/demo/scripts/run.sh", 0o755, "#!/bin/sh\n"},
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, ModTime: testCommitTime, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
		if strings.HasSuffix(e.name, "/") {
			hdr.Typeflag, hdr.Size = tar.TypeDir, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
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

// fixture is one workspace root with an instance and a sandbox home.
type fixture struct {
	home, root, instance string
	mu                   sync.Mutex
	fetches              []string
	archive              []byte
	fetchErr             error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		home:     filepath.Join(base, "home"),
		root:     filepath.Join(base, "ws"),
		instance: filepath.Join(base, "ws", "inst"),
		archive:  archiveFor(t, testCommit),
	}
	for _, d := range []string{f.home, filepath.Join(f.instance, ".niwa", "marketplaces")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The held tree is read-only, which t.TempDir's cleanup can't remove.
	t.Cleanup(func() { restoreWrite(base) })
	return f
}

func restoreWrite(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
}

func (f *fixture) store() Store {
	return Store{Home: f.home, WorkspaceRoot: f.root, LockWait: 10 * time.Second, Fetch: f.fetch}
}

func (f *fixture) fetch(_ context.Context, owner, repo, commit string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.fetches = append(f.fetches, owner+"/"+repo+"@"+commit)
	f.mu.Unlock()
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return io.NopCloser(bytes.NewReader(f.archive)), nil
}

func (f *fixture) link() string {
	return filepath.Join(f.instance, ".niwa", "marketplaces", "skills")
}

func testRelease(t *testing.T) Release {
	t.Helper()
	r, ok := NewRelease("Acme", "Skills", testCommit, "v1.4.0", "v1.4.0")
	if !ok {
		t.Fatal("release not valid")
	}
	return r
}

func TestEnsurePlacesOnceAndLinks(t *testing.T) {
	f := newFixture(t)
	r := testRelease(t)
	s := f.store()

	restored, cond, err := s.Ensure(context.Background(), r, f.link(), nil, nil)
	if err != nil || cond != CondNone || !restored {
		t.Fatalf("first Ensure = %v, %q, %v", restored, cond, err)
	}
	restored, cond, err = s.Ensure(context.Background(), r, f.link(), nil, nil)
	if err != nil || cond != CondNone || restored {
		t.Fatalf("second Ensure = %v, %q, %v", restored, cond, err)
	}
	if len(f.fetches) != 1 || f.fetches[0] != "acme/skills@"+testCommit {
		t.Fatalf("fetches = %v, want one at the commit", f.fetches)
	}

	target, err := os.Readlink(f.link())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.home, ".niwa", "w-"+keyOf(f.root), "plugins", "github.com", "acme", "skills", testCommit, "tree")
	if target != want {
		t.Fatalf("link target = %s, want %s", target, want)
	}
	if got, ok := ParseLink(target); !ok || got.ID() != r.ID() {
		t.Fatalf("ParseLink(%s) = %v, %v", target, got, ok)
	}
	if _, err := os.Stat(filepath.Join(f.link(), "plugins", "demo", "skills", "greet", "SKILL.md")); err != nil {
		t.Fatalf("skill not readable through the link: %v", err)
	}
	if held, _ := s.Held(r); !held {
		t.Fatal("release not held")
	}
}

func keyOf(root string) string {
	k := Dir("/h", root)
	return strings.TrimPrefix(filepath.Base(filepath.Dir(k)), "w-")
}

func TestPlacedReleaseIsReadOnlyAndMarked(t *testing.T) {
	f := newFixture(t)
	r := testRelease(t)
	s := f.store()
	if _, cond, err := s.Ensure(context.Background(), r, f.link(), nil, nil); err != nil || cond != CondNone {
		t.Fatalf("Ensure: %q %v", cond, err)
	}
	rdir := releaseDir(Dir(f.home, f.root), r)
	tree := filepath.Join(rdir, "tree")
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, _ := os.Lstat(p)
		perm := info.Mode().Perm()
		switch {
		case info.IsDir() && perm != 0o555:
			t.Errorf("%s: dir mode %o, want 555", p, perm)
		case info.Mode().IsRegular() && strings.HasSuffix(p, "run.sh") && perm != 0o555:
			t.Errorf("%s: mode %o, want 555", p, perm)
		case info.Mode().IsRegular() && !strings.HasSuffix(p, "run.sh") && perm != 0o444:
			t.Errorf("%s: mode %o, want 444", p, perm)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	markInfo, err := os.Lstat(filepath.Join(rdir, "held.json"))
	if err != nil || markInfo.Mode().Perm() != 0o444 {
		t.Fatalf("held.json: %v, mode %v", err, markInfo.Mode())
	}
	m, err := s.ReadMark(r)
	if err != nil {
		t.Fatal(err)
	}
	if m.Source != "acme/skills" || m.Ref != "v1.4.0" || m.Tag != "v1.4.0" || m.Commit != testCommit {
		t.Fatalf("mark = %+v", m)
	}
	if m.CommitTime != testCommitTime.Format(time.RFC3339) {
		t.Fatalf("commit_time = %s, want %s", m.CommitTime, testCommitTime.Format(time.RFC3339))
	}
	if m.Seal.Entries == 0 || m.Seal.Bytes == 0 || m.Seal.MaxCtimeNS == 0 {
		t.Fatalf("seal = %+v", m.Seal)
	}
	pdir := Dir(f.home, f.root)
	for _, dir := range []string{pdir, filepath.Join(pdir, "github.com"), filepath.Join(pdir, "github.com", "acme"), filepath.Join(pdir, "github.com", "acme", "skills"), rdir} {
		info, err := os.Lstat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, mode %v, want 0700", dir, err, info.Mode().Perm())
		}
	}
	// Nothing is left staged.
	if entries, _ := os.ReadDir(filepath.Join(pdir, ".tmp")); len(entries) != 0 {
		t.Fatalf(".tmp holds %d entries", len(entries))
	}
}

func TestEnsureRefusesAWrapperNamingAnotherCommit(t *testing.T) {
	f := newFixture(t)
	f.archive = archiveFor(t, otherCommit)
	r := testRelease(t)
	_, cond, err := f.store().Ensure(context.Background(), r, f.link(), nil, nil)
	if err == nil || cond != CondNotPlaced {
		t.Fatalf("Ensure = %q, %v; want a refusal", cond, err)
	}
	if held, _ := f.store().Held(r); held {
		t.Fatal("a mismatched archive was held")
	}
	if _, err := os.Lstat(f.link()); !os.IsNotExist(err) {
		t.Fatalf("link placed for an unheld release: %v", err)
	}
	requireNoReleaseDir(t, f, r)
}

// requireNoReleaseDir fails when anything exists at r's release path: a
// refused placement leaves no <commit> directory, not only an unheld one.
func requireNoReleaseDir(t *testing.T, f *fixture, r Release) {
	t.Helper()
	dir := releaseDir(Dir(f.home, f.root), r)
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("%s exists after a refused placement (%v)", dir, err)
	}
}

// archiveWithoutManifest is archiveFor's tarball for commit with no
// .claude-plugin/marketplace.json in it.
func archiveWithoutManifest(t *testing.T, commit string) []byte {
	t.Helper()
	wrapper := "Acme-skills-" + commit[:7]
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	body := "---\nname: greet\n---\n"
	for _, hdr := range []*tar.Header{
		{Name: wrapper + "/", Mode: 0o755, ModTime: testCommitTime, Typeflag: tar.TypeDir},
		{Name: wrapper + "/plugins/demo/skills/greet/SKILL.md", Mode: 0o644, ModTime: testCommitTime, Typeflag: tar.TypeReg, Size: int64(len(body))},
	} {
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
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

func TestEnsureRefusesAnArchiveWithNoManifest(t *testing.T) {
	f := newFixture(t)
	f.archive = archiveWithoutManifest(t, testCommit)
	r := testRelease(t)
	_, cond, err := f.store().Ensure(context.Background(), r, f.link(), nil, nil)
	if err == nil || cond != CondNotPlaced {
		t.Fatalf("Ensure = %q, %v; want a refusal", cond, err)
	}
	if held, _ := f.store().Held(r); held {
		t.Fatal("an archive with no manifest was held")
	}
	if _, err := os.Lstat(f.link()); !os.IsNotExist(err) {
		t.Fatalf("link placed for an unheld release: %v", err)
	}
	requireNoReleaseDir(t, f, r)
}

func TestEnsureReportsAFailedDownloadWithoutACondition(t *testing.T) {
	f := newFixture(t)
	f.fetchErr = errors.New("source down")
	_, cond, err := f.store().Ensure(context.Background(), testRelease(t), f.link(), nil, nil)
	if err == nil || cond != CondNone {
		t.Fatalf("Ensure = %q, %v; want a plain error", cond, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(Dir(f.home, f.root), ".tmp")); len(entries) != 0 {
		t.Fatalf("staging left behind: %d entries", len(entries))
	}
}

// A release taken from a link is fetched only for a declared source whose
// commit Reachable accepts; otherwise nothing is fetched and the link stays.
func TestEnsureRefusesALinkedReleaseItMayNotFetch(t *testing.T) {
	r := testRelease(t)
	cases := []struct {
		name      string
		declared  map[string]bool
		reachable Reachable
	}{
		{"undeclared source", map[string]bool{"acme/other": true}, func(context.Context, string, string, string) (bool, error) { return true, nil }},
		{"unreachable commit", map[string]bool{"acme/skills": true}, func(context.Context, string, string, string) (bool, error) { return false, nil }},
		{"reachability unknown", map[string]bool{"acme/skills": true}, func(context.Context, string, string, string) (bool, error) {
			return false, errors.New("compare failed")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			old := "/elsewhere/.niwa/w-0/plugins/github.com/acme/skills/" + testCommit + "/tree"
			if err := os.Symlink(old, f.link()); err != nil {
				t.Fatal(err)
			}
			_, _, err := f.store().Ensure(context.Background(), r, f.link(), tc.declared, tc.reachable)
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("Ensure error = %v, want ErrRefused", err)
			}
			if len(f.fetches) != 0 {
				t.Fatalf("fetched %v", f.fetches)
			}
			if got, _ := os.Readlink(f.link()); got != old {
				t.Fatalf("link changed to %s", got)
			}
		})
	}
}

func TestEnsureRelinksAHeldReleaseWithoutChecks(t *testing.T) {
	f := newFixture(t)
	r := testRelease(t)
	s := f.store()
	if _, _, err := s.Ensure(context.Background(), r, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	old := "/elsewhere/.niwa/w-0/plugins/github.com/acme/skills/" + testCommit + "/tree"
	if err := os.Symlink(old, f.link()); err != nil {
		t.Fatal(err)
	}
	refuse := func(context.Context, string, string, string) (bool, error) { return false, nil }
	if _, _, err := s.Ensure(context.Background(), r, f.link(), nil, refuse); err != nil {
		t.Fatalf("Ensure of a held release: %v", err)
	}
	if got, _ := os.Readlink(f.link()); got != TreePath(f.home, f.root, r) {
		t.Fatalf("link = %s, want this store's tree", got)
	}
	if len(f.fetches) != 1 {
		t.Fatalf("fetches = %v", f.fetches)
	}
}

func TestEnsureReplacesAnUnmarkedReleaseDirectory(t *testing.T) {
	f := newFixture(t)
	r := testRelease(t)
	rdir := releaseDir(Dir(f.home, f.root), r)
	if err := os.MkdirAll(filepath.Join(rdir, "tree"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, cond, err := f.store().Ensure(context.Background(), r, f.link(), nil, nil); err != nil || cond != CondNone {
		t.Fatalf("Ensure: %q %v", cond, err)
	}
	if held, _ := f.store().Held(r); !held {
		t.Fatal("not held after replacing the unmarked directory")
	}
}

func TestEnsureNeverReplacesARealDirectoryAtTheLink(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.link(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store().Ensure(context.Background(), testRelease(t), f.link(), nil, nil); err == nil {
		t.Fatal("Ensure replaced a real directory")
	}
	if info, err := os.Lstat(f.link()); err != nil || !info.IsDir() {
		t.Fatalf("the real directory is gone: %v", err)
	}
}

// Ten callers, each linking its own instance, race on one unheld release:
// one fetch, and every link points at the one held tree.
func TestConcurrentEnsureFetchesOnce(t *testing.T) {
	f := newFixture(t)
	r := testRelease(t)
	links := make([]string, 10)
	for i := range links {
		dir := filepath.Join(f.root, fmt.Sprintf("inst-%d", i), ".niwa", "marketplaces")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		links[i] = filepath.Join(dir, "skills")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for _, link := range links {
		wg.Add(1)
		go func(link string) {
			defer wg.Done()
			if _, _, err := f.store().Ensure(context.Background(), r, link, nil, nil); err != nil {
				errs <- err
			}
		}(link)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(f.fetches) != 1 {
		t.Fatalf("fetches = %d, want 1", len(f.fetches))
	}
	if held, cond := f.store().Held(r); !held || cond != CondNone {
		t.Fatalf("Held = %v, %q after the race", held, cond)
	}
	want := TreePath(f.home, f.root, r)
	for _, link := range links {
		if target, err := os.Readlink(link); err != nil || target != want {
			t.Fatalf("%s -> %q (%v), want %s", link, target, err, want)
		}
	}
}

func TestHostileRefIsStoredEmpty(t *testing.T) {
	f := newFixture(t)
	r, _ := NewRelease("acme", "skills", testCommit, "v1\x1b[31m", "../../etc")
	if _, _, err := f.store().Ensure(context.Background(), r, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	m, err := f.store().ReadMark(r)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	if m.Ref != "" || m.Tag != "" {
		t.Fatalf("mark kept a hostile ref: %s", data)
	}
	if !strings.HasSuffix(r.Label(), testCommit[:12]) {
		t.Fatalf("label = %q, want the short commit", r.Label())
	}
}

func TestParseLink(t *testing.T) {
	good := "/home/u/.niwa/w-abc/plugins/github.com/acme/skills/" + testCommit + "/tree"
	if r, ok := ParseLink(good); !ok || r.Source() != "acme/skills" || r.Commit != testCommit {
		t.Fatalf("ParseLink(good) = %v, %v", r, ok)
	}
	for _, bad := range []string{
		"relative/plugins/github.com/acme/skills/" + testCommit + "/tree",
		"/h/.niwa/w-abc/plugins/github.com/acme/skills/" + testCommit,
		"/h/.niwa/w-abc/plugins/github.com/acme/skills/" + testCommit + "/tree/",
		"/h/.niwa/w-abc/plugins/gitlab.com/acme/skills/" + testCommit + "/tree",
		"/h/.niwa/w-abc/plugins/github.com/Acme/skills/" + testCommit + "/tree",
		"/h/.niwa/w-abc/plugins/github.com/acme/../" + testCommit + "/tree",
		"/h/.niwa/w-abc/plugins/github.com/acme/skills/notacommit/tree",
		"/h/.niwa/w-abc/other/github.com/acme/skills/" + testCommit + "/tree",
	} {
		if _, ok := ParseLink(bad); ok {
			t.Errorf("ParseLink(%q) accepted", bad)
		}
	}
}

func TestReachable(t *testing.T) {
	tips := func(context.Context, string, string) (map[string]string, error) {
		return map[string]string{
			"HEAD":             "1111111111111111111111111111111111111111",
			"refs/heads/main":  "1111111111111111111111111111111111111111",
			"refs/tags/v1.0.0": "2222222222222222222222222222222222222222",
			"refs/tags/v1.2.0": "3333333333333333333333333333333333333333",
		}, nil
	}
	var compared []string
	behind := func(_ context.Context, _, _, base, commit string) (bool, error) {
		compared = append(compared, base)
		return commit == testCommit && base == "3333333333333333333333333333333333333333", nil
	}
	reach := NewReachable(tips, behind)
	if ok, err := reach(context.Background(), "acme", "skills", "2222222222222222222222222222222222222222"); !ok || err != nil {
		t.Fatalf("a tag tip was refused: %v", err)
	}
	if len(compared) != 0 {
		t.Fatalf("a tip match made compare calls: %v", compared)
	}
	if ok, _ := reach(context.Background(), "acme", "skills", testCommit); !ok {
		t.Fatal("an ancestor of the newest tag was refused")
	}
	if compared[0] != "1111111111111111111111111111111111111111" || compared[1] != "3333333333333333333333333333333333333333" {
		t.Fatalf("compare order = %v, want the default branch then the newest tag", compared)
	}
	if ok, _ := reach(context.Background(), "acme", "skills", otherCommit); ok {
		t.Fatal("a commit behind no ref passed")
	}
}

// placedRelease places testRelease and returns its fixture, store, release
// and tree path.
func placedRelease(t *testing.T) (*fixture, Store, Release, string) {
	t.Helper()
	f := newFixture(t)
	r := testRelease(t)
	s := f.store()
	if _, cond, err := s.Ensure(context.Background(), r, "", nil, nil); err != nil || cond != CondNone {
		t.Fatalf("Ensure: %q %v", cond, err)
	}
	return f, s, r, TreePath(f.home, f.root, r)
}

// withWrite gives dir its owner write bit for fn and takes it away after.
func withWrite(t *testing.T, dir string, fn func()) {
	t.Helper()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fn()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
}

func requireVerify(t *testing.T, s Store, r Release, want bool) {
	t.Helper()
	ok, err := s.Verify(r)
	if ok != want {
		t.Fatalf("Verify = %v (%v), want %v", ok, err, want)
	}
}

// Verify passes on a tree just placed, and on the same tree read by a store
// made afresh, as a later command would.
func TestVerifyPassesOnAPlacedTree(t *testing.T) {
	f, s, r, _ := placedRelease(t)
	requireVerify(t, s, r, true)
	requireVerify(t, Store{Home: f.home, WorkspaceRoot: f.root}, r, true)
}

// Each change to a placed tree fails Verify.
func TestVerifyFailsOnAChangedTree(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, tree string)
	}{
		{"changed size", func(t *testing.T, tree string) {
			file := filepath.Join(tree, "plugins", "demo", "skills", "greet", "SKILL.md")
			if err := os.Chmod(file, 0o644); err != nil {
				t.Fatal(err)
			}
			fh, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fh.WriteString("extra\n"); err != nil {
				t.Fatal(err)
			}
			_ = fh.Close()
			if err := os.Chmod(file, 0o444); err != nil {
				t.Fatal(err)
			}
		}},
		{"added entry", func(t *testing.T, tree string) {
			dir := filepath.Join(tree, "plugins", "demo")
			withWrite(t, dir, func() {
				if err := os.WriteFile(filepath.Join(dir, "added.md"), nil, 0o444); err != nil {
					t.Fatal(err)
				}
			})
		}},
		{"removed entry", func(t *testing.T, tree string) {
			dir := filepath.Join(tree, "plugins", "demo", "scripts")
			withWrite(t, dir, func() {
				if err := os.Remove(filepath.Join(dir, "run.sh")); err != nil {
					t.Fatal(err)
				}
			})
		}},
		{"write bit on a file", func(t *testing.T, tree string) {
			if err := os.Chmod(filepath.Join(tree, ".claude-plugin", "marketplace.json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"write bit on a directory", func(t *testing.T, tree string) {
			if err := os.Chmod(filepath.Join(tree, "plugins"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, s, r, tree := placedRelease(t)
			tc.change(t, tree)
			requireVerify(t, s, r, false)
			if held, _ := s.Held(r); !held {
				t.Fatal("Held reads the seal: a changed tree is no longer held")
			}
		})
	}
}

// A chmod put back leaves the count, the size and every mode as they were,
// and only the ctime newer than the seal. Linux stamps ctime from a coarse
// clock, so the chmod is repeated until the ctime has actually moved past the
// sealed one rather than assumed to.
func TestVerifyFailsOnACtimeNewerThanTheSeal(t *testing.T) {
	_, s, r, tree := placedRelease(t)
	m, err := s.ReadMark(r)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tree, ".claude-plugin", "marketplace.json")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := os.Chmod(file, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, 0o444); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		if ctimeNS(info.Sys().(*syscall.Stat_t)) > m.Seal.MaxCtimeNS {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ctime never moved past the seal")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, err := measure(tree)
	if err != nil {
		t.Fatal(err)
	}
	if got.writable || got.seal.Entries != m.Seal.Entries || got.seal.Bytes != m.Seal.Bytes {
		t.Fatalf("chmod and back changed more than the ctime: %+v, seal %+v", got, m.Seal)
	}
	requireVerify(t, s, r, false)
}

// A symlink in a read-only tree reports 0777 by Lstat and still passes as
// read-only; the walk never follows it.
func TestMeasurePassesOverSymlinkModes(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "tree")
	t.Cleanup(func() { restoreWrite(tree) })
	if err := os.MkdirAll(filepath.Join(tree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "sub", "file"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/file", filepath.Join(tree, "link")); err != nil {
		t.Fatal(err)
	}
	if err := makeReadOnly(tree); err != nil {
		t.Fatal(err)
	}
	m, err := measure(tree)
	if err != nil {
		t.Fatal(err)
	}
	if m.writable {
		t.Fatal("a symlink's mode counted as a write bit")
	}
	if m.seal.Entries != 4 || m.seal.Bytes != 3 {
		t.Fatalf("measure = %+v, want 4 entries and 3 bytes", m.seal)
	}
	seal, err := TakeSeal(tree)
	if err != nil || seal != m.seal {
		t.Fatalf("TakeSeal = %+v (%v), want %+v", seal, err, m.seal)
	}
}

// Ensure on a held release from a link whose tree was changed fetches it
// once, under the same checks as a missing one, and puts back a read-only,
// marked tree with the tarball's content; a second Ensure fetches nothing.
func TestEnsureRefetchesATamperedRelease(t *testing.T) {
	f, s, r, tree := placedRelease(t)
	file := filepath.Join(tree, "plugins", "demo", "skills", "greet", "SKILL.md")
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.fetches = nil

	var checked []string
	reach := func(_ context.Context, owner, repo, commit string) (bool, error) {
		checked = append(checked, owner+"/"+repo+"@"+commit)
		return true, nil
	}
	declared := map[string]bool{"acme/skills": true}
	restored, cond, err := s.Ensure(context.Background(), r, f.link(), declared, reach)
	if err != nil || cond != CondNone || !restored {
		t.Fatalf("Ensure = %v, %q, %v; want a restore", restored, cond, err)
	}
	if len(f.fetches) != 1 || len(checked) != 1 {
		t.Fatalf("fetches = %v, reachability checks = %v; want one each", f.fetches, checked)
	}
	requireVerify(t, s, r, true)
	if held, _ := s.Held(r); !held {
		t.Fatal("not held after the refetch")
	}
	if got, err := os.ReadFile(filepath.Join(f.link(), "plugins", "demo", "skills", "greet", "SKILL.md")); err != nil || string(got) != "---\nname: greet\n---\n" {
		t.Fatalf("SKILL.md = %q (%v), want the tarball's", got, err)
	}
	if info, err := os.Lstat(file); err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("SKILL.md: %v, mode %v, want 0444", err, info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Join(Dir(f.home, f.root), trashDirName)); len(entries) != 0 {
		t.Fatalf(".trash holds %d entries", len(entries))
	}

	restored, _, err = s.Ensure(context.Background(), r, f.link(), declared, reach)
	if err != nil || restored || len(f.fetches) != 1 {
		t.Fatalf("second Ensure = %v, %v with fetches %v", restored, err, f.fetches)
	}
}

// A tampered release from a link whose source isn't declared is refused and
// left as it is: nothing is fetched or moved.
func TestEnsureRefusesATamperedReleaseItMayNotFetch(t *testing.T) {
	f, s, r, tree := placedRelease(t)
	if err := os.Chmod(filepath.Join(tree, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.fetches = nil
	_, _, err := s.Ensure(context.Background(), r, f.link(), map[string]bool{}, everythingReachable)
	if !errors.Is(err, ErrRefused) || len(f.fetches) != 0 {
		t.Fatalf("Ensure = %v with fetches %v; want a refusal", err, f.fetches)
	}
	if _, err := os.Lstat(tree); err != nil {
		t.Fatalf("a refused release was moved: %v", err)
	}
}

// A held release whose tree can't be walked because a directory in it can't
// be listed is the store being unreadable: Ensure fetches nothing, moves
// nothing aside, writes no link and reports CondUnreadable.
func TestEnsureReportsAnUnwalkableTreeAsTheStoreUnreadable(t *testing.T) {
	skipAsRoot(t)
	f, s, r, tree := placedRelease(t)
	rdir := releaseDir(Dir(f.home, f.root), r)
	markPath := filepath.Join(rdir, markFileName)
	markBefore, err := os.ReadFile(markPath)
	if err != nil {
		t.Fatal(err)
	}
	dirBefore, err := os.Lstat(rdir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tree, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tree, 0o755) })
	if held, cond := s.Held(r); !held || cond != CondNone {
		t.Fatalf("Held = %v, %q; the setup must leave the release held", held, cond)
	}
	f.fetches = nil

	restored, cond, err := s.Ensure(context.Background(), r, f.link(), nil, nil)
	if restored || cond != CondUnreadable || err == nil {
		t.Fatalf("a held release whose tree can't be walked: Ensure = %v, %q, %v; want not restored, %q and an error", restored, cond, err, CondUnreadable)
	}
	if len(f.fetches) != 0 {
		t.Fatalf("a held release whose tree can't be walked was fetched again: %v", f.fetches)
	}
	dirAfter, err := os.Lstat(rdir)
	if err != nil || !os.SameFile(dirBefore, dirAfter) {
		t.Fatalf("a held release whose tree can't be walked was moved aside (%v)", err)
	}
	if markAfter, err := os.ReadFile(markPath); err != nil || !bytes.Equal(markAfter, markBefore) {
		t.Fatalf("a held release whose tree can't be walked had its held.json changed (%v)", err)
	}
	if info, err := os.Lstat(tree); err != nil || info.Mode().Perm() != 0o111 {
		t.Fatalf("a held release whose tree can't be walked had its mode reset (%v)", err)
	}
	if _, err := os.Lstat(f.link()); !os.IsNotExist(err) {
		t.Fatalf("a held release whose tree can't be walked was linked (%v)", err)
	}
}

// verifyCondition reads a permission error as the store being unreadable and
// any other Verify error as a changed release.
func TestVerifyConditionSeparatesStoreFromContent(t *testing.T) {
	denied := fmt.Errorf("walking release acme/skills: %w", &fs.PathError{Op: "open", Path: "/s/tree/plugins", Err: syscall.EACCES})
	gone := fmt.Errorf("walking release acme/skills: %w", &fs.PathError{Op: "lstat", Path: "/s/tree/plugins/x", Err: syscall.ENOENT})
	var m Mark
	parse := fmt.Errorf("parsing held.json of acme/skills: %w", json.Unmarshal([]byte("{"), &m))
	cases := []struct {
		name string
		err  error
		want Condition
	}{
		{"permission", denied, CondUnreadable},
		{"vanished entry", gone, CondNone},
		{"mark that no longer parses", parse, CondNone},
		{"no error", nil, CondNone},
	}
	for _, tc := range cases {
		if got := verifyCondition(tc.err); got != tc.want {
			t.Errorf("%s: verifyCondition(%v) = %q, want %q", tc.name, tc.err, got, tc.want)
		}
	}
}
