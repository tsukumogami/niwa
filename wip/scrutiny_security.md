# Security review — cross-session-communication branch

Scope: `internal/mcp/{audit,audit_reader,server}.go`, `internal/cli/mesh_watch.go`
inbox-ownership fix, and the `--allowed-tools / acceptEdits` test harness.

## Findings

### H1 — Audit log is forgeable by any same-uid worker. HIGH
Diagnosis: `mcp_serve.go:23-30` reads `NIWA_SESSION_ROLE`/`NIWA_TASK_ID` from
env. A prompt-injected worker can spawn its own `niwa mcp-serve` with arbitrary
role and call `niwa_send_message` (no sender-side auth, `server.go:566-633`),
producing audit lines `role=coordinator, ok=true`.
`theCoordinatorEmittedDelegateCallsForRoles` filters on `okOnly:true` and
trusts the recorded role, so fake-coordinator entries can satisfy the
graph-e2e check without the real coordinator running.
Mitigation: bind audit `role`/`task_id` to a daemon-issued spawn token (or
include verified PPID/start-time and have the reader reject mismatches), reusing
the executor-authz trust ceiling.

### H2 — `extractErrorCode` never matches production errors. HIGH (forensics)
Diagnosis: `errResultCode` emits text `"error_code: BAD_TYPE\ndetail: ..."`
(`server.go:788-793`); `errCodeRE = ^([A-Z][A-Z_]*[A-Z])` (`audit.go:152`)
requires uppercase at offset 0. Every failed call is logged with
`error_code="ERROR"` — Decision 5 of the design (preserve niwa codes) is a
no-op. `audit_test.go:49-51` tests synthetic `"NOT_TASK_PARTY: ..."` text that
no handler ever produces.
Mitigation: parse the structured `error_code: <CODE>` line (mirror
`errorCodeFromText` at `server.go:808-819`) instead of the prefix regex.

### M1 — Worker blast radius via `acceptEdits`. MEDIUM
Diagnosis: `--permission-mode=acceptEdits` (`mesh_watch.go:865`) auto-approves
Read/Write/Edit anywhere the uid can reach — not just the role's CWD. A
prompt-injected worker can edit `.niwa/mcp-audit.log`, peer role inboxes,
`.niwa/tasks/*/state.json`, or anything else under `~`. The audit log aids
forensics only against non-tampering attackers (H1).
Mitigation: sandbox the worker FS (bubblewrap/landlock or at least a chroot
under the role's repo); document audit's honest-worker trust model; consider
`chattr +a` on `mcp-audit.log` if the daemon runs with `CAP_LINUX_IMMUTABLE`.

### M2 — Forged terminal messages wake `niwa_await_task` early. MEDIUM
Diagnosis: `watcher.go:notifyNewFile` dispatches any inbox file whose `type` is
`task.completed/abandoned/cancelled` to `awaitWaiters[task_id]`. A peer (or any
same-uid process) can drop such a file in another role's inbox, waking a sync
`niwa_delegate`/`niwa_await_task`. `formatEventResult` re-reads `state.json`
(`handlers_task.go:723`) so the fabricated `result` is dropped, but the caller
is unblocked early with a non-terminal status. The forged file is then renamed
into `read/` and lost.
Mitigation: require sender role to match `state.json.worker.role` for that
`task_id` before dispatch; verify the task exists locally.

### M3 — `daemonOwnsInboxFile` reads via `os.ReadFile` (no `O_NOFOLLOW`). MEDIUM
Diagnosis: `mesh_watch.go:698` follows symlinks. Same-uid attacker can plant a
symlink in another role's inbox pointing outside `.niwa/`. Today the daemon
only parses-and-decides (rename targets the symlink itself), so impact is
limited to opening arbitrary files for read; it becomes a real disclosure
gadget if a future change ever logs body content.
Mitigation: `os.OpenFile(O_RDONLY|O_NOFOLLOW)` and read from the fd, matching
`audit_reader.go:29`.

### L1 — Allowed-tools list drift across three call sites. LOW
Diagnosis: `mesh_watch.go:93-105`, `channels.go:54-66`, and
`mesh_steps_test.go:1200-1212` each redeclare the canonical 11-tool list.
Server adds a tool / one list misses it → worker silently hangs on the first
prompt until the stall watchdog fires (~15 min). DoS, not a privilege issue.
Mitigation: export a single slice from `internal/mcp` and assert it against
`tools/list` in a unit test.

### L2 — No audit-log rotation. LOW
Diagnosis: design accepts unbounded growth. A wedged worker retrying a failing
tool call inside the 15-min watchdog window can write tens of thousands of
NDJSON lines. Not a safety risk but fills disk on long-lived instances.
Mitigation: rotate on `niwa apply` or daemon startup; cap per-task count.
