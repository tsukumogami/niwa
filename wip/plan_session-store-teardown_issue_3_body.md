---
complexity: testable
complexity_rationale: Mechanical rerouting of five existing files onto an already-tested lock package, where correctness is proved by the forced race tests written in Issue 1 turning green rather than by new design judgement.
---

## Goal

Route the snapshot writer and swap, the session mapping store, the watch state writers and config discovery's moved-aside repair through `internal/configdir`'s `Read`/`Mutate` entry points, so every niwa write into a refreshed config directory is ordered against the swap and none of them can recreate the directory.

## Acceptance Criteria

- [ ] `materializeAndSwap` no longer uses the fixed `configDir + ".next"` path or its preflight `safeRemoveAll`: it creates `configDir`'s parent when missing, creates its private staging directory via `configdir.NewStaging` inside `configdir.Read`, fetches into that staging `snap/` with no lock held, and performs the marker write, the three `preserve*` carry-over copies and `SwapSnapshotAtomic` inside one `configdir.Mutate`; it removes its own staging directory after the mutate returns.
- [ ] `SwapSnapshotAtomic` takes `prev` from `configdir.PrevPath` instead of spelling `target + ".prev"`, has lost its unconditional preflight delete of `.prev` (recovery owns that path now), and renames the moved-aside old snapshot to a `configdir` trash name rather than `safeRemoveAll`-ing it in place; the trash is deleted after the lock is released, not inside the `Mutate` callback.
- [ ] `EnsureConfigSnapshotWithStatus` and `refreshSnapshot` run the provenance-marker probe, the `.git` probe and `ReadProvenance` inside `configdir.Read`; the no-drift branch re-reads, compares and rewrites the marker with an atomic write inside `configdir.Mutate`; and `ReconcileAndReloadConfig`'s post-swap `config.Load` runs inside `configdir.Read`.
- [ ] `WriteSessionMapping` and `DeleteSessionMapping` perform their whole write-and-rename (respectively `os.Remove`) inside `configdir.Mutate` on `<workspaceRoot>/.niwa`, and `ListSessionMappings` and `ReadSessionMapping` perform their directory scan and read inside `configdir.Read`; none of the four exported signatures changes.
- [ ] No mapping writer calls `os.MkdirAll`: with `<workspaceRoot>/.niwa` absent, `WriteSessionMapping` and `DeleteSessionMapping` return an error naming the directory instead of creating it, and when `.niwa` exists they create only the `sessions/` leaf with `os.Mkdir` at 0700, with mapping files still 0600.
- [ ] `writeHandledState` and `SaveStagedRecord` run inside `configdir.Mutate` on `<workspaceRoot>/.niwa`, return an error when `.niwa` is absent, and create only their own leaf (`watch/`) with `os.Mkdir`; no `os.MkdirAll` of `<workspaceRoot>/.niwa` remains anywhere in `internal/watch`.
- [ ] `config.Discover` gains exactly one new branch: when a candidate directory has no `.niwa/workspace.toml` but does have both a `.niwa.lock` regular file and a `.niwa.prev` directory, and the candidate, the lock file and the prev directory are all owned by the current user, it calls `configdir.RecoverMovedAside` and re-checks the candidate before walking to the parent. Every other discovery outcome, including the not-found error text, is unchanged.
- [ ] A grep over `internal/workspace`, `internal/watch` and `internal/config` finds no remaining literal config-directory suffix (`".next"`, `".prev"`, `".lock"`, `".trash"`, `".stray"`); those names are produced only by `internal/configdir`.
- [ ] The Issue 1 forced race tests pass unchanged, with no edit to their assertions: `TestForcedMappingWriteDuringSwapSurvives`, `TestForcedMappingDeleteDuringSwapSticks` and `TestForcedOverlappingRefreshesSucceed` in `internal/workspace`, `TestForcedWatchStateWriteDuringSwap` in `internal/watch`, and `TestForcedReaperMappingReadDuringSwap` in `internal/cli`.
- [ ] The Issue 1 kill tests pass: `TestKilledRefreshBeforeSwapLeavesNoStaging` (child killed at `snapshot-carried-over`; the next refresh succeeds and no `.next-*` or `.prev` directory from the first remains) and `TestKilledRefreshBetweenRenames` (child killed at `snapshot-moved-aside`; the next niwa command that reads the config directory finds `workspace.toml` and the pre-kill mappings with no manual cleanup, and the next refresh succeeds).
- [ ] Forced overlapping refreshes complete without error for an overlay directory and for the global configuration directory as well as the workspace root, and with a fake GitHub fetcher they complete without error in both the drift path and the no-change path.
- [ ] With `configdir.Timeout` overridden to 1 second and a fake fetcher holding one refresh's fetch for 3 seconds, a concurrent `WriteSessionMapping` and a concurrent refresh of the same directory each complete within 1 second, showing the fetch is outside the lock.
- [ ] After a forced mapping write landing in the swap's rename window, the live `.niwa` contains that refresh's `workspace.toml` and provenance marker, the carried-over `instance.json`, and the new mapping, and `sessions/` is still mode 0700 with its files 0600.
- [ ] `go build ./...`, `GOOS=windows go build ./...` and `go test -race ./internal/workspace/... ./internal/watch/... ./internal/config/... ./internal/cli/...` all pass, and `go.mod` gains no module.

## Dependencies

Blocked by <<ISSUE:2>>

## Downstream Dependencies

Issue 6's destroy resolver makes its single mapping-store read through the now-locked `ListSessionMappings`, so it inherits R15's guarantee that no teardown decision rests on a mid-swap read without adding a lock of its own; `loadMappingsForDestroy` needs the exported signature to be unchanged. Issue 7's functional four-way parallel-dispatch scenario against a local config source depends on the ordering this issue installs in the refresh and the mapping store: without it, overlapping dispatches still lose mappings and fail each other.
