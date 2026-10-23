Feature: Host-local env layer
  The machine config's [env] and [claude.env] tables set or clear single keys
  after every other layer, the personal overlay included, so one host can
  differ on one key while the rest of the overlay still applies. Each host key
  is reported in apply output with the layer it won over, and never with a
  value.

  @critical
  Scenario: a host clears one overlay key and overrides another while the rest of the overlay arrives
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with a commit
    And a config repo "hostenv" exists with body:
      """
      [workspace]
      name = "hostenv"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And a personal overlay exists with body:
      """
      [global.claude.env]
      promote = ["KOTO_SESSION_CLOUD_BUCKET", "KOTO_SESSION_CLOUD_SECRET_KEY"]

      [global.claude.env.vars]
      KOTO_SESSION_BACKEND = "cloud"
      OVERLAY_FLAG = "overlay-flag-on"

      [global.env.secrets]
      KOTO_SESSION_CLOUD_BUCKET = "overlay-bucket"
      KOTO_SESSION_CLOUD_SECRET_KEY = "overlay-secret"
      """
    # Written before init, so init's own rewrite of the machine config has to
    # keep the host tables.
    And the niwa machine config is replaced with:
      """
      [claude.env]
      unset = ["KOTO_SESSION_BACKEND"]

      [env.vars]
      KOTO_SESSION_CLOUD_SECRET_KEY = "host-secret"
      """
    When I run niwa init from config repo "hostenv"
    Then the exit code is 0
    When I run "niwa create hostenv"
    Then the exit code is 0
    # The session env: the cleared key is gone, the overridden promoted key
    # carries the host value, and the overlay's other keys still arrive.
    And the repo "tools/app" settings document in instance "hostenv" sets no env "KOTO_SESSION_BACKEND"
    And the repo "tools/app" settings document in instance "hostenv" sets env "KOTO_SESSION_CLOUD_SECRET_KEY" to "host-secret"
    And the repo "tools/app" settings document in instance "hostenv" sets env "KOTO_SESSION_CLOUD_BUCKET" to "overlay-bucket"
    And the repo "tools/app" settings document in instance "hostenv" sets env "OVERLAY_FLAG" to "overlay-flag-on"
    # The repo env file gets the host value for the key the host overrode, and
    # the overlay's value for the one it did not.
    And the file "tools/app/.local.env" in instance "hostenv" contains "KOTO_SESSION_CLOUD_SECRET_KEY=host-secret"
    And the file "tools/app/.local.env" in instance "hostenv" contains "KOTO_SESSION_CLOUD_BUCKET=overlay-bucket"
    And the file "tools/app/.local.env" in instance "hostenv" does not contain "overlay-secret"
    # Each host key names the layer it won over, with no value.
    And the error output contains "host-local claude.env unset"
    And the error output contains "host-local env.vars"
    And the error output contains "wins over the personal overlay"
    And the error output does not contain "host-secret"
    And the error output does not contain "overlay-secret"
    # A later apply keeps applying the layer.
    When I run "niwa apply hostenv"
    Then the exit code is 0
    And the repo "tools/app" settings document in instance "hostenv" sets no env "KOTO_SESSION_BACKEND"
    And the repo "tools/app" settings document in instance "hostenv" sets env "OVERLAY_FLAG" to "overlay-flag-on"

  @critical
  Scenario: a host sets a session key to a different value
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with a commit
    And a config repo "hostvar" exists with body:
      """
      [workspace]
      name = "hostvar"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And a personal overlay exists with body:
      """
      [global.claude.env.vars]
      KOTO_SESSION_BACKEND = "cloud"
      OVERLAY_FLAG = "overlay-flag-on"
      """
    And the niwa machine config is replaced with:
      """
      [claude.env.vars]
      KOTO_SESSION_BACKEND = "local"
      """
    When I run niwa init from config repo "hostvar"
    Then the exit code is 0
    When I run "niwa create hostvar"
    Then the exit code is 0
    And the repo "tools/app" settings document in instance "hostvar" sets env "KOTO_SESSION_BACKEND" to "local"
    And the repo "tools/app" settings document in instance "hostvar" sets env "OVERLAY_FLAG" to "overlay-flag-on"
    And the error output contains "host-local claude.env.vars"
    And the error output contains "wins over the personal overlay"

  Scenario: a host layer that sets and clears the same key fails apply
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with a commit
    And a config repo "hostbad" exists with body:
      """
      [workspace]
      name = "hostbad"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And the niwa machine config is replaced with:
      """
      [claude.env]
      unset = ["KOTO_SESSION_BACKEND"]

      [claude.env.vars]
      KOTO_SESSION_BACKEND = "local"
      """
    When I run niwa init from config repo "hostbad"
    Then the exit code is 0
    When I run "niwa create hostbad"
    Then the exit code is not 0
    And the error output contains "KOTO_SESSION_BACKEND: key is both set in vars and listed in unset"
