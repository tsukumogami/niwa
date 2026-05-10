# Lead: Does the attach lock need to be communicable across the mesh, and what does the coordinator see during a human attach?

## Findings

### 1. Today's coordinator → worker channel is filesystem-only, push-from-coordinator, pull-from-coordinator

The coordinator-to-worker channel is a **directory write** via `niwa_delegate(session_id=X)`. Implementation in `internal/mcp/handlers_task.go:111-165` (`handleDelegate`):

- Reads `<mainInstanceRoot>/.niwa/sessions/<sid>.json` to resolve `WorktreePath` (via `resolveCreationInboxDir`, lines 265-293).
- Writes the task envelope to `<worktreePath>/.niwa/roles/<role>/inbox/<task_id>.json` via atomic rename. No daemon RPC; no socket; no liveness check on the worktree daemon.
- Records `SessionID` in `state.json` so cancel/update can reconstruct the same path later.

The worktree daemon picks the envelope up via fsnotify on `<worktreePath>/.niwa/roles/<role>/inbox/` (`internal/cli/mesh_watch.go:198-212` registers the watch; `runEventLoop` lines 460-523 handles the CREATE event; `handleInboxEvent` lines 776-898 atomically renames `inbox/<id>.json → inbox/in-progress/<id>.json` and spawns a worker via `claude -p` or `claude --resume`).

There is **no push from the worker daemon back to the coordinator about session state**. The only push notifications the daemon emits are per-task lifecycle messages (`task.progress`, `task.completed`, `task.abandoned`, `task.cancelled`) written to the **coordinator's** role inbox via `sendTaskMessage` (`handlers_task.go:1044-1065`). These are tied to task IDs the coordinator already knows about — they are not session-state signals.

**Consequence:** the coordinator only learns about session state by polling. The two pull paths are:

- `niwa_list_sessions` (`handlers_session.go:26-50`) — reads every `<instance>/.niwa/sessions/<sid>.json` file and returns `SessionLifecycleState` array.
- `niwa_query_task` / `niwa_await_task` against tasks that already exist in this session (gives indirect signals: state, last_progress).

There is **no fsnotify or push channel telling a coordinator "session X just changed"**. The MCP server's `notifications/claude/channel` push (`watcher.go:202-205`) is per-message-type push for the *coordinator's own inbox*, not for sessions.

### 2. The coordinator naturally observes any state-file change via `niwa_list_sessions` polling — no notification path needed

`niwa_list_sessions` reads each session file fresh on every call (`handlers_session.go:26-50` → `ListSessionLifecycleStates` in `session_lifecycle.go:94-121`). Any new field written to the file via atomic temp+rename is visible on the next call.

This means **the simplest correct design is: attach is a filesystem flip in the session JSON; the coordinator sees it the next time it polls `niwa_list_sessions`**. No mesh hop required.

The session lifecycle file is the design's stated cross-process notification mechanism for session metadata. The `ClaudeConversationID` field already follows this pattern (`DESIGN-mesh-session-lifecycle.md:209-247`): the coordinator writes most fields on create; the per-worktree daemon writes `ClaudeConversationID` exactly once on the first task `running` transition. Both sides use atomic temp+rename. Adding an `attach_lock` field follows the same playbook.

### 3. `niwa_delegate` to a "busy" session today: there is no busy concept. The daemon spawns concurrently

`handleInboxEvent` (`mesh_watch.go:776-898`) processes each delegate envelope independently. There is **no per-session serialisation or queue**:

- The daemon's central event loop (lines 460-523) reads fsnotify CREATEs and dispatches each through `handleInboxEvent` immediately.
- `handleInboxEvent` does the atomic claim (queued → running via `mcp.UpdateState`) and calls `spawnWorker` (line 897). Each spawn is a fresh `claude -p` (or `claude --resume <sid>`) child process. There's nothing limiting parallel children.

The DESIGN-cross-session-communication doc (line 213-216) states a "single-worker-per-role invariant" — but that's enforced **at the inbox level**: at most one envelope per role can be queued at a time (the rename to in-progress consumes it). It does **not** mean the daemon refuses to spawn a second worker if a second envelope shows up.

