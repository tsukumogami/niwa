# Lead: Where could setup scripts hook into the worktree lifecycle, and what does each candidate point already have in hand?

Source read: worktree at `.claude/worktrees/niwa-explore`, branch `docs/worktree-setup-scripts`, `d25ad4d`.

## Findings

### 1. The CLI surface

`niwa worktree` is one cobra parent command whose Go variable is still called
`sessionCmd` (`internal/cli/session.go:25`, `Use: "worktree"`, `Aliases:
{"session"}`). Registrations are spread over four files:

| Subcommand | Registered | Command var | RunE |
|---|---|---|---|
| `list` | `session.go:13` | `sessionListCmd` (`session.go:70`) | `runSessionList` (`session.go:111`) |
| `create` | `session_lifecycle_cmd.go:21` | `sessionCreateCmd` (`:42`) | `runSessionCreate` (`:133`) |
| `apply` | `session_lifecycle_cmd.go:22` | `sessionApplyCmd` (`:26`) | `runSessionApply` (`:241`) |
| `destroy` | `session_lifecycle_cmd.go:23` | `sessionDestroyCmd` (`:67`) | `runSessionDestroy` (`:501`) |
| `from-hook` | `session_from_hook_cmd.go:18` | `sessionFromHookCmd` (`:33`, `Hidden: true`) | `runSessionFromHook` |
| `attach` | `session_attach_register.go:9` | `sessionAttachCmd` (`:22`) | — |
| `detach` | `session_attach_register.go:10` | `sessionDetachCmd` | — |

**`worktree create` — exact order of operations** (`internal/cli/session_lifecycle_cmd.go:133-232`):

1. `:141` `resolveInstanceRoot()` — env `NIWA_INSTANCE_ROOT` or walk up for `.niwa/instance.json` (`session.go:114`, `:128`).
2. `:145-163` repo resolution (positional, else `workspace.ResolveRepoNameFromCwd`).
3. `:169-172` purpose (positional, else `defaultSessionPurpose = "session"`, `:112`).
4. `:175` **`worktree.CreateSession(...)`** → `internal/worktree/worktree.go:165`. Inside it, in order:
   - `:187` `findRepoInWorkspace` (repo must be a clone with `.git`)
   - `:194-201` mkdir `.niwa/sessions`, mint session id
   - `:203-206` mkdir `.niwa/worktrees`, compute `wtPath = <instanceRoot>/.niwa/worktrees/<repo>-<sid>`, branch `session/<sid>` (or `params.BranchPrefix + sid`)
   - `:214` **`git -C <repoPath> worktree add <wtPath> -b <branch>`**
   - `:220-224` `cleanupWorktree` closure defined — every failure from here removes the worktree
   - `:227` `scaffoldWorktreeNiwa(wtPath)` (creates only `.niwa/sessions/`)
   - `:236` `gitexclude.EnsureRepoExclude(wtPath)`
   - `:243-247` **`WriteSessionLifecycleState`** (state written last, after the worktree exists)
5. `:194` **`applyContentToWorktree(instanceRoot, worktreePath, repo, purpose, branch)`** (`:298`) → config discover/load, `FindRepoGroup`, `resolveWorktreeDelegation` (a bounded 5s `claude --version` probe, `:745`/`:783`), `mergeWorktreeOverlay`, `loadGlobalConfigOverride`, then `:366` **`workspace.ApplyToWorktree(...)`** (`internal/workspace/worktree_content.go:525`).
6. `:202-221` output: `--json` object, or `session: created <sid> at <path>` + one `session: content <file>` line per written file.
7. `:223` `validateLandingPath`, `:226` `writeLandingPath` — writes the path to `NIWA_RESPONSE_FILE`.
8. `:230` `hintShellInit`.

So: git worktree exists at step 4 (`worktree.go:214`); session state is durable at the end of step 4; content lands in step 5; the shell only ever `cd`s **after the process exits** (step 7 just writes the file the wrapper reads).

**`worktree apply`** (`:241-282`): `resolveInstanceRoot` → `worktree.ReadSessionLifecycleState(sessionsDir, sid)` (`:254`) → reject terminal status (`:258`) → reject empty `WorktreePath` (`:265`) → `applyContentToWorktree(...)` at `:274` → print. No `CreateSession`.

