# Security Review: mesh-session-lifecycle

## Dimension Analysis

### External Artifact Handling
**Applies:** No

This feature introduces no new external artifact handling. It does not download
binaries, fetch remote configurations, or process inputs from outside the local
machine. Session creation runs `git worktree add` against the local repository,
which uses the already-configured git remote and existing credentials. The
design introduces no new ingress points for external data — all inputs (session
ID, purpose, repo name, task envelopes) originate from the local coordinator
process over the existing stdio MCP channel.

No validation gaps from external sources are introduced.

---

### Permission Scope
**Applies:** Yes

**Filesystem writes expand in two ways:**

First, `niwa_create_session` calls `git worktree add` and then writes daemon
state files under `<instance>/.niwa/worktrees/` and
`<instance>/.niwa/sessions/`. These are subdirectories of the existing instance
root, so no new filesystem scope is claimed. The daemon writes session state
files with mode `0o600` (owner read/write), consistent with the existing
`sessions.json` policy.

Second, `handleDelegate(session_id=X)` now writes task envelopes to a path
derived from a session state file: `<worktreePath>/.niwa/roles/<repo>/inbox/`.
This is the first case where a coordinator daemon writes to a path that is not
its own `instanceRoot`. The worktree paths are under `<instance>/.niwa/worktrees/`,
which is still within the same instance, but the write is driven by a
file-system path read from a state file rather than a hard-coded derivation.
This is discussed further under Path Traversal and Injection.

**Process creation:** `EnsureDaemonRunning` is extended to spawn per-worktree
daemon processes. This is not a privilege escalation — it runs as the same user.
However, spawning daemons on behalf of coordinator instructions means a
compromised coordinator can create long-lived processes. No new capabilities
(setuid, capability bits, namespaces) are involved.

**Environment variable exposure:** `NIWA_MAIN_INSTANCE_ROOT` and
`NIWA_SESSION_ID` are added to spawned daemon environments. Both are filesystem
paths or short identifiers, not credentials. They are visible in `/proc/PID/environ`
to processes running as the same user, which is the existing threat model for all
niwa daemon environment variables.

No privilege escalation risk is introduced. The expanded write surface (inbox
paths derived from state files) is the primary permission scope concern and is
addressed in the Path Traversal section.

---

### Supply Chain or Dependency Trust
**Applies:** No

This feature adds no new Go module dependencies, external tool invocations, or
plugin loading. `git worktree add` is the existing system git binary, trusted
identically to the git operations niwa already performs. All new code is
first-party Go in the existing package structure. No new trust boundaries with
third-party code are introduced.

---

### Data Exposure
**Applies:** Yes

**Fields stored in session state files** include: `WorktreePath` (filesystem path),
`Purpose` (user-supplied free text), `ClaudeConversationID`, `CreatorPID`,
`ParentSessionID`, and `PRUrl`. Mode `0o600` limits reads to the file owner.
`ClaudeConversationID` is the field that deserves the most scrutiny: it is a
handle into Claude's conversation history. Exposure of this value to another
local user would let them attempt to resume a session if they could also
construct the matching JSONL path. The `0o600` permission is the correct
mitigation.

**`Purpose`** is user-supplied text stored verbatim in the state file. If
callers supply sensitive text (API keys, passwords, internal URLs) as a session
purpose, it is persisted on disk in `<instance>/.niwa/sessions/<session-id>.json`.
The design should document that `purpose` is not a secrets field — callers must
treat it as a description, not a storage location for credentials.

**Worktree paths** in session state files (and the resulting environment
variable `NIWA_MAIN_INSTANCE_ROOT`) are visible in process listings to same-user
processes. This is unchanged from the existing daemon model and within the
documented threat model.

**Audit log:** The existing `mcp-audit.log` captures tool call metadata. The
design inherits this — new tool calls (`niwa_create_session`, etc.) will appear
in audit logs. The `purpose` field from `niwa_create_session` may be logged if
the audit sink records arguments; this should be confirmed during implementation
to avoid leaking sensitive purpose strings into audit output.

