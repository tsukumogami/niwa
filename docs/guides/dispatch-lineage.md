# Dispatch lineage

Every worker `niwa dispatch` launches for Claude Code carries lineage resource
attributes on its OpenTelemetry telemetry, so its metrics and events can be
traced back to the dispatch that made it. This guide says what they are, where
their values come from, how they reach the worker, and what niwa does with the
resource attributes you already set.

## The attributes

| Attribute | Value | When it's left out |
|---|---|---|
| `niwa.dispatch.id` | A fresh version-4 UUID per dispatch | Never, for a Claude worker |
| `niwa.dispatch.slug` | The `--name` slug, with `_` written as `-` | No `--name`, or nothing usable in it |
| `niwa.parent.session.id` | The id of the session that ran `niwa dispatch`, from `CLAUDE_CODE_SESSION_ID` | Dispatched from a plain shell, or the value isn't id-shaped |
| `niwa.requested.skill` | The `--skill` value, `<plugin>:<name>` | No `--skill` |
| `niwa.brief.id` | `sha256:` and the hex digest of the `--brief` file's content, or of the prompt when there's no `--brief` | Never |
| `niwa.workspace` | The `name` in the workspace's `.niwa/workspace.toml` | The name doesn't match the value pattern below. A configuration that can't be read refuses the dispatch before launch, so it never reaches this point |
| `niwa.authority.level` | The dispatch's resolved authority level: `autonomous`, `supervised` or `strict` | Never, for a Claude worker |
| `niwa.authority.grant` | The granted authorities in `merge-pr`, `close-issue`, `cut-release` order, joined with `/` (`merge-pr/close-issue`), or `none` | Never, for a Claude worker |

The two authority attributes are observability only. The record dispatch
writes into the instance before the worker starts is what anything that
decides on a grant reads; the grant joins with `/` because a comma would split
the attribute list.

An attribute that has no value is left out, never sent empty. niwa never guesses
one: with no `--skill` there is no requested skill, and the parent is never
inferred from the process tree.

Every value must match `^[A-Za-z0-9._:/@-]{1,128}$`. A value that doesn't is
dropped, and `niwa dispatch` names the attribute (never the value) on stderr.
The workspace attribute is the configured name, the same on every machine and
every instance; it isn't the instance's directory name or a local alias the
workspace was initialized under.

## The flags

- `--brief <path>` names the file holding the worker's brief. It must be a
  readable regular file of at most 1 MiB inside the workspace, symlinks
  followed; anything else refuses the dispatch before an instance is created.
  Only its content digest leaves the machine, never its path or content. The
  digest identifies the file as it was at dispatch time.
- `--skill <plugin>:<name>` names the skill the brief asks the worker to run.
  A value of another shape refuses the dispatch before an instance is created.

The `/dispatch` skill niwa installs at the workspace root passes `--brief` with
the brief file it writes, and `--skill` when the brief names a skill.

## How the attributes reach the worker

Claude Code reads every resource attribute from one variable,
`OTEL_RESOURCE_ATTRIBUTES`, a comma-separated `key=value` list. Whichever layer
sets it last replaces the whole list. A background worker is started by Claude
Code's background daemon, so the environment `niwa dispatch` runs in isn't a
dependable way to reach it; the settings document niwa passes with
`--settings` is.

niwa therefore composes one value and sends it twice, with identical content:
as `env.OTEL_RESOURCE_ATTRIBUTES` in the launch's `--settings` document, and in
the environment of the process it starts. Whichever of the two Claude Code lets
win, the worker ends up with the same list. The daemon keeps the settings
document with the worker's launch flags, so the attributes come back when the
daemon resumes the worker.

The same `env` object also carries `NIWA_REAP_KEY` and any names the workspace
passes through with `[session.dispatch_env]`; see
[Passing variables through to dispatched workers](session-env-passthrough.md).

The tests hold this without depending on which order Claude Code picks. The
`internal/otelprecedence` package works out the worker's value from a recorded
launch and the user settings file three ways: settings first, launch
environment first, and no launch environment. The dispatch tests and the
scenarios in `test/functional/features/dispatch-lineage.feature` require every
niwa attribute and every seeded user entry under all three, and a launch that
drops either copy fails at least one of them.

## Your own resource attributes are kept

If your Claude Code user settings (`settings.json` under `CLAUDE_CONFIG_DIR`,
or `~/.claude/settings.json`) set `env.OTEL_RESOURCE_ATTRIBUTES`, or the shell
you dispatch from exports it, niwa carries those entries forward:

1. the entries from your user settings, in their order, byte for byte;
2. then each entry from the dispatching environment whose key your settings
   don't already set;
3. with any entry in the `niwa.` namespace removed from both;
4. then niwa's own entries.

niwa reads only that one key of the settings file and never prints it. If the
inherited value can't be carried safely (the settings file isn't valid JSON,
the key isn't a string, or an entry isn't a single `key=value` pair of
printable ASCII without `,`, `;`, `\` or `"`, with key and value at most 255
characters), niwa sends no attributes at all, prints a warning that quotes
nothing, and the dispatch still goes ahead. The worker then gets exactly what
it would have had without niwa: your attributes, and no lineage.

