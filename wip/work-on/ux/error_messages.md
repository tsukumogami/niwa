# Error Message Audit — niwa CLI / MCP / apply / daemon

Scope: stderr/RunE returns from `internal/cli/`, structured `errResult`/
`errResultCode` from `internal/mcp/`, `apply` pipeline failures from
`internal/workspace/`, and daemon log + spawn failure paths from
`internal/cli/mesh_watch.go` + `internal/workspace/daemon.go`.

Quality bar applied per message: identifies the failing operation;
explains the cause when known; suggests a recovery action; consistent
tone (lowercase, present tense, verb-noun preamble); preserves the
underlying error chain via `%w`.

---

## 1. Inventory

Fifteen-plus representative examples. File paths are absolute; line
numbers match the snapshot used during the audit.

### 1.1 CLI (`internal/cli/`)

**A. `niwa apply` — workspace not found**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/apply.go:96`
- Verbatim: `"could not locate workspace configuration"`
- Verdict: identifies the operation, gives no path, no recovery hint
  (`run niwa init` or `cd into a workspace`).

**B. `niwa apply` — config source URL drift**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/apply.go:327-335`
- Verbatim:
  ```
  workspace config source changed
    was:  %s
    now:  %s
    The current %s on disk is from the old source. Replacing it will
    discard any uncommitted edits inside.
  To proceed:
    1. cd %s && git status   # check for uncommitted work (legacy working tree)
    2. niwa apply --force     # discard and re-materialize from the new source
  ```
- Verdict: best-in-class. Names the failure, shows old vs new state,
  gives a numbered recovery checklist with concrete commands. This is
  the precedent the rest of the codebase should match.

**C. `niwa apply` — multi-instance failure aggregation**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/apply.go:277-284`
- Verbatim: `"apply failed for %s: %w"` and `"apply failed for %d instances: %w"`
- Verdict: preserves chain via `%w` and `errors.Join`. Identifies which
  instance(s) failed. No recovery hint, but the wrapped error from the
  pipeline carries the cause.

**D. `niwa init` — overlay/no-overlay mutex**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/init.go:133`
- Verbatim: `"--overlay and --no-overlay are mutually exclusive"`
- Verdict: clear. Implicit recovery (drop one of the flags). Acceptable.

**E. `niwa init` — registry collision (rebind)**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/init.go:171,180,198`
- Verbatim format: `"%s\n  %s"` over `conflict.Detail` and
  `conflict.Suggestion`
- Verdict: structured. Detail + suggestion two-liner is a deliberate
  pattern. This is a second positive precedent alongside (B).

**F. `niwa create` — workspace not found**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/create.go:93,95`
- Verbatim:
  - `"workspace %q not found in registry (no workspaces registered)"`
  - `"workspace %q not found in registry. Registered workspaces: %s"`
- Verdict: lists registered names so the user can self-correct typos.
  The first variant could add `Run niwa init <name> to create one.`

**G. `niwa create` — not inside a workspace**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/create.go:107`
- Verbatim: `"not inside a workspace. Pass a workspace name or run from within a workspace directory"`
- Verdict: explicit recovery. Good.

**H. `niwa destroy` — outside workspace**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/destroy.go:78`
- Verbatim: `"not inside a niwa workspace or instance"`
- Verdict: identifies the failure. No recovery hint (e.g. `cd into a
  workspace, or run niwa destroy <name>`). Mismatch with (G) which
  does suggest recovery for the same class of error.

**I. `niwa destroy` — non-TTY missing instance arg**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/destroy.go:247`
- Verbatim: `"no instance specified and not running in a terminal; pass an instance name or use --force to wipe the workspace"`
- Verdict: textbook. Cause + two recovery options.

**J. `niwa destroy` — internal cwd-class fallthrough**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/destroy.go:91`
- Verbatim: `"internal error: unhandled cwd class %s"`
- Verdict: invariant violation. Acceptable as a "should never happen"
  guard, but no escape hatch (file an issue, run with `--debug`).

