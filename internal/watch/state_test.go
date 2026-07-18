package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandledKey_Shapes(t *testing.T) {
	if got := HandledIdentity("acme", "api", 42); got != "acme/api#42" {
		t.Fatalf("HandledIdentity = %q", got)
	}
	if got := HandledKey("acme", "api", 42, "a1b2c3d"); got != "acme/api#42@a1b2c3d" {
		t.Fatalf("HandledKey with sha = %q", got)
	}
	if got := HandledKey("acme", "api", 42, ""); got != "acme/api#42" {
		t.Fatalf("HandledKey empty sha = %q", got)
	}
}

func TestHandledSet_SHAAwareRoundTrip(t *testing.T) {
	ws := t.TempDir()

	// Missing file -> empty set, default level semantics, no error.
	set, err := LoadHandledSet(ws)
	if err != nil {
		t.Fatalf("LoadHandledSet on empty: %v", err)
	}
	if len(set) != 0 {
		t.Fatalf("expected empty set, got %d", len(set))
	}
	if sem, err := LoadTriggerSemantics(ws); err != nil || sem != SemanticsLevel {
		t.Fatalf("default semantics = %q, err %v", sem, err)
	}

	if err := AppendHandled(ws, "acme", "api", 42, "a1b2c3d4e5f6a7b8"); err != nil {
		t.Fatalf("AppendHandled: %v", err)
	}
	// Idempotent at the same SHA.
	if err := AppendHandled(ws, "acme", "api", 42, "a1b2c3d4e5f6a7b8"); err != nil {
		t.Fatalf("AppendHandled (dup): %v", err)
	}
	if err := AppendHandled(ws, "acme", "web", 7, "0123456789abcdef"); err != nil {
		t.Fatalf("AppendHandled 2: %v", err)
	}

	set, err = LoadHandledSet(ws)
	if err != nil {
		t.Fatalf("LoadHandledSet: %v", err)
	}
	if set["acme/api#42"] != "a1b2c3d4e5f6a7b8" || set["acme/web#7"] != "0123456789abcdef" {
		t.Fatalf("SHA round trip mismatch: %v", set)
	}
	if len(set) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(set), set)
	}

	// The header round-trips: the declared semantics survive an append.
	if sem, err := LoadTriggerSemantics(ws); err != nil || sem != SemanticsLevel {
		t.Fatalf("semantics after append = %q, err %v", sem, err)
	}
	// The compatibility membership projection.
	mem := HandledMembership(set)
	if !mem["acme/api#42"] || !mem["acme/web#7"] || len(mem) != 2 {
		t.Fatalf("membership projection = %v", mem)
	}
}

func TestAppendHandled_UpdatesSHAForward(t *testing.T) {
	ws := t.TempDir()
	if err := AppendHandled(ws, "acme", "api", 42, "aaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHandled(ws, "acme", "api", 42, "bbbbbbb"); err != nil {
		t.Fatal(err)
	}
	set, err := LoadHandledSet(ws)
	if err != nil {
		t.Fatal(err)
	}
	if set["acme/api#42"] != "bbbbbbb" {
		t.Fatalf("expected SHA moved forward to bbbbbbb, got %q", set["acme/api#42"])
	}
	if len(set) != 1 {
		t.Fatalf("expected 1 identity (no duplicate line), got %d: %v", len(set), set)
	}
	// The on-disk file carries exactly one data line for the identity: an update
	// rewrites in place rather than appending a second line.
	data, err := os.ReadFile(filepath.Join(ws, handledSetRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "acme/api#42"); n != 1 {
		t.Fatalf("expected 1 line for identity, found %d in:\n%s", n, data)
	}
}

func TestHandledSet_LegacyLineIsUnknownSHA(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy file: no header, SHA-less lines.
	content := "acme/api#42\nacme/web#7\n"
	if err := os.WriteFile(filepath.Join(ws, handledSetRelPath), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := LoadHandledSet(ws)
	if err != nil {
		t.Fatalf("LoadHandledSet: %v", err)
	}
	if len(set) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(set), set)
	}
	for id, sha := range set {
		if sha != "" {
			t.Fatalf("legacy entry %q should be unknown-SHA, got %q", id, sha)
		}
	}
	// A legacy file with no header reports the default (level) semantics.
	if sem, err := LoadTriggerSemantics(ws); err != nil || sem != SemanticsLevel {
		t.Fatalf("legacy semantics = %q, err %v", sem, err)
	}
}

