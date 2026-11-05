Feature: niwa retask hands a dispatched worker its next task in the same session
  The holder of a dispatch handle sends follow-ups with `niwa retask` and sees
  each one acted on in the same session: the same session id, the same job
  entry, the same short id under `niwa list`, and the same mapping, byte for
  byte.

  These scenarios run the compiled binary against the copy-rule stub of
  Claude Code (internal/testutil/claudestub). The stub starts a copy of a
  session whenever a resume carries a flag, runs before a stop settles, or
  targets a running session, and continues the same session otherwise. So a
  retask that rotates the session id, passes a flag on the wake, skips the
  settle, wakes without a stop, or leaves a second job entry fails them. The
  stub encodes Claude Code's documented copy rule; nothing here runs retask
  against a real Claude Code build.

  @critical
  Scenario: niwa retask hands a dispatched worker three follow-ups in its own session
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given the copy-rule claude stub is on PATH
    When I dispatch the retask worker with "niwa dispatch --detach TASK-ONE"
    When I retask the worker by instance name with "FOLLOW-UP-ONE"
    Then the exit code is 0
    And the worker's transcript holds "TASK-ONE" then "FOLLOW-UP-ONE"
    And the worker is still one session under its dispatch handle
    When I retask the worker by instance name with "FOLLOW-UP-TWO"
    Then the exit code is 0
    And the worker's transcript ends with "FOLLOW-UP-TWO"
    And the worker is still one session under its dispatch handle
    When the retask worker is stopped with its job entry kept
    And I retask the worker by instance name with "FOLLOW-UP-THREE"
    Then the exit code is 0
    And the last retask stopped nothing
    And the worker's transcript ends with "FOLLOW-UP-THREE"
    And the worker is still one session under its dispatch handle

  # A keep-alive-armed worker reads as working while it waits, with only its
  # session_cron self-wake in flight. It is the main case retask exists for, so
  # a classifier that read it as busy would fail here with busy.
  @critical
  Scenario: niwa retask hands a keep-alive-armed worker two follow-ups in its own session
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [claude.settings]
      remoteControlAtStartup = "true"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given the copy-rule claude stub is on PATH
    And the copy-rule claude stub arms keep-alive workers
    When I dispatch the retask worker with "niwa dispatch --keep-alive --detach TASK-ONE"
    When I retask the worker by instance name with "FOLLOW-UP-ONE"
    Then the exit code is 0
    And the worker's transcript holds "TASK-ONE" then "FOLLOW-UP-ONE"
    And the worker is still one session under its dispatch handle
    And the worker's session_cron task is still listed
    When I retask the worker by instance name with "FOLLOW-UP-TWO"
    Then the exit code is 0
    And the worker's transcript ends with "FOLLOW-UP-TWO"
    And the worker is still one session under its dispatch handle
    And the worker's session_cron task is still listed
