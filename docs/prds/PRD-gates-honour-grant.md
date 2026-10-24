---
schema: prd/v1
status: In Progress
problem: |
  niwa writes a dispatched session's named authorities (merge-pr, close-issue,
  cut-release) into a record before the session starts, but nothing that decides
  a merge, an issue close or a release reads it. The workspace gate hook denies
  merges unless the session's instance is on a configured list of instance names
  and asks a person before every close and release, so every coordinator hand-off
  costs a configuration change and a `niwa apply`, a refused session can't tell
  what it lacked, a child can be dispatched wider than its parent, and shirabe's
  `/coordinate` hands every merge to a person whatever it was granted.
goals: |
  The grant a session is dispatched with decides what it may merge, close and
  release. A successor coordinator granted all three does all three as its first
  actions with no configuration change and no person asked; a refusal names the
  grant held and the authority lacking; dispatch refuses a child wider than its
  dispatcher; a session can read back its own grant and get the gate's answer;
  and no session gets a more permissive verdict than today's hook would give it,
  except one whose grant allows the action.
absorbed:
  - docs/briefs/BRIEF-gates-honour-grant.md
---

# PRD: gates-honour-grant

## Status

In Progress

Absorbed [BRIEF-gates-honour-grant](docs/briefs/BRIEF-gates-honour-grant.md); carried in Absorbed Brief.

## Absorbed Brief

niwa records, before a dispatched session starts, which named authorities
it holds and where each came from, and that record is the one place a
dispatcher says what a session may land. Nothing that decides a landing
reads it. The workspace's gate decides merges from a list of instance names
and asks a person before every close and release, so a coordinator hand-off
is a configuration change, a session's grant has no effect at the moment it
matters, a refusal says nothing about what was missing, delegation can widen
authority, and `/coordinate` hands every merge to a person.

What should be true instead is that whoever dispatches a session decides, at
dispatch, what it may land, and that decision holds without anyone touching
the workspace afterwards. A successor coordinator granted merge, close and
release does all three as its first actions; a refused session reads what it
holds and what it lacks and hands the step back to its dispatcher; a child
can't be granted more than its dispatcher; a dispatched session can read its
own grant and get the gate's answer; and an operator or owner working
somewhere with no grant sees nothing change.

The feature holds in the session's read-back, the gate honouring the grant
for merging, closing and releasing, moving dispatched sessions off the
instance list, telling a dispatched session from a hand-started one in the
same instance, refusing a wider child at dispatch, and `/coordinate`
acting on its grant. It pushes
out stronger enforcement, the rest of never-wider, author exclusion, new
gated actions, refusal routing, moving the gate into niwa, the koto decision
record, and redesigning `/coordinate`'s dispatch, each set out in Out of
Scope below.

## Problem Statement

`niwa dispatch` can grant a worker named authorities, `merge-pr`,
`close-issue` and `cut-release`, through `--grant` or a role, and it writes a
record into the worker's instance before the worker starts saying what the
worker holds and where each authority came from
([authority grants guide](../guides/authority-grants.md)). That record is the
only place a dispatcher states what a session may land, and nothing that
decides a landing reads it.

The deciding is done by a workspace `PreToolUse` hook. The tsukumogami
workspace's hook, `gate-online.sh` in tsukumogami/dot-niwa, denies every merge
unless the session's instance is named in `GATE_MERGE_EXEMPT_INSTANCE`, and
asks a person before every `gh issue close` and `gh release create`. Whoever
runs a coordinator, and the coordinators themselves, pay for that:

- A successor coordinator runs in a new instance, so before it can merge
  anything someone changes the workspace configuration to name it, drops its
  predecessor and runs `niwa apply`. That happens at every coordinator
  hand-off, which in this workspace is a routine event rather than a rare one.
- A session dispatched without `merge-pr` in a listed instance can merge; a
  session dispatched with `close-issue` still waits for a person before each
  close.
- A refusal says the session was blocked, not what it holds or lacks, so it
  can't tell whether to hand the step back or stop.
- A dispatched session can dispatch a child with authorities it doesn't hold.
  niwa reads the dispatcher's record at dispatch to record the parent link,
  and compares nothing.
- shirabe's `/coordinate` reads the hook's text, finds merges behind it, and
  hands every merge to a person, whatever the coordinator was granted.

The grant record shipped with niwa's authority grants, which left the gates
to this work, so the declared grant now exists and has no effect.

Two facts make this harder than "make the hook read the record". The record
is per instance, and the gate has to tell the dispatched session from another
session someone starts by hand in the same instance, which today nothing can
do: niwa's session-scoped read follows a session mapping without checking
that dispatch wrote it. And the dispatch's session mapping is written only
after the worker has started, so a successor whose first action is a merge
can race it.

