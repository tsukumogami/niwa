Feature: niwa reap refuses repository configuration that names a program
  The removal rule reads each repository's configuration before any other
  git call in it, and before any other call in any repository of the unit.
  A key that names a program git might run (a filter, diff or merge driver,
  a credential helper, a signing program, an fsmonitor hook, or any key
  named like one) keeps the unit with the key named, and nothing more runs
  there. The read runs again right before every git status, so a key written
  in between keeps the unit too, as "changed during the pass".

  Each script below writes a marker file when it runs; a scenario passes
  only when no marker exists afterwards.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
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
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"

  @critical
  Scenario: niwa reap and its verdicts run none of the hook, ssh, pager and credential programs a repository names
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c701"
    When I run "niwa dispatch config-programs --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance selects filter x for every path and has a stat-dirty tracked file
    And repo "tools/myapp" of the dispatch instance sets "core.fsmonitor" to a marker-writing script
    And repo "tools/myapp" of the dispatch instance sets "core.hooksPath" to a marker-writing script
    And repo "tools/myapp" of the dispatch instance sets "core.sshCommand" to a marker-writing script
    And repo "tools/myapp" of the dispatch instance sets "core.pager" to a marker-writing script
    And repo "tools/myapp" of the dispatch instance sets "credential.helper" to a marker-writing script
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c701" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "repository configuration names a program: core.fsmonitor"
    And no marker-writing script ran
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "repository configuration names a program: core.fsmonitor"
    And no marker-writing script ran

  @critical
  Scenario: a filter driver in a repository's configuration keeps the unit with the key named
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c702"
    When I run "niwa dispatch config-filter --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance selects filter x for every path and has a stat-dirty tracked file
    And repo "tools/myapp" of the dispatch instance sets "filter.x.clean" to a marker-writing script
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c702" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "tools/myapp: repository configuration names a program: filter.x.clean"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "tools/myapp: repository configuration names a program: filter.x.clean"
    And no marker-writing script ran

  @critical
  Scenario: a filter driver set only in config.worktree keeps the unit with the key named
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c703"
    When I run "niwa dispatch config-worktree --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance selects filter x for every path and has a stat-dirty tracked file
    And repo "tools/myapp" of the dispatch instance sets "filter.x.clean" in its config.worktree to a marker-writing script
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c703" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "tools/myapp: repository configuration names a program: filter.x.clean"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "tools/myapp: repository configuration names a program: filter.x.clean"
    And no marker-writing script ran

  @critical
  Scenario: a signed stash with log.showSignature on runs no signing program
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c704"
    When I run "niwa dispatch config-signed-stash --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance holds a signed commit in refs/stash
    And repo "tools/myapp" of the dispatch instance sets "log.showSignature" to "true"
    And repo "tools/myapp" of the dispatch instance sets "gpg.program" to a marker-writing script
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c704" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "tools/myapp: repository configuration names a program: gpg.program"
    And no marker-writing script ran
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "tools/myapp: repository configuration names a program: gpg.program"
    And no marker-writing script ran

  @critical
  Scenario Outline: a program-naming key written in another case is matched under its canonical name
    Given a fake claude for dispatch with session "<session>"
    When I run "niwa dispatch config-case --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance selects filter x for every path and has a stat-dirty tracked file
    And repo "tools/myapp" of the dispatch instance sets "<written>" to a marker-writing script
    And the dispatch session "<session>" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "repository configuration names a program: <canonical>"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "repository configuration names a program: <canonical>"
    And no marker-writing script ran

    Examples:
      | written           | canonical         | session                              |
      | Core.FSMonitor    | core.fsmonitor    | c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c705 |
      | Credential.Helper | credential.helper | c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c706 |

  @critical
  Scenario: a filter planted after the first configuration read and before the rule's status keeps the unit
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c707"
    When I run "niwa dispatch config-plant-rule --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance selects filter x for every path and has a stat-dirty tracked file
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c707" is deleted from the Agent View
    And a marker-writing filter x is planted between the rule's configuration read and its status
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "tools/myapp: changed during the pass: repository configuration names a program: filter.x.clean"
    And no marker-writing script ran
    When the planted filter is removed from repo "tools/myapp" of the dispatch instance
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "tools/myapp: changed during the pass: repository configuration names a program: filter.x.clean"
    And no marker-writing script ran

  @critical
  Scenario: a filter planted after the re-read's configuration read and before its status keeps the unit
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c708"
    When I run "niwa dispatch config-plant-reread --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance selects filter x for every path and has a stat-dirty tracked file
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c708" is deleted from the Agent View
    And a marker-writing filter x is planted between the re-read's configuration read and its status
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output names the dispatch instance
    And the error output contains "changed during the pass: repository configuration names a program: filter.x.clean in tools/myapp"
    And no tombstone remains in the workspace root
    And no marker-writing script ran

  @critical
  Scenario: a partial clone keeps the unit on a git older than 2.45
    Given the host's git is older than 2.45
    And a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c709"
    When I run "niwa dispatch config-partial --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance sets "remote.origin.promisor" to "true"
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c709" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "tools/myapp: partial clone needs git 2.45: remote.origin.promisor"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "tools/myapp: partial clone needs git 2.45: remote.origin.promisor"

  @critical
  Scenario: the removal path never starts git from an empty PATH entry or one linked into the workspace root
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c710"
    When I run "niwa dispatch config-path --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit is made on branch "unpushed-work" in repo "tools/myapp" of the dispatch instance
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c710" is deleted from the Agent View
    And niwa runs with a PATH that starts with an empty entry and a directory linked into the workspace root, each holding a planted git, then a directory whose git records each call
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "branch unpushed-work"
    And no planted git ran
    And the recording git ran
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "branch unpushed-work"
    And no planted git ran

  @critical
  Scenario: a repository git refuses for its ownership keeps the unit with git's refusal line
    Given a fake claude for dispatch with session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c711"
    When I run "niwa dispatch config-owner --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance is owned by a second uid
    And the dispatch session "c7c7c7c7-c7c7-4c7c-8c7c-c7c7c7c7c711" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "detected dubious ownership"
    And no output contains "safe.directory"
