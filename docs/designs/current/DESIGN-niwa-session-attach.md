---
status: Planned
upstream: docs/prds/PRD-niwa-session-attach.md
problem: |
  Niwa's mesh has a single recovery primitive when a worker drifts off-task:
  destroy the session and start over. There is no way for a human to take a
  conversation over, see the worker's full transcript, prompt it, fix things,
  and hand it back. The PRD locks in the user-facing behavior; this design
  document specifies how to build it inside niwa's existing daemon, lifecycle,
  CLI, and MCP surfaces without touching adjacent in-flight work.
decision: |
  Add `niwa session attach <id>` and `niwa session detach <id> [--force]`
  driven by a new package `internal/cli/sessionattach`. The lock is a
  non-blocking `flock(2)` on `<worktree>/.niwa/attach.lock`; visibility is a
  sibling JSON sentinel at `<worktree>/.niwa/attach.state` that
  `niwa session list` reads at render time and `niwa_list_sessions` projects
  into a new computed `attach` sub-object on `SessionLifecycleState`.
  Daemon coordination uses the existing `TerminateDaemon` /
  `EnsureDaemonRunning` helpers — attach terminates the per-worktree daemon
  and respawns it on detach, letting the existing catch-up replay path drain
  any envelopes that queued during attach. Pre-flight transcript validation
  computes the `s/[^A-Za-z0-9]/-/g` path encoding directly and emits three
  niwa-shaped error messages before exec'ing `claude --resume <uuid>` as a
  child process under the foreground niwa-attach process.
rationale: |
  Every primitive this design needs already exists in the codebase: the
  flock pattern is `acquireDaemonPIDLock`, the liveness check is
  `IsPIDAlive`, the daemon stop/start is `TerminateDaemon` /
  `EnsureDaemonRunning`, and the atomic-tmp-rename pattern is the same one
  used for `SessionLifecycleState`. Reusing them keeps the implementation
  small, makes the security review trivial (no new privileged operations),
  and avoids competing with PR #115's mesh-reliability work — both PRs add
  computed sub-objects to `SessionLifecycleState` without touching the V:1
  schema. The terminate-and-respawn daemon pattern intentionally leverages
  the existing inbox-replay path so concurrent-worker handling, envelope
  queuing, and post-detach delivery fall out for free with zero new daemon
  code paths.
---

# DESIGN: niwa session attach

## Status

Current

## Context and Problem Statement

The niwa mesh today has a single recovery primitive when a worker drifts
off-task or stalls: `niwa session destroy`. This forces the operator to
either trust an agent that's already off-track or discard accumulated
context (commits, conversation history, design state).

The PRD locks in the behavior. This design document answers the
implementation questions the PRD doesn't address:

- What package does the new code live in, and what existing packages does
  it depend on?
- What are the exact function signatures, file paths, and on-disk shapes?
- What is the precise sequence of operations during attach acquire and
  detach release, including error-handling at each step?
- How does the new `attach` sub-object on `SessionLifecycleState` get
  populated, and how does it interoperate with PR #115's `daemon`
  sub-object?
- What test infrastructure exists, and what does the implementation need
  to add?
- What changes to documentation and the shell-wrapper landing-path
  protocol are required?

The technical surfaces affected are:

- **`internal/cli/`**: two new command files (`session_attach.go`,
  `session_detach.go`); modification of `session.go` to remove the
  deprecated mesh-list alias; modification of `session_lifecycle_cmd.go`
  to add the AVAILABILITY column and the new sort key/filters.
- **`internal/mcp/`**: a new file `attach_state.go` with the sentinel
  read/write helpers; modification of `session_lifecycle.go` to add the
  computed `Attach` field; modification of `handlers_session.go` to gate
  destroy on the attach lock; modification of `server.go` for
  `niwa_list_sessions` filter parameters and the new `SESSION_ATTACHED`
  error.
- **`docs/guides/sessions.md`**: new "Human-in-the-Loop" section per
  PRD AC29.
- **`test/functional/features/`**: a new `@critical` Gherkin scenario
  per PRD AC30.

## Decision Drivers

From the PRD:

- **Lock correctness**: only one human attached at a time per session;
  stale locks must be detectable and recoverable without operator surgery.
- **Mesh-safe by construction**: the daemon must not spawn workers in an
  attached session; envelopes that arrive during attach must be processed
  on detach.
- **Loud failure on transcript loss**: if `claude --resume` cannot find
  the transcript, the operator sees a niwa-shaped error explaining what
  to do, not claude's UUID-shaped error.
- **CLI/MCP parity**: every CLI flag has an MCP equivalent; every CLI
  output column comes from a strict subset of MCP fields.
- **No schema-version bump**: PR #115 sets the precedent for additive
  computed sub-objects under V:1.

Implementation-specific:

- **Reuse existing primitives**: every operation should map to an
  existing helper (`flock`, `IsPIDAlive`, `TerminateDaemon`,
  `EnsureDaemonRunning`, `WriteSessionLifecycleState`'s tmp+rename
  pattern). New patterns must be justified explicitly.
- **Single-process supervision**: niwa-attach is a foreground
  long-running parent that holds the flock and forks Claude Code as a
  child. No exec-replacement, no wrapper-driven multi-call sequencing.
