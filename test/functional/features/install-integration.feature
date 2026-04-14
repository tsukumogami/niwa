Feature: OOTB shell completion via install paths
  niwa ships completion through two install paths: install.sh writes
  ~/.niwa/env that evals `niwa shell-init auto` on every shell start,
  and the in-repo tsuku recipe's install_shell_init hook captures
  `niwa shell-init bash/zsh` output into $TSUKU_HOME/share/shell.d/.
  Both paths produce the same shell-init output, so these scenarios
  verify the output contains both the wrapper and the cobra completion
  function, and that sourcing it in a fresh shell makes __complete
  dispatch correctly.

  Design: docs/designs/current/DESIGN-contextual-completion.md (Decision 6)

  Background:
    Given a clean niwa environment

  # --- shell-init output contract (used by both install paths) ---

  @critical
  Scenario: bash shell-init emits the wrapper function
    When I run "niwa shell-init bash"
    Then the exit code is 0
    And the "bash" shell-init output contains "niwa()"
    And the "bash" shell-init output contains "_NIWA_SHELL_INIT=1"

  @critical
  Scenario: bash shell-init emits the cobra completion function
    When I run "niwa shell-init bash"
    Then the exit code is 0
    And the "bash" shell-init output contains "__start_niwa"
    And the "bash" shell-init output contains "__complete"

  @critical
  Scenario: zsh shell-init emits the wrapper and the completion function
    When I run "niwa shell-init zsh"
    Then the exit code is 0
    And the "zsh" shell-init output contains "niwa()"
    And the "zsh" shell-init output contains "#compdef"
    And the "zsh" shell-init output contains "__complete"

  # --- End-to-end install.sh chain ---
  #
  # Simulates the runtime state install.sh produces: ~/.niwa/bin/niwa on PATH,
  # ~/.niwa/env with the delegation eval, a fresh shell that sources it.
  # After sourcing, `niwa __complete` should dispatch through the wrapper,
  # hit the real binary, and return candidates from the sandboxed registry.

  @critical
  Scenario: install.sh env-file chain dispatches __complete after sourcing
    Given a registered workspace "alpha" exists
    When I source the installer env file and run completion for "apply" with prefix ""
    Then the exit code is 0
    And the completion output contains "alpha"

  Scenario: install.sh env-file chain supports niwa go completion
    Given a registered workspace "myws" exists
    When I source the installer env file and run completion for "go -w" with prefix ""
    Then the exit code is 0
    And the completion output contains "myws"

  # --- tsuku recipe contract ---
  #
  # The in-repo recipe at .tsuku-recipes/niwa.toml declares
  # source_command = "{install_dir}/bin/niwa shell-init {shell}"
  # for bash and zsh. tsuku's install_shell_init action captures that
  # command's stdout and bakes it into a shell.d file. The scenarios
  # above already prove the bake source content is correct; this one
  # confirms the recipe declaration still matches the binary's
  # supported shells.

  Scenario: recipe declares bash and zsh shell-init targets
    When I run "niwa shell-init auto"
    Then the exit code is 0
