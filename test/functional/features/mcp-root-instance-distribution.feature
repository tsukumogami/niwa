Feature: niwa distributes verbatim-named files to the instance root
  End-to-end coverage that an [instance.files] entry materializes a
  verbatim-named (no .local infix) file at the instance root, driven from
  workspace config through the init -> create -> apply workflow. The motivating
  case is a Claude Code project .mcp.json: it must keep its exact name to be
  loaded, and the instance root is not a git repository, so the per-repo .local
  rewrite does not apply.

  The workspace-root counterpart ([root.files], materialized by
  MaterializeWorkspaceRoot) is covered by unit tests in
  internal/workspace/root_materializer_test.go.

  Runs offline against the localGitServer bare-repo fake; no GitHub access.

  Design: docs/designs/current/DESIGN-mcp-root-instance-distribution.md

  @critical
  Scenario: an [instance.files] entry lands verbatim at the instance root
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
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file ".mcp.json" in instance "ws" contains "mcpServers"
    And the file ".mcp.local.json" does not exist in instance "ws"