**Other callers of the same content helper** — there are four, and any hook placed inside `ApplyToWorktree` fires on all of them:
- `session_lifecycle_cmd.go:194` (`worktree create`)
- `session_lifecycle_cmd.go:274` (`worktree apply`)
- `session_from_hook_cmd.go:144` (Claude's `WorktreeCreate` hook)
- `cli/apply.go:306` (`niwa apply` run with a worktree scope — `runApplyWorktreeScope`)
- plus `workspace/apply.go:2433`, which calls `workspace.ApplyToWorktree` directly from Step 6.6.

**What `ApplyToWorktree` already does, in order** (`worktree_content.go:525-717`): 1 repo content, 1b plugin skills, 1c MCP/payload config, 2/2b materializers + env inherit (gated), 3 rules import (gated), 4 worktree-context layer, then `gitexclude.EnsureRepoExclude`, then **step 5 at `:712` `runWorktreeHooks(configDir, worktreePath, repo, purpose, branch, opts.Stderr)`** — described in its own comment (`:707-711`) as "Analog of the instance setup-script run". That is the last thing it does before returning.

### 2. What each candidate hook point has in hand

`RunSetupScripts(repoDir, setupDir string, r *Reporter, red *secret.Redactor)` (`internal/workspace/setup.go:53`). `r` must be non-nil — `runCmdWithReporter` (`gitutil.go:140`) calls `r.Log` unconditionally at `:171` and `r.Warn` at `:176`. `red` is nil-tolerant (`gitutil.go:167`). `ResolveSetupDir(ws *config.WorkspaceConfig, repoName string)` (`setup.go:33`) needs only the config and the repo name.

| Candidate | cfg | Reporter | Redactor | worktree path | repo | group | instance root |
|---|---|---|---|---|---|---|---|
| inside `worktree.CreateSession` | **no** | **no** | **no** | yes (`wtPath`) | yes (`params.Repo`) | **no** | yes |
| inside `workspace.ApplyToWorktree` | yes (`cfg`) | **no** (only `opts.Stderr io.Writer`) | **no** (constructible) | yes | yes | yes | yes |
| CLI `worktree create`, after ApplyToWorktree | **not in scope** (local to `applyContentToWorktree`) | **no** | **no** | yes | yes | **no** | yes |
| CLI `worktree apply` | same as above | **no** | **no** | yes (from state) | yes | **no** | yes |
| new fan-out in `apply.go` after Step 6.75 | yes (`effectiveCfg`) | yes (`a.Reporter`) | **yes** (`redactor`, `apply.go:804`) | via session enumeration | yes | yes (`repoGroups`) | yes |

Detail per row:

- **`worktree.CreateSession`** (`internal/worktree/worktree.go:165`). The package is a documented leaf — package doc at `worktree.go:1-6` says it "imports neither internal/mcp nor internal/workspace", and its import block (`:9-19`) is stdlib plus `internal/gitexclude`. `workspace` imports `worktree`, so calling `workspace.RunSetupScripts` from here is an import cycle. It also has no config at all: `CreateSessionParams` carries `InstanceRoot`, `Repo`, `Purpose`, `ParentSessionID`, `BranchPrefix`, `GitInvoker`. Ruled out without an injected-closure seam (the `workspace.CreateSessionFunc` pattern at `bootstrap.go:55` inverted).

- **`workspace.ApplyToWorktree`** (`worktree_content.go:525`). Signature is `(cfg *config.WorkspaceConfig, configDir, instanceRoot, worktreePath, group, repo, purpose, branch string, opts WorktreeApplyOptions)`. `ResolveSetupDir(cfg, repo)` works verbatim. The clone dir is already computed here — `cloneRepoDir := filepath.Join(instanceRoot, group, repo)` at `worktree_content.go:781`. Same package as `RunSetupScripts`, no cycle. Two gaps: no `*Reporter` (only `opts.Stderr io.Writer`, which `runWorktreeHooks` uses at `:1064-1067` and writes raw child stdout/stderr to at `:1097-1098`), and no `*secret.Redactor`. Both are constructible here — `NewReporter(w io.Writer)` (`reporter.go:41`) infers TTY from `*os.File`, and `readCloneEnvOutput` (`worktree_content.go:379`) already returns the clone's resolved env as `map[string]string`, which is exactly the fragment set a `secret.NewRedactor()` would need `Register`ing.

- **CLI `worktree create` after `ApplyToWorktree` returns** (`session_lifecycle_cmd.go:194`). At `:195` the frame holds `instanceRoot`, `worktreePath`, `repo`, `purpose`, `branch`, `sessionID`, `written`. It does **not** hold `cfg`, `configDir`, or `group` — those are locals of `applyContentToWorktree` (`:299-308`) and are discarded on return. Adding a setup run here means either re-loading config (a fifth config-read site the comment at `:290-296` explicitly warns against) or widening `applyContentToWorktree`'s return.

- **CLI `worktree apply`** (`:241`): identical shape, plus `state` (a `worktree.SessionLifecycleState`). Same missing cfg/group.

- **New fan-out in `apply.go` right after Step 6.75.** This is the richest point. In `runPipeline`'s frame at `apply.go:1951-1980` you have `effectiveCfg`, `configDir`, `instanceRoot`, `a.Reporter`, `redactor` (built at `:804`, deliberately as the pipeline's first statement so setup scripts see a complete fragment set — see the comment at `:789-803`), `classified`, `repoGroups` (built at `:1928-1931`), `repoIndex`, `overlayDir`, `now`. Everything `RunSetupScripts` and `ResolveSetupDir` want, plus the enumeration inputs Step 6.6 uses.

