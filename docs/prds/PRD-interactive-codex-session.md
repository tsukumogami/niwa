---
status: Done
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

Done

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

This feature is deliberately the smallest usable slice. In particular, under
Codex the `AGENTS.md` tree is materialized only at the niwa-owned non-repository
levels (workspace-root and group); repository- and worktree-level Codex context is
deferred (see Out of Scope). Its architectural shape -- a session-global agent
discriminator plus an output-filename-by-agent seam -- warrants a DESIGN doc
before implementation, and several implementation choices are left open for that
DESIGN to settle (see Decisions and Trade-offs and Open Questions). The
forthcoming DESIGN-interactive-codex-session document owns those choices.

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
  unchanged. The override SHALL be surfaced through **both** a command-line flag
  (e.g. `--agent <name>`) and an environment variable (e.g. `NIWA_AGENT`), so a
  developer can switch per-invocation or per-shell. When both are supplied, the
  flag wins; when neither is supplied, the workspace default applies; when there
  is no workspace default, the agent is `claude`. (The exact flag/env spelling and
  the resolution wiring are a DESIGN detail; this requirement fixes that both
  surfaces exist and their precedence order.)

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

- **R6.** When the selected agent is `codex`, niwa SHALL materialize its context
  content under the filename `AGENTS.md` at the niwa-owned, non-repository levels
  of the tree -- the workspace-root and group levels, the same directories where
  it writes `CLAUDE.md` today. These directories are niwa-owned and are not git
  repositories, so an `AGENTS.md` written there cannot collide with a repository's
  own committed `AGENTS.md` nor dirty any git working tree. A `codex` session
  launched at the workspace/instance root reads this `AGENTS.md` at its working
  directory directly (the verified Codex ingestion behavior). Repository- and
  worktree-level Codex context is deliberately deferred (see Out of Scope and the
  Decisions and Trade-offs rationale).

- **R6a.** When the selected agent is `codex`, niwa SHALL NOT write the
  repository- and worktree-level context files (the `CLAUDE.local.md` files it
  writes for Claude): writing a Claude-named local file under Codex would be inert
  (Codex does not read it), and writing an `AGENTS.md` inside a cloned repository
  is deferred because it requires collision handling with a repository's own
  committed `AGENTS.md` and git-clean coverage (see Out of Scope). Under Codex the
  repository/worktree levels are simply left unwritten in this slice.

- **R7.** When the selected agent is `claude` (including the default,
  unselected case), niwa SHALL write the context tree exactly as it does today,
  byte-for-byte, with no change to filenames, locations, or content.

- **R8.** The content that niwa materializes (the expanded template body) SHALL
  be identical regardless of the selected agent; only the output filename differs
  by agent. (Whether the two agents should ever receive materially different
  context bodies is out of scope for this slice; see Out of Scope.)

- **R8a.** Each apply SHALL materialize a complete, fresh context tree for the
  currently-selected agent, overwriting any prior tree niwa wrote for that same
  agent, so the active agent never reads a stale niwa-written tree. Context trees
  for different agents MAY coexist in a workspace (a `CLAUDE.md` tree and an
  `AGENTS.md` tree side by side is not an error). niwa does NOT remove a
  previously-selected agent's tree when the agent changes between applies (see
  Known Limitations); the contract is that a developer applies under the agent
  they are about to run, so the just-applied tree is always fresh.

### Functional -- credentials

- **R9.** niwa SHALL bind `OPENAI_API_KEY` into a prepared workspace through the
  same secret-binding mechanism it uses for `ANTHROPIC_API_KEY`, such that a
  workspace can carry both keys.

- **R10.** Binding one agent's credential SHALL NOT disturb the other's. Both
  agents SHALL be able to coexist on one host, using their separate homes
  (`~/.claude` and `~/.codex`) and separate credentials.

### Functional -- model categories

