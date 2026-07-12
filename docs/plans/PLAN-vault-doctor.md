---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-vault-doctor.md
milestone: "vault doctor command"
issue_count: 6
---

# PLAN: Vault doctor

## Status

Active

## Scope Summary

Implement `niwa vault check`, a read-only doctor for the team-vault
credential-sync contract, per DESIGN-vault-doctor.md (Accepted) and
PRD-vault-doctor.md (R1-R12). The command enumerates every configured
(kind, project) credential pair across the three vault registries apply
uses, fetches each body live through the single credential-sync
provider, validates it with the same `parseProviderAuthBody` validator
apply trusts, checks the local `provider-auth.toml` file layer, and
reports per-pair statuses -- human table by default, `--json` on
request -- with exit codes 0 (all valid), 1 (invalid pair or file-layer
failure), and 2 (vault unreachable or tool error). No secret value ever
reaches output on any path.

Almost everything load-bearing is reuse: `parseProviderAuthBody`,
`pickCredentialSyncSpec`/`openCredentialSyncProvider`,
`CredentialSyncPathPrefix`, `LoadProviderAuth`/`MatchProviderAuth`, the
`vault.Provider` sentinel errors, and the three-registry enumeration.
Net-new: result types, the `CheckProviderAuth` orchestrator in
`internal/workspace`, two renderers, the `vault` cobra parent with its
`check` subcommand, a typed exit-code error mapped in `root.go`, and
the test suite (unit fixtures, secret-canary, read-only hash check,
`@critical` functional scenario).

## Decomposition Strategy

Single PR, six issues. The split follows the design's layering so each
issue has one responsibility and a clean review boundary:

1. **Types and enumeration first.** The per-pair result type, the fixed
   status vocabulary, and the three-registry pair enumeration (with the
   self-pair guard) are pure data-and-logic with no I/O. Everything
   else consumes them, so they land first.
2. **Two independent check layers.** The live vault fetch (issue 2) and
   the local file-layer check (issue 3) both depend only on the types,
   not on each other. They can be built in parallel.
3. **Rendering next.** The table and JSON renderers (issue 4) consume
   the report shape from issue 1 and are independent of how results
   were produced.
4. **Wiring last.** The cobra surface, orchestration, and exit-code
   mapping (issue 5) can only be assembled once the layers it wires
   exist.
5. **Integration tests close it out.** Per-unit tests live inside each
   issue; issue 6 holds the cross-cutting proofs the PRD's acceptance
   list demands -- doctor/apply parity, the secret canary across all
   modes, the read-only hash check, and the functional exit-code
   scenario -- because each spans multiple layers.

Every acceptance criterion maps back to a PRD requirement or acceptance
item; nothing here goes beyond the design's stated scope.

## Issue Outlines

### Issue 1: Result types, status vocabulary, and pair enumeration

**Goal**: Define the per-pair result and report types with the fixed status vocabulary, and implement enumeration of expected (kind, project) pairs across the three vault registries with the credential-sync self-pair exclusion.

**Acceptance Criteria**:
- [ ] A per-pair result type carries `{Kind, Project, ProviderName, Status, Detail}` and the report type holds pair records plus file-layer findings, matching the design's Solution Architecture step 6.
- [ ] The vault-side status vocabulary is exactly `OK`, `missing-entry`, `malformed-body`, `missing-field`, `unsupported-version` (PRD R3), plus the run-level informational `no-credential-sync-configured` status for when `pickCredentialSyncSpec` returns nil (design step 1).
- [ ] Enumeration merges and deduplicates pairs from the three registries apply feeds `injectProviderTokens`: workspace-overlay, team workspace-config, and personal global-overlay (PRD R1; design step 2), covering both the anonymous `[vault.provider]` form and named `[vault.providers.<name>]` entries (PRD AC 1).
- [ ] The credential-sync provider's own (kind, project) is excluded from vault fetching and pre-marked `OK` with detail "authenticates via CLI session", mirroring `lookupVault`'s `SelfKind`/`SelfProject` guard -- never `missing-entry` (design step 3).
- [ ] Unit tests: a config with one anonymous and two named providers yields exactly the implied pairs (PRD R1 testable clause); a multi-registry fixture shows pairs split across all three registries appear; a self-pair fixture shows the exclusion.

**Dependencies**: None
**Type**: code
**Files**: `internal/workspace/providerauthcheck.go`, `internal/workspace/providerauthcheck_test.go`

### Issue 2: Live fetch and validation per pair

