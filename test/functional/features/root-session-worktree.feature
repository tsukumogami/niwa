Feature: worktrees from sessions launched at a root
  A Claude Code session launched at an instance root or at the workspace
  root has no repo for its working directory, so niwa writes its worktree
  integration into the root settings too: the WorktreeCreate/WorktreeRemove
  hooks that route EnterWorktree through `niwa worktree from-hook`, a
  PreToolUse guard (`niwa worktree guard`) that refuses the two ways of
  building a worktree that can't work from a root, and, at the instance root,
  a SessionStart hook (`niwa instance context`) that states the instance's
  layout. From a root the agent picks the repo by naming the worktree
  <repo>/<slug> or <group>/<repo>/<slug>.

  These scenarios drive the compiled binary offline. A fake `claude` on PATH
  makes the apply-time harness probe report hook support. Hook commands are
  run the way Claude Code runs them for a root session: started in the root,
  with CLAUDE_PROJECT_DIR naming it, and the hook payload on stdin. HOME is
  the scenario sandbox, so the Claude config the trust check reads is a
  scenario file, never the developer's.

  Design: docs/designs/current/DESIGN-enforced-agent-conformance.md

  Background:
    Given a clean niwa environment
    And a fake claude reporting version "2.1.183" is on PATH
    And a local git server is set up

  # ---------------------------------------------------------------------
  # Settings output at both roots.
  # ---------------------------------------------------------------------

  @critical
  Scenario: init and create write the worktree hooks, guard and context at the roots
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    # The workspace root gets the integration at init, before any instance.
    Then the workspace root settings register a "WorktreeCreate" hook running "worktree from-hook"
    And the workspace root settings register a "WorktreeRemove" hook running "worktree from-hook"
    And the workspace root settings register a "PreToolUse" hook with matcher "EnterWorktree|Bash" running "worktree guard"
    And the workspace root settings register no hook running "instance context"
    When I run "niwa create rs"
    Then the exit code is 0
    And the instance "rs" settings register a "WorktreeCreate" hook running "worktree from-hook"
    And the instance "rs" settings register a "WorktreeRemove" hook running "worktree from-hook"
    And the instance "rs" settings register a "PreToolUse" hook with matcher "EnterWorktree|Bash" running "worktree guard"
    And the instance "rs" settings register a "SessionStart" hook with matcher "startup|resume|clear|compact" running "instance context"
    # Repos keep their own hooks but never get the root-only guard or context.
    And the repo "public/niwa" in instance "rs" settings register a "WorktreeCreate" hook running "worktree from-hook"
    And the repo "public/niwa" in instance "rs" settings register no hook running "worktree guard"
    And the repo "public/niwa" in instance "rs" settings register no hook running "instance context"
    # A later apply keeps the workspace root's integration in place.
    When I run "niwa apply" from workspace "rs"
    Then the exit code is 0
    And the workspace root settings register a "PreToolUse" hook with matcher "EnterWorktree|Bash" running "worktree guard"
    And the workspace root settings register a "WorktreeCreate" hook running "worktree from-hook"

  Scenario: worktree_delegation = false leaves the workspace root without the integration
    Given a source repo "niwa" exists with a commit
    And a config repo "rs-off" exists with body:
      """
      [workspace]
      name = "rs-off"

      [claude]
      worktree_delegation = false

      [groups.public]

      [repos.niwa]
      url = "{repo:niwa}"
      group = "public"
      """
    When I run niwa init from config repo "rs-off"
    Then the exit code is 0
    When I run "niwa create rs-off"
    Then the exit code is 0
    When I run "niwa apply" from workspace "rs-off"
    Then the exit code is 0
    And the workspace root settings register no hook running "worktree from-hook"
    And the workspace root settings register no hook running "worktree guard"
    # The workspace-level switch covers the workspace root only; the
    # per-instance opt-out is `niwa init --no-worktree-delegation`.
    And the instance "rs-off" settings register a "PreToolUse" hook with matcher "EnterWorktree|Bash" running "worktree guard"

  Scenario: an instance opted out with --no-worktree-delegation gets no root hooks and no worktree sentence
    Given a worktree-delegation opt-out workspace "wd-optout" exists
    When I run "niwa create wd-optout"
    Then the exit code is 0
    And the instance "wd-optout" settings register no hook running "worktree from-hook"
    And the instance "wd-optout" settings register no hook running "worktree guard"
    And the file ".claude/settings.json" in instance "wd-optout" does not contain "EnterWorktree"
    And the repo "apps/app" in instance "wd-optout" settings register no hook running "worktree guard"
    When I run the instance context hook from the root of instance "wd-optout"
    Then the exit code is 0
    And the session-start context contains "apps/app"
    And the session-start context does not contain "EnterWorktree"

  # ---------------------------------------------------------------------
  # from-hook create and remove from the instance root.
  # ---------------------------------------------------------------------

  @critical
  Scenario: from the instance root a <repo>/<slug> name creates and removes a niwa worktree
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "." and name "niwa/fix-x" from the root of instance "rs"
    Then the exit code is 0
    And the printed worktree is a session of repo "niwa" with purpose "fix-x" in instance "rs"
    When I run "niwa worktree list" from workspace "rs"
    Then the exit code is 0
    And the output contains "fix-x"
    When I pipe a WorktreeRemove hook for the printed worktree path from the root of instance "rs"
    Then the exit code is 0
    And the printed worktree path does not exist
    When I run "niwa worktree list --status active" from workspace "rs"
    Then the exit code is 0
    And the output does not contain "fix-x"

  Scenario: from the instance root a <group>/<repo>/<slug> name picks the repo
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "." and name "public/koto/fix-y" from the root of instance "rs"
    Then the exit code is 0
    And the printed worktree is a session of repo "koto" with purpose "fix-y" in instance "rs"

  Scenario: a prefix wins over cwd, and cwd picks the repo for an unprefixed name
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "public/koto" and name "niwa/fix-x" from the root of instance "rs"
    Then the exit code is 0
    And the printed worktree is a session of repo "niwa" with purpose "fix-x" in instance "rs"
    When I pipe a WorktreeCreate hook with cwd "public/koto" and name "fix-z" from the root of instance "rs"
    Then the exit code is 0
    And the printed worktree is a session of repo "koto" with purpose "fix-z" in instance "rs"

  Scenario: a single-repo instance takes an unprefixed name from its root
    Given a root-session workspace "solo" exists with repos "niwa" in group "public"
    When I run "niwa create solo"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "." and name "fix-x" from the root of instance "solo"
    Then the exit code is 0
    And the printed worktree is a session of repo "niwa" with purpose "fix-x" in instance "solo"

  Scenario: with several repos and none selected, create refuses and lists them
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "." and name "fix-x" from the root of instance "rs"
    Then the exit code is not 0
    And the output is empty
    And the error output contains "no repo selected"
    And the error output contains "koto"
    And the error output contains "niwa"
    And the error output contains "koto/<slug>"
    And no worktree was created in instance "rs"
    When I pipe a WorktreeCreate hook with cwd "." and name "nosuchrepo/fix-x" from the root of instance "rs"
    Then the exit code is not 0
    And the error output contains "no repo selected"
    And no worktree was created in instance "rs"
    And no path named "nosuchrepo" exists under the workspace root

  # ---------------------------------------------------------------------
  # from-hook create and remove from the workspace root.
  # ---------------------------------------------------------------------

  Scenario: from the workspace root create resolves the instance from cwd and remove finds it again
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "rs" and name "niwa/fix-x" from the workspace root
    Then the exit code is 0
    And the printed worktree is a session of repo "niwa" with purpose "fix-x" in instance "rs"
    When I pipe a WorktreeRemove hook for the printed worktree path from the workspace root
    Then the exit code is 0
    And the printed worktree path does not exist

  Scenario: from the workspace root with cwd in no instance, create names the instances
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "." and name "niwa/fix-x" from the workspace root
    Then the exit code is not 0
    And the error output contains "not inside a niwa instance"
    And the error output contains "rs, rs-2"
    And no worktree was created in instance "rs"
    And no worktree was created in instance "rs-2"

  Scenario: the workspace-root hook honors an instance's --no-worktree-delegation opt-out
    Given a worktree-delegation opt-out workspace "wd-optout" exists
    When I run "niwa create wd-optout"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "wd-optout/apps/app" and name "app/fix-x" from the workspace root
    Then the exit code is not 0
    And the error output contains "opted out of worktree delegation"
    And no worktree was created in instance "wd-optout"

  # ---------------------------------------------------------------------
  # The guard.
  # ---------------------------------------------------------------------

  @critical
  Scenario: the guard refuses hand-built worktrees at the instance root and lets the rest through
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a Bash call running "git -C public/niwa worktree add ../x" to the worktree guard from the root of instance "rs"
    Then the exit code is 2
    And the error output contains "use EnterWorktree with name koto/<slug>"
    And the error output contains "Repos in this instance: koto, niwa"
    When I pipe an EnterWorktree call with path "public/niwa/.claude/worktrees/x" to the worktree guard from the root of instance "rs"
    Then the exit code is 2
    And the error output contains "use EnterWorktree with name"
    # Allowed: a name, an unrelated command, a read-only worktree command,
    # and input the guard can't read (it fails open).
    When I pipe an EnterWorktree call with name "niwa/fix-x" to the worktree guard from the root of instance "rs"
    Then the exit code is 0
    When I pipe a Bash call running "git worktree list" to the worktree guard from the root of instance "rs"
    Then the exit code is 0
    When I pipe a Bash call running "ls -la" to the worktree guard from the root of instance "rs"
    Then the exit code is 0
    When I pipe "this is not json" to the worktree guard from the root of instance "rs"
    Then the exit code is 0
    And the error output contains "allowing"

  Scenario: the guard lets EnterWorktree into an existing niwa worktree through
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a WorktreeCreate hook with cwd "." and name "niwa/fix-x" from the root of instance "rs"
    Then the exit code is 0
    When I pipe an EnterWorktree call with the printed worktree path to the worktree guard from the root of instance "rs"
    Then the exit code is 0

  Scenario: at the workspace root the guard points the agent at an instance
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I pipe a Bash call running "git worktree add ../x" to the worktree guard from the workspace root
    Then the exit code is 2
    And the error output contains "This session is at a workspace root"
    And the error output contains "cd into one of: rs"
    And the error output contains "use EnterWorktree with name <repo>/<slug>"

  # ---------------------------------------------------------------------
  # Session-start context.
  # ---------------------------------------------------------------------

  Scenario: the instance context hook states the instance layout at the instance root only
    Given a root-session workspace "rs" exists with repos "niwa,koto" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I run the instance context hook from the root of instance "rs"
    Then the exit code is 0
    And the session-start context contains "This directory is a niwa instance root."
    And the session-start context contains "- public/niwa"
    And the session-start context contains "- public/koto"
    And the session-start context contains "Worktrees here are created with EnterWorktree using a name of the form <repo>/<slug>."
    When I run the instance context hook from the workspace root
    Then the exit code is 0
    And the output is empty

  # ---------------------------------------------------------------------
  # Trust: Claude Code runs no project hooks in an untrusted directory.
  # ---------------------------------------------------------------------

  Scenario: apply warns when the instance root has no trusted ancestor
    Given a root-session workspace "rs" exists with repos "niwa" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    When I run "niwa apply" from workspace "rs"
    Then the exit code is 0
    And the error output contains "is untrusted in Claude Code"
    And the error output contains "trust_instances = true"

  Scenario: apply does not warn when the workspace root is trusted
    Given the Claude config in HOME trusts the workspace root
    And a root-session workspace "rs" exists with repos "niwa" in group "public"
    When I run "niwa create rs"
    Then the exit code is 0
    And the error output does not contain "is untrusted in Claude Code"
    When I run "niwa apply" from workspace "rs"
    Then the exit code is 0
    And the error output does not contain "is untrusted in Claude Code"
