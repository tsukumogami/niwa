package workspace

import (
	"context"
	"testing"
)

// TestCredentialPool_FileOnlyHit covers the file-layer match path.
// The pool returns the matching entry plus an AuditRecord that
// records SourceLocalFile for the (kind, project) pair (PRD AC-32).
func TestCredentialPool_FileOnlyHit(t *testing.T) {
	entries := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-1",
				"client_id":     "cid",
				"client_secret": "csec",
			},
		},
	}
	pool := NewCredentialPool(entries, nil)

	entry, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("Lookup returned unexpected error: %v", err)
	}
	if entry == nil {
		t.Fatal("Lookup returned nil entry on file-layer hit")
	}
	if entry.Kind != "infisical" {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, "infisical")
	}
	if rec.Source != SourceLocalFile {
		t.Errorf("rec.Source = %q, want %q", rec.Source, SourceLocalFile)
	}
	if rec.Kind != "infisical" || rec.Project != "uuid-1" {
		t.Errorf("rec = %+v, expected Kind=infisical Project=uuid-1", rec)
	}
	if rec.Fallback != "" {
		t.Errorf("rec.Fallback = %q on file-only hit, want empty", rec.Fallback)
	}
}

// TestCredentialPool_FileOnlyMiss covers the file-miss path with no
// vault loader. The pool returns nil entry plus a tentative
// SourceCLISession audit record.
func TestCredentialPool_FileOnlyMiss(t *testing.T) {
	pool := NewCredentialPool(nil, nil)

	entry, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-missing")
	if err != nil {
		t.Fatalf("Lookup returned unexpected error: %v", err)
	}
	if entry != nil {
		t.Errorf("Lookup returned non-nil entry on file-only miss: %+v", entry)
	}
	if rec.Source != SourceCLISession {
		t.Errorf("rec.Source = %q, want %q", rec.Source, SourceCLISession)
	}
}

// TestCredentialPool_AuditAccumulates confirms that AuditLog grows
// with each Lookup call and preserves order (insertion order). I3
// reads the slice for state persistence; I9 reads it for R12 stderr.
func TestCredentialPool_AuditAccumulates(t *testing.T) {
	entries := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-1",
				"client_id":     "cid",
				"client_secret": "csec",
			},
		},
	}
	pool := NewCredentialPool(entries, nil)
	ctx := context.Background()

	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-1")        // hit
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-missing")  // miss
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-1")        // hit again

	log := pool.AuditLog()
	if len(log) != 3 {
		t.Fatalf("AuditLog length = %d, want 3", len(log))
	}
	if log[0].Source != SourceLocalFile || log[0].Project != "uuid-1" {
		t.Errorf("log[0] = %+v, want SourceLocalFile uuid-1", log[0])
	}
	if log[1].Source != SourceCLISession || log[1].Project != "uuid-missing" {
		t.Errorf("log[1] = %+v, want SourceCLISession uuid-missing", log[1])
	}
	if log[2].Source != SourceLocalFile || log[2].Project != "uuid-1" {
		t.Errorf("log[2] = %+v, want SourceLocalFile uuid-1", log[2])
	}
}

// TestCredentialPool_NilLoaderIsFileOnly confirms that passing a
// nil loader to NewCredentialPool produces a pool that never reaches
// into a vault layer. With no file entries either, every Lookup
// records SourceCLISession.
func TestCredentialPool_NilLoaderIsFileOnly(t *testing.T) {
	pool := NewCredentialPool(nil, nil)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, rec, err := pool.Lookup(ctx, "infisical", "uuid")
		if err != nil {
			t.Fatalf("Lookup #%d returned unexpected error: %v", i, err)
		}
		if rec.Source != SourceCLISession {
			t.Errorf("Lookup #%d rec.Source = %q, want %q", i, rec.Source, SourceCLISession)
		}
	}
	if len(pool.AuditLog()) != 3 {
		t.Errorf("AuditLog length = %d, want 3", len(pool.AuditLog()))
	}
}

// TestCredentialPool_MatchParity confirms the pool uses the same
// matching rule as MatchProviderAuth — a regression guard so I7's
// later additions can't drift the matching logic.
func TestCredentialPool_MatchParity(t *testing.T) {
	entries := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-A",
				"client_id":     "cidA",
				"client_secret": "csecA",
			},
		},
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-B",
				"client_id":     "cidB",
				"client_secret": "csecB",
			},
		},
	}
	pool := NewCredentialPool(entries, nil)
	ctx := context.Background()

	// Pool match for B should return entry B, not A — same rule as
	// MatchProviderAuth's project comparison.
	entry, _, err := pool.Lookup(ctx, "infisical", "uuid-B")
	if err != nil {
		t.Fatalf("Lookup returned unexpected error: %v", err)
	}
	if entry == nil {
		t.Fatal("Lookup returned nil entry for known project uuid-B")
	}
	cid, _ := entry.Config["client_id"].(string)
	if cid != "cidB" {
		t.Errorf("matched entry's client_id = %q, want %q", cid, "cidB")
	}
}

// TestCredentialPool_AuditLogPreservesOrder is a property-style
// guard: AuditLog returns entries in the same order Lookup was
// called, not a reordered or deduplicated view.
func TestCredentialPool_AuditLogPreservesOrder(t *testing.T) {
	pool := NewCredentialPool(nil, nil)
	ctx := context.Background()

	projects := []string{"a", "b", "c", "a", "b"}
	for _, p := range projects {
		_, _, _ = pool.Lookup(ctx, "infisical", p)
	}

	log := pool.AuditLog()
	if len(log) != len(projects) {
		t.Fatalf("AuditLog length = %d, want %d", len(log), len(projects))
	}
	for i, want := range projects {
		if log[i].Project != want {
			t.Errorf("log[%d].Project = %q, want %q", i, log[i].Project, want)
		}
	}
}
