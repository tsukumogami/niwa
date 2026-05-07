---
status: Planned
problem: |
  `niwa destroy` today accepts an instance name from any cwd and refuses to
  destroy the workspace itself. This makes the command ergonomically blunt —
  there's no contextual awareness of "destroy what I'm in," no picker for
  ambiguous cases at the workspace root, no path to delete an empty workspace,
  no shell-wrapper-driven `cd`-out-of-deleted-dir, and no comprehensive
  unpushed-work guardrail when an entire workspace is wiped. The rework adds
  contextual mode selection, a picker UX, a workspace-wipe path under
  `--force`, and shell-wrapper landing-path emission for cases where destroy
  removes the user's enclosing directory.
decision: |
  Rewrite `niwa destroy` as a contextual command that classifies cwd and
  dispatches to one of three mode-specific runners. Inside an instance,
  destroy the enclosing instance and land the shell at the workspace root.
  At the workspace root with a name, destroy the named instance (today's
  flow). At the workspace root with no name, show a picker (or destroy
  directly when only one instance exists, or delete the empty workspace
  when no instances exist). With `--force` at the workspace root, wipe
  the entire workspace after a comprehensive non-pushed-work scan that
  requires typed confirmation when work would be lost. New helpers land
  as additive siblings (`internal/workspace/scan.go`,
  `internal/workspace/destroy_workspace.go`,
  `internal/workspace/cwd_classify.go`, `internal/cli/prompt.go`) so
  `niwa reset`'s helpers stay untouched. The tsuku TUI picker is copied
  into a new `internal/tui/` package. The shell wrapper gains `destroy`
  in its cd-eligible whitelist via a one-line change.
rationale: |
  This approach minimizes blast radius on shared helpers (reset's pipeline
  is preserved by construction), reuses the existing `NIWA_RESPONSE_FILE`
  protocol without touching its primitives, and groups novel surface
  (picker, prompt, scan) into small purpose-built files that each have a
  clear test seam. Extending shared helpers in place was rejected because
  reset would silently change behavior. Importing the tsuku picker as a Go
  module was rejected because `internal/` visibility blocks cross-module
  import — copying is cheaper than reorganizing tsuku's package layout.
  Workspace-wipe runs sequentially in deterministic order rather than
  concurrently because output races at a confirmation prompt are worse UX
  than a few extra seconds of wall-clock time at a rare prompt.
---

# DESIGN: niwa destroy

## Status

Planned

## Context and Problem Statement

`niwa destroy [instance]` (`internal/cli/destroy.go`,
`internal/workspace/destroy.go`) accepts an optional instance name from any
cwd. With no arg, it walks cwd up via `DiscoverInstance` to find an enclosing
instance. With a name, it enumerates instances under the workspace root and
matches. `--force` skips the uncommitted-changes guard (a single
`git status --porcelain` run per cloned repo). `ValidateInstanceDir` refuses
to destroy a workspace root by checking for `.niwa/workspace.toml`.

Three friction points motivate the rework:

1. **No contextual awareness.** `niwa destroy <name>` works from any cwd,
   including from inside another instance. From inside an instance, the
   user's intent is almost always "destroy this one I'm in," but the command
   accepts (and acts on) an unrelated name. Users typing the name of the
   wrong instance can destroy the wrong thing.

2. **No picker.** From the workspace root with no arg, today's command
   walks cwd up looking for an instance, doesn't find one, and errors.
   The user has to enumerate instances in their head and re-run with a
   specific name. A picker is the natural UX.

3. **No path to remove the workspace itself, or land outside it.** The
   workspace directory is left behind even after every instance is destroyed.
   And destroying the cwd's enclosing instance strands the user's shell in a
   deleted directory because `niwa destroy` doesn't participate in the
   `NIWA_RESPONSE_FILE` shell-wrapper protocol that `create` / `go` / `init`
   / `session create` already use.

In addition, today's `--force` only governs the uncommitted-changes guard. A
workspace-wipe operation deserves a stronger gate: a per-instance, per-repo
scan that detects unpushed commits, stashes, and the same state inside any
git worktrees the instance owns (niwa itself creates session worktrees under
`<instance>/.niwa/worktrees/`).

## Decision Drivers

Drawn from exploration findings and exploration decision blocks (see
`wip/explore_niwa-destroy-rework_findings.md` and `_decisions.md`):

- **Preserve `niwa reset`.** All four destroy helpers
  (`ResolveInstanceTarget`, `ValidateInstanceDir`, `CheckUncommittedChanges`,
  `DestroyInstance`) are shared with reset. The rework must land as additive
  sibling helpers, never as in-place edits. Reset's behavior must not
  silently change.
- **Preserve `ValidateInstanceDir`'s "refuses workspace root" invariant.**
  The new "wipe whole workspace" path under `--force` must NOT loosen this
  validator. It needs a separate sibling helper (`DestroyWorkspace`) with
  its own safety checks.
