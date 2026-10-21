---
schema: prd/v1
status: In Progress
problem: |
  A session `niwa dispatch` launches settles every decision point it reaches
  the same way in every workspace, because nothing tells it how much it may
  decide. The only authority setting niwa has, the permission mode, governs
  tool-call approval rather than who decides, and nothing records what a
  dispatch was allowed or lets one dispatch be narrowed.
goals: |
  An operator sets one authority level per workspace, narrows it for a single
  dispatch, and reads back what each dispatch resolved to and why. A session
  at a decision point asks niwa who decides it and whether the author is
  excluded, and gets an answer that is "settle it yourself" at the autonomous
  end and "a person" at the strict end. Workspaces that set nothing behave
  exactly as they do today.
absorbed:
  - docs/briefs/BRIEF-authority-level.md
---

# PRD: authority-level

## Status

In Progress

Absorbed [BRIEF-authority-level](docs/briefs/BRIEF-authority-level.md); carried in Absorbed Brief.

## Absorbed Brief

The same toolkit serves a solo maintainer who lets agents finish their own
work and a team that wants a person at every gate. Both run the same decision
points; what differs is who may decide. niwa launches every delegated
session, so niwa is where that answer has to be set and recorded. The brief
framed the gap as an operator having no way to say how much a dispatched
session may decide: every worker settles every point the same way, the one
per-machine knob governs only tool-call approval, and nothing records or
narrows what a dispatch was allowed.

The outcome it set: an operator sets the workspace's authority once, narrows
it for one dispatch, and reads back what each dispatch resolved to; a session
at a decision point learns whether it settles the point or hands it on; a
workspace that sets nothing keeps today's behavior, and nothing added is
paperwork for its own sake. Its four journeys (a solo maintainer working
unattended, a team lead setting the strict end in shared config, an operator
narrowing one risky dispatch, and a later enforcement feature reading the
answer) are the user stories below, and its scope boundary is the
requirements and Out of Scope below.

## Problem Statement

`niwa dispatch` launches sessions that run as the operator. When one of them
reaches a decision point, such as accepting its own requirements document or
merging its own green pull request, nothing tells it whether the workspace
wants it to settle the point or hand it to someone. A solo maintainer who lets
agents finish their own work and a team that wants a person at every merge get
the same worker, and the difference lives only in how each brief happens to be
worded. Today every dispatched session effectively decides for itself.

The one authority setting niwa does have is the permission mode: the
`--permission-mode` flag, the machine's `[global] dispatch_permission_mode`,
and the workspace's `bypass` posture, in that order. It decides how tool calls
get approved. It doesn't decide who settles a decision point, and on it the
machine outranks the workspace in both directions, which suits a machine
owner's trust in their own laptop but is the wrong shape for a team's rule
about who decides.

Three further gaps follow. There's no way to give one dispatch less room than
the workspace's usual without editing shared config. Nothing records what a
dispatch was allowed, so after the fact an operator can't tell whether a
session that merged its own work was permitted to. And work that will enforce
authority later, keeping an author from approving its own work or applying an
organization's per-point rule about who decides, has no place to read a
resolved answer from.

## Goals

- An operator sets how much authority a workspace's dispatched sessions get,
  once, in the workspace config, and can narrow it for one dispatch.
- No setting anywhere widens what the workspace allows; a request for more is
  refused, not quietly trimmed.
- Every dispatch's resolved level, and where it came from, can be read back
  from niwa while the instance exists, and from telemetry after it's reaped.
- A session reaching a decision point gets a plain answer about who decides it
  and whether the author is excluded, so the same point settles itself at the
  autonomous end and goes to a person at the strict end.
- A later organization declaration of strictness per decision point plugs in
  without changing any caller.
- A workspace that configures nothing keeps today's behavior and is never told
  to ask anyone.

## User Stories

- As a solo maintainer who has configured nothing, I want my dispatched
  workers to keep settling their own decision points, so that unattended work
  still finishes without me.
