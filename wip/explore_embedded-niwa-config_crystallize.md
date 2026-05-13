# Crystallize: embedded-niwa-config

## Verdict

**Artifact type: PRD.** Route to a new PRD document at
`docs/prds/PRD-config-source-discovery.md`. Suggested title: "Workspace
Config Source Discovery" (closes the R5 gap in
`PRD-workspace-config-sources.md`).

## Scoring Summary

| Type | Signals | Anti-Signals | Score | Notes |
|---|---|---|---|---|
| **PRD** | 4 | 0 | **4** | Single coherent feature, decisions to capture, user stories missing, "what to build and why" is the core question |
| Design Doc | 4 | 1 | 3 (demoted) | What-to-build still partly unclear; some requirements not yet specified |
| Plan | 2 | 2 | 0 (demoted) | Approach is contested for probe mechanism; open Decisions remain |
| No Artifact | 0 | 1+ | demoted | Others need documentation to build from |
| Decision Record | 1 | 1 | n/a | Multiple interrelated decisions, not a single one |
| Spike Report | 0 | 1 | n/a | Feasibility is settled; gap is requirements, not feasibility |
| VISION | 0 | 1 | n/a | Project exists; tactical scope |
| Roadmap | 0 | 1 | n/a | Single feature, no multi-feature sequencing needed |
| Rejection Record | 0 | 1 | n/a | Strong positive demand, not a rejection |
| Competitive Analysis | 0 | 1 | n/a | Public repo; internal technical decisions, not external landscape |

PRD wins by both raw score and the absence of anti-signals.

## Why PRD (and not the alternatives)

- **Not a Design Doc.** Several R5-adjacent requirements (the migration
  tooling, the rank-3 `niwa.toml` keep/drop question) are still open at
  the requirements level — not just the architecture level. A design doc
  would assume the requirements are settled. The PRD's job is to settle
  them.
- **Not a Plan.** An upstream PRD does exist
  (`PRD-workspace-config-sources.md`), but it's marked Done with an
  unbuilt requirement (R5). Sequencing work without first reconciling
  the "Done with gap" situation would propagate the inconsistency.
- **Not No Artifact.** The user asked for a PRD and the work touches a
  user-facing UX surface that needs an acceptance contract.

## Disambiguation Notes

- The existing `PRD-workspace-config-sources.md` is the umbrella spec.
  The new PRD references it as upstream and scopes itself to closing
  the gap. It is not an amendment because the new PRD adds policy
  decisions (consolidation tooling shape, probe mechanism, rank-3
  drop/keep) that don't fit cleanly as line-item amendments.
- Phase 5 (Produce) hands off to `/shirabe:prd` with the findings,
  scope, and decisions files as upstream context. In `--auto` mode the
  user expects the PRD as the deliverable; the handoff produces the
  document directly rather than blocking on interactive prompts.

## Open Items the PRD Must Resolve

These are the Decisions the PRD's "Decisions and Trade-offs" section will
need to land:

1. **Discovery probe mechanism** — two-call (Contents API + tarball) vs
   single-call (download tarball, scan in-memory for markers).
   Recommended starting position: **single-call**, because the existing
   PRD already accepts whole-repo bandwidth as a known limitation for
   the first fetch, and avoiding a second round-trip simplifies the
   error surface.
2. **Migration tooling shape** — none (passive coexistence) vs
   `niwa migrate-source <name>` command. Recommended starting position:
   **ship the command, keep coexistence by default**. The user's
   "consolidate everything onto the new pattern" preference is best
   served by making the migration painless, not by forcing it.
3. **Rank-3 `niwa.toml` discovery — keep or drop?** The existing PRD's
   R5+R8 requires it; dropping simplifies the discovery error matrix.
   Recommended starting position: **drop rank-3 in v1.x**, since the
   user's stated goal (single-repo workspaces and brain-repo
   consolidation) is served by rank-1; rank-3 was never observed in
   the wild. Promote to its own Decision in the PRD with clear
   trade-offs documented.

## Next Phase

Proceed to Phase 5 (Produce → PRD handoff).