**K. `niwa mesh watch` — fsnotify watcher creation**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/mesh_watch.go:201-203`
- Verbatim: `"creating fsnotify watcher: %w"`
- Verdict: preserves chain. But on Linux the typical cause is inotify
  exhaustion (`fs.inotify.max_user_instances`), and the message gives
  the user no pointer toward `sysctl`. This is the highest-value
  improvement candidate in this audit.

### 1.2 MCP (`internal/mcp/`)

**L. Inventory of error codes** (extracted from `errResultCode` call
sites across `auth.go`, `server.go`, `handlers_session.go`,
`handlers_task.go`):

| Code                      | Sites                                             |
|---------------------------|---------------------------------------------------|
| `NOT_TASK_PARTY`          | auth.go ×11                                       |
| `NOT_TASK_OWNER`          | auth.go ×1                                        |
| `TASK_ALREADY_TERMINAL`   | auth.go ×3, handlers_task.go:1023                 |
| `BAD_PAYLOAD`             | handlers_task.go ×9, handlers_session.go ×2       |
| `BAD_TYPE`                | server.go:703                                     |
| `UNKNOWN_ROLE`            | server.go ×3, handlers_task.go ×2, session ×2     |
| `INBOX_UNWRITABLE`        | server.go ×3, handlers_task.go:243                |
| `SESSION_REQUIRED`        | handlers_task.go:126                              |
| `SESSION_NOT_FOUND`       | handlers_task.go ×2, handlers_session.go:245      |
| `SESSION_INACTIVE`        | handlers_task.go:275                              |
| `INVALID_WORKTREE_PATH`   | handlers_task.go:286                              |

**M. `niwa_send_message` — bad role format**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/mcp/server.go:700`
- Verbatim: `"role %q has invalid format"`
- Verdict: code is `UNKNOWN_ROLE`, but the message describes a *format*
  validation failure (`fieldPattern.MatchString` + `..` reject). The
  same code is reused on line 715 (`"role %q is not registered under
  .niwa/roles/"`) for a genuine "no such directory" condition. One
  code, two distinct conditions. A caller that branches on
  `UNKNOWN_ROLE` cannot tell them apart without parsing the detail.

**N. `niwa_delegate` — missing session**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/mcp/handlers_task.go:126-128`
- Verbatim: `"niwa_delegate requires a session_id; provision one with niwa_create_session, or set read_only:true for tasks that make no git changes"`
- Verdict: best-in-class for MCP. Names the calling tool, the missing
  argument, the recovery tool, and the alternative escape hatch.

**O. `niwa_delegate` — bad mode**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/mcp/handlers_task.go:122-123`
- Verbatim: `"mode must be \"async\" or \"sync\"; got %q"`
- Verdict: shows expected enum + observed value. Good.

**P. `handleCreateSession` — daemon starter not configured**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/mcp/handlers_session.go:154`
- Verbatim: `"niwa_create_session: daemon starter not configured (internal error)"`
- Verdict: invariant violation. No structured code (would belong as
  `INTERNAL_ERROR` or similar). Inconsistent with sibling validation
  errors that *do* carry codes. Same on line 239 for `daemon stopper`.

**Q. `handleCreateSession` — daemon spawn warning**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/mcp/handlers_session.go:220-225`
- Verbatim: `"daemon failed to start: " + err.Error()` packed into
  `daemon_warning` field; also written to stderr as
  `"niwa_create_session: daemon failed to start at %s: %v\n"`
- Verdict: deliberate non-fatal handling but the warning has no error
  code, so a downstream MCP client cannot reliably detect it without
  string-matching. This is exactly the gap `DAEMON_SPAWN_TIMEOUT` is
  supposed to close.

**R. `niwa_send_message` — generic envelope errors (no code)**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/mcp/server.go:697,708,711`
- Verbatim: `"to and type are required"`, `"body is required"`,
  `"body exceeds 64 KB limit"`
- Verdict: bare `errResult` (no code). The audit log records these as
  `ErrorCode="ERROR"` per `audit_test.go:71-82`. Inconsistent with the
  same payload-shape errors in `handlers_task.go` that *do* use
  `BAD_PAYLOAD`.

**S. `handleCreateSession` — generic worktree creation failure**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/mcp/handlers_session.go:190-191`
- Verbatim: `"git worktree add: %v\n%s"` (err + raw stderr from git)
- Verdict: surfaces the underlying git stderr, which is helpful, but
  has no error code. A common cause (branch already exists) has no
  recovery hint.

