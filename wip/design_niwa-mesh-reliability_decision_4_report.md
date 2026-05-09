# Decision 4: Coordinator role visibility for session workers

## Question

Should the `coordinator` role be made known to session workers via a special-case in `isKnownRole`, or by provisioning a synthetic `<worktree>/.niwa/roles/coordinator/` directory at session-creation time?

This covers issues #92 (live-coordinator routing for `niwa_ask`) and #109 (`UNKNOWN_ROLE` returned for `niwa_ask`/`niwa_send_message` when worker targets `coordinator`).

## Options

### A. Special-case `isKnownRole` (and the send-message inbox path)

**Mechanics**

- Modify `isKnownRole(role)` at `internal/mcp/server.go:768-778` to consult `s.mainInstanceRoot` when `role == "coordinator" && s.mainInstanceRoot != ""`, mirroring the existing `askRoot` redirect at `server.go:817-819`.
- Apply the same redirect inside `sendMessageWithID` at `server.go:719`, where `inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", args.To, "inbox")` currently anchors to the worktree. Without this, a worker `niwa_send_message(to="coordinator")` would (after the `isKnownRole` fix) write into the *worktree's* `.niwa/roles/coordinator/inbox/` — a directory no daemon watches. Centralize this by introducing a small `s.roleRoot(role)` helper that returns `s.mainInstanceRoot` for `"coordinator"` (when set) and `s.instanceRoot` otherwise; `isKnownRole`, `sendMessageWithID`, and `handleAsk`'s `askRoot` selection all call it.
- No filesystem changes during session creation. `scaffoldWorktreeNiwa` is untouched.

**Pros**

- Surgical: one routing concept (`roleRoot`), three call sites switched to use it. Total diff is small and obvious.
- Keeps a single source of truth for the live-coordinator inbox: `<mainInstanceRoot>/.niwa/roles/coordinator/inbox/`. The daemon already watches it via `registerInboxWatches` (`internal/cli/mesh_watch.go:2236-2266`).
- No dead inbox directory in the worktree. `registerInboxWatches` enumerates `<niwaDir>/roles/`, and the worktree daemon's `watched_roles count=N` log line stays accurate (it counts only the worker's own repo role, which is correct — the worktree daemon has no business reacting to coordinator-bound mail).
- Generalizes cleanly: any future role that always lives in the main instance (e.g. a hypothetical workspace-wide auditor role) gets the same treatment by extending `roleRoot`'s decision rule, without touching scaffolding.
- The daemon's spawn guard (`daemonOwnsInboxFile` claims only `task.delegate` files — `mesh_watch.go:746-758`) is unaffected because nothing changes about what files are written to the worktree.

**Cons**

- The "consult mainInstanceRoot for coordinator" rule lives in code, not in the filesystem. Reviewers reading `scaffoldWorktreeNiwa` won't see anything that hints `coordinator` is a known role inside a worktree session.
- Slight asymmetry: every other role-existence check matches the on-disk layout 1:1; `coordinator` becomes the one role with a code-side redirect. Callers grepping for `.niwa/roles/coordinator` in the worktree won't find it.

**Risk**

Low. The change is contained to three sites in one file. Existing unit coverage for `handleAsk`'s live-coordinator branch (`session_registry_ask_test.go:TestHandleAsk_LiveCoordinator_WritesTaskAsk`) doesn't exercise the worktree-vs-main split today, so a new test that constructs a server with `instanceRoot = worktree` and `mainInstanceRoot = main` is required to lock the fix in place.

### B. Synthetic `<worktree>/.niwa/roles/coordinator/inbox/` in `scaffoldWorktreeNiwa`

**Mechanics**

