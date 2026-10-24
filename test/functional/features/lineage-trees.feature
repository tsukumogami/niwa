Feature: the lineage record holds whole dispatch trees after their instances are gone
  Dispatches keep their lineage entries in <workspace root>/.niwa-lineage/
  through every way niwa tears an instance down, so niwa lineage show can walk
  a tree of dispatches nearest first long after the instances are reaped. Each
  level says how niwa came by its parent links and the person it acts for.

  The fake claude stands in for every worker, and no workflow engine is on
  PATH, so nothing here launches a real agent or needs koto.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And no workflow engine is on PATH

  @critical
  Scenario: a three-level tree reads back nearest first after every instance is reaped
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the niwa machine config global table contains:
      """
      principal = "dana@example.com"
      """
    And a fake claude for dispatch that mints a new session per launch
    And the dispatching session is "50505050-0000-4000-8000-505050505050"
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "A"
    And the dispatching session is the session of "A"
    When I dispatch with "" from inside the instance of "A"
    Then the exit code is 0
    Given I keep the dispatch as "B"
    And the dispatching session is the session of "B"
    When I dispatch with "" from inside the instance of "B"
    Then the exit code is 0
    Given I keep the dispatch as "C"
    When every kept dispatch's session is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no kept dispatch instance remains
    When I run "niwa lineage show --session {B.session} --json" naming kept dispatches from the workspace root
    Then the exit code is 0
    And the lineage chain is:
      | dispatch | parent session                            | parent dispatch | principal                          |
      | B        | A observed                                | A observed      | dana@example.com inherited observed |
      | A        | 50505050-0000-4000-8000-505050505050 handed | none            | dana@example.com declared observed  |
    And the lineage chain stops at "root"
    When I run "niwa lineage show --session {C.session} --json" naming kept dispatches from the workspace root
    Then the exit code is 0
    And the lineage chain is:
      | dispatch | parent session                            | parent dispatch | principal                          |
      | C        | B observed                                | B observed      | dana@example.com inherited observed |
      | B        | A observed                                | A observed      | dana@example.com inherited observed |
      | A        | 50505050-0000-4000-8000-505050505050 handed | none            | dana@example.com declared observed  |
    And the lineage chain stops at "root"
    When I run "niwa lineage show --session {C.session}" naming kept dispatches from the workspace root
    Then the exit code is 0
    And the output contains "dispatch {C.dispatch}" naming kept dispatches
    And the output contains "parent dispatch: {B.dispatch} observed" naming kept dispatches
    And the output contains "parent session:  50505050-0000-4000-8000-505050505050 handed (not observed)" naming kept dispatches

  @critical
  Scenario: a worker a coordinator dispatched from the workspace root links to it as mapped
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the niwa machine config global table contains:
      """
      principal = "dana@example.com"
      """
    And a fake claude for dispatch that mints a new session per launch
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "K"
    And the dispatching session is the session of "K"
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "W"
    When every kept dispatch's session is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no kept dispatch instance remains
    When I run "niwa lineage show --session {W.session}" naming kept dispatches from the workspace root
    Then the exit code is 0
    And the output contains "dispatch {W.dispatch}" naming kept dispatches
    And the output contains "parent dispatch: {K.dispatch} mapped (not observed)" naming kept dispatches
    And the output contains "parent session:  {K.session} handed (not observed)" naming kept dispatches
    And the output contains "principal:       dana@example.com, source inherited, trust handed (not observed)" naming kept dispatches
    And the output contains "dispatch {K.dispatch}" naming kept dispatches
    When I run "niwa lineage show --session {W.session} --json" naming kept dispatches from the workspace root
    Then the exit code is 0
    And the lineage chain is:
      | dispatch | parent session | parent dispatch | principal                          |
      | W        | K handed       | K mapped        | dana@example.com inherited handed   |
      | K        | none           | none            | dana@example.com declared observed  |
    And the lineage chain stops at "root"

  Scenario: a session niwa has no mapping for, or a mapping with no dispatch id, is handed
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    And the dispatching session is "1e1e1e1e-0000-4000-8000-1e1e1e1e1e1e"
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "imported"
    When I run "niwa lineage show {imported.dispatch} --json" naming kept dispatches from the workspace root
    Then the lineage chain is:
      | dispatch | parent session                              | parent dispatch | principal          |
      | imported | 1e1e1e1e-0000-4000-8000-1e1e1e1e1e1e handed | none            | none none observed |
    Given the dispatching session is the session of "imported"
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "colliding"
    When I run "niwa lineage show {colliding.dispatch} --json" naming kept dispatches from the workspace root
    Then the lineage chain is:
      | dispatch  | parent session                              | parent dispatch | principal          |
      | colliding | imported handed                             | imported mapped | none inherited handed |
      | imported  | 1e1e1e1e-0000-4000-8000-1e1e1e1e1e1e handed | none            | none none observed |
    Given the mapping of "colliding" records no dispatch id
    And the dispatching session is the session of "colliding"
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "unlinked"
    When I run "niwa lineage show {unlinked.dispatch} --json" naming kept dispatches from the workspace root
    Then the lineage chain is:
      | dispatch | parent session   | parent dispatch | principal          |
      | unlinked | colliding handed | none            | none none observed |

  Scenario Outline: an entry survives <teardown>
    Given a config repo "ws" exists with authority fixture "<fixture>"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa dispatch teardown --detach" from the workspace root
    Then the exit code is 0
    And the dispatch's lineage entry records session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    Given I remember the dispatch's lineage entry
    When <step one>
    And <step two>
    Then the exit code is 0
    And the remembered lineage entry is unchanged

    Examples:
      | teardown                     | fixture       | step one                                                                     | step two                                                     |
      | a mapped reap                | none          | the dispatch session "dddddddd-dddd-4ddd-8ddd-dddddddddddd" is deleted from the Agent View | I run niwa reap from the workspace root                      |
      | a stored-mapping reap        | none          | the dispatch instance directory is removed by hand                           | I run niwa reap from the workspace root                      |
      | niwa destroy                 | none          | the dispatch session "dddddddd-dddd-4ddd-8ddd-dddddddddddd" has exited       | I destroy the dispatch instance from the workspace root with "" |
      | niwa destroy --keep-session  | none          | the dispatch session "dddddddd-dddd-4ddd-8ddd-dddddddddddd" has exited       | I destroy the dispatch instance from the workspace root with "--keep-session" |
      | niwa reset                   | none          | the dispatch session "dddddddd-dddd-4ddd-8ddd-dddddddddddd" has exited       | I reset the dispatch instance from the workspace root         |
      | a failing on-reap command    | on-reap-fails | the dispatch session "dddddddd-dddd-4ddd-8ddd-dddddddddddd" is deleted from the Agent View | I run niwa reap from the workspace root                      |
      | niwa apply                   | none          | I run "niwa apply" from the workspace root                                   | I run "niwa apply" from the workspace root                   |

  Scenario: an entry survives the backstop reap of an unmapped instance
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
    When I run "niwa dispatch backstop --detach" from the workspace root
    Then the exit code is 0
    Given I remember the dispatch's lineage entry
    When the dispatch-origin mapping is removed
    And the dispatch session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee" is deleted from the Agent View
    And the dispatch instance is aged past the backstop TTL
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And the remembered lineage entry is unchanged

  Scenario: an entry survives an on-reap command that fails until its mapping is dropped
    Given a config repo "ws" exists with authority fixture "on-reap-fails"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ffffffff-ffff-4fff-8fff-ffffffffffff"
    When I run "niwa dispatch failing-hook --detach" from the workspace root
    Then the exit code is 0
    Given I remember the dispatch's lineage entry
    When the dispatch session "ffffffff-ffff-4fff-8fff-ffffffffffff" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the session mapping exists for session "ffffffff-ffff-4fff-8fff-ffffffffffff"
    When I run niwa reap 9 more times from the workspace root
    Then the session mapping does not exist for session "ffffffff-ffff-4fff-8fff-ffffffffffff"
    And the remembered lineage entry is unchanged

  Scenario: a dispatch's entry survives another dispatch's refresh held between copy and swap
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When a dispatch from the workspace root holds its refresh between copy and swap
    And I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "second"
    And I remember the lineage entry of "second"
    # The held dispatch's own outcome isn't asserted: the second dispatch's
    # refresh used the same staging directory, so the held swap can fail and
    # roll back. Either way the second dispatch's entry must stay.
    When the held dispatch is released and ends
    Then the remembered lineage entry of "second" is unchanged
    When I run "niwa lineage list" from the workspace root
    Then the exit code is 0
    And the output contains "{second.dispatch}" naming kept dispatches

  Scenario: niwa destroy --workspace removes the lineage store
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "gone"
    And I remember the lineage entry of "gone"
    When every kept dispatch's session is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    When I run "niwa destroy --workspace --confirm ws" from the workspace root
    Then the exit code is 0
    And the lineage store is gone

  Scenario: dispatch, show and list work with every proxy pointed at a closed port
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the proxy variables point at a closed local port
    And a fake claude for dispatch that mints a new session per launch
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch as "offline"
    When I run "niwa lineage show {offline.dispatch}" naming kept dispatches from the workspace root
    Then the exit code is 0
    And the output contains "dispatch {offline.dispatch}" naming kept dispatches
    When I run "niwa lineage list" from the workspace root
    Then the exit code is 0
    And the output contains "{offline.dispatch}" naming kept dispatches

  Scenario: a workspace that never dispatched has no lineage store
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa apply" from the workspace root
    Then the exit code is 0
    And the workspace root has no lineage store
    When I run "niwa lineage list" from the workspace root
    Then the workspace root has no lineage store
