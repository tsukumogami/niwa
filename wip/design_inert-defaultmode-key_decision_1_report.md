# Decision 1: Where the dispatch derivation gets the instance's effective posture

resolution: inline (Decision-bypass-with-inline-resolution, parent `/scope`)
complexity: critical
status: complete
chosen: B -- persist the resolved instance posture in niwa's instance state at materialization; dispatch reads it from there
confidence: high

## Question

Where does the derivation in `runDispatch` read the instance's effective
permission posture, so that no generated settings document can grant or
withhold bypass, every dispatch form carries the flag, and `niwa watch`'s
launches never do?

## Constraints (from the Decision Drivers)

- The input is the instance's effective declared posture: workspace overlay,
  workspace, personal overlay, then `[instance.claude.settings]`. A tampered
  instance-root document can't grant bypass, and a deleted one can't withhold
  it (PRD R3, AC7).
- Derivation tests take the posture from a real declaration through real
  materialization, not from a fake (R17).
- `niwa watch` shares `buildDispatchPassthrough` and must never receive a
  derived flag (R12).
- Values under `[claude.settings]` may be `vault://`-backed and resolve only
  during materialization.

## Options

**A. Thread the resolved posture out through the provisioning result.** Add a
posture field to `provisionResult`, fill it from the Applier's effective
config, and derive from it in `runDispatch`.
- For: no new persisted state; the value never touches disk.
- Against: the Applier would have to return a resolved-config fact out of
  `Create`, which today returns nothing of the kind. Every fake
  `provisionInstanceFunc` in the dispatch tests constructs `provisionResult`
  by hand, so a posture field on it would be set by the fake. That's the
  hand-written-input pattern R17 forbids, moved one layer up. The derivation
  also couldn't be exercised against an already-materialized instance, which
  is exactly the shape AC7 describes.

**B. Persist the resolved instance posture in `InstanceState` and read it at
dispatch (chosen).** At materialization, record the declared value the
instance-root document resolved from (`bypass`, `ask`, or absent) in an
additive `omitempty` field of `InstanceState`, which is written to
`.niwa/instance.json`. Dispatch reads it through the existing state loader.
- For: `instance.json` is niwa's own document, not a Claude Code settings
  file. Deleting or rewriting the instance-root `settings.json` doesn't touch
  it, so both AC7 cases hold by construction. The pipeline already carries
  facts into `InstanceState` this way (`shadows`, `authSources`, `trustKeys`
  are persisted from `pipelineResult` by Create and Apply), and
  `ephemeralSessionMode` already treats `instance.json` as the authority over
  a copy in a settings document. The derivation becomes a function of the
  instance path that a unit test can run against a real materialization. It
  records the value after vault resolution, which only materialization can
  do. And resume, or any later reader, can consult the same record.
- Against: one new field on persisted state. Instances written by an older
  niwa lack it, but dispatch always provisions a fresh instance, so the field
  is always present where the derivation reads it. The state file becomes a
  grant source, which is the same trust boundary as `workspace.toml`: both are
  local files under the operator's own control plane.

**C. Re-resolve the effective config at dispatch time.** Load `workspace.toml`,
the workspace overlay, and the personal overlay again in `runDispatch`, apply
instance overrides, and read the posture.
- Against: it duplicates the precedence rules in a second place that can drift
  from the materializer's. It re-runs overlay discovery and vault resolution
  on the dispatch path. And it re-derives a value the pipeline already
  computed for the same instance moments earlier.

**D. Keep a file-borne signal under a niwa-owned key in the instance-root
settings document**, following the `keepAliveOnDispatch` pattern.
- Against: this fails R3 outright. A tampered key grants bypass, and a deleted
  document withholds it (both AC7 cases). It also keeps a generated Claude
  Code settings file in the decision path, which is the coupling this feature
  removes.

## Rationale

B is the only option under which both tamper cases hold by construction and
the derivation stays testable from a real declaration. It follows a pattern
the pipeline already uses for the same kind of fact. A fails R17's intent at
the fake-provisioner layer. C duplicates resolution logic and vault access. D
fails R3.

## Assumptions

- The instance-root document's posture is available as a resolved value
  during materialization (the root settings materializer already computes it
  from the merged config).
- `InstanceState` accepts an additive `omitempty` field without a schema
  migration (the research found no migration functions and additive fields in
  current use).

## Rejected

A (fake-provisioner coupling; untestable against a materialized instance),
C (duplicated precedence and vault resolution), D (fails R3's tamper cases).
