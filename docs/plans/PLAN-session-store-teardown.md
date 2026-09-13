---
schema: plan/v1
status: Active
execution_mode: single-pr
tracking_level: none
upstream: docs/designs/DESIGN-session-store-teardown.md
milestone: "session-store-teardown"
issue_count: 7
---

# PLAN: session-store-teardown

## Status

Active

## Scope Summary

Order every refresh of a niwa config directory against the other refreshes and
against niwa's own writes and destructive mapping reads, and let
`niwa worktree destroy` resolve the session id or handle a developer holds to
that session's niwa-managed worktrees, with a fixed outcome contract and a
root refusal for the other worktree subcommands. The work lands as one pull
request against `tsukumogami/niwa`, closing niwa issues #292 and #297.

## Decomposition Strategy

**Horizontal decomposition.** The design refactors existing code along package
seams, and the seams are the decomposition: a test seam that must land red
first, then the package that owns the lock and the swap's layout, then the
writers and readers routed through it, then the CLI resolution the last of
those readers feeds.

Two of the design's decisions force this order rather than merely suggesting
it. The test seam (Decision 3) exists so each race test fails against the
commit before its fix, which only works if the hook points and the tests land
first, in their own commit, with the failing output recorded. And the lock
(Decision 1) owns the file names and the recovery rules that every routed
caller uses, so the package has to exist before any caller can be moved onto
it; moving a caller first would mean writing those names twice and then
deleting one copy.

Walking skeleton was not appropriate: there is no new end-to-end flow to stub,
and the riskiest part is ordering, which has no useful thin slice, because a
half-ordered directory is the bug being fixed.

The root refusal (issue 5) is deliberately independent of the lock work. It
changes resolution for nine callers, so it is easier to review as its own
commit, and nothing in it needs the lock.

## Issue Outlines

### Issue 1: test(workspace): add testhook seam and failing race tests

**Goal**: Add the `internal/testhook` registry with its parser-based guard
test, place the three behavior-neutral hook points in today's snapshot writer,
swap and apply paths, and land the forced race tests that fail against this
commit.

**Acceptance Criteria**:
- [ ] `internal/testhook` declares `Point`, the four point constants, and
      exactly `Hit(Point) error` and `Set(Point, func() error) (restore func())`;
      it imports only `sync` and `sync/atomic`, declares no `init()` and reads
      no environment.
- [ ] `Hit` returns nil through one atomic load when no hook is set and returns
      the hook's error verbatim when one is; `Set` panics when the point already
      has a hook and its `restore` clears it; unit tests cover all four
      behaviors including the panic.
- [ ] A guard test parses every non-`_test.go` file under `cmd/`, `internal/`
      and `test/` with `go/parser` and fails if any references `testhook.Set`
      through its own import alias; it asserts it found at least one production
      file importing the package, and is itself exercised against a synthetic
      source it does flag.
- [ ] `materializeAndSwap` calls `Hit(SnapshotCarriedOver)` after the three
      `preserve*` copies and before `SwapSnapshotAtomic`, and returns the
      hook's error after its existing staging cleanup.
- [ ] `SwapSnapshotAtomic` calls `Hit(SnapshotMovedAside)` between its two
      renames and returns the hook's error, leaving the directory moved aside
      so the kill tests see the real interrupted state.
- [ ] `Create` and `Apply` call `Hit(RootStateRead)` beside their root
      `LoadState` calls; a hook error makes that variable nil, which is what a
      failed read yields today, and with no hook set both sites behave exactly
      as before.
- [ ] `ConfigDirLockContended` is declared with no production call site in this
      commit.
- [ ] Forced tests in `internal/workspace` hold a refresh at
      `SnapshotCarriedOver` and assert: a mapping write during the hold returns
      nil and survives; a mapping delete during the hold returns nil and stays
      deleted; a second overlapping refresh of the same directory completes
      with no error from either; and a mapping write held at
      `SnapshotMovedAside` returns nil and leaves the directory holding
      `workspace.toml`, the new marker, the carried-over `instance.json` and
      the new mapping.
- [ ] A forced test drives `Create` with a failing `RootStateRead` hook and
      asserts the root `instance.json` is byte-identical afterwards; a second
      holds one `Create`'s read while another writes root disclosures and
      asserts the file always parses and never loses `ephemeral_session_mode`
      or `overlay_url`.