No new network transmission of user data is introduced.

---

### Path Traversal and Injection
**Applies:** Yes

This is the dimension with the most concrete risk.

**Session ID used in path construction.** `niwa_list_sessions` filters on
`^[0-9a-f]{8}\.json$` before reading files. This is correct. However,
`ReadSessionLifecycleState(mainInstanceRoot, sessionID)` is called by
`handleDelegate` and `handleAskVirtual` with a session ID sourced from the
`delegateArgs.SessionID` field — a coordinator-supplied string over MCP. If this
string is not validated against the `[0-9a-f]{8}` pattern before being joined
into a filesystem path, a coordinator could pass `../../etc/passwd` or a
path-separator-containing value to read or write outside the sessions directory.

The design mentions the regex filter for `niwa_list_sessions` but does not
explicitly state that `ReadSessionLifecycleState` applies the same validation
before joining the session ID into the path. **The implementation must apply the
`^[0-9a-f]{8}$` regex to all caller-supplied session IDs before constructing
any filesystem path.** This should be a validation step at the entry point of
`ReadSessionLifecycleState` itself, not left to each call site.

**`WorktreePath` read from session state file used to derive inbox paths.** In
`handleDelegate`, the inbox path is constructed as:

```
<session.WorktreePath>/.niwa/roles/<session.Repo>/inbox/
```

Both `WorktreePath` and `Repo` come from the session state file, which is
written by the coordinator. If an attacker can write to `<mainInstanceRoot>/.niwa/sessions/`,
they can place a crafted state file with an arbitrary `WorktreePath` (e.g.,
`/tmp/attacker/`) and cause `handleDelegate` to write task envelopes there.
Access to the sessions directory is gated on filesystem permissions (`0o600`
files, `0o700` directory, owner-only access), so this requires a same-user or
privileged attacker — which is the existing threat model. No cross-user risk
exists. The implementation should nonetheless validate `WorktreePath` is a
subdirectory of `mainInstanceRoot` before using it to construct write targets,
as defense in depth against coordinator bugs or state file corruption.

**`purpose` field.** This is user-supplied free text stored as a JSON string.
JSON encoding handles quoting automatically when the struct is marshaled with
`encoding/json`. There is no shell interpolation of `purpose` anywhere in the
design. No injection risk exists as long as the value is only ever processed
through Go's JSON library and never concatenated into a shell command or SQL
query. This should be verified during implementation wherever `purpose` appears
in log or diagnostic output.

**`Repo` field in inbox path construction.** `session.Repo` is also a state
file field used in path construction. The same defense-in-depth recommendation
applies: validate `Repo` against a known set of repo names or a safe identifier
pattern (`[a-zA-Z0-9_.-]+`) before joining into a filesystem path.

**Collision check for session IDs.** `newSessionLifecycleID()` retries on
collision by checking for an existing file. This loop needs a bound on retries
(e.g., 10 attempts) to prevent a spin in the degenerate case where all 32-bit
IDs in a tiny directory are exhausted (not practically reachable, but the loop
should not be unbounded).

---

### Cross-Session Message Spoofing
**Applies:** Yes

**Can session B forge a message claiming to be from session A?**

`handleAskVirtual` resolves the allowed routing targets from the calling
session's own state file: a child session may address its `ParentSessionID`,
and a parent may address entries in its `Children[]` list. The check reads
`<mainInstanceRoot>/.niwa/sessions/<calling-session-id>.json` to determine who
the caller may reach. The caller's session ID comes from `NIWA_SESSION_ID`,
which is set at daemon spawn time by the coordinator.

**The key trust assumption is that `NIWA_SESSION_ID` in a running daemon process
reflects the session the coordinator assigned it.** Since the environment
variable is set by the coordinator at spawn time and is not re-readable from
user input (only the coordinator controls the spawn), this is correct. A child
daemon cannot change its own `NIWA_SESSION_ID` after startup.