- **Test surface follows the existing functional-test patterns**: the
  `localGitServer` fixture in `test/functional/` is the integration-test
  contract; new tests use it.
- **Linux-first, with documented non-Linux fallback**: PID start-time
  reads `/proc`, which is Linux-only. macOS/BSD callers degrade to the
  conservative signal-0 liveness check (already in `liveness.go:31-33`).

## Considered Options

The PRD already documented 19 decisions (D1-D19). This section captures
the implementation-level decisions that the PRD did not need to settle,
each with the alternative considered and why the chosen option won.

### CO1. Package layout

- **Option A (chosen): one new package, `internal/cli/sessionattach`,
  containing `attach.go` and `detach.go`.** New tests in the same
  package. The package depends on `internal/mcp` (state schema,
  liveness, sentinel helpers) and `internal/workspace` (daemon
  helpers).
- **Option B**: put the commands in `internal/cli/session_attach.go`
  and `internal/cli/session_detach.go` directly (no sub-package).
- **Option C**: extend `internal/mcp` with attach handlers analogous to
  `handlers_session.go`.
- **Why A**: the attach/detach commands have meaningful internal
  helpers (lock acquisition, sentinel read/write, daemon orchestration,
  pre-flight validation, child-process supervision) that benefit from
  being unit-testable without dragging in the cobra surface. A
  sub-package keeps the boundary clean. Option B works but mixes
  internal helpers with cobra wiring. Option C inverts the dependency
  direction — mcp does not run cobra commands.

### CO2. Sentinel file location and shape

- **Option A (chosen): `<worktree>/.niwa/attach.state` JSON file with
  `{ "v": 1, "owner_pid": int, "owner_start_time": int64, "started_at":
  RFC3339, "lock_path": string }`.** Atomic tmp+rename. 0600 perms.
  Sibling to the existing `daemon.pid`, `daemon.log`, `daemon.pid.lock`.
- **Option B**: store the sentinel inside `<instance>/.niwa/sessions/<sid>.json`
  alongside the lifecycle state.
- **Option C**: derive metadata at read time from the open `flock`
  (`fcntl(F_GETLK)`) without persisting a sentinel.
- **Why A**: keeps the sentinel adjacent to the lock it shadows, which
  mirrors how the daemon co-locates `daemon.pid` and `daemon.pid.lock`.
  Option B creates a write contention point on the lifecycle file
  (which has no flock today, only tmp+rename); see SURPRISE 1 in the
  exploration's lock-semantics research. Option C does not work
  cross-process because `fcntl` only reports the holder PID and even
  then only on Linux; we need start-time and started-at for stale
  detection and visibility.

### CO3. Child-process supervision pattern

- **Option A (chosen): `exec.Cmd` with `cmd.Run()` and stdio inherited
  from os.Stdin/Stdout/Stderr, `cmd.Dir` set to the worktree.** niwa
  process holds the flock for the lifetime of the child and runs a
  cleanup routine on the child's exit.
- **Option B**: `syscall.Exec` to replace the niwa process with claude.
- **Option C**: spawn claude under `setsid` and detach.
- **Why A**: the lock is held by the niwa process's open flock fd. If
  niwa exec-replaces (Option B), the fd persists across exec but the
  niwa cleanup code does not run — there's no way to remove the
  sentinel file on claude exit. If niwa detaches the child (Option C),
  niwa exits and the lock drops immediately while claude is still
  running. Only A keeps the lock-lifetime tied to the human's session.

### CO4. Stop-the-daemon strategy

- **Option A (chosen): call `TerminateDaemon(<worktree>)` on attach
  acquire; call `EnsureDaemonRunning(<worktree>, nil)` on detach
  release.** In-flight envelopes accumulate in the inbox and are
  drained by the catch-up replay path on respawn.
- **Option B**: leave the daemon running and add a sentinel-file-skip
  in `handleInboxEvent` so the daemon ignores envelopes when the lock
  is held.
- **Option C**: send a SIGUSR1 to the daemon to pause its claim loop
  without terminating the process.
- **Why A**: zero changes to the daemon's hot path. The catch-up
  replay path (`scanExistingInboxes` at `mesh_watch.go:275`) is
  exercised on every daemon restart and is well-tested. Option B
  pollutes the daemon with attach awareness and adds a file-stat to
  every inbox event. Option C invents a new IPC mechanism in a
  codebase that has none today.

### CO5. Pre-flight transcript validation placement

- **Option A (chosen): synchronous pre-flight inside the attach
  command after lock acquire and before daemon terminate.** Returns
  early with the niwa-shaped error message and releases the lock if
  validation fails.
- **Option B**: speculative — exec claude directly and let it fail.
  niwa post-processes the stderr.
- **Option C**: defer validation to a separate command
  (`niwa session show` or similar).
- **Why A**: lets niwa emit niwa-shaped errors with three distinct
  cases (no conv_id, missing transcript, empty transcript) per PRD R4.
  Option B would require parsing claude's stderr (fragile across
  claude versions) and wouldn't help with case A (no conv_id captured).
  Option C delays the error to a separate user action.

