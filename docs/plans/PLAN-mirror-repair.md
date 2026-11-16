---
schema: plan/v2
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-mirror-repair.md
milestone: "niwa restores a deleted or damaged host mirror by itself"
step_count: 9
---

# PLAN: mirror-repair

## Status

Active

## Scope Summary

niwa keeps each host mirror behind a stable symlink, checks it offline on
create, apply, dispatch, worktree create and the SessionStart hook, and
restores a deleted or damaged mirror from the remote under a per-mirror lock
with no person acting. A functional feature file proves it with borrowers it
builds itself.

Nothing here builds reference clones or a borrowing create: that later work
consumes `EnsureHealthy`, `Seed` and the stable path this plan delivers.

## Decomposition Strategy

Walking skeleton, cut by behaviour. Step 1 runs the whole path for the
simplest fault: two borrowers built by the feature file, the mirror at the
stable path deleted, `niwa apply` run, and the mirror back with both borrowers
clean. An ordinary `niwa apply` reaches the new mirror check from
`runPipeline`, finds the deleted mirror, rebuilds it from the remote under the
waiting lock and swaps the stable link, so the risky joins (call site, lock,
link swap, borrower alternates through the stable path, the functional
harness) are all proven before any fault beyond deletion is handled.

Every later step adds one behaviour on top: damaged stores with the basic
mirror log, the other commands and the hook, concurrency and failure,
retention, the deleted-remote-commit case, log hardening and timing, first
build, and the hostile-input refusals. Tests and docs ride with the behaviour
they cover.

## Steps

### Step 1: A deleted mirror is restored by niwa apply and both borrowers work again

**Goal**: When the mirror that a workspace's borrowers name has been deleted, `niwa apply` puts it back before it clones anything: `runPipeline` in `internal/workspace/apply.go` passes the classified repos and the workspace's instance clone paths (`Options.Instances`) to `mirror.EnsureHealthy`, which sees the stable path `~/.niwa/w-<key>/mirrors/<repo-id>` missing, takes that mirror's lock with `WaitFileLock` from the new leaf package `internal/filelock` (which also takes today's flock helpers and `CanonicalPath`, with thin wrappers left in `internal/workspace`; `internal/mirror` imports `internal/filelock` and never `internal/workspace`), clones the store with `--mirror` from the workspace config's URL into `.store/`, makes `.link-<rand>` and renames it over the stable path, and reports a `Reporter` warning naming the mirror, the fault and the result. It runs on the functional sandbox with a local bare remote and two instances whose clones the scenario turns into borrowers (alternates naming `<stable path>/objects`, repacked with `git repack -a -d -l`), then deletes the mirror's link and store; only the missing fault and the apply call site are in scope, with no salvage, no refresh, no probe and no manifest beyond what the restore needs, and nothing here builds reference clones or a borrowing create.

**Acceptance Criteria**:

- [ ] With the mirror in place and before the deletion, `git count-objects -v` in each borrower shows the borrower holds none of the mirror's objects after the repack, so the borrowers depend on the mirror.
- [ ] With the stable link and its store deleted and before `niwa apply`, `git fsck --full` in a borrower exits non-zero and reports missing objects.
- [ ] `niwa apply` at the workspace root exits 0.
- [ ] After apply, the stable path is a symlink whose target is a relative `.store/<repo-id>-<UTC stamp>-<rand>` naming a directory under the sandbox `~/.niwa/w-<key>/mirrors/`, and `git rev-parse --verify --quiet HEAD^{commit}` run against that store exits 0.
- [ ] The restored store's `remote.origin.url` is the local bare remote's URL as the workspace config names it.
- [ ] In each borrower, `git log` on the default branch reaches the root commit and exits 0.
- [ ] In each borrower, `git fsck --full` exits 0 with no missing, broken or dangling-link lines.
- [ ] Each borrower's `objects/info/alternates` is byte-for-byte what it was before the deletion, still naming `<stable path>/objects`, and the borrower's refs, `HEAD`, index and working tree match the snapshot taken before the deletion.
- [ ] A commit made in one borrower and never pushed, plus an uncommitted edit beside it, are both still there after apply: the commit is reachable from its branch and `git status --porcelain` prints the same line as before.
- [ ] apply's stderr carries one warning that names the mirror's repo id, the fault `missing` and the result `restored`.
- [ ] After apply, no `.tmp/<repo-id>-*` directory and no `.link-*` entry is left under `mirrors/`.
- [ ] The workspace key directory has mode 0700 and the mirror's lock file under the sandbox `~/.niwa/locks/` has mode 0600.
- [ ] Nothing mirror-related exists outside the sandbox `~/.niwa/`: no `mirrors`, `.store` or `m-*.lock` path under the real home, the workspace root or either instance, and the suite's sentinel check passes.
- [ ] A second `niwa apply` straight after the first exits 0, prints no mirror warning, and leaves the stable link's target unchanged.
- [ ] A workspace with no borrowers gets no `~/.niwa/w-*/mirrors` directory and no mirror warning from `niwa create` or `niwa apply`, and `make test-functional-critical` passes as before.

