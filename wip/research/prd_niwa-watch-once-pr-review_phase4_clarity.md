# Clarity Review

## Verdict: PASS

The revision resolves all four prior notes with HOW consistently labeled DESIGN-deferred; only one low-severity flag-naming inconsistency remains and it does not let two developers build materially different features.

## Ambiguities Found

1. R5 vs. R13/D7 (dispatch flag naming): R5 mandates dispatch "always with `--detach` (`-d`)", but R13 and D7 justify agent-view surfacing by "a `--bg` worker auto-registers." -> Two different flag names (`--detach`/`-d` vs `--bg`) are used for what the surfacing story treats as one mechanism; if they are distinct flags, a `--detach` dispatch may not auto-register in the agent view R13 depends on, and two developers could wire this differently. -> Use one flag name throughout, or add a sentence stating that `--detach` carries the `--bg` background-registration behavior.

2. R11 "successfully dispatched under enforced containment" (also AC17): "successfully dispatched" is not tied to an observable event. -> With `--detach` the parent returns before the agent drafts, so it is unclear whether success means the detached process launched or that a draft was produced; the handled-set write point (R11/R12) hinges on this. -> Pin "successful dispatch" to a concrete signal (e.g. "the `niwa dispatch -d` invocation exits zero"). Low severity: context implies launch-success, but the phrase is load-bearing.

## Suggested Improvements

1. Prior note R6 (known location) is resolved: R6 now carries "(The exact path is a DESIGN detail; the requirement is that the location is fixed and predictable, not chosen ad hoc by the agent.)" -- correctly labeled DESIGN-deferred while pinning the fixed/predictable contract.

2. Prior note R8 (minimum the task needs) is resolved: R8 now fixes the mechanism as allowlist-based ("not a best-effort scrub of a denylist"), defers only the allowlist contents to DESIGN, and AC12 verifies the session env is a subset of the allowlist. The residual "minimal" is bounded by the mechanism and no longer hides ambiguity.

3. Prior note R11 (stable PR identity) is resolved: now defined inline as "repository plus PR number," consistent with D2 and AC-level dedup.

4. Prior note R2 (soft "e.g." on qualifier) is resolved: R2 names `user-review-requested` with no hedge and D3 is marked "Decided (confirmed)"; the confirmed decision is no longer undercut.

5. AC8/D5 bound N is clean HOW-deferral (AC8 parameterized on configured N; exact value and configurability deferred to DESIGN), not ambiguity. Noted to confirm it was checked.

## Summary

All four prior minor notes are tightened: R6's "known location" and R8's allowlist are now explicitly DESIGN-deferred with the mechanism pinned, R11 defines "stable PR identity" as repository plus PR number, and R2's qualifier decision is stated without the undercutting "e.g." Requirements are specific, acceptance criteria are binary and self-verifying (direct-execution security tests, byte-identical prompt assertion, parameterized bound), and subjective terms are bounded or labeled DESIGN-deferred. The only new issues are a low-severity `--detach`/`--bg` flag-naming inconsistency and an undefined "successfully dispatched" event; neither is requirement-defining, so the PRD passes.
