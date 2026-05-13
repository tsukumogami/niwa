# /prd Scope: embedded-niwa-config

## Problem Statement

`niwa init --from owner/repo` against a general-purpose repo today
materializes the **entire repo** at `<workspace>/.niwa/`, even when the
repo carries its niwa configuration at a `.niwa/` subdirectory. This
makes single-repo workspaces impractical (a developer has to either
stand up a dedicated `dot-niwa` repo, or type `--from owner/repo:.niwa`
verbatim and remember that syntax) and prevents the natural "brain
repo" pattern where a workspace's strategic content and niwa config
co-exist in one repo. The upstream PRD
`docs/prds/PRD-workspace-config-sources.md` already specifies the fix
(R5: probe `.niwa/workspace.toml`, root `workspace.toml`, root
`niwa.toml` in precedence order when no explicit subpath is given) and
is marked `Done`, but the discovery code path is not actually built —
`materializeFromGitHub` extracts with empty subpath whenever the user
omits one. The need is to close that gap while resolving three
adjacent policy decisions the upstream PRD left implicit.

## Initial Scope

### In Scope

- Implement R5 convention-based subpath discovery: when the user types
  `niwa init --from owner/repo`, niwa probes the source for marker
  files in precedence order and resolves the subpath automatically.
- Implement R6 (ambiguity error when multiple markers are present at
  source root) and R7 (no-marker discovery error with a clear
  diagnostic and the explicit-subpath escape hatch).
- Make R33 functionally true: existing `--from org/dot-niwa` users
  resolve via rank-2 discovery (root `workspace.toml`) with no action
  required after upgrade.
- Specify the **discovery probe mechanism** (two-call API probe vs
  single-call tarball-scan) as a Decision in the PRD.
- Specify the **migration tooling shape** (`niwa migrate-source <name>`
  command vs passive coexistence) as a Decision.
- Specify whether to **keep or drop rank-3 `niwa.toml` discovery** in
  this iteration.
- Restate the affected user stories (Story 1: subpath adoption;
  Story 2: migrating from standalone dot-niwa) end-to-end so the new
  PRD reads standalone.
- Updated acceptance criteria for the discovery surface, anchored to
  fixtures already specified in the upstream PRD (`tarballFakeServer`,
  legacy-working-tree fixture).
- Documentation updates: `docs/guides/workspace-config-sources.md`
  needs the single-repo and brain-repo walk-throughs.

### Out of Scope

- Re-specification of subpath fetch mechanics (already specified in
  the upstream PRD: R14 GitHub tarball + selective extraction; R15
  git-clone fallback). The new PRD references these as inputs, not
  outputs.
- Schema redesign of `workspace.toml`. The new convention only changes
  *where* niwa looks for it, not what it contains.
- Sparse-checkout or any other bandwidth-optimisation fetch path. The
  upstream PRD lists these as deferred follow-ups; that stays true.
- Per-host adapters for GitLab, Bitbucket, etc. The fallback path
  covers them; out of v1.
- Recipe schema or action system changes in the `tsuku` package
  manager.
- Vault provider, telemetry, or session lifecycle changes.
- Hard removal of the standalone `dot-niwa` pattern. The new PRD
  documents a "gentle consolidation" stance: make `.niwa/` so painless
  to adopt that consolidation happens organically. No flag-day
  deprecation.

## Research Leads

The exploration's seven leads have already been investigated; the PRD
draft can build on them directly rather than re-investigating.

1. **Probe mechanism trade-offs** (resolved in exploration; needs to
   be written up as a Decision): single-call tarball-scan is the
   recommended starting position because it adds no extra round-trip
   and reuses the existing extraction pipeline. Two-call (Contents
   API probe + tarball fetch) is the alternative if the single-call
   bandwidth cost is unacceptable for any future deployment shape.

2. **Ambiguity diagnostic shape** (open in PRD): when a source repo
   contains both `.niwa/workspace.toml` AND a root `workspace.toml`,
   the upstream PRD's R6 says to fail with "ambiguous niwa config"
   naming both files. The new PRD should confirm this remains
   correct and decide whether to add a `--prefer-rank N` override or
   just rely on the user trimming one of the markers.

