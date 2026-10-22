---
schema: design/v1
status: Proposed
problem: |
  niwa has no way to declare how much a dispatched session may decide or
  which named actions it may take, no place to resolve that at dispatch,
  and no record a session, an operator or a later gate can read back. The
  rules are needed by config loading, dispatch, three read commands and
  `niwa list`; dispatch's side effects start early and the worker's session
  id isn't known until after launch; and readers this unit doesn't build
  need a read contract they can depend on.
decision: |
  A leaf package, `internal/authority`, owns the vocabularies, role
  expansion, narrowest-wins resolution with sources, the record type and
  the decide answer. Config gains an `[authority]` table in the base
  workspace config, a narrowing-only `[authority]` in the overlay and two
  `[global]` machine keys, validated outside config parsing so dispatch
  refuses while `apply` only warns. Dispatch resolves at step (2b), before
  the prompt capture, the sweep and provisioning, and writes one record,
  `<instance>/.niwa/authority.json`, at a new step (8a) before launch.
  Every outside reader uses one command, `niwa authority show --json`
  (plus `decide`), which resolves the instance from the working directory
  or a `-C <dir>` anchor, prints a versioned document, and fails closed;
  the file's path and layout stay private.
rationale: |
  One pure package gives every caller the same rule set. Keeping base,
  overlay and machine separate until resolution is what lets the record
  name which layer narrowed a ceiling. (2b) is the last point before any
  side effect and (8a) the first point the instance exists, with rollback
  already covering it. A command-only read contract keeps every reader on
  niwa's own instance resolution and lets the storage change, and every
  way it can fail reads as "not held", which leaves a later gate exactly
  as strong as today's hook.
upstream: docs/prds/PRD-authority-grants.md
user_visible_surface: true
decision_provenance: inline-resolved
---

# DESIGN: authority-grants

## Status

Proposed

## Context and Problem Statement

`niwa dispatch` launches a worker into a fresh instance and records, after
the fact, who launched it: a session mapping at
`<workspace>/.niwa/sessions/<session-id>.json` carries the dispatch id and
the caller's session id. Nothing records how much the worker may decide or
which named actions it may take, and there's nowhere to declare either. The
PRD (`docs/prds/PRD-authority-grants.md`) asks niwa to add both and to
read them back, without changing what any session can actually do.

What it asks for, in the terms this design answers:

- **Vocabulary.** Three ordered levels, `autonomous`, `supervised` and
  `strict`, each resolving a decision point to one decider (`session`,
  `dispatcher`, `person`) and an author-exclusion value. A closed set of
  three named authorities, `merge-pr`, `close-issue` and `cut-release`,
  each defined by the actions it covers. The level and the grant are
  separate, and an action an authority covers is the session's to take
  only when it holds the authority and the decision resolves to `session`
  (R1 to R4).
- **Declaring policy.** An `[authority]` table in the workspace's base
  config holds the level, a ceiling of grantable authorities (empty when
  absent, so nothing is grantable until a workspace opts in) and roles
  that bundle authorities. An overlay may narrow the level and the ceiling
  but can't declare roles, and the personal config's `[global]
  authority_level` and `authority_ceiling` can only narrow too. Invalid
  values refuse dispatch and the authority commands but only warn
  elsewhere, so `niwa apply` can still pull a fix (R5 to R11).
- **Dispatch.** `--level` narrows, `--grant` and `--role` grant, and a
  request past either ceiling is refused with exit 1, naming what was
  asked for, how, and the ceiling with its source, before the prompt is
  captured or anything is created (R12 to R16).
- **Record and read-back.** One record per dispatch, written into the
  instance before the worker starts, never rewritten, removed with the
  instance. A stderr line at dispatch, an `authority` object in `niwa list
  --json`, `niwa authority show` for any dispatch or for the calling
  session, a no-record answer that holds nothing, a "held" rule that never
  trusts an exit code alone, and two telemetry attributes (R17 to R25).
- **Decision points.** `niwa authority decide <point>` prints the decider
  for a point from the level, with a seam for per-point declarations, and
  a caller settles only on exit 0 plus `decider=session` (R26, R27).
- **Constraints.** No change to enforcement, the permission mode, argv or
  settings documents; no koto; every agent; local, bounded-cost lookups; a
  guide documenting the contract (R28 to R34).

The technical problem has four parts. The rules (what's narrower, what's
inside a ceiling, what a valid record is) are needed by config loading,
dispatch, three read commands and `niwa list`, which sit in packages that
can't import each other freely. Config is read today as one merged
document, but the record has to say which of three layers narrowed a
ceiling. Dispatch has a fixed order of side effects (a terminal prompt at
step (3c), a sweep that deletes other instances at (5), provisioning at
(6), launch at (9)), and the worker's session id isn't known until after
launch for either agent. And the readers this unit doesn't build, a gate
hook, a coordinator's posture script and a never-wider check, need a read
contract now, because they'll be written against it.

This design draws on an earlier unmerged draft of the level half, pull
request #401, and keeps its level semantics, its leaf package and its
pre-launch instance record.

