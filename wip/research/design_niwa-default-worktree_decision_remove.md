# Decision: WorktreeRemove reconciliation

How should the Claude Code WorktreeRemove hook reconcile with niwa's worktree destroy lifecycle to satisfy PRD R6 (no orphaned worktree dir or stale session record; niwa is system of record)?

---

## Findings

### 1. DestroySession semantics and guards (worktree.go:268–353)

**Lifecycle on destroy:**
- Reads session state from `<instance>/.niwa/sessions/<session-id>.json`
- Checks idempotency: if status is already `ended` or `abandoned`, returns immediately with no teardown (worktree.go:278–281)
- **Guard 1 — attach-lock check** (worktree.go:292–299): Rejects destroy if `attach.state` indicates a live holder (PID alive per `IsPIDAlive`). Guard can be bypassed with `force=true`.
- **Guard 2 — dirty-worktree check** (worktree.go:307–319): Rejects destroy if worktree has uncommitted git changes (detected via `git status --porcelain`). Guard can be bypassed with `force=true`.
- Writes terminal state (`status = ended`) to disk *before* attempting teardown (worktree.go:321–325)
- **Teardown order** (worktree.go:327–350): Runs `git worktree remove --force` (always force, since directory is about to be deleted), then `git branch -d/-D` (conditional on `force` param)
- Returns state with optional `BranchWarning` if branch deletion fails

**What guards protect:**
- Attach-lock guard: Prevents removing a worktree that a live `niwa session attach` process is actively using, protecting against data loss if the directory is deleted while in use
- Dirty-worktree guard: Prevents discarding uncommitted work (the worktree directory is deleted irreversibly by `git worktree remove --force`)

### 2. Session state model and discovery (session_lifecycle.go, session_discovery.go, attach_state.go)

**Session lifecycle state** (`<instance>/.niwa/sessions/<sid>.json`):
- `SessionID`: 8-character lowercase hex
- `Repo`: repo name (used to find repo path via `findRepoInWorkspace`)
- `Status`: `active` | `ended` | `abandoned`
- `WorktreePath`: absolute path to the worktree directory
- `BranchName`: git branch (defaults to `session/<sid>` for pre-v1.1 state files)

**How niwa maps worktree path → session ID** (session_discovery.go, session_lifecycle.go):
1. Session state is keyed by `session_id` (8-char hex); the session file path is `<sessionsDir>/<sid>.json`
2. niwa stores `WorktreePath` in the session state (absolute path)
3. To find a session by path, a caller must scan `ListSessionLifecycleStates()` and compare `WorktreePath` fields
4. There is no reverse index from path to session ID; discovery is linear scan

**Attach-lock mechanism** (attach_state.go):
- File: `<worktree>/.niwa/attach.state` (JSON)
- Holder: process PID + start time (recycle-safe check via `IsPIDAlive`)
- Lock-file sentinel: `<worktree>/.niwa/attach.lock`
- Availability states: `available` (no sentinel), `attached` (live holder), `stale` (holder dead)
- Readers can opportunistically reap stale sentinels (`ReadAttachState(..., reapStale=true)`)

**Claude session discovery** (session_discovery.go):
- Tier 1: `CLAUDE_SESSION_ID` env var (validated against regex)
- Tier 2: PPID walk: `.claude/sessions/<ppid>.json` (two levels up from niwa process, checking CWD match)
- Tier 3: Project directory scan: `.claude/projects/<base64url-cwd>/*.jsonl` sorted by mtime

### 3. Liveness and cleanup mechanism (liveness.go, attach_state.go)

**PID liveness checks:**
- `IsPIDAlive(pid, startTime)`: Combines PID existence check (Signal(0)) with start-time gate for recycle safety
- `IsProcessAlive(pid)`: PID-only check (no start-time gate), fail-closed on EPERM (different UID)

**Stale reaping:**
- `ReadAttachState(worktreePath, reapStale=true)` opportunistically deletes `attach.state` if holder is dead
- No automatic sweep or TTL mechanism exists in current codebase; reaping is reactive (on-demand when stale is detected)

