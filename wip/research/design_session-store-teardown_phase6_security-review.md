# Security Review (Phase 6 juror)

Design under review: `docs/designs/DESIGN-session-store-teardown.md` (status Planned)
Upstream: `docs/prds/PRD-session-store-teardown.md`
Code baseline: the worktree at `public/niwa/.claude/worktrees/session-store-teardown`, Go 1.25.3.

The `## Security Considerations` section was drafted by an earlier security pass on
this same design and has had no independent reader. This review tests it rather
than accepting it. Every claim below was checked against the code; where I could
run an experiment instead of reasoning, I did (Go's `os.Rename`, `O_NOFOLLOW`,
flock, `%q`, `GOOS=windows go build`).

## Verdict: FAIL

The section asserts two concrete mitigations that exist nowhere — not in the code,
not in the design body — while the design simultaneously declares the package that
would have to carry them unchanged; and it names the overlay sibling-path collision
risk and then mitigates only one of the three paths that carry it.

## Findings

1. **The git-argument mitigations do not exist anywhere in the design or the code**: High
   -> Specify them in Solution Architecture and put them where the git calls are.

   The section states: "Before any git call it checks that each record's worktree
   path lies under the instance's worktree directory and that its branch name
   doesn't start with `-`."

   Neither check exists today. There is no containment assertion on
   `SessionLifecycleState.WorktreePath` anywhere in the repository — the string
   `<instance>/.niwa/worktrees` is only ever *constructed* for writes
   (`internal/worktree/worktree.go:195-199`), never used as a prefix test. The only
   validation on the lifecycle store is the session-**id** regex
   (`internal/worktree/session_lifecycle.go:17,22,75-77,97-99,126`); path fields are
   never checked. And there is no branch-name check: `internal/worktree/worktree.go:344`
   is

   ```go
   branchName := state.EffectiveBranchName()
   gitInvoker.CommandContext(ctx, "-C", repoPath, "branch", branchArg, branchName).Run()
   ```

   a raw JSON string as a positional `git branch` argument with no ref validation
   and no `--` end-of-options marker. Same shape at
   `internal/worktree/worktree.go:330`: `"worktree", "remove", "--force", worktreePath`.

   Worse, the design body never specifies either check. Decision 2 describes
   `destroySessionWorktrees` as "lists the instance's lifecycle records, keeps the
   active ones in worktree-id order, re-checks the instance directory with `Lstat`,
   and calls `worktree.DestroySession` for each" — no path or branch check. So the
   Security Considerations section is the *only* place these mitigations appear,
   which means an implementer following the design will not build them.

   The structural problem underneath: the design states "`internal/worktree` is
   unchanged and imports nothing new." The values are consumed at
   `worktree.go:330` and `:344`, inside `DestroySession`, which re-reads the record
   from disk itself. A check placed in `internal/cli` guards a value the callee
   independently re-derives, and leaves the pre-existing destroy-by-id and
   `WorktreeRemove`-hook paths (`internal/cli/session_from_hook_cmd.go:271`) with no
   check at all. These belong in `DestroySession`, next to the `exec` call —
   which means `internal/worktree` is not unchanged.

