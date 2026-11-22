package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRemoteIdentity(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// One github.com repo, spelled six ways.
		{"git@github.com:o/r.git", "github.com/o/r"},
		{"ssh://git@github.com/o/r", "github.com/o/r"},
		{"ssh://git@github.com:22/o/r.git", "github.com/o/r"},
		{"https://github.com/o/r.git/", "github.com/o/r"},
		{"https://alice:tok@GitHub.com:443/o/r", "github.com/o/r"},
		{"http://github.com:80/o/r", "github.com/o/r"},
		// A port that isn't the scheme's default is kept.
		{"ssh://git@host:2222/o/r", "host:2222/o/r"},
		{"ssh://git@host/o/r", "host/o/r"},
		{"https://host:22/o/r", "host:22/o/r"},
		// Another host or another path is another remote.
		{"git@gitlab.com:o/r.git", "gitlab.com/o/r"},
		{"https://github.com/o/other.git", "github.com/o/other"},
		// A local path and a file:// URL give the path, without .git and
		// trailing slashes.
		{"/srv/git/r.git", "/srv/git/r"},
		{"/srv/git/r.git/", "/srv/git/r"},
		{"file:///srv/git/r.git", "/srv/git/r"},
		{"file:///srv/git/r/", "/srv/git/r"},
		// A query or fragment, which recordURL strips, is no part of it.
		{"https://host/o/r.git?x=1#f", "host/o/r"},
	} {
		if got := remoteIdentity(tc.in); got != tc.want {
			t.Errorf("remoteIdentity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if remoteIdentity("ssh://git@host:2222/o/r") == remoteIdentity("ssh://git@host/o/r") {
		t.Error("a non-default port reads as the default one")
	}
}

// spellingHome points HOME, which the network calls read their global git
// config from, at a config that sends each spelling of the mirror.test
// remote to the fixture's remote, so a refresh or restore under any of them
// reaches it with no network. It returns the URL that carries a token.
func spellingHome(t *testing.T, f fixture) string {
	t.Helper()
	token := "https://alice:tok@mirror.test/o/app.git"
	var b strings.Builder
	fmt.Fprintf(&b, "[url \"file://%s\"]\n", f.remote)
	for _, from := range []string{sshSpelling, userSpelling, httpsSpelling, token} {
		fmt.Fprintf(&b, "\tinsteadOf = %s\n", from)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return token
}

// stateFields reads the fixture's state file as raw JSON fields.
func stateFields(t *testing.T, f fixture) map[string]any {
	t.Helper()
	data, err := os.ReadFile(statePath(f.mdir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

// requireStateRemote fails unless the state file on disk records the
// recorded form of url and the identity of its remote.
func requireStateRemote(t *testing.T, f fixture, url string) {
	t.Helper()
	fields := stateFields(t, f)
	if fields["url"] != recordURL(url) || fields["identity"] != remoteIdentity(url) {
		t.Fatalf("state file url %v, identity %v; want %q, %q", fields["url"], fields["identity"], recordURL(url), remoteIdentity(url))
	}
}

// storeRemoteURL is the linked store's remote.origin.url.
func storeRemoteURL(t *testing.T, f fixture) string {
	t.Helper()
	return testGit(t, f.root, "-C", StablePath(f.home, f.root, "app"), "config", "remote.origin.url")
}

// After a seed, a refresh and a restore, the state file holds the URL with
// its credentials stripped and the identity of the config's remote.
func TestState_RecordsTheIdentity(t *testing.T) {
	f := newFixture(t)
	token := spellingHome(t, f)
	repo := Repo{ID: "app", URL: token}
	f.borrow(t)

	if res := Seed(context.Background(), f.applyOpts(), repo); res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("seed = %+v", res)
	}
	requireStateRemote(t, f, token)
	if got := stateFields(t, f)["url"]; got != httpsSpelling {
		t.Fatalf("state url = %v, want %q", got, httpsSpelling)
	}

	if res := EnsureHealthy(context.Background(), refreshing(f.applyOpts()), []Repo{repo}); len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("refresh = %+v", res)
	}
	requireStateRemote(t, f, token)

	if err := os.Remove(filepath.Join(f.store(t), "HEAD")); err != nil {
		t.Fatal(err)
	}
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{repo})
	if len(res) != 1 || res[0].Fault != FaultRepoUnreadable || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("restore = %+v", res)
	}
	requireStateRemote(t, f, token)
}

// A state file written by v0.43.0 has no identity. It reads without error,
// its identity derived from the URL it does record, the SSH spelling of its
// remote is no fault, and the next write of the file adds the field.
func TestState_WithoutIdentityIsReadFromItsURL(t *testing.T) {
	f := newFixture(t)
	spellingHome(t, f)
	f.borrow(t)
	repo := Repo{ID: "app", URL: sshSpelling}
	if res := Seed(context.Background(), f.applyOpts(), repo); res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("seed = %+v", res)
	}
	writeOldStateURL(t, f, sshSpelling)
	if _, ok := stateFields(t, f)["identity"]; ok {
		t.Fatal("the rewritten state file still has an identity")
	}

	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatalf("reading a state file with no identity: %v", err)
	}
	if st.Identity != "mirror.test/o/app" {
		t.Fatalf("identity = %q, want mirror.test/o/app", st.Identity)
	}
	if got := check(f.gitCtx(t), f.mdir(), repo); got != FaultNone {
		t.Fatalf("check = %q, want no fault", got)
	}

	if res := EnsureHealthy(context.Background(), refreshing(f.applyOpts()), []Repo{repo}); len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("refresh = %+v", res)
	}
	requireStateRemote(t, f, sshSpelling)
}

