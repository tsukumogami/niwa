# niwa CLI surface — UX inventory and assessment

Scope: existing CLI under `internal/cli/`, focused on `session`, `task`, `mesh`,
plus cross-cutting plumbing (`root`, error rendering, exit codes). Verdict
gauges fitness for a production-grade UX bar in the context of the mesh
reliability redesign at `docs/designs/DESIGN-niwa-mesh-reliability.md`.

Top-level verdict: **needs polish**. The command surface covers the right
verbs, but rendering, error discipline, and machine-readable output are not
where they need to be before the mesh reliability work lands. None of the
list/show commands offer JSON output, errors are routinely printed two or
three times, and the mesh-relevant `daemon.alive` / `abandoned` / redelegate
concepts have no CLI surface yet.

## 1. Inventory

Captured by running `/tmp/niwa --help` (built from `cmd/niwa/main.go`) and
each subcommand's `--help`.

### Top-level (`niwa --help`)

Source: `internal/cli/root.go:23-43`. Available commands, in alphabetical
order: `apply`, `completion`, `config`, `create`, `destroy`, `go`, `help`,
`init`, `mesh`, `reset`, `session`, `shell-init`, `status`, `task`,
`version`.

Persistent flags: `--no-progress`, `-h/--help`, `-v/--version`.

### `niwa session …`

Defined in `internal/cli/session.go:11-26` with subcommands wired up across
`session_lifecycle_cmd.go` and `session_register.go`.

| Subcommand | Behavior | Source |
|---|---|---|
| `session create <repo> <purpose>` | Scaffold worktree, write lifecycle state, start per-worktree daemon, write landing path. Success line goes to **stderr** (`fmt.Fprintf(cmd.ErrOrStderr(), "session: created %s at %s\n", ...)`). | `session_lifecycle_cmd.go:20-86` |
| `session destroy <session-id> [--force]` | Kill workers, mark session ended, stop daemon, remove worktree, delete branch (force if unmerged). Success line on **stderr**. No confirmation prompt. | `session_lifecycle_cmd.go:33-115` |
| `session list [--repo] [--status]` | With no flags: warns "deprecated", delegates to `mesh list`. With flags: lists lifecycle sessions in a fixed-width table. | `session.go:32-61`, `session_lifecycle_cmd.go:119-166` |
| `session register [--repo] [--role] [--check-only]` | Hidden-from-help-but-present registration helper for shell hooks; prints `session_id=… role=…` to **stdout** in a key=value format that no other command uses. | `session_register.go:27-91` |

Empty-state for `session list --status active` prints **only the header
row** (`session_lifecycle_cmd.go:150-152`) — no "no sessions found" line.

### `niwa task …`

Defined in `internal/cli/task.go:22-71`.

| Subcommand | Behavior | Source |
|---|---|---|
| `task list [--state --role --delegator --since]` | Reads every `.niwa/tasks/<id>/` via `mcp.ReadState`. Filters with AND semantics, sorts newest-first. Prints fixed-width table to stdout. | `task.go:39-49`, `task.go:78-126`, `task.go:212-234` |
| `task show <task-id>` | Prints envelope, state block, and full transitions log. JSON bodies pretty-printed with two-space indent. | `task.go:51-56`, `task.go:250-351` |

`task list` columns: `TASK | TARGET | STATE | RESTART | AGE | DELEGATOR |
BODY` (`task.go:213-214`). `TASK` column is truncated to 8 chars
(`task.go:239-243`); `task show` requires the **full** UUID, not the short
form, with no documented way to expand a short id. Empty-state prints the
header only (`task.go:215-218`).

### `niwa mesh …`

Defined in `internal/cli/mesh.go:9-17`.

