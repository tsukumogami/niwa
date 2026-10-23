---
schema: design/v1
status: Proposed
upstream: docs/prds/PRD-dispatch-lineage.md
decision_provenance: inline-resolved
problem: |
  niwa's only parent links for a dispatched session live in the instance's
  authority record and the workspace's session mapping, and `niwa reap`
  deletes both. Neither names the person a session acts for, and the parent
  session id is copied from a caller-controlled variable with no mark saying
  whether niwa checked it. A durable, dispatch-keyed record has to be written
  at dispatch time, when the evidence for each link still exists, and read
  back long after.
decision: |
  A new leaf package, `internal/lineage`, owns the entry format, its
  validation and a pure resolver that turns what dispatch already reads at
  step (2e) into parent links with sources and a principal. One JSON file per
  dispatch lands in `<workspace root>/.niwa/lineage/` at step (8a), created
  exclusively beside the authority record, removed by the same rollback, and
  completed once with the worker's session id at step (11). The config
  snapshot carries the directory across refreshes as it carries `sessions/`.
  A `niwa lineage` command group reads it; the authority record gains the
  same parent session source and principal as additive v1 fields; `niwa
  watch` writes root entries through the same writer.
rationale: |
  Resolving at (2e) keeps every input in one place, at the one moment the
  dispatching instance's mapping and the parent's entry are guaranteed to be
  readable, and a pure resolver makes the PRD's source tables directly
  testable. File-per-dispatch matches the session store's write pattern,
  needs no locking between parallel dispatches, and lets a reader skip one
  bad entry. Writing at (8a) is the only point where a write failure can
  still refuse before a worker runs. A separate command group keeps the
  authority read contract, which gates depend on, unchanged in meaning.
---

# DESIGN: dispatch-lineage

## Status

Proposed

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
holding for each dispatch the instance, the agent, the origin (`dispatch` or
`watch`), the worker's session id once captured, two parent links each with
a source, and a principal with a source and a trust value. The technical
problem has four parts.

**Where the record lives and how it is written.** It has to sit at the
workspace root outside every instance, survive every reap path, `niwa
destroy` of an instance, `niwa reset` and every configuration refresh, and
go away only with the workspace (PRD R10, R11). It must be written before the
worker starts, so a write failure refuses the dispatch the way an authority
record write failure does, and it must not exist for a dispatch that removes
its instance (R2, R3). The worker's session id isn't known until step (10)
captures it, and capture can fail; a kept foreground instance with no
captured session still has an entry (R2). Entries are 0600 in a 0700
directory, written atomically, and at most 4 KiB (R22, R23).

**How each link's source is decided.** The parent session link's source is
`none` when the variable is unset or not a well-formed id; `handed` when
`--parent-from` was given, when the working directory is in no instance, or
when that instance has no session mapping; `observed` when the instance's
newest mapping (latest recorded creation time, ties to the lower session id,
the rule `NewestMappingPerInstance` already implements) names the same id;
and `unverified`, with the mapping's id recorded beside the claim, when it
names a different one (R4). The parent dispatch link is `observed` when the
working directory's instance gave it, `handed` when `--parent-from` did, and
`none` with no parent record (R5). Nothing is refused for any source (R6):
resumed sessions and Codex workers legitimately present an id other than the
mapped one.

**Where the principal comes from.** It is inherited from the parent
dispatch's entry when the parent link has an id and that entry is readable,
with trust `observed` only when both the link and the parent's own principal
trust are `observed`. Otherwise it is the machine's declared
`[global] principal` (1 to 128 letters, digits, `.`, `_`, `-`, `@`, `+`),
else the OS account name if it fits that rule, else empty with source
`none`. A readable parent whose principal is empty is inherited as empty. An
invalid declared value refuses every dispatch and watch review launch. The
principal never comes from a token, a keyring, a login or the network (R7,
R8, R9).

