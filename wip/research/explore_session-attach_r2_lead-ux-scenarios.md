# Lead: 7 scenario walkthroughs with exact terminal output

## Conventions used in every scenario

These are the consistency rules the PRD should lock in. They are repeated here so each scenario is internally legible, but the rules themselves are cross-cutting:

- **Shell prompt:** `$ ` (single dollar-space). No `~/path %`-style prompt; the PRD-quoted snippets stay portable.
- **List columns:** `SESSION_ID  REPO  STATUS  AVAILABILITY  CREATED  PURPOSE`. Five-character + double-space separation. Column widths follow today's `writeSessionLifecycleTable` helper (`session_lifecycle_cmd.go:149-166`): 8 / 12 / 10 / 13 / 20 / rest. The new `AVAILABILITY` column is 13 wide to fit `attached(stale)`. Header is UPPER, body is lower.
- **Status vocabulary:** lowercase. Lifecycle: `active`, `ended`, `abandoned`. Availability: `idle`, `attached`, `attached(stale)`. Empty-string availability renders as `idle`. For `ended`/`abandoned` rows availability renders as `-`.
- **Stream discipline:** the table goes to stdout. Progress lines, warnings, prompts, errors go to stderr. Every stderr line written by `niwa session attach` is prefixed `attach: ` (analogous to `session: destroyed <id>` in `session_lifecycle_cmd.go:113`). Errors are prefixed `attach: error: `. Warnings are prefixed `attach: warning: `.
- **Exit codes:**
  - `0` — success (clean attach, clean detach, no-op idempotent calls).
  - `1` — generic failure (bad args, unknown session, internal error).
  - `2` — pre-condition failed (session not `active`, lock contention, worker still running with no `--force`).
  - `3` — user aborted at a prompt.
  - The exit code of the wrapped Claude Code child propagates through attach when Claude exits non-zero (capped at 125 to avoid colliding with niwa codes; the PRD should commit to this cap).
- **Confirmation prompts:** mirror `ReadConfirmation` in `internal/cli/destroy.go`. Type the session ID exactly to confirm. EOF or mismatch aborts (exit 3).
- **Time format:** `CREATED` is relative ("3m ago", "2h ago", "yesterday"); attach-related timestamps in stderr are absolute UTC `2026-05-09T14:32:11Z` for log-grep friendliness.

---

## Scenario 1: Happy path — stuck worker, attach, redirect, detach

### What the user sees

```
$ niwa session list
  SESSION_ID  REPO         STATUS     AVAILABILITY  CREATED              PURPOSE
  0c446995    vision       active     idle          2h ago               long-running learning log refac...
  f8b69f74    vision       active     idle          18m ago              F4 lifecycle metadata PRD secti...

$ niwa session attach f8b69f74
attach: acquiring lock on session f8b69f74 (vision)
attach: pausing worktree daemon (pid 28144)
attach: launching claude --resume 7e2a04b1-d0c5-4fda-9f6e-c0ed5a1f3b22
[claude --resume <conv_id> launches; transcript loads; cursor returns to user prompt]
attach: claude exited (code 0)
attach: releasing lock
attach: respawning worktree daemon
attach: detached cleanly from f8b69f74
$ niwa session list
  SESSION_ID  REPO         STATUS     AVAILABILITY  CREATED              PURPOSE
  0c446995    vision       active     idle          2h ago               long-running learning log refac...
  f8b69f74    vision       active     idle          19m ago              F4 lifecycle metadata PRD secti...
$
```

Stdout: the two `niwa session list` tables.
Stderr: every `attach: ...` line above (six lines pre-launch, three lines post-launch).
Exit code: `0`.

### State changes

Before `attach`:
- `<worktree>/.niwa/daemon.pid` references the worktree daemon pid 28144 (alive).
- No `<worktree>/.niwa/attach.lock`, no `<worktree>/.niwa/attach.state`.

During `attach`:
- `<worktree>/.niwa/attach.lock` created (zero-byte; flock held by `niwa session attach` process).
- `<worktree>/.niwa/attach.state` written atomically with `{attached_pid, attached_start_time, attached_at, attach_method: "interactive"}`.
- Worktree daemon (pid 28144) terminated via `TerminateDaemon` (SIGTERM, 5s grace, SIGKILL fallback). `<worktree>/.niwa/daemon.pid` becomes stale (file remains until respawn rewrites it).
- `<instance>/.niwa/sessions/f8b69f74.json` updated: `availability: "attached"`, `attach_owner_pid: <niwa-attach pid>`, `attach_owner_start_time: <jiffies>`, `attached_at: "2026-05-09T14:32:11Z"`. `V` bumped to `2`.

