---
status: Done
problem: |
  The shipped `niwa watch --once` PR-review wedge is not trustworthy across
  repeated runs. Its dedup state is a flat, permanent, SHA-blind handled-set,
  so a genuine re-request after new commits never re-fires and the reviewer
  session's accumulated context is thrown away; no check re-validates a staged
  review at unblock time, so a merged, closed, un-requested, or force-pushed PR
  still presents a postable draft; and only a per-pass bound exists, so live
  staged agents accumulate across runs without a ceiling.
goals: |
  Make repeated `watch --once` runs trustworthy: a re-request after new commits
  continues the developer's existing review session when it survives (or stages
  a fresh one when it does not), a staged review whose PR moved on discards
  itself instead of presenting a dead draft, and live staged agents never exceed
  a fixed cap. Add a durable trigger-semantics declaration to the dispatch-state
  contract so a future edge-triggered source is not locked into PR coalescing.
upstream: docs/briefs/BRIEF-niwa-watch-pr-hardening.md
motivating_context: |
  ED1 shipped the wedge deliberately minimal and deferred durable dedup/cursor
  state, unblock-time freshness re-validation, and richer concurrency control as
  "later hardening". The wedge has run long enough to prove the containment
  model; this PRD specifies that deferred hardening as testable requirements.
---

# PRD: niwa watch PR-wedge hardening

## Status

Done

## Problem Statement

`niwa watch --once` stages one contained, pre-drafted review per PR the
developer is directly requested on. Three properties of the shipped verb make
repeated runs untrustworthy.

First, dedup is SHA-blind and permanent. The handled-set
(`internal/watch/state.go`) is keyed only by `owner/repo#number`, so once a PR
is handled it is suppressed forever. When the author pushes fixes and
re-requests review, the standing "awaiting my review" signal re-lights and the
watcher ignores it. Worse, the review session from the first pass has
accumulated real, human-supplied context (what the developer expects, which
docs and code matter), making it the best-informed reviewer of the next
iteration -- a dedup model that can only ignore-or-restart discards that
investment either way.

Second, staged reviews go stale invisibly. Hours or days can pass between
dispatch and the human unblocking a session, and in that window the PR can
merge, close, drop the developer from its requested reviewers, or be
force-pushed. Nothing re-checks any of that; the developer posts a review of a
diff that no longer exists.

Third, the only staging limit is per pass (`DefaultPerRunBound`). The handled-set
never forgets and staged sessions are healthy mapped instances, which niwa's
reaper deliberately leaves alone (it reclaims only unmapped orphans). Run after
run the staged population grows without a ceiling, so there is no bound on inbox
flood or cost.

Underneath these is a contract question the next feature author feels rather
than today's developer: PR review is a level-triggered signal (coalesce to the
current diff), but the dispatch-state contract this hardening introduces will
outlive the PR wedge, and a future edge-triggered source (distinct messages that
must not be dropped) has the opposite semantics. Baking PR coalescing in as a
universal rule would make the next wedge silently drop events.

## Goals

- A re-request after new commits does the right thing without developer
  bookkeeping: continue the existing review session when it survives and is
  free, stage a fresh one when it does not, and never run two review sessions
  for one PR at once.
- A staged review that has gone stale discards itself with a stated reason
  instead of presenting a dead draft as postable.
- The total number of live staged review agents is bounded by a fixed cap
  across runs, with pending PRs backfilled as capacity frees rather than
  dropped.
- The dispatch-state contract records each source's trigger semantics so PR
  coalescing is a per-source choice, not a universal rule.
- Every change is deterministic, fail-closed, and covered by tests; the shipped
  containment model and multi-repo scope are preserved, not reworked.

## User Stories

This is a developer-tooling feature; the "user" is the developer running
`niwa watch --once` over their workspace. Use-case descriptions stand in for
user stories.

- **Resume a briefed reviewer.** A developer reviewed a PR, gave the agent
  context, posted the review, and left the session idle. The author pushes a
  revision and re-requests review. On the next run the same session takes up the
  new diff with everything it learned, rather than a fresh agent starting cold.
