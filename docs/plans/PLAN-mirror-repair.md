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

**Goal**: A mirror whose store is damaged rather than deleted is caught by an offline check and rebuilt by the next `niwa apply`, the old store's intact packs and loose objects are carried into the new clone, and each check and restore is recorded in the mirror log, so both borrowers read again with nobody running a repair. `check` moves out of `internal/mirror/mirror.go` into `internal/mirror/check.go` and keeps step 1's link and target validation, then reads local files only: it reports `FaultMissing` when `.meta/<repo-id>.json` is absent or unreadable or its `store` isn't the link's target; the new `FaultObjectsEmpty` when the store's `objects/` is missing or holds no file under `objects/pack/` and no loose object under `objects/<2 hex>/`; the new `FaultPackMissing` when a manifest pack or its `.idx` is gone; the new `FaultPackChanged` when a manifest pack's size or trailing checksum differs, or its ctime moved and `git verify-pack` on its `.idx` fails; and the new `FaultRepoUnreadable` when the store's `HEAD` or `config` is missing or `git rev-parse --verify --quiet HEAD^{commit}` against it fails. Packs the manifest doesn't list are ignored, and ctime is read with `unix.Lstat` from `golang.org/x/sys/unix` (already in `go.mod`), whose `Ctim` field is the same on Linux and macOS. `restore` keeps step 1's `filelock.WaitFileLock` wait and re-check under the lock, and after `cloneMirror` lands the clone in `.tmp/` it salvages into it from every `.store/<repo-id>-<stamp>-<rand>` directory that `storeNamePattern` matches (regular files by `Lstat` only, packs that pass `git verify-pack`, loose objects that hash to their name), writes the new state file to a temporary name in `.meta/`, renames the store into `.store/`, swaps the link with `swapLink`, renames the state file into place, and only then removes the old store directories by exact name, never the one the link names. The state file holds `schema` 1, `repo_id`, `url` (the config's URL with userinfo, query and fragment stripped), `store` and `packs` (`name`, `size`, `ctime_ns`, `trailer`). `Mirrored` also keeps a repo that has a state file. `Options` gains `Command`, which `ensureMirrors` in `internal/workspace/apply.go` sets to `create` or `apply`, and `EnsureHealthy` appends one JSON line per check and one per restore to `~/.niwa/w-<key>/mirrors/.meta/mirror.log` with `time`, `command`, `mirror`, `event`, `fault`, `result`, `check_ms` and, on a restore, `took_ms`; a failed log write doesn't change the `Result`. The `Reporter` warning stays step 1's `warning: mirror <id>: fault <fault>, result <result>`. It runs on step 1's functional setup (one mirror, two borrowers naming its stable path) with the store damaged four ways instead of deleted: object store emptied, the pack deleted, the pack truncated to half, and `HEAD` deleted. Relinking with no network, the free-space check, `.tmp/` clearing, `url-changed`, the fixed retention config and `refs/niwa/kept/*` pins, log string cuts and rotation, and moving a real directory at the stable path aside stay with later steps.

**Acceptance Criteria**:

- [ ] After any build of a mirror (the first one, which step 1's scenario gets from `niwa apply` once the borrowers name the stable path, or a restore), `.meta/` is a 0700 directory, `.meta/<repo-id>.json` is a 0600 file whose `store` equals the stable link's target, and its `packs` list every `.pack` in that store with its name, its size in bytes, its ctime in nanoseconds and its trailing checksum in hex, each matching what `stat` and the pack's last 20 bytes show; `.meta/` holds nothing but `<repo-id>.json` files and `mirror.log`.
- [ ] The state file's `url` never carries userinfo, a query or a fragment: the unit case for the function that produces it turns `https://user:tok@host/o/r.git?x=1#f` into `https://host/o/r.git` and leaves a URL with none of them unchanged, and the restored store's `remote.origin.url` is still the config's URL as step 1 left it.
- [ ] `.meta/mirror.log` is a 0600 file, and each `niwa apply` that finds the mirror appends exactly one `event` `check` line for it, plus exactly one `event` `restore` line when it restores; every line is one JSON object carrying `time`, `command` (`apply`), `mirror` (the repo id), `event`, `fault`, `result` and `check_ms`, and a restore line also carries `took_ms`.
- [ ] With the store's `objects/` directory emptied, `niwa apply` exits 0, the log gains a `check` line with fault `objects-empty` and a `restore` line with fault `objects-empty` and result `restored`, stderr carries exactly one warning `mirror app: fault objects-empty, result restored`, and the stable link names a different store than before.
- [ ] With the store's one pack file deleted and its `.idx` left, `niwa apply` exits 0 and the log, the warning and the moved link show fault `pack-missing` with result `restored`.
- [ ] With the store's one pack truncated to half its size, `niwa apply` exits 0 and the log, the warning and the moved link show fault `pack-changed` with result `restored`.
- [ ] With the store's `HEAD` deleted, `niwa apply` exits 0 and the log, the warning and the moved link show fault `repo-unreadable` with result `restored`.
- [ ] After each of those four restores, every ref in both borrowers reads to the root (`git rev-list --max-parents=0` on each ref from `git for-each-ref` names the source repo's root commit) and `git fsck --full` in each borrower exits 0 with no missing, broken or dangling-link lines.
- [ ] After each of those four restores, each borrower matches the snapshot taken before the fault (alternates byte for byte, still naming `<stable path>/objects`, plus refs, `HEAD`, index, status and tracked files), and the unpushed commit and the uncommitted edit are intact.
- [ ] On a healthy mirror, a second `niwa apply` exits 0, prints no mirror warning, appends one `check` line with an empty fault and result `healthy` and no `restore` line, and leaves the stable link's target and the state file's bytes unchanged.
- [ ] In `go test ./internal/mirror/...`, each fault the functional outline can't reach is its own case: a manifest pack's `.idx` deleted gives `pack-missing`; a pack's bytes overwritten mid-file at the same size gives `pack-changed`; the store's `config` deleted gives `repo-unreadable`; a deleted state file with the store intact gives `missing`, then `restored`, with a new state file listing the new store's packs; and a state file whose `store` names another directory gives `missing`.
- [ ] A manifest pack whose ctime moved after a `chmod` but whose bytes are intact gives no fault, no restore and an unchanged link, and an extra pack in the store that the manifest doesn't list gives no fault either.
- [ ] In a restore from an old store holding one intact pack, one truncated pack, an `.idx` whose `.pack` is gone, one loose object that hashes to its name and one that doesn't, the new store holds the intact pack under its original name (passing `git verify-pack`) and the good loose object, and none of the other three.
- [ ] A restore copies nothing from an old store but regular pack, `.idx` and loose-object files: a symlink named like a pack is neither followed nor copied, and a key planted in the old store's `config`, a file in its `hooks/` and `info/`, its `objects/info/alternates` and its `packed-refs` are absent from the new store.
- [ ] After a restore, `.store/` holds exactly one directory of this repo id, the one the link and the state file name: the damaged store and an unused `.store/app-<stamp>-<rand>` left beside it are salvaged from and removed, while a store directory of a repo id `app-extra` is still present and unchanged.
- [ ] `Mirrored` keeps a repo that has only a state file (no link, no store, no borrower), and still keeps nothing in a workspace with no borrowers.
- [ ] Step 1's scenarios `A deleted mirror is restored by apply` and `A workspace with no borrowers gets no mirror` still pass, and `make test-functional-critical` passes as before.

**Check**: Run `go test ./internal/mirror/... ./internal/workspace/...` and see the fault, lost and mismatched state file, moved-ctime, healthy, salvage and old-store cases pass, then `GOOS=linux go vet ./internal/mirror/...` and `GOOS=darwin go vet ./internal/mirror/...` so the ctime read builds on both. Then run `make test-functional-mirror-repair` and see the outline `A damaged mirror is restored by an ordinary command` pass for all four examples (empty object store, deleted pack, pack truncated to half, deleted HEAD): each scenario damages the store, runs only `niwa apply`, and then finds the log's `check` and `restore` lines and one warning naming the fault and `restored`, both borrowers reading every ref to the root with `git fsck --full` clean, and nothing in either borrower changed. The scenario has no repair step for the reviewer to run. Step 1's two scenarios still pass in the same run.

**Dependencies**: Step 1

**Files**: `internal/mirror/mirror.go`, `internal/mirror/check.go`, `internal/mirror/state.go`, `internal/mirror/log.go`, `internal/mirror/salvage.go`, `internal/mirror/restore.go`, `internal/mirror/layout.go`, `internal/mirror/git.go`, `internal/mirror/mirror_test.go`, `internal/workspace/apply.go`, `test/functional/features/mirror-repair.feature`, `test/functional/mirror_steps_test.go`

### Step 3: niwa create, niwa dispatch, niwa worktree create and the SessionStart hook restore a deleted mirror too, and the hook reports it in its context

`niwa create`, `niwa dispatch` and hook provisioning already reach the check through `runPipeline` since step 1; this step proves each with the deleted-mirror scenario outline, adds the check to `niwa worktree create` and to the hook's non-provisioning paths with a 10-second `LockWait`, sets `Options.Command` to `dispatch`, `from-hook` or `worktree` where it applies, and puts the fixed-template notice in `additionalContext`.

### Step 4: Two commands racing on one broken mirror restore it once, and a restore that fails changes nothing and the next command retries

The two-command race over step 1's lock wait, `busy` outcome and re-check under the lock, the free-space check, leftover `.tmp/` clearing, a failure at each restore step leaving the old link, store and state file in place, moving a real directory found at the stable path into `.store/` and salvaging it rather than reporting `restore-failed`, the `url-changed` rebuild from the config's URL with no salvage, and the relink with no network when only the link is gone.

### Step 5: The mirror keeps commits the remote deleted across refreshes

No-prune refresh with gc and maintenance off, reflogs kept, `refs/niwa/kept/*` pins for salvaged refs, and the daily deep verify.

### Step 6: A borrower branch on pushed commits the remote deleted survives the mirror's loss while the remote serves them, and is otherwise reported unrecoverable

The read-only borrower probe, the bounded fetch by id and its pin, and `LostRef` reporting with the remote-tracking case kept separate.

### Step 7: The mirror log stays one line per event under hostile names and rotates at 1 MiB, and a healthy check stays offline and under 100 ms

The 256-byte string cuts, one `O_APPEND` write per line, rotation to `mirror.log.1`, a write failure kept to a warning, and the timing scenario.

### Step 8: Seed builds a repo's first mirror under the same lock and fixed config, ready for the reference-clone work

The `Seed` entry point and the contract the reference-clone work relies on, with nothing that borrows.

### Step 9: Planted symlinks, a hostile config, an outside link target and hostile ref or file names are refused, never followed or echoed raw

The Security Considerations defences step 1 didn't land: `core.hooksPath` to `~/.niwa/empty-hooks/` and the local-read git environment (`GIT_CONFIG_NOSYSTEM`, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_OPTIONAL_LOCKS=0`, empty credential and ssh helpers), `O_NOFOLLOW` on the state file and log, mode tightening at each check, refusing planted symlinks in the store and `.meta/`, a hostile store config, and output escaping; repo id and link target validation, the outside-target refusal and the `GIT_*` scrub are already in place.

## Implementation Sequence

The steps run in order on one branch and land in one pull request. Step 2
needs step 1; once step 2 is done, steps 3, 4, 5 and 7 don't depend on each
other. Step 6 waits for step 5, and steps 8 and 9 wait for step 4. Each outline
step's dependencies are settled again when the plan's owner details it.
