package vault_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

const (
	scrubCtxFragment   = "context-registered-token"
	scrubKnownFragment = "known-fragment-token"
)

// TestScrubStderrEmpty covers the early-exit path for empty stderr.
func TestScrubStderrEmpty(t *testing.T) {
	got := vault.ScrubStderr(context.Background(), nil)
	if got != "" {
		t.Fatalf("ScrubStderr(nil) = %q, want empty", got)
	}
	got = vault.ScrubStderr(context.Background(), []byte{})
	if got != "" {
		t.Fatalf("ScrubStderr(empty) = %q, want empty", got)
	}
}

// TestScrubStderrUsesContextRedactor asserts AC: ScrubStderr applies
// the context's Redactor when one is attached.
func TestScrubStderrUsesContextRedactor(t *testing.T) {
	r := secret.NewRedactor()
	r.Register([]byte(scrubCtxFragment))
	ctx := secret.WithRedactor(context.Background(), r)

	raw := []byte("error: saw " + scrubCtxFragment + " in stderr")
	got := vault.ScrubStderr(ctx, raw)
	if strings.Contains(got, scrubCtxFragment) {
		t.Fatalf("ctx-scoped redactor did not scrub: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("expected placeholder in output: %q", got)
	}
}

// TestScrubStderrKnownFragments asserts AC: ScrubStderr runs known
// fragments through a fresh Redactor.
func TestScrubStderrKnownFragments(t *testing.T) {
	v := secret.New([]byte(scrubKnownFragment), secret.Origin{})
	raw := []byte("backend failure at " + scrubKnownFragment + " :(")
	got := vault.ScrubStderr(context.Background(), raw, v)
	if strings.Contains(got, scrubKnownFragment) {
		t.Fatalf("known fragment leaked through ScrubStderr: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("expected placeholder, got: %q", got)
	}
}

// TestScrubStderrAppliesBothLayers exercises the both-layers case:
// ctx redactor AND known fragments both apply, and neither leaks.
func TestScrubStderrAppliesBothLayers(t *testing.T) {
	r := secret.NewRedactor()
	r.Register([]byte(scrubCtxFragment))
	ctx := secret.WithRedactor(context.Background(), r)

	v := secret.New([]byte(scrubKnownFragment), secret.Origin{})
	raw := []byte("dual leak: " + scrubCtxFragment + " and " + scrubKnownFragment)

	got := vault.ScrubStderr(ctx, raw, v)
	if strings.Contains(got, scrubCtxFragment) {
		t.Fatalf("ctx fragment leaked: %q", got)
	}
	if strings.Contains(got, scrubKnownFragment) {
		t.Fatalf("known fragment leaked: %q", got)
	}
}

// TestScrubStderrDoesNotPolluteContextRedactor verifies that fragments
// passed only through the known slice do not end up on the context
// Redactor. This matters because the ctx redactor is shared across
// the whole apply, and leaking a single-call fragment into it would
// widen the scrub set more than intended.
func TestScrubStderrDoesNotPolluteContextRedactor(t *testing.T) {
	r := secret.NewRedactor()
	ctx := secret.WithRedactor(context.Background(), r)

	v := secret.New([]byte(scrubKnownFragment), secret.Origin{})
	_ = vault.ScrubStderr(ctx, []byte("x "+scrubKnownFragment), v)

	// Now scrub a fresh string through the ctx redactor only; the
	// known fragment should NOT have been registered there.
	leftover := r.Scrub("still has " + scrubKnownFragment + " here")
	if !strings.Contains(leftover, scrubKnownFragment) {
		t.Fatalf("known fragment leaked into ctx redactor: %q", leftover)
	}
}

// TestScrubStderrShortFragmentIgnored locks in the inherited
// behaviour from secret.Redactor: fragments shorter than the minimum
// length are silently ignored (they collide too often to be safely
// replaced). A caller who hands ScrubStderr a 3-byte "secret" gets
// the literal back unchanged.
func TestScrubStderrShortFragmentIgnored(t *testing.T) {
	short := secret.New([]byte("abc"), secret.Origin{})
	got := vault.ScrubStderr(context.Background(), []byte("abc xyz"), short)
	if got != "abc xyz" {
		t.Fatalf("short fragment was unexpectedly scrubbed: %q", got)
	}
}

// TestScrubStderrNoRedactorInCtxOrKnown covers the no-op case: no
// ctx redactor, no known fragments. ScrubStderr should pass the
// bytes through as a string.
func TestScrubStderrNoRedactorInCtxOrKnown(t *testing.T) {
	raw := []byte("harmless output")
	got := vault.ScrubStderr(context.Background(), raw)
	if got != "harmless output" {
		t.Fatalf("ScrubStderr without redactors modified input: %q", got)
	}
}