## Decision Drivers

1. **Refusals leave nothing behind.** Every refusal happens before the
   prompt capture, the sweep and provisioning (R16).
2. **One definition of every rule.** Config validation, dispatch, `policy`,
   `show`, `decide` and `niwa list` share one implementation of ordering,
   ceilings, role expansion and record validation.
3. **The record exists before the worker's first command,** for Claude and
   Codex, detached and foreground, and survives resume (R17, R22).
4. **Every failure reads as "not held"** to a reader outside niwa,
   including a niwa that doesn't know the command (R23, R24).
5. **No change to what a session can do.** argv, the permission mode,
   hooks and settings documents are untouched apart from two telemetry
   attributes (R28 to R30).
6. **Bounded, offline lookups** that a hook can afford on gated commands
   (R33).
7. **Follow existing patterns.** The machine keys read and fail like
   `dispatch_permission_mode`; overlay-inert settings warn like the
   `[workspace]` tombstone; the record is written like the dispatch
   marker; telemetry goes through the lineage composer.
8. **No koto, and nothing a later unit must undo.** The record format and
   lookup are what later gate, coordinator and never-wider work consume,
   so they're versioned and documented now.

## Considered Options

### Decision 1: Where the authority rules live

Resolved inline.

Config validation in `internal/config`, dispatch and the three new
commands in `internal/cli`, and the instance listing in `internal/workspace`
all need the same answers: is this a level, is this an authority, which of
two levels is narrower, does this role fit inside this ceiling, is this
record valid, who decides this point. `internal/config` can't import
`internal/cli`, and the listing code needs to validate a record without
pulling in either.

Key assumptions:

- `internal/workspace` can import a package that imports nothing from niwa.

#### Chosen: a leaf package, `internal/authority`

It owns the `Level` and `Authority` vocabularies and their orders, parsing
with the PRD's trimming and case rules, role expansion, the
narrowest-wins resolution with a source for every part, the refusal
errors (which carry what was asked for, how, and the ceiling with its
source, so dispatch can print R13's and R14's messages), the `Record` type
with its JSON encoding and validation, and the `Decide` answer with its
`Declarations` seam. It does no I/O. Every ordering and refusal rule is a
table test with no cobra and no filesystem. Pull request #401 made the
same call for the level half, and the grant extends the same package.

#### Alternatives Considered

**Helpers in `internal/cli`**: everything the commands need sits next to
them. Rejected because config validation can't reach them, so a second
parser would grow in `internal/config` and drift.

**Rules in `internal/config`**: config already parses the values. Rejected
because config would own policy (deciders, role expansion, the record),
and the listing code would import config to validate a record that isn't
config.

**Separate packages for the level and the grant**: smaller units.
Rejected because resolution, the record and the refusal message span
both, and the PRD ties them (R4).

### Decision 2: How outside readers look up a grant, and how a hook anchors the lookup

Delegated to /decision.

Four readers outside dispatch need a dispatch's record: the session
reading its own grant, a later gate hook deciding whether to lift a merge
deny, a later coordinator's posture script predicting that gate, and a
later never-wider check inside `niwa dispatch`. The PRD already says the
record is one copy, written into the instance before launch (R17), that
self-read resolves by the instance containing the working directory
because a Codex worker inherits its dispatcher's session id variable and a
resumed Claude session can get a new id (R21, R22), and that every failure
reads as "not held" (R23, R24). Three things were open: whether a hook can
reach its own instance through the command without trusting input the
model can steer, whether a documented record file would serve jq-only
hooks better, and how the never-wider check finds the parent's record.

Key assumptions:

- Claude Code runs each hook by the absolute path niwa writes into
  settings, so a hook's `$0` names its own copy. Anchoring wrong still
  fails closed, since no record exists outside a dispatched instance.
- A later gate consults the grant only to lift a deny it has already
  decided on, so a missing, old or hung niwa leaves today's verdict.
- niwa's startup stays free of network calls and config loading.
- Codex has no hook niwa gates today, so a Codex grant is readable but
  not enforced anywhere.

#### Chosen: one command contract, anchored by directory

`niwa authority show --json` and `niwa authority decide` are the only
interfaces outside readers use. The guide documents the printed JSON
document (schema `niwa.authority.record/v1` and the fields R17 names), the
selection order, the no-record document (`"record": false`, empty grant)
and the held rule. It doesn't document where the record lives on disk or
its stored bytes, so niwa can move or restructure the file while printing
the same document.

