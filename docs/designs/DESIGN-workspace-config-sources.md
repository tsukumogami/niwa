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

The design decomposes into five independent decisions, each evaluated
against the Decision Drivers above. Full per-decision reports live in
`wip/research/design_workspace-config-sources_decision_<N>_report.md`
during the active design phase; the summaries below capture the chosen
option and the alternatives rejected.

### Decision 1: Provenance marker format and location

The snapshot needs a source-identity surface that replaces `.git/`
presence for five readers (`niwa reset`, the plaintext-secrets
guardrail, drift detection, `niwa status`, the integrity heuristic).
PRD R11 fixes the field set; the format and location are open.

**Key assumptions**: marker written exactly once per snapshot (no
in-place mutation); future R11 schema additions stay flat-scalar (no
nested tables); BurntSushi/toml v1.6.0 stays a vendored direct
dependency.

#### Chosen: TOML at `<workspace>/.niwa/.niwa-snapshot.toml`

Leading-dot filename, inside the snapshot directory. TOML matches
niwa's existing convention for human-readable config (workspace.toml,
~/.config/niwa/config.toml), gives `cat` output a flat-key shape that
mirrors PRD R38's literal field list, and uses the BurntSushi/toml
parser already vendored. Inside-snapshot location couples the marker
to the snapshot via the same atomic-swap mechanism (Decision 2), so
the marker can never be observed pointing at a different snapshot
than the one on disk.

#### Alternatives Considered

- **JSON inside snapshot dir** — runner-up. Defensible on
  consistency-with-instance.json grounds but loses on `cat`
  readability (curly braces and quoting noise) and on R11's native
  RFC-3339 timestamp ergonomics (`fetched_at` becomes a string in
  JSON; TOML has native datetime).
- **TOML sibling outside snapshot dir** (`<workspace>/.niwa-snapshot.toml`)
  — solves the source-repo-collision concern structurally but breaks
  R12's atomic-swap unity: the marker and snapshot would need a
  two-step rename, creating a window where the marker references a
  different snapshot than what's on disk.
- **Plain key=value text** — bespoke parser shifts complexity from a
  tested library (BurntSushi/toml) into untested niwa code; the cat
  readability gain is marginal; schema evolution is harder, not
  easier.
- **YAML** — requires a third-party dependency (`gopkg.in/yaml.v3`),
  violating the stay-in-stdlib invariant; no benefit over TOML or
  JSON for a flat-scalar 9-field record.
- **No-leading-dot variants** (`niwa-snapshot.toml`) and
  **`.niwa.lock` filename** — weaker system-file signal; weaker
  collision defense; `.lock` implies process-held-lock semantics the
  marker doesn't have.

### Decision 2: Atomic snapshot swap and instance.json placement

PRD R12 requires `<workspace>/.niwa/` to never be observed absent or
partially populated during refresh. Today `instance.json` lives
inside `.niwa/`, which would be renamed-out-from-under by any swap.
Two coupled choices.

**Key assumptions**: niwa runs on Linux and macOS only (no Windows in
v1); the sub-microsecond window during the two-step rename is
acceptable per PRD R12's "at no point" framing (no concurrent
observer of `<workspace>/.niwa/` relies on directory presence at
sub-microsecond granularity); `instance.json` is written exclusively
by the niwa CLI (no external tooling races); workspace refresh and
state writes share a filesystem.

#### Chosen: Two-rename swap on a sibling staging directory; relocate `instance.json` to `<workspace>/.niwa-state/instance.json` with a dual-path read fallback

The swap sequence: stage at `<workspace>/.niwa.next/`, then
`rename(.niwa, .niwa.prev)` → `rename(.niwa.next, .niwa)` → `fsync`
→ `RemoveAll(.niwa.prev)`. An idempotent preflight cleanup removes
stale `.niwa.next/` and `.niwa.prev/` from interrupted prior runs.

