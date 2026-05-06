# Lead: CLI vs coordinator session conflict

## Findings

### Are CLI-created and MCP-created sessions structurally identical?

Yes, they are structurally identical. Both creation paths call the same underlying function.

`niwa session create` (CLI path, `internal/cli/session_lifecycle_cmd.go`) calls `srv.CreateSessionDirect(repo, purpose, "")`, which delegates directly to `handleCreateSession` in `internal/mcp/handlers_session.go`. The MCP path (`niwa_create_session`) calls the same `handleCreateSession` via `callTool`. There is no separate code path; it is one function invoked two ways.

Both paths:
- Call `newSessionLifecycleID(sessionsDir)` — random 8-hex-char ID with collision check
- Create a git worktree at `<instanceRoot>/.niwa/worktrees/<repo>-<sessionID>/`
- Create a git branch `session/<sessionID>`
- Run `scaffoldWorktreeNiwa(worktreePath, repo)` — identical directory layout
- Call `NewSessionLifecycleState(...)` — same `SessionLifecycleState` schema, `v=1`
- Write the state file to `<instanceRoot>/.niwa/sessions/<sessionID>.json`
- Start a per-worktree daemon via the injected `daemonStarter`

The resulting state files are byte-for-byte schema-equivalent. There is no `created_by` or `creation_source` field in `SessionLifecycleState` that distinguishes CLI from MCP origin.

### What distinguishes a human-created session from a coordinator-created session in the state file?

Only `parent_session_id`. When the CLI calls `CreateSessionDirect(repo, purpose, "")`, the third argument is hardcoded to `""`, so `parent_session_id` is omitted from the JSON (the field is `omitempty`). The session is recorded as a root session. When a coordinator calls `niwa_create_session` from within its own session, it can pass a `parent_session_id`; if called from the top-level coordinator (outside any session), it also passes `""` and gets a root session.

The `CreatorPID` and `CreatorStartTime` fields in `SessionLifecycleState` record who created the session but do not distinguish CLI from MCP invocation — both run as processes on the same host.

There is no flag, enum, or field that marks a session as "human-created" vs "coordinator-created". The two are semantically identical in the state file.

### What happens if a coordinator creates a session for repo X and a human also runs `niwa session create X`?

Two sessions are created successfully. Each gets a unique session ID (random 8-hex with collision check against existing files in `sessionsDir`). Each gets its own:
- Distinct worktree path: `<instanceRoot>/.niwa/worktrees/X-<sessionID1>/` and `X-<sessionID2>/`
- Distinct git branch: `session/<sessionID1>` and `session/<sessionID2>`
- Distinct per-worktree daemon
- Distinct state file: `<instanceRoot>/.niwa/sessions/<sessionID1>.json` and `<sessionID2>.json`

There is no global locking or exclusion on session creation per repo. Creating two sessions for the same repo is explicitly allowed — the PRD acceptance criteria include: "Creating two sessions for the same repo succeeds; both appear in `niwa_list_sessions` with distinct IDs."

No naming collision occurs because the worktree directory and branch name both include the unique session ID. No daemon conflict occurs because each daemon is scoped to its own worktree directory.

The only shared resource is the sessions state directory (`<instanceRoot>/.niwa/sessions/`), and writes there are atomic rename operations on distinct filenames.

### Can a coordinator delegate into a human-created session via `niwa_delegate(session_id=<human-session-id>)`?

Yes, without restriction. `resolveCreationInboxDir` in `handlers_task.go` (lines 257–285) reads the session's `SessionLifecycleState` from disk, checks only:
1. The session ID exists (`SESSION_NOT_FOUND`)
2. `status == "active"` (`SESSION_INACTIVE`)
3. The worktree path is within the instance root (`INVALID_WORKTREE_PATH`)
4. The target role directory exists in the worktree (`UNKNOWN_ROLE`)

There is no ownership check. A coordinator can route work into any active session regardless of who created it. Conversely, the session tree model's parent-child routing restriction (for `niwa_ask`) is based on `parent_session_id` relationships recorded at creation time — a session with `parent_session_id=null` (CLI-created root session) has no parent to route asks to, and no coordinator owns it as a child.

### Does the session tree model break if a human creates a root session while a coordinator also creates root sessions?

Structurally, no. The tree model is purely an in-state-file graph; it does not enforce any runtime invariants during creation. Two root sessions (both with `parent_session_id=null`) are siblings at the workspace level — they appear as parallel roots in `niwa session tree`.

