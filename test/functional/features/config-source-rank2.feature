Feature: Rank-2 deprecation notice and niwa plugin auto-install
  End-to-end coverage of the workspace-config-sources discovery
  feature: when a workspace is initialized from a rank-2 source
  (workspace.toml at the source repo root), niwa emits a one-time
  deprecation notice on stderr AND auto-installs the embedded niwa
  Claude Code plugin under ~/.claude/plugins/marketplaces/niwa/.
  A second invocation against the same workspace neither re-emits
  the notice nor re-runs the install.

  Design: docs/designs/DESIGN-config-source-discovery.md
  PRD: docs/prds/PRD-config-source-discovery.md (R10, R14, R17-R20)

  @critical
  Scenario: rank-2 source triggers deprecation notice and plugin install
    Given a clean niwa environment
    And a local git server is set up
    And a rank-2 config repo "legacy-ws" exists with body:
      """
      [workspace]
      name = "legacy-ws"

      [groups.tools]
      """
    When I run niwa init from config repo "legacy-ws"
    Then the exit code is 0
    And the stderr contains "deprecated rank-2 layout"
    And the stderr contains "/niwa:migrate-config"
    And the file ".claude/plugins/marketplaces/niwa/manifest.json" exists in HOME

  @critical
  Scenario: --no-install-plugins opts out of auto-install but still emits rank-2 notice
    Given a clean niwa environment
    And a local git server is set up
    And a rank-2 config repo "legacy-optout" exists with body:
      """
      [workspace]
      name = "legacy-optout"

      [groups.tools]
      """
    When I run niwa init from config repo "legacy-optout" with --no-install-plugins
    Then the exit code is 0
    And the stderr contains "deprecated rank-2 layout"
    And the stderr contains "niwa --install-plugins"
