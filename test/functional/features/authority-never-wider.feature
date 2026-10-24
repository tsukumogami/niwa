Feature: niwa dispatch never grants a child more than its dispatcher holds, and hooks can find niwa
  A dispatch resolves its child's grant against the workspace's ceiling and
  then against its dispatcher's own record: an authority the dispatcher
  doesn't hold is refused before any prompt or instance, naming the
  authority, how it was asked for and what the dispatcher holds. Inside an
  instance the dispatcher is that instance's dispatch; at the workspace root
  it is the calling session's dispatch, when its session mapping says
  dispatch wrote it. With no dispatcher to read, only the ceiling limits the
  child.

  Every Claude settings document niwa writes carries NIWA_BIN, the niwa that
  wrote it, so a hook can run niwa when the harness's PATH has none.

  The fake claude stands in for every worker, and no workflow engine is on
  PATH.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And no workflow engine is on PATH
    And a config repo "ws" exists with authority fixture "coordinator-lands"
    When I run niwa init from config repo "ws"
    Then the exit code is 0

  @critical
  Scenario: a dispatched session can pass on what it holds and nothing more
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given I remember the workspace before the next dispatch
    When I dispatch with "--grant merge-pr,close-issue" from inside the dispatch instance
    Then the dispatch was refused at authority resolution with "authority close-issue (requested by --grant) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch "
    Given I remember the workspace before the next dispatch
    When I dispatch with "--role coordinator" from inside the dispatch instance
    Then the dispatch was refused at authority resolution with "authority close-issue (requested by role coordinator) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch "
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I dispatch with "--grant merge-pr" from inside the dispatch instance
    Then the exit code is 0

  Scenario Outline: a dispatcher that <dispatcher> passes on nothing
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach <first grant>" from the workspace root
    Then the exit code is 0
    Given <change>
    And I remember the workspace before the next dispatch
    When I dispatch with "--grant merge-pr" from inside the dispatch instance
    Then the dispatch was refused at authority resolution with "authority merge-pr (requested by --grant) is wider than the dispatcher's grant: <holds>"
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I dispatch with "" from inside the dispatch instance
    Then the exit code is 0

    Examples:
      | dispatcher                         | first grant      | change                                                                                                                                  | holds                                                                      |
      | was granted nothing                |                  | I remember the workspace before the next dispatch                                                                                       | the dispatcher holds none (dispatch                                        |
      | has a later-version record         | --grant merge-pr | the dispatch instance's authority record is replaced with '{"schema": "niwa.authority.record/v2", "dispatch_id": "99999999-9999-4999-8999-999999999999"}' | the dispatcher holds none (dispatch 99999999-9999-4999-8999-999999999999) |
      | lost its record                    | --grant merge-pr | the dispatch instance's authority record is deleted                                                                                     | the dispatcher's record is missing or can't be used, so it holds none     |

  Scenario: at the workspace root a dispatched session's mapping names the dispatcher
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    And the dispatching session is the first dispatch's session
    And I remember the workspace before the next dispatch
    When I dispatch with "--grant close-issue" from the workspace root
    Then the dispatch was refused at authority resolution with "authority close-issue (requested by --grant) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch "
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I dispatch with "--grant merge-pr" from the workspace root
    Then the exit code is 0

  Scenario: at the workspace root a dispatched session whose record is gone passes on nothing
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    And the dispatch instance's authority record is deleted
    And the dispatching session is the first dispatch's session
    And I remember the workspace before the next dispatch
    When I dispatch with "--grant merge-pr" from the workspace root
    Then the dispatch was refused at authority resolution with "authority merge-pr (requested by --grant) is wider than the dispatcher's grant: the dispatcher's record is missing or can't be used, so it holds none"
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I dispatch with "" from the workspace root
    Then the exit code is 0

  Scenario: with no dispatcher record only the grant ceiling limits the child
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I dispatch with "--grant close-issue,cut-release" from the workspace root
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the newest instance is the dispatch instance
    And a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I dispatch with "--role coordinator --grant cut-release" from inside the dispatch instance
    Then the exit code is 0

  Scenario: a session started by hand in a granted instance passes on that instance's grant and no more
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the dispatching session is "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
    And I remember the workspace before the next dispatch
    When I dispatch with "--grant close-issue" from inside the dispatch instance
    Then the dispatch was refused at authority resolution with "authority close-issue (requested by --grant) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch "
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I dispatch with "--grant merge-pr" from inside the dispatch instance
    Then the exit code is 0

  Scenario: every settings document carries NIWA_BIN, and a hook with no niwa on PATH runs it
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And every Claude settings document niwa wrote carries NIWA_BIN naming the niwa that wrote it
    And a hook with no niwa on PATH runs "$NIWA_BIN" authority check for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" against the dispatch instance
