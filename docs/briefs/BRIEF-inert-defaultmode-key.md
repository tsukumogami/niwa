---
schema: brief/v1
status: Draft
problem: |
  A developer who declares how much a workspace's sessions may do without
  asking can't rely on that declaration reaching the sessions niwa launches,
  and the settings niwa generates quietly replace the developer's own
  permission posture with a more restrictive one while displaying a setting
  that looks like it governs the session and doesn't.
outcome: |
  The posture a developer declares reaches every session niwa launches for
  them, their own personal permission settings keep working inside
  niwa-managed sessions, and anything a developer or an agent reads in a
  generated settings file about permissions is true.
---

# BRIEF: inert-defaultmode-key

## Status

Draft

## Problem Statement

A workspace can declare how much its sessions may do before stopping to ask
the developer. The common reason to declare it is unattended work: a developer
dispatches a worker to run in its own instance and doesn't want it halting on
every tool call with nobody there to approve. niwa turns that declaration into
a permission setting inside each instance's generated settings files.

Claude Code stopped honoring the most permissive values of that setting when
they come from a project's own settings files. From 2.1.257 onward, the value
niwa writes for an unattended workspace has no effect on the session reading
it. The developer's declaration doesn't reach the session through that file.

What makes this worse than a no-op is how the ignored value behaves. It isn't
skipped over in favor of the next settings layer down. It takes precedence over
the developer's own user-level settings and is then reset to the most
restrictive default. A developer who has configured their own sessions to
accept edits without prompting finds that every niwa-managed session prompts
anyway. The workspace asked for fewer prompts, and the developer gets more of
them than they would if niwa had written nothing at all.

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

A developer who declares an unattended posture for a workspace gets it in every
session niwa launches for them, by a route that takes effect. They don't have
to know which launch path was used, and they don't have to add a flag by hand
to get what the workspace already says.

A developer who has their own permission preferences keeps them inside
niwa-managed sessions. Opening a session in an instance behaves the way opening
one anywhere else does, and the workspace's declaration no longer quietly
overrides a posture the developer set up for themselves.

When a developer or an agent opens an instance's generated settings to work out
why a session did or didn't prompt, what they find is accurate. No setting in
the file claims to govern a posture it doesn't govern, so the investigation
starts from what's true rather than from a plausible wrong answer.

An operator who runs supervised sessions, where a person approves actions as
they happen, keeps that arrangement exactly as it works today.

## User Journeys

<Phase 3 will name the concrete journeys that exercise the feature.>

## Scope Boundary

<Phase 3 will record what this feature holds in and pushes out.>
