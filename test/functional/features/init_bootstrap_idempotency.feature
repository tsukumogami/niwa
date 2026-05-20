Feature: niwa init --bootstrap preflight conflict surfaces (R8)
  R8 specifies bootstrap-aware preflight errors when the target name or
  directory is already in use. Sub-cases:
    1. Workspace exists (a previous bootstrap run completed).
    2. Registry name is already in use but points elsewhere.
    3a. Non-niwa file at <cwd>/<name>.
    3b. Non-niwa directory at <cwd>/<name>.
    3c. Symlink at <cwd>/<name>.

  Each sub-case asserts the user-visible Detail / Suggestion substrings
  so a wrong-arm regression is caught immediately. We use the GitHub
  fake to ensure --bootstrap reaches the preflight gate (R9 host check
  passes, then preflight fires before any clone).

  PRD: docs/prds/PRD-init-bootstrap-empty-source.md

  # --- R8 sub-case 3b: directory at target ---

  @critical
  Scenario: R8 non-niwa directory at target surfaces (directory) qualifier
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake serves "owner/foo" at ref "HEAD" empty
    And I pre-create directory "bar"
    When I run "niwa init bar --from owner/foo --bootstrap" from workspace root
    Then the exit code is not 0
    And the error output contains "already exists (directory)"

  # --- R8 sub-case 1: workspace exists ---
  # Pre-plant a .niwa/workspace.toml so preflight routes to
  # ErrWorkspaceExists (more specific than ErrTargetDirExists).

  @critical
  Scenario: R8 workspace exists at target surfaces ErrWorkspaceExists (R6 routing)
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake serves "owner/foo" at ref "HEAD" empty
    And a workspace "bar" exists
    When I run "niwa init bar --from owner/foo --bootstrap" from workspace root
    Then the exit code is not 0
    And the error output contains "Use niwa apply"
