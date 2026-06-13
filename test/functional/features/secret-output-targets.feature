# secret-output targets (PRD-secret-output-targets)
#
# These scenarios drive the compiled binary through init -> create -> apply with
# a per-repo `env_output` declaration, then assert the materialized secret files
# (path and format), that the default path is unchanged when nothing is
# declared, that custom names stay invisible to git status, and that unsafe
# targets fail closed.

Feature: configurable secret-output targets
  niwa expands a repo's resolved secrets into operator-declared file(s) and
  format(s) via a cascading env_output setting, defaulting to .local.env in
  dotenv form. Custom target names are recorded as niwa-managed git-ignore
  coverage so a real secret never becomes committable.

  Design: docs/designs/current/DESIGN-secret-output-targets.md

  @critical
  Scenario: default target is unchanged when nothing is declared
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [env.vars]
      FOO = "bar"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "ws" contains "FOO=bar"

  Scenario: custom dotenv and json targets, default suppressed
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [env.vars]
      FOO = "bar"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      env_output = [".env.local", "secrets.json"]
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "tools/app/.env.local" in instance "ws" contains "FOO=bar"
    And the file "tools/app/secrets.json" in instance "ws" contains "bar"
    And the file "tools/app/secrets.json" in instance "ws" does not contain "FOO=bar"
    And the file "tools/app/.local.env" does not exist in instance "ws"
    And the git status of repo "app" in instance "ws" is clean

  Scenario: shell format inferred from extension
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [env.vars]
      FOO = "bar"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      env_output = "env.sh"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "tools/app/env.sh" in instance "ws" contains "export FOO='bar'"

  Scenario: explicit format override beats extension inference
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [env.vars]
      FOO = "bar"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      env_output = [{ path = ".env", format = "json" }]
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the file "tools/app/.env" in instance "ws" contains "bar"
    And the file "tools/app/.env" in instance "ws" does not contain "FOO=bar"

  Scenario: a custom name stays invisible to git status
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [env.vars]
      FOO = "bar"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      env_output = ".env"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the git exclude file of repo "app" in instance "ws" contains ".env"
    And the git status of repo "app" in instance "ws" is clean

  Scenario: a path-traversal target fails closed
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [env.vars]
      FOO = "bar"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      env_output = "../escape.env"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is not 0
    And the error output contains "escapes the repository"
    And the error output does not contain "FOO=bar"
