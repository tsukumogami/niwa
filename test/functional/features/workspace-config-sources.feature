Feature: workspace config sources (snapshot model)

  The snapshot model in the workspace-config-sources design replaces the
  legacy `git pull --ff-only` config sync with a fetch + atomic-swap
  primitive. These scenarios verify the user-visible guarantees of that
  design — most importantly that issue #72 (force-push wedges the
  workspace) is structurally impossible under the new model.

  # --- PRD #72 regression: force-push survival ---
  # The headline acceptance gate. Today's `git pull --ff-only` can't
  # recover when the upstream config repo rewrites history (force push).
  # The snapshot model replaces .niwa/ wholesale, so a force-pushed
  # upstream resolves on the next apply.

  @critical
  Scenario: niwa apply survives an upstream force-push of the config repo
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the provenance marker exists
    When I run "niwa apply myws"
    Then the exit code is 0
    # Upstream maintainer rewrites history and force-pushes. Under the
    # legacy model, the next apply would fail with "fatal: Not possible
    # to fast-forward, aborting" (the failure mode in issue #72).
    When the config repo "myws" is force-pushed to:
      """
      [workspace]
      name = "myws"
      """
    And I run "niwa apply myws"
    Then the exit code is 0

  # --- PRD R28: same-URL legacy working tree lazy-converts to snapshot ---
  # Existing users on the pre-snapshot model get a transparent in-place
  # upgrade on the next apply, without --force. The conversion notice
  # is one-time per workspace (PRD R28).

  @critical
  Scenario: niwa apply lazy-converts a legacy working tree to a snapshot
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "lazy" exists with body:
      """
      [workspace]
      name = "lazy"
      """
    When I run niwa init from config repo "lazy"
    Then the exit code is 0
    # Simulate a workspace from before the snapshot model: replace the
    # marker-bearing snapshot with a real .git working tree.
    Given the config dir is a git working tree from config repo "lazy"
    When I run "niwa apply lazy"
    Then the exit code is 0
    And the provenance marker exists
    And the error output contains "converted from working tree to snapshot"
    # Second apply: notice should NOT fire again (PRD R28 one-time).
    When I run "niwa apply lazy"
    Then the exit code is 0
    And the error output does not contain "converted from working tree to snapshot"