### 1.3 Apply pipeline (`internal/workspace/apply.go`)

**T. Overlay sync hard failure**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/workspace/apply.go:695,724`
- Verbatim: `"workspace overlay sync failed. Use --no-overlay to skip."`
- Verdict: **drops the underlying `syncErr`/`cloneErr`**. The recovery
  hint is good, but the user has to look at the daemon log or rerun
  with verbose tracing to learn what actually broke (network, auth,
  branch missing). This violates the `%w` invariant kept elsewhere in
  the same file.

**U. Overlay missing `workspace-overlay.toml`**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/workspace/apply.go:758`
- Verbatim: `"workspace overlay is missing workspace-overlay.toml"`
- Verdict: identifies the missing file. No path, no pointer to
  `docs/guides/workspace-config-sources.md` or to the convention.

**V. Org repo discovery**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/workspace/apply.go:1624,1629-1632,1670-1674`
- Verbatim:
  - `"discovering repos for org %q: %w"` — preserves chain
  - `"duplicate repo name %q found in orgs %q and %q; rename or use explicit repos lists to resolve"`
  - `"org %q has %d repos, which exceeds the max_repos threshold of %d; set max_repos to a higher value in [[sources]] or provide an explicit repos list"`
- Verdict: best-in-class for the apply pipeline. Names the resource,
  the threshold, two named recovery actions. Match the precedent set
  by apply.go:327.

**W. Clone aggregation**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/workspace/apply.go:1119-1124`
- Behaviour: only the *first* clone error is reported; subsequent
  errors are swallowed when `cloneErr != nil`. Wrapping is preserved
  via `fmt.Errorf("cloning repo %s: %w", r.name, r.err)`.
- Verdict: acceptable. Aggregating like `combineInstanceErrors`
  (apply.go:282) would be more informative when several repos fail at
  once — currently a multi-repo auth failure surfaces only the first
  victim.

**X. Materializer hook generic error**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/workspace/apply.go:1322`
- Verbatim: `"materializer %s for repo %s: %w"`
- Verdict: identifies materializer name and repo, preserves chain.
  Good. Could mention which `wip/` artifact was being processed.

### 1.4 Daemon (`internal/cli/mesh_watch.go`, `internal/workspace/daemon.go`)

**Y. Silent daemon spawn timeout**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/workspace/daemon.go:91-101`
- Verbatim:
  ```
  // Timed out — daemon may have failed to start (e.g. missing fsnotify
  // support). Return nil so Create/Apply still succeed; the missing PID
  // file is the observable failure signal.
  return nil
  ```
- Verdict: **silently returns success on timeout**. The user finishes
  `niwa apply` with exit 0, then discovers via `niwa mesh ls` (or task
  hangs) that no daemon is running. This is the precise behaviour the
  mesh-reliability design is replacing with `DAEMON_SPAWN_TIMEOUT`.

**Z. Daemon-internal `another daemon is running`**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/mesh_watch.go:248-251`
- Verbatim: `"another daemon is running; exiting"`
- Verdict: structured log line; `return nil`. Correct behaviour
  (loser exits cleanly), but the user has no way to distinguish "I
  meant to start a second daemon and lost the race" from "the existing
  daemon is healthy" without inspecting `daemon.pid`.

**AA. PID lock acquisition failure (not EWOULDBLOCK)**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/mesh_watch.go:253-256`
- Verbatim: `"warning: acquire daemon.pid.lock failed: %v"` (logger)
  then `"acquiring daemon.pid.lock: %w"` (returned)
- Verdict: log + return both shaped well, chain preserved. Good.

**BB. Catch-up scan failure**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/mesh_watch.go:275-278`
- Verbatim: `"warning: catch-up scan failed: %v"`
- Verdict: warning-only, daemon proceeds. The user only sees this if
  they `tail .niwa/daemon.log` — no surfacing through the CLI.

**CC. Per-role inbox watch registration**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/mesh_watch.go:2255-2261`
- Verbatim:
  - `"warning: role %s missing inbox dir: %v"`
  - `"warning: could not watch inbox for role %s: %v"`
- Verdict: per-role failures reduce to log warnings; the daemon
  silently continues with a smaller watch set. A user whose worker
  never runs has no signal pointing at the daemon log.

