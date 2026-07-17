---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-niwa-watch-pr-hardening.md
milestone: "niwa watch PR-wedge hardening"
issue_count: 5
---

# PLAN: niwa watch PR-wedge hardening

## Status

Active

## Scope Summary

Harden the shipped `niwa watch --once` PR-review wedge per
`DESIGN-niwa-watch-pr-hardening.md`: SHA-aware re-dispatch state, a deterministic
freshness re-validation with a staged-record GC, a cross-run total-staged cap, a
level/edge trigger-semantics declaration, and — as the deferrable tail — live-idle
session continuation via stop-and-resume. One PR against `niwa`.

## Decomposition Strategy

**Horizontal, on the state spine.** The design decomposes into layered slices over
one shared spine (the two-store split by lifetime): the SHA-aware handled-set, the
pure re-dispatch decision, the freshness predicate + staged-record GC, the cap, and
the net-new continuation mechanism. Each layer has a stable interface to the next
(handled-set -> decision -> pass wiring -> cap -> continuation), so horizontal
component-by-component fits better than a walking skeleton — there is already a
working end-to-end wedge (ED1); this thickens it. The layers are sequenced so each
lands observable hardening and the single novel, higher-risk piece (continuation)
is last and independently deferrable.

## Issue Outlines

### Issue 1: feat(watch): SHA-aware handled-set with dual-format migration and trigger-semantics

**Goal**: Evolve the handled-set from SHA-blind `owner/repo#number` lines to a
SHA-aware format recording the last-dispatched head SHA per PR, with a dual-format
parser that migrates legacy entries without a re-fire storm, plus a level/edge
trigger-semantics declaration on the state contract.

**Acceptance Criteria**:
- [ ] `HandledKey`/`isHandledKey` parse and validate `owner/repo#number@<sha>` (hex-SHA charset) alongside legacy SHA-less lines; malformed lines still skip, never fatal.
- [ ] `LoadHandledSet` returns a per-PR map to last-dispatched SHA; a legacy line parses as "handled at unknown SHA".
- [ ] `AppendHandled` writes the SHA-aware shape; a legacy unknown-SHA entry adopts the current head on first observation without re-staging (no upgrade storm).
- [ ] A level/edge trigger-semantics declaration is recorded in the state contract (header/version line the dual-format parser tolerates); the PR source declares `level`.
- [ ] Unit tests cover: new-shape round-trip, legacy line -> unknown-SHA, migration adopt-without-restage, malformed skip.
- [ ] `go test ./... && go vet ./... && gofmt -l` clean.

**Dependencies**: None

**Type**: code
**Files**: `internal/watch/state.go`, `internal/watch/state_test.go`

### Issue 2: feat(watch): SHA-keyed re-dispatch decision function

**Goal**: Add a pure decision function producing a per-PR plan
(`Fresh`/`Noop`/`Defer`; `Continue` reserved for Issue 5) from (last-dispatched SHA,
current head, session liveness), and wire it into `runWatchOnce` in place of the
SHA-blind skip.

