Feature: niwa init --bootstrap failure-mode user-visible text
  End-to-end scenarios for the bootstrap-flag failure paths: R25 mutual
  exclusion, R9 non-GitHub host check, R10/R11 adjacent status-code
  failures, and R13 TTY/non-TTY dispatch.  Each scenario asserts both
  the exact stderr string mandated by the PRD and the exit code from
  the PRD R23 mapping table.

  PRD: docs/prds/PRD-init-bootstrap-empty-source.md
  Design: docs/designs/DESIGN-init-bootstrap-empty-source.md

  # --- R25 mutual exclusion: --bootstrap AND --no-bootstrap ---

  @critical
  Scenario: R25 mutual exclusion produces exact string and exit code 2
    Given a clean niwa environment
    When I run "niwa init --from acme/foo --bootstrap --no-bootstrap"
    Then the exit code is 2
    And the error output contains "--bootstrap and --no-bootstrap are mutually exclusive"

  # --- R9 host check: non-GitHub source refused before any git invocation ---

  @critical
  Scenario: R9 host check refuses non-GitHub source with exit code 3
    Given a clean niwa environment
    When I run "niwa init myws --from gitlab.com/acme/foo --bootstrap"
    Then the exit code is 3
    And the error output contains "bootstrap supports only GitHub sources in v1; got host=gitlab.com"

  # --- R13 non-TTY no-flag fail-fast ---
  # Uses the GitHub fake to serve an empty (no-marker) tarball so the
  # materialize probe surfaces *config.NoMarkerError. With stdin pointed
  # at a pipe and no --bootstrap, the R13 row 6 fail-fast applies.

  @critical
  Scenario: R13 non-TTY no-flag fail-fast emits exact text and exit code 4
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake serves "owner/foo" at ref "HEAD" empty
    When I run "niwa init bar --from owner/foo" from workspace root
    Then the exit code is 4
    And the error output contains "remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold"

  # --- R13 TTY interactive prompt scenarios ---
  # Run niwa under util-linux `script -q` so IsStdinTTY returns true.
  # The pty wrapper feeds the supplied input to stdin and captures the
  # combined stdout+stderr stream for assertion.

  @critical
  Scenario: R13 TTY no-flag prompt accepts Y proceeds (declined fixture; bootstrap stops at clone)
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake serves "owner/foo" at ref "HEAD" empty
    When I run "niwa init bar --from owner/foo" under a pty with input "y\n"
    Then the error output contains "Remote has no .niwa/workspace.toml. Scaffold a minimal config and stage it on a niwa-bootstrap branch?"

  @critical
  Scenario: R13 TTY no-flag prompt declines on N (exit 0)
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake serves "owner/foo" at ref "HEAD" empty
    When I run "niwa init bar --from owner/foo" under a pty with input "n\n"
    Then the exit code is 0
    And the error output contains "Remote has no .niwa/workspace.toml. Scaffold a minimal config and stage it on a niwa-bootstrap branch?"

  # --- R10 / R11 401 / 403 / 404 user-visible text ---
  # The GitHub fake's SetStatus knob (test/functional/tarball_fake_server.go
  # SetStatus) returns the supplied HTTP code for the tarball + commits
  # endpoints. The 401/403/404 paths route through classifyMaterializeError
  # which emits the PRD-mandated substrings (R10 GH_TOKEN scopes / R11
  # three-causes).

  @critical
  Scenario: R10 401 stderr substring (auth error)
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake returns HTTP 401 for "owner/foo" at ref "HEAD"
    When I run "niwa init bar --from owner/foo --bootstrap" from workspace root
    Then the exit code is 1
    And the error output contains "verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope"

  @critical
  Scenario: R10 403 stderr substring (auth error)
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake returns HTTP 403 for "owner/foo" at ref "HEAD"
    When I run "niwa init bar --from owner/foo --bootstrap" from workspace root
    Then the exit code is 1
    And the error output contains "verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope"

  @critical
  Scenario: R11 404 stderr substrings (typo / private-no-token / zero-commit)
    Given a clean niwa environment
    And a GitHub fake is configured
    And the GitHub fake returns HTTP 404 for "owner/foo" at ref "HEAD"
    When I run "niwa init bar --from owner/foo --bootstrap" from workspace root
    Then the exit code is 1
    And the error output contains "verify the slug is correct (org/repo)"
    And the error output contains "if the repo is private, set GH_TOKEN"
    And the error output contains "if the repo is brand new and has no commits yet"
