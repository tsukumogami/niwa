---
schema: prd/v1
status: In Progress
problem: |
  A release seat niwa dispatches can hold merging and closing from its grant
  but not releasing. The tsukumogami workspace grants no `cut-release`, the
  `gate-online` hook has no rule for the "Prepare Release" workflow dispatch
  that `/shirabe:release` actually cuts with, and the skill asks a person for
  the version-and-notes go on every run, so the owner approves every cut and
  every host install after it.
goals: |
  The owner grants release authority once, through niwa, the same way merge
  and close are granted. A seat holding `cut-release` runs `/shirabe:release`
  from version choice to published release, then updates the host, with no
  person asked on an ordinary cut. Every other session is asked before a
  release however it is started, and the gate names the authority it lacks.
  The change is proved in a throwaway repository before any real release
  relies on it.
motivating_context: |
  The owner directed that release permission be granted through niwa the same
  way merge and close already are, reversing the earlier ruling that kept
  `cut-release` out of the tsukumogami workspace, and ruled that an approved
  cut carries the installs that follow it on the host.
absorbed:
  - docs/briefs/BRIEF-release-honours-grant.md
---

# PRD: release-honours-grant

## Status

In Progress

Absorbed [BRIEF-release-honours-grant](docs/briefs/BRIEF-release-honours-grant.md); carried in Absorbed Brief.

## Absorbed Brief

A session niwa dispatches to cut releases can be granted merging and
closing but not releasing. The workspace never grants `cut-release`, the
gate watches `gh release create` (the draft) and not the "Prepare Release"
workflow dispatch that actually cuts, and `/shirabe:release` asks a person
for the version-and-notes go whatever the session holds. The installs that
bring the host up to a new release wait on a go of their own.

What should be true instead is that the owner grants release authority
once, through niwa, as merge and close are granted. A release seat holding
`cut-release` runs `/shirabe:release` from version choice to a published
release and an updated host with nobody asked, and the owner reads the
result afterwards. A session without the grant, and the owner's own session
niwa never dispatched, are asked before a release however it is started,
told which authority they lack; a seat that never held `cut-release` can't
pass it on; and before any real release relies on it, the whole path is
proved against a throwaway repository and only then does the workspace
grant `cut-release`.

The feature holds in the workspace's ceiling and `coordinator` role, the
gate's treatment of a release workflow dispatch, the skill reading the
grant for its go, the installs that follow a cut and are tied to it, niwa's
guide, and the rollout with its live check. It pushes out other release
routes (hand-pushed tags, `gh release edit`, the REST releases endpoint),
the confirmations that aren't the go, a separate release role, gating
installs in general, changes to niwa's grant model, and stronger
enforcement, each set out in Out of Scope below.

## Problem Statement

niwa writes a grant into the record of every session it dispatches, and the
workspace's `gate-online` PreToolUse hook decides merges, issue closes and
releases for a session bound to its dispatch by that grant (niwa's authority
guide, "A gate on the binding"). Merging and closing work end to end: a
coordinator dispatched with the `coordinator` role holds `merge-pr` and
`close-issue` and lands its work without asking anyone. Releasing doesn't,
and the owner still approves every cut by hand. Three things keep it that
way.

1. **Nobody can hold `cut-release` in this workspace.** The tsukumogami
   workspace's grant ceiling and its `coordinator` role name `merge-pr` and
   `close-issue` only, on an earlier ruling that a release stays a person's
   call. A dispatch asking for `cut-release` is refused before launch.
2. **The gate watches the half of a cut that doesn't publish.** The hook
   treats `gh release create` as a release. `/shirabe:release` does run that
   command, but only to create a draft; the release is cut when the skill
   dispatches the repository's "Prepare Release" workflow, which stamps the
   version, pushes the tag and lets the build and finalize workflows publish.
   The hook has no rule for a workflow dispatch, so today any session, granted
   or not, can dispatch the release workflow without being asked, by the
   skill or by typing `gh workflow run prepare-release.yml` itself.
3. **The skill asks whatever the session holds.** `/shirabe:release` stops
   for a person to confirm the version and the notes on every run, so even a
   seat the owner has trusted to release would interrupt the owner once per
   cut.

After a cut the host is brought up to the new version: the released tool is
reinstalled with tsuku, and the workspace is re-applied with niwa, which also
moves its Claude plugins to the new release. Those installs wait for a go of
their own, so an approved cut leaves the host on the old version until
someone says so again. Nothing ties an install to the release it follows.

This matters now because the owner has directed that release permission be
granted through niwa like merge and close, so that a release seat stops
asking for every cut.

## Goals

- The owner grants release authority once, at dispatch, and is no longer
  asked per cut or per install that follows it.
- A seat holding `cut-release` runs `/shirabe:release` end to end on an
  ordinary cut (an earlier release exists, no blocker, no major bump, no
  security-labelled change, no local command), from version choice to a
  published release and an updated host, with no person asked.