### CO6. AVAILABILITY column data source

- **Option A (chosen): `niwa session list` reads each lifecycle state
  file, then opens the corresponding `<worktree>/.niwa/attach.state`
  to project the AVAILABILITY value.** Uses `IsPIDAlive` to classify
  as `available`/`attached`/`stale`.
- **Option B**: persist AVAILABILITY into the lifecycle state file at
  attach acquire/release time.
- **Option C**: maintain a second index file mapping session_id to
  attach state.
- **Why A**: matches PR #115's pattern for the `daemon` sub-object —
  computed at read time from observable filesystem state. Option B
  introduces write contention on the lifecycle file (no flock today)
  and a staleness window if the niwa-attach process dies between
  state-file write and lock release. Option C is a complexity
  multiplier with no win.

### CO7. Force-detach kill chain

- **Option A (chosen): SIGTERM to the holder PID, wait
  `NIWA_DESTROY_GRACE_SECONDS` (default 5s), SIGKILL if still alive.**
  Mirrors `TerminateDaemon`'s kill chain exactly.
- **Option B**: SIGKILL immediately (bypass the grace).
- **Option C**: SIGTERM only; refuse to escalate.
- **Why A**: matches existing pattern. SIGTERM gives the
  niwa-attach process a chance to run its cleanup (remove sentinel,
  close claude child cleanly) before being killed. Option B prevents
  cleanup. Option C leaves the operator with no recovery path if the
  process ignores SIGTERM.

### CO8. AVAILABILITY filter semantics for `stale`

- **Option A (chosen): `--attached` includes only live attaches;
  `--available` includes only `available` rows; `stale` rows appear
  under neither.** Operators see stale rows in the unfiltered listing
  and decide whether to detach them.
- **Option B**: `--attached` includes `stale` rows.
- **Option C**: `--stale` filter as a third orthogonal flag.
- **Why A**: per PRD R16. Stale rows represent neither "free for
  immediate attach" nor "actively in use"; conflating them with either
  filter would mislead operators. Option C adds a flag for a transient
  state that operators discover via the unfiltered view anyway.

## Decision Outcome

Implement attach as a foreground niwa-supervised process that holds a
non-blocking exclusive flock on `<worktree>/.niwa/attach.lock`,
publishes attach metadata via a sibling `attach.state` JSON sentinel,
terminates the per-worktree daemon for the duration of the human
session, validates the claude transcript pre-flight with three distinct
niwa-shaped error messages, and exec's `claude --resume <uuid>` as a
child process with stdio inherited from the operator's terminal. On
clean child exit, niwa removes the sentinel, respawns the daemon, and
emits worktree-state warnings for any uncommitted/untracked/unpushed
work. On stale-lock detection (`IsPIDAlive` returns false), readers
treat the slot as released and may opportunistically reap the sentinel.

## Solution Architecture

### Package and File Layout

```
internal/cli/sessionattach/        # NEW package
  attach.go                        # niwa session attach command
  attach_test.go
  detach.go                        # niwa session detach command
  detach_test.go
  preflight.go                     # transcript validation + path encoding
  preflight_test.go
  supervise.go                     # child-process spawn + signal forwarding
  supervise_test.go
  worktree_warnings.go             # uncommitted/untracked/unpushed checks
  worktree_warnings_test.go

internal/cli/
  session.go                       # MODIFIED: remove deprecated alias
  session_attach_register.go       # NEW: cobra registration only
  session_lifecycle_cmd.go         # MODIFIED: AVAILABILITY column, sort, filters

internal/mcp/
  attach_state.go                  # NEW: sentinel read/write/IsPIDAlive integration
  attach_state_test.go
  session_lifecycle.go             # MODIFIED: add Attach computed field type
  handlers_session.go              # MODIFIED: SESSION_ATTACHED gate on destroy
  server.go                        # MODIFIED: niwa_list_sessions filter params

docs/guides/
  sessions.md                      # MODIFIED: Human-in-the-Loop section

test/functional/features/
  session_attach.feature           # NEW: @critical Gherkin scenario
```

The new `internal/cli/sessionattach/` sub-package keeps the helpers
unit-testable without dragging cobra in. `session_attach_register.go`
in `internal/cli/` exists only to wire the cobra commands into the
existing `sessionCmd` parent; the actual command logic lives in the
sub-package.

### Type and Function Signatures

#### `internal/mcp/attach_state.go` (new)

