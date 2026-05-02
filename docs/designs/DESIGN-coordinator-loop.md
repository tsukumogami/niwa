---
status: Proposed
problem: |
  When a coordinator delegates a long-running task via niwa_delegate, the stall
  watchdog kills the worker at 15-minute intervals because nothing calls
  niwa_report_progress during deep workflow work. On each kill niwa spawns a fresh
  process, losing all in-session context, and the cycle repeats until max_restarts
  is exhausted. Separately, niwa_ask to a terminated role silently routes the
  question back to the caller's own inbox via an ephemeral spawn, with no way for
  the caller to distinguish this from a real response.
decision: |
  Three coordinated changes: (1) a stop hook script installed at the workspace
  level calls a new niwa CLI subcommand at every Claude Code turn boundary,
  resetting the stall watchdog automatically without any agent involvement; (2) a
  new niwa_register_worker_session MCP tool lets workers register their Claude
  Code session ID during bootstrap, enabling the watchdog to resume the killed
  session with an injected reminder instead of spawning fresh; (3) handleAsk
  returns a typed no_live_session status immediately when the target role has no
  live session, replacing the ephemeral-spawn fallback.
rationale: |
  Requiring individual skills or applications to call niwa_report_progress breaks
  abstraction — skills should not carry awareness of their execution context. The
  stop hook places progress heartbeating in infrastructure that fires regardless
  of what the agent is doing. Resume-with-reminder preserves in-session context
  that fresh spawn throws away, and the niwa_register_worker_session bootstrap
  step follows the same registration pattern already used for coordinator sessions.
  The no_live_session status fits the existing ask-status vocabulary without
  conflating routing failures with timeouts.
---

# DESIGN: Coordinator Loop Stall Recovery

## Status

Proposed

## Context and Problem Statement

A coordinator delegating a long-running workflow (explore+PRD+design) via
`niwa_delegate` observed three failure modes:

**Failure 1 — Stall watchdog kills workers mid-workflow.** The daemon's stall
detector fires after 900 seconds of no `niwa_report_progress` calls. Skills running
inside a delegated session — reading files, running parallel research agents, writing
outputs — don't call this tool. From the watchdog's perspective, a worker doing 15
minutes of uninterrupted useful work is indistinguishable from a hung process.

**Failure 2 — Fresh-spawn restart loses context.** niwa's current restart path spawns
a new `claude -p` process with a fixed bootstrap prompt. All in-session context is
gone. Recovery depends on whatever filesystem state the application happened to write
during the killed sessions — an application-level coincidence, not a niwa guarantee.
A worker built on any other application starts from scratch, hits the same 15-minute
window, gets killed again, and repeats until `max_restarts` is exhausted.

**Failure 3 — `niwa_ask` to a terminated role produces a confusing loopback.** When
a coordinator calls `niwa_ask` to a role with no live session, `handleAsk` falls back
to spawning an ephemeral worker. That worker's response routes back to the delegator.
The coordinator receives its own question in its inbox with no indication that routing
failed.

The shared root cause of Failures 1 and 2: the system expects workers to call
`niwa_report_progress` but provides no reliable mechanism to make this happen.
Requiring individual skills or application code to call it breaks abstraction — skills
should not know how they are invoked.

## Decision Drivers

- **Abstraction integrity**: Skills and application code must not carry awareness of
  their niwa invocation context. Progress heartbeating must be infrastructure.
- **Application-agnostic**: The fix must work for any niwa worker, not only shirabe.
- **Minimal agent footprint**: Prefer mechanisms that function without agent
  cooperation. If an agent ignores instructions, the system should still function.
- **Context preservation on recovery**: A stall kill is not a crash. Restarting from
  scratch is unnecessary when the session is resumable.
- **Resilience through layering**: Stack defenses so that if one mechanism misses,
  the next catches it.
- **Backward compatibility**: Existing sessions, tools, and coordinators continue to
  work without modification.

