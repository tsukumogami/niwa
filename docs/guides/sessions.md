# Sessions

A session gives a worker agent persistent Claude conversation context and an
isolated git worktree. Instead of each delegated task starting a fresh Claude
process in the shared repo clone, tasks delegated to a session all run in the
same worktree and pick up the same conversation history.

## What a session is

When the coordinator creates a session for a repo, niwa:

1. Creates a new git branch `session/<session-id>` from the current HEAD of
   the repo.
2. Adds a git worktree at `<instance>/.niwa/worktrees/<repo>-<session-id>/`.
3. Starts a per-worktree daemon to handle task delivery in that worktree.
4. Writes a lifecycle state file at
   `<instance>/.niwa/sessions/<session-id>.json`.

All tasks subsequently delegated to that session run inside that worktree.
After the first task completes, niwa captures the worker's `CLAUDE_SESSION_ID`
into the state file. Every following task spawns the worker with
`claude --resume <id>`, so the conversation thread continues across tasks.

Two sessions for the same repo can run in parallel. Each gets its own branch,
its own worktree, and its own daemon.

## Lifecycle

```
niwa_create_session(repo, purpose)
         |
         v
    [status: active]
    worktree exists
    daemon running
         |
         | niwa_delegate(..., session_id=X)  ← repeat as needed
         |
         v
    tasks run with --resume continuity
         |
         v
niwa_destroy_session(session_id)
         |
         v
    [status: ended]
    worktree removed
    daemon stopped
    session branch left in git history
```

Sessions are terminal once ended. There's no resume-from-ended path; create a
new session if you need to continue work in a fresh worktree.

## Filesystem layout

After `niwa_create_session` completes, the following appear on disk:

```
<instance>/
  .niwa/
    sessions/
      <session-id>.json          # lifecycle state (status, worktree_path, ...)
    worktrees/
      <repo>-<session-id>/       # git worktree (the working directory)
        .niwa/
          roles/<repo>/inbox/    # task inbox for the session daemon
          tasks/
          sessions/
          daemon.pid
          daemon.log
```

The `sessions/<session-id>.json` state file is the source of truth for session
status. It records:

| Field | Description |
|-------|-------------|
| `session_id` | 8-character lowercase hex identifier |
| `repo` | Repo name this session is for |
| `purpose` | Human-readable description set at creation |
| `status` | `active`, `ended`, or `abandoned` |
| `creation_time` | RFC3339 timestamp |
| `worktree_path` | Absolute path to the worktree directory |
| `claude_conversation_id` | Set after the first worker exits; used for `--resume` |
| `parent_session_id` | Set when created from another session (optional) |

After `niwa_destroy_session` runs:

- The worktree directory is removed.
- `status` in the state file changes to `ended`.
- The `sessions/<session-id>.json` file stays on disk.

The state file persists so `niwa session list --status ended` still shows
closed sessions.

## The session branch in git

`niwa_destroy_session` does NOT automatically delete the `session/<session-id>`
branch from the repo. The branch is left in git history so you can review,
merge, or cherry-pick the work before discarding it.

Default destroy behavior uses `git branch -d`, which only removes the branch if
it's already merged into the current branch. If the branch has unmerged commits,
niwa leaves it in place and warns you:

- **CLI**: the warning is printed to stderr before the "session: destroyed" line.
- **MCP**: the `branch_warning` field is included in the returned
  `SessionLifecycleState` JSON alongside the regular state fields.

To remove the branch regardless of merge status, pass `--force` (CLI) or
`force: true` (MCP tool):

```bash
niwa session destroy <session-id> --force
```

To clean up branches manually after reviewing the work:

```bash
# From inside the repo (not the worktree):
git branch -d session/<session-id>    # safe: fails if unmerged
git branch -D session/<session-id>    # unsafe: deletes regardless
```

To list all session branches in a repo:

```bash
git branch --list 'session/*'
```

## CLI command reference

### `niwa session create <repo> <purpose>`

Creates a session for the named repo. Scaffolds the worktree, writes the state
file, starts the per-worktree daemon, and navigates your shell to the new
worktree directory (requires the niwa shell integration).

