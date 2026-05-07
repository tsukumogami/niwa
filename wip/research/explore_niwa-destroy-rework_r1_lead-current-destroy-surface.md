# Lead: Current destroy surface

## Findings

### Command entry

`internal/cli/destroy.go` (81 LOC) wires the cobra command `destroy [instance]`.

- Flag: `--force` (bool, default `false`) — "skip uncommitted changes check". Bound to package-level `destroyForce`.
- Args: `cobra.MaximumNArgs(1)` — accepts zero or one positional arg.
- ValidArgsFunction: `completeInstanceNames` (shared with reset/status/apply --instance).
- Long help today reads: "If no instance name is given, the current directory is used to discover the enclosing instance." This is the one-line semantics statement we'll be rewriting.

`runDestroy` flow (linear, no branching beyond the dirty check):

1. `os.Getwd()` → cwd.
2. `nameArg` from `args[0]` or empty.
3. `workspace.ResolveInstanceTarget(cwd, nameArg)` → `instanceDir`.
4. `workspace.ValidateInstanceDir(instanceDir)`.
5. If `!destroyForce`: `workspace.CheckUncommittedChanges(instanceDir)`. If any dirty repos, list them on stderr (`Repos with uncommitted changes:`, then sorted names), then return `instance has uncommitted changes in N repo(s); use --force to override`.
6. `workspace.TerminateDaemon(instanceDir)` — error is downgraded to a stderr warning (`warning: could not stop mesh daemon: %v`), never aborts destroy.
7. `workspace.DestroyInstance(instanceDir)`.
8. Print `Destroyed instance: %s\n` to stdout.

The command does **not** call `writeLandingPath` today. The shell-wrapper landing-path protocol is wired in `internal/cli/go.go`, `internal/cli/init.go`, and `internal/cli/session_lifecycle_cmd.go:81` (`niwa session destroy` does emit), but `niwa destroy` is silent on the protocol — meaning today, destroying the cwd's enclosing instance leaves the user's shell stuck in a deleted directory unless they manually `cd` out.

### Workspace helpers (with preservation contracts)

All four live in `internal/workspace/destroy.go` (138 LOC).

#### `ResolveInstanceTarget(cwd, nameArg string) (string, error)`

- If `nameArg != ""` → `resolveInstanceByName(cwd, nameArg)`.
- Otherwise → `DiscoverInstance(cwd)`, wrapping the error as `"resolving current instance: %w"`.

`resolveInstanceByName`:

- `config.Discover(cwd)` to find the workspace root (`workspaceRoot := filepath.Dir(configDir)`).
- `EnumerateInstances(workspaceRoot)`.
- Iterate, `LoadState(dir)`, match on `state.InstanceName == name`. First match wins.
- Error formats: `instance %q not found: no instances exist in workspace` when zero instances found, or `instance %q not found, available instances: a, b, c` when there are instances but none match.

**Callers**: `internal/cli/destroy.go:45` and `internal/cli/reset.go:50`. Two callers, both CLI commands that share the same name-or-cwd resolution semantics.

**Preservation contract**:

- The error string `instance %q not found, available instances: %s` is user-facing and likely matched in user habit/scripts; preserve verbatim for the named-arg case (which still applies in the rework when invoked from any cwd).
- The "named arg works from anywhere inside a workspace" behavior is shared with `reset` — the rework must keep this seam working for `reset`. If the rework restricts destroy-by-name to workspace-root-only, that restriction must NOT regress `reset`'s ability to resolve from anywhere.
- `config.Discover` walks UP from cwd, so a name arg supplied from inside an instance still resolves the workspace root and finds the named instance. The new design should preserve this for `reset`; for `destroy` it can either preserve or restrict.

**Seam for the rework**: `ResolveInstanceTarget` is one of two seams. Since `reset` also uses it, the rework should NOT change its signature. Instead, the new destroy entry point can call `resolveInstanceByName` or write its own resolution logic and bypass `ResolveInstanceTarget`. Alternatively, factor out a new helper (e.g., `ResolveInstanceByName`) that destroy and reset both call without the cwd-fallback branch.

#### `ValidateInstanceDir(dir string) error`

- Confirms `<dir>/.niwa/instance.json` exists → error: `not an instance directory: %s does not exist`.
- Confirms `<dir>/.niwa/workspace.toml` does NOT exist → error: `refusing to destroy workspace root: %s exists`.

**Callers**: `internal/cli/destroy.go:50`, `internal/cli/reset.go:55`, plus `DestroyInstance` itself (line 129).