**Goal**: Fetch each enumerated pair's credential body through the single credential-sync provider and validate it with `parseProviderAuthBody`, mapping outcomes to the status vocabulary.

**Acceptance Criteria**:
- [ ] The provider is opened once via `pickCredentialSyncSpec` + `openCredentialSyncProvider` and its `Resolve` is called for every pair with the same `vault.Ref` construction as `lookupVault` (`CredentialSyncPathPrefix + kind`, key `"p-" + project`) -- never a bespoke probe, never a pair's own provider config (PRD R2, R5; design Decision 2A, step 4).
- [ ] `ErrKeyNotFound` maps to `missing-entry`; `ErrProviderUnreachable` marks the run vault-unreachable and aborts classification (PRD R10 distinction; design step 4).
- [ ] Successful fetches run `parseProviderAuthBody` (called in-package, still unexported) and its error classes map to `malformed-body` / `missing-field` / `unsupported-version` / `OK` (PRD R2, R3).
- [ ] One pair's failure never stops the loop; all pairs are evaluated in a single run (PRD R12).
- [ ] When no credential-sync provider is configured, the report carries the single informational `no-credential-sync-configured` finding with no pairs verified -- never an empty all-clear, never vault-unreachable (design step 1).
- [ ] The provider bundle is closed via `bundle.CloseAll()` after iteration (design Implementation Approach step 1).
- [ ] Unit tests with a fake `vault.Provider` cover each failure class fixture: absent key, non-TOML body, body missing `client_secret`, `version = "2"`, empty version accepted as "1", near-8-KiB body (PRD R3 testable clause, R5 edge cases).

**Dependencies**: Blocked by <<ISSUE:1>>
**Type**: code
**Files**: `internal/workspace/providerauthcheck.go`, `internal/workspace/providerauthcheck_test.go`

### Issue 3: Local file-layer check

**Goal**: Check `~/.config/niwa/provider-auth.toml` presence, mode, and per-entry shape via `LoadProviderAuth`/`MatchProviderAuth`, mapping results to the file-layer status vocabulary.

**Acceptance Criteria**:
- [ ] `LoadProviderAuth(configDir)` failures map to `bad-mode` (mode other than 0600) and `malformed-file` (unparseable file or entry missing a required field); a missing file maps to `absent`, informational and never a failure on its own (PRD R4).
- [ ] The `malformed-file` finding's Detail is a fixed categorical string -- never `LoadProviderAuth`'s raw TOML parse error, which is unsanitized (design Security Considerations, file-layer error sanitization).
- [ ] For a present valid file, `MatchProviderAuth` reports which pairs have a local entry (PRD R4; design step 5).
- [ ] Unit tests: a 0644 file flags `bad-mode`; a present file with a kind-less entry flags `malformed-file` with the fixed detail string; no file yields `absent`; a valid 0600 file yields no failure finding (PRD R4 testable clause, AC 7).

**Dependencies**: Blocked by <<ISSUE:1>>
**Type**: code
**Files**: `internal/workspace/providerauthcheck.go`, `internal/workspace/providerauthcheck_test.go`

### Issue 4: Table and JSON renderers

**Goal**: Render the report as a human-readable table by default and as machine-readable JSON under `--json`, emitting only pair identity, keyedPath, and status detail -- never a secret.

**Acceptance Criteria**:
- [ ] Default mode prints one row per pair (pair identity plus status) and separate file-layer lines (PRD R9); a three-pair run produces three matchable pair rows (PRD R9 testable clause).
- [ ] `--json` emits one record per checked pair and per file-layer finding, each carrying the pair identity and status; the output parses as JSON and a consumer recovers every (kind, project, status) triple without scraping the table (PRD R8).
- [ ] Renderer output carries only pair identity, keyedPath, and the scrubbed detail string the validator already renders -- no fetched body bytes or field values on any path (PRD R7; design Security Considerations, secret hygiene).
- [ ] The `no-credential-sync-configured` run renders an explicit "no pairs were verified" message in both modes, not an empty all-clear (design step 1).
- [ ] The vault-unreachable rendering names the provider and suggests its login command, so "not logged in" is never mistaken for a missing entry (design Mitigations).
- [ ] Unit tests cover both modes across representative statuses, including the informational and unreachable cases.

**Dependencies**: Blocked by <<ISSUE:1>>
**Type**: code
**Files**: `internal/cli/vault_check.go`, `internal/cli/vault_check_test.go`

### Issue 5: Command surface, orchestration, and exit codes

