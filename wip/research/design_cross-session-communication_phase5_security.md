# Security Review: cross-session-communication

## Dimension Analysis

### External Artifact Handling

**Applies:** No

The design does not download, fetch, or execute external artifacts. All inputs
are local filesystem reads: JSON message files written by sibling processes
within the same workspace instance directory. The `claude --resume <session-id>`
invocation passes a string sourced from `sessions.json`, but it doesn't fetch
anything from the network. Message files are parsed as JSON (Go stdlib
`encoding/json`), which is memory-safe and does not evaluate content as code.

No mitigations required for this dimension.

---

### Permission Scope

**Applies:** Yes — Medium severity

**Risks:**

1. The daemon (`niwa mesh watch`) runs with the full permissions of the user who
   ran `niwa apply`. It has read/write access to the entire workspace instance
   directory and can invoke `claude --resume`, which in turn may access any tool
   or file the Claude Code session can reach.
2. `InstallChannelInfrastructure` writes `.claude/.mcp.json` into the workspace,
   registering the MCP server. Any user who can write to the instance directory
   can modify this file and redirect the MCP endpoint — potentially pointing
   Claude sessions at a different server.
3. The daemon is spawned with `Setsid: true`, detaching it from the controlling
   terminal. It continues running after the user's shell exits, which is
   intentional, but means a compromised daemon persists beyond the session.

**Severity:** Medium

**Mitigations:**

- The `.niwa/` directory and `.claude/` directory should be created with mode
  `0700` (owner-only). Any world-readable instance directory lets other local
  users inspect `sessions.json` (which contains PIDs and Claude session IDs)
  and read message inboxes.
- Document that the feature only works correctly for single-user machines or
  within user-namespaced environments. Multi-user systems where the home
  directory is shared (e.g., shared dev containers) require additional OS-level
  access controls.
- Consider chmod-enforcing the PID file and `sessions.json` to `0600` at write
  time, regardless of umask.

---

### Supply Chain or Dependency Trust

**Applies:** Partial — Low severity

The design adds `fsnotify` as the only new dependency. `fsnotify` is already in
`go.mod` per the design's own statement, so no new supply chain surface is
introduced. The `claude --resume` binary is assumed to already be present in
`PATH`; the design doesn't download it.

**Residual concern (Low):** If `PATH` is manipulated by a malicious process
before the daemon is spawned, `exec.Command("claude", "--resume", ...)` could
resolve to a different binary. This is standard Go `os/exec` behavior and not
specific to this design, but worth noting given the daemon runs detached.

**Mitigation:** Resolve the `claude` binary path at daemon spawn time using
`exec.LookPath` and store the absolute path in the PID file or pass it as an
argument to `mesh watch`, so the daemon uses a fixed path rather than a
PATH-dependent lookup on each invocation.

---

### Data Exposure

**Applies:** Yes — Medium severity

**Risks:**

1. `sessions.json` stores Claude session IDs, PIDs, repo directories, and
   registration timestamps. Claude session IDs are bearer-like tokens — anyone
   who reads this file and can invoke `claude --resume <id>` can impersonate
   the session.
2. Message bodies in the inbox are plaintext JSON files. If workspace tasks
   involve secrets (API keys, tokens passed as task context), those values
   persist on disk in `inbox/` and `inbox/read/` until explicitly deleted.
3. The two-level PPID walk for Claude session ID discovery reads
   `~/.claude/sessions/<ppid>.json`, which may contain session metadata beyond
   what's needed. The design cross-checks the `cwd` field, which is good, but
   the fallback mtime-sorted scan of `~/.claude/projects/` reads all session
   files until a match is found.

**Severity:** Medium

**Mitigations:**

- Set file mode `0600` on all files created in `.niwa/sessions/` (inbox files,
  `sessions.json`).
- Implement a message TTL: the `ExpiresAt` field exists on the `Message` struct.
  The daemon or `watchInbox` should delete expired messages from `inbox/read/`
  on a periodic sweep rather than retaining them indefinitely.
- The `read/` subdirectory should be treated as a short-term audit trail, not
  permanent storage. Document a maximum retention window (e.g., 24 hours) and
  implement cleanup in the daemon.
- Avoid logging full message body content in any debug/trace output. Log message
  IDs and types only.

---

### Process Injection / Command Injection

**Applies:** Yes — High severity

**Risks:**

1. `claude --resume <session-id>` constructs a command where `<session-id>` is
   sourced from `sessions.json`, which is written by `niwa session register`.
   The session ID is discovered from environment variables or the Claude
   filesystem. If `CLAUDE_SESSION_ID` is attacker-controlled (e.g., a malicious
   `.envrc` or shared `.env` file in the workspace config), the value flows
   directly into `exec.Command` arguments.

   With Go's `exec.Command`, arguments are passed as a slice — not through a
   shell — so classic shell metacharacter injection (`; rm -rf ~`) does not
   apply. However, if the session ID is used as a filesystem path component
   elsewhere (e.g., `~/.claude/sessions/<session-id>.json`), a path traversal
   payload (`../../etc/passwd`) could redirect reads to arbitrary files.

