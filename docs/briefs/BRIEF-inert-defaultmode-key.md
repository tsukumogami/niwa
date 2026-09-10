---
schema: brief/v1
status: Accepted
problem: |
  A developer's declared posture doesn't reliably reach niwa's sessions: the
  generated settings override the developer's own posture with a stricter one,
  the asking value was never one Claude Code recognizes, and the file shows a
  permissions setting that governs nothing.
outcome: |
  The posture a developer declares reaches the sessions niwa starts for them,
  their own personal permission settings keep working in sessions they open
  inside niwa-managed instances, and anything a developer or an agent reads in
  a generated settings file about permissions is true.
---

# BRIEF: inert-defaultmode-key

## Status

Accepted

## Problem Statement

A developer who tells niwa a workspace's sessions should run unattended gets
sessions that stop and ask anyway, and more often than if niwa had done nothing
at all. The common reason to declare an unattended posture is background work:
a developer dispatches a worker to run in its own instance and doesn't want it
halting on every tool call with nobody there to approve. niwa turns that
declaration into a permission setting inside each instance's generated settings
files.

Claude Code stopped honoring the most permissive values of that setting when
they come from a project's own settings files. From 2.1.257 onward, the value
niwa writes for an unattended workspace has no effect on the session reading
it. The developer's declaration doesn't reach the session through that file.

What makes this worse than a no-op is how the ignored value behaves. It isn't
skipped over in favor of the next settings layer down. It takes precedence over
the developer's own user-level settings and is then reset to the most
restrictive default. A developer who has configured their own sessions to
accept edits without prompting finds that every session they open in a
niwa-managed instance prompts anyway. The workspace asked for fewer prompts,
and the developer gets more of them.

The generated file hides all of this. It shows a permissions block that reads
as the thing deciding the session's posture. Someone trying to understand why a
session prompted, whether a person or an agent, opens that file, finds a
confident answer, and starts from a false premise. niwa's own dispatch flow
separately arranges for unattended workers to get the posture through a path
that does take effect, so the declaration works in one place and fails in
others, and nothing on disk says which.

A second declared value, the one meant for sessions that should keep asking,
has never produced a setting Claude Code recognizes. Workspaces that chose it
have been getting defaults the whole time.

## User Outcome

A developer who declares an unattended posture for a workspace gets it in the
sessions niwa starts for them, by a route that takes effect. They don't have to
add a flag by hand to get what the workspace already says.

A developer who has their own permission preferences keeps them in sessions
they open themselves inside a niwa-managed instance. The workspace's
declaration governs the sessions niwa starts on the developer's behalf; it
doesn't reach into an interactive session the developer opened, and it no
longer quietly overrides a posture the developer set up for themselves.

When a developer or an agent opens an instance's generated settings to work out
why a session did or didn't prompt, what they find is accurate. No setting in
the file claims to govern a posture it doesn't govern, so the investigation
starts from what's true rather than from a plausible wrong answer.

An operator who runs supervised sessions, where a person approves actions as
they happen, keeps that arrangement exactly as it works today.

## User Journeys

### Journey 1: Maintainer dispatching an unattended worker

A workspace maintainer has declared the unattended posture because the
workspace's work is routinely handed to background workers. They run
`niwa dispatch` with a task and walk away, typing no permission flag of their
own. The worker runs to completion without stopping to ask for approval,
because the workspace already said it shouldn't. If the maintainer later opens
the worker's instance, nothing in it claims the posture came from somewhere it
didn't.

### Journey 2: Developer opening a session in an instance

A developer's own user-level settings put their sessions in a mode that accepts
edits without prompting. They open an ordinary interactive session inside a
niwa instance of a workspace that declared the unattended posture. They aren't
dispatching anything, so niwa isn't starting this session on their behalf.
Today the session prompts on every edit, more restrictive than their own setup.
After this feature the session behaves the way their own settings say, the same
as it would outside niwa.

### Journey 3: Investigating an unexpected prompt

A developer, or an agent working on their behalf, notices a session asked for
approval when they expected it not to, or didn't ask when they expected it to.
They open the instance's generated settings to find out why. Every
permission-related value they find there actually takes effect, so the
investigation leads to the real cause instead of stopping at a confident-looking
setting that explains nothing.

### Journey 4: Workspace that declared it wants sessions to ask

A maintainer declared the asking posture for a workspace that handles
sensitive operations, meaning to keep a person in the loop. They expect their
sessions to ask before acting. Today that declaration produces a value Claude
Code doesn't recognize, so sessions run on whatever defaults apply and the
generated file suggests otherwise. After this feature the declaration no longer
produces a value Claude Code ignores, and the generated file no longer claims a
posture it doesn't deliver.

### Journey 5: Operator running a supervised review

An operator runs niwa's supervised review, where a person approves each
consequential action as it happens and the review writes its own
approval-gated posture into the instance. They start a review after this
feature ships. The review asks for approval exactly as it did before, because
the feature leaves the posture that arrangement relies on untouched.

## Scope Boundary

### In scope

- What niwa writes about permission posture into each settings document it
  generates: the instance root, each repo inside an instance, and the
  workspace root. This covers every value a workspace can declare today.
- Where niwa's dispatch flow reads a workspace's declared posture from when it
  decides how a worker should run.
- What the existing asking declaration produces, since the value it produces
  today has never been one Claude Code recognizes.
- The committed niwa documentation that describes the generated setting as the
  mechanism that sets a session's posture.

### Out of scope

- **Changing which postures a workspace may declare, or how it declares
  them.** The declaration key and its accepted names stay as they are.
  Renaming or extending them is a compatibility change for every existing
  workspace and a separate decision.
- **Applying the declared posture to interactive sessions a developer opens
  themselves.** The declaration governs sessions niwa starts. A developer who
  opens their own session gets their own settings, as Journey 2 describes.
- **The supervised review's own posture.** It writes a restrictive value that
  Claude Code honors from the same file, and it works today. This feature must
  not change it, so its correctness isn't this feature's to redesign.
- **Moving remote control off the single inline-settings argument.** This
  feature doesn't need that argument slot freed.
- **A shared builder for the inline settings document niwa passes to a
  worker.** This feature puts nothing new in that document.
- **Containment for unattended workers**, meaning limits on file writes and
  network access for a worker that doesn't ask. That gap exists with or
  without this feature, and
  `docs/designs/current/DESIGN-dispatch-permission-mode.md` already names it as
  a separate follow-up.
- **Other tools that independently write the same permission setting into a
  repo's local settings.** niwa can only change what niwa writes.
- **The posture niwa generates for other agent harnesses.** Those use separate
  keys, a separate vocabulary, and a separate trust mechanism, and none of them
  is affected by the change described here.

## References

- `docs/designs/current/DESIGN-dispatch-permission-mode.md`: the design that
  added the dispatch-time route for the unattended posture, which this feature
  builds on.
- `docs/prds/PRD-config-distribution.md`: where the mapping from a workspace's
  declared posture to a generated setting was first specified.
- Claude Code 2.1.257 release notes
  (https://github.com/anthropics/claude-code/releases/tag/v2.1.257): the change
  that stopped project-scope settings from setting the bypass permission mode.
