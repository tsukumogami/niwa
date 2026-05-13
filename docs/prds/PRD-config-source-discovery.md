---
status: Draft
problem: |
  niwa today cannot be adopted in a single-repo workspace. When a developer
  runs `niwa init --from owner/repo` against a general-purpose repo that
  carries its workspace config at `.niwa/`, niwa materializes the entire
  repo at `<workspace>/.niwa/` because the subpath-discovery requirement
  (R5 in `PRD-workspace-config-sources.md`) was specified but never
  implemented. The same gap blocks the natural "brain repo" pattern where
  a workspace's strategic content and niwa config coexist in one repo.
goals: |
  Close the R5 implementation gap so that `niwa init --from owner/repo`
  against a general-purpose repo "just works" by probing for marker files
  in a fixed precedence order, resolves the three policy questions the
  upstream PRD left open (probe mechanism, migration tooling, rank-3
  `niwa.toml` keep-or-drop), and ships a `niwa migrate-source` command
  that makes the brain-repo migration painless without forcing it.
  Existing standalone `dot-niwa` workflows keep applying without any user
  action.
upstream: docs/prds/PRD-workspace-config-sources.md
---

# PRD: Config Source Discovery

## Status

Draft

## Problem Statement

The umbrella PRD for workspace config sources
(`docs/prds/PRD-workspace-config-sources.md`, status `Done`) specifies a
v1 model where `niwa init --from owner/repo` against a repo whose
workspace config lives at `<repo>/.niwa/` "just works" — niwa probes the
source for marker files in a fixed precedence order, resolves the
subpath automatically, and materializes only that subpath. Most of that
PRD has shipped: the slug grammar `[host/]owner/repo[:subpath][@ref]`
parses, the subpath-aware fetch path is built, snapshots replace the old
working-tree posture, and the overlay slug derivation already handles
the subpath case.

The discovery step itself — requirement R5 — is the gap. Verified in
the code today:

- `internal/workspace/snapshotwriter.go:440` calls
  `github.ExtractSubpath(body, src.Subpath, staging)` directly. When
  `src.Subpath == ""`, this is the "extract everything" path.
- `internal/github/tar.go:117` short-circuits the subpath filter when
  subpath is empty.
- No code path probes the source for `.niwa/workspace.toml`,
  `workspace.toml`, or `niwa.toml` before deciding what to extract.

The user-visible consequence: a developer with a single general-purpose
repo (the entire workspace is one repo) cannot adopt niwa without
either (a) standing up a second `dot-niwa` repo just for config — the
exact friction niwa is supposed to remove — or (b) typing
`--from owner/repo:.niwa` verbatim each time and remembering that
syntax. The "brain repo" pattern — a workspace whose strategic content
and niwa config live in one repo — is blocked for the same reason.

Three policy questions the upstream PRD left implicit are resolved by
this PRD (see Decisions and Trade-offs below):

1. **Probe mechanism**: single fetch + in-stream scan, not a separate
   API call.
2. **Migration tooling**: a `niwa migrate-source` command ships
   together with discovery so consolidation has a painless on-ramp.
3. **Rank-3 `niwa.toml` discovery**: removed in v1.x; only rank-1 and
   rank-2 markers remain.

## Goals

- **Make single-repo workspaces viable.** `niwa init --from owner/repo`
  against a general-purpose repo that contains `.niwa/workspace.toml`
  resolves automatically — no extra flags, no extra repo to stand up.
- **Make brain-repo composition first-class.** A workspace whose niwa
  config sits at a subdirectory of an existing brain repo is the
  default, well-trodden path; the user never has to think about
  subpath syntax unless they want to override discovery.
- **Preserve standalone `dot-niwa` workflows.** Existing
  `--from org/dot-niwa` users keep applying after upgrade with no
  registry edits, no `--force` flag, no error messages they have to
  decode. They resolve via rank-2 discovery (root `workspace.toml`).
- **Ship gentle migration tooling.** `niwa migrate-source <name>`
  rewrites a registry entry to point at a new source slug after a
  maintainer has moved config from a standalone repo into a brain
  repo's `.niwa/`. The binary itself never refuses to load the legacy
  shape; consolidation happens organically because the new path is
  painless, not because the old path was deprecated.
- **Make discovery errors actionable.** When a source contains
  multiple markers at the source root, or none of them, the error
  message names every accepted path and the explicit-subpath escape
  hatch.

## User Stories

### Story 1: Single-repo workspace adoption

A developer has one repo, `acme/widget`, that contains both their
project code and `.niwa/workspace.toml` declaring the workspace
config. They run `niwa init --from acme/widget my-workspace`. niwa
probes the source, finds `.niwa/workspace.toml`, resolves the subpath
to `.niwa/`, fetches only that subpath into the workspace snapshot
at `<my-workspace>/.niwa/`, and registers the workspace. A subsequent
`niwa apply` clones `acme/widget` as a workspace component. The
developer's working copy of `acme/widget` ends up under
`<my-workspace>/acme/widget/`; the snapshot of the config remains the
source of truth.

### Story 2: Brain-repo composition