- A session without the grant, and the owner's own undispatched session, are
  asked before a release whichever way it is started, and the question says
  which authority was missing. Nothing widens for them.
- Release authority can be passed on only by a seat that holds it.
- The gate and niwa's part run with no koto, and `/shirabe:release` keeps
  asking a person on a host with no niwa.
- The change is proved in a throwaway repository before the workspace grants
  `cut-release` for real.

## User Stories

- As the **owner**, I want to grant release authority once through niwa, so
  that a release seat cuts and installs without asking me each time, and I
  read what it did afterwards.
- As a **release seat** dispatched with the `coordinator` role holding
  `cut-release`, I want `/shirabe:release` to take its recommended version and
  its own notes, create the draft, dispatch the release workflow and update
  the host without stopping, so that a cut finishes while nobody is watching.
- As a **worker dispatched without `cut-release`**, or **the owner working in
  a session niwa didn't dispatch**, I want to be asked before a release
  whether I use the skill or dispatch the workflow by hand, with the gate
  saying I hold no `cut-release`, so that whoever answers knows why.
- As a **release seat handing its seat on**, I want to dispatch my successor
  with `cut-release`, and I want a seat that never held it to be refused when
  it asks for it for a child, so that release authority only flows from the
  owner's grant.
- As a **maintainer of a repository released with `/shirabe:release`**, I
  want to declare in my repository what the host installs after a release
  of it, so that the installs are reviewed with my code and run only after
  my release is published.
- As the **owner rolling this out**, I want the whole path proved against a
  throwaway repository before the workspace grants `cut-release`, so that no
  real release is the first test of it.

## Terms

| Term | Meaning |
|------|---------|
| case table | dot-niwa's `tests/gate-online.test.sh`, the hook's test table |
| the `179f588` hook | `gate-online.sh` as merged in dot-niwa commit `179f588`, vendored into the case table as a fixture to compare against |
| bound, holder | `niwa authority check` answers `bound` for the session, its grant names `cut-release`, and the `cut-release` point reads decider `session` with author exclusion `off` |
| the go | `/shirabe:release`'s question confirming the version and the notes before the draft is created |
| an expansion | a word the hook's scanner marks as computed: holding `$`, `$(...)` or a backtick |
| no person can answer | the run's question gets no answer because nothing is attached to the session to put it to, as in an unattended dispatched seat |

## Requirements

### Workspace configuration (dot-niwa)

- **R1. The workspace grants releasing.** The tsukumogami workspace's
  `[authority]` ceiling and its `coordinator` role each hold `merge-pr`,
  `close-issue` and `cut-release`, in that order. The comment above the table
  states that the grant decides releases for a dispatched session, and no
  longer says that `cut-release` stays out as a person's call.
