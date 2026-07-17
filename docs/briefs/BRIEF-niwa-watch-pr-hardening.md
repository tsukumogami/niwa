---
schema: brief/v1
status: Draft
problem: |
  The shipped `niwa watch --once` wedge is not trustworthy across repeated
  runs: its SHA-blind handled-set never re-fires a genuine re-request,
  nothing re-validates a staged review at unblock time, and only a per-pass
  bound exists so live staged agents accumulate without limit.
outcome: |
  A developer runs watch repeatedly and trusts what it stages: a re-request
  after new commits continues their existing review session with everything
  it already learned (or starts fresh when none survives), a staged review
  whose PR moved on discards itself instead of presenting a dead draft, and
  live staged agents never exceed a fixed cap however bursty the stream.
motivating_context: |
  The first version of proactive PR-review dispatch shipped deliberately
  minimal: its own scope boundary deferred durable dedup/cursor state,
  re-request expiry, unblock-time freshness re-validation, and richer
  concurrency control as "later hardening". The wedge has now run long
  enough to prove the containment model, and those deferred reliability
  gaps are the next work in front of it -- this brief frames that
  hardening pass.
---

# BRIEF: niwa watch PR-wedge hardening

## Status

Draft

Framing for the hardening pass over the shipped `niwa watch --once`
PR-review wedge. The downstream PRD owns the requirements (the exact
freshness-check set, the handled-state format and migration, the cap's
default and configuration surface); the downstream DESIGN owns the
architecture (where dispatch state lives, how liveness is derived, how a
source declares its trigger semantics). This BRIEF frames the problem,
the outcome, the journeys, and the boundary.

## Problem Statement

`niwa watch --once` stages a contained, pre-drafted review for every PR
the developer is directly requested on -- once. The dedup state that
makes a second run safe is a flat, permanent, SHA-blind handled-set
keyed only by `owner/repo#number`. That was the right minimal shape for
the first version, and it is wrong the first time a PR evolves: the
author pushes fixes and re-requests the developer's review, the standing
"awaiting my review" signal lights up again, and the watcher -- the one
tool whose job is to turn that signal into a staged review -- silently
ignores it forever. The developer is back to manual triage for exactly
the PRs that are most actively in motion.

And the review session from the first pass is not disposable. Over that
review the developer often hands the agent real context -- what they
expect from the change, the docs and code that matter, what to weigh --
so by the time a review is posted the session is the best-informed
reviewer of the next iteration that exists. A dedup model that can only
choose between "ignore the re-request" and "start a naive session from
scratch" throws that investment away either way.

The staged reviews themselves go stale invisibly. Proactive staging
means hours or days can pass between the dispatch and the human
unblocking the session, and in that window the PR can merge, close, drop
the developer from its requested reviewers, or be force-pushed out from
under the drafted review. Nothing re-checks any of that at unblock time.
The developer reads, edits, and posts a review of a diff that no longer
exists -- the staleness is discovered by the PR author, downstream,
instead of by the tool that knew how to check.

And the only staging limit is per pass. Each run stages at most a
handful of new reviews, but the handled-set never forgets and the
staged sessions are healthy, mapped instances -- niwa's reaper
deliberately reclaims only unmapped orphans, so it never trims them.
Run after run, the staged population only grows: there is no ceiling on
the total number of live review agents a workspace accumulates, which
means no ceiling on inbox flood or on the cost a bursty week of review
requests can incur.

Underneath all three gaps is a contract question the first version never
had to answer -- one the next feature author feels rather than today's
developer. A PR review request is a level-triggered signal -- "this PR is
now at state S and wants your review" -- so the right dedup state
coalesces to current state and only ever cares about the latest diff.
But the dispatch-state contract this hardening introduces will outlive
the PR wedge, and a future source that is a genuine event stream (
distinct messages that must not be coalesced or dropped) has the
opposite semantics. If the state contract bakes PR-style coalescing in
as a universal rule, the next wedge inherits a dedup model that silently
drops events.

## User Outcome

