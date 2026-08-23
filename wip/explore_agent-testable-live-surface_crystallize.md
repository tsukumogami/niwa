# Crystallize: agent-testable live surface

## Decision

Two outcomes, split on kind rather than on topic. Decided by the author.

1. **Two GitHub issues** for the things that are defects in shipped code, each
   standing on its own evidence.
2. **One spike report** in `docs/spikes/` for the testing-infrastructure
   picture, written so an implementer can act on it without re-measuring.

Nothing is built in this session.

## Why this split rather than one artifact

The exploration produced two categories that do not review the same way. A
defect in how niwa installs and then shadows its own AppArmor profile is a
product bug with a reproduction and a fix; a proposal to add a contract test and
a loopback oracle is a design argument about coverage. Folding the first into
the second buries a live bug inside a proposal that may take a while to land, and
the workspace has a precedent for the opposite: incidental defects found during a
feature became their own issues rather than riding along.

## Issue 1 — niwa shadows its own AppArmor profile

`niwa setup-sandbox` grants the userns capability to
`~/.tsuku/tools/current/bwrap`, and niwa's own PATH puts a different bwrap ahead
of it, so the profile never applies to tools that resolve bwrap from PATH.
Reproduced and verified directly. niwa's `watch-live-egress.yml` already passes
the resolved path, so the fix is stated in the repo and not applied to the repo.

## Issue 2 — the functional suite's Codex discovery model disagrees with the binary

Three measured disagreements out of 26 fixtures, found by running the suite's own
unexported helpers against codex-cli 0.147.0: symlinked directories, unreadable
chain files, and missing budget truncation. The third can make `contains`
assertions pass on content no default-budget session sees.

## Spike report — the testing-infrastructure picture

Covers: what is observable credential-free and sandbox-free for each agent; the
corrected sandbox probe; the loopback-mock oracle for Claude; the
pending-reads-as-pass reporting gap and its bounded inventory; the CI situation
for both agents; and the contract test as the thing that would have caught issue
2. Written as findings plus recommendations, not as a design.

## Carried forward, not resolved

Output redaction (from L6) is a prerequisite for any credentialed agent run and
is not addressed by either outcome above. It is recorded in the spike report as
the condition on that path rather than filed, because after L3's result the
Claude scenario no longer needs a credential at all — which removes the only
concrete consumer that would have justified building it now.
