---
schema: plan/v2
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-plugin-release-hold.md
milestone: "Plugin releases held once per host"
step_count: 7
---

# PLAN: plugin-release-hold

## Status

Active

## Scope Summary

niwa holds each github plugin marketplace release once per workspace on the
host, keyed by the commit its track resolves to, at
`~/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/`, with a
read-only, sealed `tree/` and a `held.json` completion mark. An instance holds
only a symlink at `.niwa/marketplaces/<name>`. Every provisioning command
restores a release that is absent, unmarked or no longer matches its seal for
any instance on disk, kept ones included, and sweeps a release nothing links to
unless it is the newest of its source. `[workspace] share_plugin_releases =
false` turns placement off, and a store that is unreadable, unwritable, can't
be created or can't take a release falls back to today's per-instance fetch
with a notice naming the store's path and the condition.

The work reuses the workspace key, the waiting file lock and the lock-name
scheme that PR #511 merged for the git mirror in 0957d4a.

This plan builds nothing. It describes the work for a later implementation
run, and no test is run against a real host's instances or stores until the
milestone is built. The checks below run in the functional suite's own
`$HOME` against the tarball fake; the by-hand walkthrough against real GitHub
on a scratch home, from the design's test strategy, comes after the last step.

## Decomposition Strategy

Walking skeleton, cut by behaviour. Step 1 crosses the whole placement path on
one real input: a functional-suite workspace whose root declares two github
marketplaces served by the tarball fake. `niwa create` resolves each ref to a
commit, places the release once in the host store under its lock, links the
instance to it, and the skills load through the link at the root, in a repo
and in a session worktree; the second create downloads nothing. Every later
behaviour acts on a held release and a link, so that path comes first.

Step 2 makes the other create paths (dispatch and the SessionStart hook) and
every overlap (a burst, a killed create) share the one copy safely. Steps 3 to
5 keep the store honest for instances already linked: repair of a missing or
unmarked release, the seal that catches a changed tree, and the sweep that
never deletes a release in use. Steps 6 and 7 cover the off switch and the
fallback with its notice, and step 7 also writes the guide section.

The PRD's three Evidence scenarios map onto the steps this way:

- **A second create downloads nothing**: steps 1 and 2 (the `niwa create`,
  `niwa dispatch` and hook rows, links only, under 1 MiB, skills at root, repo
  and worktree, a burst downloading once, a killed create leaving nothing
  held), with the moving-ref comparison with the store on and off in step 6.
- **A deleted release is refetched by niwa with nobody acting, while an older
  instance still loads its skills**: step 3 (apply, dispatch, hook and a
  create on a newer release each restore it; a deleted mark counts as missing;
  a failed refetch is named and retried; no command or guide offers a repair),
  step 4 (a tampered release is refetched), and step 5 (a kept instance's
  release survives every command).
- **Store off gives a full per-instance copy; store unreachable falls back and
  says so**: step 6 (the setting) and step 7 (the four conditions, dispatch's
  output, the hook's context, a burst on an unwritable store, an unheld release
  with its source down), with the single-instance baseline already in step 1.

## Steps

### Step 1: a second niwa create links to the held release and downloads nothing

