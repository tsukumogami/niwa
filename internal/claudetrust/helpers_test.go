package claudetrust

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resetLockState restores the package's lock timings, the process-wide
// recorded lock timeouts and the rename hook, now and when the test ends.
// Tests in this package mutate package state, so none of them run in parallel.
func resetLockState(t *testing.T) {
	t.Helper()
	reset := func() {
		lockWait = defaultLockWait
		lockStale = defaultLockStale
		lockPoll = defaultLockPoll
		clearLockTimeouts()
		beforeRename = nil
	}
	reset()
	t.Cleanup(reset)
}

// fastLock shortens the lock wait for tests that expect a skip.
func fastLock(t *testing.T, wait time.Duration) {
	t.Helper()
	lockWait = wait
	lockPoll = 10 * time.Millisecond
}

// fixture is a temp home and an instance root inside the same temp tree.
type fixture struct {
	env  Env
	file string // the config file env resolves to
	root string // an existing instance root
	key  string // Key(root)
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	resetLockState(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(base, "workspace", "inst-1")
	for _, d := range []string{home, root} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := Env{Home: home}
	return fixture{env: env, file: filepath.Join(home, ".claude.json"), root: root, key: mustKey(t, root)}
}

func mustKey(t *testing.T, root string) string {
	t.Helper()
	k, err := Key(root)
	if err != nil {
		t.Fatalf("Key(%s): %v", root, err)
	}
	return k
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

// jsonKey quotes s for embedding as a JSON object key in a fixture.
func jsonKey(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// decodeAny decodes data with numbers kept as their literal text.
func decodeAny(t *testing.T, data []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decoding %s: %v", data, err)
	}
	return v
}

// entryOf returns projects[key] from the config file, and whether it exists.
func entryOf(t *testing.T, path, key string) (map[string]any, bool) {
	t.Helper()
	doc := decodeAny(t, readFile(t, path))
	projects, _ := doc["projects"].(map[string]any)
	e, ok := projects[key]
	if !ok {
		return nil, false
	}
	m, isObj := e.(map[string]any)
	if !isObj {
		t.Fatalf("entry %q is %T, not an object", key, e)
	}
	return m, true
}

// snapshot captures a file's bytes and identity, to prove a call left it alone.
type snapshot struct {
	data []byte
	info os.FileInfo
}

func snap(t *testing.T, path string) snapshot {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot{data: readFile(t, path), info: info}
}

// assertUntouched fails unless path has the same bytes, inode and mtime.
func assertUntouched(t *testing.T, path string, before snapshot) {
	t.Helper()
	after := snap(t, path)
	if !bytes.Equal(before.data, after.data) {
		t.Errorf("%s changed:\nbefore: %s\nafter:  %s", path, before.data, after.data)
	}
	if !os.SameFile(before.info, after.info) {
		t.Errorf("%s was replaced (inode changed)", path)
	}
	if !before.info.ModTime().Equal(after.info.ModTime()) {
		t.Errorf("%s mtime changed: %v -> %v", path, before.info.ModTime(), after.info.ModTime())
	}
}

// assertClean fails if a lock directory or a temp file is left beside path.
func assertClean(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path + lockSuffix); !os.IsNotExist(err) {
		t.Errorf("lock %s left behind (err=%v)", path+lockSuffix, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".niwa-") {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
}

func assertNoHooksKey(t *testing.T, path string) {
	t.Helper()
	if bytes.Contains(readFile(t, path), []byte("hasTrustDialogHooksAccepted")) {
		t.Errorf("%s contains hasTrustDialogHooksAccepted", path)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}
