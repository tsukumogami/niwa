# Security Re-Review (Phase 6 juror, second pass)

Design under review: `docs/designs/DESIGN-session-store-teardown.md` (status Planned, revised
after a FAIL).
Upstream: `docs/prds/PRD-session-store-teardown.md` (also revised).
Prior review: `wip/research/design_session-store-teardown_phase6_security-review.md`
(17 findings, 10 overclaims, FAIL).
Code baseline: the worktree at `public/niwa/.claude/worktrees/session-store-teardown`,
Go 1.25.3, `go build ./...` clean.

Every assertion the design makes about today's code was checked against today's code. Where
the design proposes a mechanism, I checked whether the mechanism it names as "existing"
actually exists.

## Verdict: FAIL

The revision is a large, genuine improvement. Eight of the seventeen prior findings are
properly closed, several with better reasoning than I asked for (the non-unix recovery
skip, the `preserve*` symlink refusal, the marker-drift test, the `instance.json` guards).

It fails on three things. First, the central new defense — "every auxiliary path is
identified by content, never by name alone" — rests on a marker test that provably cannot
tell a live overlay clone from a previous snapshot, because **an overlay clone is itself a
refreshed config directory and carries that exact marker**. The design says the opposite in
two places. Second, `<dir>.prev` is still a fixed name and is the swap's own rename
destination, so the "random suffix, retry once, a squatter cannot wedge us" defense does
not apply to the one path that matters most. Third — and this is the same shape as the
finding that failed the first pass — the design again names a mitigation mechanism that
does not exist: "the sanitizer the dispatch path already uses, which strips control
characters". The dispatch path does not strip. It refuses, and
`internal/cli/dispatch_reentry.go:102-106` says so in a comment written specifically to
explain why stripping was rejected.

---

## Previous findings

| # | Prior finding | Verdict |
|---|---|---|
| 1 | Git-argument mitigations exist nowhere | **CLOSED** |
| 2 | Teardown reach + fail-open dirty guard | **CLOSED** |
| 3 | Overlay sibling-path collision only 1/3 mitigated | **OPEN** (worse: the new defense is provably wrong) |
| 4 | "Lstat-then-use closes the race" is false | **CLOSED** |
| 5 | Lock offered as TOCTOU answer; planted name wedges recovery | **PARTIAL** |
| 6 | uid check breaks non-unix build; PRD criterion unmet | **CLOSED** |
| 7 | Non-unix no-op runs unsynchronized recovery | **CLOSED** |
| 8 | `%q` claim contradicted | **OPEN** (reworded to a different non-existent mechanism) |
| 9 | `preserve*` follow symlinks; `HoldsSnapshot` presence-only | **CLOSED** |
| 10 | Lock-file open underspecified | **PARTIAL** |
| 11 | Starvation thinner than stated | **CLOSED** |
| 12 | `config.Discover` blast radius | **PARTIAL** |
| 13 | "One owner for the layout" scoped narrower than the claim | **OPEN** |
| 14 | Constant duplication drift | **CLOSED** |
| 15 | "keys valid UUID"; R6 is not a security control | **PARTIAL** |
| 16 | R7 symlink-resolution asymmetry | **PARTIAL** |
| 17 | New sibling dirs vs the root scanners | **OPEN** |

Closed: 8. Not fully closed: 9 (4 OPEN, 5 PARTIAL).

### 1 — CLOSED

The design now specifies both checks in the body, not only in the security section, and
accepts the structural consequence. Decision 2 (design:365-388) states them; Components
(design:696-703) restates them at `internal/worktree/worktree.go`; Key Interfaces
(design:780) declares `validateSessionRecord(instanceRoot string, r *SessionRecord) error`;
Consequences (design:1084-1085) records "`internal/worktree` stops being an unchanged leaf".
The `--` end-of-options marker is specified (design:383).

Verified the checks genuinely do not exist today. `DestroySession`
(`internal/worktree/worktree.go:268-353`) re-reads the record itself
(`ReadSessionLifecycleState` at `:274`) and calls
`gitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "remove", "--force", worktreePath)`
(`:330`) and
`gitInvoker.CommandContext(ctx, "-C", repoPath, "branch", branchArg, branchName)` (`:344`).
There is no `--` anywhere in `internal/worktree/`, no `filepath.Rel`, and no
`.niwa/worktrees` prefix test on a record's path. The only validation in the package is the
session-id regex `^[0-9a-f]{8}$` (`internal/worktree/session_lifecycle.go:17,28,75,97`);
`WorktreePath` and `BranchName` are unvalidated on both write and read
(`session_lifecycle.go:52`). Placing the checks at the git calls rather than in
`internal/cli` is the right call for the reason the design gives.

Residual, filed as new finding L11: there is a third copy of this argv
(`workspace.DefaultDestroySession`, `internal/workspace/bootstrap.go:280-311`) that the
design does not cover.

### 2 — CLOSED

Design:383-384 and design:948-951: "a record whose worktree directory is missing is refused
rather than reported clean". Verified the fail-open today at
`internal/worktree/worktree.go:83-95`:

```go
if worktreePath == "" { return false, nil }
if _, err := os.Stat(worktreePath); os.IsNotExist(err) { return false, nil }
```

The design's description of the current behavior is accurate. Residual, filed as L13: the
`os.Stat` error is tested only against `os.IsNotExist`, so a permission or `ELOOP` error
still falls through to the git call; the design only changes the ENOENT arm.

### 3 — OPEN, and the new defense is provably wrong

This is the first of three blockers. See **New finding H1**. The revision replaced a
`workspace.toml` test with a provenance-marker test and asserts (design:220-224, 977-978)
that the marker separates niwa's own rotation from a look-alike name. It does not: overlay
clones are maintained by the same refresh pipeline and carry `.niwa-snapshot.toml`.

