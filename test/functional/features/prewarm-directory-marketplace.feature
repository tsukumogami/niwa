Feature: Plugin pre-warm registers a directory marketplace before installing from it
  `claude plugin install` resolves a marketplace through Claude Code's registry
  (~/.claude/plugins/known_marketplaces.json), not through the project settings
  that declare it. A directory marketplace the host has never registered (a
  `repo:` marketplace, which niwa serves from the workspace's managed copy) must
  therefore be added before its plugins are pre-warmed, or every install fails
  with "not found in marketplace". Once the registry lists it, later applies
  leave the entry alone.

  The pre-warm shells out to a fake `claude` that records each call and, like
  the real binary, registers a directory marketplace it is asked to add. The
  scenario runs on the scenario-sandboxed $HOME and the offline localGitServer.

  Issue: #385

  @critical
  Scenario: a repo marketplace is registered once, before its plugin is installed
    Given a clean niwa environment
    And a local git server is set up
    And a fake claude for plugin pre-warming
    And a staged file ".claude-plugin/marketplace.json" with body:
      """
      {"name":"demo-market","plugins":[{"name":"demo","source":"./plugins/demo"}]}
      """
    And a staged file "plugins/demo/.claude-plugin/plugin.json" with body:
      """
      {"name":"demo","version":"0.1.0"}
      """
    And a source repo "mkt" exists with the staged files
    And a config repo "dirws" exists with body:
      """
      [workspace]
      name = "dirws"

      [groups.tools]

      [claude]
      plugins = ["demo@demo-market"]

      [[claude.marketplaces]]
      source = "repo:mkt/.claude-plugin/marketplace.json"

      [repos.mkt]
      url = "{repo:mkt}"
      group = "tools"
      """
    When I run niwa init from config repo "dirws"
    Then the exit code is 0
    When I run "niwa create dirws"
    Then the exit code is 0
    And the instance "dirws" exists
    And the pre-warm made a call with "marketplace add /" before a call with "install demo@demo-market"
    And the stderr does not contain "pre-warming"
    When I run "niwa apply dirws"
    Then the exit code is 0
    And the pre-warm made 1 call with "marketplace add /"
    And the pre-warm made 2 calls with "install demo@demo-market"