**Preservation contract**: this is the "you cannot `rm -rf` your workspace root through destroy" guarantee. **CRITICAL invariant**: the new design adds a "wipe entire workspace under `--force`" branch — that path must be a separate code path that does NOT route through `ValidateInstanceDir(workspaceRoot)`, since the validator's whole purpose is to block exactly that. Keep `ValidateInstanceDir` strict; carve out a sibling helper for the workspace-self-destroy path. The error string `refusing to destroy workspace root` is asserted in `TestValidateInstanceDir_IsWorkspaceRoot` and `TestDestroyInstance_RejectsWorkspaceRoot`.

#### `CheckUncommittedChanges(instanceDir string) ([]string, error)`

- `LoadState(instanceDir)`, iterate `state.Repos`.
- Skip repos where `Cloned == false`.
- Skip repos whose on-disk dir doesn't exist (`os.IsNotExist`) — silently.
- For each remaining: `git -C <repoDir> status --porcelain`. Non-empty trimmed output → "dirty".
- Returns repo names (map keys) of dirty repos. Order is map-iteration order (caller sorts).
- Error contract: only fails on `LoadState` errors or `git status` exec failures. No git available means failure mode is exec error; tests `t.Skip` when git isn't on PATH.

**Callers**: `internal/cli/destroy.go:55`, `internal/cli/reset.go:60`. Same two CLI callers.

**Preservation contract**: The lead mentions a "comprehensive non-pushed-work scan" in the new design. The current check ONLY looks at uncommitted changes (`git status --porcelain`); it does NOT detect:
- Unpushed commits (no `git log @{u}..` check).
- Stashes (`git stash list`).
- Untracked-but-not-listed-by-status files? — actually `--porcelain` does include untracked files (`??` lines), so this is covered.
- Branches with no upstream.

The rework expands this scan, so `CheckUncommittedChanges` either grows or gets a sibling. If we extend it in place, watch the `reset` caller — reset users may not want a "you have unpushed commits" hard-block. Safer: introduce a new helper (e.g., `CheckLosableWork`) for destroy and leave `CheckUncommittedChanges` for reset.

#### `DestroyInstance(dir string) error`

- Re-validates via `ValidateInstanceDir` (defensive double-check).
- `os.RemoveAll(dir)`. Wraps error as `removing instance directory: %w`.

**Callers**: `internal/cli/destroy.go:74`, `internal/cli/reset.go:100` (reset destroys then recreates).

**Preservation contract**: The double-validate guards against a caller that bypassed the entry-point validator. Keep this. Note that `DestroyInstance` removes the dir but does NOT clean up the registry entry in the global config — a separate concern that may be relevant if "destroy whole workspace" is added.

### Tests (unit and functional)

#### `internal/workspace/destroy_test.go` (415 LOC)

Helpers: `destroySetupWorkspace` (wraps `setupWorkspace` from a sibling test file), `destroySetupInstance`, `initGitRepo`.

- **ResolveInstanceTarget**:
  - `TestResolveInstanceTarget_ByName` — happy path, two instances, resolve "alpha".
  - `TestResolveInstanceTarget_ByNameNotFound` — error mentions the requested name.
  - `TestResolveInstanceTarget_ByNameNoInstances` — error contains "no instances exist".
  - `TestResolveInstanceTarget_ByCwd` — nested dir inside instance resolves to instance.
  - `TestResolveInstanceTarget_ByCwdNotInInstance` — error when cwd has no enclosing instance.
- **ValidateInstanceDir**:
  - `TestValidateInstanceDir_Valid` — happy path.
  - `TestValidateInstanceDir_NotAnInstance` — error contains "not an instance".
  - `TestValidateInstanceDir_IsWorkspaceRoot` — directory with both `instance.json` and `workspace.toml` — error contains "workspace root".
- **CheckUncommittedChanges**:
  - `TestCheckUncommittedChanges_CleanRepos`, `_DirtyRepo`, `_SkipsNotCloned`, `_SkipsMissingDir`, `_MultipleRepos` — all skip when git absent.
- **DestroyInstance**:
  - `TestDestroyInstance_Success`, `_RejectsNonInstance`, `_RejectsWorkspaceRoot` (with explicit assertion that the workspace-root directory is NOT removed).

#### `internal/cli/destroy_test.go` (28 LOC)

Two minimal cobra-wiring tests:
- `TestDestroyCmd_HasForceFlag` — `--force` registered, default `"false"`.
- `TestDestroyCmd_AcceptsOptionalPositionalArg` — accepts 0 or 1 args, rejects 2.