### 3. The Step 6.6 machinery, in detail

`worktreeRefreshInputs` is `apply.go:2317-2347`; `refreshWorktreeEnvs` is `apply.go:2372-2465`. Call site `apply.go:1932`.

**Enumeration.** It reads **session state files, not `git worktree list` and not the `.niwa/worktrees` directory**: `sessionsDir := filepath.Join(in.instanceRoot, StateDir, "sessions")` then `worktree.ListSessionLifecycleStates(sessionsDir)` (`apply.go:2378-2379`; the function is `internal/worktree/session_lifecycle.go:116`). An enumeration error is non-fatal — `DeferWarn` and refresh nothing (`:2385-2386`).

**Per-session inclusion pipeline** (`:2394-2440`), in order:
1. Out of scope — `s.Status != worktree.SessionStatusActive`, or repo missing from `in.repoGroups` / `in.repoIndex`: skipped **silently** (`:2397-2403`). Comment: "a removed repo is not an edge state."
2. Missing directory — `os.Stat(wtPath)` fails: `DeferWarn("worktree %s (repo %s) directory is missing; skipping env refresh")`, no forward-carry (`:2409-2411`).
3. Locked — `worktree.ReadAttachState(wtPath, false)` returns `worktree.AttachAttached`: `DeferWarn(... "is attached (locked) by another process")` and forward-carry (`:2416-2419`). `reapStale` is hard-coded false; the comment at `:2415` says apply must never reap another process's lock.
4. Detached — `gitRegistered(cloneDir, wtPath)` false: `DeferWarn(... "is not registered with git (detached)")` and forward-carry (`:2422-2427`). `cloneDir := filepath.Join(in.instanceRoot, group, s.Repo)` at `:2423`. The default implementation is `gitRegistersWorktree` (`apply.go:2504`), which shells out via `listWorktrees(cloneDir)` (`internal/workspace/scan.go:380`, `git worktree list --porcelain`) and treats any git failure as "not registered". It is injectable through `in.gitRegistered` for tests (`:2341-2344`).
5. Included → `ApplyToWorktree(...)` at `:2433`. A per-worktree failure is `DeferWarn`ed and forward-carried, never fatal (`:2445-2448`).

**Reporting** is entirely `a.Reporter.DeferWarn` — queued and printed by `FlushDeferred` after the summary (`reporter.go:158`, `:165`).

**Is the enumeration reusable?** No. It is **inline inside `refreshWorktreeEnvs`** — the loop, the four guards, and the `ApplyToWorktree` call are one function body with no extracted "list eligible worktrees" helper anywhere in the package. A second fan-out step would need that loop extracted (something like `eligibleWorktrees(instanceRoot, repoGroups, repoIndex, gitRegistered) ([]sessionWithGroup, []warning)`), or it would duplicate all four guards. The forward-carry logic (`priorManagedFiles` `:2470`, `forwardCarry` `:2481`, `pathUnder` `:2494`) is managed-file bookkeeping and is not needed by a setup-script fan-out.

### 4. Shell navigation and blocking

The protocol is a temp file, not stdout (`docs/designs/current/DESIGN-shell-navigation-protocol.md`, "Option D"). The wrapper `mktemp`s a file, exports `NIWA_RESPONSE_FILE`, **runs niwa to completion**, then reads the file and `cd`s (`internal/cli/shell_init.go:38-50`):

```sh
NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"
__niwa_rc=$?
__niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)
rm -f "$__niwa_tmp"
if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
    builtin cd "$__niwa_dir" || return
fi
```

