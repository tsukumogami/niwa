---
upstream: docs/prds/PRD-niwa-watch-pr-hardening.md
status: Planned
problem: |
  The shipped `niwa watch --once` verb cannot support re-request re-fire,
  unblock-time freshness, or a cross-run staged cap on its current state: the
  handled-set is SHA-blind and permanent, the staged record carries no reference
  that would let the watcher find or continue a dispatched session, nothing runs
  at unblock time, and both stores grow monotonically while only the instance
  layer is reaped. niwa also has no primitive to continue a dispatched review
  session and no idle/attached detection.
decision: |
  Split the dispatch state by lifetime: the permanent handled-set gains the
  last-dispatched head SHA (dual-format, legacy lines = unknown-SHA, migrated
  without a re-fire storm) plus a level|edge trigger-semantics declaration, and
  the per-dispatch StagedRecord gains InstancePath and captured session ids. A new
  staged-record GC prunes dead or stale records each pass, giving the record layer
  the lifecycle the instance reaper already has and making a cross-run cap
  accurate. The re-dispatch decision becomes a pure function producing
  Fresh/Continue/Defer/Noop; freshness is a deterministic predicate run at the
  watcher pass and a session pre-flight; the cap counts live records and composes
  with the per-run bound via config following the watch_sandbox pattern. Live-idle
  continuation uses stop-and-resume by conversation id in a fresh checkout — the
  one primitive niwa has — sequenced last and independently deferrable.
