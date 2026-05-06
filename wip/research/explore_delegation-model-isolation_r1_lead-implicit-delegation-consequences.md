# Lead: Practical consequences of implicit session-bound delegation

## Findings

### What `niwa_delegate` without `session_id` does today, step by step

Source: `internal/mcp/handlers_task.go` (`handleDelegate`, `createTaskEnvelope`, `resolveCreationInboxDir`)

1. `handleDelegate` validates `to`, `body`, and `mode`, then calls `createTaskEnvelope` with `sessionID=""`.
2. `createTaskEnvelope` calls `resolveCreationInboxDir("", role)`. With an empty session ID, this immediately returns `<instanceRoot>/.niwa/roles/<role>/inbox` — the main instance inbox. No session lookup, no worktree check.
3. The task envelope (`envelope.json`) and initial `state.json` (with `SessionID: ""`) are written into `<instanceRoot>/.niwa/tasks/<task-id>/`.
4. An atomic rename drops a `task.delegate` message into the main instance inbox for the target role.
5. The main-instance daemon's fsnotify watcher fires on the inbox CREATE event. The daemon claims the envelope by renaming `inbox/<id>.json` → `inbox/in-progress/<id>.json` and transitions state to `running`.
6. In `handleInboxEvent` (around line 868 in `mesh_watch.go`), the daemon checks `claimedSt.SessionID`. Because it is empty, no `ClaudeConversationID` lookup occurs. `evt.resumeSessionID` stays empty.
7. `spawnWorker` builds an `exec.Command` using `-p <bootstrap-prompt>` (no `--resume`). The worker's `NIWA_INSTANCE_ROOT` points to the **main clone root** and `cmd.Dir` is set to the role's repo directory inside the main clone.
8. The worker runs in the main clone. Any files it creates, edits, or git-commits land directly on the main clone's current branch.

There is no worktree created, no session state file written, and no daemon separation. The worker shares the main clone's git state with everything else that runs there.

### Who would set the purpose string for implicit sessions?

Source: `internal/mcp/handlers_session.go` (`handleCreateSession`, `createSessionArgs`)

`niwa_create_session` requires `purpose` as a mandatory field (enforced in `handleCreateSession`: `if args.Purpose == ""` → `BAD_PAYLOAD`). The purpose is:
- stored in `<sessionID>.json` as `SessionLifecycleState.Purpose`
- visible in `niwa session list` output
- the sole human-readable label for a session at the MCP level (PRD R24)

If `niwa_delegate` implicitly created sessions, there are three options for the purpose:
1. **Derive it from the task body** — the task body is untrusted, delegator-supplied content. Using it verbatim for a session label would either require sanitization or expose injection risk.
2. **Use a generic placeholder** like `"auto-session for task <task-id>"` — this makes `niwa session list` nearly unreadable when there are many concurrent delegations, and gives the coordinator no signal about what each session is for.
3. **Require the delegator to pass a purpose** — this is equivalent to making session creation explicit, just with slightly different syntax. It adds mandatory coordinator overhead for every delegation.

None of these options is clean. The explicit-session model keeps purpose meaningful because the coordinator sets it intentionally before a sequence of related tasks. Implicit sessions generated per-task would flood the session list with low-value entries.

### Who would destroy implicitly created sessions?

Source: `internal/mcp/handlers_session.go` (`handleDestroySession`), `internal/cli/mesh_watch.go` (`captureConversationID`), session lifecycle state model.

Today's explicit session lifecycle:
- Created by `niwa_create_session` (MCP) or `niwa session create` (CLI), both requiring `repo` and `purpose`.
- Status persists in `<sessionID>.json` independently of any task's lifecycle.
- Destroyed only by explicit `niwa_destroy_session` or `niwa session destroy` — there is no auto-destroy on task completion.
- `handleDestroySession` kills running workers, writes `status="ended"`, stops the per-worktree daemon, removes the worktree directory, and attempts to delete the session branch.

If every `niwa_delegate` created an implicit session, three destruction paths exist:

