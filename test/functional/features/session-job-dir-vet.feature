Feature: niwa vets a session's job directory before every claude rm
  claude rm takes a session id, and Claude Code deletes the session's job
  directory under ~/.claude/jobs by its own means, which niwa can't see. So
  right before each claude rm niwa vets that directory: the jobs root and the
  job directory must be real directories the person owns, with no group or
  other write permission, on one device, with no link on the way from the
  home directory, and nothing under the job directory may be anything but a
  regular file or a directory. A failure keeps the unit, or stops destroy,
  with the path named, and no claude rm runs. The reap and destroy vet every
  session they will remove before their first removal step.

  These scenarios wrap the dispatch steps' fake claude: the wrapper logs every
  call with the directory it ran from, and lists a session whose job entry has
  no state record, which liveness reads as gone. A link's target lives outside
  the scenario's sandbox and is compared file by file after the run.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1"
    And the fake claude is wrapped
    When I run "niwa dispatch vet-me --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    Given an outside directory holds a marker
    And the claude call log is cleared

  @critical
  Scenario: niwa reap keeps a clean unit whose listed session's job directory is a link
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the job directory of session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is a link to the outside directory
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "the job directory"
    And the error output contains "a symbolic link at"
    And the error output contains ".claude/jobs/f1f1f1f1"
    And the fake claude removed no session
    And the outside directory is unchanged

  Scenario: niwa reap keeps a clean unit whose jobs root is a link
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the jobs root is a link to the outside directory
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "a symbolic link at"
    And the error output contains ".claude/jobs"
    And the fake claude removed no session
    And the outside directory is unchanged

  Scenario: niwa reap keeps a clean unit whose config directory is a link
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the config directory is a link to the outside directory
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "a symbolic link at"
    And the error output contains ".claude"
    And the fake claude removed no session
    And the outside directory is unchanged

  Scenario: niwa reap keeps a clean unit whose CLAUDE_CONFIG_DIR is a link
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And CLAUDE_CONFIG_DIR is a link to the outside directory
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "a symbolic link at"
    And the error output contains "claude-config-link"
    And the fake claude removed no session
    And the outside directory is unchanged

  Scenario: niwa reap keeps a clean unit whose listed session's job directory is group writable
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the job directory of session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is group writable
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "jobs/f1f1f1f1 is writable by group or others"
    And the fake claude removed no session

  Scenario: niwa reap keeps a clean unit whose listed session's job directory is on another device
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the job directory of session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is on another device
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "another filesystem at"
    And the fake claude removed no session

  Scenario: niwa reap keeps a clean unit whose listed session's job directory another uid owns
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the job directory of session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is owned by another uid
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "is owned by uid"
    And the fake claude removed no session

  @critical
  Scenario: niwa reap removes a unit whose deleted session the listing doesn't hold, with no claude rm
    When the dispatch session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And the fake claude removed no session
    And no claude call carries an override flag or runs in the workspace

  Scenario: niwa reap keeps a unit whose listed session has no job directory, naming the missing path
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is listed with no job directory
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "jobs/f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1 is missing"
    And the fake claude removed no session

  @critical
  Scenario: niwa reap keeps a clean unit whose listed session holds a link to a directory outside
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" has an inner link to the outside directory
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "scratch/out"
    And the fake claude removed no session
    And the wrapped fake claude ran no claude rm
    And the outside directory is unchanged

  Scenario: niwa reap keeps a clean unit whose listed session holds a named pipe
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" has an inner named pipe
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "niwa: kept"
    And the error output contains "scratch/out"
    And the fake claude removed no session

  @critical
  Scenario: niwa reap removes a clean unit and its listed session once nothing in the job directory is a link
    Given the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" has an inner regular file
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And the fake claude removed session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1"
    And no claude call carries an override flag or runs in the workspace

  @critical
  Scenario: niwa destroy stops when a session it would remove holds a link to a directory outside
    Given the dispatch session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" has exited
    And the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" has an inner link to the outside directory
    When I destroy the dispatch instance from the workspace root with "--force"
    Then the exit code is not 0
    And the error output contains "nothing was destroyed"
    And the error output contains "scratch/out"
    And the dispatch instance still exists
    And the fake claude removed no session
    And the outside directory is unchanged
    And no claude call carries an override flag or runs in the workspace

  Scenario: niwa reap runs no hook the instance's Claude settings declare
    Given the dispatch instance declares a marker hook on every event
    And the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is gone but still listed
    And the session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" has an inner regular file
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And no hook marker was written

  Scenario: niwa destroy runs no hook the instance's Claude settings declare
    Given the dispatch instance declares a marker hook on every event
    And the dispatch session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" has exited
    When I destroy the dispatch instance from the workspace root with "--force"
    Then the exit code is 0
    And no dispatch instance remains
    And the fake claude removed session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1"
    And no hook marker was written
    And no claude call carries an override flag or runs in the workspace
