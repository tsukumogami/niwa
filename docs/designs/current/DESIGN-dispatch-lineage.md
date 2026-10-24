---
schema: design/v1
status: Current
problem: |
  niwa's only parent links for a dispatched session live in the instance's
  authority record and the workspace's session mapping, and `niwa reap`
  deletes both. Neither names the person a session acts for, and the parent
  session id is copied from a caller-controlled variable with no mark saying
  whether niwa checked it. A durable, dispatch-keyed record has to be written
  at dispatch time, when the evidence for each link still exists, and read
  back long after, including for workers that coordinators dispatch from the
  workspace root.
decision: |
  A new leaf package, `internal/lineage`, owns the entry format, its
  validation and a pure resolver that turns what dispatch already reads at
  step (2e) into parent links with sources and a principal; a worker
  dispatched from the workspace root links to its coordinator through the
  session mapping its caller names, marked `mapped`. One JSON file per
  dispatch lands in `<workspace root>/.niwa-lineage/`, outside the swapped
  configuration directory, at step (8a), created beside the authority record,
  removed by the same rollback, and completed once with the worker's session
  id by a field-preserving rewrite as soon as step (10) captures it. A `niwa
  lineage` command group reads it; the authority record gains the same
  parent session source and principal as lenient optional v1 fields.
rationale: |
  Resolving at (2e) keeps every input in one place, at the one moment the
  dispatching instance's mapping and the parent's entry are reliably present,
  and a pure resolver makes the PRD's source tables directly testable. A
  store outside the configuration directory is untouched by the copy-and-swap
  every dispatch's refresh performs, so concurrent dispatches can't lose each
  other's entries. File-per-dispatch needs no locking and lets a reader skip
  one bad entry. Writing at (8a) is the only point where a write failure can
  still refuse before a worker runs. A separate command group keeps the
  authority read contract, which gates depend on, unchanged in meaning.
upstream: docs/prds/PRD-dispatch-lineage.md
decision_provenance: inline-resolved
---

# DESIGN: Dispatch lineage

## Status

Current

Revised after review: the store moved out of the configuration directory,
root dispatches gained the `mapped` parent link, the entry format became
explicitly additive, the principal became opt-in with its trust capped by
both links, and watch reviews were deferred.

## Context and Problem Statement

`niwa dispatch` already reads everything a lineage record needs, but it reads
it into places that don't last. At step (2e), `dispatchParent`
(`internal/cli/dispatch_authority.go`) finds the dispatching instance through
`--parent-from` or the working directory and reads that instance's authority
record for the parent dispatch id. At step (4), `lineageParentSessionID`
(`internal/cli/dispatch_lineage.go`) reads the calling session's id from the
variable the agent's launch spec names (`CLAUDE_CODE_SESSION_ID`). At (8a)
both go into the new instance's `.niwa/authority.json`, and at (11) the
session mapping `<root>/.niwa/sessions/<session id>.json` repeats the
dispatch id and the parent session id. Reap removes the instance directory
and then the mapping (`internal/cli/reap.go`, `internal/cli/on_reap.go`), so
both copies are gone. The rollback paths and the unmapped backstop drop them
too.

The PRD asks for a record that survives all of that, keyed by dispatch id,
holding for each dispatch the instance, the agent, the origin, the worker's
session id once captured, two parent links each with a source, and a
principal with a source and a trust value. The technical problem has five
parts.

**Where the record lives and how it is written.** It has to sit at the
workspace root outside every instance, survive every reap path, `niwa
destroy` of an instance, `niwa reset` and every configuration refresh,
including refreshes run by concurrent dispatches, and go away only with the
workspace (PRD R10, R11). It must be written before the worker starts, so a
write failure refuses the dispatch the way an authority record write failure
does, and it must not exist for a dispatch that removes its instance (R2,
R3). The worker's session id isn't known until step (10) captures it, and
capture can fail; a kept foreground instance with no captured session still
has an entry (R2). Entries are 0600 in a 0700 directory, written atomically,
and at most 4 KiB (R23, R24).

**How each link's source is decided.** The parent session link's source is
`none` when the variable is unset or not a well-formed id; `handed` when
`--parent-from` was given, when the working directory is in no instance, or
when that instance has no session mapping; `observed` when the instance's
newest mapping (latest recorded creation time, ties to the lower session id,
the rule `NewestMappingPerInstance` already implements) names the same id;
and `unverified`, with the mapping's id recorded beside the claim, when it
names a different one (R4). The parent dispatch link is `observed` when the
working directory's instance has an authority record, `handed` when
`--parent-from` found one, `mapped` when the working directory is in no
instance and a session mapping keyed by the variable's value carries a
dispatch id, and `none` otherwise (R5). The `mapped` row exists because
coordinators dispatch from the workspace root; without it every real tree is
one level deep. Nothing is refused for any source (R6).

**Where the principal comes from.** It is opt-in. It is inherited from the
parent dispatch's entry when the parent link has an id and that entry is
readable, with trust `observed` only when the parent dispatch link is
`observed`, the parent session link is not `unverified`, and the parent's own
principal trust is `observed`. Otherwise it is the machine's declared
`[global] principal` (1 to 128 letters, digits, `.`, `_`, `-`, `@`, `+`),
else empty with source `none`; either fallback has trust `handed` when a
parent dispatch link has an id, since the person should have come from that
parent. A readable parent whose principal is empty is inherited as empty. An invalid declared value refuses every dispatch. The
principal never comes from a token, a keyring, a login, the OS account or the
network, and no gate may read it (R7, R8, R9).