- As a team lead, I want to set the `strict` level in the workspace config
  everyone clones, so that every teammate's dispatched worker hands decision
  points to a person without each brief having to say so.
- As an operator about to dispatch into an unfamiliar repository from an
  `autonomous` workspace, I want to narrow that one dispatch to `supervised`,
  so that its worker hands decision points to my session instead of settling
  them.
- As an operator, I want a dispatch that asks for more authority than the
  workspace allows to be refused before anything launches, so that a typo or
  an old script can't quietly grant more than the policy says.
- As a machine owner, I want to cap every dispatch on my laptop at `strict`
  whatever the workspace says, so that a cloned config can't make my machine's
  workers settle things on their own.
- As an operator reviewing finished work, I want to read the authority level
  each dispatch ran under and its source, so that I can tell whether a session
  that merged its own work was allowed to.
- As a skill author, I want to ask niwa who decides a named decision point and
  whether the author is excluded, so that my skill settles or escalates by the
  workspace's policy rather than by a hard-coded choice.
- As the author of a later enforcement feature, I want the author-exclusion
  setting and the authority behind it exposed per decision point, so that I
  enforce it without inventing a second setting.

## Requirements

### Functional

**The levels**

- **R1. Three ordered levels.** The authority level takes one of three
  values, ordered from widest to narrowest: `autonomous`, `supervised`,
  `strict`. "Narrower" always means further toward `strict`.
- **R2. What each level means at a decision point.** Until an organization
  declaration exists for a point (R17), the point resolves from the level
  alone. Each level is named for its stance and resolves to one decider:

  | Level | Decider | Author exclusion | Meaning |
  |-------|---------|------------------|---------|
  | `autonomous` | `session` | `off` | the session settles the point itself |
  | `supervised` | `coordinator` | `on` | the session that dispatched it decides |
  | `strict` | `person` | `on` | a person decides |

  Deciders are ordered `session` < `coordinator` < `person`, and author
  exclusion `off` < `on`, from widest to narrowest.

**Setting the level**

- **R3. Workspace level.** A workspace declares its level with
  `authority_level` under `[workspace]` in `.niwa/workspace.toml`. With no key
  the workspace contributes nothing, and the built-in default `autonomous`
  applies with source `default`.
- **R4. Machine ceiling.** A machine may declare `authority_level` under
  `[global]` in the personal niwa config, the same file that holds
  `dispatch_permission_mode` (`$XDG_CONFIG_HOME/niwa/config.toml`, or
  `~/.config/niwa/config.toml` when `XDG_CONFIG_HOME` is unset). It acts only
  as a ceiling: it takes part in R8's narrowest-wins resolution and can never
  make the result wider. `niwa config set authority-level <level>` writes it,
  refusing an invalid value with a non-zero exit, and `niwa config unset
  authority-level` clears it.
- **R5. Per-dispatch narrowing.** `niwa dispatch --authority <level>` takes
  part in R8's resolution for that one dispatch. A value equal to or narrower
  than the ceiling is accepted.
- **R6. Wider requests are refused.** The ceiling is the narrower of the
  workspace level (or the default) and the machine ceiling when set. A
  `--authority` value wider than the ceiling refuses the dispatch before any
  instance is created, with exit code 1 and a message naming the requested
  level, the ceiling level, and the ceiling's source.
- **R7. Values are checked exactly.** Each of the three settings accepts only
  the three level names, lowercase, after trimming surrounding whitespace. An
  empty or whitespace-only value means not set. An invalid workspace value is
  a configuration error: workspace config loading fails with a message naming
  the key, the value and the three accepted names, so `niwa dispatch`,
  `niwa apply` and `niwa authority show` each exit non-zero on it. A
  workspace config that can't be loaded for any reason refuses the dispatch
  before any instance is created, so the level never falls back to the
  default because the file was unreadable. An invalid
  machine value or `--authority` value refuses the dispatch with exit code 1
  before any instance is created, with a message naming the setting and the
  accepted names. `niwa authority show` and `niwa authority decide` exit 1 on
  an invalid workspace or machine value, with the same message.
