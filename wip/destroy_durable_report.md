<!-- decision:start id="destroy-durable-work" status="assumed" -->
### Decision: how niwa destroy decides a branch's work is durable

**Context**

`niwa destroy` scans an instance before deleting it (internal/workspace/scan.go). For branches it
reads `upstream:track`: no upstream means "local-only", "ahead" means "unpushed". Measured on
current main with a bare remote and clones:

- pushed without `-u`, squash-merged: flagged local-only (false block);
- branched from origin/main by worktree tooling, so upstream is origin/main: "ahead" forever after
  a squash merge (the issue's report);
- pushed with `-u`, never merged, remote branch deleted and pruned: upstream reads `[gone]`, not
  "ahead", so it passes (silent loss);
- pushed with `-u`, never merged, remote branch deleted but not pruned: the stale cache says it
  is pushed, so it passes (silent loss).

So the check is wrong in both directions. It blocks durable work and passes lost work, because it
trusts local bookkeeping (upstream config and a possibly stale remote-tracking cache) rather than
asking where the content actually lives.

Research measured four content tests across 17 isolated scenarios (git 2.43). `git merge-tree
--write-tree` against the default branch's tip never passed at-risk work but false-blocked whenever
main later touched the same or adjacent lines. Running it against each first-parent default-branch
commit since the fork, limited to commits touching the branch's paths, passed every landed case,
blocked every at-risk one, and took about 15 ms on a 2000-commit main. Patch-id false-blocks on
context drift and, with `--stable`, false-passes a whitespace-only commit. Per-file equality
false-blocks on any later edit to a touched file. GitHub's `pulls?state=all&head=owner:branch`
still returns a merged PR after its head branch is deleted, with `merged_at` and `head.sha`; without
the `owner:` prefix the filter is silently ignored. Only interactive destroy runs the scan (reap,
watch, dispatch and session hooks call DestroyInstance directly), so a bounded network call there
slows no automated path.

**Assumptions**

- GitHub squash commits sit on the default branch's first-parent chain. If wrong, the test misses
  the squash and blocks (conservative).
- Some users run git older than 2.38 (Ubuntu 22.04 ships 2.34). The content test then can't run,
  and the scan says so and falls back to ancestry.
- A merged PR's `head.sha` is frozen at merge time. Not relied on for passing; see below.

**Chosen: Landed by content, or on a live remote branch the forge vouches for; everything else blocks with a named reason**

Per repo, before judging branches:

1. For each remote, refresh the default branch only (`git fetch <remote> <default>` into its
   remote-tracking ref, bounded by a timeout) and list the remote's live branches with
   `git ls-remote --heads` (same timeout). A failure is recorded, not fatal: later steps treat an
   unreachable remote as "can't vouch" and say so. Without the refresh, a PR merged on GitHub
   but not yet fetched would block every time, which is the false block this fix exists to remove.

For each local branch (and a detached HEAD), in order, stopping at the first that holds:

2. **Landed by ancestry.** The tip is an ancestor of some remote's default branch. Safe.
3. **Landed by content.** Let `mb` be the merge base with the default branch. For the tip itself
   and each first-parent default-branch commit after `mb` that touches a path the branch changed,
   `git merge-tree --write-tree <candidate> <tip>` exits 0 and prints `<candidate>^{tree}`. Merging
   the branch into a state the default branch actually reached adds nothing, so every change the
   branch carries is on the default branch. Safe. Any other exit, or a different tree, is "not
   landed"; the ambiguity between conflict and error doesn't matter because both block. Needs git
   2.38; below that this step is skipped and the reason says so.
4. **On a live remote branch the forge vouches for.** The tip is contained in a branch that
   `ls-remote` shows exists right now (the local copy of that sha must contain the tip), AND the
   forge says no merged PR has that branch as its head (an open PR, or no PR). Safe. A merged PR
   means the head branch is about to be deleted or already could be, so it doesn't count: only
   step 3 can pass such a branch, which is exactly what catches commits pushed after the merge.
   The forge is asked only for github.com remotes, with `head=<owner>:<branch>`, a token from the
   existing resolver, and a timeout. When it can't answer (offline, non-GitHub, no access, error,
   timeout) the live branch does not count.
5. **Otherwise at risk.** The message names the branch, the number of commits not on the default
   branch, and the specific reason: no remote holds the commits; the remote branch it was pushed
   to no longer exists; its PR (#N) merged and these commits aren't in the default branch; the PR
   state couldn't be checked (with why) so the remote branch can't be trusted to survive; the
   remote couldn't be reached; or git is too old to recognise a squash merge.

Uncommitted changes, untracked files, stashes, detached-HEAD orphans and worktrees outside the
instance stay as they are, checked in the primary and every linked worktree inside the instance.
Two silent passes found in the same code are closed as part of this: git errors during the scan
are now reported as "couldn't check" findings instead of being dropped, and `git status` runs
with `-unormal` so a repo's `status.showUntrackedFiles=no` can't hide untracked files.

**Rationale**

The brief's durable test is content, and step 3 is the only measured test with no false pass
that also survives the default branch moving on. Steps 1 and 4 replace local bookkeeping with
the remote's answer at destroy time, which closes both silent losses measured on main. Step 4
alone decides the in-flight case (open PR, not merged) without inventing a false block, and the
forge's only power is to take durability away, never to grant it for content that isn't on the
default branch. Every "couldn't look" outcome lands on block with the reason printed, so there's
no silent pass anywhere in the new path.

**Alternatives Considered**

- **Pushed to any remote-tracking ref (`--not --remotes`)**: the issue's first property. Rejected:
  a stale cache counts a deleted branch as pushed, and a head branch about to be deleted counts as
  durable, so commits pushed after a merge pass.
- **Landed on the default branch only, offline**: no network, no forge. Rejected: every branch with
  an open PR blocks, and so does every PR merged on GitHub but not yet fetched, which recreates the
  false-block problem and the habit of reaching for `--force`.
- **Landed, or on a live remote branch (no forge)**: rejected because a merged PR's head branch
  still counts as durable until someone deletes it, so post-merge commits pass.
- **Content test by patch-id or per-file equality**: rejected on the measured false pass
  (whitespace-only work under `--stable`) and the false blocks on routine drift.
- **Letting a merged PR whose `head.sha` equals the local tip pass on the forge's word**: would
  pass a squash the maintainer edited at merge time, or a partial landing, where content the
  branch carries isn't on the default branch. Rejected in favour of the content test; those two
  cases block, which is defensible and rare.

**Consequences**

Interactive destroy now makes bounded network calls per repo (one fetch of the default branch,
one ls-remote, and a PR lookup per branch that isn't landed), so it's slower and needs a
per-call timeout. Offline, a branch that is only on a remote branch blocks with a reason that says
the check couldn't reach the forge; a user can still confirm at the TTY prompt or use `--force`.
Squash-merged and fully landed work passes with no prompt. Git below 2.38 degrades to today's
behaviour on squash merges, with a message that says why.
<!-- decision:end -->