3. **Migration UX walk-through** (resolved in exploration; needs to
   be written up): `niwa migrate-source <name>` is recommended as a
   thin command that inspects the current source, suggests an
   updated slug, and writes the registry change. The existing
   `--force` flow handles the on-disk re-materialization. The
   command's UX is the open spec point.

4. **Rank-3 (`niwa.toml`) drop decision** (open): keep means full
   fidelity with the upstream PRD; drop means simpler error matrix.
   The exploration's recommendation is to **drop** for v1.x and
   leave a Documented Limitation noting that brain repos using
   `niwa.toml` at root must migrate to `.niwa/workspace.toml`.

5. **Overlay slug migration risk** (resolved in exploration; needs
   to be restated): migrating a workspace from `org/dot-niwa` to
   `org/brain:.niwa` implicitly changes the overlay slug from
   `org/dot-niwa-overlay` to `org/.niwa-overlay`. The migration
   playbook in the new PRD must call this out.

6. **Single-repo on-disk topology** (resolved): no new topology;
   the existing pattern (snapshot at `<workspace>/.niwa/`, repos
   cloned under `<instance-root>/<org>/<repo>/`) works. The new
   PRD should restate this layout for the single-repo case so
   users can visualise it.

7. **Brain-repo composition** (resolved): no special handling
   needed — the brain repo flows through `discoverAllRepos` and
   `Classify` like any other repo (per PR #138 precedent).

## Coverage Notes

The exploration was tightly scoped to "what gap exists and what does
closing it look like." Items the PRD process should still resolve:

- **Implementation-level diagnostic strings.** R6 says "ambiguous
  niwa config" naming the conflicting files; the exact wording
  belongs in the design phase that follows. The PRD should commit
  to the *contract* (file paths in the message; literal substring
  for golden test assertion) and leave the exact text to design.
- **Acceptance criteria text-fragments.** The upstream PRD already
  has AC-D1 through AC-D9 covering discovery. The new PRD should
  state which of those carry over verbatim, which are restated
  for clarity, and which need amendment (e.g., AC-D3 / AC-D4 about
  rank-3 `niwa.toml` may go away if rank-3 is dropped).
- **The "Done" status reconciliation.** The new PRD should either
  re-status the upstream PRD or live alongside it with a clear
  pointer noting that R5+R6+R7+R8+R33 are the unfinished work this
  PRD covers. Format choice belongs to the PRD process.

## Decisions from Exploration

- **Target artifact**: PRD. User-stated preference and crystallize
  scoring agree.
- **Artifact form**: freestanding new PRD that references
  `PRD-workspace-config-sources.md` as upstream, not an amendment
  block. Reasoning: the new Decisions (probe mechanism, migration
  tooling, rank-3 drop) don't fit cleanly as amendments to existing
  requirements.
- **Convergence**: one discover-converge round was sufficient. No
  signal that a second round would surface new dimensions.
- **Consolidation stance**: gentle, not forced. The new PRD
  documents the `niwa migrate-source` command as the painless
  migration path; the binary itself keeps coexistence behaviour
  (rank-1 and rank-2 discovery both work) so existing users are
  never broken.
- **Migration tooling**: ship `niwa migrate-source <name>` (open
  spec on flags and UX); pair with a documented brain-repo
  maintainer playbook.
- **Rank-3 `niwa.toml`**: drop in v1.x. Brain repos using it migrate
  to `.niwa/workspace.toml`. Document as Known Limitation /
  Migration step.
- **Probe mechanism**: single-call tarball-scan recommended; the
  PRD's Decisions section walks the alternatives and lands on this.
- **Out-of-scope guard**: do not re-spec subpath fetch mechanics,
  recipe schema, or anything the upstream PRD already specifies and
  has implemented. The new PRD is narrowly scoped to closing the
  discovery gap.
