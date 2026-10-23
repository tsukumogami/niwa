---
schema: prd/v1
status: Accepted
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
upstream: docs/briefs/BRIEF-dispatch-lineage.md
---

## Status

Accepted

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
- **As a coordinator session** about to route a worker's question, I want to
  read the worker's lineage as a versioned JSON document that marks each
  parent link observed or handed, so that I treat a handed parent as handed
  and never guess from the shape of the record.
- **As the author of a gate script** that reads authority relations from
  `niwa authority show`, I want the live record to carry the same parent
  source and principal as the lineage entry, so that my gate and the
  lineage reader never disagree about a running session.
- **As a reviewer** checking the trust boundary, I want a dispatch that
  claims a calling session other than the one niwa's records say it came
  from to be recorded with that claim marked unverified, so that a false
  claim can't pass as an observed parent.
- **As an operator** reading a session two levels down, I want its record to
  name the person it acts for directly, so that the answer holds even when
  the records above it are missing.
- **As an operator** whose session was started by `niwa watch` or from a
  plain terminal, I want its record to say it has no parent session and
  still name the person it acts for, so that "on whose behalf" has an answer
  at the root.

## Requirements

Terms used below. A **session id** is well formed when it is 8 to 128
characters of letters, digits, `_` or `-`, the check niwa already applies to
the calling-session variable. An instance's **newest mapping** is, among the
session mappings naming that instance, the one with the latest recorded
creation time, ties going to the lower session id: the rule `niwa list`
already uses. An entry is **kept** when the dispatch leaves its instance in
place when `niwa dispatch` returns.

### The record

- **R1. One lineage entry per kept dispatch.** Every `niwa dispatch` that
  keeps its instance has exactly one lineage entry, keyed by its dispatch id,
  in a store at the workspace root outside every instance. Each entry holds
  the fields below; the JSON names are the ones R15's documents print.

  | Field | Value |
  |---|---|
  | `schema` | `niwa.lineage.entry/v1` |
  | `dispatch_id` | the dispatch's version-4 UUID |
  | `instance` | the instance's directory name |
  | `agent` | the dispatched agent's name, as the session mapping records it (`claude`, `codex`): 1 to 32 lowercase letters, digits or `-` |
  | `origin` | `dispatch`, or `watch` (R12) |
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
  found as today: through `--parent-from`, else from the working directory's
  instance. Its source is `observed` when the working directory's instance's
  authority record gave it, `handed` when `--parent-from` did, and `none`
  when no parent record was found (`id` empty). A parent record that exists
  but can't be read still refuses the dispatch, as it does today.
- **R6. Recorded, never refused.** No dispatch is refused because a parent
  link is `handed`, `unverified` or `none`. A resumed session and a Codex
  worker both legitimately present an id other than the one niwa mapped.

### The person a session acts for

- **R7. The principal, its source and its trust.** Every entry names one
  principal, the person the dispatched session acts on behalf of:

  | Situation | `source` | `name` | `trust` |
  |---|---|---|---|
  | The parent dispatch link has an id and that dispatch's entry is readable | `inherited` | the parent entry's principal name | `observed` if the parent dispatch link is `observed` and the parent entry's principal trust is `observed`; otherwise `handed` |
  | Otherwise, the personal niwa config declares a principal (R8) | `declared` | the declared value | `observed` |
  | Otherwise, the operating-system account's name is valid by R8's rule | `os-account` | the account name | `observed` |
  | Otherwise | `none` | empty | `observed` |

  The second to fourth rows also apply when a parent dispatch link has an id
  but its entry is missing, unreadable or of a later version; the entry's
  `parent_dispatch` still names the parent, so a reader sees a parent whose
  principal wasn't inherited. A readable parent entry whose principal is
  `none` is still inherited: the child's principal is `inherited` with an
  empty name, never filled in from this machine. The principal's `source` is
  a provenance label; its `trust` is what says whether niwa observed it.