**How the format grows.** Within `niwa.lineage.entry/v1` a reader ignores
fields it doesn't know and keeps them on rewrite; unknown source, trust and
origin values are accepted when they are name values (1 to 32 lowercase
letters, digits or `-`); and choosing which files are entries goes by name,
file type and size, never by whether the content parses (R16).

**How it is read.** `niwa lineage show` selects one entry (by a positional
dispatch id, `--session`, `--instance`, the working directory or `-C`, the
chain starting from the latest `written_at`, ties to the lower dispatch id,
with every match reported) and walks up through parent dispatch ids of any
source with fixed stop rules (root, not on record, unreadable, later version,
cycle, 64 levels), printing a versioned `niwa.lineage.chain/v1` document
under `--json`; `niwa lineage list` prints every entry (R13 to R17). In
`parent_session.source`, `parent_dispatch.source` and `principal.trust`, only
`observed` is observed, and other values print with `(not observed)` (R18).
The authority record carries the same parent session source and principal
while the instance lives (R19). All of it works with no other tool installed,
offline, with the principal kept off telemetry and lineage never carried by a
session export or import (R12, R20, R21), and the lineage guide states the
trust boundary (R22). Dispatch argv, settings, telemetry and existing
refusals don't change (R25).

## Decision Drivers

- **Capture evidence when it exists.** The mapping that makes a session link
  observable, the mapping that links a root-dispatched worker to its
  coordinator, and the parent entry a principal is inherited from can all be
  gone by the time anyone reads the record, so the sources must be decided
  and stored at dispatch time.
- **Refuse only before launch.** A lineage write that fails after the worker
  starts can't stop it; the write has to happen where the authority record's
  write already sits, inside the same rollback.
- **Stay out of every directory something else rewrites.** Every dispatch
  provisions through `ReconcileAndReloadConfig`
  (`internal/cli/instance_from_hook.go`), whose snapshot writer copies the
  configuration directory's carried state into staging and swaps
  (`internal/workspace/snapshotwriter.go`). Anything written into that
  directory between another dispatch's copy and its swap is lost.
- **Don't widen any existing contract.** `niwa authority show --json` is what
  gates read to decide whether an authority is held. Its meaning and its
  fail-closed reading of later versions must not change, and the session
  mapping stays keyed and deleted exactly as today.
- **Parallel dispatches don't coordinate.** Several dispatches in one
  workspace can write at once; the store needs no lock.
- **One bad entry costs one entry.** A same-user process can edit the store,
  so every read validates, and a reader skips or reports a bad entry without
  losing the rest.
- **Grow without a version bump.** A later field, such as an audit record of
  the grant, must arrive without older readers losing data.
- **Testable resolution.** The PRD gives the source rules as tables; the code
  that applies them should be a pure function a table test can cover row by
  row.
- **No other tool, no network.** Nothing in the write or read path calls out,
  and nothing reads another tool's files.
- **Small, fixed footprint.** One entry is under 4 KiB, and a workspace that
  never dispatches gets no directory.

## Considered Options

### Decision 1: Where entries live and how they are stored

Resolved inline.

The store has to outlive every instance, survive the configuration
refreshes concurrent dispatches run, accept concurrent writers, and let one
entry be completed later with a session id. Everything under
`<root>/.niwa/` is the configuration directory the snapshot writer copies
and swaps on each refresh, and every dispatch runs a refresh.

Key assumptions:
- `EnumerateInstances` (`internal/workspace/state.go`) counts only child
  directories holding `.niwa/instance.json`, and `niwa destroy --workspace`
  removes the whole workspace root, so a sibling directory at the root is
  neither mistaken for an instance nor left behind.
- The dispatch id is a fresh version-4 UUID, so two dispatches colliding on a
  file name is not a case the write has to win a race on; checking for an
  existing name first is enough to refuse a duplicate.
- Entry counts stay modest. A coordinator dispatching 20 or more workers a
  day passes 10,000 entries a year; `show --session`, `show --instance` and
  `list` read every file, which is fine at that size, and a later `prune
  --before` can trim it, because the walk already handles a missing
  ancestor.

#### Chosen: one JSON file per dispatch in `<root>/.niwa-lineage/`

Each entry is `<root>/.niwa-lineage/<dispatch id>.json`, one JSON object,
mode 0600, in a directory created 0700 on first write; every write also
sets an existing directory to 0700, which is stricter than doing it once per
process and keeps no per-process state. The dispatch id is a
UUID, so the file name needs no escaping, and every read requires the
`dispatch_id` inside a file to equal its name, so a copied or renamed file is
unreadable rather than read as a different dispatch. Writes follow
`WriteAuthorityRecord`: an `Lstat` of the final name refuses if anything is
there, then a temporary file named `.<dispatch id>.<random>.tmp` is opened
`O_CREATE|O_EXCL|O_NOFOLLOW` at 0600 in the same directory, written, synced
and renamed into place. No refresh, reap or destroy of an instance touches
the directory; only destroying the workspace removes it.

#### Alternatives Considered

**Inside the configuration directory, carried across refreshes by a
`preserveLineage` step.** The pattern `sessions/` uses. Rejected because the
snapshot writer copies carried state to staging and then swaps: an entry a
second dispatch writes or completes between the first dispatch's copy and
its swap disappears, and with no pruning the copy, and its window, grows with
every dispatch.

**An append-only JSON Lines log at the workspace root.** One line per event,
the session id as a second line. Rejected because parallel dispatches would
append to one file (atomic only for small writes on some filesystems), a
reader would have to join two lines per dispatch, and one corrupt line would
make every later reader decide how much of the file to trust.

**Keep the authority record by moving it out of the instance at reap.** Reap
already runs code before deleting the instance. Rejected because the
backstop reap, the rollbacks and a SIGKILL mid-reap never run that code, so
the record would be lost on exactly the paths the PRD lists, and reap would
become a writer of a durable record.

