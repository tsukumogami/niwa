# Lead: Session ID threading without worktrees

## Findings

### 1. How ClaudeSessionID is currently stored and threaded

**Storage:** `TaskState.Worker.ClaudeSessionID` (internal/mcp/types.go:246)
- Set by `registerSessionID()` in internal/mcp/server.go:933-947
- Reads from env var `CLAUDE_SESSION_ID` when task starts
- Only written if sessionID passes regex validation: `^[a-zA-Z0-9_-]{8,128}$` (internal/mcp/session_discovery.go:17)
- Persisted to state.json when daemon reads it via UpdateState

**Threading to daemon:**
1. Worker receives `CLAUDE_SESSION_ID` env var (set by spawnWorker in mesh_watch.go:939-943)
2. Worker calls registerSessionID() at Server startup (mesh_watch.go:106)
3. Daemon reads state.json via retrySpawn() pre-read (mesh_watch.go:1619-1626)
4. Daemon passes sessionID to spawnWorker as inboxEvent.resumeSessionID field (mesh_watch.go:1720)

### 2. What `claude --resume <id>` actually provides

**Context window continuation:** When spawnWorker receives a resumeSessionID:
- Constructs exec.Command with `--resume <id>` flag (mesh_watch.go:918) instead of `-p <bootstrap_prompt>`
- Passes to Claude Code's launcher via argv
- Claude Code loads the prior session JSONL file from `$HOME/.claude/projects/<base64_cwd>/<session_id>.jsonl`
- **This is conversation/context continuation** — the new worker process picks up the previous session's message history
- NOT just a branch point; the full prior context window is available to the resumed session

**Proof of context continuation:**
- retrySpawn validates session JSONL integrity before resuming (checkSessionFileIntegrity, mesh_watch.go:1728-1764)
- Checks file exists, is non-empty, has at least one valid JSON line in last 4 KB
- This guards against corrupted sessions that would fail to load in Claude Code

### 3. MaxResumes limits and rationale

**Limits (internal/cli/mesh_resume_test.go:42):**
- `defaultMaxResumes = 2`
- Each task gets MaxResumes capped at 2 by default
- Set in TaskState when niwa_delegate creates a task (internal/mcp/handlers_task.go:201)
- Can be overridden per-task if caller sets MaxResumes in state.json

**Guards enforced before resume (mesh_watch.go:1646-1656):**
1. **Guard 1 (restart cap):** RestartCount < MaxRestarts — if exceeded, abandon without retrying
2. **Guard 2 (resume cap):** ResumeCount < effectiveMaxResumes — if exceeded, do fresh spawn
3. **Guard 3 (session exists):** ClaudeSessionID != "" — if empty, do fresh spawn
4. **Guard 4 (file integrity):** checkSessionFileIntegrity passes — if JSONL missing/broken, do fresh spawn
5. **Guard 5 (format):** sessionIDRegex matches — if invalid format, do fresh spawn

**Why MaxResumes <= 2:**
- The comment doesn't explicitly state rationale, but implied constraints:
  - Prevent infinite resume loops on corrupted session files
  - Balance: one resume attempt gives *some* recovery; two attempts avoid thrashing
  - Beyond 2 resumes, a fresh spawn is more likely to succeed than trying to recover broken context
  - Stall watchdog (Issue 6) runs at 2s polling — multiple resumes compound staleness detection

---

## Session ID Threading Without Worktrees: Feasibility Analysis

### Proposed Approach
Add optional `resume_session_id` field to delegateArgs (niwa_delegate input schema):
```go
type delegateArgs struct {
    To               string          `json:"to"`
    Body             json.RawMessage `json:"body"`
    Mode             string          `json:"mode,omitempty"`
    ExpiresAt        string          `json:"expires_at,omitempty"`
    ResumeSessionID  string          `json:"resume_session_id,omitempty"` // NEW
}
```

Thread it through:
1. Coordinator calls `niwa_delegate(to="worker_role", body={...}, resume_session_id="<id>")`
2. handleDelegate writes ResumeSessionID to envelope or state.json
3. Daemon reads it, passes as inboxEvent.resumeSessionID to spawnWorker
4. Worker spawned with `--resume <id>` instead of `-p <bootstrap>`
5. Worker gets prior conversation context, continues where previous task left off

