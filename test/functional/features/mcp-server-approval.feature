Feature: niwa pre-approves the MCP servers a workspace declares
  A server declared under [mcp.servers.<name>] is generated into the instance
  root's .mcp.json, and the same apply writes Claude Code's
  enabledMcpjsonServers list into the instance root's .claude/settings.json so
  a session there does not stop to ask about each one. The list names exactly
  the servers niwa generated, sorted, and follows the declaration on every
  apply: removing a server drops it from the list, and removing them all drops
  the key.

  A .mcp.json distributed verbatim through [instance.files] is not approved:
  niwa never parses that file, so it has no names to approve, and it never
  writes the wholesale enableAllProjectMcpServers form.

  Runs offline against the localGitServer bare-repo fake; no GitHub access.

  @critical
  Scenario: declared servers are approved, and the list follows the declaration
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [claude.settings]
      permissions = "ask"

      [mcp.servers.zeta]
      command = "zeta-server"

      [mcp.servers.example]
      url = "https://mcp.example.test/v1"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the file ".mcp.json" in instance "ws" contains "mcp.example.test"
    And the file ".mcp.json" in instance "ws" contains "zeta-server"
    And the instance "ws" settings document approves MCP servers "example,zeta"
    # The approval sits beside the rest of what niwa writes there.
    And the instance "ws" settings document has permissions.defaultMode "default"
    # Dropping one server shrinks the list on the next apply.
    When the config repo "ws" is force-pushed to:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [claude.settings]
      permissions = "ask"

      [mcp.servers.example]
      url = "https://mcp.example.test/v1"
      """
    And I run "niwa apply ws"
    Then the exit code is 0
    And the file ".mcp.json" in instance "ws" does not contain "zeta-server"
    And the instance "ws" settings document approves MCP servers "example"
    And the instance "ws" settings document has permissions.defaultMode "default"
    # Dropping the last one removes the key.
    When the config repo "ws" is force-pushed to:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [claude.settings]
      permissions = "ask"
      """
    And I run "niwa apply ws"
    Then the exit code is 0
    And the instance "ws" settings document approves no MCP servers
    And the instance "ws" settings document has permissions.defaultMode "default"

  @critical
  Scenario: a verbatim .mcp.json is distributed but not approved
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with a "mcp.json" source file and body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [instance.files]
      "mcp.json" = ".mcp.json"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the file ".mcp.json" in instance "ws" contains "mcpServers"
    And the instance "ws" settings document approves no MCP servers
