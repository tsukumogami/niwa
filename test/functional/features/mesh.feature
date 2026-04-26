Feature: Cross-session mesh (Issue #10 harness)
  End-to-end scenarios that exercise the daemon's claim → spawn → restart /
  watchdog / reconciliation pipelines with a scripted worker fake in place
  of the real `claude -p` binary. All scenarios run under
  `make test-functional-critical` in under 10 s each by setting small-integer
  timing overrides.

  # ---------------------------------------------------------------------
  # Happy-path delegation (AC-D7, AC-D8, AC-D9).
  # ---------------------------------------------------------------------

  @critical
  Scenario: async delegation completes via fake worker finish-completed
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "async-happy" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create async-happy"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "async-happy"
    When I delegate a task to role "worker" in instance "async-happy" with body '{"kind":"unit"}'
    Then the task state in instance "async-happy" eventually becomes "completed"
    And the task transitions log in instance "async-happy" contains "state_transition"

  @critical
  Scenario: fake worker abandon flows through as abandoned outcome
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "abandon-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-abandoned"
    When I run "niwa create abandon-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "abandon-ws" with body '{"kind":"unit"}'
    Then the task state in instance "abandon-ws" eventually becomes "abandoned"
    And the task reason in instance "abandon-ws" contains "scripted-abandon"

  # ---------------------------------------------------------------------
  # Cancellation races (AC-D9, AC-Q10, AC-Q11).
  # ---------------------------------------------------------------------

  @critical
  Scenario: cancellation before claim transitions task to cancelled
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "cancel-ws" exists
    And the daemon has small timing overrides
    And the daemon pauses before claiming envelopes
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create cancel-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "cancel-ws" with body '{"kind":"unit"}'
    Then the pause marker "before_claim" eventually appears
    When I cancel the task in instance "cancel-ws"
    And I release the daemon pause marker
    Then the task state in instance "cancel-ws" eventually becomes "cancelled"

  @critical
  Scenario: cancellation racing consumption rename resolves to single terminal state (AC-Q10)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "qrace-ws" exists
    And the daemon has small timing overrides
    And the daemon pauses after claiming envelopes
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create qrace-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "qrace-ws" with body '{"kind":"unit"}'
    Then the pause marker "after_claim" eventually appears
    When I cancel the task in instance "qrace-ws"
    And I release the daemon pause marker
    Then the task state in instance "qrace-ws" eventually becomes "completed"

  @critical
  Scenario: update racing consumption rename returns too_late after claim (AC-Q11)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "update-race-ws" exists
    And the daemon has small timing overrides
    And the daemon pauses after claiming envelopes
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create update-race-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "update-race-ws" with body '{"kind":"original"}'
    Then the pause marker "after_claim" eventually appears
    When I update the task body in instance "update-race-ws" to '{"kind":"updated"}'
    Then the output contains status "too_late"
    When I release the daemon pause marker
    Then the task state in instance "update-race-ws" eventually becomes "completed"

  # ---------------------------------------------------------------------
  # Restart cap + watchdog (AC-L3, AC-L4).
  # ---------------------------------------------------------------------

  @critical
  Scenario: restart cap abandons after 4 unexpected exits (AC-L3)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "retry-cap-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "progress-then-exit-zero"
    When I run "niwa create retry-cap-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "retry-cap-ws" with body '{"kind":"retry"}'
    Then the task state in instance "retry-cap-ws" eventually becomes "abandoned"
    And the task reason in instance "retry-cap-ws" contains "retry_cap_exceeded"
    And the task restart_count in instance "retry-cap-ws" equals 3

  @critical
  Scenario: stall watchdog escalates to SIGTERM then SIGKILL (AC-L4)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "stall-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "ignore-sigterm"
    When I run "niwa create stall-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "stall-ws" with body '{"kind":"stall"}'
    Then the task transitions log in instance "stall-ws" contains "watchdog_signal"
    And the task transitions log in instance "stall-ws" contains "SIGKILL"

  # ---------------------------------------------------------------------
  # Crash recovery (AC-L9, AC-L10).
  # ---------------------------------------------------------------------

  @critical
  Scenario: daemon crash with live worker — new daemon adopts orphan (AC-L9)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "live-orphan-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "stall-forever"
    When I run "niwa create live-orphan-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "live-orphan-ws" with body '{"kind":"long"}'
    Then the task state in instance "live-orphan-ws" eventually becomes "running"
    When I SIGKILL the daemon for instance "live-orphan-ws"
    And I restart the daemon for instance "live-orphan-ws"
    Then the task transitions log in instance "live-orphan-ws" contains "adoption"

  @critical
  Scenario: daemon crash with dead worker — new daemon reclassifies as unexpected exit (AC-L10)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "dead-orphan-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "stall-forever"
    When I run "niwa create dead-orphan-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "dead-orphan-ws" with body '{"kind":"long"}'
    Then the task state in instance "dead-orphan-ws" eventually becomes "running"
    When I SIGKILL the daemon for instance "dead-orphan-ws"
    And I SIGKILL the worker for instance "dead-orphan-ws"
    And I restart the daemon for instance "dead-orphan-ws"
    Then the task transitions log in instance "dead-orphan-ws" contains "unexpected_exit"

  # ---------------------------------------------------------------------
  # Concurrent apply (AC-C3).
  # ---------------------------------------------------------------------

  @critical
  Scenario: two concurrent applies spawn exactly one daemon (AC-C3)
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "concurrent-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create concurrent-ws"
    Then the exit code is 0
    When I run two concurrent applies for instance "concurrent-ws"
    Then exactly one daemon is running for instance "concurrent-ws"

  # ---------------------------------------------------------------------
  # Body-redaction regression (Decision 1 / PRD R36).
  # ---------------------------------------------------------------------

  @critical
  Scenario: daemon log does not contain envelope bodies or result payloads
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "redact-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create redact-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "redact-ws" with body '{"kind":"secret","marker":"NIWA-BODY-LEAK-CANARY"}'
    Then the task state in instance "redact-ws" eventually becomes "completed"
    And the daemon log for instance "redact-ws" does not contain "NIWA-BODY-LEAK-CANARY"

  # ---------------------------------------------------------------------
  # Authorization negative (scenario-25): unauthorized caller receives
  # NOT_TASK_PARTY. Uses a bogus task_id + wrong session role to trip the
  # executor check in auth.go.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa_finish_task with wrong task_id returns NOT_TASK_PARTY
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "auth-neg-ws" exists
    And the daemon has small timing overrides
    When I run "niwa create auth-neg-ws"
    Then the exit code is 0
    Then an unauthorized MCP call for instance "auth-neg-ws" receives NOT_TASK_PARTY

  # MCP config layout regression: Claude Code's MCP discovery loads
  # `<cwd>/.mcp.json` at the directory root and does not walk parent
  # directories. Niwa writes only `<instance>/.mcp.json` — the PRD's
  # headline scenario opens Claude at the instance root, where that
  # file is what discovery resolves. No per-repo files: writing
  # `<repoDir>/.mcp.json` would collide destructively with any project
  # that ships its own MCP config (see issue #78). The legacy
  # `.claude/.mcp.json` path Claude Code never reads must not be
  # written either.
  #
  # This scenario does not invoke claude — it asserts the on-disk
  # layout is correct. The bug it guards against shipped in v0.9.0
  # because every test that did invoke claude bypassed discovery via
  # `--mcp-config <path> --strict-mcp-config`.
  @critical
  Scenario: MCP config layout — instance-root only, no per-repo files
    Given a clean niwa environment
    And a local git server is set up
    And a multi-repo channeled workspace "mcp-layout" with web and backend exists
    When I run "niwa create mcp-layout"
    Then the exit code is 0
    And the file ".mcp.json" exists in instance "mcp-layout"
    And the file ".claude/.mcp.json" does not exist in instance "mcp-layout"
    And the file "apps/web/.mcp.json" does not exist in instance "mcp-layout"
    And the file "apps/web/.claude/.mcp.json" does not exist in instance "mcp-layout"
    And the file "apps/backend/.mcp.json" does not exist in instance "mcp-layout"
    And the file "apps/backend/.claude/.mcp.json" does not exist in instance "mcp-layout"

  # Single-repo channeled workspace: the simplest topology that engages
  # per-repo role enumeration without exercising multi-repo collision
  # logic. Asserts the instance-root .mcp.json is the sole MCP config
  # written (the per-repo dir gets its niwa-mesh skill but no .mcp.json).
  @critical
  Scenario: single-repo channeled workspace provisions instance-root .mcp.json only
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "single-repo-mcp" exists
    When I run "niwa create single-repo-mcp"
    Then the exit code is 0
    And the file ".mcp.json" exists in instance "single-repo-mcp"
    And the file "apps/app/.mcp.json" does not exist in instance "single-repo-mcp"
    And the file "apps/app/.claude/.mcp.json" does not exist in instance "single-repo-mcp"
    And the file "apps/app/.claude/skills/niwa-mesh/SKILL.md" exists in instance "single-repo-mcp"
    And the file ".niwa/roles/coordinator/inbox" exists in instance "single-repo-mcp"
    And the file ".niwa/roles/app/inbox" exists in instance "single-repo-mcp"

  # Drift recovery: the channels installer's writeIdempotent path must
  # restore a managed file after a user manually removes it. Without
  # this, an operator who clears `.mcp.json` thinking they're "resetting"
  # would silently lose the niwa MCP server and `niwa apply` would
  # fix nothing on the next run.
  @critical
  Scenario: niwa apply restores manually-deleted instance .mcp.json
    Given a clean niwa environment
    And a local git server is set up
    And a multi-repo channeled workspace "mcp-drift" with web and backend exists
    When I run "niwa create mcp-drift"
    Then the exit code is 0
    And the file ".mcp.json" exists in instance "mcp-drift"
    When I delete file ".mcp.json" in instance "mcp-drift"
    And I run "niwa apply mcp-drift"
    Then the exit code is 0
    And the file ".mcp.json" exists in instance "mcp-drift"

  # Pre-1.0 migration: the channel installer's migratePre1Layout helper
  # must remove .niwa/sessions/<uuid>/ directories on the first apply
  # under the new model. The legacy session dirs predate the role-based
  # mesh and would otherwise bloat .niwa/ across upgrades.
  @critical
  Scenario: niwa apply --channels migrates a pre-1.0 .niwa/sessions/ layout
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "mcp-migrate" exists
    When I run "niwa create mcp-migrate"
    Then the exit code is 0
    # Plant a legacy session directory and rewind the new layout so the
    # migration helper sees pre-1.0 state on the next apply (it short-
    # circuits when .niwa/roles/ already exists).
    When I delete directory ".niwa/roles" in instance "mcp-migrate"
    And I plant a legacy session directory "11111111-2222-4333-8444-555555555555" in instance "mcp-migrate"
    And I run "niwa apply mcp-migrate"
    Then the exit code is 0
    And the file ".niwa/sessions/11111111-2222-4333-8444-555555555555" does not exist in instance "mcp-migrate"
    And the file ".niwa/roles/coordinator/inbox" exists in instance "mcp-migrate"

  # Virtual-peer roles: a workspace with [channels.mesh.roles] entries
  # whose values are empty strings creates inboxes for those roles
  # without requiring a corresponding cloned repo. Useful for peers
  # that exist outside the workspace topology (for example a host-side
  # human or a separate process that participates as a niwa role).
  # Asserts that the inbox tree is provisioned and no per-repo writes
  # are attempted for paths that don't exist.
  @critical
  Scenario: virtual-peer roles via [channels.mesh.roles] provision inboxes only
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "virtual-peer-ws" exists
    When I run "niwa create virtual-peer-ws"
    Then the exit code is 0
    And the file ".mcp.json" exists in instance "virtual-peer-ws"
    And the file ".niwa/roles/coordinator/inbox" exists in instance "virtual-peer-ws"
    And the file ".niwa/roles/worker/inbox" exists in instance "virtual-peer-ws"
    And the file ".niwa/roles/coordinator/inbox/in-progress" exists in instance "virtual-peer-ws"
    And the file ".niwa/roles/worker/inbox/expired" exists in instance "virtual-peer-ws"

  # --no-channels disable round-trip: enable channels via the synthesized
  # path (--channels flag, no [channels.mesh] in config), then re-apply
  # with --no-channels. Asserts the instance-root .mcp.json and the
  # niwa-mesh skill are removed from ManagedFiles by cleanRemovedFiles.
  # Runtime artifacts under .niwa/roles/ and .niwa/tasks/ are left in
  # place — see issue #75 for the proper teardown design.
  @critical
  Scenario: niwa apply --no-channels removes the instance-root .mcp.json
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "mcp-toggle" exists
    When I run "niwa create mcp-toggle"
    Then the exit code is 0
    And the file ".mcp.json" exists in instance "mcp-toggle"
    And the file ".claude/skills/niwa-mesh/SKILL.md" exists in instance "mcp-toggle"
    When I run "niwa apply mcp-toggle --no-channels"
    Then the exit code is 0
    And the file ".mcp.json" does not exist in instance "mcp-toggle"
    And the file ".claude/skills/niwa-mesh/SKILL.md" does not exist in instance "mcp-toggle"

  # ---------------------------------------------------------------------
  # @channels-e2e (Issue #11): real `claude -p` scenarios. These cover
  # niwa surface the deterministic fake worker cannot reach — namely
  # (1) that Claude Code can load the instance-root `.mcp.json`, launch
  # `niwa mcp-serve`, and the first MCP tool call succeeds, and (2) that
  # niwa's fixed bootstrap prompt drives a real LLM to call
  # `niwa_check_messages` and then `niwa_finish_task` to completion.
  #
  # Prompts are deliberately anchored for deterministic matching:
  # Scenario 1 looks for the literal marker "CHECKED:" on stdout;
  # Scenario 2 asserts only on state.json (not LLM text) so wording
  # drift in the model's output cannot flake the test.
  #
  # Both scenarios skip cleanly when `claude` is not on PATH or
  # `ANTHROPIC_API_KEY` is unset (via claudeIsAvailable → godog.ErrPending).
  # Neither is tagged @critical, so `make test-functional-critical` never
  # invokes a real LLM.
  # ---------------------------------------------------------------------

  @channels-e2e
  Scenario: MCP-config loadability — claude -p can load instance-root .mcp.json and call niwa_check_messages
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a channeled workspace "mcp-loadability" exists
    When I run "niwa create mcp-loadability"
    Then the exit code is 0
    And the file ".mcp.json" exists in instance "mcp-loadability"
    When I run claude -p preserving case from instance root "mcp-loadability" with prompt:
      """
      Use the niwa_check_messages tool to check your inbox and output exactly: CHECKED:<count>, where <count> is the number of messages. Do not explain.
      """
    Then the output contains "CHECKED:"

  @channels-e2e
  Scenario: Bootstrap-prompt effectiveness — daemon-spawned real claude drives task to completed
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a channeled workspace "bootstrap-e2e" exists
    And the daemon uses the real claude worker spawn path
    When I run "niwa create bootstrap-e2e"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "bootstrap-e2e"
    When I queue a niwa_finish_task instruction for role "coordinator" in instance "bootstrap-e2e"
    Then the task state in instance "bootstrap-e2e" eventually becomes "completed" within 120 seconds

  # ---------------------------------------------------------------------
  # @channels-e2e-graph: full delegation graph with real LLMs on both
  # sides of the exchange. A coordinator `claude -p` runs at the instance
  # root, reads the niwa-mesh skill installed by `niwa create`, and is
  # asked to achieve a goal that requires delegating work to two repo-
  # scoped workers ("web" and "backend"). The daemon spawns each worker
  # as a fresh `claude -p` process; the workers must read the task body
  # the coordinator produced and create the marker file, then call
  # niwa_finish_task. Assertions are on observable artifacts only — the
  # marker files and .niwa/tasks/*/state.json — so wording drift in
  # either LLM's free-text output cannot flake the test.
  #
  # This is the headline PRD use case: "open one claude at the workspace
  # instance root, tell it 'do X in web and Y in backend, each in its
  # own session', and niwa launches the workers if they don't exist yet."
  #
  # Skipped on CI when `claude` or ANTHROPIC_API_KEY is missing (via
  # claudeIsAvailable → godog.ErrPending).
  # ---------------------------------------------------------------------

  @channels-e2e-graph
  Scenario: Coordinator LLM delegates to web and backend workers and both complete
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a multi-repo channeled workspace "graph-e2e" with web and backend exists
    And the daemon uses the real claude worker spawn path
    When I run "niwa create graph-e2e"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "graph-e2e"
    When I run claude -p preserving case from instance root "graph-e2e" within 600 seconds with prompt:
      """
      This workspace has two sub-projects, apps/web and apps/backend, each of which needs a marker.txt file created inside the repo directory.

      - apps/web/marker.txt must contain exactly the text: web
      - apps/backend/marker.txt must contain exactly the text: backend

      You have niwa tools available for delegating work to agents running in those repos. Use them to get both markers created, wait for the work to finish, and output exactly GRAPH_DONE when both are complete. Do not create the files yourself — delegate both tasks.
      """
    Then the output contains "GRAPH_DONE"
    And the file "marker.txt" in repo "apps/web" of instance "graph-e2e" exactly matches "web"
    And the file "marker.txt" in repo "apps/backend" of instance "graph-e2e" exactly matches "backend"
    And exactly 2 tasks in instance "graph-e2e" are in state "completed"
    # Audit-grounded checks: prove the coordinator and workers actually used the
    # niwa MCP path, not a side channel (e.g., coordinator writing markers itself
    # plus delegating empty tasks). See DESIGN-mcp-call-telemetry.md.
    And the coordinator in instance "graph-e2e" emitted niwa_delegate calls to roles "web,backend"
    And role "web" in instance "graph-e2e" emitted at least 1 successful niwa_finish_task call
    And role "backend" in instance "graph-e2e" emitted at least 1 successful niwa_finish_task call