- **R8. Resolution order.** A dispatch's resolved level is the narrowest of
  the workspace level (or the default), the machine ceiling when set, and the
  `--authority` value when given, once R6 and R7 have passed. Its source is one
  of `flag`, `machine`, `workspace` or `default`: the setting that supplied the
  resolved value, and on a tie the first of that list.
- **R9. Independent of the permission mode.** The authority level never
  changes the permission mode a worker launches with, and no permission-mode
  setting changes the authority level. A dispatch with `--permission-mode
  bypassPermissions` at the `strict` level launches in bypass mode and still
  resolves its decision points to `person`.

**Reading it back**

- **R10. Dispatch says what it resolved.** Every dispatch that launches a
  worker prints exactly one stderr line of the form
  `niwa dispatch: authority level <level> from <source>`. A refused dispatch
  prints its refusal instead.
- **R11. The dispatch record carries it.** The session mapping a dispatch
  writes records the resolved level as `authority_level` and its source as
  `authority_source`. `niwa list --json` includes both fields, with those
  names, for each instance whose newest mapping carries them, and omits them
  for an instance whose newest mapping doesn't (one written before this
  feature).
- **R12. It outlives the instance.** A dispatch whose agent receives niwa's
  lineage resource attributes (Claude Code today) adds
  `niwa.authority.level=<level>` to the `OTEL_RESOURCE_ATTRIBUTES` value it
  sends the worker. An agent that receives no lineage attributes (Codex today)
  gets no telemetry attribute.
- **R13. Inspecting the configuration.** `niwa authority show`, run inside a
  workspace, prints three lines: `workspace: <level|not set>`,
  `machine: <level|not set>`, and `default dispatch: <level> from <source>`,
  the last being what a dispatch with no flag would resolve to. Outside a
  workspace it exits 1 naming the missing workspace.

**Asking about a decision point**

- **R14. The decision-point question.** `niwa authority decide <point>`
  prints, one per line, `point=<point>`, `level=<level>`,
  `decider=<session|coordinator|person>`, `author_exclusion=<on|off>` and
  `source=<source>`. It exits 0 when the decider is `session` and 3 otherwise.
  It exits 2 on a usage error (missing or invalid point name), checked first,
  and 1 on a configuration error or outside a workspace.
