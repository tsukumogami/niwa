Feature: niwa worktree env parity
  Regression coverage for issue #162: the worktree apply path must run the
  same resolve+merge pipeline the instance apply path runs, so the worktree
  .local.env carries the same env keys the instance .local.env does. Before
  the fix, applyContentToWorktree never called MergeGlobalOverride, so a
  personal-overlay-declared env key reached the instance .local.env but went
  missing from the worktree .local.env.

  This scenario covers the personal-overlay merge half of the fix. The vault
  resolve half is covered by unit tests in internal/workspace; the functional
  harness does not wire a fake vault backend, so it is not exercised here.
  See the follow-up note in the issue #162 PR for the gap.

  @critical
  Scenario: niwa worktree create installs personal-overlay env key into worktree .local.env
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "wt-env" exists with body:
      """
      [workspace]
      name = "wt-env"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    And a personal overlay exists with body:
      """
      [workspaces.wt-env.env.vars]
      PERSONAL_KEY = "personal-value"
      """
    When I run niwa init from config repo "wt-env"
    Then the exit code is 0
    When I run "niwa create wt-env"
    Then the exit code is 0
    When I call niwa worktree create for repo "myapp" with purpose "wt-env-parity" in instance "wt-env"
    Then the last session is active in instance "wt-env"
    And the session worktree exists in instance "wt-env"
    # The personal-overlay env key must reach the worktree .local.env. Before
    # the fix this assertion failed because applyContentToWorktree handed the
    # un-merged cfg straight to ApplyToWorktree.
    And the file ".local.env" in the last worktree contains "PERSONAL_KEY=personal-value"
    # Cleanup
    When I call niwa_destroy_session in instance "wt-env"
    Then the session is ended in instance "wt-env"
