# Decision 1: niwa_delegate Routing

## Context

Today, `handleDelegate` in `internal/mcp/handlers_task.go` calls `createTaskEnvelope`,
which builds a `task.delegate` message and atomic-renames it into
`<s.instanceRoot>/.niwa/roles/<to>/inbox/<taskID>.json`. The path is assembled
entirely from `s.instanceRoot` — the MCP server's instance root, which is the main
workspace instance root for a coordinator's session.

The daemon (`internal/cli/mesh_watch.go`) runs as `niwa mesh watch --instance-root=<path>`.
At startup it registers fsnotify watches on every `<instance-root>/.niwa/roles/<role>/inbox/`
directory. When a `.json` file appears in a watched inbox, the daemon claims it by
renaming `inbox/<id>.json` to `inbox/in-progress/<id>.json`, transitions state to
`running`, and spawns a worker process. The worker reads its task envelope by calling
`niwa_check_messages`, which has a special-case branch that surfaces the in-progress
file for the worker's own `NIWA_TASK_ID`.

Session worktrees live at `<instance>/.niwa/worktrees/<repo>-<session-id>/`. Each
worktree has its own `.niwa/` subtree including `roles/<repo-role>/inbox/`. A
per-worktree daemon is started via `EnsureDaemonRunning` at session creation time
(`internal/workspace/daemon.go`), watching the worktree's own inbox directory.

The problem: `createTaskEnvelope` hardcodes the inbox path as
`s.instanceRoot/.niwa/roles/<to>/inbox/`. When a coordinator's MCP server has
`s.instanceRoot` pointing at the main instance, and the task is meant for a session
worktree, the envelope lands in the main instance's inbox — where the main instance
daemon picks it up, not the per-worktree daemon. The per-worktree daemon never sees
the task.

Role validation (`isKnownRole`) also uses `s.instanceRoot`, so a role that only exists
in the worktree's `.niwa/roles/` won't pass the check either. Both the role validation
and the inbox write path need to change together.

`internal/mcp/session_registry.go` provides `WriteSessionEntry` and
`lookupLiveCoordinator`. There is no existing API for resolving a session ID to a
worktree path. Session state files will live at
`<instance>/.niwa/sessions/<session-id>.json` (per PRD R23); reading one is
straightforward filesystem access.

## Key Assumptions

- The per-worktree daemon is already running when `niwa_delegate(session_id=X)` is
  called. `niwa_create_session` starts it; `niwa_delegate` may need to call
  `EnsureDaemonRunning` defensively, but the daemon is not expected to be absent in
  the normal path.
- The worktree path is `<mainInstanceRoot>/.niwa/worktrees/<repo>-<session-id>/`
  and is recorded in the session state file at `<mainInstanceRoot>/.niwa/sessions/<session-id>.json`.
- The coordinator's MCP server `s.instanceRoot` is always the main instance root, not
  a worktree root. Coordinators do not run inside session worktrees.
- A `session_id` in `niwa_delegate` is a niwa session handle (8 hex characters), not
  a Claude conversation ID. The coordinator never handles the Claude conversation ID.
- The existing task store path (`<instanceRoot>/.niwa/tasks/<id>/`) remains rooted in
  the main instance. Only the inbox write and role validation need to target the
  worktree.
- `niwa_delegate` without `session_id` must write to `s.instanceRoot` paths exactly
  as it does today.

## Options Considered

### Option A: Direct Inbox Write

When `session_id` is present, `handleDelegate` reads the session state file to resolve
the worktree path, constructs the inbox path as
`<worktreePath>/.niwa/roles/<role>/inbox/`, and writes the task envelope there
directly. Role validation switches to checking `<worktreePath>/.niwa/roles/<role>/`
instead of the main instance path. The per-worktree daemon picks up the envelope via
its existing fsnotify watch. The task store directory (`<mainInstanceRoot>/.niwa/tasks/<id>/`)
stays in the main instance root, as today.

**Pros:**

- Minimal code change. `createTaskEnvelope` already accepts the inbox path
  implicitly via `s.instanceRoot`; the only addition is resolving `worktreePath`
  and substituting it for the inbox-directory construction. Everything else —
  the atomic rename, the `writeMessageAtomic` function, the daemon watch loop —
  is reused unchanged.