`StateDir` constant renames from `.niwa` to `.niwa-state` (sibling
subdir alongside `.niwa/`); a separate `SnapshotDir = ".niwa"`
constant identifies the snapshot directory. `LoadState`,
`DiscoverInstance`, and `EnumerateInstances` gain a dual-path lookup
(new path first, legacy path as fallback). `SaveState` performs the
relocation lazily on first save and emits a one-time `note:` via
`DisclosedNotices`. Dual-path fallback removal scheduled for v1.1.

A new `internal/workspace/snapshot.go` defines the single
`swapSnapshotAtomic(target, staging)` primitive used by all three
clone sites (team config, personal overlay, workspace overlay),
satisfying R13 with no per-site logic.

#### Alternatives Considered

- **`renameat2(RENAME_EXCHANGE)` (Linux-only atomic swap)** —
  non-portable; would still need an identical macOS fallback;
  optimizes a window the PRD already accepted.
- **Three-rename with explicit inline recovery** — redundant given
  the idempotent preflight cleanup; same unrecoverable-mid-swap
  window as two-rename without the recovery benefit.
- **Symlink swap to a content-addressed directory** — contradicts
  PRD R10's mental model that `<workspace>/.niwa/` IS the snapshot;
  breaks scripts that don't follow symlinks; introduces stale-
  snapshot GC; PRD Out of Scope rules out the shared-cache
  motivation in v1.
- **Sibling flat file `<workspace>/.niwa-state.json`** — forecloses
  adjacent state files (locks, breadcrumbs) without proliferating
  top-level dotfiles. The subdirectory form preserves room to grow.
- **Inside-snapshot `<workspace>/.niwa/.state/instance.json`** —
  violates PRD R10's strict snapshot-contents contract or requires a
  second exclusion rule; couples state I/O into the refresh
  primitive.
- **`$XDG_STATE_HOME/niwa/<workspace-hash>/instance.json`** — breaks
  workspace portability (state separated from workspace); breaks
  `DiscoverInstance`'s upward-walk semantics; adds hash-based
  lookup complexity.

### Decision 3: Slug parser package and API

PRD R1-R4 commits to `[host/]owner/repo[:subpath][@ref]`. Today's
ad-hoc parsers in `clone.go:90` (`ResolveCloneURL`) and
`overlay.go:227` (`parseOrgRepo`) get replaced by a canonical parser
consumed by nine call sites (init, config-set, fetcher, registry
writer, discovery probe, overlay slug deriver, status, guardrail,
state writer).

**Key assumptions**: default host is `github.com`; round-trip is
exact for whole-repo slugs (`org/repo` parses and re-renders
byte-identically per R23/R33); parser performs no I/O; the five-
tuple is exhaustive (no sixth identity dimension surfaces in later
phases); stdlib-only.

#### Chosen: New leaf package `internal/source/` with typed `Source` struct

`Parse(string) (Source, error)` and `Source.String() string` for
round-trip. Methods on `Source`: `CloneURL(protocol)`,
`TarballURL()`, `CommitsAPIURL(ref)`, `OverlayDerivedSource()`
(implements PRD R35's basename-based rule), `DisplayRef()` (for
`niwa status`'s "(default branch)" annotation).

Decisive factor: cycle analysis. The slug parser is consumed by both
`internal/cli/`, `internal/workspace/`, `internal/config/`, and
`internal/guardrail/`. Only a leaf package lets all of them import
without breaking the existing `workspace → config` import direction.
Typed-struct-with-methods matches niwa's dominant pattern (see
`internal/secret`, `internal/github`, `internal/vault`).

#### Alternatives Considered

- **Extend `internal/workspace/`** — creates an unbreakable
  `config → workspace` import cycle when the registry writer needs
  the parser.
- **Extend `internal/config/`** — bloats the most-imported package
  in niwa with stylistically misplaced parsing logic.
- **Keep in `workspace/clone.go` and rename** — same cycle problem;
  also a rewrite, not a rename.
- **Free functions only with plain `Source` struct** — departs from
  niwa's typed-value-with-methods convention; lets callers
  hand-build invalid five-tuples.
- **Fluent builder pattern** — no use case for progressive assembly;
  no codebase precedent.
- **Multi-return parser with separate renderer** — five-return
  functions defeat the "one canonical type" design driver.

