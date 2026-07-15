---
status: Planned
upstream: docs/prds/PRD-interactive-codex-session.md
problem: |
  niwa prepares a workspace for exactly one agent, Claude Code. The context
  tree is written to files literally named CLAUDE.md/CLAUDE.local.md at eight
  hardcoded write sites, and there is no session-global knob to select a
  different agent. A developer who wants an interactive OpenAI Codex session
  gets a workspace Codex cannot read (Codex reads AGENTS.md) and no way to ask
  niwa for one.
decision: |
  Introduce an Agent discriminator (claude default, codex) resolved once per
  session from a workspace-config default plus a flag/env override, threaded
  through the materialization entry points via an Agent field on the options
  structs. An agent-aware filename helper replaces the hardcoded CLAUDE.md
  literals at the niwa-owned (workspace-root and group) write sites; under Codex
  those emit AGENTS.md, and the repository/worktree-level writes are skipped.
  OPENAI_API_KEY rides the already agent-neutral secret table unchanged. The
  model-category resolver becomes agent-aware as keystone groundwork.
rationale: |
  The agent is one choice for a whole session, so it is modeled as a
  session-global discriminator alongside the existing EphemeralSessionMode and
  DispatchModel precedents, not as a per-repo field in the [claude] cascade.
  Codex materialization is confined to the directories niwa owns outright
  because writing AGENTS.md inside a cloned repository would both clobber a
  repository's own committed AGENTS.md (the repo-level write is a blind
  overwrite) and dirty the git working tree (niwa's exclude mechanism covers
  only *.local* names). Confining the slice to the non-repository levels keeps
  it correct and small while introducing the seam later features extend without
  rework.
---

# DESIGN: interactive Codex session in a niwa workspace

## Status

Planned

Upstream PRD: docs/prds/PRD-interactive-codex-session.md (In Progress). The
downstream PLAN decomposes this into implementation issues.

## Context and Problem Statement

niwa's materialization pipeline assumes a single agent. Three surfaces encode
the assumption:

1. **Output filenames.** The context tree is written to files named `CLAUDE.md`
   (at the niwa-owned workspace-root and group levels) and `CLAUDE.local.md` (at
   the repository and worktree levels). These are plain string literals at eight
   write sites across `internal/workspace/content.go`,
   `internal/workspace/root_materializer.go`, and
   `internal/workspace/worktree_content.go`. The output filename is distinct from
   the content *source* (resolved relative to `content_dir`), which is already
   filename-agnostic.
2. **Credentials.** The secret split binds `ANTHROPIC_API_KEY`.
3. **Model categories.** `internal/cli/dispatch_model.go` resolves the portable
   categories `fast`/`balanced`/`powerful` to Claude model names.

There is no session-global concept of a "selected agent" at all. A developer who
wants to run OpenAI Codex in a niwa workspace therefore has no supported path:
Codex reads its context from `AGENTS.md`, so even a fully materialized workspace
is invisible to it, and nothing lets the developer declare that a workspace
targets Codex.

The upstream PRD scopes the smallest usable slice: select the agent, materialize
the context as `AGENTS.md` at the levels niwa owns, bind `OPENAI_API_KEY`, and
make the model resolver agent-aware. Launching sessions, hooks/provisioning, a
full config table, and repository-level Codex context are explicitly out of
scope. This DESIGN decides how to build that slice.

Two verified facts from a conformance check against `codex-cli 0.144.3` shape the
design: Codex ingests `AGENTS.md` from its working directory directly (no hook),
and Codex's home and credentials (`~/.codex`, `OPENAI_API_KEY`) do not collide
with Claude's (`~/.claude`, `ANTHROPIC_API_KEY`).

## Decision Drivers

- **Session-global, not per-repo.** One niwa session runs one agent for the whole
  workspace. The agent is a discriminator resolved once, never a value merged
  differently per repository. It must not enter the `[claude]` override cascade,
  whose entire purpose is per-repo merge.
- **Backward compatibility is absolute.** The default and unselected path is
  Claude and must be byte-for-byte unchanged; the existing test suite must pass
  untouched except where a test deliberately exercises the new agent.
