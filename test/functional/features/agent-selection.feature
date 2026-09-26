Feature: choosing which agent niwa launches
  Preparation serves both agents; launching picks one. This feature covers the
  picking. Four sources answer the question, and the answer is resolved once:
  the --harness flag, the NIWA_DISPATCH_HARNESS environment variable, a
  workspace's [workspace].default_agent, and the developer's own machine-wide
  [global].default_dispatch_harness, in that order, falling back to claude.

  The flag is deliberately not called --agent. On niwa dispatch that name
  already means the subagent type forwarded INTO the launched agent -- a role
  within it -- and the two settings answer different questions.

  The machine-wide rung is why `niwa config set default-dispatch-harness`
  exists. It writes ~/.config/niwa/config.toml, a file niwa never
  re-materializes from anywhere. The obvious-looking place,
  [workspace].default_agent inside a workspace's .niwa/, is often a snapshot
  replaced wholesale on the next refresh, so a setting written there can go away without saying so. See
  docs/guides/codex-agent.md.

  None of this changes what niwa apply prepares. Every apply prepares the tree
  for every agent niwa supports whatever any of these four say, which is why
  create and apply still have no agent flag at all (asserted in
  codex-agent.feature).

  Guide: docs/guides/codex-agent.md

  @critical
  Scenario: the flag launches an agent the workspace never asked for
    # The workspace states no default_agent, so it resolves to claude. The flag
    # is the whole difference, and the mapping records what actually launched.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01b00000-0000-7000-8000-00000000cafe"
    When I run "niwa dispatch some-task --detach --harness codex" from the workspace root
    Then the exit code is 0
    And the dispatch mapping for session "01b00000-0000-7000-8000-00000000cafe" records agent "codex"
    # The verb and the handle are the agent's own, and the trust override rides
    # with them: a resume command without it reaches the right session and comes
    # up read-only, which is exactly the shape the defect had. Asserted as one
    # line rather than as two substrings -- dispatch prints the session id on a
    # line of its own, so a bare id assertion passes even when the hint block
    # names a different handle entirely.
    And the printed resume command for "01b00000-0000-7000-8000-00000000cafe" grants the dispatched instance

  @critical
  Scenario: the machine-wide setting picks the agent, and dispatch reads it
    # This is the whole claim behind `niwa config set default-dispatch-harness`:
    # the value lands somewhere niwa actually reads, with no flag and no
    # environment variable on the dispatch command line.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa config set default-dispatch-harness codex"
    Then the exit code is 0
    And the output contains "Default dispatch harness set to codex"
    Given a fake codex for dispatch with session "01c00000-0000-7000-8000-00000000d00d"
    When I run "niwa dispatch some-task --detach" from the workspace root
    Then the exit code is 0
    And the dispatch mapping for session "01c00000-0000-7000-8000-00000000d00d" records agent "codex"

  @critical
  Scenario: a workspace that states its agent outranks the machine-wide setting
    # The machine-wide value is the broadest rung: it fills in for workspaces
    # that state nothing rather than overriding those that do. A developer who
    # wants the other agent here reaches for NIWA_DISPATCH_HARNESS or --harness,
    # which both outrank the workspace.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      default_agent = "claude"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa config set default-dispatch-harness codex"
    Then the exit code is 0
    Given a fake claude for dispatch with session "01d00000-0000-7000-8000-00000000feed"
    When I run "niwa dispatch some-task --detach" from the workspace root
    Then the exit code is 0
    And the dispatch mapping for session "01d00000-0000-7000-8000-00000000feed" records agent "claude"

  @critical
  Scenario: every source is held to the same closed set
    # An unknown agent is rejected wherever it is typed, and the rejection
    # names the values that would have worked -- the person reading it is by
    # definition the one who does not know them. The config command rejects
    # before writing, so a typo leaves nothing behind to fail later.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa config set default-dispatch-harness gemini"
    Then the exit code is not 0
    And the error output contains "unknown agent"
    And the error output contains "claude, codex"
    When I run "niwa dispatch some-task --detach --harness gemini" from the workspace root
    Then the exit code is not 0
    And the error output contains "unknown agent"
    # And the machine-wide setting is still unset, so a plain dispatch is
    # unaffected by the rejected value.
    When I run "niwa config unset default-dispatch-harness"
    Then the exit code is 0
    And the output contains "No machine-wide default dispatch harness set."

  @critical
  Scenario: a profile that still exports NIWA_AGENT is told the name moved
    # The variable was renamed rather than aliased, so a shell profile that has
    # exported NIWA_AGENT=codex since v0.9 now selects nothing. Nothing in the
    # resolution reports that on its own -- no rung held a bad value, the
    # variable simply is not read -- so without this the developer watches
    # claude start and is told nothing about why. The dispatch still runs; this
    # is a notice, not a refusal.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "01a30000-0000-7000-8000-00000000ba5e"
    And I set env "NIWA_AGENT" to "codex"
    When I run "niwa dispatch some-task --detach" from the workspace root
    Then the exit code is 0
    And the error output contains "NIWA_AGENT"
    And the error output contains "NIWA_DISPATCH_HARNESS=codex"
    # The notice reports; it never resolves. The launched agent is still the one
    # the rungs niwa reads resolve to.
    And the dispatch mapping for session "01a30000-0000-7000-8000-00000000ba5e" records agent "claude"

  @critical
  Scenario: naming an agent in --agent is warned about, not silently obeyed
    # --agent forwards a subagent type INTO the launched agent. A developer who
    # means "launch codex" and types it here gets a Claude worker carrying a
    # subagent type nothing defines, which fails inside the worker where niwa
    # wires no output. It stays a warning rather than a refusal: an agent's name
    # is a legitimate subagent type, and refusing would break a setup that works.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "01a20000-0000-7000-8000-00000000ba5e"
    When I run "niwa dispatch some-task --detach --agent codex" from the workspace root
    Then the exit code is 0
    And the error output contains "subagent type"
    And the error output contains "--harness codex"
    And the dispatch mapping for session "01a20000-0000-7000-8000-00000000ba5e" records agent "claude"
