# Lead: Where does the worker's Claude Code transcript live, and can `claude --resume` find it given only a session_id?

## Findings

### 1. Where the transcript lives on disk

Worker transcripts are JSONL files written by Claude Code at:

```
~/.claude/projects/<base64url(no-padding)(cwd)>/<claude_session_id>.jsonl
```

The encoding scheme is documented and used in three call sites in niwa:

- `internal/mcp/session_discovery.go:107-109` (`discoverViaProjectScan` — Tier 3 fallback ID discovery)
- `internal/cli/mesh_watch.go:1832-1834` (`checkSessionFileIntegrity` — pre-resume guard)
- `internal/cli/mesh_resume_test.go:126,374,399,415` (test fixtures fabricating JSONLs)

The encoding is `base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(cwd))`. The comment at `session_discovery.go:105-106` explicitly notes this matches Claude Code CLI v1.x.

The `cwd` baked into the path is the **worker's CWD at process start**, set in `mesh_watch.go:1012` via `cmd.Dir = resolveRoleCWD(s.instanceRoot, evt.role)`. For session workers, `resolveRoleCWD` returns `<worktree>/<repo-name>` (`mesh_watch.go:2310-2342`), because the session daemon's `instanceRoot` IS the worktree root. So a session worker's transcript lives at:

```
~/.claude/projects/<base64url(<worktree>/<repo>)>/<claude_session_id>.jsonl
```

This is a path component of the (cwd, session_id) tuple, **not just session_id**.

### 2. Is `claude_conversation_id` alone sufficient for `claude --resume`?

**No, it is sufficient only when invoked from the same CWD.** Evidence:

- `mesh_watch.go:1010-1012`: when the daemon spawns the resume worker, it sets `cmd.Dir = resolveRoleCWD(...)` — the same CWD the original worker used. The resume invocation is implicit-CWD-locked: the daemon arranges it because the daemon-spawned worker's CWD always matches the original's.
- `mesh_watch.go:1832-1834`: `checkSessionFileIntegrity` constructs the JSONL path from `(homeDir, roleCWD, sessionID)`. If any of the three changes, the integrity check fails and the daemon falls back to a fresh spawn (`mesh_watch.go:1700-1707`, Guard 4).
- `DESIGN-coordinator-loop.md:411`: "integrity check: `~/.claude/projects/<cwd>/<session_id>.jsonl` exists and has valid JSON" — confirms the path is a function of cwd, not session_id alone.
- `DESIGN-mesh-session-lifecycle.md:43-45`: "Claude conversation history lives in a JSONL file keyed to (CWD, session-id)."

**Implication for `niwa session attach`:** the attach command MUST `cd` into the worker's working directory (`<worktree>/<repo>`) before exec'ing `claude --resume <conv_id>`, otherwise Claude Code creates a brand-new conversation. The current `niwa go <repo> <session-id>` shell wrapper navigates to `<worktree>` (the parent of `<repo>`) — `attach` needs the deeper path.

### 3. What if the JSONL is missing, pruned, or moved?

The current code-path is **silently degrading** for the daemon retry case:

- `mesh_watch.go:1755-1758`: Guard 4 (file integrity) catches missing/empty/corrupt JSONL and falls back to a fresh spawn. The user sees a degraded retry; the conversation thread is lost without explicit error.
- `mesh_watch.go:874-877` (the cross-task `--resume` path): "No file-integrity guard here — the ID was already validated when it was captured from the first worker's exit; if `--resume` fails because the JSONL is missing, Claude Code falls back to a fresh session." So the second-task spawn does NOT pre-check; it trusts Claude Code's own error handling.
- `PRD-mesh-session-lifecycle.md:305-307` (R12): "If the session's Claude conversation history is missing or corrupted, niwa falls back to spawning a fresh worker (matching current behavior) and records a warning in session state." But: there is no code that writes such a warning today. Grepping `corrupted_session` finds it only in the PRD acceptance criterion (line 452); no `state.go`/`session_lifecycle.go` write site exists.
- `mesh_watch.go:866` (silent log only): the only artifact of a missing JSONL is a daemon log line `inbox_event ... session_resume=...` (logged before validation), and the eventual fresh spawn that Claude Code performs internally.

