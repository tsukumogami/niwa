---
schema: prd/v1
status: In Progress
problem: |
  A session `niwa dispatch` launches runs as the operator and settles every
  decision point the same way in every workspace, because nothing tells it
  how much it may decide. An operator can't hand one session the authority
  to merge pull requests, close issues or cut releases and withhold it from
  another, and nothing records afterwards what a dispatch was allowed or
  where that came from.
goals: |
  An operator sets an authority level and a ceiling of grantable named
  authorities once per workspace, narrows the level or grants named
  authorities and roles per dispatch without touching shared config, and is
  refused before anything launches when a dispatch asks for more than the
  ceiling. Each dispatch's resolved level and grant, with where every part
  came from, can be read back by the operator and by the session itself.
  A workspace that configures nothing behaves as it does today, and
  enforcement stays exactly where it is.
absorbed:
  - docs/briefs/BRIEF-authority-grants.md
---

# PRD: Authority levels and named grants for dispatched sessions

## Status

In Progress

Absorbed [BRIEF-authority-grants](docs/briefs/BRIEF-authority-grants.md); carried in Absorbed Brief.

## Absorbed Brief

A session `niwa dispatch` launches can do anything its operator can, and
nothing tells it how much of that it's meant to use. The brief framed the
gap from the operator's side. A solo maintainer and a team that wants a
person at every merge get the same worker. Merging, closing issues and
cutting releases can't be handed to one session and withheld from
another. Handing a coordinating session's job to its successor costs a
config edit and a re-apply, because the only lever is a hook keyed on
instance names that carry a random token. And nobody can say afterwards
what a session was allowed or why.