- Extend `scaffoldWorktreeNiwa` (`internal/mcp/handlers_session.go:80-108`) to also create `.niwa/roles/coordinator/inbox/{,in-progress,cancelled,expired,read}/` alongside the existing repo-role inbox.
- `isKnownRole` then passes naturally for `"coordinator"` because the directory exists.
- `handleAsk` keeps the `askRoot` redirect (`server.go:817-819`) and continues to write `task.ask` notifications to the *main* coordinator inbox via `lookupLiveCoordinator(askRoot)`.
- **However**, `sendMessageWithID` would still write to `s.instanceRoot/.niwa/roles/coordinator/inbox/` — i.e. into the worktree's synthetic dir. So Option B is incomplete on its own for issue #109 unless the send-message inbox path is *also* redirected (which is exactly the work Option A does). In other words, B is a strict superset of A's effort, plus a directory.

**Pros**

- "Filesystem layout is the source of truth" stays true at face value: every role-existence check sees the same shape regardless of worktree vs. main.
- A reviewer reading `scaffoldWorktreeNiwa` immediately sees that `coordinator` is a recognized role here.

**Cons**

- The synthetic `inbox/` is dead real estate. The worktree's daemon enumerates `.niwa/roles/` in `registerInboxWatches` (`mesh_watch.go:2250-2263`) and would dutifully add a watch on it. Now `watched_roles count=2` becomes the new baseline for every session worktree, even though nothing should ever be delivered there. That's a code smell at minimum and an active foot-gun if any future code path *does* end up writing to `<worktree>/.niwa/roles/coordinator/inbox/` — those messages would be picked up by the worktree daemon and routed as if they were for a coordinator that doesn't live in the worktree.
- Doesn't actually fix `niwa_send_message` on its own. The send path computes `inboxDir = s.instanceRoot/...` (`server.go:719`), so messages would land in the synthetic worktree dir. To make B work end-to-end you still need Option A's redirect for the send-message inbox path — which means B is "A plus an extra mkdir", not an alternative to A.
- Forces every future contributor to mentally distinguish "real coordinator inbox" (in main) from "decoy coordinator inbox" (in worktree). New checks like "is this role registered" gain a confusing twin meaning.
- Subtle interaction with `registerInboxWatches`: the worktree daemon ends up watching a directory that never should receive mail. If any test fixture or stray code writes a file there during development, the watcher will fire spuriously and the daemon will try to claim it (passing `daemonOwnsInboxFile` if it's a `task.delegate`) and spawn an ephemeral coordinator worker *inside the worktree* — exactly the deadlock scenario PR #93 was meant to eliminate.

**Risk**

Medium. The directory is harmless on day one but creates a gradient where future bugs slide downhill into it. The daemon's role enumeration and the watcher loop both treat `roles/<name>/` as authoritative; introducing a "this one is fake" exception erodes that invariant.

## Chosen

**Option A.** Add a `roleRoot(role string) string` helper on `Server`, return `s.mainInstanceRoot` when `role == "coordinator" && s.mainInstanceRoot != ""` and `s.instanceRoot` otherwise, and route `isKnownRole`, `sendMessageWithID`'s inbox path, and `handleAsk`'s `askRoot` selection through it. No changes to `scaffoldWorktreeNiwa`.

## Rationale

The decisive evidence is in `sendMessageWithID` (`server.go:695-742`): it both calls `isKnownRole(args.To)` *and* computes `inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", args.To, "inbox")`. Whichever option we pick, the inbox path itself has to be redirected for `coordinator` — the worktree's roles dir cannot be the destination, because the daemon watching the worktree is the *worker's* daemon, not the coordinator's, and it's not registered in `sessions.json` as a coordinator session. That redirect is the same one-line `if role == "coordinator" && s.mainInstanceRoot != ""` decision that `handleAsk` already encodes for `askRoot`. Adding it once via a small helper and reusing it from `isKnownRole` lands the whole fix without ever creating a directory.

Option B does not eliminate this work — it just adds an mkdir on top of it. The synthetic directory then becomes a genuine liability: `registerInboxWatches` enumerates `roles/`, so the worktree daemon would watch a directory it has no business handling, and any stray write into it (test fixture, future bug, manual reproduction) would either be silently swallowed (ordinary message) or trigger ephemeral-coordinator spawn (`task.delegate`). The whole point of PR #93 was to remove that spawn path; reintroducing a way to land delegates in the worktree's coordinator inbox undoes that protection. Keeping the worktree's `.niwa/roles/` exactly mirroring the worker's actual responsibilities — i.e. one role, the worker's repo — preserves the invariant that "directory under `.niwa/roles/` means the daemon here is responsible for it."

The trade-off is that one role (`coordinator`) is now special in code rather than on disk. That is acceptable: `coordinator` *is* special. It's the one role that semantically lives in the main instance and is reached from session worktrees through the `mainInstanceRoot` redirect, which `handleAsk` already establishes as a precedent (`server.go:817-819`). Aligning `isKnownRole` and the send-message inbox path with that same precedent is the simpler, more honest fix.

## Auto-registration trigger expansion

`maybeRegisterCoordinator` currently fires from two handlers:

- `handleCheckMessages` — `internal/mcp/server.go:500`
- `handleAwaitTask` — `internal/mcp/handlers_task.go:398`

A coordinator that uses only `niwa_delegate` + `niwa_query_task` (a fan-out-then-poll pattern) never registers, so worker `niwa_ask`/`niwa_send_message` falls through to `no_live_session` even after Option A lands. Add `maybeRegisterCoordinator()` calls to:

1. **`handleDelegate`** — first thing in the handler. A coordinator that has issued any delegation has by definition declared its role and become a candidate for return-channel mail.
2. **`handleQueryTask`** — first thing in the handler. The fan-out-then-poll pattern reads as `delegate(...); for { query_task(...); }`. Without registering on `niwa_query_task`, a coordinator polling for delegate completion is invisible to workers asking it questions.

Two additional handlers worth considering for symmetry but are lower priority:

3. **`handleSendMessage`** — a coordinator initiating peer messaging to workers should be registered, but most coordinators that send messages will also poll for replies via `niwa_check_messages` and register that way. Add it for completeness; it's a one-line call and hits the same idempotent path.
4. **`handleListOutboundTasks`** — same reasoning as `handleQueryTask`; some coordinators poll outbound tasks instead of querying individually.

Recommended minimum: handlers 1 and 2 (`handleDelegate`, `handleQueryTask`). Recommended complete set: 1–4. The function is idempotent and cheap (it short-circuits when `s.role != "coordinator"` and writes only when no live entry exists for the role), so the cost of being generous with trigger sites is essentially zero. The risk of *not* being generous is exactly the bug we're already seeing for delegate-only coordinators.

This change is additive and lands alongside Option A in the same PR.

## Confidence

**High.** The choice rests on three concrete code observations (the `askRoot` precedent at `server.go:817-819`, the inbox-write at `server.go:719`, the role enumeration at `mesh_watch.go:2250-2263`) and a clear ranking of failure modes between A and B. Option B doesn't reduce A's required work and adds a directory whose only role in the system is to be misleading.

## Assumptions

1. `s.mainInstanceRoot` is reliably populated for session worker MCP servers via `NIWA_MAIN_INSTANCE_ROOT` (set in `handleCreateSession`'s extraEnv at `handlers_session.go:212-215` and read into `Server` at `server.go:97-98`). The research confirms this; no further verification needed.
2. `lookupLiveCoordinator(<mainInstanceRoot>)` already returns the correct main-instance inbox path (`<mainInstanceRoot>/.niwa/roles/coordinator/inbox`) and prunes stale PIDs. The research confirms this at `session_registry.go:53-92`.
3. `niwa_finish_task` answers from coordinator to a session-worker originator already route correctly via `taskStoreRoot()` / `resolveInboxDir` (per research open-question #4). If that turns out to need its own fix, it's separable from this decision and tracked as follow-up — it doesn't change which coordinator-visibility option we pick here.
4. No existing test or production code path writes to `<worktree>/.niwa/roles/coordinator/inbox/`. (If something does, Option B's "harmless synthetic dir" framing would be wrong, which would only strengthen the case for A. Either way, A is safe.)
5. Adding `maybeRegisterCoordinator()` to `handleDelegate`/`handleQueryTask` does not create lock-ordering or reentrancy issues with the rest of those handlers. The function takes its own lock around `WriteSessionEntry` and returns quickly; the existing call sites are at handler entry, which is the natural placement.
