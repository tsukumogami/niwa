<!-- decision:start id="test-harness-contract" status="assumed" -->
### Decision: Test Harness Contract and Integration-Test Scope

**Context**

The PRD's R51 requires niwa to expose a deterministic test harness so every
acceptance criterion in the cross-session-communication feature can be
verified without a live `claude -p` subprocess. The harness has four
coupled parts: (i) the invocation shape of `NIWA_WORKER_SPAWN_COMMAND`,
(ii) how the scripted worker fake talks to niwa, (iii) the semantics of
the four timing-override env vars (`NIWA_RETRY_BACKOFF_SECONDS`,
`NIWA_STALL_WATCHDOG_SECONDS`, `NIWA_SIGTERM_GRACE_SECONDS`,
`NIWA_DESTROY_GRACE_SECONDS`), and (iv) whatever narrow live-claude
coverage justifies keeping a `@channels-e2e` tag at all.

The existing `test/functional/` tree already carries nearly everything the
harness needs: a `callMCPTool` helper that drives `niwa mcp-serve` via
JSON-RPC, a `runClaudeP` template for subprocess exec with sandboxed env,
per-scenario `homeDir`/`tmpDir`/`workspaceRoot` isolation, and an
`envOverrides` map that already flows into every spawned process. The
harness is additive: a scripted-worker binary plus a small number of new
step functions that set `envOverrides["NIWA_WORKER_SPAWN_COMMAND"]` and
the four timing overrides. The prior-art `DESIGN-channels-integration-test.md`
and its three pre-registered-coordinator `@channels-e2e` scenarios are
deleted wholesale because (a) the PRD reassigns `claude -p` from
coordinator to worker, and (b) every coordinator-side tool call is already
verified by direct MCP-JSON-RPC in the `@critical` set.

The decision must also respect Decision 3 (MCP server topology and caller
authentication): whatever authorization primitive niwa uses to prove a
worker owns the task it is finishing, the scripted fake must be able to
prove the same way. If Decision 3 chose a task-scoped credential written
into the worker's env by the daemon, the fake inherits it the same way.
If Decision 3 chose PPID-based discovery, the fake runs under the daemon
the same way. The harness is agnostic to that mechanism; it only requires
that the daemon's spawn path is the only path that touches it.

**Assumptions**

- **Decision 3 exposes worker authorization to the spawned process via env
  or via `.mcp.json` reference.** The fake, spawned by the daemon with the
  same env and argv niwa would have passed to `claude -p`, inherits the
  authorization material the same way. If Decision 3 instead requires the
  worker to call a signed setup RPC, the fake must implement that setup
  step; this is accommodated by making the fake MCP-client-driven rather
  than filesystem-writer-driven.
- **The `@critical` scenario port to the new tool surface happens in the
  same design.** Existing `@critical` scenarios that call
  `niwa_send_message`, `niwa_check_messages`, `niwa_ask` continue to cover
  coordinator-surface behavior. The harness does not have to reproduce
  that coverage; it focuses on worker-side behavior.
- **`claude -p`'s argv is stable for v1.** The harness substitutes only
  the binary; if Anthropic changes `claude -p` argv in a way niwa's spawn
  path must adapt to, the adaptation happens in niwa's spawn path and the
  fake sees the new argv unchanged.
- **Fractional-second timing precision is not required for v1.** Overrides
  are integer seconds. If a future scenario needs sub-second timing, a
  successor decision can add `_MS` variants without breaking the v1 names.

**Chosen: Literal-path binary + MCP-client-first fake with direct-FS
escape hatch + integer-seconds overrides with comma-list backoff + two
residual `@channels-e2e` smoke scenarios**

#### Invocation shape (sub-question 1)

`NIWA_WORKER_SPAWN_COMMAND` is a **literal path to an executable**. When
set, niwa's daemon exec's it in place of `claude`, preserving every other
argv element and env var niwa would have passed. Concretely, where
production exec would be:

```
claude -p "<bootstrap>" --permission-mode=acceptEdits \
  --mcp-config=<instanceRoot>/.claude/.mcp.json --strict-mcp-config
```

the test path becomes:

```
$NIWA_WORKER_SPAWN_COMMAND -p "<bootstrap>" --permission-mode=acceptEdits \
  --mcp-config=<instanceRoot>/.claude/.mcp.json --strict-mcp-config
```

