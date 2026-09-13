# Security Review: session-store-teardown

Scope: the design at `docs/designs/DESIGN-session-store-teardown.md`, read
against its PRD and the current code in `internal/workspace`
(`session_map.go`, `snapshot.go`, `snapshotwriter.go`, `state.go`,
`codex_trust_lock_unix.go`), `internal/config` (`discover.go`, `overlay.go`),
`internal/cli/session.go` and `internal/worktree` (`worktree.go`,
`session_lifecycle.go`).

Threat model: niwa runs as the invoking user, with no setuid and no daemon. A
process running as the same user can already rewrite anything niwa touches, so
same-user findings matter mainly as robustness and accident-safety concerns
(a crafted or corrupt record causing damage the user didn't ask for). Other
users matter only where niwa reads or mutates paths they can write: shared
parent directories on the `config.Discover` walk-up, or a workspace created
under a group-writable directory.

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes

**User-supplied destroy value.** The positional value is only ever compared as
a string against fields of the loaded mappings (session id, handle, the first
8 characters of the id) and against worktree ids of the current instance. It
is never joined into a path before a match, and `ReadSessionLifecycleState`
already refuses anything that isn't `^[0-9a-f]{8}$` before it builds a path.
Mapping paths come from `sessionMappingPath`, which checks the UUID format. So
the value can't be used for path traversal. The design should say plainly that
the resolver never passes the raw value to a path builder, and that a
worktree-id match goes through the validated lifecycle reader or the listed
records.

**Mapping contents (`instance_path`, `handle`, `created`).** Any same-user
process can write these files, so they aren't trustworthy. The PRD's ordered
path checks are the right defense: the path must lexically be a direct child of
the root other than `.niwa`; `Lstat` for existence; after resolving symlinks it
must be one of the directories `EnumerateInstances` returns; and it must be the
newest mapping for its instance. The acceptance criteria cover the
outside-the-workspace, root-itself and symlink-out cases. Two gaps remain:

- TOCTOU between the checks and `DestroySession`. The design passes the
  instance directory on after the checks, and `DestroySession` then does
  `ReadDir` and runs `git -C <repo>` under that path. If a same-user process
  swaps the checked directory for a symlink in between, the destroy follows
  it. The window is small and the attacker must already be the user, so
  severity is low. Mitigation: hand `DestroySession` the path produced by the
  symlink-resolving membership check (the enumerated directory), not the
  recorded string, and re-`Lstat` it to confirm it's a real directory
  immediately before each call.
- Fields that aren't paths still come from the file. `handle`, `session_id`
  and `instance_path` are printed in the ambiguity (exit 4), refusal (exit 1)
  and outcome lines. A crafted mapping could embed terminal control sequences.
  Mitigation: format every mapping-derived value with `%q`, or strip control
  characters, as the no-match line already does for the user's own value.
  `EnumerateInstances` itself rejects control characters in names through
  `ValidName`.

The resolver's UUID filter on `session_id` (in `loadMappingsForDestroy`) also
works as a validation step. Mappings with a non-UUID key are dropped before
matching, so a hand-planted `sessions/foo.json` can't become a match.

**Lifecycle records reached through a session.** Once an instance is picked,
`destroySessionWorktrees` acts on every active record in its `.niwa/sessions/`.
`DestroySession` then trusts `state.WorktreePath`, `state.Repo` and the branch
name from JSON: it runs `git worktree remove --force <WorktreePath>` and
`git branch -d|-D <branch>`. This behaviour already exists (destroy by
worktree id uses it today). `findRepoInWorkspace` limits `Repo` to a
directory-name match two levels under the instance, and `git worktree remove`
only removes worktrees registered to that repo. The new part is reach: one
command now processes every record in an instance chosen by a mapping, not one
record the user named. It's low risk because the instance must already pass the
membership check. As a cheap hardening step, check before calling git that
`WorktreePath` sits under the instance's worktree tree and that the branch name
doesn't start with `-`, or pass `--` ahead of it.

**Planted `<dir>.prev`, `<dir>.next-*`, `<dir>.lock`.** This is the most
important part of this dimension.

- `.prev` rename-back. Recovery renames `D.prev` to `D` when `D` is missing. If
  `D.prev` is a symlink, a regular file, or a directory niwa didn't create,
  `rename(2)` moves that entry into `D`, and every later command reads its
  configuration and writes mappings through it. For the workspace root, this
  needs write access to the workspace root, which already gives full control,
  so it isn't an escalation. Two cases still make it worth guarding:
  1. `config.Discover` now runs this recovery on every candidate directory it
     walks up through, from any command: shell completion, the SessionStart
     hook, `status`. A walk that crosses a shared, non-sticky, group-writable
     ancestor holding `.niwa.lock` and `.niwa.prev/` makes the victim rename
     another user's directory into `.niwa` and then load it as workspace
     config. Sticky `/tmp` blocks the rename (EPERM on another user's entry).
     And the walk-up already trusts any ancestor's `.niwa/workspace.toml`, so
     the extra exposure is narrow. But the new branch turns a read-only
     discovery into a filesystem write in directories the user may not own.
  2. Overlay name collisions (next item).

  Mitigation: recovery renames back only when `Lstat(D.prev)` is a real
  directory (not a symlink) owned by the current uid and containing the
  provenance marker the swap wrote. The `Discover` branch also requires the
  candidate directory and `.niwa.lock` to be owned by the current uid, and
  otherwise leaves the candidate alone and keeps walking.

- Overlay directory names can collide with the new suffixes. `OverlayDir`
  names clones `<org>-<repo>` under the per-user
  `$XDG_CONFIG_HOME/niwa/overlays/`, a directory shared by all of the user's
  workspaces. GitHub repo names may contain dots, so an overlay
  `acme/cfg.prev` lives at `overlays/acme-cfg.prev` and an overlay
  `acme/cfg.next-x` lives at `overlays/acme-cfg.next-x`. Refreshing overlay
  `acme/cfg` then:
  - removes `acme-cfg.prev` when both exist. The current unconditional
    preflight delete in `SwapSnapshotAtomic` already does this, so it isn't
    new.
  - renames `acme-cfg.prev` into `acme-cfg` when `acme-cfg` is missing. This
    is new: a different repository's clone becomes this overlay, and its hooks,
    settings and env tables are applied.
  - treats `acme-cfg.next-x` as a dead staging wrapper. If `TryAcquire` opens
    the wrapper's `lock` with `O_CREATE` (the existing Codex helper's pattern),
    it creates the file, gets the lock, and deletes the whole unrelated clone.

  Both need a same-user repo naming coincidence, and the data lost is a
  re-cloneable checkout, so severity is low to moderate. The config
  substitution is the worse outcome. Mitigation: identify wrappers
  structurally, not by glob alone. The candidate must be a real directory
  (`Lstat`), niwa must have created its `lock` file with `O_CREAT|O_EXCL`, and
  the candidate must hold a `snap/` entry. The sweep opens `lock` without
  `O_CREATE`; a missing lock file means "not a wrapper, or one still being set
  up", and the sweep skips it. Apply the marker check above to `.prev`. Picking
  a staging suffix that can't occur in an `<org>-<repo>` name would remove the
  collision outright, but a suffix built only from characters GitHub forbids
  isn't portable to `file://` names, so the structural check is the one to
  rely on.

- Wrapper sweep TOCTOU against a live owner. `os.MkdirTemp` creates the
  wrapper, then the owner creates and flocks `lock`. A sweeper running under
  the directory lock in that gap finds a wrapper with no held lock and deletes
  it, and the owner's fetch fails. That's a correctness and availability bug,
  not a security bug, and the no-`O_CREATE` rule above closes it. A wrapper
  with no lock file is skipped, unless an optional age threshold (older than
  the lock timeout) says it's abandoned.

- Lock file symlink. `Acquire` opens `D.lock` with `O_CREATE|O_RDWR`, which
  follows a symlink. A planted `D.lock -> /some/path` makes niwa create an
  empty 0600 file at that path, or open an existing file read-write and flock
  it. There's no truncation or write, so the effect is small: an empty file
  appears somewhere the user can write. Mitigation: open with `O_NOFOLLOW`
  (`syscall.O_NOFOLLOW` is available on Linux and macOS from the standard
  library), `fstat` to require a regular file, and on failure return an error
  that names the path.

- Recovery TOCTOU in general. All of recovery's check-then-act steps (missing
  `D` plus existing `.prev`, the wrapper liveness check) run with the exclusive
  lock held. That orders them against every current-version niwa writer, and
  the design already accepts that older niwa versions and non-niwa writers
  aren't ordered. Nothing else is needed as long as each step `Lstat`s right
  before acting and uses `safeRemoveAll`, which removes a top-level symlink
  without following it.

