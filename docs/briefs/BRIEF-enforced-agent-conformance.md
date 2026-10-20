---
schema: brief/v1
status: Done
problem: |
  A session launched at a niwa instance root -- every dispatched and
  ephemeral worker -- can't isolate its work in a niwa worktree. It either
  stalls on a permission prompt that fails even after approval, or silently
  makes a worktree niwa can't see. The only correction today is prose the
  operator maintains, and agents don't reliably follow it.
outcome: |
  An operator dispatches work and walks away. The session lands its work in
  a niwa worktree of the right repo with no prompt, and an agent that reaches
  for the wrong route is stopped at the call and told what to do instead. No
  line in CLAUDE.md or dot-niwa is needed; niwa owns the behavior.
motivating_context: |
  A background session asked its operator for permission to enter a worktree
  it had built by hand inside a nested repo, then failed after approval.
  Tracing it showed the prompt was one symptom of a wider gap: niwa's
  worktree integration never reaches sessions launched at the instance root.
  The operator wants agent behavior enforced by mechanism rather than by
  instructions, and any instructions that remain delivered by niwa itself.
---

# BRIEF: enforced agent conformance

## Status

Done

Four framing details are left to the downstream PRD: which repo wins
when a worktree request names one and the session sits in another,
whether a path-form `EnterWorktree` call is rewritten or refused,
whether niwa's other repo-only hooks (PR-body, work-summary) also
belong at the instance root, and what happens to native worktrees
earlier sessions left on disk.

## Problem Statement

niwa launches every background worker at the root of a workspace
instance: the plain directory that holds the cloned repositories, not
any one of them. Ephemeral sessions end up there too. Claude Code reads
a session's project hooks from the directory it was launched in and
doesn't walk down into nested repos, and niwa writes its worktree
integration -- the `WorktreeCreate`/`WorktreeRemove` delegation to
`niwa worktree from-hook`, or the deny that replaces it on older
harnesses -- only into each repo's `.claude/settings.local.json`. The
instance root's settings, which niwa also generates, carry none of it.

So a root-launched session that tries to isolate its work does one of
two things, and both are wrong:

1. It runs `git worktree add` by hand inside a repo and calls
   `EnterWorktree` with that path. Claude Code raises a "permission-root
   relocation" safety prompt that no permission rule can pre-approve
   outside bypass mode. The operator, often away from the session,
   comes back to a stalled run. If they approve, the call fails anyway,
   because the session's directory isn't inside a git repository.
2. It changes directory into a repo and calls `EnterWorktree` by name.
   With no hook in scope, Claude Code takes its own git path and creates
   a native `<repo>/.claude/worktrees/<name>` checkout. That succeeds
   quietly, and niwa can't see it, list it, protect it, or clean it up.
   Every worktree found across the dispatched instances checked during
   the investigation was of this kind; none was a niwa worktree.

The operator's only lever today is prose: a line in a generated
CLAUDE.md, in `workspace-context.md`, or in dot-niwa content they
maintain themselves. Claude Code describes that kind of file as context
rather than enforced configuration, and the transcripts show agents
taking the hand-built route regardless. Each new failure invites another
line of instructions that the next agent may also ignore.

## User Outcome

An operator hands work to a background worker with `niwa dispatch`, or
opens an interactive session at the instance root, and doesn't have to
supervise how it isolates its work. The session ends up in a niwa
worktree of the repo it means to change -- visible to `niwa worktree`
commands and cleaned up on the normal path -- without a permission
prompt, whether or not the host runs with bypass permissions.

When an agent reaches for a route that can't work from the root, the
tool itself stops it at that call and says, in the refusal, which form
does work and which repos are available. The agent corrects itself in
the same turn; the operator never sees the mistake.

The operator maintains nothing to get this. No line in CLAUDE.md or
dot-niwa carries the rule. Whatever guidance an agent still needs comes
from niwa at session time, worded as fact, and survives compaction. A
maintainer reading niwa can point at the mechanism that enforces each
rule rather than at a paragraph that asks for it.

## User Journeys

### An operator dispatches a worker and walks away

An operator runs `niwa dispatch` on a task in one repo and leaves. The
worker boots at the instance root, asks for a worktree for that repo,
and gets a niwa worktree with no prompt. When the operator returns, the
work sits in a branch niwa tracks, and the worktree shows up in niwa's
own listing. Before this feature the same run either waited on an
approval the operator wasn't there to give, or left a native checkout
behind that niwa didn't know about.

### An interactive session at the root picks one repo out of many

A developer opens Claude Code at an instance root that holds ten repos
and asks for a change to one of them. The session names that repo when
it creates its worktree and lands inside it. If it asks without naming
a repo while sitting at the root, the refusal lists the repos and the
form that works, and the next attempt succeeds.

### An agent takes the hand-built route and is corrected at the call

An agent, following habit rather than any instruction, runs
`git worktree add` inside a nested repo, or hands `EnterWorktree` a
path. niwa stops the call before it reaches the prompt or the native
path, and the refusal names the supported form. The operator never sees
a permission prompt and no stray checkout is left behind.

### A maintainer traces a rule to its enforcement

A niwa maintainer, months later, wants to know why root sessions no
longer hit the worktree prompt and whether the old CLAUDE.md line about
it is still needed. They find the rule enforced by a hook niwa installs
and a resolution branch in `niwa worktree from-hook`, both covered by
tests, and find that the generated prose that used to carry the rule is
gone. The same pattern tells them where the next local-workflow rule
should live.

## Scope Boundary

**In:**

- niwa's worktree integration reaching sessions launched at the instance
  root, both the hook delegation and its deny fallback on unsupported
  harnesses.
- A way for a worktree request made at the bare instance root to resolve
  to the intended repo, or to fail with a refusal that lists the repos
  and the accepted form.
- Stopping the routes that can't work from the root -- `EnterWorktree`
  with a model-supplied path, and a hand-run `git worktree add` -- at the
  call, with the corrective guidance in the refusal.
- Moving the niwa-generated layout guidance that currently carries this
  rule as prose into niwa's own runtime channels (refusal text and
  session-start context that survives compaction), and removing it from
  the generated files.
- Setting this up as the pattern later niwa-owned guards follow.

**Out:**

- Changing where `niwa dispatch` starts its workers. Starting them inside
  the target repo was weighed and set aside: it needs a primary-repo
  choice, demotes sibling repos, and can't help sessions provisioned at
  the workspace root.
- Permission rules or bypass mode as the fix. Claude Code doesn't let a
  rule suppress the relocation prompt, and bypass mode only hides the
  prompt while leaving the native-worktree failure in place.
- Any new CLAUDE.md or dot-niwa prose as the fix.
- The wider set of prose rules that could become mechanisms: wip
  cleanup and references, edits to generated files, emoji and
  banned-word checks, koto state-aware guards. They reuse the pattern
  later as their own features.
- Changes to Claude Code itself, and to shirabe skills.
- The koto plugin's hook layout, which may not load at all; that's a
  separate bug.

## References

- docs/designs/current/DESIGN-niwa-default-worktree.md -- placed the
  worktree hook per repo after measuring that a parent's hook doesn't
  reach a session launched inside a child repo.
- docs/spikes/SPIKE-enterworktree-hook-bypass.md -- found that a hook at
  a non-git root fires but `niwa worktree from-hook` can't map the root
  to a repo.
- docs/spikes/SPIKE-niwa-default-worktree.md -- the settings-discovery
  measurements behind the per-repo placement.
- docs/designs/current/DESIGN-ephemeral-session-instances.md -- why a
  hook can't relocate a session's working directory.
