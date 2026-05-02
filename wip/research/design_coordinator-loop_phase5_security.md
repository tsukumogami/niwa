# Security Review: coordinator-loop

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes

This design reads and executes content from two external sources.

**Session file integrity check (Change 2).** The daemon reads up to 4 KB from
`~/.claude/projects/<base64url-cwd>/<session_id>.jsonl` before passing the
session ID to `claude --resume`. The session ID used to construct the path
comes from `TaskState.Worker.ClaudeSessionID`, which the worker stored via
`niwa_register_worker_session`. That storage path validates the ID against
`sessionIDRegex` (`^[a-zA-Z0-9_-]{8,128}$`) before writing it. The base64url
encoding of the CWD is computed by the daemon at resume time using
`resolveRoleCWD`, not from user input. Both components are therefore
controlled before they reach `filepath.Join`, making a path-traversal attack
on the integrity-check read impractical — no `..` or `/` can survive the
regex, and the CWD is daemon-derived.

The check verifies that the last complete line parses as valid JSON. It reads,
not executes, the file. No code is evaluated from the session file content;
only a JSON parse result (pass/fail) influences the resume decision. This
limits the blast radius of a crafted `.jsonl` file to a false-positive
integrity verdict, which at worst causes an unnecessary resume attempt (see
the fallback cap below).

**`claude --resume` invocation (Change 2).** The daemon calls
`claude --resume <session_id> -p "<reminder>"`. The design states:
"Session ID + reminder message are validated/constant; not constructed from
user input." The session ID has already been validated through the regex
before storage. The reminder message is a hard-coded constant in
`mesh_watch.go`. Neither value is derived from task body content, worker
output, or any other untrusted source. There is no shell interpolation —
the exec path uses `exec.Command` with discrete arguments, not a shell
invocation.

**Severity:** Low. The two validation layers (regex at write time, no
shell-expansion at exec time) close the obvious injection vectors. The
remaining risk is that a compromised worker can register an arbitrary
session ID that passes the regex, causing the daemon to resume a session
of the attacker's choosing. This is same-UID by design (workers run as the
user), so the threat model already accepts same-UID access. A malicious
session ID cannot escalate privilege; it can at most cause the daemon to
resume a different session, which is within the worker's existing
capability set.

**Mitigation already present:** `sessionIDRegex` blocks any non-alphanumeric
character. The daemon's `kindExecutor` authorization check on
`niwa_register_worker_session` requires the caller to hold the right
NIWA_TASK_ID and NIWA_SESSION_ROLE and (on Linux) to pass the PPID/start_time
chain check before the session ID is accepted.

**Recommendation:** Document in the handler that the session ID is stored for
a subsequent `exec.Command` call, so future maintainers do not relax the
regex without understanding the exec context.

---

### Permission Scope

**Applies:** Yes

The design introduces two new code paths that interact with the filesystem and
process lifecycle.

**Stop hook CLI subcommand (Change 1).** `niwa mesh report-progress` reads
`state.json` via `mcp.ReadState` (shared flock, O_NOFOLLOW) and writes it via
`mcp.UpdateState` (exclusive flock, O_NOFOLLOW, atomic rename). This is
structurally identical to `handleReportProgress` in the MCP path. No new
filesystem access beyond what the MCP server already performs. The subcommand
runs in the worker's process environment, which already has read/write access
to `.niwa/tasks/<id>/` by construction.

Authorization is lighter than the MCP `kindExecutor` check: the subcommand
verifies that `NIWA_SESSION_ROLE` matches `state.json.worker.role` and
`NIWA_TASK_ID` matches `state.json.task_id`, but does not perform the
PPID/start_time chain check. The design documents this explicitly and justifies
it: the hook script is a child of the Claude Code harness, not the worker
process itself, so PPID-chain validation would always fail. The weaker check
accepts that any process with the correct env vars and read access to state.json
can reset the watchdog timestamp.

The attack surface from the weaker check is bounded. The env vars
(NIWA_TASK_ID, NIWA_SESSION_ROLE, NIWA_INSTANCE_ROOT) are set by the daemon at
spawn time and are not user-controllable secrets — they are structural routing
values. A same-UID attacker who can read the worker's environment already has
full access to the worker's workspace. No privilege escalation path exists here
because the subcommand only modifies `last_progress.at` (a timestamp field) in a
task the worker already owns.

**Severity:** Low. The weakened authorization is a documented, bounded
trade-off, not a gap. The worst-case outcome of an unauthorized call is a reset
watchdog timestamp, which delays (but does not prevent) stall detection for a
task the attacker already controls under the same UID.

**`niwa_register_worker_session` MCP tool (Change 2).** Uses `UpdateState`,
which requires the existing `kindExecutor` authorization including (on Linux)
the PPID/start_time check. Writes only `Worker.ClaudeSessionID` and
`Worker.ResumeCount` to state.json. No new permissions beyond what existing
executor-kind tools already hold.

**`claude --resume` process spawning (Change 2).** The daemon already spawns
`claude` processes. Resume-path spawning adds `--resume <session_id>` to the
argv. No new capabilities, no setuid, no new file descriptors beyond what
the existing `spawnWorker` path opens. The per-task worker MCP config is
regenerated for resume spawns (stated in Decision 2), preserving MCP isolation
for resumed sessions.

**handleAsk no_live_session (Change 3).** This change removes a code path (the
ephemeral spawn fallback) rather than adding one. It reduces permission scope
by not creating task store directories and not spawning processes when no live
session is present.