**Config tarballs and clones.** The fetch path is unchanged, apart from
landing in `D.next-X/snap` instead of `D.next`. Extraction still writes into
a niwa-created directory, so this design adds nothing new on the fetch side.
The wrapper being private (0700, see below) is a small improvement.

### Permission Scope

**Applies:** Yes (low severity; mostly about keeping current modes)

- Lock files: 0600, empty, never unlinked. Other users can't open them, so
  they can't flock them. Good.
- Staging wrappers: `os.MkdirTemp` creates them 0700, narrower than today's
  0755 `configDir + ".next"`. The design doesn't say what mode `snap/` gets,
  and `snap/` becomes the live `D` at the swap. If it's made inside a 0700
  wrapper with a 0700 mode, `.niwa` silently goes from 0755 to 0700. That's
  safe but it's a behaviour change the PRD rules out. Mitigation: create
  `snap/` 0755 (umask applies), matching today's staging directory, and rely
  on the 0700 wrapper for privacy while the fetch runs.
- Mapping store: `preserveSessionMappings` already chmods the carried
  `sessions/` to the source mode, and files keep 0600 through the copy.
  Nothing in the design changes the carry-over. The mapping writer moves from
  `MkdirAll(..., 0o700)` to `os.Mkdir` of the `sessions/` leaf alone; the
  design must keep 0700 on that call. Temp files keep 0600.