2. Message `Type`, `From`, `To`, `TaskID`, and `Body` fields are read from
   inbox JSON files written by `niwa_send_message`. These fields could carry
   path traversal sequences if any of them are used to construct file paths
   (e.g., a type-based routing directory).

**Severity:** High for path traversal; Low for shell injection (Go exec is
arg-safe).

**Mitigations:**

- Validate `ClaudeSessionID` at registration time against a strict allowlist
  pattern (e.g., `^[a-zA-Z0-9_-]{8,128}$`). Reject and log a warning for any
  value that doesn't match. Do not write invalid values to `sessions.json`.
- When using `ClaudeSessionID` to construct filesystem paths, use
  `filepath.Join` and then verify the result is still under the expected base
  directory (`strings.HasPrefix(resolved, base)`).
- Validate all `Message` fields that could be used as path components with the
  same pattern check. The `Type` field in particular looks like a routing key
  (`question.ask`) and should be validated as a constrained namespace.
- Never pass `Body` content to any `exec.Command` call, even indirectly through
  a temp file that's later sourced.

---

### Inter-Process Trust

**Applies:** Yes — High severity

This is the most significant security dimension for this design.

**Risks:**

1. **Unauthenticated inbox.** Any process running as the same user can write
   files into `.niwa/sessions/<uuid>/inbox/`. The `watchInbox` goroutine
   consumes every file that appears there. There is no signature, HMAC, or
   shared secret on messages. A malicious or compromised process on the same
   machine can inject arbitrary messages into any session's inbox — including
   fake `question.answer` replies that resolve a `niwa_ask` waiter with
   attacker-controlled content.

2. **Session impersonation.** `sessions.json` maps UUIDs to session metadata.
   Any local process can read this file (unless mode `0600` is enforced — see
   Data Exposure) and target a specific session's inbox. Combined with the lack
   of message authentication, an attacker process can craft a reply that matches
   a pending `reply_to` waiter and inject an arbitrary answer into a blocking
   `niwa_ask` call.

3. **Daemon trust.** The daemon calls `claude --resume` on behalf of sessions.
   If an attacker can write to `sessions.json` or inject a new entry, they can
   cause the daemon to resume a session ID they control, effectively hijacking
   the next wakeup call.

4. **MCP server trust.** The MCP server is registered in `.claude/.mcp.json`.
   Claude Code trusts tool calls returned by the registered MCP server. If the
   MCP socket or named pipe can be connected to by other processes (depending on
   how the MCP transport is bound), a rogue process could issue tool call
   responses that Claude Code acts on.

