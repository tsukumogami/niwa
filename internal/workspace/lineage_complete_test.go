package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tsukumogami/niwa/internal/lineage"
)

const lineageTestSession = "c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0"

func TestCompleteLineageEntry_InsertsTheSessionID(t *testing.T) {
	root := t.TempDir()
	if err := CreateLineageEntry(root, lineageTestEntry()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(LineageDir(root), lineageTestID+".json"))
	if err := CompleteLineageEntry(root, lineageTestID, lineageTestSession); err != nil {
		t.Fatal(err)
	}
	got, found, err := ReadLineageEntry(root, lineageTestID)
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	want := lineageTestEntry()
	want.Schema = lineage.SchemaEntryV1
	want.SessionID = lineageTestSession
	if got.Parsed.Entry != want {
		t.Errorf("entry = %+v\nwant    %+v", got.Parsed.Entry, want)
	}
	// Only the session id was added.
	wantRaw := bytes.Replace(before, []byte(`"written_at":"2026-10-23T12:00:00Z"`),
		[]byte(`"written_at":"2026-10-23T12:00:00Z","session_id":"`+lineageTestSession+`"`), 1)
	if !bytes.Equal(got.Raw, wantRaw) {
		t.Errorf("raw =\n%s\nwant\n%s", got.Raw, wantRaw)
	}
	info, _ := os.Stat(filepath.Join(LineageDir(root), lineageTestID+".json"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("entry mode = %o", info.Mode().Perm())
	}
	if names, _ := os.ReadDir(LineageDir(root)); len(names) != 1 {
		t.Errorf("store holds %d files, want just the entry", len(names))
	}
}

func TestCompleteLineageEntry_KeepsUnknownMembers(t *testing.T) {
	root := t.TempDir()
	good := strings.TrimSuffix(validEntryBody(t, lineageTestID), "}\n")
	unknown := `"added_later":{"z": [1, 2],  "a": "<&>"}`
	plantEntry(t, root, lineageTestID, good+","+unknown+"}\n", 0o600)
	if err := CompleteLineageEntry(root, lineageTestID, lineageTestSession); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(LineageDir(root), lineageTestID+".json"))
	if !bytes.Contains(after, []byte(unknown)) {
		t.Errorf("unknown member not kept byte for byte:\n%s", after)
	}
	got, _, err := ReadLineageEntry(root, lineageTestID)
	if err != nil || got.Parsed.Entry.SessionID != lineageTestSession {
		t.Errorf("entry %+v, err %v", got.Parsed.Entry, err)
	}
}

func TestCompleteLineageEntry_RefusalsLeaveTheEntry(t *testing.T) {
	withID := lineageTestEntry()
	withID.SessionID = lineageTestSession
	recorded, _ := lineage.Marshal(withID)
	good := validEntryBody(t, lineageTestID)
	cases := map[string]struct {
		body string
		want error
	}{
		"already recorded": {string(recorded) + "\n", lineage.ErrSessionIDRecorded},
		"later version":    {`{"schema":"niwa.lineage.entry/v2","dispatch_id":"` + lineageTestID + `"}`, lineage.ErrLaterVersion},
		"would exceed the cap": {strings.TrimSuffix(good, "}\n") + `,"pad":"` +
			strings.Repeat("x", MaxLineageEntryBytes-len(good)-20) + `"}` + "\n", ErrLineageEntryTooLarge},
		"unreadable": {"not json", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := plantEntry(t, root, lineageTestID, c.body, 0o600)
			if len(c.body) > MaxLineageEntryBytes {
				t.Fatalf("fixture is %d bytes, over the read cap", len(c.body))
			}
			err := CompleteLineageEntry(root, lineageTestID, lineageTestSession)
			if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
			after, _ := os.ReadFile(path)
			if string(after) != c.body {
				t.Errorf("entry changed:\n%s", after)
			}
			if names, _ := os.ReadDir(LineageDir(root)); len(names) != 1 {
				t.Errorf("store holds %d files", len(names))
			}
		})
	}
}

func TestCompleteLineageEntry_FailedWriteLeavesTheEntry(t *testing.T) {
	root := t.TempDir()
	if err := CreateLineageEntry(root, lineageTestEntry()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(LineageDir(root), lineageTestID+".json")
	before, _ := os.ReadFile(path)
	prev := lineageDirChmod
	lineageDirChmod = func(*os.File, os.FileMode) error { return syscall.EPERM }
	t.Cleanup(func() { lineageDirChmod = prev })
	if err := CompleteLineageEntry(root, lineageTestID, lineageTestSession); err == nil {
		t.Fatal("completion succeeded")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Errorf("entry changed:\n%s", after)
	}
}

func TestCompleteLineageEntry_MissingOrBadID(t *testing.T) {
	root := t.TempDir()
	if err := CompleteLineageEntry(root, lineageTestID, lineageTestSession); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing entry: err = %v", err)
	}
	if err := CompleteLineageEntry(root, "../../etc/passwd", lineageTestSession); err == nil {
		t.Error("accepted a non-UUID id")
	}
}