| Subcommand | Behavior | Source |
|---|---|---|
| `mesh list` | Lists coordinator sessions registered in `.niwa/sessions/sessions.json`. Columns: `ROLE PID STATUS LAST-SEEN PENDING`. Liveness via `mcp.IsPIDAlive`. | `mesh_list.go:20-85` |
| `mesh watch --instance-root <path>` | Long-running daemon. Writes PID file at `.niwa/daemon.pid`, log at `.niwa/daemon.log`. SIGTERM for clean shutdown. | `mesh_watch.go:65-75`, `mesh_watch.go:156-200` |
| `mesh report-progress` | Internal-ish: invoked from worker hooks; reads `NIWA_TASK_ID` from env and bumps the stall watchdog deadline. Sets `SilenceUsage: true` (one of the only commands that does). | `mesh_report_progress.go:18-69` |

`mesh list` has the only differentiated empty-state message: `"no
coordinator sessions registered"` (`mesh_list.go:64-66`).

### Other top-level commands (briefly)

`apply`, `create`, `destroy`, `init`, `go`, `status`, `reset`, `config`,
`completion`, `shell-init`, `version` — out of primary scope, but a few
patterns surface in cross-cutting findings below. Notably, `destroy` has
proper destructive-action UX (typed-confirmation prompt, TTY guard,
`--force`) at `destroy.go:108-128, 297-339` and is the model the session
commands should follow.

## 2. Per-command UX assessment

### `niwa session create` — **needs polish**

- Success message (`"session: created %s at %s"`) is written to **stderr**
  (`session_lifecycle_cmd.go:76`). Anyone piping the command to capture the
  session ID gets nothing on stdout. Compare to `destroy.go:138, 276` which
  writes "Destroyed instance: %s" to **stdout**.
- No JSON output mode. Scripts that want the session ID have to scrape the
  human string. The MCP tool returns structured JSON (`session_id`,
  `worktree_path`, `daemon_warning`) — that's already discarded at
  `session_lifecycle_cmd.go:65-72`.
- Error from the MCP layer is unwrapped via `fmt.Errorf("%s",
  result.Content[0].Text)` (`session_lifecycle_cmd.go:62`), losing any
  embedded error_code prefix that the audit log relies on
  (`mcp/audit.go:138`).

### `niwa session destroy` — **needs polish**

- Same stderr/stdout inconsistency as `create`
  (`session_lifecycle_cmd.go:113`).
- **No confirmation prompt** before destroying a session. `niwa destroy`
  (the workspace-level one) has a typed-confirmation flow with a
  non-pushed-work scan (`destroy.go:108-128, 319-339`). `session destroy`
  goes straight to "kill workers, remove worktree, delete branch" with
  only the merged-branch check guarding the branch delete. A worktree may
  have local commits ahead of origin that fall outside the merged-branch
  check.
- `--force` does double duty (delete unmerged branch) but doesn't cover
  the worktree itself — the docs at `session_lifecycle_cmd.go:36-38`
  could be clearer about which work survives and which doesn't.

### `niwa session list` — **needs polish**

- The "deprecated alias" path (`session.go:55-59`) prints a deprecation
  warning and silently runs `mesh list`. There's no removal date
  documented, and the help text contradicts itself (`session list` has a
  short summary suggesting it lists sessions, then the `Long` says it
  delegates to `mesh list`).
- The lifecycle-list table at `session_lifecycle_cmd.go:149-166` uses
  different column widths and a different relative-time formatter than
  `mesh list` — same kind of data, two layouts.
- Empty result prints just the header. No "no sessions found" message.
- No JSON output.

### `niwa session register` — **acceptable** (special-case)

Used by shell hooks, not by humans directly. The `session_id=… role=…`
key=value output (`session_register.go:89`) is fine for shell consumption
but is a third output style that no other command uses. Documenting this
as the intended hook interface (or moving it under a `internal-` prefix)
would help.

### `niwa task list` — **needs polish**

- No JSON output. Filtering is reasonable but every consumer has to
  re-parse the table. The `--since` duration parser
  (`task.go:85-91`) is user-friendly for humans, less so for scripts.
- Empty-state prints just the header (`task.go:215-218`). At minimum
  this should print "no tasks found" when filters are active so users
  can distinguish "no matches" from "command didn't run."
- Truncating the task ID to 8 chars (`task.go:239-243`) without
  letting `task show` accept that prefix is a footgun. Users see
  `ab12cd34` in `task list`, copy it, run `task show ab12cd34`, get
  "task not found" because the directory is named with the full UUID.
