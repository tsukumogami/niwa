---
status: Proposed
upstream: docs/prds/PRD-config-source-discovery.md
problem: |
  niwa's snapshot materializer extracts the whole tarball whenever the
  parsed `Source.Subpath` is empty, so `niwa init --from owner/repo`
  against a general-purpose repo writes the entire source repo into
  `<workspace>/.niwa/`. The infrastructure to fetch only a subpath
  exists, but no probe pass inspects the source to decide which subpath
  applies. The overlay slug derivation also splits on subpath in a way
  that becomes brittle once discovery starts producing subpaths from
  bare slugs.
decision: |
  Add a streaming probe to the existing GitHub tarball extraction (and
  its non-GitHub shallow-clone counterpart) that records which marker
  files appear at the source-root level during the first pass, then
  reuses the buffered tarball or already-cloned tree to extract the
  resolved subpath in the second pass. Anchor the auto-discovered
  overlay slug to the source repo name in every case, dropping the
  upstream R35 case-split. Emit one-time deprecation notices via the
  existing DisclosedNotices mechanism when discovery resolves via
  rank-2 for either the team config or the overlay. Migration tooling
  ships as a Claude Code skill that reuses niwa's probe through a
  small read-only Go helper exposed to the skill via the existing CLI.
rationale: |
  The streaming-probe shape minimizes new I/O — discovery rides on the
  fetch that materialization needs anyway, with no extra round-trip
  and no new auth surface. Anchoring overlay derivation to the repo
  name removes a silent behaviour change when bare slugs start
  resolving to subpaths. Migrating registry edits to a skill keeps
  the rarely-used migration path out of niwa's binary surface and
  defers the implementation cost to documentation-as-code. The
  alternatives considered (Contents API probe, two-pass tarball
  fetch, niwa CLI migrate command, separate subpath plumbing) each
  added either round-trips, parallel code paths, or shipping surface
  area that didn't match the migration's one-shot nature.
---

# DESIGN: Config Source Discovery

## Status

Proposed

## Context and Problem Statement

The umbrella PRD `PRD-workspace-config-sources.md` already specifies
subpath-aware sources. The slug parser (`internal/source/parse.go`),
the typed `Source` struct (`internal/source/source.go`), the GitHub
tarball + selective-extraction pipeline
(`internal/workspace/snapshotwriter.go`, `internal/github/tar.go`),
the non-GitHub shallow-clone fallback (`internal/workspace/fallback.go`),
the snapshot-with-provenance posture, and the overlay snapshot fetch
all already accept and honour `Source.Subpath` when it is set
explicitly. The gap is at the entry boundary: when the user types
`niwa init --from owner/repo` with no explicit subpath, `Source.Subpath`
is empty, and the materializer at `internal/workspace/snapshotwriter.go:440`
calls `github.ExtractSubpath(body, "", staging)` — which means "extract
everything." There is no probe pass to inspect the source for a
`.niwa/workspace.toml` or a root `workspace.toml` and decide which
subpath to resolve to.

This PRD-tracked work has four implementation challenges:

1. **Streaming probe-and-extract.** The probe needs to discover which
   markers exist at the source-root level before deciding what to
   extract. The fetch already streams the gzipped tarball through
   `archive/tar`. Making a second tarball request to probe doubles
   the bandwidth on what is already the most expensive operation in
   apply; making a separate API call adds a round-trip the existing
   architecture explicitly rejected. The probe needs to happen
   in-stream against the same response body that materialization
   reads, while preserving the security defenses already encoded in
   `ExtractSubpath` (decompression-bomb cap, path-containment,
   wrapper-stripping, atomic failure).

2. **Probe semantics for the non-GitHub path.** The shallow-clone
   fallback at `internal/workspace/fallback.go:42` produces a temp
   directory with the working tree. Probing there is a directory
   listing — cheaper than the GitHub case, but the same precedence
   rules (rank 1 wins over rank 2; empty `.niwa/` is not rank 1;
   ambiguity fails) must produce identical observable behaviour.

3. **Overlay derivation that resists discovery side effects.**
   `Source.OverlayDerivedSource()` today computes
   `<owner>/<basename-of-subpath>-overlay` whenever a subpath is
   present and `<owner>/<repo>-overlay` otherwise. Once discovery
   starts populating `Source.Subpath = ".niwa"` from bare slugs, the
   same `--from acme/vision` invocation that produced overlay
   `acme/vision-overlay` before discovery would silently start
   producing `acme/.niwa-overlay` after — a behaviour change buried
   in a function the caller doesn't think about. The design needs
   to short-circuit this path so the overlay slug derives from the
   source repo name in every case.

