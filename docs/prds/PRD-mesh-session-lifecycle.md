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

4. Sessions form a tree rooted at the coordinator. The parent-child relationship is
   established at creation time and defines the only valid communication channel
   between sessions: a session can address its parent or any of its direct children,
   and no other sessions.

5. Coordinators can end sessions safely. Niwa prevents cleanup of sessions with
   unpushed commits or active children, and gives the coordinator explicit control
   over when to cascade teardown through a subtree.

6. Users can inspect the session tree, navigate to any session's worktree, query a
   session's agent directly, and prune subtrees safely from the CLI.

7. Non-mesh developers can use `niwa session start` to create an isolated worktree
   for their work without needing a coordinator.

## Folder Layout

Session worktrees live inside the main instance's `.niwa/` metadata directory,
isolating them from the workspace-root enumeration that `niwa apply` and
`EnumerateInstances` use to discover niwa instances. The main clone stays on
`main` at its existing path; nothing moves.

```
<workspace>/
  <instance-name>/                        # niwa instance root
    .niwa/
      instance.json                       # instance state (existing)
      sessions/
        sessions.json                     # coordinator registry (existing)
        <session-id>.json                 # per-session lifecycle state (new)
      worktrees/                          # session worktrees (new)
        <repo>-<session-id>/              # git worktree = session instance root
          .git                            # file pointer to main clone's objects
          .niwa/                          # session's own niwa instance (new)
            instance.json                 # includes session_worktree marker
            daemon.pid
            roles/
              <repo-role>/
                inbox/
            tasks/
          <working tree files>            # feature branch checkout
    repos/
      <repo-name>/                        # main clone, always on main
        .git/                             # full git directory
        <source files>
```

Placement rationale:

- `<instance>/.niwa/worktrees/` is inside the instance directory, not at the
  workspace root. `EnumerateInstances` scans only immediate subdirectories of
  the workspace root for `.niwa/instance.json`; session worktrees nested three
  levels inside an instance's `.niwa/` are never discovered as standalone
  instances.
- The two-level `EnumerateRepos` scan (`instanceRoot` → groups → repos) does
  not reach `<instanceRoot>/.niwa/worktrees/<repo>-<session-id>/`, which is
  three levels from `instanceRoot`. Session worktrees are invisible to `niwa
  apply`, `niwa go -r` completion, and repo status enumeration without any
  additional code changes.
- Per-session lifecycle state lives alongside `sessions.json` under
  `<instance>/.niwa/sessions/`, keeping all coordinator-visible session data
  under a single directory.

## Session Tree Model

Sessions form a tree. Understanding the tree is prerequisite to understanding
session lifecycle, routing, and cleanup.

### Structure

Every session has exactly one parent, established at creation time and never
changed. The parent is the session that called `niwa_create_session`; if the
call comes from outside any session (the top-level coordinator or a CLI user),
the session is a root session with no parent. Root sessions are direct children
of the workspace.

```
workspace
└── coordinator (root session, no parent)
    ├── tsuku-a3f7c2d1  (child of coordinator)
    │   └── koto-b7e91f04  (grandchild; child of tsuku-a3f7c2d1)
    └── shirabe-c2d45a88  (child of coordinator)
```

### Communication

A session may only communicate with its direct parent or its direct children.
There is no routing between siblings, cousins, or arbitrary sessions. This
keeps the communication graph identical to the tree structure and makes session
ownership unambiguous: a session is responsible for everything it creates.

Two routing targets are valid in `niwa_ask`:
- `"parent"` — routes to the calling session's direct parent.
- `<session-id>` — routes to a named direct child of the calling session.
- `"coordinator"` — preserved as a shortcut to the root session from any
  depth, for backward compatibility and for cases where a deeply nested session
  needs to reach the top without knowing the full chain.

Routing to any other target is rejected.

### Lifecycle dependencies

A session cannot be ended cleanly while it has active or pending-merge
descendants. Ending a non-leaf session requires either ending all descendants
first (bottom-up), or using `--force`, which cascades termination through the
subtree deepest-first before removing the target.

