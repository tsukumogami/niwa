Feature: a declared permission posture across the generated settings documents
  A workspace declares its permission posture as permissions = "bypass" or
  "ask" in [claude.settings], and can override it for the instance, for one
  repo, or from the developer's personal overlay. niwa turns that declaration
  into Claude Code settings documents in four places: the workspace root and
  the instance root (.claude/settings.json), each repo, and each worktree
  (.claude/settings.local.json). "ask" becomes permissions.defaultMode
  "default"; "bypass" writes no mode, because Claude Code doesn't honor
  bypassPermissions from a project settings file and niwa dispatch passes
  --permission-mode instead.

  Each row below is one scenario from the expected-value table in
  docs/prds/PRD-inert-defaultmode-key.md. The posture comes from the config
  repo's workspace.toml or the personal overlay, written as TOML dotted keys so
  a whole declaration fits in one cell: the workspace and instance keys sit at
  the top of workspace.toml, and the repo key sits inside [repos.app]. Repo
  "app" plays the table's repo X and carries the worktree; repo "other" has no
  override of its own. A row with no personal posture writes an empty overlay,
  which declares nothing.

  The worktree resolves its settings through its own config path, which skips
  the personal overlay, so S9's worktree takes the overlay's "ask" only at the
  next instance apply. The roots and repos are checked as niwa init, niwa
  create, and niwa worktree create left them; then S9's row runs one instance
  apply before its worktree check, and every other row runs none.

  Scenario Outline: <scenario> reaches every settings document with the mode the table gives it
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a source repo "other" exists
    And a config repo "posture" exists with body:
      """
      <workspace_posture>
      <instance_posture>

      [workspace]
      name = "posture"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      <repo_posture>

      [repos.other]
      url = "{repo:other}"
      group = "tools"
      """
    And a personal overlay exists with body:
      """
      <personal_posture>
      """
    When I run niwa init from config repo "posture"
    Then the exit code is 0
    When I run "niwa create posture"
    Then the exit code is 0
    When I call niwa worktree create for repo "app" with purpose "posture-matrix" in instance "posture"
    Then the workspace root, instance "posture", repos "tools/app,tools/other", and last worktree settings documents exist and parse as JSON
    And the workspace root settings document has permissions.defaultMode "<workspace_root>"
    And the instance "posture" settings document has permissions.defaultMode "<instance_root>"
    And the repo "tools/app" settings document in instance "posture" has permissions.defaultMode "<repo_app>"
    And the repo "tools/other" settings document in instance "posture" has permissions.defaultMode "<repo_other>"
    When I run niwa apply <applies_after_worktree> times in instance "posture"
    Then the last worktree settings document has permissions.defaultMode "<worktree>"

    @critical
    Examples: the bypass posture a dispatched worker relies on
      | scenario                   | workspace_posture                      | instance_posture | repo_posture | personal_posture | applies_after_worktree | workspace_root | instance_root | repo_app | repo_other | worktree |
      | S1 (workspace bypass)      | claude.settings.permissions = "bypass" |                  |              |                  | 0                      | none           | none          | none     | none       | none     |

    Examples: the rest of the table
      | scenario                                    | workspace_posture                      | instance_posture                                | repo_posture                           | personal_posture                                          | applies_after_worktree | workspace_root | instance_root | repo_app | repo_other | worktree |
      | S2 (workspace ask)                          | claude.settings.permissions = "ask"    |                                                 |                                        |                                                           | 0                      | default        | default       | default  | default    | default  |
      | S3 (undeclared)                             |                                        |                                                 |                                        |                                                           | 0                      | none           | none          | none     | none       | none     |
      | S4 (workspace bypass, repo ask)             | claude.settings.permissions = "bypass" |                                                 | claude.settings.permissions = "ask"    |                                                           | 0                      | none           | none          | default  | none       | default  |
      | S5 (workspace bypass, instance ask)         | claude.settings.permissions = "bypass" | instance.claude.settings.permissions = "ask"    |                                        |                                                           | 0                      | default        | default       | none     | none       | none     |
      | S6 (workspace ask, instance bypass)         | claude.settings.permissions = "ask"    | instance.claude.settings.permissions = "bypass" |                                        |                                                           | 0                      | none           | none          | default  | default    | default  |
      | S7 (workspace ask, repo bypass)             | claude.settings.permissions = "ask"    |                                                 | claude.settings.permissions = "bypass" |                                                           | 0                      | default        | default       | none     | default    | none     |
      | S8 (undeclared, personal bypass)            |                                        |                                                 |                                        | workspaces.posture.claude.settings.permissions = "bypass" | 0                      | none           | none          | none     | none       | none     |
      | S9 (workspace bypass, personal ask)         | claude.settings.permissions = "bypass" |                                                 |                                        | workspaces.posture.claude.settings.permissions = "ask"    | 1                      | none           | default       | default  | default    | default  |