- **Don't interrupt active work.** New commits arrive while the developer is
  attached to a staged review or it is mid-thought. The watcher leaves it
  untouched and defers the update until the session is idle or a later run.
- **Discard a stale review.** The developer unblocks a staged review whose PR
  merged, closed, dropped their request, or was force-pushed. The session
  re-validates first, states what changed, and discards itself.
- **Survive a burst.** A release week produces more review requests than the cap
  allows. Runs stage up to the ceiling and say so; remaining PRs stay pending
  and backfill oldest-first as staged sessions clear.

## Requirements

### Functional -- SHA-aware state and the re-dispatch decision

- **R1.** The dedup state SHALL record, per PR identity (`owner/repo#number`),
  the head SHA that was last dispatched for that PR, replacing the SHA-blind
  handled entry.
- **R2.** On each pass, for each PR the developer is directly requested on, the
  re-dispatch decision SHALL be keyed on whether the PR's current head SHA
  differs from the last-dispatched SHA recorded for it ("new activity").
- **R3.** When there is new activity and **no surviving review session** for the
  PR, the run SHALL stage exactly one fresh review session against the current
  head. A previously dismissed session and a crashed-then-reaped session both
  count as no surviving session.
- **R4.** When there is new activity and a surviving review session that is
  **detached and idle**, the run SHALL continue that session against the new
  activity (so its accumulated context is retained) rather than staging a fresh
  session. Continuing an existing session does not add a live staged agent, so
  it SHALL NOT consume additional capacity against the cap of R11 (the session
  was already counted).
- **R5.** When there is new activity and a surviving review session that is
  **live but not free to continue** -- it is busy (mid-turn) or a human is
  attached to it -- the run SHALL NOT interrupt it; the update is deferred until
  the session is free (detached and idle) or a later run stages it. Only a
  detached-and-idle session is continued under R4; an attached session defers
  even when it is otherwise idle, since the human is in it.
- **R6.** The system SHALL NOT run two live review sessions for the same PR at
  once, and SHALL coalesce multiple intervening pushes to the current head
  (no queue of superseded diffs).
- **R7.** When a PR's head SHA is unchanged since the last dispatch, the run
  SHALL take no action for that PR. Dismissing a session is the developer's
  signal that the reviewer is discarded; the next new activity then stages
  fresh.
- **R8.** The per-PR staged record SHALL carry a reference sufficient to
  (a) determine whether a review session for that PR is still live, (b)
  distinguish live-idle from live-busy/attached, and (c) target that session to
  continue it. (Today the record holds no session/instance reference and
  liveness is only inferable from naming.)

### Functional -- unblock-time freshness re-validation

- **R9.** Before a staged review is acted on (at unblock time), the system SHALL
  re-validate that the PR is still open, still requests the developer as a
  reviewer, and has not been rewritten away from the dispatched head SHA. "Rewritten
  away" means the dispatched SHA is no longer an ancestor of the PR's current
  head (a force-push or rebase). Ordinary advancement, where the dispatched SHA
  is still an ancestor of the current head, is not itself a freshness failure --
  it is new activity handled by the re-dispatch path (R2-R7), not a discard
  condition at unblock time.
- **R10.** A staged review that fails any freshness check SHALL discard itself
  and state which condition failed, and SHALL NOT present the stale draft as
  postable.

### Functional -- total-staged concurrency cap

- **R11.** The system SHALL enforce a maximum number of concurrently live staged
  review agents, counted across runs from the live staged records -- distinct
  from and additional to the existing per-run bound.
- **R12.** When the cap is reached, a run SHALL NOT stage further **fresh**
  reviews, SHALL report that the cap was hit, and SHALL leave the remaining
  eligible PRs pending (not recorded as handled). Continuing an already-live
  session (R4) is not blocked by the cap. Subsequent runs, once capacity frees,
  SHALL backfill from the still-pending PRs oldest-first by PR creation time
  (the same ordering the shipped per-run selection uses).

### Functional -- trigger-semantics contract

- **R13.** The dispatch-state contract SHALL let a source declare its trigger
  semantics as level-triggered or edge-triggered. Coalesce-to-current and the
  one-live-session rule SHALL apply to level-triggered sources (the PR wedge);
  the contract SHALL NOT hard-code coalescing in a way that would force it on a
  future edge-triggered source.

