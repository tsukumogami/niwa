# Lead: What is the real blast radius of making `niwa apply` / `niwa create` exit non-zero when a setup script failed?

Repo: `public/niwa`, branch `docs/setup-script-visibility`. All line numbers are
from the working tree as read during this round.

## Findings

### 1. The pipeline structure: where Step 6.75 sits relative to the state write

`RunSetupScripts` is called from **inside** `runPipeline`, at
`internal/workspace/apply.go:1580-1596`. `runPipeline` is a pure "do the work,
return a result" function (`internal/workspace/apply.go:702`); it returns
`(*pipelineResult, error)` and **writes no state**.

The state write happens in the two callers, strictly after `runPipeline` returns:

| Function | runPipeline call | `SaveState` | Error handling of runPipeline |
|---|---|---|---|
| `Applier.Create` | `apply.go:420` | `apply.go:489` | `apply.go:428-431`: `_ = os.RemoveAll(instanceRoot); return "", err` |
| `Applier.Apply` | `apply.go:569` | `apply.go:681` | `apply.go:576-578`: bare `return err` |

This is the mechanical crux, and it answers the last sub-question directly:

**Returning an error from Step 6.75 is not "exit non-zero after the pipeline
completed". It is strictly worse than aborting mid-pipeline, in both callers.**

- **`Create`**: `apply.go:430` runs `os.RemoveAll(instanceRoot)` on *every*
  non-nil `runPipeline` error. A failed setup script in one repo would delete
  the entire freshly-cloned instance — every repo, every materialized file. The
  cross-repo resilience Decision 2 protects would not merely be reversed; it
  would be inverted into total destruction. `internal/workspace/bootstrap.go:213-222`
  documents this as a load-bearing contract: *"the implicit contract for the
  create step is 'either Applier.Create returns nil OR the instance dir does not
  survive'"*.
- **`Apply`**: the bare `return err` at `apply.go:576` skips `SaveState`
  (`apply.go:681`), `cleanRemovedFiles`, `cleanRemovedGroupDirs`,
  `emitRotatedFiles`, and `saveWorkspaceRootDisclosures`. Everything Step 6
  materialized is already on disk, but `state.json` still holds the *previous*
  apply's `ManagedFiles`, `Repos`, `LastApplied`, `Shadows`, and `AuthSources`.
  Files written this run become unmanaged orphans; drift baselines are stale;
  the next apply's `cleanRemovedFiles` reasons from the wrong prior set.

**So option 2 is only expressible if the failure signal travels out of
`runPipeline` through `pipelineResult` (struct at `apply.go:323-343`, which
already carries a `warnings []string` field used exactly this way at
`apply.go:499-501` and `apply.go:691-693`) and is converted into an error by
`Create`/`Apply` *after* their `SaveState` call.** There is currently no code
path in either function that returns a non-nil error after `SaveState` succeeds,
so this shape would be new — but it is structurally available and requires no
change to `runPipeline`'s contract.

Note also that Step 6.75 today does not feed `allWarnings`/`result.warnings` at
all — it calls `a.Reporter.DeferWarn` directly (`apply.go:1591`), bypassing the
result struct. Routing it through `pipelineResult` is a prerequisite for any
exit-code option, and is the same wiring an "N setup scripts failed" summary
line would need.

### 2. Enumerated callers of the apply pipeline

Every caller of `Applier.Apply` / `Applier.Create` in non-test code:

| # | Call site | Command | On non-nil return |
|---|---|---|---|
| 1 | `internal/cli/apply.go:262` | `niwa apply` | collected into `applyErrors`, prints `error: applying to <root>: <err>` to stderr, `continue`s to the next instance; after the loop `combineInstanceErrors` (`internal/cli/apply.go:430`) wraps them and `runApply` returns non-nil |
| 2 | `internal/cli/create.go:247` | `niwa create` | `return err` — **before** `writeLandingPath` (`create.go:268`) and before the `--json` encode (`create.go:271-283`) |
| 3 | `internal/cli/reset.go:151` | `niwa reset` | `return fmt.Errorf("recreating instance: %w", err)`; the `Reset instance: <path>` line is never printed |
| 4 | `internal/cli/init.go:197` (inside `createWrapper`) | `niwa init --bootstrap` | flows to `RunBootstrap` step 3 (`internal/workspace/bootstrap.go:223-226`) → `bootstrap step=create: <err>`; the bootstrap aborts before `CreateSession`, `ScaffoldFromSource`, `git add`, and `git commit` |
| 5 | `internal/cli/instance_from_hook.go:417` (inside `realProvisionInstance`) | shared provisioner behind **three** commands | `return provisionResult{}, err` — **the instance path is discarded** |

