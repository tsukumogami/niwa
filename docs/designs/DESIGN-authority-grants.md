---
schema: design/v1
status: Planned
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
  refuses while `apply` only warns. Dispatch resolves at a new step (2e), before
  the prompt capture, the sweep and provisioning, and writes one record,
  `<instance>/.niwa/authority.json`, at a new step (8a) before launch.
  Every outside reader uses one command, `niwa authority show --json`
  (plus `decide`), which resolves the instance from the working directory
  or a `-C <dir>` anchor, prints a versioned document, and fails closed;
  the file's path and layout stay private.
rationale: |
  One pure package gives every caller the same rule set. Keeping base,
  overlay and machine separate until resolution is what lets the record
  name which layer narrowed a ceiling. (2e) is the last point before any
  side effect and (8a) the first point the instance exists, with rollback
  already covering it. A command-only read contract keeps every reader on
  niwa's own instance resolution and lets the storage change, and every
  way it can fail reads as "not held" and as "no record", which leaves a
  later gate exactly as strong as today's hook.
upstream: docs/prds/PRD-authority-grants.md
user_visible_surface: true
decision_provenance: inline-resolved
---

# DESIGN: authority-grants

## Status

Planned

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

1. **Refusals leave nothing behind.** Every refusal at resolution
   happens before the prompt capture, the sweep and provisioning (R16).
   The one later refusal, R16a's re-check against what provisioning
   actually built, rolls back the instance it created.
2. **One definition of every rule.** Config validation, dispatch, `policy`,
   `show`, `decide` and `niwa list` share one implementation of ordering,
   ceilings, role expansion and record validation.
3. **The record exists before the worker's first command,** for Claude and
   Codex, detached and foreground, and survives resume (R17, R22).
4. **Every failure reads as "not held" and as "no record"** to a reader outside niwa,
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
reading its own grant, a later gate hook deciding whether to allow a
merge, a later coordinator's posture script predicting that gate, and a
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
  fails closed: a directory in no instance gives the no-record answer
  (the lookup never reads the session id variable), and no record exists
  outside a dispatched instance.
- A later gate lets the grant decide only when the lookup positively
  reports a record (`"record": true`), so a missing, old or hung niwa,
  and a session with no record, leave today's verdict.
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

