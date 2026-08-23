# Phase 6 jury: codex-instance-root-skills

Serial-self-jury under the parent-orchestration dispatch fallback: the
three reviewer rubrics were walked in sequence by the authoring session
and their verdicts folded into one table.

## Architecture review

1. Implementable: yes -- components name real files, functions, and the
   registries they extend; the data flow names the pipeline step each
   delivery rides. PASS.
2. Missing components: the pre-flip shape of increment 4 originally
   implied the offline scenario could land before the flip, which would
   fail; corrected to state the increment is inert until the flip.
   APPLIED.
3. Sequencing: warning re-gate deliberately lands before the flip (no
   silent window); flip is one commit; cycle removal first and
   behavior-preserving. PASS.
4. Simpler alternatives: identity-only registration and plan-tag-only
   binding are simpler and were rejected on requirement grounds, with
   the requirement named. PASS.
5. Note, not blocking: RootSettingsMaterializer's name sits near the
   workspace-root writeRootSettings; implementation should keep the
   instance-root/workspace-root distinction in the type's doc comment.

## Security review

1. Attack vectors: upward-searching exclude writes (named and
   forbidden), name-collision replacement of niwa's tree (deterministic
   rule), symlink escape (producer name rule, same as per-repo). PASS.
2. Mitigations sufficient: yes; the no-containment-pass posture is
   inherited with its recorded justification, not newly invented. PASS.
3. N/A claims: none claimed N/A; all four dimensions addressed. PASS.
4. Residual risk: Claude-side registration defect is recorded, not
   fixed -- an honesty question, not an exposure; the live check's
   credential-free posture is a requirement. PASS.

## Structural-format review

1. Section presence and order: Status, Upstream Design Reference
   (tactical, upstream exists), Context and Problem Statement, Decision
   Drivers, Considered Options, Decision Outcome, Solution
   Architecture, Implementation Approach, Security Considerations,
   Consequences, References. PASS.
2. Frontmatter: schema/status/upstream before problem/decision/
   rationale, matching the house example
   (DESIGN-agent-capability-contract.md); literal block scalars used.
   decision_provenance recorded per the dispatch fallback. Validator
   verdict is the authority. PASS pending validator.
3. Altitude: no PRD-altitude requirements restated as requirements (the
   design carries what each requirement asks for in the section that
   answers it); no PLAN-altitude issue lists. PASS.
4. Budget: Decision 2 is the longest section and carries four rejected
   alternatives plus the R7 position the PRD requires recorded here;
   judged content-bearing, not overshoot. PASS.

## Strawman check

Every rejected alternative states what it proposed, why it was viable,
and the specific requirement or measured fact that rejected it; two
(plan-tag-only binding, marketplace-directory extraction) are argued as
the codebase-consistent or natural-looking options before rejection.
PASS.

## Consolidated actions

| Source | Feedback | Action | Applied |
|--------|----------|--------|---------|
| Architecture | Increment 4 implied pre-flip scenario | Reworded: inert until flip | [x] |
| Architecture | RootSettingsMaterializer naming note | Left to implementation, recorded here | [x] |
| Security | none blocking | -- | [x] |
| Structural | validator is authority on frontmatter extras | run shirabe validate | [x] |
