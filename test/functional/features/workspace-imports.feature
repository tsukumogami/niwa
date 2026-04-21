Feature: workspace imports via .claude/rules
  niwa apply writes workspace context @imports to .claude/rules/workspace-imports.md
  with absolute paths instead of injecting relative @imports into CLAUDE.md.
  This prevents Claude Code from showing an external-import approval dialog when
  starting a session from a sub-repo directory.

  @critical
  Scenario: create writes workspace-imports.md and does not add relative imports to CLAUDE.md
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "myws" exists
    And the file ".claude/rules/workspace-imports.md" exists in instance "myws"
    And the file ".claude/rules/workspace-imports.md" in instance "myws" contains "workspace-context.md"
    And the file "CLAUDE.md" in instance "myws" does not contain "@workspace-context.md"

  @claude-integration
  Scenario: claude sees workspace context from workspace root but not from sub-repo
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "niwatest-xq7749"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "niwatest-xq7749" exists
    And the repo "tools/myapp" exists in instance "niwatest-xq7749"
    When I run claude -p from instance root "niwatest-xq7749" with prompt:
      """
      Do not read any files. Using only your current context, do you know about
      a workspace named niwatest-xq7749? Answer yes or no only.
      """
    Then the output contains "yes"
    When I run claude -p from repo "tools/myapp" in instance "niwatest-xq7749" with prompt:
      """
      Do not read any files. Using only your current context, do you know about
      a workspace named niwatest-xq7749? Answer yes or no only.
      """
    Then the output does not contain "yes"
