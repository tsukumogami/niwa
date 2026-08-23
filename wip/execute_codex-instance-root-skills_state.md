---
topic: codex-instance-root-skills
last_updated: 2026-08-23
phase_pointer: spawn_and_await
execution_mode: auto
home_pr: 271
settled_branch: docs/codex-root-skills
plan_doc: docs/plans/PLAN-codex-instance-root-skills.md
koto_session: execute-codex-instance-root-skills
exit:
exit_artifacts: []
child_snapshots: []
---

# /execute state: codex-instance-root-skills

## Phase 0 — complete

Slug `codex-instance-root-skills` re-validated against `^[a-z0-9-]+$`. The PLAN's
`execution_mode` re-validated as `single-pr`, so no coordinated-mode preflight was
run — that declaration carries no `mode:single-pr` record because every tool the
single-pr path needs is already `always`. No stale `parent_orchestration:` sentinel
was present at session start.

The cross-skill child template asserted clean
(`skills/execute/scripts/assert-child-template.sh`, exit 0).

## Phase 1 — drive

**Home PR adopted, not created.** This run entered on the `/scope` branch
`docs/codex-root-skills`, which carries the whole scoping chain. Rather than
opening a second PR on an `impl/` branch and orphaning those commits, the branch
was pushed and draft PR tsukumogami/niwa#271 opened on it, then adopted through the
`orchestrator_setup` override path. The settled branch is recorded in koto context
and read back verified, so `spawn_and_await` routes every child to it.

**Worktree discipline: impact `none`.** `git rev-list --count fc50683..origin/main`
returns 0 — main has not advanced since this branch's base, so there is nothing to
rebase onto and no upstream change that could touch the PLAN's foundation.
Classification written to `wip/work-on_codex-instance-root-skills_impact.json`.

**Children materialized.** Seven, one per PLAN issue, with the PLAN's dependency
edges: issues 1, 2 and 3 depend on nothing; 4 needs 1 and 3; 5 needs 3; 6 needs 2,
4 and 5; 7 needs 6.

They run **sequentially**, not in parallel. Every commit lands on one shared branch
in one worktree, so concurrent children would race on the same tree — which is why
the PLAN's own Implementation Sequence calls parallelization theoretical only.

### Issue progress

- [ ] 1 — refactor(plugin): unhook internal/plugin from internal/workspace
- [ ] 2 — feat(plugin): add the Claude plugin manifest to the embedded tree
- [ ] 3 — feat(agentplan): add root skills and niwa-plugin leaf vocabulary
- [ ] 4 — feat(workspace): register the root skills and niwa-plugin deliveries
- [ ] 5 — refactor(cli): gate the dispatch warning on the payload-scope predicate
- [ ] 6 — feat(agentplan): flip rows 18 and 19 for codex and bind both capabilities
- [ ] 7 — test(functional): add root skills placement and discovery scenarios

## Drift observed, for the coordinator handoff

**shirabe's plan skill and its validator disagree about single-pr Dependency
Graphs.** The `/plan` skill's single-pr spec requires the `## Dependency Graph`
section populated with internal IDs; validator check FC14 raises a notice saying
a `single-pr` plan must not populate it and should either switch to `multi-pr`
or drop the diagram body. Both cannot be satisfied.

The child followed the skill spec and kept the mermaid diagram, accepting the
notice. This run removed the diagram and folded its full edge set into the
Implementation Sequence prose, because `single-pr` is not negotiable here (the
PRD requires both rows to flip in one change, and `/execute` accepts only
`single-pr` or `coordinated`), the validator then reports 0 errors and 0
notices, no required-section check fires on the removal, and one edge that had
existed only in the diagram is now stated in text.

Neither choice is wrong; the inconsistency is upstream in shirabe and is worth
an issue there rather than a local workaround repeated by every plan author.
