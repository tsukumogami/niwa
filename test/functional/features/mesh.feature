Feature: Session mesh: filesystem-based inter-session messaging
  End-to-end scenarios for the niwa session mesh. Two sessions register under
  the same instance root and exchange messages via the filesystem inbox.

  @critical
  Scenario: two sessions exchange a message via the filesystem inbox
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    And a sessions.json entry exists for role "coordinator"
    And the coordinator inbox directory exists
    When I run "niwa session register" as role "worker"
    Then the exit code is 0
    And a sessions.json entry exists for role "worker"
    When the worker session sends a "task.delegate" message to "coordinator" with body "hello"
    Then the exit code is 0
    And the coordinator inbox contains 1 message
    When the coordinator session checks messages
    Then the output contains "task.delegate"
    And the output contains "hello"

  @critical
  Scenario: niwa apply with [channels.mesh] creates channel infrastructure artifacts
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "chan-ws" exists with body:
      """
      [workspace]
      name = "chan-ws"

      [channels.mesh]
      [channels.mesh.roles]
      coordinator = ""
      worker = "tools/worker"
      """
    When I run niwa init from config repo "chan-ws"
    Then the exit code is 0
    When I run "niwa create chan-ws"
    Then the exit code is 0
    And the instance "chan-ws" exists
    And the file ".niwa/sessions/sessions.json" exists in instance "chan-ws"
    And the file ".claude/.mcp.json" exists in instance "chan-ws"
    And the file ".claude/.mcp.json" in instance "chan-ws" contains "mcp-serve"
    And the file ".claude/.mcp.json" in instance "chan-ws" contains "NIWA_INSTANCE_ROOT"
    And the file "workspace-context.md" in instance "chan-ws" contains "## Channels"
    And the file "workspace-context.md" in instance "chan-ws" contains "coordinator"
    And the file ".niwa/hooks/mesh-session-start.sh" exists in instance "chan-ws"
    And the file ".niwa/hooks/mesh-user-prompt-submit.sh" exists in instance "chan-ws"

  @critical
  Scenario: second niwa apply does not duplicate channel artifacts
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "chan-ws" exists with body:
      """
      [workspace]
      name = "chan-ws"

      [channels.mesh]
      [channels.mesh.roles]
      coordinator = ""
      """
    When I run niwa init from config repo "chan-ws"
    Then the exit code is 0
    When I run "niwa create chan-ws"
    Then the exit code is 0
    And the file "workspace-context.md" in instance "chan-ws" contains "## Channels"
    When I run "niwa apply chan-ws"
    Then the exit code is 0
    And the file "workspace-context.md" in instance "chan-ws" contains "## Channels"
    And the file ".niwa/sessions/sessions.json" in instance "chan-ws" contains "{\"sessions\":[]}"

  @critical
  Scenario: niwa session register populates claude_session_id via tier-2 PPID walk
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    And a Claude session file exists for the parent process with session ID "test-claude-session-abc1" and matching cwd
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    And the sessions.json entry for role "coordinator" has claude_session_id "test-claude-session-abc1"

  @critical
  Scenario: niwa session register omits claude_session_id when no session file exists
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    And the sessions.json entry for role "coordinator" has no claude_session_id

  Scenario: claude_session_id is skipped when cwd does not match session file
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    And a Claude session file exists for the parent process with session ID "test-claude-session-abc1" and mismatched cwd
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    And the sessions.json entry for role "coordinator" has no claude_session_id

  @critical
  Scenario: workspace without [channels.mesh] does not create channel artifacts
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "plain-ws" exists with body:
      """
      [workspace]
      name = "plain-ws"
      """
    When I run niwa init from config repo "plain-ws"
    Then the exit code is 0
    When I run "niwa create plain-ws"
    Then the exit code is 0
    And the instance "plain-ws" exists
    And the file "workspace-context.md" in instance "plain-ws" does not contain "## Channels"
    And the file ".niwa/sessions" does not exist in instance "plain-ws"
    And the file ".claude/.mcp.json" does not exist in instance "plain-ws"
    And the file ".niwa/hooks/mesh-session-start.sh" does not exist in instance "plain-ws"
