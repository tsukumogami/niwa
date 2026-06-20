---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-niwa-default-worktree.md
milestone: "niwa default worktree mechanism"
issue_count: 7
---

# PLAN: niwa as the default worktree mechanism

## Status

Active

## Scope Summary

Implement the design's worktree-delegation integration: route Claude Code
agent-initiated worktree creation through niwa via per-repo
`WorktreeCreate`/`WorktreeRemove` hooks that call a new `niwa worktree from-hook`
subcommand, with an apply-time version probe choosing between the hook and a
deny+steer fallback, and an init-time opt-out. Lands as a single PR.

## Decomposition Strategy

**Horizontal.** The feasibility spike already proved the end-to-end path
(WorktreeCreate fires, replaces default creation), so integration risk is low and
a walking skeleton's early-integration payoff is small. The components have clear
interfaces: the `--json` output and the cwd-resolver are independent leaf pieces;
`from-hook` composes them; the materializer wiring and version probe layer on top;
the opt-out gates the install; functional coverage caps it. Each issue builds one
component with its unit tests, in dependency order.

Value note (single-pr): the whole PR is the unit of value — a niwa workspace where
agent worktrees become niwa worktrees by default. No individual issue ships
standalone user value, which is why this is one PR, not several.

## Issue Outlines

### Issue 1: feat(worktree): cwd-aware create, --json, and destroy --by-path

**Goal**: Make the human worktree commands the shared core the hook reuses — infer
the repo from cwd, emit a machine-readable path, and destroy by path (design
Decisions 1, 2; PRD R4).

**Acceptance Criteria**:
- [ ] `niwa worktree create` accepts an optional repo arg; when omitted it infers
  the repo from the process cwd (via the resolver from Issue 2), and purpose is
  optional. A bare `niwa worktree create` from inside a repo works.
- [ ] `niwa worktree create --json` prints a stable JSON object with the absolute
  worktree path and session id; default (non-`--json`) output is unchanged.
- [ ] `niwa worktree destroy --by-path <path>` resolves the path to a session and
  destroys it.
- [ ] Unit tests cover the JSON shape, cwd-inference, and `--by-path` resolution.

**Dependencies**: Blocked by <<ISSUE:2>>
**Type**: code
**Files**: `internal/cli/session_lifecycle_cmd.go`

### Issue 2: feat(workspace): add canonicalizing cwd-to-repo-name resolver

**Goal**: Resolve a hook-supplied `cwd` path to a known workspace repo name,
safely (design Solution Architecture; PRD R3).

**Acceptance Criteria**:
- [ ] New resolver maps an absolute `cwd` to the owning workspace repo name.
- [ ] Both `cwd` and each candidate repo path are canonicalized with
  `filepath.EvalSymlinks` + `Clean` before a longest-prefix comparison.
- [ ] A `cwd` outside every workspace repo is rejected with a clear error.
- [ ] Unit tests cover the happy path plus `..`-bearing and symlinked `cwd`
  rejection.

**Dependencies**: None
**Type**: code
**Files**: `internal/workspace/`

### Issue 3: feat(worktree): add `niwa worktree from-hook` (create + remove)

**Goal**: The thin hook entry, invoked directly by Claude (no shim script), that
routes Claude's WorktreeCreate / WorktreeRemove to the shared core (design
Decisions 1 and 3; PRD R1, R5, R6, R10).

**Acceptance Criteria**:
- [ ] `from-hook` reads the hook JSON on stdin and dispatches on
  `hook_event_name`; it is invoked directly as `niwa worktree from-hook` (the hook
  `command` is an absolute-path binary invocation, not a shim script).
- [ ] Create: resolves the repo from the stdin `cwd` via the resolver (Issue 2),
  runs the two-step flow `CreateSession` + `applyContentToWorktree` (secrets +
  CLAUDE context; R10 warn-and-continue surfaced), prints the absolute worktree path
  to stdout, exits 0; resolver failure or any create error exits non-zero.
