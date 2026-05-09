# Lead: What is the actual implemented task lifecycle in the daemon, what triggers `dangling`, and how is the state visible through the API today?

## Findings

### 1. Where the watch loop lives

The "daemon" referenced in issue #112 is `niwa mesh watch`, implemented in
`internal/cli/mesh_watch.go` (2,394 lines). `internal/workspace/daemon.go` is
just the spawner — `EnsureDaemonRunning` (`internal/workspace/daemon.go:35`) —
called from the `apply` flow.

The central event loop is `runEventLoop`
(`internal/cli/mesh_watch.go:460-523`). It selects across:

- `catchupCh` — pre-existing envelopes scanned at startup
  (`scanExistingInboxes`, `internal/cli/mesh_watch.go:2273-2297`).
- `watcher.Events` — fsnotify CREATE events on every
  `.niwa/roles/<role>/inbox/` directory registered by
  `registerInboxWatches` (`internal/cli/mesh_watch.go:2236-2266`). Subdirs
  (`in-progress/`, `dangling/`, `cancelled/`, etc.) are not watched.
- `watcher.Errors` — fsnotify errors are logged only; **there is no periodic
  resync** (comment at `internal/cli/mesh_watch.go:509-513` flags this as a
  known follow-up). One-shot catch-up only at startup.
- `exitCh` — supervisor goroutines reporting worker exits.

Both inbox event sources funnel into `handleInboxEvent`
(`internal/cli/mesh_watch.go:776-898`).

### 2. The implemented state machine (vs. what skill docs claim)

`internal/mcp/types.go:173-200` defines the **only** legal `state.json.state`
values:

```go
const (
    TaskStateQueued    = "queued"
    TaskStateRunning   = "running"
    TaskStateCompleted = "completed"
    TaskStateAbandoned = "abandoned"
    TaskStateCancelled = "cancelled"
)

var validTaskStates = map[string]bool{ ... } // these 5 only
```

`validateState` (`internal/mcp/taskstore.go:228-239`) returns
`ErrCorruptedState` for any other value. **`dangling` is not a state.json
state.** It is purely a **filesystem-level holding area** —
`<role>/inbox/dangling/<task-id>.json`. The skill doc
(`/.claude/skills/niwa-mesh/SKILL.md`) describes the
queued/running/terminal lifecycle and does not mention `dangling` anywhere.
This matches issue #112's complaint: dangling is undocumented because it is
literally not a state, just a misplaced envelope.

### 3. The exact dangling trigger

From `handleInboxEvent` (`internal/cli/mesh_watch.go:776-803`):

```go
func handleInboxEvent(evt inboxEvent, s spawnContext) {
    if !daemonOwnsInboxFile(evt.filePath, evt.taskID) {
        return // peer message; left for MCP server
    }
    taskDir := filepath.Join(s.taskStoreRootDir(), ".niwa", "tasks", evt.taskID)
    if _, err := os.Stat(filepath.Join(taskDir, "state.json")); err != nil {
        // Dangling delegate envelope ...
        s.logger.Printf("inbox_event role=%s task=%s skip=dangling path=%s", ...)
        danglingDir := filepath.Join(filepath.Dir(evt.filePath), "dangling")
        os.MkdirAll(danglingDir, 0o700)
        danglingPath := filepath.Join(danglingDir, filepath.Base(evt.filePath))
        os.Rename(evt.filePath, danglingPath)
        return
    }
    ...
}
```

The classification fires **iff both** of the following hold:

1. `daemonOwnsInboxFile` returns true — the file body parses as JSON with
   `type == "task.delegate"` (`internal/cli/mesh_watch.go:746-758`). Peer
   messages (`task.completed`, `task.abandoned`, `task.cancelled`,
   `question.ask`, malformed JSON) are explicitly **not** owned by the
   daemon and are left in place. (`TestDaemonOwnsInboxFile_DelegatesAndPeerMessages`
   at `mesh_watch_test.go:151-235` is the regression test.)
2. `<taskstoreroot>/.niwa/tasks/<task-id>/state.json` does not exist
   (`os.Stat` returns an error). The taskstore lives at the **main instance
   root** (`internal/mcp/handlers_session.go:317`), even when the inbox
   file lives in a session worktree.