Both commands take `-C <dir>`, meaning "find the instance from `<dir>`
instead of the working directory", tested to give byte-identical output to
running from `<dir>` inside an instance; a `<dir>` in no instance gives
the no-record answer, exactly as a bare run outside an instance does.
Neither ever reads the session id variable: a worker can inherit its
dispatcher's (a Codex worker does), so a session that left its instance,
or one at the workspace root, would otherwise read its dispatcher's wider
record as its own. A hook anchors on its own
script path, `niwa authority show --json -C "$(dirname "$0")"`, and never
on the payload's `cwd` (the model's), `CLAUDE_PROJECT_DIR` (Claude-only and
the workspace root for ephemeral sessions), `--instance <name>` (needs the
workspace root and names aren't unique) or whatever directory the harness
starts the hook in.

The reads can't hang, write or leak. `show` and `decide` find the instance
with stat-only checks (which locate the workspace root and its instance
list without loading any config) and read the record before they touch
workspace config; they load and validate the authority config only for
the no-record level (PRD R11). A held check against an
existing record therefore performs only reads, and a config typo added
after a dispatch doesn't hide that dispatch's record. The record is opened without following symlinks
and without blocking, confirmed a regular file, and read up to a size
cap. A watchdog of about two seconds exits 1 with nothing on stdout. With
`--json`, stdout carries only the document.

The guide ships one reference held check, and niwa's CI runs that exact
check against real output, the no-record document, malformed and
unknown-schema documents, help text, and the errors of a niwa without the
command:

```bash
# niwa_holds <absolute-dir> <authority>: returns 0 only when held.
niwa_holds() {
  local out
  out=$(niwa authority show --json -C "$1" 2>/dev/null) || return 1
  printf '%s' "$out" | jq -e --arg a "$2" '
    .schema == "niwa.authority.record/v1" and .record == true
    and any(.grant[]?; .authority == $a)' >/dev/null 2>&1
}
```

Beside it the guide ships `niwa_grant_decides <absolute-dir>`, which
runs the same lookup and returns 0 only for a document with that schema,
`.record == true` and a `grant_ceiling_source` other than `"none"`. A
gate uses it to tell "a record from a workspace that opted in, which
doesn't hold this" (the grant decides, so deny) from every failure, the
no-record answer and a workspace that never declared a ceiling (today's
behaviour). Both are functions that return,
never ones that exit: pasted into a gate hook, an `exit 1` is a
non-blocking hook error that would drop a deny the gate had already
reached. The CI test runs them inside a stub gate and checks that every
failure case leaves the stub's today's-behaviour verdict in place.

For never-wider, the record gains `parent_dispatch_id`, the dispatch id of
the dispatched instance `niwa dispatch` was run from, or the one
`--parent-from` names, and `parent_dispatch_source` saying which. Dispatch
finds the dispatcher's record through `FindInstance` on that anchor, else
its own working directory, at step (2e), with no session-variable
fallback, and that one read is
both where `parent_dispatch_id` comes from and where a later never-wider
check will compare. It doesn't key on `parent_session_id`, which dispatch
reads from a variable a worker can inherit from its own dispatcher: a
Codex worker carries its dispatcher's value, so a dispatch made inside one
would name the grandparent.

The file and dual-contract alternatives below were each argued for and
rejected on the evidence stated with them.

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

- Provisioning picks an overlay through three branches
  (`internal/workspace/apply.go`): none when the state says `NoOverlay`,
  the URL the state records, or a convention overlay derived from the
  config source's URL, which provisioning may clone during the dispatch.
  Only the instance's own state records which one it applied.
- A machine whose state records an overlay but has no local clone of it
  is rare enough that refusing, with a message naming `niwa apply`, is
  acceptable.

#### Chosen: three separate layers, resolved together, validated apart from parsing

`WorkspaceConfig` gains an `[authority]` table: `level`, `ceiling`, and
`roles.<name>.authorities`. `WorkspaceOverlay` gains a narrowing-only
`[authority]` table with `level` and `ceiling`; its `roles` key is
decoded only so every command that loads the overlay warns it has no
effect. `GlobalSettings` gains `authority_level` and `authority_ceiling`
with fail-closed accessors that mirror `DispatchPermissionMode()`. All of
these values are decoded loosely (as untyped TOML values), so a wrong
type such as `ceiling = "merge-pr"` reaches validation as a bad value
instead of failing the whole config decode, which would stop `apply`.
The overlay and global parsers move from `toml.Unmarshal` to
`toml.Decode` so their undecoded keys are visible; unknown keys under
`authority.` and `global.authority_` go into the policy's per-layer
unknown-key lists (and are dropped from the generic "unknown config
field" warning so they're reported once).

`MergeWorkspaceOverlay` doesn't touch authority. The resolver takes base,
overlay and machine as three inputs, so the record can say which one
narrowed each ceiling: the level ceiling's source is the layer that
supplied the narrowest value (ties to `machine`, then `overlay`, then
`workspace`), and the grant ceiling's source is `none` when no layer
declares a ceiling at all, else `machine` if the machine list removed
anything, else `overlay` if the overlay list did, else `workspace` when
the base declares one, else the narrowing layer that declares one
(`machine` first). `none` lets a later gate tell a workspace that never
opted in from one that declared an empty ceiling.

Values are checked by `authority.ValidateConfig`, called after parsing
rather than inside `config.validate()`. Dispatch and `niwa authority
policy` refuse on its error with exit 1, as do `show` and `decide` when
they need the config for a no-record answer; `apply` and every other
loader print it as a warning and carry on.

One overlay resolver, `workspace.ResolveAuthorityOverlay(stateDir,
configSourceURL)`, mirrors provisioning's three branches and is shared by
dispatch, `policy` and the no-record level: `NoOverlay` in the given
state's `.niwa/instance.json` means no overlay; else the `overlay_url` it
records, whose clone must exist and parse or the caller refuses; else the
convention URL derived from the registered config source, looked up by
the same effective workspace name provisioning uses
(`resolveEffectiveWorkspaceName`, then `LookupWorkspace`), used when its
clone exists locally and treated as absent when it doesn't. At (2e) the
state is the workspace root's. Treating an uncloned convention overlay
as absent would be the silent widening rejected below if nothing caught
it, so dispatch re-checks at (8a) (PRD R16a): it calls the resolver with
the new instance's own state, whose convention branch now finds a clone
provisioning just made; only when that state records no overlay URL and
the convention branch finds none either (provisioning couldn't read the
overlay's commit) does it fall back to the overlay (2e) used. It
re-reads the config provisioning may have refreshed and resolves the
same request again. If that refuses, or the level (narrower or wider) or
the grant differs from (2e)'s, the dispatch refuses and the armed
rollback removes the instance; otherwise the record takes the second
resolution's two ceilings and their sources, and keeps (2e)'s level,
level source and grant, which R19's line printed. So the record never claims a wider
ceiling than the instance was built from. `policy` and the no-record
level have no such re-check and can show a wider policy than a dispatch
ends up with when a convention overlay isn't cloned yet.

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
machine config at (2a); the agent-harness resolution at (2b), which also
loads the workspace config but tolerates a load failure; the agent checks
at (2c) and (2d); the binary preflight at (3); a terminal prompt at (3c);
the dispatch id minted at (4); a sweep that can delete other instances at
(5); provisioning at (6); the rollback armed at (7); the pending marker at
(8); the launch at (9); session-id capture at (10) and the session
mapping at (11). The PRD wants every refusal before any side effect,
including the prompt (R16), and the record in place before the worker's
first command (R17, R22).

Key assumptions:

- Provisioning at (6) may refresh the base config or clone a convention
  overlay after (2e) resolves; the (8a) re-check below catches a change
  that arrives that way (a level change in either direction or a changed
  grant refuses; a ceiling-only change is recorded).

#### Chosen: resolve at a new step (2e), record at (8a), telemetry through the lineage composer

At (2a), beside `DispatchPermissionMode()`, read the two machine keys; an
invalid value refuses. Agent resolution at (2b) keeps its tolerant
workspace-config load, so its own behaviour doesn't change. A new step
(2e), after (2d) and before the preflight at (3), refuses when the
workspace config load at (2b) failed for any reason, a missing
`workspace.toml` included (dispatch already requires a workspace root,
so this case means the file vanished or can't be read), then resolves the
overlay through `ResolveAuthorityOverlay`, runs `ValidateConfig`, parses
`--level`, `--grant` and `--role`, and calls `authority.Resolve`; any
error returns before (3), so before the prompt, the sweep and
provisioning. The same step reads the dispatcher's own record: it calls
`FindInstance` on `--parent-from <abs dir>` when given, else on the
dispatcher's working directory (never the session id variable), and,
when that's a dispatched instance with a record, reads it through the
same reader `show` uses and keeps the whole record, so the later
never-wider check can be added at this step with no second read; this
unit uses only its `dispatch_id`, as `parent_dispatch_id`, and records
how it was found as `parent_dispatch_source` (`anchor`, `cwd` or `none`).
`--parent-from` takes the same checks as `show -C`: an empty, relative or
nonexistent path exits 1 before anything is created. It's stricter than
`-C` in three ways, since the dispatcher passes it on purpose: a path in
no instance, an instance under a workspace root other than the
dispatch's (`class.WorkspaceRoot`, compared after resolving symlinks on
both sides, since `ClassifyCwd` leaves them and `FindInstance` resolves
them), and, when the working directory is
already in a dispatched instance, a different instance, each exit 1. So
`anchor` and `cwd` never disagree. It exists because a
coordinator that runs `niwa dispatch` from the workspace root, as
shirabe's does today, would otherwise record no parent. An instance with
no record gives an empty `parent_dispatch_id` and `none`; a record that
exists but can't be read or is malformed refuses the dispatch rather than
recording an empty parent. A record with a later schema version is not
"no record": dispatch takes its version-independent `dispatch_id` as
`parent_dispatch_id` with the usual `cwd` or `anchor` source and
proceeds, and refuses if even that doesn't parse, so an older niwa never
records an empty parent for an instance a newer niwa dispatched.

`Resolve` returns the record's policy fields (level, ceilings, sources,
roles, grant). Dispatch stamps the identity fields at (8a): the
`dispatch_id` it mints at (4), after the prompt, and the
`parent_session_id` and `parent_dispatch_id` it already holds. `Resolve`
checks the record's encoded size with those fields at their maximum
lengths against the reader's 64 KiB cap, a constant in
`internal/authority` that the reader uses too. R19's stderr line prints
after (4) and before the sweep at (5), so a dispatch that fails its
binary preflight or prompt capture never prints a line; one that prints
it and then fails to provision or launch has rolled back, and the line
describes a dispatch that didn't launch.

At a new step (8a), right after `writeDispatchMarker`, dispatch first
re-checks the policy against what the new instance was built from, as
Decision 3 describes: it resolves the same request against the
instance's applied overlay and the config provisioning may have
refreshed, refuses (and the armed rollback removes the instance) if that
refuses or changes the level or grant, and otherwise takes that
resolution's ceilings and their sources while keeping (2e)'s level, level
source and grant. Then it stamps the identity fields and
writes the record to
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

The two resolution orders sit side by side and neither reads the other.
The permission mode keeps its order unchanged: `--permission-mode`, then
`[global] dispatch_permission_mode`, then a `bypass` workspace posture,
with the machine outranking the workspace in both directions, resolved
at the (9a-derive) step as today. Authority resolves at (2e) narrowest-wins across
workspace, overlay, machine and flag, where the machine can only narrow.
A `--level strict` dispatch with `--permission-mode bypassPermissions`
launches in bypass mode and records `strict`; a `bypass` posture implies
no level and no grant (R29).

#### Alternatives Considered

**Resolve after provisioning, beside the permission mode at (9a-derive)**: all
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

### Decision 6: An authority name this niwa doesn't know, in config

Resolved inline.

The record already reads an unknown authority as present but not held
(R17a). The workspace config is shared the same way records are: once a
newer niwa adds an authority (`teardown`, say) and the shared base
config's ceiling or a role lists it, every teammate on an older niwa
reads that config. The question is what the older niwa does with the
name.

#### Chosen: ignore it with a warning, and refuse it only on the flag

An authority name that matches the name pattern but isn't one of R3's
is dropped from the ceiling or role that lists it, with a warning naming
the name and the key, printed by `niwa apply`, `niwa dispatch` and `niwa
authority policy`. Dropping it grants nothing: a ceiling entry this niwa
can't grant can't widen anything, a narrowing ceiling (overlay or
machine) that lists only unknown names is a present, empty list rather
than an unset one, so it still narrows to nothing, and a role keeps its
known authorities. Only the dispatcher's own request is refused: an
unknown name on `--grant`, or in `niwa config set authority-ceiling`,
exits 1 as today, because that's a person typing on this niwa. A name
that fails the pattern (uppercase, spaces, a misspelt key) is still an
invalid value, and an unknown level still refuses, since a level this
niwa can't order can't be narrowed safely.

#### Alternatives Considered

**Refuse any unknown name, as before**: keeps every typo loud. Rejected
because it makes a config change that is correct for a newer niwa stop
every older niwa from dispatching at all, which is the failure the
record's additive rule already avoids. A misspelt known name in a
ceiling only narrows, and the warning names it.

**Ignore it silently**: no noise for teammates who haven't upgraded.
Rejected because a misspelt `merge-pr` in a role would then grant less
than the operator meant with nothing saying why.

## Decision Outcome

niwa gains a small, pure `internal/authority` package and wires it into
three places: config loading, dispatch and a new `niwa authority` command
group. A workspace declares `[authority]` in its base config: a `level`, a
`ceiling` of grantable authorities, and roles. An overlay can lower the
level and shrink the ceiling, the personal config can do the same through
`[global] authority_level` and `authority_ceiling`, and nothing anywhere
can widen what the base config allows. A bad value stops `niwa dispatch`
and the authority commands with a message naming the key, the value and
the accepted values, while `apply` warns and carries on. An authority
name this niwa doesn't know, in a ceiling or a role, is only warned
about and grants nothing (Decision 6), so a config written for a newer
niwa doesn't stop an older one.

At dispatch, step (2e) loads all three layers, parses `--level`,
`--grant` and `--role`, and resolves the level and the grant. A wider
level, an authority outside the ceiling (directly or through a role), an
unknown authority on `--grant`, an unknown role, or an unreadable
overlay ends the dispatch
with exit 1 before the prompt is captured, before the sweep and before an
instance exists. A successful dispatch prints one line, for example
`niwa dispatch: authority level strict from flag; grant merge-pr
(dispatch)`, then provisions as today and, at step (8a), re-checks the
request against the config and overlay the instance was actually built
from (refusing and rolling back only if that refuses or changes the level
or grant) and writes the record into `<instance>/.niwa/authority.json`
before launch. The record
holds the schema name, the dispatch id, the parent session id, the parent
dispatch id and how the parent was found (`parent_dispatch_source`), the
level and its source, both ceilings and their sources, the granted
roles, and each granted authority with its sources in a fixed order; a
reader lists entries it can't hold under `unknown_grant`. It's never
rewritten and goes away with the instance. Claude
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
config.toml [global] (narrow) ────┤   (step 2e)          ├─> stderr line
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
// ParseNameList splits comma lists for --grant, --role and `config set
// authority-ceiling`, trims entries and refuses an empty one; only
// `config set` gives "none" a meaning (the empty list).
func ParseNameList(raw []string) ([]string, error)

type Layer string // "flag", "machine", "overlay", "workspace", "built-in",
                   // and "none" for a grant ceiling no layer declared;
                   // a reader also uses "newer-version" (never written)

type RawLayer struct {               // one layer as decoded, values untyped
    Level       any                  // nil when absent
    Ceiling     any                  // nil when absent; a list when set
    Roles       map[string]any       // base layer only
    UnknownKeys []string             // undecoded keys under [authority] / authority_*
}
// ValidateConfig checks the three raw layers (types, vocabularies, role
// names, unknown keys) and returns the typed Policy, with warnings for
// unknown authority names it dropped from ceilings and roles (Decision
// 6), or an error naming the key, the quoted value and the accepted
// values.
func ValidateConfig(base, overlay, machine RawLayer) (Policy, []string, error)

type Policy struct {                 // what the three config layers declare
    BaseLevel, OverlayLevel, MachineLevel *Level
    BaseCeiling []Authority          // nil and empty both mean "nothing grantable"
    OverlayCeiling, MachineCeiling *[]Authority // nil means "doesn't narrow"
    Roles map[string][]Authority
}

const MaxRecordBytes = 64 << 10      // shared by Resolve's size check and the reader

type Request struct { Level *Level; Grants []Authority; Roles []string }

type Effective struct {              // what policy resolves to before a request
    LevelCeiling Level; LevelCeilingSource Layer
    GrantCeiling []Authority; GrantCeilingSource Layer
}
func EffectivePolicy(p Policy) Effective

type Source string // "dispatch" or "role:<name>"
type GrantEntry struct { Authority Authority; Sources []Source } // dispatch first, roles by name

type Record struct {
    Schema               string // "niwa.authority.record/v1"
    Present              bool   // encoded as "record"; true for every written record
    DispatchID           string
    ParentSessionID      string
    ParentDispatchID     string
    ParentDispatchSource string // "anchor", "cwd" or "none"
    Level             Level;  LevelSource        Layer
    LevelCeiling      Level;  LevelCeilingSource Layer
    GrantCeiling      []Authority; GrantCeilingSource Layer
    Roles             []string // name order
    Grant             []GrantEntry
    UnknownGrant      []RawGrantEntry // read-side only: entries this build can't hold
    NewerVersion      string // read-side only: a later stored schema, read as holding nothing
}
type RawGrantEntry struct {           // as stored, names unchecked against the vocabulary
    Authority string; Sources []string
}
// Resolve fills the policy fields (identity fields are stamped by
// dispatch at (8a)) and refuses with *LevelWiderError,
// *OutsideCeilingError (every offending authority with its route),
// *UnknownValueError, or *RecordTooLargeError.
func Resolve(p Policy, r Request) (Record, error)
// MarshalJSON writes the one document the file stores and `show --json`
// prints, `record: true` included; a no-record answer is built
// separately with `record: false`.
func (r Record) MarshalJSON() ([]byte, error)
// ParseRecord is additive within v1: an unknown field is ignored; a
// grant entry whose authority this build doesn't know, or whose sources
// are all of unknown kinds, is kept out of Grant (so it is never held)
// and listed in UnknownGrant as stored; an entry with at least one known
// source stays in Grant with its known sources; an unknown name in
// grant_ceiling is dropped and listed nowhere; an unknown
// parent_dispatch_source reads as "none" with an empty
// ParentDispatchID. Every field R17 lists is required; only fields
// added within v1 later are optional. Every name must still match
// its pattern. A schema of niwa.authority.record/v<N>, N >= 2, returns
// a Record with NewerVersion set, DispatchID read, and every other field
// at its narrowest: Level and LevelCeiling Strict, every source
// "newer-version", empty parents (ParentDispatchSource "none"), and
// empty GrantCeiling, Roles, Grant and UnknownGrant. schema and
// dispatch_id are fixed across every version. A malformed record still fails: not a JSON object, a
// schema matching no version, a later version without a UUID
// dispatch_id, a v1 record with a missing or wrongly typed known field,
// an unknown level, a name failing the pattern, or a bad id.
func ParseRecord(b []byte) (Record, error)

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
and a tombstone `roles`); `GlobalSettings.AuthorityLevel` and
`AuthorityCeiling`, all decoded as untyped values and turned into
`authority.RawLayer`s with their undecoded keys (the overlay and global
parsers move to `toml.Decode` for that, and the base parser's generic
"unknown config field" warning skips `authority.*` keys).
`authority_ceiling` is stored without `omitempty` so an empty list
survives a rewrite, and the config writer keeps using the TOML encoder.
`config.AuthorityWarnings(cfg, overlay, global)` returns the authority
validation errors, the warning for an overlay or machine ceiling naming
an authority the base ceiling lacks, and the overlay `roles` tombstone
as strings (the
tombstone is reported here only, not by `TombstoneWarnings`); the apply
pipeline calls it whether or not an overlay is present and prints its
lines with its other deferred warnings, so every loader that isn't
dispatch or an authority command warns from one place.

**`internal/workspace` (changed).** `authority_record.go`:
`WriteAuthorityRecord(instancePath, authority.Record)` (0600, temp file
and rename) and `ReadAuthorityRecord(instancePath)` (open with
`O_NOFOLLOW|O_NONBLOCK`, `fstat` must be a regular file, read at most
`authority.MaxRecordBytes`, `authority.ParseRecord`), returning a
distinct "no record" result for a missing file. `FindInstance(dir)`
returns the outermost valid instance containing `dir`, using the same
marker checks `ClassifyCwd` uses but resolving symlinks first and
requiring the candidate to be an instance the workspace root enumerates;
`ClassifyCwd` itself is unchanged, so `destroy`, `apply` and dispatch see
the paths they see today. `ResolveAuthorityOverlay(stateDir, configSourceURL)`
mirrors provisioning's overlay branches (Decision 3). The session-id and
dispatch-id patterns that `internal/cli` holds today
(`parentSessionPattern`, `dispatchIDPattern`) move into
`internal/authority`, whose package doc says it also owns these id
formats, and the lineage code, `niwa list` and `ParseRecord` all use that
one copy. `niwa list` reads each instance's record into
`InstanceRecord.Authority`, or an `error` field naming the failure class
when it can't.

**`internal/cli` (changed and new).** `dispatch.go`: the `--level`,
`--grant`, `--role` flags, resolution at (2e), the stderr line after
(4), the record at (8a), the lineage inputs. `authority.go`: the `niwa
authority` group with `policy`, `show` and `decide`; one
`selectDispatch` function, used by `show` and `decide`, that applies
R21's order (`--session`, `--instance`, then `-C` or the working
directory, and never the session id variable) and returns an instance
path or "no record"; the `-C`, `--session`, `--instance` and `--json` flags; the
watchdog; and the exit-code mapping. Never-wider doesn't use
`selectDispatch`: it reads the dispatcher's record at (2e) from
`--parent-from` or the working directory alone. `dispatch.go` also
gains `--parent-from`, checked like `-C`.
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
private). This build always writes `"unknown_grant": []`; the example
shows what a niwa that doesn't know `teardown` prints when it reads a
record a newer niwa wrote:

```json
{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "parent_dispatch_source": "none",
  "level": "strict", "level_source": "flag",
  "level_ceiling": "supervised", "level_ceiling_source": "workspace",
  "grant_ceiling": ["merge-pr", "close-issue"], "grant_ceiling_source": "machine",
  "roles": ["coordinator"],
  "grant": [
    {"authority": "merge-pr", "sources": ["role:coordinator"]},
    {"authority": "close-issue", "sources": ["dispatch", "role:coordinator"]}
  ],
  "unknown_grant": [
    {"authority": "teardown", "sources": ["dispatch"]}
  ]
}
```

`grant_ceiling_source` is one of `machine`, `overlay`, `workspace` or
`none`. A record dispatched where no layer declares a ceiling carries
`"grant_ceiling": [], "grant_ceiling_source": "none"`; one dispatched
under `[authority] ceiling = []` carries `"grant_ceiling": [],
"grant_ceiling_source": "workspace"`. A later gate keeps today's verdict
for the first and lets the grant decide, so denies, for the second.

v1 is additive. A later niwa may add fields, authority names and source
kinds without changing the schema string; a reader ignores fields it
doesn't know, never treats an unknown authority as held, and lists it
under `unknown_grant`. Only renaming a field or changing what one means
moves the schema to v2. A v1 reader doesn't treat v2 or later as
malformed: it reads only `schema` and the version-independent
`dispatch_id` (which must be a UUID, or the record is malformed) and
treats the record as holding no grant. `show` prints `dispatch record:
newer version <schema>; grant none`, and `--json` prints a complete v1
document: `"record": true`, then `record_version` naming the stored
schema (a v1 record's document has no such key), that `dispatch_id`,
empty parent fields with `parent_dispatch_source: none`, `level` and
`level_ceiling` both `strict` with sources `newer-version`, and empty
`grant_ceiling` (source `newer-version`), `roles`, `grant` and
`unknown_grant`. `decide` answers from that `strict` with
`source=newer-version` and `record=present`; `niwa list --json` carries
the same document. `WriteAuthorityRecord` refuses a record with
`NewerVersion` set, so none is ever stored. `schema`, in the
`niwa.authority.record/v<N>` form, and `dispatch_id`, a UUID, are fixed
across every version. That way
an older and a newer niwa on one host don't stop each other's
instances from dispatching after a bump, and reading the record as granting
nothing gives up no authority; a gate honouring the grant denies that
instance's gated actions until it reads the newer version, failing narrow. A record that is actually malformed (not a JSON object, a
schema matching no version, a v1 record missing a required field) still
fails closed. A field added within v1 is optional, and a reader that knows it defaults it when
an older niwa's record lacks it. The
guide's held check reads `grant[].authority` only, so a v1 record with
entries it doesn't know still answers for the ones it does.

