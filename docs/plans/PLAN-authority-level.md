---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-authority-level.md
milestone: "Authority level"
issue_count: 3
---

# PLAN: authority-level

## Status

Active

## Scope Summary

Build the authority level from `docs/designs/DESIGN-authority-level.md`: the
`internal/authority` resolution package and its config keys, dispatch-time
resolution with refusal and read-back, and the `niwa authority decide`
question proven end to end with a dispatched session. Requirement numbers
below are the PRD's (`docs/prds/PRD-authority-level.md`).

## Decomposition Strategy

Horizontal. The design has one stable interface, the `internal/authority`
package, that both the dispatch path and the new commands call, so it goes
first and the two consumers follow in the order their data flows: dispatch
writes the records that `decide` reads. A walking skeleton would build a
throwaway `decide` before the records it reads exist. Everything lands in one
pull request under the repository's default consolidated delivery preference;
no split branch fires, since none of the three pieces is useful to an
operator without the others.

## Issue Outlines

### Issue 1: feat(authority): add the level, its resolution and config keys

**Goal**: Add `internal/authority` with the level order, parsing,
narrowest-wins resolution, the per-point answer through a `Declarations`
seam, the `[workspace]` and `[global]` `authority_level` keys, `niwa config
set/unset authority-level`, and `niwa authority show`.

**Acceptance Criteria**:
- [ ] Table tests cover `Parse` (trimming, blank as unset, case-sensitive
  rejection), `Resolve` (every source, tie order flag, machine, workspace,
  default, a wider flag refused with a `WiderError` naming the ceiling and
  its source) and `Decide` (R2's table, and the declaration-seam cases from
  the PRD's acceptance criteria, including a narrowed declaration reporting
  the level's source).
- [ ] `ValidPoint` accepts `a`, `merge-2` and a 64-character name, and
  rejects `Merge_Now`, `2merge`, `-merge`, an empty name and a 65-character
  name.
- [ ] A workspace config with `authority_level = "Strict"` fails to load with
  a typed `*InvalidAuthorityLevelError` naming the key, value and accepted
  names; `" strict "` loads as `strict`; `""` loads as unset.
- [ ] `niwa config set authority-level strict` writes the key under
  `[global]` in `$XDG_CONFIG_HOME/niwa/config.toml`; an invalid value exits
  non-zero and leaves the file unchanged; `unset` removes it.
- [ ] `niwa authority show` with workspace `supervised` and machine `strict`
  prints `workspace: supervised`, `machine: strict` and `default dispatch:
  strict from machine`; with neither set it prints `workspace: not set`,
  `machine: not set` and `default dispatch: autonomous from default`; it
  exits 1 outside a workspace or on an invalid workspace or machine value.
- [ ] `niwa apply` exits non-zero on an invalid workspace `authority_level`.
- [ ] `go test ./...` and `go vet ./...` pass.

**Dependencies**: None

**Type**: code
**Files**: `internal/authority/authority.go`, `internal/config/config.go`, `internal/config/registry.go`, `internal/cli/config_authority_level.go`, `internal/cli/authority.go`

### Issue 2: feat(dispatch): resolve, refuse and record the authority level

**Goal**: Make `niwa dispatch` take `--authority`, resolve the level before
provisioning, refuse a wider request or an unreadable workspace config,
print the source line, write the instance authority record and the session
mapping fields, copy them into `niwa list --json`, and add the
`niwa.authority.level` lineage attribute.

**Acceptance Criteria**:
- [ ] Unit tests with the dispatch fakes cover every resolution and refusal
  case in the PRD's "Resolution and refusal" acceptance criteria, including
  that a refused dispatch leaves no instance, no mapping and no launch, and
  prints no authority line.
- [ ] A workspace config with a TOML syntax error refuses dispatch with exit
  1 before provisioning.
- [ ] Every launching dispatch prints exactly one line matching
  `^niwa dispatch: authority level (autonomous|supervised|strict) from (flag|machine|workspace|default)$`.
- [ ] The instance has `.niwa/authority.json` (mode 0600) with the resolved
  level and source before the agent is launched, and the session mapping
  carries `authority_level` and `authority_source`, for Claude and Codex.
- [ ] `niwa list --json` shows both fields for a new dispatch and omits them
  for a mapping without them.
- [ ] A Claude dispatch's `OTEL_RESOURCE_ATTRIBUTES` in the launch
  environment and in the `--settings` env contains
  `niwa.authority.level=<level>`; a Codex dispatch's does not.
- [ ] With nothing configured, the Claude launch argv matches the existing
  dispatch tests and the `--settings` document differs only by the new
  attribute; `--permission-mode` forwarding is unchanged at every level, and
  one case dispatching at `strict` with `--permission-mode bypassPermissions`
  asserts both the forwarded flag and the recorded `strict`.
- [ ] The fake launcher asserts `.niwa/authority.json` exists when it is
  called, and a provisioning failure after the record is written leaves no
  instance and no record behind.
- [ ] `niwa list --json` reads the fields from the newest of two mappings for
  one instance.
- [ ] A functional scenario covers refusal and `niwa list --json` read-back.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/cli/dispatch.go`, `internal/cli/dispatch_lineage.go`, `internal/workspace/session_map.go`, `internal/workspace/state.go`, `internal/cli/list.go`

### Issue 3: feat(authority): answer who decides a decision point

**Goal**: Add `niwa authority decide <point> [--session <id>]` reading the
session mapping, then the instance record, then the workspace default, with
exit codes 0, 3, 2 and 1; prove it end to end with a dispatched session; and
document the feature in `docs/guides/authority-level.md`.

**Acceptance Criteria**:
- [ ] Unit tests cover every case in the PRD's "The decision-point question"
  acceptance criteria: the three levels' answers and exit codes, `--session`
  overriding `CLAUDE_CODE_SESSION_ID`, the instance record used when no
  mapping exists yet, the `(no dispatch record)` fallback, point-name
  validation exiting 2, and outside a workspace exiting 1.
- [ ] A session mapping at `strict` beside an instance record at
  `autonomous` answers `strict` (the mapping wins).
- [ ] A session mapping holding `authority_level: bogus`, and separately an
  instance record holding it, each exit 1 and never fall back.
- [ ] `decide`'s code path constructs no network client (asserted by a test
  that fails if the command imports or calls niwa's network packages).
- [ ] Exit 3 prints only the five `key=value` lines, with no extra error
  text.
- [ ] With 100 session mappings in a test workspace, `decide` finishes in
  under one second in CI.
- [ ] A functional scenario dispatches a fake agent that runs `niwa authority
  decide merge` and writes `settled` on exit 0 or `waiting-for-person` on
  exit 3; the marker reads `settled` in an `autonomous` workspace and
  `waiting-for-person` in a `strict` one.
- [ ] A real Claude Code session dispatched at `autonomous` settles a merge
  decision through `niwa authority decide merge`, and one dispatched with
  `--authority strict` stops for a person; both answers are recorded in the
  pull request.
- [ ] `docs/guides/authority-level.md` covers the keys, the resolution order
  against the permission mode, read-back, `decide` and its exit codes, the
  advisory and unauthenticated nature of the answer, and the nested-dispatch
  gap; `CLAUDE.md`'s guide list names it.

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/cli/authority.go`, `docs/guides/authority-level.md`, `CLAUDE.md`

## Implementation Sequence

Strictly sequential: Issue 1, then Issue 2, then Issue 3. Issue 2 needs the
resolution package and config keys; Issue 3's lookup reads the records Issue
2 writes, and its end-to-end proof dispatches through Issue 2's path. The
critical path is all three.
