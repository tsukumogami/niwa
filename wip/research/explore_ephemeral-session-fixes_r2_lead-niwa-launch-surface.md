# Lead: How does niwa launch / invoke `claude` today, and what existing wrapper surface could host a new "instance-rooted dispatch" command?

## Findings

### 1. The ONE place niwa execs `claude` as a session: `niwa session attach`

There is exactly one production code path in this branch that spawns an interactive `claude` session, and it is `niwa session attach`. The exec is in `internal/cli/sessionattach/supervise.go:44`:

```go
cmd := exec.CommandContext(ctx, bin, "--resume", opts.ConvID)
cmd.Dir = opts.WorkerCWD
cmd.Stdin/Stdout/Stderr = ... (inherited)
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}  // own process group for signal forwarding
```

- `bin` is `exec.LookPath("claude")` when `ClaudeBin` is empty (`supervise.go:36-43`).
- It launches `claude --resume <ConvID>` — RESUME, not a fresh prompt, and **no `--channels` flag**.
- `cmd.Dir = opts.WorkerCWD`. WorkerCWD is computed in `internal/cli/sessionattach/attach.go:117` as `filepath.Join(state.WorktreePath, state.Repo)` — i.e. the cwd is set to the repo subdir inside the worktree, exactly the "dispatch-time cwd is the only lever" insight from round 1.
- Supervise blocks on `cmd.Wait()`, forwards SIGINT/SIGTERM/SIGHUP to the child's process group (`supervise.go:65-76`), and maps the exit code (capped at 125) back. This is the reusable "spawn claude in a given cwd and supervise it" primitive.
- `AttachRun` (`attach.go:36`) wraps it with: lifecycle-state read + `status==active` validation, a flock-based attach lock with stale-sentinel recovery (`attach.go:75-108`), transcript preflight (`attach.go:116-124`), and an attach-state sentinel write (`attach.go:135-147`). The `SuperviseFn` field (`attach.go:28`) is an injection seam for tests.

The other `claude` exec in the repo is a version probe only: `internal/workspace/harness_compat.go:37` runs `claude --version` to gate worktree-hook support. Not a session launcher.

### 2. The `--channels` / "telegram: no bot for workspace" string is NOT in this branch

This is the load-bearing negative finding. I searched exhaustively:
- `grep "no bot for workspace" / "skipping --channels"` across `*.go`, `*.sh`, `*.md`, `*.toml` → **zero hits anywhere in the repo.**
- No `mesh` cobra command exists: `grep 'Use:.*"mesh"|meshCmd|MeshWatch|"watch"'` over non-test Go → zero hits.
- No code assembles a `--channels` flag and no code execs `claude --bg`: `grep '"--bg"|claude.*--bg|--channels|spawnWorker|launchClaude'` over non-test Go → zero hits.
- `internal/workspace/channels.go` (referenced by archived design `DESIGN-niwa-ask-live-coordinator.md:71` as the home of `buildSkillContent()` and the mesh skill) **does not exist** in this checkout.

What DOES exist is documentation of the feature, not its implementation:
- `README.md:129-130` documents `niwa mesh watch --instance-root <path>` ("the mesh watch daemon, started automatically by `niwa apply` when `[channels.mesh]` is configured") and `niwa destroy` SIGKILLing running workers.
- `docs/prds/PRD-cross-session-communication.md:156-159` defines the `--channels`/`--no-channels` flags on `niwa create`/`niwa apply`, the `NIWA_CHANNELS=1` env var, and precedence (flag > env > config). `[channels.mesh.roles]` maps roles (coordinator/worker) to repos.
- `docs/designs/current/DESIGN-init-command.md:114-119` and `PRD-init-bootstrap-empty-source.md` scaffold a `[channels.mesh]` block.

**Conclusion on the wrapper mechanism:** the "telegram: no bot for workspace 'test' on host 'ryzen9', skipping --channels" message is emitted by the **mesh-watch daemon launcher** — the component that turns `[channels.mesh]` config into `claude --bg ... --channels <...>` invocations and wires per-workspace telegram bots. That launcher is a separate, not-yet-merged (or out-of-this-branch) feature. The `docs/ephemeral-session-fixes` branch this worktree is on (git log: round-1/round-2 explore commits on top of `feat: provision one ephemeral niwa instance per Claude Code session (#169)`) carries the ephemeral-instance hook machinery but NOT the mesh launcher. So the proof-string is real, but the code that prints it is not reachable from this checkout — it lives in the mesh feature line.

