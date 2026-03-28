---
status: Proposed
problem: |
  niwa has no way to create a new workspace. Users must manually create
  .niwa/workspace.toml and content files. There's no scaffolding, no way
  to pull a shared config from GitHub, and no registration in the global
  registry. The init command is the entry point to the entire niwa workflow.
decision: |
  Three init modes: scaffold (local, commented template), named (registry
  lookup or scaffold), and remote (shallow git clone into .niwa/). Fail-fast
  on conflicts with targeted error messages. No --force flag initially.
rationale: |
  Shallow git clone makes .niwa/ a proper repo for future niwa update support.
  Commented template teaches the schema in-place without requiring external docs.
  Fail-fast prevents silent overwrites and catches common mistakes like running
  init inside an existing instance.
---

# DESIGN: Init command

## Status

Proposed

## Context and Problem Statement

niwa can apply a workspace config (`niwa apply`) but has no way to create one. A developer starting a new workspace must hand-write `.niwa/workspace.toml` and set up the content directory manually. There's also no way to pull a shared workspace config from a GitHub repo, which means team onboarding requires copying files around.

The init command needs to support three modes:
1. `niwa init` (no args) -- scaffold a minimal workspace.toml with defaults
2. `niwa init <name>` -- create a named workspace, using the registry if the name is already registered (pulls from remote), or scaffold locally if not
3. `niwa init <name> --from <org/repo>` -- clone a config repo from GitHub into .niwa/ and register it

The global registry at `~/.config/niwa/config.toml` already exists (`internal/config/registry.go`). The init command reads from it, writes to it, and uses it for name resolution.

## Decision Drivers

- **Three modes required**: no-args scaffold, named init, remote clone -- each has different UX
- **Registry already exists**: `internal/config/registry.go` has LoadGlobalConfig, SaveGlobalConfig, LookupWorkspace, SetRegistryEntry
- **.niwa/ is the config home**: workspace.toml and content files live in .niwa/ at the workspace root (Decision 7 from workspace-config design)
- **Config repo is a git checkout**: when from remote, .niwa/ is the git checkout of the config repo
- **Safe defaults**: don't overwrite existing workspace.toml, refuse if .niwa/ already exists
- **PRD R7**: init command spec from the PRD defines the three modes and their behavior
- **Future support for `niwa update`**: .niwa/ must be a git repo when cloned from remote

## Considered Options

### Decision 1: Remote config fetch mechanism

When a user runs `niwa init <name> --from <org/repo>`, niwa needs to fetch the config repo and place it at `.niwa/` in the current directory. The directory must support `niwa update` later, which requires git history and remote tracking.

Key assumptions: git is available on the user's system (already a dependency for repo cloning). Config repos are small (TOML files and markdown templates). The existing `Cloner` in `internal/workspace/clone.go` wraps git operations and can be extended.

#### Chosen: Shallow git clone

Use `git clone --depth 1` to clone the config repo directly as `.niwa/`. When a specific tag is provided, pass it as `--branch`. For commit pinning, clone the default branch then `git checkout <commit>`.

The flow:
1. Resolve clone URL from `<org/repo>` using the global config's `CloneProtocol` (HTTPS or SSH).
2. If `--ref <tag>` is specified, pass `--branch <tag>` to the shallow clone.
3. If `--review` is set, clone to a temp directory, display workspace.toml, prompt for confirmation, then move to `.niwa/` or abort.
4. Register the source in the global registry.

#### Alternatives considered

**GitHub API tarball download**: Download via API and extract into `.niwa/`. Rejected because it produces a plain directory with no git history -- `niwa update` would need to re-download and diff/replace files instead of using git pull. Also couples niwa to GitHub's API specifically.

**`gh repo clone`**: Delegates to GitHub's CLI. Rejected because it adds an external dependency users may not have, and provides no advantage over raw git clone since niwa already constructs clone URLs.

**Full git clone**: Works but fetches unnecessary history. Shallow clone is strictly better with no downside since config repos are small. `niwa update` can deepen the clone if needed.

### Decision 2: Scaffold template for local init

When a user runs `niwa init` or `niwa init <name>` without `--from`, niwa scaffolds a workspace.toml locally. The scaffold should teach the schema through examples while being immediately parseable.

#### Chosen: Commented template with active workspace block

Only `[workspace]` is active (the sole required section). All other sections are commented-out examples showing realistic values. The scaffold is immediately parseable -- `niwa apply` on it produces a no-op with a message about no configured sources.

Two variants:
- `niwa init <name>` sets `name = "<name>"`
- `niwa init` (no args, detached) sets `name = "workspace"` as a default

