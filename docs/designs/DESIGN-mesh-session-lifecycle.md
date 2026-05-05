---
status: Accepted
upstream: docs/prds/PRD-mesh-session-lifecycle.md
problem: |
  Niwa's task model spawns a fresh Claude process per delegation with no shared
  conversation history, performs all work on the main clone's checked-out branch,
  and cannot address tree-structured sessions because the isKnownRole gate rejects
  any target that lacks a role directory on disk.
decision: |
  Introduce git worktree-based sessions under <instance>/.niwa/worktrees/, each
  on its own session branch, with its own per-worktree daemon and per-session JSON
  state file. Task delegation to a session writes directly to the worktree daemon's
  inbox (resolved from the state file). The handleAsk "coordinator" bug (wrong
  instanceRoot in session worktrees) is fixed via a mainInstanceRoot field on the
  server struct. The shell wrapper gains a session arm with a shared cd helper.
  Three new MCP tools and updates to the injected niwa-mesh skill complete the
  coordinator API. Parent/child ask routing is deferred to a follow-on.
rationale: |
  Per-session JSON files (one file per session, atomic temp+rename writes) match
  the PRD's file layout, require no inter-daemon locking, and leave the existing
  coordinator process registry (sessions.json) completely untouched. Direct inbox
  writes for delegate routing reuse the established lookupLiveCoordinator pattern
  with no new coordination layer. Removing children[] and the Stale and PRUrl
  fields eliminates unreliable advisory state with no current readers. Deferring
  parent/child ask routing removes a large, underspecified feature with no V1
  use case from the critical path.
---

# DESIGN: Mesh Session Lifecycle

## Status

Accepted

## Context and Problem Statement

Niwa's task infrastructure is built around a single main clone per repo and a
flat role-based routing model. Every `niwa_delegate` call spawns a fresh Claude
process against the main clone; every `niwa_ask` resolves a target by looking
up a role directory under `.niwa/roles/<role>/`. This works for stateless task
dispatch but breaks down in three ways that this design must resolve.

**No persistent Claude context across tasks.** Claude conversation history lives
in a JSONL file keyed to (CWD, session-id). When the daemon spawns a worker for
task B, it starts a fresh Claude process — the session JSONL from task A is
unreachable because the new process generates a new session ID. Coordinators
running multi-step workflows (`/shirabe:design → /shirabe:plan`) must re-state
context with every delegation.

**Main clone branch contamination.** All work happens on the main clone's checked-
out branch. After a coordinator finishes a feature branch and moves on, the repo
stays on that branch. `niwa apply` skips non-default-branch repos, so workspaces
accumulate stale checkouts with no automated recovery path.