```go
package mcp

// AttachState is the on-disk shape of <worktree>/.niwa/attach.state.
// It is written by niwa-attach on lock acquire and removed by the
// same process on clean exit. Stale sentinels (owner_pid is dead per
// IsPIDAlive) are detected by readers and may be opportunistically
// reaped.
type AttachState struct {
    V              int    `json:"v"`
    OwnerPID       int    `json:"owner_pid"`
    OwnerStartTime int64  `json:"owner_start_time"`
    StartedAt      string `json:"started_at"`     // RFC3339 UTC
    LockPath       string `json:"lock_path"`      // ".niwa/attach.lock"
}

// AttachAvailability is the computed projection of AttachState onto
// the SessionLifecycleState response shape. The "stale" value
// indicates the sentinel exists but the holder is dead.
type AttachAvailability string

const (
    AttachAvailable AttachAvailability = "available"
    AttachAttached  AttachAvailability = "attached"
    AttachStale     AttachAvailability = "stale"
)

// ReadAttachState returns the parsed sentinel and a derived
// availability. Returns (nil, AttachAvailable, nil) when no sentinel
// exists. Returns (state, AttachStale, nil) when the sentinel exists
// but owner_pid is dead. Returns (state, AttachAttached, nil) when
// owner_pid is alive.
//
// reapStale, when true, deletes a stale sentinel before returning.
// Best-effort: deletion failure is logged but not returned.
func ReadAttachState(worktreePath string, reapStale bool) (*AttachState, AttachAvailability, error)

// WriteAttachState atomically writes the sentinel. Atomic via
// tmp+rename, mode 0600.
func WriteAttachState(worktreePath string, state AttachState) error

// RemoveAttachState removes the sentinel file. Idempotent: missing
// file returns nil, not an error.
func RemoveAttachState(worktreePath string) error

// AttachLockPath returns <worktreePath>/.niwa/attach.lock.
func AttachLockPath(worktreePath string) string

// AttachStatePath returns <worktreePath>/.niwa/attach.state.
func AttachStatePath(worktreePath string) string
```

#### `internal/mcp/session_lifecycle.go` (modified)

Add a computed projection field to the response shape. The on-disk
JSON is unchanged (no V bump); the field is populated by the
list-sessions handler at projection time, not by `WriteSessionLifecycleState`.

```go
type SessionLifecycleState struct {
    // ... existing fields unchanged ...

    // Attach is a computed projection from the worktree's attach.state
    // sentinel. Set by handlers that project lifecycle state into a
    // response (niwa_list_sessions, niwa session list); never written
    // to disk. JSON tag uses ",omitempty" so the field is omitted from
    // the JSON response (not null) when no lock is held.
    Attach *AttachState `json:"attach,omitempty"`
}
```

The `,omitempty` tag combined with a `*AttachState` pointer means a
nil pointer marshals as the absent key, matching PRD R12's "absent,
not null" requirement.

#### `internal/cli/sessionattach/preflight.go` (new)

```go
package sessionattach

// EncodeProjectDir applies Claude Code's project-dir encoding rule
// (s/[^A-Za-z0-9]/-/g, leading "/" -> leading "-") to the absolute
// CWD. Empirically verified against claude v2.1.138.
func EncodeProjectDir(cwd string) string

// TranscriptPath returns the deterministic claude transcript path
// for a given worktree (the worker's CWD = <worktree>/<repo_name>)
// and conversation id.
func TranscriptPath(homeDir, workerCWD, convID string) string

// PreflightError represents a pre-flight validation failure with the
// case identifier (A/B/C) used to select the user-visible message.
type PreflightError struct {
    Case  rune    // 'A', 'B', or 'C'
    Path  string  // for B/C: the expected transcript path
    State *mcp.SessionLifecycleState
}

func (e *PreflightError) Error() string

// Preflight validates that a session is attachable: claude_conversation_id
// is non-empty, the transcript file exists, the file has non-zero size.
// Returns *PreflightError on failure (callers format the user-visible
// message via the Error() method, which produces the verbatim PRD R4
// strings).
func Preflight(state mcp.SessionLifecycleState) error
```

#### `internal/cli/sessionattach/attach.go` (new)

```go
package sessionattach

// Run executes the attach command. Holds the flock for the lifetime
// of the spawned claude process; returns the propagated exit code
// (capped at 125) on clean detach.
func Run(ctx context.Context, opts Options) error

type Options struct {
    InstanceRoot string
    SessionID    string
    Force        bool       // --force: SIGTERM running worker
    Stdin        io.Reader  // typically os.Stdin
    Stdout       io.Writer
    Stderr       io.Writer
}
```

The command's logical sequence (see Sequence Diagram below):

1. Read lifecycle state (`mcp.ReadSessionLifecycleState`); validate
   `Status == "active"` per PRD R2 (else exit 1).
2. Acquire `flock(<worktree>/.niwa/attach.lock, LOCK_EX|LOCK_NB)` per
   PRD R3 (else exit 3 with the lock-contention error).
3. Wait for or kill the running worker per PRD R6 (Force vs poll).
4. Pre-flight transcript validation per PRD R4 (else exit 1 with the
   case-specific message; release the lock).
5. `TerminateDaemon(worktreePath)`.
6. `WriteAttachState(worktreePath, AttachState{...})`.
7. Spawn `claude --resume <conv_id>` with `cmd.Dir = workerCWD`,
   stdio inherited.
8. Block on `cmd.Wait()`.
9. On exit (clean or signal):
   - `RemoveAttachState(worktreePath)`.
   - `EnsureDaemonRunning(worktreePath, nil)`.
   - Compute and emit worktree-state warnings per PRD R20.
   - Release the flock (implicit via fd close on niwa process exit).
   - Return claude's exit code, capped at 125.

#### `internal/cli/sessionattach/detach.go` (new)

