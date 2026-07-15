---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-interactive-codex-session.md
milestone: "Interactive Codex session in a workspace"
issue_count: 6
---

# PLAN: interactive Codex session in a niwa workspace

## Status

Active

Single-pr plan. All six issues land on one shared branch and one draft PR. The
Claude/default path stays byte-for-byte unchanged across every issue.

## Scope Summary

Add OpenAI Codex as a selectable agent in niwa alongside Claude Code: a
session-global agent selector, an agent-aware output-filename seam that
materializes `AGENTS.md` at the niwa-owned (workspace-root and group) levels
under Codex, an `OPENAI_API_KEY` binding, and an agent-aware model resolver as
keystone groundwork.

## Decomposition Strategy

**Horizontal, foundation-first.** The components have stable interfaces (the
`Agent` type is the seam every other unit consumes), and one component gates the
rest. Issue 1 lands the pure `internal/agent` leaf package that defines the seam;
Issue 2 makes the agent selectable; Issues 3 and 4 apply the seam in the
materialization pipeline; Issues 5 and 6 extend the two independent seams
(secret table, model resolver). Every issue carries a fail-safe zero-value
contract (an unset agent behaves as Claude), so partial progress on the shared
branch never breaks the default path. A walking-skeleton shape was not chosen
because the units are cleanly layered behind the `Agent` interface rather than a
single runtime pipeline that needs an end-to-end thread proven first.

## Issue Outlines

### Issue 1: feat(agent): add the internal/agent leaf package

**Goal**: Add a dependency-free `internal/agent` package that owns the `Agent`
discriminator, its validation, its filename mapping, and session resolution.

**Acceptance Criteria**:
- [ ] `Agent` string type with `AgentClaude` ("claude") and `AgentCodex` ("codex")
      constants (PRD R1).
- [ ] `ParseAgent(s string) (Agent, error)`: empty string returns `AgentClaude`;
      `claude`/`codex` return the matching constant; any other value returns an
      error naming the accepted set `claude, codex` (PRD R15).
- [ ] `RootContextFileName()` returns `CLAUDE.md` for claude (and the zero value)
      and `AGENTS.md` for codex; `LocalContextFileName()` returns `CLAUDE.local.md`
      for claude (and zero value) and `AGENTS.md` for codex (PRD R5, R6).
- [ ] `WritesRepoLevelContext()` returns true for claude (and zero value), false
      for codex (PRD R6a).
- [ ] The zero value `Agent("")` behaves as `AgentClaude` in every accessor
      (fail-safe backward-compat contract).
- [ ] `ResolveAgent(flag, env, workspaceDefault string) (Agent, error)` applies
      precedence flag > env > workspaceDefault > `claude`, validating the chosen
      value via `ParseAgent` (PRD R3, R4).
- [ ] The package imports nothing else in the module tree (true leaf; avoids the
      config->workspace import cycle).
- [ ] Unit tests cover parse (valid/empty/unknown), each accessor for both agents
      and the zero value, and the full `ResolveAgent` precedence matrix.
- [ ] `go test ./...` and `go vet ./...` pass.

**Dependencies**: None

**Type**: code
**Files**: `internal/agent/agent.go`, `internal/agent/agent_test.go`

### Issue 2: feat(config,cli): select the agent (workspace default + flag/env override)

**Goal**: Make the agent selectable via a workspace-config default and a
per-session flag/env override, resolved once per session.

**Acceptance Criteria**:
- [ ] `WorkspaceMeta` gains `DefaultAgent string` mapped to TOML
      `[workspace].default_agent`; it is a raw string (NOT typed `agent.Agent`) so
      `internal/config` does not import `internal/agent` (no cycle) (PRD R2, R4).
- [ ] A `--agent` flag on the apply/create entry commands and a `NIWA_AGENT`
      environment variable feed `ResolveAgent`; the resolved agent is computed once
      per invocation (PRD R3).
- [ ] Precedence is flag > `NIWA_AGENT` > `[workspace].default_agent` > `claude`,
      in either override direction (codex over a claude default and claude over a
      codex default) (PRD R3).
- [ ] An unknown agent value (from any source) fails with a clear error naming
      `claude, codex` (PRD R15).
- [ ] The persisted workspace default is unchanged by a per-session override (PRD
      R3).
- [ ] Tests: `[workspace].default_agent` decodes; precedence end-to-end for each
      source and both directions; unknown-value error.