**Severity:** High — in a single-user developer environment the practical risk
is low (you'd need a compromised process running as the same user). But the
design is explicitly intended for coordinator/worker agent patterns where
multiple Claude sessions run concurrently and act on message content. A prompt
injection in one session could cause it to send a crafted message that influences
another session's behavior via the inbox.

**Mitigations:**

- **Short term (required):** Enforce `0700` on `.niwa/sessions/` and `0600` on
  all files within it. This reduces the attack surface to same-UID processes
  only (which cannot be fully prevented in a user-mode design).
- **Short term (required):** Add a `sender_uuid` field to `Message` and have
  `watchInbox` verify it matches a `uuid` in `sessions.json` before accepting
  the message. This doesn't cryptographically authenticate, but it gates
  injection to processes that can read `sessions.json` (already owner-only).
- **Medium term (recommended):** Add a per-instance shared secret (random
  128-bit token written to `.niwa/channel.key` at `InstallChannelInfrastructure`
  time, mode `0600`). Include an HMAC-SHA256 of the message fields in a
  `signature` field on `Message`. `watchInbox` verifies the signature before
  routing. The MCP server includes the secret when writing messages; the
  validator checks on read.
- **Document (required):** The design doc and user-facing docs must state that
  this feature's security model assumes single-user machines. Multi-user systems
  or shared containers require OS-level namespace isolation.

---

### File System Race Conditions

**Applies:** Yes — Medium severity

**Risks:**

1. **PID file TOCTOU.** The design writes `pid\nstart-jiffies\n` atomically via
   temp-file rename — that's correct. However, between the `IsPIDAlive` check
   and the `claude --resume` invocation, the process could have died and a new
   unrelated process could have reused the PID. The start-jiffies cross-check
   mitigates this on Linux, but it's not foolproof if jiffies resolution is low.

2. **sessions.json concurrent writes.** `niwa session register` upserts a
   `SessionEntry` in `sessions.json`. The daemon reads `sessions.json` in its
   fsnotify loop. If two sessions register simultaneously (e.g., two worker
   repos both call `session register` at apply time), and both use a read-
   modify-write pattern on `sessions.json`, one write can overwrite the other.
   The design says "upserts" but doesn't specify the locking mechanism.

3. **Inbox atomic rename.** The design states messages are written via atomic
   rename, and the watcher moves files to `read/` atomically. On most Linux
   filesystems, same-directory renames are atomic. Cross-directory renames
   (inbox/ → inbox/read/) are atomic on the same filesystem mount, which is
   guaranteed here since `read/` is a subdirectory of `inbox/`. This is safe.

4. **`niwa destroy` race.** `niwa destroy` sends SIGTERM, waits 5s, then
   SIGKILL before removing the instance directory. If the daemon is in the
   middle of writing to `sessions.json` or moving a file when SIGTERM arrives,
   the partially-written file could corrupt state. The design does not mention
   graceful shutdown of in-flight file operations.

**Severity:** Medium for sessions.json; Low for PID file race (start-jiffies
check); Low for inbox rename (atomic by design).

**Mitigations:**

- Use a file lock (e.g., `flock` via a `.sessions.json.lock` sentinel file or a
  lock file in `.niwa/`) for all reads and writes to `sessions.json`. Both the
  daemon reader and `session register` writer must hold this lock.
- In the daemon's SIGTERM handler, drain and close any in-progress file
  operations before exiting. A simple approach: set a `shutting down` atomic
  flag that causes the fsnotify loop to stop accepting new events, then `sync`
  any open file descriptors before returning.
- Document that the start-jiffies check is a best-effort liveness heuristic,
  not a guarantee, and that the system tolerates false-positive wakeups (calling
  `claude --resume` on a live session is idempotent).

---

## Recommended Outcome

**OPTION 2 - Document considerations:**

The design is architecturally sound for its stated threat model (single-user
developer machine). No dimension requires a blocking design change, but several
require implementation-time mitigations that the implementer must build in from
the start rather than retrofit. The following Security Considerations section
should be added to the design document before implementation begins.

---

### Security Considerations (draft for design doc)

**Threat model.** This feature's security properties hold when all processes
running as the message-passing user are trusted. It does not protect against
a malicious process running as the same UID. Multi-user systems or shared
containers require OS-level namespace isolation outside the scope of this
design.

**File permissions.** All files under `.niwa/sessions/` must be created with
mode `0600` (files) and `0700` (directories). `InstallChannelInfrastructure`
must set these modes explicitly, independent of umask.

**Input validation.** `ClaudeSessionID` must be validated against
`^[a-zA-Z0-9_-]{8,128}$` at registration time. Values that don't match must be
rejected and logged; they must not be written to `sessions.json` or used in
`exec.Command` arguments. All `Message` fields used as path components (`Type`,
`From`, `To`) must be validated against a similarly constrained pattern.

**Message authentication.** Phase 5 should implement HMAC-SHA256 message
signing using a per-instance shared secret at `.niwa/channel.key` (mode `0600`,
generated at `InstallChannelInfrastructure` time). The `niwa_send_message` MCP
tool signs outgoing messages; `watchInbox` verifies signatures before routing.
Unsigned messages must be rejected with a warning. This can be deferred to a
follow-on issue but should be tracked from the start.

**sessions.json concurrency.** All reads and writes to `sessions.json` must be
protected by a file lock (advisory lock on a `.sessions.lock` file in `.niwa/`).
This applies to both `niwa session register` and the daemon's read path.

**Message retention.** Consumed messages in `inbox/read/` must be purged. The
daemon should sweep `read/` on startup and periodically (e.g., hourly) and
delete files older than a configurable TTL (default: 24 hours). Message bodies
must not appear in log output; log IDs and types only.

**Binary path resolution.** The `claude` binary path must be resolved with
`exec.LookPath` once at daemon start and stored as an absolute path. Do not
rely on PATH at each invocation.

**Graceful shutdown.** The daemon's SIGTERM handler must stop accepting new
fsnotify events, complete any in-progress file moves, and sync open file
descriptors before exiting. This prevents partial writes during `niwa destroy`.

---

## Summary

The design is suitable for implementation with no blocking architectural
changes, but it requires several security-hardening measures to be built in
during Phase 1 and Phase 4. The most significant risks are unauthenticated
inter-process message injection (High — mitigated near-term by file permissions
and sender validation, medium-term by HMAC signing), path traversal via
insufficiently validated session IDs and message fields (High — mitigated by
strict allowlist validation at registration time), and concurrent write
corruption of `sessions.json` (Medium — mitigated by file locking). All
mitigations are straightforward to implement in pure Go without new
dependencies.
