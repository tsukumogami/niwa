# Authority level

A session launched by `niwa dispatch` reaches decisions as it works: whether to
merge, whether to accept a review, whether to take a step that's hard to undo.
The authority level says who settles those decisions. The workspace declares
it, a machine can narrow it, a single dispatch can narrow it further, and the
session asks `niwa authority decide` when it reaches a decision point.

The level doesn't change what a session is allowed to do with its tools. That's
the permission mode's job (see `docs/guides/dispatch-permission-mode.md`), and
the two never affect each other.

## The three levels

From widest to narrowest:

| Level | Who decides a decision point | Author excluded from deciding |
|---|---|---|
| `autonomous` | The session that reached it | No |
| `supervised` | The coordinator, the session that dispatched it | Yes |
| `strict` | A person | Yes |

`autonomous` is the default. It's what every dispatched session already does,
so a workspace that sets nothing sees no change: every decision point answers
`session`.

The author-exclusion value says whether whoever produced the work may also be
the one to approve it. It's reported for each point so a caller can act on it;
niwa doesn't enforce it.

## Setting the level

The workspace declares its level under `[workspace]` in
`.niwa/workspace.toml`:

```toml
[workspace]
name = "myws"
authority_level = "supervised"
```

Only the base workspace config can set it; an overlay can't.

A machine sets a ceiling under `[global]` in your personal niwa config,
`~/.config/niwa/config.toml` (or `$XDG_CONFIG_HOME/niwa/config.toml` when
`XDG_CONFIG_HOME` is set):

```toml
[global]
authority_level = "strict"
```

You can write it from the command line instead:

```
niwa config set authority-level strict
```

prints `Authority level set to strict in <path>`, and

```
niwa config unset authority-level
```

prints `Authority level removed.`, or `No authority level set.` when there was
nothing to remove. `niwa config set` checks the value before touching the file
and leaves it unchanged when the value is invalid.

A single dispatch narrows its own level with a flag:

```
niwa dispatch "<task>" --authority strict
```

Each of the three settings accepts only `autonomous`, `supervised` or `strict`,
lowercase, after trimming surrounding whitespace. An empty or whitespace-only
value means not set. Anything else is a configuration error, never a quiet
fallback: `Strict`, `STRICT` and `loose` are all rejected, and a workspace
config that holds one fails to load, which stops `niwa dispatch`, `niwa apply`
and both `niwa authority` commands.

## How a dispatch's level is resolved

A dispatch runs at the narrowest of three values:

1. the workspace level, or `autonomous` when the workspace sets none;
2. the machine ceiling, when it's set;
3. the `--authority` flag, when it's given.

The narrower of the first two is the ceiling. A `--authority` value wider than
the ceiling is refused before any instance is created, with exit code 1 and a
message naming the requested level, the ceiling and where the ceiling came
from, so you know which file to change. It's refused rather than clamped so
nobody believes a dispatch got room it didn't get.

The level is reported with its source: `flag`, `machine`, `workspace` or
`default`. When two settings hold the same level, the source is the first of
that list, the most specific setting that holds it.

This order differs from the permission mode's on purpose. For
`dispatch_permission_mode`, the machine value replaces the workspace's in
either direction, because how much a worker may do unattended on your machine
is your call. The authority level is the workspace's statement about who
settles its decisions, so a machine may narrow it and never widen it: a machine
set to `autonomous` in a `strict` workspace still dispatches at `strict`. The
permission mode is resolved exactly as before and nothing in it reads the
level. A dispatch with `--permission-mode bypassPermissions` at the `strict`
level launches in bypass mode, and every decision point still answers
`person`.

A workspace config that can't be loaded for any reason, including a TOML
syntax error, refuses the dispatch before any instance exists, so the level
can't fall back to the default because the file was unreadable.

## Reading it back

Every dispatch that launches a worker prints one stderr line saying what it
resolved:

```
niwa dispatch: authority level strict from workspace
```

A refused dispatch prints its refusal instead.

