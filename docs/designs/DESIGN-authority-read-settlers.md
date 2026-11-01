---
schema: design/v1
status: Proposed
problem: |
  `niwa authority check` tells a bound session, per decision point, who
  settles a step, but gives a root or record-unusable session no per-point
  answer, gives no meaning to a teardown point, names no seat above a
  session that settles a step itself, returns a dispatcher as a dispatch id
  rather than an address, and has no authority over another coordinator's
  worker cap. A coordinator at a finishing step can't learn who settles it,
  or where to send it, from the lookup the gate uses.
decision: |
  `check` answers `--point` in every state by narrowing only: a root
  session from the no-flag level `decide` already uses, narrowed by the
  instance's record, with `dispatcher` read as `person`; a lost record as
  `person` with author exclusion on. Each point gains a `source`. The
  guide fixes `teardown` as a point name that only narrows, with no new
  authority. The answer gains a top-level `session` and a wider `parent`
  (session id, and a name guarded by the dispatch-written mapping) once
  per answer, never per point. niwa defines no cap-change authority.
rationale: |
  Every addition is a new member within check/v1, every new answer narrows,
  and neither the gate nor any verdict changes. Reusing `decide`'s
  no-record answer makes the two commands agree instead of leaving callers
  a second level table. Naming facts (the asking session, the seat above,
  an address when niwa has one) rather than a reviewer or an excluded
  author leaves the author-exclusion rule to its own work. A cap authority
  would be a label no gate can enforce.
---

# DESIGN: authority-read-settlers

## Status

Proposed

## Context and Problem Statement

