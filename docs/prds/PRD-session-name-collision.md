---
schema: prd/v1
status: Done
problem: |
  Developers who launch several background workers with `niwa dispatch` under a reused
  `--name` get workers that share one Claude Code session name, and that name is the
  address other sessions use to reach a worker. A message sent to a shared name has no
  documented destination, and niwa neither prevents the collision nor tells the developer
  it happened. It's already happening: an observed listing showed seven names shared by
  fifteen of 115 peers, and a developer had renamed a session by hand to tell two apart.
goals: |
  Every worker `niwa dispatch` launches under a developer-supplied name answers to a name
  that no other niwa dispatch holds, short of a random collision as unlikely as one
  between instance directory names, and that name still starts with the role the
  developer chose. The developer reads the name in the dispatch output and can recover it
  later from `niwa list`. niwa's documentation stops claiming a collision-safety that
  session names didn't have.
absorbed:
  - docs/briefs/BRIEF-session-name-collision.md
---

# PRD: Unique session names for dispatched workers

## Status

Done

This PRD owns the requirements for making the session names `niwa dispatch` forwards
unique. It closes its brief's two open questions, how short the distinguishing part can
be and how the developer learns the name, and leaves the mechanism to the design.

Absorbed [BRIEF-session-name-collision](docs/briefs/BRIEF-session-name-collision.md); carried in Absorbed Brief.

## Absorbed Brief

This PRD absorbed the feature's brief, which framed why the work matters before any
requirement was written.

**The problem.** A developer who dispatches several background workers under a reused
role name, such as two `--name review` dispatches for two pull requests, gets workers
that share one Claude Code session name. The session name is how other sessions reach a
worker, and the namespace spans every local, remote and cloud session on the account.
A message sent to a shared name has no documented destination, and nobody is told. The
collision lives in the session name, the one identifier niwa hands a worker and never
shows back. It is already happening: in a September 2026 peer listing, seven names were
shared by fifteen of 115 peers, and a developer had renamed a session by hand to tell two
apart.

**The outcome.** A developer reuses role names freely, niwa never hands a worker a name
another niwa worker already holds, and the developer can find the one name that reaches a
specific worker without renaming anything. A message sent to that name reaches that
worker and no other, so choosing a name goes back to being about readability.

**Who it serves.** A developer telling two live look-alike workers apart an hour after
dispatching them. A coordinating session handing work to a peer it picked by name, on
behalf of the developer who trusts its handoffs. A developer capturing a new worker's
name at launch, before switching to other work.

**Where it stops.** The feature makes sure niwa isn't the source of a collision it
created. It does not reach for uniqueness across sessions niwa didn't launch, and it
leaves `niwa watch` naming, unnamed dispatches, the `/dispatch` skill's brief files, how
Claude Code resolves an ambiguous name, the pre-approval of inbound peer messages, and
Codex workers to their own framing.

## Problem Statement

A developer runs parallel work with `niwa dispatch`, one background Claude Code worker
per task, and names each worker after its role: `review`, `coordinator`, `ci`. Role
names are short and they recur. A developer reviewing two pull requests at once
dispatches `--name review` twice.

Today both workers answer to the same session name. niwa sanitizes `--name` into a slug
and forwards that bare slug to the worker as its display name. It also generates a
random token for every dispatch, but it spends that token only on the instance
directory (`<config>+review-4e33acfa`), never on the name. In Claude Code the session
name is the address: a session finds a peer by listing sessions and sends to it by name,
and the namespace spans every background, interactive, local and remote session on the
account.

When a message goes to a name that two live sessions share, nothing documents which one
receives it. Claude Code's cross-session messaging documentation describes addressing by
name and states no rule for a name that matches two live sessions. The message may reach
the wrong worker or neither, and nobody gets an error. niwa gives no warning at dispatch
time, never prints the name it forwarded, and states in its own dispatch design that
dispatch "stays collision-safe even when two dispatches share a `--name`". That's true of
instance directories, and false of session names.

