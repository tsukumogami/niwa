Feature: niwa onboard: team/individual vault setup wizard
  End-to-end scenarios for `niwa onboard`, driven against the hermetic
  Infisical management REST double and the on-PATH `infisical` CLI
  stub -- no real service or developer login. The happy path and the
  TTY-decline path are `@critical` so the core flow is gate-checked on
  every PR; the re-run scenarios (complete-setup-goes-straight-to-
  verification, partial resume, topology-change re-mint) compose from
  the same seeding and are not gate-checked, to keep the required PR
  path fast.

  PRD: docs/prds/PRD-niwa-onboard.md
  Design: docs/designs/DESIGN-niwa-onboard.md

  Background:
    Given a clean niwa environment
    And a workspace "acme" exists with body:
      """
      [workspace]
      name = "acme"

      [vault.provider]
      kind = "infisical"
      project = "team-proj-1"
      identity_id = "ident-1"
      identity_name = "Test Identity"
      env = "dev"
      """
    And a personal overlay exists with body:
      """
      [global.vault.provider]
      kind = "infisical"
      project = "personal-proj-1"
      """
    And the personal-overlay pointer is registered as "acme/niwa-overlay"
    And the personal overlay repo is git-initialized
    And an infisical REST double is configured
    And I set env "NIWA_INFISICAL_OPERATOR_TOKEN" to "test-operator-token"

  # --- AC-31: individual-setup happy path, both doubles, no real service ---

  @critical
  Scenario: individual-setup happy path drives both doubles and stores a verified credential
    Given the infisical REST double has identity "ident-1" with client_id "client-abc"
    And the infisical REST double mints client_secret "minted-secret-1" with secret id "secret-id-1" for identity "ident-1"
    And the infisical REST double exchanges client_secret "minted-secret-1" for access token "access-token-1"
    And the infisical REST double serves environment secrets for project "team-proj-1" env "dev"
    When I run "niwa onboard --individual --same-login --accept-api-url" from workspace "acme"
    Then the exit code is 0
    And the infisical REST double recorded a request containing "/v1/auth/universal-auth/identities/ident-1"
    And the infisical REST double recorded a request containing "/v1/auth/universal-auth/identities/ident-1/client-secrets"
    And the infisical REST double recorded a request containing "/v1/auth/universal-auth/login"
    And the infisical REST double recorded a request containing "/v4/secrets"

  # --- AC-32: TTY-decline path, exit 3, no state change ---

  @critical
  Scenario: operator declining the detected individual setup aborts with exit 3 and changes no state
    Given the infisical REST double has identity "ident-1" with client_id "client-abc"
    When I run "niwa onboard --accept-api-url" from workspace "acme" under a pty with input "n\n"
    Then the exit code is 3
    And the infisical REST double recorded no request containing "client-secrets"

  # --- AC-19: a completed individual setup goes straight to verification ---

  Scenario: re-run against a completed individual setup goes straight to wizard-end verification
    Given the infisical REST double has identity "ident-1" with client_id "client-abc"
    And the stored infisical secret "p-team-proj-1" at path "/niwa/provider-auth/infisical" env "dev" project "personal-proj-1" exists with body:
      """
      version = "1"
      client_id = "client-abc"
      client_secret = "already-stored-secret"
      """
    When I run "niwa onboard --accept-api-url" from workspace "acme" under a pty with input ""
    Then the exit code is 0
    And the infisical REST double recorded no request containing "client-secrets"

  # --- AC-20: a partial team setup resumes at the first incomplete step ---
  # Identity is already landed (a real REST read confirms it, no
  # prompt); the environment grant is not yet present, so the wizard
  # must skip straight past the identity step and prompt only for the
  # grant -- proving already-done steps are detected and skipped
  # rather than re-walked from the top (R15/AC-20).

  Scenario: re-run against a partially-landed team setup skips the done step and resumes at the grant step
    Given the infisical REST double has identity "ident-1" with client_id "client-abc"
    When I run "niwa onboard --team --accept-api-url" from workspace "acme" with input "\n"
    Then the infisical REST double recorded a request containing "/v1/auth/universal-auth/identities/ident-1"
    And the output does not contain "create a machine identity"
    And the output contains "grant Test Identity read access to the dev environment"

  # --- AC-21: a topology change re-mints and re-stores at the new location ---

  Scenario: re-run after a topology change re-mints and best-effort revokes the superseded secret
    Given the onboard mint record for kind "infisical" project "team-proj-1" has secret id "prior-secret-id"
    And the infisical REST double has identity "ident-1" with client_id "client-abc"
    And the infisical REST double mints client_secret "minted-secret-2" with secret id "new-secret-id" for identity "ident-1"
    And the infisical REST double exchanges client_secret "minted-secret-2" for access token "access-token-2"
    And the infisical REST double serves environment secrets for project "team-proj-1" env "dev"
    When I run "niwa onboard --individual --same-login --accept-api-url" from workspace "acme"
    Then the exit code is 0
    And the infisical REST double recorded a request containing "client-secrets/prior-secret-id/revoke"
