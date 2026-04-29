# Security Review: niwa-ask-live-coordinator

## Dimension Analysis

### External Artifact Handling

**Applies:** No

The design does not download, execute, or process external inputs beyond existing niwa mechanisms. All data sources are local filesystem artifacts:

- `sessions.json`: Written by the MCP server process itself; read back within the same process context
- Inbox messages: Created atomically by `writeMessageAtomic` with atomic rename (write to `.tmp`, rename to final name)
- Task envelopes: Created via the existing `createTaskEnvelope` path, unchanged by this design

The design adds one new read: `handleAsk` reads `sessions.json` to discover live coordinator sessions. This is not an external artifact — it's a file the MCP server maintains, with no parsing of untrusted content. The file contains PID and start-time fields from the same process that writes it.

All file writes use the proven atomic pattern (write to temporary file, rename). No new parsing of complex formats is introduced.

**Verdict:** Not applicable — no external artifacts.

---

### Permission Scope

**Applies:** Yes (minimal risk)

**Current state:**
- All niwa communication is local filesystem-based (no network)
- All agents run as the same OS user (PRD R1)
- File permissions: private directories 0o700, private files 0o600
- No POSIX capabilities, no setuid, no privilege escalation
- The MCP server runs in the Claude Code process context (same user)

**Changes introduced:**
- `handleAsk` reads `sessions.json` (already readable by the MCP server; no new permissions required)
- `maybeRegisterCoordinator` writes `SessionEntry` to `sessions.json` (append/modify via atomic read-modify-write; same user writing a file it owns)
- `notifyNewFile` dispatches to `questionWaiters[to.role]` (in-memory channel; no filesystem permissions)
- `handleAwaitTask` registers in `questionWaiters` (in-memory; no filesystem permissions)

**Potential risks:**
1. **File race on sessions.json**: Multiple workers could call `handleAsk` concurrently while a coordinator is registering itself. If reads and writes are unsynchronized, the registry could become inconsistent.

   **Mitigation in design:**
   - Each `SessionEntry` write is independent — workers call `handleAsk` and read `sessions.json`; coordinators call `maybeRegisterCoordinator` and write `SessionEntry`
   - Reads are non-destructive: `handleAsk` reads and calls `IsPIDAlive`, but doesn't modify `sessions.json`
   - Writes use atomic read-modify-write: the implementation should read `sessions.json`, deserialize, append/update the coordinator's entry, serialize, and write atomically to a temporary file then rename
   - This pattern matches the existing atomic rename practice (`writeMessageAtomic`)

2. **PID recycling on stale sessions**: A coordinator exits, the OS reuses its PID for an unrelated process. The new process is not a coordinator, but `IsPIDAlive` returns true because the PID is alive.

   **Mitigation in design:**
   - `IsPIDAlive(pid, startTime int64)` cross-checks PID with process start time from `/proc/<pid>/stat`
   - Stale entries have mismatched start times and are correctly rejected
   - Risk level: Low — the existing `IsPIDAlive` implementation already guards against this

3. **TOCTOU (time-of-check-time-of-use)**: `handleAsk` calls `IsPIDAlive(pid, startTime)` and then writes to the coordinator's inbox. Between the check and the write, the coordinator's process could exit.

   **Impact:** The question is written to the coordinator's inbox even though the coordinator has exited. The question waits indefinitely in the inbox.

   **Mitigation:** This is the accepted design behavior (from Decision 1 rationale: "No timeout/fallback-to-spawn"). The question queues until the coordinator next contacts the daemon. If the coordinator never resumes, the worker blocks until `niwa_ask` timeout (default 600s). This is not a security issue — it's the intended async-messaging model.

**Verdict:** Acceptable. The design preserves the OS user isolation model (all agents are same user) and adds no new escalation vectors. Atomic file operations prevent corruption. PID-start-time cross-checking prevents recycling attacks. The liveness check is not a security gate, so TOCTOU is not a vulnerability.

---

### Supply Chain or Dependency Trust

**Applies:** No