**Exclusive creation by hard link.** Write the temporary file, then `link` it
to the final name, which fails if the name exists, closing even a same-user
race. Rejected because nothing else in niwa uses hard links, a filesystem
without them would refuse every dispatch, and the race it closes needs two
dispatches minting the same UUID.

### Decision 2: Where and how the links and the principal are resolved

Resolved inline.

The source rules read five things: the session variable, whether
`--parent-from` was given, the dispatching instance and its newest mapping,
the mapping keyed by the variable's value when the dispatch runs at the
root, and the parent dispatch's own lineage entry. Step (2e) already reads
most of these: it calls `dispatchParent`, which runs `FindInstance` and reads
the parent's authority record, and it refuses before anything exists.

Key assumptions:
- The session mapping store is readable at (2e). A mapping that can't be
  listed or read counts as no mapping, which makes the session link
  `handed` and the root fallback `none`, never `observed` or `mapped`.
- An unreadable parent lineage entry is not a refusal: the PRD falls back to
  the declared principal and keeps the parent link.

#### Chosen: resolve at (2e) through a pure resolver in `internal/lineage`

`dispatchParent` gains one return value, the working directory's
`workspace.FoundInstance` (the zero value when the directory is in no
instance), which it already computes. `resolveDispatchAuthority` then
gathers a `lineage.Inputs` value at (2e): the session variable's value (read
once there, then reused for the telemetry attribute and the authority
record); whether `--parent-from` was set; whether the working directory was
in an instance; that instance's newest mapping session id (read only when
`--parent-from` was not given: `ListSessionMappings`, then
`NewestMappingPerInstance`, looked up by `filepath.Clean` of the instance's
root, the key that function already uses); when the working directory is in
no instance and `--parent-from` was not given, the dispatch id of the
mapping keyed by the variable's value (`ReadSessionMapping`, only when the
value is a lowercase UUID as that store requires; any other well-formed
value skips the lookup, leaving the dispatch link `none`); the parent
dispatch id and how it was found; the parent's lineage
entry (read through the store by whichever parent dispatch id resolved); and
the declared principal. `lineage.Resolve(Inputs)` applies the PRD's tables in
row order and returns the values the entry and the authority record both
store. It does no I/O, so a table test covers every row, including the
`mapped` row, the trust caps and the empty-inherited principal.

With `--parent-from`, the caller chooses which instance's record is the
parent, so it also chooses which principal is inherited; the link is
`handed`, and so is the principal's trust. With a `mapped` parent, niwa
found the coordinator's mapping through the id the caller named, so the
principal inherited through it is `handed` too.

The declared principal is read from `[global] principal` in the personal
config (`internal/config/registry.go`) and from nowhere else: no workspace or
overlay key, no OS account, no credential. It is validated at load beside
`authority_level` and `authority_ceiling`, and `checkMachineAuthority` at
(2a) refuses an invalid value with the key and the file named.

#### Alternatives Considered

**Resolve at (8a), next to the record write.** Everything is still readable
then. Rejected because it splits the parent reads from (2e), which already
reads the parent record and may refuse, and it delays the evidence read past
provisioning, which can take minutes, during which a reap could remove the
mapping that would have made a link observed or mapped.

**Store the raw inputs and decide sources when the record is read**, joining
a root-dispatched worker to its coordinator on session ids at read time.
Rejected because the mappings that decide the source and the join are
exactly what reap deletes: by read time the answer is always `handed`, and
the join has nothing left to join on.

**Match the session against any mapping for the instance, or against the
mapping carrying the parent's dispatch id.** Any mapping would accept a
session that left the instance long ago; matching by dispatch id would miss a
session the SessionStart hook mapped. Rejected in favour of the newest
mapping, the rule the PRD defines and `niwa list` already uses.

**Fall back to the OS account name for the principal.** Every entry would
name someone with no setup. Rejected because it records a name without the
user choosing to, and a worker can read its own principal and pass it to its
model provider.

### Decision 3: When the entry is written, completed and removed

Resolved inline.

The PRD wants an entry to exist exactly when the dispatch keeps its instance,
written before the worker starts, completed once with the session id, and
otherwise unchanged, with fields this niwa doesn't know kept through the
completion.

Key assumptions:
- The rollback armed at (7) is the only path by which a dispatch removes its
  instance after (8a); SIGKILL leaves an unmapped instance for the backstop.

#### Chosen: create at (8a), complete at capture by a field-preserving rewrite, remove inside the rollback

At (8a), after `writeAuthorityRecordFunc` succeeds, dispatch calls
`workspace.CreateLineageEntry(root, entry)`. A failure, an existing entry for
the id included, returns a refusal and the rollback runs. Only when the
create succeeds does dispatch set a local `entryCreated` flag, and the
rollback closure armed at (7) gains one line: when `success` is false and
`entryCreated` is true it removes the entry for this dispatch id as well as
the instance. A refusal after (8a), a launch failure and a detached capture
failure therefore leave no entry, and a create that failed because the id
was already taken never deletes the entry it collided with. Today `success`
is always set before any path could remove a completed entry; the rollback
closure carries a comment saying so, so a later change that reorders the
steps doesn't start deleting completed entries.

Completion runs right after (10) captures the session id and before (11)
writes the mapping: `workspace.CompleteLineageEntry(root, dispatchID,
sessionID)` reads the stored document as an ordered list of raw JSON
members, refuses one of a later version or one that already has a
`session_id`, inserts `session_id` after `written_at`, and writes the
members back, every other one byte for byte, through a temporary file and a
rename. It never re-marshals a fixed struct, so a field a later niwa added
survives an older niwa's completion. Running before the mapping write means a
foreground dispatch whose mapping write then fails, which keeps its
instance, still has its session id recorded. A completion failure prints one
warning naming the entry and the dispatch goes on. The foreground path whose
capture finds no session keeps its entry with no session id, which is what
R2 asks for. A SIGKILL between (8a) and the rollback leaves an entry whose
instance the backstop later removes; that entry stays, with no session id,
which reads correctly as a dispatch that launched or tried to.

