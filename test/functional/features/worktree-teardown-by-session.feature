Feature: niwa worktree destroy resolves a session id or handle from the workspace root
  End-to-end scenarios for tearing down a session's worktrees by the id a
  developer actually holds. The lifecycle store keys worktrees by eight hex
  characters that appear nowhere a developer can see them; what they have is the
  session id from `niwa list` or the short handle from the window title. Destroy
  now resolves all three, from anywhere in the workspace.

  Every scenario runs offline. The workspace comes from the local bare-repo
  server through a file:// config source and `niwa init`, and the session comes
  from `niwa dispatch` with the fake claude on PATH -- no GitHub fake, no real
  claude, no network. The fake claude pins the session UUID and writes its job
  state under the sandboxed $HOME, so the dispatch capture correlates it and the
  mapping is keyed on exactly the id the scenario names.

  The handle is not a second spelling of the id. For a Claude session the handle
  niwa records is the name of the job directory the worker wrote, which is the
  short form the agent's own verbs accept; the scenario that uses it asserts the
  mapping records that value before passing it to destroy, so the handle arm of
  the resolver is the one under test.

  Teardown is worktrees and nothing else. Every destroy scenario asserts the
  three things that must survive it: the session mapping, the instance
  directory, and the clone inside it. A teardown that reclaimed the instance
  would leave the developer with no session to go back to.

  Design: docs/designs/current/DESIGN-session-store-teardown.md

  # ---------------------------------------------------------------------
  # Destroy by session id, merged branch: the whole worktree goes.
  #
  # The worktree's branch carries no commits of its own, so it is already
  # merged into what the clone has checked out and `git branch -d` removes it.
  # The destroyed line is the enriched, session-resolved one -- it names the
  # worktree id, its repo and its path, because a caller who asked by session
  # needs to be told which worktrees of which instance went.
  # ---------------------------------------------------------------------

  @critical
  Scenario: destroy by session id from the workspace root removes the worktree and its merged branch
    Given a clean niwa environment
    And a local git server is set up
    # A repo with a commit: a worktree branch is only a real ref once its base
    # has one, and the merged/unmerged distinction is about commits.
    And a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.apps]

      [repos.app]
      url = "{repo:app}"
      group = "apps"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch teardown-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    And a dispatch-origin mapping exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    # One active worktree in the instance the session backs.
    When I create a worktree for repo "app" with purpose "merged-branch" in the dispatch instance
    Then the last worktree branch exists in repo "app" of the dispatch instance
    # The developer pastes the session id, from the workspace root, where the
    # worktree lifecycle store of that instance is not even visible.
    When I run "niwa worktree destroy aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" from the workspace root
    Then the exit code is 0
    And the output has exactly one destroyed line for the last worktree in repo "app"
    And the last worktree record is ended in the dispatch instance
    And the session worktree directory does not exist
    And the last worktree branch does not exist in repo "app" of the dispatch instance
    # What teardown must not touch.
    And the session mapping exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And the dispatch instance still exists
    And the repo "app" still exists in the dispatch instance

  # ---------------------------------------------------------------------
  # Destroy by session id, unmerged branch: the worktree goes, the branch
  # stays, and the command still succeeds.
  #
  # A commit inside the worktree puts the branch ahead of the clone, so
  # `git branch -d` refuses. That refusal is not a failure of the teardown:
  # the directory is removed and the record ends, the branch is kept so the
  # work is not discarded, one warning says so on stderr, and the exit code
  # is 0. Exactly one warning, because exactly one worktree was kept.
  # ---------------------------------------------------------------------

  @critical
  Scenario: destroy by session id keeps an unmerged branch and warns, exiting 0
    Given a clean niwa environment
    And a local git server is set up
    # A repo with a commit: a worktree branch is only a real ref once its base
    # has one, and the merged/unmerged distinction is about commits.
    And a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.apps]

      [repos.app]
      url = "{repo:app}"
      group = "apps"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I run "niwa dispatch unmerged-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch-origin mapping exists for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I create a worktree for repo "app" with purpose "unmerged-branch" in the dispatch instance
    And I commit a change "work.txt" in the last worktree
    When I run "niwa worktree destroy bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" from the workspace root
    Then the exit code is 0
    And the output has exactly one destroyed line for the last worktree in repo "app"
    And the last worktree record is ended in the dispatch instance
    And the session worktree directory does not exist
    # The commits are still reachable, and the developer was told once.
    And the last worktree branch exists in repo "app" of the dispatch instance
    And the error output has exactly one line containing "unmerged commits remain"
    # What teardown must not touch.
    And the session mapping exists for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    And the dispatch instance still exists
    And the repo "app" still exists in the dispatch instance

  # ---------------------------------------------------------------------
  # Destroy by the recorded handle.
  #
  # The handle route, exercised distinctly: the scenario first pins that the
  # mapping records this handle -- the job directory name the worker wrote,
  # not the session id -- and then hands that value, and only that value, to
  # destroy. Because the mapping carries a handle, the resolver's id-prefix
  # fallback is switched off for it, so this can only be the handle match.
  # ---------------------------------------------------------------------

  @critical
  Scenario: destroy by the recorded handle tears down the same session's worktree
    Given a clean niwa environment
    And a local git server is set up
    # A repo with a commit: a worktree branch is only a real ref once its base
    # has one, and the merged/unmerged distinction is about commits.
    And a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.apps]

      [repos.app]
      url = "{repo:app}"
      group = "apps"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    When I run "niwa dispatch handle-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch-origin mapping exists for session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    # The value the next command uses is the recorded handle, not the id.
    And the session mapping for session "cccccccc-cccc-4ccc-8ccc-cccccccccccc" records handle "cccccccc"
    When I create a worktree for repo "app" with purpose "handle-route" in the dispatch instance
    Then the last worktree branch exists in repo "app" of the dispatch instance
    When I run "niwa worktree destroy cccccccc" from the workspace root
    Then the exit code is 0
    And the output has exactly one destroyed line for the last worktree in repo "app"
    And the last worktree record is ended in the dispatch instance
    And the session worktree directory does not exist
    And the last worktree branch does not exist in repo "app" of the dispatch instance
    # What teardown must not touch.
    And the session mapping exists for session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    And the dispatch instance still exists
    And the repo "app" still exists in the dispatch instance

  # ---------------------------------------------------------------------
  # The workspace root contract.
  #
  # A root holding instances is not an instance. A value that matches no
  # session and no worktree exits 3 -- its own code, so a cleanup script can
  # tell "nothing there" from a guard refusal -- and `niwa worktree list`
  # there says where to go instead, on stderr, with no table and exit 0, so a
  # script looping over directories is not derailed by hitting the root.
  #
  # The eight-hex value is deliberate: it is exactly the shape of a worktree
  # id, and at a root that reading does not exist. There is a real mapping in
  # the store when it runs, so this also pins that a non-matching value is not
  # quietly resolved to the one session that is there.
  # ---------------------------------------------------------------------

  @critical
  Scenario: at a workspace root an unmatched value exits 3 and worktree list redirects
    Given a clean niwa environment
    And a local git server is set up
    # A repo with a commit: a worktree branch is only a real ref once its base
    # has one, and the merged/unmerged distinction is about commits.
    And a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.apps]

      [repos.app]
      url = "{repo:app}"
      group = "apps"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa dispatch root-contract-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch-origin mapping exists for session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    # Nothing answers to this value, in either reading.
    When I run "niwa worktree destroy deadbeef" from the workspace root
    Then the exit code is 3
    # The line names the value back, quoted; the step vocabulary cannot carry a
    # quote inside a quoted argument, so the two halves are asserted apart.
    And the error output contains "no worktree or session matches"
    And the error output contains "deadbeef"
    # And nothing was torn down on the way to saying so.
    And the session mapping exists for session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    And the dispatch instance still exists
    And the repo "app" still exists in the dispatch instance
    # The other half of the root contract: list redirects instead of failing.
    When I run "niwa worktree list" from the workspace root
    Then the exit code is 0
    And the output is empty
    And the error output contains "this is the workspace root, not an instance"
