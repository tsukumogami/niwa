Feature: select OpenAI Codex as the workspace agent
  When a workspace declares default_agent = "codex" (or a session overrides with
  --agent codex), niwa materializes its context as AGENTS.md at the niwa-owned
  levels instead of CLAUDE.md, and skips writing repository-level context so a
  repo's own committed AGENTS.md is never clobbered. The default (Claude) path is
  unchanged.

  Design: docs/designs/current/DESIGN-interactive-codex-session.md

  @critical
  Scenario: a codex-default workspace materializes AGENTS.md at the instance root
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with a "ws.md" source file and body:
      """
      [workspace]
      name = "ws"
      default_agent = "codex"

      [groups.tools]

      [claude.content.workspace]
      source = "ws.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the instance "ws" exists
    And the file "AGENTS.md" exists in instance "ws"
    And the file "AGENTS.md" in instance "ws" contains "mcpServers"
    And the file "CLAUDE.md" does not exist in instance "ws"
    And the file "tools/app/CLAUDE.local.md" does not exist in instance "ws"
    And the file "tools/app/AGENTS.md" does not exist in instance "ws"

  @critical
  Scenario: niwa dispatch refuses in a codex-default workspace
    # niwa dispatch launches a Claude worker, so it refuses when the workspace
    # agent is codex rather than silently preparing a Codex instance a Claude
    # worker cannot read. The refusal fires before any instance is provisioned.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      default_agent = "codex"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa dispatch some-task --detach" from the workspace root
    Then the exit code is not 0
    And the error output contains "does not support"
    And the error output contains "NIWA_AGENT=claude"

  @critical
  Scenario: the default (Claude) workspace still materializes CLAUDE.md
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with a "ws.md" source file and body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [claude.content.workspace]
      source = "ws.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the instance "ws" exists
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "AGENTS.md" does not exist in instance "ws"
