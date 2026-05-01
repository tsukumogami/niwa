# Lead: Stall watchdog threshold and configurability

## Findings

### Threshold Value: Hardcoded Default of 900 Seconds (15 Minutes)

**Location:** `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-7/public/niwa/internal/cli/mesh_watch.go` lines 79, 1896

The stall watchdog threshold is **900 seconds (15 minutes) by default**. This is defined as:
```go
type daemonConfig struct {
    ...
    StallWatchdog time.Duration   // NIWA_STALL_WATCHDOG_SECONDS (default 900)
    ...
}

func loadDaemonConfig(logger *log.Logger) daemonConfig {
    cfg := daemonConfig{
        ...
        StallWatchdog: 900 * time.Second,  // line 1896
        ...
    }
    ...
}
```

The value matches the bug report precisely: kills at exactly 15-minute intervals indicate the watchdog fired on schedule at 900-second boundaries (minute 15, 30, 45).

### Global Configuration: NIWA_STALL_WATCHDOG_SECONDS Environment Variable

**Configurability:** YES — but **daemon-wide only**, not per-task or per-role.

**Location:** `mesh_watch.go` lines 1908–1913

The daemon reads the `NIWA_STALL_WATCHDOG_SECONDS` environment variable at startup:
```go
if raw := os.Getenv("NIWA_STALL_WATCHDOG_SECONDS"); raw != "" {
    if v, err := strconv.Atoi(raw); err == nil && v > 0 {
        cfg.StallWatchdog = time.Duration(v) * time.Second
    } else {
        logger.Printf("warning: NIWA_STALL_WATCHDOG_SECONDS=%q invalid; using default", raw)
    }
}
```

**Constraints:**
- Value must be a positive integer representing seconds
- Invalid values (parse error, non-positive) silently fall back to the 900-second default with a warning log
- The override is applied **once at daemon startup** and applies to **all workers** spawned by that daemon instance
- **No per-task override exists** in the task envelope, task state, or any task-level configuration

### Watchdog Implementation: How It Works

**Location:** `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-7/public/niwa/internal/cli/mesh_watch.go` lines 1038–1151

The watchdog is implemented in `runWatchdog()` and enforces two independent timers:

1. **Stalled-progress timer** (lines 1063, 1096–1102):
   - Fires when `last_progress.at` has not advanced for the configured `stallWatchdog` duration
   - Reset every 2 seconds (line 1058: `watchdogPollInterval`)
   - When fired, sends SIGTERM to the worker's process group and escalates to SIGKILL after a grace period (lines 1101–1102)
   - Reason logged as "stall" (line 1102)

2. **Defensive-reap timer** (lines 1145–1146):
   - Monitors tasks whose state is already terminal but the worker process is still alive
   - Allows a grace period (`sigTermGrace`, default 5 seconds) for the worker to exit cleanly after `niwa_finish_task`
   - If the worker doesn't exit within the grace window, escalates to SIGTERM/SIGKILL (lines 1110–1111)

**Critical detail:** The stall timer is **reset by progress calls** (line 1128 in `handleReportProgress`):
```go
if curProgressAt != "" && curProgressAt != lastProgressAt {
    lastProgressAt = curProgressAt
    resetTimer(stallTimer, s.stallWatchdog)
}
```

This means **workers must call `niwa_report_progress` every 15 minutes (or the configured threshold) to stay alive**. Workers doing real work but not calling `niwa_report_progress` will be killed after exactly 15 minutes.

### No Per-Task Override Mechanism

**Task State Structure:** `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-7/public/niwa/internal/mcp/types.go` lines 258–276

The `TaskState` struct has **no field for stall threshold**:
```go
type TaskState struct {
    V                  int               `json:"v"`
    TaskID             string            `json:"task_id"`
    State              string            `json:"state"`
    StateTransitions   []StateTransition `json:"state_transitions"`
    RestartCount       int               `json:"restart_count"`
    MaxRestarts        int               `json:"max_restarts"`  // restart cap is per-task
    LastProgress       *TaskProgress     `json:"last_progress,omitempty"`
    Worker             TaskWorker        `json:"worker"`
    DelegatorRole      string            `json:"delegator_role"`
    TargetRole         string            `json:"target_role"`
    ...
}
```

While `MaxRestarts` is stored per-task (enabling per-task restart cap overrides), there is **no equivalent stall-watchdog-threshold field**. The watchdog duration is read from the daemon config once at startup and applied uniformly to all tasks.

**Task envelope also has no stall override:** The `TaskEnvelope` struct (types.go lines 214–224) carries delegation metadata but no progress-timeout or stall-threshold override.

### Skill Guidance on Progress Reporting

**Location:** `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-7/public/niwa/internal/workspace/channels.go` lines 686–692

The `niwa-mesh` skill installed into every workspace documents the recommended cadence:
```
Call `niwa_report_progress` every 3-5 minutes of wall-clock work, or
every ~20 tool calls, whichever arrives sooner. ... Progress calls
reset the stalled-progress watchdog, so regular reporting prevents
SIGTERM escalation during long runs.
```

This is **guidance, not enforcement**. Workers are free to report less frequently, but risk being killed if more than 15 minutes (900 seconds) elapse without a progress call.