2. **Teardown by session multiplies reach across a store with zero field validation, and the guard that is supposed to bound it fails open**: High
   -> Validate lifecycle path fields at read time; make the dirty guard fail closed.

   The section's blast-radius argument is that teardown by session "applies the
   guards `niwa worktree destroy` already has to each active lifecycle record."
   The `--force` refusal is a genuine bound and I credit it. But one of those
   guards silently disarms. `worktreeHasUncommittedChanges`
   (`internal/worktree/worktree.go:83-95`):

   ```go
   if worktreePath == "" { return false, nil }
   if _, err := os.Stat(worktreePath); os.IsNotExist(err) { return false, nil }
   ```

   An empty or nonexistent `worktree_path` reports "clean", and nothing ties the
   inspected tree to the tree that `git worktree remove --force` then acts on at
   line 330. A record naming a different, real worktree of the same repo passes the
   dirty check against that other tree and has it force-removed.

   The change amplifies this. Today you must be inside an instance and name each
   8-hex id. After the change one command run from the workspace root, resolving
   through a same-user-writable mapping, destroys **every** active record in the
   instance in one pass. There is a clear asymmetry in the codebase that the
   section does not acknowledge: mapping consumers validate before use
   (`ValidSessionID` UUID regex at `session_map.go:129-134`, `IsSafeHandle` at
   `internal/watch/state.go:411-417`, `dispatchSessionNameRe`, `ValidateInstanceDir`
   at `internal/workspace/destroy.go:77-89`, `printableToken`/`shellToken` at
   `internal/cli/dispatch_reentry.go:115-150`). Lifecycle-record consumers validate
   nothing. The section writes a full paragraph on mapping untrustedness and then
   routes teardown into the store that has no validation at all.

3. **The overlay sibling-path collision is named and only one-third mitigated**: High
   -> Content-check `.prev` and `.trash-*` the way staging is content-checked, or namespace the auxiliary paths out of the overlay directory.

   The section raises this itself: "their clones are named `<org>-<repo>` in one
   per-user directory, so a repository named, say, `cfg.prev` or `cfg.next-x` has a
   name that matches another overlay's suffix." It then defends only the staging
   case, via the `lock` + `snap/` content check. That leaves:

   - **`.prev` (recovery rules 1, 2, 3)** — a fixed, unpredictable-suffix-free name.
     Rule 2 renames `D.prev` to trash and deletes it whenever `D` holds the snapshot,
     with no content check on `D.prev` at all. Rule 1 renames `D.prev` **back to `D`**
     when `D` is missing and `D.prev` "holds the snapshot" — and a legitimate overlay
     clone contains `workspace.toml`, so `HoldsSnapshot` is true for it by definition.
     That is one overlay's configuration being promoted as another's.
   - **`.trash-*` (recovery rule 5)** — "Leftover trash from a killed deleter is
     renamed into this run's trash list." Name-pattern match, no content check
     described, then deleted.

   The name really is attacker-influenceable. `config.OverlayDir`
   (`internal/config/overlay.go:328-341`) builds `dirName = org + "-" + repo` with
   no charset validation; `parseOrgRepo` (`overlay.go:259-318`) only rejects empty
   parts, strips a trailing `.git`, and (shorthand branch only) rejects `:` or a
   leading `/`. Dots and suffixes pass. Overlay `acme/tools.prev` lands at
   `overlays/acme-tools.prev`, byte-identical to `PrevPath("acme-tools")`. And the
   URL is not always user-typed: `apply.go:1094` derives it via
   `config.DeriveOverlayURL(opts.configSourceURL)` from whatever config source the
   workspace points at.

   Two of these are already live defects today (`snapshotwriter.go:358`
   `safeRemoveAll(staging)`, `snapshot.go:53` and `:98` `safeRemoveAll(prev)`), so
   this is not a regression — but the design keeps the fixed `.prev` name, adds
   `.trash-*` and `.stray-*` on top, adds recovery logic that *renames on the basis
   of the name*, and has a section that raises the exact risk and stops short.

4. **"Swapping the directory for a symlink after the check can't redirect teardown" is false**: Medium-High
   -> Use `os.Root` (already available; no new module) or drop the claim to "narrows the window".

   `Lstat`-then-use is a narrowed race, not a closed one. The claim is stated as a
   closure. `DestroySession` then re-opens every path by string —
   `ReadSessionLifecycleState`, `findRepoInWorkspace`, `git -C` — so the check and
   the uses are separated by several syscalls and a subprocess spawn.

   There is a real fix available with no new module: `go.mod` declares `go 1.25.3`
   and I verified `os.OpenRoot`, `Root.Lstat` and `Root.Rename` all work on this
   toolchain. An `os.Root` opened on the instance directory gives genuinely
   traversal-safe resolution for the record reads and the containment check.

