Feature: prepare every instance for both agents
  Every instance niwa prepares serves Claude Code and Codex alike: the
  niwa-owned levels carry CLAUDE.md and AGENTS.md side by side, and the Claude
  tree is written in full whatever default_agent says. That setting selects
  which agent a niwa-launched session runs, not what preparation produces.
  niwa still writes no AGENTS.md inside a cloned repository, so a repo's own
  committed AGENTS.md is never clobbered.

  Design: docs/designs/current/DESIGN-dual-agent-workspace.md
  Requirements: docs/prds/PRD-dual-agent-workspace.md

  What a Codex session "sees" is decided here against the single context file
  Codex's first-match rule selects for a directory: the walk starts at the
  nearest ancestor holding a project-root marker (`.git`), reads one file per
  directory down to the working directory, and takes AGENTS.override.md ahead
  of AGENTS.md. Every "Codex context at" step below reports that selection, so
  a scenario fails when a lower-precedence candidate shadows the composed file
  -- the silent failure the whole override design exists to prevent. No step
  needs a live session, a network, or a model.

  The scenarios that do need a live session gate on `codex` being on PATH and
  skip when it is absent, so the @critical set stays offline and fast. They are
  tagged @codex-live and are never the only coverage for a mechanism, except
  the interactive start, where a live check carries information nothing else
  can.

  Coverage of the PRD's acceptance criteria, by ordinal. Every criterion is
  named by the scenario that decides it; where two scenarios share one, both
  are named.

     1  a prepared instance serves a Codex session from the instance root down
     2  the three compatibility scenarios below, plus the unit-level
        exclusivity inversions the criterion itself enumerates
     3  a prepared instance serves a Codex session from the instance root down
     4  a worktree carries the workspace context and its own framing
     5  a prepared instance serves a Codex session from the instance root down
     6  a workspace with nothing to say leaves the repository's own context in
        place; committed content at niwa's names degrades loudly
     7  the declared budget covers a context chain past Codex's default
     8  the workspace's skills reach Codex whole and namespaced
     9  a live Codex session writes a file on its first attempt (live)
    10  trust entries are canonical, one per repository, and additive;
        a worktree carries the workspace context and its own framing
    11  a prepared instance serves a Codex session from the instance root down
        (the offline half: no hook definitions and no hook state anywhere);
        a live interactive Codex session starts clean (live)
    12  every offline scenario that applies asserts git status;
        a live interactive Codex session starts clean (live)
    13  trust entries are canonical, one per repository, and additive;
        an unreadable credential file fails neither create nor apply
    14  trust entries are canonical, one per repository, and additive
    15  a codex-default workspace still materializes the whole Claude tree
    16  niwa dispatch refuses in a codex-default workspace
    17  a claude-default workspace materializes both agents' context too
    18  re-applying three times adds nothing; the skills scenario's re-applies;
        trust entries are canonical, one per repository, and additive
    19  changed content replaces the previous content everywhere

  @critical
  Scenario: a codex-default workspace still materializes the whole Claude tree
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with a "ws.md" source file and body:
      """
      [workspace]
      name = "ws"
      default_agent = "codex"

      [groups.tools]

      [claude.content.workspace]
      source = "ws.md"

      [claude.content.repos.app]
      source = "ws.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the instance "ws" exists
    And the file "AGENTS.md" exists in instance "ws"
    And the file "AGENTS.md" in instance "ws" contains "mcpServers"
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "CLAUDE.md" in instance "ws" contains "mcpServers"
    And the file "tools/app/CLAUDE.local.md" exists in instance "ws"
    # niwa writes AGENTS.override.md into repositories, never AGENTS.md: this
    # assertion guards the repo's own committed file and must stay.
    And the file "tools/app/AGENTS.md" does not exist in instance "ws"
    # A config declaring the agent setting re-applies with no migration step.
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "AGENTS.md" exists in instance "ws"

  @critical
  Scenario: niwa dispatch refuses in a codex-default workspace
    # niwa dispatch launches a Claude worker, so it refuses when the workspace
    # agent is codex rather than silently preparing a Codex instance a Claude
    # worker cannot read. The refusal fires before any instance is provisioned.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      default_agent = "codex"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa dispatch some-task --detach" from the workspace root
    Then the exit code is not 0
    And the error output contains "does not support"
    And the error output contains "NIWA_AGENT=claude"

  @critical
  Scenario: a claude-default workspace materializes both agents' context too
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with a "ws.md" source file and body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [claude.content.workspace]
      source = "ws.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the instance "ws" exists
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "AGENTS.md" exists in instance "ws"
    And the file "AGENTS.md" in instance "ws" contains "mcpServers"
    # A config predating the agent setting re-applies unchanged, and still
    # serves both agents: criterion 17 asks for an instance that serves both,
    # not merely one that exits zero, so the Codex side is asserted too.
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "CLAUDE.md" exists in instance "ws"
    And the file "AGENTS.md" exists in instance "ws"
    And the Codex payload of instance "ws" is reachable from "ws/tools/app"
    And the developer Codex config trusts repo "app" in instance "ws"

  # ---------------------------------------------------------------------
  # Criteria 1, 3, 5, 11 (offline), 12 (offline): one prepared instance,
  # read from the root and from three directories deep inside a repository
  # that ships context files of its own.
  #
  # No step here sets NIWA_* or CODEX_HOME, and the selection the assertions
  # read is a property of the filesystem, so it is the same selection a shell
  # with no environment preparation would make.
  # ---------------------------------------------------------------------

  @critical
  Scenario: a prepared instance serves a Codex session from the instance root down
    Given a clean niwa environment
    And a local git server is set up
    And a staged file "AGENTS.md" with body:
      """
      The app repository's own context. SENTINEL-REPO-COMMITTED
      """
    And a staged file "docs/AGENTS.md" with body:
      """
      Context for the docs subtree only. SENTINEL-INTERMEDIATE
      """
    And a staged directory "docs/deep/deeper"
    And a source repo "app" exists with the staged files
    And a staged file ".niwa/content/instance.md" with body:
      """
      Instance layer. SENTINEL-INSTANCE
      """
    And a staged file ".niwa/content/group.md" with body:
      """
      Group layer. SENTINEL-GROUP
      """
    And a staged file ".niwa/content/repos/app.md" with body:
      """
      Repository layer. SENTINEL-REPO-CONFIGURED
      """
    And a config repo "ws" exists with the staged files and body:
      """
      [workspace]
      name = "ws"
      content_dir = "content"

      [groups.tools]

      [claude.content.workspace]
      source = "instance.md"

      [claude.content.groups.tools]
      source = "group.md"

      [claude.content.repos.app]
      source = "repos/app.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    # Criterion 1: the instance root.
    And the Codex context at "ws" selects "AGENTS.md"
    And the Codex context at "ws" contains "SENTINEL-INSTANCE"
    # Criterion 3: three directories deep, with a committed context file in an
    # intermediate directory. The walk stops at the repository's .git, so every
    # outer layer has to arrive through the composed override; the intermediate
    # file proves the check is per-directory and not a repo-root read.
    And the Codex context at "ws/tools/app/docs/deep/deeper" selects "AGENTS.override.md,docs/AGENTS.md"
    And the Codex context at "ws/tools/app/docs/deep/deeper" contains "SENTINEL-INSTANCE"
    And the Codex context at "ws/tools/app/docs/deep/deeper" contains "SENTINEL-GROUP"
    And the Codex context at "ws/tools/app/docs/deep/deeper" contains "SENTINEL-REPO-CONFIGURED"
    And the Codex context at "ws/tools/app/docs/deep/deeper" contains "SENTINEL-REPO-COMMITTED"
    And the Codex context at "ws/tools/app/docs/deep/deeper" contains "SENTINEL-INTERMEDIATE"
    # Criterion 5: the repository's own file coexists with the composed one and
    # comes out of the apply byte-identical.
    And the file "AGENTS.md" at "ws/tools/app" matches git HEAD
    # The budget and the skills reach a session through the repository's own
    # .codex entry; asserting on the instance payload alone would pass with the
    # link missing, dangling, or retargeted.
    And the Codex payload of instance "ws" is reachable from "ws/tools/app"
    # Criterion 11, offline half, and the no-credentials rule.
    And instance "ws" declares no Codex hooks
    And the Codex payload of instance "ws" declares no credentials
    # Criterion 12, offline half.
    And the git status of every repo in instance "ws" is clean
    And the git exclude of repo "app" in instance "ws" carries the bare Codex patterns

  # ---------------------------------------------------------------------
  # Criterion 6: with nothing configured at any layer, niwa writes no file at
  # its own name. An empty or marker-only override would claim the directory's
  # single context slot and suppress the repository's own file in silence.
  # ---------------------------------------------------------------------

  @critical
  Scenario: a workspace with nothing to say leaves the repository's own context in place
    Given a clean niwa environment
    And a local git server is set up
    And a staged file "AGENTS.md" with body:
      """
      The app repository's own context. SENTINEL-REPO-COMMITTED
      """
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the file "AGENTS.override.md" does not exist at "ws/tools/app"
    And the Codex context at "ws/tools/app" selects "AGENTS.md"
    And the Codex context at "ws/tools/app" contains "SENTINEL-REPO-COMMITTED"
    And the git status of every repo in instance "ws" is clean

  # ---------------------------------------------------------------------
  # Criterion 7: the byte budget. Codex spends one counter across the whole
  # chain, outermost-first, and truncates with no marker and nothing on stderr,
  # so an under-declared budget eats the innermost layer in silence.
  # ---------------------------------------------------------------------

  @critical
  Scenario: the declared budget covers a context chain past Codex's default
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a staged file ".niwa/content/instance.md" of 20000 bytes ending with "SENTINEL-INSTANCE-TAIL"
    And a staged file ".niwa/content/group.md" of 20000 bytes ending with "SENTINEL-GROUP-TAIL"
    And a staged file ".niwa/content/repos/app.md" with body:
      """
      Repository layer, last in the chain and first to be cut.
      SENTINEL-REPO-TAIL
      """
    And a config repo "ws" exists with the staged files and body:
      """
      [workspace]
      name = "ws"
      content_dir = "content"

      [groups.tools]

      [claude.content.workspace]
      source = "instance.md"

      [claude.content.groups.tools]
      source = "group.md"

      [claude.content.repos.app]
      source = "repos/app.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the Codex context at "ws/tools/app" selects "AGENTS.override.md"
    And the Codex context at "ws/tools/app" exceeds the default Codex budget
    And the Codex context at "ws/tools/app" fits the budget declared by instance "ws"
    And the Codex context at "ws/tools/app" contains "SENTINEL-REPO-TAIL"

  # ---------------------------------------------------------------------
  # Criterion 8: the workspace's skills, delivered whole and under the same
  # name Claude resolves them by. The marketplace is a repository this
  # workspace clones, so the whole scenario runs offline.
  # ---------------------------------------------------------------------

  @critical
  Scenario: the workspace's skills reach Codex whole and namespaced
    Given a clean niwa environment
    And a local git server is set up
    And a fake claude for plugin pre-warming
    And a staged file ".claude-plugin/marketplace.json" with body:
      """
      {"name":"demo-market","plugins":[{"name":"demo","source":"./plugins/demo"}]}
      """
    And a staged file "plugins/demo/.claude-plugin/plugin.json" with body:
      """
      {"name":"demo","version":"0.1.0"}
      """
    And a staged file "plugins/demo/skills/greet/SKILL.md" with body:
      """
      ---
      name: greet
      ---
      SENTINEL-SKILL-BODY
      """
    And a staged file "plugins/demo/references/notes.md" with body:
      """
      Reference material the skill points at. SENTINEL-REFERENCE
      """
    And a staged file "plugins/demo/scripts/run.sh" with body:
      """
      #!/bin/sh
      echo SENTINEL-SCRIPT
      """
    And a source repo "mkt" exists with the staged files
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [claude]
      plugins = ["demo@demo-market"]

      [[claude.marketplaces]]
      source = "repo:mkt/.claude-plugin/marketplace.json"

      [repos.mkt]
      url = "{repo:mkt}"
      group = "tools"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    # The plugin resolves under its own name, and the delivered root is the
    # whole plugin: manifest, references, and scripts, every file byte-identical.
    And the Codex skills link "demo" of instance "ws" mirrors "ws/tools/mkt/plugins/demo"
    And the file ".claude-plugin/plugin.json" exists at "ws/.codex/skills/demo"
    And the file "references/notes.md" exists at "ws/.codex/skills/demo"
    And the file "scripts/run.sh" exists at "ws/.codex/skills/demo"
    And the Codex payload of instance "ws" has exactly 1 skills link
    # Reconciliation, not accumulation: the set is the same after re-applying.
    When I run "niwa apply ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the Codex payload of instance "ws" has exactly 1 skills link
    And the Codex skills link "demo" of instance "ws" mirrors "ws/tools/mkt/plugins/demo"

  # ---------------------------------------------------------------------
  # Criteria 10, 13, 14: the one write outside the instance. The workspace root
  # is reached through a symlink here, so an entry keyed by the path as handed
  # to niwa -- present, well-formed, and useless -- fails the canonical check.
  # ---------------------------------------------------------------------

  @critical
  Scenario: trust entries are canonical, one per repository, and additive
    Given a clean niwa environment
    And a local git server is set up
    And the developer Codex config exists with body:
      """
      model = "gpt-5-codex"

      [tui]
      theme = "dark"
      """
    And the developer Codex home has a credential file
    And a source repo "app" exists
    And a source repo "lib" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [repos.lib]
      url = "{repo:lib}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    # From here on every path niwa derives carries a symlinked component.
    Given the registry entry "ws" is re-pointed through a symlink
    When I run "niwa create ws"
    Then the exit code is 0
    And the developer Codex config has exactly 2 project entries
    And the developer Codex config trusts repo "app" in instance "ws"
    And the developer Codex config trusts repo "lib" in instance "ws"
    # Criterion 14: nothing of the developer's changed, and nothing global was
    # added, so a repository outside any instance behaves exactly as before.
    And the developer Codex config grew only by project entries inside the workspace root
    # Criterion 13: the login state is never read or written.
    And the developer Codex credential file is unchanged
    # Criteria 10 and 18: three applies, same entries.
    When I run "niwa apply ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the developer Codex config has exactly 2 project entries
    And the developer Codex config trusts repo "app" in instance "ws"
    And the developer Codex config grew only by project entries inside the workspace root
    And the developer Codex credential file is unchanged

  # ---------------------------------------------------------------------
  # Criterion 13, second half: niwa never opens the credential file, so one it
  # cannot read fails nothing.
  # ---------------------------------------------------------------------

  @critical
  Scenario: an unreadable credential file fails neither create nor apply
    Given a clean niwa environment
    And a local git server is set up
    And the developer Codex home has a credential file
    And the developer Codex credential file is unreadable
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the developer Codex credential file is unchanged
    And the developer Codex config trusts repo "app" in instance "ws"

  # ---------------------------------------------------------------------
  # Criteria 4, 10 (worktree half), 12: a niwa-managed worktree is first-class
  # for Codex. The instance-only sentinel is what a collapse to
  # current-directory-only discovery would lose.
  # ---------------------------------------------------------------------

  @critical
  Scenario: a worktree carries the workspace context and its own framing
    Given a clean niwa environment
    And a local git server is set up
    And a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a staged file ".niwa/content/instance.md" with body:
      """
      Instance layer. SENTINEL-INSTANCE
      """
    And a config repo "ws" exists with the staged files and body:
      """
      [workspace]
      name = "ws"
      content_dir = "content"

      [groups.tools]

      [claude.content.workspace]
      source = "instance.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I call niwa worktree create for repo "app" with purpose "ship-the-thing" in instance "ws"
    Then the last session is active in instance "ws"
    And the Codex context at "{worktree}" selects "AGENTS.override.md"
    And the Codex context at "{worktree}" contains "SENTINEL-INSTANCE"
    And the Codex context at "{worktree}" contains "Worktree Context"
    And the Codex context at "{worktree}" contains "ship-the-thing"
    And the Codex context at "{worktree}" contains "- Branch:"
    And the Codex payload of instance "ws" is reachable from "{worktree}"
    And the git status of the last worktree is clean
    # The repository's own entry covers its worktrees; a per-worktree entry
    # would leave one behind in the developer's config for every worktree that
    # ever existed.
    And the developer Codex config has exactly 1 project entry
    And the developer Codex config does not trust the last worktree

  # ---------------------------------------------------------------------
  # Criterion 18: re-applying adds nothing. Every append-shaped surface is
  # checked, since each accumulates differently when it regresses.
  # ---------------------------------------------------------------------

  @critical
  Scenario: re-applying three times adds nothing
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a staged file ".niwa/content/instance.md" with body:
      """
      Instance layer. SENTINEL-INSTANCE
      """
    And a config repo "ws" exists with the staged files and body:
      """
      [workspace]
      name = "ws"
      content_dir = "content"

      [groups.tools]

      [claude.content.workspace]
      source = "instance.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the git exclude of repo "app" in instance "ws" carries the bare Codex patterns
    And the Codex payload of instance "ws" has exactly 1 config file
    And the developer Codex config has exactly 1 project entry
    And the Codex context at "ws/tools/app" selects "AGENTS.override.md"
    And the Codex context at "ws/tools/app" contains "SENTINEL-INSTANCE"
    And the git status of every repo in instance "ws" is clean

  # ---------------------------------------------------------------------
  # Criterion 19: refresh, not append. Yesterday's content must be gone from
  # the clone and from the worktree.
  # ---------------------------------------------------------------------

  @critical
  Scenario: changed content replaces the previous content everywhere
    Given a clean niwa environment
    And a local git server is set up
    And a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a staged file ".niwa/content/instance.md" with body:
      """
      Instance layer. SENTINEL-BEFORE
      """
    And a config repo "ws" exists with the staged files and body:
      """
      [workspace]
      name = "ws"
      content_dir = "content"

      [groups.tools]

      [claude.content.workspace]
      source = "instance.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I call niwa worktree create for repo "app" with purpose "refresh-the-thing" in instance "ws"
    Then the last session is active in instance "ws"
    And the Codex context at "ws/tools/app" contains "SENTINEL-BEFORE"
    And the Codex context at "{worktree}" contains "SENTINEL-BEFORE"
    Given a staged file ".niwa/content/instance.md" with body:
      """
      Instance layer. SENTINEL-AFTER
      """
    When the config repo "ws" is re-pushed with the staged files and body:
      """
      [workspace]
      name = "ws"
      content_dir = "content"

      [groups.tools]

      [claude.content.workspace]
      source = "instance.md"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And I run "niwa apply ws"
    Then the exit code is 0
    And the Codex context at "ws/tools/app" contains "SENTINEL-AFTER"
    And the Codex context at "ws/tools/app" does not contain "SENTINEL-BEFORE"
    When I call niwa worktree apply for the last session in instance "ws"
    Then the exit code is 0
    And the Codex context at "{worktree}" contains "SENTINEL-AFTER"
    And the Codex context at "{worktree}" does not contain "SENTINEL-BEFORE"
    And the git status of the last worktree is clean

  # ---------------------------------------------------------------------
  # Criteria 6, 10, 12: committed content at either name niwa writes. Each
  # shape degrades differently and all three are reported, nothing is
  # overwritten, and no repository is left dirty.
  # ---------------------------------------------------------------------

  @critical
  Scenario: committed content at niwa's names degrades loudly and is never overwritten
    Given a clean niwa environment
    And a local git server is set up
    And a staged file ".codex/config.toml" with body:
      """
      # the repository's own Codex payload
      SENTINEL-COMMITTED-PAYLOAD = true
      """
    And a source repo "owncodex" exists with the staged files
    And a staged file "AGENTS.override.md" with body:
      """
      The repository's own override. SENTINEL-COMMITTED-OVERRIDE
      """
    And a source repo "ownoverride" exists with the staged files
    And a staged symlink "AGENTS.md" pointing at "{home}/.codex/auth.json"
    And a source repo "linked" exists with the staged files
    And the developer Codex home has a credential file
    And a staged file ".niwa/content/instance.md" with body:
      """
      Instance layer. SENTINEL-INSTANCE
      """
    And a config repo "ws" exists with the staged files and body:
      """
      [workspace]
      name = "ws"
      content_dir = "content"

      [groups.tools]

      [claude.content.workspace]
      source = "instance.md"

      [repos.owncodex]
      url = "{repo:owncodex}"
      group = "tools"

      [repos.ownoverride]
      url = "{repo:ownoverride}"
      group = "tools"

      [repos.linked]
      url = "{repo:linked}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    # A committed .codex costs the repository everything: no link, no override,
    # and no trust entry, all reported and none of it silent.
    And the error output contains "tools/owncodex/.codex is occupied by something niwa did not write"
    And the file "AGENTS.override.md" does not exist at "ws/tools/owncodex"
    And the file ".codex/config.toml" at "ws/tools/owncodex" matches git HEAD
    And the developer Codex config does not trust repo "owncodex" in instance "ws"
    # A committed override alone costs only the composed context.
    And the error output contains "tools/ownoverride/AGENTS.override.md is occupied by something niwa did not write"
    And the file "AGENTS.override.md" at "ws/tools/ownoverride" matches git HEAD
    And the Codex payload of instance "ws" is reachable from "ws/tools/ownoverride"
    And the developer Codex config trusts repo "ownoverride" in instance "ws"
    # A committed context file that is a symlink is refused at the open, so the
    # target's bytes never reach a session's instruction context.
    And the error output contains "was not inlined"
    And the Codex context at "ws/tools/linked" selects "AGENTS.override.md"
    And the Codex context at "ws/tools/linked" contains "SENTINEL-INSTANCE"
    And the Codex context at "ws/tools/linked" does not contain "developer-login-state"
    And the developer Codex credential file is unchanged
    And the git status of every repo in instance "ws" is clean

  # ---------------------------------------------------------------------
  # Criterion 9, live and gated: a session in a freshly prepared repository
  # writes on its first attempt, with no setup command run first. The gate is
  # `codex` on PATH, a login the sandbox can use, and a machine whose kernel
  # lets Codex build its own sandbox -- the last because a container that
  # withholds unprivileged user namespaces blocks every session write whatever
  # niwa prepared. Any of the three missing leaves the scenario pending, not
  # failed.
  # ---------------------------------------------------------------------

  @codex-live
  Scenario: a live Codex session writes a file on its first attempt
    Given a clean niwa environment
    And codex is available
    And the Codex sandbox can run here
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run codex exec from "ws/tools/app" with prompt:
      """
      Create a file named codex-wrote-this.txt in the current directory containing the single word ready.
      """
    Then the exit code is 0
    And the file "codex-wrote-this.txt" exists at "ws/tools/app"

  # ---------------------------------------------------------------------
  # Criteria 11 and 12, live and gated: the interactive start, which is the one
  # place a live check carries information nothing offline can, and the git
  # status a session that only started leaves behind.
  # ---------------------------------------------------------------------

  @codex-live
  Scenario: a live interactive Codex session starts clean from the root and from a nested directory
    Given a clean niwa environment
    And codex is available
    And a local git server is set up
    And a staged directory "src/inner"
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I start an interactive codex session at "ws/tools/app" under a pty
    Then the codex session reached its ready state
    And the codex session output shows no trust or approval prompt
    When I start an interactive codex session at "ws/tools/app/src/inner" under a pty
    Then the codex session reached its ready state
    And the codex session output shows no trust or approval prompt
    And the git status of every repo in instance "ws" is clean
