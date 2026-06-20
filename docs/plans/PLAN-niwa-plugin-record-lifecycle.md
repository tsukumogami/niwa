---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-niwa-plugin-record-lifecycle.md
milestone: "niwa plugin record lifecycle"
issue_count: 9
---

# PLAN: niwa plugin record lifecycle

## Status

Active

Single-pr plan, authored from the Accepted design. No GitHub issues or
milestone are created; the outlines below guide one PR. Awaiting review
before implementation begins via /work-on.

## Scope Summary

Implement the niwa-side fix for Claude plugin-record decay: a registry-
access package, three pruning surfaces (destroy, apply, `niwa plugins
prune`), a per-marketplace `auto_update` config (default off), the
marketplace name-keying fix, and per-marketplace version tracking that
defaults github sources to their latest stable release instead of main.

## Decomposition Strategy

**Horizontal.** The design describes loosely-coupled components with a
clear prerequisite order: a foundation package that several call sites
delegate to, plus an independent config-schema track. One component
(`internal/pluginrecord`) is the prerequisite for the destroy, apply,
and command surfaces, which matches the horizontal "prerequisite before
consumers" shape rather than a walking skeleton — there is no runtime
end-to-end path whose integration risk justifies a thin vertical slice
first. The config track (Issues 6-7) is independent of the registry
track (Issues 1-5) and runs in parallel.

## Issue Outlines

### Issue 1: feat(pluginrecord): registry I/O core with atomic write and backup

**Goal**: Add `internal/pluginrecord` with locate, preserve-unknowns
load/save, atomic temp-and-rename write, timestamped rotated backups,
and fail-safe handling of malformed or absent registries.

**Acceptance Criteria**:
- [ ] Locator resolves `~/.claude/plugins/installed_plugins.json` from
      `os.UserHomeDir`, overridable via an injected base dir for tests.
- [ ] Load uses a preserve-unknowns model: unmodelled record fields and
      top-level keys round-trip byte-stably through a load/save cycle.
- [ ] Writes go to a same-dir temp file created `O_CREATE|O_EXCL`, then
      `os.Rename` over the target; an interrupted write leaves either
      the prior or the fully-written file, never a truncated one (R10).
- [ ] Backup before first write is a timestamped sibling
      `installed_plugins.json.niwa-bak.<RFC3339>`, retains the last N,
      created `O_EXCL` with the source file's mode (R11).
- [ ] A malformed registry leaves the file unchanged and returns a typed
      error (R12); an absent registry loads as empty and is a no-op.

**Dependencies**: None

**Type**: code
**Files**: `internal/pluginrecord/registry.go`, `internal/pluginrecord/registry_test.go`

### Issue 2: feat(pluginrecord): dangling/instance predicates and Prune

**Goal**: Add the removal predicates and the single `Prune(selector,
opts)` entry point with dry-run and a removal report.

**Acceptance Criteria**:
- [ ] `dangling` matches a record whose non-empty `installPath` or
      `projectPath` directory is missing, via `Lstat` semantics (R9).
- [ ] `instanceOwned(root)` matches a record whose `projectPath` is
      within `root` using cleaned-path `filepath.Rel` containment, so a
      sibling whose path shares a textual prefix is NOT matched (R9).
- [ ] `Prune` re-reads the latest file immediately before writing,
      removes matching records, drops now-empty plugin keys, and returns
      a report (count removed, per-plugin breakdown, backup path).
- [ ] `dryRun` returns the same report with no write and no backup (R4).
- [ ] Removing only matches the selector; non-matching records are never
      removed (R9).

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/pluginrecord/prune.go`, `internal/pluginrecord/prune_test.go`

### Issue 3: feat(destroy): prune instance-owned records on teardown

**Goal**: Wire `DestroyInstance` and `DestroyWorkspace` to prune the
records owned by the instance roots they remove.

**Acceptance Criteria**:
- [ ] `DestroyInstance` prunes records whose `projectPath` is under the
      instance root (R1); `DestroyWorkspace` prunes for every instance
      root it removes (R2).
- [ ] The prune runs by the instance-owned predicate against the
      instance root (not the dangling predicate), so the records are
      matched by ownership; the order relative to `os.RemoveAll` is
      pinned so the matching is unambiguous.
- [ ] A unit test seeds a registry with records for two sibling
      instances and asserts destroying one removes exactly its records
      and leaves the sibling's intact.
- [ ] Destroy still succeeds (and reports) when the registry is absent
      or malformed (fail-safe, no destroy failure).

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/workspace/destroy.go`, `internal/workspace/destroy_workspace.go`, `internal/workspace/destroy_test.go`

### Issue 4: feat(apply): dangling-only sweep in the apply pipeline

**Goal**: Add a dangling-only prune step to `niwa apply` after managed-
file cleanup and before state save.

**Acceptance Criteria**:
- [ ] After `niwa apply`, no record remains whose `installPath` or
      `projectPath` directory is missing (R5).
- [ ] The sweep removes only dangling records, never live ones.
- [ ] A malformed/absent registry does not fail apply (fail-safe).

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/workspace/apply.go`, `internal/workspace/apply_test.go`

### Issue 5: feat(cli): niwa plugins prune command

**Goal**: Add `niwa plugins prune` — preview by default, `--apply` to
remove dangling records.

**Acceptance Criteria**:
- [ ] Default run previews: prints dangling count, per-plugin breakdown,
      and makes no change to the file on disk (R3, R4).
- [ ] `--apply` performs the removal, prints the summary and the backup
      location, and exits non-zero only on operational failure (not
      merely because dangling records were found).
- [ ] Registered under the existing `niwa plugins` command group.

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/cli/plugins.go`, `internal/cli/plugins_test.go`

