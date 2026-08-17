# Crystallize: dual-agent-workspace

## Candidacy

- **`/execute`** is not a candidate. No qualifying PLAN exists: the repo has no
  `docs/plans/` directory and no document carries `schema: plan/v1` for this
  topic.
- **Competitive analysis** is not a candidate. The scope file records
  `## Visibility: Public`, which removes the category entirely.

## Stage 1: what the exploration is

**A chain** — 5 signals, 0 anti-signals. Top-ranked and undemoted.

Signals matched: the exploration converged on something someone will build; a
scope boundary emerged rather than just an answer; decisions made during the
exploration need a durable home and have downstream work attached; architecture
and sequencing questions remain open; and the core question throughout was what
to build and how.

No anti-signal applies. The conclusion is proceed, the output is not a single
choice between named options, and someone is committed to acting on it.

Runners-up, both demoted for carrying anti-signals:

- **Spike report** (3 signals, 2 anti-signals). The exploration did test specific
  technical risks and produce concrete findings, but the question was never "can
  we do this?" — it was "what do we build, and how?", and the investigation was
  broad across twelve leads rather than focused on one technical risk.
- **Decision record** (2 signals, 1 anti-signal). Real alternatives were compared
  with trade-offs, and future contributors will need the rationale. But the
  output is several interrelated decisions with work attached, not one choice —
  the tiebreaker's own test for preferring a chain.
- **Rejection record** — no signals. The conclusion is proceed.

## Stage 2: where the chain starts

**`/scope`** — 7 signals, 0 anti-signals.

A single coherent feature emerged. The exploration surfaced multiple viable
implementation paths and chose between them, architectural questions about the
materialization layout remain open, and — the strongest signal — substantial
architectural decisions were made during the exploration that must be recorded
somewhere durable. Everything under `wip/` is deleted before a PR merges, so the
reasoning behind rejecting a per-instance Codex home, rejecting a global
project-root marker change, and rejecting content transformation would be lost
with the branch if it is not written into a permanent artifact.

Alternatives:

- **File an issue** — ranked last (0 signals, 3 anti-signals). Others need
  documentation to build from, the exploration made real architectural and
  structural decisions, and scope was debated across four rounds. Filing would
  discard the rationale.
- **`/charter`** — ranked lower (0 signals, 3 anti-signals). The project already
  exists, this is one bounded feature however large, and its users and needs are
  uncontested. There is no set of separately-sequenced features needing order.

## Decision

Run `/scope` on the dual-agent workspace feature, carrying the findings forward.

## What the chain must carry

The exploration overturned two premises the brief treated as settled, and the
downstream artifacts must be written against the corrected picture rather than
the brief's:

1. **No per-instance Codex home is needed.** The brief's design direction assumed
   one, plus a mechanism to export `CODEX_HOME`. Codex's project-level config
   layer carries context, skills, config, MCP servers, and hooks from a `.codex/`
   directory discovered by walking up from the working directory. This dissolves
   the delivery problem, the share/isolate matrix, and the auth-symlink hazard
   together, and it means niwa never touches the developer's credentials.

2. **Codex does walk up the tree for context**, from the nearest project-root
   marker down to the working directory. The brief's "no walk" finding was an
   artifact of testing outside a git repository. Composition is therefore
   per-repo rather than a whole-workspace flatten.

Decisions that need to survive into a permanent document:

- Per-repo payload delivery under the default `.git` marker, rather than an
  instance-root payload with a repointed `project_root_markers` — because
  repointing replaces `.git` machine-wide and degrades Codex in every repository
  outside a niwa instance.
- `AGENTS.override.md` as the per-repo context filename, written unconditionally
  and inlining any committed `AGENTS.md`, rather than relying on a
  `CLAUDE.local.md` fallback that silently delivers nothing for repos shipping
  their own `AGENTS.md`.
- Whole plugin directories delivered verbatim with no content transformation —
  no frontmatter rewriting, no variable substitution — because namespacing
  follows `plugin.json` through symlinks and `${CLAUDE_PLUGIN_ROOT}` is never
  expanded by Codex on any route.
- No `OPENAI_API_KEY` export, because an exported key is inert when the
  subscription login works and a silent metered fallback when it does not.
- The git-exclude extension is needed after all (for `.codex` and
  `AGENTS.override.md`, with the pattern written bare rather than with a
  trailing slash), while the collision guard remains unnecessary.

Known risks to carry as acceptance criteria rather than assumptions: a wrong or
stale hook `trusted_hash` degrades in complete silence; the project-doc byte
budget is shared across the walk chain, drains root-first, and truncates
silently; and an empty context file claims its directory's slot and suppresses
every remaining candidate.