However, there are semantic gaps:
- A CLI-created root session cannot receive `niwa_ask(to="parent")` from its workers because `parent_session_id` is null. Workers in this session cannot surface questions upward.
- No coordinator "owns" the CLI-created session as a child, so no coordinator's `niwa_list_sessions` naturally surfaces it as "mine". A coordinator that calls `niwa_delegate(session_id=<human-session-id>)` has bypassed the tree model entirely — it delegates into a session it did not create and has no parent-child link to.
- `niwa_destroy_session` on a CLI-created root session works identically to destroying any other root session — it checks for active children (there are none, unless the CLI user's session was given children by a coordinator), then removes the worktree. There is no "owner must destroy" enforcement.

### What is `parent_session_id=null` for CLI-created sessions — does it put them in the same tree root as coordinator-created root sessions?

Yes. Both appear as roots in the session tree. There is no namespace separation; the session tree is a single flat list plus parent-child links. CLI-created root sessions and coordinator-created root sessions occupy the same conceptual level and are visually peers in `niwa session tree` output.

This is intentional per the PRD: R16 explicitly says "Without `--parent`, `parent_session_id` is recorded as null; the session is a root session."

### Routing authorization from a coordinator's perspective

`niwa_ask` routing authorization (in `handleAsk`) uses `isKnownRole` to check whether the target role directory exists, then `lookupLiveCoordinator` to find a live coordinator entry in `sessions.json`. There is no parent-child check in `handleAsk` in the current code — the tree-routing authorization described in the PRD (R19: reject routing to non-adjacent sessions) appears to be an open item. The PRD lists it as an open question: "The existing `handleAsk` gate validates `args.To` against role directories on disk before any routing logic runs. Virtual targets... have no role directory, so they return `UNKNOWN_ROLE` before routing code is reached."

In practice this means that, today, `niwa_ask` routes by role name (not session ID), and all role-based routing is to the coordinator. There is no session-tree-aware routing enforcement in the current code. The parent-child constraint is a PRD design goal, not an implemented guard.

## Implications

**CLI and MCP sessions are fully fungible.** A coordinator that finds a CLI-created active session via `niwa_list_sessions` can delegate into it with `niwa_delegate(session_id=...)`, and there is nothing at the protocol level preventing this. This is either a feature (flexibility) or a gap (ownership ambiguity).

**The "should all `niwa_delegate` calls be session-bound by default" question.** The current code makes session binding opt-in via `session_id`. Changing this to default-bound would require: (a) a session to be auto-created when none is supplied, or (b) a "current session context" propagated through the coordinator. Neither is implemented. The CLI session model shows the design intent: sessions are explicit, not ambient.

**For humans working in a repo's main clone:** The main clone is always on `main` and is never a session. There is no session concept for "the main clone itself" — sessions are strictly worktree-based. A human working directly in the main clone (not via `niwa session create`) is outside the session model entirely and has no session ID to reference.

**`niwa_delegate` into a human-created session works today.** The only condition checked is that the session is active and the role exists in the worktree. A coordinator can treat a CLI-created session as just another session to route work into.

## Surprises

The CLI `session create` command passes `parentSessionID=""` hardcoded — there is no `--parent` flag implementation visible in `session_lifecycle_cmd.go`, despite R16 specifying `[--parent <session-id>]`. The PRD requirement exists but the flag is not wired in the current code (the third `CreateSessionDirect` argument is always `""`).

The session tree routing constraints (R19: reject non-adjacent routing) are not enforced in the current `handleAsk` implementation. The code routes by role name only, not by session tree position. This means sibling session routing via `niwa_ask` is not blocked by code today — only by convention.

There is no `created_by` or `origin` field in `SessionLifecycleState`. If the future design requires different behavior for CLI-created vs coordinator-created sessions (e.g., coordinator auto-adopts, cleanup policies differ), the state schema would need a new field.

## Open Questions

**Ownership without a parent link:** If a coordinator delegates into a CLI-created root session (which has no coordinator as parent), who is responsible for destroying it? The tree model assumes the creator destroys, but there is no enforcement. Should the coordinator be required to adopt (re-parent) a root session before delegating into it?

**Default session binding for `niwa_delegate`:** If the answer to the core question is "yes, all delegates should be session-bound by default," where does the ambient session ID come from? Options: (a) the coordinator's own session ID propagated as a context variable, (b) auto-create-on-first-use, (c) require explicit session creation before any delegation. None is currently implemented.

**Human in main clone:** If a developer is working directly in `repos/X/` (the main clone), and a coordinator also has an active session for repo X, there is no coordination mechanism between them. Git-level conflicts are possible but not tracked by niwa. Is the main clone intended to be "frozen" once any session for that repo is active?

**`--parent` flag for CLI session create:** R16 specifies this flag but it is not implemented. Is this a known gap or intentionally deferred?

**Session-tree routing enforcement:** R19 says non-adjacent routing is rejected, but the code does not implement this check. Any implementation of session-tree routing for `niwa_ask` would need the "is known role" gate to recognize session-tree virtual targets (`"parent"`, child session ID), as noted in the PRD open questions section.

## Summary

CLI-created sessions (`niwa session create`) and coordinator-created sessions (`niwa_create_session`) are structurally identical — both call the same `handleCreateSession` function, produce the same on-disk schema, and create the same per-worktree layout; the only difference is that CLI-created sessions always have `parent_session_id=null`. A coordinator can freely delegate into a CLI-created session (and vice versa) because `niwa_delegate(session_id=...)` checks only liveness and role presence, not ownership or parent-child relationship. The biggest open question is ownership: without a parent-child link between the coordinator and a CLI-created session, there is no defined owner responsible for cleanup, and the tree-routing constraints that would normally enforce session boundaries are not yet implemented in the code.
