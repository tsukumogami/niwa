# Lead: How does the niwa daemon route inbox messages to live coordinator sessions today, and where is the spawn-an-ephemeral-worker fallback wired?

## Findings

### 1. Two distinct registries exist; the on-disk schema for coordinator-routing is `sessions.json`, not `.niwa/roles/<role>/`

There are two unrelated state files under `<instance>/.niwa/sessions/` despite the shared directory name:

- **`sessions.json`** — the *coordinator process registry*. Schema:
  `internal/mcp/types.go:96-115` (`SessionEntry`, `SessionRegistry`). Fields: `id, role, repo, pid, start_time, inbox_dir, registered_at, claude_session_id`. This is the only structure that `lookupLiveCoordinator` reads to decide whether routing-to-live-session is possible.
- **`<sessionID>.json`** — per-session lifecycle state for niwa_create_session worktrees, schema at `internal/mcp/session_lifecycle.go:30-47` (`SessionLifecycleState`). Distinct type, distinct code path. Comment at `session_lifecycle.go:31-32` calls this out: *"This type is distinct from SessionEntry (the coordinator process registry). The two types share no fields and are written by separate code paths."*

`.niwa/roles/<role>/` itself contains only the *inbox tree* (`inbox/` and the daemon-managed sub-dirs `in-progress/`, `cancelled/`, `expired/`, `read/`). There is **no** per-role JSON file with PID/identity — that information lives only in `sessions.json`. So when issue #109 says the worker has "no `coordinator` entry under `.niwa/roles/`", that's accurate but slightly misleading: there's no role-keyed PID file scheme at all in the current code.

### 2. The role registration code path

`internal/cli/session_register.go` (the `niwa session register` CLI):

- Derives a role via four-tier priority (flag → `NIWA_SESSION_ROLE` → `--repo` → pwd-relative-to-instance-root, falling back to `"coordinator"` when cwd equals instance root) — `session_register.go:123-155`.
- Creates a per-session inbox at `<instanceRoot>/.niwa/sessions/<sessionID>/inbox/` (this directory appears to be vestigial — see (4) below).
- Builds a `SessionEntry{ID, Role, Repo, PID, StartTime, InboxDir, RegisteredAt, ClaudeSessionID}` and calls `mcp.WriteSessionEntry` to atomically merge it into `sessions.json` — `session_register.go:65-87`.
- `WriteSessionEntry` (`internal/mcp/session_registry.go:20-50`) prunes stale entries (dead PIDs) for the same role and rejects writes when a *live* PID for the same role already exists (`ErrAlreadyRegistered`).

There's also an automatic registration path: `maybeRegisterCoordinator` (`session_registry.go:103-141`) is called from `handleAwaitTask` and (per the file comment) `handleCheckMessages`. It writes a `SessionEntry` for `s.role == "coordinator"` on first use. The comment justifies this as making coordinator visibility "automatic" — a coordinator that has never called either tool has no inbox-watcher running anyway.

**Crucial:** `maybeRegisterCoordinator` only fires when `s.role == "coordinator"`. A worker MCP server (say `s.role == "vision"`) inside a session worktree never registers anything, so nothing in `<worktreePath>/.niwa/sessions/sessions.json` ever points at the main coordinator.

### 3. Full path from worker `niwa_ask(to='coordinator')` to either delivery or rejection

Trace, from `internal/mcp/server.go:792` (`handleAsk`):

1. **Validate args** — to/body/timeout (`server.go:793-801`).
2. **`isKnownRole(args.To)` — THIS IS THE BLOCKER.** `server.go:802-805`. The check at `server.go:768-778` does `os.Stat(<s.instanceRoot>/.niwa/roles/<role>)` and fails if the directory does not exist. For a worker, `s.instanceRoot` is the *worktree* path (set via `NIWA_INSTANCE_ROOT` baked into the per-spawn worker MCP config — `internal/workspace/channels.go:90`). The worktree only contains `.niwa/roles/<repo>/` because of `scaffoldWorktreeNiwa` (`internal/mcp/handlers_session.go:80-108`, lines 86-90). There is no `.niwa/roles/coordinator/` in the worktree, so `isKnownRole("coordinator")` returns `false` and `handleAsk` returns `errResultCode("UNKNOWN_ROLE", ...)` immediately. **This matches the failure mode in issue #109 verbatim.**
3. *(Hypothetically, if step 2 passed)* Wrap the body with `"kind":"ask"` and choose `askRoot`:
   ```go
   askRoot := s.instanceRoot
   if args.To == "coordinator" && s.mainInstanceRoot != "" {
       askRoot = s.mainInstanceRoot
   }
   coordinatorInbox, liveCoord := lookupLiveCoordinator(askRoot)
   ```
   (`server.go:816-820`). For session workers `mainInstanceRoot != ""` (set from `NIWA_MAIN_INSTANCE_ROOT` — see `server.go:97-98` and the daemon spawn extraEnv at `handlers_session.go:212-215`), so we'd correctly look up `sessions.json` in the *main* instance.