This is observed, not hypothetical. A peer listing taken in September 2026 from inside a
dispatched session showed 115 peers, seven names shared by fifteen of them, and one name
held by three sessions at once. On the same machine, a developer had renamed one of two
same-named dispatched sessions by hand to tell them apart. The framing this PRD was
written from is carried in the Absorbed Brief section above.

## Goals

- A developer reuses a role name across as many dispatches as their work needs, and every
  worker niwa launches under that name can be reached on its own.
- The name a worker answers to still starts with the role the developer typed, so the
  developer recognizes it in Agent View and in peer listings.
- The developer reads the forwarded name in the dispatch output, without piecing it
  together from an instance path, and can recover it later from `niwa list`.
- niwa never reports a name a session doesn't answer to.
- niwa's documentation stops claiming a collision-safety that session names didn't have.

## User Stories

- **As a developer reviewing pull requests in parallel**, I dispatch two workers with
  `--name review` and later want to send the first one a follow-up. I want the two workers
  to answer to different names that both start with `review`, so the follow-up reaches the
  worker I meant, and I can tell them apart by the name I noted from each dispatch.
- **As a coordinating session that is itself a dispatched worker**, I list my peers, pick
  one by name, and send it a unit of work. I want the name I picked to belong to one niwa
  worker, even when other niwa workers on the account were dispatched under the same role
  name, so the developer who relies on my handoffs can trust they landed where I said.
- **As a developer who has just run `niwa dispatch`**, I want the dispatch output to tell
  me the name the new worker answers to, so I can note it before moving on and find the
  worker in Agent View or hand the name to another session.
- **As a developer who closed the terminal that ran the dispatch**, I want `niwa list` to
  show the worker's name next to its instance, so a name printed once isn't lost with the
  terminal.
- **As a tool or skill that reads `niwa dispatch` output**, such as the `/dispatch` skill
  or niwa's own live tests, I want the session name on a line with a fixed label, and the
  existing lines left exactly as they are, so I can read the name without breaking how I
  already read the session id and the instance path.

## Requirements

### Name shape

- **R1. Unique forwarded name.** When `niwa dispatch` forwards a session name, that name
  SHALL differ from the name forwarded by any other niwa dispatch, on any machine and in
  any workspace, except by a random collision no more likely than a collision between two
  dispatch instance directory names today. Two dispatches given the same `--name`,
  whether run one after the other or at the same time, SHALL forward different names.
- **R2. Name format.** The forwarded name SHALL be the sanitized slug derived from
  `--name`, unchanged and never shortened, followed by `-`, followed by a distinguishing
  part of at least 8 lowercase hex digits. The whole name SHALL match
  `^[a-z0-9_]+-[0-9a-f]{8,}$`. The slug alphabet is `[a-z0-9_]` and never contains `-`, so
  removing the final `-` and everything after it yields the sanitized slug exactly. No
  length cap applies to the forwarded name.
- **R3. Unique by construction.** The distinguishing part SHALL carry at least 32 bits
  read from a cryptographically secure random source before the worker launches. It SHALL
  NOT depend on reading, listing, or locking any other session, instance, or peer, so it
  holds against peers niwa can't see. The random source SHALL be replaceable in tests. If
  reading it fails, the dispatch SHALL fail before any worker launches, as it does today
  when the instance name can't be generated.
- **R4. One argument.** The forwarded name SHALL reach the worker as a single argument
  placed immediately after the agent's display-name flag. By R2 it never starts with `-`.
- **R5. Fixed for the life of the dispatch.** Resuming, attaching to, or listing a
  dispatched session SHALL NOT forward or report a different name. No path generates a
  second name for a dispatch.

### Reporting