rationale: |
  Every piece but continuation extends an existing seam (the handled-set parser's
  skip-malformed tolerance, the staged-record store, the global-config resolver
  pattern, the reaper's liveness probe), keeping the change deterministic and
  contained. The SHA lives in the permanent store because a dismissed PR must
  still suppress on an unchanged head; the record carries continuation refs because
  they die with the session. Stop-and-resume is chosen because niwa has no in-place
  prompt-delivery primitive and no idle detection — the alternatives are an
  external remote-control surface niwa does not drive, or discarding the reviewer's
  context, which the settled decision rejects. Continuation is sequenced last so
  the three contained gaps ship as real hardening even if it needs a follow-up PR,
  and the Defer fallback never regresses the two-live-sessions invariant. The
  boundary stays egress denial: continuation re-asserts the shipped containment and
  fails closed, adding no new channel and no new trust in the model.
---

# DESIGN: niwa watch PR-wedge hardening

## Status

Planned

## Context and Problem Statement

The shipped `niwa watch --once` verb (ED1) is a stateless, deterministic
poll-and-dispatch pass. Its durable state is two flat structures under `.niwa/`:
a SHA-blind handled-set (`internal/watch/state.go`, `.niwa/watch-handled`,
lines keyed `owner/repo#number`) and one staged-review record per dispatched PR
(`.niwa/watch/<handle>.json`, `StagedRecord{Handle,Owner,Repo,Number,URL,
DraftPath}`). Selection is a pure function bounded per pass by
`DefaultPerRunBound` (`internal/watch/select.go`). The accepted PRD
(`PRD-niwa-watch-pr-hardening.md`) requires four behavioral changes on top of
this: SHA-aware re-dispatch that continues a live-idle review session or stages
fresh, unblock-time freshness re-validation, a total-staged concurrency cap
counted across runs, and a per-source trigger-semantics declaration on the
dispatch state.

The technical problem is that none of these can be built on the current state
model as-is. The handled-set has no room for a head SHA; the staged record has
no reference that would let the watcher find, probe the liveness of, judge the
idle/attached status of, or continue an already-dispatched session; nothing runs
at the moment a staged review is unblocked; and the only concurrency bound is a
per-pass constant, not a count of live staged agents. The design must decide how
the dispatch state is represented and migrated, what session-continuation and
liveness primitives niwa can offer (and which must be built), where freshness
re-validation hooks into the staged session's lifecycle, and how the cap is
counted and configured — all without weakening the shipped containment model or
the multi-repo scope, and keeping the watcher deterministic (no model in the
poll, decision, freshness check, or cap).

## Decision Drivers

- **Level-triggered coalescing is the PR wedge's semantics, but not universal.**
  The state contract must let a source declare level vs edge so a future
  edge-triggered source is not forced into PR coalescing (PRD R13).
- **Never two live sessions per PR; retain reviewer context when it survives.**
  The re-dispatch decision must continue a detached-and-idle session rather than
  discard its accumulated context, and must never interrupt a busy/attached one
  (PRD R4-R6).
- **Determinism.** No model participates in the poll, the re-dispatch decision,
  freshness re-validation, or the cap (PRD R14).
- **Fail-closed and fail-loud.** A transient failure must not suppress a review,
  must not look like "nothing to stage", and must not record a PR handled at a
  SHA it did not stage (PRD R15).
- **Preserve, don't rebuild.** The shipped containment (sandbox/hooks/post-guard)
  and multi-repo scope carry over unchanged (PRD R16).
- **Migrate the shipped flat state without a re-fire storm** and without losing
  suppression of already-handled PRs (PRD R17).
- **Follow existing niwa patterns.** Reuse the settings-merge seam, the global
  config pattern (`watch_sandbox`), the staged-record store, and the reaper
  rather than introducing parallel machinery.

## Considered Options

Four independent decisions were investigated against the shipped code. Each names
the alternatives so the choice is not read as automatic.

### Decision 1 — Where the dispatch state lives and how it migrates

The state splits across the two shipped stores **by lifetime**:

- **Last-dispatched head SHA -> the handled-set (permanent, per PR).** The flat
  line format grows a SHA (`owner/repo#number@<sha>`); `LoadHandledSet` becomes
  dual-format (a legacy SHA-less line parses as "handled at unknown SHA"),
  reusing the existing skip-malformed tolerance (`state.go:47-49`) as the
  migration seam. The SHA must live in the permanent store, not the record,
  because a dismissed PR still has to suppress on an unchanged head (R7) and
  re-qualify on a new head (R2) after its record is gone.
- **Session-continuation reference -> the StagedRecord (per live dispatch).** The
  record grows `InstancePath` (the liveness anchor, already in scope at save
  time) plus the captured `SessionID` / `ConversationID` / `ShortID` needed to
  continue a session (Decision 2).
- **Trigger semantics -> a typed `level | edge` declaration** the source writes,
  with the PR source declaring `level`; the coalesce / one-live-session logic
  branches on it instead of hard-coding.

*Alternatives rejected:* a single new JSON state file replacing the flat
handled-set (larger blast radius, discards the skip-malformed migration seam); the
SHA in the StagedRecord only (records are GC'd, so a dismissed PR would lose its
last-dispatched SHA and re-fire on an unchanged head).

### Decision 2 — How a live-idle session is continued (critical)

The investigation established the hard constraint: **watch sessions capture no id,
niwa has no idle/attached detection, and there is no in-place prompt-delivery
primitive.** The one continuation primitive that exists is `claude --resume
<conv_id>` (wired today only to worktree lifecycle sessions).

*Chosen: stop-and-resume by conversation id with a fresh checkout.* At stage time
capture and persist the session's conversation id and short id (the
`dispatchCapture` cwd-correlation already exists — `dispatch_capture.go:41`);
determine liveness from the persisted id via `sessionLive`; classify
idle/busy/attached by reading the job `state.json` fields niwa deliberately
ignores today (`job_state.go:53-59`); and continue a detached-idle session by
stopping it and `claude --resume`-ing it in a freshly checked-out clone at the new
head with a re-review prompt. This is the dispatcher's named sketch and the only
path consistent with both the settled resume behavior and the primitives that
exist.

