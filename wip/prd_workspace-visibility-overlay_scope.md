# /prd Scope: workspace-visibility-overlay

## Problem Statement

When a niwa workspace config repo is made public (enabled by vault integration in PR #52), the workspace.toml still exposes private information through five surfaces: `[repos.*]` TOML section keys (repo names), `[[sources]]` org identifiers, `[groups.*]` names that imply private categories exist, `[claude.content.repos.*]` entries including subdirectory mappings that reveal internal code structure, and `[channels.*.access]` sections containing user IDs. Teams that publish their workspace config to enable open contribution or config-as-documentation currently have no way to keep private repo references out of the public config without maintaining a completely separate private workspace config that cannot benefit from the public base.

A convention-based private workspace extension — a companion repo named `<config-repo>-private` at the same GitHub org — would let teams publish their public workspace config while keeping private repo references, group definitions, and operational config in a separately access-controlled companion that niwa fetches automatically when the user has permission.

## Initial Scope

### In Scope

- Convention for naming and discovering the private workspace extension (`<config-repo>-private` at same org)
- A new `PrivateWorkspaceExtension` config type carrying additive fields: `[[sources]]`, `[groups.*]`, `[repos.*]`, `[claude.content.*]`, plus override fields (`[claude.hooks]`, `[claude.settings]`, `[env]`)
- Merge semantics between public workspace config and private extension: sources/groups/repos/content union (additive, not replacing); hooks append; settings per-key; env files append, vars per-key
- Graceful degradation: silent skip when private companion is inaccessible (first-time clone failure); error when sync fails for a previously-cloned companion (user has access but something went wrong)
- Apply pipeline integration: private extension sync and merge after workspace config load, before GlobalOverride
- CLI commands to register and unregister a private extension (`niwa config set private <repo>`)
- `--skip-private` flag for `niwa init` for CI/CD environments
- `CLAUDE.private.md` injection for private workspace-level AI context (parallel to `CLAUDE.global.md`)
- User stories for: team lead sharing public workspace config, new contributor bootstrapping with partial access, CI/CD environment (no private access), individual developer with full access

### Out of Scope

- Secrets and vault integration (covered by PR #52)
- Per-developer personal config (covered by existing GlobalOverride / `niwa config set global`)
- Selective per-repo access within the private extension — all-or-nothing access to the companion repo is the v1 model
- Content override for repos already defined in the public config (private extension can add content for private-only repos, not override content for public repos)
- GitHub variables as a config placement mechanism
- Auto-hiding of repos discovered via shared source orgs (teams sharing an org between public and private repos must use explicit repo lists in the public config)

## Research Leads

1. **What are the user stories that drive acceptance criteria?** — The exploration identified three actor types (team lead, contributor with full access, contributor with partial/no access, CI/CD) but didn't write out their specific goals, tasks, and failure modes. These stories determine which of the three discovery options (pure convention, explicit field, opt-out) to choose.

2. **How should discovery mechanism be decided: pure convention vs explicit field?** — Both are technically feasible. Pure convention (zero workspace.toml change) provides stronger privacy (public config is unaware of companion) but requires registry access at apply time and breaks for non-registry workspaces. Explicit field (`private_extension = "org/repo"` in `[workspace]`) is portable and auditable but has the public config acknowledge the companion's existence. User stories and team workflow norms should drive this choice.

3. **What are the migration path requirements for existing private workspace configs?** — Teams currently using private configs with niwa have both secrets (vault handles) and structural private info (this feature handles) in the same config. What does a migration look like? Can teams migrate incrementally or must they restructure all at once?

4. **What are the security requirements for the private extension registration and storage?** — The GlobalOverride design established `0o600` file permissions for config storing sensitive data. What are the analogous requirements for private extension registration? Are there threat models specific to team-shared (vs individual) private configs?

5. **How does collision handling work for shared source orgs?** — The exploration found that if both public and private configs reference the same GitHub org, duplicate repo detection errors. What are the requirements for teams that have public and private repos in the same org? Is explicit repo listing in the public config sufficient, or do teams need a more explicit exclusion mechanism?

## Coverage Notes

The exploration was one round and is sufficient to write a PRD, but the following gaps need explicit PRD treatment:

- **User stories with acceptance criteria**: No user stories were written. The PRD must define these concretely (team lead, new member, partial-access contributor, CI/CD) before requirements can be written.
- **Discovery mechanism decision**: The exploration found three viable options (A: pure convention from registry, B: explicit field in workspace.toml, C: opt-out) but could not choose without requirements. The PRD's user stories and scenario walkthroughs should enable the choice.
- **Naming convention generalization**: The exploration assumed `<config-repo>-private` naming. What happens when the public config is not named `dot-niwa`? The PRD should state whether the convention is `<any-name>-private` or requires a fixed `dot-niwa` naming scheme.
- **Content for private-only repos vs content override for public repos**: The exploration deferred content override to v2. The PRD should confirm this scope decision explicitly and define what "content for private-only repos" means as an acceptance criterion.
- **`CLAUDE.private.md` injection requirements**: Parallel to `CLAUDE.global.md`, a private workspace-level AI context file is a natural feature. The PRD should include or explicitly exclude this.

## Decisions from Exploration

- **GitHub variables ruled out**: Using GitHub's native variables/secrets for config placement is not the approach. The companion repo model is the design direction.
- **All-or-nothing access accepted**: Selective per-repo access within the private extension (some but not all private repos accessible to partial-access users) is out of scope for v1. If you can access the private companion, you get all private config; if not, you get none.
- **Secrets out of scope**: This feature only addresses structural privacy (repo names, configurations, group names). Secrets are handled by vault (PR #52).
- **Auto-discovery warning leaks are a known v1 tradeoff**: Teams sharing a GitHub org between public and private repos must use explicit repo lists in their public config to prevent private repo names from appearing in "excluded with warning" output.
- **Content override for existing public repos deferred**: Private extension cannot override CLAUDE.md content for repos already defined in the public workspace config. This is a v2 concern.
- **PRD before design**: Requirements need to be articulated first. Three implementation paths exist (discovery mechanism, graceful degradation edge cases, merge collision handling); choosing between them requires requirements.