An orphaned session (its creating coordinator process has died) surfaces with a
stale marker. The subtree beneath it is preserved unchanged; no automatic
cleanup occurs.

### Inspection

The session tree is the primary view for understanding workspace state. Users
can view the tree, navigate to any session's worktree, and query a session's
agent directly from the CLI without entering its worktree or interrupting its
work.

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

**As a coordinator, I want sessions I create to automatically become my children in
the session tree**, so that the communication graph matches the delegation structure
without me having to track routing manually.

**As a session agent, I want to ask my parent session a question and receive an
answer**, so that I can surface decisions upward without the top-level coordinator
needing to orchestrate every exchange.

**As a coordinator, I want to end a session that has active child sessions to be
blocked**, so that I cannot accidentally orphan in-progress work by cleaning up
a parent before its children are done.

**As a user, I want to view the full session tree for a workspace**, so that I can
see what work is in flight, how sessions relate to each other, and which ones are
stale or blocked.

**As a user, I want to ask a running session's agent a question from the CLI**,
so that I can inspect progress or give direction without navigating to the
session's worktree or interrupting its active task.

**As a user, I want to force-end a session subtree in one command**, so that I can
clean up abandoned or completed work without having to end each child individually.

**As a developer using niwa without a coordinator, I want to start a session for a
repo from the CLI**, so that my main clone stays on `main` while I work on a feature
in a clean, isolated worktree.

## Requirements

### Session management (mesh)

**R1.** `niwa_create_session` is a new MCP tool that accepts a repo identifier and a
purpose string. It creates a new session, provisions a git worktree for the session,
starts a per-worktree daemon, and returns a `session_id`. The calling session becomes
the parent of the new session; the parent-child binding is recorded at creation time
and never changes. When called from outside any session (e.g., the top-level
coordinator), `parent_session_id` is null and the new session is a root session.
The `session_id` is niwa's internal session handle (see R22); it is distinct from
the Claude conversation ID used by `--resume`. No Claude process is started at
session creation time. The coordinator uses `session_id` to route subsequent
`niwa_delegate` calls into this session; niwa manages the Claude conversation ID
internally and the coordinator never handles it directly.

**R2.** `niwa_delegate` accepts an optional `session_id` parameter. When provided, the
worker is spawned within the session's worktree. If the session has a recorded Claude
session ID from a prior worker, the new worker resumes that session. If not, the
first worker in the session starts a fresh Claude session.

**R3.** `niwa_list_sessions` is a new MCP tool that returns all sessions for the
current workspace instance. Each entry includes: session ID, parent session ID (null
for root sessions), direct child session IDs, repo, purpose, status (one of
`active`, `pending_merge`, `ended`, `abandoned`), creation time, and — for sessions
whose creating process is no longer running — a stale indicator showing the previous
process ID.

**R4.** `niwa_end_session` is a new MCP tool that accepts a `session_id` and an optional
`force` flag (default false). Without `force`, it blocks when either (a) the session
worktree contains commits not reachable from any remote-tracking branch, returning
`{status: "blocked_by_unpushed_work"}`, or (b) the session has any active or
pending-merge descendants, returning `{status: "blocked_by_active_children",
children: [<immediate child session IDs>]}`. Neither condition removes the worktree.

**R5.** A session with all commits pushed and no active descendants can be ended
cleanly via `niwa_end_session` without `force`. The worktree is removed and the
session transitions to `ended`. With `force: true`, niwa terminates all descendant
sessions bottom-up (deepest leaves first, each subject to the same worktree removal
as a force-end on that individual session), then removes the target session's
worktree and returns `{status: "abandoned"}`.

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

**R11.** The Claude conversation ID is captured automatically by the first worker that
runs in a session. When Claude Code spawns, it sets `CLAUDE_SESSION_ID` in its
environment; the worker's MCP server reads this value at startup and writes it to
`state.json`. After the first worker finishes, the daemon reads the captured value
and writes it to the session's state file. All subsequent `niwa_delegate` calls
within the same session spawn the worker with `claude --resume <claude-conversation-id>`,
using this stored value. The coordinator never handles the Claude conversation ID
directly; niwa manages it as an internal implementation detail of session
continuity.

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

