//go:build unix

package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
}

func assertUnwritable(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnwritable) {
		t.Fatalf("err = %v, want one wrapping ErrUnwritable", err)
	}
}

func assertLoadEmpty(t *testing.T, got map[string]Entry) {
	t.Helper()
	if got == nil || len(got) != 0 {
		t.Fatalf("Load = %v, want an empty map", keysOf(got))
	}
}

func TestModesUnderPermissiveUmask(t *testing.T) {
	dir := isolate(t)
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false)
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("store directory mode = %o, want 700", m)
	}
	if m := mode(t, dataPath(dir, id)); m != 0o600 {
		t.Errorf("data file mode = %o, want 600", m)
	}
	if m := mode(t, lockPath(dir, id)); m != 0o600 {
		t.Errorf("lock file mode = %o, want 600", m)
	}

	// A temp file left behind by an aborted update is 0600 too.
	testHookBeforeRename = func() error { return errors.New("abort") }
	t.Cleanup(func() { testHookBeforeRename = nil })
	_ = Update(dir, id, map[string]Entry{"K": entry("v2", time.Now(), "t")}, nil, false)
	temps, _ := filepath.Glob(filepath.Join(dir, ".*.json.tmp-*"))
	if len(temps) != 1 {
		t.Fatalf("temp files = %v, want 1", temps)
	}
	if m := mode(t, temps[0]); m != 0o600 {
		t.Errorf("temp file mode = %o, want 600", m)
	}
}

func TestLooseDirectoryIsTightened(t *testing.T) {
	for _, op := range []string{"Update", "Load"} {
		t.Run(op, func(t *testing.T) {
			dir := isolate(t)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if op == "Update" {
				mustUpdate(t, dir, testIdentity(), map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false)
			} else {
				mustLoad(t, dir, testIdentity())
			}
			if m := mode(t, dir); m != 0o700 {
				t.Fatalf("store directory mode = %o, want 700", m)
			}
		})
	}
}

func TestSymlinkedStoreDirectoryIsUntrusted(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	real := filepath.Join(t.TempDir(), "real")
	mustUpdate(t, real, id, map[string]Entry{"K": entry("stored", time.Now(), "t")}, nil, false)
	before, err := os.ReadFile(dataPath(real, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, dir); err != nil {
		t.Fatal(err)
	}

	got, err := Load(dir, id)
	assertLoadEmpty(t, got)
	assertUnwritable(t, err)
	assertUnwritable(t, Update(dir, id, map[string]Entry{"K": entry("new", time.Now(), "t")}, nil, false))

	after, err := os.ReadFile(dataPath(real, id))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the symlink's target was written")
	}
}

func TestStoreDirectoryNotOwnedIsUntrusted(t *testing.T) {
	t.Run("other owner", func(t *testing.T) {
		dir := isolate(t)
		id := testIdentity()
		mustUpdate(t, dir, id, map[string]Entry{"K": entry("stored", time.Now(), "t")}, nil, false)
		before, _ := os.ReadFile(dataPath(dir, id))

		effectiveUID = func() int { return os.Geteuid() + 1 }
		t.Cleanup(func() { effectiveUID = os.Geteuid })

		got, err := Load(dir, id)
		assertLoadEmpty(t, got)
		assertUnwritable(t, err)
		assertUnwritable(t, Update(dir, id, map[string]Entry{"K": entry("new", time.Now(), "t")}, nil, false))
		after, _ := os.ReadFile(dataPath(dir, id))
		if !bytes.Equal(before, after) {
			t.Fatal("data file changed in a store the user doesn't own")
		}
	})

	t.Run("real root-owned directory", func(t *testing.T) {
		isolate(t)
		skipAsRoot(t)
		fi, err := os.Stat("/")
		if err != nil {
			t.Skip(err)
		}
		if uid, _ := fileOwner(fi); uid == os.Geteuid() || InWorkTree("/") {
			t.Skip("/ is owned by the test user or sits in a work tree")
		}
		got, err := Load("/", testIdentity())
		assertLoadEmpty(t, got)
		assertUnwritable(t, err)
		assertUnwritable(t, Update("/", testIdentity(), map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false))
	})
}

func TestSymlinkedDataFileIsUntrusted(t *testing.T) {
	for _, where := range []string{"inside the store", "outside the store"} {
		t.Run(where, func(t *testing.T) {
			dir := isolate(t)
			id := testIdentity()
			// Store a valid file for id, then move it aside and point the
			// data file name at it, so reading through the link would
			// return entries.
			mustUpdate(t, dir, id, map[string]Entry{"K": entry("stored", time.Now(), "t")}, nil, false)
			target := filepath.Join(dir, "decoy")
			if where == "outside the store" {
				target = filepath.Join(t.TempDir(), "decoy")
			}
			if err := os.Rename(dataPath(dir, id), target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, dataPath(dir, id)); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(target)

			got, err := Load(dir, id)
			assertLoadEmpty(t, got)
			assertUnwritable(t, err)
			assertUnwritable(t, Update(dir, id, map[string]Entry{"K": entry("new", time.Now(), "t")}, nil, false))

			after, _ := os.ReadFile(target)
			if !bytes.Equal(before, after) {
				t.Fatal("the symlink's target was modified")
			}
			if fi, err := os.Lstat(dataPath(dir, id)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatal("the data file symlink was replaced")
			}
		})
	}
}

