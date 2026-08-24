---
schema: prd/v1
status: Accepted
problem: |
  niwa elevates a dispatched Codex worker with arguments on that one
  process's command line -- a deliberate per-invocation grant that dies
  with the process. Every command niwa builds or prints to step back
  into the session is assembled from the agent's resume verb and the
  session handle alone, so the next process re-evaluates posture from
  the developer's own configuration, where the instance root isn't
  trusted, and the session silently drops to read-only with per-command
  approval. The worker a developer steps back into is not the worker
  they dispatched.
goals: |
  Every command niwa constructs or prints that re-enters a session it
  launched carries the launched agent's declared workdir grant for that
  session's instance. A developer who names a posture on the same
  command line still gets the one they named, and niwa keeps writing
  nothing into the developer's own Codex configuration. A test fails
  when the grant stops reaching any re-entry surface, and the
  acceptance evidence is a measurement against the real binary with a
  control, runnable without a login and at zero model cost.
absorbed:
  - docs/briefs/BRIEF-codex-dispatch-posture-persistence.md
source_issue: 273
motivating_context: |
  Measured against the real binary (codex-cli 0.149.0, isolated
  CODEX_HOME, zero model turns): a session resumed without the
  launch-time grant records a read-only sandbox before its first model
  request; resumed with the same grant niwa passes at launch, the
  posture is identical to the launch turn. Controls ruled out subagent
  inheritance and in-process decay as explanations. Reported as
  tsukumogami/niwa#273.
---

# PRD: codex dispatch posture persistence

## Status

Accepted

This PRD owns the requirements for carrying niwa's per-invocation
workdir grant through every command that steps back into a dispatched
session. It closes its upstream brief's two open questions -- how an
explicitly requested posture composes with the grant, and which
re-entry surfaces exist today -- and leaves every mechanism to the
downstream design.

Absorbed [BRIEF-codex-dispatch-posture-persistence](docs/briefs/BRIEF-codex-dispatch-posture-persistence.md); carried in Absorbed Brief.

## Absorbed Brief

The framing this PRD was written from, carried forward because the brief
that held it was folded in here.

niwa has to make a dispatched worker writable in a directory the agent
has never seen, and it does that for one process rather than by writing
the developer's own configuration. That is a deliberate trade and it is
the reason the feature exists in the shape it does: a per-process
override vouches for the one worker niwa starts, while a persisted entry
would vouch for anything anybody runs in that directory afterwards. So
the problem is not "the worker can't write" -- it is that a guarantee
scoped to one process was never carried to the next one, and every door
back into the session goes through a new process.

The outcome the framing commits to has three parts, and the requirements
below are written against all three. A developer stepping back into a
dispatched session finds the worker in the posture niwa launched it
with, whichever command they came through. A developer who names a
posture deliberately gets the one they named -- the grant is niwa's
default for its own workers, not the last word over a person. And niwa's
footprint does not grow: the fix extends the grant's reach, never its
blast radius.

That last clause is the boundary, and it was drawn at framing time
rather than trimmed later. Persisting trust into the developer's own
configuration would also fix resume, and it is refused anyway, because
it fixes it by vouching for every future session anyone starts in that
directory. Whether niwa should ever widen what it writes there is a
product question that belongs to whoever owns that decision; it is not
this feature's to settle, and nothing below assumes an answer to it.

## Problem Statement

When niwa dispatches a background Codex worker into a freshly
provisioned instance, it has to make the worker writable in a directory
Codex has never seen. It does that per invocation: arguments on the
worker's own command line mark the working directory trusted for
exactly that process. The choice is deliberate, and the reasoning is
recorded beside the grant in `internal/agentplan/dispatch.go`: a
per-process override vouches for the one worker niwa is starting and
evaporates when it exits, where writing the developer's configuration
would vouch for anything anybody ever runs in that directory
afterwards.