The outcome it set: an operator decides once, for everyone sharing the
workspace, how much dispatched sessions may decide and which actions may
ever be handed to one. They give one session less room or exactly the
actions it needs at dispatch without editing shared config, and they're
refused before anything launches when they ask for more than the
workspace allows. For any dispatch the operator, and the session itself,
can answer "what was this session allowed, and why?". The same decision
point settles itself at the autonomous end and goes to a person at the
strict end, and a workspace that configures nothing behaves as it does
today. The work declares and tracks authority in the right format;
stronger enforcement is separate, later work, and an earlier unmerged
draft (pull request #401) covering the level half was input.

Its five scenarios (a solo maintainer who configures nothing, a workspace
owner declaring policy once, a coordinator handing off to a successor
with a role, an operator narrowing one risky dispatch, and a dispatch
refused for asking too much) are the user stories below. Its boundary is
carried by the requirements and Out of Scope: gates honouring the grant,
capping a child at its parent, stronger enforcement, the permission mode,
start-of-session prompts and escalation routing, per-point declarations,
and changing a grant mid-session all stay out.

## Problem Statement

`niwa dispatch` launches sessions that run as the operator and can do
anything the operator can. When one of them reaches a decision point, such
as accepting its own requirements document or merging its own green pull
request, nothing tells it whether this workspace wants it to settle the
point or hand it to someone. A solo maintainer who lets agents finish their
own work and a team that wants a person at every merge get the same worker,
and the difference lives only in how each brief happens to be worded.

The actions that matter most can't be handed out one at a time either. An
operator who wants one session to merge pull requests, close issues and
cut releases, and another to do none of that, has no way to tell niwa. The
closest lever today is a workspace hook that denies merges unless the
session's instance name appears in a config variable, and that asks a
person before every issue close or release with no way to hand either
over. Instance names carry a random token, so a successor's name can't be
written down before it exists: when a coordinating session hands its job
to a successor, someone edits the workspace config and re-applies it
before the successor can merge, and closes and releases still go to a
person.

An operator reviewing finished work can't tell what a session was allowed
when it was dispatched, or whether a permission came from that dispatch or
from a broader setting, so a session that merged its own work can't be
checked against what it was meant to hold.

The one authority-shaped setting niwa has, the permission mode
(`--permission-mode`, `[global] dispatch_permission_mode`, the workspace
`bypass` posture), decides how a worker's tool calls get approved. It
doesn't decide who settles a decision point or which named actions a
session holds, and on it the machine outranks the workspace in both
directions, which suits a machine owner's trust in their own laptop and is
the wrong shape for a team's rule about who decides.

## Goals

- An operator decides once, in the workspace config everyone shares, how
  much dispatched sessions may decide, which named actions may ever be
  granted, and which roles bundle them.
- At dispatch the operator gives one session less room, or exactly the
  named actions it needs, without editing shared config.
- Nothing widens what the workspace allows. A request for more is refused
  before anything launches, naming what was asked for and the limit.
- For any dispatch, the operator can answer "what was this session
  allowed, and why?", and the session can answer it about itself.
- The same decision point settles itself at the autonomous end and goes to
  a person at the strict end.
- Later work that makes the workspace's gates honour a grant, or refuses a
  child wider than its parent, reads one record through one lookup instead
  of inventing its own.
- A workspace that configures nothing behaves as it does today, and no
  session can do anything it couldn't do before.

## User Stories

- As a solo maintainer who has configured nothing, I want my dispatched
  workers to keep settling their own decision points, so that unattended
  work still finishes without me.
- As the owner of a team workspace, I want to set the strict level, a
  ceiling that allows merging and closing issues but not releases, and a
  `coordinator` role that bundles what a coordinating session needs, in
  the config every teammate clones, so that the whole team gets the same
  policy without each brief saying so.
- As a workspace owner, I want to check what niwa made of my authority
  settings before anyone dispatches against them, so that a typo in a role
  shows up as an error now rather than as a refusal later.
- As a coordinating session handing off to a successor, I want to dispatch
  it with the `coordinator` role and have it read back merge, close and
  release, each attributed to the role, so that the handoff needs no edit
  to shared config.
- As an operator in an autonomous workspace, I want to dispatch one session
  into an unfamiliar repository at the strict level, so that its decision
  points go to a person while my other sessions keep settling theirs.
- As an operator, I want a dispatch that asks for an authority past the
  ceiling, directly or through a role, to be refused before anything
  launches, so that an old script or a typo can't quietly hand out more
  than the policy allows.
- As a machine owner, I want to cap every dispatch on my laptop at the
  strict level and at no release authority, whatever a cloned workspace
  says, so that a workspace config can't make my machine's workers settle
  things on their own or grant releases.
- As an operator reviewing finished work, I want to read the level and
  each named authority a dispatch held, with where each came from, by its
  instance or session from the workspace root, so that I can tell whether
  a session that merged its own work was granted it.
- As a dispatched session, I want to read back my own grant with one
  command that works whether I'm a Claude or a Codex session, so that I
  act on what I hold instead of guessing from my brief.
- As the author of later gate or re-delegation work, I want a documented
  record and lookup whose every failure reads as "not held", so that I can
  build on it without parsing niwa's session files or making anything
  weaker than today.

## Requirements

### Functional

**Vocabulary**

- **R1. Three ordered levels.** The authority level takes one of
  `autonomous`, `supervised`, `strict`, ordered from widest to narrowest.
  "Narrower" always means further toward `strict`.
- **R2. What a level means at a decision point.** A decision point is a
  named place where a session would settle something on its own, such as
  accepting its own requirements or merging its own pull request. Until a
  per-point declaration exists (R27), a point resolves from the level
  alone:

  | Level | Decider | Author exclusion |
  |-------|---------|------------------|
  | `autonomous` | `session` | `off` |
  | `supervised` | `dispatcher` | `on` |
  | `strict` | `person` | `on` |

  `dispatcher` is the seat that launched this session: the coordinating
  role that ran the `niwa dispatch`, or the person at that shell when it
  ran outside any session. It names the seat, not one instance, so a
  successor that takes over the seat is the dispatcher from then on. The
  record (R17) names only the original parent and keeps no "who
  supervises now" field, so later work can add a supervision link
  without a format change. This feature only names the dispatcher;
  finding and reaching it is separate work (Out of Scope). Author
  exclusion `on` means whoever settles the point may
  not be the session that authored the work the point is about. Deciders
  are ordered `session` < `dispatcher` < `person` and author exclusion
  `off` < `on`, widest first. Decision point names are an open vocabulary
  that callers choose; this feature maps no point to a named authority.
- **R3. Named authorities are a closed set.** niwa defines the named
  authorities a grant may hold, each by the actions it covers. This
  feature defines exactly three:

  | Authority | Covers |
  |-----------|--------|
  | `merge-pr` | merging a pull request, by any route (CLI, REST, GraphQL, auto-merge) |
  | `close-issue` | closing an issue; closing a pull request without merging is not covered |
  | `cut-release` | publishing a release or a release tag |

  The routes in the first row are what the authority means to a later
  gate; this feature tests none of them, since nothing here gates an
  action. Any other name in a config value or a flag is refused under R11
  or R14. Adding an authority is a niwa change, and it doesn't change the
  record's version (R17a): a record written by a newer niwa may name an
  authority this niwa doesn't know. niwa's own reader reports such an
  entry as unknown and not held, and the rest of the record stays valid;
  a tool reading the JSON never treats an unknown name as one it knows.
  Wherever niwa lists authorities (the record, the read-back, the stderr
  line, telemetry) it lists the ones in this table in this table's
  order, and any others after them in name order.
- **R4. The level and the grant are separate and both apply.** The grant
  names the actions a session may take; the level decides who settles a
  decision point. No level adds or removes a named authority, and no grant
  changes a level. An action a named authority covers is the session's to
  take only when it holds that authority and the decision point for the
  action resolves to `session`; neither answer alone permits it. A session
  at `strict` holding `merge-pr` still resolves its merge decision to
  `person`, and a session at `autonomous` holding no `merge-pr` resolves
  it to `session` and still doesn't hold the action. This feature records
  both halves and states this rule as the contract later gate work
  follows; which decision point stands for which action, and enforcing
  the combination, are that work's (Out of Scope).

**Declaring the policy**

- **R5. Workspace level.** The workspace config declares its level as
  `level` under a new `[authority]` table in `.niwa/workspace.toml`. With
  no key the built-in level `autonomous` applies, with source `built-in`.
- **R6. Workspace ceiling.** The workspace config declares the named
  authorities a dispatch may be granted as `ceiling`, a list under
  `[authority]`. With no key the ceiling is empty: no named authority may
  be granted, and a dispatch granted one is refused under R14.
- **R7. Roles.** The workspace config declares roles as
  `[authority.roles.<name>]` tables, each with an `authorities` list. A
  role name matches `^[a-z][a-z0-9-]{0,63}$`. A role may name any
  authority from R3, including one outside the ceiling, since the machine
  can narrow the ceiling further (R9) and the check happens at grant time
  (R14). A role with an empty `authorities` list is valid and grants
  nothing.
- **R8. The base config declares; an overlay can only narrow.** Roles, the
  workspace level and the workspace ceiling are declared in the
  workspace's base config. A workspace overlay may set `level` and
  `ceiling` under its own `[authority]` table, and they only narrow: the
  level ceiling is the narrower of the base and overlay levels, and the
  ceiling is the authorities in both lists, with source `overlay` when the
  overlay is what narrowed it. An overlay can't declare or change a role;
  an `[authority.roles]` table in an overlay is ignored, and every command
  that loads the overlay warns that it has no effect.
- **R8a. Making authorities grantable.** A workspace makes merging,
  closing issues and cutting releases grantable, and gives coordinating
  sessions a role for all three, by adding this to its base config:

  ```toml
  [authority]
  level = "autonomous"
  ceiling = ["merge-pr", "close-issue", "cut-release"]

  [authority.roles.coordinator]
  authorities = ["merge-pr", "close-issue", "cut-release"]
  ```

  after which `niwa dispatch --role coordinator "<task>"` launches a
  session whose record holds all three. The guide (R34) carries this
  example.
- **R9. Machine limits.** The personal niwa config (the file that holds
  `[global] dispatch_permission_mode`) may declare `authority_level` and
  `authority_ceiling` under `[global]`. `authority_level` caps the level;
  it can never make a resolved level wider. `authority_ceiling` is a list,
  and the effective ceiling is the authorities in both it and the
  workspace ceiling; it can never add an authority. An absent key limits
  nothing; `authority_ceiling = []` limits the effective ceiling to empty.
  `niwa config set authority-level <level>`, `niwa config set
  authority-ceiling <a,b,...>` (or `none` for an empty list) and the
  matching `niwa config unset` commands write and clear them, trimming
  whitespace around each entry and refusing an invalid value with a
  non-zero exit and the file unchanged. A duplicate entry in any ceiling
  or role list is kept once.
- **R10. Checking the policy.** `niwa authority policy`, run inside a
  workspace, prints the workspace level and ceiling, the machine's
  `authority_level` and `authority_ceiling` (or `not set`), the effective
  level ceiling and grant ceiling each with its source, each declared role
  with its authorities, marking every authority outside the effective
  ceiling, and what a dispatch with no authority flags would resolve to.
  `--json` prints the same as one JSON document with `schema:
  "niwa.authority.policy/v1"`, which grows additively under the same
  rule as the record (R17a). It also warns when an overlay or machine
  ceiling names an authority the base ceiling lacks: the intersection
  drops it, and `niwa apply` prints the same warning. Outside a workspace
  it exits 1 naming the missing workspace.
- **R11. Values are checked exactly.** Every level value accepts only the
  three names of R1 and every authority value only the names of R3, both
  lowercase, after trimming surrounding whitespace; an empty or
  whitespace-only level means not set. An unknown key inside a base or
  overlay `[authority]` table, or an unknown `authority_`-prefixed key
  under `[global]`, counts as an invalid value rather than the usual
  unknown-key warning, since a misspelt narrowing key would otherwise
  leave policy silently wider. An invalid workspace value (level,
  ceiling entry, role name, role authority, unknown key, or a value of the
  wrong type) refuses `niwa dispatch` and `niwa authority policy` with exit
  1 and a message naming the key, the value and the accepted values. `niwa
  authority show` and `decide` refuse the same way only when they need the
  config, which is for the no-record answer (R23, R26); reading an existing
  record never depends on the config, so a typo added after a dispatch
  doesn't hide that dispatch's record. Other commands that load the
  workspace config, `niwa apply` among them, print the same message as a
  warning and carry on, so a fix can still be pulled from the config's
  source. An invalid machine value is treated the same way. A workspace config that exists but
  can't be read or parsed refuses a dispatch with exit 1 before any
  instance is created, so neither the level nor the ceiling ever falls
  back to a default because the file was unreadable.

**Narrowing and granting at dispatch**

- **R12. Per-dispatch level.** `niwa dispatch --level <level>` narrows the
  level for that one dispatch. A value equal to or narrower than the level
  ceiling (R13) is accepted.
- **R13. Level resolution and refusal.** The level ceiling is the
  narrowest of the workspace level, the overlay level and the machine
  `authority_level`, each taking part only when set; when none is set it
  is the built-in `autonomous`. Its source is whichever of `machine`,
  `overlay`, `workspace` or `built-in` supplied it, a tie going to the
  first of that list. A `--level` value wider than the ceiling refuses
  the dispatch with exit 1, naming the requested level, the ceiling level
  and the ceiling's source. Otherwise the resolved level is the narrower
  of the ceiling and the `--level` value, with source `flag` when the
  flag supplied it (a tie goes to the flag) and the ceiling's source
  otherwise, as resolved at dispatch; R16a's re-check can later record a
  different `level_ceiling_source` for the same level, so the two fields
  can differ in a valid record. The effective grant ceiling is the base config's ceiling
  list, intersected with the overlay's list when set and the machine's
  list when set. Its source is `machine` when the machine list removed any
  authority from the base list, else `overlay` when the overlay list did,
  else `workspace`. These are the only source values a record carries for
  a level or a ceiling; the no-record read-back marks itself separately
  (R23).
- **R14. Granting.** `niwa dispatch --grant <authority>` and `--role
  <name>`, each repeatable, grant the named authority, or every authority
  the role names, to that one dispatch; the grant is the union of
  everything requested. Each takes a comma-separated list too, split and
  trimmed the same way `niwa config set authority-ceiling a,b` is, so
  `--grant merge-pr,close-issue` equals two `--grant` flags and an empty
  entry is refused. If any requested authority,
  directly or through a role, is outside the effective ceiling, the
  dispatch is refused with exit 1, and the message names each such
  authority, how it was requested (`--grant` or `role <name>`), the
  effective ceiling and its source (R13). An unknown authority, an
  undeclared role, or a `--level` value outside R1 refuses the dispatch
  with exit 1 naming it and the accepted values, checked with the same
  trimming and case rules as R11. Requests are never trimmed: a dispatch
  either gets everything it asked for or launches nothing.
- **R14a. Naming the parent explicitly.** `niwa dispatch --parent-from
  <abs dir>` finds the dispatcher's instance from `<abs dir>` instead of
  the working directory, with the same rules and checks as `niwa
  authority show -C` (R21): an empty, relative or nonexistent path
  refuses the dispatch with exit 1 before anything is created. It's how a
  dispatcher that runs `niwa dispatch` from outside its own instance (a
  coordinator that changes to the workspace root first, say) names its
  instance as the parent. Without the flag, the working directory is
  used. Like every lineage field it's advisory: it records who says they
  dispatched, and checks nothing about the caller.
- **R15. Empty grant.** A dispatch with no `--grant` and no `--role` holds
  no named authority. No setting grants an authority by default.
- **R16. Refusals leave nothing behind.** A dispatch refused under R11,
  R13 or R14 when authority is resolved creates no instance, writes no
  record or session mapping, removes no other instance and launches
  nothing, and it's refused before dispatch asks for a prompt on the
  terminal and before it prints R19's line.
- **R16a. A policy that changes during provisioning is checked again.**
  Provisioning can refresh the workspace config or bring in an overlay
  after authority is resolved. Before writing the record, dispatch
  resolves the same request again against the config and overlay the new
  instance was built from. If that resolution refuses the request, or
  gives a different level (narrower or wider) or grant than the first
  one, the dispatch exits
  1 naming the setting that changed, the new instance is removed, nothing
  is written and nothing launches. By then the prompt may have been taken,
  the reclamation sweep may have run, and R19's line has printed; this is
  the one refusal that comes that late. Otherwise the record carries this
  second resolution's two ceilings and their sources, so it never claims
  a wider ceiling than the instance was built from, while the level, its
  source and the grant stay the ones R19's line printed.

**Recording and reading back**

- **R17. The dispatch record.** Every dispatch that launches a worker
  writes one authority record into the dispatched instance before the
  worker process starts, holding: the record's format version, the
  dispatch id, the parent session id the dispatch already records, the
  resolved level and its source, the level ceiling and its source, the
  effective grant ceiling and its source, each role granted, and each
  granted authority with every source it came from (`dispatch` for
  `--grant`, `role:<name>` for a role). An authority requested both
  directly and through a role, or through two roles, appears once with all
  its sources, `dispatch` first and then roles in name order. A dispatch
  that ran outside any session records an empty parent session id. The
  record also holds `parent_dispatch_id`: the dispatch id of the
  dispatched instance named by `--parent-from` (R14a) or, without it,
  the one `niwa dispatch` was run from; and `parent_dispatch_source`:
  `anchor` when `--parent-from` found the parent's record, `cwd` when the
  working directory did, `none` when no record was found (an empty
  `parent_dispatch_id`). A missing parent record means no parent, never a
  refusal; a parent record that exists but is malformed refuses the
  dispatch. It's the link later never-wider work follows, since the
  parent session id is read from a variable a worker can inherit from its
  own dispatcher (a Codex worker does), so it can name the wrong session.
  The record is the only copy; every reader in R20 to R26 reads it. Its
  JSON form, the one `niwa authority show --json` prints, carries
  `schema: "niwa.authority.record/v1"`, `record: true`, and the top-level
  fields `dispatch_id`, `parent_session_id`, `parent_dispatch_id`,
  `parent_dispatch_source`, `level`, `level_source`, `level_ceiling`,
  `level_ceiling_source`, `grant_ceiling`, `grant_ceiling_source`,
  `roles` (in name order), `grant` (a list of objects with `authority`
  and `sources`, holding only authorities this niwa knows) and
  `unknown_grant` (the same shape, for entries this niwa doesn't know,
  never held).
- **R17a. The v1 format grows additively.** New fields, new authority
  names and new source kinds stay `niwa.authority.record/v1`; only a
  rename or a change of a field's meaning (a new level, which changes the
  ordering, among them) moves to a new version. A reader ignores fields
  it doesn't know. A grant entry whose authority this niwa doesn't know,
  or whose sources are all of kinds it doesn't know, is reported under
  `unknown_grant`, not held, and leaves the rest of the record valid, so
  a newer niwa adding an authority (teardown, say) neither blocks a
  dispatch from that seat nor revokes the grants an installed gate
  reads. A record that is actually malformed (unreadable JSON, a missing
  required field, a value of the wrong type, an unknown level, an id or
  name with characters outside its pattern) still fails closed (R23).
