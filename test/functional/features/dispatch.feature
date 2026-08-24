Feature: niwa dispatch: provision, rollback, and reaper reclamation
  End-to-end scenarios for `niwa dispatch` using a local bare-repo server as a
  fake remote and a FAKE `claude` on PATH. No real claude, no daemon, and no
  network are required.

  dispatch creates a fresh ephemeral instance, launches a `claude --bg` worker
  rooted in it, captures the worker's session UUID by jobs-dir cwd correlation,
  and records an ephemeral dispatch-origin mapping keyed on the UUID. Any failure
  before the mapping is durable rolls the instance back. The name+TTL reaper
  backstop (keyed on the dispatch instance name, so a SIGKILL before the marker
  is written still leaves a reclaimable orphan) and the liveness-rule sweep
  reclaim the instance once its session is deleted (its job entry disappears); a
  session that merely finished a task or went idle keeps its instance.

  The fake claude writes $HOME/.claude/jobs/<short>/state.json carrying the
  chosen UUID and the launch cwd (the instance dir, which dispatch sets via
  cmd.Dir), so the capture path resolves it. The functional sandbox points HOME
  into a per-scenario directory, so the jobs dir is hermetic.

  Design: docs/designs/DESIGN-instance-dispatch.md

  # --- Provision + map on a successful dispatch ---

  @critical
  Scenario: dispatch provisions an instance and records a dispatch-origin mapping
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch hello-task --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    And a dispatch-origin mapping exists for session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

  # --- Model selection resolves a category to a concrete model ---

  @critical
  Scenario: dispatch resolves a capability category and forwards it to the worker
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I run "niwa dispatch model-task --model powerful --detach" from the workspace root
    Then the exit code is 0
    And the launched claude was invoked with "--model opus"

  # --- Rollback on launch failure ---

  @critical
  Scenario: a launch failure rolls the dispatch instance back
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch that fails to launch
    When I run "niwa dispatch doomed-task --detach" from the workspace root
    Then the exit code is not 0
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  # --- Reaper reclaims a deleted dispatch session ---

  @critical
  Scenario: niwa reap reclaims a dispatch instance after its session is deleted
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    When I run "niwa dispatch reap-me --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  # --- Reaper spares a live dispatch session ---

  @critical
  Scenario: niwa reap spares a dispatch instance whose session is still live
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
    When I run "niwa dispatch keep-me --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And a dispatch-origin mapping exists for session "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

  # --- Reaper spares a live worker whose mapping was lost (backstop liveness) ---
  # Regression guard for the data-loss bug. An UNMAPPED, past-TTL dispatch
  # instance that a live worker is still rooted in must NOT be reclaimed by the
  # name+TTL backstop. Before the fix the backstop keyed on name + age alone,
  # with no liveness check, and deleted exactly this shape -- including the
  # caller's own instance mid-dispatch, which vanished its cwd and then broke the
  # follow-on provisioning clone. The live worker's job-state cwd is the instance
  # dir, so the reaper's mapping-independent liveness guard spares it.

  @critical
  Scenario: niwa reap spares an unmapped dispatch instance whose worker is still live
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I run "niwa dispatch keep-live --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch-origin mapping is removed
    And the dispatch instance is aged past the backstop TTL
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists

  # --- Interactive prompt capture ---
  #
  # With no prompt argument on a terminal, dispatch opens a capture: paste or
  # type the task, press Enter to dispatch. The pasted block is delimited by the
  # terminal's bracketed-paste markers, which is what makes the newlines inside
  # it inert so a single Enter can submit.

  @critical
  Scenario: a pasted multiline task dispatches and reaches the worker verbatim
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
    When I run "niwa dispatch --detach" under a pty with input "\e[200~panic: runtime error\n\tmain.go:42\e[201~\r"
    Then the exit code is 0
    And the launched claude was invoked with "panic: runtime error"

  # The never-block guarantee. Standard input is a pipe that is never written to
  # and never closed, so a command that reads it hangs rather than receiving an
  # immediate end-of-input. That distinction is the whole point: with stdin at
  # /dev/null this scenario would pass against an implementation that violates
  # the guarantee.

  @critical
  Scenario: dispatch with no prompt and no terminal refuses instead of waiting
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "ffffffff-ffff-4fff-8fff-ffffffffffff"
    When I run "niwa dispatch" with stdin held open
    Then the exit code is 1
    And the error output contains "not an interactive terminal"

  # --- Oversized prompts ---
  #
  # A prompt too large to travel as one argv element is not refused. niwa writes
  # it to a file inside the instance and hands the worker a pointer plus an
  # excerpt. The path must be absolute: the fake worker resolves it from "/" so
  # an instance-relative path fails this scenario rather than passing by
  # accident, since dispatch sets the worker's cwd to the instance.
  #
  # This goes through the capture rather than a positional argument, and it has
  # to: delivering an oversized prompt positionally would require the harness to
  # exec niwa with an argument past the very limit under test. The capture is
  # the only path where niwa builds the oversized string itself.

  @critical
  Scenario: an oversized prompt dispatches through a spilled file
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
    When I dispatch a generated paste of 140000 bytes
    Then the exit code is 0
    And the worker received a pointer to a spilled prompt

  # --- What a dispatched session leaves behind ---
  #
  # The handle a dispatch prints is printed once. For an agent that will not
  # hand over a session while its turn is running, the terminal never attaches,
  # so resuming later is the only way that session is ever used -- and the
  # developer needs the handle after the terminal that printed it is gone.
  # niwa list already reads the mapping store for its keep-alive marker, and the
  # mapping records the agent and the handle, so the command it prints is built
  # from that agent's own declaration rather than from a name typed here.

  @critical
  Scenario: a dispatched session is reachable from niwa list after the terminal closes
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01a10000-0000-7000-8000-00000000ab1e"
    When I run "niwa dispatch some-task --detach --harness codex" from the workspace root
    Then the exit code is 0
    When I run "niwa list" from the workspace root
    Then the exit code is 0
    And the output contains "codex resume -c "
    And the output contains "01a10000-0000-7000-8000-00000000ab1e"

  # --- The under-equipped-worker warning, before the prompt rather than after ---
  #
  # A worker launched at the instance root reads the orientation and the
  # workspace's skills written there, and none of the configuration half -- no
  # MCP servers, no session environment, no posture -- because for a Codex
  # session that half lives in a document niwa writes inside repositories. The
  # warning that says so is advice about a prompt that has not been written
  # yet. This scenario asks for a prompt niwa cannot get -- no terminal to
  # capture one from -- so the run ends before anything is provisioned or
  # launched. The warning still has to be there: if it printed with the
  # completion hints, as it once did, this run would say nothing.
  #
  # The wording has narrowed twice, each time because a delivery arrived rather
  # than because the sentence was softened. It used to say the worker received
  # no orientation, which the declaration said and the agent contradicted; then
  # the workspace's skills started reaching the root too, and the warning
  # stopped claiming those were missing. Both halves are asserted below, so a
  # warning that drifts in either direction fails here.

  @critical
  Scenario: the under-equipped-worker warning arrives before the prompt is asked for
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01a30000-0000-7000-8000-00000000c0de"
    When I run "niwa dispatch --harness codex" with stdin held open
    Then the exit code is 1
    And the error output contains "not an interactive terminal"
    And the error output contains "reads the workspace orientation and skills written there"
    And the error output contains "but not the workspace's MCP servers, session environment, or approval and sandbox posture"

  # --- Reclaiming a dispatched Codex instance, and refusing to ---
  #
  # Codex never removes a rollout, so the record-gone rule the other agent is
  # reclaimed by never fires for it. Before the idle rule, that meant a
  # dispatched Codex instance was spared by every sweep for good and only
  # `niwa destroy` cleared it. The rule reads two things instead: when the
  # session was last worked in, and whether a writer holds its lock right now.
  #
  # Neither scenario needs a real codex binary, which is the point of running
  # them here rather than behind @codex-live: the record is a file with an
  # mtime and the lock is a real flock, and those are the whole mechanism.

  @critical
  Scenario: niwa reap reclaims a dispatched Codex instance nobody came back to
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01a40000-0000-7000-8000-00000000dead"
    When I run "niwa dispatch abandoned --detach --harness codex" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    And a dispatch-origin mapping exists for session "01a40000-0000-7000-8000-00000000dead"
    # Still fresh: the sweep leaves it alone, which is what stops the rule
    # below from being "reap everything".
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    Given the codex session was last worked in "48h" ago
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And no dispatch-origin mapping remains

  # The guard, and the case that would have destroyed a working directory. A
  # single turn can run far longer than the grace period without appending
  # anything to the rollout, so staleness alone would reap an instance a worker
  # is writing in. The lock is what a live worker holds throughout its turn.

  @critical
  Scenario: niwa reap spares a Codex instance whose session has a live writer
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake codex for dispatch with session "01a50000-0000-7000-8000-00000000beef"
    When I run "niwa dispatch long-turn --detach --harness codex" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    Given the codex session was last worked in "48h" ago
    And a writer holds the codex session "01a50000-0000-7000-8000-00000000beef" lock
    When I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And a dispatch-origin mapping exists for session "01a50000-0000-7000-8000-00000000beef"