The fake parses `-p <bootstrap>` to extract the task-id, reads the
`--mcp-config` path to discover how to reach `niwa mcp-serve`, ignores
`--permission-mode` and `--strict-mcp-config`. All other env vars (most
importantly whatever task-id / authorization env Decision 3 injects) flow
through unchanged.

The fake itself is a tiny Go binary built alongside the test suite at
`test/functional/cmd/niwa-test-worker/` with its behavior driven by a
separate env var `NIWA_TEST_WORKER_SCRIPT` that names a script file inside
`test/functional/scripts/`. One binary, many scripts — each script defines
a sequence of MCP calls with optional sleeps, conditional branches (on
inbox contents), and exit codes. The scripts are data, not code, so adding
a new AC scenario adds a new script file plus a Gherkin step, not a new
fake binary.

**Rejected alternatives for this sub-question:**

- **Command-template substitution** (`NIWA_WORKER_SPAWN_COMMAND="/fake
  --script {task_id} --root {instance_root}"`). Rejected. It duplicates
  information niwa already passes through argv (`-p`) and env
  (`NIWA_INSTANCE_ROOT`), introduces a niwa-owned templating DSL that
  production code has to parse and substitute, and creates a surface where
  bugs in substitution can mask or create bugs in niwa's real spawn path.
  The literal-path form keeps production code unchanged except for the
  single `if envVar != "" { binary = envVar }` branch.
- **Embedded Go fake registered at runtime.** Rejected. It requires a
  test-only hook in `mesh_watch.go` that production builds must carry, it
  bypasses the spawn path entirely (defeating the purpose of a harness
  that proves the spawn path works), and it prevents scenarios from
  exercising multi-process race behavior where the daemon and the worker
  are genuinely separate OS processes. The literal binary is a real
  subprocess with a real PID, so `IsPIDAlive` checks, SIGCHLD handling,
  SIGTERM/SIGKILL delivery, `adopted_at` reconciliation, and all the
  kernel-level lifecycle machinery the PRD requires are exercised for
  real, not simulated in-process.

#### Fake-to-niwa communication (sub-question 2)

The fake is an **MCP client by default**. Each script step typically reads
"call `niwa_check_messages`; parse the envelope; call `niwa_finish_task`
with these arguments; exit 0". The fake uses the same JSON-RPC
primitive the existing `callMCPTool` helper uses: spawn `niwa mcp-serve`
(pointed at by `--mcp-config`), write `initialize` + `tools/call`, read
the response. This guarantees the scripted worker exercises the same MCP
surface a real worker would — any authorization bug, any argument-parsing
bug, any state-transition bug in the tool implementation is caught.

For the narrow set of AC that cannot be verified through MCP alone, the
fake has an **escape hatch for direct filesystem operations**. These are:

- **AC-Q10 / AC-Q11 — race window between daemon read and rename.** The
  PRD's test-spawn-hook / fsnotify-pause language implies a production
  surface niwa must expose for "pause the daemon at a named point". The
  pause hook lives in niwa, not in the fake. The fake's role in these
  ACs is only to not spawn yet; the scenario uses a
  `NIWA_DAEMON_PAUSE_AT=pre-rename` env var on the daemon to block at a
  named checkpoint, issues a direct inbox rename as the "cancel", then
  lifts the pause. The fake itself does nothing special here.
- **AC-L9 / AC-L10 — daemon crash with live/dead worker.** Simulating the
  daemon's death requires the scenario to `kill -9` the daemon process
  from the Go test driver (not from the fake). The fake's contribution is
  either staying alive (L9) or exiting before the daemon is killed (L10);
  both are expressible as script directives (`sleep_until_signal`,
  `exit_immediately`).

So the "direct-FS escape hatch" is not something the fake itself uses;
it's a niwa-side production surface for deterministic pause points, plus
Go-test-driver-level process control. The fake stays purely an MCP
client. Keeping the fake's communication surface narrow means every
scenario is reasoning in one idiom (MCP calls) plus optional
test-driver-level process orchestration; there is no third surface.

**Rejected alternatives for this sub-question:**

