package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/lineage"
)

const lineageTestID = "0b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d"

func lineageTestEntry() lineage.Entry {
	return lineage.Entry{
		DispatchID:     lineageTestID,
		Instance:       "ws+task-0b9a3c1e",
		Agent:          "claude",
		Origin:         lineage.OriginDispatch,
		WrittenAt:      "2026-10-23T12:00:00Z",
		ParentSession:  lineage.Link{Source: lineage.SourceNone},
		ParentDispatch: lineage.Link{Source: lineage.SourceNone},
		Principal:      lineage.Principal{Source: lineage.SourceNone, Trust: lineage.TrustObserved},
	}
}

func TestCreateLineageEntry_FreshStoreModes(t *testing.T) {
	root := t.TempDir()
	if err := CreateLineageEntry(root, lineageTestEntry()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(LineageDir(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("store dir mode = %o, want 0700", got)
	}
	info, err = os.Stat(filepath.Join(LineageDir(root), lineageTestID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("entry mode = %o, want 0600", got)
	}
	entries, _ := os.ReadDir(LineageDir(root))
	if len(entries) != 1 {
		t.Errorf("store holds %d files, want just the entry (no temporaries)", len(entries))
	}
}

func TestCreateLineageEntry_ReadBack(t *testing.T) {
	root := t.TempDir()
	want := lineageTestEntry()
	if err := CreateLineageEntry(root, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := ReadLineageEntry(root, lineageTestID)
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	want.Schema = lineage.SchemaEntryV1
	if got.Parsed.Entry != want || got.Parsed.LaterVersion {
		t.Errorf("read %+v, want %+v", got.Parsed.Entry, want)
	}
	onDisk, _ := os.ReadFile(filepath.Join(LineageDir(root), lineageTestID+".json"))
	if !bytes.Equal(got.Raw, onDisk) {
		t.Errorf("Raw differs from the stored bytes")
	}
}

func TestCreateLineageEntry_RefusesExisting(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(LineageDir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(LineageDir(root), lineageTestID+".json")
	existing := []byte(`{"schema":"niwa.lineage.entry/v1","dispatch_id":"` + lineageTestID + `","note":"first"}` + "\n")
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	err := CreateLineageEntry(root, lineageTestEntry())
	if !errors.Is(err, ErrLineageEntryExists) {
		t.Fatalf("err = %v, want ErrLineageEntryExists", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, existing) {
		t.Errorf("existing entry changed:\n%s", after)
	}
	entries, _ := os.ReadDir(LineageDir(root))
	if len(entries) != 1 {
		t.Errorf("store holds %d files after a refused create", len(entries))
	}
}

func TestLineageStore_RefusesNonUUIDIDs(t *testing.T) {
	root := t.TempDir()
	e := lineageTestEntry()
	e.DispatchID = "../../etc/passwd"
	if err := CreateLineageEntry(root, e); err == nil {
		t.Error("create accepted a non-UUID id")
	}
	if _, err := os.Stat(LineageDir(root)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused create made the store: %v", err)
	}
	if _, _, err := ReadLineageEntry(root, "../x"); err == nil {
		t.Error("read accepted a non-UUID id")
	}
	if err := RemoveLineageEntry(root, "../x"); err == nil {
		t.Error("remove accepted a non-UUID id")
	}
}

func TestReadLineageEntry_Missing(t *testing.T) {
	_, found, err := ReadLineageEntry(t.TempDir(), lineageTestID)
	if found || err != nil {
		t.Errorf("found %v, err %v; want neither", found, err)
	}
}

func TestReadLineageEntry_Unusable(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":      `{`,
		"another id":     `{"schema":"niwa.lineage.entry/v1","dispatch_id":"1b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d"}`,
		"over the cap":   `{"schema":"niwa.lineage.entry/v1","dispatch_id":"` + lineageTestID + `","pad":"` + strings.Repeat("x", MaxLineageEntryBytes) + `"}`,
		"another schema": `{"schema":"niwa.authority.record/v1","dispatch_id":"` + lineageTestID + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			_ = os.Mkdir(LineageDir(root), 0o700)
			if err := os.WriteFile(filepath.Join(LineageDir(root), lineageTestID+".json"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, found, err := ReadLineageEntry(root, lineageTestID)
			var le *LineageEntryError
			if !found || !errors.As(err, &le) {
				t.Errorf("found %v, err %v; want a *LineageEntryError", found, err)
			}
		})
	}
}

func TestReadLineageEntry_LaterVersion(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(LineageDir(root), 0o700)
	body := `{"schema":"niwa.lineage.entry/v3","dispatch_id":"` + lineageTestID + `","instance":["anything"]}`
	if err := os.WriteFile(filepath.Join(LineageDir(root), lineageTestID+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, found, err := ReadLineageEntry(root, lineageTestID)
	if err != nil || !found || !got.Parsed.LaterVersion || got.Parsed.Entry.Schema != "niwa.lineage.entry/v3" {
		t.Errorf("got %+v, found %v, err %v", got.Parsed, found, err)
	}
}

func TestRemoveLineageEntry(t *testing.T) {
	root := t.TempDir()
	if err := CreateLineageEntry(root, lineageTestEntry()); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLineageEntry(root, lineageTestID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := ReadLineageEntry(root, lineageTestID); found {
		t.Error("entry still there after remove")
	}
	if err := RemoveLineageEntry(root, lineageTestID); err != nil {
		t.Errorf("removing a missing entry: %v", err)
	}
}