**Check**: Run `make test-functional-mirror-repair` and see the scenario `A deleted mirror is restored by apply` pass: after `niwa apply` the stable path is a symlink into `.store/`, `git log` reaches the root commit and `git fsck --full` is clean in both borrowers, nothing in either borrower changed (their alternates already named the stable path), the unpushed commit and uncommitted edit are intact, and stderr carries the restore warning naming the mirror, `missing` and `restored`.

**Dependencies**: None

**Files**: `internal/mirror/`, `internal/filelock/`, `internal/workspace/file_lock.go`, `internal/workspace/file_lock_unix.go`, `internal/workspace/apply.go`, `test/functional/features/mirror-repair.feature`, `test/functional/mirror_steps_test.go`, `Makefile`

### Step 2: A damaged mirror is restored by an ordinary command, keeping every intact pack

**Goal**: A mirror whose store is damaged rather than deleted is caught by an offline check and rebuilt by the next `niwa apply`. The old store's intact packs and loose objects are carried into the new clone, and each check and restore is recorded in the mirror log, so both borrowers read again with nobody running a repair. It runs on step 1's setup (one mirror, two borrowers naming its stable path), with the store damaged four ways instead of deleted: object store emptied, a pack deleted, a pack truncated to half, and `HEAD` deleted.

**Acceptance Criteria**:

- [ ] After any build of a mirror (step 1's rebuild or this step's restore), `.meta/<repo-id>.json` exists with mode 0600 and lists every pack in the store with its name, its size in bytes, its ctime in nanoseconds and its trailing checksum, each matching what `stat` and the pack's last bytes show
- [ ] After a restore, `~/.niwa/w-<key>/mirrors/.meta/mirror.log` exists with mode 0600, and each check and each restore appends one JSON line naming the mirror, the event, the fault, the result and the check's duration in ms
- [ ] With the store's `objects/` directory emptied, `niwa apply` exits 0, the mirror log gains a `check` line with fault `objects-empty` and a `restore` line with result `restored`, and the command prints a warning naming the mirror, the fault and the result
- [ ] With one manifest pack deleted from the store, `niwa apply` exits 0 and logs fault `pack-missing` with result `restored`
- [ ] With one manifest pack truncated to half its size, `niwa apply` exits 0 and logs fault `pack-changed` with result `restored`
- [ ] With the store's `HEAD` deleted, `niwa apply` exits 0 and logs fault `repo-unreadable` with result `restored`
- [ ] With the state file deleted and the store intact, `niwa apply` exits 0, logs fault `missing` with result `restored`, and a new state file listing the new store's packs is in place afterwards
- [ ] After each of those restores, every ref in both borrowers reads to its root commit (`git rev-list --all` exits 0 in each) and `git fsck --full` in each borrower prints no errors and exits 0
- [ ] After each restore, each borrower's files are byte-identical to before the fault, its alternates file included, and the alternates line still names `<stable path>/objects`
- [ ] On a healthy mirror, `niwa apply` logs a `check` line with no fault and no `restore` line, and the stable path still links to the same store directory, so no restore ran
- [ ] A manifest pack whose ctime moved but whose bytes are intact (after a `chmod`) passes `git verify-pack`, and `niwa apply` logs no fault and no restore; a pack in the store that the manifest doesn't list causes no fault either
- [ ] In a restore from a store holding one intact pack, one truncated pack, one loose object that hashes correctly and one that doesn't, the new store holds the intact pack under its original name (passing `git verify-pack`) and the good loose object, and holds neither the truncated pack nor the bad loose object
- [ ] A restore copies nothing from the old store but regular pack, `.idx` and loose-object files: a symlink named like a pack is not followed or copied, and the old store's `config`, `hooks/`, `info/`, `objects/info/alternates` and `packed-refs` are absent from the new store
- [ ] After a restore, `.store/` holds the store the new link names and no store left from this repo id, while a directory there belonging to a different repo id is still present and unchanged
- [ ] `go test ./internal/mirror/...` passes, with a case for each of `objects-empty`, `pack-missing`, `pack-changed` and `repo-unreadable`, one for a lost state file reporting `missing`, and one for a healthy store reporting no fault