The defect is that nothing carries the grant past the process it was
handed to. Three commands step back into a session niwa launched --
the attach niwa itself runs when an agent hands a session over during
the dispatch turn, the resume hint printed after a successful dispatch,
and the resume command `niwa list` prints beside a session -- and all
three are built from the agent's resume verb and the session handle
alone. The next process re-evaluates posture from the developer's own
configuration, where the instance root is not trusted, and the session
silently drops to read-only with per-command approval. It wrote freely
all morning; it asks permission for everything after lunch.

This was measured, not inferred from reading configuration code.
Against the real binary (codex-cli 0.149.0, isolated CODEX_HOME, zero
model turns), a resumed session records its resolved sandbox policy
before the first model request: without the grant, read-only; with the
grant niwa passes at launch, workspace-write, identical to the launch
turn. Two controls rule out the competing explanations. Subagent
threads write their own session records and the drop reproduces on a
plain resume with none involved, and the process niwa launched stays
workspace-write for its whole life -- the first read-only turn appears
at the next process's bootstrap.

## Goals

- A developer who steps back into a dispatched session, through any
  command niwa built or printed, finds the worker in the posture niwa
  launched it with: writable in its instance, no approval prompt per
  command.
- The guarantee doesn't depend on which door they came through. The set
  of covered commands is defined by what re-enters a session, so a
  surface added later is covered by the rule rather than missed by a
  list.
- A developer who names a posture deliberately gets the one they named.
  The grant is niwa's default for its own workers, not the last word
  over an explicit ask.
- niwa's footprint doesn't grow. Nothing is written into the
  developer's own Codex configuration on the re-entry path, exactly as
  on the launch path -- and that property is tested, not asserted.
- The regression is pinned by a test that fails when the grant stops
  reaching any re-entry surface, and the acceptance evidence is a
  measurement against the real binary with a control, not an argv
  assertion.

## User Stories

- As a developer who dispatched a task with `--detach`, I want the
  resume hint dispatch printed to bring the session up in its launch
  posture, so that the worker I resume after lunch is the worker I
  dispatched, not a read-only copy asking permission per command.
- As a developer whose terminal is long closed, I want the resume
  command `niwa list` prints to carry the same guarantee, so that the
  surface I happen to re-enter through doesn't decide what the session
  can do.
- As a developer inspecting a finished worker, I want an explicit
  sandbox choice I add to a niwa-printed command to win over niwa's
  grant, so that I can open the session read-only on purpose.
- As a developer who audits what tools write to my machine, I want a
  resumed session elevated without any trust entry landing in my own
  Codex configuration, so that niwa's vouching stays scoped to the
  processes it launches.
- As a maintainer reviewing this change, I want the evidence to be a
  recorded sandbox posture from the real binary with a negative
  control, so that I'm reviewing a measurement rather than a claim
  about arguments.

## Requirements

- **R1. Every re-entry command carries the grant.** Any command niwa
  constructs or prints that steps back into a session it launched
  carries the workdir grant the launched agent declared, with that
  session's own instance directory substituted into it. Membership in
  this set is the rule itself -- "steps back into a session niwa
  launched" -- not a fixed list. Today the set has three members: the
  attach niwa runs when an agent hands a session over during the
  dispatch turn, the resume hint printed after a successful dispatch,
  and the resume command `niwa list` prints beside a recorded session.
  To make the rule testable rather than aspirational, every re-entry
  command is produced through one shared production path, and a test
  fails when any other code path assembles one -- so a fourth surface
  is covered the moment it exists instead of missing the grant
  silently.
- **R2. The grant comes from the agent's launch declaration, and only
  from it.** The re-entry path reads the grant from the same
  declaration the launch path reads. The session mapping records which
  agent launched the session; the agent's own declaration supplies the
  grant. No second table mapping agents to grants exists on the
  re-entry path. An agent that declares no workdir grant produces
  re-entry commands with no grant in them.
- **R3. An explicit ask wins.** When a developer names a posture on a
  re-entry command, the session comes up in the posture they named.
  Measured against the real binary, this is the binary's own
  resolution: an explicit sandbox selection outranks the trust-derived
  default, so a posture the developer appends takes effect over the
  grant. niwa's obligation is to leave that resolution intact -- it
  never strips, rewrites, or refuses what the developer typed.
