# Decision 2: Claude Session ID Discovery

## Context

`niwa session register` is called by the SessionStart hook every time a Claude Code
session opens or resumes. It writes a `SessionEntry` to `sessions.json` that includes
the niwa session UUID, role, PID, process start time, and inbox directory path. The
daemon (`niwa mesh watch`) needs to call `claude --resume <claude-session-id>` to wake
idle sessions when messages arrive for them. For this to work, `niwa session register`
must also capture the Claude Code session ID and store it in `SessionEntry`.

The key constraint is that `niwa session register` runs as a subprocess of the
SessionStart hook script, which is itself spawned by Claude Code. It cannot call into
Claude Code internals. It knows `NIWA_INSTANCE_ROOT`, the current working directory, and
whatever Claude Code injects into the hook subprocess environment.

The `SessionEntry` struct and `session_register.go` exist in the codebase but do not yet
record `ClaudeSessionID`. This decision determines how to fill that field.

## Key assumptions

- Claude Code passes a JSON payload on stdin to command hooks (confirmed by hook scripts
  in `.claude/hooks/`), but the exact fields for `SessionStart` are not documented in
  the available research. The payload is known to include `cwd` and event type.
- Claude Code maintains a live session registry at `~/.claude/sessions/` with per-PID
  JSON files containing `sessionId`, `cwd`, and `startedAt`. These files persist after
  sessions exit and are not cleaned up reliably.
- Claude Code stores session transcripts as JSONL files at
  `~/.claude/projects/<base64url-encoded-cwd>/<session-uuid>.jsonl`. The encoding
  algorithm is base64url of the absolute CWD path.
- `CLAUDE_SESSION_ID` may or may not be exported to hook subprocesses. No community
  report or documentation confirms this. It is treated as unverified.
- The daemon requires the Claude session ID for `claude --resume`. Without it, the
  daemon cannot resume idle sessions and must fall back to waiting for manual session
  open.

## Chosen: Option A+B — Try `CLAUDE_SESSION_ID`, then fall back to `~/.claude/sessions/<pid>.json`, then filesystem scan; leave empty if all fail

Read the Claude session ID in this priority order:

1. **`CLAUDE_SESSION_ID` environment variable** (Option A): If Claude Code exports this
   to hook subprocesses, it is authoritative and zero-cost. Read it first with no I/O.

2. **`~/.claude/sessions/<ppid>.json`** (live registry, new in research): Claude Code
   writes a JSON file per running process at `~/.claude/sessions/<pid>.json` containing
   `sessionId`, `cwd`, and `startedAt`. `niwa session register` can read `os.Getppid()`
   to get the Claude Code process PID (the parent of the hook script, which is the parent
   of the `niwa` subprocess), then read `~/.claude/sessions/<ppid>.json`. This is fast
   (one file read), requires no encoding math, and directly correlates the Claude process
   PID to its session ID.

3. **Filesystem scan of `~/.claude/projects/<encoded-cwd>/`** (Option B): Encode the
   CWD using base64url (standard encoding, no padding, of the UTF-8 absolute path).
   List `*.jsonl` files, sort by mtime descending, and take the most recently modified.
   This is the fallback when the live registry file is absent or stale.

4. **Leave `claude_session_id` empty and log a warning** (Option E partial): If all
   three paths fail, the field is left empty. The daemon skips `claude --resume` for
   this session and relies on SessionStart hook delivery when the session next opens
   manually. This is already specified as the graceful degradation path in the PRD
   (R32 and the Known Limitations section).

The implementation in `runSessionRegister` adds `claudeSessionID := discoverClaudeSessionID()`
before constructing `SessionEntry`, where `discoverClaudeSessionID` tries the three
paths in order and returns an empty string on total failure. The `SessionEntry` struct
gains a `ClaudeSessionID string \`json:"claude_session_id,omitempty"\`` field.

## Rationale

**Why not Option A alone**: `CLAUDE_SESSION_ID` is unconfirmed. No documentation or
community report establishes that Claude Code exports it to hook subprocesses. Shipping
code that silently stores an empty session ID whenever the env var is absent — and only
noticing the gap when the daemon fails to resume sessions — is worse than having a
two-layer fallback. The env var check costs nothing when it succeeds and adds one
`os.Getenv` call when it fails.

**Why the `~/.claude/sessions/<ppid>.json` path is the practical primary**: The research
confirms this file exists and contains `sessionId` keyed by PID. The hook script spawns
`niwa session register` as a child process. The parent of `niwa` is the hook script
shell. The parent of the hook script shell is the Claude Code process. On Linux and
macOS, `os.Getppid()` gives the hook script's PID; the Claude Code PID is that
process's parent. A two-level PPID walk (niwa → hook shell → claude) is reliable
because the hook subprocess chain is shallow and deterministic. This avoids encoding
math and races on the most-recent-file heuristic. The file at `~/.claude/sessions/<pid>.json`
was written by Claude Code itself when the session started — it is authoritative for
that PID.

**Why the filesystem scan is needed as a second fallback**: The `~/.claude/sessions/`
file persists after session exit and may be stale for a recycled PID. The scan of
`~/.claude/projects/<encoded-cwd>/` by mtime is a weaker heuristic (could pick the wrong
session if two sessions opened for the same CWD in close succession), but it catches
cases where the live registry file is absent. The race window is narrow: `SessionStart`
fires at the beginning of a session, and the `*.jsonl` file is created at that moment
or shortly before. The most-recently-modified file is overwhelmingly likely to be the
current session.

