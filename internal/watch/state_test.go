package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHandledSet_RoundTripAndMembership(t *testing.T) {
	ws := t.TempDir()

	// Missing file -> empty set, no error.
	set, err := LoadHandledSet(ws)
	if err != nil {
		t.Fatalf("LoadHandledSet on empty: %v", err)
	}
	if len(set) != 0 {
		t.Fatalf("expected empty set, got %d", len(set))
	}

	k := HandledKey("acme", "api", 42)
	if k != "acme/api#42" {
		t.Fatalf("HandledKey = %q", k)
	}
	if err := AppendHandled(ws, k); err != nil {
		t.Fatalf("AppendHandled: %v", err)
	}
	// Idempotent.
	if err := AppendHandled(ws, k); err != nil {
		t.Fatalf("AppendHandled (dup): %v", err)
	}
	if err := AppendHandled(ws, HandledKey("acme", "web", 7)); err != nil {
		t.Fatalf("AppendHandled 2: %v", err)
	}

	set, err = LoadHandledSet(ws)
	if err != nil {
		t.Fatalf("LoadHandledSet: %v", err)
	}
	if !set["acme/api#42"] || !set["acme/web#7"] {
		t.Fatalf("membership missing: %v", set)
	}
	if len(set) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(set), set)
	}
}

func TestHandledSet_MalformedLinesSkipped(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "acme/api#42\n\n  garbage line  \nacme/web#not-a-number\nacme/#7\n/repo#7\nacme/web#7\n"
	if err := os.WriteFile(filepath.Join(ws, handledSetRelPath), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := LoadHandledSet(ws)
	if err != nil {
		t.Fatalf("LoadHandledSet: %v", err)
	}
	if len(set) != 2 || !set["acme/api#42"] || !set["acme/web#7"] {
		t.Fatalf("malformed lines not skipped correctly: %v", set)
	}
}

func TestAppendHandled_RejectsMalformed(t *testing.T) {
	ws := t.TempDir()
	if err := AppendHandled(ws, "not-a-key"); err == nil {
		t.Fatal("expected error on malformed key")
	}
}

func TestStagedRecord_RoundTrip(t *testing.T) {
	ws := t.TempDir()
	rec := StagedRecord{
		Handle:    "abcd1234",
		Owner:     "acme",
		Repo:      "api",
		Number:    42,
		URL:       "https://github.com/acme/api/pull/42",
		DraftPath: filepath.Join(ws, "inst", "watch-review-draft.md"),
	}
	if err := SaveStagedRecord(ws, rec); err != nil {
		t.Fatalf("SaveStagedRecord: %v", err)
	}
	got, err := LoadStagedRecord(ws, "abcd1234")
	if err != nil {
		t.Fatalf("LoadStagedRecord: %v", err)
	}
	if got != rec {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, rec)
	}
	handles, err := ListStagedHandles(ws)
	if err != nil {
		t.Fatalf("ListStagedHandles: %v", err)
	}
	if len(handles) != 1 || handles[0] != "abcd1234" {
		t.Fatalf("ListStagedHandles = %v", handles)
	}
}

func TestStagedRecord_UnsafeHandleRejected(t *testing.T) {
	ws := t.TempDir()
	for _, bad := range []string{"../escape", "a/b", "with space", "", "dots.dots"} {
		if err := SaveStagedRecord(ws, StagedRecord{Handle: bad}); err == nil {
			t.Errorf("SaveStagedRecord accepted unsafe handle %q", bad)
		}
		if _, err := LoadStagedRecord(ws, bad); err == nil {
			t.Errorf("LoadStagedRecord accepted unsafe handle %q", bad)
		}
	}
}

func TestListStagedHandles_MissingDir(t *testing.T) {
	ws := t.TempDir()
	handles, err := ListStagedHandles(ws)
	if err != nil {
		t.Fatalf("ListStagedHandles on missing dir: %v", err)
	}
	if handles != nil {
		t.Fatalf("expected nil, got %v", handles)
	}
}