#### Alternatives Considered

**Write the entry once, after capture at (11).** One write, no completion
step. Rejected because a write failure after launch can't refuse a worker
that is already running, which breaks R3, and a kept foreground instance
with no captured session would get no entry.

**Keep the session id in a second file beside the entry.** Each file would be
written exactly once. Rejected because every reader would join two files, a
missing second file would be ambiguous (not captured, or lost), and the PRD
describes one entry per dispatch.

**Complete by parsing into the `Entry` struct and marshalling it again.**
Simplest code. Rejected because it silently drops any field this niwa
doesn't know, which breaks the additive rule the moment a later niwa writes
one.

### Decision 4: How lineage is read, and what the authority record gains

Resolved inline.

Readers come in two kinds: people and tools looking back at a reaped tree,
and gates checking a running session through `niwa authority show`. The
authority record is format `niwa.authority.record/v1`, additive within the
version, with `v1Optional` (`internal/authority/record.go`) for fields added
after the first release; a later version is read as holding nothing.

Key assumptions:
- Gates only read `grant`, `grant_ceiling_source` and `record`; new fields in
  the authority record don't change any gate's verdict.

#### Chosen: a `niwa lineage` command group, and lenient additive v1 fields

`internal/cli/lineage.go` adds `niwa lineage show` and `niwa lineage list`.
`show` takes a positional dispatch id, unlike `niwa authority show`, or
`--session`, `--instance` or `-C`, with the same usage-error and path rules
`niwa authority show` applies (exit 2 for two selectors or `-C` with
another, exit 1 for an empty, relative or nonexistent `-C`). With no
selector or `-C`, it finds the instance with `FindInstance`, reads its
authority record's dispatch id, and falls back to the entries for that
instance name, printing `selected by instance name` when it does, since a
reused instance name can match an older dispatch. Whenever a selector
matches more than one entry, `show` walks from the latest and reports all of
them: `<n> entries matched; showing the latest` and the other ids in text,
`"matched"` and `"matches"` in JSON. It walks `parent_dispatch.id`, whatever
its source, through the store with a visited set and a 64-level limit and
renders text or `niwa.lineage.chain/v1` JSON from the same walk result.
`list` reads the directory, parses each file, and sorts. A reader that meets
a `stop` value it doesn't know treats it as not `root`.

The authority record gains two optional v1 fields, registered in
`v1Optional` so records written before them keep reading:
`parent_session_source` (a string, default `""`) and `principal` (an object
`{name, source, trust}`, default `null`). Unlike the record's other fields
they parse leniently: a value that is the wrong type or fails its pattern
reads as unrecorded instead of making the record malformed, because a strict
reading would let an advisory field written by a later niwa void a real
grant at a gate. `niwa authority show` prints an empty or unreadable source
and a null or unreadable principal as `unrecorded`, and `--json` prints both
fields as stored when they are valid and omits them otherwise. Both are
written from the same `lineage.Resolve` result as the entry, so the two
can't disagree. The record's existing `parent_dispatch_source` keeps `cwd`,
`anchor` and `none`; the entry maps them to `observed`, `handed` and `none`,
and a `mapped` parent is `none` there, because that field only names a
record found through an instance.

#### Alternatives Considered

**Add a chain view to `niwa authority show`.** One command for both
questions. Rejected because `authority show` is the contract a gate reads to
decide whether an authority is held, scoped to one live instance, with its
own no-record rules; walking a durable store of reaped dispatches inside it
mixes two contracts a gate author must keep apart.

**Add reaped rows to `niwa list --json`.** Operators already read it.
Rejected because `list` enumerates instances, an unversioned array with no
place for a chain, and a reaped dispatch has no instance to list.

**Bump the authority record to v2.** A clean place for new fields. Rejected
because every older niwa reads a v2 record as holding nothing, so a gate
running an older niwa would deny every authority a new dispatch was granted.

## Decision Outcome

Lineage becomes a fourth thing dispatch writes, with its own small package
and store beside the configuration directory, and it reuses every read
dispatch already makes. At step (2a) the machine config's `principal` is
validated with the other machine authority settings. At (2e), right after
`dispatchParent`, dispatch builds `lineage.Inputs` from the session
variable, the flags, the working directory's instance and its newest session
mapping, the mapping the variable names when the dispatch runs at the root,
the parent dispatch id and its lineage entry, and the declared principal,
and `lineage.Resolve` turns them into a parent session link, a parent
dispatch link and a principal, each with its source and, for the principal,
its trust. Nothing in that step refuses for a lineage reason. At (8a), the
authority record is written with the new parent session source and
principal fields, then `CreateLineageEntry` writes
`<root>/.niwa-lineage/<dispatch id>.json`, refusing if the name is taken;
either failing refuses and the rollback, which now also removes the entry it
created, leaves nothing. As soon as (10) captures the session id, before
(11) writes the mapping, the entry is completed with it by a rewrite that
keeps every other stored member, a failure being a warning.

Reading is a new `niwa lineage` group. `show` selects one entry, reports
every match, walks up through parent dispatch ids of any source, and stops
at the root, a missing entry, an unreadable or later-version entry, a cycle,
or 64 levels, printing a fixed stop line or JSON `stop` value for each.
`list` prints every entry newest first. Both validate every known field on
read, keep unknown ones, report a bad entry as an error for that entry, and
treat only `observed` as observed in the three trust fields. No refresh
touches the store, and nothing removes it until the workspace is destroyed.
The lineage guide gains a section on the record and its trust boundary.