Both commands take `-C <dir>`, meaning "resolve exactly as if run from
`<dir>`", tested to give byte-identical output. A hook anchors on its own
script path, `niwa authority show --json -C "$(dirname "$0")"`, and never
on the payload's `cwd` (the model's), `CLAUDE_PROJECT_DIR` (Claude-only and
the workspace root for ephemeral sessions), `--instance <name>` (needs the
workspace root and names aren't unique) or whatever directory the harness
starts the hook in.

The reads can't hang, write or leak. `show` and `decide` find the instance
with stat-only checks and read the record before they touch workspace
config; they discover the workspace root only for `--session`,
`--instance` or the no-record level, so a held check against an existing
record writes nothing. The record is opened without following symlinks
and without blocking, confirmed a regular file, and read up to a size
cap. A watchdog of about two seconds exits 1 with nothing on stdout. With
`--json`, stdout carries only the document.

The guide ships one reference held check, and niwa's CI runs that exact
check against real output, the no-record document, malformed and
unknown-schema documents, help text, and the errors of a niwa without the
command:

```bash
out=$(niwa authority show --json -C "$dir" 2>/dev/null) || exit 1
printf '%s' "$out" | jq -e --arg a "$authority" '
  .schema == "niwa.authority.record/v1" and .record != false
  and any(.grant[]?; .authority == $a)' >/dev/null
```

For never-wider, the record gains `parent_dispatch_id`, the dispatch id of
the dispatched instance `niwa dispatch` was run from. A later check
selects the dispatcher's record with R21's order applied to the
dispatcher's own working directory, which dispatch already resolves. It
doesn't key on `parent_session_id`, which is empty for every Codex worker
and names the grandparent for a dispatch made inside one.

All four validators in the decision run converged on this option,
including the advocates of the file and dual contracts.

#### Alternatives Considered

**The record file as the documented contract**: hooks walk up from `$0`
and read a documented `<instance>/.niwa/authority.json` with jq, the
command a convenience. Rejected because it pins a path inside `.niwa/` and
the stored bytes as public format, and every reader re-implements instance
discovery and validation, which `show` does more strictly. Its claimed
advantages didn't hold: a FIFO or stalled filesystem blocks jq just as it
blocks niwa, and "works with no niwa" adds nothing when no niwa already
reproduces today's gate.

**Both documented, the command authoritative**: the file answers "is X
held" through one published jq filter. Rejected because it carries the
file contract's costs plus two contracts held in step by a conformance
test, to save about 10 ms of process start.

**A per-authority predicate, `niwa authority holds <authority>`**:
prints a fixed token. Deferred rather than rejected: the published, tested
held check covers the parsing risk now, and adding it later still fails
closed on older niwas. If added, its negative token must not contain
"held".

**Never-wider through `parent_session_id`**: the PRD's earlier wording.
Rejected as the selection key for the Codex reasons above;
`parent_session_id` stays as lineage.

### Decision 3: Declaring authority across base, overlay and machine config

Resolved inline.

The PRD puts roles, the level and the ceiling in the base config (R5 to
R7), lets an overlay narrow the level and ceiling but not declare roles
(R8), lets the machine narrow both (R9), and wants every narrowed value to
carry the layer that narrowed it (R13). Invalid values refuse dispatch and
the authority commands but only warn elsewhere, so `niwa apply` can pull a
fix (R11). Today an overlay's `[workspace]` keys are decoded only to warn
(a tombstone), unknown keys only warn, and `MergeWorkspaceOverlay` folds
the overlay into one config.

Key assumptions:

- The workspace root's `.niwa/instance.json` records the overlay URL
  dispatch should read, as the worktree path already reads it.
- A machine whose state records an overlay but has no local clone of it
  is rare enough that refusing, with a message naming `niwa apply`, is
  acceptable.

#### Chosen: three separate layers, resolved together, validated apart from parsing

`WorkspaceConfig` gains an `[authority]` table: `level`, `ceiling`, and
`roles.<name>.authorities`. `WorkspaceOverlay` gains a narrowing-only
`[authority]` table with `level` and `ceiling`; its `roles` key is
decoded only so every command that loads the overlay warns it has no
effect. `GlobalSettings` gains `authority_level` and `authority_ceiling`
with fail-closed accessors that mirror `DispatchPermissionMode()`.

`MergeWorkspaceOverlay` doesn't touch authority. The resolver takes base,
overlay and machine as three inputs, so the record can say which one
narrowed each ceiling: the level ceiling's source is the layer that
supplied the narrowest value (ties to `machine`, then `overlay`, then
`workspace`), and the grant ceiling's source is `machine` if the machine
list removed anything, else `overlay` if the overlay list did, else
`workspace`.

Values are checked by `authority.ValidateConfig`, called after parsing
rather than inside `config.validate()`. Dispatch and the `niwa authority`
commands refuse on its error with exit 1; `apply` and every other loader
print it as a warning and carry on. Dispatch reads the overlay the
workspace state records (`overlay_url` to `config.OverlayDir` to
`workspace-overlay.toml`). An overlay the state records but that can't be
read or parsed refuses the dispatch instead of silently dropping its
narrowing.

`niwa config set authority-level|authority-ceiling` and `niwa config
unset` follow `config_permission_mode.go`: validate through the same
parser dispatch uses, trim and de-duplicate list entries, accept `none` as
the empty list, and leave the file unchanged on a bad value.

#### Alternatives Considered

**Keys under `[workspace]`** (#401's shape): already inert in an overlay.
Rejected because `[workspace]` holds scalars and has no natural home for
roles, and an overlay couldn't narrow anything.

**Merge authority inside `MergeWorkspaceOverlay`**: one merged value for
everyone. Rejected because the merged value loses which layer narrowed
the ceiling, which the record must name.

**Validate inside `config.validate()`**: one place for all checks.
Rejected because it fails every config load, `apply` included, so a
workspace with a typo couldn't pull its own fix (a concern raised on
#401).

**Treat a missing overlay as no narrowing**: matches the worktree path's
fallback. Rejected because it silently widens policy on exactly the
machine that lacks the overlay.

### Decision 4: Where dispatch resolves, refuses, records and reports

Resolved inline.

`runDispatch` in `internal/cli/dispatch.go` runs numbered steps: the
machine config at (2a), the workspace config at (2b), a terminal prompt
at (3c), a sweep that can delete other instances at (5), provisioning at
(6), the rollback armed at (7), the pending marker at (8), the launch at
(9), session-id capture at (10) and the session mapping at (11). The PRD
wants every refusal before any side effect, including the prompt (R16),
and the record in place before the worker's first command (R17, R22).

Key assumptions:

- Provisioning's config refresh at (6) may change the snapshot after
  resolution; the PRD accepts that as a known limitation.

#### Chosen: resolve at (2b), record at (8a), telemetry through the lineage composer

At (2a), beside `DispatchPermissionMode()`, read the two machine keys; an
invalid value refuses. At (2b), the workspace config load becomes
fail-closed for a file that exists but can't be read or parsed (today it
prints a notice and leaves the failure to provisioning). Right after it,
load the overlay, run `ValidateConfig`, parse `--level`, `--grant` and
`--role`, and call `authority.Resolve`; any error returns before (3). The
same step resolves `parent_dispatch_id`: when the dispatcher's working
directory is inside a dispatched instance (`class.InstanceDir`), it reads
that instance's record and keeps its `dispatch_id`; an unreadable record
there refuses the dispatch rather than recording an empty parent. Print
R19's stderr line once resolution succeeds.

At a new step (8a), right after `writeDispatchMarker`, write the record to
`<instance>/.niwa/authority.json` with mode 0600 through a temp file and
rename. The rollback armed at (7) already removes the instance directory,
record included, on any later failure. Add `niwa.authority.level` and
`niwa.authority.grant` to `lineageInputs`, so `composeDispatchLineage`
emits them beside the existing attributes under its existing validation,
in both the launch environment and the settings document. The grant value
joins authorities with `/` (`merge-pr/close-issue`), which the composer's
value pattern `^[A-Za-z0-9._:/@-]{1,128}$` already admits; a comma would
both fail that pattern and break the comma-separated variable, dropping
the lineage attributes beside it. Nothing from
(9) on reads the resolved authority, so argv, the permission mode and the
settings document are untouched (R28 to R30).

#### Alternatives Considered

**Resolve after provisioning, beside the permission mode at (9a)**: all
dispatch defaults in one place. Rejected because a refusal would need the
rollback and would come after the operator typed a prompt.

**Write the record with the session mapping at (11)**: one write site.
Rejected because (11) runs after launch, and after the whole first turn
for a foreground dispatch.

**Write the record under the workspace root, keyed by dispatch id**:
survives `niwa reset`. Rejected because the root's `.niwa/` is a snapshot
that's replaced on refresh unless the file joins the carried state, and a
record outliving its instance would answer for a session that no longer
exists.

### Decision 5: The no-record level for `decide`

Resolved inline.

With no record, `show` reports an empty grant, but `decide` still has to
print a level. The choice is between the level a no-flag dispatch would
get now (#401's answer) and the strictest level.

#### Chosen: the no-flag level, marked

`decide` with no record answers from the level a dispatch with no flags
would resolve to, with ` (no dispatch record)` appended to `source=`
(R26). With no record the caller is most often the operator's own
session, for which that's the right answer, and the suffix lets a caller
that needs a dispatched session's answer tell the two apart.

#### Alternatives Considered

**`strict` when there's no record**: fails toward a person. Rejected
because it would tell the operator's own session, in an autonomous
workspace, to ask a person before every decision.

## Decision Outcome

niwa gains a small, pure `internal/authority` package and wires it into
three places: config loading, dispatch and a new `niwa authority` command
group. A workspace declares `[authority]` in its base config: a `level`, a
`ceiling` of grantable authorities, and roles. An overlay can lower the
level and shrink the ceiling, the personal config can do the same through
`[global] authority_level` and `authority_ceiling`, and nothing anywhere
can widen what the base config allows. A bad value stops `niwa dispatch`
and the authority commands with a message naming the key, the value and
the accepted values, while `apply` warns and carries on.

At dispatch, step (2b) loads all three layers, parses `--level`,
`--grant` and `--role`, and resolves the level and the grant. A wider
level, an authority outside the ceiling (directly or through a role), an
unknown authority or role, or an unreadable overlay ends the dispatch
with exit 1 before the prompt is captured, before the sweep and before an
instance exists. A successful dispatch prints one line, for example
`niwa dispatch: authority level strict from flag; grant merge-pr
(dispatch)`, then provisions as today and, at step (8a), writes the
record into `<instance>/.niwa/authority.json` before launch. The record
holds the schema name, the dispatch id, the parent session id, the parent
dispatch id, the level and its source, both ceilings and their sources,
the granted roles, and each granted authority with its sources in a fixed
order. It's never rewritten and goes away with the instance. Claude
workers also get two telemetry attributes; nothing else about the launch
changes.

Reading is one command for everyone. A dispatched session runs `niwa
authority show` and gets its own record, found from its working directory
whether it's Claude or Codex, at the root of its instance or in a repo or
worktree inside it, before its mapping exists and after a resume. An
operator runs it with `--session` or `--instance`, or reads `niwa list
--json`, whose `authority` object comes from the same record. A later gate
runs `niwa authority show --json -C "$(dirname "$0")"`, applies the
guide's held check and, per R4, `decide`; every failure, from no niwa to a
malformed record, reads as not held. `niwa authority policy` shows what the
three layers resolve to before anyone dispatches, and `niwa authority
decide <point>` answers who settles a point, `session` at `autonomous` and
`person` at `strict`.

### Rationale

The pieces reinforce each other. Keeping all rules in one package is what
lets config, dispatch, `policy`, `show` and `list` agree on every edge
case, including which layer narrowed a ceiling. Keeping the layers apart
until resolution is what lets the record name its sources, which is what
the operator's "what was this session allowed, and why?" needs. Resolving
before any side effect and writing after the instance exists put both the
refusal and the record exactly where the PRD needs them, with the
existing rollback covering the record. And because the record is read
only through `show`, its storage stays private, every reader shares one
instance resolution, and the held rule is tested once in niwa's CI rather
than re-derived by each consumer. The trade-off accepted is a process
start per lookup, about 10 ms, which a gate pays only on commands its own
text match already flagged.

## Solution Architecture

### Overview

```
.niwa/workspace.toml [authority] ─┐
overlay [authority] (narrow) ─────┼─> authority.Resolve ─┬─> refusal (exit 1, nothing created)
config.toml [global] (narrow) ────┤   (step 2b)          ├─> stderr line
--level / --grant / --role ───────┘                      ├─> lineage: niwa.authority.*
                                                         └─> <instance>/.niwa/authority.json (step 8a)
                                                                   │
   niwa authority show [--json] [-C dir|--session|--instance] ─────┤
   niwa authority decide <point> [-C dir|--session|--instance] ────┤
   niwa list --json (authority object) ────────────────────────────┘
   niwa authority policy [--json]  ── reads the three layers, no record
```

### Components

**`internal/authority` (new, pure).**

```go
type Level int        // Autonomous < Supervised < Strict, widest first
type Authority string // "merge-pr", "close-issue", "cut-release"; Authorities() in R3 order
type Decider int      // Session < Dispatcher < Person

func ParseLevel(raw string) (*Level, error)          // trimmed; nil when blank
func ParseAuthority(raw string) (Authority, error)   // trimmed, exact case

type Layer string // "flag", "machine", "overlay", "workspace", "built-in"

type Policy struct {                 // what the three config layers declare
    BaseLevel, OverlayLevel, MachineLevel *Level
    BaseCeiling []Authority          // nil and empty both mean "nothing grantable"
    OverlayCeiling, MachineCeiling *[]Authority // nil means "doesn't narrow"
    Roles map[string][]Authority
}
func ValidateConfig(p Policy) error  // names key, value, accepted values

type Request struct { Level *Level; Grants []Authority; Roles []string }

type Effective struct {              // what policy resolves to before a request
    LevelCeiling Level; LevelCeilingSource Layer
    GrantCeiling []Authority; GrantCeilingSource Layer
}
func EffectivePolicy(p Policy) Effective

type Source string // "dispatch" or "role:<name>"
type GrantEntry struct { Authority Authority; Sources []Source } // dispatch first, roles by name

type Record struct {
    Schema            string // "niwa.authority.record/v1"
    DispatchID        string
    ParentSessionID   string
    ParentDispatchID  string
    Level             Level;  LevelSource        Layer
    LevelCeiling      Level;  LevelCeilingSource Layer
    GrantCeiling      []Authority; GrantCeilingSource Layer
    Roles             []string
    Grant             []GrantEntry
}
// Resolve refuses with *LevelWiderError, *OutsideCeilingError (every
// offending authority with its route), *UnknownValueError.
func Resolve(p Policy, r Request) (Record, error)
func (r Record) MarshalJSON() ([]byte, error)
func ParseRecord(b []byte) (Record, error) // rejects unknown level/authority, bad schema

type Declared struct{ Decider Decider; AuthorExclusion bool }
type Declarations interface{ Lookup(point string) (Declared, bool) }
var NoDeclarations Declarations
func ValidPoint(name string) bool // ^[a-z][a-z0-9-]{0,63}$
type Answer struct {
    Point string; Level Level; Decider Decider; AuthorExclusion bool
    Source string // a Layer, "declaration", plus " (no dispatch record)" when no record
}
func Decide(point string, level Level, levelSource Layer, noRecord bool, d Declarations) Answer
```

**`internal/config` (changed).** `WorkspaceConfig.Authority` (`level`,
`ceiling`, `roles`); `WorkspaceOverlay.Authority` (`level`, `ceiling`,
and a tombstone `roles` that `TombstoneWarnings` reports);
`GlobalSettings.AuthorityLevel` and `AuthorityCeiling` with accessors
returning parsed values or an error. A helper builds `authority.Policy`
from the three.

**`internal/workspace` (changed).** `authority_record.go`:
`WriteAuthorityRecord(instancePath, authority.Record)` (0600, temp file
and rename) and `ReadAuthorityRecord(instancePath)` (open with
`O_NOFOLLOW|O_NONBLOCK`, `fstat` must be a regular file, read at most
64 KiB, `authority.ParseRecord`). A stat-only `FindInstance(dir)` that
returns the instance containing `dir`, using the same worktree and
`.niwa/instance.json` checks `ClassifyCwd` uses, without config
discovery. `annotateFromSessionMappings`' caller in `niwa list` reads each
instance's record into `InstanceRecord.Authority`, or an `error` field
when it can't.

**`internal/cli` (changed and new).** `dispatch.go`: the `--level`,
`--grant`, `--role` flags, resolution at (2b), the stderr line, the
record at (8a), the lineage inputs. `authority.go`: the `niwa authority`
group with `policy`, `show` and `decide`, the `-C`, `--session`,
`--instance` and `--json` flags, the watchdog, and the exit-code mapping.
`config_authority.go`: `niwa config set/unset authority-level` and
`authority-ceiling`.

### Key Interfaces

**Workspace config.**

```toml
[authority]
level = "supervised"
ceiling = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]
```

**The record document** (what `show --json` prints; the stored file is
private):

```json
{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "level": "strict", "level_source": "flag",
  "level_ceiling": "supervised", "level_ceiling_source": "workspace",
  "grant_ceiling": ["merge-pr", "close-issue"], "grant_ceiling_source": "machine",
  "roles": ["coordinator"],
  "grant": [
    {"authority": "merge-pr", "sources": ["role:coordinator"]},
    {"authority": "close-issue", "sources": ["dispatch", "role:coordinator"]}
  ]
}
```

With no record: `{"schema": "niwa.authority.record/v1", "record": false,
"grant": [], "level": "<no-flag level>", "level_source": "<layer> (no
dispatch record)"}`.

**Commands and exit codes.**

| Command | 0 | 1 | 2 |
|---------|---|---|---|
| `niwa authority policy [--json]` | printed | config error, outside a workspace | usage |
| `niwa authority show [--json] [-C dir \| --session id \| --instance name]` | printed (record or none) | bad or unreadable record, missing instance, config error, outside a workspace, watchdog | usage, `--session` with `--instance` |
| `niwa authority decide <point> [same selectors]` | printed an answer | as `show` | usage, bad point name (checked first) |

A caller settles a point only on exit 0 with `decider=session`, and holds
an authority only by the reference held check. Neither ever trusts exit 0
alone.

**Dispatch flags.** `--level <level>`, `--grant <authority>` (repeatable),
`--role <name>` (repeatable).

### Data Flow

1. `niwa dispatch --role coordinator "<task>"` from inside coordinator
   instance A.
2. (2a) machine keys parsed. (2b) base config loaded fail-closed, overlay
   loaded from the recorded URL, `ValidateConfig`, request parsed,
   `Resolve`; A's record read through `FindInstance(cwd)` for
   `parent_dispatch_id`. Any error: exit 1, nothing created.
3. Stderr line printed. (3c) to (8) unchanged. (8a) record written into
   the new instance B.
4. (9) launch with unchanged argv and settings, plus two lineage
   attributes. (10), (11) unchanged.
5. B's first command, `niwa authority show`, runs `FindInstance(cwd)`,
   reads B's record and prints it.
6. Later, a gate hook in B runs `niwa authority show --json -C
   "$(dirname "$0")"` and the guide's held check; this unit ships the
   check and its CI test, not the gate.

### What later work consumes

- **Gates honouring the grant.** A gate keeps today's text match, and only
  when it has reached a merge, close or release verdict does it run the
  held check (guarded by `command -v niwa`) and `niwa authority decide
  <point>` with the same `-C` anchor; it lifts its verdict only on held
  plus `decider=session`. A missing, old or failing niwa leaves today's
  verdict, so the gate can't end up weaker than the current hook. A
  hook-side time bound is recommended behind niwa's own watchdog.
- **A coordinator's posture read.** The same command with `-C` set to the
  instance root it found, and the same held check, so its prediction
  matches the gate. With no niwa it predicts not held. It should dispatch
  successors from inside its own instance so `parent_dispatch_id` and the
  never-wider check find its record.
- **Never-wider re-delegation.** A check inside `niwa dispatch`, before any
  side effect, selects the dispatcher's record by R21's order applied to
  the dispatcher's working directory, and refuses a child whose level or
  grant is wider. With no dispatcher record it adds no cap. Records link
  child to parent through `parent_dispatch_id`.

## Implementation Approach

### Phase 1: Vocabulary, policy and inspection

`internal/authority` with table tests for parsing, `EffectivePolicy`,
`Resolve` (every source and tie rule, every refusal), `Record` encoding and
`ParseRecord`, and `Decide` with a fake `Declarations`. The three config
layers, `ValidateConfig` wired to refuse in the authority commands and
warn in `apply`, the overlay tombstone warning, `niwa config set/unset
authority-level|authority-ceiling`, and `niwa authority policy`. Includes
the empty-list round trip (machine and overlay `[]` stay empty through
decode and a later `niwa config set`) and `ParseRecord`'s field
validation. No change to dispatch.

Deliverables: `internal/authority/*.go`, `internal/config` changes,
`internal/cli/config_authority.go`, `internal/cli/authority.go` (`policy`).

### Phase 2: Dispatch resolution, refusal and record

The three flags, the fail-closed workspace load and overlay load at (2b),
resolution and refusal before (3c), `parent_dispatch_id`, the stderr line,
the record at (8a), and the lineage attributes. Unit tests with the
existing dispatch fakes; functional scenarios for every refusal (nothing
created, no prompt), the record's contents, a failed record write rolling
back with no launch, the `/`-joined grant attribute surviving beside the
existing lineage attributes, and an unchanged argv and settings document.

Deliverables: `internal/cli/dispatch.go`, `internal/cli/dispatch_lineage.go`,
`internal/workspace/authority_record.go` (write).

### Phase 3: Read-back

`FindInstance`, `ReadAuthorityRecord`, `niwa authority show` and `decide`
with every selector, `-C`, the no-record document, the watchdog and the
safe open (FIFO, symlink, oversized and unreadable fixtures), records
with control characters in string fields, a planted `.niwa` marker in a
repository, empty, relative and missing `-C` values, a mapping whose
`instance_path` points outside the workspace, and the `authority` object
in `niwa list --json`. Functional tests: a fake agent
reading its own record as its first command (Claude and Codex, detached and
foreground, root, repo and worktree, with an inherited session id), the
decide-driven marker test at both ends, and the bounded-read and
no-network tests.

Deliverables: `internal/workspace/authority_record.go` (read, find),
`internal/cli/authority.go` (`show`, `decide`), `internal/cli/list.go`.

### Phase 4: Guide and contract tests

`docs/guides/authority-grants.md` with the keys, flags, authority
coverage, the R8a example, the JSON document and schema, the selection
order and `-C`, the no-record answer, the held rule and the reference
check, the settle rule, R4's combination, and what the record does and
doesn't guarantee, including that a gate only ever lifts a verdict on a
held authority and never denies on "not held". A CI test runs the guide's
held check and its exact-line `decider=session` check against real `show`
and `decide` output and against each failure. The one-time live check with a
real Claude Code session is recorded in the implementing pull request.

Deliverables: `docs/guides/authority-grants.md`, the contract test, and
the CLAUDE.md contributor-guides entry.

Phases 2 and 3 depend on Phase 1; Phase 3's functional tests need Phase
2's record; Phase 4 needs all three.

## Security Considerations

This feature declares and records authority; it doesn't enforce it. A
dispatched session runs as the operator and can read and edit every file
the operator can, including its own instance's authority record. Nothing
in this design stops it from doing so, and nothing here claims to. Making
the record tamper-proof, scoping credentials and wrapping commands are
separate, later work. What this design does promise is that it makes
nothing weaker than today, that a later gate reading the record can't be
led into failing open by this unit's behaviour, and that no input reaches
a path, a shell or a terminal unsafely.

**Inputs and validation.** Every authority value is checked against a
closed set after trimming, with exact lowercase matching: three levels,
three authorities, and role and decision point names matching
`^[a-z][a-z0-9-]{0,63}$`. No flag or config value builds a path or reaches
a shell. Messages that quote a bad value print it quoted and escaped, so a
hand-edited value with control bytes can't paint the terminal. A workspace
config that exists but can't be read, an overlay the workspace state
records but that's missing or unparseable, and an invalid machine value
all refuse the dispatch rather than fall back, because a silent fallback
would widen policy on the machine that has the problem.

**Empty lists are real limits.** `authority_ceiling = []` and an overlay
`ceiling = []` mean nothing is grantable, not "unset". The machine field
is stored so that an empty list survives decoding and every later rewrite
of the personal config (no `omitempty` on it), and the overlay field
decodes `[]` to a present, empty list. A round-trip test sets `none`, runs
an unrelated `niwa config set`, and checks the ceiling is still empty.

**Writing the record.** The record is written at (8a) through a temp file
created with `O_EXCL|O_NOFOLLOW` in the new instance's `.niwa/`, synced and
renamed, with mode 0600; it is not written with the dispatch marker's
plain writer. A failed write, or a record that would exceed the reader's
64 KiB cap, fails the dispatch and triggers the rollback, so no worker is
ever launched without its record. The record holds no secret, but the
design doesn't claim it's private from other local users: the instance
directory keeps the mode provisioning gives it.

**Reading the record.** `show` and `decide` arm a watchdog of about two
seconds before their first filesystem call, open the record without
following a final symlink and without blocking, require a regular file,
read at most 64 KiB, and print only after a complete, successful parse,
so a timeout or error leaves stdout empty. `ParseRecord` validates every
string field, not only the vocabularies: `dispatch_id` and
`parent_dispatch_id` are UUIDs or empty, `parent_session_id` passes the
existing session-id check or is empty, each role matches the role
pattern, each source is `dispatch` or `role:<valid name>`, and the grant
is in R3's order. A hand-edited record therefore can't put control
characters in front of an operator running `niwa list` or `show`. Error
text and `niwa list`'s per-instance `error` name a failure class and never
echo file contents or the record path. `--instance` names go through the
existing name validation and instance enumeration before they're joined
to a path, and the `instance_path` a session mapping holds is accepted for
`--session` only if it's an instance directly under the workspace root.
Neither command writes anything, makes a network call or starts a
process.

**Anchoring a hook.** Cloned repositories are untrusted content, and a
directory that carries its own `.niwa` marker could be mistaken for an
instance. `FindInstance` therefore accepts only an instance that sits
directly under a workspace root (or a niwa worktree of one), and
discovers the workspace root from that instance, not from the nearest
`workspace.toml`. An empty, relative or nonexistent `-C` argument is an
error, never a fallback to the working directory. The guide tells hook
authors to anchor on the instance's own hook copy, to check that `$0` is
absolute, and to pass the anchor after `-C` as an absolute path.

**Failing toward today's behaviour.** Every failure reads as "not held": a
missing or older niwa, a missing `jq`, a non-zero exit, help text, an
unknown schema, a malformed or unreadable record, or the watchdog. The
guide states that a gate may use the lookup only to lift a verdict it has
already reached on its own text match, and only on a held authority plus
an exact `decider=session` line; it must never treat "not held" as a
reason to deny. The guide ships exact-match checks for both, and niwa's CI
runs them against real and failing output. With no record, `decide`
answers from the level a no-flag dispatch would get and marks the source,
because the usual caller then is the operator's own session.

**Supply chain and dependencies.** No new Go module is added; the reader
uses the standard library. `jq` is a test-time tool for niwa and a
runtime tool only for hook authors, and a missing `jq` reads as not held.
The overlay is a git clone of the organization's overlay repository. It
can narrow the level and ceiling and can't declare roles, so a compromised
overlay can at worst refuse dispatches or reduce grants. Its `roles`
tombstone and unknown keys are only warned about, never evaluated.

**Data exposure.** The record holds the dispatch id, the parent session
and dispatch ids, the level, the ceilings, the roles and the grants with
their sources: no credentials, prompt text or paths. Dispatch's stderr
line uses the same closed vocabulary. The two telemetry attributes go to
the worker's configured collector for agents that receive lineage
attributes and reveal which authorities a session holds; they're
best-effort and aren't the record.

**Residual risks accepted.** A session can edit its own record, the
workspace state that names the overlay, and its parent linkage (it can
dispatch from outside its instance and so record no parent), so later
never-wider work must not treat `parent_dispatch_id` as a security
boundary. A session can pass another session's id to `--session`. A
session that corrupts its own record blocks its own child dispatches. A
record states what was resolved at dispatch and doesn't follow later
policy changes; provisioning can refresh config after resolution; a stale
overlay clone can carry an older narrowing than its remote; and an older
niwa that rewrites the personal config drops the machine keys, which
`niwa authority policy` then shows as `not set`. All of these sit inside
the boundary the feature states: until a separate effort hardens
enforcement, anything running as the operator holds the operator's rights.

## Consequences

### Positive

- An operator can declare, narrow, grant and read back authority, and a
  successor coordinator can be handed merge, close and release with no
  config edit between sessions.
- Later gate, coordinator and never-wider work build on one package, one
  versioned document and one tested held check, and none of them needs a
  PRD change to handle an old or missing niwa.
- Workspaces that set nothing see one stderr line and two telemetry
  attributes, and every session can do exactly what it could before.

### Negative

- A grant changes nothing until a gate reads it, so for now it's a
  declaration with no effect beyond being readable.
- A workspace config that exists but can't be parsed now stops `niwa
  dispatch` at (2b) instead of at provisioning, and a machine that lacks
  a recorded overlay's clone can't dispatch until it applies.
- Every outside lookup starts a niwa process, about 10 ms.
- A child dispatch isn't capped at its parent yet, so until never-wider
  lands a session granted nothing can dispatch a child granted anything
  inside the ceiling.
- The record is writable by the session it describes.

### Mitigations

- The guide states plainly that the grant isn't enforced yet, and the PRD
  names the gate work that will read it.
- Both new refusals name the file or overlay and the fix (`niwa apply`).
- Gates call the lookup only on commands their own text match flagged.
- The record carries `parent_dispatch_id` now, so never-wider can be built
  without a format change.
- Enforcement stays at today's strength by the owner's ruling; making the
  record tamper-proof is the separate later effort the PRD names.
