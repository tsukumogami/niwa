---
topic: niwa-watch-once-pr-review
chain_started: 2026-07-09T15:33:59Z
last_updated: 2026-07-09T15:42:04Z
phase_pointer: phase-2
visibility: Public
exit: UNSET
exit_artifacts: []
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
chain_ran:
  - brief
child_snapshots:
  brief:
    status: Draft
    content_hash: ef3cac080b1be3e04c9aa39da374cdfd1c8eb65b
    captured_at: 2026-07-09T15:42:04Z
worktree_rebases: []
# HARD APPROVAL GATE (dispatcher): /brief produced BRIEF in Draft; jury
# all-PASS (content-quality + structural-format). Execution PAUSED here
# awaiting dispatcher approval before /prd, /design, /plan and the Accept
# transition of the BRIEF. Sentinel cleared (child returned).
paused_at_gate: brief
# Dispatcher-imposed hard approval gate: produce BRIEF, then STOP for
# human approval before invoking /prd, /design, /plan. See dispatch brief
# at .niwa/dispatch-briefs/ed1-watch-once.md. Roadmap feature: ED1 of
# ROADMAP-event-driven-dispatch (vision PR #554).
---

# Scope state: niwa-watch-once-pr-review (ED1)

## Phase 1 discovery verdict (cold-start)

No on-disk artifacts at any canonical path; no framing shift (fresh feature).

Chain-proposal gate verdicts:
- /brief  — fires (R4 EITHER-signal: no upstream BRIEF at canonical path)
- /prd    — fires (R5 Mandatory-with-auto-skip: no Accepted PRD at canonical path)
- /design — fires (R7 shape-dependent; projected P1/P2/P3 pre-PRD):
    - P1 architectural-alternatives: fires — sandbox-profile carrier
      (.claude/settings.json merge vs --settings flag), env-scrub model
      (allowlist vs denylist), handled-set file format/location are open
    - P2 new-component: fires — net-new `niwa watch --once` verb + net-new
      dispatch enforcement surface (sandbox profile application + env scrubbing)
    - P3 Complex: fires — security-critical remote-execution boundary
      (Bar A) that must be adversarially tested
- /plan   — fires (ALWAYS)

## Approval-gate note

Per dispatcher brief, execution STOPS after /brief returns until the
dispatcher approves. Only then do /prd, /design, /plan run, followed by
shirabe:execute.