- [ ] A forced test in `internal/watch` holds a root refresh at
      `SnapshotCarriedOver` and asserts a handled-state write and a staged-record
      write during the hold return nil and survive.
- [ ] A forced test in `internal/cli` holds a refresh at `SnapshotMovedAside`
      while the backstop sweep reads the mapping store, against a mapped
      dispatch instance older than the backstop threshold, and asserts the
      instance is not selected and its mapping survives.
- [ ] Two kill tests re-execute the test binary with a marker read only from
      `_test.go` code, block the child at each swap point and SIGKILL it, and
      assert the on-disk shape and that the next refresh succeeds; both skip on
      non-unix, and no hook-using test calls `t.Parallel`.
- [ ] At this commit `go build ./...` succeeds, `GOOS=windows go build` succeeds
      for `./internal/workspace/... ./internal/config/... ./internal/watch/...
      ./internal/worktree/... ./internal/testhook/...` (the PRD's measured
      Windows scope; `./...` does not build for Windows today and is not
      required), and every pre-existing test passes, while the new forced tests
      fail; that failing output is captured for the pull request body and is not
      committed.

**Dependencies**: None

**Type**: code
**Files**: `internal/testhook/testhook.go`, `internal/testhook/testhook_test.go`, `internal/workspace/snapshotwriter.go`, `internal/workspace/snapshot.go`, `internal/workspace/apply.go`

### Issue 2: feat(configdir): per-config-dir lock, layout and recovery

**Goal**: Add `internal/configdir`, the one owner of a refreshed config
directory's lock, swap layout names, snapshot-marker check, staging creation,
recovery rules and the `Mutate`/`Read` entry points, on unix and with a
documented non-unix no-op.

**Acceptance Criteria**:
- [ ] The package is a leaf: its dependency list is the standard library plus
      `internal/testhook`, and `go.mod` gains no module.
- [ ] Layout helpers are exported and pinned by a table test: `LockPath` is
      `<dir>.lock`, `PrevPath` is `<dir>.prev`, staging is `<dir>.next-<random>`,
      trash is `<dir>.trash-<random>`, stray is `<dir>.stray-<random>`; every
      generated suffix is random rather than derived from the clock, and two
      calls never collide.
- [ ] `HoldsSnapshot` is true only for a real directory holding the provenance
      marker as a regular file, and false for a directory holding only
      `workspace.toml` (which is what a legitimate overlay clone holds), for a
      missing path, for a regular file, for a symlink to a qualifying directory,
      and for one whose marker is a directory or a symlink.
- [ ] The package's copies of the two marker filenames are covered by a test
      that fails if either drifts from the constant it mirrors in
      `internal/workspace`.
- [ ] `Acquire` opens the lock with `O_CREAT|O_RDWR|O_NOFOLLOW|O_NONBLOCK` at
      0600, `fstat`s the descriptor and refuses anything that is not a regular
      file without taking a lock, never unlinks the file, and polls `flock`
      every 20 ms against a deadline from the overridable `Timeout` (default
      30s); a FIFO planted at the lock path produces that refusal promptly
      rather than blocking the open.
- [ ] Mode semantics are proved with concurrent goroutines: two shared holds
      overlap; exclusive excludes shared and exclusive in either order;
      releasing lets the waiter through; nothing upgrades shared to exclusive.
- [ ] The first busy try calls `Hit(ConfigDirLockContended)` exactly once per
      acquisition and returns a hook error rather than discarding it.
- [ ] A timeout names the directory and the lock path and says another niwa
      command is using it; with the bound at 1s, an acquisition blocked behind
      a 3s hold returns that error within 2s.
- [ ] A second acquisition of a path this process already holds fails
      immediately with `nested acquisition of <dir>.lock`, well inside the
      bound, and the held-path set clears on release.
- [ ] `Mutate` acquires exclusive, runs `Recover` before `fn`, releases before
      deleting trash, and leaves no trash behind; a test proves the deletes
      happen after release. `Read` acquires shared. In both, an error from `fn`
      propagates and the lock is still released.
- [ ] `NewStaging`, called inside `Read`, creates `<dir>.next-<random>` at 0700
      holding `snap/` at 0755 and a `lock` created `O_CREAT|O_EXCL` and held
      exclusive for its life; two calls produce distinct paths.
