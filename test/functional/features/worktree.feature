Feature: niwa worktree (renamed from niwa session)
  End-to-end scenarios for the canonical `niwa worktree` command tree and
  its backward-compatible `niwa session` alias. The worktree commands drive
  the same lifecycle as before; only the command name changed. The alias
  must keep working and must print a one-line deprecation notice to stderr.

  # ---------------------------------------------------------------------
  # Canonical create path: niwa worktree create <repo> <purpose>.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa worktree create scaffolds a worktree and lifecycle state
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wt-create" exists
    When I run "niwa create wt-create"
    Then the exit code is 0
    When I call niwa worktree create for repo "app" with purpose "wt-create-fixture" in instance "wt-create"
    Then the last session is active in instance "wt-create"
    And the session worktree exists in instance "wt-create"
    When I run "niwa worktree list --status active" from channeled instance "wt-create"
    Then the exit code is 0
    And the output contains "SESSION_ID"
    And the output contains "available"
    # Cleanup
    When I call niwa_destroy_session in instance "wt-create"
    Then the session is ended in instance "wt-create"

  # ---------------------------------------------------------------------
  # Alias contract: niwa session create still works AND warns.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa session create still works and emits the deprecation notice
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wt-alias" exists
    When I run "niwa create wt-alias"
    Then the exit code is 0
    When I call niwa_create_session for repo "app" with purpose "wt-alias-fixture" in instance "wt-alias"
    Then the last session is active in instance "wt-alias"
    And the last command stderr contains the session deprecation notice
    # Cleanup
    When I call niwa_destroy_session in instance "wt-alias"
    Then the session is ended in instance "wt-alias"
