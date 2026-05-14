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

## Considered Options

### Decision 1: Probe-and-resolve pipeline architecture and rank-2 guard location

**Context.** The umbrella PRD shipped the slug grammar, the GitHub
tarball fetch, the non-GitHub shallow-clone fallback, the snapshot
materialization model, and the overlay sub-fetch. The probe pass —
the discovery step that decides whether `--from owner/repo` (no
explicit subpath) resolves to rank-1 (`.niwa/workspace.toml`) or
rank-2 (root `workspace.toml`) — was never built. The same probe
needs to operate across three fetch sites: the GitHub tarball path,
the non-GitHub shallow-clone path, and the overlay sub-fetch (R12,
parameterized by overlay marker filenames). The rank-2 acceptance
flag must live in a single, deletable site so the follow-up release
(PRD R15) can remove rank-2 with one mechanical edit.

Key assumptions:
- The existing 500 MB decompression-bomb cap
  (`MaxDecompressedBytes`) is the binding resource ceiling; buffering
  the decompressed tar within that cap is acceptable.
- GitHub tarball entry ordering is unconstrained, so a true
  single-pass stream-and-decide collapses to the same buffering
  footprint as the chosen design.
- The non-GitHub fallback already materializes the full shallow
  clone on disk, so a directory-listing probe adds zero I/O
  round-trips.

#### Chosen: Decompress-once-to-buffer, two-pass over decompressed tar, centralized `RankDecider`

A new function `ProbeAndExtractSubpath` lives in `internal/github/tar.go`:

1. Reads the gzipped stream through the existing
   `io.LimitReader(r, MaxDecompressedBytes+1)` cap on the
   *compressed* input (unchanged from `internal/github/tar.go:63`
   today), then drains the decompressed bytes through a NEW
   `io.LimitReader(gz, MaxDecompressedBytes+1)` on the
   *decompressed* output into an internal pre-allocated
   `bytes.Buffer` with a small initial capacity (e.g.,
   1 MB — the common-case decompressed size for config-bearing
   sources). The buffer grows geometrically beyond that but
   is bounded by the decompressed cap; pre-allocation
   significantly reduces doubling-related peak RAM compared
   with a zero-initial-capacity buffer. If either limit fires
   during the read, the function returns the existing
   cap-exceeded diagnostic before any disk write. Pass 2's
   `ExtractSubpath` retains its existing cumulative-decompressed-
   bytes check (`internal/github/tar.go:150-168`) — that defense
   is unchanged.
2. Pass 1 (probe): iterates `tar.NewReader(bytes.NewReader(buf))`.
   For each entry, applies the existing wrapper-anchoring,
   filename-validation, and type-allowlist checks. Records only
   whether each marker in `MarkerSet` exists at the source-root
   level. No file bytes written; headers only.
3. Calls `RankDecider(found, markers)` to resolve rank-1-wins-over-
   rank-2 precedence and emit a `*DeprecationNotice` value if rank-2
   resolved.
4. Pass 2 (extract): if the decider returned a subpath, calls the
   existing `ExtractSubpath` against a fresh
   `tar.NewReader(bytes.NewReader(buf))` with the resolved subpath.
   All seven security defenses run unchanged on this pass.

The non-GitHub path: `FetchSubpathViaGitClone` in
`internal/workspace/fallback.go` runs `git clone --depth=1` into a
temp dir as today, then calls a new sibling
`probeAndResolveCloneRoot(tmp, markers, decider)` that `os.Stat`s
each marker file at the clone's top level, calls the same
`RankDecider`, and returns the resolved subpath before
`cloneAndCopy`.

The overlay sub-fetch: `EnsureOverlaySnapshot` in
`internal/workspace/overlaysync.go` calls the same probe pipeline,
parameterized by the overlay `MarkerSet`
(`.niwa/workspace-overlay.toml`, root `workspace-overlay.toml`).
The silent-skip-on-failure behaviour from upstream R35 / PRD R11
wraps the call.

The rank-2 guard lives in `RankDecider`'s body:

```go
// BEGIN rank-2 deprecated branch — remove in the follow-up release
// that hard-removes rank-2 discovery per PRD-config-source-discovery R15.
const rankTwoAccepted = true
if rankTwoAccepted && found.HasRank2() {
    return "", &DeprecationNotice{Rank: 2, Markers: markers}, nil
}
// END rank-2 deprecated branch
```

The follow-up release deletes the marked branch and `MarkerSet`'s
rank-2 fields; no other site changes.

Rationale: this design satisfies all six decision drivers with the
smallest surface change. The 500 MB cap stays binding; security
defenses in `internal/github/tar.go` run unchanged on both passes;
probe failures return before pass 2 starts so the staging dir is
empty and the caller's existing `safeRemoveAll` deferred cleanup
leaves `<workspace>/.niwa/` byte-identical; both ranks resolve
through one gated branch; the rank-2 deletion target is one
function, marked with grep-able BEGIN/END comments; and the
existing `tarballFakeServer` / `localGitServer` fixtures cover the
new code paths without new test infrastructure.

#### Alternatives Considered

- **Single-pass stream-and-decide that buffers eligible entries.**
  Rejected. Tarball entry ordering is unconstrained; in the rank-2
  case the eligible-entries set is the entire repo, collapsing the
  design to the same buffer footprint as the chosen option with
  worse control-flow clarity.
- **Two-pass with re-decompression via a second network fetch.**
  Rejected by PRD R7. Doubles bandwidth on every cold apply and
  adds a second auth round-trip with new failure modes.