- **Keystone cleanliness.** This is the foundation later Codex features build on.
  The seam (the Agent type, the filename helper, the threading) must be complete
  and correct even where this slice does not yet exercise every level, so later
  features extend it rather than rework it.
- **Correctness over coverage for the git-repo boundary.** Writing into a cloned
  repository risks clobbering repository-owned files and dirtying the git tree.
  The slice must not introduce either hazard; where handling them cleanly is
  non-trivial, defer the level rather than ship a hazard.
- **Reuse existing seams.** The secret table and the model resolver already exist
  and are close to agent-neutral. Extend them; do not build a parallel config
  layer.

## Considered Options

### Decision 1 — How to introduce the output-filename-by-agent seam

The output filename is a hardcoded literal at eight write sites. The seam must
make the filename a function of the selected agent.

- **Option 1A (chosen): an `Agent` type with filename accessors.** Define
  `Agent` as a small string type (`claude`, `codex`) in the workspace package,
  with methods `RootContextFileName()` (returns `CLAUDE.md` / `AGENTS.md`) and
  `LocalContextFileName()` (returns `CLAUDE.local.md` / `AGENTS.md`). Replace the
  literals at the niwa-owned write sites with calls. The type is the single home
  for the agent-to-filename mapping and the natural place for validation.
- **Option 1B: a `map[Agent]struct{base,local string}` package variable.** A
  data table instead of methods. Rejected: a bare map invites unchecked lookups
  (a zero/unknown key returns empty filenames silently); methods on a validated
  type fail loudly and read better at call sites.
- **Option 1C: an `if agent == codex` branch at each write site.** Rejected:
  duplicates the mapping at eight sites, so a later agent or a filename change
  touches every site — the opposite of a keystone seam.

### Decision 2 — Where the selected agent lives and how it resolves

The agent needs a per-workspace default plus a per-session override, resolved
once, outside the `[claude]` cascade.

- **Option 2A (chosen): workspace-config default + flag/env override.** A new
  `default_agent` field on `WorkspaceMeta` (TOML `[workspace].default_agent`),
  defaulting to empty (= `claude`). A per-session override via a `--agent` flag on
  the apply/create entry commands and a `NIWA_AGENT` environment variable.
  Resolution precedence, computed once by a small `ResolveAgent` function:
  flag > env > `[workspace].default_agent` > `claude`. This mirrors
  `DispatchModel`'s "config default overridden by a per-invocation flag" shape,
  moved to workspace scope because the agent is a per-workspace authoring choice
  a team shares (unlike the per-host `DispatchModel`). `WorkspaceMeta` already
  carries workspace-authoring fields (`Name`, `ContentDir`, `DefaultBranch`), so
  the field is at home and is structurally outside the `[claude]` cascade.