- **R18. The record is fixed for the session's life.** niwa never rewrites
  an authority record after it's written. A later `niwa apply`, a change
  to the workspace or machine config, a resume of the session, or a
  dispatch of a different session leaves it as it was. A wider grant means
  a new dispatch. The record lives with the dispatched instance and is
  removed with it (rollback, reap, destroy); `niwa reset` of a dispatched
  instance removes it too, after which the instance reports no record
  (R23).
- **R19. Dispatch says what it resolved.** Every dispatch that launches a
  worker prints exactly one stderr line of the form
  `niwa dispatch: authority level <level> from <source>; grant <list>`,
  where `<list>` is `none` or each authority followed by its sources in
  parentheses, written `dispatch` and `role <name>` (the record writes
  `dispatch` and `role:<name>`), for example
  `merge-pr (role coordinator), close-issue (dispatch, role coordinator)`.
  A refused dispatch prints its refusal instead. The line prints once
  authority is resolved and before provisioning, so a dispatch that later
  fails to provision or launch, and rolls back, has printed it too.
- **R20. The operator reads it across instances.** `niwa list --json`
  includes each dispatched instance's resolved level, level source and
  grant with sources, under one `authority` object read from the record,
  and omits the object for an instance with no record (one dispatched
  before this feature, or one niwa didn't dispatch). An instance whose
  record can't be read or is malformed gets an `authority` object holding
  only an `error` naming why, and the rest of the listing is unaffected.
- **R21. Anyone reads one dispatch.** `niwa authority show` prints a
  dispatch's resolved level and source, its grant with each authority's
  sources (or `none`), its granted roles, the ceilings recorded with it,
  its dispatch id, its parent session id, and its parent dispatch id with
  that link's source. It selects the dispatch, in this order: the
  session named by `--session <id>` (a session id; a coordinator that
  holds session names reads its workers through `niwa list --json`,
  whose rows carry each instance's session name and `authority` object,
  or passes `--instance`); the instance named by
  `--instance <name>`; otherwise the instance containing the working
  directory. It never reads the session id variable: a working directory
  in no instance, or in an instance with no record, gives the no-record
  answer (R23). A worker can inherit its dispatcher's variable (a Codex
  worker does), so reading it would let a session that left its instance,
  or one at the workspace root, report its dispatcher's wider record as
  its own. `--session` and `--instance` together are a usage error (exit
  2). A `--session` with no mapping gives the no-record answer; an
  `--instance` naming no instance exits 1. `-C <dir>` resolves from
  `<dir>` instead of the working directory, so a hook can anchor the
  lookup to its own script's location rather than to a working directory
  the model can change. An empty, relative or nonexistent `-C` exits 1,
  and `-C` with `--session` or `--instance` is a usage error (exit 2).
  `--json` prints the record
  (R17) or, with no record, a document with the same schema and
  `"record": false`. It reads only local files and makes no network call,
  and run outside a workspace it exits 1 naming the missing workspace.
