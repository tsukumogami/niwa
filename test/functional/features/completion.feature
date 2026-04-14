Feature: Shell completion for workspace, instance, and repo names
  niwa exposes dynamic tab-completion via cobra's hidden __complete
  subcommand. These scenarios invoke __complete directly against a
  sandboxed HOME/XDG_CONFIG_HOME/workspace root to verify the closures
  produce the right candidates with the right directive trailer.

  Design: docs/designs/current/DESIGN-contextual-completion.md

  Background:
    Given a clean niwa environment

  # --- Workspace-name completion ---

  @critical
  Scenario: apply completes registered workspaces from the global config
    Given a registered workspace "alpha" exists
    And a registered workspace "beta" exists
    And a registered workspace "gamma" exists
    When I run completion for "apply" with prefix ""
    Then the exit code is 0
    And the completion output contains "alpha"
    And the completion output contains "beta"
    And the completion output contains "gamma"

  @critical
  Scenario: apply prefix-filters to matching workspaces
    Given a registered workspace "alpha" exists
    And a registered workspace "alphabet" exists
    And a registered workspace "beta" exists
    When I run completion for "apply" with prefix "alp"
    Then the exit code is 0
    And the completion output contains "alpha"
    And the completion output contains "alphabet"
    And the completion output does not contain "beta"

  Scenario: create completes workspace names
    Given a registered workspace "alpha" exists
    When I run completion for "create" with prefix ""
    Then the exit code is 0
    And the completion output contains "alpha"

  Scenario: init completes workspace names (registry-only)
    Given a registered workspace "alpha" exists
    When I run completion for "init" with prefix ""
    Then the exit code is 0
    And the completion output contains "alpha"

  Scenario: go -w completes registered workspaces undecorated
    Given a registered workspace "alpha" exists
    When I run completion for "go -w" with prefix ""
    Then the exit code is 0
    And the completion output contains "alpha"

  # --- Instance-name completion ---

  @critical
  Scenario: destroy completes instance names of the current workspace
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    And an instance "myws-2" of workspace "myws" exists with repos ""
    When I run completion for "destroy" with prefix "" from instance "myws" of workspace "myws"
    Then the exit code is 0
    And the completion output contains "myws"
    And the completion output contains "myws-2"

  Scenario: reset completes instance names of the current workspace
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    When I run completion for "reset" with prefix "" from instance "myws" of workspace "myws"
    Then the exit code is 0
    And the completion output contains "myws"

  Scenario: status completes instance names
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos ""
    When I run completion for "status" with prefix "" from instance "myws" of workspace "myws"
    Then the exit code is 0
    And the completion output contains "myws"

  # --- Repo-name completion ---

  @critical
  Scenario: go -r completes repos in the current instance
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos "group-a/api,group-a/web,group-b/sdk"
    When I run completion for "go -r" with prefix "" from instance "myws" of workspace "myws"
    Then the exit code is 0
    And the completion output contains "api"
    And the completion output contains "web"
    And the completion output contains "sdk"

  @critical
  Scenario: go -w <ws> -r scopes to the sorted-first instance
    Given a registered workspace "myws" exists
    And an instance "myws" of workspace "myws" exists with repos "g/shared-repo,g/only-in-first"
    And an instance "myws-2" of workspace "myws" exists with repos "g/shared-repo,g/only-in-second"
    When I run completion for "go -w myws -r" with prefix ""
    Then the exit code is 0
    And the completion output contains "shared-repo"
    And the completion output contains "only-in-first"
    And the completion output does not contain "only-in-second"

  # --- Context-aware niwa go [target] ---

  @critical
  Scenario: go [target] unions repos and workspaces with kind decoration
    Given a registered workspace "myws" exists
    And a registered workspace "codespar" exists
    And an instance "myws" of workspace "myws" exists with repos "group/api,group/web"
    When I run completion for "go" with prefix "" from instance "myws" of workspace "myws"
    Then the exit code is 0
    And the completion output contains "api"
    And the completion output contains "web"
    And the completion output contains "codespar"
    And the completion description for "api" is "repo in 1"
    And the completion description for "codespar" is "workspace"

  Scenario: go [target] collision surfaces both kinds
    Given a registered workspace "myws" exists
    And a registered workspace "tsuku" exists
    And an instance "myws" of workspace "myws" exists with repos "group/tsuku"
    When I run completion for "go" with prefix "ts" from instance "myws" of workspace "myws"
    Then the exit code is 0
    And the completion description for "tsuku" is "repo in 1"
    And the completion description for "tsuku" is "workspace"

  Scenario: go [target] prefix filters across both kinds
    Given a registered workspace "alpha" exists
    And a registered workspace "beta" exists
    And an instance "alpha" of workspace "alpha" exists with repos "group/alpha-repo,group/other"
    When I run completion for "go" with prefix "alp" from instance "alpha" of workspace "alpha"
    Then the exit code is 0
    And the completion output contains "alpha"
    And the completion output contains "alpha-repo"
    And the completion output does not contain "beta"
    And the completion output does not contain "other"