### Non-functional

- **R14.** The watcher SHALL remain deterministic: no model participates in the
  poll, the re-dispatch decision, freshness re-validation, or the cap.
- **R15.** State operations SHALL be fail-closed and fail-loud: a transient
  poll, dispatch, or state-write failure SHALL NOT permanently suppress a review
  nor silently look like "nothing to stage", and SHALL NOT record a PR as
  handled at a SHA it did not actually stage.
- **R16.** The change SHALL preserve the shipped multi-repo scope
  (`WorkspaceScope`) and the shipped containment model (sandbox, PreToolUse
  hooks, post-guard); it verifies these, it does not rebuild or weaken them.
- **R17.** The handled-state change SHALL migrate from the shipped flat
  handled-set without losing suppression of already-handled PRs, and SHALL treat
  malformed or legacy entries safely (never a fatal error, consistent with the
  shipped read behavior). A migrated legacy entry (a handled PR with no recorded
  SHA) SHALL NOT trigger a re-fire storm on the first post-upgrade run: on first
  observation after migration the system SHALL adopt the PR's current head as
  its last-dispatched SHA without re-staging, so the entry re-fires only on
  genuinely new activity after the upgrade.

## Acceptance Criteria

- [ ] Given a PR recorded as dispatched at SHA `A` and no surviving session,
      when the head advances to `B`, the selection stages exactly one fresh
      review; at unchanged `A` it stages nothing. (R1, R2, R3, R7)
- [ ] Given new activity and a live-idle surviving session for the PR, the run
      continues that session and stages no new session. (R4, R6)
- [ ] Given new activity and a live-busy/attached surviving session, the run
      defers: it neither interrupts the session nor stages a second one. (R5, R6)
- [ ] Given a session that was dismissed, or one that crashed and was reaped,
      new activity stages a fresh session. (R3, R7)
- [ ] Multiple pushes between two runs result in a single review against the
      latest head, never a queue of reviews of superseded diffs. (R6)
- [ ] The staged record persists a session/instance reference, and a test can
      resolve from it (a) liveness, (b) idle-vs-busy, (c) a continue target. (R8)
- [ ] A staged review whose PR is closed/merged, no longer requests the
      developer, or was rewritten (dispatched SHA no longer an ancestor of the
      current head) self-discards at unblock time with a message naming the
      failed condition, and posts nothing. (R9, R10)
- [ ] A staged review whose PR is still open, still requests the developer, and
      whose dispatched SHA is still an ancestor of the current head passes
      freshness re-validation and is NOT discarded (happy path). (R9, R10)
- [ ] With N live staged records at the cap, a run stages no further fresh
      reviews, reports the cap, and leaves remaining eligible PRs unrecorded;
      after a staged session clears, a later run stages the oldest pending PR by
      creation time. (R11, R12)
- [ ] Continuing a detached-and-idle session for a PR with new activity is not
      blocked when the cap is at its limit, and does not increase the live count.
      (R4, R11, R12)
- [ ] The per-run bound and the total-staged cap are both enforced in one pass:
      a run stages no more than the per-run bound and no more than remaining cap
      capacity, whichever is smaller. (R11)
- [ ] A source in the dispatch state declares level-vs-edge trigger semantics;
      coalescing and one-live-session apply for a level source, and a test
      demonstrates an edge-declared source is not subjected to coalescing. (R13)
- [ ] `go test ./...`, `go vet ./...`, and `gofmt -l` are clean; the re-dispatch
      decision, freshness re-validation, and cap are unit-tested as pure/table
      logic where the shipped code already is. (R14)
- [ ] A transient failure during a pass records no PR as handled at a SHA it did
      not stage, and a later run re-attempts it. (R15, fail-closed)
- [ ] A poll failure exits non-zero with an error and does not report "nothing
      to stage"; it is distinguishable from an empty result. (R15, fail-loud)
- [ ] The workspace scope matcher and the containment posture are unchanged by
      this pass: a directly-requested PR outside the workspace is still dropped,
      and a review is still staged under the shipped sandbox/hook/post-guard
      settings. (R16)
