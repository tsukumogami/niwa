# Clarity Review

## Verdict: PASS

The requirements and acceptance criteria are unambiguous at the WHAT level; two developers building against the same DESIGN would converge on the same behavior for every core flow, with a handful of minor clarifications that would remove residual interpretation gaps.

## Ambiguities Found

1. **R9 / AC "force-pushed off the dispatched SHA": force-push vs. normal advancement not distinguished.** R2 defines "new activity" as *any* head-SHA change, but R9's freshness check names only "force-pushed away from the dispatched head SHA" as the SHA-related failure. When ordinary new commits (a fast-forward, not a rewrite) land between staging at SHA `A` and unblock, it is unclear whether that trips the freshness check. One developer could implement "head != dispatched SHA -> stale"; another could implement "non-fast-forward relative to dispatched SHA -> stale." These diverge exactly when normal commits arrive. -> Clarify whether R9's SHA condition means "any head advance off `A`" or specifically "history rewritten / non-fast-forward off `A`," and state the intended unblock behavior for the normal-advance case (given poll-time R4/R5 and unblock-time R9 are separate loops, a session staged at `A` can be unblocked after head moves to `B` before the next poll).

2. **R12 / AC "oldest-first" backfill ordering key undefined.** "backfill from the still-pending PRs oldest-first" and "stages the oldest pending PR" do not say oldest *by what* — PR creation time, time the review was first requested, or first time the watcher observed it pending. Different keys produce different backfill orders under a burst. -> Name the ordering key (e.g., "oldest by review-request timestamp").

3. **R5 "live but busy or attached" - grouping is stated but the idle/busy boundary is only implied.** R5 and R8(b) treat "live-idle" and "live-busy/attached" as a clean partition, and R8 correctly defers the *mechanism* that distinguishes them to DESIGN. What is not stated at the WHAT level is whether "attached" (a developer connected to the session) is *always* treated as not-continuable even when otherwise idle. R5's phrasing implies yes, but a reader could infer an attached-but-idle session is continuable. -> One sentence confirming attached always defers, independent of idle/busy, would close this.

## Suggested Improvements

1. **Make the poll-time vs. unblock-time relationship explicit for the SHA-moved case.** The PRD cleanly separates the re-dispatch loop (R2-R7) from the freshness loop (R9-R10) but never states what happens when both could apply to the same PR (staged at `A`, head now `B`, developer unblocks before the next poll re-dispatches). Rationale: this is the one interaction where the two loops overlap, and Ambiguity #1 lives in that gap; a single clarifying sentence resolves it.

2. **Define the pending-PR ordering key once, referenced by R12 and its AC.** Rationale: "oldest-first" appears in both the requirement and the acceptance criterion but is testable only against a named key; without it the AC is not fully binary.

3. **State the attached-session rule explicitly in R5.** Rationale: removes the only reading under which R4 (continue live-idle) and R5 (defer live-busy/attached) could be applied inconsistently to an attached session.

## Summary

This PRD is clear and well-scoped: requirements use SHALL/SHALL NOT consistently, the acceptance criteria are binary and trace to numbered requirements, and mechanism is deliberately and legitimately deferred to DESIGN (resume mechanism, file format, cap value, idle/busy detection, hook point) rather than left vague. The residual ambiguities are narrow — the force-push-vs-normal-advance detection semantics in R9, the undefined "oldest-first" ordering key in R12, and the attached-session edge of R5 — none of which block the core flows but each of which could let two conforming implementations diverge on an edge case. Verdict is PASS; the three clarifications are cheap to fold in.
