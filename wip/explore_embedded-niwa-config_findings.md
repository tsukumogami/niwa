# Findings: embedded-niwa-config

## Decision: Crystallize

The exploration is converging on a focused PRD. One round of research was
sufficient because the area is already heavily specified — the surprise is
that most of the work the user assumed was needed has already been done.

## TL;DR

**The user's request is largely already specified — and most of it is already
implemented.** A prior PRD (`docs/prds/PRD-workspace-config-sources.md`,
status `Done`) covers exactly this scenario: subpath-aware sources, snapshot
materialization, marker-file discovery, and a `git mv ./* .niwa/` migration
story for existing `dot-niwa` repos. The slug grammar
`[host/]owner/repo[:subpath][@ref]` is built, the snapshot fetch path with
subpath filtering is built, and the overlay slug derivation already handles
the subpath case. Three independent research leads converged on the same
gap: **R5 (convention-based discovery — auto-probing for `.niwa/workspace.toml`,
root `workspace.toml`, root `niwa.toml` when no explicit subpath is given) is
specified but not yet implemented in code.**

This reframes the exploration from "design a large UX change" to "close the
remaining gap in an already-shipped PRD, and decide consolidation policy."

## What's Already Built (verified in source)

- **`Source` type with subpath** — `internal/source/source.go:27-33`. Empty
  Subpath documented as "run convention discovery at the repo root."
- **Slug parser** — `internal/source/parse.go` with full tests in
  `internal/source/source_test.go`. Accepts `org/repo:.niwa@ref` and produces
  a typed `Source{Subpath: ".niwa"}`. Strict parse-time error for
  `org/repo:` (empty subpath), embedded whitespace, multiple `:`/`@`.
- **GitHub tarball fetch with subpath extraction** —
  `internal/workspace/snapshotwriter.go:440` calls
  `github.ExtractSubpath(body, src.Subpath, staging)`. When `src.Subpath`
  is non-empty, only entries under that prefix are written to disk; everything
  else is skipped during stream-extraction. Security defenses include
  wrapper-stripping, path-containment, decompression-bomb cap, and atomic
  failure (`internal/github/tar.go`).
- **Non-GitHub fallback** — `internal/workspace/fallback.go:42` uses
  `git clone --depth=1` into a tempdir, then copies `<clone>/<subpath>` into
  staging.
- **Snapshot model (no `.git/`)** — `EnsureConfigSnapshot` materializes a
  pure file tree at `<workspace>/.niwa/` with a provenance marker recording
  the source URL, owner/repo, subpath, ref, resolved commit oid, fetched-at
  timestamp, and fetch mechanism.
- **Atomic snapshot swap** — `materializeAndSwap` stages at
  `<configDir>.next/`, preserves `instance.json` into staging, then swaps.
- **Working-tree → snapshot lazy conversion** — `lazyConvertWorkingTree`
  handles same-URL upgrades from the legacy `.git/`-backed model.
- **Overlay slug derivation that handles subpath** —
  `Source.OverlayDerivedSource()` (`internal/source/source.go:127-141`)
  returns `<source-org>/<basename>-overlay` where `<basename>` is the
  subpath's last segment. So `org/brain:.niwa` derives
  `org/.niwa-overlay`; `org/brain:teams/research` derives
  `org/research-overlay`.

## What's Missing (the actual gap)

**R5: Convention-based subpath discovery.** When the user types
`niwa init --from owner/repo` (no explicit subpath) against a general-purpose
repo that contains `.niwa/workspace.toml`, niwa today materializes the
**entire repo** at `<workspace>/.niwa/`. No probing happens before extraction
— `ExtractSubpath(body, "", staging)` is the "extract everything" path.

Concretely:

- `internal/workspace/snapshotwriter.go:440` calls extract with empty subpath
  when no subpath is given.
- `internal/github/tar.go:117` short-circuits the subpath filter when subpath
  is empty.
- No code path probes the source repo for `.niwa/workspace.toml`,
  `workspace.toml`, or `niwa.toml` before deciding what to extract.

The downstream effects:

- **R6 (ambiguity error when multiple markers present)** — not implemented.
- **R7 (no-marker error)** — not implemented.
- **R8 (require explicit `content_dir` when discovery resolves via rank-3
  `niwa.toml`)** — not implemented.
- **R33 (existing standalone `org/dot-niwa` continues to work via rank-2
  discovery)** — partially OK by accident: today's behavior is "extract whole
  repo," which is what existing `dot-niwa` users get. But the PRD's R33
  promise that discovery rank 2 is the resolution path is unfulfilled.

The post-flight check in `internal/cli/init.go:254-258` does verify
`workspace.toml` exists at `<workspace>/.niwa/workspace.toml`, so a single-repo
attempt with the today's binary fails post-extraction (no `workspace.toml` at
the root of a general-purpose repo). The user just sees a generic
"post-flight verification failed" error, not the friendly discovery diagnostic
the PRD describes.

## Lead-by-Lead Synthesis

### Lead 1 — Config location convention