**DD. Watchdog SIGTERM/SIGKILL events**
- File: `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/internal/cli/mesh_watch.go:1284-1313`
- Verbatim: `"watchdog task=%s log_sigterm_err=%v"`, `"watchdog
  task=%s sigterm_err=%v"`, `"watchdog task=%s sigterm=sent pgid=%d"`,
  ...
- Verdict: structured key=value log entries are excellent for
  scripting (jq, awk). Internal observability is good. The opposite
  problem of (BB)/(CC): rich detail nobody surfaces to the user.

---

## 2. Quality verdict per category

| Surface         | Verdict          | Anchor examples                                  |
|-----------------|------------------|--------------------------------------------------|
| CLI             | acceptable+      | (B), (E), (I), (G) good; (H), (K) need polish    |
| MCP             | acceptable       | (N), (O) good; (M), (R) inconsistent; (P) opaque |
| apply pipeline  | acceptable       | (V), (X) good; (T) drops chain; (U) terse        |
| daemon          | needs polish     | (DD) internal-rich; (Y) silent failure; (BB)/(CC) invisible |

Surface ranking by quality, best to weakest: CLI ≈ apply pipeline >
MCP > daemon. The CLI is closest to "production-grade polish"; the
daemon is the laggard, and that gap is exactly what the mesh
reliability work is correcting.

---

## 3. Pattern findings

**Anti-pattern 1: dropped error chain.** Two clear cases —
`apply.go:695` and `apply.go:724` — return a static recovery hint
without wrapping the underlying `cloneOrSync` error. Compare to
`apply.go:1322` (materializer) which carries `%w` correctly. The
information lost is exactly what an operator needs to act ("network
unreachable" vs "branch missing" vs "auth").

**Anti-pattern 2: opaque "operation failed" without recovery hint.**
`destroy.go:78` (`"not inside a niwa workspace or instance"`) names
the failure but offers no next step. `create.go:107` for the same
class of error *does* offer one. The codebase has the pattern; it's
not applied uniformly.

**Anti-pattern 3: structured-code reuse blurs distinct failures.**
`UNKNOWN_ROLE` is emitted both for "role string fails regex
validation" (`server.go:700`) and "role directory does not exist"
(`server.go:715`). Programmatic callers cannot distinguish them.

**Anti-pattern 4: validation errors with and without codes.**
`handlers_task.go` validation uses `errResultCode("BAD_PAYLOAD",
...)`; `server.go:697,708,711` validation in `sendMessageWithID` uses
plain `errResult(...)` for the same class. Audit-log consumers see
`ErrorCode=ERROR` for the latter (`audit_test.go:67-82`).

**Anti-pattern 5: silent success on partial failure.** `daemon.go:91-101`
returns `nil` when the PID file never appears. The "observable
failure signal" comment acknowledges this, but the observation
mechanism is "user notices later". This is the design's #1 reliability
fix.

**Anti-pattern 6: daemon-only observability.** Three log paths
(`mesh_watch.go:255`, `mesh_watch.go:277`, `mesh_watch.go:2256-2260`)
write `warning: ...` to `daemon.log` only. Users with no idea where
that log lives need a second tool (`niwa mesh logs` does not exist
yet) or have to read the source to find `<instance-root>/.niwa/daemon.log`.

**Anti-pattern 7: inotify exhaustion without a hint.**
`mesh_watch.go:201-203` wraps the `fsnotify.NewWatcher()` error but
gives the user no pointer to `sysctl fs.inotify.max_user_instances`.
This is a real failure mode on dense developer machines and CI hosts.

**Positive pattern (multiline, named, prescriptive).** `apply.go:327`
and `init.go` (via `InitConflictError.Detail` + `.Suggestion`) show
that the codebase has a working template for high-quality errors:
detail line, blank, recovery line(s) with concrete commands. The new
mesh-reliability codes should mirror this shape.

---

## 4. Mesh-redesign integration

The design's three new error codes should adopt the structured-code
discipline of `BAD_PAYLOAD`/`SESSION_REQUIRED` and the prescriptive
recovery shape of `apply.go:327`. The verbatim drafts below.

### 4.1 `DAEMON_SPAWN_TIMEOUT`

Surface: synchronous response from `niwa_create_session` (and any
future session-spawning tool) when `EnsureDaemonRunning` exhausts its
500 ms PID-file wait.

```
error_code: DAEMON_SPAWN_TIMEOUT
detail: mesh daemon did not write daemon.pid within 500ms at <worktreePath>/.niwa/daemon.pid; the worktree, branch, and session state file have been rolled back. Common causes: inotify exhaustion (sysctl fs.inotify.max_user_instances), missing claude binary on PATH, or unwritable .niwa/. Check <worktreePath>/.niwa/daemon.log for the spawn error.
```

Notes:
- Names the failed operation (`mesh daemon did not write daemon.pid`).
- States the timeout (500 ms) so the caller can compare against
  observed spawn time.
- Names the rolled-back artifacts so the caller knows the system is
  in a clean state.
- Lists the three real-world causes the design's functional tests
  exercise (Issue 2 test cases a, b, c).
- Points the operator at `daemon.log` — the only place the underlying
  failure is recorded.

### 4.2 `MISSING_SKILLS`

Surface: synchronous response from `niwa_delegate` and
`niwa_redelegate` when `body.required_skills` includes entries absent
from the target session's plugin manifest. No task ID is allocated.

```
error_code: MISSING_SKILLS
detail: target role <role> is missing required skills: <missing-list>. Available skills: <available-list>. Check spelling, install the plugin in the target's CLAUDE configuration, or remove the entries from required_skills.
```

The structured payload (per design line 1031-1043) is the
authoritative shape:
```json
{
  "error_code": "MISSING_SKILLS",
  "missing": ["shirabe:prd"],
  "available": ["superpowers:tdd", "init", "review"]
}
```

The detail string mirrors the payload so a human reading raw audit
logs gets the same information without parsing JSON.

### 4.3 `SOURCE_BODY_LOST`

Surface: synchronous response from `niwa_redelegate` when the source
task's `envelope.json` is missing (the rare `taskstore_lost`
recreate-stub case from Issue 5).

