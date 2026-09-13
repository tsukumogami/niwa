<!-- decision:start id="refresh-ordering" status="confirmed" -->
### Decision: How refreshes of a config directory are ordered

**Context**

Every refresh of a config directory (the workspace root's `.niwa/`, an overlay
clone under `$XDG_CONFIG_HOME/niwa/overlays/`, the global clone at
`$XDG_CONFIG_HOME/niwa/global`; `config/overlay.go:325`,
`config/registry.go:403`) ends in `materializeAndSwap`. It deletes and
recreates the fixed staging path `configDir + ".next"`
(`internal/workspace/snapshotwriter.go:355-364`), fetches into it
(tarball at `:378`, `git clone` at `:389`), writes the marker (`:423`), copies
`instance.json`, `dispatch-briefs/` and `sessions/` in from the live directory
(`:443`, `:454`, `:464`), and calls `SwapSnapshotAtomic` (`:469`). That
function deletes a fixed `target + ".prev"` (`snapshot.go:48-53`), renames
live to `.prev` (`:69`), staging to live (`:77`, rollback `:79`) and deletes
`.prev` (`:98`). Nothing orders any of this against a second refresh, against
`WriteSessionMapping` (`session_map.go:141-167`, whose `MkdirAll` at `:150`
recreates `.niwa` if it runs in the rename window), against
`DeleteSessionMapping` (`session_map.go:235-243`, called from `reap.go:487`),
or against the reaper's two reads (`reap.go:342`, `:580`), which see an empty
store mid-swap because `ListSessionMappings` maps ENOENT to "no mappings"
(`session_map.go:203-205`).

The refresh is frequent and long. A non-GitHub source re-materializes on every
call (`snapshotwriter.go:161-167`). A GitHub drift check can spend 3.5 s in
retry backoff (`:32-36`, `:176`) before the tarball fetch starts. One dispatch
refreshes twice (`configreload.go:66` via `instance_from_hook.go:454`, then
`apply.go:489`), after an opportunistic reap (`dispatch.go:492`) and before
its mapping write (`dispatch.go:877`). Research also turned up two facts the
PRD research didn't. The swap's preflight deletes `.prev` even when the target
is missing (`snapshot.go:53`), so after a crash between the two renames the
next refresh destroys the only copy of the old snapshot and its mappings. And
the refresh reads the live marker without any ordering (`snapshotwriter.go:92`,
`:139`), so a refresh can fail with ENOENT because another one is mid-swap,
which breaks R11 even with no mapping writer involved.

**Assumptions**

- No production path refreshes one config dir from two goroutines of the same
  process at once. `Apply` refreshes per instance in a sequential loop
  (`apply.go:640`); no goroutine surrounds any refresh call. If one is added
  later, the lock still works (flock contends per open file description) but
  the "no nesting" rule below has to be rechecked.
- A mapping writer and the swap are the only in-place mutators of the root
  `.niwa/` that this decision orders. Root `SaveState` belongs to the separate
  root-state decision, but it has the same `MkdirAll(.niwa)` hazard
  (`state.go:297-300`), so that decision should either route through this lock
  or stop creating `.niwa`.
- Mixed niwa versions running concurrently against one workspace get no
  guarantee. An older binary still uses the fixed `.next` without the lock.
- In `--auto` mode no user confirmed the call, so the status is `assumed`.

**Chosen: A. A short per-config-dir lock around carry-over and swap, with the fetch unlocked in a private staging directory**

*Lock file.* The lock for config dir `D` is the sibling file
`filepath.Clean(D) + ".lock"`: `<root>/.niwa.lock`,
`$XDG_CONFIG_HOME/niwa/overlays/<name>.lock`,
`$XDG_CONFIG_HOME/niwa/global.lock`. It sits outside the rotated directory, so
a swap never renames it away. It's created `O_CREATE|O_RDWR`, mode 0600, and
never unlinked. Unlinking a flock file lets a waiter holding the old inode and
a new opener on a fresh inode both believe they hold the lock. A sibling beats
`~/.niwa/locks/<hash>.lock` (the Codex precedent, `codex_trust.go:633-643`)
for three reasons. It doesn't depend on every process agreeing on `$HOME`,
which the hook, dispatch and tests don't guarantee. Two spellings of `D`
through a symlinked parent reach the same inode with no `EvalSymlinks` hashing
step. And its presence marks `D` as a niwa-refreshed directory, which crash
recovery uses below. The root lock is a regular file, so `EnumerateInstances`
skips it (`state.go:361`).

*Helper.* A new `configdir_lock_unix.go` / `configdir_lock_other.go` pair
copies `codex_trust_lock_unix.go:13-57`: non-blocking `flock` polled every
20 ms against a deadline. Mode is `LOCK_SH` or `LOCK_EX`. The bound is
`const configDirLockTimeout = 30 * time.Second` feeding a package variable
that tests override, the same way `driftCheckBackoff` works. On timeout the
error reads `timed out after 30s waiting for another niwa command using
<D> (lock <D>.lock)`. The `!unix` file returns a no-op release and carries the
comment that no ordering is provided on that platform, matching
`codex_trust_lock_other.go:62-71`. The helper goes in a small leaf package so
both `internal/config` (discovery-time recovery) and `internal/workspace` can
import it. That adds a package, not a module.

