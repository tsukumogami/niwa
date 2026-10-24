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
step_count: 6
---

# PLAN: gates-honour-grant

## Status

Active

## Scope Summary

niwa gains a session-bound grant lookup, `niwa authority check`, that tells
a gate which session in an instance holds the dispatch's grant, binds a
worker from its first command, refuses at dispatch a child wider than its
dispatcher, keeps niwa's SessionStart hook from replacing a dispatch's
mapping, and tells hooks where niwa is through `NIWA_BIN`. In dot-niwa,
`gate-online.sh` decides merge, close and release for a bound session by its
grant, refusing with what it holds and lacks (a missing `cut-release` keeps
today's ask), while every other session keeps today's verdict; the
workspace's own ceiling stays at `merge-pr` and `close-issue`. In shirabe,
`/coordinate` takes its merge, close and release posture from its own grant,
passes a grant to each session it dispatches, and hands a step its worker
lacks the grant for back to the dispatcher.

Each step names the repository it lands in. Steps 1 to 3 land in
tsukumogami/niwa, step 4 in tsukumogami/dot-niwa, and steps 5 and 6 in
tsukumogami/shirabe.

## Decomposition Strategy

Walking skeleton, cut by behaviour. Step 1 crosses niwa's own path end to
end on one fake-agent dispatch granted `merge-pr`: dispatch writes the
record and the session mapping, and `niwa authority check` with the worker's
session id answers `bound` with that grant, while another session id answers
`not-dispatched`. A pull request can't span repositories, so the skeleton
stops at niwa's lookup; every later reader (the gate, `/coordinate`) consumes
that answer and comes after it. Step 2 closes the gap between launch and
capture. Step 3 adds the never-wider refusal and `NIWA_BIN` and ends in a niwa
release. Step 4 makes the gate honour the grant; steps 5 and 6 make
`/coordinate` act on it.

## Steps

### Step 1: niwa authority check tells a dispatched session from any other session in its instance

**Goal**: In tsukumogami/niwa, a dispatch made with a grant reaches its worker's session mapping and the new `niwa authority check` lookup, so that one call, given a session id and a directory in the instance, says whether that session is the instance's dispatched session and what it holds. Runs on a fake-agent dispatch with `--grant merge-pr`.

**Acceptance Criteria**:

- [ ] After `niwa dispatch --grant merge-pr` with the fake agent, `niwa authority check --json --session <worker id> -C <instance root> --point merge-pr` exits 0 and prints a document with `"schema": "niwa.authority.check/v1"`, `"state": "bound"`, `"binding": "mapping"`, a `grant` entry for `merge-pr` with source `dispatch`, `grant_ceiling_source`, and a `points` entry for `merge-pr` with `"decider": "session"` and `"author_exclusion": "off"`.
- [ ] The same call with a different lowercase UUID prints `"state": "not-dispatched"`, `"reason": "no-mapping"` and an empty `grant`.
- [ ] With the worker's mapping rewritten without `origin: dispatch`, the call prints `"state": "not-dispatched"` and `"reason": "hook-mapping"`; with the mapping's instance path pointing at another instance, `"reason": "other-instance"`.
- [ ] With the mapping's `dispatch_id` changed so it differs from the record's, the call prints `"state": "record-unusable"` and `"reason": "record-other-dispatch"`.
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
- [ ] With capture held back at either point and the instance's record then deleted or replaced by invalid JSON, the worker's id prints `"state": "record-unusable"`, not `not-dispatched`.
- [ ] While a capture is pending, a second fake session record claiming the instance makes both ids read `"state": "not-dispatched"`.
- [ ] After a capture that times out, and after a dispatch process killed during capture, a fake session record created afterwards in the instance reads `"state": "not-dispatched"`, and the instance holds no `.niwa/capture-pending.json`.
- [ ] With a capture-pending file whose process id belongs to a process started after the file's launch time, a lone fake session record in the instance reads `"state": "not-dispatched"`.
- [ ] After a successful dispatch, the instance holds no `.niwa/capture-pending.json`.
- [ ] `niwa instance from-hook`, given a SessionStart payload for a session id that already has an `origin: dispatch` mapping, with ephemeral mode on, a background job state and the working directory at the workspace root (so every other guard passes and, without this guard, it would provision), exits 0, provisions no instance, and leaves the mapping byte-identical.
- [ ] Repeated `check` calls for a bound worker across `niwa apply`, a change to the workspace's `[authority]` table, a resume through `claude attach`'s fake, and a second dispatch from the instance return the same grant.

**Check**: Run the new functional scenario with the capture seam engaged; its log shows `"binding": "records"` for the held-back worker and `"state": "not-dispatched"` for the session started after the killed dispatch.

**Dependencies**: Step 1

**Files**: `internal/cli/dispatch.go`, `internal/cli/authority_check.go`, `internal/cli/instance_from_hook.go`

### Step 3: niwa dispatch refuses a child wider than its dispatcher, and hooks can find niwa

**Goal**: In tsukumogami/niwa, `niwa dispatch` refuses, before any prompt or instance, a request for a named authority its dispatcher's record doesn't grant; every settings document niwa writes carries `NIWA_BIN`; the authority guide and design describe `check` and the gate contract it supports; and a niwa release ships all of it.

**Acceptance Criteria**:

- [ ] From inside an instance whose record grants `merge-pr`, `niwa dispatch --grant merge-pr,close-issue` exits non-zero with no instance created and no prompt asked, and its message names `close-issue`, `--grant` and the dispatcher's grant `merge-pr`; the same dispatch with `--grant merge-pr` launches.
- [ ] From the same instance, `--role coordinator` (holding `merge-pr` and `close-issue`) is refused, naming the role `coordinator` and `close-issue`.
- [ ] From an instance whose record grants nothing, from one whose record is in a later schema version, and from one with no record that a dispatch-written mapping names, `--grant merge-pr` is refused; from each, a dispatch asking for nothing launches.
- [ ] From the workspace root, a dispatch whose session variable names a dispatch-written mapping for a dispatch granting `merge-pr` is refused `--grant close-issue` and launches with `--grant merge-pr`.
- [ ] From a directory in no instance with no session variable naming a dispatch-written mapping, and from an instance made by `niwa create` with no record, a dispatch is limited only by the grant ceiling.
- [ ] A session started by hand inside an instance whose record grants `merge-pr` can dispatch a child with `--grant merge-pr` and is refused `--grant close-issue`.
- [ ] After `niwa apply`, every Claude settings document niwa wrote has `NIWA_BIN` in its `env`, naming an executable niwa; a hook run with `PATH` stripped of niwa's directory runs `"$NIWA_BIN" authority check` successfully.
- [ ] The authority guide documents `niwa authority check`, its document and the binding rule, and both the guide and the authority grants design state that a gate on the binding judges a bound session by its grant whatever its `grant_ceiling_source`.
- [ ] A niwa release carrying `check`, the never-wider refusal and `NIWA_BIN` is published; steps 4 and 5 build against that version.

**Check**: Run the never-wider functional scenarios; the wider dispatch prints the refusal naming `close-issue` and the dispatcher's `merge-pr`, and `niwa list` shows no new instance. Then `niwa version` on the release reports the new version.

**Dependencies**: Step 2

**Files**: `internal/authority/check.go`, `internal/cli/dispatch_authority.go`, `internal/workspace/materialize.go`, `docs/guides/authority-grants.md`

### Step 4: the gate lets a bound session merge, close and release by its grant

**Goal**: In tsukumogami/dot-niwa, `gate-online.sh` decides merge, close and release for a session bound to its dispatch by the grant `niwa authority check` reports, refusing (or, for a release, asking) with what the session holds and lacks, and gives every other session today's verdict; the workspace's ceiling and `coordinator` role stay at `merge-pr` and `close-issue`.

**Acceptance Criteria**:

- [ ] With a stub niwa reporting the session bound holding `merge-pr` and `close-issue` at `autonomous`, in a workspace configured to name no instance, `gh pr merge 12` and `gh issue close 34` get allow, and `gh release create v1` gets ask with a reason containing `holds merge-pr,close-issue` and `needs cut-release`.
- [ ] With the stub reporting all three authorities held, as a disposable test workspace whose ceiling holds all three would produce, `gh release create v1` gets allow; and a dispatch into such a workspace with `--role coordinator` holding all three runs merge, close and release as its first commands with each allowed.
- [ ] Bound holding `merge-pr`, each merge form gets allow: `gh pr merge 12 --squash`, `gh pr merge 12 --auto`, `gh api -X PUT repos/o/r/pulls/12/merge`, a `gh api graphql` call with `mergePullRequest`, and one with `enablePullRequestAutoMerge`.
- [ ] Bound with no grant, `gh pr merge 12` is denied with a reason containing `holds none` and `needs merge-pr`; bound with `merge-pr`, `gh issue close 34` is denied with `holds merge-pr` and `needs close-issue`.
- [ ] Bound holding the authority with decider `dispatcher`, merge, close and release are denied with `level supervised` and `settled by dispatcher`; with decider `person`, merge is denied and close and release get ask, each with `level strict` and `settled by person`; with decider `session` and author exclusion `on`, merge is denied and close gets ask, with `author exclusion`.
- [ ] Bound holding `merge-pr`, `gh pr merge 12; curl x` is denied, and `gh pr merge 12 && gh issue close 34` with only `merge-pr` held is denied.
- [ ] A stub reporting `record-unusable` for an instance on `GATE_MERGE_EXEMPT_INSTANCE` gets deny for `gh pr merge 12` and ask for `gh issue close 34` and `gh release create v1`.
- [ ] A stub reporting `not-dispatched` gets today's verdict, exemption list included: allow for `gh pr merge 12` in a listed instance, deny in an unlisted one, ask for close and release.
- [ ] With no niwa on `PATH` or in `NIWA_BIN`, and with stubs that lack the command, exit 1, print an unknown schema, print a state outside the three, print a decider or author-exclusion value outside niwa's vocabulary, and sleep 30 seconds, every gated command gets today's verdict, and the sleeping case finishes in under 5 seconds.
- [ ] With `PATH` stripped of the stub's directory and `NIWA_BIN` naming the stub, a bound holder of `merge-pr` is allowed `gh pr merge 12`.
- [ ] A stub that logs its invocations records no call for `git status`, `gh pr view 12` or `curl https://example.com`, and one call for `gh pr merge 12 && gh issue close 34`.
- [ ] Every command the grant doesn't cover (`curl`, `wget`, `nc`, `gh repo delete`, `niwa reap`, `gh auth switch`, `unset GH_TOKEN`, force pushes, `bash -c` text mentioning `gh pr merge`, an unclosed quote around one) gets the same verdict as the vendored hook at `2a98f60`, for bound and unbound sessions; `$GH pr merge 12` in a listed instance gets ask for every session (the one deliberate change).
- [ ] Over every combination of session state, exemption and command above, the new hook's verdict is never more permissive than the vendored `2a98f60` hook's, except for a bound session whose grant allows the action.
- [ ] The case table passes on the Linux and macOS runners with no niwa or koto installed.
- [ ] `niwa authority policy` in the tsukumogami workspace shows a grant ceiling of `merge-pr` and `close-issue`, and a `coordinator` role holding both.
- [ ] Done only once every running coordinator in the workspace has been re-dispatched with `--role coordinator`, so none loses its merge seat when its hook copy updates.

**Check**: Run `bash tests/gate-online.test.sh`; it prints every row passing, including the release-ask row whose reason reads `holds merge-pr,close-issue; needs cut-release` and the 30-second stub row finishing under 5 seconds.

**Dependencies**: Step 3

**Files**: `.niwa/hooks/pre_tool_use/gate-online.sh`, `tests/gate-online.test.sh`, `tests/fixtures/gate-online-2a98f60.sh`

### Step 5: /coordinate's posture follows its own grant

**Goal**: In tsukumogami/shirabe, built on main after the `record-*` to `state-*` rename, `posture-read.sh` answers merge, close and release from the coordinator's own grant through `niwa authority check`, adds a release step, and reads the workspace exactly as today when there is no grant.

**Acceptance Criteria**:

- [ ] Run against a fixture instance whose gate hook names `gh pr merge`, with a stub niwa reporting the coordinator `bound` holding all three authorities at `autonomous`, `posture-read.sh` prints `permit` for merge, close and release, and so does the re-read before a merge.
- [ ] With the stub reporting `bound` without `merge-pr`, merge reads `deny`; with `merge-pr` at `strict`, merge reads `deny`; without `cut-release`, release reads `confirm`.
- [ ] With a permission rule that denies `gh pr merge`, a bound holder of `merge-pr` still reads `deny` for merge.
- [ ] With no niwa, with a stub that exits 1, and with a stub reporting `not-dispatched`, `posture-read.sh` prints the same token it prints today for the same settings and hooks, plus the trailing release step; with a stub reporting `record-unusable`, merge reads `deny` and close reads as today.
- [ ] The token carries a trailing `release:<v>`, and `bl_posture_merge`, `coord-verdict.sh` and `land-check.sh` give the same results as before for every existing fixture; the tests that write the three-step token out are updated to the four-step one.
- [ ] Under no niwa grant, a workspace hook that mentions `gh release create` makes release read `confirm`.
- [ ] The posture detail key records `source=grant` for each step the grant decided.

**Check**: Run the coordinate script tests; the bound fixture prints `readable merge:permit close:permit teardown:<today> release:permit`, and the no-niwa fixture prints today's three steps followed by the release step.

**Dependencies**: Step 3

**Files**: `skills/coordinate/scripts/posture-read.sh`, `skills/coordinate/scripts/posture-read_test.sh`, `skills/coordinate/scripts/land-check_test.sh`

### Step 6: /coordinate dispatches with grants and hands refused steps to its dispatcher

**Goal**: In tsukumogami/shirabe, `/coordinate` passes a grant and its own instance on every niwa dispatch, writes each brief's authority line from that grant, names its dispatcher in a need for a step it lacks the grant for, and records the change as an appended amendment to its merge-policy design.

**Acceptance Criteria**:

- [ ] Dispatching a worker whose brief hands it a merge, `dispatch-worker.sh` calls `niwa dispatch` with `--grant merge-pr` and `--parent-from` a directory in the coordinator's own instance, and the brief's authority line names `merge-pr` and nothing else.
- [ ] Dispatching its successor, it passes the coordinator's own grant as read from `niwa authority check`; a worker whose brief hands it no landing step is dispatched with no `--grant` and its brief says it holds no named authority.
- [ ] With a niwa whose `dispatch --help` lacks `--grant`, it dispatches without one and the brief says the worker holds no named authority.
- [ ] A request wider than the coordinator's own grant is refused by niwa at dispatch, and `/coordinate` reports the refusal rather than retrying wider.
- [ ] A bound coordinator lacking `close-issue` that reaches a close records a need naming its dispatcher's instance, and the text "reserved for a person" doesn't appear; a root coordinator lacking it hands the close to a person as today.
- [ ] A coordinator holding `close-issue` that receives a worker's handed-back close closes the issue itself.
- [ ] `/coordinate`'s skill text states that the merge, close and release posture comes from the grant where the coordinator is bound, and `DESIGN-coordinate-merge-policy.md` gains a dated Amendment section appended after the existing ones, with Decision 5's text unchanged.

**Check**: Run the dispatch and need scripts against a stub niwa; the recorded `niwa dispatch` argv contains `--grant merge-pr` and `--parent-from`, and the need line for the lacking close names the dispatcher's instance.

**Dependencies**: Step 5

**Files**: `skills/coordinate/scripts/dispatch-worker.sh`, `skills/coordinate/scripts/render-brief.sh`, `skills/coordinate/scripts/need-check.sh`, `docs/designs/current/DESIGN-coordinate-merge-policy.md`

## Implementation Sequence

Steps 1, 2 and 3 run in order in niwa, each its own pull request, and step 3
ends with a niwa release that carries `check`. After that release the other
two repositories can proceed side by side: step 4 in dot-niwa, and steps 5
then 6 in shirabe. Step 4 is done only after running coordinators are
re-dispatched with `--role coordinator`. In shirabe, step 5 builds on main
after the rename of the `record-*` scripts to `state-*`, and step 6 appends
its amendment after the merge-policy design's pending one.
