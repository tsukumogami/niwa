---
review_of: docs/designs/DESIGN-worker-permissions.md
reviewer: architecture-review agent
date: 2026-04-28
---

# Architecture review: DESIGN-worker-permissions

## Overall clarity

The design is clear enough to implement without ambiguity. The data flow diagram,
the minimal struct shape for `WorkerPermissionMode`, and the explicit call-site
pseudocode in `spawnWorker` leave no meaningful open questions for the implementer.

## Missing components or interfaces

One gap: the design names `WorkerPermissionMode` as a new function in
`internal/workspace/` but does not specify which file it goes in — the components
table lists a new `permissions.go` while the text in Decision 2 refers only to
"the workspace package." The deliverables list does name `permissions.go`, so
this is consistent, but the component table should make the file name explicit
for both functions (`WorkerPermissionMode` and `WorkerExtraAllowedTools`) to avoid
a reviewer confusion on split-file vs. combined-file placement.

No other interfaces are missing. The `settingsPermissionsDoc` struct is defined,
the `mcp.WorkerFallbackBashTools` var is specified, and `spawnContext.workerPermMode`
is named. Unit-test fixtures are called out in Phase 1 deliverables.

## Phase sequencing

The three phases are correctly ordered. Phase 1 (workspace helper + tool list)
produces independently testable units before Phase 2 wires them in, and Phase 3
adds end-to-end coverage after the wiring exists. No simplification or merging
is warranted: merging Phase 1 and Phase 2 would prevent unit-testing the helper
in isolation before it is live in the daemon, which the design correctly avoids.

## `WorkerExtraAllowedTools` as a separate function

The function is marginal. Its entire body is a one-liner:

```go
if permMode == "bypassPermissions" { return nil }
return mcp.WorkerFallbackBashTools
```

The design justifies it as "testability at the `spawnWorker` call site," but the
call site in `spawnWorker` is itself simple enough that inlining the same
conditional is equally readable and does not require a test — the branch is
covered by any functional test that exercises both permission modes. Recommend
inlining the branch directly in `spawnWorker` (in `mesh_watch.go`) and dropping
`WorkerExtraAllowedTools`. This reduces the public surface of `internal/workspace`
by one function with no loss of testability, since `WorkerPermissionMode` is the
function that needs the fixture-based unit tests.

## `spawnContext.workerPermMode` field placement

Placement is correct. `spawnContext` is already the canonical bundle for
stable-at-startup daemon fields (`backoffs`, `stallWatchdog`, `sigTermGrace`,
`spawnBin`), and it is initialized in the block at line 264 of `mesh_watch.go`
before the event loop starts. Adding `workerPermMode string` there mirrors the
existing pattern exactly and requires no structural change.

The initialization call should appear in the `spawnContext` literal at lines 264-275
alongside `backoffs` and `stallWatchdog`, not as a separate assignment after the
struct literal, to keep all startup-time fields visible in one place.

## Simpler alternatives overlooked

No significantly simpler alternative was missed. The design evaluated all
reasonable options (full bypass, curated-only, per-delegation field) and correctly
rejected each. The chosen approach (startup-time read into `spawnContext`) is the
minimal correct shape.

One minor simplification: the design can omit `WorkerExtraAllowedTools` as noted
above, reducing the workspace package's public API from two new functions to one.

## Summary of recommended changes

1. Drop `WorkerExtraAllowedTools` from `internal/workspace/permissions.go`; inline
   the `permMode == "bypassPermissions"` branch directly in `spawnWorker`
   (`mesh_watch.go` lines 888-895) to reduce unnecessary public API surface.
2. Make the components table explicit that both `WorkerPermissionMode` and (if
   retained) `WorkerExtraAllowedTools` live in `internal/workspace/permissions.go`,
   not in `channels.go`.
3. Initialize `workerPermMode` inside the `spawnContext` struct literal (lines
   264-275 of `mesh_watch.go`) rather than as a post-literal assignment, consistent
   with how all other `spawnContext` fields are set.
