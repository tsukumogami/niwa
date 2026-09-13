---
schema: plan/v1
status: Active
execution_mode: single-pr
tracking_level: none
upstream: docs/designs/DESIGN-session-store-teardown.md
milestone: "session-store-teardown"
issue_count: 4
---

# PLAN: session-store-teardown

## Status

Active

## Scope Summary

Let `niwa worktree destroy` resolve the session id or handle a developer holds
to that session's niwa-managed worktrees, with a fixed outcome contract, a
validated lifecycle record, and a root refusal for the other worktree
subcommands. One pull request against `tsukumogami/niwa`, closing niwa issue
#292.

Issue #297, the config-directory refresh ordering, is deliberately **not** in
this plan. It was, and the split is the point: the two halves meet at exactly
one call site, the resolver's single mapping read, and every finding from two
failed security reviews belongs to the #297 half. This plan ships that read
unlocked, which is what every reader of the store does today, so #297 later
swaps in the locked variant at that one place without touching anything here.
The eight-issue plan covering both is preserved in commit `873b124`, and the
design still describes both halves.

## Decomposition Strategy

**Horizontal decomposition.** The work splits along package seams, and the two
seams that matter are both independent of each other: where a command decides
which instance it is operating on (`internal/cli`), and where a lifecycle record
becomes git arguments (`internal/worktree`). Each lands on its own commit, and
the resolver that needs both comes after.

Walking skeleton was not appropriate: there is no new end-to-end flow to stub.
The command already exists and already destroys worktrees; what changes is what
it accepts as a target and what it refuses.

The root refusal (issue 1) and record validation (issue 2) are both independent
of everything else and of each other. Issue 2 is split out rather than folded
into the resolver because it lands in a different package, along a seam the
design deliberately pushed down, and because it is the one commit that refuses
input today's code accepts -- which is easier to review on its own than buried
in a feature commit.

## Issue Outlines

### Issue 1: fix(cli): refuse worktree subcommands at a multi-instance root

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

### Issue 2: fix(worktree): validate a lifecycle record before it reaches git

**Goal**: Stop `DestroySession` trusting the record it just read. Validate the
worktree path and branch name beside the git calls that consume them, fail the
dirty-tree guard closed, and route the third copy of the same argv through the
same rules.

**Acceptance Criteria**:
- [ ] `worktree.ValidateRecordFields(instanceRoot, worktreePath, branchName)` is
      exported and takes fields rather than a record type, because
      `workspace.DefaultDestroySession` parses its own inline struct rather than
      `SessionLifecycleState`; `internal/workspace` already imports
      `internal/worktree`, so this adds no dependency edge.
- [ ] It requires a non-empty worktree path resolving under
      `<instanceRoot>/.niwa/worktrees/`, and a branch name matching the design's
      exact ref pattern -- non-empty; no byte below 0x20 and no DEL; no space;
      none of `~ ^ : ? * [ \`; no `..`; no leading `-`; no trailing `.lock`; no
      leading or trailing `/` and no `//`; not `@` alone and no `@{`. Table
      tests cover a path escaping the instance, an absolute path elsewhere, an
      empty path, a `--upload-pack=`-shaped branch, `@{-1}`, a branch with a
      control character, a branch with U+202E, and a normal record.
- [ ] Every branch name `EffectiveBranchName` produces today passes, asserted
      over the shapes niwa writes, so the validation refuses only records niwa
      did not write.
- [ ] `DestroySession` calls it before either git call and passes the branch
      after `--`; a test asserts the `--` is present in the argv.
- [ ] The uncommitted-changes guard fails closed on *any* `stat` error rather
      than only on `ENOENT`: tests cover a missing worktree directory and one
      whose `stat` fails for another reason, both of which reach the git call
      today and report clean.
- [ ] Containment refuses a `..` component outright rather than cleaning it,
      and compares symlink-resolved forms of both sides, so a symlinked instance
      root resolves rather than producing a false refusal while a symlink planted
      inside the worktrees directory cannot redirect teardown.