5. **The exclusive lock is offered as the TOCTOU answer, and a planted name wedges recovery**: Medium
   -> Give `.stray-` a random suffix like trash/staging; handle a pre-existing rename destination explicitly.

   "All of recovery's check-then-act steps run with the exclusive lock held" is
   presented as the answer to TOCTOU on `.prev`/`.trash-*`/`.stray-*`. A lock
   excludes only processes that take that lock — not older niwa binaries (the design
   admits this), not other same-user processes (the threat model the section itself
   adopts for mappings), and not another user with write access to the parent
   directory. The filesystem-level `Lstat` -> uid-check -> `Rename` gap is untouched
   by it.

   I verified the consequence is worse than a redirect: Go's `os.Rename` refuses
   **any** existing destination, returning `EEXIST` even onto an empty directory.
   So a pre-planted entry at `<dir>.prev`, `<dir>.trash-X`, or the **predictable**
   `<dir>.stray-<timestamp>` does not redirect the rename — it makes it fail. Since
   `Recover` runs at the head of every `Mutate`, that wedges every mapping write,
   every watch-state write and every `instance.json` write on that directory. The
   timestamp suffix is the weak one: staging and trash get randomness
   (`StagingPattern`, `TrashPattern`), stray gets `StrayName` and a timestamp. It
   should get randomness too.

6. **The uid check as specified breaks the non-unix build — and that build is already broken**: Medium
   -> Put ownership checks in `configdir_unix.go` / `_other.go`; stop treating the PRD's Windows criterion as satisfiable as-is.

   No production code anywhere performs an ownership check today. The only
   `syscall.Stat_t` use is `internal/cli/sessionattach/attach.go:197-206`, and it
   only formats a diagnostic message. `syscall.Stat_t` does not exist on Windows,
   and the Key Interfaces list places `Recover` and `RecoverMovedAside` in an
   untagged `recover.go`.

   I ran the PRD's own platform criterion:

   ```
   $ GOOS=windows go build ./...
   internal/cli/sessionattach/attach.go:183: undefined: syscall.Flock
   internal/cli/sessionattach/attach.go:201: undefined: syscall.Stat_t
   internal/promptcapture/terminal.go:70:  undefined: syscall.SIGTSTP
   ... (8 more)
   ```

   `GOOS=windows go build ./...` already fails on today's code. The PRD acceptance
   criterion "`GOOS=windows go build ./...` succeeds" is unmet before this change,
   which means the entire non-unix fallback story — including the `_other.go` no-op
   the section points at — is unverifiable and untested.

7. **The non-unix no-op is a risk in disguise, not an accepted gap**: Medium
   -> State that `Recover` is skipped (or `Mutate` errors) on non-unix; do not let unsynchronized destructive renames ship behind a "no ordering" comment.

   The section dismisses this in one sentence: "Non-unix platforms get no ordering,
   as stated at the fallback." But the design says `configdir_other.go` "returns a
   no-op release." With `Acquire` a no-op, `Mutate` still runs `Recover` and `fn` —
   so on a non-unix platform the new **destructive, rename-based recovery runs with
   no exclusion whatsoever**. That is strictly worse than today, where nothing
   renames `.prev` back into place at all. The existing Codex-trust `_other.go`
   (`codex_trust_lock_other.go`) is safe to no-op precisely because the operation it
   guards is a single atomic replacement; this one guards a multi-step rename dance.
   The precedent does not transfer, and the design invokes it as if it does.

