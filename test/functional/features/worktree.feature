Feature: niwa worktree (renamed from niwa session)
  End-to-end scenarios for the canonical `niwa worktree` command tree and
  its backward-compatible `niwa session` alias. The worktree commands drive
  the same lifecycle as before; only the command name changed. The alias
  must keep working and must print a one-line deprecation notice to stderr.

  # ---------------------------------------------------------------------
  # Canonical create path: niwa worktree create <repo> <purpose>.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa worktree create scaffolds a worktree and lifecycle state
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wt-create" exists
    When I run "niwa create wt-create"
    Then the exit code is 0
    When I call niwa worktree create for repo "app" with purpose "wt-create-fixture" in instance "wt-create"
    Then the last session is active in instance "wt-create"
    And the session worktree exists in instance "wt-create"
    When I run "niwa worktree list --status active" from channeled instance "wt-create"
    Then the exit code is 0
    And the output contains "SESSION_ID"
    And the output contains "available"
    # Cleanup
    When I call niwa_destroy_session in instance "wt-create"
    Then the session is ended in instance "wt-create"

  # ---------------------------------------------------------------------
  # Content parity: niwa worktree create installs the owning repo's CLAUDE
  # content, the workspace-context rules import, and a purpose/branch layer.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa worktree create installs repo content, rules import, and purpose/branch layer
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wt-content" exists with repo content
    When I run "niwa create wt-content"
    Then the exit code is 0
    When I call niwa worktree create for repo "app" with purpose "ship-the-thing" in instance "wt-content"
    Then the last session is active in instance "wt-content"
    And the session worktree exists in instance "wt-content"
    # The owning repo's CLAUDE.local.md (the repo-content parity payload).
    And the file "CLAUDE.local.md" in the last worktree contains "app repo content layer"
    # The worktree rules import pointing at the instance workspace-context.md.
    And the file ".claude/rules/worktree-imports.md" exists in the last worktree
    And the file ".claude/rules/worktree-imports.md" in the last worktree contains "workspace-context.md"
    # The generated purpose/branch layer.
    And the file "CLAUDE.local.md" in the last worktree contains "Worktree Context"
    And the file "CLAUDE.local.md" in the last worktree contains "ship-the-thing"
    # Cleanup
    When I call niwa_destroy_session in instance "wt-content"
    Then the session is ended in instance "wt-content"

  # ---------------------------------------------------------------------
  # Re-sync path: niwa worktree apply re-installs a worktree's content
  # idempotently (the worktree analog of niwa apply). A second run must
  # produce no spurious change, and the deprecated `niwa session apply`
  # alias must resolve and warn.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa worktree apply re-syncs worktree content idempotently
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wt-apply" exists with repo content
    When I run "niwa create wt-apply"
    Then the exit code is 0
    When I call niwa worktree create for repo "app" with purpose "resync-the-thing" in instance "wt-apply"
    Then the last session is active in instance "wt-apply"
    And the session worktree exists in instance "wt-apply"
    # First apply: re-sync the existing worktree (no CreateSession).
    When I call niwa worktree apply for the last session in instance "wt-apply"
    Then the exit code is 0
    And the file "CLAUDE.local.md" in the last worktree contains "app repo content layer"
    And the file ".claude/rules/worktree-imports.md" in the last worktree contains "workspace-context.md"
    And the file "CLAUDE.local.md" in the last worktree contains "resync-the-thing"
    # Snapshot the content, then re-run apply and assert nothing changed.
    When I snapshot the file "CLAUDE.local.md" in the last worktree
    And I snapshot the file ".claude/rules/worktree-imports.md" in the last worktree
    And I call niwa worktree apply for the last session in instance "wt-apply"
    Then the exit code is 0
    And the file "CLAUDE.local.md" in the last worktree is unchanged
    And the file ".claude/rules/worktree-imports.md" in the last worktree is unchanged
    # The deprecated alias resolves to the same command and warns.
    When I call niwa session apply for the last session in instance "wt-apply"
    Then the exit code is 0
    And the last command stderr contains the session deprecation notice
    And the file "CLAUDE.local.md" in the last worktree is unchanged
    # Cleanup
    When I call niwa_destroy_session in instance "wt-apply"
    Then the session is ended in instance "wt-apply"

  # ---------------------------------------------------------------------
  # Alias contract: niwa session create still works AND warns.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa session create still works and emits the deprecation notice
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "wt-alias" exists
    When I run "niwa create wt-alias"
    Then the exit code is 0
    When I call niwa_create_session for repo "app" with purpose "wt-alias-fixture" in instance "wt-alias"
    Then the last session is active in instance "wt-alias"
    And the last command stderr contains the session deprecation notice
    # Cleanup
    When I call niwa_destroy_session in instance "wt-alias"
    Then the session is ended in instance "wt-alias"