## Decisions Already Made

Settled during exploration — treated as constraints here, not open questions:

- Skill-level progress reporting rejected: requiring individual skills to call
  `niwa_report_progress` breaks abstraction.
- Resume over fresh spawn: on stall kill, resume the killed session with an injected
  reminder rather than spawning fresh.
- Stop hook as primary mechanism: a Claude Code stop hook fires at every turn
  boundary, providing automatic heartbeating with no agent awareness.
- Decision protocol enforcement out of scope: the delegation body's
  `decision_protocol` field has no enforcement mechanism by design.

## Considered Options

### Decision 1: Stop Hook Automation Level

The stall watchdog resets only when `last_progress.at` in `state.json` advances.
The fix is a Claude Code stop hook that fires at every turn boundary. The design
question is how the hook actually resets the watchdog: by directly updating the task
state via a CLI subcommand (no agent involvement), or by injecting a reminder string
that the agent reads and is expected to act on.

Key implementation facts: `NIWA_TASK_ID`, `NIWA_INSTANCE_ROOT`, and
`NIWA_SESSION_ROLE` are set unconditionally at worker spawn time
(`mesh_watch.go:907–909`) and inherited by all child processes including hook
scripts. The `mcp.UpdateState` function in `taskstore.go` enforces flock +
atomic-rename + fsync, making concurrent writes from the hook script safe alongside
the MCP server and the watchdog goroutine. The watchdog reads `last_progress.at`
under a shared flock every two seconds and resets its stall timer when the value
advances — so any writer going through `UpdateState` is immediately visible.

#### Chosen: Option A — Fully Automated via New CLI Subcommand

A new `niwa mesh report-progress --task-id $NIWA_TASK_ID` CLI subcommand updates
`state.json` directly via `mcp.UpdateState`, resetting `last_progress.at` at every
turn boundary without any agent involvement. The hook script produces no stdout on
success, so Claude Code injects nothing into the conversation and the agent sees
nothing. On error (invalid task ID, terminal task), the script exits silently — a
terminal task returns `ErrAlreadyTerminal` from `UpdateState` and is treated as
a no-op.

The subcommand uses a lighter authorization model than the MCP server: it reads
`state.json` directly and verifies that `NIWA_SESSION_ROLE` matches
`state.json.worker.role` and `NIWA_TASK_ID` matches `state.json.task_id`. The MCP
`kindExecutor` check (PPID chain + `start_time` cross-validation) cannot be
satisfied from a hook script because the hook is a child of the Claude Code harness,
not the worker process itself. The env vars `NIWA_TASK_ID`, `NIWA_SESSION_ROLE`, and
`NIWA_INSTANCE_ROOT` are set by the daemon unconditionally at spawn time and inherited
by all child processes; the subcommand treats them as its trust anchor rather than
walking the process tree.

The stop hook is configured in `workspace.toml` under `[hooks]`. niwa's merge
semantics concatenate hooks at every config layer — shirabe's existing stop hook and
niwa's new hook both appear in the `settings.json` array and both execute. There is
no conflict or override risk.

#### Alternatives Considered

**Option B — Reminder injection**: Hook outputs a reminder string; Claude Code
injects it as a user message; the agent is expected to call `niwa_report_progress`.
Rejected because it violates abstraction integrity — agent compliance cannot be
guaranteed by construction, and an agent focused on long-running tool calls may defer
or ignore the reminder.

**Option C — Hybrid (automated with fallback reminder)**: Attempts the CLI call and
falls back to a reminder on failure. Rejected because the fallback reintroduces the
agent dependency Option A eliminates. On real failure, a reminder does not help the
watchdog and misleads the agent. Silent exit is the correct behavior on failure.

---

### Decision 2: Session ID Capture and Resume-with-Reminder Path