8. **The `%q` quoting claim is contradicted by the outcome contract and by the line R9 actually emits**: Medium
   -> Apply a `printableToken`-style filter (the repo already has one) rather than `%q`, and cover the branch-warning line.

   The mechanism is sound where applied — I verified `%q` escapes ESC, U+202E,
   U+200B and BEL:

   ```
   "a\x1b[31mred‮evil​zw\abel"
   ```

   But two things break the claim. First, `runSessionDestroy`
   (`internal/cli/session_lifecycle_cmd.go:539-595`) contains **no `%q` at all**
   today, and the PRD's R9 output table specifies literal unquoted placeholders
   (`session: destroyed <worktree-id> (<repo>) at <path>`). Quoting them with `%q`
   changes the bytes R9 and R21 pin.

   Second, and more directly: R9 routes the kept-branch warning to stderr as part of
   the **destroyed** outcome. That line is built at `internal/worktree/worktree.go:345-348`
   with `%s` three times and printed with a bare `fmt.Fprintln` at
   `session_lifecycle_cmd.go:589`. It is a paste-ready
   `git -C <repo> branch -D <branch>` carrying an unvalidated branch name, and
   teardown by session now emits one per worktree in the instance. The section's
   terminal-injection paragraph covers the values that matter least and misses the
   one the new feature multiplies. Note the dispatch path already guards exactly
   this class of data (`dispatch_reentry.go:115-150`); the destroy path does not.

9. **The snapshot carry-over still follows symlinks, in code this change reshapes**: Medium
   -> Switch the three `preserve*` probes to `Lstat`, and make `HoldsSnapshot` require a regular file.

   The section asserts the mapping store's modes survive the swap, and that is
   **true** — `preserveSessionMappings` explicitly re-tightens with
   `os.Chmod(dst, info.Mode().Perm())` at `snapshotwriter.go:578`. Credit where due.
   But it says nothing about link-following, and all three carry-over helpers follow:

   - `preserveInstanceState` — `os.ReadFile(src)` at `snapshotwriter.go:485`
   - `preserveDispatchBriefs` — `os.Stat(src)` at `:516` (not `Lstat`)
   - `preserveSessionMappings` — `os.Stat(src)` at `:556` (not `Lstat`)

   A symlink at `<configDir>/sessions` pointing at any readable directory passes the
   `IsDir()` gate and `copySubtree` walks the link target, whose contents are then
   promoted by the swap to *be* the mapping store.

   Relatedly, `HoldsSnapshot` is a presence check: `provenanceMarkerExists`
   (`snapshotwriter.go:113-116`) uses `os.Stat`, and `isWorkspaceRoot`
   (`state.go:321-324`) likewise. A symlink — or a directory — at
   `<dir>/.niwa-snapshot.toml` makes a directory "hold the snapshot", which is the
   sole content input to recovery rules 1, 2 and 3.

10. **The lock-file open is underspecified in a way that can defeat R17's bound**: Low-Medium
    -> Open with `O_NOFOLLOW|O_NONBLOCK` and `fstat` the fd; the repo already has this exact constant pair.

    Two verified facts. `O_NOFOLLOW` does what the section says for a symlink — the
    open fails with `ELOOP` and the target is **not** created. Good. But
    `O_CREATE|O_RDWR|O_NOFOLLOW` on a planted **FIFO succeeds** (I observed mode
    `prw-------`), so "must be a regular file" can only come from an `fstat` on the
    returned fd, and the design does not say so. If an implementer picks the natural
    `O_RDONLY` for a file never written, the open **blocks forever** on a FIFO,
    defeating R17's bound before any deadline logic runs.

    The repo already solves this: `internal/workspace/contextprobe_unix.go:11`
    defines `nofollowOpenFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK` with a
    `_other.go` fallback of `0`. Reuse it.

    Separately, a planted non-regular `<dir>.lock` is an unrecoverable hard error
    for every niwa command on that workspace. The Mitigations say lock files are
    "harmless if deleted between commands (they are recreated)" and say nothing
    about this case or the recovery path for it.