- **R6. Reported at dispatch.** When a dispatch forwards a session name, the stdout
  success block SHALL include the line `  session name: <name>` (two leading spaces, the
  label `session name: `, then the name, then the end of the line), where `<name>` is
  byte-identical to the value forwarded to the worker. The line SHALL come immediately
  after the `  instance:` line and before any management hint lines. The success block
  prints before niwa branches on launch mode, detach, or attach, so the line SHALL appear
  on every exit that prints the success block: the default attach flow, `--detach`, a
  failed attach, a foreground launch, and an agent that won't open a session mid-turn.
  A dispatch that fails before the success block prints no report line. That includes a
  foreground dispatch whose session capture fails: it exits zero and keeps the work, as it
  does today, but prints no success block and so no report line.
- **R7. Existing output unchanged.** The headline `Dispatched session <uuid>` SHALL keep
  its current shape, with nothing appended. The `  instance:` line and the hint lines
  SHALL keep their current text and order. The fixed text of the report line (everything
  except `<name>`) SHALL NOT contain `instance: ` and SHALL NOT name an agent binary. The
  `<name>` value is whatever R2 produces from the developer's input.
- **R8. Recoverable from `niwa list`.** For each dispatch that forwards a session name,
  niwa SHALL record the forwarded name. For an instance whose dispatch recorded one,
  `niwa list` SHALL print `  session name: <name>` on the line directly after the instance
  name and before any `  resume:` line, byte-identical to the forwarded value. `niwa list --json` SHALL carry the same value in a `session_name` field on that
  instance's record.
- **R9. Never a name the session doesn't hold.** For an instance with no recorded forwarded
  name (a session dispatched before this change, an unnamed dispatch, or a dispatch whose
  agent declares no display-name flag), `niwa list` SHALL print no session-name line and
  its `--json` record SHALL omit `session_name`. niwa SHALL NOT derive a session name from
  an instance directory name.

### Unchanged behavior

- **R10. Unnamed dispatches unchanged.** A dispatch with no `--name`, or whose `--name`
  sanitizes to an empty slug, SHALL forward no session name, print no report line, and
  record no name, exactly as today.
- **R11. Neighboring commands unchanged.** `niwa watch` SHALL forward the same session
  names and print the same `(handle ...)` lines it does today. The name-forwarding helper
  `buildDispatchPassthrough` is also called by `niwa watch` at its staging and resume
  paths, so the suffix SHALL be applied by the dispatch caller, not inside that helper. `niwa create --name` SHALL
  produce the same instance names. Dispatch instance directory naming SHALL stay
  `<config>+<slug>-<8hex>`, and the reaper's eligibility for dispatch instances SHALL be
  unchanged.
- **R12. Agents without a name flag unchanged.** When the launched agent declares no
  display-name flag (Codex today), dispatch SHALL forward no name, print no report line,
  and record no name.
- **R13. Existing sessions untouched.** The change SHALL NOT rename, message, or otherwise
  modify sessions dispatched before it. For a session mapping written before the change,
  `niwa list` (text and `--json`), `niwa status`, `niwa reap`, and the resume, logs and
  stop hints SHALL produce the same output they produce today.

### Documentation

- **R14. Documentation corrected.** In the same change:
  - the `--name` flag help of `niwa dispatch` SHALL say that the session name is the slug
    plus a distinguishing suffix;
  - the doc comment on `buildDispatchPassthrough` in `internal/cli/dispatch.go` SHALL stop
    saying the session carries the same display name embedded in the instance directory;
  - in `docs/designs/current/DESIGN-instance-dispatch.md`, the sentence saying dispatch
    "stays collision-safe even when two dispatches share a `--name`" SHALL either say it
    holds for session names too or be limited to instance directories, and the statements
    that the slug names the Claude session (in D1 and D2) SHALL describe the suffixed name;
  - `internal/workspace/rootskills/dispatch/SKILL.md` SHALL describe the suffixed name,
    and its report-back step SHALL tell the agent to relay the reported session name
    alongside the session id. `niwa apply` already rewrites installed root skills, so
    existing workspaces pick the text up on their next apply;
  - the long help of `niwa list` SHALL mention the session-name line and the
    `session_name` JSON field.

### Non-functional

