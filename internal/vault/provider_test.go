package vault_test

import (
	"context"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// staticProvider is a minimal test-only Provider that does not
// exercise the full backend surface; it simply satisfies the
// interface so we can verify that the interface signatures compile
// and behave as described.
type staticProvider struct {
	name   string
	kind   string
	value  secret.Value
	token  vault.VersionToken
	closed bool
}

func (p *staticProvider) Name() string { return p.name }
func (p *staticProvider) Kind() string { return p.kind }
func (p *staticProvider) Resolve(_ context.Context, _ vault.Ref) (secret.Value, vault.VersionToken, error) {
	return p.value, p.token, nil
}
func (p *staticProvider) Close() error {
	p.closed = true
	return nil
}

// staticBatchProvider additionally implements vault.BatchResolver.
type staticBatchProvider struct {
	staticProvider
	batchCalls int
}

func (p *staticBatchProvider) ResolveBatch(_ context.Context, refs []vault.Ref) ([]vault.BatchResult, error) {
	p.batchCalls++
	out := make([]vault.BatchResult, len(refs))
	for i, r := range refs {
		out[i] = vault.BatchResult{Ref: r, Value: p.value, Token: p.token}
	}
	return out, nil
}

// TestProviderInterfaceSurface asserts AC: Provider exposes the four
// mandated methods. A non-implementing type would fail the static
// assignment.
func TestProviderInterfaceSurface(t *testing.T) {
	var p vault.Provider = &staticProvider{name: "team", kind: "fake"}
	if got := p.Name(); got != "team" {
		t.Fatalf("Name() = %q, want %q", got, "team")
	}
	if got := p.Kind(); got != "fake" {
		t.Fatalf("Kind() = %q, want %q", got, "fake")
	}
	if _, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"}); err != nil {
		t.Fatalf("Resolve returned unexpected error: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close returned unexpected error: %v", err)
	}
}

// TestBatchResolverDetectedByTypeAssertion asserts AC: optional
// BatchResolver interface is detected by runtime type assertion. The
// resolver stage consumers rely on this to opt into batching.
func TestBatchResolverDetectedByTypeAssertion(t *testing.T) {
	plain := &staticProvider{name: "team", kind: "fake"}
	batch := &staticBatchProvider{staticProvider: staticProvider{name: "team2", kind: "fake"}}

	var p1 vault.Provider = plain
	var p2 vault.Provider = batch

	if _, ok := p1.(vault.BatchResolver); ok {
		t.Fatalf("plain Provider should not satisfy BatchResolver")
	}
	if _, ok := p2.(vault.BatchResolver); !ok {
		t.Fatalf("BatchResolver Provider should satisfy BatchResolver")
	}

	br := p2.(vault.BatchResolver)
	refs := []vault.Ref{{Key: "a"}, {Key: "b"}}
	results, err := br.ResolveBatch(context.Background(), refs)
	if err != nil {
		t.Fatalf("ResolveBatch returned unexpected error: %v", err)
	}
	if len(results) != len(refs) {
		t.Fatalf("ResolveBatch returned %d results, want %d", len(results), len(refs))
	}
	if batch.batchCalls != 1 {
		t.Fatalf("expected batch path invoked once, got %d", batch.batchCalls)
	}
}

// TestProviderSpecZeroValue verifies ProviderSpec is a plain struct
// whose zero value is usable. The resolver stage uses a zero spec as
// a sentinel ("no provider declared"), so we lock in that behaviour.
func TestProviderSpecZeroValue(t *testing.T) {
	var spec vault.ProviderSpec
	if spec.Name != "" || spec.Kind != "" || spec.Source != "" {
		t.Fatalf("ProviderSpec zero value has non-empty fields: %+v", spec)
	}
	if spec.Config != nil {
		t.Fatalf("ProviderSpec zero value should have nil Config map")
	}
}

// TestVersionTokenZeroValue verifies VersionToken has no hidden
// defaults that might poison comparisons (state.json diffing).
func TestVersionTokenZeroValue(t *testing.T) {
	var vt vault.VersionToken
	if vt.Token != "" || vt.Provenance != "" {
		t.Fatalf("VersionToken zero value has non-empty fields: %+v", vt)
	}
}
