# Phase 2 Research: Ops/Reliability Researcher

## Lead 2: Error and Failure Modes

### Findings

#### Scenario 1: Session offline — recipient not running when message is sent

**What happens**: Session A calls the MCP `send_message` tool targeting Session B's role. Session B has no registered entry in `sessions.json`, or its PID fails a `kill(0)` liveness check. The file-based queue still exists as a directory on disk.

**What the system can do**: With a file-based transport, the message can be written to Session B's inbox directory regardless of whether Session B is running. The atomic-rename delivery guarantee holds independently of the recipient's state. The sender gets an immediate "delivered to inbox" acknowledgment, not a "received by session" acknowledgment — a distinction the MCP tool response must make explicit.

**What Claude (sender) sees**: The MCP tool should return a structured result indicating the recipient is not currently active and the message has been queued. Example: `{"status": "queued", "recipient_active": false, "expires_at": "..."}`. Claude can then inform the user or proceed without waiting, depending on whether the message was a blocking question or a fire-and-forget delegation.

**What the human sees**: Nothing automatically — the sender's session continues. If the message was a blocking `question.ask` with `reply_to` correlation, the coordinator session should surface a notice that no live recipient was found and the question is pending.

**What should happen**: Queue and wait, up to the message TTL. Dropping immediately on offline detection would make the system brittle against the common case of sessions starting at slightly different times. Returning an error without queuing would force the sender to implement its own retry loop, which is worse.

**PRD requirement direction**: The system shall accept messages for offline recipients, write them to the recipient's inbox directory via atomic rename, and return a `queued` status (not `delivered`) to the sender. The MCP tool response shall distinguish between `queued` (inbox written, recipient not confirmed active) and `delivered` (inbox written, recipient confirmed active by PID or recent heartbeat).

---

#### Scenario 2: Session crash mid-task — in-flight work lost, coordinator left waiting

**What happens**: Session B received a `task.delegate` message and sent `task.ack`. It began working — possibly reading files, making edits — then crashed: context window overflow, user Ctrl+C, OOM kill, or network disconnect from Anthropic. The coordinator holds a pending task correlation and no further `task.result` ever arrives.

**Coordinator state**: The coordinator sees nothing immediately. Its MCP polling tool returns no new messages in Session B's reply inbox. The `task_id` it's tracking has received a `task.ack` but no `task.result`. After the TTL window elapses with no result, the message expires.

**Session B's inbox state**: Messages that Session B received but did not explicitly acknowledge (move to a "processed" subdirectory) remain in its inbox. On restart, Session B will re-read them — which could mean re-processing a delegation it already started.

**What Claude (coordinator) sees after TTL**: The expiry notification (if implemented) tells the coordinator that a sent message expired. But a `task.delegate` expiry is not the same as a `task.result` expiry — the coordinator's question is "did Session B finish the work?", not "did my delegation message get read?". TTL on the delegation envelope is a poor signal for task completion state.

**What the human sees**: Nothing automatically. The workspace has a zombie task — `task_id` acknowledged but never resolved. The human must inspect the session registry, check Session B's PID, and decide whether to restart the session.

**What the system should do**: The coordinator session needs a way to detect task abandonment, separate from message TTL. This requires tracking task state — not just message delivery. A `task.progress` heartbeat (sent periodically by Session B while working) gives the coordinator a liveness signal beyond the initial `task.ack`. If no heartbeat arrives within a configurable window, the coordinator can surface a warning: "Task task-issue-69 assigned to niwa-worker has not reported progress in 30 minutes. Session B (PID 14322) may have crashed."

**Recovery path for the human**: The user should be able to call `niwa session list` to see stale sessions (PID dead, task in progress) and `niwa task status <task_id>` to inspect what was partially done. Recovery is manual for v1 — the user restarts Session B, which re-reads its inbox and picks up the un-processed delegation.

