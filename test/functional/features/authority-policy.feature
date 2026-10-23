Feature: Authority policy: declare levels, grants and roles, then inspect them
  A workspace declares its authority level, grant ceiling and roles in the
  [authority] table of its base config. The machine config can only narrow
  them, through `niwa config set authority-level` and `authority-ceiling`, and
  `niwa authority policy` shows what each layer declares and what a dispatch
  would resolve to. A bad value refuses the authority commands but only warns
  in niwa apply, so a workspace can still pull its own fix.

  @critical
  Scenario: the machine ceiling narrows the workspace ceiling, and policy says so
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [authority]
      level = "supervised"
      ceiling = ["merge-pr", "close-issue", "cut-release"]

      [authority.roles.coordinator]
      authorities = ["merge-pr", "close-issue", "cut-release"]
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa authority policy" from the workspace root
    Then the exit code is 0
    And the output contains "grant ceiling: merge-pr, close-issue, cut-release from workspace"
    And the output contains "no-flag dispatch: level supervised from workspace; grant none"
    When I run "niwa config set authority-ceiling merge-pr"
    Then the exit code is 0
    When I run "niwa config set authority-level strict"
    Then the exit code is 0
    When I run "niwa authority policy" from the workspace root
    Then the exit code is 0
    And the output contains "level ceiling: strict from machine"
    And the output contains "grant ceiling: merge-pr from machine"
    And the output contains "close-issue (outside ceiling), cut-release (outside ceiling)"
    When I run "niwa config set authority-ceiling merge-pr,nope"
    Then the exit code is not 0
    And the error output contains "merge-pr, close-issue, cut-release"

  @critical
  Scenario: an invalid authority value refuses policy but only warns in apply
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"

      [authority]
      level = "bogus"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the error output contains "invalid [authority] level"
    When I run "niwa authority policy" from the workspace root
    Then the exit code is 1
    And the error output contains "autonomous, supervised, strict"
