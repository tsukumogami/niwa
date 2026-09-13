# Security Review (Phase 6 juror, third pass)

Design under review: `docs/designs/DESIGN-session-store-teardown.md` (status Planned, revised
after two FAILs).
Upstream: `docs/prds/PRD-session-store-teardown.md`. Downstream: `docs/plans/PLAN-session-store-teardown.md`.
Prior passes: `wip/research/design_session-store-teardown_phase6_security-review.md` (17 findings),
`wip/research/design_session-store-teardown_phase6_security-rereview.md` (16 findings, 10 overclaims).
Code baseline: the worktree at `public/niwa/.claude/worktrees/session-store-teardown`, `go 1.25.3`.

Every claim the design makes about existing code was re-checked against the tree. Where the
design names a mechanism as "existing", I read the mechanism.

## Verdict: FAIL

This is by a wide margin the best revision so far. Thirteen of the second pass's sixteen
findings are genuinely closed, three are partially closed, none is merely reworded, and all
ten overclaims are gone and replaced with accurate statements. The swap journal is exactly
the mechanism the last pass asked for, the threat-model boundary is now stated once and
honoured consistently, `internal/safetext` is a real package in a legal position, and
`watch.IsSafeHandle`, `os.OpenRoot` and the `internal/worktree` import direction all check
out against the tree.

It fails on two things, both of the class the last two passes failed on: a sentence asserting
a property the code does not have.