- [ ] `Recover` has a case-per-rule table test: rename-back when the live
      directory is missing and `.prev` is a previous snapshot; trash `.prev`
      when the live directory holds the marker; quarantine a live directory
      without the marker to `.stray-<random>` and rename `.prev` back when only
      `.prev` qualifies; and fold a leftover trash directory, recognized by its
      sentinel file, into this run's list.
- [ ] Staging recovery treats a candidate as dead only when it is a real
      directory holding both `lock` and `snap/`, opens that `lock` without
      `O_CREAT`, leaves a live holder's staging alone, and leaves a look-alike
      directory with no `lock` untouched and uncreated; the legacy fixed
      `<dir>.next` is the one lockless shape removed.
- [ ] Name collisions with legitimate overlay clones are proved harmless: a real
      directory at `<dir>.prev`, `<dir>.next-x` or a trash-shaped name holding
      only a `workspace.toml` and no niwa marker or sentinel is left in place and
      reported, with no rename and no delete.
- [ ] Planted paths are refused rather than acted on: a `.prev` that is a
      symlink, a regular file, or another user's directory produces no rename;
      removals unlink a top-level symlink without following it; and
      `RecoverMovedAside` takes the lock itself, performs only the rename-back
      under the same checks, repairs at most one candidate per call, and returns
      nil when the live directory exists.
- [ ] A rename whose random destination already exists retries once with a fresh
      name and then returns an error naming the path, rather than leaving the
      live directory moved aside; a test plants the first destination and asserts
      the mapping write that follows still succeeds.
- [ ] The non-unix file builds and returns a no-op release and a `Recover` that
      does nothing, each documenting that the platform gets neither ordering nor
      crash repair; `GOOS=windows go build ./internal/configdir/...` succeeds and
      `go test -race ./internal/configdir/...` passes on Linux.
- [ ] With the bound at 1s, a dispatch-shaped sequence of acquisitions in one
      process completes without timing out, and an exclusive waiter behind
      repeatedly overlapping shared holds acquires within the bound.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/configdir/configdir.go`, `internal/configdir/configdir_unix.go`, `internal/configdir/configdir_other.go`, `internal/configdir/recover_unix.go`, `internal/configdir/recover_other.go`

### Issue 3: fix(workspace): order refresh, mapping store and watch writes

**Goal**: Route the snapshot writer and swap, the session mapping store, the
watch state writers and config discovery's moved-aside repair through
`internal/configdir`'s entry points, so every niwa write into a refreshed
config directory is ordered against the swap and none can recreate the
directory.

**Acceptance Criteria**:
- [ ] `materializeAndSwap` drops the fixed `.next` path and its preflight
      delete: it creates the parent when missing, creates staging via
      `NewStaging` inside `Read`, fetches unlocked, and does the marker write,
      the three carry-over copies and the swap inside one `Mutate`, removing
      its staging afterwards.
- [ ] `SwapSnapshotAtomic` takes `prev` from `configdir.PrevPath`, has lost its
      unconditional preflight delete, and renames the old snapshot to a trash
      name deleted after the lock is released.
- [ ] The marker and `.git` probes and `ReadProvenance` run inside `Read`; the
      no-drift branch re-reads, compares and rewrites the marker atomically
      inside `Mutate`; the post-swap `config.Load` runs inside `Read`.
- [ ] The three `preserve*` helpers use `Lstat` and refuse a symlinked source: a
      symlink planted at the mapping directory, the state file or the third
      carried path leaves the swap carrying nothing from it and reports the
      refusal, instead of promoting the symlink's target.
- [ ] The four mapping-store functions run inside `Mutate` (write, delete) and
      `Read` (list, read) on the root `.niwa`, with no exported signature
      changed.
- [ ] No mapping writer calls `MkdirAll`: with `.niwa` absent they return an
      error naming it, and when it exists they create only `sessions/` at 0700
      with files at 0600.
- [ ] The two watch state writers run inside `Mutate`, return an error when
      `.niwa` is absent, and create only their own leaf; no `MkdirAll` of the
      root `.niwa` remains in `internal/watch`.
- [ ] `config.Discover` gains exactly one branch: a candidate with no
      `.niwa/workspace.toml` but with a `.niwa.lock` regular file and a
      `.niwa.prev` directory, all owned by the current user, calls
      `RecoverMovedAside` and re-checks; every other outcome is unchanged.
- [ ] No literal config-directory suffix remains in `internal/workspace`,
      `internal/watch` or `internal/config`; those names come only from
      `internal/configdir`.
- [ ] The Issue 1 forced race tests pass unchanged, with no edit to their
      assertions, in `internal/workspace`, `internal/watch` and `internal/cli`.
- [ ] Both Issue 1 kill tests pass: after a kill before the swap the next
      refresh succeeds with no leftover staging or prev; after a kill between
      the renames the next command reading the directory finds `workspace.toml`
      and the pre-kill mappings with no manual cleanup.
- [ ] Forced overlapping refreshes also complete without error for an overlay
      directory and the global config directory, and with a fake GitHub fetcher
      in both the drift and no-change paths.
- [ ] With the bound at 1s and a fetch held 3s, a concurrent mapping write and
      a concurrent refresh of the same directory each complete within 1s.
- [ ] After a forced mapping write in the rename window, the live directory
      holds that refresh's `workspace.toml` and marker, the carried-over
      `instance.json` and the new mapping, with `sessions/` still 0700 and its
      files 0600.
- [ ] `go build ./...`, `GOOS=windows go build` over the touched packages, and
      `go test -race` over the touched packages all pass, and `go.mod` gains no
      module.

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/workspace/snapshotwriter.go`, `internal/workspace/snapshot.go`, `internal/workspace/session_map.go`, `internal/workspace/configreload.go`, `internal/watch/state.go`, `internal/config/discover.go`