- **R2. The widening lands last.** R1 is merged only after R3 to R21 have
  merged, the gate, skill and extension changes are in released versions
  (dot-niwa's hook on main, a shirabe release carrying R11 to R19, and each
  repository's extension on main), and the live check (R24) has passed. Its
  pull request records the live check's result.

### The gate (dot-niwa)

- **R3. A release workflow dispatch is a release.** `gate-online.sh` treats
  each of these as a command needing `cut-release`:
  - `gh workflow run` whose selector (its first argument that isn't a flag or
    the value of `-r`/`--ref`, `-R`/`--repo`, `-f`/`--raw-field` or
    `-F`/`--field`; `--json` takes no value) names a release workflow (R4),
    whatever other flags it carries and in whichever order;
  - `gh api` whose path addresses `actions/workflows/<selector>/dispatches`
    under `repos/<owner>/<repo>/` (literal or `{owner}`/`{repo}` placeholders,
    with or without a leading slash, followed by the end of the word, `/`,
    `?` or `#`), with the selector naming a release workflow, unless the
    method is GET. The method is the last `-X`/`--method` given, in any
    spelling the hook reads (`-X GET`, `-XGET`, `--method GET`,
    `--method=GET`, any case); only that explicit GET exempts the path, and
    a call with no method flag is read as a write. The same reading applies
    to the merge endpoint rule.
  The hook finds a `gh` command's subcommand words by skipping flags between
  them, and the value of `-R`/`--repo`, so `gh workflow -R o/r run`, `gh
  release -R o/r create` and `gh pr -R o/r merge` are read as the commands
  they are, for this rule and for the hook's existing ones.
- **R4. Release workflow names.** A selector names a release workflow when,
  compared case-insensitively, it equals `prepare-release.yml`,
  `prepare-release.yaml`, `release-prepare.yml`, `release-prepare.yaml` or
  `Prepare Release`. These are the names every repository in this workspace
  uses for the workflow that calls shirabe's reusable release workflow; a
  repository that adds a release workflow under another name adds the name
  to this list in dot-niwa.
- **R5. An unreadable selector asks.** A `gh workflow run` with no selector,
  with more than one positional word, or with a selector that is digits only,
  an expansion, or holds any of `* ? [ ] { } ~ \ % /`, an R3 `gh api` path
  whose selector is digits only, an expansion or holds one of those
  characters, and a `gh api` call whose endpoint is wholly an expansion with
  a non-GET method flag or a body flag, get ask for every session, and no
  grant or exemption lifts it.
- **R6. Other workflows are unchanged.** A dispatch whose selector is a
  readable name outside R4 gets the `179f588` hook's verdict. Text that only
  mentions `gh workflow run` (a comment, a quoted string, a commit message, a
  heredoc body, an `echo` argument) isn't a dispatch.
- **R7. The verdict for a release dispatch.** By the `niwa authority check`
  answer for the session with the point `cut-release`, taking the first row
  that matches:

  | Session | Release workflow dispatch |
  |---------|---------------------------|
  | bound, lacks `cut-release` | ask, reason naming what it holds and `needs cut-release` |
  | bound, holds it, decider `dispatcher` | deny, reason naming the level and `settled by dispatcher` |
  | bound, holds it, decider `person` | ask, reason naming the level and `settled by person` |
  | bound, holds it, author exclusion `on` | ask, reason naming `author exclusion` |
  | bound, holds it, decider `session`, author exclusion `off` | allow |
  | `record-unusable` | ask |
  | `not-dispatched`, a failed lookup, or an answer outside niwa's vocabularies | ask |

  The exemption list doesn't change any row. The last row is a change for
  sessions niwa didn't dispatch: they dispatch the release workflow unasked
  today.
- **R8. The draft is unchanged.** `gh release create`, with or without
  `--draft`, keeps the `179f588` hook's verdicts.
- **R9. Never more permissive.** Over every session state, exemption setting
  and command in the case table, the new hook's verdict is never more
  permissive than the `179f588` hook's, except that a holder is allowed a
  release workflow dispatch; the case table marks each row where the new
  hook is stricter.
- **R10. One lookup, only when needed.** A command needing any authority
  makes exactly one `niwa authority check` call per hook run, carrying one
  `--point` per authority it needs, each once, under the hook's existing
  bound (3 seconds, the whole hook within 5); a command needing none makes
  no call.

### The release skill (shirabe)

- **R11. The skill reads the grant.** Before the go, `/shirabe:release`
  asks niwa whether its own session is a holder, with `niwa authority check
  --json --session "$CLAUDE_CODE_SESSION_ID" -C <its instance root> --point
  cut-release`, finding niwa on `PATH` and then in `$NIWA_BIN`, bounded at 3
  seconds. The instance root is the nearest directory at or above the
  repository holding `.niwa/instance.json`. The answer counts only when it
  is exactly one `niwa.authority.check/v1` document with every state,
  authority, level, decider and author-exclusion value in niwa's
  vocabularies and a `dispatch_id` that is a lowercase UUID. The niwa it runs
  must not lie under the repository's working tree. The check prints one
  line: `go=grant dispatch=<dispatch id>` for a holder, or `go=person
  reason=<why>` otherwise. The check is advisory: the session id and the
  working directory are the session's own to change, so the gate, reading
  the payload's session and its own path, stays the control on every step.
- **R12. The grant answers the go.** A holder skips the go: it takes the
  version given in the invocation, or else the version its commit analysis
  recommends when that is a minor or patch bump, and the notes it drafted,
  once the version matches `^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`. It
  still prints the notes, the unattributed commits and the version, followed
  by the line `release go: from the grant of dispatch <dispatch id>`.
- **R13. The grant answers nothing else.** These still ask a person,
  whatever the grant: the version of a first release (no earlier `v*` tag);
  a recommended major bump with no version given in the invocation; how a
  security-labelled pull request is described in the notes; running a check,
  asset or install command from the untracked `release.local.md`; a
  declared check that exits 5 asking for a confirmation; a range whose
  security check printed an `UNRESOLVED` line; and a range holding an
  unattributed commit other than the release workflow's own version commits
  (a direct push nobody reviewed as a pull request). A version that fails
  R12's pattern stops the run for everyone.
- **R14. Blockers stop a holder.** Every precondition check (clean tree, CI
  green, no existing tag or draft, no open `blocks-release` or
  `priority:critical` issue, a declared check failing) stops a holder's run
  exactly as it stops anyone's, and the run reports which check stopped it.
- **R15. Anyone else is asked as today.** With no niwa, a niwa whose
  `authority` command lacks `check`, no `CLAUDE_CODE_SESSION_ID`, no instance
  root, a lookup that fails, runs past the bound or prints anything R11
  doesn't count, or any answer other than a holder's, the check prints
  `go=person` and the skill asks the go exactly as it does today, making no
  niwa call when there is no session id or no instance root.
- **R16. A refusal is reported, not routed around.** When the gate denies
  the draft or the workflow dispatch, or asks and the person declines or no
  person can answer, and when a person declines or can't answer the go, the
  skill stops, closes its session `abandoned`, and reports the step and the
  gate's reason or the declined question. It never reaches a refused step
  another way, such as the REST form of a refused `gh workflow run`.

### The installs after a cut

- **R17. Installs are declared with the release.** A release extension's
  `## After release` heading lists commands, one per bullet, the command in
  the bullet's first backtick span; a bullet with no backtick span is skipped
  with a warning naming it. Commands run in order from the repository root
  with `RELEASE_VERSION` set to the released version without a leading `v`.
  koto, shirabe, niwa and tsuku each declare, in a tracked
  `.claude/shirabe-extensions/release.md`, the host installs their release
  calls for, in this order: reinstalling the released tool with tsuku at
  `RELEASE_VERSION`; a command that exits non-zero unless the installed tool
  reports `RELEASE_VERSION`; and, for koto, shirabe and niwa, re-applying
  every instance of the workspace from its root with `niwa apply`. On a
  holder's run the commands are read from the extension as committed at the
  commit the release was cut from, not from the working tree.
- **R18. Installs run only after their own cut is published.** In the run
  that dispatched the release workflow, after its monitoring sees the
  dispatched run conclude `success`, the
  skill waits up to 30 minutes, polling every 30 seconds, for `gh release
  view v<version>` to report `isDraft: false`. Only then does it run the
  `## After release` commands. It runs none under `--dry-run`, after a failed
  workflow, when the release isn't reported published within the wait, when
  `gh release view` fails, or for a version the run didn't dispatch. When the
  wait runs out it says the release isn't published yet and names each
  install it didn't run.
- **R19. The grant answers the installs.** Before the installs the skill
  reads the grant again (R11). A holder runs the tracked commands without
  asking. Any other session is asked once, listing the tracked commands; on
  a yes it runs them, and on a no, or when no person can answer, it runs none
  and reports each command it didn't run. A command from `release.local.md`
  is asked about one at a time for every session and skipped on a no. A
  command that exits non-zero is reported with its command and the last 20
  lines of its output, and the commands after it don't run, so `niwa apply`
  never runs after a failed reinstall or version check; the release stays
  published.
  The run's final report lists each install command with how it ended (ran
  and passed, failed, not run, declined).
