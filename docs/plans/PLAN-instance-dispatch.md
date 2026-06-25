---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-instance-dispatch.md
milestone: "instance-dispatch command"
issue_count: 7
---

# PLAN: niwa instance-dispatch command

## Status

Active

Single-PR plan decomposing the Accepted DESIGN docs/designs/DESIGN-instance-dispatch.md
(upstream PRD docs/prds/PRD-instance-dispatch.md, R1-R46). All six issues land on one
shared branch and one PR. Each outline's acceptance criteria trace to PRD requirements
(R#) and DESIGN decisions (D#); criteria are tagged [offline] (achievable in CI with the
`localGitServer` harness, a stubbed launcher, and fabricated `~/.claude/jobs/<id>/state.json`
files) or [live] (needs a real `claude --bg`).

## Scope Summary

Add a net-new `niwa dispatch` command that creates an ephemeral instance, launches a
`claude --bg` worker rooted in it, captures the worker's session UUID by jobs-dir cwd
correlation, records an ephemeral dispatch-origin mapping, and guarantees no
unreclaimable orphan via command self-rollback plus a marker+TTL reaper backstop. The
existing hook auto-provisioning is untouched.

## Decomposition Strategy

**Horizontal.** The DESIGN sequences a bottom-up build with clear interfaces: additive
schema fields, then a launcher, then identity capture, then the command that integrates
them, then the reaper backstop, then an end-to-end functional scenario. Each lower layer
is a prerequisite for the command that composes them, and each is independently unit-
testable, so layer-by-layer fits better than a thin end-to-end skeleton. The single
functional scenario (Issue 6) provides the end-to-end check once the layers exist. All
issues share one branch and one PR (single-pr): the feature delivers observable value
only as a whole (a dispatch command with guaranteed reclamation), so splitting into
separately-landing PRs would ship building blocks with no standalone user value.

## Issue Outlines

### Issue 1: feat(workspace): add Origin and Cwd fields for dispatch provenance and capture

**Goal**: Add the two additive, backward-compatible struct fields the dispatch path needs:
`Origin` on `SessionMapping` and `Cwd` on the job-state struct.

**Acceptance Criteria**:
- [ ] [offline] `SessionMapping` gains an `Origin string` field (JSON `omitempty`); an
  existing mapping JSON without the field decodes with `Origin == ""` (back-compat). **(D6, R24)**
- [ ] [offline] The job-state struct in `internal/cli/job_state.go` gains a `Cwd string`
  field that decodes from `state.json`'s `cwd`; absent decodes to `""`. **(D3, R19)**
- [ ] [offline] Round-trip unit tests: a mapping written with `Origin: "dispatch"` reads
  back with it; a legacy mapping (no field) still loads and is reaped by existing rules. **(R41)**
- [ ] `go test ./...` passes; no change to existing mapping/job-state call sites' behavior. **(R1)**

**Dependencies**: None

**Type**: code
**Files**: `internal/workspace/session_map.go`, `internal/cli/job_state.go`

### Issue 2: feat(cli): add a capture-free background launcher abstraction

**Goal**: Add an injectable launcher that runs `claude --bg <prompt>` with `cmd.Dir` set
to a target instance directory, generalizing the exec pattern in
`internal/cli/sessionattach/supervise.go`.

**Acceptance Criteria**:
- [ ] [offline] A package-level launcher function variable exists (so tests can substitute
  a fake); the production implementation runs `claude` with `--bg`, the prompt, and the
  pass-through flags as discrete argv elements with `cmd.Dir = instanceDir`. **(D7, R14)**
- [ ] [offline] Unit test asserts the constructed argument vector: prompt and each
  pass-through value (`--model`/`--permission-mode`/`--agent`) are separate argv elements,
  never string-concatenated, so a crafted prompt or value cannot inject a flag. **(D8, security note 1)**
- [ ] [offline] Unit test asserts `cmd.Dir` equals the instance dir and the launcher does
  not require a stdout-capture mode. **(D3, D7)**
- [ ] [offline] An empty prompt is rejected by the launcher (or its caller) before exec. **(R43)**

**Dependencies**: None

**Type**: code
**Files**: `internal/cli/dispatch_launcher.go`

### Issue 3: feat(cli): add jobs-dir cwd-correlation session-identity capture

**Goal**: Recover a launched worker's full session UUID by polling the jobs directory for
the `state.json` whose `cwd` equals the instance directory.

**Acceptance Criteria**:
- [ ] [offline] Capture takes an injectable jobs-dir root and an injectable clock; it polls
  for a `state.json` whose normalized `cwd` (`filepath.EvalSymlinks` + `Clean`) equals the
  normalized instance dir and returns its `sessionId`. **(D3, R19, R21)**
- [ ] [offline] The recovered id is validated with `ValidSessionID` before being returned. **(security note 2)**
- [ ] [offline] Table tests: (a) state.json present immediately; (b) absent then appears
  within the bound (poll succeeds); (c) never appears -> bounded timeout returns a capture
  failure, not a hang; (d) two state.json files claim the same cwd -> capture failure, never
  an arbitrary pick. **(R20, R21, R22)**