What this means for what you set:

- Inherited attributes now reach the worker's command line, the background
  daemon's saved job state, and the worker's exported telemetry. They must hold
  no secret, as they already must for any telemetry.
- The slug and the workspace name are exported.
- A managed settings value for `OTEL_RESOURCE_ATTRIBUTES` wins over niwa's.

## Sessions niwa doesn't launch

A session you start yourself, at a workspace root or in an instance, or one a
SessionStart hook provisioned an instance for, gets no lineage attributes.
niwa has no launch argument to add to it, and writing the variable into the
instance's project settings would replace your own attributes with a copy that
goes stale when yours change.

A session you reopen yourself with plain `claude --resume`, outside the daemon,
starts a new process without niwa's settings document and doesn't carry the
attributes either.

## Seeing a dispatch's identity

`niwa list --json` shows `dispatch_id` and, when there was one,
`parent_session_id` for an instance whose dispatched session is still mapped.
Once the instance is gone, the lineage record below still has it.

## The lineage record

Every dispatch that keeps its instance also writes one lineage entry, named by
its dispatch id, into `<workspace root>/.niwa-lineage/`. The store sits outside
every instance and outside the workspace's `.niwa/` configuration directory, so
nothing that tears an instance down touches it: a reap of any kind, `niwa
destroy` with or without `--keep-session`, `niwa reset`, a failing on-reap
command and `niwa apply` all leave entries as they were. Entries last until the
workspace is destroyed; `niwa destroy --workspace` removes the store with it. A
workspace that never dispatched has no store.

An entry records the dispatcher at dispatch time and never changes after the
worker's session id is filled in. Re-parenting a session by message, or a
coordinator handing its work to a successor, doesn't show up in it.

### How each link was established

An entry carries two parent links, each with the id niwa found and how it came
by it, and the person the dispatch acts for:

| Source | Meaning |
|---|---|
| `observed` | niwa checked the link against its own records |
| `handed` | the dispatcher supplied it and niwa took it as given |
| `mapped` | niwa found it through a session mapping, without checking that the dispatcher owns it |
| `unverified` | the claimed session didn't match the mapping niwa holds for the dispatcher's instance; `mapped_session_id` names the one it does hold |
| `none` | there is no parent |

Only `observed` counts as observed. Treat every other value as not observed,
including a value this niwa doesn't know yet. `niwa lineage show` prints
`(not observed)` after each of them.

What to expect in practice:

- A person's own interactive session has no mapping, so a first-level
  dispatch's parent session is `handed`. So is the parent session of a
  dispatch from an imported session, or from a session whose mapping carries
  no dispatch id; that dispatch's parent dispatch is `none`.
- `--parent-from` makes both links `handed`; the parent dispatch link is
  `none` when the anchored instance has no record.
- A worker a coordinator dispatches from the workspace root links to the
  coordinator as `mapped`, not observed: the root has no record of its own,
  so niwa finds the coordinator through the mapping for the session id the
  variable carries. An imported session whose id collides with a local mapping
  gets the same `mapped` link, never `observed`.
- `unverified` is expected after a resume and for a Codex worker's
  dispatches, since neither keeps the session id niwa mapped. The parent
  dispatch link is the one to follow for the tree.
- A resumed Claude session that got a new session id keeps its record under
  the original id.

`observed` means the session id and working directory the caller claimed
matched niwa's records. A process running as the same user that reads the
mapping names can arrange both, so `observed` says the records agree, not who
ran the command.

### The person a dispatch acts for

The principal is empty until you set it in your personal niwa config,
`$XDG_CONFIG_HOME/niwa/config.toml` (`~/.config/niwa/config.toml` when
`XDG_CONFIG_HOME` is unset):

```toml
[global]
principal = "dana@example.com"
```

A value is 1 to 128 letters, digits, `.`, `_`, `-`, `@` or `+`; any other value
refuses every dispatch on the machine. A dispatch with no parent dispatch
records it as `declared` with trust `observed`. A dispatch whose parent entry
can be read inherits the parent's principal, an empty one included, as
`inherited`. Its trust stays `observed` only when niwa observed every hop: the
parent dispatch link is `observed`, the parent session isn't `unverified`, and
the parent's own principal trust is `observed`. Anything else, such as a
worker a coordinator dispatches from the workspace root (a `mapped` link) or
one dispatched with `--parent-from`, gets trust `handed`. A parent link whose
entry can't be read gives the declared principal, or none, with trust
`handed`. A worker can read its own principal with
`niwa lineage show` or `niwa authority show` from its instance, and passes it
on by dispatching from there.

The declared principal is machine-wide: every workspace on the machine records
the same one, any process running as you can read it, and it follows whichever
personal config the environment points niwa at. Clearing it doesn't change
entries already written. niwa never derives it from a login, a token or the
operating-system account, and never adds it to the worker's telemetry.