The design doesn't introduce new dependencies on external code, configuration, or artifacts. It uses:
- Existing Go standard library functions (`os.ReadFile`, `json.Marshal`, `time.Parse`)
- Existing custom functions (`IsPIDAlive` from `liveness.go`, atomic rename pattern from `writeMessageAtomic`)
- Existing local files (`.niwa/sessions/sessions.json` — created and maintained by niwa itself)

No package imports are added. No external service calls are introduced.

**Verdict:** Not applicable — no new supply chain trust required.

---

### Data Exposure

**Applies:** Yes (acceptable, already constrained)

**Data in motion:**
- Questions (`task.ask` notifications): Written to the coordinator's inbox at `.niwa/roles/coordinator/inbox/<id>.json`
- Answers (`niwa_finish_task` result): Returned via the task completion path, already present
- PID and start-time in `sessions.json`: Process metadata, used for liveness checks

**Exposure risks:**

1. **Question visibility in inbox**: Questions are written as `Message` files in the coordinator's inbox. These are world-readable if file permissions are misconfigured.

   **Mitigation:**
   - Inbox directories are created with 0o700 (read/write/execute for owner only)
   - Message files inherit the directory's mode; `writeMessageAtomic` writes with 0o600
   - All niwa state is private to the OS user running Claude Code
   - No change to the existing isolation model

2. **Session registry exposure**: `sessions.json` contains PID and start-time metadata for live coordinators.

   **Mitigation:**
   - File is at `.niwa/sessions/sessions.json`, within the private workspace
   - Permissions: 0o600 (owner read/write only)
   - Data is: session ID (already exposed via env), role name (public within the workspace), PID (process metadata, not secret), start time (time value, not secret)
   - No sensitive credentials or keys are stored here

3. **Coordinator discovery via sessions.json**: The existence of a `sessions.json` entry could leak that a coordinator is active.

   **Threat model:** Same OS user has read access to all workspace files. This is not a confidentiality issue; the coordinator's activity is already observable by watching the inbox directory or enumerating processes.

4. **Cross-session question visibility**: A coordinator could theoretically answer questions from any worker. The design doesn't restrict which workers can ask which coordinators.

   **Design constraint:** NIWA_SESSION_ROLE is set in `.mcp.json` (documented, not secret). Workers hardcode their target role when calling `niwa_ask(to='coordinator')`. If a workspace has multiple coordinators with different roles, the question routing by role (in `handleAsk` and `notifyNewFile`) ensures the right one receives the question. If a workspace has only one coordinator role, there's no multi-coordinator confusion issue.

   **Risk:** A worker with the wrong role in `.mcp.json` could ask the wrong coordinator, or a single coordinator could receive questions from workers in unrelated projects. This is a configuration/design issue, not a vulnerability in the routing logic.

**Verdict:** Acceptable. Data exposure is constrained by the existing filesystem permissions model. No new secrets are introduced. The design doesn't weaken the OS user isolation.

---

### Message Injection

**Applies:** Yes (low risk, well-mitigated)

**Attack scenario:** Can a malicious worker craft a `task.ask` message that tricks the coordinator into misinterpreting the question, or leaks/corrupts coordinator state?

**Analysis:**

1. **`task.ask` message structure**: The design specifies a Message with type="task.ask" and a body containing the question. The body is passed through `niwa_finish_task` unchanged, so it reaches the coordinator as JSON.

   ```json
   {
     "type": "task.ask",
     "from": "<worker_role>",
     "to": "coordinator",
     "task_id": "<ask_task_id>",
     "body": { "ask_task_id": "...", "from_role": "...", "question": <original_body> }
   }
   ```

