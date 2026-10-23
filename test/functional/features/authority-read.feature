Feature: niwa authority show, decide and niwa list read back a dispatch's grant
  A dispatched session reads its own authority with `niwa authority show`,
  found from the instance its working directory is in and never from the
  session id variable. `niwa authority decide` says who settles a decision
  point, and `niwa list --json` carries each instance's record, so a
  coordinator reads every session it dispatched from one call.

  The fake agents run `niwa authority show` as the first thing a launch or a
  resume does, from their own working directory, and keep what it printed in
  HOME. A launch makes that read before niwa has written the session's
  mapping.

  Background:
    Given a clean niwa environment
    And a local git server is set up

  @critical
  Scenario: a dispatched Claude worker reads its own record as its first command
    # A repo with a commit, so a niwa worktree of it can be created.
    Given a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with authority fixture "r8a" and repo "app"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that reads its authority first, with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    And the worker's "launch" read printed the dispatch instance's record
    And the worker's first read ran before its session mapping existed
    And "niwa authority show --json" from "tools/app" in the dispatch instance prints the worker's first read
    When I create a worktree for repo "app" with purpose "authority-read" in the dispatch instance
    Then "niwa authority show --json" from the worktree prints the worker's first read

  Scenario: a foreground Claude dispatch reads its own record as its first command
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that reads its authority first, with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --grant merge-pr" from the workspace root
    Then the exit code is 0
    And the worker's "launch" read printed the dispatch instance's record
    And the worker's first read ran before its session mapping existed

  Scenario Outline: a Codex worker reads its own record as its first command, and niwa list carries it
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake codex for dispatch that reads its authority first, with session "01d00000-0000-7000-8000-00000000a11c"
    When I run "niwa dispatch hello-task --harness codex --grant merge-pr <flags>" from the workspace root
    Then the exit code is 0
    And the worker's "launch" read printed the dispatch instance's record
    And the worker's first read ran before its session mapping existed
    And "niwa list --json" shows the dispatch instance's authority granting "merge-pr"

    Examples:
      | flags    |
      | --detach |
      |          |

  Scenario: a Codex worker carrying its dispatcher's session id reads its own record, and none outside its instance
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch coordinator --detach --role coordinator" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    And the dispatching session is the first dispatch's session
    And a fake codex for dispatch that reads its authority first, with session "01d00000-0000-7000-8000-00000000a11c"
    When I run "niwa dispatch worker --detach --harness codex --grant merge-pr" from the workspace root
    Then the exit code is 0
    And the worker's "launch" read printed the dispatch instance's record
    Given the fake agent reads its authority from "{workspace-root}" as plain text
    And a fake codex for dispatch that reads its authority first, with session "01d00000-0000-7000-8000-00000000b22c"
    When I run "niwa dispatch wanderer --detach --harness codex --grant merge-pr" from the workspace root
    Then the exit code is 0
    And the worker's "launch" read printed the line "dispatch record: none"

  Scenario: a worker settles a point only when decide names the session
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that reads its authority first and mints a new session per launch
    And the fake agent asks decide about merge-own-pr first
    When I run "niwa dispatch settle --detach" from the workspace root
    Then the exit code is 0
    And the decide marker reads "settled"
    When I run "niwa dispatch ask --detach --level strict" from the workspace root
    Then the exit code is 0
    And the decide marker reads "waiting-for-person"

  Scenario: a coordinator reads the posture of every session it dispatched from one niwa list call
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that reads its authority first and mints a new session per launch
    When I run "niwa dispatch coordinator --detach --role coordinator" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    And I keep the dispatch instance as "coordinator"
    When I dispatch with "--grant merge-pr" from inside the first dispatch instance
    Then the exit code is 0
    Given I keep the dispatch instance as "first-worker"
    When I dispatch with "--level strict" from inside the first dispatch instance
    Then the exit code is 0
    Given I keep the dispatch instance as "second-worker"
    Then one "niwa list --json" carries each kept dispatch's record as its authority object, with "coordinator" the parent of "first-worker" and "second-worker"

  Scenario: show and decide stay fast in a workspace with many session mappings
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given 250 session mappings exist in the workspace
    Then the median of ten runs of "niwa authority show" from inside the dispatch instance is under 200 ms
    And the median of ten runs of "niwa authority decide merge-own-pr" from inside the dispatch instance is under 200 ms
