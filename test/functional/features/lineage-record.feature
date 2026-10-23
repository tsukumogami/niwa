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