- **R8. Declaring the person.** The personal niwa config's `[global]` table
  accepts `principal = "<value>"`. A value is valid when it is 1 to 128
  characters of letters, digits, `.`, `_`, `-`, `@` or `+`. An invalid value
  refuses every dispatch and every `niwa watch` review launch on that
  machine with an error naming the key and the config file. No dispatch flag sets or overrides the principal.
- **R9. A person, never a credential.** niwa never derives the principal
  from a token, a keyring login, a GitHub account or any network call, and
  recording it changes nothing about which credentials a session holds.

### Durability

- **R10. Entries outlive the instance.** An entry survives each of: `niwa
  reap` along every path that removes a session (the mapped instance, the
  stored mapping whose instance is gone, and the unmapped backstop); `niwa
  destroy` of the instance, with or without `--keep-session`; `niwa reset`;
  an on-reap command that fails, and one that has failed often enough for
  its mapping to be dropped; and the deletion of the session mapping. The
  store sits outside every directory `niwa apply` and its configuration
  refresh rewrite, so neither touches it.
- **R11. How long entries last.** Entries last as long as the workspace root.
  Destroying the workspace removes them; nothing in this feature removes one
  earlier.

### Other launch paths

- **R12. Watch reviews are root entries.** Each review session `niwa watch`
  launches gets an entry with origin `watch`, a fresh dispatch id niwa mints
  for it, `parent_session` and `parent_dispatch` sources `none`, a principal
  by R7's rows two to four, and `session_id` when the review's capture finds
  the session.

### Reading it

- **R13. Selecting an entry.** `niwa lineage show` takes one selector:
  - a dispatch id as its argument, matching that entry;
  - `--session <id>`, matching entries whose `session_id` is that id;
  - `--instance <name>`, matching entries for that instance name;
  - none, or `-C <dir>`: the instance containing the working directory (or
    `<dir>`); the entry for its authority record's dispatch id when it has
    one, otherwise the entries for that instance name.

  Where several entries match, the latest `written_at` wins, ties going to
  the lower dispatch id. Two selectors together, or `-C` with any other
  selector, is a usage error (exit 2). An empty, relative or nonexistent
  `-C` exits 1. A selector that matches nothing prints `no lineage entry`
  and exits 1: that includes a workspace with no store, a working directory
  in the workspace but in no instance, and an authority record whose
  dispatch id has no entry. Run outside a workspace it exits 1 naming the
  missing workspace.
- **R14. Showing the chain.** `show` prints the selected entry and then each
  ancestor reached through `parent_dispatch` ids, nearest first. For every
  level it prints the dispatch id, the worker session id, the parent session
  with its source, the parent dispatch with its source, and the principal
  with its source and trust, and every level whose parent session id is set
  prints `directed by <session id>`. The layout is the design's; tests may
  rely on the values, `(not observed)`, `directed by`, and the stop text
  below. The walk ends as the table says, the selected entry counting as
  level 1:

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
  `{"schema": "niwa.lineage.chain/v1", "chain": [...], "stop": "<reason>"}`,
  where `chain` holds one object per level printed, and `stop` is a value
  from R14's table. A readable level is the entry's R1 fields; a later
  version is `{"dispatch_id", "schema", "known": false}` and nothing else of
  it is read; an unreadable entry is `{"dispatch_id", "error"}`; a parent
  with no entry is `{"dispatch_id", "error": "not on record"}`. `niwa lineage list
  --json` prints `{"schema": "niwa.lineage.list/v1", "entries": [...]}` in
  the same entry shapes. Every source and trust value is printed as stored.
  The formats grow additively within a version.
- **R16. Listing entries.** `niwa lineage list` prints every entry in the
  workspace, latest `written_at` first, ties broken by dispatch id, one line
  each: dispatch id, worker session id, parent session id with its source,
  and principal. An unreadable entry prints as its dispatch id and error, and
  the rest still list; `list` exits 0 unless the store itself can't be read.
  A workspace with no store lists nothing and exits 0.