**Goal**: `niwa create` in a workspace that has a workspace root takes each
github marketplace from its ref to a commit (`HeadCommit`), places that release
once at `~/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/` (a
read-only `tree/` and a `held.json` mark, staged and renamed under the
release's lock), and makes the instance's `.niwa/marketplaces/<name>` a symlink
to its `tree/`, so the next create of the same workspace downloads nothing and
its skills load through the link. It runs on a functional-suite workspace whose
root declares two github marketplaces served by the tarball fake, in the
suite's own `$HOME`.

**Acceptance Criteria**:

- [ ] The first `niwa create` exits 0 and makes exactly one tarball request per
  marketplace, each for the 40-hex commit the fake's commits endpoint returned
  for the ref, never for the tag name.
- [ ] A second `niwa create` of the same workspace exits 0 and makes no tarball
  request at all; the fake's log shows one commit-resolve request per
  marketplace and nothing else for those sources.
- [ ] In the second instance, `find .niwa/marketplaces -type f` prints nothing,
  every entry there is a symlink whose absolute target is
  `$HOME/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/tree`, and
  `du -sk` over the instance's `.niwa/marketplaces` reports under 1024.
- [ ] A skill from each marketplace resolves to a readable `SKILL.md` through
  the delivered links at the instance root, in a repo of the instance and in a
  session worktree, in both instances.
- [ ] In the held release, no entry under `tree/` has a write bit (directories
  0555, files 0444 or 0555 where the archive marked them executable),
  `held.json` is mode 0444 and names the source, the ref, the commit and a
  commit time equal to the archive entries' modification time, and the
  `plugins/` directory and each level niwa made under it are mode 0700.
- [ ] No file of either release tree exists under the workspace root's
  `.niwa/` or inside either instance, and the scenario's `workspace.toml`
  carries no `share_plugin_releases` key and the commands no new flag.
- [ ] Two workspace roots on the same host get two different
  `~/.niwa/w-<key>/` directories, and a create run through a symlink to a root
  uses the same `w-<key>` directory as one run through its real path.
- [ ] An instance whose `.niwa/marketplaces/<name>` is already a real directory
  holding a manifest (made before this change) keeps that directory unchanged
  through `niwa apply`, with no tarball request; and after a newer tag is
  published, `niwa apply` in the first instance makes no tarball request and
  its links still name the commit it was created with, and after that apply a
  skill from each marketplace still resolves in the first instance at its
  root and in a session worktree.
- [ ] A unit test of `Ensure` with a release taken from a link whose source
  isn't declared, or whose commit `Reachable` refuses, records no fetch,
  returns `ErrRefused` and leaves the link unchanged.
- [ ] A single-instance layout with no workspace root holds its own real tree
  under its `.niwa/marketplaces/`, and nothing is created under
  `$HOME/.niwa/` for plugins.
- [ ] A unit test resolving the key directory through both stores' path
  helpers (this store's and PR #511's mirror store) for the same root passes,
  and fails when the two definitions differ.

**Check**: Run `make build-test`, then `make test-functional-plugin-release-hold`.
The `niwa create` row of `A second instance downloads no held release` passes,
its request-count step reports one tarball request per release across both
creates and zero for the second, and the location and single-instance scenarios
pass. Then `go test ./internal/hostdir/... ./internal/pluginstore/... ./internal/workspace/...`
exits 0.

**Dependencies**: None

**Files**: `internal/hostdir/hostdir.go`, `internal/hostdir/hostdir_test.go`, `internal/mirror/layout.go`, `internal/pluginstore/layout.go`, `internal/pluginstore/place.go`, `internal/pluginstore/link.go`, `internal/pluginstore/reachable.go`, `internal/workspace/pluginskills.go`, `test/functional/features/plugin-release-hold.feature`, `test/functional/pluginstore_steps_test.go`, `test/functional/tarball_fake_server.go`, `Makefile`

### Step 2: every create path shares one held copy, and a burst or a killed create never links a half-placed release

**Goal**: The second create downloads nothing and links only when it is made
by `niwa dispatch` (with the suite's fake `claude`) or by the SessionStart
hook's `niwa instance from-hook`, not only by `niwa create`. Overlapping creates
of one workspace download a missing release once and wait for it under its
lock, and a create killed during the download leaves that release unheld, with
nothing linked to the partial copy. Step 1 left the store in
`internal/pluginstore` (`Store.Ensure`, `place`, `download`) reached from
`internal/workspace/pluginskills.go` through `pluginStoreFor`, and the
`A second instance downloads no held release` outline with a `niwa create ws`
row only; this step adds the other two rows, the overlap and interrupt
scenarios, and a `plugin-store-download` fault label at the start of
`Store.download` that uses `internal/testfault`'s existing `hold:` spec.

**Acceptance Criteria**:

- [ ] With both releases held, a second instance made by `niwa dispatch` makes
  no tarball request, its `.niwa/marketplaces/*` entries are symlinks into the
  store with no regular file beneath them, and a skill from each marketplace
  resolves at its root, in a repo and in a session worktree.
- [ ] The same three results hold for a second instance made by the
  SessionStart hook step.
- [ ] With neither release held and the `plugin-store-download` fault set to
  `hold:` on the first download, three creates started as separate processes
  before that download finishes all exit 0, the fake logs exactly one tarball
  request per release in total, and every instance's links resolve to the same
  `<commit>/tree` for each release.
- [ ] A create killed while the `hold:` fault keeps its download open leaves no
  `github.com/<owner>/<repo>/<commit>` directory and no `held.json` for that
  release; the next create makes one tarball request for it, links to the
  complete release, and no `.tmp/` entry for that release is left once it
  finishes.
- [ ] A tarball whose wrapper directory names another commit, or one with no
  `.claude-plugin/marketplace.json`, leaves no held release behind: a unit test
  of `Ensure` returns an error and finds nothing at the release path (no
  `<commit>` directory, not only `Held` false). Step 1's
  `TestEnsureRefusesAWrapperNamingAnotherCommit` covers the first case's error
  and `Held`; the release-path check and the missing-manifest case are new.
- [ ] Ten concurrent `Ensure` calls on one unheld release in a unit test record
  one fetch, and all ten see the release held and their links pointing at it.
  Step 1's `TestConcurrentEnsureFetchesOnce` checks the one fetch with no link;
  each call now passes its own link and the test checks every link's target.

**Check**: Run `make build-test`, then `make test-functional-plugin-release-hold`.
The `niwa dispatch` and SessionStart hook rows of `A second instance downloads
no held release`, `Three overlapping creates download each release once` and
`An interrupted create leaves its release unheld` pass, and the overlap
scenario's request-count step reports 1 for each release. Then `go test -race
./internal/pluginstore/...` exits 0.

**Dependencies**: Step 1

**Files**: `internal/pluginstore/place.go`, `internal/pluginstore/place_test.go`, `test/functional/features/plugin-release-hold.feature`, `test/functional/pluginstore_steps_test.go`

### Step 3: a deleted or unmarked release is fetched back by the next provisioning command with nobody acting

**Goal**: Every provisioning command (`niwa apply`, `niwa create`, `niwa
dispatch` and the SessionStart hook's `niwa instance from-hook`) restores, once
per command per workspace and before the current instance's own placement, any
release that an instance on disk links to and that is missing: its
`<commit>` directory is absent, or present without a valid `held.json`. The
restore writes only inside the store, runs nothing inside any instance, and is
reported with the release and the instances it served. A link whose commit only
a fork holds is refused and reported, a link to an undeclared source is
reported and left alone, and a failed refetch is a warning naming the release,
leaves nothing marked held, and is tried again by the next command. No command,
flag or hint offers a manual repair. Steps 1 and 2 left the restore inside
`Store.Ensure` (`internal/pluginstore/place.go`: `Held`, `trashUnmarked`,
`place`), reached only for the instance being applied, through
`keepMarketplaceLink` in `internal/workspace/pluginskills.go`, with `restored`
discarded and failures turned into a `MissingPluginRoot` warning that says to
re-run `niwa apply`. `create`, `dispatch` and the hook make a new instance and
never look at an older instance's links. This step adds the store's `Repair`
pass over every instance's links (the scan through `internal/hostdir`'s
instance listing, so `InstanceDirs` gains its first non-test caller), calls it
from `runPipeline` before plugin placement, reports through
`internal/fallbacknotice`, and removes the manual-repair wording.

**Acceptance Criteria**:

- [ ] With two instances linked to the same held release, deleting that
  release's `<commit>` directory and then running `niwa apply` in the second
  instance makes exactly one tarball request for it, puts back a marked,
  read-only release at the same path, and a skill from that marketplace
  resolves in the first instance at its root, in a repo and in a session
  worktree, with no command run inside the first instance.
- [ ] Deleting only that release's `held.json` counts as missing: the next
  `niwa apply` makes one tarball request for it and the release is marked again.
- [ ] The same restore (one tarball request, the older instance's skill
  resolving) happens when the next command is `niwa dispatch`, the SessionStart
  hook, or a `niwa create` whose marketplace track now resolves to a newer
  release, each run with no command inside the older instance.
- [ ] Each restore prints one line naming the release
  (`github.com/<owner>/<repo>@<commit>`) and the instances it served, in the
  terminal for `apply` and `create`, in `niwa dispatch` output and in the
  hook's context.
- [ ] A link whose commit is in no ref of its declared source and behind none
  of them (a fork-only commit) is refused: nothing is fetched or held for it,
  its link is left as it is, and the output names the release and says it was
  refused. A missing release whose commit is behind a ref of its source but at
  no tip is restored, which needs the tarball fake to answer GitHub's compare
  endpoint (today it has none, so every non-tip commit is refused as unchecked).
  A link to a source the workspace no longer declares is reported and left
  alone.
- [ ] With the `fetch-tarball` fault set to `error:` for the missing release,
  the command exits 0, warns with the release's name, leaves no `held.json` and
  no `<commit>` directory for it, and the next command with the fault cleared
  restores it with one tarball request.
- [ ] Repair runs once per command per workspace: with three instances linked
  to one missing release, one command makes one tarball request for it.
- [ ] A unit test of `Repair` on a store with one held, one missing and one
  unmarked release, linked from instances on disk, fetches only the two that
  aren't held and returns a result naming each restored release and its
  instances.
- [ ] No output, hint or help text names a repair command or tells the user to
  re-run a command to fix a release: the `MissingPluginRoot` hint and the
  `reachable.go` comment that names a `Repair` match the code.

**Check**: Run `make build-test`, then `make test-functional-plugin-release-hold`:
the new scenarios for a deleted release (apply, dispatch, the hook and a create
on a newer release), a deleted `held.json`, a fork-only commit, a failed
refetch then a clean retry, and three instances on one missing release pass,
and the request-count steps report the counts above. Then `go test -race
./internal/pluginstore/... ./internal/workspace/...` exits 0.

**Dependencies**: Step 2

**Files**: `internal/pluginstore/repair.go`, `internal/pluginstore/repair_test.go`, `internal/pluginstore/place.go`, `internal/pluginstore/reachable.go`, `internal/hostdir/hostdir.go`, `internal/workspace/apply.go`, `internal/workspace/pluginskills.go`, `internal/fallbacknotice/`, `test/functional/tarball_fake_server.go`, `test/functional/features/plugin-release-hold.feature`, `test/functional/pluginstore_steps_test.go`

### Step 4: a held release changed after placement is fetched again and reported as tampered

A held tree whose entry count, total size, write bits or newest ctime no longer match its seal is moved aside and fetched again at its commit by the next command that links or repairs it, and the output says the release was tampered with.

### Step 5: unused older releases are swept while any release an instance on disk links to survives

Each provisioning command deletes held releases no instance links to except the newest of each source by commit time, never one a live or kept instance (under the root or in `~/.niwa/w-<key>/kept/`) links to through create, apply, dispatch, the hook, reset or destroy, deletes nothing when a scan fails, and `niwa destroy --force` at the root removes the store unless kept instances remain.

### Step 6: share_plugin_releases = false gives a workspace's new instances their own full copy

With `share_plugin_releases = false` under `[workspace]` a create writes a full per-instance tree while another workspace on the host still links and the workspace's already linked instances keep loading skills and being repaired, and a moving ref gives file-for-file the same tree (`diff -r`) with the store on and off.

### Step 7: an unusable store falls back to a per-instance fetch and says so

An unreadable, unwritable, uncreatable or unplaceable store gives the create its own copy and a notice naming the store's path and the condition in the terminal (a narrowed mode on an existing `plugins/` is that condition and is never reset, and `unwritable` is a write probe of `plugins/` itself), in `niwa dispatch` output and in the SessionStart hook's context, three creates on an unwritable store each fall back, an unheld release whose source is down is reported with nothing held, and `docs/guides/repo-marketplaces.md` describes the store, the setting, the notice and the self-restore.

## Implementation Sequence

The steps land in order, as commits of one pull request. No step's work merges
on its own: steps 1 and 2 alone would link instances into a store that nothing
repairs yet, so a release deleted by hand would leave instances without skills
that today keep their own copy. Steps 6 and 7 need only step 2's placement
path and could be built before steps 3 to 5, but step 4 needs step 3's repair
pass and step 5 needs step 3's scan of every instance, so the plan keeps the
linear order and the owner details each step from what the one before taught.

The plan's owner details step 3 after step 2's work reaches its terminal, and
each later step the same way, in a commit that touches only this plan.

Notes to carry into execution:

- PR #511 merged as 0957d4a. Step 1 takes its `internal/filelock`
  (`WaitFileLock`, `CanonicalPath`), its workspace key and its lock-name
  scheme as they merged, and moves the key into `internal/hostdir` for both
  stores, with `internal/mirror` importing it.
- Kept instances under `~/.niwa/w-<key>/kept/` come from separate teardown
  work. Step 5's scenarios create that directory themselves, so the step does
  not wait on that work.
- The chmod scenarios in step 7 assume a non-root user, as CI runs.
- After step 7, run the design's by-hand walkthrough against real GitHub on a
  scratch `$HOME` with a fork the reviewer controls: count `/tarball/`
  requests across two creates, delete a release and its mark and watch the
  next command restore it, then turn the store off and make it unusable in
  each of the four ways. That walkthrough, not anything before it, is the
  first run against a real host's instances or store.
