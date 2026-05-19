# Plan Analysis: init-bootstrap-empty-source

## Source Document

Path: `docs/designs/DESIGN-init-bootstrap-empty-source.md`
Status: Accepted
Input Type: design
Upstream PRD: `docs/prds/PRD-init-bootstrap-empty-source.md` (Accepted)

## Scope Summary

Implement `niwa init <name> --from <slug> --bootstrap` as a turnkey
lifecycle command: chain init → create → session-create so the user
lands inside a real niwa worktree at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`
with a scaffolded `.niwa/workspace.toml` committed on the
`niwa-bootstrap/<sid>` branch.

## Components Identified

- **`*github.StatusError`** (`internal/github/errors.go` new): typed
  error replacing four string-formatted status-error sites.
- **Fifth-wrap fix** (`internal/workspace/snapshotwriter.go:503`): swap
  `fmt.Errorf` string format for `%w`-wrap to preserve typed error
  through the chain.
- **Classifier helper** (`internal/cli/init_classifier.go` new):
  `classifyMaterializeError` per Decision D (D2: extracted helper for
  testability). Ordered most-specific-first per PRD N2.
- **`--bootstrap` / `--no-bootstrap` flags** (`internal/cli/init.go`):
  flag wiring, R25 mutual exclusion, R2 name derivation, R13
  TTY-prompt dispatch.
- **TTY prompt** (`internal/cli/init.go`): exact R13 prompt string
  with explicit Y/N + bare-Enter semantics.
- **`(*github.APIClient).GetRepo`** (`internal/github/client.go`):
  new single-repo metadata fetch; `Private` bool → visibility helper
  shared with `ListRepos`.
- **`ScaffoldFromSource`** (`internal/workspace/scaffold.go`): new
  sibling of `Scaffold`; emits PRD Appendix A byte-for-byte; R15
  `.gitkeep`; R16 invariant documented.
- **`GitInvoker` + `BootstrapParams`** (`internal/workspace/bootstrap.go`
  new): test-injectable git invoker; orchestrator params struct.
- **`RunBootstrap`** (`internal/workspace/bootstrap.go` new): the
  orchestrator body. Host check via `src.IsGitHub()` first; then
  `Applier.Create`; then `mcp.CreateSession` with
  `BranchPrefix: "niwa-bootstrap/"`; then scaffold + add + commit.
  Owns `instanceCreated` (R7 create-step) and `sessionCreated` (R7
  post-CreateSession commit-step) defers.
- **Session `BranchName` field** (`internal/mcp/session_lifecycle.go`):
  new `BranchName string` with `json:"branch_name,omitempty"`,
  `EffectiveBranchName()` method, and updated readers (`destroy` path,
  push-hint warnings).
- **`CreateSession` factored entry** (`internal/mcp/handlers_session.go`):
  factor `handleCreateSession` into `CreateSession(ctx, CreateSessionParams)`
  accepting `BranchPrefix` and a `GitInvoker`. Existing MCP handler
  becomes a thin wrapper preserving `session/<sid>` behavior.
- **Success-block emission** (`internal/cli/init.go`): R19 byte-for-byte
  format from PRD Appendix B; one blank line before/after; lines in
  exact order.
- **Landing-path file** (`internal/cli/init.go`): R20 `writeLandingPath`
  with the worktree absolute path on the bootstrap success path.
- **Functional test coverage**
  (`test/functional/features/`): `@critical` Gherkin scenarios per
  PRD Acceptance Criteria.
- **Documentation**: `docs/guides/` page or extension describing
  `--bootstrap`; README mention.

## Implementation Phases (from design)

Five phases per the design's Implementation Approach (lines 1024-1206
of DESIGN doc):

**Phase 1: Error classification foundation.** Typed `*github.StatusError`,
fifth-wrap fix at `snapshotwriter.go:503`, classifier helper, unit
tests covering precedence. User-visible state: no change (classifier
constructed but not yet called from `runInit`).

**Phase 2: Flag surface + prompt UX.** `--bootstrap` / `--no-bootstrap`
flags, R25 mutual exclusion, R2 name derivation, R13 prompt machinery,
classifier dispatch from `runInit`, stub bootstrap dispatch returning
"not implemented yet", exit-code surfacing per R23, `@critical` Gherkin
for 401/403/404/R25/R13 paths. User-visible state: adjacent failure
modes now produce case-specific messages; `--bootstrap` fails with the
stub error.

**Phase 3: Scaffold derivation + GitInvoker seam.**
`(*github.APIClient).GetRepo`, `ScaffoldOptions` + `ScaffoldFromSource`
(PRD Appendix A byte-for-byte), `GitInvoker` + `BootstrapParams` in
new `internal/workspace/bootstrap.go` (no `RunBootstrap` body yet),
soft-fail visibility lookup with R17 note. User-visible state: no
end-to-end change yet (scaffold unit-tested but unused).

**Phase 4: Bootstrap orchestrator + session BranchPrefix.** Add
`BranchName` to `SessionLifecycleState`; factor `handleCreateSession`
into `CreateSession(ctx, CreateSessionParams)` accepting
`BranchPrefix` + `GitInvoker`; readers use `EffectiveBranchName()`;
full `RunBootstrap` body with `instanceCreated` + `sessionCreated`
defers; replace Phase 2's stub; R19 success block; R20 landing-path.
User-visible state: end-to-end happy path works.

**Phase 5: End-to-end coverage + docs + Gherkin.** Full PRD AC matrix
in functional tests; unit tests for test-seam ACs (argv injection,
host-check ordering, classifier table, cleanup defers); `docs/guides/`
update; README. User-visible state: feature complete.

## Success Metrics

From PRD Acceptance Criteria (28 happy-path + adjacent-failure + flag
+ idempotency + rollback + scaffold + test-seam ACs) and from
design's Consequences:

- Happy path: `niwa init <name> --from <slug> --bootstrap` produces
  workspace + instance + cloned source + session worktree + bootstrap
  branch in a single command.
- 7 conflict sub-cases per R8 each emit case-specific Detail+Suggestion.
- Stepwise rollback at init/create/session-create produces the right
  end-state (R7).
- Adjacent failure modes (401, 403, 404, ambiguous) get
  case-specific messages via the typed `*github.StatusError` classifier.
- Scaffold matches PRD Appendix A byte-for-byte; `.gitkeep` present;
  Visibility derives from `Repo.Private` bool.
- Branch name `niwa-bootstrap/<sid>` stored in session state.
- Commit uses user's git identity (no `--author`, no `GIT_AUTHOR_*`).
- No `git push` invoked by niwa.
- No secrets land on disk.

## External Dependencies

- **Existing `config.NoMarkerError` and `config.AmbiguousMarkersError`
  types and predicates** (`internal/config/discover.go`). Stay
  unchanged; classifier dispatches on them.
- **Existing `*workspace.InitConflictError{Err, Detail, Suggestion}`
  pattern** (`internal/workspace/preflight.go`). Reused for all
  case-specific classifier output. Phase 2 adds an `ExitCode` field
  for R23 exit-code surfacing.
- **Existing `cli.IsStdinTTY()` and `cli.ReadConfirmation()`**
  (`internal/cli/prompt.go`). Reused for the R13 prompt.
- **Existing `exec.CommandContext("git", args...)` pattern**
  (e.g. `internal/workspace/clone.go`). `GitInvoker` interface
  follows it; default-constructed `stdGitInvoker{}` calls
  `exec.CommandContext` unchanged.
- **Existing `workspace.ResolveCloneURL(src, …)`** for slug → URL.
  `RunBootstrap` invokes it internally so host check (on `src`) and
  fetch URL agree.
- **Existing `Applier.Create`** (`internal/cli/apply.go` /
  `internal/workspace/apply.go`). `RunBootstrap` invokes the create
  pipeline as-is; channels infrastructure (R3/C1) auto-installs
  because the scaffold declares `[channels.mesh]`.
- **Existing `writeLandingPath`** (`internal/cli/landing.go`). R20
  consumed unchanged.
- **Existing `localGitServer`** and `tarballFakeServer` test
  helpers per the PRD's test-fixture conventions section.
- **Existing `Source.IsGitHub()`** (`internal/source/source.go:148`).
  Load-bearing — handles both empty Host (canonical slug form) and
  "github.com" explicit form. NEVER swap for literal-byte check.