When the watchdog kills a worker, niwa currently schedules a fresh spawn. The desired
behavior is to resume the killed session (`claude --resume <session_id> -p
"<reminder>"`) when the session is recoverable, preserving full conversation context
and injecting a targeted correction. This requires three sub-decisions: how the
session ID is captured before any stall kill can occur, how the watchdog uses it,
and how the infinite-resume loop is bounded.

`claude --resume <session_id> -p "<message>"` is confirmed to work in print mode —
the bug report that prompted this design used exactly this invocation. Session files
are `.jsonl` at `~/.claude/projects/<base64url-cwd>/`. SIGTERM leaves a complete
final line; SIGKILL may leave a partial last line but `--resume` is tolerant of a
truncated trailing entry as long as preceding lines parse as valid JSON. niwa already
has `DiscoverClaudeSessionID()` in `session_discovery.go` for coordinator sessions;
workers are explicitly not registered (PRD R39/R40), so `TaskWorker` has no session
ID field and no MCP tool provides one.

#### Chosen: Option A — New MCP Tool `niwa_register_worker_session`

The worker calls `niwa_register_worker_session` once during bootstrap, before
starting real work, passing its Claude Code session ID. The daemon stores it in a new
`TaskState.Worker.ClaudeSessionID` field. This tool follows the same authorization
(`kindExecutor`) and storage (`UpdateState`) discipline as all existing worker-side
tools, and extends the bootstrap contract that already mandates `niwa_check_messages`
as the first call.

When the watchdog fires and schedules a retry via `retrySpawn`, the path checks
`Worker.ClaudeSessionID`. If present and `Worker.ResumeCount < MaxResumes`, the
daemon runs a lightweight session file integrity check: the `.jsonl` must exist,
be non-empty, and have at least one complete JSON line. If the check passes, the
daemon calls `spawnWorker` with `resumeMode=true`, building the command as
`claude --resume <session_id> -p "<reminder>" --permission-mode=<mode>
--mcp-config=<path> --strict-mcp-config --allowed-tools <tools>`. `Worker.ResumeCount`
is incremented; `RestartCount` is not. If the session ID is absent, the file is
missing, or the integrity check fails, the daemon falls back to fresh spawn unchanged.

The infinite-resume loop is capped by a new `TaskState.Worker.ResumeCount` field and
a `TaskState.MaxResumes` field (default 2). When `ResumeCount >= MaxResumes`, the
next watchdog firing triggers a fresh spawn, resetting `ResumeCount` to 0 and
incrementing `RestartCount`. This keeps the existing `restart_count`/`max_restarts`
semantics visible to coordinators via `niwa_await_task` unchanged; the resume
sub-cycle is an implementation detail of the daemon restart path.

If `RestartCount >= MaxRestarts` when a watchdog fires, the resume path is not entered
regardless of `ResumeCount`. The task proceeds to the existing max_restarts exhaustion
handling (abandon with terminal status). This prevents a situation where resume
attempts continue after the task's outer retry budget is spent.

The reminder message injected on resume:

> You were stopped by the stall watchdog because niwa_report_progress was not called
> within the required interval. The workspace stop hook handles watchdog resets
> automatically at every turn boundary — you do not need to call niwa_report_progress
> manually for heartbeating. Call niwa_report_progress now if you want to record a
> meaningful status message, then continue your work.

The bootstrap prompt changes from:

> You are a worker for niwa task %s. Call niwa_check_messages to retrieve your task
> envelope.

To:

> You are a worker for niwa task %s. Call niwa_register_worker_session with your task
> ID and your Claude session ID first, then call niwa_check_messages to retrieve your
> task envelope. The workspace stop hook resets the stall watchdog automatically —
> you do not need to call niwa_report_progress manually.