**R16.** `niwa session start <repo> [--purpose <text>]` creates a session worktree
at `<instance>/.niwa/worktrees/<repo>-<session-id>/`, starts the per-worktree
daemon, prints the session ID and absolute worktree path to stdout, and leaves the
caller's working directory unchanged.

**R17.** `niwa session end <session-ref> [--force]` ends the session matched by
`<session-ref>` (session ID, purpose prefix, or unambiguous repo name per R25).
When the reference matches multiple sessions the command lists them and exits
non-zero without ending any session. Without `--force`, exits non-zero and prints a
warning when the session worktree contains commits not reachable from any
remote-tracking branch; with `--force`, removes the worktree and exits zero.

**R18.** `niwa session list [--repo <repo>] [--status <status>]` lists all sessions
for the current workspace instance. Each row shows: session ID, repo, purpose
(truncated to 60 characters), status, creation time, and a `[stale]` marker when
the owning coordinator PID is no longer alive. `--repo` filters to sessions for
that repo; `--status` filters to a specific lifecycle state.

**R26.** `niwa session go <session-ref>` navigates the shell to the session worktree
root. It uses the same integration mechanism as `niwa go` (the shell function
installed by `niwa setup`), so it changes the shell's working directory. When
`<session-ref>` matches multiple sessions the command prints a disambiguation list
and exits non-zero.

**R27.** Tab completion for `niwa session go`, `niwa session end`, `niwa session ask`,
and all CLI commands that accept a `<session-ref>` includes all active and
pending-merge sessions in the current workspace, showing session ID, repo name, and
purpose prefix. Completion narrows on repo name, session ID prefix, or purpose prefix.

**R29.** `niwa session tree [<session-ref>]` prints the session tree as an indented
hierarchy. Each line shows: session ID, repo, purpose (truncated to 50 characters),
status, and a `[stale]` marker when the creating process is no longer alive. Without
a session-ref, shows the full workspace session tree. With a session-ref, shows the
subtree rooted at that session. Leaf sessions with no children are distinguishable
from non-leaf sessions in the output.

**R30.** `niwa session ask <session-ref> "<question>"` routes a question from the
user to the specified session's active worker and waits for the response. The session
need not be idle; the question is delivered as an inbox message and the command
blocks until the worker responds via `niwa_finish_task` or equivalent. The response
is printed to stdout. If the session has no active worker the command exits non-zero
with a message indicating the session is idle.

**R31.** `niwa session end` with a session-ref that resolves to a non-leaf session
prints the full subtree of active and pending-merge descendants alongside the
unpushed-work warning (if applicable) before blocking. `--force` on a non-leaf
session cascades termination bottom-up through the subtree, printing each terminated
session as it completes.

### Session tree operations (mesh)

**R28.** `niwa_get_session_tree` is a new MCP tool that returns the session tree for
the current workspace, rooted at the workspace. Each node includes: session ID,
parent session ID, direct child session IDs, repo, purpose, status, and creation
time. An optional `root_session_id` parameter returns only the subtree rooted at
that session.

### Cross-instance routing

**R19.** Session-worktree workers route messages via the session tree. `niwa_ask`
accepts three valid targets:

- `"parent"` — routes to the calling session's direct parent, resolved by reading
  `parent_session_id` from the calling session's state file.
- `<session-id>` — routes to a named session that is a direct child of the calling
  session. Routing to a session that is not a direct child returns an error.
- `"coordinator"` — routes to the root coordinator session regardless of tree depth,
  preserved for backward compatibility and for cases where deep traversal is not
  needed.

Any other routing target is rejected. The parent-child binding established at
`niwa_create_session` is the sole source of truth for routing authorization.

### Naming and identification

**R20.** Session IDs are unique within a workspace. No two sessions in the same
workspace share the same ID.

