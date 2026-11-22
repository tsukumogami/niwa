Feature: niwa reap keeps a deleted session's instance that holds unpushed work
  A dispatched session can be deleted by hand while its instance still holds
  work nobody pushed. reap runs each such unit through the removal rule before
  it removes anything: every ref in the instance's clone is compared with the
  live refs ls-remote reports for the clone URL the workspace configuration
  gives, and a ref the clone URL doesn't show keeps the unit. A kept unit is
  printed with the check that kept it and left byte for byte as it was.

  The suite's repositories are local bare repositories reached by file URL,
  which the rule's git calls refuse outside the test build, so each scenario
  admits them with NIWA_TEST_REMOVAL_FILE_REMOTES.

  @critical
  Scenario: niwa reap keeps a deleted session's instance whose clone holds an unpushed commit
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa dispatch keep-my-work --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit is made on branch "unpushed-work" in repo "tools/myapp" of the dispatch instance
    And the dispatch session "dddddddd-dddd-4ddd-8ddd-dddddddddddd" is deleted from the Agent View
    And the dispatch instance's contents are recorded
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the dispatch instance's contents are unchanged
    And the error output names the dispatch instance
    And the error output contains "repository check"
    And the error output contains "branch unpushed-work"
    And a dispatch-origin mapping exists for session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa create" from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "branch unpushed-work"

  @critical
  Scenario: niwa reap removes a deleted session's instance whose clone holds nothing unpushed
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
    When I run "niwa dispatch nothing-to-keep --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  @critical
  Scenario: the reap before niwa dispatch keeps the unit and the dispatch still runs
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch first-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit is made on branch "unpushed-work" in repo "tools/myapp" of the dispatch instance
    And I keep the dispatch instance as the first dispatch
    And the dispatch session for the first dispatch is deleted from the Agent View
    And I run "niwa dispatch second-task --detach" from the workspace root
    Then the exit code is 0
    And the first dispatch instance still exists
    And the error output contains "branch unpushed-work"
