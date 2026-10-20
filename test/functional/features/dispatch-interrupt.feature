Feature: niwa dispatch stops cleanly on SIGTERM
  A TERM or Ctrl-C to `niwa dispatch` while it provisions cancels the
  dispatch: the git commands it started are killed with their process group,
  which takes the https remote helper git leaves running when only git is
  killed, and the dispatch's own failure path removes the instance. A worker
  the dispatch already launched is never touched: the handler is released
  before the launch, and the launch is not given the context it cancels.

  The hung remote is a local port that accepts connections and never answers,
  so the clone waits on its TLS handshake until something stops it. No
  network is needed.

  Scenario: a TERM during a hung https clone leaves no git process and no instance
    Given a clean niwa environment
    And a local git server is set up
    And a never-answering https remote "stuck" is listening
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.stuck]
      url = "{repo:stuck}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "abcdabcd-abcd-4bcd-8bcd-abcdabcdabcd"
    When I start "niwa dispatch stuck-task --detach" from the workspace root in the background
    And the never-answering remote has been reached
    And niwa is sent SIGTERM
    Then niwa exits within 15 seconds
    And no process for the never-answering remote remains within 5 seconds
    And no dispatch instance remains
    And niwa was ended by SIGTERM
    And the error output contains "interrupted by SIGTERM while provisioning its instance"

  Scenario: a TERM after a detached worker is launched leaves the worker running
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake codex for dispatch whose worker keeps running
    When I start "niwa dispatch long-task --detach --harness codex" from the workspace root in the background
    And the detached worker has started
    And niwa is sent SIGTERM
    Then niwa exits within 15 seconds
    And the detached worker is still running

  Scenario: a TERM during a backgrounded hand-off does not cut the hand-off short
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch whose hand-off takes 3 seconds
    When I start "niwa dispatch slow-handoff --detach" from the workspace root in the background
    And the hand-off has started
    And niwa is sent SIGTERM
    Then niwa exits within 15 seconds
    And the hand-off finishes within 15 seconds
