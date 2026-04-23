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
    And the error output contains "could not discover Claude session ID"

  @critical
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

  @critical
  Scenario: apply with [channels.mesh] spawns mesh watch daemon
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "daemon-ws" exists with body:
      """
      [workspace]
      name = "daemon-ws"

      [channels.mesh]
      [channels.mesh.roles]
      coordinator = ""
      """
    When I run niwa init from config repo "daemon-ws"
    Then the exit code is 0
    When I run "niwa create daemon-ws"
    Then the exit code is 0
    And the instance "daemon-ws" exists
    And the file ".niwa/daemon.pid" exists in instance "daemon-ws"

  @critical
  Scenario: second apply does not spawn a second daemon
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "daemon2-ws" exists with body:
      """
      [workspace]
      name = "daemon2-ws"

      [channels.mesh]
      [channels.mesh.roles]
      coordinator = ""
      """
    When I run niwa init from config repo "daemon2-ws"
    Then the exit code is 0
    When I run "niwa create daemon2-ws"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "daemon2-ws"
    When I remember the daemon PID for instance "daemon2-ws"
    When I run "niwa apply daemon2-ws"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "daemon2-ws"
    And the daemon PID for instance "daemon2-ws" has not changed

  @critical
  Scenario: destroy terminates the mesh watch daemon
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "destroy-daemon-ws" exists with body:
      """
      [workspace]
      name = "destroy-daemon-ws"

      [channels.mesh]
      [channels.mesh.roles]
      coordinator = ""
      """
    When I run niwa init from config repo "destroy-daemon-ws"
    Then the exit code is 0
    When I run "niwa create destroy-daemon-ws"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "destroy-daemon-ws"
    When I run "niwa destroy --force destroy-daemon-ws" from workspace "destroy-daemon-ws"
    Then the exit code is 0
    And the instance "destroy-daemon-ws" does not exist

  @critical
  Scenario: daemon self-exits when sessions directory is removed
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "selfstop-ws" exists with body:
      """
      [workspace]
      name = "selfstop-ws"

      [channels.mesh]
      [channels.mesh.roles]
      coordinator = ""
      """
    When I run niwa init from config repo "selfstop-ws"
    Then the exit code is 0
    When I run "niwa create selfstop-ws"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "selfstop-ws"
    When I remove the sessions directory from instance "selfstop-ws"
    Then the daemon for instance "selfstop-ws" eventually stops

  @critical
  Scenario: niwa_ask receives an answer from another session
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    When I run "niwa session register" as role "worker"
    Then the exit code is 0
    When the coordinator asks the worker a question and the worker replies
    Then the ask response contains the answer

  @critical
  Scenario: niwa_ask returns ASK_TIMEOUT when no reply arrives
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    When I run "niwa session register" as role "worker"
    Then the exit code is 0
    When the coordinator calls niwa_ask with timeout 2 seconds and no reply
    Then the output contains "ASK_TIMEOUT"

  @critical
  Scenario: niwa_wait unblocks when count threshold is met
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    When 2 "task.result" messages are placed in the coordinator inbox
    When the coordinator calls niwa_wait for "task.result" messages with count 2
    Then the output contains "task.result"
    And the output contains "2 message"

  @critical
  Scenario: niwa_send_message rejects invalid field
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    When I run "niwa session register" as role "worker"
    Then the exit code is 0
    When the coordinator sends a message with invalid type "../../evil"
    Then the output contains "isError"

  @critical
  Scenario: CLAUDE_SESSION_ID env var is used as tier-1 session ID
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    And I set env "CLAUDE_SESSION_ID" to "abcd1234EFGH"
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    And the sessions.json entry for role "coordinator" has claude_session_id "abcd1234EFGH"

  @critical
  Scenario: invalid CLAUDE_SESSION_ID is rejected with a warning
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    And I set env "CLAUDE_SESSION_ID" to "../../evil"
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    And the sessions.json entry for role "coordinator" has no claude_session_id
    And the error output contains "CLAUDE_SESSION_ID has invalid format"

  @critical
  Scenario: niwa create with bare [channels.mesh] (no roles) provisions channel infrastructure
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "bare-mesh-ws" exists with body:
      """
      [workspace]
      name = "bare-mesh-ws"

      [channels.mesh]
      """
    When I run niwa init from config repo "bare-mesh-ws"
    Then the exit code is 0
    When I run "niwa create bare-mesh-ws"
    Then the exit code is 0
    And the instance "bare-mesh-ws" exists
    And the file ".niwa/sessions/sessions.json" exists in instance "bare-mesh-ws"
    And the file ".claude/.mcp.json" exists in instance "bare-mesh-ws"
    And the file ".niwa/daemon.pid" exists in instance "bare-mesh-ws"
    And the file "workspace-context.md" in instance "bare-mesh-ws" contains "## Channels"
    And the file ".niwa/hooks/mesh-session-start.sh" exists in instance "bare-mesh-ws"

  @critical
  Scenario: session registered from a repo directory gets role matching repo basename
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" from repo directory "myrepo"
    Then the exit code is 0
    And a sessions.json entry exists for role "myrepo"

  @critical
  Scenario: session registered from instance root gets role coordinator via pwd fallback
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    When I run "niwa session register" from instance root
    Then the exit code is 0
    And a sessions.json entry exists for role "coordinator"

  @critical
  Scenario: NIWA_SESSION_ROLE overrides pwd-derived role
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    And I set env "NIWA_SESSION_ROLE" to "custom"
    When I run "niwa session register"
    Then the exit code is 0
    And a sessions.json entry exists for role "custom"

  @critical
  Scenario: --role flag overrides NIWA_SESSION_ROLE and pwd fallback
    Given a clean niwa environment
    And NIWA_INSTANCE_ROOT is set to a temp directory
    And I set env "NIWA_SESSION_ROLE" to "env-role"
    When I run "niwa session register --role explicit"
    Then the exit code is 0
    And a sessions.json entry exists for role "explicit"

  @critical
  Scenario: niwa create --channels provisions channel infrastructure on a plain workspace
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "flag-chan-ws" exists with body:
      """
      [workspace]
      name = "flag-chan-ws"
      """
    When I run niwa init from config repo "flag-chan-ws"
    Then the exit code is 0
    When I run "niwa create --channels flag-chan-ws"
    Then the exit code is 0
    And the instance "flag-chan-ws" exists
    And the file ".niwa/sessions/sessions.json" exists in instance "flag-chan-ws"
    And the file ".niwa/daemon.pid" exists in instance "flag-chan-ws"

  @critical
  Scenario: NIWA_CHANNELS=1 provisions channel infrastructure on a plain workspace
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "env-chan-ws" exists with body:
      """
      [workspace]
      name = "env-chan-ws"
      """
    When I run niwa init from config repo "env-chan-ws"
    Then the exit code is 0
    And I set env "NIWA_CHANNELS" to "1"
    When I run "niwa create env-chan-ws"
    Then the exit code is 0
    And the instance "env-chan-ws" exists
    And the file ".niwa/sessions/sessions.json" exists in instance "env-chan-ws"
    And the file ".niwa/daemon.pid" exists in instance "env-chan-ws"

  @critical
  Scenario: NIWA_CHANNELS=1 with --no-channels does NOT provision channel infrastructure
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "no-chan-ws" exists with body:
      """
      [workspace]
      name = "no-chan-ws"
      """
    When I run niwa init from config repo "no-chan-ws"
    Then the exit code is 0
    And I set env "NIWA_CHANNELS" to "1"
    When I run "niwa create --no-channels no-chan-ws"
    Then the exit code is 0
    And the instance "no-chan-ws" exists
    And the file ".niwa/sessions" does not exist in instance "no-chan-ws"
    And the file ".niwa/daemon.pid" does not exist in instance "no-chan-ws"

  @critical
  Scenario: daemon log records message type but not body content
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "logcheck-ws" exists with body:
      """
      [workspace]
      name = "logcheck-ws"

      [channels.mesh]
      [channels.mesh.roles]
      coordinator = ""
      worker = ""
      """
    When I run niwa init from config repo "logcheck-ws"
    Then the exit code is 0
    When I run "niwa create logcheck-ws"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "logcheck-ws"
    When I set NIWA_INSTANCE_ROOT to instance "logcheck-ws"
    When I run "niwa session register" as role "coordinator"
    Then the exit code is 0
    When I run "niwa session register" as role "worker"
    Then the exit code is 0
    When the worker session sends a "task.result" message to "coordinator" with body "DAEMON-BODY-EXCLUSION-MARKER"
    Then the exit code is 0
    And the daemon log for instance "logcheck-ws" eventually contains "new message"
    And the file ".niwa/daemon.log" in instance "logcheck-ws" does not contain "DAEMON-BODY-EXCLUSION-MARKER"

  @channels-e2e
  Scenario: headless coordinator reads messages via niwa_check_messages after channels provision
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a config repo "headless-check-ws" exists with body:
      """
      [workspace]
      name = "headless-check-ws"
      """
    When I run niwa init from config repo "headless-check-ws"
    Then the exit code is 0
    When I run "niwa create --channels headless-check-ws"
    Then the exit code is 0
    When I set up coordinator session for instance "headless-check-ws"
    And 1 "task.result" messages are placed in the coordinator inbox
    When I run claude -p from instance root "headless-check-ws" with prompt:
      """
      Use the niwa_check_messages tool to check your inbox. Find the message type of the first message and output exactly: FOUND:<type> where <type> is the message type value.
      """
    Then the exit code is 0
    And the output contains "found:task.result"

  @channels-e2e
  Scenario: headless coordinator completes ask round-trip with simulated worker
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a config repo "headless-ask-ws" exists with body:
      """
      [workspace]
      name = "headless-ask-ws"
      """
    When I run niwa init from config repo "headless-ask-ws"
    Then the exit code is 0
    When I run "niwa create --channels headless-ask-ws"
    Then the exit code is 0
    When I set up coordinator session for instance "headless-ask-ws"
    And I set up worker session for instance "headless-ask-ws"
    When I run claude -p from instance root "headless-ask-ws" with simulated worker reply and prompt:
      """
      Use the niwa_ask tool to ask the worker the question "What is the answer?". When you receive the reply, output exactly: ANSWER:<value> where <value> is the answer field from the reply body.
      """
    Then the exit code is 0
    And the output contains "answer:42"

  @channels-e2e
  Scenario: headless coordinator collects task results via niwa_wait after channels provision
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a config repo "headless-wait-ws" exists with body:
      """
      [workspace]
      name = "headless-wait-ws"
      """
    When I run niwa init from config repo "headless-wait-ws"
    Then the exit code is 0
    When I run "niwa create --channels headless-wait-ws"
    Then the exit code is 0
    When I set up coordinator session for instance "headless-wait-ws"
    And 2 "task.result" messages are placed in the coordinator inbox
    When I run claude -p from instance root "headless-wait-ws" with prompt:
      """
      Use the niwa_wait tool with types=["task.result"] and count=2 to collect 2 task results. When you receive them, output exactly: COLLECTED:<n> where <n> is the number of messages collected.
      """
    Then the exit code is 0
    And the output contains "collected:2"
