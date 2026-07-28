# Decision 2 — `worktree from-hook` create-path failure atomicity

## The question

What should `niwa worktree from-hook` do when its create path fails *after*
`git worktree add` and the session-state write have already succeeded?

Today it does nothing. The command returns a non-zero exit, Claude Code fails
`EnterWorktree` and shows the error, and the git worktree plus an `active`
session record both survive. `niwa worktree list` then reports an `active`
worktree that was never fully provisioned, and cleanup takes a manual
`niwa worktree destroy <id> --force` plus `git worktree prune`.

## What the create path actually does

`runFromHookCreate` (`internal/cli/session_from_hook_cmd.go:116`) is two calls:

1. `worktree.CreateSession(...)` — validates the repo, reserves a session id,
   `git worktree add <path> -b session/<sid>`, scaffolds `.niwa/`, records git
   exclude coverage, writes `<instance>/.niwa/sessions/<sid>.json` with
   `status: active`. **This step is already atomic**: every failure after
   `git worktree add` runs the internal `cleanupWorktree` closure
   (`internal/worktree/worktree.go:214`) and returns with nothing on disk.
2. `applyContentToWorktree(...)` → `workspace.ApplyToWorktree`
   (`internal/workspace/worktree_content.go:453`) — installs repo CLAUDE
   content, runs the repo materializers, inherits the clone's env output,
   re-asserts git exclude coverage, writes the worktree rules import and the
   purpose/branch layer, then runs any `worktree-hooks/` apply scripts. **This
   step has no rollback at all.** Six distinct `return nil, err` sites can fire.

So the atomicity gap is exactly one call wide: `CreateSession` cleans up after
itself, `ApplyToWorktree` does not, and the hook path inherits the difference.

The observed failure (`claude.env: promoted key "GH_TOKEN" not found in
resolved env vars`) comes from the settings materializer in step 2 of
`ApplyToWorktree`, i.e. after step 1 has already written `CLAUDE.local.md`.

Note the asymmetry with the human path. `runSessionCreate`
(`internal/cli/session_lifecycle_cmd.go:194`) hits the *same* failure and
deliberately retains, with an error that says so: "the worktree exists; re-sync
it later". That is right for a human — they are standing at the terminal, the
worktree is a real place they asked for, and `niwa worktree apply <id>` is a
genuine repair. It is wrong for the hook path: Claude aborts `EnterWorktree`,
never enters the directory, and no agent will ever return to re-sync it.

## Does the dirty guard apply here? — explicitly, almost never

Decision 3 refuses to destroy a worktree that might hold uncommitted work.
That concern does **not** transfer to a failed create, for a checkable reason:

- Every file niwa writes into a worktree either carries the `.local` infix
  (`CLAUDE.local.md`, `settings.local.json`, hook scripts via `localRename`,
  `[files]` destinations, which the materializer rewrites to include `.local`)
  or lives under `.niwa/`. Both are in the managed exclude block
  (`internal/gitexclude/exclude.go:35`).
- The single niwa-authored file without a `.local` infix,
  `.claude/rules/worktree-imports.md`, gets its exclude coverage asserted at
  `worktree_content.go:577` — *before* it is written at line 584. That ordering
  is deliberate and commented; it exists precisely so a delegated worktree does
  not read dirty.
- The agent never enters the directory on the failure path, so it cannot have
  authored anything.

The one real exception is step 5: a workspace-authored `worktree-hooks/apply`
script runs arbitrary commands in the worktree and can touch tracked files
(`go mod tidy`, a formatter, a generator) before a later script exits non-zero.
That is rare, but it is the case where a failed create genuinely could hold work
worth keeping — and it is the case the recommendation must not trample.

Conclusion: treat "clean" as the overwhelming common case, but keep the dirty
guard armed rather than asserting cleanliness. That costs one `git status
--porcelain` on an already-failing path.

## Options

### A. Full rollback

Remove the git worktree, delete the branch, delete the session state file, so
the failure leaves no trace.

- **Pros.** Matches `CreateSession`'s existing internal contract. Nothing to
  clean up. No new lifecycle state. Retries never accumulate anything.
- **Cons.** Deletes the audit trail entirely — a user debugging repeated agent
  isolation failures has only the transient stderr text. Deleting the state file
  is a *new* operation: nothing in niwa removes `<sid>.json` today, so it
  introduces a second deletion semantic alongside "mark terminal, keep the
  record", which the guide documents as the contract ("the state file stays on
  disk"). Rolls back past the dirty guard unless it re-implements it.
- **Failure scenario.** `git worktree remove` fails (the directory is busy — a
  file watcher, an editor, an NFS lock). Rollback aborts partway: the state file
  is already deleted but the worktree directory and branch remain. niwa no
  longer knows the worktree exists, `niwa worktree list` shows nothing, and the
  user is left with an orphan that is *not* surfaced anywhere — the exact
  silent-orphan outcome the design's security section rejects.

