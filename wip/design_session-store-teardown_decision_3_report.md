<!-- decision:start id="race-test-seam" status="confirmed" -->
### Decision: Test-only seam for forcing race interleavings

**Context**

The PRD's concurrent-refresh and root-state criteria (PRD-session-store-teardown.md:373-424) and R25 (:289-293) require tests that fail on the pre-fix code because a test-only hook holds one side at a named point, not because a loop got lucky (:297-302). The points that must be holdable are: a refresh between carry-over and swap (`preserveInstanceState`/`preserveDispatchBriefs`/`preserveSessionMappings` then `SwapSnapshotAtomic`, internal/workspace/snapshotwriter.go:443-469); the gap between the two renames (internal/workspace/snapshot.go:69-77); a refresh held in its fetch; the reaper's and destroy's mapping read landing mid-swap; a process killed at the first two points; and the root `instance.json` read in `Applier.Create` (internal/workspace/apply.go:481) forced to fail.

The existing seams cover none of the pause cases. `testfault` reads `NIWA_TEST_FAULT` and can only return an error or truncate a stream (internal/testfault/testfault.go:14-16, 97-113). The package-variable pattern (`driftCheckBackoff`, snapshotwriter.go:32, overridden at snapshotwriter_test.go:73-75; `provisionInstanceFunc`/`destroyInstanceFunc`, internal/cli/instance_from_hook.go:118,123) is well established but only reaches code in the same package. That matters because the reaper's destructive sweep lives in internal/cli (`selectBackstopTargets` reap.go:572, `reapBackstop` reap.go:719, both reading `workspace.ListSessionMappings` at reap.go:342,580), and destroy's new mapping lookup will too, while the swap they must land inside lives in internal/workspace. `ListSessionMappings` treats a missing store as empty (session_map.go:199-205), which is exactly what a mid-swap read sees.

Functional tests start the niwa binary as separate processes with an environment built per scenario (test/functional/steps_test.go:90-130, `envOverrides` at :126-128). The PRD accepts forced tests against the snapshot writer as the proof for R11-R13 across commands and treats the four-way parallel dispatch as the command-level check (PRD:299-302), so cross-process forcing is not required.

**Assumptions**

- The fix's per-directory lock contends between goroutines in one process, as the existing flock helper does (one open file description per acquisition, per the concurrency research on codex_trust_lock_unix.go). If the fix adds an in-process mutex in front of flock instead, the goroutine tests still hold, but the kill test must go through a real child process, as chosen below.
- Hook points can be added to the pre-fix code without changing behavior, in a commit that lands before the fix along with the tests. That's how the "pre-fix fails" criteria are demonstrated. Where the pre-fix code has no gap to hook (for example, a truncating `os.WriteFile` in `SaveState`), that commit splits the call into open, hook, write, which has the same behavior.
- Lock acquisition polls with a non-blocking try (the existing pattern). The first failed try is where a "contended" hook fires, once per wait.

**Chosen: Named in-process hook points in a leaf `internal/testhook` registry, with helper-process re-exec for kills**

A small leaf package, `internal/testhook`, declares named points as constants and exposes two calls. `Hit(point) error` is called by production code. It does one atomic pointer load and returns nil when no hook is registered. `Set(point, fn func() error) (restore func())` is called only from `_test.go` files. The package doesn't import `testing`. Production code calls `Hit` at:

- `snapshot-carried-over`: after the carry-over copies, before `SwapSnapshotAtomic`, inside whatever section the fix serializes (point 1).
- `snapshot-moved-aside`: between `rename(target, prev)` and `rename(staging, target)` in `SwapSnapshotAtomic` (point 2).
- `store-lock-contended`: the first time a lock acquisition finds the lock held. Tests use it to learn that a waiter is parked without sleeping.
- `root-state-read`: at the root `LoadState` in `Create` (apply.go:481). A hook that returns an error stands in for the failed read (point 6). The same mechanism covers the write-side point the concurrent-disclosure criterion needs.

Each case is forced like this:

- **Points 1 and 2.** A goroutine runs a refresh with a hook that signals "arrived" and blocks on a channel. The test then runs the mapping write or delete, a second refresh, or the reaper or destroy lookup (from internal/cli tests, which can reach the exported registry) in another goroutine. It then waits on either "that goroutine finished" or "`store-lock-contended` fired," and releases the hold.
  - On pre-fix code the other side finishes during the hold: the mapping is lost or resurrected, the reaper reads an empty store, destroy exits 3, or the swap fails with ENOTEMPTY. The assertion fails every time.
  - On fixed code the other side parks, the contended hook fires, the test releases, and the assertions pass.
  - Neither branch depends on timing.