### Issue 4: fix(workspace): atomic locked SaveState with skip-after-failed-read

**Goal**: Make `SaveState` write `instance.json` through a uniquely named temp
file renamed into place, take the config-directory lock (and stop calling
`MkdirAll`) when the target is a refreshed config directory, and stop root
disclosures being written after a failed read.

**Acceptance Criteria**:
- [ ] `SaveState` writes a uniquely named temp file in the same `.niwa/`, sets
      it 0644, and renames it over `instance.json`; a concurrent reader sees
      the whole previous file or the whole new one, never a partial one.
- [ ] After a first write and an overwrite the file is 0644, no temp file is
      left on success, and a failed marshal or temp write leaves an existing
      file byte-identical.
- [ ] The write routes through `Mutate` when the target is a refreshed config
      directory (a sibling `.niwa.lock` exists, or the directory carries
      `.niwa/workspace.toml`), and on that path does not `MkdirAll` the `.niwa`
      directory, returning an error when it is absent.
- [ ] For a child instance under a multi-instance root, `SaveState` keeps
      today's unlocked path including `MkdirAll`, creates no lock file, and the
      instance-state writes behave as before.
- [ ] All three refreshed-directory callers get the locked atomic write with no
      per-caller change: root disclosures, the `niwa init` root write, and the
      single-instance `Apply` write; a test per caller shows each waits behind
      an exclusive hold rather than writing through.
- [ ] Forced at `SnapshotCarriedOver`: a `SaveState` into a refreshed directory
      starting mid-refresh completes, does not recreate `.niwa` (the swap still
      succeeds), and leaves the directory holding that refresh's
      `workspace.toml` and marker alongside a parseable `instance.json`.
- [ ] `saveWorkspaceRootDisclosures` returns without writing when its read
      failed and a root `instance.json` exists, and still writes when the file
      is absent or the read succeeded.
- [ ] The Issue 1 root-state tests turn green here, both the concurrent-write
      case and the forced-failed-read case.
- [ ] No `SaveState` call happens inside an existing `Mutate` or `Read`
      callback, so the nested detector never fires: one `niwa dispatch` against
      a local-source workspace completes with the default bound.
