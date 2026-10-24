---
schema: plan/v2
status: Active
execution_mode: multi-pr
split_mode_source: intent
split_rationale: |
  Hard Constraint. The work lands in three repositories in a fixed order:
  tsukumogami/niwa ships `niwa authority check` and a release, and only then
  can tsukumogami/dot-niwa's gate and tsukumogami/shirabe's /coordinate call
  it; a pull request can't span repositories.
tracking_level: none
upstream: docs/designs/DESIGN-gates-honour-grant.md
milestone: "Gates honour the grant a session was dispatched with"
step_count: 7
---

# PLAN: gates-honour-grant

## Status

Active

## Scope Summary

niwa gains a session-bound grant lookup, `niwa authority check`, that tells
a gate which session in an instance holds the dispatch's grant, binds a
worker from its first command, refuses at dispatch a child wider than its
dispatcher, keeps niwa's SessionStart hook from replacing a dispatch's
mapping, and tells hooks where niwa is through `NIWA_BIN`. In dot-niwa the
workspace can grant `cut-release`, and `gate-online.sh` decides merge, close
and release for a bound session by its grant, refusing with what it holds
and lacks, while every other session keeps today's verdict. In shirabe,
`/coordinate` takes its merge, close and release posture from its own grant,
passes a grant to each session it dispatches, and hands a step its worker
lacks the grant for back to the dispatcher.

Each step names the repository it lands in. Steps 1 to 3 land in
tsukumogami/niwa, steps 4 and 5 in tsukumogami/dot-niwa, and steps 6 and 7
in tsukumogami/shirabe.

## Decomposition Strategy

Walking skeleton, cut by behaviour. Step 1 crosses niwa's own path end to
end on one fake-agent dispatch granted `merge-pr`: dispatch writes the
record and the session mapping, and `niwa authority check` with the worker's
session id answers `bound` with that grant, while another session id answers
`not-dispatched`. A pull request can't span repositories, so the skeleton
stops at niwa's lookup; every later reader (the gate, `/coordinate`) consumes
that answer and comes after it. Step 2 closes the gap between launch and
capture. Step 3 adds the never-wider refusal and `NIWA_BIN` and ends in a niwa
release. Steps 4 and 5 make the gate honour the grant; steps 6 and 7 make
`/coordinate` act on it.

## Steps

### Step 1: niwa authority check tells a dispatched session from any other session in its instance

**Goal**: In tsukumogami/niwa, a dispatch made with a grant reaches its worker's session mapping and the new `niwa authority check` lookup, so that one call, given a session id and a directory in the instance, says whether that session is the instance's dispatched session and what it holds. Runs on a fake-agent dispatch with `--grant merge-pr`.

**Acceptance Criteria**:

- [ ] After `niwa dispatch --grant merge-pr` with the fake agent, `niwa authority check --json --session <worker id> -C <instance root> --point merge-pr` exits 0 and prints a document with `"schema": "niwa.authority.check/v1"`, `"state": "bound"`, `"binding": "mapping"`, a `grant` entry for `merge-pr` with source `dispatch`, and a `points` entry for `merge-pr` with `"decider": "session"`.
- [ ] The same call with a different lowercase UUID prints `"state": "not-dispatched"`, `"reason": "no-mapping"` and an empty `grant`.
- [ ] With the worker's mapping rewritten without `origin: dispatch`, the call prints `"state": "not-dispatched"` and `"reason": "hook-mapping"`; with its `dispatch_id` changed, `"reason": "other-dispatch"`.
- [ ] With the instance's record deleted, the worker's id prints `"state": "record-unusable"` and `"reason": "record-missing"`; with the record replaced by invalid JSON, `"reason": "record-malformed"`.
- [ ] The same call run with `-C` set to a repo inside the instance and to a niwa worktree of it gives the same answer as from the instance root.
- [ ] `-C` relative, nonexistent or in no instance, and a session id that isn't a lowercase UUID, each exit 1 with nothing on stdout.
- [ ] Without `--json`, a bound worker reads `session: bound to dispatch <id> (mapping)` and `grant: merge-pr (dispatch)`; a worker dispatched with no grant reads `grant: none`.
- [ ] `niwa authority show`, `show --json` and `decide` print byte-identical output for the existing fixtures, and record fixtures from niwa 0.36.0 and 0.37.0 read through `check` with the grant they recorded.
- [ ] The CI test that asserts no koto is on `PATH` runs the new scenarios.