- **R15. Whose level it uses.** The answer uses the first of these that
  exists:
  1. The session mapping for the calling session, identified by `--session
     <id>` when given and otherwise by `CLAUDE_CODE_SESSION_ID` (the variable
     niwa's dispatch lineage already reads), when it carries
     `authority_level`. Mappings are stored one per session id.
  2. The authority record of the dispatched instance containing the working
     directory. Dispatch writes it before it launches the worker, so a
     session that asks before its mapping exists, or an agent that sets no
     session id, still gets its dispatch's level.
  3. The level a dispatch with no flag would resolve to, with the source
     suffix `(no dispatch record)`, for example
     `source=workspace (no dispatch record)`. This covers sessions niwa didn't
     dispatch and instances already reaped.

  A record that holds a value outside the three levels is a configuration
  error (exit 1), never a fallback.
- **R16. Decision point names.** A decision point name matches
  `^[a-z][a-z0-9-]{0,63}$`. Any matching name is accepted; until an
  organization declaration names its points, every name resolves by R2.
- **R17. A seam for the organization declaration.** The answer to R14 is
  computed from a per-point declaration (a decider and an author-exclusion
  value) when one exists for that point, and from R2 otherwise. A declared
  point reports `source=declaration`. A dispatch, machine or workspace level
  narrower than the declaration narrows each component to the stricter of the
  declared value and the level's R2 value; nothing makes a declared answer
  wider. When that narrowing changes either component, the source is the
  level's source rather than `declaration`. No declaration source exists in any workspace today, so every point
  resolves by R2.
- **R18. Author exclusion is exposed per point.** The author-exclusion value
  in R14's answer belongs to the decision point, follows the level until a
  declaration sets it for that point, and is narrowed the same way as the
  decider under R17.

### Non-functional

- **R19. Nothing changes for workspaces that set nothing.** With no workspace
  key, no machine key and no flag, dispatch launches the worker with the same
  argv and the same `--settings` document as before, except for the
  `niwa.authority.level` entry in `OTEL_RESOURCE_ATTRIBUTES`; the other
  differences are the R10 stderr line and the R11 record fields. Every decision
  point resolves to `session`.
- **R20. Refusals leave nothing behind.** A dispatch refused under R6 or R7
  creates no instance, writes no session mapping and launches nothing.
- **R21. The decision-point question is cheap and offline.** `niwa authority
  decide` reads only local files, opens no network connection, and finishes in
  under one second on a workspace with 100 session mappings on the CI runners
  niwa's tests use.
- **R22. Every agent gets the level.** The resolved level and source are
  recorded in the session mapping for every agent `niwa dispatch` can launch
  (Claude Code and Codex today).

## Acceptance Criteria

Resolution and refusal

- [ ] Workspace `authority_level = "strict"`, no machine key, no flag: the
  session mapping has `authority_level: strict`, `authority_source: workspace`,
  and `niwa list --json` shows both for that instance.
- [ ] Workspace `autonomous`: a dispatch with `--authority supervised` records
  `supervised`/`flag`; a second dispatch with no flag records
  `autonomous`/`workspace`.
- [ ] Workspace `supervised`: `--authority supervised` is accepted and records
  `supervised`/`flag` (tie goes to the flag).
- [ ] Workspace `supervised`: `--authority autonomous` exits 1, the message
  names `autonomous`, `supervised` and `workspace`, and afterwards no new
  instance directory and no new session mapping exist and the fake launcher
  recorded no launch.
- [ ] Machine `strict`, workspace `autonomous`, no flag: records
  `strict`/`machine`. With `--authority supervised`: exits 1 naming `machine`
  as the ceiling's source.
- [ ] Machine `autonomous`, workspace `strict`: records `strict`/`workspace`.
  Machine `strict`, workspace `strict`: records `strict`/`machine`.
- [ ] No workspace key, no machine key, no flag: records
  `autonomous`/`default`.
- [ ] Whitespace: workspace `" strict "` resolves to `strict`; workspace `""`
  and `"   "` resolve as not set (`autonomous`/`default`).
- [ ] Invalid values: workspace `"Strict"` makes `niwa dispatch` and `niwa
  authority show` exit non-zero with a message naming `authority_level`,
  `Strict` and the three accepted names. `--authority bypass` and a machine
  value `"STRICT"` each exit 1 with no instance, no mapping and no launch.
- [ ] `niwa config set authority-level strict` writes `authority_level =
  "strict"` under `[global]` in `$XDG_CONFIG_HOME/niwa/config.toml`; `niwa
  config set authority-level loose` exits non-zero and leaves the file
  unchanged; `niwa config unset authority-level` removes the key.

Read-back

- [ ] Every launching dispatch's stderr contains exactly one line matching
  `^niwa dispatch: authority level (autonomous|supervised|strict) from (flag|machine|workspace|default)$`,
  and a refused dispatch's stderr contains no such line.
- [ ] A Claude dispatch's `OTEL_RESOURCE_ATTRIBUTES`, in both the launch
  environment and the `--settings` document's `env`, contains
  `niwa.authority.level=<resolved level>`; a Codex dispatch's launch
  environment contains no `niwa.authority.level`, and its session mapping
  records the level and source.
- [ ] With no workspace key, no machine key and no flag, a Claude dispatch's
  argv equals the argv recorded by the existing dispatch tests, and its
  `--settings` document equals the pre-feature document except for the added
  `niwa.authority.level` entry.
- [ ] `niwa list --json` omits `authority_level` and `authority_source` for an
  instance whose newest mapping lacks them.
- [ ] `niwa authority show` with workspace `supervised` and machine `strict`
  prints `workspace: supervised`, `machine: strict` and
  `default dispatch: strict from machine`; with neither set it prints
  `workspace: not set`, `machine: not set` and
  `default dispatch: autonomous from default`. Outside a workspace it exits 1.
- [ ] A dispatch at `strict` with `--permission-mode bypassPermissions`
  launches with `--permission-mode bypassPermissions` and records `strict`; a
  dispatch at `strict` with no permission flag and no machine permission mode
  launches with no `--permission-mode`, as before.

The decision-point question

- [ ] With `CLAUDE_CODE_SESSION_ID` set to a session whose mapping records
  `autonomous`/`workspace`, `niwa authority decide merge` prints
  `point=merge`, `level=autonomous`, `decider=session`,
  `author_exclusion=off`, `source=workspace` and exits 0.
- [ ] For a mapping recording `strict`/`flag` it prints `decider=person`,
  `author_exclusion=on`, `source=flag` and exits 3; for `supervised` it prints
  `decider=coordinator`, `author_exclusion=on` and exits 3.
- [ ] `--session <id>` selects the mapping in place of the environment
  variable.
- [ ] With an `autonomous` workspace and a dispatch run with `--authority
  strict`, `niwa authority decide merge` run inside that instance with no
  session id, before any session mapping exists, prints `level=strict` and
  `source=flag` and exits 3.
- [ ] Run from the workspace root with no session id, or with an id that has
  no mapping, in a `strict` workspace, it prints `level=strict` and
  `source=workspace (no dispatch record)` and exits 3.
- [ ] A session mapping or instance record holding `authority_level: bogus`
  makes `niwa authority decide merge` exit 1.
- [ ] A workspace config with a TOML syntax error refuses `niwa dispatch`
  with exit 1 before any instance is created.
- [ ] Point names: `a`, `merge-2` and a 64-character name are accepted;
  `Merge_Now`, `2merge`, `-merge`, an empty name and a 65-character name each
  exit 2. No point argument exits 2. Outside a workspace, or with an invalid
  workspace or machine value, it exits 1.
- [ ] With 100 session mappings in a test workspace, `niwa authority decide
  merge` completes in under one second in CI, and the command's code path
  makes no network call (no network client is constructed).
- [ ] Unit tests on the declaration seam: a declaration of `coordinator`/`off`
  for `merge` at level `autonomous` returns `coordinator`/`off`/`declaration`
  for `merge` and `session`/`off` for `accept`; the same declaration at level
  `strict` (source `flag`) returns `person`/`on`/`flag`; a declaration of `person`/`on` at level
  `autonomous` stays `person`/`on`.

End to end

- [ ] A functional test dispatches a fake agent whose script runs `niwa
  authority decide merge` and writes `settled` to a marker file on exit 0 or
  `waiting-for-person` on exit 3. Dispatched in an `autonomous` workspace the
  marker reads `settled`; dispatched with workspace `strict` it reads
  `waiting-for-person`.
- [ ] A real Claude Code session dispatched by `niwa dispatch`, briefed to
  reach a merge decision by running `niwa authority decide merge` and acting
  on the answer, settles the point itself when dispatched at `autonomous` and
  stops for a person when dispatched with `--authority strict`. The run and
  both answers are recorded in the implementing pull request.

## Out of Scope

- **Declaring strictness per decision point and named deciders.** That's the
  organization's declaration, owned by separate governance work; this feature
  reads the level for every point and leaves the seam R17 describes.
- **Capping a child dispatch at its parent's level.** A session dispatched at
  `strict` that dispatches again gets the workspace and machine ceiling, not
  its parent's level. Never-wider-than-the-parent is separate work that reads
  the record R11 writes.
- **Telling a session up front what it may do.** Nothing here changes the
  prompt or context a worker starts with; a session asks when it reaches a
  decision point.
- **Routing an escalated decision.** R14 names who decides. Getting the
  question to that session or person, and waiting for the answer, is separate
  work.
- **Enforcing author exclusion and locking the strict end.** Refusing an
  author at acceptance or merge, and making the level impossible to widen from
  below, build on R14 and R18.
- **Changing the permission mode.** R9 keeps tool-call approval exactly as the
  machine setting and the flag decide it today.
- **Wiring the toolkit's own skills to the answer.** Making shirabe's decision
  points call `niwa authority decide` is follow-up work in that repository.
- **Recording decisions.** R14 resolves who may decide; recording who did
  decide, and letting a higher authority reverse it, is separate work. A
  session that settles a point does so on the level, and records it however
  that later work lands.

## Known Limitations

- The decision-point answer is advisory. A session can ignore it or edit its
  own instance's files; within one operator's workspace that's no escalation,
  since anything running as the operator already holds their rights. Making
  the strict end hold against the session itself is the later locking work.
- A session's dispatch record and instance record are deleted at reap. After
  reap only the telemetry attribute remains, and a question asked then falls
  back under R15. Telemetry is best-effort and is no audit log.
- A caller that treats only exit 0 as permission is safe under every
  fallback; one that parses the output should read `source` before trusting a
  `(no dispatch record)` answer.
- `coordinator` names a role, not a session. A session dispatched straight
  from a person's own session hands its points to that session, which in
  practice means the person; finding and reaching it is the routing work.
- `CLAUDE_CODE_SESSION_ID` is supplied by the session itself, so a session can
  claim another's id. Within one operator's workspace this gains nothing the
  session couldn't already do.

## Decisions and Trade-offs

- **One level, resolved per decision point, rather than several dials.** A
  survey of how other operations set authority found no operation whose
  settings move as one range, and merge set separately wherever it exists.
  This PRD keeps one operator level anyway, because the per-point dimension
  belongs to the organization's declaration, which is declared nowhere else,
  and the level only supplies its default. Tool-call approval stays a separate
  setting (R9) rather than being folded in. Alternatives: separate keys for
  approval, pacing, finishing and spawning (rejected: it would declare
  per-group strictness the organization declaration owns, and three of the
  four have no niwa consumer today); a single dial that also sets the
  permission mode (rejected: it would collide with the machine setting the
  operator already relies on).
- **Three levels, one per decider.** The three deciders the organization
  declaration will use are an agent within its scope, the dispatching
  coordinator, and a named person or role, so each level maps to one: level
  names describe the stance (`autonomous`, `supervised`, `strict`) and decider
  names describe who (`session`, `coordinator`, `person`). Two levels would
  leave no way to say "my session decides, not me".
- **The machine may narrow but never widen.** On the permission mode the
  machine outranks the workspace both ways, on the reasoning that a cloned
  workspace config shouldn't loosen a machine owner's choice. That reasoning
  carries over in one direction only: a machine owner can make their own
  dispatches stricter, but a team's level about who decides isn't the machine
  owner's to loosen. So the authority level is narrowest-wins across every
  setting, and the permission mode keeps its own order unchanged.
- **Refuse, don't clamp.** A `--authority` wider than the ceiling is refused
  rather than trimmed to the ceiling, so an operator never believes a dispatch
  got authority it didn't.
- **Default `autonomous`.** It's today's behavior, and a workspace with
  nothing declared must never be told to ask an approver.
- **Exit codes for the answer.** Exit 0 for "settle it" and 3 for "hand it
  on" lets a shell step branch without parsing, while the printed fields carry
  the detail. Codes 1 and 2 keep their usual meanings of error and usage
  error.
- **Session identity from the variable lineage already uses.** Dispatch
  lineage reads `CLAUDE_CODE_SESSION_ID` to record a worker's parent, so the
  question reads the same variable to find the caller's record, with
  `--session` for scripts and tests.
- **Discovery reused existing research.** The codebase leads were already
  answered by a survey done before drafting, so no new research agents ran.