```
error_code: SOURCE_BODY_LOST
detail: cannot recover task body for source task <source-task-id> because envelope.json is missing (state was reconstructed from transitions.log). Re-supply the body via body_overrides and retry, or abandon the redelegation.
```

Notes:
- Names what was lost and *why* (envelope absent, state reconstructed).
- Cites both legitimate next actions: retry with `body_overrides`, or
  give up.
- The `body_overrides` mention matches the design's
  caller-supplies-recovery contract on line 1023-1026.

### 4.4 Backward-propagation candidates

The new codes raise the bar above the current MCP median. Three
existing errors should be updated to match:

1. `handlers_session.go:154,239` (`daemon starter not configured`,
   `daemon stopper not configured`) — adopt
   `errResultCode("INTERNAL_NOT_CONFIGURED", ...)` so callers can
   distinguish a wiring bug from a transient failure. Currently bare
   `errResult`.
2. `handlers_session.go:190-191` (`git worktree add`) — wrap with
   `errResultCode("WORKTREE_CREATE_FAILED", ...)` and surface the
   most common cause (branch already exists from an aborted prior
   session) with a recovery hint pointing at
   `git worktree list && git branch -D session/<id>`.
3. `apply.go:695,724` — restore `%w` wrapping so the overlay sync
   error chain reaches `niwa apply`'s exit message instead of the
   static "use --no-overlay" hint.

---

## 5. Concrete proposed issues

Numbered. Each lists title, goal, and 3-5 acceptance criteria.
Where the work folds into an existing PLAN issue, that is noted.

### Issue 5.1 — Wire DAEMON_SPAWN_TIMEOUT message text

Folds into PLAN Issue 2 (`plan_niwa-mesh-reliability_issue_2_body.md`).

- **Title**: `feat(mcp): add DAEMON_SPAWN_TIMEOUT error code with prescriptive recovery hint`
- **Goal**: replace the silent `return nil` at
  `daemon.go:91-101` with a typed timeout error so
  `handleCreateSession` can roll back and respond with
  `DAEMON_SPAWN_TIMEOUT`.
