package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
)

func authorityInstance(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWriteAuthorityRecord_Writes(t *testing.T) {
	inst := authorityInstance(t)
	rec, err := authority.Resolve(authority.Policy{}, authority.Request{})
	if err != nil {
		t.Fatal(err)
	}
	rec.DispatchID = "3c803591-4c01-486f-a927-f96ec399d6a7"
	if err := WriteAuthorityRecord(inst, rec); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(AuthorityRecordPath(inst))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(AuthorityRecordPath(inst))
	if err != nil {
		t.Fatal(err)
	}
	got, err := authority.ParseRecord(data)
	if err != nil {
		t.Fatalf("the written record doesn't parse: %v", err)
	}
	if got.DispatchID != rec.DispatchID || got.GrantCeilingSource != authority.LayerNone {
		t.Errorf("read back %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Join(inst, StateDir))
	if len(entries) != 1 {
		t.Errorf(".niwa holds %d entries, want only the record (no temp file left)", len(entries))
	}
}

func TestWriteAuthorityRecord_RefusesNewerVersion(t *testing.T) {
	inst := authorityInstance(t)
	rec, err := authority.ParseRecord([]byte(`{"schema":"niwa.authority.record/v2","dispatch_id":"3c803591-4c01-486f-a927-f96ec399d6a7"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteAuthorityRecord(inst, rec); !errors.Is(err, ErrNewerVersionRecord) {
		t.Fatalf("err = %v, want ErrNewerVersionRecord", err)
	}
	entries, _ := os.ReadDir(filepath.Join(inst, StateDir))
	if len(entries) != 0 {
		t.Errorf("something was written: %v", entries)
	}
}
