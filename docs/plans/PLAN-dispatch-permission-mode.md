---
schema: plan/v1
status: Active
execution_mode: single-pr
tracking_level: none
upstream: docs/designs/DESIGN-dispatch-permission-mode.md
milestone: "dispatch permission mode"
issue_count: 1
---

# PLAN: dispatch permission mode

## Status

Active

## Scope Summary

Derive a `--permission-mode bypassPermissions` argv value for a `niwa
dispatch`-launched Claude Code worker from the workspace's already-
materialized instance settings, when the operator didn't pass the flag
explicitly, and only for Claude (never Codex) — restoring the unattended-
dispatch behavior Claude Code 2.1.258 broke.

## Decomposition Strategy

**Horizontal.** The design's Decision Outcome (Option A) makes this shape
inevitable rather than a free choice: it commits to one `readInstanceSettings`
call site, one struct-field widening, and one conditional, precisely because
the design rejected a second read site (Option B) and rejected pushing logic
into `buildDispatchPassthrough` (Option C) to keep that function a pure argv
builder. Those two rejections are what collapse this into a single,
non-splittable diff against `runDispatch`'s step-9 sequence — there is no
seam a second issue could land on without re-opening a question the design
already closed. One issue covers it.

## Issue Outlines

### Issue 1: fix(dispatch): derive --permission-mode from the workspace's declared permission posture

**Goal**: When a workspace's materialized instance settings declare
`permissions.defaultMode: "bypassPermissions"` and the operator supplied no
explicit `--permission-mode`, forward `--permission-mode bypassPermissions`
to the launched Claude Code worker's argv, without touching Codex dispatch
or regressing existing remote-control/keep-alive behavior.

**Acceptance Criteria**:
- [ ] Dispatching a Claude Code worker into a workspace whose materialized
  `.claude/settings.json` has `permissions.defaultMode: "bypassPermissions"`,
  with no `--permission-mode` passed to `niwa dispatch`, results in the
  launched worker's argv containing `--permission-mode bypassPermissions`.
- [ ] An explicit `--permission-mode` passed to `niwa dispatch` is what
  reaches the worker, regardless of the workspace's declared posture.
- [ ] A workspace whose materialized settings have no
  `permissions.defaultMode`, or a value other than `"bypassPermissions"`,
  results in no `--permission-mode` flag being forwarded from this
  derivation path.
- [ ] Dispatching a Codex worker never receives a derived value through its
  `--sandbox` flag from this feature, regardless of the workspace's
  declared permission posture.
- [ ] A test asserts the derived flag is present for a bypass-configured
  Claude Code dispatch and absent for each of: no posture declared, `"ask"`
  posture, a Codex dispatch, and an explicit `--permission-mode` already
  supplied.
- [ ] A test constructs a fixture where the workspace's `workspace.toml`
  `permissions` value and the materialized `.claude/settings.json`
  `permissions.defaultMode` value disagree, and asserts the derived flag
  tracks the materialized settings value in both directions — proving the
  derivation reads materialized settings, not `workspace.toml`.
- [ ] A materialized `.claude/settings.json` that is absent, unreadable, or
  not valid JSON results in no `--permission-mode` flag being forwarded, and
  the `niwa dispatch` invocation does not fail because of it.
- [ ] The existing `dispatch_remotecontrol_roundtrip_test.go` and
  `dispatch_keepalive_roundtrip_test.go` suites pass unmodified after the
  settings-read call site moves, confirming no regression to remote-control
  default-fill or keep-alive arming.
- [ ] When the derivation fires, a one-line notice is printed to stderr
  naming that `--permission-mode bypassPermissions` was derived from the
  workspace's declared posture. The notice does NOT appear for any of the
  negative cases above (no posture declared, `"ask"` posture, a Codex
  dispatch, or an explicit `--permission-mode` already supplied) — a test
  asserts absence in each of those cases.
- [ ] Must deliver: `go test ./internal/cli/... -run TestDispatch -v` and
  the full `go test ./...` suite both pass.

**Dependencies**: None

**Type**: code
**Files**: `internal/cli/dispatch.go`, `internal/cli/dispatch_plugins.go`

## Dependency Graph

_Empty — single-pr mode expresses dependencies via each outline's own Dependencies field under Issue Outlines rather than a populated diagram._

## Implementation Sequence

Single issue, no dependencies, no parallelization to plan. Issue 1 is the
whole critical path.
