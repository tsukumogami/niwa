---
schema: prd/v1
status: Draft
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

Draft

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

### The record

- **R1. One lineage entry per launched dispatch.** Every `niwa dispatch` that
  launches a worker writes one lineage entry, keyed by its dispatch id, into a
  store at the workspace root. Each entry holds: the format version; the
  dispatch id; the instance name; the agent; when it was written; the origin
  (`dispatch` or `watch`, R12); the worker's session id once niwa has
  captured it; the parent session link (R4); the parent dispatch link (R5);
  and the principal (R7).
- **R2. Written before the worker starts, completed once, never rewritten
  otherwise.** The entry is written before the worker process starts, beside
  the authority record. A dispatch that rolls back before or during launch
  leaves no entry. Once niwa captures the worker's session id it adds it to
  the entry; a dispatch whose capture fails but whose instance is kept (a
  foreground turn with no session record) keeps its entry with the session
  id absent. Nothing else changes an entry after it is written.
- **R3. A write failure stops the dispatch.** If the entry can't be written,
  the dispatch refuses and rolls back exactly as it does when the authority
  record can't be written, so no worker runs without an entry. A failure to
  add the captured session id is a warning, not a refusal, and leaves the
  entry without it.

### Parent links and how niwa came by them

- **R4. The parent session link and its source.** The parent session id is
  read as today, from the variable the agent's launch spec names. Beside it
  the entry records one source:
  - `observed`: the dispatch runs inside an instance (found from the working
    directory) whose newest session mapping names the same session id.
  - `handed`: niwa has no mapping for the dispatching instance to check the
    id against: the dispatch runs at the workspace root, outside any
    instance, from an instance found through `--parent-from`, or from an
    instance with no mapping.
  - `unverified`: the dispatching instance's newest mapping names a
    different session. The entry keeps the claimed id as the parent session,
    marked `unverified`, and records the session id the mapping names beside
    it.
  - `none`: no parent session id was given, or it failed its shape check.
