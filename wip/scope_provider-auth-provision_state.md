```yaml
topic: provider-auth-provision
visibility: Public
chain_started: 2026-07-10T03:12:17Z
last_updated: 2026-07-10T03:12:17Z
phase_pointer: phase-2
parent_orchestration:
  active_child: brief
  invoked_at: 2026-07-10T03:12:17Z
exit: UNSET
exit_artifacts: []
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
gate_verdicts:
  brief: "fires (R4 EITHER-signal: no upstream BRIEF at canonical path)"
  prd: "fires (R5 Mandatory-with-auto-skip: no Accepted PRD at canonical path)"
  design: "fires (R7 shape-dependent: P1 fires (storage-target/identity-reuse/TTL alternatives open); P2 does-not-fire (subcommand in existing binary, existing vault abstraction); P3 fires (reverses 'only reads' non-goal, adds identity-write path to secrets API))"
  plan: "fires (ALWAYS)"
scope_note: "Anchored in public/niwa for the native command layer only. The onboard-codespar skill (private/tools) is a downstream follow-on effort; /scope v1 is public-repo-only and produces one terminal PLAN."
```