The scaffold also creates an empty `claude/` directory (matching the default `content_dir`) so users don't hit "directory not found" when uncommenting content entries.

```toml
[workspace]
name = "my-project"
# version = "0.1.0"
default_branch = "main"
content_dir = "claude"

# --- Sources: GitHub orgs to discover repos from ---
# Uncomment and configure at least one source before running niwa apply.
#
# [[sources]]
# org = "my-org"

# --- Groups: classify repos into directories ---
# [groups.public]
# visibility = "public"
#
# [groups.private]
# visibility = "private"

# --- Per-repo overrides ---
# [repos.my-repo]
# claude = false

# --- Content hierarchy ---
# [content.workspace]
# source = "workspace.md"

# --- Hooks, settings, environment, channels ---
# See docs/designs/DESIGN-workspace-config.md for full schema reference.
# [hooks]
# [settings]
# [env]
# [channels]
```

#### Alternatives considered

**Fully populated with example values**: Active sources, groups, and content pointing to a fictional org. Rejected because it fails on `niwa apply` (the org doesn't exist) and requires deleting lines rather than uncommenting.

**Minimal scaffold with link to docs**: Only `[workspace]` with a comment pointing to documentation. Rejected because the scaffold should be self-documenting.

**Interactive prompting**: Ask for org name during init. Rejected for the local path because it adds network dependency and interactive complexity. The `--from` path handles remote configs.

### Decision 3: Conflict detection and edge cases

Init must handle cases where the current directory already has niwa artifacts or is inside an existing workspace. The PRD mandates: "niwa init in a directory that already has a workspace.toml refuses and suggests niwa apply."

#### Chosen: Fail-fast with targeted detection

Each conflict case gets a specific error with recovery instructions. No `--force` flag initially. Detection order is local-first for performance.

**Case 1: workspace.toml already exists**
```
Error: this directory is already a niwa workspace (.niwa/workspace.toml exists)
  Run "niwa apply" to update this workspace, or remove .niwa/ to start fresh.
```

**Case 2: Running inside an existing instance**
Uses `DiscoverInstance(cwd)` to find `.niwa/instance.json` in current or parent directories.
```
Error: current directory is inside a workspace instance at /path/to/instance
  Navigate to a directory outside any existing workspace to run init.
```

**Case 3: .niwa/ exists but isn't a recognized config**
```
Error: .niwa/ directory already exists but contains no recognized configuration
  Remove .niwa/ manually if you want to initialize a new workspace here.
```

Detection order: Case 1 -> Case 3 -> Case 2 (cheapest checks first; upward walk last).

All checks run before any filesystem writes -- init either succeeds fully or changes nothing.

#### Alternatives considered

**--force flag**: Allows overwriting existing config. Rejected because `rm -rf .niwa/ && niwa init` achieves the same result without adding a code path that risks data loss.

**Warning instead of error for Case 2**: Let init proceed inside an instance. Rejected because nesting workspace roots inside instances creates confusing state.

**Automatic cleanup of partial state**: Detect and recover from partial init failures. Rejected because distinguishing "partial init" from "something else created .niwa/" is unreliable.

## Decision Outcome

### Summary

The init command has three modes that share a common pre-flight check (conflict detection) and post-flight step (registry update):

1. **`niwa init`** (no args): scaffold `.niwa/workspace.toml` with commented template, create empty `claude/` directory. Name defaults to "workspace". No registry entry (detached workspace).

2. **`niwa init <name>`**: check registry for `<name>`. If registered with a source, clone from that source (same as --from). If not registered, scaffold locally with `name = "<name>"` and register as a local-only config.

3. **`niwa init <name> --from <org/repo>`**: shallow git clone the config repo as `.niwa/`, register the name-to-source mapping in the global registry. Subsequent `niwa init <name>` in a new directory uses the registered source.

All modes refuse if the current directory already has a workspace config, is inside an instance, or has an unrecognized `.niwa/` directory.

### Rationale

Shallow git clone for remote configs is the only approach that supports future `niwa update` (which needs git fetch/pull). The commented template for local scaffolds teaches the schema in-place without requiring users to read external docs. Fail-fast conflict detection prevents the most common mistakes (re-init, init in wrong directory) without adding complexity like --force flags.

## Solution Architecture

### Command flow: `niwa init`

```
1. Parse args: determine mode (no-args, named, named+from)
2. Pre-flight checks (Decision 3):
   a. Check $PWD/.niwa/workspace.toml -- refuse if exists
   b. Check $PWD/.niwa/ -- refuse if exists (unknown content)
   c. Walk up for .niwa/instance.json -- refuse if inside instance
3. Execute init mode:
   Mode A (no args): scaffold workspace.toml, create claude/
   Mode B (named, not registered): same as A with name set
   Mode C (named, registered) or (named+from): shallow clone into .niwa/
4. Post-flight:
   a. Verify .niwa/workspace.toml exists and parses
   b. Register in global registry (skip for detached)
   c. Print success message with next steps
```

### Package changes

**New file: `internal/cli/init.go`**
- Cobra `init` subcommand with `<name>` positional arg and `--from` flag
- Calls pre-flight checks, then delegates to scaffold or clone

**New file: `internal/workspace/scaffold.go`**
- `Scaffold(dir, name string) error` -- writes commented workspace.toml template to `.niwa/workspace.toml` and creates empty `claude/` directory
- Template is a Go string constant, not a separate template file

**Extended: `internal/workspace/clone.go`**
- Add `CloneWith(ctx, url, targetDir string, opts CloneOptions) error` as a `Cloner` method
- Existing `Clone` and `CloneWithBranch` become convenience wrappers around `CloneWith`
- `CloneOptions` has `Ref` (tag or commit) and `Depth` fields

**Extended: `internal/workspace/state.go`**
- `DiscoverInstance` already exists -- used for Case 2 detection

**Extended: `internal/config/registry.go`**
- No changes needed -- existing LoadGlobalConfig, SaveGlobalConfig, LookupWorkspace, SetRegistryEntry cover the registry operations

### Go types

```go
// CloneOptions controls clone behavior.
type CloneOptions struct {
    Ref   string // tag, branch, or commit SHA to checkout
    Depth int    // clone depth (0 = full, 1 = shallow)
}

// InitOptions holds configuration for the init command.
type InitOptions struct {
    Name string // workspace name (empty for detached)
    From string // org/repo source (empty for local scaffold)
}

// Pre-flight errors use typed errors for standard Go error handling.
var (
    ErrWorkspaceExists  = errors.New("workspace already exists")
    ErrInsideInstance   = errors.New("inside existing workspace instance")
    ErrNiwaDirectoryExists = errors.New(".niwa directory exists with unknown content")
)

// InitConflictError wraps a pre-flight error with a recovery suggestion.
type InitConflictError struct {
    Err        error  // one of the sentinel errors above
    Detail     string // e.g., path to existing workspace
    Suggestion string // recovery action for the user
}
```

## Security Considerations

- **Remote config trust**: cloned workspace.toml and content files direct file writes and shape AI agent behavior. Users should only init from repos they trust. The `--review` flag (future) lets users inspect before committing.
- **Git hooks execute during clone**: `git clone` runs hooks from the cloned repo (e.g., `post-checkout`). This means `niwa init --from` is equivalent in trust to running `git clone` directly -- the user trusts the source repo with arbitrary code execution at clone time. The `--review` flag cannot mitigate this since hooks run before inspection. This is inherent to git and consistent with how users already think about cloning repos.
- **Registry trust chain visibility**: when `niwa init <name>` resolves a source from the registry, niwa must print the resolved URL before cloning (e.g., "Initializing from registered source: github.com/org/repo"). This makes the trust chain visible -- a malicious hook from a prior clone could have modified the registry to redirect future inits to an attacker-controlled repo. Printing the URL lets the user catch unexpected sources.
- **Name validation as security invariant**: the `[a-zA-Z0-9._-]+` regex for org/repo names prevents command injection in constructed clone URLs. This validation must remain in place whenever names are interpolated into shell commands or URLs. Any change to the allowed character set requires a security review.
- **No secret exposure**: init doesn't handle secrets. Bot tokens and API keys live in per-host config files (out of scope for init).
- **Filesystem safety**: pre-flight checks prevent overwriting existing configs. Init creates only `.niwa/` and its contents -- no writes outside the current directory.

## Consequences

### Positive

- One command to start a new workspace: `niwa init my-project`
- Team onboarding becomes: `niwa init <name> --from <org/repo>` then `niwa create` then `niwa apply`
- Scaffold teaches the full schema through commented examples
- Registry enables name-based workspace resolution across machines
- Shallow clone supports future `niwa update` without extra work

### Negative

- Git is a hard dependency for remote init. Users without git can only use local scaffolding.
- No interactive prompting for the local path -- users edit the scaffold manually. This is intentional (simple, offline) but less guided than an interactive wizard.
- No --force flag means users must manually `rm -rf .niwa/` to re-init. This is a trade-off for safety that can be revisited if user friction warrants it.