No integration-style test in the cli package; full E2E coverage is functional only (and minimal — see below).

#### Functional features mentioning destroy

(Searched `test/functional/features/`.)

- **`completion.feature:57`** `@critical` — "destroy completes instance names of the current workspace". Sets up two instances `myws` and `myws-2`, runs completion for `destroy` with empty prefix, expects both names. **This is the only `niwa destroy` functional scenario.**
- **`mesh.feature`** — every other `destroy` mention (lines 450, 468, 472, 476, 478, 480, 481, 482, 483, 663, 712, 725, 728, 760, 770, 775, 784, 821, 828, 907, 921) refers to `niwa session destroy` or `niwa_destroy_session` MCP tool — a **different command** that destroys a mesh session inside an instance. These do NOT exercise `niwa destroy` and are not affected by the rework.
- **No scenario** in `critical-path.feature` exercises `niwa destroy`.

So the rework currently has thin functional coverage: one completion scenario and the unit tests in `destroy_test.go`. Per the workspace CLAUDE.md guidance ("When you ship a user-facing CLI command or fix a regression in the init → create → apply workflow, add a `@critical` Gherkin scenario"), this rework should land at least one `@critical` scenario in `test/functional/features/` covering the new contextual behavior.

### Daemon termination

`internal/workspace/daemon.go:118` defines `TerminateDaemon(instanceRoot string) error`.

Sequence:

1. **Always run first** (even if no daemon): `killRunningWorkerPGIDs(niwaDir)` — enumerates `.niwa/tasks/*/state.json`, finds tasks in `running` state with `worker.PID > 0`, sends `SIGKILL` to `-pid` (the entire process group). Errors silenced. **This is the Issue-8 acceptEdits-blast-radius hardening: workers get NO grace period.**
2. `ReadPIDFile(niwaDir)` — returns `(0, 0, nil)` if `daemon.pid` is missing. If `pid == 0`, return nil (no daemon running — clean exit).
3. `mcp.IsPIDAlive(pid, startTime)` — if false, remove `daemon.pid` and return nil.
4. `os.FindProcess(pid)` — if it errors, remove `daemon.pid` and return nil.
5. `proc.Signal(SIGTERM)` — if it errors, remove `daemon.pid` and return nil.
6. Poll up to `NIWA_DESTROY_GRACE_SECONDS` (default 5s) at 100ms intervals waiting for daemon exit.
7. If still alive: `SIGKILL`, then remove `daemon.pid`. Return nil.

**Idempotency**: yes — every "no daemon found" branch returns nil. The function never errors after step 1. The only error path is step 2 returning a non-nil err on `daemon.pid` read failure.

**Behavior with missing instance state file**: `ReadPIDFile` walks `.niwa/daemon.pid`, not `instance.json`, so a missing instance state file does NOT affect `TerminateDaemon`. The `killRunningWorkerPGIDs` walk targets `.niwa/tasks/`; if the directory is missing, `os.ReadDir` errors and the function returns silently.

**What if the instance dir itself is gone**: `killRunningWorkerPGIDs` swallows the dir-missing error; `ReadPIDFile` returns `(0, 0, nil)` for `os.IsNotExist`. So calling `TerminateDaemon` against a path that no longer exists is safe and returns nil.

**Tests** in `internal/workspace/daemon_test.go`:
- `TestTerminateDaemon_SigKillsWorkersFirst` — SIGKILL bypasses grace.
- `TestTerminateDaemon_SkipsNonRunningTasks` — completed tasks not killed.
- `TestTerminateDaemon_DaemonGraceStillHonored` — daemon gets full grace before SIGKILL.
- `TestTerminateDaemon_WorkerKilledBeforeDaemonGrace` — ordering invariant.
- `TestTerminateDaemon_NoDaemonPresent` — clean nil return when no daemon.

**Other callers**: `internal/cli/session_lifecycle_cmd.go:58,96` and `internal/cli/mcp_serve.go:32` — these install `TerminateDaemon` as a callback for the session-lifecycle MCP server, NOT as a direct call. They're insulated from any change in destroy.go's call site.

**Preservation contract**: idempotent, never errors fatally, swallow-and-continue is the contract. The new control flow can call `TerminateDaemon` at multiple points (e.g., once per instance in workspace-wipe mode) without coordination concerns. The 5s grace per call is a real cost — wiping N instances will take up to 5N seconds in the worst case unless we parallelize.

### Other callers (helper sharing)