**Goal**: Add the `vault` cobra parent and `check` subcommand that wire enumeration, live fetch, file check, and rendering together, with the 0/1/2 exit-code contract mapped through a typed error in `Execute()`.

**Acceptance Criteria**:
- [ ] A new `vault` parent command and `check` subcommand self-register via `init()` + `rootCmd.AddCommand`, following the existing command pattern (design Decision 1A).
- [ ] The subcommand loads the workspace config and personal global overlay, assembles the three vault registries the same way apply's Step 0.4 pipeline does, calls `CheckProviderAuth`, and renders via issue 4's renderers (design Solution Architecture, CLI piece).
- [ ] A typed `vaultCheckError{ExitCode int}` is returned and `Execute()` in `internal/cli/root.go` maps it, alongside the existing `sessionattach.ExitCodeError` / `workspace.InitConflictError` branches (design Decision 3A).
- [ ] Exit codes: 0 when every pair and the file layer are valid, including the `absent` file and the `no-credential-sync-configured` informational run; 1 when the vault was reached but any pair or file-layer check failed (`missing-entry`, `malformed-body`, `missing-field`, `unsupported-version`, `bad-mode`, `malformed-file`); 2 when the vault is unreachable or the provider tool is missing/broken (PRD R10; design Decision 3A).
- [ ] The command is strictly read-only -- it calls only `Resolve`, `LoadProviderAuth`, and config loading, never writes, and never creates vault folders or entries as a probe side effect (PRD R6).
- [ ] The command runs standalone in a workspace where no apply has ever run, without requiring or triggering one (PRD R11).
- [ ] Unit tests cover the exit-code mapping for each outcome class.

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:4>>
**Type**: code
**Files**: `internal/cli/vault.go`, `internal/cli/vault_check.go`, `internal/cli/root.go`, `internal/cli/vault_check_test.go`

### Issue 6: Cross-cutting tests -- parity, secret canary, read-only proof, functional scenario

**Goal**: Add the integration-level tests the PRD's acceptance list demands: doctor/apply parity, the sentinel-secret canary across all statuses and modes, the read-only hash check, and the @critical functional exit-code scenario.

**Acceptance Criteria**:
- [ ] A table-driven test feeds identical credential-body fixtures (valid and every invalid class, including empty-version and near-8-KiB edge cases) to both the doctor and the apply path and asserts the verdicts match on every fixture (PRD R5, final AC).
- [ ] A secret-canary test seeds fixtures with known sentinel secret values -- including a malformed local `provider-auth.toml` -- and asserts the sentinel never appears in stdout, stderr, or logs across every status and both output modes (PRD R7; design Implementation Approach step 3).
- [ ] A read-only test hashes state.json, the workspace config, and the provider-auth file before and after a run and asserts byte-for-byte identity (PRD R6, AC 13).
- [ ] A test runs the doctor to a full report in a workspace where apply has never run (PRD R11, AC 8), and a two-broken-pairs fixture shows both findings in one run (PRD R12, AC 9).
- [ ] A `@critical` Gherkin scenario in `test/functional/features/` covers the all-OK (exit 0), one-broken-pair (exit 1), and unreachable-vault (exit 2) runs, pinning the 0/1/2 contract (PRD R10, AC 14; design Implementation Approach step 4 and the repo's functional-testing convention).

**Dependencies**: Blocked by <<ISSUE:5>>
**Type**: code
**Files**: `internal/workspace/providerauthcheck_test.go`, `internal/cli/vault_check_test.go`, `test/functional/features/vault_check.feature`

## Dependency Graph

_Not applicable in single-pr mode; ordering lives in the outline Dependencies fields and the Implementation Sequence._

## Implementation Sequence

The critical path is 1 → 2 → 5 → 6: types and enumeration unblock the
live-fetch layer, the command surface needs every layer it wires, and
the cross-cutting tests need the finished command.

Issues 2, 3, and 4 all depend only on issue 1 and not on each other, so
once the types land they can proceed in parallel: the vault fetch, the
file-layer check, and the renderers touch disjoint concerns (issue 2
and 3 share `providerauthcheck.go`, so coordinate edits if worked
concurrently). Issue 5 assembles them and adds the exit-code contract;
issue 6 closes with the parity, canary, read-only, and functional
proofs that span the whole stack. Per-unit tests ship inside issues
1-5; issue 6 holds only the checks that need multiple layers alive at
once.

All six issues land on one branch and merge as a single PR.
