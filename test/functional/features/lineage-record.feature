Feature: niwa dispatch writes a lineage entry and niwa lineage show reads it
  Every dispatch that keeps its instance writes one lineage entry, keyed by
  its dispatch id, into <workspace root>/.niwa-lineage/, beside the authority
  record it writes into the instance. The store sits outside every instance
  and outside the workspace configuration directory, and niwa lineage show
  reads an entry back by its dispatch id.

  The fake claude stands in for the worker, so nothing here launches a real
  agent.

  Background:
    Given a clean niwa environment
    And a local git server is set up

  @critical
  Scenario: a dispatch's lineage entry reads back after an apply
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And the dispatch's lineage entry is private to its owner
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    When I run "niwa lineage show" for the dispatch with "--json"
    Then the exit code is 0
    And the lineage show output is the dispatch's entry with every link none
    When I run "niwa lineage show" for the dispatch with ""
    Then the exit code is 0
    And the output contains "parent dispatch: none (source none)"
    And the output contains "principal:       none (source none, trust observed)"
    And nothing under the workspace root's .niwa names the dispatch

  @critical
  Scenario: each dispatch records its parent links with how niwa came by them
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    And the dispatching session is "c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0"
    When I run "niwa dispatch coordinator --detach" from the workspace root
    Then the exit code is 0
    And the dispatch's lineage entry records parent session "handed" and parent dispatch "none"
    Given I keep the dispatch instance as the first dispatch
    And the dispatching session is the first dispatch's session
    When I dispatch with "" from inside the first dispatch instance
    Then the exit code is 0
    And the dispatch's lineage entry records parent session "observed" and parent dispatch "observed"
    When I dispatch with "" from the workspace root
    Then the exit code is 0
    And the dispatch's lineage entry records parent session "handed" and parent dispatch "mapped"

  @critical
  Scenario: a completed entry is unchanged by a reap, another dispatch and an apply
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I run "niwa dispatch reap-me --detach" from the workspace root
    Then the exit code is 0
    And the dispatch's lineage entry records session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    Given I remember the dispatch's lineage entry
    When the dispatch session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And the remembered lineage entry is unchanged
    Given a fake claude for dispatch with session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    When I run "niwa dispatch second --detach" from the workspace root
    Then the exit code is 0
    And the remembered lineage entry is unchanged
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the remembered lineage entry is unchanged
