---
status: Draft
source_issue: 117
problem: |
  When a worker agent in a niwa mesh session hits an interesting edge case —
  abandons mid-task, makes a questionable decision, hits a constraint, or
  stalls — the workspace coordinator's only recovery options today are to let
  the worker complete on its own, send a one-way `niwa_send_message`, or
  destroy the session and restart from scratch. There is no human-in-the-loop
  primitive that lets an operator step into a session, see what the agent has
  done, prompt it interactively, fix things manually, and hand it back
  without losing context or fighting the mesh for control.
goals: |
  Operators can attach to any active session in their workspace instance,
  resume Claude Code with the worker's full transcript history, work
  interactively, and detach cleanly with the session returning to normal mesh
  operation. Discovery, lock acquisition, daemon coordination, and transcript
  loading happen end-to-end through a single `niwa session attach <id>`
  command. Operators can break stale locks via `niwa session detach <id>
  --force`. The lock state is visible to coordinators (via filesystem-readable
  state) so the mesh naturally backs off without push notifications or new
  MCP tools.
---

# PRD: niwa session attach

## Status

Draft

## Problem Statement

A workspace coordinator runs multi-step work through the niwa mesh. Today,
when a worker hits an interesting edge case — drifts off-task, makes a
questionable design call, stalls waiting for input it cannot get, or runs
into a constraint that requires human judgement — the coordinator has three
options:

1. Let the worker complete on its own. Wastes time when the trajectory is
   already wrong.
2. Send `niwa_send_message`. One-way and unacknowledged; the worker may not
   read it, may misinterpret it, and there is no way for the coordinator to
   confirm the prompt landed.
3. Destroy the session and restart. Discards everything the worker already
   produced — including unpushed commits, in-flight design decisions, and
   the conversation context that may represent significant work.

There is no fourth option that lets a human take the conversation over,
inspect what the agent has done, drive it interactively for a few turns,
and hand it back. This forces a binary choice between trusting an agent
that's already off-track and discarding accumulated context.

The underlying mechanism for the fourth option already exists. niwa
captures `claude_conversation_id` after the worker's first task exits and
re-uses it via `claude --resume` for every subsequent worker spawn in the
session. The same `--resume` mechanic, invoked by a human in the worktree,
would land them in Claude Code with the worker's full transcript loaded.
What's missing is the orchestration: lock acquisition so the daemon stops
spawning workers while the human is in the session, validation that the
transcript is loadable, the discovery surface to find the right session,
and the operator-friendly UX around it all.

## Goals

1. **Human-in-the-loop primitive**. An operator can step into any active
   session, see the worker's full conversation history, prompt the agent
   interactively or fix things manually, and exit cleanly — without
   destroying state.

2. **Mesh-safe by construction**. While an operator is attached, the mesh
   does not spawn workers in that session. When the operator detaches, the
   mesh resumes naturally and processes any envelopes that queued during
   attach.

3. **Recoverable from failures**. Stale locks (SSH disconnect, terminal
   crash, host reboot) are detectable from the next `session list` poll
   and breakable via an explicit `detach --force` command. No support
   intervention or filesystem surgery required.

4. **Visible without polling fatigue**. The attach state appears as a
   first-class column on `niwa session list` and a first-class field on
   `niwa_list_sessions` — coordinators see "this session is held by a
   human" without inventing new RPC surfaces or push channels.

5. **Independent shipping**. The feature does not depend on fixes to
   issues #108, #109, #111, or #112. It interoperates with PR #115's
   mesh-reliability work via additive schema fields rather than colliding
   with it.

## User Stories

The primary persona is the **workspace coordinator** (a human running niwa
locally, driving multi-step delegation through the mesh). The
coordinator's mental model is: "I dispatch agents, they run, I watch for
progress, I intervene when something looks wrong."

1. **As a workspace coordinator running a long-form mesh task**, I want
   to step into the session, see what the agent has done so far, and
   prompt it interactively for a course correction, so that I can recover
   from edge cases without destroying state.

