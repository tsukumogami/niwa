---
upstream: docs/prds/PRD-workspace-config-sources.md
status: Proposed
problem: |
  niwa's three clone primitives (team config, personal overlay, workspace
  overlay) all materialize git working trees and sync via `git pull
  --ff-only`, which wedges on remote rewrite (issue #72), forces
  whole-repo sourcing (no subpath support), and silently invites edits
  that the next refresh discards.
decision: |
  TBD — populated after Phase 3 cross-validation.
rationale: |
  TBD — populated after Phase 4 architecture synthesis.
---

# DESIGN: Workspace Config Sources

## Status

Proposed

## Context and Problem Statement

The PRD (`docs/prds/PRD-workspace-config-sources.md`) commits to a
unified subpath-aware snapshot model for sourcing git-hosted workspace
configuration. This design doc covers HOW.

The implementation challenge spans five surfaces, each currently
coupled to assumptions the snapshot model breaks:

1. **Three clone primitives that all assume working trees.**
   `internal/workspace/configsync.go:42` (`SyncConfigDir` for the team
   config clone and the personal overlay clone), `internal/workspace/
   overlaysync.go:45` (`CloneOrSyncOverlay` for the workspace overlay
   clone), and `internal/workspace/clone.go:43` (`Cloner.CloneWith`
   used by `init` and `niwa config set global`) all do `git clone` +
   `git pull --ff-only`. Snapshots replace this with a fetch-and-swap
   primitive that has no `.git/`. Every call site needs to migrate
   together; partial migration leaves an inconsistent recovery model.

2. **Two `.git/`-dependent code paths that silently regress.**
   `internal/cli/reset.go:131` (`isClonedConfig`) and
   `internal/guardrail/githubpublic.go:75`
   (`CheckGitHubPublicRemoteSecrets`) both use `<configDir>/.git/`
   presence as a proxy for "this came from a remote." Removing
   `.git/` without a replacement source-identity marker silently
   disables `niwa reset` and the public-repo plaintext-secrets
   guardrail.

3. **Slug grammar with no current home.** Today
   `internal/workspace/clone.go:90` (`ResolveCloneURL`) and
   `internal/config/overlay.go:227` (`parseOrgRepo`) each do their
   own ad-hoc parsing of `org/repo` shorthand. The new
   `[host/]owner/repo[:subpath][@ref]` grammar (PRD R1, R3) needs a
   shared canonical parser whose output a typed source struct that
   `Cloner`, the registry writer, the discovery probe, the overlay
   slug deriver, and `niwa status` all consume.

4. **Registry and state schemas with new identity dimensions.** PRD
   R22-R25 commit to lazy migration: registry mirror fields populated
   on first load, `InstanceState` schema v3 with a `config_source`
   block populated on next save. The migration code paths sit at
   the boundaries of `internal/config/registry.go` and
   `internal/workspace/state.go` respectively and must preserve
   unrelated fields untouched.

5. **Test infrastructure absent for GitHub-path verification.** The
   PRD's Test Strategy section commits to building a
   `tarballFakeServer` paired with `localGitServer`, plus
   fault-injection seams and a state-file factory. Without this,
   the GitHub-path acceptance criteria (R14-R18) and atomic-refresh
   ACs (R12, R26) can't be verified mechanically.

Beyond these direct surfaces, the design must commit to specific
choices the PRD deliberately deferred:

- **Provenance marker file format and on-disk location** (PRD Out of
  Scope). The marker is the source-identity signal that the snapshot
  model needs (replaces `.git/` for `isClonedConfig` and the
  guardrail) and the drift-detection signal that next apply reads. A
  poor format choice ripples into every read site.
- **Snapshot atomic-swap mechanism** (PRD R12 commits to the
  contract; not the sequence). POSIX `rename(2)` semantics for
  non-empty directories vary by platform; the design must pick a
  sequence that satisfies "at no point is `.niwa/` absent or
  partially populated."
- **`instance.json` placement relative to the snapshot.** Today the
  state file lives inside `.niwa/instance.json`. Once `.niwa/` is a
  directory the refresh path may rename out from under, the state
  file's location must change or the swap must explicitly preserve
  it.
- **Slug parser package boundary.** New shared parser needs a home;
  candidates are `internal/source/`, extending `internal/workspace/`,
  or living in `internal/config/`. Affects which packages depend on
  which.

## Decision Drivers

Drawn from PRD requirements and from the implementation surface
above.

### Correctness invariants (from PRD)

- **Issue #72 must become unreachable** (PRD goal, R10). The
  `git pull --ff-only` failure mode disappears from the supported
  surface.
- **No content bleed.** Files outside the resolved subpath never
  persist on disk during materialization (PRD R10, R37; ruled out
  every sparse-checkout / partial-clone variant).
- **Snapshot refresh is atomic** from the perspective of concurrent
  readers (PRD R12). No window where `.niwa/` is absent or
  partially-populated.
- **Backwards compatibility is non-negotiable.** Existing
  standalone-`dot-niwa` registries continue to apply with no user
  action (PRD R28, R33-R34).

### Implementation drivers

- **Three call sites migrate together.** `init`, `apply` (team
  config sync), and `niwa config set global` (personal overlay
  install) all currently invoke clone primitives. A partial
  migration leaves users with a mix of working-tree and snapshot
  directories that recover differently.
- **Source-identity marker is load-bearing.** Beyond the PRD's
  explicit consumers (`niwa status`, drift detection), the marker
  replaces `.git/`-presence as the signal `niwa reset` and the
  plaintext-secrets guardrail use. Format and placement choices
  affect all five readers symmetrically.
- **Test infrastructure deliverables are first-class.** The PRD
  explicitly named `tarballFakeServer`, fault-injection seams, and a
  state-file factory as in-scope. The design must commit to their
  shape before Phase 4 architecture synthesis or the AC-to-code
  mapping is unverifiable.
- **No system dependencies.** Per the workspace's CLAUDE.md
  "self-contained, no system dependencies" invariant: the
  tarball-extraction path uses Go's `archive/tar` (not system
  `tar(1)`); the git-clone fallback uses `os/exec` against the
  user's pre-installed `git` (the same dependency niwa already has
  today via `Cloner.CloneWith`).
- **Stay inside Go standard library where reasonable.** niwa today
  uses `internal/github/client.go` with `http.DefaultClient` for the
  GitHub API; the tarball fetch path should extend that pattern, not
  introduce a new HTTP client dependency.

### Maintainability drivers

- **Migration paths are observable.** Each lazy migration (registry
  mirror upgrade R23, state schema v3 R24, working-tree-to-snapshot
  R28) must produce a visible signal so a debugging contributor can
  trace what happened. Silent migrations are fine for users; opaque
  migrations are not fine for maintainers.
- **One canonical source-tuple representation.** Slug parsing,
  registry mirror, state's `config_source`, status display, and
  guardrail input all consume the same five-tuple
  `(host, owner, repo, subpath, ref)`. A single typed Go struct
  used everywhere prevents the "five places represent the same
  concept differently" pattern.
- **Each clone primitive replacement is testable in isolation.**
  Replacing `SyncConfigDir`, `CloneOrSyncOverlay`, and
  `Cloner.CloneWith` should each have its own coverage; the
  fixtures (per Test Strategy) should support per-primitive tests
  before integration.

## Considered Options

(Populated by Phase 2-3.)

## Decision Outcome

(Populated by Phase 4.)

## Solution Architecture

(Populated by Phase 4.)

## Implementation Approach

(Populated by Phase 4.)

## Security Considerations

(Populated by Phase 5.)

## Consequences

(Populated by Phase 4.)