- [ ] `go test -race ./...` passes on Linux, `GOOS=windows go build
      ./internal/workspace/...` succeeds, and `go.mod` gains no module.

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/workspace/state.go`, `internal/workspace/apply.go`

### Issue 5: fix(cli): refuse worktree subcommands at a multi-instance root

**Goal**: Rebuild `discoverInstanceRoot` on `workspace.ClassifyCwd` and a new
`workspace.IsSingleInstanceLayout`, add the `errAtWorkspaceRoot` sentinel
carrying the root message, and give `worktree list` the branch that prints it
and exits 0.

**Acceptance Criteria**:
- [ ] `IsSingleInstanceLayout` returns true only when the root has no child
      instance and its `instance.json` loads with a non-empty instance name; a
      unit test covers the empty-name case every registered `niwa init` writes,
      the named case, a root with a child instance, a root with no state file,
      and an unreadable one.
- [ ] `discoverInstanceRoot` is rebuilt on `ClassifyCwd` and no longer walks
      for `.niwa/instance.json` itself: inside a worktree or instance it
      returns the instance directory; at a workspace root it returns the root
      only in the single-instance layout and otherwise `errAtWorkspaceRoot`;
      outside it returns today's error.
- [ ] The sentinel lives in `internal/cli/session.go`, callers match it with
      `errors.Is`, and its message is exactly the root line `Execute` prints
      before exiting 1.
- [ ] `resolveInstanceRoot` still returns `NIWA_INSTANCE_ROOT` verbatim without
      classifying, and never yields the sentinel when it is set, including when
      it points at a multi-instance root.
- [ ] A table test pins resolution for nine layouts: an instance root, a repo
      clone inside an instance, a niwa worktree, a Claude-created worktree
      under a repo, a multi-instance root, a freshly initialized root, a
      single-instance root, a directory outside any workspace, and a run with
      `NIWA_INSTANCE_ROOT` set.
- [ ] (pre-fix fails) `worktree list` at a multi-instance root holding at least
      one mapping prints the root message on stderr without the `error:`
      segment, prints no table and no empty-filter line, and exits 0.
- [ ] `worktree list --json` there still prints `[]` on stdout alongside the
      stderr line and exits 0.
- [ ] At that root, `worktree create`, `apply`, `attach`, `detach` and
      `niwa go <repo> <8-hex>` each print exactly the root error on stderr and
      exit 1, with nothing on stdout and no read of the mapping store as
      worktree records.
- [ ] Session-id completion offers nothing there, and the WorktreeRemove hook
      logs its existing warning carrying the sentinel text and exits 0 without
      attempting a destroy.
- [ ] `internal/cli/apply.go` uses `IsSingleInstanceLayout` in place of its
      inline check, so a freshly initialized root is no longer treated as
      single-instance while a named one still is.
- [ ] In a single-instance workspace, `worktree create` and
      `destroy <worktree-id>` at the root behave exactly as before, and `list`
      there prints its table.
- [ ] `go build`, `go vet` and the CLI and workspace tests pass, every existing
      worktree subcommand test passes unchanged, and `go.mod` gains no module.

**Dependencies**: None

**Type**: code
**Files**: `internal/cli/session.go`, `internal/cli/session_lifecycle_cmd.go`, `internal/cli/apply.go`, `internal/workspace/state.go`

### Issue 6: feat(cli): resolve worktree destroy by session id or handle

**Goal**: Resolve `niwa worktree destroy <value>` as a session id, handle or
worktree id against one locked mapping snapshot, and destroy that session's
active worktrees under the outcome contract.

**Acceptance Criteria**:
- [ ] `workspace.NewestMappingPerInstance` exists, keys by cleaned instance
      path, picks the latest `Created` and the first in session-id order on
      ties, and `niwa list` adopts it with its existing tests passing.
- [ ] A new `internal/cli/worktree_destroy_resolve.go` provides the six
      resolver functions; `internal/worktree` gains no import of
      `internal/cli` and its exported signatures are unchanged.
- [ ] `loadMappingsForDestroy` makes exactly one mapping read, through the
      locked `ListSessionMappings`, and keeps only entries whose session id is
      a valid UUID; a planted 8-hex lifecycle file beside the mappings is not
      treated as a mapping.
- [ ] A table test on matching covers: session id, recorded handle, and the
      exactly-8-hex prefix of a handle-less mapping; exact and case-sensitive;
      deduplicated so a Codex mapping whose handle is its id yields one match;
      a mapping with a recorded handle is not matched by its prefix; a
      7-character prefix, an uppercase handle and a non-hex value match
      nothing.
- [ ] A table test on target resolution covers: a worktree id of the current
      instance; a single mapping match; no match exiting 3 with the no-match
      line; more than one match exiting 4 naming every match; and, inside an
      instance, a value that is both a worktree id and a mapping match exiting
      4 with the guidance, while the same value at the root resolves to the
      session.
- [ ] `resolveDestroyScope` classifies through `ClassifyCwd`, yielding an
      instance directory inside an instance, a worktree or a single-instance
      root, and a workspace-root scope otherwise.
- [ ] `checkSessionInstance` applies the ordered rungs and stops at the first
      that applies: not an instance location, nothing at the path (the exit-0
      "no longer exists" outcome), not among the enumerated instances, not the
      newest mapping for that instance; otherwise it returns the enumerated
      directory, never the recorded string. Unit tests cover each rung,
      including a path outside the workspace, the root itself, and a symlink
      resolving outside.
- [ ] `destroySessionWorktrees` processes active records in worktree-id order
      and re-checks the instance directory immediately before each destroy.
- [ ] `DestroySession` validates the record it read before either git call: it
      requires a non-empty worktree path resolving under
      `<instanceRoot>/.niwa/worktrees/`, requires a branch name that looks like
      a ref and does not begin with `-`, and passes it after `--`. Unit tests
      cover a path escaping the instance, an absolute path elsewhere, an empty
      path, a `--upload-pack=`-shaped branch and a normal record, and assert
      every record niwa writes today still passes.
- [ ] A record whose worktree directory is missing is refused rather than
      reported clean: a test asserts the uncommitted-changes guard now fails
      closed on a missing tree, where today it returns clean.
- [ ] Record reads resolve through a root opened on the instance directory.
- [ ] Success prints one destroyed line per worktree on stdout and each kept
      branch's existing warning on stderr, exiting 0; tests cover merged and
      unmerged branches.
- [ ] Nothing-to-destroy prints the no-active-worktree line (including that
      branches outside niwa-managed worktrees were not checked) and exits 0; a
      mapping whose instance is gone prints the already-removed line and exits
      0; a second teardown prints the first line and exits 0.
- [ ] Partial refusal: with two active worktrees where the first is refused by
      a guard, the second is destroyed, the first stays, stdout has the
      destroyed line, stderr has one error line per refusal, and the command
      exits 1.
- [ ] Teardown by session never deletes the mapping, the instance or its
      clones; every session-resolved test asserts all three survive.
- [ ] Every mapping- and record-derived value in output goes through the
      dispatch path's existing sanitizer rather than `%q`, so the literal line
      shapes the outcome contract fixes are preserved: a mapping whose handle
      carries an ANSI escape, and a kept-branch warning naming a branch with a
      control character, both print with the control characters stripped and no
      surrounding quotes.
- [ ] `runSessionDestroy` routes the positional value through the resolver and
      refuses `--force` with a session id or handle as a usage error (exit 2)
      before anything is destroyed; `--force` with a worktree id or
      `--by-path` is unchanged.
- [ ] A worktree-only match takes today's path unchanged for merged, unmerged,
      attached and uncommitted cases, and `--by-path` from the root gives
      today's result; existing destroy tests pass apart from the collision
      (exit 4) and no-match (exit 3) changes.
- [ ] Session resolution gives the same result from the root, another
      instance, and a worktree, for both id forms; a value matching nothing
      exits 3 from either location.
- [ ] Forced: a destroy whose mapping read lands during a swap resolves the
      session rather than exiting 3.

**Dependencies**: Blocked by <<ISSUE:3>>, <<ISSUE:5>>

**Type**: code
**Files**: `internal/cli/worktree_destroy_resolve.go`, `internal/cli/session_lifecycle_cmd.go`, `internal/cli/list.go`, `internal/workspace/session_map.go`, `internal/worktree/worktree.go`

### Issue 7: docs(worktree): functional coverage and guide updates

**Goal**: Cover destroy by session id and by handle from the workspace root end
to end, recompose the four-way parallel dispatch against a local config source,
and bring the worktree and config-sources guides in line with the new
behavior.

**Acceptance Criteria**:
- [ ] A new `test/functional/features/worktree-teardown-by-session.feature`
      builds its workspace from the existing local-git-server, config-repo,
      `niwa init` and fake-claude steps, so every scenario runs against a
      `file://` config source with no GitHub fake and no network.