*What takes the lock, and in which mode.*

| Operation | Mode | Scope |
|---|---|---|
| Refresh: marker/`.git` probe and `ReadProvenance` (`snapshotwriter.go:92-93`, `:139`) | SH | the reads only, released before `HeadCommit` and the fetch |
| Refresh: fetch into staging | none | `HeadCommit` retries, tarball, `git clone` all unlocked |
| Refresh: sweep, carry-over (`:443-467`), swap (`:469`) | EX | milliseconds |
| Refresh no-drift marker rewrite (`:186-193`) | EX | re-read the marker under the lock; rewrite `fetched_at` only if `resolved_commit` still equals the value just checked, via temp file then rename |
| Post-swap reload in `ReconcileAndReloadConfig` (`configreload.go:73`) | SH | the `config.Load` only |
| `WriteSessionMapping`, `DeleteSessionMapping` | EX | the temp-write and rename, or the remove |
| `ListSessionMappings`, `ReadSessionMapping` (reaper, `niwa list`, worktree destroy) | SH | the directory scan only |

Readers take `LOCK_SH`. They only need to exclude the rotation, not each
other, and the reaper runs at the top of every dispatch, create and watch, so
four parallel dispatches shouldn't queue their sweeps behind one another.
Mutators take `LOCK_EX`. Nothing ever upgrades from SH to EX, because flock's
upgrade isn't atomic.

Because the read locks live inside the store functions, R15 holds for every
current and future reader: the reaper's sweeps and destroy's resolution decide
on a snapshot of the store taken while no swap was running. What happens
between that read and the destroy is the check-then-act gap the reaper already
documents (`reap.go:464-470`) and is out of scope.

*Staging and prev paths.* Each refresh creates a private wrapper
`os.MkdirTemp(parent, base(D)+".next-*")`. Inside it are a `lock` file that
the refresh holds `LOCK_EX` for as long as it lives, and a `snap/` directory
the fetch writes into, the same empty-directory shape the fetch gets today
(`snapshotwriter.go:362`). The swap renames `snap/` to `D`, then removes the
wrapper. Keeping the lock file beside the content, not inside it, means it
never ends up in the snapshot. Under the lock, `prev` stays at the fixed path
`D + ".prev"`: only one swapper runs at a time, and a single known path makes
crash recovery deterministic.

*Sweep and recovery, run first by every EX holder of `D`'s lock.*
1. If `D` is missing and `D.prev` is a directory, the refresh was killed
   between the two renames: rename `D.prev` back to `D`. This replaces the
   unconditional delete at `snapshot.go:53`.
2. If `D` exists and `D.prev` exists, the refresh was killed after the second
   rename: remove `D.prev`.
3. For each `D.next-*` wrapper, try `LOCK_EX|LOCK_NB` on its `lock` file.
   Acquired means the owner is dead: remove the wrapper. Busy means a refresh
   is still fetching: leave it. The caller's own wrapper is busy through its
   other file descriptor, so it's skipped. The legacy fixed `D.next` from older
   binaries is removed too.

These rules cover all four kill points. Killed while fetching or during
carry-over leaves a wrapper whose lock the kernel released, and the next
refresh sweeps it (step 3). Killed between the renames is handled by step 1.
Killed after the swap is handled by step 2. That meets R16 and both
kill-point acceptance criteria.

*Recovery for commands that never take the lock.* The PRD also requires that
the next command to read the directory, not just the next refresh, finds
`workspace.toml` and the mappings. Every command finds the root through
`config.Discover` (`discover.go:25-36`, from `cwd_classify.go:98`, `:126`).
So when Discover finds no `<dir>/.niwa/workspace.toml`, but `<dir>/.niwa.lock`
and `<dir>/.niwa.prev/` both exist, it takes the EX lock, repeats step 1 if
it still applies, and re-checks. A live swap holds EX, so this waits a few
milliseconds and then finds `D` back in place; it never races the swap.

*A writer never recreates `.niwa`.* Under EX, after recovery, the mapping
writer creates only `sessions/` and requires `.niwa` to exist, using
`os.Mkdir` on the leaf instead of `MkdirAll` on the path. A missing `.niwa`
under the lock means the workspace itself is gone, which is an error, not
something to recreate.

*In-process reentrancy: callers are structured so no lock scope nests.* There
is no process-local registry. The lock is taken only inside unexported leaf
helpers, which never call another lock-taking function and never call back
into caller code while holding it. There's no exported `WithConfigDirLock(fn)`.
A dispatch then runs this sequence, one acquisition after another:
- the reap's SH read, then one EX delete per target (`reap.go:342`, `:487`);
- refresh 1: SH probe, unlocked fetch, EX swap, SH reload;
- refresh 2 (`apply.go:489`): the same;
- the mapping write under EX (`dispatch.go:877`).