Caller 5 fans out to three distinct commands via the `provisionInstanceFunc`
package variable (`internal/cli/instance_from_hook.go:104`):

- **`niwa instance from-hook` (SessionStart)** — `instance_from_hook.go:172`.
  On error: `return fmt.Errorf("niwa: error: provisioning instance for session %s: %w", ...)`.
  The `WriteSessionMapping` call (`:180`) and the `additionalContext` injection
  (`:186-193`) never run.
- **`niwa dispatch`** — `internal/cli/dispatch.go:300`. On error:
  `return fmt.Errorf("niwa: error: provisioning dispatch instance: %w", err)`.
  This returns at step (6), **before** the self-rollback defer is armed at step
  (7) (`dispatch.go:305-312`) and before the pending marker is written at step
  (8). The worker is never launched.
- **`niwa watch`** (contained PR-review instances) — `internal/cli/watch.go:777`,
  `return fmt.Errorf("provisioning contained instance: %w", err)`, likewise
  before its rollback defer at `watch.go:783-792`.

Commands that do **not** reach the apply pipeline (checked): `niwa reap`
(no `Applier` reference), `niwa worktree apply` / `niwa apply` in worktree scope
(`runApplyWorktreeScope`, `internal/cli/apply.go:293`, uses
`applyContentToWorktree`, not `Applier.Apply`), `niwa worktree create`
(`worktree.CreateSession`), `niwa destroy`, `niwa go`, `niwa status`.

### 3. What each non-zero exit actually costs

**`niwa create` → the shell wrapper stops `cd`-ing.** `internal/cli/shell_init.go:39-50`
defines `__niwa_cd_wrap`, and `:46` is a literal `$?` check:

```sh
if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
    builtin cd "$__niwa_dir" || return
fi
```

`create` is one of the four wrapped subcommands (`shell_init.go:52-56`:
`create|destroy|go|init`). Two independent things break: the wrapper's exit-code
gate, and — because `create.go:249` returns before `writeLandingPath` — the
response file is never written at all, so even removing the `-eq 0` gate would
not help. Same for `niwa init --bootstrap`.

**`niwa create --json` emits nothing.** `create.go:249` returns before the
`json.NewEncoder(...).Encode(out)` at `create.go:281`. Any consumer that reads
the `{name, number, path}` shape (documented as the surface the hook mirrors,
`instance_from_hook.go:89`) gets an empty stdout.

**The SessionStart hook orphans the instance permanently.** This is the worst
one. `realProvisionInstance` returns `provisionResult{}` on error
(`instance_from_hook.go:418`), throwing away the path `Create` returned. So:

1. The instance directory exists on disk with all repos cloned and state written
   (in the hypothetical "error after `SaveState`" shape).
2. `WriteSessionMapping` never runs, so nothing points at it.
3. The reaper's primary sweep joins instances to sessions by mapping — an
   unmapped instance is invisible to it.
4. The name+TTL backstop (`selectBackstopTargets`, `internal/cli/reap.go:232-267`)
   is explicitly scoped to dispatch-shaped names only. The comment at
   `reap.go:264-266` states it outright: *"a hook-created instance
   (`<config>-<sessionhex>`, no `+` marker) ... never match the name predicate,
   so they are never touched regardless of age or mapping."*

Result: a permanently unreclaimable orphan instance per failed session start.

The hook command carries no `|| true` guard — `guardedNiwaHookCommand`
(`internal/workspace/materialize.go:354-358`) emits
`command -v niwa >/dev/null 2>&1 && exec niwa instance from-hook; exec '<abs>' instance from-hook`,
so the subcommand's exit status is the hook's exit status. The design comment at
`materialize.go:340-342` notes the harness surfaces hook stderr in the tool
result, so the failure is at least loud — but the session still loses its
`additionalContext` injection and never learns it has an instance.