### 4 — CLOSED

The overclaim is gone and replaced with an accurate statement. Design:386-388: "This
narrows rather than closes the window for the paths that still resolve by string, and the
design says so rather than claiming a closure." Design:931-935 repeats it; Consequences
(design:1080-1081) lists it as a negative; Mitigations (design:1106-1107) explains why the
record fields are validated at the point of use as a result. The `os.Root` remedy is adopted
for the record reads (design:702, "Record reads resolve through a root opened on the
instance directory"). Note `os.Root`/`os.OpenRoot` is used nowhere in the repo today, so
this is new code — but `go.mod:3` declares `go 1.25.3`, so it is available, and no module is
added.

### 5 — PARTIAL

The predictable-name half is closed: trash and stray now carry random suffixes and a
rename that fails on an existing destination "retries once with a fresh name and then
returns an error naming the path" (design:238-239, 740-742).

Two halves remain open.

- `<dir>.prev` is still a fixed name (design:158, and `PrevPath(dir string) string` at
  design:752). It is the destination of the swap's own first rename, so "retry with a fresh
  name" is not available for it. See **New finding H2**.
- "All of recovery's check-then-act steps run with the exclusive lock held" (design:984) is
  still offered as the ordering answer, and a lock still excludes only lock-takers. The
  design concedes that older binaries get no guarantee (design:151-152) but does not follow
  that through to what recovery will then *do* to an older binary's in-flight swap. See
  **New finding H3**.

### 6 — CLOSED

Design:648-651: "Ownership checks live in the unix file too, since the type they need does
not exist on other platforms, and `recover.go` is split the same way." That fixes the build
problem.

The PRD escalation also landed. PRD:517-523 now reads: "`GOOS=windows go build` succeeds for
every package this change touches or adds ... `GOOS=windows go build ./...` is not required
and does not pass today: `internal/cli/sessionattach` and `internal/promptcapture` already
fail to build for Windows, and this change neither fixes nor worsens that." I re-ran it and
confirm the failure is exactly those two packages (8 + 9 errors, `syscall.Flock`,
`syscall.Stat_t`, `syscall.SIGTSTP`, etc.). The criterion is now satisfiable.

Worth noting for the implementer, not as a finding: no production code performs an
ownership check anywhere today. The sole `syscall.Stat_t` read
(`internal/cli/sessionattach/attach.go:201`) only fills a `%d` in a diagnostic message and
enforces nothing. The `uid == Geteuid()` comparison recovery needs is entirely new code with
no precedent to copy, and there is no `GOOS=windows` job in CI that would catch a
build-tag mistake in it.

### 7 — CLOSED, well

Design:241-247 makes it an explicit decision with a rationale, not a dismissal: "Recovery
does not run on non-unix. There the lock is a no-op, so running a multi-step rename dance
with no exclusion would be worse than today ... On those platforms the exclusive entry point
runs its callback without recovery, an interrupted refresh needs manual cleanup, and the
fallback file says so. This is a different trade from the existing Codex trust lock's no-op,
which guards a single atomic replacement rather than a rename sequence." That is precisely
the distinction I said the precedent did not carry. Key Interfaces confirms it
(design:758, `Recover ... no-op on non-unix`), as does Components (design:646-648).

### 8 — OPEN: the mechanism was renamed, not found

Second blocker. See **New finding H4**. The `%q` claim is gone, but its replacement —
design:390-392 and design:937-941, "the sanitizer the dispatch path already uses, which
strips control characters" — names something that does not exist. This is the same failure
mode that failed the first pass: the security section is the sole source for a mitigation,
and the mechanism it points at is not there.

### 9 — CLOSED

Design:660-663 switches all three `preserve*` helpers to `Lstat` and makes them refuse a
symlinked source, with the exact reasoning I gave; design:991-998 repeats it. Design:753 and
design:716-717 make `HoldsSnapshot` require the marker as a regular file.

Verified the current behavior the design describes. `preserveInstanceState` uses
`os.ReadFile` (`snapshotwriter.go:485`), `preserveDispatchBriefs` uses `os.Stat`
(`:516`), `preserveSessionMappings` uses `os.Stat` (`:556`) — all three follow symlinks.
`provenanceMarkerExists` (`snapshotwriter.go:113-116`) discards the `FileInfo` entirely, so
a directory or a symlink at `.niwa-snapshot.toml` satisfies it today. The mode claim is also
accurate: `preserveSessionMappings` re-tightens with `os.Chmod(dst, info.Mode().Perm())` at
`:578`.

### 10 — PARTIAL

Closed half: design:640-643 now names `O_NOFOLLOW|O_NONBLOCK` plus an `fstat` for a regular
file, and design:958-962 repeats it. The assertion that this pair already exists in the
repository is **true**: `internal/workspace/contextprobe_unix.go:11` is
`const nofollowOpenFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK`, with
`contextprobe_other.go` giving `0`, and both existing callers
(`contextprobe.go:140` and `:189`) do `f.Stat()` and reject a non-regular file. The design
describes a real pattern accurately. (Note the existing Codex trust lock does *not* use it:
`codex_trust_lock_unix.go:31` is `os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)` with
no `O_NOFOLLOW`. The new lock is an improvement over the one it says it follows.)

Open halves, filed as **M5** and **M6**: the mode and owner of a *pre-existing* lock file
are never checked, and the Mitigations' "harmless if deleted between commands" contradicts
the design's own reason for never unlinking.

### 11 — CLOSED

Design:262-270 now states the thing I said was understated: "Shared holds are short but not
all equal: the mapping scan is one directory read, while the post-swap reload spans a config
parse and staging creation spans a mkdir and a lock." It names the watch traffic and its
cost, points at the follow-up, adds a test for an exclusive waiter behind repeated shared
holds, and lists starvation in Consequences (design:1075-1076). No fairness mechanism was
added, which is an explicit, argued acceptance rather than a gap. Acceptable residual risk.

### 12 — PARTIAL

Closed: design:249-254 bounds the repair to one candidate per command ("it repairs at most
one candidate per command, so the bound applies once rather than once per directory the walk
passes through"), and names the nesting hazard. Consequences lists the new capability
(design:1073-1074).

Open: two things. The DoS paragraph (design:1026-1037) still says "No lock wait is
unbounded: a command that can't get the lock within 30 seconds fails" without noting that
the branch is now reachable from shell completion (`internal/cli/completion.go:41`), the
SessionStart hook (`internal/cli/instance_from_hook.go:429`) and `niwa watch`
(`internal/cli/watch.go:1019`) — so a TAB press can block for up to the bound. Sixteen
`config.Discover` call sites, confirmed. And the nested-acquisition detector keys on lock
*paths held*, so it does not fire for a *different* directory taken inside a callback; see
**M7**.

For the record, `Discover` (`internal/config/discover.go:17-39`) walks a single ancestor
chain with one `os.Stat` per level, so "N x 30s" from the first review was wrong once the
one-repair bound is in place. The revision's bound is correct.

### 13 — OPEN

Consequences still says "The swap's file names have one owner" (design:1057-1058), and
Decision 1 says `internal/configdir` "owns every name the swap uses" (design:155-157).
`internal/plugin/installer.go:135-176` (`stageAndRename`) runs the same `.next`/`.prev`
dance with plain `os.RemoveAll(nextPath)` / `os.RemoveAll(prevPath)` (`:141-142`) and
`os.Stat(installPath)` (`:154`) — no `Lstat`, no `safeRemoveAll`, not routed through
`configdir`. Low severity, but the claim should be scoped to workspace config directories.

### 14 — CLOSED

Design:649-652: "The package copies the two marker filenames it tests for rather than
importing them (it sits below the packages that define them), with a test that fails if
either copy drifts, because a wrong marker name makes recovery pick the wrong side." Exactly
the remedy asked for, with the correct failure mode named.

### 15 — PARTIAL

First half closed: design:344-346 and design:923-925 now say "whose `session_id` field is a
valid UUID", and Components (design:670-672) explains why — "since the store returns bodies
rather than filenames". Verified: `ListSessionMappings`
(`internal/workspace/session_map.go:199-229`) returns `[]SessionMapping` decoded from file
bodies and never exposes a filename; the filename is used only as a `.json` suffix filter
(`:211`).

Second half open (Low): R6's newest-mapping rule is decided entirely by the untrusted
`created` field, and the "Scope of teardown by session" paragraph (design:943-956) still
lists it among the guards without saying it is a usability rail rather than a security
control. See **L14**.

### 16 — PARTIAL

Design:930-931 still says the recorded path is accepted only if, "after symlinks are
resolved, [it] is one of the directories `EnumerateInstances` returns", and design:356-357
adds "(lexically, and against the root's resolved path)". The parenthetical fixes the
*location* rule but not the *membership* comparison. Verified `EnumerateInstances`
(`internal/workspace/state.go:353-374`) returns `filepath.Join(workspaceRoot, entry.Name())`
at `:367` — a lexical join of whatever root the caller passed, never `EvalSymlinks`'d, never
`Abs`'d. So a resolved recorded path is still being compared against an unresolved set, and
the mismatch reappears wherever the root itself sits under a symlink (macOS `/tmp` ->
`/private/tmp`). The fix is one sentence: resolve the enumerated set too, or compare both
sides unresolved.

One incidental improvement worth recording: `EnumerateInstances` filters entry names through
`ValidName` (`state.go:559-570`), which rejects control characters and Cf/Zl/Zp, so an
instance *directory name* cannot itself carry a terminal escape into R9's output. That is
not true of the branch name or the handle.

### 17 — OPEN

The Mitigations say only that "The lock files are regular files, ignored by instance
enumeration" (design:1094-1095). Nothing states the invariant for `<root>/.niwa.next-*`,
`<root>/.niwa.trash-*` or the deliberately kept `<root>/.niwa.stray-*`, all of which land as
direct children of the workspace root where `EnumerateInstances` and both reaper sweeps scan.
They are safe today by one path segment — enumeration needs `<child>/.niwa/instance.json`
and the carry-over puts the file at `snap/instance.json` — and this change reshapes exactly
that layout. See **L15**.

---

## New findings

### H1 (High) — The provenance marker cannot distinguish a live overlay clone from a previous snapshot, so recovery will delete or promote one

**Design quote** (design:976-978, and the same claim at design:220-224):

> A clone holds a `workspace.toml`, not niwa's marker, so it fails all three tests and is
> left in place and reported.

> Only a refreshed directory ever has a previous snapshot, and a refreshed directory always
> carries the marker, so the marker is what separates niwa's own rotation from a name that
> merely looks like it.

**Why it matters.** Both sentences are false, and the second one is self-refuting once you
notice that an overlay clone *is* a refreshed directory. Overlays go through the same
pipeline: `EnsureOverlaySnapshot` (`internal/workspace/overlaysync.go:37`) tests
`provenanceMarkerExists(dir) || dotGitExists(dir)` to decide the overlay already exists, and
both the refresh path (`overlaysync.go:40` -> `EnsureConfigSnapshotWithStatus`) and the
fresh path (`overlaysync.go:51` -> `MaterializeFromSource`) end in `materializeAndSwap`,
which writes the marker unconditionally at `snapshotwriter.go:426` (`WriteProvenance(staging,
prov)`). So every overlay clone niwa maintains carries `.niwa-snapshot.toml` alongside its
`workspace.toml`.

Now take the collision the design itself raises. `config.OverlayDir`
(`internal/config/overlay.go:325-351`) builds `dirName = org + "-" + repo` (`:340`) with no
charset validation, so overlay `acme/tools.prev` lands at `overlays/acme-tools.prev` —
byte-identical to `PrevPath("overlays/acme-tools")`. With both overlays present:

- **Recovery rule 2 deletes a live overlay.** `acme-tools` holds the marker and
  `acme-tools.prev` is "a real directory, owned by the current user, holding the provenance
  marker as a regular file" — it passes the evidence test. Rule 2 renames it to a trash name
  and deletes it after the lock is released. A legitimate, unrelated overlay clone is
  destroyed.
- **Recovery rules 1 and 3 promote it.** If `acme-tools` is absent or lacks a marker,
  `acme-tools.prev` is renamed *into* `acme-tools` and becomes that overlay's configuration.
- **The swap cannot run at all.** `rename(acme-tools, acme-tools.prev)` fails because the
  destination exists (see H2).

This is not purely adversarial: the collision is reachable from a config source the workspace
points at, because `apply.go:1094` derives the overlay URL via
`config.DeriveOverlayURL(opts.configSourceURL)` rather than from something the user typed.
And today's code already has the deletion half (`snapshot.go:53`, preflight
`safeRemoveAll(prev)`), so the design does not introduce it — but it claims to have closed
it, and has not.

**Fix.** The marker cannot carry this weight; stop asking it to. Namespace the auxiliary
paths out of the sibling space entirely — put `prev`, `next`, `trash` and `stray` inside one
`<dir>.niwa-swap-<random>/` working directory whose name no overlay can produce (the `-`
join means an overlay can produce any `<a>-<b>`, so use a character `parseOrgRepo` cannot
emit, or a hidden prefix under the overlays root). Failing that, add a second evidence test
recovery can actually rely on: a swap journal (see H3), which is the same mechanism H3 needs.

### H2 (High) — `<dir>.prev` is a fixed name and is the swap's own rename destination, so a squatter wedges every refresh with no retry available

**Design quote** (design:979-981):

> The destinations niwa creates carry random suffixes, and since Go's rename refuses an
> existing destination, a planted entry at a guessed path fails the rename rather than
> redirecting it; that failure is retried once with a fresh name and then reported, so a name
> squatter cannot wedge mapping writes indefinitely.

**Why it matters.** The claim is true for trash and stray, whose names the design randomized
in this revision. It is not true for `.prev`, which design:158 and `PrevPath(dir string)
string` (design:752) keep fixed, and which is the destination of
`SwapSnapshotAtomic`'s first rename (`internal/workspace/snapshot.go:69`,
`os.Rename(target, prev)`). There is no fresh name to retry with: the rename target is the
name.