**For attach this matters because:** if the user wiped `~/.claude/`, ran disk cleanup, or upgraded Claude Code across a transcript-format break, `niwa session attach` will silently launch a fresh Claude conversation labeled with the old `claude_conversation_id`. The user expects "load the worker's full history" and gets "blank session, no error." A pre-flight integrity check identical to `checkSessionFileIntegrity` belongs in the attach path.

### 4. Multi-worker per session: is the binding 1:1 or 1:N? Sequential or concurrent?

**Today: 1:N sequential, all sharing one Claude conversation.** Evidence:

- `docs/guides/sessions.md:19-22`: "All tasks subsequently delegated to that session run inside that worktree. After the first task completes, niwa captures the worker's `CLAUDE_SESSION_ID`... Every following task spawns the worker with `claude --resume <id>`." Multiple tasks per session is the design.
- `PRD-mesh-session-lifecycle.md:53`: "Coordinators can delegate multiple sequential tasks to the same repo agent within [a session]."
- `test/functional/features/mesh.feature:692-709` ("Second delegation to same session uses --resume"): explicitly tests two sequential delegations to the same session. The test's first delegation completes (`task state ... eventually becomes "completed"`) BEFORE the second delegation is queued.
- `mesh_watch.go:1606-1633` (`captureConversationID`): the conversation ID is captured on **first task exit** and the function immediately early-returns on subsequent calls (`if st.ClaudeConversationID != "" { return // already set; one-time write }` — line 1626-1628). So one ID per session, for the entire session lifetime, regardless of how many workers run.

**Concurrent same-session workers — is it prevented anywhere?** I found no explicit "one worker at a time per session" lock. The session daemon processes inbox events serially via the central event-loop goroutine (`mesh_watch.go:289-290`), but there is no inbox-level mutex that holds while a worker runs — a second envelope arriving in the same inbox would be claimed and `spawnWorker` called again. However:

- `PRD-mesh-session-lifecycle.md:670-672` explicitly warns about this: "[bare resume_session_id without worktrees] silently breaks under parallel sessions (two workers would share one JSONL)." The PRD frames this as a **two-different-sessions** problem and does not directly address concurrent workers within a single session.
- The implication is that today's design assumes the coordinator delegates sequentially within a session — there's no architectural enforcement, just convention. If a coordinator delegated two tasks to the same session in quick succession, both workers would attempt to `--resume` the same JSONL concurrently, with undefined Claude Code behavior.