After clean detach (claude exits with `/exit` or Ctrl-D):
- `<worktree>/.niwa/attach.lock` removed.
- `<worktree>/.niwa/attach.state` removed.
- `<instance>/.niwa/sessions/f8b69f74.json` updated: `availability` cleared (omitempty → absent on disk). `V` stays at `2`.
- New worktree daemon spawned via `EnsureDaemonRunning`; its `scanExistingInboxes` catches up any envelopes the coordinator queued during attach.

### Why this UX

Six pre-launch stderr lines is the right verbosity floor: each line names a discrete on-disk effect (lock, daemon pause, claude launch). If any step fails, the failing line is the last on stderr, so the user knows where to look. The post-launch three lines (`exited`, `releasing lock`, `respawning daemon`, `detached`) mirror the acquire phase symmetrically — same number of effects unwound, same prefix. The final `detached cleanly from <id>` is the single line that scripts can grep for to confirm a clean exit.

The list-before / list-after framing is what makes attach safe to recommend in docs: the `AVAILABILITY` column visibly toggles `idle → attached → idle` in the user's terminal history, providing self-evidence that the lock acquired and released.

---

## Scenario 2: Pair-debug — attach to running session (wait-for-worker)

### What the user sees

```
$ niwa session list
  SESSION_ID  REPO         STATUS     AVAILABILITY  CREATED              PURPOSE
  f8b69f74    vision       active     idle          22m ago              F4 lifecycle metadata PRD secti...

$ niwa session attach f8b69f74
attach: acquiring lock on session f8b69f74 (vision)
attach: worker is running (task t-9ab3, pid 30412, started 47s ago)
attach: waiting for worker to finish (poll every 2s; Ctrl-C to abort, --force to terminate)
attach: still waiting (worker running, 12s elapsed)
attach: still waiting (worker running, 24s elapsed)
attach: worker finished (task t-9ab3, state: succeeded)
attach: pausing worktree daemon (pid 28144)
attach: launching claude --resume 7e2a04b1-d0c5-4fda-9f6e-c0ed5a1f3b22
[claude --resume <conv_id> launches; transcript loads; cursor returns to user prompt]
attach: claude exited (code 0)
attach: releasing lock
attach: respawning worktree daemon
attach: detached cleanly from f8b69f74
$
```

Stdout: only the first `niwa session list` table.
Stderr: all `attach: ...` lines.
Exit code: `0`.

Timing notes:
- The first `acquiring lock` line appears immediately. The flock is taken non-blockingly; lock acquisition itself does not wait.
- The `worker is running` detection is the *content* of the wait, not a side effect of the flock. The lock is held during the wait, which is what prevents a second user from racing in.
- `still waiting` lines are emitted at a 12-second interval (every 6 polls at 2s/poll). Not at every poll — that would spam.
- Ctrl-C during the wait drops the lock and exits with code `3` ("attach aborted by user").

### State changes

During the wait phase:
- `<worktree>/.niwa/attach.lock` exists (flock held).
- `<worktree>/.niwa/attach.state` written, but with `attach_method: "waiting_for_worker"` until the launch phase begins.
- The worktree daemon is **not** terminated yet — it must remain alive so the worker it spawned can finish writing its terminal `state.json`. Termination happens only after the worker reaches a terminal state.
- `<instance>/.niwa/sessions/f8b69f74.json` updated with `availability: "attached"` from the moment the lock is acquired (so a coordinator running `niwa_list_sessions` mid-wait sees the session as locked, even before claude launches).

Once the worker finishes, the rest of the state changes match Scenario 1.

### Why this UX

The wait is **observable, not silent**. Three principles:

1. The user is told *what* the lock is waiting on (`task t-9ab3, pid 30412, started 47s ago`). Without that, the user can't decide whether to wait or `--force`.
2. The user is told *how long* it has been waiting (`12s elapsed`, `24s elapsed`) at a sane cadence — every 12s, not every poll. This matches the user's mental model of "is anything happening?" without flooding the scrollback.
3. The user is told *what their options are* on the first wait line: Ctrl-C to abort, `--force` to terminate. They don't have to remember the flag set; the line tells them.

The decision to hold the flock during the wait (rather than poll lock-free and acquire later) is what prevents a second user from sneaking in. It also matches the lock-semantics lead's recommendation: "Reject on contention; do not queue or wait" applies to *attach contention*, not to *worker contention* — those are different races and the PRD must distinguish them.

---

## Scenario 3: Force-on-running-worker — `--force` flag

### What the user sees (SIGTERM succeeds)

```
$ niwa session attach f8b69f74 --force
attach: acquiring lock on session f8b69f74 (vision)
attach: worker is running (task t-9ab3, pid 30412, started 47s ago)
attach: --force: signaling worker process group with SIGTERM (5s grace)
attach: worker exited (task t-9ab3, state: failed, terminated by signal)
attach: pausing worktree daemon (pid 28144)
attach: launching claude --resume 7e2a04b1-d0c5-4fda-9f6e-c0ed5a1f3b22
[claude --resume <conv_id> launches; transcript loads; cursor returns to user prompt]
attach: claude exited (code 0)
attach: releasing lock
attach: respawning worktree daemon
attach: detached cleanly from f8b69f74
$
```

