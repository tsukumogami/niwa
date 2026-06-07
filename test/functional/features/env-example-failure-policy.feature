# env-example failure policy (PRD-env-example-failure-policy)
#
# These scenarios drive the compiled binary through init -> create -> apply,
# plant a .env.example into the cloned repo after create, then re-run apply so
# the pre-pass classifies the planted values and resolves a warn/fail action.
#
# Entropy fixture: the value aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n has a Shannon
# entropy of ~4.89 bits/char (well above the >3.5 threshold), is not on the
# vendor-token blocklist, and is not on the safe allowlist, so it classifies as
# CategoryEntropy. The vendor-token fixture sk_live_aB3dE7gH1jK9mN2pQ5rT8wX
# matches the sk_live_ blocklist prefix, so it classifies as CategoryVendorToken
# regardless of entropy. Neither literal is a magic constant: the entropy value
# is a deterministic mix of distinct characters and the vendor value is a known
# blocklist prefix plus the same high-entropy tail.

Feature: env-example failure policy
  niwa's .env.example pre-pass warns by default on probable secrets and lets
  operators opt into hard failures per detection category at user, project,
  per-repo, and variable granularity, with most-specific-wins resolution, an
  inline annotation, and a per-run downgrade flag. No diagnostic ever echoes
  value bytes or the entropy score.

  # PRD AC: undeclared high-entropy value warns and apply exits 0 (warn default).
  Scenario: warn-by-default on an undeclared high-entropy value
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
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output contains "API_TOKEN"
    And the error output contains "category entropy"

  # PRD AC: undeclared vendor-token value warns and apply exits 0 (warn default).
  Scenario: warn-by-default on an undeclared vendor-token value
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
    And I write "STRIPE_KEY=sk_live_aB3dE7gH1jK9mN2pQ5rT8wX" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output contains "STRIPE_KEY"
    And the error output contains "category vendor-token"

  # PRD AC: entropy=fail at the user (global) level fails a high-entropy value
  # while a vendor-token-only value still warns (per-category independence and
  # user-level inheritance fall-through, since nothing is set at project level).
  Scenario: user-level entropy fail fails entropy but vendor-token still warns
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
    And a personal overlay exists with body:
      """
      [workspaces.myws.env_example_policy]
      entropy = "fail"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is not 0
    And the error output contains "API_TOKEN"
    And the error output contains "category entropy"

  # Companion to the above: a vendor-token-only value under the same user-level
  # entropy=fail policy still warns (vendor_token unset -> default warn).
  Scenario: user-level entropy fail leaves vendor-token warning
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
    And a personal overlay exists with body:
      """
      [workspaces.myws.env_example_policy]
      entropy = "fail"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "STRIPE_KEY=sk_live_aB3dE7gH1jK9mN2pQ5rT8wX" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output contains "STRIPE_KEY"
    And the error output contains "category vendor-token"

  # PRD AC: a project-level policy overrides the user-level policy. The global
  # sets entropy=fail; the workspace sets entropy=warn; the workspace wins, so
  # apply proceeds.
  Scenario: project-level policy overrides user-level policy
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [workspace.env_example_policy]
      entropy = "warn"

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    And a personal overlay exists with body:
      """
      [workspaces.myws.env_example_policy]
      entropy = "fail"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output contains "API_TOKEN"
    And the error output contains "category entropy"

  # PRD AC: a per-repo policy overrides the workspace-wide policy within the same
  # workspace. The workspace sets entropy=fail; repo "safe" overrides to warn.
  # The high-entropy value in "safe" warns (apply proceeds).
  Scenario: per-repo policy overrides workspace-wide policy
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "strict" exists
    And a source repo "safe" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [workspace.env_example_policy]
      entropy = "fail"

      [repos.strict]
      url = "{repo:strict}"
      group = "tools"

      [repos.safe]
      url = "{repo:safe}"
      group = "tools"

      [repos.safe.env_example_policy]
      entropy = "warn"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/safe" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output contains "API_TOKEN"
    And the error output contains "category entropy"

  # PRD AC: a value marked warn-only via inline annotation warns while a project
  # fail policy still fails other keys in the same file. The workspace sets
  # entropy=fail; KEEP_ME carries "# niwa: warn" and warns, BLOCK_ME has no
  # annotation and fails, so apply exits non-zero and names BLOCK_ME.
  Scenario: inline annotation warns while project fail policy fails other keys
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [workspace.env_example_policy]
      entropy = "fail"

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write to file ".env.example" in repo "tools/myapp" of instance "myws" with body:
      """
      KEEP_ME=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n # niwa: warn
      BLOCK_ME=Xq7Lm2Pv9Rt4Ks1Wn6Yb3Zc8Df5Gh0
      """
    When I run "niwa apply myws"
    Then the exit code is not 0
    And the error output contains "BLOCK_ME"

  # PRD AC: a workspace-config per-variable entry overrides an inline annotation
  # (operator wins). The repo marks API_TOKEN "# niwa: warn" inline, but the
  # workspace config sets [env_example_policy.vars] API_TOKEN = "fail". The
  # operator entry wins, so apply exits non-zero.
  Scenario: workspace per-variable entry overrides inline annotation
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [workspace.env_example_policy.vars]
      API_TOKEN = "fail"

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n # niwa: warn" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is not 0
    And the error output contains "API_TOKEN"

  # PRD AC: --allow-plaintext-secrets downgrades all fail outcomes to warnings
  # and apply exits 0. The workspace sets entropy=fail; the flag downgrades it.
  Scenario: per-run override downgrades all failures to warnings
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [workspace.env_example_policy]
      entropy = "fail"

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws --allow-plaintext-secrets"
    Then the exit code is 0
    And the error output contains "downgraded fail to warn"
    And the error output contains "API_TOKEN"

  # PRD AC: detection behavior does not differ by remote visibility. The same
  # high-entropy value with the same default policy warns and exits 0 whether or
  # not a repo is "public". (The public-remote branch was removed in Issue 4;
  # there is no visibility axis to set, so equivalent default behavior here
  # demonstrates the removal end-to-end.)
  Scenario: behavior does not differ by remote visibility
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
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output contains "category entropy"

  # PRD AC: disabling the scan produces no warnings or failures for that scope.
  # read_env_example = false at the workspace level; even a high-entropy value
  # under no policy yields no probable-secret diagnostic, and apply exits 0.
  Scenario: scan disabled produces no warnings or failures
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      read_env_example = false

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output does not contain "API_TOKEN"
    And the error output does not contain "probable secret"

  # PRD AC: no warning or error contains the value bytes, a value fragment, or
  # the numeric entropy score. Drive both a warn (default) and a fail path and
  # grep stderr for the value bytes, a substring fragment, and the 3.5 threshold
  # number. The diagnostics name only the key and the category.
  Scenario: diagnostics never contain value bytes or the entropy score
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [workspace.env_example_policy]
      entropy = "fail"

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And I write "API_TOKEN=aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is not 0
    And the error output contains "API_TOKEN"
    And the error output contains "category entropy"
    And the error output does not contain "aB3dE7gH1jK9mN2pQ5rT8wX0zC4vB6n"
    And the error output does not contain "aB3dE7gH"
    And the error output does not contain "3.5"

  # PRD AC: an existing workspace config with no failure policy declared applies
  # without error and exhibits warn-by-default behavior on a probable secret.
  Scenario: no policy declared applies without error and warns
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
    And I write "API_TOKEN=sk_live_aB3dE7gH1jK9mN2pQ5rT8wX" to file ".env.example" in repo "tools/myapp" of instance "myws"
    When I run "niwa apply myws"
    Then the exit code is 0
    And the error output contains "category vendor-token"