func TestSymlinkedLockFileIsUntrusted(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"K": entry("stored", time.Now(), "t")}, nil, false)
	target := filepath.Join(dir, "decoy")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath(dir, id)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lockPath(dir, id)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(dataPath(dir, id))

	got, err := Load(dir, id)
	assertLoadEmpty(t, got)
	assertUnwritable(t, err)
	assertUnwritable(t, Update(dir, id, map[string]Entry{"K": entry("new", time.Now(), "t")}, nil, false))

	if b, _ := os.ReadFile(target); string(b) != "untouched" {
		t.Fatalf("lock symlink target = %q, want untouched", b)
	}
	if after, _ := os.ReadFile(dataPath(dir, id)); !bytes.Equal(before, after) {
		t.Fatal("data file changed through a symlinked lock")
	}
}

func TestDataFileWithLooseModeIsUntrusted(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"K": entry("stored", time.Now(), "t")}, nil, false)
	if err := os.Chmod(dataPath(dir, id), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, id)
	assertLoadEmpty(t, got)
	assertUnwritable(t, err)
	assertUnwritable(t, Update(dir, id, map[string]Entry{"K": entry("new", time.Now(), "t")}, nil, false))
}

func TestUnreadableDataFilesAreEmptyAndReplaced(t *testing.T) {
	id := testIdentity()
	other := testIdentity()
	other.ProjectID = "someone-else"
	_, otherEcho := fileStem(other)

	valid := func(echo map[string]string, value []byte) []byte {
		b, err := jsonMarshal(dataFile{FormatVersion: 1, Identity: echo, Keys: map[string]Entry{"OLD": {Value: value, ResolvedAt: time.Now()}}})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	_, ownEcho := fileStem(id)

	cases := map[string][]byte{
		"invalid JSON":           []byte("{not json"),
		"unknown format version": bytes.Replace(valid(ownEcho, []byte("v")), []byte(`"format_version": 1`), []byte(`"format_version": 2`), 1),
		"identity mismatch":      valid(otherEcho, []byte("v")),
		"larger than 1 MiB":      valid(ownEcho, bytes.Repeat([]byte("a"), maxDataFileSize)),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			dir := isolate(t)
			mustUpdate(t, dir, id, map[string]Entry{"SEED": entry("s", time.Now(), "t")}, nil, false)
			if err := os.WriteFile(dataPath(dir, id), data, 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := Load(dir, id)
			if err != nil {
				t.Fatalf("Load error = %v, want nil", err)
			}
			assertLoadEmpty(t, got)

			mustUpdate(t, dir, id, map[string]Entry{"NEW": entry("n", time.Now(), "t")}, nil, false)
			assertKeys(t, mustLoad(t, dir, id), "NEW")
			raw, _ := os.ReadFile(dataPath(dir, id))
			if !strings.Contains(string(raw), `"format_version": 1`) {
				t.Fatalf("rewritten file is not format 1:\n%s", raw)
			}
		})
	}
}

// jsonMarshal encodes the way Update does.
func jsonMarshal(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

func TestSanityOfOversizeCase(t *testing.T) {
	// The oversize fixture above must really exceed the cap, or that case
	// proves nothing.
	isolate(t)
	b, _ := jsonMarshal(dataFile{FormatVersion: 1, Keys: map[string]Entry{"OLD": {Value: bytes.Repeat([]byte("a"), maxDataFileSize)}}})
	if len(b) <= maxDataFileSize {
		t.Fatalf("fixture is %d bytes, not above the cap", len(b))
	}
}

func TestDataFileWithNoPermissionsIsEmpty(t *testing.T) {
	skipAsRoot(t)
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"K": entry("stored", time.Now(), "t")}, nil, false)
	if err := os.Chmod(dataPath(dir, id), 0o000); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, id)
	if err != nil {
		t.Fatalf("Load error = %v, want nil", err)
	}
	assertLoadEmpty(t, got)
}

func TestUnwritableStoreDirectory(t *testing.T) {
	skipAsRoot(t)
	isolate(t)
	parent := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	dir := filepath.Join(parent, "secret-cache")

	assertUnwritable(t, Update(dir, testIdentity(), map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false))

	// An existing store directory the user can't write refuses too.
	dir2 := filepath.Join(t.TempDir(), "secret-cache")
	if err := os.Mkdir(dir2, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir2, 0o700) })
	assertUnwritable(t, Update(dir2, testIdentity(), map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false))
}

func TestErrorsCarryNoValues(t *testing.T) {
	skipAsRoot(t)
	isolate(t)
	dir := filepath.Join(t.TempDir(), "secret-cache")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	const marker = "store-secret-marker-7f3a"
	err := Update(dir, testIdentity(), map[string]Entry{"K": entry(marker, time.Now(), marker)}, nil, false)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("err = %v; want an error that doesn't carry the value", err)
	}
}
