# Phase 2 Research: User Researcher

## Lead 1: User Stories

### Findings

#### User Story 1: Team Lead Publishing Workspace Config

**Actor:** Sarah, team engineering lead at a 15-person SaaS company

**Scenario:**
Sarah's team has been managing development environments with niwa for 6 months. They have:
- 8 public repositories (marketing site, SDK, CLI, documentation)
- 5 private repositories (internal APIs, admin dashboards, database migration scripts)
- Shared workspace config in `acmecorp/dot-niwa` (private until now)
- Unified `~/.config/niwa/config.toml` registration pointing to `acmecorp/dot-niwa`

The company wants to open-source the public repos and publish the workspace config as a reference for how teams structure multi-repo development. However, Sarah cannot publish the current workspace config to GitHub because it contains the names of the private repos (`vision-api`, `admin-panel`, `db-scripts`) in `[repos.*]` keys, which reveals the team's internal architecture.

**Goal:**
Split the workspace config into public and private parts without duplicating the entire TOML structure. The public config should describe only public repos and be publishable on GitHub. The private config should live in a companion repo accessible only to team members, extending the public config with the private repos.

**Success State:**
1. Sarah publishes `acmecorp/dot-niwa` to GitHub with only the 8 public repos, no mention of private repos
2. She creates `acmecorp/dot-niwa-private` as a private GitHub repo containing the private repos and any private group definitions
3. New team members can clone and run `niwa init acmecorp` from the public config — they get only the public repos
4. Existing team members with access to the private repo automatically get all 13 repos when they run `niwa apply`
5. Sarah's laptop continues to work with both public and private repos, and she doesn't need to change her initialization command
6. CI/CD pipeline clones the public config only and runs with the 8 public repos (no private access needed)