`niwa authority check` ([DESIGN-gates-honour-grant](DESIGN-gates-honour-grant.md),
shipped in #417 and #418) is the one lookup a gate, a coordinator and a
session use to learn what a session holds. It binds a session to its
dispatch and answers `bound`, `record-unusable` or `not-dispatched`. For a
bound session it adds, per `--point`, the decider and author-exclusion
value that `niwa authority decide` computes
([DESIGN-authority-grants](current/DESIGN-authority-grants.md), R2, R26
and R27 of [PRD-authority-grants](../prds/PRD-authority-grants.md)).
Requirement numbers below are that PRD's unless they say "gates", which
means [PRD-gates-honour-grant](../prds/PRD-gates-honour-grant.md). A coordinator that has to work out who settles each finishing
step (merging a pull request, closing an issue, cutting a release, tearing
down an instance) and where to send that step finds five gaps in today's
answer:

1. **No per-point answer at the root.** `points` is built only in
   `CheckBound` (`internal/authority/check.go`); a `not-dispatched` answer
   is `{schema, state, reason, grant: []}` and a `record-unusable` one adds
   only `dispatch_id`. A session nobody dispatched, which is how a person's
   own session and a root coordinator run, can't learn who settles a step
   from niwa. Its caller falls back to its own rule ("bound goes to the
   dispatcher, root goes to a person"), which is a second copy of the
   level table that can drift from niwa's.
2. **No teardown point.** The point vocabulary is open (PRD R2: "callers
   choose"; `ValidPoint` checks only the name's shape), so `--point
   teardown` already returns a level-based answer. But no document names
   that point, no named authority covers teardown (the PRD's Out of Scope
   calls it "the likeliest next authority", left to the work that gates
   it), and the workspace gate doesn't gate `niwa destroy`. Callers read
   teardown's settler from the workspace's permission rules instead.
3. **No review target when the session settles.** When a point resolves
   to `decider=session`, the answer names only the session. A caller that
   always has its own work reviewed before it settles (a coordinator's
   verdict on its own milestone is never approved by its author) gets no
   target for that review from niwa.
4. **A named decider isn't deliverable.** `decider=dispatcher` comes with
   a `parent` of `{dispatch_id, instance}`. That's a key, not an address:
   no session id, no session name a message can be sent to, and no
   `parent` at all when the dispatcher was itself undispatched
   (`ParentDispatchID` empty). The record keeps `parent_session_id`
   (`internal/authority/record.go`), and the session mapping keeps the
   session name, but the answer exposes neither. With author exclusion
   `on`, the answer names who may *not* settle (the author) without naming
   who is the author. The PRD says plainly that it "only names the
   dispatcher; finding and reaching it is separate work".
5. **No authority over another lane's worker cap.** niwa's named
   authorities are a closed set of three (`merge-pr`, `close-issue`,
   `cut-release`; PRD R3: "a fixed set per niwa version"). A coordinator's
   active-worker cap is a coordinator setting that niwa neither stores nor
   gates. A session that wants to move another coordinator's cap has no
   grant to show for it, so the caller has to refuse every such move.

Two facts about the deployed configuration bear on the answers. First,
the point vocabulary really is open: nothing in niwa stops a caller from
asking about any well-formed name, and what this design "names" is a
documented, stable meaning for a name, not a new entry in a validator.
Second, what a workspace may grant is its own config's call. The
tsukumogami workspace's public config (`dot-niwa`, `.niwa/workspace.toml`)
today declares a ceiling and a `coordinator` role of `merge-pr`,
`close-issue` and `cut-release`, and an overlay can only narrow either.
Nothing in this design changes or depends on a particular ceiling.

The boundary on gap 3 and gap 4 is the author-exclusion rule. PRD R2
defines author exclusion `on` as "whoever settles the point may not be the
session that authored the work". Which sessions count as someone other
than the author at each level, and how a reviewing seat's verdict comes to
count as that seat's, belong to separate, not-yet-started work on author
exclusion. This design must leave that rule where it is: it may say *where*
a review or a decision can be delivered, never *whether* a given seat
qualifies.

## Decision Drivers

- **One answer for gate, coordinator and session.** The gates design's
  first driver: whatever is added comes through `niwa authority check`, so
  a caller's prediction and the gate's verdict can't drift.
- **Never more permissive.** Nothing added may widen what a session can
  settle or what the gate allows. Every new field is advisory to a router
  or narrows; the gate's table for `bound` and its today's-verdict
  fallback for everything else stay as they are (gates design R14, R26).
- **Additive within `niwa.authority.check/v1`.** The schema comment and
  the guide promise additive fields don't change the version; shirabe's
  reader validates enum values, so a new *value* in an existing field is a
  compatibility question and a new field isn't.
- **The decider names a seat, not a session.** PRD R2: the dispatcher is
  "the coordinating position, whichever session currently fills it", and
  the record keeps no "who supervises now" field so later work can add
  one. An address niwa returns is the original occupant's unless niwa
  knows better.
- **No tool requirement in either direction.** niwa reads nothing koto
  writes (PRD R31) and shirabe must run without niwa (gates PRD R20). A
  coordinator's successor chain lives in its own records, so niwa can't
  follow it; shirabe can't require niwa to answer.
- **Leave author exclusion's rule to its own work.** The answer may carry
  facts a rule needs (who the author is, where the seat above is), never
  the rule.
- **Nothing surfaces outside the host by default.** Session names, ids and
  instance paths are local; nothing in this design writes them into a
  commit, a pull request or any GitHub surface.
- **Grantable only through a ceiling.** A new authority, if any, follows
  R3 and the ceilings: absent from every ceiling until a workspace adds
  it, so no workspace gains anything by upgrading niwa.
- **Cheap, local, bounded.** The check stays under the gate's 3-second
  bound and reads no more than it does today plus a fixed number of local
  files (PRD R33).

## Considered Options

### Decision 1: Per-point answers outside the bound state

A root coordinator, a person's own session and a session whose record is
lost all ask `niwa authority check` the same question a bound session
asks, and today get no `points`. niwa already has an answer for "no
record": `niwa authority decide` resolves the level a dispatch with no
`--level` would get (the resolved level ceiling from machine, overlay or
workspace config, else the built-in `autonomous`) and prints
`source=<layer> (no dispatch record)` (PRD R26). PRD R30 says a workspace
that sets nothing resolves every point to `session`. A dispatch's level
only narrows that ceiling, so a lost record's level is unknown but never
wider than the no-flag level; only `strict` is never wider than anything.

Both callers already ignore `points` outside `bound`: the gate's `jq`
reads only `.state` for any other state and shares one verdict between
`not-dispatched` and a failed lookup, and shirabe's posture reader
validates and uses points only when the state is `bound`.

Key assumptions:

- The gate and shirabe keep deciding non-bound states on `state` first, as
  both do today. A reader that applied the bound table to a non-bound
  answer anyway still lands on "not held" because `grant` stays `[]`.
- A session started by hand inside another dispatch's instance shouldn't
  get a wider answer than that instance's record gives.

#### Chosen: answer every state, by narrowing only

`--point` is answered in every state. A non-bound answer carries `points`
only when `--point` was given, so an answer asked without points is
byte-identical to today's.

- **`not-dispatched`.** The level is the no-flag level `decide` uses with
  no record. When the instance holds a dispatch record (a session started
  by hand in a worker's instance), the level is the narrower of the two,
  so `check` is never wider than `niwa authority decide -C <instance>`
  there; a record that exists but can't be read narrows to `strict`. The
  point then resolves through `Decide` with the same declarations seam a
  bound point uses. A `dispatcher` result becomes `person`: a session
  nobody dispatched has no dispatcher seat, the person who started it is
  the only seat above it (PRD R2 already calls the dispatcher of a
  dispatch run outside any session "the person at that shell"), and
  `person` is the stricter of the two. Author exclusion keeps the level's
  value.
- **`record-unusable`.** Every point is `decider: person`,
  `author_exclusion: on`, with no policy read. The lost record could have
  held any level, and nothing else answers without risking a wider one.
- **`source` per point.** Each point gains a `source` member, bound points
  too. For a bound point it's the level's source as `decide` prints it with
  a record. For `not-dispatched` it's `<layer> (no dispatch record)`,
  `decide`'s own string, or `<layer> (instance record)` when the
  instance's record narrowed it, with ` (no dispatcher)` appended when
  `dispatcher` became `person`. For `record-unusable` it's
  `record-unusable`. The `decider` and `author_exclusion` value sets don't
  change, and a non-bound answer never says `dispatcher`.
- **No record-shaped fields outside `bound`.** `level`, `level_source`,
  `grant_ceiling_source` and `parent` stay bound-only, so nothing reads a
  non-bound answer as if it came from a record.
- **Failure.** A policy that can't be loaded fails the lookup with exit 1,
  as `decide` and a bound lookup already do. Both callers read a failed
  lookup the way they read `not-dispatched`, so no verdict moves.

Points in a non-bound answer are routing facts. They never enter the
gate's bound table and never turn a step into a permit: they can only
choose where a refusal or a review goes.

#### Alternatives Considered

**Leave non-bound states without points.** Today's shape. Rejected:
every caller keeps its own "root goes to a person" rule, a second copy of
the level table that can drift from niwa's, and a root session in a
workspace that declared `supervised` can't learn that from the lookup the
gate uses.

**A fixed `person`/`on` for every non-bound state.** Rejected: it ignores
the level the workspace set, contradicts what `decide` prints for the same
directory and R30's "nothing changes for workspaces that set nothing", and
hard-codes yet another table.

**The no-flag level for `record-unusable` too.** One rule for both states.
Rejected: a dispatch's level only narrows the ceiling, so this can answer
wider than the lost record did.

**Report `dispatcher` as it is at the root.** Leave the narrowing to the
caller. Rejected: the answer would name a seat that doesn't exist, and
every caller would need the "a dispatcher with no dispatcher is a person"
rule.

### Decision 2: A teardown point

Point names are an open vocabulary (PRD R2), so `--point teardown`
already returns the level's answer; nothing gives that name a meaning.
No named authority covers teardown, and the PRD leaves the next authority,
teardown the likeliest, "to the work that gates it". The workspace gate
denies `niwa reap` and doesn't gate `niwa destroy` or `claude rm`.
shirabe reads teardown's settler from the permission rules and sends a
reserved teardown to a person.

Key assumptions:

- A later teardown authority, if one comes, will be named `teardown`,
  following the convention of one point named after each authority.
- Callers treat this point as narrowing only.

#### Chosen: name the point, not the authority

The guide documents `teardown` as a conventional point name meaning
"removing an instance or a session: `niwa destroy`, `claude rm` and
equivalents". It's an ordinary `points` entry, answered exactly like
`merge-pr` (bound or not, per Decision 1), with no code, schema or gate
change. Because no authority covers it, the answer is only the level's
half of the settle rule (PRD R4): a caller combines it with its own
permission reading and takes the stricter. `decider=session` never permits
a teardown on its own; `dispatcher` or `person` sends it there; and the
workspace's permission rules keep the final word, as they do for every
step today.

#### Alternatives Considered

**Neither point nor authority.** Rejected: callers keep inventing names
and keep their own level reading for teardown, which is the drift the
shared lookup exists to stop.

**The point plus a `teardown` authority with no gate.** Rejected: a grant
nothing enforces reads as permission that isn't there, and a fourth
authority value is a change every grant and ceiling reader has to accept.

**Point, authority and a gate rule for `niwa destroy`.** Rejected here:
it changes gate verdicts, which both PRDs leave to the work that gates
teardown. This design leaves that door open by fixing the point's name.

**A dedicated `teardown` field.** Rejected: it duplicates `points` and
special-cases one step.

### Decision 3: The target the answer names

A coordinator that learns who settles a step still can't send the step
anywhere. For `decider=dispatcher` the answer's `parent` is a key, and it
drops `parent` entirely when the dispatcher was a root session, though the
record keeps that session's id. For `decider=session` it names no seat
above the session, so the review a coordinator always sends its own
verdict to has no target from niwa. For author exclusion `on` it says who
may not settle without naming who that is.

What niwa holds locally: the record's `parent_session_id` is set whenever
the dispatcher ran inside an agent session that exposes its id, including
a root session at the workspace root, where the parent dispatch id is
empty; a plain shell leaves both empty. A dispatched dispatcher's session
mapping carries `session_name` when it was dispatched with a name. A root
dispatcher has no mapping, or a hook mapping with no name, so niwa holds no
address for it. `niwa list --json` takes an instance's `session_name` from
its newest mapping, which isn't always the session that ran the dispatch.
Every current reader of the check (the posture reader, the gate, the
release grant script) accepts new top-level members and new members inside
`parent` and inside a point, and rejects any new `decider` or
`author_exclusion` value.

Key assumptions:

- A root interactive session's id reaches the record, because the agent
  sets its session id variable for commands the session runs.
- The name niwa forwards at dispatch is the name a coordinator addresses
  that session by.
- No reader outside the three above parses the answer strictly.

#### Chosen: one description of the seat above, plus the asking session's identity

`points` keeps `{point, decider, author_exclusion}` (plus Decision 1's
`source`) with no new value. The answer gains facts that are the same for
every point:

1. **`parent` describes the dispatcher seat's original occupant**, and is
   emitted whenever the record names any dispatching session, not only a
   dispatched one:
   - `dispatch_id`: present when the dispatcher was itself dispatched,
     left out when it was a root session (it becomes optional);
   - `instance`: as today, from the parent's lineage entry while it exists;
   - `session_id`: the record's parent session id, whenever recorded;
   - `session_name` and `accepts_session_messages`: only when the session
     mapping keyed by that id exists, was written by a dispatch
     (`origin: dispatch`), names the same dispatch id as the record, and
     the name passes the shape check `niwa list` applies. Otherwise both
     are left out, so a root dispatcher never carries a name.

   `parent` is absent only when no dispatching session is on record (a
   person at a plain shell, or an agent that exposes no session id).
2. **A new top-level `session`** `{session_id, instance}` names the session
   the answer is about, in every state. It's labelled as that session,
   never as "the author": whose work a point concerns is the router's
   knowledge, not niwa's.
3. **The guide says what these facts are and aren't.** `parent` is the
   seat above as recorded at dispatch. A present `session_name` is the
   original occupant's name while its mapping exists, and proves nothing
   about whether that session still holds the seat. A router with its own
   successor chain (a coordinator's records) prefers that chain and can
   seed its first hop from `parent`. Nothing in `parent` or `session`
   says who qualifies to review or to settle.

A router reads it this way:

| Point says | Where a step or a review can go |
|------------|---------------------------------|
| `decider=session` | niwa names no reviewer. The seat above is `parent`, which a caller's own review rule may use; with no `parent`, nobody dispatched the session and the only seat above it is a person. |
| `decider=dispatcher` | `parent`: its `session_name` when present, else `session_id` resolved through the router's own records. A `parent` with an id and no name means "a session dispatched you and niwa has no address for it", never "a person". |
| `decider=person` | no target from niwa; the caller's own channel to a person. |
| author exclusion `on` | `session` is the asking session and `parent` the seat above; the router, which knows whose work the point is about, compares. |

Cost: one more bounded read on the bound path, the single mapping file
keyed by the record's parent session id, with no directory scan. That's
beyond R33's literal "no mapping except the one `--session` names" in the
same way grant resolution's read of a dispatcher's mapping already is, and
inside the gates design's bound. The gate reads none of the new members.

#### Alternatives Considered

**A per-point delivery target.** Each point carries a `deliver_to`, a
`review_by` seat for `decider=session`, and an `exclude` identity under
author exclusion. Rejected: `review_by` and `exclude` decide part of the
author-exclusion rule (the asking session isn't always the author; a
coordinator merges a worker's pull request), the target repeats one value
per point while nothing produces per-point overrides, and the posture
reader stores no points, so it would need a shirabe change the `parent`
members don't.

**Ids only.** Add `parent.session_id` and a top-level session id, with no
name and no added read. Rejected as the whole answer: the only id-to-name
join niwa offers returns an instance's newest session, not the
dispatch's, so routers other than shirabe get no reliable address. It
stays as the floor: ids are emitted whenever known, and the name is the
guarded extra.

**A separate resolver command, with the check unchanged.** Rejected: it's
a second place that binds a session and a second read of the record at a
different moment, against "one answer", to save one bounded file read. A
command that follows a seat's successor stays possible once niwa has a
supervision link.

### Decision 4: An authority over another lane's worker cap

PRD R3 defines each named authority by the actions it covers, from a set
fixed per niwa version; workspaces bundle authorities into roles but
can't invent new ones. A coordinator's active-worker cap is that
coordinator's own setting, kept in its own records and written by its own
tool, whose write path doesn't check who's calling. niwa neither stores nor
gates it, and can't read where it's kept without reading koto.

Key assumptions:

- niwa has no near-term plan to store or enforce a worker limit itself.
- A person, or the target coordinator's own dispatcher seat, changing a
  cap isn't the cross-lane case this question is about.

#### Chosen: no niwa authority; the cap stays the coordinator's

niwa defines no cap-change authority, and says so in the guide as a
deliberate non-goal. A cross-lane cap change is a request sent to the
coordinator that owns the cap, through the same delivery facts Decision 3
adds, and that coordinator decides it by its own rules. A caller that
refuses every cross-lane cap move stays correct. The question reopens
only if niwa itself comes to store or enforce a worker limit; then the
shape is the one R3 already prescribes for any new authority (a fourth
named authority defined by the commands a gate can see, absent from every
ceiling until a workspace adds it), not a special case.

#### Alternatives Considered

**A fourth named authority for cap moves.** Rejected: there's no action a
gate can see, a flat grant can't say which lanes it reaches, and it works
only if the target coordinator chooses to honour it, which makes it a
label rather than an authority.

**A general authority over another dispatch's run settings.** Rejected for
the same reasons, and it's too vague for R3's definition of an authority
by the actions it covers.

**Workspace-defined authorities.** Rejected: it breaks R3's closed set and
the way an older niwa reads a shared config.

**Derive it from lineage (a dispatcher may move its descendants' caps).**
Rejected: that's a rule about a relationship rather than an authority, it
rests on parent links that can dangle, and the lineage work it would
change is separate.

## Decision Outcome

The four answers fit into one shape: `niwa authority check` becomes the
place a router learns, for any session and any point, who settles it and
what niwa knows about where it can go, while every verdict stays where it
is.

Decision 1 fills `points` for every state, narrowing only, so a root
session gets `session` or `person` for each point from the same level
`decide` prints, and an unusable record gets `person`. Decision 2 gives
`teardown` a fixed meaning in that same list without inventing a grant
nobody gates. Decision 3 adds, once per answer, the seat above (`parent`,
now with a session id and, when niwa can vouch for it, a name) and the
asking session (`session`), which is what a coordinator needs to deliver a
step to its dispatcher, to send its own verdict for review even when the
point says `session`, and to tell itself apart from a reviewer, without
niwa saying who qualifies. Decision 4 keeps niwa's authority set at three
and routes a cross-lane cap change as a request through those same facts.

Everything added is a new member within `niwa.authority.check/v1`. No
`decider`, `author_exclusion` or authority value is new, the gate's table
and fallback are untouched, and no ceiling, role or default grant changes,
so a workspace gains nothing by upgrading niwa. Nothing new is written
anywhere: the answer is read on demand from local files, so session ids,
names and instance names stay on the host and never reach a commit, a pull
request or GitHub. niwa still reads nothing koto writes, and a caller
without niwa keeps its own rule.

### How this fits the shipped designs

- [DESIGN-authority-grants](current/DESIGN-authority-grants.md): the level
  table, the declarations seam (R27), the settle rule (R26) and the closed
  authority set (R3) are unchanged. `decide`'s no-record answer becomes
  the root answer of `check`, so the two commands agree. The record format
  doesn't change; `parent_session_id` was already kept.
- [DESIGN-gates-honour-grant](DESIGN-gates-honour-grant.md): the binding,
  the `bound` table and every fallback are unchanged. Its Key Interfaces
  table gains `session`, `points` outside `bound`, a per-point `source`,
  and `parent.session_id`, `session_name` and `accepts_session_messages`,
  with `parent.dispatch_id` becoming optional. Its posture-reader decision
  (Decision 6) still holds for verdicts; what it adds is that a reader may
  route by non-bound points.
- The PRD's Out of Scope items "finding and reaching" the dispatcher and
  "how a decision that goes to a person or a dispatcher reaches them" are
  partly answered: niwa now supplies the facts for reaching a dispatcher
  seat's original occupant. Following a takeover to the current occupant,
  and reaching a person, stay separate.

### What this leaves to the author-exclusion work

Author exclusion `on` means whoever settles a point may not be the session
that authored the work (PRD R2). The separate work that settles author
exclusion owns:

- which sessions count as someone other than the author at each level;
- who decides a point under exclusion, and whether a dispatcher seat that
  launched the author qualifies;
- how a reviewing seat's verdict comes to be recorded as that seat's;
- whether a review is required before a session settles a point that
  resolved to `session`, and who must give it.

This design supplies facts that rule will need (the asking session's
identity, the seat above, an address for it when niwa has one) and adds
nothing that answers any of the four. In particular, `parent` is never
labelled a reviewer and `session` is never labelled the author.

## Solution Architecture

### Overview

All of the change is inside `niwa authority check` and its guide. The
command keeps its flags, its binding, its exit codes and its schema
string. It answers `--point` in every state, adds a `source` to each point,
adds a top-level `session`, and widens `parent`. The guide documents the
`teardown` point name, what the delivery facts do and don't mean, and that
niwa defines no cap-change authority. The gate, the record, dispatch and
config don't change.

### Components

- **`internal/authority/check.go`.** `CheckNotDispatched` and
  `CheckRecordUnusable` take the asked points and their resolved answers,
  so a non-bound document carries `points` when asked. `CheckPoint` gains
  `Source`. `CheckParent` gains `SessionID`, `SessionName` and
  `AcceptsSessionMessages`, and `DispatchID` becomes `omitempty`.
  `CheckDoc` gains `Session *CheckSession` (`{session_id, instance}`).
- **`internal/authority/decide.go`.** One small helper narrows a
  `dispatcher` decider to `person` for an answer with no dispatcher seat
  and returns the source suffix. "The narrower of two levels" uses the
  `Level` order R1 already defines (`autonomous` < `supervised` <
  `strict`), the same comparison dispatch's ceilings use. `fromLevel`, the
  declarations seam and their order don't change.
- **`internal/cli/authority_check.go`.** This layer resolves every point
  and passes the answers into the constructors, at each of the call sites
  that build a non-bound document today; `internal/authority` stays free
  of config and file reads. On `not-dispatched` with points
  asked, it loads the policy the way `decide` does with no record, reads
  the instance's own record when one exists and narrows to it (`strict`
  when it can't be read), and resolves each point. On `record-unusable` it
  answers `person`/`on` without a policy read. On `bound` it fills
  `parent` whenever `ParentSessionID` or `ParentDispatchID` is set, and
  reads the one mapping keyed by `ParentSessionID`, keeping its name only
  when `origin` is `dispatch`, its `dispatch_id` equals the record's
  `ParentDispatchID`, and the name passes `niwa list`'s shape check
  (`dispatchSessionNameRe`); any failure there drops the name and keeps
  the id, never failing the lookup. In
  every state it sets `session` from `--session` and the name of the
  instance it resolved (the base name of the instance path, as `parent`
  already reports names). `session` echoes the request; it proves nothing
  about who is asking.
- **`CheckDoc.Text()`.** The plain output gains one `point:` line per
  point (`<point>: <decider>, author exclusion <value> (<source>)`) in
  every state, and, on `bound`, a `dispatcher:` line naming the parent's
  `session_name` when present, else its session id, else its dispatch id.
  It prints only values the JSON carries, so the same name guard applies.
- **`docs/guides/authority-grants.md`.** New entries for the non-bound
  points and their `source` strings, the `teardown` point and its
  narrowing-only use, the delivery facts and their limits, and the
  cap-change non-goal.

### Key Interfaces

A bound worker whose dispatcher was a dispatched coordinator, asked about
merge and teardown:

```json
{
  "schema": "niwa.authority.check/v1",
  "state": "bound",
  "binding": "mapping",
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "session": {"session_id": "5b1e0c9a-2d7f-4e3a-9c41-7a0e8f6d2b13", "instance": "ws+worker-7c1d9e2a"},
  "parent": {
    "dispatch_id": "9f0c2a11-77b2-4d0e-8a51-0c1d2e3f4a5b",
    "instance": "ws+coordinator-1a2b3c4d",
    "session_id": "e0a4d8b2-61c3-4f0e-b7a9-3d2c1f5e8a40",
    "session_name": "plugin_coordinator-1a2b3c4d",
    "accepts_session_messages": true
  },
  "grant": [{"authority": "merge-pr", "sources": ["role:coordinator"]}],
  "level": "supervised",
  "level_source": "dispatch",
  "grant_ceiling_source": "workspace",
  "points": [
    {"point": "merge-pr", "decider": "dispatcher", "author_exclusion": "on", "source": "dispatch"},
    {"point": "teardown", "decider": "dispatcher", "author_exclusion": "on", "source": "dispatch"}
  ]
}
```

A root session in a workspace that declares `supervised`:

```json
{
  "schema": "niwa.authority.check/v1",
  "state": "not-dispatched",
  "reason": "no-mapping",
  "session": {"session_id": "a7f3c2e1-0b9d-4c5a-8e6f-1d2c3b4a5e6f", "instance": "ws+root-0f1e2d3c"},
  "grant": [],
  "points": [
    {"point": "merge-pr", "decider": "person", "author_exclusion": "on", "source": "workspace (no dispatch record) (no dispatcher)"}
  ]
}
```

| Member | Present | Meaning |
|--------|---------|---------|
| `session` | always | the session the answer is about: `--session` and the instance niwa resolved it to. Not "the author". |
| `points` | whenever `--point` was given | `bound`: as today. `not-dispatched`: from the no-flag level, narrowed by the instance's record, `dispatcher` read as `person`. `record-unusable`: `person`/`on`. |
| `points[].source` | with every point | where the answer came from (see Decision 1). |
| `parent` | `bound`, when the record names a dispatching session | the dispatcher seat's original occupant. |
| `parent.dispatch_id` | when the dispatcher was dispatched | now optional. |
| `parent.session_id` | when recorded | the dispatching session's id. |
| `parent.session_name`, `parent.accepts_session_messages` | when its dispatch-written mapping exists and matches | the name that session answers to, and whether it took session messages at launch. |

No `decider`, `author_exclusion`, `state`, `binding` or `reason` value is
new. The plain-text output gains one line per point and, when present, a
`dispatcher:` line naming the seat's session name or id.

### Data Flow

A coordinator at a finishing step calls `check` once with the points it
needs (`merge-pr`, `close-issue`, `cut-release`, `teardown`). From the
answer it reads, per point, whether the session settles; when it doesn't,
where the step goes (`parent` for `dispatcher`; its own channel for
`person`); and, for its own work, the seat above to review it (`parent`,
or a person when there's none). It still asks its own permission reading
and takes the stricter, and for a bound session the gate still decides by
the grant. When niwa is missing or the lookup fails, it uses its own rule
exactly as today.

The gate calls `check` exactly as today and reads only `state`, `grant`
and the bound `points`; nothing new reaches its verdict.

## Implementation Approach

### Phase 1: answer points in every state (niwa)

The non-bound answer, the per-point `source`, the `dispatcher`-to-`person`
narrowing and the top-level `session`. Unit tests for each state's table,
for the instance-record narrowing, and that an answer asked without
`--point` stays byte-identical to today's.

Deliverables:
- `internal/authority/check.go`, `internal/authority/decide.go`
- `internal/cli/authority_check.go`
- tests beside each

### Phase 2: the delivery facts in `parent` (niwa)

`parent` for a root dispatcher, `parent.session_id`, and the guarded
`session_name` and `accepts_session_messages`. Tests for each guard (no
mapping, hook origin, a different dispatch id, a bad name) and a check
that the gate's fixture answers still parse. It touches `CheckParent`
and the bound path, not the non-bound constructors, so it can land
before or after Phase 1.

Deliverables:
- `internal/authority/check.go`, `internal/cli/authority_check.go`
- tests beside each

### Guide entries land with their code

Each phase carries its own `docs/guides/authority-grants.md` entries, so
the guide never documents behaviour niwa doesn't have: Phase 1 adds the
non-bound points, the `source` grammar and the `teardown` point with its
narrowing-only use; Phase 2 adds the delivery facts, their limits and the
note that the output is host-local. The cap-change non-goal goes in with
whichever lands first.

### Follow-up outside niwa

shirabe's posture reader can then route by a non-bound point instead of
its own root rule, ask the `teardown` point and take the stricter of it
and its permission reading, and use `parent.session_name` as the first hop
to a dispatcher seat before its own successor chain. That's shirabe's
change, on its own schedule, and it keeps its own rule whenever niwa is
missing or the lookup fails. The workspace gate needs no change.

## Security Considerations

This design adds read-only facts to a local lookup. It writes nothing,
changes no gate verdict and grants nothing.

- **Never more permissive.** No new `decider`, `author_exclusion` or
  authority value exists; non-bound points narrow (the no-flag level, then
  the instance record, `strict` on an unreadable record, `person` in place
  of a missing dispatcher, `person`/`on` for a lost record), and both
  callers keep deciding non-bound states on `state` with `grant: []`. A
  caller that misreads a non-bound point as permission still finds no
  grant.
- **Routing facts aren't permits.** A non-bound `decider=session` says no
  seat above has to settle the point; it never lets the session take the
  action, which still needs a grant and the gate. `teardown` has no
  authority behind it, so its answer only narrows a caller's permission
  reading. Tests pin both: for every level, a non-bound point is never
  wider than `niwa authority decide` for the same directory, and an
  unreadable instance record answers `strict`.
- **Trust in the delivery facts.** The record and the mappings are files
  the session's own user can edit (PRD Out of Scope, unchanged here). The
  dispatch-origin and dispatch-id guards keep a hand-started or later
  session in the parent's instance from being named as the dispatcher by
  accident; they don't stop someone who can write `.niwa/` files, and a
  forged `session_name` can misdirect a request, not grant anything: the
  recipient still decides with its own authority. One case is worth naming:
  a caller that sends its own work for review to `parent` could have that
  review pointed at a seat the editor controls. Nothing here catches that;
  the author-exclusion work decides who may review, and until it does this
  stays the accepted residual risk of editable records. Likewise a bound
  worker that deletes its own mapping falls to `not-dispatched` and gets a
  non-bound answer, which is never wider than `decide` for that directory
  and still holds no grant.
- **Handling the mapping read.** The record's parent session id is read
  with the record's own id check, but the mapping path is built only from
  an id that passes the lowercase-UUID check `--session` already uses. Any
  failure on that path (an id of another shape, a missing or unreadable
  file, a hook origin, a missing or different dispatch id) leaves out
  `session_name` and `accepts_session_messages`, keeps `session_id`, and
  never fails the lookup. A name is kept only when it matches the pattern
  dispatch writes and `niwa list` checks
  (`^[a-z0-9](?:[a-z0-9_]{0,38}[a-z0-9])?-[0-9a-f]{8}$`), which admits no
  control or escape characters. A router still treats the name as data.
- **Stale addresses.** A name can outlive the seat it names when a
  coordinator is replaced and its instance kept. The guide says a name
  proves nothing about who holds the seat now, and a router with its own
  successor chain uses that first.
- **Data exposure.** Session ids, session names and instance names are
  already readable through `niwa list --json` and `niwa lineage show` by
  the same user. The answer is printed only to the caller; nothing in this
  design writes it to a commit, a pull request body or any remote surface,
  and the guide says the output is host-local and isn't for pasting into
  commits, pull requests or issues.
- **Cost.** At most one more mapping file read on the bound path and a
  fixed set of config files plus one record on the non-bound path when
  points are asked, all local, inside the gate's 3-second bound.

## Consequences

### Positive

- A root session, a person's session and a session with a lost record
  learn who settles each step from the lookup the gate uses, and callers
  can drop their own copies of the level table.
- `teardown` has one documented name and meaning, ready for the work that
  gates it.
- A coordinator gets an address for its dispatcher seat when niwa has one,
  and a seat above for its own review even when it settles a step itself.
- Root dispatchers become visible as a seat with an id, separate from a
  person at a plain shell; today both read as "no parent".
- The authority set stays at three, and the cap stays where it's enforced.

### Negative

- The bound path reads one more local file.
- A `session_name` can name a coordinator that has since been replaced.
- A broken workspace config turns a non-bound points answer into a failed
  lookup.
- The `source` grammar grows by two suffixes and one fixed value.
- A cross-lane cap change has no niwa grant to show, only a request.
- `teardown` gets a level answer but no grant, so its final word stays
  with the permission rules.

### Mitigations

- The added read is one file keyed by id, with no scan, inside the bound.
- The guide states the name's limit, and routers prefer their own
  successor chain.
- A failed lookup lands on the same verdict as `not-dispatched` in both
  callers, so a broken config costs routing, not safety. Routing then
  falls back to the caller's own rule without saying so; the lookup's
  exit 1 is the signal, and `niwa authority decide` in the same directory
  shows the config error.
- The guide documents every `source` string; readers already accept new
  members.
- The decision to add a cap-change authority reopens, in R3's existing
  shape, if niwa ever stores or enforces a worker limit.
- A `teardown` authority can be added later by the work that gates
  teardown, under the name this design fixes.
