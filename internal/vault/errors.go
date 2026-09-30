package vault

import (
	"errors"
	"fmt"
)

// ErrKeyNotFound is returned by Provider.Resolve when the requested
// key does not exist in the backend. Callers check via errors.Is; the
// resolver stage consults this sentinel to decide whether to
// downgrade a missing optional key to an empty value.
var ErrKeyNotFound = errors.New("vault: key not found")

// ErrProviderUnreachable is returned by Provider.Resolve (or
// Factory.Open) when the backend cannot be contacted: auth failure,
// network error, missing CLI binary, expired session. Callers check
// via errors.Is.
//
// Matching it does NOT mean a failure is one the secret store may serve
// stale values for. Every unauthenticated or unreachable failure wraps
// it, but so do some answered ones: a 401 or 403 the backend returned
// for a session it vouches for still carries the "(auth failure)"
// wording and this sentinel, so the callers that have always tolerated
// it keep doing so (the resolver marks the key unresolved with
// CauseProviderUnreachable instead of failing the run, and credential
// sync reports it as an unreachable vault). Only the
// FailureClass on a ClassifiedError decides whether a failure is
// servable; read it with errors.As.
var ErrProviderUnreachable = errors.New("vault: provider unreachable")

// ErrClientNotInstalled narrows ErrProviderUnreachable to the case
// where the backend's client binary is absent from the host — the
// process never started, as opposed to starting and failing to
// authenticate.
//
// It wraps ErrProviderUnreachable, so every existing
// errors.Is(err, ErrProviderUnreachable) check keeps matching. Callers
// that need the narrower answer must test for ErrClientNotInstalled
// FIRST, since the broader sentinel matches both.
//
// The distinction exists because the two have different remedies: one
// is "install the client", the other is "repair credentials or
// connectivity". Reporting collapses them only at its own peril.
var ErrClientNotInstalled = fmt.Errorf("%w: client binary not installed", ErrProviderUnreachable)

// ErrProviderNameCollision is returned when a personal overlay
// declares a provider whose name is already declared by the team
// config. Per R12 (vault-integration design), the personal overlay
// may ADD new provider names but cannot REPLACE team-declared ones.
var ErrProviderNameCollision = errors.New("vault: personal overlay cannot replace team-declared provider")

// ErrTeamOnlyLocked is returned when a personal overlay attempts to
// set a value for a key that the team config marked as team_only.
// Per R8, team_only keys are not overridable by personal overlays.
var ErrTeamOnlyLocked = errors.New("vault: key is locked by team_only")

// FailureKind is the class a backend gives a failed resolution. It
// travels on the error (see ClassifiedError) so policy above the
// backend can act on it without knowing the backend's wording.
type FailureKind int

const (
	// ClassUnauthenticated: the principal's login is missing, expired
	// or rejected.
	ClassUnauthenticated FailureKind = iota + 1
	// ClassUnreachable: the backend could not be reached, or did not
	// answer in time.
	ClassUnreachable
	// ClassAnswered: the backend answered, and the answer was a
	// failure (a missing folder, a server error, a refusal for a
	// principal the backend vouches for).
	ClassAnswered
)

// String names the class for tests and debugging. It is never part of
// a user-facing message.
func (k FailureKind) String() string {
	switch k {
	case ClassUnauthenticated:
		return "unauthenticated"
	case ClassUnreachable:
		return "unreachable"
	case ClassAnswered:
		return "answered"
	}
	return fmt.Sprintf("FailureKind(%d)", int(k))
}

// Reason says why an unauthenticated or unreachable failure happened.
// It is the zero value for an answered failure.
type Reason int

const (
	// ReasonLoggedOut goes with every ClassUnauthenticated failure.
	ReasonLoggedOut Reason = iota + 1
	// ReasonTimedOut goes with a ClassUnreachable failure whose call,
	// or the probe that classified it, reached its time bound.
	ReasonTimedOut
	// ReasonUnreachable goes with every other ClassUnreachable failure.
	ReasonUnreachable
)

// String names the reason for tests and debugging.
func (r Reason) String() string {
	switch r {
	case 0:
		return "none"
	case ReasonLoggedOut:
		return "logged out"
	case ReasonTimedOut:
		return "timed out"
	case ReasonUnreachable:
		return "unreachable"
	}
	return fmt.Sprintf("Reason(%d)", int(r))
}

// FailureClass is the classification a backend attaches to a failed
// resolution. HTTPStatus is the status of the server response the
// failure carried, or 0 when there was none.
type FailureClass struct {
	Class      FailureKind
	Reason     Reason
	HTTPStatus int
}

// Error lets errors.As find a FailureClass in a ClassifiedError's
// chain. The text is never shown: ClassifiedError.Error returns only
// the inner error's text.
func (f *FailureClass) Error() string {
	return fmt.Sprintf("vault: failure classified %s (reason %s, http status %d)", f.Class, f.Reason, f.HTTPStatus)
}

// ClassifiedError carries a backend error together with its
// classification. Error returns the inner error's text unchanged, so
// adding a class never alters what a user reads. Unwrap exposes both
// the inner error (errors.Is keeps finding every sentinel it carries)
// and the class (errors.As finds *FailureClass). It is a named type
// rather than errors.Join or an extra %w because either would change
// the text.
type ClassifiedError struct {
	Err   error
	Class FailureClass
}

// Error returns the inner error's text, byte for byte.
func (e *ClassifiedError) Error() string { return e.Err.Error() }

// Unwrap returns the inner error and the class.
func (e *ClassifiedError) Unwrap() []error { return []error{e.Err, &e.Class} }

// Classify attaches class to err and is how a backend should build a
// ClassifiedError. An unauthenticated or unreachable failure must match
// ErrProviderUnreachable (callers that soften that condition, such as
// credential sync and the key report, keep doing so), but its text must
// stay what the backend said: when err doesn't already carry the
// sentinel, Classify adds it without touching the text. An answered
// failure keeps exactly the sentinels err has. err must not be nil.
func Classify(err error, class FailureClass) error {
	if class.Class != ClassAnswered && !errors.Is(err, ErrProviderUnreachable) {
		err = &unreachableError{err: err}
	}
	return &ClassifiedError{Err: err, Class: class}
}

// unreachableError keeps an error's text exactly while adding
// ErrProviderUnreachable to its chain.
type unreachableError struct{ err error }

func (e *unreachableError) Error() string { return e.err.Error() }

func (e *unreachableError) Unwrap() []error { return []error{e.err, ErrProviderUnreachable} }