```go
// Run executes the detach command. Returns nil on a clean release
// (exit 0); returns an error with a non-zero exit code per the PRD
// Exit Code Mapping otherwise.
func Run(ctx context.Context, opts DetachOptions) error

type DetachOptions struct {
    InstanceRoot string
    SessionID    string
    Force        bool       // --force: SIGTERM live holder
    Stdout       io.Writer
    Stderr       io.Writer
}
```

Logical sequence:

1. Read lifecycle state. Resolve worktree path.
2. Read `attach.state`. If absent, exit 0 (no lock to break).
3. Check holder liveness via `IsPIDAlive(state.OwnerPID, state.OwnerStartTime)`.
4. If dead, remove sentinel and exit 0 (auto-recovery path; no
   `--force` needed).
5. If alive and `--force` not set, return error with exit 3 (lock
   contention) and the holder details.
6. If alive and `--force` set: SIGTERM the holder, wait
   `NIWA_DESTROY_GRACE_SECONDS`, SIGKILL if needed, remove sentinel,
   exit 4 (force operation killed live holder per PRD Exit Code
   Mapping).

#### `internal/mcp/handlers_session.go` (modified)

```go
// In handleDestroySession, before any teardown:
attachState, attachAvail, _ := ReadAttachState(state.WorktreePath, false)
if attachAvail == AttachAttached && !args.Force {
    return errorResponse("SESSION_ATTACHED", fmt.Sprintf(
        "session %s is currently attached (pid=%d, started=%s); "+
        "run `niwa session detach %s --force` to release the lock first, "+
        "or pass force=true to destroy anyway",
        state.SessionID, attachState.OwnerPID, attachState.StartedAt,
        state.SessionID))
}
// ... existing destroy sequence proceeds, including with attached session
//     when force=true (the existing kill-workers + remove-worktree flow
//     also kills the attach holder via the worktree-removal cascade).
```

#### `internal/mcp/server.go` (modified)

`niwa_list_sessions` accepts two new optional input fields and returns
the projected `Attach` field. The handler:

```go
// In handleListSessions:
states, _ := ListSessionLifecycleStates(sessionsDir)
filtered := make([]SessionLifecycleState, 0, len(states))
for _, st := range states {
    // Project Attach
    attachState, attachAvail, _ := ReadAttachState(st.WorktreePath, true /* reap */)
    if attachAvail == AttachAttached {
        st.Attach = attachState
    }
    // ... apply filters: repo, status, attached, available
    if args.Attached && attachAvail != AttachAttached { continue }
    if args.Available && attachAvail != AttachAvailable { continue }
    if args.Repo != "" && st.Repo != args.Repo { continue }
    if args.Status != "" && st.Status != args.Status { continue }
    filtered = append(filtered, st)
}
return jsonResponse(filtered)
```

#### `internal/cli/session_lifecycle_cmd.go` (modified)

Three changes to `runSessionLifecycleList`:

1. **Project AVAILABILITY** by calling `ReadAttachState` per row.
2. **Sort** by the composite key from PRD R17 (attached first by
   `started_at`, then by status, then by `creation_time` desc).
3. **Render** the new column (between STATUS and CREATED). Header
   `AVAILABILITY` (12 chars wide). Values `available`/`attached`/`stale`.

Three changes to `sessionListCmd`:

1. Add `--attached` and `--available` boolean flags.
2. Plumb both through `runSessionLifecycleList`.
3. (Separately, in `session.go`:) **Remove** the deprecated alias
   path. Flagless `niwa session list` now calls
   `runSessionLifecycleList` directly.

### Sequence: Attach Acquire (Happy Path)

```
operator                niwa-attach            mcp.ReadAttachState     daemon
   │                         │                        │                   │
   │ niwa session attach <id>│                        │                   │
   │────────────────────────▶│                        │                   │
   │                         │ ReadSessionLifecycleState                  │
   │                         │ ─── validate status==active ──────────────▶│
   │                         │                                            │
   │                         │ flock(attach.lock, EX|NB) ─── OK ──────────│
   │                         │                                            │
   │                         │ scan tasks for running worker ─── none ────│
   │                         │                                            │
   │                         │ Preflight(state) ─── transcript OK ────────│
   │                         │                                            │
   │                         │ TerminateDaemon(worktree) ─────────────────│ exit
   │                         │                                            │
   │                         │ WriteAttachState(worktree, {pid,...})      │
   │                         │                                            │
   │                         │ exec.Cmd("claude", "--resume", convID)     │
   │                         │   cmd.Dir = workerCWD                      │
   │                         │   cmd.Stdin/Stdout/Stderr = os.*           │
   │                         │   cmd.Run() (blocks)                       │
   │  ◀── claude TUI ────────────────────────────────────────────────────▶│
   │                         │                                            │
   │  /exit                  │ ─── claude returns exit 0                  │
   │                         │                                            │
   │                         │ RemoveAttachState(worktree)                │
   │                         │ EnsureDaemonRunning(worktree, nil) ────────│ start
   │                         │ emit worktree warnings (if any)            │
   │                         │ flock released (process exits)             │
   │  ◀── exit 0 ────────────│                                            │
```

### Sequence: Attach Wait-for-Worker (No --force)