4. `lookupLiveCoordinator` (`session_registry.go:57-92`) reads `<askRoot>/.niwa/sessions/sessions.json`, scans entries for `Role=="coordinator"`, and returns the *role-inbox* path `<askRoot>/.niwa/roles/coordinator/inbox` (NOT the stored `InboxDir` — see comment at `session_registry.go:53-56`) plus `found=true` when the PID is alive. Stale entries are pruned in-place.
5. Two branches (`server.go:823-843`):
   - **Live coordinator path:** `createAskTaskStore` (`handlers_task.go:316-366`) writes only `envelope.json` + `state.json` under `.niwa/tasks/<id>/` — *no `task.delegate` envelope is written to any inbox*. Then `writeAskNotification` (`server.go:894-925`) writes a `task.ask` Message (with `_niwa_note` prompt-injection guard) directly into the coordinator's role inbox. Then registers an `awaitWaiter` keyed by the ask task ID and selects on `terminal-event-channel | timeout`.
   - **No-live path:** returns `{"status":"no_live_session", "role":..., "message":...}` immediately. **No task directory is created.** The comment at `server.go:835-836` makes this explicit: *"No live coordinator: return typed no_live_session response immediately, before creating any task directory."*

The coordinator picks up `task.ask` files via the watcher: `notifyNewFile` in `internal/mcp/watcher.go:121-140` dispatches to `questionWaiters[m.To.Role]` (registered by `handleAwaitTask` via `registerQuestionWaiter` — `handlers_task.go:475-488`) and in the `niwa_check_messages` polling path. The coordinator answers via `niwa_finish_task(task_id=ask_task_id)`, which writes a `task.completed` to the ask-originator's inbox; that's caught by the awaitWaiter dispatch in `watcher.go:151-177`.

### 4. The "intentionally removed" comment is GONE — and the spawn-an-ephemeral-worker fallback is also gone

I searched the current tree for `"intentionally removed"`, `"spawn an ephemeral"`, `"deprecated"`, etc. (no hits in `internal/mcp/`). Git log: commit `71b42ec feat(mcp): route niwa_ask to live coordinator session (#93)` is the PR that closed half of issue #92. The commit message states:

> When a worker calls niwa_ask and a coordinator session is registered in sessions.json with a live PID, the question is routed directly to the coordinator's role inbox instead of spawning an ephemeral coordinator worker. This prevents the deadlock where a coordinator blocked on niwa_await_task would never receive the question because the daemon would spawn a separate coordinator process that cannot access the blocked session's state.

So as of #93:
- The "live coordinator" branch is wired (writing only envelope+state, never an inbox `task.delegate`, so `daemonOwnsInboxFile` never matches it — `internal/cli/mesh_watch.go:746-758`: *"The daemon claims a file iff its body has `type == "task.delegate"`."*).
- The "no live coordinator" branch is **also no longer the spawn path** — it returns `no_live_session` immediately without writing a `task.delegate` envelope (`server.go:834-843`), so the daemon never sees anything to claim.

**This means the "spawn-an-ephemeral-worker fallback" hypothesized by the lead does not exist in the current code for `niwa_ask`.** The daemon's only spawn trigger is a file with `type == "task.delegate"`, which is produced by `niwa_delegate` — never by `niwa_ask` post-#93. Worker `niwa_ask(to='coordinator')` either:
- Fails fast with `UNKNOWN_ROLE` (issue #109's case, due to the `isKnownRole` check on the worktree's local roles dir), OR
- Writes a `task.ask` to the main coordinator's inbox and waits (the post-#93 happy path, only reachable if the worktree had a `coordinator` role dir, which it doesn't).

The "fabricated approval" symptom described in issue #92's narrative reflects the pre-#93 world, where `handleAsk` flowed through the same `task.delegate`-shaped path as `niwa_delegate` and the daemon spawned a fresh `claude -p` process to "answer" the ask. Today only `niwa_delegate` produces a `task.delegate` envelope (`handlers_task.go:245-257`), so there's no remaining ambient ephemeral-worker reply path.

### 5. How role/PID information is propagated from coordinator to worker at session creation

Reading `handleCreateSession` (`internal/mcp/handlers_session.go:146-229`) end-to-end:

- Validates the requested repo's role exists under the *main* instance's roles dir (`handlers_session.go:158-161`).
- Generates a session ID, creates a git worktree (`handlers_session.go:188-191`).
- `scaffoldWorktreeNiwa(worktreePath, repo)` (`handlers_session.go:80-108`) creates **only**:
  - `<worktree>/.niwa/{tasks,sessions}/`
  - `<worktree>/.niwa/roles/<repo>/inbox/{,in-progress,cancelled,expired,read}/`
  - Empty `daemon.pid` and `daemon.log` placeholders.
  No `.niwa/roles/coordinator/` directory. No `sessions.json` priming. No coordinator PID anywhere.
- `WriteSessionLifecycleState` writes the lifecycle state file (per-session, not per-role).
- Spawns the worktree daemon with extraEnv `NIWA_MAIN_INSTANCE_ROOT=<main>`, `NIWA_SESSION_ID=<sid>` (`handlers_session.go:212-215`). These propagate into worker MCP config through `WorkerMCPConfig` (`internal/workspace/channels.go:94-99`).

So the answer to "how is coordinator role/PID propagated?" is **it isn't, except indirectly via `NIWA_MAIN_INSTANCE_ROOT`**. The worker's MCP server can compute the path to the main coordinator's `sessions.json` (and `handleAsk` does, at `server.go:817-820`), but:

- The `isKnownRole` precondition fires against the *local* worktree's `.niwa/roles/`, which has no `coordinator/` dir.
- Even if `isKnownRole` were redirected, `lookupLiveCoordinator` against the main `sessions.json` only succeeds if the coordinator has called `niwa_check_messages` or `niwa_await_task` *(triggering `maybeRegisterCoordinator`)* OR has explicitly run `niwa session register`. A coordinator that just delegated and is now waiting on `niwa_await_task` *will* be registered (because `handleAwaitTask` calls `s.maybeRegisterCoordinator()` first — `handlers_task.go:398`). A coordinator that's spinning in user-driven flow without ever calling those two tools will not be.

The daemon's startup logs `watched_roles count=N` from `registerInboxWatches` (`internal/cli/mesh_watch.go:208-212` / `mesh_watch.go:2236-2266`), which only enumerates `<niwaDir>/roles/`. For a worktree that's just the one repo role, hence the `count=1` observed in issue #109's reproduction.

### 6. Surprising property: even with #93 wired, the worker can't reach the live coordinator path

The `isKnownRole` check at `server.go:802` runs against `s.instanceRoot` (the worktree), not against the same root used for live-session lookup (`askRoot = s.mainInstanceRoot` for coordinator targets). PR #93 hoisted the *lookup* to mainInstanceRoot but did **not** hoist the *role-existence precondition*, which still uses the worktree. So the live-coordinator branch in `handleAsk` is unreachable from a session worker today, regardless of whether a coordinator is registered. Tests in `session_registry_ask_test.go` (e.g. `TestHandleAsk_LiveCoordinator_WritesTaskAsk` at line 51) construct a `Server` whose `instanceRoot` IS the main root *and* contains a `coordinator/` role dir — they don't exercise the worktree-vs-main split, so the regression is invisible to the unit suite.

## Implications

1. **Issue #92 is half-solved.** The routing-to-live-session code exists (`handleAsk` live-coordinator branch + `task.ask` watcher dispatch + `questionWaiters`). The ephemeral-worker spawn path is gone (no more `task.delegate` written by `niwa_ask`). What remains is unreachable from a session worker because of the `isKnownRole` worktree-lookup ordering issue.

