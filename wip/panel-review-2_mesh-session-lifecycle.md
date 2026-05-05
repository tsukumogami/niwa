# Panel Review 2: PRD Critique — Mesh Session Lifecycle

Five reviewers examined the revised PRD (post-tree-model, post-create/destroy rename).
Findings are classified as **Breaking**, **Contradiction**, **Under-specified**,
**Missing**, or **AC mismatch**.

Duplicates across reviewers are merged. Numbering gaps (R7/R8/R28/R30) are
intentional removals and not findings.

---

## Breaking Changes

### BK1 — `niwa session create` shell navigation silently no-ops on existing installs

**File:** `internal/cli/shell_init.go:41`

The shell wrapper intercepts `create|go` by matching `$1`. When the user types
`niwa session create`, `$1 == "session"` — it falls through to the `*)` branch and
calls `command niwa "$@"` without reading `NIWA_RESPONSE_FILE`. The CWD change
specified by R16 is silently lost.

The wrapper must be extended to intercept `session create` (matching on `$1 $2` or
adding `session` to the intercept list). Because the wrapper is emitted by
`niwa shell-init bash/zsh` and installed in `~/.niwa/env` at setup time, every
existing install is stale until users re-run `niwa shell-init install`. This is a
user-visible breaking change for anyone who installed before the feature ships.

**R16 must acknowledge this dependency and the re-install requirement.**

---

### BK2 — `niwa_ask(to="parent")` fails `isKnownRole` before routing logic runs

**File:** `internal/mcp/server.go:604`

`handleAsk` validates `args.To` against `.niwa/roles/<role>/` directory existence
before any routing logic runs. The string `"parent"` has no role directory on disk,
so the call returns `UNKNOWN_ROLE` unconditionally. The entire `"parent"` routing
target from R19 is dead on arrival.

This is a variant of the B3 break identified in the first panel review — the
role-directory authorization model is incompatible with virtual routing targets.
The PRD specifies `ROUTING_DENIED` as the error code for invalid targets (R19), but
the gate that fires before routing even starts produces `UNKNOWN_ROLE` instead.
The design doc must specify how `isKnownRole` is bypassed or extended for the three
tree-routing targets.

---

### BK3 — `niwa session list` command name collision with existing implementation

**File:** `internal/cli/session.go` (existing `niwa session list`)

The existing `niwa session list` is a coordinator-role registry view: columns are
ROLE, PID, STATUS, LAST-SEEN, PENDING. R18 redefines `niwa session list` with a
completely different schema (session ID, repo, purpose, status, creation time, stale
marker) for the new lifecycle session model. Implementing R18 as specified breaks the
existing command. The PRD does not acknowledge this collision.

**Design decision required:** rename the existing command (e.g., `niwa mesh status`)
or rename the new one (e.g., `niwa session ls`).

---

## Contradictions

### C1 — R5 always marks force-destroyed target as `abandoned`; R17 and CLI are silent on cascade

**R5 (line ~274):** "the target transitions to `abandoned`" regardless of whether
the target's own commits are pushed. Descendants are evaluated individually
(pushed → `ended`, unpushed → `abandoned`), but the target is hardcoded to
`abandoned`. A force-destroy of a clean parent session cannot result in `ended` —
the target is penalised for using `--force` even when there was nothing dirty to
force past.

**R17 (CLI path):** says `--force` "removes the worktree and exits zero" with no
mention of cascading through children or recording terminal state. If `niwa session
destroy --force` does not cascade, it can silently orphan active child sessions. If
it does cascade, R17 should say so.

**Fix:** in R5, evaluate the target's commit state the same way as descendants
(pushed → `ended`). In R17, state explicitly whether CLI `--force` cascades.

---

### C2 — R4 is silent on priority when both blocking conditions apply; AC overrides

