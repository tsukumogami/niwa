---
schema: prd/v1
status: In Progress
problem: |
  When sessions dispatch sessions, the only niwa records of who asked for
  each dispatch live in files `niwa reap` deletes, no record names the person
  a tree of sessions works for, and the parent ids niwa does write carry no
  mark of whether niwa checked them or took the caller's word. Operators
  can't reconstruct a finished tree, and tools that reason about authority
  have to treat a claimed parent as a fact.
goals: |
  After every session in a tree is gone, an operator reads from a niwa
  record in the workspace which session asked for each dispatch, which
  person the tree acts for, and how niwa came by every parent link. Tools
  read the same answers in a versioned form that never presents a handed
  parent as an observed one, and none of it needs another tool installed.
absorbed:
  - docs/briefs/BRIEF-dispatch-lineage.md
---

## Status

In Progress

Absorbed [BRIEF-dispatch-lineage](docs/briefs/BRIEF-dispatch-lineage.md); carried in Absorbed Brief.

The brief was folded into this PRD and has no separate file; the link above is the absorption record the document format requires, and the brief's content is the Absorbed Brief section below.

## Absorbed Brief

The brief framed the gap from the operator's side. Sessions dispatch
sessions, and once a tree of them finishes and `niwa reap` removes its
instances, niwa keeps no local record of which session asked for which
dispatch, nothing at all about the person the tree worked for, and no mark
on the parent ids it did record saying whether niwa saw that parent or took
the caller's word. Later authority work (capping a child at its parent,
routing a session's questions to whoever directs it, attributing decisions
to a person) needs the tree readable after the sessions end.

The outcome it set: an operator who comes back after every instance in a
tree is reaped reads, from a niwa record in the workspace, the whole chain
for any dispatch, the person the tree acts for at every level, and beside
every parent link whether niwa observed it or the caller handed it. A
dispatch whose claimed parent contradicts niwa's own records never passes
as observed. Tools read the same answers in a stable form, and none of it
needs another tool installed.

