# Plan Analysis: DESIGN-inert-defaultmode-key

## Source Document
Path: docs/designs/DESIGN-inert-defaultmode-key.md
Status: Accepted (transitioned to Planned under the `/scope` sentinel)
Input Type: design

## Scope Summary
Stop niwa writing Claude Code permission modes that don't take effect
(`bypassPermissions`) or void the whole settings file (`askPermissions`), and
move `niwa dispatch`'s `--permission-mode` derivation off the materialized
settings file onto a posture niwa records in its own instance state.

## Components Identified
- Instance posture resolver and persisted field: `instancePermissionsPosture`,
  `InstanceState.ClaudePermissions`, the `pipelineResult` carry, and the
  Create/Apply copy (`internal/workspace`).
- Dispatch derivation and argv builder: `derivePermissionMode`, the state load
  in `runDispatch`, `buildDispatchPassthrough` taking the permission mode as a
  parameter, and both watch call sites passing `""` (`internal/cli`).
- Dead-reader removal: the `Permissions` projection on `instanceSettings` and
  `internal/workspace/permissions.go`.
- Posture mapping: `claudeDefaultMode` replacing `permissionsMapping`, and
  secret-safe errors for `permissions` and the two sibling boolean keys
  (`internal/workspace/materialize.go`).
- End-to-end coverage: dispatch argv scenarios, the S1-S9 document-matrix
  Scenario Outline, and the new settings-file step (`test/functional`).
- Documentation: the seven committed documents the PRD names.

## Implementation Phases (from design)

Phase 1: Record the posture in instance state. Add the resolver, the field,
the carry, and the Create/Apply copy. No generated settings document changes.

Phase 2: Move the derivation onto instance state. Add the pure derivation and
the state load; make the builder take the permission mode; update the dispatch,
both watch, and the test call sites; remove the `Permissions` projection and
delete `WorkerPermissionMode`. Replace the seven fixture-based tests with
declaration-driven ones; add the tamper, watch argv, re-entry, and static
single-reader checks; give the fake provisioners a minimal state file.

Phase 3: Change what the materializer writes. Replace the mapping with
`claudeDefaultMode`; make the three settings errors secret-safe; flip the tests
that asserted the retired values; add the differential, re-apply, agreement,
invalid-value, parse, and watch-on-materialization tests; regenerate goldens;
give the `workspace-config-sources` `@critical` scenario its instance-state
observable.

Phase 4: End-to-end coverage. Dispatch scenarios S2-S9, explicit-flag cases,
remote control, Codex, personal overlay; the S1-S9 matrix Scenario Outline.

Phase 5: Documentation. Correct the seven documents, including the
`dispatch.feature` comment and the `2.1.258` -> `2.1.257` correction.

## Success Metrics
From the PRD's acceptance criteria (AC1-AC21) and the design's Consequences:
- A bypass dispatch carries `--permission-mode bypassPermissions` from every
  dispatch form; an explicit flag wins and is the only one on the argv.
- No generated document carries `bypassPermissions`, `auto`, or
  `askPermissions`; `ask` writes `default`; every value present is in the
  recognized set.
- No settings file can grant or withhold bypass; watch launches carry no
  derived flag.
- Re-apply repairs existing documents; `go test ./...` and
  `make test-functional-critical` pass.

## External Dependencies
- The upstream PRD (`docs/prds/PRD-inert-defaultmode-key.md`): its
  acceptance criteria and S1-S9 expected-value table are the test oracle.
- The existing functional harness steps: fake `claude` argv recording and
  `a personal overlay exists with body`.
- Claude Code 2.1.257+ behavior for project-scope `defaultMode`, measured on
  2.1.267.
