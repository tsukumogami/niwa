Feature: Parallel repo clones in niwa create and apply
  Verifies that multiple repos are cloned concurrently with correct outcomes.
  Uses a local bare-repo server — no GitHub access required.

  Design: docs/designs/current/DESIGN-parallel-clones.md

  # --- Happy path: multiple repos all land in the instance directory ---

  @critical
  Scenario: niwa create with multiple repos clones all repos
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "alpha" exists
    And a source repo "beta" exists
    And a source repo "gamma" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.alpha]
      url = "{repo:alpha}"
      group = "tools"

      [repos.beta]
      url = "{repo:beta}"
      group = "tools"

      [repos.gamma]
      url = "{repo:gamma}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "myws" exists
    And the repo "tools/alpha" exists in instance "myws"
    And the repo "tools/beta" exists in instance "myws"
    And the repo "tools/gamma" exists in instance "myws"

  # --- Fail-fast: one bad URL causes create to fail and removes the instance dir ---

  @critical
  Scenario: niwa create with one invalid repo URL fails and cleans up the instance directory
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "good" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.good]
      url = "{repo:good}"
      group = "tools"

      [repos.broken]
      url = "file:///nonexistent/does-not-exist.git"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is not 0
    And the instance "myws" does not exist

  # --- Apply: multiple repos already cloned are all synced successfully ---

  @critical
  Scenario: niwa apply with multiple repos syncs all repos without errors
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "alpha" exists
    And a source repo "beta" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.alpha]
      url = "{repo:alpha}"
      group = "tools"

      [repos.beta]
      url = "{repo:beta}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "myws" exists
    And the repo "tools/alpha" exists in instance "myws"
    And the repo "tools/beta" exists in instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the repo "tools/alpha" exists in instance "myws"
    And the repo "tools/beta" exists in instance "myws"