- **R22. A session reads its own grant.** Run with no selector from inside
  a dispatched instance, by a Claude or a Codex session, from the instance
  root, a repository inside it, or a worktree of one, `niwa authority
  show` reports that instance's dispatch. It does so from the session's
  first command, before the session's own mapping exists, and after a
  resume that gives the session a new id. Subagents running in the
  session's working directory, or in a worktree of its instance, get the
  same answer.
- **R23. No record means no named authority.** When no record is found for
  the selected session or instance (a session niwa didn't dispatch, a
  person's own session, or an instance dispatched before this feature),
  `niwa authority show` exits 0 and prints the line `dispatch record:
  none`, an empty grant, and the level a no-flag dispatch would resolve to
  with its source followed by ` (no dispatch record)`. With `--json` the
  document carries `"record": false`, an empty `grant`, and that level
  with its plain source in `level_source`; `record` is what marks it. A
  record that exists but can't be read or is malformed (R17a) makes it
  exit 1, never fall back to the no-record answer; an unknown authority
  or source kind is not malformed and lands in `unknown_grant`.
- **R24. "Held" is never inferred from an exit code.** A caller treats an
  authority as held only when `niwa authority show --json` exits 0 and
  prints a document with the documented schema name whose grant names that
  authority. Every other outcome (a non-zero exit, no output, help text
  from a niwa without the command, an unknown schema, a malformed
  document) means not held. The guide (R34) states this rule, and the
  command and its flags appear in `niwa authority --help` so callers can
  detect them.
- **R25. Telemetry carries it as observability.** A dispatch whose agent
  receives niwa's lineage resource attributes (Claude Code today) adds
  `niwa.authority.level=<level>` and `niwa.authority.grant=<a/b/...>` (the
  granted authorities in R3's order joined with `/`, or `none`) to the
  attributes it sends the worker, alongside the lineage attributes it
  already sends. The separator is `/` because `OTEL_RESOURCE_ATTRIBUTES`
  separates entries with commas. An agent that receives no lineage
  attributes (Codex today) gets none, and its record carries the same
  values. Telemetry is best-effort and isn't the record.

**Answering a decision point**

- **R26. The decision-point question.** `niwa authority decide <point>`
  prints, one per line, `point=`, `level=`, `decider=`,
  `author_exclusion=`, `source=` and `record=present` or `record=none`,
  using the same dispatch selection as R21 and, with no record, the level
  a no-flag dispatch would resolve to with source suffix ` (no dispatch
  record)`. It exits 0 when it printed
  an answer, 2 on a usage error (a missing point, or one not matching
  `^[a-z][a-z0-9-]{0,63}$`, checked first), and 1 on a configuration or
  record error or outside a workspace. A caller settles the point itself
  only when the command exits 0 and prints `decider=session`; any other
  outcome means the point isn't the session's to settle. The answer
  doesn't read the grant (R4).
- **R27. A seam for per-point declarations.** The answer is computed from
  a per-point declaration (a decider and an author-exclusion value) when
  one exists for that point, and from R2 otherwise. A level narrower than
  the declaration narrows each component to the stricter of the two;
  nothing makes a declared answer wider. A declared point the level didn't
  narrow (including a level whose value equals the declared one) reports
  `source=declaration`; one where the level narrowed either component
  reports the level's source. No declaration source exists in any
  workspace today, so every point resolves by R2.

### Non-functional

- **R28. Enforcement doesn't change.** Nothing in this feature changes
  what a session can do. No permission mode, hook, gate, credential or
  settings document a worker launches with depends on its level or grant,
  apart from the lineage attributes of R25. A grant is a declared,
  tracked, readable record; the workspace's gates decide exactly as they
  did before until separate work makes them read it, and a session
  holding an authority can still be stopped by its permission mode or by
  a gate. Nothing here promises that a session can't get past the
  workspace hook, or can't take an action its grant leaves out.
- **R29. Independent of the permission mode.** The authority level and
  grant never change the permission mode a worker launches with, and no
  permission-mode setting (including a `bypass` workspace posture) changes
  the level or the grant. The permission mode keeps its order (flag, then
  machine, then workspace posture, with the machine outranking the
  workspace both ways); the authority settings resolve narrowest-wins
  across workspace, machine and dispatch, and the machine can only narrow
  them.
- **R30. Nothing changes for workspaces that set nothing.** With no
  `[authority]` table, no machine authority keys and no authority flags, a
  dispatch launches the worker with the same argv and the same settings
  document as before, apart from R25's two attributes; the other visible
  differences are R19's stderr line, the record of R17 and R20's field.
  Every decision point resolves to `session` and the grant is empty.
- **R31. No koto.** Every requirement here holds on a host with niwa and
  no koto installed, and no part of the record or the lookup reads
  anything koto writes.
- **R32. Every agent.** The record (R17), the session's read-back (R22)
  and `niwa list --json` (R20) work the same for every agent `niwa
  dispatch` can launch (Claude Code and Codex today).
- **R33. Cheap and offline lookups.** `niwa authority show` and `niwa
  authority decide` read only local files, open no network connection,
  start no other process, and read no session mapping except the one a
  `--session` lookup names, so their cost doesn't grow
  with the number of sessions in the workspace and a hook can call them
  on the commands it gates. Measured through the built binary, the median
  of ten runs on a workspace with 250 session mappings is under 200
  milliseconds on the CI runners niwa's tests use.
- **R34. The record format is documented.** A guide at
  `docs/guides/authority-grants.md` documents the
  `[authority]` keys, the machine keys, the flags, what each named
  authority covers, the record's JSON shape and schema version, the
  selection order of R21, the no-record answer, the "held" rule of R24,
  the settle rule of R26, how level and grant combine (R4), that
  `level_source` describes resolution at dispatch and can differ from
  `level_ceiling_source` (R13, R16a), the additive versioning rule and
  `unknown_grant` (R17a), `--parent-from` and `parent_dispatch_source`
  (R14a), and what the record does and doesn't guarantee. It says plainly
  that until a workspace adds an `[authority]` ceiling nobody can be
  granted anything, and that once it exists a successor gets a fresh
  grant with `--role coordinator` and no config edit. It tells anyone
  archiving an instance to save `niwa authority show --json -C
  <instance>` before `niwa destroy`, since the record goes with the
  instance. It tells callers that dispatch refuses an unknown flag, and
  that a caller unable to pass a narrowing flag (`--level`) must not
  dispatch without it, while one unable to pass `--grant`, `--role` or
  `--parent-from` may, since the session then holds less or records no
  parent.

## Acceptance Criteria

Declaring the policy

- [ ] A workspace with `[authority] level = "supervised"`, `ceiling =
  ["merge-pr", "close-issue"]` and a role `coordinator` naming all three
  authorities loads; `niwa authority policy` prints level `supervised`
  from `workspace`, the two-entry ceiling from `workspace`, and the role
  with `cut-release` marked outside the ceiling.
- [ ] With no `[authority]` table and no machine keys, `niwa authority
  policy` prints level `autonomous` from `built-in`, an empty ceiling, no
  roles, and a no-flag dispatch resolving to `autonomous` with no grant.
- [ ] Each of `level = "Strict"`, `ceiling = ["merge"]`, a role named
  `Coord_1`, and a role naming `release` makes `niwa dispatch` and `niwa
  authority policy` exit 1 with a message naming the key, the bad value
  and the accepted values, and makes `niwa apply` print that message as a
  warning and exit as it would without the bad value. `level = " strict "`
  loads as `strict`, and `level = ""` loads as not set.
- [ ] With a base ceiling of all three and an overlay `[authority]
  ceiling = ["merge-pr"]` and `level = "strict"`, `niwa authority policy`
  prints the effective ceiling `merge-pr` and level ceiling `strict`, both
  from `overlay`; an overlay ceiling naming an authority the base ceiling
  lacks adds nothing; an overlay `[authority.roles.x]` table changes no
  role and loading the overlay prints a warning naming it.
- [ ] A workspace whose base config holds exactly R8a's example dispatches
  with `--role coordinator` and records all three authorities from
  `role:coordinator`.
- [ ] `niwa config set authority-level strict` writes `authority_level =
  "strict"` under `[global]`; `niwa config set authority-ceiling
  merge-pr,close-issue` writes the two-entry list; `niwa config set
  authority-level loose` and `niwa config set authority-ceiling merge`
  exit non-zero and leave the file unchanged; each `niwa config unset`
  removes its key.

Level resolution and refusal

- [ ] Workspace `strict`, no machine key, no flag: the dispatch's record,
  `niwa authority show` and `niwa list --json` each report `strict` from
  `workspace`.
- [ ] Workspace `autonomous`: `--level supervised` records `supervised`
  from `flag`; a second dispatch with no flag records `autonomous` from
  `workspace`.
- [ ] Workspace `supervised` with `--level supervised` records
  `supervised` from `flag` (a tie goes to the flag).
- [ ] Workspace `supervised` with `--level autonomous` exits 1, the
  message names `autonomous`, `supervised` and `workspace`, and afterwards
  no new instance directory, no record and no session mapping exist, no
  other instance was removed, and the fake launcher recorded no launch.
- [ ] Machine `authority_level = "strict"`, workspace `autonomous`, no
  flag: records `strict` from `machine`; with `--level supervised` it
  exits 1 naming `machine` as the ceiling's source.
- [ ] No workspace key, no machine key, no flag: records `autonomous` from
  `built-in`.

Granting and refusal

- [ ] Workspace ceiling of all three and a role `coordinator` naming all
  three: a dispatch with `--role coordinator` records `merge-pr`,
  `close-issue` and `cut-release`, each with source `role:coordinator`; a
  second dispatch with `--grant merge-pr` records `merge-pr` alone with
  source `dispatch`; `niwa authority show` and `niwa list --json` report
  the same for each.
- [ ] A dispatch with `--grant close-issue --role coordinator` records
  `close-issue` once with sources `dispatch` and `role:coordinator`.
- [ ] Workspace ceiling `["merge-pr", "close-issue"]`: a dispatch with
  `--grant cut-release` exits 1 and the message names `cut-release`,
  `--grant` and the ceiling from `workspace`; a dispatch with `--role
  coordinator` (a role naming `cut-release`) exits 1 and the message names
  `cut-release`, the role `coordinator` and the ceiling. Neither leaves an
  instance, a record or a mapping, and neither launches.
- [ ] Workspace ceiling of all three, machine `authority_ceiling =
  ["merge-pr"]`: `--grant close-issue` exits 1 naming the ceiling's
  source as `machine`; `--grant merge-pr` launches and records `merge-pr`
  from `dispatch`.
- [ ] A workspace with no `ceiling` key: `--grant merge-pr` exits 1 naming
  the empty ceiling and the `[authority] ceiling` key.
- [ ] `--grant merge` and `--role nobody` (undeclared) each exit 1 naming
  the value and the accepted values, with nothing created.
- [ ] A dispatch with no `--grant` and no `--role` records an empty grant,
  and `niwa authority show` and its stderr line both say `none`.
- [ ] A dispatch refused when authority is resolved (R16), run with no
  positional prompt from a terminal, exits 1 without prompting for one.
- [ ] R16a: when provisioning brings in an overlay whose ceiling leaves out
  a requested authority, the dispatch has printed R19's line, then exits 1
  naming the overlay's ceiling; the new instance is gone, no record or
  mapping exists and nothing launched. When provisioning brings in an
  overlay that removes `cut-release` from the ceiling of a dispatch that
  asked only for `merge-pr`, the dispatch launches and its record shows
  the narrower ceiling with source `overlay`.

Read-back

- [ ] Every launching dispatch's stderr contains exactly one line starting
  `niwa dispatch: authority level `, and a dispatch refused when authority
  is resolved (R16) has none on its stderr. A dispatch with no authority
  settings or flags prints
  exactly `niwa dispatch: authority level autonomous from built-in; grant
  none`; one with `--level strict --grant merge-pr` in a workspace whose
  ceiling holds `merge-pr` prints exactly `niwa dispatch: authority level
  strict from flag; grant merge-pr (dispatch)`.
- [ ] A fake agent that runs `niwa authority show --json` as the first
  command of its launch, before niwa has written its session mapping,
  receives the record for its own dispatch, with the level and grant the
  dispatch printed. The same holds for a Claude dispatch and a Codex
  dispatch, run from the instance root and from a repository inside it,
  and when the worker's environment carries the dispatcher's own session
  id variable, pointing at a session with a different record.
- [ ] `niwa authority show --session <id>` and `--instance <name>` each
  return the matching dispatch's record from the workspace root.
- [ ] `niwa authority show --json -C <instance>/.claude/hooks/pre_tool_use`,
  run from the workspace root, prints output byte-identical to running
  `niwa authority show --json` from that directory; `-C` pointing at a
  repository's hook directory inside the instance gives the same record.
- [ ] An overlay `[authority] ceilng = []` (misspelt) and a machine
  `authority_celing = []` each make `niwa dispatch` exit 1 naming the key;
  `niwa authority show -C ""`, `-C relative/dir` and `-C /no/such/dir`
  exit 1, and `-C <dir> --session <id>` exits 2; `niwa authority decide x`
  prints `record=present` inside a dispatched instance and `record=none`
  at the workspace root.
- [ ] A dispatch run from inside a dispatched instance records that
  instance's `dispatch_id` as `parent_dispatch_id` with
  `parent_dispatch_source: cwd`, including when the dispatcher is a Codex
  worker whose environment carries its own dispatcher's session id; a
  dispatch run from the workspace root records an empty
  `parent_dispatch_id` with `parent_dispatch_source: none`; a dispatch run
  from the workspace root with `--parent-from <that instance's absolute
  path>` records the instance's `dispatch_id` with
  `parent_dispatch_source: anchor`. `--parent-from` with an empty,
  relative or nonexistent path exits 1 with nothing created;
  `--parent-from` naming an instance with no record records `none`;
  naming one whose record is malformed refuses the dispatch.
- [ ] `--grant merge-pr,close-issue` records the same grant as `--grant
  merge-pr --grant close-issue`; `--grant merge-pr,` exits 1 naming the
  empty entry.
- [ ] An overlay ceiling naming `cut-release` over a base ceiling of
  `["merge-pr"]` leaves `cut-release` ungrantable, and `niwa authority
  policy` and `niwa apply` each print a warning naming `cut-release` and
  the overlay; `niwa authority policy --json` carries `schema:
  "niwa.authority.policy/v1"`.
- [ ] Run from the workspace root with no selector and no session id, or
  with a session id that has no record, `niwa authority show` exits 0 and
  prints `dispatch record: none` and an empty grant.
- [ ] A record holding `level: bogus`, truncated JSON, a missing
  `dispatch_id`, or an authority name with an uppercase letter makes `niwa
  authority show` and `niwa authority decide merge-own-pr` exit 1.
- [ ] A v1 record that grants `close-issue` (source `dispatch`) and
  `teardown` (source `dispatch`), has a `merge-pr` entry whose only source
  is `delegated:x`, and carries an extra top-level field `supervisor`
  makes `niwa authority show --json` exit 0, with `close-issue` in
  `grant`, `teardown` and `merge-pr` under `unknown_grant`, and the extra
  field ignored; the guide's held check reads `close-issue` as held and
  `teardown` and `merge-pr` as not held; a dispatch run from inside that
  instance proceeds and records its `parent_dispatch_id`.
- [ ] After the dispatch, editing the workspace ceiling to empty and
  running `niwa apply` leaves `niwa authority show` for the existing
  dispatch unchanged.
- [ ] After `niwa reset` of a dispatched instance, `niwa authority show`
  inside it prints `dispatch record: none`.
- [ ] `niwa list --json` omits `authority` for an instance with no record
  and includes it, with level, source and grant, for a dispatched one.
- [ ] `niwa authority show --json` for a `--grant merge-pr` dispatch exits
  0 and prints a document with `schema` equal to
  `niwa.authority.record/v1`, every field R17 names, `parent_session_id`
  equal to the dispatching session's id (empty for a dispatch run from a
  plain shell), and `grant` holding one entry with `authority` `merge-pr`
  and `sources` `["dispatch"]`; for a no-grant dispatch `grant` is empty;
  in a no-record directory it exits 0 with `"record": false` and an empty
  `grant`.
- [ ] Editing only the instance's record file (changing the level) changes
  what `niwa authority show` and `niwa list --json` report for that
  instance, showing they read the one copy.
- [ ] A Claude dispatch's `OTEL_RESOURCE_ATTRIBUTES`, in both the launch
  environment and the settings document's `env`, contains
  `niwa.authority.level=<level>` and `niwa.authority.grant=<list>`; a
  Codex dispatch's launch environment contains neither.

Decision points at the two ends

- [ ] In a dispatched instance whose record says `autonomous`, `niwa
  authority decide merge-own-pr` prints `decider=session` and
  `author_exclusion=off`; at `strict` it prints `decider=person` and
  `author_exclusion=on`; at `supervised` it prints `decider=dispatcher`
  and `author_exclusion=on`. Each exits 0.
- [ ] A dispatch at `strict` granted `merge-pr` still gets
  `decider=person` from `niwa authority decide merge-own-pr`.
- [ ] A functional test dispatches a fake agent whose script runs `niwa
  authority decide merge-own-pr` and writes `settled` to a marker when the
  command exits 0 and prints `decider=session`, and `waiting-for-person`
  otherwise. In an `autonomous` workspace the marker reads `settled`;
  dispatched with `--level strict` it reads `waiting-for-person`.
- [ ] Live check with a real Claude Code session, in a workspace holding
  R8a's example: a session dispatched with `--grant merge-pr`, briefed to
  read back its own grant with `niwa authority show` and to reach a merge
  decision by running `niwa authority decide merge-own-pr`, reports
  `merge-pr` from `dispatch`, settles the point itself at `autonomous`,
  and stops for a person when dispatched with `--level strict`. From
  inside that session, a dispatch asking for a role or grant past a
  narrowed ceiling (the overlay or machine ceiling set to `merge-pr`, and
  `--grant cut-release`) is refused before anything launches, naming
  `cut-release` and the ceiling. It passes when the recorded transcript
  shows, at `autonomous`, `decider=session` followed by the session
  proceeding with the merge step it was briefed on; at `strict`,
  `decider=person` followed by the session stopping without that step;
  the session's own `niwa authority show` output naming `merge-pr` from
  `dispatch`; and the refusal text. The transcript excerpts are recorded
  in the implementing pull request. This check is run once before merge;
  the fake-agent test above is what CI runs.
- [ ] Point names: `a`, `merge-2` and a 64-character name are accepted;
  `Merge_Now`, `2merge`, `-merge`, an empty name, a 65-character name and
  a missing argument each exit 2.
- [ ] Unit tests on the declaration seam: a declaration of
  `dispatcher`/`off` for `merge-own-pr` at `autonomous` returns
  `dispatcher`/`off`/`declaration` for `merge-own-pr` and `session`/`off`
  for `accept-prd`; the same declaration at `strict` (source `flag`)
  returns `person`/`on`/`flag`; at `supervised` (source `workspace`) it
  returns `dispatcher`/`on`/`workspace`, the level narrowing only author
  exclusion; a declaration of `person`/`on` at `autonomous` stays
  `person`/`on`/`declaration`.
- [ ] `niwa authority decide merge-own-pr` prints `source=flag`,
  `source=machine`, `source=workspace` and `source=built-in` for records
  resolved from each; `niwa authority decide Bad_Name` run outside a
  workspace exits 2; `niwa authority decide x` outside a workspace exits
  1, and from the workspace root (no record) with an invalid machine value
  exits 1.
- [ ] Inside a dispatched instance with a valid record, after the base
  config gains `[authority] level = "Strict"` and the machine config
  gains `authority_level = "loose"`, `niwa authority show --json` and
  `niwa authority decide merge-own-pr` still exit 0 and report the
  recorded dispatch, while `niwa authority policy` exits 1.

Edge cases and errors

- [ ] A hand-edited personal config with `authority_level = "loose"`, and
  separately `authority_ceiling = ["merge"]`, makes `niwa dispatch` and
  `niwa authority policy` exit 1 naming the key and the value, with
  nothing created.
- [ ] A `workspace.toml` with a TOML syntax error, and separately one that
  can't be read, makes `niwa dispatch` exit 1 before any instance is
  created.
- [ ] `--level loose` and `--level Strict` each exit 1 naming the value and
  the three levels; `--level " strict "` and `--grant " merge-pr "` are
  accepted as `strict` and `merge-pr`; `--grant Merge-PR` exits 1.
- [ ] `niwa authority policy --json` emits one JSON document carrying the
  same values as the text form; `niwa authority policy`, `show` and
  `decide x` run outside a workspace each exit 1 naming the missing
  workspace.
- [ ] Machine `authority_ceiling = []` with a workspace ceiling of all three:
  `niwa authority policy` prints an empty effective ceiling from
  `machine`, and `--grant merge-pr` is refused. Machine `authority_level =
  "autonomous"` with workspace `strict` records `strict` from `workspace`.
  Machine `authority_ceiling = ["cut-release"]` with workspace ceiling
  `["merge-pr"]` gives an empty effective ceiling. Base ceiling of all
  three, overlay `["merge-pr", "close-issue"]` and machine `["merge-pr",
  "cut-release"]` give an effective ceiling of `merge-pr` from `machine`.
- [ ] With base ceiling of all three, machine `authority_ceiling =
  ["merge-pr"]` and a role naming all three, `niwa authority policy`
  marks `close-issue` and `cut-release` on that role as outside the
  effective ceiling.
- [ ] With the session id variable naming a session that has a record,
  `niwa authority show -C <dir>` where `<dir>` is in no instance, and
  `niwa authority show` with no selector run from the workspace root,
  each print `dispatch record: none`.
- [ ] Workspace ceiling `["merge-pr"]`: `--grant merge-pr --grant
  close-issue --grant cut-release` exits 1, names both `close-issue` and
  `cut-release` with `--grant` as the route, and launches nothing.
- [ ] A role with `authorities = []` granted with `--role` records an
  empty grant with the role listed; two roles that both name `merge-pr`
  record `merge-pr` once with both `role:` sources.
- [ ] A dispatch's record is gone after the dispatch is rolled back (the
  fake launcher fails), after `niwa destroy` of the instance, and after
  `niwa reap` reclaims it; it is unchanged after the session is resumed
  through `claude attach` or `codex resume`.
