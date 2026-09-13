# Architecture review: DESIGN-session-store-teardown (phase 6)

Scope: the Solution Architecture, Implementation Approach and Considered Options
sections, checked against the code in `internal/workspace`, `internal/cli`,
`internal/worktree` and `internal/config`.

## Verdict

The architecture fits the codebase and is close to implementable. The package
graph is sound: no cycles and no upward imports. Two structural problems should
be fixed in the design before implementation, because both would be copied:

1. The lifted `IsSingleInstanceLayout` rule misclassifies every zero-instance
   multi-instance root as a single-instance layout.
2. The snapshot swap's on-disk naming (`.prev`, `.lock`, `.next-*`) is spelled
   in four places across three packages with no single owner.

Everything else is advisory: clarity gaps, phase bookkeeping, and one
centralization that would make "every exclusive holder runs recovery"
structural rather than by convention.

## What was verified against the code

- **Import direction.** `internal/config` imports only `secret`, `vault` and
  `source` (`maybesecret.go`, `registry_mirror.go`). `internal/workspace`
  imports `config` (e.g. `cwd_classify.go`, `snapshotwriter.go`).
  `internal/worktree` imports only `gitexclude` (`worktree.go:18`). The new
  edges are `config -> dirlock -> testhook`, `workspace -> dirlock`,
  `workspace -> testhook` and `cli -> workspace`/`worktree`. All point
  downward, and none creates a cycle. `testhook` importing only `sync` and
  `sync/atomic` keeps it a true leaf. `worktree` stays a leaf because the
  resolver lives in `cli`.
- **Named functions exist where stated:**
  - `materializeAndSwap` (`snapshotwriter.go:353`), `SwapSnapshotAtomic`
    (`snapshot.go:37`) and the three `preserve*` copies (`snapshotwriter.go:483/514/554`)
  - the four mapping-store functions (`session_map.go:141/172/199/235`)
  - `SaveState` (`state.go:297`), `saveWorkspaceRootDisclosures` (`apply.go:2727`)
    and the root `LoadState` in `Create` (`apply.go:481`)
  - `ClassifyCwd` (`cwd_classify.go:86`) and `config.Discover` (`discover.go:19`)
  - `discoverInstanceRoot`/`resolveInstanceRoot` (`cli/session.go:136/151`),
    `runSessionDestroy` (`session_lifecycle_cmd.go:539`),
    `runSessionLifecycleList` (`:608`) and `annotateFromSessionMappings`
    (`list.go:167`)
- **The nine callers of `resolveInstanceRoot` are confirmed:**
  `session_lifecycle_cmd.go` (create, apply, destroy, list),
  `session_attach_register.go` (attach, detach), `go.go`, `completion.go`, and
  `session_from_hook_cmd.go`.
- **The claim that `Execute` prints errors verbatim with exit 1 holds.**
  `root.go` maps `*sessionattach.ExitCodeError` to its code and gives
  everything else exit 1.
- **The existing Codex trust lock is exactly what the design says it copies**
  (`codex_trust_lock_unix.go`): a non-blocking exclusive `flock` polled every
  20 ms against a 30 s deadline, and one open file description per
  acquisition, so goroutines in one process contend the way separate
  processes do. The `_other.go` no-op exists as described.
- **The shared lock around the post-swap reload can't self-deadlock.**
  `config.Load` (`config.go:560`) never calls `config.Discover`, so it can't
  reach Discover's new exclusive-taking recovery branch while
  `ReconcileAndReloadConfig` holds the shared lock.
- **There is no nesting on the dispatch path.** `dispatch.go` calls
  `reapOpportunistically`, then the provision path (two refreshes), then
  `WriteSessionMapping` (`:877`), strictly in sequence.

## Question 1: Is it clear enough to implement?

Mostly yes. The component list, interface signatures and data flows are
specific enough to code from. These gaps would force an implementer to guess.

**1a. The phantom `SaveState` split in Phase 1.** Phase 1 says "splitting
`SaveState`'s single write where a gap is needed," but none of the four named
hook points is inside `SaveState`. Either name a fifth point, such as the one
the torn-read test for the `instance.json` atomicity requirement holds on, or
drop the phrase and say which existing point that test uses. As written, a
reader can't tell how the half-written-`instance.json` test is forced.

