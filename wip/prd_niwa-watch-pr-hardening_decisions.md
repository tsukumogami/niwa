# /prd Decisions: niwa-watch-pr-hardening (--auto)

| id | artifact | tier | status | question |
|----|----------|------|--------|----------|
| D1 | PRD §Decisions | 2 | confirmed | Resume live-idle session vs always-fresh -> resume (settled with dispatcher) |
| D2 | PRD §Decisions | 2 | confirmed | Coalesce vs queue -> coalesce (level-triggered) |
| D3 | PRD §Decisions | 2 | confirmed | Cap counts live records across runs, not per-pass |
| D4 | PRD §Decisions | 2 | confirmed | Trigger-semantics declaration in state contract |
| D5 | PRD §Decisions | 2 | assumed | Freshness checks = open/still-requesting/not-force-pushed (DESIGN owns hook point) |
| D6 | PRD §Decisions | 3 | confirmed | Resume mechanism, state format, cap default, idle-detection -> deferred to DESIGN |

All requirements-altitude; mechanism deferred to DESIGN per parent chain. No
Tier-4 (irreversible) decisions surfaced. --auto: followed recommended defaults
grounded in the Accepted BRIEF and read code.
