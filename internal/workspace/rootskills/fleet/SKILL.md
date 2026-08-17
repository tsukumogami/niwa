---
name: fleet
description: Track and steer the background workers this session dispatched. Use when the user asks what the workers are doing, what is in flight, what to dispatch next, whether anything is stuck, or to review the pull requests the workers opened. Triggers on "what's in flight", "status of the workers", "show me the table", "what should we dispatch next", "is anything stuck", "review what came back". This covers work done by OTHER sessions dispatched from here -- not the current session's own pull requests.
---

# /fleet

You dispatched workers. Now you have to know what they are doing, catch what went wrong
quietly, and decide what to do about what came back.

Use this from a coordinator session at the workspace root, after `/dispatch` has put one
or more workers in flight. It is the other half of dispatching: `/dispatch` decides how to
hand work off, this decides what to do once it is out there.

**Scope boundary.** This skill is about work done by *other* sessions. If the user wants
the pull requests *this* session opened, that is a per-session in-flight report and a
different tool; do not issue a fleet-wide listing for that question, and do not answer
this one with a session-scoped ledger.

## The one rule that makes the rest work

**State the check and when it ran, or say plainly that you did not check.**

This is not the same as "verify before you claim," which sounds right and catches nothing.
The failures worth preventing all *felt* verified, because a check did run -- it just
answered a slightly different question than the one asked:

- Querying a list of issue numbers you already knew about cannot discover anything newly
  filed. The query succeeded. The answer was wrong.
- Checking that two changes touch no common files is not checking that they are safe to
  run in parallel. One can make the other destructive without sharing a line.
- Checking that a retry process launched is not checking that it was accepted. A refused
  resume exits in about three seconds and looks, from the caller's side, like a fast
  success.
- Checking for the absence of a placeholder string is not checking for the presence of
  content.

So: "no overlapping files as of `gh pr diff`, 14:20" is a claim someone can falsify. "Safe
to run in parallel" is not. When you did not check, say so -- an honest gap is cheap and a
confident wrong answer costs a merge.

## The work-in-flight table

This is the artifact the user will ask for repeatedly, and the thing that drives what gets
dispatched next. Build it fresh each time; do not read back a stale one.

| Issue | Session | PR | Work state | CI |
|-------|---------|----|------------|----|

- **Issue** -- link it. Plain text where there is none.
- **Session** -- the short id and whether it is running, idle, finished, or stopped, from
  `claude agents --json --all`.
- **PR** -- link it. Plain text where there is none.
- **Work state** -- **the column no forge query can produce.** Committed and pushed? Files
  edited but uncommitted? Clean and not started? See the sweep below.
- **CI** -- the check rollup, and whether it is still moving.

Gather it with direct queries. `gh pr list`, `gh issue view`, `claude agents --json`, and
a filesystem sweep will build this correctly in under a minute.

### Do not delegate this

Handing "gather the status" to a sub-agent is tempting under context pressure and it is a
bad trade. In one measured batch, three of four status-gathering agents wrote their file's
skeleton and then went idle without filling it in, while the direct queries were correct
every time. Delegating a lookup adds a silent-stall failure mode to a task that had none.

The exception is instructive. The fourth agent *did* finish, and it found the single most
important fact of that session -- because it ran a check the coordinator had not thought
to run, a filesystem sweep across every worker's tree. The distinguishing property is not
size or difficulty:

- **A lookup against a source you can query directly** -- do it yourself.
- **A sweep across a surface you would not otherwise visit** -- worth delegating, because
  the value is in going somewhere you were not going to go.

## The stranded-work sweep

**A pull request can be green, complete, and missing its fix.**

A worker can have exactly the right change on disk, uncommitted, in its own worktree, and
the pull request will show a merged-ready branch with passing checks and a thorough
description. Pull request state, check runs, review status, commit history -- every
forge-side signal agrees the work is done. Only the filesystem disagrees.

This happens whenever a worker's turn is cut off between editing and committing: an
interrupted resume, a killed process, a session that stopped for input nobody gave it.
Sweep for it before you tell anyone a fleet is finished, and before any merge:

```bash
for wt in <each worker's worktree>; do
  echo "== $wt"
  git -C "$wt" status --porcelain
  git -C "$wt" log --oneline @{upstream}..HEAD 2>/dev/null
done
```

Uncommitted changes, or commits that exist locally and not on the remote, mean the pull
request is not what it appears to be. Say so before the merge, not after.

## Deciding what to do about a finding

You have three options for anything a worker left undone or got wrong. They are not
equally priced.

**Wake the session** when it holds context you would otherwise have to rebuild, or when
the change needs its judgment about its own in-progress edits. Reconciling a half-finished
diff is the clearest case: the author knows what it was mid-way through doing and you
would be guessing at intent.

**Fix it yourself** when the change is self-contained and you can see the whole of it. A
one-line correction to a file nobody is editing does not need its author.

**File it** when it is a real finding that is not this pull request's job. Record it as an
issue, not in the pull request body -- squash-merge deletes everything below the `---`, so
a finding recorded only in a description is destroyed the moment it lands.

**The cost that makes this a real decision.** Waking a session is not cheap. A resumed turn
reloads the entire conversation before it does anything, so the floor scales with the
transcript's size, not with the size of what you are asking. One follow-up message to a
worker on a substantial task measured **$8.83 across 40 turns**. Four of those is more than
thirty dollars, for follow-ups on already-written pull requests. Do not wake a session for
something you could fix in a minute.

See `references/session-control.md` for how to actually reach one, and the three traps that
each look like success.

## Watching for what lands next

If you set up a monitor for new pull requests or CI transitions, **seed it from an explicit
list of what you are waiting for**, never from a snapshot of what is already open.

A monitor seeded with "everything currently open is baseline noise" is correct only if you
started it before the thing you want to watch. Started afterwards -- which is the normal
case, because you set up the watch once workers are already running -- it treats the work
you care about as pre-existing and stays silent. It looks healthy the entire time it is
reporting nothing.

Name the pull requests or issues you expect, including the ones already open with checks
still in flight.

## Reviewing what came back

When workers deliver, review before merging. The standard is in
`references/review-standard.md`. The short version: **do not trust the pull request body**,
and every real finding comes from *running* something, not from reading.
