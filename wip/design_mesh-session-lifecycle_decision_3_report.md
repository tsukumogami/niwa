# Decision 3: Session Lifecycle State Schema

**Topic:** mesh-session-lifecycle
**Question:** What is the session lifecycle state schema — file layout, data model, and writer/reader
contract for session creation, update, and destruction?

---

## Context

Two separate concerns currently share the same directory (`<instance>/.niwa/sessions/`) but must
not be conflated:

1. **Coordinator process registry** (`sessions.json`): records which coordinator process is live
   — its PID, start time, role, inbox path. Written by `WriteSessionEntry`, read by
   `lookupLiveCoordinator`. This is about mesh process liveness, not about sessions as units of
   work.

2. **Session lifecycle registry** (this decision): records worktree-based sessions — their state
   (`active`/`ended`/`abandoned`), parent-child tree structure, Claude conversation ID, worktree
   path, and stale indicator.

The PRD already fixes the file layout: per-session files at
`<instance>/.niwa/sessions/<session-id>.json`. The key sub-question is how the two registries
coexist without reader/writer coupling and without either registry corrupting the other under
concurrent writes.

The sessions directory contains only two kinds of files:
- `sessions.json` — coordinator process registry (existing, untouched by this feature)
- `<8-hex>.json` — per-session lifecycle state files (new)

These are distinguishable by name, so scanners can read one class without seeing the other.

---

## Key Assumptions

- The filesystem is shared across all worktree daemon instances. A session file written by the
  main-instance daemon must be readable by any per-worktree daemon without additional IPC.
- Session files survive host reboots. No in-memory cache is authoritative; every reader re-reads
  from disk.
- Multiple daemons may write different session files concurrently (one per session), but no two
  daemons ever write the _same_ session file simultaneously. The session owner (the daemon that
  created it) is the only writer for updates; terminal state is written once and never updated.
- The coordinator PID stored in a session file is the PID of the coordinator process that called
  `niwa_create_session`. Stale detection uses the same `IsPIDAlive(pid, start_time)` pattern
  already in `internal/mcp/liveness.go`.
- Session IDs are 8 lowercase hex characters (R22). This is distinct from the UUID format that
  `NewSessionID()` currently generates for coordinator registry entries; the session lifecycle
  layer needs its own ID generator.
- Terminal states (`ended`, `abandoned`) are written once and never updated; readers may safely
  cache a terminal state indefinitely.

---

## Options

### Option A: Per-session JSON files (one file per session)

Each session gets its own `<session-id>.json` file at
`<instance>/.niwa/sessions/<session-id>.json`.

**Write path:** session creation writes a new file; updates overwrite the file atomically (temp
file + rename). The owning daemon is the only writer for that file, so there is no concurrent
writer conflict for a given session file. `niwa_list_sessions` scans the directory, skips
`sessions.json` by name, and reads every `<8-hex>.json` file.

**Advantages:**
- Atomic writes are cheap and safe: temp file + rename is an O(1) syscall sequence and is
  crash-safe on all POSIX filesystems.
- No inter-daemon coordination required: each daemon only writes files it owns.
- The existing `sessions.json` is completely isolated by filename; no code touches both in the
  same read/write path.
- Deleting a session (M6 policy question) is a single `os.Remove` call.
- Session discovery after a coordinator restart is a single `os.ReadDir` + filter.
- Adding a field to one session does not require touching any other session's file.

**Disadvantages:**
- Listing all sessions requires N file reads instead of one. At typical session counts (< 20 per
  instance) this is not a real cost.
- Two sessions created in the same millisecond by the same coordinator could theoretically get
  the same 8-hex ID, though the ID space (4 billion values) makes this negligible. The writer
  should check for collision before returning the ID.

### Option B: Single mesh-sessions.json

All sessions in one file, keyed by session ID.

**Write path:** every session creation or update requires a read-modify-write on the entire file.
Under concurrent writes (two sessions updated simultaneously), both daemons may read stale data
and the second writer silently clobbers the first's change.

Making this safe requires either:
- A file lock (`flock`) held for the duration of read-modify-write, serializing all session
  writes across the entire instance.
- An atomic write-replace that loses any concurrent write (last-writer-wins semantics, which is
  incorrect for independent sessions being updated simultaneously).

The `WriteSessionEntry` function for `sessions.json` uses atomic rename, which is correct there
because only one coordinator process writes to it at a time. Session lifecycle updates do not
have this property: multiple worktree daemons can be running simultaneously, each updating their
own session independently.

