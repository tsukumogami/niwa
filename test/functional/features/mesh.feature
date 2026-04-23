Feature: Cross-session mesh (Issue #10 harness)
  End-to-end scenarios that exercise the daemon's claim → spawn → restart /
  watchdog / reconciliation pipelines with a scripted worker fake in place
  of the real `claude -p` binary. All scenarios run under
  `make test-functional-critical` in under 10 s each by setting small-integer
  timing overrides.

  # ---------------------------------------------------------------------
  # Happy-path delegation (AC-D7, AC-D8, AC-D9).
  # ---------------------------------------------------------------------

  @critical
  Scenario: async delegation completes via fake worker finish-completed
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "async-happy" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create async-happy"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "async-happy"
    When I delegate a task to role "worker" in instance "async-happy" with body '{"kind":"unit"}'
    Then the task state in instance "async-happy" eventually becomes "completed"
    And the task transitions log in instance "async-happy" contains "state_transition"

  @critical
  Scenario: fake worker abandon flows through as abandoned outcome
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "abandon-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-abandoned"
    When I run "niwa create abandon-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "abandon-ws" with body '{"kind":"unit"}'
    Then the task state in instance "abandon-ws" eventually becomes "abandoned"
    And the task reason in instance "abandon-ws" contains "scripted-abandon"

  # ---------------------------------------------------------------------
  # Cancellation races (AC-D9, AC-Q10, AC-Q11).
  # ---------------------------------------------------------------------

  @critical
  Scenario: cancellation before claim transitions task to cancelled
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "cancel-ws" exists
    And the daemon has small timing overrides
    And the daemon pauses before claiming envelopes
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create cancel-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "cancel-ws" with body '{"kind":"unit"}'
    Then the pause marker "before_claim" eventually appears
    When I cancel the task in instance "cancel-ws"
    And I release the daemon pause marker
    Then the task state in instance "cancel-ws" eventually becomes "cancelled"

  @critical
  Scenario: cancellation racing consumption rename resolves to single terminal state (AC-Q10)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "qrace-ws" exists
    And the daemon has small timing overrides
    And the daemon pauses after claiming envelopes
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create qrace-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "qrace-ws" with body '{"kind":"unit"}'
    Then the pause marker "after_claim" eventually appears
    When I cancel the task in instance "qrace-ws"
    And I release the daemon pause marker
    Then the task state in instance "qrace-ws" eventually becomes "completed"

  @critical
  Scenario: update racing consumption rename returns too_late after claim (AC-Q11)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "update-race-ws" exists
    And the daemon has small timing overrides
    And the daemon pauses after claiming envelopes
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create update-race-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "update-race-ws" with body '{"kind":"original"}'
    Then the pause marker "after_claim" eventually appears
    When I update the task body in instance "update-race-ws" to '{"kind":"updated"}'
    Then the output contains status "too_late"
    When I release the daemon pause marker
    Then the task state in instance "update-race-ws" eventually becomes "completed"

  # ---------------------------------------------------------------------
  # Restart cap + watchdog (AC-L3, AC-L4).
  # ---------------------------------------------------------------------

  @critical
  Scenario: restart cap abandons after 4 unexpected exits (AC-L3)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "retry-cap-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "progress-then-exit-zero"
    When I run "niwa create retry-cap-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "retry-cap-ws" with body '{"kind":"retry"}'
    Then the task state in instance "retry-cap-ws" eventually becomes "abandoned"
    And the task reason in instance "retry-cap-ws" contains "retry_cap_exceeded"
    And the task restart_count in instance "retry-cap-ws" equals 3

  @critical
  Scenario: stall watchdog escalates to SIGTERM then SIGKILL (AC-L4)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "stall-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "ignore-sigterm"
    When I run "niwa create stall-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "stall-ws" with body '{"kind":"stall"}'
    Then the task transitions log in instance "stall-ws" contains "watchdog_signal"
    And the task transitions log in instance "stall-ws" contains "SIGKILL"

  # ---------------------------------------------------------------------
  # Crash recovery (AC-L9, AC-L10).
  # ---------------------------------------------------------------------

  @critical
  Scenario: daemon crash with live worker — new daemon adopts orphan (AC-L9)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "live-orphan-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "stall-forever"
    When I run "niwa create live-orphan-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "live-orphan-ws" with body '{"kind":"long"}'
    Then the task state in instance "live-orphan-ws" eventually becomes "running"
    When I SIGKILL the daemon for instance "live-orphan-ws"
    And I restart the daemon for instance "live-orphan-ws"
    Then the task transitions log in instance "live-orphan-ws" contains "adoption"

  @critical
  Scenario: daemon crash with dead worker — new daemon reclassifies as unexpected exit (AC-L10)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "dead-orphan-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "stall-forever"
    When I run "niwa create dead-orphan-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "dead-orphan-ws" with body '{"kind":"long"}'
    Then the task state in instance "dead-orphan-ws" eventually becomes "running"
    When I SIGKILL the daemon for instance "dead-orphan-ws"
    And I SIGKILL the worker for instance "dead-orphan-ws"
    And I restart the daemon for instance "dead-orphan-ws"
    Then the task transitions log in instance "dead-orphan-ws" contains "unexpected_exit"

  # ---------------------------------------------------------------------
  # Concurrent apply (AC-C3).
  # ---------------------------------------------------------------------

  @critical
  Scenario: two concurrent applies spawn exactly one daemon (AC-C3)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "concurrent-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create concurrent-ws"
    Then the exit code is 0
    When I run two concurrent applies for instance "concurrent-ws"
    Then exactly one daemon is running for instance "concurrent-ws"

  # ---------------------------------------------------------------------
  # Body-redaction regression (Decision 1 / PRD R36).
  # ---------------------------------------------------------------------

  @critical
  Scenario: daemon log does not contain envelope bodies or result payloads
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "redact-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create redact-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "redact-ws" with body '{"kind":"secret","marker":"NIWA-BODY-LEAK-CANARY"}'
    Then the task state in instance "redact-ws" eventually becomes "completed"
    And the daemon log for instance "redact-ws" does not contain "NIWA-BODY-LEAK-CANARY"

  # ---------------------------------------------------------------------
  # Authorization negative (scenario-25): unauthorized caller receives
  # NOT_TASK_PARTY. Uses a bogus task_id + wrong session role to trip the
  # executor check in auth.go.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa_finish_task with wrong task_id returns NOT_TASK_PARTY
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "auth-neg-ws" exists
    And the daemon has small timing overrides
    When I run "niwa create auth-neg-ws"
    Then the exit code is 0
    Then an unauthorized MCP call for instance "auth-neg-ws" receives NOT_TASK_PARTY
