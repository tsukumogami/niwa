package cli

import (
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// classifyMaterializeError translates the error returned by
// workspace.MaterializeFromSource into a *workspace.InitConflictError
// carrying user-actionable Detail+Suggestion text plus a PRD R23 exit
// code. The helper is constructed and unit-tested in isolation by Issue
// 1; the runInit call site continues to use the bare wrap until the
// follow-up issue swaps it in.
//
// Precedence (most-specific-first, per PRD N2):
//
//  1. *config.AmbiguousMarkersError — Detail is the typed error's
//     existing Error() text verbatim.
//  2. *config.NoMarkerError —
//     when hasBootstrap is true, returns (nil, nil) so the caller can
//     dispatch to the bootstrap flow (PRD R13 prompt seam). When
//     hasBootstrap is false, returns an InitConflictError whose
//     Suggestion includes the `--bootstrap` retry hint (PRD R11) and
//     ExitCode=4.
//  3. *github.StatusError with StatusCode 401 or 403 — Suggestion
//     contains the exact PRD R10 GH_TOKEN scopes substring.
//  4. *github.StatusError with StatusCode 404 — Suggestion contains the
//     three PRD R11 substrings (slug check, private-repo hint,
//     brand-new-empty-repo hint).
//  5. Fall-through — returns (nil, err) so the caller's existing wrap
//     stays in effect.
//
// errors.As walks the entire chain at each step, so an error satisfying
// multiple arms simultaneously is resolved deterministically by the
// order of the As checks below.
func classifyMaterializeError(err error, hasBootstrap bool) (*workspace.InitConflictError, error) {
	if err == nil {
		return nil, nil
	}

	// Arm 1: ambiguous markers — never fall back to a status arm, even
	// if a *github.StatusError sits deeper in the chain.
	var ambigErr *config.AmbiguousMarkersError
	if errors.As(err, &ambigErr) {
		return &workspace.InitConflictError{
			Err:        err,
			Detail:     ambigErr.Error(),
			Suggestion: "",
			ExitCode:   1,
		}, nil
	}

	// Arm 2: no markers found. With --bootstrap, return (nil, nil) so
	// the caller can dispatch to RunBootstrap. Without --bootstrap,
	// emit a conflict carrying the retry hint and ExitCode=4 per R23.
	var noMarkerErr *config.NoMarkerError
	if errors.As(err, &noMarkerErr) {
		if hasBootstrap {
			return nil, nil
		}
		return &workspace.InitConflictError{
			Err:    err,
			Detail: noMarkerErr.Error(),
			Suggestion: "If the repository is brand new and has no commits yet, " +
				"push at least one commit (an empty README is enough) and retry with --bootstrap.",
			ExitCode: 4,
		}, nil
	}

	// Arms 3 and 4: typed HTTP status error from GitHub. Walk the
	// chain twice so arm 3 (401/403) beats arm 4 (404) per PRD N2
	// even when the chain carries both (e.g., a 404 outer wrapping a
	// 401 inner). errors.As stops at the first match, so a single walk
	// would let whichever StatusError sits outermost decide — that's
	// the wrong precedence for arm-3-vs-arm-4 conflicts.
	if statusErr := findStatusError(err, func(s *github.StatusError) bool {
		return s.StatusCode == 401 || s.StatusCode == 403
	}); statusErr != nil {
		return &workspace.InitConflictError{
			Err: err,
			Detail: fmt.Sprintf("GitHub returned HTTP %d while fetching the config repo.",
				statusErr.StatusCode),
			Suggestion: "verify GH_TOKEN scopes; fine-grained PATs need Contents: read, " +
				"classic PATs need repo scope.",
			ExitCode: 1,
		}, nil
	}
	if statusErr := findStatusError(err, func(s *github.StatusError) bool {
		return s.StatusCode == 404
	}); statusErr != nil {
		return &workspace.InitConflictError{
			Err: err,
			Detail: "GitHub returned HTTP 404 while fetching the config repo. " +
				"verify the slug is correct (org/repo) and the repo exists.",
			Suggestion: "if the repo is private, set GH_TOKEN with read access. " +
				"if the repo is brand new and has no commits yet, push at least one " +
				"commit (an empty README is enough) and retry with --bootstrap.",
			ExitCode: 1,
		}, nil
	}

	// Fall-through: hand the original error back so the caller's
	// existing wrap (today's "materializing config repo: %w") stays in
	// effect.
	return nil, err
}

// findStatusError walks err's full error chain (single-target Unwrap()
// and multi-target Unwrap() []error) looking for the first
// *github.StatusError satisfying match. Returns nil when no such error
// exists. Used by classifyMaterializeError to give arm 3 (401/403)
// precedence over arm 4 (404) regardless of which one sits outermost
// in the chain.
func findStatusError(err error, match func(*github.StatusError) bool) *github.StatusError {
	if err == nil {
		return nil
	}
	if s, ok := err.(*github.StatusError); ok && match(s) {
		return s
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return findStatusError(u.Unwrap(), match)
	case interface{ Unwrap() []error }:
		for _, inner := range u.Unwrap() {
			if found := findStatusError(inner, match); found != nil {
				return found
			}
		}
	}
	return nil
}
