Feature: niwa worktree-delegation integration
  Routes Claude Code's native agent worktree creation through niwa so the
  agent gets a niwa worktree (with secrets + CLAUDE context), one per task,
  with niwa as the system of record. An apply-time `claude --version` probe
  chooses between the per-repo WorktreeCreate/WorktreeRemove hook (supported)
  and a permissions.deny fallback (unsupported); `niwa init
  --no-worktree-delegation` opts the whole integration out.

  These scenarios drive the compiled niwa binary offline. The runtime
  scenarios pipe synthetic Claude hook JSON to `niwa worktree from-hook`,
  exercising the end-to-end integration WITHOUT a real Claude. The
  install-branch scenarios make the harness probe deterministic with a FAKE
  `claude` on PATH (a tiny script printing a chosen version).

  Design: docs/designs/current/DESIGN-niwa-default-worktree.md

  # ---------------------------------------------------------------------
  # Runtime: a WorktreeCreate hook routed through from-hook yields a niwa
  # worktree, listed by `niwa worktree list`. This is the integration's
  # whole point: the native agent path produces a niwa worktree.
  # ---------------------------------------------------------------------

  @critical
  Scenario: WorktreeCreate hook via from-hook creates a niwa worktree
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wd-create" exists
    When I run "niwa create wd-create"
    Then the exit code is 0
    And the repo "apps/app" exists in instance "wd-create"
    # Simulate Claude firing the WorktreeCreate hook: pipe the hook JSON
    # (cwd = the repo path inside the instance) to from-hook on stdin.
    When I pipe a WorktreeCreate hook for repo "apps/app" with name "demo" in instance "wd-create"
    Then the exit code is 0
    # from-hook prints ONLY the absolute worktree path; it must exist on disk.
    And the printed worktree path exists
    # niwa is the system of record: the worktree is a listed niwa session.
    When I run "niwa worktree list" from channeled instance "wd-create"
    Then the exit code is 0
    And the output contains "app"
    And the output contains "active"

  # ---------------------------------------------------------------------
  # Runtime: a WorktreeRemove hook routed through from-hook reconciles the
  # niwa session (clean worktree => ended), so `niwa worktree list --status
  # active` no longer shows it.
  # ---------------------------------------------------------------------

  @critical
  Scenario: WorktreeRemove hook via from-hook ends a clean delegated worktree
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wd-remove" exists
    When I run "niwa create wd-remove"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook for repo "apps/app" with name "demo" in instance "wd-remove"
    Then the exit code is 0
    And the printed worktree path exists
    When I run "niwa worktree list --status active" from channeled instance "wd-remove"
    Then the exit code is 0
    And the output contains "active"
    # A freshly delegated worktree must read clean to git with NO commit: niwa
    # records git-exclude coverage for the .claude/ scaffolding it writes
    # (notably .claude/rules/worktree-imports.md), so the guarded (non-force)
    # teardown sees a clean worktree and ends it (design Decision 3, clean ->
    # ended). No workaround commit is needed.
    # Simulate Claude firing WorktreeRemove with the worktree_path: from-hook
    # is non-blocking (always exit 0) and ends the now-clean session.
    When I pipe a WorktreeRemove hook for the printed worktree path in instance "wd-remove"
    Then the exit code is 0
    # The worktree directory is gone and the session no longer lists active.
    And the printed worktree path does not exist
    When I run "niwa worktree list --status active" from channeled instance "wd-remove"
    Then the exit code is 0
    And the output does not contain "active"

  # ---------------------------------------------------------------------
  # Install (supported branch): with the probe reporting SUPPORTED, the repo
  # settings.local.json carries the WorktreeCreate/WorktreeRemove hooks (each
  # an absolute-path `worktree from-hook` command) and NO permissions.deny.
  # ---------------------------------------------------------------------

  @critical
  Scenario: supported harness installs the worktree hooks, no deny
    Given a clean niwa environment
    And a fake claude reporting version "2.1.183" is on PATH
    And a local git server is set up
    And a single-repo channeled workspace "wd-supported" exists
    When I run "niwa create wd-supported"
    Then the exit code is 0
    And the repo "apps/app" exists in instance "wd-supported"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-supported" contains "WorktreeCreate"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-supported" contains "WorktreeRemove"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-supported" contains "worktree from-hook"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-supported" does not contain "EnterWorktree"

  # ---------------------------------------------------------------------
  # Install (deny fallback branch): with the probe reporting UNSUPPORTED, the
  # repo settings.local.json carries permissions.deny [EnterWorktree,
  # ExitWorktree] and NO worktree hooks.
  # ---------------------------------------------------------------------

  @critical
  Scenario: unsupported harness installs the deny fallback, no hooks
    Given a clean niwa environment
    And a fake claude reporting version "2.0.0" is on PATH
    And a local git server is set up
    And a single-repo channeled workspace "wd-deny" exists
    When I run "niwa create wd-deny"
    Then the exit code is 0
    And the repo "apps/app" exists in instance "wd-deny"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-deny" contains "EnterWorktree"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-deny" contains "ExitWorktree"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-deny" does not contain "WorktreeCreate"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-deny" does not contain "worktree from-hook"

  # ---------------------------------------------------------------------
  # Opt-out: `niwa init --no-worktree-delegation` skips the whole block, so
  # apply writes neither the hooks nor the deny fallback regardless of the
  # harness version.
  # ---------------------------------------------------------------------

  @critical
  Scenario: opt-out installs neither hooks nor deny
    Given a clean niwa environment
    And a fake claude reporting version "2.1.183" is on PATH
    And a local git server is set up
    And a worktree-delegation opt-out workspace "wd-optout" exists
    When I run "niwa create wd-optout"
    Then the exit code is 0
    And the repo "apps/app" exists in instance "wd-optout"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-optout" does not contain "WorktreeCreate"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-optout" does not contain "worktree from-hook"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-optout" does not contain "EnterWorktree"