- **Buffer the gzipped bytes instead of the decompressed bytes.**
  Rejected as inferior: doubles CPU (re-decompress on each pass) to
  save memory on the smaller raw-bytes cost; forces awkward cap
  semantics that either weaken the bomb defense or defeat the
  memory saving.
- **Marker-decision callback embedded inside `ExtractSubpath`.**
  Rejected. Pollutes `ExtractSubpath`'s single responsibility
  (subpath-filtered extraction with seven security defenses).
  Keeping the probe as a separate caller preserves the
  security-audited function's surface and tests.
- **Rank-2 guard as a parameter threaded through every probe
  callsite.** Rejected. Forces the follow-up release to edit every
  callsite plus the parameter itself. A single named constant
  inside `RankDecider` gates the same behaviour with one deletion
  point.
- **Rank-2 guard as a bare TODO with no structural marker.**
  Rejected on hygiene grounds. Without a named gate and BEGIN/END
  delimiters, the rank-2 branch invites tendrils (a deprecation
  notice here, a metric there) across unrelated modules over time.

### Decision 2: Overlay slug derivation override

**Context.** `Source.OverlayDerivedSource()` in
`internal/source/source.go:127-141` currently implements upstream
PRD R35's case-split: whole-repo sources derive
`<repo>-overlay`; subpath sources derive
`<basename-of-subpath>-overlay`. PRD R10 overrides this with an
unconditional repo-name rule. `internal/source` is a leaf package
that imports nothing else in niwa; the override must preserve that
property.

Key assumptions: zero production callers of
`OverlayDerivedSource()` exist outside the niwa module (Go internal
visibility plus a repo-wide grep confirmed this), so deprecation
cycles buy no migration safety.

#### Chosen: Modify `OverlayDerivedSource()` in place

Delete the `if s.Subpath != ""` branch and the `lastPathSegment()`
helper (becomes dead code with no other callers). Rewrite the doc
comment to cite PRD R10 and remove the R35 case-split language.
Flip the existing test cases in `internal/source/source_test.go`
(around lines 217-256): the same input shapes (whole-repo,
single-segment subpath, multi-segment subpath, ref inheritance,
host inheritance) now all assert `<repo>-overlay` to demonstrate
R10's invariance under subpath.

Rationale: the function becomes trivially short, the leaf-package
property is preserved (no new imports), and the test surface
expands by exactly the cases R10 introduces (subpath-invariance)
without adding new test scaffolding.

#### Alternatives Considered

- **Add `OverlayDerivedSourceV2()` and deprecate the old.**
  Rejected. No callers to migrate, so the deprecation cycle is pure
  overhead. Leaves the R35 subpath behaviour callable in a package
  whose job is to encode the *current* canonical identity, and
  creates a follow-up cleanup PR that does nothing but delete the
  old method.
- **Parameterize via a style enum or options struct.** Rejected.
  The only use case for the old "R35 style" is historical — not a
  use case. Adds a type to a leaf package that had none, doubles
  the test surface for no functional benefit, and either breaks
  the method shape or collapses into the V2 alternative via a
  wrapper.

### Decision 3: Rank-2 deprecation notice wiring

**Context.** PRD R14 requires a one-time `note:`-prefixed notice
to stderr when discovery resolves via rank-2, scoped per workspace
per artifact (team config / overlay) per command-type (init /
apply). The existing `DisclosedNotices` mechanism (used by upstream
R18 rename redirect, R28 working-tree conversion, R32 `--allow-dirty`
deprecation) is the right vehicle. The probe owns the rank
resolution; someone has to translate `(rank, artifact, context)`
into a notice ID + message and emit it via `DisclosedNotices`.

Key assumptions: Decision 1's probe signature exposes the rank to
its caller (`(subpath, rank, err)`-style return); the notice
cardinality "once per workspace per artifact per command-type"
treats a workspace's init+apply as a single lifecycle (a workspace
either sees the init-context notice or the apply-context notice,
not both, because by apply time init's notice already persisted).

#### Chosen: Probe returns descriptor; tiny `disclosure.go` helper centralises notice rendering

The probe stays pure: returns `(resolvedSubpath, rank, *DeprecationNotice,
err)`. The pipeline layer in `internal/workspace/snapshotwriter.go`
(or its callers in `internal/cli/init.go` and
`internal/workspace/apply.go`) checks the rank and calls a small
helper:

```go
// internal/workspace/disclosure.go (new file)
package workspace

const (
    NoticeIDRank2TeamConfig = "rank2-deprecation:team-config"
    NoticeIDRank2Overlay    = "rank2-deprecation:overlay"
)

// EmitRank2Notice records the rank-2 deprecation notice in the
// instance-scoped DisclosedNotices list (the []string field on
// InstanceState at internal/workspace/state.go:114, persisted in
// <workspace>/.niwa/instance.json via mergeDisclosedNotices on next
// state-save). The persistence scope is per-instance: the notice
// fires once for a given workspace and is suppressed on subsequent
// init/apply commands against that same workspace. A different
// workspace using the same source slug fires its own notice.
//
// This matches how upstream R18/R28/R32 use DisclosedNotices today.
func EmitRank2Notice(state *InstanceState, id, identifier string, reporter *Reporter) {
    if state.noticeDisclosed(id) {
        return
    }
    msg := fmt.Sprintf(
        "note: %s is using the deprecated whole-repo config layout "+
            "(root workspace.toml). Future releases will require config under "+
            ".niwa/workspace.toml. To migrate, run: "+
            "/shirabe:niwa-migrate-config %s in Claude Code.",
        identifier, identifier,
    )
    reporter.Log(msg)
    state.DisclosedNotices = append(state.DisclosedNotices, id)
}
```