- **R20. Installs outside a release keep today's rules.** The gate gets no
  rule for `tsuku`, `niwa apply` or `claude plugin` commands, and nothing in
  this work lets a holder install outside an R18 run.

### niwa

- **R21. The guide says what `cut-release` covers.** niwa's authority guide
  states that `cut-release` covers dispatching a repository's release
  workflow as well as creating a release, and the host installs a release run
  makes after its own cut; and its "A gate on the binding" section says a
  gate treats a release workflow dispatch as a release. It says that a
  text-reading gate covers only the commands it reads, naming the release
  routes it doesn't (a tag push, `gh release edit`, the REST releases
  endpoint, a script), so the table doesn't read as complete coverage. No
  niwa code changes.
- **R22. Release authority flows only from a holder.** A seat holding
  `cut-release` can dispatch a child granted `cut-release`, and a seat
  without it is refused before any instance exists. This is niwa's existing
  never-wider check, which this work relies on and doesn't change; the live
  check proves it with `cut-release`.

### Rollout

- **R23. Nothing widens before R1.** The gate change, the skill change and
  the declared installs land and are released while the tsukumogami
  ceiling still lacks `cut-release`, so no session there is a holder and
  each change only adds questions until R1 merges.
- **R24. Live check before the widening.** In a disposable workspace whose
  ceiling and `coordinator` role hold all three authorities and which runs
  the R3 gate, against a throwaway repository whose `prepare-release.yml`
  calls shirabe's reusable release workflow and whose release extension's
  `## After release` command writes `RELEASE_VERSION` to a marker file, the
  "Live check" acceptance criteria pass, and their output is recorded in R1's
  pull request.
- **R25. Running seats pick it up by re-dispatch.** A record is never
  rewritten, so a seat dispatched before R1 merged holds no `cut-release`.
  Once R1 is merged, the coordinator that dispatched the running release seat
  (or the owner, for a seat the owner dispatched) re-dispatches it with
  `--role coordinator`.

### Across the work

- **R26. No koto in the gate or niwa.** The gate and niwa's part need no
  koto; this is what "passes with niwa and no koto" means for this work,
  since `/shirabe:release` already requires koto (R27). The case table runs with neither niwa nor koto installed, against a
  stub niwa, on the Linux and macOS runners.
- **R27. shirabe doesn't require niwa.** `/shirabe:release` keeps its
  existing koto requirement, and its grant check (R11) needs neither koto nor
  niwa to run: without niwa it prints `go=person`.

## Acceptance Criteria

Gate rows run the hook as niwa installs it into an instance, with a payload
carrying the session id, in the case table against a stub niwa, with
`GATE_MERGE_EXEMPT_INSTANCE` unset unless a row says otherwise. "Holder
stub" means a stub answering bound with `cut-release`, decider `session`,
author exclusion `off`, at `autonomous`.

