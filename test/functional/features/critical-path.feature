Feature: Critical path: init, create, and apply
  End-to-end scenarios for the niwa workspace workflow using a local bare-repo
  server as a fake remote. No GitHub access required.

  Design: docs/designs/DESIGN-functional-test-critical-path.md

  # --- Happy path: full init → create → apply cycle ---

  @critical
  Scenario: happy path init, create, and apply with a source repo
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
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
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "myws" exists
    And the repo "tools/myapp" exists in instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0

  # --- Regression: create -2 must not fail due to missing ConfigSourceURL ---
  # Before the fix, create.go used instanceName ("myws-2") for the registry
  # lookup instead of configName ("myws"), leaving ConfigSourceURL empty on
  # second and subsequent instances.

  @critical
  Scenario: creating a second instance does not fail due to missing ConfigSourceURL
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "myws" exists
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "myws-2" exists

  # --- Orphan cleanup: failed create must not leave a partial instance dir ---

  @critical
  Scenario: failed create cleans up the orphan instance directory
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.broken]
      url = "file:///nonexistent/does-not-exist.git"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is not 0
    And the instance "myws" does not exist

  # --- Overlay: convention discovery via file:// URL ---
  # DeriveOverlayURL now handles file:// URLs so that local bare-repo tests can
  # exercise the convention overlay path without a GitHub remote.

  @critical
  Scenario: init discovers overlay repo via convention URL and create applies it
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    And an overlay repo "myws-overlay" exists with body:
      """
      [env]
      [env.vars]
      NIWA_TEST_OVERLAY = "applied"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the instance "myws" exists

  # --- .env.example source attribution appears in verbose status ---
  # SourceKindEnvExample sources are written into .local.env's Sources slice
  # during apply. niwa status --verbose must display ".env.example" as the
  # source label, not "vault" or "plaintext".

  @critical
  Scenario: apply with .env.example writes source attribution visible in verbose status
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
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
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "PORT=8080" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    When I run "niwa status --verbose" from workspace "myws"
    Then the exit code is 0
    And the output contains ".env.example"
    And the output does not contain "vault://.env.example"
    And the output does not contain "plaintext://.env.example"

  # --- Idempotency: apply twice must both succeed ---

  @critical
  Scenario: apply is idempotent
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa apply myws"
    Then the exit code is 0
    When I run "niwa apply myws"
    Then the exit code is 0