### Decision 4: Test infrastructure architecture

PRD's Test Strategy section names `tarballFakeServer`, fault-
injection, and a state-file factory as in-scope deliverables. Three
coupled sub-questions about how tests exercise the new fetch path.

**Key assumptions**: D5's tarball client exposes a configurable
`BaseURL` (constructor parameter or env var) that tests point at the
fake; functional tests invoke the same shipped binary (process
boundary defeats build-tag and DI fault-injection mechanisms); state
schema is in flux during this work, so in-code factories beat
on-disk fixture trees; per-scenario sandbox lifetime bounds request-
log memory.

#### Chosen: `httptest.Server`-based `tarballFakeServer`; single `NIWA_TEST_FAULT` env-var seam backed by `internal/testfault/`; Gherkin step pair backed by Go state-file factory

The `tarballFakeServer` lives next to `localGitServer` in
`test/functional/`, wraps `httptest.NewServer`, and serves both the
GitHub API endpoints (`/repos/{owner}/{repo}/tarball/{ref}`,
`/repos/{owner}/{repo}/commits/{ref}`) and the codeload 302 redirect
target on the same host:port. Tests configure response bodies,
status codes, ETags, redirects, and inspect the request log through
methods on the helper struct.

Fault injection lives in a new package `internal/testfault/` —
exactly one package, one exported function (`testfault.Maybe(label
string) error`), one method to set faults from tests
(`testfault.Set(spec string)`). Production code in the github fetch
path and the snapshot writer call `testfault.Maybe("fetch-tarball")`
etc. at well-defined points. The package reads `NIWA_TEST_FAULT` env
var (e.g., `truncate-after:N@fetch-tarball`) and returns errors
accordingly. Production builds compile in the empty-default
behavior; the env var is the sole activation mechanism.

State-file factory: a Gherkin step pair in `test/functional/` —
`Given an instance state file at version <N>` (default-shape) and
`Given an instance state file at version <N> with body:` (docstring
override) — backed by a Go factory function that authors the
schema-version-specific JSON.

Decisive factor: niwa's functional tests cross a process boundary
(they exec the shipped binary). Build tags fork the artifact-under-
test; package-level test hooks can't be set across processes;
dependency injection requires changing production structure for test
concerns. Env vars survive the boundary cleanly.

#### Alternatives Considered

- **Standalone goroutine HTTP server** — duplicates `httptest.Server`
  with no capability gain.
- **Extending `localGitServer` to also serve HTTP tarballs** —
  conflates two protocols; bloats helper.
- **Test container running a real GitHub-API mock** — violates
  no-system-deps invariant.
- **Build tag (`//go:build niwa_test_faults`) for fault injection**
  — forks the shipped binary.
- **Public test hook variable in production package** — test process
  can't write to binary process's package-level vars.
- **Dependency injection / Fetcher interface for fault injection** —
  changes production structure for test concerns; doesn't help
  functional tests that cross the process boundary.
- **Pre-baked fixture directories under `test/functional/fixtures/state/v2/`**
  — proliferate as state schema evolves; harder to keep in sync.

### Decision 5: GitHub tarball client implementation

PRD R14 commits to GitHub REST tarball + Go's `archive/tar`. R16
adds drift via `commits/{ref}` SHA endpoint + ETag. R17 reads
`GH_TOKEN`. R18 follows 301s for renames. The implementation
question: package layout, API shape, extraction, auth, and redirect
plumbing.

**Key assumptions**: D4's `tarballFakeServer` serves both API and
codeload-302 target on the same host:port (one BaseURL covers both);
symlink entries are skipped in v1 (R10 specifies regular files);
`GH_TOKEN` is stable for the process lifetime (read once at
constructor).

#### Chosen: Extend `internal/github/client.go` with `HeadCommit` + `FetchTarball` methods on existing `APIClient`; tar extraction in `internal/github/tar.go`; `BaseURL` substitution as test seam