**R4:** defines `blocked_by_unpushed_work` and `blocked_by_active_children` as
separate response shapes but does not say which takes precedence when both apply.
The AC (line ~469) fills the gap ("children take precedence") but the requirement
itself is silent. Move the priority rule into R4.

---

### C3 — R5 vs. R4 on `abandoned` target state makes clean force-destroy
indistinguishable from dirty force-destroy

A coordinator calling `niwa_destroy_session(force=true)` gets `{status: "abandoned"}`
whether the target was clean or not. The coordinator cannot tell after the fact which
case occurred without calling `niwa_list_sessions` and checking the descendant states.
Related to C1 — fix there resolves this too.

---

## Under-specified Requirements

### U1 — `niwa_list_sessions` missing `worktree_path` per entry (R3)

After a coordinator context reset, it calls `niwa_list_sessions` to re-orient and
then routes delegations into existing sessions. Without `worktree_path` in the
list response, the coordinator must infer the path from R23's naming convention
rather than reading it directly. Add `worktree_path` as a required field.

### U2 — `root_session_id` filter unusable after context reset (R3)

The filter requires the coordinator to already know a root session ID. After a
context reset, it doesn't — the filter is useless in the exact scenario it was
designed to support. The coordinator must retrieve the full list and reconstruct the
tree from `parent_session_id` links. The filter is still useful in the non-reset
case; document the limitation or provide a first-class "list root sessions only"
flag instead.

### U3 — `niwa_delegate` routing to per-worktree daemon unspecified (R2)

R2 says `niwa_delegate` with `session_id` spawns the worker in the session's
worktree. But `niwa_delegate` today enqueues a task into the **main instance
daemon's** inbox. It is not specified how the call is routed to the **per-worktree
daemon's** inbox instead. This is the core routing mechanism for the entire feature
and it is absent from the requirements.

### U4 — Partial worktree left on disk when coordinator crashes mid-create (R1)

If the coordinator crashes after `git worktree add` succeeds but before the tool
returns `session_id`, the coordinator never learns the ID. A new coordinator sees
the session in `niwa_list_sessions` as stale (per R9) but cannot tell whether the
session is fully initialised or a creation artifact. No mechanism distinguishes the
two states; `niwa_delegate` targeting a half-initialised session would fail silently.
R1 needs an initialisation status field or a distinct `initializing` transient state.

### U5 — R6 has no transition graph; `abandoned` has no recovery path

R6 defines three states but not which transitions are legal. In practice:
`active → ended` (clean destroy), `active → abandoned` (force destroy or cascade).
`ended` and `abandoned` are terminal — there is no `→ active` recovery. This is
probably correct, but it is not stated. A coordinator that wants to "re-open" an
abandoned session must call `niwa_create_session` fresh, losing the prior Claude
conversation ID. Whether this is intentional needs to be explicit.

### U6 — R9 does not specify which PID is checked

"Verified by PID check" — the coordinator's PID or the per-worktree daemon's PID?
The sessions registry likely records the coordinator PID for the `sessions.json`
coordinator entry, but each session also has its own daemon. If only the coordinator
PID is checked, a session whose own daemon died (but coordinator is alive) appears
healthy in `niwa_list_sessions`. Add `daemon_alive` as a separate indicator, or
clarify which PID drives the stale marker.

### U7 — Session daemon death not surfaced (R9)

R9 covers orphaning when the coordinator dies. A session's per-worktree daemon dying
independently (coordinator still alive) is not addressed. The session appears `active`
and healthy in `niwa_list_sessions`; R2's lazy-restart would recover it on next
`niwa_delegate`, but there is no visibility into the dead-daemon state. A coordinator
relying on `niwa_list_sessions` to detect problems would miss this.

### U8 — R19 routing to `ended`/`abandoned` has no defined error code

R19 says routing to a session in `ended` or `abandoned` state "returns an immediate
error" but does not name the error code. Define it (e.g., `SESSION_INACTIVE`,
consistent with R2's error code for the same condition).