Use this when you want to open a persistent working context for a repo — for
example, to hand off a multi-step implementation task where context continuity
matters.

```bash
niwa session create niwa "implement the sessions feature"
```

The shell navigates to `<instance>/.niwa/worktrees/niwa-<session-id>/`
automatically if your shell integration is active. See `niwa shell-init` for
setup.

### `niwa session destroy <session-id> [--force]`

Destroys a session: kills any running workers, marks the state ended, stops the
daemon, and removes the worktree. By default, tries to delete the session branch
only if it's already merged (`git branch -d`). Pass `--force` to delete the
branch unconditionally (`git branch -D`).

Destroy is idempotent — calling it on an already-ended session returns the
current state without taking further action.

```bash
niwa session destroy ab12cd34
niwa session destroy ab12cd34 --force
```

### `niwa session list [--repo <name>] [--status …] [--attached|--available]`

Lists per-session lifecycle states with their attach availability.

```bash
niwa session list
niwa session list --status active
niwa session list --repo niwa
niwa session list --attached
niwa session list --available
```

Output columns: SESSION_ID, REPO, STATUS, AVAILABILITY, CREATED, PURPOSE.

The AVAILABILITY column has three values:

| Value | Meaning |
|-------|---------|
| `available` | No attach lock held. Free for `niwa session attach`. |
| `attached`  | A `niwa session attach` process holds the lock. The mesh queue is paused for this session. |
| `stale`     | A sentinel exists but the holder PID is dead. The lock is no longer effective; the next read reaps the sentinel. |

Sort order: attached sessions first (descending by attach start time),
then by status (active before terminal), then by creation time descending.
This surfaces "is anyone in there?" at the top of the table.

Filters AND-combine. `--attached` and `--available` are mutually
exclusive. Sessions with `AVAILABILITY=stale` appear under neither filter
— run without filters to see them.

For the coordinator process registry view (alive/dead daemons, pending
inbox counts), use `niwa mesh list` directly.

### `niwa mesh list`

Lists coordinator sessions registered in the instance — the coordinator process
registry. This is separate from the lifecycle session list. Use it to check
whether the coordinator is alive and how many pending tasks are queued.

```bash
niwa mesh list
```

Output columns: ROLE, PID, STATUS (alive/dead), LAST-SEEN, PENDING.

### `niwa go <repo> <session-id>`

Navigates your shell to the worktree directory for a session. Useful when
you've lost track of where a session's worktree is, or when scripting around
session directories.

```bash
niwa go niwa ab12cd34
```

The repo argument must match the session's repo. niwa rejects the navigation if
it doesn't, so a typo in the session ID doesn't silently land you in the wrong
directory.

## Human-in-the-Loop: Attaching to a Session

`niwa session attach <session-id>` lets an operator step into a mesh
session, resume Claude Code with the worker's full transcript history,
work interactively, and detach cleanly with the session returning to
normal mesh operation. This is the missing primitive for recovering
from edge cases where a worker has drifted off-task: instead of
destroying the session and losing all accumulated context, you take
the conversation over and redirect.

The attach command:

1. Validates the session is `active` and a transcript is loadable.
2. Acquires an exclusive flock on `<worktree>/.niwa/attach.lock`.
3. Terminates the per-worktree daemon so the mesh stops claiming
   envelopes for this session.
4. Writes a sentinel at `<worktree>/.niwa/attach.state` so the lock
   state is visible to `niwa session list` and `niwa_list_sessions`.
5. Spawns `claude --resume <conv_id>` with stdio inherited from your
   terminal and its working directory set to the worker's CWD.
6. On Claude Code exit, removes the sentinel, respawns the daemon,
   prints any worktree-state warnings, and propagates Claude's exit
   code (capped at 125).

### Attach

```bash
niwa session list                        # find the session you want to enter
niwa session attach <session-id>         # acquire lock, launch claude --resume
# [interactive Claude Code TUI; type /exit when done]
```

While you are attached:

- The per-worktree daemon is not running. Coordinator delegations to
  this session via `niwa_delegate` queue silently in the inbox.
- The mesh queue is invisible from inside the attached Claude Code
  session — that's intentional, to prevent dual-control surprises.