- **Point 3.** No new hook. `materializeAndSwap` already takes a `FetchClient` (snapshotwriter.go:353), so a fake fetcher that blocks on a channel holds a GitHub-style refresh inside its fetch. The 1-second-bound criterion (PRD:404-406) is timed against that.
- **Point 5.** The test re-executes its own test binary (`os.Args[0] -test.run=^TestHelperRefresh$`) with a helper-only env marker read only in `_test.go`. In the child, the helper sets the hook at `snapshot-carried-over` or `snapshot-moved-aside` to write "arrived" to stdout and block. The parent then sends SIGKILL, so the kernel releases the flock and no deferred cleanup runs, just like a real kill. The parent then runs the next refresh or reader in-process and asserts self-heal (PRD:397-403).
- **Guard.** A unit test fails if any non-`_test.go` file calls `testhook.Set`. With that in place there's no production path that can register a hook: no env var, no flag, and no file.

Functional tests keep their current role. The parallel-dispatch scenario is recomposed with a local config source, the configuration it avoids today (session-message-acceptance.feature, scenario comment), and stays command-level and probabilistic, as the PRD allows.

**Rationale**

This is option A's mechanism (a package-level override checked for nil), which the codebase already uses and reviewers already accept. It's lifted into its own package so tests in internal/cli can hold a swap in internal/workspace, which the reaper and destroy criteria need. Holding a hook on a channel is exact in-process, and it runs under `go test -race` with no build variant (CI and CLAUDE.md run untagged `go test ./...`).

The `store-lock-contended` point is what keeps the post-fix branch deterministic: a test knows the waiter is parked without sleeping. Kills go through a real child process, so the flock is released by the kernel rather than a deferred unlock, and nothing kill-capable is reachable from a release binary.

The production cost is one atomic load at four points, each on a millisecond filesystem path. The hook can't be triggered accidentally because only code linked into the process can register one, and the guard test forbids that outside tests. That's a stronger guarantee than the existing env-driven `testfault`.

**Alternatives Considered**

- **Unexported package-level hook variables in internal/workspace, with kills simulated by a panicking hook (option A as posed).** Rejected in that form for two reasons. The reaper and destroy tests live in internal/cli (reap.go:572,719) and can't set an unexported workspace variable. And a panic unwinds through deferred unlocks and cleanup, so it doesn't reproduce a kill. The chosen option keeps A's mechanism and fixes both gaps.
- **Extending testfault with `pause-until-file` and `exit` specs (option B).** This would allow cross-process forcing in functional tests, but the PRD doesn't need it (PRD:299-302). Rejected because it puts a hang and a process exit behind an environment variable that release binaries honor (testfault.go:40-44). A stray `NIWA_TEST_FAULT` would then hang or kill a real command, which is worse than today's error-only exposure. The two-way handshake also turns every forced test into file polling, and the child-process pattern already gives real kills without a production exit path.
- **A Hooks interface threaded from callers (option C).** Rejected because refresh has eight production call sites (configreload.go:66, overlaysync.go:40, apply.go:489, 640, 964, snapshotwriter.go:69, 70, 87). The contended signal would also have to reach the mapping writer, the reaper and destroy. That widens production signatures for test-only benefit, against the footprint constraint in R21, and it gives no ordering power a registry lacks.
- **Build-tag-gated hooks compiled only into test builds (option D).** Rejected because unit tests run untagged. The only custom tag today, `live`, gates a separate suite (Makefile:55). A tag would either change every test invocation or silently compile the forced tests out when someone forgets it. What it buys over a nil check is nothing measurable at runtime.

**Consequences**

- **Easier.** Every forced criterion becomes an in-process goroutine test with channel handshakes, including the reaper and destroy cases driven from internal/cli. Kill tests get real SIGKILL fidelity. `testfault` stays error-only.
- **Harder.** Hook points must be placed in the pre-fix code first so the tests can be shown failing before the fix lands, and they must keep their meaning across the fix's restructuring (unique staging, lock around carry-over and swap). The new lock code has to fire `store-lock-contended`, and the helper-process test needs Unix signals, so it's skipped on non-Unix, which the platforms criterion already treats as a documented gap.
- **Not forced.** The four-way parallel-dispatch functional scenario is the one criterion left probabilistic, as the PRD permits.
<!-- decision:end -->