*Alternatives rejected:* in-place injection via the remote-control bridge (niwa
does not drive RC; it is an external steering surface with no payload API, and
injecting mid-turn is the interruption R5 forbids); always-stage-fresh, which is a
real, cheaper alternative — it needs no id capture, no idle detection, and no
resume wiring, and would ship Phases A-C alone — but it structurally cannot retain
the reviewer's accumulated context, which is the specific outcome the settled
resume decision exists to secure, so it fails the goal rather than the budget.
Notably, always-stage-fresh is exactly the `Defer`/`Fresh` fallback this design
runs until Phase D lands, so the choice is not fresh-vs-resume but whether resume
is built on top of that fallback.

### Decision 3 — Where freshness re-validation runs

*Chosen: one deterministic predicate at two hook points.* The predicate — PR still
open, developer still requested, dispatched SHA still an ancestor of the current
head (distinguishing force-push/rebase from ordinary advancement) — reuses data
the poll already fetches plus one ancestry check. It runs (A) in the watcher pass
over live staged records, pruning stale ones, and (B) as a deterministic
pre-flight the review session invokes before presenting its draft, covering an
unblock that happens between passes. The agent invokes the check and honors its
exit; the discard is code-driven, not a model judgment (R14).

*Alternatives rejected:* session-side only (a session never re-engaged never
re-checks); watcher-pass only (misses the true unblock moment); letting the agent
judge staleness from the clone (violates R14).

### Decision 4 — How the cap is counted and configured

*Chosen: count live agents, not records, with a staged-record GC.* The cap counts
records whose instance still has a live job (`instanceHasLiveJob`); dead records
are pruned each pass, so the count reflects live agents and the record store stops
growing unbounded. Continuing a live-idle session reuses an already-counted agent,
so it is cap-neutral. Config follows the `watch_sandbox` pattern: a
`WatchMaxStaged int` field on `GlobalSettings`, resolved at the watch use site,
default const `DefaultMaxStaged = 5` in `internal/watch`. In `runWatchOnce` the
cap composes with the per-run bound by passing `min(DefaultPerRunBound, maxStaged
- liveCount)` as `Select`'s existing bound.

*Alternatives rejected:* counting records without GC (overcounts dead sessions);
`*int` tri-state config (Select's `<=0`-means-default convention makes a plain
`int` the closer fit).

## Decision Outcome

The four decisions share one spine: **split the dispatch state by lifetime and add
the record-lifecycle the shipped design lacks.** The permanent handled-set gains
the last-dispatched SHA (driving the re-dispatch decision); the per-dispatch
StagedRecord gains a session-continuation reference and is garbage-collected when
its instance dies. That GC — fed by both the freshness prune (Decision 3) and the
dead-record prune (Decision 4) — is the missing counterpart to the instance
reaper, and it is what makes the cross-run cap accurate, frees capacity on
dismissal, and stops both stores from growing unbounded.

On top of that state spine, the re-dispatch decision is a pure function of
(last-dispatched SHA, current head, session liveness, session idle/attached
status). The single genuinely new capability is Decision 2's stop-and-resume
continuation; every other piece extends existing niwa seams (the handled-set
parser, the staged-record store, the global-config pattern, the reaper's liveness
probe).

## Solution Architecture

### Components

1. **SHA-aware handled-set** (`internal/watch/state.go`). `HandledKey` /
   `isHandledKey` / `LoadHandledSet` / `AppendHandled` learn the
   `owner/repo#number@<sha>` shape; `LoadHandledSet` returns a
   `map[key]lastSHA` (dual-format: legacy lines -> `""` meaning unknown). A
   header/version line carries the source's trigger-semantics declaration; the
   dual-format parser tolerates it.

2. **Extended StagedRecord** (`internal/watch/state.go`). New fields:
   `InstancePath`, `SessionID`, `ConversationID`, `ShortID`. Written at stage time
   after a capture step; read for liveness, resume, freshness, and cap.