The worker discovers its session ID from `$CLAUDE_SESSION_ID` (set by Claude Code in
the worker's environment) or via `DiscoverClaudeSessionID`'s PPID-walk and
project-scan tiers if the env var is absent. If all three tiers fail, the worker logs
a warning and continues without calling `niwa_register_worker_session` — degraded mode
where resume capability is absent but the task otherwise runs normally, falling back to
fresh spawn on stall kill. `$CLAUDE_SESSION_ID` availability in worker environments
is the expected common case; the fallback tiers exist to handle version skew.

#### Alternatives Considered

**Option B — Extend `niwa_report_progress` to accept optional session ID on first call**:
Rejected because it couples a correctness-critical registration event to a
best-effort behavioral call. Workers that never call `niwa_report_progress` before
the first watchdog firing — exactly the failure mode resume is designed to recover
from — would have no session ID registered.

**Option C — Post-mortem discovery from `~/.claude/projects/`**: After the watchdog
kills the worker, scan by mtime for the most recent session file. Rejected for three
reasons: the worker's CWD is computed at spawn time but not persisted in `TaskState`,
requiring a re-derivation that could fail; mtime scanning is racy when other Claude
Code processes run in the same project directory; and post-mortem discovery adds
latency to the time-critical restart path.

---

### Decision 3: `niwa_ask` No-Live-Session Error Format

When `handleAsk` finds no live session for the target role, the current fallback
spawns an ephemeral worker whose response routes back to the caller — a self-message
loop with no typed signal. The fix is to return an immediate typed response that
callers can detect and act on.

Three response mechanisms were considered: a new status value in the existing `textResult`
vocabulary, reuse of `timeout` with a `reason` field, or a protocol-level error
(`IsError: true`). The design must fit the existing ask-status vocabulary used by
coordinators in their `niwa_await_task` re-wait loops.

#### Chosen: Option A — New Status Value `no_live_session`

Return a `textResult` with `status: "no_live_session"`, the `role` field set to the
target role name, and a human-readable `message`. The handler returns this immediately,
before creating any ask task store entry, because no routing occurred and no ask task
exists to reference.

```json
{
  "status": "no_live_session",
  "role": "coordinator",
  "message": "No live session found for role 'coordinator'. The role may have completed its task or not yet started."
}
```

The three ask statuses now map cleanly to three distinct caller actions:
`question_pending` → wait for answer; `timeout` → retry or abandon; `no_live_session`
→ investigate (role not running). Callers detect the condition with a string comparison
on `status`, the same pattern already used in `niwa_await_task` re-wait loops.

#### Alternatives Considered

**Option B — Reuse `timeout` with `reason: "no_live_session"`**: Rejected because
`timeout` means a wait elapsed with no response — an ask task exists and ran out of
time. `no_live_session` is an immediate routing rejection before any wait begins.
Conflating the two would cause coordinators that retry on timeout to re-issue the
ask unnecessarily.

**Option C — MCP protocol-level error (`IsError: true`)**: Rejected on two grounds.
First, the server's dispatch always places `toolResult` in the JSON-RPC `Result`
field for all handler returns; returning a true protocol error would require changing
`callTool` or `handleRequest` beyond `handleAsk`. Second, `IsError: true` in this
codebase signals bad arguments or authorization failures, not runtime topology
conditions. A missing session is something the caller observes and acts on, not a
programming error.

---

## Decision Outcome

**Chosen: 1A + 2A + 3A**

### Summary

The three changes form a layered defense for coordinator delegation of long-running
tasks. The stop hook fires at every Claude Code turn boundary and calls
`niwa mesh report-progress --task-id $NIWA_TASK_ID`, which updates `last_progress.at`
in the task's `state.json` via the existing `mcp.UpdateState` flock-and-atomic-rename
path. The watchdog's stall timer resets silently on every turn — no agent awareness
required. If a worker's turn somehow takes longer than 15 minutes (a single tool call
blocking for the full stall window), the watchdog fires as before.

