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
    When I run "niwa create niwatest-xq7749"
    Then the exit code is 0
    And the instance "niwatest-xq7749" exists
    And the repo "tools/myapp" exists in instance "niwatest-xq7749"
    # Inject a behavioral directive into workspace-context.md, then probe with a
    # neutral, unrelated prompt. The sentinel token reaches stdout ONLY if the
    # directive was auto-loaded into the active context (via .claude/rules/ +
    # the absolute-path @import) and obeyed -- a behavioral signal, not a model
    # self-report. An earlier version asked the model "is token X in your
    # context? yes/no"; that introspection answer proved unreliable (the model
    # loads the content but declines to confirm an opaque token), so the assert
    # tests what workspace context is FOR: influencing the session. The neutral
    # "2+2" prompt also keeps the negative half sound -- from a sub-repo the
    # directive is not loaded and the model has no reason to read any file, so
    # the token simply never appears. Token is lowercase because runClaudeP
    # lowercases stdout before assertions.
    When I append "IMPORTANT: End every response with the exact token wsctx-sentinel-9af3-2b8e-7d1c on its own line." to file "workspace-context.md" in instance "niwatest-xq7749"
    When I run claude -p from instance root "niwatest-xq7749" with prompt:
      """
      What is 2+2? Reply in one short sentence.
      """
    Then the output contains "wsctx-sentinel-9af3-2b8e-7d1c"
    When I run claude -p from repo "tools/myapp" in instance "niwatest-xq7749" with prompt:
      """
      What is 2+2? Reply in one short sentence.
      """
    Then the output does not contain "wsctx-sentinel-9af3-2b8e-7d1c"