- **Reuse the existing landing-path protocol.** `NIWA_RESPONSE_FILE` and the
  `__niwa_cd_wrap` shell helper handle the deleted-cwd case correctly today
  (synchronous `cd` before prompt redraw, guarded by `[ -d ... ]` to avoid
  cd-ing to a missing path). The wrapper's command whitelist is the only
  thing that needs to grow — destroy is currently outside it.
- **Niwa has zero interactive prompts today.** The picker and typed-
  confirmation will be the first. Both must adopt niwa's existing
  conventions (stderr for interactive surfaces, stdout for the final
  summary line, lowercase-verb `fmt.Errorf`, `; use --force to override`
  wording, `hintShellInit` after success) and establish exactly two new
  patterns: a `term.IsTerminal(os.Stdin.Fd())` check and the picker/
  prompt helper.
- **Worktree scanning is mandatory in the workspace-wipe non-pushed-work
  scan.** Niwa creates session worktrees under
  `<instance>/.niwa/worktrees/<repo>-<session-id>/` via
  `internal/mcp/handlers_session.go:188`. Without scanning them, an active
  session's branch could vanish silently.
- **Sub-2s detector cost on realistic workspaces.** The detector runs ~5
  git plumbing commands per repo (`status --porcelain`,
  `for-each-ref refs/heads`, `stash list`, `worktree list --porcelain`,
  conditional detached-HEAD check). At the existing `cloneWorkers=8`
  parallelism (`apply.go:1093-1140`), 15 repos finishes in <2s — fine for
  an interactive prompt.
- **PRD/design-doc surface is small but non-trivial.** Three PRDs need
  amendments (`PRD-shell-integration` R1/R11/D3, `PRD-cross-session-
  communication` R38/AC-P11, `PRD-workspace-config-sources` line 1001)
  and three design docs need touch-ups
  (`DESIGN-instance-lifecycle` Decision 4,
  `DESIGN-shell-navigation-protocol` cd-eligible list,
  `DESIGN-contextual-completion` Decision 3). A new `PRD-niwa-destroy.md`
  is also warranted as the canonical home for the picker UX, contextual
  mode selection, and wrapper cd-out-of-deleted-dir requirements.

## Decisions Already Made

These decisions are exploration outputs and should be treated as constraints,
not reopened during design:

### From the user (settled before research)

- Trailed-off "These are probably the only" sentence in the original
  ask was an accident; ignore.
- Empty workspace + `niwa destroy` (no arg) deletes the workspace without
  `--force`, lands user one level up.
- Non-empty workspace + `niwa destroy` (no arg) shows a picker.
- `niwa destroy <name>` is only valid from the workspace root, never from
  inside an instance.
- Per-instance destroy keeps today's `--force` semantics (skip
  uncommitted-changes guard). The broader non-pushed-work check is
  workspace-self-destroy only.
- Empty-workspace definition is **lax**: no instance directories present is
  sufficient.
- Workspace-self-destroy under `--force` scans every instance for non-pushed
  work, including across git worktrees. Clean → delete silently. Dirty →
  list affected instances/branches/worktrees and require typed confirmation.

### From research (Round 1)

- **Picker reuse**: copy `tsuku/internal/tui/picker.go` (and `sanitize.go`,
  tests) into `niwa/internal/tui/`. The `internal/` location blocks module
  import; copy is cheaper than reorganizing tsuku for cross-module export.
  API: `Pick(prompt, []Choice) (int, error)` + `IsAvailable()` +
  `ErrCanceled`. Single dep is `golang.org/x/term` which niwa already
  requires.
- **Wrapper change**: extend `internal/cli/shell_init.go:54` from
  `create|go|init)` to `create|go|init|destroy)`. Update the two
  golden-string assertions in `internal/cli/shell_init_test.go`. Protocol
  primitives need no changes.
- **New helper `internal/workspace/scan.go`** for the comprehensive
  non-pushed-work detector. Types: `LossKind`, `Loss`, `RepoScan`,
  `InstanceScan`. Existing `CheckUncommittedChanges` stays untouched
  (reset still uses it).
- **New helper `internal/workspace/destroy_workspace.go`** for the
  `DestroyWorkspace(workspaceRoot)` path. `ValidateInstanceDir` stays
  strict.
- **Sequential workspace-wipe ordering** (alphabetical by instance name).
  Output races at a confirmation prompt are worse than 5s × N wall-clock;
  realistic N is small.
- **Typed-confirmation prompt fires BEFORE `writeLandingPath`.** A user
  hitting ESC must not be `cd`-ed away from a workspace they didn't
  actually destroy.
- **Confirmation token is the workspace name** (override-aware via
  `EffectiveConfigName`). Industry convention (GitHub, Heroku, Stripe)
  and defeats muscle-memory through fixed strings.

