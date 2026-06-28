Feature: Ephemeral per-session instances: provision, keep-while-resumable, and delete-only reaping
  End-to-end scenarios for the "one Claude Code session == one ephemeral niwa
  instance" workflow using a local bare-repo server as a fake remote. No GitHub
  access required.

  niwa init (from a config repo) opts the workspace root into ephemeral-session
  mode by default. A dispatched background session fires the workspace-root
  SessionStart hook, which niwa routes to `niwa instance from-hook`: it
  provisions a dedicated instance, records a session->instance mapping, and
  injects context.

  Teardown is delete-only and reaper-driven. SessionEnd is NOT a teardown
  trigger (it fires on idle-suspend, not uniquely on delete), so the instance
  survives task completion, idle, and suspension. `niwa reap` reclaims an
  instance only once its session's job entry is gone -- the proxy for the
  developer deleting the session from the Agent View.

  The SessionStart guard and the reaper read the session's Claude Code job
  state from ~/.claude/jobs/<session-id>/state.json. The functional sandbox
  points HOME into a per-scenario directory, so the job-state fixture is
  hermetic and never touches the developer's real ~/.claude.

  Design: docs/designs/current/DESIGN-ephemeral-session-instances.md

  # --- A finished/idle session keeps its instance ---
  # A dispatched background session (template "bg") passes the SessionStart
  # guard, so niwa provisions "myws-<session-prefix>" and writes the mapping.
  # SessionEnd is a no-op -- it does NOT reclaim the instance -- and `niwa reap`
  # spares the instance while its job entry is still present (the session is
  # idle-but-resumable). This is the regression guard for the reaped-on-
  # completion / reaped-on-idle bug.

  @critical
  Scenario: SessionEnd and reap keep a still-resumable session's instance
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
    And the instance "myws-aaaaaaaa-aaa" exists
    And the session mapping exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the instance "myws-aaaaaaaa-aaa" exists
    And the session mapping exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

  # --- Reaper reclaims an instance whose session was deleted ---
  # The session is provisioned, then deleted from the Agent View (its job entry
  # disappears). The instance and mapping linger until `niwa reap` runs, which
  # finds the gone job entry by the liveness rule and reclaims the orphan.

  @critical
  Scenario: niwa reap reclaims an ephemeral instance whose session was deleted
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
    When the session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the instance "myws-bbbbbbbb-bbb" does not exist
    And the session mapping does not exist for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
