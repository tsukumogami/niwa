---
schema: plan/v2
status: Active
execution_mode: multi-pr
split_mode_source: intent
split_rationale: |
  Hard Constraint. The work lands in three repositories (tsukumogami/niwa,
  tsukumogami/koto, tsukumogami/dot-niwa), and dot-niwa may declare the
  pre-removal gate only after niwa and koto releases carry the gate and the
  verb, so the units can't share one pull request.
tracking_level: none
upstream: docs/designs/DESIGN-instances-outlive-sessions.md
milestone: "Instances and worktrees outlive the sessions that need them"
step_count: 10
---

# PLAN: Instances and worktrees outlive the sessions that need them

## Status

Active

## Scope Summary

niwa keeps an instance and a held worker's worktree while a session still
needs them, through an engine-neutral pre-removal gate that koto answers, a
worktree sweep keyed on the requesting session's job, a destroy preflight that
refuses a running or kept instance unless forced, and a `niwa resume` that
replays what dispatch recorded. dot-niwa declares the koto seams niwa already
ships, then the gate.

## Decomposition Strategy

Walking skeleton. Step 1 crosses the whole engine-neutral path in niwa on one
input: a workspace declares `[session.before_remove]` with a stub command,
`niwa dispatch` starts its implicit reap, the reap asks the gate before it
destroys a reapable ephemeral instance, and the command's refusal keeps the
instance and prints the reason; with nothing declared the reap behaves as
today. A stub stands in for any engine, which is the point of the seam. Every
later step adds one behaviour on top: destroy refusing (step 2), the gate's
other removal sites and time bounds (step 3), the worktree sweep (step 4),
launch records and resume (steps 5 and 6), koto's answer (steps 7 and 8), and
dot-niwa's declarations (steps 9 and 10).

## Steps

### Step 1: an implicit reap spares an instance the workspace's pre-removal command refuses, and says why

**Goal**: A workspace can declare `[session.before_remove]`, and the reap that `niwa dispatch` runs implicitly asks that command before it destroys a reapable ephemeral instance: an exit other than 0 keeps the instance and its session mapping and prints one line naming the instance and the command's reason. This is the walking skeleton: it crosses the config load (`internal/config/session.go`, validated like `[session.on_reap]`), the new gate (`internal/cli/removal_gate.go`, sharing the on-reap exec helpers) and the mapped-reap call site in `reapWorkspace` (`internal/cli/reap.go`, immediately before `destroyInstanceFunc`), on one reapable ephemeral instance reaped by `niwa dispatch`'s implicit reap in a scratch workspace.

**Acceptance Criteria**:

- [ ] With `[session.before_remove]` declared as a command that prints `kept by test` and exits 1, an implicit reap started by `niwa dispatch` leaves the reapable ephemeral instance directory and its session mapping on disk unchanged, and stderr holds a line starting `niwa: kept <instance>: reason from before_remove: ` that names the instance by its directory name and carries `"kept by test"` Go-quoted.
- [ ] After the declared command is changed to exit 0, the next implicit reap removes that instance directory and its mapping.
- [ ] With no `[session.before_remove]` declared and a stub `sh` that records each invocation first on `PATH`, the same reap removes the instance as it does today and the stub records no invocation for the gate.
- [ ] The gate runs the command from the workspace root with stdin at end of file and an environment of exactly `PATH`, `HOME`, `NIWA_REMOVE_PATH` (the instance's absolute path), `NIWA_REMOVE_STEP=reap`, `NIWA_SESSION_IDS` (the mapped session ids, space-separated) and the declared names that are set; a stub that dumps its environment to a file shows no other variable.
- [ ] A command that writes to both stdout and stderr and exits 1 puts none of that raw output on niwa's terminal streams: only the single sanitized reason line appears, the reason is the last non-empty output line, it is at most 200 bytes, and an escape character in it is replaced.
- [ ] A configuration whose `[session.before_remove]` lists a name ending `_TOKEN`, a name starting `NIWA_`, a timeout that does not parse, or a timeout of `0s` fails to load with an error naming the field; a valid declaration loads.
- [ ] `go test ./...` in niwa passes with no `koto` binary on `PATH`.

**Check**: In a scratch workspace created under a temporary directory (never the real workspace, no koto involved), provision one reapable ephemeral instance with a session mapping whose job entry is gone, and declare a stub `[session.before_remove]` command that prints `kept by test` and exits 1. Run `niwa dispatch` with a stub agent and see the instance directory and mapping still present and the `niwa: kept <instance>: reason from before_remove: "kept by test"` line on stderr; run `niwa dispatch` twice more and see the same state. Change the stub to exit 0, run `niwa dispatch` once more, and see the instance and its mapping gone. Remove the declaration, reprovision the instance, run `niwa dispatch` with a recording stub `sh` first on `PATH`, and see the instance removed and no recorded gate invocation. Finish with `PATH=<dir without koto>:/usr/bin:/bin go test ./...` from the niwa repository root and see it pass.

**Dependencies**: None

**Type**: feature

**Complexity**: testable

**Files**: `internal/config/session.go`, `internal/cli/removal_gate.go`, `internal/cli/on_reap.go`, `internal/cli/reap.go`

### Step 2: `niwa destroy` refuses an instance whose session is running or the gate keeps, unless forced

**Goal**: `niwa destroy` stops taking a directory out from under a session that still needs it. A new preflight (`internal/cli/destroy_preflight.go`, called from `runDestroyInstance` and `runDestroyWorkspace` in `internal/cli/destroy.go`) lists Claude Code's sessions once through `listClaudeSessionsFunc`, finds the mapped owning sessions that are running, and asks the gate once per instance (step `destroy` or `destroy-workspace`) before any prompt. Without `--force` any reason means a non-zero exit with nothing removed and every reason listed (short id, session name when recorded, gate reason); with `--force` each reason is printed as overridden and destroy proceeds as it does today. `--workspace` refuses as a whole and still rejects `--force`, and a listing failure is a reason when the instance has a mapped session.

**Type**: feature

**Complexity**: testable

**Acceptance Criteria**:

- [ ] `niwa destroy <name>` of an instance whose mapped session the listing reports as running exits non-zero, prints that session's short id and, when one is recorded, its session name, shows no prompt, and leaves the instance on disk.
- [ ] A running session with no recorded session name is named by its short id alone.
- [ ] When the declared `[session.before_remove]` command also exits 1 for that instance, the refusal lists both the running session and the command's reason line, and a stub that appends one line per invocation to a file shows it ran exactly once, with `NIWA_REMOVE_STEP` set to `destroy`.
- [ ] `niwa destroy --force <name>` of that instance prints both reasons as overridden and then removes the instance.
- [ ] `niwa destroy --workspace --confirm <name>` with one instance kept by a running session and another kept by the gate removes nothing, names both instances with their reasons, and ran the gate with `NIWA_REMOVE_STEP` set to `destroy-workspace`; `niwa destroy --workspace --force` is still rejected as it is today.
- [ ] With the session listing failing, destroy of an instance that has a mapped session refuses and says the listing failed, `--force` proceeds, and destroy of an instance with no mapped session proceeds without calling the listing.
- [ ] A destroy of an instance with no running session and no `[session.before_remove]` declared behaves as it does today, and the stub shell shows no process was started for the gate.
- [ ] A refused destroy shows no unpushed-work prompt and no confirmation prompt, and a refused `--workspace` destroy leaves every instance and the workspace root on disk.

**Check**: In a scratch workspace created under a temp directory (never the real workspace), set `listClaudeSessionsFunc` to return a running session row for one instance and a stub `[session.before_remove]` command that prints `kept by test` and exits 1. Run `go test ./internal/cli -run 'DestroyPreflight|Destroy'` and expect the tests for each criterion to pass: the refusal output holds the short id, the session name and `kept by test`, the instance directory is still there, and the stub's invocation log has one line. Then run `go test ./...` and `go vet ./...` and expect no failures, with the existing destroy tests unchanged.

**Dependencies**: Step 1

**Files**: `internal/cli/destroy_preflight.go`, `internal/cli/destroy_preflight_test.go`, `internal/cli/destroy.go`

### Step 3: the gate guards the backstop and the watch discard, within its time bounds

**Goal**: The reap's backstop for unmapped dispatch instances and `niwa watch`'s discard of a staged review instance ask the pre-removal gate before they remove anything, and a test keeps any later removal path from skipping it. Assumes step 1's gate API at 4f8f6db: `newRemovalGate(root, step, out)` per pass, `check(path, ids) (bool, string)` and `ask(path, ids) bool`, the `removeStepReapBackstop` and `removeStepWatchDiscard` names, and `sessionIDsByInstance(root)`.

**Acceptance Criteria**:

- [ ] With `[session.before_remove]` declared to print a reason and exit 1, `reapBackstop` leaves an unmapped, dispatch-named, past-TTL instance on disk and prints `niwa: kept <instance>: ...`; the command saw `NIWA_REMOVE_STEP=reap-backstop` and an empty `NIWA_SESSION_IDS`; with the command exiting 0 the backstop removes it.
- [ ] With the same command, `niwa watch`'s discard of a staged review instance whose session is gone leaves the instance on disk with the same line, the command saw `NIWA_REMOVE_STEP=watch-discard`; with exit 0 it is discarded as before.
- [ ] With nothing declared, the backstop and the discard behave as before and start no process for the gate.
- [ ] With a command that sleeps 2 seconds and exits 0, under a 10-second timeout, the backstop removes the instance; with a shell that can't start, it keeps the instance and its line says the command could not start.
- [ ] With no timeout declared and four reapable instances whose commands each sleep 12 seconds, the first three runs are each cut at 10 seconds and keep their instances as timed out, and the fourth doesn't start and its line says the step's time ran out.
- [ ] A test lists every non-test caller of `destroyInstanceFunc` and `worktree.DestroySession` and fails when a new one appears. The gated callers are named in it (the mapped reap, the backstop, the watch discard, and `niwa destroy` through its preflight in step 2), the worktree sweep is named as not yet gated, since step 4 gates it, and so are the deliberately ungated ones: the rollbacks after a failed dispatch or watch launch, `niwa reset`, and the explicit `niwa session destroy` and `niwa worktree destroy` paths, which a person runs on a named target.

**Check**: `go test -count=1 -run 'TestRemovalGate|TestReapBackstop|TestWatch' ./internal/cli/` against scratch workspaces with stub commands; the new tests pass and the existing backstop and watch tests pass unchanged.

**Dependencies**: Step 1

**Files**: `internal/cli/reap.go`, `internal/cli/watch.go`, `internal/cli/removal_gate_test.go`

### Step 4: a held worker's hook-made worktree survives while its job is listed, and the sweep asks the gate

**Goal**: A worktree niwa's WorktreeCreate hook made for a session that had a Claude Code job entry is swept only once that job entry is gone, however idle its transcript, and the sweep asks the pre-removal gate before removing one. Assumes step 1's gate API at 4f8f6db: `newRemovalGate(root, removeStepWorktreeSweep, out)` built in `sweepHookWorktrees` and `ask(path, ids)`.

**Acceptance Criteria**:

- [ ] A WorktreeCreate hook run for a session with a job entry records the payload's session id (only when it is a well-formed session id) in the lifecycle record and records that it had a job entry; a run for a session with none records its id and that it had none.
- [ ] A held worker's hook-made worktree, record and transcript both 48 hours old, job entry still listed, is kept by every sweep; once the job entry is removed, the next sweep removes it.
- [ ] A worktree recorded with no job entry, and one whose record predates the change (no session id), are removed once record and transcript are older than the grace period, and kept while the transcript is younger.
- [ ] A job-keyed worktree whose record is younger than the grace period, or that is changed since creation, is kept with its job entry gone.
- [ ] With the jobs directory missing or unreadable, no job-keyed worktree is removed.
- [ ] A hook payload whose session id isn't a well-formed session id records no id, and that worktree follows the transcript rule.
- [ ] With a pre-removal shell that can't start, a worktree the sweep would otherwise remove is kept and its line says the command could not start.
- [ ] A worktree the sweep would otherwise remove is kept when the pre-removal command exits 1 for its path, which it sees with `NIWA_REMOVE_STEP=worktree-sweep` and the recorded session id in `NIWA_SESSION_IDS`, with a `niwa: kept` line.

**Check**: `go test -count=1 -run 'TestSweep|TestHookWorktree|TestSessionFromHook|TestRemovalGate' ./internal/cli/ ./internal/worktree/` against scratch instances and a scratch jobs directory; the new tests pass and the existing sweep tests pass unchanged.

**Dependencies**: Step 1

**Files**: `internal/cli/reap_worktrees.go`, `internal/cli/session_from_hook_cmd.go`, `internal/worktree/session_lifecycle.go`

### Step 5: a dispatched worker's launch is recorded and outlives a destroy until its transcript goes

**Goal**: `niwa dispatch` writes a launch record for each worker it launches, holding what it launched the worker with, in a store outside the instance and the configuration directory, so the record survives `niwa destroy`; the reap removes a record only once Claude Code no longer holds the session's transcript. Uses nothing from step 1's gate.

**Acceptance Criteria**:

- [ ] After a dispatch, `<workspace root>/.niwa-launch/<dispatch id>.json` exists with mode 0600 in a 0700 directory and holds the schema `niwa.launch/v1`, dispatch id, session id, agent, instance name, permission mode, session name, model, the settings document passed, and the environment names and values niwa set for the worker.
- [ ] No value of a name `ValidateSessionEnvName` refuses as a credential is written, and a dispatch whose record write fails warns and still completes.
- [ ] After `niwa destroy` of that instance and the on-reap processing of its session, the record is still there.
- [ ] A reap removes a record whose transcript Claude Code no longer holds, and keeps a record whose transcript exists or whose transcript directory can't be read.
- [ ] A record file that doesn't parse, or whose name isn't a valid dispatch id, is left in place with one warning line naming it, and the reap goes on with the others.
- [ ] A record file is written atomically and its dispatch id is validated before any path is built from it.

**Check**: `go test -count=1 -run 'TestLaunchRecord|TestDispatch|TestReap' ./internal/workspace/ ./internal/cli/` with a stub agent binary in a scratch workspace; the new tests pass and the existing dispatch and reap tests pass unchanged.

**Dependencies**: None

**Files**: `internal/workspace/launch_store.go`, `internal/cli/dispatch.go`, `internal/cli/reap.go`

### Step 6: `niwa resume` brings a dispatched worker back in its recorded posture, restoring a destroyed instance and its trust

**Goal**: `niwa resume <instance name | session id | session name> [prompt]` finds the worker's launch record, validates it, runs the machine authority checks dispatch runs, restores a missing instance under its original name and path with its Claude trust grant, and resumes the session with the recorded launch, never more permissive than current policy. Uses step 5's launch store and nothing from step 1's gate.

**Acceptance Criteria**:

- [ ] With a stub `claude` recording its arguments, `niwa resume <instance name>` for a dispatched worker whose instance was destroyed restores the directory at the original path, writes the Claude trust entry for it, runs `claude --bg --resume <session id>` with `--permission-mode`, `--name=`, `--model=` and `--settings` from the record and `--` before the prompt, prints the session id, the permission mode and the restored commits, and says unpushed work isn't restored.
- [ ] `niwa resume <session id>` and `niwa resume <session name>` resume the same worker; with the directory still present, nothing is restored and no trust entry is written.
- [ ] The launched mode is the stricter of the recorded mode and the mode the current policy gives, in Claude Code's order `plan`, `default`, `acceptEdits`, `bypassPermissions`. For each pair the tests cover, recorded then current: `bypassPermissions` then `default` launches `default` with a stderr notice naming both; `acceptEdits` then `plan` launches `plan` with the notice; `acceptEdits` then `bypassPermissions` launches `acceptEdits` with no notice; `default` then `default` launches `default` with no notice. A recorded mode outside that list refuses the record.
- [ ] `niwa resume` exits non-zero naming the argument for: no launch record; two matching records (both listed); a gone transcript; a running session; something other than the instance (a file, a directory without the instance's state, or a symlink) at the original path; a record naming an environment variable the current `[session.dispatch_env]` no longer passes; a record whose settings carry a key outside the set dispatch emits; a record whose model or session name doesn't start with a letter or digit; a record file not owned by the user or not mode 0600. In each case the stub `claude` records no call, no directory is created or restored, and no trust entry is written.
- [ ] The resumed instance has a session mapping with the original dispatch id, written before the launch.

**Check**: `go test -count=1 -run 'TestResume|TestLaunchRecord' ./internal/cli/ ./internal/workspace/` with a stub `claude` and a scratch workspace whose repositories are local bare repositories; the new tests pass.

**Dependencies**: Step 5

**Files**: `internal/cli/resume.go`, `internal/cli/dispatch_reentry.go`, `internal/cli/instance_from_hook.go`, `internal/workspace/launch_store.go`

### Step 7: `koto session needed` answers for a path from the runs anchored under it and their local successors

**Goal**: In tsukumogami/koto, a read-only `koto session needed <PATH>` exits 1 while a workflow session anchored at or under the path is running and no later member of its lineage (through the continues relation koto#481 added) is anchored outside it, 0 when no such session exists, and 2 when it can't decide, writing nothing. Independent of niwa's gate; niwa's `[session.before_remove]` calls it in step 10.

**Acceptance Criteria**:

- [ ] Given a path holding the execution anchor of a running session that nothing continues, the verb exits 1 and prints one line of at most 200 bytes naming that session; after a session anchored elsewhere continues it, or it reaches a terminal state, or it is cancelled, the verb exits 0.
- [ ] With v1 and v2 (continuing v1) under the path and v3 (continuing v2) outside it, the verb exits 0; with v3 also under the path and running, it exits 1; a successor with no recorded anchor leaves the path needed; a terminal successor outside the path still releases it.
- [ ] `/a/repo-2` holding a running session doesn't make `/a/repo` needed; a symlinked spelling gives the same answer; a session anchored above the path doesn't count; a path that can't be canonicalized exits 2.
- [ ] An unparseable session under the path, alongside readable ones, makes it exit 1 naming it as unreadable, never 0; a store it can't list makes it exit 2.
- [ ] Hashing the local store before and after shows no change, including on a store with an unreadable session, and the verb never calls a command that pulls, spools or restores.

**Check**: koto's hosted CI is the build of record. Every test builds its sessions, anchors and continues links as fixtures in its own temporary directory under a scratch `KOTO_HOME`, with every `KOTO_SESSION_*` name, `KOTO_SESSIONS_BASE` and `CLAUDE_CODE_SESSION_ID` unset, and asserts the resolved store is under that directory before running; no test reads or writes a real store or bucket. Locally only `cargo build`.

**Dependencies**: None

**Files**: `src/session/needed.rs`, `src/cli/session.rs`, `docs/guides/cli-usage.md`

### Step 8: `koto session needed` judges successors only the bucket knows and imported copies

In tsukumogami/koto, the verb follows successors the continues relation records in the bucket and an imported copy naming its source, so a run continued on another host or imported away releases its old path.

### Step 9: dot-niwa closes koto's agent-session anchors on reap and links dispatched workers to their coordinator

In tsukumogami/dot-niwa, the workspace declares koto's guarded anchor close as `[session.on_reap]` and passes `KOTO_DISPATCHER_ANCHOR` while unsetting `KOTO_ANCHOR` through `[session.dispatch_env]`, checked by a dispatched worker whose anchor shows its coordinator's as dispatcher and reads closed after a reap.

### Step 10: dot-niwa declares koto's verb as the pre-removal gate, and a paused run's instance is kept until its run moves or finishes

In tsukumogami/dot-niwa, once niwa and koto releases carry steps 1 to 4 and 7, the workspace declares `[session.before_remove]` running the guarded `koto session needed`, checked end to end in a scratch workspace across every reap and a plain destroy.

## Implementation Sequence

The step order is the sequence, and each step lands as its own pull request
in the repository named below. Dependencies name earlier steps; several lanes
can run beside each other.

| Step | Repository | Depends on | Waits for the continues relation? |
|------|------------|------------|-----------------------------------|
| 1 | tsukumogami/niwa | none | no |
| 2 | tsukumogami/niwa | Step 1 | no |
| 3 | tsukumogami/niwa | Step 1 | no |
| 4 | tsukumogami/niwa | Step 1 | no |
| 5 | tsukumogami/niwa | none | no |
| 6 | tsukumogami/niwa | Step 5 | no |
| 7 | tsukumogami/koto | none | its local half only, which koto#481 has landed |
| 8 | tsukumogami/koto | Step 7 | yes: the relation's bucket back-reference and import contract |
| 9 | tsukumogami/dot-niwa | none | no (needs niwa v0.36.0 or later, released) |
| 10 | tsukumogami/dot-niwa | Steps 1, 2, 3, 4, 7 and 9 | no, but it needs niwa and koto releases carrying steps 1 to 4 and 7 |

Steps 1 to 7 and 9 can be built before the continues relation finishes
landing; step 9 has no edge at all and can land first. Step 8 is the only step
that waits for the rest of the relation. Step 10 waits for releases rather
than for the relation, and must not be declared before step 7's successor
walk is released: without it a taken-over predecessor reads as running and
its instance is never reclaimed.

Lanes that can run beside each other: steps 2, 3 and 4 after step 1; steps 5
and 6; steps 7 and 8; step 9 at any time. Step 10 joins them.
