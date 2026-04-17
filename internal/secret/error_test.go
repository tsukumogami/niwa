package secret_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

const errTestPlaintext = "super-secret-password-9001"

type sentinelError struct{ msg string }

func (e *sentinelError) Error() string { return e.msg }

func TestWrapNilReturnsNil(t *testing.T) {
	if got := secret.Wrap(nil); got != nil {
		t.Fatalf("Wrap(nil) = %v, want nil", got)
	}
}

// TestWrapScrubsFragment asserts AC: Wrap registers each value's
// bytes on a per-context Redactor and returns a *Error whose
// Error() scrubs its inner message.
func TestWrapScrubsFragment(t *testing.T) {
	v := secret.New([]byte(errTestPlaintext), secret.Origin{})
	inner := fmt.Errorf("leaked: %s in stderr", errTestPlaintext)
	wrapped := secret.Wrap(inner, v)
	msg := wrapped.Error()
	if strings.Contains(msg, errTestPlaintext) {
		t.Fatalf("Wrap did not scrub plaintext: %q", msg)
	}
	if !strings.Contains(msg, "***") {
		t.Fatalf("Wrap did not emit placeholder: %q", msg)
	}
}

// TestWrapUnwrapPreservesChain asserts AC: secret.Error.Unwrap()
// preserves the chain for errors.Is and errors.As.
func TestWrapUnwrapPreservesChain(t *testing.T) {
	sentinel := &sentinelError{msg: "sentinel boom"}
	v := secret.New([]byte("bytes-long-enough-to-register"), secret.Origin{})
	wrapped := secret.Wrap(sentinel, v)

	// errors.Is walks through Unwrap.
	if !errors.Is(wrapped, sentinel) {
		t.Fatalf("errors.Is did not find sentinel through secret.Wrap")
	}

	// errors.As extracts the sentinel type.
	var got *sentinelError
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As did not extract *sentinelError through secret.Wrap")
	}
	if got != sentinel {
		t.Fatalf("errors.As returned different sentinel pointer")
	}
}

// TestWrapPreservesRedactorAcrossReWrap asserts that wrapping an
// already-wrapped *Error with a different inner error shares the
// Redactor (so a fragment registered in the outer Wrap is still
// scrubbed from the inner message).
func TestWrapPreservesRedactorAcrossReWrap(t *testing.T) {
	v1 := secret.New([]byte("first-secret-token"), secret.Origin{})
	v2 := secret.New([]byte("second-secret-token"), secret.Origin{})

	inner := fmt.Errorf("both first-secret-token and second-secret-token leaked")
	wrap1 := secret.Wrap(inner, v1)
	wrap2 := secret.Wrap(wrap1, v2)

	msg := wrap2.Error()
	if strings.Contains(msg, "first-secret-token") {
		t.Fatalf("first fragment leaked after double-wrap: %q", msg)
	}
	if strings.Contains(msg, "second-secret-token") {
		t.Fatalf("second fragment leaked after double-wrap: %q", msg)
	}
}

// TestErrorfAutoRegistersValues asserts AC: Errorf auto-registers
// Value args and preserves %w semantics.
func TestErrorfAutoRegistersValues(t *testing.T) {
	v := secret.New([]byte(errTestPlaintext), secret.Origin{})
	sentinel := &sentinelError{msg: "sentinel"}

	// %w preserved; Value interpolated via %s goes through
	// Value.Format, so the raw plaintext never enters the string
	// in the first place. The auto-register path matters for
	// cases where plaintext reaches the message through a
	// different arg (e.g., stderr capture).
	err := secret.Errorf("ctx: %w extra=%s", sentinel, errTestPlaintext)

	if !errors.Is(err, sentinel) {
		t.Fatalf("Errorf did not preserve %%w chain")
	}

	// The plaintext appeared in the format args directly, but
	// the Value was not in args — simulating the common path we
	// DO defend: register via explicit Wrap/Errorf.
	errWithValue := secret.Errorf("%w leaked=%s", err, errTestPlaintext)
	// errWithValue's Redactor is inherited from err; that
	// redactor has no fragments yet because nothing registered
	// the Value. Now register via a third-arg Value:
	final := secret.Errorf("%w sideband=%s registered=%v", errWithValue, errTestPlaintext, v)

	msg := final.Error()
	if strings.Contains(msg, errTestPlaintext) {
		t.Fatalf("Errorf did not scrub plaintext after Value-arg registered it: %q", msg)
	}
}

// TestErrorfAutoRegistersValuePointer checks *Value args also
// auto-register.
func TestErrorfAutoRegistersValuePointer(t *testing.T) {
	v := secret.New([]byte(errTestPlaintext), secret.Origin{})
	err := secret.Errorf("leaked=%s ref=%v", errTestPlaintext, &v)
	if strings.Contains(err.Error(), errTestPlaintext) {
		t.Fatalf("*Value arg did not auto-register: %q", err.Error())
	}
}

