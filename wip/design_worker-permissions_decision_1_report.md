<!-- decision:start id="worker-session-permissions" status="assumed" -->
### Decision: Worker session permission scope

**Context**

niwa's mesh daemon spawns worker sessions to handle delegated tasks. Workers always run headless (`claude -p`), with no human present to answer approval dialogs. The current spawn uses `--permission-mode=acceptEdits`, which auto-approves file writes but not shell tool calls. Any worker that needs to run `gh pr create`, `go test`, or `git push` hits an approval dialog that never gets answered and the task transitions to `abandoned`.

The coordinator's permission mode is already expressed in `<instanceRoot>/.claude/settings.local.json` as `permissions.defaultMode` (`bypassPermissions` or `askPermissions`), written by `materialize.go` from the user's niwa config (`permissions = "bypass"` or `permissions = "ask"`). The daemon has `instanceRoot` available in its `spawnContext` struct and can read that file at spawn time with negligible overhead.

`daemon.go` already treats the `acceptEdits` blast radius as a security-relevant quantity: it SIGKILLs all worker process groups before SIGTERMing the daemon during teardown, specifically to bound the exfiltration window of a worker with wide permissions. This decision uses the same principle.

**Assumptions**

- `--permission-mode=bypassPermissions` is a valid Claude CLI argument (same spelling as `settings.local.json`). If the actual CLI flag is `--dangerously-skip-permissions`, the spawn code should use that instead; the logic is identical.
- The curated Bash patterns (`Bash(gh *)`, `Bash(git *)`, `Bash(go test *)`, `Bash(go build *)`, `Bash(make *)`) cover the primary worker shell operations for the current niwa user base. Users with workflows requiring additional tools (`cargo`, `npm`, `docker`, etc.) would need those patterns added to the fallback list.
- Workers always run as the same OS user as the daemon (same-UID trust model). No cross-user privilege concerns apply.
- `<instanceRoot>/.claude/settings.local.json` is reliably present and readable at spawn time, since the daemon starts only after `niwa apply` has run.

**Chosen: B — Inherit coordinator mode**

Workers read `<instanceRoot>/.claude/settings.local.json` at spawn time and derive their `--permission-mode` from the coordinator's `permissions.defaultMode`:

- If `defaultMode == "bypassPermissions"`: spawn with `--permission-mode=bypassPermissions`
- Otherwise (any other value, absent key, or parse error): spawn with `--permission-mode=acceptEdits` plus an extended `--allowed-tools` list that appends curated Bash patterns (`Bash(gh *)`, `Bash(git *)`, `Bash(go test *)`, `Bash(go build *)`, `Bash(make *)`) to the existing niwa MCP tool list

The curated Bash patterns for the fallback branch live in `internal/mcp/allowed_tools.go` alongside `ClaudeAllowedTools`, so both the daemon and functional-test harness reference the same source.

No user-facing configuration changes are required. The behavior upgrades automatically when the daemon next restarts.

**Rationale**

The user's existing permission setting is the right authority signal for worker permissions. A user who configured `permissions = "bypass"` has already accepted that their coordinator session runs shell commands without approval; restricting workers to a curated list while the coordinator has full bypass creates an inconsistent and surprising model. Conversely, a user who configured `permissions = "ask"` or left permissions unconfigured has not accepted a full bypass, and workers should not silently expand past that intent.

Full bypass (Option A) was rejected because the codebase already treats blast radius as security-load-bearing (daemon teardown SIGKILL ordering), and unconditional full bypass would apply maximum blast radius to all users regardless of their stated preference. Per-delegation specification (Option D) was rejected because it does not fix the immediate bug — existing coordinator sessions have no `allowed_tools` in their `niwa_delegate` calls and would remain broken until every coordinator skill is updated. Curated preset alone (Option C) was rejected because it ignores the user's permission signal and requires ongoing maintenance as the tool ecosystem grows, while providing no additional safety for bypass-configured users.

Option B resolves the immediate bug for bypass-configured users (primary power users), provides a functional fallback for ask/unconfigured users via the curated preset, adds no new configuration surface, and keeps worker blast radius bounded by the same threshold the user already chose for their coordinator session.

**Alternatives Considered**

- **Full bypass (A)**: Workers always receive `bypassPermissions`. Rejected because it silently maximizes blast radius for all users regardless of their coordinator permission setting, inconsistent with the codebase's existing security posture around worker permissions.
- **Curated preset (C)**: Workers always use `acceptEdits` plus a fixed list of Bash patterns. Rejected as the sole mechanism because it ignores the user's bypass signal (bypass users get unnecessarily constrained workers) and requires ongoing pattern-list maintenance as the tool ecosystem grows. The curated list is retained as the fallback branch within Option B.
- **Per-delegation specification (D)**: `niwa_delegate` gains an `allowed_tools` field; workers get only what the coordinator explicitly grants. Rejected because it does not fix the immediate bug without updating all coordinator skills, adds schema complexity, and shifts cognitive burden onto coordinators that are typically AI agents following a fixed skill prompt.

**Consequences**

- Workers spawned under a bypass-configured coordinator gain full permission parity with the coordinator session. The daemon teardown SIGKILL ordering (already present) continues to bound the exfiltration window on teardown.
- Workers spawned under an ask/unconfigured coordinator gain access to the curated Bash pattern set; they can complete common dev tasks (git, gh, go, make) without interactive approval.
- The spawn code acquires a new I/O dependency (one JSON file read per spawn). On failure (file absent or malformed), it falls back to the curated preset — no spawn is blocked.
- The curated Bash pattern list in `allowed_tools.go` becomes a maintained artifact. New tool patterns can be added without changing the permission-mode logic.
- Workers with bypass permissions can run arbitrary shell commands. Prompt-injection into a task body remains a residual risk (unchanged from the coordinator's existing exposure). This is a known PRD limitation.
<!-- decision:end -->
