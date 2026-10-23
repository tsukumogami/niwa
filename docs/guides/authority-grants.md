# Authority levels and named grants

`niwa dispatch` can say how much a worker may decide on its own and which
named actions it may take. The first is the worker's authority level; the
second is its grant, a set of named authorities such as `merge-pr`. Both are
declared in config, narrowed or granted per dispatch, written into a record
inside the dispatched instance before the worker starts, and read back with
`niwa authority show` and `niwa authority decide`.

Nothing in this guide changes what a session can do. The grant is recorded,
not enforced: no hook, permission mode, credential or settings document a
worker launches with depends on it. The workspace's gates decide exactly as
they did before until a gate is written to read the record, and the second
half of this guide is the contract such a gate follows.

None of this needs koto. A host with niwa alone declares, records and reads
authority, and no part of the record or the lookup reads anything koto
writes.

## Levels and authorities

A level is one of three values, widest first:

| Level | Decider | Author exclusion |
|-------|---------|------------------|
| `autonomous` | `session` | `off` |
| `supervised` | `dispatcher` | `on` |
| `strict` | `person` | `on` |

A decision point is a named place where a session would settle something on
its own, such as accepting its own requirements or merging its own pull
request. Until a point carries a declaration of its own, it resolves from the
level alone, by this table. "Narrower" always means further toward `strict`.

`dispatcher` means the seat that launched the session: the coordinating
position, whichever session currently fills it, or the person at the shell
when the dispatch ran outside any session. It's the seat, not a particular
session, so a successor that takes over a coordinator's seat is the
dispatcher from then on. Author exclusion `on` means whoever settles the
point may not be the session that wrote the work the point is about.

The named authorities are a fixed set per niwa version. This version has
three:

| Authority | Covers |
|-----------|--------|
| `merge-pr` | merging a pull request, by any route (CLI, REST, GraphQL, auto-merge) |
| `close-issue` | closing an issue; closing a pull request without merging is not covered |
| `cut-release` | publishing a release or a release tag |

Wherever niwa lists authorities it uses this order.

### How the level and the grant combine

They're separate, and both apply. The grant names the actions a session may
take; the level decides who settles a decision point. No level adds or
removes an authority, and no grant changes a level. An action an authority
covers is the session's to take only when it holds that authority and the
decision point for the action resolves to `session`. Neither answer alone
permits it: a session at `strict` holding `merge-pr` still resolves its merge
decision to `person`, and a session at `autonomous` without `merge-pr`
resolves the decision to `session` and still doesn't hold the action.

## Declaring the policy

Authority is declared in three layers. The workspace's base config declares;
the overlay and the machine can only narrow.

### The workspace: `[authority]`

The base `.niwa/workspace.toml` takes an `[authority]` table:

| Key | Meaning | With no key |
|-----|---------|-------------|
| `level` | the workspace level | `autonomous`, with source `built-in` |
| `ceiling` | the authorities a dispatch may be granted | empty: nothing is grantable |
| `roles.<name>.authorities` | a named bundle of authorities | no roles |

With no `ceiling`, nobody can be granted anything, and a dispatch that asks
for an authority is refused. A workspace opts in by declaring one. This makes
merging, closing issues and cutting releases grantable, and gives
coordinating sessions a role for all three:

```toml
[authority]
level = "autonomous"
ceiling = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]
```

After that, `niwa dispatch --role coordinator "<task>"` launches a session
whose record holds all three. A role name matches `^[a-z][a-z0-9-]{0,63}$`.
A role may name an authority outside the ceiling, since the machine can
narrow the ceiling further and the check happens when the role is granted. A
role with an empty `authorities` list is valid and grants nothing.

### The overlay

A workspace overlay may set `level` and `ceiling` under its own
`[authority]` table, and both only narrow: the level ceiling is the narrower
of the base and overlay levels, and the grant ceiling is the authorities in
both lists. An overlay ceiling naming an authority the base lacks doesn't add
it; `niwa authority policy` and `niwa apply` warn about it. An overlay can't
declare or change a role. An `[authority.roles]` table in an overlay is
ignored, and every command that loads the overlay says so.

