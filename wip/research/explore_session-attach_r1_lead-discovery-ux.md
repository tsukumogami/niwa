# Lead: What does `niwa session list` need — columns, sort, filters, alignment with `niwa_list_sessions`, and how does it differ from `niwa mesh list`?

## Findings

### 1. Today's `niwa session list` behavior (precise)

`niwa session list` is a **gateway** with two distinct modes today, defined in `/home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/internal/cli/session.go`:

- **Without flags** (`session.go:55-60`): falls through to `runMeshList` (the coordinator-process registry view) and prints to stderr:
  > `warning: 'niwa session list' without flags is deprecated; use 'niwa mesh list' to list coordinator sessions`

  This is a **deprecated alias** intentionally kept for one release per the design doc (`DESIGN-mesh-session-lifecycle.md:397-399`).

- **With `--repo` or `--status`** (`session.go:60`): calls `runSessionLifecycleList` (`session_lifecycle_cmd.go:119-147`), which reads per-session state files from `<instance>/.niwa/sessions/*.json`.

**Lifecycle-mode output** (`session_lifecycle_cmd.go:149-166`):

```
  ID       REPO         STATUS     CREATED              PURPOSE
  ab12cd34 niwa         active     2m ago               implement sessions
```

- Columns: `ID` (8-char hex, 8-wide), `REPO` (12-wide), `STATUS` (10-wide; `active`/`ended`/`abandoned`), `CREATED` (relative time, 20-wide), `PURPOSE` (free-text, truncated at 40 chars to `...`).
- **Sort order**: `sort.SliceStable` by `SessionID` ascending (`session_lifecycle_cmd.go:141-143`). Random hex IDs make this effectively random — there is no time- or status-based ordering.
- **Filters** (`session_lifecycle_cmd.go:131-140`): exact-match `--repo` and exact-match `--status`. Both empty-string-defaulted; no validation that the value is a known status (typing `--status activ` returns an empty list silently).
- **Empty-result behavior**: writes only the header row; no "no sessions" message. (Compare `niwa mesh list` which prints `no coordinator sessions registered` — `mesh_list.go:64-66`.)
- **Output destination**: stdout via `cmd.OutOrStdout()`; the deprecation warning goes to stderr.

The docs at `/home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/docs/guides/sessions.md:167-179` accurately describe the columns (`ID, REPO, STATUS, CREATED, PURPOSE`) and the deprecation fall-back.

### 2. `niwa_list_sessions` MCP tool — schema and divergences

Schema (`/home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/internal/mcp/server.go:389-399`):

```
{
  "name": "niwa_list_sessions",
  "input": {
    "repo":   "string (optional)",
    "status": "string: active|ended|abandoned (optional)"
  }
}
```

Returns a JSON array of `SessionLifecycleState` objects (`handlers_session.go:26-50`). Each entry's full schema (`session_lifecycle.go:30-47`):

| Field | In CLI output? |
|---|---|
| `v` (schema version) | No |
| `session_id` | Yes (ID) |
| `parent_session_id` | **No** |
| `repo` | Yes |
| `purpose` | Yes |
| `status` | Yes |
| `creation_time` (RFC3339) | Yes (rendered as relative `CREATED`) |
| `worktree_path` | **No** |
| `claude_conversation_id` | **No** |
| `creator_pid` | **No** |
| `creator_start_time` | **No** |
| `branch_warning` | N/A (response-only, never persisted) |

**Filter parity**: CLI and MCP both filter by `repo` and `status` exact-match — they share the input schema (`listSessionsArgs` matches `sessionListRepo`/`sessionListStatus`).

**Divergence**: the MCP tool returns the full struct; the CLI hides 5 fields. For attach UX the most important hidden one is `worktree_path` — the operator running `session list` and then `session attach <id>` doesn't see *where* they're attaching to (and `niwa go <repo> <id>` requires the repo as a separate arg). `claude_conversation_id` is also hidden, which matters because attach launches `claude --resume <id>`: an empty `claude_conversation_id` means "no first-task transcript yet" — a state that fundamentally affects whether attach can do anything useful.

**Sort**: the MCP tool returns sessions in directory-read order (whatever `os.ReadDir` gives; `session_lifecycle.go:94-121`). The CLI then re-sorts by `session_id`. So MCP consumers get an undefined order, CLI consumers get hex-id order.

### 3. `niwa mesh list` is a **different concept**, not just a different name

