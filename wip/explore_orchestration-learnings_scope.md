# Explore Scope: orchestration-learnings

## Visibility

Public

## Core Question

A coordinator session dispatched 17 workers, reviewed 10 PRs, and merged 16. It
established verifiable facts about how `claude` background sessions behave, evolved a
dispatch-brief format that measurably changed worker output, and made several repeatable
mistakes. None of it is recorded durably. What artifacts, in which loaded paths, keep
that usable in three months?

## Context

Author decisions from the Phase 1 conversation:

- **Home repo is `niwa` (public).** niwa launches the background sessions, owns the
  `/dispatch` root skill, and already documents session lifecycle under `docs/guides/`.
- **`_common.md` ships as a niwa template but stays overrideable/extensible**, in the same
  spirit as the shirabe extension mechanism (`<skill>.md` + `<skill>.local.md`).
- **Skill shape is open** — the author asked for a researched recommendation, not a menu.
- **Reviews get the full treatment** — a durable review standard, not just scattered rules.

Facts established before scoping:

- `niwa` already ships one root skill (`internal/workspace/rootskills/dispatch/SKILL.md`)
  into `<workspace-root>/.claude/skills/dispatch/SKILL.md`. It covers "the worker starts
  blind", the work-in-flight block, and `--detach`. It says nothing about launch mode,
  chain shape, or a standing working agreement.
- `.niwa/dispatch-briefs/` is deliberately niwa-local runtime state:
  `internal/workspace/snapshotwriter.go` *preserves* it across an apply so a refresh never
  clobbers it, which also means nothing ever seeds it from source. `_common.md` therefore
  exists on exactly one machine.
- The coordinator reads `/dispatch`; the worker reads `_common.md`. Two different load
  points, and collapsing them loses one.
- Chain-shape rationale is recoverable: every brief's `## Required workflow` section states
  its reason. A third shape appears twice that the task brief did not list — `work-on`
  escalating to `decision` on one named question.

## In Scope

- Harness mechanics reference: `claude --resume`, `stop`, `agents`, `--fork-session`,
  `--from-pr`, each finding carrying the command that verifies it, with the three known
  corrections applied.
- Dispatch authoring: launch mode (autonomous vs interactive-first), chain shape, brief
  skeleton, sibling-collision naming, cost of waking a session.
- The standing working agreement (`_common.md`) as a durable, seeded, overrideable file.
- Review standard: the "do not trust the PR body" discipline and the coordinator-side
  rules it depends on (stranded-work sweep, monitor seeding, no unverified-state claims).

## Out of Scope

- tsuku code changes; the open issues from that session; merged PRs and closed issues.
- Anything depending on a path under a `~/.claude/jobs/` scratch directory.

## Research Leads

1. **What mechanism can niwa use to seed a file into `.niwa/dispatch-briefs/` without
   breaking the preservation guarantee, and how do root skills get installed?**
   Determines whether a shipped-plus-overrideable `_common.md` is possible at all, and
   what the code change costs. (agent: lead-niwa-distribution)

2. **How does the shirabe extension layering actually work — who reads
   `.claude/shirabe-extensions/<skill>.md` and `<skill>.local.md`, in what precedence, and
   how does niwa put them there?**
   The author named this as the model for `_common.md` overrides, so the mechanism has to
   be understood before it is imitated. (agent: lead-extension-layering)

3. **Are launch mode and chain shape recoverable as criteria from the briefs plus the
   merged-PR outcomes, rather than as intuition?**
   The stated reasons are in the briefs and the outcomes are in the PRs, so the
   correlation is checkable. (agent: lead-chain-shape)

4. **Which claims in `resume-findings.md` still hold against the installed `claude`, and
   what command proves each one today?**
   The reference doc's whole value is re-verifiability; findings already stale must be
   corrected rather than preserved. (direct, not delegated)

5. **What load-bearing facts exist only in the coordinator transcript?**
   The four unverified-state assertions, the stalled status agents, the #2513 sequence,
   the monitor-seeding failure, the cost figure. (direct, not delegated)

6. **What shape should the process/judgment artifacts take, given that `/dispatch` is
   already a loaded path?**
   Synthesised in convergence from leads 1-3, not delegated: this is the decision the
   author asked for a recommendation on.

### Note on delegation

Leads 4 and 5 are run directly rather than by agents. The session being studied lost four
consecutive status-gathering agents to stalls while direct `gh` and `jq` queries answered
the same questions in under a minute. Applying that finding here is deliberate.
