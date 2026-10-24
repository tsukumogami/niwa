---
schema: design/v1
status: Proposed
upstream: docs/prds/PRD-gates-honour-grant.md
problem: |
  niwa's authority record is per instance and written before launch, but the
  session mapping that ties a session id to a dispatch is written only after the
  worker starts, and niwa's session-scoped read doesn't check that dispatch wrote
  the mapping. So nothing can tell a gate which session in an instance holds the
  grant, the workspace gate decides from an instance-name list instead, dispatch
  never compares a child's grant with its dispatcher's, and shirabe's
  `/coordinate` reads the gate hook's text for its posture.
decision: |
  niwa adds `niwa authority check`, one read-only lookup taking a session id, an
  anchor directory and decision points, which binds the session to the instance's
  dispatch through the dispatch-written mapping, or before capture through the
  agent's own session records matched the way capture matches them, and answers
  `bound`, `record-unusable` or `not-dispatched` with the grant and each point's
  decider. Dispatch refuses a grant wider than the dispatcher's record, the
  SessionStart hook stops overwriting dispatch mappings, and niwa puts its own
  path in each settings document's environment. The dot-niwa gate splits its
  awk verdict into the rules the grant covers and the rest, makes one bounded
  lookup only for a merge, close or release, and decides those by a fixed
  table. `/coordinate`'s posture reader asks the same lookup first and falls
  back to today's reading.