**How it is read.** `niwa lineage show` selects one entry (by dispatch id,
`--session`, `--instance`, the working directory or `-C`, latest
`written_at` winning, ties to the lower dispatch id) and walks up through
parent dispatch ids with fixed stop rules (root, not on record, unreadable,
later version, cycle, 64 levels), printing a versioned
`niwa.lineage.chain/v1` document under `--json`; `niwa lineage list` prints
every entry (R13 to R16). In `parent_session.source`,
`parent_dispatch.source` and `principal.trust`, only `observed` is observed,
and unknown values print with `(not observed)` (R17). The authority record
carries the same parent session source and principal while the instance
lives (R18). All of it works with no other tool installed, offline, and with
the principal kept off telemetry (R19, R20), and the lineage guide states the
trust boundary (R21). Dispatch argv, settings, telemetry and existing
refusals don't change (R24).

## Decision Drivers

- **Capture evidence when it exists.** The mapping that makes a session link
  observable and the parent entry a principal is inherited from can both be
  gone by the time anyone reads the record, so the sources must be decided
  and stored at dispatch time.
- **Refuse only before launch.** A lineage write that fails after the worker
  starts can't stop it; the write has to happen where the authority record's
  write already sits, inside the same rollback.
- **Don't widen any existing contract.** `niwa authority show --json` is what
  gates read to decide whether an authority is held. Its meaning and its
  fail-closed reading of later versions must not change, and the session
  mapping stays keyed and deleted exactly as today.
- **Parallel dispatches don't coordinate.** Several dispatches in one
  workspace can write at once; the store needs no lock.
- **One bad entry costs one entry.** A same-user process can edit the store,
  so every read validates, and a reader skips or reports a bad entry without
  losing the rest.
- **Testable resolution.** The PRD gives the source rules as tables; the code
  that applies them should be a pure function a table test can cover row by
  row.
- **No other tool, no network.** Nothing in the write or read path calls out,
  and nothing reads another tool's files.
- **Small, fixed footprint.** One entry is under 4 KiB, and a workspace that
  never dispatches gets no directory.

## Considered Options

### Decision 1: Where entries live and how they are stored

The store has to outlive every instance, survive the configuration directory
swap `niwa apply` performs, accept concurrent writers, and let one entry be
completed later with a session id. The workspace's configuration directory,
`<root>/.niwa/`, already holds niwa-written local state that survives the
swap: `refreshSnapshot` (`internal/workspace/snapshotwriter.go`) carries
`dispatch-briefs/` and `sessions/` into the new snapshot by name before a
generic carry-over handles everything else.

Resolved inline.

Key assumptions:
- Hard links work in the workspace root's filesystem (true for the local
  filesystems niwa supports; the write falls back to refusing, not to a
  non-exclusive write, if `link` fails).
- The number of entries stays in the thousands, so listing reads every file.

#### Chosen: one JSON file per dispatch in `<root>/.niwa/lineage/`

Each entry is `<root>/.niwa/lineage/<dispatch id>.json`, one JSON object with
the PRD's fields in a fixed key order, mode 0600, in a directory created
0700 on first write. The snapshot refresh gains a `preserveLineage` step
beside `preserveSessionMappings`, carrying the directory by name and
restoring its mode, so a refresh can't drop it even if a config source ever
ships a `lineage/` of its own. The dispatch id is a version-4 UUID, so the
file name needs no escaping and two dispatches never share one. Writes go to
a temporary file opened `O_CREATE|O_EXCL|O_NOFOLLOW` at 0600 in the same
directory, synced, then hard-linked to the final name: `link` fails if the
name exists, which makes creation exclusive and atomic at once, and the
temporary name is removed either way. Completing the entry with the session
id writes a new temporary file and renames it over the final name, which is
atomic and replaces the one entry that already exists.

#### Alternatives Considered

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

**A sibling directory outside the configuration directory, such as
`<root>/.niwa-lineage/`.** It would never be touched by the snapshot swap.
Rejected because it adds a new top-level name to every workspace, every
root-level scan that enumerates instances would have to learn to skip it, and
the configuration directory's carry-over already protects niwa-written state
with a tested pattern.

