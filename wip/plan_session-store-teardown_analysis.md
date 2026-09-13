# Plan Analysis: DESIGN-session-store-teardown

## Source Document
Path: docs/designs/DESIGN-session-store-teardown.md
Status: Accepted (transitioned to Planned by this run)
Input Type: design

## Scope Summary
Order every refresh of a niwa config directory against the other refreshes and
against niwa's own writes and destructive mapping reads, and let
`niwa worktree destroy` resolve the session id or handle a developer holds to
that session's niwa-managed worktrees, with a stable outcome contract and a
root refusal for the other worktree subcommands.

## Components Identified
- `internal/testhook` (new leaf): named in-process hook points, settable only
  from test code, with a parser-based guard test.
- `internal/configdir` (new leaf): the lock (`<dir>.lock`, shared/exclusive,
  30s test-overridable bound, nested-acquisition detector, `O_NOFOLLOW`), the
  swap layout names, `HoldsSnapshot`, staging creation, the recovery rules,
  and the `Mutate`/`Read` entry points.
- Snapshot writer and swap (`internal/workspace/snapshotwriter.go`,
  `snapshot.go`): private staging, unlocked fetch, exclusive
  recover+carry-over+swap, trash renames with deletion after release.
- Mapping store (`internal/workspace/session_map.go`): locked writes, deletes
  and reads; `os.Mkdir` of the `sessions/` leaf only;
  `NewestMappingPerInstance`.
- Watch state (`internal/watch/state.go`): both writers under the lock, no
  `MkdirAll` of `.niwa`.
- Config discovery (`internal/config/discover.go`): moved-aside repair branch
  with ownership checks.
- Root state (`internal/workspace/state.go`, `apply.go`): atomic locked
  `SaveState`, `IsSingleInstanceLayout`, skip-after-failed-read.
- Root refusal (`internal/cli/session.go`, `session_lifecycle_cmd.go`,
  `apply.go`): `discoverInstanceRoot` on `ClassifyCwd`, `errAtWorkspaceRoot`,
  the `worktree list` branch.
- Destroy resolver (`internal/cli/worktree_destroy_resolve.go`, `list.go`):
  scope, one locked mapping read, matching, ordered instance checks, per-worktree
  destroys, the R9 outcome contract, `--force` refusal with a session.
- Functional coverage and docs (`test/functional/`, `docs/guides/`).

## Implementation Phases (from design)
Phase 1: Test seam and failing tests on today's code. Adds `internal/testhook`
and its guard test, three hook points with no behavior change, and the forced
tests for mapping write, mapping delete, watch write during a swap,
refresh-versus-refresh, the reaper's read, the two kill points and the
root-state read. Depends on nothing. Deliberately red; its failing output is
recorded for the PR.

Phase 2: `internal/configdir` with layout helpers, `HoldsSnapshot`, the lock
(nested detector, contended hook, overridable bound), `Mutate`/`Read`, staging
creation and recovery, with unit tests including planted look-alikes. Depends
on Phase 1.

Phase 3: Snapshot writer and in-place writer ordering; mapping store and watch
writers through the entry points; `config.Discover` branch. Mapping, watch,
refresh, reaper and kill tests turn green. Depends on Phase 2.

Phase 4: `instance.json` guards (atomic locked `SaveState`,
skip-after-failed-read). Root-state tests turn green. Depends on Phase 2.

Phase 5: Root refusal (`IsSingleInstanceLayout`, rebuilt
`discoverInstanceRoot` with a layout table test, `worktree list` branch).
Depends on nothing earlier; changes behavior for nine callers.

Phase 6: Teardown resolution (resolver, `NewestMappingPerInstance`, R9/R10
command tests, forced teardown-read test). Depends on Phases 3 and 5.

Phase 7: Functional scenarios (destroy by session id and handle from the root;
four parallel dispatches against a local config source) and guide updates.
Depends on Phases 3 and 6.

## Success Metrics
From the design's Consequences and the PRD's acceptance criteria: no mapping
lost, resurrected or written into a directory left without its configuration
under forced interleavings; no refresh failing because of another; teardown
resolving session ids and handles from the root, any instance or a worktree,
with exit codes 0/1/3/4 and the stated output lines; a killed refresh
self-healing; the bound honored and nested acquisition failing at once;
`GOOS=windows go build` succeeding and `go test -race ./...` passing on Linux
and macOS; no new module.

## External Dependencies
- niwa issue #74 (convention-aware fetch) stays out of scope; this work keeps
  local state inside the rotated directory and carries it across.
- Follow-up issues proposed to the coordinator, not filed here: the root
  `instance.json` stale read-modify-write (notice-key loss) and watch state not
  being carried across a refresh.