// A mirror built under the SSH spelling stays healthy when the config moves
// to HTTPS or changes the user alone, and the refresh after each puts the
// config's new spelling, credentials stripped, in the store's config and the
// state file, without moving the link.
func TestRefresh_SpellingChangeTakesTheNewSpelling(t *testing.T) {
	f := newFixture(t)
	spellingHome(t, f)
	f.borrow(t)
	if res := Seed(context.Background(), f.applyOpts(), Repo{ID: "app", URL: sshSpelling}); res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("seed = %+v", res)
	}
	link := f.linkTarget(t)
	for _, url := range []string{httpsSpelling, userSpelling, sshSpelling} {
		res := EnsureHealthy(context.Background(), refreshing(f.applyOpts()), []Repo{{ID: "app", URL: url}})
		if len(res) != 1 || res[0].Fault != FaultNone || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
			t.Fatalf("refresh under %q = %+v, want healthy", url, res)
		}
		if got := storeRemoteURL(t, f); got != recordURL(url) {
			t.Fatalf("after the refresh under %q the store's remote.origin.url = %q, want %q", url, got, recordURL(url))
		}
		requireStateRemote(t, f, url)
		if f.linkTarget(t) != link {
			t.Fatalf("the refresh under %q moved the link", url)
		}
	}
}

// restoreLine is the last restore line in the fixture's mirror log.
func restoreLine(t *testing.T, f fixture) map[string]any {
	t.Helper()
	lines := rawLogLines(t, logPath(f.mdir()))
	for i := len(lines) - 1; i >= 0; i-- {
		if line := decodeLine(t, lines[i]); line["event"] == eventRestore {
			return line
		}
	}
	t.Fatal("the mirror log has no restore line")
	return nil
}

// salvagedFrom is a restore line's salvaged_from, and whether it has one.
func salvagedFrom(t *testing.T, line map[string]any) ([]string, bool) {
	t.Helper()
	raw, ok := line["salvaged_from"]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("salvaged_from = %v, want a list", raw)
	}
	var names []string
	for _, v := range list {
		names = append(names, fmt.Sprint(v))
	}
	return names, true
}

// A restore for a damage fault after the config moved from the SSH to the
// HTTPS spelling of the same remote names the damage, not url-changed, and
// salvages the old store: a pack only it held reaches the new store, and the
// restore line names the store it came from.
func TestRestore_AfterASpellingChangeSalvages(t *testing.T) {
	for _, url := range []string{httpsSpelling, userSpelling} {
		t.Run(url, func(t *testing.T) {
			f := newFixture(t)
			spellingHome(t, f)
			f.borrow(t)
			if res := Seed(context.Background(), f.applyOpts(), Repo{ID: "app", URL: sshSpelling}); res.Outcome != OutcomeRestored || res.Err != nil {
				t.Fatalf("seed = %+v", res)
			}
			old := f.linkTarget(t)
			onlyOld := extraPack(t, filepath.Join(f.store(t), "objects", "pack"), "only the old store")
			if err := os.Remove(filepath.Join(f.store(t), "HEAD")); err != nil {
				t.Fatal(err)
			}

			res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{{ID: "app", URL: url}})
			if len(res) != 1 || res[0].Fault != FaultRepoUnreadable || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
				t.Fatalf("restore = %+v, want repo-unreadable/restored", res)
			}
			if f.linkTarget(t) == old {
				t.Fatal("the link still names the old store")
			}
			if _, err := os.Lstat(filepath.Join(f.store(t), "objects", "pack", onlyOld)); err != nil {
				t.Fatalf("the pack only the old store held didn't reach the new one: %v", err)
			}
			from, ok := salvagedFrom(t, restoreLine(t, f))
			if !ok || !slices.Equal(from, []string{old}) {
				t.Fatalf("restore line salvaged_from = %v (present %v), want [%q]", from, ok, old)
			}
			if got := storeRemoteURL(t, f); got != recordURL(url) {
				t.Fatalf("the store's remote.origin.url = %q, want %q", got, recordURL(url))
			}
			requireStateRemote(t, f, url)
		})
	}
}

// A config that points the repo at another remote, here another path with
// the same history, rebuilds with fault url-changed, salvages nothing, and
// says so by giving its restore line no salvaged_from.
func TestRestore_AnotherRemoteSalvagesNothing(t *testing.T) {
	f := built(t)
	other := filepath.Join(filepath.Dir(f.remote), "other.git")
	testGit(t, filepath.Dir(f.remote), "clone", "--quiet", "--bare", f.remote, other)
	onlyOld := extraPack(t, filepath.Join(f.store(t), "objects", "pack"), "only the old store")

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{{ID: "app", URL: "file://" + other}})
	if len(res) != 1 || res[0].Fault != FaultURLChanged || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("restore = %+v, want url-changed/restored", res)
	}
	if _, err := os.Lstat(filepath.Join(f.store(t), "objects", "pack", onlyOld)); !os.IsNotExist(err) {
		t.Fatalf("a pack only the old store held reached the new one (%v)", err)
	}
	if from, ok := salvagedFrom(t, restoreLine(t, f)); ok {
		t.Fatalf("restore line salvaged_from = %v, want none", from)
	}
	requireStateRemote(t, f, "file://"+other)
}