```
operator                niwa-attach            taskstore           worker
   │                         │                     │                  │
   │ niwa session attach <id>│                     │                  │
   │────────────────────────▶│                     │                  │
   │                         │ flock acquired      │                  │
   │                         │                     │                  │
   │                         │ list tasks where    │                  │
   │                         │ envelope in inbox  │                  │
   │                         │ AND state=running ──▶                  │
   │                         │ ◀── 1 task ─────────│                  │
   │                         │                     │                  │
   │                         │ poll every 1s       │                  │
   │ ◀── stderr line every 5s│                     │                  │
   │                         │                     │ ◀── worker exits │
   │                         │ poll: state=complete                   │
   │                         │ ◀── 0 tasks ────────│                  │
   │                         │                     │                  │
   │                         │ proceed to preflight + spawn ...       │
```

A SIGINT (Ctrl-C) during the poll causes niwa-attach's signal handler
to release the flock and exit non-zero (no daemon termination has
happened yet at this point in the sequence).

### Sequence: Detach with Stale Lock

```
operator                niwa-detach            ReadAttachState
   │                         │                       │
   │ niwa session detach <id>│                       │
   │────────────────────────▶│                       │
   │                         │ ReadAttachState ──────▶
   │                         │ ◀── state, AttachStale│
   │                         │                       │
   │                         │ RemoveAttachState ────▶ rm sentinel
   │ ◀── exit 0 ─────────────│                       │
```

### Failure Modes and Error Wrapping

| Failure | Where caught | Action | Exit code |
|---------|--------------|--------|-----------|
| Session not found | `ReadSessionLifecycleState` returns error | Emit `niwa: error: session <id> not found` | 1 |
| Wrong status | Status check after read | Emit PRD R2 error | 1 |
| Lock held by live process | `flock` returns EWOULDBLOCK + `IsPIDAlive` returns true | Emit PRD R3 error | 3 |
| Lock held by dead process | `flock` returns EWOULDBLOCK but `IsPIDAlive` false | Reap sentinel, retry flock once | retry → success or 3 |
| Pre-flight case A (no conv_id) | `Preflight` returns `&PreflightError{Case: 'A'}` | Emit PRD R4 case-A error | 1 |
| Pre-flight case B (transcript missing) | Stat returns ENOENT | Emit PRD R4 case-B error | 1 |
| Pre-flight case C (transcript empty) | Stat returns size 0 | Emit PRD R4 case-C error | 1 |
| Cross-UID (EACCES on state read) | `os.ReadFile` returns EACCES | Wrap per PRD R26 | 1 |
| Worker won't terminate after SIGKILL | `cmd.Wait` after grace period | Log error, exit 1 (rare) | 1 |
| Daemon respawn fails on detach | `EnsureDaemonRunning` returns error | Log warning to stderr, still exit with claude's code | propagated |
| `claude` binary missing | `exec.LookPath` or `cmd.Start` error | Emit `niwa: error: claude binary not found in PATH` | 1 |
| Claude exits non-zero | `cmd.Run` returns `*exec.ExitError` | Propagate exit code, capped at 125 | 1-125 |
| SIGINT during attach wait | signal handler | Release lock, exit 130 | 130 |

### Stale-Lock Reaping by Readers

`ReadAttachState(worktree, reapStale=true)` is called by three readers:

1. **`niwa session list`**: passes `reapStale=true` so the listing reflects
   reality even if a previous attach died without cleaning up.
2. **`niwa session attach`**: on initial flock failure, calls
   `ReadAttachState(worktree, reapStale=true)`. If the result is
   `AttachStale`, the sentinel is reaped and flock is retried once. If
   the retry succeeds, the attach proceeds normally (treating the
   stale sentinel as already released).
3. **`niwa session detach`**: passes `reapStale=true` for the
   stale-recovery path.

`niwa_list_sessions` MCP handler also passes `reapStale=true` so
coordinator polls naturally drain stale sentinels.

The reap is strictly opportunistic: deletion failure is logged but
does not propagate as an error.

### Daemon Coordination Sequencing

niwa today has two daemon roles:

- **Main-instance daemon** (`<instance>/.niwa/daemon.pid`): handles
  the workspace's coordinator role.
- **Per-worktree daemon** (`<worktree>/.niwa/daemon.pid`): handles
  the session worktree's role inbox.

Attach acts on the **per-worktree daemon only**. The main-instance
daemon is unaffected — it never claims envelopes for session
worktrees. This means:

- `TerminateDaemon(worktreePath)` (NOT `TerminateDaemon(instanceRoot)`).
  The function takes a daemon's "instance root" which for session
  worktrees IS the worktree path.
- `EnsureDaemonRunning(worktreePath, nil)` on detach release.
- The `extraEnv` parameter is `nil` because the per-worktree daemon
  inherits its env from the spawning process; on respawn after
  detach, the env is whatever was originally set when the session
  was created (already in the niwa env at this point).

### Path Encoding Implementation

```go
// EncodeProjectDir applies the s/[^A-Za-z0-9]/-/g substitution rule.
func EncodeProjectDir(cwd string) string {
    var b strings.Builder
    b.Grow(len(cwd))
    for _, r := range cwd {
        if (r >= 'A' && r <= 'Z') ||
           (r >= 'a' && r <= 'z') ||
           (r >= '0' && r <= '9') {
            b.WriteRune(r)
        } else {
            b.WriteByte('-')
        }
    }
    return b.String()
}
```