- **R11.** When the selected agent is `codex`, niwa's model-resolution seam SHALL
  resolve the portable model categories (`fast`, `balanced`, `powerful`) to Codex
  model names; when the selected agent is `claude`, category resolution SHALL be
  unchanged. This requirement wires the resolver to be agent-aware as keystone
  groundwork -- the seam's only consumer today is the background-dispatch launcher,
  which is out of scope for this slice (see Out of Scope), so delivery of the
  resolved Codex model to a running agent is not part of F2. The requirement is
  satisfied and verified at the resolver level (a unit assertion on the resolution
  function), not through a launched session.

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
      the context content is written as `AGENTS.md` at the workspace-root and group
      levels -- the same niwa-owned directories where `CLAUDE.md` is written today
      (R1, R2, R6).
- [ ] Under `codex`, niwa writes no repository- or worktree-level context file:
      no `CLAUDE.local.md` and no in-repo `AGENTS.md`, so no git working tree is
      dirtied and no repository's own committed `AGENTS.md` is overwritten (R6a).
- [ ] A single session can override the agent in either direction -- `codex` over
      a `claude` default, and `claude` over a `codex` default -- while the
      persisted workspace default is unchanged; the override affects only that
      session's preparation (R3).
- [ ] After switching the selected agent and re-applying, the just-applied agent's
      context tree is freshly written (not stale), and the two agents' trees are
      permitted to coexist without error (R8a).
- [ ] The selected agent is resolved once per session and is not exposed as a
      per-repo field in the `[claude]` override cascade (R4) -- verified by the
      selector living in a session-global location, not as a field of the per-repo
      `[claude]` override structure.
- [ ] A per-session override is expressible both as a command-line flag and as an
      environment variable; when both are set the flag wins, and when neither is
      set the workspace default (else `claude`) applies (R3).
- [ ] The materialized context body is identical whether the agent is `claude` or
      `codex`; only the output filename differs (R5, R8).
- [ ] A prepared workspace can carry both `OPENAI_API_KEY` and `ANTHROPIC_API_KEY`
      bound through the same mechanism, and binding one does not remove or corrupt
      the other (R9, R10).
- [ ] Calling the model-resolution function with agent `codex` and each category
      (`fast` / `balanced` / `powerful`) returns a Codex model name distinct from
      the Claude resolution; calling it with agent `claude` returns exactly today's
      values -- both verified as unit assertions on the resolver, not through a
      launched session (R11).
- [ ] An unrecognized model value is still forwarded unchanged with a warning
      under either agent (R12).
- [ ] An unknown agent value is rejected with an error that names the accepted
      values (`claude`, `codex`) (R15).
- [ ] No new code path launches, spawns, or exec's an agent session (R14) --
      verified mechanically: the change introduces no new `os/exec` invocation of
      an agent binary (`claude`, `codex`), only materialization, selection, secret
      binding, and model resolution.
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
- **Repository- and worktree-level Codex context.** Under Codex, this slice writes
  `AGENTS.md` only at the niwa-owned non-repository levels (workspace-root and
  group). Writing an `AGENTS.md` inside a cloned repository or worktree needs
  collision handling against a repository's own committed `AGENTS.md` (the current
  repo-level write is a blind overwrite) and extension of niwa's git-clean
  exclusion mechanism (which today covers only `*.local*`-named files, not
  `AGENTS.md`). Both are deferred to later work; delivering them requires the
  full agent-neutral config layer's machinery.
- **Claude behavior changes.** The incumbent path is not refactored beyond the
  seams the new agent needs (the output-filename seam and the per-agent model
  map). Claude remains the default and its materialization, secret binding, and
  model resolution are otherwise untouched.
- **Delivering the resolved Codex model to a running agent.** The per-agent model
  resolver is wired as groundwork (R11), but its only consumer is the
  background-dispatch launcher, which is out of scope. Nothing in this slice hands
  the resolved model to a live session.

## Known Limitations

