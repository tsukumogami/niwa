---
topic: codex-instance-root-skills
last_updated: 2026-08-23
phase_pointer: phase-2-chain-orchestration
chain_started: 2026-08-23T17:00:00Z
visibility: Public
execution_mode: auto
max_rounds: 5
consumed_handoff: wip/scope_codex-instance-root-skills_handoff.md
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_ran: []
parent_orchestration:
  active_child: brief
  invoked_at: 2026-08-23T17:35:00Z
chain_skipped: []
exit:
exit_artifacts: []
---

# /scope state: codex-instance-root-skills

## Phase 0 — complete

Slug validated against `^[a-z0-9-]+$` as provided.
`shirabe slug-prefix-detect` returned `no-prevailing-prefix`, so no
recommendation was surfaced. Visibility read from the explicit
`## Repo Visibility: Public` header in the repo's CLAUDE.md — not a default.
No `--upstream` supplied, so `consumed_upstream:` is absent.

The resume ladder's Slot 7 clause matched: `/explore` left a handoff at
`wip/scope_codex-instance-root-skills_handoff.md`, which this run consumed and
entered Phase 1 with pre-loaded.

No stale `parent_orchestration:` block was found at session start.

## Phase 1 — complete

### Child-doc discovery

All five canonical paths were globbed. None exists:

- `docs/briefs/BRIEF-codex-instance-root-skills.md` — absent
- `docs/prds/PRD-codex-instance-root-skills.md` — absent
- `docs/designs/DESIGN-codex-instance-root-skills.md` — absent
- `docs/designs/current/DESIGN-codex-instance-root-skills.md` — absent
- `docs/plans/PLAN-codex-instance-root-skills.md` — absent

No re-entry protection fires for any child; `chain_skipped:` stays empty.

### Framing-shift answer

The handoff carries a pre-supplied answer of **no signal surfaced**, evidenced
by the problem shape, audience, scope boundary and success criterion all holding
across both exploration rounds — what moved was the delivery route, not the
framing. Running in `--auto`, the confirmation resolves to the handoff's answer
and it is recorded as confirmed. The cold-start projection is suppressed: a
handoff run is not a cold start.

### R6 shape-predicate walk

All three predicates fire, so `/design` runs with its full decision roster.

- **P1 — architectural-alternatives count: fires.** The handoff names four
  alternatives left open, each with a real cost on both sides: row 18's binding
  shape (named delivery versus plan-entry tag), row 19's materialization site
  (extract-and-symlink versus copy-in-place), the dispatch warning's new gate
  (exported producer predicate versus a name-based check), and whether niwa's
  embedded tree gains a Claude plugin manifest. Accepted from the handoff with
  its stated reasons, per the Slot 7 rule.
- **P2 — new-component references: fires.** Recomputed against the worktree
  rather than accepted from the handoff, since it is a filesystem claim. The
  work introduces components that do not exist in the tree today: a sibling
  producer method on the skills path, at least one new `Delivery` constant with
  a registered type behind it (a `Materializer` for the plan-routed row and a
  `procedure` for the procedure-routed one), a new exported predicate over the
  payload layout's scope support, and an extraction step for the embedded
  plugin tree. All land inside existing packages rather than new directories,
  which is why the predicate fires modestly rather than strongly.
- **P3 — Complexity classification: fires.** The change spans four packages and
  at least eight surfaces that react mechanically, two structural scans
  constrain the implementation rather than merely reacting to it, binding routes
  are fixed per capability so the two rows bind through two different
  registries, and the two agents' row-19 deliveries have genuinely different
  lifecycles. Accepted from the handoff with its stated reasons.

### Chain proposal

Planned chain: **`/brief` → `/prd` → `/design` → `/plan`** — the whole tactical
chain, no child held back.

Running in `--auto`, the `Proceed / Adjust / Bail` confirmation resolves to
**Proceed**.