**Advantages:**
- A single file read returns all sessions.
- Simpler glob patterns for backup/copy operations.

**Disadvantages:**
- Concurrent writes from multiple worktree daemons require a shared lock. `flock` is available
  on Linux and macOS, but holding it for the duration of a read-modify-write means every session
  update serializes globally, even when sessions are entirely independent.
- Corruption of one session's JSON entry (e.g., from a partial write) can make the entire file
  unparseable, losing visibility into all sessions.
- The file name (`mesh-sessions.json`) is close enough to `sessions.json` to cause confusion in
  code review and tooling.

### Option C: Extend sessions.json

Add a `sessions` map to the existing coordinator registry file, reusing `WriteSessionEntry` and
the existing read/write path.

**Advantages:**
- No new file format.
- `WriteSessionEntry` already handles atomic rename.

**Disadvantages:**
- Conflates two unrelated concepts: coordinator process liveness (ephemeral, PID-keyed) and
  session lifecycle (persistent, session-ID-keyed). A reader scanning for coordinator entries
  must skip session entries and vice versa.
- `WriteSessionEntry` prunes stale entries by role during every write. Adapting it to handle
  session lifecycle entries without pruning them on coordinator PID death requires branching on
  entry type, making the function harder to reason about.
- Adding session lifecycle state increases the size of every coordinator registry read — a hot
  path called on every `niwa_ask` routing decision.
- The coordinator registry's `ErrAlreadyRegistered` semantics do not apply to session lifecycle
  entries. Sharing one function for both means the caller must suppress semantically wrong errors.

---

## Chosen: Option A — Per-session JSON files

### Rationale

Option A matches the layout the PRD already specifies, keeps the two registries physically
separate and conceptually independent, and requires no shared locking. The only multi-daemon
write scenario is multiple sessions being created concurrently by the same coordinator; each
daemon writes a distinct file, so concurrent writes to different files are already safe. Within
a single session, the owning daemon is the sole writer — atomic rename is sufficient.

The coordinator process registry (`sessions.json`) is untouched. Its read/write path, its
`IsPIDAlive`-based stale pruning, and its `ErrAlreadyRegistered` semantics remain exactly as
they are today. The new session lifecycle code never reads or writes `sessions.json`.

Scanners distinguish the two file types by name: `sessions.json` matches no 8-hex-char pattern,
and `<8-hex>.json` files are never named `sessions.json`. The directory listing loop is a
one-line filter: `regexp.MustCompile("^[0-9a-f]{8}\\.json$")`.

### Go Struct Definition

```go
// SessionLifecycleState is the on-disk schema for a per-session lifecycle state
// file at <instance>/.niwa/sessions/<session-id>.json.
//
// This type is distinct from SessionEntry (the coordinator process registry).
// The two types share no fields and are written by separate code paths.
//
// Schema version v=1. Fields marked omitempty are absent until set.
type SessionLifecycleState struct {
    V                   int      `json:"v"`                              // schema version, always 1
    SessionID           string   `json:"session_id"`                     // 8 lowercase hex chars (R22)
    ParentSessionID     string   `json:"parent_session_id,omitempty"`    // absent for root sessions
    Children            []string `json:"children,omitempty"`             // direct child session IDs
    Repo                string   `json:"repo"`                           // repo identifier within instance
    Purpose             string   `json:"purpose"`                        // up to 256 UTF-8 chars (R21)
    Status              string   `json:"status"`                         // "active", "ended", "abandoned"
    CreationTime        string   `json:"creation_time"`                  // RFC3339 UTC
    WorktreePath        string   `json:"worktree_path"`                  // absolute path to worktree root
    ClaudeConversationID string   `json:"claude_conversation_id,omitempty"` // captured after first worker
    CreatorPID          int      `json:"creator_pid"`                    // coordinator PID at creation
    CreatorStartTime    int64    `json:"creator_start_time"`             // for IsPIDAlive stale detection
    Stale               bool     `json:"stale,omitempty"`                // true if creator PID is dead
    PRUrl               string   `json:"pr_url,omitempty"`               // reserved for follow-on
}
```

### Session ID Generator

The PRD requires 8 lowercase hex characters (R22), which differs from the UUID format that
`NewSessionID()` currently produces for coordinator registry entries. A new function is needed:

```go
// newSessionLifecycleID returns a random 8-character lowercase hex string
// suitable for use as a session ID per PRD R22.
func newSessionLifecycleID() string {
    var b [4]byte
    _, _ = rand.Read(b[:])
    return fmt.Sprintf("%08x", b)
}
```