Emission happens *after* snapshot promotion succeeds (after the
atomic swap), so a fetch failure between the probe and the swap
never leaves a deprecation notice for a snapshot that never
landed. The notice's literal-substring requirements
(`deprecated`, identifier, `/shirabe:niwa-migrate-config` per
PRD R14 and AC-N1 / AC-N2) live in one function — one place to
change, one place to test.

Rationale: the probe stays free of `*Reporter` and notice-state
dependencies, so it remains reusable by Decision 4's `niwa source
inspect` command (which is read-only and must not emit
deprecation notices to stderr — the CLI returns the rank as JSON
instead). The helper localises the message text so the AC's
substring requirements have a single source of truth.

#### Alternatives Considered

- **Probe returns descriptor; pipeline-level branching duplicates
  the message at every call site.** Rejected. Duplicates the
  message text across three sites (team-config apply, overlay
  apply, init CLI), creating three places to keep AC-N's required
  literal substrings in sync. The helper costs one small file and
  one function; it pays back with a single source of truth.
- **Probe emits notices directly via a reporter.** Rejected on
  atomic-snapshot-integrity grounds. The snapshot writer's
  existing R18 rename-redirect emit happens before
  `SwapSnapshotAtomic`, so a marker-write or extraction failure
  between emit and swap would leave a deprecation notice for a
  snapshot that never landed. Also pushes workspace-name and
  artifact-identity context into the snapshot writer, which today
  has neither and gains no other benefit. The R18 precedent does
  not extend here because R18's message is context-free; R14's is
  not.

### Decision 4: niwa CLI entry point for the migration skill

**Context.** The shirabe migration skill needs to probe a slug
without re-implementing the tarball-stream-and-scan in
TypeScript/JavaScript (PRD R18). Path (b) of the migration
(slug-swap) probes an *unregistered* destination slug, so the
entry point must accept arbitrary slugs, not just workspace
names. The probe must be read-only (no fetch beyond what
discovery would do anyway; no registry writes; no snapshot
mutation).

Key assumptions: the skill invokes niwa via Bash and reads stdout
as structured JSON; niwa's design ethos favours
`--help`-discoverability over hidden internal commands; the probe
function from Decision 1 can be exposed as a read-only function
over a slug (D1 confirms a separable probe phase exists).

#### Chosen: `niwa source inspect <slug> [--json]` user-facing subcommand

A new subcommand under a new `source` noun. The command:

- Accepts a slug in the standard `[host/]owner/repo[:subpath][@ref]`
  grammar (parsed via `internal/source/Parse`).
- Calls the same probe logic Decision 1 ships
  (`ProbeMarkers` extracted from `ProbeAndExtractSubpath` per the
  Phase 3 cross-validation refinement) but skips pass 2 (extraction).
- Outputs human-readable text by default; JSON when `--json` is
  passed. The JSON schema is versioned with a `schema_version: 1`
  field.

JSON output shape (for a successful probe):

```json
{
  "schema_version": 1,
  "slug": "acme/dot-niwa",
  "markers_found": ["workspace.toml"],
  "resolved_rank": 2,
  "resolved_subpath": "",
  "suggested_new_slug_inplace": "acme/dot-niwa",
  "suggested_new_slug_brain_repo_example": "acme/brain"
}
```

Rationale: path (b) of the migration probes unregistered
destination slugs, forcing a slug-accepting entry point regardless.
Once the shape accepts a slug, the discoverability cost of a
visible subcommand is zero (`niwa source inspect --help`); a
hidden `__probe-source` would hide diagnostic value the only known
consumer (the user) might want.

#### Alternatives Considered

- **Hidden `niwa __probe-source <slug>`.** Rejected. Hiding the
  command breaks niwa's `--help`-driven-discoverability pattern with
  no compensating benefit; JSON contract stability is served by
  `schema_version`, not by hiding the command.
- **Workspace-name-only `niwa probe <workspace-name>`.** Rejected.
  Cannot serve path (b)'s unregistered-destination probe; strictly
  dominated by any slug-accepting variant.
- **Overload `niwa status <name> --probe-source --json`.**
  Rejected. `status` reports state of registered, materialized
  workspaces; probe operates on source slugs that may not be
  registered. Semantic mismatch confuses `--help` and the user's
  mental model.
- **Separate Go binary.** Rejected. Doubles release-artifact
  count, creates drift risk between binaries, offers no benefit
  over a subcommand of the existing binary.

## Decision Outcome

The four decisions compose into a single coherent pipeline.

**Step 1 — Probe** (Decision 1): on `niwa init --from owner/repo`
(or any apply that reads from a registry slug without an explicit
subpath), niwa fetches the source via the existing GitHub tarball
path or non-GitHub shallow-clone fallback, decompresses into a
bounded buffer (within the 500 MB cap), iterates tar headers (or
runs `os.Stat` on the shallow clone's root), and records which
markers from `MarkerSet` exist at the source-root level. The same
pipeline runs against the overlay sub-fetch with the overlay
`MarkerSet`.

**Step 2 — Decide** (Decision 1's `RankDecider`): the probe's
findings pass into a single function that resolves rank-1 over
rank-2, surfaces ambiguity / no-marker errors per PRD R3 / R4, and
returns a `(subpath, rank, *DeprecationNotice, err)` tuple. The
rank-2 acceptance is gated by one named constant inside this
function.

**Step 3 — Extract** (Decision 1, pass 2): the resolved subpath
flows into the existing `ExtractSubpath` (GitHub) or `cloneAndCopy`
(non-GitHub), which runs unchanged. All seven security defenses
apply on the extraction pass.

**Step 4 — Promote** (existing `SwapSnapshotAtomic`): the staged
snapshot replaces the previous one atomically. The provenance
marker records the resolved subpath (today it copies the empty
`Source.Subpath`; after this design the resolved value flows
through).

**Step 5 — Disclose** (Decision 3): once promotion succeeds, the
caller checks the rank. If rank-2, the `disclosure.go` helper
emits the one-time deprecation notice via `DisclosedNotices`,
scoped to the workspace and artifact. The notice never fires
before promotion, so a failed apply never leaves a notice for a
snapshot that never landed.

**Step 6 — Overlay derivation** (Decision 2): `niwa apply` calls
`src.OverlayDerivedSource()` to construct the overlay slug; the
function returns `<owner>/<repo>-overlay` regardless of subpath.
The overlay snapshot is then materialized using the same Step
1-5 pipeline, parameterized by overlay marker filenames.

**Step 7 — Migration skill surface** (Decision 4): the shirabe
skill shells out to `niwa source inspect <slug> --json` to probe
a registered workspace's source or a candidate destination slug.
The same probe code (factored as `ProbeMarkers` from
`ProbeAndExtractSubpath`) runs in read-only mode — no extraction,
no notice emission, no registry mutation. The skill parses the
JSON, presents migration paths to the user, and edits the
registry on the user's confirmation.

## Solution Architecture

### Overview

The design adds a probe pass to the existing fetch pipeline, a
shared decider that resolves the rank, and a small notice helper.
Three of the four decisions touch the existing snapshot
materialization seam; the fourth (Decision 4) adds a new CLI noun
that calls into the same probe primitives. The user-visible
contract changes are: bare `--from owner/repo` slugs now resolve
to the right subpath via discovery; rank-2 legacy sources emit a
one-time deprecation notice; the overlay slug becomes predictable;
and a new `niwa source inspect` subcommand lets the migration
skill (and future diagnostic users) inspect any slug's root
layout.

### Components

```
internal/source/                                  (leaf package, unchanged surface)
  source.go
    OverlayDerivedSource()                        # Decision 2: subpath case-split removed
    lastPathSegment()                             # Decision 2: deleted (dead code)