### Decision 2: Where and how the links and the principal are resolved

The source rules read four things: the session variable, whether
`--parent-from` was given, the dispatching instance and its newest mapping,
and the parent dispatch's own lineage entry. Step (2e) already reads the
first three's neighbours: it calls `dispatchParent`, which runs
`FindInstance` and reads the parent's authority record, and it refuses
before anything exists.

Resolved inline.

Key assumptions:
- The dispatching instance's newest mapping is readable at (2e). A mapping
  directory that can't be listed reads as "no mapping", which makes the
  session link `handed`, never `observed`.
- An unreadable parent lineage entry is not a refusal: the PRD falls back to
  the machine's principal and keeps the parent link.

#### Chosen: resolve at (2e) through a pure resolver in `internal/lineage`

`resolveDispatchAuthority` gathers a `lineage.Inputs` value at (2e), right
after `dispatchParent`: the session variable's value (read once there, then
reused for the telemetry attribute and the authority record), whether
`--parent-from` was set, whether the working directory was in an instance,
that instance's newest mapping session id (through `ListSessionMappings` and
`NewestMappingPerInstance`, read only when the instance came from the
working directory), the parent dispatch id and how it was found, the parent's
lineage entry (read through the store by that id, when there is one), the
declared principal and the OS account name. `lineage.Resolve(Inputs)
(Links, Principal)` applies the PRD's two tables in row order and returns the
values the entry and the authority record both store. It does no I/O, so a
table test covers every row, including the inherited-trust combinations and
the empty-inherited principal.

The declared principal is read from `[global] principal` in the personal
config (`internal/config/registry.go`). It is validated at load beside
`authority_level` and `authority_ceiling`, and `checkMachineAuthority` at
(2a) refuses an invalid value with the key and the file named; `niwa watch`
runs the same check before it launches a review. The OS account name comes
from `os/user.Current().Username` and is used only when it passes the same
pattern; an error or a failing name falls through to `none`.

#### Alternatives Considered

**Resolve at (8a), next to the record write.** Everything is still readable
then. Rejected because it splits the parent reads from (2e), which already
reads the parent record and may refuse, and it delays the evidence read past
provisioning, which can take minutes, during which a reap could remove the
mapping that would have made the link observed.

**Store the raw inputs and decide sources when the record is read.** The
entry would hold the variable's value and the instance name, and `show` would
compute the source. Rejected because the mapping and the parent entry that
decide the source are exactly what reap deletes: by read time the answer is
always `handed`.

**Match the session against any mapping for the instance, or against the
mapping carrying the parent's dispatch id.** Any mapping would accept a
session that left the instance long ago; matching by dispatch id would miss a
session the SessionStart hook mapped. Rejected in favour of the newest
mapping, the rule the PRD defines and `niwa list` already uses.

### Decision 3: When the entry is written, completed and removed

The PRD wants an entry to exist exactly when the dispatch keeps its instance,
written before the worker starts, completed once with the session id, and
otherwise unchanged.

Resolved inline.

Key assumptions:
- The rollback armed at (7) is the only path by which a dispatch removes its
  instance after (8a); SIGKILL leaves an unmapped instance for the backstop.

#### Chosen: create at (8a), complete at (11), remove inside the rollback

At (8a), after `writeAuthorityRecordFunc` succeeds, dispatch calls
`workspace.CreateLineageEntry(root, entry)`. A failure, an existing entry for
the id included, returns a refusal and the rollback runs. The rollback
closure armed at (7) gains one line: when `success` is false it removes the
entry for this dispatch id as well as the instance, so a refusal after
(8a), a launch failure and a detached capture failure all leave no entry. At
(11), after `WriteSessionMapping`, dispatch calls
`workspace.CompleteLineageEntry(root, dispatchID, sessionID)`, which reads
the entry, checks it has no session id yet, sets it and replaces the file by
rename. A failure there prints one warning naming the entry and the dispatch
still succeeds. The foreground path that sets `success` before capture keeps
its entry with no session id, which is what R2 asks for. A SIGKILL between
(8a) and the rollback leaves an entry whose instance the backstop later
removes; that entry stays, with no session id, which reads correctly as a
dispatch that launched or tried to.