**Any work inside the command sits before the handoff, unconditionally.** There is no way to hand the user in early and keep working: the `cd` happens in the parent shell after the child exits. And the `cd` is gated on `$? -eq 0`, which the setup-script design already reasoned about for instances — DESIGN-post-clone-scripts.md:536-538: "The shell wrapper's `cd` into a new instance is gated on exit 0, so a fatal setup failure would strand the operator outside the very instance they need to enter to fix the script." The same argument transfers directly to a worktree.

**Precedent for a bounded blocking subprocess on this exact path: yes.** `resolveWorktreeDelegation` (`session_lifecycle_cmd.go:745`) runs `claude --version` on every worktree create and apply, bounded by `worktreeProbeTimeout = 5 * time.Second` (`:783`). Its comment (`:768-771`) is the relevant precedent: "it runs on every worktree create and apply — including inside a hook subprocess — so a `claude` wrapper that hangs would otherwise block worktree creation until the harness's own hook timeout fires."

**Precedent for backgrounding: only in `dispatch`**, which is a different shape — `dispatch_launcher.go:284` `cmd.Start()` for a detached worker, behind an explicit `--detach` flag (`dispatch.go:31`). Nothing in the worktree path backgrounds anything.

**Two blocking constraints unique to the worktree path:**
- The `WorktreeCreate` hook entry niwa writes carries **no `timeout` field** (`materialize.go:795-806` writes only `{"type": "command", "command": ...}`), unlike the SessionStart/SessionEnd entries which set `"timeout": sh.TimeoutSeconds` (`materialize.go:882`) from `rootSessionHookTimeoutSeconds = 180` (`root_materializer.go:62`, chosen "large enough to absorb `niwa create`'s clone + vault cost"). So `niwa worktree from-hook` inherits the harness's default hook timeout. A multi-second setup run on that path risks the agent's worktree creation timing out.
- **`niwa worktree create` is not cd-wrapped at all.** `shell_init.go:52-66` wraps `create|destroy|go|init` and, nested, `session` → `create`. The string `worktree` does not appear anywhere in `internal/cli/shell_init.go`. So the canonical `niwa worktree create` never triggers `__niwa_cd_wrap`; only the deprecated `niwa session create` alias does. (Verified: `grep -n worktree internal/cli/shell_init.go` returns nothing.)

### 5. Existing flags and config surface

**`niwa worktree create` takes exactly one flag: `--json`** (`session_lifecycle_cmd.go:117`). `destroy` has `--force` and `--by-path` (`:115-116`); `apply` has none; `list` has `--repo/--status/--attached/--available/--json` (`session.go:105-109`). Only the root-level persistent `--no-progress` (`root.go:61`) applies globally.

**`--no-*` opt-out precedent is well established elsewhere**, all as `BoolVar` on the command: `--no-pull`, `--no-install-plugins`, `--no-cascade` (`apply.go:23,28,30`), `--no-install-plugins` (`create.go:21`), `--no-overlay`, `--no-worktree-delegation`, `--no-ephemeral-sessions`, `--no-bootstrap` (`init.go:48-54`). Note that `--no-worktree-delegation` and `--no-ephemeral-sessions` are *persisted into instance state* (`state.go:104-117`) rather than being per-invocation — that is the precedent for a durable opt-out.

**Config schema for `setup_dir`:**
- Workspace level: `WorkspaceMeta.SetupDir string` with `toml:"setup_dir,omitempty"` (`internal/config/config.go:328`) — plain string, empty means unset.
- Per-repo override: `RepoOverride.SetupDir *string`, `toml:"setup_dir,omitempty"` (`internal/config/config.go:473`) — a **pointer**, so "absent" and "explicitly empty" are distinguishable.
- `ResolveSetupDir` (`setup.go:33-41`): repo override if the pointer is non-nil (**including `""`, which means disabled**) → workspace `SetupDir` if non-empty → `defaultSetupDir = "scripts/setup"` (`setup.go:14`). An empty *workspace* value falls through to the default; only a per-repo `setup_dir = ""` disables.
- `RunSetupScripts` maps `setupDir == ""` to `result.Disabled = true` and returns immediately (`setup.go:60-63`).

There is **no** worktree-specific setup config key anywhere in `internal/config/`.

### 6. Error posture

`worktree.CreateSession` is atomic past `git worktree add`: the `cleanupWorktree` closure (`worktree.go:220-224`) runs on scaffold failure (`:228`), git-exclude failure (`:237`), and state-write failure (`:244`).

