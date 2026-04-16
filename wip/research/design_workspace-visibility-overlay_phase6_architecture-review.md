# Architecture Review: workspace-visibility-overlay

## Scope

This review covers the Solution Architecture and Implementation Approach sections of the
workspace-visibility-overlay design. It assesses implementability, missing components,
phase sequencing, and whether simpler alternatives were overlooked. All findings reference
the current codebase at `internal/config/`, `internal/workspace/`, and `internal/cli/`.

---

## 1. Is the Architecture Clear Enough to Implement?

**Yes, with two ambiguities that need resolution before Phase 3.**

The component inventory is accurate and the data flow diagram maps cleanly onto the actual
pipeline. `runPipeline()` in `apply.go` already has labeled steps (2, 2.1, 2.5, 2a, 3–6.75)
so the numbering in the design matches the source. `MergeGlobalOverride` is the direct
precedent for `MergeWorkspaceOverlay` and the deep-copy + per-field merge semantics are
already established in `override.go`.

**Ambiguity 1: `ensureImportInCLAUDE` insertion position.**

The current `ensureImportInCLAUDE(claudePath, importLine string)` always prepends — it
does not accept a position parameter. The design calls `ensureImportInCLAUDE(instanceRoot, "@CLAUDE.overlay.md", afterWorkspaceContext)` with an insertion position argument.
That signature does not exist. The function needs to be extended to support ordered insertion
before Phase 4 can be implemented. The required ordering (workspace context → overlay →
workspace content → global) is not currently enforced; `InstallGlobalClaudeContent` also
calls `ensureImportInCLAUDE` with the current prepend behavior. Ordered insertion is a
non-trivial change to a shared utility that affects at least three call sites.

**Ambiguity 2: `OverlayDir` sanitization for non-GitHub URLs.**

The design specifies "parse GitHub URL or shorthand, produce `<org>-<repo>`" but the
codebase has no URL parsing utilities for GitHub URLs or shorthands. The implementation
will need to handle at minimum: HTTPS (`https://github.com/org/repo`), SSH
(`git@github.com:org/repo.git`), and shorthand (`org/repo`). The design should state
the exact parsing logic or reference a helper that will be added to the `config` package.
Leaving this unspecified risks inconsistency between init-time derivation and apply-time
re-derivation.

---

## 2. Missing Components or Interfaces

**Missing: `SchemaVersion` bump.**

`InstanceState.SchemaVersion` is currently `1` (constant in `state.go`). Adding
`OverlayURL`, `NoOverlay`, and `OverlayCommit` fields to the struct is backward-compatible
(all tagged `omitempty`), but there is no schema migration or version bump planned. Existing
`instance.json` files will load without error and report the new fields as zero values, which
is correct behavior. This is fine as-is; no migration is needed since zero-value semantics
are correct (no overlay). The design should acknowledge this explicitly rather than leaving
it implicit.

**Missing: `niwa apply` overlay re-sync path for new `--overlay` / `--no-overlay` override.**

The design adds `--overlay` and `--no-overlay` flags to `niwa init` but not to `niwa apply`.
If a user wants to add or remove an overlay on an existing instance without re-init, there is
no CLI path. The design does not state whether this is intentional. If overlay URL changes
require re-init, document that constraint. If per-apply override is intended, the flag wiring
in `apply.go` needs to be included in Phase 3.

**Missing: Managed file tracking for overlay artifacts.**

The pipeline returns a `pipelineResult` with `managedFiles []ManagedFile`. The design
describes `InstallOverlayClaudeContent` writing `CLAUDE.overlay.md` to the instance root
and `CloneOrSyncOverlay` writing the clone to `$XDG_CONFIG_HOME/niwa/overlays/`. The
`CLAUDE.overlay.md` at the instance root should be added to `managedFiles` so it is cleaned
up when the overlay is removed or when `--no-overlay` is applied. The overlay clone
directory itself is outside `managedFiles` scope (XDG, not instance root) and does not
need tracking, but the copied CLAUDE file does. The design does not mention this tracking.

**Missing: `niwa destroy` behavior for overlay state.**

`destroy.go` cleans up instance state. If `OverlayURL` and `OverlayCommit` are stored in
`instance.json`, `niwa destroy` will remove `instance.json` but leave the overlay clone at
`$XDG_CONFIG_HOME/niwa/overlays/<name>/`. This is consistent with how the global config
clone is left in place when a workspace is destroyed, but it should be stated explicitly.
If multiple instances share the same overlay clone (same overlay URL, different instance
paths), destroy should not remove the shared clone.

---

## 3. Are the Implementation Phases Correctly Sequenced?

**Mostly yes. One dependency is inverted and one phase boundary is drawn at the wrong
place.**