`niwa watch` follows the same three steps around its review launch in
`internal/cli/watch.go`: it uses the reap key it already mints with
`newDispatchID` as the entry's dispatch id, creates the entry (origin
`watch`, both parent links `none`, principal from the machine) before
`dispatchLaunch`, removes it if the launch fails, and completes it when
`captureReviewSession` returns an id. Continuing a staged review resumes the
same session and writes nothing.

#### Alternatives Considered

**Write the entry once, after capture at (11).** One write, no completion
step. Rejected because a write failure after launch can't refuse a worker
that is already running, which breaks R3, and a kept foreground instance
with no captured session would get no entry.

**Keep the session id in a second file beside the entry.** Each file would be
written exactly once. Rejected because every reader would join two files, a
missing second file would be ambiguous (not captured, or lost), and the PRD
describes one entry per dispatch.

### Decision 4: How lineage is read, and what the authority record gains

Readers come in two kinds: people and tools looking back at a reaped tree,
and gates checking a running session through `niwa authority show`. The
authority record is format `niwa.authority.record/v1`, additive within the
version, with `v1Optional` (`internal/authority/record.go`) for fields added
after the first release; a later version is read as holding nothing.

Resolved inline.

Key assumptions:
- Gates only read `grant`, `grant_ceiling_source` and `record`; new fields in
  the authority record don't change any gate's verdict.

#### Chosen: a `niwa lineage` command group, and additive v1 fields

`internal/cli/lineage.go` adds `niwa lineage show` and `niwa lineage list`.
`show` takes a dispatch id argument or `--session`, `--instance` or `-C`,
with the same usage-error and path rules `niwa authority show` applies
(exit 2 for two selectors or `-C` with another, exit 1 for an empty,
relative or nonexistent `-C`). With no selector or `-C`, it finds the
instance with `FindInstance`, reads its authority record's dispatch id, and
falls back to the latest entry for that instance name. It walks
`parent_dispatch.id` through the store with a visited set and a 64-level
limit and renders text or `niwa.lineage.chain/v1` JSON from the same walk
result. `list` reads the directory, parses each file, and sorts.

The authority record gains two optional v1 fields, registered in
`v1Optional` so records written before them keep reading:
`parent_session_source` (a string, default `""`) and `principal` (an object
`{name, source, trust}`, default `null`). `niwa authority show` prints an
empty source or a null principal as `unrecorded`. Both are written from the
same `lineage.Resolve` result as the entry, so the two can't disagree. The
record's existing `parent_dispatch_source` keeps `cwd`, `anchor` and `none`;
the entry maps them to `observed`, `handed` and `none`.

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
and store, and it reuses every read dispatch already makes. At step (2a) the
machine config's `principal` is validated with the other machine authority
settings. At (2e), right after `dispatchParent`, dispatch builds
`lineage.Inputs` from the session variable, the flags, the working directory's
instance and its newest session mapping, the parent dispatch id and its
lineage entry, the declared principal and the OS account, and
`lineage.Resolve` turns them into a parent session link, a parent dispatch
link and a principal, each with its source and, for the principal, its
trust. Nothing in that step refuses for a lineage reason. At (8a), the
authority record is written with the new parent session source and principal
fields, then `CreateLineageEntry` writes `<root>/.niwa/lineage/<dispatch
id>.json` exclusively; either failing refuses and the rollback, which now
also removes the entry, leaves nothing. At (11) the entry is completed with
the captured session id, a failure being a warning. `niwa watch` writes root
entries around its review launch the same way.

