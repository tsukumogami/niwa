Feature: niwa authority check tells a dispatched session from any other session in its instance
  A gate knows two things about the session it guards: the session id in its
  payload and its own path, which names the instance. `niwa authority check`
  takes those two and says whether that session is the one the instance's
  dispatch launched (bound), is that session but its record can't be used
  (record-unusable), or is any other session (not-dispatched), with the grant
  and each decision point's answer for a bound session.

  The fake claude stands in for every worker, and no workflow engine is on
  PATH. Each check runs from a directory in no instance, so the answer comes
  from -C alone.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And no workflow engine is on PATH

  @critical
  Scenario: a dispatch's own session is bound with its grant, and another session is not
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check document is:
      """
      {
        "schema": "niwa.authority.check/v1",
        "state": "bound",
        "binding": "mapping",
        "dispatch_id": "<dispatch-id>",
        "grant": [
          {
            "authority": "merge-pr",
            "sources": [
              "dispatch"
            ]
          }
        ],
        "level": "autonomous",
        "level_source": "workspace",
        "grant_ceiling_source": "workspace",
        "points": [
          {
            "point": "merge-pr",
            "decider": "session",
            "author_exclusion": "off"
          }
        ]
      }
      """
    When I run the check "niwa authority check --json --session bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb -C {instance} --point merge-pr"
    Then the check document is:
      """
      {
        "schema": "niwa.authority.check/v1",
        "state": "not-dispatched",
        "reason": "no-mapping",
        "grant": []
      }
      """

  Scenario: a mapping dispatch didn't write is not a binding
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the worker's session mapping is rewritten without its origin
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check document is:
      """
      {
        "schema": "niwa.authority.check/v1",
        "state": "not-dispatched",
        "reason": "hook-mapping",
        "grant": []
      }
      """

  Scenario: a dispatch mapping naming another instance is not a binding here
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch first-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    And a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I run "niwa dispatch second-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the worker's session mapping is rewritten to name the first dispatch instance
    When I run the check "niwa authority check --json --session bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb -C {instance} --point merge-pr"
    Then the check document is:
      """
      {
        "schema": "niwa.authority.check/v1",
        "state": "not-dispatched",
        "reason": "other-instance",
        "grant": []
      }
      """

  Scenario: a mapping naming another dispatch than the record makes the record unusable
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the worker's session mapping is rewritten to name dispatch "99999999-9999-4999-8999-999999999999"
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check document is:
      """
      {
        "schema": "niwa.authority.check/v1",
        "state": "record-unusable",
        "reason": "record-other-dispatch",
        "dispatch_id": "<dispatch-id>",
        "grant": []
      }
      """

  Scenario: a dispatched session whose record is deleted stays dispatched, with an unusable record
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the dispatch instance's authority record is deleted
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check document is:
      """
      {
        "schema": "niwa.authority.check/v1",
        "state": "record-unusable",
        "reason": "record-missing",
        "dispatch_id": "<dispatch-id>",
        "grant": []
      }
      """

  Scenario: a dispatched session whose record is not JSON stays dispatched, with an unusable record
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    Given the dispatch instance's authority record is replaced with '{"schema": "niwa.authority.record/v1", "record": tru'
    When I run the check "niwa authority check --json --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance} --point merge-pr"
    Then the check document is:
      """
      {
        "schema": "niwa.authority.check/v1",
        "state": "record-unusable",
        "reason": "record-malformed",
        "dispatch_id": "<dispatch-id>",
        "grant": []
      }
      """

  Scenario: a repo in the instance and a niwa worktree of it give the instance root's answer
    Given a staged file "README.md" with body:
      """
      app
      """
    And a source repo "app" exists with the staged files
    And a config repo "ws" exists with authority fixture "r8a-example" and repo "app"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    When I create a worktree for repo "app" with purpose "authority-check" in the dispatch instance
    Then the check prints the same for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" from "tools/app" in the dispatch instance, from the worktree and from the instance root
    And the check prints the same for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" from "tools/app" in the dispatch instance, from the worktree and from the instance root

  Scenario Outline: a check that can't anchor or can't trust its session id prints nothing and exits 1
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    When I run the check "niwa authority check --json --session <session> -C <dir> --point merge-pr"
    Then the exit code is 1
    And the output is empty

    Examples:
      | session                              | dir                        |
      | aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa | relative/dir               |
      | aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa | {workspace-root}/no-such   |
      | aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa | {outside}                  |
      | AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA | {instance}                 |
      | not-a-session-id                     | {instance}                 |

  Scenario: a session reads its own binding and grant as plain text
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    When I run the check "niwa authority check --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance}"
    Then the exit code is 0
    And the output contains "session: bound to dispatch "
    And the output contains " (mapping)"
    And the output contains "grant: merge-pr (dispatch)"

  Scenario: a session dispatched with no grant reads grant none
    Given a config repo "ws" exists with authority fixture "r8a-example"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    When I run the check "niwa authority check --session aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa -C {instance}"
    Then the exit code is 0
    And the output contains " (mapping)"
    And the output contains "grant: none"