rationale: |
  The binding reuses evidence niwa already writes (the mapping, the record's
  dispatch id, the lineage entry, the agent's own session records), so it adds
  no trust anchor and no launch flag, and it degrades to today's verdict
  whenever it can't answer. One lookup serves the gate, the coordinator and the
  session, so prediction and gate can't drift. Keeping the gate's text matching
  and adding one call only on gated commands keeps every other command's cost
  and verdict unchanged. Each repo can ship on its own: an old gate ignores the
  new command, and an old niwa reads as a failed lookup.
decision_provenance: inline-resolved
---

# DESIGN: gates-honour-grant

## Status

Proposed

## Context and Problem Statement

niwa 0.36.0 added authority grants: `niwa dispatch --grant <authority>` and
`--role <name>` resolve a set of named authorities (`merge-pr`,
`close-issue`, `cut-release`) against the workspace, overlay and machine
ceilings, and step (8a) writes the result into the new instance as a record
before the worker launches
([DESIGN-authority-grants](current/DESIGN-authority-grants.md)). The record
carries a `dispatch_id`. After launch, step (10) captures the worker's
session id by polling the agent's own session records for one whose working
directory is the instance, step (10a) writes that id into the dispatch's
lineage entry at `<root>/.niwa-lineage/<dispatch id>.json`, and step (11)
writes a session mapping at `<root>/.niwa/sessions/<session id>.json` with
`origin: dispatch` and the same `dispatch_id`.

The [PRD](../prds/PRD-gates-honour-grant.md) asks for the grant to decide
merges, issue closes and releases at the workspace gate, for the dispatched
session only, and for shirabe's `/coordinate` and the session itself to read
the same answer. Four technical gaps stand between today's code and that.

**No way to say which session holds the grant.** The record names a dispatch,
not a session, and a gate hook's only reliable identity inputs are its own
absolute path (`$0`, which names the instance) and the harness payload's
`session_id`. `niwa authority show --session <id>` follows a mapping's
`instance_path` without checking `origin` or comparing the mapping's
`dispatch_id` with the record's, and it can't be combined with `-C`. A
session someone starts by hand inside a granted instance would read the
instance's grant through `show -C`. The PRD requires such a session to get
exactly today's verdict (its R2 and R12), and requires a dispatched session
whose record is missing, malformed or names another dispatch to get no more
than an instance the exemption list doesn't name (R3, R13).

**The mapping arrives after the worker starts.** For Claude the gap between
launch and mapping is normally under a second, with a 30-second capture
ceiling; for a foreground Codex worker the mapping lands only after the
whole first turn. A successor whose first command is a merge has to be bound
anyway (R4).

**Dispatch compares nothing.** Step (2e) already reads the dispatcher's own
record (`dispatchAuthority.parent`) and uses only its dispatch id. The PRD
requires a request for any named authority that record doesn't grant to be
refused before a prompt or an instance exists, naming the authority, how it
was asked for and the dispatcher's grant; a dispatcher that grants nothing,
or whose record is in a later schema version, caps the child at nothing; no
dispatcher record means no cap beyond the ceiling (R7).

**The gate and the coordinator read text.** dot-niwa's `gate-online.sh`
decides with one awk pass over the command: merges are denied unless the
hook's own path names an instance in `GATE_MERGE_EXEMPT_INSTANCE`, and
`gh issue close` and `gh release create` are asked. shirabe's
`posture-read.sh` reads the same hook as text, so any hook that mentions
`gh pr merge` makes merge `confirm`, and every merge goes to a person. The
PRD wants a bound session judged by a fixed table (allow when the grant
holds the authority and the decision point named after it resolves to
`decider=session`; deny naming the held grant and the missing authority, or
naming the level; today's verdict for everyone else), at most one niwa call
per hook run bounded at 3 seconds, the whole hook under 5 seconds, and
`/coordinate` reading its merge, close and release posture from the same
answer with today's reading as the fallback (R10 to R16, R19 to R24).

Two constraints run through all of it. niwa must not need koto, and shirabe
must run without niwa (R9, R20). Enforcement stays at today's reach: the
record and the mapping are files the session's own user can edit, and that
is accepted (Out of Scope in the PRD).

## Decision Drivers

- **One answer for gate, coordinator and session.** The PRD's goal that a
  session reads what the gate will decide is only checkable if all three
  call the same thing with the same inputs.
- **Never more permissive.** Every failure, including a niwa too old to have
  the lookup, must land on today's verdict (R14, R26).
- **Bound from the first command** (R4), without a measurement this host can't
  run now: whether `claude --bg` honours a pre-assigned session id is
  unmeasured.
- **No new trust anchor.** The record and mapping are already the evidence;
  enforcement isn't being hardened, so the binding shouldn't pretend to be.
- **The gate's cost stays where it is for ungated commands.** It runs on
  every Bash call (R15).
- **Each repo ships on its own.** niwa, dot-niwa and shirabe release on
  separate schedules, and any order of rollout must stay safe.
- **No koto in niwa or the gate; no niwa requirement in shirabe.**
- **Bash 3.2 and BSD awk.** The gate runs on macOS's stock bash and awk as
  well as Linux; CI tests both.

## Considered Options

### Decision 1: The lookup's surface

The gate, `/coordinate` and the session need one call that answers binding,
grant and deciders together (R1). The question is whether that's a new
command or new flags on `show` and `decide`.

#### Chosen: a new `niwa authority check` command with its own document

`niwa authority check --session <id> -C <abs dir> [--point <name>]...
[--json]` prints one document with schema `niwa.authority.check/v1`: the
session state, the grant when bound, and each point's level and decider. One
process answers what the guide's reference checks needed three for, and its
document names the state explicitly, so a reader never infers binding from
the absence of a field. `show` and `decide` stay byte-identical (R6).

#### Alternatives Considered

**Add `--session` with `-C` to `show` and `decide`.** Rejected: `show`'s
document is the versioned record contract and has no place for a binding
state, the selectors are mutually exclusive today by design, and a gate
would still need two calls.

**Ship a shell library the gate sources.** Rejected: it moves niwa's binding
rules into a script every workspace copies, which is the parsing of niwa's
files the PRD rules out.

### Decision 2: Binding a session before capture

R4 requires the worker to be bound from its first command, while the mapping
is written after launch.

#### Chosen: the lookup applies capture's own match when capture hasn't finished

With no mapping for the session id, the lookup checks that the instance's
record is usable, that the dispatch's lineage entry exists, names an agent,
and has no session id yet (capture hasn't reached step 10a), and that no
dispatch-written mapping names the instance. It then reads that agent's
session records with the same function capture uses (`matchRecordByCwd`
against the instance directory). If exactly one record claims the instance
and its id is the payload's session id, the session is `bound`, with
`"binding": "records"`. Anything else (no record yet, two records, another
id) is `not-dispatched`. Once capture finishes the mapping takes over
(`"binding": "mapping"`). Nothing is written by the lookup.

This needs no launch change, works for every agent whose spec declares
session records, and can't bind a session capture wouldn't have bound.

#### Alternatives Considered

**Pre-assign the session id at launch** (`claude --session-id <uuid>`), and
write the binding before launch. Rejected for now: whether a background
Claude launch honours it is unmeasured, Codex has no equivalent, and it
changes the worker's argv, which the authority work kept identical whatever
was granted. It stays the better answer if measurement later shows the
records path misses a case.

**Write a pending binding before launch and give it to the first session
that asks.** Rejected: a session started by hand inside the instance during
the window could claim it.

**Have the gate wait for capture.** Rejected: the wait counts against the
5-second hook budget, and a foreground Codex worker's mapping can take a
whole turn.

### Decision 3: Letting a hook find niwa

R8 requires a hook to run niwa when the harness's `PATH` lacks it. niwa's
own hook commands already solve this with a PATH-first command that falls
back to the absolute path recorded at apply time, but a workspace hook is a
verbatim script with no templated path.

#### Chosen: niwa writes `NIWA_BIN` into every settings document's `env`

Materialization already knows niwa's absolute path (`NiwaPath`) and already
writes an `env` block. It adds `NIWA_BIN=<that path>` to each Claude
settings document it writes. A hook tries `command -v niwa` first and falls
back to `$NIWA_BIN` when that names an executable, the same PATH-first order
niwa's own hooks use, so an upgrade under a versioned install keeps working.

#### Alternatives Considered

**A shim at a fixed path inside each instance.** Rejected: another file to
keep in step across apply, reset and worktrees, for what an environment
variable already carries.

**Leave it to each workspace.** Rejected: the workspace can't know where
niwa is installed on each host.

### Decision 4: How the gate combines today's rules with the grant

The awk pass returns one best verdict today. The grant decides only merge,
close and release, and every other rule must keep its verdict (R16).

#### Chosen: awk reports two verdicts and the authorities a command needs

awk keeps its scanner unchanged and records, per command, which rule fired.
It prints three lines: the verdict from the rules the grant doesn't cover
(`base`), the merge, close and release verdict today's rules give with and
without the exemption (`today`, `nonexempt`), and the authorities the
command's gated rules need (`needs merge-pr,close-issue`). The shell part
then makes no lookup when `needs` is empty and returns `base`; otherwise it
makes one bounded `niwa authority check` call for the points in `needs` and
decides by the table below, then takes the most restrictive of that and
`base`.

| `check` answer | Each needed authority |
|----------------|-----------------------|
| `bound`, held, decider `session` | allow |
| `bound`, not held | deny: `holds <list or none>; needs <authority>` |
| `bound`, held, decider `dispatcher` or `person` | deny: `level <level>; <authority> settled by <decider>` |
| `record-unusable` | the `nonexempt` verdict |
| `not-dispatched` | the `today` verdict |
| lookup failed | the `today` verdict |

A lookup failed when niwa can't be found, exits non-zero, runs past the
bound, or prints a document whose `schema` isn't exactly
`niwa.authority.check/v1` or whose `state` isn't one of the three.

#### Alternatives Considered

**Paste the guide's three reference checks and keep the awk unchanged.**
Rejected: three calls instead of one, no binding (they read by instance), and
no way to tell `record-unusable` apart.

**Move the whole gate into niwa.** Out of scope by the PRD, and it would make
every workspace's gate depend on niwa's release schedule.

### Decision 5: The never-wider check at dispatch

#### Chosen: compare named authorities at step (2e), after `authority.Resolve`

`resolveDispatchAuthority` already holds the resolved request and the
dispatcher's record. A new `authority.CapByDispatcher(resolved, parent)`
returns the resolved grant entries the parent doesn't hold, each with its
sources; the parent's held set is empty when its record grants nothing or
was read as a later schema version. With no parent record it returns
nothing. Any entry refuses the dispatch before the binary preflight, the
prompt and provisioning, with one line per authority:

```text
niwa: error: authority close-issue (requested by --grant) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch 3c803591-...)
niwa: error: authority cut-release (requested by role coordinator) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch 3c803591-...)
```

Step (8a)'s re-check runs it again against the same parent, so provisioning
can't widen the child either.

#### Alternatives Considered

**Cap at the instance's ceiling only.** Rejected: that's today's behaviour,
which the PRD calls the problem.

**Read the dispatcher through the session id variable.** Rejected for the
same reason the authority work rejected it: a Codex worker inherits its
dispatcher's variable, so it would name the wrong parent.

### Decision 6: Where `/coordinate` reads the grant

#### Chosen: `posture-read.sh` asks `niwa authority check` before reading hooks

`posture-read.sh` already finds the instance root. When niwa is found (the
same PATH-then-`NIWA_BIN` order as the gate) and `CLAUDE_CODE_SESSION_ID` is
set, it runs `niwa authority check --json --session "$CLAUDE_CODE_SESSION_ID"
-C <instance root> --point merge-pr --point close-issue --point cut-release`,
bounded at 3 seconds. For `bound`, merge, close and release take `permit` or
`deny` from the answer by the gate's table, and teardown is read as today.
For `record-unusable`, merge is `deny` and the rest is read as today. For
anything else, the whole token is today's. A new `release` step joins
`merge`, `close` and `teardown`, matched by `gh release create` in the
hook-text reading, so the token gains `release:<v>`; its readers parse steps
by name, so the extra field doesn't break them.

`bl_merge_posture`, `land-check.sh` and `land-merge.sh` keep calling
`posture-read.sh`, so the land re-read gets the grant answer with no change
of their own.

#### Alternatives Considered

**Read `niwa list --json`.** Rejected: it's keyed by instance, not by
session, and doesn't carry the binding.

**Give `/coordinate` its own grant parser over niwa's files.** Rejected by
the PRD's one-lookup requirement, and it would tie shirabe to niwa's storage.

## Decision Outcome

niwa gains one lookup that knows which session an instance's grant belongs
to, a never-wider check at dispatch, a guard that keeps the SessionStart
hook from replacing a dispatch's mapping, and an environment variable that
tells hooks where niwa is. dot-niwa's gate keeps its scanner and asks niwa
only when a command needs an authority, deciding those by a fixed table and
leaving every other rule alone. `/coordinate` asks the same lookup the gate
asks, so its posture matches the gate, and it falls back to reading hooks
whenever there's no bound answer.

The pieces fit because each one degrades to today. A gate on a host with an
old niwa gets a failed lookup and today's verdict. A new niwa under an old
gate changes nothing, because nothing calls `check`. A `/coordinate` without
niwa reads hooks as it does now. The only verdict that becomes more
permissive is a bound session's, for an authority its grant holds at a level
that lets it settle the point.

## Solution Architecture

### Overview

```
PreToolUse payload {session_id, tool_input.command}
  -> gate-online.sh (awk: base, today, nonexempt, needs)
       needs empty -> base
       needs set   -> niwa authority check --session S -C dirname($0) --point ...
                        -> table -> most restrictive(base, grant verdict)

posture-read.sh -> niwa authority check --session $CLAUDE_CODE_SESSION_ID -C <instance root>
                     bound -> merge/close/release from grant; else today's hook reading

niwa dispatch (2e) -> Resolve -> CapByDispatcher(parent record) -> refuse | continue
```

### Components

**niwa**

- `internal/authority/check.go` (new): the state type
  (`bound`, `record-unusable`, `not-dispatched`), the `CheckDoc` document
  type with its schema string, and `CapByDispatcher`.
- `internal/cli/authority_check.go` (new): the command. It validates `-C`
  as `show -C` does (absolute, existing, in an instance of this workspace,
  outermost instance, a niwa worktree counts as its instance) and the
  session id as `workspace.ValidSessionID`, refusing with exit 1 and empty
  stdout otherwise. It reads the record, the mapping, and, only for the
  pre-capture case, the lineage entry and the agent's session records. It
  runs under the same two-second watchdog `show` uses.
- `internal/cli/dispatch_capture.go`: `matchRecordByCwd` is reused by the
  lookup as it is; no behaviour change.
- `internal/cli/dispatch_authority.go`: calls `CapByDispatcher` after
  `authority.Resolve` and again in `recheckDispatchAuthority`.
- `internal/cli/instance_from_hook.go`: before writing a mapping, reads any
  existing mapping for the session id and leaves an `origin: dispatch` one in
  place (R25).
- `internal/workspace/materialize.go`: adds `NIWA_BIN` to every Claude
  settings document's `env`.
- `docs/guides/authority-grants.md`: the `check` command, its document, the
  binding rule, and a rewritten "Writing a gate" section; the old reference
  checks stay documented for gates that read by instance.

**dot-niwa**

- `.niwa/hooks/pre_tool_use/gate-online.sh`: the awk `rules()` function tags
  each match with its class (`base`, or the authority it needs); `END`
  prints `base`, `today`, `nonexempt` and `needs`; the shell adds the niwa
  locator, a bounded lookup function in the guide's style (`timeout` when
  present, a background job and `kill` otherwise), the jq checks, and the
  table. The header comment describes the grant rule and that
  `GATE_MERGE_EXEMPT_INSTANCE` now applies only to sessions niwa didn't
  dispatch.
- `tests/gate-online.test.sh`: a stub `niwa` on `PATH` that prints a fixture
  chosen by an environment variable, logs its argv to a file, and can sleep
  or exit non-zero; rows for every table cell, each merge form, each failure,
  the R16 commands, compound commands, the invocation log, the 5-second
  bound, and a comparison against the hook at commit `2a98f60` over the same
  inputs (R26).
- `.niwa/workspace.toml`: `ceiling` and `roles.coordinator.authorities` gain
  `cut-release`, and the comment above them says why; this merges only with
  the workspace owner's sign-off (PRD R18).

**shirabe**

- `skills/coordinate/scripts/posture-read.sh`: the grant read before the
  settings and hooks read, the `release` step, and a `source=grant` detail in
  the `coord/posture.json` context key so a reader can tell where a step's
  posture came from.
- `skills/coordinate/scripts/dispatch-worker.sh`: adds `--grant` (from the
  brief's new `grant` field) and `--parent-from <own instance root>` when
  `niwa dispatch --help` lists them, using the probe it already runs for
  `--brief` and `--skill`.
- `skills/coordinate/scripts/render-brief.sh` and the brief template: the
  authority line is generated from `grant` ("holds merge-pr", "holds no named
  authority"), and the brief tells the worker that a landing step its grant
  doesn't cover goes back to its dispatcher.
- `skills/coordinate/scripts/need-check.sh`: a reserved step's wording names
  the dispatcher's instance when the coordinator's own `check` answer is
  `bound`, and "a person" only for a root coordinator.
- `skills/coordinate/koto-templates/coordinate.md` and `SKILL.md`: the
  posture rule text (R24), the release step in the land and close-out
  directives.
- `docs/designs/current/DESIGN-coordinate-merge-policy.md`: an amendment to
  Decision 5 recording that for a bound coordinator the grant, not the hook's
  text, sets the merge, close and release posture.

### Key Interfaces

`niwa authority check --session <uuid> -C <absolute dir> [--point <name>]... [--json]`

```json
{
  "schema": "niwa.authority.check/v1",
  "state": "bound",
  "binding": "mapping",
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "grant": [
    {"authority": "merge-pr", "sources": ["role:coordinator"]},
    {"authority": "close-issue", "sources": ["role:coordinator"]}
  ],
  "level": "autonomous",
  "level_source": "workspace",
  "points": [
    {"point": "merge-pr", "decider": "session", "author_exclusion": "off"},
    {"point": "close-issue", "decider": "session", "author_exclusion": "off"}
  ]
}
```

| Field | Present | Meaning |
|-------|---------|---------|
| `schema` | always | `niwa.authority.check/v1`; additive fields don't change it |
| `state` | always | `bound`, `record-unusable` or `not-dispatched` |
| `binding` | `bound` | `mapping` (captured) or `records` (before capture) |
| `dispatch_id` | `bound`, `record-unusable` | the dispatch the mapping or lineage entry names |
| `grant` | always | held entries with sources, `[]` unless `bound` |
| `level`, `level_source` | `bound` | the record's level |
| `points` | `bound` | one entry per `--point`, decided as `decide` decides it |
| `reason` | `record-unusable`, `not-dispatched` | a short machine-stable code: `no-mapping`, `hook-mapping`, `other-instance`, `other-dispatch`, `record-missing`, `record-malformed`, `record-other-dispatch` |

Plain output, for a session reading its own grant (R6):

```text
session: bound to dispatch 3c803591-4c01-486f-a927-f96ec399d6a7 (mapping)
grant: merge-pr (role coordinator), close-issue (role coordinator)
level: autonomous from workspace
```

or `session: not dispatched (no-mapping); grant: none` and
`session: dispatched, record unusable (record-missing); grant: none`.

Exit 0 for every state; exit 1 with empty stdout for a bad `-C`, a session
id that isn't a lowercase UUID, a directory in no instance, or the watchdog;
exit 2 for a usage error. A record from niwa 0.36.0 or 0.37.0 is read by the
same code `show` uses, so its grant reads as recorded (R27).

### Data Flow

**Binding.** For `--session S -C D`:

1. Find the instance `I` holding `D`. None: exit 1.
2. Read `I`'s record. Read the mapping for `S`.
3. Mapping present, `origin: dispatch`, `instance_path` resolves to `I`:
   record usable and its `dispatch_id` equals the mapping's, `bound`
   (`mapping`); otherwise `record-unusable` with the matching reason.
4. Mapping present any other way: `not-dispatched` (`hook-mapping` or
   `other-instance`).
5. No mapping: if the record is usable, its lineage entry has an agent and
   no session id, and no dispatch-written mapping names `I`, match the
   agent's session records against `I`; exactly one match whose id is `S`
   is `bound` (`records`). Otherwise `not-dispatched` (`no-mapping`).

A session resumed through niwa's resume (`claude attach`) keeps its id, so
step 3 still binds it. A subagent's payload carries its parent's id. A
command run from a niwa worktree or a repo resolves to the same `I`. After
`niwa destroy` or a reap the record and mapping are gone, so step 2 finds
nothing and the answer is `not-dispatched`.

**Gate.** awk runs once; if `needs` is empty the hook prints `base`'s
decision and makes no niwa call. Otherwise the shell runs one `check` with
`--point` per needed authority, bounded at `min(3, NIWA_LOOKUP_SECONDS)`,
and applies the table.

**Dispatch.** (2e) resolves, reads the parent, caps; (8a) resolves again,
caps again, writes the record; launch; capture; lineage session id; mapping.

## Implementation Approach

The work lands in three repositories, niwa first, because the gate and
`/coordinate` call a command only a new niwa has. Each later step is safe
against an older niwa (a failed lookup), so the order is for usefulness, not
for safety.

### Phase 1: niwa lookup and binding (niwa)

`internal/authority/check.go`, `internal/cli/authority_check.go`, unit tests
for every binding branch with fake records, mappings, lineage entries and
agent session records, and functional scenarios with the fake agent: bound
by mapping, bound by records with capture held back by the existing test
seam, hand-started session, SessionStart-hook mapping, other instance, other
dispatch, deleted and malformed record, after destroy, a niwa worktree, and
the 0.36.0 and 0.37.0 record fixtures. A regression test pins `show`, `show
--json` and `decide` output. The `TestCI_AuthorityRunsWithoutKoto` guard
covers the new command.

### Phase 2: niwa never-wider, mapping guard and `NIWA_BIN` (niwa)

`CapByDispatcher` with its refusal text at (2e) and (8a); the from-hook
guard; `NIWA_BIN` in materialized settings. Functional scenarios: wider
grant and wider role refused with no instance created and no prompt;
narrower launches; dispatcher with no grant; later-version dispatcher; no
dispatcher record; a hand session in a granted instance (the PRD's pinned
limitation). Guide update. A niwa release follows this phase.

### Phase 3: the gate (dot-niwa)

The awk split, the lookup, the table and the stub-driven case table, with
the comparison against the hook at `2a98f60`. Runs in the existing
`test-hooks.yml` matrix with no niwa installed.

### Phase 4: the workspace ceiling (dot-niwa)

`cut-release` in the ceiling and the coordinator role, separate from the
gate change so the owner can sign it off on its own.

### Phase 5: `/coordinate` (shirabe)

The posture read and `release` step; the dispatch flags and the brief's
grant line; the need wording; the template and skill text; the merge-policy
amendment. Tests drive `posture-read.sh` with a stub niwa and the existing
fixture instances.

## Security Considerations

**Who can claim a grant.** The binding trusts three same-user-writable
files: the record, the mapping and the lineage entry, plus the agent's own
session records. A process running as the operator can forge any of them,
which today's threat model already accepts: anything running as the operator
can act with the operator's rights, and the gate is a text-matching hook a
session can go around. This design doesn't make that worse. It adds no path
that binds a session niwa didn't launch: a hand-started session has no
mapping and, before capture, either no matching session record or a second
one that makes the match ambiguous, which reads as `not-dispatched`.

**The pre-capture window.** In the window between launch and capture, a
second background session started by hand in the same instance produces two
records claiming its directory. The lookup then binds neither, which is the
safe answer: the dispatched worker falls to today's verdict until capture
writes its mapping, and capture itself refuses an ambiguous match as it does
today.

**Failure direction.** Every lookup failure (no niwa, old niwa, hang, bad
output, unknown schema, unknown state) reads as today's verdict. A
`record-unusable` answer reads as the non-exempt verdict, so a dispatched
session can't regain the exemption by deleting its record.

**Untrusted input.** The payload's `session_id` reaches niwa only as an
argument niwa validates as a lowercase UUID before building any path; the
anchor is the hook's own absolute `$0`, never the payload's `cwd`. The deny
reason strings carry authority names and a level, all from niwa's closed
vocabularies, never text from the command.

**Hook timing.** A harness that kills a slow hook drops its verdict, deny
included. The gate bounds its one call at 3 seconds behind niwa's own
two-second watchdog, and its whole run at 5 seconds, well under the
60-second default.

**`NIWA_BIN` in the environment.** The variable names a binary path the
session could already find; it carries no credential. A session that points
it elsewhere only changes what its own gate runs, which it could already do
by editing its hooks.

**Never-wider and identity.** The cap reads the record of the instance the
dispatch runs from. A session started by hand inside a granted instance can
dispatch a child up to that instance's grant; the PRD records this as a
known limitation for the later enforcement work.

## Consequences

### Positive

- A coordinator hand-off needs no workspace configuration change: the
  successor's own grant lets it merge, close and release.
- A refused session reads exactly what it holds and what it lacks.
- `/coordinate` predicts the gate by asking the same lookup.
- Delegation can't widen named authorities.
- The exemption list shrinks to sessions niwa didn't dispatch.

### Negative

- The lookup's pre-capture branch reads agent session records, a coupling to
  each agent's record layout that capture already has.
- `gate-online.sh` grows a shell section and a stub-driven test harness.
- When the new gate lands, bound sessions lacking `close-issue` or
  `cut-release` move from ask to deny, and a dispatched session without
  `merge-pr` in a listed instance loses merging.
- A session resumed under a new session id outside niwa loses its grant until
  re-dispatched.

### Mitigations

- The record-layout coupling reuses capture's function, so one change covers
  both, and a layout change fails toward `not-dispatched`.
- The gate's case table compares the new hook against the old one over the
  same inputs, so a regression in an ungated rule fails CI.
- The rollout change is pinned by a test and described in the guide;
  re-dispatching with `--role coordinator` restores a coordinator.
- Pre-assigning session ids at launch remains open as a follow-up if
  measurement shows the records path misses a resume or respawn case.