**The gate (R3 to R10, R26)**

- [ ] With the holder stub, each of these gets allow: `gh workflow run
  prepare-release.yml -f version=1.2.3 -f tag=v1.2.3 -f ref=main`, `gh workflow
  run --ref main prepare-release.yml`, `gh workflow run -f version=1.2.3
  prepare-release.yml`, `gh workflow run --json prepare-release.yml`, `gh
  workflow run release-prepare.yml -R o/r`, `gh workflow run prepare-release.yaml`, `gh workflow run
  release-prepare.yaml`, `gh workflow run "Prepare Release"`, `gh workflow
  run PREPARE-RELEASE.YML`, `gh api -X POST
  repos/o/r/actions/workflows/prepare-release.yml/dispatches -f ref=main`,
  `gh api --method=POST repos/o/r/actions/workflows/prepare-release.yml/dispatches`,
  `gh api repos/o/r/actions/workflows/prepare-release.yml/dispatches -f
  ref=main` (an implied POST), `gh api
  /repos/{owner}/{repo}/actions/workflows/release-prepare.yml/dispatches -f
  ref=main`, and `gh api --input body.json
  repos/o/r/actions/workflows/prepare-release.yml/dispatches`.
- [ ] With a stub answering bound holding `merge-pr` and `close-issue` only,
  `gh workflow run prepare-release.yml` gets ask with a reason containing
  `holds merge-pr,close-issue` and `needs cut-release`, in an instance on the
  exemption list and in one off it.
- [ ] Holding `cut-release` with decider `dispatcher` at `supervised`, the
  same command is denied with a reason containing `level supervised` and
  `settled by dispatcher`; with decider `person` at `strict`, it gets ask with
  `level strict` and `settled by person`; with author exclusion `on`, it gets
  ask with `author exclusion`; with decider `dispatcher` and author exclusion
  `on` together, it is denied with `settled by dispatcher`.
- [ ] With the stub answering `record-unusable`, answering `not-dispatched`,
  exiting 1, printing an unknown schema, printing two documents, printing a
  decider outside niwa's vocabulary, and with no niwa on `PATH` or in
  `NIWA_BIN`, `gh workflow run prepare-release.yml` gets ask, in an instance
  on the exemption list and in one off it.
- [ ] With the holder stub and in an instance on the exemption list, each of
  these gets ask: `gh workflow run 123456`, `gh workflow run`, `gh workflow
  run "$W"`, `gh workflow run "$(pick)"`, `gh api -X POST
  repos/o/r/actions/workflows/123456/dispatches`, and `gh api -X POST
  "repos/o/r/actions/workflows/$W/dispatches"`.
- [ ] With every stub above, each of these gets allow: `gh workflow run
  ci.yml`, `gh workflow run "Build and Test"`, `gh workflow run
  prepare-releases.yml`, `gh api -X GET
  repos/o/r/actions/workflows/prepare-release.yml/dispatches`, `gh api -XGET
  repos/o/r/actions/workflows/prepare-release.yml/dispatches`, `gh api
  --method get repos/o/r/actions/workflows/prepare-release.yml/dispatches`,
  `gh api -X get repos/o/r/actions/workflows/prepare-release.yml/dispatches`,
  `echo "gh workflow run prepare-release.yml"`, `git commit -m "gh workflow
  run prepare-release.yml"`, a comment `# gh workflow run
  prepare-release.yml`, and a `cat <<'EOF'` heredoc whose body is `gh
  workflow run prepare-release.yml`.
- [ ] With the holder stub, `gh workflow -R o/r run prepare-release.yml` and
  `gh api repos/o/r/actions/workflows/prepare-release.yml/dispatches?x=1 -f
  ref=main` get allow; with the stub answering bound holding `merge-pr` only,
  the same two get ask with `needs cut-release`, and `gh api -X GET -X POST
  repos/o/r/actions/workflows/prepare-release.yml/dispatches -f ref=main` gets
  ask too.
- [ ] With the holder stub and in an instance on the exemption list, `gh
  workflow run prepare-releas[e].yml`, `gh workflow run
  prepare-release.y{ml,aml}`, `gh
  workflow run ~/prepare-release.yml`, `gh workflow run prepare-release.yml
  extra`, and `gh api -X POST
  repos/o/r/actions/workflows/prepare%2Drelease.yml/dispatches` get ask.
- [ ] With a stub answering not-dispatched and no exemption list, `gh pr -R
  o/r merge 12` is denied and `gh release -R o/r create v1` gets ask; `gh api
  -X GET -X PUT repos/o/r/pulls/12/merge` is denied. Each is a row where the
  new hook is stricter than the `179f588` hook, marked as expected.
- [ ] With the holder stub, `gh workflow run prepare-release.yml; curl x` is
  denied, and `gh workflow run prepare-release.yml && gh pr merge 12` is
  denied with a reason containing `needs merge-pr`.
