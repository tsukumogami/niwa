---
status: Draft
problem: |
  Coordinators running multi-step workflows—delegating design, then planning, then
  implementation to the same repo agent—lose Claude context between each delegation
  because every niwa_delegate call spawns a fresh session. The second agent starts
  blind, cannot reference what the first agent produced, and forces the coordinator
  to repeat context. Separately, each repo's main clone gets stranded on a feature
  branch when work switches focus, because niwa apply only pulls repos that are
  clean and on their default branch. There is no automated path back to main, so
  workspaces drift over time.
goals: |
  Coordinators can group related task delegations into a named session that preserves
  Claude context from one task to the next. Each session is anchored to its own git
  worktree, keeping the main clone permanently on main. A coordinator can create,
  query, and end sessions safely, with guards preventing accidental loss of unpushed
  work. Non-mesh developers can use the same session model via a CLI command to get
  an always-clean main clone without a coordinator.
---

# PRD: Mesh Session Lifecycle

## Status

Draft

## Problem Statement

When a coordinator delegates a sequence of tasks to the same repo agent—for example,
`/shirabe:design` followed by `/shirabe:plan`—each delegation spawns a fresh Claude
session. The planning agent has no access to what the design agent produced. It cannot
reference decisions made, files modified, or reasoning established during the design
step. This has caused real workflow failures where the coordinator must re-explain
context it already produced.

The underlying cause is that niwa's unit of identity is the task, not the session.
Context lives inside a single Claude session JSONL file; it does not carry across
task boundaries by default.

A second problem compounds this: each repo's main clone can only be on one branch.
When a coordinator finishes work on a feature branch and moves to another repo, the
previous repo stays on the feature branch. `niwa apply` skips repos on non-default
branches. Over time, workspaces accumulate repos stuck on stale feature branches,
and the user has no automated recovery path.

Both problems have a common solution: a session model where each active unit of work
gets its own git worktree. The main clone stays on main permanently. Active work
happens in isolated worktrees with their own Claude sessions, their own daemons, and
their own lifecycle.

## Goals

1. Coordinators can delegate multiple sequential tasks to the same repo agent within
   a session, with each worker picking up the conversation where the previous one left
   off.

2. Coordinators can run multiple independent sessions for the same repo in parallel,
   each isolated in its own worktree with no interference.

3. The main clone of every repo in a workspace always stays on `main`. All active
   work happens in session-specific worktrees.

4. Coordinators can end sessions safely. Niwa prevents cleanup of sessions with
   unpushed commits and gives the coordinator explicit control over when to discard
   work.

5. Non-mesh developers can use `niwa session start` to create an isolated worktree
   for their work without needing a coordinator.

## User Stories

**As a coordinator, I want to create a named session for a repo before delegating
multi-step work**, so that each subsequent worker in the session resumes the
conversation from where the previous one stopped, without needing me to restate
context.

**As a coordinator, I want to list my active sessions with their purpose and current
status**, so that after my own context window resets I can re-orient and continue
managing ongoing work without losing track of what's in progress.

**As a coordinator, I want to end a session that has unpushed commits to fail safely**,
so that I cannot accidentally delete a worktree containing work that hasn't reached
the remote yet.

**As a coordinator, I want to run two separate feature sessions for the same repo
concurrently**, so that each feature's agent works in isolation with its own context
and worktree, and neither interferes with the other.

**As a developer using niwa without a coordinator, I want to start a session for a
repo from the CLI**, so that my main clone stays on `main` while I work on a feature
in a clean, isolated worktree.

## Requirements

### Session management (mesh)

**R1.** `niwa_create_session` is a new MCP tool that accepts a repo identifier and a
purpose string. It creates a new session, provisions a git worktree for the session,
starts a per-worktree daemon, and returns a `session_id`.

**R2.** `niwa_delegate` accepts an optional `session_id` parameter. When provided, the
worker is spawned within the session's worktree. If the session has a recorded Claude
session ID from a prior worker, the new worker resumes that session. If not, the
first worker in the session starts a fresh Claude session.

**R3.** `niwa_list_sessions` is a new MCP tool that returns all sessions for the
current workspace instance. Each entry includes: session ID, repo, purpose, status
(one of `active`, `pending_merge`, `ended`, `abandoned`), creation time, and — for
sessions whose coordinator process is no longer running — a stale indicator showing
the previous coordinator's process ID.

**R4.** `niwa_end_session` is a new MCP tool that accepts a `session_id` and an optional
`force` flag (default false). Without `force`, it blocks on sessions where the
session worktree contains commits not reachable from any remote-tracking branch, and
returns `{status: "blocked_by_unpushed_work"}` without removing the worktree. With
`force: true`, it removes the worktree regardless and returns `{status: "abandoned"}`.

**R5.** A session with all commits pushed can be ended cleanly via `niwa_end_session`
without `force`. The worktree is removed and the session transitions to `ended`.

### Session lifecycle