3. **Session capture in the watch path** (`internal/cli/watch.go`). `stageReview`
   adopts the existing `captureSessionID` cwd-correlation
   (`dispatch_capture.go`) after `dispatchLaunch`, persisting the ids into the
   StagedRecord. One grounding caveat Phase D must resolve: the shipped capture
   yields the bg session UUID, whereas `claude --resume` keys on a *conversation
   id*. Phase D must establish that a resume-usable id can be captured for a watch
   session (from the session UUID, the transcript path, or Claude's
   session-to-conversation mapping). If a resume-usable id cannot be captured
   reliably, the live-idle branch stays `Defer` — the design does not over-promise
   continuation on an id it cannot resolve. Capture is best-effort and fail-loud: a
   miss degrades to the instance-path liveness anchor (the shipped behavior) and a
   `Defer`/`Fresh` plan, never a crash.

4. **Liveness + idle/attached classifier** (`internal/watch` + `internal/cli`).
   A small reader over `~/.claude/jobs/<id>/state.json` that returns
   `dead | idle | busy | attached` for a staged record, built on the fields
   `job_state.go` currently ignores. `dead` -> prune; `idle` (and detached) ->
   eligible to continue; `busy`/`attached` -> defer.

5. **Re-dispatch decision** (`internal/watch/select.go` or a new pure function).
   Input: requested PRs, handled-set (with SHAs), live staged records with their
   idle/attached status, scope, per-run bound, remaining cap. Output: a typed plan
   per PR — `Fresh`, `Continue(record)`, `Defer`, or `Noop` — table-testable the
   way `Select` is today.

6. **Continuation mechanism** (`internal/watch` + a resume path generalized from
   `internal/cli/sessionattach/supervise.go`). Given a `Continue(record)` plan:
   stop the session (`ShortID`), fresh-checkout the new head into the instance's
   clone as inert data (reusing `FetchPRHead`), and `claude --resume
   <ConversationID>` with a re-review prompt. Sequenced last and independently
   deferrable (see Implementation Approach).

7. **Freshness predicate** (`internal/watch`). Pure `freshness(record, pollState)
   -> ok | reason`, consumed by two hooks. The **watcher-pass prune is the
   deterministic backstop** — it runs unconditionally every pass and does not
   depend on the agent. The **session pre-flight** is the unblock-time complement
   that relies on the agent invoking the check (a niwa subcommand) as its first
   step; it narrows the between-passes window but is not load-bearing on its own,
   because the next watcher pass would prune the same stale record regardless.

8. **Staged-record GC** (`internal/cli/watch.go`, each pass). Prune records that
   are `dead` or fail freshness; stop/reap their instances. The cap *count* is made
   accurate by the live-probe (`instanceHasLiveJob`) evaluating each record's
   current liveness; the GC's own job is to bound record-store growth and free
   capacity on dismissal — the record-layer counterpart to `reapOpportunistically`.

9. **Cap + config** (`internal/config/registry.go`, `internal/watch/select.go`,
   `internal/cli/watch.go`). `WatchMaxStaged` global setting, `DefaultMaxStaged`
   const, `resolveMaxStaged` resolver, and the `min(perRunBound, maxStaged -
   liveCount)` composition in `runWatchOnce`.

### Data flow (one `watch --once` pass)

```
reapOpportunistically(root)                       # instance layer (shipped)
prune/GC staged records: dead OR !freshness       # NEW record layer (D3,D4)
poll GitHub: open + still-requesting PRs, heads    # shipped
load handled-set (key -> lastSHA)                  # SHA-aware (D1)
liveCount = live staged records                    # (D4)
for each requested PR (scope-filtered):
  plan = decide(lastSHA, head, liveRecord?, idle/attached)   # pure (D2,D1)
    head == lastSHA            -> Noop
    new head, no live record   -> Fresh
    new head, live+detached-idle -> Continue(record)
    new head, live+busy/attached -> Defer
apply, bounded by min(perRunBound, maxStaged-liveCount) for Fresh only:
  Fresh    -> stageReview (capture ids), handled@head, save record
  Continue -> stop+resume in fresh checkout (cap-neutral), handled@head
  Defer/Noop -> nothing
```

Determinism holds: every branch is decided by code over polled facts and on-disk
state; no model participates.

## Implementation Approach