- Body summary truncated to 200 chars with no trailing `…` indicator
  (`task.go:202-205`) — looks like a complete short body.
- Corrupted task directories are silently skipped (`task.go:163-167`).
  Comment says "a human can run `niwa task show` on the ID to see the
  raw error" — but the human has no way to discover the ID since
  the listing skipped it. At least a stderr count of skipped tasks
  would help.

### `niwa task show` — **acceptable, but**

- Renders nicely for humans (`task.go:278-321`). Pretty-printed JSON
  for `body`, `result`, `reason`, `cancellation_reason`.
- Error path triple-prints: handler writes to stderr at
  `task.go:258`, then returns `fmt.Errorf("task not found: …")` which
  cobra prints as `Error: …`, plus root `Execute()` prints it again
  (`root.go:55`). See cross-cutting finding #1.
- No JSON output mode (a structured `niwa task show --json` would
  let the niwa-mesh skill ingest task state without the brittle text
  parse paths in `mcp/audit.go:138`).
- Doesn't accept a short-prefix task ID.

### `niwa mesh list` — **acceptable**

- Cleanest empty-state message of the three list commands
  (`mesh_list.go:64-66`).
- Columns are clear (`mesh_list.go:68-69`). The `STATUS` column is
  alive/dead — the design adds a `daemon.alive` concept at the session
  level (DESIGN-niwa-mesh-reliability.md:945, 1062), but `mesh list`
  is about coordinator-process registry, not session daemons. The
  user-visible terminology will collide once both surfaces exist;
  see gap analysis #1.
- No JSON output.

### `niwa mesh watch` — **acceptable**

- Help text covers what it does, where the PID and log file live, and
  how to shut down (`mesh_watch.go:67-75`). Solid.
- `--instance-root` is `MarkFlagRequired` (`mesh_watch.go:62`) so the
  failure mode is a clear "required flag(s) ... not set" message.
- Foreground mode only — no `--detach` to background it. That's
  arguably correct (operators run it under a process supervisor) but
  is undocumented.

### `niwa mesh report-progress` — **good**

- Single-purpose internal helper. No-ops cleanly when env vars are
  missing (`mesh_report_progress.go:25-29`). Sets
  `SilenceUsage: true` (`mesh_report_progress.go:21`) so accidental
  CLI use doesn't dump a usage banner. This should be the model for
  the rest of the package.

### `niwa destroy` (workspace-level) — **good** (reference model)

Cited as a model only — not in primary scope. Demonstrates: typed
confirmation, TTY-guard, `--force`, and explicit success on stdout
(`destroy.go:108-128, 138, 276, 297-339`). The session-level destroy
should reuse these patterns.

## 3. Gaps relative to DESIGN-niwa-mesh-reliability.md

### Gap 1 — `daemon: {alive, pid, started_at}` on session entries

**Design reference:** lines 945, 1053-1074. Each `niwa_list_sessions`
entry gains a `daemon` sub-object distinct from the lifecycle `status`
field.

**CLI gap:** `niwa session list --status …` (the lifecycle view at
`session_lifecycle_cmd.go:149-166`) has no daemon column. Operators
have no way to answer "is this session usable?" without dropping into
the MCP shell. `niwa mesh list` already shows liveness for
**coordinator** processes — there's now a parallel concept for
**worktree session** daemons that the CLI does not surface.

