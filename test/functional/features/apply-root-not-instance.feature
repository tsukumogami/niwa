Feature: niwa apply at the workspace root does not turn the root into an instance
  Regression coverage for the init -> apply workflow. `niwa init` persists an
  .niwa/instance.json at the workspace root (it carries init-time state that
  `niwa create` reads). That file made `niwa apply` run from the root
  misclassify the root as instance-0 and clone the configured repos directly
  under the root. apply at the root must only manage the root-level
  configuration and cascade to the instances within it -- never materialize
  repos at the root.

  Runs offline against the localGitServer bare-repo fake; no GitHub access.

  @critical
  Scenario: apply at the workspace root does not clone repos into the root
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "cfg" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init "team" from config repo "cfg"
    Then the exit code is 0
    And the workspace root "team" has a workspace.toml
    When I run "niwa apply" from workspace "team"
    Then the exit code is 0
    And the file "tools" does not exist under workspace root "team"
    And the file "tools/myapp" does not exist under workspace root "team"
