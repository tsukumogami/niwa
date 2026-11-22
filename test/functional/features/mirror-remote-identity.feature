@mirror-repair
Feature: the host mirror treats two spellings of one remote as one remote
  A mirror records the remote it was cloned from. Moving a repo's URL from
  its SSH spelling to HTTPS, or changing only its user, still names that
  remote, so a restore for a later fault salvages the old store as it would
  for any other fault. Pointing the repo at another remote rebuilds the
  mirror with no salvage. The sandbox git config sends each spelling of
  mirror.test/o/app to the local source repo, so every spelling reaches it
  with no network.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with 3 commits
    And the sandbox git config sends "git@mirror.test:o/app.git" to source repo "app"
    And the sandbox git config sends "deploy@mirror.test:o/app.git" to source repo "app"
    And the sandbox git config sends "https://mirror.test/o/app.git" to source repo "app"
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "git@mirror.test:o/app.git"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0

  # The borrower in myws holds a branch on a commit the remote then destroys,
  # so after the restore it reads only if the old store was salvaged. The
  # config moves and the store is damaged before any command sees the new
  # spelling, so the restore is the first to compare it with the record.
  Scenario Outline: A restore after the URL moves to <spelling> salvages the old store
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    And the state file of "app" records the identity "mirror.test/o/app"
    When I add a branch "topic" with one commit to source repo "app"
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And each borrower of "app" has the remote-tracking branch "origin/topic" at the added topic commit
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the borrower of "app" in instance "myws" has a local branch "keep" with one unpushed commit on the added topic commit
    When source repo "app" deletes the added branch and prunes its commit
    Then a fresh clone of source repo "app" lacks the added topic commit
    When the config repo "myws" is force-pushed to:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "<url>"
      group = "tools"
      """
    And I remember the mirror link of "app"
    And I damage the store of "app" by deleting its HEAD
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries exactly the mirror warning "mirror app: fault repo-unreadable, result restored"
    And the mirror log gained a check line and a restore line for "app" with fault "repo-unreadable" and result "restored"
    And the restore line the mirror log gained for "app" salvaged from the remembered store
    And the mirror link of "app" moved to a new store
    And the mirror of "app" has the remote URL "<recorded>"
    And the state file of "app" records the identity "mirror.test/o/app"
    And every ref of each borrower of "app" reads to the root and fsck is clean
    And the unpushed commit on the branch and its own file still read in the borrower of "app" in instance "myws"

    Examples:
      | spelling             | url                           | recorded                      |
      | its HTTPS spelling   | https://mirror.test/o/app.git | https://mirror.test/o/app.git |
      | another user         | deploy@mirror.test:o/app.git  | mirror.test:o/app.git         |

  # elsewhere holds the same history at another path, so the borrowers keep
  # reading from the rebuilt mirror.
  Scenario: Pointing the repo at another remote rebuilds the mirror with no salvage
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" borrow from the mirror of "app"
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
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
    And I remember the mirror link of "app"
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries exactly the mirror warning "mirror app: fault url-changed, result restored"
    And the mirror log gained a check line and a restore line for "app" with fault "url-changed" and result "restored"
    And the restore line the mirror log gained for "app" salvaged from no store
    And the mirror link of "app" moved to a new store
    And the mirror of "app" has the remote URL of source repo "elsewhere"
    And every ref of each borrower of "app" reads to the root and fsck is clean