**Concrete change:** add a `DAEMON` column to the lifecycle table.
Render `alive`/`dead` (matching `mesh_list.go:71-74`'s vocabulary) so
the two tables read consistently. Optionally show `pid` when
`--verbose`.

### Gap 2 — `state="abandoned" reason="taskstore_lost"` for dangling tasks

**Design reference:** lines 484-540, 887. Previously-dangling tasks
become real `abandoned` rows with a typed reason.

**CLI gap:**
- `niwa task list --state abandoned` already works (`abandoned` is in
  the documented `--state` enum at `task.go:64`).
- `niwa task show` already prints a `reason:` block when `st.Reason`
  is non-empty (`task.go:313-316`), so the typed reason will surface
  for free.
- BUT `task list` has no column for the reason — operators have to
  `task show` each abandoned task to learn whether it's a normal
  failure or a `taskstore_lost` recovery candidate. Add a "REASON"
  column when `--state abandoned`, or a short reason indicator
  (e.g. `taskstore_lost`) in the BODY column for abandoned rows.
- The "skipped corrupt task" path at `task.go:163-167` will need
  reconsidering: under the design, the daemon writes
  `WriteAbandonedTaskStub` (DESIGN line 507-512) so corrupt
  state.json should become real abandoned rows. The silent-skip
  path should at minimum log a count to stderr so this regression
  is detectable.

### Gap 3 — `niwa_redelegate` MCP tool has no CLI mirror

**Design reference:** lines 51-52, 256, 280, 533, 979-1029. The
canonical recovery primitive for any terminal task.

**CLI gap:** there is no `niwa task redelegate` subcommand. Any
operator who finds an abandoned task in `niwa task list` has to drop
into a Claude Code session and call `mcp__niwa__niwa_redelegate` from
there. That's a poor recovery flow when the operator is already at a
shell prompt running `niwa task list`.

**Concrete change:** add `niwa task redelegate <task-id>` with flags
that mirror the MCP tool surface (lines 982-990): `--to`,
`--session-id`, `--read-only`, `--mode async|sync`, `--expires-at`,
and a `--body-overrides` flag accepting either a JSON string or a
`@file.json` path. Print the response (`task_id`,
`redelegated_from`, `source_state_at_fork`) so scripts can pipe it.
Render `SOURCE_BODY_LOST` (line 1023) with a hint pointing to
`--body-overrides`.

### Gap 4 — `MISSING_SKILLS`, `DAEMON_SPAWN_TIMEOUT`, `SOURCE_BODY_LOST` error codes

**Design reference:** lines 1031-1051.

**CLI gap:** the only place CLI commands invoke the MCP layer
directly is `session create` and `session destroy`
(`session_lifecycle_cmd.go:60, 98`). Both unwrap MCP errors via
`fmt.Errorf("%s", result.Content[0].Text)` which preserves the raw
error text but does **not** read the structured `error_code` prefix
that `mcp/audit.go:138` already parses.

**Concrete change:** introduce a small helper that parses
`error_code: <CODE>\n<message>` (mirror `mcp/audit.go:138`'s logic)
and renders specific codes with recovery hints:

- `DAEMON_SPAWN_TIMEOUT` → "the per-worktree daemon did not start
  within 500ms; check `<worktree>/.niwa/daemon.log` for the spawn
  trace" (the design rolls back the session, so the user is told
  about the timeout rather than left to discover an orphan).
- `MISSING_SKILLS` → "the target session is missing required
  skills: …; install them or pick a different `--session-id`"
  (with a follow-up to `niwa session list` to find candidates).
- `SOURCE_BODY_LOST` (only reachable from a `redelegate` CLI per
  Gap 3) → "the source task's envelope.json is gone; re-supply the
  body via `--body-overrides @body.json`."

### Gap 5 — `roleRoot` redirect (workers reaching coordinator)

**Design reference:** lines 41-43, 70-71, 213, 709-720, 909-916. Pure
internal-routing change inside the MCP server; resolves a path
asymmetry between worktree workers and coordinators.

**CLI gap:** none directly. CLI commands resolve paths via
`resolveInstanceRoot` (`session.go:91-100`) and
`resolveTasksDir` (`task.go:420-434`); both walk up to find
`.niwa/instance.json` and don't care which role the caller occupies.
The redirect is invisible to the CLI.

**Concrete change:** none. Note this in the implementation issues
so the reviewer can confirm.

## 4. Cross-command issues

### 4.1 — Errors print twice (sometimes three times)

Reproducer: `cd /tmp && niwa task list` prints:

```
Error: not inside a workspace instance (no .niwa/instance.json found walking up from /tmp)
Usage: …  (the full usage banner)
not inside a workspace instance (no .niwa/instance.json found walking up from /tmp)
```

Cause: `Execute()` at `root.go:53-58` does `fmt.Fprintln(os.Stderr,
err)` after cobra has already printed the error, AND no command sets
`SilenceUsage` so the usage banner fires too. `niwa task show <bad>`
adds a third copy because the handler writes its own `fmt.Fprintf(…,
"task not found: %s\n", taskID)` at `task.go:258` before returning the
same error.

Only `mesh report-progress` (`mesh_report_progress.go:21`) has
`SilenceUsage: true`. Every other command emits the cobra usage banner
on any RunE error, treating "command crashed" the same as "user typed
the wrong arg."

### 4.2 — Success messages: stdout vs stderr inconsistency

| Command | Success goes to | Source |
|---|---|---|
| `session create` | **stderr** | `session_lifecycle_cmd.go:76` |
| `session destroy` | **stderr** | `session_lifecycle_cmd.go:113` |
| `destroy` (workspace) | **stdout** | `destroy.go:138, 276, 350` |
| `session register` | **stdout** | `session_register.go:89` |

The session lifecycle commands picked stderr deliberately so that
landing-path stdout writes from the shell wrapper aren't polluted —
but that means users can't pipe `session create` through `awk` to
extract the session ID. Either both stdout (with the wrapper using a
sentinel) or both stderr (with a `--quiet` flag) — pick one and
document it.

### 4.3 — No `--json` / `--format` flag anywhere

Confirmed via `grep -n "json\|--json\|--format" task.go mesh_list.go
session_lifecycle_cmd.go`: zero matches. Every list and show command
prints fixed-width tables only. The mesh reliability work introduces
new structured fields (`daemon`, `redelegated_from`,
`source_state_at_fork`); without machine output, every consumer ends
up scraping columns. The MCP server already returns clean JSON; the
CLI just throws it away (e.g. `session_lifecycle_cmd.go:65-72`).

### 4.4 — Empty-state messaging is inconsistent

- `mesh list` with no rows: `"no coordinator sessions registered"`
  (`mesh_list.go:64-66`).
- `task list` with no rows: header only (`task.go:215-218`).
- `session list --status active` with no rows: header only
  (`session_lifecycle_cmd.go:150-152`).

A user running `niwa task list --since 5m` sees a table header and
has to guess whether "no matches" or "broken." Pick one convention —
either always print the header (with a "(no rows)" hint) or never
print it on empty.

### 4.5 — Relative time formatter is fine but limited

`formatRelativeTime` at `status.go:464-490` resolves to "just now",
"Nm ago", "Nh ago", "Nd ago". For tasks older than a day, "3d ago"
loses the timestamp; for tasks 90 days old it says "90d ago" which
is technically correct but unreadable. Worth adding a `--verbose`
or `--full-time` flag for absolute timestamps in `task list` /
`session list`.

### 4.6 — Task ID short-prefix asymmetry

`task list` prints 8-char prefixes (`task.go:239-243`) but `task
show` requires the full UUID (`task.go:256-261`). Standard practice
is to accept any unambiguous prefix on `show`. This will compound
with `redelegate` once it lands (Gap 3): users will copy short IDs
and have them fail.

### 4.7 — Help-text "Subcommands:" lists are out of date

`session.go:21-25` says "Subcommands: create, destroy, list" but
`session register` is also wired in (`session_register.go:21`). The
`mesh.go:13-16` list mentions only `watch` and `list`; `mesh
report-progress` is also wired (`mesh_report_progress.go:15`). These
are static strings, not generated, so they drift.

### 4.8 — Commands accept env-var overrides but don't list them in `--help`

`NIWA_INSTANCE_ROOT` (`task.go:421`, `session.go:92`),
`NIWA_TASK_ID` and `NIWA_SESSION_ROLE`
(`mesh_report_progress.go:26, 31`), `NIWA_RESPONSE_FILE`
(`root.go:38`), `NIWA_RETRY_BACKOFF_SECONDS` /
`NIWA_STALL_WATCHDOG_SECONDS` / `NIWA_SIGTERM_GRACE_SECONDS`
(`mesh_watch.go:81-85`) — none documented in `--help`. A footer
section ("Environment variables: …") on `task`, `session`, and
`mesh` would help discovery.

## 5. Proposed UX issues for the PLAN

Each entry: title (conventional commit prefix), one-sentence goal,
3-5 acceptance criteria.

### 5.1 — `fix(cli): silence cobra usage banner and de-duplicate error output`

Goal: ensure every command prints its error exactly once on stderr,
with the usage banner reserved for actual usage errors.

- Remove the trailing `fmt.Fprintln(os.Stderr, err)` in
  `root.go:53-58` OR set `cobra.Command.SilenceErrors = true`
  globally and keep the trailing print as the single source.
- Set `SilenceUsage: true` on every `RunE` command (currently only
  `mesh report-progress` does).
- Remove the redundant `fmt.Fprintf(cmd.ErrOrStderr(), "task not
  found: …")` at `task.go:258`.
- Add a regression test capturing both stdout and stderr for at
  least one error path per command group.

### 5.2 — `feat(cli): add --json output to task list, task show, session list, mesh list`

Goal: every read-only list/show command can emit structured JSON for
scripting and skill consumption.

- New `--json` flag on `task list`, `task show`, `session list`,
  `mesh list`.
- JSON shape mirrors the MCP `niwa_list_sessions` /
  `niwa_query_task` payloads where they exist; otherwise document
  the shape in the help text.
- `--json` is mutually exclusive with table-rendering side effects
  (no header, no warnings on stdout — warnings go to stderr).
- Functional tests assert the JSON parses and contains the expected
  fields for empty, single-row, and many-row cases.

### 5.3 — `feat(cli): surface daemon liveness in session list`

Goal: operators can answer "is this session usable?" from a single
`niwa session list` invocation, matching the MCP shape introduced by
the mesh reliability design.

- Add `DAEMON` column to the lifecycle-list table
  (`session_lifecycle_cmd.go:149-166`) rendering `alive`/`dead`,
  matching `mesh_list.go:71-74`'s vocabulary.
- Populate from the same probe used by `niwa_list_sessions`
  (`mcp.IsPIDAlive` against the daemon PID).
- `--verbose` exposes `pid` and `started_at` columns.
- `--json` output (paired with issue 5.2) includes the full
  `daemon: {alive, pid, started_at}` sub-object.

### 5.4 — `feat(cli): add niwa task redelegate as the documented recovery command`

Goal: provide a CLI mirror of the `niwa_redelegate` MCP tool so
operators can recover from terminal tasks without launching a Claude
session.

- New `niwa task redelegate <source-task-id>` subcommand wired in
  `internal/cli/task.go` alongside `task list` / `task show`.
- Flag set mirrors DESIGN lines 982-990: `--to`, `--session-id`,
  `--read-only`, `--mode {async,sync}`, `--expires-at`,
  `--body-overrides {<json> | @<path>}`.
- On success, print `task_id`, `redelegated_from`, and
  `source_state_at_fork` (one per line for humans; `--json` for
  scripts).
- Renders `SOURCE_BODY_LOST` and `MISSING_SKILLS` errors with
  actionable hints (see issue 5.5).
- Functional test covering: terminal source recovery, active source
  fork, missing-source-body recovery via `--body-overrides`.

### 5.5 — `fix(cli): render structured MCP error codes with recovery hints`

Goal: replace the opaque "MCP returned an error" passthrough with
per-code recovery guidance.

- New helper in `internal/cli/` that parses the
  `error_code: <CODE>\n<message>` prefix produced by
  `errResultCode` (mirror `mcp/audit.go:138`'s logic).
- For each known code (`DAEMON_SPAWN_TIMEOUT`, `MISSING_SKILLS`,
  `SOURCE_BODY_LOST`, `UNKNOWN_ROLE`, `SESSION_NOT_FOUND`,
  `TASK_ALREADY_TERMINAL`), print a short hint after the message
  pointing to the next CLI action (`niwa session list`,
  `--body-overrides`, etc.).
- Unknown error codes pass through unchanged so future codes don't
  break the renderer.
- Used by `session create`, `session destroy`, and the new `task
  redelegate` (issue 5.4).

### 5.6 — `fix(cli): align stdout/stderr discipline and empty-state messaging`

Goal: all success messages go to stdout; warnings and progress to
stderr; every list command prints a clear empty-state line.

- `session create` and `session destroy` write success summaries to
  **stdout** (currently stderr at `session_lifecycle_cmd.go:76,
  113`). Landing-path delivery moves to a dedicated channel
  (already partly in `NIWA_RESPONSE_FILE`).
- `task list`, `session list --status …` print a "(no tasks)" /
  "(no sessions)" line on empty result, matching `mesh
  list`'s "no coordinator sessions registered" pattern.
- Add stdout/stderr assertions to the existing CLI tests for one
  representative success and one error case per command.

### 5.7 — `feat(cli): accept short task ID prefixes in task show and task redelegate`

Goal: any unambiguous prefix shown in `task list` works on `task
show` and `task redelegate`.

- `task show` (`task.go:250-275`) resolves a prefix by listing
  `.niwa/tasks/` and matching unique prefixes; ambiguous prefixes
  print the matches and exit non-zero.
- Same resolution applies to `task redelegate <source-task-id>`
  (issue 5.4).
- Help text on `task show` documents the prefix behavior.
- Functional test covers exact-id, valid-prefix, ambiguous-prefix,
  and unknown-prefix cases.

### 5.8 — `chore(cli): document env-var overrides and refresh "Subcommands:" lists`

Goal: `niwa <group> --help` accurately lists every subcommand and
documents the env vars users can set.

- `session.go:21-25` and `mesh.go:13-16` Long descriptions list
  every wired subcommand (currently `session register` and `mesh
  report-progress` are missing).
- Add an "Environment variables:" footer to `task`, `session`, and
  `mesh` help text, listing `NIWA_INSTANCE_ROOT`,
  `NIWA_TASK_ID`, `NIWA_SESSION_ROLE`, plus the daemon-side
  `NIWA_RETRY_BACKOFF_SECONDS` / `NIWA_STALL_WATCHDOG_SECONDS` /
  `NIWA_SIGTERM_GRACE_SECONDS` on `mesh watch`.
- Test asserting that every subcommand registered in `init()`
  appears in the parent's `Long` description (so the lists don't
  drift again).

### 5.9 — `feat(cli): add typed-confirmation prompt to session destroy`

Goal: bring the session-level destroy in line with the workspace-
level destroy's UX (`destroy.go:108-128`).

- `session destroy <id>` scans the worktree for unpushed work
  before tearing it down (reuse `workspace.ScanInstance`-style
  scan adapted to a single worktree).
- On found loss + TTY: typed-confirmation prompt against the
  session ID; on found loss + non-TTY: refuse and print the loss
  list, exit non-zero.
- `--force` skips the scan (current `--force` semantics extend
  cleanly: it already bypasses the merged-branch check).
- Test covers clean session, dirty session + TTY confirmation,
  dirty session + non-TTY refusal, and `--force` bypass.

### 5.10 — `feat(cli): show abandoned reason in task list`

Goal: operators can spot `taskstore_lost` recovery candidates from
the list view without drilling into each task.

- When `STATE` is `abandoned`, prefix the BODY column (or add a
  REASON column) with the short reason from `state.json`'s
  `reason.code` field (e.g. `[taskstore_lost]`).
- Same data is already loaded in `collectTaskRows`
  (`task.go:147-181`); read it from `st.Reason` and project to a
  single token.
- `--json` (issue 5.2) emits the full reason object verbatim.
- Test covers a `taskstore_lost` row, a `retry_cap_exceeded` row,
  and a non-abandoned row to confirm the prefix only fires for
  abandoned.

---

Out of scope for this report: shell-completion polish, `niwa go`
ergonomics, the workspace-level commands (`apply`, `init`, etc.) —
those exist but aren't load-bearing for the mesh reliability redesign.