### 3. Mapping the proposed `niwa <verb>` dispatch command onto existing building blocks

The new command — pre-create an ephemeral instance, launch `claude --bg "<prompt>"` rooted in it, capture the session id, record the session→instance mapping — reuses almost everything already present:

| Step | Existing building block | Location |
|------|------------------------|----------|
| (a) Create ephemeral instance | `realProvisionInstance` → `applier.Create(ctx, cfg, configDir, workspaceRoot, instanceName)` | `internal/cli/instance_from_hook.go:364-408` |
| naming the instance | `computeInstanceName` (config-name + suffix, avoids NextInstanceNumber race) | `instance_from_hook.go:381`, `create.go:68` |
| GitHub token for clone | `resolveGitHubToken()` + `github.NewAPIClient` | `instance_from_hook.go:386-387` |
| opportunistic reap before create | `reapOpportunistically(workspaceRoot)` | `internal/cli/reap.go:181` |
| (b) Launch claude in instance cwd | `sessionattach.Supervise` (generalize: today it hardcodes `--resume <ConvID>`; the dispatch verb needs `--bg "<prompt>"` instead — a small arg change, `cmd.Dir` already parameterized) | `internal/cli/sessionattach/supervise.go:44` |
| (c) Capture session id | NOT directly available from Supervise today. `claude --bg` returns/records a session id; for the SessionStart-hook path the id arrives via hook stdin (`instanceHookPayload.SessionID`, `instance_from_hook.go:60-66`). A dispatch verb would need to capture it from `claude`'s `--bg` output/job-state, not from a hook. |
| (d) Record session→instance mapping | `workspace.WriteSessionMapping(workspaceRoot, SessionMapping{...Ephemeral:true})` | `internal/workspace/session_map.go:83`; struct at `session_map.go:49-60` |
| teardown / reclamation | `runInstanceHookEnd` (SessionEnd) + `reapWorkspace` backstop, both keyed on the mapping | `instance_from_hook.go:194`, `reap.go:153` |

**Where the command file would live:** `internal/cli/` (sibling of `instance_from_hook.go`, `reap.go`, `session.go`), registered on `rootCmd` via an `init()` like every other command. A natural home is a new `internal/cli/dispatch.go` (or as a subcommand under the existing hidden `instanceCmd`, `instance_from_hook.go:30`). The launch/supervise half belongs in `internal/cli/sessionattach/` alongside `Supervise`, generalizing `SuperviseOptions` to carry either `--resume <id>` or `--bg <prompt>`.

### 4. cwd + environment materialization (the GH_TOKEN / claude.env story)