### U9 — `niwa go <repo> <session-id>` flag interaction unspecified (R26)

`niwa go` has four existing paths (`-w`, `-r`, `-w -r`, positional). R26 adds a
two-positional path. `niwa go -w myworkspace myrepo a3f7c2d1` and
`niwa go -r myrepo a3f7c2d1` are not addressed; the existing conflict guards only
cover `len(args) == 1` with flags. Specify that `<session-id>` is invalid when any
flag is also provided, or define the combined behavior.

### U10 — R16 gives no fallback when shell function is not installed

R16 says `niwa session create` navigates using "the same mechanism as `niwa go`."
It does not say what happens if the user skipped `niwa setup`. Define the fallback:
print the worktree path and exit zero without navigation, consistent with how `niwa
go` behaves in non-function environments.

### U11 — Force-cascade response shape missing per-descendant summary (R4/R5)

`niwa_destroy_session(force=true)` returns `{status: "abandoned"}`. The coordinator
cannot see which descendants ended cleanly vs. had unpushed work without calling
`niwa_list_sessions` after the fact. R5 should define a response that includes a
per-descendant outcome list.

### U12 — `niwa_create_session` response missing branch name (R1)

R1 returns `{session_id, worktree_path}`. The coordinator frequently needs to know
which git branch the session is tracking (e.g., to reference it in a PR, to pass it
to the next delegation). Add `branch` to the response.

---

## Missing Requirements

### M1 — No `niwa session show <session-id>` command

`niwa session list` and `niwa session tree` are aggregate views. There is no
targeted single-session detail command showing full state: worktree path, branch,
parent/child IDs, Claude conversation ID, stale PID, daemon status. The existing
`niwa task show <id>` establishes this pattern. Missing for the same reasons it
exists for tasks.

### M2 — No `--instance` scope flag on `niwa session list` / `niwa session tree`

`niwa apply`, `niwa status`, and `niwa destroy` all accept an instance name to scope
from the workspace root. R18 and R29 have no equivalent. A user with multiple
instances cannot scope session commands without `cd`-ing first.

### M3 — `niwa session destroy` has no cwd-contextual fallback (R17)

`niwa destroy` (instance) accepts zero arguments and discovers the target instance
from cwd. `niwa session destroy` requires an explicit session ID. A user `cd`'d into
a session worktree cannot type `niwa session destroy` to destroy the current session.
The contextual-discovery pattern established by the parent command is broken without
explanation.

### M4 — No `--status` or `--repo` completion on `niwa session list` (R27)

R27 specifies completion for `niwa session destroy` and `niwa go <repo>` but not for
the `--status` and `--repo` flags on `niwa session list`. `--status` takes an enum
(`active`, `ended`, `abandoned`) and `--repo` takes a repo name — both are natural
completion targets. Without `RegisterFlagCompletionFunc`, both fall back to
file-path completion.

### M5 — `niwa_list_sessions` `root_session_id` filter has no AC

The filter parameter added to R3 (returns subtree rooted at a given session) has no
corresponding acceptance criterion.

### M6 — No policy for per-session state files after destroy (R5)

R5 defines worktree removal and state transition but not whether
`<instance>/.niwa/sessions/<session-id>.json` is deleted or retained. Retaining it
enables post-mortem inspection; deleting it keeps the directory clean. Neither is
stated.

### M7 — `niwa_create_session` response has no `branch` field (R1)

Covered under U12 — listed here as a missing response field for completeness.

---

## AC Mismatches

### AC1 — R12 AC tests a field name not defined by R12

R12 says niwa "records a warning in session state." The AC checks for a
`corrupted_session` field. The field name, type, and location are not defined in
R12; the AC specifies implementation detail that the requirement leaves open.

### AC2 — R19 cross-instance routing AC tests only the coordinator path