Its four journeys (an operator reconstructing a reaped tree, a coordinator
reading one link and how far to trust it, a dispatch claiming a parent it
didn't come from, and a session started with no parent session) are the
user stories below. Its boundary is carried by the requirements and Out of
Scope: capping authority, hardening the trust boundary, reading any
workflow engine's records, decision attribution and escalation routing,
cross-machine lineage, changing credentials, and guessing parents from the
process tree all stay out. It left three questions to this document: what
happens to a false parent claim, how long the record outlives the instance,
and what identifies the person. Decisions and Trade-offs answers each.

## Problem Statement

Sessions dispatch sessions. A person starts a session, it runs `niwa
dispatch` to hand work to a second session, and that one hands a slice to a
third. Each dispatch gets its own instance, and while the instances exist
niwa knows a little about how they relate: the instance's authority record
names the dispatch that made it, the dispatching instance's record when there
was one, and the calling session's id, and the session mapping at the
workspace root repeats the dispatch id and the calling session's id. `niwa
reap` deletes both. After that the parent survives only as an attribute on
the worker's telemetry, in whatever collector the worker was pointed at, if
any, and no local command reads it.

So an operator looking back at a finished tree has nothing in the workspace
to read. They can't say which session asked for a dispatch, can't walk from
a leaf to the session a person was typing into, and can't say who the tree
worked for, because no record anywhere names the person a dispatched session
acts on behalf of.

The parent ids niwa does record have a second problem: nothing says how far
they can be trusted. The calling session's id is read from a variable in the
caller's environment, which the caller controls; a Codex worker inherits its
dispatcher's value, so its own dispatches name its grandparent. The parent
dispatch comes from the working directory or from a `--parent-from` path the
caller supplies. niwa writes all of these the same way. A tool that caps a
child at its parent's grant, routes a session's questions to whoever directs
it, or attributes a decision to a person would have to take a claimed parent
as fact. Each of those is planned work, and each needs this tree readable
first.

## Goals

- An operator reconstructs a finished, reaped tree of dispatched sessions
  from a record in the workspace: for every dispatch, the session that asked
  for it, the dispatch that session belonged to, and the person the tree acts
  for.
- For any session below the first level, the operator reads the session that
  directs it.
- Beside every recorded parent, the operator and any tool read how niwa came
  by it, and a parent niwa didn't observe never reads as one it did.
- The record is niwa's own, kept locally, and reads the same in a workspace
  with no workflow engine installed.

## User Stories

- **As an operator** who had session A dispatch session B and then reaped
  both, I want to ask niwa about B the next day and read that B's parent is
  A, A's parent is my own session, and both acted for me, so that I can
  reconstruct what ran on my behalf without the instances, the mappings or a
  telemetry backend.
- **As an operator running a coordinator** that dispatches its workers from
  the workspace root, I want each worker's record to link it to the
  coordinator's dispatch, marked as a link niwa found through its session
  mappings rather than one it saw, so that the tree isn't flat in the way
  coordinators are actually run.
- **As a coordinator session** about to route a worker's question, I want to
  read the worker's lineage as a versioned JSON document that marks each
  parent link observed or not, so that I treat a handed parent as handed and
  never guess from the shape of the record.
- **As the author of a gate script** that reads authority relations from
  `niwa authority show`, I want the live record to carry the same parent
  source and principal as the lineage entry, so that my gate and the
  lineage reader never disagree about a running session.
- **As a reviewer** checking the trust boundary, I want a dispatch that
  claims a calling session other than the one niwa's records say it came
  from to be recorded with that claim marked unverified, so that a false
  claim can't pass as an observed parent.
- **As an operator** who declared who dispatches on this machine act for, I
  want a session two levels down to name that person directly, so that the
  answer holds even when the records above it are missing.

## Requirements

Terms used below. A **session id** is well formed when it is 8 to 128
characters of letters, digits, `_` or `-`, the check niwa already applies to
the calling-session variable. An instance's **newest mapping** is, among the
session mappings naming that instance, the one with the latest recorded
creation time, ties going to the lower session id: the rule `niwa list`
already uses. An entry is **kept** when the dispatch leaves its instance in
place when `niwa dispatch` returns. A **name value** is 1 to 32 lowercase
letters, digits or `-`.

### The record

- **R1. One lineage entry per kept dispatch.** Every `niwa dispatch` that
  keeps its instance has exactly one lineage entry, keyed by its dispatch id,
  in a store at `<workspace root>/.niwa-lineage/`, outside every instance
  and outside the workspace configuration directory. Each entry holds the
  fields below; the JSON names are the ones R15's documents print.

  | Field | Value |
  |---|---|
  | `schema` | `niwa.lineage.entry/v1` |
  | `dispatch_id` | the dispatch's version-4 UUID |
  | `instance` | the instance's directory name |
  | `agent` | the dispatched agent's name, as the session mapping records it (`claude`, `codex`): a name value |
  | `origin` | a name value; this niwa writes `dispatch` |
  | `written_at` | when the entry was written, RFC 3339 in UTC |
  | `session_id` | the worker's session id, absent until captured (R2) |
  | `parent_session` | `{id, source, mapped_session_id}` (R4); `id` empty when there is none, `mapped_session_id` present only when `source` is `unverified` |
  | `parent_dispatch` | `{id, source}` (R5); `id` empty when there is none |
  | `principal` | `{name, source, trust}` (R7) |

- **R2. Written before launch, completed once, otherwise never rewritten.**
  The entry is written before the worker process starts, beside the
  authority record. When the dispatch then removes its instance (a refusal,
  a rollback, a detached launch whose session capture fails), it removes the
  entry too. When niwa captures the worker's session id it adds `session_id`
  to the entry; a dispatch that keeps its instance without capturing one (a
  foreground turn that ends with no session record) keeps its entry without
  it. Nothing else ever changes an entry.
- **R3. Write failures.** If the entry can't be written before launch, the
  dispatch refuses and removes its instance, exactly as when the authority
  record can't be written. A dispatch id that already has an entry is a
  write failure. If adding the captured session id fails, dispatch prints one
  warning naming the entry, keeps the entry without `session_id`, and
  succeeds.

### Parent links and how niwa came by them

- **R4. The parent session link and its source.** The parent session id is
  read as today, from the variable the dispatched agent's launch spec names.
  Its source is decided by the first row that matches:

  | Situation | `source` | `id` |
  |---|---|---|
  | The variable is unset or not a well-formed session id | `none` | empty |
  | `--parent-from` was given | `handed` | the variable's value |
  | The working directory is in no instance | `handed` | the variable's value |
  | The working directory's instance has no session mapping | `handed` | the variable's value |
  | Its newest mapping names the same session id | `observed` | the variable's value |
  | Its newest mapping names a different session id | `unverified` | the variable's value, with `mapped_session_id` set to the mapping's id |

- **R5. The parent dispatch link and its source.** The parent dispatch id is
  decided by the first row that matches:

  | Situation | `source` | `id` |
  |---|---|---|
  | The working directory's instance has an authority record | `observed` | that record's dispatch id |
  | `--parent-from` named an instance with an authority record | `handed` | that record's dispatch id |
  | The working directory is in no instance, no `--parent-from` was given, the variable's value is a lowercase UUID, and the session mapping keyed by it carries a dispatch id | `mapped` | that mapping's dispatch id |
  | Otherwise | `none` | empty |

  The `mapped` row is how a worker dispatched from the workspace root, the
  way coordinators run, links to the coordinator's dispatch. It is not
  `observed`: niwa found a mapping for the session the caller named, but did
  not see that the caller is that session. A variable that is well formed
  but not a lowercase UUID skips the lookup, since the mapping store keys
  only on those. Throughout, `observed` means the caller's claimed session
  id and working directory matched niwa's own records, not that niwa proved
  the caller is that session. A parent record that exists but
  can't be read still refuses the dispatch, as it does today.
- **R6. Recorded, never refused.** No dispatch is refused because a parent
  link is `handed`, `mapped`, `unverified` or `none`. A resumed session and a
  Codex worker both legitimately present an id other than the one niwa
  mapped.

### The person a session acts for

- **R7. The principal, its source and its trust.** Every entry names one
  principal, the person the dispatched session acts on behalf of:

  | Situation | `source` | `name` | `trust` |
  |---|---|---|---|
  | The parent dispatch link has an id and that dispatch's entry is readable | `inherited` | the parent entry's principal name | `observed` only when the parent dispatch link is `observed`, the parent session link is not `unverified`, and the parent entry's principal trust is `observed`; otherwise `handed` |
  | Otherwise, the personal niwa config declares a principal (R8) | `declared` | the declared value | `observed`, or `handed` when the parent dispatch link has an id |
  | Otherwise | `none` | empty | `observed`, or `handed` when the parent dispatch link has an id |

  The second and third rows also apply when a parent dispatch link has an id
  but its entry is missing, unreadable or of a later version; the entry's
  `parent_dispatch` still names the parent, so a reader sees a parent whose
  principal wasn't inherited, and the trust is `handed` because the person
  should have come from that parent. A readable parent entry whose principal is
  `none` is still inherited: the child's principal is `inherited` with an
  empty name, never filled in from this machine. The principal's `source` is
  a provenance label; its `trust` is what says whether niwa observed a match
  for it. No gate and no permission decision may read the principal.
- **R8. Declaring the person.** The principal is opt-in. The personal niwa
  config's `[global]` table accepts `principal = "<value>"`; nothing else
  supplies a name, and the workspace config and overlays have no such key.
  A value is valid when it is 1 to 128 characters of letters, digits, `.`,
  `_`, `-`, `@` or `+`. An invalid value refuses every dispatch on that
  machine with an error naming the key and the config file. No dispatch flag
  sets or overrides the principal.
- **R9. A person, never a credential.** niwa never derives the principal
  from a token, a keyring login, a GitHub account, the operating-system
  account or any network call, and recording it changes nothing about which
  credentials a session holds.

### Durability

- **R10. Entries outlive the instance.** An entry survives each of: `niwa
  reap` along every path that removes a session (the mapped instance, the
  stored mapping whose instance is gone, and the unmapped backstop); `niwa
  destroy` of the instance, with or without `--keep-session`; `niwa reset`;
  an on-reap command that fails, and one that has failed often enough for
  its mapping to be dropped; the deletion of the session mapping; and every
  configuration refresh, concurrent dispatches included, because the store
  is not inside any directory a refresh copies or swaps.
- **R11. How long entries last.** Entries last as long as the workspace root.
  `niwa destroy --workspace` removes them with it; nothing in this feature
  removes one earlier.
- **R12. Lineage stays with the workspace that wrote it.** Entries are never
  carried by any tool's session export or import. A session imported from
  another host, with no mapping in this workspace, dispatches with its parent
  session `handed`, never `observed`; and because session ids are not
  scoped to a host, `show --session <id>` on a workspace that has seen that
  id before can match an older, unrelated entry.

### Reading it

- **R13. Selecting an entry.** `niwa lineage show` takes one selector:
  - a dispatch id as its positional argument, matching that entry (unlike
    `niwa authority show`, which takes no positional argument);
  - `--session <id>`, matching entries whose `session_id` is that id;
  - `--instance <name>`, matching entries for that instance name;
  - none, or `-C <dir>`: the instance containing the working directory (or
    `<dir>`); the entry for its authority record's dispatch id when it has
    one, otherwise the entries for that instance name.

  Where several entries match, the chain starts from the latest
  `written_at`, ties going to the lower dispatch id, and every match is
  reported (R15). Two selectors together, or `-C` with any other selector,
  is a usage error (exit 2). An empty, relative or nonexistent `-C` exits 1.
  A selector that matches nothing prints `no lineage entry` and exits 1:
  that includes a workspace with no store, a working directory in the
  workspace but in no instance, and an authority record whose dispatch id
  has no entry. Run outside a workspace it exits 1 naming the missing
  workspace.
- **R14. Showing the chain.** `show` prints the selected entry and then each
  ancestor reached through `parent_dispatch` ids, whatever their source,
  nearest first. For every level it prints the dispatch id, the worker
  session id, the parent session with its source, the parent dispatch with
  its source, and the principal with its source and trust, and every level
  whose parent session id is set prints `directed by <session id>`. When
  more than one entry matched, it prints `<n> entries matched; showing the
  latest` and the other matches' dispatch ids; when the match came from the
  instance-name fallback it prints `selected by instance name`. The layout
  is the design's; tests may rely on the values, `(not observed)`,
  `directed by`, those two notices, and the stop text below. The walk ends
  as the table says, the selected entry counting as level 1:

  | When | Last level printed | Stop text | JSON `stop` |
  |---|---|---|---|
  | An entry's parent dispatch id is empty | that entry | none | `root` |
  | A parent dispatch id has no entry | `<id> not on record` | none | `not-on-record` |
  | An entry can't be read or fails validation | `<id> unreadable: <reason>` | none | `unreadable` |
  | An entry is of a later version | `<id> later version <schema>` | none | `later-version` |
  | A parent dispatch id was already printed | the level before it | `stopped: cycle at <id>` | `cycle` |
  | Level 64 has a parent dispatch id | level 64 | `stopped: 64 levels` | `depth` |

  `show` exits 0 when the selected entry is readable, including one of a
  later version, and exits 1 when it can't be read or fails validation.
- **R15. Versioned documents for tools.** `show --json` prints
  `{"schema": "niwa.lineage.chain/v1", "chain": [...], "stop": "<reason>",
  "matched": <n>, "matches": [<dispatch id>, ...],
  "instance_name_fallback": <bool>}`, where `chain` holds one object per
  level printed, `stop` is a value from R14's table, `matched` is how many
  entries the selector matched, `matches` lists all of their dispatch ids,
  latest first, and `instance_name_fallback` is true when the match came
  from the instance-name fallback. A readable level is the entry as stored; a
  later version is `{"dispatch_id", "schema", "known": false}` and nothing
  else of it is read; an unreadable entry is `{"dispatch_id", "error"}`; a
  parent with no entry is `{"dispatch_id", "error": "not on record"}`.
  `niwa lineage list --json` prints `{"schema": "niwa.lineage.list/v1",
  "entries": [...]}` in the same entry shapes. Every source, trust and
  origin value is printed as stored, and every stored member is printed as
  data, never interpreted.
- **R16. The format is additive.** Within `niwa.lineage.entry/v1`, a reader
  ignores fields it doesn't know and keeps them: completing an entry with its
  session id leaves every other stored field, unknown ones included, as it
  was. Source, trust and origin values this niwa doesn't know are accepted
  when they are name values. A document with a duplicate member name is
  unreadable. Which files are entries goes by name alone: a file named
  `<dispatch id>.json` is an entry, and one that isn't a regular file or is
  larger than the read cap (R23) is reported as unreadable; any other name
  is skipped. Whether the content parses never decides it.
- **R17. Listing entries.** `niwa lineage list` prints every entry in the
  workspace, latest `written_at` first, ties broken by dispatch id, one line
  each: dispatch id, worker session id, parent session id with its source,
  and principal. An unreadable entry prints as its dispatch id and error, and
  the rest still list; `list` exits 0 unless the store itself can't be read.
  A workspace with no store lists nothing and exits 0.
- **R18. Only `observed` is observed.** The rule covers three fields:
  `parent_session.source`, `parent_dispatch.source` and `principal.trust`.
  In them, a value other than `observed`, `mapped` and any value this niwa
  doesn't know included, is not observed. The human output prints such a
  value followed by `(not observed)`, except `none` on a link, which means
  there is no parent and prints alone. The JSON prints every value as
  stored, and the guide (R22) tells tool authors to apply the same rule.
  `principal.source` is a provenance label and is printed without
  annotation.
- **R19. The live read agrees.** The authority record gains
  `parent_session_source` and `principal` (`{name, source, trust}`), with
  the same values as the lineage entry's `parent_session.source` and
  `principal`. Its existing `parent_dispatch_source` keeps
  its values, and `cwd`, `anchor` and `none` there correspond to the
  entry's `observed`, `handed` and `none`; a `mapped` parent dispatch is
  recorded there as `none`, since that field names only a record found
  through an instance. So `niwa authority show` and `niwa lineage show`
  agree while the instance exists. An authority record written before this
  feature lacks the new fields, and `niwa authority show` prints them as
  `unrecorded`; an older niwa reading a newer record ignores them, as the
  record format already allows.

### Constraints

- **R20. No other tool required.** Writing and reading lineage works with no
  workflow engine installed and reads nothing another tool writes.
- **R21. Local and offline.** Writing an entry and every lineage read touch
  only local files and make no network call. The principal is not added to
  the worker's telemetry attributes.
- **R22. The guide states the trust boundary.** `docs/guides/dispatch-lineage.md`
  gains a section on the lineage record stating: a person's own interactive
  session has no mapping, so a first-level parent session is `handed`;
  `--parent-from` makes both links `handed`; a worker dispatched from the
  workspace root links to its coordinator as `mapped`, which is not
  observed; `unverified` is expected after a resume and for a Codex worker's
  dispatches, and the parent dispatch link is the one to follow for the
  tree; a resumed Claude session that got a new session id keeps its record
  under the original id; only `observed` counts as observed, unknown values
  included; the principal is empty until `[global] principal` is set, with
  an example of setting it, and a worker can read and pass on its own
  principal; any process running as the same user can edit the store, so
  every entry is advisory; lineage never travels with a session export or
  import; `show --session` and `--instance` may match more than one entry;
  `show` takes a positional dispatch id; a tool treats a `stop` value it
  doesn't know as not `root`; `observed` means the caller's claimed session
  id and working directory matched niwa's records, which a same-user
  process that reads the mapping names can arrange; lineage records the
  dispatcher at dispatch time, not re-parenting by message or a
  coordinator's successor; clearing `[global] principal` doesn't change
  entries already written; the declared principal is machine-wide, readable
  by any process running as the user, and the environment can point niwa at
  another personal config; whether a version-controlled workspace root picks
  up `.niwa-lineage/`, with the ignore line that keeps it out; and entries
  last until the workspace is destroyed.
- **R23. Store safety.** The store's directory is mode 0700, set on the
  first write in each process even when an existing directory is wider; if
  it can't be set (another user owns it, say), the write refuses. Each entry
  is 0600. An entry is written to a temporary file and renamed into place,
  so a reader never sees a partial entry. Reads cap a file at 8 KiB, and a
  completed entry that would exceed it is not written. Every field this
  niwa knows is re-validated against the shape R1, R4, R5, R7 and R8 give it
  when read, and an entry failing any check is unreadable. A dispatch id
  taken from any record (a parent link, a mapping, an authority record) is
  checked to be a UUID before it names a file. An entry file whose mode
  grants any access beyond its owner is unreadable. `show` and `list` only
  read: they neither tighten the directory nor warn about its mode.
- **R24. Bounded entries.** Every field is length-bounded (ids by their
  formats, the instance name by niwa's instance naming, the principal by
  R8, name values by their rule), so an entry this niwa writes is at most
  4 KiB.
- **R25. Nothing else changes.** Dispatch argv, the permission mode,
  settings, the telemetry attributes and every existing refusal are
  unchanged. A workspace that never dispatches gets no store.

## Acceptance Criteria

Tests that need a parent session set `CLAUDE_CODE_SESSION_ID` and write the
session mappings the situation calls for; tests that need a credential put a
stub `gh` first on `PATH` and set `GH_TOKEN`.

The tree:
- [ ] With `principal` declared, dispatch A from the workspace root with the
      session variable set to S0; from inside A's instance, with the variable
      set to A's mapped session id, dispatch B; reap both. `niwa lineage
      show --session <B's session>` prints B's parent session as A's session
      id `observed`, B's parent dispatch as A's dispatch id `observed`, then
      A's level with parent session S0 `handed` and parent dispatch `none`,
      stop `root`; both levels name the declared principal. B's level prints
      `directed by <A's session id>` and A's prints `directed by S0` (R1,
      R4, R5, R7, R10, R14).
