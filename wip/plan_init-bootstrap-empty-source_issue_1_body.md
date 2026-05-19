---
complexity: testable
complexity_rationale: |
  This issue introduces a new typed error (`*github.StatusError`), a new classifier
  helper (`classifyMaterializeError`), and modifies five existing error-construction
  sites plus their test fakes. Every change is exercisable by unit tests:
  - StatusError's `Error()` text is asserted verbatim against today's strings.
  - The four fetch.go call sites and the snapshotwriter.go wrap are reachable via
    existing fake HTTP transports already used in `snapshotwriter_test.go`.
  - The classifier's precedence is validated by a table-driven test that constructs
    error chains satisfying multiple arms simultaneously (ambiguous + 401, etc.).
  - Each arm's Detail/Suggestion text is asserted via exact-substring matching
    against PRD R10/R11 strings.
  No user-visible behavior changes in this issue (runInit still uses the bare wrap),
  so there is nothing to verify end-to-end -- the unit-test surface is sufficient.
---

## Goal

Introduce a typed `*github.StatusError`, fix the fifth error-wrap site so `errors.As` can reach it, and add a `classifyMaterializeError` helper with PRD-N2 precedence -- all without changing user-visible behavior.

## Context

Today, `runInit` distinguishes "no marker" from "ambiguous markers" from "HTTP 401/403" from "HTTP 404" by string-matching the wrapped error message. That is fragile: any rewording of a `fmt.Errorf` string in `internal/github/fetch.go` silently regresses the init UX. PRD R10/R11/R12 require deterministic, exact-substring guidance for each failure mode, and PRD N2 fixes a most-specific-first precedence order. This issue lays the typed-error foundation that <<ISSUE:2>>, <<ISSUE:3>>, and <<ISSUE:4>> all build on.

Design: `docs/designs/DESIGN-init-bootstrap-empty-source.md`

## Acceptance Criteria

- [ ] New file `internal/github/errors.go` defines `type StatusError struct { StatusCode int; Message string; URL string }` with an `Error()` method that preserves today's exact wrapped text (so existing string-matching callers continue to work until <<ISSUE:2>> removes them).
- [ ] All four error-construction sites in `internal/github/fetch.go` (lines 69, 72, 145, 149) return `&StatusError{StatusCode: ..., Message: ..., URL: ...}` instead of `fmt.Errorf("...")` strings.
- [ ] The fifth wrap at `internal/workspace/snapshotwriter.go:503` (inside `materializeFromGitHub`) switches from `fmt.Errorf("...: %s", err)` to `fmt.Errorf("...: %w", err)` so `errors.As(err, &target)` can reach the typed `*github.StatusError` value.
- [ ] The four test fakes in `internal/workspace/snapshotwriter_test.go` that previously produced string errors are updated to construct `&github.StatusError{StatusCode: ...}` directly.
- [ ] New file `internal/cli/init_classifier.go` defines `classifyMaterializeError(err error, hasBootstrap bool) (*workspace.InitConflictError, error)`. The helper does NOT yet replace the call site in `runInit` -- it is constructed and unit-tested in isolation.
- [ ] Classifier precedence (per PRD N2) is ordered most-specific-first:
  1. `*config.AmbiguousMarkersError`
  2. `*config.NoMarkerError`
  3. `*github.StatusError` with `StatusCode == 401 || StatusCode == 403`
  4. `*github.StatusError` with `StatusCode == 404`
  5. Generic fall-through (returns the original error unchanged)
- [ ] Table-driven test in `internal/cli/init_classifier_test.go` exercises error chains that satisfy multiple arms simultaneously (e.g. an `AmbiguousMarkersError` wrapping a `StatusError{401}`, a `NoMarkerError` wrapping a `StatusError{404}`) to prove the precedence order rather than incidental Go type-switch behavior.
- [ ] The 401/403 arm emits a `*workspace.InitConflictError` whose `Detail`+`Suggestion` together contain the exact substring `verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope` (PRD R10).
- [ ] The 404 arm emits a `*workspace.InitConflictError` whose `Detail`+`Suggestion` together contain all three PRD R11 substrings (repo-not-found wording, network-check hint, `--bootstrap`-retry mention).
- [ ] The ambiguous arm preserves today's `*config.AmbiguousMarkersError.Error()` text verbatim in `Detail` (no rewording).
- [ ] `*workspace.InitConflictError` gains an `ExitCode int` field (or, if a sentinel pattern is cleaner, a parallel `ExitCode()` method) carrying the PRD R23 exit codes (e.g. 2 for ambiguous, 3 for no-marker, 4 for auth, 5 for not-found).
- [ ] **User-visible state after this issue: NO CHANGE.** The classifier is constructed and tested but `runInit` still uses the bare wrap. The behavior swap lands in <<ISSUE:2>>.
- [ ] Unit tests cover each typed-error arm with exact-substring assertions on `Detail`+`Suggestion` (not regex, not contains-any -- the test must fail if the PRD-mandated wording drifts).

## Dependencies

None.

## Downstream Dependencies

- <<ISSUE:2>> wires `classifyMaterializeError` into `runInit`, replacing the string-match block, and adds the `--bootstrap`-retry hint to the 404 arm's user-facing output.
- <<ISSUE:3>>'s new `GetRepo` call returns `*github.StatusError` directly, relying on the type defined here.
- <<ISSUE:4>>'s bootstrap dispatch keys off the classifier's `NoMarker` arm, so the precedence order frozen here is load-bearing for that issue.
