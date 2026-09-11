Feature: niwa dispatch: accepting messages from other sessions
  End-to-end scenarios for `niwa dispatch --accept-session-messages` and the
  `[global] accept_session_messages_on_dispatch` machine setting, using the local
  bare-repo server and the FAKE `claude` on PATH from the dispatch suite. No real
  claude, daemon, or network.

  When the behavior resolves on and the dispatched agent can receive it, niwa puts
  `crossSessionInbound: "accept"` in the single `--settings` document the worker is
  launched with, prints one audit line naming what turned it on, records the fact
  in the durable session mapping, and shows a one-time explanation. The
  explanation prints wherever the behavior takes effect, terminal or not; what a
  terminal decides is whether niwa remembers having shown it, so away from one it
  says so and comes back. The flag overrides the machine setting in either
  direction; nothing else -- no workspace, instance, or repository settings source
  -- can turn it on.

  What these scenarios do NOT cover: whether a message from another live Claude
  Code session actually reaches the worker without an approval prompt. That needs
  two live sessions and a real Claude daemon, so it is the PRD's manual delivery
  check rather than anything runnable here. Everything up to the launch seam --
  resolution, the document, the lines, the record, the report, the marker -- is
  here.

  Every dispatch below passes --detach, so nothing waits for a worker to finish.

  Design: docs/designs/DESIGN-dispatch-sendmessage-approval.md

  # --- Off by default ---
  #
  # The two shapes of "the developer did not ask for this": no key at all, and the
  # key explicitly false. Neither may put anything in front of the worker, and
  # neither may say anything.

  @critical
  Scenario: with no machine setting and no flag the worker keeps Claude Code's default
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aa000000-0000-4000-8000-000000000001"
    When I run "niwa dispatch off-by-default --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the dispatch mapping for session "aa000000-0000-4000-8000-000000000001" does not record session-message acceptance
    When I run "niwa list --json" from the workspace root
    Then the exit code is 0
    And the list JSON reports the dispatch instance as not accepting session messages

  @critical
  Scenario: a machine setting of false leaves the worker at Claude Code's default
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      accept_session_messages_on_dispatch = false
      """
    Given a fake claude for dispatch with session "aa000000-0000-4000-8000-000000000002"
    When I run "niwa dispatch off-by-setting --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the dispatch mapping for session "aa000000-0000-4000-8000-000000000002" does not record session-message acceptance
    When I run "niwa list --json" from the workspace root
    Then the exit code is 0
    And the list JSON reports the dispatch instance as not accepting session messages

  # --- On by flag ---
  #
  # The bare flag and its explicit =true form are the same request. Both name the
  # flag as the source, because a developer reading the line months later needs to
  # know whether they typed it or their machine did.

  @critical
  Scenario Outline: the flag turns acceptance on and says so once
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "<session>"
    When I run "niwa dispatch on-by-flag <flag> --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output has exactly 1 line containing "accepts messages from other sessions without asking"
    And the error output has exactly 1 line containing "without asking (source: --accept-session-messages); see https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"
    And the dispatch mapping for session "<session>" records session-message acceptance
    When I run "niwa list --json" from the workspace root
    Then the exit code is 0
    And the list JSON reports the dispatch instance as accepting session messages
    When I run "niwa list" from the workspace root
    Then the exit code is 0
    And the output contains "(accepts session messages)"

    Examples:
      | flag                            | session                              |
      | --accept-session-messages       | ab000000-0000-4000-8000-000000000001 |
      | --accept-session-messages=true  | ab000000-0000-4000-8000-000000000002 |

  @critical
  Scenario: the flag turns acceptance on over a machine setting of false
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      accept_session_messages_on_dispatch = false
      """
    Given a fake claude for dispatch with session "ab000000-0000-4000-8000-000000000003"
    When I run "niwa dispatch flag-over-false --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output has exactly 1 line containing "without asking (source: --accept-session-messages); see https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"
    And the error output has exactly 0 lines containing "keeps Claude Code's default for messages from other sessions"
    And the dispatch mapping for session "ab000000-0000-4000-8000-000000000003" records session-message acceptance

  # --- On by machine setting ---

  @critical
  Scenario: the machine setting turns acceptance on and names itself as the source
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      accept_session_messages_on_dispatch = true
      """
    Given a fake claude for dispatch with session "ac000000-0000-4000-8000-000000000001"
    When I run "niwa dispatch on-by-setting --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output has exactly 1 line containing "accepts messages from other sessions without asking"
    And the error output has exactly 1 line containing "without asking (source: machine setting accept_session_messages_on_dispatch); see https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"
    And the error output has exactly 0 lines containing "keeps Claude Code's default for messages from other sessions"
    And the dispatch mapping for session "ac000000-0000-4000-8000-000000000001" records session-message acceptance

  @critical
  Scenario: a flag agreeing with the machine setting still says it once
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      accept_session_messages_on_dispatch = true
      """
    Given a fake claude for dispatch with session "ac000000-0000-4000-8000-000000000002"
    When I run "niwa dispatch agreeing --accept-session-messages=true --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output has exactly 1 line containing "accepts messages from other sessions without asking"
    And the error output has exactly 0 lines containing "keeps Claude Code's default for messages from other sessions"

  # --- The flag turning the machine setting off ---
  #
  # The override line exists so that a developer who turned the behavior on
  # machine-wide and off for one dispatch sees which one won. It is printed only
  # when there was something to override: with no machine setting, =false is the
  # default and says nothing.

  @critical
  Scenario: --accept-session-messages=false overrides a true machine setting and says so
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      accept_session_messages_on_dispatch = true
      """
    Given a fake claude for dispatch with session "ad000000-0000-4000-8000-000000000001"
    When I run "niwa dispatch overridden --accept-session-messages=false --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the error output has exactly 1 line containing "keeps Claude Code's default for messages from other sessions (--accept-session-messages=false overrides the machine setting)"
    And the dispatch mapping for session "ad000000-0000-4000-8000-000000000001" does not record session-message acceptance

  @critical
  Scenario: --accept-session-messages=false with no machine setting says nothing
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ad000000-0000-4000-8000-000000000002"
    When I run "niwa dispatch nothing-to-override --accept-session-messages=false --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the error output has exactly 0 lines containing "keeps Claude Code's default for messages from other sessions"

  # --- The one-time explanation, and the marker that remembers it ---
  #
  # The paragraph exists because the asymmetry is not guessable: this dispatch
  # settles what the worker ACCEPTS and says nothing about what happens when the
  # worker speaks first into a session launched without the same setting. It runs
  # under a pty because niwa only remembers having shown it when a terminal was
  # there to read it.

  @critical
  Scenario: the explanation is shown once at a terminal and remembered beside config.toml
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the session-message notice marker does not exist
    And I record the niwa machine config
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch explain-me --accept-session-messages --detach" under a pty
    Then the exit code is 0
    And the error output contains the text:
      """
      accepting messages without asking is inbound only. A message this worker sends into a session launched without it, such as a coordinator dispatched earlier, one dispatched with the behavior off, or one another tool started, still waits for approval there when the two run in different permission modes; dispatching that session again with the behavior on clears it.
      """
    And the error output contains the text:
      """
      Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change.
      """
    And the error output contains the text:
      """
      To accept there too, set "Messages from your other sessions" to accept in Claude Code's /config, or add "crossSessionInbound": "accept" to ~/.claude/settings.json.
      """
    And the error output contains the text:
      """
      That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere.
      """
    And the error output contains the text:
      """
      niwa won't show this again; it's also at https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md
      """
    And the session-message notice marker exists
    And the niwa machine config is byte-for-byte unchanged
    When I run "niwa dispatch explain-me-again --accept-session-messages --detach" under a pty
    Then the exit code is 0
    And the error output has exactly 1 line containing "accepts messages from other sessions without asking"
    And the error output does not contain the text:
      """
      accepting messages without asking is inbound only
      """

  @critical
  Scenario: without a terminal the explanation says it will come back, and leaves no marker
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch no-terminal --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the error output contains the text:
      """
      niwa will show this again until it's been shown at a terminal; it's also at https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md
      """
    And the session-message notice marker does not exist
    When I run "niwa dispatch now-a-terminal --accept-session-messages --detach" under a pty
    Then the exit code is 0
    And the error output contains the text:
      """
      niwa won't show this again; it's also at https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md
      """
    And the session-message notice marker exists

  # --- Four dispatches at once, from four terminals, against one config dir ---
  #
  # The one-time notice, the marker, the audit line, and the mapping all have to
  # come out right when several dispatches race for the same configuration
  # directory. Each run gets its own session because the fake mints one per launch;
  # four distinct mappings is what says four workers started, rather than one
  # mapping written over four times.

  @critical
  Scenario: four parallel dispatches each get their own worker, line, and mapping
    Given a clean niwa environment
    # A workspace scaffolded in place, with no config source to reconcile. Every
    # other scenario here clones one from the local git server, but a config
    # source that is not GitHub is re-materialized on every provision, and four
    # of those at once collide in the shared staging directory the snapshot
    # writer swaps through -- a provisioning concurrency gap that also loses
    # session mappings, tracked separately as issue #297. What this scenario is
    # about is four dispatches racing for one configuration directory: the
    # notice, its marker, and the four session records.
    When I run "niwa init" from the workspace root
    Then the exit code is 0
    # Scaffold mode writes no registry entry, so there is no config.toml yet and
    # the record step below would have nothing to read. An empty [global] table
    # is the smallest file that changes no behavior.
    And the niwa machine config is replaced with:
      """
      [global]
      """
    And the session-message notice marker does not exist
    And I record the niwa machine config
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch parallel-task --accept-session-messages --detach" 4 times in parallel under a pty
    Then all parallel runs exit 0
    And the session-message notice marker exists
    And at least one parallel transcript contains the text:
      """
      accepting messages without asking is inbound only. A message this worker sends into a session launched without it, such as a coordinator dispatched earlier, one dispatched with the behavior off, or one another tool started, still waits for approval there when the two run in different permission modes; dispatching that session again with the behavior on clears it.
      """
    And every parallel transcript has exactly 1 line containing "accepts messages from other sessions without asking"
    And there are 4 dispatch mappings that record session-message acceptance
    And the niwa machine config is byte-for-byte unchanged

  # --- An agent that cannot receive it ---
  #
  # Codex has no setting for accepting messages from other sessions. A flag that
  # asks for it is reported and dropped; the dispatch still succeeds, because the
  # developer asked for a worker and the undeliverable extra is not a reason to
  # refuse one. A machine setting alone says nothing at all: it is a default, not
  # a request, and warning about every Codex dispatch on a machine that set it
  # would be noise.

  @critical
  Scenario: a Codex dispatch reports the flag it cannot honor and carries nothing
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01b10000-0000-7000-8000-000000000001"
    When I run "niwa dispatch codex-task --harness codex --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the error output contains the text:
      """
      niwa dispatch: --accept-session-messages does not apply to the "codex" agent and was ignored.
      """
    And the codex launch argv does not contain "crossSessionInbound"
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the error output does not contain the text:
      """
      accepting messages without asking is inbound only
      """
    And the session-message notice marker does not exist
    And the dispatch mapping for session "01b10000-0000-7000-8000-000000000001" does not record session-message acceptance

  @critical
  Scenario: a Codex dispatch under a machine setting alone says nothing
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      accept_session_messages_on_dispatch = true
      """
    Given a fake codex for dispatch with session "01b10000-0000-7000-8000-000000000002"
    When I run "niwa dispatch codex-quiet --harness codex --detach" from the workspace root
    Then the exit code is 0
    And the error output has exactly 0 lines containing "does not apply to the"
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the error output does not contain the text:
      """
      accepting messages without asking is inbound only
      """
    And the codex launch argv does not contain "crossSessionInbound"
    And the session-message notice marker does not exist
    And the dispatch mapping for session "01b10000-0000-7000-8000-000000000002" does not record session-message acceptance

  @critical
  Scenario: a Codex dispatch turning the machine setting off prints no override line
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      accept_session_messages_on_dispatch = true
      """
    Given a fake codex for dispatch with session "01b10000-0000-7000-8000-000000000003"
    When I run "niwa dispatch codex-off --harness codex --accept-session-messages=false --detach" from the workspace root
    Then the exit code is 0
    And the error output has exactly 0 lines containing "does not apply to the"
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the error output has exactly 0 lines containing "keeps Claude Code's default for messages from other sessions"

  # --- Configuration sources that cannot turn it on ---
  #
  # Whether a worker takes messages from other sessions is the developer's call,
  # not a cloned repository's. There is deliberately no downstream layer: no
  # workspace, instance, or repository settings source is read for it at all. Each
  # fixture below puts the key somewhere a reader might expect to work, and each
  # one must change nothing -- neither the launch nor the settings files niwa
  # materializes into the instance.

  Scenario: accept_session_messages_on_dispatch in the workspace config does not turn it on
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      accept_session_messages_on_dispatch = true

      [workspace]
      name = "myws"
      accept_session_messages_on_dispatch = true
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ae000000-0000-4000-8000-000000000001"
    When I run "niwa dispatch ws-key --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And no settings file niwa wrote into the dispatch instance contains "crossSessionInbound"

  Scenario: accept_session_messages_on_dispatch in a repository table does not turn it on
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      accept_session_messages_on_dispatch = true
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ae000000-0000-4000-8000-000000000002"
    When I run "niwa dispatch repo-key --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    # This fixture clones a repository, so the scan below reads a per-repository
    # file niwa wrote as well as the instance-root one:
    # <instance>/tools/app/.claude/settings.local.json.
    And no settings file niwa wrote into the dispatch instance contains "crossSessionInbound"

  Scenario: crossSessionInbound under claude.settings does not turn it on
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [claude.settings]
      crossSessionInbound = "accept"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ae000000-0000-4000-8000-000000000003"
    When I run "niwa dispatch claude-settings-key --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And no settings file niwa wrote into the dispatch instance contains "crossSessionInbound"

  Scenario: crossSessionInbound in the workspace root settings file does not turn it on
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And a file ".claude/settings.json" exists under the workspace root with body:
      """
      {"crossSessionInbound": "accept"}
      """
    Given a fake claude for dispatch with session "ae000000-0000-4000-8000-000000000004"
    When I run "niwa dispatch root-settings-file --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And no settings file niwa wrote into the dispatch instance contains "crossSessionInbound"

  Scenario: crossSessionInbound in a cloned repository's settings files does not turn it on
    Given a clean niwa environment
    And a local git server is set up
    And a staged file ".claude/settings.json" with body:
      """
      {"crossSessionInbound": "accept"}
      """
    And a staged file ".claude/settings.local.json" with body:
      """
      {"crossSessionInbound": "accept"}
      """
    And a source repo "app" exists with the staged files
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ae000000-0000-4000-8000-000000000005"
    When I run "niwa dispatch repo-settings-file --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    # This is the only fixture that asks whether niwa's materializer copies a
    # key out of a repository's committed settings into the file niwa owns. The
    # repository commits both paths; niwa owns .claude/settings.local.json and
    # replaces it wholesale with its own generated document, so what the step
    # reads there is niwa's file and not the fixture's. The committed
    # .claude/settings.json survives beside it and is never read: the step only
    # ever looks at a repository's settings.local.json.
    And no settings file niwa wrote into the dispatch instance contains "crossSessionInbound"

  # --- Coexistence with the other launch-settings contributors ---
  #
  # Remote control and acceptance both write into the launch settings, and there
  # is exactly one document: a second --settings element would leave the worker's
  # own precedence to decide which one it honored.

  Scenario: acceptance and remote control share the one settings document
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      permissions = "bypass"

      [claude.settings]
      permissions = "bypass"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      remote_control_on_dispatch = true
      keep_alive_on_dispatch = true
      accept_session_messages_on_dispatch = true
      """
    Given a fake claude for dispatch with session "af000000-0000-4000-8000-000000000001"
    When I run "niwa dispatch coexist --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the launched claude settings document has remoteControlAtStartup true
    And the launched claude was invoked with "--permission-mode bypassPermissions"
    And the launched claude prompt contains the keep-alive arming instruction
    And the dispatch mapping for session "af000000-0000-4000-8000-000000000001" records keep-alive
    And the dispatch mapping for session "af000000-0000-4000-8000-000000000001" records session-message acceptance

  # --- A prompt that looks like a flag ---
  #
  # The prompt travels after niwa's own `--`, so a task beginning with
  # `--settings=` is a task, not a second settings document.

  Scenario: a prompt beginning with --settings= reaches the worker as the prompt
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "b0000000-0000-4000-8000-000000000001"
    When I run "niwa dispatch --accept-session-messages --detach -- --settings={}" from the workspace root
    Then the exit code is 0
    And the launched claude prompt is "--settings={}"
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output has exactly 1 line containing "accepts messages from other sessions without asking"
    And the dispatch mapping for session "b0000000-0000-4000-8000-000000000001" records session-message acceptance

  # --- A dispatch that never happened ---
  #
  # The audit line, the explanation and the marker all follow a durable mapping.
  # A launch that failed has none, so none of them may appear -- niwa must not
  # claim a worker accepts anything when no worker started.

  Scenario: a failed launch says nothing about accepting messages
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that fails to launch
    When I run "niwa dispatch doomed --accept-session-messages --detach" from the workspace root
    Then the exit code is not 0
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    And the error output does not contain the text:
      """
      accepting messages without asking is inbound only
      """
    And the session-message notice marker does not exist
    And no dispatch-origin mapping remains

  # --- Standard output is unchanged ---
  #
  # Everything this feature prints goes to stderr. A script reading the resume
  # command off stdout must see the same bytes either way.

  Scenario: turning acceptance on changes nothing on standard output
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch stdout-off --detach" from the workspace root
    Then the exit code is 0
    And I remember the dispatch standard output
    When I run "niwa dispatch stdout-on --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the dispatch standard output matches the remembered one apart from instance names and session identifiers

  # --- A configuration directory niwa cannot write ---
  #
  # The marker is best effort. A directory niwa cannot write costs the developer a
  # repeated paragraph, never a failed dispatch.

  Scenario: an unwritable config directory repeats the explanation instead of failing
    Given a clean niwa environment
    And the scenario is skipped when tests run as root
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa config directory is not writable
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch unwritable-once --accept-session-messages --detach" under a pty
    Then the exit code is 0
    And the error output contains the text:
      """
      accepting messages without asking is inbound only
      """
    And the session-message notice marker does not exist
    When I run "niwa dispatch unwritable-twice --accept-session-messages --detach" under a pty
    Then the exit code is 0
    And the error output contains the text:
      """
      accepting messages without asking is inbound only
      """
    And the session-message notice marker does not exist

  # --- A machine configuration niwa cannot read ---
  #
  # A malformed or unreadable config.toml makes the whole [global] table
  # unreadable, which reads as an absent machine setting. The flag still decides,
  # in both directions, so a developer is never stuck without a way to ask.

  Scenario: an unreadable config.toml leaves the behavior off, and the flag still turns it on
    Given a clean niwa environment
    And the scenario is skipped when tests run as root
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config is not readable
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch unreadable-off --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    When I run "niwa dispatch unreadable-on --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output has exactly 1 line containing "without asking (source: --accept-session-messages); see https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"

  Scenario Outline: a config.toml niwa cannot use leaves the behavior off, and the flag still turns it on
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And the niwa machine config global table contains:
      """
      <line>
      """
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch broken-off --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has no crossSessionInbound
    And the error output has exactly 0 lines containing "accepts messages from other sessions without asking"
    When I run "niwa dispatch broken-on --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output has exactly 1 line containing "without asking (source: --accept-session-messages); see https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"

    Examples:
      | line                                        |
      | accept_session_messages_on_dispatch = "yes" |
      | this is not valid toml                      |

  # --- The developer's own settings ---
  #
  # The decision travels as a launch flag exactly so that one dispatch cannot
  # change what every other Claude Code session on the machine does. niwa neither
  # writes the developer's user settings nor needs to read them.

  Scenario: a dispatch leaves the developer's own Claude settings alone
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And a personal claude settings file exists with body:
      """
      {"theme": "dark"}
      """
    Given a fake claude for dispatch with session "b1000000-0000-4000-8000-000000000001"
    When I run "niwa dispatch personal-settings --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the personal claude settings file is unchanged

  Scenario: an unreadable personal settings file does not stop a dispatch
    Given a clean niwa environment
    And the scenario is skipped when tests run as root
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    And a personal claude settings file exists with body:
      """
      {"theme": "dark"}
      """
    And the personal claude settings file is not readable
    # The one-time explanation names ~/.claude/settings.json as the place to
    # change the developer's OWN sessions, so it is taken out of the way here:
    # this scenario is about niwa saying nothing about a file it could not read,
    # and the paragraph would answer for it.
    And the session-message notice has already been shown
    Given a fake claude for dispatch with session "b1000000-0000-4000-8000-000000000002"
    When I run "niwa dispatch unreadable-personal --accept-session-messages --detach" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has crossSessionInbound "accept"
    And the error output does not contain "settings.json"