### Issue 6: feat(config): per-marketplace MarketplaceConfig with back-compat

**Goal**: Migrate `ClaudeConfig.Marketplaces` from `[]string` to
`[]MarketplaceConfig` ({Source, AutoUpdate}) with a custom
`UnmarshalTOML` accepting the legacy string form.

**Acceptance Criteria**:
- [ ] Legacy `marketplaces = ["ref", ...]` configs parse unchanged, each
      mapping to `{Source: ref, AutoUpdate: false}` (R7, back-compat).
- [ ] New `[[claude.marketplaces]]` tables with `source` + optional
      `auto_update` parse; absent `auto_update` is `false` (R6 default).
- [ ] Overlay merge unions on `.Source` (base-wins on conflict).
- [ ] All `[]string` consumers compile and pass against the new type —
      a compiler-driven sweep covers `MaterializeConfig`, `overlay.go`,
      `workspace_context.go`, `materialize.go`, and existing tests.

**Dependencies**: None

**Type**: code
**Files**: `internal/config/config.go`, `internal/config/config_test.go`, `internal/workspace/override.go`

### Issue 7: feat(workspace): emit configured auto_update and key by declared name

**Goal**: Make `mapMarketplaceSourceWithIndex` emit the configured
`auto_update` (default false) and register local marketplaces under
their manifest-declared name.

**Acceptance Criteria**:
- [ ] A marketplace registers with `autoUpdate` matching its config
      value; an unconfigured marketplace registers `autoUpdate: false`
      (R6); the two hardcoded `true` literals are gone.
- [ ] A `directory`/`repo:` marketplace whose manifest `name` differs
      from its source ref registers under the manifest-declared name
      (R8); github sources retain repo-name keying.
- [ ] Tests cover default-off, opted-in, and the name-keying case.

**Dependencies**: Blocked by <<ISSUE:6>>

**Type**: code
**Files**: `internal/workspace/workspace_context.go`, `internal/workspace/materialize.go`, `internal/workspace/workspace_context_test.go`

### Issue 8: feat(workspace): track latest stable marketplace release

**Goal**: Add per-marketplace version tracking — default to the latest
stable release for github sources, with branch and explicit-ref
overrides — and resolve/emit the pin.

**Acceptance Criteria**:
- [ ] First, confirm the `known_marketplaces` github-source pin field
      Claude Code honors (Decision 6 spike); record the finding and the
      fallback taken if no ref is honored.
- [ ] `MarketplaceConfig` gains a `Track` value (`release` default,
      `main`, or explicit ref); empty defaults to `release` for github
      sources (R15, R17).
- [ ] A github marketplace with no override resolves to its highest
      non-prerelease release tag and registers against it, not main
      (R14); a marketplace with no stable release falls back to the
      branch and reports it (R16).
- [ ] Override to branch and override to explicit ref each register the
      requested target; unit tests cover all four cases.

**Dependencies**: Blocked by <<ISSUE:7>>

**Type**: code
**Files**: `internal/config/config.go`, `internal/workspace/workspace_context.go`, `internal/workspace/workspace_context_test.go`

### Issue 9: test(functional): plugin-record lifecycle scenarios

**Goal**: Add Gherkin functional scenarios exercising the pruning
surfaces and release-tracking end-to-end with the localGitServer harness.

**Acceptance Criteria**:
- [ ] Scenario: destroy an instance → its records are pruned (R1/R2).
- [ ] Scenario: `niwa apply` → dangling records are swept (R5).
- [ ] Scenario: `niwa plugins prune --apply` → an accumulated dangling
      registry is recovered and a backup exists (R3/R11).
- [ ] Scenario: a github marketplace registers against its release tag
      rather than main (R14).
- [ ] Scenarios run offline via `localGitServer` and pass under
      `make test-functional` (R13).

**Dependencies**: Blocked by <<ISSUE:3>>, <<ISSUE:4>>, <<ISSUE:5>>, <<ISSUE:8>>

**Type**: test
**Files**: `test/functional/features/plugin-record-lifecycle.feature`, `test/functional/steps_test.go`

## Implementation Issues

Not applicable in single-pr mode — no GitHub issues are created. The
Issue Outlines above are the decomposition; /work-on consumes them.

## Dependency Graph

Single-pr plan — dependencies are captured per outline (the
**Dependencies** field on each issue above) and summarized in the
Implementation Sequence below; no separate issue-graph diagram is
rendered for single-pr. The edges are:

- Issue 1 → Issue 2 → {Issue 3, Issue 4, Issue 5} → Issue 9
- Issue 6 → Issue 7 → Issue 8 → Issue 9 (config / marketplace track)

Roots (no dependencies): Issue 1, Issue 6.

## Implementation Sequence

**Critical path:** Issue 1 → Issue 2 → {Issue 3, 4, 5} → Issue 9, with
the config/marketplace track (Issue 6 → 7 → 8) also feeding Issue 9.

**Parallelization:**
- The config/marketplace track (Issue 6 → Issue 7 → Issue 8) is
  independent of the registry track and can proceed in parallel from the
  start. Issue 8 (version tracking) opens with the Decision 6 spike, so
  start it early to de-risk the pin mechanism.
- Once Issue 2 lands, Issues 3, 4, and 5 are independent of each other
  and can be built in parallel.
- Issue 9 (functional scenarios) lands last, after the pruning surfaces
  and version tracking exist.

**Suggested order for a single PR:** 1, 2, then 3/4/5 (any order), with
6/7/8 interleaved wherever convenient (run the Issue 8 spike early), and
9 last.