The leading `/` of an absolute CWD becomes a leading `-` automatically
under this rule, matching the empirical behaviour observed against
claude v2.1.138.

### CLI Output Format (Reference)

```
$ niwa session list
  SESSION_ID   REPO         STATUS     AVAILABILITY  CREATED              PURPOSE
  ef56gh78     niwa         active     attached      30s ago              pair-debug edge case
  0c446995     niwa         active     available     2m ago               long-running learning log
  ab12cd34     niwa         active     available     5m ago               implement attach feature
```

Column widths: SESSION_ID 12, REPO 12, STATUS 10, AVAILABILITY 12,
CREATED 20, PURPOSE truncated at 40. Two-space leading indent matches
existing `niwa session list` and `niwa mesh list` formatting.

```
$ niwa session attach ef56gh78
session: attached ef56gh78 at /home/op/work/niwa-1/.niwa/worktrees/niwa-ef56gh78
[claude TUI takes over]
[user types, claude responds, user types /exit]
session: detached ef56gh78
```

```
$ niwa session detach 0c446995 --force
warning: detaching live attach holder pid=12345 started=2026-05-10T14:32:11Z
session: detached 0c446995
```

```
$ niwa session attach <id>          # session is currently attached
niwa: error: session <id> is already attached (pid=12345, started=2026-05-10T14:32:11Z).
Run `niwa session detach <id> --force` to break the lock if the holder is gone.
$ echo $?
3
```

## Implementation Approach

The work is structured as 12 small, independently-testable units. The
plan that follows this design will sequence them; rough dependency
order:

1. **`internal/mcp/attach_state.go`**: type, read/write/remove/path
   helpers. Unit-test against tmp dirs.
2. **`internal/mcp/session_lifecycle.go`**: add the `Attach` projection
   field. No serialization tests (existing tests cover round-trip).
3. **`internal/cli/sessionattach/preflight.go`**: encoding,
   `TranscriptPath`, `PreflightError`, `Preflight`. Unit tests with
   fake fixtures.
4. **`internal/cli/sessionattach/worktree_warnings.go`**: shells out
   to git for status, untracked, ahead-count. Unit tests using
   `localGitServer` from `test/functional/`.
5. **`internal/cli/sessionattach/supervise.go`**: spawn claude (or a
   test stand-in), wait, propagate exit. Signal-handler installs.
6. **`internal/cli/sessionattach/detach.go`**: detach command logic.
7. **`internal/cli/sessionattach/attach.go`**: attach command logic
   (the biggest chunk; depends on 1-6).
8. **`internal/cli/session_attach_register.go`**: cobra wiring.
9. **`internal/cli/session_lifecycle_cmd.go`**: AVAILABILITY column,
   sort, filters.
10. **`internal/cli/session.go`**: remove deprecated alias.
11. **`internal/mcp/handlers_session.go`** + **`server.go`**: destroy
    gate, list filters, MCP filter input.
12. **`docs/guides/sessions.md`** + **`test/functional/features/session_attach.feature`**:
    docs and `@critical` Gherkin scenario.

Each unit gets its own commit with passing tests. The plan file will
encode this as 12 sequenced atomic units of work.

## Security Considerations

The implementation introduces no new privileged operations. Every
primitive is built from existing helpers that have already been
security-reviewed (`flock`, `IsPIDAlive`, `TerminateDaemon`, atomic
tmp+rename file writes, `os.OpenFile` with mode 0600, and forking a
child process from the foreground).

### Threat Model

The same trust model applies as for the rest of niwa: **same-UID
cooperative trust** (declared in `DESIGN-cross-session-communication.md`).
All files niwa creates are mode 0600, owned by the workspace owner.
Cross-UID access produces EACCES from the kernel, which niwa wraps
in a friendly message per PRD R26.

### Specific Considerations

1. **Path traversal in session_id**: sessions IDs are validated
   against `^[0-9a-f]{8}$` by `sessionIDRe.MatchString` before any
   path is constructed. The new attach commands inherit this guard
   by going through `mcp.ReadSessionLifecycleState`. The
   `worktree_path` returned by the lifecycle state is treated as a
   filesystem path that niwa already wrote and trusts.

2. **Process injection via `claude` argv**: the convID passed to
   `claude --resume` is a captured value from
   `state.ClaudeConversationID`. Claude validates that this is a
   UUID per its own help text (`claude --session-id <uuid>`
   "must be a valid UUID"). niwa does not need to re-validate, but
   the empty-string case is rejected by pre-flight case A. UUIDs
   contain no shell metacharacters; passing as `argv[2]` of
   `exec.Cmd` is safe.

3. **Symlink races on the lock file**: `flock` operates on a file
   descriptor, not a path. `os.OpenFile(<lock_path>, O_CREATE|O_RDWR, 0o600)`
   returns the fd; `flock(fd)` is unaffected by a subsequent
   symlink swap. Same-UID cooperative trust means an attacker who
   could swap files inside `<worktree>/.niwa/` is already inside the
   trust boundary.