- **cwd**: set per-process via `cmd.Dir` — already done at `supervise.go:45`. The dispatch verb sets `cmd.Dir` to the provisioned instance dir (or its primary repo subdir, mirroring `attach.go:117`).
- **env materialization**: niwa does NOT pass secrets via process env to the child. Instead `applier.Create` materializes secret-output files INSIDE the instance/clone during creation. The machinery is `EffectiveEnvOutput` / `OutputTargets` driving env-file writes in `internal/workspace/materialize.go:90-95` (the `EnvOutputs` accumulator) and `internal/workspace/worktree_content.go:53-135, 181-182, 433` (`inheritEnvOutputs` byte-copies the clone's already-materialized env output into worktrees). So a `claude --bg` rooted in the instance picks up `GH_TOKEN`/`claude.env` because those files were written into the instance tree by `Create`, and Claude Code loads them from the instance's `.claude`/settings/env. Re-rooting cwd into the instance is therefore both necessary AND sufficient for env to resolve — no separate env-injection step is needed at launch time, provided `Create` succeeded.
- The "GH_TOKEN/claude.env promotion that failed during `niwa worktree create`" is exactly this env-output materialization step (`inheritEnvOutputs`, `worktree_content.go:181`). A dispatched session inherits the same dependency: if `applier.Create`'s env materialization fails, the launched claude won't see the token. The dispatch verb gets this for free by reusing `realProvisionInstance` (which uses the full `applier.Create` path), but it also inherits that path's failure modes.

## Implications

- The reuse story is strong for steps (a), (d), and teardown — they are already factored as package functions (`realProvisionInstance`, `WriteSessionMapping`, `reapWorkspace`) with test seams.
- The launch primitive (`Supervise`) is reusable but needs a small generalization: today it is `--resume <ConvID>`-only; the dispatch verb needs `--bg "<prompt>"`. The cwd/signal-forwarding/exit-code scaffolding all carries over unchanged.
- The genuinely NEW piece is **session-id capture for a freshly-launched `--bg` session**. Existing code only ever LEARNS a session id (from the SessionStart hook stdin); it never STARTS a session and reads its id back. The dispatch verb must capture the id from `claude --bg`'s output or its `~/.claude/jobs/<id>/state.json`, then write the mapping. This inverts the current ordering (today: hook fires → provision → map; proposed: provision → launch → capture id → map).
- Env "just works" if cwd is re-rooted into a successfully-created instance; no new env-promotion code is needed, but the dispatch verb inherits `applier.Create`'s env-materialization failure modes.

## Surprises

- The proof-string (`telegram: no bot for workspace ... skipping --channels`) is genuinely NOT produced by any code in this checkout. The whole `--channels`/mesh-watch launcher is documented in README + PRDs but unimplemented on this branch. The string the lead cited as "proof niwa intercepts claude" is proof of a DIFFERENT, parallel feature line (mesh), not of the ephemeral-instance hooks that this branch actually ships.
- niwa does NOT shim, alias, or PATH-wrap `claude`. There is no generated shell function and no settings-hook that rewrites the `claude` invocation. The only integration points are (1) Claude Code's own SessionStart/SessionEnd hooks calling back into `niwa instance from-hook` (`instance_from_hook.go:41`), and (2) `niwa session attach` directly exec'ing `claude --resume`. The "wrapper surface" is thinner than the lead's framing assumed.
- `niwa session attach` launches `--resume`, never a fresh prompt — so there is no existing "launch claude with a prompt" code at all today; it must be built (by generalizing Supervise).

## Open Questions

1. **Where does the `--channels` launcher actually live?** It is not in this branch. Is it on the mesh feature branch / an unmerged PR, or in a sibling repo? Confirming its real shape matters because the dispatch verb may want to coexist with (or reuse) the mesh launcher's `claude --bg` invocation rather than build a second one.
2. **How is the `--bg` session id captured at launch?** Does `claude --bg "<prompt>"` print the session id to stdout, or must niwa poll `~/.claude/jobs/`? This determines whether step (c) is a stdout-parse or a job-state watch.
3. **Hook-vs-dispatch double-provision.** If the dispatch verb pre-creates the instance AND the SessionStart hook also fires (the session IS a `bg` worker), the re-entrancy guard (`sessionStartGuardPasses`, `instance_from_hook.go:248-273`) must prevent a second instance. The guard's "already inside an instance" check (step 3) keys on cwd resolving inside an instance — does a dispatched `--bg` whose cwd is the pre-created instance trip that guard correctly? Needs verification.
4. Should the new verb live as `internal/cli/dispatch.go` on `rootCmd`, or as a subcommand of the hidden `instanceCmd`? The former is user-facing; the latter groups it with the hook machinery.

## Summary
The only place niwa spawns an interactive claude today is `niwa session attach`, which execs `claude --resume <ConvID>` with `cmd.Dir` set to the worktree (`internal/cli/sessionattach/supervise.go:44`) — there is no shell shim/alias and, critically, the cited `--channels`/"no bot for workspace" string is NOT in this branch at all (it belongs to an unimplemented-here mesh-watch launcher documented only in README/PRDs). A dispatch verb can reuse `realProvisionInstance`→`applier.Create` (instance_from_hook.go:364), `WriteSessionMapping` (session_map.go:83), `reapWorkspace` (reap.go:153), and a generalized `Supervise` (swap `--resume <id>` for `--bg "<prompt>"`, cwd already parameterized); env resolves for free because `applier.Create` materializes GH_TOKEN/claude.env files INTO the instance tree, so re-rooting cwd is sufficient. The biggest open question is session-id capture for a freshly-launched `claude --bg` — existing code only ever learns a session id from a SessionStart hook, never starts a session and reads its id back, so that capture step is the one genuinely new building block.