Stderr only. Exit code: `0`.

### What the user sees (SIGTERM stalls, SIGKILL fallback)

```
$ niwa session attach f8b69f74 --force
attach: acquiring lock on session f8b69f74 (vision)
attach: worker is running (task t-9ab3, pid 30412, started 47s ago)
attach: --force: signaling worker process group with SIGTERM (5s grace)
attach: worker did not exit within 5s of SIGTERM; sending SIGKILL
attach: worker exited (task t-9ab3, state: failed, killed by signal)
attach: pausing worktree daemon (pid 28144)
attach: launching claude --resume 7e2a04b1-d0c5-4fda-9f6e-c0ed5a1f3b22
[claude --resume <conv_id> launches; transcript loads; cursor returns to user prompt]
attach: claude exited (code 0)
attach: releasing lock
attach: respawning worktree daemon
attach: detached cleanly from f8b69f74
$
```

Stderr only. Exit code: `0`.

### Confirmation policy: `--force` is **immediate**, no prompt

The PRD should commit: `--force` does **not** prompt for confirmation. Three reasons:

1. The flag itself is the confirmation. Operators who type `--force` have already decided.
2. Destroy's typed-confirmation prompt fires only when there is *unrecoverable loss* on disk (uncommitted work, unpushed commits — see `internal/cli/destroy.go:319-339`). Killing a worker that hasn't yet committed is not a destroy-equivalent loss; the worker can be respawned by the coordinator and its envelope re-claimed.
3. The async case — operator scripting `niwa session attach <id> --force` from a runbook — would be made strictly worse by an interactive prompt.

If the operator wants typed confirmation, they should run `niwa session attach <id>` without `--force` first to see what's running, then run with `--force` knowingly.

### State changes

