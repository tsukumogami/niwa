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

  # ---------------------------------------------------------------------
  # Durability (DESIGN Decision 7): the emitted hook command resolves niwa
  # from PATH and falls back to the absolute path recorded at apply time, so
  # an installed hook keeps working after a niwa upgrade rather than pinning
  # the release that happened to run apply. Both hook consumers get the same
  # shape -- the per-repo worktree hook and the workspace-root SessionStart
  # hook, which propagates staleness because it provisions instances in
  # process.
  # ---------------------------------------------------------------------

  @critical
  Scenario: the worktree hook command resolves niwa from PATH before its fallback
    Given a clean niwa environment
    And a fake claude reporting version "2.1.183" is on PATH
    And a local git server is set up
    And a single-repo channeled workspace "wd-durable" exists
    When I run "niwa create wd-durable"
    Then the exit code is 0
    And the repo "apps/app" exists in instance "wd-durable"
    # The PATH arm is what survives an upgrade.
    And the file "apps/app/.claude/settings.local.json" in instance "wd-durable" contains "command -v niwa"
    And the file "apps/app/.claude/settings.local.json" in instance "wd-durable" contains "exec niwa worktree from-hook"
    # The fallback arm keeps the integration working where niwa is off PATH.
    And the file "apps/app/.claude/settings.local.json" in instance "wd-durable" contains "; exec "
    # `||` would be evaluated only if exec returned, which it does not.
    And the file "apps/app/.claude/settings.local.json" in instance "wd-durable" does not contain "||"

  @critical
  Scenario: the workspace-root session hook gets the same durable shape
    Given a clean niwa environment
    And a fake claude reporting version "2.1.183" is on PATH
    And a local git server is set up
    And a single-repo channeled workspace "wd-root-hook" exists
    When I run "niwa create wd-root-hook"
    Then the exit code is 0
    And the file ".claude/settings.json" under the workspace root contains "command -v niwa"
    And the file ".claude/settings.json" under the workspace root contains "exec niwa instance from-hook"

  # ---------------------------------------------------------------------
  # Create-path atomicity (DESIGN Decision 8): a delegated create that fails
  # AFTER `git worktree add` must reconcile rather than strand state. Session
  # creation is already atomic; content install is not, so without the
  # reconciliation the tool call fails while the worktree and an `active`
  # session record both survive -- and `niwa worktree list` then reports an
  # active worktree no process is in.
  # ---------------------------------------------------------------------

  @critical
  Scenario: a failed delegated create leaves no active session behind
    Given a clean niwa environment
    And a fake claude reporting version "2.1.183" is on PATH
    And a local git server is set up
    And a single-repo channeled workspace "wd-rollback" exists with repo content
    When I run "niwa create wd-rollback"
    Then the exit code is 0
    And the repo "apps/app" exists in instance "wd-rollback"
    # Break content install only -- the instance itself is already built.
    When the content source "content/repos/app.md" is missing from instance "wd-rollback"
    And I pipe a WorktreeCreate hook for repo "apps/app" with name "doomed" in instance "wd-rollback"
    # The hook must fail so Claude Code does not chdir into a rolled-back path.
    Then the exit code is not 0
    # niwa is the system of record: no active row, and nothing left on disk.
    When I run "niwa worktree list" from channeled instance "wd-rollback"
    Then the exit code is 0
    And the output does not contain "active"
    And no extra worktree is registered for repo "apps/app" in instance "wd-rollback"
