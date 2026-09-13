<!-- decision:start id="destroy-resolution" status="confirmed" -->
### Decision: How `niwa worktree destroy` resolves its target, and where the root refusal lives

**Context**

Every `niwa worktree` subcommand finds its records through `resolveInstanceRoot`
(`internal/cli/session.go:136-145`), which honors `NIWA_INSTANCE_ROOT` and
otherwise walks up to the first `.niwa/instance.json` (`session.go:151-167`).
The workspace root carries one, so at the root the walk stops there and every
caller reads the root mapping store (`.niwa/sessions/<uuid>.json`) as if it
held 8-hex lifecycle records. There are nine callers: create, apply, destroy,
list (`session_lifecycle_cmd.go:142,254,556,612`), attach and detach
(`session_attach_register.go:81,103`), `niwa go <repo> <id>` (`go.go:254`),
completion (`completion.go:101`) and the WorktreeRemove hook
(`session_from_hook_cmd.go:263`). `runSessionDestroy`
(`session_lifecycle_cmd.go:539-595`) passes its positional value straight to
`worktree.DestroySession` (`worktree.go:268`). That call reads exactly one
lifecycle file, so the value can only ever be an 8-hex worktree id. Nothing
maps a session UUID or a Claude handle to a worktree.

The PRD fixes the whole contract. R4 sets the three matching routes. R5 refuses
a worktree-id/session collision. R6 lets only the newest mapping for an
instance act on it. R7 lists five path checks in order. R9 has seven outcomes
with exit codes 0, 1, 3 and 4 and exact lines. R10 defines what each subcommand
does at a multi-instance root, and it keeps the single-instance layout, where
the root is the instance, working as today. What's left to decide is where
each piece of code lives. The limits are that `internal/worktree` stays a leaf
(`worktree.go:355-357` duplicates `StdGitInvoker` for exactly that reason),
that `workspace.ClassifyCwd` (`cwd_classify.go:86-149`) is reused rather than
a new walker written, and that the mapping read must be a single call that
decision 1's lock helper can wrap.

Three facts from the code shape the answer:

- **Plain errors already print verbatim with exit 1.** `Execute`
  (`root.go:84-107`) prints an `*sessionattach.ExitCodeError`'s `Msg`
  (when non-empty) and exits with its `Code`. Any other error is printed as is
  and exits 1. Every caller returns the `resolveInstanceRoot` error unchanged,
  so one sentinel error produces R10's exit-1 line for all of them.
- **The single-instance layout already has a detection rule.** It sits
  inline in `apply.go:358-369`: `EnumerateInstances` finds no child instance
  and the root has `instance.json`. The unit fixture `newCreateFlowFixture`
  (`session_lifecycle_issue1_test.go:50-82`) uses exactly this layout and sets
  `NIWA_INSTANCE_ROOT`, and `NIWA_INSTANCE_ROOT` is also exported into worktree
  setup scripts (`internal/workspace/worktree_content.go:102,118`).
- **Mapping files and lifecycle files share one directory in that layout.**
  `ListSessionMappings` decodes every `*.json` (`session_map.go:210-222`), and
  lifecycle files are `^[0-9a-f]{8}\.json$` (`session_lifecycle.go:22`). So a
  lifecycle record would decode as a fake mapping whose `SessionID` is its
  worktree id. Every worktree-id destroy would then look like an R5 collision.

**Assumptions**

- `destroy --by-path` at a multi-instance root keeps today's result, taking
  R10's "As today" literally. Today it scans the mapping store through the
  8-hex filter, finds nothing, and exits 1 with
  `no active worktree found at path "<p>"` (`session_lifecycle_cmd.go:518-522`).
  The new code gives the same line and exit code without reading the mapping
  store. If the PRD meant "works as it does inside an instance", the change is
  local: resolve the instance from the path with `ClassifyCwd(path)`.
- `niwa worktree list --json` at the root still prints `[]` on stdout, along
  with the R10 line on stderr, so JSON consumers keep parsing an array. R10
  says only "no table".
- `NIWA_INSTANCE_ROOT` stays a verbatim override that is never refused.
  niwa sets it only to an instance root, so the root refusal applies only to a
  root reached through cwd.
- Decision 1 provides a helper in `internal/workspace` that returns a mapping
  list read outside any swap window. Destroy calls it once and doesn't hold
  the lock past the read.
