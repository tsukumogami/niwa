package mirror

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRecordURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:tok@host/o/r.git?x=1#f": "https://host/o/r.git",
		"https://host/o/r.git":                "https://host/o/r.git",
		"ssh://git@host/o/r.git":              "ssh://host/o/r.git",
		"git@host:o/r.git":                    "host:o/r.git",
		"file:///srv/git/r.git":               "file:///srv/git/r.git",
		"/srv/git/r.git":                      "/srv/git/r.git",
	} {
		if got := recordURL(in); got != want {
			t.Errorf("recordURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// applyOpts are the fixture's options as niwa apply passes them.
func (f fixture) applyOpts() Options {
	opts := f.opts()
	opts.Command = "apply"
	return opts
}

// built returns a fixture whose mirror one EnsureHealthy has built.
func built(t testing.TB) fixture {
	t.Helper()
	f := newFixture(t)
	f.borrow(t)
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("building the mirror: %+v", res)
	}
	return f
}

// linkTarget reads the stable link.
func (f fixture) linkTarget(t testing.TB) string {
	t.Helper()
	target, err := os.Readlink(StablePath(f.home, f.root, "app"))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// store is the directory the stable link names.
func (f fixture) store(t testing.TB) string {
	t.Helper()
	return filepath.Join(f.mdir(), f.linkTarget(t))
}

// packs lists the .pack files in the linked store.
func (f fixture) packs(t *testing.T) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(f.store(t), "objects", "pack", "*.pack"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	return found
}

// logLines decodes the mirror log.
func (f fixture) logLines(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(logPath(f.mdir()))
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("log line %q is not one JSON object: %v", sc.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestBuild_WritesTheStateFileAndManifest(t *testing.T) {
	f := built(t)
	meta := metaDir(f.mdir())
	if info, err := os.Lstat(meta); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf(".meta = %v (%v), want a 0700 directory", info, err)
	}
	info, err := os.Lstat(statePath(f.mdir(), "app"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file = %v (%v), want a 0600 file", info, err)
	}
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if st.Store != f.linkTarget(t) {
		t.Fatalf("state store = %q, want the link target %q", st.Store, f.linkTarget(t))
	}
	if st.URL != f.repo().URL {
		t.Fatalf("state url = %q, want %q", st.URL, f.repo().URL)
	}
	packs := f.packs(t)
	if len(packs) == 0 || len(st.Packs) != len(packs) {
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
	entries, err := os.ReadDir(meta)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !metaEntryAllowed(e.Name()) {
			t.Fatalf(".meta holds %q", e.Name())
		}
	}
	for _, name := range []string{"mirror.log.1", "app.json", "app.stores"} {
		if !metaEntryAllowed(name) {
			t.Fatalf("the .meta listing refuses %q", name)
		}
	}
	for _, name := range []string{"mirror.log.2", "mirror.log.1.bak", ".app.json-1234abcd", ".app.stores-1234abcd", "other.json", "notes.txt"} {
		if metaEntryAllowed(name) {
			t.Fatalf("the .meta listing accepts %q", name)
		}
	}
}

// metaEntryAllowed reports whether name may sit in the fixture's .meta/: its
// one repo's state file and store record, the mirror log or the log's one
// older generation.
func metaEntryAllowed(name string) bool {
	return name == "app.json" || name == "app.stores" || name == "mirror.log" || name == "mirror.log.1"
}

func TestLog_OneCheckLinePerCallAndOneRestoreLinePerRestore(t *testing.T) {
	f := built(t)
	info, err := os.Lstat(logPath(f.mdir()))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mirror.log = %v (%v), want 0600", info, err)
	}
	lines := f.logLines(t)
	if len(lines) != 2 || lines[0]["event"] != "check" || lines[1]["event"] != "restore" {
		t.Fatalf("log after a build = %v, want a check and a restore line", lines)
	}
	for _, line := range lines {
		for _, key := range []string{"time", "command", "mirror", "event", "fault", "result", "check_ms"} {
			if _, ok := line[key]; !ok {
				t.Fatalf("log line %v has no %q", line, key)
			}
		}
		if line["mirror"] != "app" || line["command"] != "apply" || line["fault"] != "missing" || line["result"] != "restored" {
			t.Fatalf("log line %v", line)
		}
	}
	if _, ok := lines[0]["took_ms"]; ok {
		t.Fatal("a check line carries took_ms")
	}
	if _, ok := lines[1]["took_ms"]; !ok {
		t.Fatal("a restore line has no took_ms")
	}

	before, err := os.ReadFile(statePath(f.mdir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	target := f.linkTarget(t)
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("second call = %+v, want healthy", res)
	}
	lines = f.logLines(t)
	if len(lines) != 3 {
		t.Fatalf("log after a healthy check holds %d lines, want 3", len(lines))
	}
	last := lines[2]
	if last["event"] != "check" || last["fault"] != "" || last["result"] != "healthy" {
		t.Fatalf("healthy check line = %v", last)
	}
	after, err := os.ReadFile(statePath(f.mdir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || f.linkTarget(t) != target {
		t.Fatal("a healthy check changed the state file or the link")
	}
}

func TestLog_AFailedWriteLeavesTheResult(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	if err := ensureLayout(f.home, f.root); err != nil {
		t.Fatal(err)
	}
	// A directory where the log goes makes every write fail.
	if err := os.Mkdir(logPath(f.mdir()), 0o700); err != nil {
		t.Fatal(err)
	}
	res := EnsureHealthy(context.Background(), f.opts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("result = %+v, want restored with no error", res)
	}
	if res[0].LogErr == nil {
		t.Fatal("a log that can't be written left no LogErr on the result")
	}
	if info, err := os.Lstat(logPath(f.mdir())); err != nil || !info.IsDir() {
		t.Fatalf("the directory at the log's path was moved or removed: %v (%v)", info, err)
	}

	// A healthy mirror, with and without the refresh, keeps the failure too.
	for _, opts := range []Options{f.opts(), func() Options { o := f.opts(); o.Refresh = true; return o }()} {
		res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
		if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil || res[0].LogErr == nil {
			t.Fatalf("healthy result with Refresh %v = %+v, want healthy, no Err, LogErr set", opts.Refresh, res)
		}
	}
	if info, err := os.Lstat(logPath(f.mdir())); err != nil || !info.IsDir() {
		t.Fatalf("the directory at the log's path was moved or removed: %v (%v)", info, err)
	}
}

func TestMirrored_KeepsARepoWithOnlyAStateFile(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(metaDir(f.mdir()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(f.mdir(), "app"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Mirrored(f.home, f.root, nil, []Repo{f.repo()}); len(got) != 1 || got[0].ID != "app" {
		t.Fatalf("Mirrored = %v, want app kept for its state file", got)
	}
}