- Other operators see `AVAILABILITY=attached` with your PID in
  `niwa session list`.

Pass `--force` to SIGTERM a running worker before acquiring the lock
(default behaviour without `--force` is to wait for the worker to
finish naturally, polling every 1s and printing a status line every
5s). On the host this means: `niwa session attach <id> --force`.

### Detach

Normal release happens automatically when Claude Code exits — there's
no command to run. The explicit `niwa session detach` exists only as
an operator escape hatch for stale locks (SSH disconnect, terminal
crash, host reboot).

```bash
niwa session detach <session-id>            # auto-recover dead-holder lock
niwa session detach <session-id> --force    # break a live attach lock
```

`niwa session detach <id>` (no flag) succeeds silently when the
holder PID is dead and exits with code 3 if the holder is alive (with
a message pointing at `--force`).

`niwa session detach <id> --force` SIGTERMs the holder, waits
`NIWA_DESTROY_GRACE_SECONDS` (default 5 seconds), SIGKILLs if needed,
and exits with code 4 to signal that a live holder was killed (so
scripts can distinguish "reaped a stale lock" from "killed an active
session"). A warning line is printed to stderr.

### Discovering an Attached Session

`niwa session list` shows the AVAILABILITY column for every session.
The attached row is sorted to the top:

```text
$ niwa session list --status active
  SESSION_ID   REPO         STATUS     AVAILABILITY CREATED              PURPOSE
  ef56gh78     niwa         active     attached     30s ago              pair-debug edge case
  0c446995     niwa         active     available    2m ago               long-running learning log
```

To filter to only attached sessions, pass `--attached`. To filter to
only sessions free for attach, pass `--available`. They are mutually
exclusive.

For programmatic inspection, `niwa_list_sessions` (MCP) returns the
same data with an `attach` sub-object on each row:

```json
{
  "session_id": "ef56gh78",
  "status": "active",
  "attach": {
    "v": 1,
    "owner_pid": 12345,
    "owner_start_time": 9876543210,
    "started_at": "2026-05-10T14:32:11Z",
    "lock_path": ".niwa/attach.lock"
  },
  ...
}
```

The `attach` key is **absent** (not `null`) when no lock is held.

### Force Detach

Use `niwa session detach <id> --force` when:

- Your SSH session disconnected mid-attach and the niwa-attach
  process is still alive but unreachable.
- A teammate's terminal crashed and you need the session unlocked so
  the mesh can resume.
- You see `AVAILABILITY=attached` for a process that was clearly not
  cleanly exited (e.g. `pid=` in the listing matches no live process
  on your machine).

The command warns loudly to stderr before sending SIGTERM so it's
clear the operation is destructive. If `niwa session list` shows
`AVAILABILITY=stale`, `--force` is not needed — `niwa session detach <id>`
without flags reaps the dead-holder sentinel automatically.

### Destroy Interaction

`niwa session destroy <id>` against an attached session refuses
unless you pass `--force`. The MCP error code is `SESSION_ATTACHED`;
the message references the recovery command:

```text
$ niwa session destroy ef56gh78
niwa: error: session ef56gh78 is currently attached (pid=12345, started=...).
Run `niwa session detach ef56gh78 --force` to release the attach lock first,
or pass force=true to destroy regardless
```

### Failure Modes

When `niwa session attach <id>` cannot launch Claude Code, it emits
one of three niwa-shaped error messages so you know exactly what to
fix. (Three case strings reproduced verbatim from PRD R4.)

**Case A — no captured conversation id:**

```text
niwa: error: session <id> has no captured claude conversation id
(the worker may have crashed before MCP server startup; inspect with
`niwa session show <id>` or remove with `niwa session destroy <id>`).
```

This means the session was created but the first worker crashed
before the MCP server could capture `$CLAUDE_SESSION_ID`. There's no
transcript to resume; create a fresh session.

**Case B — transcript file missing:**

```text
niwa: error: claude transcript missing for session <id>
(expected: ~/.claude/projects/<encoded>/<conv_id>.jsonl). Claude may
have purged the transcript or the worktree was moved. Start a fresh
session with `niwa session create` or remove with `niwa session
destroy <id>`.
```

This means niwa captured the conversation id but Claude Code's
transcript file is gone. Most often: a `claude project purge` ran, or
the worktree was relocated. Create a fresh session.

**Case C — transcript file empty:**

```text
niwa: error: claude transcript is empty for session <id>
(path: ~/.claude/projects/<encoded>/<conv_id>.jsonl). The transcript
was started but no records were written. Start a fresh session with
`niwa session create`.
```

A zero-byte transcript means Claude Code opened the file but never
wrote a record (e.g. crashed during initialisation). Create a fresh
session.

### Scenario Walkthroughs

These scenarios show the exact terminal output for the seven cases
the PRD's acceptance criteria cover. They are the operator's
reference for what `niwa session attach` and `niwa session detach`
look like in practice.

#### 1. Happy path: stuck worker → attach → redirect → exit

```text
$ niwa session list
  SESSION_ID   REPO         STATUS     AVAILABILITY CREATED              PURPOSE
  ef56gh78     niwa         active     available    14m ago              implement attach feature

$ niwa session attach ef56gh78
session: attached ef56gh78 at /home/op/work/niwa-1/.niwa/worktrees/niwa-ef56gh78
[claude --resume <conv_id> takes over the terminal]
[user types instructions, claude responds, user types /exit]
session: detached ef56gh78
$ echo $?
0
```

#### 2. Pair-debug: attach to running session, wait for worker

```text
$ niwa session attach ef56gh78
niwa: waiting for worker on task <task_id>...
niwa: waiting for worker on task <task_id>...
session: attached ef56gh78 at /home/op/work/niwa-1/.niwa/worktrees/niwa-ef56gh78
[claude takes over]
```

A status line prints every 5 seconds while the worker is alive.

#### 3. Force-on-running-worker

```text
$ niwa session attach ef56gh78 --force
warning: --force: terminating worker on task <task_id> pid=12345
session: attached ef56gh78 at /home/op/work/niwa-1/.niwa/worktrees/niwa-ef56gh78
[claude takes over]
```

#### 4. Hand-fix-and-hand-back: uncommitted edits on detach

```text
[inside claude, you edit a file but don't commit, then /exit]
warning: worktree has uncommitted changes
   M README.md
session: detached ef56gh78
$ echo $?
0
```

The next mesh worker spawned in this session inherits the dirty
tree. Detach does not stash, prompt, or abort.

#### 5. Terminal crash recovery

```text
[your SSH session disconnects mid-attach]
[from another terminal:]
$ niwa session list --status active
  SESSION_ID   REPO         STATUS     AVAILABILITY CREATED              PURPOSE
  ef56gh78     niwa         active     stale        25m ago              implement attach feature

$ niwa session detach ef56gh78
$ niwa session list --status active
  SESSION_ID   REPO         STATUS     AVAILABILITY CREATED              PURPOSE
  ef56gh78     niwa         active     available    25m ago              implement attach feature
```

(Stale-lock listing reaps the sentinel as a side-effect; the explicit
detach also works and is what to use when the listing reports `stale`
and you want to be sure.)

#### 6. Force-detach a live holder

```text
$ niwa session detach ef56gh78
niwa: error: session ef56gh78 is currently attached (pid=12345, started=2026-05-10T14:32:11Z).
Run `niwa session detach ef56gh78 --force` to break the lock.
$ echo $?
3

$ niwa session detach ef56gh78 --force
warning: detaching live attach holder pid=12345 started=2026-05-10T14:32:11Z
$ echo $?
4
```

Exit code 4 specifically signals "killed live holder" so scripts can
distinguish it from the silent stale-lock cleanup case (exit 0).

#### 7. Pre-attach validation: ended session

```text
$ niwa session attach ef56gh78
niwa: error: session ef56gh78 has status ended; attach requires status active.
(For ended sessions, the worktree was removed on destroy; create a new session instead.)
$ echo $?
1
```

`abandoned` sessions get a similar message (no writer for that state
exists today; the AC is a regression guard).

### Exit Codes

| Code | Meaning |
|------|---------|
| 0    | Clean exit (Claude Code returned 0 with no warnings). |
| 1    | Pre-flight validation failure (status not active, transcript missing/empty, no conv_id). |
| 2    | Usage error (e.g. `niwa session detach` with no session id). |
| 3    | Lock contention (attach lock held by a live process). |
| 4    | `niwa session detach --force` killed a live holder. |
| 1-125 | Propagated from Claude Code (codes ≥ 126 are clamped to 125 to avoid shell-reserved codes). |

## MCP tools (coordinator use)

Coordinators interact with sessions through three MCP tools.

### `niwa_create_session`

```json
{
  "repo": "niwa",
  "purpose": "implement the sessions feature",
  "parent_session_id": "..."  // optional
}
```

Returns `{ "session_id": "ab12cd34", "worktree_path": "/path/to/worktree" }`
on success.

If the daemon's spawn pre-flight fails (mkdir, log open, `cmd.Start()`),
the response includes a soft `"daemon_warning"` and the session state is
still written — the coordinator can retry daemon startup or proceed
with caution.

If the daemon process starts but never reaches steady state (typically
inotify exhaustion: the daemon writes its PID file only after fsnotify
watcher registration succeeds, and it never gets there), the call
returns an `errResult` with structured error code `DAEMON_SPAWN_TIMEOUT`
after a 500 ms wait. The worktree, branch, and session-state file are
rolled back automatically — the operator does not need to clean up. The
recovery action is to inspect `<worktree-path>/.niwa/daemon.log` for
the spawn trace, raise the inotify limit if relevant, and re-call
`niwa_create_session`.

### `niwa_destroy_session`

```json
{
  "session_id": "ab12cd34",
  "force": false
}
```

Returns the final `SessionLifecycleState`. Idempotent on already-terminal
sessions. If `force` is `false` and the session branch has unmerged commits, the
response also includes `"branch_warning"` with a message and the manual deletion
command.

### `niwa_list_sessions`

```json
{
  "repo": "niwa",      // optional
  "status": "active"   // optional: active, ended, abandoned
}
```

Returns a JSON array of session entries. Each entry embeds the persisted
`SessionLifecycleState` plus a computed `daemon` sub-object reflecting
the per-worktree daemon's runtime liveness:

```json
{
  "session_id": "ab12cd34",
  "status": "active",
  "daemon": {
    "alive": true,
    "pid": 12345,
    "started_at": "2026-05-09T10:00:00Z"
  },
  "...": "..."
}
```

The `status` field stays the lifecycle marker (`active`/`ended`/`abandoned`),
written only by `niwa_create_session` and `niwa_destroy_session`.
`daemon.alive` is the orthogonal runtime probe — a session whose
daemon was killed mid-life reports `daemon.alive=false` while
`status=active`. Callers should check `daemon.alive` before queuing new
work into a session.

Empty filter result returns an empty array (not null).

### `niwa_redelegate`

```json
{
  "source_task_id": "abc123",
  "to": "web",                   // optional override
  "session_id": "ab12cd34",      // optional override
  "read_only": false,            // optional override
  "body_overrides": { ... },     // optional shallow merge
  "mode": "async",
  "expires_at": "2026-05-09T..."  // optional
}
```

Re-fires a previously-delegated task body without rewriting it. Source
state may be any of queued/running/completed/abandoned/cancelled — the
source's state is unchanged, so active sources keep running while the
new task runs independently.

Returns `{task_id, redelegated_from, source_state_at_fork}` so callers
can distinguish recovery flows (terminal source) from active forks
(queued/running source). Authorization is `kindDelegator` on
`source_task_id`.

When the source's `envelope.json` is missing (the rare `taskstore_lost`
recreate-stub case described under [Task store loss](#task-store-loss)
below), the call returns `SOURCE_BODY_LOST` and the caller must supply
the body via `body_overrides`.

### `niwa_delegate` with `session_id`

When `session_id` is passed to `niwa_delegate`, the task routes to that
session's worktree daemon inbox rather than the main instance daemon. Omit it
to route to the main instance as usual.

## When the session daemon crashes

If the per-worktree daemon dies after a session is created:

- The worktree stays on disk.
- The state file stays with `status: active`.
- No new tasks can be delivered until the daemon restarts.

To inspect what happened, check the daemon log inside the worktree:

```
<worktree>/.niwa/daemon.log
```

To clean up without restarting, destroy the session:

```bash
niwa session destroy <session-id>
```

Destroy reads the state file for the worktree path, kills any workers still
listed as running, stops whatever daemon process it can find, removes the
worktree, and marks the session ended. It doesn't require the daemon to be
alive.

If destroy itself fails (for example, the worktree path is gone but the state
file references it), use `--force`. Force destroy uses `git branch -D` and
proceeds past filesystem errors rather than stopping on the first one.

## Task store loss

The mesh stores per-task state under `<workspace>/.niwa/tasks/<id>/`. If
that directory is wiped while inbox envelopes still reference its task
IDs (manual cleanup, partial workspace destroy, fresh checkout that
gitignored the task store), the daemon classifies the orphaned envelope
as `taskstore_lost` and transitions the task to `state="abandoned"` with
`reason="taskstore_lost"`.

Read APIs surface this consistently — `niwa_query_task`,
`niwa_list_outbound_tasks`, and `niwa_await_task` all return the
abandoned terminal state. `niwa_cancel_task` and `niwa_update_task`
return `TASK_ALREADY_TERMINAL` rather than the legacy
`{too_late, queued}` contradiction earlier versions produced.

The recovery primitive is `niwa_redelegate(source_task_id=<id>)`. When
`envelope.json` is intact, the original body is reused verbatim. When
`envelope.json` is also missing (the worst case), redelegate returns
`SOURCE_BODY_LOST` and the caller supplies the body explicitly via
`body_overrides`.

## Worker config inheritance

Workers spawned by `niwa_delegate` inherit the workspace's full
`.claude/` tree — settings, plugins, skills, hooks, marketplaces, and
`CLAUDE.local.md` — equivalent to a user running `claude` directly in
the role's repo. The originating repo's `.claude/` layers on top for
session workers. The single carve-out is MCP server inheritance from
`~/.claude.json`, which is scoped away by `--strict-mcp-config`.

This contract is delivered via three argv flags `niwa` appends to every
`claude -p` spawn:

```
--add-dir <workspaceRoot>
--add-dir <repoPath>
--setting-sources user,project,local
```

Coordinators that mandate a workspace skill in delegation bodies
(e.g., body that says "use /shirabe:plan") can rely on it being
available. Declare the dependency via `body.required_skills: ["shirabe:plan"]`
to catch typos at queue time — the MCP server returns `MISSING_SKILLS`
synchronously with `{missing, available}` if any required entry is
absent.

## Parallel sessions for the same repo

You can run multiple sessions for the same repo simultaneously. Each session
gets a distinct branch and worktree:

```
.niwa/worktrees/
  niwa-ab12cd34/   # session ab12cd34 — "add authentication"
  niwa-ef56gh78/   # session ef56gh78 — "refactor config loading"
```

The sessions share nothing. Their git histories diverge from the same base
commit, their daemons are independent processes, and their task inboxes are
separate directories. Merging the branches back together is the operator's
responsibility.

## Contributor notes

The lifecycle state schema is versioned (`v: 1`). The `SessionLifecycleState`
struct in `internal/mcp/session_lifecycle.go` is the authoritative definition.
If you add fields, increment `V` and handle the zero-value of new fields when
reading existing state files.

Session IDs are 8-character lowercase hex strings generated from
`crypto/rand`. The ID is validated against `^[0-9a-f]{8}$` on every read to
guard against path traversal from caller-supplied values — don't relax this
check.

`SessionLifecycleState` (the lifecycle registry) is distinct from
`SessionEntry` (the coordinator process registry in `sessions.json`). The two
types have no shared fields and are written by separate code paths. `niwa mesh
list` reads `sessions.json`; `niwa session list` reads the per-session
`*.json` files.

The worktree `.niwa/` layout is deliberately minimal. It has no `mcp.json` and
no `workspace-context.md` — those are main-instance artifacts. The layout
contains only what's needed for task delivery: a role inbox, a task directory,
and daemon bookkeeping files.
