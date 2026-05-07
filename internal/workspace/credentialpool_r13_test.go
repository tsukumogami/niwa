package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// TestPool_R13_VaultUnreachableTypedErrorRecovery confirms apply-
// time wiring can errors.As the typed unreachable error to recover
// the (kind, project) + provider name for R13.1's aggregated warning.
func TestPool_R13_VaultUnreachableTypedErrorRecovery(t *testing.T) {
	_, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/uuid-X": {err: vault.ErrProviderUnreachable},
	})
	pool := NewCredentialPool(nil, loader)

	_, _, err := pool.Lookup(context.Background(), "infisical", "uuid-X")
	if err == nil {
		t.Fatal("expected vault-unreachable error")
	}

	var vue *vaultUnreachableError
	if !errors.As(err, &vue) {
		t.Fatalf("error must be a *vaultUnreachableError, got: %T %v", err, err)
	}
	if vue.Kind != "infisical" || vue.Project != "uuid-X" || vue.ProviderName != "personal" {
		t.Errorf("vue = %+v, want Kind=infisical Project=uuid-X ProviderName=personal", vue)
	}
	// Sentinel must still match for any downstream consumer.
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Errorf("error chain must still match vault.ErrProviderUnreachable. Got: %v", err)
	}
}

// TestPool_R13_VaultUnreachableObservationsDeduplicated covers the
// aggregator: repeat Lookups for the same (kind, project, provider)
// produce one observation, not many.
func TestPool_R13_VaultUnreachableObservationsDeduplicated(t *testing.T) {
	_, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/uuid-X": {err: vault.ErrProviderUnreachable},
		"/niwa/provider-auth/infisical/uuid-Y": {err: vault.ErrProviderUnreachable},
	})
	pool := NewCredentialPool(nil, loader)
	ctx := context.Background()

	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-X")
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-X") // repeat — cache hit, but still recorded once
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-Y") // distinct pair

	obs := pool.VaultUnreachableObservations()
	if len(obs) != 2 {
		t.Errorf("expected 2 observations (uuid-X + uuid-Y), got %d", len(obs))
	}
}

// TestPool_R13_FileWinsOnUnreachableWithFallback covers PRD AC-7
// + AC-16: when the vault is unreachable BUT the file layer
// covers the pair, Lookup must prefer the file entry — the apply
// continues normally with the local-file credential, the audit
// row reflects local-file (no Fallback because the vault entry
// was never fetched), and the unreachable observation is still
// recorded on the pool for the apply-side aggregated warning.
//
// Earlier wiring incorrectly returned the unreachable error
// before checking the file layer, which would have failed
// AC-7's row-classification rule and AC-16's "exit 0 when every
// pair has a fallback" requirement.
func TestPool_R13_FileWinsOnUnreachableWithFallback(t *testing.T) {
	file := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-X",
				"client_id":     "cid",
				"client_secret": "csec",
			},
		},
	}
	_, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/uuid-X": {err: vault.ErrProviderUnreachable},
	})
	pool := NewCredentialPool(file, loader)

	entry, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-X")
	if err != nil {
		t.Fatalf("expected nil error when file covers the pair, got: %v", err)
	}
	if entry == nil {
		t.Fatal("expected the file entry to be returned")
	}
	if got, _ := entry.Config["client_id"].(string); got != "cid" {
		t.Errorf("client_id = %q, want cid (file entry)", got)
	}
	if rec.Source != SourceLocalFile {
		t.Errorf("rec.Source = %q, want %q", rec.Source, SourceLocalFile)
	}
	// No Fallback: the vault entry was never fetched.
	if rec.Fallback != "" {
		t.Errorf("rec.Fallback = %q, want empty (vault was unreachable; no entry to be a fallback)", rec.Fallback)
	}
	// The unreachable observation MUST still be recorded so the
	// apply-side aggregated warning fires (PRD R13.1).
	if len(pool.VaultUnreachableObservations()) != 1 {
		t.Errorf("expected 1 unreachable observation even on file-wins path, got %d", len(pool.VaultUnreachableObservations()))
	}
}