11. **The accepted starvation case is thinner than stated, and the design adds traffic to it**: Low-Medium
    -> Either add fairness (an intent flag or ticket), or drop watch-state writes from `Mutate`.

    The flock behaviour is exactly as feared — I verified that with `LOCK_SH` held,
    `LOCK_EX|LOCK_NB` fails while further `LOCK_SH|LOCK_NB` succeeds, so a stream of
    overlapping readers really can hold a writer off to the bound.

    Two things the acceptance understates. "Readers hold only for a directory scan"
    is not accurate: `Read` also spans `config.Load` in `ReconcileAndReloadConfig`
    and `NewStaging` (mkdir + `O_CREAT|O_EXCL` + flock). And the design newly routes
    **watch-state writes** through `Mutate` (`writeHandledState`, `SaveStagedRecord`,
    `internal/watch/state.go:160-202` and `:331-347`) — for state the PRD's own Known
    Limitations say no refresh carries across. That pays exclusive-lock traffic on
    the root `.niwa` for a guarantee the design explicitly does not provide: the
    write succeeds under the lock and the next swap discards it. A long-running
    `niwa watch` plus `niwa list` plus the SessionStart hook is precisely the
    "sustained concurrent traffic" the acceptance waves away.

12. **`config.Discover` becoming lock-taking and rename-capable has a wider blast radius than admitted**: Low-Medium
    -> Bound the repair branch per command, and say what happens if it is reached from inside a `Mutate`/`Read` callback.

    The Consequences call this "narrow", and the *trigger* genuinely is narrow
    (`workspace.toml` missing next to both `.niwa.lock` and `.niwa.prev`). The
    *exposure* is not. `config.Discover` has around twenty callers, including shell
    completion (`internal/cli/completion.go:41`), the SessionStart hook
    (`internal/cli/instance_from_hook.go:429`), `niwa watch`
    (`internal/cli/watch.go:1019`), and every status command. A TAB press can now
    block on a flock.

    Two gaps the DoS paragraph misses. The 30-second bound is **per acquisition**,
    not per command, and `Discover` walks up through multiple candidates — worst
    case N x 30s, so "No lock wait is unbounded" is true per lock and misleading per
    command. And the nested-acquisition detector turns any path that reaches
    `ClassifyCwd`/`Discover` from inside a `Mutate` or `Read` callback into an
    immediate hard failure, on the same lock path. The design asserts no nesting by
    inspecting today's callers; it does not say what the invariant is or how a future
    caller is stopped from breaking it.

13. **"One owner for the layout" is scoped narrower than the claim**: Low
    -> Say the ownership covers workspace config directories only.

    `internal/plugin/installer.go:136-176` (`stageAndRename`) runs the same
    `.next`/`.prev` dance with plain `os.RemoveAll` and `os.Stat` — no `Lstat`
    guard, no `safeRemoveAll` — and is not routed through `configdir`. The
    Consequences' "The swap's file names have one owner" overstates what lands.

14. **Constant duplication with a recovery-correctness consequence**: Low
    -> Name the source of truth for the two constants and add a drift test.

    `configdir` sits below `internal/config` and `internal/workspace` in the stated
    dependency graph, so `HoldsSnapshot` cannot import `workspace.ProvenanceFile`
    (`state.go:39`, `".niwa-snapshot.toml"`) or `config.ConfigFile`
    (`"workspace.toml"`) without a cycle. Both must be duplicated in `configdir`.
    The existing `sessionsDirName` comment in `session_map.go:112-117` warns about
    exactly this drift class for exactly this reason. Here the failure mode is
    recovery deciding the wrong side holds the snapshot — i.e. rules 1 and 3
    promoting `.prev` over a good `D`.

15. **"Keeps only mappings whose key is a valid UUID" describes a filter the mechanism cannot apply**: Low
    -> Say "whose `session_id` field is a valid UUID", or have the store return the filename.

    `ListSessionMappings` (`session_map.go:199-229`) never returns filenames; it
    returns each file's body-level `SessionID`. The filter can therefore only be on
    the JSON field, not the key. The wording implies a filename-derived trust the
    code does not have.

    In the same register: R6's "newest mapping" rule is decided entirely by the
    untrusted `created` field. It is a usability rail against acting on a superseded
    session, not a security control, and the "Scope of teardown by session"
    paragraph should not be read as if it bounds blast radius.