**Recommendation:** Add an explicit test asserting that `report-progress --task-id`
with a spoofed NIWA_SESSION_ROLE (one that doesn't match state.json.worker.role)
is rejected, to verify the authorization boundary is enforced even without the
PPID chain check.

---

### Supply Chain or Dependency Trust

**Applies:** No

This design does not add new dependencies, does not download any artifacts, and
does not introduce any new build-time or runtime trust anchors. All three
changes operate on local filesystem data (state.json, transitions.log,
session .jsonl files). The `claude` binary invoked on the resume path is the
same binary already invoked by the existing `spawnWorker` path — no new
binary is fetched or verified. The `sessionIDRegex` and the constant reminder
message are statically compiled into the daemon; they have no external origin.

---

### Data Exposure

**Applies:** Yes

**Session ID storage (Change 2).** `TaskState.Worker.ClaudeSessionID` persists
the Claude Code session ID in `state.json`. Session IDs matching
`^[a-zA-Z0-9_-]{8,128}$` are opaque identifiers for conversation histories
stored in `~/.claude/projects/`. Storing one in `state.json` makes it readable
by anyone who can read the task directory.

Who can read `.niwa/tasks/<id>/state.json`? The directory is created with mode
0700, and `state.json` is written with mode 0600. Under a single-user
deployment (the documented model), this is exclusively the user who spawned
the workspace. No multi-user or group-readable path exists in the current code.
The session ID is not transmitted over any network path — it stays on local
disk.

The session ID does not contain conversation content; it is a key used to
locate the `.jsonl` conversation file. The `.jsonl` file is itself already
stored at `~/.claude/projects/`, accessible to the same user with or without
this change. Storing the session ID in `state.json` does not expand the data
surface; it provides a pointer to data that was already locally accessible.

**`transitions.log` entries (Change 2).** The design adds a `resume=true`
boolean to spawn `TransitionLogEntry` entries in `transitions.log`. No session
ID is written to `transitions.log`. The existing Note in `types.go` states
that progress `body` fields are intentionally excluded from `transitions.log`
for redaction. This design is consistent with that convention.

**Reminder message content (Change 2).** The reminder passed via
`claude --resume ... -p "<reminder>"` is a hard-coded constant. It contains no
task-specific data, no user content, and no secrets. It appears in
`transitions.log` only as part of the `spawn` entry's metadata, not as
`body` content.

**Stop hook visibility (Change 1).** The subcommand writes only a timestamp to
`last_progress.at` in `state.json`. It does not write any task body, file
content, or environment variable values. The summary field is set to a fixed
heartbeat string (or whatever the implementation chooses for the automated
heartbeat), not derived from live conversation content.

**Severity:** Low. The session ID in `state.json` is the only new data item and
it is scoped to local disk under the user's own 0600-mode file. The design does
not transmit data off-host and does not expand the set of users who can access
session information.

**Recommendation:** Document in the `TaskWorker` struct comment (alongside the
existing `ClaudeSessionID` field) that this value is a conversation-session
pointer and should not appear in debug output that might be shared externally
(e.g., bug reports). The existing transitions.log note covers that file; the
state.json comment is the gap.

---

## Recommended Outcome

**OPTION 2 - Document considerations:**

The design has no changes needed. All three risk areas identified have
mitigations either already in place (sessionIDRegex, kindExecutor
authorization, 0600 file modes, no-shell exec.Command, MCP config
regeneration) or explicitly accepted in the decision documents with bounded
blast radius. The implementer should record the following in code comments:

**Security Considerations for Implementers**

1. **Stop hook authorization weakening.** `niwa mesh report-progress` skips
   the PPID/start_time chain check that `kindExecutor` enforces in the MCP
   path. This is intentional (hook scripts are harness children, not worker
   children), but the check that remains — matching NIWA_TASK_ID and
   NIWA_SESSION_ROLE against state.json — must not be removed or weakened
   further. Add a test that confirms a mismatched NIWA_SESSION_ROLE is
   rejected.

2. **Session ID in exec.Command.** `Worker.ClaudeSessionID` is stored for
   subsequent use in an `exec.Command` argv slot. The `sessionIDRegex`
   validation at write time is the injection gate for this exec path. Any
   future relaxation of that regex (e.g., adding `/` or `.` characters to
   support a different session ID format) requires a security review of the
   exec invocation.

3. **Session ID in state.json is a conversation pointer.** Do not include
   `Worker.ClaudeSessionID` in log output intended for external sharing (e.g.,
   sanitized bug reports). The value does not contain conversation content but
   it identifies which conversation to resume, which may be sensitive in some
   contexts.

4. **Resume cap prevents infinite loop.** The `MaxResumes` cap (default 2) is
   the primary defense against a compromised worker that deliberately fails to
   call `niwa_report_progress` to force repeated resumes. Ensure the cap is
   enforced before the integrity check runs, not after, so the check cannot
   be bypassed by manipulating the `.jsonl` file.

---

## Summary

The design is sound. The three changes operate entirely within the existing
same-UID, local-filesystem trust boundary that the rest of niwa's task-lifecycle
system uses. The stop hook's lighter authorization check is a documented
trade-off with a bounded worst-case (watchdog timestamp manipulation by a
same-UID process that already controls the worker environment). The session ID
handling is protected at both the write boundary (regex validation + kindExecutor
authorization) and the exec boundary (discrete argv, no shell interpolation). No
design changes are needed; the implementer should add inline comments at the
three points identified above to ensure the security invariants remain legible
to future maintainers.