- **R15. No added dispatch-time lookups.** Producing the name SHALL depend only on the
  slug and the random source. The dispatch SHALL NOT gain any network call, subprocess, or
  enumeration of directories or of other sessions' records. Reading and writing the
  dispatch's own records is allowed.
- **R16. Agent-neutral implementation.** The change SHALL keep
  `TestDispatchPathNamesNoAgentConstant` and `TestDispatchPathNamesNoAgentLiteral` in
  `internal/cli/dispatch_layout_test.go` passing, and SHALL key any per-agent difference
  on the agent's declared flags rather than on its name.

## Acceptance Criteria

Criteria marked *(review)* are checked by reading the change. Every other criterion is a
test.

- [ ] With the random source stubbed to return the byte `0xab` for every read,
      `niwa dispatch "<task>" --name review` forwards, immediately after the display-name
      flag in the recorded launch argv, a name matching `^review-(ab){4,}$`. (R2, R3, R4)
- [ ] Two dispatches with `--name review` and different stubbed random bytes forward
      different names, both matching `^review-[0-9a-f]{8,}$`. (R1, R2)
- [ ] The name-generation function, called from 64 goroutines at once under `go test
      -race` with the real random source, returns 64 distinct names and no race is
      reported. (R1, R3)
- [ ] With the random source stubbed to return an error, the dispatch exits non-zero,
      provisions no instance, launches no worker, and prints no report line. (R3, R6)
- [ ] For inputs `Review`, `auth layer`, `café!!`, and a `--name` that sanitizes to
      exactly 40 runes, the forwarded name equals the sanitized slug, then `-`, then the
      distinguishing part; the 40-rune slug appears in full; and removing the final `-`
      and what follows yields `sanitizeInstanceSlug(input)`. (R2)
- [ ] The name-generation function takes only the slug and the random source as inputs,
      and the change adds no network call, subprocess, or directory enumeration anywhere on
      the dispatch path. *(review)* (R15)
- [ ] A successful named Claude dispatch prints on stdout, as the line directly after the
      `  instance:` line, `  session name: <name>`, where `<name>` is byte-identical to
      the value after the display-name flag in the recorded launch argv. (R6)
- [ ] The report line appears on stdout in the default flow with attach stubbed to
      succeed, with `--detach`, with attach stubbed to fail, for a stubbed launch spec
      that declares a display-name flag and runs in the foreground, and for a stubbed spec
      that declares a display-name flag and doesn't open a session mid-turn. (R6)
- [ ] A dispatch whose launch, session capture, or mapping write is stubbed to fail prints
      no report line. (R6)
- [ ] The first stdout line still equals `Dispatched session <uuid>` with nothing after
      the UUID. The existing test that finds the instance path by the first line
      containing `instance: ` still passes. With a stubbed agent whose binary is not
      `claude`, stdout contains no `claude ` substring for a slug that doesn't contain
      one. (R7)
- [ ] After a named Claude dispatch, `niwa list` prints `  session name: <name>` on the
      line directly after that instance's name and before its `  resume:` line, and `niwa list --json` carries `"session_name": "<name>"` on its
      record, both byte-identical to the forwarded value. (R8)
- [ ] Neither the resume command `niwa list` prints nor the dispatch's attach step
      forwards a display-name flag whose value differs from the recorded name. (Today
      neither forwards one at all, and a test pins that.) (R5)
- [ ] For a legacy mapping fixture (a `.niwa/sessions/<id>.json` in today's schema with
      no recorded name and instance name `<config>+review-<8hex>`), `niwa list` text and
      `--json`, `niwa status`, `niwa reap`, and the resume, logs and stop hints produce
      output identical to a golden captured from the current binary, with no session-name
      line and no `session_name` field. (R9, R13)
- [ ] A dispatch with no `--name`, and one with `--name "!!!"`, forward no display-name
      flag, print no report line, record no name, and show no session-name line in `niwa list`.
      The existing tests pinning that behavior pass unchanged. (R10)
