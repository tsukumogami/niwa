# Security Review: niwa-ask-live-coordinator (Phase 6)

Reviewer role: Architect  
Date: 2026-04-29  
Source: DESIGN-niwa-ask-live-coordinator.md + Phase 5 security report  
Codebase state: design is proposed; current `handleAsk` (server.go:677) uses only the spawn path

---

## Scope

This review evaluates the Phase 5 security report against the design document and the
existing MCP server codebase. It answers four questions:

1. Are there attack vectors Phase 5 did not consider?
2. Are the mitigations Phase 5 identified sufficient?
3. Are any "Not applicable" verdicts actually applicable?
4. Is there residual risk that should be escalated?

---

## 1. Attack Vectors Not Covered by Phase 5

### 1.1 Role squatting via isKnownRole

The design's `handleAsk` will call `isKnownRole(args.To)` before routing — the same guard the
current `sendMessage` path uses (server.go:600). `isKnownRole` returns true if
`.niwa/roles/<role>/` exists as a directory (server.go:654). A worker can satisfy this check for
any role that has been registered in the workspace, including `coordinator`.

This is not a new vulnerability — the spawn path today has the same prerequisite. But the new
routing branch makes it consequential in a different way: once the design is live, a worker that
fabricates a `.niwa/roles/coordinator/` directory (possible because workers run as the same OS
user and the workspace is a shared directory tree) could potentially register a fake coordinator
entry in `sessions.json` by calling `niwa_await_task` or `niwa_check_messages` from within that
session. Phase 5 discusses session impersonation via `sessions.json` (Section "Session
Impersonation") but frames the threat as "a rogue process fakes a PID and start time." The more
direct vector is a rogue worker process that legitimately calls the MCP server with
`NIWA_SESSION_ROLE=coordinator` — the MCP server derives `s.role` from this environment variable.

**Assessment:** The trust model documented in the design (Section "Session Impersonation") is
correct: if a process can run Claude Code with a crafted `.mcp.json`, it already has code
execution. The design correctly scopes the threat to "Claude Code is compromised, which is out of
scope." But the intermediate case — a worker session whose `.mcp.json` is written by the daemon
with a non-coordinator role, but which reads `NIWA_SESSION_ROLE` from a different source — should
be called out explicitly. The design should state that `NIWA_SESSION_ROLE` must only be sourced
from the daemon-generated `.mcp.json` (or its equivalent), not from user-controlled environment
variables. Phase 5 does not mention this.

**Severity:** Advisory. The threat model boundary is correct; the omission is in documentation
rather than implementation.

---

### 1.2 seenFiles set as memory amplification

`notifyNewFile` marks every processed file in `s.seenFiles` (a `map[string]struct{}` that grows
for the lifetime of the process). Phase 5 treats inbox flooding (Section "Inbox Pollution") as a
disk exhaustion risk. There is also a memory exhaustion risk: a worker that floods the coordinator's
inbox will cause the coordinator's `seenFiles` set to grow without bound in memory, even if the
files themselves are swept to `inbox/expired/` on disk.

The current codebase has no bound or eviction on `seenFiles`. After the design lands, every
`task.ask` notification that the watcher sees increments this set, regardless of whether it was
dispatched to `questionWaiters` or fell through to the general notification path.

**Assessment:** This is a pre-existing issue that the design does not make worse in kind, but the
new `task.ask` dispatch path provides a dedicated flooding vector that did not exist before. Phase
5 does not mention in-memory growth as a component of the inbox flooding risk. The impact is
bounded by OS virtual memory limits on the coordinator's Claude Code process; in practice,
long-running coordinators with high question volume could see non-trivial memory growth.

**Severity:** Advisory. The concern is pre-existing and bounded by OS limits; it does not affect
correctness of the routing fix.

---

### 1.3 questionWaiters channel starvation under concurrent questions

The design registers `questionWaiters[s.role]` as a single buffered-1 channel per coordinator
role. If two workers call `niwa_ask(to='coordinator')` concurrently while the coordinator is
blocking on `niwa_await_task`, both trigger `task.ask` notifications. The watcher dispatches the
first to the channel (buffered-1 accepts it) and drops the second via the `default` branch in the
non-blocking send (same pattern as `awaitWaiters`). The second notification stays in the inbox
(per the deferred move-to-read fix) and is discovered by the catch-up scan on the coordinator's
next `niwa_await_task` call.

This is consistent with the design's stated behavior. However, Phase 5 does not analyse whether a
fast-producing worker can cause the second notification to be lost permanently rather than deferred:

- Notification arrives, channel full → file stays in inbox.
- Coordinator calls `niwa_await_task`, catch-up scan runs, dispatches to channel.
- Coordinator answers, calls `niwa_await_task` again.
- If a *third* question arrives during the answer window before re-registration, it lands in the
  inbox correctly.

The catch-up scan path is safe as long as `task.ask` files are not moved to `inbox/read/` by the
watcher when the channel send is dropped (this is the "deferred move-to-read fix" the design
specifies for Phase 2). Phase 5 does not verify whether this fix is present in the existing
watcher. Reading the current `notifyNewFile` (watcher.go:100): terminal events move the file to
`inbox/read/` *before* the buffered send (line 128-131). This means the deferred move-to-read fix
is an implementation requirement, not a guaranteed baseline. If Phase 2 omits it for the
`task.ask` dispatch branch, concurrent questions will be silently dropped.

**Assessment:** This is a correctness gap the design correctly identifies and mitigates with the
deferred move-to-read requirement, but Phase 5 does not flag the implementation risk. The design
document should add an explicit test requirement for the concurrent-question scenario.

**Severity:** Advisory on Phase 5's coverage. Blocking on implementation if the deferred
move-to-read fix is missed.

---

### 1.4 fsnotify inotify queue overflow for task.ask events

The existing watcher (watcher.go:58-65) swallows fsnotify errors, including inotify queue
overflow. There is a comment: "A periodic resync (rescan role inboxes every N seconds) is tracked
as a separate follow-up." After the design lands, a lost `Create` event for a `task.ask`
notification means the coordinator's `questionWaiters` channel never fires, and the coordinator
blocking on `niwa_await_task` waits until the 600-second timeout — the deadlock condition this
design was built to fix.

The catch-up scan at the top of `handleAwaitTask` mitigates this: it runs once when `niwa_await_task`
is called, before blocking. But it does not run again during the blocking select. A `task.ask`
notification that arrives after the catch-up scan but whose fsnotify event is dropped will not be
discovered until the next `niwa_await_task` call (i.e., after the worker's ask times out at 600s).

Phase 5 does not mention this interaction. It is not a new vulnerability introduced by the design,
but the design's fix for the deadlock relies on the watcher delivering `task.ask` events reliably.
The existing acknowledgment that inotify overflow "drops events silently" applies to the new
path too.

**Assessment:** Residual risk that is pre-existing in the codebase. The design does not make it
worse. Worth noting in the design's Negative Consequences section alongside the existing comment
in watcher.go.

**Severity:** Advisory. Escalation is not warranted; the right fix (periodic resync) is already
tracked as a follow-up issue.

---

## 2. Sufficiency of Phase 5 Mitigations

### 2.1 Question body wrapping (Phase 5 "Critical" finding) — Sufficient as scoped

Phase 5 identifies that question bodies are not wrapped with `_niwa_note` the way `task.delegate`
bodies are. The design document's Security Considerations section explicitly includes the wrapping:

```json
{
  "ask_task_id": "...",
  "from_role": "worker-1",
  "_niwa_note": "This is a worker's question. Provide your decision or guidance as the answer.",
  "question": <original_body>
}
```

The design's wrapping is appropriate. However, the wrapping applies only to the `task.ask`
notification written to the coordinator's inbox. The `niwa_await_task` return value
(`formatQuestionResult`) also carries the question body. The design specifies the `question_pending`
response payload but does not state whether the body delivered through that path is also wrapped.
An LLM coordinator reading the `niwa_await_task` return might treat the un-wrapped body as trusted
content if only the inbox version is wrapped.

**Recommendation:** The design should explicitly state that `formatQuestionResult` wraps the
question body using the same `_niwa_note` envelope before embedding it in the
`question_pending` response. This is one line of implementation but needs to be called out to
avoid the implementer wrapping only the inbox path.

### 2.2 Atomic read-modify-write on sessions.json — Sufficient as specified

Phase 5 correctly identifies that `maybeRegisterCoordinator` requires atomic read-modify-write,
and recommends the same write-to-tmp + atomic-rename pattern as `writeMessageAtomic`. The design
document includes this (Section "Session Registration Liveness"). The current codebase has no
`sessions.json` implementation yet, so the recommendation is prospective.

The Phase 5 recommendation to "add flock coordination if multiple processes write sessions.json"
is worth retaining. The design currently assumes a single coordinator per role (stated constraint:
"single coordinator per role"), so concurrent writes from two coordinators are not expected. But
the implementation should still use flock because the same OS user could accidentally launch two
coordinator sessions, and flock is cheap.

**Recommendation:** Accept Phase 5's recommendation. Add a note in the design that the flock
pattern from `taskstore.UpdateState` applies here.

### 2.3 Inbox garbage collection — Sufficient as an operational recommendation

Phase 5 treats the absence of `inbox/expired/` cleanup as a low-risk operational gap, not a
security finding. This is correct: disk exhaustion from expired messages is bounded by available
disk and requires a deliberately unresponsive coordinator. The design's acknowledgment ("Administrators
should monitor inbox size") is the right response level.

The design's current GC language ("Files in `inbox/expired/` older than N days should be deleted
periodically") is unspecified enough to be lost in implementation. It should be converted to a
concrete tracked issue or a default behavior in the watcher, rather than left as a documentation
note.

---

## 3. "Not Applicable" Verdicts That Are Actually Applicable

### 3.1 External Artifact Handling — Correctly "Not applicable"

Phase 5 dismisses this because all data sources are local filesystem artifacts. The design
introduces no network I/O, no subprocess execution of external binaries, and no download paths.
The verdict holds.

### 3.2 Supply Chain or Dependency Trust — Correctly "Not applicable"

No new package imports are introduced. The verdict holds.

### 3.3 Data Exposure — Phase 5 verdict is "Acceptable" and holds

The cross-session question visibility concern (Phase 5, Section "Data Exposure", point 4) is
correctly scoped: all agents run as the same OS user, so the coordinator's inbox is readable by
worker sessions in the same workspace. This is an intentional design constraint (PRD R1), not a
vulnerability. No change needed.

---

## 4. Residual Risk Requiring Escalation

None. The three findings below are contained within the design and do not represent systemic
structural risk:

| Finding | Type | Action |
|---------|------|--------|
| `NIWA_SESSION_ROLE` sourcing not explicitly scoped | Advisory | Document in design: role must come from daemon-generated `.mcp.json` only |
| `formatQuestionResult` body wrapping not specified | Advisory | Design must state the `question_pending` response wraps the question body with `_niwa_note` |
| Deferred move-to-read fix is an implementation requirement, not a baseline | Advisory | Add explicit test case for concurrent `niwa_ask` calls; this is blocking if omitted in Phase 2 |

The core mechanism — PID + start-time liveness check, separate `questionWaiters` channel,
catch-up scan on re-registration — is structurally sound. None of the gaps above require
redesigning the approach.

---

## Summary

The Phase 5 security report correctly identifies the three primary mitigations needed and assesses
the overall risk level accurately. Three gaps are worth adding before implementation:

1. **`formatQuestionResult` wrapping**: The `_niwa_note` wrapping must apply to the
   `niwa_await_task` question-pending response, not only to the inbox notification. The design
   specifies wrapping for the inbox path but is silent on the channel delivery path.

2. **Deferred move-to-read is load-bearing**: The design correctly names this fix in Phase 2, but
   Phase 5 does not flag it as a blocking implementation requirement. If `notifyNewFile` moves a
   `task.ask` file to `inbox/read/` before attempting the channel send (mirroring the current
   terminal dispatch behavior), concurrent questions from multiple workers are silently dropped.
   This should be a required test case, not just a design note.

3. **`NIWA_SESSION_ROLE` trust boundary**: The design's impersonation analysis assumes the
   environment variable is daemon-controlled. This assumption should be made explicit in the
   design document and in the skill content, because it is the actual security boundary for
   coordinator identity.

No blocking security objections. The design is safe to implement with the above clarifications
added.