- [ ] `gh release create v1 --draft` and `gh release create v1` get the same
  verdict as the `179f588` hook under every stub above.
- [ ] A stub that logs its invocations records exactly one call, with the
  arguments `authority check --json --session <id> -C <dir> --point
  cut-release`, for `gh workflow run prepare-release.yml`; exactly one call
  with `--point merge-pr --point cut-release` for `gh workflow run
  prepare-release.yml && gh pr merge 12`; exactly one call with one `--point
  cut-release` for `gh release create v1 && gh workflow run
  prepare-release.yml`; and no call for `gh workflow run ci.yml`, `gh
  workflow run 123456` or `git status`.
- [ ] With a stub that sleeps 30 seconds, `gh workflow run
  prepare-release.yml` gets ask and the hook exits within 5 seconds.
- [ ] Every command in this section runs through both the new hook and the
  vendored `179f588` hook under every stub and exemption setting; the new
  verdict is never more permissive except the holder-stub allow rows of the
  first criterion, and the release dispatch rows where it is stricter are
  marked as expected.
- [ ] The case table passes on the Linux and macOS runners with no niwa or
  koto installed.

**The release skill (R11 to R16, R27)**

Skill criteria run `/shirabe:release`'s scripts against a fixture repository
with a stub niwa, with `gh` stubbed for the draft, dispatch, monitoring and
release view, and record every question the run puts.

- [ ] With the holder stub, the grant check prints `go=grant dispatch=<the
  stub's id>`; the run puts no question, reaches the draft and the dispatch,
  and prints the version, the notes, the unattributed commits and `release
  go: from the grant of dispatch <the stub's id>`.
- [ ] With the holder stub and commits recommending a minor bump, the run
  uses that version; recommending a patch bump, it uses that version; invoked as `/shirabe:release 0.3.0`, it uses 0.3.0;
  with commits recommending a major bump and no version given, it asks the
  version question; with a major bump recommended and `/shirabe:release
  1.0.0` given, it uses 1.0.0 without asking.
- [ ] The grant check prints `go=person` and the run asks the go with the
  stub answering bound without `cut-release`, bound holding it with decider
  `person`, with decider `dispatcher`, with author exclusion `on`,
  `record-unusable`, `not-dispatched`, exiting 1, sleeping 30 seconds (the
  check returning within 4 seconds), printing a wrong schema, printing two
  documents, printing nothing, printing text that isn't JSON, and printing a
  `state` outside niwa's vocabulary; with a niwa stub whose `authority check`
  exits 2 as an unknown command; and with no niwa on `PATH` or in
  `NIWA_BIN`.
- [ ] With `CLAUDE_CODE_SESSION_ID` unset, and with the fixture repository
  outside any directory holding `.niwa/instance.json`, the grant check prints
  `go=person` and the logging stub records no call.
- [ ] With the holder stub, a repository with no earlier `v*` tag gets the
  version question; a release range holding a `security`-labelled pull
  request gets the treatment question; a `release.local.md` check gets the
  run-it question; and a declared check exiting 5 with a `confirm:` line gets
  the confirmation question.
- [ ] With the holder stub, an open `blocks-release` issue, an open
  `priority:critical` issue, a failing CI run on HEAD, a dirty working tree,
  an existing `v<version>` tag, an existing draft for `v<version>`, and a
  declared check exiting 1 each stop
  the run before the draft, with output naming the check.
- [ ] With the holder stub, when the stubbed gate denies the draft, and when
  it denies the workflow dispatch, the run stops before the next step, closes
  its session `abandoned`, prints the gate's reason, and makes no `gh api`
  call to a dispatches endpoint.
- [ ] Without a holder, when the go is declined, and when no person can
  answer it, the run stops before the draft and closes its session
  `abandoned`. With the holder stub, when the stubbed gate asks for the
  workflow dispatch and the answer is no, the run stops the same way and
  prints the gate's reason.
- [ ] The grant check prints `go=person` when the stub's `dispatch_id` isn't
  a lowercase UUID, and when the only niwa it finds lies under the fixture
  repository's working tree.
- [ ] With the holder stub, a range whose security check prints an
  `UNRESOLVED` line, and a range holding an unattributed commit that the
  release workflow didn't write, each get the go question.
- [ ] With the holder stub, `/shirabe:release '1.2.3;id'` and
  `/shirabe:release 1.2` stop before the draft with a message naming the
  version, and so does the same run without a holder.
- [ ] The grant check runs with no koto on `PATH`.

**The installs (R17 to R20)**

- [ ] koto, shirabe, niwa and tsuku each have a tracked
  `.claude/shirabe-extensions/release.md` whose `## After release` bullets
  reinstall that repository's tool with tsuku at `RELEASE_VERSION`, check
  the installed version, and run `niwa apply` from the workspace root (koto,
  shirabe and niwa only), in that order.
- [ ] With the holder stub and the working copy's `release.md` edited after
  the release commit to add a bullet, Phase 7 runs the committed bullets and
  not the added one.