### Rationale

The four decisions lean on each other. Resolving at (2e) is what makes the
store worth having: it is the last moment the mappings and the parent entry
are reliably present, so the sources stored, `mapped` included, are the ones
niwa could actually check. Keeping the store out of the configuration
directory is what makes concurrent dispatches safe without a lock. Writing at
(8a) inside the existing rollback is what lets the write refuse without
leaving half a dispatch behind, and the existence check gives that write a
clean failure for a duplicate id that never touches the entry it collided
with. One file per dispatch, completed by a member-preserving rewrite, is
what lets the format grow and lets `show` and `list` report one bad entry
without losing the rest. Keeping the read surface separate from `niwa
authority show`, while feeding both from one resolver, gives gates and
lineage readers the same answers without changing what a gate's contract
means. The cost accepted is a second copy of the parent session source and
principal in the authority record, kept honest by being written from one
value.

## Solution Architecture

### Overview

```
                 (2a) principal check          (2e) resolve
 machine config ──────────────────────┐   ┌──────────────────────────┐
 session variable ─────────────────────┼──>│ lineage.Inputs           │
 cwd instance + newest mapping ────────┤   │   lineage.Resolve()      │
 root: mapping named by the variable ──┤   │   -> links, principal    │
 parent authority record (dispatchParent)  │                          │
 parent lineage entry (store read) ────┘   └────────────┬─────────────┘
                                                        │
                (8a) write authority.json (+2 fields) ──┤
                      CreateLineageEntry ───────────────┤──> <root>/.niwa-lineage/<id>.json
                (10)  CompleteLineageEntry(session id) ─┘   (before the (11) mapping write)
                rollback (7): remove the entry it created, with the instance

 niwa lineage show/list ──> store read + validate ──> walk ──> text | JSON
 niwa authority show ─────> authority.json (+ parent_session_source, principal)
```

### Components

- **`internal/lineage`** (new leaf package, no niwa imports beyond
  `internal/authority` for the id validators):
  - `entry.go`: the `Entry`, `Link` and `Principal` types; `SchemaEntryV1 =
    "niwa.lineage.entry/v1"`; `Marshal` writing the known members in a fixed
    order; `Parse` returning an `Entry` plus the raw members it didn't know,
    a later-version result for any `niwa.lineage.entry/vN` with N > 1, or an
    invalid-entry error; `SetSessionID(raw []byte, id string) ([]byte,
    error)`, the member-preserving rewrite completion uses, which rejects a
    document with a duplicate member as `Parse` does.
  - `validate.go`: the field patterns: dispatch id through
    `authority.ValidDispatchID`; session ids through
    `authority.ValidSessionID`; instance `^[A-Za-z0-9._+-]{1,255}$`
    (real instance names join the workspace name and suffix with `+`, and an
    entry with every field at its maximum still fits in 4 KiB); agent,
    origin, source and trust values as name values `^[a-z0-9-]{1,32}$`
    (unknown values kept); `written_at` RFC 3339 UTC; principal name
    `^[A-Za-z0-9._@+-]{1,128}$` or empty.
  - `resolve.go`: `Inputs`, `Resolve`. Link sources `observed`, `handed`,
    `mapped`, `unverified`, `none`; principal sources `inherited`,
    `declared`, `none`. `Resolve` applies the parent-dispatch rows in order
    (cwd record, `--parent-from`, `mapped`, `none`), so `--parent-from`
    always wins over `mapped`, and caps the principal's trust at `handed`
    whenever the parent dispatch link is not `observed`, the parent session
    link is `unverified`, the parent's principal trust is not `observed`, or
    a parent link has an id but its entry couldn't be used.
  - `walk.go`: `Walk(start Entry, get func(id string) Lookup) Chain`, with
    the visited set, the 64-level limit and the stop reasons `root`,
    `not-on-record`, `unreadable`, `later-version`, `cycle`, `depth`.
    `Lookup` is one read's result: an `*Entry`, a later-version schema
    string, an error, or not found. `Chain` is the ordered `[]Level` (each a
    `Lookup` with its dispatch id) plus the `Stop` reason and, for a cycle,
    the repeated id.
  - `observed.go`: `IsObserved(value string) bool`, true only for
    `observed`, used by every renderer.
- **`internal/workspace/lineage_store.go`** (new): `LineageDir(root)`
  (`<root>/.niwa-lineage`), `CreateLineageEntry`, `CompleteLineageEntry`,
  `RemoveLineageEntry`, `ReadLineageEntry(root, id)`,
  `ListLineageEntries(root)` (each result an entry, a later-version marker or
  a per-file error). Every function that takes a dispatch id checks it is a
  UUID before joining it into a path, whatever record it came from. Every
  read opens with `O_NOFOLLOW|O_NONBLOCK`, requires a regular file and reads
  at most 8 KiB, as `ReadAuthorityRecord` does; an entry whose
  `dispatch_id` differs from its file name, or with a duplicate member, is
  unreadable. Listing selects files by name (`<dispatch id>.json`, so the
  dot-prefixed temporaries are skipped) and reports any selected file that
  isn't a regular file within the cap, without parsing to decide. A
  `.niwa-lineage` path that is a symlink or not a directory refuses writes;
  every write opens the directory
  `O_RDONLY|O_DIRECTORY|O_NOFOLLOW` and `fchmod`s it to 0700 (stricter than
  once per process, and no per-process state to keep), and a failure
  there (another user owns it, say) refuses the write. `show` and `list`
  only read: they neither tighten the directory nor warn about its mode. An
  entry file whose mode grants access beyond its owner is unreadable. Completion refuses to write an entry that would exceed
  the read cap. Store file names are UUIDs in lowercase, so a
  case-insensitive filesystem can't make two ids collide.
