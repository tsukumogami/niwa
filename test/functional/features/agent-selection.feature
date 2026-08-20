Feature: choosing which agent niwa launches
  Preparation serves both agents; launching picks one. This feature covers the
  picking. Four sources answer the question, and the answer is resolved once:
  the --launch-agent flag, the NIWA_AGENT environment variable, a workspace's
  [workspace].default_agent, and the developer's own machine-wide
  [global].default_agent, in that order, falling back to claude.

  The flag is deliberately not called --agent. On niwa dispatch that name
  already means the subagent type forwarded INTO the launched agent -- a role
  within it -- and the two settings answer different questions.

  The machine-wide rung is why `niwa config set default-agent` exists. It
  writes ~/.config/niwa/config.toml, a file niwa never re-materializes from
  anywhere. The obvious-looking place, [workspace].default_agent inside a
  workspace's .niwa/, is often a snapshot replaced wholesale on the next
  refresh, so a setting written there can go away without saying so. See
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
    When I run "niwa dispatch some-task --detach --launch-agent codex" from the workspace root
    Then the exit code is 0
    And the dispatch mapping for session "01b00000-0000-7000-8000-00000000cafe" records agent "codex"
    And the output contains "codex resume 01b00000-0000-7000-8000-00000000cafe"

  @critical
  Scenario: the machine-wide setting picks the agent, and dispatch reads it
    # This is the whole claim behind `niwa config set default-agent`: the value
    # lands somewhere niwa actually reads, with no flag and no environment
    # variable on the dispatch command line.
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa config set default-agent codex"
    Then the exit code is 0
    And the output contains "Default agent set to codex"
    Given a fake codex for dispatch with session "01c00000-0000-7000-8000-00000000d00d"
    When I run "niwa dispatch some-task --detach" from the workspace root
    Then the exit code is 0
    And the dispatch mapping for session "01c00000-0000-7000-8000-00000000d00d" records agent "codex"

  @critical
  Scenario: a workspace that states its agent outranks the machine-wide setting
    # The machine-wide value is the broadest rung: it fills in for workspaces
    # that state nothing rather than overriding those that do. A developer who
    # wants the other agent here reaches for NIWA_AGENT or --launch-agent,
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
    When I run "niwa config set default-agent codex"
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
    When I run "niwa config set default-agent gemini"
    Then the exit code is not 0
    And the error output contains "unknown agent"
    And the error output contains "claude, codex"
    When I run "niwa dispatch some-task --detach --launch-agent gemini" from the workspace root
    Then the exit code is not 0
    And the error output contains "unknown agent"
    # And the machine-wide setting is still unset, so a plain dispatch is
    # unaffected by the rejected value.
    When I run "niwa config unset default-agent"
    Then the exit code is 0
    And the output contains "No machine-wide default agent set."
