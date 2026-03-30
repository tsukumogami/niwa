---
status: Proposed
problem: |
  niwa clones repos during create/apply but can't run repo-provided setup scripts
  afterward. Some repos need git hooks installed, local config generated, or dev
  environments bootstrapped. Today this requires a separate manual step or an
  external installer. niwa should support running a repo's own setup script as
  part of the apply pipeline.
decision: |
  Add a workspace-level setup_script convention (default: scripts/niwa-setup.sh)
  that niwa runs after materializers for each repo where the script exists.
  Per-repo overrides can change the path or set it to empty to disable. Scripts
  run from the repo directory with the working directory set to the repo root.
  Non-zero exit codes are reported as warnings, not fatal errors.
rationale: |
  Convention-over-configuration matches niwa's philosophy -- repos that follow the
  convention need zero config. The non-fatal approach avoids blocking the entire
  apply pipeline when one repo's setup script fails, while still surfacing the
  failure clearly. Per-repo override to empty string gives a clean disable mechanism.
---

# DESIGN: Post-Clone Scripts

## Status

Proposed

## Context and Problem Statement

niwa's apply pipeline clones repos, installs content (CLAUDE.md hierarchy), and
runs materializers (hooks, settings, env, files). But some repos need additional
setup that's specific to the repo itself -- installing git hooks via a repo-provided
script, generating local configuration files, or bootstrapping development tooling.

Today these setup steps happen outside niwa: either manually by the developer, or
through a separate installer script. This breaks the "one command to set up the
workspace" promise. niwa should be able to run a repo's own setup script as part
of the apply pipeline, after cloning and materialization are complete.

This is distinct from niwa's materializers, which distribute config FROM the
workspace config repo TO target repos. Post-clone scripts run code that lives
INSIDE the target repo.

## Decision Drivers

- Convention over configuration: repos that follow the convention need zero config
- Non-destructive: a failing setup script shouldn't block the entire workspace apply
- Idempotent: scripts must be safe to re-run on every `niwa apply`
- Transparent: users should see what scripts are being executed
- Per-repo control: some repos may need a different script path or no script at all

## Considered Options

### Decision 1: Script path configuration and convention

How to declare which script niwa should run in each repo, and what the default
convention is.

#### Chosen: Workspace-level default with per-repo override

A single workspace-level setting declares the default script path. Per-repo
overrides can change the path or disable it.

```toml
[workspace]
setup_script = "scripts/niwa-setup.sh"   # default, can be omitted

[repos.legacy-app]
setup_script = "setup/bootstrap.sh"      # different path for this repo

[repos.static-site]
setup_script = ""                        # disable for this repo
```

The default value is `scripts/niwa-setup.sh` when `setup_script` is not set.
This follows the convention-over-configuration pattern used throughout niwa.
Repos that want setup just create the file at the conventional path; repos
that don't need setup do nothing (the script is silently skipped when absent).

**Convention details:**
- Default path: `scripts/niwa-setup.sh`
- The script must be executable (`chmod +x`)
- Scripts that don't exist are silently skipped
- Empty string explicitly disables (even if the default-path file exists)

#### Alternatives Considered

**No default convention (always explicit):** Require every workspace to declare
`setup_script` for each repo that needs one. Rejected because it defeats
convention-over-configuration -- most repos that need setup will use a standard
path, and requiring explicit config for each is boilerplate.

**Auto-discover multiple scripts:** Scan for all `*.sh` files in a setup directory
and run them in alphabetical order. Rejected because it's unpredictable -- adding
a file to a directory silently changes behavior. A single well-known entry point
is more transparent.

### Decision 2: Execution semantics

How scripts are invoked, what environment they receive, and how failures are
handled.

#### Chosen: Run from repo root, warn on failure