Declaring an overlay `ceiling` opts every workspace the overlay applies to
into grant gating, even one whose base config has no ceiling. Sessions
dispatched there afterwards record a declared ceiling, so a gate that honours
the grant judges them by their grant instead of by today's verdict, and a
session on the gate's exemption list moves from today's verdict to the
grant's (see [Writing a gate](#writing-a-gate-that-honours-the-grant)).

### The machine: `authority_level` and `authority_ceiling`

Your personal niwa config, `~/.config/niwa/config.toml` (or
`$XDG_CONFIG_HOME/niwa/config.toml`), the file that holds
`dispatch_permission_mode`, may cap both under `[global]`:

```toml
[global]
authority_level = "supervised"
authority_ceiling = ["merge-pr"]
```

`authority_level` caps the level and can never make one wider.
`authority_ceiling` limits the grant ceiling to the authorities in both it and
the workspace's ceiling, and can never add one. An absent key limits
nothing; `authority_ceiling = []` limits the ceiling to empty. The commands
that write them trim each entry, keep a duplicate once, and refuse an invalid
value with the file left as it was:

```sh
niwa config set authority-level strict
niwa config set authority-ceiling merge-pr,close-issue
niwa config set authority-ceiling none      # an empty list: nothing grantable
niwa config unset authority-level
niwa config unset authority-ceiling
```

Declaring `authority_ceiling` opts every workspace on this machine into
grant gating, even one with no base ceiling, in the same way an overlay
ceiling does: exempt sessions dispatched there afterwards move from today's
verdict to the grant's. An empty machine ceiling is a declared one too, so a
gate that honours the grant denies the gated actions of every session
dispatched on this machine after it's set.

### Checking values

Levels accept only `autonomous`, `supervised` and `strict`, and authorities
only the three names above, lowercase, after trimming whitespace. An empty
level means not set. An invalid value (an unknown level, a role or authority
name failing the name pattern, an unknown key inside `[authority]`, an
unknown `authority_*` key under `[global]`, a value of the wrong type)
refuses `niwa dispatch` and
`niwa authority policy` with exit 1 and a message naming the key, the value
and the accepted values. `niwa apply` prints the same message as a warning
and carries on, so a fix can still be pulled from the config's source.

One exception keeps a shared config usable across niwa versions. An
authority name in a ceiling or a role that matches the name pattern but isn't
one this niwa knows (`teardown`, say, added by a newer niwa) is dropped from
that list with a warning, and grants nothing. A ceiling left empty by the drop
is still a declared, empty ceiling. `--grant teardown` and `niwa config set
authority-ceiling teardown` are still refused, since that's a person typing on
this niwa.

### `niwa authority policy`

Run inside a workspace, `niwa authority policy` prints each layer's level and
ceiling (`not set` where absent), the resolved level ceiling and grant
ceiling with their sources, every role with its authorities (marking any
outside the effective ceiling), and what a dispatch with no authority flags
would resolve to. `--json` prints the same as one document with schema
`niwa.authority.policy/v1`.

## Dispatching with a level and a grant

`niwa dispatch` takes four authority flags:

| Flag | Meaning |
|------|---------|
| `--level <level>` | narrow the level for this one dispatch |
| `--grant <authority>` | grant one authority; repeatable |
| `--role <name>` | grant every authority the role names; repeatable |
| `--parent-from <abs dir>` | name the dispatcher's instance when dispatch runs from somewhere else |

`--grant` and `--role` take comma lists too: `--grant merge-pr,close-issue`
equals two `--grant` flags, and an empty entry (`--grant merge-pr,`) is
refused. The grant is the union of everything requested.

Resolution is narrowest-wins. The level ceiling is the narrowest of the
workspace, overlay and machine levels, with its source (`machine`, `overlay`,
`workspace` or `built-in`; a tie goes to the first of these). A `--level`
wider than the ceiling is refused. Otherwise the level is the narrower of the
two, with source `flag` when the flag supplied it. The grant ceiling is the
base ceiling intersected with the overlay's and the machine's lists, and its
source is `none` when no layer declares a ceiling at all, otherwise the layer
that narrowed it or declared it.