- [ ] Extend that tree with C dispatched from inside B's instance: `show` on
      C prints three levels, nearest first (R14).
- [ ] A coordinator K is dispatched; from the workspace root, with the
      variable set to K's mapped session id, K dispatches worker W. W's entry
      has parent dispatch K's dispatch id with source `mapped`, parent
      session `handed`, and principal trust `handed`; `show` on W prints two
      levels and stop `root`, with `mapped (not observed)` (R5, R7, R14,
      R18).
- [ ] In the first tree, A's principal source is `declared` and B's is
      `inherited` with trust `observed`; without `principal` declared, both
      read source `none` with an empty name, whatever the OS account (R7,
      R8, R9).
- [ ] `show --json` on B prints schema `niwa.lineage.chain/v1`, two chain
      objects carrying every R1 field, stop `root`, `matched` 1 and
      `matches` holding B's dispatch id (R1, R15).

Parent links:
- [ ] From inside A's instance with the variable set to a well-formed id
      other than A's mapped one, the dispatch launches and B's entry has
      parent session source `unverified`, `id` the claimed value and
      `mapped_session_id` A's, and B's principal trust is `handed` (R4, R6,
      R7).
- [ ] Each remaining R4 row gives its source: variable unset (`none`),
      variable `not-a-valid-id!` (`none`), dispatch from the workspace root
      (`handed`), from an instance with no mapping (`handed`), and with
      `--parent-from` (`handed`). Every one of these dispatches launches
      (R4, R6).
