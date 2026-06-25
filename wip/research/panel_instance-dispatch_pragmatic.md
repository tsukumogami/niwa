# Pragmatic Review

**Verdict:** PASS

The chain reuses existing create/destroy/mapping/reaper machinery, stays additive, and the one piece that looks like gold-plating (the marker+TTL backstop) is a forced move against a verified code constraint, not speculation.

## Blocking Findings
1. none

## Non-Blocking Notes
1. **Reaper marker+TTL backstop (Issue 5) is justified, not gold-plating.** The PRD's R32 ("no unreclaimable orphan, ever") is a hard requirement, and the code confirms the gap is real: `selectReapTargets` (reap.go:112) drops `!rec.Ephemeral` records, and the Ephemeral flag derives solely from the mapping store, so an instance created-but-not-yet-mapped is invisible to the existing sweep. A Go `defer` self-rollback genuinely cannot run on SIGKILL. Given R32 is in-scope, the backstop is the minimum mechanism that closes the gap — the alternative (an external supervisor) is heavier. Not YAGNI. The only judgment call is whether R32's "ever" should have been softened to "on any returned error" for v1, demoting the backstop to a follow-up; that is a PRD-scoping question, and since the user asked for exhaustive corner-case coverage, keeping it is consistent with intent.

2. **Separate-scan (not a `selectReapTargets` branch) is the right call, not over-abstraction.** Tempting to flag a second scan as duplicated reaper surface, but it's forced: the unmapped orphan is `Ephemeral:false` and dropped before any per-record check, so the backstop physically cannot live inside the existing loop. The DESIGN names this explicitly (D4). Correct and bounded.

3. **Pass-through flags `--model`/`--permission-mode`/`--agent` (D1) are mild speculative surface.** No PRD requirement asks for them (R2 only names prompt + optional label). They are cheap argv forwarding, so this is inert, but they are scope the PRD didn't request. Acceptable to keep (they're one-liners and match `claude --bg`'s surface); flag only if implementation cost grows.

4. **D3 supersedes R18 — clean simplification, worth noting.** The PRD specified scraping `backgrounded · <short-id>` from stdout (R18) then resolving the UUID (R19). The DESIGN drops the scrape entirely in favor of jobs-dir `cwd` correlation, removing a fragile-output dependency and a second test seam (fake stdout). This is the chain getting *simpler* downstream, which is the right direction; the traceability note correctly records R18 as subsumed.

5. **Under-specification risk: the backstop TTL value and config knob.** R45/Issue 5 require the TTL be "strictly longer than worst-case dispatch wall-clock" but no concrete default or config key is named, and dispatch wall-clock is "a property, not a tested threshold" (R45). A too-short TTL reaps a slow in-flight instance (the DESIGN's own listed risk). Issue 4 should fix a conservative default constant and Issue 5 should test the young-instance spare case against it — the PLAN does test the spare case (Issue 5 criterion 2), so the seam exists; only the concrete number is unstated. Minor, resolvable in implementation.

6. **Single-PR sizing is realistic.** Six issues, all additive, bottom-up with clear interfaces (two struct fields -> launcher -> capture -> command -> backstop -> one functional scenario). No issue refactors shared create/reaper internals; each is independently unit-testable; the heaviest (Issue 4) composes already-built pieces. Nothing here should be cut to a follow-up — splitting would ship building blocks with no standalone user value, as the PLAN argues.

7. **45-requirement PRD is proportionate to the explicit exhaustiveness ask, and the DESIGN/PLAN translate it without bloat.** The requirements collapse cleanly into 6 issues and ~8 DESIGN decisions; corner cases (concurrency race, capture ambiguity, partial-failure buckets, SIGKILL) each map to a real code constraint rather than inventing hypotheticals. This is exhaustive specification, not ceremony — the offline/live tagging and traceability table keep it auditable rather than padded.

## Summary
The scoping chain is pragmatically sound: it consistently reuses existing niwa primitives, stays strictly additive, and gets simpler (not more elaborate) as it moves from PRD to DESIGN (R18 scrape dropped for cwd-correlation). The marker+TTL reaper backstop — the obvious over-engineering suspect — is a forced move against a verified code constraint (unmapped instances are invisible to the existing reaper) and is the minimum mechanism satisfying the in-scope R32 guarantee, so it is justified rather than YAGNI. The only loose thread is the unspecified concrete backstop TTL default, a minor implementation-time detail the test seams already accommodate.