A developer who runs `niwa watch --once` more than once -- by hand
today, on a schedule later -- stops second-guessing it. When a PR comes
back with new commits and a fresh review request, what happens depends on
the review session they already have. If that session is still alive and
idle in their agent view, it picks the change back up: the reviewer they
briefed looks at the new diff, carrying everything it already learned,
rather than a stranger starting over. If they threw that session away, or
it crashed, the next run stages a clean one against the new head. Either
way exactly one review agent ends up on the PR -- never two -- and the
developer does no bookkeeping about which PRs are "done".

An active session is never yanked out from under them. If new commits
land while the developer is attached to that review or it is mid-thought,
the watcher does not interrupt it; the update waits until the session is
idle (or the next run catches it). Dismissing a session stays the
developer's call and means "I'm finished with this reviewer" -- the next
change then starts fresh, because they chose to discard the context.

When the developer unblocks a staged review, it is either still real or
it says so: a session whose PR has merged, closed, stopped requesting
them, or been force-pushed discards itself instead of walking the
developer into posting a review of a vanished diff. And the workspace
has a hard ceiling on how many live staged reviews can exist at once,
so a burst of review requests -- or simply a busy week -- costs a
bounded amount of compute and attention, with capacity freed the moment
staged sessions are dismissed or completed.

## User Journeys

### The reviewer the developer briefed picks up the revision

A developer reviewed a PR staged by an earlier watch run, gave the agent
real context along the way, posted the review, and left the session
sitting idle in their agent view rather than dismissing it. The author
pushes commits addressing the feedback and re-requests review. Trigger:
the next `niwa watch --once` run, with the head SHA changed and the
review session still alive and idle. Outcome: that same session takes up
the new diff -- the reviewer they already briefed, re-evaluating the
revision with everything it learned the first time -- instead of a fresh
agent starting from nothing. Had the developer dismissed the session, the
same run would instead stage one clean review against the new head; a
crashed-and-reaped session lands there too. Either path stages exactly
one agent, never a second alongside the first.

### New commits arrive while the developer is still in the review

The developer is attached to a staged review, or the session is
mid-thought, when the author force-pushes twice more and re-requests each
time. Trigger: watch runs while that session is live but busy. Outcome:
the watcher does not interrupt an active session -- it leaves the work
untouched and lets the update wait until the session goes idle (or a
later run catches it). When the session is next free it takes up the
latest head, the intermediate pushes coalesced into one current diff
rather than a queue of three superseded ones. Throughout, there is never
a second live session racing the first.

### Unblocking a review whose PR moved on

Days after a busy watch run, the developer opens a staged review from
their agent view. In the meantime the PR was merged by another reviewer
(or closed, or the author withdrew the request, or force-pushed the
branch). Trigger: the developer unblocks the staged session. Outcome:
the session re-validates its PR first, announces what changed, and
discards itself instead of presenting the stale draft as postable work.

### A bursty week hits the staging ceiling

A release week produces fourteen review requests across the workspace's
repos while the developer is heads-down. Trigger: repeated watch runs
(manual or, later, scheduled) over a result set much larger than the
cap. Outcome: runs stage new reviews only up to the total-staged
ceiling and say so plainly; the remaining PRs stay unhandled, not
dropped -- as the developer dismisses or completes staged sessions,
subsequent runs backfill from the still-pending requests, oldest first.

## Scope Boundary

### IN

- **SHA-aware handled state.** The handled-set records the last-
  dispatched head SHA alongside the PR identity, replacing the
  permanent SHA-blind entry as the dedup contract.
- **The decided re-dispatch behavior.** Per requested PR, per pass, keyed
  on new activity (the head SHA moved since the last dispatch):
  - no surviving review session -> stage one fresh session against the
    current head (a dismissed session, or a crashed-and-reaped one, both
    count as none surviving);
  - a live *and idle* review session -> that session continues against
    the new activity, its accumulated reviewer context retained rather
    than discarded;
  - a live *but busy or attached* session -> not interrupted; the update
    waits until it is idle, or the next run picks it up.

  Across all cases: coalesce to the current head (no queue of superseded
  diffs), and never two live sessions for one PR. An unchanged head does
  nothing; dismissal is the developer's signal to discard the reviewer.
