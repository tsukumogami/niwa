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

None of this needs koto, the workflow engine for coding agents. A host with niwa alone declares, records and reads
authority, and no part of the record or the lookup reads anything koto
writes. The existing Test job in CI runs the authority unit tests and the
functional suite, the gate-contract scenarios for this guide among them, and
a CI-only test, `TestCI_AuthorityRunsWithoutKoto`, asserts that no koto is on
PATH while it does.

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
| `cut-release` | publishing a release or a release tag, dispatching a repository's release workflow, and the host installs a release run makes after its own cut |

Wherever niwa lists authorities it uses this order.

`cut-release` covers a release however it starts. A repository that
publishes through a release workflow is released by dispatching that
workflow, so the dispatch needs `cut-release` just as creating the release
directly does. The authority also covers the installs a release run makes on
the host once its own release is published, such as reinstalling the
released tool and re-running `niwa apply`, and only those: an install a
session runs outside a release keeps whatever rules applied to it before. A
gate doesn't read those installs as a release; the release run that makes
them decides whether to ask a person by its session's grant.

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
level in a config file means not set; a blank level typed on `--level` or
given to `niwa config set authority-level` is refused with exit 1. An invalid
value (an unknown level, a role or authority
name failing the name pattern, an unknown key inside `[authority]`, an
unknown `authority_*` key under `[global]`, a value of the wrong type)
refuses `niwa dispatch` and
`niwa authority policy` with exit 1 and a message naming the key, the value
and the accepted values. `niwa authority show` and `decide` refuse the same
way, with exit 1, when there's no record and they read the config to give
the no-record answer; with a record they don't read the config. `niwa apply` prints the same message as a warning
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
refused. The grant is the union of everything requested. A blank `--level`
(`--level ""`) is refused too, rather than read as no flag; leave the flag out
to take the level ceiling.

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

### Never wider than the dispatcher

A dispatch also can't grant its child a named authority its dispatcher
doesn't hold. The dispatcher is found the way the parent link is:

- Inside an instance (the working directory's, or the one `--parent-from`
  names), it's that instance's dispatch, and its record's grant is the most
  the child may get. A record that grants nothing, or one in a later schema
  version, passes on nothing. An instance with no record that a
  dispatch-written session mapping still names was dispatched and lost its
  record, so it passes on nothing too.
- At the workspace root there's no instance to read, so the dispatcher is the
  calling session, when the session id variable names a mapping dispatch
  wrote: that dispatch's record caps the child, and if the record is missing,
  can't be read or names another dispatch, the child gets nothing. A Codex
  worker inherits its dispatcher's variable, so this may name an ancestor,
  whose grant is at least as wide as the caller's.
- With neither (a directory in no instance and no such mapping, or an
  instance made by `niwa create` that has no record), only the grant ceiling
  limits the child.

A session started by hand inside a dispatched instance runs from that
instance, so its own dispatches are capped by the instance's record. The
check runs with the ceiling, before the prompt, the sweep and provisioning,
and again with the late re-check, and the refusal names each authority, how
it was asked for and what the dispatcher holds:

```text
niwa: error: authority close-issue (requested by --grant) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch 3c803591-4c01-486f-a927-f96ec399d6a7)
niwa: error: authority merge-pr (requested by --grant) is wider than the dispatcher's grant: the dispatcher's record is missing or can't be used, so it holds none (dispatch 3c803591-4c01-486f-a927-f96ec399d6a7)
```

Every line of this refusal contains `is wider than the dispatcher's grant:`,
which no other refusal does, so a caller that reports it can match on that.