**Check**: Run the functional scenario `make test-functional` for the new feature file; in its log, the bound step shows `"state": "bound"` with `merge-pr`, and the other-session step shows `"state": "not-dispatched"`.

**Dependencies**: None

**Files**: `internal/authority/check.go`, `internal/cli/authority_check.go`, `test/functional/features/authority-check.feature`

### Step 2: a dispatched worker is bound from its first command

**Goal**: In tsukumogami/niwa, a worker that runs `niwa authority check` before dispatch has finished capturing its session reads `bound`, through the lineage entry's session id once that's written and through the agent's own session records while a capture is pending, and a session started by hand after a failed or abandoned capture never does. niwa's SessionStart hook also leaves a dispatch's mapping in place.

**Acceptance Criteria**:

- [ ] With dispatch's post-launch capture held back by the test seam before the lineage session id is written, the fake worker's `check` with its own id prints `"state": "bound"` and `"binding": "records"`, and a fake worker granted `merge-pr` sees `merge-pr` in its grant.
- [ ] With capture held back after the lineage session id is written and before the mapping, the same call prints `"binding": "lineage"`.
- [ ] While a capture is pending, a second fake session record claiming the instance makes both ids read `"state": "not-dispatched"`.
- [ ] After a capture that times out, and after a dispatch process killed during capture, a fake session record created afterwards in the instance reads `"state": "not-dispatched"`, and the instance holds no `.niwa/capture-pending.json`.
- [ ] After a successful dispatch, the instance holds no `.niwa/capture-pending.json`.
- [ ] `niwa session from-hook` given a SessionStart payload for a session id that already has an `origin: dispatch` mapping exits 0, provisions no instance, and leaves the mapping byte-identical.
- [ ] Repeated `check` calls for a bound worker across `niwa apply`, a change to the workspace's `[authority]` table, a resume through `claude attach`'s fake, and a second dispatch from the instance return the same grant.

**Check**: Run the new functional scenario with the capture seam engaged; its log shows `"binding": "records"` for the held-back worker and `"state": "not-dispatched"` for the session started after the killed dispatch.

**Dependencies**: Step 1

**Files**: `internal/cli/dispatch.go`, `internal/cli/authority_check.go`, `internal/cli/instance_from_hook.go`

### Step 3: niwa dispatch refuses a child wider than its dispatcher, and hooks can find niwa

In tsukumogami/niwa, dispatch refuses a grant wider than the dispatcher's record, settings carry `NIWA_BIN`, and the guide documents `check` and the gate contract, followed by a niwa release.

### Step 4: the workspace can grant cut-release

In tsukumogami/dot-niwa, the grant ceiling and the coordinator role hold `cut-release`, signed off by the workspace owner and merged before or with Step 5.

### Step 5: the gate lets a bound session merge, close and release by its grant

In tsukumogami/dot-niwa, `gate-online.sh` decides merge, close and release for a bound session by its grant, refuses naming what it holds and lacks, and keeps today's verdict for every other session.

### Step 6: /coordinate's posture follows its own grant

In tsukumogami/shirabe, `posture-read.sh` answers merge, close and release from the coordinator's own grant, adds the release step, and reads the workspace as today when there is no grant.

### Step 7: /coordinate dispatches with grants and hands refused steps to its dispatcher

In tsukumogami/shirabe, `/coordinate` passes a grant and its own instance on dispatch, writes the brief's authority line from the grant, and names its dispatcher in a need for a step it lacks.

## Implementation Sequence

Steps 1, 2 and 3 run in order in niwa, each its own pull request, and Step 3
ends with a niwa release that carries `check`. After that release the two
other repositories can proceed side by side. In dot-niwa, Step 4 (the
ceiling, which needs the owner's sign-off) merges before or with Step 5 (the
gate), because the gate turns a bound session's `gh release create` from ask
into deny unless the session can hold `cut-release`; Step 5 also needs Step
3's `NIWA_BIN`. In shirabe, Step 6 then Step 7. The plan's owner details
each step after the one before it reaches its terminal, in a commit that
touches only this plan; Step 3's detailing should give the never-wider
refusal and `NIWA_BIN` a criterion each and name the release as the step's
done condition.
