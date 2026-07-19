---
schema: prd/v1
status: In Progress
problem: |
  Whoever holds a niwa dispatch handle — a headless coordinator, an
  operator, or niwa watch — cannot hand the running worker a follow-up
  task. The only available maneuver forks a new session, orphaning the
  original, desynchronizing the durable session mapping, and letting two
  sessions contend for one instance. Every consumer rediscovers this
  failure and builds its own partial workaround.
goals: |
  One niwa command delivers a follow-up instruction to a dispatched
  session through its handle. The worker continues with its accumulated
  context, exactly one live session owns the instance afterward, the
  mapping and niwa list stay truthful, and repeated retasks chain
  indefinitely. The mechanism uses only supported Claude Code surfaces,
  needs no root, and can adopt a native platform delivery channel later
  without changing its interface.
upstream: docs/briefs/BRIEF-dispatch-handle-retask.md
motivating_context: |
  A coordinator re-tasked a dispatched worker via `claude --resume --bg`
  and got a forked session plus an orphan. niwa watch's review
  continuation independently degraded to once-per-session for the same
  reason (#211). Spikes against Claude Code 2.1.214 confirmed no
  supported headless in-place push into a live background session
  exists, and third-party inbound channels are blocked on unattended
  sessions — so niwa must own safe re-task semantics on top of what the
  platform does support.
---

# PRD: Dispatch handle retask

## Status

In Progress

## Problem Statement

`niwa dispatch` records a durable session-to-instance mapping and hands
back a handle good for attach, logs, stop, and reap — but not for
delivering another instruction. Once a worker consumes its launch
prompt, no headless path can give it more work: resuming a live
background session forks a new session id, the superseded session
lingers as an orphan job entry, and the mapping now points at a session
that will never answer. The failure has independent victims: a
coordinator agent re-tasking its workers, and niwa watch's review
continuation, whose re-capture goes ambiguous after one resume and
degrades to once-per-session (#211). The platform constraint is
verified and durable; what's missing is niwa managing it once, safely,
for every consumer.

## Goals

- A single command delivers a follow-up instruction to a dispatched
  session identified by its handle.
- Context continuity: the worker resumes with its full prior
  conversation.
- Ownership stays clean: one live session per instance, mapping rebound
  atomically, superseded sessions removed.
- Retasks chain: the second and Nth retask work as well as the first.
- Watch's continuation adopts the same primitive and stops degrading.
- The delivery mechanism is replaceable: when the platform's inbound
  channel path becomes available to third parties, niwa can switch to
  it without changing the command's interface or semantics.

## User Stories

- As a headless coordinator agent, I want to push a follow-up task to a
  worker I dispatched, so that it continues in its instance with its
  accumulated context instead of me forking a duplicate session.
- As an operator, I want to re-task a dispatched worker from my
  terminal without attaching to it, so that iteration doesn't require
  taking over the session interactively.
- As the niwa watch loop, I want to continue a detached-idle review
  session at a new PR head repeatedly, so that reviewer context chains
  across pushes instead of dying after the first continuation.
- As an operator running `niwa list`, I want the mapping to stay
  truthful through retasks, so that liveness, keep-alive markers, and
  reap decisions remain correct.

## Requirements

### Functional

- **R1. Command surface.** `niwa retask <target> <prompt>` delivers a
  follow-up instruction to a dispatched session. `<target>` accepts the
  same identifiers the session mapping already resolves: the instance
  name and the session short id. Exactly one prompt argument is
  accepted and passes to the worker as a single argument with no shell
  interpolation, matching dispatch's existing guard.
- **R2. Context continuity.** After a retask, the worker's conversation
  contains its full prior transcript followed by the new instruction.
- **R3. Single-owner rebind.** A successful retask leaves exactly one
  live session bound to the instance. The durable mapping is updated to
  the surviving session's ids in the same operation that establishes
  it, and the superseded session's job entry is removed. No orphan job
  entry and no stale mapping survive a successful retask.
- **R4. Worker-state coverage.** Retask succeeds against a worker that
  is (a) live and idle (terminal state, detached) or (b) stopped with
  its job entry intact. Retask against a worker that is actively
  running a turn, attached to a terminal, or whose job entry is gone
  fails closed with a distinct, actionable error and changes nothing.
- **R5. Chainable capture.** When the platform mints a new session id
  underneath, retask reliably recovers the surviving session's ids even
  though the instance briefly correlates with more than one job entry.
  Ambiguity is resolved deterministically (newest-registration wins,
  validated before use); an unresolvable capture fails closed without
  corrupting the mapping. Retask N+1 works after retask N.
- **R6. Handle stability.** The niwa-level handle (instance name,
  mapping file identity) survives retasks unchanged. Callers never need
  the rotated session id to keep operating on the worker.
- **R7. Watch adoption.** niwa watch's continuation path uses the same
  retask primitive. The no-egress sandbox posture watch applies to
  review sessions (see DESIGN-niwa-watch-pr-hardening) is re-asserted
  through the same settings-applying launch path on every continuation,
  and continuation chains across multiple pushes before dismissal.
- **R8. Supported surfaces only.** The implementation uses documented
  or stable Claude Code CLI surfaces (resume, stop, respawn, jobs-dir
  reads). It does not edit Claude Code's internal state files, does not
  require root or managed settings, and does not depend on the
  currently-fenced third-party channel mechanism.
- **R9. Replaceable delivery seam.** The delivery step (how the
  instruction reaches the session) is isolated behind one internal
  seam, so an in-place channel delivery can replace fork-and-rebind
  without changing the command surface, the mapping schema, or R2-R6
  semantics.

### Non-functional

- **N1. Fail-closed.** Any failure before the mapping rebind leaves the
  prior session, job entry, and mapping intact and usable; the command
  reports the failure and exits non-zero. A retask is never partially
  applied from the caller's perspective.
- **N2. Concurrency safety.** Two concurrent retasks against the same
  target cannot both succeed; the loser fails closed with a clear
  error. A retask concurrent with `niwa reap` never yields a reaped
  instance with a live session.
- **N3. Observability.** `niwa list` output (human and `--json`)
  reflects the surviving session immediately after a retask, including
  keep-alive reporting. Errors name the target, the detected worker
  state, and the reason.
- **N4. No new privileges.** No sudo, no writes outside the workspace,
  the jobs dir surfaces already consumed, and niwa's own state.

## Acceptance Criteria

Each criterion is labeled with its verification level: **unit** (offline
Go tests against seams and fakes), **integration** (a local Claude Code
daemon with throwaway sessions), or **live gate** (the existing
disposable-host gate watch already uses).

- [ ] (integration) `niwa retask <instance-name> "<prompt>"` against a
  live-idle dispatched worker resumes it with full prior context;
  afterwards `niwa list --json` shows exactly one session for the
  instance and its ids match the mapping on disk.
- [ ] (integration) The same command against a stopped worker (job
  entry present) revives it and delivers the instruction, meeting the
  same post-conditions.
- [ ] (integration) After a successful retask, the superseded session's
  job entry is gone and `claude agents --json` shows no second session
  for the instance cwd.
- [ ] (integration) Three consecutive retasks against the same target
  all succeed, each carrying the accumulated context forward, with the
  caller using only the instance name it got at dispatch time — at no
  point does it need the rotated session id (R6).
- [ ] (integration) Retask against an actively-working worker exits
  non-zero with an error naming the busy state; the worker's run is not
  interrupted and the mapping is unchanged.
- [ ] (unit) Retask against an unknown target, an ambiguous capture, or
  a gone job entry exits non-zero with a distinct error per cause and
  leaves all state unchanged.
- [ ] (unit) Two concurrent retasks against the same target: exactly
  one succeeds and the other fails closed with a concurrency error;
  the surviving mapping matches the winner. A retask racing a reap
  sweep either completes the rebind before the reaper reads liveness or
  fails closed — a test drives both interleavings through the liveness
  and store seams and asserts no reaped-instance-with-live-session
  state is reachable (N2).
- [ ] (integration) Watch continuation drives a second and third
  re-request at newer heads through the retask primitive while the
  review session stays undismissed, with the staged record holding
  valid surviving-session ids after each (chaining past the current
  once-per-session degradation).
- [ ] (live gate) A watch-sandboxed session retasked through the
  primitive has its egress-denial settings re-asserted, verified by the
  existing disposable-host gate.
- [ ] (integration) The prompt argument is delivered without shell
  interpretation (a prompt containing `$(...)`, quotes, and newlines
  arrives byte-identical).
- [ ] (unit) Capture disambiguation with two job entries sharing the
  instance cwd: newest registration wins; a tie or an invalid id fails
  closed.

## Out of Scope

- Interrupting or queueing into a worker mid-turn. V1 refuses busy
  workers; queue-on-busy arrives only with a native channel delivery
  (see Known Limitations).
- The human steering paths (terminal attach, claude.ai remote control)
  and keep-alive itself; retask neither requires nor arms keep-alive.
- Building or distributing an approved channel plugin, and any change
  to Claude Code itself. The platform gaps are recorded for an upstream
  feature request tracked separately from this PRD.
- Re-tasking sessions niwa did not dispatch, sessions from another
  workspace, or non-ephemeral sessions without a mapping.
- Multi-prompt batching, scheduled or conditional retasks.

## Known Limitations

- **Fork-under-the-hood.** Until a native in-place delivery exists, a
  retask rotates the underlying Claude session id (transcript is
  preserved). Anything holding the raw session id across a retask —
  claude.ai remote-control bookmarks, external notes — points at the
  superseded session. The niwa handle is the stable identity (R6).
- **Busy means refused.** Fork-tolerant delivery cannot land an
  instruction as "the next turn" of a running worker without
  interrupting it, so v1 fails closed on busy workers rather than
  queueing.
- **Keep-alive interplay.** A superseded session's keep-alive self-wake
  dies with its job entry on removal (verified platform behavior); the
  surviving session does not inherit keep-alive and would need it
  re-armed by a future dispatch-level mechanism if wanted.

## Decisions and Trade-offs

- **D1. Command surface: one verb, mapping-resolved targets.**
  `niwa retask <target> <prompt>` with `<target>` as instance name or
  session short id. Alternatives: separate `send`/`continue` verbs, or
  session-UUID-only targeting. One verb keeps the mental model "the
  handle you got from dispatch is the thing you retask"; UUIDs remain
  usable indirectly because the mapping stores them. (Closes the
  brief's command-surface question.)
- **D2. No keep-alive coupling.** Retask neither implies nor requires
  keep-alive: revive-on-retask (respawn preserves the session id)
  covers stopped workers, so reachability maintenance stays a separate
  opt-in. Alternative — auto-arming keep-alive on retask — was
  rejected: it couples an instruction-delivery command to a
  reachability policy and can't be honored reliably from outside the
  session. (Closes the keep-alive question.)
- **D3. Channel-path adoption via internal seam, not interface.**
  Forward-compatibility is a requirement on the internal delivery seam
  (R9), not a CLI mode flag. Alternative — a `--via channel|resume`
  flag — was rejected: callers shouldn't choose mechanics, and the
  fenced channel path would make the flag dead weight. (Closes the
  channel-adoption question.)
- **D4. Sandboxed sessions: same primitive, settings re-asserted.**
  Watch adopts the shared primitive and the primitive re-runs the
  sandbox-applying launch path (R7), matching the containment
  discipline watch's continuation already enforces. Alternative — excluding sandboxed sessions
  from retask — was rejected: watch is a primary consumer. (Closes the
  sandbox-interaction question.)
- **D5. Superseded session removed immediately.** After a successful
  rebind the old job entry is removed in the same flow (R3).
  Alternative — retaining it briefly for audit — was rejected: the
  transcript survives in the new session, and a lingering entry is
  exactly the ambiguity that breaks capture (#211). (Closes the
  cleanup question.)
- **D6. Busy workers are refused, not deferred.** The command fails
  closed and leaves scheduling to the caller (watch already has its
  own defer loop). Alternative — an internal wait/retry — was
  rejected: it hides latency policy inside a primitive whose consumers
  have different patience budgets.
- **D7. Capture disambiguation is in scope, not a dependency.** R5's
  chainable capture is this feature's work, not a wait on #211: without
  it, a second retask before the superseded session is dismissed
  strands the mapping — the exact once-per-session degradation watch
  has today. Alternatives — depending on #211 landing separately, or
  scoping v1 to one retask per session — were rejected: the first
  sequences two features around one invariant, the second ships the
  known failure. Shipping R5 closes #211's ask for watch as a side
  effect (R7).

## Downstream Artifacts

- (populated as the design and plan land on this branch)