4. **TOCTOU between `IsPIDAlive` check and reap**: a stale-lock
   reap that races with a simultaneous attach by a (legitimate)
   second operator is not a security issue — both processes share
   UID and goal. The flock provides mutual exclusion at the actual
   acquire.

5. **`--force` destruction of an active attach**: this is an
   intentional operator action that requires either the operator
   typing `--force` interactively or scripting it explicitly. Mirrors
   the existing `niwa session destroy --force` semantics. The Exit
   Code Mapping returns code 4 specifically so scripts can
   distinguish "killed live holder" from "reaped stale lock".

6. **Worker SIGTERM under `--force` (attach)**: identical to the
   existing `killSessionWorkers` path used by destroy. No new
   privilege; no new attack surface. The kill targets the worker's
   process group (`syscall.Kill(-pid, SIGTERM)`) because workers
   spawn with `Setsid=true`.

7. **No new IPC, no new sockets, no new network listeners**. Attach
   is a foreground CLI process spawning claude as a child; it has
   no network presence and creates no Unix sockets. The MCP additions
   are filter parameters and a sub-object on existing tool responses
   — no new tools, no new endpoints.

8. **Sentinel write contention**: only the niwa-attach process holds
   the flock, so only one writer ever touches `attach.state` at a
   time. The atomic tmp+rename pattern means a partial sentinel
   cannot be observed; readers either see the previous state or the
   complete new state, never an intermediate.

### What This Design Does NOT Add

- No new MCP tools (PRD direction; reaffirmed by exploration's MCP
  surface review).
- No new privileged operations (no setuid, no capabilities).
- No new daemon (the per-worktree daemon is the existing one;
  attach merely terminates and respawns it).
- No new network or socket surface.
- No new authentication or authorization layer (single-UID trust
  model is unchanged).

The security review verdict is: **no new attack surface; uses
existing, reviewed primitives only**.

## Consequences

### Positive

- The mesh gains a missing primitive (human-in-the-loop) that closes
  the destroy-or-trust binary.
- Existing daemon orchestration is reused, so the catch-up replay
  path (which is well-tested) handles envelope queuing and
  post-detach delivery for free.
- Schema additions are computed projections, so PR #115's
  mesh-reliability work and this PR can land in either order without
  collisions.
- Three distinct niwa-shaped error messages turn claude's opaque
  "No conversation found with session ID" into actionable operator
  feedback.
- The CLI becomes more useful even for non-attach workflows: the
  default sort by attached-first surfaces the operator's hot
  question; the AVAILABILITY column is informational regardless of
  whether the operator intends to attach.

### Negative

- The deprecated `niwa session list` (no flags) → `niwa mesh list`
  alias is removed, breaking any scripts that relied on the legacy
  fallback. Mitigated by the deprecation warning that has been live
  since DESIGN-mesh-session-lifecycle landed and by an explicit
  Compatibility and Migration section in the PRD.
- The acquire-to-launch latency budget includes a daemon-terminate
  grace period (default 5s with `NIWA_DESTROY_GRACE_SECONDS`).
  Operators on slow machines or long-running grace periods will feel
  this. Mitigation: the env var is operator-tunable, and the
  Timing-and-Limits table makes the budget explicit.
- Attach holds an exclusive lock for the lifetime of the human's
  Claude Code session. Two operators sharing the same workspace
  cannot attach to the same session simultaneously. Mitigation:
  documented as Known Limitation 4 in the PRD.
- SSH-disconnect-with-survivor is not auto-detected. Operators must
  run `niwa session detach --force` manually. Mitigation: the SIGHUP
  cascade handles the common case automatically; the rare nohup-style
  hostile-detach is recoverable via the `--force` escape hatch.
- The implementation adds ~600-800 LoC of Go (one new sub-package
  with three command files, three helper files, and their tests, plus
  ~200 LoC of modifications to existing files). Net code growth is
  modest; new package boundary is clean.

### Mitigations

| Risk | Mitigation |
|------|-----------|
| Operator confused by `--force` semantic asymmetry | PRD Decision D3 calls it out explicitly; help text on each flag pins the meaning. |
| Stale lock from PID recycling in long-lived workspaces (non-Linux) | PRD Known Limitation 2 + R25 fallback (signal-0 only on non-Linux). |
| Daemon respawn fails on detach (rare; usually disk full or permissions) | Warning printed to stderr; attach still exits with claude's code; operator can manually `niwa apply` to trigger respawn. |
| Concurrent attach attempts | Reject fast (PRD AC10, AC12b); operator sees the holder details and the recovery command. |
| Claude version drift in path encoding | Pre-flight is a UX layer, not a safety layer; if the encoding changes in a future Claude release, attach degrades to "claude returns its own error" rather than silently failing. PRD R4 is explicit about this. |

### Downstream Implications

- The Plan that follows this design will produce 12 atomic units of
  work, sequenced per Implementation Approach.
- The implementation lands on the same branch as this design (single
  PR per the user's instruction).
- After merge, `docs/guides/sessions.md` will document the new
  primitive; the niwa-mesh skill (delivered via `tsuku`) does not
  need updates because attach is operator-only and not an agent
  primitive.