**1b. The parent directory must exist before the lock.** `D.lock` and the
`os.MkdirTemp(parent, ...)` wrapper both need `filepath.Dir(D)` to exist.
Today `materializeAndSwap` runs `MkdirAll(parent)` only after the fetch
(`snapshotwriter.go:429`). For first-time materialization of
`$XDG_CONFIG_HOME/niwa/global` (via `config_set.go:76`) and of overlays,
`$XDG_CONFIG_HOME/niwa` or `overlays/` may not exist yet. The design should
say the `MkdirAll(parent)` moves to the top, before the probe lock.

**1c. Recovery on every exclusive holder.** The lock table and the Decision 1
text say every exclusive holder runs recovery first. Under Components, only
the refresh and the mapping writer/deleter mention it. The root disclosure
write (`saveWorkspaceRootDisclosures`) and the no-drift marker rewrite don't.
This is also the structural point in the Question 4 recommendation below.

**1d. The root `LoadState` in `Apply`.** `Apply` has a second root read with
a discarded error (`apply.go:664`, `wsRootState, _ := LoadState(workspaceRoot)`)
that feeds the same `saveWorkspaceRootDisclosures`. The skip-after-failed-read
fix covers both call sites because it lives in the callee, but only `Create`
gets the `root-state-read` hook, so the `Apply` path goes untested. Place the
hook in both, or say explicitly that one test covers the shared callee.

**1e. Accuracy of the lifecycle-store claims.** Two statements don't match the
code.

- Context and Decision 2 say that at the root, worktree commands "read the
  mapping store as lifecycle records." `ListSessionLifecycleStates` filters on
  `^[0-9a-f]{8}\.json$` (`session_lifecycle.go:22`), so UUID mapping files are
  never parsed. At the root, the commands see an empty lifecycle store. The
  user-visible failure is "no such worktree" or an empty list, not misparsed
  records. This doesn't change the fix, but the framing should be corrected.
- The Data Flow says lifecycle records live in "the instance's own `.niwa/`,
  which no refresh rotates." In the single-instance layout that's false: the
  instance's `.niwa/` is the refreshed config directory (`Apply` refreshes
  `configDir` at `apply.go:640`). The PRD already lists this exception under
  Known Limitations; the design should repeat it. Beyond the lost-write window
  for unlocked `CreateSession`/`DestroySession` writes there, `.niwa/worktrees/`
  isn't in the three-item carry-over set. A refresh of a single-instance root
  rotates the worktree checkouts away. That is pre-existing and out of scope,
  but it matters for finding 5a.

**1f. Root writers outside the lock.** Two more code paths write the root
`instance.json` besides `saveWorkspaceRootDisclosures`: `init.go:772`, and in
the single-instance layout, `Apply`'s instance `SaveState` (`apply.go:592/792`),
where the instance root is the workspace root. Atomic `SaveState` covers
these for tearing, but they aren't ordered against a concurrent refresh. The
lock table should list them as intentionally unlocked: init runs on a fresh
root, and the single-instance layout has no dispatch concurrency. Otherwise a
later reader will assume the table is exhaustive.

**1g. Key normalization in `NewestMappingPerInstance`.** The helper keys by
`filepath.Clean(InstancePath)`, while `annotateFromSessionMappings` today keys
by the raw `m.InstancePath` and looks up by `records[i].Path`. Record paths
come from `filepath.Join`, so they're already clean. Adopting the helper
therefore silently starts matching mappings whose recorded path wasn't clean.
That's probably desirable, but it's a small change in `niwa list` behavior and
should be called out (the design says "no behavior change beyond the PRD's").

## Question 2: Missing components or interfaces

**2a. No single owner for the swap's on-disk naming (Blocking).** The `.prev`
suffix is currently computed in one place, `SwapSnapshotAtomic`
(`target + ".prev"`). After this design it is spelled in four places across
three packages:

- the `.prev` rename in `SwapSnapshotAtomic` (`workspace`)
- the cleanup in `recoverConfigDir` (`workspace`)
- `dirlock.RecoverMovedAsideLocked` (`dirlock`)
- the check in `config.Discover` (`config`), which also spells `.niwa.lock`
  and `.niwa.prev/` literally

