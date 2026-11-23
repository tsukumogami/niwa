@reference-clones @dissociate
Feature: niwa dissociate makes an instance independent of the host mirrors
  niwa dissociate copies every object an instance's clones borrow from the
  workspace's host mirrors into the clones and then removes their alternates
  files, each only after git fsck passes without it. It refuses with nothing
  changed when the instance's lock is held or free space is short, and a
  failure part way leaves every clone borrowing. A repo turned off keeps its
  mirror checked by niwa apply until its last borrower is dissociated. Every
  borrowing instance here is made by niwa create in the test build.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with 3 commits
    And a source repo "lib" exists with 2 commits
    And a source repo "docs" exists with 2 commits

  @critical
  Scenario: A borrowing instance with a worktree reads its whole history with the mirrors deleted
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"

      [repos.docs]
      url = "{repo:docs}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And every clone in instance "myws" borrows from its mirror
    And no clone in instance "myws" holds a pack a mirror store holds or a gc or maintenance key
    When I run "niwa worktree create app" in instance "myws"
    Then the exit code is 0
    And instance "myws" has 1 worktree
    When I run "niwa dissociate myws" from workspace root
    Then the exit code is 0
    And the output contains "dissociated tools/app"
    And the output contains "dissociated tools/docs"
    And the output contains "dissociated tools/lib"
    When I remove every trace of the mirror of "app"
    And I remove every trace of the mirror of "lib"
    And I remove every trace of the mirror of "docs"
    Then every clone and worktree in instance "myws" reads all its history and fsck is clean
    And every clone in instance "myws" reads its default branch and fsck is clean
    And no clone in instance "myws" borrows
    And no clone in instance "myws" has an alternates file left aside
    And no clone in instance "myws" has a gc or maintenance key

  Scenario: Run from inside the instance it frees only the borrowers, and a second run has nothing to change
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"

      [repos.docs]
      url = "{repo:docs}"
      group = "tools"
      share_git_history = false
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the clone of "tools/app" in instance "myws" borrows from the mirror of "app"
    And the clone of "tools/lib" in instance "myws" borrows from the mirror of "lib"
    And the clone of "tools/docs" in instance "myws" does not borrow
    When I remember the clone of "tools/docs" in instance "myws"
    And I run "niwa dissociate" in instance "myws"
    Then the exit code is 0
    And the output contains "dissociated tools/app"
    And the output contains "dissociated tools/lib"
    And the output does not contain "tools/docs"
    And the clone of "tools/docs" in instance "myws" is byte-for-byte unchanged
    And no clone in instance "myws" borrows
    When I run "niwa dissociate" in instance "myws"
    Then the exit code is 0
    And the output contains "nothing to change"
    And the output does not contain "dissociated"

  Scenario: A held instance lock refuses with nothing changed
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    When I remember the alternates files and pack directories of instance "myws"
    And I run "niwa dissociate myws" from workspace root while the instance lock of "myws" is held
    Then the exit code is not 0
    And the error output contains "lock is held by niwa destroy"
    And the error output contains "nothing was changed"
    And the alternates files and pack directories of instance "myws" are unchanged
    And every clone in instance "myws" borrows from its mirror

  Scenario: Free space below the mirror pack sizes refuses before any copy
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    When the state file of "app" records its packs at 1152921504606846976 bytes each
    And I remember the alternates files and pack directories of instance "myws"
    And I run "niwa dissociate myws" from workspace root
    Then the exit code is not 0
    And the error output contains "not enough free space"
    And the alternates files and pack directories of instance "myws" are unchanged
    And every clone in instance "myws" borrows from its mirror

  Scenario: A copy failing for the second of three clones leaves every clone borrowing
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"

      [repos.docs]
      url = "{repo:docs}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    # The clones are copied in order (app, docs, lib), so docs is the second.
    When I set env "NIWA_TEST_FAULT" to "error:injected@dissociate-copy:tools/docs"
    And I run "niwa dissociate myws" from workspace root
    Then the exit code is not 0
    And the error output contains "tools/docs"
    And the output does not contain "dissociated"
    And every clone in instance "myws" borrows from its mirror
    And no clone in instance "myws" has an alternates file left aside
    And every clone in instance "myws" reads its default branch and fsck is clean

  Scenario: A dissociate stopped with an alternates file set aside is finished by the next run
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    When the dissociate of the clone of "tools/app" in instance "myws" stopped with its alternates set aside
    # app's objects were never copied, so its check fails and the file goes
    # back; the copy fault then stops the run with app borrowing again.
    And I set env "NIWA_TEST_FAULT" to "error:injected@dissociate-copy"
    And I run "niwa dissociate myws" from workspace root
    Then the exit code is not 0
    And the error output contains "put back the alternates file of tools/app"
    And every clone in instance "myws" borrows from its mirror
    And no clone in instance "myws" has an alternates file left aside
    When I set env "NIWA_TEST_FAULT" to ""
    And I run "niwa dissociate myws" from workspace root
    Then the exit code is 0
    And the output contains "dissociated tools/app"
    And the output contains "dissociated tools/lib"
    When I remove every trace of the mirror of "app"
    And I remove every trace of the mirror of "lib"
    Then every clone and worktree in instance "myws" reads all its history and fsck is clean
    And no clone in instance "myws" borrows
    And no clone in instance "myws" has an alternates file left aside

  Scenario: A repo turned off keeps its mirror checked by apply until its last borrower is dissociated
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And every clone in instance "myws" borrows from its mirror
    When the config repo "myws" is force-pushed to:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      share_git_history = false
      """
    And I delete the mirror of "app" and its store
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "apply"
    And the mirror of "app" is a link into its store
    When I run "niwa dissociate myws" from workspace root
    Then the exit code is 0
    And the output contains "dissociated tools/app"
    When I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror log gained no line for "app"
    And the mirror of "app" is a link into its store
    When I remove every trace of the mirror of "app"
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror log gained no line for "app"
    And no mirror of "app" exists
    And every clone in instance "myws" reads its default branch and fsck is clean