**`niwa dispatch`: the worker never launches.** Answering the sub-question
directly — dispatch does *not* "run an apply and then launch a session" as two
independently-recoverable steps. Provisioning is step (6) of a linear sequence
and a hard gate: `dispatch.go:300-303` returns on any provisioning error, and
because that return happens *before* the rollback defer at `dispatch.go:305-312`,
the instance is neither destroyed nor used. It is, however, dispatch-named, so
the name+TTL backstop *will* reclaim it after `dispatchBackstopTTL` — dispatch
is the one caller that degrades gracefully on the disk-hygiene axis. But a
dispatch whose clones, materialization, and state write all succeeded would be
thrown away entirely because one repo's setup script exited 1.

**`niwa apply` is the benign one.** The instance loop
(`internal/cli/apply.go:261-272`) collects errors and continues, so a
setup-failure error from instance 1 does not stop instance 2 from converging;
`updateRegistry` still runs afterward (`apply.go:279`). The only cost is the
stderr line `error: applying to <root>: ...` and `combineInstanceErrors`'
`apply failed for <root>` wrapper, both of which read as "this instance did not
converge" when in fact it did. That's a wording problem, not a mechanism problem.

**`niwa init --bootstrap` loses the whole bootstrap.** `RunBootstrap` returns at
step 3 (`bootstrap.go:223-226`), so the session-create, the second scaffold
write, the `git add`, and the `git commit` (steps 4-7) never happen. The
workspace root survives (the `disarm()` at `internal/cli/init.go:171` already
fired), but the bootstrap repo is left with no committed `.niwa/` config.

### 4. Shell scripts, Makefile, CI

Searched `install.sh`, `scripts/`, `Makefile`, and `.github/workflows/` for
`niwa apply|create|init|dispatch` — **zero hits**. `install.sh` only downloads
and checksums the release binary (`install.sh:70-105`) and appends a
`niwa shell-init auto` eval to the user's shell config (`install.sh:117-118`).
`scripts/` contains only `docker-test.sh`. None of the eight workflow files
invoke the built binary's apply path. So there is no CI or installer `$?` check
to break.

### 5. Functional test suite

`test/functional/features/` holds 30 feature files with 288 exit-code assertion
lines, of which 107 are `Then the exit code is 0` within three lines of a
`niwa apply` / `niwa create` run.

**None of them involve setup scripts.** Grepping the entire `test/` tree for
`setup_dir`, `scripts/setup`, or the word `setup` in any `.feature` file returns
nothing; no fixture repo builder creates an executable under a setup directory.
So a non-zero-on-setup-failure change would break **zero** existing functional
assertions. A new scenario would have to construct the fixture from scratch.

Unit coverage is equally narrow: `internal/workspace/setup_test.go` has 11 tests
(`TestResolveSetupDir*` ×4, `TestRunSetupScripts{Disabled,MissingDir,EmptyDir,
Success,StopOnError,NonExecutableWarning,LexicalOrder}`), all calling
`RunSetupScripts` directly. Nothing exercises Step 6.75 through `runPipeline`,
`Apply`, or `Create`, and nothing asserts on captured script output. The only
non-test references to the setup API outside `setup.go` are the two lines in
Step 6.75 itself (`apply.go:1582`, `apply.go:1584`).

### 6. Existing exit-code contract

niwa **does** distinguish exit codes, in two places, both via typed errors
unwrapped in `Execute()` (`internal/cli/root.go:73-97`):

- `sessionattach.ExitCodeError` (`internal/cli/sessionattach/detach.go:33-38`) —
  used by `niwa session attach` / `niwa session detach`. Codes in use: 1
  (generic), 3 (attach lock held, `attach.go:99`), 4 (killed live holder,
  `detach.go:98`), plus pass-through of the supervised `claude` process's own
  code (`attach.go:168`). Documented in `docs/guides/worktree.md:521` under
  "Attach exit codes".
- `workspace.InitConflictError.ExitCode` (`internal/workspace/preflight.go:37-46`)
  — used by `niwa init`. The doc comment states the mapping: *"1=step failure,
  2=flag-validation, 3=host-validation, 4=NoMarker-without-bootstrap"*.
  Documented in `docs/designs/current/DESIGN-init-bootstrap-empty-source.md:1156`
  ("Exit codes (R23)").

