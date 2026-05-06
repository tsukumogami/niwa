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

  # ---------------------------------------------------------------------
  # Worker permission inheritance (DESIGN-worker-permissions).
  # The daemon resolves --permission-mode at startup from settings.local.json
  # and stores it in spawnContext. The dump-args fake writes os.Args to
  # .niwa/.test/worker_spawn_args.txt so these scenarios can assert on the
  # exact flags passed to the worker without invoking a real claude session.
  # ---------------------------------------------------------------------

  @critical
  Scenario: worker inherits bypassPermissions when coordinator is bypass-configured
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "bypass-perm-ws" with permissions "bypass" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "dump-args"
    When I run "niwa create bypass-perm-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "bypass-perm-ws" with body '{"kind":"perm-check"}'
    Then the task state in instance "bypass-perm-ws" eventually becomes "completed"
    And the worker in instance "bypass-perm-ws" was spawned with "--permission-mode=bypassPermissions"
    And the worker in instance "bypass-perm-ws" was not spawned with "Bash(gh *)"

  @critical
  Scenario: worker receives curated Bash tools when coordinator has no bypass configured
    Given a clean niwa environment
    And a local git server is set up
    And a channeled workspace "fallback-perm-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "dump-args"
    When I run "niwa create fallback-perm-ws"
    Then the exit code is 0
    When I delegate a task to role "worker" in instance "fallback-perm-ws" with body '{"kind":"perm-check"}'
    Then the task state in instance "fallback-perm-ws" eventually becomes "completed"
    And the worker in instance "fallback-perm-ws" was spawned with "--permission-mode=acceptEdits"
    And the worker in instance "fallback-perm-ws" was spawned with "Bash(gh *)"
    And the worker in instance "fallback-perm-ws" was spawned with "Bash(git *)"

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

  # AC-P5: the channels installer must create .niwa/sessions/sessions.json
  # at apply time so `niwa session list` and the coordinator-session
  # registry probes find a well-formed file from the start, not a lazy-
  # create on first `niwa session register`. The file is created only
  # if absent — re-apply must not overwrite a populated registry.
  @critical
  Scenario: niwa create provisions .niwa/sessions/sessions.json (AC-P5)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-registry-ws" exists
    When I run "niwa create session-registry-ws"
    Then the exit code is 0
    And the file ".niwa/sessions/sessions.json" exists in instance "session-registry-ws"

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

  # Regression guard: workers dispatched to non-coordinator roles must
  # receive NIWA_SESSION_ROLE=<their-role> in the MCP env block, not
  # "coordinator". Before the per-spawn worker.mcp.json fix, Claude Code's
  # env-block merge overrode NIWA_SESSION_ROLE to "coordinator" for every
  # worker — they looked in the coordinator inbox, found no task, and
  # exited without calling niwa_finish_task, producing retry_cap_exceeded.
  @channels-e2e
  Scenario: Worker with non-coordinator role receives correct role in MCP env block
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a channeled workspace "worker-role-e2e" exists
    And the daemon uses the real claude worker spawn path
    When I run "niwa create worker-role-e2e"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "worker-role-e2e"
    When I queue a niwa_finish_task instruction for role "worker" in instance "worker-role-e2e"
    Then the task state in instance "worker-role-e2e" eventually becomes "completed" within 120 seconds

  # ---------------------------------------------------------------------
  # Session lifecycle: niwa_create_session and niwa_destroy_session (AC-S2a,
  # AC-S2b). These scenarios exercise the MCP tools directly via
  # callMCPToolAsRole so no real LLM or daemon worker is needed.
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa_create_session provisions worktree and session state (AC-S2a)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-create-ws" exists
    When I run "niwa create session-create-ws"
    Then the exit code is 0
    When I call niwa_create_session for repo "app" with purpose "integration test" in instance "session-create-ws"
    Then the session is active in instance "session-create-ws"
    And the session worktree exists in instance "session-create-ws"
    And the session scaffold directory ".niwa/tasks" exists in the worktree
    And the session scaffold directory ".niwa/roles/app/inbox" exists in the worktree
    And the session scaffold directory ".niwa/roles/app/inbox/in-progress" exists in the worktree
    When I call niwa_destroy_session in instance "session-create-ws"
    Then the session is ended in instance "session-create-ws"

  @critical
  Scenario: niwa_destroy_session is idempotent (AC-S2b)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-destroy-ws" exists
    When I run "niwa create session-destroy-ws"
    Then the exit code is 0
    When I call niwa_create_session for repo "app" with purpose "idempotency test" in instance "session-destroy-ws"
    Then the session is active in instance "session-destroy-ws"
    When I call niwa_destroy_session in instance "session-destroy-ws"
    Then the session is ended in instance "session-destroy-ws"
    When I call niwa_destroy_session in instance "session-destroy-ws"
    Then the session is ended in instance "session-destroy-ws"

  # ---------------------------------------------------------------------
  # Session CLI: niwa session create/destroy, niwa mesh list, niwa go
  # <repo> <session-id> (AC-S5a, AC-S5b, AC-S5c)
  # ---------------------------------------------------------------------

  @critical
  Scenario: niwa session create writes response file for shell navigation (AC-S5a)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "cli-create-ws" exists
    When I run "niwa create cli-create-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "cli-create-ws"
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa session create app integration-test-purpose"
    Then the exit code is 0
    And the response file contains a path under instance "cli-create-ws" worktrees directory
    And a session lifecycle state file exists for repo "app" with status "active" in instance "cli-create-ws"

  @critical
  Scenario: niwa go with session-id navigates to worktree (AC-S5b)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "cli-go-ws" exists
    When I run "niwa create cli-go-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "cli-go-ws"
    When I call niwa_create_session for repo "app" with purpose "go-nav test" in instance "cli-go-ws"
    Then the session is active in instance "cli-go-ws"
    And I set env "NIWA_RESPONSE_FILE" to a temp path
    When I run "niwa go app" with last session id
    Then the exit code is 0
    And the response file contains the last session worktree path in instance "cli-go-ws"

  @critical
  Scenario: niwa mesh list shows coordinator sessions (AC-S5c)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "mesh-list-ws" exists
    When I run "niwa create mesh-list-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "mesh-list-ws"
    When I run "niwa session register"
    Then the exit code is 0
    When I run "niwa mesh list"
    Then the exit code is 0
    And the output contains "ROLE"
    And the output contains "coordinator"

  @critical
  Scenario: niwa session list --status filters lifecycle sessions (AC-S5d)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-ls-ws" exists
    When I run "niwa create session-ls-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-ls-ws"
    When I call niwa_create_session for repo "app" with purpose "list-filter test" in instance "session-ls-ws"
    Then the session is active in instance "session-ls-ws"
    When I run "niwa session list --status active"
    Then the exit code is 0
    And the output contains "active"
    And the output contains "app"

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

  # -----------------------------------------------------------------------
  # Session-targeted task routing: tasks with session_id are routed to the
  # session's worktree daemon, not the main instance daemon.
  # -----------------------------------------------------------------------

  @session-daemon
  Scenario: Task delegated to session routes to worktree daemon and completes (AC-S3a)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-route-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create session-route-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-route-ws"
    When I call niwa_create_session for repo "app" with purpose "routing test" in instance "session-route-ws"
    Then the session is active in instance "session-route-ws"
    And the session worktree exists in instance "session-route-ws"
    When I delegate a task to session role "app" in instance "session-route-ws"
    Then the task state in instance "session-route-ws" eventually becomes "completed" within 60 seconds

  @session-daemon
  Scenario: Two parallel sessions for same repo route tasks independently (AC-S3b)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-parallel-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create session-parallel-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-parallel-ws"
    When I call niwa_create_session for repo "app" with purpose "session A" in instance "session-parallel-ws"
    Then the session is active in instance "session-parallel-ws"
    And the session worktree exists in instance "session-parallel-ws"
    When I delegate a task to session role "app" in instance "session-parallel-ws"
    Then the task state in instance "session-parallel-ws" eventually becomes "completed" within 60 seconds
    When I call niwa_create_session for repo "app" with purpose "session B" in instance "session-parallel-ws"
    Then the session is active in instance "session-parallel-ws"
    And the session worktree exists in instance "session-parallel-ws"
    When I delegate a task to session role "app" in instance "session-parallel-ws"
    Then the task state in instance "session-parallel-ws" eventually becomes "completed" within 60 seconds

  @session-daemon
  Scenario: niwa_delegate with non-existent session_id returns SESSION_NOT_FOUND (AC-S3c)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-notfound-ws" exists
    When I run "niwa create session-notfound-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-notfound-ws"
    When I delegate a task to session role "app" with session id "deadbeef" in instance "session-notfound-ws" expecting an error
    Then the last MCP response contains code "SESSION_NOT_FOUND"

  @session-daemon
  Scenario: niwa_delegate to an ended session returns SESSION_INACTIVE (AC-S3d)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-inactive-ws" exists
    When I run "niwa create session-inactive-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-inactive-ws"
    When I call niwa_create_session for repo "app" with purpose "will be ended" in instance "session-inactive-ws"
    Then the session is active in instance "session-inactive-ws"
    When I call niwa_destroy_session in instance "session-inactive-ws"
    Then the session is ended in instance "session-inactive-ws"
    When I try to delegate a task to session role "app" in instance "session-inactive-ws"
    Then the last MCP response contains code "SESSION_INACTIVE"

  # -----------------------------------------------------------------------
  # Session continuity: the worktree daemon captures CLAUDE_SESSION_ID from
  # the first worker and writes it to the session state file. A second
  # delegation to the same session uses --resume so the conversation continues.
  # -----------------------------------------------------------------------

  @session-daemon
  Scenario: ClaudeConversationID captured from first worker exit (AC-R11a)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-capture-ws" exists
    And the daemon has small timing overrides
    And I set the fake Claude session ID to "test-claude-id-abc123"
    And the daemon runs with fake worker scenario "capture-id-and-dump-args"
    When I run "niwa create session-capture-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-capture-ws"
    When I call niwa_create_session for repo "app" with purpose "capture test" in instance "session-capture-ws"
    Then the session is active in instance "session-capture-ws"
    When I delegate a task to session role "app" in instance "session-capture-ws"
    Then the task state in instance "session-capture-ws" eventually becomes "completed" within 60 seconds
    And the session claude_conversation_id equals "test-claude-id-abc123" in instance "session-capture-ws"

  @session-daemon
  Scenario: Second delegation to same session uses --resume (AC-R11b)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-resume-ws" exists
    And the daemon has small timing overrides
    And I set the fake Claude session ID to "test-resume-id-xyz789"
    And the daemon runs with fake worker scenario "capture-id-and-dump-args"
    When I run "niwa create session-resume-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-resume-ws"
    When I call niwa_create_session for repo "app" with purpose "resume test" in instance "session-resume-ws"
    Then the session is active in instance "session-resume-ws"
    When I delegate a task to session role "app" in instance "session-resume-ws"
    Then the task state in instance "session-resume-ws" eventually becomes "completed" within 60 seconds
    And the session claude_conversation_id equals "test-resume-id-xyz789" in instance "session-resume-ws"
    When I delegate a task to session role "app" in instance "session-resume-ws"
    Then the task state in instance "session-resume-ws" eventually becomes "completed" within 60 seconds
    And the worker in session was spawned with "--resume"

  @session-daemon
  Scenario: niwa_destroy_session abandons in-progress task (AC-S2c)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-kill-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "stall-forever"
    When I run "niwa create session-kill-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-kill-ws"
    When I call niwa_create_session for repo "app" with purpose "kill test" in instance "session-kill-ws"
    Then the session is active in instance "session-kill-ws"
    When I delegate a task to session role "app" in instance "session-kill-ws"
    And the task state in instance "session-kill-ws" eventually becomes "running" within 30 seconds
    When I call niwa_destroy_session in instance "session-kill-ws"
    Then the session is ended in instance "session-kill-ws"
    And the task state in instance "session-kill-ws" eventually becomes "abandoned" within 15 seconds
    And the task reason in instance "session-kill-ws" contains "session_destroyed"

  # -----------------------------------------------------------------------
  # Git and filesystem: session worktrees are git worktrees; the main clone
  # must stay on main throughout the session lifecycle.
  # -----------------------------------------------------------------------

  @session-git
  Scenario: Main clone remains on main branch after session create (AC-S2d)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-main-ws" exists
    When I run "niwa create session-main-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-main-ws"
    When I call niwa_create_session for repo "app" with purpose "git isolation test" in instance "session-main-ws"
    Then the session is active in instance "session-main-ws"
    And the main clone of "app" in instance "session-main-ws" is on branch "main"

  @session-git
  Scenario: Session branch created in repo at session create time (AC-S2e)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-branch-ws" exists
    When I run "niwa create session-branch-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-branch-ws"
    When I call niwa_create_session for repo "app" with purpose "branch test" in instance "session-branch-ws"
    Then the session is active in instance "session-branch-ws"
    And the session branch exists in repo "app" of instance "session-branch-ws"

  @session-git
  Scenario: Worktree directory removed after session destroy (AC-S2h)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-rm-ws" exists
    When I run "niwa create session-rm-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-rm-ws"
    When I call niwa_create_session for repo "app" with purpose "remove test" in instance "session-rm-ws"
    Then the session is active in instance "session-rm-ws"
    And the session worktree exists in instance "session-rm-ws"
    When I call niwa_destroy_session in instance "session-rm-ws"
    Then the session is ended in instance "session-rm-ws"
    And the session worktree directory does not exist

  @session-git
  Scenario: Main clone stays on main after session destroy (AC-S2d-post-destroy)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-main-post-ws" exists
    When I run "niwa create session-main-post-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-main-post-ws"
    When I call niwa_create_session for repo "app" with purpose "main branch preservation" in instance "session-main-post-ws"
    Then the session is active in instance "session-main-post-ws"
    When I call niwa_destroy_session in instance "session-main-post-ws"
    Then the session is ended in instance "session-main-post-ws"
    And the main clone of "app" in instance "session-main-post-ws" is on branch "main"

  @session-git
  Scenario: niwa apply does not touch session worktrees (AC-S15a)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-apply-ws" exists
    When I run "niwa create session-apply-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-apply-ws"
    When I call niwa_create_session for repo "app" with purpose "apply isolation" in instance "session-apply-ws"
    Then the session is active in instance "session-apply-ws"
    And the session worktree exists in instance "session-apply-ws"
    When I run "niwa apply session-apply-ws"
    Then the exit code is 0
    And the session worktree exists in instance "session-apply-ws"
    And the output does not contain ".niwa/worktrees"

  # -----------------------------------------------------------------------
  # Input validation: error codes for invalid inputs to session tools.
  # -----------------------------------------------------------------------

  @session-error
  Scenario: niwa_create_session with unknown repo returns UNKNOWN_ROLE (AC-R1-err)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-badrepo-ws" exists
    When I run "niwa create session-badrepo-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-badrepo-ws"
    When I run "niwa session create nonexistent-repo test-purpose"
    Then the exit code is not 0
    And the error output contains "UNKNOWN_ROLE"

  @session-error
  Scenario: niwa_destroy_session with unknown session_id returns SESSION_NOT_FOUND (AC-R4-err)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-badsid-ws" exists
    When I run "niwa create session-badsid-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-badsid-ws"
    When I run "niwa session destroy deadbeef"
    Then the exit code is not 0
    And the error output contains "SESSION_NOT_FOUND"

  # -----------------------------------------------------------------------
  # Cross-instance ask routing: niwa_ask(to="coordinator") from a session
  # worktree worker must reach the main instance coordinator, not the
  # worktree's coordinator (which doesn't exist).
  # -----------------------------------------------------------------------

  @session-daemon
  Scenario: Worker in session worktree routes niwa_ask to main coordinator (AC-S4a)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-ask-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "ask-and-finish"
    When I run "niwa create session-ask-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-ask-ws"
    And the coordinator session is registered in instance "session-ask-ws"
    When I call niwa_create_session for repo "app" with purpose "ask routing test" in instance "session-ask-ws"
    Then the session is active in instance "session-ask-ws"
    When I delegate a task to session role "app" in instance "session-ask-ws"
    And the coordinator blocks on niwa_await_task and handles questions for instance "session-ask-ws"
    Then the task state in instance "session-ask-ws" eventually becomes "completed" within 60 seconds

  # -----------------------------------------------------------------------
  # Delegation isolation (SESSION_REQUIRED / read_only contract)
  # These five @critical scenarios verify the delegation guard introduced in
  # the mesh session lifecycle design: niwa_delegate requires a session_id,
  # read_only:true opts out for tasks that make no git changes, and session_id
  # takes precedence when both are provided.
  # -----------------------------------------------------------------------

  @critical
  Scenario: niwa_delegate without session_id returns SESSION_REQUIRED
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-required-ws" exists
    When I run "niwa create session-required-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-required-ws"
    When I try to delegate a task to role "app" without session_id in instance "session-required-ws"
    Then the last MCP response contains code "SESSION_REQUIRED"
    And no task files exist in instance "session-required-ws"

  @critical
  Scenario: niwa_delegate with read_only:true and no session_id routes to main clone
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "readonly-delegate-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create readonly-delegate-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "readonly-delegate-ws"
    When I delegate a read_only task to role "app" in instance "readonly-delegate-ws"
    Then the task state in instance "readonly-delegate-ws" eventually becomes "completed" within 30 seconds
    And the file ".niwa/worktrees" does not exist in instance "readonly-delegate-ws"

  @critical
  Scenario: niwa_delegate with session_id routes to session worktree daemon (regression)
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-route-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create session-route-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-route-ws"
    When I call niwa_create_session for repo "app" with purpose "regression test" in instance "session-route-ws"
    Then the session is active in instance "session-route-ws"
    And the session worktree exists in instance "session-route-ws"
    When I delegate a task to session role "app" in instance "session-route-ws"
    Then the task state in instance "session-route-ws" eventually becomes "completed" within 60 seconds

  @critical
  Scenario: niwa_delegate with both session_id and read_only:true routes to session worktree
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "session-precedence-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create session-precedence-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "session-precedence-ws"
    When I call niwa_create_session for repo "app" with purpose "precedence test" in instance "session-precedence-ws"
    Then the session is active in instance "session-precedence-ws"
    When I delegate a task to session role "app" with read_only true in instance "session-precedence-ws"
    Then the task state in instance "session-precedence-ws" eventually becomes "completed" within 60 seconds

  @critical
  Scenario: Coordinator golden path: create session, delegate with session_id, work completes, session destroyed
    Given a clean niwa environment
    And a local git server is set up
    And a single-repo channeled workspace "golden-path-ws" exists
    And the daemon has small timing overrides
    And the daemon runs with fake worker scenario "finish-completed"
    When I run "niwa create golden-path-ws"
    Then the exit code is 0
    And I set NIWA_INSTANCE_ROOT to instance "golden-path-ws"
    When I call niwa_create_session for repo "app" with purpose "golden path test" in instance "golden-path-ws"
    Then the session is active in instance "golden-path-ws"
    And the session worktree exists in instance "golden-path-ws"
    When I delegate a task to session role "app" in instance "golden-path-ws"
    Then the task state in instance "golden-path-ws" eventually becomes "completed" within 60 seconds
    When I call niwa_destroy_session in instance "golden-path-ws"
    Then the session is ended in instance "golden-path-ws"
    And the session worktree directory does not exist

  # -----------------------------------------------------------------------
  # @session-e2e: real claude -p scenarios for session lifecycle.
  # Skipped when claude or ANTHROPIC_API_KEY is not available.
  # The @known-gap scenario documents the missing --resume implementation.
  # -----------------------------------------------------------------------

  @session-e2e
  Scenario: Real coordinator creates session and delegates task to session worktree (AC-R2-e2e)
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a single-repo channeled workspace "session-e2e-ws" exists
    And the daemon uses the real claude worker spawn path
    When I run "niwa create session-e2e-ws"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "session-e2e-ws"
    When I run claude -p preserving case from instance root "session-e2e-ws" within 300 seconds with prompt:
      """
      Use niwa_create_session to create a session for the "app" repo with purpose "e2e test". Then use niwa_delegate with the returned session_id to delegate a task to "app" with body {"action":"create_marker","path":"marker.txt","content":"session_e2e"}. Wait for the task to complete with niwa_await_task. Then output exactly: SESSION_E2E_DONE
      """
    Then the output contains "SESSION_E2E_DONE"
    And exactly 1 tasks in instance "session-e2e-ws" are in state "completed"

  @session-e2e @known-gap
  Scenario: Second task in same session resumes Claude conversation (AC-R10-e2e — --resume not yet implemented)
    Given a clean niwa environment
    And claude is available
    And a local git server is set up
    And a single-repo channeled workspace "session-continuity-ws" exists
    And the daemon uses the real claude worker spawn path
    When I run "niwa create session-continuity-ws"
    Then the exit code is 0
    And the file ".niwa/daemon.pid" exists in instance "session-continuity-ws"
    When I run claude -p preserving case from instance root "session-continuity-ws" within 600 seconds with prompt:
      """
      Your goal is to test session continuity.

      Step 1: Use niwa_create_session to create a session for "app" with purpose "continuity test".
      Step 2: Use niwa_delegate with session_id to send task A to "app" with body {"action":"write_secret","secret":"HELLO_FROM_TASK_A"}. Wait for completion.
      Step 3: Use niwa_delegate with the SAME session_id to send task B to "app" with body {"action":"recall_secret"}. Wait for completion.
      Step 4: If task B's result contains "HELLO_FROM_TASK_A", output CONTINUITY_OK. Otherwise output CONTINUITY_FAIL.
      """
    Then the output contains "CONTINUITY_OK"
