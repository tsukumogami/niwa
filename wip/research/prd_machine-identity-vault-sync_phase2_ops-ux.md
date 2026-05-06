# Phase 2 Research: Ops/UX Perspective

## Lead 2: Audit surface design

### Findings

**Existing `--audit-secrets` implementation:**
- Location: `internal/cli/status_audit.go` (CLI command definition) and `internal/cli/status.go` (dispatcher)
- Implementation is fully offline: reads parsed workspace config and `InstanceState.Shadows` from the nearest instance state (`internal/workspace/status.go`)
- **Column shape**: KEY / CLASSIFICATION / TABLE / SHADOWED (fixed 4-column text table, aligned with padding computed from max widths)
- **Classification values**: `vault-ref` (lines 19-22), `plaintext`, `empty`, `resolved` (only when caller has pre-resolved the config)
- **Shadowed column**: "no" or "yes (personal-overlay)" or "yes (personal-overlay, scope=<scope>)" — pulled from `state.Shadows` slice (line 57)
- **Exit code semantics**: exits non-zero when plaintext values AND vault is configured (lines 75-84). This is called the "drift signal" contract for CI gates
- **Text table printing**: `printAuditTable()` at line 215 dynamically computes column widths and prints human-readable output (not JSON/structured)

**Existing shadow diagnostic during `niwa apply`:**
- Location: `internal/workspace/apply.go` lines 774-831
- **Shadow emission wording**: "shadowed <kind> <name> [personal-overlay shadows team: team=<source>, personal=<source>]" (line 827)
- **Two shadow types emitted**:
  1. Provider-level shadows (lines 783-794): "shadowed provider <name> [personal-overlay shadows team: team=workspace.toml, personal=niwa.toml]"
  2. Env/key-level shadows (lines 825-829): "shadowed env-secret <key> [personal-overlay shadows team: team=<file>, personal=<file>]"
- **Deferred output**: both use `a.Reporter.Defer()` so diagnostics batch at end-of-apply stderr
- **Shadow persistence**: collected in `pipelineShadows` slice, written to `state.Shadows` (line 273), persists across applies

**Relationship between audit and shadow:**
- The `--audit-secrets` command reads `state.Shadows` from the last apply to populate the SHADOWED column
- No new provider calls are needed: audit works fully offline from parsed config + stored state
- Shadows are ONE class of concerns (personal-overlay overrides); audit is BROADER (all *.secrets classification)

### Implications for Requirements

1. **For `--audit-auth` design**: The new credential-source audit MIRRORS the `--audit-secrets` structure exactly. It should be:
   - A TEXT TABLE (not JSON) with columns: KIND / PROJECT / SOURCE / SHADOWED
   - Fully OFFLINE (reads state.json and provider-auth.toml; NO vault fetch required)
   - CLASSIFICATIONS for SOURCE column: `vault:<vault-name>`, `local-file`, `cli-session`, `none` (when both vault and local miss)
   - SHADOWED column: "yes (local-file)" when BOTH vault and local-file entries exist for same (kind, project) pair
   - Exit code: unclear if `--audit-auth` needs a non-zero exit on credentials sourced from vault (unlike `--audit-secrets` which exits non-zero on plaintext+configured-vault). This is a design choice below.

2. **New column vs new subcommand**: The codebase shows a pattern of separate `--audit-secrets` and `--check-vault` flags (not subcommands). These are separate flags on the same `niwa status` command. By analogy, `--audit-auth` should be a THIRD flag, not a new subcommand. This keeps the mental model unified: `niwa status` is the audit/inspection surface, and flags select which audit view to run.

3. **Exit code contract**: Today's contracts are:
   - `--audit-secrets`: exit non-zero = "plaintext found AND vault configured" (user can migrate)
   - `--check-vault`: implied to exit non-zero on resolve failures
   - The PRD must decide: should `--audit-auth` exit non-zero when:
     - ANY credential is missing (neither vault nor local-file provides it)?
     - VAULT entries exist AND local-file entries OVERRIDE them (drift signal)?
     - VAULT entries exist for a given (kind, project) but the vault entry is MALFORMED (bad TOML, missing required field)?

4. **User mental model**: Secrets and auth are ONE continuum, not separate concerns. Both go through vault/local resolution. However, the audit SURFACES are separate because:
   - `--audit-secrets` enumerates env vars from the config
   - `--audit-auth` enumerates (kind, project) pairs niwa needs auth for (derived from resolved vault registries, not config)
   - They should live as separate flags to avoid cognitive overload