2. **As a workspace coordinator pair-debugging a stuck worker**, I want to
   discover the session by ID and attach to it, so that I can run a few
   exploratory `git status` and tool calls in the same conversation
   thread the agent has been in.

3. **As a workspace coordinator with an SSH session that disconnected
   mid-attach**, I want a single command that breaks the stale lock so
   the mesh can resume, so that I don't have to manually delete lock
   files or restart the daemon.

4. **As a workspace coordinator who attempts to attach to a session that
   was destroyed**, I want a clear refusal with an explanation, so that
   I understand the worktree is gone and can take a different action
   instead of guessing why the command failed.

5. **As a workspace coordinator who runs `niwa session list` while a
   colleague is attached to one of the sessions**, I want to see the
   attach state in the listing, so that I can avoid running mesh
   delegations that would queue indefinitely.

## Requirements

### Functional

**R1.** `niwa session attach <session_id>` shall acquire an exclusive
lock on the named session, terminate the session's per-worktree daemon,
validate the transcript is loadable, and exec `claude --resume
<conversation_id>` with the worker's working directory.

**R2.** `niwa session attach` shall reject the operation if the session's
`status` is anything other than `active`. The error shall name the
current status and explain that attach is only valid for active sessions.

**R3.** `niwa session attach` shall reject the operation if a non-stale
attach lock is already held by another process. The error shall name
the holder's PID and start time, and shall reference the
`niwa session detach <id> --force` recovery command.

**R4.** `niwa session attach` shall pre-flight validate the transcript
file before launching Claude Code, with three distinct error messages
for: (a) no `claude_conversation_id` was ever captured, (b) the
transcript file is missing at the deterministic path, (c) the
transcript file exists but is zero bytes.

**R5.** `niwa session attach` shall hold the lock for the lifetime of
the launched Claude Code process. When Claude Code exits, the lock
shall be released, the per-worktree daemon shall be respawned, and any
envelopes that queued in the inbox during attach shall be processed by
the respawned daemon's catch-up replay.

**R6.** `niwa session attach --force` shall, if a worker is currently
running in the session, send SIGTERM to the worker's process group,
wait for the existing destroy grace period (default 5 seconds,
configurable via `NIWA_DESTROY_GRACE_SECONDS`), and SIGKILL if still
alive. Without `--force`, attach shall poll until any running worker
reaches a terminal state before proceeding.

**R7.** `niwa session attach` shall propagate Claude Code's exit code,
capped at 125 (reserves 126 / 127 / 128+ for shell semantics). On
internal niwa-side errors before exec, exit codes 1 (validation
failure), 2 (usage error), or 3 (lock contention) shall be used.

**R8.** `niwa session detach <session_id>` shall release the attach
lock if it is held by a dead process (auto-recovery for clean stale
locks; no `--force` required).

**R9.** `niwa session detach <session_id> --force` shall release the
attach lock unconditionally, including by sending SIGTERM to a live
holder process if necessary.

**R10.** `niwa session detach` (without a session_id) shall print a
usage error explaining that detach is an explicit operator command and
that normal attach release happens automatically when Claude Code exits.