**R21.** Purpose strings are limited to 256 UTF-8 characters. Requests with a
longer purpose string are rejected with an error before the session is created.

**R22.** Session IDs are generated by niwa as 8 lowercase hex characters (e.g.,
`a3f7c2d1`). They are the canonical identifier for a session across all surfaces:
MCP tool responses, CLI output, and the filesystem path. Coordinators receive the
generated ID in the `niwa_create_session` response; CLI users see IDs in `niwa
session list` output and in the path printed by `niwa session start`.

**R23.** The git worktree directory for a session is named `<repo>-<session-id>`
(e.g., `myrepo-a3f7c2d1`) and placed at
`<instance>/.niwa/worktrees/<repo>-<session-id>/`. The name encodes the source repo
without requiring a registry lookup.

**R24.** The purpose string is the sole human-readable label for a session at the
MCP level. Coordinators identify sessions by `session_id`; the purpose is advisory
context. Niwa does not expose a separate "session name" concept at the MCP layer.

**R25.** CLI commands accept a `<session-ref>` that resolves as follows, tried in
order: exact session ID match, then single session whose purpose begins with the
given string, then — when exactly one session exists for the given repo in the
current workspace — the repo name alone. When a `<session-ref>` matches multiple
sessions the command prints the matching sessions and exits non-zero without
performing any action.

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

### Session tree structure

- [ ] `niwa_create_session` called from within session S records S's ID as
      `parent_session_id` in the new session's state. `niwa_list_sessions` returns
      the new session with `parent_session_id` equal to S's ID.
- [ ] `niwa_create_session` called from the top-level coordinator (outside any
      session) records `parent_session_id` as null. The new session appears as a
      root session in `niwa_list_sessions`.
- [ ] `niwa_get_session_tree` returns a tree where coordinator is the root and
      child sessions appear nested under their parent. Two siblings created by the
      same parent appear at the same depth with no parent-child relationship between
      them.
- [ ] `niwa session tree` prints the same tree structure as an indented hierarchy,
      showing session ID, repo, purpose, and status per line.

### Session cleanup

- [ ] `niwa_end_session(id)` on a session with commits not pushed to any remote
      returns `{status: "blocked_by_unpushed_work"}` and leaves the worktree intact.
- [ ] `niwa_end_session(id)` on a session with at least one active child returns
      `{status: "blocked_by_active_children"}` and lists the immediate child session
      IDs. The worktree is not removed.
- [ ] `niwa_end_session(id)` on a session with both active children and unpushed
      commits returns `blocked_by_active_children` (children take precedence).
- [ ] `niwa_end_session(id, force=true)` on a session with two active children
      terminates both children before removing the target. After the call, neither
      child worktree exists on disk. The target worktree is also removed.
- [ ] `niwa_end_session(id)` on a session where all commits are pushed and there
      are no active descendants removes the worktree and returns `{status: "ended"}`.
- [ ] After any `niwa_end_session`, the main clone remains on `main` with no
      modifications.

### Session tree routing

- [ ] Worker in session S1 (direct child of coordinator) calls
      `niwa_ask(to="parent")`. The coordinator receives the ask notification in
      its inbox and answers via `niwa_finish_task`. S1's worker receives the answer.
- [ ] Worker in session S2 (child of S1, grandchild of coordinator) calls
      `niwa_ask(to="parent")`. S1's active worker receives the ask. S2 does not
      receive any notification to the coordinator directly.
- [ ] Worker in session S1 calls `niwa_ask(to=<S2-session-id>)` where S2 is a
      direct child of S1. S2's worker receives the ask.
- [ ] Worker in session S1 calls `niwa_ask(to=<session-id>)` where that session is
      a sibling of S1 (not a parent or child). The call returns an error; no message
      is delivered.
- [ ] Worker in session S2 (grandchild of coordinator) calls
      `niwa_ask(to="coordinator")`. The coordinator receives the ask. S1 (the
      intermediate parent) receives no notification.

### Session tree inspection (CLI)

