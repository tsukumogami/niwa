# Decision 2: Virtual Routing Targets in niwa_ask

**Topic:** mesh-session-lifecycle
**Question:** How does `niwa_ask` support virtual routing targets (`"parent"`, direct
child session-id) given that `isKnownRole` validates against role directories on disk
and virtual targets have no directory?

## Context

`handleAsk` in `internal/mcp/server.go` has two sequential gates before any routing
occurs:

1. `isKnownRole(args.To)` — stats `.niwa/roles/<args.To>/` under `s.instanceRoot`.
   If the directory does not exist, returns `UNKNOWN_ROLE` immediately.
2. `lookupLiveCoordinator(s.instanceRoot)` — reads
   `<s.instanceRoot>/.niwa/sessions/sessions.json` to find the coordinator's inbox.

In the session tree model, three routing targets must be supported:
- `"parent"` — resolved by reading `parent_session_id` from the calling session's
  state file, then routing to that session's daemon inbox.
- `<session-id>` — a direct child of the calling session; routes to the child's
  daemon inbox.
- `"coordinator"` — existing behavior, reads the coordinator entry from the main
  instance's sessions registry.

None of these virtual targets have a role directory in a session worktree. Gate 1
fires `UNKNOWN_ROLE` before any routing logic runs. Gate 2, if reached, reads the
wrong registry (the worktree's `sessions.json` instead of the main instance's).

The PRD (R19) requires `ROUTING_DENIED` for invalid targets — not `UNKNOWN_ROLE`.
The existing `"coordinator"` path must continue working. Stale parent PIDs must
return an immediate error without delivering the message.

## Key Assumptions

- Session state files at `<main_instance>/.niwa/sessions/<session-id>.json` record
  `parent_session_id` and `children[]`. The calling session's ID is known to the
  MCP server (passed at startup via env var or config, similar to `s.role`).
- The main instance root is propagated to the session-worktree daemon at spawn time
  (e.g., as `NIWA_MAIN_INSTANCE_ROOT`), separate from `s.instanceRoot` which points
  to the worktree. This is the same mechanism required for the coordinator registry
  lookup fix identified in panel review finding B3b.
- Child session IDs are those listed in the calling session's state file under
  `children[]`. The session state file is the sole source of truth for the
  parent-child binding.
- `IsPIDAlive` is available for liveness checks (already in `session_registry.go`).
- The MCP server struct gains a `sessionID` field alongside the existing `role` and
  `instanceRoot` fields. Non-session workers have `sessionID == ""`.

## Options Considered

### Option A: Registry pre-check before isKnownRole (Chosen)

Before `isKnownRole` runs, `handleAsk` checks whether `args.To` is one of the three
known virtual targets. If yes, routing is dispatched immediately via a new
`handleAskVirtual` function without touching `isKnownRole`. If no, the call falls
through to the existing `isKnownRole` gate — preserving full backward compatibility
for role-name targets.

`handleAskVirtual` resolves each target as follows:

- `"parent"`: reads the calling session's state file to get `parent_session_id`;
  reads the parent session's state file to get its daemon inbox path and coordinator
  PID; calls `IsPIDAlive`; returns immediate error if stale; writes the ask
  notification to the parent session's inbox.
- `<session-id>`: reads the calling session's state file to verify the target is in
  `children[]`; if not, returns `ROUTING_DENIED`; loads the child session's state
  file; checks liveness; writes the ask notification to the child's inbox.
- `"coordinator"`: calls `lookupLiveCoordinator(mainInstanceRoot)` using the main
  instance root rather than `s.instanceRoot` — fixes the B3b bug identified in the
  panel review, and works both inside and outside session worktrees.

Any other target falls through to `isKnownRole` as today.

`handleAsk` with `to="coordinator"` called from outside a session worktree
(`s.sessionID == ""`) uses `s.instanceRoot` directly — behavior unchanged.

**Pros:**
- Minimal diff: `handleAsk` gains one pre-check at the top; no changes to
  `isKnownRole`, `lookupLiveCoordinator`, or the existing role-routing path.
- Error codes are correct: `ROUTING_DENIED` for unauthorized targets (wrong child,
  unknown session), `UNKNOWN_ROLE` preserved for unrecognized role names via the
  existing path, `SESSION_INACTIVE` for ended/abandoned sessions.