### Would This Solve Problem 1? (Task-scoped sessions discard context)

**Partial YES, with caveats:**

**What it solves:**
- Sequential delegations can now share Claude context
- If coordinator explicitly threads session ID, next worker picks up prior conversation
- Avoids re-establishing facts established in prior task's session

**What it does NOT solve:**
- Coordinator must explicitly capture and thread the session ID
  - No automatic mechanism to inherit parent task's session
  - Requires explicit coordination logic in coordinator's prompt
- Only works for **linear chains** (A→B→C)
- Parallel delegates would all try to resume the same session → session conflicts
  - File system is not multi-writer safe for JSONL
  - Two workers resuming same session simultaneously = corrupt/truncated context
- Session isolation: all delegates share one conversation thread
  - Mixing concerns in one context (design + plan + review all in same thread)
  - Harder to isolate errors or rewind one phase

### What This Approach Would NOT Solve

**Dirty workspace problem (repos stranded on feature branches):**
- Session ID threading is pure **context** — doesn't touch file system state
- Main clone still gets stranded when work switches repos
- niwa apply can't merge dirty branches
- **Worktrees are the solution** — they isolate working directories per session
- This lead solves coordination of context, not workspace isolation

**Parallel sessions for same repo:**
- Without worktrees, two sequential delegations to same role compete for the same main clone
- Session 1 checks out feature/design on main
- Session 2 tries to check out feature/plan on same main → conflict
- **Session ID threading doesn't help** — both sessions see the same working tree
- Requires explicit branch checkout logic in workers, or worktrees for isolation

**Physical isolation of session work:**
- No git worktree = no separate .git, no separate branches, no per-session snapshots
- All work converges on single main clone's HEAD
- Session context is restored but file system state is NOT

---

## Analysis: Why Session ID Threading Alone Is Insufficient

The lead question asks: "Can simpler session-ID-threading achieve enough without worktrees?"

**The answer is: No, it solves only half the problem.**

### What's Missing in "Just Thread Session ID"

1. **Workspace coherence:** Session context ≠ repo state
   - Worker A resumes session, thinks it's still in design phase
   - Repo is on feature/implement branch (from prior worker)
   - Context says "do planning" but working tree shows "implementation done"
   - **Mismatch → contradictory instructions to Claude**

2. **Task isolation:** Session is global per resumption chain
   - Can't easily rewind one task's work without rewinding the whole chain
   - Progress updates from task B overwrite context from task A
   - **No per-task working snapshots**

3. **Crash recovery:** Session state ≠ file system consistency
   - Daemon kills worker due to stall
   - Session context preserved via --resume
   - But repo is in inconsistent state (partially merged branch, uncommitted files)
   - Resume doesn't fix the file system — fresh spawn also wouldn't help

### The Hidden Requirement: Workspace Isolation

The original problems statement says:
- Problem 1: "Task-scoped sessions discard context between delegations (design → plan fails)"
- Problem 2: "Main clone gets stranded on feature branch when work switches repos"

These are **orthogonal concerns:**
- Problem 1 = context loss (solved by session ID threading)
- Problem 2 = workspace state loss (solved ONLY by worktrees or explicit repo management)

Addressing Problem 1 without solving Problem 2 means:
- Coordinator has context from design phase ✓
- But working tree is on wrong branch ✗
- Coordinator's instructions are inconsistent with reality

---

## Open Questions

1. **What if session ID threading is combined with explicit worker cleanup?**
   - Worker runs: `git checkout main && git reset --hard origin/main` at task start
   - Clears dirty workspace before inheriting context
   - Would this be sufficient, or does niwa apply's "merge clean branches only" constraint still block?
   - **Answer likely:** Insufficient. The problem is the repo branch structure itself, not cleanup; niwa apply fails when main is behind feature branches, regardless of clean/dirty state.

