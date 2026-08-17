# Testability Review (serial-self-jury)

## Verdict: PASS

A test plan is derivable from the acceptance criteria alone; nearly every
criterion names its failing case.

## Untestable Criteria

None strictly untestable. Two process criteria (measurements before the
design fixes rows 4 and 12; spike contributions posted to the PR or
committed) verify against artifacts rather than code, which is acceptable
for requirements about process.

## Missing Test Coverage

1. R20 (Codex-side failures are loud) has no AC -- add a forced-failure
   criterion (delivery target unwritable => apply fails with a named error,
   not a warning).
2. R8 alias semantics (old key parses with warning; both-set errors) has no
   AC -- add one so the rename discipline is verifiable, not conventional.

## Summary

Structural criteria are exemplary: each names the mutation that must fail
(delete a declaration, fake an implementation, hand-edit the guide). Close
the two missing-AC gaps flagged by the completeness pass and the set is
fully verifiable.
