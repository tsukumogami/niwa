# Exploration Decisions: session-attach

Auto-mode decisions made during exploration. Each entry: what was decided, why,
and how it shapes downstream artifacts.

## Round 1

- **Topic = `session-attach`**: derived from issue #117 title; kebab-case for branch and wip artifact naming.
- **7 round-1 leads, equal weight**: per user direction "spend equal amount with agents investigating each thoroughly". One agent per major open question in the issue body, plus the adversarial-demand lead required by the `needs-prd` label.
- **Adversarial demand finding documented as a known caveat, not a stop-gate**: round 1 demand-validation agent reported "demand not validated" (single-author proposal, zero corroborating asks). User direction is to proceed; PRD will surface this as a risk/assumption rather than route to a "don't pursue" outcome.

## Round 2

- **5 round-2 agents, all UX-focused per user direction**: full-breadth UX investment.
  - `ux-cli-tone`: niwa CLI tone audit
  - `ux-peer-patterns`: peer-tool human-takeover patterns
  - `ux-scenarios`: 7 concrete scenario walkthroughs
  - `ux-mcp-surface`: MCP-tool surface review
  - `transcript-failure-modes`: empirical drill-down on the highest-stakes round-1 finding
- **Round 2 agents instructed to peek at PR #115**: per user direction, to avoid divergence with the in-flight mesh-reliability design (DESIGN-niwa-mesh-reliability covering #108/#109/#111/#112).

## Convergence (Phase 3, --auto)

- **Verb pair = `niwa session attach <id>` + `niwa session detach <id> [--force]`**: tmux + docker precedent. `attach` for normal flow, `detach` ONLY as operator escape hatch for stale locks (auto-detach on claude exit is the normal release path).
- **State-model field shape = nested `attach` sub-object on `SessionLifecycleState`**: mirrors PR #115's `daemon` sub-object precedent. Both features add parallel runtime/availability axes alongside `status`.
- **No `SessionLifecycleState.V` bump**: PR #115 sets the no-bump precedent for additive sub-objects under V:1. Following its lead avoids contradicting an in-flight design choice.
- **Lock = `flock(<worktree>/.niwa/attach.lock)` + sentinel `<worktree>/.niwa/attach.state`**: direct precedent in `acquireDaemonPIDLock`. Implicit release via fd-lifetime.
- **`niwa session attach` is a long-running parent process, NOT exec-replacement**: required so the flock survives until the human's claude session ends. Pattern: `cmd.Run()` with stdio inherited from os.Stdin/Stdout/Stderr, cmd.Dir = worktree path.
- **Daemon coordination = `TerminateDaemon` + `EnsureDaemonRunning`**: subsumes "wait for running worker" + "queue while attached" + "deliver on detach" via the existing inbox-replay catch-up path. No new daemon code needed; only the orchestration in the attach command.
- **Pre-attach validation: refuse on `Status != active`**: forensic attach on `ended` is physically infeasible (worktree gone, branch deleted by destroy). Refuse with clear error pointing at the destroy-removed-the-worktree fact.
- **Worktree state on detach: warn loudly, never auto-clean**: precedent from `branch_warning` on destroy. The `LossKind` taxonomy in `internal/workspace/scan.go` provides the shape of the warning.
- **Multi-user: declare single-UID by reference**: cite `DESIGN-cross-session-communication.md`'s "same-UID cooperative trust" boundary. No new safeguards in the attach feature.
- **Coordinator awareness: polling-only**: filesystem-visible state file means coordinator sees the change on next `niwa_list_sessions` poll. New `SESSION_ATTACHED` MCP error code on `niwa_destroy_session` when `--force` isn't set. No push channel, no changes to `niwa_delegate`/`niwa_ask`/`niwa_send_message`.
- **Transcript validation: pre-flight stat for UX, not safety**: `claude --resume <uuid>` already fails loudly (exit 1) on every failure mode. niwa pre-flight stat exists solely to emit niwa-shaped error messages with three distinct cases (no-conv-id, transcript-missing, transcript-empty).
- **NEVER use `claude --continue`**: only `--continue` silently degrades to a fresh session (exit 0). `--resume <uuid>` always fails loudly.
- **Path encoding: `s/[^A-Za-z0-9]/-/g`** (NOT base64url as round 1 reported). Empirical correction.
- **AVAILABILITY column values: `available` / `attached` / `stale`**: matches niwa's existing lowercase kebab-case state vocabulary. CLI-tone-audit prescription.
- **Default `niwa session list` sort: attached first, then status, then creation-time descending**: surfaces operator's hot question ("is anyone in there?") at the top.
- **`niwa session list` flagless default flips to lifecycle view**: completes the deprecation that `mesh list` started; required for the issue's UX sketch to make sense.
- **Filter additions: `--attached` / `--available`**: orthogonal to existing `--repo` and `--status`. Coordinated with PR #115's `--daemon-alive`.
- **MCP surface change is minimal and additive**: `attach` sub-object on `niwa_list_sessions` output (computed at query time from `attach.state`); new `SESSION_ATTACHED` error from `niwa_destroy_session`. User's "no new MCP tools" guidance holds.
- **Exit code policy: propagate claude's exit, cap at 125**: reserves 126/127/128+ for shell semantics.
- **`--force` semantic asymmetry must be called out in PRD**: on `attach`, `--force` SIGTERMs the worker. On `detach`, `--force` steals the lock from another holder. PRD calls this out explicitly because symmetry instinct misleads operators.
- **SSH-disconnect-with-survivor: SIGHUP-handler-only + `--force` escape hatch**: no heartbeat in v1.
- **Recommended artifact: PRD**: per crystallize-framework. Requirements are the unsettled axis; technical approach is grounded enough that the design doc will mostly recapitulate code prescriptions already captured.
- **Two rounds suffice**: per user direction "Two rounds, deeper" + the empirical evidence that round 2 closed all gaps from round 1. No round 3.

## Pipeline-level decisions

- **Auto mode for the entire pipeline**: explore → /shirabe:prd → /shirabe:design → /shirabe:plan (single-pr) → /shirabe:work-on.
- **Single branch, single PR**: all work lands on `docs/session-attach`. Draft PR opens after the PRD is committed.
- **v1 scope = full**: attach + `niwa session detach <id> --force` + `AVAILABILITY` column. One coherent PR.
- **Blocker policy**: if I hit a hard blocker, run /shirabe:decision framework, document the chosen path, keep going.
- **Done bar**: unit tests + `@critical` Gherkin functional test + docs updated + go vet clean + I run scenarios locally + observed UX documented.
- **Out-of-scope**: don't fix #108/#109/#111/#112 directly. PR #115 is fixing them.
