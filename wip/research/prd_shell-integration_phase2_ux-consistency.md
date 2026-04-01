# UX Consistency Research: niwa go and niwa create

Phase 2 research for the shell integration PRD. Focuses on current command
structure, naming model, and design options for consistent navigation and
creation UX.

## 1. Current Command Inventory

### niwa create

- **Args**: none (cobra.NoArgs)
- **Flags**: `--name <suffix>` -- custom instance name suffix
- **Behavior**: Discovers nearest workspace.toml by walking up from cwd.
  Creates a new instance directory under the workspace root.
- **Instance naming**:
  - First instance: bare config name (e.g., `tsukumogami`)
  - Subsequent: `<config>-<N>` where N = max(existing) + 1
  - With `--name=hotfix`: `<config>-hotfix`
- **Output**: Prints the created instance path
- **Limitation**: Must be run from within a workspace tree. No way to create
  an instance for a workspace you're not currently inside.

### niwa init

- **Args**: `[name]` (0 or 1)
- **Flags**: `--from <org/repo>`
- **Three modes**:
  1. `niwa init` -- scaffold minimal workspace.toml, no registry entry
  2. `niwa init <name>` -- scaffold with name, register in global config
  3. `niwa init <name> --from <org/repo>` -- clone config repo, register
- **Registry interaction**: Stores `{root, source}` in
  `~/.config/niwa/config.toml` under `[registry.<name>]`

### niwa status

- **Args**: `[instance]` (0 or 1)
- **Behavior**:
  - From inside instance: shows detail (repos, drift, managed files)
  - From workspace root: shows summary table of all instances
  - With instance name arg: shows detail for that instance

### niwa apply / niwa destroy / niwa reset

- All use workspace/instance discovery from cwd
- `apply` supports `--instance <name>` flag for targeting

## 2. Naming Model

### Hierarchy

```
workspace name    "tsukumogami"       (from workspace.toml [workspace] name)
  instance name   "tsukumogami-4"     (config_name + number/suffix)
    group name    "public"            (from [groups] keys)
      repo name   "niwa"              (from GitHub org discovery or [repos] keys)
```

### Global Registry

Located at `~/.config/niwa/config.toml`:

```toml
[registry.tsukumogami]
source = "tsukumogami/dot-niwa"   # or absolute path to workspace.toml
root = "/home/user/dev/niwaw/tsuku"
```

- Keyed by workspace name (the [workspace] name field, not the directory name)
- Stores workspace root directory (parent of .niwa/ and all instances)
- Stores source for re-init

### Name Relationships

| Level | Example | Stored Where | Unique Within |
|-------|---------|-------------|---------------|
| Workspace | `tsukumogami` | Global registry key, workspace.toml | Global (user machine) |
| Instance | `tsukumogami-4` | instance.json instance_name | Workspace root |
| Group | `public` | workspace.toml [groups] keys | Workspace config |
| Repo | `niwa` | instance.json repos keys, GitHub API | Instance (enforced by git) |

### Ambiguity Analysis

Workspace names and repo names live in different namespaces but could collide
in string form. For example, a workspace named "niwa" and a repo named "niwa"
would be ambiguous if `niwa go niwa` meant "go to workspace niwa" vs "go to
repo niwa in current instance."

Instance names always contain the workspace name as a prefix
(`tsukumogami-4`), so they won't collide with bare repo names in practice --
but they could collide with workspace names if someone named their workspace
`tsukumogami-4`.

## 3. How Other Multi-Level CLIs Handle Navigation

### kubectl (context / namespace / resource)

```
kubectl config use-context <name>     # switch context (like workspace)
kubectl -n <namespace> get pods       # namespace is a flag (like group)
kubectl get pod <name>                # resource is a positional arg
```

- Top-level selection is a separate subcommand (`config use-context`)
- Mid-level selection is a persistent flag (`-n`)
- Leaf-level selection is positional

**Takeaway**: Different levels use different mechanisms (subcommand, flag, arg).
No single arg tries to mean multiple things.

### git worktree

```
git worktree list                     # show all worktrees
git worktree add <path> [<branch>]    # create new worktree
cd <path>                             # navigation is plain cd
```

- No built-in "go to worktree" command
- Navigation relies on the user knowing paths
- Creation takes a path, not a name

**Takeaway**: git delegates navigation to the shell. No abstraction layer for
jumping between worktrees.

### tmux (session / window / pane)

```
tmux switch-client -t <session>       # switch session
tmux select-window -t <window>        # switch window (within session)
tmux select-pane -t <pane>            # switch pane (within window)
```

- Each level has its own subcommand
- Target syntax: `session:window.pane` with colon/dot separators
- Shorthand: bare name matches session; `session:2` means window 2 in session

**Takeaway**: Uses a structured address format with delimiters to disambiguate
levels. This is the closest analogy to niwa's hierarchy.

### zoxide / autojump

```
z <partial>                           # jump to best-matching directory
z <partial1> <partial2>               # multiple tokens narrow the match
```

- Frecency-based fuzzy matching on directory names
- No hierarchy concept -- just flat directory matching
- Works well when names are distinctive

**Takeaway**: If workspace and repo names are typically distinct (they are in
practice), fuzzy matching could work without explicit level disambiguation.

### terraform workspaces

```
terraform workspace list
terraform workspace select <name>
terraform workspace new <name>
```

- Single-level naming (no hierarchy)
- Separate subcommands for list/select/create

**Takeaway**: Clean but only works for one level.

## 4. Design Options for niwa go