**R11.** `SessionLifecycleState` shall gain a nested `attach` sub-object
with the shape `{ owner_pid, owner_start_time, started_at, lock_path }`,
populated when an attach lock is held. Mirrors PR #115's `daemon`
sub-object pattern. No `V` bump (additive field under existing V:1
readers, matching PR #115's precedent).

**R12.** `niwa_list_sessions` MCP tool shall include the `attach`
sub-object in its return shape, computed at query time from the on-disk
sentinel file. The sub-object shall be omitted (not null) when no lock
is held.

**R13.** `niwa_destroy_session` MCP tool shall return a new
`SESSION_ATTACHED` error code when destroy is attempted on an attached
session and `force` is not set. The error message shall reference the
`niwa session detach --force` command.

**R14.** `niwa session list` (no flags) shall default to the lifecycle
view (current behavior with `--status` or `--repo` flags). The
deprecated `mesh list` alias path shall be removed; users wanting the
coordinator process registry shall call `niwa mesh list` directly.

**R15.** `niwa session list` shall add an `AVAILABILITY` column with
values `available`, `attached`, `stale`. The column appears between
`STATUS` and `CREATED`.

**R16.** `niwa session list` shall support `--attached` and
`--available` filter flags, AND-combined with the existing `--repo`
and `--status` filters.

**R17.** `niwa session list` default sort order shall be: attached
sessions first (descending by attach start time), then by status
(active before terminal), then by creation time descending.

**R18.** Lock acquisition shall use a non-blocking exclusive `flock`
on `<worktree>/.niwa/attach.lock` with implicit release via
file-descriptor lifetime. A sibling sentinel JSON file at
`<worktree>/.niwa/attach.state` shall record `{ owner_pid,
owner_start_time, started_at }` for visibility.

**R19.** Stale-lock detection shall use the existing `IsPIDAlive`
helper (PID + start_time defends against PID recycling). A lock whose
owner PID is dead shall be treated as released by readers (`niwa session
list`, `niwa session attach`, `niwa session detach`) and may be
reaped opportunistically.

**R20.** Worktree state on detach: if the worktree has uncommitted
changes, untracked files, or unpushed commits on the session branch,
detach shall print a `git status` summary as a warning to stderr but
shall not abort, prompt, or auto-stash. Mirrors the existing
`branch_warning` precedent on `niwa_destroy_session`.

### Non-Functional

**R21.** Attach acquire-to-launch latency: the time from
`niwa session attach <id>` to Claude Code's first prompt shall be
under 5 seconds in the happy path on a typical developer machine.
Includes daemon-terminate grace period.

**R22.** No new external dependencies. Lock primitive uses `syscall.Flock`
(already in the codebase). Liveness uses `IsPIDAlive` (already in the
codebase). Daemon orchestration uses `TerminateDaemon` and
`EnsureDaemonRunning` (already in the codebase).

**R23.** No schema-version bump on `SessionLifecycleState`. Additive
field only, with zero-value handling matching the contributor note in
`docs/guides/sessions.md`. Coordinated with PR #115 to land both
features under V:1 cleanly.

**R24.** Single-user-per-instance assumption is declared by reference
to `DESIGN-cross-session-communication.md`'s "same-UID cooperative
trust" boundary. No new safeguards are added; existing 0600 file
permissions enforce the boundary structurally.

**R25.** Linux-only requirements: the lock primitive uses `syscall.Flock`,
which is portable. The PID liveness check (`IsPIDAlive` start-time
verification) reads `/proc/<pid>/stat`, which is Linux-only. On
non-Linux platforms, attach degrades to the conservative
"alive-if-signal-0-succeeds" check (existing fallback in
`internal/mcp/liveness.go:31-33`).

## Acceptance Criteria

### Happy Path

- [ ] AC1. `niwa session attach <id>` on a healthy active session
  acquires the lock, terminates the daemon, validates the transcript,
  and launches Claude Code with the worker's transcript visible.
- [ ] AC2. The launched Claude Code's first interactive prompt shows
  the worker's last message (transcript was successfully resumed).
- [ ] AC3. After exiting Claude Code (Ctrl-D, `/exit`, or normal
  completion), the lock is released, the daemon is respawned, and
  `niwa session list` shows `AVAILABILITY=available` for the session.
- [ ] AC4. After detach, `niwa_delegate(session_id=<id>, ...)` succeeds
  and the daemon picks up the new envelope (catch-up replay handles
  any envelopes queued during attach).

### Pre-Attach Validation

- [ ] AC5. `niwa session attach <id>` against a non-existent session ID
  exits non-zero with stderr `niwa: error: session <id> not found`.
- [ ] AC6. `niwa session attach <id>` against an `ended` session exits
  non-zero with a message stating the session is ended and the worktree
  is gone, suggesting the user create a new session.
- [ ] AC7. `niwa session attach <id>` against a session whose
  `claude_conversation_id` is empty (first task crashed before MCP
  registration) exits non-zero with case-A error message.
- [ ] AC8. `niwa session attach <id>` against a session whose
  transcript file is missing exits non-zero with case-B error message
  including the expected path.
- [ ] AC9. `niwa session attach <id>` against a session whose
  transcript file is zero bytes exits non-zero with case-C error
  message including the path.

### Lock Contention

- [ ] AC10. A second `niwa session attach <id>` while the first is
  still attached exits non-zero with stderr identifying the holder's
  PID, the start time, and the `niwa session detach --force` recovery
  command.
- [ ] AC11. `niwa session attach <id>` after the previous attach died
  cleanly (process gone, lock released by kernel) succeeds without
  manual intervention.
- [ ] AC12. `niwa session attach <id>` when the previous holder PID is
  dead but the sentinel file remains succeeds, treating the stale
  sentinel as released.

### Force Behaviour

- [ ] AC13. `niwa session attach <id> --force` against a session with a
  running worker SIGTERMs the worker, waits the destroy grace period,
  and proceeds to acquire the lock.
- [ ] AC14. `niwa session attach <id>` (without --force) against a
  session with a running worker waits for the worker to complete
  naturally before acquiring the lock. Stderr shows
  `niwa: waiting for worker on task <task_id>...` periodically.
- [ ] AC15. `niwa session detach <id>` against a session with a dead
  attach holder releases the lock and exits 0, no `--force` required.
- [ ] AC16. `niwa session detach <id> --force` against a session with a
  live attach holder sends SIGTERM to the holder, waits, and releases
  the lock.

### Discovery Surface

- [ ] AC17. `niwa session list` (no flags) shows the lifecycle view
  (NOT the deprecated `mesh list` fallback).
- [ ] AC18. `niwa session list` includes the `AVAILABILITY` column
  between `STATUS` and `CREATED` with values `available`, `attached`,
  or `stale`.
- [ ] AC19. `niwa session list --attached` filters to only sessions
  currently held by an attach lock.
- [ ] AC20. `niwa session list --available` filters to only sessions
  not currently held.
- [ ] AC21. `niwa session list` default sort places attached sessions
  first (descending by attach start time), then sorts by status
  (active first), then by creation time descending.
- [ ] AC22. `niwa_list_sessions` MCP tool returns `attach` sub-object
  with `owner_pid`, `owner_start_time`, `started_at`, `lock_path` when
  a lock is held; omits the field when no lock is held.

### Destroy Interaction

- [ ] AC23. `niwa_destroy_session` (or `niwa session destroy <id>`)
  against an attached session without `--force` returns the new
  `SESSION_ATTACHED` error code with a message referencing the
  `niwa session detach --force` command.
- [ ] AC24. `niwa session destroy <id> --force` against an attached
  session sends SIGTERM to the attach holder, waits, and proceeds with
  destruction (kills daemon, removes worktree, marks ended).

### Worktree State on Detach

- [ ] AC25. Detach with a clean worktree exits 0 silently.
- [ ] AC26. Detach with uncommitted edits prints a `git status`
  summary to stderr labelled `warning: worktree has uncommitted
  changes`, then exits with the propagated Claude exit code (does not
  abort).
- [ ] AC27. Detach with unpushed commits on the session branch prints
  the same style of warning as `branch_warning` does on
  `niwa_destroy_session`.

### Multi-User Boundary

- [ ] AC28. `niwa session attach <id>` invoked by a different UID than
  the workspace owner fails fast with a UID-mismatch error before
  attempting any state read (the existing 0600 file permissions
  produce an `EACCES` that niwa wraps in a friendly message).

### Documentation

- [ ] AC29. `docs/guides/sessions.md` is updated with a "Human-in-the-Loop:
  Attaching to a Session" section documenting `niwa session attach`,
  `niwa session detach`, the `AVAILABILITY` column, scenario
  walkthroughs (happy path, pair-debug, force-on-running-worker,
  ended-session reject, concurrent-attach reject, terminal-crash
  recovery), and the failure modes with their exact error messages.

### Test Coverage

- [ ] AC30. A `@critical` Gherkin scenario in
  `test/functional/features/` exercises the attach → detach → mesh
  resume golden path end-to-end against a real `niwa` binary.
- [ ] AC31. Unit tests cover lock acquisition, stale-lock detection,
  pre-flight validation (all three error cases), and the
  `AVAILABILITY` column rendering in `niwa session list`.

## Out of Scope

The following are deliberately excluded from this PRD. Each has either
a deferred plan, a separate issue, or a structural reason it doesn't
fit.

- **Cross-workspace-instance session discovery.** `niwa session list`
  stays scoped to the current instance per issue #117 lock-in. A
  follow-up may add `--instance <name>` if a user need emerges.
- **Multi-user shared-machine semantics.** Single-UID is the declared
  trust model. Multi-user support would require permission redesign,
  socket UID checks, and ownership coordination — out of scope for v1.
- **Transcript editing or splicing.** The operator attaches and
  continues the conversation. Surgically editing prior turns is not
  supported.
- **Programmatic / MCP-based attach.** No `niwa_attach_session` MCP
  tool. Attach is a human-driven CLI feature; programmatic attach
  would require a different concurrency model and is not justified by
  any current request.
- **Heartbeat protocol for SSH-disconnect-with-survivor detection.**
  v1 ships with SIGHUP-handler-only auto-release plus
  `niwa session detach --force` as the operator escape hatch. A
  heartbeat would be the only new pattern in the codebase; not
  justified for the rare nohup-style hostile-detach case.
- **Forensic attach to ended sessions.** `niwa_destroy_session` runs
  `git worktree remove --force` and deletes the session branch — both
  the working directory and the ref `claude --resume` would need are
  destroyed. Forensic inspection of an ended session means reading
  its `<sid>.json` state file, not attaching.
- **`abandoned` session attach semantics.** No code path produces
  `abandoned` today. Defer attach behaviour for `abandoned` until a
  writer for the state is added.
- **Coordinator push notification on attach state change.** The
  filesystem-visible state file is sufficient via polling. No new
  notification channel is added.
- **Fixing related issues #108, #109, #111, or #112.** These are
  addressed in PR #115 (`docs/niwa-mesh-reliability`). This PRD's
  schema additions coordinate with PR #115's `daemon` sub-object
  pattern but do not depend on it landing first.
- **Plugin or Claude-Code-config inheritance for the attached
  session.** Attach exec's the user's plain `claude` binary; the
  attached session inherits the user's normal Claude Code
  configuration (full plugins, full tool palette). Workers' stripped-
  down config is not propagated. This is the right default for
  pair-debug and recovery use cases.

## Open Questions

None remain that block PRD acceptance. The exploration that produced this
PRD resolved all 7 open questions from the source issue (#117) plus the
follow-on questions surfaced during research:

- State-model field shape: nested `attach` sub-object (decided per R11).
- Schema versioning: no V bump (decided per R23, per PR #115 precedent).
- Lock primitive: `flock` + sentinel JSON (decided per R18).
- Stale-lock recovery: `IsPIDAlive` + `--force` escape (decided per R19, R8, R9).
- Pre-attach validation states: `active` only (decided per R2).
- Worktree state on detach: warn loudly, don't auto-clean (decided per R20).
- Multi-user boundary: declared by reference (decided per R24).
- Transcript persistence: pre-flight stat for UX, never `--continue`
  (decided per R4).
- Coordinator awareness: polling, no push channel (decided per R12).
- Discovery UX: AVAILABILITY column orthogonal to STATUS, attached-first
  sort (decided per R15, R17).
- MCP surface: additive `attach` sub-object + `SESSION_ATTACHED` error
  (decided per R12, R13).
- Force semantics: explicitly different on attach vs detach (per Decisions section).

The remaining open question (claude-version-drift sensitivity to the
`s/[^A-Za-z0-9]/-/g` path encoding) is a known fragility, not a blocker:
the PRD already requires (R4) that niwa tolerate `claude --resume` exit
1 even when the pre-flight check passes. If a future Claude Code release
changes the path scheme, attach degrades to "always defer to claude's
own error" rather than breaking outright.

## Known Limitations

1. **Demand validation is incomplete.** The exploration's
   adversarial-demand lead concluded "demand not validated" — issue
   #117 is a single-author proposal with no corroborating asks in the
   niwa repo. The maintainer's direction is to proceed; this PRD
   documents the assumption that the maintainer values this enough to
   maintain the new surface. If future telemetry shows attach is
   rarely used, the feature should be a candidate for removal.

2. **Linux-only PID-liveness verification.** The `IsPIDAlive` check
   that defends against PID recycling is Linux-only (reads
   `/proc/<pid>/stat`). On non-Linux platforms, attach falls back to
   the conservative signal-0 check, which can produce false-positive
   "alive" detections after PID recycling. Users on macOS or BSD may
   encounter "lock held by alive process" false alarms in long-lived
   workspaces. Acceptable for v1 because niwa is Linux-first today.

3. **SSH-disconnect-with-survivor relies on operator action.** If a
   user's SSH session disconnects but the niwa-attach process inherits
   no controlling terminal (rare — typically SIGHUP cascades and kills
   it), the lock stays held until the operator runs
   `niwa session detach --force`. No automatic detection. Heartbeat
   was considered and rejected as over-engineering for v1.

4. **Concurrent attach attempts reject rather than queue.** If two
   operators on the same machine try to attach to the same session
   simultaneously, the second receives an immediate
   `LOCK_HELD` error pointing at the holder. No FIFO queue. This is
   intentional — queuing would add complexity for a workflow that's
   rarely needed in the single-user model. Two-user "tmux-style
   shared session" attach is explicitly out of scope.

5. **Exit-code asymmetry between attach contexts.** When Claude Code
   exits with a non-zero code, `niwa session attach` propagates it
   capped at 125. When niwa-side validation fails before exec, codes
   1 (validation), 2 (usage), or 3 (lock contention) are used.
   Operators reading exit codes need to know which side produced the
   failure.

## Decisions and Trade-offs

The following decisions were made during exploration and locked in by
this PRD. Each entry: what was decided, what alternatives were
considered, and why the chosen option won.

### D1. Verb pair: `attach` + `detach` (not `enter`/`exit`, not `connect`/`disconnect`)

- **Decided:** `niwa session attach <id>` for normal flow,
  `niwa session detach <id> [--force]` as operator escape hatch.
- **Alternatives:** `enter`/`exit`, `connect`/`disconnect`, `take`/`release`.
- **Why:** tmux + Docker establish `attach`/`detach` as deeply
  ingrained muscle memory for the human-takeover pattern. Other verbs
  add cognitive load without payoff.

### D2. Detach is operator-only, not the normal release path

- **Decided:** Normal attach release happens automatically when Claude
  Code exits. The explicit `niwa session detach <id>` command exists
  only for stale-lock recovery.
- **Alternatives:** require explicit `niwa session detach` after every
  attach; symmetric attach/detach commands.
- **Why:** Asymmetric pairing matches user expectation (you exit
  Claude Code, you don't run a release command). Explicit detach
  exists for the case where the lock is stuck.

### D3. `--force` semantics differ between `attach` and `detach`

- **Decided:** On `attach`, `--force` SIGTERMs a running worker. On
  `detach`, `--force` steals the lock from another holder. PRD calls
  this out explicitly because the symmetry instinct misleads
  operators.
- **Alternatives:** make `--force` mean the same thing on both;
  rename one of the two flags (e.g., `--steal` on detach).
- **Why:** Each command has exactly one destructive thing it can do,
  and `--force` naming is the established niwa convention. Renaming
  would be inconsistent with `niwa session destroy --force`. The
  doc-time clarification is cheap.

### D4. State-model field shape: nested `attach` sub-object

- **Decided:** New nested `attach` sub-object on
  `SessionLifecycleState` with `{ owner_pid, owner_start_time,
  started_at, lock_path }`.
- **Alternatives:** new `Status` value (`attached`); flat
  `availability` string field; separate JSON file referenced from
  `SessionLifecycleState`.
- **Why:** Mirrors PR #115's `daemon` sub-object precedent, which
  established the parallel-axis pattern (lifecycle is `status`,
  availability/health are separate sub-objects). Overloading `Status`
  would conflate two semantics; a flat field doesn't compose with
  PR #115's shape.

### D5. No `SessionLifecycleState.V` bump

- **Decided:** Additive field under existing V:1.
- **Alternatives:** bump to V:2 per the contributor note in
  `docs/guides/sessions.md`.
- **Why:** PR #115 sets the no-bump precedent for additive sub-objects.
  Following its lead avoids contradicting an in-flight design choice.
  Zero-value handling in readers (existing pattern) absorbs the change.

### D6. Lock primitive: `flock` + sentinel JSON

- **Decided:** Non-blocking exclusive `flock(<worktree>/.niwa/attach.lock)`
  for mutual exclusion + sibling `<worktree>/.niwa/attach.state` JSON
  sentinel (atomic tmp+rename) for visibility.
- **Alternatives:** flock-only (no sentinel; visibility via flock
  probing); state-file-only (no flock; rely on PID liveness alone);
  `lock_holder` field directly on `SessionLifecycleState` JSON.
- **Why:** Direct precedent in `acquireDaemonPIDLock`. Implicit
  release via fd-lifetime is the simplest stale-recovery story. The
  sentinel exists so `niwa session list` can read holder metadata
  without flock-probing the file (which would require a separate file
  open and is more invasive than reading a small JSON).

### D7. `niwa session attach` is a long-running parent process, NOT exec-replacement

- **Decided:** niwa stays running, runs Claude Code as a child via
  `exec.Cmd`, waits, releases lock on Claude exit.
- **Alternatives:** `syscall.Exec` to replace niwa with Claude Code
  (cleaner UX, Ctrl-C goes straight to claude); wrapper-driven
  acquire/release/cd via shell helper.
- **Why:** Exec-replacement drops the flock immediately, making the
  lock useless. Wrapper-driven adds three calls and is fragile across
  shell exits. Niwa-supervised matches the daemon supervision pattern
  the codebase already uses.

### D8. Daemon coordination: terminate-and-respawn (not lock-aware-skip)

- **Decided:** `TerminateDaemon` on attach acquire,
  `EnsureDaemonRunning` on detach release. Catch-up inbox replay
  handles in-flight envelopes naturally.
- **Alternatives:** add a sentinel-file-skip in `handleInboxEvent` so
  the daemon stays alive but doesn't claim envelopes during attach;
  pause via a signal to the daemon.
- **Why:** Terminate-and-respawn requires zero changes to the
  daemon's hot path. The catch-up replay path is already exercised by
  every daemon restart. The cost is ~3-5s of acquire latency for the
  destroy-grace period; acceptable for a human-initiated command.

### D9. Pre-flight transcript validation is for UX, not safety

- **Decided:** Validate before exec, with three distinct error
  messages. Don't trust-and-let-claude-fail.
- **Alternatives:** trust claude (it already exits 1 on every failure
  mode); validate but fall back to fresh session on failure
  (silent-degrade UX).
- **Why:** Empirically `claude --resume` fails loudly so safety is not
  the issue. Pre-flight exists so niwa can emit niwa-shaped error
  messages with three actionable cases (no conv_id, missing transcript,
  empty transcript) instead of `claude`'s opaque "No conversation
  found with session ID: <uuid>" — which is user-hostile when the user
  typed a niwa session id, not a claude conv_id.

### D10. Never use `claude --continue`

- **Decided:** Always use `claude --resume <uuid>`, never `--continue`.
- **Alternatives:** `--continue` (resumes most-recent session in CWD).
- **Why:** Empirically, `--continue` silently degrades to a fresh
  session (exit 0) when invoked from a CWD with no history. This is
  the worst possible UX for attach. `--resume <uuid>` always fails
  loudly when the transcript is missing.

### D11. Path encoding: `s/[^A-Za-z0-9]/-/g`

- **Decided:** Match Claude Code's actual encoding rule (verified
  empirically on claude v2.1.138).
- **Alternatives:** `base64url(cwd)` was the round-1 hypothesis;
  empirical testing showed it was wrong.
- **Why:** Niwa must look in the directory Claude Code actually
  writes to. The encoding is a `s/[^A-Za-z0-9]/-/g` substitution
  prefixed with the leading `/` becoming a leading `-`.

### D12. AVAILABILITY column values: `available` / `attached` / `stale`

- **Decided:** Lowercase kebab-case state vocabulary matching niwa's
  existing voice.
- **Alternatives:** `free`/`attached`/`stale-lock`,
  `idle`/`attached`/`expired`.
- **Why:** Matches the existing `active`/`ended`/`abandoned` style in
  the STATUS column. `available` is unambiguous in the AVAILABILITY
  column header context. `stale` is shorter than `stale-lock` for the
  table.

### D13. Default sort: attached first, then status, then creation time descending

- **Decided:** Three-key composite sort.
- **Alternatives:** sort by ID (current behaviour, effectively
  random); creation-time-only.
- **Why:** Attach surfaces the operator's hot question ("is anyone in
  there right now?") to the top of the table. Status and
  creation-time are tiebreakers for sessions in normal availability.

### D14. `niwa session list` flagless default flips to lifecycle view

- **Decided:** Remove the deprecated `mesh list` alias path. Flagless
  `niwa session list` shows lifecycle sessions.
- **Alternatives:** keep the deprecation warning for another release.
- **Why:** The issue's UX sketch is incompatible with the deprecated
  alias being default. Attach is the right reason to flip the
  default; the deprecation has been live since
  DESIGN-mesh-session-lifecycle landed.

### D15. SSH-disconnect: SIGHUP-handler + `--force`, no heartbeat

- **Decided:** v1 relies on SIGHUP cascading to kill niwa-attach (the
  common case) plus operator-driven `niwa session detach --force` for
  the rare survivor case.
- **Alternatives:** heartbeat from niwa-attach (writes timestamp every
  N seconds; readers check freshness); TTY-poll (niwa-attach detects
  closed stdin and exits voluntarily).
- **Why:** Heartbeat would be the only new pattern in the codebase
  (niwa today uses no heartbeats). The TTY-poll case is rare. Operator
  recovery is one command. Not worth the complexity.

### D16. Exit codes: propagate Claude exit capped at 125

- **Decided:** Forward Claude Code's exit code through niwa, capped
  at 125. Reserve 126/127/128+ for shell semantics.
- **Alternatives:** always exit 0 on successful detach (regardless of
  Claude exit); use a niwa-specific code for "Claude exited
  non-zero".
- **Why:** Operators write scripts around niwa commands. Hiding
  Claude's exit code breaks that contract. Capping at 125 avoids
  collisions with shell-reserved codes.

### D17. MCP surface change is minimal and additive

- **Decided:** New `attach` sub-object on `niwa_list_sessions`
  output. New `SESSION_ATTACHED` error code from
  `niwa_destroy_session`. No changes to `niwa_delegate`, `niwa_ask`,
  `niwa_send_message`, `niwa_create_session`, `niwa_query_task`,
  `niwa_await_task`. No new MCP tools.
- **Alternatives:** add `niwa_attach_session` programmatic tool; add
  push-notification channel for state changes.
- **Why:** Filesystem-visible state suffices for coordinator polling.
  No external request emerged for programmatic attach. The
  "CLI-first" guidance was confirmed by the round-2 MCP-surface
  audit.

### D18. Demand-validation caveat surfaced as a Known Limitation

- **Decided:** Document the demand-validation finding ("not
  validated, not validated-as-absent") as a Known Limitation rather
  than blocking on stop-gate evidence.
- **Alternatives:** route to a Rejection Record artifact; require
  more demand evidence before drafting the PRD.
- **Why:** The maintainer has set direction to proceed. The risk is
  documented so future telemetry can revisit if the feature is
  rarely used.
