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
  Scenario: niwa reap keeps a deleted session's instance whose linked worktree holds an unpushed commit
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
    And a fake claude for dispatch with session "c1c1c1c1-c1c1-4c1c-8c1c-c1c1c1c1c1c1"
    When I run "niwa dispatch keep-my-worktree --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a linked worktree "wt-feature" of repo "tools/myapp" holds a commit on branch "worktree-work" in the dispatch instance
    And the dispatch session "c1c1c1c1-c1c1-4c1c-8c1c-c1c1c1c1c1c1" is deleted from the Agent View
    And the dispatch instance's contents are recorded
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the dispatch instance's contents are unchanged
    And the error output names the dispatch instance
    And the error output contains "repository check"
    And the error output contains "wt-feature"
    And the error output contains "branch worktree-work"

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

  @critical
  Scenario: niwa reap --verdict-only judges a kept unit and a passing unit and leaves both in place
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
    And a fake claude for dispatch with session "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1"
    When I run "niwa dispatch keep-this --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit is made on branch "unpushed-work" in repo "tools/myapp" of the dispatch instance
    And I keep the dispatch instance as the first dispatch
    And the dispatch session "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1" is deleted from the Agent View
    Given a fake claude for dispatch with session "b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2"
    When I run "niwa dispatch pass-this --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2" is deleted from the Agent View
    And the workspace's units and records are recorded
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the first dispatch instance the verdict "keep"
    And the output gives the dispatch instance the verdict "reap"
    And the output contains "repository check: tools/myapp: commits the clone URL does not show: branch unpushed-work"
    And the output contains "tools/myapp/.git/ unique"
    And the first dispatch instance still exists
    And the dispatch instance still exists
    And the fake claude removed no session
    And the workspace's units and records are unchanged
    When I run "niwa reap --verdict-only --json" from the workspace root
    Then the exit code is 0
    And the output is a niwa.verdicts/v1 document giving the first dispatch instance the verdict "keep"
    And the output is a niwa.verdicts/v1 document giving the dispatch instance the verdict "reap"
    And the first dispatch instance still exists
    And the dispatch instance still exists
    And the fake claude removed no session
    And the workspace's units and records are unchanged
    When I run niwa reap "--verdict-only" naming the dispatch instance from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "reap"
    And the output gives no verdict for the first dispatch instance
    When I run niwa reap "--verdict-only --json" naming the dispatch instance from the workspace root
    Then the exit code is 0
    And the output is a niwa.verdicts/v1 document giving the dispatch instance the verdict "reap"
    And the output gives no verdict for the first dispatch instance
    And the fake claude removed no session
    And the workspace's units and records are unchanged
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the first dispatch instance still exists
    And the dispatch instance no longer exists
    And the error output contains "branch unpushed-work"

  @critical
  Scenario: niwa reap and its verdicts keep a unit whose .niwa is a link, and leave the link's target alone
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
    And a fake claude for dispatch with session "b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3"
    When I run "niwa dispatch link-niwa --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch instance's .niwa is replaced by a link to a directory
    And the dispatch session "b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3" is deleted from the Agent View
    And the link's target directory is recorded
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "hold check: hold could not be read: .niwa is a symlink"
    And the output contains "records check: records could not be read: .niwa is a symlink"
    And the link's target directory is unchanged
    When I run "niwa reap --verdict-only --json" from the workspace root
    Then the exit code is 0
    And the output is a niwa.verdicts/v1 document giving the dispatch instance the verdict "keep"
    And the output contains "hold could not be read: .niwa is a symlink"
    And the link's target directory is unchanged
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output names the dispatch instance
    And the error output contains "hold could not be read: .niwa is a symlink"
    And the link's target directory is unchanged

  @critical
  Scenario: a credential in the clone URL appears in no output of niwa reap or its verdicts
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
    And a fake claude for dispatch with session "b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4"
    When I run "niwa dispatch hold-a-credential --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4" is deleted from the Agent View
    And the workspace configuration's URL for repo "myapp" points at a refused local address with "user:secret" and query ""
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "clone URL unreachable"
    And no output contains "secret"
    When I run "niwa reap --verdict-only --json" from the workspace root
    Then the exit code is 0
    And the output is a niwa.verdicts/v1 document giving the dispatch instance the verdict "keep"
    And the output contains "clone URL unreachable"
    And no output contains "secret"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output names the dispatch instance
    And the error output contains "clone URL unreachable"
    And no output contains "secret"

  @critical
  Scenario: an access_token query value in the clone URL appears in no output of niwa reap or its verdicts
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
    And a fake claude for dispatch with session "b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5"
    When I run "niwa dispatch hold-a-token --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5" is deleted from the Agent View
    And the workspace configuration's URL for repo "myapp" points at a refused local address with "" and query "?access_token=tq7Rv2Lx9Wp"
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "clone URL unreachable"
    And no output contains "tq7Rv2Lx9Wp"
    When I run "niwa reap --verdict-only --json" from the workspace root
    Then the exit code is 0
    And the output is a niwa.verdicts/v1 document giving the dispatch instance the verdict "keep"
    And no output contains "tq7Rv2Lx9Wp"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "clone URL unreachable"
    And no output contains "tq7Rv2Lx9Wp"

  @critical
  Scenario: an AWS key id in an untracked file's name appears in no output of niwa reap or its verdicts
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
    And a fake claude for dispatch with session "b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6"
    When I run "niwa dispatch name-a-key --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a file "tools/myapp/notes-AKIAIOSFODNN7EXAMPLE.txt" is written in the dispatch instance with content "n"
    And the dispatch session "b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "untracked files:"
    And no output contains "AKIAIOSFODNN7EXAMPLE"
    When I run "niwa reap --verdict-only --json" from the workspace root
    Then the exit code is 0
    And the output is a niwa.verdicts/v1 document giving the dispatch instance the verdict "keep"
    And the output contains "untracked files:"
    And no output contains "AKIAIOSFODNN7EXAMPLE"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "untracked files:"
    And no output contains "AKIAIOSFODNN7EXAMPLE"

  @critical
  Scenario: a Slack token in an unpushed branch's name appears in no output of niwa reap or its verdicts
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
    And a fake claude for dispatch with session "b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7"
    When I run "niwa dispatch name-a-branch --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit is made on branch "work-xoxb-2468013579-qwertyuiop" in repo "tools/myapp" of the dispatch instance
    And the dispatch session "b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "commits the clone URL does not show"
    And no output contains "xoxb-"
    When I run "niwa reap --verdict-only --json" from the workspace root
    Then the exit code is 0
    And the output is a niwa.verdicts/v1 document giving the dispatch instance the verdict "keep"
    And the output contains "commits the clone URL does not show"
    And no output contains "xoxb-"
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "commits the clone URL does not show"
    And no output contains "xoxb-"

  # The settings come into doubt after init, which refuses a catch-all
  # pattern itself: the reap is what has to stop on them.

  @critical
  Scenario: niwa reap --verdict-only prints no verdict and fails when the removal settings are invalid
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
    And a fake claude for dispatch with session "b8b8b8b8-b8b8-4b8b-8b8b-b8b8b8b8b8b8"
    When I run "niwa dispatch bad-settings --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "b8b8b8b8-b8b8-4b8b-8b8b-b8b8b8b8b8b8" is deleted from the Agent View
    And the workspace configuration declares the disposable pattern "**"
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is not 0
    And the output is empty
    And the error output has exactly 1 line containing "niwa: removed nothing: the workspace's removal settings are invalid: session.removal.disposable[0]: pattern"
    And the error output contains "matches every path"
    And the dispatch instance still exists
    When I run "niwa reap --verdict-only --json" from the workspace root
    Then the exit code is not 0
    And the output is empty
    And the error output has exactly 1 line containing "niwa: removed nothing: the workspace's removal settings are invalid: session.removal.disposable[0]: pattern"
    And the error output contains "matches every path"
    And the dispatch instance still exists

  # The reclaimed line names an instance reclaimed on idleness, which only an
  # agent answered by activity reaches. Its reason is niwa's own words; the
  # instance name carries the dispatch's --name slug, which can hold a token
  # shape, so that is the secret that reaches the line.

  @critical
  Scenario: the reclaimed line of niwa reap prints a token in the instance name scrubbed
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01a60000-0000-7000-8000-00000000f00d"
    When I run "niwa dispatch abandoned --detach --harness codex --name ghp_r8x2k7m4q9w3e6t1y5u0i2o8p4a6s3" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    Given the codex session was last worked in "48h" ago
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And the error output has exactly 1 line containing "niwa: reclaimed myws+[redacted]-"
    And the error output contains ": its codex session had gone 24h0m0s without being worked in, and nothing was writing to it"
    And no output contains "ghp_"
    And no output contains "r8x2k7m4q9w3e6t1y5u0i2o8p4a6s3"