16. **Symlink-resolution asymmetry in the R7 instance check**: Low
    -> Resolve both sides, or neither.

    The section says the recorded path is accepted only if, "after symlinks are
    resolved, [it] is one of the directories `EnumerateInstances` returns".
    `EnumerateInstances` (`state.go:353-374`) returns **unresolved**
    `filepath.Join(workspaceRoot, entry.Name())` values, and skips symlinked children
    outright (`entry.IsDir()` has `Lstat` semantics). Comparing a resolved recorded
    path against unresolved enumerated paths mismatches wherever the workspace root
    itself sits under a symlink — macOS `/tmp` -> `/private/tmp` is the everyday
    case — turning legitimate teardowns into R7 rule-3 refusals.

17. **New sibling directories versus the root scanners are unstated**: Low
    -> State the invariant and pin it with a test.

    The Mitigations say lock files are "ignored by instance enumeration" but say
    nothing about `.niwa.next-*`, `.niwa.trash-*` and the deliberately kept
    `.niwa.stray-*`, all of which land as direct children of the workspace root
    where `EnumerateInstances` and the reaper scan. They are safe today by exactly
    one path segment: enumeration needs `<child>/.niwa/instance.json`, and the
    carry-over puts the file at `snap/instance.json`. Since this change reshapes the
    carry-over layout, that accident deserves to be an asserted invariant.

## Overclaims or gaps in the Security Considerations section

1. "Before any git call it checks that each record's worktree path lies under the
   instance's worktree directory and that its branch name doesn't start with `-`."
   -> Neither check exists in the code, and neither appears anywhere else in the
   design. The section is the sole source for mitigations an implementer following
   Solution Architecture will not build — and the design's "`internal/worktree` is
   unchanged" constraint puts them structurally out of reach of the call sites at
   `worktree.go:330` and `:344`.

2. "it is re-checked with `Lstat` right before each destroy, so swapping the
   directory for a symlink after the check can't redirect teardown."
   -> `Lstat`-then-use narrows a race; it does not close one. `DestroySession`
   re-resolves every path by string afterwards. Stated as a closure, it is false.
   `os.Root` (available on the declared Go 1.25.3) is what would make it true.

3. "All of recovery's check-then-act steps run with the exclusive lock held."
   -> Offered as the TOCTOU answer, but a lock only excludes processes that take it.
   It excludes neither older niwa binaries (the design concedes this elsewhere) nor
   other same-user processes — the very actors the section's own mapping paragraph
   declares untrusted. The threat model is applied to file *contents* and dropped
   for file *paths*.

4. "That matters for overlays: their clones are named `<org>-<repo>` ... so a
   repository named, say, `cfg.prev` or `cfg.next-x` has a name that matches another
   overlay's suffix."
   -> The section raises the risk and then mitigates only the staging half. `.prev`
   is destroyed by rule 2 with no content check and *promoted* by rule 1 (any real
   overlay clone satisfies `HoldsSnapshot`); `.trash-*` is swept by rule 5 on name
   alone. Naming the risk and covering a third of it reads as coverage.

5. "Every mapping-derived value printed in outcome, refusal or ambiguity lines is
   quoted with `%q`, so a crafted handle or path can't inject terminal control
   sequences."
   -> `%q` works (verified), but `runSessionDestroy` uses none today, R9's own
   output table specifies unquoted placeholders that `%q` would change, and the
   line R9 routes to stderr on the destroyed path — the kept-branch warning at
   `worktree.go:345-348`, a paste-ready `git branch -D` — is `%s` and bare
   `Fprintln`. The claim covers the values that matter least.