- [ ] `niwa authority show` from a worktree of a repository inside a
  dispatched instance reports that instance's record.
- [ ] With the working directory in instance `b`, `niwa authority show
  --session <a>` reports session `a`'s dispatch; with no selector and the
  session id variable naming session `a`, it reports `b`.
- [ ] With no record, `niwa authority decide merge-own-pr` in a `strict`
  workspace prints `level=strict` and `source=workspace (no dispatch
  record)`, and `niwa authority show --json` exits 0 with an empty grant
  and a field stating there's no record.
- [ ] `niwa authority --help` lists `policy`, `show` and `decide`, and
  `niwa dispatch --help` lists `--level`, `--grant` and `--role`;
  `niwa dispatch --no-such-flag` exits non-zero.
- [ ] A workspace with a bad `[authority]` value and one with a bad machine
  value each refuse a dispatch with no new instance, record or mapping
  and no launch.
- [ ] An overlay `[authority.roles.x]` table produces its warning on `niwa
  apply` and `niwa dispatch` as well as `niwa authority policy`.
- [ ] Machine and workspace both `strict`, no flag: records `strict` from
  `machine`. A role named with 64 characters loads; one with 65 is
  refused. `--role Bad_Name` exits 1. `--session <a> --instance <b>`
  exits 2; `--instance missing` exits 1.
