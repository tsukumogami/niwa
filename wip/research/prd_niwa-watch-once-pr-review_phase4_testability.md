# Testability Review

## Verdict: PASS

The revision resolves every prior FAIL: the security ACs now specify concrete mechanisms (canary secret + allowlist-subset, direct-execution to bypass model judgment, credential-absence + egress-denial to bound the negative), and a full test plan is writable from AC1-AC18 alone; only two secondary coverage gaps remain, neither an untestable criterion.

## Prior-Issue Verification

- **R8 "unrelated secrets" baseline** — FIXED. AC12 plants `NIWA_CANARY_SECRET` in the dispatcher env and asserts (a) its absence from the session and (b) session env is a subset of the defined allowlist. Both halves are concrete: grep for the sentinel, set-difference against the allowlist.
- **R17 "denied at OS layer not model" discrimination** — FIXED. AC9 and AC14 both require the outbound action be executed directly in the session shell, bypassing model judgment, so denial is provably the sandbox's doing (connection blocked / EPERM / proxy refusal), not the model declining. The discrimination method is now explicit.
- **R14 "session never posts" unbounded negative** — FIXED. AC15 bounds the negative through mechanism rather than open-ended observation: the session holds no posting credential (AC12 baseline) and egress is denied (AC9), so it physically cannot post; the window is bounded to "between dispatch and explicit approval."
- **R3/R6/old-R13 (now R15) missing ACs** — FIXED. AC4 covers R3 (out-of-workspace), AC7 covers R6 (draft artifact at known location, halted), AC18 covers R15 (byte-identical prompt, no model call).
- **Missing zero-match coverage** — FIXED. AC6 covers the empty poll (exit zero, "nothing to stage" message).

## Per-AC Testability (security focus)

- **AC9 (egress)**: concrete — direct `curl` in session, assert connection failure. PASS.
- **AC10 (write-scoping)**: concrete — direct write outside clone, assert denial. PASS.
- **AC11 (fail-closed)**: testable but the weakest — "a privileged action that would otherwise prompt for approval" is defined by behavior, not a named action, so the test author must identify a concrete prompt-triggering action from the (DESIGN-level) harness config. Verifiable in principle; slightly under-specified at PRD altitude.
- **AC12 (canary + allowlist subset)**: concrete, both directions. PASS. (The subset check needs the allowlist defined in DESIGN, which the PRD correctly defers.)
- **AC13 (fail-closed refusal)**: testable — assert non-zero exit, message names the containment failure, no session launched. Caveat: the test must be able to *induce* "OS sandbox absent/unsupported" deterministically (unsupported platform or an injectable fault); the PRD does not say how, but that is a DESIGN/test-harness concern, not an untestable criterion.
- **AC14 (adversarial direct-execution)**: concrete and strongest — three named outbound actions, each executed directly and asserted denied at the OS/tool layer. PASS.
- **AC15 (post-impossibility)**: now concretely verifiable via credential-absence + egress-denial plus PR observation. PASS.
- **AC18 (determinism)**: byte-identical prompt from identical metadata is directly assertable; "no model/LLM call on poll/relevance/prompt path" is verifiable architecturally (no LLM dependency on the path) or by instrumenting for model-API calls. PASS.

## Untestable Criteria

None that block. One soft item:

1. AC11 "an action that would otherwise prompt for approval": the trigger is defined by harness behavior rather than a named action -> have DESIGN/test name the concrete prompt-triggering action used as the fixture, so the fail-closed assertion is reproducible.

## Missing Test Coverage

1. **R12 poll-failure branch**: AC17 covers the *dispatch*-failure branch (not recorded, re-attempted) and AC6 covers the *empty-but-successful* poll, but no AC exercises a **failed poll** (auth expired, host unreachable, rate limit) asserting exit non-zero, fail-loud message, and nothing recorded as handled. This is the security-relevant "looks like nothing to review when actually broken" case from D6 -> add an AC for a failing GitHub poll.
2. **R13 agent-view surfacing**: no AC verifies the staged session is actually surfaced in the existing Claude Code agent view. AC7 only checks the draft artifact exists on disk. Also a spec seam: R13 says a `--bg` worker auto-registers, while R5/AC1 dispatch with `--detach` (`-d`); if these are the same registration path it is fine, but a test should confirm the `-d` dispatch registers -> add a lightweight AC (or fold into AC7) asserting the staged session appears in the agent view.
3. **R16 GitHub-only scope**: no AC; acceptable as a negative scope statement, noted for completeness only.

## Summary

The revision converts the previously untestable security criteria into concrete, mechanism-based assertions: the canary-secret baseline (AC12), the direct-execution discrimination between sandbox-denial and model-refusal (AC9/AC14), and the credential-absence-plus-egress-denial bounding of the never-posts negative (AC15) are all now verifiable, and the zero-match, team-only, out-of-workspace, per-run-bound, and dispatch-failure edges are covered. A complete test plan is writable from AC1-AC18 alone. The remaining gaps are two secondary coverage items — the failed-poll branch of R12 and explicit agent-view surfacing for R13 — plus a minor request to name AC11's concrete prompt-triggering fixture; none is an untestable criterion, so the verdict is PASS.