A request past either ceiling launches nothing. The refusal names what was
asked for, how, and the ceiling with its source:

```text
niwa: error: --level supervised is wider than the authority level ceiling strict from machine
niwa: error: authority cut-release (requested by role coordinator) is outside the grant ceiling: the grant ceiling is merge-pr from workspace
```

Every refusal at this point comes before dispatch asks for a prompt, before
the sweep of stale instances and before any instance exists. One refusal
comes later: provisioning can refresh the workspace config or bring in an
overlay, so dispatch resolves the request again against what the new
instance was built from, and if that refuses it or gives a different level or
grant, the dispatch exits 1 naming the setting that changed and the new
instance is removed.

A dispatch that launches prints one line to stderr saying what it resolved:

```text
niwa dispatch: authority level strict from flag; grant merge-pr (dispatch)
niwa dispatch: authority level autonomous from built-in; grant none
```

The line is recorded, not enforced: it describes the record, and nothing
reads it to allow or block an action. A Claude worker also gets two
telemetry resource attributes beside niwa's lineage attributes,
`niwa.authority.level=<level>` and `niwa.authority.grant=<a/b/...>` (the
authorities joined with `/`, or `none`). They're recorded, not enforced
either, best-effort, and aren't the record. A Codex worker gets neither, and
its record carries the same values.

### Callers that build a dispatch command

Dispatch refuses a flag it doesn't know, so an older niwa refuses
`--level`, `--grant`, `--role` and `--parent-from` outright. A caller that
can't pass `--level` (because the niwa it runs is too old, say) must not
dispatch without it, since the worker would then run at a wider level than
the caller meant. A caller that can't pass `--grant`, `--role` or
`--parent-from` may dispatch without them: the session then holds less, or
records no parent, which is never wider.

### The parent link and `--parent-from`

Each record names the dispatch it came from in `parent_dispatch_id`, found
from the dispatcher's own record: the instance the dispatcher's working
directory is in, or the one `--parent-from <abs dir>` names. Dispatch never
reads the session id variable for this, since a Codex worker inherits its
dispatcher's. `parent_dispatch_source` says how the parent was found:
`cwd`, `anchor` (through `--parent-from`) or `none` (no record found, so the
id is empty).

A dispatch run from the workspace root records its parent only with
`--parent-from`. A coordinator that changes to the workspace root before
dispatching passes `--parent-from` with a directory in its own instance;
without it the child records `parent_dispatch_source: none`.

`--parent-from` takes the same checks as `niwa authority show -C`: an empty,
relative or nonexistent path exits 1 before anything is created. It's
stricter than `-C` in three ways, each also an exit 1 before anything is
created: a path in no instance; an instance under a different workspace root
than the dispatch's; and, whenever dispatch runs with its working directory
inside an instance, any instance other than that one. Any instance counts for
the last rule, including one made with `niwa create` that has no record, and
a niwa worktree of an instance counts as that instance. That way the anchor
and the working directory can never name two different parents, and an
instance someone made by hand is still the dispatcher's own. An instance
that has no record gives no parent. A record that exists but can't be read or
is malformed refuses the dispatch rather than recording an empty parent.

## The record

Every dispatch that launches a worker writes one record into the dispatched
instance before the worker starts. It's never rewritten: `niwa apply`, a
config change, a resume and other dispatches leave it as it was, and a wider
grant means a new dispatch. It's removed with the instance (rollback,
`niwa reset`, `niwa destroy`, `niwa reap`). To keep a copy before destroying
an instance, save its document first:

```sh
niwa authority show --json -C /path/to/instance > record.json
niwa destroy <instance>
```

Where the record is stored, and its stored bytes, aren't part of the
contract; the document `niwa authority show --json` prints is. A session
started by hand, or an instance from `niwa create`, has no record: no grant,
and no cap on what it dispatches.

### The document

```json
{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "parent_dispatch_source": "none",
  "level": "strict",
  "level_source": "flag",
  "level_ceiling": "supervised",
  "level_ceiling_source": "workspace",
  "grant_ceiling": ["merge-pr", "close-issue"],
  "grant_ceiling_source": "machine",
  "roles": ["coordinator"],
  "grant": [
    {"authority": "merge-pr", "sources": ["role:coordinator"]},
    {"authority": "close-issue", "sources": ["dispatch", "role:coordinator"]}
  ],
  "unknown_grant": []
}
```