- **`internal/config/registry.go`**: `Principal string` in `GlobalSettings`
  (`principal` key) and `GlobalConfig.Principal() (string, error)`
  validating with the lineage pattern. No workspace or overlay type gains a
  `principal` key.
- **`internal/authority/record.go`**: `ParentSessionSource string` and
  `Principal *PrincipalDoc` on `Record`, where `PrincipalDoc` is `{Name,
  Source, Trust string}` (the same shape as `lineage.Principal`, declared
  here because `internal/lineage` imports `internal/authority` and not the
  reverse); `parent_session_source` and `principal` in `recordDoc` after
  `parent_dispatch_source`, and in `v1Optional` with defaults `""` and
  `null`. Their parse is lenient: a wrong type or a value failing the
  pattern (duplicated here as constants) reads as unrecorded, never as a
  malformed record.
- **`internal/cli/dispatch_authority.go`**: `checkMachineAuthority` checks
  `Principal()`; `dispatchParent` also returns the working directory's
  `FoundInstance`; `resolveDispatchAuthority` builds `lineage.Inputs`,
  including the root mapping lookup, and stores the `Resolve` result on
  `dispatchAuthority`; `stampDispatchRecord` copies it onto the record.
- **`internal/cli/dispatch.go`**: (8a) create and set `entryCreated`,
  completion right after (10) and before (11), the rollback closure's guarded
  removal; the session variable read moves to (2e) and its value is reused at
  (4) for the telemetry attribute, which stays as it is.
- **`internal/cli/lineage.go`** (new): the `lineage`, `lineage show` and
  `lineage list` commands and their text and JSON renderers.
- **`internal/cli/authority.go`**: `show` prints the two new fields, or
  `unrecorded`; `--json` carries them when valid and omits them otherwise.
- **`docs/guides/dispatch-lineage.md`**: a "The lineage record" section
  covering R22's points; `docs/guides/authority-grants.md` notes the two new
  record fields.

### Key Interfaces

```go
package lineage

type Link struct {
    ID              string `json:"id"`
    Source          string `json:"source"`
    MappedSessionID string `json:"mapped_session_id,omitempty"` // parent session only, when unverified
}

type Principal struct {
    Name   string `json:"name"`
    Source string `json:"source"` // inherited | declared | none
    Trust  string `json:"trust"`  // observed | handed
}

type Entry struct {
    Schema         string    `json:"schema"`
    DispatchID     string    `json:"dispatch_id"`
    Instance       string    `json:"instance"`
    Agent          string    `json:"agent"`
    Origin         string    `json:"origin"`
    WrittenAt      string    `json:"written_at"`
    SessionID      string    `json:"session_id,omitempty"`
    ParentSession  Link      `json:"parent_session"`
    ParentDispatch Link      `json:"parent_dispatch"`
    Principal      Principal `json:"principal"`
}

type Inputs struct {
    SessionVar          string // raw value of the launch spec's variable
    ParentFromSet       bool
    CwdInInstance       bool
    NewestMappedSession string // "" when the instance has no mapping, or the instance came from --parent-from
    ParentDispatchID    string
    ParentDispatchFrom  string // authority.ParentSourceCwd | Anchor | None
    RootMappedDispatch  string // cwd in no instance and no --parent-from: the dispatch id of the mapping keyed by SessionVar (a lowercase UUID), or ""
    ParentEntry         *Entry // nil when absent, unreadable or a later version
    DeclaredPrincipal   string // already validated; "" when not declared
}

func Resolve(in Inputs) (parentSession, parentDispatch Link, p Principal)
```

The documents `show --json` and `list --json` print:

```json
{"schema": "niwa.lineage.chain/v1",
 "chain": [ <entry as stored> | {"dispatch_id": "...", "schema": "...", "known": false}
                              | {"dispatch_id": "...", "error": "..."} ],
 "stop": "root",
 "matched": 1,
 "matches": ["<dispatch id>"],
 "instance_name_fallback": false}

{"schema": "niwa.lineage.list/v1", "entries": [ ...same shapes... ]}
```

The authority record's JSON gains, after `parent_dispatch_source`:

```json
"parent_session_source": "observed",
"principal": {"name": "dana@example.com", "source": "inherited", "trust": "observed"},
```

Exit codes: `show` 0 when the selected entry is readable (a later version
included), 1 for an unreadable selected entry, no match, a bad `-C` or no
workspace, 2 for usage errors; `list` 0 unless the store directory exists
and can't be read.

### Data Flow

A coordinator tree, from a person's root session P to coordinator K to
worker W, with `principal` declared:

1. P runs `niwa dispatch` at the workspace root. (2e): no instance from the
   working directory, P has no mapping, so the session link is `handed`
   with P's id, the dispatch link `none`, and the principal `declared` with
   trust `observed`. (8a) writes K's authority record and
   `.niwa-lineage/<K>.json`. Capture at (10) adds K's session id.
2. K runs `niwa dispatch` from the workspace root, as coordinators do. (2e):
   no instance from the working directory, so the session link is `handed`
   with K's id; the mapping keyed by K's session id carries K's dispatch id,
   so the dispatch link is `mapped`; `.niwa-lineage/<K>.json` supplies the
   principal (`inherited`, trust `handed`, because the link is not
   observed). (8a) and (10) as before for W.
3. Both are reaped. The instances and mappings go; `.niwa-lineage/`
   doesn't.