1. **Auto-destroy on task completion**: the daemon would need to call `handleDestroySession` when the task reaches a terminal state. This is not implemented. Adding it requires the daemon to know whether a session was implicitly created vs. coordinator-managed — a new bit of state not currently tracked. More importantly, auto-destroy would remove worktrees that contain commits not yet pushed, silently destroying work.

2. **Leave sessions alive, require explicit cleanup**: implicit sessions would accumulate indefinitely. Every delegation produces a session entry. `niwa session list` becomes a graveyard of single-task sessions. This is worse than the current model for workspace hygiene.

3. **Auto-destroy only if no git changes**: the daemon would need to check `git log` on the session branch before cleanup. This is possible but adds complexity. A worker that makes changes and calls `niwa_finish_task` without pushing would still block auto-destroy (matching the existing unpushed-work guard in `handleDestroySession`), reverting to option 2.

In all cases, the coordinator loses clear visibility into which sessions are "owned" cleanup responsibilities vs. transient artifacts.

### Cost of creating a worktree per task

Source: `internal/mcp/handlers_session.go` (`handleCreateSession`), `internal/cli/mesh_watch.go` (`runMeshWatch`), PRD Known Limitations section.

`handleCreateSession` performs these steps for each session:
1. `git worktree add <path> -b session/<id>` — a `git worktree add` call. On large repos this is a fast metadata operation (seconds, not minutes), but it is not instantaneous. On repos with thousands of files it can take 1-3 seconds depending on filesystem.
2. `scaffoldWorktreeNiwa` — creates ~10 directories and 2 placeholder files under `.niwa/` in the new worktree. Filesystem overhead only, negligible.
3. `WriteSessionLifecycleState` — one atomic write to `sessions/<id>.json`.
4. `s.daemonStarter(worktreePath, extraEnv)` — starts a **new daemon process** for the worktree. This is a full `niwa mesh watch --instance-root=<worktreePath>` process. It writes a PID file, opens a log file, registers fsnotify watchers, runs reconciliation and catch-up scans, and blocks in its event loop.

Per-task costs if delegation were session-bound:
- **Time**: `git worktree add` + daemon startup (~2-5 seconds cold, depending on repo size and kernel fsnotify initialization). For short tasks (read-only lookups, quick edits), this adds more overhead than the task itself.
- **Disk**: each worktree creates a full working-tree checkout (hardlinks on most filesystems, so shared objects; still means one inode per file). For large repos this is meaningful. The PRD's Known Limitations section explicitly calls this out: "disk space usage grows proportionally" with session count.
- **Processes**: each session runs a persistent daemon process. One daemon per task for all concurrent delegations means O(concurrency) extra daemons, each with their own fsnotify watchers. At high concurrency this is significant resource pressure.
- **Cleanup**: every task completion requires a `git worktree remove` + `git branch -d` + daemon stop, plus the unpushed-work guard decision. For read-only tasks that make zero commits this is pure overhead with no benefit.

### What happens to read-only or no-git-change delegations?

A typical coordinator might delegate tasks like "summarize the current state of these files" or "check whether this test passes and return the output." These produce no git changes. Under implicit session creation:

1. A worktree is provisioned (git checkout + daemon spawn).
2. The worker runs in the worktree, reads files, returns a result, calls `niwa_finish_task`.
3. The daemon sees the task reach terminal state. The session branch has no commits beyond the branch point.
4. Auto-destroy could safely proceed (no unpushed commits). But the daemon has no way to know at session-creation time whether the task will be read-only.
5. The branch must be cleaned up regardless. `git branch -d session/<id>` succeeds because the branch is already merged (no new commits). The worktree directory is removed.

Net result: a read-only delegation accrues 2-5 seconds of worktree provisioning overhead, a full daemon lifecycle, and a branch creation/deletion cycle — for zero isolation benefit. The main clone was never at risk from a read-only worker.

The isolation benefit of a worktree (keeping the main clone clean) only materializes when the worker makes commits. For tasks that produce no git changes, worktree isolation is pure overhead with no upside.

