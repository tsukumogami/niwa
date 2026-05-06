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

### `niwa session list [--repo <name>] [--status active|ended|abandoned]`

Lists lifecycle sessions with optional filters. Without flags, this command
falls back to `niwa mesh list` (a deprecated alias) and prints a warning; use
`niwa mesh list` directly for that view.

```bash
niwa session list --status active
niwa session list --repo niwa
niwa session list --repo niwa --status active
```

Output columns: ID, REPO, STATUS, CREATED, PURPOSE.

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

Returns `{ "session_id": "ab12cd34", "worktree_path": "/path/to/worktree" }`.
If the daemon fails to start, the response also includes `"daemon_warning"` but
the session state is still written — the coordinator can retry daemon startup or
proceed with caution.

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

Returns a JSON array of `SessionLifecycleState` objects. Returns an empty array
(not null) when no sessions match.

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
