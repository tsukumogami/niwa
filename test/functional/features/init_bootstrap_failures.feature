Feature: niwa init --bootstrap failure-mode user-visible text
  End-to-end scenarios for the bootstrap-flag failure paths: R25 mutual
  exclusion, R9 non-GitHub host check, and R13 TTY/non-TTY dispatch.
  Each scenario asserts both the exact stderr string mandated by the PRD
  and the exit code from the PRD R23 mapping table.

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
  # The materialize fall-through (non-GitHub file:// clones) on
  # localGitServer source repos surfaces a "git rev-parse HEAD" error
  # rather than *config.NoMarkerError because the fallback path expects
  # a populated working tree with branch HEAD set up the way GitHub-style
  # clones produce. The localGitServer fixture builds non-GitHub bare
  # repos that don't satisfy that contract. Unit tests in
  # internal/cli/init_bootstrap_test.go stub MaterializeFromSource to
  # inject *config.NoMarkerError directly and assert the exact R13
  # non-TTY string + exit 4. The functional scenario stays @pending
  # until either (a) the localGitServer learns to spin up a fake GitHub
  # tarball endpoint that returns a "no marker" success, or (b) the
  # harness can simulate a GitHub host directly. Issue 5 covers this.

  @pending
  Scenario: R13 non-TTY no-flag fail-fast emits exact text and exit code 4
    Given a clean niwa environment

  # --- R13 TTY interactive prompt scenarios ---
  # The functional harness does not currently simulate a TTY-connected
  # stdin (binary runs as subprocess; stdin is a pipe). The TTY-Y /
  # TTY-N branches are covered by unit tests in
  # internal/cli/init_bootstrap_test.go which stub IsStdinTTY directly.
  # When the harness gains TTY simulation (Issue 5 territory) these
  # @pending scenarios should become @critical.

  @pending
  Scenario: R13 TTY no-flag prompt accepts Y (placeholder, requires TTY simulation)
    Given a clean niwa environment

  @pending
  Scenario: R13 TTY no-flag prompt declines on N (placeholder, requires TTY simulation)
    Given a clean niwa environment

  # --- R10 / R11 401 / 403 / 404 user-visible text ---
  # The local git server cannot emulate HTTP 401/403/404 responses; it
  # speaks the git protocol over file://. R10 / R11 coverage stays in
  # unit-level tests against classifyMaterializeError until a remote
  # status-injection harness lands.

  @pending
  Scenario: R10 401 stderr substring (placeholder, requires HTTP-status fake)
    Given a clean niwa environment

  @pending
  Scenario: R10 403 stderr substring (placeholder, requires HTTP-status fake)
    Given a clean niwa environment

  @pending
  Scenario: R11 404 stderr substrings (placeholder, requires HTTP-status fake)
    Given a clean niwa environment