### Current lifecycle of a session-tagged delegation — how sessions persist after task completion

Source: `internal/mcp/handlers_task.go` (`createTaskEnvelope`), `internal/cli/mesh_watch.go` (`captureConversationID`, `handleSupervisorExit`), `internal/mcp/session_lifecycle.go`.

When `niwa_delegate(session_id="a3f7c2d1", ...)` is called:

1. `createTaskEnvelope` reads the session state file, verifies `status == "active"`, extracts `session.WorktreePath`, and writes the inbox message to `<worktreePath>/.niwa/roles/<role>/inbox/`.
2. `state.json` is written with `SessionID: "a3f7c2d1"` — the session ID is stored on the task.
3. The **per-worktree daemon** (not the main-instance daemon) fires on the inbox event.
4. In `handleInboxEvent`, the daemon reads `claimedSt.SessionID` → `"a3f7c2d1"`, looks up `sessions/a3f7c2d1.json`, and reads `ClaudeConversationID`. If set, `evt.resumeSessionID` is populated so the worker spawns with `--resume`.
5. After the worker exits normally (calls `niwa_finish_task`), `handleSupervisorExit` calls `captureConversationID(st.Worker.ClaudeSessionID, ...)`.
6. `captureConversationID` checks `NIWA_MAIN_INSTANCE_ROOT` and `NIWA_SESSION_ID` env vars (injected at daemon spawn), reads `sessions/<sessionID>.json`, and writes `ClaudeConversationID` **once** (the first task to complete sets it; subsequent tasks in the same session find it already set and skip).
7. The session state file's `Status` remains `"active"`. The session is **never automatically ended** by task completion. It persists indefinitely.
8. The next `niwa_delegate` into the same session picks up the recorded `ClaudeConversationID` and spawns with `--resume`, continuing the conversation.

This lifecycle is explicit by design: the coordinator controls when to end a session via `niwa_destroy_session`. The task and the session have independent lifecycles. A session can outlive many tasks; a task's completion does not imply the session is done.

This design choice is incompatible with implicit sessions that auto-destroy on task completion, because the primary value of sessions (conversation continuity across tasks) requires sessions to persist between tasks. Implicit per-task sessions would destroy the conversation state immediately, defeating the point.

---

## Implications

### Implicit session creation is incompatible with the session model's primary value

The session model exists to preserve Claude conversation history across multiple sequential tasks delegated to the same repo. An implicit session that auto-destroys after one task cannot provide that continuity. If implicit sessions persisted instead, they would accumulate faster than coordinators could manage them, and the "purpose" field that makes sessions legible would be meaningless.

### The overhead is non-trivial for short-lived tasks

`git worktree add` + daemon spawn + daemon teardown adds 4-10 seconds of overhead per task and a new process lifetime. For tasks that take 30+ seconds this is acceptable. For tasks that take 5 seconds (a file read, a lint check, a quick query), it doubles or triples total execution time. Any architecture that makes worktree isolation mandatory-by-default must accept this as a floor cost.

### The unpushed-work guard creates a destroy obligation the coordinator cannot auto-satisfy

`handleDestroySession` uses `git branch -d` (not `-D`) by default to prevent silent data loss. If a worker makes commits and the coordinator wants auto-destroy, the coordinator must push the branch before destroying — which requires it to know the branch was used, know the remote, and decide what to do with partial work. This is coordinator-level reasoning that cannot be reliably automated.

### Read-only delegations should not pay worktree cost

There is a real class of delegations that are purely read-only: query tasks, analysis tasks, review tasks. These do not benefit from git isolation. A policy of "always create a worktree" would penalize this class without benefit. If implicit sessions become default, the design needs a way to opt out for read-only delegations — which partially recreates the current opt-in model but inverted.

### The current backward-compat rationale is weak, but the replacement design is unclear