On a stall kill, the daemon checks `TaskState.Worker.ClaudeSessionID`, populated
earlier when the worker called `niwa_register_worker_session` during bootstrap. If
present and the session's `.jsonl` file passes a lightweight integrity check (exists,
non-empty, last complete line is valid JSON), the daemon invokes
`claude --resume <session_id> -p "<reminder>"` instead of the standard
`claude -p "<bootstrap>"`. The resumed session retains its full conversation history
and receives a targeted reminder explaining what happened and what to do next. Resume
attempts are capped by `TaskState.MaxResumes` (default 2) per fresh-spawn cycle;
when that cap is reached, the daemon falls back to fresh spawn and increments
`RestartCount` as today. If session ID was never registered or the session file is
corrupted, the daemon falls back to fresh spawn immediately.

The `niwa_ask` change is independent: `handleAsk` returns `{status:
"no_live_session", role: "..."}` immediately when no live session exists for the
target role, before creating any ask task. The ephemeral-spawn fallback is removed.
Coordinators check `status` after every `niwa_ask` call — the same pattern they
already use for `question_pending` / `timeout` — and can now distinguish the
no-session case explicitly.

### Rationale

The decisions reinforce each other across the two main failure modes. The stop hook
eliminates stall kills during normal operation; the resume path recovers context when
a stall kill does occur despite the hook (single long-blocking tool call). Together
they make coordinator delegation resilient without requiring any awareness from the
skills or applications running inside the worker session. The `niwa_ask` fix is
orthogonal but closes the remaining confusing behavior that surfaced in the same bug
report.

The key trade-off accepted is the new bootstrap requirement: workers must call
`niwa_register_worker_session` before starting real work, or they won't benefit from
the resume path. Workers that crash or are killed before the first bootstrap tool call
fall back to fresh spawn — the existing behavior. This is an acceptable gap because
the stop hook already makes stall kills rare, and crash recovery doesn't benefit from
session resumption (the session context itself may be inconsistent after an unexpected
crash).

## Solution Architecture

### Components Added or Modified

| Component | Change | File |
|-----------|--------|------|
| `niwa mesh report-progress` | New CLI subcommand | `internal/cli/mesh.go` |
| Stop hook script | New workspace-level hook | `.niwa/hooks/stop/report-progress.sh` |
| `workspace.toml` hook registration | New `[hooks] stop` entry | workspace template |
| `niwa_register_worker_session` | New MCP tool | `internal/mcp/` (handler, server, types) |
| `TaskWorker` | Add `ClaudeSessionID`, `ResumeCount` | `internal/mcp/types.go` |
| `TaskState` | Add `MaxResumes` | `internal/mcp/types.go` |
| `bootstrapPromptTemplate` | Updated to include registration call | `internal/cli/mesh_watch.go` |
| `retrySpawn` / `spawnWorker` | Resume-vs-fresh-spawn branch | `internal/cli/mesh_watch.go` |
| `handleAsk` | Replace ephemeral-spawn fallback with `no_live_session` return | `internal/mcp/handlers_task.go` |
| `transitions.log` | Add `resume: true` boolean to spawn entries | `internal/mcp/types.go` |

### Data Flow: Normal Operation (Stop Hook)

```
Worker turn completes
  → Claude Code fires Stop hook
  → report-progress.sh: niwa mesh report-progress --task-id $NIWA_TASK_ID
  → CLI subcommand: reads state.json (shared flock), verifies role+task_id
  → UpdateState: writes last_progress.at = now (exclusive flock, atomic rename)
  → Watchdog goroutine: reads last_progress.at (shared flock every 2s), resets stall timer
  → No stdout from hook → agent sees nothing
```

### Data Flow: Stall Kill and Resume

```
Worker turn takes > 900s (single blocking tool call)
  → Watchdog: fires SIGTERM → SIGKILL (5s grace) → unexpected_exit
  → retrySpawn reads TaskState.Worker.ClaudeSessionID
  → If present and ResumeCount < MaxResumes:
      integrity check: ~/.claude/projects/<cwd>/<session_id>.jsonl exists and has valid JSON
      → spawnWorker(resumeMode=true): claude --resume <session_id> -p "<reminder>" ...
      → Worker.ResumeCount++ (no RestartCount change)
  → If absent / check fails:
      → spawnWorker(resumeMode=false): claude -p "<bootstrap>" ...
      → Worker.ResumeCount = 0, RestartCount++
```