- No new coordination layer. The coordinator writes directly to the filesystem
  path the worktree daemon watches. There is no intermediary that can fail or
  introduce delay.
- Survives reboots cleanly. The envelope is a file in the worktree's inbox.
  When the worktree daemon restarts after a reboot, its catch-up inbox scan
  finds any envelope that arrived while it was down and claims it.
- Backward compatible by construction. The `session_id` gate (`if args.SessionID
  != ""`) means the no-session-id path is unchanged.
- No network service or IPC. Satisfies the explicit constraint against running
  network services between daemons.
- Consistent with how `niwa_ask` already routes to the coordinator's inbox
  by resolving a path from `lookupLiveCoordinator` — cross-instance routing
  via known filesystem paths is already established pattern in this codebase.

**Cons:**

- The coordinator's MCP server writes into a path it does not "own" (the
  worktree daemon's inbox). This is a mild ownership violation: if the worktree
  path in the session state file is stale or corrupted, the coordinator writes
  to a path the worktree daemon may not be watching.
- `handleCancelTask` and `handleUpdateTask` also construct the inbox path from
  `s.instanceRoot`. For session-routed tasks, these handlers would need the same
  worktree-path lookup to rewrite or cancel the queued inbox file. The task
  store's `state.json` must record the worktree path or session ID so these
  handlers can reconstruct the correct inbox path without calling `handleDelegate`
  again.

**Evidence from codebase:** `lookupLiveCoordinator` in `session_registry.go` already
follows this pattern — it constructs an inbox path from a registry lookup and returns
it to the caller, which then writes into it. The coordinator calls `writeAskNotification`
using a path it looked up, not a path it owns. Option A generalises this existing pattern
to delegate routing.

---

### Option B: Main Daemon as Router

The coordinator writes the task envelope to the main instance's inbox (current
behavior) with a `session_id` field added to the envelope. The main daemon reads
the `session_id`, looks up the worktree path, and forwards a copy of the envelope
to the worktree daemon's inbox.

**Pros:**

- Coordinator always writes to the path it owns; no cross-instance writes from
  `handleDelegate`.
- Role validation remains against `s.instanceRoot`.

**Cons:**

- Adds a significant new responsibility to the main daemon. The daemon's event
  loop (`mesh_watch.go`) currently only claims envelopes for roles it watches and
  spawns workers. Forwarding envelopes for roles in other instance roots is
  entirely new logic: read the `session_id`, resolve the worktree path, write
  into the worktree's inbox, and either delete or move the original. This is
  substantially more code than Option A.
- Creates a sequential dependency: the main daemon must process the envelope and
  forward it before the per-worktree daemon can claim it. If the main daemon is
  slow or restarting, the task is delayed even though the worktree daemon may be
  fully ready.
- The main daemon must not claim envelopes addressed to worktree roles. A role
  that lives only in a worktree must be invisible to the main daemon's watcher;
  if the main daemon accidentally watches or claims a worktree inbox, task
  ownership becomes ambiguous.
- Two writes (main inbox + worktree inbox) means two filesystem events and two
  chances for a crash to leave the system in an inconsistent state. A crash after
  the main daemon reads the envelope but before it writes to the worktree inbox
  leaves the task stranded.
- Not obviously compatible with `niwa_cancel_task` and `niwa_update_task`, which
  write directly to the inbox path recorded in `state.json`. If the task was
  forwarded, `state.json` must record the forwarded inbox path, not the original.

---

### Option C: Shared Pending Directory

Task envelopes addressed to a session are written to a shared
`<mainInstanceRoot>/.niwa/sessions/pending/<session-id>/` directory. The
per-worktree daemon polls or watches that directory for envelopes addressed to its
session ID and moves them to its own inbox when claimed.

**Pros:**

- Coordinator writes to a location it fully owns (the main instance's `.niwa/`
  directory).
- Clean ownership: the pending directory is neutral territory; either daemon can
  read it without crossing into the other's owned paths.

**Cons:**

- Requires a new watcher or poll loop in the per-worktree daemon. The daemon
  currently only watches its own `roles/<role>/inbox/` directories. Adding a
  shared-pending watch means the daemon must know its own session ID at startup
  and register an additional watch — new startup logic and a new env-var
  dependency.
