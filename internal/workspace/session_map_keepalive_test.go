package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSessionMapping_KeepAliveRoundTrip verifies the additive KeepAlive field
// persists and reads back unchanged (the Origin round-trip mirror).
func TestSessionMapping_KeepAliveRoundTrip(t *testing.T) {
	root := t.TempDir()

	m := SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "tsuku+ka-deadbeef",
		InstancePath: filepath.Join(root, "tsuku+ka-deadbeef"),
		Ephemeral:    true,
		Origin:       "dispatch",
		KeepAlive:    true,
	}
	if err := WriteSessionMapping(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.KeepAlive {
		t.Errorf("KeepAlive = false after round-trip, want true")
	}
}

// TestSessionMapping_KeepAliveOmittedWhenFalse pins the omitempty discipline:
// a non-opted mapping's JSON carries no "keep_alive" key at all, so mappings
// written by this niwa are byte-identical to pre-keep-alive ones -- and a
// legacy mapping (no key) decodes to KeepAlive == false.
func TestSessionMapping_KeepAliveOmittedWhenFalse(t *testing.T) {
	root := t.TempDir()

	m := SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "tsuku-plain",
		InstancePath: filepath.Join(root, "tsuku-plain"),
		Ephemeral:    true,
	}
	if err := WriteSessionMapping(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".niwa", "sessions", testSessionID+".json"))
	if err != nil {
		t.Fatalf("read raw mapping: %v", err)
	}
	if bytes.Contains(data, []byte("keep_alive")) {
		t.Fatalf("non-opted mapping must omit the keep_alive key entirely, got:\n%s", data)
	}

	got, err := ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.KeepAlive {
		t.Errorf("a mapping with no keep_alive key must decode to false")
	}
}
