---
schema: plan/v2
status: Active
execution_mode: multi-pr
split_mode_source: intent
split_rationale: |
  Hard Constraint. The work lands in five repositories in a fixed order: the
  dot-niwa gate rule and the shirabe skill change must be merged and released
  before anything relies on them, and dot-niwa's ceiling widening may land
  only after a released skill and a live check. A pull request can't span
  repositories.
tracking_level: none
upstream: docs/designs/DESIGN-release-honours-grant.md
milestone: "A session cuts a release on the authority it was dispatched with"
step_count: 7
---

# PLAN: release-honours-grant

## Status

Active

## Scope Summary

The dot-niwa `gate-online` hook decides a dispatch of a repository's release
workflow (`gh workflow run` naming "Prepare Release" or one of its file
names, or the REST dispatches endpoint) by the session's `cut-release`
grant, asks for a dispatch whose workflow it can't read, and reads `gh`
subcommands and API methods the way `gh` does. shirabe's `/shirabe:release`
reads its own session's grant through a bounded `niwa authority check`,
skips the version-and-notes go for a holder on an ordinary cut, and, after
its own release is published, runs the host installs each repository
declares under `## After release`. koto, niwa and tsuku declare their
installs; niwa's authority guide says what `cut-release` covers. Last,
after a live check in a throwaway repository, dot-niwa's ceiling and
`coordinator` role grant `cut-release`.

Each step names the repository it lands in: step 1 in tsukumogami/dot-niwa,
steps 2 and 3 in tsukumogami/shirabe, step 4 in tsukumogami/koto, step 5 in
tsukumogami/niwa, step 6 in tsukumogami/tsuku, and step 7 in
tsukumogami/dot-niwa.

## Decomposition Strategy

Walking skeleton, cut by behaviour. A pull request can't span
repositories, so the skeleton is the gate: step 1 takes one release
dispatch, `gh workflow run prepare-release.yml`, through the hook's scanner,
its grant lookup and its verdict table end to end against a stub niwa, so a
holder is allowed, a session without the grant is asked with what it lacks,
and a session niwa didn't dispatch is asked. That is the decision every
later step relies on. Step 2 makes the skill read the same answer for its
own go. Steps 3 to 6 tie the installs to the cut. Step 7 is the only step
that removes a person from a release, so it comes last, behind a live check.

## Steps

### Step 1: the gate decides a release workflow dispatch by the session's cut-release grant

**Goal**: In tsukumogami/dot-niwa, `gate-online.sh` reads a dispatch of a
release workflow, by `gh workflow run` or by `gh api` on the dispatches
endpoint, as a command needing `cut-release` and decides it through its
existing grant path; a dispatch whose workflow it can't read asks for every
session; other workflows keep the `179f588` verdict; and `gh` subcommands
and API methods are read as `gh` reads them, for this rule and the existing
ones.

**Acceptance Criteria**:

- [ ] With a stub niwa answering bound holding `cut-release` at
  `autonomous` (decider `session`, author exclusion `off`), `gh workflow run
  prepare-release.yml -f version=1.2.3 -f tag=v1.2.3 -f ref=main`, `gh
  workflow run --ref main release-prepare.yml`, `gh workflow run "Prepare
  Release"`, `gh workflow run PREPARE-RELEASE.YAML`, `gh workflow -R o/r run
  prepare-release.yml`, `gh api -X POST
  repos/o/r/actions/workflows/prepare-release.yml/dispatches -f ref=main`,
  and `gh api repos/{owner}/{repo}/actions/workflows/release-prepare.yml/dispatches?x=1`
  each get allow.
- [ ] With the stub answering bound holding `merge-pr` and `close-issue`,
  `gh workflow run prepare-release.yml` and `gh api -X GET -X POST
  repos/o/r/actions/workflows/prepare-release.yml/dispatches -f ref=main`
  each get ask with a reason containing `holds merge-pr,close-issue` and
  `needs cut-release`, in an instance on the exemption list and off it.
- [ ] Holding `cut-release` with decider `dispatcher` the dispatch is denied
  (`settled by dispatcher`), with decider `person` it gets ask (`settled by
  person`), with author exclusion `on` it gets ask (`author exclusion`), and
  with `dispatcher` and author exclusion `on` together it is denied.
- [ ] With the stub answering `record-unusable`, `not-dispatched`, exiting 1,
  printing an unknown schema, printing two documents or sleeping 30
  seconds, and with no niwa on `PATH` or in `NIWA_BIN`, `gh workflow run
  prepare-release.yml` gets ask, and the sleeping case finishes within 5
  seconds.
- [ ] Under the holder stub and on the exemption list, `gh workflow run
  123456`, `gh workflow run`, `gh workflow run "$W"`, `gh workflow run
  prepare-releas[e].yml`, `gh workflow run prepare-release.y{ml,aml}`, `gh
  workflow run ~/prepare-release.yml`, `gh workflow run
  .github/workflows/prepare-release.yml`, `gh workflow run
  prepare-release.yml extra`, `gh api -X POST
  repos/o/r/actions/workflows/123456/dispatches`, `gh api -X POST
  repos/o/r/actions/workflows/prepare%2Drelease.yml/dispatches`, and `gh api
  "$EP" -X POST` each get ask.
