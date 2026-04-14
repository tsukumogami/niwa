Feature: Shell navigation protocol
  After `niwa create` and `niwa go`, the shell should cd to the resulting
  directory. The CLI communicates the landing path to the shell wrapper via
  NIWA_RESPONSE_FILE when set, or stdout when absent (backward compat for
  scripts that call $(niwa go ...) directly).

  Design: docs/designs/current/DESIGN-shell-navigation-protocol.md

  Background:
    Given a clean niwa environment

  # --- Backward compat: direct binary invocation without the wrapper ---

  @critical
  Scenario: niwa go from inside a workspace prints the landing path on stdout
    Given a workspace "myws" exists
    When I run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the output contains "myws"
    And the error output contains "go: workspace root"

  @critical
  Scenario: niwa go on unknown workspace exits non-zero
    When I run "niwa go nonexistent"
    Then the exit code is not 0
    And the error output contains "not found"

  # --- Temp-file protocol: CLI writes to NIWA_RESPONSE_FILE, stdout stays clean ---

  @critical
  Scenario: NIWA_RESPONSE_FILE set routes landing path to the file
    Given a workspace "myws" exists
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the output is empty
    And the response file contains the path to workspace "myws"

  @critical
  Scenario: NIWA_RESPONSE_FILE absent falls back to stdout
    Given a workspace "myws" exists
    When I run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the output contains "myws"

  # --- Security: the CLI rejects response file paths outside the temp dir ---

  @critical
  Scenario: NIWA_RESPONSE_FILE outside tmp is rejected
    Given a workspace "myws" exists
    And I set env "NIWA_RESPONSE_FILE" to "/etc/niwa-pwned"
    When I run "niwa go" from workspace "myws"
    Then the exit code is not 0
    And the error output contains "outside temp directory"

  @critical
  Scenario: NIWA_RESPONSE_FILE with traversal is rejected
    Given a workspace "myws" exists
    And I set env "NIWA_RESPONSE_FILE" to "/tmp/../etc/niwa-pwned"
    When I run "niwa go" from workspace "myws"
    Then the exit code is not 0
    And the error output contains "outside temp directory"

  # --- End-to-end wrapper: source shell-init, run niwa go, verify cd happened ---

  @critical
  Scenario: sourced bash wrapper cds into workspace root
    Given a workspace "myws" exists
    When I source the bash wrapper and run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the wrapped shell ended in workspace "myws"

  @critical
  Scenario: sourced bash wrapper preserves niwa exit code on failure
    When I source the bash wrapper and run "niwa go nonexistent"
    Then the exit code is not 0
    And the wrapped shell did not change directory

  Scenario: sourced wrapper does not leave a temp file behind
    Given a workspace "myws" exists
    When I source the bash wrapper and run "niwa go" from workspace "myws"
    Then the exit code is 0
    # The wrapper creates its own temp file via mktemp, not via the env var
    # we'd set from a scenario. Verifying no leftover in .niwa-test/home/.
    And the home file "niwa-response-leaked" does not exist

  # --- Non-cd commands pass through the wrapper unchanged ---

  Scenario: wrapper delegates non-cd commands directly
    When I source the bash wrapper and run "niwa --help"
    Then the exit code is 0
    And the output contains "niwa manages multi-repo workspaces"
