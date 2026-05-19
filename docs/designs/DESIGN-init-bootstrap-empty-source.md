---
status: Accepted
upstream: docs/prds/PRD-init-bootstrap-empty-source.md
problem: |
  `niwa init <name> --from <slug> --bootstrap` is now an Accepted PRD,
  but no code in niwa chains init, create, and session-create together;
  no orchestrator owns the cross-step rollback contract; no
  exec-injection seam exists for unit-testing the git argv; the session
  branch name is hardcoded `session/<sid>` at handlers_session.go:227
  with no field for an alternate name; and the materialize boundary at
  init.go:265 still emits a single generic wrap for every failure mode
  the PRD requires classified output for (401/403, 404, ambiguous,
  no-marker). The design's job is to specify the implementation shape
  that satisfies the PRD without forking existing standalone command
  internals.
decision: |
  Add a new `internal/workspace/bootstrap.go` exposing
  `RunBootstrap` that orchestrates the lifecycle by calling the
  existing `Applier.Create` (used by `niwa create`) and the existing
  `mcp.handleCreateSession` machinery (factored into a reusable
  `mcp.CreateSession` entry point) — not by duplicating their logic.
  Cleanup is layered: `runInit`'s existing workspace-dir defer covers
  the init step, `RunBootstrap` owns instance-dir teardown for
  create-step failure, and session-create's own internal rollback
  (already at handlers_session.go:270-278) covers the session step. A
  classifier helper in `internal/cli/init.go` replaces the bare wrap
  at init.go:265 using `errors.As` against a new `*github.StatusError`
  type and the existing `*config.NoMarkerError` /
  `*config.AmbiguousMarkersError`. Scaffold derivation lives in a new
  `workspace.ScaffoldFromSource` writing the Appendix-A body
  byte-for-byte; a `branch_name` field is added to
  `SessionLifecycleState` with empty-string fallback for back-compat;
  and a `GitInvoker` interface is threaded from the cli layer through
  `RunBootstrap` into the bootstrap-owned git calls so unit tests
  record every `*exec.Cmd` without running git.
rationale: |
  The PRD pins T1 (turnkey chain) and R6 (each chained step's success
  criteria match the standalone command's). Both constraints reject
  forking create/session-create logic into bootstrap — any divergence
  becomes a maintenance liability the moment the standalone command
  changes. Reusing the public-facing entry points keeps the chain a
  composition of niwa's existing primitives. The classifier extracted
  into a helper (rather than inlined at init.go:265) keeps the seam
  unit-testable against the synthetic error chains the PRD's
  classifier-ordering AC demands. The `GitInvoker` interface (rather
  than a package-level function variable) confines the test seam to
  the bootstrap package's exported surface, so a production binary
  built without the test recorder has no global mutable state an
  attacker could swap. Adding `branch_name` to
  `SessionLifecycleState` (rather than a sidecar file or a session-ID
  encoding) keeps the on-disk schema additive and the back-compat
  fallback trivial — readers tolerate the missing field and synthesize
  `session/<sid>`.
---

# DESIGN: init bootstrap from empty source

## Status

Accepted

## Context and Problem Statement

The accepted PRD (`docs/prds/PRD-init-bootstrap-empty-source.md`)
specifies the user-facing behavior of `niwa init <name> --from <slug>
--bootstrap`: a single command that produces a workspace, an instance,
a cloned bootstrap repo, a session worktree, and a committed
`niwa-bootstrap/<sid>` branch the user can push. The PRD pins the
lifecycle model (T1 turnkey: init → create → session-create as one
chained command), the source-org pipeline scope (S2: `[[sources]]`
allow-list of the bootstrap repo), the workspace-name derivation (N1:
slug repo basename), the channels behavior (C1: `[channels.mesh]`
active by default), the branch-name format (B3:
`niwa-bootstrap/<sid>`), the rollback model (R2: stepwise rollback),
and the multi-run idempotency surface (I3: bootstrap-aware preflight
errors). The design's task is to specify HOW these settle into niwa's
existing codebase.

Today niwa's lifecycle is three separate commands. `niwa init`
(`internal/cli/init.go`) writes `<cwd>/<name>/.niwa/workspace.toml` and
a registry entry. `niwa create` (`internal/cli/create.go` → `runApply`
→ `Applier.Create` in `internal/workspace/apply.go:230`) clones
`[[sources]]` repos under `<instanceRoot>/<group>/<repo>/`, writes
`<instanceRoot>/.niwa/instance.json`, and (with `[channels.mesh]`
present) calls `InstallChannelInfrastructure`
(`internal/workspace/channels.go:241`) to write
`<instanceRoot>/.niwa/roles/<repo>/` and `.mcp.json`. `niwa session
create <repo> <purpose>` (`internal/cli/session_lifecycle_cmd.go` →
`internal/mcp/handlers_session.go::handleCreateSession`) requires
those three artifacts and produces branch `session/<sid>` at
handlers_session.go:227, worktree
`<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`, lifecycle state JSON
at `<instanceRoot>/.niwa/sessions/<sid>.json`, and a per-worktree
daemon. No orchestrator exists that calls all three in sequence. No
seam exists for tests to record git invocations without running git.
The bare wrap at `internal/cli/init.go:265` (`return fmt.Errorf(
"materializing config repo: %w", err)`) collapses every
materialize-time failure into a single opaque error, contradicting the
PRD's R10/R11/R12/R13 case-specific message contracts.

The accepted PRD reshapes the design problem from "in-place scaffold
plus pre-commit on the main checkout" (the rejected W1 model of the
prior design) to "compose three existing lifecycle commands behind
one flag." This rewrite reflects the new shape.

## Decision Drivers

- **R6 (parity with standalone steps)** forces the orchestrator to
  drive existing `Applier.Create` and session-create internals rather
  than duplicating their logic. Any forked code path drifts from the
  standalone command the user must fall back to after a partial
  failure.
- **R7 (stepwise rollback)** distributes cleanup across three
  callers, none of which can be the sole owner. `runInit`'s existing
  `workspaceCreated` defer at `init.go:215-226` already covers the
  init step. The orchestrator must add instance-dir teardown for the
  create step. Session-create's own rollback at
  `handlers_session.go:270-278` already covers the session step. The
  design must specify how these three layers compose without one
  layer's defer racing another's cleanup.
- **R5 + N4 (`niwa-bootstrap/<sid>` branch format is a durable
  contract)** requires the session machinery to accept an alternate
  branch name. The hardcoded `branchName := "session/" + sessionID`
  at `handlers_session.go:227` is the constraint. The design must
  thread a branch override through the session entry point without
  breaking the existing `niwa session create` CLI, where no override
  is accepted.
- **R22 (injectable exec invoker, test-recordable git argv)** crosses
  package boundaries: bootstrap's own git calls (`git init` is not
  needed since `Applier.Create` already drives the clone, but commit
  + branch operations are bootstrap's own) and the session-create
  layer's git calls (`git worktree add`, `git branch -D`) all need
  test-time recording. The design must pick a seam shape that reaches
  into both without leaking a test backdoor into the production
  binary.
- **N2 (typed `*github.StatusError` plus `errors.As` classifier
  precedence)** rules out string-matching dispatch and the inline
  switch the rejected design proposed. The classifier needs a stable
  precedence order, table-driven test coverage, and a clean home
  outside `runInit`'s already-long function.
- **R21 + R9 (host check before any git invocation)** forces the
  orchestrator to validate `src.Host` before delegating to
  `Applier.Create`, since `Applier.Create` itself invokes git inside
  `runPipeline`. The design must specify where the gate lives so a
  non-GitHub source can never reach a `git fetch`.
- **No `wip/` artifacts referenced from durable files** (workspace
  hygiene). The design cites only committed code paths.
- **PRD requires substantial documentation match.** Every R-number
  this design's components implement is cited inline; this protects
  the design against silent drift if the PRD is later revised.

## Considered Options

The PRD already settled user-facing decisions (T1, S2, N1, C1, B3,
R2, I3). Those appear here as constraints, not options. The
design-level decisions below address technical shape: where the
orchestrator code lives, how the exec invoker is injected, how the
branch name overrides plug into the session struct, and how the
classifier seam is extracted.

### Decision A: Orchestrator placement

**Context.** `niwa init` is implemented in `internal/cli/init.go` as a
~270-line `runInit` function. The bootstrap orchestrator adds three
phases (init-postflight, create, session-create) plus stepwise
rollback. It must call into `internal/workspace/apply.go::Applier.Create`
and `internal/mcp/handlers_session.go` machinery. Three placements
are viable.

**Key assumptions.**

- `Applier.Create` and the session-create internals are reusable
  without renaming or restructuring. (Confirmed: `Applier.Create` at
  `apply.go:230` takes `(ctx, cfg, configDir, workspaceRoot,
  instanceName)`; `handleCreateSession` at `handlers_session.go:185`
  is method-bound to `*Server` but can be factored.)