### B. Mark failed and retain

Transition the session to a new `failed` status, leave the worktree on disk,
surface it in `niwa worktree list`, clean it with `niwa worktree destroy`.

- **Pros.** Maximum forensic value: the half-provisioned directory is there to
  inspect. Honest record — the list no longer claims `active`. Reuses the
  existing destroy command for cleanup.
- **Cons.** Needs a new terminal status, and terminality is spelled out
  literally as `Ended || Abandoned` at five production sites
  (`worktree.go:279`, `session_lifecycle_cmd.go:260` and `:457`,
  `sessionattach/attach.go:61`, `statusRank`), plus the `--status` flag help and
  the guide's status table. Miss one and a `failed` session is treated as live —
  e.g. `resolveSessionIDByPath` would match it, and the apply pipeline's
  worktree env refresh would skip it only by accident. Retained directories
  accumulate: each agent retry leaves another one, and nothing sweeps them.
- **Failure scenario.** An agent retries `EnterWorktree` four times against a
  misconfigured workspace. The developer comes back to four `failed` rows, four
  worktree directories, and four `session/<sid>` branches, and has to destroy
  each one by hand. The forensic value of directory two through four is zero —
  they are byte-identical to directory one.

### C. Partial rollback

Remove the git worktree, keep a `failed` session record as an audit trail.

- **Pros.** No directory accumulation; the record survives for debugging.
- **Cons.** Carries B's full new-status blast radius while producing an on-disk
  shape — terminal record, no worktree directory — that `ended` *already*
  denotes. Two statuses would then mean "record without a directory", and the
  only difference is why.
- **Failure scenario.** Six months on, a contributor adds a status check and
  writes `status == "ended"` because that is what the guide's lifecycle diagram
  shows. `failed` sessions silently fall through it. The distinction bought
  nothing and cost a permanent invariant.

### D. Leave as-is, improve the error

No state change. The error names the exact cleanup command.

- **Pros.** Smallest possible change — one `fmt.Errorf`. Zero risk of the
  rollback itself misbehaving. Preserves everything for inspection.