**No existing sweep/liveness mechanism found:**
- There is no background `niwa worktree sweep` or similar cleanup command in the codebase
- There is no TTL-based auto-cleanup (that mechanism is described in the ADR but not yet implemented)
- Session state files in `<instance>/.niwa/sessions/` persist indefinitely, even after `status=ended`

### 4. Hook input contract

**Hook mechanics** (from worktree.go, session_lifecycle_cmd.go):
- Claude Code's hook framework will call the remove hook on session exit or subagent finish
- Hook is **non-blocking**: exit code ≠0 is logged in debug-level only, does not prevent Claude from considering the worktree removed
- Hook input is **stdin** (traditional bash hook style): likely `session_id`, `cwd`, `worktree_path` on separate lines or JSON

**Mapping back to niwa session:**
- If hook receives `session_id`: direct lookup in `<instance>/.niwa/sessions/<sid>.json`
- If hook receives `worktree_path` only: must scan `ListSessionLifecycleStates()` to find matching `WorktreePath` field

---

## Options

### Option A: Non-force destroy via `niwa worktree destroy` (accepts guards may reject)

**The call:**
```
niwa worktree destroy <session-id>
```

**Behavior:**
- Hook invokes `niwa worktree destroy <sid>` without `--force`
- DestroySession respects the dirty and attach guards
- If either guard rejects, `niwa worktree destroy` exits non-zero; the hook logs the failure at debug-level only
- Claude already considers the worktree removed from its perspective, so the failure is silent to Claude
- niwa's session state may remain `active` if destroy was rejected

**Trade-offs:**
- **Pro:** Protects uncommitted work and active attach processes (the guards were designed for this)
- **Pro:** Simple, re-uses existing safe destroy semantics
- **Con:** Orphaned session state if guards reject and Claude never retries
- **Con:** Silent orphan from Claude's perspective; developer must notice via `niwa worktree list`
- **Con:** Does not satisfy PRD R6 (niwa is system of record) if attach-guard or dirty-guard causes rejection

### Option B: Force destroy via `niwa worktree destroy --force` (always succeeds)

**The call:**
```
niwa worktree destroy <session-id> --force
```

**Behavior:**
- Hook invokes `niwa worktree destroy <sid> --force`
- Bypasses both the attach-lock and dirty-worktree guards
- DestroySession always completes: marks status `ended`, removes worktree dir, deletes branch
- If attach-lock is held, the `niwa session detach --force` command is not run first, so the lock file may remain after worktree removal
- If worktree has uncommitted work, the work is discarded (git worktree remove --force deletes the directory)

**Trade-offs:**
- **Pro:** Always succeeds; niwa session state is always updated to `ended`
- **Pro:** Satisfies PRD R6 from niwa's perspective (no stale session record left behind)
- **Pro:** Simple, single invocation
- **Con:** Discards uncommitted work silently (the work is in the worktree dir being deleted)
- **Con:** Orphans attach-lock sentinel on disk; if process is still alive but forgotten, it holds a stale lock
- **Con:** Violates the intent of the dirty and attach guards (they exist to prevent data loss)

### Option C: Detach-only, mark for later cleanup (non-force)

**The call:**
```
niwa session detach <session-id> --force
# Then (optionally) mark the session for later cleanup
```

**Behavior:**
- Hook invokes `niwa session detach <session-id> --force` to release the attach lock if held
- Does not call `niwa worktree destroy`; leaves the session in `active` status
- Optionally writes a sentinel file (e.g., `<instance>/.niwa/sessions/<sid>.cleanup-pending`) to mark for later cleanup
- A future background sweep or manual `niwa worktree destroy` can clean up marked sessions