The "Cross-instance routing" AC (line ~599) only tests `niwa_ask` reaching the
coordinator. The three distinct targets (`"parent"`, `<session-id>`, `"coordinator"`),
the `ROUTING_DENIED` path, and routing to `ended`/`abandoned` sessions all have no
ACs in that section. (The session tree routing section covers some of these but is
scoped to a different requirement context.)

### AC3 — R4 AC says "immediate child IDs" where R4 says "all active descendant IDs"

The AC (line ~466) says `blocked_by_active_children` "lists all active descendant
session IDs" — this matches the updated R4 text. Check the ACs around force-cascade
to ensure they also test grandchildren, not just direct children.

### AC4 — R17/R31 split coverage

`niwa session destroy` behavior is spread across R17 (single-node destroy) and R31
(non-leaf cascade). The ACs for the CLI path cover the unpushed-work case (R17) and
the force-cascade printing (R31) but the two are not linked by a single end-to-end
AC that tests: non-leaf session, has active child, `--force`, child terminated first,
parent terminated second, both final states correct.

---

## Summary

| # | Class | One-liner |
|---|-------|-----------|
| BK1 | Breaking | Shell wrapper doesn't intercept `session create`; nav silently no-ops |
| BK2 | Breaking | `niwa_ask(to="parent")` fails `isKnownRole` before routing runs |
| BK3 | Breaking | `niwa session list` name collides with existing coordinator list command |
| C1 | Contradiction | R5 force-destroy target always `abandoned` even if clean; R17 silent on cascade |
| C2 | Contradiction | R4 priority rule lives in AC, not in the requirement |
| C3 | Contradiction | Force-destroy response indistinguishable clean vs. dirty (corollary of C1) |
| U1 | Under-specified | `niwa_list_sessions` missing `worktree_path` |
| U2 | Under-specified | `root_session_id` filter unusable after context reset |
| U3 | Under-specified | `niwa_delegate` → per-worktree daemon routing mechanism absent |
| U4 | Under-specified | Crash mid-create leaves undetectable half-initialised session |
| U5 | Under-specified | R6 no transition graph; `abandoned` recovery path unstated |
| U6 | Under-specified | R9 ambiguous which PID is checked |
| U7 | Under-specified | Per-session daemon death not surfaced in list |
| U8 | Under-specified | Routing to `ended`/`abandoned` has no error code |
| U9 | Under-specified | `niwa go` flag interactions with two positional args |
| U10 | Under-specified | R16 no fallback when shell function not installed |
| U11 | Under-specified | Force-cascade response missing per-descendant outcome |
| U12 | Under-specified | Create response missing `branch` field |
| M1 | Missing | No `niwa session show <id>` command |
| M2 | Missing | No `--instance` scope on session list/tree |
| M3 | Missing | `niwa session destroy` has no cwd fallback |
| M4 | Missing | No completion for `--status` and `--repo` on session list |
| M5 | Missing | `root_session_id` filter has no AC |
| M6 | Missing | No policy for `<session-id>.json` after destroy |
| AC1 | AC mismatch | R12 AC tests `corrupted_session` field not defined in R12 |
| AC2 | AC mismatch | R19 routing AC only tests coordinator path |
| AC3 | AC mismatch | Force-cascade AC coverage may miss grandchildren |
| AC4 | AC mismatch | R17/R31 split: no end-to-end cascade AC for CLI path |

### Bottom line

Three breaking changes require design decisions before the design doc:

1. **BK1** — Shell wrapper must be extended; re-install impact on existing users.
2. **BK2** — `isKnownRole` is incompatible with virtual routing targets (`"parent"`,
   `<session-id>`). The design doc must define how tree-routing bypasses the
   role-directory gate.
3. **BK3** — `niwa session list` name collision with existing command requires a
   rename decision.

The most significant specification gap is **U3**: `niwa_delegate` routing to the
per-worktree daemon is the core mechanism of the feature and is entirely absent from
the requirements. Everything else is additive or fixable with targeted requirement
edits.