- [ ] Under every stub above, `gh workflow run ci.yml`, `gh workflow run
  "Build and Test"`, `gh workflow run prepare-releases.yml`, `gh api -X GET
  repos/o/r/actions/workflows/prepare-release.yml/dispatches`, `gh api
  --method=get repos/o/r/actions/workflows/prepare-release.yml/dispatches`,
  `gh api "repos/$R/pulls/$N"`, `bash -c 'gh workflow run ci.yml'`, `echo
  "gh workflow run prepare-release.yml"`, `git commit -m "gh workflow run
  prepare-release.yml"` and a heredoc body naming it get the `179f588`
  hook's verdict, and `bash -c 'gh workflow -R o/r run prepare-release.yml'`
  gets ask.
- [ ] With a stub answering `not-dispatched` and no exemption list, `gh pr
  -R o/r merge 12` and `gh api -X GET -X PUT repos/o/r/pulls/12/merge` are
  denied and `gh release -R o/r create v1` gets ask; `gh api
  repos/o/r/pulls/12/merge` with no method flag stays denied.
- [ ] A logging stub records exactly one call with `--point cut-release` for
  `gh workflow run prepare-release.yml`, one call with `--point merge-pr
  --point cut-release` for `gh workflow run prepare-release.yml && gh pr
  merge 12`, one call with a single `--point cut-release` for `gh release
  create v1 && gh workflow run prepare-release.yml`, and none for `gh
  workflow run ci.yml`, `gh workflow run 123456` or `git status`.
- [ ] `gh release create v1 --draft`, `tsuku update koto`, `niwa apply` and
  `claude plugin marketplace update shirabe` get the `179f588` hook's
  verdict under every stub.