Callers must check for an existing file at the candidate path before returning the ID; if a
collision is found, generate a new ID and retry (expected to be extremely rare).

### Atomic Write Mechanism

All writes use temp file + rename:

```go
// WriteSessionLifecycleState atomically persists state to
// <sessionsDir>/<state.SessionID>.json. Safe to call concurrently
// provided no two callers write the same session ID.
func WriteSessionLifecycleState(sessionsDir string, state SessionLifecycleState) error {
    target := filepath.Join(sessionsDir, state.SessionID+".json")
    data, err := json.MarshalIndent(state, "", "  ")
    if err != nil {
        return err
    }
    tmp := target + ".tmp"
    if err := os.WriteFile(tmp, data, 0o600); err != nil {
        return err
    }
    return os.Rename(tmp, target)
}
```

For the `children` field: when `niwa_create_session` adds a child session ID to the parent's
`children` list, the parent's session file is updated by the coordinator's daemon process. The
coordinator daemon is the sole writer for the parent session file, so no additional locking is
required there either.

### Reader Contract

`niwa_list_sessions` reads session lifecycle state by:

1. `os.ReadDir(sessionsDir)` — enumerate all entries.
2. Filter to names matching `^[0-9a-f]{8}\.json$` — skip `sessions.json` and any `.tmp` files.
3. `os.ReadFile` each match and `json.Unmarshal` into `SessionLifecycleState`.
4. For each entry, call `IsPIDAlive(state.CreatorPID, state.CreatorStartTime)` to compute the
   live stale indicator. The `Stale` field in the file may be stale itself (set at last write);
   the live check is always authoritative.
5. Apply any caller-requested filters (`--repo`, `--status`).

Errors reading individual files are logged and skipped; a single corrupt file does not prevent
listing the rest. This matches the PRD requirement ("if the sessions registry file is missing or
corrupted, returns an empty list") generalized to the per-file model.

### Policy: Session Files After Destroy (M6)

Terminal state files (`ended`, `abandoned`) are retained on disk. Retaining them enables
post-mortem inspection — a coordinator that restarts after a crash can distinguish a cleanly
ended session from an active one without inferring state from worktree presence. The worktree is
deleted; the `.json` file stays. A future `niwa session gc` command can prune old terminal-state
files if disk accumulation becomes a concern.

---

## Rejected

### Option B: Single mesh-sessions.json

Concurrent writes from independent worktree daemons require a shared file lock or accept
last-writer-wins semantics that would clobber independent session updates. The locking overhead
serializes all session writes across the instance even when the sessions are entirely
independent. A single corrupt entry makes all sessions invisible until the file is repaired
manually.

### Option C: Extend sessions.json

Conflates coordinator process liveness with session lifecycle state. The two concepts have
different pruning semantics, different schema shapes, and different reader audiences. Merging
them into one file couples the hot path (`niwa_ask` coordinator lookup, called on every worker
question) to session lifecycle data that it does not need. It also requires the existing
`WriteSessionEntry` function to distinguish entry types and suppress `ErrAlreadyRegistered`
errors that do not apply to lifecycle entries.

---

## Consequences

**Positive:**
- `sessions.json` is completely isolated from the new session lifecycle code. No existing
  coordinator registry logic changes.
- Per-session atomicity is free: temp file + rename, no lock, no coordination.
- A single corrupt session file does not break listing other sessions.
- Terminal state files survive the session worktree, enabling post-mortem inspection.
- Adding fields to `SessionLifecycleState` is a schema migration on individual files, not a
  format change that affects all sessions simultaneously.

**Negative / watch-outs:**
- `niwa_list_sessions` performance scales linearly with session count. At the expected scale
  (< 20 sessions per instance) this is negligible, but implementations must not assume the
  list is always short.
- The `children` field in a parent's session file is maintained by the coordinator daemon on
  session creation. If the coordinator crashes mid-create, the parent's `children` list may be
  missing the new child ID. Readers should treat the child's `parent_session_id` as the
  authoritative binding and reconstruct the tree bottom-up when building the full session tree.
- The `Stale` field on disk reflects the liveness state at last write, not current reality.
  Readers must always call `IsPIDAlive` for an accurate stale indicator; the field on disk is a
  hint only.
- Session ID collisions are possible but vanishingly rare (4-billion-entry space, < 20 sessions
  typical). Writers must check before returning and retry if a collision is found.