`niwa mesh list` (`/home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/internal/cli/mesh_list.go`) reads `<instance>/.niwa/sessions/sessions.json` — the **coordinator process registry**, distinct from per-session lifecycle files (`session_lifecycle.go:25-29` explicitly notes the two types share no fields).

Columns (`mesh_list.go:68-84`): `ROLE | PID | STATUS (alive/dead) | LAST-SEEN | PENDING`.

- `ROLE` is the role name (e.g. `coordinator`), not a session ID.
- `STATUS` here means **process liveness** (`IsPIDAlive` on `mcp.SessionEntry.PID`), not `active/ended/abandoned`.
- `PENDING` counts inbox JSON envelopes (`session.go:69-86`).
- Sort: by role name ascending (`mesh_list.go:55-57`).
- Only **coordinator-role** entries appear here; workers are deliberately not registered (`mesh_list.go:25-27`, AC-O1 in DESIGN-mesh-session-lifecycle.md).

**The issue's UX sketch confuses the two.** The sketch shows:

```
SESSION_ID  REPO    PURPOSE                   STATUS
0c446995    vision  Long-running learning log active
```

This is the **lifecycle view** (`session list` with flags today), *not* `mesh list`. The sketch is right that this is the discovery surface for attach — coordinator processes aren't what a human attaches to. The sketch is wrong about one thing: it shows `niwa session list` with no flags producing this output, but today that path is the deprecated coordinator-registry alias. Issue #117 implicitly assumes the deprecation is complete and `session list` defaults to the lifecycle view.

### 4. Proposed surface for the attach state

Given the lifecycle struct already has 12 fields and only 5 reach the CLI today, the new attach surface should fit alongside `STATUS` rather than collide with it. Three feasible surface designs:

**Option A — separate `AVAILABILITY` column** (recommended):

```
ID       REPO    STATUS    AVAILABILITY  CREATED   PURPOSE
ab12cd34 niwa    active    idle          2m ago    implement sessions
ef56gh78 niwa    active    attached(dan) 30s ago   pair-debug edge case
```

- `STATUS` keeps its current lifecycle meaning (`active`/`ended`/`abandoned`), preserving back-compat with scripts that grep `STATUS active`.
- `AVAILABILITY` is the new column; values `idle` (default), `attached`, optionally `attached(<user>)` if multi-user becomes relevant. For an `ended` session, `AVAILABILITY` renders as `-`.
- Stays compatible with #111: that issue adds a `daemon` sub-object (alive/dead, pid, last_claim_at). A third column `DAEMON` (`alive`/`dead`) lands cleanly next to `AVAILABILITY` without re-overloading `STATUS`.