Same as Scenario 1, plus:
- `<instance>/.niwa/tasks/t-9ab3/state.json` transitions to `state: "failed"` with `terminated_by_signal: "SIGTERM"` (or `SIGKILL`). This is what the worker process records before exit, or what `killSessionWorkers`-equivalent code records after observing the worker has died.
- The task envelope in the worktree's inbox is **not** deleted by attach. It remains for the daemon's catch-up scan to re-examine on detach. (The coordinator may have already retried it via dangling-task classification per #112; PRD should align.)

### Why this UX

The force path mirrors the destroy path's worker-kill flow (`killSessionWorkers` / `killRunningWorkerPGIDs`) line for line, on purpose. SIGTERM with 5s grace matches `NIWA_DESTROY_GRACE_SECONDS`. The user-visible difference between SIGTERM-success and SIGKILL-fallback is one extra stderr line — important because if the worker required a SIGKILL, the operator should know (it implies the worker had a misbehaving cleanup path worth investigating).

The SIGTERM-success case shows `state: failed, terminated by signal`; the SIGKILL case shows `state: failed, killed by signal`. The vocabulary distinction matters for forensics and for #112 dangling-task classification.

---

## Scenario 4: Hand-fix-and-hand-back — uncommitted edits in worktree

### What the user sees (warn-and-allow path; default)

```
$ niwa session attach f8b69f74
attach: acquiring lock on session f8b69f74 (vision)
attach: pausing worktree daemon (pid 28144)
attach: launching claude --resume 7e2a04b1-d0c5-4fda-9f6e-c0ed5a1f3b22
[claude --resume <conv_id> launches; user manually edits files in another pane; user exits claude with /exit]
attach: claude exited (code 0)
attach: warning: worktree has uncommitted changes
attach: warning:   modified:   docs/designs/current/DESIGN-f4-lifecycle-metadata.md
attach: warning:   modified:   internal/mcp/session_lifecycle.go
attach: warning:   untracked:  scratch/notes.txt
attach: warning: changes are preserved on disk; the next worker will see them via git status
attach: warning: to discard, run: git -C <worktree> checkout -- . && git -C <worktree> clean -fd
attach: warning: to commit before detaching, re-attach and commit from inside claude
attach: releasing lock
attach: respawning worktree daemon
attach: detached cleanly from f8b69f74
$
```

Stderr only. Exit code: `0` (detach does **not** block).

### What "to commit before detaching" means

Detach is a one-way door: once `claude` exits, niwa releases the lock. The user has two paths if they want to commit before the next worker sees the dirty tree:

1. **In the moment**: don't exit claude. Run `git add . && git commit -m "..."` from inside the claude session (claude has shell access via the Bash tool) or in a sibling shell at the worktree path, then exit.
2. **After the fact**: re-attach with `niwa session attach <id>`. The lock acquires (no worker is running because the daemon is paused — wait, the daemon was *respawned* on detach). Actually: re-attach acquires the lock, terminates the just-respawned daemon, and the user gets back into claude with the dirty tree intact.

The PRD should explicitly call out: there is no `niwa session detach <id> --commit` shortcut. Re-attach is the path.

### Why this UX

Three principles directly inherited from the destroy precedent (`branch_warning` at `internal/mcp/handlers_session.go:287-292`):

1. **Warn loudly, do not auto-clean.** The warning lists every modified, staged, and untracked path. No truncation — if the user has 50 untracked files, all 50 lines print. The warning is verbose by design; the user just made manual changes and needs full visibility.
2. **Detach proceeds.** Blocking detach on dirty tree creates a deadlock with no escape — the user can't `niwa session detach --force` from inside claude, and a sibling-shell escape is unprincipled. The destroy command handles dirty trees by *requiring confirmation before destroying*, not by blocking; attach mirrors this.
3. **Tell the user the exact recovery commands.** The two `to discard` / `to commit before detaching` lines mean the user never has to hunt for the flag set. This matches the `branch_warning` message format ("review and delete manually: `git -C <repo> branch -D <branch>`").

The decision to render warnings on stderr (not stdout) is deliberate: scripts piping `niwa session attach` output won't pollute their stdout with warnings. The stderr stream is for humans; stdout stays clean.

---

## Scenario 5: Terminal crash mid-attach — stale lock recovery

### What the user (in a second terminal) sees

```
$ niwa session list
  SESSION_ID  REPO         STATUS     AVAILABILITY    CREATED              PURPOSE
  f8b69f74    vision       active     attached(stale) 23m ago              F4 lifecycle metadata PRD secti...

$ niwa session attach f8b69f74
attach: error: session f8b69f74 is locked
attach:   attached at:  2026-05-09T14:32:11Z (8m ago)
attach:   attach owner: pid 30412 (start time mismatch — process likely dead)
attach:   detected as:  stale (owner pid is not alive)
attach: hint: run "niwa session detach f8b69f74 --force" to break the stale lock
$ echo $?
2
$ niwa session detach f8b69f74 --force
attach: stale lock detected (owner pid 30412 is not alive)
attach: removing stale lock at /home/dan/dev/niwaw/tsuku/tsuku-2/.niwa/worktrees/vision-f8b69f74/.niwa/attach.lock
attach: removing stale state file at /home/dan/dev/niwaw/tsuku/tsuku-2/.niwa/worktrees/vision-f8b69f74/.niwa/attach.state
attach: clearing availability on session f8b69f74
attach: respawning worktree daemon
attach: stale lock cleared on f8b69f74
$ niwa session list
  SESSION_ID  REPO         STATUS     AVAILABILITY  CREATED              PURPOSE
  f8b69f74    vision       active     idle          24m ago              F4 lifecycle metadata PRD secti...
$ niwa session attach f8b69f74
attach: acquiring lock on session f8b69f74 (vision)
[...normal flow continues as Scenario 1...]
```

The first `niwa session list` shows `attached(stale)`, exit `0`.
The first `niwa session attach` (refusal): stderr only, exit `2`.
The `niwa session detach --force`: stderr only (and a final stdout-free success), exit `0`.
The second `niwa session attach`: continues as in Scenario 1.

### How `attached(stale)` is detected

`niwa session list` reads each session's `availability` field from `<instance>/.niwa/sessions/<id>.json`. For any session with `availability == "attached"`, the lister calls `IsPIDAlive(attach_owner_pid, attach_owner_start_time)` against `/proc`:

- `IsPIDAlive` returns `true` → render as `attached`.
- `IsPIDAlive` returns `false` → render as `attached(stale)`.

This is **passive detection** — the lister does not modify state; it just renders differently. Per the state-model lead, `IsPIDAlive` is the existing primitive used for creator-PID staleness checks, and reusing it here keeps the recovery model consistent.

The stale state is **not** auto-cleaned by the lister, because list operations should be read-only. Cleanup happens via `niwa session detach <id> --force`. (An attach attempt against a stale-locked session could opportunistically auto-clean and retry — the PRD should choose. Recommended: refuse with the helpful hint as shown, force the operator to confirm via the explicit `detach --force` step. This follows the destroy precedent where stale state is recovered via explicit `--force`, not silent retry.)

### Why this UX

The error message has three parts, in this order:

1. **What** is wrong (`session f8b69f74 is locked`).
2. **Diagnostic detail** (when it locked, who owns it, why it's been classified stale). Three indented lines, aligned, parseable by eye.
3. **What to do next** (`hint: run "niwa session detach ... --force"`). One line, ready to copy-paste.

This pattern — diagnostic-then-hint — is already in niwa via `errDaemonAlreadyRunning` ("another daemon is already running for this instance"). The attach error mirrors that shape so users who learned the daemon error pattern get attach for free.

The `detach --force` output narrates each on-disk effect (`removing stale lock`, `removing stale state file`, `clearing availability`, `respawning worktree daemon`) for the same reason as Scenario 1: each line names a discrete effect so debugging a partial failure is straightforward.

The decision to **not** prompt for typed confirmation when the lock is stale (`pid is not alive`) is deliberate: there is nothing to lose. Compare the live-lock case (Scenario 7), where typed confirmation is required.

---

## Scenario 6: Pre-attach validation — attach to ended session

### What the user sees

```
$ niwa session list --all
  SESSION_ID  REPO         STATUS     AVAILABILITY  CREATED              PURPOSE
  ab12cd34    vision       ended      -             3d ago               investigate flaky retry path
  f8b69f74    vision       active     idle          24m ago              F4 lifecycle metadata PRD secti...

$ niwa session attach ab12cd34
attach: error: session ab12cd34 is not active (status: ended)
attach:   ended at:    2026-05-06T11:08:42Z (3d ago)
attach:   worktree:    removed by destroy
attach:   branch:      session/ab12cd34 (deleted)
attach: hint: attach is permitted only on active sessions; the worktree and branch
attach:   are removed when a session ends, so claude --resume has no working tree to load.
attach:   to inspect history, read the session state file directly:
attach:     cat <instance>/.niwa/sessions/ab12cd34.json
attach:   to recover the conversation transcript (read-only):
attach:     cat ~/.claude/projects/$(echo -n '<worktree>' | basenc --base64url -w0)/<conv_id>.jsonl
$ echo $?
2
```

Stdout: the `niwa session list --all` table.
Stderr: the `attach: ...` block.
Exit code: `2`.

### State changes

**None.** The validation runs before any lock acquisition or state mutation. The session lifecycle file is read; no write occurs.

### Why this UX

This is the longest stderr message in any of the seven scenarios, and it should be: this is the path where the user's mental model is wrong (they think attach can resurrect history; the codebase forecloses on that). The PRD's contract is to set the right mental model. Three components:

1. **Plain refusal with reason** (`is not active (status: ended)`).
2. **Why it's impossible** — three indented diagnostic lines explaining what was destroyed (worktree, branch). This is the surface that prevents follow-up "but can't you just …?" questions.
3. **Two recovery paths** — the state JSON for metadata, the transcript JSONL for the actual conversation. The transcript path uses the `~/.claude/projects/<base64url(cwd)>/<conv_id>.jsonl` form from the round 1 transcript-persistence findings. Even though `claude --resume` can't load it (no CWD), `cat`-ing the JSONL gives the operator the content they actually wanted.

The `--all` flag on `session list` is needed to see ended sessions because the discovery-UX lead recommends defaulting to "active only" (rationale: ended sessions are noise during normal operation; `--all` opts back into the full view). If the PRD adopts a different default, this scenario's first command becomes plain `niwa session list` with no flag.

The exit code is `2` (pre-condition failure), matching the lock-contention case in Scenario 5 — both are "the session is in a state that doesn't permit attach right now," even though one is recoverable (lock can be broken) and one isn't (worktree is gone).

---

## Scenario 7: Concurrent attach — second terminal tries to attach

### What user B (or user A in another terminal) sees

```
$ niwa session list
  SESSION_ID  REPO         STATUS     AVAILABILITY  CREATED              PURPOSE
  f8b69f74    vision       active     attached      27m ago              F4 lifecycle metadata PRD secti...

$ niwa session attach f8b69f74
attach: error: session f8b69f74 is locked
attach:   attached at:  2026-05-09T14:32:11Z (5m ago)
attach:   attach owner: pid 30412 (alive)
attach:   detected as:  active (owner process is alive)
attach: hint: another niwa session attach is in progress for this session.
attach:   options:
attach:     wait until that attach detaches and retry
attach:     break the lock if you know the holder is idle:
attach:       niwa session detach f8b69f74 --force
attach: detach --force on a live attach will require typed confirmation against the session id.
$ echo $?
2
```

Stdout: the `niwa session list` table.
Stderr: the `attach: ...` block.
Exit code: `2`.

If user B then runs `niwa session detach f8b69f74 --force`:

```
$ niwa session detach f8b69f74 --force
attach: warning: lock holder is alive (pid 30412)
attach: warning: forcing detach will SIGTERM the holder process; any unsaved claude
attach:   prompt will be lost. The worktree and git state are preserved.
attach: type "f8b69f74" exactly to confirm:
> f8b69f74
attach: signaling lock holder pid 30412 with SIGTERM (5s grace)
attach: lock holder exited (lock auto-released by kernel)
attach: removing state file at /home/dan/dev/niwaw/tsuku/tsuku-2/.niwa/worktrees/vision-f8b69f74/.niwa/attach.state
attach: clearing availability on session f8b69f74
attach: respawning worktree daemon
attach: live lock cleared on f8b69f74
$ echo $?
0
```

The `> f8b69f74` line is the user's typed input echoed back from stdin (TTY echo); it is not produced by niwa.

If user B mismatches or hits EOF at the prompt:

```
$ niwa session detach f8b69f74 --force
attach: warning: lock holder is alive (pid 30412)
attach: warning: forcing detach will SIGTERM the holder process; any unsaved claude
attach:   prompt will be lost. The worktree and git state are preserved.
attach: type "f8b69f74" exactly to confirm:
> nope
attach: error: confirmation did not match session id; aborting
$ echo $?
3
```

### Multi-user note

Per the round 1 multi-user-safety lead, niwa's trust boundary is "same UID cooperative." Concurrent attach within a UID is the only case the PRD scopes; cross-UID attach is precluded by `0700` permissions on `.niwa/`. The error wording does not need to identify the holder by username because there is only one user; if cross-UID becomes in-scope later, the holder identity expands to `<user>@<host>` and the message extends naturally.

### State changes

**On rejection (no `--force`):** none. The flock acquisition fails non-blockingly with `EWOULDBLOCK`, niwa exits before any state write.

**On `detach --force` against a live lock, after typed confirmation:**
- `niwa session detach` sends SIGTERM to the holder pid (30412), waits 5s, SIGKILLs if needed.
- The holder process exiting drops the kernel-held flock automatically (no explicit unlock needed).
- niwa removes `<worktree>/.niwa/attach.state` (the holder may have already removed it during graceful SIGTERM cleanup; idempotent).
- `<instance>/.niwa/sessions/f8b69f74.json` updated: `availability` cleared.
- Worktree daemon respawned via `EnsureDaemonRunning`.

### Why this UX

The error wording is symmetric with Scenario 5's stale-lock case — same three-line diagnostic block (`attached at`, `attach owner`, `detected as`), just with `alive` and `active` instead of `not alive` and `stale`. This symmetry is what lets the PRD describe the lock as a single concept with one of two modes; users learn one error format.

Two-step gating on `detach --force` against a live lock is the load-bearing safety mechanism. The destroy command's typed-confirmation discipline (`internal/cli/destroy.go:319-339`) is the precedent: when there is unrecoverable user state at risk (claude prompt being typed but not yet sent), require typed confirmation. When there is no risk (stale lock — Scenario 5), skip the prompt.

The PRD must commit: typed confirmation fires when `IsPIDAlive(holder_pid, holder_start_time) == true`. When it returns false, the lock is provably stale and confirmation is skipped.

The user input form `f8b69f74` matches the destroy command's "type the exact name to confirm" pattern. The session ID is the right token because (a) it's the parameter the user already typed, and (b) it's unique within the workspace.

---

## Cross-scenario observations

### Pattern: stderr is the narration channel

Every scenario uses stderr for narration (lock acquired, daemon paused, claude launched, daemon respawned, detached). Stdout is reserved for tabular data (`niwa session list` output) and nothing else from the attach commands. This means:

- Scripting `niwa session attach <id>` and piping stdout gives you nothing — which is correct, because attach is interactive. Scripts that need attach state should `niwa session list --status active --attached` or similar.
- Scripting `niwa session list` and piping stdout gives you a clean parseable table that scripts can `awk` over. Warnings and errors stay on stderr where they belong.

### Pattern: every progress line names exactly one on-disk effect

Compare Scenario 1's pre-launch lines:
- `acquiring lock on session f8b69f74 (vision)` ← writes attach.lock + attach.state
- `pausing worktree daemon (pid 28144)` ← TerminateDaemon
- `launching claude --resume <id>` ← exec/spawn

This 1:1 mapping between log lines and disk effects means a partial failure (e.g., daemon refuses to terminate) leaves the user staring at a clear "the last thing it tried to do was X" line. Three lines, three effects, three potential failure points — no log line is decorative.

### Pattern: error messages have three sections (refusal / diagnostic / hint)

Scenarios 5, 6, 7 all use the same three-section error format:
1. One-line refusal naming the session and the reason class.
2. Indented diagnostic block (2-4 lines) with structured key:value pairs.
3. `hint:` block with copy-pasteable next commands.

This makes the errors learnable: once you've seen one, you can predict the structure of the next. The destroy command's error messaging follows the same shape; attach inherits it.

### Pattern: exit code 2 means "pre-condition failed, not your typo"

Scenarios 5 (lock contention), 6 (session not active), and 7 (concurrent attach) all exit `2`. The user can distinguish "I typed the command wrong" (exit 1, stderr says `unknown session id` or similar) from "the system is in a state that won't accept this command right now" (exit 2). This distinction matters for retry logic in coordinator scripts.

### Pattern: `attach: ` prefix on every stderr line

Every stderr line from `niwa session attach` (and `niwa session detach`) is prefixed `attach: `. Compare today's `session: destroyed <id>` from `session_lifecycle_cmd.go:113`. The prefix is the namespace; multi-step output is still legible when interleaved with other tool output. (If the user is running `niwa session attach` inside a wrapper script that also writes to stderr, `attach: ` lets them grep their wrapper's output cleanly.)

---

## Implications

### What the PRD must commit to from these scenarios

1. **Six-column list output**: `SESSION_ID | REPO | STATUS | AVAILABILITY | CREATED | PURPOSE`. The `AVAILABILITY` column adds 13 characters of width to today's table; PRD must either widen the terminal expectation or rebalance column widths.

2. **Three availability values**: `idle`, `attached`, `attached(stale)`. Empty-string availability renders as `idle`. For `ended`/`abandoned` rows, availability renders as `-`. The `(stale)` suffix is computed at render time via `IsPIDAlive`, not stored on disk.

3. **`--force` is immediate, no prompt** for `niwa session attach <id> --force`. Confirmation is the flag itself.

4. **`detach --force` requires typed confirmation when lock holder is alive**, no confirmation when lock is stale. The decision threshold is `IsPIDAlive(holder_pid, holder_start_time)`.

5. **Detach on uncommitted changes warns and proceeds** — never blocks, never auto-stashes. Mirrors the `branch_warning` precedent. Warning lists every modified, staged, untracked path on its own stderr line.

6. **Wait-for-worker behavior is observable**: stderr emits `still waiting (worker running, <N>s elapsed)` at 12-second intervals during the wait. First wait line tells the user their three options (wait, Ctrl-C, `--force`).

7. **Pre-attach validation refuses ended/abandoned sessions** with exit `2` and a multi-line stderr message naming the destroyed worktree, the deleted branch, and the `cat` recipes for forensic inspection.

8. **Stream discipline**: stdout is for tables only. All `niwa session attach` and `niwa session detach` output goes to stderr, prefixed `attach: `.

9. **Exit codes**: `0` = success, `1` = generic/argument error, `2` = pre-condition failed (lock contention, wrong status, worker running without `--force`), `3` = user aborted at prompt. Wrapped claude exit code propagates capped at 125.

### What the PRD's acceptance criteria look like

The seven scenarios above are the acceptance criteria. The PRD should quote them verbatim under "User-Visible Behavior — Acceptance" and have the implementation lead reproduce each scenario's terminal output character-for-character (modulo session IDs, paths, timestamps) in a functional test harness. The `localGitServer` helper noted in the niwa testing guide can host the worktree; the `NIWA_BIN` envvar pattern lets the test exec the compiled binary against a fixture workspace.

### What DOES NOT belong in the PRD's acceptance scenarios

These are deliberately omitted from the seven and should stay out of v1:

- Cross-instance attach (`niwa session attach --instance <name> <id>`) — the issue scopes v1 to current-instance.
- Multi-user concurrent attach across UIDs — precluded by 0700 permissions.
- Forensic attach to ended sessions in any "read-only" mode — physically impossible without redesigning destroy.
- Attach to a session whose `claude_conversation_id` is empty (worker has not yet registered) — should be its own scenario in a v2 exploration; the discovery-UX lead flagged this.
- Programmatic / MCP attach hooks — the issue explicitly puts this out of scope.

---

## Surprises

1. **The `--force` flag means two different things on different commands.** On `niwa session attach`, `--force` SIGTERMs the running worker and proceeds. On `niwa session detach`, `--force` breaks the attach lock (SIGTERMs the holder if alive, just removes files if dead). These are *different* effects on *different* targets. The PRD should be explicit that `attach --force` and `detach --force` are not symmetric. Naming alternatives like `--evict-worker` for attach and `--steal` for detach would be more precise but break with niwa's existing `--force` vocabulary.

2. **Detach has no idempotent no-op path documented anywhere yet.** What does `niwa session detach <id>` do when the session isn't attached? Probably exit `0` with a stderr line ("nothing to detach; session is idle"). The PRD should commit. Today's destroy is idempotent on `ended` sessions (`handlers_session.go:249`); detach should be idempotent on `idle` sessions for the same reason.

3. **The "warn and allow" detach behavior in Scenario 4 means the next worker spawn inherits a dirty tree.** This is not a bug — workers can `git status` and decide — but it's a behavior change relative to today, where the worktree is always clean at worker-spawn time (because workers spawn fresh on session create). The PRD should explicitly document this and the niwa-mesh skill should be updated to instruct workers to check tree cleanliness on startup.

4. **The `cat <instance>/.niwa/sessions/<id>.json` recipe in Scenario 6's hint exposes implementation paths to users.** This is intentional — the operator at this point is in forensic mode, not discovery mode — but the PRD might prefer a `niwa session inspect <id>` command that wraps the `cat`. If so, the hint changes from `cat ...` to `niwa session inspect ab12cd34`. Equivalent UX, less leakage.

5. **The 12-second cadence in Scenario 2's `still waiting` lines is arbitrary.** Could be 10s, could be 30s. The PRD should pick a number and document it; 12 was chosen here because it's frequent enough to feel responsive but rare enough not to flood the scrollback over a long wait. The cadence should be env-overridable for testing (`NIWA_ATTACH_WAIT_INTERVAL_SECONDS`).

6. **Scenario 3 shows `state: failed, terminated by signal` for SIGTERM and `state: failed, killed by signal` for SIGKILL.** This vocabulary distinction does not exist in niwa today (workers terminated during destroy don't carry a signal annotation in their final `state.json`). The PRD adds a small hidden surface: workers' final state needs to record *how* they were terminated. If that's too invasive, the simpler form is `state: failed, terminated_by: SIGTERM` / `terminated_by: SIGKILL` as a single field.

7. **Scenario 7's typed confirmation against the session ID** is the right pattern, but session IDs are 8-character hex — easy to mistype. The destroy command confirms against the workspace name, which is human-readable. If the operator types `f8b69f73` instead of `f8b69f74`, the confirmation fails (correct behavior — they don't have the right session in their head). But this is a higher friction than destroy's name-confirmation. The PRD could relax to "type 'detach' to confirm" if the typed-id friction proves too high in practice.

---

## Open Questions

1. **Does `niwa session detach <id>` (without flags, from outside the attached terminal) exist as a command?** The seven scenarios cover `niwa session detach <id> --force` (steal/recover). What about a no-force detach from outside? Could be useful for "ask the holder to detach gracefully" (send SIGTERM, no SIGKILL). The PRD should commit.

2. **Should `niwa session list` show stale-locked sessions in the default view, or only with `--all`?** Scenario 5 implicitly shows `attached(stale)` in the default view. This is correct (it's the operator's hottest signal — "something is wrong here"), but the PRD should commit explicitly.

3. **Does claude's exit code propagate to `niwa session attach`'s exit code?** Scenario 1 shows `claude exited (code 0)` and niwa exits `0`. What if claude exits `1` (claude's own error)? The convention chosen above is "propagate, capped at 125." The PRD should commit; alternatives include "always exit 0 if attach succeeded regardless of claude's exit" (treat claude's exit as inside-the-session and not niwa's concern).

4. **What happens if the user runs `niwa session attach <id>` from a shell that's already CWD'd inside the worktree?** The attach should succeed (the chdir is a no-op). The PRD should commit that attach is CWD-independent.

5. **Should the wait-for-worker phase show worker progress?** Scenario 2 shows `still waiting (worker running, 12s elapsed)`. Could also show worker's last `state.json` timestamp or last log line. Probably out of scope for v1; the wait is opaque-by-design (the human is waiting for the worker to *finish*, not to inspect mid-flight).

6. **Should attach acquire the lock before or after the wait-for-worker phase?** Scenario 2 puts lock acquisition first. Alternative: poll worker state lock-free until terminal, then acquire the lock. The first approach (lock first) prevents a second user from sneaking in during the wait; the second approach allows the second user to wait alongside, with whoever's poll-completes-first acquiring. Lock-first is what's documented above and matches the "reject contention immediately" lock-semantics finding. The PRD should commit.

7. **What about the scenario where the worker `claude_conversation_id` is empty?** An attach to a session whose worker has not yet registered would have nothing for `claude --resume` to load. This is mentioned in the discovery-UX lead and Surprises above; the seven scenarios assume a non-empty `claude_conversation_id`. The PRD should add an eighth scenario or fold it into Scenario 6 (pre-attach validation: "no transcript yet").

---

## Summary

The dominant interaction pattern is six-stderr-lines pre-launch, claude-runs-in-terminal, three-stderr-lines post-detach — every line names exactly one on-disk effect, so partial failures are always diagnosable from the last printed line. The biggest UX risk is that `--force` means different things on `attach` and `detach` (kill worker vs. steal lock); the PRD must call this out explicitly because the symmetry instinct will mislead operators. The biggest open question is whether `niwa session attach` should propagate claude's exit code or always exit zero on successful detach — Scenario 1 picks "propagate capped at 125" as a default, but this is the kind of policy choice that's easy to invert and the PRD should pick deliberately rather than inherit.