### From the lightweight decision protocol (auto-mode, Round 1)

- **Reset stays out of scope.** Destroy and reset diverge on contextual
  semantics; helpers stay shared.
- **Single-instance picker is skip-and-go.** With exactly one instance,
  destroy directly (still subject to today's dirty-repo gate).
- **PRD-shell-integration R2 cleanup is out of scope** for this PR.
- **No confirmation prompt** when destroying from inside an instance
  with no arg. Today's silent behavior preserved; new prompts apply only
  to new branches.

## Routing Matrix (settled)

| cwd            | args                            | --force | behavior                                                   | shell cwd lands |
|----------------|---------------------------------|---------|------------------------------------------------------------|-----------------|
| inside instance | none                           | any     | destroy enclosing instance                                 | workspace root  |
| inside instance | `<name>`                       | any     | reject — name only valid from workspace root               | unchanged       |
| workspace root | `<name>`                        | as today | destroy named instance                                     | unchanged       |
| workspace root | none, ≥2 instances              | absent  | interactive picker → destroy chosen                        | unchanged       |
| workspace root | none, 1 instance                | absent  | destroy that instance directly                             | unchanged       |
| workspace root | none, 0 instances (empty)       | absent  | destroy the entire workspace                               | workspace parent |
| workspace root | none, ≥1 instance               | present | scan all instances; clean → wipe silently; dirty → list & require typed confirm | workspace parent |
| outside both   | any                             | any     | error (today's behavior)                                   | unchanged       |

## Considered Options

These are the design-level decisions left open after the exploration. Each
was settled inline via the lightweight decision protocol in `--auto` mode.
Decision blocks are recorded inline; the table below summarizes choices.

| # | Decision | Choice | Alternative(s) rejected |
|---|---|---|---|
| 1 | `runDestroy` control flow shape | Dispatch to mode-specific runners | Single RunE with branched if/else |
| 2 | CWD discriminator | New `ClassifyCwd` helper in `internal/workspace/` | Inline two helper calls; generalize `ResolveApplyScope` |
| 3 | Scan API shape | `ScanInstance` + `ScanInstancesParallel` simple functions | Builder/options pattern |
| 4 | Picker `Choice.Description` content | Instance name only (v1) | Name + branch; name + branch + last-modified |
| 5 | Workspace-wipe daemon-kill ordering | Per-instance synchronous, alphabetical | Batched kill-all-then-wipe; concurrent at clone-workers |
| 6 | Test plan structure | New `destroy.feature` with 4 `@critical` + 3 standard scenarios | Rely on existing `completion.feature` only |

<!-- decision:start id="control-flow" status="confirmed" -->
### Decision: `runDestroy` control flow

**Question:** Branch inside a single `RunE`, or dispatch to mode-specific
helpers (`runDestroyInstance`, `runDestroyWorkspace`, `runDestroyEmpty`)?

**Evidence:** L3 findings show today's `runDestroy` is a single linear flow
~80 LOC. The new control flow has at least three modes plus an early
rejection branch. A single function would mix concerns and make per-mode
unit-testing awkward (today's `destroy_test.go` is ~30 LOC of cobra-wiring;
the new tests need to drive each mode independently).

**Choice:** Dispatch to mode-specific runners. The cobra command's `RunE`
classifies cwd, validates the (cwd, args, --force) tuple, and dispatches to
one of three private functions. Rejection cases (`destroy <name>` from
inside an instance) return the error directly from the dispatcher.

**Alternatives considered:**
- Single `RunE` with branches: simpler structure but mixes concerns and
  makes per-mode tests harder to write.

**Assumptions:** Mode-specific functions don't grow shared state that
forces them back into one function.

**Consequences:** Each mode is independently testable. The dispatcher
itself is small (~30 LOC of routing).
<!-- decision:end -->

<!-- decision:start id="cwd-discriminator" status="confirmed" -->
### Decision: CWD discriminator

**Question:** Reuse the `ResolveApplyScope` pattern (which classifies cwd
into `ApplySingle`/`ApplyAll`), generalize that helper, or write a fresh
`ClassifyCwd`?

**Evidence:** L3 findings document `ResolveApplyScope` at
`internal/workspace/scope.go:41`. Its enum doesn't map cleanly to destroy's
three modes (`InsideInstance`, `AtWorkspaceRoot`, `Outside`). Generalizing
it would couple two unrelated commands. Inlining the two underlying calls
(`DiscoverInstance` + `config.Discover`) duplicates logic any future
contextual command (e.g., a hypothetical `niwa where`) would also want.

**Choice:** New helper at `internal/workspace/cwd_classify.go`:

```go
type CwdClass int
const (
    CwdInsideInstance CwdClass = iota
    CwdAtWorkspaceRoot
    CwdOutside
)
type Classify struct {
    Class         CwdClass
    WorkspaceRoot string  // populated for InsideInstance and AtWorkspaceRoot
    InstanceDir   string  // populated for InsideInstance
}
func ClassifyCwd(cwd string) (Classify, error)
```

**Alternatives considered:**
- Inline calls in `runDestroy`: duplicates logic for future commands.
- Generalize `ResolveApplyScope`: couples two unrelated commands.

**Assumptions:** `DiscoverInstance` returning a non-nil error implies "not
inside an instance" rather than a deeper failure (matches today's behavior
in completion.go and status.go).

**Consequences:** A small reusable helper that future commands can adopt
without re-deriving the discrimination logic.
<!-- decision:end -->

<!-- decision:start id="scan-api" status="confirmed" -->
### Decision: Scan API shape

**Question:** Two simple functions (`ScanInstance` + `ScanInstancesParallel`)
or an options/builder pattern?

**Evidence:** L5 findings propose `ScanInstance(instanceDir) (InstanceScan, error)`
and `ScanInstancesParallel(roots []string, workers int) ([]InstanceScan, error)`.
The current consumers are exactly one (the workspace-wipe path); future
expansion would add filter flags (e.g., `--include-untracked`). The simple
function pair handles today's needs without API surface for unproven
extension points.

**Choice:** Two simple functions:

```go
package workspace

func ScanInstance(instanceDir string) (InstanceScan, error)
func ScanInstancesParallel(workspaceRoot string, instanceDirs []string, workers int) ([]InstanceScan, error)
func FormatScans(scans []InstanceScan, w io.Writer, workspaceName string)
```

**Alternatives considered:**
- Builder/options pattern: premature flexibility; adds API surface
  without a real consumer.

**Assumptions:** Future filter flags (e.g., suppressing untracked-file
counts) can be added as parameters or as a small `ScanOptions` struct
without breaking the simple-function shape.

**Consequences:** Minimal API surface; easy to mock in tests.
<!-- decision:end -->

<!-- decision:start id="picker-description" status="assumed" -->
### Decision: Picker `Choice.Description` content

**Question:** Show instance name only, or include the active branch / last-
modified time in each picker row?

**Evidence:** L1 findings document the picker's `Choice` struct as
`{Name, Description}`. Adding branch info means picking which repo's HEAD
to show (instances have multiple repos) and adds I/O to the picker render
path. Last-modified is a status-panel feature out of scope for v1. The
user's spec example showed bare names: "Workspace is not empty. Must
provide a valid workspace instance name. Here are the ones valid: foo, bar".

**Choice:** Instance name only in v1 — `Description` is empty.

**Alternatives considered:**
- Name + active branch: requires picking a "primary" repo per instance.
  Defer until users ask.
- Name + last-modified: useful but turns the picker into a status panel.

**Assumptions:** Users select by instance name in the picker the same way
they would have typed it.

**Consequences:** Picker rows are minimal. Adding columns later is a
non-breaking change (callers fill `Description`, picker renders if non-empty).
<!-- decision:end -->

<!-- decision:start id="wipe-ordering" status="confirmed" -->
### Decision: Workspace-wipe daemon-kill ordering

**Question:** Per-instance synchronous (kill→wipe→next) or batched
(kill-all → wait → wipe-all)?

**Evidence:** L3 findings note `TerminateDaemon` is idempotent and has up
to 5s grace per call (`NIWA_DESTROY_GRACE_SECONDS`). Batched ordering
would shave wall-clock time but breaks idempotency on partial failure
(if instance N's daemon misbehaves, instances 1..N-1 are in an
intermediate killed-but-not-wiped state). Per-instance synchronous keeps
each instance atomic at the granularity that matters for resume.

**Choice:** Per-instance synchronous, alphabetical by instance name.
For each instance:

1. `TerminateDaemon(instanceDir)` (existing helper, swallows errors).
2. `ValidateInstanceDir(instanceDir)` (defensive double-check).
3. `os.RemoveAll(instanceDir)`.
4. Print `Destroyed instance: <name>` to stderr (workspace-wipe path
   uses stderr for per-instance progress; final summary stays on stdout).

After all instances complete, `os.RemoveAll(workspaceRoot)`.

**Alternatives considered:**
- Batched kill-all-then-wipe-all: faster but breaks per-instance
  idempotency.
- Concurrent at `cloneWorkers` parallelism: small wall-clock gain at the
  cost of interleaved stderr output during a confirmation prompt.

**Assumptions:** Realistic N (≤5 instances) keeps total wall-clock under
30s, acceptable for a rare destructive command.

**Consequences:** Predictable output, resumable on partial failure.
<!-- decision:end -->

<!-- decision:start id="test-plan" status="confirmed" -->
### Decision: Test plan structure

**Question:** Where do new unit tests live, what `@critical` Gherkin
scenarios are needed?

**Evidence:** L3 findings show today has one `@critical` scenario in
`completion.feature:57` and ~30 LOC of cobra-wiring tests in
`destroy_test.go`. The CLAUDE.md guidance says "When you ship a user-facing
CLI command or fix a regression in the init → create → apply workflow,
add a `@critical` Gherkin scenario." This rework changes user-facing
behavior across multiple branches.

**Choice:** New file `test/functional/features/destroy.feature` with:

`@critical` scenarios (4):

1. **Destroy from inside an instance** lands the shell at the workspace
   root via `NIWA_RESPONSE_FILE`.
2. **Destroy by name from workspace root** preserves today's flow:
   destroys named instance, no shell `cd`, no picker.
3. **Destroy with no arg from workspace root, single instance** skips the
   picker and destroys that instance directly.
4. **Workspace-self-destroy via `--force` on a clean workspace** scans
   (finds no losses), wipes silently, lands shell at workspace parent.

Standard scenarios (3):

5. **Destroy with name from inside an instance** is rejected with the
   expected error wording.
6. **`--force` workspace destroy with unpushed work** prints the loss
   list and aborts when the typed confirmation does not match.
7. **`--force` workspace destroy with unpushed work** completes when
   the typed confirmation matches the workspace name.

Unit tests:
- `internal/cli/destroy_test.go` — extended with control-flow tests, the
  TTY-check refusal path, and error-message golden assertions.
- `internal/workspace/cwd_classify_test.go` — new, table-driven over the
  three classes.
- `internal/workspace/scan_test.go` — new, covers each `LossKind`,
  worktree enumeration, edge cases (broken `.git`, missing dir, orphan
  instance).
- `internal/workspace/destroy_workspace_test.go` — new, happy path plus
  invariant test that `DestroyWorkspace` does not call
  `ValidateInstanceDir` on the workspace root.
- `internal/tui/picker_test.go` — copied from tsuku.
- `internal/cli/prompt_test.go` — new, covers `ReadConfirmation` (match,
  mismatch, ESC, EOF).

**Assumptions:** The functional-test harness (`localGitServer`, godog
runner) handles `NIWA_RESPONSE_FILE` end-to-end the same way `go.feature`
does today. (Verified in L3.)

**Consequences:** Functional coverage matches the routing matrix's
critical paths. Unit tests cover each new helper in isolation.
<!-- decision:end -->

## Decision Outcome

The reworked `niwa destroy` is a single cobra command whose `RunE` does
three things: (1) classify cwd via the new `ClassifyCwd` helper, (2)
validate the `(class, args, --force)` tuple against the routing matrix, and
(3) dispatch to one of three mode-specific runners. The new `internal/tui/`
package holds a copied picker, `internal/workspace/scan.go` runs the
comprehensive non-pushed-work scan in parallel, `internal/workspace/
destroy_workspace.go` implements the workspace-wipe path, and
`internal/cli/prompt.go` provides `IsStdinTTY` and `ReadConfirmation`. The
shell wrapper gets `destroy` added to its cd-eligible whitelist; the
landing-path protocol's primitives are unchanged. Three PRDs and three
design docs receive amendments; a companion `PRD-niwa-destroy.md` is
optional in this PR (treated as a follow-up if review wants it scoped out).

## Solution Architecture

### Component map

```
internal/cli/destroy.go              REWRITTEN  cobra command, dispatcher, mode runners
internal/cli/prompt.go               NEW        IsStdinTTY, ReadConfirmation
internal/cli/shell_init.go           MODIFIED   one-line case extension
internal/cli/destroy_test.go         EXTENDED   control flow + error wording

internal/tui/picker.go               NEW (copy) Pick, Choice, IsAvailable, ErrCanceled
internal/tui/sanitize.go             NEW (copy) SanitizeDisplayString
internal/tui/picker_test.go          NEW (copy)

internal/workspace/cwd_classify.go   NEW        ClassifyCwd, Classify, CwdClass
internal/workspace/scan.go           NEW        Loss types + ScanInstance + ScanInstancesParallel + FormatScans
internal/workspace/destroy_workspace.go NEW     DestroyWorkspace
internal/workspace/destroy.go        UNTOUCHED  reset still uses these
internal/workspace/destroy_test.go   UNTOUCHED  reset's coverage preserved

test/functional/features/destroy.feature NEW    4 @critical + 3 standard scenarios

docs/prds/PRD-shell-integration.md        AMEND  R1, R11, D3, Out-of-Scope
docs/prds/PRD-cross-session-communication.md AMEND  R38, AC-P11
docs/prds/PRD-workspace-config-sources.md AMEND  line 1001
docs/designs/current/DESIGN-instance-lifecycle.md     AMEND  Decision 4
docs/designs/current/DESIGN-shell-navigation-protocol.md AMEND  cd-eligible list
docs/designs/current/DESIGN-contextual-completion.md  AMEND  Decision 3
```

### Control flow (sequence)

```
cobra entry: niwa destroy [name] [--force]
    │
    ▼
ClassifyCwd(cwd)
    │
    ├─ Outside ──────────────────────────────────► error "not inside a niwa workspace"
    │
    ├─ InsideInstance, args=name ─────────────────► error "instance name only valid from workspace root"
    │
    ├─ InsideInstance, no args ───────────────────► runDestroyInstance(class.InstanceDir, force)
    │      ├─ TerminateDaemon → CheckUncommittedChanges (skip if force) → DestroyInstance
    │      └─ writeLandingPath(class.WorkspaceRoot)
    │
    ├─ AtWorkspaceRoot, args=name ────────────────► resolveInstanceByName(cwd, name) → runDestroyInstance(dir, force)
    │      └─ no landing path written
    │
    ├─ AtWorkspaceRoot, no args, no force
    │      ├─ EnumerateInstances(workspaceRoot)
    │      │     ├─ 0 instances ────────────────► runDestroyEmpty(workspaceRoot) → writeLandingPath(parent)
    │      │     ├─ 1 instance ─────────────────► runDestroyInstance(only, force=false) → no landing path
    │      │     └─ ≥2 instances ────────────────► IsStdinTTY?
    │      │                                          ├─ no  ► error "use --force or specify <name>"
    │      │                                          └─ yes ► picker.Pick → runDestroyInstance(chosen, false)
    │
    └─ AtWorkspaceRoot, no args, --force ─────────► runDestroyWorkspace(workspaceRoot)
           ├─ EnumerateInstances → 0 ► runDestroyEmpty
           ├─ ScanInstancesParallel(workers=8)
           ├─ if any HasLoss:
           │    ├─ FormatScans → stderr
           │    ├─ ReadConfirmation(workspaceName) → false ► abort, no landing path
           │    └─ true ► proceed
           ├─ for each instance, alphabetical:
           │    ├─ TerminateDaemon
           │    ├─ ValidateInstanceDir (defensive)
           │    └─ RemoveAll
           ├─ RemoveAll(workspaceRoot)
           └─ writeLandingPath(parent)
```

### Helper signatures

```go
// internal/workspace/cwd_classify.go
type CwdClass int
const (
    CwdInsideInstance CwdClass = iota
    CwdAtWorkspaceRoot
    CwdOutside
)
type Classify struct {
    Class         CwdClass
    WorkspaceRoot string  // absolute; populated for InsideInstance and AtWorkspaceRoot
    InstanceDir   string  // absolute; populated for InsideInstance
}
func ClassifyCwd(cwd string) (Classify, error)

// internal/workspace/scan.go
type LossKind string
const (
    LossWorkingTreeDirty LossKind = "dirty"
    LossUntracked        LossKind = "untracked"
    LossUnpushedCommits  LossKind = "unpushed"
    LossLocalOnlyBranch  LossKind = "local-only"
    LossStash            LossKind = "stash"
    LossDetachedOrphan   LossKind = "detached"
    LossExternalWorktree LossKind = "external-wt"
)
type Loss struct {
    Kind   LossKind
    Branch string  // "" for stash/dirty/untracked
    Detail string  // "3 modified files", "2 commits", "1 stash"
    Path   string  // worktree path if not primary
}
type RepoScan struct {
    Name    string
    Losses  []Loss
    Skipped string  // non-empty if scan failed
}
type InstanceScan struct {
    InstanceName string
    InstanceDir  string
    Repos        []RepoScan
}
func (s InstanceScan) HasLoss() bool

func ScanInstance(instanceDir string) (InstanceScan, error)
func ScanInstancesParallel(workspaceRoot string, instanceDirs []string, workers int) ([]InstanceScan, error)
func FormatScans(scans []InstanceScan, w io.Writer, workspaceName string)

// internal/workspace/destroy_workspace.go
type DestroyWorkspaceOpts struct {
    Force bool   // bypass non-pushed-work scan
    Reporter *Reporter  // optional progress
}
func DestroyWorkspace(workspaceRoot string, opts DestroyWorkspaceOpts) error

// internal/cli/prompt.go
func IsStdinTTY() bool
// ReadConfirmation reads a single line from stdin and returns whether
// it equals expected after Trim. Returns (false, nil) on mismatch,
// (false, err) on read error.
func ReadConfirmation(prompt, expected string, in io.Reader, out io.Writer) (bool, error)
```

### Shell wrapper extension

`internal/cli/shell_init.go:54` (current):

```sh
case "$1" in
    create|go|init)
        __niwa_cd_wrap "$@"
        ;;
```

After the change:

```sh
case "$1" in
    create|destroy|go|init)
        __niwa_cd_wrap "$@"
        ;;
```

Tests in `internal/cli/shell_init_test.go` are updated to assert membership
of each command name independently rather than golden-string-matching the
exact case label, so future additions (e.g., a hypothetical
`niwa workspace switch`) don't churn this test repeatedly.

### Landing-path order invariant

The typed-confirmation prompt fires **before** `writeLandingPath`. The
sequence in `runDestroyWorkspace` (when `--force` and unpushed work is
detected):

1. `ScanInstancesParallel` → list of `InstanceScan` with losses.
2. `FormatScans` to stderr.
3. `ReadConfirmation` against the workspace name.
4. If mismatch or EOF → abort, **do not** call `writeLandingPath`. The
   user's shell stays where it was. Exit non-zero.
5. If match → proceed with the per-instance destroy loop.
6. After successful workspace-root `RemoveAll`, call `writeLandingPath(parent)`.

Test `internal/cli/destroy_test.go` includes a regression test asserting
that a confirmation mismatch produces a non-zero exit AND that
`NIWA_RESPONSE_FILE` is empty after the run.

## Implementation Approach

The work splits into seven phases. Each is independently committable but
the PR squashes to a single commit per the tsukumogami squash-merge
convention.

### Phase A: doc-only PRD/design amendments (parallel, low-risk)

- `PRD-shell-integration.md`: amend R1 to add `destroy` to cd-eligible
  set; amend R11 to fire the runtime hint on destroy too; amend D3 /
  Out-of-Scope paragraph to acknowledge destroy's intentional cd
  behavior.
- `PRD-cross-session-communication.md`: amend R38 with multi-instance
  clause; add AC-P11b (or extend AC-P11) for picker and `--force`
  workspace cases.
- `PRD-workspace-config-sources.md`: line 1001 — soften "non-interactive"
  claim with "except typed-confirmation when destroy detects unpushed
  work."
- `DESIGN-instance-lifecycle.md`: amend Decision 4 with the cwd-
  context-driven mode selection.
- `DESIGN-shell-navigation-protocol.md`: amend cd-eligible list to
  include destroy (and bring init / session create up to date).
- `DESIGN-contextual-completion.md`: amend Decision 3 with picker UX vs
  completion reconciliation.
- (Optional) Write `PRD-niwa-destroy.md` as the canonical home for the
  new requirements. May land in a follow-up PR.

### Phase B: shell wrapper extension

- `internal/cli/shell_init.go:54` — add `destroy` to the case label.
- `internal/cli/shell_init_test.go` — replace golden-string assertion
  with per-subcommand membership checks.

### Phase C: copy picker

- Copy `tsuku/internal/tui/picker.go`, `sanitize.go`, `picker_test.go`
  into `niwa/internal/tui/`.
- Add a comment block at the top of `picker.go` referencing the upstream
  source: `tsukumogami/tsuku@c8f58101 (#2369)`.

### Phase D: new helpers

- `internal/workspace/cwd_classify.go` + `cwd_classify_test.go`.
- `internal/workspace/scan.go` + `scan_test.go`.
- `internal/workspace/destroy_workspace.go` + `destroy_workspace_test.go`.
- `internal/cli/prompt.go` + `prompt_test.go`.

### Phase E: rewrite destroy command

- Rewrite `internal/cli/destroy.go` per the control-flow diagram.
- Extend `internal/cli/destroy_test.go` with the new control-flow tests.
- Update help text (`Long:`) to describe the routing matrix.

### Phase F: functional tests

- New `test/functional/features/destroy.feature` with 4 `@critical` + 3
  standard scenarios. Reuse the `localGitServer` helper and the
  response-file pattern from `go.feature`.

### Phase G: verification

- `go test ./...` — unit tests pass.
- `make test-functional-critical` — `@critical` Gherkin scenarios pass.
- Manual smoke: source the new wrapper, run `niwa destroy` from inside
  an instance and from a workspace root, confirm shell `cd`s.

### Cross-phase dependencies

```
A (doc) ─────► (independent, can land any time)
B (wrapper) ─► E
C (picker) ──► E
D (helpers) ─► E
E (destroy) ─► F
F (tests) ───► G (verification)
```

Phase A (doc-only) is parallelizable with B/C/D. Phases B, C, D have no
mutual dependencies and can run concurrently before E.

## Security Considerations

`niwa destroy` is destructive. The rework introduces no new privilege
escalation, file-overwrite, or network-exposure surface. Specific
considerations:

1. **Path traversal in `NIWA_RESPONSE_FILE`**: handled by existing
   `validateResponseFilePath` (`internal/cli/landing.go:62-79`). Destroy
   uses the same writer. Defense unchanged.

2. **Path traversal in landing path**: destroy computes the landing path
   from `Classify.WorkspaceRoot` and `filepath.Dir(workspaceRoot)`, both
   absolute paths derived from `config.Discover`. The wrapper's
   `[ -d "$dir" ]` guard provides defense in depth — if destroy chose a
   non-existent path, the wrapper silently skips the `cd`.

3. **Race between scan and wipe**: between `ScanInstancesParallel` and
   the `RemoveAll` loop, the user could push their work from another
   shell. False positive (extra prompt) is harmless. False negative is
   the typed-confirmation path; the prompt itself is the safety.

4. **Symlink handling in workspace dir**: `os.RemoveAll` removes the
   symlink itself, not the target. Standard Go behavior. No new
   vulnerability.

5. **Confirmation token spoofing**: the token is the workspace name,
   sourced from `EffectiveConfigName` in the running niwa process. Not
   user-injectable.

6. **TTY assumption for picker**: refusing the picker in non-TTY mode
   prevents the picker from rendering raw control sequences into a log
   stream. The refusal error is the only output; safe by construction.

7. **Worktree enumeration via `git worktree list`**: relies on git's
   worktree admin files. A malicious worktree admin entry (someone with
   write access to `.git/worktrees/`) could cause the scanner to
   enumerate paths outside the workspace. The scanner only reads from
   those paths (`status`, `for-each-ref`); it does not modify them.

8. **`os.RemoveAll` on the workspace root**: this is the entire point of
   the `--force` workspace-wipe path. The typed-confirmation gate is the
   safety; once confirmed, the deletion is unrecoverable. This is
   covered by the `Out of Scope` section's "no undo, no soft-delete"
   note.

No new threat model is introduced beyond what `niwa destroy` already has
today. The typed-confirmation prompt is a *new* defense (against
accidental workspace-wipe), not a new attack surface.

## Consequences

### Positive

- Contextual destroy that "just works" from common cwds.
- Shell wrapper drops the user out of deleted directories via the
  existing landing-path protocol.
- Workspace-wipe path with comprehensive non-pushed-work scan that
  catches active sessions and worktrees.
- Picker UX consistent with tsuku's recipe-disambiguation flow.
- New `internal/cli/prompt.go` and `internal/tui/` packages establish
  reusable primitives for future commands.
- Functional coverage for the create/destroy lifecycle gains 4 new
  `@critical` scenarios (today: 1 — completion only).

### Negative

- More user-facing surface: more error messages, more help text variants,
  the first interactive prompts in niwa.
- First non-TTY contract documented in user-facing help (the picker and
  typed-confirmation refusals).
- Workspace-wipe wall-clock time is linear in N (sequential ordering),
  bounded by `5 × N` seconds in the worst case (daemon grace).
- Sequence-dependent invariant: typed-confirmation MUST fire before
  `writeLandingPath`. Enforced only by tests.
- Picker code lives in two repos (tsuku + niwa). Drift risk over time.
- PRD/design-doc surface touched (3 PRDs + 3 design docs amended,
  optionally +1 new PRD).

### Mitigations

- Tests cover the non-TTY contract for both the picker and the typed-
  confirmation prompt.
- Tests cover the typed-confirmation → landing-path order invariant
  (a regression test asserts `NIWA_RESPONSE_FILE` is empty after a
  confirmation mismatch).
- Picker copy includes a header comment referencing the upstream commit
  (`tsukumogami/tsuku@c8f58101`) so future maintainers can compare.
- Companion `PRD-niwa-destroy.md` (Phase A optional) is the canonical
  home for the new requirements; existing PRDs only get cross-link
  amendments.

## Out of Scope

- Reset rework (separate follow-up if/when desired).
- Recovering destroyed workspaces (no undo, no soft-delete).
- Network access for the unpushed-work scan (no `git fetch`; trust local
  remote-tracking refs).
- Submodule recursion (informational "N submodules not scanned" only).
- PRD-shell-integration R2 stale-stdout cleanup (separate doc PR).

## References

- Exploration scope: `wip/explore_niwa-destroy-rework_scope.md`
- Round 1 findings: `wip/explore_niwa-destroy-rework_findings.md`
- Round 1 decisions: `wip/explore_niwa-destroy-rework_decisions.md`
- Round 1 research:
  - `wip/research/explore_niwa-destroy-rework_r1_lead-tsuku-picker-reuse.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-shell-wrapper-coverage.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-current-destroy-surface.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-niwa-ux-patterns.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-non-pushed-work-detection.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-prd-impact.md`
- Existing PRDs to amend: `docs/prds/PRD-shell-integration.md`,
  `docs/prds/PRD-cross-session-communication.md`,
  `docs/prds/PRD-workspace-config-sources.md`
- Companion PRD to write: `docs/prds/PRD-niwa-destroy.md` (Proposed)
- Existing design docs to amend:
  `docs/designs/current/DESIGN-instance-lifecycle.md`,
  `docs/designs/current/DESIGN-shell-navigation-protocol.md`,
  `docs/designs/current/DESIGN-contextual-completion.md`