- [ ] A dispatch with `--name` against Codex's declared launch spec (whose display-name
      flag is empty) forwards no display-name flag, prints no report line, records no name,
      and shows no session-name line in `niwa list`. The declared spec is enough; no Codex
      binary is needed. (R12)
- [ ] Characterization tests for `niwa watch`, added before the change, record the
      display-name value its staging and resume paths forward and the `staged review ...
      (handle ...)` and `continued review ... (handle ...)` lines, and they pass unchanged
      after it. (R11)
- [ ] `niwa create --name "Auth Layer"` still produces an instance named
      `<config>+auth_layer`, dispatch instance directories still match
      `\+[a-z0-9_]*-[0-9a-f]{8}$`, and the reaper backstop's existing tests pass
      unchanged. (R11)
- [ ] The change contains no code that renames or messages an existing session.
      *(review)* (R13)
- [ ] The `niwa dispatch` `--name` help string contains the word `suffix`. The phrase
      "carries the same display name embedded in the instance directory" no longer appears
      in `internal/cli/dispatch.go`. `DESIGN-instance-dispatch.md` no longer contains
      "stays collision-safe even when two dispatches share a `--name`" unless the sentence
      either names only instance directories or states that session names carry the
      suffix. (R14)
- [ ] `DESIGN-instance-dispatch.md` no longer contains the phrases "a slug that names both
      the instance and the Claude session" or "the slug, which names the instance and the
      session" unless the sentence goes on to name the suffix, and its D2 description of
      the forwarded `--name` value mentions the suffix. The `/dispatch` skill's `--name`
      description mentions the suffix. (R14)
- [ ] A root-materializer test asserts that the installed `/dispatch` skill's report-back
      step names the session name, and a new test asserts that the `niwa list` long help
      contains `session name:` and `session_name`. (R14)
- [ ] `TestList_JSONShapeIsUnchanged` passes, with its comment updated to allow the
      optional `session_name` key. (R8, R9)
- [ ] `TestDispatchPathNamesNoAgentConstant` and `TestDispatchPathNamesNoAgentLiteral`
      pass unchanged. (R16)
- [ ] Any per-agent difference the change introduces is keyed on the agent's declared
      flags, not its name. *(review)* (R16)

## Out of Scope

- **Global uniqueness across the session namespace.** niwa can neither see nor lock
  sessions it didn't launch, including sessions on other machines, which were most of the
  observed peers. This feature makes sure niwa isn't the source of a collision it
  created, and stops there.
- **Detecting collisions at dispatch time.** Warning about, or reacting to, a same-name
  peer found at dispatch time is out. See Decisions and Trade-offs.
- **`niwa watch` review-session naming.** Its names collide by construction but double
  as staged-record filenames and typed handles. That needs its own framing (R11 keeps it
  unchanged).
- **Unnamed dispatches.** Whether anonymous workers should become addressable is a
  separate product decision (R10 keeps them unchanged).
- **The `/dispatch` skill's brief files.** Same-named dispatches overwrite each other's
  brief one layer above niwa. That's a real collision of the same family, and it needs a
  different fix. R14 touches only the skill's naming and report-back text.
- **The slug sanitizer and its 40-rune cap.** Both stay as they are. They're shared with
  `niwa create --name` and `niwa watch`.
- **`--label`.** It isn't shown, forwarded, or changed.
- **Cleaning up existing collisions.** Sessions dispatched before this change keep their
  names (R13).
- **Keeping names unique after someone renames a session inside Claude Code.** niwa
  doesn't observe renames made in the harness.
- **A machine-readable `niwa dispatch` output mode.** No `--json` or porcelain output is
  added to dispatch. `niwa list --json` gains one field (R8).
- **How Claude Code resolves an ambiguous name,** and the pre-approval of inbound peer
  messages for dispatched workers. Both belong to other work.
- **Codex workers.** They get no session name today (R12).

## Known Limitations

- Sessions dispatched before this change keep their bare names. After upgrading, a new
  `review` dispatch can't collide with another new one, but an old worker still answers
  to bare `review`, and `niwa list` shows no session name for it.