- [ ] Reading a handled-state file written by the shipped flat format continues
      to suppress those PRs after the migration, malformed lines are skipped
      rather than fatal, and a migrated SHA-less entry adopts the current head on
      first observation without re-staging (no upgrade storm). (R17)

## Out of Scope

- **Scheduling.** Driving `watch --once` from an OS timer or harness routine is
  the next feature; this pass keeps the verb run-by-hand.
- **Attention and cost controls beyond the cap.** Batching, priority ordering,
  heads-down suppression, bulk discard, and per-repo budgets are later work.
- **Any edge-triggered source.** Message streams and other
  manufactured-relevance sources stay out; only the trigger-semantics
  declaration is in scope, not a first edge consumer.
- **Containment changes.** The sandbox, hooks, and post-guard carry over
  unchanged; this PRD does not reopen the security design.
- **Posting, auto-posting, or auto-dismissal.** The agent drafts and halts; the
  human posts and dismisses. Freshness re-validation discards stale work; it
  never posts fresh work or dismisses on the developer's behalf.
- **Queueing re-requests.** Rejected, not deferred: queuing superseded pushes is
  incompatible with coalesce-to-current.
- **Rebuilding multi-repo scope.** Already shipped; this pass verifies it.

## Known Limitations

- **One-time migration boundary.** Adopting the current head for a migrated
  SHA-less entry without re-staging (R17) means any commits an already-handled PR
  accumulated before the upgrade are not re-reviewed; only activity after the
  upgrade re-fires. This is the deliberate cost of avoiding a re-fire storm on the
  first post-upgrade run, and it applies once, at the migration boundary.
- **Edge-source declaration is unexercised.** The trigger-semantics declaration
  (R13) reserves the seat for a future edge-triggered source but has no edge
  consumer yet, so its edge branch is asserted by contract (a level source
  coalesces, an edge-declared source is exempt) rather than exercised by a
  shipping edge behavior.

## Decisions and Trade-offs

These record requirements-level decisions and close the upstream BRIEF's Open
Questions. Mechanism choices are explicitly deferred to the DESIGN.

- **Resume a live-idle session vs always stage fresh (R4).** Chosen: resume, to
  retain the reviewer context the developer invested. Alternatives: the original
  suppress-until-dismissed model (discards the re-request) and auto-supersede
  (dismiss the old session and stage fresh). Resume was chosen because a posted
  review makes the session the best-informed reviewer of the next iteration;
  discarding it is the frustrating outcome. Settled with the dispatcher during
  scoping.
- **Coalesce-to-current vs queue (R6).** Chosen: coalesce. PR review is
  level-triggered, so a queue would stage redundant reviews of superseded diffs.
- **Cap counts live records across runs, not per pass (R11).** Chosen because the
  reaper leaves healthy staged instances alone, so only a cross-run count bounds
  the live population; the per-run bound is retained as a first-pass floor.
- **Trigger-semantics declaration in the state contract (R13).** Chosen to avoid
  locking a future edge-triggered source into PR coalescing; the cost is a small
  amount of contract surface added before its second consumer exists, accepted
  deliberately.
- **Resuming a live-idle session does not consume cap capacity (R4, R11).**
  Chosen because the session was already counted when it was first staged;
  continuing it adds no live agent, so blocking a resume at the cap would strand
  a re-request behind unrelated staged work. The cap gates only fresh stages.
- **Legacy migration adopts the current head without re-firing (R17).** Chosen
  over re-firing every already-handled open PR on upgrade (a burst) and over
  suppressing legacy entries forever (which would keep the SHA-blind bug). The
  trade-off -- pre-upgrade commits on a handled PR are not re-reviewed -- is
  recorded under Known Limitations.
- **Deferred to DESIGN.** The resume mechanism (e.g. stop/resume by session id
  with a fresh checkout, or continuing a kept-alive session), the exact
  handled-state file format and its migration shape, the cap's default value and
  whether it is configurable in this pass, the mechanism that distinguishes
  live-idle from live-busy and the behavior when a session never returns to idle,
  and the precise hook point at which freshness re-validation runs in the staged
  session's lifecycle -- all are architecture the DESIGN owns.
