---
status: Draft
problem: |
  niwa prepares a workspace for a single agent, Claude Code: it writes the
  context tree as CLAUDE.md files, binds ANTHROPIC_API_KEY, and resolves model
  categories to Claude model names. A developer who wants an interactive OpenAI
  Codex session in a niwa workspace has no supported path -- the context is
  written under a filename Codex does not read, and no knob selects Codex as the
  workspace's agent.
goals: |
  niwa can prepare a workspace for an interactive Codex session at parity of
  usability with preparing a Claude session today. A developer selects the agent
  (per-workspace default plus per-session override), and niwa materializes the
  same context tree as AGENTS.md, binds OPENAI_API_KEY, and resolves model
  categories to Codex names. Claude stays the default and its behavior is
  unchanged.
upstream: docs/briefs/BRIEF-interactive-codex-session.md
motivating_context: |
  A conformance check against codex-cli 0.144.3 confirmed the load-bearing
  facts: Codex ingests AGENTS.md from its working directory directly (no hook),
  its home and credentials (~/.codex, OPENAI_API_KEY) do not collide with
  Claude's (~/.claude, ANTHROPIC_API_KEY), and no config file is required for a
  session to see its context and work. This PRD scopes the launch slice: the
  keystone that later Codex-support features build on.
---

# PRD: interactive Codex session in a niwa workspace

## Status

Draft

The downstream DESIGN owns the architecture: how the agent selector is modeled,
how the output-filename-by-agent seam is introduced, and how per-agent model
resolution dispatches. This PRD stops at requirements.

## Problem Statement

niwa's purpose is to prepare a workspace so an AI coding agent can do useful work
in it. Today every preparation step assumes one agent, Claude Code. The context
tree (root, group, repo, worktree) is materialized to files named `CLAUDE.md` and
`CLAUDE.local.md`; the secret split binds `ANTHROPIC_API_KEY`; and the portable
model categories (fast/balanced/powerful) resolve to Claude model names.

A developer who wants to run an interactive OpenAI Codex session in a
niwa-managed workspace is unsupported on two counts. First, there is no way to
tell niwa that a workspace targets Codex -- niwa has no concept of a selectable
agent. Second, even a fully materialized workspace is invisible to Codex, because
Codex reads its context from `AGENTS.md`, not `CLAUDE.md`. The developer is left
to hand-rename context files and hand-export credentials that niwa already knows
how to produce for Claude.

This affects any developer who runs Codex -- by quota, preference, or task fit --
in a workspace niwa manages. The cost is a manual, error-prone workaround for a
workspace-preparation job niwa exists to automate. It matters now because Codex
runs a niwa-prepared workspace with no additional machinery once the context
lands under the right filename: Codex ingests `AGENTS.md` from its working
directory on its own, and its home and credentials do not collide with Claude's.
The blocker is entirely on niwa's side -- it prepares the wrong shape and offers
no way to ask for another.

## Goals

- A developer can declare which agent a workspace is prepared for, and niwa
  prepares it correctly for that choice, at the same effort as a Claude
  workspace today.
- Selecting Codex materializes the existing context tree as `AGENTS.md` in the
  same locations, binds `OPENAI_API_KEY`, and resolves model categories to Codex
  names -- after which the developer runs `codex` and it sees its context and
  does real work.
- Both agents coexist on one host without collision.
- Claude remains the default; a workspace that does not select an agent behaves
  exactly as it does today.

This feature is deliberately the smallest usable slice. Its architectural shape
-- a session-global agent discriminator plus an output-filename-by-agent seam --
warrants a DESIGN doc before implementation, and several implementation choices
are left open for that DESIGN to settle (see Decisions and Trade-offs and Open
Questions). The forthcoming DESIGN-interactive-codex-session document owns those
choices.

## User Stories

- As a workspace maintainer whose team runs Codex, I want to set Codex as the
  workspace's default agent once, so that every `niwa apply` prepares the
  workspace for Codex without me restating the choice.

- As a developer whose Claude quota is exhausted, I want to override the agent to
  Codex for a single session, so that I can keep working under Codex without
  changing the workspace default my teammates rely on.

- As a developer who runs both agents on one machine, I want each workspace
  prepared for whichever agent it targets, so that selecting Codex for one
  workspace never disturbs the Claude credentials or context another workspace
  prepared.

