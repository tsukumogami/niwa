package onboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMintRecord_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	if err := writeMintRecord(dir, "infisical", "proj-1", mintRecord{SecretID: "secret-abc"}); err != nil {
		t.Fatalf("writeMintRecord: %v", err)
	}

	rec, found, err := readMintRecord(dir, "infisical", "proj-1")
	if err != nil {
		t.Fatalf("readMintRecord: %v", err)
	}
	if !found {
		t.Fatalf("found = false, want true")
	}
	if rec.SecretID != "secret-abc" {
		t.Errorf("SecretID = %q, want secret-abc", rec.SecretID)
	}
}

func TestMintRecord_AbsentIsNotFoundNoError(t *testing.T) {
	dir := t.TempDir()

	rec, found, err := readMintRecord(dir, "infisical", "proj-missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Errorf("found = true, want false for an absent record")
	}
	if rec.SecretID != "" {
		t.Errorf("SecretID = %q, want empty", rec.SecretID)
	}
}

func TestMintRecord_MalformedIsNotFoundNoError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, recordFileName("infisical", "proj-1"))
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seeding malformed record: %v", err)
	}

	rec, found, err := readMintRecord(dir, "infisical", "proj-1")
	if err != nil {
		t.Fatalf("malformed record must not be a fatal error, got: %v", err)
	}
	if found {
		t.Errorf("found = true, want false for a malformed record")
	}
	if rec.SecretID != "" {
		t.Errorf("SecretID = %q, want empty", rec.SecretID)
	}
}

func TestMintRecord_EmptySecretIDIsNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, recordFileName("infisical", "proj-1"))
	if err := os.WriteFile(path, []byte(`{"secret_id":""}`), 0o600); err != nil {
		t.Fatalf("seeding empty-id record: %v", err)
	}

	_, found, err := readMintRecord(dir, "infisical", "proj-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Errorf("found = true, want false for a record with an empty secret_id")
	}
}

func TestMintRecord_KeyedByKindAndProject(t *testing.T) {
	dir := t.TempDir()

	if err := writeMintRecord(dir, "infisical", "proj-a", mintRecord{SecretID: "secret-a"}); err != nil {
		t.Fatalf("writeMintRecord(proj-a): %v", err)
	}
	if err := writeMintRecord(dir, "infisical", "proj-b", mintRecord{SecretID: "secret-b"}); err != nil {
		t.Fatalf("writeMintRecord(proj-b): %v", err)
	}

	recA, foundA, err := readMintRecord(dir, "infisical", "proj-a")
	if err != nil || !foundA || recA.SecretID != "secret-a" {
		t.Errorf("proj-a: rec=%+v found=%v err=%v, want secret-a/true/nil", recA, foundA, err)
	}
	recB, foundB, err := readMintRecord(dir, "infisical", "proj-b")
	if err != nil || !foundB || recB.SecretID != "secret-b" {
		t.Errorf("proj-b: rec=%+v found=%v err=%v, want secret-b/true/nil", recB, foundB, err)
	}
}

func TestMintRecord_WriteIs0600AndAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := writeMintRecord(dir, "infisical", "proj-1", mintRecord{SecretID: "secret-abc"}); err != nil {
		t.Fatalf("writeMintRecord: %v", err)
	}
	path := filepath.Join(dir, recordFileName("infisical", "proj-1"))
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 0600", perm)
	}

	// No stray temp files should survive a successful write (temp-in-
	// dir-then-rename discipline).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want exactly 1 (no leftover temp file): %+v", len(entries), entries)
	}
}

func TestMintRecord_OverwriteReplacesPreviousValue(t *testing.T) {
	dir := t.TempDir()
	if err := writeMintRecord(dir, "infisical", "proj-1", mintRecord{SecretID: "secret-old"}); err != nil {
		t.Fatalf("writeMintRecord(old): %v", err)
	}
	if err := writeMintRecord(dir, "infisical", "proj-1", mintRecord{SecretID: "secret-new"}); err != nil {
		t.Fatalf("writeMintRecord(new): %v", err)
	}
	rec, found, err := readMintRecord(dir, "infisical", "proj-1")
	if err != nil || !found {
		t.Fatalf("readMintRecord: rec=%+v found=%v err=%v", rec, found, err)
	}
	if rec.SecretID != "secret-new" {
		t.Errorf("SecretID = %q, want secret-new", rec.SecretID)
	}
}

func TestRecordFileName_SanitizesPathSeparators(t *testing.T) {
	name := recordFileName("infisical", "../../etc/passwd")
	if filepath.Base(name) != name {
		t.Errorf("recordFileName produced a path-escaping value: %q", name)
	}
	if name == recordFileName("infisical", "..") {
		t.Errorf("distinct hostile inputs collided")
	}
}