The CLI layer **does the opposite** after `ApplyToWorktree` fails — it retains the worktree and tells the user (`session_lifecycle_cmd.go:188-197`):

```go
	// Install the owning repo's CLAUDE content (and the worktree-context
	// rules import + purpose/branch layer) into the new worktree, so a
	// worktree ends up with the same class of accessories a repo checkout
	// gets from `niwa apply`. The worktree already exists at this point; an
	// install failure is surfaced but does not unwind the worktree (it can
	// be re-synced later).
	written, err := applyContentToWorktree(instanceRoot, worktreePath, repo, purpose, branch)
	if err != nil {
		return fmt.Errorf("niwa: error: installing content into worktree %s (the worktree exists; re-sync it later): %w", sessionID, err)
	}
```

Returning an error here means `validateLandingPath`/`writeLandingPath` (`:223`, `:226`) never run and the exit code is non-zero — so even under the `session create` alias the user is **not** `cd`'d in.

The hook path deliberately diverges. `runFromHookCreate` (`session_from_hook_cmd.go:144-157`) calls `reconcileFailedHookCreate`, which runs `DestroySession` with `force=false` — a full rollback in the normal case. Its comment (`:145-156`) states the reasoning: "runSessionCreate ... retains and tells. A human is standing at the terminal ... An agent never enters the directory on this path and will not come back to it."

For comparison, the instance path treats a setup failure as **data, never an error** (`apply.go:1951-1980`): the comment at `:1952-1954` says "A script failure is carried out as data (setupIncomplete) and never returned as an error: every repo gets its turn, and the pipeline's error path must not be reached, since on create it deletes the instance root." It surfaces as `a.Reporter.DeferWarn` per script plus `logSetupIncomplete` (`apply.go:400`), a plain `Log` placed between the summary and the deferred block.

`runWorktreeHooks`, by contrast, **is** fatal: any non-zero exit returns an error from `ApplyToWorktree` (`worktree_content.go:1105-1107`), which on the create path becomes the retained-worktree error above and on the hook path becomes a full rollback.

## Implications

The natural hook point is **inside `ApplyToWorktree`, immediately before or after step 5's `runWorktreeHooks`** (`worktree_content.go:712`). It is the only place that already holds `cfg`, `repo`, `group`, `instanceRoot`, and `worktreePath` together, it is in the same package as `RunSetupScripts` and `ResolveSetupDir`, and its own comment already frames step 5 as the setup-script analog. The cost is that it fires on all five callers — including `worktree apply`, `niwa apply --worktree`, and Step 6.6's per-apply fan-out over every live worktree. If setup is expensive, that turns every `niwa apply` into an N-worktree setup storm unless the fan-out path is gated.

The two things `ApplyToWorktree` lacks are both closeable at that point rather than by threading through five callers: a `*Reporter` can be built from `opts.Stderr` with `NewReporter`, and a `*secret.Redactor` can be populated from `readCloneEnvOutput`'s already-computed map. Threading a Reporter through `WorktreeApplyOptions` is the alternative and would let the apply pipeline pass `a.Reporter` so worktree setup output joins the apply's own stream.

