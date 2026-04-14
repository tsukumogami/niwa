# Decision: niwa go -r scoping when -w is set

## Context

When the user types `niwa go -w <ws> -r <TAB>`, shell completion must produce a list of candidate repo names. The runtime path for the same invocation is `resolveWorkspaceRepo` in `internal/cli/go.go:136-162`: it loads the global registry, enumerates instances of `<ws>` via `workspace.EnumerateInstances`, sorts the instance directory paths lexicographically, picks `instances[0]`, and then resolves the repo inside that single instance using `findRepoDir` (`internal/cli/repo_resolve.go:13-51`). No other instance is consulted — if `myrepo` exists in instance `2` but not in instance `1`, the command errors out.

The decision is whether completion should mirror that single-instance scope exactly, widen it by unioning repos across all instances of `<ws>`, narrow it further by demanding an instance selector, or branch on cwd. Instances are typically clones of the same `workspace.toml`, so repo sets overlap heavily, but drift is possible (a repo added locally, a partial clone, a reconfigured group).

## Options Considered

### Option 1: Mirror runtime (sorted-first instance)
- Description: `completeRepoNames(cwd, ws)` looks up `<ws>` in the global registry, calls `workspace.EnumerateInstances(entry.Root)`, sorts the paths, picks `[0]`, and enumerates its group/repo layout. Identical traversal to `resolveWorkspaceRepo`.
- Pros:
  - Perfect symmetry with runtime: every suggestion is guaranteed to resolve. No "suggested but rejected" cases.
  - Minimum work: one `ReadDir` on workspace root plus one `ReadDir` on the chosen instance plus one `ReadDir` per group. Well under 100ms on any realistic layout.
  - Shortest suggestion list, which matters for shell rendering (especially zsh/fish grids).
  - Trivial to implement — it reuses the exact enumeration primitives already in `internal/workspace` and `internal/cli/repo_resolve.go`.
- Cons:
  - If the sorted-first instance is missing a repo that exists elsewhere, the user won't see that repo in completion. They'd have to type it or use a different flag combination. Today, though, the command would reject that repo anyway, so completion is not hiding anything reachable.
  - Lexicographic sort on numeric instance names (`"1" < "10" < "2"`) is quirky, but completion inherits the same quirk as runtime, so there's no new confusion.

### Option 2: Union across all instances
- Description: Enumerate all instances of `<ws>`, enumerate each instance's group/repo tree, union the names, dedupe, return the merged list.
- Pros:
  - Maximum discoverability: any repo name that exists in any instance shows up.
  - Robust against instance drift.
- Cons:
  - Breaks symmetry with runtime. Completion would suggest `myrepo` even when `resolveWorkspaceRepo` rejects it for the sorted-first instance. This is precisely the "completion offers X, command refuses X" bug the Decision Drivers call out.
  - Latency scales with instance count: for each instance we need one `ReadDir` on the instance + one per group. Most workspaces have 1-3 instances, but a worst case of 10 instances with 5 groups each is 60 `ReadDir` calls. Still typically under 100ms on local disk, but closer to the budget and worse on slow FS or network mounts.
  - Longer, noisier suggestion lists in shells.
  - Implementation is more code (iteration, set merging) than Option 1.

### Option 3: Require instance narrowing
- Description: Return empty when only `-w` is set. Require a new `--instance`/`-i` flag or a second positional argument before completing `-r`.
- Pros:
  - Makes the "which instance?" question explicit, eliminating ambiguity entirely.
  - Keeps completion output tight.
- Cons:
  - No such flag exists today. Adding one expands scope well beyond completion and changes the CLI surface.
  - Breaks the current working UX: `niwa go -w ws -r repo` is a documented, working invocation (see examples in the goCmd long help). Making completion demand more than the runtime demands inverts the usual relationship.
  - Empty completion when the command itself would succeed is its own surprise.
  - Punts the problem — it doesn't choose a policy for `-w`-only, it just blocks completion.