That is the **only** dangling trigger in the codebase. There is **no timeout
path**, **no missing-daemon check** (the daemon classifying it is itself the
daemon — by definition it's running), **no multi-cycle counter**. A task is
dangling iff its envelope sits in the inbox without a corresponding
`state.json`.

### 4. How an envelope can end up dangling

`createTaskEnvelope` (`internal/mcp/handlers_task.go:177-258`) writes
**state.json BEFORE** placing the envelope in the inbox:

1. `MkdirAll(taskDir)` — `internal/mcp/handlers_task.go:187`
2. `WriteFile(envelope.json)` — line 210
3. `WriteFile(state.json)` (initial state = `queued`) — line 235
4. `writeMessageAtomic(inboxDir, taskID, msg)` — line 255 (writes
   `<taskID>.json.tmp` then `os.Rename`)

So under normal `niwa_delegate` flow, dangling is **structurally impossible**
— state.json exists before the inbox file. Dangling only occurs when:

- The taskstore (`<mainInstanceRoot>/.niwa/tasks/<id>/`) was deleted out from
  under the daemon (manual cleanup, partial workspace destroy, disk loss).
- An envelope was hand-written into the inbox without going through
  `createTaskEnvelope` (test fixture, manual seeding, third-party tooling).
  This is how `TestHandleInboxEvent_DanglingEnvelope`
  (`mesh_watch_test.go:743-784`) triggers it.
- A worker session was destroyed via `niwa_destroy_session` while a task was
  still queued in that worktree's inbox, then the corresponding task dir
  (which lives in the main instance, not the worktree) was somehow lost.
  Reading `internal/mcp/session_lifecycle.go` and the session-destroy path
  did not show any code that deletes task dirs; the more likely path in the
  field is either manual `rm -rf .niwa/tasks/` or a test-harness fixture.

So the most plausible field repro is: a delegator restarts/rebuilds in a way
that wipes `.niwa/tasks/` (e.g. fresh checkout, `git clean`) while a stale
inbox envelope survives in a worker's worktree.

### 5. Stickiness of the dangling classification

**Dangling is sticky in code.** The classifier is the single decision
`os.Stat(state.json) != nil`. There is no scrubbing pass, no recovery hook,
and no in-memory "we already moved this" cache. Behaviour after the move:

- The file now lives in `inbox/dangling/<task-id>.json`. fsnotify is
  **only** registered on `inbox/`, not on `dangling/` (see
  `registerInboxWatches`, `internal/cli/mesh_watch.go:2236-2266`), so no
  more CREATE events fire for the moved file.
- `scanExistingInboxes` (`internal/cli/mesh_watch.go:2273-2297`) skips
  directories: `if e.IsDir() { continue }`. So even at the next daemon
  startup, the dangling file is **never re-enqueued**.
- **However**, if an operator manually moves the file back to `inbox/`
  (e.g. `mv inbox/dangling/<id>.json inbox/`), fsnotify fires CREATE,
  `handleInboxEvent` runs, the same `state.json` Stat fails (because
  nothing changed about the taskstore), and the file gets renamed back
  into `dangling/`. That is exactly the loop issue #112 describes — the
  classification is **deterministic**, not sticky-by-side-state, so there
  is no way for the operator to break out of it without also restoring
  `<mainInstanceRoot>/.niwa/tasks/<task-id>/state.json`.

### 6. How `niwa_query_task` represents a dangling envelope

`handleQueryTask` (`internal/mcp/handlers_task.go:387-393`):

```go
func (s *Server) handleQueryTask(args queryTaskArgs) toolResult {
    _, st, errR := authorizeTaskCall(s.identity(), args.TaskID, kindParty)
    if errR != nil {
        return *errR
    }
    return textResult(formatQueryResult(st))
}
```

`formatQueryResult` (`internal/mcp/handlers_task.go:937-960`) returns the
literal `state` field from `state.json`. **This call only succeeds if
state.json exists** — `authorizeTaskCall` ultimately runs `ReadState`
(`internal/mcp/taskstore.go:164-185`), which fails when state.json is
missing.

If a task is dangling **and the taskstore was wiped** (the field repro), the
query returns `NOT_TASK_PARTY` or a corrupted-state error rather than
`queued`. If a task is dangling because state.json **survived** while the
envelope was moved manually — the precise scenario where the daemon classifies
it dangling — then `query_task` reads state.json and returns
`state="queued"`, since the daemon never transitioned state.json (only the
inbox file moved). That matches issue #112's report exactly. The queue/dangling
distinction is invisible at the API.

### 7. How `niwa_list_outbound_tasks` represents a dangling envelope

`handleListOutboundTasks` (`internal/mcp/handlers_task.go:710-771`)
enumerates `<taskstoreroot>/.niwa/tasks/*/`, calls `ReadState` on each, and
populates `state` from `state.json`. Same observation: it never inspects the
inbox filesystem layout, so a dangling task with an intact state.json shows
up as `state="queued"`.

### 8. Why `niwa_cancel_task` returns `{status: "too_late", current_state: "queued"}`

`handleCancelTask` (`internal/mcp/handlers_task.go:878-931`):

```go
src := filepath.Join(inboxDir, args.TaskID+".json")
cancelledDir := filepath.Join(inboxDir, "cancelled")
...
dst := filepath.Join(cancelledDir, args.TaskID+".json")
if err := os.Rename(src, dst); err != nil {
    if os.IsNotExist(err) {
        // Daemon already claimed the envelope. Return current state.
        if _, st, err := ReadState(taskDir); err == nil {
            return textResult(fmt.Sprintf(
                `{"status":"too_late","current_state":%q}`, st.State))
        }
        return textResult(`{"status":"too_late","current_state":"consumed"}`)
    }
    ...
}
```

Cancel only looks at `inbox/<task-id>.json` — it never checks
`inbox/dangling/<task-id>.json` (or `inbox/in-progress/`, or any sibling
subdir). When the envelope has been moved to `dangling/`:

1. `os.Rename(inbox/<id>.json → inbox/cancelled/<id>.json)` fails with
   ENOENT.
2. The handler treats ENOENT as "daemon already claimed it" and reads
   state.json.
3. State.json says `queued` (the daemon only moved the file, never
   transitioned state).
4. Handler returns `{"status":"too_late","current_state":"queued"}`.

The `too_late` semantics were designed for the **claim race** (daemon won
the rename → envelope is now in `in-progress/`, state is `running`). The
dangling case reuses the same code path but produces an apparently
contradictory pair (`too_late` + `queued`) because the state and the
envelope location are out of sync.

### 9. Other API surfaces

- `niwa_update_task` (`internal/mcp/handlers_task.go:793-874`) has the same
  shape: it Stats `inbox/<id>.json` at line 854-859 and returns
  `{"status":"too_late","current_state":"consumed"}` on ENOENT. For a
  dangling envelope this would surface as `consumed` because the rejection
  happens after the state.json read, but before the inbox stat the
  state-mutator returns `nil, nil, nil` for non-queued; for **queued** state
  the mutator falls through and the inbox stat then trips. Net effect: a
  dangling task with state.json=`queued` would let the state.json rewrite
  succeed (bumping `updated_at`), then fail at the inbox stat with
  `consumed`. That's another inconsistent surface.
- `niwa_await_task` (`internal/mcp/handlers_task.go:397-469`) registers a
  waiter and blocks until state.json transitions or timeout. For a dangling
  task, state.json never transitions, so await blocks the full timeout (10
  minutes default) and returns
  `{"status":"timeout","current_state":"queued"}`. Same illusion of progress.
- `niwa_delegate(mode="sync")` would block forever on the awaitWaiter unless
  the caller passed a timeout via async + await; sync mode has no timeout
  (`handlers_task.go:160-164`).

### 10. No resurrect / recover primitive exists

`grep -rn "resurrect|recover|reclassify|requeue"` across the repo turned up
zero references in production code. There is no `niwa_resurrect_task`, no
admin API, no CLI subcommand to move a file out of `dangling/`. The only
operator path documented in code is the comment at
`internal/cli/mesh_watch.go:789-791`: "for operator inspection". The dangling
directory is essentially a quarantine: anything that lands there is
unrecoverable through niwa's tools.

### 11. Shape of an opt-in `niwa_resurrect_task` primitive

A minimal resurrect would need to:

- **Inputs**: `task_id` (UUIDv4), maybe `reason` for audit. Implicitly
  scoped to the caller's role-as-delegator.
- **Authorization**: `kindDelegator` (same as `niwa_cancel_task`/
  `niwa_update_task`). Must reject if state.json doesn't exist —
  resurrect cannot fabricate a task. Must reject if state is terminal
  (`isTaskStateTerminal`) — already-done tasks should not bounce back to
  queued.
- **State guard**: state.json must show `state == "queued"`. If it shows
  `running`/terminal, the dangling/active mismatch is more severe than a
  resurrect can fix and the call should fail with a diagnostic.
- **Filesystem mutation**: `os.Rename(inbox/dangling/<id>.json →
  inbox/<id>.json)` under taskstore flock semantics; ENOENT means it isn't
  actually dangling. After the rename, fsnotify will fire CREATE in the
  watched `inbox/` and `handleInboxEvent` will retry. If state.json is
  intact, the daemon will claim it normally; if state.json is missing,
  it'll bounce right back to dangling — so resurrect must read state.json
  first and refuse if missing.
- **Audit**: append a `TransitionLogEntry` (`Kind: "resurrected"` or
  reused state-transition `queued → queued` with actor) so the audit trail
  shows the operator-driven resurrection.
- **Race with daemon claim**: if the task was somehow simultaneously claimed
  (state.json transitioned to running during the rename), the daemon will
  see a stray inbox file with no `<id>.json` left to claim — the standard
  catch-up scan handles this fine. The risk is that `inbox/<id>.json`
  rename clobbers an in-progress envelope; guard with `os.Link` followed by
  `os.Remove` (or stat-then-rename under a flock the cancel path also
  honors).
- **Public-API surface vs. CLI-only**: keeping it CLI-only (`niwa task
  resurrect <id>`) avoids broadening the MCP surface for an
  operator-recovery path that workers themselves should never need.

The bigger design question — surfaced by the inconsistencies in §6/§8 — is
whether dangling deserves to be a real state.json state (so query_task can
return `state="dangling"` honestly) or whether the recovery primitive
should aim to make dangling structurally impossible (e.g. stop quarantining
and instead transition state.json to `abandoned` with reason
`taskstore_lost`).

## Implications

- **API truthfulness is broken for dangling tasks.** Every tool that returns
  `state` reads state.json, which is intentionally unaware of the
  inbox-filesystem holding area. Callers that try to recover via
  `cancel`/`update`/`await` get answers consistent only with state.json,
  not with the actual envelope location.
- **The `too_late` shape is overloaded.** Today it covers (a) daemon
  already claimed the envelope (state moved past `queued`), (b) envelope
  consumed via ENOENT race, and (c) envelope yanked into `dangling/`. The
  three cases differ semantically but share a response format.
- **Dangling tasks consume taskstore disk forever.** Nothing scans
  `inbox/dangling/` and nothing reaps `.niwa/tasks/<id>/` for tasks that
  are stuck `queued`. `list_outbound_tasks` will keep showing them.
- **Operator recovery requires manual filesystem surgery.** The only field
  fix today is "find the task dir, recreate state.json by hand, mv the
  envelope back to inbox/" — error-prone and undocumented.
- **The classifier is correct but the policy is harsh.** Quarantine
  prevents the inotify-overflow / repeated-CREATE thrash that motivated
  the rename. But it's irreversible from the tool surface, and §6
  shows the symptom is invisible to delegators.

## Surprises

- **fsnotify does not watch `dangling/`.** I expected the daemon to keep an
  eye on the quarantine to surface auto-recovery if state.json reappeared.
  It does not.
- **There is no periodic resync.** A dropped fsnotify event means a task
  sits in `inbox/` forever until daemon restart triggers
  `scanExistingInboxes`. Comment at line 509-513 explicitly tags this as a
  known follow-up.
- **The dangling check uses a different root than the inbox.** Inbox
  pathing follows the session worktree
  (`internal/mcp/handlers_task.go:265-292`), while
  `s.taskStoreRootDir()` returns the main instance root. So a session-scoped
  dangle is governed by `<mainInstance>/.niwa/tasks/<id>/state.json`
  existing, not anything inside the worktree.
- **`update_task` would partially "succeed" on a dangling envelope.** The
  state.json mutator runs and bumps `updated_at` before the inbox stat
  fails. The audit log gets a no-op entry but the body change is silently
  lost.
- **Dangling can mask serious data loss.** If `.niwa/tasks/` is wiped, the
  daemon classifies the orphaned envelope as dangling but **the tools
  themselves would not be able to read state, returning corrupted-state
  errors instead of `queued`.** The `state="queued"` symptom in #112
  implies state.json survived — meaning the trigger in the field is more
  subtle than "taskstore was deleted."
- **`task.completed`/`task.abandoned`/`task.cancelled` peer messages live
  in the same inbox but are NOT subject to the dangling check.** The
  delegate-only ownership predicate
  (`internal/cli/mesh_watch.go:746-758`) is the bug-fix referenced in
  `mesh_watch_test.go:151-235` — peer terminal events used to be yanked
  into `dangling/`, dropping awaitWaiter wakeups. The fix scopes dangling
  to delegate envelopes only.

## Open Questions

- What real-world sequence in the field produces a dangling envelope with
  state.json **intact**? The createTaskEnvelope ordering says it shouldn't
  happen normally. Is it manual cleanup, partial workspace destroy, the
  `niwa destroy --instance` scope, an unobserved race in
  `writeMessageAtomic`'s temp+rename, or something in `niwa init` /
  `niwa apply` that wipes `.niwa/tasks/` while preserving `.niwa/roles/`?
- Should `dangling` become a real `state.json` state, with the daemon
  transitioning state.json on quarantine? That would make
  `query_task`/`list_outbound_tasks`/`await_task` all surface the truth
  and enable a clean `niwa_cancel_task` shape (`status:"cancelled"`,
  prior state in audit).
- Should resurrect be MCP-tool-callable or CLI-only? Workers themselves
  should never need it; coordinators occasionally might. The skill-doc
  surface (`mcp__niwa__niwa_resurrect_task`) widens the trust boundary
  meaningfully.
- Is there an inotify-overflow / dropped-CREATE failure mode that would
  cause a non-dangling task to stay stuck in `inbox/` forever (the
  follow-up flagged at `mesh_watch.go:509-513`), and does that interact
  with #112's symptom?
- Does `niwa destroy` (per-instance / per-session modes) ever delete
  `.niwa/tasks/<id>/` while leaving inbox envelopes in worktrees? The
  recent destroy rework (`24b5b67`, `e154049`) needs cross-checking
  against this scenario.
- Should `niwa_cancel_task` look in `inbox/dangling/` as a fallback
  rename source so dangling tasks become operator-cancellable today,
  even before resurrect lands?

## Summary

The "dangling" classification is a daemon-only filesystem quarantine
(`inbox/<role>/dangling/`), not a real state.json state — it triggers
exclusively when `handleInboxEvent` sees a `task.delegate` envelope whose
`<mainInstance>/.niwa/tasks/<id>/state.json` is missing
(`internal/cli/mesh_watch.go:783-803`); there is no timeout, no missing-daemon
check, no multi-cycle counter. Dangling is sticky because fsnotify does not
watch the dangling subdir and `scanExistingInboxes` skips directories, so
files only re-enter the loop if an operator manually moves them back, at
which point the same deterministic classifier renames them straight back.
The API surface lies about this: `niwa_query_task` and `niwa_list_outbound_tasks`
both read state.json (which the daemon never transitions) and report
`state="queued"`, while `niwa_cancel_task` returns
`{status:"too_late",current_state:"queued"}` because it only knows how to
rename `inbox/<id>.json` and treats the resulting ENOENT as "daemon claimed
it" — there is no `niwa_resurrect_task` and no operator path to recover a
dangled envelope through niwa's tools today.
