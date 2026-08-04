# Lead 4 (direct): re-verifying the resume findings against the installed CLI

Run directly rather than delegated. Every claim below was produced by a command run in
this session, on `claude 2.1.221`, against two throwaway probe sessions created for the
purpose in scratch directories. No real worker session was touched.

Probes: `9e4d2cae-2a27-40fa-a6fc-1b3f38a34142` (probe), `fd4e4a87-cb8a-4481-bbfe-1e0df5bdeee9`
(probe2). Both stopped and their scratch directories removed after the run.

## Summary of what changed

The prior findings document is right about the shape of the problem and wrong about the
recipe. Three specific things:

| Prior claim | Status | Correct version |
|---|---|---|
| "Recommended form, run from the session's own instance dir" | **Wrong** | Run from the cwd recorded in the transcript, which is not the instance dir once the session enters a worktree |
| "Row has no `pid`: safe. Row has a `pid`: refused." | **Wrong** | A SIGTERM-killed session has no `pid` and is still refused. `state` is the discriminator, not `pid` |
| "the resumed turn runs with the session's tools and no permission prompting" | **Incomplete** | Permission mode is not inherited; it must be passed again on the resume |

Everything else in the prior document reproduced.

## 1. The resume directory

`claude --resume` resolves the session against the **current working directory**. Claude
Code stores each session under `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`, where the
encoding replaces `/`, `.`, `+` and `_` all with `-`. That is lossy and cannot be inverted.

Observed encodings:

```
/home/dgazineu/dev/niwaw/tsuku/tsuku+orchestration_learnings-6b7b745a
  -> -home-dgazineu-dev-niwaw-tsuku-tsuku-orchestration-learnings-6b7b745a
/home/dgazineu/.claude/jobs/2de7b65a/tmp/probe
  -> -home-dgazineu--claude-jobs-2de7b65a-tmp-probe
```

`+` and `_` both became `-`; `/.claude` became `--claude`. Nothing in the encoded name
says which character was which.

**Three directories were tested against a stopped probe session.**

Wrong directory (an unrelated repo worktree):

```
$ cd /path/to/some/other/dir
$ claude --resume 9e4d2cae-2a27-40fa-a6fc-1b3f38a34142 --print "..."
No conversation found with session ID: 9e4d2cae-2a27-40fa-a6fc-1b3f38a34142
# exit 1, 2s
```

The encoded projects directory itself — the obvious guess, and also wrong, because the
lookup re-encodes whatever cwd you are in:

```
$ cd ~/.claude/projects/-home-dgazineu--claude-jobs-2de7b65a-tmp-probe
$ claude --resume 9e4d2cae-... --print "..."
No conversation found with session ID: 9e4d2cae-...
# exit 1, 2s
```

The cwd recorded inside the transcript — correct:

```
$ T=$(find ~/.claude/projects -maxdepth 2 -type f -name "9e4d2cae-....jsonl")
$ W=$(head -20 "$T" | jq -r 'select(.cwd) | .cwd' | head -1)
$ echo "$W"
/home/dgazineu/.claude/jobs/2de7b65a/tmp/probe
$ cd "$W" && claude --resume 9e4d2cae-... --print --output-format json "..."
# exit 0, 8s, same session_id, side effect landed
```

**Why the instance dir fails in practice.** `claude agents` reports a cwd, but it is not a
reliable resume directory:

- For a **live** session it reports the *current* cwd, which moves when the session enters
  a worktree — while the transcript file stays where it was created.
- For a **finished** session it reports the *launch* cwd, while the transcript may have
  been re-homed under the worktree the session moved into.

Both directions were observed on this machine at the same moment:

| Session | `claude agents` cwd | transcript directory |
|---|---|---|
| `09397719` (finished worker) | instance root | `...-public-tsuku--claude-worktrees-issue-2496-...` |
| `02d5ab53` (finished worker) | instance root | `...-public-tsuku--claude-worktrees-fix-2471-...` |
| `2de7b65a` (this session, live) | worktree | launch directory |

So neither "use the instance dir" nor "use the agents cwd" is safe. The only reliable
recipe is to find the transcript and read the cwd out of it.

**The verified recipe:**

```bash
U=<full-session-uuid>
T=$(find ~/.claude/projects -maxdepth 2 -type f -name "$U.jsonl")
W=$(head -20 "$T" | jq -r 'select(.cwd) | .cwd' | head -1)
cd "$W" && claude --resume "$U" --print --output-format json <permission flags> "<message>"
```

`-type f` matters: entering a worktree also creates a *directory* named after the session
uuid (holding `subagents/` and `tool-results/`), so a bare `-name "$U*"` matches two things.

## 2. A live background session refuses, non-destructively

```
$ claude --resume 9e4d2cae-... --print "This is message two."
Error: Session 9e4d2cae-... is currently running as a background agent (bg). Use
`claude agents` to find and attach to it, or add --fork-session to branch off a copy.
# exit 1, 4s
```

Reproduced at 2.1.221. Fails in seconds before any model call; the session's pid was
unchanged afterwards and no message was delivered.

## 3. `claude stop` is still the missing step, and is still hidden

```
$ claude --help | grep -cE '^\s+stop\b'
0
$ claude stop --help
Usage: claude stop <id>
  Stop a background session. Its conversation is kept; resume it later with `claude attach <id>`.
```

It takes the short job id. After a clean stop the row loses `pid` and `status` and keeps
`"state":"done"`; the same resume that was refused then succeeds in place, same session id.

## 4. Never wrap a resume in a short timeout