### Option 4: Current-instance priority with fallback
- Description: If cwd is inside some instance of `<ws>` (detect via `workspace.DiscoverInstance` and compare against registry entry's root), scope completion to that instance. Otherwise fall back to Option 1.
- Pros:
  - Nice when cwd happens to be inside a non-default instance of the named workspace.
  - Modestly better discoverability than Option 1 for that narrow case.
- Cons:
  - Asymmetric with runtime: `resolveWorkspaceRepo` ignores cwd entirely when `-w` is set. Completion that uses cwd would suggest repos the command won't accept — again violating the symmetry driver.
  - The cwd-matches-workspace case is uncommon. If the user's cwd is already inside an instance of `<ws>`, they don't need `-w` — they'd use `niwa go -r` (which hits `resolveCurrentInstanceRepo` and correctly uses cwd). Adding cwd detection here solves a problem users don't have.
  - More code, more branches, more edge cases (what if cwd is in instance `3` of `<ws>` but instance `3` no longer has `.niwa/instance.json`?).

## Decision

Chosen option: **1 (Mirror runtime — sorted-first instance)**.

### Rationale

The Decision Drivers rank "symmetry with runtime" as a correctness property ("divergence between what completion offers and what the command accepts is a bug"). Only Option 1 satisfies it. `resolveWorkspaceRepo` commits to exactly one instance — the lexicographically first — and rejects any repo not resolvable there. Any completion strategy that surfaces repos from other instances will, by construction, suggest names the command refuses, which is the defining bug the design is trying to avoid.

Option 1 also wins on the other drivers:
- **Latency**: one `ReadDir` on the workspace root, one on the chosen instance, one per group. Tens of syscalls at most, well inside the 100ms envelope.
- **Cross-shell rendering**: shortest candidate list.
- **Implementation complexity**: directly reuses `workspace.EnumerateInstances` and the group-scan pattern already in `findRepoDir`. No new primitives.

The cost is discoverability for drifted instances, but (a) instances are clones of the same `workspace.toml` and normally share repo layouts, and (b) when a repo truly exists only in a non-sorted-first instance, the user's recourse is the same as without completion: navigate into that instance's directory and use `niwa go -r`, where cwd correctly selects the intended instance. Completion doesn't need to paper over a runtime limitation; if we want cross-instance repo resolution, the right fix is in `resolveWorkspaceRepo`, not in the completion layer.

### Trade-offs accepted

- Users with drift between instances won't see repos unique to non-first instances when completing `-w <ws> -r <TAB>`. This is acceptable because (1) the runtime would reject those names anyway, and (2) the fix, if desired, belongs in runtime resolution, not completion.
- Completion inherits the lexicographic-vs-numeric sort quirk of the runtime (`"1" < "10" < "2"`). This is a pre-existing runtime behavior and a separate decision; keeping completion in lockstep means any future fix applies uniformly to both.
- Completion will silently yield an empty list if the sorted-first instance's root is missing or unreadable, matching what runtime would error on. Completion should swallow errors and return empty rather than surface them, per shell completion conventions.

## Rejected alternatives

- **Option 2 (union across instances)**: Maximizes discoverability at the direct cost of symmetry. Suggesting repos the command will reject is the specific failure mode the design drivers identify as a bug. Also increases latency linearly with instance count.
- **Option 3 (require instance narrowing)**: Demands a CLI surface change (new flag) that's out of scope for contextual completion, and produces empty completion for an invocation pattern that works today. Trades a minor ambiguity for a major usability regression.
- **Option 4 (cwd priority with fallback)**: Breaks symmetry whenever cwd is inside a non-sorted-first instance, for a marginal discoverability gain in a case users would normally handle with `-r` alone (no `-w`). Adds branching logic without a commensurate UX payoff.

## Assumptions

- `resolveWorkspaceRepo`'s single-instance scoping will remain the runtime policy for `-w <ws> -r <repo>` for the foreseeable future. If that policy changes (e.g., to search all instances), completion should track the new policy under the same symmetry principle.
- Instances of the same workspace typically share repo layouts (cloned from the same `workspace.toml`); drift is the exception, not the norm.
- Shell completion functions are expected to tolerate errors silently (return empty list, no stderr). Callers should not surface FS errors during completion.
- The 100ms latency envelope applies to the completion callback invocation on a warm filesystem. Cold-cache NFS or remote mounts are out of scope for this decision.
