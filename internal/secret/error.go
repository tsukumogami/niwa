package secret

import (
	"context"
	"errors"
	"fmt"
)

// Error wraps an inner error together with a Redactor. Its Error()
// method returns the inner error's string scrubbed by the Redactor.
// Unwrap exposes the inner chain so errors.Is and errors.As continue
// to work identically to a naked fmt.Errorf wrap.
//
// Error preserves its Redactor across re-wraps: if an Error is
// wrapped again by a later Wrap/Errorf call, the outer Error reuses
// the same Redactor so every fragment registered at any depth is
// scrubbed from the top-level string.
type Error struct {
	inner    error
	redactor *Redactor
}

// Error returns the inner error's message with all registered
// fragments replaced by "***". If the Redactor is nil (which should
// not happen for Errors produced by this package, but is defended
// against for robustness), the inner message is returned unmodified.
func (e *Error) Error() string {
	if e == nil || e.inner == nil {
		return ""
	}
	msg := e.inner.Error()
	if e.redactor == nil {
		return msg
	}
	return e.redactor.Scrub(msg)
}

// Unwrap returns the wrapped error, preserving the chain for
// errors.Is and errors.As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.inner
}

// Redactor returns the Redactor associated with this Error. Used
// internally when re-wrapping; exposed so callers who want to
// register additional fragments on the chain's Redactor can do so.
func (e *Error) Redactor() *Redactor {
	if e == nil {
		return nil
	}
	return e.redactor
}

// Wrap returns a *Error that scrubs err's message through a Redactor
// carrying the bytes of every value in values. If err is nil and no
// values are supplied, Wrap returns nil.
//
// Wrap's Redactor-selection policy:
//
//  1. If err is already (or wraps) a *Error, reuse that Error's
//     Redactor. This preserves fragment visibility across deep chains
//     — every fragment registered at any level scrubs the top-level
//     message.
//  2. Otherwise, a fresh Redactor is allocated.
//
// WithRedactor-scoped context is NOT consulted by Wrap: context
// plumbing happens through Errorf, which sees the caller-supplied
// arg list. Wrap is intentionally a pure function of err + values so
// it can be called from code paths that don't have a context handy.
func Wrap(err error, values ...Value) error {
	if err == nil {
		return nil
	}
	r := inheritRedactor(err)
	if r == nil {
		r = NewRedactor()
	}
	for _, v := range values {
		r.RegisterValue(v)
	}
	// If err is already a *Error with the same Redactor, we don't
	// need a new wrapper layer — just return it so the chain stays
	// shallow. If the Redactor pointer differs, wrap so Error()
	// uses the updated fragment set.
	var existing *Error
	if errors.As(err, &existing) && existing.redactor == r {
		return existing
	}
	return &Error{inner: err, redactor: r}
}

// Errorf is fmt.Errorf with secret-scrubbing. It:
//
//   - Auto-registers any Value or *Value in args on a Redactor
//     inherited from the args or from a Redactor attached to a
//     context.Context among the args (via WithRedactor), or a fresh
//     one if neither is present.
//   - Preserves %w semantics: errors.Is and errors.As walk the
//     resulting chain exactly as they would for fmt.Errorf.
//   - Returns a *Error whose Error() output is the fmt.Errorf string
//     with every registered fragment replaced by "***".
//
// If args contain no Value and no existing *Error to inherit from,
// Errorf still returns a *Error carrying a fresh Redactor; the
// message will equal the fmt.Errorf output since no fragments are
// registered. This keeps the return type stable so callers can rely
// on errors.As(err, &*secret.Error{}) succeeding.
func Errorf(format string, args ...any) error {
	r := pickRedactor(args)
	if r == nil {
		r = NewRedactor()
	}
	for _, a := range args {
		switch v := a.(type) {
		case Value:
			r.RegisterValue(v)
		case *Value:
			if v != nil {
				r.RegisterValue(*v)
			}
		}
	}
	inner := fmt.Errorf(format, args...)
	return &Error{inner: inner, redactor: r}
}

// inheritRedactor walks err's chain and returns the Redactor of the
// first *Error it finds, or nil. This lets Wrap/Errorf share a
// single Redactor across deep error-wrap chains.
func inheritRedactor(err error) *Redactor {
	var se *Error
	if errors.As(err, &se) {
		return se.redactor
	}
	return nil
}

// pickRedactor scans args for (in order of preference):
//
//  1. An *Error (or an error wrapping one via %w): reuse its
//     Redactor so the whole chain shares fragments.
//  2. A context.Context that carries a Redactor via WithRedactor.
//
// Returns nil if neither is found; Errorf then allocates a fresh
// Redactor.
func pickRedactor(args []any) *Redactor {
	// First pass: existing *Error in the chain wins (covers the
	// "wrap a Wrapped error with another Errorf" case).
	for _, a := range args {
		if e, ok := a.(error); ok {
			if r := inheritRedactor(e); r != nil {
				return r
			}
		}
	}
	// Second pass: context-attached Redactor.
	for _, a := range args {
		if ctx, ok := a.(context.Context); ok {
			if r := RedactorFrom(ctx); r != nil {
				return r
			}
		}
	}
	return nil
}