Sequenced so each slice is usable and the novel piece is last and independently
deferrable — matching the walking-skeleton philosophy of the parent roadmap.

- **Phase A — SHA-aware state + trigger semantics + migration.** Evolve the
  handled-set format and `LoadHandledSet`/`AppendHandled`/`HandledKey`; legacy
  entries adopt the current head on first observation (no storm). Add the
  trigger-semantics declaration. Extend `Select`/the decision function to key on
  SHA for the `Fresh` vs `Noop` cases. Ships re-fire-on-new-head with a
  suppress-while-live fallback (no continuation yet). Fully unit-testable.
- **Phase B — freshness predicate + staged-record GC.** Add `InstancePath` to the
  record, the freshness predicate, the watcher-pass prune, and the session
  pre-flight subcommand. Ships self-discarding stale reviews and the record
  lifecycle that stops unbounded growth.
- **Phase C — total-staged cap.** Add the config field/default/resolver and the
  `min(perRunBound, maxStaged-liveCount)` composition. Depends on B's live-count
  accuracy.
- **Phase D — session continuation (the novel capability).** First establish that
  a resume-usable conversation id can be captured for a watch session (the gating
  unknown — the shipped capture yields a session UUID, not necessarily the id
  `claude --resume` wants); if it cannot, the live-idle branch stays `Defer` and
  Phase D reduces to a documented non-goal. Then persist the ids, build the
  idle/attached classifier, and wire stop-and-resume through the fresh-dispatch
  launch wrapper (so the OS sandbox re-enters — see Security). Flips the
  live-and-idle branch from `Defer` to `Continue`. If D proves larger than one PR's
  worth, A-C still ship as real hardening and D becomes a bounded follow-up; the
  `Defer` fallback is safe and never regresses the two-live-sessions invariant.

## Security Considerations

Event-driven PR-review dispatch is a remote-execution vector: the review session
ingests attacker-authorable PR content under the developer's authority. ED1's
boundary is deterministic egress denial (OS sandbox over Bash + PreToolUse hooks
over WebFetch/WebSearch/MCP + `--strict-mcp-config`, with the agent drafting and
never posting). This design must not weaken that boundary; several new surfaces
are reviewed against it.

- **Continuation must re-apply, never relax, containment — via the same launch
  wrapper as fresh dispatch.** A continued session runs on a *new* untrusted diff.
  The fresh checkout at the new head is fetched with the shipped inert-data path
  (`FetchPRHead`, outside the sandbox), and the instance's review settings
  (`ApplyReviewSettings` — sandbox stanza, hooks, post-guard) are re-asserted
  before resume. Critically, "re-assert containment" is not just re-writing
  settings on disk: the resume MUST launch through the same sandbox-applying path a
  fresh `dispatchLaunch` uses, so the OS no-egress sandbox re-enters over the
  session's Bash — the strongest ED1 control — and does not silently degrade to the
  hook layer only. The continuation path therefore reuses the fresh-dispatch launch
  wrapper, not the lighter `sessionattach` attach path (which was built for trusted
  worktree sessions and applies no sandbox). If containment cannot be enforced on
  resume, the pass fails closed exactly as `stageReview` does today
  (`resolveReviewPlan` refusal), never resuming uncontained. This is a testable
  sub-claim, not an atomic assertion: the plan carries an integration test that a
  *resumed* watch session's Bash egress is denied at the OS layer, not merely that
  the hooks are present.
- **Compounded untrusted context is bounded by egress denial, not context
  hygiene.** `claude --resume` carries the prior transcript (earlier untrusted PR
  content) into the new turn, and the instance lives across iterations. Because
  the boundary is egress denial rather than credential hiding, a longer-lived or
  multi-diff-poisoned context still has no outbound channel — it cannot
  exfiltrate, push, or post. The larger residency window does not open a new
  channel; it is the same sealed box, longer.
