Feature: niwa retask vets a copy's job directory before it removes the copy
  When a wake starts a copy of the worker's session instead of continuing it,
  retask removes the copy with claude rm. That claude rm waits on the same vet
  of the copy's job directory as every other: a link, pipe, socket or device
  under it, or a job directory that fails the root checks, leaves the copy in
  place with the path named and no claude rm runs.

  The scenario runs the copy-rule stub of Claude Code with every wake starting
  a copy, wrapped so the copy's job directory gets a link to a directory
  outside the scenario's sandbox as the copy starts.

  Scenario: niwa retask leaves a copy whose job directory holds a link to a directory outside
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given the copy-rule claude stub is on PATH
    And the fake claude is wrapped
    When I dispatch the retask worker with "niwa dispatch --detach TASK-ONE"
    Given an outside directory holds a marker
    And a copy the wake starts holds a link to the outside directory
    And the claude call log is cleared
    When I retask the worker by instance name with "FOLLOW-UP-ONE"
    Then the exit code is not 0
    And the error output contains "scratch/out"
    And the wrapped fake claude ran no claude rm
    And the outside directory is unchanged
    And no claude call carries an override flag or runs in the workspace