- Two recorded instance paths refer to the same instance when their
  `filepath.Clean` forms are equal. niwa writes them with `filepath.Join`,
  which already produces clean paths.

**Chosen: A. A destroy-specific resolver in `internal/cli`, with the root refusal centralized in `resolveInstanceRoot`**

*Root refusal (R10).* `discoverInstanceRoot(startDir)` is rebuilt on
`workspace.ClassifyCwd`. Its signature and the `NIWA_INSTANCE_ROOT`
precedence in `resolveInstanceRoot` stay as they are:

```go
// internal/cli/session.go
const workspaceRootRefusal = "this is the workspace root, not an instance; " +
    "run inside an instance, or pass a session id to niwa worktree destroy"

// errAtWorkspaceRoot prints as the R10 exit-1 line through Execute's
// plain-error path; `worktree list` matches it with errors.Is.
var errAtWorkspaceRoot = errors.New("niwa: error: " + workspaceRootRefusal)

func discoverInstanceRoot(startDir string) (string, error) {
    c, err := workspace.ClassifyCwd(startDir)
    // CwdInsideWorktree, CwdInsideInstance -> c.InstanceDir
    // CwdAtWorkspaceRoot -> c.WorkspaceRoot if workspace.IsSingleInstanceLayout(c.WorkspaceRoot),
    //                       else errAtWorkspaceRoot
    // CwdOutside         -> today's "not inside a workspace instance ..." error
}
```

`workspace.IsSingleInstanceLayout(root string) bool` is new in
`internal/workspace/state.go`. It lifts the `apply.go:358-369` rule out as is,
and `apply.go` is changed to call it. Seven of the nine callers then behave
the way R10 asks with no other change:

- create, apply, attach, detach and `go <repo> <id>` print the R10 line and
  exit 1.
- Completion gets an error and offers nothing (`completion.go:101-104`).
- The hook logs and returns nil (`session_from_hook_cmd.go:263-267`).

Only `runSessionLifecycleList` adds a branch:
`if errors.Is(err, errAtWorkspaceRoot)`, it prints `"niwa: " +
workspaceRootRefusal` to stderr (plus `[]` on stdout in `--json` mode) and
returns nil, so exit 0. Destroy doesn't call `resolveInstanceRoot` for the
positional form, because at the root it resolves by session instead.

*Destroy resolution.* The functions go in a new file,
`internal/cli/worktree_destroy_resolve.go`:

```go
// destroyScope says which stores this invocation may consult.
type destroyScope struct {
    InstanceDir   string // "" at a multi-instance root
    WorkspaceRoot string // "" for an orphan instance or an override outside a workspace
}

// resolveDestroyScope: NIWA_INSTANCE_ROOT set -> InstanceDir = it,
// WorkspaceRoot = ClassifyCwd(it).WorkspaceRoot. Otherwise ClassifyCwd(cwd):
// worktree/instance -> both; multi-instance root -> WorkspaceRoot only;
// single-instance root -> both = root; outside -> today's error (R3).
func resolveDestroyScope() (destroyScope, error)

// loadMappingsForDestroy is the one mapping-store read destroy makes. Its body
// is decision 1's locked read of workspace.ListSessionMappings, filtered to
// entries with workspace.ValidSessionID(m.SessionID) so single-instance-layout
// lifecycle files never masquerade as mappings. Returns nil for WorkspaceRoot == "".
func loadMappingsForDestroy(workspaceRoot string) ([]workspace.SessionMapping, error)

// matchMappings applies R4: SessionID == v, or Handle != "" && Handle == v, or
// Handle == "" && v matches ^[0-9a-f]{8}$ && HasPrefix(SessionID, v).
// Exact and case-sensitive; de-duplicated by SessionID, so a Codex mapping whose
// handle equals its id is one match.
func matchMappings(ms []workspace.SessionMapping, v string) []workspace.SessionMapping

// resolveDestroyTarget is pure: given the scope, the value and the one mapping
// snapshot, it returns the worktree id or the single mapping, or an
// *ExitCodeError with Code 3 (no match) or 4 (R4/R5 ambiguity).
func resolveDestroyTarget(scope destroyScope, v string, ms []workspace.SessionMapping) (destroyTarget, error)

// checkSessionInstance applies R7 rules 1-4 against the same snapshot and
// returns the instance dir, or gone=true for rule 2, or an exit-1 refusal.
func checkSessionInstance(workspaceRoot string, m workspace.SessionMapping, all []workspace.SessionMapping) (instanceDir string, gone bool, err error)

// destroySessionWorktrees applies R2/R9: it lists the instance's lifecycle
// records, destroys each active one in worktree-id order, and returns
// *ExitCodeError{Code: 1, Msg: ""} if any was refused.
func destroySessionWorktrees(cmd *cobra.Command, instanceDir string, m workspace.SessionMapping, force bool, git worktree.GitInvoker) error
```