- [ ] A scenario destroys by session id from the workspace root with one active
      worktree on a merged branch: exit 0, exactly one destroyed line naming
      the worktree id and repo, the record ended, the directory gone, the
      branch gone.
- [ ] The same setup with an unmerged branch: exit 0, directory removed, branch
      still present, and exactly one stderr line containing the unmerged-commits
      warning.
- [ ] A scenario passes the recorded handle instead of the session id and
      asserts the same outcome, so the handle route is exercised distinctly.
- [ ] Every destroy scenario asserts what survives: the session mapping, the
      instance directory, and the cloned repository inside it.
- [ ] A scenario covers the root contract: an 8-hex value matching nothing
      exits 3 with the no-match line, and `worktree list` prints the root
      message on stderr, no table, exit 0.
- [ ] The four-parallel-dispatch scenario is recomposed onto a config repo from
      the local git server, dropping its machine-config workaround, and its
      comment is rewritten to say the ordering now holds, that this is the
      PRD's one probabilistic command-level check, and that the forced tests
      carry the determinism.
- [ ] That scenario keeps its existing assertions and adds that the four
      mappings name four distinct existing instance directories and that
      `niwa list` reports all four.
- [ ] New steps are added only for what the suite lacks (a worktree in the
      recorded dispatch instance, a commit leaving a branch unmerged, lifecycle
      and directory assertions for that instance, and the four-distinct-instance
      check), each registered and commented; no existing step is renamed,
      removed or changed.
