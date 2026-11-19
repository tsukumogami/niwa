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
    And the mirror log gained a healthy check line and a refresh line for "app" from "apply"
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
    And the mirror log gained a healthy check line and a refresh line for "app" from "apply"
    And the mirror link of "app" is unchanged
    And the state file of "app" is unchanged apart from last_refresh

    Examples:
      | damage                      | fault           |
      | emptying its object store   | objects-empty   |
      | deleting its pack           | pack-missing    |
      | truncating its pack to half | pack-changed    |
      | deleting its HEAD           | repo-unreadable |

  # The lock is held the way another niwa command's restore would hold it, so
  # both applies find the mirror missing and wait; once it's released one
  # restores and the other finds the mirror healthy under the lock. The second
  # apply starts a second after the first, past the config snapshot refresh
  # every apply makes at the root: two commands that re-materialize the root's
  # config snapshot in the same instant still race, and that open race is
  # tracked as niwa#297.
  Scenario: Two commands restore one broken mirror once
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
    And I hold the mirror lock of "app" while "niwa apply" runs in instances "myws" and "myws-2" for 2 seconds
    Then both racing commands exited 0
    And exactly one racing command printed the mirror warning "mirror app: fault missing, result restored" and the other printed none
    And the mirror log gained one restore of "app" and a healthy check from the other command
    And the mirror of "app" is a link into its store
    And the state file of "app" records the linked store and its packs
    And only the linked store of "app" remains
    And no restore leftovers remain under the mirrors
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And the borrowers of "app" match their snapshot
    And the unpushed commit and uncommitted edit are intact

  # The source repo is moved away so the clone the restore needs fails. The
  # hook that provisions nothing runs the restore; nothing it touched may
  # change. git can't read a borrower while the pack it borrows is truncated,
  # so the borrowers are compared byte for byte on disk until the next
  # ordinary command, with the source back, restores.
  Scenario: A failed restore changes nothing and the next command retries
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
    And I record the borrowers of "app" on disk
    When I damage the store of "app" by truncating its pack to half
    And I remember the mirror link of "app"
    And I remember the state file of "app"
    And I remember the files of the store of "app"
    And I count the mirror log lines
    And I move the source repo "app" away
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook's context carries the notice for "app" with fault "pack-changed" and result "restore-failed"
    And the mirror log gained a check line and a restore line for "app" with fault "pack-changed" and result "restore-failed" from command "from-hook"
    And the mirror link of "app" is unchanged
    And the state file of "app" is unchanged
    And the store of "app" holds the same files
    And only the linked store of "app" remains
    And no restore leftovers remain under the mirrors
    And the borrowers of "app" are unchanged on disk
    When I put the source repo "app" back
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries exactly the mirror warning "mirror app: fault pack-changed, result restored"
    And the mirror link of "app" moved to a new store
    And the mirror of "app" is a link into its store
    And only the linked store of "app" remains
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And the borrowers of "app" match their snapshot
    And the unpushed commit and uncommitted edit are intact

  # Only the link is deleted and the source repo is moved away: the restore
  # can't clone, and needs not to, since the recorded store is intact.
  Scenario: A mirror whose link alone is gone is relinked with no network
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
    When I remember the mirror link of "app"
    And I count the mirror log lines
    And I delete only the stable link of "app"
    And I move the source repo "app" away
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook's context carries the notice for "app" with fault "missing" and result "restored"
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "from-hook"
    And the mirror link of "app" is unchanged
    And only the linked store of "app" remains
    And no restore leftovers remain under the mirrors
    And every ref of each borrower of "app" reads to the root and fsck is clean

  # The source repo gains a branch and a commit, then deletes the branch,
  # resets main past the commit and prunes both, as a branch deletion and a
  # force push on a real remote would. niwa worktree create and the hook that
  # provisions nothing only check the mirror; apply refreshes it. The
  # borrowers aren't compared with a snapshot, since apply's own sync of each
  # clone moves its remote-tracking refs when the remote changes.
  Scenario: The refresh keeps commits the remote deleted
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I add a branch "topic" with one commit and one commit on main to source repo "app"
    And I count the mirror log lines
    And I run "niwa worktree create app" in instance "myws"
    Then the exit code is 0
    And the error output carries no mirror warning
    When I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook printed nothing
    And the error output carries no mirror warning
    And the mirror log gained only healthy check lines for "app" from "worktree" and "from-hook"
    And the mirror of "app" has no branch "topic"
    When I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the mirror log gained a healthy check line and a refresh line for "app" from "apply"
    And the mirror of "app" holds the commits added to source repo "app"
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the borrower of "app" in instance "myws" has a local branch "keep" at the added topic commit
    And the borrower of "app" in instance "myws" has an unpushed commit and an uncommitted edit
    When source repo "app" deletes the added branch, resets main back one commit and prunes both commits
    Then a fresh clone of source repo "app" holds neither added commit
    When I remember the mirror link of "app"
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the mirror log gained a healthy check line and a refresh line for "app" from "apply"
    And the mirror link of "app" is unchanged
    And the mirror of "app" keeps the commits source repo "app" dropped
    And the store of "app" holds the fixed retention config
    And the state file of "app" records the linked store and its packs
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And the branch "keep" in the borrower of "app" in instance "myws" still names the added topic commit
    And the unpushed commit and uncommitted edit are intact

  # A branch "keep" in myws holds one unpushed commit on a pushed commit that
  # the source repo then deletes, keeping it only under a pull-request ref it
  # hides, the way a forge does. The mirror that held it is lost, and the
  # SessionStart hook of a session that isn't a background worker restores it.
  # That hook syncs no clone, so the borrowers are held to their bytes on disk.
  Scenario: A branch on deleted remote commits survives while the remote serves them
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I add a branch "topic" with one commit to source repo "app"
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And each borrower of "app" has the remote-tracking branch "origin/topic" at the added topic commit
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the borrower of "app" in instance "myws" has a local branch "keep" with one unpushed commit on the added topic commit
    And the borrower of "app" in instance "myws" has an unpushed commit and an uncommitted edit
    When source repo "app" keeps the added topic commit only under a hidden ref and deletes the added branch
    Then a fresh clone of source repo "app" lacks the added topic commit
    Given I snapshot the borrowers of "app"
    And I record the borrowers of "app" on disk
    And I count the mirror log lines
    When I delete the mirror of "app" and its store
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook's context carries the notice for "app" with fault "missing" and result "restored"
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "from-hook"
    And the mirror of "app" pins the added topic commit and has no branch for it
    And the borrowers of "app" are unchanged on disk
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And the unpushed commit on the branch and its own file still read in the borrower of "app" in instance "myws"
    And the unpushed commit and uncommitted edit are intact
    When I count the mirror log lines
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook printed nothing
    And the mirror log gained only a healthy check line for "app" from "from-hook"

  # The same, except that the source repo destroys the commit outright: no
  # ref keeps it, and the prune drops it. The restore can't get it back, so
  # it names the lost branch and the stale remote-tracking refs in the mirror
  # log and counts them in the hook's notice, and changes nothing in either
  # borrower. The next hook finds the mirror healthy and reports nothing.
  Scenario: A branch on commits the remote destroyed is reported unrecoverable
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I add a branch "topic" with one commit to source repo "app"
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And each borrower of "app" has the remote-tracking branch "origin/topic" at the added topic commit
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the borrower of "app" in instance "myws" has a local branch "keep" with one unpushed commit on the added topic commit
    And the borrower of "app" in instance "myws" has an unpushed commit and an uncommitted edit
    When source repo "app" deletes the added branch and prunes its commit
    Then a fresh clone of source repo "app" lacks the added topic commit
    Given I snapshot the borrowers of "app"
    And I record the borrowers of "app" on disk
    And I count the mirror log lines
    When I delete the mirror of "app" and its store
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook's context carries the notice for "app" with fault "missing", result "restored" and 3 unrecoverable refs, naming none
    And the mirror log then gained the unrecoverable lines for "app" from command "from-hook"
    And the mirror of "app" pins nothing
    And the borrowers of "app" are unchanged on disk
    And every other ref of each borrower of "app" reads to the root
    And the unpushed commit on the branch and its own file still read in the borrower of "app" in instance "myws"
    And the unpushed commit and uncommitted edit are intact
    When I count the mirror log lines
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook printed nothing
    And the mirror log gained only a healthy check line for "app" from "from-hook"

  # The two commands that check a healthy mirror without refreshing it run
  # with the source repo moved away, so a command that reached for the remote
  # would fail or warn. The design names the scenario for its 100 ms budget;
  # the unit tests show the check cheap by counting the one local git process
  # it starts, and this scenario asserts no time, only that each check line
  # records one.
  Scenario: A healthy check needs no network and stays under 100 ms
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
    When I remember the mirror link of "app"
    And I remember the state file of "app"
    And I count the mirror log lines
    And I move the source repo "app" away
    And I run "niwa worktree create app" in instance "myws"
    Then the exit code is 0
    And the error output carries no mirror warning
    When I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook printed nothing
    And the mirror log gained only healthy check lines for "app" from "worktree" and "from-hook"
    And every check line the mirror log gained carries check_ms as a whole number of 0 or more
    And the mirror link of "app" is unchanged
    And the state file of "app" is unchanged
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And nothing mirror-related exists outside the sandbox home

  # A reference-transaction hook planted in the store runs on every ref
  # update git makes there unless core.hooksPath points elsewhere, and a
  # store config naming a hook directory and an fsmonitor script would run
  # both. Each writes a marker if it ever runs. Every git call niwa makes
  # names the empty ~/.niwa/empty-hooks as core.hooksPath and turns fsmonitor
  # off, and the refresh rewrites the config whole before it fetches.
  Scenario: A hook planted in a mirror never runs and a hostile mirror config is rewritten
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I plant a reference-transaction hook that writes a marker in the store of "app"
    And I add a branch "topic" with one commit and one commit on main to source repo "app"
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the mirror log gained a healthy check line and a refresh line for "app" from "apply"
    And the mirror of "app" holds the commits added to source repo "app"
    And no hook marker exists
    When I remember the mirror link of "app"
    And I write core.hooksPath naming the marker hook's directory and core.fsmonitor naming a marker script into the store config of "app"
    And I add one commit on main to source repo "app"
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the mirror log gained a healthy check line and a refresh line for "app" from "apply"
    And the mirror of "app" holds the commit added on main
    And the mirror link of "app" is unchanged
    And the store of "app" holds the fixed retention config
    And no hook marker exists
    And the sandbox home's empty-hooks directory is empty with mode 0700
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And nothing mirror-related exists outside the sandbox home