- `instance.json`: the atomic `SaveState` will most likely use
  `os.CreateTemp`, which makes files 0600, where today's `WriteFile` uses
  0644. Pick one on purpose: chmod the temp file to 0644 before the rename to
  keep current behaviour, or document the narrowing. Use a unique temp name
  (`CreateTemp`), not a fixed `.tmp`, because the root file is written by
  several commands. The root write happens under the exclusive lock, but
  instance-level `SaveState` calls don't.
- Escalation: none. There's no setuid, no sudo, and no new privileged
  operation.
- Teardown's deletion scope: the membership check keeps `DestroySession`
  inside enumerated instances of this workspace. The deletions are the same as
  today (worktree directory, merged branch, lifecycle record status). R8 keeps
  the mapping, instance and clones untouched. A crafted mapping can't point
  destroy outside the workspace, apart from the small TOCTOU above.
- `--force` with session resolution: `destroySessionWorktrees` takes `force`.
  Combined with a session value, that removes every dirty worktree in the
  instance and force-deletes every unmerged branch (`git branch -D`). That's
  more than today's `--force`, which acts on one named worktree. A user can
  reach it by accident: a handle-less 8-hex prefix that happens to be unique,
  or a copy-paste of the wrong session. It isn't a privilege issue, but it's
  the widest destructive action in the design. Mitigation: pick an explicit
  behaviour and document it. Either refuse `--force` with a session value
  (exit 2, usage error) or require the full session id when `--force` is
  given, and list what will be forced before acting.

### Supply Chain or Dependency Trust

**Applies:** No

The design adds two internal packages (`internal/dirlock`,
`internal/testhook`) and no module. It uses `syscall.Flock` from the standard
library, as the existing Codex trust lock does. The PRD's acceptance criteria
check that `go.mod` gains nothing. The atomic-exchange alternative, which would
use `golang.org/x/sys`, a module niwa already requires, is only a possible
later hardening. The fetch and clone paths and their sources are unchanged.

### Data Exposure

**Applies:** Yes (minimal)

- Lock files are empty, and their names are fixed (`.niwa.lock`,
  `<overlay>.lock`, `global.lock`). They don't reveal session ids or paths
  beyond the directory name, which is already visible.
- Staging wrappers hold a copy of the fetched config and, during carry-over,
  of `instance.json`, `dispatch-briefs/` and `sessions/`. The 0700 wrapper
  keeps other users out while it exists. The carried `sessions/` keeps 0700
  and 0600 inside. `.prev` holds the old live directory with its original
  modes for milliseconds. So there's no wider exposure than the live
  directory has today.
- Error messages: the timeout error names the directory and lock path. The
  resolver prints session ids, handles and instance paths in ambiguity and
  refusal lines. All of this goes to the invoking user's own stderr, and none
  of it is a credential. Session ids let the same user resume a session, but
  anyone who can read that stderr can already read the mapping store. The only
  output concern is the control-character one under External Artifact Handling.
- Test hooks carry no data.

### Test-only Seam

**Applies:** Yes (low risk as designed, with two conditions)

`internal/testhook` is compiled into release binaries because production code
calls `Hit`. `Hit` is one atomic load that returns nil unless some code has
called `Set`. `Set` is exported but has no trigger reachable from outside:
no environment variable, flag, file or IPC. Only Go code inside the process
can call it, and the guard test fails the build if a non-`_test.go` file does.
That's stronger than the existing `testfault` package, which release binaries
honor through `NIWA_TEST_FAULT`. The design's choice not to extend `testfault`
with pause and exit specs is right on security grounds too: those would let an
environment variable hang or kill a production niwa at a chosen point, for
example inside a SessionStart hook.