**Option B — overload `STATUS`**: render as `active+attached`, `active+idle`, etc. Saves a column but breaks scripts that pattern-match `STATUS active` (the issue's open-questions list explicitly asks whether the existing `status` field accommodates this).

**Option C — hide attach state behind `--show-attached`**: poor fit. The whole point of attach discovery is finding which session is currently held by a human; hiding it behind a flag means operators have to know to ask. Default-visible is correct.

**Recommendation: Option A.** It also gives a clean place to surface `claude_conversation_id != ""` ("transcript ready") vs `""` ("no transcript yet, attach will start fresh") — that gating signal could live as a row hint or a fourth state value (`idle-no-transcript`), or as a dedicated column.

### 5. Filters for the new state

Today's `--status` validates against `active/ended/abandoned` only by silent passthrough — no allow-list. Two patterns to consider:

- **`--status attached`**: bad fit. `attached` is orthogonal to lifecycle status; a session is `active AND attached`, not one or the other. Folding it into `--status` forces operators into a single-axis filter when they need two-axis (status × availability).
- **`--available <true|false>`** or **`--attached`**: clean orthogonal flag. `niwa session list --status active --attached` is unambiguous.

For #111 alignment: that issue adds daemon health, which should be a *third* axis: `--daemon-alive=true|false`. So the filter set grows to:

```
--repo <name>           # existing
--status active|ended|abandoned   # existing
--attached / --idle     # new (from #117)
--daemon-alive=...      # new (from #111)
```

This is a clean orthogonal grid; the PRD should commit to all flags being orthogonal AND-combined and to the underlying MCP tool gaining matching parameters so CLI and MCP stay aligned.

### 6. Sort order

Today: by `session_id` (random hex, effectively random). For the attach use case ("operator wants to find a stuck session quickly"), the most useful default is **most-recent-creation-time first**, with the `attached` rows surfaced at the top regardless of age (because that's the operator's hot question: "is anyone in there right now?").

Concrete proposal:

```
sort key (descending):
  1. attached (true > false)
  2. status   (active > ended > abandoned)
  3. creation_time (newer first)
```

Optional `--sort created|id|status` for power users; default is the composite above.

### 7. Attach launch model — exec vs spawn

The issue's sketch (`> [launches claude --resume <transcript_id>]`) plus the operator-facing UX ("user works interactively, then exits Claude Code") strongly implies **`exec`-into-claude semantics**, not spawn-and-detach:

- The session lifecycle file already stores `claude_conversation_id` (set by `registerSessionID` in `server.go:1037-1061` after the first task's worker registers). That field is exactly what `claude --resume <id>` consumes.
- `niwa session attach` doesn't need a daemon hop: the lock can be a file under `.niwa/sessions/<id>.lock`, the cd target is `state.WorktreePath`, and the launch is `syscall.Exec` (or `os.StartProcess` + `Wait`) into the user's `claude` binary.
- This reuses the **shell navigation protocol** for the cd portion: write the worktree path to `NIWA_RESPONSE_FILE`, let the wrapper cd, then exec `claude`. `niwa go` already does the cd half (`go.go:253-281`); attach extends that pattern with a process-replacement step.
- However, exec-replacement makes lock release tricky — niwa is no longer the running process, so it can't run cleanup on `claude` exit. Two design alternatives:
  - **Wrapper-driven cleanup**: shell wrapper runs `niwa session attach --acquire`, then `cd "$worktree" && claude --resume "$tid"`, then `niwa session attach --release`. Three calls.
  - **Niwa-supervised**: niwa stays running, spawns claude, waits, releases on exit. Simpler ownership, but niwa is now a foreground long-running process — different from every other niwa command.
- The PRD has to commit to one. Wrapper-driven is more consistent with how `niwa go` works (niwa exits, shell takes over); supervised is more robust to crashes (release-on-exit is automatic via defer).

Either way: **attach is more than a `niwa go` lookalike**. `niwa go` is purely shell-navigation; attach is shell-navigation + lock + claude launch. The `niwa go <repo> <session-id>` command does NOT have to be a hand-off target — attach owns the full flow.

## Implications

The PRD must commit to:

1. **Deprecation completion**: `niwa session list` (no flags) must default to the lifecycle view in v1 of attach. The current deprecated-alias path in `session.go:55-60` is incompatible with the issue's UX sketch and would be silently confusing.

2. **Five default columns**: `SESSION_ID | REPO | STATUS | AVAILABILITY | CREATED | PURPOSE`. Drop the issue's sketch; commit to AVAILABILITY as a sibling of STATUS, not a substitute.

3. **Default sort**: attached first, then by status, then by creation time descending. Document this so coordinators writing scripts can rely on it.

4. **Filter orthogonality**: `--repo`, `--status`, `--attached`/`--idle`. Coordinate with #111 so `--daemon-alive` lands without re-shaping the flag set.

5. **CLI/MCP alignment**: extend the MCP tool with the same filters AND extend the lifecycle struct (or a sibling response struct) with the new `availability`/`lock_holder` fields. The CLI must render a subset of MCP fields, never a different shape.

6. **Lock surface in MCP, not just CLI**: a coordinator running `niwa_list_sessions` from inside the mesh needs to see `attached: true` to know to not delegate. The new field is not a CLI-only convenience.

7. **Worktree path in CLI output (optional but recommended)**: today's CLI hides `worktree_path`. For attach, operators may want to see it before attaching (e.g. to peek at `daemon.log` first). Consider a `--wide` or `-v` flag, or a sixth column when terminal width allows.

8. **Attach launch model**: pick wrapper-driven vs niwa-supervised exec. Document in DESIGN-shell-navigation-protocol.md (extend it; the doc already lists `session create` as a cd-eligible command — `session attach` would be the first cd+exec command).

## Surprises

- **The "deprecated alias" is still load-bearing.** The issue's sketch is incompatible with the current default; the PRD essentially blocks on completing the rename. The DESIGN doc said "deprecation warning to give scripts a migration path for one release" (line 689) — attach is the right reason to flip the default. This is a small back-compat call but worth surfacing.

- **`PURPOSE` truncation at 40 chars** (`session_lifecycle_cmd.go:160-162`) discards information that could matter when an operator is choosing which session to attach to ("F4 lifecycle metadata PRD section 3 follow-ups…" — the qualifier matters). The issue's sketch shows truncation with `...` but doesn't address it. Worth confirming in the PRD whether the truncation stays or becomes terminal-width-aware.

- **`session_id` sort produces effectively random rows.** Because IDs are random hex, today's "deterministic" sort key is operationally indistinguishable from no sort. This is a usability bug, not just a design choice — operators have no time anchor when scanning the list. Attach makes this acute.

- **The lifecycle struct already version-stamps (`v: 1`)**, so adding `availability` / `lock_holder` doesn't need a v2; it just needs zero-value handling in readers (per `sessions.md:303-308`). Lower migration cost than I expected.

- **Sessions persist after `ended`** (`sessions.md:91-94`). The PRD should decide whether `session list` shows ended sessions by default. Current default is yes (`--status` is optional and defaults to "all"). For attach discovery, default-include-ended is noisy; default-exclude-ended (with `--all` to include) would be friendlier — but that's a behavior change to land separately.

- **`niwa go <repo> <session-id>` requires the repo argument** even though the session ID is unique (`go.go:258-265`). The cross-check is intentional ("a typo in the session ID doesn't silently land you in the wrong directory"). Attach inherits this question: should `niwa session attach <id>` accept just the ID, or should it also require the repo? Suggest ID-only with the lookup resolving the repo internally — operator just typed `session list` and saw the ID, requiring them to retype the repo is friction without safety value (the ID itself is the lookup key).

## Open Questions

- **MCP tool versioning:** if `niwa_list_sessions` adds an `availability` field, do we version the tool (`v2`) or rely on additive-field tolerance? Coordinators built against the current schema must not break.

- **Attached-by-whom field:** in single-user mode, `lock_holder` could just be a boolean. In multi-user mode (out of scope for v1 per the issue), it'd be a username. The PRD should commit to the schema shape now even if v1 only writes one value, so we don't migrate later.

- **Sort stability when `attached` flips:** if a session transitions to `attached` mid-list-poll, the proposed sort moves it to the top. For watch-style usage (`watch -n 1 niwa session list`) this is intuitive; for scripted parsing it's surprising. Probably fine but worth one sentence in the PRD.

- **Empty-list message for the lifecycle view:** today there's no message; mesh list says "no coordinator sessions registered". The lifecycle view should get an analogous message ("no sessions in this instance" or "no active sessions; pass `--all` to include ended ones"). Trivial, but ties into the default-include-ended question.

- **#111 ordering:** is attach (#117) blocked on #111's daemon-health surface, or do they ship in either order? They share the `niwa_list_sessions` schema-evolution path — landing them together avoids two breaking-shape changes in one cycle. The PRD should flag this dependency explicitly.

- **`claude --resume <id>` — does the existing `claude_conversation_id` actually work?** Issue #117 itself flags this under "transcript persistence and locatability". The lifecycle struct has the field, the worker MCP server writes it on first `running` transition (`server.go:1047-1061`), but no test verifies that `claude --resume <captured-id>` actually loads the transcript. Worth a spike before the PRD commits to attach-launches-claude as the user-facing UX.

- **Should `session list` collapse parent/child trees?** `parent_session_id` exists in the struct but is never surfaced. If a session has children, an operator attaching to the parent might want to see the tree. Probably out of scope for v1, but the sketch's flat list assumes no hierarchy.

## Summary

`niwa session list` today is a two-mode gateway whose flagless default is a deprecated alias for `niwa mesh list`; with `--repo` or `--status` it shows lifecycle sessions sorted by random hex ID with columns `ID | REPO | STATUS | CREATED | PURPOSE`, and `niwa_list_sessions` exposes 12 fields of which the CLI renders 5. The PRD must commit to (a) flipping the default to the lifecycle view, (b) adding an `AVAILABILITY` column orthogonal to `STATUS` rather than overloading status, (c) a meaningful default sort (attached first, then status, then creation-time-desc), and (d) keeping CLI columns a strict subset of the MCP tool's response shape so they don't drift; the schema-shape change should be coordinated with #111 to avoid two breaking changes. The biggest open question is whether `claude --resume <claude_conversation_id>` actually works against the conversation IDs niwa captures today — if not, attach's user-visible UX is blocked on a worker-spawn change to persist transcripts in a way `claude` can resume them.
