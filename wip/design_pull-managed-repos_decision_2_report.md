# Decision 2: Default Branch Detection

How should niwa determine each repo's default branch for pull eligibility?

## Options Evaluated

### Option A: Config-first

Use `[repos.X] branch` if set, else workspace `default_branch`, else "main".

**Pros:**
- Fully deterministic. The answer depends only on the TOML file, which the user controls and can inspect.
- Works offline -- no git queries needed.
- Aligns with how `CloneWithBranch()` already resolves branches at clone time (per-repo override, then empty string which defers to git default). Adding `default_branch` as the middle fallback completes the chain.
- Simple to implement: a single function with two lookups and a constant fallback.

**Cons:**
- If a user clones a repo whose remote default is "master" (not "main") and doesn't set `branch` or `default_branch`, niwa assumes "main" and skips pull for a repo that's actually on its default branch.
- Requires users of non-"main" repos to remember to set the override. This is a documentation burden, though it only matters for repos that don't use "main".

**Edge cases:**
- Repo cloned before this feature: works fine, the config is the source of truth regardless of when the clone happened.
- Repo remote changed its default branch (e.g., "master" to "main"): user must update config to match, but that's an explicit action they'd want to take anyway.

**Reliability:** High. Pure config lookup, no I/O.
**Complexity:** Low. One function, two map lookups, one constant.
**Predictability:** High. User reads the TOML and knows exactly what niwa will check.

---

### Option B: Git-first

Query `git symbolic-ref refs/remotes/origin/HEAD` to get the remote's actual default branch.

**Pros:**
- Always correct for the remote's current state (after a fetch).
- No config maintenance needed. "Just works" for repos with any default branch name.