Conditions:

1. The re-exec kill tests need a "helper-only marker" (usually an environment
   variable) that makes the child block at a hook point. The check for that
   marker, and the `Set` call it triggers, must live only in `_test.go` files
   (`TestMain` or a `TestHelperProcess` function), so the marker does nothing
   in a release binary. The guard test should also reject any production-file
   reference to the marker's name.
2. The guard test should find callers of `testhook.Set` by parsing Go files
   (for example `go/parser` over every non-test file in the module), not by
   string grep, so an aliased import can't slip past it.

With both conditions met, a release binary can't trigger the seam.

### Denial of Service

**Applies:** Yes (low)

- Other users: they can't open a 0600 lock file, so they can't hold the lock.
  The exception is an attacker who can create `D.lock` before niwa does, which
  requires write access to `D`'s parent. For the workspace root that access
  already allows much worse. For the XDG directories it's the user's private
  tree. The `O_NOFOLLOW`, regular-file and owner check recommended above also
  stops niwa using a lock file another user planted.
- Same-user holder: the wait is bounded at 30 seconds, and then the command
  fails with an error that names the directory. A process that re-acquires the
  lock in a loop can keep niwa failing, but a same-user process can just kill
  niwa. The bound turns a hang into an error, which is what the PRD requires.
- Shared-lock starvation of writers: flock isn't fair. An exclusive waiter
  polling `LOCK_EX|LOCK_NB` every 20 ms can lose indefinitely to overlapping
  shared holders. Each shared hold is a directory scan or a marker read
  (milliseconds), so this needs a steady stream of concurrent readers, such as
  scripts looping `niwa list` or several `niwa watch` processes. Under that
  load a mapping write or swap could hit the 30-second bound. Mitigation,
  optional: when an exclusive acquisition is contended, write a "writer
  waiting" sentinel next to the lock that new shared acquirers back off on for
  one poll interval. Or accept it and name it in the Known Limitations.
- Blocking in hooks and completion: `config.Discover`'s recovery branch takes
  the exclusive lock, so any command, including shell completion and the
  SessionStart hook, can wait up to 30 seconds when the crash-recovery
  condition holds and another holder is active. That only happens after a
  crash, while the lock is held, and holds last milliseconds. Acceptable.
  Consider a shorter bound for the Discover branch.
- Filling staging: each live refresh leaves one wrapper, and the next
  exclusive holder sweeps dead ones. A same-user process can fill the disk in
  plenty of other ways, and other users can't create wrappers in these
  directories. Not a real concern.

## Recommended Outcome

OPTION 2 - Document considerations.

The design is sound: its ordering puts every check-then-act step under a lock,
its path checks come from the PRD, and it keeps the test seam out of reach at
runtime. None of the findings needs a different architecture. Several are
implementation requirements that belong in the design so the implementer
doesn't have to rediscover them: strict recognition of staging wrappers and
`.prev`, no `O_CREATE` in the liveness check, `O_NOFOLLOW` on the lock file,
uid checks in `config.Discover`, the resolved path handed to `DestroySession`,
and a decision on `--force`. Decision 1's recovery paragraph should link to
the section below.

Draft section:

---

## Security Considerations

niwa runs as the invoking user with no elevated privilege. Nothing here adds
an operation the user couldn't already perform, so the questions are whether
crafted or corrupt local files can make niwa act outside the workspace, and
whether the new files widen what other users can see or block.

**Destroy input and mapping contents.** The value passed to
`niwa worktree destroy` is only compared as a string against fields of the
loaded mappings and against worktree ids of the current instance. It never
becomes a path until a lifecycle read that first requires eight lowercase hex
characters. Mapping files can be written by any process running as the user,
so their fields are untrusted. The resolver keeps only mappings whose key is a
valid UUID. It accepts a recorded `instance_path` only if the path is lexically
a direct child of the workspace root other than `.niwa`, exists, and, after
symlinks are resolved, is one of the directories `EnumerateInstances` returns.
The path handed to `DestroySession` is that enumerated directory, not the
recorded string, and it's re-checked with `Lstat` right before each destroy,
so swapping the directory for a symlink after the check can't redirect
teardown. Every mapping-derived value printed in outcome, refusal or ambiguity
lines is quoted with `%q`, so a crafted handle or path can't inject terminal
control sequences.