**Why Option C (skip session ID, use `--continue` or hook-only delivery) is wrong**:
The PRD's daemon architecture depends on `claude --resume <session-id>` to wake idle
sessions. `claude --continue` resumes the *most recent* session for the CWD — wrong if
multiple sessions exist for the same directory or if the most recent session is a
different role. Relying solely on SessionStart hook delivery means idle sessions are
never woken by the daemon; the user must manually open each session to receive messages.
This eliminates the daemon's core value: autonomous delivery when the session is not
open.

**Why Option D (sentinel file written by Claude Code) is not viable**: It requires users
to configure Claude Code to write a file that Claude Code doesn't write by default.
Niwa cannot require users to add external configuration to Claude Code itself to make
a niwa feature work. This is a provisioning-model violation.

**Why Option E alone is not viable for v1**: Pure Option E means the daemon is never
able to resume sessions. The daemon becomes a message monitor with no wakeup capability,
degrading to pure SessionStart hook delivery. The PRD explicitly defines daemon-managed
wakeup as a v1 requirement (R29). Option E's graceful fallback is correct for the case
where session ID discovery fails; making it the primary path removes the daemon's reason
for existence.

## Rejected alternatives

### Option A alone: `CLAUDE_SESSION_ID` environment variable
Attempt to read `CLAUDE_SESSION_ID` from the environment. If set, use it.

Rejected because: no documentation or community evidence confirms Claude Code exports
this variable to hook subprocesses. An implementation that depends solely on an
unconfirmed env var will silently produce empty session IDs in production, disabling
daemon wakeup without any warning until someone investigates. The env var check is
retained as the first step in the combined approach — if it exists, it's zero-cost and
authoritative.

### Option C: Daemon uses `--continue` or hook-only delivery; no session ID needed
Instead of `claude --resume <session-id>`, the daemon calls `claude --continue` (most
recent session for CWD) or relies entirely on the SessionStart hook to deliver messages
when sessions are manually opened.

Rejected because: `claude --continue` picks the most recent session for the CWD, which
is wrong when multiple sessions exist for the same directory at different times (e.g.,
a worker session was closed and a new unrelated session was opened). More critically,
this eliminates the daemon's ability to wake sessions autonomously. If the daemon cannot
call `claude --resume`, idle sessions are never woken by message arrival — the daemon
becomes a message monitor with no delivery capability. The PRD defines daemon wakeup as
a v1 requirement.

### Option D: Sentinel file written by Claude Code
Ask users to configure Claude Code to write the session ID to a known path at session
start, which the hook reads.

Rejected because: this requires users to modify their Claude Code configuration (outside
of niwa's provisioning scope) to make a niwa feature work. Niwa's value is that
sessions find everything configured when they open. Requiring users to add external
Claude Code config negates that.

### Option E: Skip session ID for v1; fall back to SessionStart-only delivery
Don't record the Claude session ID. Accept that the daemon cannot do `claude --resume`.
Deliver messages only at session start via `initialUserMessage`.

Rejected as the primary path because the daemon's core function is waking idle sessions
autonomously. Without session ID, idle sessions are never woken by message arrival. This
is retained as the graceful degradation path — when all three discovery methods fail, the
field is left empty and the daemon logs a warning, falling back to SessionStart delivery.
Option E's fallback behavior is part of the chosen approach, not the primary path.

## Consequences

- Positive:
  - Session ID is captured at registration time with high reliability via the PPID →
    `~/.claude/sessions/<ppid>.json` path, which is exact and requires no heuristics
  - Graceful degradation when discovery fails: daemon skips resume, SessionStart hook
    still delivers messages on manual open
  - The `CLAUDE_SESSION_ID` check is zero-cost on the happy path if Claude Code ever
    does export it; the code is already there
  - No new dependencies; `os.Getppid()`, file reads, and `encoding/base64` are Go stdlib

- Negative:
  - PPID walk requires two levels (niwa process → hook shell → Claude Code process) on
    Linux/macOS; this is reliable on POSIX but would break if Claude Code stops writing
    `~/.claude/sessions/<pid>.json` in a future version
  - The base64url encoding of the CWD path must match Claude Code's exact algorithm; if
    Claude Code changes its encoding, the filesystem scan produces wrong results. This
    is a forward-compatibility risk for the fallback path only, not the primary path
  - If `~/.claude/sessions/` accumulates stale files (entries for dead PIDs), a stale
    file for a recycled PID could be read by mistake. The `cwd` field in that file should
    be cross-checked against the current CWD to detect this case
  - `SessionEntry.ClaudeSessionID` will be empty for sessions that opened without the
    hook (e.g., bare mode, `claude -p`). Those sessions cannot be resumed by the daemon

- Mitigations:
  - Cross-check the `cwd` field in `~/.claude/sessions/<ppid>.json` against the current
    working directory before trusting the `sessionId` field; if they disagree, the file
    is stale and the code falls through to the filesystem scan
  - Log a warning (not an error) when `claude_session_id` is left empty so operators
    can observe when daemon wakeup will be unavailable for a given session
  - Document the forward-compatibility risk for the base64url encoding in a code comment;
    if Claude Code changes the encoding, the fallback scan can be updated in isolation
    without affecting the primary PPID path
  - Add a functional test that verifies `claude_session_id` is populated in
    `sessions.json` after `niwa session register` runs (using a fake `~/.claude/sessions/`
    fixture), catching regressions if the discovery logic breaks