**PRD requirement direction**: The system shall track task state separately from message delivery state. Sessions shall emit `task.progress` heartbeat messages at a configurable interval (default 10 minutes) while executing a delegated task. The coordinator's MCP tool shall expose a method to query all open `task_id` entries and their last-seen heartbeat timestamp. A task with no heartbeat for more than 2x the heartbeat interval shall be surfaced as `suspect` in the task list.

**Decision needed**: Whether re-delivery of a delegation to a restarted Session B is automatic (the inbox message is still there) or manual (the user must re-delegate). Automatic re-delivery risks re-doing partial work if Session B doesn't have enough context to detect it already started.

---

#### Scenario 3: Message TTL expiry — sender waiting, recipient never reads

**What happens**: Session A sent a `question.ask` with `expires_at` set 1 hour from send time. Session B never polled (crashed, never started, or is overwhelmed with other work). The TTL deadline passes.

**Two sub-cases**:

*Sub-case A: File-based transport, no active expiry enforcement.* The message file sits in Session B's inbox directory indefinitely unless the system actively prunes it. If nothing prunes expired files, the message is effectively delivered the next time Session B polls — potentially hours after the `expires_at`. The sender's correlation ID is still live in its context. This could result in Session A answering a question that was no longer relevant.

*Sub-case B: Active TTL enforcement.* A background process (or lazy expiry on read) marks or removes messages whose `expires_at` has passed before delivery. Session B's MCP polling tool skips or discards expired messages when it scans the inbox directory.

**What Claude (sender) sees**: If sender-side failure notification is a requirement, the system must actively push an expiry event back to Session A — not wait for Session A to poll. The file-based transport has no native push to the sender. The sender's MCP poll tool could check the status of sent messages by scanning a "sent with TTL" log, but this adds complexity.

**What the human sees**: Without active surfacing, nothing. The coordinator session is silently stalled waiting on an answer that will never come. This is the most insidious failure mode — invisible deadlock.

**What the log shows**: If messages are retained (not deleted on expiry), a `niwa mesh log` command can show: "Message q-7f3a2b1c sent by coordinator to niwa-worker at 12:01, expired at 13:01, never read." If messages are deleted, this history is lost.

**What should happen**: Expired unread messages should be moved to an `expired/` subdirectory, not deleted, so they're inspectable. The sender should receive an expiry notification the next time it polls the mesh — even if that's after the fact. If the sender's MCP polling tool returns expiry events alongside new messages, Claude can surface this to the user: "Your question to niwa-worker expired unanswered."

**PRD requirement direction**: The system shall implement lazy TTL enforcement: when Session B's MCP polling tool reads its inbox, it shall skip (and move to `expired/`) any messages whose `expires_at` has passed. The system shall also write an expiry notification to Session A's inbox so that the sender learns of expiry on its next poll. Expiry notifications shall include the original message `id`, `type`, and `sent_at`. Expired messages shall be retained in an `expired/` subdirectory for at least 24 hours, inspectable via `niwa mesh log`.