Nothing nests, so R19 holds with no special handling. A registry was rejected
for three reasons. Go has no goroutine identity to key it on. It would hide
real contention between goroutines. And it can't express SH-held-then-EX
anyway. A unit test that runs the dispatch-shaped sequence with the bound set
to 1 s guards the rule.

**Rationale**

Only A satisfies R18 and R19 together while keeping R12-R16 deterministic. The
lock is held only for local file copies and two renames, so a 3 s fetch in one
command never delays another command's mapping write or swap, and four
dispatches' eight refreshes fetch in parallel and queue only for milliseconds.
That's how R11's "the bound must not trip for four concurrent dispatches"
holds. Ordering carry-over, swap, and every in-place mutation under one
exclusive lock closes lost writes, resurrected deletes and the `MkdirAll`
gutting in one mechanism. Taking read locks inside the store functions makes
R15 structural instead of a per-caller discipline. The design reuses a pattern
the codebase already ships (`codex_trust_lock_unix.go`,
`cli/writer_lock_unix.go:72`) and adds no module, because `syscall.Flock` is in
the standard library.

**Alternatives Considered**

- **B. The same lock held for the whole refresh, including the fetch.**
  Simpler: staging can stay at a fixed path and nothing needs a liveness
  check. Rejected because it breaks R18 by construction: another command's
  mapping write waits for the fetch, which includes up to 3.5 s of `HeadCommit`
  backoff (`snapshotwriter.go:32-36`) plus the tarball or clone. It also
  serializes the eight fetches of four parallel dispatches, which puts R11's
  30 s bound within reach of an ordinary slow network.
- **C. No lock; move the mapping store out of the rotated directory, plus
  unique staging and prev paths.** Rejected. The PRD explicitly leaves moving
  niwa's local state out of the config directory (#74) out of scope, and it
  would mean migrating existing mappings. It also doesn't fix refresh against
  refresh. With unique paths and no ordering, refresh A renames live to its
  prev, refresh B's `Lstat` sees no target and renames its staging into place,
  and A's second rename fails with ENOTEMPTY (`snapshot.go:64-83`). That's
  exactly the failure R11 forbids. The no-drift marker rewrite and the unlocked
  marker reads stay exposed too.
- **D. No lock; after the swap, merge mappings back from `.prev` and record
  deletes as tombstones, plus unique paths.** Rejected. It inherits C's
  refresh-against-refresh failure. It doesn't stop `MkdirAll` recreating
  `.niwa` in the rename window, and it doesn't fix a reader taking an empty
  read mid-swap (R15). Tombstones also need their own garbage collection. The
  result is more code than A with weaker guarantees.
- **E. No lock; swap with an atomic directory exchange (`renameat2`
  `RENAME_EXCHANGE` on Linux, `renamex_np` `RENAME_SWAP` on macOS, both in
  `golang.org/x/sys/unix`, which `go.mod` already requires).** This removes
  the window where the directory is missing, but it does nothing to order a
  mapping write against carry-over, so writes are still lost unless D's merge
  is bolted on. Support also varies by filesystem, and the fallback brings
  back every race. Rejected as the ordering mechanism, but worth keeping as
  hardening inside A's swap (see Consequences).

**Consequences**

- New files: the lock helper pair, and `<D>.lock` beside each refreshed
  config dir. `materializeAndSwap` changes shape: an unlocked fetch into a
  private wrapper, then one EX section for sweep, carry-over and swap.
  `SwapSnapshotAtomic`'s preflight becomes the recover-or-remove rule.
  `refreshSnapshot`'s marker reads and `ReconcileAndReloadConfig`'s reload
  take SH. The no-drift marker rewrite takes EX, compares before writing, and
  becomes atomic. The mapping store's four functions take the lock themselves.
  `config.Discover` gains one conditional recovery branch.
- The EX section gives the test-seam decision a natural point to force the
  interleaving: a hook between carry-over and swap. A forced mapping write
  there blocks within the bound and lands after the swap, which is what the
  PRD's acceptance criteria assert.
- A refresh that loses the race still swaps its own fetch in afterwards, so
  the last successful swap wins (R14). Two refreshes of the same commit both
  swap; skipping a redundant swap is a possible later optimization, not a
  requirement.
- Still exposed: readers of config content during a long pipeline (content
  installs reading `D` over seconds) can hit the microsecond rename window
  unlocked. Holding SH across the pipeline would break R18. This is the PRD's
  "other readers can still see a swap" limitation. The exchange swap from E
  (fall back to two renames on EINVAL/ENOTSUP, and for a first-time
  materialization with no existing target) would close that window on
  supporting filesystems without a new module. It's recommended as a
  follow-up, not required for R11-R16.
- On non-unix platforms the lock is a no-op, so today's races remain there.
  Discovery-time recovery could then race a live swap. The worst case is that
  swap failing and the old snapshot staying in place, never a gutted
  directory.
<!-- decision:end -->
