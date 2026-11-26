Feature: niwa dispatch: a dispatch profile shapes one launch
  `niwa dispatch --profile <name>` looks the name up in
  [session.dispatch_profiles] and adds the profile's drops to that one
  launch's settings document: a permissions deny list of the dropped tools,
  disableBundledSkills, and ENABLE_CLAUDEAI_MCP_SERVERS=false in the env
  block, with NIWA_DISPATCH_PROFILE marking the launch. A profile's
  auto_compact_window becomes CLAUDE_CODE_AUTO_COMPACT_WINDOW in the settings
  document's env block and nowhere else, and every launch drops a value it
  inherits. [session] default_dispatch_profile names the profile a dispatch
  takes when it names none. A dispatch that resolves to no profile, from the
  same workspace, launches the document it always did.

  The fake claude records the launch argv, and the steps read the settings
  document out of it. It also records whether its own process environment
  held CLAUDE_CODE_AUTO_COMPACT_WINDOW.

  @dispatch-profile
  Scenario: a profiled dispatch carries the profile and an unprofiled one doesn't
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [session.dispatch_profiles.coordinator]
      drop_tools = ["Artifact", "Workflow", "SendFeedback", "ReportFindings", "EnterWorktree", "AskUserQuestion", "Skill"]
      drop_bundled_skills = true
      drop_account_connectors = true
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch hello-task --detach --name lead --profile coordinator" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has exactly the keys "disableBundledSkills,env,permissions"
    And the launched claude settings document denies exactly "Artifact,Workflow,SendFeedback,ReportFindings,EnterWorktree,AskUserQuestion,Skill"
    And the launched claude settings document has disableBundledSkills true
    And the launched claude settings document sets env "ENABLE_CLAUDEAI_MCP_SERVERS" to "false"
    And the launched claude settings document sets env "NIWA_DISPATCH_PROFILE" to "coordinator"
    When I run "niwa dispatch hello-task --detach --name plain" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has exactly the keys "env"
    And the launched claude settings document env has no "ENABLE_CLAUDEAI_MCP_SERVERS"
    And the launched claude settings document env has no "NIWA_DISPATCH_PROFILE"
    And the launched claude was not invoked with "disableBundledSkills"
    And the launched claude was not invoked with "permissions"

  @dispatch-profile
  Scenario: a profile's compaction window rides the settings document only, and an exported one never reaches a worker
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [session.dispatch_profiles.worker]
      auto_compact_window = 400000

      [session.dispatch_profiles.coordinator]
      drop_bundled_skills = true
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch hello-task --detach --name windowed --profile worker" from the workspace root
    Then the exit code is 0
    And the launched claude settings document has exactly the keys "env"
    And the launched claude settings document sets env "CLAUDE_CODE_AUTO_COMPACT_WINDOW" to "400000"
    And the launched claude settings document sets env "NIWA_DISPATCH_PROFILE" to "worker"
    And the launched claude process environment has no compaction window
    When I run "niwa dispatch hello-task --detach --name exported --profile worker" from the workspace root with env "CLAUDE_CODE_AUTO_COMPACT_WINDOW" set to "900000"
    Then the exit code is 0
    And the launched claude settings document sets env "CLAUDE_CODE_AUTO_COMPACT_WINDOW" to "400000"
    And the launched claude process environment has no compaction window
    When I run "niwa dispatch hello-task --detach --name unwindowed --profile coordinator" from the workspace root with env "CLAUDE_CODE_AUTO_COMPACT_WINDOW" set to "900000"
    Then the exit code is 0
    And the launched claude settings document env has no "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
    And the launched claude process environment has no compaction window
    When I run "niwa dispatch hello-task --detach --name plain" from the workspace root with env "CLAUDE_CODE_AUTO_COMPACT_WINDOW" set to "900000"
    Then the exit code is 0
    And the launched claude settings document has exactly the keys "env"
    And the launched claude settings document env has no "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
    And the launched claude settings document env has no "NIWA_DISPATCH_PROFILE"
    And the launched claude process environment has no compaction window

  @dispatch-profile
  Scenario: the default profile applies to a dispatch that names none, and --profile wins over it
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [session]
      default_dispatch_profile = "worker"

      [session.dispatch_profiles.worker]
      auto_compact_window = 400000

      [session.dispatch_profiles.coordinator]
      drop_bundled_skills = true
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch hello-task --detach --name defaulted" from the workspace root
    Then the exit code is 0
    And the error output contains "launched with dispatch profile"
    And the launched claude settings document sets env "CLAUDE_CODE_AUTO_COMPACT_WINDOW" to "400000"
    And the launched claude settings document sets env "NIWA_DISPATCH_PROFILE" to "worker"
    And the launched claude process environment has no compaction window
    When I run "niwa dispatch hello-task --detach --name lead --profile coordinator" from the workspace root
    Then the exit code is 0
    And the launched claude settings document sets env "NIWA_DISPATCH_PROFILE" to "coordinator"
    And the launched claude settings document env has no "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
    When I run "niwa list" from the workspace root
    Then the exit code is 0
    And the output contains "dispatch profile: worker"
    And the output contains "dispatch profile: coordinator"

  @dispatch-profile
  Scenario: --no-profile launches with no profile under a default, and refuses beside a named profile
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [session]
      default_dispatch_profile = "worker"

      [session.dispatch_profiles.worker]
      auto_compact_window = 400000
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that mints a new session per launch
    When I run "niwa dispatch hello-task --detach --name bare --no-profile" from the workspace root with env "CLAUDE_CODE_AUTO_COMPACT_WINDOW" set to "900000"
    Then the exit code is 0
    And the error output contains "--no-profile: skipped the default dispatch profile"
    And the error output does not contain "launched with dispatch profile"
    And the launched claude settings document has exactly the keys "env"
    And the launched claude settings document env has no "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
    And the launched claude settings document env has no "NIWA_DISPATCH_PROFILE"
    And the launched claude process environment has no compaction window
    When I run "niwa list" from the workspace root
    Then the exit code is 0
    And the output does not contain "dispatch profile:"
    When I run "niwa dispatch hello-task --detach --name blank --no-profile --profile=" from the workspace root
    Then the exit code is 0
    And the launched claude settings document env has no "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
    And the launched claude settings document env has no "NIWA_DISPATCH_PROFILE"
    When I run "niwa dispatch hello-task --detach --name both --no-profile --profile worker" from the workspace root
    Then the exit code is not 0
    And the error output contains "--no-profile and --profile"
    And the error output contains "can't be used together"
