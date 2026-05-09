# Exploration Findings: niwa-mesh-reliability

Round 1 synthesis of six research leads investigating the cluster of mesh
reliability issues filed since #92.

## The Unifying Pattern

Every issue in scope has the same shape: **niwa stores or relies on state
in the filesystem that the API layer either doesn't read or relies on
implicit discovery to find.** Each fix moves something from "filesystem
convention" to "explicit contract."

| Concern | What's on disk | Where the convention breaks |
|---|---|---|
| Coordinator routing (#92, #109) | `sessions.json` records the live coordinator | `isKnownRole` checks the worker's local roles dir, not the main instance's |
| Plugin propagation (#108) | `<workspaceRoot>/.claude/settings.json` has the workspace plugin set | Worker spawn relies on Claude Code's CWD-walk discovery to find it; empirically unreliable |
| Skill delivery (#97) | `<repoPath>/.claude/skills/niwa-mesh/SKILL.md` in every consumer repo's working tree | Agents `git add` it into PRs |
| Spawn success (#110) | `daemon.pid` written only after fsnotify registers | The 500ms wait result is silently discarded |
| Daemon liveness (#111) | `daemon.pid` + `IsPIDAlive` already exist | `niwa_list_sessions` returns persisted `Status` without probing |
| Task state (#112) | Envelope quarantined in `inbox/dangling/` | `state.json` never transitions; query/cancel/await all return stale `queued` |

Two practical consequences:
- The runtime narratives in some issues lag behind merged PRs by one
  commit. Issue #92's "ephemeral worker fabricates an approval" narrative
  describes the pre-PR-#93 world; today's `handleAsk` already routes to
  the live coordinator. The remaining bug is the precondition that
  rejects the routing path before it runs.
- The fixes cluster into two categorical strategies: lift filesystem
  state into the API (#92, #109, #110, #111, #112), and replace
  filesystem-side-channel discovery with explicit injection (#97, #108).

## Findings by Lead

### Lead 1: Coordinator routing (#92, #109)

`handleAsk` (`internal/mcp/server.go:780-843`) ALREADY implements
live-coordinator routing — PR #93 closed the original spawn-fabrication
half of #92. For `to="coordinator"`, it consults
`<mainInstanceRoot>/.niwa/sessions/sessions.json` via
`lookupLiveCoordinator`, writes a `task.ask` notification directly to the
coordinator's role inbox, and registers an awaitWaiter. The "no live
session" branch returns `{"status":"no_live_session"}` synchronously
without writing any envelope.

**The remaining bug:** `isKnownRole(args.To)` at `server.go:802` runs
against `<s.instanceRoot>/.niwa/roles/<role>/` — for a worker, that's the
worktree path. `scaffoldWorktreeNiwa` (`handlers_session.go:80-108`) only
creates `roles/<repo>/`, never `roles/coordinator/`. So workers fail at
`UNKNOWN_ROLE` before the routing logic at line 817 ever runs. PR #93
hoisted the lookup to `mainInstanceRoot` but did NOT hoist the
role-existence precondition. The unit tests at
`session_registry_ask_test.go` set `instanceRoot == mainInstanceRoot`,
so the regression is invisible.

**Auto-registration gap:** `maybeRegisterCoordinator` only fires from
`niwa_check_messages` and `niwa_await_task`. A coordinator that only
calls `niwa_delegate` + `niwa_query_task` (a fan-out-then-poll pattern)
never registers, so even with the precondition fixed, ask-routing falls
through to `no_live_session`.

**Fix shape:**
- Make `isKnownRole(role)` honor `mainInstanceRoot` when
  `role == "coordinator" && s.mainInstanceRoot != ""`. Mirror the same
  redirect `askRoot` already does at `server.go:817-819`. Apply the
  same change to `handleSendMessage` (and any sibling preconditions).
- Also call `maybeRegisterCoordinator` from `handleDelegate` so
  fan-out-then-poll coordinators auto-register.
- No new role/PID file format. No worktree role-dir provisioning.

### Lead 2: Worker spawn environment (#108, #97)

The two issues are independent in mechanism even though both manifest in
worker sessions.

**#108 root cause:** `spawnWorker` (`internal/cli/mesh_watch.go:908-1016`)
invokes `claude -p` with no `--plugin`, `--marketplace`, `--settings`, or
`CLAUDE_CONFIG_DIR` flag. Workers inherit only HOME and PATH, plus three
NIWA_* env vars. Plugin discovery is left entirely to Claude Code's
CWD-based walk-up — which empirically does not surface workspace plugins
when CWD is a session worktree under `.niwa/worktrees/`. The repo case
fails too because workspace `enabledPlugins` reference aliases like
`shirabe@shirabe`; if the worker's user-level `~/.claude.json` doesn't
know that marketplace, the alias drops silently.

Three fix injection points exist, in increasing surgical scope:
1. **argv flag** to `claude -p` (`mesh_watch.go:982-1001`) — depends on
   Claude Code's accepting `--settings` or `--plugin`/`--marketplace`.
2. **env injection** of `CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude` next
   to existing NIWA_* env at `mesh_watch.go:1005-1009`.
3. **filesystem mirror** in `scaffoldWorktreeNiwa`
   (`handlers_session.go:80-108`) — symlink or copy
   `<mainInstance>/.claude/settings.json` into
   `<worktree>/.claude/settings.local.json`. One-function change. Most
   robust to Claude Code discovery quirks since it puts settings exactly
   where Claude Code will look first.

**#97 root cause:** `InstallChannelInfrastructure`
(`internal/workspace/channels.go:347-359`) unconditionally writes
`<repoPath>/.claude/skills/niwa-mesh/SKILL.md` for every non-coordinator
role on every `niwa apply`. Comment at `channels.go:338-339` calls this
"Decision 5: flat uniform skill" — current intended behavior. The design
didn't anticipate that worker agents would `git add .` and commit it.

Fix: stop writing per-repo copies. Deliver the skill via `CLAUDE.local.md`
injection (already used for workspace context) or via the instance-root
copy only, with appropriate Claude Code discovery configuration so workers
still find it.

### Lead 3: Session and daemon health (#110, #111)

The daemon already writes a single canonical readiness signal:
`daemon.pid` is written ONLY after fsnotify watcher registration succeeds
(`internal/cli/mesh_watch.go:283-287`). The comment is explicit:
> "Write PID file atomically AFTER watches are registered and the lock
> is held so EnsureDaemonRunning's pid-file-appears signal means the
> daemon really can accept events."

`EnsureDaemonRunning` (`internal/workspace/daemon.go:35-102`) already
polls for that signal for 500ms, but **silently returns nil on timeout**
with the comment: *"Timed out — daemon may have failed to start. Return
nil so Create/Apply still succeed; the missing PID file is the observable
failure signal."* This is the contract that needs to flip.

The MCP `niwa_create_session` handler (`handlers_session.go:146-228`)
already handles `daemonStarter` errors by populating `daemon_warning` —
but with the timeout silently swallowed, that field is essentially
unreachable for the inotify-exhaustion failure mode in #110.

For #111: `handleListSessions` (`handlers_session.go:26-50`) returns
persisted `SessionLifecycleState.Status` verbatim. `mcp.IsPIDAlive`
(`internal/mcp/liveness.go:14-35`) already exists, used by
`lookupLiveCoordinator` and `EnsureDaemonRunning` itself, with PID-recycle
protection via `/proc/<pid>/stat` start-time cross-check. No heartbeat
file exists or is needed.

**Fix shape:**
- `EnsureDaemonRunning` returns a typed `ErrDaemonSpawnTimeout` on the
  500ms timeout. `handleCreateSession` rolls back the worktree, branch,
  and session-state file and returns `IsError: true`.
- Persisted `Status` keeps its lifecycle-marker meaning (single writer:
  the lifecycle code path). Add a separate computed `daemon` sub-object
  to `niwa_list_sessions` entries: `{alive, pid, started_at}`. Probe by
  reading `<worktreePath>/.niwa/daemon.pid` and calling `IsPIDAlive`.
- Watch out for the empty `daemon.pid` placeholder pre-created by
  `scaffoldWorktreeNiwa` (`handlers_session.go:97-105`). Existing
  `ReadPIDFile` returns `(0, 0, nil)` for missing and an error for
  present-but-empty, so `IsPIDAlive(0, 0)` correctly returns false.

### Lead 4: Task lifecycle and dangling (#112)

`dangling` is **not a state** — it's a filesystem quarantine
(`inbox/<role>/dangling/`). Five legal states only:
queued/running/completed/abandoned/cancelled (`internal/mcp/types.go:171-189`).

The classifier (`mesh_watch.go:776-803`) fires iff:
1. The inbox file body has `type == "task.delegate"` (peer messages are
   immune — `mesh_watch.go:746-758`).
2. `<mainInstanceRoot>/.niwa/tasks/<id>/state.json` does not exist.

Under normal `niwa_delegate` flow, dangling is **structurally impossible**
because `createTaskEnvelope` writes state.json BEFORE the inbox file
(`handlers_task.go:177-258`). Field repro requires the task store to
have been wiped (manual cleanup, partial workspace destroy, fresh
checkout, etc.) while the inbox envelope survived.

**Stickiness is deterministic, not stateful.** fsnotify doesn't watch
`dangling/`, and `scanExistingInboxes` skips directories at startup. If
an operator manually moves the file back, the same `os.Stat(state.json)`
check fails again and the file gets renamed back. There is no
in-memory cache; the loop is the inevitable consequence of the
classification rule.

**API surface lies:**
- `niwa_query_task` and `niwa_list_outbound_tasks` read state.json
  (which the daemon never transitions for dangling) and return
  `state="queued"`.
- `niwa_cancel_task` returns `{status:"too_late",current_state:"queued"}`
  because it only renames `inbox/<id>.json` and treats the resulting
  ENOENT as "daemon already claimed it" — the same response shape as a
  legitimate claim race.
- `niwa_update_task` partially "succeeds": the state.json mutator bumps
  `updated_at` before the inbox stat fails, then returns
  `consumed`/`too_late`. The body change is silently lost.
- `niwa_await_task` blocks the full 10-minute timeout for nothing.

There is no `niwa_resurrect_task` and no operator path. Manual filesystem
surgery is the only field fix today.

**Two design choices need resolution:**
- **(A)** Make `dangling` a real state.json state. Daemon transitions
  state.json on quarantine. `query_task`/`list`/`await` surface the
  truth. Cancel/update/await responses become structurally consistent.
- **(B)** Add `niwa_resurrect_task(task_id)` as an opt-in recovery
  primitive. Reverts the rename, leaves state.json untouched, daemon
  picks the file back up via fsnotify CREATE. Works if the underlying
  cause has been resolved.

These are not mutually exclusive. (A) gives the API truthfulness; (B)
gives the recovery primitive. Together they restore the documented
contract. Lead 5 also notes that `niwa_redelegate` should accept
dangling source tasks — covering the case where (B)'s precondition
(state.json intact) fails.

### Lead 5: Delegate API extensions (#113, #114)

`niwa_delegate` (`server.go:264-279`, `handlers_task.go:111-165`) accepts
opaque `body` plus `to/mode/expires_at/session_id/read_only`. The only
structured precondition today is `SESSION_REQUIRED`; `UNKNOWN_ROLE` is
just an `os.Stat` of the role dir. There is no skill/manifest awareness.

**The task store is flat by task_id, not partitioned by state.** Every
task lives at `<taskStoreRoot>/.niwa/tasks/<id>/{envelope.json,state.json}`
for its entire lifetime — only the inbox **message** moves between
subdirs (in-progress/cancelled/expired/dangling/...). This makes
`niwa_redelegate` trivial because `ReadState(taskDirPath(...))` resolves
the source regardless of inbox state.

**`required_skills` gate (#113)** slots between `UNKNOWN_ROLE` and
`createTaskEnvelope` in `handleDelegate` (`handlers_task.go:130-141`).
Body convention (`body.required_skills: string[]`) keeps it inside the
opaque-body contract; a new `MISSING_SKILLS` error code returns
`{missing, available}` without allocating a task ID. Depends on lead 2's
plugin manifest source-of-truth.

**`niwa_redelegate` shape (#114):**
- `kindDelegator` auth on `source_task_id` (mirrors cancel/update).
- Read source via `ReadState`. Reject if source state is `queued` or
  `running` (no concurrent dual-fanout) — caller cancels first.
- Allow re-delegation from `abandoned`, `cancelled`, `completed`, and
  from dangling envelopes (detect by state==queued + inbox file absence).
- Regenerate: id, sent_at, parent_task_id, state.json. Propagate
  (overridable): body (with shallow `body_overrides` merge), to.role,
  session_id, expires_at. Reset `from` to caller for correct attribution.
- Add `redelegated_from` envelope field for audit chain.
- Run the same `required_skills` gate.

**Surprise — dead vocabulary:** `TaskEnvelope.deadline_at` is declared
in types.go:222 but no code path writes it. `inbox/read/` is provisioned
but never used. Both are out of scope but worth noting.

### Lead 6: Skill contract audit

The `niwa-mesh` skill is generated by `buildSkillContent`
(`internal/workspace/channels.go:682-833`) and shipped to two locations:
`<instance>/.claude/skills/niwa-mesh/SKILL.md` plus a per-repo copy in
every non-coordinator role's working tree (channels.go:354). Eight or
more byte-identical copies live in the workspace tree today.

**Of nine first-class runtime claims, six are broken:**

| Claim | Status | Issue |
|---|---|---|
| `dangling` not in state machine docs | Broken — daemon emits it | #112 |
| Worker can `niwa_ask(to="coordinator")` | Broken — `UNKNOWN_ROLE` synchronously | #109 |
| Message vocab includes `question.ask`/`question.answer`/`status.update` | Broken — no handler dispatches them | (silent) |
| `task.delegate`/`task.ask` NOT in vocab | Broken — these are the actually-routed types | (silent) |
| `niwa_create_session` "starts the daemon" | Broken — failures not surfaced | #110 |
| `niwa_list_sessions` "rediscover active sessions" | Broken — `status` is not health | #111 |
| Worker has access to coordinator-mandated skills | Broken — workspace plugins missing | #108 |
| Skill file should not appear in PRs | Broken — committed by agents | #97 |
| Recovery from abandoned/dangling | Gap — no `niwa_redelegate` | #114 |

Workers that take the message vocabulary literally and emit
`question.ask` are silently ignored — the watcher dispatches only
`task.*` types. This is a documentation footgun that has been latent
forever.

The skill is generated from a single source-of-truth function, so
bringing it back into alignment is a one-PR change for the text. The
runtime fixes need to land first to give the new text something
truthful to describe.

## Open Questions Surviving Round 1

1. **#108 plugin propagation mechanism.** Three injection points
   identified (argv flag, env var, filesystem mirror). The choice
   depends on which Claude Code-side flags are stable and on whether
   we want session and main-instance workers to share the mechanism.
   Filesystem mirror via `scaffoldWorktreeNiwa` is the most surgical;
   env-based `CLAUDE_CONFIG_DIR` is the most explicit. **Resolution
   needed in design doc.**
2. **#112 dangling: state vs primitive vs both.** Two design options
   (A = real state, B = resurrect primitive) are not mutually
   exclusive. The cleanest model commits to one and documents it
   honestly. **Resolution needed in design doc.**
3. **#97 skill delivery.** Stop writing per-repo copies → deliver via
   `CLAUDE.local.md` injection, or via instance-root copy only with
   Claude Code discovery configured to find it. The choice ties into
   the plugin-propagation mechanism from #108. **Resolution needed.**
4. **`required_skills` placement.** Inside `body` (matches existing
   "opaque body" convention) or as top-level `delegateArgs` field
   (better audit log fidelity)? **Decision needed but minor.**
5. **Worker auto-registration of `coordinator` role dir.** Two options:
   special-case `to == "coordinator"` in `isKnownRole`, OR have
   `scaffoldWorktreeNiwa` create a synthetic `roles/coordinator/inbox/`
   in worktrees. The first is surgical; the second is more uniform.
   **Resolution needed but small.**

These are all answerable inside a single design doc. None of them
require independent feasibility investigation — the implementations are
clear, the trade-offs are visible, and the unit tests are tight enough
that wrong choices would surface quickly.

## Decision: Crystallize

The findings strongly converge on a single coordinated design rather
than nine independent bugfixes. The issues share root causes (file/API
state asymmetry), share affected modules
(`internal/mcp/`, `internal/workspace/`, `internal/cli/mesh_watch.go`),
and share user-facing surfaces (the niwa-mesh skill, `docs/guides/sessions.md`).
Splitting them into nine PRs would generate cross-cutting churn and
miss the chance to bring the skill text and runtime contract back into
lockstep in one pass.

Proceeding to Phase 4.