| Helper | Callers |
|---|---|
| `ResolveInstanceTarget` | `cli/destroy.go:45`, `cli/reset.go:50` |
| `ValidateInstanceDir` | `cli/destroy.go:50`, `cli/reset.go:55`, `workspace/destroy.go:129` (self-call from `DestroyInstance`) |
| `CheckUncommittedChanges` | `cli/destroy.go:55`, `cli/reset.go:60` |
| `DestroyInstance` | `cli/destroy.go:74`, `cli/reset.go:100` |
| `TerminateDaemon` | `cli/destroy.go:70`, plus three indirect callers (`session_lifecycle_cmd.go`, `mcp_serve.go`) that install it as a callback |

**Implication**: Every helper destroy uses is also used by `niwa reset`. Reset and destroy share an entire resolution-and-validation pipeline. The rework cannot modify the helpers' signatures without updating reset, and modifying their semantics changes reset's behavior too. Safest path: leave helpers alone, build the new destroy on top of them, possibly add new sibling helpers for the new behaviors.

### Completion

`internal/cli/completion.go:35` defines `completeInstanceNames`. It:

1. `os.Getwd()`.
2. `config.Discover(cwd)` — walks UP looking for the workspace root.
3. `workspace.EnumerateInstances(workspaceRoot)`.
4. For each, `workspace.LoadState(dir)`, filter via `workspace.ValidName`, accumulate `state.InstanceName`.
5. Sort, prefix-filter against `toComplete`, return.

**Crucially**: `config.Discover` walks UP, so completion works from anywhere inside the workspace — both from the workspace root AND from inside any instance / nested dir. It does NOT special-case "you must be at the workspace root".

If the new design restricts destroy-by-name to workspace-root-only (i.e., naming an instance from inside another instance is no longer valid), then `completeInstanceNames` will offer names that the new destroy will refuse. Two options:

1. **Keep completion as-is** and let the runtime error be the teaching moment.
2. **Make completion context-aware**: when invoked from inside an instance, the new destroy treats the positional name as ignored or invalid → completion should return empty (or only the enclosing instance's own name).

The completion function is shared across `destroy`, `reset`, `status`, and `apply --instance` (per its own docstring). Changing its behavior affects all four. **Recommendation**: don't touch `completeInstanceNames`; if the new destroy forbids name-from-inside-instance, the runtime error message can guide the user. A destroy-specific completion variant is only worth it if we want a great UX here.

### Discovery helpers

#### `workspace.DiscoverInstance(startPath string) (string, error)` (`internal/workspace/state.go:326`)

Walks UP from `startPath` looking for `.niwa/instance.json` (the function uses `statePath(dir)` which is `.niwa/instance.json`). Returns the directory containing the marker. Errors `no instance found walking up from %s`. Resolves to absolute path first.

**Callers**: `cli/completion.go:97,161`, `cli/go.go:144,196`, `cli/status.go:101`, `cli/status_audit.go:93`, `cli/status_audit_auth.go:36`, `cli/status_check_vault.go:44`, `workspace/destroy.go:25` (via `ResolveInstanceTarget`), `workspace/preflight.go:90`, `workspace/scope.go:47`. Heavily used.

#### `config.Discover(startDir string) (configPath, configDir string, err error)` (`internal/config/discover.go:18`)

Walks UP from `startDir` looking for `.niwa/workspace.toml`. Returns the absolute path to the file AND the absolute path to the config dir (`.niwa/`). The workspace root is `filepath.Dir(configDir)` (this idiom appears in `resolveInstanceByName`, `resolveNamed`, `completeInstanceNames`, `ResolveApplyScope`).

Error: `no %s/%s found in any parent of %s`.

**For "destroy from inside" detection**: the rework needs both helpers. Algorithm:

1. `config.Discover(cwd)` — find the workspace root (walks up, succeeds anywhere inside the workspace).
2. `workspace.DiscoverInstance(cwd)` — find the enclosing instance, IF cwd is inside one. Compare its result with the workspace root — they'll differ when cwd is inside an instance (instance dir is a child of the workspace root); they'd be conceptually equal when cwd is at the workspace root, but `DiscoverInstance` only succeeds if it finds `instance.json` walking up, and the workspace root has `workspace.toml` not `instance.json`, so `DiscoverInstance` will error from the workspace root. **This is the seam for "are we at the workspace root vs. inside an instance".**

`workspace.ResolveApplyScope` (`internal/workspace/scope.go:41`) already implements this exact discriminator: try `DiscoverInstance` first → `ApplySingle`; fall back to `config.Discover` → `ApplyAll`. The new destroy should mirror this structure (and arguably reuse it — `ApplyScope` has a `Mode` enum already).

### Recent commits

`git log --oneline -- internal/cli/destroy.go internal/workspace/destroy.go internal/workspace/destroy_test.go` returns three commits:

- `0f5f530` — initial implementation in v0.1: "feat: implement niwa v0.1 -- workspace config, init, create, apply, status, reset, destroy (#5)". This is the only commit that authored these files; everything we see today shipped in that one PR.
- `f65ebc6` — "feat(cli): add contextual tab-completion (#50)". Added `ValidArgsFunction = completeInstanceNames`.
- `6d75a43` — "feat(channels): add cross-session communication via filesystem session mesh (#71)". Added the `workspace.TerminateDaemon` call in `runDestroy` (the daemon-shutdown step).

No commits to these files in the last 6 months. The destroy surface has been remarkably stable; the only changes since v0.1 are the completion wiring and the daemon-termination call. **This means the rework is essentially the first significant evolution of destroy since its inception.**

### One-time notices

`docs/guides/one-time-notices.md` describes a per-instance-state mechanism: `InstanceState.DisclosedNotices []string`, helpers `noticeDisclosed` and `mergeDisclosedNotices`. Notices are emitted from inside `runPipeline` (apply.go) and persisted on the next `SaveState`.

**Relevance to destroy rework**: limited. The notice mechanism is **per-instance**, persisted in the instance's `instance.json` — but destroy *removes* the instance, so a "destroy hint" notice would be wiped immediately. To inform the user once per workspace about the new picker UX, we'd need a different storage location (the workspace's `.niwa/` config dir, or `~/.config/niwa/` global state). The existing one-time-notice plumbing does NOT fit. Out of scope.

If we want a "first-time destroy from workspace root" hint, the cleanest path is just printing it unconditionally on the picker screen — it's not noisy because the picker is interactive and only appears in that explicit codepath.

## Implications

**What we can rewrite freely:**
- `internal/cli/destroy.go` — all of it. The cobra wiring (`destroyCmd` + `init` + `runDestroy`) is the only `niwa destroy` user.
- `runDestroy`'s control flow — no other code path depends on its sequencing.
- The destroy-specific tests in `internal/cli/destroy_test.go` — minimal, only assert flag/args wiring.

**What we must preserve:**
- `ResolveInstanceTarget`, `ValidateInstanceDir`, `CheckUncommittedChanges`, `DestroyInstance` signatures and semantics — `niwa reset` shares all four. Modify behavior only via new sibling helpers.
- `ValidateInstanceDir`'s "refuses to destroy a workspace root" invariant — this is the safety net for both destroy and reset. The new "wipe the entire workspace" path must NOT route through this validator; it needs its own well-named helper (e.g., `DestroyWorkspace(workspaceRoot)`) with its own safety checks (registry-entry cleanup, confirmation prompt, idempotency on partial failure).
- `TerminateDaemon` idempotency — the rework can call it once per instance in workspace-wipe mode; safe but slow (5s grace each).
- The `--force` flag's existing meaning ("skip uncommitted changes check"). The lead suggests `--force` will additionally enable workspace-wipe-from-root; that's an additive expansion, not a breaking change, but the help text must say so.
- `Destroyed instance: %s` stdout message format — likely consumed by user scripts.
- `instance has uncommitted changes in N repo(s); use --force to override` error format and the `Repos with uncommitted changes:\n  %s\n` stderr listing.

**Seams for the new control flow:**

- **"Where am I?" discriminator**: `workspace.DiscoverInstance(cwd)` succeeds iff inside an instance; `config.Discover(cwd)` succeeds iff inside (or at) a workspace. Combine to classify cwd as: (a) at workspace root, (b) inside an instance, (c) outside any niwa workspace. Mirror `ResolveApplyScope` here.
- **Picker reuse**: a sibling research note (`explore_niwa-destroy-rework_r1_lead-tsuku-picker-reuse.md`) likely covers this. The new destroy needs a picker only in the (a) "at workspace root, no name arg" case.
- **Landing-path emit**: `writeLandingPath` is package-`cli` private. The shell-wrapper protocol is already wired for `niwa go`, `niwa init`, and `niwa session destroy`. Reuse it: when the destroyed instance contains cwd, emit the workspace root (or its parent) as the landing path so the wrapper drops the user out of the deleted directory.
- **Comprehensive non-pushed-work scan**: introduce a new helper alongside `CheckUncommittedChanges` so we can extend the check (unpushed commits, stashes, etc.) for destroy without changing reset's behavior.

**Recommended new structure**:

```
internal/workspace/destroy.go  (existing, untouched)
internal/workspace/destroy_workspace.go  (new: DestroyWorkspace, CheckLosableWork, classifyCwd)
internal/cli/destroy.go  (rewritten: contextual control flow, picker, landing-path emit)
```

## Surprises

- **Destroy and reset are siamese twins**: every destroy helper has exactly one other caller, and that caller is reset, with identical sequencing. The "rework destroy" project has a hidden second product question: should reset get the same contextual treatment? If yes, factor the new behavior into shared helpers; if no, the divergence will calcify the seams. **Worth flagging in crystallize.**
- **`niwa destroy` does not currently use the landing-path protocol** despite operating on directories that may contain cwd. This is arguably a bug today — running `niwa destroy` from inside the instance you're destroying leaves your shell in a deleted directory. The rework will fix this implicitly.
- **`mesh.feature` is loaded with `destroy`-named scenarios that have NOTHING to do with `niwa destroy`** — they exercise `niwa session destroy` / `niwa_destroy_session`. Easy to mistake for coverage; verify when grepping.
- **The `Long` help string is the only place** that documents the cwd-fallback behavior. There's no end-user docs/guides entry for destroy. The help string is therefore load-bearing for user-facing semantics.
- **No commits in 6 months** — the surface is mature and untouched, so latent assumptions are likely undocumented and we should rely on tests as the spec.
- **`config.Discover` returns `configDir` (the `.niwa/` dir), not the workspace root**; you have to `filepath.Dir(configDir)` to get the root. This idiom is repeated in 4+ places — easy to get wrong on a fresh write.

## Open Questions

1. **Does the rework apply to reset too?** Reset shares the entire helper pipeline. If destroy gets a picker and contextual semantics, should reset?
2. **Workspace-wipe from root with `--force`**: should it also remove the registry entry from `~/.config/niwa/config.toml`? Today `DestroyInstance` only removes the instance dir, not the registry. Removing a workspace ought to remove its registry entry too, but we should confirm.
3. **Picker UX when there's only one instance**: skip and proceed, or always show the picker?
4. **Picker UX from inside an instance**: lead says "destroying the enclosing instance from inside" — does that imply NO picker is shown when inside an instance, even with no name arg? (Current behavior is "destroy enclosing instance silently".) If we want symmetric explicitness, a confirmation prompt for the inside-an-instance case may be warranted.
5. **`--force` semantics broadening**: today `--force` skips the dirty check. The new design seems to add "from root, also wipe workspace". Is `--force` overloaded, or do we want a distinct `--workspace` / `--all` flag for the wipe path?
6. **Comprehensive non-pushed-work scan scope**: just unpushed commits + stashes, or also untracked files (already in porcelain), unstaged worktree state in nested worktrees from the mesh feature, etc.?
7. **Should the new destroy honor `NIWA_RESPONSE_FILE` for the inside-an-instance case only**, or also emit a landing path on the picker / workspace-wipe path? (If you wipe the workspace and your shell was inside it, you still need to be dropped out.)
8. **Functional-test `@critical` scenarios to add**: at minimum, "destroy from inside instance drops shell at workspace root", "destroy from workspace root with name arg works", "destroy from workspace root --force wipes workspace". What else is `@critical` vs. nice-to-have?

## Summary

Scope is **medium**: the destroy-specific surface is small (one cobra command, ~140 LOC of helpers, 415 LOC of unit tests, one functional scenario, all authored in the v0.1 megacommit and untouched since), but every helper is shared with `niwa reset`, so the rework is constrained to additive helpers and a rewritten cli/destroy.go rather than in-place edits. **Key invariants to preserve**: `ValidateInstanceDir`'s "refuses to destroy a workspace root" guard (the new workspace-wipe path must bypass it via a sibling helper, not loosen it); `TerminateDaemon` idempotency and its 5s default grace; the `Destroyed instance: %s` and `instance has uncommitted changes in N repo(s); use --force to override` user-facing strings; reset's continued use of the four helpers with unchanged semantics. **Biggest risk**: silently changing reset's behavior by editing shared helpers in place — every modification to `destroy.go` (the workspace package) and `state.go` discovery must be evaluated against the reset call sites, and the rework's "comprehensive non-pushed-work scan" should land as a new helper rather than an extension of `CheckUncommittedChanges`.