**Implication for attach:** the assumption that a session has at most one active worker is unenforced but pervasive. `niwa session attach` can lean on the same assumption (refuse to attach while a worker is running, or wait for it; per issue #117's locked-in default), and the attach lock can be the FIRST formal mutual-exclusion primitive at session granularity.

### 5. Is the `claude_conversation_id` capture mechanism reliable?

**Mechanism:** Three-step chain:

1. Claude Code sets `$CLAUDE_SESSION_ID` in the worker's env (`DESIGN-coordinator-loop.md:181-195`, "Option A — Implicit Side-Effect Registration at MCP Server Startup").
2. The MCP server inside the worker reads `$CLAUDE_SESSION_ID` at startup and writes it to `state.json.Worker.ClaudeSessionID` via `registerSessionID()` (`internal/mcp/server.go:1037-1061`). Best-effort: silent on missing env var, silent on UpdateState error.
3. The session daemon's supervisor-exit handler reads `state.json` after the worker process exits and, if `Worker.ClaudeSessionID` is non-empty, calls `captureConversationID` (`mesh_watch.go:1389-1391`), which writes it to the session lifecycle state file (`mesh_watch.go:1614-1633`). One-time write — `if st.ClaudeConversationID != "" { return }`.

**When does it write?** Post-exit, on first task only. `mesh_watch.go:1376-1391` shows `handleSupervisorExit` is the only caller; it runs after `cmd.Wait()` returns.

**What if the first task fails?** `captureConversationID` runs regardless of exit code (`mesh_watch.go:1389` checks only `Worker.ClaudeSessionID != ""`, not state). So even an abandoned first task captures the conversation ID, **provided** the MCP server got far enough to call `registerSessionID()` before the worker died. If Claude Code crashes before the MCP server starts, no ID is captured and the next delegation runs as a fresh session.

**Reliability gaps for attach:**

- If the very first delegation runs and crashes before the MCP server starts (unlikely but possible — bad workspace config, missing tool binary, etc.), `claude_conversation_id` stays empty. `niwa session attach` would have nothing to `--resume`. The session is effectively unattachable in this state, and the failure mode is silent: the user sees an active session with no conv ID, and there's no surfaced reason.
- If `$CLAUDE_SESSION_ID` is not set by Claude Code (version skew with the spawned binary), `registerSessionID` no-ops (`server.go:1052`) and the field stays empty. Same outcome.
- The capture is bound to `NIWA_MAIN_INSTANCE_ROOT` and `NIWA_SESSION_ID` env vars (`mesh_watch.go:1614-1619`); if either is absent the function returns early. These are set at session-daemon spawn (`handlers_session.go:211-215`); a daemon restarted by an unrelated path (e.g. a user manually re-invoking the watch command) could miss them and silently fail to capture.

**One subtle issue:** `captureConversationID` runs in the per-worktree session daemon. If that daemon crashes between worker exit and the capture write, the ID is lost. There's no journaling of the intent-to-capture; it's a single best-effort write.

## Implications

For the PRD's "transcript persistence and locatability" open question:

1. **The session_id alone is NOT sufficient.** `claude --resume` requires both the conversation ID and the original CWD. The PRD must specify that `niwa session attach` exec's claude from `<worktree>/<repo>` (the same path resolved by `resolveRoleCWD`), and that the attach UX surfaces this path to the user (so they understand where they are when the shell drops them).

2. **A pre-flight integrity check belongs in attach.** The same `checkSessionFileIntegrity` logic from `mesh_watch.go:1832-1868` should run before exec'ing `claude --resume`. If the JSONL is missing/corrupt/pruned, attach should refuse with a clear error rather than silently launching a fresh session that the user mistakes for resumed history. This is a small dependency, not a worker-spawn change — the function is already in the codebase and the regex/encoding logic is already proven.

3. **The PRD does NOT need to scope a worker-spawn change as a dependency.** Transcript persistence already works for the daemon-spawned `--resume` path; attach can ride the same primitive. What the PRD does need:
   - An attach precondition that `claude_conversation_id` is non-empty (refuse early with a clear error if absent).
   - A precondition that the JSONL passes the integrity check.
   - A documented assumption that the user does not wipe `~/.claude/projects/`.

4. **Multi-worker-per-session is functionally 1:N sequential, but unenforced.** The PRD can build attach on top of this assumption without worry, because issue #117 already commits to "wait for running worker" as the default (which makes the lock the de facto enforcer of one-worker-at-a-time at the session level — a useful invariant beyond just attach).

5. **The capture mechanism has silent failure modes that attach inherits.** If the PRD wants attach to be a recovery primitive ("step in when something went wrong"), it must explicitly handle the case where `claude_conversation_id` is empty — which is exactly when something went wrong on the first delegation. Possible behaviors: refuse attach with an explanation, or fall back to launching plain `claude` in the worktree (read-only forensics mode mentioned in the issue).

## Surprises

- **The conversation ID is captured by the daemon, not by niwa MCP tooling.** I expected a tool call (`niwa_register_session_id` or similar) to be the source of truth. Instead it's a side-effect of `registerSessionID()` at MCP server startup, fed by an env var that Claude Code itself sets. This is robust against agent misbehavior (the agent can't refuse to register) but brittle against Claude Code version skew (if the env var name changes, capture silently breaks).