**Decision needed**: Whether expiry notification to the sender is best-effort (written to sender's inbox, may be read much later) or whether the PRD requires a guarantee that the sender learns within a bounded time. Given polling model, only best-effort is achievable without a daemon.

---

#### Scenario 4: Duplicate roles — two sessions register as the same role

**What happens**: The user opens two terminals in the repo `public/niwa`. Both sessions call the MCP registration tool. Both succeed. `sessions.json` now has two entries with `role: niwa-worker` and different PIDs.

**Routing ambiguity**: When the coordinator sends a message `to: {role: "niwa-worker"}`, which session gets it? The transport has no defined policy. If the system does nothing, the first-registered session gets the message (first-match routing), the second gets nothing, and the human doesn't know.

**What Claude sees in the duplicate session**: The second session registered fine and has no indication it's a duplicate. It will poll for messages, find none (because routing went to session 1), and potentially proceed independently with no coordinator input.

**What the human sees**: Non-obvious divergence. The coordinator may get a `task.ack` from session 1 and a separate, unrequested `status.update` from session 2. Diagnosing this requires the user to run `niwa session list` and notice two entries with the same role.

**Options**:
1. **Error on register**: Reject the second registration if an active session with the same role already exists (PID alive). Return an error that Claude surfaces to the user: "Role niwa-worker is already registered by PID 14322. Set NIWA_SESSION_ROLE to a different value or stop the other session."
2. **Allow with round-robin**: Accept both, route messages round-robin or randomly. Harder to reason about; produces split-brain if both sessions are working independently.
3. **Allow with fan-out**: Deliver directed messages to all sessions matching the role. This turns a point-to-point send into a broadcast, which is semantically wrong for `task.delegate`.
4. **Auto-suffix**: Rename the second registration to `niwa-worker-2` automatically. Ergonomic but surprising.

**What should happen**: Option 1 is correct. Duplicate roles are almost always user error, not intent. The error message must be actionable: tell the user which role is taken, which PID holds it, and how to override with `NIWA_SESSION_ROLE`.

**PRD requirement direction**: The system shall reject registration of a role that is already held by a live session (verified by `kill(0)`). The rejection shall include the conflicting PID and the env variable for override. If the existing session's PID is dead (stale entry), the system shall reclaim the role and register the new session without error (see Scenario 5).

**Decision needed**: Whether the user should be allowed to force duplicate roles for intentional fan-out scenarios (two reviewers, both named `reviewer`). This is an advanced use case that can be deferred to v2, but the schema should not preclude it.

---

#### Scenario 5: Registry corruption / stale entries — crash without clean unregister

**What happens**: Session B crashes — OOM, SIGKILL, power loss. It never called the MCP unregister tool. Its entry in `sessions.json` remains: `{role: "niwa-worker", pid: 14322, registered_at: "..."}`. PID 14322 is now recycled to an unrelated process.

**Discovery behavior**: When a new Session B starts, it calls the register tool. The system reads `sessions.json` and finds an existing entry for `niwa-worker`. It runs `kill(0, 14322)`. The process exists (PID recycled) — this is a false positive. The system incorrectly concludes the old session is alive and rejects the new registration.

**Mitigation for false positives**: A supplementary liveness check beyond `kill(0)`: record the `process start time` (from `/proc/<pid>/stat` on Linux) at registration time. On liveness check, compare the recorded start time against the current process's start time. If they differ, the PID has been recycled and the entry is stale.

**What the human sees without mitigation**: The new session can't register its role and Claude reports an error about a session that isn't running. Confusing and unrecoverable without manual intervention.

**What the registry should do on startup**: When the system reads `sessions.json` and encounters entries, it should proactively prune stale ones using the start-time check (or a simpler: entry age > 24 hours without a heartbeat). Pruning should happen at registration time (lazy) and optionally at `niwa session list` time (explicit).

**Concurrent write risk**: `sessions.json` is written by sessions themselves (no daemon). If Session C is registering while Session D is also registering, both may read the file, modify their in-memory copy, and write back — losing one registration. This is the "lost update" race condition and must be addressed by file locking (advisory `flock` on Linux/macOS) or an atomic compare-and-swap write pattern.

**PRD requirement direction**: The system shall record process start time alongside PID in `sessions.json`. Liveness checks shall compare both PID existence and recorded start time. The system shall prune entries where the PID is dead or the start time differs on every read, before performing any routing lookup. Writes to `sessions.json` shall use advisory `flock` or an atomic rename-from-tempfile pattern to prevent concurrent update loss.

---

#### Scenario 6: Broker/infrastructure unavailable — missing or unwritable inbox

**What happens**: The `.niwa/sessions/` directory doesn't exist (workspace `niwa apply` was never run for the new instance), or it exists but the inbox subdirectory for the target session is missing (session never registered but the coordinator is sending anyway), or the filesystem is full and `rename` fails.

**What Claude (sender) sees**: The MCP `send_message` tool fails. The tool's return value should be a structured error, not a raw Go panic stack. Example: `{"error": "TRANSPORT_UNAVAILABLE", "detail": "Inbox directory for role niwa-worker does not exist. Run niwa apply to provision the messaging layer."}`.

**What should happen**: The MCP tool must distinguish between:
- `RECIPIENT_NOT_REGISTERED` — inbox directory doesn't exist because the session never registered (vs. `RECIPIENT_OFFLINE` where the inbox exists but the PID is dead)
- `INBOX_UNWRITABLE` — inbox exists but the atomic rename failed (filesystem full, permissions error)
- `MESH_NOT_PROVISIONED` — the entire `.niwa/sessions/` directory is absent (niwa apply was not run)

Each error code tells Claude something different. `MESH_NOT_PROVISIONED` means "tell the user to run niwa apply." `INBOX_UNWRITABLE` means "there's a filesystem problem." `RECIPIENT_NOT_REGISTERED` means "the target session hasn't started yet."

**What the human sees**: Claude surfaces the structured error message. For `MESH_NOT_PROVISIONED`, the human sees an actionable instruction. For `INBOX_UNWRITABLE`, the human sees a system-level problem.

**PRD requirement direction**: The MCP send tool shall return structured error objects with a machine-readable `error_code` field and a human-readable `detail` string. The system shall define at minimum these error codes: `MESH_NOT_PROVISIONED`, `RECIPIENT_NOT_REGISTERED`, `RECIPIENT_OFFLINE` (inbox exists, PID dead), `INBOX_UNWRITABLE`, `MESSAGE_TOO_LARGE`. All errors shall be non-fatal to the calling session — Claude shall be able to continue other work after a send failure.

---

#### Scenario 7: Message ordering — two senders write simultaneously

**What happens**: Session A and Session C both send messages to Session B at nearly the same instant. Both call `rename` into Session B's inbox directory with different filenames (e.g., `<uuid-A>.json` and `<uuid-C>.json`).

**What POSIX guarantees**: `rename` is atomic per the POSIX spec. The two renames are independent filesystem operations. Both succeed without conflict. The inbox directory will contain both files. Which file "arrived first" depends on the order the kernel processed the renames — not on the logical send order.

**What Session B's polling sees**: Session B reads the inbox directory and processes files in filesystem listing order (typically inode order or modification time order, not arrival order). If Session B sorts by `sent_at` in the message envelope, it gets logical send time order. If it sorts by file modification time, it gets approximate arrival order.

**Does ordering matter?**: For most message types, no. A `status.update` and a `task.delegate` from different senders to the same recipient are independent. For `question.answer` correlating by `reply_to`, ordering doesn't matter — the recipient matches by ID.

**The one case where ordering matters**: If two coordinators simultaneously delegate the same `task_id` to the same worker (which shouldn't happen if roles are unique), the worker could process one and miss the other. This is prevented by role uniqueness enforcement (Scenario 4).

**PRD requirement direction**: The system shall not guarantee delivery order between messages from different senders. Sessions shall sort received messages by `sent_at` timestamp in the envelope before processing. The PRD shall document that ordering is best-effort by `sent_at` and that applications requiring strict ordering must include sequence numbers in the `task_id` or `body`. The spec shall note that same-sender ordering is preserved (a single session's sends are sequential).

---

#### Scenario 8: Large messages — code diffs, long descriptions, review feedback

**What happens**: A coordinator sends a `review.feedback` message with a full diff of a 2,000-line file, or a `task.delegate` with a detailed description including an entire specification document. The message payload is 500 KB. The receiving session's MCP poll tool reads this into Claude's context.

**Context window impact**: A 500 KB message injected into Claude's context window consumes significant token budget. For Claude Sonnet, that's roughly 125,000 tokens — a meaningful fraction of the available window. For a session already deep in a task, this could push it over the limit and trigger context compaction, which may lose task context.

**File write cost**: A 500 KB file rename is cheap. Disk space for the `.niwa/sessions/` directory is a non-issue at human interaction speeds.

**What Claude (recipient) sees**: The full payload appears in its context. If it's too large, the session's context management may truncate or compress it. Important routing metadata in the envelope (e.g., `reply_to`, `task_id`) must appear at the start of the envelope so it's preserved even if the `body` is compressed.

**What should happen**: Messages should have a size limit. Beyond that limit, the sender should write the large content to a file in the shared workspace and put the file path in the message body instead of the content itself. The message schema already supports this pattern — the `body` field is free-form, so `body.diff_path` pointing to a file at `{instance_root}/.niwa/mesh/artifacts/<uuid>.diff` is straightforward.

**Size limit selection**: 64 KB is a reasonable default — large enough for typical review comments (a few hundred lines), small enough to stay well within any reasonable context budget. Messages between 64 KB and a hard cap (e.g. 1 MB) should be accepted but the MCP tool should emit a warning. Messages above the hard cap should be rejected with `MESSAGE_TOO_LARGE` and instructions to use the artifact path pattern.

**PRD requirement direction**: The system shall enforce a soft message size limit of 64 KB and a hard limit of 1 MB on message file size. Messages exceeding the soft limit shall trigger a warning in the MCP tool response. Messages exceeding the hard limit shall be rejected with error code `MESSAGE_TOO_LARGE`. The `body` schema for message types that may carry large content (`task.delegate`, `review.feedback`) shall include an optional `artifact_path` field pointing to a file in `{instance_root}/.niwa/mesh/artifacts/`. The system shall provision the `artifacts/` subdirectory alongside the inbox directories.

---

### Implications for Requirements

**The PRD shall require:**

1. The MCP `send_message` tool shall write messages to recipient inbox directories via atomic rename and return a `status` field of either `queued` (recipient offline) or `delivered` (recipient confirmed active). The distinction is based on PID liveness at send time, not on read confirmation.

2. When a message's `expires_at` passes before it is read, the system shall move the message to an `expired/` subdirectory and write an expiry notification to the sender's inbox on the next read cycle. Expiry notifications shall include the original `id`, `type`, and `sent_at`.

3. Expired messages shall be retained in `expired/` for a minimum of 24 hours and shall be inspectable via a `niwa mesh log` command or equivalent.

4. Session registration shall reject any role that is already held by a live session (PID alive and start time matching). The rejection error shall include the conflicting PID and the `NIWA_SESSION_ROLE` environment variable for override.

5. Liveness checks shall use both PID existence (`kill(0)`) and process start time from `/proc/<pid>/stat` (Linux) or `sysctl` (macOS) to detect recycled PIDs. Entries with dead or recycled PIDs shall be pruned automatically on every read.

6. All writes to `sessions.json` shall be protected by advisory file locking (`flock` or equivalent) or an atomic compare-and-swap write (write to temp file, rename) to prevent concurrent update loss.

7. The MCP `send_message` tool shall return structured error objects with a machine-readable `error_code` field. Defined error codes: `MESH_NOT_PROVISIONED`, `RECIPIENT_NOT_REGISTERED`, `RECIPIENT_OFFLINE`, `INBOX_UNWRITABLE`, `MESSAGE_TOO_LARGE`. All errors shall be non-fatal to the calling session.

8. The system shall not guarantee delivery order between messages from different senders. The MCP polling tool shall sort received messages by `sent_at` before returning them. The documentation shall state this explicitly.

9. The system shall enforce a soft message size limit of 64 KB (warning) and a hard limit of 1 MB (rejected with `MESSAGE_TOO_LARGE`). The `artifacts/` subdirectory shall be provisioned alongside inboxes for large-payload reference passing.

10. Sessions executing a delegated task shall emit `task.progress` messages at a configurable interval (default 10 minutes). The coordinator's task-status query method shall surface tasks with no heartbeat within 2x the interval as `suspect`.

11. The MCP registration tool shall return a structured error when called in a workspace where `niwa apply` has not been run (`.niwa/sessions/` absent), with a human-readable instruction to run `niwa apply`.

12. The `niwa session list` command shall show, for each registered session: role, PID, liveness status (live/dead/suspect), last heartbeat timestamp, and any open task IDs.

---

### Open Questions

These are decisions where the correct answer isn't obvious and require a product owner call:

**Q1: Automatic re-delivery after session restart (Scenario 2)**
When Session B restarts after a crash, its inbox still contains unprocessed messages including the original `task.delegate`. Should the system automatically surface these as "pending" on restart, or should the user explicitly restart the task? Automatic re-delivery risks re-executing partially completed work. Manual re-delivery requires the user to know there were pending messages. One approach: the MCP registration tool, on detecting a prior inbox with unprocessed messages, should tell Claude during session startup: "You have 1 unread message from before your last session: [summary]." This requires the MCP registration and polling tools to be called at session startup, which may not always happen.

**Q2: Blocking questions and coordinator stalls (Scenario 2)**
Should the coordinator be able to mark a `question.ask` as `blocking: true`, meaning it cannot proceed until an answer arrives? If yes, the coordinator's MCP polling must support a blocking wait (with timeout) rather than just returning immediately if the inbox is empty. Blocking waits complicate the pull model and may cause Claude's tool call to time out. Alternatively, the coordinator can use a non-blocking poll loop with an explicit "I'm waiting for an answer" state in its context — which is workable but puts the burden on Claude's reasoning.

**Q3: Expiry notification delivery guarantee (Scenario 3)**
Best-effort expiry notification (write to sender's inbox, may be read much later) means the coordinator could remain stalled for a long time after TTL expiry if it doesn't poll frequently. Should the system require coordinators to poll at a minimum frequency (e.g., every 5 minutes) as a PRD requirement? Or should the product accept that the human must manually check on stalled coordinators?

**Q4: Intentional duplicate roles (Scenario 4)**
Is there a valid use case for two sessions with the same role (e.g., two `reviewer` sessions processing review requests in parallel)? If yes, the PRD needs a fan-out delivery model for directed messages to roles, and the message schema needs to support "deliver to any one session with this role" vs. "deliver to all sessions with this role." This is a materially different routing model and should not be designed in v2 unless v1 has confirmed no demand.

**Q5: Cross-platform process start time (Scenario 5)**
Linux provides `/proc/<pid>/stat` for process start time. macOS requires `sysctl`. Windows has different APIs. The PRD should specify the platform scope for v1 (same-machine, Linux/macOS only?) and whether process start time liveness is required or optional (fallback to kill(0) only). This has implementation cost implications.

**Q6: Artifact cleanup ownership (Scenario 8)**
Who deletes files in `.niwa/mesh/artifacts/`? The sender writes them; the recipient reads them. If the recipient crashes before reading, the artifact lingers. If the sender deletes after the TTL, the recipient may try to read a deleted file. A `niwa mesh gc` command (garbage collect artifacts and expired messages older than N days) seems necessary — but who runs it, and when?

**Q7: Task state tracking scope (Scenario 2)**
`task.progress` heartbeats require sessions to call an MCP tool repeatedly during long-running work. This means Claude must remember to do so as part of its execution loop, which is an instruction that must appear in the workspace CLAUDE.md or in a hook. Should this be enforced by a hook that fires periodically and reminds Claude to emit a heartbeat, or is it purely advisory (documented convention that Claude agents are expected to follow)? Hooks can enforce this but add complexity to the provisioning layer.

---

## Summary

The eight failure scenarios reveal two dominant reliability risks: invisible deadlock (coordinator stalled waiting on messages that expired or recipients that crashed, with no automatic surfacing to the user) and stale registry poisoning (crashed sessions leaving PID entries that block role re-registration). Both risks are addressable with concrete mechanisms — lazy TTL enforcement with expiry notifications, process start-time liveness checks, and file locking for registry writes — but both require active implementation effort rather than benign-default behavior. The largest product decision is whether task progress tracking (heartbeats from worker sessions) is a mandatory protocol requirement or an advisory convention: mandatory gives the coordinator reliable abandonment detection but requires every Claude session to follow a structured emission loop; advisory is easier to implement but leaves the coordinator blind to crashes until the human notices. Three questions — automatic re-delivery after crash, blocking question semantics, and artifact lifecycle ownership — have no obvious answer and need explicit product owner decisions before the PRD can be written to the implementation level.