### Data Flow: Bootstrap Session Registration

```
Worker spawns
  → Reads bootstrap prompt: "Call niwa_register_worker_session first, then niwa_check_messages"
  → Worker discovers session ID from $CLAUDE_SESSION_ID (or DiscoverClaudeSessionID)
  → Calls niwa_register_worker_session(task_id, session_id)
  → Handler: validates session_id against sessionIDRegex, UpdateState writes Worker.ClaudeSessionID
  → Worker calls niwa_check_messages → retrieves task envelope → begins real work
```

### Schema Changes

**`TaskWorker`** (new fields, all `omitempty`):
```go
ClaudeSessionID string `json:"claude_session_id,omitempty"`
ResumeCount     int    `json:"resume_count,omitempty"`
```

**`TaskState`** (new field, `omitempty`):
```go
MaxResumes int `json:"max_resumes,omitempty"`
```

**`TransitionLogEntry`** (new field, `omitempty`):
```go
Resume bool `json:"resume,omitempty"`
```

Set to `true` on resume-spawn entries. Backward-compatible: existing readers ignore the unknown field. Enables tooling and diagnostics to distinguish resume attempts from fresh spawns in `transitions.log`.

Zero values produce correct fallback behavior: no session ID → fresh spawn; `MaxResumes=0` → default of 2; `Resume` absent → fresh spawn. Existing `state.json` and `transitions.log` files without these fields continue to parse correctly.

## Implementation Approach

### Phase 1 — Stop Hook (self-contained, no schema changes)

1. Add `niwa mesh report-progress` CLI subcommand in `internal/cli/mesh.go`. Uses `mcp.UpdateState` + role/task_id ownership check. Silent on success, silent on terminal-task no-op.
2. Write `report-progress.sh` stop hook script.
3. Register the hook in `workspace.toml` template under `[hooks] stop`.
4. Update hook materialization in `materialize.go` if needed (append path).
5. Tests: unit test for the CLI subcommand (ownership check, UpdateState call, terminal no-op); integration test that a spawned worker with the hook configured resets the watchdog at turn boundaries.

### Phase 2 — Session ID Capture (schema change, new MCP tool)

1. Add `ClaudeSessionID` and `ResumeCount` to `TaskWorker`; add `MaxResumes` to `TaskState` with default-2 logic in `createTaskEnvelope`.
2. Implement `niwa_register_worker_session` handler, register in `server.go`, add to allowed-tools list for workers.
3. Update `bootstrapPromptTemplate` in `mesh_watch.go`.
4. Tests: tool handler (valid/invalid session ID, idempotent re-registration); bootstrap prompt includes registration call.

### Phase 3 — Resume Path (watchdog change)

1. Implement session file integrity check (file exists, non-empty, last complete line parses as JSON).
2. Add resume-vs-fresh-spawn branch in `retrySpawn`. Add `resume: true` field to spawn `TransitionLogEntry`.
3. Tests: watchdog fires on a worker with a valid session ID → resume path taken; corrupt session file → fresh spawn taken; ResumeCount >= MaxResumes → fresh spawn taken.

### Phase 4 — `niwa_ask` Fix (requires Phase 2 in production)

1. Remove the ephemeral-spawn fallback from `handleAsk`. Return `{status: "no_live_session", role: args.To, message: "..."}` immediately when no live session found.
2. Tests: `niwa_ask` to a role with no session returns `no_live_session`; `niwa_ask` to a live coordinator still routes correctly (regression).

