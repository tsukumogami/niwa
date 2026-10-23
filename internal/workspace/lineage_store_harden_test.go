package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/lineage"
)

const lineageOtherID = "1b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d"

// plantEntry writes body as the entry file for id, at mode.
func plantEntry(t *testing.T, root, id, body string, mode os.FileMode) string {
	t.Helper()
	_ = os.Mkdir(LineageDir(root), 0o700)
	path := filepath.Join(LineageDir(root), id+".json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func validEntryBody(t *testing.T, id string) string {
	t.Helper()
	e := lineageTestEntry()
	e.DispatchID = id
	b, err := lineage.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// readWithin reads id, failing the test if the read takes long enough to
// mean it blocked.
func readWithin(t *testing.T, root, id string) (StoredLineageEntry, bool, error) {
	t.Helper()
	type res struct {
		e     StoredLineageEntry
		found bool
		err   error
	}
	ch := make(chan res, 1)
	go func() {
		e, found, err := ReadLineageEntry(root, id)
		ch <- res{e, found, err}
	}()
	select {
	case r := <-ch:
		return r.e, r.found, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("the read hung")
		return StoredLineageEntry{}, false, nil
	}
}

func TestReadLineageEntry_PlantedFiles(t *testing.T) {
	cases := map[string]struct {
		plant func(t *testing.T, path string)
		class string
	}{
		"symlink": {func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "target.json")
			if err := os.WriteFile(target, []byte(validEntryBody(t, lineageTestID)), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, RecordClassSymlink},
		"fifo": {func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
		}, RecordClassNotRegular},
		"directory": {func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}, RecordClassNotRegular},
		"oversized": {func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(strings.Repeat(" ", MaxLineageEntryBytes+1)), 0o600); err != nil {
				t.Fatal(err)
			}
		}, LineageClassTooLarge},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(LineageDir(root), 0o700); err != nil {
				t.Fatal(err)
			}
			c.plant(t, filepath.Join(LineageDir(root), lineageTestID+".json"))
			_, found, err := readWithin(t, root, lineageTestID)
			var le *LineageEntryError
			if !found || !errors.As(err, &le) || le.Class != c.class {
				t.Errorf("found %v, err %v; want class %q", found, err, c.class)
			}
		})
	}
}

func TestReadLineageEntry_UnreadableContent(t *testing.T) {
	good := validEntryBody(t, lineageTestID)
	cases := map[string]struct {
		body  string
		mode  os.FileMode
		class string
	}{
		"group readable":   {good, 0o640, LineageClassWideMode},
		"world readable":   {good, 0o604, LineageClassWideMode},
		"another id":       {validEntryBody(t, lineageOtherID), 0o600, LineageClassOtherDispatch},
		"duplicate member": {strings.Replace(good, `"agent":"claude"`, `"agent":"claude","agent":"codex"`, 1), 0o600, RecordClassMalformed},
		"no schema":        {strings.Replace(good, `"schema":"niwa.lineage.entry/v1",`, "", 1), 0o600, RecordClassMalformed},
		"non-lineage":      {strings.Replace(good, "niwa.lineage.entry/v1", "niwa.authority.record/v1", 1), 0o600, RecordClassMalformed},
		"parent traversal": {strings.Replace(good, `"parent_dispatch":{"id":""`, `"parent_dispatch":{"id":"../../etc/passwd"`, 1), 0o600, RecordClassMalformed},
		"bad written_at":   {strings.Replace(good, "2026-10-23T12:00:00Z", "noon", 1), 0o600, RecordClassMalformed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.body == good && c.mode == 0o600 {
				t.Fatal("fixture is the good entry")
			}
			root := t.TempDir()
			plantEntry(t, root, lineageTestID, c.body, c.mode)
			_, found, err := ReadLineageEntry(root, lineageTestID)
			var le *LineageEntryError
			if !found || !errors.As(err, &le) || le.Class != c.class {
				t.Errorf("found %v, err %v; want class %q", found, err, c.class)
			}
			if err != nil && strings.Contains(err.Error(), "passwd") {
				t.Errorf("error echoes a stored value: %v", err)
			}
		})
	}
}

