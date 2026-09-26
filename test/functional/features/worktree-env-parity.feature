Feature: niwa worktree env parity
  A worktree's environment is INHERITED from the instance clone's already-
  materialized .local.env by byte-copy, with no secret resolution and no
  network access on the worktree path (DESIGN-worktree-env-provisioning,
  decision A1). The instance clone resolves the environment once at
  `niwa create`/`niwa apply` time; `niwa worktree create` then copies the
  clone's output bytes into the worktree's config-resolved target(s).

  This scenario is the offline @critical coverage: the local git server provides
  the clone, `niwa create` materializes a personal-overlay-declared env key into
  the clone .local.env, and `niwa worktree create` inherits that key into the
  worktree .local.env without re-resolving anything. The vault-resolution and
  byte-equivalence-across-formats cases are covered by unit tests in
  internal/workspace (inherit_env_test.go); the functional harness does not wire
  a fake vault backend.

  @critical
  Scenario: niwa worktree create inherits the clone's env into the worktree .local.env
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
    # The personal-overlay env key was resolved into the clone .local.env at
    # `niwa create` time; the worktree inherits it by byte-copy. No secret
    # resolution runs on the worktree path.
    And the file ".local.env" in the last worktree contains "PERSONAL_KEY=personal-value"
    # Cleanup
    When I call niwa_destroy_session in instance "wt-env"
    Then the session is ended in instance "wt-env"

  # Regression coverage for the worktree promote path. When [claude.env] promotes
  # a key whose value is NOT in the config repo -- it comes from the personal
  # overlay (or vault / machine-identity sync) and is resolved only at
  # `niwa create` time -- the worktree path's SettingsMaterializer must INHERIT
  # the promoted value from the clone's already-materialized env rather than
  # re-resolving it. Before the fix, `niwa worktree create` failed with
  # `claude.env: promoted key "PERSONAL_KEY" not found in resolved env vars`
  # because the worktree path sees the config repo config only (no personal
  # overlay merge, no secret resolution).
  @critical
  Scenario: niwa worktree create inherits a promoted env key resolved only at instance apply time
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "wt-promote" exists with body:
      """
      [workspace]
      name = "wt-promote"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"

      [claude.env]
      promote = ["PERSONAL_KEY"]
      """
    And a personal overlay exists with body:
      """
      [workspaces.wt-promote.env.vars]
      PERSONAL_KEY = "personal-value"
      """
    When I run niwa init from config repo "wt-promote"
    Then the exit code is 0
    When I run "niwa create wt-promote"
    Then the exit code is 0
    When I call niwa worktree create for repo "myapp" with purpose "wt-promote" in instance "wt-promote"
    Then the last session is active in instance "wt-promote"
    And the session worktree exists in instance "wt-promote"
    # The promoted key was resolved into the clone at `niwa create` time; the
    # worktree's settings.local.json inherits it from the clone env, not by
    # re-resolving the personal overlay (which the worktree path never sees).
    And the file ".claude/settings.local.json" in the last worktree contains "PERSONAL_KEY"
    And the file ".claude/settings.local.json" in the last worktree contains "personal-value"
    # Cleanup
    When I call niwa_destroy_session in instance "wt-promote"
    Then the session is ended in instance "wt-promote"
