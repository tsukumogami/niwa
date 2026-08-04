# Controlling a background Claude Code session

niwa dispatches work as background Claude Code sessions. This guide records how those
sessions behave when you try to reach one after it is running — what works, what fails,
and what fails while looking like it worked.

**Every claim carries the command that produces it.** These are claims about a CLI that
changes, not laws. If one stops matching what you see, re-run its command and correct the
entry rather than working around it. A set of findings that a reader cannot re-check turns
into folklore within one release, and this document exists because that already happened
once: an earlier, careful write-up of this same material recommended a resume recipe that
does not work, and the error survived precisely because nobody could tell trust from
verification.

**Observed against `claude 2.1.221`.** Run `claude --version` before trusting anything
below. The findings were produced against two throwaway probe sessions created for the
purpose in scratch directories; no real session was touched.

The operational summary — the recipe and the traps — ships with niwa as
`references/session-control.md` inside the `fleet` root skill, so a coordinator has it
without this repository on disk. This guide is the evidence behind it.

## 1. The resume directory

`claude --resume` resolves a session against the **current working directory**. Sessions
are stored at `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`, where the encoding maps
`/`, `.`, `+` and `_` all to `-`:

```
/home/u/dev/ws/ws+my_topic-6b7b745a
  -> -home-u-dev-ws-ws-my-topic-6b7b745a
/home/u/.claude/jobs/2de7b65a/tmp/probe
  -> -home-u--claude-jobs-2de7b65a-tmp-probe
```

`+` and `_` both became `-`; `/.claude` became `--claude`. The encoding is lossy and
cannot be inverted.

Three directories, tested against a stopped probe session.

**An unrelated directory** — fails, in a way that reads like a missing session:

```
$ cd /some/other/dir
$ claude --resume 9e4d2cae-2a27-40fa-a6fc-1b3f38a34142 --print "..."
No conversation found with session ID: 9e4d2cae-2a27-40fa-a6fc-1b3f38a34142
# exit 1, 2s
```

**The encoded projects directory** — the obvious guess, and also wrong, because the lookup
re-encodes whatever cwd you are standing in:

```
$ cd ~/.claude/projects/-home-u--claude-jobs-2de7b65a-tmp-probe
$ claude --resume 9e4d2cae-... --print "..."
No conversation found with session ID: 9e4d2cae-...
# exit 1, 2s
```

**The cwd recorded inside the transcript** — correct:

```
$ T=$(find ~/.claude/projects -maxdepth 2 -type f -name "9e4d2cae-*.jsonl")
$ W=$(head -20 "$T" | jq -r 'select(.cwd) | .cwd' | head -1)
$ echo "$W"
/home/u/.claude/jobs/2de7b65a/tmp/probe
$ cd "$W" && claude --resume 9e4d2cae-... --print --output-format json "..."
# exit 0, 8s, same session_id, side effect landed
```

`-type f` is load-bearing: entering a git worktree also creates a *directory* named after
the session uuid, holding `subagents/` and `tool-results/`.

### Why the reported cwd is not a substitute

`claude agents` reports a cwd, but it is not a reliable resume directory. For a **live**
session it reports the *current* cwd, which moves when the session enters a worktree while
the transcript stays where it was created. For a **finished** session it reports the
*launch* cwd, while the transcript may have been re-homed under the worktree the session
moved into.

Both were observed on one machine at the same moment:

| Session | `claude agents` cwd | Transcript directory |
|---|---|---|
| finished worker | instance root | encoded worktree path |
| finished worker | instance root | encoded worktree path |
| live session | worktree | encoded launch directory |

So neither "use the instance directory" nor "use the reported cwd" is safe. Find the
transcript and read the cwd out of it.

## 2. A live background session refuses, harmlessly

```
$ claude --resume 9e4d2cae-... --print "This is message two."
Error: Session 9e4d2cae-... is currently running as a background agent (bg). Use
`claude agents` to find and attach to it, or add --fork-session to branch off a copy.
# exit 1, 4s
```

It fails in seconds before any model call. The session's pid was unchanged afterwards and
no message was delivered — non-destructive, and also useless.

## 3. `claude stop` is the missing step, and it is hidden

```
$ claude --help | grep -cE '^\s+stop\b'
0
$ claude stop --help
Usage: claude stop <id>
  Stop a background session. Its conversation is kept; resume it later with `claude attach <id>`.
```

It takes the **short job id**, not the session uuid. After a clean stop the agents row
loses `pid` and `status` and keeps `"state":"done"`, and the same resume that was refused
succeeds in place with the same session id.

`claude attach <id>` is the intended way to reach a live session, but it is interactive, so
it is not usable from a non-interactive agent.

## 4. Never wrap a resume in a short timeout

```
$ timeout 5 claude --resume 9e4d2cae-... --print --output-format json "This is message four."
# exit 124, 6s
```

The captured result object shows the cost:

```json
{"is_error":true,"stop_reason":"tool_use","terminal_reason":"aborted_streaming",
 "subtype":"error_during_execution","num_turns":3,"total_cost_usd":0.0031, ...}
```

**The side effect still landed.** The probe's log gained its line before the process died.
A timeout does not roll anything back — it kills a turn partway through, after some tool
calls have run and before the rest have. On a worker that means edits on disk with nothing
committed, and a transcript that ends mid-thought. The caller sees only exit 124 and cannot
tell how far the turn got.

A resumed turn does real work for minutes. Use `nohup` and poll.

## 5. A killed session keeps a stale registration, and has no `pid`

A live background agent, killed the way `timeout` kills one:

```
$ kill -TERM 2390835        # the pid from `claude agents`
$ ps -p 2390835 || echo gone
gone
$ claude agents --json --all | jq -c '.[] | select(.id=="fd4e4a87")'
{"id":"fd4e4a87","cwd":".../probe2","kind":"background",
 "sessionId":"fd4e4a87-...","name":"log rotation task","state":"working"}
```

The process is dead. The row says `"state":"working"` and carries **no `pid` field at
all**. Its turn was killed mid-flight and its closing side effect never ran.

The obvious retry is refused:

```
$ claude --resume fd4e4a87-... --print "This is message two."
Error: Session fd4e4a87-... is currently running as a background agent (bg). ...
# exit 1, 3s
```

That error, from a process that no longer exists, is what makes this dangerous: a script
that treats a fast non-zero exit as "already handled" concludes the message was delivered.

`claude stop` clears it, and the stop must be verified rather than assumed:

```
$ claude stop fd4e4a87
stopped fd4e4a87
$ claude agents --json --all | jq -r '.[] | select(.id=="fd4e4a87") | .state'
"stopped"
$ claude --resume fd4e4a87-... --print --output-format json "This is message two."
# exit 0, side effect landed
```

### This supersedes the `pid`-absence rule

An earlier version of these findings said a row without a `pid` is safe to resume. **That
is wrong**, and the stale row above is the counter-example: no `pid`, and refused. Use
`state`:

| `state` | `pid` | Resume behaviour |
|---|---|---|
| `done` | present | refused — live and idle |
| `working` | present | refused — live and busy |
| `working` | **absent** | **refused — stale registration, process is dead** |
| `done` | absent | succeeds |
| `stopped` | absent | succeeds |

When a `pid` is present, `ps -p <pid>` tells you whether it is real.

## 6. Permission mode is not inherited by a resumed turn

The probe was launched with a permission-bypassing flag. A resume without it stalled on a
prompt nothing could answer:

```
$ claude --resume 9e4d2cae-... --print --output-format json "This is message two."
# exit 0, 13s, $0.050
"result": "Permission needed to append TURN 2 to log.txt. Please approve the tool use."
```

The same message with the flag re-passed completed and did the work, for $0.0067. **The
failed attempt cost eight times as much as the successful one**, because a resume reloads
the entire context before it discovers it cannot act.

Pass the permission mode explicitly on every resume.

## 7. `--fork-session` runs invisibly

```
$ claude agents --json --all | jq 'length'
20
$ claude --resume fd4e4a87-... --fork-session --print --output-format json "Fork test."
# exit 0, new session_id de19b852-..., real tool call executed
$ claude agents --json --all | jq 'length'
20
$ claude agents --json --all | jq -c '.[] | select(.sessionId=="de19b852-...")'
# (no output)
```

The fork inherited the full context and standing instructions, executed real tool calls,
cost money, and never appeared in agent view. On a real worker that copy could commit and
push while the session you meant to reach never sees the message. It is not a workaround
for the refusal in section 2.

## 8. `--from-pr` fails open

```
$ claude --from-pr 999999 --print --output-format json "Noop probe - do not act."
# exit 0, session_id 52d45119-...
$ wc -l ~/.claude/projects/.../52d45119-....jsonl
12
$ grep -c TURN ~/.claude/projects/.../52d45119-....jsonl
0
```

A number matching no linked session produced a brand-new 12-line session with no inherited
context and reported success. It also cost **$0.0998** — the most expensive command in the
probe run, for a session that does nothing. It is a lookup convenience, not a safety
feature, and it is *less* safe than a raw id because a miss is silent.

## 9. Launching

Two constraints, both found by hitting them:

```
$ claude --bg --session-id <uuid> -p "..."
--bg and --print conflict: --print never starts the interactive session that `claude agents`
attaches to, so the job would be unattachable. The prompt is the positional — drop --print:
`claude --bg '<task>'`
```

```
$ claude --bg --session-id <uuid> "..."
warning: --bg manages the session id; ignoring --session-id (use --resume <id> to continue
an existing session)
```

**A background session's uuid cannot be pinned in advance.** Any script that wants to
address a session later has to capture the id at launch and keep it.

Also observed: a background session launched where permissions are not pre-granted parks at
`{"status":"waiting","waitingFor":"permission prompt","state":"blocked"}` and makes no
progress. It is not dead, and nothing surfaces the stall except that row.

## 10. Cost

Every figure is `total_cost_usd` from the result object, on a small model, for a session
whose entire context is a dozen log lines:

| Operation | Cost |
|---|---|
| Resume that stalled on a permission prompt | $0.0500 |
| Resume that did the work | $0.0067 |
| Resume killed by `timeout 5` | $0.0031 |
| `--fork-session` resume | $0.0065 |
| `--from-pr` miss (new empty session) | $0.0998 |

A resume reloads the entire context before it does anything — 23k cache-read tokens on that
trivial probe. On a real worker with a substantial transcript, one follow-up message
measured **$8.83 across 40 turns**. The floor scales with transcript size, not with the
size of what you are asking for, and the failure modes above cost more than the successes.

## Re-verifying this guide

Do not re-run these against a real session. Create a throwaway:

```bash
mkdir -p /tmp/probe && cd /tmp/probe && printf 'baseline\n' > log.txt
nohup claude --bg --model haiku <permission flags> \
  "Run: echo TURN 1 >> ./log.txt   Standing instruction: on every future message, append
   the next sequential TURN line to ./log.txt, then reply with the line you wrote." \
  > launch.out 2>&1 &
```

Read the short id out of `launch.out`, the uuid out of `claude agents --json --all`, and
work through the sections in order. Stop the probe and remove the directory afterwards.

Correct any entry that no longer reproduces, and update the version stamp at the top. A
finding that has gone stale should be fixed here, not preserved as history.
