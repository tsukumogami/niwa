```yaml
topic: vault-doctor
visibility: Public
chain_started: 2026-07-11T21:50:28Z
last_updated: 2026-07-11T21:50:28Z
phase_pointer: phase-2
parent_orchestration:
  active_child: brief
  invoked_at: 2026-07-11T21:50:28Z
exit: UNSET
exit_artifacts: []
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
gate_verdicts:
  brief: "fires (R4: no upstream BRIEF)"
  prd: "fires (R5: no Accepted PRD)"
  design: "fires (R7 shape-dependent: P1 fires (surface choice: `niwa vault check` subcommand vs `status --check-*` flag; plus which source layers to check); P2 does-not-fire (reuses parseProviderAuthBody/LoadProviderAuth/VaultRegistry, subcommand in existing binary); P3 does-not-fire (mostly reuse, Medium)); LIGHT single-decision design"
  plan: "fires (ALWAYS)"
scope_note: "Crystallized from /explore team-vault-bootstrap. Doctor = highest-confidence slice. Config-scaffold (niwa vault init) is named follow-on; provider-side creation is OUT (delegated to provider admin CLI). Single-pr terminal PLAN."
```
