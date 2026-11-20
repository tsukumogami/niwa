# A brief's first read

Some briefs are meant to be read in stages: a short opening section first
(who the worker is, what it checks before anything else), and the rest only
after that. Writing that rule into the brief doesn't hold. The worker picks
its first command before it has read any of the file, and a common first
command is a checksum chained with a whole-file read:

```bash
sha256sum brief.md && cat brief.md
```

Once that runs, the whole brief is in the worker's context, and the rule
arrives in the same call that broke it. Moving the rule higher up the file,
or repeating it in the prompt, doesn't change the order of events.

`--first-read` takes the rule out of the brief and puts it where it can act
before the worker's first call.

## Declaring it

```bash
niwa dispatch "Read and follow your brief: .niwa/dispatch-briefs/lane.md" \
  --brief .niwa/dispatch-briefs/lane.md --first-read 3 --detach
```

`--first-read N` says the worker reads lines 1-N of the brief, on their own,
before it reads any more of it. The bound is a line count rather than a text
marker: a range like `sed -n 1,/STOP/p` ends at the first line that contains
the word anywhere, which can come well before the boundary you meant.

The dispatch refuses, before it creates anything, when:

- `--first-read` is given without `--brief`, or with a count below 1;
- N is the brief's whole length or more, so it bounds nothing;
- the worker's agent can't carry the guard. Today that is every agent but
  Claude Code, since the guard travels in the launch's settings document.

## What the worker sees

The dispatch records the brief's resolved path and N in the new instance, as
`.niwa/first-read.json`, and the launch carries a PreToolUse hook,
`niwa instance first-read-guard`, matched on Bash, Read and Grep.

The guard is meant to run beside the hooks in the instance's own settings, not
in place of them. Claude Code's hooks reference says hook entries "merge across
settings levels rather than replacing each other", but that sentence names
user, project and local settings, not the `--settings` flag. A live dispatch
with `--first-read` settled it: the first-read guard refused a whole read of
the brief, and in the same session the instance's own worktree guard refused
a `git worktree add`, so both sets of hooks ran.

Until the first read has run, the guard looks at every call that names the
brief:

| Call | Before the first read |
|------|-----------------------|
| `sed -n 1,Bp <brief>` with 1 <= B <= N, alone | allowed, and counts as the first read |
| `head -n K <brief>` (or `-nK`, `-K`) with K <= N, alone | allowed, and counts as the first read |
| Read with a `limit` of at most N and no `offset` past line 1 | allowed, and counts as the first read |
| `sha256sum <brief>` or `shasum -a 256 <brief>`, alone | allowed, and doesn't count |
| anything else naming the brief, any of the above chained, piped or redirected included | refused |
| a call that doesn't name the brief | allowed |

A bounded read has to start at line 1. A range that begins later, such as
`sed -n 3,3p` or a Read with an `offset` of 3, is refused: it would mark the
brief read while skipping the opening lines the declaration exists to put
first.

A refused call shows the worker the command that satisfies the rule, for
example:

```
niwa: this dispatch's brief declares a first read of lines 1-3, and this call
reads it some other way. Run `sed -n 1,3p '/ws/.niwa/dispatch-briefs/lane.md'`
as a call of its own, with nothing chained to it; once it has run, the brief
can be read freely. A `sha256sum '/ws/.niwa/dispatch-briefs/lane.md'` on its
own is allowed before it.
```

Once a bounded read has run, the guard records it in
`.niwa/first-read.done` and allows everything from then on. It only governs
the first read: what the worker does between that read and the rest of the
brief is still up to the brief's own text.

## Limits

The guard steers a worker; it isn't a security boundary. It recognizes the
brief by resolved path in a Read or Grep. In a Bash command it first looks
for the brief's file name anywhere in the command text, and it reads simple
commands only, so a brief reached through a variable, a glob or a nested
shell isn't recognized. Because that first check is a plain text match, a Bash
call whose text contains the brief's file name, as part of another path or
inside a longer word, is refused until the first read has run.

It fails open. A payload it can't parse, an instance with no declaration, or
a declaration it can't read lets the call through.

The hook runs the niwa binary that ran the dispatch, by its absolute path,
not whichever `niwa` comes first on the worker's `PATH`. The hooks niwa writes
into settings files resolve `niwa` from `PATH` so they follow upgrades, but
this one lasts a single session, and an older niwa on `PATH` that lacks the
subcommand prints its help and exits 0, which Claude Code reads as "allow".
If the dispatching binary is removed while the worker runs, the hook fails
and the call goes ahead.
