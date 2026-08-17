Feature: prepare every instance for both agents
  Every instance niwa prepares serves Claude Code and Codex alike: the
  niwa-owned levels carry CLAUDE.md and AGENTS.md side by side, and the Claude
  tree is written in full whatever default_agent says. That setting selects
  which agent a niwa-launched session runs, not what preparation produces.
  niwa still writes no AGENTS.md inside a cloned repository, so a repo's own
  committed AGENTS.md is never clobbered.

  Design: docs/designs/current/DESIGN-dual-agent-workspace.md

  @critical
  Scenario: a codex-default workspace still materializes the whole Claude tree
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

      [claude.content.repos.app]
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
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "CLAUDE.md" in instance "ws" contains "mcpServers"
    And the file "tools/app/CLAUDE.local.md" exists in instance "ws"
    # niwa writes AGENTS.override.md into repositories, never AGENTS.md: this
    # assertion guards the repo's own committed file and must stay.
    And the file "tools/app/AGENTS.md" does not exist in instance "ws"
    # A config declaring the agent setting re-applies with no migration step.
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "AGENTS.md" exists in instance "ws"

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
  Scenario: a claude-default workspace materializes both agents' context too
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
    And the file "AGENTS.md" exists in instance "ws"
    And the file "AGENTS.md" in instance "ws" contains "mcpServers"
    # A config predating the agent setting re-applies unchanged, and still
    # serves both agents.
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "AGENTS.md" exists in instance "ws"