## Goals

- The person or coordinator who dispatches a session decides, at dispatch,
  what it may merge, close and release, and that decision holds with no
  configuration change afterwards.
- A session refused one of those actions learns, from the refusal, what it
  holds and what it lacks.
- No session is granted more than the session that dispatched it.
- A dispatched session, and `/coordinate` acting for it, reads the same answer
  the gate will give.
- Nothing gets more permissive except through a grant: a session niwa didn't
  dispatch, a host without niwa, and a dispatched session whose record can't
  be used get no more than today's hook gives them. Enforcement keeps today's
  reach; it isn't hardened.

## Terms

| Term | Meaning |
|------|---------|
| dispatch-written mapping | a session mapping in niwa's store that `niwa dispatch` wrote (its `origin` is `dispatch`), as opposed to one niwa's SessionStart hook wrote (empty `origin`) |
| bound | niwa can show that the session is the one a dispatch launched for the instance in question, and that dispatch is the one the instance's record names (R2) |
| record-unusable | niwa holds dispatch-written evidence (R2) that the session was launched into the instance, but the instance's record is missing, malformed, or names a different dispatch (R3) |
| not dispatched | neither of the above: no evidence, a mapping niwa's SessionStart hook wrote, or a mapping naming another instance |
| decision point, decider | as in niwa's authority guide; this work uses one point per authority, named after it (`merge-pr`, `close-issue`, `cut-release`) |
| niwa worktree | a worktree `niwa worktree` created under an instance's `.niwa/worktrees/` |
| root coordinator | a coordinator whose own session is not bound, so no dispatcher stands above it |
| posture | `/coordinate`'s per-step reading of what it may do: `permit`, `confirm` or `deny`, from `posture-read.sh` |
| today's verdict | what the gate hook at tsukumogami/dot-niwa commit `2a98f60` gives the same command from the same instance |

## User Stories

- As a reviewer replacing a coordinator, I want to dispatch its successor
  with a role and have its first actions merge a ready pull request and close
  the issue, and, in a workspace whose ceiling grants releases, cut the
  release too, so that a hand-off needs no configuration pull request, no
  `niwa apply` and no approval from me.
- As a worker dispatched with merge authority alone, I want a refused close
  to tell me I hold `merge-pr` and lack `close-issue`, so that I hand the
  step back to my coordinator (R11, R22); and I want a release that goes to a
  person to say the same about `cut-release`, so the person knows why it was
  asked.
- As a coordinator holding merge authority, I want a dispatch that asks for
  merge and close for a worker to be refused before anything launches, naming
  my own grant, so that I re-dispatch with merge alone.
- As a coordinator resumed with a ready pull request waiting, I want to read
  my own grant, each authority with its source, and get the answer the gate
  will give, so that I merge the pull request myself rather than queue it for
  a person.
- As an operator running `/coordinate` in a session I opened myself, or on a
  host with shirabe and koto but no niwa, I want it to read its posture from
  the workspace's settings and hooks as it does today.
- As the workspace owner, I want my own merging session, which isn't
  dispatched, to keep what today's exemption gives it.
- As the workspace owner rolling this out, I want the verdict changes the new
  hook brings for sessions already running written down and pinned by a test
  (Known Limitations, and the rollout criterion), so that I know to
  re-dispatch any that need a grant.

## Requirements

R1 to R9 and R25 land in niwa; R10 to R18 in dot-niwa (its configuration and
gate hook); R19 to R24 in shirabe. R26 and R27 apply to all three.

### Reading the grant (niwa)

- **R1. One session-bound lookup.** niwa provides one read-only command that
  takes an absolute directory inside an instance, a session id, and one or
  more decision point names, and answers in one call: the session's state
  (`bound`, `record-unusable` or `not-dispatched`); when bound, each
  authority its record grants with each authority's sources; and for each
  point, the level and the decider. The answer is a JSON document with a
  schema string, and it follows the authority record's versioning: fields may
  be added without changing the schema string, and a reader ignores fields
  it doesn't know. A relative or nonexistent directory, a directory in no
  instance, or a malformed session id exits non-zero with nothing on stdout.
  The gate, `/coordinate` and a session reading its own grant all use it.