**R6.** Sessions have four lifecycle states: `active`, `pending_merge`, `ended`,
`abandoned`. State is persisted durably; it survives coordinator and daemon restarts.

**R7.** A coordinator can transition a session from `active` to `pending_merge` to
signal that a PR has been opened and the session is waiting for merge. The session
and its worktree are preserved in `pending_merge` state.

**R8.** When a `pending_merge` session is ready to close (PR merged or explicitly
closed), the coordinator transitions it to `ended` via `niwa_end_session`.

**R9.** A session is considered orphaned when the coordinator process that created it
is no longer running (verified by PID check) and the session remains in `active` or
`pending_merge` state. `niwa_list_sessions` surfaces orphaned sessions with a stale
indicator. Orphaned sessions are not cleaned up automatically; the new coordinator
decides what to do with them.

### Session continuity

**R10.** Within a session, each worker spawned via `niwa_delegate` resumes the Claude
conversation history from the previous worker in the same session.

**R11.** The Claude session ID from the first worker in a session is captured and
stored in session state. All subsequent workers in that session resume from it.

**R12.** If the session's Claude conversation history is missing or corrupted, niwa
falls back to spawning a fresh worker (matching current behavior) and records a
warning in session state.

### Backward compatibility

**R13.** `niwa_delegate` called without a `session_id` behaves exactly as today:
each task gets a fresh Claude session. No existing coordinator workflows are
affected.

**R14.** Workspaces without any sessions configured continue to work without change.
`niwa apply` behavior for the main clone is unchanged.

**R15.** `niwa apply` does not modify session worktrees. Session worktrees are managed
exclusively by session lifecycle (create and end). Apply operates on the main clone
only.

### Non-mesh session management

**R16.** `niwa session start <repo>` CLI command creates a worktree for the given
repo, starts the per-worktree daemon, and prints the worktree path. It accepts an
optional `--purpose` flag.

**R17.** `niwa session end [<session-id>]` CLI command ends a session with the same
safety guard as `niwa_end_session`. Without `--force`, it prints a warning and exits
non-zero if there are unpushed commits.

**R18.** `niwa session list` CLI command lists all sessions for the current workspace,
showing ID, repo, purpose, status, and creation time.

### Cross-instance routing

**R19.** Workers running in session worktrees can reach the live coordinator via
`niwa_ask`. The session model does not break the existing coordinator routing
mechanism.

### Identifiers and input constraints

**R20.** Session IDs are unique within a workspace. No two sessions in the same
workspace share the same ID.

**R21.** Purpose strings are limited to 256 UTF-8 characters. Requests with a
longer purpose string are rejected with an error before the session is created.

## Acceptance Criteria

### Session creation

- [ ] `niwa_create_session` with a valid repo and purpose returns a `session_id`
      and creates a git worktree alongside the main clone.
- [ ] A per-worktree daemon is running for the session after `niwa_create_session`.
- [ ] `niwa_list_sessions` returns the new session with `status: "active"` and the
      provided purpose string.
- [ ] Creating two sessions for the same repo succeeds; both appear in
      `niwa_list_sessions` with distinct IDs.

### Session continuity

- [ ] A coordinator delegates task A in a session; when task A's worker exits, the
      session records the Claude session ID used by that worker.
- [ ] A coordinator delegates task B in the same session; task B's worker is spawned
      with `--resume <session_claude_id>`, where `session_claude_id` is the value
      recorded after task A. The Claude session ID for task B matches the recorded
      value.
- [ ] A coordinator delegates task A in session S1 and task B in session S2 for the
      same repo; task B's worker is spawned without `--resume` (or with a different
      session ID than task A's). The two sessions do not share a Claude conversation.
- [ ] Two sessions for the same repo are simultaneously active. Each receives a
      separate delegation. Each worker operates in its own worktree directory and
      the task state entries for the two delegations are distinct with no shared
      fields.
- [ ] The session JSONL file is deleted before task B's delegation. Task B's worker
      is spawned without `--resume`, exits without error, and session state records
      a `corrupted_session` warning entry.

### Session cleanup

- [ ] `niwa_end_session(id)` on a session with commits not pushed to any remote
      returns `{status: "blocked_by_unpushed_work"}` and leaves the worktree intact.
- [ ] `niwa_end_session(id, force=true)` on a session with unpushed commits removes
      the worktree and returns `{status: "abandoned"}`.
- [ ] `niwa_end_session(id)` on a session where all commits are pushed removes the
      worktree and returns `{status: "ended"}`.
- [ ] After any `niwa_end_session`, the main clone remains on `main` with no
      modifications.

### Backward compatibility

- [ ] `niwa_delegate` called without `session_id` spawns a fresh Claude session
      exactly as before. No regression in existing workflows.
- [ ] A workspace with no sessions configured runs `niwa apply` without change.
- [ ] `niwa apply` on a workspace with active sessions does not modify the session
      worktrees.

### Crash recovery

- [ ] A coordinator process is killed while a session is `active`. A new coordinator
      process starts and calls `niwa_list_sessions`. The response includes the
      session from the previous coordinator with a stale indicator showing the
      previous PID. The session worktree is still present on disk.