- [ ] Remove: maps the worktree to a niwa session by `WorktreePath`
  (`ListSessionLifecycleStates` scan; Claude `session_id` is not niwa's sid),
  releases the agent's attach lock, attempts `DestroySession(force=false)`, and on
  a genuine dirty rejection logs-and-retains rather than force-deleting; always
  exits 0.
- [ ] `name` is passed as argv (never shell-interpolated) and control characters
  are stripped.
- [ ] The `WorktreeRemove` stdin field carrying the worktree path is confirmed
  empirically (or `cwd` is used as the fallback) and documented.
- [ ] Unit tests cover create and remove with synthetic hook JSON, including
  out-of-workspace `cwd` rejection.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>
**Type**: code
**Files**: `internal/cli/`, `internal/worktree/`

### Issue 4: feat(workspace): add claude --version harness probe

**Goal**: Detect at apply time whether the harness honors per-repo worktree hooks
(design Decision 4; PRD R7).

**Acceptance Criteria**:
- [ ] A probe runs `claude --version`, parses the version, and compares it to the
  baseline (v2.1.183).
- [ ] Returns supported / unsupported; treats a probe error or missing `claude`
  optimistically as supported.
- [ ] Unit tests cover at-baseline, below-baseline, unparseable, and
  missing-binary cases.

**Dependencies**: None
**Type**: code
**Files**: `internal/workspace/`

### Issue 5: feat(apply): install the worktree integration per-repo (hook or deny) with fallback disclosure

**Goal**: Wire the integration into the apply materializers — hook when supported,
deny+steer when not — disclosed on every apply (design Decisions 4 and 6; PRD R2,
R3, R8, R11, R12).

**Acceptance Criteria**:
- [ ] When the probe (Issue 4) reports supported, `SettingsMaterializer` writes the
  `WorktreeCreate`/`WorktreeRemove` hook entries into each repo's
  `settings.local.json`, each as an absolute-path `niwa worktree from-hook` command
  Claude invokes directly (no shim script, no `HooksMaterializer` change).
- [ ] When unsupported, it instead writes
  `permissions.deny: ["EnterWorktree","ExitWorktree"]` plus steer-to-niwa guidance
  (a new `permissions.deny` capability for the materializer); hook and deny are
  never both written.
- [ ] Fallback mode is disclosed on every `niwa apply` plus a one-time
  first-encounter explainer.
- [ ] Install runs through the non-interactive apply path and is idempotent across
  re-applies (no duplication).
- [ ] Unit tests cover the supported branch, the deny branch, and idempotent
  re-apply.

**Dependencies**: Blocked by <<ISSUE:3>>, <<ISSUE:4>>
**Type**: code
**Files**: `internal/workspace/materialize.go`, `internal/workspace/apply.go`

### Issue 6: feat(init): add --no-worktree-delegation opt-out

**Goal**: Let a developer opt a workspace instance out of the integration at init
time (design Decision 5; PRD R9).

**Acceptance Criteria**:
- [ ] `niwa init --no-worktree-delegation` persists a bool in `InstanceState`,
  mirroring `SkipGlobal` / `NoOverlay`.
- [ ] When set, apply skips the entire integration block (no hook, no deny, no
  probe).
- [ ] Re-running init without the flag, then `niwa apply`, installs the integration
  (reversible).
- [ ] Unit tests cover opt-out gating and reversal.

**Dependencies**: Blocked by <<ISSUE:5>>
**Type**: code
**Files**: `internal/cli/init.go`, `internal/workspace/state.go`, `internal/workspace/apply.go`

### Issue 7: test(functional): @critical worktree-delegation end-to-end coverage

**Goal**: Functional coverage of the integration across the supported, fallback,
and opt-out paths (niwa testing convention for init→create→apply changes).

**Acceptance Criteria**:
- [ ] A `@critical` Gherkin scenario: after `niwa apply`, an agent-style worktree
  action yields a niwa worktree (listed by `niwa worktree list`, with the
  materialized secret env and CLAUDE-context files present) and no bare
  `.claude/worktrees/` checkout.
- [ ] A scenario covering the deny path when the probe reports unsupported.
- [ ] A scenario covering the opt-out installing neither hook nor deny.
- [ ] Uses the `localGitServer` offline bare-repo fakes; runs under
  `make test-functional-critical`.

**Dependencies**: Blocked by <<ISSUE:5>>, <<ISSUE:6>>
**Type**: task
**Files**: `test/functional/features/`

## Dependency Graph

Single-pr plan: dependencies are captured inline in each Issue Outline's
**Dependencies** field and summarized in Implementation Sequence below. No GitHub
issue graph is rendered (no issues are created in single-pr mode).

## Implementation Sequence

- **Parallelizable first wave**: Issues 2 (cwd resolver) and 4 (version probe) have
  no dependencies and can be built concurrently.
- **Critical path**: 2 → 1 → 3 → 5 → 6 → 7. Issue 1 (cwd-aware create / `--json` /
  `destroy --by-path`) needs the resolver; Issue 3 (`from-hook`) builds on the
  resolver and the shared core; Issue 5 (apply wiring) needs the subcommand and the
  probe; Issue 6 (opt-out) gates Issue 5's install block; Issue 7 (functional
  coverage) exercises the full integration including the opt-out.
- **Riskiest issues** (build with the most care): Issue 3 (teardown data-safety and
  cwd security) and Issue 5 (touches per-repo settings on every apply). Both are
  `critical`-shaped; the unit tests in each, plus the functional coverage in Issue
  7, are the safety net.
- **Known implementation risk carried from the design**: the `WorktreeRemove` stdin
  schema was not exercised by the spike. Issue 3 confirms which field carries the
  worktree path (falling back to `cwd`) before relying on it.
