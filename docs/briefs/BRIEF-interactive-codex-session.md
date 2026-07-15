---
schema: brief/v1
status: Accepted
problem: |
  niwa prepares a workspace for one agent only: it writes a CLAUDE.md context
  tree, binds ANTHROPIC_API_KEY, and resolves model categories to Claude model
  names. A developer who wants to run an interactive OpenAI Codex session in a
  niwa workspace has no supported path -- the context lands under a filename
  Codex does not read, and there is no way to select Codex as the agent.
outcome: |
  A developer selects Codex as the workspace agent (a per-workspace default plus
  a per-session override), runs niwa, and gets a workspace prepared for Codex:
  the same context tree materialized as AGENTS.md, OPENAI_API_KEY bound, and
  model categories resolved to Codex names. They run `codex` and it sees its
  context and does real work -- with the same ease as preparing a Claude session.
motivating_context: |
  A conformance check against codex-cli 0.144.3 confirmed the load-bearing
  facts: Codex ingests AGENTS.md from the working directory directly (no hook
  needed), its home and credentials (~/.codex, OPENAI_API_KEY) do not collide
  with Claude's (~/.claude, ANTHROPIC_API_KEY), and no config file is required
  for a session to see its context and work. This is the launch slice -- the
  keystone that later Codex-support features build on.
---

# BRIEF: interactive Codex session in a niwa workspace

## Status

Accepted

The downstream PRD owns the requirements; the downstream DESIGN owns the
selector modeling and the output-filename-by-agent seam. This brief stops at
the developer-facing framing.

## Problem Statement

niwa's job is to prepare a workspace so an AI coding agent can do useful work in
it: it materializes a layered context tree (root, group, repo, worktree), binds
the agent's API-key secret, and maps portable model categories
(fast/balanced/powerful) to concrete model names. Every one of those steps
currently assumes a single agent -- Claude Code.

The assumption is baked in at the output layer, not just the defaults. The
context tree is written to files named `CLAUDE.md`; the secret split binds
`ANTHROPIC_API_KEY`; the model-category map resolves to Claude model names. A
developer whose quota, preference, or task steers them toward OpenAI Codex has no
supported way to get a niwa workspace ready for it. Codex reads its context from
`AGENTS.md`, not `CLAUDE.md`, so even a fully materialized workspace is invisible
to it. And there is no knob to say "this workspace targets Codex" in the first
place -- niwa has no concept of a selectable agent.

The gap is not that Codex is hard to run. Codex ingests `AGENTS.md` from its
working directory on its own, keeps its home and credentials separate from
Claude's, and needs no config file to start working. The gap is that niwa
prepares the wrong shape: right content, wrong filename, wrong secret, wrong
model names, and no way to ask for anything else. A developer who wants an
interactive Codex session in a niwa-managed workspace is on their own to
hand-rename files and hand-wire credentials that niwa already knows how to
produce for Claude.

## User Outcome

A developer working in a niwa workspace can choose which agent the workspace is
prepared for, and niwa prepares it correctly for that choice. When the developer
selects Codex -- as the workspace's default, or as a one-off override for a
single session -- niwa materializes the same layered context it already knows how
to build, but as `AGENTS.md` files in the same locations, binds
`OPENAI_API_KEY` alongside the Claude key so both agents coexist on the host, and
resolves the same portable model categories to Codex model names. The developer
runs `codex` in the prepared workspace and it sees its context and does real
work.

What changes for the developer is that "which agent" becomes a supported,
declared choice instead of an unsupported manual workaround. Preparing a Codex
workspace becomes as ordinary as preparing a Claude one: pick the agent, run
niwa, start the session. The developer never hand-renames a context file or
hand-exports a credential niwa could have bound. Claude remains the default and
its behavior is unchanged; Codex becomes a peer the workspace can be pointed at.

## User Journeys

### Ada sets Codex as the workspace default

Ada maintains a workspace where the whole team runs Codex. She declares Codex as
the workspace's default agent once. From then on, `niwa apply` materializes the
context tree as `AGENTS.md` files, binds `OPENAI_API_KEY`, and resolves model
categories to Codex names -- without her restating the choice per run. She opens
the workspace, runs `codex`, and it picks up its context immediately. The outcome
she reaches: a workspace that is prepared for Codex by default, at the same
effort as a Claude-default workspace.

### Ben overrides to Codex for one session

Ben's workspace defaults to Claude, but his Claude quota is exhausted for the
day and he wants to keep working under Codex. He overrides the agent for this
one session, runs niwa, and gets a Codex-prepared workspace -- `AGENTS.md`
context, `OPENAI_API_KEY` bound -- without changing the workspace default that
his teammates rely on. The outcome he reaches: a per-session switch that resolves
once for his session and leaves the workspace default untouched.

### Cleo runs both agents on one host

Cleo already runs a Claude-default workspace on her machine, and today she points
a second workspace at Codex. Because niwa binds each agent's credential into its own split and the
agents keep separate homes (`~/.claude` and `~/.codex`), her two setups never
collide -- selecting Codex for one workspace does not disturb the Claude
credentials or context another workspace prepared. The outcome she reaches:
coexisting agents on one host, each workspace prepared for whichever agent it
targets.

## Scope Boundary

### IN

- A per-workspace default agent plus a per-session override, resolved once per
  session. The active agent is a single session-global choice, not a per-repo
  value merged across the config cascade.
- Materializing the existing context tree (root, group, repo, worktree) as
  `AGENTS.md` when the selected agent is Codex, in the same locations niwa writes
  `CLAUDE.md` today. This requires a seam that chooses the output filename by
  agent -- the output name is distinct from the content source directory.
- Binding `OPENAI_API_KEY` alongside `ANTHROPIC_API_KEY` in niwa's existing
  agent-neutral secret split, so both agents coexist on one host with separate
  credentials.
- Per-agent model-category maps: the portable categories (fast/balanced/powerful)
  resolve to Codex model names when Codex is selected, reusing the existing
  model-resolution seam.

### OUT

- **Launching or exec'ing the session.** niwa prepares the workspace; the
  developer runs the agent binary themselves. niwa does not spawn `claude`
  today and will not spawn `codex`. Background dispatch, `codex exec`, and
  session attach are separate downstream work.
- **Hooks, provisioning, and reaping.** Codex reads `AGENTS.md` from its working
  directory directly, so a bare interactive session needs no SessionStart hook,
  no auto-provisioning, and no liveness reaper. Ephemeral-session provisioning
  for Codex is separate downstream work.
- **A full Codex config table.** This feature is the launch slice of the config
  work: enough to select Codex and materialize its context. A complete
  agent-neutral config layer -- a general `[agent]`/`[codex]` table, generalized
  content-source directories, host-coexistence config beyond the secret binding
  -- is separate downstream work.
- **Claude behavior changes.** Claude stays the default and its materialization,
  secret binding, and model resolution are unchanged. This feature adds a peer;
  it does not refactor the incumbent beyond the seams the peer needs.

## Open Questions

- The exact modeling of the selector (a session-global state flag alongside the
  existing dispatch settings versus some other placement) is deferred to the
  downstream DESIGN. The brief's commitment is only that the active agent is a
  session-global discriminator, not a per-repo mergeable config value.
- Whether the per-session override is surfaced as a flag, an environment
  variable, or both is a requirements detail the downstream PRD owns.
