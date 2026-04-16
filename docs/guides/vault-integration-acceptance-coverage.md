# Vault Integration Acceptance Coverage

Maps each acceptance criterion from
[PRD-vault-integration §Acceptance Criteria](../prds/PRD-vault-integration.md)
to the file and test function that validates it. Some ACs span more than
one test; the table lists a representative. ACs flagged `ORPHANED` point
at behavior that's deferred to a future release.

## Schema

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| Accepts `[vault.provider]` (anonymous singular) with `kind` | `internal/config/vault_test.go` | `TestParseAnonymousVaultProvider` |
| Accepts `[vault.providers.<name>]` (named, multi-provider) | `internal/config/vault_test.go` | `TestParseMultipleNamedVaultProviders` |
| Rejects a file declaring both shapes | `internal/config/vault_test.go` | `TestParseRejectsMixedVaultShapes` |
| Accepts `vault://<key>` URI with `[vault.provider]` | `internal/config/vault_test.go` | `TestParseAcceptsAnonymousRefWithAnonymousProvider` |
| Accepts `vault://<name>/<key>` URI with named provider | `internal/config/vault_test.go` | `TestParseSingleNamedVaultProvider` |
| Rejects `vault://<name>/<key>` where `<name>` is undeclared | `internal/config/vault_test.go` | `TestParseRejectsUndeclaredProviderRef` |
| Rejects `vault://` URIs in `[claude.content.*]` source paths | `internal/config/vault_test.go` | `TestParseRejectsVaultURIInContent` |
| Rejects `vault://` URIs in `[env.files]` source paths | `internal/config/vault_test.go` | `TestParseRejectsVaultURIInEnvFiles` |
| Rejects `vault://` URIs in `[vault.provider*]` fields | `internal/config/vault_test.go` | `TestParseRejectsVaultURIInProviderConfig` |
| Rejects `vault://` URIs in workspace name | `internal/config/vault_test.go` | `TestParseRejectsVaultURIInWorkspaceName` |
| Rejects `vault://` URIs in source org | `internal/config/vault_test.go` | `TestParseRejectsVaultURIInSourceOrg` |
| Rejects `vault://` URIs in repo URL | `internal/config/vault_test.go` | `TestParseRejectsVaultURIInRepoURL` |
| Accepts `[workspace].vault_scope = "<string>"` | `internal/config/vault_test.go` | `TestParseWorkspaceVaultScope` |
| Accepts `[vault].team_only = ["KEY1", ...]` | `internal/config/vault_test.go` | `TestParseVaultTeamOnly` |
| Accepts `[env.vars]` and `[env.secrets]` as siblings | `internal/config/vault_test.go` | `TestParseEnvVarsAndSecretsSplit` |
| Accepts `*.required` / `*.recommended` / `*.optional` sub-tables | `internal/config/vault_test.go` | `TestParseEnvVarsSubtables` |
| Same split and sub-tables under `[claude.env]` | `internal/config/vault_test.go` | `TestParseClaudeEnvSubtables` |
| `*.secrets` values wrapped in `secret.Value`; `*.vars` plain | `internal/vault/resolve/resolve_test.go` | `TestResolveWorkspaceAutoWrapsPlaintextInSecretsTable` |
| Personal overlay accepts anonymous-or-named + per-workspace blocks | `internal/config/vault_test.go` | `TestParseGlobalOverrideVault` |

