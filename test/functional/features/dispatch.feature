Feature: niwa dispatch: provision, rollback, and reaper reclamation
  End-to-end scenarios for `niwa dispatch` using a local bare-repo server as a
  fake remote and a FAKE `claude` on PATH. No real claude, no daemon, and no
  network are required.

  dispatch creates a fresh ephemeral instance, launches a `claude --bg` worker
  rooted in it, captures the worker's session UUID by jobs-dir cwd correlation,
  and records an ephemeral dispatch-origin mapping keyed on the UUID. Any failure
  before the mapping is durable rolls the instance back. The marker+TTL reaper
  backstop and the existing liveness-rule sweep reclaim the instance once its
  session ends.

  The fake claude writes $HOME/.claude/jobs/<short>/state.json carrying the
  chosen UUID and the launch cwd (the instance dir, which dispatch sets via
  cmd.Dir), so the capture path resolves it. The functional sandbox points HOME
  into a per-scenario directory, so the jobs dir is hermetic.

  Design: docs/designs/DESIGN-instance-dispatch.md

  # --- Provision + map on a successful dispatch ---

  @critical
  Scenario: dispatch provisions an instance and records a dispatch-origin mapping
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    And a dispatch-origin mapping exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

  # --- Rollback on launch failure ---

  @critical
  Scenario: a launch failure rolls the dispatch instance back
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that fails to launch
    When I run "niwa dispatch doomed-task --detach" from the workspace root
    Then the exit code is not 0
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  # --- Reaper reclaims a terminated dispatch session ---

  @critical
  Scenario: niwa reap reclaims a dispatch instance after its session goes terminal
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I run "niwa dispatch reap-me --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" goes terminal
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  # --- Reaper spares a live dispatch session ---

  @critical
  Scenario: niwa reap spares a dispatch instance whose session is still live
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    When I run "niwa dispatch keep-me --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And a dispatch-origin mapping exists for session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
