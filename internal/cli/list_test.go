package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// seedInstance writes a minimal valid instance directory (with a
// .niwa/instance.json marker) under workspaceRoot and returns its path.
func seedInstance(t *testing.T, workspaceRoot, name string, number int) string {
	t.Helper()
	dir := filepath.Join(workspaceRoot, name)
	stateDir := filepath.Join(dir, ".niwa")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := workspace.InstanceState{
		SchemaVersion:  1,
		InstanceName:   name,
		InstanceNumber: number,
		Root:           dir,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "instance.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestEnumerateInstanceRecords_TwoInstances covers enumeration over a
// fixture workspace with two instances: both are returned, sorted by name,
// with absolute paths and ephemeral=false (no mapping store present).
func TestEnumerateInstanceRecords_TwoInstances(t *testing.T) {
	root := t.TempDir()
	dir1 := seedInstance(t, root, "tsuku", 1)
	dir2 := seedInstance(t, root, "tsuku-2", 2)

	records, err := workspace.EnumerateInstanceRecords(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d: %+v", len(records), records)
	}
	if records[0].Name != "tsuku" || records[0].Path != dir1 {
		t.Errorf("record[0] = %+v, want name=tsuku path=%s", records[0], dir1)
	}
	if records[1].Name != "tsuku-2" || records[1].Path != dir2 {
		t.Errorf("record[1] = %+v, want name=tsuku-2 path=%s", records[1], dir2)
	}
	for _, r := range records {
		if r.Ephemeral {
			t.Errorf("expected ephemeral=false without a mapping store, got true for %s", r.Name)
		}
	}
}

// TestEnumerateInstanceRecords_EphemeralMarker asserts that an instance
// referenced by an ephemeral session mapping is reported ephemeral=true.
func TestEnumerateInstanceRecords_EphemeralMarker(t *testing.T) {
	root := t.TempDir()
	dir1 := seedInstance(t, root, "tsuku", 1)
	seedInstance(t, root, "tsuku-2", 2)

	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mapping := map[string]any{
		"session_id":    "11111111-2222-3333-4444-555555555555",
		"instance_name": "tsuku",
		"instance_path": dir1,
		"ephemeral":     true,
	}
	data, err := json.Marshal(mapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "11111111-2222-3333-4444-555555555555.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	records, err := workspace.EnumerateInstanceRecords(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := map[string]workspace.InstanceRecord{}
	for _, r := range records {
		byName[r.Name] = r
	}
	if !byName["tsuku"].Ephemeral {
		t.Error("expected tsuku to be ephemeral=true")
	}
	if byName["tsuku-2"].Ephemeral {
		t.Error("expected tsuku-2 to be ephemeral=false")
	}
}

// TestListCmd_HasJSONFlag pins the --json flag registration and default.
func TestListCmd_HasJSONFlag(t *testing.T) {
	flag := listCmd.Flags().Lookup("json")
	if flag == nil {
		t.Fatal("expected --json flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %q", flag.DefValue)
	}
}

// TestListCmd_WiredIntoRoot asserts list is registered on the root command.
func TestListCmd_WiredIntoRoot(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "list" {
			return
		}
	}
	t.Fatal("expected 'list' command to be wired into rootCmd")
}

// TestListJSONShape verifies the --json array shape over two instances.
func TestListJSONShape(t *testing.T) {
	records := []workspace.InstanceRecord{
		{Name: "tsuku", Path: "/ws/tsuku", Ephemeral: false},
		{Name: "tsuku-2", Path: "/ws/tsuku-2", Ephemeral: true},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(records); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
	for _, rec := range got {
		for _, k := range []string{"name", "path", "ephemeral"} {
			if _, ok := rec[k]; !ok {
				t.Errorf("missing key %q in %v", k, rec)
			}
		}
	}
	if got[1]["ephemeral"] != true {
		t.Errorf("expected tsuku-2 ephemeral=true, got %v", got[1]["ephemeral"])
	}
}