- **R2. Binding a session to its dispatch.** A session is `bound` to an
  instance only when niwa holds evidence, written by `niwa dispatch` itself,
  that this session id is the one a dispatch launched into the instance the
  directory is in, and that dispatch's id equals the dispatch id in that
  instance's record. The evidence is, in order: the dispatch-written mapping;
  the session id dispatch recorded in the dispatch's lineage entry; or,
  before dispatch has recorded the session id, a capture-pending marker
  dispatch writes into the instance, used only while that dispatch's capture
  is pending and the dispatch process that wrote it is still alive (started
  no later than the marker's launch time), together with exactly one agent
  session record claiming the instance whose id is this session's. Every
  other session is `not-dispatched` unless R3 applies, and holds no grant.
- **R3. A dispatched session whose record can't be used.** A session for
  which R2's evidence exists but whose instance record is missing,
  malformed, or carries a different dispatch id is `record-unusable` and
  holds no grant. This holds before capture as well as after it.
- **R4. The first command finds the grant.** A dispatched worker is `bound`
  at the earliest moment it can run a command. In particular, the lookup
  reports it `bound` even when niwa's post-launch bookkeeping (capturing the
  session id and writing the mapping) hasn't finished.
- **R5. How long the binding lasts.** The binding has no time limit. It holds
  while the session idles, when niwa's own resume re-enters it, for subagents
  that share the session's id, and from any repo or niwa worktree of the
  instance. It ends when the instance is reaped or destroyed. A session that
  comes back under a new session id (a resume outside niwa that mints one, a
  respawn, a Codex worker that niwa couldn't map) is `not-dispatched`, and
  gets a grant again only by a new dispatch.
- **R6. A session reads its own grant.** A dispatched session reads back its
  own grant through R1, from the instance root, any repo in it, or a niwa
  worktree of it, printing each authority with its source (`dispatch` or
  `role:<name>`), or that it holds no named authority, or that its record is
  unusable. `niwa authority show` and `niwa authority decide` keep their
  current output for every input they accept today.
- **R7. A child is never wider than its dispatcher.** When the instance the
  dispatch runs from (or the one `--parent-from` names) has a record,
  `niwa dispatch` refuses a request for any named authority that record
  doesn't grant. The check runs after the existing ceiling checks and before
  dispatch asks for a prompt or creates anything. The refusal names each
  authority asked for beyond the dispatcher's grant, whether it came from
  `--grant` or from a role (by name), and the dispatcher's grant (or `none`).
  A dispatcher record that grants nothing caps the child at nothing; so does
  a dispatcher record in a schema version later than `niwa.authority.record/v1`,
  and so does a dispatcher instance with no record that a dispatch-written
  mapping names (a dispatched session whose record went missing). A dispatch
  run from the workspace root, outside any instance, takes as its dispatcher
  the dispatch whose dispatch-written mapping the calling session id names,
  when there is one. With no dispatcher record and no such mapping, no cap
  applies beyond the grant ceiling. Only named authorities are compared.
- **R8. A gate can find niwa.** A hook niwa installs into an instance can run
  niwa even when the harness gives it a `PATH` without niwa's directory.
- **R9. No koto.** Nothing in R1 to R8 reads a file koto writes, and all of it
  works on a host with no koto installed.
- **R25. No grant changes while a session runs.** Nothing niwa does while a
  session runs (a resume, `niwa apply`, a config change, another dispatch from
  or into the instance) changes that session's record, its mapping's dispatch
  id, or the lookup's answer about its grant. A wider grant needs a new
  dispatch.

### The gate (dot-niwa)

The gate decides by this table. "Today" is today's verdict for that instance
(deny for a merge unless the instance is on `GATE_MERGE_EXEMPT_INSTANCE`, in
which case allow; ask for a close or a release). "Non-exempt" is today's
verdict for an instance the list doesn't name: deny for a merge, ask for a
close or a release.

| Session state (R1) | Merge | Close | Release |
|--------------------|-------|-------|---------|
| `bound`, holds the authority, decider `session`, author exclusion `off` | allow | allow | allow |
| `bound`, lacks the authority | deny (lacking) | deny (lacking) | ask (lacking) |
| `bound`, holds it, decider `dispatcher` | deny (level) | deny (level) | deny (level) |
| `bound`, holds it, decider `person` | deny (level) | ask (level) | ask (level) |
| `bound`, holds it, decider `session`, author exclusion `on` | deny (exclusion) | ask (exclusion) | ask (exclusion) |
| `record-unusable` | deny | ask | ask |
| `not-dispatched` | today | today | today |
| lookup failed (R14) | today | today | today |

A release lacking `cut-release` keeps today's ask, so a person approving each
release keeps doing so exactly as now; the ask names what the session holds
and lacks. Deny for a missing authority applies to merge and close only.

- **R10. The grant decides for a bound session.** The gate applies the table
  above to the merge commands it gates today (`gh pr merge`, including
  `--auto`; `gh api` to the REST merge endpoint; the GraphQL
  `mergePullRequest` and `enablePullRequestAutoMerge` mutations), to
  `gh issue close`, and to `gh release create`. `GATE_MERGE_EXEMPT_INSTANCE`
  plays no part in a bound or record-unusable session's verdict. A command
  holding several gated actions gets the most restrictive of their verdicts
  (deny over ask over allow), as the hook does today.
- **R11. A refusal says why.** Every deny or ask the table marks
  "lacking" has a reason containing `holds <a>[,<a>...]` (or `holds none`)
  and `needs <authority>`. Every one marked "level" contains `level <level>`,
  `settled by <dispatcher|person>` and `<authority>`. Every one marked
  "exclusion" contains `author exclusion` and `<authority>`.
- **R12. Sessions niwa didn't dispatch keep today's verdict.** A
  `not-dispatched` session, including one started by hand in a granted
  instance and the owner's own merging session, gets today's verdict,
  exemption list included.
- **R13. A record that can't be used gets no exemption.** A
  `record-unusable` session gets what today's hook gives an instance the
  exemption list doesn't name: deny for a merge, ask for a close or release.
- **R14. A failed lookup keeps today's verdict.** When niwa can't be found,
  doesn't have the R1 command, exits non-zero, runs past the R15 bound, or
  prints a document whose schema string isn't exactly the one R1 defines,
  whose session state isn't one of R1's three, or whose authority, level,
  decider or author-exclusion values fall outside niwa's fixed vocabularies,
  the gate gives today's verdict.
- **R15. Lookup cost.** The gate calls niwa only for a command that matches a
  merge, close or release rule, makes at most one R1 call per hook run, and
  bounds that call at 3 seconds, or at `NIWA_LOOKUP_SECONDS` when that is set
  lower, as niwa's reference checks do. The whole hook run finishes within 5 seconds even when niwa
  hangs, against the harness's 60-second default hook timeout.
- **R16. Every other rule is unchanged.** Every deny and ask rule the grant
  doesn't cover gives today's verdict for every session: `curl`, `wget`, `nc`,
  `ncat`, `netcat`, `gh repo delete`, `niwa reap`, `gh auth switch`,
  `unset GH_TOKEN`, force pushes, text handed to a shell that mentions a gated
  command, a command name that is an expansion, and an unclosed quote around
  a gated command. One deliberate exception: today a command name that is an
  expansion and would be a merge (`$GH pr merge 12`) is allowed in a listed
  instance, because the exemption reaches it; after this work it is asked for
  every session, since a command the gate can't read never takes the
  exemption or a grant. That change is toward less permissive.
- **R17. The case table covers the grant.** The hook's test table has rows
  for every cell of the table above, for each merge form in R10, for each R14
  failure, and for R16, driven by a stub niwa, and runs on the Linux and macOS
  CI runners with no niwa or koto installed.
- **R18. This workspace grants merge and close.** The tsukumogami
  workspace's grant ceiling and its `coordinator` role hold `merge-pr` and
  `close-issue`; `cut-release` stays out, so a release stays a person's call
  here. Everything this work builds for release (the gate's release row, the
  release posture step, the never-wider check) is still built, and the
  release half of the first criterion is shown in a disposable test
  workspace whose own ceiling holds all three and which runs the same gate
  hook.

### The coordinator (shirabe)

- **R19. Posture from the coordinator's own grant.** When R1 says the
  coordinator's own session is `bound`, `/coordinate`'s posture for merge,
  close and release comes from that answer by the gate's table: `permit`
  where the gate would allow, `confirm` where it would ask, and `deny` where
  it would deny. For those
  three steps this replaces reading the gate hook's text, both when the
  posture is first read and when it's read again before a merge or close.
- **R20. No grant, today's posture.** When niwa is absent, the R1 call fails,
  or it says the coordinator is `not-dispatched`, `/coordinate` reads its
  posture exactly as it does today. When R1 says `record-unusable`, the
  posture matches the gate: `deny` for merge, and today's reading for close
  and release. shirabe runs without niwa installed.
- **R21. Each dispatch carries a grant.** When `/coordinate` dispatches a
  session through niwa, it passes a grant as `--grant` and its own instance
  as `--parent-from`. A successor coordinator gets the dispatching
  coordinator's own grant. A worker gets the authorities for the landing
  steps its brief hands it to take itself, and none when the brief hands it
  none; this is never wider than the coordinator's own grant, and R7 refuses
  it if it were. The brief's authority
  line is generated from that grant and names exactly its authorities, or
  says it holds none. When `niwa dispatch --help` doesn't list `--grant`,
  `/coordinate` dispatches without one and the brief says the worker holds
  none.
- **R22. A refused step goes back to the dispatcher.** A bound worker whose
  posture denies a merge, close or release hands the step to the coordinator
  that dispatched it through `/coordinate`'s existing need path. The need
  names the dispatcher's instance, not "reserved for a person". The
  dispatching coordinator then takes the step if its own posture permits it,
  or hands it up the same way. Only a root coordinator hands a step to a
  person.
- **R23. Release is a posture step.** `/coordinate`'s posture covers
  `release` beside `merge`, `close` and `teardown`, under R19 and R20.
- **R24. The rule and the record follow the grant.** `/coordinate`'s stated
  rule on the workspace's permissions says the posture for these three steps
  comes from the grant where R19 applies. shirabe's merge-policy design
  records an amendment to its decision that the gate hook's text governs the
  merge posture.

### Across all three

- **R26. Never more permissive.** Ordering verdicts allow, ask, deny from
  most to least permissive, no session gets a more permissive verdict than
  today's except in the table's first row.
- **R27. Older records.** A record written by niwa 0.36.0 or 0.37.0 reads
  through R1 with the grant it recorded. A reader of R1's document that
  ignores unknown fields reads a document from a later niwa without error.

## Acceptance Criteria

Gate scenarios run the hook as niwa installs it into an instance, with a
payload carrying the session id; "configured to name no instance" means
`GATE_MERGE_EXEMPT_INSTANCE` is unset. Dispatch scenarios use a fake agent
where noted.

**Grant decides (R10, R11, R18)**

- [ ] In the tsukumogami workspace, configured to name no instance, a
  successor dispatched with `--role coordinator` at level `autonomous`, with
  no configuration change and no `niwa apply` after the dispatch, runs
  `gh pr merge 12` and `gh issue close 34` as its first commands and both get
  allow; its `gh release create v1.2.3` gets ask with a reason containing
  `holds merge-pr,close-issue` and `needs cut-release`.
- [ ] In a disposable test workspace whose ceiling and `coordinator` role hold
  all three authorities and which runs the same gate hook, the same successor
  runs `gh pr merge 12`, `gh issue close 34` and `gh release create v1.2.3` as
  its first three commands; each gets allow and none is asked. Dispatched
  with `--grant merge-pr,close-issue,cut-release` instead, the same holds.
- [ ] Bound and holding `merge-pr` at `autonomous`, each merge form gets
  allow: `gh pr merge 12 --squash`, `gh pr merge 12 --auto`,
  `gh api -X PUT repos/o/r/pulls/12/merge`, a `gh api graphql` call with
  `mergePullRequest`, and one with `enablePullRequestAutoMerge`.
- [ ] Bound with `--grant merge-pr`, `gh issue close 34` is denied with a
  reason containing `holds merge-pr` and `needs close-issue`, and
  `gh release create v1` gets ask with a reason containing `holds merge-pr`
  and `needs cut-release`.
- [ ] Bound with no grant, `gh pr merge 12` is denied with a reason
  containing `holds none` and `needs merge-pr`.
- [ ] Bound with `--grant merge-pr,close-issue --level strict`,
  `gh pr merge 12` is denied and `gh issue close 34` gets ask, each with a
  reason containing `level strict` and `settled by person`. With
  `--level supervised`, both are denied with a reason containing
  `level supervised` and `settled by dispatcher`.
- [ ] With a stub niwa reporting a bound session holding `merge-pr` whose
  `merge-pr` point has decider `session` and author exclusion `on`,
  `gh pr merge 12` is denied with a reason containing `author exclusion`.
- [ ] Bound with `--grant merge-pr`, `gh pr merge 12 && gh issue close 34`
  is denied.
- [ ] `niwa authority policy` in the tsukumogami workspace shows a grant
  ceiling of `merge-pr` and `close-issue`, and a `coordinator` role holding
  both.

**Binding (R1 to R5, R25)**

- [ ] R1's lookup reports `not-dispatched` for: a session id with no mapping;
  a mapping niwa's SessionStart hook wrote; and a dispatch-written mapping
  naming another instance. In each case the gate gives today's verdict.
- [ ] R1's lookup reports `record-unusable` for a dispatch-written mapping
  naming the instance when the record has been deleted, replaced with
  malformed JSON, or rewritten with another dispatch id, and for a session
  bound through the lineage entry or the capture-pending marker when the
  record is deleted or malformed before capture finishes. The gate denies
  `gh pr merge 12` for such a session even when its instance is on
  `GATE_MERGE_EXEMPT_INSTANCE`, and asks for `gh issue close 34`.
- [ ] A session started by hand in an instance whose dispatched session holds
  `merge-pr`, in a workspace configured to name no instance, is reported
  `not-dispatched` and is denied `gh pr merge 12`.
- [ ] With dispatch's post-launch capture held back by a test hook so the
  mapping isn't written, a fake worker granted `merge-pr` runs R1's lookup and
  `gh pr merge 12` through the gate as its first command; the lookup reports
  `bound` and the merge gets allow.
- [ ] A bound session that idles while `niwa reap` runs (and leaves it, since
  its session is still live), is re-entered through niwa's resume, and then
  runs `gh pr merge 12` is
  allowed; so is a subagent payload carrying the same session id, and the
  same command run from a niwa worktree and from a repo of the instance.
- [ ] After `niwa destroy` of the instance, R1's lookup for that session id
  with a directory in another instance reports `not-dispatched`.
- [ ] A fake session resumed under a new session id is reported
  `not-dispatched` and denied `gh pr merge 12`.
- [ ] R1's lookup is run before and after each of: `niwa apply`, a change to
  the workspace's `[authority]` table, a resume, and a second dispatch from
  the instance. Its grant answer for the session is identical each time.
- [ ] R1 with a relative directory, a nonexistent directory, a directory in
  no instance, and a session id that isn't a UUID each exits non-zero with
  empty stdout.
- [ ] R1's document carries a schema string, the session state, and for a
  bound session each granted authority with its sources and each asked point
  with its level and decider; a test reader that ignores unknown fields
  parses it when an extra field is added.

**Reading one's own grant (R6, R27)**

- [ ] A session dispatched with `--role coordinator` reading its own grant
  from the instance root, a repo and a niwa worktree prints `merge-pr` and
  `close-issue`, each with source `role:coordinator`; one
  dispatched with `--grant merge-pr` prints `merge-pr` with source
  `dispatch`; one dispatched with no grant prints that it holds no named
  authority; one with a deleted record prints that its record is unusable.
- [ ] For every row of the gate table, a table-driven test asserts that the
  self read-back and the gate agree: a held authority with decider `session`
  is allowed, and nothing else is.
- [ ] `niwa authority show`, `show --json` and `decide` produce byte-identical
  output before and after this work for the existing test fixtures.
- [ ] A record fixture written by niwa 0.36.0, and one by 0.37.0, read through
  R1 with the grant they recorded.

**Never wider (R7)**

- [ ] From inside an instance whose record grants `merge-pr`,
  `niwa dispatch --grant merge-pr,close-issue` exits non-zero with no
  instance created and no prompt asked; the message names `close-issue`,
  `--grant` and the dispatcher's grant `merge-pr`. The same dispatch with
  `--grant merge-pr` launches.
- [ ] From the same instance, `--role coordinator` is refused naming the
  role `coordinator` and `close-issue`.
- [ ] From an instance whose record grants nothing, `--grant merge-pr` is
  refused; from an instance whose record is in a later schema version,
  `--grant merge-pr` is refused and a dispatch asking for nothing launches.
- [ ] From a directory in no instance with no session variable that names a
  dispatch-written mapping, and from an instance made by `niwa create` with
  no record, a dispatch is limited only by the grant ceiling.
- [ ] From an instance with no record that a dispatch-written mapping names,
  `--grant merge-pr` is refused.
- [ ] From the workspace root, a dispatch whose session variable names a
  dispatch-written mapping for a dispatch granting `merge-pr` is refused
  `--grant close-issue` and launches with `--grant merge-pr`.
- [ ] A session started by hand inside an instance whose record grants
  `merge-pr` can dispatch a child with `--grant merge-pr`, and is refused
  `--grant close-issue` (the Known Limitation, pinned).

**Failure keeps today (R8, R12, R14, R15, R16, R17)**

- [ ] With the harness `PATH` stripped of niwa's directory, a bound session
  granted `merge-pr` is still allowed `gh pr merge 12`.
- [ ] With no niwa reachable at all, with a stub niwa that lacks the R1
  command, one that exits 1, one that prints a document with an unknown
  schema string, one that prints a state outside R1's three, and one that
  sleeps 30 seconds, every gated command gets today's verdict for its
  instance, and the hanging case finishes in under 5 seconds.
- [ ] A session with no dispatch record in an instance on
  `GATE_MERGE_EXEMPT_INSTANCE` is allowed `gh pr merge 12` and asked for
  `gh issue close 34` and `gh release create v1`, exactly as today.
- [ ] Every command R16 lists gets today's verdict for a bound session
  holding all three authorities and for a `not-dispatched` session.
- [ ] A stub niwa that logs its invocations records no call for
  `git status`, `gh pr view 12` or `curl https://example.com`, and one call
  for `gh pr merge 12 && gh issue close 34`.
- [ ] The gate's case table passes on the Linux and macOS runners with no
  niwa or koto on `PATH`.
- [ ] For every combination of session state (each row of the gate table),
  exemption (listed or not) and command (each merge form, `gh issue close`,
  `gh release create`, and each R16 command), the case table compares the new
  hook's verdict with the verdict of the hook at `2a98f60`, kept as a test
  fixture so CI can run it, for the same input, and the new one is never more
  permissive except in the table's first row (R26).
- [ ] In a listed instance, `$GH pr merge 12` gets ask for a bound and a
  `not-dispatched` session alike (the R16 exception, pinned).
- [ ] A `record-unusable` session is asked for `gh release create v1`, and a
  `not-dispatched` session in a non-exempt instance is denied
  `gh pr merge 12 && gh issue close 34`.
- [ ] A session dispatched before this work with no `merge-pr`, into an
  instance on `GATE_MERGE_EXEMPT_INSTANCE`, is denied `gh pr merge 12` once
  the new hook is installed (the rollout change, pinned).

**No koto (R9)**

- [ ] With no koto on `PATH` and koto's session store made unreadable, every
  niwa criterion above passes.

**Coordinator (R19 to R24)**

- [ ] Run against a fixture instance whose gate hook names `gh pr merge` and
  a stub niwa reporting the coordinator `bound` with all three authorities at
  `autonomous`, `posture-read.sh` reports `permit` for merge, close and
  release, and so does the re-read before a merge.
- [ ] With the stub reporting `bound` with no `merge-pr`, the posture for
  merge is `deny`; with `merge-pr` at `strict`, it's `deny`; with no
  `cut-release`, release is `confirm`.
- [ ] With no niwa, with a stub that exits 1, and with a stub reporting
  `not-dispatched`, `posture-read.sh` prints the same posture token it prints
  today for the same settings and hooks. With a stub reporting
  `record-unusable`, merge reads `deny` and close reads as it does today.
- [ ] `/coordinate` dispatching its successor passes its own grant; a worker
  whose brief hands it no landing step is dispatched with no `--grant`.
- [ ] `/coordinate` dispatching a worker meant to hold `merge-pr` calls
  `niwa dispatch` with `--grant merge-pr` and `--parent-from` set to a
  directory in its own instance, and the brief's authority line names
  `merge-pr` and nothing else. With a niwa whose `dispatch --help` lacks
  `--grant`, it dispatches without one and the brief says the worker holds
  no named authority.
- [ ] A bound worker without `close-issue` that reaches a close records a
  need naming its dispatcher's instance; the text "reserved for a person"
  doesn't appear. A coordinator holding `close-issue` that receives it closes
  the issue; a root coordinator lacking it hands the close to a person.
- [ ] `posture-read.sh` reports a `release` step; under no niwa grant it
  reads `confirm` for a workspace hook that mentions `gh release create`.
- [ ] `/coordinate`'s skill text states the grant rule of R24, and shirabe's
  merge-policy design carries an amendment naming the changed decision.

## Out of Scope

- **Stronger enforcement.** Stopping a session that edits its own record,
  mapping, environment or settings, acts from a process outside
  `niwa dispatch`, or hides its identity; refusing outright instead of falling
  back when a lookup fails; checking the grant inside shirabe's own merge and
  close scripts; wrapping every command the grant forbids. The gate keeps
  today's reach and nothing here hardens the record or the mapping.
- **The rest of never-wider.** Capping a child's level by its dispatcher's,
  walking older ancestors, and what a dispatch with no verifiable parent may
  confer.
- **Deciding authorship.** Whether a given pull request is the session's
  own work. The gate honours the author-exclusion flag niwa reports for a
  point, but never reads who wrote what.
- **New gated actions.** Teardown and any other action joining these three.
- **Releases the hook doesn't see.** A release triggered by pushing a tag, or
  through the REST API, isn't one of today's gated commands and stays
  ungated.
- **Routing a refusal beyond the dispatcher.** Where a refused session goes
  next, and a refusal-only baseline.
- **Moving the gate into niwa.** The gate stays a workspace hook.
- **The koto decision record.** No gate reads it.
- **Redesigning `/coordinate`'s dispatch.** This work passes a grant through
  the dispatch path; it doesn't change how `/coordinate` dispatches.

## Known Limitations

- A session that comes back under a new session id loses its grant until
  it's dispatched again (R5). It falls to today's verdict for a session niwa
  didn't dispatch.
- R7 reads the record of the instance the dispatch runs from, so a session
  started by hand inside a granted instance can dispatch a child up to that
  instance's grant. A session that changes to a directory outside every
  instance, with no session variable naming a dispatch-written mapping,
  dispatches with no cap beyond the ceiling.
- R14 keeps today's verdict, exemption list included, when niwa can't answer,
  because without niwa a gate can't tell a dispatched session from any other.
  Once dispatched coordinators no longer need the list it names only sessions
  niwa didn't dispatch, so this costs nothing in practice; a workspace that
  still lists a dispatched session's instance keeps that gap until it removes
  the entry.
- When the new hook lands, every bound session is judged by its grant at
  once: a dispatched session without `merge-pr` in a listed instance loses
  merging, and close goes from ask to deny for every bound session lacking
  `close-issue`. Re-dispatching with a role fixes it, so running coordinators
  are re-dispatched with `--role coordinator` before the hook lands. These
  moves are all toward less permissive.

## Decisions and Trade-offs

- **Bind the grant to the session, not just the instance.** Reading by
  instance alone would hand a session started by hand inside a granted
  instance that instance's grant. The binding uses the dispatch-written
  mapping and the dispatch id already on the record (R2), so it needs no new
  trust anchor. Alternative: trust the record for any session in the
  instance; rejected for that reason.
- **The grant decides for every bound session, whatever the ceiling's
  source.** niwa's authority grants design and guide promised that a record
  whose ceiling source is `none` keeps today's verdict, so a gate upgrade
  wouldn't change merging for a workspace that never adopted grants. This
  work supersedes that promise for gates that honour the binding: a bound
  session is always judged by its grant, so a dispatched session is never
  judged by the instance list. Records written before a workspace declared
  its ceiling carry `none` and hold no grant, so those sessions lose what
  the list gave them until they're re-dispatched. R1 reports the record's
  ceiling source so a reader can see which case it is. Alternative: keep the
  opt-in rule; rejected because it leaves a dispatched session's verdict to
  the list.
- **An unusable record versus a failed lookup.** niwa can say
  `record-unusable` (R3) only when it runs, so R13 covers that answer and R14
  keeps today's verdict when niwa can't answer. Alternative: treat a failed
  lookup as non-exempt; rejected because the owner's merging session, which
  relies on the list, would lose merging whenever niwa is unreachable.
- **Close and release.** A session niwa didn't dispatch is still asked, as
  today. A bound session lacking `close-issue` is denied, because its close
  goes back to its dispatcher. A bound session lacking `cut-release` keeps
  today's ask for a release, with the reason naming what it holds and lacks,
  so a person approving each release from a dispatched release session keeps
  working as now.
- **The level and its refusals apply through one point per authority.** The
  gate asks the point named after the authority and allows only
  `decider=session` with author exclusion `off`. A `supervised` session
  holding the authority is denied, since the step goes back to its
  dispatcher. A `strict` session gets the non-exempt verdict (ask for close
  and release, deny for merge), since the point is a person's and a
  dispatcher that isn't a person can't settle it. Author exclusion `on` gives
  the non-exempt verdict too, naming author exclusion; the gate never reads
  who authored the work.
- **Never-wider covers named authorities against the immediate dispatcher.**
  The level half and the ancestor walk belong to the wider never-wider work,
  which extends this check. A dispatcher with no record and no
  dispatch-written mapping imposes no cap, so the owner dispatching from
  their own shell can still grant. At the workspace root, niwa already
  resolves the parent dispatch from the calling session's mapping for the
  lineage record; capping against that dispatch can't refuse wrongly,
  because an inherited session variable names an ancestor whose grant is at
  least as wide.
- **A standing rule narrower than the grant binds as an instruction only.** A
  dispatcher's rule carried in a brief, such as "never close an issue", isn't
  read by the gate or by niwa. R21 keeps the brief's authority line from
  claiming more than the grant; the gate decides by the grant alone.
- **`cut-release` stays out of this workspace.** The workspace's ceiling and
  coordinator role hold `merge-pr` and `close-issue` only, on the owner's
  ruling that a release stays a person's call here. Release support is
  built anyway, and the release half of the successor scenario is shown in a
  disposable test workspace whose ceiling holds all three. Alternative:
  widen this workspace's ceiling; rejected on that ruling.
- **No re-binding after a new session id.** niwa's own resume re-enters the
  same session, so the common path keeps its binding. Re-binding a new id
  would need a hook point niwa doesn't have and a way to tell a resume from a
  hand session; a re-dispatch is the supported path.
- **The idle reap.** niwa reclaims a dispatched instance only once its
  session is gone and removes the mapping after the instance, so a resumable
  idle session keeps both; R5 needs nothing more.

## Downstream Artifacts

- `docs/designs/DESIGN-gates-honour-grant.md` (planned): the lookup's shape,
  how R4's binding exists before the first command, the gate's changes, and
  `/coordinate`'s posture change.