**Cons:**
- Requires a prior `git fetch` or `git remote set-head origin --auto` to populate `refs/remotes/origin/HEAD`. Without it, the ref may not exist (many clones don't set it). This is the critical weakness.
- Breaks offline. If the ref was never populated, there's no fallback.
- Adds I/O (shelling out to git) to what should be a fast classification step.
- `refs/remotes/origin/HEAD` can be stale if the remote changed its default branch and no one ran `set-head --auto`.
- Non-deterministic from the user's perspective -- the answer depends on hidden git state that isn't visible in config.

**Edge cases:**
- Fresh clone via `git clone`: usually sets `origin/HEAD`, so this works.
- Clone via `git clone --branch X`: may not set `origin/HEAD`, depends on git version.
- `CloneWith()` in niwa uses `--branch` when a branch is specified, which may skip setting `origin/HEAD`.
- Repos cloned before this feature: `origin/HEAD` may or may not exist depending on how they were cloned.

**Reliability:** Medium. Depends on git state that niwa doesn't control.
**Complexity:** Medium. Need git command execution, error handling for missing ref, fallback logic.
**Predictability:** Low. Users can't easily predict what `origin/HEAD` resolves to without running `git symbolic-ref` themselves.

---

### Option C: Hybrid (config -> git -> "main")

Use config if set, fall back to git query, fall back to "main".

**Pros:**
- Covers the most cases. Config overrides give control; git query handles the common case of unspecified repos; "main" is the last resort.
- Per-repo branch overrides still work.

**Cons:**
- Three-level fallback is harder to reason about. When pull doesn't happen, the user has to debug which level resolved the branch and why it doesn't match.
- Inherits Option B's offline and staleness problems for the middle tier.
- The git query adds latency and can fail silently (returning empty, causing fallback to "main" when the actual branch is "master").
- Testing is harder: need to mock git state in addition to config state.

**Edge cases:**
- Same as Option B's edge cases, but only for repos without config overrides.
- If git query fails silently (no `origin/HEAD`), falls through to "main" which may be wrong. The user wouldn't know the git tier was skipped.

**Reliability:** Medium. The config tier is solid, but the git tier introduces the same problems as Option B.
**Complexity:** High. Three tiers, git I/O, error handling, harder to test and debug.
**Predictability:** Medium. The config path is predictable, but the git fallback isn't transparent.

---

### Option D: Record at clone time

Store the branch used during initial clone in `RepoState`, use that as the default branch.

**Pros:**
- Captures the actual branch that was cloned, so it matches reality for that repo.
- No git queries needed after clone. Works offline.
- Self-documenting: `instance.json` shows exactly what branch niwa considers "default" for each repo.

**Cons:**
- Repos cloned before this feature have no recorded branch. Need a migration path: either backfill (how? git query? config lookup?) or treat missing as "main". This bootstrapping problem is unavoidable.
- If the user changes `[repos.X] branch` in config after cloning, the recorded branch is stale. Need logic to detect config/state divergence and decide what wins. This adds a drift detection concern that's orthogonal to pull.
- Adds a new field to `RepoState`, which currently only has `URL` and `Cloned`. The schema change is small but couples branch tracking to the state file.
- The "branch used during clone" isn't necessarily the remote's default branch. If a user clones with `branch = "develop"`, that's what gets recorded, but it's an override, not a default. The semantic meaning gets muddled.

**Edge cases:**
- Migration: existing repos get no branch in state. Must fall back to config or "main".
- Config change after clone: `branch = "develop"` changed to `branch = "release"`. State says "develop", config says "release". Which wins? If config wins, this reduces to Option A with extra bookkeeping. If state wins, user can't change the branch without re-cloning.
- Branch field empty in state (cloned with no override, git picked its default): same as migration case.

**Reliability:** Medium. Solid after clone, but bootstrapping and drift cases reduce confidence.
**Complexity:** Medium-High. State schema change, migration logic, drift detection.
**Predictability:** Medium. Users would need to inspect `instance.json` to see what branch niwa thinks it's tracking.

---

## Recommendation

**Option A: Config-first.**

The config already has both levels of branch specification (`[repos.X] branch` per-repo, `default_branch` workspace-wide), and `RepoCloneBranch()` already resolves the per-repo override. Extending this to include `default_branch` as a middle fallback and "main" as the final fallback is a minimal, predictable change.

The main downside -- repos with non-"main" defaults needing explicit config -- is actually a feature. niwa is a declarative workspace manager. Users declare their workspace structure in TOML, and niwa applies it. If a repo uses "master" as its default branch, that's workspace-specific knowledge that belongs in the config.

The git-query approaches (B and C) introduce non-determinism and offline failures for marginal benefit. The recording approach (D) adds state complexity and a migration burden without solving the fundamental question differently than config does.

**Confidence: High.**

The config-first approach is the simplest, most predictable, and most aligned with niwa's declarative design. The "main" hardcoded fallback matches the overwhelming majority of modern repos.

## Assumptions

- The vast majority of repos in niwa-managed workspaces use "main" as their default branch. Repos using other default branches are the exception and worth the config entry.
- Users expect niwa's behavior to be determined by the TOML config they write, not by hidden git state.
- Offline operation is important (niwa apply shouldn't require network for branch resolution).
- `RepoCloneBranch()` will be extended to check `ws.Workspace.DefaultBranch` before returning empty string, providing a clean three-tier resolution: per-repo override -> workspace default -> "main".

## Rejected Alternatives

- **Option B (Git-first):** `refs/remotes/origin/HEAD` is unreliable. It may not exist, may be stale, and requires I/O. Non-deterministic behavior contradicts niwa's declarative model.
- **Option C (Hybrid):** Adds complexity without proportional benefit. The git tier inherits Option B's problems and makes debugging harder. Three-level fallback with mixed sources (config + git + constant) is over-engineered for this use case.
- **Option D (Record at clone time):** Introduces a migration burden for existing repos, conflates "branch used to clone" with "default branch", and creates a drift detection problem. If config always wins over state (which it should), this reduces to Option A with unnecessary bookkeeping.