A team uses `acme/vision` as their brain repo: it holds the project's
strategic documents, planning notes, Claude config, and now also the
workspace's niwa config at `vision/.niwa/`. The workspace declares
three other components (`acme/web`, `acme/api`, `acme/infra`). A
developer runs `niwa init --from acme/vision my-workspace`. Discovery
resolves the subpath to `.niwa/`, the snapshot materializes only the
config files, and `niwa apply` clones all four workspace repos —
including `acme/vision` itself — under the instance root. The brain
repo flows through `discoverAllRepos` and `Classify` like any other
workspace repo (per the precedent established in PR #138). The user
never has to write a special config entry to say "the brain repo is
both my config source and a workspace component."

### Story 3: Maintainer publishes config from brain repo

A maintainer of `acme/vision` decides to host the workspace config
inside the brain repo. They `git mv` the standalone-`dot-niwa`
contents into `acme/vision/.niwa/`, commit, and push. They post a
one-line announcement: "the workspace config now lives in the brain
repo — run `niwa migrate-source <your-workspace-name>` to switch."
Each consumer's switch is independent; the standalone `dot-niwa` repo
keeps working for anyone who hasn't migrated yet, so there's no
synchronized cutover.

### Story 4: Consumer runs `niwa migrate-source`

A developer running the maintainer's announcement from Story 3 types
`niwa migrate-source my-workspace --to acme/vision`. The command
updates the registry entry's source slug from `acme/dot-niwa` to
`acme/vision`, prints a one-line confirmation, and prompts the user
to run `niwa apply --force` (the existing `--force` gate from
upstream R26 still applies because the registered source URL
changed). The developer runs `niwa apply --force`. niwa detects the
URL change, refuses without `--force` had it been absent, and with
`--force` atomically replaces the snapshot from the new source's
`.niwa/` per the upstream PRD's R27. From this point forward, the
developer's workspace is on the new pattern.

### Story 5: Existing standalone `dot-niwa` user upgrades

A developer with an established workspace pointing at
`org/dot-niwa` (whose entire content is the workspace config)
upgrades to a niwa binary that ships this PRD. They take no action.
The next `niwa apply` succeeds: discovery probes the source, finds
root `workspace.toml` (rank-2 marker), resolves the subpath to `""`
(whole-repo), and the materialization path matches what it did
before. No `--force` flag, no destroy/re-init ritual.

### Story 6: Discovery ambiguity diagnostic

A maintainer publishes a brain repo with both
`.niwa/workspace.toml` and a top-level `workspace.toml` (perhaps
left over from a partial migration). A consumer runs
`niwa init --from acme/vision`. niwa probes the source, finds both
markers, and refuses with an error that names both files and points
the maintainer at the fix:

```
error: ambiguous niwa config in acme/vision
  found:   .niwa/workspace.toml
  found:   workspace.toml
  Remove one of the markers, or override discovery with
  --from acme/vision:.niwa (explicit subpath).
```

The consumer reports the error to the maintainer; the maintainer
removes the stale file; the consumer re-runs `niwa init` and
succeeds.

### Story 7: Discovery no-marker diagnostic

A consumer types `niwa init --from acme/random-repo` against a
public repo that doesn't carry niwa config at all. niwa probes,
finds neither `.niwa/workspace.toml` nor a root `workspace.toml`,
and refuses with a diagnostic that names both accepted paths and
the explicit-subpath escape hatch:

```
error: no niwa config found in acme/random-repo
  probed:  .niwa/workspace.toml  (rank 1)
  probed:  workspace.toml        (rank 2, repo root)
  If the config lives elsewhere, point at it explicitly:
    niwa init --from acme/random-repo:path/to/config
```

The user knows immediately whether the repo is meant to be a niwa
source and, if so, where else to look.

### Story 8: Rank-3 user migrates after upgrade

A team had been using a brain repo `acme/legacy` whose niwa config
sat at the repo root as `niwa.toml` (the upstream PRD's rank-3
shape, which this PRD removes). After upgrading to a binary that
ships this PRD, the first `niwa apply` against any workspace
sourced from `acme/legacy` exits with the Story-7 "no niwa config
found" diagnostic — the diagnostic deliberately does not mention
`niwa.toml` because rank-3 is no longer an accepted marker.

The team has two recovery paths, both documented in the
`docs/guides/workspace-config-sources.md` migration section:
(a) `git mv niwa.toml .niwa/workspace.toml` in `acme/legacy`,
commit, push, and re-run `niwa apply`; or (b) keep the file at
root, rename it to `workspace.toml`, commit, push. Either path
resolves discovery and the next `niwa apply` succeeds. No
upgrade-time pre-flight or proactive warning fires — the error
surfaces on first apply only.

## Requirements

### Functional requirements

**Convention-based subpath discovery**

- **R1.** When the user passes `--from owner/repo` (no explicit
  subpath; i.e., the parsed `Source.Subpath` is empty), `niwa init`
  MUST probe **only the source-root level** for marker files in this
  fixed precedence order: `.niwa/workspace.toml` (rank 1), root
  `workspace.toml` (rank 2). The probe MUST ignore nested occurrences
  of these filenames at deeper paths (e.g., `subdir/.niwa/workspace.toml`
  or `subdir/workspace.toml` are not discovery candidates). The first
  match resolves the subpath:
  - rank 1 match → `Source.Subpath = ".niwa"`
  - rank 2 match → `Source.Subpath = ""` (whole-repo case; preserves
    the existing standalone `dot-niwa` shape)
- **R2.** An explicit subpath in the slug (e.g.,
  `--from owner/repo:custom/path`) MUST bypass discovery entirely.
  niwa MUST NOT probe for rank-1 / rank-2 markers in this case, and
  ambiguity errors (R3) MUST NOT fire. If the explicit subpath does
  not contain a `workspace.toml`, niwa MUST fail without falling
  back to discovery (preserves upstream PRD R9).
- **R3.** When more than one marker is present at the source root
  (both `.niwa/workspace.toml` AND root `workspace.toml` exist),
  niwa MUST fail with an "ambiguous niwa config" diagnostic naming
  the **markers found** and the explicit-subpath escape hatch. niwa
  MUST NOT pick a winner silently.
- **R4.** When discovery finds no marker at the source root and no
  explicit subpath was given, niwa MUST fail with a "no niwa config
  found" diagnostic listing **every accepted marker path that was
  probed** (`.niwa/workspace.toml`, root `workspace.toml`) and the
  explicit-subpath escape hatch.
- **R5.** Discovery failure on any path (multiple markers, no
  marker, network unreachable, truncated tarball) MUST leave the
  on-disk `<workspace>/.niwa/` byte-identical to its pre-init state,
  **or absent entirely if `<workspace>/` did not exist before the
  failed init**. No partial snapshot is materialized. No files
  written by the init are present after a failure.
- **R6.** Empty `.niwa/` directory at the source root (the directory
  exists but contains no `workspace.toml`) MUST NOT count as a
  rank-1 match. Discovery MUST continue to rank 2. When the source
  has an empty `.niwa/` AND a root `workspace.toml`, rank 2 wins;
  no ambiguity error fires.

**Discovery probe mechanism**

- **R7.** Discovery MUST use a single fetch of the source — the
  GitHub tarball (`github.com` host) or a single shallow clone
  (`git clone --depth=1`, all other hosts) — the same fetch the
  snapshot materialization uses. niwa MUST scan the in-memory
  tarball stream (or the resulting shallow-clone working tree's
  top level) for marker files at the two accepted paths, decide the
  rank-1-wins-over-rank-2 precedence, and extract only the resolved
  subpath to the staging area. niwa MUST NOT make a separate "probe"
  API call (e.g., GitHub Contents API) before the fetch.
- **R8.** The marker scan on the GitHub path MUST happen during
  tarball streaming without buffering the full tarball to disk. The
  decompression-bomb cap and security defenses in
  `internal/github/tar.go` MUST apply unchanged to the probe pass;
  a tarball that exceeds the cap during the probe scan MUST fail
  with the existing cap-exceeded diagnostic and leave no snapshot
  materialized.
- **R9.** The marker scan on the non-GitHub path MUST inspect the
  top level of the shallow-clone working tree for the two accepted
  filenames (`workspace.toml` at root; `workspace.toml` inside
  `.niwa/` subdir). The clone is reused as the staging source if
  discovery resolves successfully; the temp clone is removed after
  the snapshot is promoted (per upstream R15).

**Migration tooling: `niwa migrate-source`**

- **R10.** niwa MUST ship a `niwa migrate-source <name> [--to <slug>]
  [--yes]` command. `<name>` is the workspace instance name as
  registered in `~/.config/niwa/config.toml` (matching the value
  shown by `niwa status`). The command's job is to rewrite that
  registry entry's `source_url` field; it MUST NOT run apply, MUST
  NOT delete the on-disk `<workspace>/.niwa/`, MUST NOT trigger any
  fetch or write to the snapshot's staging area, and MUST NOT touch
  the source repo (no remote writes; remote reads only when probing
  for an inferred slug).
- **R11.** When `--to <slug>` is provided, niwa MUST validate the
  destination slug grammatically against the upstream PRD's R1-R3
  parser rules and reject malformed input at parse time with the
  upstream R3 diagnostic. niwa MUST NOT validate that the
  destination resolves to a valid workspace config at this stage —
  discovery resolution is deferred to the next `niwa apply`.
- **R12.** When `--to` is omitted, niwa MUST probe the **current**
  source (from the registry entry being migrated) for rank-1 and
  rank-2 markers using the same single-fetch mechanism as R7/R8/R9.
  The inferred new slug is `<owner/repo>:.niwa` when rank 1 matches
  and `<owner/repo>` (no subpath) when rank 2 matches.
  - If discovery against the current source produces an R3
    ambiguity, `niwa migrate-source` without `--to` MUST exit
    non-zero with the same R3 ambiguity diagnostic.
  - If discovery produces an R4 no-marker outcome,
    `niwa migrate-source` without `--to` MUST exit non-zero with
    the same R4 diagnostic.
  - If the source is network-unreachable, `niwa migrate-source`
    without `--to` MUST exit non-zero with a "could not probe
    source" message naming the source URL. niwa MUST NOT fall
    back to a cached snapshot for this probe.
- **R13.** When `--to` is omitted and discovery resolves
  unambiguously, behaviour depends on TTY and `--yes`:
  - **Interactive (stdin is a TTY) and `--yes` absent**: niwa MUST
    print a confirmation prompt of the form
    `Migrate <name> from <old-slug> to <new-slug>? [y/N]: ` to
    stdout and accept `y` or `yes` (case-insensitive) on stdin as
    confirmation. Any other input (including empty / `n` / EOF)
    MUST abort with exit code 0 and the message "aborted" to
    stderr; the registry MUST NOT be modified.
  - **Non-interactive (stdin is not a TTY) and `--yes` absent**:
    niwa MUST print the inferred slug to stdout and exit non-zero
    with a message instructing the user to re-run with `--yes`
    or `--to <slug>`. The registry MUST NOT be modified.
  - **`--yes` present (any TTY state)**: niwa MUST apply the
    inferred rewrite immediately and exit zero. `--yes` only
    auto-confirms the unambiguous case; ambiguous (R3) and
    no-marker (R4) outcomes MUST still fail per R12 regardless
    of `--yes`.
- **R14.** When `--to <slug>` exactly matches the workspace's
  current `source_url`, `niwa migrate-source` MUST exit zero
  with a single-line "already on this slug" message to stderr and
  MUST NOT modify the registry file.
- **R15.** On the next `niwa apply` after a registry source-URL
  change made by `niwa migrate-source` (or by hand-edit per the
  upstream PRD R29), apply-time behaviour MUST be identical to the
  upstream PRD's R26/R27 (refuse without `--force`; atomically
  replace the snapshot with `--force`). This PRD adds no new
  apply-time gates.
