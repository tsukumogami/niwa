Feature: Plugin pre-warm does not dirty niwa-managed settings.json
  When an instance declares a github-sourced Claude marketplace and plugin, niwa
  pre-warms them to disk during provisioning so the first Claude session finds the
  skills already installed (the #178 race fix). That pre-warm must NOT mutate the
  instance's .claude/settings.json -- the file niwa materializes and fingerprints as
  a managed file -- or the next `niwa apply` falsely reports it "modified outside
  niwa" (#179).

  The pre-warm shells out to the real `claude plugin` CLI, which these scenarios
  replace with a fake that mimics the real binary's scope-dependent settings write:
  `--scope project` re-serializes settings.json (the regression), `--scope local`
  writes settings.local.json and leaves settings.json untouched (the fix). The
  scenarios run on the scenario-sandboxed $HOME and offline localGitServer.

  Issue: #179 (follow-up to #178)

  @critical
  Scenario: pre-warming a declared github plugin leaves the managed settings.json clean
    Given a clean niwa environment
    And a local git server is set up
    And a fake claude for plugin pre-warming
    And a config repo "driftws" exists with body:
      """
      [workspace]
      name = "driftws"

      [claude]
      plugins = ["example@example-marketplace"]

      [[claude.marketplaces]]
      source = "example-org/example-marketplace"
      track = "main"
      """
    When I run niwa init from config repo "driftws"
    Then the exit code is 0
    When I run "niwa create driftws"
    Then the exit code is 0
    And the instance "driftws" exists
    # The pre-warm ran and used local scope, not project scope: the install
    # populates the plugin cache without rewriting the managed settings.json.
    And the recorded pre-warm install scope is "local"
    When I run "niwa apply driftws"
    Then the exit code is 0
    And the stderr does not contain "settings.json has been modified outside niwa"
