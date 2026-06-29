# Explore Scope: remote-control-by-default

## Visibility

Public

## Core Question

How should niwa enable Claude Code Remote ("remote-control") by default on the
sessions it starts via `niwa dispatch`, configured through a host-level niwa
setting? The host default should be overridable by downstream config, live in
niwa's existing global override layer, and apply specifically to dispatched
workers rather than all launched sessions.

## Context

- niwa launches Claude Code sessions in several ways: `niwa dispatch` (background
  workers via `claude --bg`, `internal/cli/dispatch_launcher.go`), ephemeral
  session instances (SessionStart hook -> `niwa instance from-hook`), and the
  interactive root/instance session. The user wants ONLY `niwa dispatch` sessions
  affected.
- niwa already materializes Claude Code `settings.json` / `settings.local.json`
  and env vars at four rungs: workspace root, instance, repo, plus a host-level
  layer -- `~/.config/niwa/config.toml` (GlobalConfig) and a `GlobalConfigOverride`
  (`niwa.toml`) carrying `[global.claude.settings]` / `[global.claude.hooks]`,
  applied across all workspaces on the machine.
- User decisions from scoping:
  - **Session scope:** only sessions started with `niwa dispatch`.
  - **Config placement:** the existing global override layer (host rung).
  - **Override model:** a default that downstream config (workspace / instance /
    repo) can turn off.
- Key tension to resolve: `[global.claude.settings]` applies to ALL sessions in
  ALL workspaces, but the user wants the effect scoped to `niwa dispatch` only.
  That suggests the host config holds a toggle that the dispatch launch path
  specifically consumes -- not a raw settings.json key that leaks into every
  interactive session.
- "remote-control" is read as Claude Code Remote: steering a launched session
  from claude.ai / mobile / Agent View (this session has the
  `mcp__claude_ai_Claude_Code_Remote__*` tools live; the instance dir is named
  `rc_by_default`).

## In Scope

- The exact Claude Code mechanism that enables remote-control on a launched
  (especially `--bg`/headless) session, plus any credentials/auth it needs.
- Where and how a host-level toggle plugs into `niwa dispatch`'s launch path.
- The shape and merge/materialize semantics of niwa's global override layer, and
  how a dispatch-scoped default can still be overridden downstream.
- Prior art / planned work in niwa around dispatch, remote, ephemeral sessions.

## Out of Scope

- Enabling remote-control on interactive root/instance sessions or ephemeral
  session instances (explicitly excluded by the user).
- Cross-machine / remote dispatch (a non-goal already noted in
  `docs/briefs/BRIEF-instance-dispatch.md`).
- Building Claude Code Remote itself -- this is about niwa flipping a switch the
  harness already provides.

## Research Leads

1. **How does Claude Code enable remote-control (Claude Code Remote) on a launched, especially headless/`--bg`, session?** (lead-ccr-mechanism)
   This is the critical unknown. Is it a `settings.json` key, an env var, a
   `claude` CLI flag, or an account/login state? What credentials or auth does it
   require, and does anything need to be present in the worker's environment?
   The answer determines what niwa must actually set.

2. **How does `niwa dispatch` construct and launch the session today, and where would a host-level default cleanly inject?** (lead-dispatch-plumbing)
   Trace `internal/cli/dispatch_launcher.go` (`buildClaudeBgArgs`, passthrough
   args, env inheritance) and the dispatch command wiring. Identify the seam
   where a host toggle would translate into either an extra `claude` flag, an env
   var, or an instance settings write -- scoped to dispatch only.

3. **What is the shape and merge/materialize behavior of niwa's global override layer, and how can a dispatch-scoped default still be overridden downstream?** (lead-host-config-layer)
   Map `GlobalConfig` (`~/.config/niwa/config.toml`), `GlobalConfigOverride`
   (`niwa.toml`, `[global.claude.*]`), `GlobalSettings`, and how they merge with
   workspace/instance/repo config. Determine whether the layer is consumed only
   at apply/materialize time or also reachable at dispatch time, and how a
   downstream "off" would win over a host "on".

4. **Is there prior art or planned work in niwa for this, and what do the dispatch/ephemeral-session docs already assume?** (lead-prior-art-planned)
   Read `docs/briefs/BRIEF-instance-dispatch.md`, the ephemeral-session SPIKE and
   guide, and any `remote`/CCR references. Surface existing config conventions,
   non-goals, and whether any of this is already built, partially built, or
   sequenced.

5. **Is there evidence of real demand for this, and what do users do today instead?** (lead-adversarial-demand)
   You are a demand-validation researcher. Investigate whether evidence supports
   pursuing this topic. Report what you found. Cite only what you found in durable
   artifacts. The verdict belongs to convergence and the user.

   ## Visibility

   Public

   Respect this visibility level. Do not include private-repo content in output
   that will appear in public-repo artifacts.

   ## Six Demand-Validation Questions

   Investigate each question. For each, report what you found and assign a
   confidence level (High / Medium / Low / Absent as defined below).

   Confidence vocabulary:
   - **High**: multiple independent sources confirm (distinct issue reporters,
     maintainer-assigned labels, linked merged PRs, explicit acceptance criteria
     authored by maintainers)
   - **Medium**: one source type confirms without corroboration
   - **Low**: evidence exists but is weak (single comment, proposed solution
     cited as the problem)
   - **Absent**: searched relevant sources; found nothing

   Questions:
   1. Is demand real? Look for distinct issue reporters, explicit requests,
      maintainer acknowledgment.
   2. What do people do today instead? Look for workarounds in issues, docs, or
      code comments (e.g. manually enabling remote-control after each dispatch).
   3. Who specifically asked? Cite issue numbers, comment authors, PR
      references -- not paraphrases.
   4. What behavior change counts as success? Look for acceptance criteria,
      stated outcomes, measurable goals in issues or linked docs.
   5. Is it already built? Search the codebase and existing docs for prior
      implementations or partial work.
   6. Is it already planned? Check open issues, linked design docs, roadmap
      items, or project board entries.

   ## Calibration

   Produce a Calibration section that explicitly distinguishes:
   - **Demand not validated**: majority of questions returned absent or low
     confidence, with no positive rejection evidence. Flag the gap.
   - **Demand validated as absent**: positive evidence that demand doesn't exist
     or was evaluated and rejected.

   Do not conflate these two states. "I found no evidence" is not the same as
   "I found evidence it was rejected."