If a coordinator delegates two tasks to the same session worker daemon back-to-back, both envelopes get claimed and **two workers run simultaneously in the same worktree**. The DESIGN-mesh-session-lifecycle doc's session-resume path (`mesh_watch.go:870-888`) is where that gets messy: the *second* worker would also spawn with `--resume <ClaudeConversationID>`, which means two `claude` processes against the same JSONL transcript. Nothing stops this today.

So the answer to "what does `niwa_delegate` do to a busy session" is: **it queues an envelope which the daemon claims and spawns regardless of whether another worker is in flight**. There is no first-class "busy" state.

For attach to work safely, **either**:

(a) The attach lock has to become a *daemon-side* gate: when a session has `attach_lock` set, the worktree daemon must skip claiming new envelopes (or pause after claim before spawn). This is filesystem-readable from the daemon's point of view because the daemon already reads `<sessionsDir>/<sid>.json` to look up `ClaudeConversationID` (`mesh_watch.go:870-888`), so it can read an `attach_lock` field on the same path.

(b) The attach lock is enforced at `handleDelegate` time — a coordinator's `niwa_delegate(session_id=X)` reads the session state, sees `attach_lock` set, and either fails fast (`SESSION_ATTACHED` error) or writes the envelope normally and lets it sit in the inbox until the daemon resumes claiming.

Option (a) matches the issue's stated UX: "tasks queue outside; pending state becomes visible when the human detaches." That implies the envelope reaches the inbox and waits there until detach.

### 4. Coordinator-side awareness via `niwa_list_sessions` is sufficient. Issue #109 is not a hard dependency for attach

Issue #109 is about **workers reaching the coordinator role for asks** — the worker calls `niwa_ask(to='coordinator')` and gets `UNKNOWN_ROLE` because the worker session's `.niwa/roles/coordinator/` doesn't exist. That's a worker → coordinator outbound problem. The fix in `handleAsk` is the `mainInstanceRoot` field plus `lookupLiveCoordinator` (`session_registry.go:57-92`).

The attach signal goes the other way: the coordinator wants to know **session state changed**. That's a coordinator-side read of `<instance>/.niwa/sessions/<sid>.json`, which is **already a path the coordinator can read directly** because it lives in the coordinator's main instance root. The coordinator doesn't need any worker → coordinator routing to learn about an attach lock — it just reads the file.

So `attach` can ship without #109. The only tie is documentation / skill content: if a coordinator wants to *react* to "session X is attached, my delegate is going to wait" at delegate time, the existing `niwa_delegate` error or `niwa_list_sessions` poll covers that.

### 5. Issue #111 (daemon health in `niwa_list_sessions`) overlaps the same return shape, but is structurally orthogonal

Issue #111 wants a `daemon` sub-object in `niwa_list_sessions` carrying `{pid, alive, started_at, last_claim_at, last_progress_at, watcher_count}`. That's about **daemon liveness** — is there a process still claiming envelopes for this session?

Attach state is about **session availability** — is this session reserved for human use right now?

The two axes are independent:

| daemon.alive | attach_lock | meaning |
|---|---|---|
| true | none | session ready for mesh delegation |
| true | held | session reserved for human; daemon is up but not claiming new envelopes |
| false | none | session is dead, queueing here will rot (the issue #112 dangling-task scenario) |
| false | held | shouldn't happen normally (held requires someone to release; if the daemon died, no one will) |

Both belong on the same `SessionLifecycleState` shape, but as different fields. The PRD should not collapse them into a single `availability` enum because doing so loses the "daemon is dead" diagnostic that #111 specifically asks for. Recommended shape:

```
{
  "session_id": "...",
  "status": "active",       // existing lifecycle field
  "daemon": { "pid": ..., "alive": true, ... },   // #111
  "attach": { "held_by_pid": ..., "held_since": "...", "claude_pid": ... }  // attach (this issue)
}
```

The two extensions can ship in either order. Sharing a parent JSON file means each writer must continue to use atomic temp+rename and read-modify-write to avoid clobbering — which is already the pattern (`session_lifecycle.go:52-66`).

### 6. The queue-during-attach question: the queue lives at the worktree daemon's inbox, not at the coordinator

If the issue's "tasks queue outside; pending state becomes visible when the human detaches" is the locked-in default, the natural implementation is:

- `niwa_delegate(session_id=X)` writes the envelope to `<worktreePath>/.niwa/roles/<role>/inbox/<task_id>.json` as it does today.
- The worktree daemon's `handleInboxEvent` reads the session state file (it already reads it to fetch `ClaudeConversationID` at lines 878-888) and **skips claim when `attach_lock` is held**.
- On detach, the lock release triggers a re-scan of the inbox (the daemon already has a catch-up scan pattern for orphans and queued envelopes — `mesh_watch.go:275-278` `scanExistingInboxes`).

This means the queue is **on the worktree's filesystem**, exactly where queued envelopes already live today. No new queue. No coordinator-side queuing. The coordinator's `niwa_query_task` continues to return `state: queued`; the coordinator can observe via `niwa_list_sessions` that the session is attached and its delegate is waiting.

The implementation gotcha: the daemon today logs a `skip=dangling` and **moves the envelope to `inbox/dangling/`** when the task dir doesn't exist (lines 783-803). The detach-resume path must distinguish "queued because attached, waiting for release" from dangling. Adding an `attach_lock`-aware check that returns early *without* moving to dangling is required, otherwise envelopes delegated during an attach will be dangling-classified once the (assumed-dead) state is encountered. The dangling-vs-queued classification interaction (issue #112) is real: if the lock-release path uses the same scan code path that processes dangling files, dangling envelopes from earlier mishaps will become visible to the human inside the attached session — exactly the scenario #112 flags.

### 7. The coordinator behavior contract — what the issue should commit to

Given the findings:

- The lock is **filesystem-visible** via `niwa_list_sessions`. No new push channel required.
- The coordinator does **not get an explicit notification** today and does not need one — its existing flow is "delegate, then `niwa_await_task`," which handles "task is queued for a long time" cleanly via the re-await loop pattern (`buildSkillContent` lines 800-810).
- The coordinator **may** need skill-content guidance: if a `niwa_await_task` returns timeout repeatedly with `state=queued`, the coordinator should call `niwa_list_sessions` to check whether the session is attached.
- `niwa_delegate` to an attached session should **not fail-fast** — the issue's "tasks queue outside" default is consistent with the existing queue-then-claim model. Failing fast would force the coordinator to retry-loop, which is worse than letting the envelope sit.

## Implications

**The PRD should commit to:**

1. **Attach state is communicated via the session lifecycle JSON file**, joining `claude_conversation_id` as a field written by a non-coordinator writer. Atomic temp+rename. The coordinator reads via `niwa_list_sessions`. No new push notification path.
2. **The worktree daemon is the enforcement point for queue-during-attach**. It must read the attach state from `<sessionsDir>/<sid>.json` before claiming an inbox envelope and skip-without-dangling-move when the lock is held. Detach must trigger a re-scan to drain the queued backlog.
3. **`niwa_list_sessions` is the unified surface for both #111 (daemon health) and attach state.** Add separate sub-objects (`daemon`, `attach`) rather than collapsing into one enum. Either feature can ship first; the second extends the same struct.
4. **Coordinator skill content (`buildSkillContent`) should be updated** so coordinators know to consult `niwa_list_sessions` when their delegated task sits in `queued` for a long time. This is a documentation change, not a tool change.

**Dependency relationships:**

- **Attach is NOT blocked on #109.** Issue #109 is worker → coordinator outbound (asks). Attach is coordinator-side reads of session state files in the coordinator's own instance root. Different direction; different file path; different code path.
- **Attach is NOT blocked on #111.** They both extend `niwa_list_sessions` output, but additively. Whichever ships first defines the precedent for sub-object placement.
- **Attach has a soft conflict with #112 (dangling).** The detach-and-resume path must not pass attach-queued envelopes through the dangling-classification code in `mesh_watch.go:783-803`. The PRD should explicitly state: "envelopes delegated to a session while attached are not dangling; they are deferred until detach."
- **Attach has a soft conflict with the multiple-workers-per-session reality.** The DESIGN-mesh-session-lifecycle didn't enforce single-worker-per-session. If the issue's `attach_lock` is the first piece of state that requires single-worker semantics, the PRD should flag this. (`--force` SIGTERMing the running worker before attach handles the immediate case, but doesn't address the "two delegates queued during attach, both spawn on detach" scenario.)

## Surprises

1. **There is no first-class "busy" state on a session today**. The DESIGN-cross-session-communication doc says "single-worker-per-role invariant" — but that's enforced by the inbox queue, not by anything that prevents two `claude` processes running concurrently in the same worktree against the same Claude conversation JSONL. Two back-to-back delegates today produce undefined behavior with `--resume`. Attach is the first feature that requires this to be cleaned up — or at least pinned down.

2. **The dangling classification (`mesh_watch.go:792-803`) is sticky and would catch attach-queued envelopes** unless the lock check runs *before* the dangling check. The order of the daemon's "is this delegate actionable" checks matters more than I expected.

3. **The DESIGN-mesh-session-lifecycle doc explicitly anticipated cross-process writers to the session JSON** (per-worktree daemon writes `ClaudeConversationID`, coordinator writes everything else), and committed to atomic temp+rename being sufficient because the writes are "temporally non-overlapping" (line 211). An attach lock writer (the niwa CLI `niwa session attach` process) becomes a third writer to the same file, but it's also temporally non-overlapping with the others (writes lock on attach, clears lock on detach). The pattern holds.

4. **Coordinator-side polling is the actual mesh signal**. There is *no* push-to-coordinator notification path for session state today — and that's deliberate per the design's filesystem-only-coordination principle (DESIGN-cross-session-communication.md line 353). Adding one for attach would be a new architectural primitive; not adding one keeps attach within the existing model.

## Open Questions

1. **Single-worker-per-session enforcement timing.** Does the PRD scope "no concurrent workers per session" as an attach prerequisite, or does it ship attach with the existing "two workers might both spawn" weakness and document it? If the former, that's a separate behavior change touching `handleInboxEvent` that has implications beyond attach.

2. **Daemon-side lock-aware skip behavior.** The daemon reads the session state on every claim (`mesh_watch.go:870-888`) but only to populate `resumeSessionID`. Adding an `attach_lock` check there is small, but the design needs to decide: skip-and-leave-in-inbox (envelope stays as a "queued" file the daemon will see again on the next CREATE event, which only fires on first arrival), or skip-with-deferred-rescan-after-release (daemon needs a release-triggered scan). The catch-up scan pattern (`scanExistingInboxes`) is an obvious reuse target for the second.

3. **Cross-attach concurrency.** What happens if two humans (or one human in two terminals) try `niwa session attach <sid>` simultaneously? The lock file mechanic needs to handle this. The existing `daemon.pid.lock` flock pattern (`mesh_watch.go:246-261`) is a precedent.

4. **Does `niwa_create_session` need to refuse attach-targeted operations?** If a coordinator calls `niwa_destroy_session` on a session that's currently attached, what happens? `handleDestroySession` (`handlers_session.go:237-309`) would force-kill the human's `claude` process via `killSessionWorkers`. The PRD should commit to whether destroy-while-attached is allowed, blocked, or warns.

5. **Coordinator skill-content awareness depth.** Should the niwa-mesh skill teach coordinators to *check* attach state before delegating, or just to *recover gracefully* when they discover a delegate has been sitting queued because of an attach? The cheaper path is recover-gracefully; the more polished one is check-first. This is a UX decision, not a technical one.

## Summary

The attach lock is filesystem-visible via the existing `<instance>/.niwa/sessions/<sid>.json` state file, so the coordinator naturally sees it through the next `niwa_list_sessions` poll without any new push channel — making attach independent of issue #109 (which is about worker→coordinator outbound asks, a different direction entirely) and only loosely related to issue #111 (which extends the same JSON shape additively). The biggest implementation gotcha is that the worktree daemon today claims envelopes concurrently and would dangling-classify lock-deferred envelopes, so the attach feature must add a daemon-side lock-aware skip *before* the existing dangling check and a release-triggered re-scan to drain the queue, not at the coordinator side. The biggest open question is whether the PRD must also pin down single-worker-per-session semantics — today's daemon would happily spawn two `--resume` workers against the same Claude transcript, which attach is the first feature to make unworkable.