internal/config/discover.go                       (existing file, new types)
  type MarkerSet struct{ Rank1Dir, Rank2Path string; … }
  type DeprecationNotice struct{ Rank int; … }
  ProbeMarkers(tar *tar.Reader) (found MarkerSet, err error)
  RankDecider(found, markers MarkerSet)           # Decision 1: rank-2 BEGIN/END guard
      (subpath string, notice *DeprecationNotice, err error)
  TeamConfigMarkerSet() MarkerSet                 # rank-1 .niwa/workspace.toml; rank-2 root workspace.toml
  OverlayMarkerSet() MarkerSet                    # rank-1 .niwa/workspace-overlay.toml; rank-2 root workspace-overlay.toml

internal/github/tar.go                            (existing file, new entry point)
  ExtractSubpath(r, subpath, dest)                # unchanged (still callable directly)
  ProbeAndExtractSubpath(r, markers, decider, dest)
      (resolvedSubpath, rank int, notice *DeprecationNotice, err error)
                                                  # decompress-once-to-buffer + pass 1 probe + pass 2 extract

internal/workspace/fallback.go                    (existing file)
  FetchSubpathViaGitClone(src, staging)           # unchanged signature
    probeAndResolveCloneRoot(tmp, markers, decider)
        (subpath, rank, notice, err)              # new helper called before cloneAndCopy
    cloneAndCopy(tmp, subpath, staging)           # unchanged

internal/workspace/snapshotwriter.go              (existing file)
  MaterializeFromSource(ctx, src, sourceURL, configDir, fetcher, reporter)
                                                  # returns rank int additionally
  materializeAndSwap()                            # calls ProbeAndExtractSubpath instead of ExtractSubpath
  EnsureConfigSnapshotWithStatus()                # returns rank to caller for notice emission

internal/workspace/overlaysync.go                 (existing file)
  EnsureOverlaySnapshot()                         # gains probe step using OverlayMarkerSet()
                                                  # silent-skip wraps probe + extract per upstream R35 / PRD R11

internal/workspace/disclosure.go                  (NEW file)
  const NoticeIDRank2TeamConfig
  const NoticeIDRank2Overlay
  EmitRank2Notice(state *InstanceState, id, identifier string, reporter)
                                                  # persists notice ID into <workspace>/.niwa/instance.json

internal/cli/init.go                              (existing file)
  runInit()                                       # captures rank from MaterializeFromSource;
                                                  # after success, calls EmitRank2Notice if rank == 2

internal/cli/apply.go                             (existing file)
  runApply()                                      # captures rank from EnsureConfigSnapshotWithStatus;
                                                  # after promotion, calls EmitRank2Notice for team + overlay

internal/cli/source_inspect.go                    (NEW file)
  sourceInspectCmd                                # niwa source inspect <slug> [--json]
                                                  # parses slug via internal/source.Parse
                                                  # fetches + ProbeMarkers (no extract, no notice)
                                                  # emits JSON or human text per --json
```

### Key Interfaces

**`MarkerSet`** (in `internal/config/discover.go`):

```go
type MarkerSet struct {
    Rank1Dir   string // ".niwa"  -- if probe finds <Rank1Dir>/<Rank1Filename>, rank-1 resolves
    Rank1File  string // "workspace.toml" or "workspace-overlay.toml"
    Rank2Path  string // "workspace.toml" or "workspace-overlay.toml" -- at root
}