- [ ] The case table holds a row for every command named in the PRD's gate
  acceptance criteria, including `gh workflow run --json
  prepare-release.yml`, `gh workflow run -f version=1.2.3
  prepare-release.yml`, `gh api --input body.json` and `gh api
  --method=POST` on the dispatches path (allowed for a holder), `-XGET` and
  `-X get` on it (today's verdict), and `gh workflow run prepare-release.yml;
  curl x` and `gh workflow run prepare-release.yml && gh pr merge 12` with
  only `cut-release` held (both denied, the second with `needs merge-pr`).
- [ ] Every command in the case table runs through the new hook and the
  vendored `179f588` hook under every stub and exemption setting; the new
  verdict is never more permissive except the holder allow rows, and each
  stricter row is marked as expected. The table passes on the Linux and
  macOS runners with no niwa or koto installed.

**Check**: Run `bash tests/gate-online.test.sh`; every row passes, the
holder row for `gh workflow run prepare-release.yml` prints allow, the
merge-only row prints ask with `holds merge-pr,close-issue; needs
cut-release`, and the 30-second stub row finishes in under 5 seconds.

**Dependencies**: None

**Files**: `.niwa/hooks/pre_tool_use/gate-online.sh`, `tests/gate-online.test.sh`, `tests/fixtures/gate-online-179f588.sh`

### Step 2: /shirabe:release takes its go from the session's grant

**Goal**: In tsukumogami/shirabe, a new `release-grant.sh` reports whether
the skill's own session holds `cut-release` through one bounded, validated
`niwa authority check`, and `/shirabe:release` uses that answer to skip only
the version-and-notes go on an ordinary cut, checks the version before it
reaches any command line, and closes its session `abandoned` on a refused
step.

**Acceptance Criteria**:

- [ ] With a stub niwa answering bound holding `cut-release` at
  `autonomous`, `release-grant.sh` prints `go=grant dispatch=<the stub's
  id>` and exits 0.
- [ ] It prints `go=person` and exits 0, with the reason token shown, for:
  the stub answering bound without `cut-release` (`not-held`), decider
  `person` (`decider-person`), decider `dispatcher` (`decider-dispatcher`),
  author exclusion `on` (`author-exclusion`), `record-unusable`
  (`record-unusable`), `not-dispatched` (`not-dispatched`); a `dispatch_id`
  that isn't a UUID and one in upper case, a wrong schema, two documents,
  nothing, text that isn't JSON, and a `state` outside niwa's vocabulary
  (each `unreadable-answer`); exiting 1, an `authority` command without
  `check`, and sleeping 30 seconds, returning within 4 (each
  `lookup-failed`); no niwa on `PATH` or in `NIWA_BIN`, and the only niwa
  under the repository's working tree (each `no-niwa`);
  `CLAUDE_CODE_SESSION_ID` unset (`no-session-id`); and no
  `.niwa/instance.json` at or above the repository root
  (`no-instance-root`). In the last two cases the logging stub records no
  call. None of these runs needs koto.
- [ ] Run against a fixture repository with `gh` stubbed and the holder
  stub, `/shirabe:release` puts no question for a minor-bump range, a
  patch-bump range, `/shirabe:release 0.3.0` and `/shirabe:release 1.0.0`
  over a major-bump range, and prints the version, the notes, the
  unattributed commits and `release go: from the grant of dispatch <id>`
  with the stub's own dispatch id; the stub's log shows exactly one `check`
  call before the draft.
- [ ] Under the holder stub the go is still asked for a first release, a
  recommended major bump with no version given, a range whose security
  check printed `UNRESOLVED`, and a range holding an unattributed commit the
  release workflow didn't write; and, still under the holder stub, a range
  holding a `security`-labelled pull request gets the treatment question, a
  `release.local.md` check gets the run-it question, and a declared check
  exiting 5 with a `confirm:` line gets the confirmation question.
- [ ] With every `go=person` stub above, the go is asked exactly as today.
- [ ] `/shirabe:release '1.2.3;id'` and `/shirabe:release 1.2` stop before
  Phase 2's declared checks with a message naming the version, for a holder
  and a non-holder alike.
- [ ] Under the holder stub, an open `blocks-release` or `priority:critical`
  issue, a failing CI run, a dirty tree, an existing tag or draft, and a
  failing declared check each stop the run before the draft, naming the
  check.
- [ ] When the stubbed gate denies the draft, denies the dispatch, or asks
  for the dispatch and the answer is no, and when the go is declined or
  nobody can answer it, the run stops, closes its session `abandoned`,
  prints the step and the reason, and makes no call to a dispatches
  endpoint.

**Check**: Run `bash skills/release/scripts/release-grant_test.sh` and the
release skill's evals; the holder case prints `go=grant dispatch=` followed
by the stub's id, the 30-second stub case prints `go=person
reason=lookup-failed` within 4 seconds, and the holder eval run records no
question before the draft.

**Dependencies**: None

**Files**: `skills/release/scripts/release-grant.sh`, `skills/release/scripts/release-grant_test.sh`, `skills/release/SKILL.md`, `skills/release/evals/evals.json`

### Step 3: /shirabe:release runs the declared installs after its own release is published

`after-release.sh` waits for this run's release to be published, re-reads the grant, runs the committed `## After release` commands in order (holder unasked, anyone else asked once, local ones asked each), stops at the first failure, reports each, and shirabe declares its own installs; then a shirabe release ships steps 2 and 3.

### Step 4: koto's release declares its host installs

koto gains `.claude/shirabe-extensions/release.md` whose `## After release` reinstalls koto with tsuku at the released version, checks `koto version`, then runs `niwa apply` from the workspace root.

### Step 5: niwa's release declares its host installs and its guide says what cut-release covers

niwa gains a release extension that reinstalls niwa, checks `niwa version` and runs `niwa apply` with the new binary, and its authority guide covers the release workflow dispatch, the installs after a cut, and the release routes a text gate doesn't read.

### Step 6: tsuku's release declares its self-update

tsuku gains a release extension whose `## After release` updates tsuku to the released version and checks `tsuku version`.

### Step 7: the workspace grants cut-release after a live check in a throwaway repository

After the live check in a disposable workspace and throwaway repository passes, dot-niwa's ceiling and `coordinator` role gain `cut-release`, the workspace.toml comment is rewritten, the pull request records the check, and the running release seat is re-dispatched.

## Implementation Sequence

Steps 1 and 2 can run side by side, in dot-niwa and shirabe. Step 3
follows step 2 in shirabe and ends with a shirabe release, which this
workspace still cuts with the owner's go. Steps 4, 5 and 6 follow step 3,
since their extensions are read by the released skill, and can run side
by side. Step 7 runs last: it needs steps 1 to 6 merged, the gate on
dot-niwa's main, the shirabe release out, and the live check passed.

Notes to carry into execution:

- Until step 7 merges no session in the tsukumogami workspace holds
  `cut-release`, so steps 1 to 6 only add questions.
- Step 1 asks the owner's own sessions before a release workflow dispatch,
  which they make unasked today. The owner confirms that row (the PRD's
  decision on undispatched sessions) before step 1's pull request merges,
  and the pull request records the answer; if the owner reverses it, the
  row gives today's allow and nothing else in step 1 changes.
- Step 1 can land its stricter reading of existing `gh` commands (`ghsub()`
  and `ghmethod()`) as its own commit before the release rule, inside the
  same pull request, to keep the review readable.
- Step 5's guide edit has no ordering tie to steps 3 and 4 and may land
  earlier; it must be merged before step 7.
- The live check needs real dispatches and a real release workflow, so it
  can't run on a stub. Run it by hand against the step 3 shirabe release, in
  a disposable workspace whose ceiling and `coordinator` role hold all three
  authorities, against a throwaway repository whose `prepare-release.yml`
  calls shirabe's reusable release workflow at a pinned commit, with a
  repository-scoped, short-lived credential; delete the repository and the
  credential afterwards, pass or fail.
- In steps 4 to 6, try each install command once by hand on a release that
  is already published, including whether tsuku wants a `v` before the
  version.