Reading is a new `niwa lineage` group. `show` selects one entry, walks up
through parent dispatch ids, and stops at the root, a missing entry, an
unreadable or later-version entry, a cycle, or 64 levels, printing a fixed
stop line or JSON `stop` value for each. `list` prints every entry newest
first. Both validate every field on read, report a bad entry as an error for
that entry, and treat only `observed` as observed in the three trust fields.
The snapshot refresh carries the store across configuration swaps by name,
and nothing else touches it until the workspace is destroyed. The lineage
guide gains a section on the record and its trust boundary.

### Rationale

The four decisions lean on each other. Resolving at (2e) is what makes the
store worth having: it is the last moment the mapping and the parent entry
are reliably present, so the sources stored are the ones niwa could actually
check. Writing at (8a) inside the existing rollback is what lets the write
refuse without leaving half a dispatch behind, and the exclusive
create-by-link gives that write a clean failure for a duplicate id. One file
per dispatch is what lets the completion step replace a single file without
locking out parallel dispatches, and what lets `show` and `list` report one
bad entry without losing the rest. Keeping the read surface separate from
`niwa authority show`, while feeding both from one resolver, gives gates and
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
 parent authority record (dispatchParent)  │   -> Links, Principal    │
 parent lineage entry (store read) ────┘   └────────────┬─────────────┘
                                                        │
                (8a) write authority.json (+2 fields) ──┤
                      CreateLineageEntry ───────────────┤──> <root>/.niwa/lineage/<id>.json
                (11)  CompleteLineageEntry(session id) ─┘
                rollback (7): remove entry with instance

 niwa lineage show/list ──> store read + validate ──> walk ──> text | JSON
 niwa authority show ─────> authority.json (+ parent_session_source, principal)
 snapshot refresh ────────> preserveLineage(lineage/)