- Polling introduces latency; fsnotify on the shared directory works but adds
  a non-trivial code path for the worktree daemon to distinguish its own envelopes
  from others in a shared space.
- The "move to own inbox" step after claiming from the pending directory is an
  extra hop. A crash between the move and the claim leaves an orphaned envelope
  in the worktree's inbox that the reconciliation path must handle (which it
  already does, but the semantics of "found in inbox with no state.json" are
  slightly different here since the task store is in the main instance).
- Does not map onto any existing pattern in the codebase. Option A generalises an
  existing pattern; Option C introduces a new one with new failure modes.

## Chosen: Option A — Direct Inbox Write

Option A wins because it requires the smallest code change, reuses a pattern already
established by `lookupLiveCoordinator` + `writeAskNotification`, and satisfies all
constraints without introducing new coordination layers.

The core change is:

1. Add `SessionID string` to `delegateArgs`.
2. In `handleDelegate`, when `SessionID != ""`, read
   `<mainInstanceRoot>/.niwa/sessions/<session-id>.json` to obtain the worktree path.
3. Derive the inbox path as `<worktreePath>/.niwa/roles/<to>/inbox/` and the role
   check as `<worktreePath>/.niwa/roles/<to>/`.
4. Record the worktree path (or session ID) in `state.json` so `handleCancelTask`
   and `handleUpdateTask` can reconstruct the correct inbox path.
5. No change when `SessionID == ""`.

The ownership objection (coordinator writes into a path it doesn't own) is a
theoretical concern, not a practical one: the path is validated from the session
state file at write time. If the session state file is absent or the worktree path
is stale, `handleDelegate` returns `SESSION_NOT_FOUND` or an appropriate error
before writing. The per-worktree daemon watches its own inbox; there is no risk of
the main daemon consuming the envelope.

## Rejected

- Option B (Main Daemon Router): adds large new routing logic to the main daemon,
  creates a forwarding hop that serialises delivery, and introduces a two-write
  failure window with no clean recovery path.
- Option C (Shared Pending Directory): introduces a new watcher pattern in the
  worktree daemon, a new shared directory layout, and an extra atomic-move hop —
  all without a precedent in the codebase and with no advantage over Option A.

## Consequences

**Positive:**

- No change to `writeMessageAtomic`, the daemon watch loop, or the worker claim
  path. The file landing in the worktree's inbox is treated identically to a
  message from any other sender.
- `niwa_delegate` without `session_id` is byte-for-byte identical to today.
- Session worktrees survive reboots: the catch-up inbox scan in the restarted
  worktree daemon finds any envelope that arrived while it was down.
- `handleCancelTask` and `handleUpdateTask` need only the same worktree-path
  resolution (read session state, reconstruct inbox path), which can be factored
  into a shared helper.

**Negative:**

- `state.json` must record the session ID (or worktree path) for session-routed
  tasks so `handleCancelTask` and `handleUpdateTask` know which inbox to target.
  This is a new field in `TaskState`.
- If `niwa_destroy_session` removes the worktree while a task is still in the
  worktree's inbox, the per-worktree daemon is gone and the task is stranded.
  The session lifecycle must ensure no active tasks remain before removing a
  worktree (already captured in PRD R4/R5).
- A coordinator bug that provides an incorrect `session_id` causes the envelope
  to land in the wrong inbox. Mitigation: validate the session ID against the
  sessions registry before writing; return `SESSION_NOT_FOUND` on mismatch.

**Assumptions:**

- Session state file at `<mainInstanceRoot>/.niwa/sessions/<session-id>.json`
  exists and contains a valid, absolute `worktree_path` by the time
  `niwa_delegate(session_id=X)` is called.
- The per-worktree daemon is running and watching its inbox when the envelope
  arrives, or will be restarted (via `EnsureDaemonRunning`) before or at the
  same call site, so the envelope is not left unclaimed indefinitely.
- The worktree path is stable for the lifetime of the session: it does not move
  after creation.
- Task store directories remain rooted in the main instance (`<mainInstanceRoot>/.niwa/tasks/<id>/`),
  not in the worktree. Only the inbox write and role validation use the worktree path.