- **R16.** After `niwa migrate-source` rewrites the registry, niwa
  MUST print to stderr a one-line pointer containing the literal
  substring `niwa apply --force`, instructing the user how to
  realise the change.
- **R17.** `niwa migrate-source` MUST expose stable exit codes
  documented in the `--help` output: 0 on success (including the
  no-op R14 case), 1 on registry not found / workspace name not
  registered, 2 on probe failure when `--to` is omitted (covering
  R12's three failure modes), 3 on slug-parse failure when `--to`
  is provided (R11), and 130 on user abort (R13 interactive
  "no" answer). The exact mapping of probe-failure subcategories
  to exit codes is left to the design phase but MUST keep code 2
  as the family for "probe failed."

**Backwards compatibility**

- **R18.** Existing registry entries with
  `source_url = "org/dot-niwa"` (no subpath, written by older
  binaries) MUST continue to resolve via rank-2 discovery (root
  `workspace.toml`) without user action. No upgrade-time prompt
  MUST fire for these users (preserves upstream PRD R33).
- **R19.** No existing user MUST be required to take any action
  after upgrading to a niwa binary that ships this PRD (preserves
  upstream PRD R34). The first `niwa apply` after upgrade triggers
  discovery transparently and succeeds for any source that
  matches a rank-1 or rank-2 marker.

**Rank-3 (`niwa.toml`) discovery removal**

- **R20.** Rank-3 discovery (root `niwa.toml` with explicit
  `content_dir`) specified by the upstream PRD's R5+R8 MUST be
  removed in this PRD's scope. Brain repos relying on a root
  `niwa.toml` MUST migrate to either `.niwa/workspace.toml`
  (rank 1) or root `workspace.toml` (rank 2). Existing registries
  whose source resolved only via the removed rank-3 marker MUST
  surface the R4 "no niwa config found" diagnostic on the next
  `niwa apply`; no upgrade-time prompt or pre-flight is added.
  Migration guidance for this case MUST appear in
  `docs/guides/workspace-config-sources.md`.

**Diagnostic clarity**

- **R21.** Every discovery error message (R3 ambiguity, R4 no
  marker, R5 partial-snapshot avoidance) MUST contain three pieces
  of information, each as a separately-grep-able substring:
  - the source slug (the user's `--from` value);
  - the relevant marker path(s) — for R3 ambiguity errors, every
    marker found at the source root; for R4 no-marker errors,
    every accepted marker path that was probed;
  - the explicit-subpath escape hatch syntax (a string containing
    the literal substring `--from` and the literal substring `:`).
  The exact rendering (line breaks, prefixes, ordering) is left to
  the design phase; the visible reference for the target format is
  the diagnostic block in Story 6 (R3) and Story 7 (R4).

**Upstream PRD reconciliation**

- **R22.** When this PRD is Accepted, the upstream PRD
  `docs/prds/PRD-workspace-config-sources.md` MUST be amended to
  reflect that R5+R6+R7+R8 and R33 from that PRD are tracked by
  this PRD as outstanding implementation work. Either: (a) the
  upstream PRD's status changes to "In Progress" with this PRD
  cited as the tracking artifact in a new amendment block, or
  (b) the upstream PRD adds an amendment block (dated, like the
  existing 2026-04-23 entry) acknowledging the gap and naming
  this PRD as the closing work. The choice between (a) and (b)
  is left to the maintainer accepting this PRD, but one of the
  two MUST happen at acceptance time.

**Documentation**

- **R23.** `docs/guides/workspace-config-sources.md` MUST be
  updated to include:
  - A section with the exact heading anchor `#single-repo-workspace`
    walking through Story 1 end-to-end, including the on-disk
    layout sketch and the `niwa init --from owner/repo` command
    without explicit subpath.
  - A section with the exact heading anchor `#brain-repo` walking
    through Story 2 end-to-end, including the `discoverAllRepos`
    / `Classify` behaviour for the brain repo as a workspace
    component (cross-referencing the upstream PRD's overlay
    precedent and PR #138).
  - A section with the exact heading anchor `#niwa-migrate-source`
    documenting the command synopsis, `--to` and `--yes` flag
    semantics, exit codes (per R17), and a Story-3 + Story-4
    walk-through of a brain-repo maintainer's publishing flow.
  - A section with the exact heading anchor `#rank-3-removal`
    explaining the removed root `niwa.toml` path. The section
    body MUST contain the literal substrings `niwa.toml`,
    `rank 3` (or `rank-3`), and `.niwa/workspace.toml` (the
    primary migration target).

## Acceptance Criteria

Each AC is binary pass/fail. ACs that depend on a fixture name it
explicitly. The upstream PRD's Test Strategy section defines the
`tarballFakeServer` and the legacy-working-tree fixture.

### AC: Convention-based subpath discovery (verifies R1-R6)

- [ ] **AC-D1**. Given a `tarballFakeServer` source containing only
  `.niwa/workspace.toml`, `niwa init <name> --from owner/repo`
  resolves `source_subpath = ".niwa"` in the registry. The on-disk
  `<workspace>/.niwa/` after init contains the files from the
  source's `.niwa/` directory and no files from outside it.
- [ ] **AC-D2**. Given a `tarballFakeServer` source containing only
  a root `workspace.toml` (the standalone-`dot-niwa` shape),
  `niwa init <name> --from owner/repo` resolves
  `source_subpath = ""` in the registry. The on-disk
  `<workspace>/.niwa/` contains the source's whole tree (minus
  excluded entries per upstream PRD R10).
- [ ] **AC-D3a**. Given an explicit slug
  `--from owner/repo:custom/path` where the source has
  `.niwa/workspace.toml` AND `workspace.toml` at root AND
  `custom/path/workspace.toml`, the resolved subpath is
  `custom/path` (the explicit slug bypasses discovery; no
  ambiguity error fires).
- [ ] **AC-D3b**. Given an explicit slug
  `--from owner/repo:custom/path` where the source has neither
  `.niwa/workspace.toml` nor a root `workspace.toml`, but DOES have
  `custom/path/workspace.toml`, the init succeeds. No discovery
  diagnostic is printed.
- [ ] **AC-D4**. Given an explicit slug
  `--from owner/repo:custom/path` where `custom/path/` exists but
  contains no `workspace.toml`, `niwa init` exits non-zero with
  stderr containing the slug `owner/repo:custom/path` and the
  literal `workspace.toml`. No fallback to discovery. The on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-init state, or
  absent if `<workspace>/` did not exist before the failed init.
- [ ] **AC-D5**. Given a `tarballFakeServer` source containing both
  `.niwa/workspace.toml` AND a root `workspace.toml`,
  `niwa init <name> --from owner/repo` exits non-zero with stderr
  containing the literal `ambiguous niwa config`, both marker paths
  (`.niwa/workspace.toml` and `workspace.toml`), and a substring
  matching the literal `--from` together with the literal `:` (the
  explicit-subpath hint). After the failure, the on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-init state, or
  absent if `<workspace>/` did not exist before the failed init.
  No files written by the init are present.
- [ ] **AC-D6**. Given a `tarballFakeServer` source containing
  neither `.niwa/workspace.toml` nor a root `workspace.toml`,
  `niwa init <name> --from owner/repo` exits non-zero with stderr
  containing the literal `no niwa config found`, both probed paths
  (`.niwa/workspace.toml`, `workspace.toml`), and the
  explicit-subpath hint (substrings `--from` and `:`). The on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-init state, or
  absent if `<workspace>/` did not exist before the failed init.
- [ ] **AC-D7**. Given a `tarballFakeServer` configured to return
  500 on the first tarball request, `niwa init` exits non-zero and
  the on-disk `<workspace>/.niwa/` (if it existed before) is
  byte-identical to its pre-init state.
- [ ] **AC-D8**. Given a `tarballFakeServer` source containing a
  `.niwa/` directory at root WITHOUT a `workspace.toml` inside it
  (e.g., the directory has only a `.gitkeep` file) AND a root
  `workspace.toml`, `niwa init <name> --from owner/repo` resolves
  to rank 2: `source_subpath = ""`, the whole-repo materializes,
  no ambiguity error fires. This verifies that empty `.niwa/` is
  not treated as a rank-1 match (R6).
- [ ] **AC-D9**. Given an explicit slug
  `--from owner/repo:custom/path` where the source contains a
  root `workspace.toml` AND a `.niwa/workspace.toml` AND
  `custom/path/workspace.toml`, the resolved subpath is
  `custom/path`. Same as AC-D3a, asserted separately to verify
  R2's "MUST bypass discovery entirely" wording.

### AC: Single-call probe mechanism (verifies R7-R9, R16)

- [ ] **AC-P1**. Given a `tarballFakeServer` source with
  `.niwa/workspace.toml`, the server records exactly **one**
  tarball request and **zero** Contents API (`/contents/`)
  requests during init. (Verifies R7 — no separate probe call —
  and serves as the testable AC for the implicit "no observable
  added latency" promise.)
- [ ] **AC-P2**. Given a `tarballFakeServer` source whose tarball
  is 200 MB compressed but whose `.niwa/` directory is 50 KB, the
  init succeeds and the resulting `<workspace>/.niwa/` directory
  contains the 50 KB. After init, `find $TMPDIR -path '*/niwa-*'
  -type f` returns no paths (no leaked scratch files outside the
  niwa-managed staging area and final snapshot).
- [ ] **AC-P3**. Given a `tarballFakeServer` configured with the
  `truncate-after:N` fixture mode that closes the connection
  mid-stream, `niwa init` exits non-zero and no snapshot is
  materialized (R5 / upstream R12).
- [ ] **AC-P4**. Given a `tarballFakeServer` source whose
  decompressed tarball size exceeds the 500 MB cap from
  `internal/github/tar.go`, `niwa init` exits non-zero with the
  existing cap-exceeded diagnostic. No snapshot is materialized.
  This verifies that R8's "security defenses MUST apply unchanged
  to the probe pass" is enforced (the cap fires during streaming,
  before the probe finishes scanning markers).
- [ ] **AC-P5** (non-GitHub). Given a `localGitServer` `file://`
  source with `.niwa/workspace.toml` at the repo root, a single
  `git clone --depth=1` is performed; after init, the temp clone
  directory does not exist (per upstream R15), the resulting
  `<workspace>/.niwa/` contains the files from the source's
  `.niwa/` directory and no files from outside it.

### AC: Migration tooling — `niwa migrate-source` (verifies R10-R17)

These ACs use two `tarballFakeServer` fixtures unless noted:
fixture A serves `acme/dot-niwa` (the legacy standalone shape:
root `workspace.toml`), fixture B serves `acme/vision` (with
`.niwa/workspace.toml` at root).

- [ ] **AC-M1**. Given a workspace registered with
  `source_url = "acme/dot-niwa"`, running
  `niwa migrate-source my-workspace --to acme/vision` succeeds
  (exit 0). After the command, the registry file contains
  `source_url = "acme/vision"` for `my-workspace`. The on-disk
  `<my-workspace>/.niwa/` is byte-identical to its pre-command
  state.
- [ ] **AC-M2**. After AC-M1's `niwa migrate-source` returns,
  stderr contains the literal substring `niwa apply --force`,
  pointing the user at the next step (R16).
- [ ] **AC-M3**. With the AC-M1 preconditions plus fixture B
  serving `acme/vision` with `.niwa/workspace.toml`, running
  `niwa apply my-workspace --force` after `niwa migrate-source`
  succeeds. The post-apply `<my-workspace>/.niwa/` is a fresh
  snapshot from `acme/vision`'s `.niwa/` subdirectory; the
  provenance marker records `source_url = "acme/vision"` and
  `subpath = ".niwa"`. `niwa status` for `my-workspace` shows the
  new slug and subpath.
- [ ] **AC-M4**. Given the AC-M1 preconditions, running
  `niwa migrate-source my-workspace --to acme/dot-niwa` (the slug
  matching the workspace's current source) exits 0 with stderr
  containing the literal substring `already on this slug`. The
  registry file's `source_url` for `my-workspace` is byte-identical
  to its pre-command state (R14).
- [ ] **AC-M5**. `niwa migrate-source nonexistent-workspace --to
  acme/vision` exits non-zero with exit code 1 and stderr
  containing the literal `nonexistent-workspace` and the literal
  `not found`.
- [ ] **AC-M6** (R12 inferred slug, unambiguous). Given a workspace
  registered with `source_url = "acme/dot-niwa"` and fixture A
  serving `acme/dot-niwa` with only a root `workspace.toml`,
  running `niwa migrate-source my-workspace --yes` (no `--to`)
  succeeds. The probe finds rank-2; the inferred new slug is
  `acme/dot-niwa` (unchanged); registry remains byte-identical
  (the no-op behaviour from R14 applies). Stderr contains
  `already on this slug`.
- [ ] **AC-M7** (R12 inferred slug, rank-1 found). Given a
  workspace registered with `source_url = "acme/vision"` (no
  explicit subpath) and fixture B serving `acme/vision` with
  `.niwa/workspace.toml`, running
  `niwa migrate-source my-workspace --yes` succeeds. The inferred
  slug is `acme/vision:.niwa` (rank-1 match); the registry's
  `source_url` is rewritten to `acme/vision:.niwa`; stderr
  contains the literal `niwa apply --force` pointer.
- [ ] **AC-M8** (R12 ambiguous current source). Given a workspace
  registered with `source_url = "acme/messy"` and a fixture
  serving `acme/messy` with BOTH `.niwa/workspace.toml` AND root
  `workspace.toml`, running `niwa migrate-source my-workspace`
  (no `--to`, with or without `--yes`) exits non-zero with the
  R3 ambiguity diagnostic content (per AC-D5). The registry is
  byte-identical.
- [ ] **AC-M9** (R12 no-marker current source). Given a workspace
  registered with `source_url = "acme/empty"` and a fixture
  serving `acme/empty` with neither marker, running
  `niwa migrate-source my-workspace --yes` exits non-zero with
  the R4 no-marker diagnostic content (per AC-D6). The registry
  is byte-identical.
- [ ] **AC-M10** (R12 network-unreachable probe). Given the
  fixture configured to refuse connections (network simulating
  unreachable), running
  `niwa migrate-source my-workspace --yes` exits non-zero with
  exit code 2 and stderr containing the literal `could not probe
  source` plus the source slug. The registry is byte-identical.
  niwa MUST NOT fall back to a cached snapshot for this probe.
- [ ] **AC-M11** (R13 non-interactive without `--yes`). Given the
  AC-M7 preconditions (rank-1 match available) plus stdin
  attached to a non-TTY (`/dev/null` or pipe), running
  `niwa migrate-source my-workspace` (no `--to`, no `--yes`)
  exits non-zero with stderr containing the inferred slug
  `acme/vision:.niwa` and a literal `--yes` instruction. The
  registry is byte-identical.
- [ ] **AC-M12** (R13 interactive confirm). Given AC-M7
  preconditions plus stdin attached to a pseudo-TTY scripted to
  send `y\n`, running `niwa migrate-source my-workspace` succeeds
  (exit 0), the registry is rewritten to `acme/vision:.niwa`,
  stdout contains the literal `[y/N]` (proving the prompt fired).
- [ ] **AC-M13** (R13 interactive abort). Given the same setup as
  AC-M12 but the pseudo-TTY sends `\n` (empty line or `n`),
  niwa exits 130 (per R17), the registry is byte-identical to its
  pre-command state, and stderr contains the literal `aborted`.
- [ ] **AC-M14** (R11 slug parse). `niwa migrate-source
  my-workspace --to "owner/repo:"` (empty subpath) exits 3 with
  the upstream R3a parse-time diagnostic. The registry is
  byte-identical.
- [ ] **AC-M15** (R10 no apply, no source-repo write). After any
  `niwa migrate-source` invocation, the fixture's request counter
  shows zero requests OR (for `--to`-provided invocations where
  no probe happens) zero requests; the staging directory
  `<workspace>/.niwa.next/` does not exist; the on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-command
  state. This asserts that R10's "MUST NOT trigger an apply"
  and "MUST NOT touch the source repo" contracts hold across
  all `niwa migrate-source` invocations.
- [ ] **AC-M16** (R15 negative apply gate). Given the AC-M3
  preconditions but the user runs bare `niwa apply my-workspace`
  (no `--force`) between `migrate-source` and the `--force`
  retry, `niwa apply` exits non-zero with the upstream R26
  source-URL-changed diagnostic. After this aborted apply, the
  registry source URL is still `acme/vision` (the migrate-source
  change), the on-disk `<my-workspace>/.niwa/` is byte-identical
  to its pre-bare-apply state.
- [ ] **AC-M17** (R10 migrate-source against legacy working
  tree). Given a workspace registered with
  `source_url = "acme/dot-niwa"` whose on-disk
  `<my-workspace>/.niwa/` has a `.git/` directory (the legacy
  working-tree shape from upstream R28), running
  `niwa migrate-source my-workspace --to acme/vision` succeeds
  and the on-disk `<my-workspace>/.niwa/` (including `.git/`) is
  byte-identical to its pre-command state. The subsequent
  `niwa apply --force` per AC-M3 performs both the URL change
  swap AND the upstream-R28 lazy conversion in one pass; after
  it returns, `<my-workspace>/.niwa/.git/` does not exist and
  the provenance marker shows the new source.

### AC: Backwards compatibility (verifies R18-R19)

- [ ] **AC-B1a** (legacy working tree, no URL change). Given a
  registry entry from an older binary with
  `source_url = "org/dot-niwa"` AND an on-disk
  `<workspace>/.niwa/.git/` directory (the legacy working-tree
  shape), the first `niwa apply` on a binary that ships this
  PRD succeeds with no user prompts. After apply,
  `<workspace>/.niwa/.git/` does not exist, the provenance marker
  file `<workspace>/.niwa/.niwa-snapshot.toml` (or the marker
  filename selected at design time) exists with
  `fetch_mechanism = github-tarball`, and the registry mirror
  fields are populated per upstream R23.
- [ ] **AC-B1b** (registry-only upgrade). Given a registry entry
  from an older binary with `source_url = "org/dot-niwa"` AND an
  on-disk `<workspace>/.niwa/` that is already a snapshot (no
  `.git/`, provenance marker present), the first `niwa apply`
  succeeds without any one-time conversion notice. The registry
  mirror fields are populated per upstream R23 on first save.
- [ ] **AC-B2**. Given the AC-B1a preconditions plus an additional
  workspace registered with the new slug shape
  `source_url = "acme/vision:.niwa"`, both workspaces apply
  successfully in the same `niwa apply --all` invocation. Neither
  triggers a prompt or `--force` requirement.

### AC: Rank-3 removal (verifies R20)

- [ ] **AC-R1**. Given a `tarballFakeServer` source containing
  only a root `niwa.toml` (no `.niwa/workspace.toml`, no root
  `workspace.toml`), `niwa init <name> --from owner/repo` exits
  non-zero with the R4 "no niwa config found" diagnostic. Stderr
  MUST NOT contain the substring `niwa.toml` (the removed marker
  is deliberately not advertised as an accepted path).
- [ ] **AC-R2**. The migration guidance in
  `docs/guides/workspace-config-sources.md` contains a section
  with the exact heading anchor `#rank-3-removal`. The section
  body contains all three literal substrings: `niwa.toml`,
  `rank 3` (or `rank-3`), and `.niwa/workspace.toml`.
- [ ] **AC-R3**. Given the AC-R1 setup with an existing registry
  whose source resolved only via the (now removed) rank-3
  marker, `niwa apply <name>` exits non-zero with the R4
  diagnostic. No upgrade-time pre-flight or proactive warning
  fires before the apply (verified by asserting that the first
  command after upgrade — `niwa apply` — produces the error;
  no earlier command emits a rank-3-removal warning).

### AC: Diagnostic clarity (verifies R21)

- [ ] **AC-X1**. The R3 ambiguity error message (verified
  end-to-end by AC-D5) MUST contain three independently
  grep-able substrings: the source slug (`acme/vision` for the
  test fixture), the literal `.niwa/workspace.toml`, and the
  literal `workspace.toml`. The substrings may appear on the
  same or different lines. The exact rendering is not asserted
  beyond Story 6's diagnostic block as a visible reference.
- [ ] **AC-X2**. The R4 no-marker error message (verified
  end-to-end by AC-D6) MUST contain four independently
  grep-able substrings: the source slug, the literal
  `.niwa/workspace.toml`, the literal `workspace.toml`, and a
  string containing both the literal `--from` and the literal
  `:` (the explicit-subpath hint). The substrings may appear
  on the same or different lines.

### AC: Upstream PRD reconciliation (verifies R22)

- [ ] **AC-U1**. After this PRD is Accepted,
  `docs/prds/PRD-workspace-config-sources.md` either (a) carries
  a `status` frontmatter field with the value `In Progress`
  rather than `Done`, with a body Status section matching; OR
  (b) contains an amendment block under the existing
  `## Amendments` section, dated with the acceptance date,
  containing the literal substring `PRD-config-source-discovery`
  (this PRD's filename). One of the two MUST be true.

### AC: Documentation (verifies R23)

- [ ] **AC-G1**. `docs/guides/workspace-config-sources.md`
  contains a heading with the exact anchor
  `#single-repo-workspace`. The section body contains the
  literal substring `niwa init --from owner/repo` (without
  explicit subpath) and a fenced code block showing an on-disk
  layout that includes `.niwa/` and at least one workspace
  component subdirectory.
- [ ] **AC-G2**. The same guide contains a heading with the
  exact anchor `#brain-repo`. The section body contains the
  literal substrings `discoverAllRepos`, `Classify`, and a
  reference to PR #138 (literal substring `#138`).
- [ ] **AC-G3**. The same guide contains a heading with the
  exact anchor `#niwa-migrate-source`. The section body
  contains the literal substrings `--to`, `--yes`, and at
  least one exit-code line referencing each of codes 0, 1, 2,
  3, and 130.

## Out of Scope

The following are excluded from this PRD's v1 commitment.

- **Re-specification of subpath fetch mechanics.** The upstream PRD
  R14 (GitHub tarball + selective extraction) and R15 (git-clone
  fallback) are inputs to this PRD, not outputs. This PRD does not
  modify the fetch pipeline; it adds a marker-scan pass during
  streaming.
- **Sparse-checkout for the non-GitHub fallback path.** Identified
  as a future optimization for very large monorepos; the existing
  shallow clone is sufficient for v1.
- **Per-host adapters (GitLab, Bitbucket, GitHub Enterprise Server,
  Gitea).** The host-agnostic fallback path covers them on day one.
- **Schema changes to `workspace.toml` or other config files.** This
  PRD only changes *where* niwa looks for the file, not what it
  contains.
- **Recipe schema or action-system changes in `tsuku`.**
- **Vault provider, telemetry, or session lifecycle changes.**
- **Hard deprecation of the standalone `dot-niwa` pattern.**
  Coexistence is the v1 stance per R18/R19. A future PRD may revisit
  if data shows consolidation has stalled, but this PRD does not
  schedule that conversation.
- **Forced migration tooling.** `niwa migrate-source` is opt-in;
  niwa never offers to migrate a user's registry on its own.
- **`niwa.toml` rank-3 discovery.** Removed in v1.x per R20. A
  future PRD could re-introduce a single-file root manifest if a
  concrete user need surfaces, but this PRD does not specify one.
- **A `--strict-refresh` flag for CI operators.** Mentioned in the
  upstream PRD (Story 6) as a deferred follow-up; remains deferred.
- **Auto-detection of new overlay slugs after `migrate-source`.**
  `niwa migrate-source` does not detect, warn about, or remediate
  the overlay-slug change that comes with the brain-repo migration
  (per the Known Limitations entry below).

## Open Questions

None. The three policy questions the upstream PRD left implicit
(probe mechanism, migration tooling, rank-3 keep/drop) are resolved
in the Decisions and Trade-offs section below. The choice of
upstream-PRD reconciliation style (status change vs amendment
block) per R22 is left to the maintainer accepting this PRD but is
not an open *requirements* question.

## Known Limitations

- **Slug repo-root sentinel preserved.** Per the upstream PRD's
  Known Limitations: when discovery is ambiguous (both rank-1 and
  rank-2 markers present), there is no consumer-side way to say
  "use rank-2 explicitly" via the slug — `owner/repo:` is rejected
  as an empty subpath (upstream R3a). The only resolution is for
  the source maintainer to remove one marker. This PRD inherits
  the limitation; users hitting it can use the explicit-subpath
  escape hatch (`owner/repo:.niwa` for rank-1) but cannot ask for
  "rank 2" by syntax.
- **First-fetch bandwidth on the GitHub path (inherited).** The
  tarball endpoint delivers the entire repo's gzipped bytes — the
  cost is linear in the source repo's size, not in the subpath's
  size. For brain repos that are large or carry binary assets,
  the first fetch may take noticeably longer than the resulting
  snapshot suggests. Subsequent applies are cheap (40-byte SHA
  endpoint or 304 ETag).
- **Probe-pass scan cost.** R7-R9 require scanning the tarball
  stream (or the shallow-clone working tree) for marker files
  before deciding what to extract. The scan is bounded by the
  existing 500 MB cap from `internal/github/tar.go`. For brain
  repos that approach the cap, the probe pass adds the same
  bounded cost as a full extraction.
- **Migration tooling is opt-in.** Users who don't run
  `niwa migrate-source` stay on the old slug indefinitely. The
  binary never proactively migrates anyone. Maintainers who want
  to drive consolidation must do so through communication, not
  through niwa-internal pressure.
- **`niwa.toml` rank-3 removal is a behaviour change.** Any
  existing user relying on a root `niwa.toml` discovery will see
  the R4 "no niwa config found" diagnostic on first apply after
  upgrade. The diagnostic's "no rank-3 marker mentioned" omission
  is intentional (R21) and is a small breaking change; mitigation
  is documentation (AC-R2) plus the explicit-subpath escape hatch
  (which still works for any path the user knows about).
- **Overlay slug changes during consolidation (inherited).**
  Migrating a workspace from `org/dot-niwa` to `org/brain:.niwa`
  implicitly changes the auto-discovered overlay slug from
  `org/dot-niwa-overlay` to `org/.niwa-overlay` per upstream R35.
  Maintainers must arrange for the overlay repo at the new slug
  before consumers complete their migration, otherwise the overlay
  clone silently skips and consumers lose the augmentation. The
  `niwa migrate-source` command does NOT detect or warn about this
  in v1; it is a documentation responsibility (Story 3 in the
  guide section per AC-G3 must call this out).

## Decisions and Trade-offs

### Decision: single-call probe mechanism (R7-R9)

**Decided**: discovery scans the tarball stream during the single
fetch the materialization already performs (GitHub path), or the
shallow-clone working tree's top level (non-GitHub path).
**Alternatives**: (a) make a separate GitHub Contents API call
to inspect the source for marker files before deciding what to
extract; (b) two-pass — fetch a partial tarball just for the root
layout, then re-fetch the subpath. **Reasoning**: the existing
GitHub fetch path already streams the tarball through
`ExtractSubpath` with bounded memory and strong security defenses;
adding a scan pass during the stream costs no extra round-trip and
reuses the security-audited code path. A separate Contents API
call would add latency on the cold path and split the auth surface
(Contents API has its own rate limits and PAT scope requirements).
The bandwidth cost of fetching the whole tarball is the same
either way and is already documented as a Known Limitation in the
upstream PRD.

### Decision: ship `niwa migrate-source` together with discovery (R10)

**Decided**: this PRD ships discovery and the migration command
together. **Alternatives**: ship discovery first; observe whether
consolidation friction is real; ship migration tooling only if so.
**Reasoning**: discovery without migration tooling forces users to
hand-edit their registry to move off the legacy `dot-niwa` pattern,
which is exactly the kind of friction the consolidation-friendly
pose is supposed to remove. Shipping them together gives the brain-
repo maintainer a clean message ("run this one command, then
`niwa apply --force`") and prevents a two-release UX gap during
which users see the new pattern documented but cannot adopt it
without manual registry surgery.

### Decision: drop rank-3 (`niwa.toml`) discovery (R20)

**Decided**: rank-3 discovery from the upstream PRD R5 is removed
in this PRD's scope. **Alternatives**: keep rank-3 plus the upstream
R8 explicit-`content_dir` requirement. **Reasoning**: rank-3 was
designed for the case where a brain repo wants to host its workspace
config at the repo root via `niwa.toml` instead of a subdir. No
observed user is on this path today, and the additional
`content_dir` requirement makes the error matrix substantially more
complex (R8 in the upstream PRD specifies a different diagnostic when
discovery resolves via rank-3 vs ranks 1-2). Dropping rank-3
narrows the discovery surface to two ranks, simplifies the error
matrix, and aligns the convention with established ecosystem
practice (`.github/`, `.vscode/`, `.editorconfig` — all subdir
patterns).

### Decision: coexistence by default, no hard deprecation

**Decided**: standalone `org/dot-niwa` registries continue to work
indefinitely via rank-2 discovery (R18/R19). niwa never proactively
migrates anyone. **Alternatives**: (a) add a deprecation warning
that fires once per apply when a legacy registry is detected;
(b) schedule a v2 release that hard-removes rank-2 fallback and
forces migration. **Reasoning**: the cost of legacy support is one
extra marker check during discovery; the benefit is that no existing
user is ever broken by upgrading. The user's exploration input
("consolidate everyone onto the new pattern") is best served by
making the new pattern painless to adopt (R10 migration tooling)
rather than by deprecating the old pattern. Consolidation that
happens organically because the new path is better leaves a smaller
support tail than consolidation that happens under deprecation
pressure.

### Decision: explicit-subpath slug bypasses discovery (R2, AC-D3a/D9)

**Decided**: an explicit subpath in the slug
(`--from owner/repo:custom/path`) bypasses discovery entirely;
ambiguity errors do not fire. **Alternatives**: discovery always
runs and the explicit subpath is treated as a "prefer this rank"
hint. **Reasoning**: the explicit-subpath syntax is the user
asserting "I know what I want." Treating it as a hint adds
confusion when a brain repo has both an explicit
`custom/path/workspace.toml` and a `.niwa/workspace.toml`. The
user typed the subpath; honour it. This preserves the upstream
PRD R9 contract.

### Decision: discovery errors keep the on-disk snapshot untouched (R5)

**Decided**: any discovery failure (ambiguity, no marker, network
error, truncated tarball) leaves the existing on-disk
`<workspace>/.niwa/` byte-identical to its pre-init state, or
absent entirely if `<workspace>/` did not exist before. No partial
state is visible. **Alternatives**: partial materialization at the
staging path is visible for debugging. **Reasoning**: the upstream
PRD's snapshot posture (R12 atomic swap, R37 no-side-effect-files)
commits to "no partial state visible." This PRD preserves that
contract. Debugging is supported via verbose flags printing what
was probed and what was found; the on-disk state itself stays
clean.

### Decision: `niwa migrate-source` is registry-only (R10)

**Decided**: the command rewrites the registered source slug and
nothing else. It does not run apply, does not delete the snapshot,
does not touch the source repo (except to probe when `--to` is
omitted; that probe is read-only). **Alternatives**: a "full
migration" command that rewrites the registry, runs `apply
--force`, and deletes the legacy snapshot in one shot.
**Reasoning**: separating concerns matches the existing niwa
pattern (config-set vs apply). Users who want a one-shot can pipe
the commands; users who want to inspect the registry change before
applying retain that ability. The error surface stays clean:
registry errors surface from `migrate-source`, snapshot errors
surface from `apply`.

### Decision: empty `.niwa/` is not a rank-1 match (R6)

**Decided**: a `.niwa/` directory at the source root that does
NOT contain `workspace.toml` MUST NOT trigger rank-1 matching;
discovery continues to rank 2 (or fails per R4 if rank 2 is also
absent). **Alternatives**: any presence of `.niwa/` (directory or
file) commits the source to rank 1 and fails with a "marker
incomplete" error if `workspace.toml` is missing inside.
**Reasoning**: a `.niwa/` directory with only ancillary files
(README, `.gitkeep`, etc.) is a plausible incremental setup state
during migration. Falling through to rank 2 makes the migration
forward-compatible: a maintainer can land an empty `.niwa/`
directory in PR #1, then add `.niwa/workspace.toml` in PR #2,
without breaking discovery for anyone using the source between
those two PRs.

### Decision: `--to` slug is grammar-validated only, not resolution-validated (R11)

**Decided**: when `--to <slug>` is passed,
`niwa migrate-source` validates that the slug parses per the
upstream R1-R3 parser rules and rejects malformed slugs at parse
time, but does NOT validate that the destination resolves to a
real workspace config. The next `niwa apply --force` is where
discovery resolution happens. **Alternatives**: probe the
destination during `migrate-source` and refuse if it doesn't
resolve. **Reasoning**: probing the destination doubles the
network cost of the command and creates a second class of
failures the user has to interpret (parse vs resolution).
Separating "is the slug well-formed?" from "does the destination
contain valid config?" matches the existing niwa pattern (parse
errors are immediate; resolution errors surface at apply time).
Users running `migrate-source` in a CI script that will run
`apply --force` immediately afterwards see the resolution error
in the same job; users running it by hand and inspecting the
registry before applying retain that ability.

### Decision: `niwa migrate-source --to <current-slug>` is a no-op (R14)

**Decided**: when `--to <slug>` matches the workspace's current
`source_url`, the command exits 0 with a "already on this slug"
message and does not modify the registry. **Alternatives**: refuse
with an error; rewrite silently (no-op but no message).
**Reasoning**: an idempotent no-op with a visible confirmation
fits the existing niwa pattern (running the same `config set`
twice doesn't error). The visible message tells the user the
state matches expectations; the zero exit code keeps automation
clean (CI scripts that ensure-the-slug-is-X don't need to filter
errors).

### Decision: TTY-and-`--yes` interaction (R13)

**Decided**: when `--to` is omitted and discovery resolves
unambiguously, three behaviours are possible based on stdin
state and `--yes` presence:
- TTY + no `--yes` → prompt and accept `y`/`yes` for confirmation
- non-TTY + no `--yes` → print suggestion and exit non-zero with
  a "re-run with `--yes`" instruction
- any TTY + `--yes` → apply immediately

**Alternatives**: (a) always prompt regardless of TTY; (b) always
apply when discovery is unambiguous; (c) require `--yes` explicitly
in every non-TTY context with no inferred behaviour.
**Reasoning**: the three-way split matches the conventional Unix
contract for "destructive but recoverable" commands. Pure
non-interactive automation must opt in explicitly (`--yes`);
interactive users get a familiar prompt; non-interactive users
who forgot `--yes` get a helpful "here's what I would have done,
re-run with `--yes`" message rather than either a silent apply
(surprising) or a hard refuse (unhelpful).

## Next Steps

After this PRD is Accepted:

- `/design` against this PRD to produce the technical design for
  the discovery code path and the `niwa migrate-source` command.
- Apply the upstream PRD reconciliation chosen per R22 / AC-U1.