`--parent-from` only chooses which instance is the dispatcher; it never adds
to what may be passed on. Run from outside every instance by a session whose
own mapping names a dispatch, it's capped by both the named instance and that
dispatch, so naming a wider instance, or one nothing dispatched, can't lift
the caller's cap.

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
instance someone made by hand is still the dispatcher's own. With nested
workspaces, the lookup takes the outermost instance that holds the
directory, so a dispatch run inside an inner instance records the outer
instance as its parent. An instance that has no record gives no parent. A record that exists but can't be read or
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
started by hand has no record of its own and holds no grant; what it
dispatches is capped by the record of the instance it runs in, when that has
one. An instance from `niwa create` has no record, and no cap on what's
dispatched from it beyond the grant ceiling.

### The document

```json
{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "parent_dispatch_source": "none",
  "parent_session_source": "none",
  "principal": {"name": "", "source": "none", "trust": "observed"},
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
| `parent_session_source` | how niwa came by `parent_session_id`: `observed`, `handed`, `mapped`, `unverified` or `none`, as in the lineage record ([Dispatch lineage](dispatch-lineage.md#the-lineage-record)) |
| `principal` | the person the dispatch acts for, `{"name", "source", "trust"}`, the same value its lineage entry holds |
| `level`, `level_source` | the resolved level and where it came from (`flag`, `machine`, `overlay`, `workspace`, `built-in`) |
| `level_ceiling`, `level_ceiling_source` | the level ceiling and its source |
| `grant_ceiling`, `grant_ceiling_source` | the grant ceiling and its source: `machine`, `overlay`, `workspace` or `none` |
| `roles` | the roles granted, in name order |
| `grant` | each held authority with every source it came from: `dispatch` for `--grant`, `role:<name>` for a role, `dispatch` first |
| `unknown_grant` | entries this niwa can't hold, same shape, never held |

A reader also reports `newer-version` as all three sources for a record
written in a later schema version (see [Versions](#versions)); dispatch never
records it.

`parent_session_source` and `principal` are advisory: no gate reads them. A
record written before niwa added them, or one whose value has the wrong type
or shape, reads with them unrecorded rather than malformed, so neither can
void a grant. Plain `show` prints `unrecorded` for them, and `--json` leaves
them out.

`level_source` describes the resolution at dispatch, and can differ from
`level_ceiling_source` in a valid record: when provisioning brings in an
overlay at the same level, the record keeps the level and its source as the
stderr line printed them, while the ceiling's source names the overlay the
instance was built from.

`grant_ceiling_source` is `none` only when no layer declares a ceiling.
`"grant_ceiling": [], "grant_ceiling_source": "none"` means the workspace
never opted in; `"grant_ceiling": [], "grant_ceiling_source": "workspace"`
means it declared `ceiling = []`, so no session may hold anything. Plain
`show` prints those two as `grant ceiling: (empty) from none` and
`grant ceiling: (empty) from workspace`, the same way `niwa authority policy`
prints them.

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

`source=` is the level's source, or `declaration` when the point's own
declaration decides the answer and the level didn't narrow it. A declaration
the level overrides (a `strict` level over a point declared `session`, say)
keeps the level's source. With no record, `decide` answers from the level a no-flag
dispatch would get, adds ` (no dispatch record)` to `source=` and prints
`record=none`. It exits 2 for a point name outside `^[a-z][a-z0-9-]{0,63}$`,
checked before anything else. `--json` prints the same six fields as one
object. That object carries no schema string and isn't part of the versioned
record contract; the held and settle rules below read the text lines.

### `niwa authority check`

`show` and `decide` answer for an instance. `check` answers for a session:
whether it's the session its instance's dispatch launched, and what it holds.
A gate knows two things about the session it guards, the `session_id` in its
payload and its own path, which names the instance, so that's what `check`
takes:

```sh
niwa authority check --json --session <session id> -C <absolute dir> --point merge-pr --point close-issue
```

`-C` is a directory in the instance: its root, a repo inside it, or a niwa
worktree of it. Each `--point` asks who settles that decision point, decided
as `decide` decides it. The answer is one `niwa.authority.check/v1` document:

```json
{
  "schema": "niwa.authority.check/v1",
  "state": "bound",
  "binding": "mapping",
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "grant": [
    {
      "authority": "merge-pr",
      "sources": [
        "role:coordinator"
      ]
    }
  ],
  "level": "autonomous",
  "level_source": "workspace",
  "grant_ceiling_source": "workspace",
  "points": [
    {
      "point": "merge-pr",
      "decider": "session",
      "author_exclusion": "off"
    }
  ]
}
```

`state` is one of three values:

| State | Meaning | Carries |
|-------|---------|---------|
| `bound` | the dispatch's own session, with a usable record | `binding`, `dispatch_id`, `parent` when the record names one, `grant`, `level`, `level_source`, `grant_ceiling_source`, `points` |
| `record-unusable` | the dispatch's own session, but its record is missing (`record-missing`), can't be read or used (`record-malformed`) or names another dispatch (`record-other-dispatch`) | `reason`, `dispatch_id`, an empty `grant` |
| `not-dispatched` | any other session: no mapping (`no-mapping`), a mapping dispatch didn't write (`hook-mapping`), or one for another instance (`other-instance`) | `reason`, an empty `grant` |

A session is bound when the instance's record names the dispatch that one of
these ties the session to, checked in this order, and `binding` says which:

1. `mapping`: the session mapping dispatch wrote once it captured the
   worker's session id, naming this instance.
2. `lineage`: before the mapping exists, the dispatch's lineage entry naming
   this instance and this session id.
3. `records`: before dispatch has recorded the session id, and only while
   its capture is under way, the agent's own session records, with this
   session the only one claiming the instance. Dispatch marks the capture
   with `.niwa/capture-pending.json` from just before launch until capture
   has an outcome, and the records count only while the dispatch process that
   wrote it is still running.

So a worker is bound from its first command, and its `binding` moves from
`records` to `lineage` to `mapping` as dispatch goes on; decide on the state
and the grant, never on the binding. A session someone starts by hand in the
instance is never bound: it has no dispatch mapping, no lineage entry names
it, and it can match the records only while capture runs, beside the worker,
where the match is ambiguous and binds neither. A dispatched session whose
record goes missing reads `record-unusable`, never `not-dispatched`, so losing
the record can't buy it a wider answer. A record in a later schema version
binds as usual and holds nothing at level `strict`.

Without `--json` it prints a few lines for a session reading about itself:

```text
session: bound to dispatch 3c803591-4c01-486f-a927-f96ec399d6a7 (mapping)
grant: merge-pr (role coordinator)
level: autonomous from workspace
point merge-pr: decider session, author exclusion off
```

It exits 0 for every state. It exits 1 with nothing on stdout for a `-C`
that is empty, relative, missing or in no instance, a session id that isn't
a lowercase UUID, a session mapping that can't be read, and the two-second
watchdog; and 2 for a usage error, such as a point name outside
`^[a-z][a-z0-9-]{0,63}$`. It loads no config and writes nothing.

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
dispatched session's answer, as a gate does, also requires `record=present`:
with no record, `decide` answers from the level a no-flag dispatch would get,
which in an `autonomous` workspace is `decider=session` too, so that line alone
would settle a point for a session niwa never dispatched.

These are the reference checks. niwa's CI extracts this block from this
guide and runs it inside a stub gate against real `show` and `decide`
output and against every failure, whenever the existing Test job runs. That
job's path filter doesn't start it for a change that touches only docs, so an
edit to this guide alone isn't checked until a change to code or tests runs
the job. Copy the block as it is:

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

### A gate on the binding

A gate built on `niwa authority check` judges the session it guards, not the
instance. It keeps its own text match to spot a merge, a close or a release,
and for those only it makes one bounded call, with the payload's
`session_id`, its own directory and a `--point` per authority the command
needs, then decides by the answer:

| `check` answer | Each needed authority |
|----------------|-----------------------|
| `bound`, held, decider `session`, author exclusion `off` | allow |
| `bound`, not held | deny, naming what the session holds and the authority it needs |
| `bound`, held, decider `dispatcher` | deny, naming the level |
| `bound`, held, decider `person`, or author exclusion `on` | the verdict an instance off the exemption list gets |
| `record-unusable` | the verdict an instance off the exemption list gets |
| `not-dispatched`, or the lookup failed | the gate's verdict today |

A lookup failed when niwa can't be found, exits non-zero, runs past the
bound, or prints a document whose `schema` isn't exactly
`niwa.authority.check/v1` or whose `state`, authority, level, decider or
`author_exclusion` falls outside niwa's vocabularies. The grant never lifts a
verdict the gate reached without it: a command the gate can't read, or one
that also does something the grant doesn't cover, keeps its verdict.

A release workflow dispatch is a release. The gate reads `gh workflow run`
naming the repository's release workflow, and a `gh api` call other than a
GET to that workflow's `actions/workflows/<workflow>/dispatches` endpoint,
as needing `cut-release`, and decides it by the table above as it decides
`gh release create`. Which workflows count is the gate's to declare: by file
name, such as `prepare-release.yml` or `release-prepare.yml`, and by display
name, such as "Prepare Release", compared in any case. A dispatch whose
workflow the gate can't read (a numeric workflow id, a shell expansion, a
glob, a path, another character bash or GitHub would expand or decode
before matching, no name, or more than one) is asked for every
session, since the gate can't tell it isn't a release, and no grant lifts
that. So is a `gh api` call whose endpoint is wholly a shell expansion and
that sets a method other than GET or passes a body flag. Dispatches of other
workflows keep their verdict.

"The gate's verdict today" is the verdict for a command the gate already
gated. A release workflow dispatch is often one it didn't, so the gate
chooses that verdict when it adds the rule. For a release it asks a session
niwa didn't dispatch, a person's own included, rather than keep the allow it
gave before. That makes the gate stricter, never wider.

A gate that reads command text covers only the commands it reads, so the
table above isn't a full account of what `cut-release` guards. It stops a
mistake, and an injected instruction that types a release command, but a
session set on releasing can reach a release with no command the gate reads:
pushing a tag a workflow publishes from, `gh release edit --draft=false` on a
draft, a call to the REST releases endpoint, or a script file, a `gh` alias or
a program in another language that does any of these. The control that holds
against that is on the hosting side, such as a protected environment with
required reviewers on each release workflow.

A release tool can make the same bounded `check` call for its own session.
A held `cut-release` grant the session decides answers the tool's go: it
cuts, and runs the installs that follow, without asking a person, just as a
session holding `merge-pr` merges without asking. Any other answer leaves
the go with a person. That read is advisory. A session runs as the
operator and could edit its own record, so the answer can be stale or
forged, and the gate isn't a boundary against that either. The gate still
decides the release and the workflow dispatch it reads, so it, not the
tool's read, stays the control.

Such a gate judges a bound session by its grant whatever the record's
`grant_ceiling_source`. That supersedes the rule below for `none`, which
holds for gates that read by instance: a session dispatched before the
workspace declared a ceiling recorded `none` and holds nothing, so at a gate
on the binding it's denied a merge or a close until it's re-dispatched.

### Finding niwa from a hook

Every Claude settings document niwa writes (each repo's, the instance
root's and the workspace root's) carries `NIWA_BIN` in its `env`: the
absolute path of the niwa that wrote it. A hook tries `command -v niwa`
first and falls back to `$NIWA_BIN` when that names an executable, the order
niwa's own hook commands use, so a niwa upgraded on `PATH` is the one that
runs. `NIWA_BIN` is niwa's own name: a value the workspace's env tables
declare for it is replaced. It's added to the documents niwa writes for other
reasons and never makes niwa write one, so a repo with no settings document
of its own has none. The instance root's document is always written, so a
session launched at the instance root, as a dispatched worker is, runs its
hooks with `NIWA_BIN` set.

### A gate that reads by instance

A gate written with the reference checks above reads the instance's record,
whoever runs the command:

1. The gate keeps its own text match to spot a merge, a close or a release.
   The lookup runs only for commands that match.
2. It looks the grant up with `niwa_grant_decides`. For a session whose
   lookup printed `"record": true` with a `grant_ceiling_source` other than
   `none`, the grant decides: the action is allowed only when `niwa_holds`
   finds the authority and `niwa_settles` finds an exact `decider=session`
   line beside `record=present`, and it's denied otherwise, even for an
   instance on the gate's exemption list.
3. A `none` source (the workspace never declared a ceiling), `"record":
   false` and every failed lookup leave today's behaviour, exemption list
   included. A missing, old, hung or failing niwa leaves the gate exactly as
   strong as it is now.
4. A declared-empty ceiling has a declaring source, so the grant decides and
   denies. A record in a later schema version has source `newer-version` and
   holds nothing, so it denies too.

Rule 3's `none` case holds only for gates that read by instance. A gate on
the binding judges a bound session by its grant whatever its
`grant_ceiling_source`, as described above. A gate that reads by instance
also can't tell the dispatched session from one started by hand in the same
instance, so both get the instance's grant there.

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
with a background job otherwise, so a bash gate that sources it always has
a bound. A gate written some other way that can do neither (no `timeout`
and no background-job fallback) skips the lookup and keeps today's
behaviour.

The bound applies to each lookup, and the sketch above makes up to three
in a row (`niwa_grant_decides`, `niwa_holds`, `niwa_settles`), each a niwa
process start of about 10 ms when nothing stalls. Keep the bound times three
under the harness's hook timeout, or lower `NIWA_LOOKUP_SECONDS`.

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
sessions so their records carry the ceiling. At a gate that reads by
instance nothing depends on that order, since a session dispatched before any
ceiling existed records `none` and keeps today's verdict. At a gate on the
binding it does: such a session holds nothing, so re-dispatch any session that
needs to merge or close before the gate changes.

## Migrating a workspace to a declared ceiling

Before adding an `[authority]` ceiling to a workspace that a gate already
reads, know how each session will be judged:

1. Sessions with no record keep today's verdict.
2. At a gate that reads by instance, so do sessions whose record shows
   `grant_ceiling_source: none`, because they were dispatched before the
   declaration. At a gate on the binding they're judged by their grant,
   which is empty.
3. A session dispatched after the declaration is judged by its grant, so
   sessions that need to merge are dispatched with a role that holds
   `merge-pr`, such as `--role coordinator` in the example above.

Until gates honour the grant, merging still goes through a workspace's
existing merge hook, if it has one.

## Coordinators and successors

A successor coordinator gets its own grant by being dispatched with
`--role coordinator`, with no config edit between sessions. The record names
only the original parent, by dispatch id, and says nothing about who
supervises the child now. A coordinator that dispatches from the workspace
root passes `--parent-from` with a directory in its own instance, so its
children record it as their parent. It can pass on only what it holds
itself, and a successor dispatched with `--role coordinator` from a
coordinator holding less is refused. It reads its sessions' postures through
`niwa list --json`, or `niwa authority show --instance <name>`, and reads its
own with `niwa authority check --session "$CLAUDE_CODE_SESSION_ID" -C
<its instance root>`, the same call a gate on the binding makes.

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

The binding `check` reads is the same kind of evidence: session mappings,
lineage entries, the capture-pending file and the agent's session records
are all files the operator's processes can write. A session started by hand
in a granted instance can dispatch a child up to that instance's grant, and
one that leaves every instance with no mapped session variable dispatches
limited only by the ceiling.

What it does promise is that a failure never widens anything: every way a
lookup can fail reads as not held and as no record, so a gate written to
this contract falls back to exactly the verdict the gate it replaces would
have given. Only a positively read record changes the verdict, and then by
the grant: at a gate that reads by instance, only one from a workspace that
declared a ceiling; at a gate on the binding, only for the session bound to
it.
