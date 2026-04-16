# Exploration Findings: workspace-visibility-overlay

## Core Question

When a niwa workspace config repo is made public (after vault integration removes secrets), the workspace.toml still exposes private repo names, group names, source orgs, content subdirectory mappings, and operational config. We need a convention-based mechanism — a `dot-niwa-private` companion repo — that extends a public workspace config with private repo configuration, and is only fetched when the user has access to it.

---

## Round 1

### Key Insights

1. **The attack surface is broader and less obvious than secrets** (from lead-privacy-audit): Not just secrets (handled by vault), but TOML section keys (repo names), group names, source org identifiers, `scope = "strategic"` classifications, subdirectory mappings (revealing internal code structure), and channel access control IDs all reveal private information if a workspace.toml is public. The split must happen at the *section level*, not the field level — you cannot partially expose a `[repos.vision]` entry, because the key itself is the leak.

2. **GlobalOverride is architecturally incompatible with private extension** (from lead-global-override-layer): GlobalOverride is override-only by explicit design. It lacks `Sources`, `Groups`, and `Content` fields entirely — rejected in design because they have no meaning at the user-override layer. A private extension must ADD new sources, groups, repos, and content entries (not just override hooks/env/settings), requiring a new type `PrivateWorkspaceExtension` with union merge semantics rather than override semantics.

3. **GitHub intentionally returns 404 for all inaccessible repos** (from lead-graceful-degradation): GitHub returns 404 whether a repo "doesn't exist" or "exists but you can't access it" — a deliberate enumeration-prevention policy. niwa cannot distinguish these cases via HTTP status. Graceful degradation must use "local cache as proxy": if the companion was never successfully cloned before, a clone failure is treated as silent skip; if it was successfully cloned before, sync failure is an error (the user has access but something went wrong).

4. **`-private` suffix convention is ecosystem-standard** (from lead-tool-conventions): Git's `includeIf` silent-skip, GitHub's own `.github-private` pattern, and dotfiles managers all converge on pure naming convention + silent fallback. The proposed `owner/dot-niwa-private` naming aligns with the most recognized convention in the GitHub ecosystem. A key design choice: pure convention (public config is completely unaware of companion, strongest privacy) vs explicit field (auditable, portable, opt-in only).

5. **Three hard architectural boundaries exist in the current merge model** (from lead-edge-cases): The current model explicitly prohibits (a) merging source orgs across config layers, (b) selectively hiding auto-discovered repos, and (c) overriding content file sources at non-workspace layers. All three need addressing in the design, though (b) can be sidestepped in v1 by requiring explicit repo lists (not auto-discovery) for orgs shared between public and private configs.

6. **Source org collision requires explicit repo lists** (from lead-edge-cases): If the public config and private extension both reference the same GitHub org (e.g., `org = "tsukumogami"`), the apply pipeline's duplicate repo detection errors. To share an org, the private config must use an explicit `repos = [...]` list rather than auto-discovery. This is a real constraint for the common case where both public repos (in `tsukumogami/tsuku`) and private repos (in `tsukumogami/vision`) come from the same org.

7. **Registry already stores Source URL** (from lead-schema-changes): The global registry (`~/.config/niwa/config.toml`) already stores the source repo URL (`RegistryEntry.Source`) when a workspace is initialized with `niwa init --from org/repo`. Pure convention discovery is technically feasible without workspace.toml changes. However, this requires registry access at apply time and breaks for workspaces deployed without a registry (e.g., cloned manually).

### Tensions

**Convention vs portability**: Pure convention (zero workspace.toml changes, derive companion from registry Source URL) provides the strongest privacy (public config never mentions private companion) but breaks when the workspace was initialized without registry, lacks an opt-out path, and surprises teams that don't want a companion. An explicit `private_extension = "org/repo"` field in `[workspace]` is more portable and auditable but requires the public config to acknowledge the companion — which doesn't reveal what the companion contains, only that one exists.