- Tests will exercise the orchestrator at the package boundary, not
  via the cli command surface. (Confirmed by R22's "unit test with
  the injectable exec invoker" AC framing.)

**Options.**

- **(A1) `internal/workspace/bootstrap.go` with
  `RunBootstrap(ctx, …) error`.** New file in the workspace package
  alongside `apply.go`, `clone.go`, `state.go`, `scaffold.go`. The
  orchestrator imports `internal/mcp` for the session entry point.
  Tests live at `internal/workspace/bootstrap_test.go`.
- **(A2) Extend `internal/cli/init.go` inline.** The orchestration
  steps become methods or helpers in the cli package. No new file;
  the existing `runInit` grows.
- **(A3) New `internal/cli/bootstrap.go`.** A cli-layer file between
  cli's other commands and the workspace package. Holds the
  orchestration; calls into `workspace.Applier.Create` and the
  factored session entry point.

**Chosen: A1.**

A1 puts the orchestrator next to the primitives it composes
(`Applier.Create`, `ScaffoldFromSource`) and away from cli-layer
concerns (flag parsing, stdin TTY detection, exit codes). It mirrors
the existing layering where `internal/cli/apply.go` is a thin shim
over `Applier.Apply`. Unit tests for the orchestrator do not need
cobra; they construct an `Applier` and a `GitInvoker` recorder and
exercise `RunBootstrap` directly.

`ScaffoldFromSource` is itself called from TWO sites and the
boundary matters for R7. Call site one lives in `runInit` (cli
layer) and writes `<workspaceRoot>/.niwa/workspace.toml` BEFORE
`RunBootstrap` is invoked — this is the copy that `Applier.Create`
reads inside `RunBootstrap`. Call site two lives inside
`RunBootstrap` itself and writes `<worktreePath>/.niwa/workspace.toml`
AFTER `CreateSession` returns — this is the copy that gets
committed on `niwa-bootstrap/<sid>` and travels with the pushed
bootstrap repo. Both call sites pass the SAME `ScaffoldOptions`, so
the two files are byte-identical (verified by a unit test in the
Implementation plan's Issue 4). The split exists because the two
files serve different consumers: local `Applier.Create` vs. future
`niwa init --from <slug>` materialize.

A2 was rejected because `runInit` is already long, the bootstrap
flow has structurally different cleanup requirements (instance-dir
defer that doesn't apply to non-bootstrap modes), and inlining
forces every bootstrap test to construct a cobra command — slower
and more brittle than calling `RunBootstrap` directly.

A3 was rejected because the cli layer's role in niwa is flag/UX
adaptation, not orchestration. Putting `RunBootstrap` in
`internal/cli` would make the workspace package — which owns
`Applier.Create` — depend on cli for the chained flow, inverting the
established import direction.

### Decision B: Exec-invoker injection mechanism

**Context.** R22 requires that every git invocation inside the
bootstrap chain be recordable from tests without executing git. The
established niwa pattern at `internal/workspace/clone.go:63` is
`exec.CommandContext("git", args...)` with `cmd.Dir` and `cmd.Env`
set inline. Three injection mechanisms are viable.

**Key assumptions.**

- The recorder fake needs to observe `cmd.Args`, `cmd.Env`, and
  `cmd.Dir` for the R22 / no-`--author` / no-`GIT_AUTHOR_*` ACs.
- Production callers should not see a runtime-mutable global. (Per
  N5's "no user secrets to disk" invariant and the broader
  defense-in-depth posture of the design.)
- The injection seam reaches both bootstrap's own git calls (commit
  + scaffold add) AND the session-create layer's git calls
  (`git worktree add`, `git branch -D`) — both surfaces are inside
  R22's scope.

**Options.**

- **(B1) `GitInvoker` interface passed as a parameter.**

  ```go
  type GitInvoker interface {
      CommandContext(ctx context.Context, args ...string) *exec.Cmd
  }
  ```

  The cli layer constructs the default (`stdGitInvoker{}` whose method
  returns `exec.CommandContext(ctx, "git", args...)`) and passes it
  into `RunBootstrap`. Tests pass a `recordingGitInvoker` that
  captures every `*exec.Cmd` instance into a slice. The session-create
  factored entry point accepts the same interface as a parameter.

- **(B2) Package-level function variable.** `var execCommand = exec.CommandContext`
  in `internal/workspace/bootstrap.go`; tests swap it in `TestMain`.
  This idiom exists elsewhere in niwa for legacy reasons.

- **(B3) Build-tag injection.** A `bootstrap_prod.go` and a
  `bootstrap_test.go` (with `//go:build test` or similar) provide
  separate implementations. Tests build with the tag.

**Chosen: B1.**

B1 confines the seam to `RunBootstrap`'s exported signature.
Production code constructs `stdGitInvoker{}` once in `runInit` and
passes it through; the test seam is invisible to anything outside the
test binary. No global mutable state exists. The same interface is
threaded into the factored session-create entry point so its git
calls (`worktree add`, `branch -D`) participate in the same recorder.

B2 was rejected because package-level mutable state is a footgun for
parallel testing (tests that share the package must swap and restore
without races) and the seam is observable to any future caller — a
future refactor could accidentally bind the variable in production.

B3 was rejected as over-machined for a single seam. Build tags make
sense when production and test implementations differ in size or
dependencies; here the difference is a 6-line struct.

### Decision C: Session-state `branch_name` field placement

**Context.** The PRD's R5 pins the branch name format
`niwa-bootstrap/<sid>` and requires the value be stored in session
state with empty-field fallback to `session/<sid>`. Three placements
satisfy R5.

**Key assumptions.**

- `SessionLifecycleState` is the durable schema for per-session state
  at `<instanceRoot>/.niwa/sessions/<sid>.json` (defined at
  `internal/mcp/session_lifecycle.go:30`).
- Adding a JSON field with `omitempty` is back-compat-safe: existing
  files without the field deserialize with the zero value (empty
  string); the PRD's fallback synthesizes `session/<sid>` from that.
- `niwa session list` and other consumers project the state struct
  into wire responses (already do via `handlers_session.go:37`); a
  new field appears in those responses unless tagged otherwise.

**Options.**

- **(C1) Add `BranchName string \`json:"branch_name,omitempty"\``
  to `SessionLifecycleState`.** Empty-string fallback in every reader
  that needs the branch: a tiny `effectiveBranchName(state)` helper
  returns `state.BranchName` when non-empty, else `"session/" +
  state.SessionID`. `NewSessionLifecycleState` (line 159) gains an
  optional `branchName` parameter.

- **(C2) Companion file
  `<instanceRoot>/.niwa/sessions/<sid>.branch.txt`.** Sidecar
  file written next to the state JSON, containing the branch name as
  a single line. Readers check for the file and fall back.

- **(C3) Encode branch into the session ID.** The session ID grows
  from 8 hex chars to include a prefix marker. The branch name is
  derived deterministically.

**Chosen: C1.**

C1 is the lowest-friction extension. JSON `omitempty` handles
back-compat for state files written before the field exists. A single
helper centralizes the fallback so every consumer that needs the
branch name produces consistent output. The field is part of the
existing schema's version 1 — no schema migration is required for
files written without it.

C2 was rejected because adding a sidecar to a single-file schema
breaks atomicity (write JSON, then write sidecar — either succeeds
alone leaves an inconsistent state) and complicates every existing
consumer that already knows how to read the JSON.

C3 was rejected because it changes the session ID format, which is
load-bearing across the codebase (`sessionIDRe` at
`session_lifecycle.go:17` validates the 8-hex format; path
construction at `handlers_session.go:226` substitutes the ID into
worktree paths). The PRD's R5 explicitly preserves the 8-hex format.

### Decision D: Classifier seam shape

**Context.** The current bare wrap at `internal/cli/init.go:265` —
`return fmt.Errorf("materializing config repo: %w", materializeErr)` —
collapses every error class. The PRD's R10, R11, R12, R13, and N2
require typed-error classification with a specific precedence:

1. `*config.AmbiguousMarkersError`
2. `*config.NoMarkerError`
3. `*github.StatusError` with `StatusCode == 401 || StatusCode == 403`
4. `*github.StatusError` with `StatusCode == 404`
5. Generic fall-through

Three implementations satisfy the precedence.

**Key assumptions.**

- `*github.StatusError` is a new type introduced in this design (it
  does not exist today). Its constructors replace the four bare
  status-error string wraps in `internal/github/fetch.go` and the
  fifth wrap at `internal/workspace/snapshotwriter.go:503`.
- Production callers (CLI display) do not depend on the exact bare
  wrap message; tests for the classifier itself are the only
  observers.
- The classifier must be unit-testable against synthetic error chains
  (the PRD's classifier-ordering AC requires a table-driven test that
  satisfies multiple arms simultaneously).

**Options.**

- **(D1) Replace the bare wrap with an inline `errors.As` switch in
  `runInit`.** The switch lives inside `runInit` at line 265,
  pattern-matching each arm in order.

- **(D2) Extract the classifier into a helper.** A new
  `classifyMaterializeError(err, hasBootstrap bool) error` function
  in `internal/cli/init.go` (or a sibling file `init_classifier.go`)
  consumes the materialize error and returns a typed `*InitConflictError`
  carrying `Detail` and `Suggestion` populated per the matched arm.

- **(D3) New typed wrappers in `internal/workspace/preflight.go`.** A
  full sentinel hierarchy (`ErrSourceAuthFailed`, `ErrSourceNotFound`,
  `ErrSourceConfigMalformed`) at the workspace boundary;
  `MaterializeFromSource` returns them directly.

**Chosen: D2.**

D2 keeps the seam at the cli layer (where `InitConflictError`
already lives) while making it independently testable. The helper
returns the existing `*workspace.InitConflictError` carrying the
PRD-mandated Detail/Suggestion text, so the cli display path at
`init.go:174,183,201` consumes it through the same pattern used by
preflight conflicts. The classifier-precedence AC becomes a
table-driven test on `classifyMaterializeError` with no cobra command
involved.

D1 was rejected because the inline switch grows `runInit`'s already
long body and forces every classifier-precedence test to construct a
cobra command and consume its return value.

D3 was rejected per PRD N3: workspace-level error sentinels
(`ErrSourceConfigMalformed`, `ErrSourceAuthFailed`,
`ErrSourceNotFound`) are explicitly deferred. v1 ships the typed
`*github.StatusError` and case-specific classifier output only.

## Decision Outcome

The four design decisions compose end-to-end. Happy path for
`niwa init my-project --from owner/my-project --bootstrap`:

1. `runInit` validates flags (R25 mutual exclusion, R2 name
   derivation), runs the preflight conflict checks (R8 sub-cases),
   `os.Mkdir`s the workspace root, and arms its existing
   `workspaceCreated` defer (today's `init.go:215-226`).

2. `runInit` constructs `src` via `parseInitSource`, asserts
   `src.IsGitHub()` (R9, R21) — non-GitHub fails fast with the exact
   R9 string and the `workspaceCreated` defer reclaims the directory.
   `IsGitHub()` returns true when `src.Host == ""` (the canonical
   `owner/repo` slug form leaves Host empty per
   `internal/source/parse.go`) OR `src.Host == "github.com"`. A
   literal-byte `Host == "github.com"` check would reject the happy
   path. The exec recorder used by tests observes zero git invocations
   on this path.

3. `runInit` calls `workspace.MaterializeFromSource` (today's
   line 264). If it returns `*config.NoMarkerError` and `--bootstrap`
   is set, `runInit` delegates to `classifyMaterializeError` (helper
   from Decision D), which detects the NoMarker arm and returns nil
   to signal "bootstrap continues" rather than an InitConflictError.
   Other classifier arms (Ambiguous, 401/403, 404) return populated
   InitConflictError values and `runInit` displays them and exits
   with the right code (R23: 1 for step failure, 2 for flag, 3 for
   host, 4 for NoMarker fail-fast).

4. `runInit` writes the FIRST scaffold copy to
   `<workspaceRoot>/.niwa/workspace.toml` plus
   `<workspaceRoot>/.niwa/claude/.gitkeep` (R3, R14, R15) by calling
   `ScaffoldFromSource(workspaceRoot, opts)` (the new helper from
   Decision D in `internal/workspace/scaffold.go`). Visibility for
   `[groups.<vis>]` derives exclusively from `Repo.Private` (bool)
   returned by the new `(*github.APIClient).GetRepo`. Lookup failure
   soft-fails to `[groups.public]` plus the R17 stderr note. On this
   call returning nil, `runInit` sets `workspaceCreated = false`
   (the R7 disarm-after-scaffold rule — see Cleanup defers section).

5. `runInit` calls `workspace.RunBootstrap(ctx, BootstrapParams{
       WorkspaceRoot, WorkspaceName, Src, Fetcher, GitInvoker,
       Reporter})`. Note that `opts` is also threaded into params so
   the second scaffold call inside `RunBootstrap` uses the SAME
   options — producing byte-identical content at the worktree path.

6. `RunBootstrap` invokes the create step by calling
   `Applier.Create(ctx, cfg, configDir, workspaceRoot, instanceName)`
   exactly as `niwa create` does (R6 parity). `instanceName` is
   computed via niwa's existing name-derivation rule for `niwa
   create`. Create's pipeline READS the on-disk
   `<workspaceRoot>/.niwa/workspace.toml` (the file `runInit` wrote
   in step 4 — that's why the runInit-owned first write must
   precede `RunBootstrap`) and clones the bootstrap repo (R4
   allow-list enforced by the scaffold's `[[sources]] repos = [...]`),
   writes instance state, and — because `[channels.mesh]` is active
   by default per R3/C1 — runs `InstallChannelInfrastructure` so
   `<instanceRoot>/.niwa/roles/<repo>/` exists. R26 confirms
   `niwa apply` is not called.

7. On create-step failure, `RunBootstrap`'s instance-dir defer (armed
   immediately before calling `Applier.Create`, disarmed after
   success) runs `niwa destroy --instance` semantics (or the
   equivalent direct call to `workspace.DestroyInstance` — see
   Solution Architecture for the exact entry point). Workspace
   directory, `<cwd>/<name>/.niwa/workspace.toml` (from step 4), and
   the registry entry stay intact for `niwa create` retry. Stderr
   emits the R7 create-fail rollback note from the Notices table.

8. `RunBootstrap` calls the factored
   `mcp.CreateSession(ctx, CreateSessionParams{
       InstanceRoot, Repo, Purpose: "bootstrap",
       BranchPrefix: "niwa-bootstrap/",
       GitInvoker, ...})`. This is the same code path as `niwa
   session create`, with the branch prefix overridden via the new
   parameter (rather than the hardcoded `session/<sid>` at
   `handlers_session.go:227`). Session-create's existing rollback at
   `handlers_session.go:270-278` covers worktree, state JSON, and
   branch cleanup on its own failures.

9. `RunBootstrap` writes the SECOND scaffold copy to
   `<worktreePath>/.niwa/workspace.toml` plus
   `<worktreePath>/.niwa/claude/.gitkeep` by calling
   `ScaffoldFromSource(worktreePath, opts)` with the SAME options
   passed to the step-4 call. The two files are byte-identical
   (verified by a unit test in Issue 4). The `sessionCreated` defer
   is armed immediately after `CreateSession` returns nil; any
   failure between that point and a successful commit triggers the
   defer to tear down the worktree, branch, and session state JSON.

10. Inside the worktree, `RunBootstrap` runs (via `GitInvoker`)
    `git add .niwa/` and `git commit -m "Initial niwa workspace
    config"` (R18: no `--author`, no `GIT_*_(NAME|EMAIL|DATE)` env
    override). The commit lands on `niwa-bootstrap/<sid>` with the
    two-file scaffold as its tree. R24 confirms no `git push`. On
    commit success `RunBootstrap` disarms `sessionCreated`.

11. `runInit` writes the worktree path to the landing-path file via
    `writeLandingPath` (R20) and emits the R19 success block to
    stderr (Appendix B format), then returns. `workspaceCreated` is
    ALREADY `false` at this point (it was disarmed in step 4), so a
    post-RunBootstrap error from R19/R20 emission would not reclaim
    the workspace directory.

12. Exit code 0.

Adjacent failure modes route through `classifyMaterializeError`:

- 401/403 → InitConflictError with the R10 substring. Exit 1.
- 404 → InitConflictError with all three R11 substrings. Exit 1.
- Ambiguous markers → InitConflictError with verbatim
  `AmbiguousMarkersError.Error()` text. Exit 1.
- NoMarker without `--bootstrap` (TTY-no-flag fail-fast or non-TTY)
  → InitConflictError with R13 text. Exit 4.
- Mutual exclusion `--bootstrap` + `--no-bootstrap` → exit 2 with R25
  text.
- Non-GitHub host → exit 3 with R9 text, before any git invocation.

## Solution Architecture

### Component overview

```
internal/cli/
  init.go                    -- new --bootstrap / --no-bootstrap flags;
                                workspace-name derivation (R2); TTY-gated
                                prompt (R13); call into classifier helper;
                                dispatch into workspace.RunBootstrap;
                                exit-code mapping (R23).
  init_classifier.go (new)   -- classifyMaterializeError helper (Decision D);
                                returns *workspace.InitConflictError populated
                                per matched arm. Unit-tested with synthetic
                                error chains.

internal/github/
  fetch.go                   -- replace four bare status-error wraps with
                                *github.StatusError construction.
  client.go                  -- new (*APIClient).GetRepo(ctx, owner, repo)
                                returning *Repo. Reuses the private→Visibility
                                normalization helper extracted from ListRepos.
  errors.go (new)            -- StatusError type + constructor.

internal/workspace/
  bootstrap.go (new)         -- RunBootstrap orchestrator (Decision A);
                                BootstrapParams struct;
                                stepwise rollback (R7);
                                R9/R21 host check before any git invocation;
                                R18 git identity (no --author override);
                                SECOND ScaffoldFromSource call at worktreePath
                                after CreateSession (the first call lives in
                                runInit, before RunBootstrap is invoked).
                                R19 success-block emission and R20 landing-path
                                write happen in runInit AFTER RunBootstrap
                                returns nil.
  scaffold.go                -- new ScaffoldOptions struct;
                                new ScaffoldFromSource(dir, opts) sibling of
                                today's Scaffold(dir, name);
                                writes .niwa/workspace.toml per Appendix A
                                byte-for-byte; writes .niwa/claude/.gitkeep
                                (R15).
  snapshotwriter.go          -- update fifth wrap site (line 503) from string
                                format to %w-wrapped *github.StatusError so
                                errors.As reaches it from the cli classifier.
  state.go (untouched)       -- preflight sentinels deferred to follow-up per
                                PRD N3.
  destroy.go                 -- existing instance-teardown entry point
                                (reused by RunBootstrap for create-step
                                rollback).

internal/mcp/
  session_lifecycle.go       -- add BranchName field to SessionLifecycleState
                                (Decision C); extend NewSessionLifecycleState
                                signature; back-compat helper effectiveBranchName.
  handlers_session.go        -- factor handleCreateSession into a reusable
                                CreateSession(ctx, CreateSessionParams) entry
                                point that accepts an optional BranchName and
                                a GitInvoker; the existing MCP handler
                                continues to call it with empty BranchName
                                (preserving the hardcoded session/<sid>
                                behavior for non-bootstrap callers).

internal/cli/
  session_lifecycle_cmd.go   -- unchanged at the user-facing CLI; the
                                factored entry point is package-internal.
```

### Key interfaces

**`internal/github/errors.go`** (new):

```go
// StatusError carries the HTTP status code from a GitHub API call so
// callers can classify failures via errors.As without parsing the
// message text. Constructed by every site in fetch.go that today
// returns a bare status-error string. Error() preserves today's
// wording for callers that print the wrapped error verbatim.
type StatusError struct {
    StatusCode int
    URL        string
    Body       string  // truncated body, diagnostic-only
}

func (e *StatusError) Error() string { ... }
```

**`internal/github/client.go`** (additive):

```go
// GetRepo fetches single-repo metadata. The scaffold uses the
// `Private` bool field for visibility classification (R16: bool
// only — the string `Visibility` field is NOT consulted, to close
// the TOML-injection vector against a malicious API host).
//
// Lookup failure (R17) is the caller's responsibility to soft-fail
// to [groups.public] and emit the stderr note.
func (c *APIClient) GetRepo(ctx context.Context, owner, repo string) (*Repo, error)
```

**`internal/workspace/scaffold.go`** (additive):

```go
type ScaffoldOptions struct {
    Name           string  // workspace name; positional arg or repo basename per R2
    SourceOrg      string  // <owner> from --from slug
    BootstrapRepo  string  // <repo> from --from slug; used in [[sources]] repos
    Private        bool    // R16 invariant: bool only, never derived from a
                           // remote-controlled string
    IncludeGitkeep bool    // production always true; unit tests may suppress
}

// ScaffoldFromSource writes .niwa/workspace.toml plus .niwa/claude/.gitkeep
// (R3, R14, R15) into dir. Sibling of Scaffold(dir, name); existing
// callers stay on Scaffold.
//
// Output matches PRD Appendix A byte-for-byte after substituting the
// Options fields into the placeholder tokens. Section ordering,
// blank-line separators, and comment lines are part of the contract.
func ScaffoldFromSource(dir string, opts ScaffoldOptions) error
```

**`internal/workspace/bootstrap.go`** (new):

```go
// BootstrapParams bundles the inputs RunBootstrap needs to chain
// init's tail through create through session-create.
//
// ScaffoldOpts is the SAME options struct the caller passed to its
// own ScaffoldFromSource(workspaceRoot, ...) call before invoking
// RunBootstrap. RunBootstrap uses it for its own second-scaffold
// write at worktreePath after CreateSession returns. Threading the
// same struct (rather than re-deriving) is what guarantees the two
// on-disk scaffold copies are byte-identical.
//
// The caller is responsible for the visibility lookup (R16) and the
// R17 soft-fail note. Fetcher remains in the struct because the
// host re-check inside RunBootstrap may use it for future-extension
// purposes, but visibility resolution itself is done caller-side
// before the workspace-root scaffold write.
type BootstrapParams struct {
    WorkspaceRoot string         // <cwd>/<name>/ absolute
    WorkspaceName string         // positional or R2-derived
    Src           source.Source  // parsed --from slug
    ScaffoldOpts  ScaffoldOptions // SAME opts caller used for the
                                  // workspace-root scaffold write;
                                  // reused for the worktree write
    Fetcher       FetchClient    // R17 host re-check
    GitInvoker    GitInvoker     // R22 test seam (interface)
    Reporter      *Reporter
}

// GitInvoker is the test seam for git argv recording (R22).
// Production code constructs stdGitInvoker{}; tests pass a recorder
// that observes every *exec.Cmd without running git.
type GitInvoker interface {
    CommandContext(ctx context.Context, args ...string) *exec.Cmd
}

// stdGitInvoker is the production implementation. Returns
// exec.CommandContext(ctx, "git", args...). No state.
type stdGitInvoker struct{}

func (stdGitInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
    return exec.CommandContext(ctx, "git", args...)
}

// RunBootstrap orchestrates the bootstrap chain.
//
// Precondition: the caller (runInit) has already invoked
// ScaffoldFromSource(workspaceRoot, opts) and the
// <workspaceRoot>/.niwa/workspace.toml file is on disk before
// RunBootstrap is called. RunBootstrap does NOT perform its own
// workspace-root scaffold write — it relies on the caller's first
// write and reads that file via Applier.Create. The SECOND
// scaffold write happens inside RunBootstrap (step 5 below) at
// worktreePath after CreateSession returns; both writes pass the
// SAME ScaffoldOptions, so the two files are byte-identical.
//
// Cleanup-defer contract (load-bearing for R7):
//
//   The caller's workspaceCreated defer (which reclaims
//   <cwd>/<name>/ on init-step failure) MUST be disarmed by the
//   caller AFTER the caller's own ScaffoldFromSource(workspaceRoot,
//   opts) call succeeds — NOT after RunBootstrap returns nil. This
//   separation is load-bearing for R7 create-step preservation: if
//   Applier.Create (called inside RunBootstrap) fails, the on-disk
//   workspace.toml at <workspaceRoot>/.niwa/workspace.toml MUST
//   survive so the user can run `niwa create` to retry. An
//   implementer who disarms workspaceCreated "after RunBootstrap
//   returns nil" would delete the scaffolded workspace on a
//   create-step failure, violating R7.
//
// Step ordering and cleanup contract:
//
//   1. Host check: src.IsGitHub() must return true (handles both the
//      canonical empty-Host slug form and explicit "github.com").
//      Failure → R9 error string returned. The caller's
//      workspaceCreated defer (still armed at this point only if
//      the caller has not yet invoked its own ScaffoldFromSource —
//      see Precondition above) is responsible for reclaiming
//      <cwd>/<name>/. Recorder observes zero git invocations on
//      this path.
//
//   2. Create step: Applier.Create(...) is invoked. The function
//      reads <workspaceRoot>/.niwa/workspace.toml — the file the
//      caller wrote before invoking RunBootstrap — so the
//      [channels.mesh] block in that file reaches the create
//      pipeline and InstallChannelInfrastructure runs. Before the
//      Applier.Create call, RunBootstrap arms its instanceCreated
//      defer; the defer removes <workspaceRoot>/<instanceName>/ on
//      any error returned from this step. Workspace directory and
//      <workspaceRoot>/.niwa/workspace.toml are preserved (R7
//      create-step contract). Stderr emits the R7 create-fail note.
//
//   3. Session step: the factored mcp.CreateSession entry point is
//      called with BranchPrefix = "niwa-bootstrap/" — CreateSession
//      generates sid then writes the branch as
//      "niwa-bootstrap/" + sid. CreateSession's own internal
//      rollback covers worktree and branch artifacts on failure
//      (R7 session-step contract). Stderr emits the R7 session-fail
//      note.
//
//   4. sessionCreated defer armed: immediately after CreateSession
//      returns nil. Cleanup target for any error between this point
//      and a successful commit.
//
//   5. Second scaffold write: ScaffoldFromSource(worktreePath, opts)
//      with the SAME opts passed to the caller's first call —
//      writes <worktreePath>/.niwa/workspace.toml plus
//      <worktreePath>/.niwa/claude/.gitkeep inside the bootstrap
//      worktree. Output is byte-identical to the workspace-root
//      copy (verified by a unit test that compares the two files).
//
//   6. Commit step: git add .niwa/ + git commit -m
//      "Initial niwa workspace config" inside the worktree. R18: no
//      --author flag, no GIT_AUTHOR_*/GIT_COMMITTER_* env override.
//      All git calls go through GitInvoker.
//
//   7. Disarm sessionCreated defer on commit success.
//
// On success the function returns nil. The caller emits R19 + R20
// after RunBootstrap returns. The caller's workspaceCreated defer
// is ALREADY disarmed (per the precondition above), so a
// post-RunBootstrap error would not reclaim the workspace dir.
func RunBootstrap(ctx context.Context, p BootstrapParams) error
```

**`internal/cli/init_classifier.go`** (new):

```go
// classifyMaterializeError maps a materialize error to either an
// *InitConflictError (carrying R10/R11/R12/R13 Detail+Suggestion text)
// or a sentinel indicating bootstrap continuation.
//
// Precedence (per PRD N2):
//   1. *config.AmbiguousMarkersError → R12 verbatim
//   2. *config.NoMarkerError + hasBootstrap → returns nil, nil
//      (signals "proceed with bootstrap"; runInit dispatches to
//      RunBootstrap)
//   3. *config.NoMarkerError + !hasBootstrap → R13 fail-fast text
//      (TTY/non-TTY variants pre-resolved by caller)
//   4. *github.StatusError{401|403} → R10 substring text
//   5. *github.StatusError{404} → R11 substrings (all three)
//   6. fall-through → today's generic wrap
//
// Returning (nil, nil) when bootstrap should proceed lets runInit's
// dispatch logic stay flat: a nil InitConflictError plus nil err is
// the "continue" signal.
func classifyMaterializeError(err error, hasBootstrap bool) (*workspace.InitConflictError, error)
```

**`internal/mcp/session_lifecycle.go`** (additive):

```go
type SessionLifecycleState struct {
    // ... existing fields ...
    BranchName string `json:"branch_name,omitempty"` // R5; empty for
                                                     // back-compat readers
}

// EffectiveBranchName returns BranchName when non-empty, else the
// historical "session/" + SessionID fallback. Every caller that
// needs the branch (worktree creation, branch deletion in destroy)
// reads via this helper.
func (s SessionLifecycleState) EffectiveBranchName() string {
    if s.BranchName != "" {
        return s.BranchName
    }
    return "session/" + s.SessionID
}

// NewSessionLifecycleState gains an optional branchName parameter.
// When empty, EffectiveBranchName() falls back to session/<sid> —
// preserves existing behavior for callers that don't pass one.
func NewSessionLifecycleState(sessionID, repo, purpose, parentSessionID,
    worktreePath, branchName string) SessionLifecycleState
```

**`internal/mcp/handlers_session.go`** (factored):

```go
// CreateSessionParams bundles inputs the existing handleCreateSession
// machinery already consumes plus the new BranchName override and
// the GitInvoker test seam.
type CreateSessionParams struct {
    InstanceRoot    string
    Repo            string
    Purpose         string
    ParentSessionID string
    BranchName      string  // empty → fall back to "session/<sid>";
                            // non-empty → R5 niwa-bootstrap/<sid>
    GitInvoker      workspace.GitInvoker  // R22 test seam
}

// CreateSession factors today's handleCreateSession body into a
// reusable entry point. The MCP handler at handleCreateSession
// continues to call it with empty BranchName so non-bootstrap
// session creation is unchanged. RunBootstrap calls it with
// BranchName = "niwa-bootstrap/" + sid.
//
// Returns the generated sessionID, worktreePath, and any error.
// The existing rollback (worktree-remove, state-file-delete,
// branch-D) at handlers_session.go:270-278 stays inside this
// function — R7 session-step contract.
func CreateSession(ctx context.Context, p CreateSessionParams) (sessionID,
    worktreePath string, err error)
```

### Data flow

```
niwa init my-project --from owner/my-project --bootstrap
  │
  ▼
runInit (internal/cli/init.go)
  │
  ├─ flag-validate: R25 mutual exclusion (--bootstrap + --no-bootstrap)
  │     → exit 2 with R25 string
  │
  ├─ R2 name derivation: if no positional and --bootstrap, derive
  │     workspaceName from src.Repo basename
  │
  ├─ preflight: R8 sub-cases (workspace exists / registry collision /
  │     non-niwa target) via existing preflightTargetExists +
  │     CheckInitConflicts; populate Detail/Suggestion per R8 wording
  │
  ├─ os.Mkdir(workspaceRoot), arm workspaceCreated defer
  │
  ├─ src := parseInitSource(source)
  │
  ├─ R9/R21 host check: !src.IsGitHub() → exit 3 with R9
  │     string; recorder observes zero git invocations
  │
  ├─ TTY R13 dispatch:
  │     - hasBootstrap = initBootstrap || (IsStdinTTY() && !initNoBootstrap
  │       && promptUserYesDefault())
  │     - non-TTY no-flag → exit 4 with R13 fail-fast string
  │     - TTY no-flag, user says N → exit 0 (clean decline)
  │
  ├─ workspace.MaterializeFromSource(ctx, src, ...)
  │     │
  │     ├─ on success → today's clone path (modeClone), not bootstrap
  │     │
  │     └─ on error → classifier dispatch (next step)
  │
  ├─ initConflict, err := classifyMaterializeError(materializeErr, hasBootstrap)
  │     │
  │     ├─ initConflict != nil → display via existing
  │     │     "Detail\n  Suggestion" pattern; return exit-mapped error
  │     │
  │     ├─ initConflict == nil && err == nil (NoMarker + bootstrap)
  │     │     → fall through to first scaffold write
  │     │
  │     └─ err != nil (fall-through) → generic wrap, exit 1
  │
  ├─ FIRST SCAFFOLD WRITE (runInit-owned, before RunBootstrap):
  │     opts := ScaffoldOptions{Name, SourceOrg, BootstrapRepo,
  │                             Private, IncludeGitkeep: true}
  │     ScaffoldFromSource(workspaceRoot, opts)
  │       ├─ visibility := GetRepo(ctx, owner, repo).Private (R16 bool-only)
  │       │     └─ on error → soft-fail to Private:false, emit R17 note
  │       ├─ write <workspaceRoot>/.niwa/workspace.toml (Appendix A)
  │       └─ write <workspaceRoot>/.niwa/claude/.gitkeep (R15)
  │     This copy is the one Applier.Create will READ inside
  │     RunBootstrap (so [channels.mesh] reaches the create pipeline).
  │
  ├─ DISARM workspaceCreated defer (R7 create-step preservation —
  │     once the scaffold is on disk, create-step or session-step
  │     failures must leave <workspaceRoot>/ intact).
  │
  ▼
workspace.RunBootstrap(ctx, BootstrapParams{...})
  │   NOTE: RunBootstrap does NOT write a scaffold at workspaceRoot.
  │   That write already happened in runInit (above). RunBootstrap
  │   reuses the on-disk scaffold via Applier.Create, then writes a
  │   SECOND copy at worktreePath after CreateSession returns.
  │
  ├─ host re-check (defense-in-depth; identical to runInit's check —
  │     catches future callers that bypass runInit and construct
  │     BootstrapParams directly)
  │
  ├─ instanceName := deriveInstanceName(workspaceName)
  │     (matches niwa create's existing default)
  │
  ├─ arm instanceCreated defer (cleanup on create-step failure)
  │
  ├─ Applier.Create(ctx, cfg, configDir, workspaceRoot, instanceName)
  │     │   (R6: same call shape as niwa create)
  │     ├─ READS <workspaceRoot>/.niwa/workspace.toml (the scaffold
  │     │   runInit wrote above) — that file is what drives the
  │     │   create pipeline
  │     ├─ runPipeline clones [[sources]] (R4: allow-list scoped to
  │     │   bootstrap repo)
  │     ├─ writes <instanceRoot>/.niwa/instance.json
  │     └─ InstallChannelInfrastructure (R26 confirms no apply call;
  │         channels run via create's pipeline because the scaffold
  │         declares [channels.mesh])
  │
  ├─ disarm instanceCreated defer (create-step success)
  │
  ├─ sid, worktreePath, err := mcp.CreateSession(ctx, CreateSessionParams{
  │     InstanceRoot, Repo, Purpose: "bootstrap",
  │     BranchPrefix: "niwa-bootstrap/", GitInvoker})
  │     (CreateSession generates sid then writes
  │      branch = "niwa-bootstrap/" + sid; see two-phase sid handshake
  │      below.)
  │
  ├─ arm sessionCreated defer (cleanup target for any failure between
  │     CreateSession returning success and the commit succeeding —
  │     scaffold write inside worktree, git add, git commit.)
  │
  ├─ SECOND SCAFFOLD WRITE (worktree-owned, after CreateSession):
  │     ScaffoldFromSource(worktreePath, opts)
  │       ├─ Called with the SAME ScaffoldOptions as the runInit call
  │       │   above — produces byte-identical content.
  │       ├─ write <worktreePath>/.niwa/workspace.toml (Appendix A)
  │       └─ write <worktreePath>/.niwa/claude/.gitkeep (R15)
  │     This copy gets COMMITTED on niwa-bootstrap/<sid> so future
  │     `niwa init --from <slug>` against the pushed bootstrap repo
  │     materializes the same workspace.toml.
  │
  ├─ Inside the worktree, via GitInvoker:
  │     ├─ git -C worktreePath add .niwa/
  │     └─ git -C worktreePath commit -m "Initial niwa workspace config"
  │         (R18: no --author, no GIT_*_(NAME|EMAIL|DATE) env)
  │
  ├─ disarm sessionCreated defer (commit succeeded)
  │
  └─ return nil
  │
  ▼
runInit:
  ├─ writeLandingPath(worktreePath)  (R20)
  ├─ emit success block to stderr (R19, Appendix B)
  └─ return nil → exit 0
```

#### Two-phase scaffold writes: same opts, two locations

`ScaffoldFromSource` is called TWICE with identical `ScaffoldOptions`,
producing byte-identical output at two filesystem locations:

| Call site | Filesystem path | Consumer |
|-----------|-----------------|----------|
| `runInit` (before `RunBootstrap`) | `<workspaceRoot>/.niwa/workspace.toml` | `Applier.Create`'s local pipeline (reads `[channels.mesh]`, installs channels infrastructure). Stays on disk after bootstrap as the workspace's primary config. |
| `RunBootstrap` (after `CreateSession`) | `<worktreePath>/.niwa/workspace.toml` | The commit on `niwa-bootstrap/<sid>`. This is the file future users materialize from when they run `niwa init --from <slug>` against the (pushed) bootstrap repo. |

These are the same logical artifact — the user's `workspace.toml` —
but live in different filesystem locations because they serve
different consumers. The byte-equality contract is verifiable: a
unit test in Issue 4 compares the two files after a happy-path
bootstrap and asserts they match exactly. Issue 5's Gherkin matrix
asserts the same byte-equality at the committed-tree level (the
`niwa-bootstrap/<sid>` HEAD tree's `.niwa/workspace.toml` matches
`<workspaceRoot>/.niwa/workspace.toml` byte-for-byte).

#### Two-phase sid handshake for R5

R5 requires the branch name embed `<sid>` and be stored in
`SessionLifecycleState`. The session machinery's `ReserveID` helper
allocates the sid via O_EXCL placeholder file before any branch is
created (`internal/mcp/session_lifecycle.go:138`). The handshake is:

1. `CreateSession` reserves sid via `newSessionLifecycleID`.
2. Caller computes branch name: `"niwa-bootstrap/" + sid`. (This
   happens inside `CreateSession` for bootstrap callers — the
   parameter is `BranchPrefix string` rather than `BranchName
   string`. Production callers pass `"niwa-bootstrap/"`; the MCP
   handler passes `"session/"`. Default is `"session/"` if empty.)
3. `CreateSession` writes the worktree with that branch name and
   persists `BranchName` into the state JSON.

The Decision-C interface section above used `BranchName string` for
exposition clarity. The Implementation Approach below pins
`BranchPrefix string` as the actual field — the value lives across
the sid generation boundary.

### Cleanup defers — who owns what

Four layers participate in R7's stepwise rollback. None of them
overlaps with another's territory:

1. **`runInit` (init step):** existing `workspaceCreated` defer at
   `init.go:215-226`. Reclaims `<cwd>/<name>/` on any error during
   the init-step proper — that is, before the runInit-owned
   `ScaffoldFromSource(workspaceRoot, opts)` call succeeds. **The
   defer is disarmed immediately after that first `ScaffoldFromSource`
   call returns nil**, NOT after `RunBootstrap` returns nil. This
   ordering is load-bearing for R7's create-step and session-step
   preservation rules: once the scaffold is written at
   `<workspaceRoot>/.niwa/workspace.toml`, create-step or
   session-step failures must leave the workspace dir (and the
   scaffolded `workspace.toml` inside it) intact, so the init-step
   defer cannot reclaim it. An implementer who disarms the defer
   "after `RunBootstrap` returns" would delete the user's workspace
   on a create-step failure, violating R7.

   Concretely, in code order, `runInit`:
   - arms `workspaceCreated = true` after `os.Mkdir(workspaceRoot)`
     (existing behavior at `init.go:215-226`);
   - validates flags + preflight + classifier;
   - calls `ScaffoldFromSource(workspaceRoot, opts)` — the FIRST of
     the two scaffold writes;
   - on that call returning nil, sets `workspaceCreated = false`;
   - then calls `workspace.RunBootstrap(ctx, params)`.

2. **`RunBootstrap` (create step):** new `instanceCreated` defer
   armed immediately before the `Applier.Create` call. On any error
   returned from `Applier.Create`, the defer calls
   `workspace.DestroyInstance(workspaceRoot, instanceName)` or the
   equivalent `--instance` semantics of `niwa destroy`. The
   workspace dir, the on-disk `<workspaceRoot>/.niwa/workspace.toml`
   (from the runInit-owned first write), and the registry entry are
   preserved per R7 create-step contract. Daemon shutdown follows
   R7's 5-second SIGTERM grace + SIGKILL ladder; shutdown timeout
   does not block instance removal.

3. **`CreateSession` (session step) — INTERNAL rollback:** existing
   rollback at `handlers_session.go:270-278`. Removes worktree,
   deletes session state JSON, force-deletes the branch.
   `CreateSession`'s internal rollback only fires for failures
   inside `CreateSession` itself (worktree-add failed, daemon-spawn
   timed out, etc.). It does NOT fire for failures that happen AFTER
   `CreateSession` returns success.

4. **`RunBootstrap` (post-session-create cleanup):** a `sessionCreated`
   defer armed immediately after `CreateSession` returns success and
   disarmed only when the bootstrap commit succeeds. On any error
   between `CreateSession` returning and the commit succeeding
   (e.g., the SECOND scaffold write at `<worktreePath>/.niwa/` fails,
   `git add` fails, `git commit` fails), the defer calls a helper
   equivalent to `niwa session destroy --force <sid>` so the
   worktree, branch, and session state JSON are removed before
   `RunBootstrap` returns. The R7 session-step contract treats this
   as a session-step failure (instance preserved, error message
   points at `niwa session create <repo> bootstrap` for retry).

R7 session-step contract end-state: instance stays intact; the R19
success block is NOT emitted; stderr emits the R7 session-fail note.

### Channels → roles/<repo>/ → CreateSession chain (closing the loop)

The session-create preflight gate at `handlers_session.go:200-203`
requires `<instanceRoot>/.niwa/roles/<repo>/` to exist. The bootstrap
chain produces this directory automatically:

1. The scaffolded `workspace.toml` declares `[channels.mesh]` (per
   PRD C1 / R3 — Appendix A in the PRD).
2. `RunBootstrap` calls `Applier.Create`, which calls `runPipeline`
   (existing code in `internal/cli/apply.go`).
3. `runPipeline` evaluates `cfg.Channels.IsEnabled()` and, because
   the scaffolded config declares the mesh block, calls
   `InstallChannelInfrastructure` from `internal/workspace/channels.go`.
4. `InstallChannelInfrastructure` creates
   `<instanceRoot>/.niwa/roles/<repo>/` for each repo discovered by
   the pipeline. For the bootstrap repo (which is in the
   `[[sources]]` allow-list per PRD S2), this means the bootstrap
   repo's role directory exists by the time `Applier.Create`
   returns.
5. `RunBootstrap` then calls `CreateSession`, whose preflight finds
   the role directory and passes the gate.

This chain is the structural reason PRD C1 mandates channels-on by
default in the scaffold. Removing `[channels.mesh]` from the scaffold
(or running `--bootstrap` against a future scaffold that omits it)
would break this gate and surface as `UNKNOWN_ROLE` from
`CreateSession`. Test coverage: the PRD's "Happy path with positional
name" AC verifies `<instanceRoot>/.niwa/roles/my-project/` exists
after bootstrap, which exercises this chain end-to-end.

### SIGKILL atomicity (operator concern)

Three operations inside the session-create handoff occur in sequence
and are NOT atomic against SIGKILL between operations:

1. **sid placeholder reservation** — `CreateSession` writes a
   zero-byte placeholder at
   `<instanceRoot>/.niwa/sessions/<sid>.json` to claim the id.
2. **worktree add** — `git worktree add <path> -b <branch>` creates
   the worktree directory and the branch.
3. **state JSON write** — `CreateSession` replaces the zero-byte
   placeholder with the full session state JSON.

A SIGKILL between (1) and (2) leaves an orphan zero-byte placeholder.
A SIGKILL between (2) and (3) leaves an orphan worktree + branch that
git knows about but niwa doesn't.

**Mitigations** (not a v1 acceptance criterion; documented as a
known operator concern):

- The placeholder name carries the `<sid>` only; subsequent
  `CreateSession` invocations generate fresh sids and don't collide.
- `niwa session destroy --force <sid>` removes the placeholder and
  any worktree+branch git knows about with that prefix.
- A future `niwa session reap` command (out of scope for v1) could
  sweep zero-byte placeholders and worktrees-without-state.

For v1, the operator-facing recovery is documented in `niwa session`
help and in this design's References section.

### Exit codes (R23)

| Code | Trigger |
|------|---------|
| 0    | Full success; TTY user types N at R13 prompt (clean decline) |
| 1    | Step failure (init, create, session-create). Stderr prefix `bootstrap step=<init\|create\|session-create>:` per R23. |
| 2    | Flag-validation error (R25 mutual exclusion, R8 sub-case 2 registry collision with --rebind refused) |
| 3    | Host-validation error (R9) |
| 4    | NoMarker without --bootstrap (R13 fail-fast: TTY-no-flag-decline, non-TTY-no-flag) |

The cli layer maps `*workspace.InitConflictError` return values to
exit codes via the existing pattern (cobra's `RunE` returns the
error; the binary's main wraps to `os.Exit(1)` for any non-nil
error). Codes 2, 3, 4 require new sentinel error types or an exit-code
field on `InitConflictError`; Implementation Approach Phase 2
specifies the choice.

## Implementation Approach

Five phases. The PR is single-PR per the existing plan; phases are
commit-boundary suggestions, NOT separate PRs. Each phase ends
CI-green with meaningful user-visible state.

### Phase 1: Error classification foundation

**Goal.** Build the typed-error infrastructure both bootstrap and
adjacent failure-mode handling need, without changing user-visible
behavior yet.

Deliverables:

- `internal/github/errors.go` (new): `*StatusError` type +
  constructor. `Error()` preserves today's wording so test fakes
  that string-match continue to work.
- `internal/github/fetch.go`: replace the four bare status-error
  wraps with `*StatusError` construction. (Today's text preserved by
  the `Error()` method.)
- `internal/workspace/snapshotwriter.go`: line 503 — change the
  `fmt.Errorf("EnsureConfigSnapshot: fetch %s returned %d", sourceURL,
  status)` wrap to wrap a `*github.StatusError` value via `%w`. This
  is the fifth wrap site referenced by the PRD's R10/R11 ACs —
  without this change the classifier cannot reach the production
  404 path.
- Update the four test fakes in
  `internal/workspace/snapshotwriter_test.go` to construct
  `&StatusError{StatusCode: ...}` where they used to format strings.
- `internal/cli/init_classifier.go` (new): `classifyMaterializeError`
  helper per Decision D. Unit tests at
  `internal/cli/init_classifier_test.go` cover the full precedence
  table (R10, R11, R12, R13 + N2 ordering AC).

User-visible state after Phase 1: no change. Materialize errors still
produce the generic wrap text (the classifier is constructed but not
yet called from `runInit`).

### Phase 2: Flag surface + prompt UX

**Goal.** Wire the `--bootstrap` / `--no-bootstrap` flags plus the
R13 prompt machinery. Classifier dispatch from `runInit` activates,
emitting case-specific error messages for 401/403/404/Ambiguous —
but bootstrap itself still dispatches to a stub that returns
"not implemented yet."

Deliverables:

- `internal/cli/init.go`: declare `initBootstrap` and
  `initNoBootstrap` package-level vars and flags (matching the
  existing `initOverlay` / `initNoOverlay` pattern). R25 mutual
  exclusion check.
- `internal/cli/init.go`: R2 name derivation. When `initBootstrap`
  is true and `len(args) == 0`, derive name from `src.Repo`.
- `internal/cli/init.go`: TTY-gated prompt at the NoMarker branch.
  Uses `cli.IsStdinTTY()` (`prompt.go`) and `cli.ReadConfirmation`
  (`prompt.go`) with the exact R13 prompt string. Non-TTY no-flag
  → R13 fail-fast string. R13 table fully implemented.
- `internal/cli/init.go`: replace the bare wrap at line 265 with a
  call into `classifyMaterializeError`. The classifier's
  InitConflictError result is displayed via the existing pattern at
  `init.go:174,183,201`. On NoMarker + `--bootstrap`, dispatch into
  a stub `return errors.New("bootstrap step=create: not implemented yet")`
  to keep flag-surface tests green before Phase 4.
- Exit-code surfacing: add an `ExitCode int` field on
  `*workspace.InitConflictError` (or a parallel sentinel-error
  pattern); `runInit` returns to cobra; the binary main maps the
  field to `os.Exit(...)` per R23.
- `@critical` Gherkin scenarios at `test/functional/features/`
  covering 401, 403, 404 user-visible text + R25 mutual exclusion +
  R13 TTY-yes / TTY-no / non-TTY fail-fast.

User-visible state: case-specific error messages are now emitted for
all adjacent failure modes. `--bootstrap` against an empty remote
fails with the stub error (exit 1, R23 prefix).

### Phase 3: Scaffold derivation + GitInvoker seam

**Goal.** Build scaffold + visibility-lookup + the test-injectable
git seam, all independent of the orchestrator.

Deliverables:

- `internal/github/client.go`: new `(*APIClient).GetRepo(ctx,
  owner, repo) (*Repo, error)`. Extract the `private bool →
  Visibility string` normalization that `ListRepos` does inline
  today into a package-internal helper so `GetRepo` and `ListRepos`
  produce consistent values.
- `internal/workspace/scaffold.go`: new `ScaffoldOptions` struct;
  new `ScaffoldFromSource(dir, opts)` function. Implements PRD
  Appendix A byte-for-byte. R15 `.gitkeep` always written when
  `IncludeGitkeep` is true.
- `internal/workspace/scaffold.go`: docstring on
  `ScaffoldFromSource` calls out the R16 invariant explicitly —
  `Private` is a bool, the TOML-injection vector via `Visibility`
  string is closed structurally, and future refactors must not
  silently derive `<vis-key>` from a remote-controlled string.
- `internal/workspace/bootstrap.go` (new, partial): `GitInvoker`
  interface + `stdGitInvoker` concrete + `BootstrapParams` struct.
  No `RunBootstrap` body yet — that lands in Phase 4.
- Soft-fail behavior for visibility lookup: any error from `GetRepo`
  → `Private: false`, scaffold emits `[groups.public]`, stderr `note:`
  line explains the fallback per R17. The `<cause>` substring is
  classified from the error type (network / 401 / 403 / 404 / 5xx).
- Unit tests for ScaffoldFromSource: Appendix-A byte-equality;
  visibility-lookup soft-fail variants (R17 note text per cause);
  `.gitkeep` byte-zero check; visibility-from-bool with adversarial
  fixture (R16 AC).

User-visible state: no end-to-end change yet. The scaffold function
is unit-tested but not yet called.

### Phase 4: Bootstrap orchestrator + session BranchPrefix

**Goal.** Compose Phase 1–3 into a working chain.

Deliverables:

- `internal/mcp/session_lifecycle.go`: add `BranchName` field to
  `SessionLifecycleState` with `json:"branch_name,omitempty"`; add
  `EffectiveBranchName()` method; extend `NewSessionLifecycleState`
  signature.
- `internal/mcp/handlers_session.go`: factor `handleCreateSession`
  into `CreateSession(ctx, CreateSessionParams)`. The existing MCP
  handler becomes a thin wrapper that constructs `CreateSessionParams`
  from the MCP args with empty `BranchPrefix` (preserves
  `session/<sid>` behavior). The factored entry point accepts the
  `GitInvoker` from params for its `git worktree add` and `git
  branch -D` calls (R22).
- Every reader that previously called `"session/" + sid` (the destroy
  path at `handlers_session.go:364` and any other consumers) reads
  via `state.EffectiveBranchName()`.
- `internal/workspace/bootstrap.go`: full `RunBootstrap` body per
  the Data Flow section. Owns instance-dir defer (R7 create-step)
  and `sessionCreated` defer (R7 session-step cleanup target after
  `CreateSession` returns). Calls `Applier.Create` (which READS the
  workspace-root scaffold that `runInit` already wrote), then
  `mcp.CreateSession` (with `BranchPrefix: "niwa-bootstrap/"`), then
  the SECOND `ScaffoldFromSource(worktreePath, opts)` call with the
  same `ScaffoldOptions` `runInit` used (producing byte-identical
  content at the worktree path). Inside the worktree, runs add +
  commit via `GitInvoker` (R18 invariants checked at the argv layer).
- `internal/cli/init.go`: between the classifier and `RunBootstrap`,
  call the FIRST `ScaffoldFromSource(workspaceRoot, opts)` and, on
  nil-return, disarm `workspaceCreated` (R7 create-step
  preservation). Then replace the Phase 2 stub with the real
  `workspace.RunBootstrap` call. Construct `stdGitInvoker{}` and
  pass it through `BootstrapParams.GitInvoker`. Thread `opts` into
  `BootstrapParams` so the second scaffold call inside
  `RunBootstrap` uses the SAME options.
- R19 success-block emission (Appendix B byte-for-byte; preceded and
  followed by one blank stderr line; lines in the exact order).
- R20 landing-path file write via the existing
  `writeLandingPath(worktreePath)`.

User-visible state: end-to-end happy path works. Adjacent failure
modes route correctly. Rollback at each step preserves the right
state.

### Phase 5: End-to-end coverage + documentation + Gherkin

**Goal.** Land the full AC matrix and finalize the user-facing
documentation.

Deliverables:

- `@critical` Gherkin scenarios at `test/functional/features/`
  covering every Happy-path and Failure-mode AC from the PRD:
  positional name; no positional name; 401; 403; 404 (typo,
  zero-commit, private-no-token); ambiguous markers; non-GitHub
  source; TTY prompt yes/no; non-TTY refusal; --no-bootstrap; mutual
  exclusion; R8 sub-cases 1/2/3a/3b/3c; rollback at init / create /
  session steps; scaffold byte-equality; .gitkeep; channels.mesh
  active; inline comment exact; visibility-from-bool adversarial;
  visibility soft-fail (server, network, auth, not-found); host-check
  ordering; no-author/no-GIT_AUTHOR_*; no-push; no-secret-on-disk;
  classifier ordering; branch-name stored; branch-name back-compat;
  R6 parity; landing-path file; success-block format; R2 regression.
- Unit tests covering the test-seam ACs: argv-injection guard;
  host-check ordering at exec layer; classifier ordering table;
  cleanup-defer at create-fail and init-fail.
- `docs/guides/workspace-config-sources.md` link target verified
  (the scaffold's comment cites this guide; ensure the link is
  current).
- `docs/guides/` new guide for `--bootstrap` (or extend an existing
  guide) describing the end-to-end flow, the visibility-lookup
  fallback, the branch-name format, and the R19 success block.
- README mention if the feature warrants top-level visibility.

User-visible state: feature complete and covered by the PRD's full
AC matrix.

## Security Considerations

The PRD pins the security invariants. The design's job is to ensure
each invariant lands at a structural enforcement point — not as
brittle prose-only contracts.

### Invariants inherited from PRD

- **Host check before git (R9, R21).** Enforced at two layers:
  `runInit` calls `src.IsGitHub()` immediately after
  `parseInitSource` and rejects non-GitHub with exit code 3.
  `RunBootstrap` re-checks on entry as defense-in-depth — if a
  future caller bypasses `runInit` and constructs `BootstrapParams`
  directly, the re-check catches it. Both layers use `IsGitHub()`
  from `internal/source/source.go:148` which returns true for both
  `Host == ""` (the canonical `owner/repo` slug form) and
  `Host == "github.com"` (explicit-host form). A literal-byte
  `Host == "github.com"` check would reject the PRD's happy-path
  canonical input — this is intentionally documented to prevent a
  future "tighten the check" refactor from breaking the happy path.
- **Visibility from `Repo.Private` bool only (R16).** Enforced
  structurally by `ScaffoldOptions.Private` being typed `bool`. The
  string `Visibility` field on `*Repo` is not accessed by
  `ScaffoldFromSource`'s code path. `GetRepo` returns the full
  `*Repo` struct — `ScaffoldFromSource` reads only the bool. A
  future refactor introducing a string-derived visibility must
  modify the struct field type, which is a visible change.
- **No `--author` flag, no `GIT_*_(NAME|EMAIL|DATE)` env (R18).**
  Enforced by `RunBootstrap`'s commit step constructing the `*exec.Cmd`
  via `GitInvoker.CommandContext(ctx, "commit", "-m", subject)` with
  no env modification. The R22 recorder used by tests asserts
  `cmd.Args` contains no `--author` element and `cmd.Env` contains
  no `GIT_*` overrides.
- **All git calls via `exec.CommandContext` with separate argv
  elements (R22).** Enforced by the `GitInvoker` interface contract:
  the only method returns `*exec.Cmd`. No shell, no string
  interpolation. The slug-injection AC validates this against the
  literal `owner/foo;rm -rf /tmp/x` adversarial input.
- **No automatic push (R24).** Enforced by `RunBootstrap`'s code
  path: there is no `git push` call. The R24 AC asserts the
  recorder records zero `git push` invocations across the happy
  path.
- **No secrets on disk (N5).** GitHub token from
  `resolveGitHubToken()` is passed only to HTTP requests and the git
  fetch subprocess env (per niwa's existing pattern). The scaffold
  TOML, instance state, registry entry, and session state never
  receive it. The recursive-grep AC validates this with a known
  fixture token.

### New security surface introduced by the design

- **`GitInvoker` interface as test backdoor.** The interface is
  exported from the workspace package. A future caller could
  construct a malicious `GitInvoker` and pass it to `RunBootstrap`.
  Mitigation: `RunBootstrap` is called from a single production
  path (`runInit`), which constructs `stdGitInvoker{}`. No
  configuration or env var causes a different invoker to be
  constructed. The interface is a test seam, not a configuration
  surface. The static analyzer (or a unit test) can assert that
  `runInit` constructs `stdGitInvoker{}` and not any other type.
- **`mcp.CreateSession` factored entry point exposed across the
  workspace→mcp boundary.** Today's `handleCreateSession` is method-
  bound to `*Server` and called only from the MCP handler dispatch.
  After Phase 4, `CreateSession` is a package-level function called
  from `internal/workspace/bootstrap.go`. The new caller does not
  pass through the MCP server's authentication or rate-limiting
  layers — but bootstrap runs in-process as part of `runInit`, so
  there is no remote caller to authenticate. The risk is structural
  reuse drift: a future MCP handler change that adds an
  authentication check inside `handleCreateSession` could miss the
  factored entry point. Mitigation: a unit test asserts both call
  sites (MCP handler + bootstrap) flow through `CreateSession`, so
  any future hardening landing in `CreateSession` automatically
  covers both.
- **`branch_name` field broadens MCP response payloads.** The
  `BranchName` field on `SessionLifecycleState` will appear in
  `niwa session list` JSON output and any other consumer that
  projects the struct. A bootstrap branch name leaks the workspace
  name and the session ID, both of which are already in other
  fields of the same response. Risk: low; no new disclosure.
- **Two-phase sid handshake.** R5 requires the branch name embed
  the sid. The handshake (sid generated → branch name computed →
  worktree + branch created → state JSON persisted) must be
  atomic from the perspective of an external observer. Today's
  session-create rollback at `handlers_session.go:270-278` is
  designed for the old single-phase flow. Mitigation: Phase 4's
  factoring preserves the rollback contract — sid placeholder
  reservation happens via `ReserveID`, worktree creation uses
  `cleanupWorktree` defer, and the state JSON write happens after
  worktree creation. A crash between sid reservation and worktree
  creation leaves a placeholder file that the existing scan-and-clean
  paths handle.

### Outcome

Document considerations. No design changes required. The Phase 5
security review should re-examine the `GitInvoker` interface scope
(can it be unexported?) and the factored `CreateSession` callability
from outside `internal/mcp` (does the workspace package need a thin
adapter to keep `CreateSession` itself package-private?).

## Consequences

### Positive

- **R6 parity by construction.** The orchestrator drives existing
  `Applier.Create` and the factored `CreateSession`; there is no
  forked code path to drift from `niwa create` / `niwa session
  create`.
- **R22 testability without production cost.** The `GitInvoker`
  interface is the test seam; production constructs a single
  zero-state implementation. No package-level mutable state, no
  build-tag forks.
- **Adjacent failure modes get case-specific messages
  structurally.** The `classifyMaterializeError` helper is
  table-tested for precedence; the typed `*github.StatusError`
  surfaces at every failure site that today produces opaque text.
- **R5 branch-name override is additive.** `SessionLifecycleState`
  gains one optional field; existing state files deserialize
  unchanged; existing callers that read the branch via the new
  helper get the back-compat fallback.
- **Stepwise rollback is layered, not centralized.** Each step
  owns its own cleanup, matching the standalone command's existing
  semantics. R7's contract is satisfied without a transactional log.

### Negative

- **Three new exported surfaces.** `*github.StatusError`,
  `(*github.APIClient).GetRepo`, `workspace.ScaffoldFromSource`,
  `workspace.RunBootstrap`, `workspace.GitInvoker`,
  `mcp.CreateSession`, `mcp.CreateSessionParams`, and the
  `BranchName` field on `SessionLifecycleState`. Each addition
  widens the contract niwa owes.
- **MCP handler factoring widens an existing import edge.**
  `internal/workspace/bootstrap.go` will import `internal/mcp` to
  reuse `CreateSession`. This is not a brand-new import direction —
  `internal/workspace/daemon.go` already imports `internal/mcp` —
  but it widens the surface workspace consumes from mcp. The
  alternative (a workspace-side interface satisfied by an mcp
  adapter) would invert the direction but adds an indirection layer
  the orchestrator doesn't otherwise need.
- **Two-phase sid handshake adds atomicity reasoning to the session
  layer.** The factoring must preserve every defer/cleanup
  invariant the current `handleCreateSession` body relies on. A
  bug introduced during factoring could leave placeholder files or
  orphan worktrees.
- **`BootstrapParams` carries six fields.** This is a wide
  parameter list — the alternative (positional args) is worse but
  the struct's growth means future additions land here, and the
  struct's docs are the only documentation for the orchestrator's
  inputs.
- **Tests that already exist for `handleCreateSession` need to be
  refactored to exercise `CreateSession`.** The handler's tests
  today construct an MCP server and dispatch the tool call. After
  Phase 4 those tests still work (the handler wraps CreateSession)
  but a new tier of CreateSession-direct tests lands.

### Mitigations

- **Surface widening:** every new type has docstring-level contract
  language plus AC coverage. The two-phase sid handshake is
  documented in Solution Architecture. The `BranchName` field has
  an explicit back-compat helper so no caller can accidentally read
  the wrong value.
- **Cross-package import:** an alternative is to introduce a
  workspace-package interface that `internal/mcp/handlers_session.go`
  satisfies via a small adapter file. This trades the direct import
  for a slightly more indirect call path. Phase 4 should evaluate
  the trade and pick the cleaner shape.
- **Sid handshake atomicity:** Phase 4 lands with a focused unit
  test exercising the handshake under simulated crash points
  (post-reserve, post-worktree-add, post-state-write). The test
  asserts the existing rollback paths still leave the directory in
  a consistent state.
- **`BootstrapParams` growth:** the struct is intentionally
  named-parameter style. Future additions (e.g., a `WorkspaceVersion`
  field or an alternate scaffold template) land as additive optional
  fields, not new function parameters.
- **Handler refactor risk:** Phase 4 lands the factoring + existing
  test compatibility in one commit; a second commit adds direct
  `CreateSession` tests. CI green on the first commit demonstrates
  no MCP-handler regression before any new test coverage lands.

## References

### Code paths this design touches

- `internal/cli/init.go` — `runInit`, the classifier seam at line
  265, the workspace-dir cleanup defer at lines 215-226, R2 name
  derivation, R8 preflight conflict integration, R13 TTY prompt,
  R23 exit-code mapping, R19 success-block emission, R20
  landing-path invocation.
- `internal/cli/init_classifier.go` — new file holding
  `classifyMaterializeError` per Decision D.
- `internal/cli/prompt.go` — `IsStdinTTY()` and `ReadConfirmation`
  used by the R13 TTY prompt.
- `internal/cli/apply.go` — reference for the `Applier`
  construction pattern that bootstrap composes into the chain.
- `internal/cli/landing.go` — `writeLandingPath` reused for R20.
- `internal/github/errors.go` — new file for `*StatusError`.
- `internal/github/fetch.go` — four bare-status wrap sites updated
  to return `*StatusError`.
- `internal/github/client.go` — new `(*APIClient).GetRepo`.
- `internal/workspace/bootstrap.go` — new file with
  `RunBootstrap`, `BootstrapParams`, `GitInvoker`, `stdGitInvoker`.
- `internal/workspace/scaffold.go` — new `ScaffoldOptions` +
  `ScaffoldFromSource` siblings of today's `Scaffold`.
- `internal/workspace/apply.go` — `Applier.Create` reused by
  `RunBootstrap` for the create step (R6 parity).
- `internal/workspace/channels.go` — `InstallChannelInfrastructure`
  invoked by `Applier.Create`'s pipeline when the scaffold declares
  `[channels.mesh]` (R26 confirms no `niwa apply` call from the
  chain).
- `internal/workspace/clone.go` — established
  `exec.CommandContext("git", args...)` pattern that the
  `GitInvoker` interface preserves (R22).
- `internal/workspace/destroy.go` — instance-teardown entry point
  reused by `RunBootstrap`'s create-step rollback defer.
- `internal/workspace/snapshotwriter.go` — fifth wrap site at line
  503 rewrapped via `%w` against `*github.StatusError`.
- `internal/config/discover.go` — `*NoMarkerError`,
  `*AmbiguousMarkersError`, and predicates consumed by the
  classifier.
- `internal/mcp/session_lifecycle.go` — `SessionLifecycleState`
  gains `BranchName`; `NewSessionLifecycleState` signature
  extended; new `EffectiveBranchName()` helper.
- `internal/mcp/handlers_session.go` — `handleCreateSession`
  factored into `CreateSession(ctx, CreateSessionParams)`;
  `BranchPrefix` parameter; hardcoded `"session/" + sessionID` at
  line 227 and line 364 replaced by `state.EffectiveBranchName()`
  or the parameter-driven prefix.
- `test/functional/features/` — `@critical` Gherkin scenarios for
  every PRD AC.
- `docs/guides/workspace-config-sources.md` — referenced by the
  scaffold's comment footer; verify link freshness in Phase 5.