Everything else falls through to `fmt.Fprintln(os.Stderr, err); os.Exit(1)`
(`root.go:95-96`). **`apply` and `create` have no typed exit code and no
documented exit-code table** — not in `README.md` (zero hits for "exit"), not in
`--help` text, not in any guide. There is no setup-scripts user guide at all;
`scripts/setup` and `setup_dir` appear nowhere under `docs/guides/`.

There *is* one documented exit-code promise that touches apply:
`docs/guides/workspace-config-sources.md:262` states, for a degraded-network
apply, *"Apply continues with cached snapshot and emits a `warning:` notice.
**Exit code is 0.**"* That establishes a documented precedent that an apply which
warns still exits 0 — narrow (it's about config-snapshot refresh, not setup
scripts), but it is the only committed statement in the direction of the
question.

### 7. The nearest fail-closed precedent, and why it is not the same shape

`env_example_policy` is the closest thing niwa has to an opt-in strict mode: a
per-category `warn`/`fail` posture, configurable at global/workspace/repo level
plus inline annotations, where `fail` makes apply exit non-zero. Functional
coverage at `test/functional/features/env-example-failure-policy.feature:104`
and `:247` (`Then the exit code is not 0`), and an escape hatch flag
(`--allow-plaintext-secrets`) that downgrades every `fail` to `warn`.

But mechanically it is a **prepass**, not a post-hoc verdict:
`runEnvExamplePrePass` (`internal/workspace/env_example_prepass.go:32`) runs
inside the materializer and its error aborts apply *before* the offending
material is written. Its doc comment says so — *"A resolved fail accumulates an
error that aborts apply"*. So it is precedent for the *configuration shape*
(named policy levels, per-scope override, a downgrade flag) but **not** for
"complete the whole pipeline, write state, then exit non-zero", which remains a
shape niwa has never expressed.

## Implications

1. **Option 2 is expressible, but only above `runPipeline`.** Any implementation
   that turns Step 6.75's failure into a `runPipeline` error return is wrong by
   construction: it deletes the instance in `Create` and desynchronizes
   `state.json` in `Apply`. The signal must ride out in `pipelineResult` (the
   `warnings` field at `apply.go:327` is the existing precedent for exactly this
   plumbing) and become an error only after `SaveState` at `apply.go:489` /
   `apply.go:681`.

2. **Even done correctly, the blast radius is not short and not benign — for
   `Create`.** Five call sites, three of them routed through one provisioner
   that discards the instance path on error. Ranked by damage:
   - SessionStart hook: permanent unreclaimable orphan instance (the backstop
     explicitly excludes hook-named instances, `reap.go:264-266`).
   - `niwa dispatch` / `niwa watch`: the worker never launches; the instance is
     reclaimed only by TTL.
   - `niwa create`: no `cd`, no `--json` output.
   - `niwa init --bootstrap`: the entire bootstrap sequence (session, scaffold,
     commit) is skipped.
   - `niwa reset`: no confirmation line.

3. **The `Apply` half of option 2 is genuinely cheap.** `niwa apply`'s instance
   loop already tolerates per-instance errors and continues. The only work is
   wording: `error: applying to X` and `apply failed for X` would need to become
   something that does not claim the instance failed to converge.

4. **This asymmetry suggests a split verdict is available:** non-zero exit on
   `niwa apply` costs almost nothing, while non-zero exit on `niwa create`
   breaks five downstream contracts including one that leaks disk permanently.
   If the exploration wants the exit code to tell the truth, the cheapest honest
   version is `apply`-only, or `Create` returning `(instancePath, err)` with
   every caller taught to use the path — which is a five-call-site change, three
   of them in security-sensitive provisioning code with armed rollback defers.

5. **Nothing in tests, CI, or the installer blocks any option.** No functional
   scenario exercises setup scripts; no shell script checks `$?` on apply/create
   except the `cd` wrapper. The constraint is entirely in the Go call graph, not
   in the surrounding tooling.

6. **There is no exit-code contract to violate, but there is one to write.**
   `apply`/`create` have no documented codes. If a non-zero setup-failure exit
   lands, it should probably be a distinct code (the `InitConflictError`/
   `ExitCodeError` pattern is the established mechanism) so a caller can tell
   "setup script failed but the instance is usable" from "apply failed and the
   instance did not converge" — otherwise the hook and dispatch paths cannot
   safely proceed on the former.

