Feature: niwa dispatch keep-alive: opt-in, arming, record, and observability
  End-to-end scenarios for `niwa dispatch --keep-alive` using the local bare-repo
  server and the FAKE `claude` on PATH from the dispatch suite. No real claude,
  daemon, or network.

  Keep-alive applies to remote-control workers only. When the opt-in resolves on
  (flag > downstream > host default) and the worker starts with remote control,
  dispatch prepends a fixed self-arm instruction to the task prompt so the agent
  schedules a non-visible, sub-hourly no-op self-wake; the durable session
  mapping records the armed state and `niwa list` reports the sessions still
  being kept alive. The workers here have remote control decided DOWNSTREAM
  ([claude.settings] remoteControlAtStartup in the config repo), which the real
  materializer writes into the instance settings and dispatch reads back.

  The true bridge-reachability effect (the self-wake keeping the remote session
  reachable across a long idle) cannot be exercised offline; it was validated
  manually against claude.ai (see docs/spikes/SPIKE-niwa-session-keep-alive.md,
  "No-op wake validation"). These scenarios cover everything up to the launch
  seam: resolution, the injected payload, the record, and the report.

  Design: docs/designs/DESIGN-niwa-session-keep-alive.md

  # --- The full keep-alive workflow on a remote-control worker ---

  @critical
  Scenario: dispatch --keep-alive arms the worker, records it, and niwa list reports it
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
    Given a fake claude for dispatch with session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
    When I run "niwa dispatch keepalive-task --keep-alive --detach" from the workspace root
    Then the exit code is 0
    And the launched claude prompt contains the keep-alive arming instruction
    And the dispatch mapping for session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee" records keep-alive
    When I run "niwa list --json" from the workspace root
    Then the exit code is 0
    And the list JSON reports the dispatch instance as kept alive
    When I run "niwa list" from the workspace root
    Then the exit code is 0
    And the output contains "(keep-alive)"

  # --- Keep-alive on a non-remote-control worker: warn, no arming, dispatch succeeds ---

  Scenario: --keep-alive without remote control warns and dispatches without arming
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ffffffff-ffff-4fff-8fff-ffffffffffff"
    When I run "niwa dispatch plain-task --keep-alive --detach" from the workspace root
    Then the exit code is 0
    And the error output contains "keep-alive only applies to remote-control sessions"
    And the launched claude prompt does not contain the keep-alive arming instruction
    And the dispatch mapping for session "ffffffff-ffff-4fff-8fff-ffffffffffff" does not record keep-alive