### Open Questions

1. Should `--audit-auth` trigger a vault fetch to verify that vault entries exist and are well-formed, or work fully offline from state.json + provider-auth.toml? (The scope doc says in-memory only per apply, but audit could be different.)
   - **Implication**: If offline, the audit can't detect case #3 above (malformed vault entries). If online, it adds latency and network dependency.

2. What is the exit-code contract for `--audit-auth`? Should it signal "drift" (vault entry overridden locally) with non-zero exit, for CI gates that enforce "credentials must come from vault"? Or is non-zero reserved for "missing credentials" only?

3. Should the SHADOWED indicator show both (vault-name AND local-file) in the presence of a conflict, e.g., "yes (vault:infisical + local-file:provider-auth.toml)"? Or just indicate that a shadow exists, "yes (local-file wins)"?

---

## Lead 5: Failure-mode UX

### Findings

**Vault error sentinel classification:**
- Location: `internal/vault/errors.go`
- Two primary sentinels exist:
  1. `vault.ErrKeyNotFound`: backend confirms key does not exist
  2. `vault.ErrProviderUnreachable`: auth failure, network down, missing CLI, expired session (lines 11-16)

**Resolver error handling per mode:**
- Location: `internal/vault/resolve/resolve.go` lines 519-561
- **On ErrKeyNotFound (line 526)**:
  - If `?required=false`: silent downgrade to empty (line 528-529)
  - Else if `--allow-missing-secrets`: warning printed to stderr (line 532-535), downgrade to empty
  - Else: **hard error** with remediation text naming the provider and key, suggesting three paths: "declare it in the provider", "mark ?required=false", or "re-run --allow-missing-secrets" (lines 539-544)
  - **Exit semantics**: hard error → apply fails, non-zero exit
- **On ErrProviderUnreachable (line 550)**:
  - NO downgrade option (even with --allow-missing-secrets)
  - **Hard error** with wording "provider %q unreachable while resolving key %q: <wrapped error>" (lines 551-554)
  - **Exit semantics**: hard error → apply fails, non-zero exit
  - Error is wrapped via `secret.Errorf()` to scrub sensitive fragments already registered on context redactor

**Infisical auth-specific errors:**
- Location: `internal/vault/infisical/auth.go` lines 33-108
- HTTP response parsing (lines 88-95):
  - Non-200 status → "infisical auth: universal-auth login returned HTTP <code>: <scrubbed-response-body>" (line 91-94)
  - Response body is scrubbed via `scrubResponseBody()` which uses context redactor + direct string replacement (lines 113-118)
  - Does NOT distinguish 401 (invalid client_secret) from 5xx (server error) in the outer message, but HTTP status code is visible to user
- Missing accessToken in response (lines 100-105): "infisical auth: response missing accessToken field"
- Request creation/marshalling errors (lines 63-75): "infisical auth: <operation>: <error>"

**Materialization and permission checks:**
- Location: `internal/workspace/snapshotwriter.go` (implied from vault-integration guide)
- File creation failure: hard error, not warnings
- File permission enforcement: 0o600 always, checked via `CheckFilePermissions()` before read

**No vault-sync-specific error handling yet:**
- The codebase has no yet-implemented code for the proposed credential-sync feature
- Vault unreachability and key misses are handled uniformly across all vault URI resolution paths today
- There is no "bootstrap case" handling (fresh machine with no local provider-auth.toml entries)

### Implications for Requirements

1. **Vault unreachable (network down, CLI not installed, not logged in)**:
   - **Current behavior**: Hard error, non-zero exit, no fallback
   - **For credential-sync**: A credential-sync feature MUST decide: should unreachability of the personal vault be a hard blocker (apply fails) or downgrade to local-file-only + warning?
     - Hard blocker = high confidence in vault as source of truth
     - Soft downgrade = resilient workflow that can proceed if some credentials are local
   - **Wording suggestion** (following existing patterns): "personal vault provider %q unreachable while fetching credentials; re-run --allow-missing-secrets to proceed with local-file-only credentials"
   - **Exit code**: Stays non-zero