- **Option 2B: workspace-root `.niwa/instance.json` state (mirror
  `EphemeralSessionMode`).** Rejected as the *default's* home: `instance.json` is
  per-materialized-instance local state set at `niwa init`, not shared,
  version-controlled workspace config. A team default ("this workspace runs
  Codex") belongs in the committed `workspace.toml`, not each developer's local
  state file. (`EphemeralSessionMode` is the right precedent for *shape* — a
  session-global knob resolved via a fail-safe accessor — but not for storage
  layer.)
- **Option 2C: global `~/.config/niwa/config.toml` (mirror `DispatchModel`
  exactly).** Rejected as the default's home: the agent is a per-workspace choice,
  not a per-host one. A single global default would force every workspace on the
  host to the same agent. The *override* half, though, is exactly `DispatchModel`'s
  flag pattern, which 2A adopts.
- **Option 2D: a fifth field in the `[claude]` override cascade.** This is a
  constraint the PRD (R4) forbids rather than a live option, but it is recorded
  here with its independent engineering reason: the cascade is a per-repo merge
  structure, so an agent field there would wrongly imply a repo could pick a
  different agent than its siblings within one session — which is exactly the
  per-repo-mergeable modeling the driver rules out.

### Decision 3 — Which levels materialize Codex context

Under Claude, niwa writes `CLAUDE.md` at the workspace-root and group levels
(niwa-owned, non-git directories) and `CLAUDE.local.md` at the repository and
worktree levels (inside cloned git repositories).

- **Option 3A (chosen): materialize `AGENTS.md` only at the niwa-owned
  non-repository levels; skip the repository/worktree levels under Codex.** The
  workspace-root and group directories are niwa-owned and are not git
  repositories, so an `AGENTS.md` there cannot collide with a repository's own
  file nor dirty any working tree. A `codex` session launched at the
  workspace/instance root reads that `AGENTS.md` at its working directory directly
  (verified). Under Codex the repository/worktree installers write nothing (not
  `CLAUDE.local.md`, which Codex cannot read, and not `AGENTS.md`, deferred per
  below).
- **Option 3B: full parity — also write `AGENTS.md` into repositories/worktrees,
  with collision handling and git-clean coverage.** Deferred, not chosen for this
  slice. It requires two net-new mechanisms: (1) extending niwa's git-clean
  exclusion (`internal/gitexclude`, whose managed patterns are `*.local*` and
  `.niwa/`) to cover `AGENTS.md`, since `AGENTS.md` has no `.local` infix and would
  otherwise show as an untracked file; and (2) a collision guard, because the
  repository-level content write is a blind `os.WriteFile` that would silently
  overwrite a repository's own committed `AGENTS.md`. This hazard is real in
  practice — repositories in a typical workspace ship their own `AGENTS.md`. Both
  mechanisms belong with the full agent-neutral config work; folding them into the
  launch slice would enlarge and endanger it.
- **Option 3C: full parity by blind overwrite (no guards).** Rejected outright:
  it destroys repository-owned `AGENTS.md` content and dirties the git tree,
  breaking the clean-working-tree invariant niwa maintains today.

### Decision 4 — How `OPENAI_API_KEY` binds

- **Option 4A (chosen): reuse the existing agent-neutral secret table.** The
  secret split is a `map[string]MaybeSecret` keyed by arbitrary environment-
  variable name; the vault layer resolves it key-agnostically. `OPENAI_API_KEY`
  is already bindable as another row today with no code change. The slice's work
  is to prove it (a round-trip test mirroring the existing `ANTHROPIC_API_KEY`
  test) and to document/scaffold it as a first-class example alongside the Claude
  key.
- **Option 4B: a dedicated `OPENAI_API_KEY` config field.** Rejected: the table
  is already neutral; a dedicated field re-specializes what is generic and adds
  schema surface for no capability.

### Decision 5 — How the model resolver becomes agent-aware

- **Option 5A (chosen): key the category map by agent and thread the agent into
  the resolver.** `modelCategories` becomes agent-aware (Claude keeps
  `fast/balanced/powerful → haiku/sonnet/opus`; Codex gets a parallel set of
  versionless Codex model names, adjustable defaults). `resolveDispatchModel`
  takes the selected agent and selects the right category set before its existing
  known-name / raw-forward fallbacks. The current call site preserves today's
  behavior by resolving under `claude`. This lands the resolver as keystone
  groundwork; its only consumer (the background-dispatch launcher) is out of
  scope, so nothing new is launched.
- **Option 5B: a separate `resolveCodexModel` function.** Rejected: forks the
  resolver, so the shared known-name/forward-unchanged logic drifts between two
  copies.
- **Option 5C: defer the resolver entirely.** Rejected: the model-category map is
  named as part of this keystone; landing the agent-aware shape now (data + a
  resolver signature) is cheap and prevents a later feature from reshaping the
  seam.

## Decision Outcome

The slice adds an `Agent` discriminator and threads it through materialization,
touching one new concept and four existing seams:

- **Agent type (Decision 1, 2).** A validated `Agent` string type
  (`claude` default, `codex`) in a new leaf package `internal/agent`, with
  filename accessors and a `ParseAgent` that rejects unknown values naming the
  accepted set. A `ResolveAgent(flag, env, workspaceDefault string)` precedence
  function computes the session agent once. The leaf placement avoids an import
  cycle (see Solution Architecture).
- **Selector storage (Decision 2).** `WorkspaceMeta.DefaultAgent` (TOML
  `[workspace].default_agent`), a plain string validated via `ParseAgent`, as the
  shared default; `--agent` flag and `NIWA_AGENT` env as the per-session override.
- **Filename seam (Decision 1, 3).** The agent-aware filename accessors replace
  the `CLAUDE.md` literals at the workspace-root and group write sites; under
  Codex the repository/worktree installers skip. Under Claude every path is
  byte-for-byte unchanged.
- **Threading.** An `Agent` field on `RootMaterializeOptions` (beside
  `EphemeralSessionMode`) and on the instance/worktree apply path, carried from
  the resolved session agent at the CLI entry points down to the writers.
- **Secret (Decision 4).** `OPENAI_API_KEY` documented and scaffolded as a
  first-class secret row, with a round-trip test; no mechanism change.
- **Model resolver (Decision 5).** Agent-aware `modelCategories` and
  `resolveDispatchModel`, Claude-identical for the default.

Together these deliver a working interactive Codex session at the canonical
launch point (the workspace/instance root) with zero collision or dirty-tree
risk, and leave a complete seam for repository-level context, dispatch, and the
full config table to extend.

## Solution Architecture

### The `Agent` type (new leaf package)

The `Agent` type lives in a new leaf package `internal/agent`, not in
`internal/workspace`. This placement is load-bearing: `internal/workspace`
already imports `internal/config`, so if `Agent` lived in `internal/workspace`
and `config.WorkspaceMeta` referenced it, `internal/config` would have to import
`internal/workspace` and create an import cycle. A dedicated leaf package
(importing nothing else in the tree) is importable by `internal/config`,
`internal/workspace`, and `internal/cli` alike without a cycle, and is the
natural home for the agent abstraction later features (dispatch, a harness
interface, more agents) extend. Correspondingly, `WorkspaceMeta.DefaultAgent` is
typed as a plain `string` (raw config value), and `agent.ParseAgent` validates it
at resolution time — config stays a dumb data holder and validation stays
centralized on the `Agent` type.

`internal/agent` defines:

```
type Agent string

const (
    AgentClaude Agent = "claude"
    AgentCodex  Agent = "codex"
)

// ParseAgent validates s against the closed set and returns the Agent or an
// error naming the accepted values. Empty string resolves to AgentClaude.
func ParseAgent(s string) (Agent, error)

// RootContextFileName is the filename for niwa-owned (non-repo) levels:
// CLAUDE.md for claude, AGENTS.md for codex.
func (a Agent) RootContextFileName() string

// LocalContextFileName is the filename for repository/worktree levels:
// CLAUDE.local.md for claude, AGENTS.md for codex.
func (a Agent) LocalContextFileName() string

// WritesRepoLevelContext reports whether this agent materializes
// repository/worktree-level context in this slice (true for claude, false for
// codex — see DESIGN Decision 3).
func (a Agent) WritesRepoLevelContext() bool
```

Two contracts on the accessors are load-bearing:

- **Zero value is Claude.** The zero value `Agent("")` MUST behave as
  `AgentClaude` in `RootContextFileName`, `LocalContextFileName`, and
  `WritesRepoLevelContext` (and `ParseAgent("")` returns `AgentClaude`). Many
  `RootMaterializeOptions` and apply-path values are constructed across the CLI
  and tests; a fail-safe zero value means a construction site that does not yet
  set the agent degrades to today's Claude behavior rather than to a broken empty
  filename, so backward compatibility does not depend on updating every
  construction site at once.
- **The codex `LocalContextFileName` value is provisional.** `WritesRepoLevelContext()`
  is false for codex in this slice, so `LocalContextFileName()` returning
  `AGENTS.md` for codex is currently a dead branch. Its value is provisional: the
  deferred repository-level Codex work (Decision 3B) may choose a different
  mechanism (for example appending into a repository's own `AGENTS.md`), so this
  branch is not a committed contract yet.

Keeping the closed set, the validation, and the filename mapping on one type
means a later agent or a filename change is a one-file edit.

### Resolution (new)

`ResolveAgent(flag, env string, workspaceDefault Agent) (Agent, error)` applies
`flag > env > workspaceDefault > claude`, validating the chosen value via
`ParseAgent`. It is called once at the apply/create CLI entry points, and the
result is placed on the options carried into materialization. A fail-safe
accessor is unnecessary here because resolution happens at a command boundary
that can surface an error, unlike `EphemeralSessionMode`'s post-hoc state read.

### Threading

- `RootMaterializeOptions` gains an `Agent agent.Agent` field beside
  `EphemeralSessionMode`.
  `MaterializeWorkspaceRoot` uses `opts.Agent.RootContextFileName()` where it
  writes `rootClaudeFile` today.
- The instance apply pipeline (`Applier.runPipeline`, reached from
  `Applier.Apply`/`Applier.Create`) carries the resolved agent to
  `InstallWorkspaceContent` and `InstallGroupContent`, which use
  `agent.RootContextFileName()` instead of the `CLAUDE.md` literal.
- `InstallRepoContent(To)` and the worktree context layer receive the agent and,
  when `!agent.WritesRepoLevelContext()`, return early without writing (Decision
  3). Under Claude they behave exactly as today.

**Entry points split into two classes.** Materialization is reached from two
kinds of command, and the agent they carry differs by class.

*Workspace-preparation entry points* prepare a workspace the developer will then
run an agent in, so they carry the **resolved** agent (flag > `NIWA_AGENT` >
`default_agent` > claude):

- `niwa apply` and `niwa create` — the primary apply pipeline (`Applier.Apply` /
  `Applier.Create` → `runPipeline`) and the workspace-root materializer
  (`MaterializeWorkspaceRoot`).
- `niwa init` — calls `MaterializeWorkspaceRoot`; it holds the parsed config, so
  it resolves the agent at init and avoids writing a Claude-named root file that
  only self-heals on first apply.
- `niwa reset` — runs `runPipeline`.
- the worktree lifecycle — `ApplyToWorktree`, reached from the session-lifecycle
  command.

*Launch-coupled provisioning entry points* provision an instance and then launch
a specific agent binary into it. In this slice that binary is always **Claude**
(the Claude `SessionStart` hook, `niwa dispatch`, and `niwa watch` all spawn
`claude`), so they prepare a **Claude** instance regardless of `default_agent` —
preparing it for another agent would materialize context the launched Claude
worker cannot read. The shared `realProvisionInstance` therefore pins
`applier.Agent = AgentClaude` for all three. Because a codex-default workspace
launching a Claude worker is a mismatch a developer should see rather than have
silently downgraded, **`niwa dispatch` refuses** when the resolved agent is not
Claude, naming `NIWA_AGENT=claude` as the escape hatch (its own `--agent` flag is
Claude's subagent passthrough, a different concept). A Codex `SessionStart` hook
and Codex background dispatch are later features that will carry a Codex agent
through these paths.

The zero-value agent is Claude (the fail-safe contract above), so a
workspace-preparation path left unwired degrades to today's behavior rather than
breakage. The PLAN carries both lists so no entry point is left unwired or
mis-wired.

### Data flow

```
CLI (apply/create): flags + env + cfg.Workspace.DefaultAgent
        -> ResolveAgent(...) -> Agent (once)
        -> RootMaterializeOptions.Agent / apply pipeline agent param
                -> workspace-root writer  : RootContextFileName()  (CLAUDE.md | AGENTS.md)
                -> group writer           : RootContextFileName()  (CLAUDE.md | AGENTS.md)
                -> repo/worktree writers  : skip when agent == codex
```

### What the Codex `AGENTS.md` carries (and what it does not)

At the instance root, niwa writes more than the primary `CLAUDE.md`: it also
writes companion files (`workspace-context.md`, an overlay content file, a global
content file, and a `.claude/rules` import file) and stitches them into the
primary `CLAUDE.md` through `@import` lines. Claude follows those imports; Codex
does not — Codex reads only the literal `AGENTS.md` at its working directory. So
the instance-root `AGENTS.md` this slice writes carries the primary workspace
content layer (the `[claude.content.workspace]` source), not the
`@import`-composed companions.

This slice deliberately does not inline the companion layers into `AGENTS.md`.
Doing so is a materialization change (concatenating the companion bodies rather
than emitting separate `@import`-ed files) that belongs with the full
agent-neutral content work, which owns the content-tree generalization. The
consequence is recorded honestly below: a Codex session gets the primary
workspace and root content, which is the core orientation, but not the overlay or
global companion layers in this slice.

Separately, `MaterializeWorkspaceRoot` also writes Claude-specific artifacts
(`.claude/settings.json`, root skills, the SessionStart hook entry) that are not
renamed or suppressed under Codex. These are harmless — Codex ignores `~/.claude`
and the workspace `.claude/` directory — and generalizing them is later config
work; this slice leaves them as-is by design.

### Secret and model seams

- `OPENAI_API_KEY` requires no new code: it is a row in the existing
  `[claude.env.secrets]`-shaped table, resolved by the vault layer by name. The
  scaffold gains a commented `OPENAI_API_KEY` example next to the
  `ANTHROPIC_API_KEY` one, and a config round-trip test asserts it decodes and
  resolves like the Claude key.
- `dispatch_model.go` gains an agent dimension across all three of its
  Claude-specific data structures: `modelCategories` becomes keyed by agent
  (Claude unchanged; Codex a parallel versionless map), AND `knownModelNames`
  (the set that suppresses the "unrecognized model" warning) becomes agent-scoped
  — otherwise a legitimate Codex model name would trip the warning branch — AND
  the warning message's listed vocabulary is drawn from the selected agent's sets.
  `resolveDispatchModel` takes the agent. The single existing call site resolves
  under `claude`, preserving current output exactly.

## Implementation Approach

The build decomposes into independent, sequenced units the PLAN can turn into
atomic issues:

1. **Agent type + resolution.** Add the `internal/agent` leaf package with the
   `Agent` type, `ParseAgent`, the filename accessors, `WritesRepoLevelContext`,
   and `ResolveAgent`, with unit tests for validation, precedence, and the
   filename mapping. Pure and dependency-free (imports nothing else in the tree);
   lands first.
2. **Selector config + CLI surface.** Add `WorkspaceMeta.DefaultAgent`, the
   `--agent` flag on the apply/create commands, and `NIWA_AGENT` env reading;
   wire `ResolveAgent` at the entry points. Unknown-agent errors surface here.
   Tests: config decode, precedence end-to-end, error on unknown value.
3. **Filename seam at the niwa-owned levels.** Thread `Agent` onto
   `RootMaterializeOptions` and the apply pipeline, wiring the
   workspace-preparation entry points (`apply`, `create`, `init`, `reset`, the
   worktree lifecycle) to carry the resolved agent; replace the `CLAUDE.md`
   literals at the workspace-root and group write sites with
   `RootContextFileName()`. Parameterize the existing `content_test.go` /
   `root_materializer_test.go` cases by agent; assert `AGENTS.md` under Codex and
   byte-for-byte-unchanged `CLAUDE.md` under Claude, and that a construction site
   left at the zero-value agent still produces the Claude output.
4. **Repository/worktree skip under Codex.** Make `InstallRepoContent(To)` and the
   worktree context layer skip when the agent does not write repo-level content;
   assert no repo-level file is written under Codex and the git working tree stays
   clean, and that Claude behavior is unchanged.
4b. **Launch-coupled provisioning prepares Claude, and dispatch refuses Codex.**
   Pin the shared `realProvisionInstance` (the Claude `SessionStart` hook,
   `niwa dispatch`, `niwa watch`) to `AgentClaude`, since all three launch a
   Claude worker. Make `niwa dispatch` resolve the workspace agent and refuse when
   it is not Claude, naming `NIWA_AGENT=claude` as the escape hatch. Cover with a
   `@critical` functional scenario (dispatch refuses in a codex-default
   workspace).
5. **`OPENAI_API_KEY` binding.** Add the scaffold example and a config round-trip
   test mirroring the `ANTHROPIC_API_KEY` test; document coexistence in the
   relevant guide.
6. **Agent-aware model resolver.** Make `modelCategories` and
   `resolveDispatchModel` agent-aware; keep the existing call site's output
   identical under Claude; unit-test the Codex resolution and the unchanged Claude
   resolution.

Units 1 and 2 gate the rest (they define the `Agent` value and its resolution).
Unit 4 depends on unit 3: the repository/worktree skip needs the agent already
threaded through the apply pipeline that unit 3 wires, so 3 lands before 4. Units
5 and 6 are independent seam extensions that can land in parallel with 3/4. All
six carry the fail-safe zero-value contract (a construction site that has not yet
set the agent behaves as Claude), so partial landings never break the default
path.

## Security Considerations

- **Agent value is a closed set, validated at a single boundary, from three
  sources.** The agent string reaches the system from three inputs — the `--agent`
  flag, the `NIWA_AGENT` environment variable, and the `[workspace].default_agent`
  config field — and every one is forced through `ResolveAgent` → `ParseAgent`,
  which rejects anything outside `{claude, codex}`, before any filename is chosen.
  The config source is the most untrusted of the three: a `workspace.toml` cloned
  from an untrusted upstream carries a committed `default_agent` value. Its worst
  case is bounded to a rejected `apply` (an unknown value fails `ParseAgent` with a
  clear error), never an arbitrary write: because the agent selects a filename
  (`CLAUDE.md` / `AGENTS.md`) and gates a code path, validation at the single parse
  boundary is what closes the risk. The filename accessors operate on an
  already-validated `Agent` and return fixed string constants selected by the
  enum — there is no interpolation of the agent value into a path, so even a
  hypothetically-unvalidated value yields a wrong-but-fixed filename, never a
  traversal.
- **Config field is a raw string, validated at resolution — not a "trusted type."**
  `WorkspaceMeta.DefaultAgent` is typed `string`, not `Agent`, so a value decoded
  from `workspace.toml` never "looks validated" by its type; it is a raw string
  that `ResolveAgent` must parse. This keeps validation from being silently
  skipped by a future edit that trusts the field's type, and keeps the closed-set
  check centralized on `ParseAgent`.
- **No new credential handling.** `OPENAI_API_KEY` flows through the existing
  vault-backed secret table with no new parsing, storage, or logging path; it
  inherits the same handling `ANTHROPIC_API_KEY` already has. The design does not
  touch the Claude-Code-Remote API-key special case.
- **No new process execution.** The slice adds no code that launches, spawns, or
  exec's an agent binary; it only writes files and resolves strings. This is
  asserted by the PRD's no-launch acceptance criterion.
- **No expansion of the write surface.** Under Codex the design writes *fewer*
  files than under Claude (it skips the repository levels), and only into
  niwa-owned non-repository directories, so it cannot write into or dirty a cloned
  repository.

## Consequences

### Positive

- A developer can select Codex (per-workspace default or per-session override) and
  get a workspace a `codex` session reads and works in, at the usability of a
  Claude session, with Claude unchanged as the default.
- The agent seam is complete and centralized: one validated type owns the closed
  set, the filename mapping, and the level policy, so dispatch, repository-level
  context, and the full config table extend it rather than rework it.
- Zero risk to existing behavior and to cloned repositories: the Claude path is
  byte-for-byte unchanged and the Codex path never writes inside a git repository.

### Negative / limitations

- Repository- and worktree-level per-repo context is not delivered to Codex in
  this slice. A `codex` session gets the workspace-root and instance-root primary
  content via its working-directory ingestion at the instance root, but not the
  per-repository layer Claude gets from `CLAUDE.local.md`. Mitigation: this is the
  documented boundary of the launch slice; repository-level Codex context lands
  with the full config work, which also brings the collision guard and git-clean
  coverage it requires.
- The `@import`-composed companion layers (overlay content, global content, the
  workspace-context import file) are not inlined into the Codex `AGENTS.md` in this
  slice, because Codex follows no `@import` lines. A Codex session therefore sees
  the primary workspace/root content but not those companions. Mitigation: the
  primary content is the core orientation; inlining the companions is a
  materialization change owned by the full agent-neutral content work.
- Switching agents between applies can leave the prior agent's tree on disk (niwa
  refreshes only the active agent's tree). Mitigation: apply under the agent you
  are about to run; tracked removal is deferred (PRD Known Limitations).
- The Codex model resolver has no in-scope consumer yet. Mitigation: it is landed
  as verified groundwork and consumed when dispatch arrives.

### Neutral

- Codex model-category values are versionless placeholders chosen for the seam and
  adjusted freely later, exactly as the Claude versionless names are today.
