---
schema: design/v1
status: Current
problem: |
  `niwa worktree destroy` cannot resolve the ids a developer actually holds --
  a session id or an agent handle -- so its merged-branch check never runs
  before `niwa reap` deletes the instance. Run from a workspace root it is
  worse: the root carries its own instance.json, so every worktree subcommand
  reads the root's session mapping store as though those files were worktree
  lifecycle records.
decision: |
  Rebuild `discoverInstanceRoot` on `workspace.ClassifyCwd` so one resolver
  refuses a multi-instance root for all nine callers, and add a destroy
  resolver in `internal/cli` that matches a value against one read of the
  session mapping store as a session id, a handle or an 8-hex prefix, refusing
  ambiguity. Validate a lifecycle record's own path and branch in
  `internal/worktree`, beside the git calls that consume them.
rationale: |
  The root refusal belongs in the resolver the nine callers already share, so
  completion and the WorktreeRemove hook inherit it without per-command checks.
  Resolution stays in `internal/cli` because it owns exit codes and output,
  while record validation goes next to the git calls so destroy-by-id and the
  hook are covered by the same rules. Containment never rests on the mapping
  read: the resolver acts on a directory instance enumeration produced, not on
  a string a mapping recorded.
upstream: docs/prds/PRD-session-store-teardown.md
user_visible_surface: true
---

## Status

Current

## Context and Problem Statement

niwa keeps two kinds of session record, and at teardown they never meet.

A dispatched session's **mapping** lives at the workspace root, under the
configuration directory, and is the only durable record of which instance a
session became; `niwa list` and the reaper read it. A niwa-managed worktree's
**lifecycle record** lives inside its instance and is what
`niwa worktree destroy` reads before removing a worktree and deleting its
branch only when that branch is merged.