2. **Could MaxResumes >= 2 resume attempts actually solve the dirty workspace in practice?**
   - Current design: 2 resume attempts, then fresh spawn
   - If resume fails due to dirty workspace, fresh spawn does a full checkout
   - Empirically, is this sufficient, or do chains longer than 2 tasks fail?

3. **Should delegateArgs.resume_session_id be optional or mandatory?**
   - If optional: coordinators must opt-in, requires prompt engineering to thread correctly
   - If default (auto-inherit parent): simplifies usage but hides complexity, breaks parallel tasks silently
   - **Design choice, not a blocker**

4. **How would question_pending (task.ask) interact with resumed sessions?**
   - If task B resumes session, then asks task C a question while paused
   - Task C can't resume session (B owns it)
   - Must be fresh spawn, context is lost between question and answer
   - Is this an acceptable limitation?

---

## Implications

**For session continuity design:**

The naive answer to "can simpler session ID threading replace worktrees?" is superficially YES for the context-preservation problem, but the real answer is **NO for the full system design**.

**Session ID threading solves:** Context loss between sequential tasks
**Worktrees solve:** Workspace isolation, dirty-workspace recovery, parallel session support, per-task branch isolation

**The design choice:**
- Session ID threading is a **lightweight add-on** to the current system
  - Low implementation cost (one optional field in delegateArgs)
  - Helps sequential tasks that explicitly opt-in to context sharing
  - Doesn't break existing deployments
  
- But it **cannot replace worktrees** for the full niwa-mesh use case
  - Two problems require two solutions
  - Threading session ID handles context, not repos
  - Worktrees handle repos and enable parallel sessions

**Recommendation:** Session ID threading can be a quick win for sequential single-role delegations, but the design should still plan for worktrees as the long-term solution for arbitrary delegation graphs.

---

## Surprises

1. **Session files are simple JSONL, not binary blobs**
   - Expected: Claude Code stores sessions in some optimized format
   - Reality: Base64-encoded CWD in path, plain JSONL lines in file
   - Integrity check only verifies ≥1 valid JSON line in last 4 KB (not full validation)
   - **Implication:** Partial writes / corrupted sessions can silently fail resume (Guard 4 catches it)

2. **MaxResumes default is exactly 2, with no special logic for why**
   - No comment explaining the choice
   - Tests verify it works, but not the rationale
   - Empirically: 1 resume attempt not enough for crash recovery, 3+ seems excessive given stall watchdog timeout
   - **Implication:** Changing this default would need empirical validation

3. **Session ID validation happens twice**
   - Once at worker startup (registerSessionID, server.go:938)
   - Again at daemon's retrySpawn (Guard 5, mesh_watch.go:1654)
   - No mutual validation (worker doesn't validate what daemon writes)
   - **Implication:** If daemon's sessionIDRe differs from server's sessionIDRegex, they could diverge; should be DRY

4. **ResumeCount and RestartCount are separate counters**
   - RestartCount = fresh spawns (full restarts)
   - ResumeCount = session resumptions within one restart
   - Resume path preserves RestartCount, bumps ResumeCount
   - Fresh spawn resets ResumeCount, bumps RestartCount
   - **Implication:** Coordinator can distinguish "task crashed after retries" (RestartCount high) from "network hiccup, session recovered fine" (ResumeCount > 0 but RestartCount low)

---

## Summary

**Key finding:** Session ID threading can be added to `niwa_delegate` as an optional `resume_session_id` field, allowing coordinators to explicitly thread Claude context between sequential delegations. The daemon already has the infrastructure (ClaudeSessionID in TaskState.Worker, Guard 5 validation, resumeSessionID field in inboxEvent, spawnWorker's --resume flag), so implementation is low-cost.

**Main implication:** This solves context loss but NOT workspace isolation. The dirty-workspace problem and parallel-session problem require worktrees or equivalent per-session working directory isolation — session ID threading alone cannot replace this.

**Biggest open question:** Should session ID threading be opt-in (coordinator explicitly passes `resume_session_id`) or auto-inherit from parent task? Opt-in is safer but requires more coordination logic; auto-inherit is simpler but breaks parallel tasks silently unless the design explicitly prevents it.

