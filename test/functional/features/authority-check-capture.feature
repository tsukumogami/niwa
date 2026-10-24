Feature: a dispatched worker is bound from its first command
  Dispatch writes a worker's session mapping only once it has captured the
  worker's session id, and the worker can run a command before that.
  `niwa authority check` still binds it: through the dispatch's lineage entry
  once dispatch has recorded the session id there, and before that through
  the agent's own session records, while dispatch says a capture is pending.
  A session started by hand after the capture ended, or beside the worker
  while it runs, is never bound that way.

  Dispatch is held at one of two test seams: after the launch and before
  capture, or after the lineage entry names the session and before the
  mapping. The fake claude stands in for every worker, and no workflow engine
  is on PATH.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And no workflow engine is on PATH
    And a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0

  @critical
  Scenario: a worker checking before its session is captured is bound through its own session record
    Given a fake claude for dispatch that checks its authority once it starts, with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root, held before capture
    Then the worker's own check at launch is bound through "records" holding "merge-pr"
    And the dispatch instance holds a capture-pending file
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check reads "bound" with binding "records"
    And the check's grant holds "merge-pr"
    When the held dispatch is released and ends
    Then the exit code is 0
    And the dispatch instance holds no capture-pending file
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check reads "bound" with binding "mapping"

  Scenario: a worker checking after its session id is recorded and before its mapping is bound through the lineage entry
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root, held before the mapping
    And I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check reads "bound" with binding "lineage"
    And the check's grant holds "merge-pr"
    When the held dispatch is released and ends
    Then the exit code is 0
    And the dispatch instance holds no capture-pending file

  Scenario Outline: a worker whose record is lost <held> reads record-unusable, not not-dispatched
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root, held <held>
    And <loss>
    And I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check reads "record-unusable" with reason "<reason>"
    When the held dispatch is released and ends

    Examples:
      | held               | loss                                                                                      | reason           |
      | before capture     | the dispatch instance's authority record is deleted                                       | record-missing   |
      | before capture     | the dispatch instance's authority record is replaced with '{"schema": "niwa.authority.re' | record-malformed |
      | before the mapping | the dispatch instance's authority record is deleted                                       | record-missing   |
      | before the mapping | the dispatch instance's authority record is replaced with '{"schema": "niwa.authority.re' | record-malformed |

  Scenario: a second session claiming the instance while capture is pending binds neither
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root, held before capture
    And a session record for "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" claims the dispatch instance
    And I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check reads "not-dispatched" with reason "no-mapping"
    When I run the check "niwa authority check --json --session bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb -C {instance} --point merge-pr"
    Then the check reads "not-dispatched" with reason "no-mapping"
    When the held dispatch is released and ends

  Scenario: a session started after a dispatch was killed during capture is not bound
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root, held before capture
    And the held dispatch is killed with SIGTERM
    Then the dispatch instance holds no capture-pending file
    Given a session record for "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" claims the dispatch instance
    When I run the check "niwa authority check --json --session bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb -C {instance} --point merge-pr"
    Then the check reads "not-dispatched" with reason "no-mapping"

  Scenario: a session started after a capture timed out is not bound
    Given a fake codex for dispatch that records no session
    When I run "niwa dispatch hello-task --harness codex --grant merge-pr" from the workspace root
    Then the exit code is 0
    And the error output contains "no session record was found"
    And the dispatch instance holds no capture-pending file
    Given a session record for "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" claims the dispatch instance
    When I run the check "niwa authority check --json --session bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb -C {instance} --point merge-pr"
    Then the check reads "not-dispatched" with reason "no-mapping"

  Scenario: a capture-pending file naming a process started after its launch binds nothing
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root, held before capture
    And the capture-pending file names a process started after its launch
    And I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check reads "not-dispatched" with reason "no-mapping"
    When the held dispatch is released and ends

  Scenario: niwa's SessionStart hook leaves a dispatch's mapping in place
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given a background job state exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And I remember the worker's session mapping and the workspace's instances
    When I pipe a SessionStart hook for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    Then the exit code is 0
    And the worker's session mapping is byte-identical and no instance was added

  Scenario: a bound worker keeps its grant across apply, a policy change, a resume and a dispatch of its own
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {first-instance} --point merge-pr"
    Then the check reads "bound" with binding "mapping"
    And the check's grant holds "merge-pr"
    When I run "niwa apply ws"
    Then the exit code is 0
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {first-instance} --point merge-pr"
    Then the check reads "bound" with binding "mapping"
    And the check's grant holds "merge-pr"
    Given the workspace root's config becomes "narrow-ceiling"
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {first-instance} --point merge-pr"
    Then the check reads "bound" with binding "mapping"
    And the check's grant holds "merge-pr"
    When the worker is resumed with "claude attach aaaaaaaa" under session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {first-instance} --point merge-pr"
    Then the check reads "bound" with binding "mapping"
    And the check's grant holds "merge-pr"
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I dispatch with "--grant merge-pr" from inside the dispatch instance
    Then the exit code is 0
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {first-instance} --point merge-pr"
    Then the check reads "bound" with binding "mapping"
    And the check's grant holds "merge-pr"