`APIClient` gains:
- `HeadCommit(ctx, owner, repo, ref, etag) (oid string, newETag string, statusCode int, err error)` — drift check via the SHA endpoint with `Accept: application/vnd.github.sha`.
- `FetchTarball(ctx, owner, repo, ref, etag) (body io.ReadCloser, newETag string, statusCode int, redirect *RenameRedirect, err error)` — streams the tarball; surfaces a 301 redirect chain via `RenameRedirect` for R18's rename detection.
- `BaseURL` field set at construction (existing pattern). Production sets it from `NIWA_GITHUB_API_URL` env var when present, defaulting to `https://api.github.com`. Tests inject the fake's URL via the same env var.

Tar extraction lives next to the client as a package-local free
function:
- `extractSubpath(r io.Reader, subpath string, dest string) error` — streams `archive/tar` over `compress/gzip`, applies a literal-prefix subpath filter (`<github-tarball-wrapper>/<subpath>/...`), writes regular files only (skips symlinks, directories, devices), validates path containment to prevent escape via crafted entries.

`GH_TOKEN` is read once at constructor and added as
`Authorization: Bearer <token>` on every request. Redirects use
`http.Client.CheckRedirect` to record the chain and follow both 301
and 302; rename detection inspects the chain after the response.

Decisive factor: every choice mirrors an established pattern (typed
`APIClient`, `BaseURL` test seam, no third-party deps). The
"metadata vs content" separation argued by alternatives is cosmetic
since both share auth, base URL, and HTTP client.

#### Alternatives Considered

- **New package `internal/github/fetcher/`** — cosmetically cleaner
  but duplicates auth + BaseURL plumbing across two packages,
  requires a second `Applier` injection, introduces "two packages
  both labelled github."
- **Embed inside the cross-host fetcher abstraction (no github
  subpackage)** — duplicates request-construction patterns from
  APIClient without sharing them; tightly couples this decision to
  the cross-host fetcher's shape.

## Decision Outcome

The five decisions compose into a coherent architecture with one
canonical source-identity type, one atomic-swap primitive, one
shared snapshot file shape, and one test-infrastructure pattern.

The user-typed slug (`org/brain:.niwa@v1`) flows through:

1. **`internal/source.Parse`** → typed `Source{host, owner, repo, subpath, ref}`
2. **`Source.TarballURL` + `internal/github.APIClient.FetchTarball`** → tarball stream
3. **`internal/github.extractSubpath`** → files written under
   `<workspace>/.niwa.next/`
4. **Provenance marker writer** → `.niwa-snapshot.toml` written into
   `<workspace>/.niwa.next/`
5. **`internal/workspace.swapSnapshotAtomic`** → sibling-staging
   two-rename swaps `.niwa/` for the new snapshot
6. **State writer** → `<workspace>/.niwa-state/instance.json` updated
   with `config_source` block (schema v3)

The same pipeline serves the team config, the personal overlay, and
the workspace overlay (R13 symmetry), differing only in where the
snapshot directory lives (workspace-local for team config; per-user
locations for the overlays). The auto-discovered overlay slug is
derived from `Source.OverlayDerivedSource()` per PRD R35.

`niwa reset` and the plaintext-secrets guardrail read the
`.niwa-snapshot.toml` marker to recover source identity (replacing
`.git/`-presence reads). `niwa status` formats the marker plus the
state's `config_source` block for display, including the "(default
branch)" annotation when the slug had no `@ref`.

Two integration points worth flagging for Phase 4 architecture
synthesis:

- **`testfault.Maybe()` call sites**: at fetch-tarball start, mid-
  extraction (per-entry hook), and just before the snapshot swap.
  These are the points the PRD's fault-injection ACs (truncated
  tarball, partial extraction, interrupted swap) exercise.
- **APIClient construction site**: production reads
  `NIWA_GITHUB_API_URL` env var when present (defaulting to
  `https://api.github.com`) at the point in `internal/cli/` or
  `internal/workspace/` where the client is constructed. Tests set
  the env var to point at the `tarballFakeServer`'s address.

## Solution Architecture

(Populated by Phase 4.)

## Implementation Approach

(Populated by Phase 4.)

## Security Considerations

(Populated by Phase 5.)

## Consequences

(Populated by Phase 4.)