The `.next-*` wrapper pattern is likewise created in `workspace` and swept in
`workspace`, with its liveness check in `dirlock`. This is the drift hazard
the codebase already documents for `sessionsDirName` (`session_map.go:112-118`):
two literals that must agree, where a mismatch fails silently. Here, recovery
would stop finding the moved-aside directory.

Recommendation: `dirlock` owns the layout alongside the lock. Export
`LockPath(dir)`, `PrevPath(dir)` and `StagingPattern(dir)`; have
`SwapSnapshotAtomic`, `recoverConfigDir` and `config.Discover` call them.
Because the package now knows the swap's layout, not just locking, consider
naming it `internal/configdir` (lock, layout and moved-aside recovery) rather
than `dirlock`. `dirlock` is the natural home either way: `config` can't
import `workspace`, and `dirlock` is already below both.

**2b. No single exclusive-section entry point in `workspace`.** See Question 4.

## Question 3: Phase sequencing

The order (seam, then primitive, then ordering, then root guards, then
teardown, then docs) is right: each phase depends only on earlier ones.
`dirlock`'s contended hook needs `testhook` from Phase 1, and Phases 3 and 4
need `dirlock` from Phase 2. Four points need fixing.

**3a. The teardown-read forced test can't exist in Phase 1.**
`loadMappingsForDestroy` and destroy-by-session arrive in Phase 5. On today's
code, a "teardown's read lands mid-swap" test can only target
`ListSessionMappings` directly, which makes it the same test as the reaper's.
Either define the Phase 1 test as "a mapping-store read lands mid-swap"
(covering both consumers, since the lock lives inside the store) and add a
thin destroy-path test in Phase 5, or move the teardown variant to Phase 5.

**3b. "The Phase 1 forced tests turn green here" (Phase 3) overclaims.** The
`root-state-read` test turns green in Phase 4, and any destroy-path test in
Phase 5. List the phase where each forced test is expected to go green, so a
reviewer can check each intermediate commit.

**3c. Red commits vs. merge unit.** Phase 1 is deliberately red. That's fine
if all phases ship in one PR with CI judged on the head commit. It isn't fine
if the phases become separate PRs, since Phase 1 couldn't merge. State that the
phases are commits in one PR, or that Phase 1 lands with its forced tests
gated behind a skip that Phase 3 removes. The first keeps the "fails on
today's code" evidence in history, which the determinism requirement asks for.

**3d. Phase 5 bundles two unrelated changes.** It touches the shared
`resolveInstanceRoot` (nine callers) and adds the new resolver. The refusal
change alters behavior for create, apply, attach, detach, `go`, completion and
the hook. It could land as its own step before the resolver so its
command-table tests are reviewed in isolation. This is advisory.

## Question 4: Simpler alternatives overlooked

**4a. One exclusive-section helper instead of per-caller discipline.** The
design's correctness rests on two conventions that are enforced only by
review: every exclusive holder runs recovery first, and no lock scope nests.
A single unexported helper in `workspace` makes both structural:

```go
func withConfigDirExclusive(dir string, fn func() error) error // Acquire(Exclusive); recoverConfigDir(dir); fn(); release
```

The refresh's exclusive section, `WriteSessionMapping`, `DeleteSessionMapping`,
the no-drift marker rewrite and `saveWorkspaceRootDisclosures` all go through
it. A shared twin (`withConfigDirShared`) covers the probe, the store reads
and the reload. The next writer added to the config directory then gets
recovery and ordering by default instead of by remembering. Gap 1c goes away
too, and the no-nesting rule becomes one place to audit. `config.Discover`
keeps its narrow `dirlock.RecoverMovedAside` path, because it can't reach
`workspace`.

**4b. Reuse for the Codex trust lock (advisory, follow-up).** With `dirlock`
added, the codebase has four `flock` helpers:

- `codex_trust_lock_unix.go` (workspace): blocking with a deadline
- `writer_lock_unix.go` (cli): probes a foreign lock file with no `O_CREAT`;
  the semantics really are different
- `sessionattach.acquireAttachLock`: a single non-blocking try, the same shape
  as `dirlock.TryAcquire`
- `dirlock`

The Codex trust lock is a strict subset of `dirlock.Acquire(Exclusive)` with
the same poll interval and bound. It should move onto `dirlock` in a
follow-up, so there's one blocking-lock implementation. This isn't blocking
here, since the design copies the pattern faithfully and nothing else will
copy the duplicate.