- [ ] With the holder stub, the dispatch succeeding and `gh release view`
  reporting `isDraft: true` twice then `isDraft: false`, each tracked command
  runs once, in order, from the repository root, with `RELEASE_VERSION` set
  to `0.3.0` for `v0.3.0`, and no question is put.
- [ ] An extension with three bullets runs three commands in order; a bullet
  without a backtick span is skipped with a warning naming it; an extension
  with no `## After release` heading runs nothing and prints nothing about
  installs.
- [ ] With the stub answering bound without `cut-release` at install time,
  the run asks once, listing the commands; on a yes each runs, and on a no
  none runs and the output names each command not run. With no person able
  to answer, none runs.
- [ ] With the holder stub at the go and a stub answering bound without
  `cut-release` before the installs, the run asks before the installs.
- [ ] No `## After release` command runs under `--dry-run`, after a failed
  workflow, when `gh release view` keeps reporting `isDraft: true` past the
  wait (with the wait shortened for the test), when `gh release view` fails,
  or when the run's dispatch was refused.
- [ ] With the holder stub, a `## After release` command from
  `release.local.md` gets a question naming it; without a holder, two such
  commands get one question each, and the one answered no doesn't run.
- [ ] When the wait for publication runs out, the output says the release
  isn't published yet and names each install not run; the final report of
  every install run lists each command with how it ended.
- [ ] A tracked command that exits 1 is reported with its command and its
  last output lines, the commands after it don't run (a failed reinstall is
  followed by no `niwa apply`), the final report lists them as not run, and
  no `gh release` call deletes or edits the release.
- [ ] `tsuku update koto`, `tsuku install tsukumogami/koto`, `niwa apply` and
  `claude plugin marketplace update shirabe` get the `179f588` hook's verdict
  under every gate stub.

**niwa (R21, R22)**

- [ ] niwa's authority guide's authority table lists `cut-release` as
  covering a release workflow dispatch and the installs a release run makes
  after its own cut, and its "A gate on the binding" section says a release
  workflow dispatch is decided as a release.
- [ ] The niwa change touches only `docs/`, and niwa's Test job passes.

**Live check (R2, R22, R24)**

Run in the disposable workspace and throwaway repository R24 describes,
with real niwa dispatches and no person attached to the dispatched seats.

- [ ] A seat dispatched with `--role coordinator` (holding all three) runs
  `/shirabe:release` on the throwaway repository; the repository then has a
  published release `v<version>`, the marker file holds that version, and the
  seat's transcript holds no question to a person and no gate decision other
  than allow.
- [ ] A seat dispatched with `--grant merge-pr` runs `/shirabe:release` and
  its transcript shows the go asked; its `gh workflow run prepare-release.yml`
  gets ask with a reason containing `needs cut-release`.
- [ ] A session started by hand in the disposable workspace is asked the go,
  and asked before `gh workflow run prepare-release.yml`.
- [ ] From inside the holder's instance, `niwa dispatch --grant cut-release`
  launches a child; from inside the `--grant merge-pr` seat's instance, the
  same dispatch exits non-zero before any instance exists, with a message
  containing `is wider than the dispatcher's grant:`.
- [ ] On a host with niwa and no koto on `PATH`, `gh workflow run
  prepare-release.yml` gets allow for the holder seat and ask for the
  `--grant merge-pr` seat.

**Workspace configuration and rollout (R1, R2, R23, R25)**

- [ ] Before R1 merges, with the gate and skill changes released, `niwa
  authority policy` in the tsukumogami workspace shows no `cut-release`, and
  a seat dispatched there with `--role coordinator` gets ask for `gh workflow
  run prepare-release.yml` and is asked the go.
- [ ] After R1 merges, `niwa authority policy` there shows a grant ceiling
  of `merge-pr`, `close-issue` and `cut-release` and a `coordinator` role
  holding all three, and the workspace.toml comment above the table no
  longer says a release stays a person's call.
- [ ] R1's pull request body records the live-check results, and every pull
  request R2 names merged before it was opened.
- [ ] A seat dispatched before R1 merged still reads no `cut-release` in
  `niwa authority check` for its own session; the same seat re-dispatched
  with `--role coordinator` after R1 reads `cut-release`.

## Out of Scope

- **Other ways to release.** Pushing a tag by hand, publishing a draft with
  `gh release edit --draft=false`, creating or editing a release through the
  REST releases endpoint, and a script that calls the API itself aren't gated
  today and stay ungated. This work closes the one route `/shirabe:release`
  uses.
- **Confirmations that aren't the go.** How a security fix is described, a
  repository check that asks a person to confirm something specific, a first
  release's version, a major bump nobody named, and an untracked local
  command stay a person's call (R13).
- **A separate release role or a narrower release grant.** The owner directed
  that the `coordinator` role carry `cut-release`.
- **Gating installs.** The gate gets no rule for `tsuku`, `niwa apply` or
  `claude plugin`. An install outside a release run keeps whatever rule
  governs it today, usually the session's brief.