`runSessionDestroy` keeps its usage checks
(`session_lifecycle_cmd.go:542-555`) and its `--by-path` branch. At a
multi-instance root, that branch returns the existing no-active-worktree error
directly and doesn't scan anything. For a positional value the order is:

1. Resolve the scope. The worktree candidate is the value when it matches the
   lifecycle id regex, `InstanceDir != ""`, and
   `<InstanceDir>/.niwa/sessions/<v>.json` exists, whatever its status.
   Ended records still go down today's idempotent path (`worktree.go:278-281`).
2. Load mappings once and run `matchMappings`.
3. Decide:
   - No match anywhere: exit 3, `niwa: error: no worktree or session matches "<v>"`.
   - More than one mapping, or a worktree plus any mapping: exit 4. One
     `niwa: error:` line names every match (worktree id and instance; session
     id and handle). When a worktree is among them, the R5 guidance follows:
     pass the full session id, or `--by-path <worktree path>`.
   - A worktree only: today's `DestroySession` call and output, byte for byte.
   - One mapping: continue to step 4.
4. Run R7 in order:
   1. `p := filepath.Clean(m.InstancePath)` must be an instance location:
      `filepath.Dir(p)` equals the workspace root (compared lexically, and
      also against `EvalSymlinks(root)` so a root reached through a symlinked
      cwd still matches), and `filepath.Base(p) != ".niwa"`. A relative path,
      the root itself, or anything outside the root is refused with exit 1.
   2. `os.Lstat(p)` reports not-exist: exit 0 with
      `session: nothing to destroy: instance <p> for session <sid> no longer exists`.
      `Lstat` means a dangling symlink counts as "something exists" and falls
      through to rule 3.
   3. `EvalSymlinks(p)` must equal `EvalSymlinks` of one of
      `workspace.EnumerateInstances(root)` (`state.go:353-374`). Otherwise
      exit 1: the path names no instance of this workspace.
   4. R6: `workspace.NewestMappingPerInstance(all)[p]` must be `m`. If it
      isn't, exit 1 naming the newer session, which now owns the instance.
      `NewestMappingPerInstance` is a new exported helper in `session_map.go`.
      It lifts the latest-`Created`, first-in-session-id-order-on-tie loop out
      of `list.go:179-193` and keys it by `filepath.Clean(InstancePath)`, and
      `annotateFromSessionMappings` is changed to call it, so list and destroy
      share one definition of "newest".
   5. `destroySessionWorktrees`, described below.
5. `destroySessionWorktrees` runs `worktree.ListSessionLifecycleStates` on
   `<p>/.niwa/sessions`. It drops ended and abandoned records and sorts the
   rest by `SessionID`. With none left, it prints the R9 "has no active
   niwa-managed worktree in <p>; branches outside niwa-managed worktrees were
   not checked" line and exits 0. Otherwise it calls
   `worktree.DestroySession(ctx, p, id, force, git)` for each one:
   - Success: the existing `warning: <BranchWarning>` line goes to stderr, then
     `session: destroyed <id> (<repo>) at <path>` goes to stdout.
   - `ErrSessionAttached`, `ErrWorktreeDirty` or any other error: one
     `niwa: error: <err>` line goes to stderr and the loop continues.

   Any refusal ends in `&sessionattach.ExitCodeError{Code: 1, Msg: ""}`, which
   `Execute` turns into exit 1 without printing anything more.

   Teardown by session never deletes a mapping or an instance (R8). `--force`
   keeps its meaning for each worktree.

Exit codes 3 and 4 travel as the existing `*sessionattach.ExitCodeError`,
unchanged. The comment at `root.go:78-83`, which says only attach and detach
use it, gets updated. `internal/worktree` is untouched; the resolver only
calls `DestroySession`, `ListSessionLifecycleStates` and the id regex through
their existing APIs, so the package still imports nothing from
`internal/workspace`.

**Rationale**