- [ ] A dispatch run from the workspace root with `--parent-from` naming A's
      instance records parent dispatch A's id with source `handed` and
      principal trust `handed`, even when its variable names a mapped
      session; one from an instance with no authority record records parent
      dispatch `none`; one from the workspace root whose variable names no
      mapping, or is well formed but not a lowercase UUID, records parent
      dispatch `none` (R5, R7).
- [ ] A dispatch from inside A's instance with the variable unset records
      parent dispatch `observed`, parent session `none`, and an inherited
      principal with trust `observed` (R7).
- [ ] An unreadable parent authority record still refuses the dispatch (R5).
- [ ] A dispatch from inside an instance whose lineage entry was deleted,
      edited to be unreadable, or edited to schema `niwa.lineage.entry/v2`
      records that parent dispatch id and, with `principal` declared, source
      `declared` and trust `handed`; with nothing declared, source `none`,
      an empty name and trust `handed` (R7).
- [ ] A grandchild dispatched from inside a child whose principal trust is
      `handed` records principal trust `handed`, even through an `observed`
      parent dispatch link (R7).
- [ ] A child of a parent entry whose principal is `none` records
      `inherited` with an empty name, even with `principal` declared (R7).
- [ ] A dispatch from a session imported from another host, which has no
      mapping in this workspace, records parent session `handed` (R12).
