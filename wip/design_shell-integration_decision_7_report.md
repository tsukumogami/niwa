<!-- decision:start id="go-command-design" status="assumed" -->
### Decision: niwa go — use cases, syntax, binary vs wrapper, and error communication

**Context**

The design doc defined `niwa go [workspace] [repo]` but the language conflated
workspaces with instances, and several UX questions were unresolved: whether go is
a real binary subcommand, how to handle multi-instance workspaces, what the no-args
case should target, and how to communicate to users that go needs the shell wrapper
for cd to work.

Research mapped the hierarchy: workspace root (contains workspace.toml) > instances
(immediate subdirs with instance.json) > groups > repos. The global registry maps
workspace names to roots, not instances. Instance discovery scans the workspace root.
No usage tracking exists for "most recently used instance."

The most frequent navigation case is "jump to a different repo in my current
instance" — which the original two-positional-arg design made redundant (requiring
`niwa go tsukumogami niwa` instead of just `niwa go niwa`).

**Assumptions**

- Workspace names and repo names rarely collide. Workspace names tend to be
  organization-level (tsukumogami, codespar) while repo names are project-level
  (niwa, tsuku, koto). If wrong: the disambiguation error handles it gracefully,
  and `--workspace` flag is available.
- Users work within one instance at a time far more often than they jump between
  instances. If wrong: `--instance` flag is available for explicit selection.
- Writing visit timestamps on every go command is undesirable overhead for a
  navigation command. If wrong: visit tracking can be added later without changing
  the command surface.

**Chosen: Real binary subcommand with context-aware resolution**

`niwa go` is a real cobra subcommand in the binary. It resolves navigation targets
and prints an absolute directory path to stdout. The shell wrapper captures stdout
and cds. Without the wrapper, the path is printed to the terminal — still useful
for `cd $(niwa go ...)` workflows and scripting.

### Command syntax

```
niwa go                              # instance root (from cwd)
niwa go <target>                     # context-aware: repo in current instance, OR workspace
niwa go <target> <repo>              # repo within target workspace
niwa go --workspace <name>           # explicit workspace (disambiguation)
niwa go --workspace <name> <repo>    # repo within explicit workspace
niwa go --instance <name>            # specific instance (when multiple exist)
```

### Resolution rules for single-arg `niwa go <target>`

The binary resolves a single positional argument in this order:

1. **Current instance repo**: If cwd is inside an instance, check if `<target>`
   matches a repo name in that instance (filesystem scan via `findRepoDir`).
   If found, print the repo path.

2. **Registry workspace**: If not a repo in the current instance, check the
   global registry for a workspace named `<target>`. If found, resolve to an
   instance of that workspace and print the instance root.

3. **Error**: If neither matches, print an error to stderr with suggestions
   (did you mean repo X? Is workspace Y registered?).

If BOTH match (repo name equals a workspace name), prefer the current-instance
repo. The user can force workspace resolution with `--workspace <name>`.

### No-args case: `niwa go`

Navigates to the **instance root** of the current instance (resolved via
`DiscoverInstance` from cwd). The instance root is where CLAUDE.md lives and is
the natural "home" for a working session. The workspace root (parent of all
instances) is a container directory with no working content — users rarely want
to cd there.

If cwd is not inside any instance, error with: "not inside a workspace instance."

### Multi-instance disambiguation

When a workspace has multiple instances (e.g., tsukumogami, tsukumogami-2,
tsukumogami-4):

1. If cwd is inside an instance of the target workspace, stay in that instance.
2. If outside the workspace, pick the instance with the lowest number (the
   original). This is deterministic and simple.
3. Users can override with `--instance <name>` for any instance by name.

No visit tracking is introduced. The common case (user is already in the
workspace) is handled by rule 1. The fallback (lowest number) is predictable.

### Use case matrix