```

### Components

- **`internal/lineage`** (new leaf package, no niwa imports beyond
  `internal/authority` for the id validators):
  - `entry.go`: the `Entry`, `Link` and `Principal` types; `SchemaEntryV1 =
    "niwa.lineage.entry/v1"`; `MarshalJSON` with fixed key order; `Parse`
    returning an `Entry`, a `*LaterVersion` value for any
    `niwa.lineage.entry/vN` with N > 1, or an `*InvalidEntry` error.
  - `validate.go`: the field patterns: dispatch id through
    `authority.ValidDispatchID`; session ids through
    `authority.ValidSessionID`; instance `^[A-Za-z0-9._-]{1,128}$`; agent
    `^[a-z0-9-]{1,32}$`; origin `dispatch|watch`; `written_at` RFC 3339 UTC;
    principal name `^[A-Za-z0-9._@+-]{1,128}$` or empty; source and trust
    strings `^[a-z-]{1,32}$` (unknown values are kept, per R17).
  - `resolve.go`: `Inputs`, `Links`, `Principal`, `Resolve`. Source constants
    `observed`, `handed`, `unverified`, `none`; principal sources
    `inherited`, `declared`, `os-account`, `none`.
  - `walk.go`: `Walk(start Entry, get func(id) (Lookup, error)) Chain`, with
    the visited set, the 64-level limit and the stop reasons `root`,
    `not-on-record`, `unreadable`, `later-version`, `cycle`, `depth`.
  - `observed.go`: `IsObserved(value string) bool`, true only for
    `observed`, used by every renderer.
- **`internal/workspace/lineage_store.go`** (new): `LineageDir(root)`,
  `CreateLineageEntry`, `CompleteLineageEntry`, `RemoveLineageEntry`,
  `ReadLineageEntry(root, id)`, `ListLineageEntries(root)` (each result an
  entry, a later-version marker or a per-file error). Every read opens with
  `O_NOFOLLOW|O_NONBLOCK`, requires a regular file and reads at most 8 KiB,
  as `ReadAuthorityRecord` does; listing ignores names that aren't a valid
  dispatch id with `.json`; a `lineage` path that is a symlink or not a
  directory refuses writes.
- **`internal/workspace/snapshotwriter.go`**: `preserveLineage`, called right
  after `preserveSessionMappings`. Unlike that step it first removes any
  `lineage/` the fetched config source put in staging, then copies only
  regular files named `<dispatch id>.json` within the read cap, skipping and
  reporting anything else instead of failing the refresh, and sets the
  directory mode to 0700.
- **`internal/config/registry.go`**: `Principal string` in `GlobalSettings`
  (`principal` key) and `GlobalConfig.Principal() (string, error)`
  validating with the lineage pattern.
- **`internal/authority/record.go`**: `ParentSessionSource string` and
  `Principal *PrincipalDoc` on `Record`; `parent_session_source` and
  `principal` in `recordDoc` (after `parent_dispatch_source`) and in
  `v1Optional` with defaults `""` and `null`; parse validation reusing the
  lineage patterns' rules (duplicated in `internal/authority` as constants,
  since `internal/lineage` imports `internal/authority` and not the reverse).
- **`internal/cli/dispatch_authority.go`**: `checkMachineAuthority` checks
  `Principal()`; `resolveDispatchAuthority` builds `lineage.Inputs` and
  stores the `Resolve` result on `dispatchAuthority`;
  `stampDispatchRecord` copies it onto the record.
- **`internal/cli/dispatch.go`**: (8a) create, (11) complete, the rollback
  closure's removal; the session variable read moves to (2e) and its value is
  reused at (4) for the telemetry attribute, which stays as it is.
- **`internal/cli/watch.go`**: principal check, create, remove-on-launch-
  failure and complete around the review launch.
- **`internal/cli/lineage.go`** (new): the `lineage`, `lineage show` and
  `lineage list` commands and their text and JSON renderers.
- **`internal/cli/authority.go`**: `show` prints the two new fields, or
  `unrecorded`.
- **`docs/guides/dispatch-lineage.md`**: a "The lineage record" section
  covering R21's points; `docs/guides/authority-grants.md` notes the two new
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
    Source string `json:"source"` // inherited | declared | os-account | none
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
    ParentEntry         *Entry // nil when absent, unreadable or a later version
    DeclaredPrincipal   string // already validated
    OSAccount           string // unvalidated
}

func Resolve(in Inputs) (parentSession, parentDispatch Link, p Principal)
```

The documents `show --json` and `list --json` print:

```json
{"schema": "niwa.lineage.chain/v1",
 "chain": [ <entry> | {"dispatch_id": "...", "schema": "...", "known": false}
                    | {"dispatch_id": "...", "error": "..."} ],
 "stop": "root"}

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

A three-level tree, from a person's root session P to A to B:

1. P runs `niwa dispatch` at the workspace root. (2e): no instance from the
   working directory, so the session link is `handed` with P's id, the
   dispatch link `none`, and the principal `declared` (or `os-account`) with
   trust `observed`. (8a) writes A's authority record and
   `lineage/<A>.json`. (11) adds A's session id.
2. A runs `niwa dispatch` inside its instance. (2e): `FindInstance` finds A's
   instance, `dispatchParent` reads A's record (dispatch link `observed`,
   id A), the newest mapping for A's instance names A's session, which equals
   the variable (session link `observed`), and `lineage/<A>.json` supplies the
   principal (`inherited`, trust `observed`). (8a) and (11) as before for B.
3. Both are reaped. The instances and mappings go; `lineage/` doesn't.
4. `niwa lineage show --session <B's session>` finds B's entry, prints it,
   reads `lineage/<A>.json` through B's parent dispatch id, prints it, and
   stops at `root` because A's parent dispatch id is empty.

## Implementation Approach

### Phase 1: The lineage package

`internal/lineage` with the entry format, parsing, validation, `Resolve`,
`Walk` and `IsObserved`, and table tests covering every row of the PRD's two
source tables, the inherited-trust combinations, every walk stop, and the
parse cases (later version, malformed field, oversized principal).
Deliverables: `internal/lineage/{entry,validate,resolve,walk,observed}.go`
and tests.