2. **Issue #109 has a clean fix.** Three viable mechanisms surface from the code:
   - **A.** Register a virtual `coordinator` role in the worktree at session creation (mkdir `<worktree>/.niwa/roles/coordinator/inbox/...`) — would make `isKnownRole` pass. But would also imply the daemon needs to NOT route delegate envelopes for `coordinator` to a worker spawn in the worktree (which `daemonOwnsInboxFile` already guards: only `task.delegate` files trigger spawn, and `task.ask` won't ever be written here because handleAsk uses `mainInstanceRoot` for coordinator targets).
   - **B.** Make `isKnownRole` consult `s.mainInstanceRoot` when `role == "coordinator"`, mirroring the `askRoot` fallback at `server.go:817-819`. Smallest change, most surgical.
   - **C.** Issue #109's suggested fix: write a `coordinator.json` (PID + start_time + main-root path) into the worktree at create-session time. Heavier; introduces a third role-state schema.

3. **The lead's framing of "decides between routing-to-live-session vs spawning-an-ephemeral-worker" no longer matches the implementation.** Today's branch is `live → write task.ask + wait` vs. `not live → return no_live_session`. The "ephemeral worker" language is only accurate as historical context (pre-#93) and for `niwa_delegate` (which always spawns when no live session exists for the target — by design).

4. **Coordinator auto-registration is fragile.** `maybeRegisterCoordinator` only fires from `niwa_check_messages` and `niwa_await_task`. A coordinator using only `niwa_delegate` + `niwa_query_task` (e.g. a fan-out-then-poll pattern) is never registered, so even with #109 fixed, ask-routing would silently fall through to `no_live_session` until the coordinator happens to call one of those two tools.

## Surprises

- **The "intentionally removed" comment cited in the lead does not exist in the current tree.** It was removed by PR #93 along with the spawn-fallback code it described. The lead's mental model lags behind the code by one merged PR.
- **The coordinator's own PID never moves through any explicit channel to the worker.** The system relies entirely on file-system convention: shared `mainInstanceRoot` + `sessions.json` at a known path inside it. There is no IPC, no socket, no env-passed PID.
- **`handleAsk`'s "live coordinator" check uses `mainInstanceRoot` but `isKnownRole` doesn't** — these two pieces of routing ought to use the same root. They don't.
- **`SessionEntry.InboxDir` is dead-data.** The comment at `types.go:99-101` says: *"Message inboxes are keyed by role under .niwa/roles/<role>/, not by session ID — so InboxDir's per-session usage is historical and no longer read by the MCP server."* `lookupLiveCoordinator` deliberately recomputes the inbox from `<askRoot>/.niwa/roles/coordinator/inbox` instead of trusting the field — `session_registry.go:53-56`.
- **`session_register.go` still creates per-session inbox dirs** (`session_register.go:58-63`) that nothing reads. Vestigial from pre-#93 layout.

## Open Questions

1. Was the `isKnownRole`-vs-`lookupLiveCoordinator` root mismatch known when #93 landed? Is there a deliberate reason `isKnownRole` looks at the worktree (e.g. preventing workers from delegating to roles that exist only in the main root)?
2. For `niwa_send_message(to='coordinator', ...)` (the other path issue #109 mentions): handlers_task lacks a `handleSendMessage` route to `mainInstanceRoot`. Where does `send_message` write today, and does it have the same root-mismatch?
3. Should `maybeRegisterCoordinator` also fire from `niwa_delegate` so a delegate-then-poll coordinator auto-registers? The current trigger set (`niwa_check_messages`, `niwa_await_task`) feels narrow.
4. How should answers from the coordinator flow back to a worker in a *different worktree*? `niwa_finish_task(ask_task_id=...)` writes `task.completed` to the originator's role inbox at `<task-store-root>/.niwa/roles/<originator-role>/inbox/`. For a session worker whose role lives only in the worktree, the main coordinator's inbox-write needs to target the worktree's roles dir — does `taskStoreRoot()` and the surrounding finish-task logic handle this? (Spotted relevant logic at `handlers_task.go:301-311` `resolveInboxDir` reading `SessionLifecycleState.WorktreePath`, but did not chase the full flow.)
5. PR #93 added catch-up scans (`scanInboxForQuestions` at `handlers_task.go:494-521`) and deferred move-to-read on full channels. Is the catch-up scan also resilient to a coordinator that never calls `niwa_await_task` and only ever polls `niwa_check_messages`? (Reading `notifyNewFile` lines 121-140 suggests yes — `task.ask` files stay in the inbox for `niwa_check_messages` to find when no waiter is registered.)

## Summary

Routing to a live coordinator is wired in `handleAsk` (`internal/mcp/server.go:780-843`): for `to="coordinator"` it consults `<mainInstanceRoot>/.niwa/sessions/sessions.json` via `lookupLiveCoordinator`, writes a `task.ask` notification directly into the coordinator's role inbox, and registers an awaitWaiter — there is no longer any `task.delegate`/ephemeral-worker fallback (that path was removed in PR #93, alongside the "intentionally removed" comment the lead references). However, the live-coordinator branch is unreachable from a session worker today: the `isKnownRole(args.To)` precondition at `server.go:802` checks `<worktreePath>/.niwa/roles/coordinator/`, but `scaffoldWorktreeNiwa` (`handlers_session.go:80-108`) only creates a role dir for the worker's own repo, so every worker `niwa_ask(to='coordinator')` returns `UNKNOWN_ROLE` before the routing logic runs — exactly issue #109's failure. The smallest fix is to make `isKnownRole` (and any sibling preconditions) honor the same `mainInstanceRoot` redirect that `askRoot` already uses for coordinator targets, rather than introducing a new role/PID file format under `.niwa/roles/`.
