Feature: niwa session attach + detach (Issue #117)
  End-to-end scenarios that exercise `niwa session attach` and
  `niwa session detach` against the compiled `niwa` binary. These
  scenarios cover the parts of the attach UX that don't require
  spawning a real Claude Code process: the AVAILABILITY column
  rendering on `niwa session list`, and the detach-no-op path for
  sessions with no live lock. The full attach pipeline (lock acquire,
  daemon terminate, claude --resume) is covered by unit tests in
  internal/cli/sessionattach.

  # ---------------------------------------------------------------------
  # AVAILABILITY column appears for created sessions (PRD AC18).
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa session list renders the AVAILABILITY column for an unattached session
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "attach-list" exists
    When I run "niwa create attach-list"
    Then the exit code is 0
    When I call niwa_create_session for repo "app" with purpose "attach-list-fixture" in instance "attach-list"
    Then the last session is active in instance "attach-list"
    When I run "niwa session list --status active" from channeled instance "attach-list"
    Then the exit code is 0
    And the output contains "SESSION_ID"
    And the output contains "AVAILABILITY"
    And the output contains "available"
    # Cleanup: destroy the session so the daemon stops cleanly.
    When I call niwa_destroy_session in instance "attach-list"
    Then the session is ended in instance "attach-list"

  # ---------------------------------------------------------------------
  # Detach is silently no-op when no lock is held (PRD AC15 inverse).
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa session detach is silent no-op when no lock exists
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "attach-detach-noop" exists
    When I run "niwa create attach-detach-noop"
    Then the exit code is 0
    When I call niwa_create_session for repo "app" with purpose "detach-noop-fixture" in instance "attach-detach-noop"
    Then the last session is active in instance "attach-detach-noop"
    # Detach against a session that has no attach.state sentinel returns 0
    # silently. The session id placeholder is substituted at runtime via the
    # last-session step.
    When I run niwa session detach for the last session in instance "attach-detach-noop"
    Then the exit code is 0
    # Cleanup
    When I call niwa_destroy_session in instance "attach-detach-noop"
    Then the session is ended in instance "attach-detach-noop"

  # ---------------------------------------------------------------------
  # SESSION_ATTACHED gate on niwa_destroy_session when a live attach lock
  # is held (PRD R13 / AC23). Maps to the CUJ where the operator has
  # stepped into a session and the coordinator must back off, not destroy.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa_destroy_session returns SESSION_ATTACHED when a live attach lock exists
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "destroy-gate" exists
    When I run "niwa create destroy-gate"
    Then the exit code is 0
    When I call niwa_create_session for repo "app" with purpose "destroy-gate-fixture" in instance "destroy-gate"
    Then the last session is active in instance "destroy-gate"
    # Simulate a human attached to the session by seeding an attach.state
    # sentinel that points at the live test process. The destroy MCP tool
    # must refuse with the SESSION_ATTACHED error per PRD R13.
    When I seed a live attach sentinel for the last session in instance "destroy-gate"
    When I call niwa_destroy_session without force in instance "destroy-gate"
    Then the last MCP response contains code "SESSION_ATTACHED"
    And the last MCP response contains code "niwa session detach"
    # Force destroy bypasses the gate and proceeds with teardown per PRD AC24.
    When I call niwa_destroy_session in instance "destroy-gate"
    Then the session is ended in instance "destroy-gate"