**4c. The seam choice is sound.** The alternatives for the test seam were
considered well. Two notes. Keeping the hooks as unexported variables in
`workspace` genuinely can't reach `cli` tests, so the registry is justified.
And the atomic-exchange swap is correctly deferred as hardening, not
ordering; I found no simpler ordering mechanism than the short lock.

## Question 5: Layering and dependency direction

There are no violations: `testhook` is a leaf, `dirlock` depends only on
`testhook`, `config -> dirlock` keeps `config` below `workspace`, the resolver
stays in `cli`, and `worktree` is untouched. Three structural notes remain.

**5a. The lifted single-instance rule misclassifies zero-instance roots
(Blocking).** `IsSingleInstanceLayout(root)` is defined as "no child
instance, root has `instance.json`", lifted from `resolveRegistryScope`
(`cli/apply.go:358-369`). Every registered `niwa init` writes root
`instance.json`: `buildInitState` sets state whenever ephemeral mode is on,
and that's the default (`init.go:1024-1025`). So every freshly initialized
multi-instance workspace, and every workspace whose instances were all
reaped or destroyed, satisfies the rule.

Once `discoverInstanceRoot` uses the helper, `niwa worktree create` at such a
root succeeds against the root. It puts a worktree under
`<root>/.niwa/worktrees/`, which the next refresh rotates away (see 1e). The
commands it's meant to refuse don't get refused, and teardown's
`resolveDestroyScope` treats the root as an instance. `ClassifyCwd`'s own
comment (`cwd_classify.go:113-119`) records that treating the root as an
instance is the bug that made `apply` clone into the root.

Lifting the rule into a named, shared helper spreads its false positive to
nine more callers, and future callers will copy it. `buildInitState` never
sets `InstanceName` (`init.go:1030-1036`), while a real instance's state
carries it, so a discriminator is available: read the root state and require
a non-empty `InstanceName`, or another instance-only field, in addition to
"no child instance". Define it in the helper, so `apply` gets the fix too.

**5b. `config.Discover` gains a side effect.** A read-only path walk now can
take an exclusive lock, wait up to 30 s, and rename a directory. It's called
from completion, status, `go`, create, watch and the hook. The branch fires
only when `.niwa/workspace.toml` is missing and both `.niwa.lock` and
`.niwa.prev/` exist, so the common path adds two `stat` calls per missing
level at most. That's acceptable, but the design should say two things: the
branch is taken only at the candidate that has `.niwa.prev/`, and no lock
holder calls `Discover`. The second is true today; nothing in the exclusive
or shared sections calls it.

**5c. Two test seams at one point.** `snapshot.go` already calls
`testfault.Maybe("snapshot-swap")`, and it would also gain two `testhook.Hit`
calls. `Hit` returns an error, which overlaps `testfault`'s one capability.
Either make `Hit` return nothing (hold-only), or state the boundary in both
package docs: `testfault` is env-driven, cross-process and error-only;
`testhook` is in-process, holds on a channel, and can't be triggered from a
release binary. Otherwise the next contributor won't know which seam to
extend.

## Summary of recommendations

| # | Change | Severity |
|---|---|---|
| 5a | Tighten `IsSingleInstanceLayout` so an init-only root state (no `InstanceName`) is not a single-instance layout | Blocking |
| 2a | Give one package (`dirlock`, perhaps renamed `configdir`) ownership of `LockPath`/`PrevPath`/staging pattern; `SwapSnapshotAtomic` and `config.Discover` call it | Blocking |
| 4a | Route every exclusive and shared section through one `workspace` helper that acquires, runs recovery, and releases | Advisory, strongly recommended |
| 1a-1g | Name the `SaveState` hook or drop it; hoist `MkdirAll(parent)`; cover `Apply`'s root read; correct the two lifecycle-store claims; list intentionally unlocked root writers; note the `Clean` key change | Clarity |
| 3a-3d | Re-scope the Phase 1 teardown test; list the green phase per test; state the merge unit; optionally split the refusal from the resolver | Sequencing |
| 4b, 5b, 5c | Follow-up to move the Codex trust lock onto `dirlock`; document Discover's side effect; state the testfault/testhook boundary | Advisory |
