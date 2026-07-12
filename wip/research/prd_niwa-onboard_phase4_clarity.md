# Clarity Review

## Verdict: PASS

The PRD is specific enough that two developers would build the same thing: requirements use RFC 2119 language, acceptance criteria are overwhelmingly binary and testable, and every genuinely open detail is explicitly and consistently deferred to the DESIGN rather than left as silent ambiguity.

## Ambiguities Found

1. **R15 / AC-20 ("resume sensibly", partial-resume granularity)**: R15 says re-running against a partial setup MUST "resume sensibly: steps already done are detected and skipped, and the wizard picks up where it left off," and AC-20 asserts it "skips the already-done steps and resumes at the first incomplete one." -> The set of discrete "steps" and their detection predicates is never enumerated, so "already done" and "first incomplete" have no fixed referent. Two developers could pick different checkpoint granularities (e.g., per-phase vs. per-REST-call) and both pass AC-20. -> The PRD flags resume *bookkeeping* as DESIGN-level, which covers the mechanism, but the requirement would be sharper if it named the observable checkpoints the wizard keys off (identity exists, folders exist, credential resolves) so the resume boundary is a requirement, not just a mechanic.

2. **R10 / AC-15 (`api_url` "non-default" determination)**: The body carries `api_url` "optional; omit for the provider default" and AC-15 verifies it appears "only when non-default." -> How the wizard determines what the "provider default" is, and therefore when to omit vs. emit the field, is unspecified. A reviewer cannot objectively check "non-default" without knowing the default source. -> State where the default comes from (a compiled-in constant, the provider declaration, the CLI's own default) so the omit/emit decision is deterministic.

3. **R6 / R7 / AC-9 / AC-11 ("with what settings" in guided dashboard instructions)**: The wizard MUST print "exactly what to create, where, and with what settings" (R6) and AC-9/AC-11 verify that guided instructions naming "what to create and with what settings" are emitted. -> The *content* of those instructions (which settings, which values) is not specified, so the AC verifies that instructions are printed, not that they are correct or complete. This is inherently subjective as written — a reviewer can confirm a string appears but not that it is the right guidance. -> Acceptable to defer the exact wording, but consider adding a criterion that the printed settings match a fixture/golden file so correctness is checkable, not just presence.

4. **R2 ("for example" detection signals)**: Setup inference draws on "observable workspace and session state (for example, whether the team identity and folder structure already exist...)". -> The parenthetical is illustrative, not exhaustive, so the exact inference inputs and their precedence are open; two implementations could weight signals differently. -> Low risk because R2's mandatory confirm-or-override guard (AC-2, AC-4) makes a wrong inference recoverable rather than silent. Deferring the mechanism to DESIGN is reasonable; just confirm the DESIGN is expected to close the exact signal set.

## Suggested Improvements

1. **Enumerate the resume checkpoints at the requirement level**: Even leaving bookkeeping to DESIGN, naming the observable states that define a completed step (identity present, Universal Auth attached, grant present, folders present, credential resolves) would make AC-20 objectively verifiable and remove the only "sensibly"-class soft word in the requirements.

2. **Pin the `api_url` default source in R10**: One clause stating how "provider default" is determined turns AC-15's "non-default" clause from a judgment into a binary check.

3. **Tie guided-instruction ACs to a golden fixture**: AC-9 and AC-11 currently verify presence of guidance, not its content. Referencing an expected-output fixture would make "what settings" checkable without over-specifying prose in the PRD.

## Summary

This is an unusually clear PRD: 31 acceptance criteria that are almost entirely binary and independently verifiable, RFC 2119 normative language throughout, and a disciplined pattern of explicitly flagging DESIGN-deferred mechanics rather than hiding them behind vague adjectives. The few soft spots — "resume sensibly" with an unenumerated step set, the `api_url` non-default rule, and the content (not presence) of guided dashboard instructions — are minor and mostly bounded by confirm/override guards or explicit DESIGN deferral. None rise to the level where two developers would build materially different things, so the PRD passes on clarity.