`resolveInstanceRoot` is the one function all nine callers share, so the R10
refusal written there reaches every one of them. It also reaches the two R10
cares about without saying so. Completion offers nothing and the hook logs and
exits 0 because both already handle a `resolveInstanceRoot` error that way.
Neither needs an edit. The rebuilt function uses `ClassifyCwd`, which already
tells the root apart from an instance (`cwd_classify.go:113-121`). The
single-instance exception is the same rule `apply` uses, now named once. None
of the existing unit tests reach the new code, because they set
`NIWA_INSTANCE_ROOT` and that override is kept verbatim.

Destroy has to live in `internal/cli` because its inputs are
`internal/workspace` facts: the mapping store, `EnumerateInstances`, the
workspace-root location rule, and the newest-mapping ordering. Its outputs are
CLI policy: exit codes and stdout/stderr lines. Split three ways (scope,
target, instance checks), each piece has one PRD rule to answer to. The target
resolver is pure, so R4, R5 and the no-match rule can be table-tested without
a filesystem. A single mapping snapshot feeds matching, the R6 check and the
R7 checks, which gives decision 1 exactly one read to put under its lock. It
also means "the mapping read lands during a swap" is one call to force in a
test, and R6 can't disagree with R4 because a second read raced a write.

The `ValidSessionID` filter isn't optional. Without it, the single-instance
fixture every existing destroy test uses would turn each worktree id into an
R5 collision, which the acceptance criterion "single-instance layout ...
destroy <worktree-id> at the root work as before" forbids.

**Alternatives Considered**

- **B. Same resolver, root refusal added to each command.** Rejected. It
  means six near-identical checks (create, apply, attach, detach, list, go).
  Completion and the hook would still get the root from `resolveInstanceRoot`
  and still read the mapping store as lifecycle records, which R10's first
  sentence forbids, so they'd need checks too. The next `niwa worktree`
  subcommand would inherit the bug by default. B's one advantage is a smaller
  change to `session.go`, and that doesn't matter because the typed-error
  path already lets every caller pass the error through unchanged.
- **C. A `--session <id>` flag; the positional argument stays worktree-id
  only.** Rejected. R1 and R10's first row require the positional argument to
  accept the session id and the handle, and the PRD's "ambiguity is refused,
  not resolved by precedence" trade-off already handles the collision a flag
  would dodge. A flag would also make scripts built on
  `claude agents --json` pick a flag by id shape.
- **D. Resolution inside `internal/worktree` behind a mapping-lookup
  interface.** Rejected. It keeps the leaf rule only on paper. The interface
  would have to carry the R7 location rules, instance enumeration and
  newest-mapping ordering, and its only implementation would live in
  `internal/workspace`. `worktree` would also take on exit-code and output
  policy it has never owned. `DestroySession` is already the right primitive
  for each worktree, and the orchestration around it belongs with the other
  CLI orchestrators, as `applyContentToWorktree` does for create
  (`session_lifecycle_cmd.go:291-297`).

**Consequences**

At the root, create, apply, attach, detach and `go <repo> <id>` now say where
to run instead of printing a misleading "reading session state" error. The
exit code stays 1. `worktree list` at the root prints the redirect line and
still exits 0. Completion and the hook behave as before, with no edits. From
the root, a session id or handle reaches the dispatched instance's worktrees
with the merged-branch check applied.

Inside an instance, a destroy by worktree id now also reads the root mapping
store, because R5 needs it, so it goes through decision 1's lock and its
bounded wait. If that read fails, the destroy fails with exit 1 instead of
risking a missed collision. A missing worktree id now exits 3 instead of 1,
and a non-hex value exits 3 instead of the "invalid session ID" error. The PRD
names both changes.

`list.go` and `apply.go` each lose an inline loop to a named helper in
`internal/workspace` (`NewestMappingPerInstance`, `IsSingleInstanceLayout`).
Keying `NewestMappingPerInstance` by the cleaned path changes nothing for
mappings niwa writes.

The lock is released after the mapping read. A reap that deletes the instance
between the read and the destroy therefore shows up as per-worktree
`niwa: error:` lines and exit 1, not as silent success. That's acceptable,
because reap removing a worker mid-teardown is the operator racing
themselves.

The hook's warning text at the root reads "could not resolve instance root:
niwa: error: this is the workspace root, ...", which is the one cosmetic side
effect of reusing the sentinel.
<!-- decision:end -->
