Feature: a workspace overlay declares MCP servers
  A workspace overlay can declare [mcp.servers.<name>] in the same shape the
  base config takes. After the merge an overlay server is an ordinary entry of
  the table, so the instance root's .mcp.json and each repo's generated Codex
  config carry it. A name the base config also declares is the base config's:
  the overlay entry is dropped with a warning naming it.

  Vault resolution of overlay server values against the overlay's own
  providers is covered by unit tests in internal/workspace (the functional
  suite has no fake vault backend); these scenarios use literal values.

  Runs offline against the localGitServer bare-repo fake; no GitHub access.

  Design: docs/designs/DESIGN-trust-instances.md

  @critical
  Scenario: an overlay-declared MCP server reaches the generated configs
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
      """
    And an overlay repo "ws-overlay" exists with body:
      """
      [mcp.servers.example]
      url = "https://mcp.example.com/v1"

      [mcp.servers.example.headers]
      X-Example-Header = "literal-header-value"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the file ".mcp.json" in instance "ws" contains "mcpServers"
    And the file ".mcp.json" in instance "ws" contains "https://mcp.example.com/v1"
    And the file ".mcp.json" in instance "ws" contains "literal-header-value"
    And the file "tools/app/.codex/config.toml" in instance "ws" contains "[mcp_servers.example]"

  @critical
  Scenario: the base config's MCP server wins a name collision with the overlay
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

      [mcp.servers.example]
      url = "https://base.example.com/v1"
      """
    And an overlay repo "ws-overlay" exists with body:
      """
      [mcp.servers.example]
      url = "https://overlay.example.com/v1"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the error output contains "workspace overlay declares MCP server"
    And the file ".mcp.json" in instance "ws" contains "https://base.example.com/v1"
    And the file ".mcp.json" in instance "ws" does not contain "overlay.example.com"