Dispatch writes the level and source into the instance, at
`<instance>/.niwa/authority.json`, before it launches the worker, and into the
session mapping once the session is captured. `niwa list --json` shows them as
`authority_level` and `authority_source` for each instance whose newest
mapping carries them, and leaves both out for an instance whose mapping was
written before this feature.

A Claude Code worker also carries `niwa.authority.level=<level>` among its
telemetry resource attributes (see `docs/guides/dispatch-lineage.md`). A Codex
worker gets no telemetry attribute, but its session mapping records the level
the same way.

To see what a dispatch would get before running one, run `niwa authority show`
inside the workspace:

```
workspace: supervised
machine: strict
default dispatch: strict from machine
```

The first two lines print `not set` for a setting that's absent. The last is
what a dispatch with no `--authority` flag resolves to. Outside a workspace it
exits 1.

## Asking about a decision point

A session that reaches a decision point asks:

```
niwa authority decide merge
```

It prints exactly five lines:

```
point=merge
level=strict
decider=person
author_exclusion=on
source=flag
```

and exits:

| Exit | Meaning |
|---|---|
| 0 | The decider is `session`: settle the point yourself |
| 3 | The decider is `coordinator` or `person`: someone else settles it |
| 2 | Usage error: a missing or malformed point name, or a malformed `--session` |
| 1 | Outside a workspace, or an invalid workspace or machine value, or a malformed record |

Exit 3 prints only the five lines, with nothing on stderr, because it's an
answer rather than a failure. Treat every exit other than 0 as "not yours to
settle". A caller that only proceeds on 0 stays safe whichever lookup answered
and whatever goes wrong.

A point name is a lowercase letter followed by up to 63 lowercase letters,
digits or dashes: `a`, `merge` and `merge-2` work; `Merge_Now`, `2merge` and
`-merge` exit 2. The name is checked before anything else, so a bad one exits
2 even outside a workspace. Every well-formed name is accepted and, today,
answered from the level alone.

### Whose level it uses

`decide` takes the first of these that exists:

1. The session mapping for the calling session, when it carries a level. The
   session id comes from `--session <id>` when given, and otherwise from
   `CLAUDE_CODE_SESSION_ID`, which Claude Code sets for its sessions. A
   `--session` value that isn't a session id exits 2; an environment value that
   isn't one is ignored.
2. The authority record of the instance containing the working directory.
   Dispatch writes it before the worker starts, so a session that asks before
   its mapping exists, or an agent that sets no session id, still gets its
   dispatch's level.
3. The level a dispatch with no flag would get, the same one `niwa authority
   show` prints, with `(no dispatch record)` after the source:
   `source=workspace (no dispatch record)`. This covers sessions niwa didn't
   dispatch, a shell at the workspace root, and instances already reaped.

A mapping or record that exists but holds anything other than a valid level
and source exits 1. It never falls through to the next step, which could
answer wider than the dispatch recorded.

`decide` reads a handful of local files, opens no network connection and
starts no process. A unit test walks every function its handler can reach and
fails on any reference to a network or subprocess package.

## Limits

- **The answer is advisory and unauthenticated.** A session can ignore it,
  pass another session's id, or edit its own instance's files. Within one
  operator's workspace that's no escalation, since anything running as you
  already has your rights. The level tells a cooperating session what to do;
  it doesn't stop an uncooperative one.
- **Nested dispatch isn't capped at the parent's level.** A session dispatched
  at `strict` that runs `niwa dispatch` itself gets the workspace and machine
  ceiling, not its parent's `strict`.
- **After reap only telemetry remains.** Reaping an instance deletes its
  authority record and session mapping. A question asked afterwards falls back
  to the configuration with `(no dispatch record)`, and the telemetry
  attribute is best-effort, not an audit log.
- **`coordinator` names a role, not a session.** Finding the dispatching
  session and getting the question to it is left to the caller.

A per-point declaration, where an organization names who decides a specific
point, can plug in behind `decide` later without changing its output or exit
codes; it can only ever make an answer narrower than the level allows.
