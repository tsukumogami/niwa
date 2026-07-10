---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-provider-auth-provision.md
milestone: "provider-auth provision command"
issue_count: 7
---

## Status

Active

Single-PR plan decomposing `DESIGN-provider-auth-provision.md` into seven
dependency-ordered outlines that `/work-on` implements on one branch. No GitHub issues or
milestone are created (single-pr mode).

## Scope Summary

Implement the `niwa provider-auth provision` command: a bounded credential-mint + verify +
store flow (Infisical machine-identity `client_secret` minting behind a provider-agnostic
`Provisioner` capability, verify-before-store, one caller-selected storage target) plus the
doc-consistency annotation on the machine-identity PRD.

## Decomposition Strategy

**Walking skeleton.** The feature is one runtime pipeline (mint -> verify -> store) whose
integration risk sits at the seams: a net-new REST surface, a redactor-on-context
precondition, and a new CLI exit-code path. The skeleton is the mint capability
(`<<ISSUE:1>>`); the verify probe, the file writer, and the orchestrator thicken it into an
end-to-end path (`<<ISSUE:2>>`-`<<ISSUE:4>>`); the command surfaces it (`<<ISSUE:5>>`); the
vault-store target and the doc annotation are the remaining independent slices
(`<<ISSUE:6>>`, `<<ISSUE:7>>`). Building the mint capability first forces the
secret-hygiene and REST-shape problems to surface before the command layer is wired.

## Issue Outlines

### Issue 1: feat(vault): add Provisioner capability and Infisical mint

**Goal**: Add a provider-agnostic `Provisioner` interface to `internal/vault` and implement
`MintClientSecret` in a new `internal/vault/infisical/provision.go`, minting a
`client_secret` on an existing identity.

**Acceptance Criteria**:
- [ ] `internal/vault` exposes a `Provisioner` interface with `MintClientSecret(ctx, MintRequest) (MintResult, error)`; `MintRequest.SessionToken` and `MintResult.ClientSecret` are `secret.Value`, not `string`.
- [ ] The Infisical impl reads the identity's `client_id` (`GET /v1/auth/universal-auth/identities/{id}`) and mints a `client_secret` (`POST .../identities/{id}/client-secrets`) with a bounded TTL, returning `client_id`, the secret, and `client_secret_id`.
- [ ] The session bearer is read from the provider env var / CLI session file and sent only as an `Authorization` header -- never on argv, never as a flag.
- [ ] The minted secret and the session token are registered with the ctx `secret.Redactor` at parse time; reuse `auth.go`'s HTTP + redactor plumbing.
- [ ] Unit tests run against a stub HTTP server, including a 401/403 response body that echoes the token/secret, asserting the value is scrubbed from the returned error.
- [ ] The command never creates an identity (no create/delete-identity call exists in the interface).

**Dependencies**: None

**Type**: code
**Files**: `internal/vault/provisioner.go`, `internal/vault/infisical/provision.go`

### Issue 2: feat(vault): verify minted credential can read the target env

**Goal**: Add the verify half -- confirm the minted pair authenticates and can read the
named target environment -- reusing existing in-package helpers.