- **A niwa capability to target and update a live-idle session.** The
  watcher must be able to tell a live-idle review session from a
  live-busy one, and to hand a live-idle session the updated PR state
  without discarding its context. The per-PR staged record carries
  whatever reference makes both a clean lookup (today it holds no
  session/instance reference and liveness is only inferable from naming).
  *How* niwa continues a session -- stop/resume by id with a new prompt
  and a fresh checkout, or another mechanism -- is a DESIGN decision, not
  fixed here.
- **Unblock-time freshness re-validation.** Before a staged review is
  acted on: is the PR still open, still requesting this developer, and
  not force-pushed since the dispatch? A staged review that fails the
  check self-discards with a plain statement of why.
- **A true total-staged cap.** A ceiling on live staged review agents
  counted across runs from the live staged records -- not the existing
  per-pass bound -- refusing to stage beyond it and saying so.
- **A trigger-semantics declaration in the state contract.** The
  dispatch state a source writes declares whether the source is
  level-triggered (coalesce to current, the PR wedge) or edge-triggered
  (distinct events, a future message-stream wedge), so PR coalescing is
  a per-source choice, not a baked-in universal.
- **Tests for the re-dispatch matrix:** fresh stage when no session
  survives, resume the live-idle session on a new head, do-not-interrupt
  a live-busy session, coalesce multiple pushes to the current head,
  fresh stage after dismissal or reap, no-op on an unchanged head --
  plus freshness self-discard and cap enforcement.
- **Verification (not reconstruction) of multi-repo scope.** The
  shipped scope matching already spans all workspace repos; this pass
  exercises it as part of validating the hardening, without rebuilding
  it.

### OUT

- **Scheduling.** Driving `watch --once` from an OS timer or harness
  routine is the next feature, not this one; this pass keeps the verb
  run-by-hand (while making repeated runs trustworthy enough to
  schedule).
- **Attention and cost controls beyond the cap.** Batching, priority
  ordering, heads-down suppression, bulk discard, and per-repo budgets
  remain later work; the total-staged ceiling is the only concurrency
  control this pass adds.
- **Any edge-triggered source.** Message streams and other
  manufactured-relevance sources stay out. This pass only reserves
  their seat: the trigger-semantics declaration is in scope, the first
  edge-triggered consumer of it is not.
- **Changes to the containment model.** The sandbox, hook, and
  post-guard enforcement shipped with the first version carries over
  unchanged; hardening the dispatch state does not reopen the security
  design.
- **Posting, auto-dismissal, or acting for the developer.** The agent
  still drafts and halts; the human still posts. Freshness re-validation
  discards stale work -- it never upgrades to posting fresh work.
- **Queueing re-requests.** Explicitly rejected, not deferred: queuing
  intermediate pushes would stage redundant reviews of superseded
  diffs. The dedup state coalesces to the current head, always.

## Open Questions

- The exact freshness-check set and its mechanical trigger point (what
  runs at "unblock time", and where it hooks into the staged session's
  lifecycle) -- the PRD owns the check set, the DESIGN owns the hook.
- The handled-state file format and its migration from the shipped flat
  set (upgrade in place vs sidecar), including how malformed or legacy
  entries are treated.
- The cap's default value and whether it is configurable in the first
  pass or fixed like the per-run bound.
- How the staged record references its session/instance for the
  liveness lookup, and what "dismissed" looks like from the watcher's
  side of that reference.
- The mechanism by which niwa continues a live-idle session against new
  activity -- stop/resume by id with a new prompt and a fresh checkout is
  one candidate; the DESIGN weighs it against alternatives. Includes how
  the updated PR head reaches the session's contained clone as inert data
  without reopening the containment model.
- How the watcher distinguishes live-idle from live-busy/attached, and
  what it does when a session never returns to idle (defer indefinitely
  vs a bound after which it stages fresh).

## References

- `docs/briefs/BRIEF-niwa-watch-once-pr-review.md` -- the first
  version's framing; its scope boundary explicitly deferred the state,
  freshness, and concurrency hardening this brief picks up.
- `docs/prds/PRD-niwa-watch-once-pr-review.md` -- the shipped wedge's
  requirements, including the handled-set and per-run bound this pass
  supersedes.
- `internal/watch/state.go`, `internal/watch/select.go`,
  `internal/cli/watch.go` -- the shipped handled-set, per-run bound,
  staged records, and dispatch pass this hardening extends.