func TestHandledSet_LegacyEntryPreservedOnAppend(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy unknown-SHA entry for a DIFFERENT PR than the one we append.
	if err := os.WriteFile(filepath.Join(ws, handledSetRelPath), []byte("legacy/repo#1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendHandled(ws, "acme", "api", 42, "abcdef1"); err != nil {
		t.Fatal(err)
	}
	set, err := LoadHandledSet(ws)
	if err != nil {
		t.Fatal(err)
	}
	// The legacy entry survives as unknown-SHA -- not dropped, not adopted to a
	// SHA here (adoption is the decision layer's job) -- and the new entry has its
	// SHA recorded.
	if v, ok := set["legacy/repo#1"]; !ok || v != "" {
		t.Fatalf("legacy entry not preserved as unknown-SHA: %q ok=%v", v, ok)
	}
	if set["acme/api#42"] != "abcdef1" {
		t.Fatalf("new entry SHA = %q", set["acme/api#42"])
	}
}

func TestHandledSet_MalformedLinesSkipped(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		"# niwa-watch-state v2 semantics=level",
		"acme/api#42@a1b2c3d",   // valid SHA-aware
		"",                      // blank
		"  garbage line  ",      // garbage
		"acme/web#not-a-number", // non-numeric PR
		"acme/#7",               // empty repo
		"/repo#7",               // empty owner
		"acme/api#9@NOTHEXX",    // uppercase / non-hex SHA
		"acme/api#9@ab",         // SHA too short
		"acme/web#7",            // valid legacy
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(ws, handledSetRelPath), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := LoadHandledSet(ws)
	if err != nil {
		t.Fatalf("LoadHandledSet: %v", err)
	}
	if len(set) != 2 || set["acme/api#42"] != "a1b2c3d" || set["acme/web#7"] != "" {
		t.Fatalf("malformed lines not skipped correctly: %v", set)
	}
}

func TestAppendHandled_RejectsMalformed(t *testing.T) {
	ws := t.TempDir()
	// A non-hex SHA must be refused (fail-loud), never written.
	if err := AppendHandled(ws, "acme", "api", 42, "NOTHEX!"); err == nil {
		t.Fatal("expected error on malformed SHA")
	}
	// An empty owner is likewise refused.
	if err := AppendHandled(ws, "", "api", 42, "abcdef1"); err == nil {
		t.Fatal("expected error on empty owner")
	}
}

func TestTriggerSemantics_HeaderRoundTrip(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A future edge source can declare edge; the parser round-trips it.
	content := "# niwa-watch-state v2 semantics=edge\nacme/api#42@abcdef1\n"
	if err := os.WriteFile(filepath.Join(ws, handledSetRelPath), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sem, err := LoadTriggerSemantics(ws)
	if err != nil {
		t.Fatalf("LoadTriggerSemantics: %v", err)
	}
	if sem != SemanticsEdge {
		t.Fatalf("semantics = %q, want edge", sem)
	}
	// An append preserves the declared (edge) semantics rather than clobbering it.
	if err := AppendHandled(ws, "acme", "api", 42, "0123456"); err != nil {
		t.Fatal(err)
	}
	if sem, err := LoadTriggerSemantics(ws); err != nil || sem != SemanticsEdge {
		t.Fatalf("semantics after append = %q, err %v", sem, err)
	}
}

func TestStagedRecord_RoundTrip(t *testing.T) {
	ws := t.TempDir()
	rec := StagedRecord{
		Handle:        "abcd1234",
		Owner:         "acme",
		Repo:          "api",
		Number:        42,
		URL:           "https://github.com/acme/api/pull/42",
		DraftPath:     filepath.Join(ws, "inst", "watch-review-draft.md"),
		InstancePath:  filepath.Join(ws, "inst"),
		DispatchedSHA: "abcdef1234567",
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

func TestDeleteStagedRecord(t *testing.T) {
	ws := t.TempDir()
	rec := StagedRecord{Handle: "abcd1234", Owner: "acme", Repo: "api", Number: 42}
	if err := SaveStagedRecord(ws, rec); err != nil {
		t.Fatalf("SaveStagedRecord: %v", err)
	}

	// Deleting an existing record removes it from the store.
	if err := DeleteStagedRecord(ws, "abcd1234"); err != nil {
		t.Fatalf("DeleteStagedRecord: %v", err)
	}
	handles, err := ListStagedHandles(ws)
	if err != nil {
		t.Fatalf("ListStagedHandles: %v", err)
	}
	if len(handles) != 0 {
		t.Fatalf("record not deleted: %v", handles)
	}

	// Deleting a record that is already gone is not an error (idempotent prune).
	if err := DeleteStagedRecord(ws, "abcd1234"); err != nil {
		t.Errorf("DeleteStagedRecord on missing record: %v", err)
	}
}

func TestDeleteStagedRecord_UnsafeHandleRejected(t *testing.T) {
	ws := t.TempDir()
	for _, bad := range []string{"../escape", "a/b", "with space", ""} {
		if err := DeleteStagedRecord(ws, bad); err == nil {
			t.Errorf("DeleteStagedRecord accepted unsafe handle %q", bad)
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
