Feature: niwa installs workspace-root project skills
  End-to-end coverage that `niwa init` materializes the embedded workspace-root
  project skills into the freshly created workspace root. The shipped skill is
  `/dispatch` (internal/workspace/rootskills/dispatch/SKILL.md), installed by
  MaterializeWorkspaceRoot at root altitude so a Claude Code session launched at
  the workspace root loads it from the cwd regardless of plugin enablement.

  Runs offline against the localGitServer bare-repo fake; no GitHub access.

  @critical
  Scenario: niwa init installs the /dispatch skill into the workspace root
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
    And the file ".claude/skills/dispatch/SKILL.md" exists under workspace root "my-team"
    And the file ".claude/skills/dispatch/SKILL.md" under workspace root "my-team" contains "name: dispatch"
    And the file ".claude/skills/dispatch/SKILL.md" under workspace root "my-team" contains "# /dispatch"