- [ ] A dispatch from the workspace root whose variable names a mapping that
      carries no dispatch id records parent dispatch `none` and parent
      session `handed` (R4, R5).
- [ ] An imported session whose id collides with a mapping this workspace
      already holds, dispatching from the workspace root, records parent
      dispatch `mapped` and never `observed` (R5, R12).
- [ ] A dispatch from inside an instance whose authority record exists but
      which has no session mapping records parent session `handed`, parent
      dispatch `observed`, and an inherited principal with trust `observed`
      (R4, R5, R7).

The principal:
- [ ] `principal = "dana@example.com"` is accepted; `principal = ""`,
      `principal = "Dana G"` and a 129-character value each refuse dispatch
      with an error naming `principal` and the config file (R8).
- [ ] `niwa dispatch --help` lists no flag that sets the principal, and a
      `principal` key in the workspace config or an overlay has no effect
      (R8).
- [ ] With `principal` declared, `GH_TOKEN` set, and a stub `gh` reporting a
      different login, the entry's principal is the declared value (R9).

Writing:
- [ ] A dispatch refused at authority resolution, and one rolled back during
      provisioning, leave no entry (R2).
- [ ] A detached dispatch whose session capture fails leaves no entry; a
      foreground one whose turn ends with no session record keeps an entry
      without `session_id` (R2).
