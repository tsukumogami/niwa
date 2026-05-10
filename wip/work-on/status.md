# work-on status: niwa-mesh-reliability

Started: 2026-05-09
Completed: 2026-05-10
Branch: docs/niwa-mesh-reliability (override — single-pr mode on existing branch with PR #115)
Source: docs/plans/PLAN-niwa-mesh-reliability.md
Outcome: COMPLETE — all 13 implementation issues delivered.

## Phases

- [x] Phase 0: Branch override + status tracking setup
- [x] Phase 1: Multi-agent UX research (full surface — CLI + MCP + setup flow)
- [x] Phase 2: PLAN doc expansion with UX findings (issues 9-13)
- [x] Phase 3: Per-issue implementation (Issues 1-13)
- [x] Phase 4: Final test sweep + PR finalization
- [ ] Phase 5: User review + merge (handed off to user)
- [ ] Phase 6: Plan completion cascade (DESIGN → current after merge)

## Final PR state

- Title: `feat(mesh): mesh reliability cluster — design + plan + implementation`
- Body: rewritten to reflect 13 implementation issues with their commits and source-issue closures.
- Last commit: `468cb6d test(mesh): flip per-repo SKILL.md assertion to match issue #97`
- All commits on branch (newest first):
  - 468cb6d test(mesh): flip per-repo SKILL.md assertion to match issue #97
  - f02dcf3 docs(mesh): rewrite niwa-mesh skill and sessions guide (Issue 8)
  - 881c8d9 feat(cli): add --json output and DAEMON column to niwa session list (Issue 13)
  - be1b821 feat(cli): add niwa task redelegate as CLI mirror of niwa_redelegate (Issue 12)
  - a5e3095 feat(cli): render structured MCP error codes with recovery hints (Issue 11)
  - 038c3fb fix(cli): silence cobra duplicate-error printing and align stdout/stderr (Issue 10)
  - 5b52af9 feat(mesh): add niwa_redelegate primitive (Issue 7)
  - 06d4801 feat(mesh): add required_skills queue-time gate (Issue 6)
  - 7b819c8 feat(mesh): transition taskstore-lost tasks to abandoned (Issue 5)
  - 36b97a1 feat(mesh): inherit workspace Claude config in spawned workers (Issue 4)
  - f2da852 feat(mcp): add structured error wire format with body (Issue 9)
  - 33697ae feat(mesh): expose daemon liveness on niwa_list_sessions (Issue 3)
  - abacbae feat(daemon): return typed spawn-timeout error from EnsureDaemonRunning (Issue 2)
  - 7f0b262 fix(mesh): route coordinator-targeted role lookups to main instance (Issue 1)

## Test status (last green baseline)

- `go vet ./...` — clean
- `go test ./...` — green (all packages cached or fresh)
- `make test-functional-critical` — green except for the known provider-shadow scenario
  flake recorded in `decisions/D3_provider_shadow_flake.md` (environmental, infisical
  CLI session expired locally; CI runs on a fresh environment).

## Outstanding follow-on work (not landed in this PR)

The Decision D1 record pruned 51 raw UX research findings down to 5 in-scope issues
(Issues 9-13). The remaining ~38 findings are valid follow-on work but were
intentionally deferred because they did not change the user-visible contract of the
mesh-reliability cluster. They should be filed as separate issues post-merge —
the work-on autonomous run was scoped to mesh reliability specifically, not to
the entire CLI/MCP UX surface. See `wip/work-on/decisions/D1_ux_scope_pruning.md`
for the categorized backlog.

Issue #116 (deferred fleet-observability scope) is referenced in the design doc
and remains the right home for the richer daemon-health surface (last_claim_at,
last_progress_at, watcher_count) once a heartbeat infrastructure PRD lands.

## Notes for the user

- All wip/ artifacts are kept committed on the branch for review. Per CLAUDE.md
  convention, they should be cleaned before merge — the user owns that step
  since the squash-merge flow keeps wip/ out of main history regardless.
- `wip/work-on/decisions/` contains the three decision records (D1-D3) with full
  rationale.
- `wip/work-on/ux/` contains the five UX research streams that informed Issues 9-13
  selection.