| Field | Meaning |
|-------|---------|
| `schema` | `niwa.authority.record/v1` |
| `record` | `true` for a record, `false` for the no-record answer |
| `dispatch_id` | the dispatch's id, a UUID |
| `parent_session_id` | the dispatching session's id, empty outside any session |
| `parent_dispatch_id`, `parent_dispatch_source` | the parent link above |
| `level`, `level_source` | the resolved level and where it came from (`flag`, `machine`, `overlay`, `workspace`, `built-in`) |
| `level_ceiling`, `level_ceiling_source` | the level ceiling and its source |
| `grant_ceiling`, `grant_ceiling_source` | the grant ceiling and its source: `machine`, `overlay`, `workspace` or `none` |
| `roles` | the roles granted, in name order |
| `grant` | each held authority with every source it came from: `dispatch` for `--grant`, `role:<name>` for a role, `dispatch` first |
| `unknown_grant` | entries this niwa can't hold, same shape, never held |

`level_source` describes the resolution at dispatch, and can differ from
`level_ceiling_source` in a valid record: when provisioning brings in an
overlay at the same level, the record keeps the level and its source as the
stderr line printed them, while the ceiling's source names the overlay the
instance was built from.

`grant_ceiling_source` is `none` only when no layer declares a ceiling.
`"grant_ceiling": [], "grant_ceiling_source": "none"` means the workspace
never opted in; `"grant_ceiling": [], "grant_ceiling_source": "workspace"`
means it declared `ceiling = []`, so no session may hold anything.

### Versions

v1 is additive. A later niwa may add fields, authority names and source kinds
without changing the schema string. A reader ignores fields it doesn't know
and never treats an unknown authority as held. niwa's own reader lists a
grant entry whose authority it doesn't know, or whose sources are all of
kinds it doesn't know, under `unknown_grant`, and plain `show` prints it on a
separate "not recognised by this niwa" line.

