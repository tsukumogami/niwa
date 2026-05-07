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

// TestAuditTrail_AsMap_Empty confirms AsMap returns nil for an
// empty trail. Avoids serializing an empty `auth_sources: {}` into
// state.json (omitempty handles it on the JSON side, but this also
// preserves the "no entries" state across the type boundary).
func TestAuditTrail_AsMap_Empty(t *testing.T) {
	var trail AuditTrail
	if got := trail.AsMap(); got != nil {
		t.Errorf("AsMap() on empty trail = %+v, want nil", got)
	}
}

// TestAuditTrail_AsMap_AllSources confirms each Source value renders
// to the categorical string state.json expects. AC-39 covers the
// anonymous-vault case explicitly.
func TestAuditTrail_AsMap_AllSources(t *testing.T) {
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceLocalFile},
		{Kind: "infisical", Project: "uuid-B", Source: SourceVault, Provider: "personal"},
		{Kind: "infisical", Project: "uuid-C", Source: SourceVault, Provider: ""}, // anonymous
		{Kind: "infisical", Project: "uuid-D", Source: SourceCLISession},
		{Kind: "infisical", Project: "uuid-E", Source: SourceNone},
	}
	got := trail.AsMap()

	cases := []struct {
		key  string
		want AuthSourceRecord
	}{
		{"infisical/uuid-A", AuthSourceRecord{Source: "local-file"}},
		{"infisical/uuid-B", AuthSourceRecord{Source: "vault:personal"}},
		{"infisical/uuid-C", AuthSourceRecord{Source: "vault:(anonymous)"}}, // AC-39
		{"infisical/uuid-D", AuthSourceRecord{Source: "cli-session"}},
		{"infisical/uuid-E", AuthSourceRecord{Source: "none"}},
	}
	if len(got) != len(cases) {
		t.Errorf("map size = %d, want %d", len(got), len(cases))
	}
	for _, c := range cases {
		rec, ok := got[c.key]
		if !ok {
			t.Errorf("AsMap missing key %q", c.key)
			continue
		}
		if rec != c.want {
			t.Errorf("AsMap[%q] = %+v, want %+v", c.key, rec, c.want)
		}
	}
}

// TestAuditTrail_AsMap_FallbackPropagated confirms Fallback flows
// through verbatim. The pool sets Fallback to "vault:<name>" when
// the file layer wins and the vault also had an entry; AsMap copies
// it without translation.
func TestAuditTrail_AsMap_FallbackPropagated(t *testing.T) {
	trail := AuditTrail{
		{
			Kind:     "infisical",
			Project:  "uuid-X",
			Source:   SourceLocalFile,
			Fallback: "vault:personal",
		},
	}
	got := trail.AsMap()
	rec, ok := got["infisical/uuid-X"]
	if !ok {
		t.Fatal("AsMap missing key infisical/uuid-X")
	}
	if rec.Source != "local-file" {
		t.Errorf("Source = %q, want %q", rec.Source, "local-file")
	}
	if rec.Fallback != "vault:personal" {
		t.Errorf("Fallback = %q, want %q", rec.Fallback, "vault:personal")
	}
}

// TestAuditTrail_AsMap_LastWriteWins confirms that when the same
// (kind, project) appears multiple times in the trail (e.g., a
// provider declared in both team and personal vault registries),
// the LAST record wins. This matches apply.go's "later layer
// overrides earlier layer" semantics so the user's actual final
// state for that pair lands in the audit.
func TestAuditTrail_AsMap_LastWriteWins(t *testing.T) {
	trail := AuditTrail{
		{Kind: "infisical", Project: "dup", Source: SourceCLISession},
		{Kind: "infisical", Project: "dup", Source: SourceLocalFile},
	}
	got := trail.AsMap()
	if rec := got["infisical/dup"]; rec.Source != "local-file" {
		t.Errorf("LastWriteWins: AsMap[infisical/dup].Source = %q, want %q", rec.Source, "local-file")
	}
}

// Test_renderSource_UnknownReturnsEmpty confirms the defensive
// default branch — an AuditRecord with an unrecognised Source
// renders to "" rather than a junk string. (Lookup never produces
// such a record; this guards against a future Source enum addition
// that forgets to update renderSource.)
func Test_renderSource_UnknownReturnsEmpty(t *testing.T) {
	rec := AuditRecord{Source: Source("garbage")}
	if got := renderSource(rec); got != "" {
		t.Errorf("renderSource(unknown) = %q, want empty string", got)
	}
}