The design also removes the one thing that currently unsticks this. Today `SwapSnapshotAtomic`
unconditionally clears the path first (`snapshot.go:53`, `safeRemoveAll(prev)`), which
deletes a regular file, a FIFO or a directory there — `safeRemoveAll`
(`snapshot.go:108-121`) has no type check, so anything non-symlink falls to
`os.RemoveAll`. Design:664-666 removes that preflight ("loses its unconditional preflight
delete, recovery now owns `.prev`"), and recovery's policy is to leave anything that fails
its evidence test in place (design:739-740). So after this change:

`touch <root>/.niwa.prev` — one same-user command, or a stale artifact, or the legitimate
overlay from H1 — permanently fails every refresh of that workspace, with no self-repair and
no code path that clears it. That is a strictly worse failure mode than today's, introduced
by a change whose stated goal is self-healing.

**Fix.** Randomize the moved-aside name the same way trash and stray were randomized:
rename `D` to `D.prev-<random>` and have recovery discover it by scanning siblings for the
evidence rather than by a fixed path. That removes both the squat-wedge and the H1 overlay
collision on `.prev` in one move. If the fixed name is kept for compatibility with older
binaries, then recovery must move a non-evidence `.prev` aside to a stray name instead of
leaving it, and that behavior must be specified.

### H3 (High) — Recovery has no swap-intent record, so it cannot tell a crash from a concurrent swap or a planted directory

**Design quote** (design:984):

> All of recovery's check-then-act steps run with the exclusive lock held.

and design:151-152:

> concurrently running niwa versions older than this change get no guarantee.

**Why it matters.** Recovery decides "a refresh was killed between its renames" purely from
the *current state of two paths*. It has no evidence that any refresh ever started, that it
was this binary's, or that it is finished. Three consequences the design does not address:

1. **Recovery corrupts an older binary's in-flight swap.** This is the ordinary condition
   during an upgrade: a long-running `niwa watch`, or a backgrounded `niwa dispatch
   --detach`, started before the new binary landed. The old binary renames `D` to `D.prev`
   (`snapshot.go:69`) and is about to rename staging into place (`:77`). A new binary's
   `Mutate` — which is now on the path of every mapping write, every watch state write and
   every `instance.json` write — runs `Recover`, sees `D` missing and `D.prev` carrying the
   marker, applies rule 1 and renames `D.prev` back to `D`. The old binary's second rename
   then fails, and its rollback (`snapshot.go:78-84`, `os.Rename(prev, target)`) fails too
   because `prev` is gone and `target` now exists. The result is a stranded snapshot and an
   error the old binary reports as a swap failure. The design's "older binaries get no
   guarantee" reads as "they are on their own"; in fact the new code actively reaches into
   their swap.

2. **Recovery is a directory-promotion primitive available to any same-user process.** The
   design adopts a same-user threat model explicitly for file contents ("Mapping files can be
   written by any process running as the user, so their fields are untrusted", design:920-922)
   and then defends the auxiliary paths with content evidence that the same actor can forge
   trivially: a directory containing a `.niwa-snapshot.toml` and a `workspace.toml` satisfies
   every test rules 1 and 3 apply. Rule 3 then moves the real `.niwa` to a stray name and
   promotes the planted one. Since `config.Discover` now runs `RecoverMovedAside`
   (design:693-695), the promotion fires from shell completion, the SessionStart hook and
   `niwa watch` — code paths that previously only read. A promoted `workspace.toml` drives
   repo clone URLs and the `scripts/setup/` a later apply runs.

   I want to be fair about the boundary here: a same-user process that can write into
   `<root>/` can already overwrite `.niwa/workspace.toml` directly, so this is not a
   privilege escalation. The problem is consistency. The design spends four paragraphs
   defending the auxiliary paths against exactly this actor and asserts the defense holds.
   Either the actor is in the threat model, in which case content evidence is not enough, or
   they are not, in which case several of the design's other defenses are decoration. The
   document has to pick one and say so.

3. **Recovery cannot be distinguished from a legitimate parallel state.** The lock answers
   only "no other *new* niwa process is inside an exclusive section right now". It says
   nothing about whether the state it is looking at was produced by a crash.

**Fix.** Write a swap-intent journal under the lock before the first rename: a small file
beside the directory (or inside the staging directory) naming the moved-aside path, the
staging path, a random token and the pid, removed under the lock after the second rename.
Recovery then acts **only** on paths a journal names, and treats a journal whose staging
lock is still held as live rather than dead. That makes rules 1, 2 and 3 evidence-based in a
way a look-alike directory cannot forge, closes the older-binary interference (an old
binary's swap leaves no journal, so recovery leaves it alone), and gives H1 the
discriminator the marker cannot provide. Then state the threat model boundary explicitly in
Security Considerations: same-user write access to the workspace root is outside it, or it
is not.

### H4 (High) — "The sanitizer the dispatch path already uses" does not exist; the dispatch path deliberately refuses instead of stripping

**Design quote** (design:390-392, restated at design:937-941 and design:886-887):

> Every value interpolated into an outcome, refusal or ambiguity line has control characters
> stripped by the sanitizer the dispatch path already uses, rather than being quoted, so the
> literal shapes R9 pins are preserved.

**Why it matters.** This is the same defect that failed the first pass — a mitigation whose
only home is the security section, pointing at a mechanism that is not there — with a
different mechanism named. Three separate problems:

1. **Nothing on the dispatch path strips.** The two functions in
   `internal/cli/dispatch_reentry.go` are `printableToken` (`:115-122`), a `bool` predicate
   that strips nothing, and `shellToken` (`:87-92`), a POSIX shell quoter that removes
   nothing. The file's own comment at `:102-106` states the decision the design contradicts:

   > The answer is to refuse rather than to strip. The repository has a stripper for this
   > threat already, but stripping produces a command that still looks runnable and no longer
   > does what it says; refusing produces no command, which is a state every caller here
   > already handles.

2. **The one stripper with the right coverage is structurally out of reach.** The closest
   match is `stripControlChars` (`internal/cli/session_from_hook_cmd.go:331-345`), which
   drops C0, DEL and C1. It is unexported, it lives in `internal/cli`, and it is on the
   WorktreeCreate hook path, not the dispatch path. The line that most needs it — the
   kept-branch warning, `"branch %s was not deleted ... git -C %s branch -D %s"` — is built
   *inside* `internal/worktree` at `worktree.go:345-348` and printed raw by
   `fmt.Fprintln(cmd.ErrOrStderr(), "warning:", state.BranchWarning)` at
   `session_lifecycle_cmd.go:590`. `internal/worktree` is a strict leaf
   (`go list -deps` shows only `internal/gitexclude`) and `internal/cli` imports it, so it
   cannot import back. This is finding 1 all over again: the mitigation is named in the
   security section and is unreachable from where the value is composed.

3. **No stripper in the repo covers the bidi case.** `unicode.IsControl` and every
   hand-rolled stripper here match Cc only. U+202E (RTL override) and U+200B are Cf and pass
   all of them. That matters specifically for a paste-ready `git -C <repo> branch -D <name>`
   line, which teardown by session now emits once per worktree instead of once per command.
   `SanitizeDisplayString` (`internal/tui/sanitize.go:17`) is the only exported candidate and
   is the weakest — `docs/designs/current/DESIGN-dispatch-paste-prompt.md:186` already
   rejected it for this threat.

The design does add a branch-shape check (design:383, "requires the branch name to be a
plausible ref that does not begin with `-`"), which, if strict, would neutralize most of
this at the source. But "a plausible ref" is not a specification, and it is the only barrier
between a completely unvalidated JSON field
(`internal/worktree/session_lifecycle.go:52`, read by a bare `json.Unmarshal` at `:105-108`)
and two git argv positions plus a copy-pasteable command line.

**Fix.** Three concrete things. (a) Name the exact accepted branch pattern — the
`git check-ref-format` rules, or an explicit allowlist regex — instead of "a plausible ref",
and state that it rejects every byte below 0x20, DEL, space, `~^:?*[\`, `..`, a leading `-`,
and a trailing `.lock`. (b) Replace "the sanitizer the dispatch path already uses" with the
mechanism that will actually be built: export a stripper into a package
`internal/worktree` may import (a new leaf, or `internal/tui` after strengthening it),
covering C0, C1, DEL, U+2028/U+2029 and the Cf bidi/zero-width block, and say which package
it lands in. (c) Say that `watch.IsSafeHandle` is applied to the mapping's `handle` field
(see M9) so the ambiguity line has nothing to inject with.

### M5 (Medium) — A pre-existing lock file's mode and owner are never checked

**Design quote** (design:958-962):

> it is created 0600, so no other unprivileged user can open it to hold the lock.

**Why it matters.** `O_CREAT` does not change the mode or owner of a file that already
exists. The guarantee therefore holds only for a lock file niwa created. The design specifies
an `fstat` for "is it a regular file" (design:641-642) but no check that the descriptor is
owned by the current user and is not group- or world-writable — even though recovery *does*
get an ownership check for the directories it acts on, so the asymmetry is within one
package.

Where it bites: the global lock is `$XDG_CONFIG_HOME/niwa/global.lock`, and `XDG_CONFIG_HOME`
is environment-controlled; and a workspace root under a group-writable parent lets another
user pre-create `<root>/.niwa.lock` as 0666 and then hold `LOCK_EX` to fail every niwa
command on that workspace at the 30-second bound.

**Fix.** After the open, `fstat` and require a regular file, `st.Uid == os.Geteuid()`, and
`mode & 0o022 == 0`; refuse with a message naming the path otherwise. Put it in
`configdir_unix.go` next to the ownership helpers the recovery rules already need there.

### M6 (Medium) — "Harmless if deleted" contradicts the never-unlink rationale, and nothing re-validates the inode after `flock`

**Design quote** (design:1094-1096):

> The lock files are regular files, ignored by instance enumeration, and harmless if deleted
> between commands (they are recreated)

against design:165-168:

> created mode 0600 and never unlinked, because unlinking a flock file lets a waiter on the
> old inode and a new opener on a fresh one both think they hold it.

**Why it matters.** The design correctly identifies the inode hazard and then tells the
reader that deleting the file is harmless. Both cannot be true. "Between commands" is not
something niwa can observe: a `rm` (or a `git clean`, or a tidy-up script) that lands while
one process holds the lock and before another opens it puts the two on different inodes,
and both proceed believing they are serialized. Nothing errors. The ordering guarantee —
including the "no mapping write is lost" and "no delete is undone" claims in the Summary —
silently evaporates.

**Fix.** After acquiring the flock, `fstat` the descriptor and `stat` the path, and require
the same `(dev, ino)`; if they differ, close, reopen and retry within the bound. This is the
standard flock re-validation and it is a few lines. Then correct the mitigation sentence: a
deleted lock file is harmless only when no niwa command holds it, and the re-validation is
what makes the difference detectable.

### M7 (Medium) — No lock-ordering discipline across config directories, and the nested detector does not cover it

**Design quote** (design:174-177):

> the package therefore keeps a process-local set of held lock paths and fails a nested
> acquisition at once with `nested acquisition of <D>.lock`.

**Why it matters.** The detector keys on the *path already held*, so it fires only for the
same directory. A callback that takes a lock on a *different* config directory is not
detected. That path is live: `Apply` touches the workspace-root `.niwa`, an overlay clone and
the global clone in one command (`internal/workspace/apply.go:489`, `:640`, `:964`,
`overlaysync.go:40`), and `config.Discover` — now lock-taking and reachable from sixteen call
sites — can be entered from inside `ClassifyCwd`, which several CLI paths call. Two processes
that acquire root-then-overlay and overlay-then-root deadlock until both hit the 30-second
bound and both fail.

The design asserts the invariant by inspecting today's callers ("whose callbacks never call
another lock-taking function", design:256-258) but never states it as a rule or says how a
future caller is stopped. The consequence degrades to an error rather than a hang, which is
why this is Medium and not High.

**Fix.** State a total order over config directories (global < overlay < workspace root, or
lexical by lock path) and make `Acquire` refuse an out-of-order acquisition using the same
process-local set that already tracks held paths — it has the information, it just does not
use it for this. One extra comparison in the detector.

### M8 (Medium) — "Left in place and reported" is ambiguous, and one reading wedges every write on the directory

**Design quote** (design:739-740):

> Anything at one of those paths that fails its evidence test is left in place and reported.

**Why it matters.** `Recover` runs at the head of every `Mutate` (design:179-181), which is
now every mapping write, every mapping delete, every watch state write and every refreshed
`instance.json` write. If "reported" means `Recover` returns an error and `Mutate` propagates
it, then a single non-evidence entry at `<dir>.prev` — a regular file, a FIFO, a symlink, or
the legitimate overlay of H1 — fails all of those, permanently, with no self-repair. If it
means a warning that lets `fn` run, the design should say so. `Recover(dir string) (trash
[]string, err error)` (design:758) gives no clue, and the design never says which errors from
recovery are fatal to the callback.

**Fix.** Specify it: an evidence-test failure is a warning on stderr, recorded once per
directory per process, and `Mutate` runs `fn` anyway. Only a rename that fails after the
retry is fatal, and only for the operation that needed it. Add a test with a regular file at
`<dir>.prev` asserting a mapping write still succeeds.

### M9 (Medium) — The `handle` field is never validated, though the repo has the exact validator for it

**Design quote** (design:344-346):

> the locked `ListSessionMappings`, filtered to entries whose session id is a valid UUID

**Why it matters.** The design validates `session_id` and stops there. `handle` is matched
as a string (R4) and then **printed to stderr** in R9's ambiguity line
(`"<value>" matches <match>, <match>`) and potentially in refusal lines. It comes from the
same untrusted mapping file. With H4 unresolved, that is the injection path.

The repo already has the right check: `watch.IsSafeHandle`
(`internal/watch/state.go:411`) is exported precisely so "callers use [it] to validate a
captured short id ... before it becomes a CLI argument", and it allows only
`[A-Za-z0-9_-]{1,128}`. `SaveStagedRecord` (`state.go:333`) already refuses an unsafe handle.
The design's own critique of the codebase's asymmetry applies to its own resolver.

**Fix.** Apply `watch.IsSafeHandle` to the `handle` field in `loadMappingsForDestroy`
alongside the UUID filter, and say so in the design. A mapping with an unsafe handle keeps
its session-id match and loses its handle match. That closes the stderr injection path
independently of whatever sanitizer H4 settles on.

### M10 (Medium) — Trash is deleted outside the lock, where another process's rule 5 can be deleting it too

**Design quote** (design:735-737):

> Leftover trash, recognized by the sentinel file niwa writes into every trash directory it
> creates, is folded into this run's trash list.

**Why it matters.** `Mutate` deletes trash *after* releasing the lock (design:179-181,
design:565-566), which is the right call for latency. But between release and delete, another
process's `Recover` takes the lock, sees the same sentinel-bearing `<dir>.trash-<random>`,
folds it into its own list, and both processes then run `safeRemoveAll` over the same tree
concurrently. `os.RemoveAll` racing itself produces ENOENT and ENOTEMPTY errors on entries
the other just removed. The design does not say these are expected or that they are ignored,
and one of them surfacing as a command failure would be a self-inflicted R11 violation.

**Fix.** Either keep the deleting process's claim visible — rename to a
`<dir>.trash-<random>.claimed-<pid>` name under the lock before releasing, and have rule 5
skip a claimed name whose pid is still live — or state plainly that post-lock trash deletion
is best-effort and its errors are discarded, matching `snapshot.go:98`'s existing
`_ = safeRemoveAll(prev)`.

### L11 (Low) — A third copy of the destroy argv is not covered by the new validation

`workspace.DefaultDestroySession` (`internal/workspace/bootstrap.go:280-311`) parses the
lifecycle record with its own inline struct, reimplements `EffectiveBranchName`
(`:300-302`), and runs:

```go
_ = gitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "remove", "--force", st.WorktreePath).Run()
_ = gitInvoker.CommandContext(ctx, "-C", repoPath, "branch", "-D", st.BranchName).Run()
```

No containment check, no branch validation, no `--`, and `-D` rather than `-d`. The design's
reasoning for putting the checks next to the git calls (design:377-380) is exactly right and
does not reach here. Exposure is genuinely small — the only caller is
`internal/cli/init.go:222`, the `niwa init --bootstrap` rollback, acting on a record the
bootstrap itself just wrote — so this is Low. But the design should either route it through
the same validator or say in one sentence why it does not need to.

### L12 (Low) — `.stray-*` accumulates without bound and is never swept

Design:731-733 keeps a stray directory deliberately ("kept, not deleted"), and Mitigations
(design:1096-1097) says it "is kept deliberately and documented in the config-sources guide".
Nothing ever removes one. Every recovery that hits rule 3 leaks a directory into the
workspace root, and rule 3 fires whenever an in-place write lands inside a swap window —
which, before this change ships everywhere, is the ordinary condition it exists to detect.
The design should say what a user is meant to do with them and whether `niwa reap` or the
next refresh ever reports their accumulated size.

### L13 (Low) — The dirty guard's non-ENOENT stat error still falls through to git

`worktreeHasUncommittedChanges` (`internal/worktree/worktree.go:87-89`) tests the `os.Stat`
error only against `os.IsNotExist`. A permission error, or `ELOOP` on a symlink loop, leaves
`err != nil` and control proceeds to `git -C <worktreePath> status --porcelain`, whose
failure then returns an error rather than "dirty". The design closes the ENOENT arm
(design:383-384) but says nothing about the others. Fail closed on any stat error, not just
the missing one.

### L14 (Low) — R6 rests on the untrusted `created` field and is presented as a guard

The "Scope of teardown by session" paragraph (design:943-956) lists R6's newest-mapping rule
among the things that bound teardown. `Created` is a JSON field in the same attacker-writable
mapping file as `instance_path` and `handle`. R6 is a usability rail against acting on a
superseded session; it bounds nothing against a crafted store. One sentence saying so keeps
the paragraph honest.

Related, and worth one line in the design even though the PRD scopes it out: three different
mapping-to-instance selection rules will exist after this change. Destroy and `niwa list`
use newest-by-`Created` (design:672-674); `niwa reap`'s primary sweep uses last-write-wins
over directory-read order (`internal/cli/reap.go:346-351`) and its backstop uses presence
only (`reap.go:584-589`). The reaper is the one that deletes instances.

### L15 (Low) — The new sibling directories versus the root scanners is an unasserted invariant

See prior finding 17. `<root>/.niwa.next-*`, `<root>/.niwa.trash-*` and `<root>/.niwa.stray-*`
are direct children of the workspace root. `EnumerateInstances`
(`internal/workspace/state.go:353-374`) and both reaper sweeps scan there. They are excluded
today only because enumeration requires `<child>/.niwa/instance.json` (`state.go:368`,
`statePath(dir)`) and the carry-over puts `instance.json` at `snap/instance.json` — and this
change reshapes the staging layout. Note also that `state.go:368` uses `os.Stat` with no
type check, so a *directory* named `instance.json` qualifies a child. State the invariant and
pin it with a test that creates all three sibling shapes and asserts `EnumerateInstances`
and both reaper sweeps return nothing extra.

### L16 (Low) — "The swap's file names have one owner" is scoped wider than what lands

See prior finding 13. `internal/plugin/installer.go:135-176` runs the same `.next`/`.prev`
rotation with `os.RemoveAll` and `os.Stat` and is not routed through `configdir`. Scope the
claim to workspace configuration directories.

---

## Overclaims

1. **design:976-978** — "A clone holds a `workspace.toml`, not niwa's marker, so it fails all
   three tests and is left in place and reported."
   → False. Every overlay clone niwa maintains is a refreshed config directory and carries
   `.niwa-snapshot.toml` (`overlaysync.go:37` uses that marker to decide the clone exists;
   `snapshotwriter.go:426` writes it on every materialization). Should say: the marker cannot
   distinguish them, which is why the auxiliary paths are namespaced out of the overlay
   directory / gated on a swap journal.

2. **design:222-224** — "Only a refreshed directory ever has a previous snapshot, and a
   refreshed directory always carries the marker, so the marker is what separates niwa's own
   rotation from a name that merely looks like it."
   → Self-refuting: because every refreshed directory carries the marker, a refreshed overlay
   named `<x>.prev` carries it too. The premise proves the opposite of the conclusion. Should
   say: the marker establishes that a directory is niwa's, not *whose* or *which role* it
   holds; role has to come from a record written at swap time.

3. **design:979-981** — "The destinations niwa creates carry random suffixes ... so a name
   squatter cannot wedge mapping writes indefinitely."
   → True for mapping writes; false for refreshes. `<dir>.prev` is fixed (design:158, 752),
   is the swap's own rename destination (`snapshot.go:69`), and the design removes the
   preflight that currently clears it (design:664-666). Should say: mapping writes cannot be
   wedged because their destinations are randomized; refreshes still can be, because `.prev`
   is a fixed name — unless it is randomized too.

4. **design:984** — "All of recovery's check-then-act steps run with the exclusive lock
   held."
   → Still offered as the ordering answer. Should say: the lock orders recovery against other
   *post-change* niwa processes only. It does not order recovery against a concurrently
   running older binary mid-swap, and recovery has no way to tell that state from a crash,
   so it can undo an in-flight swap.

5. **design:390-392 / 937-941** — "control characters stripped by the sanitizer the dispatch
   path already uses" / "the same sanitizer the dispatch path already applies, which strips
   control characters".
   → No such sanitizer. `printableToken` (`dispatch_reentry.go:115`) is a predicate,
   `shellToken` (`:87`) is a quoter, and `:102-106` documents that refusing rather than
   stripping was the deliberate choice. Should name the stripper that will be written, the
   package it lands in (`internal/worktree` cannot import `internal/cli`), and its coverage —
   including that Cc-only stripping leaves U+202E and U+200B.

6. **design:960-962** — "it is created 0600, so no other unprivileged user can open it to
   hold the lock."
   → Holds only for a lock file niwa created; `O_CREAT` does not change an existing file's
   mode or owner. Should say: the descriptor is `fstat`ed and refused unless it is a regular
   file owned by the invoking user with no group or world write bit.

7. **design:1094-1096** — lock files are "harmless if deleted between commands (they are
   recreated)".
   → Contradicts design:165-168's own reason for never unlinking. Should say: harmless only
   when no niwa command holds it, and the post-`flock` `(dev, ino)` re-check is what turns
   the unsafe case into an error instead of a silent loss of ordering.

8. **design:930-931** — "after symlinks are resolved, is one of the directories
   `EnumerateInstances` returns."
   → `EnumerateInstances` returns unresolved lexical joins (`state.go:367`). Comparing a
   resolved path against an unresolved set mismatches under a symlinked workspace root.
   Should say both sides are resolved, and the design should pick which.

9. **design:383** — "requires the branch name to be a plausible ref that does not begin with
   `-`".
   → "Plausible ref" is not a specification, and this is the sole barrier between a wholly
   unvalidated JSON field and two git argv positions plus a paste-ready command. Should name
   the exact pattern.

10. **design:559-563 (Summary)** — "So a write lands either before the carry-over, and rides
    across, or after the swap; a delete is never undone; the reaper and teardown always see a
    whole store; and the directory is never left without its configuration."
    → Stated unconditionally. It holds on unix only; the non-unix gap is stated correctly
    elsewhere (design:241-247, 1036-1037) but the Summary is the paragraph a reader quotes.
    Add "on Linux and macOS".

---

## Conclusion

**Must change before this ships.**

- **H1.** Stop relying on the provenance marker to tell an overlay clone from a previous
  snapshot; it cannot, because overlay clones are refreshed config directories and carry it.
  Namespace the auxiliary paths out of the overlays directory, or gate recovery on a swap
  journal. Correct design:976-978 and design:222-224.
- **H2.** Randomize the moved-aside name, or specify what recovery does with a
  non-evidence `<dir>.prev`. As written, one stray file at a fixed, guessable path
  permanently fails every refresh of a workspace, with the self-repair the design promises
  explicitly declining to touch it.
- **H3.** Add a swap-intent journal so recovery acts on evidence a crash actually produced,
  and state the same-user threat-model boundary in one sentence rather than defending against
  that actor in four paragraphs while leaving rules 1 and 3 forgeable. Say what recovery does
  when an older binary is mid-swap.
- **H4.** Name a mitigation that exists. Specify the branch-name pattern exactly, say which
  package the control-character stripper lives in and how `internal/worktree` reaches it, and
  note that the repo's existing strippers are Cc-only.
- **M5, M6, M8, M9.** Four small specification changes with outsized effect: check the
  existing lock file's owner and mode; re-validate the inode after `flock`; say whether a
  failed evidence test fails the callback; apply the existing `watch.IsSafeHandle` to the
  handle field.
- Prior finding **16** — one sentence to resolve both sides of the instance-path comparison.

**Acceptable residual risk.** The starvation acceptance (prior 11) is now honestly stated and
bounded, with a test. The `Lstat`-then-use window on the instance directory (prior 4) is
correctly described as narrowed and is defended in depth by the record validation. The
non-unix gap (prior 7) is an argued decision with a real distinction from the Codex-lock
precedent. The single 30-second wait a `config.Discover` repair can add to a TAB press
(prior 12) is worth a sentence in the DoS paragraph but is not a blocker. L11 through L16 are
notes, not gates — though L15 should get the test it asks for, since this change is what
makes the invariant fragile.

The mechanism this design describes is good. What keeps failing review is the gap between
the mechanism and the sentences describing it: three of the four High findings are places
where the security section asserts a property the mechanism does not deliver, and two of
them are the second iteration of the same sentence. The fix for H4 in particular is not a
code change, it is writing down what will actually be built.