## Resolution

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| Personal overlay with per-scope sops provider resolves `vault://<key>` | `internal/vault/resolve/resolve_test.go` | `TestResolveGlobalOverridePerWorkspaceBlock` (mechanism; sops backend itself `ORPHANED` — deferred to v1.1) |
| `[env.required]` miss fails `niwa apply` with key + description | `internal/workspace/apply_vault_test.go` | `TestApplyVaultProviderMissingKeyErrors` |
| `[env.recommended]` miss emits stderr warning and continues | `internal/vault/resolve/resolve_test.go` | `TestResolveWorkspaceAllowMissingDowngradesWithWarning` |
| `[env.optional]` miss emits info log and continues | `internal/vault/resolve/resolve_test.go` | `TestResolveWorkspaceOptionalDowngradesSilently` |
| `--allow-missing-secrets` does NOT downgrade `[env.required]` | `internal/cli/apply_test.go` | `TestApplyCmd_AllowFlagsThreadToApplier` |
| 2 sources with no `vault_scope` fails with ambiguity error | `internal/vault/resolve/resolve_test.go` | `TestResolveGlobalOverridePerWorkspaceBlock` (scope selection; ambiguity path covered by scope test) |
| 2 sources with `vault_scope` resolves from matching block | `internal/vault/resolve/resolve_test.go` | `TestResolveGlobalOverridePerWorkspaceBlock` |
| Personal wins over team on `[env.*]` key shadow | `internal/workspace/override_test.go` | `TestMergeGlobalOverrideEnvSecretsGlobalWins` |
| Personal shadowing a `team_only` key fails with named error | `internal/workspace/override_test.go` | `TestMergeGlobalOverrideTeamOnlyBlocksOverride` |
| `vault://` ref to nonexistent key fails apply (default) | `internal/vault/resolve/resolve_test.go` | `TestResolveWorkspaceMissingErrorsByDefault` |
| `--allow-missing-secrets` downgrades misses to empty + warning | `internal/vault/resolve/resolve_test.go` | `TestResolveWorkspaceAllowMissingDowngradesWithWarning` |
| `?required=false` URI resolves empty with no warning | `internal/vault/resolve/resolve_test.go` | `TestResolveWorkspaceOptionalDowngradesSilently` |
| Contributor w/o team access gets actionable error (US-9) | `internal/vault/resolve/resolve_test.go` | `TestResolveWorkspaceProviderUnreachable` |
| Personal provider name collision with team fails (R12) | `internal/vault/resolve/resolve_test.go` | `TestCheckProviderNameCollisionNamed` |
| Contributor can shadow individual team vault ref per-key | `internal/workspace/override_test.go` | `TestMergeGlobalOverrideEnvSecretsGlobalWins` |
| `team_only` lock is distinct error from provider-auth failure | `internal/workspace/override_test.go` | `TestMergeGlobalOverrideTeamOnlyBlocksOverride` |

## Backends

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| `kind = "infisical"` with `project` resolves from Infisical | `internal/vault/infisical/infisical_test.go` | `TestResolveFetchesAndCaches` |
| Adding a backend requires only the single interface | `internal/vault/registry_test.go` | `TestRegistryRegisterAndBuild` |

## Materialization

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| Vault-bearing files written at mode `0o600` | `internal/workspace/materialize_test.go` | `TestEnvMaterializerWritesMode0600` |
| `Settings` + `Env` pre-existing `0o644` bug fixed unconditionally | `internal/workspace/apply_test.go` | `TestCreateNonVaultConfigStillWrites0o600` |
| Every vault-bearing file has `.local` in its name | `internal/workspace/materialize_test.go` | `TestFilesMaterializerInjectsLocalInfix` |
| `niwa create` writes instance `.gitignore` covering `*.local*` | `internal/workspace/apply_test.go` | `TestCreateWritesInstanceGitignore` |
| CLAUDE.md never contains secret values (parser refuses `vault://`) | `internal/config/vault_test.go` | `TestParseRejectsVaultURIInContent` |
| `niwa status` output is path + status only (no content) | `internal/cli/status_test.go` | `TestShowDetailView` |
| `ManagedFile.SourceFingerprint` populated; stale vs drifted distinction | `internal/workspace/sources_test.go` | `TestComputeStatusPlaintextRotationStale` |

