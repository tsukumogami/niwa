Feature: niwa dispatch: a dispatch profile trims one launch
  `niwa dispatch --profile <name>` looks the name up in
  [session.dispatch_profiles] and adds the profile's drops to that one
  launch's settings document: a permissions deny list of the dropped tools,
  disableBundledSkills, and ENABLE_CLAUDEAI_MCP_SERVERS=false in the env
  block, with NIWA_DISPATCH_PROFILE marking the launch. A dispatch without the
  flag, from the same workspace, launches the document it always did.

  The fake claude records the launch argv, and the steps read the settings
  document out of it.

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
