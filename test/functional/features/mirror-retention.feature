@mirror-repair
Feature: niwa keeps an old mirror store until no clone on disk names it
  A restore replaces a mirror's store but removes no store a borrower may
  still read. Every store is listed in the mirror's store record, the one a
  restore replaced is marked retired, and a later sweep removes a retired
  store only once a strict scan of every clone on disk, kept and
  half-created instances included, finds no alternates line naming it. A
  workspace destroy keeps a mirror a kept instance still borrows from. As in
  mirror-repair.feature, the borrowers are the clones niwa create makes in
  the test build, each checked for the one stable-path alternates line and
  for holding no pack the mirror holds.

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

  # One instance is moved under kept/ and a half-created one, a directory
  # with a borrowing clone and no state file, sits beside the rest. Each
  # command that checks mirrors passes every clone on disk, so the restore's
  # probe reads both.
  Scenario Outline: A restore by <command> probes kept and half-created instances
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    Given instance "myws-2" is moved under the sandbox home's kept/
    And a half-created instance "myws-3" holds a copy of the clone of "tools/app" in instance "myws" and no state file
    When I count the mirror log lines
    And I delete the mirror of "app" and its store
    And I restore the mirror by running <command>
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    And the restore line for "app" probed the clones of "tools/app" in "kept/myws-2" and "myws-3"
    And nothing mirror-related exists outside the sandbox home

    Examples:
      | command                                    |
      | niwa apply at the root                     |
      | a SessionStart hook that doesn't provision |
      | niwa worktree create in myws               |

  # With kept/ unreadable the strict scan fails, so neither the sweep that
  # follows a restore nor the next command's removes anything, however old
  # the retired stores are.
  Scenario: An unreadable kept directory keeps every retired store
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I remember the mirror link of "app"
    And I damage the store of "app" by deleting its HEAD
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the store "app" replaced is kept and recorded as retired
    Given the kept directory cannot be read
    And I age the store record of "app" past an hour
    When I count the mirror log lines
    And I damage the store of "app" by deleting its HEAD
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries exactly the mirror warning "mirror app: fault repo-unreadable, result restored"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And no store of "app" was removed and the record still lists the retired store
    And the mirror log gained a keep line for the retired store of "app" because the scan failed
    And its mode is restored
    And nothing mirror-related exists outside the sandbox home

  # One borrower's alternates names the linked .store/ directory itself
  # rather than the stable path. That store outlives a restore, a rebuild
  # for another remote, and a sweep with the record aged past an hour.
  Scenario: A store a clone names directly survives a restore, a remote change and the sweep
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    Given the borrower of "app" in instance "myws-2" names the linked store directly
    When I damage the store of "app" by deleting its HEAD
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the store the borrower names directly is still on disk
    Given a source repo "elsewhere" is a copy of source repo "app"
    When the config repo "myws" is force-pushed to:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:elsewhere}"
      group = "tools"
      """
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries exactly the mirror warning "mirror app: fault url-changed, result restored"
    And the store the borrower names directly is still on disk
    When I age the store record of "app" past an hour
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the store the borrower names directly is still on disk
    And nothing mirror-related exists outside the sandbox home

  # A retired store younger than an hour stays; once the record is aged past
  # an hour, the next command's sweep removes it by name and logs it.
  Scenario: The sweep removes a retired store an hour old that no clone names
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I remember the mirror link of "app"
    And I damage the store of "app" by deleting its HEAD
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the store "app" replaced is kept and recorded as retired
    When I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And no store of "app" was removed and the record still lists the retired store
    When I age the store record of "app" past an hour
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the mirror log gained a remove line for the retired store of "app"
    And the mirror of "app" is a link into its store
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And nothing mirror-related exists outside the sandbox home

  # Branch "a" holds a small commit and branch "b" a 256 KiB random file.
  # The borrower in myws keeps a branch on each, the source repo then drops
  # both, and the old store's packs are replaced with one pack whose first
  # half holds everything "a" needs and whose cut-off second half holds what
  # "b" needs. The restore salvages the readable half; "keep-b" is reported
  # unrecoverable, and the old store is removed only by a later sweep.
  Scenario: A restore recovers the readable half of a truncated old pack
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When source repo "app" gains branch "a" with a small commit and branch "b" with a commit holding a 256 KiB random file
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    Given the borrower of "app" in instance "myws" has local branches "keep-a" on "a" and "keep-b" on "b"
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    When source repo "app" deletes branches "a" and "b" and prunes their commits
    And I replace the packs of the store of "app" with one pack holding branch "a"'s objects first and branch "b"'s last, truncated halfway
    And I count the mirror log lines
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the branch "keep-a" in the borrower of "app" in instance "myws" reads to the root
    And the mirror log names "refs/heads/keep-b" unrecoverable and not "refs/heads/keep-a"
    And no restore leftovers remain under the mirrors
    When I age the store record of "app" past an hour
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And in the mirror log every restore line carrying probed comes before every remove line
    And no restore leftovers remain under the mirrors
    And nothing mirror-related exists outside the sandbox home

  Scenario: Destroying the workspace removes mirrors no kept instance borrows from
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I run "niwa destroy --workspace --confirm myws" from workspace root
    Then the exit code is 0
    And the mirrors of the removed workspace root are gone, and its key directory with them unless something else is in it
    And nothing mirror-related exists outside the sandbox home once the workspace root is gone

  Scenario: Destroying the workspace keeps a mirror a kept instance borrows from
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    Given instance "myws-2" is moved under the sandbox home's kept/
    When I run "niwa destroy --workspace --confirm myws" from workspace root
    Then the exit code is 0
    And the error output says the mirror of "app" was kept because "a kept instance borrows from it"
    And the mirror of "app" outlived the destroy
    And nothing mirror-related exists outside the sandbox home once the workspace root is gone

  Scenario: Destroying the workspace keeps a mirror whose lock another command holds
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I run "niwa destroy --workspace --confirm myws" from workspace root while the mirror lock of "app" is held
    Then the exit code is 0
    And the error output says the mirror of "app" was kept because "busy"
    And the mirror of "app" outlived the destroy
    And nothing mirror-related exists outside the sandbox home once the workspace root is gone

  Scenario: Destroying the workspace keeps every mirror while kept/ can't be read
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    Given the kept directory cannot be read
    When I run "niwa destroy --workspace --confirm myws" from workspace root
    Then the exit code is 0
    And the error output says the mirror of "app" was kept because "kept instances couldn't be read"
    And the mirror of "app" outlived the destroy
    And its mode is restored
    And nothing mirror-related exists outside the sandbox home once the workspace root is gone