**Trade-offs:**
- **Pro:** Does not discard uncommitted work; developer can inspect the worktree before cleanup
- **Pro:** Does not bypass attach-lock guard; if process is alive, lock remains
- **Pro:** Leaves decision to human developer (recovery friendly)
- **Con:** Does not immediately satisfy PRD R6 (orphaned worktree dir and session remain until manual cleanup)
- **Con:** Requires a sweep mechanism (not yet implemented) to complete the cleanup
- **Con:** Multiple orphaned worktrees accumulate over time without active cleanup

### Option D: DETACH first (release lock), then destroy with guards (best-effort)

**The call:**
```
niwa session detach <session-id> --force  # Release any attach lock
niwa worktree destroy <session-id>        # Destroy with guards (safe teardown)
```

**Behavior:**
- Hook first removes the attach-lock sentinel via `niwa session detach --force`, clearing the holder
- Then calls `niwa worktree destroy <sid>` without `--force`
- If detach fails, the hook continues anyway (best-effort)
- If destroy is then rejected by dirty-guard, the session remains `active` and the worktree lingers
- If destroy succeeds, session is cleanly marked `ended`

**Trade-offs:**
- **Pro:** Clears the attach-lock threat (live holder can no longer block teardown)
- **Pro:** Still respects dirty-worktree guard (protects uncommitted work in the common case)
- **Pro:** Safer than pure force (less data loss risk)
- **Con:** Two invocations; failure modes are split (detach might fail, destroy might fail)
- **Con:** Still orphans if dirty-guard rejects (session remains `active`)
- **Con:** More complex; requires error handling for both detach and destroy

---

## Recommendation

**Option B: Non-force destroy via `niwa worktree destroy <session-id> --force`**

**Why:**

1. **Satisfies PRD R6 deterministically.** Every invocation completes; niwa's session state always transitions to `ended`, the worktree directory is always removed, and the branch is always deleted (or tagged-archived if that mechanism is added later). No stale session record or orphaned worktree directory remain from Claude's perspective.

2. **Simple hook contract.** Single, deterministic invocation; no multi-step error handling or retry logic. The hook is non-blocking by design, so it should be simple enough that a single call covers the common case.

3. **Aligns with the intended use.**
   - The guards (attach-lock, dirty-worktree) were designed for developer-initiated destroy via CLI (`niwa worktree destroy` from the terminal), where the developer can interactively decide what to do.
   - Agent-initiated removal is fundamentally different: Claude is tearing down its own worktree as part of task cleanup, not the developer asking to preserve work. The agent's best practice is to commit or stash before removal, not leave uncommitted changes.
   - The worktree is a temporary task artifact (like a `.claude/worktrees/` bare worktree was); it is not a persistent developer workspace.

4. **Accepts the trade-off gracefully.** If there is uncommitted work (the agent failed to commit before cleanup) or an active attach lock (rare; attach is meant to be short-lived), using `--force` is the right call: Claude removes its own artifacts aggressively, and the session state remains truthful. The developer can always inspect the orphaned branch if work was in progress (the branch is tagged-archived rather than deleted if the archive mechanism is implemented).

5. **Future-proof.** When the per-spawn cleanup policy (ADR-worktree-cleanup-policy) is implemented with TTL and audit archival, this approach composes cleanly: the `niwa worktree destroy` with force completes the immediate cleanup, and the audit trail (session state transition, branch tags) supports later forensics.

**Non-recommendation rationale:**

- **Option A** (non-force destroy) leaves orphaned sessions when guards reject, violating R6
- **Option C** (detach-only) requires a sweep mechanism and doesn't immediately clean up, also violating R6
- **Option D** (two-step) is more complex without additional benefit over B; the dirty-guard won't help if the agent didn't commit anyway

---

## Summary

niwa's `DestroySession` has two protective guards (attach-lock, dirty-worktree) designed for human-initiated teardown. The agent's worktree removal is fundamentally different: Claude tears down its own temporary artifacts. The hook should call `niwa worktree destroy <session-id> --force` to ensure deterministic cleanup and keep niwa as the system of record. The dirty and attach guards serve human developers; the agent's cleanup is best-effort and unconditional.