`niwa authority policy --json` prints one document under the same
additive rule, with these top-level fields in this order: `schema`
(`"niwa.authority.policy/v1"`), `workspace`, `overlay` and `machine`
(each `{"level": <level or null>, "ceiling": <list or null>}`, null
meaning not set), `level_ceiling`, `level_ceiling_source`,
`grant_ceiling`, `grant_ceiling_source`, `roles` (a list of `{"name",
"authorities", "outside_ceiling"}` in name order), `no_flag_dispatch`
(`{"level", "level_source", "grant": []}`) and `warnings` (strings).
When the overlay's or the machine's ceiling names an authority the base
ceiling lacks, the intersection drops it, and `policy` and `apply` print
a warning naming the authority and the layer. A name this niwa doesn't
know gets only Decision 6's dropped-name warning, not this one too.

With no record: `{"schema": "niwa.authority.record/v1", "record": false,
"grant": [], "level": "<no-flag level>", "level_source": "<layer>"}`;
`record` is what marks it, and only the text output and `decide`'s
`source=` line add ` (no dispatch record)`.

**Commands and exit codes.**

| Command | 0 | 1 | 2 |
|---------|---|---|---|
| `niwa authority policy [--json]` | printed | config error, outside a workspace | usage |
| `niwa authority show [--json] [-C dir \| --session id \| --instance name]` | printed (record or none) | bad or unreadable record, missing instance, bad `-C`, config error, outside a workspace, watchdog | usage, any two selectors together |
| `niwa authority decide <point> [same selectors]` | printed an answer | as `show` | usage, bad point name (checked first) |

