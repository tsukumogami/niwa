Feature: apply and destroy never take the whole workspace's scope by accident
  apply and destroy act on every instance when run at the workspace root.
  They used to get that scope from any directory under the root that isn't an
  instance, such as the half-provisioned directory an interrupted create
  leaves behind (issue #344), and a bare `destroy --force` at the root wiped
  every instance when its instance name went missing (issue #342). Both now
  refuse. An explicit `niwa apply` at the root still converges every instance.

  Runs offline against the localGitServer bare-repo fake; no GitHub access.

  Background:
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
    When I run "niwa create" from directory "." under workspace "team"
    Then the exit code is 0
    When I run "niwa create" from directory "." under workspace "team"
    Then the exit code is 0
    And the file "team/tools/myapp" exists under workspace root "team"
    And the file "team-2/tools/myapp" exists under workspace root "team"
    # What an interrupted create leaves: a directory with the instance's
    # shape and no .niwa/instance.json.
    Given a foreign directory "team/team+-0000beef/tools" exists in the workspace root

  @critical
  Scenario: apply from a stray directory under the root refuses and converges nothing
    When I remove "team/tools/myapp" under workspace root "team"
    And I remove "team-2/tools/myapp" under workspace root "team"
    And I run "niwa apply" from directory "team+-0000beef" under workspace "team"
    Then the exit code is not 0
    And the error output contains "is not an instance, a worktree, or the workspace root"
    And the error output contains "interrupted"
    When I run "niwa apply --instance team" from directory "team+-0000beef/tools" under workspace "team"
    Then the exit code is not 0
    And the error output contains "is not an instance, a worktree, or the workspace root"
    And the file "team/tools/myapp" does not exist under workspace root "team"
    And the file "team-2/tools/myapp" does not exist under workspace root "team"
    # The cascade from the root itself still converges every instance.
    When I run "niwa apply" from directory "." under workspace "team"
    Then the exit code is 0
    And the file "team/tools/myapp" exists under workspace root "team"
    And the file "team-2/tools/myapp" exists under workspace root "team"

  @critical
  Scenario: destroy from a stray directory under the root refuses, with or without --force
    When I run "niwa destroy" from directory "team+-0000beef" under workspace "team"
    Then the exit code is not 0
    And the error output contains "is not an instance, a worktree, or the workspace root"
    When I run "niwa destroy --force" from directory "team+-0000beef" under workspace "team"
    Then the exit code is not 0
    And the error output contains "is not an instance, a worktree, or the workspace root"
    When I run "niwa destroy --workspace --confirm team" from directory "team+-0000beef/tools" under workspace "team"
    Then the exit code is not 0
    And the file "team/tools/myapp" exists under workspace root "team"
    And the file "team-2/tools/myapp" exists under workspace root "team"

  @critical
  Scenario: a bare destroy --force at the root refuses and names the explicit form
    When I run "niwa destroy --force" from directory "." under workspace "team"
    Then the exit code is not 0
    And the error output contains "niwa destroy --workspace"
    And the file "team/tools/myapp" exists under workspace root "team"
    And the file "team-2/tools/myapp" exists under workspace root "team"
    And the workspace root "team" has a workspace.toml

  @critical
  Scenario: destroy --workspace needs the workspace name before it removes anything
    When I run "niwa destroy --workspace" from directory "." under workspace "team"
    Then the exit code is not 0
    And the error output contains "--confirm team"
    When I run "niwa destroy --workspace --confirm myws" from directory "." under workspace "team"
    Then the exit code is not 0
    And the error output contains "does not match"
    And the file "team/tools/myapp" exists under workspace root "team"
    When I run "niwa destroy --workspace --confirm team" from directory "." under workspace "team"
    Then the exit code is 0
    And the workspace "team" does not exist
