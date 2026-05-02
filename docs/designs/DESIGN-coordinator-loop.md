---
status: Proposed
problem: |
  When a coordinator delegates a long-running task via niwa_delegate, the stall
  watchdog kills the worker at 15-minute intervals because nothing calls
  niwa_report_progress during deep workflow work. On each kill, niwa spawns a
  fresh worker process — losing all in-session context — and the cycle repeats
  until max_restarts is exhausted. The coordinator's decision loop never fires
  because the worker never reaches the niwa_ask calls that would surface decisions.
  Three architectural gaps need to be closed: automatic progress heartbeating,
  context-preserving stall recovery, and a typed error from niwa_ask when the
  target role has no live session.
---

# DESIGN: Coordinator Loop Stall Recovery

## Status

Proposed

## Context and Problem Statement

A coordinator delegating a long-running workflow (explore+PRD+design via shirabe)
observed three failure modes on niwa 0.9.4:

**Failure 1: Stall watchdog kills workers mid-workflow.** The daemon's stall detector
fires after 900 seconds (15 minutes) of no `niwa_report_progress` calls. Skills running
inside a delegated session — reading files, running parallel research agents, writing
outputs — do not call this tool. From the watchdog's perspective, a worker doing 15
minutes of uninterrupted work looks identical to a hung process. The worker is killed,
restarted from a blank state, and the cycle repeats until `max_restarts` is exhausted.
The coordinator's `niwa_await_task` loop returns only timeouts until the task finally
completes or is abandoned.

**Failure 2: Fresh-spawn restart loses context.** niwa's current restart path spawns
a new `claude -p` process with a fixed bootstrap prompt. All in-session context is
gone. The previous session's work is preserved only if the application happened to
write checkpoint files to disk — but this is an application concern (shirabe's wip/
pattern), not a niwa guarantee. A worker built on any other application starts
completely from scratch, hits the same 15-minute window, and gets killed again.

**Failure 3: `niwa_ask` to a terminated role produces a confusing loopback.** When
a coordinator calls `niwa_ask(to='codespar-web')` after the task completes, `handleAsk`
finds no live session and falls back to spawning an ephemeral worker. That worker's
response routes back to the delegator — the coordinator receives its own question in
its inbox. There is no typed error, and callers cannot distinguish this case from a
normal response.

The root cause shared by Failures 1 and 2: the system assumes workers will call
`niwa_report_progress` regularly, but provides no mechanism to make this happen
automatically. Requiring individual skills to call it breaks their abstraction —
skills should not know how they are invoked.

## Decision Drivers

- **Abstraction integrity**: Skills and application code should not carry awareness
  of their niwa invocation context. Progress reporting must be infrastructure, not
  a per-skill concern.
- **Application-agnostic**: The fix must work for any niwa worker — not only shirabe.
  Coordinator delegation is a general mechanism.
- **Minimal agent footprint**: Prefer mechanisms that work without the agent's active
  cooperation. If the agent ignores instructions, the system should still function.
- **Context preservation on recovery**: A stall kill is not the same as a crash.
  The worker was making progress; restarting from scratch is unnecessary and
  potentially expensive for long workflows.
- **Resilience through layering**: No single mechanism is guaranteed to fire at the
  right moment. Stack defenses so that if one misses, the next catches it.
- **Backward compatibility**: Existing sessions, tools, and coordinator patterns
  should continue to work without modification.

## Decisions Already Made

From exploration (these are constraints, not open questions for this design):

- **Skill-level fix rejected**: Requiring shirabe or other skills to call
  `niwa_report_progress` breaks the abstraction boundary. The fix lives in niwa's
  delegation and restart infrastructure.
- **Resume-over-fresh-spawn**: On stall kill, niwa should resume the killed session
  (`claude --resume <session_id>`) with a reminder injected, rather than spawning
  a fresh process. This preserves context and corrects the root cause in-flight.
- **Stop hook as primary mechanism**: A Claude Code stop hook configured at the
  workspace level fires at every turn boundary, providing a reliable automatic
  heartbeat without requiring agent awareness. niwa's hook merge semantics use
  append-only concatenation — no conflict with existing shirabe stop hooks.
- **Fresh spawn as fallback only**: When session resume is not possible (session file
  corrupted, session ID not yet captured), fall back to the current fresh-spawn path.
- **`niwa_ask` typed error**: When `handleAsk` finds no live session for the target
  role, return a typed error rather than routing to the ephemeral-spawn fallback.
  Callers must be able to distinguish this case.
- **Decision protocol not in scope**: The delegation body's `decision_protocol` field
  has no enforcement mechanism and that is by design. This design does not attempt
  to enforce it.

## Open Design Questions

The following questions need architectural decisions with trade-off analysis:

### 1. Stop Hook Automation Level

The stop hook fires at every turn boundary. Two approaches:

**Option A — Fully automated (hook calls niwa directly)**: The hook script reads
`NIWA_TASK_ID` from the worker's environment and calls a new CLI subcommand
(e.g., `niwa mesh report-progress --task-id $NIWA_TASK_ID`) that updates
`last_progress.at` in the task state file. Requires no agent involvement. Requires
a new niwa CLI subcommand that writes to task state from a shell process.

**Option B — Reminder output (hook injects context)**: The hook script outputs a
reminder string that Claude Code injects as a user message on the next turn.
The agent reads it and is expected to call `niwa_report_progress`. Simpler to
implement, but still relies on agent compliance.

### 2. Session ID Capture for Resume

The resume path requires knowing the killed worker's Claude Code session ID. Workers
are currently spawned with no session ID recorded. Three candidate approaches:

**Option A — New MCP tool `niwa_register_worker_session`**: Worker calls this tool
during bootstrap (before starting real work). Daemon stores the session ID in
`TaskState.Worker.ClaudeSessionID`. Clean, explicit, but adds a required bootstrap
step.

**Option B — Extend `niwa_report_progress` to accept optional session ID**: On first
call, worker passes its session ID alongside the progress message. Avoids a new
tool but couples session registration to progress reporting.

**Option C — Post-mortem discovery from `~/.claude/projects/`**: After a stall kill,
scan the worker's project directory by mtime to find the most recent session file.
Requires knowing the worker's cwd and relies on filesystem timing — fragile and racy.

### 3. Resume Code Path in Watchdog

When the watchdog fires and a session ID is available, the restart path changes from:
```
claude -p "<bootstrap_prompt>" --permission-mode=acceptEdits --mcp-config=<path>
```
to:
```
claude --resume <session_id> -p "<reminder>" --permission-mode=acceptEdits --mcp-config=<path>
```

Open questions: What should the reminder say, and should the agent be instructed to
call `niwa_report_progress` as its first action? Should the resume count toward
`max_restarts`, or be tracked separately? What happens if the resumed session stalls
again — is there a max-resume cap to prevent infinite loops? How should the daemon
validate that the session `.jsonl` file is intact before attempting resume (a SIGKILL
mid-write could leave a partial final line)?

### 4. `niwa_ask` Error Response Format

When `handleAsk` finds no live session for the target role, what does the typed error
look like? Should it follow the existing `question_pending` / `timeout` / `completed`
status vocabulary, or introduce a new status value? Should the error include the target
role name so the caller can take corrective action?

## Related Design Docs

- `DESIGN-cross-session-communication.md` — foundational mesh architecture
- `DESIGN-niwa-ask-live-coordinator.md` — live coordinator session routing (0.9.4)
- `DESIGN-worker-permissions.md` — worker spawn configuration