- As a developer who has always used niwa with Claude, I want a workspace that
  selects no agent to behave exactly as it does today, so that adding Codex
  support does not change my existing setup.

## Requirements

### Functional -- agent selection

- **R1.** niwa SHALL support a per-workspace concept of a selected agent, with at
  least the values `claude` and `codex`. `claude` is the default when nothing is
  selected.

- **R2.** niwa SHALL let a workspace declare a default agent that persists across
  runs, so the choice does not have to be restated per invocation.

- **R3.** niwa SHALL let a single session override the workspace default agent,
  affecting only that session and leaving the persisted workspace default
  unchanged.

- **R4.** The selected agent SHALL be resolved once per session as a
  session-global value -- a single discriminator for the whole workspace
  preparation, not a per-repo value merged across the existing configuration
  cascade. The selection mechanism SHALL NOT be added as another field in the
  per-repo `[claude]` override cascade. (The concrete modeling -- which existing
  session-global precedent it mirrors and the exact override surface -- is an
  open architectural decision for the DESIGN; see Open Questions.)

### Functional -- context materialization

- **R5.** The output filename niwa writes the materialized context tree to SHALL
  be determined by the selected agent. The output filename is distinct from the
  content source (the directory and source files niwa reads context from), which
  SHALL NOT change based on the selected agent.

- **R6.** When the selected agent is `codex`, niwa SHALL materialize the same
  layered context tree it writes today -- at the workspace root, group, repo, and
  worktree levels -- under the filename `AGENTS.md`, and the repo/worktree-level
  local variant under the corresponding Codex-appropriate local filename, in the
  same locations it writes the Claude filenames today.

- **R7.** When the selected agent is `claude` (including the default,
  unselected case), niwa SHALL write the context tree exactly as it does today,
  byte-for-byte, with no change to filenames, locations, or content.

- **R8.** The content that niwa materializes (the expanded template body) SHALL
  be identical regardless of the selected agent; only the output filename differs
  by agent. (Whether the two agents should ever receive materially different
  context bodies is out of scope for this slice; see Out of Scope.)

### Functional -- credentials

- **R9.** niwa SHALL bind `OPENAI_API_KEY` into a prepared workspace through the
  same secret-binding mechanism it uses for `ANTHROPIC_API_KEY`, such that a
  workspace can carry both keys.

- **R10.** Binding one agent's credential SHALL NOT disturb the other's. Both
  agents SHALL be able to coexist on one host, using their separate homes
  (`~/.claude` and `~/.codex`) and separate credentials.

### Functional -- model categories

- **R11.** When the selected agent is `codex`, niwa SHALL resolve the portable
  model categories (`fast`, `balanced`, `powerful`) to Codex model names, through
  the existing model-resolution seam. When the selected agent is `claude`,
  category resolution SHALL be unchanged.

- **R12.** An unrecognized model value SHALL continue to be forwarded unchanged
  (with a warning), preserving today's behavior of not gatekeeping model names,
  independent of the selected agent. (The concrete Codex model-name values and
  the resolution-dispatch shape are an open architectural decision for the
  DESIGN; see Open Questions.)

### Non-functional

- **R13.** Backward compatibility: existing workspaces and existing tests that do
  not select an agent SHALL continue to pass and behave identically. The default
  path is Claude and is unchanged.

- **R14.** The change SHALL be limited to the workspace-preparation surface. niwa
  SHALL NOT gain code that launches, spawns, or exec's an agent session as part
  of this feature (see Out of Scope).

- **R15.** The agent value SHALL be validated: an unknown agent value SHALL be
  rejected with a clear error naming the accepted values, rather than silently
  materializing an unusable workspace.

## Acceptance Criteria

- [ ] With no agent selected, `niwa apply` writes the workspace/group/repo/
      worktree context tree as `CLAUDE.md` / `CLAUDE.local.md`, byte-for-byte
      identical to today (R7, R13).
- [ ] A workspace can declare a default agent of `codex`, and after `niwa apply`
      the context tree is written as `AGENTS.md` (and the repo/worktree local
      variant) at the workspace root, each group, each repo, and applied
      worktrees -- in the same locations the Claude filenames are written today
      (R1, R2, R6).
- [ ] A single session can override the agent to `codex` while the persisted
      workspace default remains `claude`; the override affects only that
      session's preparation and does not rewrite the workspace default (R3).
