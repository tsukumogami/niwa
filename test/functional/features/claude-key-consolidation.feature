Feature: [claude.content] canonical schema with deprecated [content] alias
  workspace.toml's top-level [content] table was renamed to [claude.content]
  so the Claude-specific semantics are explicit. The legacy [content] key
  remains an accepted alias through the deprecation window (until v1.0)
  but emits a warning; using both forms together is a hard error.

  Design: docs/designs/current/DESIGN-claude-key-consolidation.md

  Background:
    Given a clean niwa environment

  # --- Deprecated alias is still accepted, with a warning ---

  @critical
  Scenario: niwa emits a deprecation warning when workspace.toml uses [content]
    Given a workspace "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [content.workspace]
      source = "ws.md"
      """
    When I run "niwa create" from workspace "myws"
    Then the error output contains "[content] is deprecated"
    And the error output contains "[claude.content]"

  @critical
  Scenario: canonical [claude.content] emits no deprecation warning
    Given a workspace "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [claude.content.workspace]
      source = "ws.md"
      """
    When I run "niwa create" from workspace "myws"
    Then the error output does not contain "[content] is deprecated"

  # --- Using both forms is a hard parse error ---

  @critical
  Scenario: niwa errors when workspace.toml sets both [content] and [claude.content]
    Given a workspace "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [content.workspace]
      source = "old.md"

      [claude.content.workspace]
      source = "new.md"
      """
    When I run "niwa create" from workspace "myws"
    Then the exit code is not 0
    And the error output contains "[content]"
    And the error output contains "[claude.content]"

  # --- Type split: override positions reject [claude.content] ---

  Scenario: [repos.<name>.claude.content] surfaces as an unknown-field warning
    Given a workspace "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [repos.myrepo]
      url = "https://example.com/myrepo"

      [repos.myrepo.claude.content]
      workspace = { source = "should-not-work.md" }
      """
    When I run "niwa create" from workspace "myws"
    Then the error output contains "unknown config field"
    And the error output contains "repos"
    And the error output contains "claude"
    And the error output contains "content"