- **R17. Only `observed` is observed.** The rule covers three fields:
  `parent_session.source`, `parent_dispatch.source` and `principal.trust`.
  In them, a value other than `observed`, including one this niwa doesn't
  know, is not observed. The human output prints such a value followed by
  `(not observed)`, except `none` on a link, which means there is no parent
  and prints alone. The JSON prints every value as stored, and the guide
  (R21) tells tool authors to apply the same rule. `principal.source` is a
  provenance label and is printed without annotation.
- **R18. The live read agrees.** The authority record gains the parent
  session source and the principal (name, source, trust), with the same
  values as the lineage entry. Its existing `parent_dispatch_source` keeps
  its values, and `cwd`, `anchor` and `none` there correspond to the
  entry's `observed`, `handed` and `none`. So `niwa authority show` and
  `niwa lineage show` agree while the instance exists. An authority record written before
  this feature lacks them, and `niwa authority show` prints them as
  `unrecorded`; an older niwa reading a newer record ignores them, as the
  record format already allows.

### Constraints

- **R19. No other tool required.** Writing and reading lineage works with no
  workflow engine installed and reads nothing another tool writes.
- **R20. Local and offline.** Writing an entry and every lineage read touch
  only local files and make no network call. The principal is not added to
  the worker's telemetry attributes.
- **R21. The guide states the trust boundary.** `docs/guides/dispatch-lineage.md`
  gains a section on the lineage record stating: a person's own interactive
  session has no mapping, so a first-level parent session is `handed`;
  `--parent-from` makes both links `handed`; `unverified` is expected after
  a resume and for a Codex worker's dispatches, and the parent dispatch link
  is the one to follow for the tree; only `observed` counts as observed,
  unknown values included; any process running as the same user can edit
  the store, so every entry is advisory; and entries last until the
  workspace is destroyed.
- **R22. Store safety.** The store's directory is mode 0700 and each entry
  0600. An entry is written to a temporary file and renamed into place, so a
  reader never sees a partial entry. Every field, `agent` and `written_at`
  included, is re-validated against the shape R1, R4, R7 and R8 give it when
  read, and an entry failing any check is unreadable.
- **R23. Bounded entries.** Every field is length-bounded (ids by their
  formats, the instance name by niwa's instance naming, the principal by
  R8), so an entry is at most 4 KiB.
- **R24. Nothing else changes.** Dispatch argv, the permission mode,
  settings, the telemetry attributes and every existing refusal are
  unchanged. A workspace that never dispatches gets no store.

## Acceptance Criteria

Tests that need a parent session set `CLAUDE_CODE_SESSION_ID` and write the
session mappings the situation calls for; tests that need a credential put a
stub `gh` first on `PATH` and set `GH_TOKEN`.