**Failure Modes & Expected Behavior:**
- New team member (Jane) runs `niwa init acmecorp` → sees 8 public repos, no errors, no mention of private companion missing. Silent success.
- Jane runs `niwa apply` on her freshly initialized workspace → succeeds with 8 public repos. She is unaware the private extension exists.
- Intern (Bob) joins, runs `niwa init acmecorp`, but his GitHub account lacks access to the private repo → gets 8 public repos silently (same as Jane). No error messages, no noise.
- CI/CD runner (non-interactive) runs `niwa apply --skip-private` → gets 8 public repos, hook scripts do not try to clone the private companion. CI pipeline does not fail due to missing access.
- Existing team member (Marcus) with full access runs `niwa apply` in an existing instance → if private companion was cloned before, sync failure (network down, GitHub down) is a fatal error. Marcus gets a clear error message. If private companion was never cloned (first-time access), sync failure is silent (user likely doesn't have access yet; same behavior as Jane).

---

#### User Story 2: Contributor with Full Access

**Actor:** Marcus, senior engineer on the team for 2 years

**Scenario:**
Marcus has been a contributor to the team's private workspace for 2 years. His `.niwa/` directory contains the working workspace config (already has access to the private companion repo registered). The team just published the public config to GitHub and introduced the private companion mechanism.

When Marcus runs `niwa apply` in his existing workspace (initialized before the split), his workspace config is still the old all-in-one private config (not yet split). He wants to migrate to the new public+private model without losing his workspace instance.

**Goal:**
Continue working with all 13 repos without disruption. Migrate to the new public+private config structure smoothly.

**Success State:**
1. Marcus's instance continues to work (backward compatibility — if no private extension is configured but the public config has all repos, it still works)
2. Marcus updates the workspace config source to point to the new public `acmecorp/dot-niwa` repo
3. He initializes or re-registers the private companion `acmecorp/dot-niwa-private` (via `niwa config set private acmecorp/dot-niwa-private`)
4. He runs `niwa apply` → both configs are merged, resulting in the same 13 repos
5. No repos are re-cloned unnecessarily (existing clones are verified, not deleted)
6. His per-repo customizations (local branch overrides, per-repo hooks from global config) continue to work

**Failure Modes & Expected Behavior:**
- Marcus runs `niwa apply` before registering the private companion → gets 8 public repos only, no error. The instance silently has fewer repos than before.
- Marcus registers the companion, then runs `niwa apply` → repos that were already cloned (from the old all-in-one config) are verified, new repos are cloned. Apply is idempotent — running twice in a row has no effect.
- Private companion sync fails (network issue) on an instance where it was previously cloned → fatal error with clear message: "Private companion sync failed. Ensure GitHub access is enabled and network is online."
- Marcus deletes his local clone of a private repo manually, then runs `niwa apply` → repo is re-cloned (apply is convergence-based, not state-preserving)

---

#### User Story 3: Contributor with Partial/No Access

**Actor:** Jane, junior engineer joining the team

**Scenario:**
Jane is a new hire with one month on the team. She hasn't been granted access to the private GitHub repositories yet (still completing onboarding security training). She is, however, a member of the team's GitHub organization and can access the public repos.

**Goal:**
Set up her development environment using niwa, get all the public repos, and work productively without hitting errors or being exposed to the fact that private repos exist.

**Success State:**
1. Jane runs `niwa init acmecorp --from acmecorp/dot-niwa` → .niwa/ is cloned from the public config
2. Jane runs `niwa create` → all 8 public repos are cloned, CLAUDE.md files are generated
3. Jane runs `niwa apply` → no errors. The private companion clone fails silently (she has no access). Jane gets 8 public repos, no mention of a private extension or missing repos.
4. Jane can develop normally with the public repos
5. Two weeks later, after completing security training, Jane is granted access to the private repos
6. Jane runs `niwa apply` again (no new command, no re-initialization) → private companion now clones successfully, 5 new repos appear, merged with the existing 8
7. Jane runs `niwa status` → shows all 13 repos, with no indication of which are private vs public (they're just repos)

**Failure Modes & Expected Behavior:**
- Jane's instance was created before the private companion existed. First `niwa apply` after the feature ships → silent skip of private companion (no change in behavior from today)
- Jane makes a typo when registering the private companion URL → next `niwa apply` fails on private companion sync (if it was never cloned before, it's a silent skip; if a previous apply had successfully cloned it, this is a fatal error). Jane gets a clear error: "Private workspace extension clone failed: <specific GitHub error>. Check the registered URL with `niwa config unset private` and `niwa config set private <correct-url>`."
- Jane is revoked from the private repos due to team restructuring → next `niwa apply` encounters private companion sync failure. If previously cloned, fatal error. Jane contacts her manager, who runs `niwa config unset private` to remove the companion. Next apply succeeds with 8 public repos only. Jane's existing private repos remain on disk (niwa does not auto-clean); she manually removes them or leaves them (inert).

---

#### User Story 4: CI/CD Environment

**Actor:** GitHub Actions workflow (non-interactive, runs on every PR merge)

**Scenario:**
The team's CI/CD pipeline includes a step that runs `niwa apply` to set up a fresh development environment, clone repos, and install workspace-level tools. The pipeline should:
- Never require GitHub PAT or SSH access to private repos
- Fail clearly if public repos are inaccessible (public config is wrong)
- Silently skip private repos (private companion is not relevant to CI)
- Maintain the 8-public-repo workspace footprint regardless of how the private companion is configured

**Goal:**
Run CI/CD consistently with public repos only, without leaking private repo references into build logs or configuration.

**Success State:**
1. CI/CD runner clones the public workspace config repo (already part of the CI setup)
2. CI/CD workflow calls `niwa apply --skip-private`
3. Private companion is not cloned, not synced, not queried
4. 8 public repos are cloned and workspace is set up
5. Build succeeds
6. No error messages mentioning private repos or missing access
7. Build logs are safe to share publicly (no secret repo names, no GitHub access failures)

**Failure Modes & Expected Behavior:**
- CI/CD runner does NOT use `--skip-private` → private companion clone is attempted. If it fails (no PAT in CI, private access denied), behavior depends on history:
  - First-time CI run (private companion never cloned): silent skip, build proceeds with 8 public repos
  - Subsequent CI run (private companion was cloned in a previous run, but CI container is ephemeral): companion sync fails. Fatal error in apply. CI fails with message "Private workspace extension not accessible." This is bad (CI should not fail due to private access). Mitigation: CI operator MUST use `--skip-private` flag to ensure consistent behavior.
- CI/CD runner uses `--skip-private` but workspace config accidentally includes private repos in the public section → apply fails because those repos cannot be cloned (private). Error message includes the repo name, which is a secret leak. Mitigation: the split mechanism (public config, private companion) is designed to prevent this; if private repos end up in the public config, that's a configuration error that belongs in code review, not a niwa feature failure.

---

### Implications for Requirements

**From Story 1 (Team Lead):**
- Private companion discovery MUST be silent by default (no "companion not found" messages). This prevents revealing the existence of a private extension to users without access.
- The merge of public and private configs must result in a unified workspace from the user's perspective (single CLAUDE.md hierarchy, single repo list in `niwa status`). Users should not be aware of the split at runtime.
- The `--skip-private` flag is essential for CI/CD — it must cleanly disable private companion integration without errors.
- Backward compatibility: workspaces initialized before the private companion feature shipped should continue to work (silent if no private companion is configured).

**From Story 2 (Full Access):**
- Migration tooling or clear docs are needed for teams with existing all-in-one private configs. A migration path is a must-have.
- The private companion registration command (`niwa config set private`) must parallel the global config command structure for consistency.
- Instance state must track whether private companion was ever cloned (to distinguish "first access, user likely has no access" from "was cloned before, sync failure is an error").

**From Story 3 (Partial Access):**
- Graceful degradation is critical. First-time private companion clone failure must be silent; subsequent failures must error. This requires local cache semantics.
- The feature must not expose the private companion's existence or contents to users without access. The error message on first-time failure must be silent (no user-facing message), not a warning.
- No "discovered repos from private companion" messages in `niwa status` for users without access.

**From Story 4 (CI/CD):**
- The `--skip-private` flag must be available on `niwa apply` and `niwa init`.
- When `--skip-private` is used, the private companion registration must be ignored entirely (no sync attempt, no error handling needed).
- The feature must not require CI/CD operators to manage authentication to private repos. Public access is the only assumption.

---

### Open Questions

1. **Naming convention: Is `<config-repo>-private` sufficient, or should the convention be configurable?** 
   - Assumption: standard `-private` suffix works for teams. Are there teams where the companion should be named differently (e.g., `dot-niwa-team-only`, `dot-niwa-internal`)? 
   - For v1: lock it to the `-private` convention. Allow configurable naming in v2 if teams request it.

2. **Discovery mechanism: Should the public config explicitly declare the private companion, or should it be pure convention?**
   - Story 1 (Team Lead) and Story 3 (Partial Access) both suggest pure convention is better (no public config mention of private companion = stronger privacy).
   - Story 2 (Full Access) migration suggests explicit registration (`niwa config set private`) is required anyway for a newly-split workspace.
   - **Decision needed:** Is workspace.toml silence on the private companion a requirement (strongest privacy), or is explicit opt-in acceptable (more discoverable)?

3. **Shared org scenario: Should teams be able to share a GitHub org between public and private repos?**
   - The exploration found that shared-org setups require explicit repo lists in the public config (auto-discovery would leak private repo names). Is this constraint acceptable, or should the PRD require separate orgs or alternative mechanisms?
   - **Decision needed:** Document this as a known limitation ("teams sharing an org must use explicit repo lists") or redesign the discovery mechanism to handle it?

4. **Content for private-only repos: Should the private companion be able to add CLAUDE.md content for repos it introduces?**
   - Yes, based on Story 1 (private repos may need custom CLAUDE.md files). The private companion should support the `[claude.content.repos.*]` section just like the public config.
   - **Decision needed:** Should v1 also support `CLAUDE.private.md` (workspace-level private context injection), or is per-repo content sufficient?

5. **Instance migration: Should niwa provide tooling to migrate an existing all-in-one private config to the new public+private split?**
   - Story 2 (Full Access) implies this is a real need for existing teams. A migration command or clear guide is essential.
   - **Decision needed:** Is this a CLI tool (`niwa migrate-private`), documentation/manual steps, or out of scope for v1?

---

## Lead 2: Migration Path

### Findings

**Current State: All-in-One Private Workspace Config**

Teams that have adopted niwa for multi-repo management today typically have a single workspace config repo, often private or internally-scoped, that contains everything:
- All repos (public and private)
- All groups
- All content files
- All hooks and settings

Example: `acmecorp/niwa-workspace-config` (private GitHub repo)
```toml
[[sources]]
org = "acmecorp"

[groups.public]
visibility = "public"

[groups.private]
visibility = "private"

[repos.vision]
scope = "strategic"

[repos.some-oss-lib]
scope = "public-contribution"
```

Team members are registered to this repo with private GitHub access. CI/CD pipelines also have PAT access for the private repos. There are no public instances of this workspace.

**Phase 1 Motivation: Make the Config Public**

The team decides to:
- Open-source the public repos to enable community contribution
- Publish the workspace config to GitHub as a reference for how teams structure development
- Keep private repos and operational config out of the public view

**Migration Path: Two Options**

#### Option A: Hard Cut (v1 Recommended Path)

**Step 1: Create the split**
1. Sarah creates `acmecorp/dot-niwa` (public) with only the 8 public repos (sources, groups, content)
2. Sarah creates `acmecorp/dot-niwa-private` (private) with the 5 private repos (sources, groups, content) + override sections (hooks, env, settings that are team-private)
3. The private companion only includes fields that differ from or extend the public config (additive sources, groups, repos, content)

**Step 2: Existing team members re-initialize or update**

For team members with existing workspaces on `acmecorp/niwa-workspace-config` (the old all-in-one config):

**Path 2a: In-place update (minimal disruption)**
1. Sarah documents the change in the team README
2. Existing team members run: `niwa init acmecorp --from acmecorp/dot-niwa` to update their `.niwa/` to point to the new public config
3. They run: `niwa config set private acmecorp/dot-niwa-private` to register the companion
4. They run: `niwa apply` → repos are verified (not re-cloned if already present); new repos from the private companion are added
5. No workspace instances are deleted or recreated; they just converge to the new config

**Path 2b: Clean-slate re-initialization (complete reset)**
1. Existing team members back up their workspace instances (save any local work)
2. They run: `rm -rf <workspace-root>; mkdir <workspace-root>; cd <workspace-root>`
3. They run: `niwa init acmecorp --from acmecorp/dot-niwa`
4. They run: `niwa config set private acmecorp/dot-niwa-private`
5. They run: `niwa create` → fresh workspace instance with all 13 repos
6. They restore any local branches, stashed work, etc. from the backup

Path 2a is preferred (zero-downtime) if teams trust that the split is correct. Path 2b is preferred if teams want a clean slate or suspect config drift.

**Step 3: CI/CD updates**

CI/CD currently clones and applies the old all-in-one config with private access. After the split:
- Option 1: Update CI/CD to use the public config only (`niwa init acmecorp --from acmecorp/dot-niwa` + `niwa apply --skip-private`). This is cleanest but requires CI/CD to consciously skip private access.
- Option 2: Update CI/CD to have `acmecorp/dot-niwa-private` registered (setup as before), but the CI container has no PAT for private repos. First run: silent skip of private companion (no access). Subsequent runs: private companion sync failure (if the feature requires "not cloned = silent, was cloned = error" semantics). This is fragile.
- Recommendation: Strongly encourage Option 1. Use `--skip-private` in CI/CD explicitly.

**Timeline:**
- Immediate (Day 1): Sarah publishes the split configs and announces the change.
- By end of week: All team members who have workspaces have re-initialized or updated (Path 2a or 2b). CI/CD is updated to use public config + `--skip-private`.
- No downtime if team uses Path 2a; 1-2 hours per developer if they choose Path 2b.

---

#### Option B: Phased Migration (Longer Timeline)

For teams that want to keep the old all-in-one config working in parallel while gradually migrating:

**Step 1: Support hybrid mode**

Define hybrid behavior: if a workspace.toml does not have a private companion registered, it can still work with all public and private repos mixed in the same config (today's behavior). If a private companion is registered, merging is additive.

This requires that the "local cache as proxy" graceful degradation is very explicit: if a workspace was initialized with an all-in-one config, that config continues to work. Only when a private companion is explicitly registered does the split take effect.

**Step 2: Gradual team migration**

1. Sarah publishes the split configs
2. Each team member migrates on their own timeline (no deadline)
3. Old all-in-one config continues to work (backward compat required)
4. New team members use the split config from day 1
5. Over time, the old config becomes unused

**Challenges:**
- Backward compatibility constraint: niwa must handle workspaces that never register a private companion indefinitely
- Confusion: teams might accidentally mix old and new initialization patterns
- Maintenance burden: the team must maintain both public and all-in-one configs in parallel (they diverge over time)

**Recommendation:** Option B is viable but requires strong backward-compat guarantees in the PRD. Option A (hard cut) is preferred if the team can coordinate. The PRD should decide: is phased migration a requirement, or is a single-day cutover acceptable?

---

### Implications for Requirements

**From Option A (Hard Cut):**
- **Backward compatibility is not required for workspaces that never use a private companion.** A workspace initialized from an all-in-one config continues to work (silent no-op if private companion is never registered).
- **The private companion registration command is essential for the migration.** `niwa config set private <repo>` enables existing workspaces to opt into the split mechanism without re-initialization.
- **Instance state must track "was private companion ever cloned" to distinguish first-time failures (silent) from sync failures on a previously-working instance (error).** This is the "local cache as proxy" semantic.
- **Apply idempotence is critical.** Existing repos must not be re-cloned during migration (apply verifies, does not delete and re-clone).

**From Option B (Phased Migration):**
- **All-in-one configs must continue to work indefinitely.** The PRD must declare whether v1 requires this or if it's a v2 constraint.
- **The private companion MUST be optional.** A workspace can be registered without a companion and work fine (no errors about missing private extension).

---

### Open Questions

1. **What is the timeline constraint for the migration?** 
   - Must the split be adoptable within a single day (Option A), or can teams migrate gradually (Option B)?
   - **Implication:** This determines whether backward compat for all-in-one configs is a v1 requirement.

2. **Should niwa provide migration tooling, or is documentation sufficient?**
   - Option A: `niwa migrate-private <new-public-config> <new-private-config>` command that guides the split
   - Option B: Clear docs + manual steps (what we've outlined above)
   - Option C: Out of scope for v1; teams manage the split themselves
   - **Decision needed:** What's the expected user investment?

3. **If a team accidentally leaves private repos in the public config during migration, should niwa warn or error?**
   - Today: no check. Public config can have private repos (they just fail to clone on CI/CD).
   - Option A: `niwa apply` warns if a repo's visibility doesn't match its group (private repo in public group)
   - Option B: No change; it's a configuration error that code review should catch
   - **Decision needed:** Should the feature include a lint/audit command to help teams validate the split?

4. **How should the "all-in-one config era" be officially ended?**
   - Option A: v1.0 is the final release that supports all-in-one configs; v2.0 requires the split (breaking change)
   - Option B: Support indefinitely (all-in-one configs work; private companion is always optional)
   - **Decision needed:** What's the deprecation path?

---

## Summary

User stories reveal that the feature must be **completely transparent to users without private access** (silent first-time failures, no prompts, no mention of a private companion) while providing **clear error handling for users with access** (subsequent failures after initial success are errors, not silent skips). The split between public and private configs must result in a **unified workspace experience** from the user's perspective — repos appear indistinguishable, CLAUDE.md hierarchy is merged seamlessly, and status output treats all repos equally.

Migration from existing all-in-one private configs is a **realistic near-term use case** requiring either a hard cut (teams re-initialize on the same day) or explicit phased migration support (all-in-one configs work indefinitely in parallel). The PRD must decide the migration timeline constraint and whether backward compatibility for all-in-one configs is a v1 requirement or a v2 decision. **Critical for the design:** instance state must track whether a private companion was ever successfully cloned (to implement "silent on first failure, error on subsequent failures" semantics), and the CLI must provide registration commands (`niwa config set/unset private`) that parallel the existing global config pattern for team familiarity.
