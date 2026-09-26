package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readRawMapping decodes the mapping file for testSessionID as a plain JSON
// object, so a test can tell an absent key from a false one.
func readRawMapping(t *testing.T, root string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".niwa", "sessions", testSessionID+".json"))
	if err != nil {
		t.Fatalf("read raw mapping: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode raw mapping: %v", err)
	}
	return raw
}

// TestSessionMapping_AcceptsSessionMessagesRoundTrip verifies a mapping that
// recorded the behavior writes the key and reads back true.
func TestSessionMapping_AcceptsSessionMessagesRoundTrip(t *testing.T) {
	root := t.TempDir()

	m := SessionMapping{
		SessionID:              testSessionID,
		InstanceName:           "tsuku+inbound-deadbeef",
		InstancePath:           filepath.Join(root, "tsuku+inbound-deadbeef"),
		Ephemeral:              true,
		Origin:                 "dispatch",
		AcceptsSessionMessages: true,
	}
	if err := WriteSessionMapping(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v, ok := readRawMapping(t, root)["accepts_session_messages"]; !ok || v != true {
		t.Errorf("raw mapping accepts_session_messages = %v (present %v), want true", v, ok)
	}
	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.AcceptsSessionMessages {
		t.Error("AcceptsSessionMessages = false after round-trip, want true")
	}
}

// TestSessionMapping_AcceptsSessionMessagesOmittedWhenFalse pins omitempty: a
// mapping where the behavior did not take effect carries no key at all, so it
// is byte-identical to one written before the field existed.
func TestSessionMapping_AcceptsSessionMessagesOmittedWhenFalse(t *testing.T) {
	root := t.TempDir()

	m := SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "tsuku-plain",
		InstancePath: filepath.Join(root, "tsuku-plain"),
		Ephemeral:    true,
		Origin:       "dispatch",
	}
	if err := WriteSessionMapping(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := readRawMapping(t, root)["accepts_session_messages"]; ok {
		t.Fatal("a mapping without the behavior must omit the accepts_session_messages key")
	}
	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.AcceptsSessionMessages {
		t.Error("AcceptsSessionMessages = true for a mapping written without it")
	}
}

// TestSessionMapping_LegacyDecodesAcceptsSessionMessagesFalse verifies a
// mapping written before the field existed decodes as false.
func TestSessionMapping_LegacyDecodesAcceptsSessionMessagesFalse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{
  "session_id": "` + testSessionID + `",
  "instance_name": "tsuku-legacy",
  "instance_path": "/tmp/tsuku-legacy",
  "ephemeral": true,
  "origin": "dispatch",
  "keep_alive": true
}`
	if err := os.WriteFile(filepath.Join(dir, testSessionID+".json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if got.AcceptsSessionMessages {
		t.Error("a legacy mapping must decode AcceptsSessionMessages as false")
	}
	if !got.KeepAlive || got.Origin != "dispatch" {
		t.Errorf("legacy mapping decoded unexpectedly: %+v", got)
	}
}