4. `niwa lineage show --session <W's session>` finds W's entry, prints it,
   reads `.niwa-lineage/<K>.json` through W's parent dispatch id, prints it,
   and stops at `root` because K's parent dispatch id is empty.

A worker K dispatches from inside its own instance instead takes the
`observed` rows for both links, and its principal stays `observed`.

## Implementation Approach

### Phase 1: The lineage package

`internal/lineage` with the entry format, parsing that keeps unknown
members, the member-preserving `SetSessionID`, validation, `Resolve`,
`Walk` and `IsObserved`, and table tests covering every row of the PRD's
source tables (the `mapped` row and the trust caps included), every walk
stop, and the parse cases (later version, malformed field, oversized
principal, unknown field and unknown origin kept).
Deliverables: `internal/lineage/{entry,validate,resolve,walk,observed}.go`
and tests.

### Phase 2: The store

`internal/workspace/lineage_store.go` with existence-checked create,
member-preserving completion, removal, single read and listing. Tests:
modes, an existing 0755 directory tightened on every write, a duplicate id
refusing without touching the existing entry, a failed rename leaving
nothing at the final path (through a test seam), the 8 KiB cap, a file
whose `dispatch_id` differs from its name or with a duplicate member
reading as unreadable, a non-UUID id never reaching a path, a planted
symlink, FIFO and directory reading as per-entry errors without hanging,
selection by name only, a failed `fchmod` refusing the write, an unknown
member surviving completion, a failed or oversized completion leaving the
original entry byte for byte, and `--parent-from` winning over `mapped`.
Depends on Phase 1.

### Phase 3: The principal setting and the authority record fields

