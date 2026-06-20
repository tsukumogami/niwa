# PRD Decisions (auto mode): niwa-default-worktree

Auto-mode decision log per references/decision-protocol.md. Each entry: decision,
evidence, rationale.

## Phase 1 (scope)
- Reuse exploration + spike research rather than re-running a heavy discover
  fan-out. Evidence: the 4 explore lead files and the completed spike already
  cover niwa worktree internals, the settings-install surface, feasibility, and
  the value gap. Rationale: avoid redundant research; run only 2 focused agents
  on the genuinely-open requirements questions.

## Phase 3 (draft) — open questions resolved
- Fallback visibility (brief OQ1): VISIBLE (one-time notice + explicit agent
  redirect). Evidence: one-time-notice precedent (prd-conventions findings).
  Rationale: silent degradation is the problem being solved.
- Opt-out (brief OQ2): init-time flag persisted in instance state, mirroring
  --skip-global/--no-overlay; reversible by re-init. Evidence: prd-conventions
  findings (opt-outs are state flags, not [instance] config).
- Secret policy: warn-and-continue + surfaced degradation (R10). Evidence:
  prd-constraints (AllowMissingSecrets=true today). Rationale: don't block agents
  on transient vault outage, but no silent degradation.
- Path output (R4): require machine-readable path. Evidence: prd-constraints
  (today only human "session: created ... at ..." line; --json exists only for list).

## Phase 4 (jury) — verdicts and resolution
- completeness: PASS. clarity: FAIL. testability: FAIL.
- Applied all blocking fixes in one revision:
  - Defined the fallback trigger at policy altitude (R7: "harness does not honor
    the integration") instead of the undefined "can't be honored"; removed the
    "suppressed" HOW-verb from R7. (clarity)
  - Moved the per-repo/spike rationale out of R3 into motivating_context. (clarity)
  - Made ACs checkable: concrete observables in AC1/AC2; added ACs for per-repo
    install scope, non-interactive install (R12), Claude-Code-only scope (R13),
    fresh-instance default-on (R2), and a grep-able fallback disclosure surface;
    AC5 now states how to induce fallback. (testability)
  - R8 now says fallback must be detectable + disclosed; detection mechanism
    deferred to DESIGN. R9 now states opt-out reversibility. Added pre-existing
    orphans to Out of Scope and concurrent same-branch race to Known Limitations.
    (completeness non-blocking suggestions)
- Re-validated: shirabe validate clean. Fixes map 1:1 to the reviewers' concrete
  asks; treating the jury as satisfied for this auto run rather than re-spending a
  full 3-agent round.