- **Acceptance**:
  - `EnsureDaemonRunning` returns `ErrDaemonSpawnTimeout` (a sentinel
    `errors.Is`-comparable value) when the 500 ms poll expires.
  - `handleCreateSession` matches the sentinel, runs
    `cleanupWorktree`, removes the session state file, deletes the
    branch, and returns `errResultCode("DAEMON_SPAWN_TIMEOUT", ...)`.
  - The detail string lists the three real causes (inotify, missing
    claude binary, unwritable `.niwa/`) and points at `daemon.log`.
  - Functional tests cover all three causes (per PLAN Issue 2 ACs).

### Issue 5.2 — Wire MISSING_SKILLS message text

Folds into PLAN Issue 6 (`plan_niwa-mesh-reliability_issue_6_body.md`).

- **Title**: `feat(mcp): emit MISSING_SKILLS with prescriptive detail string`
- **Goal**: ensure the `MISSING_SKILLS` precondition gate's detail
  string matches the structured payload and gives the caller a
  concrete next step.
- **Acceptance**:
  - Detail string follows the format in §4.2.
  - Structured payload `{missing, available}` is preserved per
    design lines 1033-1043.
  - Audit log row records `ErrorCode=MISSING_SKILLS`.
  - Functional test verifies the typo case (`shirabe:rpd` →
    `MISSING_SKILLS` with `available` containing `shirabe:plan`).

### Issue 5.3 — Wire SOURCE_BODY_LOST message text

Folds into PLAN Issue 7 (`plan_niwa-mesh-reliability_issue_7_body.md`).

- **Title**: `feat(mcp): emit SOURCE_BODY_LOST with body_overrides recovery hint`
- **Goal**: when `niwa_redelegate` cannot read the source envelope,
  return a typed error directing the caller to `body_overrides`.
- **Acceptance**:
  - Detail string follows §4.3.
  - Triggered when `envelope.json` is absent on the source task; not
    when the source task is in any legal terminal state with the
    envelope intact.
  - Structured payload includes the source task ID.
  - Functional test for the `taskstore_lost` recovery loop
    (redelegate without `body_overrides` → `SOURCE_BODY_LOST` →
    redelegate with `body_overrides` → success) per PLAN Issue 7.

### Issue 5.4 — Preserve overlay sync error chain in apply

Standalone.

- **Title**: `fix(apply): preserve underlying error in workspace overlay sync failure`
- **Goal**: stop dropping `syncErr`/`cloneErr` at `apply.go:695,724`
  so apply failures surface the actual cause.
- **Acceptance**:
  - `apply.go:695` returns `fmt.Errorf("workspace overlay sync
    failed (use --no-overlay to skip): %w", syncErr)`.
  - `apply.go:724` returns the same shape with `cloneErr`.
  - Existing test fixtures continue to pass after the wording
    change; add a unit test that asserts `errors.Is(err,
    underlyingNetworkErr)` round-trips.
  - The recovery hint stays in the message — only the chain is
    restored.

### Issue 5.5 — Distinguish UNKNOWN_ROLE format vs not-found

Standalone.

- **Title**: `fix(mcp): split UNKNOWN_ROLE into INVALID_ROLE_FORMAT and UNKNOWN_ROLE`
- **Goal**: programmatic callers should be able to distinguish "you
  passed garbage" from "the role does not exist".
- **Acceptance**:
  - `server.go:700,803` (regex / `..` rejection) emit
    `INVALID_ROLE_FORMAT`.
  - `server.go:715`, `handlers_session.go:160`, `handlers_task.go:131,290`
    (directory absent) keep `UNKNOWN_ROLE`.
  - Audit log adds `INVALID_ROLE_FORMAT` to its known-codes table
    per the pattern in `audit_test.go:67`.
  - Skill documentation / `niwa-mesh` skill page lists both codes
    with their distinct meanings.

### Issue 5.6 — Add recovery hint for inotify exhaustion

Standalone.

- **Title**: `fix(mesh): point to inotify limit in fsnotify watcher creation failure`
- **Goal**: when `fsnotify.NewWatcher()` fails on Linux with
  `EMFILE`/`ENOSPC`, the daemon's error should name the sysctl knob.