### What the record is not

Any process running as the same user can edit the store, so every entry is
advisory: use it to understand a tree, not to decide what a session may do.
The authority record is what a gate reads (see
[Authority levels and named grants](authority-grants.md)).

Lineage never travels with a session export or import; an imported session
brings no entries with it.

Writing and reading lineage touches only local files: it makes no network
call and needs no workflow engine on `PATH`.

Keep `.niwa-lineage/` out of anything published from the workspace root. A
workspace root under version control needs this ignore line:

```
.niwa-lineage/
```

### `niwa lineage show`

```sh
niwa lineage show [<dispatch id>] [--session <id> | --instance <name> | -C <dir>] [--json]
```

`show` prints an entry and then each ancestor reached through its parent
dispatch ids, nearest first. Unlike `niwa authority show`, it takes the
dispatch id as a positional argument. At most one selector applies:

- a dispatch id, the entry with that id;
- `--session <id>`, the entries whose worker session id it is;
- `--instance <name>`, the entries for that instance name;
- with none of these, the instance holding `-C`'s directory or the working
  directory, by its authority record's dispatch id. When that instance has no
  record, or a record that exists but can't be read, `show` falls back to
  matching its instance name and prints `selected by instance name`.

`--session` and `--instance` may match more than one entry, an older entry
for a reused session id or instance name among them. The chain starts from
the latest written, ties going to the lower dispatch id, and the other matches
are listed. Neither ever matches an entry of a later version.

The walk stops at an entry with no parent (`root`), a parent with no entry
(`not-on-record`), an entry that can't be read (`unreadable`), one of a later
version (`later-version`), an id already printed (`cycle`) or 64 levels
(`depth`). A tool reading the stop treats any value it doesn't know as not
`root`.

Exit codes: 0 printed, including a selected entry of a later version; 1 no
lineage entry, an unreadable selected entry, a bad `-C`, an unreadable store or
outside a workspace; 2 usage.

`--json` prints one `niwa.lineage.chain/v1` document:

```json
{
  "schema": "niwa.lineage.chain/v1",
  "chain": [
    {
      "schema": "niwa.lineage.entry/v1",
      "dispatch_id": "6f1d1a52-3c0e-4b7e-9a3f-2f1f8d6f0a11",
      "instance": "tsuku+fix-cache-1a2b3c4d",
      "agent": "claude",
      "origin": "dispatch",
      "written_at": "2026-10-23T18:02:11Z",
      "session_id": "b0b0b0b0-1111-4111-8111-b0b0b0b0b0b0",
      "parent_session": {"id": "a0a0a0a0-1111-4111-8111-a0a0a0a0a0a0", "source": "observed"},
      "parent_dispatch": {"id": "0c5e2b44-7d1a-4f3e-8b2c-5a4d3c2b1a09", "source": "observed"},
      "principal": {"name": "dana@example.com", "source": "inherited", "trust": "observed"}
    }
  ],
  "stop": "not-on-record",
  "matched": 1,
  "matches": ["6f1d1a52-3c0e-4b7e-9a3f-2f1f8d6f0a11"],
  "instance_name_fallback": false
}
```

Each readable level is the entry exactly as stored, members this niwa doesn't
know included. A level of a later version is `{"dispatch_id", "schema",
"known": false}`; one that can't be read, or isn't on record, is
`{"dispatch_id", "error"}`. `session_id` is absent until the worker's session
is known, and a `parent_session` link that is `unverified` also carries
`mapped_session_id`.

### `niwa lineage list`

`list` prints every entry in the workspace, latest written first, one line
each: the dispatch id, the worker session id, the parent session with its
source, and the principal. An entry that can't be read lists as its id and
error, and the rest still list. `--json` prints one `niwa.lineage.list/v1`
document, `{"schema": "niwa.lineage.list/v1", "entries": [...]}`, each entry
shaped like a chain level above.

Exit codes: 0 listed (nothing, when there's no store); 1 the store can't be
read or outside a workspace; 2 usage.

## Other agents

Codex dispatches are unchanged: a Codex launch has no settings document to
carry the attributes, and whether its telemetry reads them from the launch
environment is unverified. The capability table in `codex-agent.md` lists this
as a row niwa hasn't built.

## The live check

`test/live/lineage_live_test.go` checks the delivery on a real machine. It
starts a local OTLP/HTTP listener that records attribute keys only, launches a
throwaway background worker whose settings document sends its telemetry there,
and reports each lineage key by name and your own keys as a count. An opt-in
arm stops the background daemon and resumes the worker without settings, to
check that the daemon kept them. Run it after upgrading Claude Code:

    NIWA_LIVE_PROBE_PARENT=<a directory Claude Code trusts> \
      go test -tags live -count=1 -run TestLineageAttributesReachBackgroundWorker -v ./test/live/

The check last passed on Claude Code 2.1.284, both arms, with an on-demand
daemon (no installed background service); no claim is made for an installed
service. The restart arm stops every background session on the machine.