`decide` prints `point=`, `level=`, `decider=`, `author_exclusion=`,
`source=` and `record=present|none`. A caller settles a point only on exit
0 with an exact `decider=session` line, and holds an authority only by the
reference held check. Neither ever trusts exit 0 alone.

**Dispatch flags.** `--level <level>`, `--grant <authority>` (repeatable;
a comma list splits, so `--grant a,b` equals `--grant a --grant b`, and an
empty entry is refused), `--role <name>` (repeatable, split the same
way), `--parent-from <abs dir>`.

### Data Flow

1. `niwa dispatch --role coordinator "<task>"` from inside coordinator
   instance A.
2. (2a) machine keys parsed. (2e) base config load checked fail-closed,
   overlay found by `ResolveAuthorityOverlay`, `ValidateConfig`, request
   parsed, `Resolve`; A's record read through `FindInstance(cwd)` for
   `parent_dispatch_id`, with `parent_dispatch_source: cwd` (from the
   workspace root, `--parent-from <A's path>` gives the same id with
   `anchor`). Any error: exit 1, nothing created.
3. (3) to (4) unchanged; the dispatch id minted at (4). Stderr line
   printed. (5) to (8) unchanged. (8a) policy re-checked against what B
   was built from, identity fields stamped, and the record written into
   the new instance B.