- [ ] A record file made unreadable (mode 000) makes `niwa authority show`
  exit 1. An instance dispatched by a build without this feature reports
  `dispatch record: none`.
- [ ] A later dispatch of another session, and a change to the machine
  config, leave an existing record byte-identical.
- [ ] `docs/guides/authority-grants.md` exists and contains each of these
  strings: `[authority]`, `authority_level`, `authority_ceiling`,
  `--level`, `--grant`, `--role`, `merge-pr`, `close-issue`,
  `cut-release`, `niwa.authority.record/v1`, `dispatch record: none`,
  `decider=session`, and R8a's example block.
- [ ] `niwa config set authority-ceiling " merge-pr , merge-pr ,close-issue"`
  writes `["merge-pr", "close-issue"]`; `niwa config set
  authority-ceiling none` writes `[]`.
- [ ] Roles `b-role` and `a-role` both naming `merge-pr`, granted with
  `--grant merge-pr --role b-role --role a-role`, record `merge-pr` with
  sources `["dispatch", "role:a-role", "role:b-role"]`.
- [ ] Workspace `strict` and overlay `strict`, no machine key: the level
  ceiling's source is `overlay`.
- [ ] A fake agent that resumes under a new session id, then runs `niwa
  authority show` from its instance, gets the same record.