### Phase 2: The store and its carry-over

`internal/workspace/lineage_store.go` with exclusive create by link,
completion by rename, removal, single read and listing, plus
`preserveLineage` in the snapshot refresh. Tests: modes, a duplicate id
refusing, a failed link or rename leaving nothing at the final path (through
a test seam), the 8 KiB cap, a planted symlink and FIFO reading as per-entry
errors without hanging, an apply-style refresh keeping the directory, and a
refresh whose config source ships a `lineage/`, an oddly named file or an
oversized file: the shipped entries are dropped, the odd files skipped and
reported, and the refresh succeeds. Depends on Phase 1.

### Phase 3: The principal setting and the authority record fields

`principal` in the machine config with validation; the two optional v1
fields in the authority record, their parse checks, and `authority show`
printing them or `unrecorded`. Tests: valid and invalid principals, old
records reading as `unrecorded`, round trips. Depends on Phase 1.

### Phase 4: Dispatch writes lineage

Wire (2a), (2e), (8a), (11) and the rollback in `niwa dispatch`. Unit tests
with the existing dispatch seams for each R4 and R5 row, the principal rows,
write refusal, completion and its warning, and the rollback and detached
capture failure leaving no entry. Depends on Phases 2 and 3.

### Phase 5: Watch writes root entries

The principal check, create, remove and complete around `niwa watch`'s review
launch, with tests. Depends on Phase 4's wiring helpers.

### Phase 6: The `niwa lineage` commands

`show` and `list`, selectors, the walk, text and JSON renderers, exit codes,
and tests for every selector, stop reason and the `(not observed)` rule.
Depends on Phases 1 and 2; can run beside Phases 4 and 5.

### Phase 7: Functional tests and the guide

Functional tests for the PRD's acceptance criteria that need real
dispatches, reaps and refreshes (the A-to-B tree, the durability paths, the
offline run with proxies pointed at a closed port, no workflow engine on
`PATH`), and the guide sections. Depends on Phases 4 to 6.

## Security Considerations