func TestListLineageEntries_SelectsByName(t *testing.T) {
	root := t.TempDir()
	if err := CreateLineageEntry(root, lineageTestEntry()); err != nil {
		t.Fatal(err)
	}
	dir := LineageDir(root)
	laterID := "2b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d"
	plantEntry(t, root, laterID, `{"schema":"niwa.lineage.entry/v4","dispatch_id":"`+laterID+`"}`, 0o600)
	// A selected name holding a directory, and one holding garbage: each
	// is a per-file error, decided without parsing for the directory.
	if err := os.Mkdir(filepath.Join(dir, lineageOtherID+".json"), 0o700); err != nil {
		t.Fatal(err)
	}
	garbageID := "3b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d"
	plantEntry(t, root, garbageID, "not json", 0o600)
	// Names that are skipped: a temporary, a non-UUID, an upper-case UUID,
	// another extension, and a valid entry under a non-entry name.
	for _, n := range []string{
		"." + lineageTestID + ".json.0123456789abcdef",
		"notes.json",
		strings.ToUpper(lineageTestID) + ".json",
		lineageTestID + ".json.bak",
		"README",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(validEntryBody(t, lineageTestID)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListLineageEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.DispatchID)
	}
	want := []string{lineageTestID, lineageOtherID, laterID, garbageID}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("listed %v, want %v", ids, want)
	}
	if got[0].Err != nil || got[0].Entry.Parsed.Entry.DispatchID != lineageTestID {
		t.Errorf("entry result = %+v", got[0])
	}
	var le *LineageEntryError
	if !errors.As(got[1].Err, &le) || le.Class != RecordClassNotRegular {
		t.Errorf("directory result err = %v", got[1].Err)
	}
	if got[2].Err != nil || !got[2].Entry.Parsed.LaterVersion {
		t.Errorf("later-version result = %+v", got[2])
	}
	if !errors.As(got[3].Err, &le) || le.Class != RecordClassMalformed {
		t.Errorf("garbage result err = %v", got[3].Err)
	}
}

func TestListLineageEntries_NoStore(t *testing.T) {
	got, err := ListLineageEntries(t.TempDir())
	if err != nil || got != nil {
		t.Errorf("got %v, err %v", got, err)
	}
}

func TestLineageStore_WritesRefuseAnUnsafeDirectory(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := t.TempDir()
		if err := os.Symlink(target, LineageDir(root)); err != nil {
			t.Fatal(err)
		}
		if err := CreateLineageEntry(root, lineageTestEntry()); err == nil {
			t.Error("wrote through a symlinked store")
		}
		if names, _ := os.ReadDir(target); len(names) != 0 {
			t.Errorf("the symlink's target holds %v", names)
		}
	})
	t.Run("file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(LineageDir(root), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := CreateLineageEntry(root, lineageTestEntry()); err == nil {
			t.Error("wrote into a store that is a file")
		}
	})
}

func TestLineageStore_WriteTightensAWideDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(LineageDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(LineageDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLineageEntry(root, lineageTestEntry()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(LineageDir(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("store mode after a write = %o, want 0700", got)
	}
}

func TestLineageStore_FailedTighteningRefusesTheWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(LineageDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := lineageDirChmod
	lineageDirChmod = func(*os.File, os.FileMode) error { return syscall.EPERM }
	t.Cleanup(func() { lineageDirChmod = prev })
	if err := CreateLineageEntry(root, lineageTestEntry()); err == nil {
		t.Fatal("the write went ahead without tightening the store")
	}
	if names, _ := os.ReadDir(LineageDir(root)); len(names) != 0 {
		t.Errorf("the store holds %v", names)
	}
}

func TestLineageStore_ReadsLeaveTheDirectoryMode(t *testing.T) {
	root := t.TempDir()
	plantEntry(t, root, lineageTestID, validEntryBody(t, lineageTestID), 0o600)
	if err := os.Chmod(LineageDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadLineageEntry(root, lineageTestID); err != nil {
		t.Fatal(err)
	}
	if _, err := ListLineageEntries(root); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(LineageDir(root))
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("store mode after reads = %o, want it left at 0755", got)
	}
}