4. (9) launch with unchanged argv and settings, plus two lineage
   attributes. (10), (11) unchanged.
5. B's first command, `niwa authority show`, runs `FindInstance(cwd)`,
   reads B's record and prints it.
6. Later, a gate hook in B runs `niwa authority show --json -C
   "$(dirname "$0")"` and the guide's held check; this unit ships the
   check and its CI test, not the gate.

### What later work consumes

- **Gates honouring the grant.** A gate keeps today's text match to spot
  a merge, close or release, then runs the lookup (guarded by `command -v
  niwa`) with the same `-C` anchor. For a session whose record exists
  (`"record": true`) and whose `grant_ceiling_source` is anything other
  than `none`, the grant decides, not the instance exemption list
  today's hook keeps: the action is allowed only when the held check
  finds the authority and `niwa authority decide <point>` prints
  `decider=session`, and denied otherwise, even for an instance on the
  exempt list. Otherwise a dispatched session on that list without
  `merge-pr` could still merge. When `grant_ceiling_source` is `none`,
  the workspace never opted in to grants, so the gate keeps today's
  verdict, exemption list included: grants are opt-in, and a gate
  upgrade must not change merging for a workspace that never adopted
  them, whatever order the gate and the ceiling arrive in. A ceiling
  declared empty has a declaring source, so the grant decides and
  denies: that workspace said no session may hold these authorities. A
  later-version record has source `newer-version`, not `none`, so the
  grant decides and holds nothing, failing closed. A session with no
  record (`"record": false`), and every lookup that fails, keeps today's
  behaviour,
  exemption list included, so a missing, old or failing niwa leaves the
  gate exactly as strong as the current hook. The
  gate must bound the lookup itself, behind niwa's own watchdog, because a
  harness that kills a slow hook drops its deny; where it can't bound it
  (no `timeout` and no background-job fallback), it skips the lookup and
  keeps today's behaviour. `$0` anchoring works for hooks niwa copies into the
  instance; a gate shipped some other way (as a plugin hook, or through a
  symlinked tree) resolves outside the instance and reads as not held,
  so such a gate has to be installed into the instance to honour a grant.
  The guide still advises rolling a gate out together with the
  workspace's `[authority]` ceiling and roles and re-dispatching running
  sessions, so their records carry the ceiling, but nothing depends on
  that order: a session dispatched before any ceiling existed records
  `none` and keeps today's verdict.