- [ ] `docs/guides/worktree.md` documents all four target forms, the
      8-hex prefix fallback for a handle-less mapping, that a session target
      destroys every active worktree of its instance in worktree-id order and
      continues past a refusal, that `--force` with a session is a usage error,
      and that the mapping, instance and clones are never removed.
- [ ] The same section carries the outcome table with exit codes, streams and
      exact wording, and notes that a worktree id matching nothing now exits 3.
- [ ] The guide documents worktree subcommands at a multi-instance root and
      states that the single-instance layout is unchanged.
- [ ] `docs/guides/workspace-config-sources.md`'s atomic-refresh section is
      rewritten for the implemented swap: the sibling lock file, private
      staging, the unlocked fetch, recovery instead of preflight cleanup, trash
      deleted after release, the kept stray directory, the bounded wait, and
      that ordering and crash repair are unix-only; the stale claims about
      idempotent preflight cleanup and never reading mid-swap are corrected.
- [ ] Every exit code a `niwa worktree destroy` invocation returns for an
      outcome that exits 1 today is announced as a behavior change, not folded
      in as a fix: the pull request body carries them under their own "Behavior
      changes" heading, naming the old code and the new one per outcome, and
      flags them for the release notes. The repository has no `CHANGELOG` file,
      so the release notes are where a user meets the change.
- [ ] `go test ./test/functional/...` passes on Linux, `go vet ./...` is clean,
      and no committed file references a non-durable working path.

**Dependencies**: Blocked by <<ISSUE:3>>, <<ISSUE:6>>

**Type**: docs
**Files**: `test/functional/features/worktree-teardown-by-session.feature`, `test/functional/features/session-message-acceptance.feature`, `docs/guides/worktree.md`, `docs/guides/workspace-config-sources.md`

## Dependency Graph

## Implementation Sequence

**Edges:** 1 before 2; 2 before 3 and 4; 3 and 5 before 6; 3 and 6 before 7.
Issues 1 and 5 have no blockers. Each outline above declares the same
dependencies, which is where an implementing agent reads them.

**Critical path:** Issue 1 -> Issue 2 -> Issue 3 -> Issue 6 -> Issue 7 (5 of 7).

**Recommended order:**
1. Issue 1 -- the test seam and the red tests, whose failing output is the
   evidence the PRD's verification requirements ask for.
2. Issue 5 -- the root refusal, independent of everything else and easiest to
   review on its own.
3. Issue 2 -- the lock package the rest of the concurrency work sits on.
4. Issue 3 -- the routed writers and readers; most of Issue 1's tests turn
   green here.
5. Issue 4 -- the root-state guards; the remaining Issue 1 tests turn green.
6. Issue 6 -- the destroy resolver, which needs the locked mapping read from
   Issue 3 and the scope classification from Issue 5.
7. Issue 7 -- functional scenarios and the guides, last because they drive the
   finished command.

**Parallelization:** Issues 1 and 5 can start together. After Issue 2, Issues 3
and 4 are independent of each other. Everything else is on the critical path.

All seven land as commits in one pull request, so the red commit at the head is
never the tip of a merged branch.
