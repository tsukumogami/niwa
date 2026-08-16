# Explore Scope: dual-agent-workspace

## Visibility

Public

## Core Question

Can a single niwa workspace instance serve both Claude Code and OpenAI Codex at
the same time, with no agent choice made at `niwa create` time? Concretely: after
`niwa create` or `niwa apply`, a developer runs `claude` exactly as today, and
also runs `codex` in the same instance and gets the workspace context plus the
workspace's skills in every session, from any working directory inside the
instance. The exploration must establish whether that is achievable end to end,
and where the mechanism is forced rather than chosen.

## Context

niwa already prepares a workspace for Claude Code: it writes a `CLAUDE.md`
context tree at the workspace root, the group directories, and inside cloned
repos and worktrees, plus `.claude/settings.json` in the instance directory
carrying marketplaces, plugins, hooks, and MCP servers.

A prior increment added an agent discriminator (`claude` | `codex`) with a
`[workspace] default_agent` key. Under `default_agent = "codex"` niwa
materializes `AGENTS.md` at the niwa-owned non-repository levels instead of
`CLAUDE.md`, skips repository-level writes, and refuses background dispatch.
That increment built an exclusive selector: one agent per workspace preparation.
Reversing that exclusivity is the point of this work.

Live measurement against codex-cli 0.147.0 has already established several
things that constrain the design. Codex has no project-level config -- every
setting comes from `$CODEX_HOME` (default `~/.codex`). Codex does not walk up
the directory tree for `AGENTS.md`: exactly two sources load, the current
working directory's `AGENTS.md` and `$CODEX_HOME/AGENTS.md`. That makes the
existing workspace-root and group-level `AGENTS.md` invisible to any Codex
session not sitting in that exact directory, which is a defect in what shipped.
A per-instance `CODEX_HOME` has been shown to work, with `auth.json` and the
plugin cache symlinked back to the shared host home so no re-login is needed.
Claude plugins install into Codex unmodified via
`.claude-plugin/marketplace.json`.

The two agents appear not to collide: different context filenames, different
discovery rules, different config files, the same plugin manifest, and env and
secrets already flowing through an agent-neutral `.local.env`.

## In Scope

- Making materialization additive: keep the `CLAUDE.md` tree exactly as it is,
  and additionally materialize a per-instance Codex home.
- Composing a single `AGENTS.md` for `$CODEX_HOME` from the context sources that
  today are assembled through `@import` composition across levels.
- Materializing the Codex `config.toml` -- marketplaces, plugins, and whatever
  else niwa already emits for Claude that has a genuine Codex analog.
- Deciding and implementing how `CODEX_HOME` reaches the developer's `codex`
  invocation.
- Reworking the exclusive one-agent-per-preparation contract in the agent
  package, while keeping `default_agent` working for configs that already
  declare it.
- Worktree behavior under the Codex home.
- Whether niwa exports `OPENAI_API_KEY`.

## Out of Scope

- Renaming or re-homing the `[claude.*]` config tables, including moving the
  secret table to a neutrally named one. New config surface, if needed, is added
  additively.
- Codex background dispatch (`codex exec`, session-id capture, resume-by-id).
- Ephemeral session provisioning and the reaper's liveness proxy for Codex.
- Changes to the workflow-skills plugin or the orchestration engine, including
  named subagent types, which do not exist under Codex.
- Building a per-instance layout selector flag. That is the last-resort fallback
  if dual capability proves unreachable, and it needs evidenced justification.

## Research Leads

1. **Can niwa pre-write a `trusted_hash` into `[hooks.state]` that Codex accepts
   without prompting, and must Codex hooks be delivered inside a plugin?**
   (lead-hook-trust) niwa injects hooks for Claude today, and later provisioning
   work depends on a Codex `SessionStart` hook. A loose `$CODEX_HOME/hooks.json`
   produced no `[hooks.state]` entry, and a synthesized local marketplace
   carrying a plugin with a `hooks.json` installed cleanly but still registered
   nothing from non-interactive commands. This is the finding most likely to
   move the design. Needs a live spike, including reading the codex binary's own
   behavior to find how the hash is computed and when the state entry is written.

2. **How should `CODEX_HOME` reach the developer's `codex` invocation?**
   (lead-codex-home-delivery) niwa's shell wrapper already exports things on
   instance entry, which would make plain `codex` just work; a `niwa codex`
   launcher is the explicit fallback. Needs the current shell-init mechanism
   read in full, including how it is installed, what it already exports, whether
   it survives subshells and non-interactive use, and what happens for a
   developer who never sources it.

3. **Should niwa export `OPENAI_API_KEY` at all?** (lead-openai-key) With the key
   present in the environment, `codex doctor` warns about mixed auth signals and
   reports that HTTP reachability uses API-key mode. The driving use case is
   spending a subscription, so exporting the key by default may bill the wrong
   account. Needs a live determination of what a real session actually uses when
   both are present, and what the current config plumbing would do with the key.

4. **Which `$CODEX_HOME` entries must be isolated per instance, which should be
   shared with the host home, and which are safe either way?**
   (lead-codex-home-layout) `auth.json` and the plugin cache are shared in the
   verified layout; `config.toml`, `skills/`, and `AGENTS.md` are isolated.
   Undecided: `sessions/`, `history.jsonl`, `models_cache.json`,
   `installation_id`, and the several other entries a real home contains. Also
   needs the refresh-token write-through behavior through a symlinked
   `auth.json` actually exercised, not assumed.

5. **Do worktrees inherit the instance's `CODEX_HOME` or get their own?**
   (lead-worktrees) niwa materializes into worktrees as well as clones. Needs the
   current worktree materialization path read, plus a decision grounded in what
   a worktree is for and what a developer running `codex` inside one expects.

6. **What must the composed `$CODEX_HOME/AGENTS.md` contain?**
   (lead-composed-context) Today's context tree is assembled from workspace,
   group, and repo sources with `@import` composition, resolved lazily by the
   agent as it walks up from the working directory. Codex gets exactly one file
   and no walk, so composition has to happen at materialization time. Needs the
   current composition mechanism read end to end, including what the imports
   pull in and how large the result would be if flattened.

7. **What exactly did the prior increment build, and what has to change?**
   (lead-current-state) A full inventory of the agent package, the
   materialization path, the config surface, the dispatch path, and their tests
   -- specifically which accessors encode the exclusive contract, what a
   `default_agent = "codex"` workspace produces today, and what breaks if
   materialization becomes additive. Must also confirm the reasoning that
   because Codex context lives in `CODEX_HOME`, niwa never writes `AGENTS.md`
   into a cloned repo, which would make a git-exclude extension and a
   collision guard against a repo's own committed `AGENTS.md` unnecessary.

8. **What exactly must niwa write into a `config.toml` for a Codex session to
   load the workspace's marketplaces, plugins, skills, and MCP servers?**
   (lead-config-materialization) The config surface reportedly maps closely onto
   what niwa already emits for Claude. Needs the precise required shape verified
   live against a per-instance home, driven only by files niwa writes -- no
   interactive `codex plugin add` -- because materialization cannot depend on
   running interactive commands.