- Liveness checks happen at routing resolution time, not at the `isKnownRole` gate.
  The gate was designed for static role registration; it is the wrong place to
  validate dynamic session topology.
- `"coordinator"` fix (`mainInstanceRoot` vs. `s.instanceRoot`) falls naturally out
  of the same pre-check, resolving B3a and B3b from the panel review in one place.
- Testable in isolation: `handleAskVirtual` can be unit-tested against fixture
  session state files without a running daemon.

**Cons:**
- `handleAsk` now has two routing code paths. The `sessionID == ""` fast-path for
  non-session workers must be documented clearly to avoid future confusion.
- Requires `s.sessionID` and `s.mainInstanceRoot` fields on the MCP server struct,
  which must be propagated from the daemon at worker spawn time.

### Option B: Virtual role directories via symlinks or stubs (Rejected)

At session worktree creation time, create stub directories under the worktree's
`.niwa/roles/` for the virtual target names:
- `.niwa/roles/parent/inbox/` → symlink to the parent session's inbox path.
- `.niwa/roles/<session-id>/inbox/` → stub directories for each child.

`isKnownRole` passes because the directories exist. The existing routing path picks
them up.

**Why rejected:**

The symlink approach conflates two separate concerns: routing authorization (who is
a valid target) and routing resolution (where is the target's inbox). `isKnownRole`
performs presence-detection; it cannot validate session liveness or confirm the
target is actually a direct child. A stale parent's symlink points to an inbox no
one watches. The caller receives no error — the ask is silently dropped.

Virtual directories also require maintenance: if a child session is created after
the parent is established, the parent's worktree needs a new stub directory. There
is no hook in the session creation path to add this. If the parent session ends, its
inbox directory disappears but the child's symlink still points to it — broken
reference, no error.

The `"coordinator"` target requires a symlink to the main instance's coordinator
inbox, but the coordinator's inbox path changes if the coordinator re-registers (it
computes `filepath.Join(instanceRoot, ".niwa", "roles", "coordinator", "inbox")`
which is stable — but only `lookupLiveCoordinator` validates liveness). A symlink
bypasses the PID check and delivers to a dead coordinator's inbox.

Session tree routing is fundamentally dynamic (parent-child binding verified from
session state, liveness checked at call time). The directory model is static
(presence-on-disk). Mapping dynamic semantics onto static directories produces
an eventually inconsistent file system that silently misbehaves rather than
returning errors.

### Option C: Parallel routing handler handleAskTree (Rejected)

Add a new `handleAskTree` function invoked first when the MCP server detects it is
running inside a session worktree (via `s.sessionID != ""`). This handler implements
tree routing entirely. The original `handleAsk` is untouched and serves as the
fallback for non-session contexts.

**Why rejected:**

Two parallel implementations of ask-routing diverge over time. Any fix to timeout
handling, task creation, awaitWaiter registration, or the `no_live_session` response
must be applied to both functions. The `"coordinator"` target must work in both
handlers, creating unavoidable duplication of `lookupLiveCoordinator` logic.

The detection mechanism (`s.sessionID != ""`) is the same condition Option A uses as
a pre-check inside `handleAsk`. Option C moves the branch point one level up
(to the function dispatch) rather than one level in (to the start of `handleAsk`).
This saves one indentation level at the cost of duplicating everything else. The
tradeoff is strictly worse.

The `handleAsk` → `handleAskVirtual` factoring in Option A achieves the same
separation without duplication: shared task creation, awaitWaiter registration, and
timeout logic live in `handleAsk`; target-specific inbox resolution lives in
`handleAskVirtual`. The only virtual-routing-specific code is in one well-bounded
function.

## Chosen Solution

**Option A: Registry pre-check before isKnownRole.**

`handleAsk` gains a pre-check that dispatches to `handleAskVirtual` when `args.To`
is `"parent"`, `"coordinator"`, or a string that matches the session-ID format. All
other values fall through to `isKnownRole` unchanged.

`handleAskVirtual` resolves the inbox directory for each virtual target and returns
it. The rest of `handleAsk` (task creation, awaitWaiter registration, timeout loop)
is unchanged and shared.

### Required struct additions

```go
// Server gains two new fields (zero values are safe: non-session workers have
// sessionID="" and mainInstanceRoot="", which disables virtual routing).
type Server struct {
    // existing fields ...
    sessionID        string // set when running inside a session worktree
    mainInstanceRoot string // main instance root; may differ from instanceRoot
                            // in session worktrees
}
```