- **R5. The parent dispatch link and its source.** The parent dispatch id is
  found as today (`--parent-from`, else the working directory's instance).
  Its source is `observed` when the working directory's instance gave it,
  `handed` when `--parent-from` did, and `none` when no parent record was
  found. A parent record that exists but can't be read still refuses the
  dispatch, as it does today.
- **R6. A false claim is recorded, never refused.** No dispatch is refused
  because its parent session is `handed` or `unverified`. A resumed session
  and a Codex worker both legitimately present an id other than the one
  niwa mapped, and this feature does not change what dispatch enforces.

### The person a session acts for

- **R7. The principal and its source.** Every entry names one principal, the
  person the dispatched session acts on behalf of, with the source niwa took
  it from:
  - `inherited`: the dispatch has a parent dispatch link (R5) and the
    parent's lineage entry exists; the principal is copied from it, so every
    level of a tree names the same person.
  - `declared`: the dispatch has no parent entry to inherit from, and the
    machine config declares the person (R8).
  - `os-account`: the same, with nothing declared; the principal is the name
    of the operating-system account that ran the dispatch.
  An inherited principal's trust is the trust of the link it came through: a
  principal inherited through a `handed` parent dispatch reads as handed.
- **R8. Declaring the person.** The personal niwa config accepts one key
  naming the person dispatches on that machine act for. Its value is checked
  against a fixed character set and length when the config loads, and an
  invalid value refuses dispatch with an error naming the key. No dispatch
  flag sets or overrides the principal.
- **R9. The principal is a person, never a credential.** niwa never derives
  the principal from a token, a keyring login, a GitHub account or any
  network call, and recording it changes nothing about which credentials a
  session holds.

### Durability

- **R10. Entries outlive the instance.** An entry survives every way an
  instance goes away: `niwa reap` (the mapped path, the stored-mapping path
  and the unmapped backstop), `niwa destroy` of the instance, `niwa reset`,
  a failed or exhausted on-reap command, and the deletion of the session
  mapping. It also survives `niwa apply` and every refresh of the workspace
  configuration directory.
- **R11. How long entries last.** Entries last as long as the workspace root.
  Destroying the workspace removes them with it; nothing in this feature
  removes them earlier.

### Other launch paths

- **R12. Watch reviews are root entries.** Each review session `niwa watch`
  launches gets an entry with origin `watch`, a dispatch id niwa mints for
  it, parent session and parent dispatch sources `none`, and a principal per
  R7's root rules, plus the session id when the review's capture finds it.

### Reading it

- **R13. A person reads one lineage.** `niwa lineage show` selects an entry by
  dispatch id, by `--session <id>` (the worker's session id), by `--instance
  <name>`, or by the instance containing the working directory (`-C <dir>`
  resolves from `<dir>` instead), with the same argument rules as `niwa
  authority show`. It prints the selected entry, then walks up through each
  parent dispatch link to the first entry with none, and for every level
  prints the session, the parent session with its source, the parent
  dispatch with its source, and the principal with its source. Each level
  below the first names the session that directs it, which is its parent
  session. A parent dispatch id with no entry on record prints as not on
  record, and the walk stops there.
- **R14. A person lists entries.** `niwa lineage list` prints every entry in
  the workspace, newest first, one line each: dispatch id, worker session id,
  parent session with its source, and principal.
- **R15. Tools read a versioned document.** `niwa lineage show --json` and
  `niwa lineage list --json` print documents carrying a schema name and
  version, with every link's source as a field. The format grows additively
  within a version; a reader of an older niwa reads an entry from a later
  version as unknown rather than guessing at it. An entry that can't be read
  or fails validation is reported as an error for that entry and never read
  as a link.
- **R16. Only `observed` reads as observed.** Every source value other than
  `observed`, including any value a niwa doesn't know, reads as not observed,
  in niwa's own output and in the rule the guide gives tool authors (R20).
  `niwa lineage show` prints a handed link as handed.
- **R17. The live read agrees.** The authority record for a dispatch carries
  the same parent session source and principal as its lineage entry, so
  `niwa authority show` and `niwa lineage show` agree while the instance
  exists.

### Constraints

- **R18. No other tool required.** Writing and reading lineage works with no
  workflow engine installed and reads nothing any other tool writes.
- **R19. Local and offline.** Writing an entry and every lineage read touch
  only local files and make no network call. The principal is not added to
  the worker's telemetry attributes.
- **R20. The guide states the trust boundary.** A guide explains what niwa
  can observe and what it can't: that a person's own interactive session is
  never observable, so a first-level parent session is `handed`; that
  `--parent-from` makes a link `handed`; that same-user processes can edit
  the store, so every entry is advisory; and how long entries last.
- **R21. Store safety.** The store's directory and files are readable and
  writable only by the user, each entry is written atomically, and every
  value is re-validated against the shape niwa writes when it is read.

### Non-functional

- **R22. Bounded entries.** One entry is at most 4 KiB, and writing it adds
  no more than 50 ms to a dispatch on a local disk.
- **R23. Nothing else changes.** Dispatch argv, permission mode, settings,
  telemetry attributes other than those already written, and every existing
  refusal are unchanged. A workspace that never dispatches gets no store.

## Acceptance Criteria

- [ ] From a person's root session, dispatch A; from inside A's instance,
      have A dispatch B; let both finish and reap both instances. `niwa
      lineage show --session <B's session id>` prints B's parent session as
      A's session id with source `observed`, B's parent dispatch as A's
      dispatch id with source `observed`, A's parent session as the person's
      session id with source `handed`, A's parent dispatch source `none`, and
      the same principal on both levels (R1, R4, R5, R7, R10, R13).
- [ ] In the same setup, B's principal reads source `inherited` and A's reads
      `declared` when the machine config declares a person, or `os-account`
      when it doesn't (R7, R8).
- [ ] From inside A's instance, run `niwa dispatch` with the session variable
      set to a well-formed id that is not A's session. The dispatch launches,
      and its entry records the claimed id as parent session with source
      `unverified` and A's session id beside it (R4, R6).
- [ ] A dispatch from inside an instance with `--parent-from` naming that
      instance records the parent dispatch source `handed` (R5).
- [ ] With the session variable unset, a dispatch records parent session
      source `none` (R4).
- [ ] `niwa lineage show --json` for each case above prints a document with
      the schema name and each link's source; a link whose stored source is
      a value this niwa doesn't know prints as not observed (R15, R16).
- [ ] An entry whose parent dispatch has no entry prints that parent as not
      on record and stops the walk, exit 0 (R13).
- [ ] An entry edited to hold a malformed id is reported as an error for that
      entry, and `niwa lineage list` still lists the others (R15, R21).
- [ ] Entries survive `niwa reap`, the unmapped backstop reap, `niwa destroy`
      of the instance, `niwa reset`, an on-reap command that fails, and a
      `niwa apply` that refreshes the configuration directory (R10).
- [ ] A dispatch refused at authority resolution, or rolled back during
      provisioning or launch, leaves no entry (R2).
- [ ] Making the store unwritable refuses the dispatch and leaves no instance
      (R3).
- [ ] A foreground dispatch whose turn ends with no session record keeps its
      instance and an entry with no session id (R2).
- [ ] A `niwa watch` review session gets an entry with origin `watch`, parent
      sources `none`, and a principal per R7's root rules (R12).
- [ ] An invalid declared principal refuses dispatch with an error naming the
      key; no dispatch flag sets the principal (R8).
- [ ] The principal never matches a value read from a token, keyring or
      network call: with a declared person, `GH_TOKEN` set and the keyring
      logged in as a different account, the entry names the declared person
      (R9).
- [ ] While an instance exists, `niwa authority show --json` and `niwa
      lineage show --json` report the same parent session source and
      principal (R17).
- [ ] The functional tests for lineage pass with no workflow engine on PATH
      (R18).
- [ ] Lineage writes and reads make no network call, and the worker's
      telemetry attributes don't include the principal (R19).
- [ ] The store's directory is mode 0700 and each entry 0600 (R21).
- [ ] A written entry is under 4 KiB, and a dispatch with lineage takes no
      more than 50 ms longer than one without on a local disk (R22).
- [ ] The guide covers each point R20 lists.

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
- **The principal stays off telemetry.** A person's name shouldn't leave the
  machine on an attribute; the local record answers the question.
