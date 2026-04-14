Feature: Shell navigation protocol
  After `niwa create` and `niwa go`, the shell should cd to the resulting
  directory. The CLI communicates the landing path to the shell wrapper via
  NIWA_RESPONSE_FILE when set, or stdout when absent (backward compat for
  scripts that call $(niwa go ...) directly).

  Design: docs/designs/current/DESIGN-shell-navigation-protocol.md

  Background:
    Given a clean niwa environment

  # --- Primary: sourced wrapper actually moves the shell ---
  #
  # These scenarios are the headline proof that the feature works. The
  # wrapper loads via `eval "$(niwa shell-init bash)"`, the command runs,
  # and we read pwd out-of-band via a stderr sentinel to confirm the cd
  # actually fired. Any regression in the wrapper template, the CLI
  # protocol writer, or the env-var plumbing will fail these.

  @critical
  Scenario: sourced wrapper cds into workspace root
    Given a workspace "myws" exists
    When I source the bash wrapper and run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the wrapped shell ended in workspace "myws"

  @critical
  Scenario: wrapper preserves niwa exit code on failure and keeps pwd
    When I source the bash wrapper and run "niwa go nonexistent"
    Then the exit code is not 0
    And the wrapped shell did not change directory

  @critical
  Scenario: wrapper navigates even when subprocess writes noise to stdout
    # This is the feature's raison d'etre: the previous stdout-capture
    # protocol broke whenever a subprocess wrote to stdout. The wrapper
    # script injects stdout noise before the real niwa runs, proving
    # the temp-file channel is stdout-independent.
    Given a workspace "myws" exists
    When I source the noisy bash wrapper and run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the wrapped shell ended in workspace "myws"

  Scenario: wrapper does not leave a temp file behind after navigation
    Given a workspace "myws" exists
    When I source the bash wrapper and run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the wrapped shell ended in workspace "myws"
    And no niwa temp files remain in the system temp directory

  Scenario: wrapper delegates non-cd commands without changing directory
    When I source the bash wrapper and run "niwa --help"
    Then the exit code is 0
    And the output contains "niwa manages multi-repo workspaces"
    And the wrapped shell did not change directory

  # --- Protocol contract (used by the wrapper) ---
  #
  # These scenarios exercise the binary directly — no wrapper, so pwd
  # can't be asserted. They lock the protocol-writer contract the wrapper
  # depends on: file format on success, stdout fallback when the env
  # var is absent (preserves $(niwa go ...) scripts).

  @critical
  Scenario: NIWA_RESPONSE_FILE receives the landing path, stdout stays empty
    Given a workspace "myws" exists
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the output is empty
    And the response file contains the path to workspace "myws"

  @critical
  Scenario: NIWA_RESPONSE_FILE absent prints path on stdout (backward compat)
    Given a workspace "myws" exists
    When I run "niwa go" from workspace "myws"
    Then the exit code is 0
    And the output contains "myws"

  @critical
  Scenario: niwa go on unknown workspace exits non-zero
    When I run "niwa go nonexistent"
    Then the exit code is not 0
    And the error output contains "not found"

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
