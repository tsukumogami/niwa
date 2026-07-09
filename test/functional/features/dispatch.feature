Feature: niwa dispatch: provision, rollback, and reaper reclamation
  End-to-end scenarios for `niwa dispatch` using a local bare-repo server as a
  fake remote and a FAKE `claude` on PATH. No real claude, no daemon, and no
  network are required.

  dispatch creates a fresh ephemeral instance, launches a `claude --bg` worker
  rooted in it, captures the worker's session UUID by jobs-dir cwd correlation,
  and records an ephemeral dispatch-origin mapping keyed on the UUID. Any failure
  before the mapping is durable rolls the instance back. The name+TTL reaper
  backstop (keyed on the dispatch instance name, so a SIGKILL before the marker
  is written still leaves a reclaimable orphan) and the liveness-rule sweep
  reclaim the instance once its session is deleted (its job entry disappears); a
  session that merely finished a task or went idle keeps its instance.

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

  # --- Model selection resolves a category to a concrete model ---

  @critical
  Scenario: dispatch resolves a capability category and forwards it to the worker
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
    When I run "niwa dispatch model-task --model powerful --detach" from the workspace root
    Then the exit code is 0
    And the launched claude was invoked with "--model opus"

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

  # --- Reaper reclaims a deleted dispatch session ---

  @critical
  Scenario: niwa reap reclaims a dispatch instance after its session is deleted
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
    When the dispatch session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" is deleted from the Agent View
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

  # --- Reaper spares a live worker whose mapping was lost (backstop liveness) ---
  # Regression guard for the data-loss bug. An UNMAPPED, past-TTL dispatch
  # instance that a live worker is still rooted in must NOT be reclaimed by the
  # name+TTL backstop. Before the fix the backstop keyed on name + age alone,
  # with no liveness check, and deleted exactly this shape -- including the
  # caller's own instance mid-dispatch, which vanished its cwd and then broke the
  # follow-on provisioning clone. The live worker's job-state cwd is the instance
  # dir, so the reaper's mapping-independent liveness guard spares it.

  @critical
  Scenario: niwa reap spares an unmapped dispatch instance whose worker is still live
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa dispatch keep-live --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch-origin mapping is removed
    And the dispatch instance is aged past the backstop TTL
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
