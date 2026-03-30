---
status: Proposed
problem: |
  niwa clones repos during create/apply but can't run repo-provided setup scripts
  afterward. Some repos need git hooks installed, local config generated, or dev
  environments bootstrapped. Today this requires a separate manual step or an
  external installer. niwa should support running a repo's own setup scripts as
  part of the apply pipeline.
decision: |
  Scan a configurable setup directory (default: scripts/setup/) in each repo for
  executable scripts and run them in lexical order. Workspace-level config changes
  the directory name; per-repo override changes or disables. Non-zero exit codes
  produce warnings, not fatal errors.
rationale: |
  A directory convention is more extensible than a single file -- repos can split
  setup across multiple scripts without merge conflicts or monolithic files. The
  generic directory name (scripts/setup/) doesn't imply niwa ownership, so repos
  can use the same convention with or without niwa. Lexical ordering via numeric
  prefixes (01-git-hooks.sh, 02-build.sh) follows established Unix patterns.
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
workspace" promise. niwa should be able to run a repo's own setup scripts as part
of the apply pipeline, after cloning and materialization are complete.

This is distinct from niwa's materializers, which distribute config FROM the
workspace config repo TO target repos. Post-clone scripts run code that lives
INSIDE the target repo.

## Decision Drivers

- Convention over configuration: repos that follow the convention need zero config
- Non-intrusive: the convention should use generic names, not niwa-specific ones
- Extensible: repos with multiple setup concerns shouldn't be forced into one file
- Non-destructive: a failing setup script shouldn't block the entire workspace apply
- Idempotent: scripts must be safe to re-run on every `niwa apply`
- Per-repo control: some repos may need a different setup path or no setup at all

## Considered Options

### Decision 1: Script discovery convention

How niwa discovers which scripts to run in each repo after cloning/applying.

#### Chosen: Setup directory with lexical ordering

niwa scans a directory in each repo for executable scripts and runs them in
lexical order. The default directory is `scripts/setup/`. Repos organize setup
into as many scripts as they need; each runs independently.

```
myrepo/
  scripts/
    setup/
      01-git-hooks.sh
      02-install-deps.sh
      03-generate-config.sh
```

**TOML configuration:**

```toml
[workspace]
setup_dir = "scripts/setup"    # default, can be omitted

[repos.legacy-app]
setup_dir = "setup/bootstrap"  # different directory for this repo

[repos.static-site]
setup_dir = ""                 # disable for this repo
```

**Convention details:**
- Default directory: `scripts/setup/`
- Scripts must be executable (`chmod +x`)
- Non-executable files are skipped with a warning
- Empty directory or missing directory: silently skipped
- Lexical order: `01-foo.sh` runs before `10-bar.sh` (numeric prefix convention)
- Only top-level files are scanned (no recursive descent into subdirectories)
- Empty string on per-repo override explicitly disables

**Why this approach:**
- Generic name (`scripts/setup/`) doesn't imply niwa ownership -- repos can use
  this convention with any tool or manually
- Multiple scripts compose naturally -- add a file, get a new setup step
- Lexical ordering via numeric prefixes is a well-established Unix pattern
  (`/etc/cron.d/`, `/etc/init.d/`, `run-parts`)
- Repos with a single setup step just put one script in the directory

#### Alternatives Considered

**Single well-known file (`scripts/niwa-setup.sh`):** One file, one entry point.
Rejected because (1) the `niwa-` prefix is intrusive -- repos shouldn't need
niwa-specific files, (2) a single file doesn't compose -- repos with multiple
setup concerns end up with a monolithic script that grows over time, and (3)
multiple contributors editing the same file creates merge conflicts.

**Hybrid entry point + directory (`setup.sh` + `setup.d/`):** A single entry
point runs first, then directory scripts. Rejected because it adds complexity
without clear benefit -- if you have an entry point, you'll put everything there
and the directory becomes an afterthought. One convention is simpler than two.

**Single configurable file path:** Workspace declares a file path, niwa runs
that one file. Most flexible for per-repo override, but the same extensibility
problem as the single-file approach -- one file doesn't compose.

### Decision 2: Execution semantics

How scripts are invoked, what environment they receive, and how failures are
handled.

#### Chosen: Run from repo root, warn on failure, stop on first script error