### Log Entries and Reason Code

**Transitions log entry:** When the watchdog fires due to stall (not defensive-reap), it appends a `watchdog_signal` entry with reason="stall":

```json
{"v":1,"kind":"watchdog_signal","at":"...","signal":"SIGTERM","actor":{...},"reason":"stall"}
```

This matches the bug report showing "reason: stall" in transitions.log.

## Implications

### Why the Bug Report Shows Exactly 15-Minute Intervals

The three kills at minutes 15, 30, 45 are **not coincidences**. They reflect:
1. The default threshold of 900 seconds (15 minutes)
2. The worker never calling `niwa_report_progress` (so the timer never reset)
3. The watchdog polling every 2 seconds and firing deterministically when the timer expires

Each restart resets the timer to 15 minutes, causing the next kill at predictable 15-minute marks.

### Design Intent: Progress as Life-Sign

The stall watchdog implements a **life-sign / heartbeat** model:
- Workers are assumed to be making progress when they call `niwa_report_progress`
- Silence for 15 minutes (default) = presumed stall or infinite loop
- The task author (delegator) configured the max restart cap (default 3), expecting restarts to succeed
- The watchdog's role is to prevent hung workers from consuming resources indefinitely

From DESIGN-cross-session-communication.md:
> "run a stalled-progress watchdog to kill runaway workers"

The watchdog is **not a per-task timeout**; it's a **staleness detector** at the daemon level.

### Bug Consequence: Incompatibility with Long-Running Tasks Without Progress Reporting

The shirabe task (explore+PRD+design) likely involved:
- Long periods of work (code reading, analysis, file writing to `wip/`)
- No calls to `niwa_report_progress` (workers write directly to disk, not through task messages)
- After 15 minutes, watchdog kills the process
- After 3 restarts, daemon abandons the task with `reason: "retry_cap_exceeded"`

This reveals a gap: **tasks that do real work but don't call `niwa_report_progress` regularly have a hard ceiling of 15 minutes per attempt**, regardless of actual progress.

## Surprises

1. **No per-task override exists at all.** While the restart cap (`MaxRestarts`) is per-task, the watchdog threshold is daemon-global. This is asymmetric.

2. **The environment variable override is startup-only.** You cannot change `NIWA_STALL_WATCHDOG_SECONDS` mid-session; it takes effect only when the daemon starts.

3. **Progress call semantics are weak.** Calling `niwa_report_progress` resets the timer, but there's no enforcement that it be called regularly. A worker can go silent for 14:59 and still trigger at 15:00. The cadence recommendation ("every 3-5 minutes") in the skill is ergonomic guidance, not a contract.

4. **Defensive-reap is separate.** Even if a worker finishes a task on time, if it doesn't exit the process, the daemon will kill it after an additional 5-second grace window (the `sigTermGrace` default). This is a cleanup mechanism for hung workers post-completion.

5. **No coordinator-to-worker timeout for niwa_ask.** The bug report mentions "niwa_ask to a completed role looping back to the sender." The `niwa_ask` default timeout (600 seconds = 10 minutes) is shorter than the worker stall timeout (900 seconds), meaning a coordinator can time out waiting for a response before the worker itself is killed.

## Open Questions

1. **Was the 15-minute threshold chosen empirically or by design?** The code comment says "Issue #6" and "Issue 6 scope ... stall watchdog" (mesh_watch.go line 26), suggesting it was driven by a specific issue. Does an issue tracker record the rationale?

2. **Should per-task stall thresholds be supported?** If a delegator knows a task will take 2 hours without progress reporting, should they be able to override the threshold when delegating (e.g., `niwa_delegate(..., stall_timeout_seconds=7200)`)?

3. **Should the skill guide recommend a safer cadence?** If tasks are vulnerable to the 15-minute kill, should the skill recommend `niwa_report_progress` every 5 minutes unconditionally, or document the risk?

4. **How does this interact with coordinator delegation workflows?** The bug shows three failure modes, including "niwa_ask to a completed role looping back to the sender." If a coordinator is blocked on `niwa_await_task` waiting for a worker to finish a delegated subtask, and that worker hits the stall timeout, the coordinator gets an out-of-band task-abandoned message. Is there a protocol for surfacing this to the coordinator?

5. **Could the shirabe task have been configured to avoid restarts?** Was the expectation that a single long task should have `MaxRestarts: 0` to fail fast on the first timeout, or is the current cap-then-abandon behavior correct for long-running exploratory work?

## Summary

The stall watchdog threshold is **hardcoded to 900 seconds (15 minutes) by default** and is **configurable daemon-wide via the `NIWA_STALL_WATCHDOG_SECONDS` environment variable**, but **no per-task or per-role override exists**. Workers must call `niwa_report_progress` regularly to reset the timer; silence for 15 minutes triggers SIGTERM escalation and eventual abandonment after restart-cap exhaustion. The design intent is to prevent runaway/hung workers from consuming resources, using progress calls as a heartbeat mechanism; the bug report's symmetrical 15-minute kills reflect this deterministic threshold firing when workers do real work without reporting progress. The architecture lacks per-task timeout configuration, creating a hard ceiling incompatible with long-running tasks that don't integrate progress reporting.

