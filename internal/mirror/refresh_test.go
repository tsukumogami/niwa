package mirror

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"golang.org/x/sys/unix"
)

// refreshOpts are the fixture's options as a command that builds instances
// passes them.
func (f fixture) refreshOpts() Options {
	opts := f.applyOpts()
	opts.Refresh = true
	return opts
}

// gitTry runs a fixture git command and returns its output and error rather
// than failing the test, for a probe whose failure is the answer.
func gitTry(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = append(cmd.Env, noMaintenanceEnv...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// storeRef is what ref names in the linked store, or "" when it names nothing.
func (f fixture) storeRef(t *testing.T, ref string) string {
	t.Helper()
	store := f.store(t)
	out, err := gitTry(store, "--git-dir", store, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return ""
	}
	return out
}

// storeHas reports whether the linked store can read object id.
func (f fixture) storeHas(t *testing.T, id string) bool {
	t.Helper()
	store := f.store(t)
	_, err := gitTry(store, "--git-dir", store, "cat-file", "-e", id)
	return err == nil
}

// pushCommit commits a new file to branch of the fixture's remote, starting
// from main, and returns the commit.
func pushCommit(t testing.TB, f fixture, branch, content string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	testGit(t, filepath.Dir(work), "clone", "--quiet", f.remote, work)
	testGit(t, work, "checkout", "--quiet", "-B", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(work, content+".txt"), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, work, "add", content+".txt")
	testGit(t, work, "commit", "--quiet", "-m", content)
	testGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return testGit(t, work, "rev-parse", "HEAD")
}

// editState rewrites the state file through edit.
func editState(t *testing.T, f fixture, edit func(*state)) {
	t.Helper()
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	edit(&st)
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(f.mdir(), "app"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// stateBytes reads the state file as it is on disk.
func stateBytes(t *testing.T, f fixture) []byte {
	t.Helper()
	data, err := os.ReadFile(statePath(f.mdir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// parseStamp reads a state file timestamp.
func parseStamp(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("timestamp %q: %v", s, err)
	}
	return ts
}

// notBefore fails unless the state timestamp s is no earlier than start. The
// state file keeps whole seconds, so start is cut to its second.
func notBefore(t *testing.T, what, s string, start time.Time) {
	t.Helper()
	if parseStamp(t, s).Before(start.Truncate(time.Second)) {
		t.Fatalf("%s = %s, before %s", what, s, start.UTC().Format(time.RFC3339Nano))
	}
}

// newLines returns the log lines written after the first n.
func newLines(t *testing.T, f fixture, n int) []map[string]any {
	t.Helper()
	return f.logLines(t)[n:]
}

// requireEvents fails unless lines are exactly events, in order, each for
// app with fault and result as given.
func requireEvents(t *testing.T, lines []map[string]any, want ...[3]string) {
	t.Helper()
	if len(lines) != len(want) {
		t.Fatalf("log gained %v, want %d lines %v", lines, len(want), want)
	}
	for i, w := range want {
		l := lines[i]
		if l["mirror"] != "app" || l["event"] != w[0] || l["fault"] != w[1] || l["result"] != w[2] {
			t.Fatalf("log line %d = %v, want event %s, fault %q, result %s", i, l, w[0], w[1], w[2])
		}
	}
}

// requireManifestMatches fails unless the state file's packs list every pack
// in the linked store with the size, ctime and trailer the store shows, and
// the store holds no loose object.
func requireManifestMatches(t *testing.T, f fixture) {
	t.Helper()
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	packs := f.packs(t)
	if len(st.Packs) != len(packs) {
		t.Fatalf("manifest lists %d packs, the store holds %d", len(st.Packs), len(packs))
	}
	for i, path := range packs {
		var sys unix.Stat_t
		if err := unix.Lstat(path, &sys); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := packEntry{
			Name:    filepath.Base(path),
			Size:    int64(len(data)),
			CtimeNs: sys.Ctim.Nano(),
			Trailer: hex.EncodeToString(data[len(data)-20:]),
		}
		if st.Packs[i] != want {
			t.Fatalf("manifest entry %+v, want %+v", st.Packs[i], want)
		}
	}
	objects := filepath.Join(f.store(t), "objects")
	dirs, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !looseDirPattern.MatchString(d.Name()) {
			continue
		}
		if files, _ := os.ReadDir(filepath.Join(objects, d.Name())); len(files) != 0 {
			t.Fatalf("the store holds loose objects under %s", d.Name())
		}
	}
}

// configList is the store's config as git lists it, sorted.
func configList(t *testing.T, store string) []string {
	t.Helper()
	out := testGit(t, store, "config", "--file", filepath.Join(store, "config"), "--list")
	lines := strings.Split(out, "\n")
	sort.Strings(lines)
	return lines
}

// wantConfig is the fixed config a store cloned from url must hold, written
// out here rather than taken from retentionConfig, so dropping a key from the
// set fails the tests that compare against it. git lists keys in lower case.
func wantConfig(url string) []string {
	want := []string{
		"core.repositoryformatversion=0",
		"core.bare=true",
		"fetch.prune=false",
		"gc.auto=0",
		"maintenance.auto=false",
		"gc.pruneexpire=never",
		"core.logallrefupdates=always",
		"gc.reflogexpire=never",
		"gc.reflogexpireunreachable=never",
		"transfer.fsckobjects=true",
		"fetch.fsckobjects=true",
		"transfer.unpacklimit=1",
		"fetch.unpacklimit=1",
		"remote.origin.url=" + url,
		"remote.origin.fetch=+refs/*:refs/*",
	}
	sort.Strings(want)
	return want
}

func TestStoreConfig_IsTheFixedSet(t *testing.T) {
	f := built(t)
	if got, want := configList(t, f.store(t)), wantConfig(f.repo().URL); !slices.Equal(got, want) {
		t.Fatalf("config after a restore =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	store := f.store(t)
	testGit(t, store, "config", "--file", filepath.Join(store, "config"), "core.hooksPath", "/planted/hooks")
	testGit(t, store, "config", "--file", filepath.Join(store, "config"), "fetch.prune", "true")
	res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("refresh = %+v, want healthy", res)
	}
	if got, want := configList(t, f.store(t)), wantConfig(f.repo().URL); !slices.Equal(got, want) {
		t.Fatalf("config after a refresh =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if left, _ := filepath.Glob(filepath.Join(f.store(t), ".niwa-config-*")); len(left) != 0 {
		t.Fatalf("the config writer left %v", left)
	}
}

func TestStoreConfig_HoldsEveryRetentionKey(t *testing.T) {
	text, err := renderConfig(storeConfigEntries("file:///srv/git/r.git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range []string{
		"fetch.prune=false", "gc.auto=0", "maintenance.auto=false", "gc.pruneExpire=never",
		"core.logAllRefUpdates=always", "gc.reflogExpire=never", "gc.reflogExpireUnreachable=never",
		"transfer.fsckObjects=true", "fetch.fsckObjects=true", "transfer.unpackLimit=1", "fetch.unpackLimit=1",
	} {
		key, value, _ := strings.Cut(kv, "=")
		if !slices.Contains(retentionConfig, configEntry{key, value}) {
			t.Errorf("retentionConfig lacks %s", kv)
		}
		name := key[strings.LastIndex(key, ".")+1:]
		if !strings.Contains(text, "\t"+name+" = "+value+"\n") {
			t.Errorf("the rendered config lacks %s:\n%s", kv, text)
		}
	}
}

func TestStoreConfig_RecordsTheURLWithoutSecrets(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:tok@host/o/r.git?x=1#f": "https://host/o/r.git",
		"/srv/git/r.git":                      "/srv/git/r.git",
		"file:///srv/git/r.git":               "file:///srv/git/r.git",
	} {
		text, err := renderConfig(storeConfigEntries(in))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "[remote \"origin\"]\n\turl = "+want+"\n") {
			t.Errorf("config for %q =\n%s\nwant url = %s", in, text, want)
		}
		if strings.Contains(text, "tok") || strings.Contains(text, "x=1") {
			t.Errorf("config for %q carries a secret:\n%s", in, text)
		}
	}
	if _, err := renderConfig([]configEntry{{"remote.origin.url", "a\nb"}}); err == nil {
		t.Error("a value with a newline was written")
	}
	if text, err := renderConfig([]configEntry{{"a.b", `x;y"z\`}}); err != nil || !strings.Contains(text, `b = "x;y\"z\\"`) {
		t.Errorf("a value git would read specially = %q (%v), want it quoted", text, err)
	}
}

// requireRefreshed checks what every refresh in the no-prune test must leave:
// the manifest, the link and state store unchanged, last_refresh moved, and
// exactly one check and one refresh line logged.
func requireRefreshed(t *testing.T, f fixture, target string, start time.Time, logged int) {
	t.Helper()
	requireManifestMatches(t, f)
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if f.linkTarget(t) != target || st.Store != target {
		t.Fatalf("link %q, state store %q, want both %q", f.linkTarget(t), st.Store, target)
	}
	notBefore(t, "last_refresh", st.LastRefresh, start)
	lines := newLines(t, f, logged)
	requireEvents(t, lines, [3]string{"check", "", "healthy"}, [3]string{"refresh", "", "healthy"})
	if _, ok := lines[1]["took_ms"]; !ok {
		t.Fatalf("refresh line %v has no took_ms", lines[1])
	}
}

func TestRefresh_KeepsWhatTheRemoteDeleted(t *testing.T) {
	f := built(t)
	target := f.linkTarget(t)
	parent := testGit(t, f.remote, "rev-parse", "refs/heads/main")
	topic := pushCommit(t, f, "topic", "topic")
	tip := pushCommit(t, f, "main", "main-tip")

	logged := len(f.logLines(t))
	start := time.Now()
	res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Fault != FaultNone || res[0].Err != nil {
		t.Fatalf("first refresh = %+v, want healthy", res)
	}
	if f.storeRef(t, "refs/heads/topic") != topic || f.storeRef(t, "refs/heads/main") != tip {
		t.Fatalf("after the first refresh topic=%s main=%s, want %s and %s",
			f.storeRef(t, "refs/heads/topic"), f.storeRef(t, "refs/heads/main"), topic, tip)
	}
	requireRefreshed(t, f, target, start, logged)

	// The remote drops topic and main's tip, and every trace of them.
	testGit(t, f.remote, "update-ref", "-d", "refs/heads/topic")
	testGit(t, f.remote, "update-ref", "refs/heads/main", parent)
	testGit(t, f.remote, "reflog", "expire", "--expire=now", "--all")
	testGit(t, f.remote, "gc", "--quiet", "--prune=now")
	if _, err := gitTry(f.remote, "cat-file", "-e", topic); err == nil {
		t.Fatal("the remote still holds topic's commit; the case proves nothing")
	}

	logged = len(f.logLines(t))
	start = time.Now()
	res = EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("second refresh = %+v, want healthy", res)
	}
	if got := f.storeRef(t, "refs/heads/topic"); got != topic {
		t.Fatalf("topic after the remote deleted it = %q, want %s", got, topic)
	}
	if got := f.storeRef(t, "refs/heads/main"); got != parent {
		t.Fatalf("main = %q, want the remote's reset %s", got, parent)
	}
	reflog, err := os.ReadFile(filepath.Join(f.store(t), "logs", "refs", "heads", "main"))
	if err != nil {
		t.Fatalf("the store keeps no reflog for main: %v", err)
	}
	found := false
	sc := bufio.NewScanner(bytes.NewReader(reflog))
	for sc.Scan() {
		if old, _, _ := strings.Cut(sc.Text(), " "); old == tip {
			found = true
		}
	}
	if !found {
		t.Fatalf("main's reflog has no line whose old value is %s:\n%s", tip, reflog)
	}
	for _, id := range []string{topic, tip} {
		if !f.storeHas(t, id) {
			t.Fatalf("the store can't read %s", id)
		}
	}
	requireRefreshed(t, f, target, start, logged)

	// A person's own git gc in the store, with only the store's config.
	store := f.store(t)
	gc := exec.Command("git", "--git-dir", store, "gc", "--quiet")
	gc.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := gc.CombinedOutput(); err != nil {
		t.Fatalf("git gc in the store: %v\n%s", err, out)
	}
	for _, id := range []string{topic, tip} {
		if !f.storeHas(t, id) {
			t.Fatalf("a plain git gc dropped %s", id)
		}
	}
}

// TestRefresh_WritesTheURLNowhereButTheConfig: git writes a fetch's command
// line into each reflog line it adds and the URL into FETCH_HEAD, and the
// store keeps its reflogs forever, so a credential in the config's URL would
// stay on disk. The refresh names the URL on the command line only.
func TestRefresh_WritesTheURLNowhereButTheConfig(t *testing.T) {
	f := built(t)
	pushCommit(t, f, "topic", "topic")
	pushCommit(t, f, "main", "main-tip")
	if res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("refresh = %+v, want healthy", res)
	}

	store := f.store(t)
	if _, err := os.Lstat(filepath.Join(store, "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("the refresh wrote FETCH_HEAD (%v)", err)
	}
	reflog, err := os.ReadFile(filepath.Join(store, "logs", "refs", "heads", "main"))
	if err != nil {
		t.Fatalf("the refresh logged no update of main: %v", err)
	}
	if !strings.Contains(string(reflog), fetchReflogAction[len("GIT_REFLOG_ACTION="):]) {
		t.Fatalf("main's reflog doesn't carry the fixed message:\n%s", reflog)
	}
	for rel, body := range treeFiles(t, store) {
		if rel != "config" && strings.Contains(body, f.remote) {
			t.Errorf("%s in the store names the remote's URL", rel)
		}
	}
}

func TestRefresh_OffFetchesNothing(t *testing.T) {
	f := built(t)
	before := f.storeRef(t, "refs/heads/main")
	state := stateBytes(t, f)
	pushCommit(t, f, "main", "unfetched")

	logged := len(f.logLines(t))
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("result = %+v, want healthy", res)
	}
	if got := f.storeRef(t, "refs/heads/main"); got != before {
		t.Fatalf("main = %s, want it left at %s", got, before)
	}
	if !bytes.Equal(stateBytes(t, f), state) {
		t.Fatal("the state file changed with Refresh off")
	}
	requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"})

	// Set on a damaged mirror, the call restores and doesn't refresh.
	damageHEAD(t, f)
	logged = len(f.logLines(t))
	res = EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("result on a damaged mirror = %+v, want restored", res)
	}
	requireEvents(t, newLines(t, f, logged),
		[3]string{"check", string(FaultRepoUnreadable), "restored"},
		[3]string{"restore", string(FaultRepoUnreadable), "restored"})
}

func TestRefresh_SkippedWhileAnotherCommandHoldsTheLock(t *testing.T) {
	f := built(t)
	before := f.storeRef(t, "refs/heads/main")
	state := stateBytes(t, f)
	pushCommit(t, f, "main", "unfetched")
	release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "test holder")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	logged := len(f.logLines(t))
	start := time.Now()
	res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if took := time.Since(start); took >= time.Second {
		t.Fatalf("a refresh with the lock held took %s, want under a second", took)
	}
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("result = %+v, want healthy with no error", res)
	}
	if got := f.storeRef(t, "refs/heads/main"); got != before {
		t.Fatalf("main = %s, want it left at %s", got, before)
	}
	if !bytes.Equal(stateBytes(t, f), state) {
		t.Fatal("the state file changed while the lock was held")
	}
	requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"})
}

func TestRefresh_AFailedFetchChangesNothing(t *testing.T) {
	f := built(t)
	target := f.linkTarget(t)
	packs := treeFiles(t, filepath.Join(f.store(t), "objects", "pack"))
	state := stateBytes(t, f)
	hideRemote(t, f)

	logged := len(f.logLines(t))
	res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Fault != FaultNone || res[0].Err == nil {
		t.Fatalf("result = %+v, want healthy with an error", res)
	}
	if f.linkTarget(t) != target {
		t.Fatal("a failed refresh moved the link")
	}
	if !maps.Equal(treeFiles(t, filepath.Join(f.store(t), "objects", "pack")), packs) {
		t.Fatal("a failed refresh changed the store's packs")
	}
	if !bytes.Equal(stateBytes(t, f), state) {
		t.Fatal("a failed refresh changed the state file")
	}
	requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"})
}

func TestRefresh_DeepVerifiesOnceADay(t *testing.T) {
	f := built(t)

	stale := stateTime(time.Now().Add(-25 * time.Hour))
	editState(t, f, func(st *state) { st.LastDeepVerify = stale })
	start := time.Now()
	if res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("refresh at 25 hours = %+v", res)
	}
	st, _ := readState(f.mdir(), "app")
	notBefore(t, "last_deep_verify at 25 hours", st.LastDeepVerify, start)
	if st.LastDeepVerify != st.LastRefresh {
		t.Fatalf("last_deep_verify %s, last_refresh %s: want both the refresh's time", st.LastDeepVerify, st.LastRefresh)
	}

	recent := stateTime(time.Now().Add(-23 * time.Hour))
	editState(t, f, func(st *state) { st.LastDeepVerify = recent })
	start = time.Now()
	if res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("refresh at 23 hours = %+v", res)
	}
	st, _ = readState(f.mdir(), "app")
	if st.LastDeepVerify != recent {
		t.Fatalf("last_deep_verify at 23 hours = %s, want it left at %s", st.LastDeepVerify, recent)
	}
	notBefore(t, "last_refresh at 23 hours", st.LastRefresh, start)

	// A state file as steps before the refresh wrote it: no timestamps.
	editState(t, f, func(st *state) { st.LastDeepVerify, st.LastRefresh = "", "" })
	if raw := string(stateBytes(t, f)); strings.Contains(raw, "last_") {
		t.Fatalf("the old-form state file still has a timestamp: %s", raw)
	}
	start = time.Now()
	if res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("refresh of an old-form state file = %+v", res)
	}
	st, _ = readState(f.mdir(), "app")
	notBefore(t, "last_deep_verify from an old-form state file", st.LastDeepVerify, start)
	notBefore(t, "last_refresh from an old-form state file", st.LastRefresh, start)
}

// rotPack adds two packs to the linked store, one the manifest lists and one
// it doesn't, then overwrites bytes mid-way through the listed one at the
// same size and records its new ctime, so the offline check, which compares
// size, trailer and ctime, passes. The rotten pack holds a commit nothing
// reads, so the check's HEAD read can't trip on it either. It returns the
// rotten pack's name and the intact one's.
func rotPack(t *testing.T, f fixture) (rotten, intact string) {
	t.Helper()
	dir := filepath.Join(f.store(t), "objects", "pack")
	intact = extraPack(t, dir, "an intact pack beside the rotten one")
	rotten = extraPack(t, dir, "a pack that rots")
	pack := filepath.Join(dir, rotten)
	listed, err := describePack(pack)
	if err != nil {
		t.Fatal(err)
	}
	editState(t, f, func(st *state) { st.Packs = append(st.Packs, listed) })
	time.Sleep(20 * time.Millisecond)
	info, err := os.Stat(pack)
	if err != nil {
		t.Fatal(err)
	}
	makeWritable(t, pack)
	fh, err := os.OpenFile(pack, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteAt(bytes.Repeat([]byte{0xff}, 8), (info.Size()-20)/2); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	now, err := describePack(pack)
	if err != nil {
		t.Fatal(err)
	}
	editState(t, f, func(st *state) {
		for i := range st.Packs {
			if st.Packs[i].Name == filepath.Base(pack) {
				st.Packs[i].CtimeNs = now.CtimeNs
			}
		}
	})
	if got := check(f.gitCtx(t), f.mdir(), f.repo()); got != FaultNone {
		t.Fatalf("check of the rotten pack = %q, want no fault: the case needs the check to pass", got)
	}
	if verifyPack(f.gitCtx(t), idxOf(pack)) == nil {
		t.Fatal("verify-pack passes the rotten pack; the case proves nothing")
	}
	return rotten, intact
}

func TestRefresh_DeepVerifyFindsRotTheCheckCantSee(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)
	rotten, intact := rotPack(t, f)
	editState(t, f, func(st *state) { st.LastDeepVerify = stateTime(time.Now().Add(-25 * time.Hour)) })

	logged := len(f.logLines(t))
	res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultPackChanged || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("result = %+v, want pack-changed/restored", res)
	}
	requireEvents(t, newLines(t, f, logged),
		[3]string{"check", "", "healthy"},
		[3]string{"restore", string(FaultPackChanged), "restored"})
	if f.linkTarget(t) == old {
		t.Fatal("the link still names the rotten store")
	}
	dir := filepath.Join(f.store(t), "objects", "pack")
	if _, err := os.Lstat(filepath.Join(dir, rotten)); !os.IsNotExist(err) {
		t.Fatalf("the new store holds the rotten pack (%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, intact)); err != nil {
		t.Fatalf("the new store lacks the intact pack: %v", err)
	}
	for _, p := range f.packs(t) {
		if err := verifyPack(f.gitCtx(t), idxOf(p)); err != nil {
			t.Fatalf("the new store's %s fails verify-pack: %v", filepath.Base(p), err)
		}
	}
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if st.LastDeepVerify == "" || st.LastDeepVerify != st.LastRefresh {
		t.Fatalf("last_deep_verify %q, last_refresh %q: want them equal", st.LastDeepVerify, st.LastRefresh)
	}
}

func TestRefresh_NoDeepVerifyWithinTheDay(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)
	rotPack(t, f)
	editState(t, f, func(st *state) { st.LastDeepVerify = stateTime(time.Now().Add(-23 * time.Hour)) })

	logged := len(f.logLines(t))
	res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultNone || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
		t.Fatalf("result = %+v, want healthy", res)
	}
	requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"}, [3]string{"refresh", "", "healthy"})
	if f.linkTarget(t) != old {
		t.Fatal("the link moved")
	}
}

func TestRestore_RecordsARefreshAndADeepVerify(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	start := time.Now()
	if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want restored", res)
	}
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRefresh != st.LastDeepVerify {
		t.Fatalf("last_refresh %q, last_deep_verify %q: want them equal", st.LastRefresh, st.LastDeepVerify)
	}
	notBefore(t, "last_refresh", st.LastRefresh, start)

	damageHEAD(t, f)
	start = time.Now()
	if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeRestored {
		t.Fatalf("second result = %+v, want restored", res)
	}
	st, _ = readState(f.mdir(), "app")
	if st.LastRefresh != st.LastDeepVerify {
		t.Fatalf("after a second restore last_refresh %q, last_deep_verify %q", st.LastRefresh, st.LastDeepVerify)
	}
	notBefore(t, "last_refresh after a second restore", st.LastRefresh, start)
}

// goneCommit puts into the linked store an intact pack holding a commit G no
// remote has, names it refs/heads/gone, and returns G and the name of the
// store's original pack.
func goneCommit(t *testing.T, f fixture) (g, original string) {
	t.Helper()
	original = f.packs(t)[0]
	work := filepath.Join(t.TempDir(), "gone")
	testGit(t, filepath.Dir(work), "init", "--quiet", work)
	if err := os.WriteFile(filepath.Join(work, "g.txt"), []byte("gone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, work, "add", "g.txt")
	testGit(t, work, "commit", "--quiet", "-m", "gone")
	testGit(t, work, "repack", "-a", "-d", "-q")
	g = testGit(t, work, "rev-parse", "HEAD")
	packs, _ := filepath.Glob(filepath.Join(work, ".git", "objects", "pack", "pack-*.pack"))
	if len(packs) != 1 {
		t.Fatalf("made %d packs, want 1", len(packs))
	}
	dir := filepath.Dir(original)
	for _, src := range []string{packs[0], idxOf(packs[0])} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(src)), data, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	store := f.store(t)
	testGit(t, store, "--git-dir", store, "update-ref", "refs/heads/gone", g)
	return g, original
}

// keptRefs lists the linked store's refs under refs/niwa/kept/.
func keptRefs(t *testing.T, f fixture) []string {
	t.Helper()
	store := f.store(t)
	out := testGit(t, store, "--git-dir", store, "for-each-ref", "--format=%(refname) %(objectname)", keptRefPrefix)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestRestore_PinsACommitOnlyAnOldStoreHeld(t *testing.T) {
	f := built(t)
	g, original := goneCommit(t, f)
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	makeWritable(t, original)
	if err := os.Truncate(original, info.Size()/2); err != nil {
		t.Fatal(err)
	}

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultPackChanged || res[0].Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want pack-changed/restored", res)
	}
	if got := keptRefs(t, f); !slices.Equal(got, []string{keptRefPrefix + g + " " + g}) {
		t.Fatalf("kept refs = %v, want only %s at %s", got, keptRefPrefix+g, g)
	}
	if !f.storeHas(t, g) {
		t.Fatalf("the new store can't read %s", g)
	}
	if got := f.storeRef(t, "refs/heads/gone"); got != "" {
		t.Fatalf("refs/heads/gone reached the new store at %s", got)
	}
}

func TestRestore_PinsNothingFromAStoreGitCantOpenOrAnotherRemote(t *testing.T) {
	t.Run("old store HEAD deleted", func(t *testing.T) {
		f := built(t)
		goneCommit(t, f)
		damageHEAD(t, f)
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeRestored {
			t.Fatalf("result = %+v, want restored", res)
		}
		if got := keptRefs(t, f); len(got) != 0 {
			t.Fatalf("kept refs = %v, want none", got)
		}
	})
	t.Run("url-changed", func(t *testing.T) {
		f := built(t)
		goneCommit(t, f)
		rewriteStateURL(t, f)
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); res[0].Fault != FaultURLChanged || res[0].Outcome != OutcomeRestored {
			t.Fatalf("result = %+v, want url-changed/restored", res)
		}
		if got := keptRefs(t, f); len(got) != 0 {
			t.Fatalf("kept refs = %v, want none", got)
		}
	})
	t.Run("relink", func(t *testing.T) {
		f := built(t)
		goneCommit(t, f)
		target := f.linkTarget(t)
		if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
			t.Fatal(err)
		}
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); res[0].Outcome != OutcomeRestored || f.linkTarget(t) != target {
			t.Fatalf("result = %+v, link %q; want restored by a relink to %q", res, f.linkTarget(t), target)
		}
		if got := keptRefs(t, f); len(got) != 0 {
			t.Fatalf("kept refs = %v, want none", got)
		}
	})
}
