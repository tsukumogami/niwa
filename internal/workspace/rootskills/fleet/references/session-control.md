# Reaching a background session

The recipe, and the three traps. Each trap looks like success from the caller's side,
which is why they are worth memorizing rather than rediscovering.

The evidence behind every claim here -- the commands, the observed output, and the CLI
version it was checked against -- is in niwa's `docs/guides/background-session-control.md`.
If a claim below stops matching what you see, that guide tells you how to re-check it
rather than guess.

## Before anything: is it actually stuck?

```bash
claude agents --json --all | jq -c '.[] | select(.id=="<short-id>")'
```

Three fields matter, and they answer two different questions. `pid` tells you whether
`--resume` will be accepted. `status` and `state` tell you what stopping it would cost.

**A `pid` is present — the session is registered, and `--resume` is refused no matter what
else the row says.** To message it you must `claude stop` first, which ends the background
agent but keeps the conversation:

| Row | What it means | What stopping costs |
|-----|---------------|---------------------|
| `status: busy` | Genuinely working right now | A turn in flight. Leave it alone unless you mean to interrupt. |
| `status: idle` | Live, between turns | Nothing. This is the cheap one to stop and resume. |
| `state: blocked`, `waitingFor` set | Stuck on a prompt nothing is going to answer | Nothing — it is not progressing. Stop it, then resume passing the permission flags it was launched with. |
| no `status` at all | Still live | Unknown. `ps -p <pid>` before assuming. |

**No `pid` — now `state` decides:**

| `state` | What it means | Resume |
|---------|---------------|--------|
| `done` or `stopped` | Finished cleanly | Succeeds |
| `working` or `blocked` | **Stale.** The process is gone; the registration outlived it | **Refused.** `claude stop` clears it, then resume |

Two traps in that pair of tables.

**`pid` absence is not a safe-to-resume signal.** A session killed by a signal loses its
`pid` and keeps a non-terminal `state`, and a resume against it is refused. When a `pid` is
present, `ps -p <pid>` tells you whether it is real.

**`status: idle` does not mean reachable.** The refusal keys on the registration, not on
whether the session is doing anything, so a live-idle session refuses exactly like a busy
one. What `status` buys you is knowing that stopping it interrupts nothing — which is the
decision that actually differs between the rows.

On one machine at one moment, 16 sessions split as: `working`+`busy`+pid 4,
`working`+`idle`+pid 4, `blocked` without pid 4, `blocked`+`idle`+pid 1, `done` without pid
1, `stopped` without pid 1, and one live row carrying a `pid` and no `state` at all. Two
things worth taking from that spread rather than from the counts: `blocked` is common
enough that a table omitting it is a table that fails on the canonical stuck session, and a
row can carry a `pid` with no `state` or `status`, so treat `pid` as the primary key and the
rest as detail.

## The recipe

```bash
U=<full-session-uuid>
T=$(find ~/.claude/projects -maxdepth 2 -type f -name "$U.jsonl")
W=$(head -20 "$T" | jq -r 'select(.cwd) | .cwd' | head -1)

claude stop <short-id>          # only if it is registered; then verify below
cd "$W" && nohup claude --resume "$U" --print --output-format json \
  <the permission flags the session was launched with> "<message>" > out.json 2>&1 &
```

Then poll `out.json` and `ps` the pid you captured.

## Trap 1: the working directory is not where you think

`--resume` resolves the session against the **current working directory**, and getting it
wrong yields `No conversation found with session ID: <uuid>` -- which reads like a missing
session rather than a wrong directory.

Three things that do not work:

- **The instance or launch directory.** A worker that enters a git worktree moves, and its
  transcript is stored under wherever it was when the file was created.
- **The cwd reported by `claude agents`.** For a live session that is the *current* cwd;
  for a finished one it is the *launch* cwd. Neither reliably matches the transcript.
- **The encoded directory under `~/.claude/projects/`.** Tempting, and wrong: the lookup
  re-encodes whatever cwd you are in, so standing inside the encoded directory produces a
  doubly-encoded path. The encoding also maps `/`, `.`, `+` and `_` all to `-`, so it
  cannot be decoded by hand anyway.

What works is reading the `cwd` recorded inside the transcript, as above. The `-type f` in
the `find` matters: a session that entered a worktree also creates a *directory* named
after its uuid.

## Trap 2: never wrap a resume in a short timeout

A resumed turn does real work for minutes. A timeout sends a signal partway through, and:

- the caller sees exit 124 and no reply;
- **the side effects that already ran still stand** -- files edited, commands executed,
  nothing rolled back;
- the transcript ends mid-thought;
- and the work sits uncommitted in the worktree, invisible to every forge query.

Use `nohup` and poll. If you need a deadline, enforce it by checking on the process, not
by killing the turn.

## Trap 3: a killed session keeps a stale registration

This is the one that merges a pull request without its fix.

After a signal kills a session mid-turn, its row survives with `state: working` and no
`pid`. The process is gone. The retry is refused:

```
Error: Session <uuid> is currently running as a background agent (bg).
```

That error, from a process that no longer exists, arrives in about three seconds. A caller
that checks only whether the retry *launched* concludes the session was restarted. It was
not, the message was never delivered, and the work stays stranded -- while the pull request
looks finished and green.

`claude stop <short-id>` clears it. **Verify the stop took effect** before resuming:

```bash
claude stop <short-id>
claude agents --json --all | jq -r '.[] | select(.id=="<short-id>") | .state'
# expect "stopped"
```

## Two more things that cost money quietly

- **Permission mode is not inherited.** A resume without the flags the session was launched
  with can stall on a prompt nothing will answer -- after reloading the whole context. That
  failed attempt costs more than the successful one.
- **`--fork-session` runs invisibly.** It exits 0, inherits the full context, executes real
  tool calls, and never appears in `claude agents`. On a real worker the copy could commit
  and push while the session you meant to reach never sees your message. It is not a
  workaround for the refusal.
- **`--from-pr` fails open.** A number matching no linked session silently starts a new,
  empty session and reports success.

## And never send two resumes at once

Two resumes against one session branch its transcript in place: the second writes a
synthetic turn to close out the first's message and hangs its own off that, so each is
blind to the other's work and one side vanishes from the visible lineage. Both report
success.