**Role-directory routing cannot address tree-structured sessions.** The existing
`niwa_ask` handler validates the `to` field against `.niwa/roles/<to>/` on disk and
looks up the coordinator via a flat `sessions.json` registry. Virtual routing
targets — `"parent"` (resolved from a calling session's recorded parent ID) and
direct child session IDs — have no role directory, so the gate returns `UNKNOWN_ROLE`
before any routing logic runs. A session tree where child sessions can address their
parent cannot be built on top of the role-directory model without extending it.

**System boundaries affected:**
- `internal/mcp/server.go` — `handleAsk`, `isKnownRole`, `handleDelegate`
- `internal/cli/mesh_watch.go` — worker spawn path, `resumeSessionID`
- `internal/cli/shell_init.go` — shell wrapper CWD-change interception
- `internal/cli/go.go` — `niwa go` second-argument extension
- `internal/cli/session.go` — `niwa session list` name collision
- `internal/workspace/state.go` — `EnumerateInstances` (layout-solved)
- `internal/mcp/session_registry.go` — coordinator registry vs. session registry
- New: session state schema, per-worktree daemon lifecycle

## Decision Drivers

- **Layout isolation without code changes:** session worktrees must be invisible to
  `EnumerateInstances` (workspace-root scan) and `EnumerateRepos` (two-level scan).
  Placement under `<instance>/.niwa/worktrees/` satisfies this without touching
  enumeration logic.
- **Backward compatibility is non-negotiable:** `niwa_delegate` without `session_id`,
  `niwa apply`, and existing `niwa_ask(to="coordinator")` must behave identically.
- **Coordinator never handles Claude conversation IDs:** session continuity (JSONL
  path, `--resume` flag) is managed entirely by the daemon and MCP layer.
- **Reuse existing daemon lifecycle:** `EnsureDaemonRunning` in `internal/workspace/`
  is already reusable; per-worktree daemons should start via the same path.
- **Session state survives reboots:** all lifecycle state is file-based; in-memory
  state is not authoritative.
- **Public repo content governance:** no internal references in design or commit
  messages.
- **The `isKnownRole` gate is the primary architectural blocker:** any solution for
  virtual routing targets must either bypass or extend this gate cleanly.
- **`niwa_delegate` routing mechanism is the core open question:** how a coordinator's
  delegate call reaches a per-worktree daemon inbox (not the main instance daemon) is
  unspecified by the PRD and is the highest-priority design question.

## Considered Options

### Decision 1: How does `niwa_delegate(session_id=X)` route to the per-worktree daemon's inbox?

Today `handleDelegate` builds its inbox path as
`<s.instanceRoot>/.niwa/roles/<to>/inbox/` using the MCP server's own instance root.
When a coordinator's server has `s.instanceRoot` pointing at the main instance, a
session-targeted delegate lands in the main daemon's inbox — the per-worktree daemon
never receives it. Role validation (`isKnownRole`) uses the same root and fails for
roles that only exist inside the worktree.

Key assumptions:
- The per-worktree daemon is running before `niwa_delegate(session_id=X)` is called.
- The worktree path is recorded in the session state file at
  `<mainInstanceRoot>/.niwa/sessions/<session-id>.json`.
- Task store directories (`<mainInstanceRoot>/.niwa/tasks/<id>/`) remain rooted in the
  main instance; only the inbox write and role validation target the worktree.

#### Chosen: Option A — Direct Inbox Write

When `session_id` is present in `delegateArgs`, `handleDelegate` reads the session
state file to resolve `worktreePath`, then constructs the inbox path as
`<worktreePath>/.niwa/roles/<role>/inbox/` and performs role validation against
`<worktreePath>/.niwa/roles/<role>/`. The task envelope is written directly there; the
per-worktree daemon picks it up via its existing fsnotify watch. The `session_id` is
recorded in `TaskState` so `handleCancelTask` and `handleUpdateTask` can reconstruct
the correct inbox path without additional coordination.

The `session_id == ""` path is byte-for-byte identical to the current behavior.

#### Alternatives Considered

**Option B — Main Daemon as Router:** The coordinator writes to the main instance's
inbox with a `session_id` field added; the main daemon reads it and forwards the
envelope to the worktree daemon's inbox. Rejected because it adds large new routing
logic to the main daemon, creates a two-hop delivery (serialising on the main daemon's
availability), and introduces a two-write failure window with no clean recovery path.

**Option C — Shared Pending Directory:** Task envelopes for sessions are written to
`<mainInstanceRoot>/.niwa/sessions/pending/<session-id>/`; the per-worktree daemon
polls or watches that directory and claims its own envelopes. Rejected because it
requires a new watcher in the per-worktree daemon, introduces a new filesystem layout
with no precedent in the codebase, and adds an extra atomic-move hop without any
advantage over Option A.

---

### Decision 2: How does `niwa_ask` support virtual routing targets given that `isKnownRole` validates against role directories on disk?

`handleAsk` has two sequential gates: `isKnownRole` (stat `.niwa/roles/<to>/`) and
`lookupLiveCoordinator`. Virtual targets (`"parent"`, `"coordinator"` from a worktree,
child session IDs) have no role directory, so gate 1 fires `UNKNOWN_ROLE` before any
routing logic runs. Gate 2, if reached, reads the wrong registry in session worktrees
(`s.instanceRoot` points to the worktree, not the main instance).

Key assumptions (V1 coordinator-fix scope):
- `NIWA_MAIN_INSTANCE_ROOT` is propagated to session-worktree daemons at spawn,
  separate from `NIWA_INSTANCE_ROOT` (the worktree path).
- The MCP server reads `NIWA_MAIN_INSTANCE_ROOT` at startup into `s.mainInstanceRoot`.
- Non-session workers have `s.mainInstanceRoot == ""`, which disables the guard.

#### Chosen: Option A — Registry pre-check before `isKnownRole` (V1 scope: coordinator fix only)

V1 implements only the `"coordinator"` case. `handleAsk` gains a guard at the top:
if `args.To == "coordinator"` and `s.mainInstanceRoot != ""` (i.e., the server is
running inside a session worktree), it uses `s.mainInstanceRoot` instead of
`s.instanceRoot` for the coordinator lookup. This fixes the B3a/B3b bug identified
in the panel review. All other values fall through to `isKnownRole` unchanged.

The MCP server struct gains one new field: `mainInstanceRoot string`. It is
zero-valued for non-session workers, which preserves existing behavior.
`NIWA_MAIN_INSTANCE_ROOT` propagates this value to the per-worktree daemon at spawn.

**Deferred to follow-on:** `"parent"` routing (child session asks its creating
session's daemon) and direct child session-ID routing. These require a concrete V1
use case, session tree state to be stable, and `s.sessionID` on the server struct.
They are not exercised by any V1 coordinator workflow.

#### Alternatives Considered

**Option B — Virtual role directories via symlinks or stubs:** At session creation
time, create stub directories for virtual target names under the worktree's
`.niwa/roles/`. Rejected because `isKnownRole` is a presence check, not a liveness
check; stale parent symlinks silently drop messages rather than returning an error.
Stubs require maintenance when children are created after the parent, and there is no
hook to add them. Dynamic session topology cannot be mapped onto a static directory
tree without diverging from reality.

**Option C — Parallel handler `handleAskTree`:** A separate handler for session
contexts with no changes to `handleAsk`. Rejected because both handlers need the same
shared logic (task creation, await registration, timeout loop). The branching point
moves one level up (function dispatch) instead of one level in (top of `handleAsk`),
at the cost of duplicating everything else. Option A's inline guard approach is
strictly less code than a parallel handler.

---

### Decision 3: What is the session lifecycle state schema — file layout, data model, and writer/reader contract?

Two registries coexist in `<instance>/.niwa/sessions/`: the existing coordinator
process registry (`sessions.json`, written by `WriteSessionEntry`) and the new
per-session lifecycle state (this decision). They must not interfere under concurrent
writes. The PRD fixes the file layout as per-session files at
`<instance>/.niwa/sessions/<session-id>.json`.

Key assumptions:
- The filesystem is shared across all worktree daemon instances.
- Session files survive host reboots; no in-memory cache is authoritative.
- Terminal states (`ended`, `abandoned`) are written once and never updated.

**Cross-validation note — per-field writer ownership:** D3 initially assumed the
session owner (coordinator daemon) is the sole writer for a session file. PRD R11
requires the per-worktree daemon to write `ClaudeConversationID` after the first
worker completes. These writes are temporally non-overlapping: the coordinator writes
on create; the per-worktree daemon writes `ClaudeConversationID` exactly once after
the first task reaches the `running` state; the coordinator writes terminal state on
destroy. Atomic temp+rename is sufficient — no concurrent writes to the same file
occur in the normal path.

#### Chosen: Option A — Per-session JSON files

Each session gets its own `<session-id>.json` file at
`<instance>/.niwa/sessions/<session-id>.json`. The owning daemon is the only writer
for lifecycle state fields; the per-worktree daemon writes `ClaudeConversationID`
exactly once. All writes use temp file + rename for atomicity.

`niwa_list_sessions` scans the directory, filters to names matching
`^[0-9a-f]{8}\.json$` (distinguishing them from `sessions.json`), reads each file,
and calls `IsPIDAlive` for a live stale check. Corrupt files are logged and skipped.

```go
type SessionLifecycleState struct {
    V                    int    `json:"v"`
    SessionID            string `json:"session_id"`
    ParentSessionID      string `json:"parent_session_id,omitempty"`
    Repo                 string `json:"repo"`
    Purpose              string `json:"purpose"`
    Status               string `json:"status"`
    CreationTime         string `json:"creation_time"`
    WorktreePath         string `json:"worktree_path"`
    ClaudeConversationID string `json:"claude_conversation_id,omitempty"`
    CreatorPID           int    `json:"creator_pid"`
    CreatorStartTime     int64  `json:"creator_start_time"`
}
```

Fields removed vs. earlier drafts: `Children []string` (advisory, incomplete on crashes
and concurrent creates; tree reconstruction uses `parent_session_id` only), `Stale bool`
(readers always call `IsPIDAlive`; a stored hint with no authoritative value adds noise),
`PRUrl string` (reserved for follow-on; no writer or reader in V1).

Terminal state files are retained after worktree deletion to enable post-mortem
inspection and clean coordinator-restart recovery.

#### Alternatives Considered

**Option B — Single `mesh-sessions.json`:** All sessions in one file. Rejected because
concurrent writes from independent worktree daemons require a shared file lock
(serialising all session updates) or last-writer-wins semantics (which clobbers
independent concurrent updates). A single corrupt entry makes all sessions invisible.

**Option C — Extend `sessions.json`:** Add a sessions map to the coordinator registry
file. Rejected because it conflates two unrelated concepts (process liveness vs.
session lifecycle), corrupts the hot path (`niwa_ask` coordinator lookup), and
requires `WriteSessionEntry` to suppress semantically-wrong `ErrAlreadyRegistered`
errors for lifecycle entries.

---

### Decision 4: How does the shell wrapper support `niwa session create` CWD navigation?

The shell wrapper in `internal/cli/shell_init.go` intercepts `$1 in (create|go)`,
runs the binary with `NIWA_RESPONSE_FILE` pointing at a temp file, and `cd`s to the
path it contains. `niwa session create` has `$1 == "session"`, which doesn't match.
`niwa go <repo> <session-id>` has `$1 == "go"`, which already matches.

Key assumptions:
- Only `niwa session create` requires CWD navigation among session subcommands.
- The tsuku recipe distributes the updated wrapper; users don't need to reinstall.

#### Chosen: Option A — Intercept `session` at `$1`, dispatch `$2`

The wrapper match pattern extends to `create|go|session`. The new `session)` arm
checks `$2`: if `create`, runs with `NIWA_RESPONSE_FILE` and cd; all other
subcommands (`destroy`, `list`, `tree`) fall through to `command niwa "$@"`. The
`create|go` arm is unchanged. `niwa go <repo> <session-id>` needs no wrapper change —
the path resolution for the second positional arg moves entirely to the Go binary.

#### Alternatives Considered

**Option B — Hidden alias `niwa session-create`:** A hidden cobra command equivalent
to `niwa session create`, intercepted by the wrapper as `session-create`. Rejected
because it splits one user command into two cobra entries and adds internal exec
complexity with no benefit over Option A.

**Option C — Universal response-file interception:** Always pass `NIWA_RESPONSE_FILE`
to every invocation; commands that don't navigate write nothing. Rejected because it
adds `mktemp` overhead to every call (including high-frequency tab-completion probes)
and contradicts the existing test contract that the default arm has zero temp-file
overhead.

## Decision Outcome

The four decisions compose into a single coherent architecture: worktree-isolated
sessions on dedicated branches, file-based lifecycle state, direct-inbox task
delivery, a targeted `handleAsk` fix for the coordinator routing bug, and selective
shell wrapper extension with a shared cd helper.

**How they connect:**

Session creation (`niwa_create_session`) creates a git worktree on a new
`session/<session-id>` branch, scaffolds the daemon's directory layout, and writes a
`SessionLifecycleState` file (D3) containing the worktree path, repo, parent session
ID, and creator PID. It starts the per-worktree daemon via `EnsureDaemonRunning`,
passing `NIWA_MAIN_INSTANCE_ROOT` as an environment variable so the daemon's MCP
server can fix the `handleAsk` coordinator routing (D2).

Task delegation (`niwa_delegate(session_id=X)`) reads the session state file to
resolve the worktree path (D3) and writes the task envelope directly to the worktree
daemon's inbox (D1). The per-worktree daemon picks it up via its existing fsnotify
watch with no new coordination logic. `handleDelegate` checks `session.Status ==
"active"` before any filesystem operation and returns `SESSION_INACTIVE` otherwise.

The `handleAsk("coordinator")` bug fix (D2) ensures that workers running inside
session worktrees reach the main instance coordinator correctly. All other
`niwa_ask` targets in V1 continue to use role-directory routing.

Shell navigation (`niwa session create`) is handled by extending the wrapper's match
pattern (D4). A shared `__niwa_cd_wrap` shell helper deduplicates the temp-file
protocol across `create`, `go`, and `session create`.

**Deferred:** `niwa_ask("parent")` and `niwa_ask("<session-id>")` routing — no V1
use case. `niwa session tree` — deferred to follow-on. `children[]`, `Stale`, and
`PRUrl` fields — removed from schema.

**Backward compatibility:** every path without a `session_id` argument is unchanged.
`isKnownRole` and `lookupLiveCoordinator` are not modified. `sessions.json` is
completely isolated from the new session lifecycle code.

## Solution Architecture

### Overview

A session is a git worktree under `<instance>/.niwa/worktrees/<repo>-<session-id>/`
with its own niwa daemon watching `<worktreePath>/.niwa/roles/<repo>/inbox/`. Session
lifecycle state is stored in `<instance>/.niwa/sessions/<session-id>.json`. The
coordinator creates and destroys sessions; it routes tasks to them via direct inbox
writes; sessions communicate upward and downward via virtual-target ask routing.

### Components

**Session state registry** (`internal/mcp/session_lifecycle.go` — new file):
- `SessionLifecycleState` struct and `WriteSessionLifecycleState` (atomic write)
- `ReadSessionLifecycleState(mainInstanceRoot, sessionID string)`
- `newSessionLifecycleID()` — 8 lowercase hex characters, distinct from UUID format
- `ListSessionLifecycleStates(sessionsDir string)` — ReadDir + filter + liveness check

**MCP handlers** (`internal/mcp/server.go`, `internal/mcp/handlers_task.go`):
- `niwa_create_session`: creates worktree, writes session state file, starts daemon
- `niwa_destroy_session`: writes terminal state, stops daemon, removes worktree
- `niwa_list_sessions`: calls `ListSessionLifecycleStates`, formats response
- `handleDelegate` extension: when `SessionID != ""`, reads session state to derive
  worktree inbox path; records `SessionID` in `TaskState`
- `handleAsk` extension: `handleAskVirtual` pre-check for virtual targets

**MCP server struct** (`internal/mcp/server.go`):
- New field: `mainInstanceRoot string`
- Zero-valued for non-session workers (disables the coordinator routing fix)
- Read from `NIWA_MAIN_INSTANCE_ROOT` env var at server startup

**Per-worktree daemon** (`internal/workspace/daemon.go`):
- `EnsureDaemonRunning` gains a new `extraEnv []string` parameter:
  `func EnsureDaemonRunning(instanceRoot string, extraEnv []string) error`
- `niwa_create_session` calls it with `["NIWA_MAIN_INSTANCE_ROOT=<main>"]`
- The daemon bakes `NIWA_MAIN_INSTANCE_ROOT` into every worker spawn via
  `WorkerMCPConfig`, so workers inside the session worktree read it at startup
- All existing `EnsureDaemonRunning` call sites pass `nil` (no behavior change)
- **`ClaudeConversationID` capture:** when the first task transitions to `running`,
  `state.json.Worker.ClaudeSessionID` contains the Claude Code session ID written by
  `registerSessionID()` in the worker's MCP server at startup. The daemon checks this
  field in the `running` transition handler; if the session lifecycle state file has
  no `claude_conversation_id` set, it writes the value atomically (temp+rename). This
  is a one-time write; subsequent tasks reuse the captured ID via `--resume`.

**Shell wrapper** (`internal/cli/shell_init.go`):
- A shared `__niwa_cd_wrap` shell helper is extracted into the template to
  deduplicate the temp-file/cd protocol used by `create`, `go`, and `session create`
- `shellWrapperTemplate` gains a `session)` arm dispatching on `$2`; `session create`
  calls `__niwa_cd_wrap`; all other session subcommands fall through to `command niwa`
- `niwa session create` handler calls `writeLandingPath(worktreePath)` and
  `hintShellInit(cmd)` identically to `niwa create` and `niwa go`

**CLI commands** (`internal/cli/session.go`):
- `niwa session create <repo> <purpose>` — new command
- `niwa session destroy <session-id>` — new command
- `niwa session list [--repo <repo>] [--status <status>]` — new command
- Existing `niwa session list` (coordinator process view) renamed to `niwa mesh list`;
  the old `niwa session list` prints a deprecation warning and delegates to `niwa mesh
  list` for one release to give existing scripts a migration path
- `niwa session tree` — deferred to follow-on

**Injected niwa-mesh skill** (`internal/workspace/channels.go`):
- `niwaMCPToolNames` gains three entries: `niwa_create_session`,
  `niwa_destroy_session`, `niwa_list_sessions` (emitted in both the SKILL.md
  `allowed-tools` block and the `## Channels` section of `workspace-context.md`)
- `buildSkillContent()` gains a **Session Management** section documenting the
  session create/destroy/list lifecycle, the `session_id` argument for
  `niwa_delegate`, and the virtual routing targets for `niwa_ask` (`"parent"`,
  child session IDs)
- The Delegation section is updated: `niwa_delegate` description mentions the
  optional `session_id` parameter for routing to a specific session worktree
- The Peer Interaction section is updated: `niwa_ask` description mentions
  `"parent"` and child-session-ID routing targets available inside sessions

### Key Interfaces

**Session state file** at `<instance>/.niwa/sessions/<session-id>.json`:
```
SessionID, ParentSessionID, Repo, Purpose, Status, CreationTime,
WorktreePath, ClaudeConversationID, CreatorPID, CreatorStartTime
```
Writers: coordinator daemon (all fields except `ClaudeConversationID`); per-worktree
daemon (`ClaudeConversationID` only, written exactly once on first task `running`
transition). Both use atomic temp+rename.

`CreatorPID` and `CreatorStartTime` are used for `IsPIDAlive` liveness checks. There
is no stored `InboxPath` or `Stale` field; both are computed at runtime.

**Inbox path derivation** (D2 → D3 integration):
```
<session.WorktreePath>/.niwa/roles/<session.Repo>/inbox/
```
Used by `handleDelegate` (D1) and `handleAskVirtual` (D2) to target the per-worktree
daemon's inbox. Before using this path as a write target, the caller must validate
that `WorktreePath` is a subpath of `mainInstanceRoot` (see Security Considerations).

**Environment variables propagated at daemon spawn:**
- `NIWA_MAIN_INSTANCE_ROOT` — main instance root for session-worktree daemons

**TaskState extension** (for `handleCancelTask`/`handleUpdateTask`):
- New field `SessionID string` — present for session-routed tasks; used to reconstruct
  the worktree inbox path without re-calling `handleDelegate`

### Data Flow

**Task delegation to a session:**
```
coordinator calls niwa_delegate(to="<role>", session_id="<sid>")
  → ReadSessionLifecycleState validates sid matches ^[0-9a-f]{8}$
  → reads <mainInstanceRoot>/.niwa/sessions/<sid>.json
  → if session.Status != "active" → returns SESSION_INACTIVE
  → validates WorktreePath is subpath of mainInstanceRoot
  → validates role at <worktreePath>/.niwa/roles/<role>/
  → writes task envelope to <worktreePath>/.niwa/roles/<role>/inbox/<taskID>.json
  → per-worktree daemon's fsnotify fires → daemon claims envelope → spawns worker
  → worker reads task via niwa_check_messages
```

**handleAsk coordinator routing fix (V1):**
```
session worker calls niwa_ask(to="coordinator", ...)
  → handleAsk: if args.To == "coordinator" and s.mainInstanceRoot != ""
      → uses s.mainInstanceRoot for lookupLiveCoordinator (not s.instanceRoot)
  → all other targets fall through to isKnownRole as today
```

**Session creation:**
```
coordinator calls niwa_create_session(repo="<role-name>", purpose="<purpose>")
  → validates repo exists at mainInstanceRoot/.niwa/roles/<repo>/
    (fails early before any filesystem side effects)
  → generates 8-hex session ID; checks for collision with existing .json files;
    retries on collision
  → git worktree add <worktreePath> -b session/<session-id>
    (branches from the repo's default branch, not the main clone's current branch;
     branch is owned by this session and deleted on destroy if no PR exists)
  → scaffolds <worktreePath>/.niwa/roles/<repo>/inbox/{in-progress,cancelled,expired,read}/
    and <worktreePath>/.niwa/tasks/, .niwa/daemon.pid, .niwa/daemon.log
  → writes <sid>.json with status="active", repo, worktree_path, purpose,
    parent_session_id (if child session), creator_pid, creator_start_time,
    creation_time
  → on any failure after git worktree add: git worktree remove --force <worktreePath>
    before returning the error; prevents orphaned worktrees with no state file
  → EnsureDaemonRunning(worktreePath, ["NIWA_MAIN_INSTANCE_ROOT=<main>"])
  → returns session ID and worktree path
```

**`repo` parameter contract:** the `repo` argument is a role name — the same
identifier used in `.niwa/roles/<repo>/` — not a directory basename or git remote
URL. When `[channels.mesh.roles]` overrides map a repo to a different role name,
callers must pass the role name (the mapped value).

**Session destroy:**
```
coordinator calls niwa_destroy_session(session_id="<sid>")
  → reads <mainInstanceRoot>/.niwa/sessions/<sid>.json
  → if session.Status in {ended, abandoned}:
      returns current terminal state with success (idempotent)
  → if any task in <mainInstanceRoot>/.niwa/tasks/ has this session's worktree
    in its inbox (status=running): force-kills the worker PGID, writes the task
    state as abandoned with reason "session_destroyed"
  → writes <sid>.json with status="ended"
  → TerminateDaemon(worktreePath) — sends SIGKILL to remaining workers, SIGTERM to daemon
  → git worktree remove --force <worktreePath>
  → if branch session/<session-id> has no PR and is fully merged: git branch -d session/<session-id>
  → worktree directory is gone; state file is retained for post-mortem inspection
  → if WorktreePath is already gone (orphaned session): skip worktree remove silently;
    write terminal state and return success
```

## Implementation Approach

### Phase 1: Session state schema and registry

Deliverables:
- `internal/mcp/session_lifecycle.go` — `SessionLifecycleState` struct,
  `WriteSessionLifecycleState`, `ReadSessionLifecycleState`, `ListSessionLifecycleStates`,
  `newSessionLifecycleID`
- Unit tests for write/read round-trip, ID collision retry, concurrent write safety,
  stale detection via `IsPIDAlive`

This phase has no external dependencies and can be reviewed in isolation.

### Phase 2: Worktree lifecycle and daemon startup

Deliverables:
- `EnsureDaemonRunning` signature extension: `func EnsureDaemonRunning(instanceRoot string, extraEnv []string) error`
  — extra env vars forwarded to daemon process and baked into `WorkerMCPConfig`;
  all existing call sites pass `nil` (verify with `grep -r EnsureDaemonRunning`)
- Worktree `.niwa/` scaffold helper — creates `roles/<repo>/inbox/` subdirs and
  `tasks/`, `daemon.pid`, `daemon.log` (lighter than full `InstallChannelInfrastructure`;
  no `mcp.json`, no `workspace-context.md`)
- `niwa_create_session` MCP handler — role validation, branch creation from default
  branch, worktree creation, scaffold, state file write, rollback via
  `git worktree remove --force` on any post-worktree failure, daemon start with
  `NIWA_MAIN_INSTANCE_ROOT`
- `niwa_destroy_session` MCP handler — idempotent on terminal state; force-kills
  running workers (task → abandoned + "session_destroyed"), writes "ended" state,
  terminates daemon, removes worktree (silently ignores ENOENT), deletes session
  branch if unmerged and no PR
- `ClaudeConversationID` capture in per-worktree daemon: on first task `running`
  transition, reads `state.json.Worker.ClaudeSessionID`; if session state file lacks
  `claude_conversation_id`, writes it atomically
- Functional tests: create → verify state file, session branch, daemon running,
  scaffold dirs; destroy → idempotent, orphaned worktree silently handled

Depends on Phase 1 (session state functions).

### Phase 3: `niwa_delegate` session routing

Deliverables:
- `SessionID string` field added to `delegateArgs` and `TaskState`
- `handleDelegate` extension — session state lookup, worktree inbox derivation
- `handleCancelTask` / `handleUpdateTask` extension — reconstruct inbox path from
  `TaskState.SessionID`
- Unit tests for session-routed delegate, cancel, update

Depends on Phase 1 (state read), Phase 2 (daemon running before delegate is called).

### Phase 4: `handleAsk` coordinator routing fix

Deliverables:
- `mainInstanceRoot string` field added to MCP server struct; read from
  `NIWA_MAIN_INSTANCE_ROOT` at server startup
- `handleAsk` guard: `if args.To == "coordinator" && s.mainInstanceRoot != ""` →
  use `s.mainInstanceRoot` for `lookupLiveCoordinator`
- Unit tests: coordinator ask from session worktree routes to main instance;
  coordinator ask from non-session context unchanged

Depends on Phase 2 (env var propagated to daemon → workers).

### Phase 5: CLI commands, shell wrapper, and skill update

Deliverables:
- `niwa session create`, `niwa session destroy`, `niwa session list` cobra commands
  in `internal/cli/session.go`
- `niwa session list` (coordinator process view) renamed to `niwa mesh list` in
  `internal/cli/mesh.go`; old `niwa session list` kept as a deprecated alias for one
  release (prints deprecation warning to stderr, delegates to `niwa mesh list`)
- Functional tests that currently reference `niwa session list` updated to
  `niwa mesh list` (audit: `test/functional/features/mesh.feature`,
  `docs/guides/cross-session-communication.md`)
- `__niwa_cd_wrap` shared shell helper extracted in `shellWrapperTemplate`;
  `create`, `go`, and `session create` all call it
- `niwa go` second-argument resolution for session worktree paths
- `niwa_list_sessions` MCP handler wired to `ListSessionLifecycleStates`
- `niwaMCPToolNames` extended with `niwa_create_session`, `niwa_destroy_session`,
  `niwa_list_sessions`
- `buildSkillContent()` updated: new Session Management section; Delegation section
  updated for `session_id` arg
- `channels_test.go` frontmatter byte-count assertion updated for new tool names
- Functional tests: `niwa session create` navigates shell, `niwa go <repo> <sid>` navigates

Depends on Phases 1–4.

## Security Considerations

**Worktree isolation:** Session worktrees inherit the main clone's git configuration
and credentials. A session with write access to the git remote can push branches. This
is expected behavior — sessions are trusted processes created by the coordinator. No
additional access controls are introduced.

**Session ID space:** Session IDs are 8 lowercase hex characters (32 bits of entropy).
At the expected scale (< 20 sessions per instance), collisions are negligible. The ID
generator checks for an existing file before returning and retries on collision. IDs
are not secret — they appear in worktree paths and session state filenames. No
security property depends on ID unpredictability.

**Session ID path validation:** Caller-supplied session IDs used in
`ReadSessionLifecycleState` (called by both `handleDelegate` and `handleAskVirtual`)
must be validated against `^[0-9a-f]{8}$` inside that function before the ID is used
to construct a file path. This validation must live in `ReadSessionLifecycleState`
itself, not at each call site, so it applies uniformly. A coordinator passing a
path-separator-containing value (`../`) could otherwise construct inbox paths outside
the sessions directory.

**Session state file permissions:** `WriteSessionLifecycleState` creates files with
mode `0o600` (owner read/write only). This matches the existing `sessions.json`
permissions and prevents other local users from reading session metadata (worktree
paths, purposes, conversation IDs).

**WorktreePath and Repo origin trust:** Inbox paths are derived from `WorktreePath`
and `Repo` fields read from session state files. Before using a derived path as a
write target, `handleDelegate` and `handleAskVirtual` must validate that
`WorktreePath` is a subpath of `mainInstanceRoot`. This guards against state file
corruption or coordinator bugs producing inbox writes outside the workspace.

**Env var propagation:** `NIWA_MAIN_INSTANCE_ROOT` and `NIWA_SESSION_ID` are added to
daemon process environments. Both values are filesystem paths or identifiers visible
in process listings on the local machine. No credentials or secrets are added.

**Inbox write authorization:** `handleDelegate` validates the session ID against the
session registry before writing to the worktree inbox. An invalid or tampered
`session_id` returns `SESSION_NOT_FOUND` before any file write. Role validation
against the worktree path prevents writing to a non-existent role directory.

**Stale parent/child routing:** `handleAskVirtual` calls `IsPIDAlive` before routing
to a parent or child session. A stale session returns an error rather than delivering
to an unmonitored inbox. Messages delivered to a dead daemon's inbox are not consumed
and remain on disk — they don't represent a security issue but do represent orphaned
state that a future `niwa session gc` command can clean.

**No network services:** all inter-daemon communication uses shared filesystem paths
(inbox directories). No network sockets or ports are opened. The attack surface is
limited to local filesystem access, which is already the threat model for the existing
niwa daemon.

## Consequences

### Positive

- Persistent Claude context across tasks within a session. The daemon resumes the same
  JSONL file for every worker in a session via `--resume <ClaudeConversationID>`.
- Main clone branch isolation. Session work happens on dedicated `session/<id>`
  branches; the main clone is never left on a feature branch by a session.
- File-based state survives reboots. Coordinator restarts, daemon crashes, and host
  reboots do not lose session state. Terminal-state files enable post-mortem inspection.
- Backward compatibility is complete. Every existing call site — `niwa_delegate`
  without `session_id`, `niwa_ask(to="coordinator")`, `niwa apply`, `EnumerateRepos`
  — is unchanged.
- `sessions.json` is untouched. The coordinator process registry, its stale-pruning
  semantics, and `lookupLiveCoordinator` are not modified.
- `handleAsk("coordinator")` bug from session worktrees (B3a/B3b) is fixed as a
  natural consequence of the `mainInstanceRoot` field.

### Negative

- `niwa_list_sessions` reads N files per call. At typical session counts (< 20) this
  is negligible, but the implementation must not assume a fixed upper bound.
- `handleAsk` gains a guard at the top of the function, adding a small amount of
  complexity for the coordinator routing fix.
- `ClaudeConversationID` is written by the per-worktree daemon after the first task
  reaches `running`; it is absent in the session state file immediately after
  creation.
- `niwa session list` rename to `niwa mesh list` is a breaking change. Mitigated by a
  one-release deprecation alias.
- Parent/child ask routing is deferred; coordinators cannot have session workers ask
  upward until a follow-on ships.

### Mitigations

- **N-file reads:** `ListSessionLifecycleStates` skips corrupt files individually. A
  cached index can be added without changing the file format if session count grows.
- **`handleAsk` guard:** the guard is a single `if` at the top of `handleAsk` with no
  new code paths for non-session workers. Cognitive load is minimal.
- **`ClaudeConversationID` timing:** `niwa_list_sessions` marks the field optional.
  Callers that need it check for emptiness.
- **Rename deprecation:** the old `niwa session list` emits a deprecation warning to
  stderr and delegates to `niwa mesh list`, giving existing scripts one release to
  migrate.