6. "`config.Discover` repairs a moved-aside `.niwa` only when the candidate
   directory, its `.niwa.lock` and its `.niwa.prev` belong to the current user ...
   never loads another user's directory as workspace configuration because of this
   branch."
   -> The ownership check is on the entries, but the guarantee needs the *parent* to
   be unwritable by others. In a user-owned but group- or world-writable directory,
   another user cannot forge a victim-owned entry but can rename one away between
   the check and the `os.Rename`. Narrow, and it should be stated as a residual
   rather than a closure. Separately, no production code performs any ownership
   check today, `syscall.Stat_t` does not exist on Windows, and `recover.go` is
   listed untagged.

7. "No lock wait is unbounded: a command that can't get the lock within 30 seconds
   fails."
   -> True per acquisition, misleading per command. `config.Discover` walks multiple
   candidates and can take the lock at more than one, so the worst case is a
   multiple of the bound; and the branch is now reachable from shell completion,
   the SessionStart hook and `niwa watch`.

8. "Non-unix platforms get no ordering, as stated at the fallback."
   -> Understates it. A no-op `Acquire` leaves `Mutate` still running `Recover`, so
   the new rename-based recovery runs with no exclusion — worse than today's
   behaviour, not merely unordered. And `GOOS=windows go build ./...` already fails,
   so the fallback is untestable and the PRD criterion it answers to is already
   unmet.

9. "The resolver keeps only mappings whose key is a valid UUID."
   -> `ListSessionMappings` returns no filenames, so only the body field can be
   filtered. Minor, but it implies a trust boundary the code does not draw.

10. "The mapping store keeps 0700 for `sessions/` and 0600 for its files ... through
    the snapshot carry-over, which already restores the source mode."
    -> This one is **correct** (`snapshotwriter.go:567,578`), and I note it as
    verified. The gap beside it is that the same three carry-over helpers follow
    symlinks (`os.Stat`/`os.ReadFile`, not `Lstat`), which the section does not
    mention while the design reshapes that code.

### What the section gets right

Worth recording so the rewrite does not lose it. The staging-liveness ordering is
genuinely sound: creating and locking staging under the shared lock while the
recovery sweep runs only under the exclusive lock means a sweep can never meet a
staging directory whose owner has not yet locked it — shared and exclusive are
mutually exclusive, so the window does not exist. The reasoning for a never-unlinked
sibling lock file is correct and matches the real flock inode hazard. The
`--force` refusal for session ids is a real bound on blast radius. `O_NOFOLLOW` on
a planted symlink behaves exactly as claimed (verified: `ELOOP`, target not
created). And the R22/R23 fix is better than the PRD advertises —
`saveWorkspaceRootDisclosures` (`apply.go:2742-2747`) currently writes a fresh empty
`InstanceState` whenever the root `LoadState` fails for any reason, clobbering
`overlay_url`, `SkipGlobal`, `NoOverlay` and `ConfigNameOverride`, not just
`ephemeral_session_mode`; the skip-after-failed-read rule closes all of it.

## Residual risk to escalate rather than accept

- Findings 1, 2 and 3 are not residual — they are gaps to close before this ships.
- Finding 7 (non-unix `Recover` running unsynchronized) should be an explicit design
  decision in the document, not a one-line dismissal.
- Finding 6 (the Windows build is already red) should be escalated to the PRD: an
  acceptance criterion that the repository does not currently satisfy cannot
  validate this change's platform story.

## Summary

The design's mechanism is sound in the places it was designed carefully — the
shared-lock staging handshake, the never-unlinked sibling lock, the `instance.json`
guards — and I verified those hold. The Security Considerations section, however,
is not a description of that mechanism. It asserts a worktree-path containment check
and a branch-name check that appear nowhere else in the design and nowhere in the
code, while the design declares the package that would have to carry them unchanged;
it presents `Lstat`-before-use and "we hold the lock" as closures of races they only
narrow; and it raises the overlay sibling-path collision by name and then defends
only the staging path, leaving the fixed `.prev` name to be deleted by one recovery
rule and promoted by another. Those three, plus the non-unix no-op quietly turning
"no ordering" into "unsynchronized destructive renames" on a build target that is
already red, are why this is a FAIL rather than a set of notes.