- **Cons.** Leaves `niwa worktree list` lying: an `active` row for a worktree no
  process is in, which is precisely the R6 claim ("niwa remains the system of
  record for active worktrees") that the feature is built on. Pushes recovery
  onto the user for a failure they did not cause and cannot see the shape of.
  Accumulates on retry exactly as in B, but without even a status marking the
  rows as junk.
- **Failure scenario.** A vault credential lapses on Monday. Every agent task
  that day fails to isolate; each one leaves an `active` row. By Friday
  `niwa worktree list` has thirty `active` worktrees, none of them real, and the
  one genuinely active worktree the developer *is* working in is buried among
  them.

### E (recommended). Guarded reconciliation — reuse the WorktreeRemove teardown

On any post-`git worktree add` failure, run the same reconciliation the
`WorktreeRemove` path already runs: `DestroySession(ctx, instanceRoot, sid,
force=false, ...)`. Then return an error that names both the original failure
and what niwa did about it.

In the normal case this is full rollback with no new machinery: `DestroySession`
writes `status: ended`, runs `git worktree remove --force`, and deletes
`session/<sid>` with `git branch -d`. The branch was created at HEAD with no
commits on it, so `-d` succeeds and no ref leaks. The end state is exactly the
end state of a normal destroy — a shape the CLI, the guide, and every reader
already understand.

In the dirty case — the `worktree-hooks` exception above — `DestroySession`
returns `ErrWorktreeDirty`, niwa retains the worktree and says so, character for
character the same log-and-retain Decision 3 specifies for teardown. If the
`git worktree remove` inside `DestroySession` fails for an unrelated reason, the
record is already `ended` and the directory is still on disk; that is a
recoverable, *named* state (`niwa worktree destroy <id> --force` and
`git worktree prune`), not an unknown one.

- **Pros.** No new lifecycle status. No new code path — it is a call to a
  function that already exists, already has the guard ordering right, and is
  already exercised by the remove path. Consistent with Decision 3 by
  construction rather than by argument: the same call, the same guards, the same
  retain-and-log fallback. Retries do not accumulate. The `active`-row lie is
  gone.
- **Cons.** The terminal record says `ended`, which does not distinguish "the
  agent worked here and finished" from "this never got off the ground". The
  audit trail is the error text plus the record's existence, not a status value.
  Adds one `git status --porcelain` and two git subprocesses to a path that is
  already failing.

- **Failure scenario (the one that would make me wrong).** `DestroySession`
  reads the session file, finds it dirty *because a worktree hook left a build
  artifact that is tracked in the repo*, and retains. The user now gets both
  the original content-install error and a retain notice, and still has manual
  cleanup — option E degrades to option D in exactly the situation where the
  extra machinery bought nothing. I accept that: it is the situation where
  retaining is *correct*, and it is rare. The version of this that would
  genuinely be a mistake is if `git status` were slow or flaky enough on large
  repos to turn a clean failure into a hung hook; that is bounded by the same
  context the create path already runs under.

## Recommendation

**Take E.** On any failure after `git worktree add` succeeds, run the guarded
`DestroySession(force=false)` and report both errors.

The rationale in one line: the create path's failure should reconcile through
the *same* teardown the remove path uses, so there is one reconciliation
mechanism in the feature rather than two.

Three supporting reasons.

First, it fixes the actual defect, which is not "a directory was left behind"
but "niwa's records claim an `active` worktree that no process is in". That
claim is R6's invariant and the premise the whole delegation feature rests on.
D leaves the claim false. E makes it true using the state machine that already
exists.

Second, it does not contradict Decision 3 — it *is* Decision 3, applied to a
second entry point. Decision 3's rule is "attempt a guarded destroy, never force
past the dirty guard, log and retain when refused". E is that rule verbatim. A
reviewer does not have to reason about whether create-failure cleanup is safe;
they have to check that it calls the same function.

Third, it avoids a new terminal status. `failed` looks cheap and is not: five
production sites spell terminality as a two-value comparison, and the cost of
missing one is a session that reads as live forever. The information a `failed`
status would carry — *why* this worktree does not exist — belongs in the error
the user reads at the moment it happens, not in a state field nobody queries.

The honest cost is the one named above: an `ended` record cannot be
distinguished from a normal completed worktree by status alone. If that turns
out to matter in practice, the cheap follow-up is a `failure_reason` string
field on the state file — additive, ignored by every existing reader, no
terminality change — not a new status.

## Implementation notes

**Files that change**

- `internal/cli/session_from_hook_cmd.go` — `runFromHookCreate`. Replace the
  bare `return fmt.Errorf(...)` after `applyContentToWorktree` with a call to a
  small `reconcileFailedHookCreate(stderr, instanceRoot, sessionID, cause)`
  helper that runs `worktree.DestroySession(ctx, instanceRoot, sessionID,
  false, worktree.StdGitInvoker{})` and composes the returned error.
- No change to `internal/worktree/worktree.go`. `DestroySession` already has the
  right guard ordering (terminal early-return, attach guard, dirty guard,
  teardown) and the right branch semantics.
- No change to `internal/workspace/worktree_content.go`. `ApplyToWorktree` stays
  fail-fast; rollback is the caller's job, matching how `CreateSession` and
  `runSessionCreate` are already layered.
- No change to `runSessionCreate` (the human path). Its retain-and-tell
  behavior is correct for an interactive caller and should be left alone — the
  divergence between the two callers is intentional and worth a comment.
- `docs/guides/worktree.md` — extend the "Teardown: clean vs. dirty" section to
  say the same reconciliation runs when a delegated *create* fails.

**New lifecycle state**

None. `active` → `ended` via the existing `DestroySession` write. `abandoned`
stays what it is today: declared, documented in the guide's status table, and
written by no production code path.

**What `niwa worktree list` shows**

Nothing, in the common case — the failed create leaves an `ended` row, which the
flagless list still prints but which no longer claims to be live, and which
`--status active` correctly excludes. In the dirty-retain case the row stays
`active` with its worktree on disk, which is accurate: there is a real directory
with real work in it.

**Idempotency and retries**

Safe. Each `EnterWorktree` attempt calls `CreateSession`, which reserves a fresh
session id with `O_CREATE|O_EXCL` and derives both the worktree path
(`<repo>-<sid>`) and the branch (`session/<sid>`) from it. A retry cannot
collide with the attempt it is retrying, and under E the previous attempt has
already been reconciled, so N retries leave N `ended` records and zero
directories.

**User-visible output**

The hook contract is unchanged: stdout stays empty on failure, exit stays
non-zero, Claude Code still fails `EnterWorktree` loudly. Only the error text
gains the reconciliation outcome. Two shapes:

```
niwa: error: installing content into worktree a3f91c2b: claude.env: promoted
key "GH_TOKEN" not found in resolved env vars
niwa: the partially created worktree was removed; run `niwa apply` to
materialize the clone's env, then retry.
```

```
niwa: error: installing content into worktree a3f91c2b: <cause>
niwa: notice: the worktree at <path> has uncommitted changes and was retained,
not removed. Review it, then reclaim it with
`niwa worktree destroy a3f91c2b --force`.
```

**Tests worth writing**

A fault-injecting `GitInvoker` plus a stub materializer that fails at each of
`ApplyToWorktree`'s return points, asserting: session file ends `ended`,
worktree directory gone, `git branch -d session/<sid>` invoked, stdout empty,
exit non-zero. Then one dirty case asserting the record stays `active`, the
directory survives, and the retain notice names the session id.