- [ ] A detached dispatch whose capture succeeds has an entry whose
      `session_id` equals the session mapping's id (R2).
- [ ] With the store directory unwritable, dispatch refuses and leaves no
      instance; with an entry already present for the minted dispatch id,
      dispatch refuses the same way and the existing entry's bytes are
      unchanged (R3).
- [ ] With the step that adds `session_id` made to fail through a test seam,
      dispatch prints one warning naming the entry, exits 0, and the entry
      has no `session_id` (R3).
- [ ] An entry's bytes are identical before and after a reap, a second
      dispatch and a `niwa apply` (R2).
- [ ] An entry holding a field this niwa doesn't know keeps it, byte for
      byte, after its session id is added; an entry whose completion would
      exceed 8 KiB is left unchanged and dispatch warns (R16, R23).

Durability:
- [ ] An entry survives each R10 path, each tested on its own: mapped reap,
      stored-mapping reap, backstop reap, `niwa destroy`, `niwa destroy
      --keep-session`, `niwa reset`, a failing on-reap command, an on-reap
      command failing until its mapping is dropped, and `niwa apply`
      refreshing the configuration directory (R10).
- [ ] Entries are written under `<workspace root>/.niwa-lineage/`, and no
      file for them exists under `<workspace root>/.niwa/` or any instance
      (R1).
- [ ] With a test seam holding one dispatch's configuration refresh between
      its copy and its swap, a second dispatch writes and completes its
      entry, and after the first refresh finishes both entries are present
      (R10).
- [ ] `niwa destroy --workspace` removes the store (R11).

Reading:
- [ ] `show` selects correctly by positional dispatch id, `--session`,
      `--instance`, the working directory and `-C`; with two entries sharing
      a session id or instance name it starts from the later `written_at`,
      prints `2 entries matched; showing the latest` with the other id, and
      the JSON's `matches` lists both; with equal `written_at` it starts
      from the lower dispatch id (R13, R14, R15).
- [ ] Run inside an instance with no authority record, `show` with no
      selector starts from the latest entry for that instance name, prints
      `selected by instance name`, and the JSON has
      `"instance_name_fallback": true`; selection by dispatch id gives
      `false` (R13, R14, R15).
- [ ] Two selectors, and `-C` with any other selector, exit 2; `-C` that is
      empty, relative or nonexistent exits 1; outside any workspace `show`
      exits 1 naming the missing workspace (R13).
- [ ] `show` prints `no lineage entry` and exits 1 for an unknown dispatch
      id, in a workspace with no store, from the workspace root with no
      selector, and from an instance whose authority record's dispatch id
      has no entry (R13).
- [ ] With the parent's entry deleted, `show` prints `<id> not on record`,
      stop `not-on-record`, exit 0, and the JSON's last chain object is
      `{"dispatch_id", "error": "not on record"}` (R14, R15).
- [ ] With two entries edited to name each other as parent dispatch, `show`
      prints `stopped: cycle at <id>` and JSON stop `cycle`; a hand-built
      chain of 70 entries prints 64 levels, `stopped: 64 levels` and JSON
      stop `depth`; a chain of exactly 64 entries ends with stop `root`
      (R14).
