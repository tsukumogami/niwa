---
status: Proposed
problem: |
  The niwa critical path — init, create, apply — has zero functional test coverage.
  Two bugs shipped in v0.7.x that would have been caught immediately by e2e tests:
  ConfigSourceURL was not set for -2 instances (overlay not discovered, required keys
  unresolved), and failed creates left orphaned empty directories. Every release
  risks regressing the primary user workflow with no automated safety net.
decision: |
  Add a new feature file tests/functional/features/critical-path.feature exercised
  by the existing godog suite. A new localGitServer helper creates in-process bare
  git repos under the sandbox and returns file:// URLs; workspace.toml files use
  [repos.<name>] entries with explicit url fields rather than [[sources]] org
  discovery, eliminating the GitHub dependency. DeriveOverlayURL and OverlayDir are
  extended to handle file:// URLs so convention overlay discovery is testable
  end-to-end. Five @critical scenarios cover the happy path, the -2 instance
  regression, orphan cleanup, overlay-provided env resolution, and apply idempotency.
rationale: |
  Local bare repos over file:// are the minimal-friction approach that exercises
  real git clone operations without a network dependency or a test git server
  process. Extending DeriveOverlayURL for file:// is a small, justified production
  code change (file:// is a legitimate git protocol) that enables full convention
  overlay discovery testing rather than limiting tests to the explicit --overlay
  flag path. The existing godog infrastructure already provides sandboxed HOME and
  workspace root, making new scenarios cheap to add. Targeting the five specific
  regressions before adding broader coverage keeps the first tranche focused and
  mergeable.
---

# DESIGN: Functional tests for the critical path (init / create / apply)

## Status

Proposed

## Context and Problem Statement

`niwa init`, `niwa create`, and `niwa apply` form the primary user workflow: init
clones a workspace config from a remote, create materializes the workspace (discovers
or clones repos, installs CLAUDE context files), and apply re-runs the pipeline on
an existing instance. This path has no functional test coverage at all.

Two bugs shipped in the v0.7.x series that would have been caught immediately:

- **ConfigSourceURL regression (fixed in current branch):** `create.go` called
  `LookupWorkspace` with the mutated instance name (`codespar-2`) instead of the
  original config name (`codespar`). For every instance past the first, the registry
  lookup returned nil, ConfigSourceURL was never set on the applier, Branch 3 overlay
  discovery was skipped, and any env keys supplied by the overlay went unresolved —
  triggering the required-keys check and leaving an empty instance directory behind.

- **Orphan directory on failed create (fixed in current branch):** `Apply.Create`
  called `os.MkdirAll` before `runPipeline`, so a validation failure left the
  instance directory on disk as an empty artifact. Subsequent `niwa create` invocations
  refused to overwrite it, making recovery require manual directory deletion.

Neither regression was caught before release because the test suite covers only shell
navigation, completion, and install integration — not the pipeline that does the
actual work.

## Decision Drivers

- No GitHub dependency. Tests must run offline, in CI without special credentials,
  and on any developer machine. Real `git clone` must be exercised (not mocked).
- Fit the existing godog framework. The `test/functional/` suite already provides
  per-scenario sandbox isolation, a prebuilt binary, and a proven step vocabulary.
  New tests should feel native to that suite.
- Catch the two known regressions directly. A test for "-2 instance succeeds" must
  fail on the old code and pass on the fixed code.
- Convention overlay discovery must be testable end-to-end. Limiting overlay tests
  to the explicit `--overlay` flag path would leave the convention discovery branch
  permanently untested.
- Minimal production code change. The only production change permitted is extending
  `DeriveOverlayURL` and `OverlayDir` to handle `file://` URLs. Nothing else in the
  pipeline should change for test purposes.

## Considered Options

### Decision 1: Local git infrastructure for clone operations

**Option A — Pre-seeded directories with `.git` markers (skip clone)**
Create expected group/repo directories with empty `.git` subdirectories before
running `niwa create`. The `Cloner.Clone` implementation skips the clone when the
target directory already exists.

Trade-offs: zero setup complexity; does not exercise the clone code path. If clone
logic regresses (wrong URL derivation, wrong target path, network-vs-SSH protocol
selection), these tests would not catch it.

**Option B — Local bare repos with `file://` URLs (chosen)**
Use `git init --bare` to create bare repositories in the sandbox, then reference
them via `file:///abs/path/to/repo.git` URLs. Source repos are declared with
`[repos.<name>] url = "file:///..."` entries (explicit repo overrides) rather than
`[[sources]] org = "..."` (which requires GitHub API). Workspace config repos are
also local bare repos initialized with a `workspace.toml` commit.

Trade-offs: requires initializing bare repos and committing workspace configs in
test helpers; exercises the full clone code path; no network required; `file://`
is a standard git protocol that `git clone` handles without special configuration.

**Option C — In-process HTTP git server**
Run `git daemon` or a Go HTTP git server library (e.g., `go-git`) in a goroutine,
serve repos over `http://localhost:PORT`. `DeriveOverlayURL` would need to parse
`http://localhost:...` URLs.

Trade-offs: most realistic transport layer; adds a third-party dependency or a
complex in-process server; adds port-allocation complexity to test setup; `http://`
is not among the URL schemes `DeriveOverlayURL` already understands, requiring a
larger production code change.

**Chosen: Option B.** Real clone operations, no network, minimal production code
change, fits the existing sandbox model.

### Decision 2: Convention overlay discovery with file:// URLs

**Option A — Explicit `--overlay` flag only (no DeriveOverlayURL change)**
Tests always pass `niwa init --from <ws-url> --overlay <overlay-url>`. Convention
discovery (Branch 3) is never exercised.

Trade-offs: no production code change; leaves Branch 3 permanently untested;
the ConfigSourceURL regression lives entirely in Branch 3, so tests would not
directly exercise the fixed code path.

**Option B — Extend DeriveOverlayURL and OverlayDir for file:// (chosen)**
Add a `file://` case to `DeriveOverlayURL`: strip the `.git` suffix if present,
append `-overlay.git`. Example: `file:///sandbox/gitserver/ws.git` →
`file:///sandbox/gitserver/ws-overlay.git`. Extend `OverlayDir` to handle
`file://` by sanitizing the path into a directory name rather than calling
`parseOrgRepo`.

Trade-offs: small production code change (two switch cases, roughly 15 lines);
enables end-to-end testing of convention discovery; makes `niwa` usable with
local git setups outside tests.

**Chosen: Option B.** The Branch 3 path is where the ConfigSourceURL regression
lives. Testing it end-to-end is the point.

### Decision 3: Scenario scope for the first tranche

**Option A — Happy path only**
One scenario: init + create + verify repos. Fast to write, leaves regressions
uncovered.

**Option B — Five targeted scenarios (chosen)**
Init+create happy path, create -2 regression, orphan cleanup, overlay env
resolution, apply idempotency. Each scenario maps to a specific known failure
mode or invariant.

**Option C — Broad coverage (lifecycle, drift, destroy, status)**
Full workflow coverage. Too large for a first tranche; most of the surface has
no known regressions to anchor scenarios against.

**Chosen: Option B.** Focus on what's broken before expanding.

## Decision Outcome

The design adds one new feature file (`test/functional/features/critical-path.feature`),
one new Go helper file (`test/functional/localrepo_test.go`), and extends two
functions in `internal/config/overlay.go`. Existing `steps_test.go` gains three
new step definitions. The five `@critical` scenarios are run by
`make test-functional-critical`.

## Solution Architecture

### Component map

```
test/functional/
  features/
    critical-path.feature     NEW – five @critical scenarios
  localrepo_test.go           NEW – local bare repo creation helpers
  steps_test.go               EXTENDED – three new step defs + assertions
  suite_test.go               UNCHANGED

internal/config/
  overlay.go                  EXTENDED – file:// cases in DeriveOverlayURL + OverlayDir
```

### Local git server helper (`localrepo_test.go`)

```go
// localGitServer manages a directory of bare repos for one scenario.
type localGitServer struct {
    root string // absolute path, e.g. <sandbox>/gitserver/
}

// newLocalGitServer creates an empty server rooted under dir.
func newLocalGitServer(dir string) (*localGitServer, error)

// Repo creates a bare repo named <name>.git and returns its file:// URL.
func (s *localGitServer) Repo(name string) (fileURL string, err error)

// ConfigRepo creates a bare repo named <name>.git, commits workspace.toml
// with the given TOML body, and returns its file:// URL.
func (s *localGitServer) ConfigRepo(name, toml string) (fileURL string, err error)
```

`Repo` runs `git init --bare <root>/<name>.git`.  
`ConfigRepo` clones the bare repo into a temp dir, writes `workspace.toml`, commits
it, and pushes back — giving the bare repo one commit that `niwa init` can clone.

`localGitServer` is stored in `testState` so all steps in a scenario share the same
server root.

### New Gherkin steps

| Step | Description |
|------|-------------|
| `a local git server is set up` | Creates `localGitServer` in state |
| `a config repo "name" exists with body: <TOML>` | Calls `ConfigRepo`, stores URL in state keyed by name |
| `a source repo "name" exists` | Calls `Repo`, stores URL in state |
| `I run niwa init from config repo "name"` | Runs `niwa init --from <url>` |
| `I run niwa init from config repo "name" with overlay "overlay-name"` | Adds `--overlay <url>` |
| `the instance "name" exists` | Asserts directory at `<workspaceRoot>/<name>` exists |
| `the instance "name" does not exist` | Asserts directory at `<workspaceRoot>/<name>` is absent |
| `the repo "group/repo" exists in instance "name"` | Asserts `<workspaceRoot>/<inst>/<group>/<repo>` exists |

Existing steps (`I run "niwa create"`, `I run "niwa apply"`, `the exit code is N`,
`the error output contains "..."`) are reused unchanged.

### DeriveOverlayURL extension

```go
case strings.HasPrefix(s, "file://"):
    // file:///path/to/ws.git  →  file:///path/to/ws-overlay.git
    // file:///path/to/ws      →  file:///path/to/ws-overlay
    path := strings.TrimPrefix(s, "file://")
    path = strings.TrimSuffix(path, ".git")
    return "file://" + path + "-overlay.git", true
```

### OverlayDir extension

```go
case strings.HasPrefix(overlayURL, "file://"):
    // Derive a stable local dir name from the last path component.
    path := strings.TrimPrefix(overlayURL, "file://")
    path = filepath.Clean(strings.TrimSuffix(path, ".git"))
    dirName := "file-" + filepath.Base(path)
    return filepath.Join(configHome, "niwa", "overlays", dirName), nil
```

The `file-` prefix prevents collisions with `org-repo` style names from GitHub
shorthand URLs.

### Workspace.toml for test scenarios

Source repos use explicit `[repos.<name>]` entries with `url = "file:///..."` to
avoid GitHub API calls:

```toml
[workspace]
name = "testws"

[groups.main]
visibility = "public"

[repos.repo1]
url = "file:///PLACEHOLDER/repo1.git"
group = "main"

[repos.repo2]
url = "file:///PLACEHOLDER/repo2.git"
group = "main"
```

The `localGitServer.ConfigRepo` helper receives the TOML body with concrete
`file://` URLs already interpolated by the calling step — no substitution at commit
time. Steps that need source repo URLs call `server.Repo(name)` first to obtain the
URLs, then call `server.ConfigRepo(wsName, toml)` with the URLs embedded.

### Five scenarios (summary)

**Scenario 1 – Init + create happy path** `@critical`  
Given two source repos and a config repo. Run `niwa init`, then `niwa create`.  
Assert exit 0, instance directory exists, both repo directories cloned.

**Scenario 2 – Create -2 instance succeeds** `@critical`  
(Regression for ConfigSourceURL bug.) Given the same setup, run `niwa create` twice.  
Assert first run exit 0 and `testws` exists; second run exit 0 and `testws-2` exists
with the same repo layout.

**Scenario 3 – Failed create leaves no orphan directory** `@critical`  
Given a workspace config declaring a required env key with no supplied value.  
Run `niwa create`. Assert exit non-zero, error output contains "required env keys",
and the instance directory `testws` does not exist.

**Scenario 4 – Overlay-provided env key resolves on -2 instance** `@critical`  
Given a workspace config with a required env key "API_KEY" and a workspace-overlay
repo providing it as a plaintext value. Run `niwa init` with convention overlay
discovery (no `--overlay` flag; local server provides `<ws>-overlay.git`). Run
`niwa create` twice. Assert both runs exit 0.

**Scenario 5 – Apply is idempotent** `@critical`  
Given an existing instance (created by scenario 1). Run `niwa apply`. Assert exit 0
and managed files (CLAUDE.local.md) are re-written without error.

## Implementation Approach

**Phase 1 — Production code change**  
Extend `DeriveOverlayURL` and `OverlayDir` in `internal/config/overlay.go` to handle
`file://` URLs. Update `TestDeriveOverlayURL` in `overlay_test.go` with `file://`
cases.

**Phase 2 — Test infrastructure**  
Write `test/functional/localrepo_test.go` with `localGitServer`. Add `gitServer`
field to `testState`. Wire the Before hook to initialize it.

**Phase 3 — Step definitions**  
Add the eight new step definitions to `steps_test.go`. Register them in
`suite_test.go`'s `initializeScenario`.

**Phase 4 — Feature file**  
Write `test/functional/features/critical-path.feature` with the five scenarios.

**Phase 5 — Verification**  
Run `make test-functional-critical`. All five scenarios must pass. Run
`make test-functional` to confirm no regressions in existing scenarios.

## Security Considerations

The local git server creates bare repos under the per-scenario sandbox directory,
which is under the niwa-test/ subtree alongside the test binary. Paths are constructed
with `filepath.Join` (no string concatenation) so there is no path traversal risk.
The `file://` URL extension in production code introduces no new attack surface:
`file://` was already a valid git clone target; the change only makes
`DeriveOverlayURL` aware of it. No credentials are involved. Test sandboxes are
cleaned up by `make test-functional`'s `rm -rf .niwa-test` after the suite completes.

## Consequences

**Positive**
- The two known regressions (ConfigSourceURL lookup, orphan directory) are caught
  automatically on every PR.
- Real `git clone` operations are exercised, meaning clone-path regressions (wrong
  URL derivation, wrong target directory) are also detectable.
- New contributors can add scenarios for future regressions without learning a
  separate mock framework.

**Negative**
- Each scenario that exercises `git init --bare` + `git clone` takes slightly longer
  than a pure in-memory test. Expected scenario runtime: 200–500ms per scenario on
  a cold filesystem.
- The `DeriveOverlayURL` and `OverlayDir` changes ship in production code. They are
  covered by unit tests and do not affect the existing behavior for GitHub or
  shorthand URLs.

**Mitigations**
- The five `@critical` scenarios run in under 3 seconds total (`make
  test-functional-critical`), so CI gate latency is acceptable.
- `file://` handling is gated behind a string prefix check, isolated from all
  existing URL parsing paths.