- **R4. Zero footprint, testably.** Elevating a resumed session writes
  nothing into the developer's own Codex configuration -- no trust
  stanza, no configuration file that wasn't there before. The testable
  form: on the same run that records the granted posture, an isolated
  Codex home holds nothing it didn't hold before the resume. Why only
  one elevation route can satisfy this is recorded in Decisions and
  Trade-offs.
- **R5. Printed commands survive a POSIX shell.** The grant's value
  contains braces, quotes, and equals signs. Every re-entry command
  niwa prints must be quoted so that pasting it verbatim into a POSIX
  shell hands the binary exactly the arguments niwa intended.
- **R6. A mapping without an instance directory yields no grant.** When
  the recorded session mapping carries no instance directory, the
  re-entry command carries no grant -- never one built from an empty or
  guessed path.
- **R7. The regression test doesn't trust the production table.** A
  test fails when the grant stops reaching any one of the re-entry
  surfaces, and its expected values are written into the test rather
  than read from the table production reads -- expectations derived
  from the production table pass vacuously when the table itself is
  wrong.
- **R8. Acceptance is a measurement with a control.** The evidence that
  posture survives resume is the real binary's recorded sandbox policy:
  resumed through a niwa-built command, workspace-write, identical to
  the launch turn; resumed without the grant, read-only. An assertion
  about the argv niwa assembled is not acceptance evidence. The
  measurement requires no Codex login -- CI has none -- and spends zero
  model turns; a machine without the binary skips it rather than
  failing.
- **R9. The guide states the posture.** `docs/guides/codex-agent.md`
  says what posture a resumed session holds and why niwa grants per
  process instead of writing the developer's configuration.

## Acceptance Criteria

- [ ] For each of the three re-entry surfaces -- the attach command
  niwa constructs when an agent hands a session over, the resume hint
  printed after dispatch, and the resume command `niwa list` prints --
  the command carries the grant, and a dispatched session resumed
  through it against the real binary under an isolated Codex home
  records workspace-write in its instance before the first model
  request. The same resume without the grant records read-only,
  measured on the non-interactive resume form as the negative control.
  (R1, R8)
- [ ] A test fails when any code path outside the shared re-entry
  construction assembles a command carrying an agent's resume verb.
  (R1)
- [ ] The measurement runs with no credential present and completes
  without a model turn; on a machine without the Codex binary it skips
  rather than fails. (R8)
- [ ] On the same run that records workspace-write, the isolated Codex
  home holds no trust stanza and no configuration file the scenario
  didn't put there. The signal is the count of trust stanzas, not a
  checksum of the configuration file: unrelated bookkeeping rewrites
  that file on every run, so a checksum reports a change that says
  nothing about trust. (R4)
- [ ] Each printed re-entry command, executed through `/bin/sh -c`
  against a stub that records the arguments it received, yields a
  literal expected argument vector written into the test -- covering
  the grant value's braces, quotes, and equals signs. (R5)
- [ ] Against the real binary, a resume carrying the grant plus an
  appended explicit read-only sandbox selection records read-only.
  (R3)
- [ ] For a fixture agent that declares no workdir grant, each re-entry
  command equals a literal expected argument vector written into the
  test, with no grant in it. (R2)
- [ ] For a fixture agent whose launch declaration carries a
  distinctive literal grant, changing the grant in the declaration
  alone changes every re-entry command to match. (R2)
- [ ] With a recorded instance directory, the re-entry command names
  that directory inside the grant; with no instance directory
  recorded, the command carries no grant argument at all. (R6)
- [ ] Removing the grant from each of the three re-entry surfaces
  independently fails a test, and that test's expected values are not
  derived from the production declaration table. (R7)
- [ ] `docs/guides/codex-agent.md` states what posture a resumed
  session holds and the per-process reasoning behind the grant. (R9)

## Out of Scope