The tree:
- [ ] Dispatch A from the workspace root with the session variable set to S0;
      from inside A's instance, with the variable set to A's mapped session
      id, dispatch B; reap both. `niwa lineage show --session <B's session>`
      prints B's parent session as A's session id `observed`, B's parent
      dispatch as A's dispatch id `observed`, then A's level with parent
      session S0 `handed` and parent dispatch `none`, stop `root`; both
      levels name the same principal. B's level prints `directed by <A's
      session id>` and A's prints `directed by S0` (R1, R4, R5, R7, R10,
      R14).
- [ ] Extend that tree with C dispatched from inside B's instance: `show` on
      C prints three levels, nearest first (R14).
- [ ] In the same tree, A's principal source is `declared` with `principal`
      set in `[global]`, `os-account` without it, and B's is `inherited`
      with trust `observed` (R7, R8).
- [ ] `show --json` on B prints schema `niwa.lineage.chain/v1`, two chain
      objects carrying every R1 field, and stop `root` (R1, R15).

Parent links:
- [ ] From inside A's instance with the variable set to a well-formed id
      other than A's mapped one, the dispatch launches and B's entry has
      parent session source `unverified`, `id` the claimed value and
      `mapped_session_id` A's (R4, R6).
- [ ] Each remaining R4 row gives its source: variable unset (`none`),
      variable `not-a-valid-id!` (`none`), dispatch from the workspace root
      (`handed`), from an instance with no mapping (`handed`), and with
      `--parent-from` (`handed`). Every one of these dispatches launches
      (R4, R6).
- [ ] A dispatch with `--parent-from` naming its own instance records parent
      dispatch source `handed` and principal trust `handed`; one from an
      instance with no authority record records parent dispatch `none` (R5,
      R7).
- [ ] An unreadable parent authority record still refuses the dispatch (R5).
- [ ] A dispatch from inside an instance whose lineage entry was deleted
      records that parent dispatch id and a `declared` or `os-account`
      principal (R7).
- [ ] A grandchild dispatched from inside a child whose principal trust is
      `handed` records principal trust `handed`, even through an `observed`
      parent dispatch link (R7).
- [ ] With no `principal` declared and an OS account name that fails R8's
      rule (set through a test seam), a root dispatch records principal
      source `none` with an empty name; a child of it records `inherited`
      with an empty name (R7).

The principal:
- [ ] `principal = "dana@example.com"` is accepted; `principal = ""`,
      `principal = "Dana G"` and a 129-character value each refuse dispatch
      with an error naming `principal` and the config file, and the invalid
      value refuses a `niwa watch` review launch the same way (R8).
- [ ] `niwa dispatch --help` lists no flag that sets the principal (R8).
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
      dispatch refuses the same way (R3).
- [ ] With the step that adds `session_id` made to fail through a test seam,
      dispatch prints one warning naming the entry, exits 0, and the entry
      has no `session_id` (R3).
- [ ] An entry's bytes are identical before and after a reap, a second
      dispatch and a `niwa apply` (R2).

Durability:
- [ ] An entry survives each R10 path, each tested on its own: mapped reap,
      stored-mapping reap, backstop reap, `niwa destroy`, `niwa destroy
      --keep-session`, `niwa reset`, a failing on-reap command, an on-reap
      command failing until its mapping is dropped, and `niwa apply`
      refreshing the configuration directory (R10).
- [ ] Destroying the workspace removes the store (R11).

Watch:
- [ ] A `niwa watch` review gets an entry with origin `watch`, a dispatch id
      distinct from every other entry's, parent sources `none`, a principal
      by R7, and `session_id` when the capture finds the session (R12).

Reading:
- [ ] `show` selects correctly by dispatch id, `--session`, `--instance`, the
      working directory and `-C`; with two entries sharing a session id or
      instance name it selects the later `written_at` (R13).
- [ ] Two selectors exit 2; `-C` that is empty, relative or nonexistent
      exits 1; outside any workspace `show` exits 1 naming the missing
      workspace (R13).
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
      stop `depth` (R14).
- [ ] An ancestor with its schema changed to `niwa.lineage.entry/v2` ends
      the walk with `<id> later version niwa.lineage.entry/v2`, JSON stop
      `later-version`, and a chain object with `known: false` and nothing
      else of the entry; selecting that entry directly exits 0 (R14, R15).
- [ ] Entries edited to hold a malformed dispatch id, an `agent` of `Claude
      Code`, a `written_at` that isn't RFC 3339, or a 129-character
      principal are each unreadable; `show` on an unreadable selected entry
      exits 1 (R14, R22).
- [ ] `niwa lineage list` orders three entries by `written_at`, newest
      first, and two with equal `written_at` by dispatch id; each line holds
      the dispatch id, session id, parent session id with its source, and
      principal; an unreadable entry lists as its dispatch id and error
      among the others, exit 0; a workspace that never dispatched lists
      nothing, exit 0 (R16).
- [ ] An entry whose parent session source is edited to `vouched` prints
      `vouched (not observed)` in `show` and `"vouched"` in `show --json`;
      one whose principal trust is edited to `vouched` prints the same way;
      a `none` link prints `none` alone; a `declared` principal source
      prints with no annotation (R17).
- [ ] While an instance exists, `niwa authority show --json` and `niwa
      lineage show --json` report the same parent session source and
      principal, and the authority record's `parent_dispatch_source` `cwd`
      or `anchor` matches the entry's `observed` or `handed`; an authority
      record without the new fields prints them as `unrecorded` (R18).

Constraints:
- [ ] The lineage functional tests pass with no workflow engine on PATH
      (R19).
- [ ] With `HTTPS_PROXY` and `HTTP_PROXY` pointing at a closed local port,
      dispatch writes its entry and `show` and `list` succeed; the worker's
      `OTEL_RESOURCE_ATTRIBUTES` holds no principal attribute (R20).
- [ ] `docs/guides/dispatch-lineage.md` has a section stating each point R21
      lists (R21).
- [ ] The store directory is mode 0700 and entries 0600; with the rename
      made to fail through a test seam, no file exists at the entry's final
      path and the dispatch refuses (R3, R22).
- [ ] An entry with every field at its maximum length is at most 4 KiB
      (R23).
- [ ] The existing dispatch, authority and telemetry tests pass unchanged,
      and a workspace that has never dispatched has no store (R24).

## Out of Scope

- **Capping a child at its parent's authority.** That check will read this
  record's links; it is separate work. This feature records and refuses
  nothing new.
- **Hardening the trust boundary.** A link niwa can't check is recorded as
  handed, not made checkable through signatures or a broker process.
- **Reading any workflow engine's own session records.** A workflow engine
  may record which run started which; niwa neither reads that nor needs it.
- **Carrying the principal onto recorded decisions, classifying where a
  session escalated, and telling a session at start who directs it.** Each
  reads this record and each is separate work.
- **Lineage across machines or workspaces, or a copy in a cloud store.** The
  record is local to the workspace that dispatched; `FindInstance` and
  `--parent-from` already confine parents to one workspace.
- **Changing which credentials a session holds.**
- **Guessing a parent from the process tree, or reviving a registry of live
  sessions.**
- **Pruning entries.** Entries last as long as the workspace root (R11); a
  command to remove old entries is later work if the store's size ever
  matters.
- **Back-filling entries for dispatches made before this feature.**

## Known Limitations

- **Every entry is advisory.** Any process running as the same user can edit
  the store, as it can the authority record and the session mappings.
- **A first-level parent session is always handed.** A person's own
  interactive session has no instance and no mapping, so niwa has nothing to
  check its id against.
- **`unverified` is often benign.** A resumed session or a Codex worker's
  dispatch reads `unverified` because its session variable differs from the
  mapping; the parent dispatch link, found from the working directory, is
  the one to follow for the tree.
- **The principal is a label, not an authenticated identity.** It is what the
  machine config declares or the OS account's name.
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
- **The principal comes from the machine, not the session.** A declared
  config value, falling back to the OS account, recorded with its source, and
  copied down the tree. Alternatives considered: the GitHub login (a
  credential's identity, and a network call), the version-control author
  setting (often shared or absent), and a dispatch flag (lets any level
  rename the person).
- **A separate store, not a longer-lived mapping or authority record.** The
  mapping is keyed by session id, which a dispatch doesn't always learn, and
  is deleted at reap by design; the authority record lives inside the
  instance. A new store keyed by dispatch id leaves both contracts alone.
- **Watch reviews are included as root entries.** They are the main way a
  session starts with no parent session, and leaving them out would leave
  the root case unrecorded for exactly the sessions that most need it.
- **The principal carries its own trust.** An inherited principal is only as
  good as the link it came through, so the entry records a trust value
  beside the source rather than folding both into one word. A principal niwa
  read from its own config or the OS account is `observed`: it is what this
  machine says, whoever that is.
- **The principal stays off telemetry.** A person's name shouldn't leave the
  machine on an attribute; the local record answers the question.