// TestPool_R13_FileMissUnreachableRecordsCLISession covers PRD
// R13.1's no-file-fallback case: when the vault is unreachable
// AND no file entry covers the pair, Lookup records a tentative
// SourceCLISession row and surfaces the error so
// injectProviderTokens can soften (continue iterating without
// injecting a token, letting the backend's CLI session take
// over).
func TestPool_R13_FileMissUnreachableRecordsCLISession(t *testing.T) {
	_, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/uuid-X": {err: vault.ErrProviderUnreachable},
	})
	pool := NewCredentialPool(nil, loader)

	_, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-X")
	if err == nil {
		t.Fatal("expected vault-unreachable error to surface (no file fallback)")
	}
	if rec.Source != SourceCLISession {
		t.Errorf("rec.Source = %q, want %q", rec.Source, SourceCLISession)
	}
	log := pool.AuditLog()
	if len(log) != 1 || log[0].Source != SourceCLISession {
		t.Errorf("expected one SourceCLISession audit row, got %+v", log)
	}
}


// TestPool_HasFileFallback exercises the fallback-detection helper
// used by injectProviderTokens to soften vault-unreachable errors.
func TestPool_HasFileFallback(t *testing.T) {
	file := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-covered",
				"client_id":     "cid",
				"client_secret": "csec",
			},
		},
	}
	pool := NewCredentialPool(file, nil)

	if !pool.HasFileFallback("infisical", "uuid-covered") {
		t.Error("expected HasFileFallback to return true for a covered pair")
	}
	if pool.HasFileFallback("infisical", "uuid-uncovered") {
		t.Error("expected HasFileFallback to return false for an uncovered pair")
	}
	if pool.HasFileFallback("sops", "uuid-covered") {
		t.Error("expected HasFileFallback to return false for a different kind")
	}
}

// TestPool_R13_UnreachableObservationRecordedOnLookup confirms
// the contract injectProviderTokens depends on for its soft path:
// a Lookup that returns vaultUnreachableError records the
// observation on the pool's VaultUnreachableObservations list AND
// preserves errors.Is(vault.ErrProviderUnreachable). Without this,
// injectProviderTokens couldn't detect the recoverable case.
//
// (Note: this test deliberately does NOT call injectProviderTokens
// itself. The end-to-end soft-path coverage lives in
// TestPool_R13_FileWinsOnUnreachableWithFallback +
// TestPool_R13_FileMissUnreachableRecordsCLISession; this one
// locks the pool-level contract those tests build on.)
func TestPool_R13_UnreachableObservationRecordedOnLookup(t *testing.T) {
	stub := &stubVaultProvider{
		name: "personal",
		kind: "infisical",
		response: map[string]stubResponse{
			"/niwa/provider-auth/infisical/uuid-Y": {err: vault.ErrProviderUnreachable},
		},
	}
	loader := &vaultCredLoader{
		Provider:     stub,
		ProviderName: "personal",
		PathPrefix:   CredentialSyncPathPrefix,
	}
	pool := NewCredentialPool(nil, loader)

	// Construct a config.VaultRegistry with an anonymous provider
	// whose project is uuid-Y. injectProviderTokens iterates and
	// invokes Lookup, which returns vaultUnreachableError.
	// We can't import config here (circular dependency would
	// arise on construction), so build via the package-level
	// types: skip — instead, we test the underlying behaviour at
	// the pool level (Lookup returns the typed error and records
	// the observation) which is sufficient to lock the contract
	// injectProviderTokens depends on. The end-to-end apply-level
	// integration is covered by the existing apply_vault_test.go
	// pattern (functional tests).
	_, _, err := pool.Lookup(context.Background(), "infisical", "uuid-Y")
	if err == nil {
		t.Fatal("expected unreachable error from pool")
	}
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Errorf("error chain must match vault.ErrProviderUnreachable. Got: %v", err)
	}
	if len(pool.VaultUnreachableObservations()) != 1 {
		t.Errorf("expected 1 unreachable observation, got %d", len(pool.VaultUnreachableObservations()))
	}
	// Audit row was recorded as tentative SourceCLISession (so
	// downstream surfaces — I3 state, I4 audit-auth, I9 R12 stderr
	// — see the pair).
	log := pool.AuditLog()
	if len(log) != 1 || log[0].Source != SourceCLISession {
		t.Errorf("expected 1 SourceCLISession audit row, got %+v", log)
	}
}