- [ ] An ancestor edited to be unreadable ends the walk with `<id>
      unreadable: <reason>`, JSON stop `unreadable` and a chain object
      `{"dispatch_id", "error"}`, exit 0 (R14, R15).
- [ ] A parent dispatch id edited to `../../etc/passwd` is never used as a
      path: the walk reports that level unreadable (R23).
- [ ] An ancestor with its schema changed to `niwa.lineage.entry/v2` ends
      the walk with `<id> later version niwa.lineage.entry/v2`, JSON stop
      `later-version`, and a chain object with `known: false` and nothing
      else of the entry; selecting that entry directly exits 0 (R14, R15).
- [ ] Entries edited to hold, each on its own, a malformed dispatch id, a
      `dispatch_id` differing from the file name, an `instance` with a
      space, an `agent` of `Claude Code`, a `written_at` that isn't RFC 3339,
      a malformed `session_id` or parent id, a source or trust of `Not
      Valid`, a 129-character principal, a duplicate member, a missing
      `schema`, or a `schema` that isn't a `niwa.lineage.entry` version are each
      unreadable; one with `origin` `cron` and an extra field reads
      normally; `show` on an unreadable selected entry exits 1 (R14, R16,
      R23).
- [ ] Files named `<dispatch id>.json` that are a symlink, a FIFO, a
      directory, or larger than 8 KiB are each listed by `list` as
      unreadable without hanging; a file named `notes.txt` and a leftover
      `.<id>.<random>.tmp` are not listed (R16, R17, R23).
- [ ] `niwa lineage list --json` over a readable entry, an unreadable one
      and a later-version one prints schema `niwa.lineage.list/v1` with an
      entry as stored, a `{"dispatch_id", "error"}` object and a
      `{"dispatch_id", "schema", "known": false}` object (R15).
- [ ] `niwa lineage list` orders three entries by `written_at`, newest
      first, and two with equal `written_at` by dispatch id; each line holds
      the dispatch id, session id, parent session id with its source, and
      principal; an unreadable entry lists as its dispatch id and error
      among the others, exit 0; a workspace that never dispatched lists
      nothing, exit 0 (R17).
- [ ] An entry whose parent session source is edited to `vouched` prints
      `vouched (not observed)` in `show` and `"vouched"` in `show --json`;
      one whose principal trust is edited to `vouched` prints the same way;
      a `none` link prints `none` alone; a `declared` principal source
      prints with no annotation (R18).
- [ ] While an instance exists, `niwa authority show --json` and `niwa
      lineage show --json <dispatch id>` report the same parent session
      source and principal, and the authority record's
      `parent_dispatch_source` `cwd` or `anchor` matches the entry's
      `observed` or `handed`; an authority record without the new fields
      prints them as `unrecorded` (R19).
- [ ] For the root-dispatched worker W while its instance exists, `niwa
      authority show --json` has `parent_dispatch_source` `none` while W's
      entry has `parent_dispatch.source` `mapped`, and both report the same
      `parent_session_source` and `principal` (R19).

Constraints:
- [ ] The lineage functional tests pass with no workflow engine on PATH
      (R20).
- [ ] With `HTTPS_PROXY` and `HTTP_PROXY` pointing at a closed local port,
      dispatch writes its entry and `show` and `list` succeed; the worker's
      `OTEL_RESOURCE_ATTRIBUTES` holds no principal attribute (R21).
- [ ] `docs/guides/dispatch-lineage.md` has a section stating each point R22
      lists (R22).
- [ ] The store directory is mode 0700 and entries 0600, including when the
      directory existed at 0755 before the first write; with the mode change
      made to fail through a test seam, the dispatch refuses; with the rename
      made to fail through a test seam, no file exists at the entry's final
      path and the dispatch refuses (R3, R23).
- [ ] An entry file chmodded to 0644 reads as unreadable; running `show` and
      `list` over a 0755 store leaves its mode at 0755 and prints no warning
      (R23).
- [ ] An entry with every field at its maximum length is at most 4 KiB
      (R24).
- [ ] The existing dispatch, authority and telemetry tests pass unchanged,
      and a workspace that has never dispatched has no store (R25).

## Out of Scope

- **Capping a child at its parent's authority.** That check will read this
  record's links; it is separate work. This feature records and refuses
  nothing new.
- **Hardening the trust boundary.** A link niwa can't check is recorded as
  handed or mapped, not made checkable through signatures or a broker
  process.
- **Reading any workflow engine's own session records, or carrying lineage
  in a session export or import.** A workflow engine may record which run
  started which; niwa neither reads that nor needs it, and lineage never
  travels with an exported or imported session (R12).
- **Carrying the principal onto recorded decisions, classifying where a
  session escalated, and telling a session at start who directs it.** Each
  reads this record and each is separate work.
- **Lineage for `niwa watch` review sessions.** Deferred: they aren't part
  of the coordinator and worker tree, and writing entries there reaches into
  watch's cleanup path.
- **Lineage across machines or workspaces, or a copy in a cloud store.** The
  record is local to the workspace that dispatched; `FindInstance` and
  `--parent-from` already confine parents to one workspace.