- **Changes to niwa's grant model, record or lookup.** `cut-release`,
  `niwa authority check` and the never-wider refusal already exist.
- **`/coordinate`'s posture read.** It already takes release from a bound
  coordinator's own grant; reading the new hook rule's text for an unbound
  coordinator is left as it is.
- **Stronger enforcement.** The gate stays text inspection, and a session
  that edits its own record or hides a dispatch behind a script, a `gh`
  alias or another language isn't stopped by it. The boundary that would
  hold against such a session is on GitHub's side, such as a protected
  environment with required reviewers on each release workflow; that is
  separate work.

## Known Limitations

- The hook knows release workflows by name (R4). A repository that names its
  release workflow differently isn't gated until the list gains its name,
  and a non-release workflow that happens to carry one of these names is
  gated as a release.
- The gate is friction against a mistake or an injected instruction, not a
  boundary against a session set on getting through: a script file, a `gh`
  alias, another language, a tag push or `gh release edit` reach a release
  without a command it reads. Every holder, which after R1 means every
  coordinator, can release any repository its token can, for as long as its
  session lives.
- With the grant, release notes are published without a person reading
  them, so a commit subject or pull request title that shouldn't be public
  goes out unless an earlier review caught it.
- The undispatched owner session gains one question per release: the go,
  then the draft (as today), then the new ask on the workflow dispatch.
- `niwa apply` from the workspace root re-applies every instance, including
  ones other sessions are working in, as it does when the owner runs it.
- A release whose build takes longer than the 30-minute wait is published
  without its installs; the run says so and the installs are run by hand.
- A seat dispatched before R1 merges holds no `cut-release` until it is
  re-dispatched (R25).

## Decisions and Trade-offs

- **The grant covers the installs that follow a cut.** The owner decided
  that every approved release also approves the installs after it on the
  host. The work ties them to the cut rather than to the holder: they run in
  the release run, after its own release is verified published (R18), so a
  holder isn't waved through for an install that follows no release.
  Alternative: gate `tsuku`, `niwa apply` and `claude plugin` commands by
  `cut-release`; rejected because that would let a holder install at any
  time and would ask every other session before routine commands it runs
  today.
- **Installs are declared by each released repository.** The repository
  knows what its release changes on a host, and its tracked extension is
  reviewed with its code. Alternative: one workspace-wide list in dot-niwa,
  delivered as each repository's `release.local.md`; rejected because the
  skill asks before every untracked command, which would put a question back
  in front of every holder.
- **The `coordinator` role carries `cut-release`.** The owner directed that
  release be granted alongside merge and close, so the role every
  coordinator is dispatched with grants it. This reverses the earlier ruling
  that kept `cut-release` out of this workspace. Alternative: a separate
  `release` role; not taken, on the owner's direction.
- **Release workflows are recognised by name.** All four repositories name
  the workflow that calls shirabe's reusable release workflow "Prepare
  Release", in two file names. The hook reads command text and nothing else,
  and `-R` can name a repository that isn't on disk, so it can't open the
  workflow file to see what it calls. Alternatives: read the workflow file
  (not possible from the text), or a list the workspace declares through an
  environment variable (one more setting that has to agree across hook
  copies, for a list that hasn't varied).
- **Undispatched sessions are asked before a release workflow dispatch.**
  Today a session niwa didn't dispatch dispatches the release workflow
  unasked, while it is asked for `gh release create`. Treating the dispatch
  as a release for every session (R7's last row) closes that gap and makes a
  direct `gh workflow run prepare-release.yml` a decided act. It tightens
  what the owner's own sessions do, so it goes to the owner with the pull
  request carrying this document; reversing it gives those sessions today's
  allow, changes R7's last row and nothing else, and is decided before the
  gate change lands.
- **The grant answers the version-and-notes go, and only that.** That
  question is the per-cut approval the owner's direction replaces. A first
  release has no analysis to take a version from; a major bump changes what
  users must do; a security treatment is a disclosure call; a local command
  is code nobody reviewed; and a check asking for a confirmation asked for a
  person. Each still asks (R13), and every blocker still stops the run
  (R14). Alternative: let the grant answer every question; rejected because
  those questions aren't about whether to cut.
- **The draft stays under `cut-release` unchanged.** `gh release create`
  already needs the authority, and a holder is already allowed it. A draft
  can be published later by `gh release edit`, so telling `--draft` apart
  would buy nothing that holds.
- **The skill reads the grant the way `/coordinate` does.** One bounded `niwa
  authority check` call for its own session, validated against niwa's
  vocabularies, falling back to asking on anything else. The gate still
  decides each command, so the skill's reading never widens what the gate
  allows.
- **Proved in a throwaway repository first.** Releases need the owner's go
  today, so the first release that relies on the grant is a throwaway one, as
  the gate's earlier live check was, and the workspace grants `cut-release`
  only after it passes.
