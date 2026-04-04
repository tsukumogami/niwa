# Lead: Demand Validation

## Findings

### Q1: Is demand real?

**Finding:** Demand is documented in the explore scope document (wip/explore_workspace-config-layering_scope.md), authored by Daniel Gazineu on 2026-04-04 (today). The problem statement is explicit: "This makes workspaces hard to share across teams because personal preferences and user-specific secrets are mixed with shared config."

The scope identifies a concrete user need: "The user wants a layered model: a shared team workspace (existing) plus a personal config layer (new)." However, **the scope document does not cite a GitHub issue, a distinct external issue reporter, or a maintainer acknowledgment from another party**. The document appears to be self-authored exploration rather than response to an external request.

The personal config schema research (wip/research/explore_workspace-config-layering_r1_lead-personal-config-schema.md) notes that the workspace identification mechanism already supports personal config through workspace.name, suggesting the infrastructure readiness is present but the demand assertion is not independently corroborated.

**Confidence: Low**. The problem statement exists in durable artifacts and is coherent, but sourced from internal exploration without external validation (no issue number, no distinct reporter, no linked discussions).

### Q2: What do people do today instead?

**Finding:** The scope document identifies today's workaround: "Today, niwa workspaces are backed by a GitHub repo containing all config: hooks, env vars, plugins, secrets, and preferences." This forces users to either (a) store personal secrets in the shared workspace repo, or (b) maintain a separate personal workspace entirely.

The PRD for config distribution (docs/prds/PRD-config-distribution.md) documents a predecessor workaround: a "700-line bash installer that copies hook scripts, generates settings.local.json, and merges .env files." This suggests teams currently manage distributed config via imperative scripts rather than layered declarations.

The README mentions "shared workspace configs via a GitHub repo" with `niwa init my-team --from my-org/workspace-config`, but does not document any guidance on how to layer personal overrides on top — the current UX assumes a single shared workspace config per team.

**Confidence: Medium**. Workarounds are documented (imperative bash scripts, mixed secrets in shared repos) but no evidence of users actively complaining about the current state or asking for alternatives.

### Q3: Who specifically asked?

**Finding:** No GitHub issue, no PR discussion, no linked external request found. The explore scope document (wip/explore_workspace-config-layering_scope.md) was created by Daniel Gazineu on 2026-04-04. The lead says "The user wants a layered model" but does not cite a specific user, issue number, or Slack/email thread.

The prior decision history shows Decision 6 (host x workspace overrides, commit fecf1bd) was authored by Dan Gazineu (alternate spelling) on 2026-03-25. This decision was **later removed** (commit 00a16b0, 2026-03-27) with the note: "Per-host overrides and channel configuration are out of scope for this design doc." This suggests the demand for layering was considered and explicitly de-scoped.

The current scope document makes no reference to the removed Decision 6 or explains why host-level layering was removed but personal config layering is now being explored.

**Confidence: Absent**. No distinct issue reporter, no PR author, no external citation. The feature originates from internal exploration without documented external demand.

### Q4: What behavior change counts as success?

**Finding:** The explore scope document defines success criteria as:
1. Personal config repo registration in `~/.config/niwa/config.toml` (one-time machine registration)
2. Schema for personal config (global section + per-workspace override sections)
3. Merge/overlay semantics (personal wins on conflict)
4. Personal config sync at `niwa apply` time
5. Opt-out flag: `--no-personal-config` at `niwa init`

The merge semantics research (wip/research/explore_workspace-config-layering_r1_lead-merge-semantics.md) provides additional specificity: personal config should follow workspace→repo override semantics (lists append, maps merge per-key, plugins replace entirely).

These criteria are explicit but authored in the same exploration thread, not independently stated by users or maintainers.

**Confidence: Medium**. Acceptance criteria are clearly written in durable artifacts but derived from internal analysis, not external statement of requirements.

### Q5: Is it already built?

**Finding:** Partial infrastructure exists:

