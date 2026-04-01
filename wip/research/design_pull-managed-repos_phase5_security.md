# Security Review: pull-managed-repos

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes

`git fetch` and `git pull --ff-only` download objects from remote repositories. This is the primary external input vector. However, git itself handles integrity verification through SHA-based content addressing -- objects are validated against their hashes on fetch. The `--ff-only` constraint further limits what pull can do: it won't create merge commits or run merge drivers, reducing the surface for unexpected code execution during the pull itself.

The main risk is that the *content* of pulled code may later be executed (setup scripts, hooks, build steps). This is not new -- the same repos are already cloned by `niwa apply`. Pulling updates is equivalent in trust posture to the initial clone: if you trust the remote enough to clone it, fetching updates carries the same trust assumption.

One consideration: git hooks in the fetched repo (e.g., `post-merge`) could execute if present. Git's `post-merge` hook runs after a successful pull. If a malicious commit adds a post-merge hook to a repo that niwa manages, the next `niwa apply` pull would trigger it. This is standard git behavior and not unique to niwa, but it is worth noting in documentation. The design's choice to shell out to `git pull` rather than implementing a custom fetch-and-reset means niwa inherits all of git's hook execution behavior.

**Risk level:** Low. Equivalent to existing clone trust model. Post-merge hook execution is standard git behavior.

### Permission Scope

**Applies:** Yes

The feature operates within the existing permission envelope:

- **Filesystem:** Reads workspace TOML config and instance state, writes only within already-cloned repo directories. No new directories are created. The dirty-check and branch-check guards prevent writes to repos the user is actively working in.
- **Network:** `git fetch origin` contacts the same remotes already configured in each repo. No new network targets are introduced.
- **Process:** Spawns `git` subprocesses via `exec.CommandContext`, consistent with the existing `Cloner` pattern. Context-based cancellation is already in place.
- **Credentials:** Inherits the user's existing git credential configuration. No new tokens or credentials are stored, created, or transmitted by niwa itself.

The skip-if-dirty and skip-if-not-default-branch guards are important safety properties. They prevent niwa from silently discarding uncommitted work or switching branches. The `--ff-only` flag prevents creating merge commits, which means pull fails cleanly on divergence rather than performing an unattended merge.

**Risk level:** Low. No privilege escalation beyond what clone already requires.

### Supply Chain or Dependency Trust

**Applies:** No (minimal new surface)

The feature adds no new dependencies. It uses `git` (already required for clone) and reads from the same TOML config files that drive the rest of the pipeline. The remote URLs come from the same config and instance state already trusted during clone.

Default branch resolution follows a clear precedence chain: per-repo TOML config, then workspace-level `default_branch`, then hardcoded "main". All inputs are local config values under the workspace author's control. There is no new remote resolution of branch names (e.g., no `git remote show origin` to discover the default branch), which avoids a class of TOCTOU issues where the remote's default branch could change between config authoring and apply time.

### Data Exposure

**Applies:** No

No new data is collected, logged, or transmitted. Git fetch/pull communicate with remotes using the user's existing credentials and protocols. Niwa does not inspect, log, or forward the content of fetched objects. Error messages from failed pulls may appear on stderr, but these contain only repo names, branch names, and git error text -- all of which the user already has access to.

## Recommended Outcome

**OPTION 1: No security changes required.**

The design stays within the existing trust boundary established by clone. The key safety properties are already present:

1. Dirty repos are never touched (prevents data loss).
2. Non-default branches are never touched (prevents workflow disruption).
3. `--ff-only` prevents unattended merges (fails cleanly on divergence).
4. `--no-pull` flag gives users an explicit opt-out.
5. No new credentials, network targets, or elevated permissions.

The only minor documentation suggestion: note that git's `post-merge` hook will fire after successful pulls, which is standard git behavior but may surprise users who think of `niwa apply` as a purely declarative operation.

## Summary

This design introduces no new security surface beyond what the existing clone operation already establishes. It shells out to `git pull --ff-only` using the same credential and network context as clone, with solid guardrails (dirty-skip, branch-check, ff-only) that prevent data loss and unattended merges. No changes to the security posture are needed.