### Option A: Positional arg = workspace, flag = deeper levels

```
niwa go                               # go to current workspace root
niwa go tsukumogami                   # go to workspace root
niwa go tsukumogami --instance 4      # go to instance (number or name)
niwa go tsukumogami --repo niwa       # go to repo (implies latest/current instance)
niwa go --repo niwa                   # go to repo in current instance
```

**Pros**: No ambiguity. Consistent with kubectl's model.
**Cons**: Verbose for the common case of jumping between repos.

### Option B: Smart single-arg with context-aware resolution

```
niwa go tsukumogami                   # always: go to workspace root
niwa go niwa                          # from inside workspace: go to repo "niwa"
niwa go niwa                          # from outside: error/prompt if ambiguous
```

Resolution order:
1. If arg matches a registered workspace name, go to workspace root
2. If inside a workspace and arg matches a repo name, go to that repo
3. Error with suggestions

**Pros**: Terse for common cases.
**Cons**: Workspace name "niwa" collides with repo name "niwa". Behavior
depends on context in surprising ways.

### Option C: Delimiter-based addressing (tmux-style)

```
niwa go tsukumogami                   # workspace root
niwa go tsukumogami:4                 # instance (by number)
niwa go tsukumogami:4/niwa            # repo in specific instance
niwa go :4/niwa                       # repo in instance 4 (current workspace)
niwa go /niwa                         # repo in current instance (or latest)
niwa go niwa                          # workspace "niwa" (registered name lookup)
```

Separator semantics: `workspace:instance/repo`

**Pros**: Single argument, no ambiguity, composable.
**Cons**: Unfamiliar syntax. Tab completion harder. The `/` could confuse
shells that interpret it as a path.

### Option D: Subcommand per level

```
niwa go tsukumogami                   # workspace root (bare arg = workspace)
niwa go repo niwa                     # repo in current instance
niwa go instance 4                    # instance by number
niwa go instance 4 repo niwa          # repo in specific instance
```

**Pros**: Fully explicit. Easy to parse and document.
**Cons**: Very verbose. "niwa go repo niwa" reads oddly.

### Option E: @prefix for workspace disambiguation

```
niwa go @tsukumogami                  # workspace root
niwa go niwa                          # repo in current instance
niwa go @tsukumogami/niwa             # repo in specific workspace (latest instance)
niwa go @tsukumogami:4/niwa           # fully qualified
```

**Pros**: @ clearly marks workspace level. Bare arg = repo (most common use).
**Cons**: @prefix is non-standard in CLI tools. Needs careful escaping in
some shells.

### Option F: Hybrid positional with --workspace flag

```
niwa go niwa                          # repo in current instance
niwa go -w tsukumogami                # workspace root
niwa go -w tsukumogami niwa           # repo in workspace (latest instance)
niwa go -w tsukumogami -i 4 niwa      # fully qualified
```

**Pros**: Bare arg defaults to repo (most common nav use case). Flags for
less common workspace/instance selection.
**Cons**: `niwa go -w tsukumogami` to reach workspace root feels like it
should be the simple case, not the flagged one.

## 5. Consistency Between go and create

Current `niwa create`:
- Takes no positional args
- Must run from inside a workspace tree
- `--name` flag for custom suffix

For consistency, `niwa create` should mirror `niwa go`'s targeting model. If
`niwa go` uses positional arg for workspace, then `niwa create` should too:

### Parallel structure examples

**If Option A (positional = workspace)**:
```
niwa go tsukumogami                   # navigate to workspace
niwa create tsukumogami               # create instance in workspace
niwa create tsukumogami --name hotfix # create named instance
```

**If Option C (delimiter)**:
```
niwa go tsukumogami                   # navigate to workspace root
niwa create tsukumogami               # create instance, land in root
niwa create tsukumogami --name hotfix # custom suffix
niwa create tsukumogami --start niwa  # create and land in repo
```

**If Option F (bare arg = repo, flag = workspace)**:
```
niwa go niwa                          # go to repo
niwa go -w tsukumogami                # go to workspace
niwa create                           # create instance (current workspace)
niwa create -w tsukumogami            # create instance (named workspace)
niwa create -w tsukumogami --start niwa  # create and cd to repo
```

## 6. Key Findings

### The workspace name is always unique
The global registry enforces unique workspace names. Repo names are only unique
within an instance. This means workspace names are the natural top-level
identifier.

### Instance numbers are the stable instance identifier
Instance names like `tsukumogami-4` are human-readable but the number portion
(`4`) is the distinguishing part. The prefix is always the workspace name.
This means `:4` is sufficient to identify an instance within a workspace.

### Groups are structural, not navigational
Groups exist for repo classification (visibility-based directory layout). Users
don't navigate to "the public group" -- they navigate to repos. Groups can be
ignored in the go/create UX.

### The common case is repo-to-repo within an instance
Use case 3 (jump between repos in same instance) is the most frequent
navigation after the initial workspace-root jump. This should be the shortest
command.

### create needs a "land in" capability
Use case 2 says "create a new instance and land in a specific repo." Current
`niwa create` only prints the path. The shell integration needs to both create
and cd. A `--start <repo>` or `--cd <repo>` flag on create would pair with
the shell function to cd after creation.

### Discovery vs registry: two resolution paths
- From inside a workspace: walk up to find .niwa/ (discovery)
- From anywhere: look up workspace name in global registry

`niwa go` needs both paths. When given a workspace name, it should consult the
registry. When given a repo name with no workspace qualifier, it should use
discovery (walk up from cwd).