Scripts are executed with:
- Working directory: the repo root
- Invocation: direct execution (script's shebang determines interpreter)
- Environment: inherits the niwa process environment
- Stdout/stderr: printed to niwa's output, prefixed with the repo name
- Exit code 0: success
- Exit code non-zero: warning printed, **remaining scripts for that repo are
  skipped**, pipeline continues with next repo

```
Running setup for tsuku...
  [01-git-hooks.sh] Installing git hooks... done.
  [02-install-deps.sh] Installing dependencies... done.
Running setup for koto...
  [01-git-hooks.sh] Warning: failed (exit code 1). Skipping remaining scripts.
Running setup for niwa... (no setup directory)
```

**Why stop-on-error per repo:** If `01-git-hooks.sh` fails, running
`02-install-deps.sh` may not make sense (scripts often have implicit ordering).
But one repo's failure shouldn't block other repos from setting up. This gives
fail-fast within a repo and resilience across repos.

**Why shebang, not `sh -e`:** Scripts choose their own interpreter via shebang
(`#!/bin/bash`, `#!/usr/bin/env python3`). This is more flexible than forcing
`sh -e`, and aligns with how executable scripts work everywhere else.

#### Alternatives Considered

**Fatal on any failure:** Stop the entire apply pipeline if any script fails.
Rejected because it makes the pipeline fragile -- one repo's broken script
blocks all other repos.

**Continue all scripts on failure:** Run every script regardless of exit codes,
report all failures at the end. Rejected because scripts within a repo often
have ordering dependencies -- running later scripts after an earlier failure
can produce confusing results.

## Decision Outcome

Post-clone scripts use a directory-based convention: niwa scans
`scripts/setup/` (configurable) in each repo for executable scripts and runs
them in lexical order. The directory name is generic and not niwa-specific, so
repos can use the same convention independently.

Failures stop remaining scripts for that repo but don't block other repos.
The workspace-level `setup_dir` changes the default directory. Per-repo overrides
change or disable with empty string. Missing or empty directories are silently
skipped.

## Solution Architecture

### Overview

A new pipeline step runs after materializers (Step 6.5) and before managed file
tracking (Step 7). It iterates classified repos, resolves the setup directory
path, scans for executable scripts, and runs each in lexical order.

### Components

**`WorkspaceMeta.SetupDir`** -- new optional field on workspace metadata.
Defaults to `scripts/setup` when empty.

**`RepoOverride.SetupDir`** -- new optional field. When set, overrides the
workspace default for that repo. Empty string disables. Uses `*string` to
distinguish "not set" from "explicitly empty."

**`RunSetupScripts`** -- new function that scans a directory for executable
scripts and runs them in lexical order.

### Key Interfaces

```go
// In config.go
type WorkspaceMeta struct {
    // ... existing fields ...
    SetupDir string `toml:"setup_dir,omitempty"`
}

type RepoOverride struct {
    // ... existing fields ...
    SetupDir *string `toml:"setup_dir,omitempty"`
}
```

```go
// In setup.go
type SetupResult struct {
    RepoName string
    Scripts  []ScriptResult
    Skipped  bool  // directory not found
    Disabled bool  // explicitly disabled
}

type ScriptResult struct {
    Name   string
    Error  error  // nil = success
}

func RunSetupScripts(repoDir, setupDir string) *SetupResult
```

### Data Flow

```
For each classified repo:
    |
    +-- Resolve setup directory:
    |     repo override (*string set) -> use override value
    |     repo override (nil)         -> use workspace default
    |     workspace default empty     -> use "scripts/setup"
    |
    +-- Is resolved path ""?
    |     yes -> skip (disabled)
    |
    +-- Does directory exist at repoDir/setupDir?
    |     no  -> skip silently
    |
    +-- Scan for executable files (top-level only, lexical order)
    |     none found -> skip silently
    |
    +-- For each script in order:
          execute (cwd = repoDir)
          exit 0   -> continue to next script
          exit !0  -> warn, skip remaining scripts for this repo
```

## Implementation Approach

### Phase 1: Config types and resolution

- Add `SetupDir string` to `WorkspaceMeta`
- Add `SetupDir *string` to `RepoOverride`
- Add resolution function (repo override -> workspace default -> "scripts/setup")
- Tests for config parsing and resolution

### Phase 2: Script execution and pipeline integration

- Implement `RunSetupScripts` (scan dir, filter executable, run in order)
- Add Step 6.75 to apply pipeline
- Print progress lines per repo and per script
- Tests: scripts exist and succeed, directory missing (skip), script fails
  (warn + skip remaining), disabled via empty string, non-executable file
  (warn + skip), empty directory (skip)

## Security Considerations

Post-clone scripts run arbitrary code from the cloned repo. This is inherently
trusted -- the user chose to clone the repo, and the scripts are part of the
repo's codebase. niwa surfaces what it's about to run (prints each script name
before execution) so the user can verify.

The security boundary is the same as `git clone` itself: if you clone a repo, you
trust its contents. niwa doesn't elevate privileges -- scripts run as the current
user with the current environment.

Mitigations:
- Only executable files are run (non-executable are warned, not silently executed)
- No recursive descent into subdirectories (limits discovery scope)
- Script paths are validated to stay within the repo directory

## Consequences

### Positive

- "One command to set up the workspace" becomes achievable
- Repos that follow the convention need zero config
- Multiple setup concerns compose naturally (one script per concern)
- Generic directory name works with or without niwa
- Idempotent execution means apply always converges

### Negative

- Lexical ordering requires discipline (numeric prefixes)
- Adding a script to the directory silently changes behavior on next apply
- No structured output from scripts -- niwa can only report pass/fail

### Mitigations

- Numeric prefix convention is well-documented and widely understood
- niwa prints each script name before execution, so changes are visible
- Per-repo disable provides an escape hatch when auto-execution is unwanted
