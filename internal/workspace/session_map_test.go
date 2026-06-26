package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testSessionID = "11111111-2222-3333-4444-555555555555"

func TestSessionMapping_RoundTrip(t *testing.T) {
	root := t.TempDir()

	m := SessionMapping{
		SessionID:      testSessionID,
		InstanceName:   "tsuku-111111112222",
		InstancePath:   filepath.Join(root, "tsuku-111111112222"),
		TranscriptPath: "/home/u/.claude/transcript.jsonl",
		Ephemeral:      true,
		Label:          "fix the parser",
	}

	if err := WriteSessionMapping(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The mapping file lands at .niwa/sessions/<id>.json.
	want := filepath.Join(root, ".niwa", "sessions", testSessionID+".json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected mapping at %s: %v", want, err)
	}

	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SessionID != m.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, m.SessionID)
	}
	if got.InstanceName != m.InstanceName {
		t.Errorf("InstanceName = %q, want %q", got.InstanceName, m.InstanceName)
	}
	if got.InstancePath != m.InstancePath {
		t.Errorf("InstancePath = %q, want %q", got.InstancePath, m.InstancePath)
	}
	if got.TranscriptPath != m.TranscriptPath {
		t.Errorf("TranscriptPath = %q, want %q", got.TranscriptPath, m.TranscriptPath)
	}
	if !got.Ephemeral {
		t.Error("expected Ephemeral=true")
	}
	if got.Label != m.Label {
		t.Errorf("Label = %q, want %q", got.Label, m.Label)
	}
	if got.Created.IsZero() {
		t.Error("expected Created to be stamped, got zero time")
	}

	if err := DeleteSessionMapping(root, testSessionID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("expected mapping removed, stat err = %v", err)
	}

	// Reading a deleted mapping is an error.
	if _, err := ReadSessionMapping(root, testSessionID); err == nil {
		t.Error("expected error reading deleted mapping, got nil")
	}
}

func TestSessionMapping_CreatedPreserved(t *testing.T) {
	root := t.TempDir()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := SessionMapping{SessionID: testSessionID, Created: created, Ephemeral: true}
	if err := WriteSessionMapping(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.Created.Equal(created) {
		t.Errorf("Created = %v, want %v", got.Created, created)
	}
}

func TestSessionMapping_RejectsMalformedID(t *testing.T) {
	root := t.TempDir()

	bad := []string{
		"",
		"not-a-uuid",
		"11111111-2222-3333-4444-55555555555",       // too short tail
		"11111111-2222-3333-4444-5555555555555",     // too long tail
		"../../etc/passwd",                          // path traversal
		"11111111222233334444555555555555",          // missing hyphens
		"GGGGGGGG-2222-3333-4444-555555555555",      // non-hex
		"11111111-2222-3333-4444-555555555555.json", // trailing junk
	}

	for _, id := range bad {
		t.Run(id, func(t *testing.T) {
			if ValidSessionID(id) {
				t.Errorf("ValidSessionID(%q) = true, want false", id)
			}
			if err := WriteSessionMapping(root, SessionMapping{SessionID: id, Ephemeral: true}); err == nil {
				t.Errorf("WriteSessionMapping(%q) = nil err, want rejection", id)
			}
			if _, err := ReadSessionMapping(root, id); err == nil {
				t.Errorf("ReadSessionMapping(%q) = nil err, want rejection", id)
			}
			if err := DeleteSessionMapping(root, id); err == nil {
				t.Errorf("DeleteSessionMapping(%q) = nil err, want rejection", id)
			}
		})
	}

	// No mapping files should have been written for any rejected id.
	sessions := filepath.Join(root, ".niwa", "sessions")
	if entries, err := os.ReadDir(sessions); err == nil {
		for _, e := range entries {
			t.Errorf("unexpected file written for rejected id: %s", e.Name())
		}
	}
}

func TestDeleteSessionMapping_MissingIsNoError(t *testing.T) {
	root := t.TempDir()
	if err := DeleteSessionMapping(root, testSessionID); err != nil {
		t.Errorf("deleting a missing mapping should be a no-op, got %v", err)
	}
}

// TestSessionMapping_OriginRoundTrip verifies the additive Origin field
// persists and reads back unchanged.
func TestSessionMapping_OriginRoundTrip(t *testing.T) {
	root := t.TempDir()

	m := SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "tsuku-disp-deadbeef",
		InstancePath: filepath.Join(root, "tsuku-disp-deadbeef"),
		Ephemeral:    true,
		Origin:       "dispatch",
	}
	if err := WriteSessionMapping(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Origin != "dispatch" {
		t.Errorf("Origin = %q, want %q", got.Origin, "dispatch")
	}
}

// TestSessionMapping_LegacyDecodesEmptyOrigin verifies a mapping JSON written
// before the Origin field existed (no "origin" key) decodes with Origin == ""
// and is otherwise intact -- the back-compat guarantee.
func TestSessionMapping_LegacyDecodesEmptyOrigin(t *testing.T) {
	root := t.TempDir()

	// Hand-write a legacy mapping with no "origin" key, mirroring what an
	// older niwa or the hook path would have produced.
	dir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{
  "session_id": "` + testSessionID + `",
  "instance_name": "tsuku-legacy",
  "instance_path": "/tmp/tsuku-legacy",
  "ephemeral": true
}`
	if err := os.WriteFile(filepath.Join(dir, testSessionID+".json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if got.Origin != "" {
		t.Errorf("legacy Origin = %q, want empty", got.Origin)
	}
	if got.SessionID != testSessionID || got.InstanceName != "tsuku-legacy" || !got.Ephemeral {
		t.Errorf("legacy mapping decoded unexpectedly: %+v", got)
	}

	// And it still round-trips: re-writing and re-reading preserves the
	// (empty) Origin without emitting an "origin" key.
	if err := WriteSessionMapping(root, got); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, testSessionID+".json"))
	if err != nil {
		t.Fatalf("reread file: %v", err)
	}
	if strings.Contains(string(data), "origin") {
		t.Errorf("rewritten legacy mapping should omit the origin key, got:\n%s", data)
	}
}