- **Direct filesystem only.** Rejected because it bypasses the MCP
  authorization code path. Every AC that asserts "worker W calling tool T
  with task-id X transitions state" would be proved against direct file
  writes, leaving the actual MCP tool handler untested in the scripted
  path. Given that tool handler bugs (authorization mismatch, state check
  ordering, error-code selection) are a dominant failure class, skipping
  this surface is unacceptable.
- **Hybrid with both surfaces as equal peers.** Rejected because it
  creates ambiguity about what the harness is proving. "The scripted fake
  used the MCP path in scenario A but the FS path in scenario B, so A
  covers the handler but B covers the raw state machine" is a subtlety
  that multiplies maintenance cost. Restricting direct-FS work to
  daemon-pause hooks (owned by niwa, not the fake) and to Go-test-driver
  process control keeps one fake-level idiom and one niwa-level test
  seam.

#### Env-var semantics (sub-question 3)

The four PRD names stand as-is. Units and multi-value semantics:

| Env var | Unit | Semantics when set |
|---------|------|--------------------|
| `NIWA_RETRY_BACKOFF_SECONDS` | integer seconds, comma list | Replaces the default 30,60,90 list. Three values required when set; scenarios use `1,2,3` to collapse AC-L5 into ~6 seconds. A single value `N` is shorthand for `N,N,N`. Zero is allowed for unit-test scenarios that want no backoff. |
| `NIWA_STALL_WATCHDOG_SECONDS` | integer seconds, single value | Replaces the default 900 (15 min). Scenarios use `2` for AC-L4. Zero disables the watchdog. |
| `NIWA_SIGTERM_GRACE_SECONDS` | integer seconds, single value | Replaces the default 5. Scenarios use `1` for AC-L4 grace-to-SIGKILL. Zero means "send SIGKILL immediately after SIGTERM" (useful for stress scenarios). |
| `NIWA_DESTROY_GRACE_SECONDS` | integer seconds, single value | Replaces the default 5. Scenarios use `1` for AC-P11. |

Parsing is strict. A malformed value (non-integer, negative, wrong comma
count for backoff) causes the daemon to log an error and exit non-zero at
startup, rather than silently using defaults. This prevents a typo in a
scenario from masquerading as a passing test by running at default
timings.

The envs are read once at daemon startup (or `niwa destroy` startup for
`NIWA_DESTROY_GRACE_SECONDS`). Values do not hot-reload; a scenario that
needs a different value runs a fresh daemon.

**Rejected alternatives for this sub-question:**

- **JSON-encoded per-attempt backoff** (e.g.
  `NIWA_RETRY_BACKOFF_SECONDS={"attempt_1":1,"attempt_2":2,"attempt_3":3}`).
  Rejected. Comma-list is trivially parseable in shell scripts, in
  Gherkin step args, and in Go (one-liner). JSON adds a parser
  dependency in the niwa code path for a feature whose entire purpose is
  test-only configuration.
- **Millisecond-precision** (renaming to `_MS` suffixes). Rejected for v1
  because every AC in the PRD expresses tolerances in "~N seconds", and
  integer-second precision lands every scenario inside 1-10 seconds. If
  future stress scenarios need sub-second timing, the names carry the
  unit in them (`_SECONDS`), so adding `_MS` variants is additive, not a
  rename.

#### Residual `@channels-e2e` scope (sub-question 4)

Two scenarios survive under `@channels-e2e`. Both are smoke tests — they
assert pass/fail, not content — and both verify things the harness
cannot verify by construction.

**Scenario 1: The niwa MCP server is loadable by Claude Code.** A plain
`niwa create --channels` workspace, a user opens `claude -p` at the
instance root, the prompt is `List the tools you have available.` The
test passes when stdout contains `niwa_delegate`, `niwa_check_messages`,
`niwa_finish_task`, and `niwa_ask`. Everything here is determined by
`.mcp.json` shape, binary paths, Claude Code's MCP config resolution
order, and `--strict-mcp-config` semantics. The harness cannot prove any
of these because the harness never asks Claude Code to discover niwa; it
runs the fake directly. If Anthropic changes `.mcp.json` schema or
`--mcp-config` flag semantics, this scenario catches it.