- [ ] A detached dispatch and a foreground dispatch each have their record
  in place when the fake agent's first command runs.
- [ ] R19's line for a dispatch granted `--grant close-issue --role
  coordinator` (a role naming `merge-pr` and `close-issue`) reads exactly
  `grant merge-pr (role coordinator), close-issue (dispatch, role
  coordinator)`; the record writes the same sources as `dispatch` and
  `role:coordinator`.

Unchanged behaviour

- [ ] With no authority settings and no authority flags, a Claude
  dispatch's argv equals the argv the existing dispatch tests record, and
  its settings document equals the pre-feature document apart from the two
  added lineage attributes.
- [ ] A dispatch at `strict` with `--permission-mode bypassPermissions`
  launches with `--permission-mode bypassPermissions` and records
  `strict`; a dispatch with `--grant merge-pr` and no permission flag or
  machine permission mode launches with no `--permission-mode`, as before;
  a workspace with a `bypass` posture and no `[authority]` records
  `autonomous` from `built-in` and an empty grant.
- [ ] The instance's hook files, and every settings document a dispatch
  writes, are byte-identical between a dispatch with `--role coordinator`
  and one with no grant, apart from the lineage attributes.
- [ ] The authority unit and functional tests pass on a CI runner with no
  koto binary on `PATH`.
- [ ] With 250 session mappings in a test workspace, the median of ten
  runs of the built binary's `niwa authority show` and `niwa authority
  decide merge-own-pr` is each under 200 milliseconds in CI.
- [ ] A unit test runs `show` and `decide` with the package's network and
  process seams replaced by doubles that fail the test when called, and
  passes; a second test counts session-mapping reads and finds at most
  one.
- [ ] A dispatch with `--level strict` and one with no flags write
  byte-identical hook files and settings documents apart from the lineage
  attributes.
- [ ] A Codex dispatch with `--grant merge-pr` appears in `niwa list
  --json` with an `authority` object naming `merge-pr`.
- [ ] A Claude dispatch whose environment already carries
  `OTEL_RESOURCE_ATTRIBUTES=team=x` sends the worker a value holding
  `team=x`, the existing lineage attributes, `niwa.authority.level` and
  `niwa.authority.grant=merge-pr/close-issue` for a dispatch granted
  those two.

## Out of Scope

- **Gates honouring the grant.** The workspace hook keeps deciding merges,
  closes and releases exactly as today. Making it, a niwa-owned gate, or a
  coordinator's posture read honour the grant is separate work; R17, R21,
  R23 and R24 are what it will consume, and it will combine grant and
  level as R4 states.
- **Capping a child at its parent.** A session that dispatches again gets
  the workspace and machine ceilings, not its own grant or level as a cap.
  Refusing a child wider than its parent is separate work. It reads the
  dispatcher's record from the instance `niwa dispatch` is run from, by
  working directory only and never through the session id variable, and
  the `parent_dispatch_id` R17 keeps links a child's record to its
  parent's. That link can dangle, since a coordinator usually reaps its
  predecessor: nothing resolves it as a live session, and a missing
  parent or older ancestor record means no cap, never a refusal. Routing a
  decision to the dispatcher (R2) is also separate work, and so is
  shirabe's coordinator passing `--parent-from` with its own instance,
  which lands with or after shirabe's dispatch-path rewrite (shirabe#978
  to #986).
- **Stronger enforcement.** Credential scoping, wrapping forbidden
  commands, a record the session can't edit, and refusing a session whose
  record is missing or malformed at a gate are a separate, later effort.
- **Changing the permission mode** (R29).
- **Telling a session its grant at start, and routing an escalated
  decision.** A session reads its grant when it asks (R22); what it's told
  in its first prompt, and how a decision that goes to a person or a
  dispatcher reaches them, are separate work.
- **Per-decision-point strictness declared by an organization.** R27
  leaves the seam; nothing here declares a point.
- **Workspace-defined named authorities, and authorities beyond three.**
  Workspaces bundle authorities into roles but can't invent new ones
  (R3). Teardown of an instance is the likeliest next authority and is
  left to the work that gates it.
- **Changing a grant while its session runs** (R18).
- **Declaring a ceiling in this project's own workspace config.** Because
  an absent ceiling grants nothing, no workspace can grant merge, close or
  release until its config says so. Adding R8a's `[authority]` table to
  the tsukumogami workspace's public config (`dot-niwa`), so successor
  coordinators can be granted merge, is a follow-up once this feature
  ships, not part of it.
- **Recording who decided.** R26 says who may decide a point; recording
  who did, and letting a higher authority reverse it, is separate work.

## Known Limitations

- The record and every answer read from it are advisory. A session runs
  as the operator, can ignore them, and can edit its own instance's
  files, including the record. Within one operator's workspace that's no
  escalation, since anything running as the operator already holds their
  rights; R28 is the honest statement of where enforcement stands.
- A session's instance, and the record with it, are deleted at reap.
  After reap only the telemetry attributes remain, and they're
  best-effort observability, not an audit log.
- `--session` accepts any session's id and checks nothing about the
  caller, so a session can read another's record. That gains nothing a
  session couldn't already do, and later work that trusts the answer
  about another session has to account for it. `--parent-from` is
  likewise advisory: it records the parent the dispatcher names.
- A session started by hand, or an instance made by `niwa create`, has no
  record: no grant and no cap. A later command that attaches a record to
  such an instance needs no format change.
- A child dispatch isn't capped at its parent's grant or level, so a
  session granted nothing can still dispatch a child granted anything
  inside the ceiling until never-wider work lands.
- A record states what was resolved at dispatch. If the workspace later
  narrows its ceiling, existing records still show the wider grant; a
  later gate decides whether to also check the current ceiling.
- Dispatch resolves against the workspace config on disk when it starts.
  Provisioning can refresh that config, or bring in an overlay, after
  that; R16a re-checks before writing the record, which makes this the
  one refusal that can come after the prompt and the reclamation sweep.
  A change that would alter the dispatch's level, in either direction,
  or its grant refuses that dispatch; a ceiling that only widened is
  recorded, and the wider ceiling is available to the next dispatch. `niwa authority policy` and the no-record answer have no such
  re-check, so a convention overlay that isn't cloned yet can make them
  show a wider policy than a dispatch would end up with.
- A niwa binary older than this feature ignores `[authority]` with an
  unknown-key warning, and one that rewrites the personal config drops the
  machine keys it doesn't know. Neither can grant a named authority, since
  an older binary has no grants, but the second silently removes a
  machine's narrowing until it's set again.
- Subagents share their session's working directory and so its grant;
  that's inheritance within one session, not a new dispatch. A second
  session started by hand inside a dispatched instance reads that
  instance's record too, which matches how today's per-instance exemption
  behaves.
- With no record, `niwa authority decide` answers from the level a no-flag
  dispatch would get, which can be `autonomous`. That's the right answer
  for the operator's own session, and the `(no dispatch record)` suffix
  lets a caller that needs a dispatched session's answer tell the two
  apart.
- The record is written for a Codex dispatch like any other, but nothing
  gates a Codex session today, so the grant has no effect there beyond
  being readable.

## Decisions and Trade-offs

- **The level and the grant are two settings, and both must allow an
  action.** The level is a single ordered value that says who settles a
  decision point; the grant is a set of actions. Folding them together
  (say, `strict` forbidding a merge grant) would make it impossible to say
  "this session may merge, but only after a person agrees", the strict
  end of the author-exclusion case. Requiring both (R4) keeps an
  autonomous session without a grant from reading "settle it yourself" as
  permission. Alternative: one dial that implies the grant. Rejected
  because it erases that case.
- **Three levels, one per decider.** Each level maps to one decider
  (`session`, `dispatcher`, `person`), so a workspace can say "the session
  that launched mine decides, not me" without a fourth value. Kept from
  the earlier draft in pull request #401, with the middle decider renamed
  from `coordinator` to `dispatcher` so it can't be read as the role of
  that name.
- **A closed set of named authorities, each defined by what it covers.** A
  gate that reads `merge-pr` has to know which commands it covers, and so
  does a coordinator mapping its own steps onto it, so niwa owns the names
  and their meaning. Alternative: workspace-declared authorities. Rejected
  for now because a gate can't honour a name it can't interpret; roles
  give workspaces their own vocabulary on top.
- **An absent ceiling grants nothing.** A workspace has to opt in before
  any dispatch holds a named authority, so a gate that later honours
  grants can never be widened by a workspace that never thought about it.
  Alternative: an absent ceiling allows every authority. Rejected because
  it would turn every unconfigured workspace into one where any dispatcher
  can grant release authority the moment gates start reading grants.
- **A role past the ceiling is refused when it's granted, not when it's
  declared.** The effective ceiling depends on the machine (R9), which the
  workspace config can't see, so a load-time check would either miss the
  machine's narrowing or fail a shared config on one laptop.
  `niwa authority policy` marks such roles so the owner sees them early.
  Alternative: refuse at declaration against the workspace ceiling.
  Rejected because it checks against the wrong ceiling.
- **The level ceiling and the grant ceiling are two settings.** One is an
  ordered value and the other a set, and a team may want "strict, but
  coordinators may merge" as readily as "autonomous, but nobody releases".
- **Narrowest wins, the machine never widens.** On the permission mode the
  machine outranks the workspace both ways, because tool-call approval is
  about trust in one's own machine. Who decides and what a session may do
  are a team's rule, so a machine owner may tighten them for their own
  dispatches and may not loosen them. The two orders sit side by side and
  neither reads the other (R29).
- **Refuse, don't trim.** A request past a ceiling launches nothing, so an
  operator never believes a dispatch got authority it didn't.
- **The base config declares, an overlay only narrows.** Authority policy
  is security-relevant, and an overlay is invisible to most contributors,
  so anything that widens policy (a role, a ceiling entry, a wider level)
  lives in the base config everyone can read. An overlay may still make
  policy stricter, the same way the machine can, so a workspace that keeps
  private settings in an overlay can tighten its ceiling or level there
  without publishing why. Nothing in a role is private: a role names
  public authorities, and instance names, the private part of today's
  exemption, play no part in a grant. Alternatives: base config only
  (rejected: a private overlay couldn't narrow anything), or overlays
  that may add roles (rejected: an invisible widening).
- **A bad `[authority]` value blocks dispatch, not `niwa apply`.** Failing
  dispatch closed keeps a typo from widening anything; letting apply carry
  on with a warning keeps the workspace able to pull the fix.
- **The record is written before launch and kept with the instance.** A
  successor's first action must find its grant, and neither agent's
  session id is known until after the worker starts, so the record is
  keyed by the instance and the dispatch id, not by session. There's one
  copy: `niwa list --json` and `niwa authority show` read it from the
  instance, and a lookup by session id goes through the session mapping
  to the instance. Alternative: also copy it into the session mapping, as
  #401 did. Rejected because two copies can disagree and the mapping is
  written after launch (after the first turn, for a foreground dispatch).
- **Self-read goes through the working directory.** A Codex worker gets no
  launch environment from niwa and inherits its dispatcher's session id
  variable, and a resumed Claude session can get a new id, so the
  instance containing the working directory is the one key that works for
  every agent and survives resume. The variable is never a fallback
  either: outside any instance the answer is no record, so a session can't
  read its dispatcher's record as its own. Alternative: fall back to the
  variable outside an instance, with a field saying how the record was
  selected. Rejected because the only session that would use it is one
  that left its instance or never had one, and for both the dispatcher's
  record is the wrong answer; `--session` remains for an explicit lookup.
- **Authority belongs to the instance.** A session resumed in another
  instance, or on another host, reads that instance's record, or none.
  The record is never copied into a workflow engine's state or any cloud
  store.
- **Read-back goes through one command.** Gates, coordinators and the
  session itself use `niwa authority show` rather than parsing niwa's
  files, so the file layout can change without breaking them.
- **No record means no named authority; "held" needs a positive document.**
  A lookup that can't find a record answers with an empty grant, so a
  later gate is never weaker than today for a session niwa didn't
  dispatch. A record that exists but is broken is an error. Because a niwa
  without the command can print help and exit 0, "held" and "settle" are
  read from printed output in a documented shape, never from an exit code
  alone (R24, R26). Alternative: #401's exit 0 for "settle" and 3 for
  "hand it on". Rejected because exit 0 is what an old niwa returns too.
- **The level flag is `--level`.** `--authority strict` would read as
  granting an authority called `strict` once named authorities exist.
- **The built-in source is named `built-in`, not `default`.** `default`
  is also a permission mode, and both appear on dispatch's stderr.
- **Default level `autonomous`.** It's today's behaviour, and a workspace
  that declares nothing must never be told to ask a person.
- **The live-session criterion stays.** A fake agent proves the branch,
  but the feature's promise is that a real session acts differently at
  the two ends, and one recorded run is the evidence for that.
- **What #401 contributed and what changed.** Kept: three ordered levels,
  narrowest wins, refuse rather than trim, fail-closed dispatch on bad
  config, a record written before launch, the per-point answer and its
  declaration seam, independence from the permission mode, the telemetry
  attribute. Changed: the level moves from `[workspace] authority_level`
  to an `[authority]` table that also holds the ceiling and roles; the
  flag becomes `--level`; the record has one copy, in the instance; `niwa
  authority show` reads a dispatch's record, and the workspace policy
  moves to `niwa authority policy`; `decide` answers through printed
  output rather than exit 0 or 3; the no-record answer is an empty grant;
  and the record and read-back carry the grant.