Only renaming a field or changing what one means moves the schema to a new
version. A record written in a later schema version reads as holding no
grant, not as malformed and not as no record: `show` prints
`dispatch record: newer version niwa.authority.record/v2; grant none`, and
`show --json` prints a v1 document with `"record": true`, a `record_version`
key naming the stored schema (a v1 record's document has no such key), the
`dispatch_id`, `level` and `level_ceiling` both `strict` with sources
`newer-version`, and empty grant fields. A record that is actually malformed
fails the lookup.

## Reading it back

### `niwa authority show`

`niwa authority show` prints a dispatch's record. It selects the dispatch in
this order:

1. `--session <id>`: the session that id names.
2. `--instance <name>`: the instance of that name.
3. Otherwise, the instance containing the working directory, or the
   directory `-C <dir>` names.

It never reads the session id variable, so a session that left its instance,
or one at the workspace root, never reads its dispatcher's record as its own.
Any two selectors together are a usage error (exit 2). An empty, relative or
nonexistent `-C` exits 1, never falling back to the working directory. A
directory in the workspace that's in no instance, an instance with no record
and a `--session` with no mapping all give the no-record answer, which exits
0 and prints:

```text
dispatch record: none
level: autonomous from built-in (no dispatch record)
grant: none
```

With `--json` the no-record answer is
`{"schema": "niwa.authority.record/v1", "record": false, "grant": [], "level": "autonomous", "level_source": "built-in"}`,
where `level` is what a dispatch with no flags would resolve to there and
`record` is what marks it. The no-record level comes from the workspace's
config, so outside any workspace the lookup exits 1 instead. A record that
exists but can't be read or is malformed exits 1 with nothing on stdout. A
watchdog stops any lookup after about two seconds, also with exit 1 and
nothing on stdout. With `--json`, stdout carries only the document.

A dispatched session reads its own record by running `niwa authority show`
with no selector, from the instance root, a repo inside it or a worktree of
it, from its first command and after a resume.

### `niwa authority decide <point>`

`decide` answers who settles a decision point, with the same selection as
`show`:

```text
point=merge-own-pr
level=autonomous
decider=session
author_exclusion=off
source=workspace
record=present
```

`source=` is the level's source, or `declaration` once a point carries its
own declaration. With no record, `decide` answers from the level a no-flag
dispatch would get, adds ` (no dispatch record)` to `source=` and prints
`record=none`. It exits 2 for a point name outside `^[a-z][a-z0-9-]{0,63}$`,
checked before anything else.

### `niwa list --json`

Each dispatched instance's row carries an `authority` object equal to its
`show --json` document. An instance with no record has no `authority` key; one
whose record can't be read has an `authority` object holding only an
`error`. A coordinator reads the posture of every session it runs from one
call.

## The held rule and the settle rule

An authority is held only when `niwa authority show --json` exits 0 and
prints a document with schema `niwa.authority.record/v1`, `"record": true`,
and a `grant` entry naming that authority. Every other outcome means not
held: a non-zero exit, no output, help text from a niwa without the command,
an unknown schema, a malformed document, a missing `jq`, the no-record
answer. Never infer "held" from an exit code alone.

A caller settles a decision point itself only when `niwa authority decide`
exits 0 and prints the exact line `decider=session`. A caller that needs a
dispatched session's answer, as a gate does, also requires `record=present`.

These are the reference checks. niwa's CI extracts this block from this
guide and runs it inside a stub gate against real `show` and `decide`
output and against every failure, so copy it as it is:

```bash
# Reference checks for a dispatch's grant. Each function returns and none
# of them ends the script, so a gate that sources them keeps the verdict it
# has already reached. Pass an absolute directory as the first argument.

# niwa_lookup <args...>: runs `niwa authority <args...>` with stderr dropped,
# bounded by NIWA_LOOKUP_SECONDS (default 3, behind niwa's own two-second
# watchdog). Returns non-zero when niwa is missing, when the command fails,
# and when it runs past the bound.
niwa_lookup() {
  local secs=${NIWA_LOOKUP_SECONDS:-3} pid watcher rc
  command -v niwa >/dev/null 2>&1 || return 1
  if command -v timeout >/dev/null 2>&1; then
    timeout "$secs" niwa authority "$@" 2>/dev/null
    return
  fi
  niwa authority "$@" 2>/dev/null &
  pid=$!
  ( sleep "$secs"; kill -KILL "$pid" ) >/dev/null 2>&1 &
  watcher=$!
  wait "$pid"
  rc=$?
  kill "$watcher" 2>/dev/null
  return "$rc"
}

# niwa_holds <absolute-dir> <authority>: returns 0 only when held.
niwa_holds() {
  local out
  out=$(niwa_lookup show --json -C "$1") || return 1
  printf '%s' "$out" | jq -e --arg a "$2" '
    .schema == "niwa.authority.record/v1" and .record == true
    and any(.grant[]?; .authority == $a)' >/dev/null 2>&1
}

# niwa_grant_decides <absolute-dir>: returns 0 only for a record from a
# workspace that declared a grant ceiling, where the grant decides.
niwa_grant_decides() {
  local out
  out=$(niwa_lookup show --json -C "$1") || return 1
  printf '%s' "$out" | jq -e '
    .schema == "niwa.authority.record/v1" and .record == true
    and (.grant_ceiling_source | type) == "string"
    and .grant_ceiling_source != "none"' >/dev/null 2>&1
}

# niwa_settles <absolute-dir> <point>: returns 0 only when a dispatch with a
# record settles <point> itself.
niwa_settles() {
  local out
  out=$(niwa_lookup decide "$2" -C "$1") || return 1
  printf '%s\n' "$out" | grep -qx 'decider=session' || return 1
  printf '%s\n' "$out" | grep -qx 'record=present'
}
```

They're functions that return, never ones that exit. Pasted into a gate
hook, an `exit 1` is a non-blocking hook error, and it would throw away a
deny the gate had already reached.

## Writing a gate that honours the grant

niwa ships no gate. This is the model a gate follows when it's written to
read the grant.

1. The gate keeps its own text match to spot a merge, a close or a release.
   The lookup runs only for commands that match.
2. It looks the grant up with `niwa_grant_decides`. For a session whose
   lookup printed `"record": true` with a `grant_ceiling_source` other than
   `none`, the grant decides: the action is allowed only when `niwa_holds`
   finds the authority and `niwa_settles` finds an exact `decider=session`
   line, and it's denied otherwise, even for an instance on the gate's
   exemption list.
3. A `none` source (the workspace never declared a ceiling), `"record":
   false` and every failed lookup leave today's behaviour, exemption list
   included. A missing, old, hung or failing niwa leaves the gate exactly as
   strong as it is now.
4. A declared-empty ceiling has a declaring source, so the grant decides and
   denies. A record in a later schema version has source `newer-version` and
   holds nothing, so it denies too.

A sketch, with the gate's existing verdict in `today`:

```bash
anchor=$(dirname "$0")
case "$anchor" in
  /*) ;;
  *) anchor= ;;   # a relative $0 can't anchor the lookup
esac
if [ -n "$anchor" ] && niwa_grant_decides "$anchor"; then
  if niwa_holds "$anchor" merge-pr && niwa_settles "$anchor" merge-own-pr; then
    verdict=allow
  else
    verdict=deny
  fi
else
  verdict=$today
fi
```

Decision point names are the caller's choice; `merge-own-pr` here is an
example, and this feature maps no point to an authority.

### Bound the lookup

A gate must bound the lookup time itself, behind niwa's own watchdog,
because a harness that kills a slow hook drops the hook's verdict, deny
included. `niwa_lookup` does it with `timeout` when that's installed and
with a background job otherwise. A gate that can do neither (no `timeout`
and no background-job fallback) skips the lookup and keeps today's
behaviour.

### Anchor on the hook's own path

Anchor the lookup on the hook's own absolute `$0` with `-C`, as
`-C "$(dirname "$0")"`, never on the payload's `cwd`, which the model
controls, nor on `CLAUDE_PROJECT_DIR` or `--instance`. Check that `$0` is
absolute before using it. This works for a hook niwa copies into the
instance; a gate installed some other way (as a plugin hook, or through a
symlinked tree) resolves outside the instance, reads as no record, and keeps
today's verdict. Such a gate has to be installed into the instance to honour
a grant.

### Roll it out with the ceiling

As advice rather than a requirement: roll a gate out together with the
workspace's `[authority]` ceiling and roles, and re-dispatch running
sessions so their records carry the ceiling. Nothing depends on that order,
since a session dispatched before any ceiling existed records `none` and
keeps today's verdict.

## Migrating a workspace to a declared ceiling

Before adding an `[authority]` ceiling to a workspace that a gate already
reads, know how each session will be judged:

1. Sessions with no record keep today's verdict.
2. So do sessions whose record shows `grant_ceiling_source: none`, because
   they were dispatched before the declaration.
3. A session dispatched after the declaration is judged by its grant, so
   sessions that need to merge are dispatched with a role that holds
   `merge-pr`, such as `--role coordinator` in the example above.

Until gates honour the grant, merging still goes through the workspace hook
and its exemption list.

## Coordinators and successors

A successor coordinator gets its own grant by being dispatched with
`--role coordinator`, with no config edit between sessions. The record names
only the original parent, by dispatch id, and says nothing about who
supervises the child now. A coordinator reads its sessions' postures through
`niwa list --json`, or `niwa authority show --instance <name>`, and predicts a
gate's answer by running the same reference checks with `-C` set to the
instance root.

## What the record does and doesn't guarantee

The record states what was resolved at dispatch. It doesn't follow later
policy changes, and a wider grant needs a new dispatch.

It isn't tamper-proof. A dispatched session runs as the operator and can edit
its own record, the workspace state that names the overlay, and its own
parent link (by dispatching from outside its instance, it records no
parent). `parent_dispatch_id` isn't a security boundary, and later
re-delegation work must not treat it as one. A session that corrupts its own
record blocks its own child dispatches, since a malformed dispatcher record
refuses the dispatch.

What it does promise is that nothing gets weaker than today: every way a
lookup can fail reads as not held and as no record, so a gate written to
this contract is never more permissive than the gate it replaces.