**Acceptance Criteria**:
- [ ] Verification calls `Authenticate` with the minted pair (auth half) and `runInfisicalExport` with the resulting JWT against the target env (read half), both from within package `infisical`.
- [ ] A successful export of the target env is the pass signal; an auth failure and a not-readable failure are returned as distinct typed errors.
- [ ] No new REST endpoint is introduced for the read probe.
- [ ] Tests cover the auth-fail and env-not-readable branches against the stub server.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/vault/infisical/provision.go`

### Issue 3: feat(workspace): add WriteProviderAuth (0600 atomic writer)

**Goal**: Add a `provider-auth.toml` writer to `internal/workspace/providerauth.go` that
emits a `[[providers]]` entry at mode `0600` atomically.

**Acceptance Criteria**:
- [ ] `WriteProviderAuth` creates the temp file with `os.OpenFile(..., 0o600)` (not write-then-chmod) in the *same directory* as the target, then renames (same-fs atomic).
- [ ] It merges or replaces the entry keyed on `(kind, project)`, preserving other entries.
- [ ] A round-trip test writes with `WriteProviderAuth` and reads back with `LoadProviderAuth` (which enforces `0o600`), asserting the entry and mode.
- [ ] The written file is never world-readable at any point (no observable partial/loose-mode intermediate).

**Dependencies**: None

**Type**: code
**Files**: `internal/workspace/providerauth.go`

### Issue 4: feat(provision): orchestrate mint -> verify -> store

**Goal**: Add `internal/provision` that sequences mint, verify, and store with the redactor
precondition and verify-before-store enforced structurally.

**Acceptance Criteria**:
- [ ] `internal/provision` attaches a `secret.Redactor` to the context with `secret.WithRedactor` and registers the session token *before* the first mint call.
- [ ] The store step is structurally unreachable when verification returns an error (not merely sequenced after it); a table test asserts nothing is written on verify failure.
- [ ] It builds the Infisical provisioner directly (import `internal/vault/infisical`), since minting is identity-level and does not go through the project-scoped `Factory.Open`/`Registry.Build`.
- [ ] Each terminal outcome maps to a typed exit-code error: success, auth failure, target-not-readable, storage-write failure.
- [ ] Table tests cover the happy path and each failure branch; no secret value appears in any error string.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>

**Type**: code
**Files**: `internal/provision/provision.go`

### Issue 5: feat(cli): add provider-auth provision command + exit-code wiring

**Goal**: Add the `provider-auth` cobra parent and `provision` subcommand, and wire the
typed exit codes into `root.go`'s `Execute()`.

**Acceptance Criteria**:
- [ ] `niwa provider-auth provision` is registered via `AddCommand` with `--identity`, `--env`, `--store=file|vault` (default `file`), and `--json` flags; `--help` documents the identity, environment, and storage-target inputs.
- [ ] `internal/cli/root.go` `Execute()` gains a branch mapping the provision typed error to distinct process exit codes (success 0, auth failure, target-not-readable, storage-write failure), mirroring the existing `ExitCodeError` handling.
- [ ] `--json` success output is valid JSON carrying `identity_id`, `client_id`, `client_secret_id`, `store_target`, `env` and omitting the secret; grepping full stdout+stderr for the secret finds nothing.
- [ ] A run with all inputs supplied via flags/env reaches a terminal exit with no interactive prompt (verified with stdin closed).
- [ ] Command flags, defaults, and messages contain no provider-name-specific or workspace-specific constant.
- [ ] The mint/verify/store path invokes no external shell utility (no `curl`/`jq`/`base64` shell-out) and its logic is pure Go, so behavior is identical on Linux and macOS (PRD R11); an end-to-end test provisions against a stub and then asserts the stored credential is readable back (file target: `LoadProviderAuth` round-trip; the credential is what `niwa status --audit-auth` would resolve).

**Dependencies**: Blocked by <<ISSUE:4>>

**Type**: code
**Files**: `internal/cli/provider_auth.go`, `internal/cli/root.go`

### Issue 6: feat(provision): vault storage target + revoke-on-rotate

**Goal**: Add the `--store=vault` target (net-new provider CLI secret-set subprocess) and
revoke the prior secret on rotation.

**Acceptance Criteria**:
- [ ] `--store=vault` writes the `version = "1"` credential body via the provider CLI's secret-set operation, feeding the body over stdin or a `0600` temp file -- never on argv.
- [ ] A re-run that overwrites a stored credential revokes the prior `client_secret` when its id is recoverable from the overwritten target; when it is not, the run surfaces the new `client_secret_id` and the prior secret expires with its TTL.
- [ ] A test asserts the secret body is not present on the subprocess argv (stdin/temp-file path exercised).
- [ ] Rotation leaves niwa able to resolve secrets with the newly stored credential.

**Dependencies**: Blocked by <<ISSUE:4>>, <<ISSUE:5>>

**Type**: code
**Files**: `internal/provision/provision.go`, `internal/vault/infisical/provision.go`

### Issue 7: docs(prd): annotate machine-identity non-goal with the provision carve-out

**Goal**: Annotate `PRD-machine-identity-vault-sync.md`'s "reads only, does not auto-mint"
wording to reference the provision carve-out so the stance and its exception read together.

**Acceptance Criteria**:
- [ ] The machine-identity PRD's non-goal wording gains a note pointing at the provision carve-out (mint-on-existing-identity + write-own-config only), without contradicting the original stance.
- [ ] The annotation names `DESIGN-provider-auth-provision.md` as the source of the carve-out.
- [ ] `shirabe validate` remains clean on the edited PRD.

**Dependencies**: None

**Type**: docs
**Files**: `docs/prds/PRD-machine-identity-vault-sync.md`

## Dependency Graph

_Not applicable in single-pr mode; ordering lives in the outline Dependencies fields and the Implementation Sequence._

## Implementation Sequence

**Critical path**: `<<ISSUE:1>>` -> `<<ISSUE:2>>` -> `<<ISSUE:4>>` -> `<<ISSUE:5>>` ->
`<<ISSUE:6>>` (mint capability, then verify, then orchestration, then the command surface,
then the vault target that needs both the orchestrator and the command).

**Parallelizable**:
- `<<ISSUE:3>>` (the `provider-auth.toml` writer) is independent of `<<ISSUE:1>>`/`<<ISSUE:2>>`
  and can be built alongside them; it only has to land before `<<ISSUE:4>>`.
- `<<ISSUE:7>>` (the PRD annotation) is a docs task with no code dependency and can land at
  any point.

**Recommended order**: `<<ISSUE:1>>`, then `<<ISSUE:2>>` and `<<ISSUE:3>>` in parallel,
then `<<ISSUE:4>>`, then `<<ISSUE:5>>`, then `<<ISSUE:6>>`; `<<ISSUE:7>>` whenever
convenient. All seven land in a single PR.