- [ ] `workspace.DefaultDestroySession`, the `niwa init --bootstrap` rollback,
      calls the same validator and passes its branch after `--`; a test covers a
      crafted record there, where today there is no validation and no `--`.
- [ ] `go build ./...`, `go vet ./...` and `go test -race
      ./internal/worktree/... ./internal/workspace/...` pass, `internal/worktree`
      gains no import beyond what it has today, and `go.mod` gains no module.

**Dependencies**: None

**Type**: code
**Files**: `internal/worktree/worktree.go`, `internal/workspace/bootstrap.go`

### Issue 3: feat(cli): resolve worktree destroy by session id or handle

**Goal**: Resolve `niwa worktree destroy <value>` as a session id, handle or
worktree id against one mapping-store read, and destroy that session's active
worktrees under the outcome contract.

**Acceptance Criteria**:
- [ ] `workspace.NewestMappingPerInstance` exists, keys by cleaned instance
      path, picks the latest `Created` and the first in session-id order on
      ties, and `niwa list` adopts it with its existing tests passing.
- [ ] A new `internal/cli/worktree_destroy_resolve.go` provides the six
      resolver functions; `internal/worktree` gains no import of
      `internal/cli` and its exported signatures are unchanged.
- [ ] `loadMappingsForDestroy` makes exactly one mapping-store read, through
      `ListSessionMappings`, and keeps only entries whose `session_id` field is
      a valid UUID; a planted 8-hex lifecycle file beside the mappings is not
      treated as a mapping. The read is unlocked, as every reader of this store
      is today; issue #297 replaces this one call with the locked variant.
- [ ] The same loader drops the handle match for any mapping whose `handle`
      fails the existing exported `watch.IsSafeHandle`: such a mapping still
      matches by session id, still appears in output, and can no longer put an
      arbitrary byte sequence into the ambiguity line. A test uses a handle
      carrying an ANSI escape and one carrying U+202E.
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
- [ ] `checkSessionInstance` resolves both sides of the `EnumerateInstances`
      membership comparison, since the enumeration returns lexical joins. A test
      with the workspace root reached through a symlink resolves the session
      rather than refusing it.
- [ ] A test pins the unlocked read's failure direction, which the pull request
      records as a known limitation: with the mapping directory absent, and with
      a subset of its files unreadable, the resolver yields no match (exit 3)
      and destroys nothing. It never yields a mapping that was not on disk,
      because each candidate is a file read whole and JSON-parsed, and a
      malformed file is skipped.
- [ ] `destroySessionWorktrees` processes active records in worktree-id order
      and re-checks the instance directory immediately before each destroy.
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
- [ ] Every mapping- and record-derived value in output goes through the widened
      `stripControlChars` rather than `%q`, so the literal line shapes the
      outcome contract fixes are preserved: a mapping whose handle carries an
      ANSI escape, and a kept-branch warning naming a branch with a control
      character or U+202E, all print stripped and unquoted. The strip happens at
      the print boundary in `internal/cli`, where both `BranchWarning` consumers
      already are.
- [ ] `stripControlChars` is widened to remove U+2028, U+2029 and the Cf bidi
      and zero-width block alongside the C0, C1 and DEL it already drops, with a
      table test covering U+202E and U+200B, which every existing stripper in
      the repository passes through.
- [ ] `runSessionDestroy` routes the positional value through the resolver and
      refuses `--force` with a session id or handle as a usage error (exit 2)
      before anything is destroyed; `--force` with a worktree id or
      `--by-path` is unchanged.
- [ ] A worktree-only match takes today's path unchanged for merged, unmerged,
      attached and uncommitted cases, and `--by-path` from the root gives
      today's result; existing destroy tests pass apart from the collision
      (exit 4) and no-match (exit 3) changes.
- [ ] `--by-path` for a path that is no niwa-managed worktree exits 3 through
      the same error the positional no-match uses, with its message unchanged;
      a test pins that it exits 1 before the change and 3 after, so the one
      moved code is recorded rather than absorbed.
