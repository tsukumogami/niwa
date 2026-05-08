Feature: niwa destroy contextual mode dispatch
  `niwa destroy` is a contextual command. From inside an instance, it
  destroys the enclosing instance and lands the shell at the workspace
  root via NIWA_RESPONSE_FILE. From the workspace root with a name, it
  destroys the named instance (no shell cd). From the workspace root
  with no name, it shows a picker (or destroys directly when only one
  instance exists, or deletes the empty workspace). With --force at the
  workspace root, it wipes the entire workspace after a non-pushed-work
  scan.

  Design: docs/designs/current/DESIGN-niwa-destroy.md

  # --- Critical paths: routing matrix and landing-path emit ---

  @critical
  Scenario: destroy from inside an instance lands at workspace root
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa destroy" from instance "myws" of workspace "myws"
    Then the exit code is 0
    And the instance "myws" of workspace "myws" does not exist
    And the response file contains the path to workspace "myws"

  @critical
  Scenario: destroy by name from workspace root preserves today's flow (no cd)
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    And an instance "myws-2" of workspace "myws" exists with repos ""
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa destroy myws" from workspace "myws"
    Then the exit code is 0
    And the instance "myws" of workspace "myws" does not exist
    And the response file is empty

  @critical
  Scenario: destroy with no arg from workspace root with single instance skips picker
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa destroy" from workspace "myws"
    Then the exit code is 0
    And the instance "myws" of workspace "myws" does not exist
    And the response file is empty

  @critical
  Scenario: workspace-self-destroy via --force on a clean workspace lands at parent
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa destroy --force" from workspace "myws"
    Then the exit code is 0
    And the workspace "myws" does not exist
    And the response file contains the path to the parent of workspace "myws"

  # --- Standard scenarios: rejection paths ---

  Scenario: destroy with name from inside an instance is rejected
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    When I run "niwa destroy other-name" from instance "myws" of workspace "myws"
    Then the exit code is not 0
    And the error output contains "instance name is only valid from the workspace root"
    And the instance "myws" of workspace "myws" exists

  Scenario: destroy with no arg from workspace root with multiple instances refuses without a TTY
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    And an instance "myws-2" of workspace "myws" exists with repos ""
    When I run "niwa destroy" from workspace "myws"
    Then the exit code is not 0
    And the error output contains "not running in a terminal"
    And the instance "myws" of workspace "myws" exists
    And the instance "myws-2" of workspace "myws" exists

  Scenario: destroy with unknown name from workspace root lists available instances
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    And an instance "myws-2" of workspace "myws" exists with repos ""
    When I run "niwa destroy nonexistent" from workspace "myws"
    Then the exit code is not 0
    And the error output contains "available instances:"
    And the error output contains "myws"
    And the error output contains "myws-2"
    And the instance "myws" of workspace "myws" exists
