Feature: Live coordinator ask routing (Issue #86)
  End-to-end scenarios that verify worker questions are routed to a live
  coordinator session via the role inbox instead of spawning an ephemeral
  worker, and that the coordinator can answer via both delivery paths without
  deadlocking.

  All scenarios run under `make test-functional-critical` in under 10 s each
  using the scripted worker fake and small-integer timing overrides.

  # ---------------------------------------------------------------------
  # Scenario 1: worker question via niwa_check_messages path
  # ---------------------------------------------------------------------

  @critical
  Scenario: worker question routed to live coordinator via check_messages
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "ask-check-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "ask-and-finish"
    When I run "niwa create ask-check-ws"
    Then the exit code is 0
    And the coordinator session is registered in instance "ask-check-ws"
    When I delegate a task to role "worker" in instance "ask-check-ws" with body '{"kind":"unit"}'
    And the coordinator answers the question for instance "ask-check-ws" via check_messages
    Then the task state in instance "ask-check-ws" eventually becomes "completed"

  # ---------------------------------------------------------------------
  # Scenario 2: worker question interrupts niwa_await_task (deadlock fix)
  # ---------------------------------------------------------------------

  @critical
  Scenario: worker question interrupts coordinator blocked on niwa_await_task
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "ask-await-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "ask-and-finish"
    When I run "niwa create ask-await-ws"
    Then the exit code is 0
    And the coordinator session is registered in instance "ask-await-ws"
    When I delegate a task to role "worker" in instance "ask-await-ws" with body '{"kind":"unit"}'
    And the coordinator blocks on niwa_await_task and handles questions for instance "ask-await-ws"
    Then the task state in instance "ask-await-ws" eventually becomes "completed"

  # ---------------------------------------------------------------------
  # Scenario 3: fallback to daemon spawn when no live coordinator
  # ---------------------------------------------------------------------

  @critical
  Scenario: worker ask falls back to daemon spawn when no live coordinator
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "ask-spawn-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "ask-roundtrip"
    When I run "niwa create ask-spawn-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "ask-spawn-ws" with body '{"kind":"unit"}'
    Then the task state in instance "ask-spawn-ws" eventually becomes "completed"
