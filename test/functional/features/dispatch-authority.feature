Feature: niwa dispatch: the authority level is resolved, refused and read back
  A dispatch runs at the narrowest of the workspace's [workspace].authority_level
  (autonomous when unset), the machine's [global].authority_level, and
  --authority. A --authority wider than the other two is refused before any
  instance exists. A launching dispatch prints one line naming the level and
  where it came from, writes the level into the instance and its session
  mapping, and `niwa list --json` reads it back.

  The fake claude is the one the other dispatch scenarios use; no real claude,
  daemon or network is needed.

  Design: docs/designs/DESIGN-authority-level.md

  @critical
  Scenario: a --authority wider than the workspace is refused and leaves nothing behind
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      authority_level = "supervised"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"
    When I run "niwa dispatch wider-task --detach --authority autonomous" from the workspace root
    Then the exit code is 1
    And the error output contains "--authority autonomous is wider than this dispatch's ceiling, supervised from workspace"
    And the error output has exactly 0 lines containing "niwa dispatch: authority level"
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  Scenario: a --authority wider than the machine ceiling is refused
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a2a2"
    And the niwa machine config global table contains:
      """
      authority_level = "strict"
      """
    When I run "niwa dispatch wider-task --detach --authority supervised" from the workspace root
    Then the exit code is 1
    And the error output contains "--authority supervised is wider than this dispatch's ceiling, strict from machine"
    And the error output has exactly 0 lines containing "niwa dispatch: authority level"
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  @critical
  Scenario: a dispatch records the workspace's level and niwa list --json reads it back
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      authority_level = "strict"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a3a3"
    When I run "niwa dispatch strict-task --detach" from the workspace root
    Then the exit code is 0
    And the error output has exactly 1 line containing "niwa dispatch: authority level strict from workspace"
    And the dispatch instance holds an authority record with level "strict" from "workspace"
    And the dispatch mapping for session "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a3a3" records authority level "strict" from "workspace"
    When I run "niwa list --json" from the workspace root
    Then the exit code is 0
    And the list JSON reports the dispatch instance at authority level "strict" from "workspace"

  Scenario: a narrower --authority is accepted and recorded as the flag
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "a4a4a4a4-a4a4-4a4a-8a4a-a4a4a4a4a4a4"
    When I run "niwa dispatch narrow-task --detach --authority supervised" from the workspace root
    Then the exit code is 0
    And the error output has exactly 1 line containing "niwa dispatch: authority level supervised from flag"
    And the dispatch mapping for session "a4a4a4a4-a4a4-4a4a-8a4a-a4a4a4a4a4a4" records authority level "supervised" from "flag"
    When I run "niwa list --json" from the workspace root
    Then the exit code is 0
    And the list JSON reports the dispatch instance at authority level "supervised" from "flag"