**Scope of teardown by session.** Teardown by session applies the guards
`niwa worktree destroy` already has to each active lifecycle record of one
instance. It never removes the mapping, the instance or its clones. Before any
git call it checks that each record's worktree path lies under the instance
and that its branch name doesn't start with `-`. `--force` is refused with a
session id or handle (usage error, exit 2). Forcing is still available one
worktree at a time through the worktree id or `--by-path`, so a mistyped or
prefix-matched id can't discard a whole instance's uncommitted work and
unmerged branches.

**Lock, staging and previous-snapshot paths.** The lock file `<dir>.lock` is
opened with `O_NOFOLLOW`, must be a regular file, and is created 0600, so
other users can't open it to hold the lock, and a planted symlink can't make
niwa create or lock a file elsewhere. Each refresh stages in an
`os.MkdirTemp` wrapper (0700). Inside it, niwa creates the `lock` file with
`O_CREAT|O_EXCL` and the `snap/` directory with 0755, so the live directory
keeps its current mode after the swap. Recovery treats an entry as a dead
wrapper only when it is a real directory (checked with `Lstat`) that contains
both `lock` and `snap/`, and whose `lock` it can take without waiting. The
liveness check opens `lock` without `O_CREATE`, so it never deletes a wrapper
whose owner hasn't finished setting it up, or a directory that merely matches
the name pattern. That matters for overlays: their clones are named
`<org>-<repo>` under one per-user directory, so a repository named, say,
`cfg.next-x` or `cfg.prev` has a name that matches another overlay's suffix.
Recovery renames `<dir>.prev` back only when it is a real directory owned by
the current user and holds the provenance marker niwa writes. Removals use
`safeRemoveAll`, which deletes a top-level symlink without following it. All
of recovery's check-then-act steps run with the exclusive lock held.

**Recovery during configuration discovery.** `config.Discover` repairs a
moved-aside `.niwa` only when the candidate directory, its `.niwa.lock` and
its `.niwa.prev` belong to the current user. A walk up through a shared
directory therefore never renames another user's files, and never loads
another user's directory as workspace configuration because of this branch.

**File modes and data exposure.** Lock files are empty. The mapping store
keeps 0700 for `sessions/` and 0600 for its files, both in the writer (which
now creates only the `sessions/` leaf, still 0700) and through the snapshot
carry-over, which already restores the source mode. The atomic `SaveState`
writes a uniquely named temp file in `.niwa/`, sets it to 0644 to match
today's `instance.json`, and renames it into place. Error messages name
directories, lock paths, session ids and instance paths only, all on the
invoking user's own stderr.

**Test-only seam.** `internal/testhook` is compiled into release binaries,
but nothing outside Go code can reach it: there is no environment variable,
flag or file trigger. With no hook set, `Hit` returns nil, and a guard test
parses every non-test Go file and fails if any of them calls `Set`. The
re-exec helper that the crash tests kill is recognized only by code in
`_test.go` files, so its marker does nothing in a release binary. Pause and
exit faults were kept out of `testfault` on purpose, because that package
honors an environment variable in release builds.

**Denial of service.** No lock wait is unbounded. A command that can't get the
lock within 30 seconds fails with an error naming the directory. Other users
can't hold the lock because they can't open a 0600 file. A process running as
the same user can keep niwa failing by holding the lock, but such a process
could just as well kill niwa. flock doesn't queue waiters fairly, so a steady
stream of overlapping shared readers can in principle make a writer hit the
bound. Shared holds last one directory scan, so this needs sustained
concurrent `niwa list` or watch traffic, and it's accepted as a known
limitation. Non-unix platforms get no ordering, as stated at the fallback.

**Dependencies.** No module is added. The lock uses `syscall.Flock` from the
standard library, as niwa's existing Codex trust lock does.

---

## Summary

The design adds no privilege and no dependency, and it keeps the test seam out
of reach at runtime. Teardown's path checks already stop a crafted mapping from
sending destroy outside the workspace. The real gaps are all in the recovery
and sweep logic: a planted or name-colliding `<dir>.prev` or `<dir>.next-*`
(overlay clones are `<org>-<repo>`, so a repo named `cfg.prev` or
`cfg.next-x` matches another overlay's pattern) can be renamed into place or
deleted; the lock file is opened without `O_NOFOLLOW`; and
`config.Discover`'s new recovery branch writes in ancestor directories without
checking who owns them. Each is fixed by stricter `Lstat`, marker, uid and
open-flag rules. Add those and a decision on `--force` with a session id to a
Security Considerations section (Option 2), with no architectural change.