- [ ] `niwa session tree` with a three-session tree (coordinator → tsuku-foo →
      koto-bar) prints koto-bar indented under tsuku-foo, which is indented under
      coordinator.
- [ ] `niwa session tree <tsuku-foo-id>` shows only tsuku-foo and koto-bar; the
      coordinator is not shown.
- [ ] `niwa session ask <session-ref> "<question>"` delivers the question to the
      named session's active worker and prints the worker's response to stdout.
- [ ] `niwa session end <parent-session-ref>` when the parent has an active child
      prints the child session IDs before blocking and exits non-zero.
- [ ] `niwa session end --force <parent-session-ref>` when the parent has one active
      child terminates the child first, then removes the parent's worktree, and
      prints both terminated sessions to stdout.

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

### Folder layout

- [ ] After `niwa session start myrepo`, the session worktree exists at
      `<instance>/.niwa/worktrees/myrepo-<session-id>/`.
- [ ] `niwa apply` run at the workspace root after session creation does not touch
      the session worktree directory. The worktree directory does not appear in
      apply's progress output.
- [ ] `niwa go -r` tab completion does not include the session worktree as a
      candidate repo destination.

### Naming and identification

- [ ] `niwa session start myrepo` prints a line containing the generated 8-hex-char
      session ID and the absolute worktree path.
- [ ] `niwa session list` shows the session ID, repo name, and purpose in each row.
- [ ] `niwa session end a3f7c2d1` (exact session ID) ends the correct session.
- [ ] `niwa session end "feature"` where exactly one session has a purpose beginning
      with "feature" ends that session.
- [ ] `niwa session end myrepo` where exactly one session exists for myrepo ends that
      session without requiring the session ID.
- [ ] `niwa session end myrepo` where two sessions exist for myrepo prints both
      sessions and exits non-zero without ending either.

### Non-mesh CLI

- [ ] `niwa session start <repo>` from a workspace root creates a worktree at
      `<instance>/.niwa/worktrees/<repo>-<session-id>/` and prints the session ID
      and absolute path to stdout.
- [ ] `niwa session list` shows all sessions in the current workspace. `niwa session
      list --repo <repo>` shows only sessions for that repo. `niwa session list
      --status active` shows only active sessions.
- [ ] `niwa session end` on a session with unpushed work exits non-zero and prints
      a warning; the worktree is not removed.
- [ ] `niwa session end --force` on a session with unpushed work removes the worktree
      and exits zero.
- [ ] `niwa session go <session-id>` changes the shell's working directory to the
      session worktree root (via the shell function installed by `niwa setup`).
- [ ] `niwa session go myrepo` where exactly one session exists for myrepo navigates
      to that session's worktree without requiring the session ID.
- [ ] `niwa session go myrepo` where two sessions exist for myrepo prints both
      sessions and exits non-zero without changing the working directory.

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

- **Non-adjacent session routing**: `niwa_ask` between sessions that are not in a
  direct parent-child relationship (siblings, cousins, skip-level). Sessions may
  only address their direct parent or direct children.
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

**Tree model for session relationships, not a flat registry.** Sessions form a parent-
child tree rather than a flat list with arbitrary routing. Alternative: allow any session
to message any other session (full mesh). Rejected because a full mesh gives no clear
ownership or cleanup ordering, makes routing authorization ambiguous, and produces
workspace states where sessions can become tangled without a clear path to untangle
them. The tree model gives every session exactly one owner (its parent), a clear cleanup
order (leaves before parents), and a bounded communication graph (parent and direct
children only). The coordinator shortcut (`"coordinator"` routing target) is preserved
for backward compatibility and for cases where deep traversal is not useful.

## Open Questions

None at this time. The session tree model (Session Tree Model section) resolves the
routing identity question raised during design: parent-child bindings established at
creation time define the communication graph, `"parent"` is the upward routing target,
and direct child session IDs are the downward routing targets. Remaining open items
(skip-level routing, session handoff, PR lifecycle) are captured in Out of Scope.