- [ ] Session resolution gives the same result from the root, another
      instance, and a worktree, for both id forms; a value matching nothing
      exits 3 from either location.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>

**Type**: code
**Files**: `internal/cli/worktree_destroy_resolve.go`, `internal/cli/session_lifecycle_cmd.go`, `internal/cli/list.go`, `internal/cli/session_from_hook_cmd.go`, `internal/workspace/session_map.go`

### Issue 4: docs(worktree): functional coverage and guide updates

**Goal**: Cover destroy by session id and by handle from the workspace root end
to end, and bring the worktree guide in line with the new behavior.

**Acceptance Criteria**:
- [ ] A new `test/functional/features/worktree-teardown-by-session.feature`
      builds its workspace from the existing local-git-server, config-repo,
      `niwa init` and fake-claude steps, so every scenario runs against a
      `file://` config source with no GitHub fake and no network.
- [ ] A scenario destroys by session id from the workspace root with one active
      worktree on a merged branch: exit 0, exactly one destroyed line naming the
      worktree id and repo, the record ended, the directory gone, the branch
      gone.
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
- [ ] New steps are added only for what the suite lacks (a worktree in the
      recorded dispatch instance, a commit leaving a branch unmerged, and
      lifecycle and directory assertions for that instance), each registered and
      commented; no existing step is renamed, removed or changed. The
      four-parallel-dispatch scenario is left alone: it belongs to #297.
- [ ] `docs/guides/worktree.md` documents all four target forms, the
      8-hex prefix fallback for a handle-less mapping, that a session target
      destroys every active worktree of its instance in worktree-id order and
      continues past a refusal, that `--force` with a session is a usage error,
      and that the mapping, instance and clones are never removed.
- [ ] The same section carries the outcome table with exit codes, streams and
      exact wording, notes that a worktree id matching nothing now exits 3 and
      that `--by-path` finding nothing moves from 1 to 3, and states that these
      codes are `destroy`'s own -- `attach`'s 3 and `detach --force`'s 4 keep
      their meanings -- so the two tables cannot be read as one.
- [ ] `niwa worktree destroy --help` names its exit codes inline, the way
      `attach` already does, so a reader meets them without the guide.
- [ ] The guide documents worktree subcommands at a multi-instance root and
      states that the single-instance layout is unchanged.
- [ ] The pull request body carries a "Behavior changes" heading naming each
      outcome whose exit code moves (a worktree id matching nothing, and
      `--by-path` finding nothing, both 1 to 3) and flags them for the release
      notes; the repository has no `CHANGELOG` file, so the release notes are
      where a user meets the change.
- [ ] The pull request body records the unlocked mapping read as a known
      limitation, stating the direction it fails in and that #297 closes it.
- [ ] `go test ./test/functional/...` passes on Linux, `go vet ./...` is clean,
      and no committed file references a non-durable working path.

**Dependencies**: Blocked by <<ISSUE:3>>

**Type**: docs
**Files**: `test/functional/features/worktree-teardown-by-session.feature`, `docs/guides/worktree.md`

## Dependency Graph

## Implementation Sequence

**Edges:** 1 and 2 before 3; 3 before 4. Issues 1 and 2 have no blockers. Each
outline above declares the same dependencies, which is where an implementing
agent reads them.

**Critical path:** Issue 1 -> Issue 3 -> Issue 4 (3 of 4).

**Recommended order:**
1. Issue 1 -- the root refusal, independent and easiest to review on its own.
2. Issue 2 -- record validation, also independent, and the one commit that
   refuses input today's code accepts.
3. Issue 3 -- the destroy resolver, which needs the scope classification from
   Issue 1 and the validated destroy from Issue 2.
4. Issue 4 -- functional scenarios and the guide, last because they drive the
   finished command.

**Parallelization:** Issues 1 and 2 can start together. Everything else is on
the critical path.

All four land as commits in one pull request.