// HasRank1 reports whether the probe found the rank-1 marker file
// at the source-root level (i.e., <Rank1Dir>/<Rank1File> exists).
// Empty .niwa/ directories with no Rank1File inside do NOT count
// (R6, AC-D8).
func (m MarkerSet) HasRank1() bool { … }

// HasRank2 reports whether the probe found the rank-2 marker file
// at the source root (Rank2Path).
func (m MarkerSet) HasRank2() bool { … }
```

**`ProbeAndExtractSubpath`** (in `internal/github/tar.go`):

```go
// ProbeAndExtractSubpath buffers the decompressed tarball into RAM
// (bounded by MaxDecompressedBytes), iterates the tar headers to
// detect which markers exist at the source-root level, calls
// decider to resolve the rank, then re-iterates the buffered bytes
// to extract entries under the resolved subpath into dest.
//
// Returns the resolved subpath, the rank that won (1 or 2), and a
// deprecation notice if rank-2 resolved (caller emits via
// DisclosedNotices).
func ProbeAndExtractSubpath(
    r io.Reader,
    markers config.MarkerSet,
    decider func(found, markers config.MarkerSet) (string, *config.DeprecationNotice, error),
    dest string,
) (resolvedSubpath string, rank int, notice *config.DeprecationNotice, err error)
```

**`niwa source inspect` JSON schema (v1)**:

```jsonc
// success case
{
  "schema_version": 1,
  "slug": "acme/dot-niwa",
  "host": "github.com",
  "owner": "acme",
  "repo": "dot-niwa",
  "explicit_subpath": "",
  "markers_found_at_root": ["workspace.toml"],
  "resolved": {
    "rank": 2,
    "subpath": "",
    "deprecated": true,
    "migration_hint": "Move config in acme/dot-niwa into .niwa/, or switch to a brain-repo slug like acme/brain."
  }
}

// ambiguity case (PRD R3)
{
  "schema_version": 1,
  "slug": "acme/messy",
  "host": "github.com", "owner": "acme", "repo": "messy",
  "explicit_subpath": "",
  "markers_found_at_root": [".niwa/workspace.toml", "workspace.toml"],
  "error": {
    "code": "ambiguous",
    "message": "ambiguous niwa config in acme/messy: found both .niwa/workspace.toml and workspace.toml"
  }
}

// no-marker case (PRD R4)
{
  "schema_version": 1,
  "slug": "acme/random",
  "host": "github.com", "owner": "acme", "repo": "random",
  "explicit_subpath": "",
  "markers_found_at_root": [],
  "error": {
    "code": "no_marker",
    "message": "no niwa config found in acme/random: probed .niwa/workspace.toml and workspace.toml at repo root"
  }
}
```

The JSON shape is versioned via `schema_version`; future changes
bump that field. Errors are returned as a top-level `error` object
rather than via process exit codes, but the process still exits
non-zero on probe failure so shell-piped consumers can branch on
exit status without parsing JSON.

### Data Flow

```
                    niwa init --from owner/repo
                              │
                              ▼
            ┌─────────────────────────────────┐
            │ internal/cli/init.go            │
            │   parseInitSource(slug)         │
            │   MaterializeFromSource()       │──┐
            └─────────────────────────────────┘  │
                                                 │
                                                 ▼
            ┌─────────────────────────────────────────────────┐
            │ internal/workspace/snapshotwriter.go            │
            │   materializeAndSwap()                          │
            │     ├── src.IsGitHub() yes ──> fetchTarball ──> │
            │     │                          ProbeAndExtractSubpath
            │     │                          (decompress→buffer→pass1 probe→
            │     │                           RankDecider→pass2 ExtractSubpath)
            │     │                                          │
            │     └── non-GitHub        ──> FetchSubpathViaGitClone:
            │                                git clone --depth=1 →
            │                                probeAndResolveCloneRoot →
            │                                RankDecider → cloneAndCopy
            │                                                │
            │     WriteProvenance(staging, prov{Subpath:resolved, …})
            │     SwapSnapshotAtomic(configDir, staging)     │
            └─────────────────────────────────────────────────┘
                                                 │
                                                 ▼
            ┌─────────────────────────────────────────────────┐
            │ internal/cli/init.go (back)                     │
            │   if rank == 2:                                 │
            │     EmitRank2Notice(state, NoticeID..., slug) │
            └─────────────────────────────────────────────────┘
                                                 │
                                                 ▼
                          (init returns; user sees notice if rank-2)


                    niwa apply <workspace-name>
                              │
                              ▼
            ┌─────────────────────────────────┐
            │ internal/cli/apply.go           │
            │   for each workspace:           │
            │     EnsureConfigSnapshotWithStatus(team-config dir, fetcher, reporter)
            │     EnsureOverlaySnapshot(overlay dir, fetcher, reporter)
            │     │                           │ (each returns rank)
            │     v                           v
            │   if team-rank == 2:            │
            │     EmitRank2Notice(state, NoticeIDRank2TeamConfig, name, reporter)
            │   if overlay-rank == 2:         │
            │     EmitRank2Notice(state, NoticeIDRank2Overlay, name, reporter)
            └─────────────────────────────────┘


                    /shirabe:niwa-migrate-config <workspace>
                              │
                              ▼
            ┌─────────────────────────────────┐
            │ shirabe skill (Claude Code)     │
            │   Read ~/.config/niwa/config.toml: get source_url
            │   Bash: niwa source inspect <source_url> --json
            │                                 │
            │                                 ▼
            │            ┌────────────────────────────────────────┐
            │            │ internal/cli/source_inspect.go         │
            │            │   parse slug                           │
            │            │   fetch tarball (or shallow clone)    │
            │            │   ProbeMarkers (pass 1 only;          │
            │            │                  no extract,           │
            │            │                  no notice emission)   │
            │            │   render JSON                          │
            │            └────────────────────────────────────────┘
            │                                 │
            │   Parse JSON; present user with migration paths
            │   On user confirmation (slug-swap path):
            │     Edit ~/.config/niwa/config.toml: rewrite source_url
            │     Print: "Run `niwa apply --force <workspace>` to materialise."
            └─────────────────────────────────┘