**Scenario 2: A real worker can consume a real task.** A plain
`niwa create --channels` workspace, the daemon is running at default
(live `claude -p`) spawn, a coordinator MCP call delegates a task with
the body `{"instruction":"Call niwa_finish_task with outcome=completed
and result={\"done\":true}. Exit."}`. The scenario passes when the task
reaches `completed` within a 90-second budget. This proves the worker
bootstrap flow end to end: real `claude -p` starts, the niwa MCP server
is reachable, the skill's worker instructions are loaded (R10), the LLM
follows them, the resulting tool call lands in the handler. No content
assertion beyond "state reached `completed`" — the LLM may say anything
and still pass. Abandonment via retry-cap is not live-tested because it
requires the LLM to reliably disobey instructions, which is exactly the
kind of non-determinism we run from.

Both scenarios are gated by `@channels-e2e` and by the existing
`claudeIsAvailable` guard, and skip when `ANTHROPIC_API_KEY` is unset.
Neither runs under the `@critical` tag. CI runs them on a nightly schedule
or on PRs that touch `internal/workspace/channels.go`, `internal/cli/
mesh_watch.go`, or the `niwa-mesh` skill installer. Ordinary PRs do not
invoke them.

**Rejected alternatives for this sub-question:**

- **Zero live-claude coverage (delete `@channels-e2e` entirely).**
  Tempting on the principle that niwa's correctness is decoupled from
  Claude's, but the two scenarios above genuinely cover surfaces niwa
  owns that the harness cannot reach: MCP config loadability by the
  Claude Code client (not niwa's server — that's covered by `@critical`)
  and end-to-end "does the bootstrap prompt actually cause the LLM to
  call a niwa tool". A regression in either surface — a malformed
  `.mcp.json`, a bootstrap prompt that omits the task-id, a skill that
  doesn't actually instruct the worker to call `niwa_finish_task` — would
  pass the harness and break real users.
- **Three-plus scenarios covering specific feature slices.** The original
  `DESIGN-channels-integration-test.md` proposed three. Rejected because
  the third scenario (coordinator `niwa_wait` via live claude) is
  structurally identical to scenario 1 plus a content-assertion surface
  that is flaky by nature (LLM output phrasing). Two smoke scenarios plus
  the harness cover everything the three live-claude scenarios covered
  with less flake.

**Rationale**

The chosen shape holds four properties simultaneously that no single
alternative achieves:

- **Production-path fidelity.** The daemon's spawn code path is not
  changed except for the minimal binary override (one line). Every other
  aspect of spawning — argv construction, env inheritance, process group
  setup, SIGTERM handling, exit detection — is exercised against a real
  subprocess, so the harness tests the real machinery, not a simulacrum.
- **Determinism at full AC coverage.** Every AC that reaches a worker
  tool call reduces to "run a specific script that calls these MCP tools
  in this order and exits with this code". Timing ACs reduce to integer
  seconds with the override envs. The only non-determinism left is kernel
  scheduling latency, which is bounded by the timing tolerances already
  in the AC language ("~2 seconds", "within 5 seconds").
- **Narrow surface for the fake.** One binary, one communication idiom
  (MCP-JSON-RPC). Scripts are data. Adding a scenario is "add one script
  + one Gherkin step + one registration in `suite_test.go`" — no changes
  to niwa source, no changes to the fake binary.
- **Honest live-claude coverage.** Two smoke tests catch exactly the
  regressions the harness cannot: MCP config wireup and bootstrap-prompt
  effectiveness. Both fail loudly when broken and pass silently when
  working; neither depends on LLM content.

**Alternatives Considered** (summary; per-sub-question rejections are in
the Chosen section above)

- **Command-template `NIWA_WORKER_SPAWN_COMMAND` with substitution.**
  Rejected: duplicates information already in argv/env, adds a
  templating DSL niwa has to maintain, creates a masking surface where
  template bugs hide spawn-path bugs.
- **Embedded Go fake with runtime registration.** Rejected: requires
  test-only production-code hooks, bypasses the real spawn path, cannot
  exercise multi-process lifecycle (SIGCHLD, `IsPIDAlive`, orphan
  adoption).
- **Fake writes directly to filesystem only.** Rejected: bypasses MCP
  tool handlers, which is the dominant failure class; leaves the actual
  tool surface untested in the scripted path.
- **Hybrid fake with MCP and direct-FS as equal peers.** Rejected: two
  idioms multiply maintenance cost; daemon-pause hooks (niwa-side)
  already handle the narrow set of ACs where pure-MCP is insufficient.
