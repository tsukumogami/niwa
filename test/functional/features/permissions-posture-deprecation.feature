Feature: the workspace permissions posture is deprecated for dispatch
  A workspace that declares permissions in [claude.settings] or
  [instance.claude.settings] still gets every effect it had: "bypass" still
  derives --permission-mode bypassPermissions for niwa dispatch when nothing
  outranks it, and "ask" still writes permissions.defaultMode "default" into
  the generated settings documents (see permission-posture-documents.feature).
  What is new is a notice, printed with the other config warnings once per
  run of niwa apply, niwa create and niwa reset, pointing at the machine
  setting that replaces it: [global] dispatch_permission_mode in
  ~/.config/niwa/config.toml.

  Design: docs/designs/DESIGN-dispatch-default-permission-mode.md

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists

  @critical
  Scenario: create, apply and reset each print the deprecation warning once
    Given a config repo "posture" exists with body:
      """
      [workspace]
      name = "posture"

      [claude.settings]
      permissions = "bypass"

      [repos.app]
      url = "{repo:app}"
      """
    When I run niwa init from config repo "posture"
    Then the exit code is 0
    When I run "niwa create posture"
    Then the exit code is 0
    And the error output has exactly 1 line containing "permissions is deprecated for dispatch"
    And the error output has exactly 1 line containing "set [global] dispatch_permission_mode in ~/.config/niwa/config.toml instead"
    When I run "niwa apply posture"
    Then the exit code is 0
    And the error output has exactly 1 line containing "permissions is deprecated for dispatch"
    When I run "niwa reset posture" from workspace root
    Then the exit code is 0
    And the error output has exactly 1 line containing "permissions is deprecated for dispatch"

  Scenario: an instance-level posture prints the warning once and names its table
    Given a config repo "posture" exists with body:
      """
      [workspace]
      name = "posture"

      [instance.claude.settings]
      permissions = "ask"

      [repos.app]
      url = "{repo:app}"
      """
    When I run niwa init from config repo "posture"
    Then the exit code is 0
    When I run "niwa create posture"
    Then the exit code is 0
    And the error output has exactly 1 line containing "[instance.claude.settings] permissions is deprecated for dispatch"
    And the error output contains "dispatch_permission_mode"
    And the error output contains "~/.config/niwa/config.toml"

  Scenario: a workspace with no posture prints no deprecation warning
    Given a config repo "posture" exists with body:
      """
      [workspace]
      name = "posture"

      [repos.app]
      url = "{repo:app}"
      """
    When I run niwa init from config repo "posture"
    Then the exit code is 0
    When I run "niwa create posture"
    Then the exit code is 0
    And the error output does not contain "dispatch_permission_mode"
    When I run "niwa apply posture"
    Then the exit code is 0
    And the error output does not contain "dispatch_permission_mode"
