---
schema: plan/v1
status: Active
execution_mode: coordinated
split_mode_source: intent
split_rationale: |
  Hard Constraint. PRD R17 makes the turn-on its own change, merged only after
  the changes for R12, R13 and R15, and R16 keeps borrowing off in every
  release build until it lands, so the turn-on cannot share a pull request with
  the work it waits on. The three mirror fixes (niwa#524, niwa#522, niwa#525)
  each repair the shipped mirror for users who don't borrow yet, so they split
  on Incremental Value too.
tracking_level: none
upstream: docs/designs/DESIGN-reference-clones.md
milestone: "reference-clones"
issue_count: 8
---

# PLAN: reference-clones

## Status

Active

## Scope Summary

New niwa instances clone each repo borrowing objects from the host's existing
per-repo mirror through a stable-path alternates line, with a fallback to a
full clone, a workspace and per-repo off setting, a command that makes an
instance independent of the mirror, safe retirement of old mirror stores, and
three mirror fixes. Everything stays off in release builds until a last
turn-on change.

## Decomposition Strategy

**Horizontal**, one issue per design phase, in the design's order. The design
changes existing mirror and clone code in eight ordered phases whose
dependencies it states: the borrowing create comes after store discovery and
the setting, the rerun after the create, and the turn-on last. Everything
before the turn-on ships dark behind a build tag, so there's no end-to-end user
path to build a walking skeleton on before the last issue.

The three mirror fixes come first and each stands alone: they repair the
mirror that already ships, for users who don't borrow yet. The setting and
release gate, the borrowing create, `niwa dissociate` with the guide, and the
mirror-repair rerun ship dark in one group. The turn-on is last, behind a gate
on the fixes, the guide and the rerun. All work lands in tsukumogami/niwa, one
pull request per group.

## Issue Outlines

### Issue 1: fix(mirror): keep remote-access variables on mirror network calls

**Repo**: tsukumogami/niwa

**Group**: mirror-env

**Goal**: Make the mirror's seed, refresh, restore and fetch-by-id keep the user's remote-access variables while every local mirror call keeps stripping all inherited `GIT_*` variables, and land the test-build hooks later work relies on.

**Acceptance Criteria**:
- [ ] `GIT_CONFIG_KEY_<n>`/`GIT_CONFIG_VALUE_<n>` entries pass to a mirror network call only when the key, compared case-folded, is under `http.`, `https.`, `credential.`, `protocol.` or `ssh.`, or is `url.<base>.insteadOf`, `core.sshCommand`, `core.gitProxy` or `core.askPass`. Every other entry is dropped, `include.*` and `includeIf.*` among them (an include makes git read another config file, which could set any key past the filter) and `remote.*` too (it could change the mirror's refspec or prune it and lose the refs that keep removed commits recoverable). The kept entries are renumbered from 0 and `GIT_CONFIG_COUNT` is set to how many were kept. Unit tests in `internal/mirror` cover mixed-case keys (`HTTP.ExtraHeader` kept, `Include.Path` dropped), a dropped entry between two kept ones, and `gc.*`, `fetch.prune`, `core.hooksPath` and `core.fsmonitor` entries dropped. (R12)
- [ ] A `GIT_CONFIG_COUNT` that isn't a plain decimal git would read, or is above 64, drops the count and every `GIT_CONFIG_KEY_<n>`/`GIT_CONFIG_VALUE_<n>` entry; unit tests cover `abc`, `-1`, `65` and a count larger than the number of entries present. (R12)
- [ ] Every mirror network call (clone, fetch, fetch by id) runs with `GIT_TERMINAL_PROMPT=0`, which niwa sets itself whatever the user's environment holds. It sits outside R12's inherited list, and credential helpers and `GIT_ASKPASS` still work. (R12)
- [ ] With every variable R12 lists set in niwa's environment, a functional scenario using the test-build environment probe shows each of them, with its value, on the probe lines of kind `network` for the seed, refresh, restore and fetch by id, and no other inherited `GIT_*` variable on any line. Probe lines of kind `local` show no inherited `GIT_*` variable at all. (R12)
- [ ] The same probe scenario pins the config filter to an allowlisted key. It sets `GIT_CONFIG_COUNT=2` with one allowlisted entry (for example `http.extraHeader` or `url.<base>.insteadOf`) and one dropped entry (for example `remote.origin.prune` or `include.path`). The network probe lines show `GIT_CONFIG_COUNT=1` with the allowlisted key and value at index 0 and never the dropped key. The scenario also sets an inherited `GIT_TERMINAL_PROMPT=1`, and every network line shows `GIT_TERMINAL_PROMPT=0`. (R12)
- [ ] A functional scenario puts the sandbox remote behind an SSH-style wrapper reached only through `GIT_SSH_COMMAND`. The repo's URL is reachable only through that wrapper, which records each call to a file in the sandbox. The scenario seeds the mirror, deletes it, and sees the next command restore it, with the wrapper's file showing it was invoked for the restore. The borrowing create lands in a later issue, so this scenario may use a hand-made borrower or no borrower at all. (R12)
- [ ] With `GIT_DIR` and `GIT_ALTERNATE_OBJECT_DIRECTORIES` set to unrelated paths in niwa's environment, the healthy check of a mirror with a borrower still reports healthy, and the probe's `local` lines show neither variable set. (R12)
- [ ] No environment value is written to the mirror log: a scenario that sets a sentinel value in `GIT_SSH_COMMAND`, `GIT_ASKPASS` and an allowlisted `GIT_CONFIG_VALUE_0` finds none of the sentinels in the log after a seed and a restore. When a network call honoured `GIT_SSL_NO_VERIFY` or a proxy command, the log says so without the value. (R12)
- [ ] `internal/testhook` provides `Fault(label) error` (calls `testfault.Maybe` in the tagged build; labels `borrow-clone` and `dissociate-copy`), the `NIWA_TEST_MIRROR_LOCK_WAIT=<duration>` override (shortens the mirror's five-minute and ten-second lock waits and never lengthens them), and the `NIWA_TEST_GIT_ENV_PROBE=<file>` probe (one JSON line per mirror git process with its kind, `network` or `local`, and the `GIT_*` entries it was given). Each hook has a `//go:build niwa_functional` file and an untagged no-op twin. (R16, R20)
- [ ] Three untagged unit tests pass under `go test ./...`. One sets all three `NIWA_TEST_*` variables and shows the mirror's behaviour unchanged, with no probe file written. One parses the package sources and fails if any file without the `niwa_functional` tag names those variables. One fails if any package calls `testfault.Maybe` with the new labels directly instead of through `internal/testhook`. (R16)
- [ ] The functional scenarios run on Ubuntu and macOS under the suite's sentinels, with no network, and write nothing outside the sandbox `$HOME`. The probe file lives inside the sandbox. (R20)

**Dependencies**: None

### Issue 2: fix(mirror): treat two spellings of one remote as the same remote

**Repo**: tsukumogami/niwa

**Group**: mirror-identity

**Goal**: Make the mirror treat the SSH and HTTPS spellings of one remote, or a change in its user part alone, as the same remote, so a restore after such a change still salvages the old store (niwa#522).

**Acceptance Criteria**:
- [ ] A table test in `internal/mirror` shows `git@github.com:o/r.git`, `ssh://git@github.com/o/r`, `ssh://git@github.com:22/o/r.git`, `https://github.com/o/r.git/`, `https://alice:tok@GitHub.com:443/o/r` and `http://github.com:80/o/r` all yield the identity `github.com/o/r` (R14).
- [ ] The same test shows a non-default port is kept (`ssh://git@host:2222/o/r` yields `host:2222/o/r`, distinct from `host/o/r`), and that a different host or a different path yields a different identity (R14).
- [ ] A plain filesystem path and a `file://` URL yield an identity from their path with the trailing `.git` and slashes removed, so the sandbox remotes the functional suite uses keep comparing as they do today and every existing scenario in `mirror-repair.feature` still passes.
- [ ] After a seed, a refresh and a restore, the mirror's state file holds the stripped URL (no userinfo, query or fragment, as `recordURL` writes it today) and an `identity` field equal to `remoteIdentity` of the config's URL.
- [ ] A state file written in the v0.43.0 format, with no `identity` field, is read without error, its identity is computed from the stored URL, and a healthy check against the SSH spelling of that same remote reports no fault; the next write of the state file adds the field.
- [ ] With only the spelling of a repo's URL changed (SSH to HTTPS, or the user part alone, such as `git@host:o/r` to `deploy@host:o/r`), the healthy check reports healthy rather than `url-changed`, the check makes no network call, and after the next refresh `git -C <stable path> config remote.origin.url` prints the config's new spelling (R14).
- [ ] A functional scenario changes a repo's URL from its SSH form to its HTTPS form for the same sandbox remote, with both spellings reaching that remote inside the sandbox and no network (R20), then damages the mirror; the next command's restore names the damage fault rather than `url-changed`, the mirror log shows salvage from the old store, and a borrower ref whose commits the remote no longer serves reads to the root afterwards (R14).
- [ ] A functional scenario points the repo at a different sandbox remote; the next command rebuilds with fault `url-changed` and the mirror log shows no salvage from the old store (R14).
- [ ] A functional scenario changes only the user part of the repo's URL, damages the mirror, and sees the restore salvage the old store exactly as in the SSH-to-HTTPS scenario (R14).
- [ ] `internal/mirror` holds exactly one `remoteIdentity`, and `urlChanged` calls it; if <<ISSUE:3>> landed first, this issue reuses its function and its salvage filter is left comparing through the same one.

**Note**: Issue 1's branch gives the `plugin-release-hold.feature` source repo fixture a commit, since an empty source repo breaks `niwa worktree create` on git older than 2.42 (Apple git 2.39 on macOS). Until that branch merges, main still has the empty fixture, so the full functional suite on macOS fails those scenarios on this branch too. If local verification fails there, carry the same one-file fixture change here; it is identical to Issue 1's, so the two merge cleanly in either order.

**Dependencies**: None

### Issue 3: fix(mirror): keep old stores until a strict scan proves no borrower names them

**Repo**: tsukumogami/niwa

**Group**: mirror-retention

**Goal**: Make a mirror restore keep every old store, record it in `.meta/<repo-id>.stores`, and leave its removal to a sweep that removes a retired store only after a strict scan of every instance on disk shows no alternates line resolves into it, which fixes niwa#525. It waits on Issue 1 for `internal/testhook`'s fault labels and lock-wait override, and on Issue 2 for `remoteIdentity(url)` and the per-store identity the `.stores` record holds.

**Acceptance Criteria**:
- [ ] Unit tests for `hostdir.Clones` and `hostdir.StrictClones` show both list every clone two levels under each directory from `hostdir.InstanceDirs` (root children except `.niwa`, plus every real directory under `~/.niwa/w-<key>/kept/`), including an instance with no state file and a repo the config doesn't name. `StrictClones` returns an error, and `Clones` doesn't, for each of: an unreadable root, `kept/`, instance or group directory; a symlink or non-directory where an instance or group directory belongs; a `.git` that is a symlink or gitfile; a symlinked `.git/objects/info`; an alternates file that isn't regular, is over 64 KiB or holds a quoted or unparseable line; and a directory removed between listing and reading. (R7, R9)
- [ ] Unit tests for `hostdir.Pins` show a store pinned, by `os.SameFile` on its `objects` directory, by a line naming the stable path, by a line naming the `.store/` directory directly, by a relative line, and by a leftover `alternates.niwa-dissociate`; blank and `#` lines are skipped and a missing alternates file pins nothing. (R9)
- [ ] With a borrowing instance moved under `kept/` and a half-created instance directory with no state file beside the others, a restore's probe lists both instances' clones in the mirror log; the same holds for the check run by `niwa apply`, the SessionStart hook's recorded-mirrors check and `niwa worktree create`, each of which passes the whole lenient walk rather than a config-derived or single-clone list. (R7)
- [ ] After a restore, `.meta/<repo-id>.stores` exists with mode 0600 and holds one entry per store (name without the `.store/` prefix, stripped URL, identity, `linked`, and `retired_at` for the replaced one), and both the old and new store directories are still on disk. A store whose entry says it was never linked is removed under the lock with no scan; on the first run against a v0.43.0 mirror with no record, the linked store's entry is seeded from the state file. (R9, R13)
- [ ] With `kept/` made unreadable (mode 000) and the record's `retired_at` and `last_swept` moved back past an hour, a restore followed by the next command's sweep removes no store, and the record still lists the retired store. A unit test shows a nil `Options.Scan`, or a scan that returns an error, removes nothing. (R9)
- [ ] A clone whose alternates names a `.store/` directory directly keeps that store on disk through a restore, through a rebuild for a changed remote, and through a sweep with the record aged past an hour. (R9)
- [ ] With the record aged past an hour and no clone pinning the retired store, the next `EnsureHealthy` removes it by exact name, leaves the linked store, and updates `last_swept`; a retired store younger than an hour stays; a second call within the hour runs no scan; with the mirror lock held by another process the sweep is skipped. A recorded name that doesn't match `<repo-id>-<UTC stamp>-<rand>` for the validated repo id, or that `Lstat` shows as a symlink or outside a real `.store/`, is left on disk and logged. (R9)
- [ ] When the restore's free-space check would refuse, the restore runs the strict scan under its lock, removes retired stores no line pins regardless of age, keeps a pinned one, checks again and proceeds; a unit test drives this through an injected free-space reading. (R9)
- [ ] After a url-changed rebuild leaves the old remote's store retired, a later restore of the new remote salvages nothing from it, and the mirror log names no salvage from a store with no record entry. (R9, R13)
- [ ] A functional scenario truncates an old store's pack halfway, the first half holding every object one borrower ref needs and the second half an object another ref needs, with the remote no longer serving either. After the restore the first ref's `git log` reads to the root, the second ref is reported unrecoverable by name, and in that restore's mirror log every probe line comes before any line removing a store. The temporary object directory under the mirror's `.tmp/` is gone afterwards. (R13)
- [ ] Destroying a workspace with no kept instances removes `mirrors/` (and the key directory when that leaves it empty). With a kept borrowing instance it keeps that mirror and prints its path with "a kept instance borrows from it"; with the mirror lock held it keeps it and prints "busy"; with `kept/` unreadable it keeps it and prints "kept instances couldn't be read". A `.store/` directory no record names is left and reported, never removed by glob. (R10)
- [ ] `go test ./...` and the functional suite pass on Ubuntu and macOS, with the new scenarios writing nothing outside the sandbox `$HOME`. (R20)

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>

### Issue 4: feat(config): add share_git_history and the release-build gate

**Repo**: tsukumogami/niwa

**Group**: borrowing

**Goal**: Add the `share_git_history` setting at workspace and repo level with its repo-then-workspace-then-on cascade, and a build-tag constant that holds borrowing off in every release build whatever the setting says.

**Acceptance Criteria**:
- [ ] A workspace config with `[workspace] share_git_history = false` and `[repos.app] share_git_history = true` loads with `WorkspaceMeta.ShareGitHistory` and `RepoOverride.ShareGitHistory` non-nil and holding those values; a config without the key loads with both nil. (R6)
- [ ] A table test of `ShareGitHistoryEnabled(repo)` over all nine combinations of repo and workspace values (unset, true, false) shows the repo's value winning in both directions when set, the workspace's value used when the repo's is unset, and true when both are unset. (R6)
- [ ] A unit test in `internal/vault/resolve` shows a `RepoOverride` with `ShareGitHistory` set comes out of `deepCopyRepos` with the same value, and that changing the copy's value leaves the original unchanged. (R6)
- [ ] A `MergeWorkspaceOverlay` test shows an overlay repo the base config doesn't name carries its `share_git_history` into the merged config, and a repo named in both follows the same rule as the other `*bool` repo fields. (R6)
- [ ] `go test ./internal/workspace/` (no tags) sees `borrowReleased == false`, and `go test -tags niwa_functional ./internal/workspace/` sees `true`; in the tagged build `borrowEnabled` returns exactly what the cascade returns for each of the nine combinations. (R16)
- [ ] An untagged unit test shows borrowing disabled both with no `share_git_history` anywhere and with it set to true at workspace and repo level: `borrowEnabled` is false for every repo, and a create against a sandbox remote leaves no `.git/objects/info/alternates` in any clone and creates no `mirrors/` entry under `~/.niwa/w-<key>/`. (R16)
- [ ] An untagged test sets `NIWA_TEST_FAULT` with the `borrow-clone` label, `NIWA_TEST_MIRROR_LOCK_WAIT` and `NIWA_TEST_GIT_ENV_PROBE`, runs a create, and shows the same clones and exit status as without them, with no probe file written. (R16)
- [ ] `TestGuard_NoReleaseBuildFileNamesTheTestHookVariables` in `internal/testhook/guard_test.go`, which landed with Issue 1, still parses the module's Go sources and fails if any file without the `niwa_functional` build tag names `NIWA_TEST_MIRROR_LOCK_WAIT` or `NIWA_TEST_GIT_ENV_PROBE`. This item reuses that guard rather than adding a second one, and the guard passes with the new code in place. (R16)
- [ ] A CI check fails when the release configuration (`.goreleaser.yaml`) passes `-tags niwa_functional` to any build; it passes on this branch and exits non-zero against a copy of the file with the tag added. (R16)
- [ ] `niwa version` from the `make build-test` binary prints "test build", and from a plain `go build ./cmd/niwa` binary it does not. (R16)
- [ ] `go vet ./...` and `go test ./...` pass both with no tags and with `-tags niwa_functional`.

**Dependencies**: Blocked by <<ISSUE:1>>

### Issue 5: feat(workspace): clone new repos borrowing from the host mirror

**Repo**: tsukumogami/niwa

**Group**: borrowing

**Goal**: Make a new clone borrow objects from the repo's host mirror through its stable path, falling back to a full clone with a named reason whenever borrowing can't happen, on every path that provisions an instance, in the test build only.

**Acceptance Criteria**:
- [ ] On a fresh sandbox host, `niwa create` of a three-repo workspace in the test build seeds a mirror per repo under `~/.niwa/w-<key>/mirrors/`, and each clone's `.git/objects/info/alternates` holds exactly one line, `<stable path>/objects`, where the stable path is the symlink and not its `.store/` target. (R1, R2, R3)
- [ ] The same alternates check passes for a repo cloned by `niwa apply` after the repo is added to the config, by `niwa dispatch`, by the SessionStart hook's provisioning (`niwa instance from-hook`) and by `niwa init` when it creates an instance. (R1)
- [ ] With a repo's mirror deleted before a second `niwa create`, the create's output and the mirror log show the restore before the clone, the restore's probe lists the first instance's clones, and the new clone's alternates names the restored mirror's stable path. (R1)
- [ ] A borrowing clone and a full clone of the same repo at the same commit have the same `git remote get-url origin`, the same `git for-each-ref` output and the same `git config --local --list` output, and neither has a file under `.git/hooks/` the other lacks. (R3)
- [ ] With one repo's mirror deleted and its seed made to fail while that repo's sandbox remote still serves a full clone (a stopped remote would fail the full clone too), `niwa create` exits 0, clones the other repos borrowing, and prints a warning naming that repo with reason `mirror-seed-failed`; that repo's clone has no alternates file. (R4)
- [ ] With another process holding a repo's mirror lock past the wait set by `NIWA_TEST_MIRROR_LOCK_WAIT`, `niwa create` clones that repo in full and prints a warning naming it with reason `mirror-busy`; the SessionStart hook's provisioning does the same after its ten-second wait, and the hook's JSON output carries that line in `additionalContext`. (R4)
- [ ] With the `borrow-clone` fault label set, the create exits 0, the repo's clone is a full clone with no alternates file, and no partial or temporary directory is left beside it. Unit tests show that a stable link whose target changes between the two reads yields one borrowing retry and then a full clone with reason `store-changed`, that a missing alternates file or a read-back mismatch yields a full clone with reason `borrow-clone-failed`, and that a repo name failing the repo-id pattern is cloned in full with a notice reading "a repo whose name niwa can't mirror" instead of the name. (R4)
- [ ] With the workspace setting off, a create makes full clones, writes no alternates file and creates no `mirrors/` entry; with the workspace off and one repo on, only that repo borrows; with the workspace on and one repo off, that repo is a full clone with no mirror and no notice while the others borrow. (R6)
- [ ] Three `niwa create` runs of the same workspace started together on a host with no mirror leave one stable path and one store per repo, and every clone either borrows from it or is a full clone named in that create's output. (R18)
- [ ] An instance created with borrowing off, then synced and applied with borrowing on, has no alternates file in any clone. In a borrowing instance after create, `niwa apply` and a sync, no clone holds a pack file whose name a mirror store holds, and `git config --local --list` shows no `gc.` or `maintenance.` key. (R5, R19, R21)
- [ ] In the untagged release build, a create with `share_git_history` set to on, and with it unset, makes full clones, seeds no mirror and prints no fallback notice, and every path above reaches borrowing only through `borrowEnabled`. (R16)
- [ ] Must deliver: test-build `niwa create` that produces borrowing clones with the single stable-path alternates line, plus reusable functional steps that create a borrowing instance and assert a clone does or doesn't borrow (required by <<ISSUE:6>>)
- [ ] Must deliver: create-made borrowers in the test build on Linux and macOS, reachable from a step the mirror-repair feature can call in place of its hand-written alternates step (required by <<ISSUE:7>>)

**Dependencies**: Blocked by <<ISSUE:3>>, <<ISSUE:4>>

### Issue 6: feat(cli): add niwa dissociate and the reference-clones guide

**Repo**: tsukumogami/niwa

**Group**: borrowing

**Goal**: Add `niwa dissociate [instance]`, which copies every borrowed object into an instance's clones and drops their alternates files only after `git fsck` passes with the mirror out of reach, and ship the user guide section on reference clones.

**Acceptance Criteria**:
- [ ] In the test build, after `niwa dissociate <instance>` on a borrowing three-repo instance with a linked worktree of one clone, and with the mirror then deleted, `git log --all` and `git fsck` exit 0 in each clone and in the worktree, no clone has `.git/objects/info/alternates` or `alternates.niwa-dissociate`, and the output lists each clone it changed. (R8)
- [ ] Run from inside an instance with no argument, `niwa dissociate` acts on that instance; a second run prints "nothing to change" and exits 0, and a clone that never borrowed is left byte-for-byte unchanged and not listed. (R8)
- [ ] With another niwa command holding the instance's lock, `niwa dissociate` exits non-zero, names the lock as the reason, and every clone's alternates file and pack directory are unchanged. (R8)
- [ ] With free space on the instance's volume below the summed mirror pack sizes for the instance's repos, `niwa dissociate` exits non-zero before any repack, and every clone's alternates file and pack directory are unchanged. (R8)
- [ ] With the test build's `dissociate-copy` fault label failing the copy for the second of three clones, the command exits non-zero, all three clones still have their alternates file, and `git fsck` passes in each with the mirror present. (R8)
- [ ] With an `alternates.niwa-dissociate` file left in a clone in place of its alternates file, the next `niwa dissociate` runs `git fsck` for that clone and removes the file on a pass, or renames it back to `alternates` on a failure. (R8)
- [ ] A clone whose alternates line resolves outside this workspace's mirror stores is reported by name and left with its alternates file unchanged. (R8)
- [ ] With a repo's `share_git_history` set to false after an instance borrowed from it, deleting that repo's mirror and running `niwa apply` restores the mirror; after `niwa dissociate` on that instance, the next `niwa apply` neither checks nor restores that mirror, as the mirror log shows. (R6, R8)
- [ ] After `niwa dissociate`, `git config --local --list` in each changed clone shows no `gc.` or `maintenance.` key, and no other niwa command in the suite copies borrowed objects into a borrower. (R21)
- [ ] A section under `docs/guides/` names `~/.niwa/w-<key>/mirrors/` and gives the four purge steps in this order: rotate the secret; in each borrowing instance rewrite or delete local branches that reach the commit and run `git fetch --prune`; delete the repo's stable path and its `.store/<repo-id>-*` directories; ask the forge to drop the commit. It says why the order matters: a borrower ref that still reaches the commit lets the next restore fetch it back by id while the remote serves it. (R11)
- [ ] The same section documents `[workspace] share_git_history` and `[repos.<name>] share_git_history`, the repo value winning in both directions and the setting taking effect once borrowing ships, and documents `niwa dissociate`: run it before copying or archiving an instance or on a long-kept instance, a dissociated clone holds every object it reaches (a removed secret included, so purging it there needs a fresh clone), and a repo turned off keeps its mirror until its last borrower is dissociated. (R6, R8, R11)
- [ ] Must deliver: the guide section with the purge steps and `share_git_history` at both levels documented, and `niwa dissociate` merged, which together with the per-repo off setting are the conditions the mirror-retention decision was accepted on (required by <<ISSUE:8>>)

**Dependencies**: Blocked by <<ISSUE:5>>

### Issue 7: test(mirror): rerun mirror-repair proofs against create-made borrowers

**Repo**: tsukumogami/niwa

**Group**: borrowing

**Goal**: Make every scenario in `test/functional/features/mirror-repair.feature` take its borrowers from `niwa create` in the test build, with the hand-written alternates step removed, and keep the feature green on Ubuntu and macOS.

**Acceptance Criteria**:
- [ ] `mirror_steps_test.go` no longer defines or registers a step that writes an alternates file; `git grep -n 'alternates' test/functional/mirror_steps_test.go` finds no `os.WriteFile` (or other write) of an alternates path, and `git grep -n 'borrow from the mirror of' test/functional/` finds no step text. (R15)
- [ ] Every scenario in `mirror-repair.feature` gets its borrowers from `niwa create` (or `niwa init` creating an instance) run with the test-build binary, and each scenario first checks that each borrower's `.git/objects/info/alternates` holds exactly one line, `<stable path>/objects`, where the stable path is the symlink, not its target. (R3, R15)
- [ ] Each borrower used by a scenario holds no pack file whose name a mirror store holds, checked before the mirror is deleted or damaged, so the proof depends on the mirror being repaired rather than on objects the clone already had. (R15, R19)
- [ ] The deleted-mirror outline (every command row, the SessionStart hook rows included), the damaged-mirror outline, and the deleted-remote-commits proofs (the refresh keeps deleted commits, a branch on deleted commits survives while the remote serves them, a branch on destroyed commits is reported unrecoverable by name) pass against create-made borrowers, and after each restore `git log --all` and `git fsck` exit 0 in every borrower the scenario expects readable. (R15)
- [ ] The concurrency scenario (two commands restore one broken mirror once), the failed-restore scenario (nothing changes and the next command retries), the link-only relink scenario, the no-borrower scenario and the hostile hook and config scenario pass against create-made borrowers with their existing assertions unchanged in meaning. (R15)
- [ ] The healthy-check scenario passes against create-made borrowers with no network and its 100 ms bound kept as written. (R15)
- [ ] `make test-functional-mirror-repair` (which builds the `niwa_functional` test binary) exits 0 locally, and the CI functional jobs that run this feature pass on both the Ubuntu and macOS runners. (R15, R20)
- [ ] Every scenario in the feature ends with `nothing mirror-related exists outside the sandbox home` or runs under the suite's equivalent sentinel, and no scenario writes under the real `$HOME`, reaches the network, or uses anything but the suite's local git server. (R20)
- [ ] The feature's description no longer says that no create borrows; it states that borrowers come from `niwa create`.
- [ ] Must deliver: `mirror-repair.feature` running green in CI on Ubuntu and macOS against create-made borrowers, with no hand-written alternates step, merged to main (required by <<ISSUE:8>>)

**Dependencies**: Blocked by <<ISSUE:5>>

### Gate: turn-on-condition

**After**: Issue 1, Issue 3, Issue 6, Issue 7

**Before**: Issue 8

**Condition**: the changes for R12 (with its scenario), R13 and R15 and the guide with the per-repo off setting are merged on the default branch, and `mirror-repair.feature` passes there in CI on Ubuntu and macOS against borrowers made by `niwa create`.

### Issue 8: feat(workspace): turn on reference clones by default

**Repo**: tsukumogami/niwa

**Group**: turn-on

**Goal**: Make borrowing the default in release builds by deleting the `borrowReleased` build-tag constant, so a create borrows wherever a workspace root exists and a layout with no workspace root keeps full clones. The turn-on condition, stated as a requirement: niwa#524 is fixed with a scenario covering it, and the mirror-repair test is rerun against borrowers made by the real `niwa create`, before borrowing is on for any user. Gating on the niwa#525 fix as well is this project's own addition, stricter than that condition and sourced to R12, R13, R15, R16 and R17; it only restricts. The niwa#522 fix (Issue 2, R14) does not gate this change: if it slips, the turn-on goes ahead and niwa#522 stays a named limit.

**Acceptance Criteria**:
- [ ] `internal/workspace/borrow_release.go` and `internal/workspace/borrow_functional.go` no longer exist, and `git grep -n borrowReleased` exits 1; `borrowEnabled` returns the `ShareGitHistoryEnabled` cascade's value with nothing ANDed into it. (R17)
- [ ] In a build without the `niwa_functional` tag, `niwa create` of a workspace that has a workspace root and no `share_git_history` at either level leaves each clone's `.git/objects/info/alternates` holding exactly one line, `~/.niwa/w-<key>/mirrors/<repo-id>/objects`, and that stable path exists. A test in the untagged build checks this and replaces the earlier untagged test that asserted full clones. (R17)
- [ ] In the same untagged build, a create in a layout with no workspace root makes full clones: no clone has an alternates file and no `mirrors/` directory is created. (R17)
- [ ] In the same untagged build, `[workspace] share_git_history = false` gives full clones and no mirror for any repo; adding `[repos.<name>] share_git_history = true` makes that one repo borrow; and with no workspace setting, `[repos.<name>] share_git_history = false` gives that repo a full clone, no alternates file and no `mirrors/<repo-id>` entry while the other repos borrow. (R6)
- [ ] After upgrading to this build, `niwa apply` in an instance created before the turn-on leaves its existing clones with no alternates file. (R5)
- [ ] `go test ./...` passes, the test that fails when an untagged file names a `NIWA_TEST_*` variable still passes, the CI check that the release configuration never passes `-tags niwa_functional` still passes, and `niwa version` on a release build doesn't print "test build". (R16, R20)
- [ ] The guide section under `docs/guides/` says borrowing is on by default wherever a workspace root exists, that a layout with no workspace root keeps full clones, that clones made before the turn-on stay full clones, and how to turn borrowing off at the workspace and repo levels; no sentence says the setting takes effect only once borrowing ships. (R11, R17)
- [ ] This change merges only after the pull requests for <<ISSUE:1>>, <<ISSUE:3>>, <<ISSUE:7>> and <<ISSUE:6>> have merged: `git log --first-parent main` shows this change's commit after each of theirs, and until it merges the untagged test that asserts full clones passes on every main commit. (R16, R17)
- [ ] The pull request description lists, for the reviewer to accept: a store can't be removed sooner than an hour after it's replaced outside space pressure; a kept borrowing instance pins its store until it's dissociated or deleted; the strict scan grows with the number of instances on disk; a git command running longer than an hour across a restore can fail partway; `niwa dissociate` needs free space for each repo's whole history; and the design's "Residual risk the turn-on accepts" items (instances no longer hold separate copies of history, the mirror keeps commits removed from the remote, and `niwa dissociate` copies those retained objects into the clone it frees). (R17)
- [ ] If <<ISSUE:2>> has not merged when this change merges, the pull request names niwa#522 as a known limit (the SSH and HTTPS spellings of one remote compare as different remotes and the restore salvages nothing across that change) and does not wait on it. (R14, R17)

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:3>>, <<ISSUE:6>>, <<ISSUE:7>>

## Dependency Graph

```mermaid
graph TD
    I1["Issue 1: remote-access variables on mirror network calls"]
    I2["Issue 2: two spellings of one remote"]
    I3["Issue 3: keep old stores until a strict scan clears them"]
    I4["Issue 4: share_git_history and the release-build gate"]
    I5["Issue 5: clone new repos borrowing from the mirror"]
    I6["Issue 6: niwa dissociate and the guide"]
    I7["Issue 7: rerun mirror-repair against create-made borrowers"]
    I8["Issue 8: turn on reference clones by default"]

    I1 --> I3
    I2 --> I3
    I1 --> I4
    I3 --> I5
    I4 --> I5
    I5 --> I6
    I5 --> I7
    I1 --> I8
    I3 --> I8
    I6 --> I8
    I7 --> I8

    classDef done fill:#c8e6c9
    classDef ready fill:#bbdefb
    classDef blocked fill:#fff9c4
    classDef needsDesign fill:#e1bee7
    classDef needsPrd fill:#b3e5fc
    classDef needsSpike fill:#ffcdd2
    classDef needsDecision fill:#d1c4e9
    classDef tracksDesign fill:#FFE0B2,stroke:#F57C00,color:#000
    classDef tracksPlan fill:#FFE0B2,stroke:#F57C00,color:#000

    class I1,I2 ready
    class I3,I4,I5,I6,I7,I8 blocked
```

**Legend**: Green = done, Blue = ready, Yellow = blocked, Purple = needsDesign, Cyan = needsPrd, Red = needsSpike, Violet = needsDecision, Orange = tracksDesign/tracksPlan

The gate `turn-on-condition` sits between Issues 1, 3, 6 and 7 and Issue 8.

## Implementation Sequence

**Critical path**: Issue 1, Issue 3, Issue 5, Issue 7, Issue 8 (five issues).

**Parallel work**:

- Issues 1 and 2 start at once. Issue 2 gates Issue 3.
- After Issue 1, Issue 4 starts; Issue 3 starts once Issues 1 and 2 have landed.
- Issue 5 starts once Issues 3 and 4 have landed.
- After Issue 5, Issues 6 and 7 run in parallel.
- Issue 8 is last, after Issues 1, 3, 6 and 7 and the `turn-on-condition` gate.

**Complexity**: Issues 1, 3, 5 and 8 are critical: the environment filter, the
store retirement sweep, the borrowing create and the turn-on each change what
every user's clones depend on. Issues 2, 4, 6 and 7 are testable.

Issue 2 adds `remoteIdentity(url)` and the identity the mirror state
records; Issue 3 reuses both, so it waits on Issue 2.
