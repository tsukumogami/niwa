Feature: niwa stays invisible to the git status of managed repos
  niwa records its ignore coverage in each managed repository's
  .git/info/exclude (a per-repo, never-committed git file), so niwa-authored
  files stay out of the repository's git status without the user adding any
  pattern to their committed .gitignore. The check asserts an empty
  git status --porcelain; it enumerates no filenames, so a newly leaking file
  trips it automatically.

  Design: docs/designs/DESIGN-repo-git-invisibility.md

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the repo "tools/app" exists in instance "ws"
    When I run "niwa apply ws"
    Then the exit code is 0

  Scenario: apply records niwa ignore coverage in the repo git exclude
    Then the git exclude file of repo "app" in instance "ws" contains "*.local*"
    And the git exclude file of repo "app" in instance "ws" contains ".niwa/"

  Scenario: niwa-style output stays invisible without a *.local* gitignore pattern
    When I create file "CLAUDE.local.md" in the working tree of repo "app" in instance "ws"
    And I create file ".niwa/state.json" in the working tree of repo "app" in instance "ws"
    Then the git status of repo "app" in instance "ws" is clean

  Scenario: the invisibility check still catches an uncovered file
    When I create file "leak.txt" in the working tree of repo "app" in instance "ws"
    Then the git status of repo "app" in instance "ws" is not clean

  Scenario: re-apply is idempotent and preserves user exclude content
    When I add line "user-keep.tmp" to the git exclude file of repo "app" in instance "ws"
    And I run "niwa apply ws"
    Then the exit code is 0
    And the git exclude file of repo "app" in instance "ws" contains "user-keep.tmp"
    And the git exclude file of repo "app" in instance "ws" contains "# >>> niwa managed >>>" exactly once