4. **One-time rank-2 deprecation surface.** The PRD requires a
   one-time notice per workspace per artifact per command-type when
   discovery resolves via rank-2 (the legacy whole-repo shape) for
   either the team config or the overlay. The
   `DisclosedNotices` mechanism (used by upstream R18, R28, R32
   already) is the right vehicle, but it needs new notice IDs and
   the discovery code needs to know which rank it resolved to so it
   can request the right notice.

5. **Migration skill that probes without re-implementing the
   pipeline.** The PRD ships migration as a Claude skill, not a niwa
   command. The skill needs the same probe niwa runs — but
   re-implementing the tarball stream-and-scan in a non-Go language
   would duplicate the security-sensitive code path. The design
   needs an exposed niwa entry point (CLI subcommand or similar)
   that performs the probe and returns a machine-readable result,
   so the skill can shell out instead of re-implementing.

The work surface touches: `internal/source/source.go`
(overlay derivation), `internal/github/tar.go` (the extraction
pipeline gets a probe pass), `internal/workspace/snapshotwriter.go`
(orchestrates probe → resolve → extract), `internal/workspace/fallback.go`
(non-GitHub probe), `internal/workspace/disclosure.go` (new notice
IDs — file location to be confirmed in Phase 4), `internal/cli/init.go`
(error message contract per R21), the registry plumbing
(`internal/config/registry.go`) for the migrate-source skill's
read-only entry point, and the corresponding test fixtures
(`tarballFakeServer`, `localGitServer`).

## Decision Drivers

The PRD-derived drivers and implementation-specific drivers that
shape the technical choices below:

- **No new round-trips (R7).** Discovery must ride on the existing
  fetch. Adding a separate API call to probe is explicitly
  forbidden; both the GitHub and non-GitHub paths must use a single
  fetch for probe + extract.
- **Security defenses preserved on the probe pass (R8).** The
  decompression-bomb cap, wrapper-stripping, path-containment, and
  atomic-failure invariants in `internal/github/tar.go` must apply
  unchanged. The probe must not introduce a new failure mode where
  a tarball that would have been rejected during normal extraction
  is accepted during probing.
- **Atomic snapshot integrity (R5; upstream R12).** Discovery
  failure must leave the on-disk `<workspace>/.niwa/` byte-identical
  to its pre-init state, including when probe-then-extract decides
  partway through that the input is unusable. No partial state.
- **Overlay slug stability under discovery (R10).** The auto-discovered
  overlay slug derives from the source repo name only. The
  discovery decision (what subpath ended up resolving) must not
  flow into overlay slug derivation, even though both happen inside
  the same `Source` value's lifetime.
- **Coexistence (R13).** Both rank-1 and rank-2 paths resolve in
  this release. Whichever code path lands the probe must not
  short-circuit rank-2 just because rank-2 is deprecated — apply
  must succeed for legacy registries with no flags.
- **Notice once-and-only-once per artifact (R14).** The
  `DisclosedNotices` mechanism is the existing pattern; new notices
  must compose with it (one ID per artifact per command-type) so
  the existing test scaffolding for notices doesn't need a new
  category.
- **Skill probe reuses niwa code (R18).** The migration skill must
  call into niwa rather than re-implement. The niwa surface area
  exposed to the skill should be the minimum needed: a read-only
  probe that takes a slug and returns a structured result.
- **No new test infrastructure beyond the upstream PRD's fixtures.**
  The PRD's acceptance criteria reference `tarballFakeServer`,
  `localGitServer`, and the legacy-working-tree fixture; the design
  must not require new fixture types or test seams.
- **`internal/source` is a leaf package.** `internal/source/source.go`
  imports nothing from the rest of niwa today. Overlay derivation
  changes must keep that property; no callbacks into `internal/config`
  or `internal/workspace` from the source package.
- **Forward-compatibility with the deferred rank-2 removal.** A
  follow-up release will hard-remove rank-2. The probe code path
  should let rank-2 acceptance be a single guard, easy to delete
  in the follow-up, not a branch that propagates rank-2-handling
  logic into the materializer.