**Finding**: `.niwa/` is already established as *the* convention in the niwa
codebase. Constants live at `internal/config/discover.go:9-14`
(`ConfigDir = ".niwa"`, `ConfigFile = "workspace.toml"`) and are used for the
local workspace discovery walk (`Discover(startDir)`). The remote-side
analogue is documented in PRD R5 with three ranks: `.niwa/workspace.toml`,
root `workspace.toml`, root `niwa.toml`. Ecosystem precedents (`.github/`,
`.vscode/`, `.editorconfig`) all support the dot-prefix-subdir pattern.

**Open question deferred to PRD**: should the rank-3 `niwa.toml` (root-level
manifest pointing elsewhere) be kept as a third escape hatch, or simplified
out for the consolidation pose? The existing PRD R5/R8 requires it, but in
practice no observed user is on that path. Worth a Decision in the new PRD.

### Lead 2 — Selective retrieval mechanism

**Finding**: The fetch mechanism is **already optimal for v1**: GitHub
tarball API + client-side stream extraction filtered by subpath. Decision
"GitHub-first-class + git-clone fallback (no per-host adapters in v1)" is
explicit in the existing PRD. For non-GitHub, shallow `git clone --depth=1`
followed by subpath copy is in place.

**Future optimization (deferred)**: sparse-checkout (`git clone --depth=1
--sparse --sparse-checkout=.niwa`) would reduce bandwidth for very large
monorepos. Adds git 2.25+ requirement. Not needed for v1 of the gap closure;
list as a follow-up candidate.

### Lead 3 — `--from` flag evolution

**Finding**: `--from` does **NOT** need to change. The slug grammar already
accepts `org/repo:.niwa`, `https://github.com/...`, full SSH URLs, and bare
`org/repo`. The user's "hopefully doesn't change" instinct is correct.

The user-facing UX gap is purely about **discovery making `--from owner/repo`
(no subpath) "do the right thing"** when the source repo has `.niwa/`. With
R5 implemented, `niwa init --from acme/widget` on a single-repo workspace
where `acme/widget` carries `.niwa/workspace.toml` Just Works.

### Lead 4 — Overlay mechanism under embedded config

**Finding**: The overlay mechanism stays the same shape. The user's
"hopefully stays the same" instinct is correct. `OverlayDerivedSource()`
already derives the right slug for the embedded case
(`acme/widget:.niwa` → `acme/.niwa-overlay`).

**Caveat (already documented in the existing PRD)**: migrating a workspace
from `org/dot-niwa` to `org/brain:.niwa` implicitly changes the overlay slug
from `org/dot-niwa-overlay` to `org/.niwa-overlay`. The brain-repo maintainer
must arrange for the overlay repo to exist at the new slug before consumers
migrate, otherwise the overlay clone silently skips and consumers lose the
augmentation without warning. This is a one-time coordination cost per
migration. The new PRD should restate this risk in the migration playbook.

### Lead 5 — Single-repo workspace end-to-end shape

**Finding**: **No topology changes needed.** The single-repo case works
within the existing `niwa init` → `niwa apply` → `niwa session create`
flow:

```
<workspace>/                         # workspace root (created by init)
├── .niwa/                           # snapshot of acme/widget's .niwa/
│   ├── workspace.toml               # canonical config (from snapshot)
│   ├── .niwa-snapshot.toml          # provenance marker
│   └── instance.json                # niwa-local state (preserved across swap)
├── acme/                            # repos discovered + cloned by apply
│   └── widget/                      # the working copy of acme/widget
│       ├── .niwa/                   # the workspace's own .niwa/ on dev branch
│       ├── src/, docs/, …            # general-purpose content
│       └── …
└── .niwa-state/                     # not used (state lives in .niwa/)
```

The same repo appearing as both an XDG-snapshot config source and a
workspace-component working copy is the **established pattern**, confirmed
by issue #137 / PR #138 for the overlay case: snapshot is source of truth;
working copy is just a development surface. The user can edit `.niwa/` in
their working copy, push, and the next apply will refresh the snapshot.

Concretely for the user's example: a developer with one repo `acme/widget`
where `.niwa/workspace.toml` declares one source pointing at
`acme/widget` itself ends up with the repo cloned at
`<workspace>/acme/widget/` and the snapshot at `<workspace>/.niwa/`. Both
have a copy of `.niwa/` content; they serve different roles. Drift in the
working copy is invisible to apply until pushed.

### Lead 6 — Brain-repo composition

**Finding**: Symmetric with Lead 5 — no special handling needed. The brain
repo behaves identically whether it's the only workspace component (single-
repo case) or one of N (brain-repo case). Step 0.6 merge, `discoverAllRepos`,
and `Classify` consume the materialized snapshot; they don't need to know
"this config repo is also a workspace component." PR #138's precedent is
directly applicable.