### handleAsk sketch

```go
func (s *Server) handleAsk(args askArgs) toolResult {
    // ... validation unchanged ...

    // Pre-check: virtual routing targets bypass isKnownRole.
    if isVirtualTarget(args.To) || s.isDirectChild(args.To) {
        return s.handleAskVirtual(args)
    }

    // Existing role-directory path (unchanged).
    if !s.isKnownRole(args.To) {
        return errResultCode("UNKNOWN_ROLE", ...)
    }
    // ... existing coordinator lookup and task creation ...
}
```

### handleAskVirtual sketch

```go
func (s *Server) handleAskVirtual(args askArgs) toolResult {
    inboxDir, err := s.resolveVirtualInbox(args.To)
    if err != nil {
        // err carries the appropriate error code (ROUTING_DENIED, SESSION_INACTIVE,
        // STALE_PARENT, etc.)
        return errResultCode(err.Code, err.Error())
    }
    // Reuse shared task creation + awaitWaiter path, passing resolved inboxDir.
    // ...
}
```

### resolveVirtualInbox logic

```
switch args.To:
  "parent":
    if s.sessionID == "": return ROUTING_DENIED ("not in a session")
    parentID = readSessionState(s.mainInstanceRoot, s.sessionID).ParentSessionID
    if parentID == "": return ROUTING_DENIED ("session is a root session")
    parentState = readSessionState(s.mainInstanceRoot, parentID)
    if parentState.Status in {ended, abandoned}: return SESSION_INACTIVE
    if !IsPIDAlive(parentState.CoordinatorPID, parentState.CoordinatorStartTime):
        return STALE_PARENT ("parent session coordinator is no longer running")
    return parentState.InboxPath

  "coordinator":
    root = s.mainInstanceRoot if s.mainInstanceRoot != "" else s.instanceRoot
    inboxDir, found = lookupLiveCoordinator(root)
    if !found: return no_live_session response (existing behavior)
    return inboxDir

  <session-id>:
    if s.sessionID == "": return ROUTING_DENIED ("not in a session")
    callerState = readSessionState(s.mainInstanceRoot, s.sessionID)
    if args.To not in callerState.Children: return ROUTING_DENIED ("not a direct child")
    childState = readSessionState(s.mainInstanceRoot, args.To)
    if childState.Status in {ended, abandoned}: return SESSION_INACTIVE
    return childState.InboxPath
```

## Consequences

**Immediate:**

- `handleAsk` backward compatibility is guaranteed: the `isKnownRole` gate and the
  coordinator-routing path are unchanged for non-virtual targets and non-session
  workers.
- `"coordinator"` from a session worktree now uses `mainInstanceRoot` instead of
  `s.instanceRoot`, fixing B3a and B3b from the panel review.
- `ROUTING_DENIED` is returned for unauthorized or unrecognized virtual targets,
  satisfying R19. `UNKNOWN_ROLE` is preserved for unrecognized role names via the
  existing path.
- Stale parents are detected at routing time via `IsPIDAlive`, satisfying R9's
  immediate-error requirement.

**Implementation dependencies:**

- `s.sessionID` and `s.mainInstanceRoot` must be set by the daemon at worker spawn
  time. The daemon already passes env vars to worker processes; two new vars
  (`NIWA_SESSION_ID`, `NIWA_MAIN_INSTANCE_ROOT`) suffice. The MCP server reads them
  at startup alongside the existing `NIWA_INSTANCE_ROOT` and `NIWA_SESSION_ROLE`
  env vars.
- Session state files must include: `parent_session_id`, `children[]` (list of
  direct child session IDs), `status`, `coordinator_pid`, `coordinator_start_time`,
  `inbox_path`. These fields are required by Decision 3 (session lifecycle schema)
  and this decision inherits that dependency.
- `readSessionState` must handle concurrent writes gracefully (atomic-rename writes
  are sufficient; the reader uses `os.ReadFile` which is atomic on Linux for files
  written via rename).

**Trade-offs accepted:**

- `handleAsk` has two routing branches. The complexity is bounded: the virtual branch
  is a pure inbox-resolution step; all shared logic (task creation, awaiting) runs
  after resolution in both branches.
- Session state files are read on every `niwa_ask` call for virtual targets. This
  is acceptable: `niwa_ask` is a blocking call with a multi-second timeout; two
  small file reads at the start of the call are not a performance concern.