However, there is no cryptographic binding between `NIWA_SESSION_ID` and the
session file on disk. A process running as the same user could:
1. Set `NIWA_SESSION_ID=<victim-session-id>` in its environment.
2. Connect to the MCP server socket.
3. Call `niwa_ask(to="parent")` and have it route using the victim session's
   parent relationship.

The MCP server is a stdio-only server bound to one process's stdin/stdout — it
is not a shared socket accessible to other processes. Only the process that the
coordinator spawned with that stdio pipe can communicate with that daemon
instance. A rogue process at the same UID cannot connect to a running niwa
daemon's MCP channel. This is the correct containment boundary.

Within the constraint that all processes are same-user (which is the documented
threat model), cross-session spoofing via the MCP channel is not possible
because the channel is process-private. A same-user process could manipulate
session state files directly (they are `0o600`, but same-user can read and write
them), which could redirect routing. This is within the existing threat model
boundary: niwa treats same-user processes as trusted.

**Child-to-child routing is blocked.** The design specifies that a child can
only ask its own parent or its own direct children. The `handleAskVirtual`
implementation enforces this by checking the caller's session file for the
allowed target. Session B cannot fabricate a request to session C (a sibling)
without either modifying its own session file or the coordinator's session file
to list C as a child of B — both of which require same-user filesystem access
and are within the acknowledged threat boundary.

The design is sound for the threat model. An explicit note in the Security
Considerations section that the session file permissions are the trust boundary
would be appropriate.

---

### PID Reuse
**Applies:** Yes

`IsPIDAlive(pid, startTime)` reads `/proc/<pid>/stat` to compare the recorded
`startTime` (jiffies since boot) against the live process's start time. This is
the standard Linux anti-PID-reuse technique.

**Is it sufficient?**

The key question is whether two different processes can share the same PID and
the same jiffies-since-boot start time, causing a stale session to appear live
to a new process using that PID. Jiffies since boot is a kernel-maintained
counter at the scheduler's time quantum (typically 4ms or 10ms on modern kernels
with `HZ=250` or `HZ=100`). Two processes can have identical `starttime` fields
in `/proc/PID/stat` only if:
- The original process exits.
- The kernel recycles the PID.
- The new process starts within the same scheduler tick as the original, AND
  the tick boundary falls such that the jiffies counter hasn't advanced.

This is a genuine but rare condition. In practice, PID recycling after a process
exits involves scheduler overhead that nearly always advances the jiffies counter
by at least one tick. The risk is non-zero but very low on a normally loaded
system.

**Consequence of a false positive:** if `IsPIDAlive` returns `true` for a new
process reusing a PID, `handleAskVirtual` would treat the parent session as live
and deliver a message to the parent inbox. The parent daemon (if alive) would
receive an unexpected message in its inbox, which would either be consumed by
the coordinator (who would see an out-of-context question) or expire. No code
execution or privilege escalation follows — the worst outcome is a message
delivered to the wrong session's inbox, which would be treated as a protocol
error by the receiving coordinator.

For the stated threat model (local, same-user, non-adversarial processes), this
risk is acceptable. The design's fallback behavior (`return true` when
`pidStartTime` returns an error) is slightly conservative in the wrong direction
on non-Linux platforms — it means a dead process whose PID has been recycled
may be incorrectly reported as alive if `/proc` is unavailable. The code comment
("be conservative and say alive") acknowledges this. For session routing this
means stale-session messages could be delivered instead of returning
`STALE_PARENT`. This is a UX defect, not a security vulnerability — the
message lands in a monitored inbox, not an uncontrolled location.

**Recommendation:** Document the known limitation of `startTime == 0` fallback
(returns `true`) in the Security Considerations section, noting that on
non-Linux platforms liveness checks degrade to PID-only checks, the same
degradation already documented for `checkExecutor`.