First, **`@` is producible in an overlay directory name**, and not by an exotic forge name —
by niwa's own documented source-slug grammar. `docs/guides/workspace-config-sources.md:80`
defines a source as `[host/]owner/repo[:subpath][@ref]`, and `config.OverlayDir`
(`internal/config/overlay.go:325-345`) applies no charset check at all. So
`niwa init --overlay acme/tools@swap` clones the perfectly ordinary repo `acme/tools` at ref
`swap` into `~/.config/niwa/overlays/acme-tools@swap`, which is byte-identical to
`SwapDir("~/.config/niwa/overlays/acme-tools")`. The design's load-bearing sentence — "A
forge host's names cannot contain `@`, so no overlay can produce `D@swap`" — is true about
forges and irrelevant: the `@` comes from niwa. Everything the revision built on top of that
premise (H1's fix, "Two siblings rather than five, and neither is a name an overlay clone can
produce") inherits the hole. And the sibling `.lock` was never namespaced at all: `.` is
legal in a GitHub repo name, so overlay `acme/tools.lock` lands on the lock path of overlay
`acme/tools`.

Second, **recovery rule 3 depends on a fact the journal never records.** The journal is
written once, before the first rename, and cleared after the second. A process killed between
the two renames cannot go back and amend it. So `Journal.Recreated` — "D reappeared after the
first rename", the flag rule 3 branches on — can never be true in the crash case it exists
to serve. Rule 3 will therefore always take its other arm and trash the moved-aside snapshot,
in exactly the situation the design describes as "the ordinary condition it exists to detect".
There is a clean evidence-based discriminator available and the design does not use it.

---

## Second-pass findings

| # | Second-pass finding | Verdict |
|---|---|---|
| H1 | Provenance marker cannot tell an overlay clone from a previous snapshot | **PARTIAL** |
| H2 | `<dir>.prev` fixed name, squatter wedges every refresh | **CLOSED** |
| H3 | Recovery has no swap-intent record | **PARTIAL** |
| H4 | "The sanitizer the dispatch path already uses" does not exist | **CLOSED** |
| M5 | Pre-existing lock file's mode and owner never checked | **CLOSED** |
| M6 | "Harmless if deleted" vs never-unlink; no inode re-check | **CLOSED** |
| M7 | No lock-ordering discipline across config directories | **CLOSED** (rationale wrong — see Overclaim 7) |
| M8 | "Left in place and reported" ambiguous | **CLOSED** |
| M9 | `handle` field never validated | **CLOSED** |
| M10 | Trash deleted outside the lock, concurrent deleters | **CLOSED** |
| L11 | Third copy of the destroy argv (`DefaultDestroySession`) | **PARTIAL** |
| L12 | `.stray-*` accumulates unswept | **CLOSED** |
| L13 | Dirty guard's non-ENOENT stat error falls through | **CLOSED** |
| L14 | R6 rests on the untrusted `created` field | **CLOSED** |
| L15 | New siblings vs the root scanners | **CLOSED** |
| L16 | "One owner for the layout" over-scoped | **CLOSED** |

Closed: 13. Partial: 3. Open: 0. All ten overclaims: closed.

### H1 — PARTIAL

The *reasoning* is now correct, and emphatically so. Design:266-276 and design:1210-1220 state
plainly that the marker cannot carry the weight, that an overlay clone *is* a refreshed config
directory, that `EnsureOverlaySnapshot` uses the marker to decide the clone exists, and that
role must come from a record written at swap time. I verified all of it:
`internal/workspace/overlaysync.go:37` is `if provenanceMarkerExists(dir) || dotGitExists(dir)`,
the true branch reaches `EnsureConfigSnapshotWithStatus` (`:40`) and the false branch reaches
`MaterializeFromSource` (`:51`), and both funnel through `materializeAndSwap`, which writes the
marker unconditionally at `snapshotwriter.go:423`. Overlay clones do carry it. The design has
this exactly right.

Recovery no longer infers role from a name or from content, which is the substantive fix.

What keeps it partial is the replacement premise. See **New finding N1**: `@` is producible,
so `D@swap` is not out of the overlay namespace, and the design's own summary sentence
("neither is a name an overlay clone can produce", design:977-978) is false. The `.lock`
sibling was never moved and collides on a plain `.` — see **N3**.

### H2 — CLOSED

Design:176-184: the moved-aside destination is `D@swap/prev-<random>`, the unconditional
preflight delete is dropped *because* there is no longer a fixed destination to clear, and a
random-name collision "retries once with a fresh name". Design:889-891 adds the piece that
matters most for an upgrade: anything at the legacy `D.prev` or `D.next` "is left untouched,
because it belongs to a binary this code cannot coordinate with; that binary clears it with
its own preflight." Verified the legacy behaviour it relies on:
`internal/workspace/snapshot.go:48,53` is `prev := target + ".prev"` then an unconditional
`safeRemoveAll(prev)` before the `os.Lstat(target)` at `:64`. The self-healing claim for old
binaries holds.

Verified the design's description of today's `.prev` loss is accurate, and worth sharpening
one notch for the implementer: on a crash between `snapshot.go:69` and `:77`, the next
refresh's `preserve*` helpers no-op (the live directory is gone), then the preflight at `:53`
deletes `.prev`. What is irrecoverably lost is the *carried-over local state* —
`instance.json`, `dispatch-briefs/`, `sessions/` — not the upstream content, which is
refetched.

### H3 — PARTIAL

The journal landed as specified, and it does the three jobs the last pass asked of it:
look-alike directories are never touched, an older binary's in-flight swap is invisible
(design:281-292 spells out the upgrade scenario correctly), and recovery finally has evidence
that a swap started. The threat-model boundary is stated once, in Decision 1 (design:305-317)
and again in Security Considerations (design:1123-1135), and the rest of the document is
consistent with it. That is the fix that was asked for.

Three things keep it partial, all new: rule 3's `Recreated` flag is unsettable (**N2**, High);
the journal's own write is not specified as atomic or durable and an unparseable journal has
no rule (**N6**); and the document contradicts itself on when the journal is cleared (**N7**).

### H4 — CLOSED

The non-existent mechanism is gone and every replacement checks out.

- The dispatch-path claim is now correct: design:527 says the dispatch path "deliberately
  *refuses* rather than strips (and its comment says so)". Verified —
  `internal/cli/dispatch_reentry.go:115` `printableToken` is a `bool` predicate over
  `unicode.IsControl`, `:87` `shellToken` is a POSIX quoter, and the comment at `:103-106`
  reads "The answer is to refuse rather than to strip."
- `internal/safetext` is a legal leaf. `go list -deps ./internal/worktree` returns exactly
  `internal/gitexclude` and itself; `internal/cli` imports `internal/worktree`, so the
  direction is right and a new no-dependency leaf is importable from both.
- `watch.IsSafeHandle` exists, is exported, and is exactly `[A-Za-z0-9_-]{1,128}` —
  `internal/watch/state.go:407-427`. `internal/cli` already calls it at `watch.go:403,456,478,524`
  and `dispatch_reentry.go:135,180`, so this is not a new edge. `SaveStagedRecord` (`state.go:331-334`)
  does refuse an unsafe handle.
- The branch pattern is spelled byte by byte (design:524-530) instead of "a plausible ref",
  and is passed after `--`. I confirmed `git branch -d -- <name>` is accepted.

Two small residuals, both filed below: the pattern omits git's `@{` rule (**N10**), and the
claim that `internal/tui`'s sanitizer is Cc-only is wrong about *why* it is inadequate
(**Overclaim 11**).

### M5 — CLOSED

Design:203-210 and design:1196-1204 specify the `fstat`: regular file, owned by the invoking
user, no group or world write bit, with the correct reason (`O_CREAT` changes neither the mode
nor the owner of an existing file) and the two concrete exposures (`XDG_CONFIG_HOME`-controlled
global lock, group-writable workspace parent). Design:786-790 puts it in `configdir_unix.go`
alongside the `O_NOFOLLOW|O_NONBLOCK` pair, and correctly flags that this is new code with no
precedent — verified, the repo's only `syscall.Stat_t` read is a `%d` in a diagnostic
(`internal/cli/sessionattach/attach.go:201`).

### M6 — CLOSED

Both halves. The post-`flock` `(dev, ino)` re-check with close-reopen-retry is specified
(design:212-218, :1204-1208), and the Mitigations sentence is corrected to "Deleting one is
harmless only while no niwa command holds it" (design:1408-1409). One new consequence the
correction does not reach: `SaveState`'s decision *whether to lock at all* keys on the lock
file's existence, so deleting it silently downgrades the write — see **N12** in the Low list.

### M7 — CLOSED (mechanism), rationale wrong

The ordering rule is specified (design:226-231): global, then overlay, then workspace root,
refused via the same process-local held set. Fine as future-proofing. The justification given
for it is not correct — see **Overclaim 7**.

### M8 — CLOSED

Design:318-325 and design:906-911 are unambiguous: a failed check inside recovery "is a
warning, not a failure of the operation that triggered it", reported once per directory per
process, callback still runs; only a rename that fails after its retry is fatal, and only to
the operation that needed it. The rationale given (recovery runs at the head of every
exclusive section, so a fatal reading would wedge every mapping, watch and state write) is
the right one. Phase 2 asks for the test.

### M9 — CLOSED

Design:449-457, :1141-1146 and Phase 6 (:1102) all apply `watch.IsSafeHandle` to the `handle`
field, with the correct semantics: a mapping whose handle fails it keeps its session-id match
and loses its handle match. See the quotation nit at **Overclaim 10**.

### M10 — CLOSED

Design:913-918 states it plainly: two processes can end up removing the same tree, the second
sees `ENOENT` and `ENOTEMPTY`, post-lock trash deletion is best-effort and its errors are
discarded, "matching what `SwapSnapshotAtomic` already does with its old snapshot". Verified
the precedent at `snapshot.go:98`, `_ = safeRemoveAll(prev)`.

### L11 — PARTIAL

The design now covers `workspace.DefaultDestroySession` in Decision 2 (:512-518), in
Components (:869-872) and in Phase 6 (:1103). The dependency direction is legal:
`internal/workspace` already imports `internal/worktree` at `apply.go:26`. Verified the
current state — `internal/workspace/bootstrap.go:307-308` runs
`"worktree", "remove", "--force", st.WorktreePath` and `"branch", "-D", st.BranchName` with no
`--`, no validation, both `Run()` errors discarded, and `sessionID` concatenated into a path
at `:282` with no validation before `os.Remove` at `:310`.

It stays partial because, as specified, it cannot be built: Key Interfaces declares the
validator **unexported** (`func validateSessionRecord(...)`, design:968) in a package that
`internal/workspace` must call it from. See **N5**.

### L12 — CLOSED

Design:1326-1331 says what a stray is, that nothing sweeps one, why (niwa could not tell what
it was), that they accumulate, and that the config-sources guide says removing one is safe
once checked. Listed in Consequences (:1378-1380) and in Phase 7's doc deliverable. Accepted,
argued residual.

### L13 — CLOSED

Design:497-500 and :1186-1189: the dirty-tree guard "fails closed on *any* `stat` error, not
only on `ENOENT`", with permission errors and `ELOOP` named. Verified the fail-open today at
`internal/worktree/worktree.go:87-89`.

### L14 — CLOSED

Stated twice, in Decision 2 (:471-473) and Security Considerations (:1159-1162): R6 "reads the
untrusted `created` field, so it keeps teardown off a superseded session but bounds nothing
against a crafted store."

### L15 — CLOSED

Design:1314-1324 states the invariant, explains that it holds "by one path segment", pins it
with a test creating a lock file plus a swap directory containing staging, trash and stray,
and records the `os.Stat`-with-no-type-check detail. Verified `EnumerateInstances`
(`internal/workspace/state.go:353-374`): `os.Stat(statePath(dir))` at `:368` with no type
check, `ValidName` filtering at `:364`, and `entry.IsDir()` at `:361` giving lstat semantics.
Phase 3 carries the deliverable.

### L16 — CLOSED

Design:160-162 and :1352-1354 scope the claim to workspace configuration directories and name
`internal/plugin`'s untouched `.next`/`.prev` rotation explicitly.

---

## New findings

### N1 (High) — `@` is producible in an overlay directory name, so `D@swap` is not out of the overlay namespace

**Design quote** (design:173-175, restated at :977-978 and :1222-1224):

> A forge host's names cannot contain `@`, so no overlay can produce `D@swap`.
> Two siblings rather than five, and neither is a name an overlay clone can produce.

**Why it matters.** True about forges, irrelevant to the risk, because the `@` does not come
from a forge. It comes from niwa. `docs/guides/workspace-config-sources.md:80` documents the
source slug as `[host/]owner/repo[:subpath][@ref]`, and `internal/source/parse.go:46-55` peels
`@<ref>` off exactly that way. But `config.OverlayDir` (`internal/config/overlay.go:325-345`)
parses with a *different* function and applies **no charset check of any kind**:

```go
org, repo, ok := parseOrgRepo(s)
if !ok { return "", fmt.Errorf("cannot parse overlay URL %q", overlayURL) }
dirName = org + "-" + repo
```

`parseOrgRepo` (`overlay.go:259-318`) checks non-emptiness, trims a `.git` suffix, and — in the
shorthand branch only — rejects `:` in `org` and a leading `/`. There is no regexp, no rune
loop, no charset table anywhere in the file. Contrast `config.NamePattern`
(`internal/config/config.go:20`, `^[a-zA-Z0-9._-]+$`), which the project does apply to group
and repo names at `config.go:682-686` and which overlay names never touch.

So `niwa init --overlay acme/tools@swap` does two different things with two different parsers:
`source.Parse` reads `@swap` as a git ref and clones the ordinary repo `acme/tools`;
`OverlayDir` reads `@swap` as part of the repo name and puts the clone at
`~/.config/niwa/overlays/acme-tools@swap` — byte-identical to
`SwapDir("~/.config/niwa/overlays/acme-tools")`. No forge-illegal character anywhere. Verified
against a copy of the real functions; these all produce an `@` in the directory name:

```
OverlayDir("acme/tools@swap")                        -> overlays/acme-tools@swap
OverlayDir("acme@swap/tools")                        -> overlays/acme@swap-tools
OverlayDir("https://github.com/acme/tools@swap")     -> overlays/acme-tools@swap
OverlayDir("git@github.com:acme/tools@swap.git")     -> overlays/acme-tools@swap
OverlayDir("file:///srv/git/acme-tools@swap.git")    -> overlays/file-acme-tools@swap
```

The overlay root (`overlay.go:351`) holds nothing but other overlay clones, so this is exactly
the overlay-vs-overlay collision the `@swap` scheme was introduced to eliminate. And the name
is sticky: `OverlayURL` round-trips through `.niwa/instance.json` with no read-side validation
and is fed back into `OverlayDir` on every apply (`internal/workspace/apply.go:1059`).

The consequences are worse than the `.prev` case the last pass found, because `@swap` is a
*working* directory rather than a moved-aside one. With both overlays present, refreshing
`acme-tools` creates its staging, its journal and its trash **inside the live `acme-tools@swap`
clone**, and renames the live `acme-tools` config directory into it. Refreshing
`acme-tools@swap` renames that whole directory — another refresh's journal, staging and
moved-aside snapshot included — to `acme-tools@swap@swap/prev-X`. The two refreshes hold
*different* locks (`acme-tools.lock` and `acme-tools@swap.lock`), so nothing serializes them,
and the in-flight refresh loses its journal along with everything the journal names, which is
precisely the state recovery cannot repair.

**Fix.** Charset-check the derived name where it is derived: apply `config.NamePattern` (or
percent-escape) to `dirName` in `OverlayDir`, and re-validate on read from `instance.json`.
Then say so in the design rather than asserting a property of forges. As defence in depth,
have `configdir` refuse to use a `SwapDir` that itself holds a `workspace.toml` or a
provenance marker, and add the overlay-shaped test Phase 2 already promises ("an overlay-shaped
directory at every name the swap could otherwise have used") with `acme/tools@swap` and
`acme/tools.lock` as the cases.

### N2 (High) — Recovery rule 3 branches on `Journal.Recreated`, which nothing can ever set

**Design quote** (design:916-918, and the struct at :960):

> The owner is gone and `D` exists: the second rename landed. If the journal records that `D`
> was recreated after the move, rename `D` to `D@swap/stray-<random>` and keep it, then rename
> the moved-aside path back; otherwise rename the moved-aside path to a trash name.
> `Recreated  bool // D reappeared after the first rename`

**Why it matters.** Rule 3 has to distinguish two states that look identical from the outside:
(a) the second rename landed, so `D` is the new snapshot and the moved-aside copy is garbage;
(b) the second rename did *not* land and some other writer recreated `D`, so the moved-aside
copy is the only real configuration and `D` is a stray. The design's discriminator is
`Recreated`.

Nothing can set it. Per design:293-296 the journal is written once, "before the first rename",
and removed "after the second rename". Between the two renames the owning process holds the
exclusive lock and does nothing but the second rename; and in the case rule 3 exists for, that
process is dead. A dead process cannot amend its journal. So `Recreated` is false in every
crash, rule 3 always takes its second arm, and the real snapshot is renamed to a trash name
and deleted after the lock is released.

That is not a corner case by the design's own account. Security Considerations (design:1326-1328)
says "rule 3 fires whenever an in-place write lands inside a swap window — which, until this
change is everywhere, is the ordinary condition it exists to detect." The writers that can
still recreate `D` mid-swap are exactly the ones outside this change's reach: an older binary
(`internal/watch/state.go:162` `os.MkdirAll(dir, 0o755)` on `.niwa`, `state.go:299` in
`SaveState`, `session_map.go:150` creating `.niwa` as an implicit parent), a non-unix process,
`niwa init`, or a third-party tool. In every one of those, the design as written deletes the
configuration it set out to protect.

**Fix.** Use evidence the journal already names instead of a flag nobody can write. The second
rename moves `journal.Staging + "/snap"` to `D`; therefore `snap` exists **iff the second
rename has not landed**. Rule 3 becomes: if `journal.Staging/snap` still exists, the second
rename did not land, so keep `D` as `D@swap/stray-<random>` and rename the moved-aside path
back; otherwise trash the moved-aside path. That is one `Lstat` on a path the journal already
records, it cannot be forged by a crash, and it lets `Recreated` be deleted from the struct.
If the field is kept for diagnostics, say it is diagnostic only. The same note applies to
`PID` and `StartedAt`, which the design records and never uses — rule 1's liveness test keys
on the staging lock, correctly, and an implementer who reaches for the pid instead reintroduces
pid reuse.

### N3 (Medium) — `<dir>.lock` is still in the overlay sibling namespace, and `.` is legal in a repo name

**Design quote** (design:190-193):

> The lock for config directory `D` is the sibling file `D + ".lock"`
> (`<root>/.niwa.lock`, `$XDG_CONFIG_HOME/niwa/overlays/<name>.lock`, ...)

**Why it matters.** The revision namespaced `@swap` out of the collision space (or tried to —
see N1) and left `.lock` exactly where `.prev` was. `.` is legal in a GitHub repo name and
`OverlayDir` checks nothing, so `OverlayDir("acme/tools.lock")` returns
`overlays/acme-tools.lock`, which is the lock path of overlay `acme/tools`. Both orderings are
broken and neither self-heals:

- Clone first: `Acquire("overlays/acme-tools")` opens `overlays/acme-tools.lock` and finds a
  *directory*. The new `fstat` regular-file check (correctly) refuses, so **every niwa command
  that touches overlay `acme/tools` fails hard**, permanently, with no code path that clears it —
  the same shape as the H2 wedge the revision just fixed for `.prev`.
- Lock first: materializing overlay `acme/tools.lock` ends in `rename(snap, D)` onto an
  existing regular file, which fails, so that overlay can never be installed.

**Fix.** Either move the lock inside the swap directory (`D@swap/lock`) once `@swap` is itself
collision-proof, or — better, since it fixes N1 at the same time — charset-check `dirName` in
`OverlayDir` as N1 describes. Whichever is chosen, the design's "exactly two siblings ...
neither is a name an overlay clone can produce" sentence has to be rewritten to say what
actually guarantees it.

### N4 (Medium) — `ephemeralInstancePaths` reads the mapping store directly and is not routed through `Read`

**Design quote** (design:253-256):

> Because the read locks live inside the store functions, every current and future reader of
> the mapping store decides on a snapshot taken while no swap ran.

**Why it matters.** There is a current reader that does not go through the store functions.
`ephemeralInstancePaths` (`internal/workspace/state.go:447-470`) does its own
`os.ReadDir(filepath.Join(workspaceRoot, StateDir, "sessions"))` at `:453`, its own
`os.ReadFile` and its own inline `json.Unmarshal` — never `ListSessionMappings`. It is called
from `state.go:422` on the `niwa list` path. The PRD names it by name: R15 covers "the reaper's
sweeps, `niwa worktree destroy`, and **the ephemeral-instance scan `niwa list` uses, which
reads the store directly today**" (PRD:302-306).

Its failure mode in the rename window is silent, which is the worst kind: `:454-456` returns an
empty set when the directory is unreadable, so every ephemeral instance is reclassified as
non-ephemeral for that run of `niwa list`. The design's caller table (design:239-250) lists
only `ListSessionMappings` and `ReadSessionMapping`.

The two reaper sweeps *are* covered — verified both go through `ListSessionMappings`
(`internal/cli/reap.go:342` primary, `:580` backstop). One correction for the design's L14
note while it is being edited: the primary sweep's last-write-wins is not over directory-read
order, because `ListSessionMappings` sorts by `SessionID` before returning
(`session_map.go:225-227`), so the lexically greatest session UUID wins deterministically.

**Fix.** Add `ephemeralInstancePaths` to the caller table and route it through
`ListSessionMappings` inside `Read` in Phase 3, or state why it is left out and narrow the
"every current and future reader" sentence to "every reader that goes through the store
functions".

### N5 (Medium) — The record validator is declared unexported in a package `internal/workspace` must call it from

**Design quote** (design:967-968):

```go
// internal/worktree (unexported)
func validateSessionRecord(instanceRoot string, r *SessionRecord) error // path containment, branch shape
```

against design:869-872 and Phase 6 (:1103), which require `workspace.DefaultDestroySession` to
be "routed through the same validator".

**Why it matters.** As written this cannot be built. The direction is legal —
`internal/workspace` already imports `internal/worktree` at `apply.go:26` — but an unexported
function is unreachable across the package boundary, so the third argv copy stays the one that
is still unguarded, which is precisely L11.

Two adjacent details the implementer will hit. The declared parameter type `*SessionRecord`
does not exist; the type is `SessionLifecycleState` (`internal/worktree/session_lifecycle.go:37`).
And `internal/workspace/bootstrap.go:314-317` carries a comment arguing the opposite dependency
direction — "kept local to avoid inverting the dependency direction (worktree is a leaf
package)" — which is stale given `apply.go:26` but which a reviewer will cite at implementation
time.

**Fix.** Declare it exported: `func ValidateSessionRecord(instanceRoot string, st *SessionLifecycleState) error`,
and say in Components that `bootstrap.go`'s copy is deleted in favour of calling
`worktree.DestroySession`-adjacent validation rather than reimplementing the argv. Note the
behaviour difference while there: `DefaultDestroySession` uses `-D` unconditionally
(`bootstrap.go:308`), where `DestroySession` chooses `-d`/`-D` (`worktree.go:335-338`); the
design should say whether that changes.

### N6 (Medium) — The journal's own write is not atomic or durable, and an unparseable journal has no rule

**Design quote** (design:293-296):

> Under the exclusive lock, before the first rename, it writes `D@swap/journal.json`

**Why it matters.** Every other write this design adds is specified as temp-file-plus-rename —
the marker rewrite ("re-read, compare, atomic write"), `SaveState`, the mapping writes. The
journal, on which all of recovery now depends, is the one write specified as a plain write.
And no rule covers a journal that parses badly: rule 0 is "No journal", rules 1-3 assume a
well-formed one, and the design has deliberately removed every content-based fallback, so
there is nothing to fall back to.

The exposure is narrow but it is the one R16 asks about. If the journal is lost or truncated
after the first rename, `D` is missing and no rule will restore it: rule 0 sends control to
rule 4, which only handles staging and trash, so `D@swap/prev-<random>` sits there forever with
the only copy of the configuration in it and the workspace has no `.niwa` at all. Under ext4
delayed allocation a `write` without `fsync` followed by a `rename` can leave a zero-length
file after a power loss, which is that state exactly. It is fair to note the PRD scopes R16 to
"the process killed" (PRD:307), where the page cache saves you; the design should say that is
the scope rather than claim "interrupted at any point".

**Fix.** Specify the journal as temp-file-plus-rename with an `fsync` of the file and of
`D@swap` before the first rename, and add one rule: an unparseable journal is treated as a live
journal that names nothing — leave everything, warn once, run the callback — except when `D` is
missing, where the design should say whether it scans `D@swap/prev-*` or requires manual repair.
Either answer is defensible; silence is not. Add the truncated-journal case to Phase 2's test
list, which currently covers a journal naming a missing path but not a journal that will not parse.

### N7 (Medium) — The design contradicts itself on when the journal is cleared

**Design quotes.** Decision 1 (design:293-295): "It removes the journal **under the lock** after
the second rename." Data Flow (design:1004): "rename `prev-X` to `D@swap/trash-Y`; clear the
journal; release". Summary (design:713): "write the journal, and swap; **the journal is removed
and the trash deleted after the lock is released**."

**Why it matters.** The Summary is the paragraph readers quote and implementers skim, and its
ordering is wrong. Clearing the journal after release leaves a stale journal readable by the
next process to take the lock, which then runs rule 3 against paths the first process already
moved to trash — producing a spurious warning at best, and at worst interacting badly with
whatever N2's fix makes rule 3 do. Trash deletion belongs after release; journal clearing does not.

**Fix.** Delete the clause from design:713: "write the journal, and swap; the journal is cleared
under the lock and the trash deleted after it is released."

### N8 (Medium) — `internal/configdir` cannot call `safeRemoveAll`

**Design quote** (design:1258):

> Removals rename to a trash name and delete with `safeRemoveAll`, which removes a top-level
> symlink without following it.

**Why it matters.** `safeRemoveAll` is unexported in `internal/workspace`
(`internal/workspace/snapshot.go:104-121`), and the design places `internal/configdir` *below*
`internal/workspace` in the dependency graph (design:754-760). It is the same reachability
problem the design already noticed and handled correctly for the two marker filenames
(design:800-803, "copies the two marker filenames it tests for rather than importing them ...
with a test that fails if either copy drifts"), left unnoticed for the helper that does the
deleting. This is the fourth instance across three passes of a mitigation named in prose being
structurally out of reach of the package that needs it, which is why it is worth flagging
despite being mechanically trivial.

**Fix.** One sentence in Components: `configdir` owns its own `safeRemoveAll`, and
`internal/workspace`'s existing copy calls into it (or is deleted). The function is 15 lines
and has no dependencies beyond `os` and `io/fs`.

### N9 (Medium) — `ReconcileAndReloadConfig`'s marker probe stays unlocked

**Design quote** (design:239-250, caller table): "Post-swap reload in `ReconcileAndReloadConfig`
| `Read` | the `config.Load`".

**Why it matters.** The design's Context names this defect explicitly: "the refresh reads the
live marker with no ordering, so it can fail with ENOENT while another refresh is mid-swap"
(design:99-100). But `ReconcileAndReloadConfig` (`internal/workspace/configreload.go:62-65`)
probes the marker *before* it does anything else:

```go
configDir := filepath.Dir(configPath)
if !provenanceMarkerExists(configDir) {
    return current, nil
}
```

and the caller table covers only the `config.Load` at `:73`. A probe landing in the rename
window returns false and the function silently returns the stale config without reconciling —
no error, no retry. That is the named defect, at a call site the fix does not reach.

`config.Load(configPath)` itself takes an explicit path and does not call `config.Discover`
(`internal/config/config.go:560`), so the design's separate assertion that no entry-point
callback reaches the lock-taking discovery branch holds for this one. Worth pinning with a test
rather than leaving as inspection, since the design has no test for that invariant.

**Fix.** Move the `configreload.go:63` probe inside the `Read` section and say so in the caller
table.

### N10 (Medium) — The branch pattern omits git's `@{` rule

**Design quote** (design:524-530):

> It must be a valid ref under `git check-ref-format`'s rules as applied here: non-empty; no
> byte below 0x20 and no DEL; no space; none of `~`, `^`, `:`, `?`, `*`, `[`, `\`; no `..`; no
> leading `-`; no trailing `.lock`; no leading or trailing `/` and no `//`; and not the single
> character `@`.

**Why it matters.** `check-ref-format` also rejects the two-character sequence `@{`, and the
design's list does not. `@{-1}` and `@{u}` contain no rejected byte, are not the single
character `@`, and pass the pattern as written. I confirmed against git that
`git branch -d -- '@{-1}'` deletes the previously checked-out branch — the `--` does not stop
revision-shorthand expansion, because the argument is still resolved as a ref. So a crafted
lifecycle record with `branch_name: "@{-1}"` survives validation and deletes a branch nobody
named. The list also omits check-ref-format's "no path component beginning with `.`" and "no
trailing `.`", which matter less but are cheap.

**Fix.** Add "no `@{` sequence; no path component beginning with `.`; no trailing `.`" to the
pattern, or say the pattern is a deliberate strict subset and name what it does not enforce.
Add `@{-1}` to Phase 6's record-validation test list beside the `--upload-pack=`-shaped branch.

### N11 (Low) — The worktree path is not specified as passed after `--`

Design:510 and :866 put the *branch name* after `--`. Nothing says the same for the worktree
path, which reaches `git worktree remove --force <path>` (`internal/worktree/worktree.go:330`)
in an argv position where a leading `-` is a flag. The containment rule is stated on the
resolved form ("after resolution, to lie under the instance's own worktrees directory"), but
the design never says the resolved path is what is passed to git rather than the raw JSON
string. Say that the resolved absolute path is passed, after `--`.

### N12 (Low) — Deleting `<dir>.lock` silently downgrades `SaveState` to an unlocked write

Design:655-659 makes `SaveState` decide whether to take the lock by testing whether "a sibling
`.niwa.lock` exists, or the directory is a workspace root or a single-instance root". The first
disjunct is the one that fires for an overlay or global directory. The corrected M6 mitigation
now says deleting a lock file is "harmless only while no niwa command holds it" — true for
ordering, but a deleted lock file also flips this heuristic, so the very next `SaveState` takes
the unlocked, `MkdirAll`-capable path into a directory a refresh may be swapping. Prefer a
positive test (the directory carries a provenance marker, or its parent is the overlays root /
`XDG_CONFIG_HOME/niwa` / a workspace root) over the lock file's own existence.

### N13 (Low) — The `preserve*` symlink rationale is wrong for the path it names

Design:820-822 and :1257-1261 justify the `Lstat` change with: "today a symlink at
`<configDir>/sessions` would be walked and its target's contents promoted by the swap to become
the mapping store." That is not what happens. `preserveSessionMappings` calls `copySubtree`
(`internal/workspace/fallback.go:368`), which is `filepath.WalkDir`; `WalkDir` lstats its root,
sees a symlink rather than a directory, and the callback's `if path == src { return nil }` at
`fallback.go:373-375` ends the walk immediately. It also skips every non-regular entry by
design (`fallback.go:362-366`). So a symlinked `sessions` produces an **empty** `dst` that is
chmodded and promoted — total silent loss of the mapping store, not promotion of a target's
contents. The claim *is* true of `preserveInstanceState`, which uses `os.ReadFile`
(`snapshotwriter.go:485`) and does follow the link. The change is still right; the stated reason
should be corrected, and the `sessions` case reframed as data loss, because that is the more
serious of the two and the one R12/R13 care about.

### N14 (Low) — `D@swap`'s own mode is unspecified

The design specifies 0600 for the lock, 0700 for staging, 0755 for `snap/`, and says nothing
about `D@swap` itself. Under a group-writable parent — the scenario the new lock-ownership check
exists for (design:207-210) — a 0755 `@swap` lets another user plant entries inside it, which is
where the journal, the staging directories and the moved-aside snapshot all live. Specify 0700,
and have `Recover` apply the same owner check to `D@swap` that `config.Discover`'s branch already
applies (design:1266-1269).

### N15 (Low) — The per-command wait is still a multiple of the bound

Design:1299-1301: "No lock wait is unbounded: a command that can't get the lock within 30
seconds fails with an error naming the directory." True per acquisition. The revision bounded
`config.Discover` to one repair per command, which closes the first pass's version of this. But
`niwa apply` at a workspace root calls `Applier.Apply` once per instance
(`internal/cli/apply.go:262-272`, a sequential loop), and each call touches the root `.niwa`
(`internal/workspace/apply.go:640`), the machine-global overlay clone (`:1063`/`:1096`) and the
global clone (`:964`). An N-instance apply therefore makes roughly 3N acquisitions, two of them
per instance on directories shared by every workspace on the machine. Worth one sentence in the
DoS paragraph, alongside the TAB-press one that is already there.

---

## Overclaims

1. **design:173-175** — "A forge host's names cannot contain `@`, so no overlay can produce
   `D@swap`."
   -> True about forges, false about the risk: the `@` comes from niwa's own slug grammar, and
   `OverlayDir` applies no charset check. Replace with: "`config.OverlayDir` applies no charset
   check and niwa's source slug uses `@` as its ref separator, so `--overlay acme/tools@swap`
   produces `overlays/acme-tools@swap` today. This change therefore charset-checks the derived
   overlay directory name; `@swap` is collision-free because of that check, not because of what
   a forge permits."

2. **design:977-978** — "Two siblings rather than five, and neither is a name an overlay clone
   can produce."
   -> Both halves fail: `@swap` per N1, and `.lock` because `.` is legal in a repo name, so
   overlay `acme/tools.lock` lands on overlay `acme/tools`'s lock path. Replace with the
   mechanism that actually guarantees it, once N1 and N3 are settled.

3. **design:253-256** — "every current and future reader of the mapping store decides on a
   snapshot taken while no swap ran."
   -> `ephemeralInstancePaths` (`internal/workspace/state.go:447`) reads the directory itself
   and is a current reader on the `niwa list` path, named in PRD R15. Replace with: "every
   reader that goes through the store functions; `ephemeralInstancePaths`, which reads the
   directory itself today, is moved onto `ListSessionMappings` in Phase 3."

4. **design:916-918 and :960** — rule 3's `Recreated` discriminator.
   -> Asserts a fact the journal never records, because the journal is written once before the
   first rename and the process that would amend it is dead. Replace with the
   `journal.Staging/snap` existence test, which is evidence a crash cannot fake.

5. **design:713** — "the journal is removed and the trash deleted after the lock is released."
   -> Contradicts design:294 and design:1004. Replace with: "the journal is cleared under the
   lock and the trash deleted after it is released."

6. **design:820-822** — "today a symlink at `<configDir>/sessions` would be walked and its
   target's contents promoted by the swap to become the mapping store."
   -> `copySubtree` is `filepath.WalkDir`, which does not descend a symlinked root
   (`fallback.go:369-375`). Replace with: "today a symlink at `<configDir>/sessions` passes the
   `IsDir` gate but `copySubtree` does not descend it, so the swap promotes an empty mapping
   store and the real one is lost; a symlinked `instance.json`, which `preserveInstanceState`
   reads with `os.ReadFile`, does have its target's contents promoted."

7. **design:229-231** — "`Apply` touches all three in one command, so without the rule two
   processes taking them in opposite orders would each wait out the full bound and both fail."
   -> Deadlock and lock-order inversion require hold-and-wait, and the same decision states two
   paragraphs later that "the lock is taken only inside the two entry points, whose callbacks
   never call another lock-taking function", i.e. acquisitions are sequential and each is
   released before the next is taken. Sequential acquisitions in opposite orders cannot block
   each other. Replace with: "the rule costs one comparison and makes any future nested
   acquisition an immediate error rather than a deadlock; with today's callers, which never
   nest, it never fires."

8. **design:524-530** — "It must be a valid ref under `git check-ref-format`'s rules as applied
   here."
   -> The list omits `@{`, a leading `.` in any path component, and a trailing `.`. `@{-1}`
   passes as written and resolves to the previously checked-out branch even after `--`
   (confirmed against git). Add those three, or say the pattern is a deliberate strict subset
   and name what it does not enforce.

9. **design:1258** — "Removals rename to a trash name and delete with `safeRemoveAll`."
   -> `safeRemoveAll` is unexported in `internal/workspace` (`snapshot.go:104`), which
   `configdir` sits below. Replace with: "`configdir` owns its own `safeRemoveAll`, which
   `internal/workspace` calls rather than keeping a second copy."

10. **design:454-456** — "`IsSafeHandle` exists for exactly this (\"callers use it to validate a
    captured short id before it becomes a CLI argument\")."
    -> The substance is right and the function is exactly as described, but the quotation is a
    paraphrase. The comment reads "It is the exported form callers use to validate a captured
    short id (isSafeHandle precedent) before it becomes a CLI argument -- e.g. `claude stop
    <ShortID>` in the continuation path" (`internal/watch/state.go:404-407`). Either quote it
    verbatim or drop the quotation marks.

11. **design:533-535** — "`internal/tui`'s sanitizer and every hand-rolled stripper here match
    Cc only, and the one adequate stripper is unexported inside `internal/cli`."
    -> Correct about `stripControlChars` (`internal/cli/session_from_hook_cmd.go:331-345`,
    unexported, C0 + DEL + C1) and about the conclusion. Wrong about `internal/tui`:
    `SanitizeDisplayString` (`internal/tui/sanitize.go:7-17`) is a regex over ANSI escape
    *sequences* — CSI, OSC, bare ESC — not a Cc matcher, so it leaves `\r`, `\b`, `\x07` and
    every other C0/C1 byte intact. It is inadequate for a different and worse reason than the
    design gives. Replace with: "`internal/tui`'s sanitizer strips ANSI escape sequences and
    leaves every other control byte, including `\r`; the hand-rolled strippers match Cc only;
    and the one with usable coverage is unexported inside `internal/cli`."

---

## Conclusion

**Must change before this ships.**

- **N1.** Charset-check the overlay directory name in `config.OverlayDir`, and stop asserting
  that a forge's rules keep `@` out of it. `niwa init --overlay acme/tools@swap` puts a live
  overlay clone exactly where another overlay's `@swap` working directory goes, and the two
  refreshes hold different locks, so nothing serializes them. Correct design:173-175 and
  design:977-978.
- **N2.** Replace `Journal.Recreated` with the `journal.Staging/snap` existence test. As
  written, rule 3 cannot detect the case it exists for and deletes the configuration it was
  added to protect, in what the design itself calls the ordinary condition.
- **N3.** Settle `<dir>.lock` the same way as `@swap`. An overlay named `acme/tools.lock`
  currently wedges every niwa command touching overlay `acme/tools`, with no self-repair — the
  H2 failure the revision just fixed, in the one sibling it did not move.
- **N4.** Route `ephemeralInstancePaths` through the locked store read, or narrow the "every
  current and future reader" sentence. PRD R15 names this reader explicitly.
- **N5.** Export the record validator and fix its declared type, or `internal/workspace`'s copy
  of the destroy argv stays the unguarded one and L11 is not closed.
- **N7.** Delete the Summary's "after the lock is released" clause for the journal; two other
  sections say the opposite and they are the ones that are right.
- **N6 and N10** are smaller but belong in the same pass: specify the journal's write as atomic
  and say what an unparseable one does; add `@{` to the branch pattern.

**Acceptable residual risk.**

The journal mechanism itself, once N2 and N6 are fixed, is the right shape, and its treatment of
older binaries mid-upgrade — write no journal, be invisible, let the old preflight clean up after
itself — is the best-argued part of the document. The threat-model boundary is now stated once
and honoured throughout, including the honest admission that a same-user process can forge a
journal and that this is accident prevention rather than a security control. The starvation
acceptance, the non-unix skip, the `Lstat`-then-use window on the instance directory, the kept
strays, and the single `config.Discover` repair per command are all argued acceptances with the
right caveats attached. N8, N9 and N11 through N15 are notes rather than gates, though N9 should
get the one-line caller-table edit while the table is open, and N13's corrected reasoning matters
because the `sessions` case is silent data loss rather than the promotion the design describes.

Three passes in, the mechanism has converged and the prose has not quite caught up. Of the two
High findings, one is a false claim about a parser in this repository and one is a logical hole
in a rule the same document describes correctly elsewhere — both are findable by reading the code
the design cites, and both are the same failure mode the first two passes named. The
encouraging difference is that this time every mitigation the design names *exists*; what fails
is a premise about what an existing parser accepts, and an invariant the new mechanism does not
actually maintain.