- **JSON-encoded backoff override.** Rejected: comma-list is
  dependency-free and trivially parseable; JSON adds a parser to
  production code for a test-only feature.
- **Millisecond-precision timing overrides.** Rejected for v1: integer
  seconds lands every AC inside 1-10 seconds; adding `_MS` variants later
  is additive.
- **Zero live-claude scenarios.** Rejected: misses MCP-loadability and
  bootstrap-prompt-effectiveness regressions that niwa owns.
- **Three-plus live-claude scenarios.** Rejected: third scenario adds
  flake (LLM content assertions) without covering a distinct niwa
  surface.

**Consequences**

*What changes:*

- A new `test/functional/cmd/niwa-test-worker/` binary, built by
  `make test-functional` as a test prerequisite. Reads
  `NIWA_TEST_WORKER_SCRIPT` to pick a script; scripts live in
  `test/functional/scripts/` as plain text or YAML.
- A new helper in `steps_test.go`:
  `setWorkerSpawnCommand(s *testState, scriptName string)` sets
  `envOverrides["NIWA_WORKER_SPAWN_COMMAND"]` to the test-worker binary
  and `envOverrides["NIWA_TEST_WORKER_SCRIPT"]` to the script.
- A new step family for timing overrides:
  `iSetTimingOverride(name, value)` sets the appropriate env var.
  Scenarios write `Given timing override NIWA_STALL_WATCHDOG_SECONDS=2`.
- Niwa's `mesh_watch.go` reads `NIWA_WORKER_SPAWN_COMMAND` at daemon
  startup; if non-empty and pointing at an existing executable, it
  replaces `claude` as the spawn binary. Other argv and env are
  unchanged.
- Niwa's daemon reads the four timing override envs at startup, parses
  them strictly (exit non-zero on malformed), and uses the overridden
  values instead of defaults.
- Niwa exposes deterministic pause hooks for daemon race-window
  testing: `NIWA_DAEMON_PAUSE_AT=<checkpoint>` blocks the daemon at a
  named point (`pre-rename`, `post-rename`, `pre-spawn`); the scenario
  lifts the pause via a `niwa mesh unpause` subcommand or a named pipe.
  These hooks are compiled in unconditionally but no-op when the env is
  unset.
- `DESIGN-channels-integration-test.md` is deleted. Its three live-claude
  scenarios in `mesh.feature` (lines 408-475) are replaced by the two
  smoke scenarios described above. The pre-registration step functions
  (`iSetUpCoordinatorSessionForInstance`, `iSetUpWorkerSessionForInstance`,
  `iRunClaudePFromInstanceRootWithSimulatedWorkerReply`) are deleted.
- `docs/guides/functional-testing.md` gets a new section "Test Harness
  for Worker Behaviors" describing the script format, the timing
  overrides, and the two residual `@channels-e2e` scenarios.

*What becomes easier:*

- Writing a new AC scenario for worker behavior: one script file plus a
  Gherkin step. No Go changes, no fake binary changes.
- Verifying timing behaviors: overrides collapse minutes-long waits into
  seconds; AC-L4 runs in ~4 seconds instead of 15 minutes plus grace.
- Debugging test failures: the fake logs every MCP call and every script
  step to stderr; failures point at a specific script line, a specific
  MCP response, or a specific state transition.
- Catching regressions in the real worker spawn path: the default
  `@critical` run exercises the real `NIWA_WORKER_SPAWN_COMMAND` wire-up
  with the test fake, so any change to how the daemon constructs the
  argv or env is caught.

*What becomes harder:*

- Adding a new MCP tool: both the real client (Claude Code) and the
  scripted fake need to know about it. Adding a script verb is the cost.
- Preventing script sprawl: without discipline, scripts accumulate
  one-per-scenario. Mitigation: script format includes reusable fragments
  (common prologues for "read task envelope", "check auth"), and scripts
  are reviewed in PRs the same way as Go code.
- Live-claude scenarios run only on schedule, so a regression in MCP
  wireup may sit undetected for up to 24 hours. Mitigation: the two
  smoke scenarios also run on PRs that touch channels installer or
  skill files.
- The two residual `@channels-e2e` scenarios still cost API credits and
  still have LLM-content non-determinism. Mitigation: smoke-only
  assertions (state transitions, tool-presence), not content matching.
<!-- decision:end -->