- Sessions niwa didn't launch can carry any name, including one that starts with a niwa
  worker's slug. Uniqueness holds among niwa-forwarded names only.
- Uniqueness is probabilistic. With 32 bits, two same-named dispatches collide about once
  in four billion pairs, the rate niwa already accepts for instance directory names.
- The forwarded name gets longer: a 40-rune slug plus `-` and 8 hex digits is 49
  characters. Claude Code's help documents no length limit for `--name`, but whether Agent
  View truncates a row that long hasn't been measured, and a truncated row could hide the
  distinguishing part. A manual check before release, dispatching with a 40-rune slug and
  looking at the Agent View row, settles it.
- niwa pairs each name with its instance, not with its task. A developer who didn't note
  which name went with which task tells them apart from the dispatch output or the task
  brief. `niwa list` shows no task description.
- A foreground dispatch whose session capture fails keeps the work and exits zero, as
  today, but prints no session name, even though the worker ran under one.
- The name isn't an attach handle. `claude attach`, `logs` and `stop` still take the short
  id printed in the hints, and the report line doesn't replace them.

## Decisions and Trade-offs

- **Unique by construction, not by detection.** Detecting a same-name peer at dispatch
  time and warning was considered. niwa can read the machine's job store cheaply, but
  most of the observed peers were off-machine and invisible to it, so a clean check would
  read as an all-clear it can't back. Suffixing only on a detected collision was also
  considered and rejected: it races against a worker launching concurrently, and it makes
  an unsuffixed name look like a guarantee. The instance directory reached the same
  conclusion for the same reason.
- **How strong and how long the distinguishing part is.** This closes the question the
  BRIEF left open about how short the distinguishing part can be. It carries at least 32
  bits as at least 8 lowercase hex digits, matching instance names, whose collision rate
  the dispatch design already accepted. The harness's own 6-hex row references tell rows
  apart in a listing and aren't a uniqueness guarantee, so they set no floor. Whether to
  reuse the instance directory's token or draw a fresh one is left to the design; R2's
  format holds either way.
- **`-` as the separator.** The slug alphabet never contains `-`, so a `-` separator keeps
  the boundary between role and suffix unambiguous and matches the instance directory
  convention. An `_` separator would make `review_ab12cd34` indistinguishable from a slug
  that ends in hex.
- **How the developer learns the name.** This closes the BRIEF's second open question. The
  name gets its own labelled stdout line, `  session name: <name>`, right after the
  instance line. Appending it to the headline was rejected because a live test reads
  everything after `Dispatched session ` as the UUID. Printing it to stderr was rejected
  because stderr also carries the key report and the reaper's output. A fixed label is what
  lets the `/dispatch` skill and tests find the line.
- **Recorded, not derived.** Deriving the name from the instance name that `niwa list`
  already shows was considered and rejected. An instance named `<config>+review-<8hex>`
  looks the same whether it came from a pre-change session answering to bare `review`, a
  Codex dispatch that forwarded no name, or a new Claude dispatch. Derivation would report
  names two of those three don't hold. niwa records the name it forwarded and shows only
  that (R8, R9). Where it's stored is left to the design.
- **No length cap.** Shortening the slug to make room for the suffix was rejected because
  it breaks the rule that the role survives unchanged, and Claude Code documents no limit.
  The Agent View width question stays a known limitation.
- **`--label` is not repurposed.** Using `--label` as the readable handle while the name
  carries the suffix was considered. It has no reader, never reaches Claude Code, and
  isn't sanitized, so it would need new surfaces to serve. The readable part stays inside
  the name, as the prefix the developer typed (R2).
- **Scope held to named dispatch.** The name-forwarding code is shared with `niwa watch`,
  and the slug sanitizer with `niwa watch` and `niwa create --name`. The change is scoped
  so neither neighbor moves (R11), and characterization tests pin watch's current behavior
  before the change lands.
