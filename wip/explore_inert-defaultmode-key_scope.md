# Explore Scope: inert-defaultmode-key

## Visibility

Public

## Execution Mode

Auto. This exploration runs in a dispatched background session with no
interactive author on the other end, so decision points follow the
research-first protocol: gather evidence, follow the recommendation, record
the decision. Max rounds: 3. Decisions accumulate in
`wip/explore_inert-defaultmode-key_decisions.md`.

## Core Question

niwa's materializer writes `permissions.defaultMode` into every instance's
`.claude/settings.json`. Claude Code no longer honors the permissive values of
that key from project scope, so the written value does not govern the session
it appears to govern -- but niwa's own dispatch flow reads the same key back to
decide whether to forward `--permission-mode` on the worker command line. The
key is dead as a Claude Code setting and load-bearing as a niwa-internal
signal. What is the right shape for that signal, and what is the smallest
change that stops the settings document from making a claim it cannot keep?

## Context

A prior session lost time to the false premise that the materialized key was
the mechanism setting the session's posture. It is not: the honored channel is
the `--permission-mode` CLI flag that dispatch derives from it, recorded in
`docs/designs/current/DESIGN-dispatch-permission-mode.md` and covered by a
`@critical` scenario in `test/functional/features/dispatch.feature`.

The producer is `buildSettingsDoc` in `internal/workspace/materialize.go`,
whose mapping accepts only `"bypass"` and `"ask"`. There are at least two
readers: the dispatch derivation (via `readInstanceSettings` in
`internal/cli/dispatch_plugins.go`) and `WorkerPermissionMode` in
`internal/workspace/permissions.go`, whose doc comment still speaks of "the
coordinator" -- vocabulary from the mesh that niwa removed. Whether anything
still calls it is open.

A neighbouring constraint bounds one of the candidate shapes: there is exactly
one `--settings` argv slot and remote control already occupies it
(`internal/cli/dispatch_remotecontrol.go`), and the repo has twice declined to
pass the flag twice because repeated-flag behavior is undocumented. A sibling
session is working the same constraint from a different direction.

## In Scope

- Verifying the Claude Code scope rule and the version it changed at, against
  current documentation rather than the brief's summary
- Every producer and consumer of the materialized `permissions.defaultMode`
  key inside niwa, including dead readers
- Candidate shapes for the internal signal: annotate in place, rename, move to
  niwa's own instance metadata, or move into the `--settings` payload
- Whether `niwa apply`, drift detection, or any verification pass depends on
  the key being present
- Whether this problem strengthens the case for a single merged
  settings-document builder (state the conclusion; do not build it)
- The smallest change that removes the false signal without breaking the
  derivation or its `@critical` scenario

## Out of Scope

- The peer-message pre-approval work; a sibling session owns it
- Changing niwa's actual permission posture, or what a workspace may declare
- Building the merged settings-document builder

## Research Leads

1. **Which Claude Code settings scopes honor `permissions.defaultMode`, for
   which modes, at which versions?** (lead-claude-code-scope-rule)
   Everything downstream rests on this. The brief says the two permissive
   modes stopped taking effect from project and local scope as of 2.1.257 and
   that user scope still honors them. That claim needs a citation to current
   documentation or release notes, not a restatement. The answer also has to
   cover the `--settings` inline document and the `--permission-mode` flag,
   since those are the channels niwa actually relies on.

2. **What is the complete producer/consumer graph for the materialized key
   inside niwa today?** (lead-producer-consumer-graph)
   The brief names four files but warns its line numbers have drifted. Trace
   every write and every read: the materializer's mapping, the root
   materializer, the dispatch derivation, `WorkerPermissionMode`, and anything
   in `internal/agentplan` or `internal/watch` that touches the same value.
   For each reader, say whether it is reachable from a real command path or is
   dead code left by the mesh removal.

3. **Does anything downstream depend on the key's presence in the written
   document -- apply, drift, verification, or tests?** (lead-drift-and-tests)
   If a later `niwa apply` compares the materialized document against what it
   would write now, or a functional scenario asserts the key's presence,
   moving or renaming it costs more than the derivation. This lead sets the
   blast radius of every candidate shape.

4. **How does niwa deliver an inline `--settings` document today, and would
   `defaultMode` be honored from it?** (lead-settings-payload-channel)
   Read `internal/cli/dispatch_remotecontrol.go` for how the payload is built
   and passed. Establish whether the inline document counts as a scope that
   honors the permissive modes, whether the single argv slot is a hard
   constraint or a convention, and what the repo's two prior declines actually
   argued. This is the lead that decides whether "move it where it would be
   honored" is even on the table.

5. **What does niwa's own instance metadata look like, and could the signal
   live there?** (lead-instance-metadata-home)
   niwa already writes state it owns -- `instance.json` and whatever else sits
   under the instance's `.niwa/`. If the derivation's input moved there, the
   settings document would stop making a claim about Claude Code behavior
   entirely. Establish what that store is, who reads it, and what a new field
   in it would cost.

6. **How have comparable projects handled a setting that a tool stopped
   honoring but their own code still reads?** (lead-prior-art-inert-keys)
   Generated-configuration tools hit this whenever an upstream tool changes
   its contract. Worth knowing whether the common resolution is annotation, a
   separate sidecar, or a hard removal with the signal recomputed from source
   -- and whether any of those has a failure mode niwa would inherit.