```

## Implementation Approach

The implementation breaks into six sequential commits, each landable
in isolation and verifiable by the existing test infrastructure.

### Phase 1: `MarkerSet`, `RankDecider`, and unit tests

Land the new types and the shared decider in
`internal/config/discover.go`. No callers yet; the function is
exercised purely by unit tests covering the decision matrix
(rank-1 present, rank-2 present, both, neither, with and without
`rankTwoAccepted`). The BEGIN/END guard comments land here.

Deliverables:
- `internal/config/discover.go`: new types and `RankDecider`
- `internal/config/discover_test.go`: unit tests for the decision
  matrix
- `internal/config/discover_marker_set_test.go`: unit tests for
  `MarkerSet` predicates (`HasRank1`, `HasRank2`, including the
  empty-`.niwa/` case)

### Phase 2: `ProbeAndExtractSubpath` in `internal/github`

Add the new entry point alongside `ExtractSubpath`. The existing
function stays callable; only the new function is added. Unit
tests cover: rank-1 buffer-and-extract, rank-2 buffer-and-extract,
ambiguity error before any write, no-marker error before any
write, cap-exceeded during buffer fill, truncated tarball mid-fill,
and an end-to-end "extracted content matches the source's subpath
exactly" assertion.

Deliverables:
- `internal/github/tar.go`: `ProbeAndExtractSubpath` and a small
  `probeMarkersFromHeaders` helper
- `internal/github/tar_test.go`: 8-10 new test cases under existing
  `tarballFakeServer` fixture conventions, including:
  - `TestProbeAndExtract_DecompressionBombDefense`: duplicates the
    existing extract-pass cap test for the new entry point, asserting
    the cap fires at the same byte budget.
  - `TestProbeAndExtract_SymlinkMarkerIsNotRank1`: a tarball whose
    `.niwa/workspace.toml` entry is a symlink (rejected by the type
    allowlist) MUST NOT be detected as a rank-1 marker by the probe
    pass. Guards against future divergence between the probe-pass
    and extract-pass allowlist checks.

### Phase 3: Non-GitHub probe and overlay subpath-awareness

Add `probeAndResolveCloneRoot` in
`internal/workspace/fallback.go` and wire it into
`FetchSubpathViaGitClone` between the clone and the copy. Extend
`EnsureOverlaySnapshot` in `internal/workspace/overlaysync.go` to
call the same probe pipeline parameterised by `OverlayMarkerSet()`.

Deliverables:
- `internal/workspace/fallback.go`: probe helper + integration
- `internal/workspace/fallback_test.go`: probe cases via
  `localGitServer`
- `internal/workspace/overlaysync.go`: overlay probe wiring
- `internal/workspace/overlaysync_test.go`: AC-V1, AC-V2, AC-V3,
  AC-V4 coverage

### Phase 4: Snapshot writer integration + provenance

Wire `materializeAndSwap` to call `ProbeAndExtractSubpath` for
GitHub and the new probe-then-copy flow for non-GitHub. Bubble the
`rank` and resolved subpath up to `MaterializeFromSource`'s return
signature; write the resolved subpath into the provenance marker.

Deliverables:
- `internal/workspace/snapshotwriter.go`: updated signatures and
  call sites
- `internal/workspace/snapshotwriter_test.go`: end-to-end probe
  scenarios (AC-D1 through AC-D8 happy paths and failure modes)
- `internal/workspace/apply.go`: minimal call-site change to
  capture rank

### Phase 5: Overlay slug override + deprecation notice helper

Land the `OverlayDerivedSource()` change in
`internal/source/source.go` (delete the subpath branch + the
`lastPathSegment` helper). Flip the existing test cases.

Land `internal/workspace/disclosure.go` with the helper +
constants. Wire `internal/cli/init.go` and
`internal/workspace/apply.go` call sites to emit the notice
after promotion.

Deliverables:
- `internal/source/source.go`, `internal/source/source_test.go`
- `internal/workspace/disclosure.go` (NEW)
- `internal/cli/init.go` and `internal/workspace/apply.go` updates
- End-to-end AC-N1 through AC-N6 and AC-V1 through AC-V6 coverage

### Phase 6: `niwa source inspect` CLI + migration skill

Add the new subcommand. Implement `ProbeMarkers` as a factored
version of `ProbeAndExtractSubpath`'s pass-1 (shared by the
production path and the inspect command). Author the
`/shirabe:niwa-migrate-config` skill markdown.

Deliverables:
- `internal/cli/source_inspect.go` (NEW) +
  `internal/cli/source_inspect_test.go`
- Refactor in `internal/github/tar.go` to expose `ProbeMarkers` as
  a standalone function (`ProbeAndExtractSubpath` then becomes
  `ProbeMarkers` + decider + `ExtractSubpath`)
- Skill source file location: TBD by the shirabe maintainer;
  exposed as `/shirabe:niwa-migrate-config <workspace-name>`
- `docs/guides/workspace-config-sources.md` updates per PRD R26 /
  AC-G1 through AC-G4

The phases are independently committable. Phase 1 has no live
callers, so it can land before the rest. Phase 6's CLI command
depends on Phase 2's refactor (factoring `ProbeMarkers` out of
`ProbeAndExtractSubpath`); the refactor itself is a no-op for
the production code path.

## Security Considerations

The design is mandated to preserve the seven security defenses in
`internal/github/tar.go` unchanged (PRD R8). Each defense is
re-examined below:

1. **Decompression-bomb cap (`MaxDecompressedBytes = 500 MB`),
   two-level.** Today's defense is two-fold and this design
   preserves both, plus adds a third intermediate cap to bound
   buffer growth:
   - Level A (preserved unchanged): the existing compressed-input
     `io.LimitReader(r, MaxDecompressedBytes+1)` at
     `internal/github/tar.go:63` wraps the gzipped response body
     before `gzip.NewReader` ever sees it. This catches
     pathological compression ratios where a small gzipped
     payload would otherwise decompress past the cap.
   - Level B (NEW, scoped to the probe pass): a second
     `io.LimitReader(gz, MaxDecompressedBytes+1)` wraps the
     decompressed stream during buffer fill. This bounds the
     in-memory buffer regardless of how well-formed the input
     is. If Level A's cap fires first, Level B is never reached.
   - Level C (preserved unchanged): the cumulative-decompressed-
     bytes check at `internal/github/tar.go:150-168` still runs
     during pass 2's `ExtractSubpath` invocation, catching any
     pathological case that slipped past Levels A and B (in
     practice, this is structurally impossible given Levels A
     and B are tight, but the defense remains as belt-and-
     suspenders).
   All three caps share the same `MaxDecompressedBytes = 500 MB`
   budget; if any fires, the function returns the existing
   cap-exceeded diagnostic before any disk write. AC-P4 verifies
   the end-to-end behaviour at the boundary.
2. **Positive type allowlist.** Pass 1 (probe) and pass 2
   (extract) both apply the existing allowlist. Probe rejects
   unsupported entry types the same way extract does.
3. **Wrapper anchoring.** Pass 1 strips the wrapper directory
   (the single top-level entry GitHub tarballs always have) using
   the same logic pass 2 uses. The probe never inspects bytes
   outside the wrapper.
4. **Subpath filter.** Pass 1 ignores subpath (it scans everything
   at root); pass 2 applies the resolved subpath via the existing
   `ExtractSubpath`. No new code path bypasses the filter.
5. **Path containment.** Pass 1 never writes to disk, so path
   containment is structurally satisfied. Pass 2 applies the
   existing containment check verbatim.
6. **Filename validation.** Pass 1 applies the existing validation
   (rejects null bytes, path traversal in filenames). Pass 2 does
   the same as today.
7. **Atomic failure.** Probe-pass errors return before pass 2
   starts. The staging directory is empty when the error fires;
   the caller's existing `_ = safeRemoveAll(staging)` deferred
   cleanup removes it. The on-disk `<workspace>/.niwa/` is
   byte-identical to its pre-init state per R5 / AC-D7.

**Threat: buffered tar enables memory exhaustion before the cap
fires.** Three caps in series bound the buffer: Level A
(compressed `LimitReader` at `tar.go:63`), Level B (decompressed
`LimitReader` during buffer fill), and Level C (cumulative
decompressed-bytes check during pass 2). The buffer is a
`bytes.Buffer` pre-allocated with ~1 MB initial capacity (sized
for the common case of config-bearing sources). Geometric doubling
above that ceiling produces a worst-case peak allocation of
~2 × `MaxDecompressedBytes` during a final doubling — i.e., up to
~1 GB if the input decompresses exactly to the 500 MB ceiling.
Pre-allocation does not change the asymptotic worst case but
significantly reduces the doubling overhead in the common case
(small subpath, small decompressed payload). This is a
documented regression from today's streaming extractor for the
pathological-input case; the Consequences section's "Negative"
list calls it out and `TestExtractSubpath_DecompressionBombDefense`
(duplicated for the new entry point per Phase 2 deliverables)
locks in the cap-firing behaviour at the boundary.

**Threat: probe-pass type-allowlist could diverge from
extract-pass.** A maliciously crafted tar entry whose header type
is permitted by the probe but rejected by extraction (or vice
versa) could create a divergence where the probe's marker
detection acts on entries the extractor will not write — or
worse, fails to detect a marker the extractor would write. The
mitigation is structural: the probe pass calls into the same
type-allowlist check as `ExtractSubpath` (defense 1 of the
original seven). A regression test SHOULD be added in Phase 2
that constructs a tarball with a symlink entry whose name is
`.niwa/workspace.toml` and asserts that the probe does NOT
detect it as a rank-1 marker (since symlinks are rejected by the
type allowlist). This guards against the case where a future
contributor accidentally relaxes the allowlist in one pass but
not the other.

**Threat: probe pass leaks file contents through error messages.**
The probe never reads file contents — only headers. Error messages
contain only entry names (already exposed via the existing
`ExtractSubpath` error path) and the decided subpath. No new
information leaks beyond what extract already exposes.

**Threat: `niwa source inspect` exposes private-repo metadata to
untrusted callers.** The command is a CLI invoked by the user (or
the shirabe skill, which the user invokes). Auth is the user's
existing `GH_TOKEN`; the command makes the same authenticated
request the materializer would make. No new auth path, no token
exposure. The JSON output names the slug the user already typed —
no additional identifiers are revealed. The command does not write
to disk.

**Threat: `niwa source inspect --json` output is consumed by
tools that don't verify `schema_version`.** Documented in the
Consequences section as a future-compat note. The JSON layout is
explicitly versioned; tool authors who pin to a schema version
get stable contracts.

**Threat: deprecation notice emission before snapshot promotion
strands user UX.** Decision 3 explicitly emits notices *after*
promotion succeeds, so a failed apply never prints a notice for
a snapshot that never landed. Verified by AC-N3 (one-time-per-
workspace via `DisclosedNotices` — promotion's success controls
the one and only emit per workspace).

**Outcome:** the design preserves all seven security defenses
unchanged and introduces no new attack surface. The probe pass is
a header-only scan over the same bytes extraction already
processes; the new CLI command is a read-only diagnostic with the
same auth posture as existing commands; the deprecation notice
emission is ordered after promotion to avoid stranded notices.

## Consequences

### Positive

- **Single-repo and brain-repo workspaces work.** `niwa init --from
  dangazineu/foo` against a general-purpose repo with `.niwa/`
  config now Just Works without typing the subpath explicitly.
- **Overlay slug is predictable.** `dangazineu/foo` always derives
  `dangazineu/foo-overlay`, regardless of whether discovery
  resolved a subpath. No silent behaviour change after the probe
  pass lands.
- **Rank-2 deletion is a single-file edit.** The follow-up release
  that hard-removes the deprecated whole-repo shape grep-finds the
  BEGIN/END markers in `internal/config/discover.go`, removes one
  branch and two `MarkerSet` fields, and is done. No downstream
  call sites change.
- **Migration skill reuses niwa's probe.** The shirabe skill
  shells out to `niwa source inspect --json` instead of
  re-implementing the tarball-stream-and-scan in a non-Go runtime.
  One source of truth for the probe logic.
- **`niwa source inspect` is independently useful.** Future
  diagnostic tools, scripts, or even ad-hoc human invocation
  benefit from a read-only probe surface that wasn't there before.
- **No new test infrastructure.** The existing `tarballFakeServer`
  and `localGitServer` fixtures cover the new code paths.

### Negative

- **Peak RAM during cold GitHub fetches grows.** A workspace whose
  source decompresses to N MB holds those bytes in RAM during the
  probe-then-extract pass, where today the streaming extractor
  only holds a working-set window. For config-sized sources
  (≤1 MB compressed, ~5-10 MB decompressed) the difference is
  negligible because the buffer is pre-allocated for that case.
  For the pathological 500 MB decompressed case (capped by the
  bomb defense) peak RAM during `bytes.Buffer` geometric growth
  is bounded by ~2 × `MaxDecompressedBytes` (~1 GB) in the worst
  case — a documented regression from today's streaming extractor.
  Pre-allocating the buffer to a common-case initial capacity
  (~1 MB) reduces doubling overhead for typical workloads; the
  pathological case still allocates up to ~1 GB at peak before
  the cap fires.
- **Rank-2 path is still live.** Anyone reading the codebase will
  see two parallel discovery branches inside `RankDecider` and may
  wonder why both exist. The BEGIN/END comments name the future
  release that resolves this, but the visual carrying cost is
  real until the follow-up ships.
- **JSON contract on `niwa source inspect --json` is new
  surface.** Future changes to the probe result shape require
  bumping `schema_version`. Tool authors who don't pin will see
  breakage; documentation must call this out.
- **Overlay slug change for brain-repo migration is one-way.**
  Migrating from `acme/dot-niwa` to `acme/brain` changes the
  auto-discovered overlay from `acme/dot-niwa-overlay` to
  `acme/brain-overlay`. The migration skill warns the user (per
  PRD R19 path-b), but the maintainer of the new overlay repo
  must arrange for the overlay repo to exist at the new slug
  before consumers migrate, or those consumers silently lose the
  overlay augmentation.
- **Probe-pass adds a header-iteration cost.** Linear in tar
  entry count, free of disk I/O. The
  `Probe-pass scan cost` Known Limitation in the PRD covers this.

### Mitigations

- **Peak RAM**: three-level cap (compressed input, decompressed
  buffer fill, cumulative extract) all share the
  `MaxDecompressedBytes` budget, so no input can decompress past
  the same byte ceiling the bomb defense already imposes. Pre-
  allocating the buffer to a small common-case capacity (~1 MB)
  minimises doubling overhead in the typical workload; the
  pathological worst-case (decompression to exactly the cap)
  allocates up to ~1 GB at peak via `bytes.Buffer` doubling
  before the cap fires. Users who hit the cap today will hit it
  after this design lands at the same byte budget; users with
  config-sized sources will see no observable RAM change.
- **Rank-2 code-base presence**: the BEGIN/END comments and the
  named `rankTwoAccepted` constant make the deletion target
  unmistakable. The follow-up release tracking issue should
  reference these comments verbatim so the deletion ticket is
  self-describing.
- **JSON contract**: documentation under
  `#niwa-source-inspect-schema` (added in Phase 6) explicitly
  states the contract is versioned. Tool authors are expected to
  inspect `schema_version`.
- **Overlay slug change**: the migration skill's path-(b) warning
  is the front-line mitigation. The
  `docs/guides/workspace-config-sources.md` section on
  `#rank-2-deprecation` documents the change so users running the
  in-place migration path (a) — which does *not* change the
  overlay slug — are aware of the alternative path's cost.
- **Probe-pass cost**: bounded by tar entry count, which for
  config-bearing sources is small. Brain repos with very large
  trees pay a one-time cost on the cold fetch; the next apply
  reuses the SHA endpoint or 304 ETag and skips the probe
  entirely (the probe only runs on a fresh fetch).