- [ ] [offline] A symlinked instance path still matches via the normalization. **(D3 risk)**

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/cli/dispatch_capture.go`

### Issue 4: feat(cli): add the niwa dispatch command with self-rollback

**Goal**: Add `niwa dispatch <prompt>` that composes workspace resolution, creation,
launch, capture, and mapping, with a deferred success-flagged self-rollback.

**Acceptance Criteria**:
- [ ] [offline] Resolves the workspace root from cwd via `ClassifyCwd`: workspace-root,
  inside-instance, and inside-worktree all resolve to the enclosing workspace root (a repo
  checkout classifies as inside-instance or inside-worktree and resolves the same way); an
  outside (non-workspace) cwd returns a clear error and creates nothing. **(R5-R9)**
- [ ] [offline] Preflights `claude` on PATH before creating any instance; absence fails with
  no instance dir and no mapping on disk. **(R16, R13)**
- [ ] [offline] Creates the instance via the existing provision path with a unique
  `disp-<8 hex>` name (customName branch, not the numbered scan), and writes a pending-marker
  containing a creation timestamp inside the instance. **(D2, D4, R11)**
- [ ] [offline] Dispatch invokes the opportunistic reclamation sweep itself (it calls the
  provision path directly, not `runCreate`), so repeated dispatch self-bounds orphans. **(R12)**
- [ ] [offline] Concurrency: N dispatches run with the real unique-naming + UUID-keyed
  mapping (fake launcher returning distinct fabricated cwds/UUIDs) produce N distinct
  instances and N distinct mappings; afterward the workspace state file parses cleanly and
  the mapping store holds exactly N ephemeral mappings, none clobbered. **(R36, R37)**
- [ ] [offline] On success: launches via the Issue 2 launcher, captures via Issue 3, writes
  the mapping (`ephemeral: true`, `origin: "dispatch"`, label if supplied) keyed on the full
  UUID, then removes the marker, then prints the session id and `attach`/`logs`/`stop` hints. **(R3, R14, R23-R25)**
- [ ] [offline] Rollback matrix (fake launcher + fake capture): launch failure, capture
  failure/timeout, and mapping-write failure each destroy the just-created instance (via
  `destroyInstanceFunc`) and return a clear error -- no instance dir and no mapping remain. **(R32-R35, R42)**
- [ ] [offline] A worker self-dispatching (cwd inside an instance) creates a sibling under
  the shared workspace root, never nested. **(R6, R46)**
- [ ] [offline] Flags `--label`, `--model`, `--permission-mode`, `--agent`, `--detach`/`-d`
  are wired; empty prompt rejected; an over-ARG_MAX prompt fails clearly. **(R2, R43, D1)**
- [ ] [offline] With a stubbed attach: a default (no `--detach`) dispatch calls attach
  exactly once with the captured session id, only after the mapping was written; `--detach`
  skips attach and returns. A stubbed attach that fails does NOT roll back or delete the
  mapping -- the command prints hints and warns. **(R47, D1)**
- [ ] [live] `niwa dispatch "<task>"` at a workspace root launches a worker listed by
  `claude agents`, rooted in the instance with its configuration, and (without `--detach`)
  attaches the terminal to it. **(R5, R10, R14, R17, R47)**

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>

**Type**: code
**Files**: `internal/cli/dispatch.go`

### Issue 5: feat(cli): add the marker+TTL reaper backstop scan

**Goal**: Close the SIGKILL gap with a separate reaper scan that reclaims marked, unmapped,
past-TTL orphan instances.

**Acceptance Criteria**:
- [ ] [offline] A scan separate from `selectReapTargets` (which drops unmapped instances as
  `Ephemeral:false`) enumerates on-disk instances and reclaims one only when its directory
  name carries the dispatch `-disp-<hex>` signature, it has no mapping, and its age exceeds
  the backstop TTL. Age uses the `.niwa/dispatch-pending` marker's embedded timestamp when
  present, else the directory mtime (so a SIGKILL before the marker write is still
  reclaimable). The name signal is atomic with directory creation, closing the orphan
  window. **(D4, arch-review)**
- [ ] [offline] Spare/reap matrix: disp-named+unmapped+old -> reaped (with marker, and
  without marker via mtime); disp-named+unmapped+young -> spared (R38); mapped (any age) ->
  not touched by the backstop (the existing sweep owns it); non-dispatch-named developer or
  hook instance -> never touched. **(R38, R41, R31)**
- [ ] [offline] The backstop is invoked on the same opportunistic occasions as the existing
  sweep (and via `niwa reap`); the existing reaper behavior and tests are unchanged. **(R12, R27, R1)**

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:4>>

**Type**: code
**Files**: `internal/cli/reap.go`

### Issue 6: test(functional): add the @critical dispatch lifecycle scenario

**Goal**: An offline end-to-end functional scenario exercising provision, rollback, and
reclamation without a live claude.

**Acceptance Criteria**:
- [ ] [offline] A `@critical` Gherkin scenario uses the `localGitServer` harness, a stubbed
  launcher, and a fabricated jobs-dir: a dispatch provisions an instance and writes a
  dispatch-origin mapping keyed on the fabricated UUID. **(R10, R23, D9)**
- [ ] [offline] An induced launch failure leaves no instance and no mapping (rollback). **(R33)**
- [ ] [offline] A fabricated terminal (`done`) job state, a removed/non-live job state
  (the shape a `claude stop`/Agent-View delete produces), and separately a past-TTL job
  state each let `niwa reap` reclaim the instance and delete the mapping. **(R28, R29, R30)**
- [ ] [offline] A fabricated live (fresh `updatedAt`, non-terminal) job state is spared by
  the sweep. **(R31)**
- [ ] [offline] The existing SessionStart hook, evaluated against a dispatch-created
  instance (a valid instance with `.niwa/instance.json`), hits the existing re-entrancy
  guard and no-ops -- it provisions no second instance -- with the hook code unchanged. **(R39, R40)**
- [ ] `make test-functional-critical` passes. **(testing convention)**

**Dependencies**: Blocked by <<ISSUE:4>>, <<ISSUE:5>>

**Type**: code
**Files**: `test/functional/features/dispatch.feature`, `test/functional/dispatch_steps_test.go`

### Issue 7: test(live): add the live end-to-end dispatch lifecycle test

**Goal**: An automated end-to-end test that runs the real `claude` lifecycle against a
local subscription -- init workspace, dispatch, assert a well-constructed dedicated
instance and a registered session, stop the session, reap, and confirm the instance is
destroyed. Gated to run whenever a live `claude` is available and never silently skipped
locally; skipped only in an environment with no Claude credentials (e.g. CI without
`ANTHROPIC_API_KEY` and no subscription).

**Acceptance Criteria**:
- [ ] [live] The test is guarded by a live-availability gate (build tag + a `claude`
  presence/credential check) and a `make test-live` (or equivalent) target runs it. The
  gate skips ONLY when no live `claude` is usable; when `claude` is usable the test runs
  and is not skipped. CI without credentials skips it; a local run with a subscription
  executes it. **(R48)**
- [ ] [live] Init a fresh niwa workspace, run `niwa dispatch "<task>" --detach`, and assert
  the dedicated instance is well-constructed: a distinct `<config>-disp-<hex>` directory
  under the workspace root, containing `.niwa/instance.json`, the materialized Claude
  configuration (settings/plugins), and the declared instance env. **(R5, R10, R14)**
- [ ] [live] Assert the launched session is registered (`claude agents` lists it) and
  rooted in that instance directory, and that the ephemeral `origin: dispatch` mapping
  keyed on the session UUID exists. **(R14, R17, R23, R24)**
- [ ] [live] Stop the session (`claude stop <id>`), then run `niwa reap`, and confirm the
  instance directory is destroyed and its mapping deleted (reclamation is reaper-primary:
  `stop` drives the session terminal, the next `reap` reclaims). **(R28, R29, R27)**
- [ ] [live] Negative control: a second dispatched session that is still live is NOT
  reclaimed by the same `reap` run -- only the stopped one is. **(R31)**
- [ ] The live run is part of the PR's definition of done: the implementer runs
  `make test-live` locally on a clean build and records the result in the PR. **(R48)**

**Dependencies**: Blocked by <<ISSUE:4>>, <<ISSUE:5>>

**Type**: code
**Files**: `test/live/dispatch_live_test.go`, `Makefile`

## Dependency Graph

Single-PR plan: no separate diagram is rendered. Dependencies are declared in each
outline's **Dependencies** field and serialized in the Implementation Sequence below.

## Implementation Sequence

- **Critical path:** Issue 1 -> Issue 3 -> Issue 4 -> Issue 5 -> Issue 6/7.
- **Parallelizable:** Issues 1 and 2 have no dependencies and can be built first in any
  order; Issue 3 needs only Issue 1. Issue 4 is the integration point and needs 1, 2, 3.
  Issue 5 needs 1 and 4. Issue 6 (offline scenario) and Issue 7 (live scenario) are last,
  each needing 4 and 5, and can be written in either order.
- **Verify gates (run before the PR is marked ready):** `go build ./...`, `go vet ./...`,
  `go test ./...` (unit), and `make test-functional-critical` (the Issue 6 offline
  scenario), all green on a clean cache. The existing ephemeral-session hook tests must
  remain green (R1 additive guarantee).
- **Live verification (definition of done, run locally, never skipped when `claude` is
  usable):** `make test-live` (the Issue 7 lifecycle test) must pass on the implementer's
  machine against a local Claude subscription -- init -> dispatch -> assert well-constructed
  instance + registered session -> stop -> reap -> confirm destroyed. CI skips it when no
  credentials are present; it is not skipped locally. The PR records the local run's result.