```
$ timeout 5 claude --resume 9e4d2cae-... --print --output-format json "This is message four."
# exit 124, 6s
```

The captured result object shows what that costs:

```json
{"is_error":true,"stop_reason":"tool_use","terminal_reason":"aborted_streaming",
 "subtype":"error_during_execution","num_turns":3,"total_cost_usd":0.0031, ...}
```

**The side effect still landed.** The probe's log gained its `TURN 4` line before the
process died. A timeout does not roll anything back: it kills a turn partway through, after
some tool calls have run and before the rest have. On a worker that means edits on disk
with nothing committed, and a transcript that ends mid-thought. The caller sees only exit
124 and has no way to tell how far the turn got.

A resumed turn does real work for minutes. Use `nohup` and poll.

## 5. A SIGTERM-killed session keeps a stale registration — and has no `pid`

This is the failure that let a PR merge without its fix, and it reproduces exactly.

A live background agent was killed the way `timeout` kills one:

```
$ kill -TERM 2390835        # the pid from `claude agents`
$ ps -p 2390835 || echo gone
gone
$ claude agents --json --all | jq -c '.[] | select(.id=="fd4e4a87")'
{"id":"fd4e4a87","cwd":".../probe2","kind":"background",
 "sessionId":"fd4e4a87-...","name":"log rotation task","state":"working"}
```

The process is dead. The row says `"state":"working"` and carries **no `pid` field at
all**. Its turn was killed mid-sleep and its closing side effect never ran.

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
$ claude agents --json --all | jq -c '.[] | select(.id=="fd4e4a87") | .state'
"stopped"
$ claude --resume fd4e4a87-... --print --output-format json "This is message two."
# exit 0, side effect landed
```

**This invalidates the prior document's governing heuristic.** It said a row without a
`pid` is safe to resume. The stale row above has no `pid` and refuses. The reliable
discriminator is `state`:

| `state` | `pid` | resume behaviour |
|---|---|---|
| `done` | present | refused (genuinely live and idle) |
| `working` | present | refused (genuinely live and busy) |
| `working` | **absent** | **refused — stale registration, process is dead** |
| `done` | absent | succeeds |
| `stopped` | absent | succeeds |

Check the process too, not just the row: `ps -p <pid>` when a `pid` is present, and treat
`state: working` with no `pid` as definitively stale.

## 6. Permission mode is not inherited by a resumed turn

New finding; the prior document reported `permission_denials: []` throughout and concluded
the resumed turn runs with the session's tools ungated. That held because that probe ran
inside a workspace configured for bypass. It is not a property of resume.

The probe session was launched with `--dangerously-skip-permissions`. A resume without the
flag stalled on a prompt it could not answer:

```
$ claude --resume 9e4d2cae-... --print --output-format json "This is message two."
# exit 0, 13s, $0.050
"result": "Permission needed to append TURN 2 to log.txt. Please approve the tool use."
```

The same message with the flag re-passed completed and wrote its line, for $0.0067. The
first attempt cost eight times as much as the successful one and accomplished nothing —
because a resume reloads the whole context regardless of whether it can then act.

Pass the permission mode explicitly on every resume.

## 7. `--fork-session` still runs invisibly

```
$ claude agents --json --all | jq 'length'
20
$ claude --resume fd4e4a87-... --fork-session --print --output-format json "This is the fork test."
# exit 0, new session_id de19b852-..., "TURN 3 appended to ./log.txt"
$ claude agents --json --all | jq 'length'
20
$ claude agents --json --all | jq -c '.[] | select(.sessionId=="de19b852-...")'
# (no output)
```

The fork inherited the standing instruction, executed a real tool call, cost money, and
never appeared in agent view. Row count unchanged.

## 8. `--from-pr` still fails open

```
$ claude --from-pr 999999 --print --output-format json "Noop probe - do not act, reply with the word noop only."
# exit 0, session_id 52d45119-...
$ wc -l ~/.claude/projects/.../52d45119-....jsonl
12
$ grep -c TURN ~/.claude/projects/.../52d45119-....jsonl
0
```

A PR number matching no `pr-link` record produced a brand-new 12-line session with no
inherited context and reported success. It also cost $0.0998 — the most expensive command
in this whole probe run, for a session that does nothing.

## 9. New mechanical facts about launching

Both found by hitting them:

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

So: a background session's uuid cannot be pinned in advance. You must launch it and read
the id back out of `claude agents`, which means any script that wants to address a session
later has to capture the id at launch time.

Also observed: a background session launched outside a permission-bypassing workspace
parks at `{"status":"waiting","waitingFor":"permission prompt","state":"blocked"}` and
makes no progress. It is not dead and nothing surfaces the stall except that row.

## 10. Cost, measured

Every figure below is `total_cost_usd` from the result object, on `claude-haiku-4-5`, for a
session whose entire context is a dozen log lines:

| Operation | Cost |
|---|---|
| resume that stalled on a permission prompt | $0.0500 |
| resume that did the work | $0.0067 |
| resume killed by `timeout 5` | $0.0031 |
| `--fork-session` resume | $0.0065 |
| `--from-pr` miss (new empty session) | $0.0998 |

A resume reloads the entire context before it does anything — 23k cache-read tokens on this
trivial probe. The coordinator's own figure for one follow-up message to a real worker
session was $8.83 across 40 turns. The floor scales with transcript size, not with the size
of what you are asking for.

## Cleanup

Both probe sessions were stopped and their scratch directories removed. No artifact in this
exploration depends on them.
