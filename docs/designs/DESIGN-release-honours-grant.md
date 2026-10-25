---
schema: design/v1
status: Planned
problem: |
  The tsukumogami workspace can't grant `cut-release`, the `gate-online` hook
  has no rule for the "Prepare Release" workflow dispatch that
  `/shirabe:release` cuts with, the skill asks a person for its
  version-and-notes go whatever the session holds, and the host installs after
  a cut have no place to be declared or tied to the cut. A release seat can't
  release without the owner, and a session without the grant can dispatch the
  release workflow unasked.
decision: |
  `gate-online.sh` gains one branch in its `rules()` function that reads the
  selector of `gh workflow run` and of a `gh api` workflow-dispatches path:
  a release workflow name makes it an ask rule needing `cut-release`, decided
  through the hook's existing grant path; an unreadable selector asks for every
  session; any other name keeps today's verdict. shirabe adds
  `release-grant.sh`, a bounded `niwa authority check` for the skill's own
  session that prints `go=grant` or `go=person`; the skill skips its go on
  `go=grant` for a given, minor or patch version, and a new Phase 7 waits for
  the release to be published, reads the grant again and runs the tracked
  `## After release` commands each repository declares. dot-niwa widens its
  ceiling and `coordinator` role last, after a live check in a throwaway
  repository.
rationale: |
  The hook already treats a release as an ask rule needing `cut-release`, so
  routing the dispatch through the same path gives every session state the
  verdict the gate contract already defines, and an unreadable selector goes
  through the same path that keeps `$GH pr merge` from taking a grant. The
  skill's check follows `/coordinate`'s posture read, so a niwa failure only
  ever costs a question. Installs reuse the release extension's bullet format,
  so each is reviewed in the repository it belongs to and runs only inside the
  run that published its release. Every change before the widening only adds
  questions, so each can land on its own.
upstream: docs/prds/PRD-release-honours-grant.md
decision_provenance: inline-resolved
---

# DESIGN: release-honours-grant

## Status

Planned

## Context and Problem Statement

The PRD (`docs/prds/PRD-release-honours-grant.md`) sets what has to be true:
a seat holding `cut-release` releases and installs with nobody asked on an
ordinary cut, everyone else is asked before any release, and the workspace
grants `cut-release` only after a live check. This design settles how, in
four places.

