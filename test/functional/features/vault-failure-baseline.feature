Feature: vault failure handling baseline
  These scenarios pin what provisioning does today when the Infisical CLI
  fails, and how many CLI calls a successful run makes. They were recorded
  before the vault-offline work changed any of it, alongside the golden
  fixtures in internal/vault/resolve/testdata/golden. The shared infisical
  stub stands in for the real CLI: INFISICAL_STUB_EXPORT_FAIL selects a fixed
  export failure, and its invocation log counts the calls.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [vault.provider]
      kind = "infisical"
      project = "golden-team-project"

      [env.secrets]
      GOLDEN_KEY = "vault://GOLDEN_KEY"
      FOLDER_KEY = "vault://folder/FOLDER_KEY"
      """
    And the infisical stub holds "GOLDEN_KEY" = "golden-functional-root-value" in project "golden-team-project" at path "/"
    And the infisical stub holds "FOLDER_KEY" = "golden-functional-folder-value" in project "golden-team-project" at path "/folder"
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0

  # Before failures were classified, the logged-out wording matched none of
  # the auth-failure markers, so the export failure was a hard resolver error
  # and apply failed with "infisical: export exited 1: error: No valid login
  # session found". The session probe now lists no session, the failure is
  # classified unauthenticated, and the key it blocked is a tolerated mark.
  Scenario: a logged-out export leaves a tolerated mark and niwa apply succeeds
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "no-valid-session"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output does not contain "export exited 1"
    And the error output contains "the infisical provider could not be reached"

  # A successful resolution reads each folder once and never asks the CLI
  # about its login session.
  Scenario: a successful apply makes one export per folder and no login status call
    Given the infisical stub logs its invocations
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output does not contain "no value"
    And the infisical stub logged 2 "export" invocations
    And the infisical stub logged 0 "login status" invocations
