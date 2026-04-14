# Lead: What is the full command/flag map that should get dynamic completion, and where does each data source live in the codebase?

## Findings

### Command inventory (from `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/cli/`)

Every cobra `var *Cmd = &cobra.Command{...}` registered on `rootCmd` (or one of its subcommands):

| File | Command | `Use` | Registered where |
|------|---------|-------|------------------|
| apply.go | `niwa apply` | `apply [workspace-name]` | root |
| config.go | `niwa config` | `config` (parent, no `RunE`) | root |
| config_set.go | `niwa config set` | `set` (parent, no `RunE`) | config |
| config_set.go | `niwa config set global` | `global <repo>` | config set |
| config_unset.go | `niwa config unset` | `unset` (parent) | config |
| config_unset.go | `niwa config unset global` | `global` (no arg) | config unset |
| create.go | `niwa create` | `create [workspace-name]` | root |
| destroy.go | `niwa destroy` | `destroy [instance]` | root |
| go.go | `niwa go` | `go [target]` | root |
| init.go | `niwa init` | `init [name]` | root |
| reset.go | `niwa reset` | `reset [instance]` | root |
| shell_init.go | `niwa shell-init` | `shell-init` (parent) | root |
| shell_init.go | `niwa shell-init {bash,zsh,auto,install,uninstall,status}` | no args | shell-init |
| status.go | `niwa status` | `status [instance]` | root |
| version.go | `niwa version` | `version` (no arg) | root |

### Coverage table (positions that should resolve to a finite, runtime-enumerable set)