- **No warning is written when JSONL is missing.** PRD R12 specifies a `corrupted_session` warning entry, but no code writes it. `grep -r corrupted_session` returns only the PRD acceptance criterion. The current implementation falls back to fresh spawn silently. This means today's mesh users have no signal that conversation continuity is broken — and attach inherits this opacity.

- **The cross-task resume path skips the integrity check** that the stall-recovery resume path performs (`mesh_watch.go:874-877` vs `mesh_watch.go:1722-1730`). The comment justifies it ("the ID was already validated when captured"), but missed the case where the JSONL is later deleted by the user. Attach should NOT replicate this asymmetry — always integrity-check before `--resume`.

- **The attach feature gets concurrency safety almost for free** because mesh delegations within a session are already de-facto serial (no architectural enforcement, just convention). The lock attach introduces is the first explicit one — and it doubles as the first formal mutex on per-session worker access.

## Open Questions

1. **What's Claude Code's actual behavior when `claude --resume <id>` is invoked from the wrong CWD?** Does it error out, silently start a fresh session, or look up the JSONL by ID alone in some fallback path? Code suggests fallback-to-fresh, but I didn't find authoritative documentation. This affects whether attach NEEDS to cd, or whether it merely SHOULD (for performance/UX).

2. **What's Claude Code's policy on JSONL retention?** Does it ever auto-prune? Rotate? Compress? Move across upgrades? The niwa docs treat the JSONL as immortal; if Claude Code GC's it after N days, long-lived sessions would silently lose attach capability.

3. **Should attach support the "no captured conversation ID" case as a feature?** A user might want to attach to a session whose first delegation failed before MCP server startup — landing them in the worktree with a fresh `claude` (no `--resume`). Issue #117 says "loaded with the worker's full transcript history" — but is partial degradation (attach without resume, with an explanation) better than refusing?

4. **Is there a race where a second delegation arrives between worker exit and `captureConversationID` write?** The new worker would be spawned without `--resume` (because the field is still empty when the inbox handler reads it at `mesh_watch.go:878-888`), and now there are two captures — but `captureConversationID` is one-time so the second write no-ops, leaving the lifecycle file pointing at worker A's conversation while worker B's transcript is keyed under worker B's session ID and orphaned. This is a real race, not just theoretical, but probably rare in practice (it requires sub-second back-to-back delegations). Attach attaching to "the captured ID" could miss the more recent transcript. Worth flagging in the PRD as an edge case.

5. **Does the attach PRD need to cover the `--strict-mcp-config` and `--allowed-tools` knobs that workers spawn with?** When a human attaches, they'd presumably want the full Claude Code experience (no MCP isolation, full tool palette) — i.e., they're launching `claude` directly, not `claude` in worker mode. The PRD has to commit to which flags carry over (the `--mcp-config` is interesting: do attached users see niwa MCP tools?). This is adjacent to transcript persistence but tangled — worth a sub-question for the PRD.

## Summary

Worker transcripts live at `~/.claude/projects/<base64url(cwd)>/<conv_id>.jsonl`, so `claude --resume <conv_id>` is sufficient ONLY when invoked from the worker's original CWD (`<worktree>/<repo>`); session_id alone won't locate the file. The capture mechanism is reliable for the happy path (first task exits cleanly with `$CLAUDE_SESSION_ID` set), but has silent failure modes — missing JSONL, no captured ID from a crashed first task, daemon crash between worker exit and capture write — that attach inherits and must surface explicitly rather than silently degrading. The biggest open question is what Claude Code actually does when `--resume` is invoked from a wrong CWD or against a missing JSONL, since the entire attach UX depends on whether failure is loud (errors) or silent (fresh session that looks resumed) — the niwa codebase assumes silent fresh-fallback but I found no authoritative confirmation.
