# Clarity Review (round 2)

## Verdict: FAIL
The document is precise almost everywhere and the round-1 findings are closed, but three gaps would still send two developers different ways: `--force` is never mentioned even though it is an existing flag on the command being extended and it bypasses the exact safety check this PRD exists to restore; "multi-instance workspace" gates all of R10 and is never defined; and R10's justification for the root-level exit codes is factually wrong for `niwa worktree create`, which makes one acceptance criterion unexecutable as written.

## Ambiguities Found

1. **R2, R9 table, whole Requirements section: `--force` is never mentioned.**
   `niwa worktree destroy` already takes `--force` today
   (`internal/cli/session_lifecycle_cmd.go:104-119`; help text: "Destroy even
   with uncommitted changes, and delete the branch regardless of merge
   status"). R2 says each worktree "is destroyed with the guards
   `niwa worktree destroy` applies today: refuse an attached worktree, refuse
   one with uncommitted changes, and delete its branch only if merged" —
   stated unconditionally, with no word on whether `--force` is honored,
   rejected, or ignored when the value resolves to a session. R9's outcome
   table has no `--force` row. -> **Why it's ambiguous:** three implementations
   all satisfy the text. (a) Wire the session path through the same
   `worktree.DestroySession(..., force)` call, so `--force` flows to every
   worktree in the instance: every run exits 0, no guard ever refuses, and
   every unmerged branch is deleted — silently inverting the PRD's stated
   purpose, and a cleanup script that adds `--force` to get past a stuck
   worktree destroys exactly what #292 is about. (b) Reject `--force` together
   with a session id as a usage error (exit 2). (c) Accept and ignore it.
   This is the highest-consequence gap in the document because it is the one
   flag that can turn a passing run into silent data loss. ->
   **Suggested clarification:** Add a requirement stating whether `--force` is
   accepted with a session id or handle, and either add a `--force` row to the
   R9 table (all guards bypassed, branches deleted regardless of merge status,
   exit 0) with a matching acceptance criterion, or state that `--force` with
   a session id is a usage error, exit 2, with the message. If it is accepted,
   say whether the kept-branch warning is replaced by a deleted-branch line.

2. **R10 opening line and closing paragraph: "multi-instance workspace" and
   "single-instance layout" are never defined, and the code cannot currently
   tell them apart.** R10 applies "At the root of a multi-instance workspace"
   and then exempts "the single-instance layout, where the workspace root is
   itself the only instance". Terms defines "instance of this workspace" and
   "instance location" but not this distinction, which gates every row of the
   table. In the code, `resolveInstanceRoot()`
   (`internal/cli/session.go:136-167`) walks up for `.niwa/instance.json`, and
   `niwa init` writes `.niwa/instance.json` at the workspace root whenever
   ephemeral-session mode is on — the default for named and clone inits
   (`internal/cli/init.go:1018-1036`, and the same fact documented at
   `internal/workspace/cwd_classify.go:113-119`). So the root carries an
   `instance.json` in both layouts, and the marker the code has today does not
   separate them. -> **Why it's ambiguous:** one developer keys the branch off
   "the root has `.niwa/workspace.toml`" (true in both layouts), another off
   "at least one child instance directory exists" (which flips a multi-instance
   workspace to the single-instance branch the moment its last instance is
   reaped, or before the first `niwa create`), another off
   `workspace.ClassifyCwd`. Each ships a different root behavior for the same
   workspace. -> **Suggested clarification:** Add a Terms entry, for example:
   "Single-instance layout: a workspace whose root is itself an instance,
   determined by <named marker or predicate>. Every other workspace is
   multi-instance, including one with no instances yet." Then say which branch
   an empty multi-instance workspace takes.

3. **R10, the create/apply/attach/detach row: "Each already exits 1 at the root
   today, with a misleading error read from the mapping store, so only the
   message changes" is wrong for `niwa worktree create`.** Bare
   `niwa worktree create` at the root exits **2**, not 1, with
   `niwa: error: could not infer repo from working directory: ...`
   (`internal/cli/session_lifecycle_cmd.go:159-166`,
   `internal/workspace/cwd_repo.go:160-161`), and that error never touches the
   mapping store. With an explicit repo it exits 1, but from
   `unknown role: repo "<repo>" not found in workspace <root>`
   (`internal/worktree/worktree.go:179-182`) — also not a mapping-store read.
   `apply`/`attach`/`detach` do match the description. -> **Why it's
   ambiguous:** an implementer who trusts "only the message changes" leaves
   `create` at exit 2 and fails R10; one who trusts the table changes it to 1.
   R21's exemption covers whichever is intended, so nothing else resolves it.
   -> **Suggested clarification:** Split the row, or replace the sentence with
   "`apply`, `attach` and `detach` already exit 1 at the root today with an
   error read from the mapping store; `create` exits 2 today and moves to 1."

4. **Acceptance criterion "At the same root, `niwa worktree create`,
   `apply <x>`, `attach <x>`, `detach <x>` and `niwa go <repo> <x>`, where
   `<x>` is an 8-hex value that is no worktree id in any instance" — `<x>`
   does not apply to `create`.** `niwa worktree create` takes
   `<repo> [purpose]`, never a worktree id. As written the criterion cannot be
   executed for `create`: a tester does not know whether to run bare
   `niwa worktree create`, `niwa worktree create <x>` (which would be read as a
   repo name), or `niwa worktree create <repo>`. -> **Suggested
   clarification:** "bare `niwa worktree create`, `niwa worktree create <repo>`
   for a repo that exists in some instance, and `apply <x>` / `attach <x>` /
   `detach <x>` / `niwa go <repo> <x>` where `<x>` is ...".

5. **R10 vs R21: `niwa worktree list --json` at the root is unassigned.**
   Today `niwa worktree list --json` at the root prints `[]` on stdout and
   exits 0 (`internal/cli/session_lifecycle_cmd.go:667-670`). R10 says `list`
   prints the root message on stderr, "no table", exit 0. R21 says existing
   invocations keep their output. -> **Why it's ambiguous:** does `--json`
   still print `[]`, print nothing, or print the message? A script parsing
   `[]` breaks under one reading and not the other, and "no table" says
   nothing about JSON. -> **Suggested clarification:** Add to the R10 `list`
   row: "`--json` still prints `[]` on stdout and exits 0; the message goes to
   stderr in both modes" (or whichever is intended).

6. **R9 vs R21: the destroyed line has two formats for the same event.**
   Today destroy prints exactly `session: destroyed <session-id>` on stdout
   (`internal/cli/session_lifecycle_cmd.go:593`) — no repo, no path. R9
   requires `session: destroyed <worktree-id> (<repo>) at <path>` for the
   session path, while R9's closing note and the sixth teardown criterion
   require destroy by worktree id and by `--by-path` to produce "the same
   stdout ... as before". -> **Why it's ambiguous:** the natural implementation
   enriches the single existing `Fprintf` and silently changes the by-id and
   `--by-path` output, violating R21; the text never says the enriched line is
   session-path-only. -> **Suggested clarification:** State it: "the enriched
   line is emitted only when the value resolved to a session; destroy by
   worktree id and by `--by-path` keep the bare `session: destroyed <id>`
   line."

7. **R7 steps 2 and 3: a dangling symlink or a non-directory at an instance
   location has two possible outcomes.** Step 2 fires when "nothing exists at
   the path" (exit 0, the R9 "no longer exists" line); step 3 fires when the
   path "exists but is not an instance of this workspace (for example a
   symlink resolving elsewhere)" (exit 1, refused). Terms resolves symlinks
   before deciding what an instance is. A symlink at an instance location whose
   target is gone satisfies both readings depending on whether existence is
   tested with `lstat` or `stat`, and the two outcomes differ in exit code and
   in whether a script treats the session as cleanly reaped. The acceptance
   criteria only cover a symlink resolving to something *outside* the
   workspace. -> **Suggested clarification:** Say which call decides step 2
   ("nothing exists at the path, tested without following symlinks" or "...
   after resolving symlinks"), and add a criterion for the dangling-symlink and
   path-is-a-file cases.

8. **R21 vs R17: a bounded-wait timeout is a new non-zero exit for existing
   invocations, and R21 does not exempt it; R17 assigns no exit code.**
   R21 exempts only "R9's new outcome codes and R10's root-level behavior", so
   as written it forbids `niwa apply` or `niwa create` ever exiting non-zero
   for a new reason — which is precisely what R17 introduces under contention.
   R11 carves R17 out of the concurrency requirement but nothing carves it out
   of R21. The acceptance criterion only demands "exits non-zero", so two
   developers can ship 1 and 2 (2 is reserved for usage errors per R9) and both
   pass. -> **Suggested clarification:** Add R17's timeout to R21's exemption
   list, and give R17 an exit code (1 reads as correct, since 2 is usage and 3
   and 4 are teardown lookup codes).

9. **R16 vs its second kill criterion: which code path recovers.** R16 says an
   interrupted refresh "leaves the configuration directory usable, and the next
   *refresh* proceeds without manual cleanup". The criterion demands that after
   a kill between the two renames, "the next niwa command that *reads* the
   configuration directory finds `workspace.toml` and the existing mappings
   there". Those are different scopes: recovery inside `materializeAndSwap` /
   `SwapSnapshotAtomic` satisfies R16 but fails the criterion, because a plain
   `niwa list` reads the directory without refreshing
   (`internal/workspace/snapshot.go:37`, `internal/workspace/snapshotwriter.go:353`).
   -> **Suggested clarification:** Amend R16 to "the next niwa command that
   reads or refreshes the directory", so the requirement and the criterion
   demand the same placement.

10. **R14: "the configuration files of the most recent successful refresh,
    whatever niwa wrote into it in the meantime" has two readings, and "most
    recent" is unordered.** The clause reads either as "plus whatever niwa
    wrote" (the R12/R13 guarantee restated) or "regardless of whatever niwa
    wrote". Separately, R18 deliberately keeps fetches outside the wait, so two
    refreshes can fetch concurrently and swap in either order: "most recent" by
    swap time can install content fetched *earlier* than the loser's. Nothing
    says whether that is acceptable or whether a generation check is required.
    -> **Suggested clarification:** "After every concurrent command has
    finished, the configuration directory holds the configuration files of the
    refresh that swapped last, together with the local state R12 and R13
    cover." Then state explicitly that an older fetch swapping last is
    acceptable, or require the swap to be skipped in that case.

11. **R9's refusal and ambiguity rows are the only ones without exact strings.**
    Every other row pins the literal line; the R6/R7 row says only an
    "`niwa: error:` line naming the session and the reason", and the ambiguous
    row "an `niwa: error:` line naming every match". User story 2 and Goal 2
    ask for output a cleanup script can act on. -> **Suggested clarification:**
    Give the two literals, as the other rows do, or state that these two rows
    are deliberately human-facing only and that scripts key on the exit code.

12. **Minor: single-instance layout excludes session teardown entirely, without
    saying so.** "Instance location" is "a direct child directory of this
    workspace's root other than `.niwa`", so in a single-instance layout —
    where the root *is* the instance — every mapping's recorded instance path
    is the root, R7 step 1 refuses it, and the criterion "a mapping whose
    instance path is ... the workspace root itself ... exit 1" locks that in.
    Known Limitations mentions the single-instance layout only for lifecycle
    records. -> **Suggested clarification:** One Known Limitations sentence:
    "In the single-instance layout, teardown by session is refused, because the
    root is not an instance location" — or, if mappings never point at the root
    there, say that.

13. **Minor: R11's "must not occur for four concurrent dispatches on an
    otherwise idle machine".** "Otherwise idle" is not something a reviewer or
    CI can verify, and it sits inside a requirement rather than a criterion.
    The parallel-dispatch criterion already covers the case objectively.
    -> **Suggested clarification:** Drop the clause from R11 and let the
    four-dispatch criterion carry it, or replace with "under the conditions of
    the four-dispatch acceptance criterion".

## Suggested Improvements

1. **Add a `--force` row to R9 and a requirement sentence in R2.** Rationale:
   it is the only existing flag on the command being extended, it disables all
   three guards this PRD exists to restore, and the document currently reads as
   though the flag does not exist. Any reviewer of the resulting PR will have
   to ask the question that the PRD should have answered.
2. **Define the single-instance / multi-instance test in Terms.** Rationale:
   R10's entire table is conditioned on a distinction the codebase does not
   currently make at this call site, so leaving it to the DESIGN means the
   branch gets chosen in code review rather than here.
3. **Audit the "as today" claims in R10 against the actual commands.**
   Rationale: one of the five (`create`) is already wrong, and "as today" is
   load-bearing three times in R10 and twice in R9. Each one is a place where a
   developer will trust the PRD instead of running the command.
4. **Say once, near R9, which outputs are session-path-only.** Rationale: the
   enriched destroyed line, the `session: nothing to destroy` lines and the new
   exit codes 3 and 4 all interact with R21's "existing invocations keep their
   output and exit codes", and the boundary is currently reconstructed from
   three separate sentences.
5. **Give R17 an exit code and add it to R21's exemption list.** Rationale: two
   one-clause edits close the last outright contradiction between the
   concurrency half and the compatibility half of the document.

## Summary
This is a strong PRD: the outcome table, the per-command root table, the Terms
section, the forced-interleaving criteria and the explicit scope-outs make most
of it genuinely unambiguous, and every round-1 and re-review finding is closed.
It fails on three things a developer would have to decide alone — whether
`--force` applies to a session-resolved destroy (which can silently delete the
unmerged branches the feature exists to protect), how a multi-instance
workspace is distinguished from a single-instance one, and what `niwa worktree
create` actually does at the root today — plus a handful of smaller
requirement-versus-criterion mismatches. Each is a one-or-two-sentence fix; none
requires rethinking the feature.
