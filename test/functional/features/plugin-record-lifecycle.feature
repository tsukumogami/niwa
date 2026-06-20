Feature: Claude plugin-record lifecycle
  niwa cleans up the Claude Code plugin-install records it causes. On
  destroy it prunes the records owned by the instance it removes; on every
  create and update it heals dangling records (whose installPath or
  projectPath directory is gone) and snapshots a backup first. It also
  tracks the latest stable release for github marketplaces by default.

  These scenarios operate on the scenario-sandboxed $HOME, so they mutate a
  test copy of ~/.claude/plugins/installed_plugins.json, never the real one.

  Design: docs/designs/DESIGN-niwa-plugin-record-lifecycle.md
  Plan: docs/plans/PLAN-niwa-plugin-record-lifecycle.md (Issue 8)

  # --- Scenario A: destroy prunes instance-owned records (R1/R2) ---

  @critical
  Scenario: destroying an instance prunes only its own plugin records
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos "tools/api"
    And an instance "myws-2" of workspace "myws" exists with repos "tools/api"
    And the plugin registry has records:
      | plugin            | projectPath                      | installPath |
      | skill@niwa        | {instance:myws/myws}/tools/api   | {home}      |
      | skill@niwa        | {instance:myws/myws-2}/tools/api | {home}      |
    When I run "niwa destroy myws" from workspace "myws"
    Then the exit code is 0
    And the instance "myws" of workspace "myws" does not exist
    And the plugin registry has 1 record for plugin "skill@niwa"
    And the plugin registry has a record with projectPath under instance "myws/myws-2"
    And the plugin registry has no record with projectPath under instance "myws/myws"

  # --- Scenario B: automatic dangling heal on create (R3/R11) ---

  @critical
  Scenario: creating a workspace heals dangling records and writes a backup
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "healws" exists with body:
      """
      [workspace]
      name = "healws"
      """
    When I run niwa init from config repo "healws"
    Then the exit code is 0
    And the plugin registry has records:
      | plugin       | projectPath               | installPath               |
      | skill@niwa   | {abs}/does/not/exist/a    | {abs}/gone/cache/a        |
      | skill@niwa   | {abs}/also/gone/b         | {abs}/gone/cache/b        |
      | skill@niwa   | {home}                    | {home}                    |
    When I run "niwa create healws"
    Then the exit code is 0
    And the instance "healws" exists
    And the plugin registry has 1 record for plugin "skill@niwa"
    And the plugin registry has a record with projectPath equal to HOME
    And a plugin registry backup exists

  # --- Scenario C: github marketplace release-tracking (R14, spike-adjusted) ---
  #
  # SPIKE FINDING (Decision 6): Claude Code does NOT honor a ref/tag/commit
  # pin on a github marketplace SOURCE object, so this scenario does not (and
  # cannot) assert that Claude installs a release tag. Instead it asserts
  # niwa's own observable behavior: niwa attempts release resolution for a
  # github marketplace by default and reports its tracking decision.
  #
  # Release resolution shells out to `git ls-remote https://github.com/<repo>`,
  # which has no offline localGitServer equivalent (the URL is hardcoded to
  # github.com). Offline the lookup fails, so niwa falls back to the default
  # branch and emits the fallback notice. The contrast scenario (track = "main")
  # emits nothing, proving the release path is the engaged default and that the
  # config knob is honored. This is the closest robust offline observable for
  # release-tracking given the spike limitation.

  Scenario: a github marketplace defaults to release-tracking and reports the fallback offline
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "mktws" exists with body:
      """
      [workspace]
      name = "mktws"

      [[claude.marketplaces]]
      source = "example-org/example-marketplace"
      """
    When I run niwa init from config repo "mktws"
    Then the exit code is 0
    When I run "niwa create mktws"
    Then the exit code is 0
    And the instance "mktws" exists
    And the stderr contains "has no stable release; tracking the default branch"

  Scenario: a github marketplace pinned to main does not report release-tracking
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "mainws" exists with body:
      """
      [workspace]
      name = "mainws"

      [[claude.marketplaces]]
      source = "example-org/example-marketplace"
      track = "main"
      """
    When I run niwa init from config repo "mainws"
    Then the exit code is 0
    When I run "niwa create mainws"
    Then the exit code is 0
    And the instance "mainws" exists
    And the stderr does not contain "has no stable release"
