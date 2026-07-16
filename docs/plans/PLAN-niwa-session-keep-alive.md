---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-niwa-session-keep-alive.md
milestone: "niwa session keep-alive"
issue_count: 5
---

# PLAN: niwa session keep-alive

## Status

Active

## Scope Summary

Implement an opt-in keep-alive for `niwa dispatch` remote-control sessions: a
`--keep-alive` flag plus `[global] keep_alive_on_dispatch` default that arms a
non-visible sub-hourly self-wake on the dispatched session so it stays reachable
across long idle periods, recorded on the durable session mapping and surfaced by
`niwa list`. The mechanism is already validated end-to-end (see the DESIGN /
SPIKE); this plan lands the plumbing in one PR.

## Decomposition Strategy

**Walking skeleton.** The one integration point is the *arming* — niwa injecting a
self-arm nudge that gets the dispatched agent to schedule a non-visible no-op wake.
Everything else (config resolution, the mapping field, observability) is plumbing
that closely mirrors the existing `remote_control_on_dispatch` feature. So the
skeleton issue wires the minimal end-to-end path first (`--keep-alive` → arm the
wake on an RC session), which is the piece worth exercising early; the remaining
issues thicken resolution, the durable record, observability, and tests/docs. The
mechanism's efficacy is already proven (SPIKE), so the skeleton's risk is
integration-into-niwa, not feasibility.

## Issue Outlines

### Issue 1: feat(dispatch): arm a keep-alive self-wake on --keep-alive (skeleton)

**Goal**: `niwa dispatch --keep-alive` on a remote-control session injects niwa's
fixed self-arm instruction at launch so the session schedules a non-visible,
sub-hourly no-op self-wake — the minimal end-to-end keep-alive path.

**Acceptance Criteria**:
- [ ] A `--keep-alive` boolean flag is registered on `niwa dispatch` (tri-state:
      distinguishes unset / explicit-true / explicit-false via a `*bool` or
      `Flags().Changed`, since no existing bool-flag pattern exists to copy).
- [ ] When keep-alive is on AND remote control is on, niwa injects a fixed,
      niwa-authored self-arm instruction via the arming channel chosen in the design
      (B1 SessionStart `additionalContext` preferred; B2 task-prompt prepend as
      fallback). It does NOT append a second `--settings` flag.
- [ ] The arming instruction directs the agent to create exactly one session-scoped
      sub-hourly (interval fixed under ~1h) no-op self-wake, and to no-op if one is
      already present.
- [ ] Requesting `--keep-alive` on a non-RC session performs no arming and prints a
      clear warning (not an error); the dispatch still succeeds.
- [ ] A dispatch without `--keep-alive` injects nothing and is byte-identical to
      today.
- [ ] Unit tests cover: flag present/absent → arm/no-arm; non-RC → warning+no-arm;
      the injected payload is the fixed constant (argv-safe, single element for B2).

**Dependencies**: None

**Type**: code
**Files**: `internal/cli/dispatch.go`, `internal/cli/dispatch_keepalive.go`

### Issue 2: feat(config): keep_alive_on_dispatch resolver (flag > downstream > host)

**Goal**: Add the `[global] keep_alive_on_dispatch` config default and a
`resolveDispatchKeepAlive` resolver so the opt-in resolves flag > downstream >
host-default, default off — mirroring `remote_control_on_dispatch` plus the new
flag layer.

**Acceptance Criteria**:
- [ ] `GlobalSettings.KeepAliveOnDispatch *bool` added (registry.go, tag
      `keep_alive_on_dispatch,omitempty`), nil = today's behavior, mirroring
      `RemoteControlOnDispatch`.
- [ ] `resolveDispatchKeepAlive` resolves precedence flag > downstream > host-default,
      default off; the `--keep-alive` flag overrides the host default in BOTH
      directions (force-on when host off, force-off when host on).
- [ ] A downstream instance setting is respected as a default-fill (host default does
      not override it), matching the RC resolver's shape.
- [ ] Unit tests cover the full precedence matrix (mirroring the RC resolver tests),
      including default-off and both-direction flag override.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/config/registry.go`, `internal/cli/dispatch_keepalive.go`, `internal/cli/dispatch.go`

### Issue 3: feat(session-map): record KeepAlive on the durable session mapping

**Goal**: Record the opt-in on the durable `SessionMapping` so keep-alive state
survives and can be reported.

**Acceptance Criteria**:
- [ ] `KeepAlive bool json:"keep_alive,omitempty"` added to `SessionMapping`
      (session_map.go), following the `Origin`/`Label` `omitempty` precedent.
- [ ] The dispatch mapping literal sets `KeepAlive` from the resolved opt-in.
- [ ] `omitempty` keeps non-opted / legacy mappings byte-identical (round-trip test).
- [ ] The reaper does NOT read `KeepAlive` (no new reaper coupling) — asserted by
      test or by the unchanged reap path.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/workspace/session_map.go`, `internal/cli/dispatch.go`

### Issue 4: feat(list): surface kept-alive live sessions in niwa list (R10)

**Goal**: `niwa list` shows which live sessions are being kept alive.

**Acceptance Criteria**:
- [ ] `niwa list` (and its `--json` output) surfaces a keep-alive indicator per
      instance, sourced from `SessionMapping.KeepAlive` joined with the existing
      liveness signal (`sessionLive`), so it reflects opted-in sessions that are
      still live.
- [ ] Unit test asserts the report matches the set that was opted-in and is live.

**Dependencies**: Blocked by <<ISSUE:3>>

**Type**: code
**Files**: `internal/cli/list.go`, `internal/workspace/state.go`

### Issue 5: test+docs: functional keep-alive scenario and user documentation

**Goal**: An end-to-end functional check for the dispatch keep-alive workflow and
user-facing docs, including the known archive limitation.

**Acceptance Criteria**:
- [ ] A `@critical` functional scenario in `test/functional/features/` covers the
      `niwa dispatch --keep-alive` workflow (opt-in resolves, arming is injected,
      mapping records it, `niwa list` reports it) using the offline `localGitServer`
      fake where remote control can be stubbed; the true bridge-reachability check is
      documented as a manual/known step (not automatable offline).
- [ ] A contributor/user guide documents keep-alive: the opt-in surface (flag +
      `[global] keep_alive_on_dispatch`, default off), that it applies only to RC
      sessions, and the **close-not-archive** release behavior plus the ~7-day cron
      TTL backstop (the confirmed archive limitation).
- [ ] `CLAUDE.md` / `docs/guides` index updated to reference the new guide.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:4>>

**Type**: docs
**Files**: `docs/guides/session-keep-alive.md`, `test/functional/features/keep-alive.feature`, `CLAUDE.md`

## Implementation Sequence

**Critical path:** Issue 1 (skeleton: flag + arming) → Issue 2 / Issue 3 (parallel:
config resolver and the mapping record both build on the flag) → Issue 4 (observability,
needs the mapping field) → Issue 5 (functional test + docs, needs everything).

**Parallelization:** after Issue 1, Issues 2 and 3 are independent and can proceed in
parallel; Issue 4 waits on Issue 3; Issue 5 integrates and closes out. Since this is a
single PR, the sequence is the recommended commit order within that PR.

**Note on Phase 0 validation:** the design's Phase 0 feasibility gate is already
satisfied empirically (no-op wake kept a dispatched RC session reachable ~18h; close/
remove teardown confirmed; archive limitation documented — see the SPIKE), so
implementation does not re-run it; Issue 1 only needs to confirm the chosen arming
channel (B1 vs B2) reliably injects for a `--bg` worker.
