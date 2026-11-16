@mirror-repair
Feature: niwa restores a deleted or damaged host mirror by itself
  A workspace's clones can borrow git objects from one bare mirror per repo
  under ~/.niwa, through their alternates file. No create borrows yet, so these
  scenarios turn two of niwa's own clones into borrowers the way a reference
  clone would be made, then take the mirror away or damage it and run an
  ordinary command. Only niwa writes the mirror.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with 3 commits
    And a config repo "myws" exists with body:
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

  # Each row deletes the mirror and runs one command. The SessionStart hook's
  # stderr reaches nobody, so its rows find the notice in the hook's context;
  # the session that isn't a background worker gets no instance, only the
  # notice. niwa worktree create checks before git worktree add, which could
  # not check the borrower's files out with the mirror gone.
  Scenario Outline: A deleted mirror is restored by <command>
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the borrower of "app" in instance "myws" has an unpushed commit and an uncommitted edit
    And I snapshot the borrowers of "app"
    And I count the mirror log lines
    When I delete the mirror of "app" and its store
    Then git fsck reports missing objects in the borrower of "app" in instance "myws"
    When I restore the mirror by running <command>
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    And the state file of "app" records the linked store and its packs
    And the mirror of "app" has the remote URL of source repo "app"
    And each borrower of "app" reads its default branch to the root and fsck is clean
    And the borrowers of "app" match their snapshot apart from what <command> adds
    And the unpushed commit and uncommitted edit are intact
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "<logged>"
    And <command> reported the restore of "app"
    And no restore leftovers remain under the mirrors
    And the workspace key directory has mode 0700 and the mirror lock has mode 0600
    And nothing mirror-related exists outside the sandbox home
    When I remember the mirror link of "app"
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the mirror log gained only a healthy check line for "app"
    And the mirror link of "app" is unchanged

    Examples:
      | command                                    | logged    |
      | niwa create of a third instance            | create    |
      | niwa apply at the root                     | apply     |
      | niwa dispatch                              | dispatch  |
      | a SessionStart hook that provisions        | from-hook |
      | a SessionStart hook that doesn't provision | from-hook |
      | niwa worktree create in myws               | worktree  |

  Scenario: A workspace with no borrowers gets no mirror
    When I run "niwa create myws"
    Then the exit code is 0
    And the error output carries no mirror warning
    And no mirror directory exists under the sandbox home
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And no mirror directory exists under the sandbox home
    When I run "niwa worktree create app" in instance "myws"
    Then the exit code is 0
    And the error output carries no mirror warning
    When I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook printed nothing
    And the error output carries no mirror warning
    And no workspace key directory exists under the sandbox home

  Scenario Outline: A damaged mirror is restored by an ordinary command
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    And the state file of "app" records the linked store and its packs
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the borrower of "app" in instance "myws" has an unpushed commit and an uncommitted edit
    And I snapshot the borrowers of "app"
    And I remember the mirror link of "app"
    And I count the mirror log lines
    When I damage the store of "app" by <damage>
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries exactly the mirror warning "mirror app: fault <fault>, result restored"
    And the mirror log gained a check line and a restore line for "app" with fault "<fault>" and result "restored"
    And the mirror link of "app" moved to a new store
    And the mirror of "app" is a link into its store
    And the state file of "app" records the linked store and its packs
    And the mirror of "app" has the remote URL of source repo "app"
    And only the linked store of "app" remains
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And the borrowers of "app" match their snapshot
    And the unpushed commit and uncommitted edit are intact
    When I remember the mirror link of "app"
    And I remember the state file of "app"
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the mirror log gained only a healthy check line for "app"
    And the mirror link of "app" is unchanged
    And the state file of "app" is unchanged

    Examples:
      | damage                      | fault           |
      | emptying its object store   | objects-empty   |
      | deleting its pack           | pack-missing    |
      | truncating its pack to half | pack-changed    |
      | deleting its HEAD           | repo-unreadable |
