# Decision 2 (critical): Session continuation + liveness/idle/attached detection

decision_provenance: inline-resolved (Tier-3 under /scope orchestration; the
mechanism was pre-delegated by the dispatcher, who named stop/resume-by-id + fresh
checkout as a candidate)

## Question
When there is new activity on a PR and a surviving review session exists, how does
the watcher (a) determine the session is live, (b) distinguish detached-idle from
busy/attached, and (c) continue the detached-idle session against the new diff
retaining its context — given that watch sessions today capture no session id, no
conversation id, no mapping, and niwa has no idle/attached detection and no
in-place prompt-delivery primitive?

## Evidence (from codebase investigation)
- Watch `stageReview` launches `claude --bg` and stops; it captures no session id
  and writes no SessionMapping. The only durable handle is `StagedRecord`
  (Handle/Owner/Repo/Number/URL/DraftPath) — no id, no instance path.
  (internal/cli/watch.go:183-274, internal/watch/state.go:114)
- Binary liveness EXISTS: `sessionLive(jobsDir, sessionID, now)` (precise, needs a
  session id) and `instanceHasLiveJob(jobsDir, instancePath)` (cwd-within-instance
  scan, no id needed). (internal/cli/job_state.go:72,110)
- `claude --resume <conv_id>` EXISTS as a supervise primitive but is wired ONLY to
  worktree lifecycle sessions (store A), which persist ClaudeConversationID.
  (internal/cli/sessionattach/supervise.go:44, attach.go:156-163)
- `dispatchCapture` recovers a session UUID by cwd-correlating ~/.claude/jobs/*/state.json.
  Dispatch persists the UUID in a SessionMapping; watch does neither.
  (internal/cli/dispatch_capture.go:41,78; dispatch.go:343-360)
- Idle-vs-busy: NOT detectable today. niwa reads only sessionId/template/cwd from
  job state.json and deliberately ignores `state`/`firstTerminalAt`
  (job_state.go:25-59). The data exists in the job file; niwa just does not read it.
- Human-attached: detectable only for store A (attach.state sentinel); NOT for
  dispatch/watch instances.
- Keep-alive (#209) is a payload-less prompt-prepend; remote-control is an external
  claude.ai/mobile steering surface niwa does not drive. Neither delivers a new
  prompt from niwa.

## Options

### Option A — In-place prompt injection via the remote-control bridge
Reuse the RC startup flag and push a "re-review" prompt into the running session.
REJECTED: niwa does not drive RC (no payload/client API); RC is an external
steering surface. Building an RC client is a large, out-of-scope capability, and
injecting into a mid-turn process is exactly the interruption R5 forbids.

### Option B — Stop-and-resume by conversation id + fresh checkout (CHOSEN)
At stage time, capture the review session's Claude conversation id and short id
(the dispatchCapture cwd-correlation already exists) and persist them, plus the
instance path, in the StagedRecord. On a later pass with new activity and a
surviving session:
- liveness from the persisted session id via `sessionLive` (precise), falling back
  to `instanceHasLiveJob` for the un-captured/legacy case;
- idle/busy/attached by reading the job state.json fields niwa currently ignores
  (`state`, terminal-attached indicator) behind a small classifier — this is the
  net-new detection the resume model needs;
- continue a detached-idle session by stopping it (`claude stop <short-id>`) and
  `claude --resume <conv_id>` in a freshly checked-out clone at the new head with a
  re-review prompt, generalizing the store-A supervise `--resume` path to watch.
This matches the dispatcher's named sketch and reuses the one continuation
primitive that exists. Cost: capture+persist ids in watch, build idle/attached
classification from job state, and wire stop-and-resume for watch sessions.

### Option C — Always stage fresh (no continuation)
REJECTED by the dispatcher's settled decision: discards the reviewer's accumulated
context, the exact outcome the resume model exists to prevent.

## Decision
Option B. It is the only path consistent with both the settled resume behavior and
the primitives that exist, and it is the mechanism the dispatcher pre-named.

## Load-bearing consequence (surface to dispatcher)
Option B is the heaviest, most novel part of ED2 by a wide margin: three of its
pieces (persisting a retrievable session/conv id for watch, idle/busy/attached
detection, stop-and-resume wiring for watch) do not exist today and touch session
lifecycle plumbing, whereas the other three PRD gaps (SHA-aware state, freshness,
cap) are comparatively contained. Mitigation carried into the Implementation
Approach: sequence the continuation mechanism LAST and make it independently
deferrable, so the SHA-aware state + freshness + cap land as real hardening even if
the continuation piece needs its own follow-up PR. Until continuation lands, the
"live session + new activity" branch DEFERS (does not stage a second session),
which is safe and never regresses the two-live-sessions invariant — it just does
not yet retain context. This will be flagged to the dispatcher in the final report
as a scope/effort finding, not silently absorbed.