This phase must not ship before Phase 2 is deployed. The ephemeral-spawn fallback
exists because early coordinator session registration was unreliable — removing it
before registration is reliable creates a regression window where asks fail silently
if the coordinator's session has not yet been registered. Existing coordinator skill
code should be updated to handle `no_live_session` status explicitly before Phase 4
ships.

## Security Considerations

**CLI subcommand authorization**: The `niwa mesh report-progress` subcommand uses a
lighter ownership check than the MCP `kindExecutor` (role + task_id matching rather
than PPID chain cross-validation). The hook runs inside the worker's process tree,
so the environment variables it reads (`NIWA_TASK_ID`, `NIWA_SESSION_ROLE`,
`NIWA_INSTANCE_ROOT`) are set by the daemon and not controllable by untrusted input.
The subcommand does not expose any capability beyond resetting a timestamp in a file
the same process already has write access to. The risk of privilege escalation via
this path is negligible.

**Session ID validation**: `niwa_register_worker_session` validates the `session_id`
parameter against `sessionIDRegex` before storing it. An invalid session ID (too
long, wrong format, path traversal attempt) returns `BAD_PAYLOAD` without touching
`state.json`.

**Session file integrity check**: The check reads only a bounded suffix of the
`.jsonl` file (last 4 KB). It does not execute or eval the content. A maliciously
crafted `.jsonl` file cannot influence code execution through this path.

**Resume command injection**: The session ID is validated against `sessionIDRegex`
before being used in the `claude --resume <session_id>` invocation. The regex
confirms the ID is a UUID-like string and does not contain shell metacharacters. The
reminder message is a compile-time constant, not constructed from user input.

**`no_live_session` response**: Returns immediately without creating any task store
entry or spawning any process. No resource allocation on this path; cannot be used
for amplification.

**Resume cap ordering**: The `MaxResumes` cap check in `retrySpawn` must run
before the session file integrity check, not after. A compromised worker that
deliberately fails to heartbeat could otherwise force repeated integrity-check
attempts by manipulating the `.jsonl` file. Checking the cap first closes this
path unconditionally.

**`ClaudeSessionID` in debug output**: `TaskState.Worker.ClaudeSessionID` is a
pointer to a conversation history. It must not appear in log output intended for
external sharing (sanitized bug reports, `transitions.log`). The field is already
`omitempty` in JSON marshaling; ensure the struct comment documents this
constraint so it is not added to diagnostic exports later.

## Consequences

### Positive

- Workers running long-running skills (shirabe, or any future skill) survive beyond
  15 minutes without any change to those skills.
- Stall kills that do occur resume context-intact rather than starting over, making
  coordinator delegation of long workflows practical.
- Coordinators can detect and handle the case where `niwa_ask` targets a terminated
  role, enabling cleaner error handling in coordinator skill code.
- The stop hook mechanism is transparent to everything running inside the worker
  session — no skill or application needs to know niwa is involved.

### Negative

- Workers that don't call `niwa_register_worker_session` (e.g., custom workers that
  skip the bootstrap contract) don't benefit from the resume path and fall back to
  fresh spawn on stall kill.
- The new bootstrap step adds one MCP round-trip at worker start. This is negligible
  in practice (< 100ms) but is now a required call that can fail.
- Removing the ephemeral-spawn fallback from `handleAsk` is a behavior change for
  any caller relying on getting some kind of response regardless of session liveness.
  No known caller depends on this behavior, but the change should be communicated.

### Mitigations

- Bootstrap failure (niwa_register_worker_session returns an error) should cause the
  worker to log a warning and continue — degraded resume capability is better than a
  broken worker.
- The `MaxResumes` and `MaxRestarts` caps bound the worst-case retry budget. A task
  that resumes and re-stalls still eventually gets abandoned rather than running
  indefinitely.

## Related Design Docs

- `DESIGN-cross-session-communication.md` — foundational mesh architecture
- `DESIGN-niwa-ask-live-coordinator.md` — live coordinator session routing (0.9.4)
- `DESIGN-worker-permissions.md` — worker spawn configuration