`principal` in the machine config with validation; the two optional v1
fields in the authority record, their lenient parse, and `authority show`
printing them or `unrecorded`. Tests: valid and invalid principals, a
`principal` key in the workspace config or an overlay having no effect, old
records reading as `unrecorded`, a record whose new fields hold a wrong type
or a bad value still reading its grant, round trips, and a check that no
gate-facing read (`authority show`'s held answer, `decide`) changes with the
new fields present. Can run beside Phase 1; its patterns are local
constants.

### Phase 4: Dispatch writes lineage

Wire (2a), (2e) with the root mapping lookup, (8a), the completion after
(10) and the rollback in `niwa dispatch`. Unit tests with the existing
dispatch seams for each R4 and R5 row, the principal rows, write refusal, a
duplicate id leaving the existing entry intact, completion and its warning,
a foreground mapping-write failure keeping the session id on the entry, and
the rollback and detached capture failure leaving no entry. Depends on
Phases 2 and 3.

### Phase 5: The `niwa lineage` commands

`show` and `list`, selectors including the positional id, the walk, text and
JSON renderers with `matched` and `matches`, exit codes, and tests for every
selector, the instance-name fallback's label, the multiple-match report,
every stop reason and the `(not observed)` rule. Depends on Phases 1 and 2;
can run beside Phase 4.

### Phase 6: Functional tests and the guide

Functional tests for the PRD's acceptance criteria that need real
dispatches, reaps and refreshes: the person-to-A-to-B tree; the coordinator
and worker pair dispatched from the root; each durability path, `niwa
destroy` with and without `--keep-session`, `niwa reset`, concurrent
dispatches during a refresh and `niwa destroy --workspace` among them; a
dispatch from an imported session with no mapping; the offline run with
proxies pointed at a closed port; no workflow engine on `PATH`. The guide
sections. Depends on Phases 4 and 5.

## Security Considerations

The lineage store is advisory local state, and the design does not try to make it tamper-proof. Any process running as the same user can write `<root>/.niwa-lineage/`, the session mappings and the authority record, so every field in an entry is a claim recorded at dispatch time, not a proof. The `source` and `trust` fields state how much niwa itself checked when it wrote the entry: `observed` means niwa saw the evidence (the instance's newest session mapping, the parent's authority record found from the working directory, the parent's lineage entry) at that moment; `mapped` means niwa found a coordinator's mapping through the session id the caller named but did not see that the caller is that session; `handed` and `unverified` mean it did not check. `observed` does not mean the file is unmodified since. No gate reads the lineage store or the new authority-record fields, and a later check such as a never-wider cap must not use the principal, either parent link or any source or trust value as an input to a permission decision; those fields identify and attribute, they do not authorize. Enforcement is unchanged from today.

Inputs are validated on write and again on every read. Dispatch and session ids go through the existing id validators, the instance, agent, origin, principal, source and trust fields have fixed patterns, and `written_at` must parse as RFC 3339. Unknown source, trust and origin values are kept when they are name values, so a newer niwa's values survive, but only the exact string `observed` is ever printed as observed. Unknown fields are kept, and completion rewrites only the `session_id` member, so an older niwa never strips a newer one's data. The declared `[global] principal` is validated when the personal config loads and an invalid value refuses the dispatch with the key and file named. The principal is a label for a person that someone chose to declare: it is never taken from a token, keyring, login, the OS account or the network, and never from the workspace config or an overlay, so the configuration snapshot, the one input that arrives over the network, can't name the person. A session variable that is unset or malformed records the source `none`, `--parent-from` always records `handed`, and the root mapping lookup records `mapped`. `observed` means the caller's claimed session id and working directory matched niwa's own records, not that niwa proved who the caller is: the mapping store's file names are session ids, so a same-user caller that sets its variable to one of them and runs from that session's instance gets `observed` links and an `observed` inherited principal, and one at the workspace root can pick which coordinator it appears under as `mapped`, and so whose principal it inherits. That is within the ruled boundary, which records rather than hardens, and the guide says so. A principal inherited through any link that isn't observed, past an `unverified` session link, or falling back to the declared one because a parent's entry couldn't be used, is capped at `handed`.

Reading the store is bounded so one bad entry costs only that entry. Every read opens the file with `O_NOFOLLOW` and `O_NONBLOCK`, requires a regular file, and reads at most 8 KiB, the same discipline the authority record reader applies, so a planted symlink, FIFO, directory or oversized file produces a per-entry error instead of a hang, an unrelated read or memory growth. Which files are entries is decided by name, type and size alone. Every read requires an entry's `dispatch_id` to equal its file name, so a copied or renamed file can't pose as another dispatch in a walk, and rejects a document with a duplicate member, so two readers can't see different values for one field. A dispatch id taken from a parent link, a mapping or an authority record, all same-user writable, is checked to be a UUID before it is joined into a path, so a forged `../` id reads as unreadable instead of opening another file. Unknown members are kept and printed as data, never interpreted, and a completion that would push an entry past the read cap is refused. The ancestor walk keeps a visited set and stops at 64 levels, on a cycle, on a missing, unreadable or later-version entry, so a forged parent id cannot loop or run long. Error text names the failure class only and never echoes file content, which may contain terminal escape sequences. An unreadable parent entry never refuses a dispatch: the principal falls back to the declared one and the parent link is kept.

Writes use the authority record's pattern: the final name is checked to be absent, then a temporary file is created `O_CREATE|O_EXCL|O_NOFOLLOW` at 0600 under a dot-prefixed random name, synced, and renamed to `<dispatch id>.json`, with the temporary removed on failure. The directory is created 0700, an existing one is opened `O_NOFOLLOW` and `fchmod`ed to 0700 on every write, a failure to tighten it (another user owns it, say) refuses the write, and a `.niwa-lineage` that is a symlink or not a directory is refused. Completion with the session id re-validates the entry it read, refuses a later-version entry rather than rewriting it with less, and replaces the file by rename. A race between two same-user writers on completion is accepted: the entry is advisory and locking would add a failure mode for no gain. The store sits outside the configuration directory, so no refresh copies, swaps or replaces it, and nothing a config source ships can land in it.

The two fields the authority record gains are parsed leniently: a wrong type or a value outside its pattern reads as unrecorded instead of making the record malformed. A strict reading would let an advisory field, written by a later niwa or edited by hand, void the record and with it every grant a gate reads from it.

The only personal data stored is the principal name, which is only what the user declared, plus session and dispatch ids and the instance name. All of it stays on the machine in 0600 files under the workspace root, and none of it goes to telemetry, to the network or to any other tool. A dispatched worker can read its own authority record and run `niwa lineage show`, so a declared principal can reach the worker's context and through it the agent's model provider; that is why the principal is opt-in, and the guide says so. Lineage never travels with a session export or import: a session imported from another host dispatches with a `handed` parent session, and because session ids aren't scoped to a host, `show --session` can match an older entry for the same id, and an imported session whose id this workspace mapped before can get a false `observed` or `mapped` link. Clearing `[global] principal` stops new entries naming the person but doesn't change entries already written or what their descendants inherit. The declared principal is machine-wide: it lands in the entries of every workspace dispatched from that machine, any process running as the user can read the personal config, and the environment can point niwa at a different personal config file, so the principal is what the dispatching process's environment says. Choosing among several matches by `written_at` relies on a field any same-user process can edit. A workspace root kept under version control would pick up `.niwa-lineage/` where the old `.niwa/` location may already have been ignored; the guide gives the ignore line. The store persists until the workspace is destroyed; a user who shares the workspace root should keep `.niwa-lineage/` out of what they publish. The design adds no dependency beyond the Go standard library and existing niwa packages, calls nothing over the network, and does not require koto or any other tool to be installed.

Residual risks accepted: a same-user process, any dispatched worker included, can forge or alter any entry, the authority record or a session mapping, and later children inherit from forged entries; an `observed` link or principal is conditional on the caller's claim and the parent's records being honest, which niwa cannot know; a principal stays in entries after it is cleared, with no prune command to remove it; a very large number of valid entries slows `show --session`, `show --instance` and `list` linearly.

## Consequences

### Positive

- An operator can reconstruct any dispatch tree after reap from local files,
  including coordinator and worker trees dispatched from the workspace root,
  with the person it acted for and how each link was established.
- Later authority work (a never-wider cap, escalation routing, decision
  attribution) has a durable, versioned, additive record to read, with the
  trust boundary stated in the data rather than in prose.
- The source rules live in one pure function, so the PRD's tables are tested
  directly and readers can't drift from writers.
- No refresh can lose an entry, and no gate's verdict changes; enforcement
  stays where it is.

### Negative

- The store grows by one small file per dispatch until the workspace is
  destroyed, and selection by session or instance reads every file.
- Dispatch does a little more I/O at (2e) (listing the session mappings, one
  mapping read at the root, one lineage read) and one more write at (8a) and
  after (10).
- The parent session source and principal now exist in two places while an
  instance lives.
- Every entry is advisory: a same-user process can write any of it.
- A first-level parent session is always `handed`, coordinator links are
  `mapped`, and Codex workers' and resumed sessions' dispatches read
  `unverified`, which a careless reader may take for an attack.
- Without a declared principal, entries name nobody.
- Watch review sessions have no entries until a later follow-up.

### Mitigations

- Entries are under 4 KiB; a `prune --before` can come later without a
  format change, and the walk already handles missing ancestors.
- The mapping listing is bounded by the number of live sessions, and the
  lineage and root mapping reads are one small file each.
- Both copies are written from one `Resolve` result, and a test asserts the
  authority record and the entry agree.
- Every read validates every known field and reports a bad entry as an
  error; the guide says entries are advisory.
- The guide explains when `handed`, `mapped` and `unverified` are expected,
  which link to follow for the tree, and how to declare the principal.