| # | Command | Position | Kind | Data source (function + file:line) | Notes |
|---|---------|----------|------|------------------------------------|-------|
| 1 | `niwa apply [workspace-name]` | positional arg 0 | workspace | `config.LoadGlobalConfig` -> iterate `GlobalConfig.Registry` keys. `config.LoadGlobalConfig` at `internal/config/registry.go:82`; `Registry` field at `internal/config/registry.go:15`; lookup used in `resolveRegistryScope` at `internal/cli/apply.go:134-143` | Only registered workspaces are valid here. Resolution path `apply.go:60-63`. |
| 2 | `niwa apply --instance <name>` | flag value | instance | `workspace.EnumerateInstances(workspaceRoot)` + `workspace.LoadState` -> `state.InstanceName`. Used indirectly via `workspace.ResolveApplyScope` -> `resolveNamed` at `internal/workspace/scope.go:82-109`, which enumerates from `EnumerateInstances` at `internal/workspace/state.go:131`. Instance name field is written by state loader at `internal/workspace/state.go:73`. | Requires cwd inside workspace (scope.go:83 calls `config.Discover`). Completion must first locate the workspace root from cwd. |
| 3 | `niwa create [workspace-name]` | positional arg 0 | workspace | Same as #1 — `config.LoadGlobalConfig().Registry`. Explicit map iteration at `internal/cli/create.go:79-89`. | Only registered workspaces. No instance-name completion here (create synthesizes a fresh name). |
| 4 | `niwa create --name <suffix>` | flag value | other (free text) | n/a | **Do not complete.** This suffix becomes part of a new directory name. Completing from existing values invites collisions. |
| 5 | `niwa create -r, --repo <name>` | flag value | repo | Nothing today — the command only uses this after creation via `findRepoDir(instancePath, createRepo)` at `internal/cli/create.go:139-146`. No "future repo list" exists pre-create. Could derive from the workspace config's `[repos]`/groups by loading `workspace.toml` from the registry entry's `Source`. | Tricky: at completion time the instance does not exist yet. Data source would be parsing the workspace config (config.Load on `entry.Source` if local, or the cloned config dir). Probably skip initially. |
| 6 | `niwa destroy [instance]` | positional arg 0 | instance | `workspace.ResolveInstanceTarget` -> `resolveInstanceByName` -> `workspace.EnumerateInstances` + `workspace.LoadState(dir).InstanceName`. Enumeration: `internal/workspace/state.go:131`; state load: `internal/workspace/state.go:73`; resolver: `internal/workspace/destroy.go:20,34`. | Requires cwd inside workspace (uses `config.Discover` from cwd). **High-consequence footgun** — completing and Enter can destroy work. |
| 7 | `niwa go [target]` | positional arg 0 | repo OR workspace (context-aware) | Union: `findRepoDir`-enumerable repos + `config.LoadGlobalConfig().Registry` workspaces. Context-aware resolver at `internal/cli/go.go:164-211`. Repo enumeration via `findRepoDir` in `internal/cli/repo_resolve.go:13-51` (scans group subdirs of discovered instance via `workspace.DiscoverInstance` at `internal/workspace/state.go:109`). Workspace source: `internal/config/registry.go:15,47`. | Completion set depends on whether cwd is inside an instance. Outside instance: only workspaces; inside instance: both, with repos given priority and duplicate names annotated. |
| 8 | `niwa go -w, --workspace <name>` | flag value | workspace | `config.LoadGlobalConfig().Registry` (same as #1). Lookup at `internal/cli/go.go:101-116,141`. | Always available regardless of cwd. |
| 9 | `niwa go -r, --repo <name>` | flag value | repo | Depends on whether `-w` is set. Without `-w`: `workspace.DiscoverInstance(cwd)` + `findRepoDir`-style enumeration, at `internal/cli/go.go:118-134`. With `-w`: lookup registry entry, `workspace.EnumerateInstances(entry.Root)`, pick sorted-first instance, then scan its groups. Code path at `internal/cli/go.go:136-162`. | **Flag-dependent completion**: completing `-r` meaningfully requires reading the `-w` value already on the command line (if present), otherwise deriving from cwd. |
| 10 | `niwa init [name]` | positional arg 0 | workspace (mostly fresh) | Three modes per `resolveInitMode` at `internal/cli/init.go:59-81`: (a) name unknown -> scaffold fresh; (b) name registered with `Source` -> clone via registry. Source is `config.LoadGlobalConfig().Registry`. | Only the "re-clone from registry" mode benefits from completion. For new workspace names the user is inventing the name. Probably skip completion or offer only registry names (trades off: could confuse users who want to create a new name). |
| 11 | `niwa init --from <repo>` | flag value | other (free text URL/slug) | n/a | Do not complete; this is a clone URL or `org/repo` slug. |
| 12 | `niwa reset [instance]` | positional arg 0 | instance | Same as #6 — `workspace.ResolveInstanceTarget` in `internal/workspace/destroy.go:20` (shared helper, despite the filename), which uses `EnumerateInstances` + `LoadState`. Call site: `internal/cli/reset.go:44-50`. | Same footgun as destroy: reset is destroy-then-recreate. Additionally, only instances whose config dir `.niwa` is a git clone are valid (see `isClonedConfig` at `reset.go:84,121`). Completion could filter to reset-eligible instances only, but that adds latency (stat each `.niwa/.git`). |
| 13 | `niwa status [instance]` | positional arg 0 | instance | `config.Discover(cwd)` to find workspace root, then `workspace.EnumerateInstances` + `LoadState` reading `InstanceName`. Code at `internal/cli/status.go:62-98`. | Safe operation; completion here is pure upside. Requires cwd inside a workspace. |
| 14 | `niwa config set global <repo>` | positional arg 0 | other (URL/slug) | n/a | Accepts any `org/repo` or URL. Not enumerable. Do not complete. |

### Positions explicitly NOT needing dynamic completion

- `niwa apply --allow-dirty`, `--no-pull` — boolean.
- `niwa destroy --force`, `niwa reset --force` — boolean.
- `niwa init --skip-global` — boolean.
- All `niwa shell-init *` subcommands — static; cobra's default subcommand completion suffices.
- `niwa version` — no args.
- `niwa config unset global` — no args.

### Data-source call-graph summary

Three data sources cover all dynamic needs:

1. **Workspace names (global registry)** — six positions (#1, #3, #7-workspace-half, #8, #10, and the parent lookup used by #9 when `-w` is set).
   Source: `GlobalConfig.Registry map[string]RegistryEntry` (`internal/config/registry.go:15`), loaded via `config.LoadGlobalConfig()` (`registry.go:82`). Completion can just iterate the keys.

2. **Instance names (per-workspace)** — five positions (#2, #6, #12, #13, and implicitly #9 when resolving via `-w`).
   Source: `workspace.EnumerateInstances(root)` at `internal/workspace/state.go:131` (directory scan looking for `.niwa/instance.json`), then `workspace.LoadState(dir)` at `state.go:73` to read `state.InstanceName`. Locating `root` needs either (a) cwd walk via `config.Discover` (`internal/config/discover.go:18`) or (b) registry lookup for named workspace.

3. **Repo names (per-instance)** — three positions (#5 conditionally, #7-repo-half, #9).
   Source: inline scan in `findRepoDir` at `internal/cli/repo_resolve.go:13-51` — read immediate subdirectories of the instance root (skipping `.niwa` and `.claude`), then each of those (groups) for a matching entry. For completion we need the full list, not a lookup, so completion code must walk two levels instead of calling `findRepoDir` verbatim. Locating the instance root needs `workspace.DiscoverInstance(cwd)` (`internal/workspace/state.go:109`) or, with `-w`, `EnumerateInstances(entry.Root)` then picking one (go.go uses sorted-first at `go.go:153`).

## Implications

- Three small helpers cover 13 of 14 completion positions: `completeWorkspaceNames`, `completeInstanceNames(cwd)`, `completeRepoNames(cwd, optWorkspace)`. The flag-dependent case (#9 `-r` with `-w`) is the only one where a completion function must read another flag's value from the parsed command line; cobra's `ValidArgsFunction`/`RegisterFlagCompletionFunc` receive the `*cobra.Command`, so `cmd.Flag("workspace").Value.String()` is accessible.
- Commands I'd consider skipping in v1: `niwa create -r` (#5) and `niwa init [name]` (#10). Both have murky data sources — #5 needs to parse a config that hasn't been applied yet; #10 is usually a fresh name. Ship completion for the other 11 positions first.
- Registry-backed completions are essentially free (single TOML read of a small file under `$XDG_CONFIG_HOME/niwa/config.toml`). Instance- and repo-backed completions involve `os.ReadDir` + stat per entry — still fast for typical workspace sizes (<20 instances, <20 groups, <50 repos per instance), but they are the latency risk to test.
- The `destroy` and `reset` footgun deserves explicit design consideration. Options: (a) complete normally and trust the user, (b) complete but require a confirmation prompt when completion produced the value, (c) never offer auto-completion for destructive positions. Worth a decision record.
- `findRepoDir` duplicates the scan-two-levels logic in three places (create, go context-aware, go -r). Completion will be a fourth. Extracting an `EnumerateRepos(instanceRoot) []string` helper in `internal/workspace/` would consolidate the logic and eliminate the need for completion to duplicate the skip-list (`.niwa`, `.claude`).

## Surprises

- `ResolveInstanceTarget` lives in `internal/workspace/destroy.go` even though `reset` and (indirectly via `--instance`) `apply` also use the same resolution path. Filename is misleading; this is a shared helper.
- There is no single "enumerate repos in an instance" function; `findRepoDir` short-circuits on the first match and returns "ambiguous" when a name appears in multiple groups. Completion needs a different shape (return all names, possibly annotated with group when ambiguous).
- `niwa go -w <ws> -r <repo>` picks the **sorted-first** instance (`go.go:153-154`) rather than some designated "primary" instance. Completion of `-r` under `-w` will mirror that choice, which feels arbitrary — if a workspace has `codespar` and `codespar-2`, `-r` completions only reflect `codespar`. Flag a follow-up question in the design.
- `apply` has two code paths for workspace resolution — positional arg (registry) and `--instance` (cwd-scoped). These should offer different completions (registry vs instance names), which cobra handles cleanly since they're different positions.
- `niwa status` from the workspace root produces a summary for all instances; from inside one, detail for that one. Completion on its positional arg is only useful from the workspace root, but that's also exactly where the user is likely to want it.

## Open Questions

- Should `destroy` / `reset` positional arg completion be suppressed, gated behind an env var, or behave normally? A UX decision, not a code one.
- Should `niwa go -r` under `-w` enumerate repos across all instances in that workspace, or stick with the sorted-first convention the runtime uses? If the former, we diverge from runtime behavior (surprising); if the latter, completion is incomplete for workspaces with multiple instances.
- Is `niwa init [name]` completion valuable at all? The only useful case is re-cloning from registry; new names are invented. Probably offer registry names only, with a docstring caveat.
- Should `niwa create -r` complete from the workspace config's declared repos (parsing `workspace.toml` from the registry source)? Requires resolving the workspace name positional arg first to locate the config. Defer until there is a clear user request.
- Is there a performance budget (e.g., "under 50 ms p95") that forces us to cache enumeration results, or is raw filesystem I/O fine for realistic workspace sizes?

## Summary

Fourteen identifier positions exist across ten niwa commands, covered by exactly three data sources: the global registry map (`GlobalConfig.Registry` from `~/.config/niwa/config.toml`) for workspaces, `workspace.EnumerateInstances` + `LoadState` for instances, and a scan-two-levels-of-subdirs operation (currently inlined in `findRepoDir`) for repos. The main design decisions are how to handle flag-dependent completion (`niwa go -r` reads `-w`), the destructive-command footgun on `destroy`/`reset`, and whether to bother completing `init [name]` and `create -r` at all. The biggest open question is the UX policy on destructive-command completion; the biggest code cleanup is extracting a shared `EnumerateRepos` helper so completion doesn't copy the group-scanning logic a fourth time.
