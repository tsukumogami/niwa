```yaml
topic: worktree-delegation-durability
chain_started: 2026-07-28T00:00:00Z
last_updated: 2026-07-28T00:00:00Z
phase_pointer: phase-3
visibility: Public
exit: full-run
exit_artifacts:
  - docs/plans/PLAN-worktree-delegation-durability.md
  - docs/designs/current/DESIGN-niwa-default-worktree.md
  - docs/spikes/SPIKE-enterworktree-hook-bypass.md
planned_chain:
  - design
  - plan
chain_ran:
  - design
  - plan
chain_skipped:
  - name: brief
    reason: settled-upstream-brief-inherited-from-niwa-default-worktree-with-no-framing-shift
  - name: prd
    reason: settled-upstream-prd-inherited-from-niwa-default-worktree
child_snapshots:
  brief:
    path: docs/briefs/BRIEF-niwa-default-worktree.md
    status: Done
  prd:
    path: docs/prds/PRD-niwa-default-worktree.md
    status: Done
  design:
    path: docs/designs/current/DESIGN-niwa-default-worktree.md
    status: Current
```

## Upstream context

- Issue: tsukumogami/niwa#221 (retargeted — see the exploration decisions).
- Exploration handoff: `wip/explore_enterworktree-hook-bypass_crystallize.md`,
  `wip/explore_enterworktree-hook-bypass_findings.md`,
  `wip/explore_enterworktree-hook-bypass_decisions.md`.
- Accepted upstream PRD: `docs/prds/PRD-niwa-default-worktree.md` (status Done,
  unchanged — R1/R5/R7/R8 still hold).
- Shipped design to revise in place:
  `docs/designs/current/DESIGN-niwa-default-worktree.md` (Decisions 4 and 6).
- Falsified spike to rewrite or delete:
  `docs/spikes/SPIKE-enterworktree-hook-bypass.md` (status Complete).

## Phase 1 gate verdicts

- **`/brief` — does not fire** (R4 EITHER-signal, neither signal holds). The
  settled upstream BRIEF for this feature is `BRIEF-niwa-default-worktree.md`
  at status Done, and the framing has not shifted: the problem shape, audience,
  scope boundary, and success criterion are all unchanged. What changed is that
  a shipped mechanism has a durability defect, which is design-altitude, not
  framing-altitude.
- **`/prd` — does not fire** (R5 Mandatory-with-auto-skip). `PRD-niwa-default-worktree.md`
  is at status Done and its requirements still hold; the exploration confirmed
  R1/R5/R7/R8 are the right contract and were never renegotiated.
- **`/design` — fires** (R7 shape-dependent).
  - **P1 — fires.** Two decisions have multiple viable implementations left
    open: how the hook command resolves niwa durably (PATH-first, absolute path
    plus staleness detection, or a guarded hybrid), and whether a failed
    `from-hook` rolls back or retains a failure-marked session.
  - **P2 — does not fire.** No new binary, service, library, or runtime
    substrate. All changes land in existing packages (`internal/workspace`,
    `internal/cli`).
  - **P3 — does not fire.** The upstream PRD carries no Complex classification
    and makes no architectural-complexity claim about this work.
- **`/plan` — fires** (ALWAYS).

## Output placement

The DESIGN output revises `docs/designs/current/DESIGN-niwa-default-worktree.md`
in place rather than creating `DESIGN-worktree-delegation-durability.md`;
Decisions 4 and 6 live there and the correction has to land where a reader will
find it. The PLAN lands at `docs/plans/PLAN-worktree-delegation-durability.md`
so it is not confused with the original feature's implementation.