- [ ] The selected agent is resolved once per session and is not exposed as a
      per-repo field in the `[claude]` override cascade (R4) -- verified by the
      selector living in a session-global location, not in `ClaudeOverride`.
- [ ] The materialized context body is identical whether the agent is `claude` or
      `codex`; only the output filename differs (R5, R8).
- [ ] A prepared workspace can carry both `OPENAI_API_KEY` and `ANTHROPIC_API_KEY`
      bound through the same mechanism, and binding one does not remove or corrupt
      the other (R9, R10).
- [ ] With agent `codex`, resolving the `fast` / `balanced` / `powerful`
      categories yields Codex model names; with agent `claude`, category
      resolution is unchanged (R11).
- [ ] An unrecognized model value is still forwarded unchanged with a warning
      under either agent (R12).
- [ ] An unknown agent value is rejected with an error that names the accepted
      values (`claude`, `codex`) (R15).
- [ ] No new code path launches, spawns, or exec's an agent session (R14) --
      verified by inspection: the feature only affects materialization,
      selection, secret binding, and model resolution.
- [ ] The existing niwa test suite (`go test ./...` and functional tests) passes
      with the change in place (R13).

## Out of Scope

- **Launching or exec'ing the session.** niwa prepares the workspace; the
  developer runs the agent binary. niwa does not spawn `claude` today and will
  not spawn `codex`. Background dispatch, `codex exec`, and session attach are
  deferred to later work.
- **Hooks, provisioning, and reaping for Codex.** Codex reads `AGENTS.md` from
  its working directory directly, so a bare interactive session needs no
  SessionStart hook, auto-provisioning, or liveness reaper. Ephemeral-session
  provisioning for Codex is deferred to later work.
- **A full agent-neutral config table.** This PRD scopes the launch slice: enough
  selection and materialization to prepare a Codex session. A complete
  agent-neutral config layer -- a general `[agent]`/`[codex]` table, generalized
  content-source directories, host-coexistence config beyond the secret binding
  -- is deferred to later work.
- **Agent-specific context bodies.** This slice materializes the same content
  body under a different filename. Whether Codex should ever receive different
  context than Claude is deferred to later work.
- **Claude behavior changes.** The incumbent path is not refactored beyond the
  seams the new agent needs (the output-filename seam and the per-agent model
  map). Claude remains the default and its materialization, secret binding, and
  model resolution are otherwise untouched.

## Decisions and Trade-offs

- **The active agent is a session-global discriminator, not a per-repo mergeable
  value.** A niwa session runs one agent for the whole workspace, so the agent is
  a single choice resolved once, not a value that different repos could set
  differently and merge. This closes the upstream BRIEF's first open question at
  the requirements level (R4). The concrete storage/override modeling -- mirroring
  the workspace-root state flag precedent, the global-config-plus-per-invocation-
  flag precedent, or another shape -- is left to the DESIGN.

- **Output filename is a function of the selected agent, separate from the content
  source.** Rather than duplicating the content tree or branching materialization
  wholesale, the requirement isolates the single thing that changes -- the output
  filename -- behind an agent-driven seam, keeping the content pipeline shared
  (R5, R8). The trade-off is that agent-specific content bodies are explicitly not
  supported in this slice; that is an accepted limitation for the launch slice.

- **Reuse the existing secret split and model-resolution seam rather than build
  agent-neutral config now.** The secret-binding mechanism and the model-category
  map already exist and are close to agent-neutral; this slice extends them for a
  second agent rather than introducing a new config layer (R9, R11). The full
  agent-neutral config layer is deferred (Out of Scope).

## Open Questions

- Which existing session-global precedent the agent selector should mirror, and
  the exact per-session override surface (a flag, an environment variable, or
  both), is deferred to the DESIGN. The requirement fixes only that the selector
  is session-global and not a per-repo cascade field (R4).
- The concrete Codex model-name values that `fast` / `balanced` / `powerful`
  resolve to, and whether resolution dispatches on the selected agent inside the
  existing seam or through a per-agent map, is deferred to the DESIGN (R11, R12).
- The exact Codex local-context filename (the `CLAUDE.local.md` analog for repo
  and worktree levels) is deferred to the DESIGN, which will confirm the filename
  Codex reads for repo-local context.