- **A coordinator's posture read.** The same command with `-C` set to the
  instance root it found, and the same held check, so its prediction
  matches the gate. With no niwa it predicts not held. To read the
  postures of the sessions it runs, it uses `niwa list --json` (each
  instance's `authority` object) or `show --instance`. It should pass
  `--parent-from <its own instance>` when it dispatches successors, so
  `parent_dispatch_id` and the never-wider check find its record. That
  change belongs with, or after, shirabe's coordinator record teardown
  (tsukumogami/shirabe#978 to tsukumogami/shirabe#986), not as a separate
  edit; until then
  a coordinator that dispatches from the workspace root records
  `parent_dispatch_source: none` and gets no cap, which is harmless while
  nothing enforces one.
- **Never-wider re-delegation.** A check at step (2e), using the
  dispatcher's record that step already reads through `FindInstance` on
  `--parent-from` or the dispatcher's working directory (never the
  session id variable),
  refuses a child whose level or grant is wider. With no dispatcher record
  it provisionally adds no cap. Records link child to parent through
  `parent_dispatch_id`. (2e) covers the dispatcher's own record only; it
  doesn't walk older ancestors. `parent_dispatch_id` can dangle, because
  a coordinator usually reaps its predecessor, so nothing resolves it as
  a live session. That a missing parent or ancestor record means no cap,
  never a refusal, is this design's provisional reading, and the
  re-delegation work decides it. A dispatcher record with a later schema
  version is the
  narrowest case, never an uncapped one: the child can be granted
  nothing, and its level can be no wider than `strict`.

### Lineage and where authority lives

niwa's lineage fields (`parent_dispatch_id`, `parent_session_id`) are
niwa's own. A workflow engine such as koto keeps its own record of which
run started which, and the two never read each other. Authority belongs
to the instance: a session resumed in another instance, or on another
host, reads that instance's record, or none, and the record is never
copied into a workflow engine's state or a cloud store.

"Dispatcher" in a record means the seat that dispatched, not a
particular session. When a coordinator is replaced, its successor holds
the same seat, so the record says nothing about who supervises the child
now. A successor coordinator gets its own grant from its own dispatch
(`--role coordinator`), with no config edit.

## Implementation Approach

### Phase 1: Vocabulary, policy and inspection

`internal/authority` with table tests for parsing, `EffectivePolicy`,
`Resolve` (every source and tie rule, every refusal), `Record` encoding and
`ParseRecord`, and `Decide` with a fake `Declarations`. The three config
layers, `ValidateConfig` wired to refuse in the authority commands and
warn in `apply`, the overlay tombstone warning, `niwa config set/unset
authority-level|authority-ceiling`, and `niwa authority policy`. Includes
the empty-list round trip (machine and overlay `[]` stay empty through
decode and a later `niwa config set`), unknown keys under `[authority]`
and `authority_*` and wrong-typed values reaching `ValidateConfig` (and
`apply` still completing with a warning), `ParseRecord`'s field
validation and its additive rule (a v1 record with an unknown field, an
unknown authority such as `teardown`, or an unknown source kind such as
`delegated:x` parses, holds only the known entries and lists the rest in
`unknown_grant`; a v2 record with a UUID `dispatch_id` parses as holding
nothing; truncated JSON, a v2 record without a UUID `dispatch_id`, an
unknown level or a name failing the pattern fails), unknown authority names in a ceiling or role
dropped with a warning (Decision 6) while `--grant` and `config set`
still refuse them, the `niwa.authority.policy/v1` document, the
warning for an overlay ceiling naming an authority the base lacks, and
`ResolveAuthorityOverlay` across its three branches. No change to
dispatch.

Deliverables: `internal/authority/*.go`, `internal/config` changes,
`internal/cli/config_authority.go`, `internal/cli/authority.go` (`policy`).

### Phase 2: Record I/O and dispatch

The record's storage first, since dispatch both writes a record and reads
the dispatcher's: `WriteAuthorityRecord`, `ReadAuthorityRecord` with the
safe open (FIFO, symlink, oversized, unreadable and control-character
fixtures), and `FindInstance` (planted-marker and symlinked-start
fixtures, with `ClassifyCwd`'s existing tests unchanged). Then dispatch:
the three flags, the (2e) step with the fail-closed config and overlay
checks, resolution and refusal before (3), `--grant a,b` splitting and
an empty entry refused, `parent_dispatch_id` and `parent_dispatch_source`
from the dispatcher's record (present, absent and invalid, through the
working directory and through `--parent-from`, plus an empty, relative
or missing `--parent-from`, one in no instance, one in another
workspace, and one disagreeing with the working directory's instance
refused), the stderr line after
(4), the (8a) re-check (a convention overlay or refreshed base config
that refuses the request or changes the level or grant during
provisioning refuses and rolls back; one that only narrows or widens the
ceiling is recorded), the
record at (8a), and the lineage attributes. Unit tests with the existing dispatch
fakes; functional scenarios for every refusal (nothing created, no
prompt), the record's contents, a failed record write rolling back with
no launch, the `/`-joined grant attribute surviving beside the existing
lineage attributes, `--level strict` with `--permission-mode
bypassPermissions` and a `bypass` posture with no `[authority]`, the
record surviving `niwa apply` and removed by `niwa reset`, and hook
files and settings documents byte-identical between a `--role
coordinator` or `--level strict` dispatch and a no-flag one apart from
the lineage attributes.

Deliverables: `internal/workspace/authority_record.go`,
`internal/workspace/cwd_classify.go` (`FindInstance`),
`internal/workspace/authority_overlay.go`, `internal/cli/dispatch.go`,
`internal/cli/dispatch_lineage.go`.

### Phase 3: Read-back

`selectDispatch`, `niwa authority show` and `decide` with every selector
and combination, `-C` (empty, relative, missing), the no-record document
and `record=` line, the watchdog, a mapping whose `instance_path` points
outside the workspace, and the `authority` object in `niwa list --json`
(including a malformed record's `error`). Functional tests: a fake agent
reading its own record as its first command (Claude and Codex, detached
and foreground, root, repo and worktree, with an inherited session id and
after a resume, `claude --bg --resume` included), `unknown_grant` in
`show` output, and the decide-driven marker test at both ends. The
250-mapping benchmark (median of ten runs of the built binary), the test
that counts session-mapping reads, and the seam test that fails on any
network or process call.

Deliverables: `internal/cli/authority.go` (`show`, `decide`,
`selectDispatch`), `internal/cli/list.go`.

### Phase 4: Guide and contract tests

`docs/guides/authority-grants.md` with the keys, flags, authority
coverage, the R8a example, the JSON document and schema, the selection
order and `-C`, the no-record answer, the held rule and the reference
check, the settle rule, R4's combination, and what the record does and
doesn't guarantee, including that a gate lets the grant decide only for
a session whose lookup printed `"record": true` with a
`grant_ceiling_source` other than `none`, and otherwise keeps today's
behaviour, exemption list included, bounds the lookup itself,
and labels the grant "recorded, not enforced". It also says that no ceiling
means nobody can be granted anything, that a successor coordinator gets
a fresh grant with `--role coordinator` and no config edit, that a seat
started by hand or an instance from `niwa create` has no record, that
`--parent-from` records the parent for a dispatch run from the workspace
root, that v1 is additive, and that `niwa authority show --json -C
<instance>` saved before `niwa destroy` keeps the record. A CI test runs
the guide's held, record and exact-line `decider=session` checks, as
shell functions inside a stub gate, against real `show` and `decide`
output, against a v1 record carrying entries this build doesn't know,
against a later-version record, and against each failure. It checks
that a present record decides even for an exempt instance, that a
record with `grant_ceiling_source: none` and every failure keep the
stub's today's-behaviour verdict, and that a declared-empty ceiling
denies. A CI job runs the
authority unit and functional tests with no koto binary on `PATH`. The
one-time live check with a real Claude Code session is recorded in the
implementing pull request.

Deliverables: `docs/guides/authority-grants.md`, the contract test, and
the CLAUDE.md contributor-guides entry.

Phase 2 depends on Phase 1; Phase 3 needs Phase 2's record and reader;
Phase 4 needs all three.

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
would widen policy on the machine that has the problem. For the same
reason, an unknown key inside a base or overlay `[authority]` table (a
misspelt `ceilng`, say) and an unknown `authority_*` key under `[global]`
are invalid values, not the usual unknown-key warning: a misspelt
narrowing key would otherwise leave policy silently wider. With no level
anywhere, the level is `autonomous`.

**Empty lists are real limits.** `authority_ceiling = []` and an overlay
`ceiling = []` mean nothing is grantable, not "unset". The machine field
is stored so that an empty list survives decoding and every later rewrite
of the personal config (no `omitempty` on it), and the overlay field
decodes `[]` to a present, empty list. A round-trip test sets `none`, runs
an unrelated `niwa config set`, and checks the ceiling is still empty.

**Writing the record.** The record is written at (8a) through a temp file
created with `O_EXCL|O_NOFOLLOW` in the new instance's `.niwa/`, synced and
renamed, with mode 0600; it is not written with the dispatch marker's
plain writer. `Resolve` de-duplicates roles and sources and checks the
encoded record against the reader's 64 KiB cap at (2e), so an oversized
request is refused before the sweep and provisioning; a failed write at
(8a) fails the dispatch and triggers the rollback, so no worker is ever
launched without its record. `niwa apply` and a config refresh leave the
record alone (it isn't a managed file, and a test pins that); `niwa reset`
removes it with the instance, after which the instance reports no record.
A dispatcher instance with no record (one dispatched by an older niwa)
gives an empty `parent_dispatch_id`; only a record that exists but is
invalid refuses the child's dispatch. The record holds no secret, but the
design doesn't claim it's private from other local users: the instance
directory keeps the mode provisioning gives it.

**Reading the record.** `show` and `decide` arm a watchdog of about two
seconds before their first filesystem call, open the record without
following a final symlink and without blocking, require a regular file,
read at most 64 KiB, and print only after a complete, successful parse,
so a timeout or error leaves stdout empty. `ParseRecord` validates every
string field, not only the vocabularies: `dispatch_id` is a UUID,
`parent_dispatch_id` is a UUID or empty, `parent_session_id` passes the
existing session-id check or is empty, each role and each authority name
(known or not) matches the name pattern, each source is `dispatch` or
`<kind>:<valid name>`, and the known grant is in R3's order. An unknown
name that passes the pattern is listed in `unknown_grant`, never held. A
hand-edited record therefore can't put control
characters in front of an operator running `niwa list` or `show`. Error
text and `niwa list`'s per-instance `error` name a failure class and never
echo file contents or the record path. `--instance` names go through the
existing name validation and instance enumeration before they're joined
to a path, and the `instance_path` a session mapping holds is accepted for
`--session` only if it's an instance directly under the workspace root.
Neither command makes a network call or starts a process. Reading an
existing record found from the working directory or `-C` performs only
reads; the `--session`, `--instance` and no-record paths discover the
workspace root the way every niwa command does.

**Anchoring a hook.** Cloned repositories are untrusted content, and a
directory that carries its own `.niwa` marker could be mistaken for an
instance. `FindInstance` resolves symlinks in the starting directory
first, then walks up and takes the outermost candidate that sits directly
under a workspace root and that niwa's own instance enumeration for that
root returns (or a niwa worktree of one), so a marker planted inside a
repository can't stand in for its instance. It uses the same marker
checks as `ClassifyCwd` but leaves `ClassifyCwd`'s own behaviour
unchanged; the two differ on purpose (nearest marker for general cwd
classification, outermost enumerated instance for authority lookups),
and a comment in `cwd_classify.go` says so. An empty, relative or nonexistent `-C` argument is an error,
never a fallback to the working directory; a `-C` directory in no
instance gives the no-record answer, as a bare run outside an instance
does, since no lookup reads the session id variable; and `-C` combined
with `--session` or `--instance` is a usage error. The guide tells hook
authors to anchor on the instance's own hook copy, to check that `$0` is
absolute, and to pass the anchor after `-C` as an absolute path.
`niwa dispatch --parent-from` goes through the same `FindInstance` and the
same path checks, so a planted marker can't name a parent either. The
dispatcher chooses the anchor, so `parent_dispatch_id` records what the
dispatcher said, and `parent_dispatch_source` says it was named rather
than found; a never-wider check reading it caps by a record the
dispatcher picked, which is why that work has to decide whether an
`anchor` parent is trusted the same as a `cwd` one.

**Failing toward today's behaviour.** Every failure reads as "not held"
and as "no record": a missing or older niwa, a missing `jq`, a non-zero
exit, help text, an unknown schema, a malformed or unreadable record, or
the watchdog. The guide states that a gate lets the grant decide only
when the lookup positively printed a document with `"record": true` and
a `grant_ceiling_source` other than `none`: then it allows a merge,
close or release only on a held authority plus an exact
`decider=session` line, and denies otherwise, the instance exemption
list included. Every other outcome, `"record": false` and a `none`
source among them, leaves today's behaviour, exemption list and all. The gate must
bound the lookup's time itself (skipping it and keeping today's
behaviour when it can't), since a harness that kills a hung hook drops
the hook's verdict. The guide ships the checks as shell functions that
return rather than exit, and niwa's CI runs them inside a stub gate
against real and failing output, checking that each failure keeps the
stub's today's-behaviour verdict. `decide` also prints a `record=present|none` line, so a
caller that needs a dispatched session's answer rejects the no-record
case without parsing `source=`. With no record, `decide`
answers from the level a no-flag dispatch would get and marks the source,
because the usual caller then is the operator's own session.

**Supply chain and dependencies.** No new Go module is added; the reader
uses the standard library. `jq` is a test-time tool for niwa and a
runtime tool only for hook authors, and a missing `jq` reads as not held.
The overlay is a git clone of the organization's overlay repository. It
can narrow the level and ceiling and can't declare roles, so a compromised
overlay can at worst refuse dispatches or reduce grants. Its `roles`
tombstone and its unknown keys outside `[authority]` are only warned
about, never evaluated; an unknown key inside its `[authority]` table is
an invalid value and refuses dispatch, like any other misspelt narrowing
key.

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
session that corrupts its own record blocks its own child dispatches. Until
a gate reads the grant, the stderr line and the telemetry attributes are
labels, not restrictions; the guide says "recorded, not enforced" where
it introduces them, and that `parent_dispatch_id` isn't a boundary. A
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
  successor coordinator can be granted merge, close and release with no
  config edit between sessions. Merging itself still goes through the
  workspace hook and its exemption list until gates honour the grant, so
  until then a successor that has to merge still needs the exemption.
- A shared config that names an authority a newer niwa added doesn't
  stop older niwas from dispatching (Decision 6).
- Later gate, coordinator and never-wider work build on one package, one
  versioned document and one tested held check, and none of them needs a
  PRD change to handle an old or missing niwa.
- Workspaces that set nothing see one stderr line and two telemetry
  attributes, and every session can do exactly what it could before.

### Negative

- A grant changes nothing until a gate reads it, so for now it's a
  declaration with no effect beyond being readable.
- A workspace config that exists but can't be parsed now stops `niwa
  dispatch` at (2e) instead of at provisioning, and a machine that lacks
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
- Enforcement stays at today's strength by design (R28); making the
  record tamper-proof is the separate later effort the PRD names.