Scripts are executed with:
- Working directory: the repo root
- Shell: `sh -e` (POSIX shell, exit on error)
- Environment: inherits the niwa process environment
- Stdout/stderr: printed to niwa's output, prefixed with the repo name
- Exit code 0: success (no output beyond the script's own output)
- Exit code non-zero: warning printed, apply continues with remaining repos

```
Running setup script for tsuku... (scripts/niwa-setup.sh)
  Installing git hooks...
  Done.
Running setup script for koto... (scripts/niwa-setup.sh)
  Warning: setup script failed (exit code 1)
Running setup script for niwa... (skipped, not found)
```

**Why warnings, not errors:** A workspace with 6 repos shouldn't fail entirely
because one repo's setup script has a transient issue. The user sees the warning
and can fix it manually. The remaining repos still get their setup.

**Why `sh -e`:** Scripts should fail fast on individual command errors rather than
silently continuing after a failure. The `-e` flag ensures this. Scripts that need
to handle errors can use `set +e` explicitly.

#### Alternatives Considered

**Fatal on failure:** Stop the entire apply pipeline if any setup script fails.
Rejected because it makes the pipeline fragile -- one repo's broken setup script
blocks all other repos from getting set up. Partial success is better than
complete failure.

**Run only on first clone (not on re-apply):** Track which repos have had their
setup script run and skip on subsequent applies. Rejected because the issue
explicitly requires idempotent execution on every apply -- setup scripts should
be written to handle re-runs, and running them ensures the repo stays in a good
state after config changes.

## Decision Outcome

Post-clone scripts use a convention-based approach: niwa looks for
`scripts/niwa-setup.sh` (configurable) in each repo after materialization. If
the script exists and is executable, niwa runs it from the repo root. Non-zero
exit codes produce warnings but don't block the pipeline.

The workspace-level `setup_script` sets the default path. Per-repo overrides
change the path or disable with empty string. Scripts that don't exist are
silently skipped, so repos that don't need setup pay no configuration cost.

## Solution Architecture

### Overview

A new pipeline step runs after materializers (Step 6.5) and before managed file
tracking (Step 7). It iterates classified repos, resolves the setup script path,
checks for existence, and executes if found.

### Components

**`WorkspaceMeta.SetupScript`** -- new optional field on the workspace metadata.
Defaults to `scripts/niwa-setup.sh` when empty.

**`RepoOverride.SetupScript`** -- new optional field. When set, overrides the
workspace default for that repo. Empty string disables.

**`RunSetupScript`** -- new function that executes a script from a repo directory
and returns success/warning.

### Key Interfaces

```go
// In config.go
type WorkspaceMeta struct {
    // ... existing fields ...
    SetupScript string `toml:"setup_script,omitempty"`
}

type RepoOverride struct {
    // ... existing fields ...
    SetupScript *string `toml:"setup_script,omitempty"`
}
```

Note: `RepoOverride.SetupScript` is `*string` (pointer) to distinguish "not set"
(use workspace default) from "explicitly empty" (disable). This is the same
pattern used by `ClaudeConfig.Enabled`.

```go
// In apply.go or a new setup.go
type SetupResult struct {
    RepoName string
    Script   string
    Skipped  bool   // script not found
    Disabled bool   // explicitly disabled via empty string
    Error    error  // non-nil means warning (non-zero exit)
}

func RunSetupScript(repoDir, scriptPath string) *SetupResult
```

### Data Flow

```
For each classified repo:
    |
    +-- Resolve script path:
    |     repo override (pointer set) -> use override value
    |     repo override (nil)         -> use workspace default
    |     workspace default empty     -> use "scripts/niwa-setup.sh"
    |
    +-- Is resolved path ""?
    |     yes -> skip (disabled)
    |
    +-- Does script exist at repoDir/scriptPath?
    |     no  -> skip silently
    |
    +-- Is script executable?
    |     no  -> warn and skip
    |
    +-- Execute: sh -e scriptPath (cwd = repoDir)
    |     exit 0   -> success
    |     exit !0  -> warn, continue
```

## Implementation Approach

### Phase 1: Config types

- Add `SetupScript string` to `WorkspaceMeta`
- Add `SetupScript *string` to `RepoOverride`
- Add resolution logic to `MergeOverrides` or a standalone resolver
- Tests for config parsing and override resolution

### Phase 2: Script execution and pipeline integration

- Implement `RunSetupScript` function
- Add Step 6.75 to the apply pipeline (after materializers, before managed files)
- Print progress lines per repo
- Tests: script exists and succeeds, script doesn't exist (skip), script fails
  (warning), disabled via empty string, non-executable script

## Security Considerations

Post-clone scripts run arbitrary code from the cloned repo. This is inherently
trusted -- the user chose to clone the repo, and the script is part of the repo's
codebase. niwa surfaces what it's about to run (prints the script path before
execution) so the user can verify.

The security boundary is the same as `git clone` itself: if you clone a repo, you
trust its contents. niwa doesn't elevate privileges -- scripts run as the current
user with the current environment.

One mitigation: niwa checks that the script is executable before running it. A
non-executable script is a warning, not a silent skip, so the user knows the
convention exists but the script needs `chmod +x`.

## Consequences

### Positive

- "One command to set up the workspace" becomes achievable
- Repos that follow the convention need zero config
- Idempotent execution means apply always converges to the right state
- Non-fatal failures keep the pipeline resilient

### Negative

- Running arbitrary scripts adds a trust surface (mitigated by transparency)
- Scripts must be idempotent, which is the repo author's responsibility
- No structured output from scripts -- niwa can only report pass/fail

### Mitigations

- Print script path before execution for transparency
- Warn on non-executable scripts so authors know to chmod +x
- Document the idempotency requirement in the setup script convention
