---
upstream: docs/prds/PRD-workspace-config-sources.md
status: Current
problem: |
  niwa's three clone primitives (team config, personal overlay, workspace
  overlay) all materialize git working trees and sync via `git pull
  --ff-only`, which wedges on remote rewrite (issue #72), forces
  whole-repo sourcing (no subpath support), and silently invites edits
  that the next refresh discards. Replacing this requires symmetric
  changes to all three clone sites plus replacement of two
  `.git/`-dependent code paths (`isClonedConfig`, the plaintext-secrets
  guardrail) and addition of a canonical source-identity surface that
  five readers consume.
decision: |
  Five composed choices: (1) typed `internal/source.Source` as the
  canonical five-tuple slug parser, leaf package consumed everywhere;
  (2) two-rename atomic snapshot swap on a sibling staging directory,
  with `instance.json` kept at `<workspace>/.niwa/instance.json` and
  carried through the swap by an assembly step that copies the file
  into staging before the rename (per 2026-04-23 amendment; original
  spec relocated to `.niwa-state/`); (3) TOML provenance marker at
  `<workspace>/.niwa/.niwa-snapshot.toml` inside the snapshot dir;
  (4) `httptest.Server`-based `tarballFakeServer` alongside
  `localGitServer`, `NIWA_TEST_FAULT` env-var seam backed by
  `internal/testfault/`, and Gherkin step pair with Go state-file
  factory; (5) extension of `internal/github/APIClient` with
  `HeadCommit` + `FetchTarball` methods and a package-local
  `extractSubpath` (Go's `archive/tar`, no system `tar`), with
  `BaseURL` substitution as the test seam.
rationale: |
  Each decision applies the workspace-wide self-contained-no-system-deps
  invariant and prefers leaf packages to break import cycles. The slug
  parser must be a leaf so all consumers can import it; the swap
  primitive is shared across three clone sites for R13 symmetry; the
  TOML marker matches niwa's existing config-readability convention and
  reuses the already-vendored BurntSushi/toml; the GitHub client extension
  mirrors every established pattern in `internal/github/` (typed
  `APIClient`, `BaseURL` test seam, no third-party deps). Test
  infrastructure choices are dictated by the process boundary functional
  tests cross — env-var fault injection is the only mechanism that
  survives.
---

# DESIGN: Workspace Config Sources

## Status

Accepted (2026-04-23 Amendment in effect)

## Amendments

### 2026-04-23 — Instance state stays in `.niwa/`

**What changed.** Decision 2's choice to relocate `instance.json` to a
sibling `<workspace>/.niwa-state/` directory is reversed. State stays at
`<workspace>/.niwa/instance.json` and is carried through the snapshot
refresh by an assembly step that copies the file from the existing
`.niwa/` into staging immediately before the atomic swap.

**Why.** The relocation was implementation-driven — its only purpose was
to keep state safe from being clobbered when the snapshot swap rotated
`.niwa/` wholesale. The simpler solution (copy state into staging before
swap) achieves the same safety without splitting the user-visible
workspace layout into two hidden directories. The implementation in
PR #73 already does this via a helper named `preserveInstanceState`;
this amendment promotes that helper from "band-aid" to "intentional
assembly step."

**Affected sections.**
- Decision 2's "Chosen" subsection updated in place: state stays at
  `<workspace>/.niwa/instance.json`; dual-path read and lazy migration
  drop out — there's no relocation to migrate.
- Phase 7 (originally "State schema v3 + `.niwa-state/` relocation")
  loses the relocation tasks; only the schema bump and registry mirror
  work remain.
- Solution Architecture's Overview reflects state living inside
  `.niwa/` and surviving the swap via the assembly step.

**What didn't change.** The slug parser, atomic swap primitive, TOML
provenance marker, test infrastructure architecture, GitHub client API
surface, and tarball-extraction strategy all stay as originally
specified.

### Future direction (needs-design, issue #74)

Issue #74 (`needs-design`) captures a longer-term improvement: move
from today's "pull the entire resolved subpath wholesale" fetch to a
convention-aware "pull only files niwa knows about" model. This is
deferred because (a) the relevant conventions are scattered across
materializers and the apply pipeline rather than codified, and
(b) the migration story for workspaces relying on wholesale-pull
needs design attention. v1 ships wholesale-pull.

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

#### Chosen: Two-rename swap on a sibling staging directory; `instance.json` stays at `<workspace>/.niwa/instance.json`, carried through the swap by the assembly step

> **Amended 2026-04-23.** The original choice relocated `instance.json`
> to `<workspace>/.niwa-state/`. User feedback during implementation
> pushed back: the relocation was implementation-driven, not
> user-driven — its only purpose was to keep state safe from being
> clobbered by the wholesale snapshot swap. The simpler solution
> (copy state into staging before swap) achieves the same safety
> without splitting the user-visible workspace into two hidden
> directories. See Solution Architecture's "Assembly step" subsection
> for the operative algorithm.

The swap sequence: stage at `<workspace>/.niwa.next/`, then
`rename(.niwa, .niwa.prev)` → `rename(.niwa.next, .niwa)` → `fsync`
→ `RemoveAll(.niwa.prev)`. An idempotent preflight cleanup removes
stale `.niwa.next/` and `.niwa.prev/` from interrupted prior runs.

`StateDir` keeps its current value of `.niwa`; the snapshot directory
and the state file share that path by intention. The assembly step
preserves `instance.json` (and any future niwa-local state file
enumerated in the closed set) by reading it from the existing
`.niwa/` and writing it into staging immediately before the swap.

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

### Overview

The workspace config-source pipeline becomes a small set of focused
packages that compose end-to-end. The user-typed slug is parsed into
a typed `Source`, the `Source` drives a stream-extracted snapshot via
the GitHub tarball API (or git-clone fallback), the snapshot
materializes at a sibling staging path, the assembly step copies any
niwa-local state files from the existing `.niwa/` into staging, a
provenance marker is written into staging, and a single atomic-swap
primitive promotes the assembled directory to `<workspace>/.niwa/`.
The same pipeline serves all three clone sites.

### Assembly step (per 2026-04-23 amendment)

The atomic swap rotates `<workspace>/.niwa/` wholesale. To keep
niwa-local state safe across that rotation, the snapshot writer
performs a small assembly step between extraction and swap:

1. Extract upstream content into staging (existing behavior — files
   from the source's resolved subpath, security-filtered per the
   tarball/clone-copy disciplines).
2. For each file in the closed set of niwa-local state files
   (currently just `instance.json`), if the file exists at
   `<workspace>/.niwa/<file>`, copy it into staging at the same
   relative path. The copy overwrites any same-named file that
   happened to come from upstream.
3. Write the provenance marker (`.niwa-snapshot.toml`) into staging.
4. Atomic swap.

The closed set is enumerated explicitly in code, not derived from a
naming pattern or a reserved subdirectory. New niwa-local files
added in future releases extend the list with an explicit code
change.

After swap, `<workspace>/.niwa/` contains: the source's regular
files (per R10), the provenance marker (R11), and the niwa-local
state files (carried by step 2 above). `find <workspace>/.niwa/
-name '.git*'` returns no results.

### Components

The change set adds two new packages, extends one, refactors three
clone-related files, and replaces two `.git/`-dependent code paths.

**New packages:**

| Package | Purpose |
|---------|---------|
| `internal/source/` | Canonical slug parser and source identity. Leaf package; imported by everyone. |
| `internal/testfault/` | Test-only fault-injection seam. Production calls `testfault.Maybe(label)` at well-defined points; default behavior is a no-op unless `NIWA_TEST_FAULT` is set. |

**Extended packages:**

| Package | Additions |
|---------|-----------|
| `internal/github/` | `APIClient.HeadCommit` and `APIClient.FetchTarball` methods; `extractSubpath` free function in a new `tar.go` file; `BaseURL` reads `NIWA_GITHUB_API_URL` env var when present. |
| `internal/workspace/` | New `snapshot.go` with `swapSnapshotAtomic(target, staging)`; new `provenance.go` with marker reader/writer; `state.go` learns v3 schema and dual-path lookup; `configsync.go` and `overlaysync.go` rewrite to compose source + fetcher + snapshot writer. |
| `internal/config/` | `RegistryEntry` gains parsed mirror fields (`source_host`, `source_owner`, `source_repo`, `source_subpath`, `source_ref`); read path lazy-populates from `source_url` when missing. |
| `internal/cli/` | `init`, `config_set`, `apply`, `status`, `reset` updated to use `internal/source` for parsing/display and to honor R3 strict parsing, R28 same-URL upgrade, R35 overlay derivation. |
| `internal/guardrail/` | `CheckGitHubPublicRemoteSecrets` reads marker tuple instead of `git remote -v`. |

**New test infrastructure:**

| File | Purpose |
|------|---------|
| `test/functional/tarball_fake_server.go` | `httptest.Server`-wrapped helper alongside `localGitServer`. |
| `test/functional/state_factory.go` | Go factory backing the `Given an instance state file at version <N>` Gherkin step. |
| `test/functional/features/workspace-config-sources.feature` | New scenarios for all PRD acceptance criteria. |

### Key Interfaces

#### `internal/source.Source`

```go
type Source struct {
    Host    string  // canonical host, e.g. "github.com"
    Owner   string  // org or user, e.g. "tsukumogami"
    Repo    string  // repo name, e.g. "niwa"
    Subpath string  // empty string == discovery; otherwise a slash-separated path
    Ref     string  // empty string == default branch; otherwise tag/branch/sha
}

func Parse(slug string) (Source, error)         // R3 strict parsing
func (s Source) String() string                 // round-trip exact for whole-repo slugs
func (s Source) CloneURL(protocol string) string
func (s Source) TarballURL() string             // GitHub-only; empty for non-github hosts
func (s Source) CommitsAPIURL(ref string) string
func (s Source) OverlayDerivedSource() Source   // implements PRD R35 basename rule
func (s Source) DisplayRef() string             // returns "(default branch)" when Ref == ""
```

#### `internal/testfault.Maybe`

```go
func Maybe(label string) error
```

Production code calls `Maybe("fetch-tarball")`, `Maybe("extract-entry")`,
`Maybe("snapshot-swap")` at well-defined points. The function returns
`nil` unless `NIWA_TEST_FAULT` env var matches a fault spec for the
label (e.g., `NIWA_TEST_FAULT=truncate-after:1024@fetch-tarball`).
Production builds incur a single env-var lookup per call (negligible
overhead).

#### `internal/github.APIClient` (additions)

```go
func (c *APIClient) HeadCommit(ctx context.Context, owner, repo, ref, etag string) (
    oid string, newETag string, statusCode int, err error)

func (c *APIClient) FetchTarball(ctx context.Context, owner, repo, ref, etag string) (
    body io.ReadCloser, newETag string, statusCode int, redirect *RenameRedirect, err error)
```

`RenameRedirect` carries the old and new `(owner, repo)` pair when
the API responds with a 301; nil otherwise.

#### `internal/workspace.swapSnapshotAtomic`

```go
func swapSnapshotAtomic(target, staging string) error
```

Two-rename swap with idempotent preflight cleanup. Used by all three
clone sites. Calls `testfault.Maybe("snapshot-swap")` at the start
for fault-injection scenarios. Returns errors that name the step
that failed (`preflight cleanup`, `rename to .prev`, `rename
.next to canonical`, `cleanup .prev`).

#### `internal/workspace` provenance marker

```go
type Provenance struct {
    SourceURL       string
    Host            string
    Owner           string
    Repo            string
    Subpath         string
    Ref             string
    ResolvedCommit  string
    FetchedAt       time.Time
    FetchMechanism  string  // "github-tarball" or "git-clone-fallback"
}

func WriteProvenance(snapshotDir string, p Provenance) error  // writes .niwa-snapshot.toml
func ReadProvenance(snapshotDir string) (Provenance, error)
```

#### `internal/workspace.InstanceState` (v3 additions)

```go
type ConfigSource struct {
    URL            string    `json:"url"`
    Host           string    `json:"host"`
    Owner          string    `json:"owner"`
    Repo           string    `json:"repo"`
    Subpath        string    `json:"subpath"`
    Ref            string    `json:"ref"`
    ResolvedCommit string    `json:"resolved_commit"`
    FetchedAt      time.Time `json:"fetched_at"`
}

type InstanceState struct {
    SchemaVersion int           `json:"schema_version"` // bumps to 3
    ConfigSource  *ConfigSource `json:"config_source,omitempty"` // populated lazily
    // ... existing fields
}
```

### Data flow

```
┌────────────────────────────────┐
│ user types: niwa init --from   │
│ org/brain:.niwa my-workspace   │
└────────────┬───────────────────┘
             │
             ▼
┌────────────────────────────────┐
│ internal/source.Parse(slug)    │ ─────► R3 strict validation
│   → Source{host,owner,repo,    │
│            subpath,ref}        │
└────────────┬───────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ Discovery (if Subpath == ""):                            │
│   probe rank-1 .niwa/workspace.toml, rank-2 root        │
│   workspace.toml, rank-3 root niwa.toml                 │
│   → resolved Source with Subpath populated              │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ Fetch:                                                   │
│   if Source.Host == "github.com":                        │
│     APIClient.HeadCommit(...) → drift check              │
│     APIClient.FetchTarball(...) → tarball stream         │
│     extractSubpath(stream, subpath, .niwa.next/)        │
│   else (fallback):                                       │
│     git clone --depth=1 to $TMPDIR/...                   │
│     copy subpath to .niwa.next/                          │
│     rm -rf $TMPDIR/...                                   │
│   testfault.Maybe("fetch-tarball") at start              │
│   testfault.Maybe("extract-entry") per tar entry         │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────┐
│ WriteProvenance(.niwa.next/, p)│
│   .niwa-snapshot.toml in dir   │
└────────────┬───────────────────┘
             │
             ▼
┌────────────────────────────────────────────────────────┐
│ swapSnapshotAtomic(.niwa, .niwa.next):                 │
│   preflight cleanup of stale .next/.prev/              │
│   rename(.niwa, .niwa.prev)                            │
│   rename(.niwa.next, .niwa)                            │
│   testfault.Maybe("snapshot-swap")                     │
│   fsync                                                │
│   RemoveAll(.niwa.prev)                                │
└────────────┬───────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────┐
│ State writer:                  │
│  .niwa-state/instance.json v3  │
│   .ConfigSource populated      │
│   from provenance + Source     │
└────────────────────────────────┘
```

The same pipeline runs for the personal overlay clone (target dir is
`$XDG_CONFIG_HOME/niwa/global/`) and the workspace overlay clone
(target dir is `$XDG_CONFIG_HOME/niwa/overlays/<dirname>/`), plus
the auto-discovery via `Source.OverlayDerivedSource()` for the
workspace overlay slug.

### Integration details (resolved during Phase 4)

- **`testfault.Maybe()` call sites**: at three points along the
  fetch + extract + swap pipeline:
  - `testfault.Maybe("fetch-tarball")` at the start of
    `APIClient.FetchTarball` (before the HTTP request).
  - `testfault.Maybe("extract-entry")` once per tar entry inside
    `extractSubpath` (lets faults stop extraction after N entries).
  - `testfault.Maybe("snapshot-swap")` at the start of
    `swapSnapshotAtomic` (lets faults run between staging and swap).
- **APIClient construction**: `internal/github.NewAPIClient` reads
  `NIWA_GITHUB_API_URL` from env; defaults to `https://api.github.com`.
  All production sites call `NewAPIClient()` (no constructor
  argument); tests set the env var to point at the
  `tarballFakeServer`.
- **State migration ordering**: when `niwa apply` lazy-converts a
  legacy working tree (PRD R28), the state-file relocation
  (`.niwa/instance.json` → `.niwa-state/instance.json`) MUST run
  BEFORE the snapshot swap so the state file exits the soon-to-be-
  renamed directory first. The same ordering applies on first
  `SaveState` post-upgrade.

## Implementation Approach

The work decomposes into 11 phases, each small enough to be one
commit (or one tight sequence of commits). Phases are mostly
sequential because the lower layers are leaf packages that the
higher layers depend on.

### Phase 1: `internal/source/` (slug parser)

Build the leaf source package. Strict parsing per R3, round-trip
exact for whole-repo slugs, methods for clone URL / tarball URL /
commits API URL / overlay-derived source / display ref.

Deliverables:
- `internal/source/source.go` (Source struct + methods)
- `internal/source/parse.go` (Parse function)
- `internal/source/source_test.go` (table-driven tests covering R3 cases)

### Phase 2: `internal/testfault/` (fault-injection seam)

Build the test-fault package. Single exported `Maybe(label)`
function reading `NIWA_TEST_FAULT` env var. Spec format:
`spec1@label1,spec2@label2`.

Deliverables:
- `internal/testfault/testfault.go`
- `internal/testfault/testfault_test.go`

### Phase 3: `internal/workspace/snapshot.go` (atomic swap)

Build the swap primitive. Idempotent preflight cleanup, two-rename
sequence, fsync. Calls `testfault.Maybe("snapshot-swap")`. Pure
function — no concept of provenance or fetch yet.

Deliverables:
- `internal/workspace/snapshot.go` (swapSnapshotAtomic)
- `internal/workspace/snapshot_test.go` (covers happy path,
  preflight cleanup of stale `.next/.prev/`, fault-injection mid-
  swap)

### Phase 4: `internal/workspace/provenance.go` (marker R/W)

Build the provenance marker reader/writer. TOML format, fixed
field set per R11. The reader returns a typed `Provenance` struct;
the writer writes `.niwa-snapshot.toml` into a given directory.

Deliverables:
- `internal/workspace/provenance.go`
- `internal/workspace/provenance_test.go` (round-trip, missing
  fields, malformed file)

### Phase 5: `internal/github/` extensions (HeadCommit, FetchTarball, extractSubpath)

Extend `APIClient` with the two new methods. Add `tar.go` with
`extractSubpath`. Wire `NIWA_GITHUB_API_URL` env var into
`NewAPIClient`. `RenameRedirect` type for 301 detection. Calls
`testfault.Maybe("fetch-tarball")` and `testfault.Maybe("extract-entry")`.

Deliverables:
- `internal/github/client.go` (additions + env var)
- `internal/github/tar.go` (extractSubpath)
- `internal/github/client_test.go` (with httptest)
- `internal/github/tar_test.go` (with synthetic tarballs)

### Phase 6: Snapshot writer and clone-primitive replacement

Rewrite `internal/workspace/configsync.go` and
`internal/workspace/overlaysync.go` to compose source parser +
fetcher + extract + provenance writer + assembly step + atomic swap.
The git-clone fallback path lives next to the github path. Replace
the legacy working-tree code paths. The assembly step (per the
2026-04-23 amendment to Solution Architecture) copies any niwa-local
state files from the existing `.niwa/` into staging immediately
before the swap so they survive the wholesale rotation.

Deliverables:
- `internal/workspace/configsync.go` (rewrite)
- `internal/workspace/overlaysync.go` (rewrite)
- `internal/workspace/fallback.go` (git-clone fallback for non-github)

### Phase 7: State schema v3

Bump `InstanceState.SchemaVersion` to 3. Add `ConfigSource` field.
v2 state files load successfully and lazy-upgrade on next save.

> **Amended 2026-04-23.** The original phase included a `StateDir`
> rename to `.niwa-state` and a dual-path lookup. Both are dropped:
> `instance.json` stays at `<workspace>/.niwa/instance.json` per the
> reversal in Decision 2's amendment. The schema bump remains.

Deliverables:
- `internal/workspace/state.go` (schema v3, ConfigSource field)
- `internal/workspace/state_test.go` (v2→v3 lazy migration,
  forward-version rejection)

### Phase 8: Registry mirror fields + lazy migration

Add parsed mirror fields to `RegistryEntry`. Lazy-populate from
`source_url` on read; persist on next save with stderr warning if
mirror disagreed with canonical. Update writers in `niwa init` and
`niwa config set global`.

Deliverables:
- `internal/config/registry.go` (mirror fields)
- `internal/config/registry_test.go` (lazy upgrade, mirror reconciliation)

### Phase 9: CLI updates (init, config_set, apply, status, reset)

Wire the canonical source parser through the CLI surface. Implement
R28 same-URL upgrade, R26-R27 URL-change `--force` gate with
workspace-name validation. Display source line in `niwa status`
detail view per R36. `--allow-dirty` deprecation notice per R32.
Replace `isClonedConfig` and the plaintext-secrets guardrail to
read the marker.

Deliverables:
- `internal/cli/init.go` (slug parser, R28 same-URL upgrade)
- `internal/cli/config_set.go` (slug parser)
- `internal/cli/apply.go` (URL-change detection, deprecation notice)
- `internal/cli/status.go` (source line, overlay slug line per R36)
- `internal/cli/reset.go` (`isClonedConfig` reads marker)
- `internal/guardrail/githubpublic.go` (reads marker)

### Phase 10: Test infrastructure (`tarballFakeServer`, state factory, scenarios)

Build the test helpers and write Gherkin scenarios for every
acceptance criterion. The `tarballFakeServer` lives next to
`localGitServer`. State-file factory backs the new step.

Deliverables:
- `test/functional/tarball_fake_server.go`
- `test/functional/state_factory.go`
- `test/functional/steps_workspace_config_sources.go`
- `test/functional/features/workspace-config-sources.feature`

### Phase 11: Documentation

Write the new guide and update the existing ones per the PRD's
documentation outline.

Deliverables:
- `docs/guides/workspace-config-sources.md` (new)
- `docs/guides/workspace-config-sources-acceptance-coverage.md` (new)
- `docs/guides/functional-testing.md` (note about `tarballFakeServer`)
- `docs/guides/vault-integration.md` (rewording of guardrail
  reference per R31)
- `README.md` (updated config-source description)
- `CLAUDE.md` (link to new guide in Contributor Guides)

### Implicit decisions surfaced and resolved

A pass over the architecture and approach text surfaced four
implicit choices that deserve explicit recognition. Each was
resolved in `--auto` per the research-first protocol; the decisions
file (`wip/design_workspace-config-sources_decisions.md`) records
the reasoning.

#### Decision 6: Snapshot write ordering

**Chosen**: write source files first, then provenance marker, then
swap. The swap promotes the staged directory only after the marker
is fully written.

**Alternatives**: write marker first then files (so a partial
extraction has a marker pointing at incomplete content — wrong);
write source files in parallel with marker (no benefit; complicates
the failure model).

#### Decision 7: Lazy state-file relocation timing

**Chosen**: relocation triggers on first `SaveState` post-upgrade.
Read path is dual-path until then.

**Alternatives**: relocate eagerly on first `niwa apply` (touches
disk for read-only workflows like `niwa status`); skip relocation
until v1.1 (forces every command to support both paths
indefinitely).

#### Decision 8: testfault label vocabulary

**Chosen**: free-form string labels matched by exact equality
against the env-var spec. No predefined enum.

**Alternatives**: predefined enum of fault points (forces design
churn whenever a new fault point is added); regex match against
labels (flexibility users won't need at the cost of test
debuggability).

#### Decision 9: snapshot-conversion notice format

**Chosen**: one-line `note:` prefixed message naming the
`<workspace>/.niwa/` path and the conversion. Recorded in
`InstanceState.DisclosedNotices` so it doesn't repeat.

**Alternatives**: multi-line explanation with link to
documentation (verbose for a one-time event); silent conversion (no
visibility into a state change).

## Security Considerations

The fetch + extract pipeline is this design's primary security
surface. The bytes streamed into `<workspace>/.niwa.next/` are
arbitrary content from a remote git source; a hostile source slug or
a man-in-the-middle attacker (defeating TLS) could deliver
attacker-controlled content. The design's defenses concentrate in
`internal/github/extractSubpath` and the snapshot swap primitive.

### Tarball extraction defenses

`extractSubpath` MUST enforce the following invariants on every entry
streamed from the tarball reader, before writing any bytes to disk:

1. **Positive type allowlist.** Only `tar.TypeReg` (regular file) and
   `tar.TypeDir` (directory) are acted on. All other entry types —
   `TypeSymlink`, `TypeLink` (hard link), `TypeChar`, `TypeBlock`,
   `TypeFifo`, `TypeXGlobalHeader`, `TypeXHeader`, GNU extensions —
   are skipped. This rules out symlink-and-write-through attacks
   structurally.

2. **Wrapper anchoring.** GitHub's tarball API wraps content in a
   single root directory (`<owner>-<repo>-<sha>/`). The first entry
   establishes the wrapper name; every subsequent entry's path must
   begin with `<wrapper>/`. Entries that don't are rejected with a
   hard error.

3. **Subpath filter.** After the wrapper prefix is stripped, the
   remaining path must begin with `<subpath>/` (or equal `<subpath>`
   for a single-file subpath per R4). Entries outside the subpath are
   skipped without writing.

4. **Path-containment check.** The destination path is computed via
   `filepath.Join(dest, relativePath)` then `filepath.Clean`-ed, then
   verified to live under `dest` (string-prefix check anchored to
   `dest + os.PathSeparator`). Entries that escape are rejected with a
   hard error, not silently skipped.

5. **Filename validation.** Entry paths must contain no NUL bytes, no
   `..` path segments, no leading `/`, and no embedded path separators
   beyond the expected `/`. Malformed names are rejected with a hard
   error.

6. **Decompression bomb defense.** The gzip reader is wrapped in an
   `io.LimitReader` with a 500 MB decompressed ceiling (overridable
   for legitimate large-subpath cases via a future env var). Per-entry
   writes use `io.CopyN(dest, src, header.Size)` with a per-entry
   ceiling derived from the same budget; cumulative bytes written are
   tracked across the extraction. Exceeding the cap returns a clean
   error.

7. **Failure leaves no partial state.** Any error during extraction
   leaves `.niwa.next/` orphaned at the staging path; the existing
   `.niwa/` is untouched. The next refresh's preflight cleanup
   removes the orphan.

The git-clone fallback path applies the same discipline to its
copy-out step: regular files only, path containment enforced, no
symlink following.

### Permissions and atomic swap

- The snapshot directory and its contents use default modes (0755 for
  directories, 0644 for files, with the user's umask applied). The
  provenance marker is world-readable: it contains no secrets.
- `instance.json` uses default 0644. The `config_source` block contains
  source URL, tuple, commit oid, and fetched-at — equivalent in
  sensitivity to `git remote -v` output.
- The two-rename swap relies on the workspace owner having exclusive
  write access to `<workspace>/`. niwa does not defend against a
  hostile co-resident user with write access to the workspace parent
  directory; that user already controls the workspace.
- The preflight cleanup of stale `.niwa.next/` and `.niwa.prev/` uses
  `os.Lstat`-aware removal so it cannot be tricked into deleting
  through a planted symlink.

### Credential handling

- `GH_TOKEN` is read once at `APIClient` construction and attached as
  `Authorization: Bearer <token>` on outbound requests. The token
  value is never written to disk, never logged, and never included
  in error messages or surfaced API types.
- Authentication for the git-clone fallback is delegated to git's
  existing credential resolver (SSH agent, `~/.netrc`, credential
  helpers). niwa does not inject or override credentials on the
  fallback path.

### Trust model and supply chain

- niwa fetches whatever the user's source slug names. There is no
  signature verification and no commit-oid pinning by default. Users
  who want pinning specify `@<sha>` in the slug. This matches the
  trust model `git clone` provides.
- Transport integrity comes from HTTPS to `api.github.com` and the
  redirect target on `codeload.github.com`. A hostile GitHub backend
  is out of scope.
- The design introduces no new third-party dependencies. Tarball
  extraction uses Go's stdlib (`archive/tar`, `compress/gzip`,
  `net/http`); the provenance marker reuses the existing
  BurntSushi/toml dependency.

### `.git/`-replacement guardrail interactions

- The plaintext-secrets public-repo guardrail (R31) reads the
  provenance marker's `host`/`owner`/`repo` instead of
  `git remote -v`. When the marker is missing, the guardrail does
  not fire — there is no remote to warn about. An attacker with
  workspace-write access who deletes the marker disables the
  guardrail; this is acceptable in the threat model (workspace-write
  is full ownership) and is by design.
- `niwa reset` (R30) reads the marker to recover the source URL for
  re-fetch. To protect against marker-tampering swap-attacks, the
  reset flow displays the URL it is about to re-fetch from before
  acting; users notice an unexpected URL.
- Lazy state and registry migrations (R23, R24, R28) reject malformed
  files cleanly: a malformed v3 state file leaves the on-disk file
  byte-identical to its pre-load state (R25); a malformed registry
  entry surfaces a parse error rather than being silently rewritten.

### Configurable endpoints

- `NIWA_GITHUB_API_URL` overrides the default `https://api.github.com`
  base URL and is intended primarily for tests against
  `tarballFakeServer`. Production use is supported for self-hosted
  endpoints the user trusts.
- `NIWA_TEST_FAULT` is a test-only seam. In production builds it
  causes a single env-var lookup per `testfault.Maybe` call and has
  no other effect.

### Accepted limitations

- **Snapshot integrity is presence-based**, not content-based. niwa
  treats marker-present-and-parseable as integrity confirmation.
  Tampered but syntactically-valid snapshots are not detected.
  Future enhancements (commit-oid attestation, content-hash
  verification recorded in `instance.json`) can land in follow-up
  releases.
- **`file://` sources bypass TLS** by definition. Users who choose
  `file://` for the git-clone fallback path are trusting the local
  filesystem; this is a deliberate choice, not a niwa weakness.

## Consequences

### Positive

- **Issue #72 becomes structurally impossible.** No working tree
  means no fast-forward, no merge state, no divergence to
  reconcile. The failure mode that triggered the redesign is gone
  by construction.
- **Brain-repo subpath sourcing is first-class.** The same
  pipeline serves `org/dot-niwa` and `org/brain:.niwa` with no
  branching at the data-flow level — the only difference is what
  bytes the fetcher pulls.
- **One snapshot primitive serves all three clone sites.** The
  team config, personal overlay, and workspace overlay all use
  `swapSnapshotAtomic`. Any future correctness fix to the swap
  benefits all three uniformly.
- **One canonical source-identity type.** `Source` flows through
  every consumer (init, config-set, fetcher, registry, state,
  status, guardrail, reset). The "five places represent the same
  concept differently" antipattern doesn't materialize.
- **Test infrastructure is reusable.** `tarballFakeServer`,
  `testfault`, and the state factory aren't one-off; they support
  any future feature that touches the fetch path or schema.

### Negative

- **First-fetch bandwidth is the gzipped repo, not the subpath.**
  GitHub's tarball API doesn't support subpath filtering on the
  server side. For a 1 KB config in a 100 MB brain repo, the user
  pays for the 100 MB download once.
- **State-file dual-path adds read-side complexity for one
  release.** `LoadState`, `DiscoverInstance`, and
  `EnumerateInstances` each carry a fallback branch until v1.1
  removes it.
- **`--allow-dirty` deprecation surface lives for one release.**
  The flag is silently accepted with a stderr notice; users with
  scripts get one release to update before v1.1 hard-removes it.
- **GHE users get the fallback path with no fast-path option.**
  Per the PRD's v1 scope decision, GHE goes through git-clone
  fallback even though the API shape is identical to github.com.
- **Swap window is sub-microsecond, not zero.** The two-rename
  sequence has a brief window where `<workspace>/.niwa/` doesn't
  exist (between the rename to `.niwa.prev/` and the rename of
  `.niwa.next/` into place). The PRD accepts this; it shows up in
  R12's "from the perspective of concurrent readers" framing.

### Mitigations

- **First-fetch bandwidth**: subsequent applies use the 40-byte
  SHA endpoint check. The high cost is amortized over many cheap
  drift checks. Documented in the new guide as expected behavior.
- **Dual-path complexity**: removal scheduled for v1.1; tracking
  via PRD R32 follow-up. The fallback path is small (a single
  `os.Stat` per lookup) and well-tested.
- **`--allow-dirty` deprecation**: notice is printed once per
  process invocation to avoid noise; v1.1 removal communicated in
  release notes.
- **GHE fallback**: per-host adapter for GHE is a v1.x candidate;
  PRD Out of Scope already documents the deferral.
- **Swap window**: no concurrent reader of niwa snapshots exists
  outside niwa itself. niwa never reads `.niwa/` mid-swap. The
  acceptance criterion (AC-M3) verifies that no partial state is
  observable from the apply pipeline's reads.
