# Architecture review (pass 2): DESIGN-session-store-teardown

Scope: the revised package graph — `internal/configdir`, `internal/testhook`,
`internal/safetext`, the `<dir>@swap` layout with its journal, and record
validation moved into `internal/worktree` — checked against the source in
`internal/{config,workspace,watch,worktree,cli,plugin}` and against
`docs/plans/PLAN-session-store-teardown.md`.

Method note: this environment has no shell, so `go list -deps` could not be run.
Every import claim below was verified by reading the import blocks of the
packages named. Where a claim depends on a package that does not exist yet
(`configdir`, `testhook`, `safetext`), it was checked against the constraint the
existing imports impose.

## Verdict

**FAIL** — 5 blocking findings. None requires re-architecting: the shape (one
package owning lock + layout + journal + recovery, resolution in `internal/cli`,
validation at the git call site in `internal/worktree`) is right and fits the
codebase. What fails is the exported surface of `internal/configdir`, two
leftovers from the pre-revision design that the revision made unreachable, one
signature that cannot compile as written, and a new leaf package that duplicates
a helper the design itself says already exists.

The import direction is sound. `internal/config` imports only `secret`, `vault`
and `source` today, so `config -> configdir -> testhook` adds no cycle;
`internal/worktree` imports only `internal/gitexclude`
(`internal/worktree/worktree.go:18`), so `worktree -> safetext` keeps it a leaf;
`internal/watch` imports only `internal/github`, so `watch -> configdir` is
downward. `internal/workspace` already imports `internal/worktree` in production
(`internal/workspace/apply.go:26`), which matters for finding 4.

## Blocking findings

### 1. The lock-ordering rank rule cannot be computed inside a leaf, and the reason given for it is not true

**Severity: Blocking.**

Design, Decision 1: "It also enforces an order: config directories are ranked
global, then overlay, then workspace root, and an acquisition that would take a
lock ranked at or below one this process already holds is refused the same way."
The Key Interfaces block gives `Acquire(dir string, mode Mode) (release func(),
err error)` — no rank, no kind, just a path.

To rank a path, `configdir` must know that
`$XDG_CONFIG_HOME/niwa/global` is the global config dir and
`$XDG_CONFIG_HOME/niwa/overlays/<name>` is an overlay. Those derivations live in
`internal/config/registry.go:403` (`GlobalConfigDir`) and
`internal/config/overlay.go:325-351` (`OverlayDir`). `internal/config` imports
`internal/configdir` in this design (`internal/config/discover.go` gains the
`RecoverMovedAside` branch), so `configdir` cannot import `config`. The only way
to implement the rule as specified is to re-derive the XDG layout inside
`configdir` — a second copy of the config-location rules, in the package the
design is adding specifically to end duplicated path knowledge.

