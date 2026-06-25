# Explore Scope: ephemeral-session-fixes

## Visibility

Public

## Core Question

What does fixing #171 and #172 look like — and are they two independent patches
or two symptoms of one flaw in the "cd + inject" activation model? #171 is about
*which* sessions get an instance (the guard reads the wrong signal). #172 is about
whether having an instance even helps (a mid-session `cd` can't re-root, so the
instance's settings.json/plugins/hooks/env never load). A single redesign —
relaunching the worker rooted inside the instance — could resolve both, so the
load-bearing question is whether that is reachable from a hook.

## Context

- Both issues are post-merge bugs in the ephemeral-session-instances feature
  (PR #169, merged 2026-06-22). Filed 2026-06-25, currently OPEN and unlabeled.
- #171: SessionStart guard gates on job-state `template == "bg"`; `template` is the
  launch agent/profile (`--agent <x>`), not a fg/bg flag. A background session
  dispatched with the default agent carries `template: "claude"` and is silently
  skipped. Reliable signal `sessionKind: "bg"` lives in the transcript, not job state.
- #172: root-launched session told to `cd` into its instance never loads the
  instance's plugins/skills/settings hooks/env (Claude resolves settings.json at
  launch from the launch dir). Root scaffold `writeRootSettings` also drops
  `Plugins`/`Marketplaces`.
- Design Decision 3 (guard) and Decision 4 (cd+inject) are the two soft spots the
  DESIGN itself flagged; these issues are those soft spots failing in practice.

## In Scope

- The coordinator-vs-worker guard signal (#171)
- The activation / config-delivery model (#172)
- The root scaffold's dropped Plugins/Marketplaces fields
- How the two fixes interact and their dependency order

## Out of Scope

- The reaper, mapping store, `--no-cascade` apply semantics (working as designed)

## Research Leads

1. **What signals are reliably available at SessionStart to distinguish a
   dispatched background worker from an interactive session?**
   The crux of #171. Need to know what the hook payload, job state, transcript
   (`sessionKind`), and environment actually carry at hook fire time, and whether
   the reliable signal is readable without a race.

2. **Can a SessionStart hook re-root or relaunch a session into the instance
   directory?**
   Decides whether #172 Option B (the potentially unifying fix) is feasible at all,
   or whether cd+inject is the ceiling of what a hook can do. Determines if both
   issues collapse into one redesign.

3. **What does the root scaffold emit vs. the instance materializer, and which
   fields hoist cleanly to the root?**
   #172 Option A. Locate the forwarding gap in `writeRootSettings`; classify
   marketplace sources (github vs. instance-relative `directory`/`path`) by whether
   they have a root-stable form.

4. **What is the real cost and correctness of #171 Option B (provision for any
   root session)?**
   Dropping the `template` check and relying on master-switch + re-entrancy. How
   often are interactive sessions opened at the root, and what does a throwaway
   clone+reap cost them? This is the option Decision 3 originally rejected.

5. **Is there one unifying fix or two independent patches, and what is the
   dependency order?**
   How #171 and #172 interact; whether relaunch-in-instance moots cd+inject and
   changes the guard calculus; what the minimal-correct vs. ideal end states are.