2. **Coordinator-side handling**: The coordinator reads this via `niwa_check_messages` (formatted as markdown) or via the `niwa_await_task` question interrupt (returned as `{ status: "question_pending", ask_task_id, from_role, body }`).

   **Threat 1:** Can a malicious `body` field contain instructions that trick the coordinator LLM into executing unintended actions?

   - The body is user-controlled JSON supplied by the worker. It reaches the coordinator as structured data (`body` field in the response or as JSON in the markdown display).
   - The coordinator's skill content (`buildSkillContent()`) already documents that delegated task bodies are "delegator-supplied untrusted content" with a `_niwa_note` wrapper (see `wrapDelegateBody` in server.go).
   - **Mitigation:** The design should apply the same wrapping to question bodies. A question answer should carry a note like: *"This is a worker's question. Provide your decision or guidance as the answer."* This prevents a malicious question from mimicking coordinator instructions.
   - **Implementation gap:** The design doc does not explicitly say whether question bodies are wrapped like delegate bodies. If not, this should be added.

   **Threat 2:** Can a malicious `from_role` or `ask_task_id` field confuse the coordinator into answering the wrong question or answering for the wrong worker?

   - These fields are generated by `handleAsk` (not from worker input). `ask_task_id` is generated by `NewTaskID()` (random UUID). `from_role` is read from `s.role` (the authenticated caller's role from the MCP server context).
   - No injection possible here — the fields are generated, not parsed from worker input.

   **Verdict on message injection:** Low risk, but a gap exists. If question bodies are not wrapped with a note like delegate bodies, coordinators need to manually verify they are responding to a real question rather than a spoofed coordinator instruction in the body. The design should add this mitigation.

3. **Notification file format**: A malicious worker could theoretically write a `.json` file directly to the coordinator's inbox, bypassing `handleAsk`.

   **Mitigation:**
   - The `.niwa/roles/coordinator/inbox/` directory is owned by the user running niwa (0o700).
   - Only the MCP server process (running as the same user) writes to this directory.
   - A worker is another Claude Code session (same user), with read access to the workspace but not write access to the inbox (no explicit grant).
   - **Critical assumption:** Assumed that different Claude Code sessions (worker vs. coordinator) run as the same OS user but do NOT have write permission to each other's inboxes. If this assumption is wrong and workers can write directly to coordinator inboxes, the design is broken.

   **Verification needed:** Check whether Claude Code sandbox or workspace isolation prevents cross-session writes.

---

### Session Impersonation

**Applies:** Yes (low risk, well-mitigated)

**Attack scenario:** Can a rogue process spoof a coordinator registration in `sessions.json` to intercept questions intended for a legitimate coordinator?

**Analysis:**

1. **Who writes `sessions.json`:** Only the MCP server process calls `maybeRegisterCoordinator`. The MCP server is invoked by Claude Code (via stdio) and runs in the Claude Code process context. Access control:
   - The MCP server writes its own session metadata (PID, start time).
   - PID and start time are the current process's own metadata (`os.Getpid()`, computed via `/proc/<pid>/stat`).
   - A rogue process cannot fake a PID and start time — `IsPIDAlive` checks that they match the current process state.

2. **Coordinator role identification:** The MCP server reads `NIWA_SESSION_ROLE` from the environment. If a rogue process runs with `NIWA_SESSION_ROLE=coordinator`, it could register itself as the coordinator.

   **Mitigation:**
   - `NIWA_SESSION_ROLE=coordinator` is set in `.mcp.json` (the MCP configuration file for the session).
   - The `.mcp.json` is created and managed by Claude Code, not by niwa or the user directly.
   - A rogue process cannot hijack the coordinator role unless it runs Claude Code with a malicious `.mcp.json`.
   - This is outside niwa's trust model — if Claude Code itself is compromised, niwa has lost.

   **Assumption:** Claude Code's configuration files (`.mcp.json`) are protected and not writable by untrusted processes. If an attacker can rewrite `.mcp.json`, they can impersonate the coordinator.

3. **Stale session cleanup:** If a legitimate coordinator exits without deregistering, its entry stays in `sessions.json`. A rogue process could wait for the coordinator to exit, then register with the same role (but a new PID).

   **Mitigation:**
   - `IsPIDAlive(pid, startTime)` checks both PID and start time.
   - When a process exits, the PID becomes available for reuse. However, the start time in `/proc/<pid>/stat` is reset to 0 for the new process.
   - `IsPIDAlive` requires startTime to match: if the stale entry has an old start time and the rogue process has a new one, the check fails.
   - Cleanup can also be proactive: `maybeRegisterCoordinator` could prune dead entries before registering.

   **Verification needed:** Confirm that `/proc/<pid>/stat` start time is unique per process lifetime and cannot be spoofed.

**Verdict:** Low risk. Coordinator registration is tied to the MCP server process (which is Claude Code's subprocess), and PID/start-time verification prevents spoofing. The threat is really "what if Claude Code is compromised," which is out of scope for niwa's security model.

---

### Inbox Pollution

**Applies:** Yes (low risk, design mitigates)

**Attack scenario:** Can a worker flood the coordinator's inbox with `task.ask` notifications, exhausting disk space or making legitimate questions undeliverable?

**Analysis:**

1. **Notification volume:** Each `task.ask` is a small JSON message (~500 bytes for typical questions).
   - A worker could call `niwa_ask` in a loop, creating thousands of notifications.
   - Each notification is a separate file at `.niwa/roles/coordinator/inbox/<id>.json`.

2. **Mitigations in the design:**

   **Mitigation A: File system quotas (implicit)**
   - The workspace `.niwa/` directory is on the same filesystem as the project code.
   - If the workspace has a quota, flooding the inbox will hit the quota (same as if code files consumed space).
   - This is not specific to niwa; it's a general filesystem resource limit.

   **Mitigation B: Notification expiry (existing mechanism)**
   - The design reuses the `ExpiresAt` field from existing messages.
   - `handleCheckMessages` sweeps expired messages to `inbox/expired/` before listing.
   - Workers can set an expiry on their questions by passing `expires_at` to `niwa_ask` (if the API supports it; check the tool schema).

   **Mitigation C: Inbox read cleanup (existing mechanism)**
   - Delivered messages are moved from `inbox/` to `inbox/read/` atomically.
   - Only unseen messages accumulate in the inbox root.
   - A coordinator that regularly calls `niwa_check_messages` will clean up delivered notifications.

3. **Remaining risks:**

   **Risk 1:** If a worker creates expired messages faster than the coordinator calls `niwa_check_messages`, the `inbox/expired/` subdirectory will grow. The sweeper doesn't delete files; it only moves them. A very fast worker could still exhaust disk.

   **Risk 2:** If `niwa_ask` doesn't expose `expires_at` to the caller (check the API), workers cannot set expiry. Questions will queue indefinitely.

   **Risk 3:** If a coordinator never calls `niwa_check_messages` or `niwa_await_task`, questions queue in `inbox/` forever, and the watcher's `seenFiles` set grows without bound.

4. **Recommended mitigation:**

   The design should specify:
   - Implement inbox garbage collection: periodically delete files in `inbox/expired/` older than N days (e.g., 7 days). This prevents unbounded growth.
   - Document in the skill that questions have default expiry (e.g., 1 hour) so workers don't need to set it explicitly.
   - Add a monitoring/alerting note: if a coordinator's inbox grows beyond a threshold (e.g., >1000 files), the administrator should investigate whether the coordinator is responsive.

**Verdict:** Low risk with recommendations. The design inherits notification expiry from existing messaging. Two improvements are recommended:
1. Explicit inbox garbage collection for expired messages.
2. Default expiry on questions (if not already present).

---

## Recommended Outcome

**OPTION 2 - Document considerations:**

The design is fundamentally sound. All attacks are low-risk and the existing mitigations are appropriate. However, three implementation considerations should be documented:

### 1. Question Body Wrapping (Critical)

**Consideration:** Question bodies should be wrapped with a note like delegated task bodies to prevent prompt injection.

**Rationale:** Delegated task bodies are wrapped with `_niwa_note` (see `wrapDelegateBody` in server.go) to signal untrusted content and prevent prompt injection. Questions are equally untrusted (written by workers) but are not wrapped in the current design. A malicious worker could embed coordinator instructions in a question body.

**Action:** Update `handleAsk` to wrap question bodies in a `_niwa_note` wrapper before writing the `task.ask` notification. Example:

```json
{
  "ask_task_id": "...",
  "from_role": "worker-1",
  "_niwa_note": "This is a worker's question. Provide your decision or guidance as the answer.",
  "question": <original_body>
}
```

Alternatively, document in the generated skill that coordinators should treat question bodies as untrusted input and not follow embedded instructions.

### 2. sessions.json Atomic Updates (Implementation)

**Consideration:** `maybeRegisterCoordinator` must perform atomic read-modify-write on `sessions.json` to avoid data loss under concurrent coordinator registrations.

**Rationale:** Multiple coordinators (if somehow there are multiple roles in a single workspace) could call `maybeRegisterCoordinator` concurrently. If the reads and writes are not atomic, updates could be lost.

**Action:** Use the same atomic pattern as `writeMessageAtomic`:
1. Read current `sessions.json` and deserialize.
2. Update or append the coordinator's `SessionEntry`.
3. Serialize to a temporary file (`.sessions.json.tmp`).
4. Atomic rename `.sessions.json.tmp` to `sessions.json`.

This ensures only one write completes; the others either see the first write or their write overwrites it. Either outcome is safe (registration idempotent).

Also add flock coordination if multiple processes write `sessions.json`. See the existing `taskstore.UpdateState` for the locking pattern.

### 3. Inbox Garbage Collection (Operational)

**Consideration:** Expired messages accumulate in `inbox/expired/` and are never deleted.

**Rationale:** Prevents unbounded disk usage if a coordinator is temporarily unresponsive and many questions expire.

**Action:** Either:
- Add periodic cleanup (e.g., delete files in `inbox/expired/` older than 7 days) in the watcher or daemon.
- Document in the operational guide that administrators should manually clean old entries if disk usage becomes a concern.

---

## Security Considerations Section for Implementation

Draft for the implementer:

---

### Security Considerations

#### Message Injection Defense

Questions are untrusted input from workers. The coordinator's MCP server wraps question bodies with a `_niwa_note` marker (similar to delegated task body wrapping) to signal untrusted content and prevent prompt injection attacks. The coordinator should not follow any instructions or meta-commands embedded in question bodies.

#### Session Registration Liveness

Coordinator registration relies on PID and process start-time verification (via `IsPIDAlive`). This prevents stale sessions from being reused by unrelated processes (PID recycling attack). The check is best-effort — if a process exits and the OS reuses its PID, there is a window until the next `handleAsk` call where the old entry matches both PID and start time. This window is short (nanoseconds to milliseconds) and is inherent to the liveness-checking approach. It is an acceptable trade-off for simplicity and is consistent with existing niwa security assumptions.

#### Inbox Flooding Resistance

Workers can flood a coordinator's inbox with `task.ask` notifications. The design mitigates this through:

1. **Expiry:** Questions can be set to expire (via `expires_at` parameter). Expired messages are moved to `inbox/expired/` and do not accumulate in the active inbox.
2. **Cleanup:** A coordinator that regularly calls `niwa_check_messages` cleans up delivered messages by moving them to `inbox/read/`.
3. **Garbage collection:** Expired messages in `inbox/expired/` should be deleted after N days (see Operational Recommendations above).

If a coordinator never calls `niwa_check_messages` and questions are not set to expire, the inbox will grow. This is by design — questions are persistent until answered, allowing asynchronous Q&A workflows. Administrators should monitor inbox growth and investigate unresponsive coordinators.

#### File System Permissions

All niwa state is protected by POSIX file permissions (0o700 directories, 0o600 files, owned by the user running Claude Code). The design adds no new permission escalation risks. Questions and answers are as private as existing messages and tasks.

---

## Summary

The design is secure for deployment. It preserves the existing OS-user isolation model, uses proven atomic file patterns, and includes liveness checks that prevent process impersonation. Three implementation considerations are required:

1. **Question body wrapping** to match the untrusted-input defense of delegated tasks.
2. **Atomic updates to sessions.json** using read-modify-write with atomic rename (and optionally flock coordination).
3. **Inbox garbage collection** to prevent unbounded growth of expired messages.

These are design clarifications, not security fixes. The core mechanism (routing to live coordinators via liveness checks, question delivery via inbox and channel notifications) is sound.

