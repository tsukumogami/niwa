@reference-clones
Feature: new clones borrow git objects from the host mirror
  In the test build a new clone of a repo borrows objects from the repo's
  host mirror under ~/.niwa, seeding the mirror first when there is none.
  Its alternates file holds one line, the mirror's stable path and
  "/objects", never the store the stable path links to. Whenever borrowing
  can't happen the repo is cloned in full and the command prints a warning
  naming the repo and the reason. share_git_history turns borrowing off for
  the workspace or one repo, and a clone that already exists is never
  changed.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with 3 commits
    And a source repo "lib" exists with 2 commits
    And a source repo "docs" exists with 2 commits

  Scenario: A first create seeds a mirror per repo and every clone borrows through its stable path
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
    And the error output carries no mirror warning
    And the error output names no clone made in full
    And the mirror of "app" is a link into its store
    And the mirror of "lib" is a link into its store
    And the mirror of "docs" is a link into its store
    And every clone in instance "myws" borrows from its mirror
    And every clone in instance "myws" reads its default branch and fsck is clean
    And no clone in instance "myws" holds a pack a mirror store holds or a gc or maintenance key
    And nothing mirror-related exists outside the sandbox home

  Scenario Outline: A new clone borrows when <command> provisions it
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
    When <command> provisions a new clone
    Then the exit code is 0
    And the error output names no clone made in full
    And the workspace holds <instances> instances with <clones> clones in all
    And every clone in every instance borrows from its mirror

    Examples:
      | command                                        | instances | clones |
      | niwa apply after a repo is added to the config | 1         | 3      |
      | niwa dispatch                                  | 2         | 4      |
      | a SessionStart hook                            | 2         | 4      |

  Scenario: niwa init that creates an instance makes its clone borrow
    Given a GitHub fake is configured
    And the GitHub fake serves "owner/foo" at ref "HEAD" empty
    And git fetches "git@github.com:owner/foo.git" from source repo "app"
    When I run "niwa init bar --from owner/foo --bootstrap" from workspace root
    # init creates the instance first; its later session step stops on this
    # fixture whatever the clone is (git add refuses the ignored .niwa), and
    # that step never touches the instance it made.
    Then the error output contains "created bar (1 repo)"
    And the error output names no clone made in full
    And every clone of workspace "bar" borrows from its mirror

  Scenario: A create after the mirror was deleted restores it before cloning and borrows from it
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
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    When I delete the mirror of "app" and its store
    And I count the mirror log lines
    And I run "niwa create myws"
    Then the exit code is 0
    And the error output carries one mirror warning for "app" with fault "missing" and result "restored"
    And the error output shows the restore of "app" before the instance was created
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "create"
    And the restore line for "app" probed the clones of "tools/app" in "myws" and "myws-2"
    And the mirror of "app" is a link into its store
    And the clone of "tools/app" in instance "myws-3" borrows from the mirror of "app"
    And each borrower of "app" reads its default branch to the root and fsck is clean

  Scenario: A borrowing clone matches a full clone, and a failed borrowing clone leaves only a full one
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
    And every clone in instance "myws" borrows from its mirror
    When I set env "NIWA_TEST_FAULT" to "error:injected@borrow-clone"
    And I run "niwa create myws"
    Then the exit code is 0
    And the error output warns that "app" was cloned in full with reason "borrow-clone-failed"
    And the error output warns that "lib" was cloned in full with reason "borrow-clone-failed"
    And no clone in instance "myws-2" borrows
    And nothing but the clones is left in the group "tools" of instance "myws-2"
    And every clone in instance "myws-2" reads its default branch and fsck is clean
    And the clones of "tools/app" in instances "myws" and "myws-2" have the same origin, refs, local config and hooks
    And the clones of "tools/lib" in instances "myws" and "myws-2" have the same origin, refs, local config and hooks

  Scenario: A repo whose mirror can't be seeded is cloned in full and the others still borrow
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
    When I remove instance "myws" from disk
    And I remove every trace of the mirror of "app"
    And the mirror of "app" can't be seeded
    And I run "niwa create myws"
    Then the exit code is 0
    And the error output warns that "app" was cloned in full with reason "mirror-seed-failed"
    And the error output names no clone made in full other than "app"
    And the clone of "tools/app" in instance "myws" does not borrow
    And the clone of "tools/lib" in instance "myws" borrows from the mirror of "lib"
    And the clone of "tools/docs" in instance "myws" borrows from the mirror of "docs"
    And every clone in instance "myws" reads its default branch and fsck is clean

  Scenario: A mirror another command holds past the wait is cloned in full as mirror-busy
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
    When I set env "NIWA_TEST_MIRROR_LOCK_WAIT" to "1s"
    And I run "niwa create myws" from workspace root while the mirror lock of "app" is held
    Then the exit code is 0
    And the error output warns that "app" was cloned in full with reason "mirror-busy"
    And the clone of "tools/app" in instance "myws-2" does not borrow
    And the clone of "tools/lib" in instance "myws-2" borrows from the mirror of "lib"

  Scenario: The SessionStart hook clones a held mirror's repo in full after its ten-second wait and says so in its context
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
    When a SessionStart hook that provisions runs while the mirror lock of "app" is held
    Then the exit code is 0
    And the hook took at least 10 seconds
    And the hook's context says "app" was cloned in full with reason "mirror-busy"
    And the clone of "tools/app" in the hook's instance does not borrow

  Scenario: The workspace setting off makes full clones and no mirror
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      share_git_history = false

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
    And the error output names no clone made in full
    And no clone in instance "myws" borrows
    And no mirror directory exists under the sandbox home

  Scenario: The workspace off and one repo on borrows that repo only
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      share_git_history = false

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      share_git_history = true

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the error output names no clone made in full
    And the clone of "tools/app" in instance "myws" borrows from the mirror of "app"
    And the clone of "tools/lib" in instance "myws" does not borrow
    And no mirror of "lib" exists

  Scenario: The workspace on and one repo off makes that repo a full clone with no mirror and no notice
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
      share_git_history = false
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the error output says nothing about "lib"
    And the clone of "tools/app" in instance "myws" borrows from the mirror of "app"
    And the clone of "tools/lib" in instance "myws" does not borrow
    And no mirror of "lib" exists

  Scenario: Three creates started together on a host with no mirror share one store per repo
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
    When I run "niwa create myws --name r{n}" from workspace root 3 times at once
    Then every create that ran at once exited 0
    And each of "app, lib" has one stable path and one store
    And every clone of the creates that ran at once borrows or is named as cloned in full in that create's output

  Scenario: Clones made with borrowing off stay full after borrowing is turned on, sync and apply
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      share_git_history = false

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    When the config repo "myws" is force-pushed to:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And I add a branch "topic" with one commit to source repo "app"
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And no clone in instance "myws" borrows
    And every clone in instance "myws" reads its default branch and fsck is clean

  Scenario: A borrowing instance holds no pack a mirror store holds after create, apply and a sync
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
    And no clone in instance "myws" holds a pack a mirror store holds or a gc or maintenance key
    When I add a branch "topic" with one commit to source repo "app"
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And every clone in instance "myws" borrows from its mirror
    And no clone in instance "myws" holds a pack a mirror store holds or a gc or maintenance key
    And every clone in instance "myws" reads its default branch and fsck is clean
