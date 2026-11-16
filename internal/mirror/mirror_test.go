package mirror

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// testGit runs a fixture git command with the developer's config shut out.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixture is a workspace with a remote, a home and one instance clone.
type fixture struct {
	home, root, remote, clone string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base := t.TempDir()
	f := fixture{
		home:   filepath.Join(base, "home"),
		root:   filepath.Join(base, "ws"),
		remote: filepath.Join(base, "remote.git"),
	}
	f.clone = filepath.Join(f.root, "inst", "tools", "app")
	for _, d := range []string{f.home, f.clone} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(base, "work")
	testGit(t, base, "init", "--quiet", "--bare", f.remote)
	testGit(t, base, "init", "--quiet", work)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, work, "add", "a.txt")
	testGit(t, work, "commit", "--quiet", "-m", "first")
	testGit(t, work, "push", "--quiet", f.remote, "HEAD:refs/heads/main")
	testGit(t, f.remote, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.MkdirAll(filepath.Join(f.clone, ".git", "objects", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) borrow(t *testing.T) {
	t.Helper()
	line := StablePath(f.home, f.root, "app") + "/objects\n"
	if err := os.WriteFile(filepath.Join(f.clone, ".git", "objects", "info", "alternates"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) opts() Options {
	return Options{Home: f.home, WorkspaceRoot: f.root, Instances: []string{f.clone}}
}

func (f fixture) repo() Repo { return Repo{ID: "app", URL: "file://" + f.remote} }

func (f fixture) mdir() string { return mirrorsDir(f.home, f.root) }

func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s holds %d entries, want none", dir, len(entries))
	}
}

func TestLayoutNames(t *testing.T) {
	root := t.TempDir()
	if key := WorkspaceKey(root); !regexp.MustCompile(`^w-[0-9a-f]{32}$`).MatchString(key) {
		t.Fatalf("WorkspaceKey = %q", key)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if WorkspaceKey(link) != WorkspaceKey(root+"/") {
		t.Fatal("a symlinked or slashed root gets a different key")
	}
	lock := LockPath("/h", root, "app")
	if filepath.Dir(lock) != "/h/.niwa/locks" || !regexp.MustCompile(`^m-[0-9a-f]{32}\.lock$`).MatchString(filepath.Base(lock)) {
		t.Fatalf("LockPath = %q", lock)
	}
	if LockPath("/h", root, "app") == LockPath("/h", root, "app2") {
		t.Fatal("two repos share a lock")
	}
	if got, want := StablePath("/h", root, "app"), "/h/.niwa/"+WorkspaceKey(root)+"/mirrors/app"; got != want {
		t.Fatalf("StablePath = %q, want %q", got, want)
	}
}

func TestValidRepoID(t *testing.T) {
	for _, id := range []string{"app", "A.b_c-1", strings.Repeat("a", 100)} {
		if !ValidRepoID(id) {
			t.Errorf("ValidRepoID(%q) = false", id)
		}
	}
	for _, id := range []string{"", ".meta", ".store", "-x", "a/b", "..", "a b", strings.Repeat("a", 101)} {
		if ValidRepoID(id) {
			t.Errorf("ValidRepoID(%q) = true", id)
		}
	}
}

func TestValidLinkTarget(t *testing.T) {
	good := ".store/app-20261116T101500Z-3f9a0b1c"
	if !validLinkTarget("app", good) {
		t.Fatalf("validLinkTarget rejected %q", good)
	}
	for _, target := range []string{
		"/abs/.store/app-20261116T101500Z-3f9a0b1c",
		".store/app-x-20261116T101500Z-3f9a0b1c",
		".store/other-20261116T101500Z-3f9a0b1c",
		"../.store/app-20261116T101500Z-3f9a0b1c",
		".store/../app-20261116T101500Z-3f9a0b1c",
		".tmp/app-20261116T101500Z-3f9a0b1c",
		".store/app-20261116T101500Z-3f9a0b1c/x",
	} {
		if validLinkTarget("app", target) {
			t.Errorf("validLinkTarget accepted %q", target)
		}
	}
}

func TestMirrored_NoBorrowersKeepsNothingAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	if got := Mirrored(f.home, f.root, []string{f.clone}, []Repo{f.repo()}); len(got) != 0 {
		t.Fatalf("Mirrored = %v, want none", got)
	}
	if res := EnsureHealthy(context.Background(), f.opts(), []Repo{f.repo()}); len(res) != 0 {
		t.Fatalf("EnsureHealthy = %v, want no results", res)
	}
	emptyDir(t, f.home)
}

func TestMirrored_KeepsBorrowedAndStoredRepos(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	other := Repo{ID: "other", URL: "file:///x"}
	bad := Repo{ID: ".meta", URL: "file:///x"}
	got := Mirrored(f.home, f.root, []string{f.clone}, []Repo{f.repo(), other, bad})
	if len(got) != 1 || got[0].ID != "app" {
		t.Fatalf("Mirrored = %v, want only app", got)
	}

	// A store of "other-x" must not count as a store of "other".
	if err := os.MkdirAll(filepath.Join(f.mdir(), ".store", "other-x-20261116T101500Z-3f9a0b1c"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := Mirrored(f.home, f.root, nil, []Repo{other}); len(got) != 0 {
		t.Fatalf("a store of another repo kept %v", got)
	}
	if err := os.MkdirAll(filepath.Join(f.mdir(), ".store", "other-20261116T101500Z-3f9a0b1c"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := Mirrored(f.home, f.root, nil, []Repo{other}); len(got) != 1 {
		t.Fatalf("Mirrored with a store of other = %v, want it kept", got)
	}
}

func TestEnsureHealthy_RestoresAMissingMirror(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)

	res := EnsureHealthy(context.Background(), f.opts(), []Repo{f.repo()})
	if len(res) != 1 {
		t.Fatalf("EnsureHealthy returned %d results, want 1", len(res))
	}
	r := res[0]
	if r.Fault != FaultMissing || r.Outcome != OutcomeRestored || r.Err != nil || !r.Usable() {
		t.Fatalf("result = %+v, want missing/restored", r)
	}
	stable := StablePath(f.home, f.root, "app")
	if r.StablePath != stable {
		t.Fatalf("StablePath = %q, want %q", r.StablePath, stable)
	}
	target, err := os.Readlink(stable)
	if err != nil {
		t.Fatalf("the stable path is not a link: %v", err)
	}
	if !validLinkTarget("app", target) {
		t.Fatalf("link target %q is not a store name", target)
	}
	store := filepath.Join(f.mdir(), target)
	if got, want := testGit(t, store, "--git-dir", store, "rev-parse", "HEAD"), testGit(t, f.remote, "rev-parse", "HEAD"); got != want {
		t.Fatalf("restored HEAD = %s, want %s", got, want)
	}
	if hooks, err := os.ReadDir(filepath.Join(store, "hooks")); err == nil && len(hooks) > 0 {
		t.Fatalf("the restored store has %d hooks, want none", len(hooks))
	}

	for _, dir := range []string{keyDir(f.home, f.root), f.mdir()} {
		info, err := os.Lstat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %v (%v), want 0700", dir, info, err)
		}
	}
	lock, err := os.Lstat(LockPath(f.home, f.root, "app"))
	if err != nil || lock.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %v (%v), want 0600", lock, err)
	}
	emptyDir(t, filepath.Join(f.mdir(), ".tmp"))
	links, _ := filepath.Glob(filepath.Join(f.mdir(), ".link-*"))
	if len(links) != 0 {
		t.Fatalf("leftover links: %v", links)
	}

	again := EnsureHealthy(context.Background(), f.opts(), []Repo{f.repo()})
	if len(again) != 1 || again[0].Outcome != OutcomeHealthy || again[0].Fault != FaultNone {
		t.Fatalf("second call = %+v, want healthy", again)
	}
	if after, _ := os.Readlink(stable); after != target {
		t.Fatalf("a healthy check moved the link from %q to %q", target, after)
	}
}

func TestEnsureHealthy_BusyWhenAnotherRestoreHoldsTheLock(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	if err := ensureLayout(f.home, f.root); err != nil {
		t.Fatal(err)
	}
	release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "other restore")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	opts := f.opts()
	opts.LockWait = 100 * time.Millisecond
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeBusy || res[0].Usable() {
		t.Fatalf("result = %+v, want busy", res)
	}
	if _, err := os.Lstat(StablePath(f.home, f.root, "app")); !os.IsNotExist(err) {
		t.Fatal("a busy restore wrote the stable path")
	}
	emptyDir(t, filepath.Join(f.mdir(), ".store"))
	emptyDir(t, filepath.Join(f.mdir(), ".tmp"))
}

func TestEnsureHealthy_FailedCloneLeavesNothing(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	repo := Repo{ID: "app", URL: "file://" + filepath.Join(t.TempDir(), "absent.git")}
	res := EnsureHealthy(context.Background(), f.opts(), []Repo{repo})
	if len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed || res[0].Err == nil {
		t.Fatalf("result = %+v, want restore-failed with an error", res)
	}
	if _, err := os.Lstat(StablePath(f.home, f.root, "app")); !os.IsNotExist(err) {
		t.Fatal("a failed restore wrote the stable path")
	}
	emptyDir(t, filepath.Join(f.mdir(), ".store"))
	emptyDir(t, filepath.Join(f.mdir(), ".tmp"))
}

func TestEnsureHealthy_LeavesANonLinkAtTheStablePath(t *testing.T) {
	f := newFixture(t)
	stable := StablePath(f.home, f.root, "app")
	if err := os.MkdirAll(stable, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(stable, "keep")
	if err := os.WriteFile(keep, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := EnsureHealthy(context.Background(), f.opts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultMissing || res[0].Outcome != OutcomeRestoreFailed {
		t.Fatalf("result = %+v, want missing/restore-failed", res)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "mine" {
		t.Fatalf("the directory at the stable path was changed: %q (%v)", data, err)
	}
	emptyDir(t, filepath.Join(f.mdir(), ".store"))
}

func TestResultNotice(t *testing.T) {
	stable := "/h/.niwa/w-0123/mirrors/app"
	log := "/h/.niwa/w-0123/mirrors/.meta/mirror.log"
	cases := []struct {
		res  Result
		want string
	}{
		{Result{RepoID: "app", StablePath: stable, Outcome: OutcomeHealthy}, ""},
		{
			Result{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestored},
			"niwa mirror app: fault missing, result restored. It was rebuilt from the remote, and the clones that borrow from it read normally. Mirror log: " + log,
		},
		{
			Result{RepoID: "app", StablePath: stable, Fault: FaultPackChanged, Outcome: OutcomeRestoreFailed},
			"niwa mirror app: fault pack-changed, result restore-failed. It could not be rebuilt, so clones that borrow from it may fail to read objects until a later niwa command restores it. Mirror log: " + log,
		},
		{
			Result{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeBusy},
			"niwa mirror app: fault missing, result busy. Another niwa command held it for the whole wait, so nothing was changed; the next niwa command checks it again. Mirror log: " + log,
		},
	}
	for _, c := range cases {
		if got := c.res.Notice(); got != c.want {
			t.Errorf("Notice() for %s =\n%q\nwant\n%q", c.res.Outcome, got, c.want)
		}
	}

	forged := Result{
		RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestoreFailed,
		Err: errors.New("\x1b[31mx\nforged line"),
	}
	got := forged.Notice()
	if strings.ContainsAny(got, "\x1b\n\r") || strings.Contains(got, "forged") || strings.Contains(got, "[31m") {
		t.Fatalf("Notice() carries the error's text: %q", got)
	}
	want := "niwa mirror app: fault missing, result restore-failed. It could not be rebuilt, so clones that borrow from it may fail to read objects until a later niwa command restores it. Mirror log: " + log
	if got != want {
		t.Fatalf("Notice() with an error =\n%q\nwant\n%q", got, want)
	}

	hostile := Result{RepoID: "app", StablePath: "/h\n\x1b[2Jx/.niwa/w-0123/mirrors/app", Fault: FaultMissing, Outcome: OutcomeRestored}
	if got := hostile.Notice(); strings.ContainsAny(got, "\x1b\n") {
		t.Fatalf("Notice() kept a control character from the home path: %q", got)
	}
}

func TestRecorded_FindsEachSourceAlone(t *testing.T) {
	stamp := "-20261116T101500Z-3f9a0b1c"
	cases := []struct {
		name  string
		setup func(t *testing.T, f fixture)
		want  []string
	}{
		{"state file", func(t *testing.T, f fixture) {
			writeFile(t, filepath.Join(f.mdir(), ".meta", "app.json"), "{}")
		}, []string{"app"}},
		{"link", func(t *testing.T, f fixture) {
			mkdir(t, f.mdir())
			if err := os.Symlink(".store/app"+stamp, filepath.Join(f.mdir(), "app")); err != nil {
				t.Fatal(err)
			}
		}, []string{"app"}},
		{"store directory", func(t *testing.T, f fixture) {
			mkdir(t, filepath.Join(f.mdir(), ".store", "app"+stamp))
		}, []string{"app"}},
		{"borrower", func(t *testing.T, f fixture) {
			f.borrow(t)
		}, []string{"app"}},
		{"store of a longer id", func(t *testing.T, f fixture) {
			mkdir(t, filepath.Join(f.mdir(), ".store", "app-extra"+stamp))
		}, []string{"app-extra"}},
		{"niwa's own entries and invalid names", func(t *testing.T, f fixture) {
			writeFile(t, filepath.Join(f.mdir(), ".meta", "mirror.log"), "{}\n")
			writeFile(t, filepath.Join(f.mdir(), ".meta", ".app.json-3f9a0b1c"), "{}")
			writeFile(t, filepath.Join(f.mdir(), ".meta", "-bad.json"), "{}")
			mkdir(t, filepath.Join(f.mdir(), ".tmp", "app-3f9a0b1c"))
			mkdir(t, filepath.Join(f.mdir(), ".store", "-bad"+stamp))
			mkdir(t, filepath.Join(f.mdir(), ".store", "app-not-a-store"))
			if err := os.Symlink(".store/app"+stamp, filepath.Join(f.mdir(), ".link-3f9a0b1c")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(f.clone, ".git", "objects", "info", "alternates"), "/elsewhere/app/objects\n")
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.setup(t, f)
			got := Recorded(f.home, f.root, []string{f.clone})
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("Recorded = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRecorded_NoKeyDirectoryReturnsAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	if got := Recorded(f.home, f.root, []string{f.clone}); len(got) != 0 {
		t.Fatalf("Recorded = %v, want none", got)
	}
	emptyDir(t, f.home)
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureHealthy_ReplacesALinkPointingOutside(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	outside := t.TempDir()
	marker := filepath.Join(outside, "HEAD")
	if err := os.WriteFile(marker, []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.mdir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}

	res := EnsureHealthy(context.Background(), f.opts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultMissing || res[0].Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want missing/restored", res)
	}
	target, err := os.Readlink(StablePath(f.home, f.root, "app"))
	if err != nil || !validLinkTarget("app", target) {
		t.Fatalf("link target after restore = %q (%v)", target, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "ref: refs/heads/main\n" {
		t.Fatalf("the outside directory was touched: %q (%v)", data, err)
	}
}
