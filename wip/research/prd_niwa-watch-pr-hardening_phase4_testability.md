# Testability Review

## Verdict: PASS

A test plan can be written directly from the acceptance criteria: nearly every AC names a concrete input state, an observable output (stage/defer/continue/discard/count), and a verification, and the deferred-to-DESIGN mechanisms (live-idle vs live-busy, resume mechanism) are correctly abstracted as decision inputs so the logic stays pure/table-testable.

## Untestable Criteria

None are technically untestable. Two are weaker than the rest but still verifiable at the right level:

1. AC9 (R13, edge semantics): "a test demonstrates an edge-declared source is not subjected to coalescing." Because no edge consumer exists (explicitly out of scope), there is no concrete edge behavior to assert against; the only verifiable claim is that the coalesce/one-live-session branch is gated on the level-triggered declaration. -> Fix: state the assertion concretely, e.g. "an edge-declared source with two distinct events retains both events (the level source coalesces to one); assert event count differs by declaration."

2. AC6 (R8): "a test can resolve from it (a) liveness (b) idle-vs-busy (c) a continue target." Real resolution is integration-level; at unit level it is testable only with a fake session/instance lookup. This is acceptable and consistent with the stated level, but the AC should say the resolver is tested against a fake so the expectation is unambiguous.

## Missing Test Coverage

1. R16 has no acceptance criterion. R16 requires preserving/verifying multi-repo scope (`WorkspaceScope`) and the containment model (sandbox, PreToolUse hooks, post-guard). The Goals say "it verifies these," but no AC exercises that verification. -> Add an AC: existing scope/containment tests still pass and the hardening changes touch none of those surfaces.

2. Freshness happy-path (R9/R10) is untested. AC7 tests only the four discard conditions (closed/merged, no-longer-requested, force-pushed). A bug that always discards would still pass AC7. -> Add an AC: a staged review whose PR is still open, still requests the developer, and is at the dispatched head passes re-validation and presents as postable (nothing discarded).

3. Fail-loud half of R15 is untested. AC11 covers fail-closed (no PR recorded as handled at a SHA it did not stage, later run re-attempts) but not the "SHALL fail-loud / SHALL NOT silently look like nothing to stage" half. -> Add an AC that a transient failure surfaces an error rather than an empty/no-op result.

4. Cap-vs-per-run-bound interaction (R11) is not exercised. R11 says the total cap is "distinct from and additional to" the per-run bound, but no AC runs both bounds together (e.g. per-run bound below remaining cap capacity). Minor; add if the two interact in a non-obvious way.

## Summary

The ACs are strong: each of the three testable subsystems (re-dispatch decision, freshness re-validation, concurrency cap) has criteria that map to concrete table-test inputs and outputs, and the DESIGN-deferred mechanisms are abstracted as decision inputs rather than baked into the criteria, keeping them pure/unit-testable. The one requirement with no AC is R16 (verify-not-weaken of scope/containment); the notable coverage gaps are the freshness happy-path (AC7 only tests discards) and the fail-loud half of R15 (AC11 only tests fail-closed). All are additive fixes; none block a PASS.
