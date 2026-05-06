Feature: niwa init creates the workspace directory
  End-to-end coverage for the named `niwa init <name>` flow: directory
  creation, target-exists rejection, registry-collision gating behind
  --rebind, and the override propagating through `niwa go` and
  `niwa status`.

  Design: docs/designs/DESIGN-niwa-init-creates-workspace-dir.md
  PRD: docs/prds/PRD-niwa-init-creates-workspace-dir.md

  @critical
  Scenario: niwa init <name> --from <fixture> creates the workspace directory and the override propagates through status
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "upstream-cfg" exists with body:
      """
      [workspace]
      name = "upstream"
      """
    When I run niwa init "my-team" from config repo "upstream-cfg"
    Then the exit code is 0
    And the workspace root "my-team" has a workspace.toml
    And the registry has workspace "my-team" rooted at "my-team"
    And niwa go "my-team" from outside lands in "my-team"
    When I run "niwa create my-team"
    Then the exit code is 0
    When I run "niwa status" from instance "my-team" of workspace "my-team"
    Then the exit code is 0
    And the stdout contains "my-team"

  Scenario: niwa init refuses when the target directory already exists
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "upstream-cfg" exists with body:
      """
      [workspace]
      name = "upstream"
      """
    And I pre-create directory "my-team"
    When I run niwa init "my-team" from config repo "upstream-cfg"
    Then the exit code is not 0
    And the stderr contains "already exists (directory)"

  Scenario: niwa init refuses a registry collision without --rebind
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "upstream-cfg" exists with body:
      """
      [workspace]
      name = "upstream"
      """
    And the registry already has workspace "my-team" rooted at "elsewhere"
    When I run niwa init "my-team" from config repo "upstream-cfg"
    Then the exit code is not 0
    And the stderr contains "--rebind" and "config.toml"
    And the registry entry "my-team" still points at "elsewhere"

  Scenario: niwa init --rebind retargets the registry to the new directory
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "upstream-cfg" exists with body:
      """
      [workspace]
      name = "upstream"
      """
    And the registry already has workspace "my-team" rooted at "elsewhere"
    When I run niwa init "my-team" from config repo "upstream-cfg" with --rebind
    Then the exit code is 0
    And the workspace root "my-team" has a workspace.toml
    And the registry has workspace "my-team" rooted at "my-team"
