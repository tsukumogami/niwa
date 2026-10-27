package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testLaunchDispatchID = "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d"

func TestLaunchRecord_WriteReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	rec := LaunchRecord{
		DispatchID:     testLaunchDispatchID,
		SessionID:      "abcdef12-3456-7890-abcd-ef1234567890",
		Agent:          "claude",
		InstanceName:   "ws+fix-0123abcd",
		PermissionMode: "acceptEdits",
		SessionName:    "fix-0123abcd",
		Model:          "opus",
		Settings:       `{"env":{"NIWA_REAP_KEY":"x"}}`,
		Env:            map[string]string{"NIWA_REAP_KEY": testLaunchDispatchID, "OTEL_RESOURCE_ATTRIBUTES": "a=b", "PASS_MARK": "carried"},
	}
	if err := WriteLaunchRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLaunchRecord(root, testLaunchDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != LaunchRecordSchema || got.WrittenAt.IsZero() {
		t.Errorf("schema %q, written_at %v", got.Schema, got.WrittenAt)
	}
	got.Schema, got.WrittenAt = "", rec.WrittenAt
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(rec)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("read back\n%s\nwant\n%s", gotJSON, wantJSON)
	}

	dirInfo, err := os.Stat(LaunchDir(root))
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("store mode = %o, want 700", mode)
	}
	fileInfo, err := os.Stat(filepath.Join(LaunchDir(root), testLaunchDispatchID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("record mode = %o, want 600", mode)
	}
}

func TestLaunchRecord_TightensAnExistingStore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(LaunchDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteLaunchRecord(root, LaunchRecord{DispatchID: testLaunchDispatchID}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(LaunchDir(root))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("store mode = %o, want 700", mode)
	}
}

func TestLaunchRecord_LeavesCredentialValuesOut(t *testing.T) {
	root := t.TempDir()
	rec := LaunchRecord{
		DispatchID: testLaunchDispatchID,
		Env: map[string]string{
			"GH_TOKEN":                 "secret-1",
			"AWS_REGION":               "secret-2",
			"DB_PASSWORD":              "secret-3",
			"CLAUDE_CODE_X":            "secret-4",
			"PASS_MARK":                "carried",
			"NIWA_REAP_KEY":            testLaunchDispatchID,
			"OTEL_RESOURCE_ATTRIBUTES": "a=b",
		},
	}
	if err := WriteLaunchRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(LaunchDir(root), testLaunchDispatchID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-1", "secret-2", "secret-3", "secret-4", "GH_TOKEN"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("record holds %q:\n%s", secret, data)
		}
	}
	got, err := ReadLaunchRecord(root, testLaunchDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PASS_MARK", "NIWA_REAP_KEY", "OTEL_RESOURCE_ATTRIBUTES"} {
		if _, ok := got.Env[name]; !ok {
			t.Errorf("record lost %s", name)
		}
	}
}

func TestLaunchRecord_RefusesAnInvalidDispatchIDBeforeAnyPath(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"", "../escape", "0B1C2D3E-4F50-4A6B-8C7D-9E0F1A2B3C4D", "not-a-uuid", testLaunchDispatchID + "/x"} {
		if err := WriteLaunchRecord(root, LaunchRecord{DispatchID: id}); !errors.Is(err, errLaunchDispatchID) {
			t.Errorf("write %q: err = %v", id, err)
		}
		if _, err := ReadLaunchRecord(root, id); !errors.Is(err, errLaunchDispatchID) {
			t.Errorf("read %q: err = %v", id, err)
		}
		if err := RemoveLaunchRecord(root, id); !errors.Is(err, errLaunchDispatchID) {
			t.Errorf("remove %q: err = %v", id, err)
		}
	}
	if _, err := os.Stat(LaunchDir(root)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused write created the store: %v", err)
	}
}

func TestLaunchRecord_WriteIsAtomic(t *testing.T) {
	root := t.TempDir()
	first := LaunchRecord{DispatchID: testLaunchDispatchID, Model: "opus"}
	if err := WriteLaunchRecord(root, first); err != nil {
		t.Fatal(err)
	}
	// A write that cannot rename over the target leaves the old record whole
	// and no temporary file behind.
	target := filepath.Join(LaunchDir(root), testLaunchDispatchID+".json")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteLaunchRecord(root, LaunchRecord{DispatchID: testLaunchDispatchID, Model: "sonnet"}); err == nil {
		t.Fatal("rename over a non-empty directory succeeded")
	}
	entries, err := os.ReadDir(LaunchDir(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestLaunchRecord_ListReportsUnreadableFiles(t *testing.T) {
	root := t.TempDir()
	if entries, err := ListLaunchRecords(root); err != nil || len(entries) != 0 {
		t.Fatalf("missing store: %v, %v", entries, err)
	}
	if err := WriteLaunchRecord(root, LaunchRecord{DispatchID: testLaunchDispatchID}); err != nil {
		t.Fatal(err)
	}
	dir := LaunchDir(root)
	other := "1b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d"
	files := map[string]string{
		other + ".json":  "{not json",
		"not-an-id.json": "{}",
		"2b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d.json": `{"schema":"niwa.launch/v1","dispatch_id":"` + testLaunchDispatchID + `"}`,
		testLaunchDispatchID + ".1.tmp":             "partial",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := ListLaunchRecords(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name] = e.Err == nil
	}
	want := map[string]bool{
		testLaunchDispatchID + ".json":              true,
		other + ".json":                             false,
		"not-an-id.json":                            false,
		"2b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d.json": false,
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for name, ok := range want {
		if v, present := got[name]; !present || v != ok {
			t.Errorf("%s: listed %v, readable %v; want readable %v", name, present, v, ok)
		}
	}
}

func TestLaunchRecord_RemoveIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := WriteLaunchRecord(root, LaunchRecord{DispatchID: testLaunchDispatchID}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := RemoveLaunchRecord(root, testLaunchDispatchID); err != nil {
			t.Fatalf("remove %d: %v", i, err)
		}
	}
	if _, err := ReadLaunchRecord(root, testLaunchDispatchID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("read after remove: %v", err)
	}
}

func TestLaunchRecordCheckedRead(t *testing.T) {
	root := t.TempDir()
	rec := LaunchRecord{DispatchID: testLaunchDispatchID, SessionID: "s", Agent: "claude", InstanceName: "i"}
	if err := WriteLaunchRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	if err := CheckLaunchStore(root); err != nil {
		t.Fatalf("CheckLaunchStore on a store WriteLaunchRecord made: %v", err)
	}
	if got, err := ReadLaunchRecordChecked(root, testLaunchDispatchID); err != nil || got.SessionID != "s" {
		t.Fatalf("ReadLaunchRecordChecked = %+v, %v", got, err)
	}

	path := filepath.Join(LaunchDir(root), testLaunchDispatchID+".json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLaunchRecordChecked(root, testLaunchDispatchID); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("a 0644 record was read: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	moved := filepath.Join(t.TempDir(), "rec.json")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLaunchRecordChecked(root, testLaunchDispatchID); err == nil {
		t.Error("a symlinked record was read")
	}

	if err := os.Chmod(LaunchDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckLaunchStore(root); err == nil {
		t.Error("a 0755 store passed")
	}

	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "real"), LaunchDir(other)); err != nil {
		t.Fatal(err)
	}
	if err := CheckLaunchStore(other); err == nil {
		t.Error("a symlinked store passed")
	}
}