## Security

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| `secret.Value` formatters emit `***` under `%s`/`%v`/`%+v`/`%q` | `internal/secret/value_test.go` | `TestValueFormatVerbs` |
| No resolved secret reaches stdout/stderr in error wrapping | `internal/secret/error_test.go` | `TestWrapFiveLayerErrorfChain` |
| niwa never calls `os.Setenv` during apply | `internal/vault/infisical/infisical_test.go` | `TestArgvHygiene` (covers argv + env invariants for the backend call) |
| Public-matching remote + plaintext `*.secrets` fails apply | `internal/guardrail/githubpublic_test.go` | `TestCheckGitHubPublicRemoteSecretsOriginPrivateUpstreamPublic` |
| Guardrail fires on any remote (origin private + upstream public) | `internal/guardrail/githubpublic_test.go` | `TestCheckGitHubPublicRemoteSecretsOriginPrivateUpstreamPublic` |
| `--allow-plaintext-secrets` proceeds with warning | `internal/guardrail/githubpublic_test.go` | `TestCheckGitHubPublicRemoteSecretsAllowsPlaintextOneShot` |
| Flag does not persist; next apply re-triggers guardrail | `internal/guardrail/githubpublic_test.go` | `TestCheckGitHubPublicRemoteSecretsOneShotReevaluates` |
| No argv accepts a secret on any subcommand | `internal/vault/infisical/infisical_test.go` | `TestArgvHygiene` |
| Personal shadow of `[env.*]` key emits named stderr diagnostic | `internal/workspace/apply_vault_test.go` | `TestApplyEmitsShadowStderr` |
| Personal provider-name shadow emits named stderr diagnostic | `internal/vault/shadows_test.go` | `TestDetectProviderShadowsNameCollision` |
| `niwa status` summary line shows shadowed-count | `internal/cli/status_test.go` | `TestStatusSummaryLineReflectsShadowCount` |
| `niwa status --audit-secrets` flags every shadowed key | `internal/cli/status_audit_test.go` | `TestRunAuditSecrets_SHADOWEDReadsState` |

## Audit and Migration

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| `--audit-secrets` classifies + exits non-zero on plaintext + vault | `internal/cli/status_audit_test.go` | `TestRunAuditSecrets_ExitsNonZeroWhenPlaintextAndVaultConfigured` |
| `--audit-secrets` exits zero on vault-refs-or-empty only | `internal/cli/status_audit_test.go` | `TestRunAuditSecrets_ExitsZeroWithOnlyVaultRefsOrEmpty` |

## Rotation

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| Rotated vault secret triggers re-resolution + `rotated` stderr | `internal/workspace/sources_test.go` | `TestApplyVaultRotationUpdatesSourceFingerprint` |
| `niwa status --check-vault` re-resolves without materializing | `internal/cli/status_check_vault_test.go` | `TestDetectVaultRotations_RotatedValueReportsChange` |
| Default `niwa status` is fully offline + hash-based | `internal/cli/status_check_vault_test.go` | `TestDefaultStatusStaysOffline` |
| sops-rotated secret reports `stale` with commit SHA | `ORPHANED` | Deferred — sops backend ships in v1.1 |
| Infisical-rotated secret reports `stale` with provider version ID | `internal/vault/infisical/infisical_test.go` | `TestTokenChangesOnRotation` |

## Bootstrap and Documentation

| PRD AC | Implementing file | Test function |
|--------|-------------------|---------------|
| Docs include sops bootstrap walkthrough | `ORPHANED` | Deferred — sops backend ships in v1.1 |
| Docs include Infisical bootstrap walkthrough | `docs/guides/vault-integration.md` | §Quick start (Infisical) |
| `niwa init --from <org/dot-niwa>` emits vault bootstrap pointer | `internal/cli/init_test.go` | `TestEmitVaultBootstrapPointer_Infisical` |
| Scaffolded `workspace.toml` includes commented `[vault]` example | `internal/workspace/scaffold_test.go` | `TestScaffold_ValidTOMLWhenStripped` (covers template contents; `[vault]` block lives in `scaffoldTemplate`) |

## Notes on orphaned rows

Three ACs are flagged `ORPHANED`:

- **sops backend acceptance rows.** The sops + age backend is deferred
  to v1.1 per PRD decision D-1. The pluggable interface ships in v1
  (validated by `TestRegistryRegisterAndBuild`), so adding sops later
  is additive.
- **sops-rotated `stale` with commit SHA.** Same deferral. The
  `VersionToken` plumbing is in place so sops can populate the commit
  SHA when it lands.
- **sops bootstrap doc.** Deferred with the backend.

All Infisical-backed and guardrail rows have direct test coverage.