1. **Workspace identification by name**: The workspace.name field in workspace.toml is validated (alphanumeric + dots/hyphens/underscores) and stored in instance state. Personal config schema research confirms this is the correct identifier for per-workspace overrides.

2. **Merge semantics**: The codebase already implements MergeOverrides() and MergeInstanceOverrides() functions that define per-field merge rules (hooks append, env vars per-key win, plugins replace, etc.). Personal config could reuse this same logic.

3. **Config discovery**: The global registry at ~/.config/niwa/config.toml already exists for workspace registration (per DESIGN-workspace-config.md Decision 3). Personal config registration could extend this structure.

4. **No implemented personal config layer**: Searching the codebase for "personal.toml" or personal config loading returns no results. The feature is not implemented.

5. **Prior host-level layering was removed**: Decision 6 (host x workspace overrides) was explicitly removed from the design (commit 00a16b0, 2026-03-27). The removal note says "out of scope for this design doc" but doesn't explain whether the concept was rejected or deferred.

**Confidence: High**. The infrastructure exists; the feature itself does not. The prior removal of similar functionality (host-level overrides) is positive evidence that this concept has been considered and deliberately excluded or deferred.

### Q6: Is it already planned?

**Finding:** Planning artifacts exist but show the feature is in early exploration:

1. **Explore scope document**: wip/explore_workspace-config-layering_scope.md created 2026-04-04 outlines 6 research leads, indicating this is an active exploration phase.

2. **Research leads completed**: Three research documents have been authored:
   - explore_workspace-config-layering_r1_lead-personal-config-schema.md (current workspace structure analysis)
   - explore_workspace-config-layering_r1_lead-merge-semantics.md (merge rules analysis)
   - explore_workspace-config-layering_r1_lead-adversarial-demand.md (this document, demand validation)

3. **No roadmap entry**: No reference to this work in README.md roadmap, no project board entry found, no linked design doc status.

4. **Open questions remain**: The merge semantics research document lists 6 "Open Questions" (plugin merging strategy, personal CLAUDE.md content layering, global vs per-workspace scope, ability to override Sources/Groups, etc.) indicating design work is incomplete.

5. **Schema not yet designed**: No personal.toml schema has been drafted or accepted. The work is in the research/feasibility phase.

**Confidence: High**. The feature is actively being explored but not yet planned for implementation. It's in research phase, not in a commit plan or release roadmap.

## Calibration

**Demand not validated.**

Multiple sources of evidence converge on the same conclusion:
- No distinct external issue reporter or GitHub issue linking demand
- No maintainer comment accepting the feature request or assigning it priority
- No user discussion in linked threads or comments
- The feature originates from internal exploration (author: Daniel Gazineu)
- A similar feature (host-level overrides) was explicitly removed from design scope on 2026-03-27
- The current explore scope provides no link to the prior removal or explanation of why the concept is being revisited

The problem statement (personal preferences mixing with shared config) is coherent and the infrastructure (merge semantics, workspace.name identification) partially exists. However, there is no positive evidence that users have asked for this feature or that the maintainers intend to prioritize it. The evidence could equally support either conclusion:
- **Demand not validated**: No external signal; feature originates from internal forward-thinking design
- **Demand validated as absent**: Prior decision to remove host-level overrides could indicate the team decided this pattern isn't needed

The key distinguishing factor is the absence of **rejection reasoning** in the removal commit (00a16b0). The removal note says "out of scope for this design doc" but doesn't say "we decided this isn't a good idea" or "users don't want this." This ambiguity means the feature is genuinely uncertain rather than rejected.

## Summary

Workspace-config-layering is an internally-driven exploration addressing a coherent problem (mixing personal secrets with shared team config) with partial infrastructure in place. No GitHub issues, distinct reporters, or maintainer acceptance signal validates demand. A prior similar feature (host overrides) was removed from design scope without documented rejection reasoning, leaving the current exploration in genuinely uncertain territory. The research is in early feasibility phase; design and implementation decisions remain open.