The lineage store is advisory local state, and the design does not try to make it tamper-proof. Any process running as the same user can write `<root>/.niwa/lineage/`, the session mappings and the authority record, so every field in an entry is a claim recorded at dispatch time, not a proof. The `source` and `trust` fields state how much niwa itself checked when it wrote the entry: `observed` means niwa saw the evidence (the instance's newest session mapping, the parent's authority record, the parent's lineage entry) at that moment, and `handed` or `unverified` mean it did not. `observed` does not mean the file is unmodified since. No gate reads the lineage store or the new authority-record fields, and a later check such as a never-wider cap must not use the principal, either parent link or any source or trust value as an input to a permission decision; those fields identify and attribute, they do not authorize. Enforcement is unchanged from today.

Inputs are validated on write and again on every read. Dispatch and session ids go through the existing id validators, the instance, agent, origin, principal, source and trust fields have fixed patterns, and `written_at` must parse as RFC 3339. Unknown source and trust strings are kept so a newer niwa's values survive, but only the exact string `observed` is ever printed as observed. The declared `[global] principal` is validated when the personal config loads and an invalid value refuses the dispatch or watch launch with the key and file named; the OS account name is used only if it passes the same pattern. The principal is a label for a person: it is never taken from a token, keyring, login or the network. A session variable that is unset or malformed records the source `none`, and `--parent-from` always records `handed`; a dispatch cannot make its claimed parent look observed except by presenting what niwa itself checks, and a same-user process that can write the mapping can already do that.

Reading the store is bounded so one bad entry costs only that entry. Every read opens the file with `O_NOFOLLOW` and `O_NONBLOCK`, requires a regular file, and reads at most 8 KiB, the same discipline the authority record reader applies, so a planted symlink, FIFO, directory or oversized file produces a per-entry error instead of a hang, an unrelated read or memory growth. Listing ignores names that are not a valid dispatch id with a `.json` suffix. The ancestor walk keeps a visited set and stops at 64 levels, on a cycle, on a missing, unreadable or later-version entry, so a forged parent id cannot loop or run long. Error text names the failure class only and never echoes file content, which may contain terminal escape sequences. An unreadable parent entry never refuses a dispatch: the principal falls back to the machine's and the parent link is kept.

Writes use the authority record's pattern: a temporary file created `O_CREATE|O_EXCL|O_NOFOLLOW` at 0600 under a random name, synced, then hard-linked to `<dispatch id>.json` so creation is exclusive and atomic, with the temporary removed either way. The directory is created 0700; an existing `lineage` that is a symlink or not a directory is refused. Completion with the session id re-validates the entry it read, refuses a later-version entry rather than rewriting it with less, and replaces the file by rename. If the file system cannot hard-link, the write refuses; it never falls back to a non-exclusive write. A race between two same-user writers on completion is accepted: the entry is advisory and locking would add a failure mode for no gain.

The configuration snapshot refresh is the one place network-delivered content and the store share a directory. `preserveLineage` therefore discards any `lineage/` the config source ships before copying the local store in, so a shared config repository cannot plant entries that read as observed. It copies only regular files named `<dispatch id>.json` and no larger than the read cap, skips and reports anything else rather than failing the apply, and sets the directory mode explicitly after the copy so a refresh does not widen 0700. Tests cover a source-shipped `lineage/`, an oddly named file, an oversized file and a symlink in the store.

The only personal data stored is the principal name, which is either what the user declared or, failing that, their OS account name, plus session and dispatch ids and the instance name. All of it stays on the machine under the workspace's local configuration directory in 0600 files, and none of it goes to telemetry, to the network or to any other tool. The OS account name is recorded without the user choosing to, so the guide says that setting `[global] principal` replaces it, and that entries can be deleted from the store by hand. The store persists until the workspace is destroyed; a user who commits or shares the workspace's configuration directory should keep `lineage/` out of what they publish. The design adds no dependency beyond the Go standard library and existing niwa packages, calls nothing over the network, and does not require koto or any other tool to be installed.

Residual risks accepted: a same-user process can forge or alter any entry, the authority record or a session mapping, and an inherited principal's `observed` trust is conditional on the parent's record and entry being unmodified, which niwa cannot know; a very large number of valid entries slows `list` and snapshot refreshes linearly, and a prune command is deferred.

## Consequences

### Positive

- An operator can reconstruct any dispatch tree after reap from local files,
  with the person it acted for and how far each link can be trusted.
- Later authority work (a never-wider cap, escalation routing, decision
  attribution) has a durable, versioned record to read, with the trust
  boundary stated in the data rather than in prose.
- The source rules live in one pure function, so the PRD's tables are tested
  directly and readers can't drift from writers.
- No gate's verdict changes; enforcement stays where it is.

### Negative

- The store grows by one small file per dispatch until the workspace is
  destroyed.
- Dispatch does a little more I/O at (2e) (listing the session mappings, one
  lineage read) and one more write at (8a) and (11).
- The parent session source and principal now exist in two places while an
  instance lives.
- Every entry is advisory: a same-user process can write any of it.
- A first-level parent session is always `handed`, and Codex workers' and
  resumed sessions' dispatches read `unverified`, which a careless reader may
  take for an attack.

### Mitigations

- Entries are under 4 KiB; a prune command can come later without a format
  change, and the walk already handles missing ancestors.
- The mapping listing is bounded by the number of live sessions, and the
  lineage read is one small file.
- Both copies are written from one `Resolve` result, and a test asserts the
  authority record and the entry agree.
- Every read validates every field and reports a bad entry as an error; the
  guide says entries are advisory.
- The guide explains when `handed` and `unverified` are expected and which
  link to follow for the tree.