The only **non-obvious** UX moment: if the user's brain repo lives at
`tsukumogami/vision` and their groups declare `org = "tsukumogami"`, the
brain repo will show up as a workspace component automatically (just like
the overlay does after PR #138). This is the desired behavior — the user
*wants* the brain repo to be a workspace component — but the user should
understand it's not magic; it's the same org-auto-scan logic that picks up
any other repo.

### Lead 7 — Migration and consolidation strategy

**Finding**: The PRD already specifies a consolidation-friendly pose
(R26-R29, R33-R34): existing standalone `dot-niwa` workflows continue to
work without manual migration; the user is never *required* to take any
action after upgrade; force-flag is required only when the URL changes. The
user's "consolidation" preference fits cleanly inside this frame.

**Recommended position for the new PRD**: ship R5 + R6 + R7 + R8 + R33
implementation, and pair it with **opt-in tooling** rather than a hard
migration. Specifically:

- `niwa migrate-source <name>` — guided rewrite of a registry entry that
  inspects the source for marker files, suggests the new slug
  (`org/repo:.niwa` or `org/repo` post-discovery), and writes the change.
- Documented "brain-repo maintainer playbook" for moving config from a
  standalone `dot-niwa` repo into `org/brain:.niwa/`.
- Stay coexistence-by-default in the binary (`org/dot-niwa` keeps working
  via rank-2 discovery, no flag day) — the user's stated openness to
  consolidation is best satisfied by making the new convention so painless
  to adopt that it consolidates organically rather than by deprecation
  threats.

## Surprises

1. **The PRD is marked Done but R5 is not built.** The status field on
   `PRD-workspace-config-sources.md` reads `status: Done`, but
   `materializeFromGitHub` extracts the whole tarball when no subpath is
   given. Either the status is mistakenly Done, or R5 was tacitly descoped
   without amendment. The new PRD must call this out as the closing-the-gap
   motivation.
2. **Issue #74 is the canonical "needs-design" follow-up.** The existing
   PRD's "Future direction (needs-design, issue #74)" section captures the
   "pull only files niwa knows about" model. That issue may also need
   updating or closing once R5 lands.
3. **`Source.OverlayDerivedSource` already covers the embedded case.** No
   thinking needed for overlay UX; the function returns the right shape for
   `acme/widget:.niwa` already. PR #138 closed the last gap by letting the
   overlay repo flow through workspace classification.

## What This Means for the Crystallize Phase

The exploration target is a **PRD that closes the R5 implementation gap**
in `PRD-workspace-config-sources.md`. The new PRD's job is to:

1. Acknowledge the existing PRD as the umbrella specification.
2. Specify the narrow gap (R5+R6+R7+R8+R33 implementation) as the v1.x
   work item.
3. Add Decisions for any new policy: the discovery probe mechanism
   (separate API call vs. peek-inside-tarball), the consolidation tooling
   shape (`niwa migrate-source` or none), and whether to keep rank-3
   `niwa.toml` discovery or simplify it out.
4. Restate the consolidation user story (the user's explicit input) without
   making it a forced migration.
5. Pull in the user stories from the existing PRD that depend on R5
   (Story 1: subpath adoption; Story 2: migrating from standalone dot-niwa)
   so the new PRD reads end-to-end without forcing the reader to flip
   between documents.

## Open Questions (for the PRD's Decisions section)

1. **Discovery probe mechanism**. Two viable options:
   - **(A) Two-call probe**: before fetching the tarball, niwa calls the
     GitHub Contents API for `.niwa/workspace.toml`, then root
     `workspace.toml`, then root `niwa.toml`, and resolves subpath from the
     first hit. Costs: one extra round-trip on the cold path; falls back to
     manual `--subpath` when Contents API is unavailable.
   - **(B) Single-call probe-via-tarball**: fetch the full tarball once,
     stream it to a buffer, scan for marker files at the recognised paths,
     pick the rank-1 winner, then re-extract just that subpath. Costs:
     buffer must be large enough (mitigated by the existing 500 MB cap);
     full tarball bandwidth is paid even for "tiny `.niwa/`" cases (this is
     a known limitation in the existing PRD anyway).

2. **Migration tooling**. Three options:
   - **(A)** No tooling — coexistence via rank-2 discovery; users update
     their registry slugs by hand when they're ready.
   - **(B)** `niwa migrate-source <name>` command that rewrites the
     registry entry and triggers the next-apply lazy conversion.
   - **(C)** Both — recommended in the new PRD because (B) is a thin
     wrapper around (A) and gives users an obvious path.

3. **`niwa.toml` rank-3 discovery — keep or simplify out?** The existing
   PRD's R5+R8 specifies it; the additional `content_dir` requirement adds
   complexity. Worth a Decision in the new PRD: keep all three ranks (full
   fidelity) or drop rank-3 (`niwa.toml` at repo root) for simplicity. The
   "single-repo workspace" goal doesn't require rank-3; consolidation might
   benefit from dropping it.

4. **Naming of overlay slug in single-repo / brain-repo cases**. Already
   resolved by `OverlayDerivedSource()` but worth restating in the new
   PRD's Acceptance Criteria so the contract is end-to-end inspectable.

## Convergence Verdict (--auto mode)

In `--auto` mode following the research-first protocol, the recommendation
is **Ready to decide**:

- Coverage across all 7 leads is high; no significant gaps remain.
- The artifact target is clear: a PRD that closes the gap in an existing
  Done-but-incomplete PRD.
- The user's pre-stated preference (produce a PRD) aligns.

Proceeding to Phase 4 (Crystallize).