**Acceptance Criteria**:
- [ ] A pure function maps (requested PRs, handled-set-with-SHAs, live staged records, scope, bound) to per-PR plans, table-testable like `Select`.
- [ ] Unchanged head -> `Noop`; new head + no live record -> `Fresh`; new head + live record -> `Defer` (until Issue 5 flips it to `Continue`).
- [ ] A dismissed or crashed-and-reaped session counts as no live record -> `Fresh` on new head.
- [ ] `runWatchOnce` applies the plan; fresh stages record `handled@head` only on success (fail-closed preserved); a poll failure still exits non-zero (fail-loud).
- [ ] Unit tests cover the decision matrix: re-fire on new SHA, no-op on unchanged SHA, fresh after dismissal/reap, defer while live.
- [ ] `go test ./... && go vet ./... && gofmt -l` clean.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/watch/select.go`, `internal/cli/watch.go`, `internal/watch/select_test.go`

### Issue 3: feat(watch): unblock-time freshness re-validation and staged-record GC

**Goal**: Add `InstancePath` to the StagedRecord as the liveness anchor, a
deterministic freshness predicate (PR open, still requesting, dispatched SHA still an
ancestor of head), a watcher-pass prune that discards stale/dead records (the
record-layer GC), and a session pre-flight subcommand for the unblock-time check.

**Acceptance Criteria**:
- [ ] StagedRecord carries `InstancePath`, validated to resolve under the managed instances root before use.
- [ ] A pure `freshness(record, pollState) -> ok | reason` predicate distinguishes force-push/rebase (dispatched SHA no longer an ancestor) from ordinary advancement.
- [ ] Each pass prunes records that are dead (no live job) or fail freshness, stopping/reaping their instances; the record store stops growing monotonically.
- [ ] A niwa subcommand runs the same predicate as a deterministic session pre-flight; a stale staged review self-discards with the failed-condition reason and posts nothing. The watcher-pass prune is the backstop.
- [ ] Unit tests cover: freshness happy-path (fresh review NOT discarded), each discard condition (closed/merged, un-requested, force-pushed), and dead-record prune.
- [ ] `go test ./... && go vet ./... && gofmt -l` clean.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>

**Type**: code
**Files**: `internal/watch/state.go`, `internal/watch/freshness.go`, `internal/cli/watch.go`, `internal/watch/freshness_test.go`

### Issue 4: feat(watch): total-staged concurrency cap

**Goal**: Add a cross-run cap on live staged agents: a `watch_max_staged` global
setting (following the `watch_sandbox` pattern) with a `DefaultMaxStaged` const,
counting records whose instance has a live job, composed with the per-run bound.

**Acceptance Criteria**:
- [ ] `GlobalSettings.WatchMaxStaged int` (`toml:"watch_max_staged,omitempty"`) with a use-site `resolveMaxStaged` (default `DefaultMaxStaged = 5` when <= 0, reject negatives with a hard error like `resolveSandboxMode`).
- [ ] The cap counts live staged records via `instanceHasLiveJob`; `runWatchOnce` passes `min(DefaultPerRunBound, maxStaged - liveCount)` as `Select`'s bound and short-circuits when remaining <= 0, leaving un-staged PRs unrecorded (oldest-first backfill preserved).
- [ ] Continuing a live-idle session (Issue 5) does not consume cap capacity (only `Fresh` does).
- [ ] Unit tests cover: cap reached -> no fresh stage + remaining PRs unrecorded + backfill oldest-first after capacity frees; per-run bound and cap both enforced (min of the two).
- [ ] `go test ./... && go vet ./... && gofmt -l` clean.

**Dependencies**: Blocked by <<ISSUE:3>>

**Type**: code
**Files**: `internal/config/registry.go`, `internal/watch/select.go`, `internal/cli/watch.go`, `internal/watch/select_test.go`

### Issue 5: feat(watch): live-idle session continuation (stop-and-resume)

**Goal**: Build the net-new continuation capability — capture a resume-usable
conversation id for a watch session, classify idle/busy/attached, and continue a
detached-idle session via stop-and-resume in a fresh checkout through the
fresh-dispatch launch wrapper — flipping the live-idle branch from `Defer` to
`Continue`. Independently deferrable per the design.

**Acceptance Criteria**:
- [ ] Establish whether a resume-usable conversation id can be captured for a watch session (extending the `dispatch_capture` cwd-correlation); if not, the live-idle branch stays `Defer` and this is recorded as a documented non-goal (the rest of the issue still lands its detection/tests).
- [ ] StagedRecord persists `SessionID`/`ConversationID`/`ShortID`, each charset-validated (`isSafeHandle` precedent) before becoming a CLI argument.
- [ ] An idle/busy/attached classifier reads the job `state.json` fields niwa currently ignores; unreadable -> `Defer` (never a wrong `Continue`); attached -> `Defer` even if idle.
- [ ] Continuation stops the session and `claude --resume`s it in a freshly `FetchPRHead`-checked-out clone at the new head with a fixed-template re-review prompt (no PR-derived text in any command/argument), re-asserting containment via the fresh-dispatch launch wrapper and failing closed if it cannot be enforced.
- [ ] Liveness cross-checked two ways (`sessionLive` on id AND `instanceHasLiveJob` on InstancePath); mismatch degrades to `Fresh`/`Defer`.
- [ ] Tests: unit tests for the classifier and the decision flip (live-idle -> Continue, live-busy/attached -> Defer, coalesce-to-latest, cap-neutral); an integration test asserting a *resumed* watch session's Bash egress is denied at the OS layer (not just hooks present).
- [ ] `go test ./... && go vet ./... && gofmt -l` clean.

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:4>>

**Type**: code
**Files**: `internal/cli/watch.go`, `internal/watch/state.go`, `internal/watch/continuation.go`, `internal/cli/sessionattach/supervise.go`

## Dependency Graph

Single-pr mode; dependencies are declared inline per outline. The edges, as a
textual DAG:

- `#1` -> `#2` (decision keys on the SHA state)
- `#1`, `#2` -> `#3` (record fields + prune wired alongside the decision in the pass)
- `#3` -> `#4` (cap counts the live records #3 makes prunable)
- `#2`, `#3`, `#4` -> `#5` (continuation flips the decision, needs the record refs, is cap-neutral)

The graph is a single linear spine `#1 -> #2 -> #3 -> #4 -> #5` with #3 also fed
directly by #1 and #5 also fed directly by #2.

## Implementation Sequence

**Critical path:** #1 -> #2 -> #3 -> #4 -> #5 (linear on the state spine).

- **#1** is the foundation (SHA-aware state); nothing else can be keyed on new
  activity without it.
- **#2** turns the SHA state into the re-dispatch decision, still suppress-while-live.
- **#3** adds the record lifecycle (InstancePath + freshness + GC) that both bounds
  store growth and makes the cap countable.
- **#4** adds the cap on top of #3's live-count.
- **#5** is the deferrable tail. Everything before it is real, shippable hardening
  (re-fire, freshness self-discard, cap). If #5 exceeds one PR's scope, land #1-#4
  and fast-follow #5; the `Defer` fallback keeps the two-live-sessions invariant and
  never regresses. Start #5 with the conversation-id capture feasibility check — it
  gates whether continuation ships or reduces to a documented non-goal this pass.

**Parallelization:** limited by the linear spine; within an issue, the pure-logic
unit tests can be written test-first (TDD) against the decision/freshness/cap
functions before the `runWatchOnce` wiring.
