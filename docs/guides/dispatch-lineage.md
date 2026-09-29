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
| `niwa.workspace` | The `name` in the workspace's `.niwa/workspace.toml` | The configuration couldn't be read |

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