- [ ] Default/unselected behavior is unchanged; `go test ./...` and `go vet` pass.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/config/config.go`, `internal/cli/apply.go`, `internal/cli/create.go`

### Issue 3: feat(workspace): agent-aware context filename at the niwa-owned levels

**Goal**: Thread the resolved agent through the materialization entry points and
replace the hardcoded `CLAUDE.md` output literals at the workspace-root and group
levels with the agent-aware filename accessor.

**Acceptance Criteria**:
- [ ] `RootMaterializeOptions` gains an `Agent agent.Agent` field beside
      `EphemeralSessionMode`; `MaterializeWorkspaceRoot` uses
      `opts.Agent.RootContextFileName()` where it writes `rootClaudeFile` today.
- [ ] The instance-root (`content.go` workspace) and group write sites use
      `agent.RootContextFileName()` instead of the `"CLAUDE.md"` literal (PRD R5,
      R6).
- [ ] The resolved agent is carried to every materializing entry point: `apply`,
      `create`, `init`, `reset`, the `instance from-hook` path, and the worktree
      lifecycle. A path that does not set the agent uses the zero value (= claude)
      and is unchanged.
- [ ] Under codex, the workspace-root and group files are written as `AGENTS.md`;
      under claude (and the zero value) they are `CLAUDE.md`, byte-for-byte
      identical to today (PRD R6, R7, R13).
- [ ] `content_test.go` and `root_materializer_test.go` are parameterized by
      agent and assert both the codex `AGENTS.md` output and the unchanged claude
      output, including a zero-value-agent case.
- [ ] No launch/exec code is added; `go test ./...` and `go vet` pass.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>

**Type**: code
**Files**: `internal/workspace/root_materializer.go`, `internal/workspace/content.go`, `internal/workspace/apply.go`

### Issue 4: feat(workspace): skip repository/worktree context writes under Codex

**Goal**: Under Codex, write no repository- or worktree-level context file, so no
git working tree is dirtied and no repository's own committed `AGENTS.md` is
overwritten.

**Acceptance Criteria**:
- [ ] `InstallRepoContent(To)` and the worktree context layer early-return without
      writing when `!agent.WritesRepoLevelContext()` (PRD R6a).
- [ ] Under codex, no `CLAUDE.local.md` and no in-repo `AGENTS.md` is written at
      the repository or worktree levels; the repository git working tree stays
      clean (no untracked or modified files introduced by niwa).
- [ ] A repository that ships its own committed `AGENTS.md` is not overwritten or
      modified under codex (PRD R6a).
- [ ] Under claude (and the zero value) the repository/worktree writes are
      byte-for-byte unchanged (PRD R7, R13).
- [ ] Tests assert the codex skip (no repo-level file, clean tree) and the
      unchanged claude behavior; `go test ./...` and `go vet` pass.

**Dependencies**: Blocked by <<ISSUE:3>>

**Type**: code
**Files**: `internal/workspace/content.go`, `internal/workspace/worktree_content.go`

### Issue 5: feat(config): bind OPENAI_API_KEY alongside ANTHROPIC_API_KEY

**Goal**: Demonstrate and document that `OPENAI_API_KEY` binds through the
existing agent-neutral secret table alongside `ANTHROPIC_API_KEY`, with no
mechanism change.

**Acceptance Criteria**:
- [ ] The scaffold gains a commented `OPENAI_API_KEY` example next to the existing
      `ANTHROPIC_API_KEY` example in the secrets table (PRD R9).
- [ ] A config round-trip test (mirroring the existing `ANTHROPIC_API_KEY` test)
      asserts `OPENAI_API_KEY` decodes and resolves as an ordinary secret row, and
      that both keys coexist in one workspace without one disturbing the other
      (PRD R9, R10).
- [ ] The relevant guide documents binding `OPENAI_API_KEY` and Claude/Codex host
      coexistence (`~/.claude` + `~/.codex`).
- [ ] No change to the Claude-Code-Remote API-key special case; `go test ./...`
      and `go vet` pass.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/workspace/scaffold.go`, `internal/config/vault_test.go`

### Issue 6: feat(cli): make the model-category resolver agent-aware

**Goal**: Make the dispatch model resolver agent-aware as keystone groundwork,
keeping the Claude resolution byte-identical.

**Acceptance Criteria**:
- [ ] `modelCategories` and `knownModelNames` (and the "unrecognized model"
      warning vocabulary) become agent-scoped: claude keeps
      `fast/balanced/powerful -> haiku/sonnet/opus` and its known names; codex gets
      a parallel versionless Codex model-name map and known-name set (adjustable
      defaults) (PRD R11).
- [ ] `resolveDispatchModel` takes the selected agent and resolves against that
      agent's sets; the existing call site resolves under `claude` and produces
      byte-identical output to today (PRD R11, R13).
- [ ] An unrecognized model value is still forwarded unchanged with a warning
      under either agent (PRD R12).
- [ ] The resolver is verified at the unit level (codex resolution yields Codex
      names distinct from Claude; claude resolution is unchanged); no launched
      session and no new consumer is added (PRD R11, R14).
- [ ] `go test ./...` and `go vet` pass.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/cli/dispatch_model.go`, `internal/cli/dispatch.go`, `internal/cli/dispatch_model_test.go`

## Implementation Sequence

**Critical path:** Issue 1 -> Issue 2 -> Issue 3 -> Issue 4. This is the
materialization spine: the seam type, then selection, then applying the seam at
the niwa-owned levels, then the Codex repo/worktree skip.

**Parallelizable once Issue 1 lands:** Issues 5 (secret binding) and 6 (model
resolver) depend only on Issue 1 (Issue 6 does not even strictly need the `Agent`
type if it defines its own, but reusing `internal/agent` keeps one source of
truth) and can be implemented alongside the Issue 3/4 spine.

**Foundation:** Issue 1 gates everything and is the only initially-ready issue.

All six issues land on one shared branch and one draft PR. Every issue keeps the
Claude/default path byte-for-byte unchanged and adds no launch/exec code, no new
external dependency, and keeps `go test ./...` and `go vet ./...` green.
