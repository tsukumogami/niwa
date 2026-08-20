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

  # A dispatched session's mapping lives in that same config dir, at
  # <workspaceRoot>/.niwa/sessions/<id>.json, and the swap took it too. It is
  # the only record of which instance a session became: losing it strands a
  # running worker behind a handle nothing can name any more and leaves the
  # reaper with no join between an instance and the session that owns it.

  @critical
  Scenario: niwa apply preserves a dispatched session's mapping across a config-snapshot refresh
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "mapws" exists with body:
      """
      [workspace]
      name = "mapws"
      """
    When I run niwa init from config repo "mapws"
    Then the exit code is 0
    And the provenance marker exists
    Given a fake codex for dispatch with session "01a40000-0000-7000-8000-00000000d1ce"
    When I run "niwa dispatch some-task --detach --launch-agent codex" from the workspace root
    Then the exit code is 0
    And a dispatch-origin mapping exists for session "01a40000-0000-7000-8000-00000000d1ce"
    When I run "niwa apply mapws"
    Then the exit code is 0
    And a dispatch-origin mapping exists for session "01a40000-0000-7000-8000-00000000d1ce"
    # And the session is still reachable, which is what the mapping is for.
    When I run "niwa list" from the workspace root
    Then the exit code is 0
    And the output contains "codex resume 01a40000-0000-7000-8000-00000000d1ce"

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

  # --- issue #227: a newly declared env key reaches .local.env on the SAME apply ---
  # The #214 scenario above asserts the workspace-ROOT settings posture, which is
  # materialized from the config `niwa apply` loads at the root, in the same
  # process that reconciles the snapshot. Per-repo secret output (.local.env) is
  # materialized further down, inside the per-instance apply pipeline, from the
  # config that pipeline is handed. A newly declared [env.vars] / [env.secrets]
  # key must land on the apply that pulls it, not the one after -- otherwise a
  # correct declaration reads as a broken value lookup.
  #
  # Both key kinds are asserted: they travel the same resolution path, and both
  # were reported stale, which rules out value resolution as the cause.
  #
  # This one is a regression guard rather than a fix. #227 reports the symptom
  # on 0.20.1, which was tagged before #216 shipped, so the apply path was
  # already correct by the time the report landed; this pins that it stays so.
  # The create/reset/from-hook scenarios below are the ones that were failing.

  @critical
  Scenario: niwa apply materializes a newly declared env key into .local.env on the same run
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "envrecon" exists with body:
      """
      [workspace]
      name = "envrecon"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"

      [env.secrets]
      EXISTING_SECRET = "existing-secret-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "envrecon"
    Then the exit code is 0
    And the provenance marker exists
    When I run "niwa create envrecon"
    Then the exit code is 0
    When I run "niwa apply envrecon"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "envrecon" contains "EXISTING_VAR=existing-var-value"
    # Upstream declares one new key of each kind.
    When the config repo "envrecon" is force-pushed to:
      """
      [workspace]
      name = "envrecon"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"
      ADDED_VAR = "added-var-value"

      [env.secrets]
      EXISTING_SECRET = "existing-secret-value"
      ADDED_SECRET = "added-secret-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And I run "niwa apply envrecon"
    Then the exit code is 0
    # One apply must be enough for both keys.
    And the file "tools/app/.local.env" in instance "envrecon" contains "ADDED_VAR=added-var-value"
    And the file "tools/app/.local.env" in instance "envrecon" contains "ADDED_SECRET=added-secret-value"

  # --- issue #227: the same guarantee on the instance-creation path ---
  # `niwa create` refreshes the config snapshot from the source too, for the
  # stated reason that create should pick up upstream maintainer changes. But
  # the refresh runs after the config was loaded, and the loaded config is what
  # drives materialization -- so a key added upstream since the last read is
  # missed. Unlike apply there is no "just run it again" here: the instance is
  # created once, and it comes up with an incomplete environment.

  @critical
  Scenario: niwa create materializes a newly declared env key into a new instance
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "envcreate" exists with body:
      """
      [workspace]
      name = "envcreate"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"

      [env.secrets]
      EXISTING_SECRET = "existing-secret-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "envcreate"
    Then the exit code is 0
    When I run "niwa create envcreate"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "envcreate" contains "EXISTING_VAR=existing-var-value"
    # Upstream declares a new key, then a second instance is created.
    When the config repo "envcreate" is force-pushed to:
      """
      [workspace]
      name = "envcreate"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"
      ADDED_VAR = "added-var-value"

      [env.secrets]
      EXISTING_SECRET = "existing-secret-value"
      ADDED_SECRET = "added-secret-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And I run "niwa create envcreate"
    Then the exit code is 0
    # The freshly created instance must carry the keys its own run pulled down.
    And the file "tools/app/.local.env" in instance "envcreate-2" contains "ADDED_VAR=added-var-value"
    And the file "tools/app/.local.env" in instance "envcreate-2" contains "ADDED_SECRET=added-secret-value"

  # --- issue #227: the same guarantee on the reset path ---
  # Reset exists to rebuild an instance from the current config, so rebuilding
  # it from a pre-refresh read is exactly the wrong outcome -- the user asked
  # for the current config and got the previous one.
  #
  # One key kind is enough here. Both travel the same ResolveEnvVars path, and
  # the apply and create scenarios above already pin that both kinds do; what is
  # unproven on this path is the reconcile, not the resolution.

  @critical
  Scenario: niwa reset rebuilds an instance from a newly declared env key
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "envreset" exists with body:
      """
      [workspace]
      name = "envreset"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "envreset"
    Then the exit code is 0
    When I run "niwa create envreset"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "envreset" contains "EXISTING_VAR=existing-var-value"
    # Upstream declares a new key, then the instance is reset.
    When the config repo "envreset" is force-pushed to:
      """
      [workspace]
      name = "envreset"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"
      ADDED_VAR = "added-var-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And I run "niwa reset envreset" from workspace root
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "envreset" contains "ADDED_VAR=added-var-value"

  # --- issue #227: the same guarantee on the session-provisioning path ---
  # This is the path a dispatched session's instance comes up through, and it
  # runs once per instance with nothing behind it. A key declared upstream
  # since the last read would be missing from that session's environment for as
  # long as the session lives, with no second run to recover it.
  #
  # One key kind, for the same reason as the reset scenario above.

  @critical
  Scenario: an ephemeral session instance is provisioned with a newly declared env key
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "envhook" exists with body:
      """
      [workspace]
      name = "envhook"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "envhook"
    Then the exit code is 0
    # Upstream declares a new key BEFORE the session is provisioned, so the
    # on-disk snapshot the hook reads is a run behind.
    When the config repo "envhook" is force-pushed to:
      """
      [workspace]
      name = "envhook"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"
      ADDED_VAR = "added-var-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    Given a background job state exists for session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    When I pipe a SessionStart hook for session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    Then the exit code is 0
    And the instance "envhook-cccccccc-ccc" exists
    And the file "tools/app/.local.env" in instance "envhook-cccccccc-ccc" contains "ADDED_VAR=added-var-value"

  # --- reset reconciles before it destroys ---
  # Reset destroys an instance and rebuilds it. The reconcile has to run first:
  # if it runs after, a source that is briefly unreachable takes the rebuild
  # down with it and the user is left with nothing where their instance was.
  # The guarantee lives in the order of two statements in runReset, so it needs
  # a test of its own -- a reorder would otherwise reintroduce the data loss
  # with the whole suite still green.

  @critical
  Scenario: niwa reset leaves the instance intact when the source is unreachable
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "envsafe" exists with body:
      """
      [workspace]
      name = "envsafe"

      [groups.tools]

      [env.vars]
      EXISTING_VAR = "existing-var-value"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "envsafe"
    Then the exit code is 0
    When I run "niwa create envsafe"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "envsafe" contains "EXISTING_VAR=existing-var-value"
    # The config source goes away, then the user resets.
    When the config repo "envsafe" is unreachable
    And I run "niwa reset envsafe" from workspace root
    Then the exit code is not 0
    # Reset failed, but it must not have destroyed anything on the way.
    And the file "tools/app/.local.env" in instance "envsafe" contains "EXISTING_VAR=existing-var-value"
