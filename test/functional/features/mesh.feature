Feature: Session mesh: filesystem-based inter-session messaging
  End-to-end scenarios for the niwa session mesh. Two sessions register under
  the same instance root and exchange messages via the filesystem inbox.

  @critical
  Scenario: two sessions exchange a message via the filesystem inbox
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    And a sessions.json entry exists for role "coordinator"
    And the coordinator inbox directory exists
    When I run "niwa session register" as role "worker"
    Then the exit code is 0
    And a sessions.json entry exists for role "worker"
    When the worker session sends a "task.delegate" message to "coordinator" with body "hello"
    Then the exit code is 0
    And the coordinator inbox contains 1 message
    When the coordinator session checks messages
    Then the output contains "task.delegate"
    And the output contains "hello"