- **Switching agents leaves the prior agent's tree in place.** niwa always writes
  a fresh tree for the agent it is applying under (R8a), but it does not remove a
  previously-selected agent's materialized tree when the agent changes. A
  workspace can therefore carry both a `CLAUDE.md` tree and an `AGENTS.md` tree.
  This is harmless as long as a developer applies under the agent they are about
  to run (the just-applied tree is fresh); it becomes stale only if a developer
  runs an agent whose tree was not refreshed by the latest apply. Tracked removal
  of a superseded agent's tree is deferred to the full agent-neutral config work.
- **The Codex model resolver has no in-scope consumer.** As noted in Out of Scope,
  the resolved Codex model is not delivered to a running session in this slice; it
  is verified at the resolver level only.

## Decisions and Trade-offs

- **The active agent is a session-global discriminator, not a per-repo mergeable
  value.** A niwa session runs one agent for the whole workspace, so the agent is
  a single choice resolved once, not a value that different repos could set
  differently and merge. This closes the upstream BRIEF's first open question at
  the requirements level (R4). The concrete storage/override modeling -- mirroring
  the workspace-root state flag precedent, the global-config-plus-per-invocation-
  flag precedent, or another shape -- is left to the DESIGN.

- **The per-session override is surfaced as both a flag and an environment
  variable, flag-wins.** The upstream BRIEF deferred the override surface (flag,
  env var, or both) to this PRD. Decision: support both, because the two serve
  different ergonomics -- a flag for a single explicit invocation, an environment
  variable for a shell session that runs the agent repeatedly -- and a
  flag-over-env precedence keeps an explicit invocation authoritative (R3). The
  exact spelling and the resolver wiring are left to the DESIGN. The alternative
  (pick one surface) was rejected as needlessly limiting for a keystone selector
  other features will build on.

- **Output filename is a function of the selected agent, separate from the content
  source.** Rather than duplicating the content tree or branching materialization
  wholesale, the requirement isolates the single thing that changes -- the output
  filename -- behind an agent-driven seam, keeping the content pipeline shared
  (R5, R8). The trade-off is that agent-specific content bodies are explicitly not
  supported in this slice; that is an accepted limitation for the launch slice.

- **Codex context is materialized only at the niwa-owned non-repository levels for
  this slice.** The workspace-root and group directories are niwa-owned and are
  not git repositories, so an `AGENTS.md` there cannot collide with a repository's
  committed file nor dirty a working tree, and a `codex` session launched at the
  workspace/instance root reads it directly. Writing `AGENTS.md` inside a cloned
  repository was considered and deferred (R6a, Out of Scope): niwa's repo-level
  content write is a blind overwrite that would clobber a repository's own
  committed `AGENTS.md`, and its git-clean exclusion mechanism covers only
  `*.local*`-named files, so an in-repo `AGENTS.md` would also dirty the tree.
  Handling both correctly belongs with the full agent-neutral config work. The
  chosen slice delivers a working Codex session at the canonical launch point with
  zero collision risk, and the agent-aware filename seam it introduces extends to
  the repository levels later without rework.

- **Reuse the existing secret split and model-resolution seam rather than build
  agent-neutral config now.** The secret-binding mechanism and the model-category
  map already exist and are close to agent-neutral; this slice extends them for a
  second agent rather than introducing a new config layer (R9, R11). The full
  agent-neutral config layer is deferred (Out of Scope).

## Open Questions

- Which existing session-global precedent the agent selector's persisted default
  should mirror (the workspace-root state file, a workspace-config field, or the
  global config), and the concrete storage location, is deferred to the DESIGN.
  The requirements fix that the selector is session-global and not a per-repo
  cascade field (R4), and that the per-session override is expressible as both a
  flag and an environment variable with the flag winning (R3).
- The concrete Codex model-name values that `fast` / `balanced` / `powerful`
  resolve to, and whether resolution dispatches on the selected agent inside the
  existing seam or through a per-agent map, is deferred to the DESIGN (R11, R12).
- Repository- and worktree-level Codex context is out of scope for this slice
  (R6a, Out of Scope), so the repo-local Codex filename question is deferred with
  it. When that work is picked up, the DESIGN will resolve the in-repo `AGENTS.md`
  collision-and-git-clean handling described in Out of Scope.