A **new fan-out step in `apply.go` after Step 6.75** is the point with the best inputs (Reporter and the pipeline's redactor both in scope) and the best failure semantics (setup-as-data is already the established posture there). But Step 6.6's enumeration is inline in `refreshWorktreeEnvs` with four guards and no extracted helper, so this option costs an extraction first. It also only covers apply — it does nothing for `worktree create`, which is where a fresh dependency-free worktree is actually born.

The CLI-level candidates are the weakest: neither `create` nor `apply` holds `cfg` or `group` after `applyContentToWorktree` returns, and the comment at `session_lifecycle_cmd.go:290-296` explicitly resists adding another config-read site.

On blocking: there is no early-handoff option. The `cd` is a post-exit shell operation, so every second of setup is a second the user waits at a blank prompt before landing. The instance path already accepted that trade (a `niwa create` blocks on clone + vault + setup) and already decided the exit code must stay 0 precisely so a failed script does not strand the operator outside the directory they need. The tighter constraint is the hook path, whose `WorktreeCreate` entry carries no `timeout` field at all — the agent-facing path is the one where a long setup run breaks something rather than merely being slow.

On error posture: the three existing worktree callers already disagree with each other (create retains, from-hook rolls back, Step 6.6 warns and continues), and the instance setup step is warn-and-continue. Matching `RunSetupScripts`'s existing non-fatal contract keeps all three consistent; matching `runWorktreeHooks`'s fatal contract would make a repo's own `npm install` able to roll back an agent's worktree.

## Surprises

- **`niwa worktree create` gets no shell auto-cd.** `internal/cli/shell_init.go` wraps `create|destroy|go|init` and a nested `session`→`create`, and contains the string `worktree` zero times. Only the deprecated `niwa session create` alias lands the user in the new tree. This is an independent bug and it also blunts the "blocking step delays the handoff" concern for the canonical command — there is no handoff today.
- **The worktree analog of setup scripts already exists, but reads from the wrong place.** `runWorktreeHooks` (`worktree_content.go:1064`) is explicitly documented as "the worktree analog of the instance setup-script run", but it discovers scripts from `<configDir>/worktree-hooks/` — the *workspace config repo* — not from the target repo. The gap is precisely repo-provided vs workspace-provided scripts. DESIGN-worktree-command-parity.md names setup scripts as a step of the instance pipeline (line 57) and then never carries them into the worktree design.
- **`runWorktreeHooks` is fatal where `RunSetupScripts` is not.** A non-zero worktree hook fails `ApplyToWorktree` (`:1105`); a non-zero setup script is a deferred warning. Two scripts with the same job have opposite blast radii.
- **There are five `ApplyToWorktree` call sites, not two.** The brief mentions create and apply; `cli/apply.go:306` (`runApplyWorktreeScope`) and `workspace/apply.go:2433` (the Step 6.6 fan-out) and `session_from_hook_cmd.go:144` are the other three.
- **Step 6.6 does not consult git for enumeration** — it enumerates session state files and uses `git worktree list --porcelain` only as a per-worktree *validity* check. A worktree created outside niwa's session records is invisible to it.
- **The redactor's placement in `runPipeline` was a deliberate fix for setup scripts specifically.** The 15-line comment at `apply.go:789-803` explains that it was moved to the pipeline's first statement so overlay-resolved secrets are registered before scripts run in the directory those secrets were materialized into. Any worktree setup run needs the same treatment, and the worktree path currently builds no redactor at all while `inheritEnvOutputs` copies real secret values into the worktree.

## Open Questions

- Should worktree setup run on every `ApplyToWorktree` (five call sites, including a per-apply fan-out over all live worktrees), or only on the create path? Idempotence is the setup-script contract already, but "idempotent" and "cheap" are different claims, and Step 6.6 multiplies the cost by the number of live worktrees.
- Is the same `setup_dir` the right directory for worktrees, or does a worktree need a distinct (usually cheaper) set — install deps but skip git-hook installation, which the shared `.git` already covers?
- On the `from-hook` path, should worktree setup run at all given the untimed `WorktreeCreate` hook entry? Options are: skip it there, add an explicit `timeout` to the emitted hook entry (`materialize.go:795-806`), or bound the setup run itself.
- Failure posture on `worktree create`: warn-and-continue (matching `RunSetupScripts` and the instance path) or fatal (matching `runWorktreeHooks`)? And on `from-hook`, does a failed setup script justify the existing full rollback?
- Does the `worktree create` shell-wrapper gap (`shell_init.go`) get fixed as part of this, or separately? It changes whether "a blocking step delays the handoff" is a real user-facing cost today.

## Summary

Setup scripts have exactly one clean hook point: inside `workspace.ApplyToWorktree` (`internal/workspace/worktree_content.go:525`), alongside step 5's `runWorktreeHooks` at `:712` — it is the only site holding cfg, repo, group, instanceRoot and worktreePath at once, it is in the same package as `RunSetupScripts`, and it already frames itself as the worktree analog of the instance setup run; it lacks only a `*Reporter` and a `*secret.Redactor`, both constructible there. The trade is blast radius: five callers reach it, including Step 6.6's per-apply fan-out over every live worktree, whereas a new step after `apply.go:1959` would have the pipeline's Reporter and redactor already in hand but covers only apply and needs `refreshWorktreeEnvs`'s inline four-guard enumeration (`apply.go:2394-2440`) extracted first. Blocking is unavoidable — the shell `cd` is a post-exit read of `NIWA_RESPONSE_FILE`, so nothing can hand the user in early — though the canonical `niwa worktree create` is not shell-wrapped at all today, and the sharper constraint is the agent-facing `WorktreeCreate` hook entry, which niwa writes with no `timeout` field.