The PRD's "No implicit sessions on untagged niwa_delegate" decision cites backward compatibility. The scope doc notes this is weak since niwa has no users. However, the alternative — implicit per-task sessions — introduces lifecycle management complexity (who destroys, who names, what happens to read-only tasks) that the current code has no answers for. The opt-in model isn't wrong because of backward compat; it's right because explicit sessions serve a clear purpose (multi-task continuity) that implicit ones cannot replicate.

---

## Surprises

### Sessions do not auto-destroy when all their tasks complete

This is not documented prominently but is clear from the code: `handleDestroySession` is never called from any task lifecycle path. The session's `status` field is only ever written to `"ended"` or `"abandoned"` by explicit destroy calls. This means a coordinator that creates sessions but forgets to destroy them leaves persistent worktrees and daemons running indefinitely. This is intentional (the PRD's no-auto-pruning decision), but it means the session model already imposes a manual cleanup obligation that any implicit-session design would need to address.

### The daemon per worktree is a full process, not a lightweight supervisor

Each session worktree runs a complete `niwa mesh watch` daemon with fsnotify, reconciliation, orphan polling, and signal handling. This is not a small forked goroutine — it's a full daemon that persists for the entire session lifetime. At scale (many concurrent sessions), this is a significant process-count increase.

### `captureConversationID` is a one-time write that happens at worker exit, not at session creation

The Claude conversation ID is not known when the session is created. It is only captured after the first worker in the session exits, by reading `CLAUDE_SESSION_ID` from the worker's environment (written into `state.json` at startup via `registerSessionID`). This means a session's continuity mechanism is bootstrapped lazily. For an implicit per-task session that destroys immediately after the task, the conversation ID would be captured and then the session file would be deleted — the capture would be pointless.

### The task store is always in the main instance, even for session workers

`taskStoreRoot()` in `server.go` returns `s.mainInstanceRoot` when set. Session workers write task state to the **main instance** `.niwa/tasks/` directory, not the session worktree. Only the inbox and working files live in the worktree. This means task state (state.json, envelope.json, stderr.log) is centralized. This is an important architectural detail: worktrees isolate git state and working files, not task metadata.

---

## Open Questions

### Who is responsible for naming an auto-session?

The purpose field is mandatory and must be human-meaningful. For implicit sessions, there is no clean source for this value. Could it be inferred from the task body? Could it be optional for implicit sessions? This is an API design decision, not just an implementation one.

### Should read-only tasks be distinguishable at delegation time?

A `read_only: true` flag on `niwa_delegate` could opt out of worktree creation for tasks with no git side effects. But coordinators cannot always predict whether a worker will make commits. Would the delegate API need a way to declare expected git effects?

### What is the right escalation path when an implicit session's worker makes unexpected commits?

If implicit sessions auto-destroy, and a worker makes commits the coordinator did not anticipate, those commits exist on a branch that is about to be deleted. Does the coordinator get a warning? Does auto-destroy block? Does the commit get orphaned?

### Is there a middle ground: sessions optional but strongly encouraged?

Rather than making sessions default, the UX could surface a warning when `niwa_delegate` is called without `session_id`. This would guide coordinators toward sessions without mandating the overhead. The cost is tooling complexity to distinguish "warned and chose not to" from "didn't know."

### What happens to the main clone over time without sessions?

The PRD's clean-main goal is never enforced; it is only achieved if coordinators use sessions. An audit of real coordinator usage patterns (once available) would show whether untagged delegations in fact leave main clones dirty, and how often that causes `niwa apply` failures.

---

## Summary

Making all `niwa_delegate` calls session-bound by default would require solving three unsolved problems simultaneously: who names auto-created sessions, who destroys the resulting worktrees (with unpushed-work safety guards), and how to avoid penalizing read-only delegations that gain no isolation benefit from a full git worktree and per-worktree daemon. The primary value of the session model — preserving Claude conversation history across sequential tasks — depends on sessions that persist between tasks, which is incompatible with auto-destroy after each task completes. The biggest open question is whether a lighter-weight alternative exists (such as a warning on untagged delegations, or an opt-out `read_only` flag) that preserves isolation for multi-step work without taxing the common single-task delegation pattern.