## Surprises

- **`Create` deletes the instance on *any* pipeline error** (`apply.go:430`),
  and `bootstrap.go:213-222` documents this as a deliberate contract rather than
  an incidental cleanup. This single line is what makes the naive reading of
  option 2 catastrophic rather than merely annoying.
- **`realProvisionInstance` discards the instance path on error**
  (`instance_from_hook.go:418`), so even a `Create` that returned both a valid
  path and an error would be unrecoverable for the three provisioning commands.
- **The reap backstop deliberately excludes hook-created instances**
  (`reap.go:264-266`) — the exclusion is correct for its own purpose but means
  the SessionStart path has no safety net at all for an instance that exists
  without a mapping.
- **Zero test coverage of setup scripts above the unit level.** 30 feature
  files, 288 exit-code assertions, and not one setup script anywhere in `test/`.
  A feature shipped with a documented Decision on failure semantics has no
  end-to-end assertion of those semantics.
- **`niwa create` writing the landing path *after* the error check** means the
  shell wrapper's `cd` and the `--json` output are both casualties of any late
  error, independent of the wrapper's `-eq 0` gate.
- **`docs/guides/workspace-config-sources.md:262` already commits in writing
  that a warning-emitting apply exits 0** — a small but real documented promise
  in the neighborhood.
- **Step 6.75 does not use `result.warnings`** (`apply.go:1591` calls
  `Reporter.DeferWarn` directly), so it is the only pipeline step whose warnings
  bypass `pipelineResult`. Every option under consideration needs that fixed
  first.

## Open Questions

- Would a distinct exit code (say 5) for "converged, but ≥1 setup script failed"
  let the hook and dispatch paths treat it as success-with-warning while a human
  or CI caller treats it as failure? That would need `Create` to return the path
  alongside the error, and `realProvisionInstance` to stop discarding it — I did
  not evaluate how invasive teaching all five call sites is.
- The `--allow-plaintext-secrets` downgrade flag on `env_example_policy` is a
  run-scoped global override. Is there an appetite for a symmetric
  `setup_policy = "warn" | "fail"` config key (matching the env-example shape)
  rather than a CLI `--strict`? I found the precedent but did not assess whether
  the config schema wants another policy block.
- `niwa watch`'s contained-instance path was not in the brief's list; I found it
  at `watch.go:777` but did not trace whether a review instance would even have
  repos with setup directories, so its practical exposure is unquantified.
- I could not determine what Claude Code does with a non-zero SessionStart hook
  beyond what `materialize.go:340-342` asserts (stderr surfaced in the tool
  result) — whether it blocks the session or merely logs is outside this repo.

## Summary

Step 6.75 runs inside `runPipeline`, which writes no state; `SaveState` happens
in `Create` (`apply.go:489`) and `Apply` (`apply.go:681`) only after it returns,
and `Create` runs `os.RemoveAll(instanceRoot)` on every pipeline error
(`apply.go:430`) — so turning a setup failure into a `runPipeline` error would
delete the whole instance on create and leave `state.json` stale on apply, which
is strictly worse than today, though a `pipelineResult` field converted to an
error after `SaveState` would express option 2 correctly. The blast radius is
five call sites (`apply.go:262`, `create.go:247`, `reset.go:151`, `init.go:197`,
`instance_from_hook.go:417`), and the last one fans out to the SessionStart hook,
`niwa dispatch`, and `niwa watch` through a provisioner that discards the
instance path on error — costing, respectively, a permanently unreclaimable
orphan instance (the reap backstop excludes hook-named instances by design), a
never-launched worker, a lost `cd` and empty `--json`, and an aborted bootstrap;
only `niwa apply` degrades gracefully, since its instance loop already collects
errors and continues. Nothing external blocks the change — no CI, Makefile, or
installer touches apply/create, and none of the 30 functional feature files or
288 exit-code assertions involve setup scripts at all — and `apply`/`create`
have no documented exit-code contract to violate, though niwa does distinguish
codes elsewhere via `sessionattach.ExitCodeError` and
`workspace.InitConflictError.ExitCode`.
