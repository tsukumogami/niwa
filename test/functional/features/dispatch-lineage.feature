Feature: niwa dispatch: lineage resource attributes reach the worker
  A dispatched Claude worker carries niwa's lineage attributes on its
  telemetry, beside the attributes the developer's own settings already set.
  niwa sends one composed OTEL_RESOURCE_ATTRIBUTES value twice: in the
  --settings document's "env" and in the launched process's environment. Which
  copy wins is Claude Code's choice, so these scenarios work out the worker's
  value under each order it could apply the layers in -- settings first,
  launch environment first, and no launch environment -- and require the same
  facts from all three.

  The fake claude records the launch argv and its OTEL_RESOURCE_ATTRIBUTES; the
  user settings file lives under a temporary CLAUDE_CONFIG_DIR.

  Guide: docs/guides/dispatch-lineage.md

  Scenario: the worker keeps the developer's attributes and gains niwa's under every order
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And the user's Claude settings under a temporary config directory hold resource attributes "team.id=platform,host.name=h1"
    And the dispatching session is "caller-session"
    And a brief file "brief.md" in the workspace root
    When I run "niwa dispatch hello-task --detach --name lineage --skill shirabe:work-on --brief brief.md" from the workspace root
    Then the exit code is 0
    And under every precedence order the worker's resource attributes hold "team.id=platform,host.name=h1" and every niwa attribute

  Scenario: a competing value in the dispatching shell doesn't displace the settings
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And the user's Claude settings under a temporary config directory hold resource attributes "team.id=platform,host.name=h1"
    And the dispatching shell exports resource attributes "team.id=other"
    And the dispatching session is "caller-session"
    And a brief file "brief.md" in the workspace root
    When I run "niwa dispatch hello-task --detach --name lineage --skill shirabe:work-on --brief brief.md" from the workspace root
    Then the exit code is 0
    And under every precedence order the worker's resource attributes hold "team.id=platform,host.name=h1" and every niwa attribute