The justification is also wrong. Design: "`Apply` touches all three in one
command, so without the rule two processes taking them in opposite orders would
each wait out the full bound and both fail." Lock-ordering prevents deadlock
between *simultaneously held* locks. `Apply` takes them sequentially, and the
design elsewhere guarantees no nesting at all ("The lock is taken only inside the
two entry points, whose callbacks never call another lock-taking function").
Sequential acquisitions in opposite orders cannot deadlock and cannot exhaust the
bound. The rule can therefore never fire in production; the plan's test for it
(`PLAN` Issue 2: "A test takes root-then-overlay and asserts the refusal, and
asserts the legal order succeeds") can only be written by doing the nesting the
design forbids.

**Fix:** delete the ordering rule from Decision 1, the Components entry, the Key
Interfaces comment and PLAN Issue 2's acceptance criterion. Keep the
nested-acquisition detector, which is a real misuse check and needs only the
process-local held-path set. If a future caller genuinely needs two locks at
once, add a caller-supplied kind then (`Mutate(dir string, kind Kind, fn ...)`),
because the caller always knows which of the three directories it asked for.

### 2. `HoldsSnapshot` and the copied marker constants are orphaned by the revision, and the frontmatter still states the rule they served

**Severity: Blocking.**

The revision replaced content-based recovery with the journal: "Recovery acts on
a journal, not on what a directory looks like... Recovery reads the journal and
acts only on the paths that journal names. There is no scan for look-alike
directories, and no inference from a name." Recovery rules 0-4 consult the
journal, the live directory and the staging lock. None reads a provenance marker.

Yet `configdir` still exports `HoldsSnapshot(dir) bool` (Components; Key
Interfaces line `func HoldsSnapshot(dir string) bool`), still "copies the two
marker filenames it tests for rather than importing them," and PLAN Issue 2
carries two acceptance criteria for both. No caller is named anywhere in the
design: the refresh's marker probe stays in `internal/workspace`, where
`provenanceMarkerExists` already lives (`internal/workspace/snapshotwriter.go:113`)
reading `ProvenanceFile` (`internal/workspace/state.go:39`).

This is the state-contract-drift shape: an exported symbol with no consumer, plus
a duplicated constant whose only protection is a drift test. The codebase's own
norm is the opposite — `sessionsDirName` (`internal/workspace/session_map.go:112-118`)
is a *named constant shared by both places* precisely because "a literal in both
would let them drift apart silently."

The frontmatter is also stale: `decision:` still says "Recovery decides by which
side holds the snapshot marker" (design line 21), which the body explicitly
refutes at lines 269-292.

**Fix:** remove `HoldsSnapshot`, the copied marker filenames and the drift test
from `configdir` and from PLAN Issue 2; leave the marker check in
`internal/workspace` where `ProvenanceFile` is defined. Rewrite the frontmatter
sentence to "Recovery acts on a swap journal written under the lock." If some
caller does still need the check below `workspace`, name it — and then move
`ProvenanceFile` down into `configdir` with `workspace.ProvenanceFile =
configdir.ProvenanceFile` as an alias (legal: `workspace` imports `configdir`),
rather than copying it. Do not copy `config.ConfigDir` / `config.ConfigFile`
(`internal/config/discover.go:10-15`) either; `config` imports `configdir`, so if
`configdir` ever needs them the constants move down and `config` aliases up.

### 3. Exported `Acquire` and `Recover` are a sanctioned bypass of the two entry points

**Severity: Blocking.**

The whole correctness argument for the entry points is: "Every caller goes
through one of the two, so 'every exclusive holder recovers first' and 'deletes
happen outside the lock' hold without each caller remembering them." The Key
Interfaces block then exports both primitives that skip those guarantees:

```go
func Acquire(dir string, mode Mode) (release func(), err error)
func Recover(dir string) (trash []string, err error)            // caller holds EX
```

`Recover`'s "caller holds EX" is a precondition the type system cannot express
and no caller can be checked against. No design-named caller needs either:
the refresh uses `Read`/`Mutate`/`NewStaging`, discovery uses
`RecoverMovedAside`, the stores use `Read`/`Mutate`. Leaving them exported means
the next writer added to the config directory — and this design puts every
mapping, watch and `instance.json` write on that path, so there will be a next
one — can take the lock without recovery and delete trash inside it, and review
is the only thing that catches it.

**Fix:** unexport both (`acquire`, `recover`). The exported surface becomes
`Mode`, `Timeout`, `LockPath`, `SwapDir`, `Mutate`, `Read`, `NewStaging`,
`RecoverMovedAside`. Update PLAN Issue 2's criteria, which currently describe
`Acquire`'s behavior as an exported contract, to describe it through `Mutate` and
`Read`.

### 4. `validateSessionRecord` as specified cannot be called by the caller the design requires to call it, and its parameter type does not exist

**Severity: Blocking.**

Key Interfaces:

```go
// internal/worktree (unexported)
func validateSessionRecord(instanceRoot string, r *SessionRecord) error
```

Two problems.

(a) The design requires `workspace.DefaultDestroySession` to use it:
"it is routed through the same validator rather than left as the one copy that is
still unguarded" (Decision 2), repeated in Components and in PLAN Issue 6. That
function lives in `internal/workspace/bootstrap.go:280`. An unexported function
in `internal/worktree` is unreachable from there. As written, the implementer
either exports it (fine — `internal/workspace/apply.go:26` already imports
`internal/worktree`, so the edge exists and is downward) or writes a third copy
of the validation, which is exactly the outcome the design says it is removing.
Note `bootstrap.go:314-318` deliberately keeps a local copy of
`findRepoInWorkspace` "to avoid inverting the dependency direction (worktree is a
leaf package)" — that comment is now out of date, and an implementer reading it
will take the copy route.

(b) `internal/worktree` has no `SessionRecord` type. The lifecycle record type is
`SessionLifecycleState` (`internal/worktree/worktree.go:268` returns it;
`session_lifecycle.go` defines it). And `DefaultDestroySession` does not have one
anyway — it parses its own inline `stateShape` struct
(`internal/workspace/bootstrap.go:290-295`) with `Repo`, `BranchName`,
`SessionID`, `WorktreePath`.

**Fix:** specify it as an exported, field-level validator both callers can reach:

```go
// internal/worktree
func ValidateRecordFields(instanceRoot, worktreePath, branchName string) error
```

and have `DestroySession` and `workspace.DefaultDestroySession` both call it.
Update the Components bullet for `bootstrap.go` to say the local
`findRepoInWorkspace` copy's rationale comment needs correcting, since
`workspace -> worktree` is an existing production edge.

### 5. `internal/safetext` duplicates a stripper the design says already exists, with no retirement plan, and strips at the wrong layer

**Severity: Blocking.**

The design justifies a whole new package by where the string is *composed*:
"`internal/worktree`, which composes the kept-branch warning, and `internal/cli`,
which prints the resolver's lines, can both import it... the one with the right
coverage is unexported in `internal/cli`." PLAN Issue 6 repeats: "The kept-branch
warning is composed inside `internal/worktree`, so the strip happens there."

Composition is not output. `BranchWarning` is a `json:"-"` field
(`internal/worktree/session_lifecycle.go:56-60`) returned to the caller; its only
two consumers in the repository are both in `internal/cli`
(`session_lifecycle_cmd.go:588-589` and `session_from_hook_cmd.go:219-222`).
Every value the design wants stripped therefore crosses exactly one output
boundary, and that boundary is in the package that already owns an adequate
stripper.

As written the change leaves the repository with two adequate strippers (the new
`safetext.Strip` and the existing unexported one in `internal/cli`) plus the
Cc-only `internal/tui` sanitizer and the hand-rolled ones the design counts. That
is one more instance of the pattern the design is complaining about, and it is
the one future contributors will copy.

**Fix:** pick one and say so. Either (a) drop `internal/safetext`, extend the
existing unexported `internal/cli` stripper to the Cf/U+2028/U+2029 block, and
strip at the two `BranchWarning` print sites and the resolver's lines — no new
package, and `internal/worktree` needs no import at all; or (b) keep
`internal/safetext` and state explicitly that it *replaces* the unexported
`internal/cli` stripper, which is deleted and its callers moved, so the repo ends
with one adequate stripper. (b) also needs a sentence on why pre-sanitizing a
data field inside `worktree` is acceptable given it is data, not output.

## Advisory findings

### 6. `Mutate(dir, fn func() error)` cannot implement its own documented behavior

**Severity: Advisory.** Design: "`configdir.Mutate(D, fn)` takes the lock
exclusive, runs recovery, calls `fn`, releases, then deletes any trash recovery
**or `fn`** produced." `Recover` returns `trash []string`, but `fn` is
`func() error` and has no way to report the trash it made — and the swap does
make some ("rename `prev-X` to `D@swap/trash-Y`"). The only way to close this is
a post-release scan of `SwapDir(dir)` for `trash-*`, which the interface block
does not mention and which sits oddly beside "recovery never scans." **Fix:**
state in Components that `Mutate` sweeps `SwapDir(dir)/trash-*` after release
(name-based inference is fine *inside* `@swap`, which is niwa's own namespace),
or change the callback to `func() (trash []string, err error)`.

### 7. The `Staging` type has no specified surface

**Severity: Advisory.** `NewStaging(dir) (*Staging, error)` is the only mention;
the swap needs the `snap/` path, and something must release the staging lock and
remove the directory ("afterwards it removes its staging directory"). The
`Staging`/`Journal` split itself is right — `Staging` is per-refresh live state,
`Journal` is the on-disk record of an in-flight swap, and only the second needs
to survive a crash. **Fix:** add the three methods to Key Interfaces:
`func (s *Staging) SnapDir() string`, `func (s *Staging) Path() string`,
`func (s *Staging) Close() error`. Also decide whether `Journal` should be
exported at all: it is exported while `writeJournal`/`readJournal`/`clearJournal`
are not, so no other package can do anything with the type.

### 8. `SaveState`'s "refreshed config directory" test should name the helper that already exists

**Severity: Advisory.** Design Decision 4: "a sibling `.niwa.lock` exists, or the
directory is a workspace root or a single-instance root." PLAN Issue 4 words it
differently: "a sibling `.niwa.lock` exists, or the directory carries
`.niwa/workspace.toml`." The second half of both is already implemented as
`isWorkspaceRoot` (`internal/workspace/state.go:321-324`), and a single-instance
root *is* a workspace root, so that clause is redundant. The `.niwa.lock`
disjunct is the risky part: it makes `SaveState`'s locking behavior depend on
whether a lock file happens to exist beside the target, so an instance directory
that ever acquires one silently changes write path. **Fix:** define the predicate
once as `isWorkspaceRoot(dir)` and say so in both documents; drop the
`.niwa.lock` disjunct, or justify a case where a refreshed config directory has
no `workspace.toml`.

### 9. The component diagram misplaces `internal/watch`

**Severity: Advisory.** The diagram puts `watch` above `workspace` in a single
chain. `internal/watch` imports only `internal/github`; it depends on neither
`workspace` nor `config`. It spells `.niwa` itself
(`internal/watch/state.go:19,23,161`) and `MkdirAll`s it at `state.go:161-163` —
which is the recreate hazard this change removes. **Fix:** draw `watch` as a
sibling of `workspace`, both over `configdir`, and add a line to PLAN Issue 3
that `internal/watch` must not gain a `workspace` import while being routed
through `Mutate` (the tempting shortcut is `workspace.StateDir`).

### 10. The `internal/plugin` carve-out is in the design but not in the plan

**Severity: Advisory.** Design: "`internal/plugin`'s installer runs a
`.next`/`.prev` rotation of its own that this change does not touch." PLAN
Issue 3 has the criterion "No literal config-directory suffix remains in
`internal/workspace`, `internal/watch` or `internal/config`" with no mention of
plugin. An implementer greps `.next`/`.prev` repo-wide, finds
`internal/plugin/installer*.go`, and either reroutes it (scope creep into a
rotation with different semantics) or leaves it and wonders. **Fix:** add
"`internal/plugin`'s installer rotation is explicitly out of scope and keeps its
own names" to Issue 3's criterion.

## Answers to the five questions

**1. Dependency direction.** Verified by reading imports (no shell available).
No cycle, no inversion in the proposed graph, with one exception: the rank rule
of finding 1, which is only implementable by duplicating `internal/config`'s XDG
layout inside a package `config` imports. `configdir` sitting below the
marker-defining packages is a true constraint for `config.ConfigDir`/`ConfigFile`
(because `config -> configdir`), but *copying* is the wrong response and, per
finding 2, is not needed at all now that recovery is journal-driven. If a
constant is ever genuinely needed in both places, move it down to the lowest
package and alias upward, which is what `agentplan.TreeMarkerFileName()`
(`internal/agentplan/skills.go:41-46`, consumed at
`internal/workspace/applyplan.go:342`) already does in this repository.

**2. Layering and placement.** `internal/configdir` owning the lock, the layout,
the journal and recovery together is the right call, not too much: they are one
protocol, and splitting the lock from the layout is precisely the two-owner drift
the pass-1 review blocked on. Minus `HoldsSnapshot` and the rank rule it is
cohesive. Record validation inside `internal/worktree` is consistent with how
this codebase assigns responsibility: `worktree` already builds the worktrees
path it must contain against (`worktree.go:195`), already builds the git argv
(`worktree.go:330,344`), and validation in `internal/cli` would guard values the
callee re-reads from disk and would leave destroy-by-id and the WorktreeRemove
hook unguarded, as the design says. `internal/safetext` is the one placement that
is not justified — see finding 5.

**3. Interface contracts.** `Mutate`/`Read` as the *only* entry points does not
hold as written: `RecoverMovedAside` is a legitimate third (discovery cannot
reach `workspace`, and the design bounds it correctly), but `Acquire` and
`Recover` are an unforced fourth and fifth (finding 3), and `Mutate`'s signature
cannot satisfy its own trash contract (finding 6). The `Staging`/`Journal` split
is sensible; `Staging` just needs a surface (finding 7).

**4. Coupling.** The concentration is appropriate — ordering is the fix, and the
only way to order is a single gate — and the design already pushes the fetch and
the deletes outside the lock, which is what keeps the gate cheap. One structural
risk is not handled: 16 production `config.Discover` call sites
(`internal/cli/{session_lifecycle_cmd,status_audit,instance_from_hook,watch,completion,go,status_check_vault,status,create}.go`,
`internal/workspace/{destroy,scope×2,cwd_classify×3}.go`) gain a failure mode
they did not have, and several of them — completion, the SessionStart hook,
`niwa watch` — should degrade silently rather than surface a timeout. If the
timeout is a plain `fmt.Errorf`, each call site will invent its own handling and
they will diverge. **Recommendation:** specify an exported
`configdir.TimeoutError` (or `ErrLockBusy` for `errors.Is`) in Key Interfaces,
and say in Components which of the 16 sites treat it as "no candidates" rather
than as an error. The other concentration risk — recovery failing every write on
a directory — is already handled correctly by making recovery warnings
non-fatal.

**5. Plan vs design.** The plan tracks the revision well: the `@swap` layout,
the journal, the three packages, the `preserve*` `Lstat` change, the
`internal/worktree` validation and the `bootstrap.go` third copy are all in the
right issues, and the edges (1→2; 2→3,4; 3,5→6; 3,6→7) are consistent with the
outlines. Four things to change:

- Issue 2's criteria for `HoldsSnapshot`, the marker-copy drift test and the
  lock-ordering test go away with findings 1 and 2.
- Issue 2 bundles `internal/safetext` under a `feat(configdir)` title. `safetext`
  needs neither `testhook` nor `configdir`, and its consumers do not arrive until
  Issue 6. Move it to wherever finding 5 lands it.
- **The decomposition should change shape.** The revision created a new seam —
  validation in `internal/worktree` + `internal/workspace/bootstrap.go` — and the
  plan's own strategy is "the seams are the decomposition," but Issue 6 swallows
  it whole alongside the CLI resolver. The validation half depends on nothing in
  the lock work; the resolver half needs Issues 3 and 5. Split into
  `6a fix(worktree): validate lifecycle records before git` (blocked by 2 only,
  can run parallel with 3/4/5) and `6b feat(cli): resolve destroy by session`
  (blocked by 3, 5, 6a). Issue 6 is currently the largest in the plan by
  acceptance-criteria count, and the split makes both halves reviewable against
  one package each.
- Issue 7's four-parallel-dispatch scenario should also be blocked by Issue 4.
  `SaveState` still calls `os.MkdirAll` on `.niwa`
  (`internal/workspace/state.go:298-301`) until Issue 4 removes it, and that is
  the exact MkdirAll the design names as the swap-breaking hazard, so the
  scenario is flaky at Issue 3 + 6 without 4. Change the edge to 3, 4, 6 → 7.

One cosmetic hole: the plan has an empty `## Dependency Graph` heading (line 606)
immediately followed by `## Implementation Sequence`. Either fill it or remove it.