**The gate.** dot-niwa's `.niwa/hooks/pre_tool_use/gate-online.sh` reads a
Bash command's text with one awk pass. Its `rules()` function maps a simple
command's head and arguments to a verdict (`rd`), a reason (`rr`) and, for a
rule a grant can decide, the authority it needs (`ra`). Rules with an
authority go through `gated()`, which records the need; END prints three
verdicts (today's, the non-exempt one, and the rules no grant covers) and the
needed authorities in niwa's order; the shell part makes one bounded
`niwa authority check` call and decides each need by the table in niwa's
authority guide ("A gate on the binding"). `gh release create` is already an
ask rule needing `cut-release`. Nothing matches `gh workflow run`, and a `gh
api` call is matched only for the merge endpoints.

**The skill.** `/shirabe:release` asks the version-and-notes go in Phase 3
step 6 with AskUserQuestion, creates the draft in Phase 4, dispatches the
workflow it finds through `uses: tsukumogami/shirabe/.github/workflows/release.yml`
in Phase 5, and polls the run for up to 5 minutes in Phase 6. It reads
repository-declared commands from `.claude/shirabe-extensions/release.md`
(tracked) and `release.local.md` (untracked, asked before running) under
`## Release checks` and `## Release assets`. `/coordinate`'s
`posture-read.sh` already makes a bounded, validated `niwa authority check`
call for its own session, which this design copies for the release.

**The installs.** Only shirabe has a release extension today, and none
declares a host install. koto, niwa and shirabe carry their tsuku recipes in
their own repositories, and `niwa apply` run from a workspace root
re-registers each instance's plugin marketplaces at the latest release tag.

**The workspace.** dot-niwa's `workspace.toml` declares `ceiling =
["merge-pr", "close-issue"]` and a `coordinator` role with the same two, with
a comment recording the earlier ruling that a release stays a person's call.

## Decision Drivers

- The gate's verdict for a release dispatch must follow the same contract it
  already follows for `gh release create`, including every failure case, so
  a lookup that fails can never widen anything.
- A selector the hook can't read must never take a grant or the exemption.
- shirabe must work without niwa and must not depend on niwa's files beyond
  the documented `check` command and the instance marker it already reads.
- The installs must be reviewed where the released code is reviewed, and run
  only after their own release is published.
- No change may remove a question before the live check has passed.
- The gate stays a text inspector with one awk pass and at most one niwa call.

## Considered Options

### Decision 1: Recognising a release dispatch in the gate

Resolved inline.

The hook has to find a release dispatch in command text, in both `gh
workflow run` and `gh api` forms, and decide it by the grant.

#### Chosen: a selector branch in `rules()` that reuses the release rule's path

First, the `gh` case in `rules()` stops taking its two subcommand words as
`segw[s, a]` and `segw[s, a + 1]`. A small walk, `ghsub()`, skips words that
start with `-` (and the word after `-R` or `--repo` written without `=`)
before each of the two subcommand words and returns their positions. Every
existing `gh` rule uses it, so `gh pr -R o/r merge 12` and `gh release -R o/r
create v1` are read as a merge and a release, which the `179f588` hook
misses; those rows become stricter, as R9 allows.

`rules()` then gains a case for `workflow run` and extends its `api` case.
For `workflow run`, it walks every word after `run`, skipping a word that
starts with `-` and, for `-r`, `--ref`, `-R`, `--repo`, `-f`, `--raw-field`,
`-F` and `--field` written without `=`, the word after it. It counts the
words left: none, or two or more, is unreadable; exactly one is the
selector. For `api`, the existing walk that finds the merge-endpoint path
also tests each word against
`(^|/)repos/[^/]+/[^/]+/actions/workflows/([^/]+)/dispatches([/?#]|$)` and
takes the captured part as the selector, extracting it with `match()`,
`RSTART`, `RLENGTH` and `substr()` so it runs on the BSD awk on macOS and
on gawk alike. The method is read once for both endpoint rules by
`ghmethod()`: the last `-X`/`--method` in any of its spellings, upper-cased,
or empty when there is none. Only an explicit GET exempts a path; a call
with no method flag is treated as a write, whatever body flags it carries
(`-f`, `-F`, `--raw-field`, `--field`, `--input`, attached or not), so the
merge rule keeps the `179f588` hook's deny for a bare `gh api
.../pulls/12/merge` and switches from its any-GET test to the same
function. A `gh api` call whose endpoint word is wholly an expansion asks
when its last method flag isn't GET or a body flag is present, since its
target can't be read; a plain read through a variable path keeps today's
verdict.

A selector then goes one of three ways:

| Selector | Rule |
|----------|------|
| release name (R4 list, case-insensitive) | `rd = "ask"`, `rr = "release workflow dispatch"`, `ra = "cut-release"`, through `gated()` |
| missing, more than one, digits only, an expansion (`segd` set, or the word holds `$`), or holding any of `* ? [ ] { } ~ \ % /` | `rd = "ask"`, `rr = "workflow dispatch whose workflow can't be read"`, no `ra`, through `verdict()` |
| any other name | no rule |

The character test covers what bash would expand or what GitHub would
decode before matching: a glob or brace that turns `prepare-releas[e].yml`
into a release name, a tilde, a backslash, and a percent-encoded path.

A helper, `release_name(w)`, lower-cases the word and compares it with
`prepare-release.yml`, `prepare-release.yaml`, `release-prepare.yml`,
`release-prepare.yaml` and `prepare release`. Because the release case
carries `ra = "cut-release"`, the END authority list, the jq vocabulary
check, the single lookup and the shell table for a bound session already do
the rest: a holder is allowed, a bound session without the authority is
asked with `holds <list>; needs cut-release`, `dispatcher` denies, `person`
and author exclusion ask, `record-unusable` and a failed lookup take the
non-exempt or today's verdict, which for this rule is ask. The unreadable
case reaches `verdict()`, so its ask sits in the base verdict that no grant
and no exemption lifts, as `$GH pr merge` does today.

The `mentions()` pre-check, which decides whether text handed to a shell or
an unclosed quote is worth an ask, gains the release names
(case-insensitive `prepare-release`, `release-prepare`, `prepare release`)
and `actions/workflows/[^ \t\n]*/dispatches`, so a release dispatch hidden
in `bash -c`, with or without flags between its `gh` words, asks like the
other gated commands, while `bash -c 'gh workflow run ci.yml'` keeps today's
verdict as R6 requires.

#### Alternatives Considered

**Gate every `gh workflow run`.** Rejected: CI and maintenance workflows are
dispatched routinely, and asking before each one would be a cost the PRD's
"other workflows are unchanged" requirement rules out.

**Treat any selector containing `release` as a release workflow.** It fails
closed for a release workflow with a new name, at the cost of a question
before a release-notes lint or similar. Not taken because the PRD settled a
fixed list and the four repositories agree on it; it stays the fallback if
a fifth name appears.

**A separate scan before awk.** Rejected: it would need its own quoting and
heredoc rules and would disagree with the scanner on what is code and what
is data.

**Read the workflow file to see whether it calls shirabe's reusable release
workflow.** Rejected: the hook reads text only, and `-R` can name a
repository that isn't on disk.

### Decision 2: Where the skill reads the grant

Resolved inline.

`/shirabe:release` has to know, at its go and before its installs, whether
its own session is a holder, and has to fall back to asking on anything
else.

#### Chosen: `skills/release/scripts/release-grant.sh`, modelled on `posture-read.sh`'s lookup

The script takes no arguments and prints exactly one line:

- `go=grant dispatch=<dispatch id>` when niwa answers `bound` for
  `$CLAUDE_CODE_SESSION_ID`, the grant names `cut-release`, and the
  `cut-release` point reads decider `session` and author exclusion `off`;
- `go=person reason=<token>` otherwise, with the token one of `no-niwa`,
  `no-session-id`, `no-instance-root`, `lookup-failed`, `unreadable-answer`,
  `not-dispatched`, `record-unusable`, `not-held`, `decider-dispatcher`,
  `decider-person` or `author-exclusion`.

It finds niwa as the gate does (`command -v niwa` with an absolute result,
else an executable absolute `$NIWA_BIN`) and refuses one whose resolved path
lies under the repository's working tree (`no-niwa`), since a committed
settings file can point `PATH` or `NIWA_BIN` there. It finds the instance
root as the nearest directory at or above the repository root (`git
rev-parse --show-toplevel`) holding `.niwa/instance.json`; niwa itself
resolves an outer instance from there. It
runs `niwa authority check --json --session "$CLAUDE_CODE_SESSION_ID" -C
<root> --point cut-release` with stdin closed, writing to a `mktemp` file
removed by a trap on every exit and signal, under `timeout -s KILL 3` or,
without `timeout`, a background job and a KILL watcher, as the gate does. It
validates the answer with the same jq program shape `posture-read.sh` uses
(exactly one document, schema `niwa.authority.check/v1`, every value in
niwa's vocabularies), adds a lowercase-UUID test on `dispatch_id`, and
treats anything else as `unreadable-answer`. It exits 0 in every case, so a
caller reads the line, never the exit code.

The check is advisory by construction. The session id comes from the
environment and the instance from the working directory, both of which the
session controls, so a session could make it print `go=grant` falsely. That
costs only the skill's own questions: the gate reads the payload's session
id and its own installed path, and still decides the draft and the dispatch.

SKILL.md calls it in two places:

- **Phase 3, step 6.** On `go=grant`, when the invocation gave a version or
  the commit analysis recommends a minor or patch bump, Phase 2's security
  check printed no `UNRESOLVED` line, and every unattributed commit in
  `work/unattributed` is one the release workflow wrote (authored and
  committed by the Actions bot identity the reusable workflow commits as,
  with one of the version-stamp or dev-bump subjects it writes, both
  written into the script as fixed strings when it is implemented), the
  skill doesn't ask: it prints the notes, the
  unattributed commits, the version and `release go: from the grant of
  dispatch <id>`, and goes on to Phase 4. On `go=person`, a first release, a
  recommended major bump with no version given, or either range condition,
  it asks exactly as today.
- **Version check.** For every session, a version given in the invocation is
  checked in Phase 1, before Phase 2's declared checks put it on a command
  line, and a version chosen at the go is checked again after the answer.
  The check is a whole-value bash test, `[[ $v =~
  ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]`, never a line-based tool,
  and a version that fails stops the run.
- **Refusals.** The session's `close ... done` moves from the end of Phase 4
  to the end of the run, so a draft or a dispatch the gate refuses, or a
  question nobody answers, closes it `abandoned` with the step and the
  gate's reason, as PRD R16 requires; the skill never retries a refused
  step another way.
- **Phase 7** (Decision 3), before the installs.

Nothing else in the skill reads the line. The security-label question, the
local-command question and a check's `confirm:` question are untouched, and
Phase 2's preconditions still stop every run.

#### Alternatives Considered

**Share `posture-read.sh`.** Rejected: it carries `/coordinate`'s settings
parsing, sealing and its four-step token, none of which the release needs;
the shared part is about forty lines.

**Extract a common niwa-lookup library for both skills.** Deferred rather
than rejected: it's a refactor of `/coordinate` with no behaviour change, and
can follow once two callers exist.

**Ask niwa from SKILL.md prose.** Rejected: an agent composing the call and
the validation each run is untestable and drifts.

### Decision 3: Declaring and running the installs

Resolved inline.

The installs that follow a cut need a place to be declared, a moment to run
that is tied to the cut, and a way to reach a workspace-wide `niwa apply`
from a repository.

#### Chosen: an `## After release` extension heading run by a new Phase 7

The release extension gains `## After release`, parsed by the same bullet
reader as `## Release checks` and `## Release assets` (the command is the
bullet's first backtick span; a bullet without one is skipped with a warning
naming it). A new Phase 7 runs after Phase 6, carried out by a new script,
`skills/release/scripts/after-release.sh`, which the skill starts in the
background and waits on, since the wait outlasts a foreground shell call.
Phase 1 also records the commit the range ends at as a new `work/head` key
(`release-state.sh read <topic> head`), which the script reads.

1. It runs only when this run dispatched the workflow in Phase 5 and Phase 6
   saw that run conclude `success`, and never under `--dry-run`.
2. It polls `gh release view v<version> --json isDraft,tagName` every 30
   seconds for up to 30 minutes until `isDraft` is false for the tag this
   run dispatched. A failed `gh release view`, or the wait running out, ends
   the phase with a message naming each install not run.
   `RELEASE_AFTER_WAIT_SECONDS` and `RELEASE_AFTER_POLL_SECONDS` override the
   two values for tests. Phase 6 keeps its 5-minute watch of the prepare
   run, which finishes once the tag is pushed; its check that the draft was
   promoted becomes a line in its report, since in koto, niwa and tsuku the
   release is published later by the build and finalize workflows, and
   Phase 7 owns that wait.
3. It runs `release-grant.sh` again. On `go=grant`, the tracked commands run
   without a question, read with `git show <head>:.claude/shirabe-extensions/release.md`
   at the commit Phase 1 recorded in `work/head`, so an
   edit to the working copy during a long run can't add one. Otherwise one
   AskUserQuestion lists them; a yes runs them, and a no or no answer runs
   none.
4. Each command from `release.local.md` is asked about one at a time, for
   every session, as local checks are.
5. Each command runs from the repository root with `RELEASE_VERSION` (bare
   `X.Y.Z`, already checked against the version pattern) and
   `RELEASE_WORKSPACE_ROOT` (the nearest directory strictly above the
   repository root holding `.niwa/workspace.toml`, empty when none; the
   search starts above the repository because dot-niwa itself commits that
   file) set. A non-zero exit is reported
   with the command and its last 20 output lines, and the commands after it
   don't run: a failed reinstall or version check never reaches `niwa
   apply`. The output goes to the run's own report and is never copied into
   an issue or pull request.
6. The run's final report lists each command and how it ended: passed,
   failed, not run, or declined.

Each released repository declares its installs in a tracked
`.claude/shirabe-extensions/release.md`:

| Repository | Commands, in order |
|------------|--------------------|
| koto | reinstall koto with tsuku at `$RELEASE_VERSION`; a check that koto reports `$RELEASE_VERSION`; `cd "${RELEASE_WORKSPACE_ROOT:?not in a niwa workspace}" && niwa apply` |
| shirabe | reinstall shirabe with tsuku at `$RELEASE_VERSION`; a check that shirabe reports `$RELEASE_VERSION`; the same `niwa apply` |
| niwa | reinstall niwa with tsuku at `$RELEASE_VERSION`; a check that niwa reports `$RELEASE_VERSION`; the same `niwa apply`, now run by the new binary |
| tsuku | update tsuku itself to `$RELEASE_VERSION`; a check that tsuku reports `$RELEASE_VERSION` |

tsuku installs these tools from their repositories as distributed sources
(`tsuku list` shows `koto 0.25.0 [tsukumogami/koto]`), and each recipe's
verify command is `<tool> version`. The reinstall is `tsuku install
tsukumogami/<tool>@v$RELEASE_VERSION`, the form tsuku's help gives for a
distributed source at a version; whether tsuku wants the `v` is tried once
by hand on a published release when each extension is written. The check
after each reinstall turns a wrong spelling or a recipe
that hasn't caught up into a reported failure, and stops the list before
`niwa apply` converges the workspace with an old binary. It confirms the
version, not the binary's integrity; that rests on tsuku's own checksum
verification where the recipe declares one.

#### Alternatives Considered

**The skill runs `niwa apply` itself.** Rejected: it writes a workspace
concern into shirabe for every adopter, and a repository that isn't in a
niwa workspace would need a way to turn it off.

**One list in dot-niwa, delivered as each repository's
`release.local.md`.** Rejected: the skill asks before every untracked
command, so a holder would be asked again, and a hand-edited local file
can't be told apart from a delivered one.

**Gate the install commands by `cut-release` in the hook.** Rejected: a
holder could then install at any time, and every other session would be
asked before routine commands.

### Decision 4: Rollout and the live check

Resolved inline.

The change removes a person from releases, so the order it lands in decides
when that happens.

#### Chosen: every question-adding change first, the live check, then the widening

1. niwa: the authority guide's coverage text (docs only).
2. dot-niwa: the gate rule and its case-table rows.
3. shirabe: `release-grant.sh`, the Phase 3 and Phase 7 changes and
   shirabe's own `## After release`, then a shirabe release carrying them.
4. koto, niwa, tsuku: each repository's `## After release` extension.
5. The live check (PRD R24) in a disposable workspace whose ceiling and
   `coordinator` role hold all three authorities, against a throwaway
   repository whose `prepare-release.yml` calls shirabe's reusable release
   workflow at a pinned commit and whose `## After release` writes
   `$RELEASE_VERSION` to a marker file. Its release secret is a credential
   scoped to that repository alone with a short expiry, never a production
   release token, and the repository and the secret are deleted when the
   check ends, pass or fail; the expiry covers a run that crashes before
   cleanup.
6. dot-niwa: the ceiling and `coordinator` role gain `cut-release` and the
   comment is rewritten, with the live check's output in the pull request.
7. The coordinator that dispatched the running release seat re-dispatches it
   with `--role coordinator`.

Until step 6 no session in the workspace is a holder, so steps 1 to 4 only
add questions: the gate asks where it allowed, the skill asks as before, and
the installs ask once.

#### Alternatives Considered

**Widen first and rely on the skill's own question.** Rejected: the gate
would allow a holder's dispatch before the skill, the installs and the gate
had been run together once.

## Decision Outcome

A release workflow dispatch becomes the same kind of rule as `gh release
create` in the gate, so the grant decides it through the table the gate
already implements and a failure never widens it. The skill learns, through
one validated lookup, whether its own session is a holder, and uses that
only to skip the per-cut go and the install question. The installs live in
each repository's tracked extension and run in a new phase that waits for
the release to be published. dot-niwa grants `cut-release` only after all of
that has run against a throwaway repository.

The parts fit because each reads the same `niwa authority check` answer for
the same session: the skill's `go=grant` is exactly the gate's allow row, so
a run that skips its question is never then asked by the gate, and a run the
gate would ask is never told by the skill that it holds the go.

## Solution Architecture

### Overview

```
/shirabe:release (release seat)
  Phase 2  preconditions ............ stop on any blocker (unchanged)
  Phase 3  release-grant.sh --------> niwa authority check --point cut-release
           go=grant & minor/patch/given -> no question, provenance line
           otherwise -> AskUserQuestion (unchanged)
  Phase 4  gh release create --draft -> gate-online.sh: cut-release (unchanged rule)
  Phase 5  gh workflow run prepare-release.yml -> gate-online.sh: cut-release (new rule)
  Phase 6  monitor run (unchanged)
  Phase 7  wait isDraft=false -> release-grant.sh -> run ## After release
```

### Components

| Component | Repository | Change |
|-----------|------------|--------|
| `.niwa/hooks/pre_tool_use/gate-online.sh` | dot-niwa | `ghsub()`, `ghmethod()`, `release_name()`, the `workflow run` case and the dispatches path in `rules()`, the unreadable-selector test, `mentions()` patterns, header comment |
| `tests/gate-online.test.sh` | dot-niwa | rows for every PRD gate criterion; `179f588` vendored as the comparison hook |
| `.niwa/workspace.toml` | dot-niwa | ceiling and `coordinator` role gain `cut-release`; comment rewritten (last) |
| `skills/release/scripts/release-grant.sh` and its test | shirabe | new |
| `skills/release/scripts/after-release.sh` and its test | shirabe | new: the publication wait, the committed-extension read, the per-command environment, stop on first failure, the report |
| `skills/release/scripts/release-state.sh` | shirabe | a `head` key written by `membership` |
| `skills/release/SKILL.md` | shirabe | the version check in Phase 1, Phase 3 step 6, the session close moved to the end, Phase 6's promotion check as a report line, new Phase 7, dry-run and error-recovery rows |
| `.claude/shirabe-extensions/release.md` | shirabe, koto, niwa, tsuku | `## After release` (new file in koto, niwa, tsuku) |
| `docs/guides/authority-grants.md` | niwa | `cut-release` coverage and the gate note |

### Key Interfaces

`release-grant.sh` output, one line on stdout, exit 0:

```text
go=grant dispatch=3c803591-4c01-486f-a927-f96ec399d6a7
go=person reason=not-held
```

Phase 7's environment for each `## After release` command:

| Variable | Value |
|----------|-------|
| `RELEASE_VERSION` | the released version, bare `X.Y.Z` |
| `RELEASE_WORKSPACE_ROOT` | nearest ancestor holding `.niwa/workspace.toml`, or empty |
| `RELEASE_DRY_RUN` | `0` (Phase 7 never runs under `--dry-run`) |

The gate's new reasons:

```text
Online operation requires confirmation: release workflow dispatch
Online operation requires confirmation: session holds merge-pr,close-issue; needs cut-release
Online operation requires confirmation: workflow dispatch whose workflow can't be read
```

### Data Flow

A holder's cut: Phase 3 calls `release-grant.sh`, which calls niwa once and
prints `go=grant`; the skill prints the provenance line and creates the
draft; the hook sees `gh release create`, needs `cut-release`, calls niwa
once and allows; Phase 5 runs `gh workflow run prepare-release.yml`, and
the hook does the same; Phase 6 sees the run succeed; Phase 7 waits for the
finalize workflow to publish, calls `release-grant.sh` again, and runs the
tracked installs. No AskUserQuestion is called and no hook returns ask.

A non-holder's cut: `release-grant.sh` prints `go=person`; the skill asks
the go; the hook asks for the draft and for the dispatch, each naming what
the session holds; Phase 7 asks once before the installs.

## Implementation Approach

### Phase 1: niwa guide (niwa)

Edit `docs/guides/authority-grants.md`: the authority table's `cut-release`
row covers creating a release, dispatching a repository's release workflow,
and the installs a release run makes after its own cut; "A gate on the
binding" says a gate treats a release workflow dispatch as a release, and
that a text-reading gate covers only the commands it reads, naming the
release routes it doesn't.

### Phase 2: the gate (dot-niwa)

Add `ghsub()` and switch every `gh` rule to it, add `ghmethod()` and switch
the merge endpoint rule to it, then add `release_name()`, the `workflow run`
selector walk, the dispatches path in the `gh api` walk, the unreadable
character test and the `mentions()` patterns; update the header's rule list and "Gating another authority"
note. Vendor the `179f588` hook as `tests/fixtures/gate-online-179f588.sh`
and point the comparison at it; add `row`, `grant`, `calls` and `timed`
rows for every PRD gate criterion, with the stricter release rows marked.

### Phase 3: the skill (shirabe)

Add `release-grant.sh` and `release-grant_test.sh` (stub niwa covering every
reason token, the 30-second sleep, missing session id, missing instance
root, no niwa, niwa without `check`, a niwa under the working tree, a
non-UUID dispatch id); add `after-release.sh` and its test (stubbed `gh`,
`tsuku` and `niwa`, the shortened wait, stop on first failure, the
committed-extension read); add the `head` key to `release-state.sh`; edit
SKILL.md's Phase 1 version check, Phase 3 step 6, the session close, Phase
6's report and the new Phase 7, and extend Dry-Run Mode and Error Recovery; add shirabe's own `##
After release`; extend the release script tests for the bullet parser and
Phase 7's gating with a stubbed `gh`. Then cut a shirabe release, which this
workspace still does with the owner's go.

### Phase 4: the extensions (koto, niwa, tsuku)

Add `.claude/shirabe-extensions/release.md` with `## After release` to each,
trying each command once by hand on a release already published.

### Phase 5: live check and widening (dot-niwa)

Run the live check, then change `workspace.toml` and open the pull request
with the check's output; after merge, re-dispatch the release seat.

## Security Considerations

**The gate is friction, not a boundary.** It reads command text. It stops
a mistake and an injected instruction that types a release command, and it
doesn't stop a session determined to get through: a script file, a `gh`
alias, another language, a tag push, `gh release edit --draft=false` or the
REST releases endpoint reach a release without a command it reads. The
boundary that would hold is GitHub's, such as a protected environment with
required reviewers on each release workflow, which this work leaves to a
separate decision. niwa's guide edit says so, so the `cut-release` row
doesn't read as complete coverage.

**What widens, and for whom.** Only a session niwa dispatched with
`cut-release`, bound to that dispatch, at a level whose decider is the
session, gains anything: an allowed release dispatch at the gate, a skipped
go and skipped install question in the skill. After the widening that is
every coordinator dispatched with the `coordinator` role, each able to pass
`cut-release` to its workers, for any repository its token can reach, for
its session's life. That is the owner's direction; a narrower `releaser`
role holding only `cut-release` would limit it to the release seat.
Everything else gains a question. The gate's never-more-permissive
comparison against the `179f588` hook proves the first half, and the skill
falls back to asking on every non-holder answer.

**Bypasses closed in the same pass.** A repeated method flag (`-X GET -X
POST`), a glob, brace, tilde, backslash or percent sign in the selector, a
second positional word, and a flag between `gh` words each used to slip
past either the new rule or the existing ones; each now reads as the command
it is or asks.

**A forged or stale answer.** A session runs as the operator and could edit
its own record or mapping, as niwa's guide says; the gate isn't a boundary
against that, and this design doesn't make it one. The skill's own check is
advisory (Decision 2), so the gate, reading the payload's session and its
own path, stays the control.

**Unreviewed notes.** With the grant, notes are published without a person
reading them. The skill still asks when the range holds a direct push, an
unresolved pull request number or a security-labelled pull request, which
are the cases where a reviewed pull request didn't stand behind the text.

**Commands run after a cut.** `## After release` commands run with the
session's own permissions and environment, from the file as committed at
the release commit; an untracked local command is asked about for every
session. A compromised recipe or release would be installed on the host, as
the installs done by hand today would install it; the version check
confirms what was installed, not that it is safe, and a failure stops the
list before `niwa apply`. The extension is reviewed only as well as each
repository's pull request review is.

**`niwa apply` from the workspace root.** It converges every instance,
including ones other sessions use, and can change hooks in them, the gate
included, to what dot-niwa's main branch holds. That is reviewed content,
and it is what the owner runs after a release now.

**Residual risks put to the owner.** Three things this design accepts and
the owner should confirm rather than inherit: after the widening, a holder
reinstalls the new binaries and runs `niwa apply` across every instance,
which can rewrite every instance's hooks, the gate included, with no person
involved; every coordinator holds `cut-release` for any repository its
token reaches while it reads untrusted issue and pull request text, which
makes GitHub-side environment protection worth a dated follow-up; and a
tracked `## After release` bullet runs unattended at the next holder cut,
which a CODEOWNERS entry on `.claude/shirabe-extensions/` in the four
repositories would put behind a named reviewer. `gh run rerun` of a prepare
run and a `repository_dispatch` aren't read as releases either, like the
other routes above.

**Secrets.** Nothing here reads or prints a token. The release workflow
uses its repository's own secret, and the throwaway repository's secret is
created for the live check and deleted with it.

## Consequences

### Positive

- The owner grants releasing once per dispatch and stops answering per cut.
- A direct `gh workflow run` of a release workflow becomes a decided act for
  every session, closing a route that was open to everyone.
- The installs after a release are written down, reviewed, versioned and
  checked, where today they are remembered.
- The skill and the gate read one answer, so they can't disagree.

### Negative

- The owner's own session gets one more question per release, on the
  dispatch.
- A release workflow with another name isn't recognised until the list
  gains it.
- `niwa apply` from the root touches instances other sessions are using.
- A release whose build outlasts the 30-minute wait is published without
  its installs.

### Mitigations

- The extra question names why it's asked, and the owner can reverse the
  undispatched row of the gate table before it lands (PRD decision).
- The name list sits next to the rule in the hook, with a test row per
  name, so adding one is a one-line change and a row.
- Phase 7 reports each install it didn't run, so a late release is finished
  by hand with the list in front of whoever does it.
- `niwa apply` runs only after the reinstall and its version check pass, and
  converges instances to reviewed dot-niwa content, which is what the owner
  applies by hand after a release today.