- **New persisted fields become process-control arguments and path components —
  validate before use.** `ShortID` and `ConversationID` flow into `claude stop
  <short-id>` and `claude --resume <conv_id>`; `InstancePath` is a checkout target
  and a path component for `instanceHasLiveJob`; `SessionID` feeds the `sessionLive`
  lookup. All are niwa-generated and captured from `~/.claude/jobs/*/state.json` or
  the provisioner, not attacker-controlled through PR content — but each must be
  confined before use: the id fields pass a conservative charset validation (the
  `isSafeHandle` precedent) before becoming CLI arguments, and `InstancePath` must
  resolve under the managed instances root before it is used as a checkout or path
  target. This closes any argument-injection or traversal surface introduced by
  persisting these fields in the StagedRecord.
- **Continue targets the right session, fail-closed on doubt.** Before
  stop-and-resume, liveness is cross-checked two ways — `sessionLive` on the
  persisted id AND `instanceHasLiveJob` on the record's `InstancePath` — and the
  ids must resolve to that instance. On any mismatch the plan degrades to `Fresh`
  or `Defer` rather than acting on an ambiguous handle, so a stale or mis-captured
  id never stops/resumes an unrelated session.
- **Freshness ancestry check runs on trusted data only.** The "dispatched SHA is
  an ancestor of the current head" test uses the trusted GitHub API / trusted-CLI
  path (the same trust boundary the poll and `FetchPRHead` already use), not a git
  invocation that could execute attacker-supplied hooks inside the clone. The
  freshness predicate consumes inert, platform-vouched data.
- **The re-review prompt is a fixed template.** The prompt delivered on `Continue`
  carries no PR-derived free text (no title, branch name, or author) built into a
  shell command or CLI argument. Like the ED1 dispatch prompt, PR content reaches
  the resumed model only as inert checkout data in the clone, never as command or
  argument text — so a crafted PR cannot influence what is executed at resume.
- **State parsing stays tolerant and non-executable.** The SHA added to the
  handled-set is validated to the hex-SHA charset by the extended `isHandledKey`;
  a malformed or hostile line is skipped (the shipped skip-not-fatal contract),
  never fatal and never interpolated anywhere executable.
- **The cap is a positive security control.** The cross-run total-staged cap
  bounds how many contained instances a burst — including a malicious flood of
  review requests — can spin up, adding a DoS/cost ceiling the shipped per-run
  bound did not provide.
- **Determinism is a security property, preserved.** The re-dispatch decision,
  freshness re-validation, and cap are all code over polled facts and on-disk
  state. No model participates, so none of them is an injectable judge.

Net: the design adds no new egress channel and no new trust in the model; it
extends state and adds a continuation mechanism that inherits the shipped
containment unchanged, with input validation on the new persisted fields.

## Consequences

### Positive

- Repeated `watch --once` runs become trustworthy: genuine re-requests re-fire,
  stale reviews self-discard, and the live staged population is bounded.
- The reviewer's accumulated context is retained across PR iterations (the
  feature's whole point) via the one continuation primitive niwa already has.
- The staged-record GC closes the monotonic-growth gap the shipped design left —
  both the handled and record stores now have a lifecycle, and dismissal frees
  cap capacity.
- The cap adds a DoS/cost ceiling; the trigger-semantics declaration keeps a
  future edge-triggered source from inheriting PR coalescing.
- Every change extends an existing seam (handled-set parser, record store,
  global-config pattern, reaper liveness probe); only continuation is net-new.

### Negative / risks

- **Continuation is the heavy, novel piece.** Capturing a retrievable session id
  for watch, building idle/busy/attached detection, and wiring stop-and-resume are
  net-new and touch session-lifecycle plumbing. Mitigation: Phase D is sequenced
  last and is independently deferrable; A-C ship real hardening without it, and
  the `Defer` fallback is safe.
- **Idle/attached detection reads job-state fields niwa deliberately ignored.**
  Depending on those fields couples watch to the Claude Code job-state shape.
  Mitigation: isolate the read behind the small classifier so a shape change has
  one call site; fall back to `Defer` (never a wrong `Continue`) when the fields
  are unreadable.