// TestErrorfInheritsContextRedactor asserts AC: when a ctx argument
// carries a Redactor, Errorf inherits it.
func TestErrorfInheritsContextRedactor(t *testing.T) {
	r := secret.NewRedactor()
	r.Register([]byte("pre-registered-fragment"))

	ctx := secret.WithRedactor(context.Background(), r)

	err := secret.Errorf("ctx=%v leak=%s", ctx, "pre-registered-fragment")
	if strings.Contains(err.Error(), "pre-registered-fragment") {
		t.Fatalf("ctx-carried Redactor did not scrub: %q", err.Error())
	}
}

// TestWrapFiveLayerErrorfChain is the AC-named test: wrap a secret
// in five fmt.Errorf("...: %w", err) layers through secret.Wrap;
// assert no secret bytes appear in the top-level Error().
func TestWrapFiveLayerErrorfChain(t *testing.T) {
	v := secret.New([]byte(errTestPlaintext), secret.Origin{})

	base := fmt.Errorf("base error carrying %s plaintext", errTestPlaintext)
	err := secret.Wrap(base, v)

	// Five layers of fmt.Errorf %w on top of the secret.Wrap.
	// Each layer would naively re-interpolate the inner message
	// (and therefore the plaintext) into its own string. Because
	// secret.Error.Error() scrubs at emission time, the top-level
	// string is clean.
	//
	// Note: fmt.Errorf's %w produces a wrapError whose Error()
	// calls the inner Error() — which in our case is secret.Error
	// scrubbing before returning. So the chain works even though
	// the outer layers are plain fmt.Errorf.
	for i := 0; i < 5; i++ {
		err = fmt.Errorf("layer %d: %w", i, err)
	}

	msg := err.Error()
	if strings.Contains(msg, errTestPlaintext) {
		t.Fatalf("plaintext leaked through 5-layer chain: %q", msg)
	}
	// Sanity: the placeholder should appear, confirming the
	// scrub path ran at least once.
	if !strings.Contains(msg, "***") {
		t.Fatalf("no placeholder in 5-layer chain output: %q", msg)
	}

	// Chain walkability: errors.Is still finds the base.
	if !errors.Is(err, base) {
		t.Fatalf("errors.Is could not walk to base through 5 layers")
	}
}

// TestErrorNilReceiver guards against panics if a caller inspects a
// nil *Error (shouldn't happen, but Error() / Unwrap() must be safe).
func TestErrorNilReceiver(t *testing.T) {
	var e *secret.Error
	if got := e.Error(); got != "" {
		t.Fatalf("nil *Error.Error() = %q, want empty", got)
	}
	if got := e.Unwrap(); got != nil {
		t.Fatalf("nil *Error.Unwrap() = %v, want nil", got)
	}
	if got := e.Redactor(); got != nil {
		t.Fatalf("nil *Error.Redactor() = %v, want nil", got)
	}
}

// TestWrapDoesNotDropOuterLayer guards against a subtle bug in the
// Wrap fast-path: if the type check used errors.As, it would bind
// to the first *Error anywhere in the chain and return it — silently
// discarding any outer fmt.Errorf layers wrapping it (e.g., the
// "reading %s: " prefix below). The correct behavior is to use a
// direct type assertion so outer layers are preserved by wrapping
// into a new *Error.
func TestWrapDoesNotDropOuterLayer(t *testing.T) {
	v := secret.New([]byte("long-enough-secret-value"), secret.Origin{})
	baseErr := fmt.Errorf("permission denied")
	inner := secret.Wrap(baseErr, v)
	outer := fmt.Errorf("reading %s: %w", "path", inner)

	// Re-wrap outer. Wrap must NOT reach past the fmt.Errorf
	// layer and return inner directly — doing so would drop the
	// "reading path: " prefix. It MUST produce a wrapper whose
	// Error() retains the prefix.
	wrapped := secret.Wrap(outer)
	msg := wrapped.Error()
	if !strings.Contains(msg, "reading path:") {
		t.Fatalf("Wrap dropped outer fmt.Errorf layer; got %q, want prefix %q", msg, "reading path:")
	}
}

// TestErrorRedactorAccessor covers the Redactor accessor used by
// callers who want to register additional fragments after the fact.
func TestErrorRedactorAccessor(t *testing.T) {
	v := secret.New([]byte("first-long-enough-secret"), secret.Origin{})
	err := secret.Wrap(fmt.Errorf("wrapped"), v)

	var se *secret.Error
	if !errors.As(err, &se) {
		t.Fatalf("errors.As did not return *Error")
	}
	if se.Redactor() == nil {
		t.Fatalf("Redactor() returned nil on wrapped Error")
	}

	// Register a new fragment after the fact; subsequent
	// Error() calls on the *Error should scrub it. Using
	// secret.Errorf to rewrap ensures the outer layer also goes
	// through a Redactor (plain fmt.Errorf would re-interpolate
	// the fragment into its own format string, which secret.Error
	// cannot scrub from the outside).
	se.Redactor().Register([]byte("late-added-fragment"))
	outer := secret.Errorf("leak late-added-fragment into outer: %w", err)
	msg := outer.Error()
	if strings.Contains(msg, "late-added-fragment") {
		t.Fatalf("late-registered fragment not scrubbed: %q", msg)
	}
}