- **Changing which credentials a session holds.**
- **Guessing a parent from the process tree, or reviving a registry of live
  sessions.**
- **Pruning entries.** Entries last as long as the workspace root (R11); a
  command to remove old entries is later work if the store's size ever
  matters.
- **Recording the grant on the entry.** An audit field for the grant is
  expected later; R16's additive rule is what lets it arrive without a new
  version.
- **Back-filling entries for dispatches made before this feature.**

## Known Limitations

- **Every entry is advisory.** Any process running as the same user can edit
  the store, as it can the authority record and the session mappings, and
  any dispatched worker can write another dispatch's entry, which later
  children then inherit from.
- **`observed` is a match, not a proof.** A caller that sets its session
  variable to an id it read from the mapping store's file names and runs
  from that instance gets `observed` links, and one at the workspace root
  can choose which coordinator it appears under as `mapped`. Session ids
  aren't scoped to a host, so an imported session whose id this workspace
  has mapped before can get a false `observed` or `mapped` link too.
- **The declared principal is machine-wide.** It comes from the personal
  config, so it appears in the entries of every workspace dispatched from
  that machine, any process running as the user can read it, and the
  environment can point niwa at a different personal config file.
- **Tie-breaks use a writable field.** Choosing among several matches by
  `written_at` relies on a field any same-user process can edit.
- **Clearing the principal doesn't scrub.** Entries written while a
  principal was declared keep it, and their descendants inherit it, until
  the workspace is destroyed.
- **A first-level parent session is always handed.** A person's own
  interactive session has no instance and no mapping, so niwa has nothing to
  check its id against.
- **`unverified` is often benign.** A resumed session or a Codex worker's
  dispatch reads `unverified` because its session variable differs from the
  mapping; the parent dispatch link, found from the working directory, is
  the one to follow for the tree.
- **A coordinator at the workspace root is linked, not observed.** Its
  workers' parent dispatch is `mapped`: niwa found the coordinator's mapping
  through the session id the worker's caller named.
- **Re-parenting and recycling aren't recorded.** A worker re-parented by a
  message, or handed to a successor when its coordinator is recycled, keeps
  the original dispatcher as its parent; lineage names who dispatched, not
  who directs now.
- **The principal is a label, not an authenticated identity,** and it is
  empty until someone declares it.
- **Watch review sessions have no entries** until a later follow-up adds
  them.
- **The store grows by one entry per dispatch** until the workspace is
  destroyed.

## Decisions and Trade-offs

- **Record a false parent claim; don't refuse it.** Refusing a dispatch whose
  session variable disagrees with niwa's mapping would break resumed
  sessions and every dispatch from a Codex worker, and the trust boundary is
  meant to be stated, not enforced harder. Alternative considered: refuse,
  with an override flag. Rejected for those two legitimate cases.
- **Entries last as long as the workspace root.** No niwa store has an
  age-based rule to copy, an entry is small, and pruning an ancestor would
  strand every descendant's walk. Alternatives considered: an age limit run
  at reap, and a count cap like the watch staging limit. Both deferred until
  size matters.
- **The store sits beside the configuration directory, not in it.** Inside
  it, every dispatch's configuration refresh copies carried state and swaps
  the directory, and an entry written by a concurrent dispatch inside that
  window would be lost. Outside it, no refresh touches the store, and no
  instance scan counts it, because it holds no instance state.
- **Root dispatches link through the session mappings.** Coordinators
  dispatch from the workspace root, so a parent link found only through the
  working directory would leave every real tree one level deep. Looking up
  the caller's session id in the mapping store links the worker to the
  coordinator's dispatch; the link is marked `mapped` because niwa did not
  see that the caller is that session. Alternative considered: have the
  reader join on session ids at read time. Rejected because it decides the
  link after the mapping it depends on is gone.
- **The principal is opt-in.** A worker can read its own principal and pass
  it to its model provider, so niwa records a name only when someone
  declares one. Alternatives considered: the OS account name as a fallback
  (records a name without the user choosing to), the GitHub login (a
  credential's identity, and a network call), the version-control author
  setting (often shared or absent), and a dispatch flag (lets any level
  rename the person).
- **A separate store, not a longer-lived mapping or authority record.** The
  mapping is keyed by session id, which a dispatch doesn't always learn, and
  is deleted at reap by design; the authority record lives inside the
  instance. A new store keyed by dispatch id leaves both contracts alone.
- **The principal carries its own trust.** An inherited principal is only as
  good as the links it came through, so the entry records a trust value
  beside the source, capped at `handed` when either link above it wasn't
  observed. A declared principal is `observed`: it is what this machine's
  config says.
- **The format is additive.** Readers keep fields they don't know and accept
  unknown name values, so a later field (an audit record of the grant, say)
  arrives without a version change.
- **The principal stays off telemetry.** A person's name shouldn't leave the
  machine on an attribute; the local record answers the question.