- **Acceptance**:
  - `mesh_watch.go:201-203` inspects the wrapped errno; on
    `errors.Is(err, syscall.EMFILE)` or `ENOSPC`, the returned error
    is `"creating fsnotify watcher (Linux: check sysctl
    fs.inotify.max_user_instances and max_user_watches): %w"`.
  - Unit test injects the errno via a fake watcher constructor.
  - The bare `%w` path is preserved for non-Linux / non-errno cases.

### Issue 5.7 — Use BAD_PAYLOAD codes uniformly across MCP validation

Standalone.

- **Title**: `fix(mcp): emit BAD_PAYLOAD code for required-field validation in send_message and ask`
- **Goal**: align `server.go:697,708,711,797,800` (currently bare
  `errResult`) with the `BAD_PAYLOAD` pattern used in
  `handlers_task.go`.
- **Acceptance**:
  - `"to and type are required"`, `"body is required"`, `"body
    exceeds 64 KB limit"` become `errResultCode("BAD_PAYLOAD",
    ...)`.
  - Audit-log row records `ErrorCode=BAD_PAYLOAD` (assertion
    pattern from `audit_test.go:67`).
  - `errResultCodes` table in the niwa-mesh skill is updated to
    list these as `BAD_PAYLOAD` cases.

### Issue 5.8 — Surface daemon log path in CLI failures

Standalone.

- **Title**: `feat(cli): print daemon.log path when daemon-related operations fail`
- **Goal**: a user who hits a daemon failure (catch-up scan warning,
  inbox-watch-registration warning, watchdog-killed task) should not
  have to read source code to find the log.
- **Acceptance**:
  - When `niwa mesh ls`, `niwa apply`, or `niwa create` detects a
    daemon-side failure, the printed error includes `(see
    <instance-root>/.niwa/daemon.log for details)`.
  - The path is computed once via a shared helper so the format stays
    consistent.
  - Skill docs (`docs/guides/sessions.md`) cross-link the daemon log
    location as a known troubleshooting surface.

### Issue 5.9 — Aggregate clone failures instead of first-only

Standalone.

- **Title**: `fix(apply): aggregate all clone failures via errors.Join`
- **Goal**: when several repos fail to clone in one apply, surface
  every cause (matching the pattern at `apply.go:282`).
- **Acceptance**:
  - `apply.go:1119-1138` collects all `r.err` values into a slice;
    on any non-nil, returns `errors.Join` of `fmt.Errorf("cloning
    repo %s: %w", r.name, r.err)` per failure.
  - `cancel()` is still called on the first failure to short-circuit
    the workers.
  - Unit test forces three clone failures and asserts `errors.Is`
    finds each individually.

### Issue 5.10 — Add recovery hint to "not inside a niwa workspace"

Standalone.

- **Title**: `fix(cli): suggest next action in destroy not-in-workspace error`
- **Goal**: align `destroy.go:78` with `create.go:107`.
- **Acceptance**:
  - Message becomes `"not inside a niwa workspace or instance; cd
    into a workspace, or run niwa destroy <name> from the
    workspace root"`.
  - Functional test assertion updated.
  - No other callers depend on the exact wording (verified via
    grep before merge).

---

## Appendix: file-line index of all citations

```
internal/cli/apply.go:75-103,277-284,310-345
internal/cli/create.go:87-107,137,177
internal/cli/destroy.go:60-91,103-147,196-220,247
internal/cli/init.go:130-256,381,548-553
internal/cli/mesh_watch.go:65-75,156-165,170-211,228,250,255,277,2236-2260
internal/cli/mesh_report_progress.go:33,37,44,51
internal/mcp/auth.go:78-216
internal/mcp/handlers_session.go:30,47,148-176,239-245
internal/mcp/handlers_task.go:122-286,541,629-647,795,1023
internal/mcp/server.go:405-486,697-721,755-803,988-1006
internal/workspace/apply.go:221-1670 (selected lines per inventory)
internal/workspace/daemon.go:35-101,118-167,253-258
docs/designs/DESIGN-niwa-mesh-reliability.md:1020-1051,1118-1135
wip/plan_niwa-mesh-reliability_issue_2_body.md:24-30
wip/plan_niwa-mesh-reliability_issue_6_body.md:34-44
wip/plan_niwa-mesh-reliability_issue_7_body.md:23-45
```