The ids a developer or a cleanup script holds for a finished worker are the
agent session id and the short handle the agent shows. Neither appears in the
lifecycle store, which is keyed by eight hex characters that surface nowhere
else. So destroy rejects the id, the merged-branch check never runs, and the
`niwa reap` that follows deletes the instance and any unmerged branch in it.
A failed lookup and a passed check look identical (#292).

Resolution makes it worse rather than catching it. `discoverInstanceRoot`
walked up for `.niwa/instance.json`, and a workspace root carries one of its
own because `niwa init` persists init-time state there for `niwa create` to
read. The walk stopped at the root, so every worktree subcommand treated the
root as an instance and read the mapping store as though it held worktree
records -- two stores with different shapes and different key spaces.

A third problem sits behind both. A lifecycle record is a JSON file any
process running as the user can write, and nothing validated its worktree path
or branch name. Both flow into git argv positions with no containment test and
no end-of-options marker, and the uncommitted-changes guard reported a missing
path as clean. Teardown by session raises what that is worth: one command now
reaches every active record in an instance rather than the single id a
developer typed.

**Scope.** This design originally also covered ordering concurrent refreshes
of a configuration directory (#297). The work split during implementation once
it was clear the two halves meet at exactly one call site -- teardown's single
read of the mapping store, which ships unlocked here. The concurrency
decisions were removed from this document rather than left describing
mechanisms that do not exist; #297 authors its own design.

## Decision Drivers

- **Teardown contract (PRD R1-R10).** Session id and handle in addition to the
  worktree id and `--by-path`; the same result from the root, any instance, or
  a worktree; refusal on any ambiguity; a fixed outcome table of exit codes
  with stable output; and, at a multi-instance root, no subcommand treating the
  root as an instance.
- **Untrusted record contents.** Mapping fields and lifecycle records are
  written by processes niwa does not control and flow into git arguments and a
  terminal.
- **Leaf discipline.** `internal/worktree` imports only `internal/gitexclude`
  among internal packages, and `internal/cli` imports it. Anything the leaf
  needs cannot come from the CLI.
- **Existing behavior (R11).** Apart from the sanctioned changes, invocations
  keep their output and exit codes, and `go.mod` gains no module.
- **Deterministic evidence (R12).** The pre-fix failure must be demonstrable.

## Considered Options

### Decision 1: How `niwa worktree destroy` resolves its target, and where the root refusal lives

This decision answers the teardown contract. Nine callers find their records
through `resolveInstanceRoot`: create, apply, destroy and list, attach and
detach, `niwa go <repo> <id>`, completion, and the WorktreeRemove hook. What it
settles is where the resolution and the root refusal live.

Key assumptions: `NIWA_INSTANCE_ROOT` stays a verbatim override and is never
refused (niwa sets it only to an instance root); `destroy --by-path` at a
multi-instance root keeps today's result, taking R10's "as today" literally;
`niwa worktree list --json` at the root still prints `[]` on stdout, since R11
keeps existing output.

#### Chosen: A destroy resolver in `internal/cli`, with the root refusal centralized in `resolveInstanceRoot`

`discoverInstanceRoot` is rebuilt on `workspace.ClassifyCwd`: inside a
worktree or instance it returns the instance; at a workspace root it returns
the root only in the single-instance layout, and otherwise returns the
sentinel `errAtWorkspaceRoot`, whose text is R10's line. The layout test is a
new `workspace.IsSingleInstanceLayout(root)`: no child instance, and the root's
`instance.json` names an instance. The second condition matters because every
registered `niwa init` writes a root `instance.json` without an instance name,
so a freshly initialized workspace, or one whose instances were all reaped,
would otherwise count as single-instance and have worktrees created inside the
rotated directory.

`internal/cli/apply.go` keeps its own inline check and deliberately does **not**
adopt the helper, which an earlier draft of this design got wrong. The two look
like one question and are not. The worktree resolver asks "may I create and
destroy worktrees in this root?", where a freshly initialized root must answer
no. Apply asks "is the root the thing I should apply to?", and for a root with
state and no children the answer is yes precisely when it is freshly
initialized: that is the bootstrap case, `niwa init` followed by
`niwa apply <name>`, where `instance_name` has not been written yet and the
per-instance pipeline still has to run the lazy snapshot conversion and the
`claude_permissions` write. Sharing the stricter predicate skipped both
silently, which two `@critical` functional scenarios caught.

Because `Execute` prints plain errors verbatim with exit 1,
create, apply, attach, detach and `niwa go` produce R10's behavior with no
edit (for `niwa go` only the message changes, and it gains no session
resolution), completion already turns a resolver error into no candidates, and
the hook already logs it and exits 0. Only `worktree list` adds a branch that
prints the redirect and exits 0. Because the rebuild changes resolution for
every caller inside instances too, a table test pins it across every layout:
an instance root, a repository clone inside it, a niwa worktree under
`<instance>/.niwa/worktrees/`, a worktree Claude Code created under
`<repo>/.claude/worktrees/`, the multi-instance root, a freshly initialized
root with no instance, the single-instance root, a directory outside any
workspace, and a run with `NIWA_INSTANCE_ROOT` set.

Destroy's positional form gets its own resolver in a new
`internal/cli/worktree_destroy_resolve.go`:

- `resolveDestroyScope` classifies the working directory into an instance
  directory (inside an instance or worktree, or a single-instance root) and a
  workspace root.
- `loadMappingsForDestroy` makes the one mapping-store read: the locked
  `ListSessionMappings`, filtered to entries whose `session_id` field is a valid
  UUID, so the single-instance layout's lifecycle files, which share that
  directory, never become fake mappings. It applies the existing exported
  `watch.IsSafeHandle` to the `handle` field too. A handle is matched as a string
  and then printed back in R9's ambiguity line, and it comes from the same
  untrusted file as everything else in the mapping; `IsSafeHandle` exists for
  exactly this ("callers use it to validate a captured short id before it becomes
  a CLI argument") and allows only `[A-Za-z0-9_-]{1,128}`. A mapping whose handle
  fails it keeps its session-id match and loses its handle match, which closes
  the injection path into stderr without dropping the record.
- `matchMappings` applies R4 (exact session id, recorded handle, or a unique
  8-hex prefix for a mapping with no handle), deduplicated by session id so a
  Codex mapping whose handle is its id is one match.
- `resolveDestroyTarget` is pure: given the scope, the value and the snapshot,
  it returns a worktree id or one mapping, or an `ExitCodeError` with code 3
  (no match) or 4 (ambiguity, with the R5 guidance when a worktree is among
  the matches). `--by-path` bypasses the resolver but shares its no-match
  error, so a path resolving to no worktree exits 3 rather than today's 1; its
  message does not change. Both routes reach the same outcome, so they must not
  differ by which one located the target. Codes 3 and 4 are `destroy`'s own:
  `attach` already exits 3 when the attach lock is held and `detach --force`
  exits 4 after killing a live holder, and per-subcommand code tables are
  already this codebase's pattern -- `niwa init` reuses 3 and 4 for meanings of
  its own. The worktree guide gains a second table bound to `destroy`, and
  `destroy --help` names its codes inline the way `attach` does. Nothing in the
  repository branches on a worktree-family exit code today, so the moved code
  breaks no caller; it still ships announced as a behavior change rather than
  folded in as a fix.
- `checkSessionInstance` applies R7's ordered checks against the same
  snapshot and returns the enumerated instance directory, not the recorded
  string: instance location (lexically, and against the root's resolved
  path), existence (`Lstat`; missing means the exit-0 "no longer exists"
  outcome), membership among `EnumerateInstances`, and R6 through a new
  `workspace.NewestMappingPerInstance` that `niwa list` also adopts.
  `EnumerateInstances` returns lexical joins of whatever root it was handed and
  resolves nothing, so both sides of the membership comparison are resolved
  before it is made; comparing a symlink-resolved recorded path against an
  unresolved set would mismatch wherever the workspace root itself sits under a
  symlink, which on macOS is the default for anything under `/tmp`. R6's
  newest-mapping rule is a usability rail, not one of these checks: it reads the
  untrusted `created` field, so it keeps teardown off a superseded session but
  bounds nothing against a crafted store.
- `destroySessionWorktrees` lists the instance's lifecycle records, keeps the
  active ones in worktree-id order, re-checks the instance directory with
  `Lstat`, and calls `worktree.DestroySession` for each, printing R9's lines,
  continuing past refusals, and returning exit 1 if any worktree was refused.

**The record's own fields are validated where they are used, which means
`internal/worktree` changes after all.** A lifecycle record is a JSON file any
process running as the user can write, and today nothing checks its path or
branch fields: `DestroySession` passes `worktree_path` to
`git worktree remove --force` and the record's branch name to `git branch -d`
as positional arguments, with no containment test and no end-of-options marker,
and the uncommitted-changes guard reports a missing or empty path as clean,
so a record naming another tree of the same repository passes a guard about a
different directory. Teardown by session multiplies what that is worth: one
command from the workspace root now reaches every active record in an instance
rather than the single id a developer typed.

Putting those checks in `internal/cli` would guard values the callee re-reads
from disk for itself, and would leave destroy-by-id and the WorktreeRemove hook
unguarded, so they go next to the git calls instead. `DestroySession` requires
the record's worktree path to be non-empty and, after resolution, to lie
under the instance's own worktrees directory, and treats a missing worktree
directory as a refusal rather than as a clean tree. The dirty-tree guard fails
closed on *any* `stat` error, not only on `ENOENT`: today a permission error or
an `ELOOP` on a symlink loop leaves the error untested and falls through to the
git call. Containment compares symlink-resolved forms, so a symlink planted
inside the worktrees directory cannot redirect teardown. This narrows rather
than closes the window: a path swapped between the check and the git call would
still be followed, and the design says so rather than claiming a closure.

The branch name gets an exact pattern, not a judgement call, because it is the
only barrier between a wholly unvalidated JSON field and two git argv positions
plus a copy-pasteable command line. It must be a valid ref under
`git check-ref-format`'s rules as applied here: non-empty; valid UTF-8; no
non-printable rune; no space; none of `~`, `^`, `:`, `?`, `*`, `[`, `\`; no
`..`; no leading `-`; no trailing `.lock`; no leading or trailing `/` and no
`//`; not the single character `@`; and no `@{` anywhere, since
`git branch -d -- '@{-1}'` deletes the previously checked-out branch. It is
passed after `--`.

"No non-printable rune" is deliberately stricter than the ASCII control bytes
this rule started as. It also rejects the bidi overrides, which is worth having
at the validation layer and not only at the print boundary: the same field
reaches a git argv, where the print-boundary stripper never runs. It refuses a
handful of names git itself would accept, such as one carrying a non-breaking
space, and that is acceptable because niwa generates branch names from
`EffectiveBranchName` and never produces one — the test asserts every shape the
tree actually writes still passes, so only a record niwa did not write is
refused.

The worktree path is checked by refusing any `..` component outright and then
comparing symlink-resolved forms of both sides. Cleaning `..` lexically would be
wrong whenever something along the path is a symlink, and a record carrying one
was not written by `CreateSession` anyway; resolving both sides then keeps a
symlinked instance root — the default for anything under `/tmp` on macOS — from
producing a false refusal, while a symlink planted inside the worktrees
directory cannot redirect teardown. Opening a root handle on the instance
directory (`os.Root`) would narrow the remaining check-to-use window further and
is left as a later hardening; it is not what ships here.

The same argv exists a third time, in `workspace.DefaultDestroySession`, the
`niwa init --bootstrap` rollback, which parses the record with its own inline
struct and runs `git branch -D` with no validation and no `--`. Its only caller
acts on a record the bootstrap just wrote, so the exposure is small, but the
reasoning for putting the checks beside the git calls applies there identically
and it is routed through the same validator rather than left as the one copy
that is still unguarded. That is why the validator is exported and takes the
three fields rather than a record type: `internal/workspace` already imports
`internal/worktree`, but it parses its own struct there, so a rule keyed to
`SessionLifecycleState` would not reach it.

Every value interpolated into an outcome, refusal or ambiguity line has control
characters stripped rather than being quoted, so the literal shapes R9 pins are
preserved. No existing helper does this as it stands: the dispatch path
deliberately *refuses* rather than strips (and its comment says so),
`internal/tui`'s sanitizer and every hand-rolled stripper here match Cc only.
The closest one, `stripControlChars` in `internal/cli`, already covers C0, C1
and DEL, and this change extends it to U+2028, U+2029 and the Cf bidi and
zero-width block and makes it the CLI's single stripper for untrusted values.
Stripping happens at the print boundary rather than where the string is
composed, which is what keeps `internal/worktree` a leaf: both consumers of the
record's `BranchWarning` are in `internal/cli` -- one prints it, one wraps it
into an error -- so no new package and no new import is needed. The Cf coverage
is the point: U+202E and U+200B pass every Cc-only stripper in this repository,
and the
line that most needs the protection is the kept-branch warning, which carries a
record-supplied branch name into a paste-ready `git branch -D` line and which
teardown by session now emits once per worktree instead of once per command.
The branch pattern above already rejects most of what would reach it; the
stripper is the second layer, and the mapping handle's `IsSafeHandle` filter is
the third.

A worktree-only match takes today's `DestroySession` path byte for byte.
`--force` with a session id or handle is refused as a usage error (exit 2);
forcing stays available one worktree at a time. Three behavior changes for
existing invocations are called out in the pull request: a worktree id matching
nothing, or a non-hex value, exits 3 instead of 1 (sanctioned by the PRD); a
destroy by worktree id now reads the root mapping store, unlocked, so a read
taken while that directory is being refreshed can report no match; and a
lifecycle record whose path
or branch field fails the new validation is refused rather than acted on, which
changes nothing for records niwa wrote.

#### Alternatives Considered

**The same resolver, with the root refusal added per command**: a smaller
change to `session.go`. Rejected because it takes six near-identical checks,
still leaves completion and the hook resolving the root as an instance unless
they get checks too, and lets the next worktree subcommand inherit the bug by
default.

**A `--session <id>` flag, with the positional argument staying worktree-id
only**: avoids the collision by syntax. Rejected because R1 and R10 require
the positional argument to accept the session id and handle, the PRD already
settled collisions by refusal, and scripts would have to pick a flag by the
shape of an id.

**Resolution inside `internal/worktree` behind a mapping-lookup interface**:
keeps orchestration next to `DestroySession`. Rejected because the interface
would carry the workspace-root location rule, instance enumeration and
newest-mapping ordering with a single implementation in `internal/workspace`,
keeping the leaf rule only on paper while pushing exit-code and output policy
into a package that has never owned it.

## Decision Outcome

### Summary

`niwa worktree destroy <value>` classifies where it is running, reads the
mapping store once, and matches the value as a worktree id of the current
instance or a session by id, recorded handle, or unique 8-hex prefix. No match
exits 3; more than one candidate exits 4 and names them. A single session runs
R7's path checks (a missing instance directory exits 0, "no longer exists"),
the newest-mapping rule, and then destroys each active worktree in worktree-id
order through the guarded `DestroySession`, continuing past refusals and
exiting 1 if any refused; `--force` with a session is refused. `DestroySession`
validates the record's own worktree path and branch before either git call. The
other worktree subcommands at a multi-instance root now say where to run
instead of treating the root as an instance; `worktree list` there exits 0.

### Rationale

The root refusal goes in the resolver all nine callers share, so completion and
the WorktreeRemove hook are fixed without touching either. Resolution stays in
`internal/cli`, which already owns exit codes and output, while the record's
own fields are validated in `internal/worktree`, next to the git calls that
consume them -- putting those in the CLI would guard values the callee re-reads
from disk for itself and would leave destroy-by-id and the hook unguarded.

Containment deliberately does not rest on the mapping read. The resolver hands
`DestroySession` a directory that instance enumeration produced, never the
string a mapping recorded, so a stale, partial or crafted store can cost a
refusal but cannot point teardown somewhere the workspace does not hold as an
instance. That is the property that let #297 be split off rather than blocking
this work.

## Solution Architecture

### Overview

One new file in `internal/cli` holds the resolver; `internal/cli/session.go`
gains the classifier-based resolution and its sentinel; `internal/worktree`
gains record validation; `internal/workspace` gains two small helpers.

### Components

```
internal/worktree (leaf)     DestroySession + ValidateRecordFields
        ^
internal/workspace           session_map.go (NewestMappingPerInstance),
        ^                    state.go (IsSingleInstanceLayout), bootstrap.go
internal/cli                 session.go, worktree_destroy_resolve.go,
                             session_lifecycle_cmd.go, list.go
```

- **`internal/cli/session.go`**: `discoverInstanceRoot` rebuilt on
  `ClassifyCwd` and `IsSingleInstanceLayout`; the `errAtWorkspaceRoot`
  sentinel, whose text is R10's line.
- **`internal/cli/worktree_destroy_resolve.go`** (new): the six resolver
  functions of Decision 1.
- **`internal/cli/session_lifecycle_cmd.go`**: `runSessionDestroy` routes the
  positional value through the resolver and refuses `--force` with a session;
  `runSessionLifecycleList` handles the sentinel with exit 0; the widened
  `stripControlChars` runs at the print boundary.
- **`internal/cli/list.go`**: adopts `NewestMappingPerInstance`.
  `internal/cli/apply.go` is deliberately unchanged -- its single-instance
  question is not the resolver's, and sharing the stricter predicate skipped
  the bootstrap path's snapshot conversion and posture write.
- **`internal/workspace/state.go`**: `IsSingleInstanceLayout(root) bool` --
  no child instance, and the root's `instance.json` names an instance.
- **`internal/workspace/session_map.go`**:
  `NewestMappingPerInstance([]SessionMapping) map[string]SessionMapping`, keyed
  by cleaned instance path, latest `Created`, first-in-session-id-order on ties.
- **`internal/worktree/worktree.go`**: `ValidateRecordFields`, called by
  `DestroySession` before either git call; the dirty guard fails closed on any
  `stat` error.
- **`internal/workspace/bootstrap.go`**: `DefaultDestroySession`, the
  `niwa init --bootstrap` rollback and a third copy of the same argv, routes
  through the same validator.

### Key Interfaces

```go
// internal/workspace
func NewestMappingPerInstance(ms []SessionMapping) map[string]SessionMapping
func IsSingleInstanceLayout(root string) bool

// internal/worktree
// Exported because workspace.DefaultDestroySession parses its own struct and
// must reach the same rules; it takes fields rather than the record type.
func ValidateRecordFields(instanceRoot, worktreePath, branchName string) error

// internal/cli (unexported)
var errAtWorkspaceRoot error
func resolveDestroyScope() (destroyScope, error)
func loadMappingsForDestroy(workspaceRoot string) ([]workspace.SessionMapping, error)
func matchMappings(ms []workspace.SessionMapping, v string) []workspace.SessionMapping
func resolveDestroyTarget(s destroyScope, v string, ms []workspace.SessionMapping) (destroyTarget, error)
func checkSessionInstance(root string, m workspace.SessionMapping, all []workspace.SessionMapping) (instanceDir string, gone bool, err error)
func destroySessionWorktrees(cmd *cobra.Command, instanceDir string, m workspace.SessionMapping, git worktree.GitInvoker) error
```

No on-disk format changes, and no new module.

### Data Flow

A teardown by session: classify cwd; read the mapping store once; match; R7's
checks against that same snapshot, yielding the enumerated instance directory;
for each active worktree of that instance, re-`Lstat` the directory and call
`DestroySession`, which validates the record's own worktree path and branch
before it runs git. No directory lock is taken: in the multi-instance layout,
lifecycle records live in the instance's own `.niwa/`, which no refresh
rotates.

## Implementation Approach

Four commits in one pull request.

### Phase 1: Root refusal

`IsSingleInstanceLayout`; `discoverInstanceRoot` rebuilt on `ClassifyCwd` with
a layout table test across nine cwd shapes; the `worktree list` branch.
Independent of everything else.

### Phase 2: Record validation

`ValidateRecordFields` and its table tests; `DestroySession` calls it and
passes the branch after `--`; the dirty guard fails closed on any `stat` error;
the bootstrap rollback routes through the same validator. Independent, and the
one commit that refuses input today's code accepts.

### Phase 3: Teardown resolution

`NewestMappingPerInstance` and `list.go`; the resolver and `runSessionDestroy`;
`IsSafeHandle` on the handle field; the widened `stripControlChars`. Depends on
Phases 1 and 2.

### Phase 4: Functional coverage and docs

Scenarios for destroy by session id and by handle from the workspace root, the
unmerged-branch case, and the root contract; `docs/guides/worktree.md` for the
target forms, the outcome table and the root behavior. Depends on Phase 3.

## Security Considerations

niwa runs as the invoking user with no elevated privilege, and this design adds
no operation the user couldn't already perform. The questions are whether
crafted or corrupt local files can make niwa act outside the workspace, and
whether the new files widen what other users can see or block.

**The boundary, stated once, because the rest of this section depends on it.**
A process running as the invoking user with write access to the workspace root
is outside the threat model. Such a process can rewrite
`.niwa/workspace.toml` directly, so nothing niwa does to its own auxiliary paths
changes what it can reach, and a defense that assumed otherwise would be
decoration. What *is* in the model is the content of files niwa parses and then
uses as arguments or prints: mapping fields and lifecycle records flow into git
argv positions and into a terminal, and those are attacker-influenced in the
ordinary case of a buggy or hostile tool writing a record, not only under a
deliberate attack. That distinction is what the
rest of this section turns on.

**Destroy input and mapping contents.** The value passed to
`niwa worktree destroy` is only compared as a string against fields of the
loaded mappings and against worktree ids of the current instance; it never
becomes a path until a lifecycle read that first requires eight lowercase hex
characters. Mapping files can be written by any process running as the user,
so their fields are untrusted. The resolver keeps only mappings whose
`session_id` field is a valid UUID, and drops the `handle` match for any mapping
whose handle fails the existing `watch.IsSafeHandle` (`[A-Za-z0-9_-]{1,128}`),
which is exported for exactly this purpose. It accepts a recorded
`instance_path` only if it is lexically a direct child of the workspace root
other than `.niwa`, exists, and is one of the directories `EnumerateInstances`
returns -- with both sides of that comparison resolved, since
`EnumerateInstances` returns lexical joins and resolves nothing, and comparing a
resolved path against an unresolved set mismatches under a symlinked root. That
membership test is the containment invariant, and it holds only because the
enumeration returns direct children of the workspace root: a mapping naming a
directory elsewhere is refused because it is not in the set, not because the
string was inspected. The path handed to `DestroySession` is that enumerated
directory, not the recorded string, and it is re-checked with `Lstat` right
before each destroy. That check narrows the window rather than closing it -- a
directory swapped for a symlink between the check and the git call would still
be followed -- which is why the record's own fields are validated at the point
of use rather than trusted because the instance path was checked. R6's
newest-mapping rule is not part of this: it reads the untrusted `created` field,
so it is a usability rail against acting on a superseded session and bounds
nothing against a crafted store.

Values interpolated into R9's lines have control characters stripped rather than
quoted, because R9 fixes those lines' literal shapes and quoting would change
them. No stripper in the repository covers the job as it stands: the dispatch
path deliberately refuses rather than strips, and every existing stripper is
Cc-only, which leaves U+202E and U+200B intact. `internal/cli`'s existing
`stripControlChars` is widened to U+2028, U+2029 and the Cf bidi and zero-width
block and becomes the CLI's single stripper for untrusted values. It runs at the
print boundary: the kept-branch warning is composed in `internal/worktree` and
carries a record-supplied branch name into a paste-ready `git branch -D` line
emitted once per worktree, but both of its consumers are in `internal/cli`, so
stripping there covers it without giving a leaf package a new import.

**Scope of teardown by session.** Teardown by session applies the guards
`niwa worktree destroy` already has to each active lifecycle record of one
instance, and never removes the mapping, the instance or its clones. Those
guards were written for records niwa itself wrote, so this design adds the
validation they assumed: before any git call, `DestroySession` requires the
record's worktree path to be non-empty and to resolve under the instance's
worktrees directory, requires the branch name to match the exact ref pattern in
Decision 1 -- non-empty; no byte below 0x20 and no DEL; no space; none of `~ ^ :
? * [ \`; no `..`; no leading `-`; no trailing `.lock`; no leading or trailing
`/` and no `//`; not the single character `@` -- and passes it after `--`. A
record whose worktree directory is missing is refused rather than reported clean;
today the uncommitted-changes guard fails open on a missing tree, and it now
fails closed on any `stat` error rather than only on `ENOENT`, since a permission
error or an `ELOOP` currently falls through to the git call too. The same
validation is applied to `workspace.DefaultDestroySession`, the
`niwa init --bootstrap` rollback, which is a third copy of the same argv with no
checks and no `--`. `--force` is refused with a session id or handle (usage error,
exit 2); forcing stays available one worktree at a time through the worktree id
or `--by-path`, so a mistyped or prefix-matched id can't discard a whole
instance's uncommitted work and unmerged branches.

**Dependencies.** No module is added, and no new package: the resolver is one
file in `internal/cli`, and the stripper is an existing unexported function
there, widened.

## Consequences

### Positive

- Teardown accepts the ids developers and scripts actually hold, from anywhere
  in the workspace, with a stable, script-readable outcome contract, and every
  other worktree subcommand stops treating the root as an instance.
- `DestroySession` stops trusting the record it just read: a crafted or corrupt
  lifecycle record can no longer point git at a path outside the instance, pass
  a branch name that reads as a flag, or slip past a dirty-tree guard that
  today returns clean for a missing worktree.
- The fix lands in the resolver nine callers share, so completion and the
  WorktreeRemove hook are covered without per-command checks.
- The pre-fix failure is demonstrable end to end, from binaries built at the
  parent commit and at the fix.

### Negative

- A destroy by worktree id inside an instance now reads the root mapping store,
  which it did not before.
- That read is unlocked, so a read taken while the root's configuration
  directory is being refreshed can report no match, and can miss an ambiguity
  it would otherwise refuse. The newest-session rung reads the same snapshot,
  so a missed newer mapping can let a superseded session through.
- The instance directory is re-checked with `Lstat` immediately before each
  destroy, which narrows the swap window rather than closing it.
- `internal/worktree` stops being an unchanged leaf: record validation lands
  there, so a record written by an older niwa is read by stricter code.
- The tree now holds three distinct notions of "single instance" -- this
  design's helper, `apply`'s inline predicate, and five older inline checks.

### Mitigations

- Containment does not depend on the read: the resolver acts on the enumerated
  instance directory, so the failure modes above cost a refusal rather than
  reaching the wrong instance, and #297 replaces that one call with a locked
  read.
- The remaining check-to-use window is why the record's own fields are
  validated where they are used rather than trusted because the instance path
  was checked.
- The validation accepts every shape niwa writes today -- a worktree under the
  instance's worktrees directory and a branch name from `EffectiveBranchName` --
  so only a record niwa did not write is refused.
- `apply` keeping its own predicate is pinned by a unit test that asserts the
  two disagree on a freshly initialized root, so they cannot be merged again
  silently. Naming the three notions is worth a follow-up, deliberately not
  this change.
