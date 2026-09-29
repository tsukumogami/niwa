package vault

import (
	"context"
	"sync"
)

// RunState remembers, for one provisioning run, which API domains a
// backend has already found unreachable and on which a CLI-session
// principal was found logged out, so later calls in the same run can
// skip a subprocess that would only fail the same way again. It is
// safe for concurrent use.
//
// A nil *RunState is valid and records nothing: every Mark is a no-op
// and Check answers that nothing is recorded. Paths that attach no
// state (status checks, onboarding) therefore behave as before.
type RunState struct {
	mu      sync.Mutex
	domains map[string]*domainVerdict
}

// domainVerdict holds the first verdict of each kind recorded for one
// domain, and which of the two came first.
type domainVerdict struct {
	unreachable       bool
	unreachableReason Reason
	unauthenticated   bool
	// unauthFirst is true when the unauthenticated verdict was
	// recorded before any unreachable one.
	unauthFirst bool
}

type runStateKey struct{}

// WithRunState returns a child of ctx carrying a fresh RunState.
func WithRunState(ctx context.Context) context.Context {
	return context.WithValue(ctx, runStateKey{}, &RunState{})
}

// RunStateFrom returns the RunState ctx carries, or nil when none is
// attached. The nil result is safe to call methods on.
func RunStateFrom(ctx context.Context) *RunState {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(runStateKey{}).(*RunState)
	return s
}

// verdict returns the entry for domain, creating it. Callers hold mu.
func (s *RunState) verdict(domain string) *domainVerdict {
	if s.domains == nil {
		s.domains = map[string]*domainVerdict{}
	}
	v, ok := s.domains[domain]
	if !ok {
		v = &domainVerdict{}
		s.domains[domain] = v
	}
	return v
}

// MarkUnreachable records that domain could not be reached, for every
// principal. Only the first unreachable reason recorded for a domain
// is kept.
func (s *RunState) MarkUnreachable(domain string, reason Reason) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.verdict(domain)
	if v.unreachable {
		return
	}
	v.unreachable = true
	v.unreachableReason = reason
}

// MarkUnauthenticated records that a CLI-session principal was logged
// out or expired on domain. It applies only to CLI-session principals.
func (s *RunState) MarkUnauthenticated(domain string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.verdict(domain)
	if v.unauthenticated {
		return
	}
	v.unauthenticated = true
	v.unauthFirst = !v.unreachable
}

// Check returns the verdict recorded for domain that applies to a
// principal, and whether there is one. An unreachable verdict applies
// to every principal; an unauthenticated one only when minted is
// false. When both apply, the one recorded first wins, so the reason
// a run reports for a domain never changes after its first failure.
func (s *RunState) Check(domain string, minted bool) (FailureClass, bool) {
	if s == nil {
		return FailureClass{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.domains[domain]
	if !ok {
		return FailureClass{}, false
	}
	unauth := FailureClass{Class: ClassUnauthenticated, Reason: ReasonLoggedOut}
	unreach := FailureClass{Class: ClassUnreachable, Reason: v.unreachableReason}
	switch {
	case !minted && v.unauthenticated && (v.unauthFirst || !v.unreachable):
		return unauth, true
	case v.unreachable:
		return unreach, true
	}
	return FailureClass{}, false
}
