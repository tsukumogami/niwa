---
complexity: testable
complexity_rationale: A new leaf package with a small API plus four hook placements in existing code, all provable by unit tests, a parser guard and a recorded red test run, with no behavior change when no hook is set.
---

## Goal

Add the `internal/testhook` registry with its parser-based guard test, place the three behavior-neutral hook points in today's snapshot writer, swap and apply paths, and land the forced race tests that fail against this commit.

## Acceptance Criteria

- [ ] `internal/testhook/testhook.go` declares `type Point string`, the constants `SnapshotCarriedOver` ("snapshot-carried-over"), `SnapshotMovedAside` ("snapshot-moved-aside"), `ConfigDirLockContended` ("configdir-lock-contended") and `RootStateRead` ("root-state-read"), and exactly two functions: `Hit(Point) error` and `Set(Point, func() error) (restore func())`. Its import block contains only `sync` and/or `sync/atomic`, it declares no `init()` and it calls no `os.Getenv`.
- [ ] `Hit` on a point with no hook set returns nil through a single atomic load; `Hit` on a point with a hook set returns that hook's error verbatim. `Set` returns a `restore` func that clears the point, and panics when called for a point that already has a hook. Unit tests in `internal/testhook/testhook_test.go` cover all four behaviors, including the panic (via `recover`).
- [ ] A guard test parses every non-`_test.go` `.go` file under `cmd/`, `internal/` and `test/` with `go/parser` and fails if any file references `testhook.Set` — matched through the file's own import alias for `github.com/tsukumogami/niwa/internal/testhook`, so an aliased import or a wrapper is caught. The test asserts it found at least one production file importing `testhook` (so an empty walk cannot pass) and is exercised against an in-test synthetic source string that it does flag.
- [ ] `materializeAndSwap` (`internal/workspace/snapshotwriter.go`) calls `testhook.Hit(testhook.SnapshotCarriedOver)` after `preserveInstanceState`, `preserveDispatchBriefs` and `preserveSessionMappings` and before `SwapSnapshotAtomic`, and returns the hook's error (after the existing `safeRemoveAll(staging)` cleanup) rather than discarding it.
- [ ] `SwapSnapshotAtomic` (`internal/workspace/snapshot.go`) calls `testhook.Hit(testhook.SnapshotMovedAside)` between the `os.Rename(target, prev)` and the `os.Rename(staging, target)`, and returns the hook's error, leaving the directory moved aside (no rollback) so the kill tests observe the real interrupted state.
- [ ] `Create` and `Apply` (`internal/workspace/apply.go`) call `testhook.Hit(testhook.RootStateRead)` beside their root `LoadState(workspaceRoot)` calls (the `initState` read in `Create`, the `wsRootState` read in `Apply`); a non-nil hook error makes that variable nil, which is what a failed root-state read yields today. With no hook set both call sites behave exactly as before.
- [ ] `ConfigDirLockContended` is declared but has no production call site in this commit; a grep for it over non-test files matches only the constant declaration (Issue 2 adds the call in `internal/configdir`).
- [ ] Forced tests in `internal/workspace` hold a refresh at `SnapshotCarriedOver` on a channel while another goroutine runs the other side, and assert: (a) `WriteSessionMapping` during the hold returns nil and the mapping is present after the refresh; (b) `DeleteSessionMapping` during the hold returns nil and the mapping is absent after the refresh; (c) a second `materializeAndSwap`/refresh of the same config directory overlapping the first completes with no error from either; (d) a `WriteSessionMapping` held at `SnapshotMovedAside` returns nil and afterwards the directory holds `workspace.toml`, the new refresh's provenance marker, the carried-over `instance.json` and the new mapping.
- [ ] A forced test in `internal/workspace` drives `Create` with a hook on `RootStateRead` that returns an error and asserts the root `instance.json` is byte-identical afterwards (today it is rewritten without `ephemeral_session_mode`), and a second forced test holds one `Create`'s `RootStateRead` while another writes root disclosures and asserts the file always parses and never loses `ephemeral_session_mode` or `overlay_url`.
- [ ] A forced test in `internal/watch` holds a refresh of the workspace root's `.niwa` at `SnapshotCarriedOver` and asserts a `writeHandledState` (via `AppendHandled`) and a `SaveStagedRecord` issued during the hold return nil and their files are present after the refresh completes.
- [ ] A forced test in `internal/cli` holds a refresh at `SnapshotMovedAside` while `selectBackstopTargets` reads the mapping store, against a mapped dispatch instance older than the backstop threshold, and asserts the instance is not selected for reaping and its mapping survives.
- [ ] Two kill tests in `internal/workspace` re-execute the test binary with a marker read only from `_test.go` code, block the child at `SnapshotCarriedOver` and at `SnapshotMovedAside` respectively, and `SIGKILL` it from the parent; they assert the post-kill on-disk shape and that the next refresh succeeds. Both call `t.Skip` on non-unix (`runtime.GOOS == "windows"`), and no hook-using test in any package calls `t.Parallel`.
- [ ] At this commit `go build ./...` and `GOOS=windows go build ./...` succeed and every pre-existing test still passes, while `go test ./internal/workspace/... ./internal/watch/... ./internal/cli/...` fails, naming each forced test added above. That failing output is captured verbatim and carried into the pull request body as the pre-fix evidence for PRD R24-R25; it is not committed to the tree.

## Dependencies

None

## Downstream Dependencies

Issue 2 needs the `internal/testhook` registry and the `ConfigDirLockContended` point constant to exist, so `internal/configdir`'s lock helper can call `testhook.Hit(testhook.ConfigDirLockContended)` on its first failed acquisition try and the contention tests can observe a waiter parking on the lock. Issues 3 and 4 turn the forced tests from this issue green without editing them: Issue 3 covers the mapping, watch, refresh-versus-refresh, reaper and kill tests, Issue 4 the two root-state tests.
