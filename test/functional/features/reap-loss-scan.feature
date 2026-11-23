Feature: niwa reap keeps a deleted session's instance that holds unpushed work
  A dispatched session can be deleted by hand while its instance still holds
  work nobody pushed. reap runs each such unit through the removal rule before
  it removes anything: every ref in the instance's clone is compared with the
  live refs ls-remote reports for the clone URL the workspace configuration
  gives, and a ref the clone URL doesn't show keeps the unit. A kept unit is
  printed with the check that kept it and left byte for byte as it was.

  The suite's repositories are local bare repositories reached by file URL,
  which the rule's git calls refuse outside the test build, so each scenario
  admits them with NIWA_TEST_REMOVAL_FILE_REMOTES.

  @critical
  Scenario: niwa reap keeps a deleted session's instance whose clone holds an unpushed commit
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
    And a fake claude for dispatch with session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa dispatch keep-my-work --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit is made on branch "unpushed-work" in repo "tools/myapp" of the dispatch instance
    And the dispatch session "dddddddd-dddd-4ddd-8ddd-dddddddddddd" is deleted from the Agent View
    And the dispatch instance's contents are recorded
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the dispatch instance's contents are unchanged
    And the error output names the dispatch instance
    And the error output contains "repository check"
    And the error output contains "branch unpushed-work"
    And a dispatch-origin mapping exists for session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa create" from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "branch unpushed-work"

  @critical
  Scenario: niwa reap removes a deleted session's instance whose clone holds nothing unpushed
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
    And a fake claude for dispatch with session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
    When I run "niwa dispatch nothing-to-keep --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  @critical
  Scenario: the reap before niwa dispatch keeps the unit and the dispatch still runs
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
    And a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch first-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit is made on branch "unpushed-work" in repo "tools/myapp" of the dispatch instance
    And I keep the dispatch instance as the first dispatch
    And the dispatch session for the first dispatch is deleted from the Agent View
    And I run "niwa dispatch second-task --detach" from the workspace root
    Then the exit code is 0
    And the first dispatch instance still exists
    And the error output contains "branch unpushed-work"

  @critical
  Scenario: niwa reap removes a unit whose only leftovers are build trees, generated, pattern and ignored files
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
    And a fake claude for dispatch with session "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"
    When I run "niwa dispatch build-things --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a file "tools/myapp/.git/info/exclude" is written in the dispatch instance with content "target/\n.env\n"
    And a file "tools/myapp/target/CACHEDIR.TAG" is written in the dispatch instance with content "Signature: 8a477f597d28d172789f06886806bc55\n"
    And a file "tools/myapp/target/debug/app.bin" is written in the dispatch instance with content "binary"
    And a file "tools/myapp/.env" is written in the dispatch instance with content "A=1"
    And a file "tools/myapp/notes.swp" is written in the dispatch instance with content "swap"
    And a file ".DS_Store" is written in the dispatch instance with content "d"
    And the dispatch session "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains

  @critical
  Scenario: niwa reap keeps the same unit once it holds one unique file
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
    And a fake claude for dispatch with session "a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a2a2"
    When I run "niwa dispatch build-things --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a file "tools/myapp/.git/info/exclude" is written in the dispatch instance with content "target/\n.env\n"
    And a file "tools/myapp/target/CACHEDIR.TAG" is written in the dispatch instance with content "Signature: 8a477f597d28d172789f06886806bc55\n"
    And a file "tools/myapp/.env" is written in the dispatch instance with content "A=1"
    And a file ".DS_Store" is written in the dispatch instance with content "d"
    And a file "results.csv" is written in the dispatch instance with content "1,2"
    And the dispatch session "a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a2a2" is deleted from the Agent View
    And the dispatch instance's contents are recorded
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the dispatch instance's contents are unchanged
    And the error output names the dispatch instance
    And the error output contains "outside check"
    And the error output contains "results.csv"

  @critical
  Scenario: niwa reap removes a unit whose only leftover is an empty directory at the instance root
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
    And a fake claude for dispatch with session "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a3a3"
    When I run "niwa dispatch leave-a-dir --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When an empty directory "scratch/empty" is made in the dispatch instance
    And the dispatch session "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a3a3" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains

  @critical
  Scenario: niwa reap keeps a unit with a directory it can't read, naming the path and the error
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
    And a fake claude for dispatch with session "a4a4a4a4-a4a4-4a4a-8a4a-a4a4a4a4a4a4"
    When I run "niwa dispatch lock-a-dir --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the directory "tools/myapp/locked" in the dispatch instance is made unreadable
    And the dispatch session "a4a4a4a4-a4a4-4a4a-8a4a-a4a4a4a4a4a4" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "tools/myapp/locked"
    And the error output contains "permission denied"
    When the directory "tools/myapp/locked" in the dispatch instance is made readable again

  @critical
  Scenario: niwa reap reports untracked files a repository's own settings would hide
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
    And a fake claude for dispatch with session "a5a5a5a5-a5a5-4a5a-8a5a-a5a5a5a5a5a5"
    When I run "niwa dispatch hide-a-file --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When git "config status.showUntrackedFiles no" is run in repo "tools/myapp" of the dispatch instance
    And a file "tools/myapp/notes.txt" is written in the dispatch instance with content "n"
    And the dispatch session "a5a5a5a5-a5a5-4a5a-8a5a-a5a5a5a5a5a5" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "untracked files: notes.txt"

  @critical
  Scenario: niwa reap never lets the per-user ignore file hide an untracked file
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
    And a fake claude for dispatch with session "a6a6a6a6-a6a6-4a6a-8a6a-a6a6a6a6a6a6"
    When I run "niwa dispatch ignore-a-file --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a file ".config/git/ignore" is written under the home directory with content "notes.txt\n"
    And a file "tools/myapp/notes.txt" is written in the dispatch instance with content "n"
    And the dispatch session "a6a6a6a6-a6a6-4a6a-8a6a-a6a6a6a6a6a6" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "untracked files: notes.txt"

  @critical
  Scenario: niwa reap sees a tracked file rewritten to its old size and modification time
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
    And a fake claude for dispatch with session "a7a7a7a7-a7a7-4a7a-8a7a-a7a7a7a7a7a7"
    When I run "niwa dispatch rewrite-a-file --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When git "config core.trustctime false" is run in repo "tools/myapp" of the dispatch instance
    And git "config core.checkStat minimal" is run in repo "tools/myapp" of the dispatch instance
    And a tracked file "stat.txt" in repo "tools/myapp" of the dispatch instance is pushed, then rewritten keeping its size and modification time
    And the dispatch session "a7a7a7a7-a7a7-4a7a-8a7a-a7a7a7a7a7a7" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "modified tracked files"

  @critical
  Scenario: niwa reap keeps a deleted session's instance a background process still works in
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
    And a fake claude for dispatch with session "a8a8a8a8-a8a8-4a8a-8a8a-a8a8a8a8a8a8"
    When I run "niwa dispatch run-in-background --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a background process is started in the dispatch instance
    And the dispatch session "a8a8a8a8-a8a8-4a8a-8a8a-a8a8a8a8a8a8" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "liveness check"
    And the error output names the background process
    When the background process is stopped