| Scenario | Command | Resolution |
|----------|---------|------------|
| Go to instance root | `niwa go` | DiscoverInstance from cwd |
| Go to repo in current instance | `niwa go niwa` | findRepoDir in current instance |
| Go to different workspace | `niwa go codespar` | Registry lookup + instance selection |
| Go to repo in different workspace | `niwa go codespar api` | Registry + instance + findRepoDir |
| Go to specific instance | `niwa go --instance tsukumogami-2` | Instance name match within current workspace |
| Go to repo in specific instance | `niwa go --instance tsukumogami-2 niwa` | Instance match + findRepoDir |
| Disambiguate workspace vs repo | `niwa go --workspace tsuku` | Force registry lookup |

### Wrapper-missing behavior

`niwa go` is a real binary subcommand. Without the shell wrapper:

- The binary runs, resolves the path, and prints it to stdout
- If `_NIWA_SHELL_INIT` is unset, the binary prints a hint to stderr:
  `hint: niwa go printed the path but can't cd without shell integration. Run: niwa shell-init install`
- The output is still useful: `cd $(niwa go niwa)` works without the wrapper
- This matches the zoxide pattern: `zoxide query` is a real command; `z` is
  the wrapper convenience

### Path safety

- `niwa go ../../etc`: rejected by path containment validation (resolved path
  must be within instance root, using `filepath.Rel` on logical path)
- `niwa go <target>` where target resolves outside any known workspace: error

**Rationale**

Context-aware resolution (current-instance repo first, then registry) covers the
most frequent case without redundancy. Typing `niwa go niwa` is natural when
you're already in the workspace — you shouldn't need to repeat the workspace name.
The registry fallback handles cross-workspace jumps.

Making go a real binary subcommand follows the zoxide pattern and preserves the
"niwa works without wrapper" principle. The path is always resolved by the binary;
cd is always done by the wrapper. Without the wrapper, the path is still printed
and useful.

Multi-instance disambiguation via "prefer current, fall back to first" avoids
introducing visit tracking state. The `--instance` flag covers the explicit case.

**Alternatives Considered**

- **Wrapper-only command (no binary subcommand)**: go exists only in the shell
  function. Rejected: breaks R14 (niwa works without wrapper), prevents scripting
  (`cd $(niwa go ...)`), and diverges from the zoxide pattern the entire design
  follows.

- **Binary subcommand that refuses without wrapper**: go checks _NIWA_SHELL_INIT
  and errors if unset. Rejected: unnecessarily hostile. The path output is useful
  even without cd.

- **Two positional args only (original design)**: `niwa go <workspace> <repo>`.
  Rejected: requires redundant workspace name for the most common case (repo in
  current instance). `niwa go tsukumogami niwa` when you're already in tsukumogami
  is poor UX.

- **Explicit flags for everything**: `--workspace` and `--repo` flags, no
  positional ambiguity. Rejected: too verbose for the common case. The
  disambiguation rule (current repo first, then registry) handles the rare
  collision, and `--workspace` is available as escape hatch.

- **Track last-visited instance**: Write a visit timestamp on each go command.
  Rejected: adds write overhead to a navigation command. Deterministic "prefer
  current, fall back to first" is simpler and covers the common case.

- **Require --instance when multiple exist**: Error with "multiple instances found."
  Rejected: annoying for the common case. Users who work in the "current" instance
  shouldn't be prompted every time.

**Consequences**

- `niwa go` becomes a more complex subcommand than originally designed (context-aware
  resolution, flags, disambiguation)
- The single-arg ambiguity (repo vs workspace) is a trade-off. It's handled by
  "current instance repo wins" but may surprise users who expect workspace lookup.
  The hint on collision helps.
- `findRepoDir` filesystem scan is used for repo resolution — no schema changes
  needed, but O(groups) per lookup
- The `--workspace` and `--instance` flags add surface area but are only needed
  for disambiguation — most users never use them
- PRD user stories US-7 and US-8 need updating to reflect the new syntax
- Design doc Decision 3 and the Solution Architecture need updating
<!-- decision:end -->
