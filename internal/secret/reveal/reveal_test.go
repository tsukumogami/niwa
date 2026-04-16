package reveal_test

import (
	"bytes"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
)

// TestUnsafeRevealReturnsPlaintext is the AC for the reveal
// sub-package: UnsafeReveal returns the plaintext bytes of a Value.
func TestUnsafeRevealReturnsPlaintext(t *testing.T) {
	plaintext := []byte("plaintext-under-test")
	v := secret.New(plaintext, secret.Origin{ProviderName: "p", Key: "k"})

	got := reveal.UnsafeReveal(v)
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("UnsafeReveal = %q, want %q", got, plaintext)
	}
}

// TestUnsafeRevealEmpty confirms UnsafeReveal returns nil for the
// zero Value.
func TestUnsafeRevealEmpty(t *testing.T) {
	var zero secret.Value
	if got := reveal.UnsafeReveal(zero); got != nil {
		t.Fatalf("UnsafeReveal(zero) = %v, want nil", got)
	}
}

// TestUnsafeRevealAfterCopy confirms secret.New copies input, so
// mutating the caller's buffer does not affect what UnsafeReveal
// returns.
func TestUnsafeRevealAfterCopy(t *testing.T) {
	buf := []byte("original-secret-bytes")
	v := secret.New(buf, secret.Origin{})
	for i := range buf {
		buf[i] = 0
	}
	got := reveal.UnsafeReveal(v)
	if !bytes.Equal(got, []byte("original-secret-bytes")) {
		t.Fatalf("UnsafeReveal saw mutation: %q", got)
	}
}