**Check**: Run `make test-functional-mirror-repair` and see the outline `A damaged mirror is restored by an ordinary command` pass for all four examples (empty object store, deleted pack, pack truncated to half, deleted HEAD): each scenario damages the store, runs only `niwa apply`, and then finds both borrowers reading every ref to its root with `git fsck --full` clean. The scenario has no repair step for the reviewer to run. Step 1's scenario `A deleted mirror is restored by apply` still passes in the same run. Then run `go test ./internal/mirror/...` and see the fault, lost state file, healthy and salvage cases pass.

**Dependencies**: Step 1

**Files**: `internal/mirror/state.go`, `internal/mirror/log.go`, `internal/mirror/check.go`, `internal/mirror/restore.go`, `internal/mirror/salvage.go`, `test/functional/features/mirror-repair.feature`, `test/functional/mirror_steps_test.go`

### Step 3: niwa create, niwa dispatch, niwa worktree create and the SessionStart hook restore a deleted mirror too, and the hook reports it in its context

The check runs from every call site the design names, and the hook's non-provisioning paths put the fixed-template notice in `additionalContext`.

### Step 4: Two commands racing on one broken mirror restore it once, and a restore that fails changes nothing and the next command retries

Lock wait and `busy`, the re-check under the lock, the free-space check, leftover `.tmp/` clearing, a failure at each restore step leaving the old link and store in place, the `url-changed` rebuild from the config's URL with no salvage, and the relink with no network when only the link is gone.

### Step 5: The mirror keeps commits the remote deleted across refreshes

No-prune refresh with gc and maintenance off, reflogs kept, `refs/niwa/kept/*` pins for salvaged refs, and the daily deep verify.

### Step 6: A borrower branch on pushed commits the remote deleted survives the mirror's loss while the remote serves them, and is otherwise reported unrecoverable

The read-only borrower probe, the bounded fetch by id and its pin, and `LostRef` reporting with the remote-tracking case kept separate.

### Step 7: The mirror log stays one line per event under hostile names and rotates at 1 MiB, and a healthy check stays offline and under 100 ms

The 256-byte string cuts, one `O_APPEND` write per line, rotation to `mirror.log.1`, a write failure kept to a warning, and the timing scenario.

### Step 8: Seed builds a repo's first mirror under the same lock and fixed config, ready for the reference-clone work

The `Seed` entry point and the contract the reference-clone work relies on, with nothing that borrows.

### Step 9: Planted symlinks, a hostile config, an outside link target and hostile ref or file names are refused, never followed or echoed raw

The Security Considerations defences: path and id validation, `O_NOFOLLOW`, mode tightening, the empty hooks path, and output escaping.

## Implementation Sequence

The steps run in order on one branch and land in one pull request. Step 2
needs step 1; once step 2 is done, steps 3, 4, 5 and 7 don't depend on each
other. Step 6 waits for step 5, and steps 8 and 9 wait for step 4. Each outline
step's dependencies are settled again when the plan's owner details it.
