Feature: the authority guide's reference checks inside a stub gate
  docs/guides/authority-grants.md ships the checks a gate uses to read a
  dispatch's grant: niwa_holds, niwa_grant_decides and niwa_settles. These
  scenarios read them from the guide and source them into a stub gate. The
  stub's own rule, today's behaviour, allows an instance on its exemption list
  and denies the rest. When niwa_grant_decides returns 0 the grant decides
  instead: allow only when the authority is held and decide prints
  decider=session, deny otherwise, exemption list or not. Every lookup that
  fails must leave today's verdict in place.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And the stub gate sources the reference checks from the authority guide

  @critical
  Scenario: a record holding merge-pr at autonomous decides, over the exemption list
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    And "niwa_holds merge-pr" returns 0
    And "niwa_holds close-issue" returns non-zero
    And "niwa_grant_decides" returns 0
    And "niwa_settles merge-own-pr" returns 0
    And the stub gate allows "merge-pr" at point "merge-own-pr" for a non-exempt instance
    And the stub gate denies "close-issue" at point "close-own-issue" for a non-exempt instance
    And the stub gate denies "close-issue" at point "close-own-issue" for an exempt instance

  Scenario: an authority this niwa doesn't know is never held
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the dispatch instance's authority record is replaced with '{"schema": "niwa.authority.record/v1", "record": true, "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7", "parent_session_id": "", "parent_dispatch_id": "", "parent_dispatch_source": "none", "level": "autonomous", "level_source": "workspace", "level_ceiling": "autonomous", "level_ceiling_source": "workspace", "grant_ceiling": ["merge-pr", "close-issue", "cut-release"], "grant_ceiling_source": "workspace", "roles": [], "grant": [{"authority": "merge-pr", "sources": ["dispatch"]}, {"authority": "teardown", "sources": ["dispatch"]}], "unknown_grant": [], "supervisor": "someone"}'
    Then "niwa_holds merge-pr" returns 0
    And "niwa_holds teardown" returns non-zero
    And "niwa_grant_decides" returns 0

  Scenario: a workspace that never declared a ceiling keeps today's verdict
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And the dispatch instance's authority record holds '"grant_ceiling_source": "none"'
    And "niwa_grant_decides" returns non-zero
    And the stub gate allows "merge-pr" at point "merge-own-pr" for an exempt instance
    And the stub gate denies "merge-pr" at point "merge-own-pr" for a non-exempt instance

  Scenario: a declared-empty ceiling decides, and denies even an exempt instance
    Given a config repo "ws" exists with authority fixture "empty-ceiling"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And the dispatch instance's authority record holds '"grant_ceiling_source": "workspace"'
    And "niwa_grant_decides" returns 0
    And "niwa_holds merge-pr" returns non-zero
    And the stub gate denies "merge-pr" at point "merge-own-pr" for an exempt instance

  Scenario: a record in a later schema version holds nothing and denies
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the dispatch instance's authority record is replaced with '{"schema": "niwa.authority.record/v2", "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7", "grant": [{"authority": "merge-pr", "sources": ["dispatch"]}], "level": "unbounded"}'
    Then "niwa_grant_decides" returns 0
    And "niwa_holds merge-pr" returns non-zero
    And the stub gate denies "merge-pr" at point "merge-own-pr" for an exempt instance

  Scenario: at strict the settle check turns the merge down, held or exempt
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --level strict --grant merge-pr" from the workspace root
    Then the exit code is 0
    And "niwa_holds merge-pr" returns 0
    And "niwa_settles merge-own-pr" returns non-zero
    And the stub gate denies "merge-pr" at point "merge-own-pr" for an exempt instance

  Scenario: the no-record answer keeps today's verdict, and its decide does not settle
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the gate looks up the workspace root
    Then the gate's "niwa authority decide merge-own-pr" prints "decider=session" and "record=none"
    And "niwa_settles merge-own-pr" returns non-zero
    And "niwa_grant_decides" returns non-zero
    And "niwa_holds merge-pr" returns non-zero
    And the stub gate allows "merge-pr" at point "merge-own-pr" for an exempt instance
    And the stub gate denies "merge-pr" at point "merge-own-pr" for a non-exempt instance

  Scenario Outline: a lookup through <niwa> niwa keeps today's verdict
    Given the gate's niwa is "<niwa>"
    Then "niwa_grant_decides" returns non-zero
    And "niwa_holds merge-pr" returns non-zero
    And "niwa_settles merge-own-pr" returns <settles>
    And the stub gate allows "merge-pr" at point "merge-own-pr" for an exempt instance
    And the stub gate denies "merge-pr" at point "merge-own-pr" for a non-exempt instance

    Examples:
      | niwa           | settles  |
      | missing        | non-zero |
      | old-help       | non-zero |
      | unknown-schema | 0        |
      | malformed      | 0        |

  # The "by timeout" row needs a timeout binary on the host and skips without
  # one (macOS ships none); Linux CI runs both rows.
  Scenario Outline: a lookup that hangs is cut off at the gate's bound <how>
    Given the gate's niwa is "hang"
    And the gate has <timeout>
    Then "niwa_grant_decides" returns non-zero
    And the last gate run finished within the gate's bound
    And "niwa_holds merge-pr" returns non-zero
    And the last gate run finished within the gate's bound
    And the stub gate allows "merge-pr" at point "merge-own-pr" for an exempt instance
    And the last gate run finished within the gate's bound
    And the stub gate denies "merge-pr" at point "merge-own-pr" for a non-exempt instance
    And the last gate run finished within the gate's bound

    Examples:
      | how                   | timeout             |
      | by timeout            | a timeout command   |
      | by a background job   | no timeout command  |
