Feature: Ephemeral per-session instances: provision, teardown, and reaping
  End-to-end scenarios for the "one Claude Code session == one ephemeral niwa
  instance" workflow using a local bare-repo server as a fake remote. No GitHub
  access required.

  niwa init (from a config repo) opts the workspace root into ephemeral-session
  mode by default. A dispatched background session fires the workspace-root
  SessionStart hook, which niwa routes to `niwa instance from-hook`: it
  provisions a dedicated instance, records a session->instance mapping, and
  injects context. SessionEnd tears that instance down. `niwa reap` is the
  backstop that reclaims an instance whose session died without firing
  SessionEnd.

  The SessionStart guard and the reaper read the session's Claude Code job
  state from ~/.claude/jobs/<session-id>/state.json. The functional sandbox
  points HOME into a per-scenario directory, so the job-state fixture is
  hermetic and never touches the developer's real ~/.claude.

  Design: docs/designs/DESIGN-ephemeral-session-instances.md

  # --- Provision on SessionStart, tear down on SessionEnd ---
  # A dispatched background session (template "bg" in its job state) passes the
  # SessionStart guard, so niwa provisions "myws-<session-prefix>" and writes the
  # mapping. The matching SessionEnd resolves that instance by session_id and
  # force-destroys it, removing the mapping.

  @critical
  Scenario: SessionStart provisions an instance and SessionEnd tears it down
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a background job state exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I pipe a SessionStart hook for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    Then the exit code is 0
    And the instance "myws-aaaaaaaa-aaa" exists
    And the session mapping exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I pipe a SessionEnd hook for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    Then the exit code is 0
    And the instance "myws-aaaaaaaa-aaa" does not exist
    And the session mapping does not exist for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

  # --- Reaper reclaims an orphaned ephemeral instance ---
  # The session is provisioned, then ends WITHOUT firing SessionEnd (its job
  # state disappears). The instance and mapping linger until `niwa reap` runs,
  # which finds the dead session by the liveness rule and reclaims the orphan.

  @critical
  Scenario: niwa reap reclaims an ephemeral instance whose session died
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a background job state exists for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    Then the exit code is 0
    And the instance "myws-bbbbbbbb-bbb" exists
    When the session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" has ended without firing SessionEnd
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the instance "myws-bbbbbbbb-bbb" does not exist
    And the session mapping does not exist for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