- **Writing trust into the developer's `~/.codex/config.toml` or any
  other persistent registry.** This would also fix resume, which is why
  it's worth excluding explicitly: it fixes it by vouching for every
  future session anyone starts in that directory, exactly the exposure
  the per-invocation design refuses. This feature extends the grant's
  reach, not its blast radius.
- **Changing what posture niwa grants.** The content of the grant --
  workspace-write, no network -- is untouched. This work is about the
  grant surviving re-entry, not about what it says.
- **Subagent threads spawned inside a worker process.** They inherit
  the resolved configuration of the process that spawned them, so
  fixing that process fixes them. Nothing niwa could put on a separate
  command line reaches them, and the measurement behind the brief
  confirmed they were never the source of the reported drop.

## Decisions and Trade-offs

Closing the brief's two open questions at requirements altitude, plus
one decision the zero-footprint requirement forced.

1. **The developer's explicit ask wins because the binary resolves it
   above the trust-derived default (R3).** Measured on the interactive
   resume form: with the grant alone, the session records
   workspace-write; with the grant plus an appended explicit read-only
   sandbox selection, read-only. Precedence is not something niwa
   constructs by ordering arguments -- an explicit sandbox selection
   outranks a trust-derived default in the binary's own resolution,
   and ordering buys nothing. The alternatives -- parsing the
   developer's arguments to strip or reject a conflicting posture, or
   letting the grant override unconditionally -- were rejected:
   parsing every agent's flag surface is fragile, and overriding a
   deliberate ask breaks the brief's commitment that the grant is a
   default, not the last word. niwa's obligation is stated as
   restraint: it never strips, rewrites, or refuses what the developer
   typed. One asymmetry, recorded: the non-interactive resume form
   accepts no sandbox flag at all, so the explicit-ask case exists
   only on the interactive form.
2. **Re-entry surfaces: a rule with a current membership of three,
   anchored in one production path (R1).** A scan of the tree on
   2026-08-24 found exactly three places that construct or print a
   command stepping back into a launched session: the attach niwa runs
   when an agent hands a session over mid-turn, the post-dispatch
   resume hint, and the resume command `niwa list` prints from a
   recorded session mapping. The alternative -- enumerating those
   three commands as the requirement -- was rejected because a fourth
   surface would then miss the grant silently. But the rule alone is a
   structural claim no test can fail on: an implementation that
   hand-wires the grant at the three known sites passes every
   behavioral test while a later surface misses it. So the rule gets a
   testable anchor -- one shared production path builds every re-entry
   command, and a test fails anything else that does. A supporting
   fact from the same scan: the recorded session mapping already
   carries the instance directory a grant needs, which is what makes
   the degraded case in R6 detectable. The attach surface isn't
   reachable for a Codex worker today -- Codex doesn't hand sessions
   over mid-turn -- but it's in the set so the guarantee doesn't
   depend on which agent declares what.
3. **Only one elevation route satisfies the zero-footprint requirement
   (R4).** Measured against 0.149.0: asking for elevation at an
   untrusted directory through a sandbox flag or a `sandbox_mode`
   configuration override causes the binary itself to append a trust
   stanza to the developer's configuration file. The whole-table
   `projects` configuration override is the only measured route that
   leaves the developer's configuration untouched -- after a resume
   carrying it, an isolated Codex home held no configuration file at
   all. Both resume forms accept the override. R4 states the behavior;
   this decision records why exactly one route qualifies, so a later
   simplification to a flag-based route re-fails R4 instead of passing
   quietly.

## References

- tsukumogami/niwa#273 -- the report this work answers.
- docs/briefs/BRIEF-codex-dispatch-posture-persistence.md -- the
  upstream framing, including the measurement and its controls.
- `internal/agentplan/dispatch.go` -- the per-invocation grant and the
  recorded reasoning for it.
- `internal/cli/dispatch.go`, `internal/cli/list.go`,
  `internal/workspace/session_map.go` -- the re-entry surfaces and the
  session mapping as of the 2026-08-24 scan.
- `docs/guides/codex-agent.md` -- the contributor guide R9 updates.
