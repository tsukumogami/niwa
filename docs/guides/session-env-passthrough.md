# Passing variables through to dispatched workers

`[session.dispatch_env]` lets a workspace move a few named variables from the
shell that runs `niwa dispatch` into the worker it launches, and keep others
out of it.

```toml
[session.dispatch_env]
passthrough = ["BUILD_LABEL"]   # copied from niwa's environment when set
unset = ["PARENT_HANDLE"]        # removed from the worker's environment
```

## Read at dispatch, not at apply

`[session.env]` is resolved when the workspace is applied and written into the
generated settings files, so every session started in an instance sees the same
values. `[session.dispatch_env]` is different: niwa reads it when it launches a
worker, and takes each value from its own environment at that moment. Nothing
that runs at apply reads it, so declaring only these lists writes no env block
anywhere.

That makes it the place for values that belong to one dispatch rather than to
the workspace: something the dispatching session knows about itself and wants
the worker to see.

## What the worker gets

For each name in `passthrough` that is set in niwa's environment, the worker
gets the same value:

- in the `env` object of the `--settings` document niwa passes to a Claude Code
  worker, next to the `OTEL_RESOURCE_ATTRIBUTES` lineage value and
  `NIWA_REAP_KEY` that niwa sets itself; and
- in the environment of the process niwa launches.

A Codex worker has no settings document, so it gets the process environment
only. A name that isn't set when the dispatch runs is skipped without a word,
and the dispatch goes ahead.

Each name in `unset` is removed from the worker's launch environment, detached
or not, so a value the dispatching shell carries never reaches a worker that has
nothing of its own to replace it with. A name that isn't set changes nothing.

`niwa watch` launches its review sessions through the same code path, so a
review gets the same pass-through, removal and reap key as a dispatched worker.

## What the load refuses

The configuration fails to load, with an error naming the entry, when either
list holds:

- a name starting `NIWA_` or `CLAUDE_`;
- `OTEL_RESOURCE_ATTRIBUTES` or `NIWA_REAP_KEY`, which niwa sets in the same
  `env` object itself;
- the same name twice, or a name in both `passthrough` and `unset`;
- a name `[session.env]` already declares in `vars` or `promote`;
- a name that looks like a credential: ending `_KEY`, `_APIKEY`, `_SECRET`,
  `_TOKEN`, `_PAT`, `_PASSWORD`, `_PASSWD` or `_CREDENTIALS`, or starting
  `AWS_`, in any letter case;
- something that isn't a variable name.

## Declare only what the worker needs

Pass-through values ride the worker's command line, inside the `--settings`
argument. Other users of the host can read a command line in a process listing,
and Claude Code's background daemon keeps it with the worker's saved launch
flags. So:

- declare only the variables a worker actually needs;
- never declare a credential. The name check above catches the common shapes,
  but it can't catch every secret (a `DATABASE_URL` with a password in it, for
  one); what keeps credentials out is that the workspace names each variable
  exactly.

Values in `unset` never reach a command line; they're only removed.

## See also

- [Ephemeral session instances](ephemeral-session-instances.md#the-reap-key-and-the-on-reap-command)
  for `NIWA_REAP_KEY` and the `[session.on_reap]` command, whose own `env` list
  takes the same refusals.
- [Dispatch lineage](dispatch-lineage.md) for the `OTEL_RESOURCE_ATTRIBUTES`
  value that shares the settings `env` object.
