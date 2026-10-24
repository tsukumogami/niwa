Feature: niwa dispatch: resolve, refuse and record a dispatch's authority
  A dispatch resolves its authority level and named grant from the workspace
  config, the overlay, the machine config and its own --level, --grant and
  --role flags before anything is created. A request the policy doesn't allow
  is refused there, before the prompt, the sweep or provisioning. A dispatch
  that passes prints one line saying what it resolved, checks the policy again
  against what provisioning built the instance from, and writes the record
  into the instance before the worker starts.

  The fake claude records its launch argv and OTEL_RESOURCE_ATTRIBUTES in
  HOME, so a missing launch record means nothing launched.

  Background:
    Given a clean niwa environment
    And a local git server is set up

  @critical
  Scenario: a granted dispatch records its authority before the worker starts
    Given a config repo "ws" exists with authority fixture "r8a-reviewer"
    And an overlay repo "ws-overlay" exists with body:
      """
      [authority]
      ceiling = ["merge-pr", "close-issue"]
      """
    When I run niwa init from config repo "ws" with overlay "ws-overlay"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And the dispatching session is "caller-session"
    When I run "niwa dispatch hello-task --detach --grant close-issue --role reviewer" from the workspace root
    Then the exit code is 0
    And the error output contains "niwa dispatch: authority level supervised from workspace; grant merge-pr (role reviewer), close-issue (dispatch)"
    And the dispatch instance's authority record is private to its owner
    And the dispatch instance's authority record is:
      """
      {
        "schema": "niwa.authority.record/v1",
        "record": true,
        "dispatch_id": "<dispatch-id>",
        "parent_session_id": "caller-session",
        "parent_dispatch_id": "",
        "parent_dispatch_source": "none",
        "parent_session_source": "handed",
        "principal": {
          "name": "",
          "source": "none",
          "trust": "observed"
        },
        "level": "supervised",
        "level_source": "workspace",
        "level_ceiling": "supervised",
        "level_ceiling_source": "workspace",
        "grant_ceiling": [
          "merge-pr",
          "close-issue"
        ],
        "grant_ceiling_source": "overlay",
        "roles": [
          "reviewer"
        ],
        "grant": [
          {
            "authority": "merge-pr",
            "sources": [
              "role:reviewer"
            ]
          },
          {
            "authority": "close-issue",
            "sources": [
              "dispatch"
            ]
          }
        ],
        "unknown_grant": []
      }
      """
    And the worker's resource attributes hold "niwa.authority.level=supervised,niwa.authority.grant=merge-pr/close-issue"

  @critical
  Scenario: a dispatch with no authority settings records the built-in level and no grant
    Given a config repo "ws" exists with authority fixture "none"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And the error output contains "niwa dispatch: authority level autonomous from built-in; grant none"
    And the dispatch instance's authority record holds '"grant_ceiling_source": "none"'
    And the worker's resource attributes hold "niwa.authority.level=autonomous,niwa.authority.grant=none"

  Scenario Outline: a dispatch refused at authority resolution leaves nothing behind
    Given a config repo "ws" exists with authority fixture "<fixture>"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch warmup --detach" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    And the dispatch session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" is deleted from the Agent View
    And the niwa machine config global table holds '<machine>'
    And the workspace root's config becomes "<root config>"
    And I remember the workspace before the next dispatch
    When I dispatch with "<flags>" from the workspace root under a pty
    Then the dispatch was refused at authority resolution with '<message>'

    Examples:
      | fixture        | machine                          | root config   | flags                                | message                                                                                                                                                  |
      | strict         | -                                | -             | --level supervised                   | niwa: error: --level supervised is wider than the authority level ceiling strict from workspace                                                         |
      | r8a            | authority_level = "strict"       | -             | --level supervised                   | niwa: error: --level supervised is wider than the authority level ceiling strict from machine                                                           |
      | narrow-ceiling | -                                | -             | --grant close-issue                  | niwa: error: authority close-issue (requested by --grant) is outside the grant ceiling: the grant ceiling is merge-pr from workspace                    |
      | narrow-ceiling | -                                | -             | --role coordinator                   | niwa: error: authority cut-release (requested by role coordinator) is outside the grant ceiling: the grant ceiling is merge-pr from workspace           |
      | none           | -                                | -             | --grant merge-pr                     | the grant ceiling is empty because no layer declares one                                                                           |
      | r8a            | -                                | -             | --level=                             | niwa: error: --level is empty; accepted values are autonomous, supervised, strict                                                                      |
      | r8a            | -                                | -             | --grant teardown                     | niwa: error: invalid --grant "teardown"; accepted values are merge-pr, close-issue, cut-release                                                         |
      | r8a            | -                                | -             | --role reviewer                      | niwa: error: invalid --role "reviewer"; accepted values are coordinator                                                                                 |
      | r8a            | -                                | -             | --grant merge-pr,                    | niwa: error: --grant: empty entry in "merge-pr,"                                                                                                        |
      | r8a            | -                                | -             | --role ,coordinator                  | niwa: error: --role: empty entry in ",coordinator"                                                                                                      |
      | r8a            | authority_level = "godmode"      | -             | --role coordinator                   | invalid [global] authority_level "godmode"; accepted values are autonomous, supervised, strict                                                          |
      | r8a            | authority_ceiling = ["Merge PR"] | -             | --role coordinator                   | invalid [global] authority_ceiling "Merge PR"                                                                                                           |
      | r8a            | -                                | not-toml      | --role coordinator                   | dispatch resolves its authority from that file                                                                                                          |
      | r8a            | -                                | invalid-level | --role coordinator                   | invalid [authority] level "bogus"; accepted values are autonomous, supervised, strict                                                                   |
      | many-roles     | -                                | -             | --role {all-roles}                   | over the 65536-byte limit                                                                                                                               |
      | r8a            | -                                | -             | --parent-from=                       | niwa: error: --parent-from is empty                                                                                                                     |
      | r8a            | -                                | -             | --parent-from relative/dir           | niwa: error: --parent-from "relative/dir" is not an absolute path                                                                                      |
      | r8a            | -                                | -             | --parent-from /nonexistent/niwa-dir  | niwa: error: --parent-from "/nonexistent/niwa-dir" does not exist                                                                                      |
      | r8a            | -                                | -             | --parent-from {workspace-root}/.niwa | is not inside an instance of this workspace                                                                                                             |

  Scenario: a dispatch that passes authority resolution does run the sweep
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch warmup --detach" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    And the dispatch session for the first dispatch is deleted from the Agent View
    When I run "niwa dispatch hello-task --detach --role coordinator" from the workspace root
    Then the exit code is 0
    And the first dispatch instance no longer exists

  Scenario: a recorded overlay whose clone is missing refuses before anything is created
    Given a config repo "ws" exists with authority fixture "r8a"
    And an overlay repo "ws-overlay" exists with body:
      """
      [authority]
      ceiling = ["merge-pr"]
      """
    When I run niwa init from config repo "ws" with overlay "ws-overlay"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And the local clone of overlay "ws-overlay" is removed
    And I remember the workspace before the next dispatch
    When I dispatch with "--role coordinator" from the workspace root under a pty
    Then the dispatch was refused at authority resolution with 'run niwa apply'

  Scenario: a malformed record in the dispatching instance refuses the dispatch
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch first --detach" from the workspace root
    Then the exit code is 0
    Given the dispatch instance's authority record is replaced with '{"schema": "niwa.authority.record/v1", "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7"}'
    And I remember the workspace before the next dispatch
    When I dispatch with "" from inside the dispatch instance
    Then the dispatch was refused at authority resolution with 'authority record is malformed; refusing to dispatch rather than record no parent'

  Scenario: the parent link follows the working directory or --parent-from, never a disagreement
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch coordinator --detach --role coordinator" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    When I dispatch with "--parent-from {first-instance}" from inside the first dispatch instance
    Then the exit code is 0
    And the dispatch instance's authority record names the first dispatch as its parent from "anchor"
    When I dispatch with "" from inside the first dispatch instance
    Then the exit code is 0
    And the dispatch instance's authority record names the first dispatch as its parent from "cwd"
    When I dispatch with "--parent-from {first-instance}" from the workspace root
    Then the exit code is 0
    And the dispatch instance's authority record names the first dispatch as its parent from "anchor"
    When I run "niwa dispatch from-root --detach" from the workspace root
    Then the exit code is 0
    And the dispatch instance's authority record holds '"parent_dispatch_source": "none"'
    Given I remember the workspace before the next dispatch
    When I dispatch with "--parent-from {dispatch-instance}" from inside the first dispatch instance
    Then the dispatch was refused at authority resolution with 'but this dispatch runs inside instance'

  Scenario: a convention overlay that provisioning brings in and that drops a requested authority refuses after the line
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given an overlay repo "ws-overlay" exists with body:
      """
      [authority]
      ceiling = ["merge-pr", "close-issue"]
      """
    And a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant cut-release" from the workspace root
    Then the dispatch was refused after provisioning with 'the overlay [authority] ceiling changed'

  Scenario: a convention overlay that drops only an unrequested authority is recorded
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given an overlay repo "ws-overlay" exists with body:
      """
      [authority]
      ceiling = ["merge-pr", "close-issue"]
      """
    And a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --grant merge-pr" from the workspace root
    Then the exit code is 0
    And the error output contains "niwa dispatch: authority level supervised from workspace; grant merge-pr (dispatch)"
    And the dispatch instance's authority record holds '"grant_ceiling_source": "overlay"'
    And the dispatch instance's authority record holds:
      """
        "grant_ceiling": [
          "merge-pr",
          "close-issue"
        ],
      """

  Scenario: an overlay at the same level keeps the level source the line printed
    Given a config repo "ws" exists with authority fixture "strict"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given an overlay repo "ws-overlay" exists with body:
      """
      [authority]
      level = "strict"
      """
    And a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And the error output contains "niwa dispatch: authority level strict from workspace; grant none"
    And the dispatch instance's authority record holds '"level_source": "workspace"'
    And the dispatch instance's authority record holds '"level_ceiling_source": "overlay"'

  Scenario Outline: a base config that provisioning refreshes to another level refuses after the line
    Given a config repo "ws" exists with authority fixture "<before>"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the config repo "ws" is re-pushed with the staged files and body:
      """
      [workspace]
      name = "ws"

      [authority]
      level = "<after>"
      """
    And a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the dispatch was refused after provisioning with 'changed): the level would now resolve to'

    Examples:
      | before | after      |
      | r8a    | strict     |
      | strict | autonomous |

  Scenario: the record survives apply, a machine config change and another dispatch, and goes with reset
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch hello-task --detach --role coordinator" from the workspace root
    Then the exit code is 0
    Given I remember the dispatch instance's authority record
    When I run "from-instance niwa apply" against the remembered instance
    Then the exit code is 0
    And the remembered authority record is byte-for-byte unchanged
    When I run "niwa config set authority-level strict"
    Then the exit code is 0
    And the remembered authority record is byte-for-byte unchanged
    When I run "niwa dispatch another --detach" from the workspace root
    Then the exit code is 0
    And the remembered authority record is byte-for-byte unchanged
    When I run "niwa reset {instance} --force" against the remembered instance
    Then the exit code is 0
    And the remembered instance has no authority record

  Scenario: a grant or a level changes nothing a dispatch writes into its instance
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch hello-task --detach --role coordinator" from the workspace root
    Then the exit code is 0
    Given I keep the dispatch instance as the first dispatch
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And the files niwa wrote into the first and the last dispatch instance match
    Given I keep the dispatch instance as the first dispatch
    When I run "niwa dispatch hello-task --detach --level strict" from the workspace root
    Then the exit code is 0
    And the files niwa wrote into the first and the last dispatch instance match

  Scenario: --level strict leaves bypass permission mode alone
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach --level strict --permission-mode bypassPermissions" from the workspace root
    Then the exit code is 0
    And the file "dispatch-launch-argv" in HOME contains "--permission-mode bypassPermissions"
    And the dispatch instance's authority record holds '"level": "strict"'

  Scenario: the dispatcher's own resource attributes survive beside the authority attributes
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    And the dispatching shell exports resource attributes "team=x"
    When I run "niwa dispatch hello-task --detach --grant merge-pr,close-issue,cut-release" from the workspace root
    Then the exit code is 0
    And the worker's resource attributes hold "team=x,niwa.authority.level=supervised,niwa.authority.grant=merge-pr/close-issue/cut-release"

  Scenario: a Codex dispatch records the same grant and carries no authority attributes
    Given a config repo "ws" exists with authority fixture "r8a"
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01d00000-0000-7000-8000-00000000a11c"
    When I run "niwa dispatch hello-task --detach --harness codex --grant merge-pr" from the workspace root
    Then the exit code is 0
    And the error output contains "niwa dispatch: authority level supervised from workspace; grant merge-pr (dispatch)"
    And the dispatch instance's authority record holds '"grant_ceiling_source": "workspace"'
    And the worker's launch carried no authority attributes