2. **Conventional credential key is absent in vault**:
   - **Current behavior**: Hard error with three remediation paths
   - **For credential-sync**: Should be DOWNGRADED to warning (apply continues) since vault is supplementary, not required. The local-file layer acts as fallback.
   - **Wording suggestion**: "warning: credential key /niwa/provider-auth/<project-uuid> not found in personal vault for (kind=<k>, project=<p>); using local-file entry if available"
   - **Bootstrap case**: User is setting up fresh machine, hasn't populated vault yet. Expect many keys missing. Should emit warnings, not hard error, so apply completes and user sees progress.

3. **Body is malformed (TOML parse error, missing required field)**:
   - **Current behavior**: No precedent in codebase (vault entries are opaque secret.Value today, not parsed)
   - **For credential-sync**: A new concern. Must parse the TOML body to extract client_id/client_secret/api_url.
   - **Wording suggestion**: "credential entry /niwa/provider-auth/<project-uuid> in personal vault has malformed TOML: <parse-error>; ignoring entry and falling back to local-file/cli-session"
   - **Exit code**: Should be warning (non-zero only if --audit-auth is explicit), not hard error, to match "vault is supplementary" principle

4. **Body is well-formed but credentials are invalid (rotated, expired)**:
   - **Current behavior**: Infisical returns HTTP 401 → "infisical auth: universal-auth login returned HTTP 401: <response body>" (no specific sentinel)
   - **For credential-sync**: This is the hardest case. Vault has the entry, it parses, but authentication fails. Is this:
     - A vault-sourced credential that's stale (user rotated secret in Infisical, state not updated)? → Hard error (user must fix vault entry)
     - A local-file credential that's stale (user rotated, forgot to update provider-auth.toml)? → Hard error (user must fix local file)
     - The personal vault itself not providing the credential (bootstrapping case)? → Warning (expected, fall through to other layers)
   - **Wording suggestion**: "infisical auth: universal-auth login failed for vault credential (kind=<k>, project=<p>): HTTP 401 unauthorized; verify the credential entry in personal vault has not been rotated"
   - **Exit code**: Hard error (authentication failure cannot be recovered without user intervention)

5. **User opted into credential sync but no `[global.vault.provider]` declared**:
   - **Current behavior**: No code yet; parser would reject this in config validation
   - **For credential-sync**: Config validation at parse time should catch and emit: "credential-sync is enabled (machine_identities.from = \"<name>\") but no vault provider named \"<name>\" is declared in [global.vault.provider] or [global.vault.providers.<name>]; either declare the provider or remove the credential-sync opt-in"
   - **Exit code**: Hard error at apply time (config validation failure)

### Open Questions

1. **Resilience vs strictness**: Should the personal vault being unreachable be a hard blocker or a soft downgrade to local-file-only? The scope doc says "vault unreachable" is in-scope failure mode, but doesn't specify the user-facing contract (error vs warning).
   - **Design choice needed**: Define a "resilience mode" where missing vault entries downgrade to warnings if local-file entries exist, vs "strict mode" where any vault miss is an error.

2. **Rotation detection**: When a user rotates a Infisical client_secret in the vault but doesn't update provider-auth.toml, how does niwa detect and surface this? Is there a "verify-credentials" mode that checks that vault-sourced entries are actually usable before apply proceeds?
   - **Implication**: The PRD should define if there's a `niwa vault verify` subcommand or if rotation failures are only surfaced when apply runs and hits the 401.

3. **Bootstrap case hardening**: Explicitly define the expected behavior and messaging when:
   - User has opted into credential-sync
   - The personal vault is reachable
   - But the personal vault has NO credential entries yet (user hasn't populated them)
   - Should multiple warnings emit (one per missing credential) or a single aggregate "no credentials found in vault" message?

---

## Summary

The existing `niwa status --audit-secrets` command is fully offline, text-table based, and exits non-zero when plaintext values are found AND a vault is configured. For `--audit-auth`, the design should follow the same pattern: a fourth flag on `niwa status` that enumerates (kind, project) pairs and their credential sources (vault / local-file / cli-session / none), with a SHADOWED indicator when both vault and local-file entries exist. The exit-code contract for credential drift (vault overridden locally) and missing credentials must be decided by the PRD. Failure modes (unreachable vault, missing keys, malformed bodies, invalid credentials) should mostly downgrade to warnings with specific wording that mirrors the existing resolver patterns, except for authentication failures (HTTP 401) which remain hard errors. The bootstrap case (opted in, vault reachable, but empty) requires explicit UX definition to avoid overwhelming users with warnings.

