package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestComputeSourceFingerprintDeterministic exercises the rollup for
// the three invariants that downstream code (niwa status, --check-
// vault in Issue 10) depends on: deterministic hex output, sort-
// independence of the input order, and a stable digest for the empty
// case so files with no recorded sources don't special-case.
func TestComputeSourceFingerprintDeterministic(t *testing.T) {
	a := []SourceEntry{
		{Kind: SourceKindPlaintext, SourceID: "workspace.env", VersionToken: "sha256:aaaa"},
		{Kind: SourceKindVault, SourceID: "team/TOKEN", VersionToken: "v42", Provenance: "audit://x"},
	}
	b := []SourceEntry{
		{Kind: SourceKindVault, SourceID: "team/TOKEN", VersionToken: "v42", Provenance: "audit://x"},
		{Kind: SourceKindPlaintext, SourceID: "workspace.env", VersionToken: "sha256:aaaa"},
	}
	fpA := ComputeSourceFingerprint(a)
	fpB := ComputeSourceFingerprint(b)
	if fpA != fpB {
		t.Fatalf("fingerprint is not sort-independent: %s vs %s", fpA, fpB)
	}
	if len(fpA) != 64 {
		t.Errorf("fingerprint length = %d, want 64 (sha256 hex)", len(fpA))
	}

	// Changing a VersionToken must shift the rollup.
	c := []SourceEntry{
		{Kind: SourceKindPlaintext, SourceID: "workspace.env", VersionToken: "sha256:aaaa"},
		{Kind: SourceKindVault, SourceID: "team/TOKEN", VersionToken: "v43", Provenance: "audit://x"},
	}
	if got := ComputeSourceFingerprint(c); got == fpA {
		t.Errorf("fingerprint did not change on VersionToken change: %s", got)
	}

	// Empty input produces the sha256 of the empty byte string, a
	// well-known constant. We don't check the exact constant so the
	// test doesn't couple to encoding-hex output; we only check that
	// the output is non-empty and matches between two calls.
	empty1 := ComputeSourceFingerprint(nil)
	empty2 := ComputeSourceFingerprint([]SourceEntry{})
	if empty1 == "" {
		t.Error("empty input produced empty fingerprint")
	}
	if empty1 != empty2 {
		t.Errorf("nil and empty slice produced different fingerprints: %s vs %s", empty1, empty2)
	}
}

// TestComputeSourceFingerprintIgnoresProvenanceAndKind locks in the
// contract that Kind and Provenance do NOT participate in the rollup
// — only (SourceID, VersionToken) do. Downstream auditing surfaces
// Kind and Provenance from Sources[] directly, so mixing them into
// the hash would cause spurious "stale" classifications when a
// provider updates an audit-log URL but not the underlying value.
func TestComputeSourceFingerprintIgnoresProvenanceAndKind(t *testing.T) {
	a := []SourceEntry{{Kind: SourceKindPlaintext, SourceID: "s", VersionToken: "v"}}
	b := []SourceEntry{{Kind: SourceKindVault, SourceID: "s", VersionToken: "v", Provenance: "audit://change"}}
	if ComputeSourceFingerprint(a) != ComputeSourceFingerprint(b) {
		t.Error("Kind/Provenance must not influence the rollup")
	}
}

// TestSourceEntryJSONRoundTrip guarantees that a ManagedFile with
// Sources populated survives a marshal/unmarshal round trip, and
// that the omitempty tags on the new fields keep pre-Issue-7
// ManagedFiles byte-identical to their v1 representation.
func TestSourceEntryJSONRoundTrip(t *testing.T) {
	orig := ManagedFile{
		Path:              "/tmp/foo.env",
		ContentHash:       "sha256:c0ffee",
		SourceFingerprint: "deadbeef",
		Sources: []SourceEntry{
			{Kind: SourceKindPlaintext, SourceID: "workspace.env", VersionToken: "sha256:aaaa"},
			{Kind: SourceKindVault, SourceID: "team/TOKEN", VersionToken: "v42", Provenance: "audit://x"},
		},
		Generated: time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ManagedFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.SourceFingerprint != orig.SourceFingerprint {
		t.Errorf("SourceFingerprint = %q, want %q", got.SourceFingerprint, orig.SourceFingerprint)
	}
	if len(got.Sources) != len(orig.Sources) {
		t.Fatalf("Sources length = %d, want %d", len(got.Sources), len(orig.Sources))
	}
	for i, want := range orig.Sources {
		if got.Sources[i] != want {
			t.Errorf("Sources[%d] = %+v, want %+v", i, got.Sources[i], want)
		}
	}

	// Omitempty: an empty ManagedFile has neither source_fingerprint
	// nor sources in its JSON form, so v1 consumers (even pre-
	// Issue-7 binaries with strict field parsing) can load it.
	emptySrc, err := json.Marshal(ManagedFile{Path: "/x", ContentHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(emptySrc), "source_fingerprint") {
		t.Errorf("empty SourceFingerprint leaked into JSON: %s", emptySrc)
	}
	if strings.Contains(string(emptySrc), "sources") {
		t.Errorf("empty Sources leaked into JSON: %s", emptySrc)
	}
}

// TestLoadStateV1MigrationShim verifies that a state.json file
// produced by a pre-Issue-7 binary (SchemaVersion=1, no source
// fields on ManagedFile) loads without error. The new fields are
// left at zero values; the next SaveState rewrites the file as v2.
func TestLoadStateV1MigrationShim(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A verbatim v1 payload: schema_version=1 and a managed file
	// that uses the old "hash" JSON key with no fingerprint/sources.
	v1JSON := `{
		"schema_version": 1,
		"config_name": "ws",
		"instance_name": "ws",
		"instance_number": 1,
		"root": "` + dir + `",
		"created": "2026-01-01T00:00:00Z",
		"last_applied": "2026-01-01T00:00:00Z",
		"managed_files": [
			{"path": "/tmp/file.md", "hash": "sha256:1111", "generated": "2026-01-01T00:00:00Z"}
		],
		"repos": {}
	}`
	if err := os.WriteFile(filepath.Join(stateDir, StateFile), []byte(v1JSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1 (migration shim must not mutate on load)", loaded.SchemaVersion)
	}
	if len(loaded.ManagedFiles) != 1 {
		t.Fatalf("ManagedFiles length = %d, want 1", len(loaded.ManagedFiles))
	}
	mf := loaded.ManagedFiles[0]
	if mf.ContentHash != "sha256:1111" {
		t.Errorf("ContentHash = %q, want sha256:1111 (v1 hash key must map to ContentHash)", mf.ContentHash)
	}
	if mf.SourceFingerprint != "" {
		t.Errorf("SourceFingerprint = %q, want empty (v1 files have no fingerprint)", mf.SourceFingerprint)
	}
	if mf.Sources != nil {
		t.Errorf("Sources = %+v, want nil (v1 files have no sources)", mf.Sources)
	}

	// Rewriting bumps to v2.
	loaded.SchemaVersion = SchemaVersion
	if err := SaveState(dir, loaded); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	reloaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState after rewrite: %v", err)
	}
	if reloaded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion after rewrite = %d, want %d", reloaded.SchemaVersion, SchemaVersion)
	}
}