---

## Recommended Outcome
**OPTION 2 - Document considerations:**

The following content should be added to or replace the existing Security
Considerations section in `DESIGN-mesh-session-lifecycle.md`:

---

### Security Considerations

**Worktree isolation:** Session worktrees inherit the main clone's git
configuration and credentials. A session with write access to the git remote
can push branches. This is expected — sessions are trusted processes created by
the coordinator under the owner's account. No additional access controls are
introduced.

**Session ID validation in path construction:** All caller-supplied session IDs
must be validated against `^[0-9a-f]{8}$` before being joined into any
filesystem path. `ReadSessionLifecycleState` must apply this check at its entry
point, not rely on each call site. Unvalidated IDs are the primary path
traversal risk in this design.

**`WorktreePath` and `Repo` origin trust:** Inbox paths derived from session
state fields (`WorktreePath`, `Repo`) should be validated as subdirectories of
`mainInstanceRoot` before use as write targets. This is defense in depth against
state file corruption or coordinator bugs — not a cross-user attack vector, as
the session directory is `0o700` and files are `0o600`.

**Session state file permissions:** Files are created with mode `0o600` (owner
read/write only). The `ClaudeConversationID` field is stored in these files;
unauthorized read would expose a handle to session conversation history.
`0o600` is the correct and sufficient protection for the single-user threat
model.

**`purpose` field:** Stored as a verbatim JSON string. JSON encoding handles
quoting; there is no injection risk from this field into filesystem operations.
However, `purpose` is not a secrets store — callers must not use it to hold
credentials or tokens, as it is persisted on disk and may appear in diagnostic
output.

**MCP channel isolation:** The MCP server communicates only over the process's
stdio pipe. No network socket is exposed. A rogue same-user process cannot
connect to a running daemon's MCP channel — cross-session spoofing via the MCP
protocol is not possible without replacing the stdio process.

**Session file as trust boundary:** The `handleAskVirtual` routing policy
(parent/child only) is enforced by reading the calling session's own state file.
This file is written by the coordinator at session creation time. Same-user
processes with write access to `<instance>/.niwa/sessions/` can manipulate
routing. This is consistent with the existing threat model, which treats the
local filesystem as the trust boundary for a single-user developer environment.

**PID reuse via jiffies collision:** `IsPIDAlive` uses `starttime` from
`/proc/<pid>/stat` (jiffies since boot) to distinguish recycled PIDs. Two
processes can share the same PID and starttime if the scheduler tick does not
advance between process exit and PID reuse. This is rare on a normally loaded
system and results at worst in a stale message delivered to the wrong session's
inbox — a protocol error, not a security vulnerability. On non-Linux platforms
where `/proc/stat` is unavailable and `startTime == 0`, the function returns
`true` (conservative), degrading to PID-only liveness checks, the same ceiling
documented for `checkExecutor`.

**No network services:** All inter-daemon communication uses shared filesystem
paths (inbox directories). No network sockets or ports are opened. The attack
surface is local filesystem access only, consistent with the existing niwa
daemon model.

**`newSessionLifecycleID` retry loop:** The collision-check retry loop should
be bounded (e.g., 10 attempts) to prevent an unbounded spin in degenerate
conditions. At a practical session count (< 20 per instance), no collision
should occur.

---

## Summary

The design is sound for its single-user, local-filesystem threat model. The
primary concrete gap is that session IDs derived from MCP caller input must be
validated with `^[0-9a-f]{8}$` inside `ReadSessionLifecycleState` rather than
at individual call sites, and `WorktreePath` / `Repo` pulled from session state
files should be validated as subpaths of `mainInstanceRoot` before being used to
construct write targets. PID reuse via jiffies collision and the `startTime==0`
fallback are known, low-risk limitations consistent with the existing
`checkExecutor` degradation model, and should be explicitly documented.