- **One-time migration boundary.** Legacy handled entries adopt the current head
  without re-firing, so pre-upgrade commits on an already-handled PR are not
  re-reviewed once. Deliberate, to avoid an upgrade storm (PRD Known Limitation).
- **Longer-lived instances.** A continued session's instance persists across
  iterations, a larger residency window than one-shot dispatch. Contained by
  egress denial (see Security); bounded by the cap and the freshness/GC prune.

### Implementation-surfaced residuals (recorded during build)

- **Idle is detectable; attached is not.** The job state exposes a positive
  detached-idle signal (`state==done && tempo==idle && inFlight.tasks==0` and no
  pending human question) that the classifier keys on, AND-ing the fields so a stale
  `inFlight.tasks` count cannot alone read as busy. But no field marks a human
  terminal-attach to a *watch* instance (niwa's attach sentinel is worktree-only).
  The detectable human-in-the-loop proxy — an awaiting-answer (`block`/`needs`)
  session — maps to Defer. The residual: a *silent idle terminal-attach* (a human
  ran `claude --resume` to read the draft and is idle with no pending question) is
  invisible, so a continuation could stop-and-resume it. Bounded by: watch sessions
  are dispatched detached; the normal flow is read-draft-then-dismiss (dismissal
  removes the job entry → not live → not continued); the two-way liveness
  cross-check; and stop-before-resume. Accepted as a narrow residual, not closed.
- **`--resume` + `--name` works, and `--bg` mints a new session id (verified by a
  live smoke).** `claude --bg --resume <uuid> --name <slug>` launches cleanly and
  carries the prior conversation forward (context is preserved). But `--bg` mints a
  *new* session id (it ignores `--session-id`), so the resumed session's id differs
  from the one the record stored. Because `sessionLive` treats any lingering job
  entry as live — and `claude stop` leaves the stopped entry present — a record left
  pointing at the stopped id would read as live-and-idle and drive a wrong second
  resume that loses the just-added review. `continueReview` therefore re-captures the
  session ids after resume (like a fresh stage). Today that re-capture is *ambiguous*
  (the stopped prior entry and the resumed one share the instance cwd), so it yields
  empty ids and the record becomes non-continuable until dismissal: continuation is
  correct **once per session**, a further re-request before the human dismisses it
  Defers, and it re-stages Fresh after dismissal (the review is never lost, only its
  context carry-over for that interim push). **Chainable** continuation across
  multiple pushes before a dismissal needs a capture-newest disambiguation — a
  bounded follow-up that lights up with no change at the `continueReview` call site.
- **The resume execution + OS-sandbox re-entry were not executed in the authoring
  run.** The classifier, decision flip, id capture/validation, cap-neutrality, and
  the two-way liveness cross-check are all unit-tested offline; the actual
  stop-and-resume execution and the containment re-entry are covered by
  `TestResumedSessionDeniesEgress_OnHarness`, which is skip-gated (needs
  `NIWA_WATCH_LIVE_TEST=1` on a bwrap/claude host) and was not run without that infra.
  Containment is inherited structurally (same `ApplyReviewSettings` + `dispatchLaunch`
  path + pass-level fail-closed refusal as a fresh stage), a code-reviewable property,
  but the empirical egress-denial assertion should be run on a capable host before
  relying on continuation in `watch_sandbox=required` mode.

### Mitigations carried into the plan

- Sequence continuation last; keep A-C independently shippable.
- Cross-check liveness two ways and fail-closed to `Fresh`/`Defer` on doubt.
- Validate the id fields (charset) and confine `InstancePath` (under the managed
  instances root) before they become CLI args or path/checkout targets.
- Re-assert containment on every resume via the fresh-dispatch launch wrapper (OS
  sandbox re-entry, not hook-only); fail closed if it cannot be enforced. Carry an
  integration test that a resumed session's Bash egress is denied at the OS layer.
- Keep the re-review prompt a fixed template; PR content enters only as inert
  checkout data.