**Graceful degradation semantics conflict**: The vault integration (PR #52) treats sync failure as fatal — consistent, authoritative. The private extension needs the opposite for first-time access: silent skip. This asymmetry (silent-skip on first clone, fatal on subsequent sync failures) is correct behavior but creates an asymmetry in the apply pipeline's error handling model.

**Auto-discovery and privacy incompatibility**: The most common niwa configuration (`[[sources]] org = "tsukumogami"` with `[groups.public] visibility = "public"`) auto-discovers ALL repos in the org, including private ones, then excludes unmatched repos with a warning. The warning itself reveals private repo names ("repo X matched no group"). For true privacy, teams sharing an org between public and private repos must use explicit repo lists in the public config (or separate orgs entirely). This is a significant constraint on niwa's zero-config auto-discovery value proposition.

**Content is intentionally not overrideable**: The current design explicitly excludes `Content` from both GlobalOverride and per-repo overrides — content is workspace-scoped and set in workspace.toml only. A private extension that adds new repos can provide content for those repos, but it cannot add private annotations to an existing public repo's CLAUDE.local.md. This is a known gap; the workaround (private CLAUDE.md files imported separately) requires manual setup.

### Gaps

- No data on how common the "shared org" pattern is vs "separate org per visibility" in real teams. The answer matters for whether org-collision handling is a day-one problem or an edge case.
- Content override/extension for existing public repos (Case F from lead-edge-cases) has no solution path yet — this would need its own design.
- The `CLAUDE.private.md` pattern (a separate injected private context file, similar to `CLAUDE.global.md`) is a natural extension of the existing workspace_context.go pattern but wasn't fully explored.

### Decisions

- **Ruling out GitHub variables**: User explicitly noted GitHub variables for config placement is not recommended. Eliminated.
- **All-or-nothing private access accepted**: Selective per-repo access within the private extension (some but not all private repos) is out of scope for v1. Users either have access to the full private companion or none of it.
- **Secrets out of scope**: PR #52 covers vault. This design only addresses structural privacy (repo names, configurations, group names).
- **Auto-discovery warning leaks**: Accepted tradeoff in v1 — teams that share an org between public and private repos must use explicit repo lists in their public config.

### User Focus

User's framing strongly favors convention over configuration ("ideally through convention instead of configuration"). They proposed `owner/dot-niwa` + `owner/dot-niwa-private` pairs as the model. Hybrid approach (some config in companion repos, some in individual private repos) is on the table but would benefit from requirements refinement.

---

## Accumulated Understanding

A public niwa workspace config exposes private information through five surfaces: (1) `[repos.*]` TOML section keys, which are repo names; (2) `[repos.*.url]`, which are full clone URLs; (3) `[repos.*.scope]`, which reveals internal priority classification; (4) `[groups.*]` definitions that imply private categories exist; and (5) `[claude.content.repos.*]` entries including subdirectory mappings that reveal internal code structure.

The solution is a convention-based private workspace extension — a companion repo (named `<workspace-config-repo>-private` by convention) that extends the public config with private sources, groups, repos, and content. It is only fetched when the user has access. The extension uses a new type (`PrivateWorkspaceExtension`) that carries additive fields (Sources, Groups, Repos, Content) plus override fields (Claude, Env) — structurally similar to WorkspaceConfig but with union merge semantics.

Key design decisions still to be made:
1. **Discovery mechanism**: Pure convention (registry Source URL, zero schema change) vs explicit field (`private_extension = "org/repo"` in workspace.toml). Explicit field is more portable; pure convention is stronger privacy.
2. **Graceful degradation model**: "Local cache as proxy" — silent skip on first-time clone failure (user likely doesn't have access), error on subsequent sync failures (user has access but something went wrong).
3. **Org sharing**: Shared-org workspaces (same GitHub org for public and private repos) require explicit repo lists in the public config. Auto-discovery in a shared org leaks private repo names via warning output.
4. **Content for private-only repos**: Private extension can provide CLAUDE.md content for repos it adds. Private extension cannot override content for repos already defined in the public config (v1 limitation; the `CLAUDE.private.md` injection pattern is a natural extension path).

The design maps well to a PRD: requirements are partially known but user stories (team lead, new team member, individual contributor without full access) need articulation. "What to build" is the open question — the "how" has multiple viable paths that need requirements to choose between.

## Decision: Crystallize