**Inverted dependency between Phase 1 and Phase 2.**

Phase 1 delivers `CloneOrSyncOverlay` and `OverlayDir` in isolation. But `CloneOrSyncOverlay`
takes a `url` argument that comes from convention derivation (`deriveOverlayURL`), which is
also in Phase 1. Those pieces are correctly co-located. However, `CloneOrSyncOverlay`
during `niwa init` also needs to capture the HEAD commit SHA for `OverlayCommit` in state —
and that SHA is read from the cloned repo using `git rev-parse HEAD`. This git interaction
is not mentioned in Phase 1's deliverables; it appears only as part of Phase 3's "warn if
HEAD SHA differs from stored `OverlayCommit`" requirement. If `OverlayCommit` is not
written at init time (Phase 1), Phase 3's warning check has nothing to compare against.
Move `OverlayCommit` capture from Phase 3 into Phase 1 deliverables.

**Phase boundary between Phase 3 and Phase 4 splits a single apply call.**

Phase 3 delivers `runPipeline()` integration (steps 2.5–2.6, overlay sync, state update)
but defers `InstallOverlayClaudeContent` to Phase 4. After Phase 3, the pipeline is
integrated but any instance with an overlay URL will complete without actually writing
`CLAUDE.overlay.md`. Integration tests for Phase 3 will need to account for partial
behavior. This is workable but should be noted so the Phase 3 test plan doesn't assert
on CLAUDE.md content ordering that doesn't exist yet.

**Phase 4 placement of `ensureImportInCLAUDE` refactor.**

The `ensureImportInCLAUDE` signature extension is implicitly required by Phase 4 but
would be cleaner in Phase 3 (or even Phase 2) so that Phase 4 only adds call sites,
not new infrastructure. If the import ordering is deferred to Phase 4, Phase 5c in the
current pipeline (global CLAUDE import) may produce incorrect ordering in the interim.
Recommend adding a Phase 2.5 or Phase 3 deliverable: "extend `ensureImportInCLAUDE` to
accept an ordered insertion position; update existing call sites."

---

## 4. Simpler Alternatives Overlooked?

**One significant simplification for Phase 1: skip convention discovery on `niwa apply`.**

The design proposes that `niwa apply` re-attempts convention discovery if `OverlayURL` is
not set in `instance.json`. This creates ongoing complexity: the pipeline must check for
`OverlayURL` absence, derive the convention URL from `RegistryEntry.Source`, attempt a
clone, and decide whether to silently skip or hard-error, all within the hot path of every
apply. The security review (`phase5_security`) independently identified this as a supply
chain risk (post-init squatting window) and recommended restricting discovery to `niwa init`
only.

Restricting discovery to `niwa init` is simpler:
- `runPipeline()` step 2.5 becomes: if `OverlayURL` set, sync; if `NoOverlay`, skip; otherwise
  skip entirely. No registry lookup, no URL derivation, no silent-skip logic in the pipeline.
- The complexity moves entirely to `runInit()`, where it belongs (it's a one-time setup
  decision) and is easier to test in isolation.
- Users who want to add an overlay later can use `niwa init --overlay <url>` in an existing
  workspace directory (if `CheckInitConflicts` is relaxed for overlay-only re-init) or a
  future `niwa overlay set <url>` subcommand.

This change reduces Phase 3 complexity by removing the convention-derivation-from-registry
path from `runPipeline()` entirely. The only behavioral difference is that existing instances
initialized without `--from` cannot automatically pick up a convention overlay on the next
apply, which is the right default given the supply chain concerns.

**Minor simplification: `OverlaySource` as internal-only field does not need TOML tag.**

`ContentRepoConfig.OverlaySource` is described as "TOML-hidden." In Go, a field with
`toml:"-"` (or no TOML tag) cannot round-trip through TOML serialization, which is fine for
a runtime-only field. But since `WorkspaceConfig` is parsed from `workspace.toml` (not
written back), the field just needs to be excluded from TOML decoding. A `toml:"-"` tag
is correct; just make sure the design uses that syntax explicitly rather than "TOML-hidden"
which is ambiguous.

---

## Summary

The architecture is sound and implementable. The component inventory maps accurately to the
codebase. Four targeted fixes make it production-ready:

1. Resolve the `ensureImportInCLAUDE` ordered-insertion gap before Phase 4 (or move it to
   Phase 3 deliverables).
2. Move `OverlayCommit` SHA capture from Phase 3 into Phase 1 deliverables.
3. Restrict convention discovery to `niwa init` only — remove the re-derivation path from
   `runPipeline()`. This simplifies Phase 3 and closes the supply chain risk noted in the
   security review.
4. Add `CLAUDE.overlay.md` to `managedFiles` tracking so it is cleaned up when the overlay
   is removed.
