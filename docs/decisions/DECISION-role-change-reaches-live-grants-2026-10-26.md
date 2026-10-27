---
status: Accepted
decision: |
  `niwa authority check` resolves a bound session's grant when it runs,
  instead of reading the list frozen into the record at dispatch. Each
  authority a role contributes comes from the role as the workspace declares
  it now, each `--grant` authority stays as recorded, and every one of them is
  kept only inside the current grant ceiling and inside the dispatcher's
  grant, itself resolved the same way up the lineage. The never-wider check at
  dispatch caps a child by the same resolution of its dispatcher. A
  dispatcher whose instance has been destroyed is passed over to its own
  dispatcher; one that can't be established or read otherwise caps the
  session at its own recorded grant, and a policy that can't be loaded fails
  the lookup, so no failure widens a grant. The record format doesn't change.
rationale: |
  A frozen list carries a widened role to no live session and passes it to no
  successor, and it keeps a narrowed role's authority live in every record
  until each session is destroyed. Resolving on read makes a narrowing take
  effect on the next lookup without anyone acting, which a refresh command or
  an apply-time rewrite can't promise. Every gate in use asks `check`, so
  resolving there reaches all of them, and the lookup stays a handful of
  local file reads.
---

# DECISION: a role's change reaches live grants through the lookup

## Status

Accepted

## Context

Dispatch resolves `--grant` and `--role` into an authority record written
once into the instance. Before this change `niwa authority check` read that
record's `grant` list and loaded no config, and the never-wider check at
dispatch compared a child's request with its dispatcher's recorded list.

So widening a role reached nothing already running. A coordinator dispatched
with `--role coordinator` before `cut-release` joined the role kept
`merge-pr, close-issue`, and a successor it dispatched with the same role was
refused as wider than its dispatcher, for good. Narrowing had the same flaw in
the dangerous direction: a role that lost an authority kept it in every live
record. The workspace's call to keep `cut-release` on the coordinator role and
narrow it later depends on a narrowing taking effect.

The gates that read a grant today (the workspace's pre-tool-use gate, the
release skill's grant check, the coordinator's posture read) all call
`check`. `niwa authority show` prints the record document, which is also the
saved-copy format the guide recommends before destroying an instance.

Lineage entries outlive their instances: destroy, reset and reap leave them,
and only a dispatch's own rollback removes one. An entry names its instance by
directory name under the workspace root, so a dispatcher's record is found by
dispatch id through its entry and checked to name that dispatch. Session
mappings and records go with the instance.

## Decision

`check` loads the current policy (the workspace config, the overlay and the
machine config, as `niwa authority policy` does) for a session it binds, and
answers with the record's grant resolved against it:

- An authority with a `role:<name>` source is held through that role only
  while the role still declares it. A role the record names that now declares
  more contributes the new authorities too. A role no longer declared
  contributes nothing.
- An authority with a `dispatch` source (from `--grant`) stays.
- Every authority is then kept only inside the current grant ceiling and
  inside the dispatcher's current grant.

The dispatcher's current grant is resolved the same way from its own record,
recursively. The dispatchers of a record are its parent dispatch (from the
record's parent link, or, for a dispatch from the workspace root, the
dispatch its lineage entry says the calling session's mapping named) and, for
a `--parent-from` dispatch, the dispatch the calling session's own mapping
names. A record with no parent dispatch and no mapped calling session has no
dispatcher, and only the ceiling caps it.

A dispatcher whose instance directory no longer exists (destroyed or
reaped, its lineage entry kept) can't act any more, so it's passed
over: its own dispatchers, found through its lineage entry, cap the child
instead. Without this, a coordinator whose predecessors have been reaped
would stay at its recorded grant for good, which is the lineage the issue is
about. A session mapping that's gone caps nothing for the same reason: niwa
removes mappings only when it removes their instances.

Failures fail narrow:

- A dispatcher that can't be established or read otherwise (a missing or
  unreadable lineage entry, an instance that exists without a usable record
  of that dispatch, a record and an entry that name different parents, a
  cycle, a chain deeper than the lineage walk's limit, or evidence that the
  dispatch ran inside an instance that had lost its record) caps the session
  at its own recorded grant. It can still narrow; it can't widen.
- A policy that can't be loaded fails the lookup: `check` exits 1 with
  nothing on stdout, which every reader already treats as holding nothing.

The never-wider check at dispatch uses the same resolution for the
dispatcher's grant, against the policy the dispatch resolved, so a lineage
started before a widening can pass the new authority down once its
dispatcher holds it.

The level isn't re-resolved; it stays as recorded. `show` and `niwa list
--json` keep printing the record as stored.

## Alternatives considered

**An explicit `niwa authority refresh`.** It would re-resolve a lineage's
records top-down after an apply and rewrite them. Rejected because a narrowing
stays live until someone remembers to run it, a partial run leaves a lineage
half old and half new, and it ends the record's promise of never being
rewritten.

**Rewriting records at `niwa apply`.** The same write-back shape, run by
apply. Rejected for the same reasons, and because config can change on disk
without an apply (a pulled overlay, an edited machine config).

**Keeping records frozen.** A grant would change only by a new dispatch.
Rejected because widening a coordinator lineage would need a fresh dispatch
from outside it, and narrowing would need every live session holding the
authority destroyed and redispatched: the narrowing the workspace relies on
would never happen in practice.

**Resolving in `show` as well as `check`.** It would make the guide's
by-instance reference checks follow a change too. Rejected for now: `show`'s
document is the record as stored and the saved-copy format, no gate in use
reads a grant through it, and the by-instance model can't tell the dispatched
session from a hand-started one anyway. The guide points gates at `check`.

## Known limits

A dispatch made inside an instance that had lost its record was capped at
nothing, so it was granted nothing. When it ran with no calling-session id,
its record and lineage entry look the same as a dispatch with no dispatcher,
and a role it named that was empty then and is widened later reaches it. Every
other such dispatch is told apart by its calling session's link and capped at
its recorded grant.

The whole lineage is resolved against the policy of the workspace the lookup
runs in, not each ancestor instance's own overlay.

Passing over a destroyed dispatcher trusts that its instance directory is
gone because niwa removed it. A session can delete another instance as the
operator, as it can edit any record; the record was never tamper-proof, and
this doesn't change that.

## Consequences

A widened role reaches the sessions it was granted to on their next `check`,
within the ceiling and within each dispatcher's current grant, and their
successors can be dispatched with it. A narrowed role, a narrowed ceiling or a
removed role takes effect on the next `check` and at the next dispatch, with
no command to run. A session whose dispatcher's instance has been destroyed
is capped by the dispatchers above it, so a coordinator lineage keeps
following the role after its earlier members are reaped.

`check` now reads config, so a config or overlay that can't be loaded makes
every bound session's lookup fail until it's fixed, which gates read as not
held. The lookup reads a few more local files (the config, the overlay, a
lineage entry and a record per ancestor), with no network call.

## References

- niwa issue #433
- `docs/guides/authority-grants.md`
