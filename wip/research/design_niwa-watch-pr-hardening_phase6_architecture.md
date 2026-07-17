# Architecture Review

## Verdict: PASS

The state-split-by-lifetime spine is correctly grounded in the two shipped stores' actual lifecycles, the four decisions compose into a coherent deterministic pass, and the phasing genuinely stands alone; the open questions are Phase-D implementation details the design already isolates and marks deferrable, not structural flaws.

## Issues Found

1. **`ConversationID` capture is asserted but not established (Component 3 / Decision 2).** The grounding confirms the reusable capture primitive (`dispatchCapture` cwd-correlation) reads `sessionId/template/cwd` from `~/.claude/jobs/*/state.json`; it does *not* confirm that a *conversation id* is obtainable there. But `claude --resume` keys on `<conv_id>`, and the resume path being generalized (`sessionattach/supervise.go`) is the only place conv-id continuation is wired today. The design treats `SessionID` and `ConversationID` as two fields it will capture at stage time, but the load-bearing distinction — is the conv id actually retrievable from the watch launch's job-state source, or only the session id? — is left implicit. This is the single most important "does it compose" question. Suggested fix: in Phase D, add an explicit note that the capture step must yield a `--resume`-usable conv id (or map session id -> conv id via the supervise.go path), and state what happens if only a session id is capturable (degrade the live-idle branch to `Defer`, preserving the fallback). It is not a FAIL because Phase D is sequenced last and independently deferrable, and the `Defer` fallback is safe.

2. **GC's contribution to cap *accuracy* is slightly overstated (Decision 4 / Decision Outcome).** The design says the staged-record GC "is what makes the cross-run cap accurate." But the count is defined as records whose instance passes `instanceHasLiveJob` — a live probe evaluated at count time — so the count is accurate whether or not dead records have been pruned. The GC's real, and sufficient, justification is bounding *store growth* (the monotonic-growth gap) and freeing cap *capacity* on dismissal, not count correctness. Minor coherence nit; suggested fix: reword so GC owns "stops unbounded growth / frees capacity" and the live-probe owns "accurate count." Both are already present; only the attribution is loose.

3. **Session-side freshness pre-flight depends on the agent choosing to invoke it (Decision 3 / Component 7).** Hook point (B) is a deterministic subcommand, but a model session must *call* it before presenting its draft. The check's logic is deterministic and its exit is honored code-side, so R14 is respected — but coverage at the unblock-between-passes moment relies on the agent actually running the pre-flight. The design should state explicitly that the watcher-pass prune (hook A) is the deterministic backstop and that a skipped session-side check degrades to "caught next pass," never to a stale review shipped uncaught. This is implied by the two-hook design but not stated as the safety argument.

## Strawman Check

Rejected alternatives are largely genuine and carry the real trade-off, not a caricature:

- *Single JSON file replacing the flat handled-set* — genuine; "larger blast radius + discards the skip-malformed migration seam" is the actual cost, and the migration seam is a verified property of `state.go:47-49`.
- *SHA-in-record-only* — genuine and load-bearing; "records are GC'd, so a dismissed PR loses its last SHA and re-fires on unchanged head" is exactly the lifecycle mismatch that motivates Decision 1. Strong.
- *In-place injection via RC bridge* — genuine; grounded in niwa not driving RC and the R5 no-interruption constraint.
- *Counting records without GC* / *`*int` tri-state config* — both genuine, the latter grounded in the verified `DefaultPerRunBound` / `<=0`-means-default convention.
- **Thinnest: *always-stage-fresh* (Decision 2).** It is dismissed by appeal to "the settled decision rejects it" rather than re-analyzed on its own architectural merits. It is a *real* alternative with a real cost (context loss), so not a strawman, but it leans on an external prior ruling rather than standing on its own here. Acceptable given the reviewer's remit is this design, not re-litigating the settled scope.

## Summary

The design correctly places the last-dispatched SHA in the permanent handled-set (a dismissed PR's record is GC'd, so only the permanent store can suppress on an unchanged head) and adds the staged-record GC as the missing record-layer counterpart to the instance reaper — both decisions match the verified store lifecycles. Layering holds: the decision, freshness, and cap logic are pure functions in `internal/watch` over polled facts and on-disk state with no model in the deterministic path, wiring/process-control sits in `internal/cli`, and config follows the `watch_sandbox`/`GlobalSettings` resolver pattern; Phases A–C are genuinely independent of the net-new continuation work in D. The remaining risks — whether the watch capture site actually yields a `--resume`-usable conversation id, and the loose attribution of cap accuracy to GC — are Phase-D details and wording, both already contained by the design's own `Defer`/fail-closed fallbacks.
