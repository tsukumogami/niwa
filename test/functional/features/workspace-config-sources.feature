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

  # --- dispatch-brief survival across a config-snapshot refresh ---
  # The /dispatch skill writes a task brief to
  # <workspaceRoot>/.niwa/dispatch-briefs/<slug>.md and then runs `niwa
  # dispatch`, whose provision path refreshes the config snapshot on the SAME
  # .niwa dir. The atomic swap replaces the whole dir with freshly fetched
  # upstream content, so the brief — niwa-local runtime state, not source
  # content — must be carried across the swap or it vanishes before the
  # dispatched worker can read it. This bit config-in-repo single-repo
  # workspaces deterministically: the config source repo is the repo the
  # worker commits to, so its HEAD advances and the drift check fires on
  # every dispatch. A local (non-GitHub) config source always re-materializes
  # on apply, so this scenario exercises the swap without needing drift.

  @critical
  Scenario: niwa apply preserves dispatch briefs across a config-snapshot refresh
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "briefws" exists with body:
      """
      [workspace]
      name = "briefws"
      """
    When I run niwa init from config repo "briefws"
    Then the exit code is 0
    And the provenance marker exists
    # The coordinator drops a brief into the workspace-root config dir, then
    # a dispatch/apply runs a config refresh on that same dir.
    Given a dispatch brief "probe.md" exists in the workspace root
    When I run "niwa apply briefws"
    Then the exit code is 0
    # The brief must survive the refresh so the dispatched worker can read it.
    And the dispatch brief "probe.md" still exists in the workspace root

  # --- issue #214: upstream config changes take effect on the SAME apply ---
  # The reconcile that refreshes the workspace-root .niwa/ snapshot from the
  # source must run BEFORE the config drives materialization, and the swapped
  # workspace.toml must be reloaded. Otherwise a settings change pushed to the
  # source only lands one apply later: the reconcile swaps the snapshot on disk
  # but the stale config already materialized the managed files.

  @critical
  Scenario: niwa apply reconciles a settings change from the source on the same run
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "recon" exists with body:
      """
      [workspace]
      name = "recon"
      """
    When I run niwa init from config repo "recon"
    Then the exit code is 0
    And the provenance marker exists
    When I run "niwa apply recon"
    Then the exit code is 0
    # Upstream adds a permission posture to the config.
    When the config repo "recon" is force-pushed to:
      """
      [workspace]
      name = "recon"

      [claude.settings]
      permissions = "bypass"
      """
    And I run "niwa apply recon"
    Then the exit code is 0
    # A single apply must materialize the new posture -- not require a second run.
    And the file ".claude/settings.json" under the workspace root contains "bypassPermissions"
