Feature: niwa trusts the instances it creates in Claude Code
  With [claude] trust_instances = true, create and apply mark the instance
  root as a trusted project in $HOME/.claude.json, keyed by the root with
  every symlink resolved, and record the grant in the instance state. Destroy
  removes what the record names, and turning the option off and applying
  withdraws the flag while keeping the entry.

  HOME is the scenario sandbox and CLAUDE_CONFIG_DIR is stripped, so the
  developer's own Claude config is never touched. Runs offline against the
  localGitServer bare-repo fake.

  Design: docs/designs/DESIGN-trust-instances.md

  @critical
  Scenario: create with trust_instances on trusts and records the instance root
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "trustws" exists with body:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = true
      """
    When I run niwa init from config repo "trustws"
    Then the exit code is 0
    When I run "niwa create trustws"
    Then the exit code is 0
    And the Claude config in HOME trusts instance "trustws"
    And instance "trustws" records Claude trust with mode "created"

  @critical
  Scenario: apply trusts an existing instance once the option is turned on
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "trustws" exists with body:
      """
      [workspace]
      name = "trustws"
      """
    When I run niwa init from config repo "trustws"
    Then the exit code is 0
    When I run "niwa create trustws"
    Then the exit code is 0
    And the Claude config in HOME has no entry for instance "trustws"
    And instance "trustws" records no Claude trust
    When the config repo "trustws" is force-pushed to:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = true
      """
    And I run "niwa apply trustws"
    Then the exit code is 0
    And the Claude config in HOME trusts instance "trustws"
    And instance "trustws" records Claude trust with mode "created"

  @critical
  Scenario: destroy removes the entry niwa created and leaves a hand-made one alone
    Given a clean niwa environment
    And a local git server is set up
    And the Claude config in HOME has a trusted entry for "/elsewhere/hand-made"
    And a config repo "trustws" exists with body:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = true
      """
    When I run niwa init from config repo "trustws"
    Then the exit code is 0
    When I run "niwa create trustws"
    Then the exit code is 0
    And the Claude config in HOME trusts instance "trustws"
    When I run "niwa destroy trustws" from workspace root
    Then the exit code is 0
    And the instance "trustws" does not exist
    And the Claude config in HOME has no entry for instance "trustws"
    And the Claude config in HOME still has a trusted entry for "/elsewhere/hand-made"

  @critical
  Scenario: turning the option off and applying withdraws the flag
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "trustws" exists with body:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = true
      """
    When I run niwa init from config repo "trustws"
    Then the exit code is 0
    When I run "niwa create trustws"
    Then the exit code is 0
    And the Claude config in HOME trusts instance "trustws"
    When the config repo "trustws" is force-pushed to:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = false
      """
    And I run "niwa apply trustws"
    Then the exit code is 0
    And the Claude config in HOME keeps an untrusted entry for instance "trustws"
    And instance "trustws" records no Claude trust

  # --- Every surface that provisions an instance runs the same step ---

  @critical
  Scenario: reset rebuilds the instance trusted once the option is on
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "trustws" exists with body:
      """
      [workspace]
      name = "trustws"
      """
    When I run niwa init from config repo "trustws"
    Then the exit code is 0
    When I run "niwa create trustws"
    Then the exit code is 0
    And the Claude config in HOME has no entry for instance "trustws"
    When the config repo "trustws" is force-pushed to:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = true
      """
    And I run "niwa reset trustws" from workspace root
    Then the exit code is 0
    And the Claude config in HOME trusts instance "trustws"
    And instance "trustws" records Claude trust with mode "created"

  @critical
  Scenario: dispatch leaves the instance it provisions trusted and recorded
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "trustws" exists with body:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = true
      """
    When I run niwa init from config repo "trustws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "abababab-abab-4bab-8bab-abababababab"
    When I run "niwa dispatch trust-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    And the Claude config in HOME trusts the dispatch instance, recorded with mode "created"

  @critical
  Scenario: session-start provisioning leaves the instance trusted and recorded
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "trustws" exists with body:
      """
      [workspace]
      name = "trustws"

      [claude]
      trust_instances = true
      """
    When I run niwa init from config repo "trustws"
    Then the exit code is 0
    Given a background job state exists for session "cdcdcdcd-cdcd-4dcd-8dcd-cdcdcdcdcdcd"
    When I pipe a SessionStart hook for session "cdcdcdcd-cdcd-4dcd-8dcd-cdcdcdcdcdcd"
    Then the exit code is 0
    And the instance "trustws-cdcdcdcd-cdc" exists
    And the Claude config in HOME trusts instance "trustws-cdcdcdcd-cdc"
    And instance "trustws-cdcdcdcd-cdc" records Claude trust with mode "created"
