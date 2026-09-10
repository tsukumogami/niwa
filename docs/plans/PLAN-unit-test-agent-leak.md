---
schema: plan/v1
status: Draft
execution_mode: single-pr
tracking_level: none
milestone: "Unit tests never launch a real agent"
issue_count: 1
---

# PLAN: Unit tests never launch a real agent

## Status

Draft

## Scope Summary

Running `go test ./...` on a machine that has `claude` on PATH starts a real
background Claude Code session with the prompt "do the thing". The session
registers in the developer's `claude agents` view, its working directory is a
`t.TempDir()` that Go deletes when the test ends, and nothing ever removes it,
so every local run of the `internal/cli` package leaves one orphaned session
behind. The source is `TestNonEmptyBodyWithEmptyPrefixIsAccepted` in
`internal/cli/dispatch_promptsplit_test.go`: it calls `realDispatchLaunch` with
the Claude launch spec and relies on the binary lookup failing, which is true
in CI (no `claude` installed) and false on a developer machine. The test also
passes whether or not a session starts, so the leak never surfaced as a
failure.

This PLAN fixes that test and adds a package-level guard so no unit test in
`internal/cli` can reach a real agent binary again. The opt-in live tests
(`test/live`, behind the `live` build tag, and the `internal/watch` live gates)
launch real sessions on purpose and already clean up; they are out of scope.

## Decomposition Strategy

**Single issue.** The test fix and the guard ship together because the guard
is what proves the fix: with the guard in place and the fix absent, the
package fails, which is the regression signal the old test never gave. Split
apart, the guard alone would fail the suite and the fix alone would leave the
next leak just as silent.

## Issue Outlines

### Issue 1: fix(cli): keep unit tests from launching real agent sessions

**Goal**: No test in `internal/cli` can start a real `claude` or `codex`
process, and the prompt-split test proves what it claims without doing so.

**Acceptance Criteria**:
- `TestNonEmptyBodyWithEmptyPrefixIsAccepted` clears PATH before calling
  `realDispatchLaunch` and asserts the returned error is the binary-not-found
  error, so it passes only when the empty-prompt guard let the body through
  and fails if the launcher ever gets further than the lookup.
- A `TestMain` in `internal/cli` puts a directory first on PATH holding `claude`
  and `codex` stubs that record each invocation and exit non-zero without
  starting anything; after the tests run, it fails the package, naming the
  arguments, if any stub was invoked.
- Tests that override PATH themselves (clearing it, or prepending their own
  fake) keep working unchanged.
- `go test ./internal/cli/` passes on a machine with `claude` on PATH and
  leaves no new entry in `~/.claude/jobs`.
- Removing the PATH fix from `TestNonEmptyBodyWithEmptyPrefixIsAccepted`
  makes `go test ./internal/cli/` fail with the guard's message.

**Dependencies**: None.

**Type**: code

**Files**: `internal/cli/dispatch_promptsplit_test.go`, `internal/cli/main_test.go`

## Implementation Issues

_Empty in single-pr mode: the work items are the Issue Outlines above._

## Dependency Graph

_None: a single issue has no inter-issue edges._

## Implementation Sequence

Implement Issue 1 on the shared branch: fix the test first, then add the
`TestMain` guard, then confirm the guard catches the original leak by
temporarily reverting the fix.