- [ ] After the crashed coordinator's session appears as stale, no daemon process
      removes the worktree or transitions the session to `ended` or `abandoned`
      without an explicit call to `niwa_end_session`.

### Non-mesh CLI

- [ ] `niwa session start <repo>` from a workspace root creates a worktree and
      prints the worktree path to stdout.
- [ ] `niwa session list` shows all active sessions in the current workspace.
- [ ] `niwa session end` on a session with unpushed work exits non-zero and prints
      a warning; the worktree is not removed.
- [ ] `niwa session end --force` on a session with unpushed work removes the worktree
      and exits zero.

### Cross-instance routing

- [ ] A worker in a session worktree calls `niwa_ask`. The coordinator receives a
      `task.ask` notification in its inbox. The coordinator answers via
      `niwa_finish_task`. The worker's `niwa_ask` call returns the coordinator's
      answer as its result.

### Input validation

- [ ] `niwa_create_session` with a purpose string longer than 256 characters returns
      an error and does not create a session or worktree.
- [ ] `niwa_list_sessions` returns distinct IDs for any two sessions in the same
      workspace, including sessions created in rapid succession.

## Out of Scope

- **Session handoff between coordinators**: transferring ownership of a session to a
  new coordinator. Deferred; requires coordinator identity design.
- **Session → PR lifecycle tracking**: exposing `pr_url` in session state or surfacing
  PR status via `niwa_list_sessions`. The `pr_url` field is reserved in the schema
  but not surfaced in this version.
- **`niwa_session_summary`**: a tool for compacted coordinators to reconstruct
  session context. Deferred to follow-on.
- **Session audit history** (`niwa_session_history`): reconstructing the ordered
  task history of a session. Deferred to follow-on.
- **Automatic `pending_merge → ended` transition**: detecting PR merge via git polling
  and auto-cleaning the worktree. Deferred to follow-on.
- **Migration tooling**: tooling to convert existing workspaces with stranded feature
  branches to the session model. Deferred to follow-on.
- **Changes to the task delegation protocol**: envelope format, inbox atomics, and
  task state schema changes beyond what's needed to carry `session_id`.

## Known Limitations

- Session continuity relies on Claude Code's `--resume` mechanism and the integrity
  of the session JSONL file. If the file is corrupted or the session is too old to be
  resumed, the worker falls back to a fresh session without error recovery. Long-running
  sessions with large context windows may hit Claude's context limits.
- `niwa apply` does not propagate workspace configuration changes (CLAUDE.md, hooks,
  settings) into existing session worktrees. Session worktrees receive the configuration
  that was active when they were created. A worktree must be ended and recreated to pick
  up config updates.
- A session's worktree stays on disk until explicitly ended. In workspaces with many
  long-lived sessions, disk space usage grows proportionally.

## Decisions and Trade-offs

**Skip standalone session ID threading; design the full model.** An earlier option was
to add a `resume_session_id` field to `niwa_delegate` without worktrees, as a cheap
fix for the context-loss problem. This was rejected because it doesn't fix the
dirty-workspace problem, silently breaks under parallel sessions (two workers would
share one JSONL), and creates migration debt. The full model addresses both problems
together.

**Per-worktree daemon, not a shared multi-root daemon.** Each session worktree is its
own niwa instance with its own daemon. A shared daemon watching multiple instance roots
was considered; it was rejected because it requires new cross-instance coordination
logic that the existing daemon model doesn't support. The per-worktree model maps onto
the existing "one daemon per instance" design with no architectural change.

**Design for universal scope; implement mesh-first.** The PRD covers both mesh
(coordinator-managed sessions via MCP tools) and non-mesh (developer-managed sessions
via CLI). Non-mesh commands are already layout-compatible with worktrees; non-mesh users
have the same dirty-workspace problem. Restricting to mesh-only would require revisiting
scope and design for non-mesh later. The first implementation target is mesh.

**No implicit sessions on untagged niwa_delegate.** A delegation without a `session_id`
continues to behave exactly as today. This preserves backward compatibility and makes
the session model strictly opt-in. Implicit sessions were rejected because they would
silently change the behavior of all existing coordinator prompts.

**No auto-pruning of orphaned sessions.** When a coordinator crashes, orphaned sessions
surface in `niwa_list_sessions` with a stale marker but are not cleaned up automatically.
Auto-pruning was rejected because orphaned sessions may contain unpushed work, and silent
data loss is worse than stale entries.

**niwa apply does not touch session worktrees.** Apply operates on the main clone only.
Mixing apply semantics with session lifecycle management was rejected because it makes
worktree state harder to reason about and would require apply to distinguish main clones
from session worktrees.

## Open Questions

None. All scope coverage notes from the PRD scope document were resolved during Phase 2
research. Remaining implementation details (shared sessions registry mechanism, daemon
startup ordering, worktree path conventions) are design-level decisions for the
downstream design document.
